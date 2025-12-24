package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type CliBrowserExtractor struct {
	tempDir    string
	workingDir string
}

func NewCliBrowserExtractor(workingDir, tempDir string) *CliBrowserExtractor {
	return &CliBrowserExtractor{
		tempDir:    tempDir,
		workingDir: workingDir,
	}
}

// ExtractTwitterImages uses command-line Chrome with virtual-time-budget for Twitter
func (cbe *CliBrowserExtractor) ExtractTwitterImages(twitterURL string) ([]*MediaInfo, error) {
	log.Printf("🚀 Extracting Twitter images with CLI Chrome + virtual-time-budget")

	// Find Chrome executable
	chromePaths := []string{
		"/usr/bin/google-chrome",
		"/usr/bin/google-chrome-stable",
		"/usr/bin/chromium-browser",
		"/usr/bin/chromium",
		"/opt/google/chrome/chrome",
		"/snap/bin/chromium",
	}

	chromePath := ""
	for _, path := range chromePaths {
		if _, err := os.Stat(path); err == nil {
			chromePath = path
			break
		}
	}

	if chromePath == "" {
		return nil, fmt.Errorf("no Chrome/Chromium executable found")
	}

	// Create temporary file for HTML output
	tempFile := filepath.Join(cbe.tempDir, fmt.Sprintf("twitter_chrome_%d.html", time.Now().Unix()))
	defer os.Remove(tempFile)

	// Chrome command with virtual-time-budget
	cmd := exec.Command(chromePath,
		"--headless",
		"--disable-gpu",
		"--no-sandbox",
		"--virtual-time-budget=10000",
		"--dump-dom",
		"--user-agent=Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36",
		twitterURL,
	)

	// Capture output
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("Chrome execution failed: %v", err)
	}

	// Save HTML for analysis
	if err := ioutil.WriteFile(tempFile, output, 0644); err != nil {
		return nil, fmt.Errorf("failed to write HTML output: %v", err)
	}

	html := string(output)
	log.Printf("📄 Chrome rendered HTML: %d bytes", len(html))

	// Extract individual images
	return cbe.extractTwitterImagesFromHTML(html, twitterURL)
}

func (cbe *CliBrowserExtractor) extractTwitterImagesFromHTML(html, twitterURL string) ([]*MediaInfo, error) {
	// Extract individual image URLs
	imagePatterns := []string{
		// Twitter image patterns with css-9pa8cd class (2025 structure)
		`<img[^>]*alt="Image"[^>]*src="(https://pbs\.twimg\.com/media/[^"]*)"[^>]*class="css-9pa8cd"`,
		`<img[^>]*class="css-9pa8cd"[^>]*alt="Image"[^>]*src="(https://pbs\.twimg\.com/media/[^"]*)"`,
		`<img[^>]*draggable="true"[^>]*src="(https://pbs\.twimg\.com/media/[^"]*)"[^>]*class="css-9pa8cd"`,
		// More generic patterns
		`<img[^>]*src="(https://pbs\.twimg\.com/media/[^"?]+\?format=jpg[^"]*)"[^>]*alt="Image"`,
		`<img[^>]*alt="Image"[^>]*src="(https://pbs\.twimg\.com/media/[^"]*)"`,
		// Any img tag with pbs.twimg.com/media/ (broader pattern)
		`<img[^>]*src="(https://pbs\.twimg\.com/media/[^"]*)"[^>]*>`,
		`<img[^>]*src="(https://pbs\.twimg\.com/media/[^"&]*)[^"]*"`,
	}

	var imageURLs []string
	for _, pattern := range imagePatterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindAllStringSubmatch(html, -1)
		for _, match := range matches {
			if len(match) > 1 {
				imageURLs = append(imageURLs, match[1])
			}
		}
	}

	// Filter out profile images and other non-tweet images
	// Use media ID for deduplication to handle URL variations
	seenMediaIDs := make(map[string]string) // mediaID -> best URL
	for _, url := range imageURLs {
		if !strings.Contains(url, "profile_images") && !strings.Contains(url, "_normal") {
			// Clean up URL (remove HTML entities)
			cleanURL := strings.ReplaceAll(url, "&amp;", "&")
			// Upgrade to high quality
			cleanURL = cbe.upgradeTwitterImageURL(cleanURL)

			// Extract media ID for deduplication
			mediaID := cbe.extractMediaID(cleanURL)
			if mediaID == "" {
				mediaID = cleanURL
			}

			if existingURL, exists := seenMediaIDs[mediaID]; exists {
				// Keep URL with higher quality
				if cbe.getURLQualityScore(cleanURL) > cbe.getURLQualityScore(existingURL) {
					seenMediaIDs[mediaID] = cleanURL
				}
			} else {
				seenMediaIDs[mediaID] = cleanURL
			}
		}
	}

	// Collect unique URLs preserving order
	seen := make(map[string]bool)
	var tweetImages []string
	for _, url := range imageURLs {
		if !strings.Contains(url, "profile_images") && !strings.Contains(url, "_normal") {
			cleanURL := strings.ReplaceAll(url, "&amp;", "&")
			cleanURL = cbe.upgradeTwitterImageURL(cleanURL)
			mediaID := cbe.extractMediaID(cleanURL)
			if mediaID == "" {
				mediaID = cleanURL
			}
			if !seen[mediaID] {
				seen[mediaID] = true
				tweetImages = append(tweetImages, seenMediaIDs[mediaID])
			}
		}
	}

	log.Printf("🖼️ Found %d individual Twitter images (from %d total URLs)", len(tweetImages), len(imageURLs))
	
	// Debug: log if we found any pbs.twimg.com at all
	if len(imageURLs) == 0 {
		pbsCount := strings.Count(html, "pbs.twimg.com")
		tweetTextCount := strings.Count(html, `data-testid="tweetText"`)
		log.Printf("🔍 Debug: HTML contains %d pbs.twimg.com references and %d tweetText elements", pbsCount, tweetTextCount)
	}

	if len(tweetImages) == 0 {
		return nil, fmt.Errorf("no Twitter images found in rendered HTML")
	}

	// Extract caption from HTML
	caption := cbe.extractTwitterCaption(html)
	
	// If caption extraction failed, fall back to Twitter extractor for text
	if caption == "" {
		log.Printf("🔍 CLI browser caption extraction failed, trying Twitter extractor for text")
		twitterExtractor := NewTwitterExtractor(cbe.workingDir, cbe.tempDir)
		if textResult, err := twitterExtractor.ExtractTweet(twitterURL); err == nil && len(textResult) > 0 {
			if textResult[0].Caption != "" {
				caption = textResult[0].Caption
				log.Printf("✅ Got caption from Twitter extractor: %.100s...", caption)
			}
		}
	}

	// Download each image
	var mediaInfos []*MediaInfo
	for i, imageURL := range tweetImages {
		filename, err := cbe.downloadMediaFile(imageURL, "twitter", "photo", i)
		if err != nil {
			log.Printf("❌ Failed to download Twitter image %d: %v", i+1, err)
			continue
		}

		mediaInfo := &MediaInfo{
			Type:     "photo",
			URL:      twitterURL,
			Caption:  caption,
			Filename: filename,
		}
		mediaInfos = append(mediaInfos, mediaInfo)
	}

	return mediaInfos, nil
}

