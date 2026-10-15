package services

import (
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

type UniversalBrowserExtractor struct {
	tempDir    string
	workingDir string
}

func NewUniversalBrowserExtractor(workingDir, tempDir string) *UniversalBrowserExtractor {
	return &UniversalBrowserExtractor{
		tempDir:    tempDir,
		workingDir: workingDir,
	}
}

// ExtractMedia is the universal fallback method for all social media platforms
func (ube *UniversalBrowserExtractor) ExtractMedia(mediaURL string) ([]*MediaInfo, error) {
	return ube.ExtractMediaWithContext(context.Background(), mediaURL)
}

// ExtractMediaWithContext performs browser extraction respecting the parent context deadline.
// If the parent context is cancelled or expires, extraction will be aborted.
func (ube *UniversalBrowserExtractor) ExtractMediaWithContext(ctx context.Context, mediaURL string) ([]*MediaInfo, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	log.Printf("🌐 Starting universal headless browser extraction for: %s", mediaURL)

	// Determine platform and use appropriate extraction strategy
	platform := ube.detectPlatform(mediaURL)
	log.Printf("🔍 Detected platform: %s", platform)

	switch platform {
	case "instagram":
		return ube.extractInstagramMedia(mediaURL)
	case "twitter":
		return ube.extractTwitterMedia(mediaURL)
	case "tiktok":
		return ube.extractTikTokMedia(mediaURL)
	default:
		return ube.extractGenericMedia(mediaURL)
	}
}

func (ube *UniversalBrowserExtractor) detectPlatform(mediaURL string) string {
	if strings.Contains(mediaURL, "instagram.com") {
		return "instagram"
	} else if strings.Contains(mediaURL, "twitter.com") || strings.Contains(mediaURL, "x.com") {
		return "twitter"
	} else if strings.Contains(mediaURL, "tiktok.com") {
		return "tiktok"
	}
	return "generic"
}

func (ube *UniversalBrowserExtractor) extractInstagramMedia(mediaURL string) ([]*MediaInfo, error) {
	log.Printf("📸 Extracting Instagram media with headless browser")

	// Extract username from URL
	username := ExtractInstagramUsername(mediaURL)

	// Use longer timeout for Instagram carousels
	html, err := ube.renderPageWithBrowserTimeout(mediaURL, 15*time.Second, 45*time.Second)
	if err != nil {
		return nil, err
	}

	// Extract Instagram author info from HTML
	displayName, detectedUsername := ube.extractInstagramAuthorInfo(html)
	if detectedUsername != "" {
		username = detectedUsername
	}

	var mediaInfos []*MediaInfo

	// Instagram image patterns (photos and carousels)
	imagePatterns := []string{
		// Modern Instagram image patterns with query parameters
		`<img[^>]*src="(https://scontent[^"]*\.cdninstagram\.com/v/t51\.2885-15/[^"]+)"[^>]*>`,
		`<img[^>]*src="(https://[^"]*\.cdninstagram\.com/v/t51\.2885-15/[^"]+)"[^>]*>`,
		// Patterns with alt text (carousel images often have these)
		`<img[^>]*alt="[^"]*"[^>]*src="(https://scontent[^"]*\.cdninstagram\.com/[^"]+)"`,
		`<img[^>]*src="(https://scontent[^"]*\.cdninstagram\.com/[^"]+)"[^>]*alt="[^"]*"`,
		// Legacy patterns with .jpg extension
		`<img[^>]*src="(https://[^"]*\.cdninstagram\.com/[^"]*\.jpg[^"]*)"[^>]*alt="[^"]*"`,
		`<img[^>]*alt="[^"]*"[^>]*src="(https://[^"]*\.cdninstagram\.com/[^"]*\.jpg[^"]*)"`,
		// Alternative CDN patterns
		`<img[^>]*src="(https://[^"]*instagram[^"]*\.com/[^"]*\.jpg[^"]*)"`,
	}

	imageURLs := ube.extractURLsWithPatterns(html, imagePatterns)
	log.Printf("🖼️ Found %d Instagram images", len(imageURLs))

	// Download images
	for i, imageURL := range imageURLs {
		cleanURL := ube.cleanInstagramURL(imageURL)
		filename, err := ube.downloadMediaFile(cleanURL, "instagram", "photo", i)
		if err != nil {
			log.Printf("❌ Failed to download Instagram image %d: %v", i+1, err)
			continue
		}

		originalCaption := ube.extractInstagramCaption(html)
		mediaInfo := &MediaInfo{
			Type:     "photo",
			URL:      mediaURL,
			Caption:  FormatAuthorCaption("instagram", displayName, username, originalCaption),
			Filename: filename,
		}
		mediaInfos = append(mediaInfos, mediaInfo)
	}

	// Instagram video patterns
	videoPatterns := []string{
		`<video[^>]*src="(https://[^"]*\.cdninstagram\.com/[^"]*\.mp4[^"]*)"`,
		`<source[^>]*src="(https://[^"]*\.cdninstagram\.com/[^"]*\.mp4[^"]*)"`,
	}

	videoURLs := ube.extractURLsWithPatterns(html, videoPatterns)
	log.Printf("🎥 Found %d Instagram videos", len(videoURLs))

	// Download videos
	for i, videoURL := range videoURLs {
		cleanURL := ube.cleanInstagramURL(videoURL)
		filename, err := ube.downloadMediaFile(cleanURL, "instagram", "video", i)
		if err != nil {
			log.Printf("❌ Failed to download Instagram video %d: %v", i+1, err)
			continue
		}

		originalCaption := ube.extractInstagramCaption(html)
		mediaInfo := &MediaInfo{
			Type:     "video",
			URL:      mediaURL,
			Caption:  FormatAuthorCaption("instagram", displayName, username, originalCaption),
			Filename: filename,
		}
		mediaInfos = append(mediaInfos, mediaInfo)
	}

	if len(mediaInfos) == 0 {
		return nil, fmt.Errorf("no Instagram media found")
	}

	return mediaInfos, nil
}

func (ube *UniversalBrowserExtractor) extractTwitterMedia(mediaURL string) ([]*MediaInfo, error) {
	log.Printf("🐦 Extracting Twitter media via FxTwitter API (full text support)")

	// Always use TwitterExtractor first - it has FxTwitter API which provides full text
	twitterExtractor := NewTwitterExtractor(ube.workingDir, ube.tempDir)
	mediaInfos, err := twitterExtractor.ExtractTweet(mediaURL)
	if err == nil && len(mediaInfos) > 0 {
		return mediaInfos, nil
	}
	log.Printf("⚠️ TwitterExtractor failed: %v, trying browser fallback", err)

	// Fallback to browser extraction for images
	html, err := ube.renderPageWithBrowser(mediaURL, 10*time.Second)
	if err != nil {
		log.Printf("❌ Browser rendering also failed: %v", err)
		return nil, fmt.Errorf("all Twitter extraction methods failed")
	}

	// Extract images from rendered HTML
	imagePatterns := []string{
		// Twitter image patterns with css-9pa8cd class (2025 structure)
		`<img[^>]*alt="Image"[^>]*src="(https://pbs\.twimg\.com/media/[^"]*)"[^>]*class="css-9pa8cd"`,
		`<img[^>]*class="css-9pa8cd"[^>]*alt="Image"[^>]*src="(https://pbs\.twimg\.com/media/[^"]*)"`,
		// More generic patterns
		`<img[^>]*src="(https://pbs\.twimg\.com/media/[^"?]+\?format=jpg[^"]*)"`,
		`<img[^>]*src="(https://pbs\.twimg\.com/media/[^"]+)"[^>]*alt="Image"`,
	}

	imageURLs := ube.extractURLsWithPatterns(html, imagePatterns)

	// Filter out profile images and other non-tweet images
	var tweetImages []string
	for _, url := range imageURLs {
		if !strings.Contains(url, "profile_images") && !strings.Contains(url, "_normal") {
			// Clean up the URL - upgrade to higher quality
			cleanURL := ube.cleanTwitterURL(url)
			tweetImages = append(tweetImages, cleanURL)
		}
	}

	log.Printf("🖼️ Found %d Twitter images via browser", len(tweetImages))

	// If we found images, download them
	if len(tweetImages) > 0 {
		var mediaInfos []*MediaInfo

		// Extract author info and caption
		displayName, username := ube.extractTwitterAuthorInfo(html)
		tweetText := ube.extractTwitterCaption(html)
		caption := FormatAuthorCaption("twitter", displayName, username, tweetText)

		// Download each image
		for i, imageURL := range tweetImages {
			filename, err := ube.downloadMediaFile(imageURL, "twitter", "photo", i)
			if err != nil {
				log.Printf("❌ Failed to download Twitter image %d: %v", i+1, err)
				continue
			}

			mediaInfo := &MediaInfo{
				Type:     "photo",
				URL:      mediaURL,
				Caption:  caption,
				Filename: filename,
			}
			mediaInfos = append(mediaInfos, mediaInfo)
		}

		if len(mediaInfos) > 0 {
			return mediaInfos, nil
		}
	}

	// Browser fallback also didn't find anything
	log.Printf("📋 No images found via browser fallback either")
	return nil, fmt.Errorf("no Twitter media found via any method")
}

func (ube *UniversalBrowserExtractor) extractTikTokMedia(mediaURL string) ([]*MediaInfo, error) {
	log.Printf("🎵 Extracting TikTok media with headless browser")

	// Extract username from URL
	username := ExtractTikTokUsername(mediaURL)

	html, err := ube.renderPageWithBrowser(mediaURL, 8*time.Second)
	if err != nil {
		return nil, err
	}

	// Extract TikTok author info from HTML
	displayName, detectedUsername := ube.extractTikTokAuthorInfo(html)
	if detectedUsername != "" {
		username = detectedUsername
	}

	// Debug: Save HTML content for analysis (temporary)
	if os.Getenv("DEBUG_TIKTOK") == "true" {
		debugFile := filepath.Join(ube.tempDir, fmt.Sprintf("tiktok_debug_%d.html", time.Now().Unix()))
		if err := os.WriteFile(debugFile, []byte(html), 0644); err == nil {
			log.Printf("🐛 [DEBUG] Saved TikTok HTML to: %s", debugFile)
		}
	}

	var mediaInfos []*MediaInfo

	// TikTok slideshow patterns (similar to existing implementation)
	slideshowPatterns := []string{
		`<img[^>]*src="(https://[^"]*\.tiktokcdn\.com/[^"]*~tplv-photomode-image\.jpeg[^"]*)"`,
		`<img[^>]*class="[^"]*ImgPhotoSlide[^"]*"[^>]*src="(https://[^"]*\.tiktokcdn\.com/[^"]*~tplv-photomode-image\.jpeg[^"]*)"`,
		`swiper-slide[^>]*>.*?<img[^>]*src="(https://[^"]*photomode-image[^"]*\.jpeg[^"]*)"`,
	}

	imageURLs := ube.extractURLsWithPatterns(html, slideshowPatterns)

	// Deduplicate TikTok images using hash from URL
	uniqueImages := ube.deduplicateTikTokImages(imageURLs)
	log.Printf("🖼️ Found %d unique TikTok slideshow images (deduplicated from %d)", len(uniqueImages), len(imageURLs))

	// Download images
	for i, imageURL := range uniqueImages {
		filename, err := ube.downloadMediaFile(imageURL, "tiktok", "photo", i)
		if err != nil {
			log.Printf("❌ Failed to download TikTok image %d: %v", i+1, err)
			continue
		}

		originalCaption := ube.extractTikTokCaption(html)
		mediaInfo := &MediaInfo{
			Type:     "photo",
			URL:      mediaURL,
			Caption:  FormatAuthorCaption("tiktok", displayName, username, originalCaption),
			Filename: filename,
		}
		mediaInfos = append(mediaInfos, mediaInfo)
	}

	// If no slideshow images, try video patterns
	if len(mediaInfos) == 0 {
		videoPatterns := []string{
			`<video[^>]*src="(https://[^"]*\.tiktokcdn\.com/[^"]*\.mp4[^"]*)"`,
			`<source[^>]*src="(https://[^"]*\.tiktokcdn\.com/[^"]*\.mp4[^"]*)"`,
		}

		videoURLs := ube.extractURLsWithPatterns(html, videoPatterns)
		log.Printf("🎥 Found %d TikTok videos", len(videoURLs))

		// Download videos
		for i, videoURL := range videoURLs {
			filename, err := ube.downloadMediaFile(videoURL, "tiktok", "video", i)
			if err != nil {
				log.Printf("❌ Failed to download TikTok video %d: %v", i+1, err)
				continue
			}

			originalCaption := ube.extractTikTokCaption(html)
			mediaInfo := &MediaInfo{
				Type:     "video",
				URL:      mediaURL,
				Caption:  FormatAuthorCaption("tiktok", displayName, username, originalCaption),
				Filename: filename,
			}
			mediaInfos = append(mediaInfos, mediaInfo)
		}
	}

	if len(mediaInfos) == 0 {
		return nil, fmt.Errorf("no TikTok media found")
	}

	return mediaInfos, nil
}

func (ube *UniversalBrowserExtractor) extractGenericMedia(mediaURL string) ([]*MediaInfo, error) {
	log.Printf("🌐 Extracting generic media with headless browser")

	html, err := ube.renderPageWithBrowser(mediaURL, 5*time.Second)
	if err != nil {
		return nil, err
	}

	var mediaInfos []*MediaInfo

	// Generic patterns for Open Graph and common media
	imagePatterns := []string{
		`<meta[^>]*property="og:image"[^>]*content="([^"]*\.(jpg|jpeg|png|webp)[^"]*)"`,
		`<meta[^>]*name="twitter:image"[^>]*content="([^"]*\.(jpg|jpeg|png|webp)[^"]*)"`,
		`<img[^>]*src="([^"]*\.(jpg|jpeg|png|webp)[^"]*)"[^>]*class="[^"]*main[^"]*"`,
	}

	imageURLs := ube.extractURLsWithPatterns(html, imagePatterns)
	log.Printf("🖼️ Found %d generic images", len(imageURLs))

	// Download images
	for i, imageURL := range imageURLs {
		filename, err := ube.downloadMediaFile(imageURL, "generic", "photo", i)
		if err != nil {
			log.Printf("❌ Failed to download generic image %d: %v", i+1, err)
			continue
		}

		mediaInfo := &MediaInfo{
			Type:     "photo",
			URL:      mediaURL,
			Caption:  ube.extractGenericCaption(html),
			Filename: filename,
		}
		mediaInfos = append(mediaInfos, mediaInfo)
	}

	// Generic video patterns
	videoPatterns := []string{
		`<video[^>]*src="([^"]*\.mp4[^"]*)"`,
		`<source[^>]*src="([^"]*\.mp4[^"]*)"`,
		`<meta[^>]*property="og:video"[^>]*content="([^"]*\.mp4[^"]*)"`,
	}

	videoURLs := ube.extractURLsWithPatterns(html, videoPatterns)
	log.Printf("🎥 Found %d generic videos", len(videoURLs))

	// Download videos
	for i, videoURL := range videoURLs {
		filename, err := ube.downloadMediaFile(videoURL, "generic", "video", i)
		if err != nil {
			log.Printf("❌ Failed to download generic video %d: %v", i+1, err)
			continue
		}

		mediaInfo := &MediaInfo{
			Type:     "video",
			URL:      mediaURL,
			Caption:  ube.extractGenericCaption(html),
			Filename: filename,
		}
		mediaInfos = append(mediaInfos, mediaInfo)
	}

	if len(mediaInfos) == 0 {
		return nil, fmt.Errorf("no generic media found")
	}

	return mediaInfos, nil
}

// Browser rendering utility
func (ube *UniversalBrowserExtractor) renderPageWithBrowser(pageURL string, waitTime time.Duration) (string, error) {
	return ube.renderPageWithBrowserTimeout(pageURL, waitTime, 30*time.Second)
}

// Browser rendering utility with custom timeout
func (ube *UniversalBrowserExtractor) renderPageWithBrowserTimeout(pageURL string, waitTime time.Duration, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Find Chrome executable (similar to TikTok extractor)
	homeDir := os.Getenv("HOME")
	possiblePaths := []string{
		filepath.Join(homeDir, "chrome/chrome"),
		filepath.Join(homeDir, "bin/chrome"),
		"/usr/bin/google-chrome",
		"/usr/bin/google-chrome-stable",
		"/usr/bin/chromium-browser",
		"/usr/bin/chromium",
		"/opt/google/chrome/chrome",
		"/snap/bin/chromium",
	}

	execPath := ""
	for _, path := range possiblePaths {
		if _, err := os.Stat(path); err == nil {
			execPath = path
			break
		}
	}

	// Chrome options with extensive logging suppression
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.DisableGPU,
		chromedp.NoSandbox,
		chromedp.Headless,
		chromedp.UserAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36"),
		chromedp.Flag("virtual-time-budget", "10000"),
		chromedp.Flag("disable-web-security", true),
		chromedp.Flag("disable-extensions", true),
		chromedp.Flag("disable-plugins", true),
		chromedp.Flag("disable-images", false),
		chromedp.Flag("disable-javascript", false),
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
		chromedp.Flag("log-level", "3"),
		chromedp.Flag("silent", true),
		chromedp.Flag("disable-logging", true),
		chromedp.Flag("quiet", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-cookie-encryption", true),
		chromedp.Flag("disable-sync", true),
		chromedp.Flag("disable-background-networking", true),
		chromedp.Flag("disable-component-update", true),
		chromedp.Flag("disable-domain-reliability", true),
		chromedp.Flag("disable-features", "VizDisplayCompositor,AudioServiceOutOfProcess"),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-setuid-sandbox", true),
	)

	if execPath != "" {
		log.Printf("🔍 Using Chrome at: %s", execPath)
		opts = append(opts, chromedp.ExecPath(execPath))
	}

	allocCtx, allocCancel := chromedp.NewExecAllocator(ctx, opts...)
	defer allocCancel()

	browserCtx, browserCancel := chromedp.NewContext(allocCtx, chromedp.WithLogf(func(string, ...interface{}) {}))
	defer browserCancel()

	var htmlContent string
	err := chromedp.Run(browserCtx,
		chromedp.Navigate(pageURL),
		chromedp.Sleep(waitTime),
		chromedp.WaitVisible(`body`, chromedp.ByQuery),
		chromedp.Sleep(2*time.Second),
		chromedp.InnerHTML(`html`, &htmlContent, chromedp.ByQuery),
	)

	if err != nil {
		return "", fmt.Errorf("browser navigation failed: %v", err)
	}

	log.Printf("🌐 Retrieved HTML content, length: %d", len(htmlContent))
	return htmlContent, nil
}

// Helper methods for extraction
func (ube *UniversalBrowserExtractor) extractURLsWithPatterns(html string, patterns []string) []string {
	var urls []string
	seen := make(map[string]bool)

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindAllStringSubmatch(html, -1)
		for _, match := range matches {
			if len(match) > 1 {
				url := match[1]
				if !seen[url] {
					urls = append(urls, url)
					seen[url] = true
				}
			}
		}
	}

	return urls
}

