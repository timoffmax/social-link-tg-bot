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

type TwitterExtractor struct {
	client     *http.Client
	cookiesPath string
	tempDir    string
	workingDir string
}

type TwitterMediaItem struct {
	URL  string `json:"url"`
	Type string `json:"type"` // "photo" or "video"
}

type TwitterTweetData struct {
	Text  string             `json:"text"`
	Media []TwitterMediaItem `json:"media"`
}

func NewTwitterExtractor(workingDir, tempDir string) *TwitterExtractor {
	client := &http.Client{
		Timeout: 30 * time.Second,
	}
	
	return &TwitterExtractor{
		client:     client,
		cookiesPath: filepath.Join(workingDir, "twitter_cookies.txt"),
		tempDir:    tempDir,
		workingDir: workingDir,
	}
}

func (te *TwitterExtractor) ExtractTweet(twitterURL string) ([]*MediaInfo, error) {
	log.Printf("🐦 Starting custom Twitter extraction for: %s", twitterURL)
	
	// Extract tweet ID from URL
	tweetID, err := te.extractTweetID(twitterURL)
	if err != nil {
		return nil, fmt.Errorf("failed to extract tweet ID: %v", err)
	}
	
	log.Printf("🔍 Extracted tweet ID: %s", tweetID)
	
	// Try multiple extraction methods
	methods := []func(string) ([]*MediaInfo, error){
		te.extractViaWebInterface,
		te.extractViaEmbedAPI,
		te.extractViaDirectScraping,
	}
	
	for i, method := range methods {
		log.Printf("📡 Trying extraction method %d/%d", i+1, len(methods))
		mediaInfos, err := method(tweetID)
		if err == nil && len(mediaInfos) > 0 {
			log.Printf("✅ Success with method %d: found %d items", i+1, len(mediaInfos))
			return mediaInfos, nil
		}
		log.Printf("❌ Method %d failed: %v", i+1, err)
	}
	
	return nil, fmt.Errorf("all extraction methods failed")
}

func (te *TwitterExtractor) extractTweetID(twitterURL string) (string, error) {
	// Extract tweet ID from various Twitter URL formats
	patterns := []string{
		`twitter\.com/\w+/status/(\d+)`,
		`x\.com/\w+/status/(\d+)`,
		`mobile\.twitter\.com/\w+/status/(\d+)`,
	}
	
	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindStringSubmatch(twitterURL)
		if len(matches) > 1 {
			return matches[1], nil
		}
	}
	
	return "", fmt.Errorf("no tweet ID found in URL: %s", twitterURL)
}

func (te *TwitterExtractor) extractViaWebInterface(tweetID string) ([]*MediaInfo, error) {
	log.Printf("🌐 Trying web interface extraction for tweet %s", tweetID)
	
	// Construct tweet URL
	tweetURL := fmt.Sprintf("https://x.com/i/web/status/%s", tweetID)
	
	// Create request
	req, err := http.NewRequest("GET", tweetURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}
	
	// Add cookies if available
	te.addCookiesToRequest(req)
	
	// Add headers to mimic a real browser
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	req.Header.Set("DNT", "1")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Upgrade-Insecure-Requests", "1")
	
	// Make request
	resp, err := te.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch tweet: %v", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}
	
	// Read response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %v", err)
	}
	
	// Parse HTML and extract media URLs
	return te.parseHTMLForMedia(string(body), tweetID)
}

func (te *TwitterExtractor) extractViaEmbedAPI(tweetID string) ([]*MediaInfo, error) {
	log.Printf("🔗 Trying embed API extraction for tweet %s", tweetID)
	
	// Use Twitter's oembed API
	embedURL := fmt.Sprintf("https://publish.twitter.com/oembed?url=https://x.com/i/web/status/%s", tweetID)
	
	req, err := http.NewRequest("GET", embedURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create embed request: %v", err)
	}
	
	resp, err := te.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch embed: %v", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("embed API HTTP %d: %s", resp.StatusCode, resp.Status)
	}
	
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read embed response: %v", err)
	}
	
	// Parse embed response
	return te.parseEmbedResponse(string(body), tweetID)
}