func (cbe *CliBrowserExtractor) upgradeTwitterImageURL(url string) string {
	// Upgrade image quality
	url = strings.ReplaceAll(url, "&name=small", "&name=orig")
	url = strings.ReplaceAll(url, "&name=medium", "&name=orig")
	url = strings.ReplaceAll(url, "&name=large", "&name=orig")
	url = strings.ReplaceAll(url, "&name=360x360", "&name=orig")
	if !strings.Contains(url, "&name=") && strings.Contains(url, "?format=") {
		url += "&name=orig"
	}

	return url
}

// extractMediaID extracts the media identifier from a Twitter image URL
func (cbe *CliBrowserExtractor) extractMediaID(url string) string {
	re := regexp.MustCompile(`pbs\.twimg\.com/media/([A-Za-z0-9_-]+)`)
	matches := re.FindStringSubmatch(url)
	if len(matches) > 1 {
		return matches[1]
	}

	return ""
}

// getURLQualityScore returns a quality score for URL (higher is better)
func (cbe *CliBrowserExtractor) getURLQualityScore(url string) int {
	if strings.Contains(url, "name=orig") {
		return 4
	}
	if strings.Contains(url, "name=large") {
		return 3
	}
	if strings.Contains(url, "name=medium") {
		return 2
	}
	if strings.Contains(url, "name=small") {
		return 1
	}

	return 0
}

func (cbe *CliBrowserExtractor) extractTwitterCaption(html string) string {
	// Try to extract the main tweet text content using multiple patterns
	patterns := []string{
		// Try data-testid="tweetText" first (most reliable in 2025)
		`<div[^>]*data-testid="tweetText"[^>]*>(.*?)</div>`,
		`<span[^>]*data-testid="tweetText"[^>]*>(.*?)</span>`,
		// Try Open Graph description (reliable)
		`<meta[^>]*property="og:description"[^>]*content="([^"]*)"`,
		// Try regular description meta tag
		`<meta[^>]*name="description"[^>]*content="([^"]*)"`,
		// Try title tag content (sometimes contains tweet text)
		`<title[^>]*>([^<]+?) / X</title>`,
		`<title[^>]*>([^<]+?) on X:</title>`,
		// JSON embedded text patterns
		`"full_text"\s*:\s*"([^"]*)"`,
		`"text"\s*:\s*"([^"]*)"`,
		// Try broader patterns for tweet content
		`"([^"]*)" / X`,
		`"([^"]*)" on X:`,
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(`(?s)` + pattern) // (?s) makes . match newlines
		if match := re.FindStringSubmatch(html); len(match) > 1 {
			caption := strings.TrimSpace(match[1])
			if caption != "" && caption != "null" && len(caption) > 10 {
				// Skip if it's just domain names or generic text
				if !strings.Contains(strings.ToLower(caption), "twitter") && 
				   !strings.Contains(strings.ToLower(caption), "log in") &&
				   !strings.Contains(strings.ToLower(caption), "sign up") {
					
					// Clean and decode the caption
					caption = cbe.cleanTweetText(caption)
					if len(caption) > 10 {
						return caption
					}
				}
			}
		}
	}

	return ""
}

func (cbe *CliBrowserExtractor) cleanTweetText(rawText string) string {
	text := rawText
	
	// Remove HTML tags
	text = regexp.MustCompile(`<[^>]*>`).ReplaceAllString(text, "")
	
	// Decode HTML entities
	text = strings.ReplaceAll(text, "&amp;", "&")
	text = strings.ReplaceAll(text, "&lt;", "<")
	text = strings.ReplaceAll(text, "&gt;", ">")
	text = strings.ReplaceAll(text, "&quot;", "\"")
	text = strings.ReplaceAll(text, "&#39;", "'")
	text = strings.ReplaceAll(text, "&#34;", "\"")
	
	// Decode JSON escape sequences
	text = strings.ReplaceAll(text, `\"`, `"`)
	text = strings.ReplaceAll(text, `\\n`, "\n")
	text = strings.ReplaceAll(text, `\\t`, "\t")
	text = strings.ReplaceAll(text, `\\r`, "\r")
	
	// Remove pic.twitter.com links
	picTwitterPattern := regexp.MustCompile(`\s*pic\.twitter\.com/[a-zA-Z0-9]+\s*`)
	text = picTwitterPattern.ReplaceAllString(text, "")
	
	// Clean up multiple spaces and newlines
	text = regexp.MustCompile(`\s+`).ReplaceAllString(text, " ")
	text = strings.TrimSpace(text)
	
	return text
}

