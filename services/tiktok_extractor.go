package services

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	
	"github.com/chromedp/chromedp"
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
		te.extractViaSlideshowAPI,
		te.extractViaVxTikTokProxy,
		te.extractViaHeadlessBrowser,
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
	// First, if it's a short URL, resolve it
	if strings.Contains(tiktokURL, "vm.tiktok.com") {
		log.Printf("🔗 Resolving short TikTok URL: %s", tiktokURL)
		resolvedURL, err := te.resolveShortURL(tiktokURL)
		if err != nil {
			return "", "", fmt.Errorf("failed to resolve short URL: %v", err)
		}
		log.Printf("🔗 Resolved to: %s", resolvedURL)
		return te.extractTikTokDetails(resolvedURL)
	}
	
	// Extract post ID and username from various TikTok URL formats
	patterns := []string{
		// Photo slideshow URLs
		`tiktok\.com/@([^/]+)/photo/(\d+)`,
		// Regular video URLs  
		`tiktok\.com/@([^/]+)/video/(\d+)`,
	}
	
	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindStringSubmatch(tiktokURL)
		if len(matches) > 2 {
			username := matches[1]
			postID := matches[2]
			return postID, username, nil
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
	// Don't accept compression to avoid binary data issues
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("DNT", "1")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Upgrade-Insecure-Requests", "1")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "none")
	
	// Make request
	resp, err := te.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch TikTok post: %v", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("TikTok HTTP %d: %s", resp.StatusCode, resp.Status)
	}
	
	// Handle compressed response with better detection
	var reader io.Reader = resp.Body
	contentEncoding := resp.Header.Get("Content-Encoding")
	log.Printf("🔍 TikTok response Content-Encoding: %s", contentEncoding)
	
	if contentEncoding == "gzip" {
		gzipReader, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to create gzip reader: %v", err)
		}
		defer gzipReader.Close()
		reader = gzipReader
		log.Printf("✅ Using gzip reader for TikTok response")
	} else if contentEncoding == "br" {
		// TikTok might use brotli compression
		log.Printf("⚠️ TikTok using brotli compression - not supported, trying raw")
	} else if contentEncoding == "deflate" {
		// TikTok might use deflate compression  
		log.Printf("⚠️ TikTok using deflate compression - not supported, trying raw")
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

func (te *TikTokExtractor) extractViaSlideshowAPI(tiktokURL string, postID string, username string) ([]*MediaInfo, error) {
	log.Printf("📸 Trying TikTok slideshow API extraction for post %s by @%s", postID, username)
	
	// Try different TikTok API endpoints that might work for slideshows
	apiEndpoints := []string{
		// Mobile web API (sometimes works)
		fmt.Sprintf("https://m.tiktok.com/api/item/detail/?itemId=%s", postID),
		// Alternative mobile API
		fmt.Sprintf("https://www.tiktok.com/api/item/detail/?itemId=%s", postID),
		// Public web API (if available)
		fmt.Sprintf("https://www.tiktok.com/node/share/video/@%s/%s", username, postID),
	}
	
	for _, apiURL := range apiEndpoints {
		log.Printf("🔗 Trying TikTok API: %s", apiURL)
		
		req, err := http.NewRequest("GET", apiURL, nil)
		if err != nil {
			continue
		}
		
		// Add mobile headers
		req.Header.Set("User-Agent", "Mozilla/5.0 (iPhone; CPU iPhone OS 15_0 like Mac OS X) AppleWebKit/605.1.15")
		req.Header.Set("Accept", "application/json, text/plain, */*")
		req.Header.Set("Referer", tiktokURL)
		
		// Add cookies if available
		te.addCookiesToRequest(req)
		
		resp, err := te.client.Do(req)
		if err != nil {
			log.Printf("❌ API request failed: %v", err)
			continue
		}
		defer resp.Body.Close()
		
		if resp.StatusCode != 200 {
			log.Printf("❌ API HTTP %d: %s", resp.StatusCode, resp.Status)
			continue
		}
		
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			log.Printf("❌ Failed to read API response: %v", err)
			continue
		}
		
		bodyStr := string(body)
		log.Printf("📊 API response length: %d bytes", len(bodyStr))
		
		// Try to parse JSON response for media URLs
		mediaInfos, err := te.parseAPIResponse(bodyStr, postID, username)
		if err == nil && len(mediaInfos) > 0 {
			log.Printf("✅ Successfully extracted %d items from API", len(mediaInfos))
			return mediaInfos, nil
		}
		
		log.Printf("❌ Failed to extract media from API response: %v", err)
	}
	
	return nil, fmt.Errorf("all TikTok API endpoints failed")
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

func (te *TikTokExtractor) extractViaHeadlessBrowser(tiktokURL string, postID string, username string) ([]*MediaInfo, error) {
	log.Printf("🌐 Trying headless browser extraction for TikTok post %s by @%s", postID, username)
	
	// Create a context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	
	// Create Chrome instance with extensive logging suppression and cookie handling fixes
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.DisableGPU,
		chromedp.NoSandbox,
		chromedp.Headless,
		chromedp.UserAgent("Mozilla/5.0 (iPhone; CPU iPhone OS 15_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/15.0 Mobile/15E148 Safari/604.1"),
		chromedp.Flag("disable-web-security", true),
		chromedp.Flag("disable-extensions", true),
		chromedp.Flag("disable-plugins", true),
		chromedp.Flag("disable-images", false), // We need images for TikTok
		chromedp.Flag("disable-javascript", false), // We need JS for TikTok
		chromedp.Flag("mute-audio", true),
		chromedp.Flag("no-first-run", true),
		chromedp.Flag("no-default-browser-check", true),
		chromedp.Flag("disable-default-apps", true),
		chromedp.Flag("disable-popup-blocking", true),
		chromedp.Flag("disable-translate", true),
		chromedp.Flag("disable-background-timer-throttling", true),
		chromedp.Flag("disable-renderer-backgrounding", true),
		chromedp.Flag("disable-backgrounding-occluded-windows", true),
		chromedp.Flag("disable-ipc-flooding-protection", true),
		chromedp.Flag("enable-features", "VizDisplayCompositor"),
		// Extensive logging suppression
		chromedp.Flag("log-level", "3"), // Only show fatal errors
		chromedp.Flag("silent", true),
		chromedp.Flag("disable-logging", true),
		chromedp.Flag("quiet", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		// Cookie-related error suppression
		chromedp.Flag("disable-cookie-encryption", true),
		chromedp.Flag("disable-sync", true),
		chromedp.Flag("disable-background-networking", true),
		chromedp.Flag("disable-component-update", true),
		chromedp.Flag("disable-domain-reliability", true),
		chromedp.Flag("disable-features", "VizDisplayCompositor,AudioServiceOutOfProcess"),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-setuid-sandbox", true),
	)
	
	allocCtx, allocCancel := chromedp.NewExecAllocator(ctx, opts...)
	defer allocCancel()
	
	// Create context with custom logger that filters out cookie errors
	browserCtx, browserCancel := chromedp.NewContext(allocCtx, chromedp.WithLogf(func(string, ...interface{}) {}))
	defer browserCancel()
	
	// Navigate to TikTok URL and wait for content to load
	var htmlContent string
	err := chromedp.Run(browserCtx,
		chromedp.Navigate(tiktokURL),
		chromedp.Sleep(5*time.Second), // Wait for JavaScript to load
		chromedp.WaitVisible(`body`, chromedp.ByQuery),
		chromedp.Sleep(3*time.Second), // Additional wait for slideshow content
		chromedp.InnerHTML(`html`, &htmlContent, chromedp.ByQuery),
	)
	
	if err != nil {
		log.Printf("❌ Headless browser navigation failed: %v", err)
		return nil, fmt.Errorf("headless browser navigation failed: %v", err)
	}
	
	log.Printf("🌐 Retrieved HTML content from headless browser, length: %d", len(htmlContent))
	
	// Try to extract slideshow images specifically
	mediaInfos, err := te.extractSlideshowFromHTML(htmlContent, postID, username)
	if err != nil {
		log.Printf("❌ Failed to extract slideshow from browser HTML: %v", err)
		return nil, err
	}
	
	if len(mediaInfos) == 0 {
		log.Printf("❌ No slideshow media found in browser HTML")
		return nil, fmt.Errorf("no slideshow media found")
	}
	
	log.Printf("✅ Headless browser extracted %d media items", len(mediaInfos))
	return mediaInfos, nil
}

func (te *TikTokExtractor) extractSlideshowFromHTML(html string, postID string, username string) ([]*MediaInfo, error) {
	var mediaInfos []*MediaInfo
	
	// Extract post text from JavaScript-rendered content
	postText := te.extractPostTextFromRenderedHTML(html)
	log.Printf("📝 Extracted text from browser HTML: %.200s", postText)
	
	// Enhanced patterns specifically for slideshow images loaded by JavaScript
	slideshowPatterns := []string{
		// 2025 TikTok slideshow patterns after JavaScript execution
		`<img[^>]*src="(https://[^"]*\.tiktokcdn\.com/[^"]*~tplv-photomode-image\.jpeg[^"]*)"[^>]*class="[^"]*ImgPhotoSlide[^"]*"`,
		`<img[^>]*class="[^"]*ImgPhotoSlide[^"]*"[^>]*src="(https://[^"]*\.tiktokcdn\.com/[^"]*~tplv-photomode-image\.jpeg[^"]*)"`,
		
		// Swiper slide images
		`<div[^>]*class="[^"]*swiper-slide[^"]*"[^>]*>.*?<img[^>]*src="(https://[^"]*photomode-image[^"]*\.jpeg[^"]*)"`,
		`swiper-slide[^>]*>.*?<img[^>]*src="(https://[^"]*photomode-image[^"]*\.jpeg[^"]*)"`,
		
		// Any img tag with photomode-image in fully loaded HTML
		`<img[^>]*src="(https://[^"]*photomode-image[^"]*\.jpeg[^"]*)"`,
		
		// Direct CDN URLs that might be in the loaded HTML
		`(https://p[0-9]+-sign-[a-z]+\.tiktokcdn\.com/tos-[^"]*~tplv-photomode-image\.jpeg[^"]*)`,
		`(https://[^"]*\.tiktokcdn\.com/[^"]*photomode-image[^"]*\.jpeg[^"]*)`,
		
		// Data attributes that might contain URLs
		`data-src="(https://[^"]*photomode-image[^"]*\.jpeg[^"]*)"`,
		`data-image="(https://[^"]*photomode-image[^"]*\.jpeg[^"]*)"`,
		
		// Background images in style attributes
		`background-image:\s*url\((https://[^)]*photomode-image[^)]*\.jpeg[^)]*)\)`,
	}
	
	var allURLs []string
	
	for i, pattern := range slideshowPatterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindAllStringSubmatch(html, -1)
		
		if len(matches) > 0 {
			log.Printf("🎯 Slideshow pattern %d matched %d times", i+1, len(matches))
		}
		
		for _, match := range matches {
			if len(match) > 1 {
				url := te.cleanURL(match[1])
				if url != "" {
					allURLs = append(allURLs, url)
				}
			}
		}
	}
	
	// Remove duplicates using better deduplication for TikTok CDN URLs
	seen := make(map[string]bool)
	var uniqueURLs []string
	for _, url := range allURLs {
		if url == "" {
			continue
		}
		
		// Create a normalized base URL for duplicate detection
		baseURL := url
		
		// For TikTok slideshow images, normalize by removing CDN server differences
		if strings.Contains(url, "tiktokcdn.com") && strings.Contains(url, "~tplv-photomode-image") {
			// Extract the unique image hash from the URL for better deduplication
			// URL format: https://p16-sign-sg.tiktokcdn.com/tos-alisg-i-photomode-sg/ccc7d4750fae4a6da2ffdc1a75bdd30b~tplv-photomode-image.jpeg
			if hashStart := strings.LastIndex(url, "/"); hashStart != -1 {
				hashPart := url[hashStart+1:]
				if tildeIndex := strings.Index(hashPart, "~"); tildeIndex != -1 {
					imageHash := hashPart[:tildeIndex]
					baseURL = imageHash // Use just the hash for deduplication
				}
			}
		}
		
		if !seen[baseURL] {
			seen[baseURL] = true
			uniqueURLs = append(uniqueURLs, url)
			log.Printf("✅ Added unique slideshow URL: %s", url)
		} else {
			log.Printf("⏭️ Skipping duplicate slideshow URL (same hash): %s", url)
		}
	}
	
	log.Printf("🖼️ Found %d unique slideshow image URLs", len(uniqueURLs))
	
	// Download each image
	for i, mediaURL := range uniqueURLs {
		mediaInfo, err := te.downloadMedia(mediaURL, postID, i)
		if err != nil {
			log.Printf("⚠️ Failed to download slideshow image %d: %v", i, err)
			continue
		}
		
		// Use post text as caption
		if postText != "" {
			mediaInfo.Caption = postText
		} else {
			// Only set default caption for the first media item
			if i == 0 {
				mediaInfo.Caption = "🎵 TikTok slideshow"
			}
		}
		
		mediaInfos = append(mediaInfos, mediaInfo)
	}
	
	// If no media found, return error
	if len(mediaInfos) == 0 {
		return nil, fmt.Errorf("no slideshow images extracted from HTML")
	}
	
	return mediaInfos, nil
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
		} else if mediaInfo.Caption == "" {
			// Only set default caption for the first media item
			if i == 0 {
				mediaInfo.Caption = "🎵 TikTok"
			}
		}
		
		mediaInfos = append(mediaInfos, mediaInfo)
	}
	
	// If no media found, return empty (let fallback handle it)
	if len(mediaInfos) == 0 {
		log.Printf("📝 No TikTok media found, returning empty for fallback")
		return nil, fmt.Errorf("no media extracted from TikTok post")
	}
	
	return mediaInfos, nil
}

