package services

// RedditVxredditExtractor resolves Reddit media through vxreddit.com, which
// scrapes Reddit and re-serves video (audio merged) from its OWN CDN
// (renders.vxreddit.com). Like the kk redirectors, this works from datacenter
// IPs where reddit.com and the v.redd.it CDN block direct access. vxreddit
// resolves /s/ share links itself, so no pre-resolution is needed.
//
// Scope: video posts are fully datacenter-robust (proxied+merged). Image posts
// come back as raw i.redd.it URLs (NOT proxied), so they are best-effort. Text
// bodies are not exposed (og:title only), so text posts stay with the JSON
// extractor and the link fallback.

import (
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/timoffmax/social-link-bot/config"
)

// redditVxredditServiceName is the outage-tracker key for vxreddit.
const redditVxredditServiceName = "vxreddit"

// vxredditMinMediaSize guards against tiny HTML/error bodies posing as media.
const vxredditMinMediaSize = int64(10 * 1024)

var (
	vxredditOGVideoPattern = regexp.MustCompile(`<meta\s+property="og:video"\s+content="([^"]+)"`)
	vxredditOGImagePattern = regexp.MustCompile(`<meta\s+property="og:image"\s+content="([^"]+)"`)
	vxredditOGTitlePattern = regexp.MustCompile(`<meta\s+property="og:title"\s+content="([^"]*)"`)
	vxredditSubredditPath  = regexp.MustCompile(`/r/([^/]+)/`)
)

// RedditVxredditExtractor downloads Reddit media via the vxreddit.com frontend.
type RedditVxredditExtractor struct {
	tempDir string
	host    string
}

// NewRedditVxredditExtractor creates a new vxreddit-backed Reddit extractor.
func NewRedditVxredditExtractor(tempDir string) *RedditVxredditExtractor {
	return &RedditVxredditExtractor{
		tempDir: tempDir,
		host:    config.GetVxredditHost(),
	}
}

// ExtractMedia fetches the vxreddit page for a Reddit URL, then downloads the
// proxied media it advertises. Video is preferred; a standalone image is used
// only when there is no video.
func (e *RedditVxredditExtractor) ExtractMedia(redditURL string) ([]*MediaInfo, error) {
	pageURL := e.buildVxredditURL(redditURL)

	page, err := e.fetchPage(pageURL)

	if err != nil {
		ReportServiceFailure(redditVxredditServiceName, err.Error())

		return nil, err
	}

	videoURL := firstSubmatch(vxredditOGVideoPattern, page)
	imageURL := firstSubmatch(vxredditOGImagePattern, page)
	title := firstSubmatch(vxredditOGTitlePattern, page)
	subreddit := firstSubmatch(vxredditSubredditPath, redditURL)

	caption := FormatAuthorCaption("reddit", subreddit, "", EscapeForTelegramHTML(html.UnescapeString(title)))

	mediaURL := ""
	expectVideo := false

	if videoURL != "" {
		mediaURL = html.UnescapeString(videoURL)
		expectVideo = true
	} else if isProxyableVxredditImage(imageURL) {
		mediaURL = html.UnescapeString(imageURL)
	}

	if mediaURL == "" {
		// A text post has no media: that's the post, not an outage (the hourly
		// health probe catches a broken vxreddit), so it doesn't feed the tracker.
		err = fmt.Errorf("no downloadable media in vxreddit page for %s", redditURL)

		return nil, err
	}

	result, err := e.downloadMedia(mediaURL, caption, redditURL, expectVideo)

	if err != nil {
		ReportServiceFailure(redditVxredditServiceName, err.Error())

		return nil, err
	}

	ReportServiceSuccess(redditVxredditServiceName)

	return result, nil
}

// downloadMedia downloads and validates a single vxreddit-advertised media URL.
func (e *RedditVxredditExtractor) downloadMedia(mediaURL, caption, originalURL string, expectVideo bool) ([]*MediaInfo, error) {
	basePath := filepath.Join(e.tempDir, fmt.Sprintf("reddit_vx_%d", time.Now().UnixNano()))

	finalURL, contentType, size, err := downloadWithUA(mediaURL, redirectorBotUserAgent, basePath)

	if err != nil {
		os.Remove(basePath)

		return nil, err
	}

	mimeType := extractMimeType(contentType)

	isVideo := strings.HasPrefix(mimeType, "video/")
	isImage := strings.HasPrefix(mimeType, "image/")

	if (isVideo == false) && (isImage == false) {
		os.Remove(basePath)

		return nil, fmt.Errorf("vxreddit returned non-media content-type %q", contentType)
	}

	if expectVideo && (isVideo == false) {
		os.Remove(basePath)

		return nil, fmt.Errorf("expected video from vxreddit, got %q", contentType)
	}

	if size < vxredditMinMediaSize {
		os.Remove(basePath)

		return nil, fmt.Errorf("suspiciously small vxreddit media body (%d bytes)", size)
	}

	mediaType, ext := classifyRedirectorMedia(contentType, finalURL)

	filePath := basePath + ext

	if renameErr := os.Rename(basePath, filePath); renameErr != nil {
		filePath = basePath
	}

	log.Printf("✅ [vxreddit] Downloaded %s (%d bytes)", mediaType, size)

	result := []*MediaInfo{{
		Type:     mediaType,
		URL:      originalURL,
		Caption:  caption,
		Filename: filePath,
	}}

	return result, nil
}

// fetchPage retrieves the vxreddit HTML page with the bot User-Agent.
func (e *RedditVxredditExtractor) fetchPage(pageURL string) (string, error) {
	req, err := http.NewRequest("GET", pageURL, nil)

	if err != nil {
		return "", err
	}

	req.Header.Set("User-Agent", redirectorBotUserAgent)

	resp, err := SharedHTTPClient.Do(req)

	if err != nil {
		return "", fmt.Errorf("vxreddit request failed: %v", err)
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("vxreddit returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))

	if err != nil {
		return "", fmt.Errorf("failed to read vxreddit page: %v", err)
	}

	return string(body), nil
}

// buildVxredditURL rewrites any reddit.com URL to its vxreddit host equivalent;
// vxreddit resolves /s/ share links itself.
func (e *RedditVxredditExtractor) buildVxredditURL(redditURL string) string {
	result := redditURL

	for _, sub := range []string{"www.reddit.com", "old.reddit.com", "m.reddit.com", "np.reddit.com", "reddit.com"} {
		if strings.Contains(result, sub) {
			result = strings.Replace(result, sub, e.host, 1)

			break
		}
	}

	return result
}

// isProxyableVxredditImage reports whether an og:image URL is a real post image
// worth downloading, excluding the external-preview thumbnails vxreddit attaches
// to video posts.
func isProxyableVxredditImage(imageURL string) bool {
	if imageURL == "" {
		return false
	}

	if strings.Contains(imageURL, "external-preview.redd.it") {
		return false
	}

	return true
}