func (cbe *CliBrowserExtractor) downloadMediaFile(mediaURL, platform, mediaType string, index int) (string, error) {
	// Create filename
	timestamp := time.Now().Unix()
	extension := ".jpg"
	if mediaType == "video" {
		extension = ".mp4"
	}
	
	filename := filepath.Join(cbe.tempDir, fmt.Sprintf("%d_%s_%s_%d%s", timestamp, platform, mediaType, index, extension))

	// Download file
	cmd := exec.Command("curl", "-s", "-o", filename, 
		"-H", "User-Agent: Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36",
		"-H", "Referer: https://twitter.com/",
		mediaURL)

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to download %s: %v", mediaURL, err)
	}

	// Check if file was downloaded successfully
	if fileInfo, err := os.Stat(filename); err != nil || fileInfo.Size() == 0 {
		os.Remove(filename)
		return "", fmt.Errorf("downloaded file is empty or missing")
	}

	return filename, nil
}

// Extract9GAGContent uses command-line Chrome with virtual-time-budget for 9GAG
func (cbe *CliBrowserExtractor) Extract9GAGContent(ninegagURL string) ([]*MediaInfo, error) {
	log.Printf("😂 Extracting 9GAG content with CLI Chrome + virtual-time-budget")

	// Find Chrome executable
	chromePaths := []string{
		"/usr/bin/google-chrome",
		"/usr/bin/google-chrome-stable",
		"/usr/bin/chromium-browser",
		"/usr/bin/chromium",
		"/opt/google/chrome/chrome",
		"/snap/bin/chromium",
	}

	chromePath := ""
	for _, path := range chromePaths {
		if _, err := os.Stat(path); err == nil {
			chromePath = path
			break
		}
	}

	if chromePath == "" {
		return nil, fmt.Errorf("no Chrome/Chromium executable found")
	}

	// Create temporary file for HTML output
	tempFile := filepath.Join(cbe.tempDir, fmt.Sprintf("9gag_chrome_%d.html", time.Now().Unix()))
	defer os.Remove(tempFile)

	// Build Chrome arguments - try with JS enabled first for 9GAG
	chromeArgs := []string{
		"--headless",
		"--disable-gpu",
		"--no-sandbox",
		"--virtual-time-budget=8000", // Shorter timeout for first attempt
		"--dump-dom",
		"--user-agent=Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36",
		"--disable-web-security",
		"--disable-features=VizDisplayCompositor",
		"--disable-extensions",
		"--disable-plugins",
		"--disable-images", // Speed up loading
		"--disable-dev-shm-usage", // Prevent shared memory issues
		"--no-first-run",
		"--timeout=25", // 25 second timeout
	}
	
	// Add 9GAG cookies if available
	ninegagCookiesPath := filepath.Join(cbe.workingDir, "9gag_cookies.txt")
	if _, err := os.Stat(ninegagCookiesPath); err == nil {
		chromeArgs = append(chromeArgs, "--cookie-file="+ninegagCookiesPath)
		log.Printf("🍪 Using 9GAG cookies: %s", ninegagCookiesPath)
	} else {
		log.Printf("⚠️ 9GAG cookies not found at %s", ninegagCookiesPath)
	}
	
	chromeArgs = append(chromeArgs, ninegagURL)
	cmd := exec.Command(chromePath, chromeArgs...)

	// Set command timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd = exec.CommandContext(ctx, cmd.Args[0], cmd.Args[1:]...)

	// Capture output
	output, err := cmd.Output()
	if err != nil {
		// If it failed, try without virtual-time-budget (simpler approach)
		log.Printf("⚠️ Chrome with virtual-time-budget failed, trying without virtual-time-budget")
		
		// Build simpler Chrome arguments without virtual-time-budget
		chromeArgsSimple := []string{
			"--headless",
			"--disable-gpu", 
			"--no-sandbox",
			"--dump-dom",
			"--user-agent=Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36",
			"--disable-web-security",
			"--disable-dev-shm-usage",
			"--no-first-run",
			"--timeout=15",
		}
		
		// Add 9GAG cookies if available
		if _, err := os.Stat(ninegagCookiesPath); err == nil {
			chromeArgsSimple = append(chromeArgsSimple, "--cookie-file="+ninegagCookiesPath)
		}
		
		chromeArgsSimple = append(chromeArgsSimple, ninegagURL)
		cmdSimple := exec.Command(chromePath, chromeArgsSimple...)
		
		ctxSimple, cancelSimple := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancelSimple()
		cmdSimple = exec.CommandContext(ctxSimple, cmdSimple.Args[0], cmdSimple.Args[1:]...)
		
		output, err = cmdSimple.Output()
		
		if err != nil {
			// As last resort, try simple HTTP request with user agent
			log.Printf("⚠️ Chrome with JS also failed, trying simple HTTP request")
			return cbe.trySimple9GAGHttp(ninegagURL)
		}
	}

	html := string(output)
	log.Printf("📄 Chrome rendered 9GAG HTML: %d bytes", len(html))
	
	// Debug: log first 200 chars of HTML if it's small
	if len(html) < 500 {
		log.Printf("🔍 HTML content preview: %s", html)
	}

	// Extract content from rendered HTML
	return cbe.extract9GAGFromHTML(html, ninegagURL)
}

