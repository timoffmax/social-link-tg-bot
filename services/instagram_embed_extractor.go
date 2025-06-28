package services

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

// InstagramEmbedExtractor uses Instagram's oEmbed API to get media info without authentication
type InstagramEmbedExtractor struct {
	TempDir string
}

// NewInstagramEmbedExtractor creates a new Instagram embed extractor
func NewInstagramEmbedExtractor(tempDir string) *InstagramEmbedExtractor {
	return &InstagramEmbedExtractor{
		TempDir: tempDir,
	}
}

// ExtractViaEmbed tries to extract Instagram media using the oEmbed API
func (e *InstagramEmbedExtractor) ExtractViaEmbed(instagramURL string) ([]*MediaInfo, error) {
	log.Printf("🔍 Trying Instagram oEmbed API for: %s", instagramURL)
	
	// Instagram oEmbed endpoint
	embedURL := fmt.Sprintf("https://api.instagram.com/oembed?url=%s", url.QueryEscape(instagramURL))
	
	client := &http.Client{
		Timeout: 10 * time.Second,
	}
	
	req, err := http.NewRequest("GET", embedURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}
	
	// Add headers to look more legitimate
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/121.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Referer", "https://www.instagram.com/")
	
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch embed data: %v", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("embed API returned status %d", resp.StatusCode)
	}
	
	var embedData struct {
		Version          string `json:"version"`
		Title            string `json:"title"`
		AuthorName       string `json:"author_name"`
		AuthorURL        string `json:"author_url"`
		AuthorID         int64  `json:"author_id"`
		MediaID          string `json:"media_id"`
		ProviderName     string `json:"provider_name"`
		ProviderURL      string `json:"provider_url"`
		Type             string `json:"type"`
		Width            int    `json:"width"`
		Height           int    `json:"height"`
		HTML             string `json:"html"`
		ThumbnailURL     string `json:"thumbnail_url"`
		ThumbnailWidth   int    `json:"thumbnail_width"`
		ThumbnailHeight  int    `json:"thumbnail_height"`
	}
	
	// Read the response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %v", err)
	}
	
	// Check if response is HTML (login page)
	if strings.Contains(string(body), "<!DOCTYPE") || strings.Contains(string(body), "<html") {
		log.Printf("❌ oEmbed API returned HTML instead of JSON (likely login page)")
		return nil, fmt.Errorf("oEmbed API blocked - returned HTML")
	}
	
	// Try to parse as JSON
	if err := json.Unmarshal(body, &embedData); err != nil {
		log.Printf("❌ Failed to parse oEmbed response: %v", err)
		log.Printf("Response preview: %s", string(body[:min(200, len(body))]))
		return nil, fmt.Errorf("failed to parse embed data: %v", err)
	}
	
	log.Printf("✅ Got oEmbed data: %s by @%s", embedData.Title, embedData.AuthorName)
	
	// Extract media URLs from the HTML embed code
	mediaURLs := e.extractMediaFromHTML(embedData.HTML)
	
	// If no media found in HTML, try the thumbnail as last resort
	if len(mediaURLs) == 0 && embedData.ThumbnailURL != "" {
		log.Printf("⚠️ No media found in embed HTML, using thumbnail")
		mediaURLs = append(mediaURLs, embedData.ThumbnailURL)
	}
	
	if len(mediaURLs) == 0 {
		return nil, fmt.Errorf("no media URLs found in embed data")
	}
	
	// Download the media files
	var mediaInfos []*MediaInfo
	for i, mediaURL := range mediaURLs {
		log.Printf("📥 Downloading media %d/%d: %s", i+1, len(mediaURLs), mediaURL)
		
		// Download the file
		filename, err := downloadFile(mediaURL, e.TempDir, fmt.Sprintf("instagram_embed_%d", i))
		if err != nil {
			log.Printf("❌ Failed to download: %v", err)
			continue
		}
		
		// Create caption with author info
		caption := FormatAuthorCaption("instagram", "", embedData.AuthorName, embedData.Title)
		
		mediaInfo := &MediaInfo{
			Type:     detectMediaTypeFromURL(mediaURL),
			URL:      instagramURL,
			Caption:  caption,
			Filename: filename,
		}
		
		mediaInfos = append(mediaInfos, mediaInfo)
	}
	
	if len(mediaInfos) == 0 {
		return nil, fmt.Errorf("failed to download any media files")
	}
	
	return mediaInfos, nil
}

// extractMediaFromHTML extracts media URLs from Instagram embed HTML
func (e *InstagramEmbedExtractor) extractMediaFromHTML(html string) []string {
	var urls []string
	
	// Patterns to find media URLs in the embed HTML
	patterns := []string{
		`"video_url":"([^"]+)"`,
		`"display_url":"([^"]+)"`,
		`src="(https://[^"]*\.cdninstagram\.com/[^"]+)"`,
		`"thumbnail_src":"([^"]+)"`,
	}
	
	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindAllStringSubmatch(html, -1)
		for _, match := range matches {
			if len(match) > 1 {
				url := match[1]
				// Unescape JSON strings
				url = strings.ReplaceAll(url, `\/`, `/`)
				url = strings.ReplaceAll(url, `\u0026`, `&`)
				if !contains(urls, url) {
					urls = append(urls, url)
				}
			}
		}
	}
	
	return urls
}

// Helper function to check if slice contains string
func contains(slice []string, str string) bool {
	for _, item := range slice {
		if item == str {
			return true
		}
	}
	return false
}

// detectMediaTypeFromURL guesses media type from URL
func detectMediaTypeFromURL(url string) string {
	if strings.Contains(url, "/v/") || strings.Contains(url, ".mp4") {
		return "video"
	}
	return "photo"
}

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// downloadFile downloads a file from URL and saves it to tempDir
func downloadFile(url, tempDir, filenamePrefix string) (string, error) {
	client := &http.Client{
		Timeout: 30 * time.Second,
	}
	
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("download failed with status %d", resp.StatusCode)
	}
	
	// Determine file extension from URL or content type
	ext := ".jpg"
	if strings.Contains(url, ".mp4") || strings.Contains(resp.Header.Get("Content-Type"), "video") {
		ext = ".mp4"
	} else if strings.Contains(url, ".png") {
		ext = ".png"
	}
	
	// Create unique filename
	filename := fmt.Sprintf("%s/%s_%d%s", tempDir, filenamePrefix, time.Now().Unix(), ext)
	
	// Create and write file
	file, err := os.Create(filename)
	if err != nil {
		return "", err
	}
	defer file.Close()
	
	_, err = io.Copy(file, resp.Body)
	if err != nil {
		os.Remove(filename)
		return "", err
	}
	
	return filename, nil
}