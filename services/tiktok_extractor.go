package services

import (
	"compress/gzip"
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

type TikTokExtractor struct {
	client     *http.Client
	cookiesPath string
	tempDir    string
	workingDir string
}

type TikTokMediaItem struct {
	URL  string `json:"url"`
	Type string `json:"type"` // "photo" or "video"
}

type TikTokPostData struct {
	Text  string             `json:"text"`
	Media []TikTokMediaItem `json:"media"`
}

func NewTikTokExtractor(workingDir, tempDir string) *TikTokExtractor {
	client := &http.Client{
		Timeout: 30 * time.Second,
	}
	
	return &TikTokExtractor{
		client:     client,
		cookiesPath: filepath.Join(workingDir, "tiktok_cookies.txt"),
		tempDir:    tempDir,
		workingDir: workingDir,
	}
}

func (te *TikTokExtractor) ExtractTikTokPost(tiktokURL string) ([]*MediaInfo, error) {
	log.Printf("🎵 Starting custom TikTok extraction for: %s", tiktokURL)
	
	// Extract post details from URL
	postID, username, err := te.extractTikTokDetails(tiktokURL)
	if err != nil {
		return nil, fmt.Errorf("failed to extract TikTok details: %v", err)
	}
	
	log.Printf("🔍 Extracted TikTok post ID: %s, username: %s", postID, username)
	
	// Try multiple extraction methods
	methods := []func(string, string, string) ([]*MediaInfo, error){
		te.extractViaDirectScraping,
		te.extractViaVxTikTokProxy,
		te.extractViaSimpleTextFallback,
	}
	
	for i, method := range methods {
		log.Printf("📡 Trying TikTok extraction method %d/%d", i+1, len(methods))
		mediaInfos, err := method(tiktokURL, postID, username)
		if err == nil && len(mediaInfos) > 0 {
			log.Printf("✅ Success with TikTok method %d: found %d items", i+1, len(mediaInfos))
			return mediaInfos, nil
		}
		log.Printf("❌ TikTok method %d failed: %v", i+1, err)
	}
	
	return nil, fmt.Errorf("all TikTok extraction methods failed")
}

func (te *TikTokExtractor) extractTikTokDetails(tiktokURL string) (string, string, error) {
	// Extract post ID and username from various TikTok URL formats
	patterns := []string{
		// Photo slideshow URLs
		`tiktok\.com/@([^/]+)/photo/(\d+)`,
		// Regular video URLs  
		`tiktok\.com/@([^/]+)/video/(\d+)`,
		// Short vm.tiktok.com URLs (need to resolve first)
		`vm\.tiktok\.com/([A-Za-z0-9]+)`,
	}
	
	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindStringSubmatch(tiktokURL)
		if len(matches) > 2 {
			username := matches[1]
			postID := matches[2]
			return postID, username, nil
		} else if len(matches) > 1 && strings.Contains(pattern, "vm.tiktok.com") {
			// For vm.tiktok.com URLs, we need to resolve the redirect first
			resolvedURL, err := te.resolveShortURL(tiktokURL)
			if err != nil {
				continue
			}
			return te.extractTikTokDetails(resolvedURL)
		}
	}
	
	return "", "", fmt.Errorf("no TikTok details found in URL: %s", tiktokURL)
}

func (te *TikTokExtractor) resolveShortURL(shortURL string) (string, error) {
	// Create a request that doesn't follow redirects
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Timeout: 10 * time.Second,
	}
	
	req, err := http.NewRequest("GET", shortURL, nil)
	if err != nil {
		return "", err
	}
	
	req.Header.Set("User-Agent", "Mozilla/5.0 (iPhone; CPU iPhone OS 15_0 like Mac OS X) AppleWebKit/605.1.15")
	
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	
	// Get the redirect location
	if location := resp.Header.Get("Location"); location != "" {
		log.Printf("🔗 Resolved %s to %s", shortURL, location)
		return location, nil
	}
	
	return "", fmt.Errorf("no redirect found for %s", shortURL)
}

func (te *TikTokExtractor) extractViaDirectScraping(tiktokURL string, postID string, username string) ([]*MediaInfo, error) {
	log.Printf("🔧 Trying direct TikTok scraping for post %s by @%s", postID, username)
	
	// Create request
	req, err := http.NewRequest("GET", tiktokURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create TikTok request: %v", err)
	}
	
	// Add cookies if available
	te.addCookiesToRequest(req)
	
	// Add headers to mimic a real mobile browser  
	req.Header.Set("User-Agent", "Mozilla/5.0 (iPhone; CPU iPhone OS 15_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/15.0 Mobile/15E148 Safari/604.1")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	req.Header.Set("DNT", "1")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Upgrade-Insecure-Requests", "1")
	
	// Make request
	resp, err := te.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch TikTok post: %v", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("TikTok HTTP %d: %s", resp.StatusCode, resp.Status)
	}
	
	// Handle compressed response
	var reader io.Reader = resp.Body
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gzipReader, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to create gzip reader: %v", err)
		}
		defer gzipReader.Close()
		reader = gzipReader
	}
	
	// Read response
	body, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to read TikTok response: %v", err)
	}
	
	// Debug: Log response info
	bodyStr := string(body)
	log.Printf("🔍 TikTok response length: %d bytes", len(bodyStr))
	
	// Parse HTML and extract media URLs
	return te.parseHTMLForMedia(bodyStr, postID, username)
}

