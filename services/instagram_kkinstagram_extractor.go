package services

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/timoffmax/social-link-bot/config"
)

// InstagramKKInstagramExtractor extracts Instagram media by requesting the bare
// /{type}/{shortcode}/ path on a kkinstagram-style redirector with a bot
// User-Agent. The service 302-redirects to a signed cdninstagram.com media URL
// resolved on its own infrastructure, so this works from datacenter IPs where
// direct Instagram access is blocked. Carousels yield only the first item.
type InstagramKKInstagramExtractor struct {
	tempDir string
	host    string
}

// kkInstagramBotUserAgent must be a bot UA — a normal browser UA gets a 301
// app-deeplink instead of the media redirect.
const kkInstagramBotUserAgent = "TelegramBot (like TwitterBot)"

// instagramPostPathPattern captures the post type (p/reel/reels/tv) and shortcode
// from any recognized Instagram or proxy URL.
var instagramPostPathPattern = regexp.MustCompile(`(?:instagram|toinstagram|onebigram|kksave|instagramez)\.com/(?:[^/]+/)?(p|reel|reels|tv)/([A-Za-z0-9_-]+)`)

// NewInstagramKKInstagramExtractor creates a new kkinstagram redirect extractor.
func NewInstagramKKInstagramExtractor(tempDir string) *InstagramKKInstagramExtractor {
	return &InstagramKKInstagramExtractor{
		tempDir: tempDir,
		host:    config.GetKKInstagramHost(),
	}
}

// ExtractMedia downloads a single Instagram media item by following the
// kkinstagram 302 redirect to the Instagram CDN.
func (e *InstagramKKInstagramExtractor) ExtractMedia(instagramURL string) ([]*MediaInfo, error) {
	postType, shortcode := instagramPostPath(instagramURL)

	if shortcode == "" {
		return nil, fmt.Errorf("cannot extract post ID from URL: %s", instagramURL)
	}

	redirectURL := fmt.Sprintf("https://%s/%s/%s/", e.host, postType, shortcode)

	basePath := filepath.Join(e.tempDir, fmt.Sprintf("instagram_kk_%s_%d", shortcode, time.Now().Unix()))

	finalURL, size, err := downloadWithUA(redirectURL, kkInstagramBotUserAgent, basePath)

	if err != nil {
		os.Remove(basePath)

		return nil, err
	}

	if size == 0 {
		os.Remove(basePath)

		return nil, fmt.Errorf("kkinstagram returned empty body for %s", shortcode)
	}

	mediaType, ext := classifyMediaURL(finalURL)

	filePath := basePath + ext

	err = os.Rename(basePath, filePath)

	if err != nil {
		filePath = basePath
	}

	log.Printf("✅ [kkinstagram] Downloaded %s (%d bytes) for %s", mediaType, size, shortcode)

	result := []*MediaInfo{{
		Type:     mediaType,
		URL:      instagramURL,
		Caption:  FormatAuthorCaption("instagram", "", "", ""),
		Filename: filePath,
	}}

	return result, nil
}

// instagramPostPath returns the normalized post type ("p", "reel", "tv") and the
// shortcode for any recognized Instagram or proxy URL.
func instagramPostPath(rawURL string) (string, string) {
	matches := instagramPostPathPattern.FindStringSubmatch(rawURL)

	if len(matches) < 3 {
		return "", ""
	}

	postType := matches[1]

	if postType == "reels" {
		postType = "reel"
	}

	return postType, matches[2]
}

// downloadWithUA fetches a URL with a custom User-Agent, following redirects, and
// writes the body to filePath. It returns the final (post-redirect) URL and the
// number of bytes written.
func downloadWithUA(rawURL, userAgent, filePath string) (string, int64, error) {
	req, err := http.NewRequest("GET", rawURL, nil)

	if err != nil {
		return "", 0, fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("User-Agent", userAgent)

	resp, err := SharedDownloadClient.Do(req)

	if err != nil {
		return "", 0, fmt.Errorf("request failed: %v", err)
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("bad status code: %d", resp.StatusCode)
	}

	finalURL := rawURL

	if (resp.Request != nil) && (resp.Request.URL != nil) {
		finalURL = resp.Request.URL.String()
	}

	out, err := os.Create(filePath)

	if err != nil {
		return "", 0, fmt.Errorf("failed to create file: %v", err)
	}

	defer out.Close()

	size, err := io.Copy(out, resp.Body)

	if err != nil {
		return finalURL, size, fmt.Errorf("failed to write file: %v", err)
	}

	return finalURL, size, nil
}
