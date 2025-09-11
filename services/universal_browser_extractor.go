package services

import (
	"compress/gzip"
	"context"
	"crypto/md5"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
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
	case "reddit":
		return ube.extractRedditMedia(mediaURL)
	case "facebook":
		return ube.extractFacebookMedia(mediaURL)
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
	} else if strings.Contains(mediaURL, "reddit.com") {
		return "reddit"
	} else if strings.Contains(mediaURL, "facebook.com") {
		return "facebook"
	} else if strings.Contains(mediaURL, "9gag.com") {
		return "9gag"
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
	log.Printf("🐦 Extracting Twitter media with headless browser (true browser extraction)")
	
	// First try actual browser extraction for images
	html, err := ube.renderPageWithBrowser(mediaURL, 10*time.Second)
	if err != nil {
		log.Printf("❌ Browser rendering failed, falling back to custom extractor: %v", err)
		// Fall back to custom Twitter extractor
		twitterExtractor := NewTwitterExtractor(ube.workingDir, ube.tempDir)
		return twitterExtractor.ExtractTweet(mediaURL)
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
		
		// Extract caption
		caption := ube.extractTwitterCaption(html)
		
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
	
	// If browser extraction didn't find images, fall back to custom extractor
	log.Printf("📋 No images found via browser, falling back to custom Twitter extractor")
	twitterExtractor := NewTwitterExtractor(ube.workingDir, ube.tempDir)
	return twitterExtractor.ExtractTweet(mediaURL)
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

func (ube *UniversalBrowserExtractor) extractYouTubeMedia(mediaURL string) ([]*MediaInfo, error) {
	log.Printf("📺 Extracting YouTube media with headless browser")

	// Extract username from URL
	username := ExtractYouTubeChannel(mediaURL)
	
	// Check if this is a community post and transform URL if needed
	isYouTubePost := strings.Contains(mediaURL, "/post/")
	if isYouTubePost {
		// Ensure https protocol for community posts
		if strings.HasPrefix(mediaURL, "http://") {
			mediaURL = "https" + mediaURL[4:]
			log.Printf("📺 Transformed HTTP to HTTPS: %s", mediaURL)
		}
	}

	// Use much longer timeout for YouTube community posts as they load heavy JavaScript
	timeout := 6 * time.Second
	if isYouTubePost {
		timeout = 60 * time.Second
		log.Printf("📺 Using extended 60-second timeout for YouTube community post")
		
		// Use specialized YouTube community post extraction
		// Author info will be extracted inside the specialized function
		return ube.extractYouTubeCommunityPost(mediaURL, username, "")
	}

	html, err := ube.renderPageWithBrowser(mediaURL, timeout)
	if err != nil {
		return nil, err
	}

	// Extract YouTube author info from HTML
	displayName, detectedUsername := ube.extractYouTubeAuthorInfo(html)
	if detectedUsername != "" {
		username = detectedUsername
	}

	var mediaInfos []*MediaInfo

	// Already determined isYouTubePost above
	
	var mediaPatterns []string
	if isYouTubePost {
		// YouTube Community Post patterns (images, polls, text)
		mediaPatterns = []string{
			// Meta tag images (primary)
			`<meta[^>]*property="og:image"[^>]*content="(https://yt[0-9]\.ggpht\.com/[^"]*)"`,
			`<meta[^>]*property="og:image"[^>]*content="(https://i\.ytimg\.com/[^"]*)"`,
			`<meta[^>]*name="twitter:image"[^>]*content="(https://yt[0-9]\.ggpht\.com/[^"]*)"`,
			`<meta[^>]*name="twitter:image"[^>]*content="(https://i\.ytimg\.com/[^"]*)"`,
			// JSON data (most reliable for actual content)
			`"url":"(https://yt[0-9]\.ggpht\.com/[^"]*)"`,
			`"url":"(https://i\.ytimg\.com/[^"]*\.jpg[^"]*)"`,
			`"thumbnail":"(https://yt[0-9]\.ggpht\.com/[^"]*)"`,
			`"image":"(https://yt[0-9]\.ggpht\.com/[^"]*)"`,
			// IMG tags in rendered HTML
			`<img[^>]*src="(https://yt[0-9]\.ggpht\.com/[^"]*)"`,
			`<img[^>]*src="(https://i\.ytimg\.com/[^"]*\.jpg[^"]*)"`,
			// Background images
			`background-image:\s*url\("(https://yt[0-9]\.ggpht\.com/[^"]*)"`,
			`background-image:\s*url\("(https://i\.ytimg\.com/[^"]*\.jpg[^"]*)"`,
		}
		log.Printf("📺 Detected YouTube Community Post")
	} else {
		// YouTube Shorts/Video patterns (usually streamed, harder to extract directly)
		// For now, focus on thumbnails and metadata
		mediaPatterns = []string{
			`<meta[^>]*property="og:image"[^>]*content="(https://i\.ytimg\.com/vi/[^"]*\.jpg)"`,
			`<link[^>]*rel="image_src"[^>]*href="(https://i\.ytimg\.com/vi/[^"]*\.jpg)"`,
		}
		log.Printf("📺 Detected YouTube Shorts/Video")
	}

	mediaURLs := ube.extractURLsWithPatterns(html, mediaPatterns)
	if isYouTubePost {
		log.Printf("🖼️ Found %d YouTube post media items", len(mediaURLs))
	} else {
		log.Printf("🖼️ Found %d YouTube thumbnails", len(mediaURLs))
	}

	// Download media (thumbnails or community post images)
	for i, mediaItemURL := range mediaURLs {
		var filename string
		var err error
		
		if isYouTubePost {
			// For community posts, download directly
			filename, err = ube.downloadMediaFile(mediaItemURL, "youtube", "photo", i)
		} else {
			// For Shorts/Videos, try to upgrade thumbnail quality
			highResURL := strings.Replace(mediaItemURL, "hqdefault.jpg", "maxresdefault.jpg", 1)
			highResURL = strings.Replace(highResURL, "mqdefault.jpg", "maxresdefault.jpg", 1)
			
			filename, err = ube.downloadMediaFile(highResURL, "youtube", "photo", i)
			if err != nil {
				// Fallback to original quality
				filename, err = ube.downloadMediaFile(mediaItemURL, "youtube", "photo", i)
			}
		}
		
		if err != nil {
			log.Printf("❌ Failed to download YouTube media %d: %v", i+1, err)
			continue
		}

		originalCaption := ube.extractYouTubeCaption(html)
		mediaInfo := &MediaInfo{
			Type:     "photo",
			URL:      mediaURL,
			Caption:  FormatAuthorCaption("youtube", displayName, username, originalCaption),
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
				Caption:  FormatAuthorCaption("youtube", displayName, username, description),
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

	// 9GAG posts don't include username in URL
	username := ""

	// Try the simple HTTP approach first since it used to work
	html, err := ube.tryImproved9GAGFallback(mediaURL)
	if err != nil {
		log.Printf("⚠️ HTTP extraction failed, trying browser: %v", err)
		// Fallback to browser if HTTP fails
		html, err = ube.renderPageWithBrowserFor9GAG(mediaURL, 8*time.Second)
		if err != nil {
			return nil, fmt.Errorf("both HTTP and browser extraction failed: %v", err)
		}
		log.Printf("✅ 9GAG browser fallback succeeded, HTML length: %d", len(html))
	} else {
		log.Printf("✅ 9GAG HTTP extraction succeeded, HTML length: %d", len(html))
	}
	
	log.Printf("📄 Retrieved HTML content from 9GAG, length: %d characters", len(html))

	// Check if this is age-restricted content
	if ube.is9GAGAgeRestricted(html) {
		log.Printf("🔞 Detected age-restricted 9GAG content, trying authenticated access")
		html, err = ube.render9GAGWithAuthentication(mediaURL)
		if err != nil {
			return nil, fmt.Errorf("failed to access age-restricted content: %v", err)
		}
	}

	// Extract 9GAG author info from HTML
	displayName, detectedUsername := ube.extract9GAGAuthorInfo(html)
	if detectedUsername != "" {
		username = detectedUsername
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

		originalCaption := ube.extract9GAGCaption(html)
		mediaInfo := &MediaInfo{
			Type:     "photo",
			URL:      mediaURL,
			Caption:  FormatAuthorCaption("9gag", displayName, username, originalCaption),
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

		originalCaption := ube.extract9GAGCaption(html)
		mediaInfo := &MediaInfo{
			Type:     "video",
			URL:      mediaURL,
			Caption:  FormatAuthorCaption("9gag", displayName, username, originalCaption),
			Filename: filename,
		}
		mediaInfos = append(mediaInfos, mediaInfo)
	}

	if len(mediaInfos) == 0 {
		return nil, fmt.Errorf("no 9GAG media found")
	}

	return mediaInfos, nil
}

func (ube *UniversalBrowserExtractor) tryImproved9GAGFallback(pageURL string) (string, error) {
	log.Printf("🌐 Trying improved 9GAG HTTP fallback")
	
	client := &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return nil // Follow redirects
		},
	}
	
	req, err := http.NewRequest("GET", pageURL, nil)
	if err != nil {
		return "", err
	}
	
	// Set headers to better mimic a real browser session
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Pragma", "no-cache")
	req.Header.Set("Sec-Ch-Ua", `"Google Chrome";v="131", "Chromium";v="131", "Not_A Brand";v="24"`)
	req.Header.Set("Sec-Ch-Ua-Mobile", "?0")
	req.Header.Set("Sec-Ch-Ua-Platform", `"Linux"`)
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "none")
	req.Header.Set("Sec-Fetch-User", "?1")
	req.Header.Set("Upgrade-Insecure-Requests", "1")
	req.Header.Set("Referer", "https://9gag.com/")
	
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}
	
	var reader io.Reader = resp.Body
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gzipReader, err := gzip.NewReader(resp.Body)
		if err != nil {
			return "", err
		}
		defer gzipReader.Close()
		reader = gzipReader
	}
	
	bodyBytes, err := io.ReadAll(reader)
	if err != nil {
		return "", err
	}
	
	return string(bodyBytes), nil
}

