package services

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/timoffmax/social-link-bot/config"
)

// maskProxyCredentials masks sensitive parts of proxy URL for logging
func maskProxyCredentials(proxyURL string) string {
	if atIndex := strings.Index(proxyURL, "@"); atIndex >= 0 {
		if protocolEnd := strings.Index(proxyURL, "://"); protocolEnd >= 0 {
			return proxyURL[:protocolEnd+3] + "***:***@" + proxyURL[atIndex+1:]
		}
	}
	return proxyURL
}

type YtDlpDownloader struct {
	TempDir    string
	BinaryPath string
	WorkingDir string
}

// extractTwitterUsernameFromURL extracts username from Twitter/X URL
func extractTwitterUsernameFromURL(url string) string {
	// Twitter/X URL patterns: https://twitter.com/username/status/... or https://x.com/username/status/...
	re := regexp.MustCompile(`(?:twitter|x)\.com/([^/]+)/status/`)
	matches := re.FindStringSubmatch(url)
	if len(matches) > 1 {
		return matches[1]
	}
	return ""
}

// extractTikTokUsernameFromURL extracts username from TikTok URL
func extractTikTokUsernameFromURL(url string) string {
	// TikTok URL patterns: https://www.tiktok.com/@username/video/... or https://vm.tiktok.com/... (no username)
	re := regexp.MustCompile(`tiktok\.com/@([^/]+)`)
	matches := re.FindStringSubmatch(url)
	if len(matches) > 1 {
		return matches[1]
	}
	// For vm.tiktok.com short URLs, we can't extract username from URL
	return ""
}

// extractYouTubeUsernameFromURL extracts username/channel from YouTube URL
func extractYouTubeUsernameFromURL(url string) string {
	// YouTube URL patterns: https://www.youtube.com/@channelname/... or https://youtube.com/shorts/... (no username)
	re := regexp.MustCompile(`youtube\.com/@([^/]+)`)
	matches := re.FindStringSubmatch(url)
	if len(matches) > 1 {
		return matches[1]
	}
	// For direct /shorts/ URLs, we can't extract username from URL
	return ""
}

// extractRedditUsernameFromURL extracts username from Reddit URL if present
func extractRedditUsernameFromURL(url string) string {
	// Reddit URL patterns with usernames: https://www.reddit.com/user/username/... or https://www.reddit.com/u/username/...
	re := regexp.MustCompile(`reddit\.com/u(?:ser)?/([^/]+)`)
	matches := re.FindStringSubmatch(url)
	if len(matches) > 1 {
		return matches[1]
	}
	return ""
}

