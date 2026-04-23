package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/timoffmax/social-link-bot/config"
)

// InstagramRapidAPIExtractor extracts Instagram media (videos + photos) via the
// instagram120 RapidAPI /api/instagram/links endpoint. Works for reels, posts,
// carousels, and IGTV without requiring Instagram cookies.
type InstagramRapidAPIExtractor struct {
	tempDir string
}

type rapidAPILinksRequest struct {
	URL string `json:"url"`
}

type rapidAPILinksItem struct {
	URLs []rapidAPILinksURL `json:"urls"`
}

type rapidAPILinksURL struct {
	URL string `json:"url"`
}

func NewInstagramRapidAPIExtractor(tempDir string) *InstagramRapidAPIExtractor {
	return &InstagramRapidAPIExtractor{tempDir: tempDir}
}

// ExtractMedia fetches the media URL(s) from RapidAPI and downloads each one.
// The returned slice preserves the order of the source carousel.
func (e *InstagramRapidAPIExtractor) ExtractMedia(instagramURL string) ([]*MediaInfo, error) {
	apiKey := config.GetRapidAPIKey()
	if apiKey == "" {
		return nil, fmt.Errorf("RAPIDAPI_KEY not configured")
	}

	items, err := e.fetchLinks(apiKey, instagramURL)
	if err != nil {
		return nil, err
	}

	if len(items) == 0 {
		return nil, fmt.Errorf("no media URLs returned from API")
	}

	result := make([]*MediaInfo, 0, len(items))
	timestamp := time.Now().Unix()

	for i, item := range items {
		if len(item.URLs) == 0 {
			continue
		}

		mediaURL := item.URLs[0].URL
		if mediaURL == "" {
			continue
		}

		mediaType, ext := classifyMediaURL(mediaURL)

		filename := fmt.Sprintf("instagram_rapidapi_%d_%d%s", timestamp, i+1, ext)
		filePath := filepath.Join(e.tempDir, filename)

		err := downloadToFile(mediaURL, filePath)
		if err != nil {
			log.Printf("⚠️ [RapidAPI] Failed to download item %d: %v", i+1, err)
			continue
		}

		result = append(result, &MediaInfo{
			Type:     mediaType,
			URL:      instagramURL,
			Caption:  FormatAuthorCaption("instagram", "", "", ""),
			Filename: filePath,
		})
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("no media could be downloaded")
	}

	log.Printf("✅ [RapidAPI] Downloaded %d media item(s) for %s", len(result), instagramURL)

	return result, nil
}

// fetchLinks calls the RapidAPI /api/instagram/links endpoint and returns the raw items.
func (e *InstagramRapidAPIExtractor) fetchLinks(apiKey, instagramURL string) ([]rapidAPILinksItem, error) {
	host := config.GetRapidAPIInstagramHost()

	body, err := json.Marshal(rapidAPILinksRequest{URL: instagramURL})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %v", err)
	}

	req, err := http.NewRequest("POST", "https://"+host+"/api/instagram/links", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-rapidapi-host", host)
	req.Header.Set("x-rapidapi-key", apiKey)

	resp, err := SharedDownloadClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("API request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var items []rapidAPILinksItem

	err = json.NewDecoder(resp.Body).Decode(&items)
	if err != nil {
		return nil, fmt.Errorf("failed to decode API response: %v", err)
	}

	return items, nil
}

// classifyMediaURL infers the media type (photo/video) and file extension from the URL path.
// Instagram CDN URLs include the extension in the path before the query string, so this avoids an extra HEAD.
func classifyMediaURL(mediaURL string) (string, string) {
	path := mediaURL
	if idx := strings.Index(path, "?"); idx != -1 {
		path = path[:idx]
	}

	ext := strings.ToLower(filepath.Ext(path))

	switch ext {
	case ".mp4", ".mov", ".webm", ".m4v":
		return "video", ".mp4"
	case ".png", ".webp", ".heic":
		return "photo", ext
	case ".jpg", ".jpeg":
		return "photo", ".jpg"
	}

	return "photo", ".jpg"
}
