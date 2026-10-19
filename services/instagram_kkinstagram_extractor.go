package services

import (
	"errors"
	"fmt"
	"log"
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

// kkInstagramServiceName is the outage-tracker key for the kkinstagram redirector.
const kkInstagramServiceName = "kkinstagram"

// Normalized Instagram post types that always hold a video.
const (
	instagramPostTypeReel = "reel"
	instagramPostTypeTV   = "tv"
)

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
// kkinstagram 302 redirect to the Instagram CDN. The response is validated as
// real CDN media so a broken/blocked redirector (200 HTML on its own host, or
// a bounce back to instagram.com) fails loudly instead of producing garbage.
func (e *InstagramKKInstagramExtractor) ExtractMedia(instagramURL string) ([]*MediaInfo, error) {
	postType, shortcode := instagramPostPath(instagramURL)

	if shortcode == "" {
		return nil, fmt.Errorf("cannot extract post ID from URL: %s", instagramURL)
	}

	redirectURL := fmt.Sprintf("https://%s/%s/%s/", e.host, postType, shortcode)

	basePath := filepath.Join(e.tempDir, fmt.Sprintf("instagram_kk_%s_%d", shortcode, time.Now().UnixNano()))

	finalURL, contentType, size, err := downloadWithUA(redirectURL, redirectorBotUserAgent, basePath)

	if err == nil {
		err = validateRedirectorMedia(e.host, "instagram.com", finalURL, contentType, size)
	}

	if err != nil {
		os.Remove(basePath)

		// A bounce back to instagram.com usually means a deleted/private post,
		// not a service outage — only real service failures feed the tracker.
		if errors.Is(err, errRedirectorOriginBounce) == false {
			ReportServiceFailure(kkInstagramServiceName, err.Error())
		}

		return nil, err
	}

	ReportServiceSuccess(kkInstagramServiceName)

	mediaType, ext := classifyRedirectorMedia(contentType, finalURL)

	filePath := basePath + ext

	err = os.Rename(basePath, filePath)

	if err != nil {
		filePath = basePath
	}

	log.Printf("✅ [kkinstagram] Downloaded %s (%d bytes) for %s", mediaType, size, shortcode)

	// For some reels Instagram gives the video only to logged-in users and
	// the redirector lands on the cover image instead.
	isVideoCover := (MediaTypePhoto == mediaType) && (isInstagramVideoPostType(postType) == true)

	if isVideoCover == true {
		log.Printf("🎬 [kkinstagram] Got only the cover of %s %s", postType, shortcode)
	}

	result := []*MediaInfo{{
		Type:       mediaType,
		URL:        instagramURL,
		Caption:    FormatAuthorCaption("instagram", "", "", ""),
		Filename:   filePath,
		VideoCover: isVideoCover,
	}}

	return result, nil
}

// isInstagramVideoPostType reports whether a normalized post type is always
// a video (reels and IGTV).
func isInstagramVideoPostType(postType string) bool {
	result := (instagramPostTypeReel == postType) || (instagramPostTypeTV == postType)

	return result
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
		postType = instagramPostTypeReel
	}

	return postType, matches[2]
}