func (te *TikTokExtractor) extractViaVxTikTokProxy(tiktokURL string, postID string, username string) ([]*MediaInfo, error) {
	log.Printf("🔄 Trying vxtiktok.com extraction for post %s by @%s", postID, username)
	
	// Convert to vxtiktok URL
	vxURL := strings.Replace(tiktokURL, "tiktok.com", "vxtiktok.com", 1)
	vxURL = strings.Replace(vxURL, "vm.tiktok.com", "vm.vxtiktok.com", 1)
	
	// Create request
	req, err := http.NewRequest("GET", vxURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create vxtiktok request: %v", err)
	}
	
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; TikTokBot/1.0)")
	
	resp, err := te.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch vxtiktok page: %v", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("vxtiktok HTTP %d: %s", resp.StatusCode, resp.Status)
	}
	
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read vxtiktok response: %v", err)
	}
	
	bodyStr := string(body)
	log.Printf("🔍 VxTikTok response length: %d bytes", len(bodyStr))
	
	// Parse vxtiktok HTML for media
	return te.parseVxTikTokHTML(bodyStr, postID, username)
}

func (te *TikTokExtractor) extractViaSimpleTextFallback(tiktokURL string, postID string, username string) ([]*MediaInfo, error) {
	log.Printf("📝 Trying simple text fallback for TikTok post %s by @%s", postID, username)
	
	// As a last resort, just return a message with the vxtiktok link
	vxURL := strings.Replace(tiktokURL, "tiktok.com", "vxtiktok.com", 1)
	vxURL = strings.Replace(vxURL, "vm.tiktok.com", "vm.vxtiktok.com", 1)
	
	textMediaInfo := &MediaInfo{
		Type:    "text",
		URL:     tiktokURL,
		Caption: fmt.Sprintf("🎵 TikTok content (photos may be available): %s", vxURL),
		Filename: "",
	}
	
	return []*MediaInfo{textMediaInfo}, nil
}

func (te *TikTokExtractor) parseHTMLForMedia(html string, postID string, username string) ([]*MediaInfo, error) {
	var mediaInfos []*MediaInfo
	
	// Extract post text
	postText := te.extractPostText(html)
	log.Printf("📝 Extracted TikTok text: %.200s", postText)
	
	// Extract media URLs using regex patterns
	mediaURLs := te.extractMediaURLs(html)
	log.Printf("📸 Extracted %d TikTok media URLs: %v", len(mediaURLs), mediaURLs)
	
	// Download each media item
	for i, mediaURL := range mediaURLs {
		mediaInfo, err := te.downloadMedia(mediaURL, postID, i)
		if err != nil {
			log.Printf("⚠️ Failed to download TikTok media %d: %v", i, err)
			continue
		}
		
		// Use post text as caption
		if postText != "" {
			mediaInfo.Caption = postText
		}
		
		mediaInfos = append(mediaInfos, mediaInfo)
	}
	
	// If no media found but we have text, return text-only
	if len(mediaInfos) == 0 && postText != "" {
		log.Printf("📝 No TikTok media found, returning text content")
		textMedia := &MediaInfo{
			Type:    "text",
			URL:     fmt.Sprintf("https://tiktok.com/@%s/photo/%s", username, postID),
			Caption: postText,
			Filename: "",
		}
		mediaInfos = append(mediaInfos, textMedia)
	}
	
	return mediaInfos, nil
}

func (te *TikTokExtractor) parseVxTikTokHTML(html string, postID string, username string) ([]*MediaInfo, error) {
	// Similar to parseHTMLForMedia but with vxtiktok-specific patterns
	return te.parseHTMLForMedia(html, postID, username)
}

func (te *TikTokExtractor) extractPostText(html string) string {
	// TikTok-specific patterns for post text
	patterns := []string{
		// JSON embedded text (most common in TikTok)
		`"desc":"([^"]*)"`,
		`"description":"([^"]*)"`,
		`"title":"([^"]*)"`,
		
		// HTML meta tags
		`<meta[^>]*property="og:description"[^>]*content="([^"]*)"`,
		`<meta[^>]*name="description"[^>]*content="([^"]*)"`,
		`<title[^>]*>([^<]*?)(?: - TikTok| on TikTok|$)</title>`,
		
		// HTML elements
		`<div[^>]*class="[^"]*caption[^"]*"[^>]*>(.*?)</div>`,
		`<p[^>]*class="[^"]*description[^"]*"[^>]*>(.*?)</p>`,
	}
	
	for _, pattern := range patterns {
		re := regexp.MustCompile(`(?s)` + pattern)
		matches := re.FindStringSubmatch(html)
		if len(matches) > 1 {
			// Clean up and decode
			text := matches[1]
			
			// Decode JSON escape sequences
			text = strings.ReplaceAll(text, `\"`, `"`)
			text = strings.ReplaceAll(text, `\\n`, "\n")
			text = strings.ReplaceAll(text, `\\t`, "\t")
			text = strings.ReplaceAll(text, `\\r`, "\r")
			
			// Clean up HTML tags
			text = regexp.MustCompile(`<[^>]*>`).ReplaceAllString(text, "")
			
			text = strings.TrimSpace(text)
			if text != "" && text != "null" && !strings.Contains(text, "tiktok") {
				return text
			}
		}
	}
	
	return ""
}

