package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"time"

	"github.com/timoffmax/social-link-bot/config"
)

// InstagramCobaltExtractor extracts Instagram media via a public cobalt.tools
// instance. Cobalt resolves the media on its own infrastructure and returns a
// signed CDN URL (or a picker list for carousels), so it works from datacenter
// IPs. Public instances are tried in order until one yields media.
type InstagramCobaltExtractor struct {
	tempDir   string
	instances []string
}

// cobaltRequest is the request body for a cobalt.tools instance.
type cobaltRequest struct {
	URL string `json:"url"`
}

// cobaltResponse is the response from a cobalt.tools instance.
type cobaltResponse struct {
	Status string             `json:"status"`
	URL    string             `json:"url"`
	Picker []cobaltPickerItem `json:"picker"`
}

// cobaltPickerItem represents one item of a carousel/picker response.
type cobaltPickerItem struct {
	Type string `json:"type"`
	URL  string `json:"url"`
}

// NewInstagramCobaltExtractor creates a new cobalt extractor.
func NewInstagramCobaltExtractor(tempDir string) *InstagramCobaltExtractor {
	return &InstagramCobaltExtractor{
		tempDir:   tempDir,
		instances: config.GetCobaltInstances(),
	}
}

// ExtractMedia resolves Instagram media via cobalt and downloads every returned
// item (a single redirect URL, or all picker entries for carousels).
func (e *InstagramCobaltExtractor) ExtractMedia(instagramURL string) ([]*MediaInfo, error) {
	postType, shortcode := instagramPostPath(instagramURL)

	requestURL := instagramURL

	if shortcode != "" {
		requestURL = fmt.Sprintf("https://www.instagram.com/%s/%s/", postType, shortcode)
	}

	var lastErr error

	for _, instance := range e.instances {
		result, err := e.extractFromInstance(instance, requestURL, instagramURL, shortcode)

		if (err == nil) && (len(result) > 0) {
			log.Printf("✅ [cobalt] Downloaded %d item(s) via %s", len(result), instance)

			return result, nil
		}

		lastErr = err
		log.Printf("⚠️ [cobalt] %s failed: %v", instance, err)
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("all cobalt instances failed")
	}

	return nil, lastErr
}

// extractFromInstance queries a single cobalt instance and downloads its media.
func (e *InstagramCobaltExtractor) extractFromInstance(instance, requestURL, originalURL, shortcode string) ([]*MediaInfo, error) {
	body, err := json.Marshal(cobaltRequest{URL: requestURL})

	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %v", err)
	}

	req, err := http.NewRequest("POST", instance, bytes.NewReader(body))

	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := SharedHTTPClient.Do(req)

	if err != nil {
		return nil, fmt.Errorf("request failed: %v", err)
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bad status code: %d", resp.StatusCode)
	}

	var decoded cobaltResponse

	err = json.NewDecoder(resp.Body).Decode(&decoded)

	if err != nil {
		return nil, fmt.Errorf("failed to decode response: %v", err)
	}

	mediaURLs := collectCobaltURLs(decoded)

	if len(mediaURLs) == 0 {
		return nil, fmt.Errorf("no media in cobalt response (status: %s)", decoded.Status)
	}

	result := make([]*MediaInfo, 0, len(mediaURLs))
	timestamp := time.Now().Unix()

	for i, mediaURL := range mediaURLs {
		mediaType, ext := classifyMediaURL(mediaURL)
		filename := fmt.Sprintf("instagram_cobalt_%s_%d_%d%s", shortcode, timestamp, i+1, ext)
		filePath := filepath.Join(e.tempDir, filename)

		err := downloadToFile(mediaURL, filePath)

		if err != nil {
			log.Printf("⚠️ [cobalt] failed to download item %d: %v", i+1, err)

			continue
		}

		result = append(result, &MediaInfo{
			Type:     mediaType,
			URL:      originalURL,
			Caption:  FormatAuthorCaption("instagram", "", "", ""),
			Filename: filePath,
		})
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("no cobalt media could be downloaded")
	}

	return result, nil
}

// collectCobaltURLs flattens a cobalt response into an ordered list of direct
// media URLs (every picker entry for carousels, or the single redirect URL).
func collectCobaltURLs(resp cobaltResponse) []string {
	result := make([]string, 0)

	if len(resp.Picker) > 0 {
		for _, item := range resp.Picker {
			if item.URL != "" {
				result = append(result, item.URL)
			}
		}

		return result
	}

	if resp.URL != "" {
		result = append(result, resp.URL)
	}

	return result
}