func (cbe *CliBrowserExtractor) extract9GAGFromHTML(html, ninegagURL string) ([]*MediaInfo, error) {
	var mediaInfos []*MediaInfo

	// Extract 9GAG images - updated patterns for 2025
	imagePatterns := []string{
		// Main post images
		`<img[^>]*src="(https://img-9gag-fun\.9cache\.com/[^"]*\.jpg[^"]*)"[^>]*class="[^"]*post-container[^"]*"`,
		`<img[^>]*class="[^"]*post-container[^"]*"[^>]*src="(https://img-9gag-fun\.9cache\.com/[^"]*\.jpg[^"]*)"`,
		// Generic 9GAG image patterns
		`<img[^>]*src="(https://img-9gag-fun\.9cache\.com/[^"]*\.(jpg|png|webp)[^"]*)"`,
		`<img[^>]*src="(https://miscmedia-9gag-fun\.9cache\.com/[^"]*\.(jpg|png|webp)[^"]*)"`,
		// Meta tag patterns
		`<meta[^>]*property="og:image"[^>]*content="(https://img-9gag-fun\.9cache\.com/[^"]*\.(jpg|png|webp)[^"]*)"`,
		// Any 9cache image
		`<img[^>]*src="(https://[^"]*\.9cache\.com/[^"]*\.(jpg|png|webp)[^"]*)"`,
	}

	var imageURLs []string
	for _, pattern := range imagePatterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindAllStringSubmatch(html, -1)
		for _, match := range matches {
			if len(match) > 1 {
				imageURLs = append(imageURLs, match[1])
			}
		}
	}

	// Filter out small/thumbnail images
	var mainImages []string
	seen := make(map[string]bool)
	for _, url := range imageURLs {
		if !seen[url] && !strings.Contains(url, "_thumb") && !strings.Contains(url, "_small") {
			mainImages = append(mainImages, url)
			seen[url] = true
		}
	}

	log.Printf("🖼️ Found %d 9GAG images (from %d total)", len(mainImages), len(imageURLs))

	// Extract videos
	videoPatterns := []string{
		// Main video patterns
		`<video[^>]*src="(https://img-9gag-fun\.9cache\.com/[^"]*\.mp4[^"]*)"`,
		`<source[^>]*type="video/mp4"[^>]*src="(https://img-9gag-fun\.9cache\.com/[^"]*\.mp4[^"]*)"`,
		`"videoUrl":"(https://[^"]*\.9cache\.com/[^"]*\.mp4[^"]*)"`,
		`<meta[^>]*property="og:video"[^>]*content="(https://[^"]*\.9cache\.com/[^"]*\.mp4[^"]*)"`,
		// Any 9cache video
		`<video[^>]*src="(https://[^"]*\.9cache\.com/[^"]*\.mp4[^"]*)"`,
		`<source[^>]*src="(https://[^"]*\.9cache\.com/[^"]*\.mp4[^"]*)"`,
	}

	var videoURLs []string
	for _, pattern := range videoPatterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindAllStringSubmatch(html, -1)
		for _, match := range matches {
			if len(match) > 1 {
				// Clean JSON escaped URLs
				url := strings.ReplaceAll(match[1], "\\/", "/")
				url = strings.ReplaceAll(url, "&amp;", "&")
				videoURLs = append(videoURLs, url)
			}
		}
	}

	// Filter unique videos (avoid duplicates/thumbnails)
	var mainVideos []string
	seenVideo := make(map[string]bool)
	for _, url := range videoURLs {
		if !seenVideo[url] && !strings.Contains(url, "_thumb") && !strings.Contains(url, "_preview") {
			mainVideos = append(mainVideos, url)
			seenVideo[url] = true
		}
	}

	log.Printf("🎥 Found %d 9GAG videos (from %d total)", len(mainVideos), len(videoURLs))

	// Extract caption
	caption := cbe.extract9GAGCaption(html)

	// Download images
	for i, imageURL := range mainImages {
		filename, err := cbe.downloadMediaFile(imageURL, "9gag", "photo", i)
		if err != nil {
			log.Printf("❌ Failed to download 9GAG image %d: %v", i+1, err)
			continue
		}

		mediaInfo := &MediaInfo{
			Type:     "photo",
			URL:      ninegagURL,
			Caption:  caption,
			Filename: filename,
		}
		mediaInfos = append(mediaInfos, mediaInfo)
	}

	// Download videos
	for i, videoURL := range mainVideos {
		filename, err := cbe.downloadMediaFile(videoURL, "9gag", "video", i)
		if err != nil {
			log.Printf("❌ Failed to download 9GAG video %d: %v", i+1, err)
			continue
		}

		mediaInfo := &MediaInfo{
			Type:     "video", 
			URL:      ninegagURL,
			Caption:  caption,
			Filename: filename,
		}
		mediaInfos = append(mediaInfos, mediaInfo)
	}

	if len(mediaInfos) == 0 {
		return nil, fmt.Errorf("no 9GAG media found in rendered HTML")
	}

	return mediaInfos, nil
}

func (cbe *CliBrowserExtractor) extract9GAGCaption(html string) string {
	patterns := []string{
		// Try Open Graph description first
		`<meta[^>]*property="og:description"[^>]*content="([^"]*)"`,
		// Try meta description
		`<meta[^>]*name="description"[^>]*content="([^"]*)"`,
		// Try title
		`<title[^>]*>([^<]*?) - 9GAG</title>`,
		`<title[^>]*>([^<]*?) \| 9GAG</title>`,
		// JSON data patterns
		`"title":"([^"]*)"`,
		`"description":"([^"]*)"`,
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		if match := re.FindStringSubmatch(html); len(match) > 1 {
			caption := strings.TrimSpace(match[1])
			if caption != "" && caption != "null" && len(caption) > 5 {
				// Skip generic 9GAG text
				if !strings.Contains(strings.ToLower(caption), "9gag") || len(caption) > 20 {
					// Clean up the caption
					caption = strings.ReplaceAll(caption, "&amp;", "&")
					caption = strings.ReplaceAll(caption, "&lt;", "<")
					caption = strings.ReplaceAll(caption, "&gt;", ">")
					caption = strings.ReplaceAll(caption, "&quot;", "\"")
					caption = strings.ReplaceAll(caption, "&#39;", "'")
					return caption
				}
			}
		}
	}

	return ""
}

