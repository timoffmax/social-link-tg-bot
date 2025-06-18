package services

import (
	"bytes"
	"context"
	"fmt"
	"io/ioutil"
	"log"
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
	var tweetImages []string
	seen := make(map[string]bool)
	for _, url := range imageURLs {
		if !strings.Contains(url, "profile_images") && !strings.Contains(url, "_normal") {
			// Clean up URL (remove HTML entities)
			cleanURL := strings.ReplaceAll(url, "&amp;", "&")
			// Upgrade to high quality
			cleanURL = cbe.upgradeTwitterImageURL(cleanURL)
			
			if !seen[cleanURL] {
				tweetImages = append(tweetImages, cleanURL)
				seen[cleanURL] = true
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

	// Chrome command optimized for thread page rendering
	args := []string{
		"--headless=new",
		"--no-sandbox",
		"--disable-gpu",
		"--disable-dev-shm-usage",
		"--disable-extensions",
		"--no-first-run",
		"--disable-default-apps",
		"--virtual-time-budget=10000", // Give more time for thread loading
		"--run-all-compositor-stages-before-draw",
		"--user-data-dir=" + os.TempDir(),
		"--dump-dom",
		twitterURL,
	}

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