func (ube *UniversalBrowserExtractor) deduplicateTikTokImages(imageURLs []string) []string {
	seen := make(map[string]bool)
	var unique []string

	hashPattern := regexp.MustCompile(`([a-f0-9]{32})~tplv`)

	for _, imageURL := range imageURLs {
		hash := ""
		if match := hashPattern.FindStringSubmatch(imageURL); len(match) > 1 {
			hash = match[1]
		} else {
			hash = imageURL // Use full URL as fallback
		}

		if !seen[hash] {
			unique = append(unique, imageURL)
			seen[hash] = true
		}
	}

	return unique
}

// URL cleaning methods
func (ube *UniversalBrowserExtractor) cleanInstagramURL(rawURL string) string {
	// For Instagram carousel images, we need to keep all parameters
	// These parameters are often required for the image to load correctly
	return rawURL
}

func (ube *UniversalBrowserExtractor) cleanTwitterURL(rawURL string) string {
	// Upgrade Twitter image quality
	cleanURL := strings.ReplaceAll(rawURL, "?format=", "?format=")
	cleanURL = strings.ReplaceAll(cleanURL, "&name=small", "&name=orig")
	cleanURL = strings.ReplaceAll(cleanURL, "&name=medium", "&name=orig")
	cleanURL = strings.ReplaceAll(cleanURL, "&name=large", "&name=orig")
	if !strings.Contains(cleanURL, "&name=") {
		cleanURL += "&name=orig"
	}
	return cleanURL
}