// trySimple9GAGHttp attempts a simple HTTP request as last resort
func (cbe *CliBrowserExtractor) trySimple9GAGHttp(ninegagURL string) ([]*MediaInfo, error) {
	log.Printf("🌐 Trying simple HTTP request for 9GAG as last resort")
	
	// Use curl with proper headers
	cmd := exec.Command("curl", "-s", "-L", 
		"-H", "User-Agent: Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36",
		"-H", "Accept: text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
		"-H", "Accept-Language: en-US,en;q=0.5",
		"--max-time", "30",
		ninegagURL)
	
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("simple HTTP request failed: %v", err)
	}
	
	html := string(output)
	log.Printf("📄 Simple HTTP got %d bytes of HTML", len(html))
	
	// Try to extract media from the HTML
	return cbe.extract9GAGFromHTML(html, ninegagURL)
}

// RenderPageForTwitterThread renders a Twitter page to extract thread conversation HTML
func (cbe *CliBrowserExtractor) RenderPageForTwitterThread(twitterURL string) (string, error) {
	log.Printf("🌐 [AUTO-THREAD] Rendering Twitter page for thread discovery: %s", twitterURL)

	// Find Chrome executable (same logic as ExtractTwitterImages)
	chromePaths := []string{
		"/usr/bin/google-chrome",
		"/usr/bin/google-chrome-stable",
		"/usr/bin/chromium-browser",
		"/usr/bin/chromium",
		"/opt/google/chrome/chrome",
		"/snap/bin/chromium",
	}

	chromePath := ""
	for _, path := range chromePaths {
		if _, err := os.Stat(path); err == nil {
			chromePath = path
			break
		}
	}

	if chromePath == "" {
		return "", fmt.Errorf("no Chrome/Chromium executable found")
	}

	// Create temporary file for HTML output
	tmpFile, err := os.CreateTemp("", "twitter_thread_*.html")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	// Chrome command optimized for thread page rendering with enhanced settings
	args := []string{
		"--headless=new",
		"--no-sandbox",
		"--disable-gpu",
		"--disable-dev-shm-usage",
		"--disable-extensions",
		"--no-first-run",
		"--disable-default-apps",
		"--virtual-time-budget=20000", // Even more time for full thread loading
		"--run-all-compositor-stages-before-draw",
		"--enable-automation",
		"--disable-background-timer-throttling",
		"--disable-backgrounding-occluded-windows",
		"--disable-renderer-backgrounding",
		"--disable-features=TranslateUI",
		"--user-data-dir=" + os.TempDir(),
		"--window-size=1920,1080",
		"--user-agent=Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
	}
	
	// Add Twitter cookies if available for better authentication and thread loading
	twitterCookiesPath := filepath.Join(cbe.workingDir, "cookies", "twitter_cookies.txt")
	if _, err := os.Stat(twitterCookiesPath); err == nil {
		args = append(args, "--cookie-file="+twitterCookiesPath)
		log.Printf("🍪 [AUTO-THREAD] Using Twitter cookies: %s", twitterCookiesPath)
	} else {
		log.Printf("⚠️ [AUTO-THREAD] Twitter cookies not found at %s", twitterCookiesPath)
	}
	
	// Try to modify URL to get better thread loading
	// Sometimes adding specific parameters helps load the full conversation
	enhancedURL := twitterURL
	if !strings.Contains(twitterURL, "?") {
		enhancedURL += "?conversation_id=" + extractTweetID(twitterURL)
	}
	
	log.Printf("🔍 [AUTO-THREAD] Using enhanced URL for thread loading: %s", enhancedURL)
	
	args = append(args, "--dump-dom", enhancedURL)

	log.Printf("🌐 [AUTO-THREAD] Executing Chrome: %s %v", chromePath, args)

	// Set up command with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, chromePath, args...)
	
	// Capture both stdout and stderr
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Execute command
	err = cmd.Run()
	if err != nil {
		log.Printf("❌ [AUTO-THREAD] Chrome execution failed: %v", err)
		log.Printf("❌ [AUTO-THREAD] Chrome stderr: %s", stderr.String())
		return "", fmt.Errorf("chrome execution failed: %v", err)
	}

	html := stdout.String()
	log.Printf("✅ [AUTO-THREAD] Chrome rendered %d bytes of HTML", len(html))

	return html, nil
}

// extractTweetID extracts tweet ID from Twitter URL
func extractTweetID(twitterURL string) string {
	re := regexp.MustCompile(`/status/(\d+)`)
	matches := re.FindStringSubmatch(twitterURL)
	if len(matches) >= 2 {
		return matches[1]
	}
	return ""
}

