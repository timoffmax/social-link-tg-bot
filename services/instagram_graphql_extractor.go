package services

import (
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
)

// InstagramGraphQLExtractor uses Instagram's GraphQL API to extract media
type InstagramGraphQLExtractor struct {
	TempDir string
}

// NewInstagramGraphQLExtractor creates a new Instagram GraphQL extractor
func NewInstagramGraphQLExtractor(tempDir string) *InstagramGraphQLExtractor {
	return &InstagramGraphQLExtractor{
		TempDir: tempDir,
	}
}

// ExtractViaGraphQL tries to extract Instagram media using GraphQL API
func (e *InstagramGraphQLExtractor) ExtractViaGraphQL(instagramURL string) ([]*MediaInfo, error) {
	log.Printf("🔍 Trying Instagram GraphQL approach for: %s", instagramURL)
	
	// Extract shortcode from URL
	shortcode := e.extractShortcode(instagramURL)
	if shortcode == "" {
		return nil, fmt.Errorf("failed to extract shortcode from URL")
	}
	
	log.Printf("📝 Extracted shortcode: %s", shortcode)
	
	// Try different API endpoints
	endpoints := []struct {
		name string
		url  string
	}{
		{
			name: "Media API",
			url:  fmt.Sprintf("https://www.instagram.com/api/v1/media/%s/info/", shortcode),
		},
		{
			name: "GraphQL Query",
			url:  fmt.Sprintf("https://www.instagram.com/graphql/query/?query_hash=2b0673e0dc4580674a88d426fe00ea90&variables={\"shortcode\":\"%s\"}", shortcode),
		},
		{
			name: "Web API",
			url:  fmt.Sprintf("https://www.instagram.com/p/%s/?__a=1&__d=dis", shortcode),
		},
	}
	
	for _, endpoint := range endpoints {
		log.Printf("🔄 Trying %s endpoint", endpoint.name)

		req, err := http.NewRequest("GET", endpoint.url, nil)
		if err != nil {
			continue
		}

		// Set headers to mimic a real browser request
		e.setHeaders(req)

		resp, err := SharedHTTPClient.Do(req)
		if err != nil {
			log.Printf("❌ %s request failed: %v", endpoint.name, err)
			continue
		}
		defer resp.Body.Close()
		
		if resp.StatusCode == 200 {
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				continue
			}
			
			// Check if response is JSON
			if strings.HasPrefix(strings.TrimSpace(string(body)), "{") {
				log.Printf("✅ Got JSON response from %s", endpoint.name)
				return e.parseGraphQLResponse(body, instagramURL)
			}
		}
		
		log.Printf("❌ %s returned status %d", endpoint.name, resp.StatusCode)
	}
	
	return nil, fmt.Errorf("all GraphQL endpoints failed")
}

// extractShortcode extracts the shortcode from Instagram URL
func (e *InstagramGraphQLExtractor) extractShortcode(url string) string {
	// Patterns to match Instagram URLs
	patterns := []string{
		`instagram\.com/p/([A-Za-z0-9_-]+)`,
		`instagram\.com/reel/([A-Za-z0-9_-]+)`,
		`instagram\.com/tv/([A-Za-z0-9_-]+)`,
		`instagram\.com/stories/[^/]+/([A-Za-z0-9_-]+)`,
	}
	
	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindStringSubmatch(url)
		if len(matches) > 1 {
			return matches[1]
		}
	}
	
	return ""
}

// setHeaders sets appropriate headers for Instagram requests
func (e *InstagramGraphQLExtractor) setHeaders(req *http.Request) {
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/121.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("X-IG-App-ID", "936619743392459")
	req.Header.Set("X-IG-WWW-Claim", "0")
	req.Header.Set("X-ASBD-ID", "129477")
	req.Header.Set("Referer", "https://www.instagram.com/")
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
}

