package services

import (
	"bytes"
	"encoding/json"
	"fmt"
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

// InstagramStoriesExtractor handles story extraction via RapidAPI (instagram120)
type InstagramStoriesExtractor struct {
	tempDir string
}

// storyAPIRequest is the request body for the RapidAPI story endpoint
type storyAPIRequest struct {
	Username string `json:"username"`
	StoryID  string `json:"storyId"`
}

// storyAPIResponse is the response from the RapidAPI story endpoint
type storyAPIResponse struct {
	Result []storyItem `json:"result"`
}

// storyItem represents a single story item from the API
type storyItem struct {
	ImageVersions2 *imageVersions `json:"image_versions2"`
	VideoVersions  []mediaVersion `json:"video_versions"`
}

// imageVersions contains image candidates
type imageVersions struct {
	Candidates []mediaVersion `json:"candidates"`
}

// mediaVersion represents a single media variant (image or video)
type mediaVersion struct {
	URL    string `json:"url"`
	Height int    `json:"height"`
	Width  int    `json:"width"`
}

var storyURLPattern = regexp.MustCompile(`instagram\.com/stories/([^/]+)/([0-9]+)`)

// NewInstagramStoriesExtractor creates a new stories extractor
func NewInstagramStoriesExtractor(tempDir string) *InstagramStoriesExtractor {
	return &InstagramStoriesExtractor{tempDir: tempDir}
}

// ExtractStory downloads an Instagram story via RapidAPI and returns media info
func (e *InstagramStoriesExtractor) ExtractStory(storyURL string) ([]*MediaInfo, error) {
	apiKey := config.GetRapidAPIKey()
	if apiKey == "" {
		return nil, fmt.Errorf("RAPIDAPI_KEY not configured")
	}

	username, storyID, err := parseStoryURL(storyURL)
	if err != nil {
		return nil, err
	}

	log.Printf("📖 [Stories API] Fetching story by @%s, ID %s", username, storyID)

	item, err := e.fetchStoryFromAPI(apiKey, username, storyID)
	if err != nil {
		return nil, err
	}

	mediaURL, mediaType := pickBestMedia(item)
	if mediaURL == "" {
		return nil, fmt.Errorf("no media URL found in story response")
	}

	log.Printf("📖 [Stories API] Found %s: %s", mediaType, mediaURL)

	ext := ".jpg"
	if mediaType == "video" {
		ext = ".mp4"
	}

	filename := fmt.Sprintf("instagram_story_%s_%d%s", storyID, time.Now().Unix(), ext)
	filePath := filepath.Join(e.tempDir, filename)

	err = downloadToFile(mediaURL, filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to download story media: %v", err)
	}

	caption := strings.TrimRight(FormatAuthorCaption("instagram", "", username, ""), "\n")

	result := []*MediaInfo{{
		Type:     mediaType,
		URL:      storyURL,
		Caption:  caption,
		Filename: filePath,
	}}

	log.Printf("✅ [Stories API] Successfully downloaded story %s from @%s", mediaType, username)

	return result, nil
}

// parseStoryURL extracts username and storyId from an Instagram story URL
func parseStoryURL(url string) (string, string, error) {
	matches := storyURLPattern.FindStringSubmatch(url)
	if (matches == nil) || (len(matches) < 3) {
		return "", "", fmt.Errorf("cannot parse story URL: %s", url)
	}

	return matches[1], matches[2], nil
}

// fetchStoryFromAPI calls the RapidAPI instagram120 story endpoint
func (e *InstagramStoriesExtractor) fetchStoryFromAPI(apiKey, username, storyID string) (*storyItem, error) {
	host := config.GetRapidAPIInstagramHost()

	body, err := json.Marshal(storyAPIRequest{
		Username: username,
		StoryID:  storyID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %v", err)
	}

	req, err := http.NewRequest("POST", "https://"+host+"/api/instagram/story", bytes.NewReader(body))
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

	var apiResp storyAPIResponse

	err = json.NewDecoder(resp.Body).Decode(&apiResp)
	if err != nil {
		return nil, fmt.Errorf("failed to decode API response: %v", err)
	}

	if len(apiResp.Result) == 0 {
		return nil, fmt.Errorf("story not found or expired")
	}

	return &apiResp.Result[0], nil
}

// pickBestMedia selects the best media URL from a story item.
// Prefers video over image. For images, picks the highest resolution candidate.
func pickBestMedia(item *storyItem) (string, string) {
	// Prefer video if available
	if len(item.VideoVersions) > 0 {
		best := item.VideoVersions[0]

		for _, v := range item.VideoVersions[1:] {
			if (v.Width * v.Height) > (best.Width * best.Height) {
				best = v
			}
		}

		return best.URL, "video"
	}

	// Fall back to image
	if (item.ImageVersions2 != nil) && (len(item.ImageVersions2.Candidates) > 0) {
		best := item.ImageVersions2.Candidates[0]

		for _, c := range item.ImageVersions2.Candidates[1:] {
			if (c.Width * c.Height) > (best.Width * best.Height) {
				best = c
			}
		}

		return best.URL, "photo"
	}

	return "", ""
}

// downloadToFile downloads a URL to a local file
func downloadToFile(url, filePath string) error {
	resp, err := SharedDownloadClient.Get(url)
	if err != nil {
		return fmt.Errorf("failed to fetch: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status code: %d", resp.StatusCode)
	}

	out, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("failed to create file: %v", err)
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	if err != nil {
		return fmt.Errorf("failed to write file: %v", err)
	}

	return nil
}
