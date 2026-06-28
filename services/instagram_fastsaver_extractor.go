package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/timoffmax/social-link-bot/config"
)

// fastSaverTimeout caps the /v1/fetch call so a failing FastSaver backend falls
// through to the free redirector methods quickly (successes return in ~5s).
const fastSaverTimeout = 8 * time.Second

// InstagramFastSaverExtractor extracts Instagram media via the FastSaverAPI
// /v1/fetch endpoint, which resolves media on its own infrastructure and returns
// a direct CDN download URL. Used as a keyed primary/backstop alongside the free
// redirector methods.
type InstagramFastSaverExtractor struct {
	tempDir string
}

// fastSaverResponse is the response from FastSaverAPI /v1/fetch.
type fastSaverResponse struct {
	OK          bool   `json:"ok"`
	ID          string `json:"id"`
	Source      string `json:"source"`
	Type        string `json:"type"`
	DownloadURL string `json:"download_url"`
	Caption     string `json:"caption"`
	Detail      string `json:"detail"`
}

// NewInstagramFastSaverExtractor creates a new FastSaverAPI extractor.
func NewInstagramFastSaverExtractor(tempDir string) *InstagramFastSaverExtractor {
	return &InstagramFastSaverExtractor{tempDir: tempDir}
}

// ExtractMedia fetches the direct media URL from FastSaverAPI and downloads it.
func (e *InstagramFastSaverExtractor) ExtractMedia(instagramURL string) ([]*MediaInfo, error) {
	apiKey := config.GetFastSaverAPIKey()

	if apiKey == "" {
		return nil, fmt.Errorf("FASTSAVER_API_KEY not configured")
	}

	fetchURL := fastSaverNormalizeURL(instagramURL)

	decoded, err := e.fetch(apiKey, fetchURL)

	if err != nil {
		return nil, err
	}

	if (decoded.OK == false) || (decoded.DownloadURL == "") {
		detail := decoded.Detail

		if detail == "" {
			detail = "no download URL"
		}

		return nil, fmt.Errorf("FastSaverAPI failed: %s", detail)
	}

	mediaType := "video"

	if (decoded.Type == "image") || (decoded.Type == "photo") {
		mediaType = "photo"
	}

	_, ext := classifyMediaURL(decoded.DownloadURL)

	filename := fmt.Sprintf("instagram_fastsaver_%s_%d%s", decoded.ID, time.Now().Unix(), ext)
	filePath := filepath.Join(e.tempDir, filename)

	err = downloadToFile(decoded.DownloadURL, filePath)

	if err != nil {
		return nil, fmt.Errorf("failed to download media: %v", err)
	}

	log.Printf("✅ [FastSaver] Downloaded %s for %s", mediaType, decoded.ID)

	result := []*MediaInfo{{
		Type:     mediaType,
		URL:      instagramURL,
		Caption:  FormatAuthorCaption("instagram", "", "", decoded.Caption),
		Filename: filePath,
	}}

	return result, nil
}

// fetch calls FastSaverAPI /v1/fetch with the API key header. The media URL is
// appended raw (not percent-encoded), matching the endpoint's expected format.
func (e *InstagramFastSaverExtractor) fetch(apiKey, mediaURL string) (*fastSaverResponse, error) {
	host := config.GetFastSaverAPIHost()
	endpoint := fmt.Sprintf("https://%s/v1/fetch?url=%s", host, mediaURL)

	ctx, cancel := context.WithTimeout(context.Background(), fastSaverTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)

	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("X-Api-Key", apiKey)

	resp, err := SharedHTTPClient.Do(req)

	if err != nil {
		return nil, fmt.Errorf("API request failed: %v", err)
	}

	defer resp.Body.Close()

	var decoded fastSaverResponse

	err = json.NewDecoder(resp.Body).Decode(&decoded)

	if err != nil {
		return nil, fmt.Errorf("failed to decode response: %v", err)
	}

	return &decoded, nil
}

// fastSaverNormalizeURL rebuilds a canonical IG post URL WITHOUT a trailing slash
// or query string, which FastSaverAPI rejects with "fetch.failed".
func fastSaverNormalizeURL(rawURL string) string {
	postType, shortcode := instagramPostPath(rawURL)

	if shortcode != "" {
		return fmt.Sprintf("https://www.instagram.com/%s/%s", postType, shortcode)
	}

	if idx := strings.IndexByte(rawURL, '?'); idx != -1 {
		rawURL = rawURL[:idx]
	}

	return strings.TrimRight(rawURL, "/")
}
