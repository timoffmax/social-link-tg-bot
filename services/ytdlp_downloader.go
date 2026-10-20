package services

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
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
	// ninegagClient downloads 9GAG's files (tests); nil uses
	// SharedDownloadClient.
	ninegagClient *http.Client
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

// ytdlpOutputTemplate names downloaded files "<download prefix><title>_<id>"
// (see newDownloadPrefix). findDownloadedFiles and removeLeftoverFiles rely on
// the prefix. The title is capped at 100 bytes: Facebook titles are whole
// captions and overflowed the 255-byte file name limit ("Cannot write video
// metadata to JSON file"); the id keeps entries of one post apart.
const ytdlpOutputTemplate = "%s%%(title).100B_%%(id)s.%%(ext)s"

// downloadPrefixTemplate is "<unix ns>-<call number>_<url hash>_".
const downloadPrefixTemplate = "%d-%d_%s_"

// urlHashLength is how many hex digits of the URL's MD5 a prefix keeps.
const urlHashLength = 8

// downloadCounter numbers the download calls, so two calls in the same
// nanosecond still get their own prefix.
var downloadCounter atomic.Uint64

// newDownloadPrefix returns the file name prefix of one download call: unique
// per call (two downloads of the same URL at once must not share or remove
// each other's files) and naming the URL by its hash.
func newDownloadPrefix(url string) string {
	sum := md5.Sum([]byte(url))
	hexSum := fmt.Sprintf("%X", sum)
	callNumber := downloadCounter.Add(1)
	nanos := time.Now().UnixNano()
	result := fmt.Sprintf(downloadPrefixTemplate, nanos, callNumber, hexSum[:urlHashLength])

	return result
}

// downloadOutputTemplate is the yt-dlp output template of a download prefix.
func (yd *YtDlpDownloader) downloadOutputTemplate(prefix string) string {
	name := fmt.Sprintf(ytdlpOutputTemplate, prefix)
	result := filepath.Join(yd.TempDir, name)

	return result
}

// ytDlpTwitterHostPattern matches twitter.com / x.com hosts (not any URL that
// merely contains "x.com", such as netflix.com).
var ytDlpTwitterHostPattern = regexp.MustCompile(`^https?://([a-z0-9-]+\.)*(twitter|x)\.com(/|$)`)

// IsTwitterURL reports whether a URL is on a twitter.com or x.com host.
func IsTwitterURL(url string) bool {
	result := ytDlpTwitterHostPattern.MatchString(url)

	return result
}

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

	// A prefix of its own per call prevents collisions between concurrent downloads
	prefix := newDownloadPrefix(url)
	outputTemplate := yd.downloadOutputTemplate(prefix)

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