// ExtractYouTubeCommunityPost uses enhanced browser automation for YouTube community posts
func (cbe *CliBrowserExtractor) ExtractYouTubeCommunityPost(youtubePostURL string) ([]*MediaInfo, error) {
	log.Printf("📺 Extracting YouTube community post with enhanced CLI Chrome")

	// Find Chrome executable
	chromePaths := []string{
		"/usr/bin/google-chrome",
		"/usr/bin/google-chrome-stable", 
		"/usr/bin/chromium-browser",
		"/usr/bin/chromium",
		"/opt/google/chrome/chrome",
		"/snap/bin/chromium",
	}

	chromePath := ""
	for _, path := range chromePaths {
		if _, err := os.Stat(path); err == nil {
			chromePath = path
			break
		}
	}

	if chromePath == "" {
		return nil, fmt.Errorf("no Chrome/Chromium executable found")
	}

	// Create temporary file for HTML output
	tempFile := filepath.Join(cbe.tempDir, fmt.Sprintf("youtube_community_%d.html", time.Now().Unix()))
	defer os.Remove(tempFile)

	// Enhanced Chrome arguments for YouTube community posts
	chromeArgs := []string{
		"--headless=new",                               // Use new headless mode
		"--no-sandbox",
		"--disable-gpu",
		"--disable-dev-shm-usage",
		"--disable-extensions",
		"--no-first-run",
		"--disable-default-apps",
		"--virtual-time-budget=45000",                  // Extended time for YouTube loading
		"--run-all-compositor-stages-before-draw",
		"--enable-automation",
		"--disable-background-timer-throttling",
		"--disable-backgrounding-occluded-windows", 
		"--disable-renderer-backgrounding",
		"--disable-features=TranslateUI,VizDisplayCompositor",
		"--user-data-dir=" + os.TempDir(),
		"--window-size=1920,1080",
		"--disable-web-security",                       // Allow cross-origin requests
		"--disable-features=VizDisplayCompositor",
		"--disable-blink-features=AutomationControlled", // Hide automation
		"--user-agent=Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
	}
	
	// Add YouTube cookies if available for better access
	youtubeCookiesPath := filepath.Join(cbe.workingDir, "youtube_cookies.txt")
	if _, err := os.Stat(youtubeCookiesPath); err == nil {
		chromeArgs = append(chromeArgs, "--cookie-file="+youtubeCookiesPath)
		log.Printf("🍪 Using YouTube cookies: %s", youtubeCookiesPath)
	} else {
		log.Printf("⚠️ YouTube cookies not found at %s", youtubeCookiesPath)
	}
	
	chromeArgs = append(chromeArgs, "--dump-dom", youtubePostURL)

	log.Printf("🌐 Executing enhanced Chrome for YouTube community post: %s %v", chromePath, chromeArgs)

	// Set up command with extended timeout for YouTube
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, chromePath, chromeArgs...)
	
	// Capture both stdout and stderr
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Execute command
	err := cmd.Run()
	if err != nil {
		log.Printf("❌ Enhanced Chrome execution failed: %v", err)
		log.Printf("❌ Chrome stderr: %s", stderr.String())
		
		// Try fallback approach without virtual-time-budget
		log.Printf("🔄 Trying fallback approach without virtual-time-budget")
		return cbe.extractYouTubeCommunityPostFallback(youtubePostURL, chromePath)
	}

	html := stdout.String()
	log.Printf("✅ Enhanced Chrome rendered %d bytes of HTML for YouTube community post", len(html))

	// Save HTML for debugging if needed
	if err := os.WriteFile(tempFile, []byte(html), 0644); err == nil {
		log.Printf("🔍 HTML saved to %s for debugging", tempFile)
	}

	// Extract content from rendered HTML
	mediaInfos, err := cbe.extractYouTubeCommunityPostFromHTML(html, youtubePostURL)
	
	// If browser extraction failed, try third-party API fallback
	if err != nil || len(mediaInfos) == 0 {
		log.Printf("🔄 Browser extraction failed/empty, trying YouTube operational API fallback")
		return cbe.extractYouTubeCommunityPostFromAPI(youtubePostURL)
	}
	
	return mediaInfos, err
}

// extractYouTubeCommunityPostFallback tries a simpler approach without virtual-time-budget
func (cbe *CliBrowserExtractor) extractYouTubeCommunityPostFallback(youtubePostURL, chromePath string) ([]*MediaInfo, error) {
	log.Printf("🔄 Trying YouTube community post fallback extraction")

	// Simpler Chrome arguments for fallback
	chromeArgsFallback := []string{
		"--headless=new",
		"--no-sandbox",
		"--disable-gpu",
		"--disable-dev-shm-usage",
		"--no-first-run",
		"--disable-blink-features=AutomationControlled",
		"--user-agent=Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		"--timeout=30",
		"--dump-dom",
		youtubePostURL,
	}

	// Add cookies if available
	youtubeCookiesPath := filepath.Join(cbe.workingDir, "youtube_cookies.txt")
	if _, err := os.Stat(youtubeCookiesPath); err == nil {
		chromeArgsFallback = append(chromeArgsFallback[:len(chromeArgsFallback)-2], "--cookie-file="+youtubeCookiesPath, "--dump-dom", youtubePostURL)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, chromePath, chromeArgsFallback...)
	output, err := cmd.Output()
	
	if err != nil {
		return nil, fmt.Errorf("fallback Chrome execution failed: %v", err)
	}

	html := string(output)
	log.Printf("✅ Fallback Chrome rendered %d bytes of HTML", len(html))

	mediaInfos, err := cbe.extractYouTubeCommunityPostFromHTML(html, youtubePostURL)
	
	// If fallback browser extraction also failed, try third-party API
	if err != nil || len(mediaInfos) == 0 {
		log.Printf("🔄 Fallback browser extraction failed/empty, trying YouTube operational API")
		return cbe.extractYouTubeCommunityPostFromAPI(youtubePostURL)
	}
	
	return mediaInfos, err
}