// Keep old method as backup
func (ube *UniversalBrowserExtractor) trySimple9GAGFallback(pageURL string) (string, error) {
	log.Printf("🌐 Trying 9GAG HTTP fallback")
	
	client := &http.Client{
		Timeout: 10 * time.Second,
	}
	
	req, err := http.NewRequest("GET", pageURL, nil)
	if err != nil {
		return "", err
	}
	
	// Set headers to mimic a regular browser
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/121.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")
	req.Header.Set("Accept-Encoding", "gzip, deflate")
	req.Header.Set("DNT", "1")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Upgrade-Insecure-Requests", "1")
	
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}
	
	var reader io.Reader = resp.Body
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gzipReader, err := gzip.NewReader(resp.Body)
		if err != nil {
			return "", err
		}
		defer gzipReader.Close()
		reader = gzipReader
	}
	
	bodyBytes, err := io.ReadAll(reader)
	if err != nil {
		return "", err
	}
	
	return string(bodyBytes), nil
}

func (ube *UniversalBrowserExtractor) tryRedditHTTPExtraction(pageURL string) (string, error) {
	log.Printf("🌐 Trying Reddit HTTP extraction")
	
	client := &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return nil // Follow redirects
		},
	}
	
	req, err := http.NewRequest("GET", pageURL, nil)
	if err != nil {
		return "", err
	}
	
	// Set headers to mimic a real browser session
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Pragma", "no-cache")
	req.Header.Set("Sec-Ch-Ua", `"Google Chrome";v="131", "Chromium";v="131", "Not_A Brand";v="24"`)
	req.Header.Set("Sec-Ch-Ua-Mobile", "?0")
	req.Header.Set("Sec-Ch-Ua-Platform", `"Linux"`)
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "none")
	req.Header.Set("Sec-Fetch-User", "?1")
	req.Header.Set("Upgrade-Insecure-Requests", "1")
	req.Header.Set("Referer", "https://www.reddit.com/")
	
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}
	
	var reader io.Reader = resp.Body
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gzipReader, err := gzip.NewReader(resp.Body)
		if err != nil {
			return "", err
		}
		defer gzipReader.Close()
		reader = gzipReader
	}
	
	bodyBytes, err := io.ReadAll(reader)
	if err != nil {
		return "", err
	}
	
	return string(bodyBytes), nil
}

