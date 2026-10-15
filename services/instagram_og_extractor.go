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
// yields the actual video — only a preview image plus caption — but is strictly
// better than a dead fixed-link fallback that shows nothing.
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
// unrelated image). Otherwise it downloads the og:image thumbnail as a photo.
func (e *InstagramOGExtractor) ExtractMedia(instagramURL string) ([]*MediaInfo, error) {
	var result []*MediaInfo
	var err error
	gate := CheckInstagramGate(instagramURL)

	if gate.Restricted == true {
		err = &InstagramGatedError{Gate: gate}
	}

	if gate.Restricted == false {
		result, err = e.extractPreview(instagramURL)
	}

	return result, err
}

// extractPreview scrapes the og:image thumbnail and caption and downloads the
// thumbnail as a photo.
func (e *InstagramOGExtractor) extractPreview(instagramURL string) ([]*MediaInfo, error) {
	htmlBody, err := e.fetchHTML(instagramURL)

	if err != nil {
		return nil, err
	}

	imageURL := firstSubmatch(ogImagePattern, htmlBody)

	if imageURL == "" {
		return nil, fmt.Errorf("no og:image found")
	}

	imageURL = html.UnescapeString(imageURL)
	caption := html.UnescapeString(firstSubmatch(ogTitlePattern, htmlBody))

	_, shortcode := instagramPostPath(instagramURL)

	filename := fmt.Sprintf("instagram_og_%s_%d.jpg", shortcode, time.Now().Unix())
	filePath := filepath.Join(e.tempDir, filename)

	err = downloadToFile(imageURL, filePath)

	if err != nil {
		return nil, fmt.Errorf("failed to download og:image: %v", err)
	}

	log.Printf("✅ [OG] Downloaded preview thumbnail for %s", instagramURL)

	result := []*MediaInfo{{
		Type:     "photo",
		URL:      instagramURL,
		Caption:  FormatAuthorCaption("instagram", "", "", caption),
		Filename: filePath,
	}}

	return result, nil
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

// firstSubmatch returns the first capture group of pattern in s, or "".
func firstSubmatch(pattern *regexp.Regexp, s string) string {
	matches := pattern.FindStringSubmatch(s)

	if len(matches) < 2 {
		return ""
	}

	return matches[1]
}