func (te *TwitterExtractor) extractViaDirectScraping(tweetID string) ([]*MediaInfo, error) {
	log.Printf("🔧 Trying direct scraping for tweet %s", tweetID)
	
	// Try to get tweet via mobile interface (sometimes more permissive)
	mobileURL := fmt.Sprintf("https://mobile.x.com/i/web/status/%s", tweetID)
	
	req, err := http.NewRequest("GET", mobileURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create mobile request: %v", err)
	}
	
	// Mobile user agent
	req.Header.Set("User-Agent", "Mozilla/5.0 (iPhone; CPU iPhone OS 15_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/15.0 Mobile/15E148 Safari/604.1")
	
	te.addCookiesToRequest(req)
	
	resp, err := te.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch mobile tweet: %v", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("mobile HTTP %d: %s", resp.StatusCode, resp.Status)
	}
	
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read mobile response: %v", err)
	}
	
	return te.parseHTMLForMedia(string(body), tweetID)
}

func (te *TwitterExtractor) parseHTMLForMedia(html, tweetID string) ([]*MediaInfo, error) {
	var mediaInfos []*MediaInfo
	
	// Extract tweet text
	tweetText := te.extractTweetText(html)
	
	// Extract media URLs using regex patterns
	mediaURLs := te.extractMediaURLs(html)
	
	log.Printf("📊 Found %d media URLs and tweet text: %.100s", len(mediaURLs), tweetText)
	
	// Download each media item
	for i, mediaURL := range mediaURLs {
		mediaInfo, err := te.downloadMedia(mediaURL, tweetID, i)
		if err != nil {
			log.Printf("⚠️ Failed to download media %d: %v", i, err)
			continue
		}
		mediaInfos = append(mediaInfos, mediaInfo)
	}
	
	// If no media found but we have text, return text-only
	if len(mediaInfos) == 0 && tweetText != "" {
		log.Printf("📝 No media found, returning text content")
		textMedia := &MediaInfo{
			Type:    "text",
			URL:     fmt.Sprintf("https://x.com/i/web/status/%s", tweetID),
			Caption: tweetText,
			Filename: "",
		}
		mediaInfos = append(mediaInfos, textMedia)
	}
	
	return mediaInfos, nil
}

func (te *TwitterExtractor) parseEmbedResponse(response, tweetID string) ([]*MediaInfo, error) {
	// Parse JSON response from embed API
	var embedData map[string]interface{}
	if err := json.Unmarshal([]byte(response), &embedData); err != nil {
		return nil, fmt.Errorf("failed to parse embed JSON: %v", err)
	}
	
	// Extract HTML from embed response
	html, ok := embedData["html"].(string)
	if !ok {
		return nil, fmt.Errorf("no HTML in embed response")
	}
	
	return te.parseHTMLForMedia(html, tweetID)
}

func (te *TwitterExtractor) extractTweetText(html string) string {
	// Multiple patterns to extract tweet text
	patterns := []string{
		`<div[^>]*data-testid="tweetText"[^>]*>(.*?)</div>`,
		`<div[^>]*class="[^"]*tweet-text[^"]*"[^>]*>(.*?)</div>`,
		`<p[^>]*class="[^"]*TweetTextSize[^"]*"[^>]*>(.*?)</p>`,
	}
	
	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindStringSubmatch(html)
		if len(matches) > 1 {
			// Clean up HTML tags and decode entities
			text := strings.ReplaceAll(matches[1], "<br>", "\n")
			text = regexp.MustCompile(`<[^>]*>`).ReplaceAllString(text, "")
			text = strings.TrimSpace(text)
			if text != "" {
				return text
			}
		}
	}
	
	return ""
}