// extractRedditSubredditFromURL extracts subreddit from Reddit URL
func extractRedditSubredditFromURL(url string) string {
	// Reddit URL patterns: https://www.reddit.com/r/subreddit/...
	re := regexp.MustCompile(`reddit\.com/r/([^/]+)`)
	matches := re.FindStringSubmatch(url)
	if len(matches) > 1 {
		return matches[1]
	}
	return ""
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
		filepath.Join(workingDir, "bin", "yt-dlp"), // Local development
		"/var/www/social-link-tg-bot/bin/yt-dlp",   // Production bin
		"yt-dlp",                                   // System PATH
		filepath.Join(workingDir, "yt-dlp"),        // Current dir (often broken)
		"/var/www/social-link-tg-bot/yt-dlp",       // Production root
		"youtube-dl",                               // Fallback
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

// ytdlpOutputTemplate names downloaded files "<unix ts>_<url hash>_<title>_<id>".
// findDownloadedFiles relies on the "<ts>_" prefix. The title is capped at 100
// bytes: Facebook titles are whole captions and overflowed the 255-byte file
// name limit ("Cannot write video metadata to JSON file"); the id keeps
// entries of one post apart.
const ytdlpOutputTemplate = "%d_%s_%%(title).100B_%%(id)s.%%(ext)s"

// ytDlpTwitterHostPattern matches twitter.com / x.com hosts (not any URL that
// merely contains "x.com", such as netflix.com).
var ytDlpTwitterHostPattern = regexp.MustCompile(`^https?://([a-z0-9-]+\.)*(twitter|x)\.com(/|$)`)

// ytDlpPlatform names the platform of a URL for the per-platform proxy settings ("" when unknown).
func ytDlpPlatform(url string) string {
	result := ""

	switch {
	case ytDlpTwitterHostPattern.MatchString(url):
		result = "twitter"
	case strings.Contains(url, "tiktok.com"):
		result = "tiktok"
	case (strings.Contains(url, "youtube.com") || strings.Contains(url, "youtu.be")):
		result = "youtube"
	case (strings.Contains(url, "facebook.com") || strings.Contains(url, "fb.com")):
		result = "facebook"
	case strings.Contains(url, "reddit.com"):
		result = "reddit"
	case strings.Contains(url, "9gag.com"):
		result = "9gag"
	}

	return result
}

func (yd *YtDlpDownloader) DownloadMedia(url string) ([]*MediaInfo, error) {
	log.Printf("Starting media download from: %s", url)

	// Skip YouTube community posts for yt-dlp (they need browser extraction)
	if strings.Contains(url, "youtube.com/post/") || strings.Contains(url, "youtu.be/post/") {
		log.Printf("📺 Skipping yt-dlp for YouTube community post - requires browser extraction")
		return nil, fmt.Errorf("yt-dlp cannot handle YouTube community posts, browser extraction required")
	}

	// Ensure temp directory exists
	if err := os.MkdirAll(yd.TempDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %v", err)
	}

	// Create unique filename with timestamp + URL hash to prevent collisions between concurrent downloads
	timestamp := time.Now().Unix()
	urlHash := strings.ToUpper(fmt.Sprintf("%x", md5.Sum([]byte(url))))[:8]
	outputTemplate := filepath.Join(yd.TempDir, fmt.Sprintf(ytdlpOutputTemplate, timestamp, urlHash))

	log.Printf("Using output template: %s", outputTemplate)

	// Get list of proxies to try based on platform
	var proxies []string

	platform := ytDlpPlatform(url)

	// Check if proxies are enabled for this platform
	useProxies := platform != "" && config.IsProxyEnabledForPlatform(platform)

	if useProxies {
		proxies = config.GetYtDlpProxies()
		log.Printf("🔄 Proxies enabled for %s", platform)
	} else {
		proxies = []string{}
		if platform != "" {
			log.Printf("🚫 Proxies disabled for %s", platform)
		}
	}

	// One fixed order for every platform: direct connection first, then the proxies (if enabled)
	proxies = append([]string{""}, proxies...)

	var lastErr error
	isYouTube := (platform == "youtube")

	for i, proxy := range proxies {
		// Proxy URLs may carry credentials: only the masked form is logged.
		connection := "direct"

		if proxy != "" {
			connection = maskProxyCredentials(proxy)
		}

		if proxy == "" {
			if isYouTube {
				log.Printf("YouTube: Using direct connection (no proxy)")
			} else {
				log.Printf("Attempt %d/%d: Trying without proxy", i+1, len(proxies))
			}
		} else {
			log.Printf("Attempt %d/%d: Trying with proxy %s", i+1, len(proxies), connection)
		}

		// Try download with current proxy
		result, err := yd.attemptDownload(url, outputTemplate, proxy)
		if err == nil {
			if isYouTube {
				log.Printf("✅ YouTube download successful")
			} else {
				log.Printf("✅ Download successful (%s)", connection)
			}
			return result, nil
		}

		lastErr = err
		if isYouTube {
			log.Printf("YouTube download failed: %v", err)
		} else {
			log.Printf("Download failed (%s): %v", connection, err)
		}
	}

	// Return the last error from yt-dlp attempts
	return nil, fmt.Errorf("all yt-dlp download attempts failed: %v", lastErr)
}

// attemptDownload tries to download with a specific proxy (or empty string for no proxy)
func (yd *YtDlpDownloader) attemptDownload(url string, outputTemplate string, proxy string) ([]*MediaInfo, error) {
	// Extract timestamp from output template
	var timestamp int64
	fmt.Sscanf(filepath.Base(outputTemplate), "%d_", &timestamp)

	// Build arguments with specific proxy
	args := yd.buildDownloadArgsWithProxy(url, outputTemplate, proxy)

	// Use a 60-second per-attempt timeout so individual yt-dlp calls don't hang
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, yd.BinaryPath, args...)

	// Create safe command string for logging (hide passwords)
	safeArgs := make([]string, len(args))
	copy(safeArgs, args)
	for i := range safeArgs {
		if i > 0 && (args[i-1] == "--password" || args[i-1] == "--username" || args[i-1] == "--proxy") {
			if args[i-1] == "--proxy" && strings.Contains(args[i], "@") {
				// Mask proxy credentials
				safeArgs[i] = maskProxyCredentials(args[i])
			} else {
				safeArgs[i] = "***"
			}
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
		// Check for YouTube-specific errors that indicate proxy issues
		isYouTube := strings.Contains(url, "youtube.com") || strings.Contains(url, "youtu.be")
		if isYouTube {
			// Bot detection errors
			if strings.Contains(outputStr, "Sign in to confirm your age") ||
				strings.Contains(outputStr, "bot detection") ||
				strings.Contains(outputStr, "CAPTCHA") ||
				strings.Contains(outputStr, "429") || // Too Many Requests
				strings.Contains(outputStr, "403") { // Forbidden
				log.Printf("🤖 YouTube bot detection or rate limit with proxy: %s", proxy)
				return nil, fmt.Errorf("YouTube bot detection or rate limit")
			}

			// Connection errors that might be proxy-related
			if strings.Contains(outputStr, "Connection reset") ||
				strings.Contains(outputStr, "Connection refused") ||
				strings.Contains(outputStr, "timed out") ||
				strings.Contains(outputStr, "ProxyError") {
				log.Printf("🔌 Connection error with proxy: %s", proxy)
				return nil, fmt.Errorf("proxy connection error")
			}
		}

		// Check if the error is only due to missing ffmpeg (post-processing failure)
		if (strings.Contains(outputStr, "ffmpeg not found") ||
			strings.Contains(outputStr, "Postprocessing: ffmpeg not found")) &&
			(strings.Contains(outputStr, "[download] 100%") ||
				strings.Contains(outputStr, "Destination:")) {
			log.Printf("Download succeeded but ffmpeg post-processing failed (non-critical)")
			downloadSuccess = true
		} else {
			// For Reddit photo posts, try to extract direct image URLs from yt-dlp output before failing
			if strings.Contains(url, "reddit.com") {
				if imageURLs := yd.extractRedditImageURLsFromOutput(outputStr); len(imageURLs) > 0 {
					log.Printf("🖼️ Found %d Reddit image URLs in yt-dlp output, downloading directly", len(imageURLs))
					return yd.downloadRedditImagesDirectly(imageURLs, url, timestamp)
				}
			}
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

	// Convert to MediaInfo with enhanced handling for all platforms
	var mediaInfos []*MediaInfo
	var tweetDescription string
	var metadataInfo map[string]interface{}

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

	// Look for metadata info file for all platforms
	for _, filePath := range downloadedFiles {
		if strings.HasSuffix(filePath, ".info.json") {
			if content, err := os.ReadFile(filePath); err == nil {
				if err := json.Unmarshal(content, &metadataInfo); err == nil {
					break
				}
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

		// If we have a video file, skip all thumbnails and standalone audio files
		if hasVideo {
			if mediaType == "photo" {
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
			} else if mediaType == "audio" {
				// Skip standalone audio files when video is present (they're usually separate DASH streams)
				log.Printf("Skipping audio file %s because video is present", filePath)
				continue
			}
		}

		// Extract author info and format caption based on platform
		caption := ""
		if strings.Contains(url, "twitter.com") || strings.Contains(url, "x.com") {
			// Twitter/X platform
			username := extractTwitterUsernameFromURL(url)
			displayName := ""

			// Try to get display name from metadata
			if metadataInfo != nil {
				if uploader, ok := metadataInfo["uploader"].(string); ok {
					displayName = uploader
				} else if creator, ok := metadataInfo["creator"].(string); ok {
					displayName = creator
				}
			}

			if tweetDescription != "" {
				caption = FormatAuthorCaption("twitter", displayName, username, tweetDescription)
			} else {
				caption = FormatAuthorCaption("twitter", displayName, username, "")
			}
		} else if strings.Contains(url, "tiktok.com") {
			// TikTok platform
			username := extractTikTokUsernameFromURL(url)
			displayName := ""

			log.Printf("🎵 [TIKTOK] URL-extracted username: '%s'", username)

			// Try to get username and display name from metadata
			if metadataInfo != nil {
				log.Printf("🎵 [TIKTOK] Metadata available, checking fields...")

				// Check various metadata fields for username/display name
				if uploader, ok := metadataInfo["uploader"].(string); ok && uploader != "" {
					log.Printf("🎵 [TIKTOK] Found uploader: '%s'", uploader)
					if username == "" {
						username = strings.TrimPrefix(uploader, "@")
						log.Printf("🎵 [TIKTOK] Set username from uploader: '%s'", username)
					} else {
						displayName = uploader
						log.Printf("🎵 [TIKTOK] Set displayName from uploader: '%s'", displayName)
					}
				}
				if uploaderID, ok := metadataInfo["uploader_id"].(string); ok && uploaderID != "" && username == "" {
					username = strings.TrimPrefix(uploaderID, "@")
					log.Printf("🎵 [TIKTOK] Set username from uploader_id: '%s'", username)
				}
				if creator, ok := metadataInfo["creator"].(string); ok && creator != "" && displayName == "" {
					displayName = creator
					log.Printf("🎵 [TIKTOK] Set displayName from creator: '%s'", displayName)
				}
				if channel, ok := metadataInfo["channel"].(string); ok && channel != "" {
					if username == "" {
						username = strings.TrimPrefix(channel, "@")
						log.Printf("🎵 [TIKTOK] Set username from channel: '%s'", username)
					} else if displayName == "" {
						displayName = channel
						log.Printf("🎵 [TIKTOK] Set displayName from channel: '%s'", displayName)
					}
				}
			} else {
				log.Printf("🎵 [TIKTOK] No metadata available")
			}

			log.Printf("🎵 [TIKTOK] Final: username='%s', displayName='%s'", username, displayName)

			// Try to get description from metadata
			description := ""
			if metadataInfo != nil {
				if desc, ok := metadataInfo["description"].(string); ok {
					description = desc
				} else if title, ok := metadataInfo["title"].(string); ok {
					description = title
				}
			}
			caption = FormatAuthorCaption("tiktok", displayName, username, description)
		} else if strings.Contains(url, "youtube.com") || strings.Contains(url, "youtu.be") {
			// YouTube platform
			username := extractYouTubeUsernameFromURL(url)
			displayName := ""

			// Try to get channel name and display name from metadata
			if metadataInfo != nil {
				if channel, ok := metadataInfo["channel"].(string); ok {
					if username == "" {
						username = channel
					} else {
						displayName = channel // If we have username from URL, channel might be display name
					}
				}
				if uploader, ok := metadataInfo["uploader"].(string); ok {
					if username == "" {
						username = uploader
					} else if displayName == "" {
						displayName = uploader
					}
				}
			}

			// Try to get description from metadata
			description := ""
			if metadataInfo != nil {
				if desc, ok := metadataInfo["description"].(string); ok {
					description = desc
				} else if title, ok := metadataInfo["title"].(string); ok {
					description = title
				}
			}
			caption = FormatAuthorCaption("youtube", displayName, username, description)
		} else if strings.Contains(url, "facebook.com") {
			// Facebook platform - usually only has display names, not usernames
			displayName := ""

			// Try to get display name from metadata (Facebook rarely has usernames)
			if metadataInfo != nil {
				// Check various metadata fields for display name
				if uploader, ok := metadataInfo["uploader"].(string); ok && uploader != "" {
					displayName = uploader
				}
				if creator, ok := metadataInfo["creator"].(string); ok && creator != "" && displayName == "" {
					displayName = creator
				}
				if channel, ok := metadataInfo["channel"].(string); ok && channel != "" && displayName == "" {
					displayName = channel
				}
			}

			// Try to get description from metadata
			description := ""
			if metadataInfo != nil {
				if desc, ok := metadataInfo["description"].(string); ok {
					description = desc
				} else if title, ok := metadataInfo["title"].(string); ok {
					description = title
				}
			}
			caption = FormatAuthorCaption("facebook", displayName, "", description) // Empty username for Facebook
		} else if strings.Contains(url, "reddit.com") {
			// Reddit platform - extract subreddit, username (if available), and post title
			subreddit := extractRedditSubredditFromURL(url)
			username := extractRedditUsernameFromURL(url) // Often empty for most Reddit post URLs

			// Try to get post title/description from metadata
			postTitle := ""
			if metadataInfo != nil {
				// Reddit metadata often has the post title in various fields
				if title, ok := metadataInfo["title"].(string); ok && title != "" {
					postTitle = title
				} else if desc, ok := metadataInfo["description"].(string); ok && desc != "" {
					postTitle = desc
				} else if fulltitle, ok := metadataInfo["fulltitle"].(string); ok && fulltitle != "" {
					postTitle = fulltitle
				}

				// Also try to get the author from metadata if not found in URL
				if username == "" {
					if uploader, ok := metadataInfo["uploader"].(string); ok && uploader != "" {
						username = strings.TrimPrefix(uploader, "u/") // Remove u/ prefix if present
					} else if creator, ok := metadataInfo["creator"].(string); ok && creator != "" {
						username = strings.TrimPrefix(creator, "u/")
					}
				}
			}

			caption = FormatAuthorCaption("reddit", subreddit, username, postTitle)
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
		// Filter out TikTok cover thumbnails (.image files) - they are not real content
		if !isTikTokCoverOnly(mediaInfos) {
			return mediaInfos, nil
		}

		log.Printf("⚠️ Thumbnails-only returned only cover images (.image), skipping to custom extractor")
	}

	// Strategy 3: Use custom TikTok extractor (direct HTML scraping for video/photo URLs)
	log.Printf("Trying custom TikTok extractor for direct video/photo extraction")
	tiktokExtractor := NewTikTokExtractor(yd.WorkingDir, yd.TempDir)

	// For TikTok, we need to resolve short URLs first before extraction
	resolvedURL := url
	if strings.Contains(url, "vm.tiktok.com") || strings.Contains(url, "vt.tiktok.com") {
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

// isTikTokCoverOnly returns true if all media items are TikTok cover thumbnails (.image files)
func isTikTokCoverOnly(mediaInfos []*MediaInfo) bool {
	for _, mi := range mediaInfos {
		if !strings.HasSuffix(mi.Filename, ".image") {
			return false
		}
	}

	return true
}

func (yd *YtDlpDownloader) DownloadTwitterMedia(url string) ([]*MediaInfo, error) {
	// HYBRID APPROACH: Get full text from FxTwitter API, media from yt-dlp
	log.Printf("🐦 Twitter hybrid extraction: text from FxTwitter + media from yt-dlp")

	// Step 1: Get full text from FxTwitter API (via TwitterExtractor)
	twitterExtractor := NewTwitterExtractor(yd.WorkingDir, yd.TempDir)
	var fullCaption string

	textResult, textErr := twitterExtractor.ExtractTweet(url)
	if textErr == nil && len(textResult) > 0 && textResult[0].Caption != "" {
		fullCaption = textResult[0].Caption
		log.Printf("✅ Got FULL text from FxTwitter API: %d chars", len(fullCaption))

		// If TwitterExtractor also got media (photos/videos), use it directly
		if textResult[0].Filename != "" {
			log.Printf("✅ TwitterExtractor also downloaded media, using it directly")
			return textResult, nil
		}
	}

	// Step 2: Try yt-dlp for media download (reliable for videos)
	mediaInfos, err := yd.DownloadMedia(url)
	if err == nil && len(mediaInfos) > 0 {
		// Apply the full text caption from FxTwitter
		if fullCaption != "" {
			log.Printf("✅ Applying FxTwitter caption to yt-dlp media")
			for _, media := range mediaInfos {
				media.Caption = fullCaption
			}
		}
		return mediaInfos, nil
	}
	log.Printf("⚠️ yt-dlp download failed: %v", err)

	// Step 3: Try thumbnails-only for image tweets
	log.Printf("Trying thumbnails-only approach for Twitter")
	mediaInfos, err = yd.downloadThumbnailsOnly(url, "Twitter")
	if err == nil && len(mediaInfos) > 0 {
		if fullCaption != "" {
			for _, media := range mediaInfos {
				media.Caption = fullCaption
			}
		}
		return mediaInfos, nil
	}

	// Step 4: Try CLI browser for images
	cliBrowser := NewCliBrowserExtractor(yd.WorkingDir, yd.TempDir)
	imageResult, imageErr := cliBrowser.ExtractTwitterImages(url)
	if imageErr == nil && len(imageResult) > 0 {
		log.Printf("✅ CLI browser got %d individual images", len(imageResult))
		if fullCaption != "" {
			for _, media := range imageResult {
				media.Caption = fullCaption
			}
		}
		return imageResult, nil
	}

	// Step 5: If we have text result from TwitterExtractor, use it (might have mosaic image)
	if textErr == nil && len(textResult) > 0 {
		log.Printf("📋 Using TwitterExtractor result")
		return textResult, nil
	}

	log.Printf("❌ All Twitter extraction methods failed")
	return nil, fmt.Errorf("all Twitter extraction methods failed")
}

func (yd *YtDlpDownloader) DownloadYouTubeMedia(url string) ([]*MediaInfo, error) {
	// Regular videos: yt-dlp only. The headless browser fallback never
	// succeeded (YouTube walls the datacenter IP) and cost 30s per link; the
	// strategy's next method (Apify) takes over instead.
	return yd.DownloadMedia(url)
}

func (yd *YtDlpDownloader) DownloadRedditMedia(url string) ([]*MediaInfo, error) {
	// For Reddit, try yt-dlp first (works well for video posts) then browser fallback

	// Strategy 1: Try normal yt-dlp download first
	mediaInfos, err := yd.DownloadMedia(url)
	if err == nil && len(mediaInfos) > 0 {
		return mediaInfos, nil
	}

	// Strategy 2: Try thumbnails-only approach for image posts
	log.Printf("Normal download failed, trying thumbnails-only approach for Reddit")
	mediaInfos, err = yd.downloadThumbnailsOnly(url, "Reddit")
	if err == nil && len(mediaInfos) > 0 {
		return mediaInfos, nil
	}

	// The headless browser fallback that used to follow never succeeded and
	// cost 30-60s per link.
	return mediaInfos, err
}

// DownloadFacebookMedia downloads Facebook videos with yt-dlp. The headless
// browser fallback that used to follow never succeeded (60s per failure) and
// hid yt-dlp's real error, so the yt-dlp error is returned as is.
func (yd *YtDlpDownloader) DownloadFacebookMedia(url string) ([]*MediaInfo, error) {
	result, err := yd.DownloadMedia(url)

	return result, err
}

func (yd *YtDlpDownloader) Download9GAGMedia(url string) ([]*MediaInfo, error) {
	log.Printf("🎭 9GAG download: trying yt-dlp with browser impersonation first")

	// Extract post ID from URL
	re := regexp.MustCompile(`9gag\.com/gag/([a-zA-Z0-9]+)`)
	matches := re.FindStringSubmatch(url)
	postID := ""
	if len(matches) > 1 {
		postID = matches[1]
	}

	// Step 1: Try yt-dlp first (handles video/"Animated" posts via curl_cffi impersonation)
	if err := os.MkdirAll(yd.TempDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %v", err)
	}

	timestamp := time.Now().Unix()
	urlHash := strings.ToUpper(fmt.Sprintf("%x", md5.Sum([]byte(url))))[:8]
	outputTemplate := filepath.Join(yd.TempDir, fmt.Sprintf(ytdlpOutputTemplate, timestamp, urlHash))

	result, err := yd.attemptDownload(url, outputTemplate, "")
	if err == nil {
		log.Printf("✅ 9GAG video downloaded via yt-dlp")
		return result, nil
	}

	ytdlpErr := err.Error()
	log.Printf("yt-dlp failed for 9GAG: %s", ytdlpErr)

	// Step 2: If yt-dlp failed (likely a photo post), try the Python API fetcher
	if postID != "" {
		log.Printf("📸 Trying 9GAG API fetch for post %s (may be a photo post)", postID)
		apiResult, apiErr := yd.fetch9GAGPostViaAPI(postID, url, timestamp, urlHash)
		if apiErr == nil {
			return apiResult, nil
		}
		log.Printf("9GAG API fetch failed: %v", apiErr)
	}

	// The CLI browser fallback that used to follow never succeeded and cost
	// 90s per link.
	return nil, fmt.Errorf("9GAG download failed: yt-dlp: %s", ytdlpErr)
}

// fetch9GAGPostViaAPI uses a Python helper with curl_cffi to fetch 9GAG post data
func (yd *YtDlpDownloader) fetch9GAGPostViaAPI(postID, originalURL string, timestamp int64, urlHash string) ([]*MediaInfo, error) {
	// Find the Python interpreter that has curl_cffi (from yt-dlp's pipx venv)
	pythonPath := yd.find9GAGPython()
	if pythonPath == "" {
		return nil, fmt.Errorf("no Python with curl_cffi found")
	}

	scriptPath := yd.getFilePath("scripts/9gag_fetch.py")
	if _, err := os.Stat(scriptPath); err != nil {
		return nil, fmt.Errorf("9gag_fetch.py not found at %s", scriptPath)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, pythonPath, scriptPath, postID)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("9gag_fetch.py failed: %v", err)
	}

	var postData struct {
		Error  string `json:"error"`
		ID     string `json:"id"`
		Title  string `json:"title"`
		Type   string `json:"type"`
		NSFW   int    `json:"nsfw"`
		Images map[string]struct {
			URL      string `json:"url"`
			Width    int    `json:"width"`
			Height   int    `json:"height"`
			VP9URL   string `json:"vp9Url"`
			H265URL  string `json:"h265Url"`
			Duration int    `json:"duration"`
			HasAudio int    `json:"hasAudio"`
		} `json:"images"`
	}

	if err := json.Unmarshal(output, &postData); err != nil {
		return nil, fmt.Errorf("failed to parse 9gag_fetch.py output: %v", err)
	}

	if postData.Error != "" {
		return nil, fmt.Errorf("9GAG API error: %s", postData.Error)
	}

	caption := postData.Title
	if caption != "" {
		caption = "😂 " + caption
	}

	// For video posts, get the best video URL
	if postData.Type == "Animated" {
		if sv, ok := postData.Images["image460sv"]; ok {
			// Prefer H265 MP4 > VP9 WebM > original MP4
			videoURL := sv.URL
			if sv.H265URL != "" {
				videoURL = sv.H265URL
			}

			filePath, dlErr := yd.download9GAGFile(videoURL, timestamp, urlHash, "video")
			if dlErr != nil {
				return nil, fmt.Errorf("failed to download 9GAG video: %v", dlErr)
			}

			mediaResult := []*MediaInfo{{
				Type:     "video",
				URL:      videoURL,
				Filename: filePath,
				Caption:  caption,
			}}
			log.Printf("✅ 9GAG video downloaded via API: %s", postData.Title)
			return mediaResult, nil
		}
	}

	// For photo posts, get the largest image
	if img700, ok := postData.Images["image700"]; ok && img700.URL != "" {
		filePath, dlErr := yd.download9GAGFile(img700.URL, timestamp, urlHash, "photo")
		if dlErr != nil {
			return nil, fmt.Errorf("failed to download 9GAG image: %v", dlErr)
		}

		mediaResult := []*MediaInfo{{
			Type:     "photo",
			URL:      img700.URL,
			Filename: filePath,
			Caption:  caption,
		}}
		log.Printf("✅ 9GAG photo downloaded via API: %s", postData.Title)
		return mediaResult, nil
	}

	// Try image460 as fallback
	if img460, ok := postData.Images["image460"]; ok && img460.URL != "" {
		filePath, dlErr := yd.download9GAGFile(img460.URL, timestamp, urlHash, "photo")
		if dlErr != nil {
			return nil, fmt.Errorf("failed to download 9GAG image: %v", dlErr)
		}

		mediaResult := []*MediaInfo{{
			Type:     "photo",
			URL:      img460.URL,
			Filename: filePath,
			Caption:  caption,
		}}
		log.Printf("✅ 9GAG photo downloaded via API (460): %s", postData.Title)
		return mediaResult, nil
	}

	return nil, fmt.Errorf("no downloadable media found in 9GAG post")
}

// download9GAGFile downloads a file from 9GAG CDN
func (yd *YtDlpDownloader) download9GAGFile(fileURL string, timestamp int64, urlHash string, mediaType string) (string, error) {
	ext := ".jpg"
	if mediaType == "video" {
		if strings.HasSuffix(fileURL, ".webm") {
			ext = ".webm"
		} else {
			ext = ".mp4"
		}
	}

	filename := fmt.Sprintf("%d_%s_9gag%s", timestamp, urlHash, ext)
	filePath := filepath.Join(yd.TempDir, filename)

	req, err := http.NewRequest("GET", fileURL, nil)
	if err != nil {
		return "", err
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Referer", "https://9gag.com/")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("HTTP request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d downloading %s", resp.StatusCode, fileURL)
	}

	outFile, err := os.Create(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to create file: %v", err)
	}
	defer outFile.Close()

	written, err := io.Copy(outFile, resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to write file: %v", err)
	}

	log.Printf("📥 Downloaded 9GAG %s: %s (%d bytes)", mediaType, filename, written)
	return filePath, nil
}

// find9GAGPython finds a Python interpreter that has curl_cffi installed
func (yd *YtDlpDownloader) find9GAGPython() string {
	// Try system Python first, then pipx venv Python
	candidates := []string{
		"python3",
		"python",
		filepath.Join(os.Getenv("HOME"), ".local/share/pipx/venvs/yt-dlp/bin/python3"),
		filepath.Join(os.Getenv("HOME"), ".local/share/pipx/venvs/yt-dlp/bin/python"),
	}

	for _, candidate := range candidates {
		cmd := exec.Command(candidate, "-c", "import curl_cffi")
		if err := cmd.Run(); err == nil {
			log.Printf("✅ Found Python with curl_cffi: %s", candidate)
			return candidate
		}
	}

	log.Printf("⚠️ No Python with curl_cffi found")
	return ""
}

// downloadThumbnailsOnly tries to download only thumbnails and metadata for posts
func (yd *YtDlpDownloader) downloadThumbnailsOnly(url string, platform string) ([]*MediaInfo, error) {
	// Ensure temp directory exists
	if err := os.MkdirAll(yd.TempDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %v", err)
	}

	// Create unique filename with timestamp + URL hash to prevent collisions between concurrent downloads
	timestamp := time.Now().Unix()
	urlHash := strings.ToUpper(fmt.Sprintf("%x", md5.Sum([]byte(url))))[:8]
	outputTemplate := filepath.Join(yd.TempDir, fmt.Sprintf(ytdlpOutputTemplate, timestamp, urlHash))

	log.Printf("Trying thumbnails-only download for %s with template: %s", platform, outputTemplate)

	// Build special args for thumbnails and metadata only
	args := []string{
		"--no-playlist",
		"--output", outputTemplate,
		"--skip-download",        // Skip video/audio download
		"--write-all-thumbnails", // Download all thumbnails
		"--write-info-json",      // Write metadata
		"--write-description",    // Write tweet text
		"--ignore-errors",        // Continue on errors
		"--no-check-certificate",
		"--user-agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36",
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
		"orig":   5, // Highest priority
		"large":  4,
		"medium": 3,
		"small":  2,
		"thumb":  1, // Lowest priority
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

		// Use description text as caption if available, otherwise empty
		caption := ""
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
		"--extractor-retries", "2",
		"--geo-bypass",
		"--geo-bypass-country", "US",
		"--ignore-errors",
		"--no-check-certificate",
		"--cache-dir", cacheDir,
		"--write-info-json",
	}

	// Point yt-dlp to ffmpeg for stream merging (needed for HLS/DASH)
	ffmpegPath := findFFmpegPath()
	if ffmpegPath != "" {
		baseArgs = append(baseArgs, "--ffmpeg-location", ffmpegPath)
	}

	// Platform-specific optimizations
	if strings.Contains(url, "tiktok.com") {
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

		baseArgs = append(baseArgs, tiktokArgs...)
	} else if strings.Contains(url, "twitter.com") || strings.Contains(url, "x.com") {
		// Twitter/X-specific args with support for images and mixed content
		twitterArgs := []string{
			"--user-agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36",
			"--write-all-thumbnails", // Download all available images
			"--write-thumbnail",      // Also try regular thumbnail extraction
			"--write-description",    // Save tweet text
			"--add-metadata",         // Add metadata to files
			"--no-abort-on-error",    // Don't stop on errors
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

		baseArgs = append(baseArgs, twitterArgs...)
	} else if strings.Contains(url, "youtube.com") || strings.Contains(url, "youtu.be") {
		// YouTube now often only provides separate audio/video HLS streams.
		// Replace the default format with one that merges audio+video tracks.
		var newBaseArgs []string
		i := 0
		for i < len(baseArgs) {
			if baseArgs[i] == "--format" && i+1 < len(baseArgs) {
				i += 2
			} else {
				newBaseArgs = append(newBaseArgs, baseArgs[i])
				i++
			}
		}
		baseArgs = newBaseArgs

		// YouTube-specific args
		youtubeArgs := []string{
			"--format", "bestvideo[height<=1080][filesize<50M]+bestaudio/bestvideo[height<=1080]+bestaudio/best[filesize<50M]/best",
			"--merge-output-format", "mp4",
			"--referer", "https://www.youtube.com/",
			"--remote-components", "ejs:github",
		}

		// Add Node.js runtime with explicit path for EJS challenge solving
		nodePath := findNodePath()
		if nodePath != "" {
			youtubeArgs = append(youtubeArgs, "--js-runtimes", "node:"+nodePath)
		} else {
			youtubeArgs = append(youtubeArgs, "--js-runtimes", "node")
			log.Printf("⚠️ No suitable Node.js (v16+) found. YouTube EJS challenge solving may fail.")
		}

		youtubeArgs = append(youtubeArgs, "--user-agent", "Mozilla/5.0 (X11; Linux x86_64; rv:109.0) Gecko/20100101 Firefox/109.0")

		// Rate limiting (essential for YouTube)
		if delay := config.GetYouTubeDelay(); delay != "" {
			youtubeArgs = append(youtubeArgs, "--sleep-interval", delay)
			log.Printf("⏱️ Using %s second delay for YouTube rate limiting", delay)
		}

		baseArgs = append(baseArgs, youtubeArgs...)
	} else if strings.Contains(url, "reddit.com") {
		// Reddit-specific args with DASH format handling
		redditArgs := []string{
			"--user-agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36",
			"--referer", "https://www.reddit.com/",
			"--add-header", "Accept-Language:en-US,en;q=0.9",
			"--add-header", "Accept:text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
		}

		// Reddit needs special format handling for DASH videos
		// Remove the default format from baseArgs since Reddit videos need special format selectors
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

		// Add Reddit-specific format selector that avoids problematic HLS fragments
		// Priority: fallback (single file) > DASH combinations > avoid HLS+DASH that gets 403 errors
		redditArgs = append(redditArgs, "--format", "fallback/dash-4+dash-7/dash-3+dash-7/dash-2+dash-7/best[filesize<50M]/best")

		baseArgs = append(baseArgs, redditArgs...)
	} else if strings.Contains(url, "facebook.com") {
		// Facebook-specific args with enhanced anti-blocking
		facebookArgs := []string{
			"--user-agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36",
			"--referer", "https://www.facebook.com/",
			"--add-header", "Accept-Language:en-US,en;q=0.9",
			"--add-header", "Accept:text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
			"--extractor-retries", "3", // Extra retries for Facebook's blocking
		}

		baseArgs = append(baseArgs, facebookArgs...)
	}

	// Don't add proxy here, it's handled by buildDownloadArgsWithProxy

	baseArgs = append(baseArgs, url)
	return baseArgs
}

func (yd *YtDlpDownloader) detectMediaType(filePath string) string {
	ext := strings.ToLower(filepath.Ext(filePath))

	switch ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".image":
		return "photo"
	case ".mp4", ".mov", ".avi", ".mkv":
		return "video"
	case ".webm":
		// WebM files will be converted to MP4 if ffmpeg is available
		// Otherwise sent as documents
		return "video"
	case ".m4a", ".mp3", ".aac", ".ogg", ".wav":
		return "audio"
	default:
		return "document"
	}
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

// findNodePath locates a suitable Node.js binary (v16+) for yt-dlp EJS challenge solving.
// Returns empty string if not found.
func findNodePath() string {
	workingDir, _ := os.Getwd()
	candidates := []string{
		filepath.Join(workingDir, "bin", "node"),
		filepath.Join(os.Getenv("HOME"), "bin", "node"),
		"/usr/local/bin/node",
		"/usr/bin/node",
	}

	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			cmd := exec.Command(candidate, "--version")
			if output, err := cmd.Output(); err == nil {
				version := strings.TrimSpace(string(output))
				if strings.HasPrefix(version, "v") {
					parts := strings.Split(version[1:], ".")
					if len(parts) > 0 {
						major := 0
						fmt.Sscanf(parts[0], "%d", &major)
						if major >= 16 {
							log.Printf("Found suitable Node.js %s at %s", version, candidate)
							return candidate
						}
						log.Printf("Skipping Node.js %s at %s (too old, need v16+)", version, candidate)
					}
				}
			}
		}
	}

	if path, err := exec.LookPath("node"); err == nil {
		cmd := exec.Command(path, "--version")
		if output, err := cmd.Output(); err == nil {
			version := strings.TrimSpace(string(output))
			if strings.HasPrefix(version, "v") {
				parts := strings.Split(version[1:], ".")
				if len(parts) > 0 {
					major := 0
					fmt.Sscanf(parts[0], "%d", &major)
					if major >= 16 {
						log.Printf("Found suitable Node.js %s in PATH at %s", version, path)
						return path
					}
				}
			}
		}
	}

	return ""
}

// findFFmpegPath locates the ffmpeg binary across common locations.
// Returns empty string if not found.
func findFFmpegPath() string {
	homeDir := os.Getenv("HOME")
	candidates := []string{
		filepath.Join(homeDir, "bin", "ffmpeg"),
		"/usr/bin/ffmpeg",
		"/usr/local/bin/ffmpeg",
	}

	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}

	// Try PATH as last resort
	if path, err := exec.LookPath("ffmpeg"); err == nil {
		return path
	}

	return ""
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

// extractRedditImageURLsFromOutput parses yt-dlp output to extract direct Reddit image URLs
func (yd *YtDlpDownloader) extractRedditImageURLsFromOutput(output string) []string {
	var imageURLs []string

	// Look for lines that contain Reddit image URLs
	// Pattern: [generic] Extracting URL: https://i.redd.it/...
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if strings.Contains(line, "Extracting URL:") && (strings.Contains(line, "i.redd.it") || strings.Contains(line, "preview.redd.it")) {
			// Extract URL using regex - only match direct Reddit image URLs, not media proxy URLs
			re := regexp.MustCompile(`https://(?:i\.redd\.it|preview\.redd\.it)/[^\s]+\.(?:jpg|jpeg|png|gif|webp)`)
			matches := re.FindAllString(line, -1)
			for _, match := range matches {
				// Skip if it's a media proxy URL
				if strings.Contains(match, "/media?url=") {
					continue
				}
				imageURLs = append(imageURLs, match)
				log.Printf("🖼️ Extracted Reddit image URL: %s", match)
			}
		}
	}

	return imageURLs
}

// downloadRedditImagesDirectly downloads Reddit images directly using HTTP
func (yd *YtDlpDownloader) downloadRedditImagesDirectly(imageURLs []string, originalURL string, timestamp int64) ([]*MediaInfo, error) {
	var mediaInfos []*MediaInfo

	// Extract subreddit and post info for captions
	subreddit := extractRedditSubredditFromURL(originalURL)
	username := extractRedditUsernameFromURL(originalURL)

	for i, imageURL := range imageURLs {
		// Download the image
		resp, err := http.Get(imageURL)
		if err != nil {
			log.Printf("❌ Failed to download image %s: %v", imageURL, err)
			continue
		}

		if resp.StatusCode != 200 {
			log.Printf("❌ HTTP error downloading image %s: %d", imageURL, resp.StatusCode)
			resp.Body.Close()
			continue
		}

		// Determine file extension from URL
		ext := "jpg" // default
		if strings.Contains(imageURL, ".png") {
			ext = "png"
		} else if strings.Contains(imageURL, ".gif") {
			ext = "gif"
		} else if strings.Contains(imageURL, ".webp") {
			ext = "webp"
		}

		// Create filename
		filename := filepath.Join(yd.TempDir, fmt.Sprintf("%d_reddit_image_%d.%s", timestamp, i+1, ext))

		// Create file
		file, err := os.Create(filename)
		if err != nil {
			log.Printf("❌ Failed to create file %s: %v", filename, err)
			resp.Body.Close()
			continue
		}

		// Copy image data directly from response to file
		_, err = file.ReadFrom(resp.Body)
		resp.Body.Close()
		file.Close()

		if err != nil {
			log.Printf("❌ Failed to write image data to %s: %v", filename, err)
			continue
		}

		log.Printf("✅ Downloaded Reddit image: %s", filename)

		// Create caption with Reddit info
		caption := FormatAuthorCaption("reddit", subreddit, username, "")

		mediaInfo := &MediaInfo{
			Type:     "photo",
			URL:      originalURL,
			Caption:  caption,
			Filename: filename,
		}

		mediaInfos = append(mediaInfos, mediaInfo)
	}

	if len(mediaInfos) == 0 {
		return nil, fmt.Errorf("failed to download any Reddit images")
	}

	return mediaInfos, nil
}