func (te *TikTokExtractor) parseVxTikTokHTML(html string, postID string, username string) ([]*MediaInfo, error) {
	log.Printf("🔍 Parsing VxTikTok HTML for slideshow images")
	
	var mediaInfos []*MediaInfo
	
	// Extract post text from vxtiktok HTML
	postText := te.extractTextFromVxTikTok(html)
	log.Printf("📝 Extracted VxTikTok text: %.200s", postText)
	
	// Look for vxtiktok-specific media patterns
	mediaURLs := te.extractVxTikTokMediaURLs(html)
	log.Printf("📸 Extracted %d VxTikTok media URLs: %v", len(mediaURLs), mediaURLs)
	
	// Download each media item
	for i, mediaURL := range mediaURLs {
		mediaInfo, err := te.downloadMedia(mediaURL, postID, i)
		if err != nil {
			log.Printf("⚠️ Failed to download VxTikTok media %d: %v", i, err)
			continue
		}
		
		// Use post text as caption
		if postText != "" {
			mediaInfo.Caption = postText
		} else if mediaInfo.Caption == "" {
			// Only set default caption for the first media item
			if i == 0 {
				mediaInfo.Caption = "🎵 TikTok"
			}
		}
		
		mediaInfos = append(mediaInfos, mediaInfo)
	}
	
	// If no media found, return empty (let fallback handle it)
	if len(mediaInfos) == 0 {
		log.Printf("📝 No VxTikTok media found, returning empty for fallback")
		return nil, fmt.Errorf("no media extracted from VxTikTok")
	}
	
	return mediaInfos, nil
}

