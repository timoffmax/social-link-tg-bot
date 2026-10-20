package services

import (
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"regexp"
	"time"
)

// InstagramOGExtractor is the last-resort Instagram fallback: it scrapes the Open
// Graph thumbnail and caption that instagram.com still serves to bot User-Agents
// (even from datacenter IPs) and returns the thumbnail as a photo. It never
// yields the actual video — only a preview image plus caption; a reel's
// thumbnail is marked as its cover, so FetchReelVideo tries for the video.
type InstagramOGExtractor struct {
	tempDir string
}

// ogScrapeUserAgent is a crawler UA that instagram.com answers with OG meta tags.
const ogScrapeUserAgent = "facebookexternalhit/1.1"

var (
	ogImagePattern = regexp.MustCompile(`<meta property="og:image" content="([^"]+)"`)
	ogTitlePattern = regexp.MustCompile(`<meta property="og:title" content="([^"]+)"`)
)

// NewInstagramOGExtractor creates a new OG preview extractor.
func NewInstagramOGExtractor(tempDir string) *InstagramOGExtractor {
	return &InstagramOGExtractor{tempDir: tempDir}
}

// ExtractMedia first asks Instagram whether the author gated the post: a gated
// post yields an InstagramGatedError (its og tags would be a login wall or an
// unrelated image). Otherwise it downloads the og:image thumbnail as a photo,
// marked as the video's cover for a reel or IGTV post (see markReelCover).
func (e *InstagramOGExtractor) ExtractMedia(instagramURL string) ([]*MediaInfo, error) {
	var result []*MediaInfo
	var err error
	imageURL := ""
	gate := CheckInstagramGate(instagramURL)

	if gate.Restricted == true {
		err = &InstagramGatedError{Gate: gate}
	}

	if gate.Restricted == false {
		result, imageURL, err = e.fetchPreview(instagramURL, false)
	}

	if err == nil {
		markReelCover(result, instagramURL, imageURL)
	}

	return result, err
}

// markReelCover flags the thumbnail of a reel or IGTV post as the cover of a
// video, so FetchReelVideo fetches the video itself (a costed special
// download). Only post media on the CDN counts: a login wall or logo is no
// cover. The gated-post preview is left alone, its paid fetch already ran.
func markReelCover(media []*MediaInfo, instagramURL, imageURL string) {
	postType, shortcode := instagramPostPath(instagramURL)
	isCover := isInstagramVideoPostType(postType) && isInstagramMediaCDNURL(imageURL)

	if isCover == true {
		log.Printf("🎬 [OG] Got only the cover of %s %s", postType, shortcode)
	}

	for _, item := range media {
		item.VideoCover = isCover
	}
}

// extractPreview scrapes the og:image thumbnail and caption and downloads the
// thumbnail as a photo. With requireMediaCDN, an og:image that is not on
// Instagram's media CDN (a login wall or logo) is rejected.
func (e *InstagramOGExtractor) extractPreview(instagramURL string, requireMediaCDN bool) ([]*MediaInfo, error) {
	result, _, err := e.fetchPreview(instagramURL, requireMediaCDN)

	return result, err
}

// fetchPreview is extractPreview that also returns the og:image URL.
func (e *InstagramOGExtractor) fetchPreview(instagramURL string, requireMediaCDN bool) ([]*MediaInfo, string, error) {
	var result []*MediaInfo
	imageURL := ""
	htmlBody, err := e.fetchHTML(instagramURL)

	if err == nil {
		rawImageURL := firstSubmatch(ogImagePattern, htmlBody)
		imageURL = html.UnescapeString(rawImageURL)
	}

	if (err == nil) && (imageURL == "") {
		err = fmt.Errorf("no og:image found")
	}

	if (err == nil) && (requireMediaCDN == true) && (isInstagramMediaCDNURL(imageURL) == false) {
		err = fmt.Errorf("og:image is not post media: %s", imageURL)
	}

	if err == nil {
		result, err = e.downloadPreview(instagramURL, imageURL, htmlBody)
	}

	return result, imageURL, err
}

// downloadPreview downloads the og:image and builds the photo with the
// og:title as caption.
func (e *InstagramOGExtractor) downloadPreview(instagramURL, imageURL, htmlBody string) ([]*MediaInfo, error) {
	var result []*MediaInfo
	rawTitle := firstSubmatch(ogTitlePattern, htmlBody)
	caption := html.UnescapeString(rawTitle)
	// Captions are sent with HTML parse mode: the unescaped title must be
	// escaped again, or a bare & or < gets the message rejected.
	escapedCaption := EscapeForTelegramHTML(caption)
	_, shortcode := instagramPostPath(instagramURL)
	timestamp := time.Now().Unix()
	filename := fmt.Sprintf("instagram_og_%s_%d.jpg", shortcode, timestamp)
	filePath := filepath.Join(e.tempDir, filename)
	err := downloadToFile(imageURL, filePath)

	if err != nil {
		err = fmt.Errorf("failed to download og:image: %w", err)
	}

	if err == nil {
		log.Printf("✅ [OG] Downloaded preview thumbnail for %s", instagramURL)

		authorCaption := FormatAuthorCaption("instagram", "", "", escapedCaption)
		result = []*MediaInfo{{
			Type:     MediaTypePhoto,
			URL:      instagramURL,
			Caption:  authorCaption,
			Filename: filePath,
		}}
	}

	return result, err
}

// fetchHTML retrieves the Instagram page HTML with a crawler User-Agent.
func (e *InstagramOGExtractor) fetchHTML(instagramURL string) (string, error) {
	req, err := http.NewRequest("GET", instagramURL, nil)

	if err != nil {
		return "", fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("User-Agent", ogScrapeUserAgent)

	resp, err := SharedHTTPClient.Do(req)

	if err != nil {
		return "", fmt.Errorf("request failed: %v", err)
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("bad status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))

	if err != nil {
		return "", fmt.Errorf("failed to read body: %v", err)
	}

	return string(body), nil
}

// instagramMediaCDNPattern matches post media on Instagram's CDN
// (scontent*.cdninstagram.com, instagram.*.fbcdn.net) under the /v/ and /o1/
// media paths; static.cdninstagram.com logos and icons don't match.
var instagramMediaCDNPattern = regexp.MustCompile(`^https://([a-z0-9-]+\.)*(cdninstagram\.com|fbcdn\.net)/(v|o1)/`)

// isInstagramMediaCDNURL reports whether imageURL is post media on the CDN.
func isInstagramMediaCDNURL(imageURL string) bool {
	result := instagramMediaCDNPattern.MatchString(imageURL)

	return result
}

// firstSubmatch returns the first capture group of pattern in s, or "".
func firstSubmatch(pattern *regexp.Regexp, s string) string {
	matches := pattern.FindStringSubmatch(s)

	if len(matches) < 2 {
		return ""
	}

	return matches[1]
}