// Caption extraction methods
func (ube *UniversalBrowserExtractor) extractInstagramCaption(html string) string {
	patterns := []string{
		`<meta[^>]*property="og:description"[^>]*content="([^"]*)"`,
		`"caption":"([^"]*)"`,
		`<title>([^<]*)</title>`,
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		if match := re.FindStringSubmatch(html); len(match) > 1 {
			caption := strings.TrimSpace(match[1])
			if caption != "" && !strings.Contains(caption, "Instagram") {
				return caption
			}
		}
	}

	return ""
}

func (ube *UniversalBrowserExtractor) extractTwitterCaption(html string) string {
	// Try to extract the main tweet text content first
	patterns := []string{
		// Try Open Graph description first
		`<meta[^>]*property="og:description"[^>]*content="([^"]*)"`,
		// Try regular description meta tag
		`<meta[^>]*name="description"[^>]*content="([^"]*)"`,
		// Try JSON data for tweet text
		`"full_text":"([^"]*)"`,
		`"text":"([^"]*)"`,
		// Try tweet div content
		`<div[^>]*data-testid="tweetText"[^>]*>([^<]*)</div>`,
	}

	var bestCaption string
	maxLength := 0

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		if match := re.FindStringSubmatch(html); len(match) > 1 {
			caption := strings.TrimSpace(match[1])

			// Clean up common artifacts
			caption = strings.ReplaceAll(caption, "\\n", "\n")
			caption = strings.ReplaceAll(caption, "\\\"", "\"")
			caption = strings.ReplaceAll(caption, "\\/", "/")

			// Skip generic descriptions
			if caption != "" &&
				!strings.Contains(strings.ToLower(caption), "twitter") &&
				!strings.Contains(strings.ToLower(caption), "x.com") &&
				!strings.Contains(strings.ToLower(caption), "see posts, photos") &&
				len(caption) > maxLength {
				bestCaption = caption
				maxLength = len(caption)
			}
		}
	}

	if bestCaption != "" {
		return bestCaption
	}

	return ""
}