// attemptDownload tries to download with a specific proxy (or empty string
// for no proxy). Whatever yt-dlp wrote under this download's name and isn't
// returned for sending (.info.json, .description, skipped thumbnails and
// audio, files of a failed attempt) is removed when it returns.
func (yd *YtDlpDownloader) attemptDownload(url string, outputTemplate string, proxy string) (result []*MediaInfo, err error) {
	defer func() {
		yd.removeLeftoverFiles(outputTemplate, result)
	}()

	// This download's file name prefix, from the output template
	prefix := ytdlpDownloadPrefix(outputTemplate)

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
					return yd.downloadRedditImagesDirectly(imageURLs, url, prefix)
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
	downloadedFiles, err := yd.findDownloadedFiles(prefix)
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

		isNinegag := strings.Contains(url, "9gag.com")

		if isNinegag == true {
			caption = ninegagMetadataCaption(metadataInfo)
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

// Download9GAGMedia tries yt-dlp, then the API helper; the helper and the
// downloads of the files it finds run under ctx (the chain's deadline).
func (yd *YtDlpDownloader) Download9GAGMedia(ctx context.Context, url string) ([]*MediaInfo, error) {
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

	prefix := newDownloadPrefix(url)
	outputTemplate := yd.downloadOutputTemplate(prefix)

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
		apiResult, apiErr := yd.fetch9GAGPostViaAPI(ctx, postID, url, prefix)
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
func (yd *YtDlpDownloader) fetch9GAGPostViaAPI(ctx context.Context, postID, originalURL string, prefix string) ([]*MediaInfo, error) {
	// Find the Python interpreter that has curl_cffi (from yt-dlp's pipx venv)
	pythonPath := yd.find9GAGPython()
	if pythonPath == "" {
		return nil, fmt.Errorf("no Python with curl_cffi found")
	}

	scriptPath := yd.getFilePath("scripts/9gag_fetch.py")
	if _, err := os.Stat(scriptPath); err != nil {
		return nil, fmt.Errorf("9gag_fetch.py not found at %s", scriptPath)
	}

	helperCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	cmd := exec.CommandContext(helperCtx, pythonPath, scriptPath, postID)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("9gag_fetch.py failed: %v", err)
	}

	result, err := yd.build9GAGPost(ctx, output, originalURL, prefix)

	return result, err
}

// build9GAGPost downloads what the API post holds: an article's text and
// files, an Animated post's MP4, or the photo, with the "😂 <title>" caption;
// the files are named with the download's prefix.
func (yd *YtDlpDownloader) build9GAGPost(ctx context.Context, output []byte, originalURL string, prefix string) ([]*MediaInfo, error) {
	var result []*MediaInfo
	post, err := parseNinegagPost(output)
	isArticle := (err == nil) && (isNinegagArticle(post) == true)

	if isArticle == true {
		result, err = yd.download9GAGArticle(ctx, post, originalURL, prefix)
	}

	if (err == nil) && (isArticle == false) {
		result, err = yd.download9GAGSingle(ctx, post, prefix)
	}

	return result, err
}

// download9GAGSingle downloads the MP4 of an Animated post or the photo.
func (yd *YtDlpDownloader) download9GAGSingle(ctx context.Context, post ninegagPost, prefix string) ([]*MediaInfo, error) {
	var result []*MediaInfo
	var filePath string
	var err error
	file, isFound := ninegagMediaFile(post.Type, post.Images)

	if isFound == false {
		err = fmt.Errorf("no downloadable media found in 9GAG post")
	}

	if err == nil {
		filePath, err = yd.download9GAGFile(ctx, file, prefix)
	}

	if err != nil {
		err = fmt.Errorf("failed to download 9GAG %s: %w", file.MediaType, err)
	}

	if err == nil {
		caption := NinegagCaption(post.Title)
		result = []*MediaInfo{{Type: file.MediaType, URL: file.URL, Filename: filePath, Caption: caption}}
		log.Printf("✅ 9GAG %s downloaded via API: %s", file.MediaType, post.Title)
	}

	return result, err
}

// download9GAGArticle returns an article as its text message followed by its
// photos and MP4s in order (sent as albums), a few downloaded at once; files
// that fail are left out.
func (yd *YtDlpDownloader) download9GAGArticle(ctx context.Context, post ninegagPost, originalURL string, prefix string) ([]*MediaInfo, error) {
	var wg sync.WaitGroup
	text := ninegagArticleText(post, originalURL)
	result := []*MediaInfo{{Type: MediaTypeText, URL: originalURL, Caption: text}}
	files := ninegagArticleFiles(post.Article)
	paths := make([]string, len(files))
	fileErrs := make([]error, len(files))
	slots := make(chan struct{}, ninegagDownloadConcurrency)

	for i, file := range files {
		wg.Add(1)

		go func(index int, file ninegagFile) {
			defer wg.Done()

			slots <- struct{}{}
			defer func() { <-slots }()

			extension := ninegagFileExtension(file)
			filename := fmt.Sprintf(ninegagArticleFileTemplate, prefix, (index + 1), extension)
			paths[index], fileErrs[index] = yd.download9GAGFileNamed(ctx, file.URL, filename, file.MediaType)
		}(i, file)
	}

	wg.Wait()

	for i, file := range files {
		if fileErrs[i] != nil {
			log.Printf("⚠️ 9GAG article file %d/%d failed: %v", (i + 1), len(files), fileErrs[i])
		}

		if fileErrs[i] == nil {
			result = append(result, &MediaInfo{Type: file.MediaType, URL: originalURL, Filename: paths[i]})
		}
	}

	log.Printf("✅ 9GAG article via API: %d of %d files, %s", (len(result) - 1), len(files), post.Title)

	return result, nil
}

// ninegagFileExtension is the extension of a downloaded 9GAG file.
func ninegagFileExtension(file ninegagFile) string {
	result := ".jpg"
	isVideo := (MediaTypeVideo == file.MediaType)
	isWebM := strings.HasSuffix(file.URL, ".webm")

	if isVideo == true {
		result = ".mp4"
	}

	if (isVideo == true) && (isWebM == true) {
		result = ".webm"
	}

	return result
}

// download9GAGFile downloads a file from 9GAG CDN as "<prefix>9gag.<ext>".
func (yd *YtDlpDownloader) download9GAGFile(ctx context.Context, file ninegagFile, prefix string) (string, error) {
	extension := ninegagFileExtension(file)
	filename := fmt.Sprintf(ninegagFileTemplate, prefix, extension)
	result, err := yd.download9GAGFileNamed(ctx, file.URL, filename, file.MediaType)

	return result, err
}

// download9GAGFileNamed downloads a file from 9GAG CDN to the temp dir under
// ctx, refusing a URL or redirect off the CDN (https on *.9cache.com only)
// and a file above Telegram's upload limit (a photo above sendPhoto's limit
// is shrunk before sending, see PreparePhotos); a failed download leaves no
// file.
func (yd *YtDlpDownloader) download9GAGFileNamed(ctx context.Context, fileURL string, filename string, mediaType string) (string, error) {
	result := ""
	filePath := filepath.Join(yd.TempDir, filename)
	limitFor := func(mimeType string) int64 {
		return TelegramUploadLimitBytes
	}
	request := mediaRequest{URL: fileURL, Headers: ninegagDownloadHeaders, Client: yd.ninegagClient, CheckURL: checkNinegagMediaURL, LimitFor: limitFor}
	download, err := downloadMediaCapped(ctx, request, filePath)

	if err != nil {
		err = fmt.Errorf("9GAG %s download failed: %w", mediaType, err)
	}

	if err == nil {
		log.Printf("📥 Downloaded 9GAG %s: %s (%d bytes)", mediaType, filename, download.Size)
		result = filePath
	}

	return result, err
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

// downloadThumbnailsOnly tries to download only thumbnails and metadata for
// posts; the files it doesn't return are removed (see attemptDownload).
func (yd *YtDlpDownloader) downloadThumbnailsOnly(url string, platform string) (result []*MediaInfo, err error) {
	// Ensure temp directory exists
	if err := os.MkdirAll(yd.TempDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %v", err)
	}

	// A prefix of its own per call prevents collisions between concurrent downloads
	prefix := newDownloadPrefix(url)
	outputTemplate := yd.downloadOutputTemplate(prefix)

	log.Printf("Trying thumbnails-only download for %s with template: %s", platform, outputTemplate)

	defer func() {
		yd.removeLeftoverFiles(outputTemplate, result)
	}()

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
	downloadedFiles, err := yd.findDownloadedFiles(prefix)
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

// ninegagMetadataCaption returns the "😂 <title>" caption from yt-dlp's
// info.json ("" without a title).
func ninegagMetadataCaption(metadataInfo map[string]interface{}) string {
	title, _ := metadataInfo["title"].(string)
	result := NinegagCaption(title)

	return result
}

// ytdlpDownloadPrefix returns the download prefix ("<unix ns>-<n>_<url
// hash>_") of the file names an output template gives ("" when the template
// has another shape).
func ytdlpDownloadPrefix(outputTemplate string) string {
	result := ""
	base := filepath.Base(outputTemplate)
	parts := strings.SplitN(base, "_", 3)

	if len(parts) == 3 {
		result = fmt.Sprintf("%s_%s_", parts[0], parts[1])
	}

	return result
}

// removeLeftoverFiles removes the temp files of this download (named by its
// output template) that are not kept for sending.
func (yd *YtDlpDownloader) removeLeftoverFiles(outputTemplate string, kept []*MediaInfo) {
	prefix := ytdlpDownloadPrefix(outputTemplate)
	keptFiles := map[string]bool{}
	entries, err := os.ReadDir(yd.TempDir)

	for _, item := range kept {
		keptFiles[item.Filename] = true
	}

	if (err == nil) && (prefix != "") {
		for _, entry := range entries {
			name := entry.Name()
			path := filepath.Join(yd.TempDir, name)
			isLeftover := (strings.HasPrefix(name, prefix) == true) && (keptFiles[path] == false) && (entry.IsDir() == false)

			if isLeftover == true {
				log.Printf("🧹 Removing yt-dlp leftover %s", name)
				os.Remove(path)
			}
		}
	}
}

// findDownloadedFiles returns the files of one download (named with its
// prefix).
func (yd *YtDlpDownloader) findDownloadedFiles(prefix string) ([]string, error) {
	var files []string

	err := filepath.Walk(yd.TempDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

		if !info.IsDir() {
			filename := info.Name()
			if (prefix != "") && (strings.HasPrefix(filename, prefix) == true) {
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
func (yd *YtDlpDownloader) downloadRedditImagesDirectly(imageURLs []string, originalURL string, prefix string) ([]*MediaInfo, error) {
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
		name := fmt.Sprintf("%sreddit_image_%d.%s", prefix, (i + 1), ext)
		filename := filepath.Join(yd.TempDir, name)

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
