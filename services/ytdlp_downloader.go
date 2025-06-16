package services

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	
	"github.com/timoffmax/social-link-bot/config"
)

type YtDlpInfo struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	Ext         string `json:"ext"`
	Description string `json:"description"`
	Uploader    string `json:"uploader"`
	Duration    int    `json:"duration"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	Filesize    int64  `json:"filesize"`
}

type YtDlpDownloader struct {
	TempDir    string
	BinaryPath string
	WorkingDir string
}

// getFilePath resolves file paths relative to the working directory
func (yd *YtDlpDownloader) getFilePath(filename string) string {
	if filepath.IsAbs(filename) {
		return filename
	}
	return filepath.Join(yd.WorkingDir, filename)
}

func NewYtDlpDownloader() *YtDlpDownloader {
	// Get current working directory
	workingDir, err := os.Getwd()
	if err != nil {
		log.Printf("Failed to get working directory: %v", err)
		workingDir = "."
	}
	
	// Use persistent temp directory instead of system temp
	tempDir := filepath.Join(workingDir, "temp")
	os.MkdirAll(tempDir, 0755)
	
	// Try to find yt-dlp binary - test in order of preference
	candidatePaths := []string{
		filepath.Join(workingDir, "bin", "yt-dlp"),        // Local development
		"/var/www/social-link-tg-bot/bin/yt-dlp",          // Production bin
		"yt-dlp",                                          // System PATH
		filepath.Join(workingDir, "yt-dlp"),               // Current dir (often broken)
		"/var/www/social-link-tg-bot/yt-dlp",              // Production root
		"youtube-dl",                                      // Fallback
	}
	
	binaryPath := "yt-dlp" // Default fallback
	
	for _, candidate := range candidatePaths {
		log.Printf("Testing yt-dlp candidate: %s", candidate)
		if testYtDlpBinary(candidate) {
			log.Printf("✅ Found working yt-dlp: %s", candidate)
			binaryPath = candidate
			break
		} else {
			log.Printf("❌ Failed: %s", candidate)
		}
	}
	
	log.Printf("YtDlpDownloader initialized with binary path: %s", binaryPath)
	log.Printf("Working directory: %s", workingDir)
	
	return &YtDlpDownloader{
		TempDir:    tempDir,
		BinaryPath: binaryPath,
		WorkingDir: workingDir,
	}
}

func (yd *YtDlpDownloader) GetMediaInfo(url string) (*YtDlpInfo, error) {
	// Use yt-dlp to extract information without downloading
	cmd := exec.Command(yd.BinaryPath, "--dump-json", "--no-playlist", url)
	
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get media info: %v", err)
	}
	
	var info YtDlpInfo
	err = json.Unmarshal(output, &info)
	if err != nil {
		return nil, fmt.Errorf("failed to parse media info: %v", err)
	}
	
	return &info, nil
}

func (yd *YtDlpDownloader) DownloadMedia(url string) ([]*MediaInfo, error) {
	log.Printf("Starting media download from: %s", url)
	
	// Ensure temp directory exists
	if err := os.MkdirAll(yd.TempDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %v", err)
	}
	
	// Create unique filename with timestamp
	timestamp := time.Now().Unix()
	outputTemplate := filepath.Join(yd.TempDir, fmt.Sprintf("%d_%%(title)s.%%(ext)s", timestamp))
	
	log.Printf("Using output template: %s", outputTemplate)
	
	// Get list of proxies to try
	proxies := config.GetYtDlpProxies()
	
	// Check if we have authentication cookies for this platform
	hasAuthCookies := false
	if strings.Contains(url, "twitter.com") || strings.Contains(url, "x.com") {
		twitterCookiesPath := yd.getFilePath("twitter_cookies.txt")
		if _, err := os.Stat(twitterCookiesPath); err == nil {
			hasAuthCookies = true
		}
	} else if strings.Contains(url, "tiktok.com") {
		tiktokCookiesPath := yd.getFilePath("tiktok_cookies.txt")
		if _, err := os.Stat(tiktokCookiesPath); err == nil {
			hasAuthCookies = true
		}
	} else if strings.Contains(url, "youtube.com") || strings.Contains(url, "youtu.be") {
		youtubeCookiesPath := yd.getFilePath("youtube_cookies.txt")
		if _, err := os.Stat(youtubeCookiesPath); err == nil {
			hasAuthCookies = true
		}
	}
	
	// If we have authentication cookies, prioritize direct connection
	if hasAuthCookies {
		log.Printf("Authentication cookies available - trying direct connection first")
		proxies = []string{""} // Start with direct connection only
	} else {
		// For TikTok and YouTube without cookies, skip proxies and try direct connection first
		if strings.Contains(url, "tiktok.com") || strings.Contains(url, "youtube.com") || strings.Contains(url, "youtu.be") {
			proxies = []string{} // Start with no proxy for these platforms
		}
		// Add empty string to try without proxy as fallback
		proxies = append(proxies, "")
	}
	
	var lastErr error
	for i, proxy := range proxies {
		if proxy == "" {
			log.Printf("Attempt %d/%d: Trying without proxy", i+1, len(proxies))
		} else {
			log.Printf("Attempt %d/%d: Trying with proxy %s", i+1, len(proxies), proxy)
		}
		
		// Try download with current proxy
		result, err := yd.attemptDownload(url, outputTemplate, proxy)
		if err == nil {
			return result, nil
		}
		
		lastErr = err
		log.Printf("Download failed with proxy '%s': %v", proxy, err)
	}
	
	return nil, fmt.Errorf("all download attempts failed, last error: %v", lastErr)
}

// attemptDownload tries to download with a specific proxy (or empty string for no proxy)
func (yd *YtDlpDownloader) attemptDownload(url string, outputTemplate string, proxy string) ([]*MediaInfo, error) {
	// Extract timestamp from output template
	var timestamp int64
	fmt.Sscanf(filepath.Base(outputTemplate), "%d_", &timestamp)
	
	// Build arguments with specific proxy
	args := yd.buildDownloadArgsWithProxy(url, outputTemplate, proxy)
	
	cmd := exec.Command(yd.BinaryPath, args...)
	
	// Create safe command string for logging (hide passwords)
	safeArgs := make([]string, len(args))
	copy(safeArgs, args)
	for i := range safeArgs {
		if i > 0 && (args[i-1] == "--password" || args[i-1] == "--username") {
			safeArgs[i] = "***"
		}
	}
	log.Printf("Executing command: %s %v", yd.BinaryPath, safeArgs)
	
	// Execute download
	output, err := cmd.CombinedOutput()
	outputStr := string(output)
	
	// Check if download was actually successful despite error code
	// yt-dlp can return error codes for non-critical failures like missing ffmpeg
	downloadSuccess := false
	if err != nil {
		// Check if the error is only due to missing ffmpeg (post-processing failure)
		if (strings.Contains(outputStr, "ffmpeg not found") || 
		    strings.Contains(outputStr, "Postprocessing: ffmpeg not found")) && 
		   (strings.Contains(outputStr, "[download] 100%") || 
		    strings.Contains(outputStr, "Destination:")) {
			log.Printf("Download succeeded but ffmpeg post-processing failed (non-critical)")
			downloadSuccess = true
		} else {
			return nil, fmt.Errorf("download failed: %v\nOutput: %s", err, outputStr)
		}
	} else {
		downloadSuccess = true
	}
	
	if downloadSuccess {
		log.Printf("Download completed successfully")
		log.Printf("Output: %s", outputStr)
	}
	
	// List all files in temp directory for debugging
	if files, err := os.ReadDir(yd.TempDir); err == nil {
		log.Printf("Files in temp directory after download:")
		for _, file := range files {
			log.Printf("  - %s", file.Name())
		}
	}
	
	// Find downloaded files
	downloadedFiles, err := yd.findDownloadedFiles(timestamp)
	if err != nil {
		return nil, fmt.Errorf("failed to find downloaded files: %v", err)
	}
	
	log.Printf("Found %d downloaded files: %v", len(downloadedFiles), downloadedFiles)
	
	// Convert to MediaInfo with enhanced handling for Twitter content
	var mediaInfos []*MediaInfo
	var tweetDescription string
	
	// Look for description file for Twitter posts
	if strings.Contains(url, "twitter.com") || strings.Contains(url, "x.com") {
		for _, filePath := range downloadedFiles {
			if strings.HasSuffix(filePath, ".description") {
				if content, err := os.ReadFile(filePath); err == nil {
					tweetDescription = strings.TrimSpace(string(content))
				}
				break
			}
		}
	}
	
	// Check if we have any actual video files
	hasVideo := false
	for _, filePath := range downloadedFiles {
		if strings.HasSuffix(filePath, ".info.json") || strings.HasSuffix(filePath, ".description") {
			continue
		}
		mediaType := yd.detectMediaType(filePath)
		if mediaType == "video" {
			hasVideo = true
			break
		}
	}
	
	for _, filePath := range downloadedFiles {
		// Skip metadata files from being sent as media
		if strings.HasSuffix(filePath, ".info.json") || strings.HasSuffix(filePath, ".description") {
			continue
		}
		
		mediaType := yd.detectMediaType(filePath)
		
		// If we have a video file, skip all thumbnails
		if hasVideo && mediaType == "photo" {
			filename := filepath.Base(filePath)
			// Check if this is a thumbnail file (contains .orig, .large, .medium, .small, .thumb)
			isThumbnail := strings.Contains(filename, ".orig.jpg") || 
						   strings.Contains(filename, ".large.jpg") || 
						   strings.Contains(filename, ".medium.jpg") || 
						   strings.Contains(filename, ".small.jpg") || 
						   strings.Contains(filename, ".thumb.jpg")
			
			if isThumbnail {
				log.Printf("Skipping thumbnail %s because video is present", filePath)
				continue
			}
		}
		
		// Use tweet text as caption if available, otherwise use default
		caption := "Downloaded from " + yd.getPlatformName(url)
		if tweetDescription != "" && (strings.Contains(url, "twitter.com") || strings.Contains(url, "x.com")) {
			caption = tweetDescription
		}
		
		mediaInfo := &MediaInfo{
			Type:     mediaType,
			URL:      url,
			Caption:  caption,
			Filename: filePath,
		}
		
		mediaInfos = append(mediaInfos, mediaInfo)
	}
	
	return mediaInfos, nil
}

func (yd *YtDlpDownloader) DownloadInstagramMedia(url string) ([]*MediaInfo, error) {
	// For Instagram, try multiple strategies for different content types
	
	// Strategy 1: Try normal download first 
	mediaInfos, err := yd.DownloadMedia(url)
	if err == nil && len(mediaInfos) > 0 {
		return mediaInfos, nil
	}
	
	// Strategy 2: Try thumbnails-only approach for image posts/carousels
	log.Printf("Normal download failed, trying thumbnails-only approach for Instagram")
	return yd.downloadThumbnailsOnly(url, "Instagram")
}

func (yd *YtDlpDownloader) DownloadTikTokMedia(url string) ([]*MediaInfo, error) {
	// For TikTok, try multiple strategies for different content types
	
	// Strategy 1: Try normal download first 
	mediaInfos, err := yd.DownloadMedia(url)
	if err == nil && len(mediaInfos) > 0 {
		return mediaInfos, nil
	}
	
	// Strategy 2: Try thumbnails-only approach for image slideshows
	log.Printf("Normal download failed, trying thumbnails-only approach for TikTok")
	mediaInfos, err = yd.downloadThumbnailsOnly(url, "TikTok")
	if err == nil && len(mediaInfos) > 0 {
		return mediaInfos, nil
	}
	
	// Strategy 3: Use custom TikTok extractor as fallback for photo slideshows
	log.Printf("yt-dlp failed completely, trying custom TikTok extractor")
	tiktokExtractor := NewTikTokExtractor(yd.WorkingDir, yd.TempDir)
	
	// For TikTok, we need to resolve short URLs first before extraction
	resolvedURL := url
	if strings.Contains(url, "vm.tiktok.com") {
		log.Printf("🔗 Pre-resolving TikTok short URL for custom extractor: %s", url)
		if resolved, err := yd.resolveRedirectURL(url); err == nil {
			resolvedURL = resolved
			log.Printf("🔗 Pre-resolved to: %s", resolvedURL)
		} else {
			log.Printf("⚠️ Failed to pre-resolve URL, using original: %v", err)
		}
	}
	
	return tiktokExtractor.ExtractTikTokPost(resolvedURL)
}

func (yd *YtDlpDownloader) DownloadTwitterMedia(url string) ([]*MediaInfo, error) {
	// For Twitter, try multiple strategies since yt-dlp image handling is problematic
	
	// Strategy 1: Try normal yt-dlp download first 
	mediaInfos, err := yd.DownloadMedia(url)
	if err == nil && len(mediaInfos) > 0 {
		return mediaInfos, nil
	}
	
	// Strategy 2: Try thumbnails-only approach for image tweets
	log.Printf("Normal download failed, trying thumbnails-only approach for Twitter")
	mediaInfos, err = yd.downloadThumbnailsOnly(url, "Twitter")
	if err == nil && len(mediaInfos) > 0 {
		return mediaInfos, nil
	}
	
	// Strategy 3: Use custom Twitter extractor as fallback
	log.Printf("yt-dlp failed completely, trying custom Twitter extractor")
	twitterExtractor := NewTwitterExtractor(yd.WorkingDir, yd.TempDir)
	return twitterExtractor.ExtractTweet(url)
}

// downloadThumbnailsOnly tries to download only thumbnails and metadata for posts
func (yd *YtDlpDownloader) downloadThumbnailsOnly(url string, platform string) ([]*MediaInfo, error) {
	// Ensure temp directory exists
	if err := os.MkdirAll(yd.TempDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %v", err)
	}
	
	// Create unique filename with timestamp
	timestamp := time.Now().Unix()
	outputTemplate := filepath.Join(yd.TempDir, fmt.Sprintf("%d_%%(title)s.%%(ext)s", timestamp))
	
	log.Printf("Trying thumbnails-only download for %s with template: %s", platform, outputTemplate)
	
	// Build special args for thumbnails and metadata only
	args := []string{
		"--no-playlist",
		"--output", outputTemplate,
		"--skip-download",               // Skip video/audio download
		"--write-all-thumbnails",        // Download all thumbnails
		"--write-info-json",             // Write metadata
		"--write-description",           // Write tweet text
		"--ignore-errors",               // Continue on errors
		"--no-check-certificate",
		"--user-agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36",
	}
	
	// Add platform-specific authentication for thumbnails download
	if strings.Contains(url, "twitter.com") || strings.Contains(url, "x.com") {
		// Check for Twitter-specific cookies file
		twitterCookiesPath := yd.getFilePath("twitter_cookies.txt")
		if _, err := os.Stat(twitterCookiesPath); err == nil {
			args = append(args, "--cookies", twitterCookiesPath)
			log.Printf("Using Twitter cookies for thumbnails download: %s", twitterCookiesPath)
		}
	} else if strings.Contains(url, "tiktok.com") {
		// Check for TikTok-specific cookies file
		tiktokCookiesPath := yd.getFilePath("tiktok_cookies.txt")
		if _, err := os.Stat(tiktokCookiesPath); err == nil {
			args = append(args, "--cookies", tiktokCookiesPath)
			log.Printf("Using TikTok cookies for thumbnails download: %s", tiktokCookiesPath)
		}
	} else if strings.Contains(url, "instagram.com") {
		// Use general cookies file for Instagram
		cookiesPath := yd.getFilePath("cookies.txt")
		if _, err := os.Stat(cookiesPath); err == nil {
			args = append(args, "--cookies", cookiesPath)
			log.Printf("Using Instagram cookies for thumbnails download: %s", cookiesPath)
		}
	}
	
	args = append(args, url)
	
	cmd := exec.Command(yd.BinaryPath, args...)
	
	log.Printf("Executing thumbnails-only command: %s %v", yd.BinaryPath, args)
	
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("Thumbnails-only download failed: %v\nOutput: %s", err, string(output))
		return nil, fmt.Errorf("thumbnails download failed: %v", err)
	}
	
	log.Printf("Thumbnails-only download completed")
	log.Printf("Output: %s", string(output))
	
	// Find downloaded files
	downloadedFiles, err := yd.findDownloadedFiles(timestamp)
	if err != nil {
		return nil, fmt.Errorf("failed to find downloaded files: %v", err)
	}
	
	log.Printf("Found %d files from thumbnails-only download: %v", len(downloadedFiles), downloadedFiles)
	
	// Convert to MediaInfo with Twitter-specific handling
	var mediaInfos []*MediaInfo
	var tweetDescription string
	
	// Look for description file
	for _, filePath := range downloadedFiles {
		if strings.HasSuffix(filePath, ".description") {
			if content, err := os.ReadFile(filePath); err == nil {
				tweetDescription = strings.TrimSpace(string(content))
			}
			break
		}
	}
	
	// Process files (skip metadata files and select best thumbnail for videos)
	bestThumbnail := ""
	thumbnailPriority := map[string]int{
		"orig": 5,    // Highest priority
		"large": 4,
		"medium": 3,
		"small": 2,
		"thumb": 1,   // Lowest priority
	}
	
	// First pass: find best thumbnail if multiple exist
	for _, filePath := range downloadedFiles {
		if strings.HasSuffix(filePath, ".info.json") || strings.HasSuffix(filePath, ".description") {
			continue
		}
		
		// Check if this is a thumbnail file
		filename := filepath.Base(filePath)
		for size, priority := range thumbnailPriority {
			if strings.Contains(filename, "."+size+".jpg") {
				if bestThumbnail == "" {
					bestThumbnail = filePath
				} else {
					// Compare priorities
					currentFilename := filepath.Base(bestThumbnail)
					for currentSize, currentPriority := range thumbnailPriority {
						if strings.Contains(currentFilename, "."+currentSize+".jpg") {
							if priority > currentPriority {
								bestThumbnail = filePath
							}
							break
						}
					}
				}
				break
			}
		}
	}
	
	// Second pass: process files, using only the best thumbnail
	for _, filePath := range downloadedFiles {
		if strings.HasSuffix(filePath, ".info.json") || strings.HasSuffix(filePath, ".description") {
			continue
		}
		
		// Skip other thumbnails if we have a best one
		filename := filepath.Base(filePath)
		isThumbnail := false
		for size := range thumbnailPriority {
			if strings.Contains(filename, "."+size+".jpg") {
				isThumbnail = true
				break
			}
		}
		
		if isThumbnail && filePath != bestThumbnail {
			log.Printf("Skipping thumbnail %s (keeping best: %s)", filePath, bestThumbnail)
			continue
		}
		
		mediaType := yd.detectMediaType(filePath)
		
		// Use description text as caption if available
		caption := fmt.Sprintf("Downloaded from %s", platform)
		if tweetDescription != "" {
			caption = tweetDescription
		}
		
		mediaInfo := &MediaInfo{
			Type:     mediaType,
			URL:      url,
			Caption:  caption,
			Filename: filePath,
		}
		
		mediaInfos = append(mediaInfos, mediaInfo)
	}
	
	// If no images found but we have text content, return it as a text media item
	if len(mediaInfos) == 0 && tweetDescription != "" {
		log.Printf("No media found, returning %s text content: %s", platform, tweetDescription)
		
		// Create a text-only media item
		textMediaInfo := &MediaInfo{
			Type:     "text",
			URL:      url,
			Caption:  tweetDescription,
			Filename: "", // No file for text-only content
		}
		
		mediaInfos = append(mediaInfos, textMediaInfo)
	}
	
	return mediaInfos, nil
}

func (yd *YtDlpDownloader) findDownloadedFiles(timestamp int64) ([]string, error) {
	var files []string
	
	err := filepath.Walk(yd.TempDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		
		if !info.IsDir() {
			filename := info.Name()
			if strings.HasPrefix(filename, fmt.Sprintf("%d_", timestamp)) {
				files = append(files, path)
			}
		}
		
		return nil
	})
	
	return files, err
}

// buildDownloadArgsWithProxy builds download args with a specific proxy
func (yd *YtDlpDownloader) buildDownloadArgsWithProxy(url string, outputTemplate string, proxy string) []string {
	args := yd.buildDownloadArgs(url, outputTemplate)
	
	// Remove any existing proxy from args (it's added at the end by buildDownloadArgs)
	var cleanArgs []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--proxy" {
			i++ // Skip the proxy value
			continue
		}
		cleanArgs = append(cleanArgs, args[i])
	}
	
	// Add specific proxy if provided
	if proxy != "" {
		cleanArgs = append(cleanArgs, "--proxy", proxy)
	}
	
	return cleanArgs
}

// buildDownloadArgs creates platform-specific arguments for yt-dlp
func (yd *YtDlpDownloader) buildDownloadArgs(url string, outputTemplate string) []string {
	// Create cache directory in working directory to avoid permission issues
	cacheDir := yd.getFilePath("cache")
	os.MkdirAll(cacheDir, 0755)
	
	baseArgs := []string{
		"--no-playlist",
		"--output", outputTemplate,
		"--format", "best[filesize<50M]/best",
		"--extractor-retries", "2",        // Reduced retries for faster response
		"--geo-bypass",                    // Bypass geographic restrictions
		"--geo-bypass-country", "US",      // Use US geolocation
		"--ignore-errors",                 // Continue on download errors
		"--no-check-certificate",          // Ignore SSL certificate errors (for proxies)
		"--cache-dir", cacheDir,           // Set cache directory to avoid permission issues
		"--write-info-json",               // Write metadata for better handling
	}
	
	// Add cookies if available
	cookiesPath := yd.getFilePath("cookies.txt")
	if yd.cookiesExist() {
		baseArgs = append(baseArgs, "--cookies", cookiesPath)
	}
	
	// Platform-specific optimizations
	if strings.Contains(url, "instagram.com") {
		// Instagram-specific args with enhanced anti-blocking
		instagramArgs := []string{
			"--user-agent", "Mozilla/5.0 (iPhone; CPU iPhone OS 15_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/15.0 Mobile/15E148 Safari/604.1",
			"--referer", "https://www.instagram.com/",
			"--no-check-certificate",
			"--extractor-args", "instagram:api_version=v1",  // Use older API
			"--add-header", "Accept-Language:en-US,en;q=0.9",
			"--add-header", "Accept:text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
		}
		
		// Add Instagram credentials if available
		if username := config.GetInstagramUsername(); username != "" {
			if password := config.GetInstagramPassword(); password != "" {
				log.Printf("Using Instagram credentials for user: %s", username)
				instagramArgs = append(instagramArgs, "--username", username, "--password", password)
			}
		}
		
		baseArgs = append(baseArgs, instagramArgs...)
	} else if strings.Contains(url, "tiktok.com") {
		// TikTok-specific args with enhanced anti-blocking and no proxy for better compatibility
		tiktokArgs := []string{
			"--user-agent", "Mozilla/5.0 (iPhone; CPU iPhone OS 15_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/15.0 Mobile/15E148 Safari/604.1",
			"--referer", "https://www.tiktok.com/",
			"--add-header", "Accept-Language:en-US,en;q=0.9",
			"--add-header", "Accept:text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
			"--add-header", "sec-fetch-dest:document", 
			"--add-header", "sec-fetch-mode:navigate",
			"--add-header", "sec-fetch-site:none",
			"--add-header", "sec-fetch-user:?1",
		}
		
		// Check for TikTok-specific cookies file first
		tiktokCookiesPath := yd.getFilePath("tiktok_cookies.txt")
		if _, err := os.Stat(tiktokCookiesPath); err == nil {
			tiktokArgs = append(tiktokArgs, "--cookies", tiktokCookiesPath)
			log.Printf("Using TikTok cookies for authentication: %s", tiktokCookiesPath)
		} else {
			log.Printf("⚠️ TikTok cookies not found at %s. Using credentials if available.", tiktokCookiesPath)
			// Add TikTok credentials if available
			if username := config.GetTikTokUsername(); username != "" {
				if password := config.GetTikTokPassword(); password != "" {
					log.Printf("Using TikTok credentials for user: %s", username)
					tiktokArgs = append(tiktokArgs, "--username", username, "--password", password)
				}
			}
		}
		
		baseArgs = append(baseArgs, tiktokArgs...)
	} else if strings.Contains(url, "twitter.com") || strings.Contains(url, "x.com") {
		// Twitter/X-specific args with support for images and mixed content
		twitterArgs := []string{
			"--user-agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36",
			"--write-all-thumbnails",          // Download all available images
			"--write-thumbnail",               // Also try regular thumbnail extraction
			"--write-description",             // Save tweet text
			"--add-metadata",                  // Add metadata to files
			"--no-abort-on-error",             // Don't stop on errors
		}
		
		// For Twitter, we need to remove the --format entirely and let yt-dlp handle it naturally
		// Remove the default format from baseArgs since Twitter images don't work with standard format selectors
		var newBaseArgs []string
		i := 0
		for i < len(baseArgs) {
			if baseArgs[i] == "--format" && i+1 < len(baseArgs) {
				// Skip both --format and its value
				i += 2
			} else {
				newBaseArgs = append(newBaseArgs, baseArgs[i])
				i++
			}
		}
		baseArgs = newBaseArgs
		
		// Check for Twitter-specific cookies file first
		twitterCookiesPath := yd.getFilePath("twitter_cookies.txt")
		if _, err := os.Stat(twitterCookiesPath); err == nil {
			twitterArgs = append(twitterArgs, "--cookies", twitterCookiesPath)
			log.Printf("Using Twitter cookies for authentication: %s", twitterCookiesPath)
		} else {
			log.Printf("⚠️ Twitter cookies not found at %s. Using credentials if available.", twitterCookiesPath)
			// Add Twitter credentials if available
			if username := config.GetTwitterUsername(); username != "" {
				if password := config.GetTwitterPassword(); password != "" {
					log.Printf("Using Twitter credentials for user: %s", username)
					twitterArgs = append(twitterArgs, "--username", username, "--password", password)
				}
			}
		}
		
		baseArgs = append(baseArgs, twitterArgs...)
	} else if strings.Contains(url, "youtube.com") || strings.Contains(url, "youtu.be") {
		// YouTube Shorts-specific args
		youtubeArgs := []string{
			"--user-agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36",
			"--referer", "https://www.youtube.com/",
		}
		
		// Check for YouTube-specific cookies file
		youtubeCookiesPath := yd.getFilePath("youtube_cookies.txt")
		if _, err := os.Stat(youtubeCookiesPath); err == nil {
			youtubeArgs = append(youtubeArgs, "--cookies", youtubeCookiesPath)
			log.Printf("Using YouTube cookies for authentication: %s", youtubeCookiesPath)
		} else {
			log.Printf("⚠️ YouTube cookies not found at %s. YouTube downloads may fail due to bot detection.", youtubeCookiesPath)
			log.Printf("To fix: Export YouTube cookies to youtube_cookies.txt file")
		}
		
		baseArgs = append(baseArgs, youtubeArgs...)
	}
	
	// Don't add proxy here, it's handled by buildDownloadArgsWithProxy
	
	baseArgs = append(baseArgs, url)
	return baseArgs
}

// cookiesExist checks if cookies file exists
func (yd *YtDlpDownloader) cookiesExist() bool {
	cookiesPath := yd.getFilePath("cookies.txt")
	_, err := os.Stat(cookiesPath)
	return err == nil
}

func (yd *YtDlpDownloader) detectMediaType(filePath string) string {
	ext := strings.ToLower(filepath.Ext(filePath))
	
	switch ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp":
		return "photo"
	case ".mp4", ".mov", ".avi", ".mkv", ".webm":
		return "video"
	default:
		return "document"
	}
}

func (yd *YtDlpDownloader) getPlatformName(url string) string {
	if strings.Contains(url, "instagram.com") {
		return "Instagram"
	} else if strings.Contains(url, "tiktok.com") {
		return "TikTok"
	} else if strings.Contains(url, "twitter.com") || strings.Contains(url, "x.com") {
		return "Twitter"
	} else if strings.Contains(url, "youtube.com") || strings.Contains(url, "youtu.be") {
		return "YouTube"
	}
	return "social media"
}

func (yd *YtDlpDownloader) CleanupFile(filepath string) error {
	return os.Remove(filepath)
}

func (yd *YtDlpDownloader) CleanupTempFiles() error {
	// Clean up files older than 1 hour
	cutoff := time.Now().Add(-1 * time.Hour)
	
	return filepath.Walk(yd.TempDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		
		if !info.IsDir() && info.ModTime().Before(cutoff) {
			os.Remove(path)
		}
		
		return nil
	})
}

func (yd *YtDlpDownloader) IsAvailable() bool {
	return testYtDlpBinary(yd.BinaryPath)
}

// testYtDlpBinary tests if a yt-dlp binary path works by running --version
func testYtDlpBinary(path string) bool {
	cmd := exec.Command(path, "--version")
	err := cmd.Run()
	return err == nil
}

// resolveRedirectURL resolves a redirect URL to its final destination
func (yd *YtDlpDownloader) resolveRedirectURL(shortURL string) (string, error) {
	// Create a custom HTTP client that doesn't follow redirects
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