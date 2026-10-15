package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// YouTubePostExtractor reads a YouTube community post (text and images) from
// the ytInitialData JSON embedded in the post page. Unlike videos, post pages
// are served to the datacenter IP, so no browser is needed.
type YouTubePostExtractor struct {
	tempDir  string
	pageBase string
}

const (
	// youtubePostUserAgent is a desktop browser UA; YouTube serves the full
	// page (with ytInitialData) to it.
	youtubePostUserAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36"

	// youtubePostTimeout bounds the page fetch.
	youtubePostTimeout = 20 * time.Second

	// youtubePostMaxPage caps how much of the page is read.
	youtubePostMaxPage = 4 * 1024 * 1024

	// youtubePostMaxText caps a text-only post (Telegram messages allow 4096
	// characters; the original link is appended).
	youtubePostMaxText = 3500

	youtubePostFilePrefix = "youtube_post"
	youtubePostImageExt   = ".jpg"

	// youtubePostPageBase is where post pages are fetched from; m.youtube.com
	// links are fetched here too, since the mobile page embeds the data in a
	// different form.
	youtubePostPageBase = "https://www.youtube.com"

	// youtubeConsentCookie skips the EU cookie-consent interstitial that a
	// cookie-less request from an EU IP may be redirected to.
	youtubeConsentCookie = "SOCS=CAI"

	// youtubePostServiceName is the outage-tracker key of the post reader: a
	// changed page format or a consent wall fails every post.
	youtubePostServiceName = "youtube-post"
)

// errYouTubePostMissing marks a post absent from its page (deleted or
// private), which is not a reader failure.
var errYouTubePostMissing = errors.New("post not found in the page")

var (
	// youtubePostIDPattern captures the post id of a /post/<id> URL.
	youtubePostIDPattern = regexp.MustCompile(`youtube\.com/post/([A-Za-z0-9_-]+)`)

	// youtubeInitialDataPattern captures the ytInitialData JSON of a page.
	youtubeInitialDataPattern = regexp.MustCompile(`(?s)var ytInitialData = (\{.*?\});</script>`)
)

// youtubePost is the part of a backstagePostRenderer the bot uses.
type youtubePost struct {
	Author    string
	Text      string
	ImageURLs []string
}

// NewYouTubePostExtractor creates a community post extractor.
func NewYouTubePostExtractor(tempDir string) *YouTubePostExtractor {
	return &YouTubePostExtractor{tempDir: tempDir, pageBase: youtubePostPageBase}
}

// ExtractMedia returns the post's images (the text as caption), or the text
// alone as a text item when the post has no image.
func (e *YouTubePostExtractor) ExtractMedia(ctx context.Context, postURL string) ([]*MediaInfo, error) {
	var result []*MediaInfo
	var post youtubePost
	postID, err := parseYouTubePostID(postURL)
	isParsed := (err == nil)
	var page string

	if isParsed == true {
		pageURL := fmt.Sprintf("%s/post/%s", e.pageBase, postID)
		page, err = e.fetchPage(ctx, pageURL)
	}

	if err == nil {
		post, err = parseYouTubePost(page, postID)
	}

	if (err == nil) && (len(post.ImageURLs) > 0) {
		result, err = e.downloadImages(ctx, post, postID, postURL)
	}

	if (err == nil) && (len(post.ImageURLs) == 0) {
		result = youtubePostText(post, postURL)
	}

	isReaderFailure := (err != nil) && (isParsed == true) && (errors.Is(err, errYouTubePostMissing) == false) && (errors.Is(err, context.DeadlineExceeded) == false)

	if isReaderFailure == true {
		detail := err.Error()
		ReportServiceFailure(youtubePostServiceName, detail)
	}

	if err == nil {
		ReportServiceSuccess(youtubePostServiceName)
	}

	return result, err
}

// parseYouTubePostID returns the id of a /post/<id> URL.
func parseYouTubePostID(postURL string) (string, error) {
	result := ""
	err := fmt.Errorf("not a YouTube post URL: %s", postURL)
	matches := youtubePostIDPattern.FindStringSubmatch(postURL)

	if len(matches) == 2 {
		result = matches[1]
		err = nil
	}

	return result, err
}

// fetchPage downloads the post page.
func (e *YouTubePostExtractor) fetchPage(ctx context.Context, postURL string) (string, error) {
	headers := map[string]string{
		"User-Agent":      youtubePostUserAgent,
		"Accept-Language": "en",
		"Cookie":          youtubeConsentCookie,
	}
	result, err := fetchCappedPage(ctx, postURL, headers, youtubePostMaxPage, youtubePostTimeout)

	return result, err
}

// parseYouTubePost finds the post's backstagePostRenderer in ytInitialData.
func parseYouTubePost(page, postID string) (youtubePost, error) {
	result := youtubePost{}
	var data interface{}
	err := fmt.Errorf("no ytInitialData in the post page")
	matches := youtubeInitialDataPattern.FindStringSubmatch(page)

	if len(matches) == 2 {
		err = json.Unmarshal([]byte(matches[1]), &data)
	}

	renderer := map[string]interface{}(nil)

	if err == nil {
		renderer = findBackstagePost(data, postID)
	}

	if (err == nil) && (renderer == nil) {
		err = fmt.Errorf("%w: %s", errYouTubePostMissing, postID)
	}

	if err == nil {
		result = youtubePostFromRenderer(renderer)
	}

	return result, err
}

// findBackstagePost walks the JSON for the backstagePostRenderer of postID.
func findBackstagePost(node interface{}, postID string) map[string]interface{} {
	var result map[string]interface{}
	object, isObject := node.(map[string]interface{})
	list, isList := node.([]interface{})

	if isObject == true {
		result = findBackstagePostInMap(object, postID)
	}

	if isList == true {
		result = findBackstagePostInList(list, postID)
	}

	return result
}