// extractTwitterAuthorInfo extracts author display name and username from Twitter HTML
func (ube *UniversalBrowserExtractor) extractTwitterAuthorInfo(html string) (displayName, username string) {
	// Twitter author patterns for browser-rendered content
	patterns := []string{
		// Modern Twitter/X structure using data-testid="User-Name"
		`data-testid="User-Name"[^>]*>.*?<span[^>]*>([^<]+)</span>.*?<span[^>]*>@([^<]+)</span>`,
		// Alternative order: username first, then display name
		`data-testid="User-Name"[^>]*>.*?<span[^>]*>@([^<]+)</span>.*?<span[^>]*>([^<]+)</span>`,
		// Meta tag patterns
		`<meta[^>]*property="og:title"[^>]*content="([^"]+) \(@([^)]+)\)`,
		`<meta[^>]*name="twitter:title"[^>]*content="([^"]+) \(@([^)]+)\)`,
		// Profile link patterns
		`<a href="/([^"]+)"[^>]*>.*?<span[^>]*>([^<]+)</span>`,
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindStringSubmatch(html)

		if len(matches) >= 3 {
			if strings.Contains(pattern, "User-Name") && strings.Contains(pattern, "@([^<]+).*?([^<]+)") {
				// data-testid pattern with username first: matches[1] is username, matches[2] is display name
				username = matches[1]
				displayName = matches[2]
			} else if strings.Contains(pattern, "User-Name") && strings.Contains(pattern, "([^<]+).*?@([^<]+)") {
				// data-testid pattern with display name first: matches[1] is display name, matches[2] is username
				displayName = matches[1]
				username = matches[2]
			} else if strings.Contains(pattern, "og:title") || strings.Contains(pattern, "twitter:title") {
				displayName = strings.TrimSpace(matches[1])
				username = strings.TrimSpace(matches[2])
			} else {
				// Link pattern: matches[1] is username, matches[2] is display name
				username = strings.TrimSpace(matches[1])
				displayName = strings.TrimSpace(matches[2])
			}

			// Clean up
			displayName = strings.TrimSpace(displayName)
			username = strings.TrimSpace(username)

			if displayName != "" || username != "" {
				log.Printf("✅ Extracted Twitter author: %s (@%s)", displayName, username)
				return displayName, username
			}
		}
	}

	return "", ""
}

