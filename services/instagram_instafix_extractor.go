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
)

// InstagramInstafixExtractor extracts Instagram photos/carousels by enumerating
// /images/{postID}/{N} on an InstaFix-compatible service (toinstagram.com,
// onebigram.com). The service 302-redirects each index to the Instagram CDN
// (proxied through its own residential infrastructure), so this works from
// datacenter IPs where direct Instagram access is blocked.
type InstagramInstafixExtractor struct {
	tempDir  string
	services []string
}

var instagramPostIDPattern = regexp.MustCompile(`(?:instagram|toinstagram|onebigram|kksave|instagramez)\.com/(?:[^/]+/)?(?:p|reel|reels|tv)/([A-Za-z0-9_-]+)`)

const instafixMaxCarouselItems = 20

func NewInstagramInstafixExtractor(tempDir string) *InstagramInstafixExtractor {
	return &InstagramInstafixExtractor{
		tempDir:  tempDir,
		services: []string{"toinstagram.com", "onebigram.com"},
	}
}

// ExtractPhotos downloads every image of an Instagram post by iterating the
// InstaFix /images/{postID}/N endpoint. Tries each configured service in order
// until one succeeds.
func (e *InstagramInstafixExtractor) ExtractPhotos(instagramURL string) ([]*MediaInfo, error) {
	postID := extractInstagramPostID(instagramURL)
	if postID == "" {
		return nil, fmt.Errorf("cannot extract post ID from URL: %s", instagramURL)
	}

	var lastErr error

	for _, service := range e.services {
		log.Printf("📷 [InstaFix] Trying %s for post %s", service, postID)
		result, err := e.extractFromService(service, postID, instagramURL)

		if err == nil && len(result) > 0 {
			log.Printf("✅ [InstaFix] Downloaded %d photo(s) via %s", len(result), service)
			return result, nil
		}

		lastErr = err
		log.Printf("⚠️ [InstaFix] %s failed: %v", service, err)
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("all InstaFix services failed")
	}

	return nil, lastErr
}

// extractFromService iterates /images/{postID}/{1..N} on a single service until
// an empty body or non-200 is returned.
func (e *InstagramInstafixExtractor) extractFromService(service, postID, originalURL string) ([]*MediaInfo, error) {
	result := make([]*MediaInfo, 0)
	timestamp := time.Now().Unix()

	for n := 1; n <= instafixMaxCarouselItems; n++ {
		imageURL := fmt.Sprintf("https://%s/images/%s/%d", service, postID, n)

		filename := fmt.Sprintf("instagram_instafix_%s_%d_%d.jpg", postID, timestamp, n)
		filePath := filepath.Join(e.tempDir, filename)

		size, err := downloadAndReportSize(imageURL, filePath)

		if err != nil {
			os.Remove(filePath)

			if n == 1 {
				return nil, err
			}

			break
		}

		if size == 0 {
			os.Remove(filePath)
			break
		}

		result = append(result, &MediaInfo{
			Type:     "photo",
			URL:      originalURL,
			Caption:  FormatAuthorCaption("instagram", "", "", ""),
			Filename: filePath,
		})
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("no photos found for post %s on %s", postID, service)
	}

	return result, nil
}

// extractInstagramPostID pulls the short code from any recognized Instagram or
// InstaFix-style URL (e.g. /p/XXX, /reel/XXX, /username/p/XXX).
func extractInstagramPostID(url string) string {
	matches := instagramPostIDPattern.FindStringSubmatch(url)
	if len(matches) < 2 {
		return ""
	}

	return matches[1]
}

// downloadAndReportSize downloads a URL to a file and returns the bytes written.
// A 200 response with 0 bytes is the InstaFix signal for "end of carousel".
func downloadAndReportSize(url, filePath string) (int64, error) {
	resp, err := SharedDownloadClient.Get(url)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("bad status code: %d", resp.StatusCode)
	}

	out, err := os.Create(filePath)
	if err != nil {
		return 0, err
	}
	defer out.Close()

	n, err := io.Copy(out, resp.Body)

	return n, err
}