func (ube *UniversalBrowserExtractor) extractRedditMedia(mediaURL string) ([]*MediaInfo, error) {
	log.Printf("🟧 Extracting Reddit media with HTTP fallback first")

	// Extract username from URL
	username := ExtractRedditUsername(mediaURL)

	// Try simple HTTP request first for text posts (faster and more reliable)
	html, err := ube.tryRedditHTTPExtraction(mediaURL)
	if err != nil {
		log.Printf("❌ Reddit HTTP extraction failed: %v, trying browser with reduced timeout", err)
		// Fallback to browser extraction with reduced timeout for text posts
		log.Printf("🟧 Trying headless browser as fallback (reduced timeout for text posts)")
		html, err = ube.renderPageWithBrowserTimeout(mediaURL, 5*time.Second, 30*time.Second)
		if err != nil {
			log.Printf("❌ Browser extraction also failed: %v", err)
			return nil, err
		}
	}

	// Extract Reddit author info from HTML
	displayName, detectedUsername := ube.extractRedditAuthorInfo(html)
	if detectedUsername != "" {
		username = detectedUsername
	}

	var mediaInfos []*MediaInfo

	// Reddit-specific patterns for images and videos
	imagePatterns := []string{
		`<meta[^>]*property="og:image"[^>]*content="([^"]*\.(jpg|jpeg|png|webp|gif)[^"]*)"`,
		`<meta[^>]*name="twitter:image"[^>]*content="([^"]*\.(jpg|jpeg|png|webp|gif)[^"]*)"`,
		`"url":"([^"]*i\.redd\.it[^"]*\.(jpg|jpeg|png|webp|gif)[^"]*)"`,
		`"preview":.*?"url":"([^"]*preview\.redd\.it[^"]*\.(jpg|jpeg|png|webp|gif)[^"]*)"`,
	}

	videoPatterns := []string{
		`<meta[^>]*property="og:video"[^>]*content="([^"]*\.(mp4|webm|mov)[^"]*)"`,
		`"fallback_url":"([^"]*v\.redd\.it[^"]*)"`,
		`"dash_url":"([^"]*)"`,
	}

	// Extract images
	imageURLs := ube.extractURLsWithPatterns(html, imagePatterns)
	log.Printf("🖼️ Found %d Reddit images", len(imageURLs))

	for i, imageURL := range imageURLs {
		// Clean up Reddit URLs
		cleanURL := strings.ReplaceAll(imageURL, "&amp;", "&")
		if filename, err := ube.downloadMediaFile(cleanURL, "reddit", "photo", i); err == nil {
			originalCaption := ube.extractRedditCaption(html)
			mediaInfo := &MediaInfo{
				Type:     "photo",
				URL:      mediaURL,
				Caption:  FormatAuthorCaption("reddit", displayName, username, originalCaption),
				Filename: filename,
			}
			mediaInfos = append(mediaInfos, mediaInfo)
		}
	}

	// Extract videos  
	videoURLs := ube.extractURLsWithPatterns(html, videoPatterns)
	log.Printf("🎬 Found %d Reddit videos", len(videoURLs))

	for i, videoURL := range videoURLs {
		cleanURL := strings.ReplaceAll(videoURL, "&amp;", "&")
		if filename, err := ube.downloadMediaFile(cleanURL, "reddit", "video", i); err == nil {
			originalCaption := ube.extractRedditCaption(html)
			mediaInfo := &MediaInfo{
				Type:     "video",
				URL:      mediaURL,
				Caption:  FormatAuthorCaption("reddit", displayName, username, originalCaption),
				Filename: filename,
			}
			mediaInfos = append(mediaInfos, mediaInfo)
		}
	}

	// If no media found, check if this is a text post
	if len(mediaInfos) == 0 {
		textContent := ube.extractRedditTextPost(html)
		if textContent != "" {
			log.Printf("📝 Found Reddit text post content")
			
			// Create a text-only MediaInfo for Reddit text post
			mediaInfo := &MediaInfo{
				Type:     "text",
				URL:      mediaURL,
				Caption:  FormatAuthorCaption("reddit", displayName, username, textContent),
				Filename: "", // No file for text posts
			}
			mediaInfos = append(mediaInfos, mediaInfo)
		}
	}

	log.Printf("✅ Reddit extraction completed: %d media items", len(mediaInfos))
	return mediaInfos, nil
}

func (ube *UniversalBrowserExtractor) extractFacebookMedia(mediaURL string) ([]*MediaInfo, error) {
	log.Printf("📘 Extracting Facebook media with headless browser (extended timeout)")

	// Extract username from URL
	username := ExtractFacebookUsername(mediaURL)

	// Use longer timeout for Facebook due to heavy JavaScript loading
	html, err := ube.renderPageWithBrowserTimeout(mediaURL, 10*time.Second, 60*time.Second)
	if err != nil {
		return nil, err
	}

	// Extract Facebook author info from HTML
	displayName, detectedUsername := ube.extractFacebookAuthorInfo(html)
	if detectedUsername != "" {
		username = detectedUsername
	}

	var mediaInfos []*MediaInfo

	// Facebook-specific patterns for images and videos
	imagePatterns := []string{
		`<meta[^>]*property="og:image"[^>]*content="([^"]*\.(jpg|jpeg|png|webp)[^"]*)"`,
		`<meta[^>]*name="twitter:image"[^>]*content="([^"]*\.(jpg|jpeg|png|webp)[^"]*)"`,
		`"image":.*?"uri":"([^"]*scontent[^"]*\.(jpg|jpeg|png|webp)[^"]*)"`,
		`"photo_image":.*?"uri":"([^"]*)"`,
	}

	videoPatterns := []string{
		`<meta[^>]*property="og:video"[^>]*content="([^"]*\.(mp4|webm)[^"]*)"`,
		`"video_url":"([^"]*)"`,
		`"src":"([^"]*video[^"]*\.(mp4|webm)[^"]*)"`,
	}

	// Extract images
	imageURLs := ube.extractURLsWithPatterns(html, imagePatterns)
	log.Printf("🖼️ Found %d Facebook images", len(imageURLs))

	for i, imageURL := range imageURLs {
		// Clean up Facebook URLs
		cleanURL := strings.ReplaceAll(imageURL, "&amp;", "&")
		if filename, err := ube.downloadMediaFile(cleanURL, "facebook", "photo", i); err == nil {
			originalCaption := ube.extractFacebookCaption(html)
			mediaInfo := &MediaInfo{
				Type:     "photo",
				URL:      mediaURL,
				Caption:  FormatAuthorCaption("facebook", displayName, username, originalCaption),
				Filename: filename,
			}
			mediaInfos = append(mediaInfos, mediaInfo)
		}
	}

	// Extract videos
	videoURLs := ube.extractURLsWithPatterns(html, videoPatterns)
	log.Printf("🎬 Found %d Facebook videos", len(videoURLs))

	for i, videoURL := range videoURLs {
		cleanURL := strings.ReplaceAll(videoURL, "&amp;", "&")
		if filename, err := ube.downloadMediaFile(cleanURL, "facebook", "video", i); err == nil {
			originalCaption := ube.extractFacebookCaption(html)
			mediaInfo := &MediaInfo{
				Type:     "video",
				URL:      mediaURL,
				Caption:  FormatAuthorCaption("facebook", displayName, username, originalCaption),
				Filename: filename,
			}
			mediaInfos = append(mediaInfos, mediaInfo)
		}
	}

	log.Printf("✅ Facebook extraction completed: %d media items", len(mediaInfos))
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

	return ""
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

	return ""
}

func (ube *UniversalBrowserExtractor) extractRedditCaption(html string) string {
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
			if caption != "" && !strings.Contains(caption, "reddit") && !strings.Contains(caption, "Reddit") {
				return caption
			}
		}
	}

	return ""
}