func (ube *UniversalBrowserExtractor) extractTikTokCaption(html string) string {
	patterns := []string{
		`<meta[^>]*property="og:description"[^>]*content="([^"]*)"`,
		`"desc":"([^"]*)"`,
		`<title>([^<]*)</title>`,
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		if match := re.FindStringSubmatch(html); len(match) > 1 {
			caption := strings.TrimSpace(match[1])
			if caption != "" && !strings.Contains(caption, "TikTok") {
				return caption
			}
		}
	}

	return ""
}

func (ube *UniversalBrowserExtractor) extractGenericCaption(html string) string {
	patterns := []string{
		`<meta[^>]*property="og:title"[^>]*content="([^"]*)"`,
		`<meta[^>]*property="og:description"[^>]*content="([^"]*)"`,
		`<title>([^<]*)</title>`,
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		if match := re.FindStringSubmatch(html); len(match) > 1 {
			caption := strings.TrimSpace(match[1])
			if caption != "" {
				return caption
			}
		}
	}

	return ""
}

// Download utility
func (ube *UniversalBrowserExtractor) downloadMediaFile(mediaURL, platform, mediaType string, index int) (string, error) {
	// Ensure temp directory exists
	if err := os.MkdirAll(ube.tempDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create temp directory: %v", err)
	}

	// Determine file extension
	ext := ".jpg" // Default
	if mediaType == "video" {
		ext = ".mp4"
	} else if strings.Contains(mediaURL, ".png") {
		ext = ".png"
	} else if strings.Contains(mediaURL, ".webp") {
		ext = ".webp"
	}

	// Create unique filename
	timestamp := time.Now().Unix()
	filename := filepath.Join(ube.tempDir, fmt.Sprintf("%d_%s_%s_%d%s", timestamp, platform, mediaType, index, ext))

	// Create request with proper headers
	req, err := http.NewRequest("GET", mediaURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %v", err)
	}

	// Add platform-specific headers
	ube.addPlatformHeaders(req, platform)

	// Download the file
	resp, err := SharedDownloadClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to download: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	// Create the file
	file, err := os.Create(filename)
	if err != nil {
		return "", fmt.Errorf("failed to create file: %v", err)
	}
	defer file.Close()

	// Copy content to file
	_, err = io.Copy(file, resp.Body)
	if err != nil {
		os.Remove(filename) // Clean up on error
		return "", fmt.Errorf("failed to write file: %v", err)
	}

	log.Printf("✅ Downloaded %s media to: %s", platform, filename)
	return filename, nil
}