func (te *TikTokExtractor) parseAPIResponse(jsonResponse string, postID string, username string) ([]*MediaInfo, error) {
	log.Printf("🔍 Parsing TikTok API JSON response for media URLs")
	
	var mediaInfos []*MediaInfo
	
	// Extract post text from JSON
	postText := te.extractTextFromJSON(jsonResponse)
	log.Printf("📝 Extracted API text: %.200s", postText)
	
	// Extract media URLs from JSON response using enhanced patterns
	mediaURLs := te.extractMediaURLsFromJSON(jsonResponse)
	log.Printf("📸 Extracted %d API media URLs: %v", len(mediaURLs), mediaURLs)
	
	// Download each media item
	for i, mediaURL := range mediaURLs {
		mediaInfo, err := te.downloadMedia(mediaURL, postID, i)
		if err != nil {
			log.Printf("⚠️ Failed to download API media %d: %v", i, err)
			continue
		}
		
		// Use post text as caption
		if postText != "" {
			mediaInfo.Caption = postText
		} else if mediaInfo.Caption == "" {
			// Only set default caption for the first media item
			if i == 0 {
				mediaInfo.Caption = "🎵 TikTok"
			}
		}
		
		mediaInfos = append(mediaInfos, mediaInfo)
	}
	
	// If no media found, return empty (let fallback handle it)
	if len(mediaInfos) == 0 {
		log.Printf("📝 No API media found, returning empty for fallback")
		return nil, fmt.Errorf("no media extracted from API response")
	}
	
	return mediaInfos, nil
}

func (te *TikTokExtractor) extractTextFromJSON(jsonStr string) string {
	// JSON patterns for text extraction from API responses
	patterns := []string{
		`"desc":"([^"]*)"`,
		`"description":"([^"]*)"`,
		`"title":"([^"]*)"`,
		`"content":"([^"]*)"`,
		`"text":"([^"]*)"`,
		`"caption":"([^"]*)"`,
	}
	
	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindStringSubmatch(jsonStr)
		if len(matches) > 1 {
			text := matches[1]
			
			// Decode JSON escape sequences
			text = strings.ReplaceAll(text, `\"`, `"`)
			text = strings.ReplaceAll(text, `\\n`, "\n")
			text = strings.ReplaceAll(text, `\\t`, "\t")
			text = strings.ReplaceAll(text, `\\r`, "\r")
			
			text = strings.TrimSpace(text)
			if text != "" && text != "null" {
				return text
			}
		}
	}
	
	return ""
}