func (ube *UniversalBrowserExtractor) extractRedditTextPost(html string) string {
	// Extract Reddit text post content from various HTML structures
	patterns := []string{
		// Full content extraction with multiple paragraphs (most specific first)
		`<div[^>]*class="md text-14-scalable"[^>]*style="--emote-size: 20px">(.+?)</div>`,
		`<div[^>]*class="md text-14-scalable"[^>]*>(.+?)</div>`,
		`<div[^>]*data-post-click-location="text-body"[^>]*>(.+?)</div>`,
		
		// Modern Reddit patterns
		`<div[^>]*data-testid="post-content"[^>]*>(.+?)</div>`,
		`<div[^>]*class="[^"]*usertext-body[^"]*"[^>]*>(.+?)</div>`,
		`<div[^>]*class="[^"]*RichTextJSON-root[^"]*"[^>]*>(.+?)</div>`,
		
		// Old Reddit patterns
		`<div[^>]*class="[^"]*md[^"]*"[^>]*>(.+?)</div>`,
		
		// Meta tag patterns (for HTTP simple responses)
		`<meta[^>]*property="og:description"[^>]*content="([^"]+)"[^>]*>`,
		`<meta[^>]*name="twitter:description"[^>]*content="([^"]+)"[^>]*>`,
		`<meta[^>]*property="og:title"[^>]*content="([^"]+)"[^>]*>`,
		
		// JSON structured data
		`"selftext":\s*"([^"]+)"`,
		`"text":\s*"([^"]+)"`,
		
		// Fallback: post title from title tag
		`<title>([^<]+)</title>`,
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindStringSubmatch(html)
		if len(matches) > 1 {
			content := strings.TrimSpace(matches[1])
			
			// Clean up content - extract text from paragraph tags
			if strings.Contains(content, "<p") {
				// Extract text from all paragraph tags and join with line breaks
				pRe := regexp.MustCompile(`<p[^>]*>(.*?)</p>`)
				pMatches := pRe.FindAllStringSubmatch(content, -1)
				var paragraphs []string
				for _, pMatch := range pMatches {
					if len(pMatch) > 1 {
						paragraph := strings.TrimSpace(pMatch[1])
						// Remove any remaining HTML tags
						paragraph = regexp.MustCompile(`<[^>]+>`).ReplaceAllString(paragraph, "")
						if paragraph != "" {
							paragraphs = append(paragraphs, paragraph)
						}
					}
				}
				if len(paragraphs) > 0 {
					content = strings.Join(paragraphs, "\n\n")
				}
			} else {
				// Remove any HTML tags if no paragraphs found
				content = regexp.MustCompile(`<[^>]+>`).ReplaceAllString(content, "")
			}
			
			// Basic text cleanup
			content = strings.ReplaceAll(content, "\\n", "\n")
			content = strings.ReplaceAll(content, "\\r", "\r") 
			content = strings.ReplaceAll(content, "\\t", "\t")
			content = strings.ReplaceAll(content, "\\/", "/")
			content = strings.ReplaceAll(content, "&amp;", "&")
			content = strings.ReplaceAll(content, "&lt;", "<")
			content = strings.ReplaceAll(content, "&gt;", ">")
			content = strings.ReplaceAll(content, "&quot;", "\"")
			
			// Filter out common non-content matches
			contentLower := strings.ToLower(content)
			if content != "" &&
				!strings.Contains(contentLower, "reddit") &&
				!strings.Contains(contentLower, "comments") &&
				!strings.Contains(contentLower, "upvotes") &&
				!strings.Contains(contentLower, "posted by") &&
				!strings.Contains(contentLower, "r/") &&
				!strings.HasPrefix(contentLower, "u/") &&
				len(content) > 15 { // Ensure it's substantial content
				return content
			}
		}
	}

	return ""
}

func (ube *UniversalBrowserExtractor) extractFacebookCaption(html string) string {
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
			if caption != "" && !strings.Contains(caption, "facebook") && !strings.Contains(caption, "Facebook") {
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

	case "youtube":
		req.Header.Set("Referer", "https://www.youtube.com/")

	case "9gag":
		req.Header.Set("Referer", "https://9gag.com/")
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

// Specialized 9GAG browser functions for age-restricted content
func (ube *UniversalBrowserExtractor) renderPageWithBrowserFor9GAG(pageURL string, waitTime time.Duration) (string, error) {
	// Give 9GAG more time to resolve - it might just be slow
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	
	log.Printf("🌐 Starting 9GAG extraction for: %s", pageURL)

	// Find Chrome executable
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

	// Enhanced Chrome options for 9GAG with stealth and cookie support
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.DisableGPU,
		chromedp.NoSandbox,
		chromedp.Headless,
		// Use recent user agent that signals 18+ content capability - rotate between different ones
		chromedp.UserAgent(ube.getRandomUserAgent()),
		chromedp.WindowSize(1920, 1080), // Set realistic window size
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
		// Enhanced stealth options to avoid detection
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("no-zygote", true),
		chromedp.Flag("disable-component-extensions-with-background-pages", true),
		chromedp.Flag("disable-features", "VizDisplayCompositor,TranslateUI"),
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
		// Accept language for international content
		chromedp.Flag("accept-language", "en-US,en;q=0.9"),
	)

	// Add proxy support if configured via environment variables
	proxyServer := os.Getenv("PROXY_SERVER") // e.g., "127.0.0.1:8080" or "proxy.example.com:3128"
	if proxyServer != "" {
		log.Printf("🌐 Using proxy server for 9GAG: %s", proxyServer)
		opts = append(opts, chromedp.ProxyServer(proxyServer))
	} else {
		// Try Tor if it's running on default port
		if ube.isTorRunning() {
			log.Printf("🧅 Using Tor proxy for 9GAG: 127.0.0.1:9050")
			opts = append(opts, chromedp.ProxyServer("socks5://127.0.0.1:9050"))
		} else {
			// Fallback: try rotating through some public proxies for 9GAG
			freeProxies := []string{
				// You can add working free proxies here
				// "proxy1.example.com:8080",
				// "proxy2.example.com:3128",
			}
			if len(freeProxies) > 0 {
				// Use first available proxy as fallback
				log.Printf("🌐 Trying fallback proxy for 9GAG: %s", freeProxies[0])
				// opts = append(opts, chromedp.ProxyServer(freeProxies[0]))
			}
		}
	}

	if execPath != "" {
		log.Printf("🔍 Using Chrome at: %s for 9GAG", execPath)
		opts = append(opts, chromedp.ExecPath(execPath))
	}

	allocCtx, allocCancel := chromedp.NewExecAllocator(ctx, opts...)
	defer allocCancel()

	browserCtx, browserCancel := chromedp.NewContext(allocCtx, chromedp.WithLogf(func(string, ...interface{}) {}))
	defer browserCancel()

	var htmlContent string
	
	// Try a more patient approach - give 9GAG time to load
	err := chromedp.Run(browserCtx,
		// Navigate to 9GAG domain first to set cookies
		chromedp.Navigate("https://9gag.com"),
		chromedp.Sleep(2*time.Second),
		// Inject stealth scripts to avoid detection
		ube.injectStealthScripts(),
		// Set cookies for age verification
		ube.set9GAGCookies(),
		chromedp.Sleep(1*time.Second),
		// Now navigate to the actual page
		chromedp.Navigate(pageURL),
		chromedp.Sleep(12*time.Second), // Give it even more time to load
		// Inject stealth scripts again on target page
		ube.injectStealthScripts(),
		// Try to handle age confirmation popup if present
		ube.handle9GAGAgeConfirmation(),
	)
	
	if err != nil {
		log.Printf("⚠️ 9GAG navigation failed: %v, trying to get HTML anyway", err)
	}
	
	// Try to get HTML content even if navigation partially failed
	err = chromedp.Run(browserCtx,
		chromedp.InnerHTML(`html`, &htmlContent, chromedp.ByQuery),
	)
	
	// If that fails, try getting document content directly
	if err != nil || len(htmlContent) < 1000 {
		log.Printf("⚠️ Failed to get HTML via innerHTML, trying document.documentElement.outerHTML")
		var docHTML string
		err2 := chromedp.Run(browserCtx,
			chromedp.Evaluate(`document.documentElement.outerHTML`, &docHTML),
		)
		if err2 == nil && len(docHTML) > len(htmlContent) {
			htmlContent = docHTML
			err = nil
		}
	}

	if err != nil {
		return "", fmt.Errorf("9GAG browser navigation failed: %v", err)
	}

	log.Printf("🌐 Retrieved 9GAG HTML content, length: %d", len(htmlContent))
	return htmlContent, nil
}

func (ube *UniversalBrowserExtractor) render9GAGWithAuthentication(pageURL string) (string, error) {
	log.Printf("🔐 Attempting authenticated 9GAG access with age verification")
	
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

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

	// Create persistent user data directory for authentication
	userDataDir := filepath.Join(ube.tempDir, "chrome-9gag-profile")
	os.MkdirAll(userDataDir, 0755)

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.DisableGPU,
		chromedp.NoSandbox,
		chromedp.Headless,
		chromedp.UserAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/121.0.0.0 Safari/537.36"),
		chromedp.Flag("user-data-dir", userDataDir),
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
		chromedp.Flag("accept-language", "en-US,en;q=0.9"),
	)

	if execPath != "" {
		opts = append(opts, chromedp.ExecPath(execPath))
	}

	allocCtx, allocCancel := chromedp.NewExecAllocator(ctx, opts...)
	defer allocCancel()

	browserCtx, browserCancel := chromedp.NewContext(allocCtx, chromedp.WithLogf(func(string, ...interface{}) {}))
	defer browserCancel()

	var htmlContent string
	err := chromedp.Run(browserCtx,
		// First, visit 9GAG main page to establish session
		chromedp.Navigate("https://9gag.com"),
		chromedp.Sleep(2*time.Second),
		// Set age verification cookies
		ube.set9GAGAgeVerificationCookies(),
		chromedp.Sleep(1*time.Second),
		// Now navigate to the actual content
		chromedp.Navigate(pageURL),
		chromedp.Sleep(5*time.Second),
		// Handle any age confirmation dialogs
		ube.handle9GAGAgeConfirmation(),
		chromedp.WaitVisible(`body`, chromedp.ByQuery),
		chromedp.Sleep(5*time.Second), // Extra wait for age-restricted content
		chromedp.InnerHTML(`html`, &htmlContent, chromedp.ByQuery),
	)

	if err != nil {
		return "", fmt.Errorf("authenticated 9GAG navigation failed: %v", err)
	}

	log.Printf("🔐 Retrieved authenticated 9GAG HTML content, length: %d", len(htmlContent))
	return htmlContent, nil
}