// extractYouTubeCommunityPostFromHTML extracts content from YouTube community post HTML
func (cbe *CliBrowserExtractor) extractYouTubeCommunityPostFromHTML(html, youtubePostURL string) ([]*MediaInfo, error) {
	var mediaInfos []*MediaInfo

	// Extract community post images with multiple patterns for YouTube's structure
	imagePatterns := []string{
		// YouTube community post image patterns (2025 structure)
		`<img[^>]*src="(https://yt3\.ggpht\.com/[^"]*)"[^>]*class="[^"]*community[^"]*"`,
		`<img[^>]*class="[^"]*community[^"]*"[^>]*src="(https://yt3\.ggpht\.com/[^"]*)"`,
		// YouTube thumbnail patterns in community posts
		`<img[^>]*src="(https://i\.ytimg\.com/vi/[^"]*)"[^>]*community`,
		`<img[^>]*src="(https://lh3\.googleusercontent\.com/[^"]*)"[^>]*>`,
		// Generic YouTube image hosting patterns
		`<img[^>]*src="(https://yt[0-9]*\.ggpht\.com/[^"]*)"[^>]*>`,
		`<img[^>]*src="(https://lh[0-9]*\.googleusercontent\.com/[^"]*)"[^>]*>`,
		// Open Graph and meta tags
		`<meta[^>]*property="og:image"[^>]*content="(https://[^"]*yt[^"]*\.com/[^"]*)"`,
		`<meta[^>]*name="twitter:image"[^>]*content="(https://[^"]*yt[^"]*\.com/[^"]*)"`,
	}

	var imageURLs []string
	for _, pattern := range imagePatterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindAllStringSubmatch(html, -1)
		for _, match := range matches {
			if len(match) > 1 {
				imageURLs = append(imageURLs, match[1])
			}
		}
	}

	// Filter and clean image URLs
	var communityImages []string
	seen := make(map[string]bool)
	for _, url := range imageURLs {
		// Skip profile pictures and channel avatars
		if !strings.Contains(url, "s48-") && !strings.Contains(url, "s88-") && 
		   !strings.Contains(url, "default") && !strings.Contains(url, "avatar") {
			// Clean URL (remove HTML entities)
			cleanURL := strings.ReplaceAll(url, "&amp;", "&")
			// Upgrade to higher quality if possible
			cleanURL = cbe.upgradeYouTubeImageURL(cleanURL)
			
			if !seen[cleanURL] {
				communityImages = append(communityImages, cleanURL)
				seen[cleanURL] = true
			}
		}
	}

	log.Printf("🖼️ Found %d YouTube community post images (from %d total URLs)", len(communityImages), len(imageURLs))

	// Extract text content from community post
	caption := cbe.extractYouTubeCommunityCaption(html)

	// If no images found, try alternative extraction methods
	if len(communityImages) == 0 {
		log.Printf("🔍 No images found with standard patterns, trying alternative extraction")
		
		// Look for any YouTube-hosted images in the HTML
		alternativePatterns := []string{
			`src="(https://[^"]*\.ytimg\.com/[^"]*)"`,
			`src="(https://[^"]*\.ggpht\.com/[^"]*)"`,
			`src="(https://[^"]*\.googleusercontent\.com/[^"]*)"`,
		}
		
		for _, pattern := range alternativePatterns {
			re := regexp.MustCompile(pattern)
			matches := re.FindAllStringSubmatch(html, -1)
			for _, match := range matches {
				if len(match) > 1 && !seen[match[1]] {
					// Skip very small images (likely icons or thumbnails)
					if !strings.Contains(match[1], "s16-") && !strings.Contains(match[1], "s24-") {
						communityImages = append(communityImages, match[1])
						seen[match[1]] = true
					}
				}
			}
		}
		
		log.Printf("🔍 Alternative extraction found %d additional images", len(communityImages))
	}

	// Download each image
	for i, imageURL := range communityImages {
		filename, err := cbe.downloadMediaFile(imageURL, "youtube", "photo", i)
		if err != nil {
			log.Printf("❌ Failed to download YouTube community image %d: %v", i+1, err)
			continue
		}

		mediaInfo := &MediaInfo{
			Type:     "photo",
			URL:      youtubePostURL,
			Caption:  caption,
			Filename: filename,
		}
		mediaInfos = append(mediaInfos, mediaInfo)
	}

	// If no images were successfully downloaded but we have text, return text-only
	if len(mediaInfos) == 0 && caption != "" {
		log.Printf("📝 No images downloaded, returning text-only content")
		textMediaInfo := &MediaInfo{
			Type:    "text",
			URL:     youtubePostURL,
			Caption: caption,
		}
		mediaInfos = append(mediaInfos, textMediaInfo)
	}

	if len(mediaInfos) == 0 {
		return nil, fmt.Errorf("no YouTube community post content found in rendered HTML")
	}

	return mediaInfos, nil
}

// upgradeYouTubeImageURL upgrades YouTube image URLs to higher quality
func (cbe *CliBrowserExtractor) upgradeYouTubeImageURL(url string) string {
	// Upgrade YouTube image quality parameters
	url = strings.ReplaceAll(url, "=s48", "=s800")  // Small to large
	url = strings.ReplaceAll(url, "=s88", "=s800")  // Medium to large
	url = strings.ReplaceAll(url, "=s176", "=s800") // Larger to max
	url = strings.ReplaceAll(url, "=w48", "=w800")  // Width small to large
	url = strings.ReplaceAll(url, "=w88", "=w800")  // Width medium to large
	url = strings.ReplaceAll(url, "=h48", "=h800")  // Height small to large
	url = strings.ReplaceAll(url, "=h88", "=h800")  // Height medium to large
	
	// For ytimg.com URLs, try to get max resolution
	if strings.Contains(url, "ytimg.com") {
		if !strings.Contains(url, "maxresdefault") && !strings.Contains(url, "hqdefault") {
			// Try to upgrade thumbnail quality
			url = strings.ReplaceAll(url, "default.jpg", "maxresdefault.jpg")
			url = strings.ReplaceAll(url, "mqdefault.jpg", "maxresdefault.jpg")
			url = strings.ReplaceAll(url, "sddefault.jpg", "maxresdefault.jpg")
		}
	}
	
	return url
}

// extractYouTubeCommunityCaption extracts text content from YouTube community post
func (cbe *CliBrowserExtractor) extractYouTubeCommunityCaption(html string) string {
	// Try to extract community post text using multiple patterns
	patterns := []string{
		// YouTube community post content patterns (2025)
		`<div[^>]*id="content-text"[^>]*>(.*?)</div>`,
		`<div[^>]*class="[^"]*post-content[^"]*"[^>]*>(.*?)</div>`,
		`<span[^>]*class="[^"]*community-post-text[^"]*"[^>]*>(.*?)</span>`,
		// Meta tag patterns for community posts
		`<meta[^>]*property="og:description"[^>]*content="([^"]*)"`,
		`<meta[^>]*name="description"[^>]*content="([^"]*)"`,
		`<meta[^>]*property="twitter:description"[^>]*content="([^"]*)"`,
		// Title patterns
		`<title[^>]*>([^<]+?) - YouTube</title>`,
		// JSON-LD patterns
		`"description"\s*:\s*"([^"]*)"`,
		`"text"\s*:\s*"([^"]*)"`,
		// Generic content patterns
		`<p[^>]*class="[^"]*content[^"]*"[^>]*>(.*?)</p>`,
		`<div[^>]*data-content[^>]*>(.*?)</div>`,
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(`(?s)` + pattern) // (?s) makes . match newlines
		if match := re.FindStringSubmatch(html); len(match) > 1 {
			caption := strings.TrimSpace(match[1])
			if caption != "" && caption != "null" && len(caption) > 5 {
				// Skip generic YouTube text
				if !strings.Contains(strings.ToLower(caption), "youtube") ||
				   !strings.Contains(strings.ToLower(caption), "sign in") ||
				   len(caption) > 50 {
					// Clean and decode the caption
					caption = cbe.cleanYouTubeText(caption)
					if len(caption) > 5 {
						return caption
					}
				}
			}
		}
	}

	return ""
}