// parseGraphQLResponse parses the GraphQL API response
func (e *InstagramGraphQLExtractor) parseGraphQLResponse(body []byte, originalURL string) ([]*MediaInfo, error) {
	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %v", err)
	}
	
	// Try to extract media URLs from various response structures
	var mediaURLs []string
	var username string
	var caption string
	
	// Try different paths in the JSON structure
	if e.extractFromPath(result, &mediaURLs, &username, &caption, []string{"items", "0"}) {
		log.Printf("✅ Extracted from items path")
	} else if e.extractFromPath(result, &mediaURLs, &username, &caption, []string{"data", "shortcode_media"}) {
		log.Printf("✅ Extracted from GraphQL path")
	} else if e.extractFromPath(result, &mediaURLs, &username, &caption, []string{"graphql", "shortcode_media"}) {
		log.Printf("✅ Extracted from graphql path")
	}
	
	if len(mediaURLs) == 0 {
		// Last resort: search for any URL patterns in the response
		mediaURLs = e.findMediaURLsInJSON(string(body))
	}
	
	if len(mediaURLs) == 0 {
		return nil, fmt.Errorf("no media URLs found in response")
	}
	
	log.Printf("📸 Found %d media URLs", len(mediaURLs))
	
	// Download the media files
	var mediaInfos []*MediaInfo
	for i, mediaURL := range mediaURLs {
		if !strings.HasPrefix(mediaURL, "http") {
			continue
		}
		
		log.Printf("📥 Downloading media %d/%d", i+1, len(mediaURLs))
		
		// Create unique filename
		ext := ".jpg"
		if strings.Contains(mediaURL, ".mp4") || strings.Contains(mediaURL, "/v/") {
			ext = ".mp4"
		}
		filename := filepath.Join(e.TempDir, fmt.Sprintf("instagram_graphql_%d_%d%s", time.Now().Unix(), i, ext))
		
		// Download the file
		if err := e.downloadFile(mediaURL, filename); err != nil {
			log.Printf("❌ Failed to download: %v", err)
			continue
		}
		
		// Create media info
		mediaType := "photo"
		if ext == ".mp4" {
			mediaType = "video"
		}
		
		formattedCaption := FormatAuthorCaption("instagram", "", username, caption)
		
		mediaInfo := &MediaInfo{
			Type:     mediaType,
			URL:      originalURL,
			Caption:  formattedCaption,
			Filename: filename,
		}
		
		mediaInfos = append(mediaInfos, mediaInfo)
	}
	
	if len(mediaInfos) == 0 {
		return nil, fmt.Errorf("failed to download any media files")
	}
	
	return mediaInfos, nil
}

// extractFromPath tries to extract media info from a specific path in the JSON
func (e *InstagramGraphQLExtractor) extractFromPath(data map[string]interface{}, mediaURLs *[]string, username, caption *string, path []string) bool {
	current := data
	for _, key := range path {
		if next, ok := current[key]; ok {
			switch v := next.(type) {
			case map[string]interface{}:
				current = v
			case []interface{}:
				if len(v) > 0 {
					if m, ok := v[0].(map[string]interface{}); ok {
						current = m
					}
				}
			}
		} else {
			return false
		}
	}
	
	// Extract username
	if user, ok := current["user"].(map[string]interface{}); ok {
		if uname, ok := user["username"].(string); ok {
			*username = uname
		}
	}
	
	// Extract caption
	if captionData, ok := current["caption"].(map[string]interface{}); ok {
		if text, ok := captionData["text"].(string); ok {
			*caption = text
		}
	} else if captionText, ok := current["caption"].(string); ok {
		*caption = captionText
	}
	
	// Extract media URLs
	// For videos
	if videoURL, ok := current["video_url"].(string); ok {
		*mediaURLs = append(*mediaURLs, videoURL)
		return true
	}
	
	// For single images
	if imageURL, ok := current["display_url"].(string); ok {
		*mediaURLs = append(*mediaURLs, imageURL)
		return true
	}
	
	// For carousel
	if edges, ok := current["edge_sidecar_to_children"].(map[string]interface{}); ok {
		if edgeList, ok := edges["edges"].([]interface{}); ok {
			for _, edge := range edgeList {
				if edgeMap, ok := edge.(map[string]interface{}); ok {
					if node, ok := edgeMap["node"].(map[string]interface{}); ok {
						if videoURL, ok := node["video_url"].(string); ok {
							*mediaURLs = append(*mediaURLs, videoURL)
						} else if displayURL, ok := node["display_url"].(string); ok {
							*mediaURLs = append(*mediaURLs, displayURL)
						}
					}
				}
			}
			return len(*mediaURLs) > 0
		}
	}
	
	return false
}

// findMediaURLsInJSON searches for media URLs in JSON string
func (e *InstagramGraphQLExtractor) findMediaURLsInJSON(jsonStr string) []string {
	var urls []string
	
	// Patterns to find Instagram CDN URLs
	patterns := []string{
		`"video_url"\s*:\s*"(https://[^"]+\.mp4[^"]*)"`,
		`"display_url"\s*:\s*"(https://[^"]+\.jpg[^"]*)"`,
		`"src"\s*:\s*"(https://[^"]+\.cdninstagram\.com[^"]+)"`,
		`"url"\s*:\s*"(https://[^"]+\.cdninstagram\.com[^"]+)"`,
	}
	
	seen := make(map[string]bool)
	
	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindAllStringSubmatch(jsonStr, -1)
		for _, match := range matches {
			if len(match) > 1 {
				url := match[1]
				// Unescape JSON
				url = strings.ReplaceAll(url, `\/`, `/`)
				url = strings.ReplaceAll(url, `\u0026`, `&`)
				
				if !seen[url] {
					seen[url] = true
					urls = append(urls, url)
				}
			}
		}
	}
	
	return urls
}

// downloadFile downloads a file from URL
func (e *InstagramGraphQLExtractor) downloadFile(url, filename string) error {
	resp, err := SharedDownloadClient.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != 200 {
		return fmt.Errorf("download failed with status %d", resp.StatusCode)
	}
	
	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer file.Close()
	
	_, err = io.Copy(file, resp.Body)
	return err
}