func (ube *UniversalBrowserExtractor) is9GAGAgeRestricted(html string) bool {
	// Check for common age-restriction indicators in 9GAG HTML
	ageRestrictedIndicators := []string{
		"age-restriction",
		"age-gate",
		"mature-content",
		"nsfw-warning",
		"adult-content",
		"18+",
		"confirm your age",
		"are you 18",
		"verify age",
		"age verification",
		"content is not suitable",
		"mature audiences",
	}

	htmlLower := strings.ToLower(html)
	for _, indicator := range ageRestrictedIndicators {
		if strings.Contains(htmlLower, indicator) {
			return true
		}
	}

	// Check if the content is blocked or shows a warning page
	if strings.Contains(htmlLower, "blocked") && strings.Contains(htmlLower, "content") {
		return true
	}

	return false
}

func (ube *UniversalBrowserExtractor) set9GAGCookies() chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		// Check if 9GAG cookies file exists
		ninegagCookiesPath := filepath.Join(ube.workingDir, "9gag_cookies.txt")
		if _, err := os.Stat(ninegagCookiesPath); err == nil {
			log.Printf("🍪 Loading 9GAG cookies for age verification")
			// Note: In a real implementation, you'd parse the cookies file and set them
			// For now, we'll set basic age verification cookies programmatically
		}

		// Set basic age verification cookies
		return ube.set9GAGAgeVerificationCookies().Do(ctx)
	})
}

func (ube *UniversalBrowserExtractor) set9GAGAgeVerificationCookies() chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		// Set cookies using a more reliable method - all in one JavaScript call
		script := `
			document.cookie = "age_verified=1; domain=.9gag.com; path=/; max-age=86400";
			document.cookie = "mature_content=true; domain=.9gag.com; path=/; max-age=86400";
			document.cookie = "nsfw_ok=1; domain=.9gag.com; path=/; max-age=86400";
			document.cookie = "user_preference=mature_allowed; domain=.9gag.com; path=/; max-age=86400";
		`
		
		err := chromedp.Evaluate(script, nil).Do(ctx)
		if err != nil {
			log.Printf("⚠️ Failed to set 9GAG cookies: %v", err)
		} else {
			log.Printf("🍪 Set 9GAG age verification cookies")
		}
		
		return nil // Continue even if cookie setting fails
	})
}

func (ube *UniversalBrowserExtractor) handle9GAGAgeConfirmation() chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		// Try to find and click age confirmation buttons
		ageConfirmSelectors := []string{
			`button[data-testid="age-confirm"]`,
			`button:contains("I am 18 or older")`,
			`button:contains("Yes, I am 18+")`,
			`button:contains("Continue")`,
			`button:contains("I understand")`,
			`.age-gate-confirm`,
			`.mature-content-continue`,
			`#age-verification-continue`,
		}

		for _, selector := range ageConfirmSelectors {
			// Try to click the button if it exists (don't fail if it doesn't)
			err := chromedp.Click(selector, chromedp.ByQuery).Do(ctx)
			if err == nil {
				log.Printf("✅ Clicked age confirmation button: %s", selector)
				// Wait a bit after clicking
				chromedp.Sleep(2 * time.Second).Do(ctx)
				break
			}
		}

		return nil
	})
}

// injectStealthScripts injects JavaScript to make the browser less detectable
func (ube *UniversalBrowserExtractor) injectStealthScripts() chromedp.Action {
	stealthScript := `
		// Override webdriver detection
		Object.defineProperty(navigator, 'webdriver', {
			get: () => undefined,
		});

		// Override chrome detection
		window.chrome = {
			runtime: {},
		};

		// Override permission API
		const originalQuery = window.navigator.permissions.query;
		window.navigator.permissions.query = (parameters) => (
			parameters.name === 'notifications' ?
				Promise.resolve({ state: Notification.permission }) :
				originalQuery(parameters)
		);

		// Override plugins
		Object.defineProperty(navigator, 'plugins', {
			get: () => [1, 2, 3, 4, 5],
		});

		// Override languages
		Object.defineProperty(navigator, 'languages', {
			get: () => ['en-US', 'en'],
		});

		// Mock connection
		Object.defineProperty(navigator, 'connection', {
			get: () => ({ effectiveType: '4g', rtt: 100, downlink: 2 }),
		});

		// Override automation indicators
		delete window.navigator.__proto__.webdriver;
		console.log('🕵️ Stealth scripts injected successfully');
	`
	
	return chromedp.Evaluate(stealthScript, nil)
}

