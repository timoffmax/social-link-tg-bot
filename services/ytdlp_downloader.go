package services

import (
	"compress/gzip"
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
	"strconv"
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

// extractInstagramUsernameFromURL extracts username from Instagram URL
func extractInstagramUsernameFromURL(url string) string {
	// Instagram URL patterns: https://www.instagram.com/username/... or https://www.instagram.com/reel/... (no username)
	re := regexp.MustCompile(`instagram\.com/([^/]+)`)
	matches := re.FindStringSubmatch(url)
	if len(matches) > 1 {
		username := matches[1]
		// Skip special paths
		if username != "p" && username != "reel" && username != "tv" && username != "stories" {
			return username
		}
	}
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

// HasInstagramCookies checks if Instagram cookies are available and configured
func (yd *YtDlpDownloader) HasInstagramCookies() bool {
	// Check if cookies are enabled for Instagram in configuration
	if !config.IsCookiesEnabledForPlatform("instagram") {
		return false
	}
	
	// Check if cookie file exists
	instagramCookiesPath := yd.getFilePath("cookies/instagram_cookies.txt")
	if _, err := os.Stat(instagramCookiesPath); err != nil {
		return false
	}
	
	return true
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
	outputTemplate := filepath.Join(yd.TempDir, fmt.Sprintf("%d_%s_%%(title)s.%%(ext)s", timestamp, urlHash))
	
	log.Printf("Using output template: %s", outputTemplate)
	
	// Get list of proxies to try based on platform
	var proxies []string
	
	// Determine platform
	platform := ""
	hasAuthCookies := false
	
	if strings.Contains(url, "twitter.com") || strings.Contains(url, "x.com") {
		platform = "twitter"
		twitterCookiesPath := yd.getFilePath("cookies/twitter_cookies.txt")
		if _, err := os.Stat(twitterCookiesPath); err == nil {
			hasAuthCookies = true
		}
	} else if strings.Contains(url, "tiktok.com") {
		platform = "tiktok"
		tiktokCookiesPath := yd.getFilePath("cookies/tiktok_cookies.txt")
		if _, err := os.Stat(tiktokCookiesPath); err == nil {
			hasAuthCookies = true
		}
	} else if strings.Contains(url, "youtube.com") || strings.Contains(url, "youtu.be") {
		platform = "youtube"
		youtubeCookiesPath := yd.getFilePath("cookies/youtube_cookies.txt")
		if _, err := os.Stat(youtubeCookiesPath); err == nil {
			hasAuthCookies = true
		}
	} else if strings.Contains(url, "instagram.com") {
		platform = "instagram"
		if config.IsCookiesEnabledForPlatform("instagram") {
			instagramCookiesPath := yd.getFilePath("cookies/instagram_cookies.txt")
			if _, err := os.Stat(instagramCookiesPath); err == nil {
				hasAuthCookies = true
			}
		}
	} else if strings.Contains(url, "facebook.com") || strings.Contains(url, "fb.com") {
		platform = "facebook"
		facebookCookiesPath := yd.getFilePath("cookies/facebook_cookies.txt")
		if _, err := os.Stat(facebookCookiesPath); err == nil {
			hasAuthCookies = true
		}
	} else if strings.Contains(url, "reddit.com") {
		platform = "reddit"
		redditCookiesPath := yd.getFilePath("cookies/reddit_cookies.txt")
		if _, err := os.Stat(redditCookiesPath); err == nil {
			hasAuthCookies = true
		}
	} else if strings.Contains(url, "9gag.com") {
		platform = "9gag"
		ninegagCookiesPath := yd.getFilePath("cookies/9gag_cookies.txt")
		if _, err := os.Stat(ninegagCookiesPath); err == nil {
			hasAuthCookies = true
		}
	}
	
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
	
	// Add direct connection strategy based on platform and cookies
	if platform == "youtube" {
		// YouTube always uses direct connection
		log.Printf("🎯 YouTube detected: Using direct connection with server-IP authenticated cookies")
		if len(proxies) == 0 {
			proxies = []string{""} // Only direct connection
		}
	} else if hasAuthCookies {
		// For other platforms with cookies, prioritize direct connection
		log.Printf("Authentication cookies available for %s - trying direct connection first", platform)
		proxies = append([]string{""}, proxies...) // Direct connection first
	} else {
		// Add direct connection as fallback
		proxies = append(proxies, "")
	}
	
	var lastErr error
	isYouTube := strings.Contains(url, "youtube.com") || strings.Contains(url, "youtu.be")
	
	for i, proxy := range proxies {
		if proxy == "" {
			if isYouTube {
				log.Printf("YouTube: Using direct connection (no proxy)")
			} else {
				log.Printf("Attempt %d/%d: Trying without proxy", i+1, len(proxies))
			}
		} else {
			log.Printf("Attempt %d/%d: Trying with proxy %s", i+1, len(proxies), proxy)
		}
		
		// Try download with current proxy
		result, err := yd.attemptDownload(url, outputTemplate, proxy)
		if err == nil {
			if isYouTube {
				log.Printf("✅ YouTube download successful with server-IP cookies")
			} else {
				log.Printf("✅ Download successful with proxy: %s", proxy)
			}
			return result, nil
		}
		
		lastErr = err
		if isYouTube {
			log.Printf("YouTube download failed: %v", err)
		} else {
			log.Printf("Download failed with proxy '%s': %v", proxy, err)
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
		} else if strings.Contains(url, "instagram.com") {
			// Instagram platform
			username := extractInstagramUsernameFromURL(url)
			displayName := ""
			
			// Try to get username and display name from metadata
			if metadataInfo != nil {
				if uploader, ok := metadataInfo["uploader"].(string); ok {
					if username == "" {
						username = uploader
					} else {
						displayName = uploader // If we have username from URL, uploader might be display name
					}
				}
				if creator, ok := metadataInfo["creator"].(string); ok && displayName == "" {
					displayName = creator
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
			caption = FormatAuthorCaption("instagram", displayName, username, description)
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

func (yd *YtDlpDownloader) DownloadInstagramMedia(url string) ([]*MediaInfo, error) {
	// Detect content type from URL
	isVideo := (strings.Contains(url, "/reel/") || strings.Contains(url, "/tv/") || strings.Contains(url, "/reels/"))
	isPhoto := strings.Contains(url, "/p/")
	hasCookies := yd.HasInstagramCookies()

	// For photo posts (which may be carousels):
	// - WITH cookies: Try browser extraction (handles carousels well with JS rendering)
	// - WITHOUT cookies: Skip browser (times out on login wall), use fixed services instead
	if isPhoto && hasCookies {
		log.Printf("Detected Instagram photo post WITH cookies - trying browser extraction first for carousel support")
		browserExtractor := NewUniversalBrowserExtractor(yd.WorkingDir, yd.TempDir)
		browserMediaInfos, err := browserExtractor.ExtractMedia(url)
		if err == nil && len(browserMediaInfos) > 0 {
			// Check if we got actual media files (not just text)
			hasActualMedia := false
			for _, media := range browserMediaInfos {
				if media.Type != "text" && media.Filename != "" {
					hasActualMedia = true
					break
				}
			}
			if hasActualMedia {
				log.Printf("✅ Browser extraction succeeded with %d items (carousel support)", len(browserMediaInfos))
				return browserMediaInfos, nil
			}
			log.Printf("Browser extraction only returned text content, trying other strategies")
		}
		log.Printf("Browser extraction failed, falling back to other methods")
	} else if isPhoto && !hasCookies {
		log.Printf("Detected Instagram photo post WITHOUT cookies - skipping browser extraction (would timeout)")
		log.Printf("Will try fixed service URLs (ddinstagram) which support carousels")

		// Use ddinstagram.com for internal downloading (better for actual media extraction)
		fixedServiceURL := strings.Replace(url, "https://www.instagram.com", "https://d.ddinstagram.com", 1)
		fixedServiceURL = strings.Replace(fixedServiceURL, "https://instagram.com", "https://d.ddinstagram.com", 1)

		log.Printf("🔄 Trying yt-dlp with ddinstagram for carousel support: %s", fixedServiceURL)
		fixedMediaInfos, err := yd.DownloadMedia(fixedServiceURL)
		if err == nil && len(fixedMediaInfos) > 0 {
			// Check if we got actual media files
			hasActualMedia := false
			for _, media := range fixedMediaInfos {
				if media.Type != "text" && media.Filename != "" {
					hasActualMedia = true
					break
				}
			}
			if hasActualMedia {
				log.Printf("✅ Fixed service (ddinstagram) succeeded with %d items", len(fixedMediaInfos))
				return fixedMediaInfos, nil
			}
		}
		log.Printf("Fixed service download failed, trying direct Instagram URL")
	}

	// Strategy 1: Try yt-dlp (prioritized for videos, fallback for photos)
	log.Printf("Trying yt-dlp for Instagram media")
	mediaInfos, err := yd.DownloadMedia(url)
	if err == nil && len(mediaInfos) > 0 {
		return mediaInfos, nil
	}

	// Strategy 2: Universal headless browser (fallback for videos if yt-dlp failed)
	if isVideo {
		log.Printf("yt-dlp failed, trying universal headless browser for Instagram video")
		browserExtractor := NewUniversalBrowserExtractor(yd.WorkingDir, yd.TempDir)
		browserMediaInfos, err := browserExtractor.ExtractMedia(url)
		if err == nil && len(browserMediaInfos) > 0 {
			// Check if we got actual media files (not just text)
			hasActualMedia := false
			for _, media := range browserMediaInfos {
				if media.Type != "text" && media.Filename != "" {
					hasActualMedia = true
					break
				}
			}
			if hasActualMedia {
				return browserMediaInfos, nil
			}
			log.Printf("Browser extraction only returned text content, continuing to other strategies")
		}
	}
	
	// Strategy 3: Try Instagram GraphQL API (sometimes works without authentication)
	log.Printf("Browser extraction failed, trying Instagram GraphQL API")
	graphqlExtractor := NewInstagramGraphQLExtractor(yd.TempDir)
	graphqlMediaInfos, err := graphqlExtractor.ExtractViaGraphQL(url)
	if err == nil && len(graphqlMediaInfos) > 0 {
		return graphqlMediaInfos, nil
	}
	log.Printf("GraphQL extraction failed: %v", err)
	
	// Strategy 4: Try Instagram oEmbed API (often blocked now)
	log.Printf("GraphQL failed, trying Instagram oEmbed API")
	embedExtractor := NewInstagramEmbedExtractor(yd.TempDir)
	embedMediaInfos, err := embedExtractor.ExtractViaEmbed(url)
	if err == nil && len(embedMediaInfos) > 0 {
		return embedMediaInfos, nil
	}
	log.Printf("oEmbed extraction failed: %v", err)
	
	// Strategy 5: Try thumbnails-only approach for metadata/description
	log.Printf("oEmbed failed, trying thumbnails-only approach for Instagram")
	mediaInfos, err = yd.downloadThumbnailsOnly(url, "Instagram")
	if err == nil && len(mediaInfos) > 0 {
		return mediaInfos, nil
	}
	
	// Strategy 6: Try direct HTTP Instagram extraction only for non-video content
	// Skip for reels/videos as they can't be extracted via HTML scraping
	if !strings.Contains(url, "/reel/") && !strings.Contains(url, "/tv/") {
		log.Printf("Thumbnails failed, trying direct Instagram text extraction")
		directMediaInfos, err := yd.downloadInstagramDirect(url)
		if err == nil && len(directMediaInfos) > 0 {
			return directMediaInfos, nil
		}
	} else {
		log.Printf("Skipping direct extraction for Instagram reel/video - not supported")
	}
	
	return nil, fmt.Errorf("all Instagram extraction strategies failed")
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
	// Check if this is a YouTube community post (requires special handling)
	if strings.Contains(url, "/post/") {
		log.Printf("📺 Detected YouTube community post, using specialized extractor")
		cliBrowser := NewCliBrowserExtractor(yd.WorkingDir, yd.TempDir)
		return cliBrowser.ExtractYouTubeCommunityPost(url)
	}
	
	// For regular YouTube videos, try yt-dlp first then fallback to headless browser for thumbnails
	
	// Strategy 1: Try normal yt-dlp download first 
	mediaInfos, err := yd.DownloadMedia(url)
	if err == nil && len(mediaInfos) > 0 {
		return mediaInfos, nil
	}
	
	// Strategy 2: Universal headless browser fallback (mainly for thumbnails and metadata)
	log.Printf("yt-dlp failed, trying universal headless browser for YouTube")
	browserExtractor := NewUniversalBrowserExtractor(yd.WorkingDir, yd.TempDir)
	return browserExtractor.ExtractMedia(url)
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
	
	// Strategy 3: Universal headless browser fallback for images and galleries
	log.Printf("yt-dlp failed completely, trying universal headless browser for Reddit")
	browserExtractor := NewUniversalBrowserExtractor(yd.WorkingDir, yd.TempDir)
	return browserExtractor.ExtractMedia(url)
}

func (yd *YtDlpDownloader) DownloadFacebookMedia(url string) ([]*MediaInfo, error) {
	// For Facebook, try yt-dlp first (works for some video posts) then browser fallback
	
	// Strategy 1: Try normal yt-dlp download first 
	mediaInfos, err := yd.DownloadMedia(url)
	if err == nil && len(mediaInfos) > 0 {
		return mediaInfos, nil
	}
	
	// Strategy 2: Universal headless browser fallback for photos and other content
	log.Printf("yt-dlp failed, trying universal headless browser for Facebook")
	browserExtractor := NewUniversalBrowserExtractor(yd.WorkingDir, yd.TempDir)
	return browserExtractor.ExtractMedia(url)
}

func (yd *YtDlpDownloader) Download9GAGMedia(url string) ([]*MediaInfo, error) {
	// For 9GAG, skip yt-dlp entirely (always fails with 403) and use CLI browser directly
	log.Printf("🚀 Using CLI browser directly for 9GAG (yt-dlp always fails with 403)")
	cliBrowser := NewCliBrowserExtractor(yd.WorkingDir, yd.TempDir)
	return cliBrowser.Extract9GAGContent(url)
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
	outputTemplate := filepath.Join(yd.TempDir, fmt.Sprintf("%d_%s_%%(title)s.%%(ext)s", timestamp, urlHash))
	
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
		twitterCookiesPath := yd.getFilePath("cookies/twitter_cookies.txt")
		if _, err := os.Stat(twitterCookiesPath); err == nil {
			args = append(args, "--cookies", twitterCookiesPath)
			log.Printf("Using Twitter cookies for thumbnails download: %s", twitterCookiesPath)
		}
	} else if strings.Contains(url, "tiktok.com") {
		// Check for TikTok-specific cookies file
		tiktokCookiesPath := yd.getFilePath("cookies/tiktok_cookies.txt")
		if _, err := os.Stat(tiktokCookiesPath); err == nil {
			args = append(args, "--cookies", tiktokCookiesPath)
			log.Printf("Using TikTok cookies for thumbnails download: %s", tiktokCookiesPath)
		}
	} else if strings.Contains(url, "instagram.com") {
		// Use general cookies file for Instagram if enabled
		if config.IsCookiesEnabledForPlatform("instagram") {
			cookiesPath := yd.getFilePath("cookies/instagram_cookies.txt")
			if _, err := os.Stat(cookiesPath); err == nil {
				args = append(args, "--cookies", cookiesPath)
				log.Printf("Using Instagram cookies for thumbnails download: %s", cookiesPath)
			}
		}
	} else if strings.Contains(url, "reddit.com") {
		// Check for Reddit-specific cookies file
		redditCookiesPath := yd.getFilePath("cookies/reddit_cookies.txt")
		if _, err := os.Stat(redditCookiesPath); err == nil {
			args = append(args, "--cookies", redditCookiesPath)
			log.Printf("Using Reddit cookies for thumbnails download: %s", redditCookiesPath)
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
	// EXCEPT for Instagram posts - let them fall through to direct HTML extraction
	if len(mediaInfos) == 0 && tweetDescription != "" {
		// For Instagram posts, don't create text-only content - let direct extraction try instead
		if strings.Contains(strings.ToLower(platform), "instagram") {
			log.Printf("No media found for Instagram, skipping text-only to allow direct extraction")
			return nil, fmt.Errorf("no media found, letting direct extraction handle it")
		}
		
		log.Printf("No media found, returning %s text content: %s", platform, tweetDescription)
		
		// Create a text-only media item for non-Instagram platforms
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

	// Note: Platform-specific cookies are added in the platform-specific sections below
	
	// Platform-specific optimizations
	if strings.Contains(url, "instagram.com") {
		// Instagram-specific args with enhanced anti-blocking
		instagramArgs := []string{
			// Use a more realistic mobile user agent
			"--user-agent", "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Mobile/15E148 Instagram 300.0.0.0.0 (iPhone13,2; iOS 17_0; en_US; en-US; scale=3.00; 1170x2532; 520433936)",
			"--referer", "https://www.instagram.com/",
			"--no-check-certificate",
			// Try different API approaches
			"--extractor-args", "instagram:api=graphql",  // Try GraphQL API
			// Add more realistic headers
			"--add-header", "Accept-Language:en-US,en;q=0.9",
			"--add-header", "Accept:text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8",
			"--add-header", "X-IG-App-ID:936619743392459",  // Instagram web app ID
			"--add-header", "X-ASBD-ID:129477",
			"--add-header", "X-IG-WWW-Claim:0",
			"--add-header", "X-Requested-With:XMLHttpRequest",
			// Add delay to avoid rate limiting
			"--sleep-requests", "2",
			"--sleep-interval", "3",
		}
		
		// Check for Instagram cookies if enabled
		if config.IsCookiesEnabledForPlatform("instagram") {
			instagramCookiesPath := yd.getFilePath("cookies/instagram_cookies.txt")
			if _, err := os.Stat(instagramCookiesPath); err == nil {
				instagramArgs = append(instagramArgs, "--cookies", instagramCookiesPath)
				log.Printf("Using Instagram cookies for authentication: %s", instagramCookiesPath)
			} else {
				log.Printf("⚠️ Instagram cookies not found at %s. Using credentials if available.", instagramCookiesPath)
				// Add Instagram credentials if available and cookies not found
				if username := config.GetInstagramUsername(); username != "" {
					if password := config.GetInstagramPassword(); password != "" {
						log.Printf("Using Instagram credentials for user: %s", username)
						instagramArgs = append(instagramArgs, "--username", username, "--password", password)
					}
				}
			}
		} else {
			log.Printf("🚫 Instagram cookies disabled via configuration")
			// Still try credentials if cookies are disabled
			if username := config.GetInstagramUsername(); username != "" {
				if password := config.GetInstagramPassword(); password != "" {
					log.Printf("Using Instagram credentials for user: %s", username)
					instagramArgs = append(instagramArgs, "--username", username, "--password", password)
				}
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
		tiktokCookiesPath := yd.getFilePath("cookies/tiktok_cookies.txt")
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
		twitterCookiesPath := yd.getFilePath("cookies/twitter_cookies.txt")
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

		// YouTube-specific args with server-IP authenticated cookies
		youtubeArgs := []string{
			"--format", "bestvideo[height<=1080][filesize<50M]+bestaudio/bestvideo[height<=1080]+bestaudio/best[filesize<50M]/best",
			"--merge-output-format", "mp4",
			"--referer", "https://www.youtube.com/",
		}
		
		// Strategy 1: Use server-IP authenticated cookie file (highest priority)
		cookieSuccess := false
		youtubeCookiesPath := yd.getFilePath("cookies/youtube_cookies.txt")
		if _, err := os.Stat(youtubeCookiesPath); err == nil {
			youtubeArgs = append(youtubeArgs, "--cookies", youtubeCookiesPath)
			log.Printf("🔥 Using server-IP authenticated YouTube cookies: %s", youtubeCookiesPath)
			cookieSuccess = true
		} else {
			log.Printf("⚠️ No YouTube cookies found. Extract from Firefox: ./bin/extract-firefox-cookies")
		}
		
		// Strategy 2: Try Firefox browser cookie extraction as fallback
		if !cookieSuccess {
			log.Printf("🦊 Trying Firefox cookie extraction for YouTube...")
			testCmd := exec.Command(yd.BinaryPath, "--cookies-from-browser", "firefox", "--dump-json", "--no-download", "https://www.youtube.com/watch?v=dQw4w9WgXcQ")
			if err := testCmd.Run(); err == nil {
				youtubeArgs = append(youtubeArgs, "--cookies-from-browser", "firefox")
				log.Printf("✅ Using Firefox browser cookies for YouTube")
				cookieSuccess = true
			}
		}
		
		// Strategy 3: Enhanced User-Agent matching
		if cookieSuccess {
			// Use Firefox User-Agent to match cookie source
			youtubeArgs = append(youtubeArgs, "--user-agent", "Mozilla/5.0 (X11; Linux x86_64; rv:109.0) Gecko/20100101 Firefox/109.0")
		} else {
			log.Printf("❌ No YouTube authentication available")
			log.Printf("💡 Setup authentication:")
			log.Printf("   1. Connect to VNC: ssh -L 5901:localhost:5901 user@server")
			log.Printf("   2. Open Firefox and login to YouTube")
			log.Printf("   3. Extract cookies: ./bin/extract-firefox-cookies")
			// Use basic User-Agent without cookies
			youtubeArgs = append(youtubeArgs, "--user-agent", "Mozilla/5.0 (X11; Linux x86_64; rv:109.0) Gecko/20100101 Firefox/109.0")
		}
		
		// Strategy 4: Rate limiting (essential for YouTube)
		if delay := config.GetYouTubeDelay(); delay != "" {
			youtubeArgs = append(youtubeArgs, "--sleep-interval", delay)
			log.Printf("⏱️ Using %s second delay for YouTube rate limiting", delay)
		}
		
		baseArgs = append(baseArgs, youtubeArgs...)
	} else if strings.Contains(url, "reddit.com") {
		// Reddit-specific args with enhanced cookie support and DASH format handling
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
		
		// Check for Reddit-specific cookies file
		redditCookiesPath := yd.getFilePath("cookies/reddit_cookies.txt")
		if _, err := os.Stat(redditCookiesPath); err == nil {
			redditArgs = append(redditArgs, "--cookies", redditCookiesPath)
			log.Printf("Using Reddit cookies for authentication: %s", redditCookiesPath)
		} else {
			log.Printf("⚠️ Reddit cookies not found at %s. Reddit downloads may be limited.", redditCookiesPath)
		}
		
		baseArgs = append(baseArgs, redditArgs...)
	} else if strings.Contains(url, "facebook.com") {
		// Facebook-specific args with enhanced anti-blocking
		facebookArgs := []string{
			"--user-agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36",
			"--referer", "https://www.facebook.com/",
			"--add-header", "Accept-Language:en-US,en;q=0.9",
			"--add-header", "Accept:text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
			"--extractor-retries", "3",        // Extra retries for Facebook's blocking
		}
		
		// Check for Facebook-specific cookies file
		facebookCookiesPath := yd.getFilePath("cookies/facebook_cookies.txt")
		if _, err := os.Stat(facebookCookiesPath); err == nil {
			facebookArgs = append(facebookArgs, "--cookies", facebookCookiesPath)
			log.Printf("Using Facebook cookies for authentication: %s", facebookCookiesPath)
		} else {
			log.Printf("⚠️ Facebook cookies not found at %s. Facebook downloads may be limited.", facebookCookiesPath)
		}
		
		baseArgs = append(baseArgs, facebookArgs...)
	}
	
	// Don't add proxy here, it's handled by buildDownloadArgsWithProxy
	
	baseArgs = append(baseArgs, url)
	return baseArgs
}

// cookiesExist checks if cookies file exists
func (yd *YtDlpDownloader) cookiesExist() bool {
	cookiesPath := yd.getFilePath("cookies/instagram_cookies.txt")
	_, err := os.Stat(cookiesPath)
	return err == nil
}

func (yd *YtDlpDownloader) detectMediaType(filePath string) string {
	ext := strings.ToLower(filepath.Ext(filePath))
	
	switch ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp":
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

func (yd *YtDlpDownloader) getPlatformName(url string) string {
	if strings.Contains(url, "instagram.com") {
		return "Instagram"
	} else if strings.Contains(url, "tiktok.com") {
		return "TikTok"
	} else if strings.Contains(url, "twitter.com") || strings.Contains(url, "x.com") {
		return "Twitter"
	} else if strings.Contains(url, "youtube.com") || strings.Contains(url, "youtu.be") {
		return "YouTube"
	} else if strings.Contains(url, "reddit.com") {
		return "Reddit"
	} else if strings.Contains(url, "facebook.com") {
		return "Facebook"
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

// downloadInstagramDirect extracts Instagram content using direct HTTP requests to mobile Instagram
func (yd *YtDlpDownloader) downloadInstagramDirect(url string) ([]*MediaInfo, error) {
	log.Printf("🔍 Attempting direct Instagram extraction for: %s", url)
	
	// Try regular Instagram URL first (simpler approach)
	log.Printf("📱 Trying direct Instagram URL: %s", url)
	
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}
	
	// Add Instagram mobile headers
	req.Header.Set("User-Agent", "Mozilla/5.0 (iPhone; CPU iPhone OS 15_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/15.0 Mobile/15E148 Safari/604.1")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Referer", "https://www.instagram.com/")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	
	// Add cookies if enabled and available
	if config.IsCookiesEnabledForPlatform("instagram") {
		if cookieData, err := os.ReadFile(yd.getFilePath("cookies/instagram_cookies.txt")); err == nil {
			// Parse cookies and add them to request
			lines := strings.Split(string(cookieData), "\n")
			for _, line := range lines {
				if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
					continue
				}
				parts := strings.Split(line, "\t")
				if len(parts) >= 7 {
					cookieName := parts[5]
					cookieValue := parts[6]
					// Clean cookie value - remove quotes and handle escape sequences
					cookieValue = strings.Trim(cookieValue, `"`)
					cookieValue = strings.ReplaceAll(cookieValue, `\"`, `"`)
					cookieValue = strings.ReplaceAll(cookieValue, `\054`, `,`)
					cookieValue = strings.ReplaceAll(cookieValue, `\\`, `\`)
					// Skip problematic cookie values that still contain invalid characters
					if cookieName != "" && cookieValue != "" && !strings.Contains(cookieValue, `\`) {
						req.AddCookie(&http.Cookie{Name: cookieName, Value: cookieValue})
					}
				}
			}
			log.Printf("📦 Added Instagram cookies to direct HTTP request")
		}
	} else {
		log.Printf("🚫 Instagram cookies disabled for direct extraction")
	}
	
	resp, err := SharedHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch Instagram page: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		log.Printf("❌ Instagram HTTP error: %d %s", resp.StatusCode, resp.Status)
		return nil, fmt.Errorf("Instagram returned HTTP %d", resp.StatusCode)
	}
	
	log.Printf("✅ Successfully fetched Instagram page, Content-Length: %s", resp.Header.Get("Content-Length"))
	
	// Read response body - handle gzip compression
	var body []byte
	
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gzipReader, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to create gzip reader: %v", err)
		}
		defer gzipReader.Close()
		body, err = io.ReadAll(gzipReader)
		if err != nil {
			return nil, fmt.Errorf("failed to read gzip response body: %v", err)
		}
		log.Printf("🗜️ Decompressed gzip content")
	} else {
		body, err = io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read response body: %v", err)
		}
	}
	
	html := string(body)
	log.Printf("📄 Retrieved HTML content: %d bytes", len(html))
	
	// DEBUG: Save HTML to file for inspection
	htmlFile := filepath.Join(yd.TempDir, fmt.Sprintf("instagram_debug_%d.html", time.Now().Unix()))
	if err := os.WriteFile(htmlFile, []byte(html), 0644); err == nil {
		log.Printf("🔍 DEBUG: Saved HTML to %s for inspection", htmlFile)
	}
	
	// Extract Instagram images using regex patterns
	return yd.extractInstagramImagesFromHTML(html, url)
}

// extractInstagramImagesFromHTML extracts image URLs from Instagram HTML content
func (yd *YtDlpDownloader) extractInstagramImagesFromHTML(html, originalURL string) ([]*MediaInfo, error) {
	var mediaInfos []*MediaInfo

	// FIRST: Try to extract carousel media if present (modern Instagram structure)
	// Find carousel_media and extract the complete array (with proper bracket matching)
	carouselStart := strings.Index(html, `"carousel_media":`)
	if carouselStart != -1 {
		// Find the opening bracket
		bracketStart := strings.Index(html[carouselStart:], "[")
		if bracketStart != -1 {
			bracketStart += carouselStart

			// Count brackets to find the matching closing bracket
			count := 1
			pos := bracketStart + 1
			for count > 0 && pos < len(html) {
				if html[pos] == '[' {
					count++
				} else if html[pos] == ']' {
					count--
				}
				pos++
			}

			if count == 0 {
				carouselJSON := html[bracketStart:pos]
				log.Printf("🎠 Detected Instagram carousel, extracting all items")

				// Extract all display_uri from carousel items
				displayURIRe := regexp.MustCompile(`"display_uri":"(https:[^"]+\.cdninstagram\.com[^"]*)"`)
				displayURIs := displayURIRe.FindAllStringSubmatch(carouselJSON, -1)

				// Also extract from candidates array for higher quality
				candidatesRe := regexp.MustCompile(`"candidates":\[[^\]]*?"url":"(https:[^"]+\.cdninstagram\.com[^"]+stp=dst-jpg_e35_tt6[^"]*)"`)
				candidateURIs := candidatesRe.FindAllStringSubmatch(carouselJSON, -1)

		// Combine both sources, preferring candidates (higher quality)
		var carouselURLs []string
		seenCarouselURLs := make(map[string]bool)

		// Add candidate URLs first (higher quality, full-size)
		for _, match := range candidateURIs {
			if len(match) > 1 {
				url := strings.ReplaceAll(match[1], `\/`, "/")
				url = strings.ReplaceAll(url, `\u0026`, "&")
				if !seenCarouselURLs[url] {
					seenCarouselURLs[url] = true
					carouselURLs = append(carouselURLs, url)
				}
			}
		}

		// Add display_uri if no candidates found
		if len(carouselURLs) == 0 {
			for _, match := range displayURIs {
				if len(match) > 1 {
					url := strings.ReplaceAll(match[1], `\/`, "/")
					url = strings.ReplaceAll(url, `\u0026`, "&")
					if !seenCarouselURLs[url] {
						seenCarouselURLs[url] = true
						carouselURLs = append(carouselURLs, url)
					}
				}
			}
		}

				if len(carouselURLs) > 0 {
					log.Printf("✅ Found %d carousel items", len(carouselURLs))

					// Extract username and caption
					username := yd.extractInstagramUsernameFromHTML(html)
					caption := yd.extractInstagramCaptionFromHTML(html)

					// Download each carousel image
					for i, imageURL := range carouselURLs {
						uniquePrefix := fmt.Sprintf("%d_%08X", time.Now().Unix(), uint32(i*1000))

						filename, err := yd.downloadImageFromURL(imageURL, uniquePrefix, i)
						if err != nil {
							log.Printf("❌ Failed to download carousel image %d: %v", i+1, err)
							continue
						}

						mediaInfo := &MediaInfo{
							Type:     "photo",
							URL:      originalURL,
							Caption:  FormatAuthorCaption("instagram", "", username, caption),
							Filename: filename,
						}
						mediaInfos = append(mediaInfos, mediaInfo)
					}

					if len(mediaInfos) > 0 {
						return mediaInfos, nil
					}
					log.Printf("⚠️ Carousel detected but no images downloaded successfully")
				}
			}
		}
	}

	// FALLBACK: Try regex patterns for single photos
	// Simple Instagram image patterns - prioritize JSON script data over meta tags
	imagePatterns := []string{
		// HIGHEST PRIORITY: JSON script with image candidates array (contains all sizes including full-size)
		// Note: URLs in JSON have escaped forward slashes (\/)
		`"candidates":\[.*?"url":"(https:\\?/\\?/scontent[^"]*\.cdninstagram\.com\\?/v\\?/t51\.2885-15\\?/[^"]*)"`,
		`"candidates":\[.*?"url":"(https:\\?/\\?/[^"]*\.cdninstagram\.com\\?/v\\?/t51\.2885-15\\?/[^"]*)"`,
		`"candidates":\[.*?"url":"(https:\\?/\\?/scontent[^"]*\.cdninstagram\.com\\?/v\\?/t51\.82787-15\\?/[^"]*)"`,
		`"candidates":\[.*?"url":"(https:\\?/\\?/[^"]*\.cdninstagram\.com\\?/v\\?/t51\.82787-15\\?/[^"]*)"`,
		// IMG tags (good quality)
		`<img[^>]*src="(https://scontent[^"]*\.cdninstagram\.com/[^"]*)"`,
		`<img[^>]*src="(https://[^"]*\.cdninstagram\.com/[^"]*)"`,
		// JSON patterns for display URLs
		`"display_url":"(https://scontent[^"]*\.cdninstagram\.com/[^"]*)"`,
		`"display_url":"(https://[^"]*\.cdninstagram\.com/[^"]*)"`,
		`"display_uri":"(https://scontent[^"]*\.cdninstagram\.com/[^"]*)"`,
		`"display_uri":"(https://[^"]*\.cdninstagram\.com/[^"]*)"`,
		`"url":"(https://scontent[^"]*\.cdninstagram\.com/[^"]*)"`,
		`"src":"(https://scontent[^"]*\.cdninstagram\.com/[^"]*)"`,
		`"display_src":"(https://scontent[^"]*\.cdninstagram\.com/[^"]*)"`,
		// Additional JSON patterns
		`"edge_sidecar_to_children".*?"display_url":"(https://[^"]*\.cdninstagram\.com/[^"]*)"`,
		`window\._sharedData.*?"display_url":"(https://[^"]*\.cdninstagram\.com/[^"]*)"`,
		`"display_resources":\[.*?"src":"(https://[^"]*\.cdninstagram\.com/[^"]*)"`,
		// LOWEST PRIORITY: Meta tag (usually cropped)
		`<meta property="og:image" content="(https://[^"]*\.cdninstagram\.com/[^"]*)"`,
		`"thumbnail_src":"(https://scontent[^"]*\.cdninstagram\.com/[^"]*)"`,
	}
	
	// Use slice to preserve order while avoiding duplicates
	var imageURLs []string
	seenURLs := make(map[string]bool)
	foundHighQuality := false
	
	// Track unique media IDs to detect carousels vs single photos
	uniqueMediaIDs := make(map[string]bool)
	
	for patternIndex, pattern := range imagePatterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindAllStringSubmatch(html, -1)
		for _, match := range matches {
			if len(match) > 1 {
				imageURL := match[1]
				// Decode HTML entities and Unicode escapes
				imageURL = strings.ReplaceAll(imageURL, `\/`, "/") // JSON escaped forward slashes
				imageURL = strings.ReplaceAll(imageURL, `\u0026`, "&")
				imageURL = strings.ReplaceAll(imageURL, `&amp;`, "&")
				imageURL = strings.ReplaceAll(imageURL, `\u002F`, "/")
				imageURL = strings.ReplaceAll(imageURL, `\u003D`, "=")
				imageURL = strings.ReplaceAll(imageURL, `\u003F`, "?")
				
				// Extract media ID from cache key to detect unique images
				mediaIDRe := regexp.MustCompile(`ig_cache_key=([^\\&]+)`)
				if mediaIDMatch := mediaIDRe.FindStringSubmatch(imageURL); len(mediaIDMatch) > 1 {
					mediaID := strings.TrimSuffix(mediaIDMatch[1], `\u00253D\u00253D`)
					mediaID = strings.TrimSuffix(mediaID, `%3D%3D`)
					uniqueMediaIDs[mediaID] = true
				}
				
				// Add to slice if not seen before (preserves order)
				if !seenURLs[imageURL] {
					seenURLs[imageURL] = true
					imageURLs = append(imageURLs, imageURL)
					
					// Log which pattern found this URL and analyze quality
					qualityType := "unknown"
					if patternIndex < 2 {
						// IMG tags are likely higher quality
						qualityType = "HIGH (IMG tag)"
						foundHighQuality = true
					} else if strings.Contains(imageURL, "t51.2885-15") && strings.Contains(imageURL, "dst-jpegr") {
						qualityType = "HIGH (t51.2885-15 + dst-jpegr)"
						foundHighQuality = true
					} else if strings.Contains(imageURL, "t51.2885-15") {
						qualityType = "MEDIUM (t51.2885-15)"
					} else if strings.Contains(imageURL, "t51.75761-15") {
						qualityType = "LOW (t51.75761-15)"
					} else if strings.Contains(imageURL, "display_url") || patternIndex < 7 {
						qualityType = "MEDIUM (JSON)"
					}
					
					log.Printf("🔍 Pattern %d found: %s | Quality: %s | URL: %s", patternIndex, imagePatterns[patternIndex][:50], qualityType, imageURL)
					
					// Mark if we found high quality URLs (first 8 patterns)
					if patternIndex < 8 {
						foundHighQuality = true
					}
				}
			}
		}
		
		// For Instagram carousels, we need to search through all high-priority patterns (0-4) 
		// to find all images before stopping early
		if foundHighQuality && len(imageURLs) > 0 && patternIndex >= 4 {
			log.Printf("✅ Found %d high-quality image(s), stopping search at pattern %d", len(imageURLs), patternIndex)
			break
		}
	}
	
	if len(imageURLs) == 0 {
		// Show a snippet of HTML for debugging
		htmlSnippet := html
		if len(html) > 500 {
			htmlSnippet = html[:500]
		}
		log.Printf("❌ No Instagram images found with patterns. HTML snippet: %s", htmlSnippet)
		return nil, fmt.Errorf("no Instagram images found in HTML")
	}
	
	// Detect if this is a single photo or carousel based on unique media IDs
	log.Printf("🔍 Found %d unique media IDs from %d total URLs", len(uniqueMediaIDs), len(imageURLs))
	
	// If we have multiple URLs but only 1-2 unique media IDs, it's likely a single photo with different sizes
	if len(uniqueMediaIDs) <= 2 && len(imageURLs) > 5 {
		log.Printf("📷 Detected single photo post with %d size variants, selecting best quality image", len(imageURLs))
		
		// Keep only the highest quality version
		var bestImage string
		for _, url := range imageURLs {
			// Prefer high-res jpegr images
			if strings.Contains(url, "dst-jpegr") && strings.Contains(url, "1440x") {
				bestImage = url
				break
			}
		}
		
		// Fallback to first high-quality image if no jpegr found
		if bestImage == "" {
			for _, url := range imageURLs {
				if strings.Contains(url, "t51.2885-15") {
					bestImage = url
					break
				}
			}
		}
		
		// Use first image as last resort
		if bestImage == "" && len(imageURLs) > 0 {
			bestImage = imageURLs[0]
		}
		
		// Replace the array with just the best image
		if bestImage != "" {
			imageURLs = []string{bestImage}
			log.Printf("✅ Selected best quality image from %d variants", len(seenURLs))
		}
	}
	
	// Extract username and caption
	username := yd.extractInstagramUsernameFromHTML(html)
	caption := yd.extractInstagramCaptionFromHTML(html)
	
	// Try to get caption from yt-dlp description file (now with unique filenames to prevent collisions)
	urlHash := strings.ToUpper(fmt.Sprintf("%x", md5.Sum([]byte(originalURL))))[:8]
	descriptionPattern := filepath.Join(yd.TempDir, fmt.Sprintf("*_%s_*.description", urlHash))
	if descFiles, err := filepath.Glob(descriptionPattern); err == nil && len(descFiles) > 0 {
		if content, err := os.ReadFile(descFiles[0]); err == nil {
			ytdlpCaption := strings.TrimSpace(string(content))
			if len(ytdlpCaption) > 0 && len(ytdlpCaption) > len(caption) {
				log.Printf("📝 Using yt-dlp description (%d chars) over HTML caption (%d chars)", len(ytdlpCaption), len(caption))
				caption = ytdlpCaption
			} else {
				log.Printf("📝 HTML caption (%d chars) is better than yt-dlp description (%d chars)", len(caption), len(ytdlpCaption))
			}
		}
	} else {
		log.Printf("📝 No yt-dlp description file found for URL hash %s, using HTML caption", urlHash)
	}
	
	log.Printf("📝 Using final caption: %.100s...", caption)
	
	log.Printf("🎯 Found %d Instagram images, attempting to download", len(imageURLs))
	log.Printf("📝 Final caption length: %d chars", len(caption))
	
	// Debug: show all found URLs with quality assessment (now in correct order)
	for idx, url := range imageURLs {
		qualityType := "unknown"
		if strings.Contains(url, "t51.2885-15") && strings.Contains(url, "dst-jpegr") {
			qualityType = "HIGH (t51.2885-15 + dst-jpegr)"
		} else if strings.Contains(url, "t51.2885-15") {
			qualityType = "MEDIUM (t51.2885-15)"
		} else if strings.Contains(url, "t51.75761-15") {
			qualityType = "LOW (t51.75761-15)"
		}
		log.Printf("🔍 Image %d: %s | Quality: %s", idx+1, url, qualityType)
	}
	
	// Use the ordered slice directly (already in correct sequence)
	imageURLSlice := imageURLs
	
	// Telegram has a limit of 10 images per media group
	const telegramMediaGroupLimit = 10
	if len(imageURLSlice) > telegramMediaGroupLimit {
		log.Printf("⚠️ Instagram post has %d images, but Telegram only supports %d per message. Truncating.", 
			len(imageURLSlice), telegramMediaGroupLimit)
		imageURLSlice = imageURLSlice[:telegramMediaGroupLimit]
	}
	
	log.Printf("📊 Found %d Instagram images in correct order", len(imageURLSlice))
	
	// Download each image directly from CDN
	// Create unique timestamp + URL hash to prevent filename collisions between concurrent posts
	timestamp := time.Now().Unix()
	uniquePrefix := fmt.Sprintf("%d_%s", timestamp, urlHash)
	
	for i, imageURL := range imageURLSlice {
		log.Printf("📥 Downloading Instagram image %d/%d: %s", i+1, len(imageURLSlice), imageURL)
		
		filename, err := yd.downloadImageFromURL(imageURL, uniquePrefix, i)
		if err != nil {
			log.Printf("❌ Failed to download Instagram image %d: %v", i+1, err)
			// Continue with other images instead of failing completely
			continue
		}
		
		log.Printf("✅ Successfully downloaded Instagram image %d: %s", i+1, filename)
		
		// Create MediaInfo for downloaded image
		mediaType := "photo"
		mediaCaption := ""
		if i == 0 && caption != "" {
			// Add caption only to the first image to avoid repetition
			mediaCaption = FormatAuthorCaption("instagram", username, username, caption)
		}
		
		mediaInfo := &MediaInfo{
			Type:     mediaType,
			URL:      originalURL,
			Caption:  mediaCaption,
			Filename: filename,
		}
		mediaInfos = append(mediaInfos, mediaInfo)
	}
	
	// If all downloads failed but we have caption or image URLs, return as fallback
	if len(mediaInfos) == 0 {
		if len(imageURLSlice) > 0 {
			log.Printf("⚠️ All image downloads failed due to Instagram protection, returning URLs as text")
			// Create a message with the direct image URLs for manual access
			fallbackText := ""
			if username != "" {
				fallbackText += fmt.Sprintf("📷 @%s\n\n", username)
			}
			if caption != "" {
				fallbackText += caption + "\n\n"
			}
			fallbackText += "📸 Instagram blocked direct download. Image URLs:\n"
			for i, imageURL := range imageURLSlice {
				fallbackText += fmt.Sprintf("%d. %s\n", i+1, imageURL)
			}
			
			textMediaInfo := &MediaInfo{
				Type:     "text",
				URL:      originalURL,
				Caption:  fallbackText,
				Filename: "", // No file for text-only content
			}
			mediaInfos = append(mediaInfos, textMediaInfo)
		} else if caption != "" {
			log.Printf("⚠️ No images found, returning caption as text fallback")
			textMediaInfo := &MediaInfo{
				Type:     "text",
				URL:      originalURL,
				Caption:  FormatAuthorCaption("instagram", username, username, caption),
				Filename: "", // No file for text-only content
			}
			mediaInfos = append(mediaInfos, textMediaInfo)
		}
	}
	
	if len(mediaInfos) == 0 {
		return nil, fmt.Errorf("no Instagram content found and no images could be downloaded")
	}
	
	log.Printf("🎉 Successfully processed %d Instagram media items", len(mediaInfos))
	return mediaInfos, nil
}

// extractInstagramUsernameFromHTML extracts username from Instagram HTML
func (yd *YtDlpDownloader) extractInstagramUsernameFromHTML(html string) string {
	patterns := []string{
		`"username":"([^"]+)"`,
		`@([a-zA-Z0-9._]+)`,
	}
	
	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		if match := re.FindStringSubmatch(html); len(match) > 1 {
			return match[1]
		}
	}
	
	return "unknown"
}

// extractInstagramCaptionFromHTML extracts caption from Instagram HTML
func (yd *YtDlpDownloader) extractInstagramCaptionFromHTML(html string) string {
	patterns := []string{
		// HIGHEST PRIORITY: Author's first comment (the actual post caption)
		// Pattern for the span containing the first author comment
		`<span[^>]*><a[^>]*href="/[^/]+/"[^>]*>@[^<]+</a>\s*([^<]+(?:<br>[^<]*)*)</span>`,
		`<span[^>]*><a[^>]*>@[^<]+</a>\s*([^<]+(?:<br>[^<]*)*)</span>`,
		// JSON patterns for actual caption content
		`"edge_media_to_caption":\{"edges":\[\{"node":\{"text":"([^"]*)"`,
		`"caption":\{"text":"([^"]*)"`,
		// Try meta description (often contains the full caption)
		`<meta name="description" content="[^"]*: &quot;([^&]*?)&quot;`,
		`<meta name="description" content="[^"]*: \"([^\"]*?)\"`,
		// Fallback patterns
		`"caption":"([^"]*)"`,
		`"text":"([^"]*)".*"shortcode"`,
		// LOWEST PRIORITY: Accessibility caption (usually generic)
		`"accessibility_caption":"([^"]*)"`,
	}
	
	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		if match := re.FindStringSubmatch(html); len(match) > 1 {
			caption := match[1]
			
			// Clean up HTML tags and convert to readable text
			caption = strings.ReplaceAll(caption, `<br>`, "\n")
			caption = strings.ReplaceAll(caption, `<br/>`, "\n")
			caption = strings.ReplaceAll(caption, `<br />`, "\n")
			
			// Remove remaining HTML tags but preserve content
			linkRe := regexp.MustCompile(`<a[^>]*href="/([^/]+)/"[^>]*>@[^<]+</a>`)
			caption = linkRe.ReplaceAllString(caption, "@$1")
			tagRe := regexp.MustCompile(`<[^>]*>`)
			caption = tagRe.ReplaceAllString(caption, "")
			
			// Decode Unicode escape sequences first (e.g., \u26f5 -> ⛵)
			caption = yd.decodeUnicodeEscapes(caption)
			
			// Unescape HTML entities and escape sequences
			caption = strings.ReplaceAll(caption, `&quot;`, `"`)
			caption = strings.ReplaceAll(caption, `&#x26f5;`, `⛵`)
			caption = strings.ReplaceAll(caption, `&#x2013;`, `–`)
			caption = strings.ReplaceAll(caption, `&#x2022;`, `•`)
			caption = strings.ReplaceAll(caption, `&#064;`, `@`)
			caption = strings.ReplaceAll(caption, `&amp;`, `&`)
			caption = strings.ReplaceAll(caption, `&lt;`, `<`)
			caption = strings.ReplaceAll(caption, `&gt;`, `>`)
			caption = strings.ReplaceAll(caption, `\"`, `"`)
			caption = strings.ReplaceAll(caption, `\\`, `\`)
			caption = strings.ReplaceAll(caption, `\n`, "\n")
			
			// Clean up and return if we have substantial content
			caption = strings.TrimSpace(caption)
			if len(caption) > 5 { // Lower threshold for author comments
				return caption
			}
		}
	}
	
	return ""
}

// decodeUnicodeEscapes converts Unicode escape sequences like \u26f5 and surrogate pairs like \ud83d\udcb0 to actual characters
func (yd *YtDlpDownloader) decodeUnicodeEscapes(text string) string {
	// First handle surrogate pairs (\ud83d\udcb0 -> 💰)
	surrogatePairRe := regexp.MustCompile(`\\u([dD][8-9a-bA-B][0-9a-fA-F]{2})\\u([dD][c-fC-F][0-9a-fA-F]{2})`)
	
	result := surrogatePairRe.ReplaceAllStringFunc(text, func(match string) string {
		// Extract high and low surrogate hex codes
		parts := strings.Split(match, `\u`)
		if len(parts) != 3 {
			return match
		}
		
		highHex := parts[1]
		lowHex := parts[2]
		
		// Convert to integers
		high, err1 := strconv.ParseInt(highHex, 16, 32)
		low, err2 := strconv.ParseInt(lowHex, 16, 32)
		
		if err1 != nil || err2 != nil {
			return match
		}
		
		// Convert surrogate pair to Unicode code point
		// Formula: 0x10000 + ((high & 0x3FF) << 10) + (low & 0x3FF)
		codePoint := 0x10000 + ((high&0x3FF)<<10) + (low&0x3FF)
		
		return string(rune(codePoint))
	})
	
	// Then handle regular 4-digit Unicode escapes
	re := regexp.MustCompile(`\\u([0-9a-fA-F]{4})`)
	
	result = re.ReplaceAllStringFunc(result, func(match string) string {
		// Extract the hex code (skip the \u prefix)
		hexCode := match[2:]
		
		// Convert hex string to integer
		if codePoint, err := strconv.ParseInt(hexCode, 16, 32); err == nil {
			// Convert to rune and then to string
			return string(rune(codePoint))
		}
		
		// If conversion fails, return the original match
		return match
	})
	
	return result
}

// downloadImageFromURL downloads an image from a direct URL with retry logic
func (yd *YtDlpDownloader) downloadImageFromURL(imageURL string, uniquePrefix string, index int) (string, error) {
	const maxRetries = 3
	var lastErr error
	
	for attempt := 1; attempt <= maxRetries; attempt++ {
		if attempt > 1 {
			log.Printf("🔄 Retry %d/%d for Instagram image download", attempt, maxRetries)
			time.Sleep(time.Duration(attempt) * time.Second) // Progressive delay
		}
		
		filename, err := yd.attemptImageDownload(imageURL, uniquePrefix, index)
		if err == nil {
			return filename, nil
		}
		
		lastErr = err
		log.Printf("❌ Download attempt %d failed: %v", attempt, err)
	}
	
	return "", fmt.Errorf("failed after %d attempts: %v", maxRetries, lastErr)
}

// attemptImageDownload performs a single download attempt
func (yd *YtDlpDownloader) attemptImageDownload(imageURL string, uniquePrefix string, index int) (string, error) {
	req, err := http.NewRequest("GET", imageURL, nil)
	if err != nil {
		return "", err
	}
	
	// Use minimal headers that browsers actually send for direct image requests
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Referer", "https://www.instagram.com/")
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Pragma", "no-cache")
	
	// Add Instagram cookies for authentication
	if cookieData, err := os.ReadFile(yd.getFilePath("cookies/instagram_cookies.txt")); err == nil {
		// Parse cookies and add them to request
		lines := strings.Split(string(cookieData), "\n")
		for _, line := range lines {
			if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
				continue
			}
			parts := strings.Split(line, "\t")
			if len(parts) >= 7 {
				cookieName := parts[5]
				cookieValue := parts[6]
				// Clean cookie value - remove quotes and handle escape sequences
				cookieValue = strings.Trim(cookieValue, `"`)
				cookieValue = strings.ReplaceAll(cookieValue, `\"`, `"`)
				cookieValue = strings.ReplaceAll(cookieValue, `\054`, `,`)
				cookieValue = strings.ReplaceAll(cookieValue, `\\`, `\`)
				// Skip problematic cookie values that still contain invalid characters
				if cookieName != "" && cookieValue != "" && !strings.Contains(cookieValue, `\`) {
					req.AddCookie(&http.Cookie{Name: cookieName, Value: cookieValue})
				}
			}
		}
	}
	
	resp, err := SharedHTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		// Try to read response body for more detailed error info
		body, _ := io.ReadAll(resp.Body)
		bodyPreview := string(body)
		if len(bodyPreview) > 200 {
			bodyPreview = bodyPreview[:200]
		}
		log.Printf("🔍 Instagram image download failed - Status: %d, Headers: %v, Body preview: %s", 
			resp.StatusCode, resp.Header, bodyPreview)
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}
	
	// Generate filename
	filename := filepath.Join(yd.TempDir, fmt.Sprintf("%s_instagram_carousel_%d.jpg", uniquePrefix, index))
	
	// Create and write file
	file, err := os.Create(filename)
	if err != nil {
		return "", err
	}
	defer file.Close()
	
	_, err = io.Copy(file, resp.Body)
	if err != nil {
		return "", err
	}
	
	return filename, nil
}