// findBackstagePostInMap checks a JSON object and its children. A reshare
// (sharedPostRenderer) carries the requested id while the post it shares has
// its own; the shared original is returned for it.
func findBackstagePostInMap(node map[string]interface{}, postID string) map[string]interface{} {
	var result map[string]interface{}
	renderer, isRenderer := node["backstagePostRenderer"].(map[string]interface{})
	shared, isShared := node["sharedPostRenderer"].(map[string]interface{})

	if (isRenderer == true) && (renderer["postId"] == postID) {
		result = renderer
	}

	if (isShared == true) && (shared["postId"] == postID) {
		original, _ := shared["originalPost"].(map[string]interface{})
		result, _ = original["backstagePostRenderer"].(map[string]interface{})
	}

	for _, child := range node {
		if result == nil {
			result = findBackstagePost(child, postID)
		}
	}

	return result
}

// findBackstagePostInList checks a JSON array's items.
func findBackstagePostInList(node []interface{}, postID string) map[string]interface{} {
	var result map[string]interface{}

	for _, child := range node {
		if result == nil {
			result = findBackstagePost(child, postID)
		}
	}

	return result
}

// youtubePostFromRenderer reads author, text and image URLs.
func youtubePostFromRenderer(renderer map[string]interface{}) youtubePost {
	authorText, _ := renderer["authorText"].(map[string]interface{})
	contentText, _ := renderer["contentText"].(map[string]interface{})
	attachment, _ := renderer["backstageAttachment"].(map[string]interface{})
	result := youtubePost{
		Author:    joinYouTubeRuns(authorText),
		Text:      joinYouTubeRuns(contentText),
		ImageURLs: youtubePostImages(attachment),
	}

	return result
}

// joinYouTubeRuns concatenates the text of a {"runs":[{"text":...}]} node.
func joinYouTubeRuns(node map[string]interface{}) string {
	var text strings.Builder
	runs, _ := node["runs"].([]interface{})

	for _, run := range runs {
		runMap, _ := run.(map[string]interface{})
		part, _ := runMap["text"].(string)
		text.WriteString(part)
	}

	result := text.String()

	return result
}

// youtubePostImages returns the largest image of a single- or multi-image
// attachment (video and poll attachments have none).
func youtubePostImages(attachment map[string]interface{}) []string {
	var result []string
	single, hasSingle := attachment["backstageImageRenderer"].(map[string]interface{})
	multi, _ := attachment["postMultiImageRenderer"].(map[string]interface{})
	images, _ := multi["images"].([]interface{})

	if hasSingle == true {
		images = []interface{}{map[string]interface{}{"backstageImageRenderer": single}}
	}

	for _, image := range images {
		imageMap, _ := image.(map[string]interface{})
		imageRenderer, _ := imageMap["backstageImageRenderer"].(map[string]interface{})
		largest := largestYouTubeThumbnail(imageRenderer)

		if largest != "" {
			result = append(result, largest)
		}
	}

	return result
}

// largestYouTubeThumbnail returns the last (largest) thumbnail URL.
func largestYouTubeThumbnail(imageRenderer map[string]interface{}) string {
	result := ""
	image, _ := imageRenderer["image"].(map[string]interface{})
	thumbnails, _ := image["thumbnails"].([]interface{})

	for _, thumbnail := range thumbnails {
		thumbnailMap, _ := thumbnail.(map[string]interface{})
		url, _ := thumbnailMap["url"].(string)

		if url != "" {
			result = url
		}
	}

	return result
}

// downloadImages downloads the post's images; the first carries the caption.
func (e *YouTubePostExtractor) downloadImages(ctx context.Context, post youtubePost, postID, postURL string) ([]*MediaInfo, error) {
	var result []*MediaInfo
	var err error
	timestamp := time.Now().UnixNano()

	// Images download one by one; the loop stops once the chain's deadline
	// has passed, keeping what it already has.
	for i, imageURL := range post.ImageURLs {
		filename := fmt.Sprintf("%s_%s_%d_%d%s", youtubePostFilePrefix, postID, timestamp, (i + 1), youtubePostImageExt)
		filePath := filepath.Join(e.tempDir, filename)
		downloadErr := ctx.Err()

		if downloadErr == nil {
			downloadErr = downloadToFile(imageURL, filePath)
		}

		if downloadErr == nil {
			result = append(result, &MediaInfo{Type: MediaTypePhoto, URL: postURL, Filename: filePath})
		}
	}

	if len(result) == 0 {
		err = fmt.Errorf("no image of post %s could be downloaded", postID)
	}

	if err == nil {
		result[0].Caption = youtubePostCaption(post, 0)
		appendOriginalURL(result, postURL)
	}

	return result, err
}

// youtubePostText returns a text-only post as one text item (sent as a
// message with the original link).
func youtubePostText(post youtubePost, postURL string) []*MediaInfo {
	caption := youtubePostCaption(post, youtubePostMaxText)
	result := []*MediaInfo{{Type: MediaTypeText, URL: postURL, Caption: caption}}

	return result
}

// youtubePostCaption formats author and text (escaped for HTML), with the
// text cut to maxText bytes when maxText > 0.
func youtubePostCaption(post youtubePost, maxText int) string {
	text := post.Text

	if maxText > 0 {
		text = truncateAtRuneBoundary(text, maxText)
	}

	escapedAuthor := EscapeForTelegramHTML(post.Author)
	escapedText := EscapeForTelegramHTML(text)
	result := FormatAuthorCaption(youtubePlatform, escapedAuthor, "", escapedText)

	return result
}