// getRandomUserAgent returns a random user agent to avoid detection
func (ube *UniversalBrowserExtractor) getRandomUserAgent() string {
	userAgents := []string{
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/121.0.0.0 Safari/537.36",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/121.0.0.0 Safari/537.36",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:122.0) Gecko/20100101 Firefox/122.0",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.2.1 Safari/605.1.15",
		"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/121.0.0.0 Safari/537.36",
	}
	
	// Use URL hash as seed for consistent randomness per URL
	
	hasher := md5.New()
	hasher.Write([]byte("9gag"))
	hash := hasher.Sum(nil)
	seed := binary.BigEndian.Uint64(hash[:8])
	
	return userAgents[seed % uint64(len(userAgents))]
}

// isTorRunning checks if Tor is running on the default SOCKS port
func (ube *UniversalBrowserExtractor) isTorRunning() bool {
	conn, err := net.DialTimeout("tcp", "127.0.0.1:9050", 2*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
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

// extractYouTubeAuthorInfo extracts channel name from YouTube HTML
func (ube *UniversalBrowserExtractor) extractYouTubeAuthorInfo(html string) (displayName, username string) {
	// YouTube channel patterns
	patterns := []string{
		// Meta tag patterns
		`<meta[^>]*name="twitter:title"[^>]*content="([^"]+)"`,
		`<meta[^>]*property="og:title"[^>]*content="([^"]+)"`,
		// Channel link patterns
		`"ownerChannelName":"([^"]+)"`,
		`"author":"([^"]+)"`,
	}
	
	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindStringSubmatch(html)
		
		if len(matches) >= 2 {
			channelName := strings.TrimSpace(matches[1])
			
			// Skip generic titles
			if !strings.Contains(channelName, "YouTube") && len(channelName) > 2 {
				log.Printf("✅ Extracted YouTube channel: %s", channelName)
				return channelName, ""
			}
		}
	}
	
	return "", ""
}

// extractRedditAuthorInfo extracts author/subreddit info from Reddit HTML
func (ube *UniversalBrowserExtractor) extractRedditAuthorInfo(html string) (displayName, username string) {
	// Reddit author patterns
	patterns := []string{
		// Post author patterns
		`"author":"([^"]+)"`,
		`data-author="([^"]+)"`,
		// Subreddit patterns
		`"subreddit":"([^"]+)"`,
		`/r/([^/"]+)`,
	}
	
	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindStringSubmatch(html)
		
		if len(matches) >= 2 {
			author := strings.TrimSpace(matches[1])
			
			if author != "" && author != "null" {
				if strings.Contains(pattern, "subreddit") || strings.Contains(pattern, "/r/") {
					log.Printf("✅ Extracted Reddit subreddit: r/%s", author)
					return "r/" + author, ""
				} else {
					log.Printf("✅ Extracted Reddit author: u/%s", author)
					return "", "u/" + author
				}
			}
		}
	}
	
	return "", ""
}

// extractFacebookAuthorInfo extracts author info from Facebook HTML
func (ube *UniversalBrowserExtractor) extractFacebookAuthorInfo(html string) (displayName, username string) {
	// Facebook author patterns - ordered by reliability
	patterns := []string{
		// Profile name in data-ad-rendering-role (most reliable)
		`data-ad-rendering-role="profile_name"[^>]*>.*?<strong[^>]*>.*?<span[^>]*>([^<]+)</span>.*?</strong>`,
		`data-ad-rendering-role="profile_name"[^>]*>.*?<a[^>]*>.*?<strong[^>]*>.*?<span[^>]*>([^<]+)</span>`,
		`data-ad-rendering-role="profile_name"[^>]*>.*?<h2[^>]*>.*?<span[^>]*>.*?<a[^>]*>.*?<strong[^>]*>.*?<span[^>]*>([^<]+)</span>`,
		// Aria-label patterns for page links (extract page name only, no username)
		`<a[^>]*aria-label="([^"]+)"[^>]*href="[^"]*facebook\.com/([^/?"]+)`,
		// Meta tag patterns  
		`<meta[^>]*property="og:title"[^>]*content="([^"]+)"`,
		`<meta[^>]*name="twitter:title"[^>]*content="([^"]+)"`,
		// JSON patterns
		`"name":"([^"]+)"`,
	}
	
	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindStringSubmatch(html)
		
		if len(matches) >= 2 {
			if len(matches) >= 3 && strings.Contains(pattern, "facebook.com") {
				displayName = strings.TrimSpace(matches[1])
				potentialUsername := strings.TrimSpace(matches[2])
				// Only use as username if it's not a special path
				if potentialUsername != "share" && potentialUsername != "reel" && potentialUsername != "stories" && 
				   potentialUsername != "pages" && potentialUsername != "groups" && potentialUsername != "watch" {
					username = potentialUsername
				}
			} else {
				displayName = strings.TrimSpace(matches[1])
			}
			
			// Clean up the display name
			displayName = strings.TrimSpace(displayName)
			displayName = regexp.MustCompile(`\s+`).ReplaceAllString(displayName, " ")
			
			// Additional filtering for Facebook
			if displayName != "" && !strings.Contains(displayName, "Facebook") && 
			   displayName != "..." && displayName != "share" && len(displayName) > 1 {
				// For Facebook, we typically only show display name (no username)
				log.Printf("✅ Extracted Facebook author: %s", displayName)
				return displayName, "" // Return empty username for Facebook
			}
		}
	}
	
	log.Printf("⚠️ No Facebook author found in HTML")
	return "", ""
}

// extract9GAGAuthorInfo extracts author info from 9GAG HTML
func (ube *UniversalBrowserExtractor) extract9GAGAuthorInfo(html string) (displayName, username string) {
	// 9GAG author patterns
	patterns := []string{
		// User patterns
		`"displayName":"([^"]+)".*?"username":"([^"]+)"`,
		`data-username="([^"]+)"`,
	}
	
	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindStringSubmatch(html)
		
		if len(matches) >= 2 {
			if len(matches) >= 3 {
				displayName = strings.TrimSpace(matches[1])
				username = strings.TrimSpace(matches[2])
			} else {
				username = strings.TrimSpace(matches[1])
			}
			
			if displayName != "" || username != "" {
				log.Printf("✅ Extracted 9GAG author: %s (@%s)", displayName, username)
				return displayName, username
			}
		}
	}
	
	return "", ""
}