func (ube *UniversalBrowserExtractor) addPlatformHeaders(req *http.Request, platform string) {
	// Set common headers
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")
	req.Header.Set("Accept", "image/webp,image/apng,image/*,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	switch platform {
	case "instagram":
		req.Header.Set("Referer", "https://www.instagram.com/")
		req.Header.Set("sec-fetch-dest", "image")
		req.Header.Set("sec-fetch-mode", "no-cors")
		req.Header.Set("sec-fetch-site", "cross-site")
		req.Header.Set("x-ig-app-id", "936619743392459")
		req.Header.Set("x-ig-www-claim", "0")
		req.Header.Set("x-requested-with", "XMLHttpRequest")

		// Add Instagram cookies if available
		if cookiesPath := filepath.Join(ube.workingDir, "cookies.txt"); ube.fileExists(cookiesPath) {
			ube.addInstagramCookiesFromFile(req, cookiesPath)
		}

	case "twitter":
		req.Header.Set("Referer", "https://twitter.com/")
		req.Header.Set("sec-fetch-dest", "image")
		req.Header.Set("sec-fetch-mode", "no-cors")
		req.Header.Set("sec-fetch-site", "cross-site")

	case "tiktok":
		req.Header.Set("Referer", "https://www.tiktok.com/")
		req.Header.Set("sec-fetch-dest", "image")
		req.Header.Set("sec-fetch-mode", "no-cors")
		req.Header.Set("sec-fetch-site", "cross-site")
	}
}