func (te *TwitterExtractor) extractMediaURLs(html string) []string {
	var urls []string
	
	// Patterns to match image and video URLs
	patterns := []string{
		`https://pbs\.twimg\.com/media/[^"'\s]+\.(?:jpg|jpeg|png|gif|webp)`,
		`https://video\.twimg\.com/[^"'\s]+\.(?:mp4|m3u8)`,
		`https://pbs\.twimg\.com/tweet_video/[^"'\s]+\.(?:mp4|gif)`,
	}
	
	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindAllString(html, -1)
		urls = append(urls, matches...)
	}
	
	// Remove duplicates
	seen := make(map[string]bool)
	var uniqueURLs []string
	for _, url := range urls {
		if !seen[url] {
			seen[url] = true
			uniqueURLs = append(uniqueURLs, url)
		}
	}
	
	return uniqueURLs
}

func (te *TwitterExtractor) downloadMedia(mediaURL, tweetID string, index int) (*MediaInfo, error) {
	log.Printf("⬇️ Downloading media: %s", mediaURL)
	
	// Create request
	req, err := http.NewRequest("GET", mediaURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create download request: %v", err)
	}
	
	// Add headers
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Referer", "https://x.com/")
	
	// Download
	resp, err := te.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to download: %v", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("download HTTP %d: %s", resp.StatusCode, resp.Status)
	}
	
	// Determine file extension
	ext := te.getFileExtension(mediaURL, resp.Header.Get("Content-Type"))
	
	// Create filename
	timestamp := time.Now().Unix()
	filename := filepath.Join(te.tempDir, fmt.Sprintf("%d_twitter_%s_%d%s", timestamp, tweetID, index, ext))
	
	// Create file
	file, err := os.Create(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to create file: %v", err)
	}
	defer file.Close()
	
	// Copy data
	_, err = io.Copy(file, resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to save file: %v", err)
	}
	
	// Determine media type
	mediaType := "document"
	if strings.Contains(ext, ".jpg") || strings.Contains(ext, ".jpeg") || strings.Contains(ext, ".png") || strings.Contains(ext, ".gif") || strings.Contains(ext, ".webp") {
		mediaType = "photo"
	} else if strings.Contains(ext, ".mp4") || strings.Contains(ext, ".mov") || strings.Contains(ext, ".avi") {
		mediaType = "video"
	}
	
	log.Printf("✅ Downloaded %s: %s", mediaType, filename)
	
	return &MediaInfo{
		Type:     mediaType,
		URL:      fmt.Sprintf("https://x.com/i/web/status/%s", tweetID),
		Caption:  fmt.Sprintf("Downloaded from Twitter"),
		Filename: filename,
	}, nil
}

func (te *TwitterExtractor) getFileExtension(url, contentType string) string {
	// Try to get extension from URL
	if ext := filepath.Ext(url); ext != "" {
		return ext
	}
	
	// Fall back to content type
	switch contentType {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "video/mp4":
		return ".mp4"
	default:
		return ".bin"
	}
}

func (te *TwitterExtractor) addCookiesToRequest(req *http.Request) {
	// Read cookies file if it exists
	if _, err := os.Stat(te.cookiesPath); os.IsNotExist(err) {
		log.Printf("⚠️ No Twitter cookies file found at %s", te.cookiesPath)
		return
	}
	
	cookiesData, err := os.ReadFile(te.cookiesPath)
	if err != nil {
		log.Printf("⚠️ Failed to read cookies file: %v", err)
		return
	}
	
	// Parse Netscape cookie format and add to request
	lines := strings.Split(string(cookiesData), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		
		parts := strings.Split(line, "\t")
		if len(parts) >= 7 {
			domain := parts[0]
			name := parts[5]
			value := parts[6]
			
			// Only add cookies for twitter/x domains
			if strings.Contains(domain, "twitter.com") || strings.Contains(domain, "x.com") {
				cookie := &http.Cookie{
					Name:  name,
					Value: value,
				}
				req.AddCookie(cookie)
			}
		}
	}
	
	log.Printf("🍪 Added Twitter cookies to request")
}