// extractYouTubeCommunityPost handles YouTube community posts with specialized browser extraction
func (ube *UniversalBrowserExtractor) extractYouTubeCommunityPost(mediaURL, username, displayName string) ([]*MediaInfo, error) {
	log.Printf("📺 Starting specialized YouTube community post extraction")
	
	// Try enhanced browser extraction first
	mediaInfos, err := ube.extractYouTubeCommunityPostWithBrowser(mediaURL, username, displayName)
	if err == nil && len(mediaInfos) > 0 {
		log.Printf("✅ Enhanced browser extraction succeeded with %d items", len(mediaInfos))
		return mediaInfos, nil
	}
	
	log.Printf("⚠️ Enhanced browser extraction failed: %v", err)
	log.Printf("🔄 Trying fallback browser extraction...")
	
	// Try fallback browser extraction
	mediaInfos, err = ube.extractYouTubeCommunityPostFallback(mediaURL, username, displayName)
	if err == nil && len(mediaInfos) > 0 {
		log.Printf("✅ Fallback browser extraction succeeded with %d items", len(mediaInfos))
		return mediaInfos, nil
	}
	
	log.Printf("⚠️ Fallback browser extraction failed: %v", err)
	log.Printf("🔄 Trying API fallback...")
	
	// Try third-party API as last resort
	mediaInfos, err = ube.extractYouTubeCommunityPostViaAPI(mediaURL, username, displayName)
	if err == nil && len(mediaInfos) > 0 {
		log.Printf("✅ API fallback succeeded with %d items", len(mediaInfos))
		return mediaInfos, nil
	}
	
	log.Printf("❌ All YouTube community post extraction methods failed")
	return nil, fmt.Errorf("all extraction methods failed for YouTube community post")
}

// extractYouTubeCommunityPostWithBrowser uses enhanced Chrome with anti-bot evasion for YouTube community posts
func (ube *UniversalBrowserExtractor) extractYouTubeCommunityPostWithBrowser(mediaURL, username, displayName string) ([]*MediaInfo, error) {
	log.Printf("📺 Starting enhanced browser extraction for YouTube community post")
	
	// Create context with extended timeout
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	
	// Enhanced Chrome options for YouTube community posts
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", "new"), // Use new headless mode
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-extensions", true),
		chromedp.Flag("no-first-run", true),
		chromedp.Flag("disable-default-apps", true),
		chromedp.Flag("disable-blink-features", "AutomationControlled"), // Hide automation
		chromedp.Flag("virtual-time-budget", "45000"), // 45 seconds for full page load
		chromedp.Flag("run-all-compositor-stages-before-draw", true),
		chromedp.Flag("enable-automation", false),
		chromedp.Flag("disable-background-timer-throttling", true),
		chromedp.Flag("disable-backgrounding-occluded-windows", true),
		chromedp.Flag("disable-renderer-backgrounding", true),
		chromedp.Flag("disable-features", "TranslateUI"),
		chromedp.WindowSize(1920, 1080),
		chromedp.UserAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"),
	)
	
	// Add YouTube cookies if available
	youtubeCookiesPath := filepath.Join(ube.workingDir, "youtube_cookies.txt")
	if _, err := os.Stat(youtubeCookiesPath); err == nil {
		opts = append(opts, chromedp.Flag("cookie-file", youtubeCookiesPath))
		log.Printf("🍪 Using YouTube cookies: %s", youtubeCookiesPath)
	}
	
	allocCtx, cancel2 := chromedp.NewExecAllocator(ctx, opts...)
	defer cancel2()
	
	taskCtx, cancel3 := chromedp.NewContext(allocCtx)
	defer cancel3()
	
	var html string
	err := chromedp.Run(taskCtx,
		chromedp.Navigate(mediaURL),
		chromedp.WaitVisible("body", chromedp.ByQuery),
		chromedp.Sleep(3*time.Second), // Let JavaScript load
		chromedp.OuterHTML("html", &html),
	)
	
	if err != nil {
		return nil, fmt.Errorf("enhanced browser navigation failed: %v", err)
	}
	
	log.Printf("📺 Enhanced browser rendered %d bytes of HTML", len(html))
	
	// Extract images and text from rendered HTML
	return ube.parseYouTubeCommunityPostHTML(html, mediaURL, username, displayName)
}

// extractYouTubeCommunityPostFallback uses simpler browser setup as fallback
func (ube *UniversalBrowserExtractor) extractYouTubeCommunityPostFallback(mediaURL, username, displayName string) ([]*MediaInfo, error) {
	log.Printf("📺 Starting fallback browser extraction for YouTube community post")
	
	// Use the existing renderPageWithBrowser method with extended timeout
	html, err := ube.renderPageWithBrowserTimeout(mediaURL, 45*time.Second, 45*time.Second)
	if err != nil {
		return nil, fmt.Errorf("fallback browser extraction failed: %v", err)
	}
	
	log.Printf("📺 Fallback browser rendered %d bytes of HTML", len(html))
	
	// Extract images and text from rendered HTML
	return ube.parseYouTubeCommunityPostHTML(html, mediaURL, username, displayName)
}

// extractYouTubeCommunityPostViaAPI uses third-party API as last resort
func (ube *UniversalBrowserExtractor) extractYouTubeCommunityPostViaAPI(mediaURL, username, displayName string) ([]*MediaInfo, error) {
	log.Printf("📺 Starting API fallback for YouTube community post")
	
	// Extract post ID from URL
	postID := ""
	if strings.Contains(mediaURL, "/post/") {
		parts := strings.Split(mediaURL, "/post/")
		if len(parts) > 1 {
			postID = strings.Split(parts[1], "?")[0] // Remove query parameters
		}
	}
	
	if postID == "" {
		return nil, fmt.Errorf("could not extract post ID from URL: %s", mediaURL)
	}
	
	log.Printf("📺 Extracted post ID: %s", postID)
	
	// Try YouTube operational API
	apiURL := fmt.Sprintf("https://yt.lemnoslife.com/community?part=snippet,contentDetails&id=%s", postID)
	log.Printf("📺 Calling API: %s", apiURL)
	
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(apiURL)
	if err != nil {
		return nil, fmt.Errorf("API request failed: %v", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("API returned HTTP %d", resp.StatusCode)
	}
	
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read API response: %v", err)
	}
	
	log.Printf("📺 API response: %d bytes", len(body))
	
	// For now, return a text-only result indicating API was called
	// TODO: Parse the JSON response to extract actual content
	mediaInfo := &MediaInfo{
		Type:    "text",
		URL:     mediaURL,
		Caption: FormatAuthorCaption("youtube", displayName, username, "YouTube Community Post (via API)"),
	}
	
	return []*MediaInfo{mediaInfo}, nil
}