// fileExists checks if a file exists
func (ube *UniversalBrowserExtractor) fileExists(filepath string) bool {
	_, err := os.Stat(filepath)
	return !os.IsNotExist(err)
}

// addInstagramCookiesFromFile adds Instagram cookies from cookies.txt to the request
func (ube *UniversalBrowserExtractor) addInstagramCookiesFromFile(req *http.Request, cookiesPath string) {
	file, err := os.Open(cookiesPath)
	if err != nil {
		return
	}
	defer file.Close()

	content, err := io.ReadAll(file)
	if err != nil {
		return
	}

	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.Fields(line)
		if len(parts) >= 7 && strings.Contains(parts[0], "instagram.com") {
			cookieName := parts[5]
			cookieValue := parts[6]

			// Add important Instagram cookies
			if cookieName == "sessionid" || cookieName == "csrftoken" || cookieName == "ds_user_id" {
				cookie := &http.Cookie{
					Name:  cookieName,
					Value: cookieValue,
				}
				req.AddCookie(cookie)
			}
		}
	}
}

// extractInstagramAuthorInfo extracts author display name and username from Instagram HTML
func (ube *UniversalBrowserExtractor) extractInstagramAuthorInfo(html string) (displayName, username string) {
	// Instagram author patterns
	patterns := []string{
		// Meta tag patterns
		`<meta[^>]*property="og:title"[^>]*content="([^"]+) \(@([^)]+)\)`,
		`<meta[^>]*name="twitter:title"[^>]*content="([^"]+) \(@([^)]+)\)`,
		// JSON-LD patterns
		`"author":\s*{\s*"@type":\s*"Person",\s*"name":\s*"([^"]+)",\s*"alternateName":\s*"@?([^"]+)"`,
		// Profile link patterns
		`href="/([^/]+)/"[^>]*>([^<]+)</a>.*?Verified`,
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindStringSubmatch(html)

		if len(matches) >= 3 {
			if strings.Contains(pattern, "og:title") || strings.Contains(pattern, "twitter:title") {
				displayName = strings.TrimSpace(matches[1])
				username = strings.TrimSpace(matches[2])
			} else if strings.Contains(pattern, "author") {
				displayName = strings.TrimSpace(matches[1])
				username = strings.TrimSpace(matches[2])
			} else {
				username = strings.TrimSpace(matches[1])
				displayName = strings.TrimSpace(matches[2])
			}

			if displayName != "" || username != "" {
				log.Printf("✅ Extracted Instagram author: %s (@%s)", displayName, username)
				return displayName, username
			}
		}
	}

	return "", ""
}