func (te *TikTokExtractor) extractMediaURLsFromJSON(jsonStr string) []string {
	var urls []string
	
	// JSON patterns for media URLs from API responses
	patterns := []string{
		// Photo slideshow patterns
		`"imagePost"[^}]*"url":"([^"]*)"`,
		`"images":\[[^\]]*"url":"([^"]*)"`,
		`"imageURL":"([^"]*)"`,
		`"photoPost"[^}]*"url":"([^"]*)"`,
		
		// Direct media URL patterns
		`"url":"(https://[^"]*\.(?:tiktokcdn\.com|tiktokcdn-us\.com|muscdn\.com)/[^"]*\.(?:jpg|jpeg|png|gif|webp|mp4)[^"]*)"`,
		`"src":"(https://[^"]*\.(?:tiktokcdn\.com|tiktokcdn-us\.com|muscdn\.com)/[^"]*\.(?:jpg|jpeg|png|gif|webp|mp4)[^"]*)"`,
		
		// Video URLs
		`"playAddr":"([^"]*)"`,
		`"downloadAddr":"([^"]*)"`,
		`"videoUrl":"([^"]*)"`,
	}
	
	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindAllStringSubmatch(jsonStr, -1)
		for _, match := range matches {
			if len(match) > 1 {
				// Decode URL escaping
				url := strings.ReplaceAll(match[1], `\/`, `/`)
				url = strings.ReplaceAll(url, `\u0026`, `&`)
				urls = append(urls, url)
			}
		}
	}
	
	// Remove duplicates and clean URLs
	seen := make(map[string]bool)
	var uniqueURLs []string
	for _, url := range urls {
		if url == "" {
			continue
		}
		
		// Clean up URL: remove HTML entities, JSON escaping, and quotes
		cleanURL := strings.ReplaceAll(url, "&amp;", "&")
		cleanURL = strings.ReplaceAll(cleanURL, "&quot;", "\"")
		cleanURL = strings.ReplaceAll(cleanURL, "\\u002F", "/")
		cleanURL = strings.ReplaceAll(cleanURL, "\\/", "/")
		
		// Remove surrounding quotes if present
		if strings.HasPrefix(cleanURL, "\"") && strings.HasSuffix(cleanURL, "\"") {
			cleanURL = cleanURL[1 : len(cleanURL)-1]
		}
		
		// Skip non-slideshow images (avatars, video-share-cards, etc)
		if strings.Contains(cleanURL, "tiktokx-cropcenter") || 
		   strings.Contains(cleanURL, "video-share-card") ||
		   strings.Contains(cleanURL, "avt-") {
			continue
		}
		
		// For TikTok slideshow images, create a normalized base URL for duplicate detection
		baseURL := cleanURL
		if strings.Contains(cleanURL, "tiktokcdn.com") {
			// Remove query parameters for duplicate detection
			if qIndex := strings.Index(cleanURL, "?"); qIndex != -1 {
				baseURL = cleanURL[:qIndex]
			}
			
			// Extract the unique image hash from the URL for better deduplication
			// URL format: .../9a78140b7d87427482e4392117b4ead9~tplv-photomode-image.jpeg
			if hashStart := strings.LastIndex(baseURL, "/"); hashStart != -1 {
				hashPart := baseURL[hashStart+1:]
				if tildeIndex := strings.Index(hashPart, "~"); tildeIndex != -1 {
					imageHash := hashPart[:tildeIndex]
					baseURL = imageHash // Use just the hash for deduplication
				}
			}
		}
		
		if !seen[baseURL] {
			seen[baseURL] = true
			uniqueURLs = append(uniqueURLs, cleanURL)
			log.Printf("✅ Added unique URL with base: %s", baseURL)
		} else {
			log.Printf("⏭️ Skipping duplicate URL with base: %s", baseURL)
		}
	}
	
	log.Printf("🔍 Found %d unique URLs after deduplication", len(uniqueURLs))
	
	return uniqueURLs
}

// extractPostTextFromRenderedHTML extracts post text from JavaScript-rendered HTML
func (te *TikTokExtractor) extractPostTextFromRenderedHTML(html string) string {
	// Enhanced patterns specifically for JavaScript-rendered TikTok content
	patterns := []string{
		// 2025 TikTok structure patterns - post content after JS rendering
		`<h1[^>]*data-e2e="browse-video-desc"[^>]*>([^<]*)</h1>`,
		`<div[^>]*data-e2e="browse-video-desc"[^>]*>([^<]*)</div>`,
		`<span[^>]*data-e2e="browse-video-desc"[^>]*>([^<]*)</span>`,
		
		// Caption containers in rendered HTML
		`<div[^>]*class="[^"]*tiktok-.*?caption[^"]*"[^>]*>([^<]*)</div>`,
		`<div[^>]*class="[^"]*caption[^"]*"[^>]*>([^<]*)</div>`,
		`<span[^>]*class="[^"]*caption[^"]*"[^>]*>([^<]*)</span>`,
		
		// Video info containers
		`<div[^>]*class="[^"]*video-info[^"]*"[^>]*>.*?<span[^>]*>([^<]*)</span>`,
		`<div[^>]*class="[^"]*video-meta[^"]*"[^>]*>.*?<span[^>]*>([^<]*)</span>`,
		
		// Photo slideshow specific containers
		`<div[^>]*class="[^"]*DivPhotoModeContainer[^"]*"[^>]*>.*?<span[^>]*>([^<]*)</span>`,
		`<div[^>]*class="[^"]*PhotoModeContainer[^"]*"[^>]*>.*?<span[^>]*>([^<]*)</span>`,
		
		// Common content containers that may contain the caption
		`<div[^>]*class="[^"]*content[^"]*"[^>]*>.*?<span[^>]*>([^<]*)</span>`,
		`<div[^>]*class="[^"]*description[^"]*"[^>]*>([^<]*)</div>`,
		
		// Try to find any meaningful text in script tags (JSON data)
		`"desc":"([^"]*)"`,
		`"description":"([^"]*)"`,
		`"title":"([^"]*)"`,
		`"content":"([^"]*)"`,
		`"text":"([^"]*)"`,
		`"caption":"([^"]*)"`,
		
		// Look for structured data
		`<script[^>]*type="application/ld\+json"[^>]*>.*?"description":"([^"]*)"`,
		`<script[^>]*type="application/ld\+json"[^>]*>.*?"text":"([^"]*)"`,
		
		// Fallback to any text that's not generic
		`<h1[^>]*>([^<]*)</h1>`,
		`<h2[^>]*>([^<]*)</h2>`,
		`<p[^>]*>([^<]*)</p>`,
		
		// Meta description as absolute fallback
		`<meta[^>]*property="og:description"[^>]*content="([^"]*)"`,
		`<meta[^>]*name="description"[^>]*content="([^"]*)"`,
	}
	
	for i, pattern := range patterns {
		re := regexp.MustCompile(`(?s)` + pattern)
		matches := re.FindStringSubmatch(html)
		if len(matches) > 1 {
			text := matches[1]
			
			// Decode JSON escape sequences
			text = strings.ReplaceAll(text, `\"`, `"`)
			text = strings.ReplaceAll(text, `\\n`, "\n")
			text = strings.ReplaceAll(text, `\\t`, "\t")
			text = strings.ReplaceAll(text, `\\r`, "\r")
			text = strings.ReplaceAll(text, `\u002F`, "/")
			text = strings.ReplaceAll(text, `\/`, "/")
			
			// Clean up HTML entities
			text = strings.ReplaceAll(text, "&amp;", "&")
			text = strings.ReplaceAll(text, "&quot;", "\"")
			text = strings.ReplaceAll(text, "&lt;", "<")
			text = strings.ReplaceAll(text, "&gt;", ">")
			text = strings.ReplaceAll(text, "&nbsp;", " ")
			
			// Remove HTML tags
			text = regexp.MustCompile(`<[^>]*>`).ReplaceAllString(text, "")
			
			text = strings.TrimSpace(text)
			
			// Skip empty, null, or generic content
			if text != "" && text != "null" && !isGenericTikTokText(text) && len(text) > 3 {
				// For non-meta patterns (first 20), prefer meaningful content
				if i < 20 && (strings.Contains(strings.ToLower(text), "check out") || strings.Contains(strings.ToLower(text), "post")) {
					// This looks like a meta description, continue searching
					continue
				}
				
				log.Printf("✅ Found meaningful text with pattern %d: %s", i+1, text)
				return text
			}
		}
	}
	
	// If no meaningful text found, try to extract from any visible text elements
	// Look for the longest non-generic text
	textCandidates := te.extractAllVisibleText(html)
	if len(textCandidates) > 0 {
		for _, candidate := range textCandidates {
			if !isGenericTikTokText(candidate) && len(candidate) > 10 && 
			   !strings.Contains(strings.ToLower(candidate), "check out") &&
			   !strings.Contains(strings.ToLower(candidate), "post") {
				log.Printf("✅ Found text candidate: %s", candidate)
				return candidate
			}
		}
	}
	
	return ""
}

