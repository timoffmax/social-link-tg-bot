package services

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
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
	case "youtube":
		return ube.extractYouTubeMedia(mediaURL)
	case "9gag":
		return ube.extract9GAGMedia(mediaURL)
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
	} else if strings.Contains(mediaURL, "youtube.com") || strings.Contains(mediaURL, "youtu.be") {
		return "youtube"
	} else if strings.Contains(mediaURL, "9gag.com") {
		return "9gag"
	}
	return "generic"
}

func (ube *UniversalBrowserExtractor) extractInstagramMedia(mediaURL string) ([]*MediaInfo, error) {
	log.Printf("📸 Extracting Instagram media with headless browser")

	html, err := ube.renderPageWithBrowser(mediaURL, 8*time.Second)
	if err != nil {
		return nil, err
	}

	var mediaInfos []*MediaInfo

	// Instagram image patterns (photos and carousels)
	imagePatterns := []string{
		// Main post images
		`<img[^>]*src="(https://[^"]*\.cdninstagram\.com/[^"]*\.jpg[^"]*)"[^>]*alt="[^"]*"`,
		`<img[^>]*alt="[^"]*"[^>]*src="(https://[^"]*\.cdninstagram\.com/[^"]*\.jpg[^"]*)"`,
		// Carousel images
		`img[^>]*src="(https://scontent[^"]*\.cdninstagram\.com[^"]*\.jpg[^"]*)"`,
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

		mediaInfo := &MediaInfo{
			Type:     "photo",
			URL:      mediaURL,
			Caption:  ube.extractInstagramCaption(html),
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

		mediaInfo := &MediaInfo{
			Type:     "video",
			URL:      mediaURL,
			Caption:  ube.extractInstagramCaption(html),
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
	log.Printf("🐦 Extracting Twitter media with headless browser")

	html, err := ube.renderPageWithBrowser(mediaURL, 6*time.Second)
	if err != nil {
		return nil, err
	}

	var mediaInfos []*MediaInfo

	// Twitter image patterns
	imagePatterns := []string{
		`<img[^>]*src="(https://pbs\.twimg\.com/media/[^"]*\.jpg[^"]*)"`,
		`<img[^>]*src="(https://pbs\.twimg\.com/media/[^"]*\.png[^"]*)"`,
		`<img[^>]*src="(https://pbs\.twimg\.com/media/[^"]*\.webp[^"]*)"`,
	}

	imageURLs := ube.extractURLsWithPatterns(html, imagePatterns)
	log.Printf("🖼️ Found %d Twitter images", len(imageURLs))

	// Download images
	for i, imageURL := range imageURLs {
		cleanURL := ube.cleanTwitterURL(imageURL)
		filename, err := ube.downloadMediaFile(cleanURL, "twitter", "photo", i)
		if err != nil {
			log.Printf("❌ Failed to download Twitter image %d: %v", i+1, err)
			continue
		}

		mediaInfo := &MediaInfo{
			Type:     "photo",
			URL:      mediaURL,
			Caption:  ube.extractTwitterCaption(html),
			Filename: filename,
		}
		mediaInfos = append(mediaInfos, mediaInfo)
	}

	// Twitter video patterns
	videoPatterns := []string{
		`<video[^>]*src="(https://video\.twimg\.com/[^"]*\.mp4[^"]*)"`,
		`<source[^>]*src="(https://video\.twimg\.com/[^"]*\.mp4[^"]*)"`,
	}

	videoURLs := ube.extractURLsWithPatterns(html, videoPatterns)
	log.Printf("🎥 Found %d Twitter videos", len(videoURLs))

	// Download videos
	for i, videoURL := range videoURLs {
		cleanURL := ube.cleanTwitterURL(videoURL)
		filename, err := ube.downloadMediaFile(cleanURL, "twitter", "video", i)
		if err != nil {
			log.Printf("❌ Failed to download Twitter video %d: %v", i+1, err)
			continue
		}

		mediaInfo := &MediaInfo{
			Type:     "video",
			URL:      mediaURL,
			Caption:  ube.extractTwitterCaption(html),
			Filename: filename,
		}
		mediaInfos = append(mediaInfos, mediaInfo)
	}

	// If no media found, return text content if available
	if len(mediaInfos) == 0 {
		if tweetText := ube.extractTwitterCaption(html); tweetText != "" {
			mediaInfo := &MediaInfo{
				Type:     "text",
				URL:      mediaURL,
				Caption:  tweetText,
				Filename: "",
			}
			mediaInfos = append(mediaInfos, mediaInfo)
		}
	}

	if len(mediaInfos) == 0 {
		return nil, fmt.Errorf("no Twitter media found")
	}

	return mediaInfos, nil
}

func (ube *UniversalBrowserExtractor) extractTikTokMedia(mediaURL string) ([]*MediaInfo, error) {
	log.Printf("🎵 Extracting TikTok media with headless browser")

	html, err := ube.renderPageWithBrowser(mediaURL, 8*time.Second)
	if err != nil {
		return nil, err
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

		mediaInfo := &MediaInfo{
			Type:     "photo",
			URL:      mediaURL,
			Caption:  ube.extractTikTokCaption(html),
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

			mediaInfo := &MediaInfo{
				Type:     "video",
				URL:      mediaURL,
				Caption:  ube.extractTikTokCaption(html),
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

func (ube *UniversalBrowserExtractor) extractYouTubeMedia(mediaURL string) ([]*MediaInfo, error) {
	log.Printf("📺 Extracting YouTube media with headless browser")

	html, err := ube.renderPageWithBrowser(mediaURL, 6*time.Second)
	if err != nil {
		return nil, err
	}

	var mediaInfos []*MediaInfo

	// YouTube Shorts video patterns (usually streamed, harder to extract directly)
	// For now, focus on thumbnails and metadata
	thumbnailPatterns := []string{
		`<meta[^>]*property="og:image"[^>]*content="(https://i\.ytimg\.com/vi/[^"]*\.jpg)"`,
		`<link[^>]*rel="image_src"[^>]*href="(https://i\.ytimg\.com/vi/[^"]*\.jpg)"`,
	}

	thumbnailURLs := ube.extractURLsWithPatterns(html, thumbnailPatterns)
	log.Printf("🖼️ Found %d YouTube thumbnails", len(thumbnailURLs))

	// Download thumbnails as a fallback
	for i, thumbnailURL := range thumbnailURLs {
		// Upgrade to maxresdefault if possible
		highResURL := strings.Replace(thumbnailURL, "hqdefault.jpg", "maxresdefault.jpg", 1)
		highResURL = strings.Replace(highResURL, "mqdefault.jpg", "maxresdefault.jpg", 1)
		
		filename, err := ube.downloadMediaFile(highResURL, "youtube", "photo", i)
		if err != nil {
			// Fallback to original quality
			filename, err = ube.downloadMediaFile(thumbnailURL, "youtube", "photo", i)
			if err != nil {
				log.Printf("❌ Failed to download YouTube thumbnail %d: %v", i+1, err)
				continue
			}
		}

		mediaInfo := &MediaInfo{
			Type:     "photo",
			URL:      mediaURL,
			Caption:  ube.extractYouTubeCaption(html),
			Filename: filename,
		}
		mediaInfos = append(mediaInfos, mediaInfo)
	}

	// If no media found, return text content if available
	if len(mediaInfos) == 0 {
		if description := ube.extractYouTubeCaption(html); description != "" {
			mediaInfo := &MediaInfo{
				Type:     "text",
				URL:      mediaURL,
				Caption:  description,
				Filename: "",
			}
			mediaInfos = append(mediaInfos, mediaInfo)
		}
	}

	if len(mediaInfos) == 0 {
		return nil, fmt.Errorf("no YouTube media found")
	}

	return mediaInfos, nil
}

func (ube *UniversalBrowserExtractor) extract9GAGMedia(mediaURL string) ([]*MediaInfo, error) {
	log.Printf("😂 Extracting 9GAG media with headless browser")

	html, err := ube.renderPageWithBrowser(mediaURL, 6*time.Second)
	if err != nil {
		return nil, err
	}

	var mediaInfos []*MediaInfo

	// 9GAG image patterns
	imagePatterns := []string{
		`<img[^>]*src="(https://img-9gag-fun\.9cache\.com/[^"]*\.jpg[^"]*)"`,
		`<img[^>]*src="(https://img-9gag-fun\.9cache\.com/[^"]*\.png[^"]*)"`,
		`<img[^>]*src="(https://img-9gag-fun\.9cache\.com/[^"]*\.webp[^"]*)"`,
		`<meta[^>]*property="og:image"[^>]*content="(https://img-9gag-fun\.9cache\.com/[^"]*\.(jpg|png|webp)[^"]*)"`,
	}

	imageURLs := ube.extractURLsWithPatterns(html, imagePatterns)
	log.Printf("🖼️ Found %d 9GAG images", len(imageURLs))

	// Download images
	for i, imageURL := range imageURLs {
		filename, err := ube.downloadMediaFile(imageURL, "9gag", "photo", i)
		if err != nil {
			log.Printf("❌ Failed to download 9GAG image %d: %v", i+1, err)
			continue
		}

		mediaInfo := &MediaInfo{
			Type:     "photo",
			URL:      mediaURL,
			Caption:  ube.extract9GAGCaption(html),
			Filename: filename,
		}
		mediaInfos = append(mediaInfos, mediaInfo)
	}

	// 9GAG video patterns (prioritize main video sources)
	videoPatterns := []string{
		// Primary video sources (avoid thumb/preview)
		`<video[^>]*class="[^"]*video-post[^"]*"[^>]*src="(https://img-9gag-fun\.9cache\.com/[^"]*\.mp4[^"]*)"`,
		`<source[^>]*type="video/mp4"[^>]*src="(https://img-9gag-fun\.9cache\.com/[^"]*\.mp4[^"]*)"`,
		`"videoUrl":"(https://[^"]*\.9cache\.com/[^"]*\.mp4[^"]*)"`,
		// Open Graph video meta tag
		`<meta[^>]*property="og:video"[^>]*content="(https://[^"]*\.9cache\.com/[^"]*\.mp4[^"]*)"`,
		// Fallback patterns
		`<video[^>]*src="(https://img-9gag-fun\.9cache\.com/[^"]*\.mp4[^"]*)"`,
		`<source[^>]*src="(https://img-9gag-fun\.9cache\.com/[^"]*\.mp4[^"]*)"`,
	}

	videoURLs := ube.extractURLsWithPatterns(html, videoPatterns)
	
	// Deduplicate 9GAG videos (often multiple qualities of same video)
	uniqueVideos := ube.deduplicate9GAGVideos(videoURLs)
	log.Printf("🎥 Found %d unique 9GAG videos (deduplicated from %d)", len(uniqueVideos), len(videoURLs))

	// Download videos
	for i, videoURL := range uniqueVideos {
		// Clean JSON escaped URLs
		cleanURL := strings.ReplaceAll(videoURL, "\\", "")
		filename, err := ube.downloadMediaFile(cleanURL, "9gag", "video", i)
		if err != nil {
			log.Printf("❌ Failed to download 9GAG video %d: %v", i+1, err)
			continue
		}

		mediaInfo := &MediaInfo{
			Type:     "video",
			URL:      mediaURL,
			Caption:  ube.extract9GAGCaption(html),
			Filename: filename,
		}
		mediaInfos = append(mediaInfos, mediaInfo)
	}

	if len(mediaInfos) == 0 {
		return nil, fmt.Errorf("no 9GAG media found")
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
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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

func (ube *UniversalBrowserExtractor) deduplicate9GAGVideos(videoURLs []string) []string {
	if len(videoURLs) <= 1 {
		return videoURLs
	}

	// Extract base video ID from 9GAG URLs and prefer highest quality
	seen := make(map[string]string) // videoID -> bestURL
	videoIDPattern := regexp.MustCompile(`([a-zA-Z0-9]+)\.mp4`)

	for _, videoURL := range videoURLs {
		videoID := ""
		if match := videoIDPattern.FindStringSubmatch(videoURL); len(match) > 1 {
			videoID = match[1]
		} else {
			videoID = videoURL // Use full URL as fallback
		}

		// If we haven't seen this video ID, or this URL seems better quality, use it
		if existingURL, exists := seen[videoID]; !exists {
			seen[videoID] = videoURL
		} else {
			// Prefer URLs with higher quality indicators (larger file paths, no "thumb" or "preview")
			if ube.is9GAGVideoHigherQuality(videoURL, existingURL) {
				seen[videoID] = videoURL
			}
		}
	}

	// Convert map back to slice
	var unique []string
	for _, url := range seen {
		unique = append(unique, url)
	}

	return unique
}

func (ube *UniversalBrowserExtractor) is9GAGVideoHigherQuality(newURL, existingURL string) bool {
	// Prefer URLs that don't contain quality indicators for lower quality
	lowQualityIndicators := []string{"thumb", "preview", "small", "mobile", "_s.mp4", "_m.mp4"}
	
	newURLLower := strings.ToLower(newURL)
	existingURLLower := strings.ToLower(existingURL)
	
	// Count low quality indicators in each URL
	newLowQualityCount := 0
	existingLowQualityCount := 0
	
	for _, indicator := range lowQualityIndicators {
		if strings.Contains(newURLLower, indicator) {
			newLowQualityCount++
		}
		if strings.Contains(existingURLLower, indicator) {
			existingLowQualityCount++
		}
	}
	
	// Prefer the URL with fewer low quality indicators
	if newLowQualityCount < existingLowQualityCount {
		return true
	} else if newLowQualityCount > existingLowQualityCount {
		return false
	}
	
	// If same number of indicators, prefer longer URL (often higher quality)
	return len(newURL) > len(existingURL)
}

// URL cleaning methods
func (ube *UniversalBrowserExtractor) cleanInstagramURL(rawURL string) string {
	// Remove Instagram URL parameters that might cause issues
	if u, err := url.Parse(rawURL); err == nil {
		q := u.Query()
		q.Del("_nc_ht")
		q.Del("_nc_cat")
		q.Del("_nc_ohc")
		u.RawQuery = q.Encode()
		return u.String()
	}
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

	return "Downloaded from Instagram"
}

func (ube *UniversalBrowserExtractor) extractTwitterCaption(html string) string {
	patterns := []string{
		`<meta[^>]*property="og:description"[^>]*content="([^"]*)"`,
		`<meta[^>]*name="description"[^>]*content="([^"]*)"`,
		`"text":"([^"]*)"`,
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		if match := re.FindStringSubmatch(html); len(match) > 1 {
			caption := strings.TrimSpace(match[1])
			if caption != "" && !strings.Contains(caption, "Twitter") && !strings.Contains(caption, "X.com") {
				return caption
			}
		}
	}

	return "Downloaded from Twitter"
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

	return "Downloaded from TikTok"
}

func (ube *UniversalBrowserExtractor) extractYouTubeCaption(html string) string {
	patterns := []string{
		`<meta[^>]*property="og:title"[^>]*content="([^"]*)"`,
		`<meta[^>]*property="og:description"[^>]*content="([^"]*)"`,
		`<title>([^<]*)</title>`,
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		if match := re.FindStringSubmatch(html); len(match) > 1 {
			caption := strings.TrimSpace(match[1])
			if caption != "" && !strings.Contains(caption, "YouTube") {
				return caption
			}
		}
	}

	return "Downloaded from YouTube"
}

func (ube *UniversalBrowserExtractor) extract9GAGCaption(html string) string {
	patterns := []string{
		`<meta[^>]*property="og:title"[^>]*content="([^"]*)"`,
		`<meta[^>]*property="og:description"[^>]*content="([^"]*)"`,
		`<h1[^>]*>([^<]*)</h1>`,
		`<title>([^<]*)</title>`,
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		if match := re.FindStringSubmatch(html); len(match) > 1 {
			caption := strings.TrimSpace(match[1])
			if caption != "" && !strings.Contains(caption, "9GAG") {
				return caption
			}
		}
	}

	return "Downloaded from 9GAG"
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

	return "Downloaded from social media"
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

	// Create HTTP client with timeout
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	// Create request with proper headers
	req, err := http.NewRequest("GET", mediaURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %v", err)
	}

	// Add platform-specific headers
	ube.addPlatformHeaders(req, platform)

	// Download the file
	resp, err := client.Do(req)
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

	case "youtube":
		req.Header.Set("Referer", "https://www.youtube.com/")

	case "9gag":
		req.Header.Set("Referer", "https://9gag.com/")
		req.Header.Set("sec-fetch-dest", "image")
		req.Header.Set("sec-fetch-mode", "no-cors")
		req.Header.Set("sec-fetch-site", "cross-site")
	}
}