// extractTikTokAuthorInfo extracts author display name and username from TikTok HTML
func (ube *UniversalBrowserExtractor) extractTikTokAuthorInfo(html string) (displayName, username string) {
	// TikTok author patterns - ordered by reliability
	patterns := []string{
		// HTML data-e2e patterns (most reliable for new TikTok UI)
		`data-e2e="browse-username"[^>]*>([^<]+)</.*?data-e2e="browser-nickname"[^>]*>.*?<span[^>]*>([^<]+)</span>`,
		`data-e2e="browser-nickname"[^>]*>.*?<span[^>]*>([^<]+)</span>.*?data-e2e="browse-username"[^>]*>([^<]+)</`,
		// Individual patterns
		`data-e2e="browse-username"[^>]*>([^<]+)</span>`,
		`data-e2e="browser-nickname"[^>]*>.*?<span[^>]*>([^<]+)</span>`,
		// Meta tag patterns
		`<meta[^>]*property="og:title"[^>]*content="([^"]+) \(@([^)]+)\)`,
		`<meta[^>]*name="twitter:title"[^>]*content="([^"]+) \(@([^)]+)\)`,
		// JSON data patterns
		`"uniqueId":"([^"]+)".*?"nickname":"([^"]+)"`,
		`"author":\s*{\s*"uniqueId":\s*"([^"]+)",\s*"nickname":\s*"([^"]+)"`,
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindStringSubmatch(html)

		if len(matches) >= 2 {
			if strings.Contains(pattern, "browse-username.*browser-nickname") {
				// Username comes first, display name second
				username = strings.TrimSpace(matches[1])
				displayName = strings.TrimSpace(matches[2])
			} else if strings.Contains(pattern, "browser-nickname.*browse-username") {
				// Display name comes first, username second
				displayName = strings.TrimSpace(matches[1])
				username = strings.TrimSpace(matches[2])
			} else if strings.Contains(pattern, "browse-username") {
				// Just username
				username = strings.TrimSpace(matches[1])
			} else if strings.Contains(pattern, "browser-nickname") {
				// Just display name
				displayName = strings.TrimSpace(matches[1])
			} else if len(matches) >= 3 {
				if strings.Contains(pattern, "uniqueId.*nickname") {
					username = strings.TrimSpace(matches[1])
					displayName = strings.TrimSpace(matches[2])
				} else {
					displayName = strings.TrimSpace(matches[1])
					username = strings.TrimSpace(matches[2])
				}
			}

			// Clean up extracted values
			displayName = strings.TrimSpace(displayName)
			username = strings.TrimSpace(username)

			if displayName != "" || username != "" {
				log.Printf("✅ Extracted TikTok author: %s (@%s)", displayName, username)
				return displayName, username
			}
		}
	}

	log.Printf("⚠️ No TikTok author found in HTML")
	return "", ""
}