// parseYouTubeCommunityPostHTML extracts images and text from YouTube community post HTML
func (ube *UniversalBrowserExtractor) parseYouTubeCommunityPostHTML(html, mediaURL, username, displayName string) ([]*MediaInfo, error) {
	log.Printf("📺 Parsing YouTube community post HTML (%d bytes)", len(html))
	
	var mediaInfos []*MediaInfo
	
	// Enhanced YouTube community post image patterns
	imagePatterns := []string{
		// High-quality community post images
		`<meta[^>]*property="og:image"[^>]*content="(https://yt[0-9]\.ggpht\.com/[^"]*)"`,
		`<meta[^>]*property="og:image"[^>]*content="(https://i\.ytimg\.com/[^"]*)"`,
		`<meta[^>]*property="og:image"[^>]*content="(https://lh[0-9]\.googleusercontent\.com/[^"]*)"`,
		`<meta[^>]*name="twitter:image"[^>]*content="(https://yt[0-9]\.ggpht\.com/[^"]*)"`,
		`<meta[^>]*name="twitter:image"[^>]*content="(https://i\.ytimg\.com/[^"]*)"`,
		`<meta[^>]*name="twitter:image"[^>]*content="(https://lh[0-9]\.googleusercontent\.com/[^"]*)"`,
		// JSON embedded images
		`"url":"(https://yt[0-9]\.ggpht\.com/[^"]*)"`,
		`"url":"(https://i\.ytimg\.com/[^"]*\.(jpg|jpeg|png|webp)[^"]*)"`,
		`"url":"(https://lh[0-9]\.googleusercontent\.com/[^"]*)"`,
		`"thumbnail":"(https://yt[0-9]\.ggpht\.com/[^"]*)"`,
		`"image":"(https://yt[0-9]\.ggpht\.com/[^"]*)"`,
		// IMG tags in rendered HTML
		`<img[^>]*src="(https://yt[0-9]\.ggpht\.com/[^"]*)"`,
		`<img[^>]*src="(https://i\.ytimg\.com/[^"]*\.(jpg|jpeg|png|webp)[^"]*)"`,
		`<img[^>]*src="(https://lh[0-9]\.googleusercontent\.com/[^"]*)"`,
		// Background images
		`background-image:\s*url\("(https://yt[0-9]\.ggpht\.com/[^"]*)"`,
		`background-image:\s*url\("(https://i\.ytimg\.com/[^"]*\.(jpg|jpeg|png|webp)[^"]*)"`,
	}
	
	imageURLs := ube.extractURLsWithPatterns(html, imagePatterns)
	log.Printf("📺 Found %d potential YouTube community post images", len(imageURLs))
	
	// Upgrade image quality and remove duplicates
	uniqueImages := ube.upgradeYouTubeImageQuality(imageURLs)
	log.Printf("📺 Found %d unique high-quality images", len(uniqueImages))
	
	// Extract community post text content
	postText := ube.extractYouTubeCommunityPostText(html)
	log.Printf("📺 Extracted post text: %.200s", postText)
	
	// Download images
	for i, imageURL := range uniqueImages {
		filename, err := ube.downloadMediaFile(imageURL, "youtube", "photo", i)
		if err != nil {
			log.Printf("⚠️ Failed to download YouTube community post image %d: %v", i, err)
			continue
		}
		
		mediaInfo := &MediaInfo{
			Type:     "photo",
			URL:      mediaURL,
			Caption:  FormatAuthorCaption("youtube", displayName, username, postText),
			Filename: filename,
		}
		mediaInfos = append(mediaInfos, mediaInfo)
	}
	
	// If no images found, create text-only post
	if len(mediaInfos) == 0 && postText != "" {
		mediaInfo := &MediaInfo{
			Type:    "text",
			URL:     mediaURL,
			Caption: FormatAuthorCaption("youtube", displayName, username, postText),
		}
		mediaInfos = append(mediaInfos, mediaInfo)
	}
	
	log.Printf("📺 Successfully extracted %d media items from YouTube community post", len(mediaInfos))
	return mediaInfos, nil
}

// extractYouTubeCommunityPostText extracts text content from YouTube community post HTML
func (ube *UniversalBrowserExtractor) extractYouTubeCommunityPostText(html string) string {
	// YouTube community post text patterns
	textPatterns := []string{
		// Meta description
		`<meta[^>]*name="description"[^>]*content="([^"]*)"`,
		`<meta[^>]*property="og:description"[^>]*content="([^"]*)"`,
		`<meta[^>]*name="twitter:description"[^>]*content="([^"]*)"`,
		// JSON-LD content
		`"text":\s*"([^"]*)"`,
		`"content":\s*"([^"]*)"`,
		// Community post content
		`class="[^"]*content-text[^"]*"[^>]*>([^<]*)</`,
		`class="[^"]*post-content[^"]*"[^>]*>([^<]*)</`,
	}
	
	for _, pattern := range textPatterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindStringSubmatch(html)
		
		if len(matches) >= 2 {
			text := strings.TrimSpace(matches[1])
			// Decode HTML entities
			text = strings.ReplaceAll(text, "&amp;", "&")
			text = strings.ReplaceAll(text, "&lt;", "<")
			text = strings.ReplaceAll(text, "&gt;", ">")
			text = strings.ReplaceAll(text, "&quot;", `"`)
			text = strings.ReplaceAll(text, "&#39;", "'")
			text = strings.ReplaceAll(text, `\"`, `"`)
			text = strings.ReplaceAll(text, `\\n`, "\n")
			
			if text != "" && len(text) > 10 { // Avoid very short matches
				return text
			}
		}
	}
	
	return ""
}

// upgradeYouTubeImageQuality upgrades YouTube image URLs to higher quality and removes duplicates
func (ube *UniversalBrowserExtractor) upgradeYouTubeImageQuality(imageURLs []string) []string {
	seen := make(map[string]bool)
	var uniqueImages []string
	
	for _, imageURL := range imageURLs {
		// Upgrade quality parameters
		upgradedURL := imageURL
		
		// Upgrade small thumbnails to larger sizes
		upgradedURL = regexp.MustCompile(`=s[0-9]+`).ReplaceAllString(upgradedURL, "=s800")
		upgradedURL = regexp.MustCompile(`=w[0-9]+-h[0-9]+`).ReplaceAllString(upgradedURL, "=w800-h600")
		
		// Upgrade YouTube thumbnails to higher quality
		if strings.Contains(upgradedURL, "i.ytimg.com") {
			upgradedURL = strings.ReplaceAll(upgradedURL, "default.jpg", "maxresdefault.jpg")
			upgradedURL = strings.ReplaceAll(upgradedURL, "hqdefault.jpg", "maxresdefault.jpg")
			upgradedURL = strings.ReplaceAll(upgradedURL, "mqdefault.jpg", "maxresdefault.jpg")
		}
		
		// Remove duplicates based on base URL (ignore quality parameters)
		baseURL := regexp.MustCompile(`[?&].*$`).ReplaceAllString(upgradedURL, "")
		baseURL = regexp.MustCompile(`=s[0-9]+.*$`).ReplaceAllString(baseURL, "")
		
		if !seen[baseURL] {
			seen[baseURL] = true
			uniqueImages = append(uniqueImages, upgradedURL)
		}
	}
	
	return uniqueImages
}