// extractAllVisibleText extracts all visible text from HTML and returns candidates
func (te *TikTokExtractor) extractAllVisibleText(html string) []string {
	var candidates []string
	
	// Remove script and style tags
	cleanHTML := regexp.MustCompile(`<script[^>]*>.*?</script>`).ReplaceAllString(html, "")
	cleanHTML = regexp.MustCompile(`<style[^>]*>.*?</style>`).ReplaceAllString(cleanHTML, "")
	
	// Extract all text from various tags
	textPatterns := []string{
		`<h[1-6][^>]*>([^<]*)</h[1-6]>`,
		`<p[^>]*>([^<]*)</p>`,
		`<div[^>]*>([^<]*)</div>`,
		`<span[^>]*>([^<]*)</span>`,
		`<a[^>]*>([^<]*)</a>`,
	}
	
	seen := make(map[string]bool)
	for _, pattern := range textPatterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindAllStringSubmatch(cleanHTML, -1)
		for _, match := range matches {
			if len(match) > 1 {
				text := strings.TrimSpace(match[1])
				// Filter out very short text, empty text, and already seen text
				if len(text) > 5 && !seen[text] {
					seen[text] = true
					candidates = append(candidates, text)
				}
			}
		}
	}
	
	return candidates
}

func (te *TikTokExtractor) extractPostText(html string) string {
	// TikTok-specific patterns for post text - enhanced for photo posts 2025
	patterns := []string{
		// Primary JSON patterns (2025 TikTok structure)
		`"desc":"([^"]*)"`,
		`"description":"([^"]*)"`,
		`"title":"([^"]*)"`,
		`"content":"([^"]*)"`,
		`"text":"([^"]*)"`,
		`"caption":"([^"]*)"`,
		
		// Photo post specific patterns with nested structures
		`"imagePostInfo"[^}]*"desc":"([^"]*)"`,
		`"photoPost"[^}]*"desc":"([^"]*)"`,
		`"photoMode"[^}]*"desc":"([^"]*)"`,
		`"imagePost"[^}]*"desc":"([^"]*)"`,
		
		// Author and video info patterns
		`"author"[^}]*"desc":"([^"]*)"`,
		`"video"[^}]*"desc":"([^"]*)"`,
		`"itemInfo"[^}]*"desc":"([^"]*)"`,
		
		// New patterns for 2025 TikTok structure (based on CSS classes)
		`<div[^>]*class="[^"]*DivMainContent[^"]*"[^>]*>.*?<span[^>]*>([^<]*?)</span>`,
		`<div[^>]*class="[^"]*DivMainContent[^"]*"[^>]*>.*?<div[^>]*>([^<]*?)</div>`,
		`<div[^>]*class="[^"]*DivMainContent[^"]*"[^>]*>(.*?)</div>`,
		`DivMainContent[^>]*>.*?<span[^>]*>([^<]*?)</span>`,
		`<div[^>]*class="[^"]*DivVideoInfoContainer[^"]*"[^>]*>.*?<span[^>]*>([^<]*?)</span>`,
		`<div[^>]*class="[^"]*video-info[^"]*"[^>]*>.*?<span[^>]*>([^<]*?)</span>`,
		`<div[^>]*class="[^"]*eqrezik[^"]*"[^>]*>.*?>([^<]*?)</div>`,
		
		// Meta tags with better precedence
		`<meta[^>]*property="og:description"[^>]*content="([^"]*)"`,
		`<meta[^>]*name="description"[^>]*content="([^"]*)"`,
		`<meta[^>]*property="og:title"[^>]*content="([^"]*)"`,
		`<meta[^>]*name="twitter:description"[^>]*content="([^"]*)"`,
		
		// Title extraction with better cleaning
		`<title[^>]*>([^<]*?)(?: - TikTok| on TikTok| \| TikTok|$)</title>`,
		
		// HTML elements with more specific selectors
		`<div[^>]*class="[^"]*caption[^"]*"[^>]*>(.*?)</div>`,
		`<p[^>]*class="[^"]*description[^"]*"[^>]*>(.*?)</p>`,
		`<span[^>]*class="[^"]*caption[^"]*"[^>]*>(.*?)</span>`,
		`<h1[^>]*class="[^"]*title[^"]*"[^>]*>(.*?)</h1>`,
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
			
			// Special handling for DivMainContent container extraction
			if strings.Contains(pattern, "DivMainContent") && strings.Contains(pattern, ">(.*?)</div>") {
				// Extract text from DivMainContent container
				extractedText := te.extractTextFromContainer(text)
				if extractedText != "" {
					return extractedText
				}
			}
			
			// Skip hashtag-only content, empty content, and generic TikTok text
			if text != "" && text != "null" && !isGenericTikTokText(text) && !strings.HasPrefix(text, "#") {
				return text
			}
		}
	}
	
	return ""
}