// cleanYouTubeText cleans and decodes YouTube community post text
func (cbe *CliBrowserExtractor) cleanYouTubeText(rawText string) string {
	text := rawText
	
	// Remove HTML tags
	text = regexp.MustCompile(`<[^>]*>`).ReplaceAllString(text, "")
	
	// Decode HTML entities
	text = strings.ReplaceAll(text, "&amp;", "&")
	text = strings.ReplaceAll(text, "&lt;", "<")
	text = strings.ReplaceAll(text, "&gt;", ">")
	text = strings.ReplaceAll(text, "&quot;", "\"")
	text = strings.ReplaceAll(text, "&#39;", "'")
	text = strings.ReplaceAll(text, "&#34;", "\"")
	text = strings.ReplaceAll(text, "&nbsp;", " ")
	
	// Decode JSON escape sequences
	text = strings.ReplaceAll(text, `\"`, `"`)
	text = strings.ReplaceAll(text, `\\n`, "\n")
	text = strings.ReplaceAll(text, `\\t`, "\t")
	text = strings.ReplaceAll(text, `\\r`, "\r")
	
	// Clean up multiple spaces and newlines
	text = regexp.MustCompile(`\s+`).ReplaceAllString(text, " ")
	text = strings.TrimSpace(text)
	
	return text
}

// YouTubeOperationalAPIResponse represents the response from YouTube operational API
type YouTubeOperationalAPIResponse struct {
	Items []struct {
		ID      string `json:"id"`
		Snippet struct {
			Text            string `json:"text"`
			AuthorDisplayName string `json:"authorDisplayName"`
			AuthorChannelURL  string `json:"authorChannelUrl"`
			PublishedAt       string `json:"publishedAt"`
		} `json:"snippet"`
		ContentDetails struct {
			Images []struct {
				URL    string `json:"url"`
				Width  int    `json:"width"`
				Height int    `json:"height"`
			} `json:"images"`
		} `json:"contentDetails"`
	} `json:"items"`
}

// extractYouTubeCommunityPostFromAPI uses YouTube operational API as fallback
func (cbe *CliBrowserExtractor) extractYouTubeCommunityPostFromAPI(youtubePostURL string) ([]*MediaInfo, error) {
	log.Printf("🌐 Using YouTube operational API for community post extraction")
	
	// Extract post ID from URL
	postID := extractYouTubePostID(youtubePostURL)
	if postID == "" {
		return nil, fmt.Errorf("unable to extract post ID from URL: %s", youtubePostURL)
	}
	
	// Call YouTube operational API
	apiURL := fmt.Sprintf("https://yt.lemnoslife.com/community?part=snippet,contentDetails&id=%s", postID)
	
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create API request: %v", err)
	}
	
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("API request failed: %v", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("API returned status %d", resp.StatusCode)
	}
	
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read API response: %v", err)
	}
	
	var apiResponse YouTubeOperationalAPIResponse
	if err := json.Unmarshal(body, &apiResponse); err != nil {
		return nil, fmt.Errorf("failed to parse API response: %v", err)
	}
	
	if len(apiResponse.Items) == 0 {
		return nil, fmt.Errorf("no community post data found in API response")
	}
	
	var mediaInfos []*MediaInfo
	item := apiResponse.Items[0]
	
	// Format caption with author information
	caption := ""
	if item.Snippet.AuthorDisplayName != "" {
		caption = fmt.Sprintf("📺 %s\n\n%s", item.Snippet.AuthorDisplayName, item.Snippet.Text)
	} else {
		caption = item.Snippet.Text
	}
	
	// Download images if available
	if len(item.ContentDetails.Images) > 0 {
		log.Printf("🖼️ API found %d images in community post", len(item.ContentDetails.Images))
		
		for i, image := range item.ContentDetails.Images {
			// Skip very small images (likely thumbnails)
			if image.Width < 100 || image.Height < 100 {
				continue
			}
			
			filename, err := cbe.downloadMediaFile(image.URL, "youtube", "photo", i)
			if err != nil {
				log.Printf("❌ Failed to download YouTube API image %d: %v", i+1, err)
				continue
			}
			
			mediaInfo := &MediaInfo{
				Type:     "photo",
				URL:      youtubePostURL,
				Caption:  caption,
				Filename: filename,
			}
			mediaInfos = append(mediaInfos, mediaInfo)
		}
	}
	
	// If no images were downloaded but we have text, return text-only
	if len(mediaInfos) == 0 && caption != "" {
		log.Printf("📝 API: No images downloaded, returning text-only content")
		textMediaInfo := &MediaInfo{
			Type:    "text",
			URL:     youtubePostURL,
			Caption: caption,
		}
		mediaInfos = append(mediaInfos, textMediaInfo)
	}
	
	if len(mediaInfos) == 0 {
		return nil, fmt.Errorf("no content extracted from YouTube operational API")
	}
	
	log.Printf("✅ YouTube operational API extracted %d media items", len(mediaInfos))
	return mediaInfos, nil
}

// extractYouTubePostID extracts post ID from YouTube community post URL
func extractYouTubePostID(url string) string {
	// YouTube community post URL pattern: https://youtube.com/post/UgkxRCmy0DZJ20UO8n8jRJDBa9RE6NJRu9Ip
	re := regexp.MustCompile(`/post/([A-Za-z0-9_-]+)`)
	matches := re.FindStringSubmatch(url)
	if len(matches) >= 2 {
		return matches[1]
	}
	return ""
}