func (te *TikTokExtractor) extractMediaURLs(html string) []string {
	var urls []string
	
	// TikTok-specific patterns for media URLs
	patterns := []string{
		// Photo slideshow images
		`"imagePost"[^}]*"url":"([^"]*)"`,
		`"images":\[.*?"url":"([^"]*)"`,
		`"imageURL"[^}]*"url":"([^"]*)"`,
		
		// Direct image URLs
		`https://[^"]*\.tiktokcdn\.com/[^"]*\.(?:jpg|jpeg|png|gif|webp)`,
		`https://[^"]*\.tiktokcdn-us\.com/[^"]*\.(?:jpg|jpeg|png|gif|webp)`,
		
		// Video URLs
		`"playAddr":"([^"]*)"`,
		`"downloadAddr":"([^"]*)"`,
		`https://[^"]*\.tiktokcdn\.com/[^"]*\.mp4`,
		
		// Generic patterns
		`"url":"(https://[^"]*\.(?:jpg|jpeg|png|gif|webp|mp4)[^"]*)"`,
	}
	
	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		if strings.Contains(pattern, "(") {
			// Pattern has capture group
			matches := re.FindAllStringSubmatch(html, -1)
			for _, match := range matches {
				if len(match) > 1 {
					// Decode URL escaping
					url := strings.ReplaceAll(match[1], `\/`, `/`)
					url = strings.ReplaceAll(url, `\u0026`, `&`)
					urls = append(urls, url)
				}
			}
		} else {
			// Pattern matches full URL
			matches := re.FindAllString(html, -1)
			for _, match := range matches {
				// Decode URL escaping  
				url := strings.ReplaceAll(match, `\/`, `/`)
				url = strings.ReplaceAll(url, `\u0026`, `&`)
				urls = append(urls, url)
			}
		}
	}
	
	// Remove duplicates
	seen := make(map[string]bool)
	var uniqueURLs []string
	for _, url := range urls {
		if !seen[url] && url != "" {
			seen[url] = true
			uniqueURLs = append(uniqueURLs, url)
		}
	}
	
	return uniqueURLs
}

func (te *TikTokExtractor) downloadMedia(mediaURL, postID string, index int) (*MediaInfo, error) {
	log.Printf("⬇️ Downloading TikTok media: %s", mediaURL)
	
	// Create request
	req, err := http.NewRequest("GET", mediaURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create download request: %v", err)
	}
	
	// Add headers
	req.Header.Set("User-Agent", "Mozilla/5.0 (iPhone; CPU iPhone OS 15_0 like Mac OS X) AppleWebKit/605.1.15")
	req.Header.Set("Referer", "https://www.tiktok.com/")
	
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
	filename := filepath.Join(te.tempDir, fmt.Sprintf("%d_tiktok_%s_%d%s", timestamp, postID, index, ext))
	
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
	
	log.Printf("✅ Downloaded TikTok %s: %s", mediaType, filename)
	
	return &MediaInfo{
		Type:     mediaType,
		URL:      fmt.Sprintf("https://tiktok.com/@username/photo/%s", postID),
		Caption:  "Downloaded from TikTok",
		Filename: filename,
	}, nil
}

func (te *TikTokExtractor) getFileExtension(url, contentType string) string {
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

func (te *TikTokExtractor) addCookiesToRequest(req *http.Request) {
	// Read cookies file if it exists
	if _, err := os.Stat(te.cookiesPath); os.IsNotExist(err) {
		log.Printf("⚠️ No TikTok cookies file found at %s", te.cookiesPath)
		return
	}
	
	cookiesData, err := os.ReadFile(te.cookiesPath)
	if err != nil {
		log.Printf("⚠️ Failed to read TikTok cookies file: %v", err)
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
			
			// Only add cookies for tiktok domains
			if strings.Contains(domain, "tiktok.com") {
				// Clean up cookie value
				value = strings.Trim(value, `"`)
				value = strings.ReplaceAll(value, `\"`, `"`)
				value = strings.ReplaceAll(value, `\n`, ``)
				value = strings.ReplaceAll(value, `\r`, ``)
				value = strings.ReplaceAll(value, `\t`, ``)
				
				// Skip cookies with invalid characters
				if strings.ContainsAny(value, "\n\r\t\"") {
					log.Printf("⚠️ Skipping invalid TikTok cookie %s", name)
					continue
				}
				
				cookie := &http.Cookie{
					Name:  name,
					Value: value,
				}
				req.AddCookie(cookie)
			}
		}
	}
	
	log.Printf("🍪 Added TikTok cookies to request")
}