// isGenericTikTokText checks if the text is generic TikTok boilerplate that should be filtered out
func isGenericTikTokText(text string) bool {
	// Convert to lowercase for case-insensitive comparison
	lower := strings.ToLower(text)
	
	// Filter out common generic TikTok texts
	genericTexts := []string{
		"category",
		"tiktok",
		"watch this video",
		"see more on tiktok",
		"for you",
		"following",
		"live",
		"foryou",
		"fyp",
		"viral",
		"trending",
		"tiktok video",
		"watch on tiktok",
		"original sound",
		"sound by",
		"use this sound",
	}
	
	// If text exactly matches any generic text, filter it out
	for _, generic := range genericTexts {
		if lower == generic {
			return true
		}
	}
	
	// If text is very short and contains only generic patterns, filter it out
	if len(text) < 3 {
		return true
	}
	
	// Don't filter out longer, meaningful text even if it contains "tiktok"
	return false
}

func (te *TikTokExtractor) extractMediaURLs(html string) []string {
	var urls []string
	
	// Debug: log HTML snippet for pattern matching (check if it's readable)
	htmlSnippet := html
	if len(html) > 1000 {
		htmlSnippet = html[:1000]
	}
	
	// Check if HTML looks like binary data (gzipped) or is readable
	if len(htmlSnippet) > 10 && (htmlSnippet[0] == 0x1f && htmlSnippet[1] == 0x8b) {
		log.Printf("⚠️ HTML appears to be gzipped but not decompressed properly")
	} else if strings.Contains(htmlSnippet, "<html") || strings.Contains(htmlSnippet, "<!doctype") || strings.Contains(htmlSnippet, "swiper-slide") {
		log.Printf("✅ HTML looks readable (contains HTML tags)")
	} else {
		log.Printf("⚠️ HTML might be corrupted or in unexpected format")
	}
	
	// Always try to extract from HTML, even if it looks binary (might be compressed text)
	logSnippet := htmlSnippet
	if len(htmlSnippet) > 500 {
		logSnippet = htmlSnippet[:500]
	}
	log.Printf("🔍 HTML snippet (first 500 chars): %s", logSnippet)
	
	// Debug: Search for container patterns in HTML
	if strings.Contains(html, "DivPhotoVideoContainer") {
		log.Printf("✅ Found DivPhotoVideoContainer in HTML")
	} else {
		log.Printf("❌ DivPhotoVideoContainer NOT found in HTML")
	}
	
	if strings.Contains(html, "DivMainContent") {
		log.Printf("✅ Found DivMainContent in HTML")
	} else {
		log.Printf("❌ DivMainContent NOT found in HTML")
	}
	
	// Debug: Look for any img tags
	imgCount := strings.Count(html, "<img")
	log.Printf("🔍 Found %d <img> tags in HTML", imgCount)
	
	// Debug: Look for tiktokcdn URLs
	tiktokCdnCount := strings.Count(html, "tiktokcdn")
	log.Printf("🔍 Found %d tiktokcdn references in HTML", tiktokCdnCount)
	
	// TikTok-specific patterns for media URLs - enhanced for photo slideshows
	patterns := []string{
		// PRIORITY: DivPhotoVideoContainer patterns (2025 TikTok structure)
		`<div[^>]*class="[^"]*DivPhotoVideoContainer[^"]*"[^>]*>.*?<img[^>]*src="([^"]*)"`,
		`DivPhotoVideoContainer[^>]*>.*?<img[^>]*src="([^"]*)"`,
		`<div[^>]*DivPhotoVideoContainer[^>]*>.*?src="([^"]*)"`,
		
		// Extract all images from DivPhotoVideoContainer sections
		`<div[^>]*class="[^"]*DivPhotoVideoContainer[^"]*"[^>]*>(.*?)</div>`,
		
		// 2025 TikTok slideshow patterns - updated structure
		`"imagePost"[^}]*"images":\s*\[([^\]]*)\]`,
		`"images":\s*\[([^\]]*)\]`,
		`"imageURLs":\s*\[([^\]]*)\]`,
		
		// Direct image URLs in arrays
		`"(https://[^"]*\.tiktokcdn[^"]*\.com[^"]*\.(?:jpeg|jpg|webp)[^"]*)"`,
		`"(https://[^"]*\.tiktokcdn-[^"]*\.com[^"]*\.(?:jpeg|jpg|webp)[^"]*)"`,
		`"(https://[^"]*\.muscdn\.com[^"]*\.(?:jpeg|jpg|webp)[^"]*)"`,
		`"(https://p[0-9]+-sign[^"]*\.tiktokcdn\.com[^"]*\.(?:jpeg|jpg|webp)[^"]*)"`,
		
		// Broader patterns to catch any TikTok CDN images
		`(https://[^"'\s]*tiktokcdn[^"'\s]*\.(?:jpeg|jpg|webp|png))`,
		`(https://[^"'\s]*muscdn[^"'\s]*\.(?:jpeg|jpg|webp|png))`,
		`(https://p[0-9]+-[^"'\s]*\.(?:jpeg|jpg|webp|png))`,
		
		// Look for any img src with TikTok domains
		`<img[^>]*src="(https://[^"]*tiktokcdn[^"]*\.(?:jpeg|jpg|webp|png)[^"]*)"`,
		`<img[^>]*src="(https://[^"]*muscdn[^"]*\.(?:jpeg|jpg|webp|png)[^"]*)"`,
		
		// PRIMARY: Swiper slideshow patterns (2025 TikTok structure) - focus on photomode only
		`<div class="swiper-slide[^>]*><img src="([^"]*photomode-image[^"]*)"[^>]*class="[^"]*ImgPhotoSlide[^"]*"[^>]*></div>`,
		`<img src="(https://[^"]*\.tiktokcdn\.com/[^"]*photomode-image[^"]*\.jpeg[^"]*)"[^>]*class="[^"]*ImgPhotoSlide[^"]*"`,
		`<img[^>]*src="(https://[^"]*\.tiktokcdn\.com/[^"]*photomode-image[^"]*\.jpeg[^"]*)"[^>]*ImgPhotoSlide`,
		`swiper-slide[^>]*><img src="([^"]*photomode-image[^"]*)"`,
		
		// Direct TikTok CDN slideshow image URLs (photomode only, no avatars or share cards)
		`https://p[0-9]+-sign-[a-z]+\.tiktokcdn\.com/tos-[^"]*photomode-sg/[^"]*~tplv-photomode-image\.jpeg[^"]*`,
		`https://[^"]*\.tiktokcdn\.com/tos-[^"]*photomode[^"]*photomode-image[^"]*\.jpeg[^"]*`,
		
		// Individual photomode image URL patterns from JSON (cleaned up)
		`"url":"(https://[^"]*photomode-image[^"]*\.jpeg[^"]*)"`,
		`"imageURL":"(https://[^"]*photomode-image[^"]*\.jpeg[^"]*)"`,
		`"src":"(https://[^"]*photomode-image[^"]*\.jpeg[^"]*)"`,
		
		// Fallback: Extract any photomode-image URL from JSON (with escaping)
		`"(https:\\u002F\\u002F[^"]*photomode-image[^"]*\.jpeg[^"]*)"`,
		`"(https:[^"]*photomode-image[^"]*\.jpeg[^"]*)"`,
		
		// Photo mode specific patterns  
		`"photoMode"[^}]*"url":"([^"]*)"`,
		`"photoCarousel"[^}]*"url":"([^"]*)"`,
		`"slideshow"[^}]*"url":"([^"]*)"`,
		
		// Direct CDN URLs with modern TikTok domains
		`https://[^"]*\.(?:tiktokcdn\.com|tiktokcdn-us\.com|tiktokv\.com)/[^"]*\.(?:jpg|jpeg|png|gif|webp)`,
		`https://[^"]*\.(?:muscdn\.com|lf[0-9]+\.com|byteoversea\.com)/[^"]*\.(?:jpg|jpeg|png|gif|webp)`,
		
		// Video URLs for mixed content
		`"playAddr":"([^"]*)"`,
		`"downloadAddr":"([^"]*)"`,
		`"videoUrl":"([^"]*)"`,
		`https://[^"]*\.tiktokcdn\.com/[^"]*\.mp4`,
		
		// Generic JSON patterns with escaped URLs
		`"url":"(https://[^"]*\.(?:jpg|jpeg|png|gif|webp|mp4)[^"]*)"`,
		`"src":"(https://[^"]*\.(?:jpg|jpeg|png|gif|webp|mp4)[^"]*)"`,
		`"href":"(https://[^"]*\.(?:jpg|jpeg|png|gif|webp|mp4)[^"]*)"`,
		
		// Meta property patterns for fallback
		`property="og:image"[^>]*content="([^"]*)"`,
		`name="twitter:image"[^>]*content="([^"]*)"`,
		`<meta[^>]*content="([^"]*)"[^>]*property="og:image"`,
		
		// TikTok 2025 specific JSON patterns
		`"cover":"([^"]*)"`,
		`"dynamicCover":"([^"]*)"`,
		`"originCover":"([^"]*)"`,
	}
	
	for i, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		if strings.Contains(pattern, "(") {
			// Pattern has capture group
			matches := re.FindAllStringSubmatch(html, -1)
			if len(matches) > 0 {
				log.Printf("🎯 Pattern %d matched %d times: %s", i+1, len(matches), truncateString(pattern, 50))
			}
			for _, match := range matches {
				if len(match) > 1 {
					// Special handling for array patterns like "images":[...]
					if strings.Contains(pattern, "images\":") || strings.Contains(pattern, "imageURLs\":") {
						// Extract URLs from JSON array
						arrayURLs := te.extractURLsFromJSONArray(match[1])
						urls = append(urls, arrayURLs...)
					} else if strings.Contains(pattern, "DivPhotoVideoContainer") && strings.Contains(pattern, ">(.*?)</div>") {
						// Extract all img src URLs from DivPhotoVideoContainer content
						containerURLs := te.extractImagesFromContainer(match[1])
						urls = append(urls, containerURLs...)
					} else {
						// Clean up URL: remove HTML entities, JSON escaping, and quotes
						url := te.cleanURL(match[1])
						if url != "" {
							urls = append(urls, url)
						}
					}
				}
			}
		} else {
			// Pattern matches full URL
			matches := re.FindAllString(html, -1)
			if len(matches) > 0 {
				log.Printf("🎯 Pattern %d matched %d times: %s", i+1, len(matches), truncateString(pattern, 50))
			}
			for _, match := range matches {
				// Clean up URL: remove HTML entities, JSON escaping, and quotes
				url := te.cleanURL(match)
				if url != "" {
					urls = append(urls, url)
				}
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
	
	log.Printf("🔍 Found %d total URLs before deduplication: %v", len(urls), urls)
	log.Printf("🔍 Found %d unique URLs after deduplication: %v", len(uniqueURLs), uniqueURLs)
	
	return uniqueURLs
}

// cleanURL removes HTML entities, JSON escaping, and quotes from URLs
func (te *TikTokExtractor) cleanURL(url string) string {
	// Clean up URL: remove HTML entities, JSON escaping, and quotes
	url = strings.ReplaceAll(url, "&amp;", "&")
	url = strings.ReplaceAll(url, "&quot;", "\"")
	url = strings.ReplaceAll(url, "\\u002F", "/")  // Escaped backslash version
	url = strings.ReplaceAll(url, "\u002F", "/")   // Direct Unicode escape version
	url = strings.ReplaceAll(url, "\\/", "/")
	url = strings.ReplaceAll(url, `\u0026`, "&")
	
	// Remove surrounding quotes if present
	if strings.HasPrefix(url, "\"") && strings.HasSuffix(url, "\"") {
		url = url[1 : len(url)-1]
	}
	
	return url
}

// extractURLsFromJSONArray extracts URLs from a JSON array string like ["url1", "url2", ...]
func (te *TikTokExtractor) extractURLsFromJSONArray(arrayStr string) []string {
	var urls []string
	
	// Simple regex to extract URLs from array
	urlPattern := `"(https://[^"]*\.(?:jpeg|jpg|webp|png|gif|mp4)[^"]*)"`
	re := regexp.MustCompile(urlPattern)
	matches := re.FindAllStringSubmatch(arrayStr, -1)
	
	for _, match := range matches {
		if len(match) > 1 {
			url := te.cleanURL(match[1])
			if url != "" {
				urls = append(urls, url)
			}
		}
	}
	
	return urls
}

// extractImagesFromContainer extracts image URLs from HTML container content
func (te *TikTokExtractor) extractImagesFromContainer(containerHTML string) []string {
	var urls []string
	
	// Extract all img src URLs from the container HTML
	imgPattern := `<img[^>]*src="([^"]*)"[^>]*>`
	re := regexp.MustCompile(imgPattern)
	matches := re.FindAllStringSubmatch(containerHTML, -1)
	
	for _, match := range matches {
		if len(match) > 1 {
			url := te.cleanURL(match[1])
			if url != "" && (strings.Contains(url, ".jpg") || strings.Contains(url, ".jpeg") || strings.Contains(url, ".webp") || strings.Contains(url, ".png")) {
				urls = append(urls, url)
			}
		}
	}
	
	log.Printf("🔍 Extracted %d images from DivPhotoVideoContainer: %v", len(urls), urls)
	return urls
}

// extractTextFromContainer extracts meaningful text from HTML container content
func (te *TikTokExtractor) extractTextFromContainer(containerHTML string) string {
	// Remove HTML tags first
	text := regexp.MustCompile(`<[^>]*>`).ReplaceAllString(containerHTML, " ")
	
	// Clean up whitespace
	text = regexp.MustCompile(`\s+`).ReplaceAllString(text, " ")
	text = strings.TrimSpace(text)
	
	// Filter out generic content
	if text != "" && !isGenericTikTokText(text) && !strings.HasPrefix(text, "#") && len(text) > 3 {
		log.Printf("📝 Extracted text from DivMainContent: %s", text)
		return text
	}
	
	return ""
}

// truncateString truncates a string to maxLen characters for logging
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
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
		Caption:  "", // Caption will be set by the caller with post text
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

func (te *TikTokExtractor) extractTextFromVxTikTok(html string) string {
	// VxTikTok-specific patterns for text extraction
	patterns := []string{
		// Meta tags
		`<meta[^>]*property="og:description"[^>]*content="([^"]*)"`,
		`<meta[^>]*name="description"[^>]*content="([^"]*)"`,
		`<meta[^>]*property="og:title"[^>]*content="([^"]*)"`,
		
		// VxTikTok specific elements
		`<title[^>]*>([^<]*?)(?: - vxtiktok| on TikTok|$)</title>`,
		`<div[^>]*class="[^"]*caption[^"]*"[^>]*>(.*?)</div>`,
		`<p[^>]*class="[^"]*description[^"]*"[^>]*>(.*?)</p>`,
		
		// JSON embedded in script tags
		`"desc":"([^"]*)"`,
		`"description":"([^"]*)"`,
		`"title":"([^"]*)"`,
	}
	
	for _, pattern := range patterns {
		re := regexp.MustCompile(`(?s)` + pattern)
		matches := re.FindStringSubmatch(html)
		if len(matches) > 1 {
			text := matches[1]
			
			// Clean up and decode
			text = strings.ReplaceAll(text, `\"`, `"`)
			text = strings.ReplaceAll(text, `\\n`, "\n")
			text = strings.ReplaceAll(text, `\\t`, "\t")
			text = strings.ReplaceAll(text, `\\r`, "\r")
			
			// Clean up HTML tags
			text = regexp.MustCompile(`<[^>]*>`).ReplaceAllString(text, "")
			
			text = strings.TrimSpace(text)
			if text != "" && text != "null" && !strings.Contains(strings.ToLower(text), "vxtiktok") && !strings.Contains(strings.ToLower(text), "company") {
				return text
			}
		}
	}
	
	return ""
}

func (te *TikTokExtractor) extractVxTikTokMediaURLs(html string) []string {
	var urls []string
	
	// VxTikTok-specific patterns
	patterns := []string{
		// Direct download links from vxtiktok
		`href="(https://[^"]*\.(?:jpg|jpeg|png|gif|webp|mp4)[^"]*)"[^>]*>(?:Download|Download Image|Save)`,
		`<a[^>]*download[^>]*href="(https://[^"]*\.(?:jpg|jpeg|png|gif|webp|mp4)[^"]*)"`,
		
		// Image sources on the page
		`<img[^>]*src="(https://[^"]*\.(?:jpg|jpeg|png|gif|webp)[^"]*)"`,
		`<video[^>]*src="(https://[^"]*\.mp4[^"]*)"`,
		
		// JSON embedded URLs
		`"imagePost"[^}]*"url":"([^"]*)"`,
		`"images":\[[^\]]*"url":"([^"]*)"`,
		`"url":"(https://[^"]*\.(?:tiktokcdn\.com|tiktokcdn-us\.com|muscdn\.com|vxtiktok\.com)/[^"]*\.(?:jpg|jpeg|png|gif|webp|mp4)[^"]*)"`,
		
		// VxTikTok proxy URLs
		`https://[^"]*\.vxtiktok\.com/[^"]*\.(?:jpg|jpeg|png|gif|webp|mp4)`,
	}
	
	for i, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		if strings.Contains(pattern, "(") {
			// Pattern has capture group
			matches := re.FindAllStringSubmatch(html, -1)
			if len(matches) > 0 {
				log.Printf("🎯 Pattern %d matched %d times: %s", i+1, len(matches), truncateString(pattern, 50))
			}
			for _, match := range matches {
				if len(match) > 1 {
					// Special handling for array patterns like "images":[...]
					if strings.Contains(pattern, "images\":") || strings.Contains(pattern, "imageURLs\":") {
						// Extract URLs from JSON array
						arrayURLs := te.extractURLsFromJSONArray(match[1])
						urls = append(urls, arrayURLs...)
					} else if strings.Contains(pattern, "DivPhotoVideoContainer") && strings.Contains(pattern, ">(.*?)</div>") {
						// Extract all img src URLs from DivPhotoVideoContainer content
						containerURLs := te.extractImagesFromContainer(match[1])
						urls = append(urls, containerURLs...)
					} else {
						// Clean up URL: remove HTML entities, JSON escaping, and quotes
						url := te.cleanURL(match[1])
						if url != "" {
							urls = append(urls, url)
						}
					}
				}
			}
		} else {
			// Pattern matches full URL
			matches := re.FindAllString(html, -1)
			if len(matches) > 0 {
				log.Printf("🎯 Pattern %d matched %d times: %s", i+1, len(matches), truncateString(pattern, 50))
			}
			for _, match := range matches {
				// Clean up URL: remove HTML entities, JSON escaping, and quotes
				url := te.cleanURL(match)
				if url != "" {
					urls = append(urls, url)
				}
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