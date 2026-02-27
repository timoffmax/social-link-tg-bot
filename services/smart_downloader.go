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
	"strings"
	"sync"
	"time"

	"github.com/timoffmax/social-link-bot/config"
)

// SmartDownloader handles media downloads with platform-specific strategies
type SmartDownloader struct {
	ytdlp    *YtDlpDownloader
	browser  *UniversalBrowserExtractor
	embedez  *EmbedezExtractor
	stories  *InstagramStoriesExtractor
	reddit   *RedditExtractor
	tempDir  string
	workingDir string
}

// NewSmartDownloader creates a new smart downloader instance
func NewSmartDownloader() *SmartDownloader {
	workingDir, err := os.Getwd()
	if err != nil {
		log.Printf("Failed to get working directory: %v", err)
		workingDir = "."
	}
	
	tempDir := filepath.Join(workingDir, "temp")
	os.MkdirAll(tempDir, 0755)
	
	return &SmartDownloader{
		ytdlp:      NewYtDlpDownloader(),
		browser:    NewUniversalBrowserExtractor(workingDir, tempDir),
		embedez:    NewEmbedezExtractor(),
		stories:    NewInstagramStoriesExtractor(tempDir),
		reddit:     NewRedditExtractor(tempDir),
		tempDir:    tempDir,
		workingDir: workingDir,
	}
}

// DownloadMedia downloads media using the optimal strategy for the platform.
// Enforces a 90-second overall deadline to prevent long hangs.
func (sd *SmartDownloader) DownloadMedia(mediaURL string) ([]*MediaInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	return sd.downloadMediaWithContext(ctx, mediaURL)
}

// downloadMediaWithContext performs the actual download logic under a context deadline.
func (sd *SmartDownloader) downloadMediaWithContext(ctx context.Context, mediaURL string) ([]*MediaInfo, error) {
	startTime := time.Now()

	// Get the extraction strategy for this URL
	strategy := GetExtractionStrategy(mediaURL)

	// Instagram video optimization: use embedez extractor when cookies are not available
	// BUT skip this if we're in direct link mode (only MethodFixedLink)
	if strategy.Platform == "instagram" && strategy.HasFixedLink {
		result, err := sd.tryInstagramVideoShortcut(ctx, mediaURL, strategy, startTime)
		if result != nil || err != nil {
			return result, err
		}
	}

	strategy.LogStrategy()

	var lastErr error

	// Run the first 2 strategies concurrently if there are at least 2 non-FixedLink methods
	concurrentCount := sd.getConcurrentCount(strategy.Methods)

	if concurrentCount >= 2 {
		result, err := sd.tryConcurrentStrategies(ctx, mediaURL, strategy, startTime)
		if result != nil {
			return result, nil
		}

		if err != nil {
			lastErr = err
		}
	}

	// Sequential fallback for remaining strategies (or all if concurrency was not used)
	startIdx := 0

	if concurrentCount >= 2 {
		startIdx = concurrentCount
	}

	for i := startIdx; i < len(strategy.Methods); i++ {
		if ctx.Err() != nil {
			log.Printf("⏰ Overall 90s deadline exceeded for %s, aborting remaining strategies", mediaURL)
			break
		}

		result, err := sd.executeStrategy(ctx, strategy.Methods[i], mediaURL, strategy, startTime)
		if result != nil {
			return result, nil
		}

		if err != nil {
			lastErr = err
		}
	}

	// If all methods failed, return error
	totalDuration := time.Since(startTime)
	LogExtraction(strategy.Platform, mediaURL, "all-methods", false, 0, totalDuration)

	return nil, fmt.Errorf("all extraction methods failed for %s: %v", mediaURL, lastErr)
}

// getConcurrentCount returns how many of the first strategies can run concurrently.
// Only non-FixedLink methods are candidates for concurrent execution.
func (sd *SmartDownloader) getConcurrentCount(methods []ExtractionMethod) int {
	count := 0

	for _, m := range methods {
		if m == MethodFixedLink {
			break
		}

		count++

		if count >= 2 {
			break
		}
	}

	return count
}

// tryConcurrentStrategies runs the first N strategies in parallel, returning the first success.
func (sd *SmartDownloader) tryConcurrentStrategies(
	ctx context.Context,
	mediaURL string,
	strategy *ExtractionStrategy,
	startTime time.Time,
) ([]*MediaInfo, error) {
	concurrentCount := sd.getConcurrentCount(strategy.Methods)

	type strategyResult struct {
		media []*MediaInfo
		err   error
	}

	resultCh := make(chan strategyResult, concurrentCount)
	concurrentCtx, concurrentCancel := context.WithCancel(ctx)
	defer concurrentCancel()

	log.Printf("🏎️ Running first %d strategies concurrently for %s", concurrentCount, strategy.Platform)

	var wg sync.WaitGroup

	for i := 0; i < concurrentCount; i++ {
		wg.Add(1)

		go func(method ExtractionMethod) {
			defer wg.Done()
			result, err := sd.executeStrategy(concurrentCtx, method, mediaURL, strategy, startTime)
			resultCh <- strategyResult{media: result, err: err}
		}(strategy.Methods[i])
	}

	// Close result channel when all goroutines complete
	go func() {
		wg.Wait()
		close(resultCh)
	}()

	var lastErr error

	for res := range resultCh {
		if res.media != nil {
			concurrentCancel()
			return res.media, nil
		}

		if res.err != nil {
			lastErr = res.err
		}
	}

	return nil, lastErr
}

// executeStrategy runs a single extraction strategy and returns results.
func (sd *SmartDownloader) executeStrategy(
	ctx context.Context,
	method ExtractionMethod,
	mediaURL string,
	strategy *ExtractionStrategy,
	overallStart time.Time,
) ([]*MediaInfo, error) {
	methodStartTime := time.Now()

	switch method {
	case MethodYtDlp:
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		log.Printf("🎯 Trying yt-dlp extraction for %s", strategy.Platform)
		mediaInfos, err := sd.tryYtDlpExtraction(mediaURL, strategy.Platform)
		methodDuration := time.Since(methodStartTime)

		if err == nil && len(mediaInfos) > 0 {
			log.Printf("✅ yt-dlp extraction succeeded with %d items", len(mediaInfos))
			LogExtraction(strategy.Platform, mediaURL, "yt-dlp", true, len(mediaInfos), time.Since(overallStart))
			appendOriginalURL(mediaInfos, mediaURL)
			return mediaInfos, nil
		}

		log.Printf("❌ yt-dlp extraction failed: %v", err)
		LogExtraction(strategy.Platform, mediaURL, "yt-dlp", false, 0, methodDuration)
		return nil, err

	case MethodBrowser:
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		log.Printf("🌐 Trying browser extraction for %s", strategy.Platform)
		mediaInfos, err := sd.browser.ExtractMediaWithContext(ctx, mediaURL)
		methodDuration := time.Since(methodStartTime)

		if err == nil && len(mediaInfos) > 0 {
			log.Printf("✅ Browser extraction succeeded with %d items", len(mediaInfos))
			LogExtraction(strategy.Platform, mediaURL, "browser", true, len(mediaInfos), time.Since(overallStart))
			appendOriginalURL(mediaInfos, mediaURL)
			return mediaInfos, nil
		}

		log.Printf("❌ Browser extraction failed: %v", err)
		LogExtraction(strategy.Platform, mediaURL, "browser", false, 0, methodDuration)
		return nil, err

	case MethodStoriesAPI:
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		log.Printf("📖 Trying Instagram Stories API extraction")
		mediaInfos, err := sd.stories.ExtractStory(mediaURL)
		methodDuration := time.Since(methodStartTime)

		if err == nil && len(mediaInfos) > 0 {
			log.Printf("✅ Stories API extraction succeeded with %d items", len(mediaInfos))
			LogExtraction(strategy.Platform, mediaURL, "stories-api", true, len(mediaInfos), time.Since(overallStart))
			appendOriginalURL(mediaInfos, mediaURL)
			return mediaInfos, nil
		}

		log.Printf("❌ Stories API extraction failed: %v", err)
		LogExtraction(strategy.Platform, mediaURL, "stories-api", false, 0, methodDuration)
		return nil, err

	case MethodRedditAPI:
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		log.Printf("🟠 Trying Reddit JSON API extraction")
		mediaInfos, err := sd.reddit.ExtractRedditPost(mediaURL)
		methodDuration := time.Since(methodStartTime)

		if (err == nil) && (len(mediaInfos) > 0) {
			log.Printf("✅ Reddit API extraction succeeded with %d items", len(mediaInfos))
			LogExtraction(strategy.Platform, mediaURL, "reddit-api", true, len(mediaInfos), time.Since(overallStart))
			appendOriginalURL(mediaInfos, mediaURL)
			return mediaInfos, nil
		}

		log.Printf("❌ Reddit API extraction failed: %v", err)
		LogExtraction(strategy.Platform, mediaURL, "reddit-api", false, 0, methodDuration)
		return nil, err

	case MethodFixedLink:
		return sd.executeFixedLinkStrategy(ctx, mediaURL, strategy, overallStart, methodStartTime)
	}

	return nil, nil
}

// executeFixedLinkStrategy handles the MethodFixedLink strategy.
func (sd *SmartDownloader) executeFixedLinkStrategy(
	ctx context.Context,
	mediaURL string,
	strategy *ExtractionStrategy,
	overallStart time.Time,
	methodStartTime time.Time,
) ([]*MediaInfo, error) {
	if !strategy.HasFixedLink {
		return nil, nil
	}

	log.Printf("🔗 Trying fixed link approach for %s", strategy.Platform)
	internalFixedURL := strategy.GetInternalFixedURL(mediaURL)
	userFixedURL := strategy.GetFixedURL(mediaURL)
	methodDuration := time.Since(methodStartTime)

	if internalFixedURL == "" {
		return nil, nil
	}

	// For Instagram, check if we should skip download attempts
	if strategy.Platform == "instagram" {
		return sd.executeInstagramFixedLink(ctx, mediaURL, strategy, internalFixedURL, userFixedURL, methodDuration)
	}

	// For other platforms, return fixed link as text
	LogExtraction(strategy.Platform, mediaURL, "fixed-link", true, 1, time.Since(overallStart))

	return []*MediaInfo{{
		Type:     "text",
		URL:      mediaURL,
		Caption:  userFixedURL,
		Filename: "",
	}}, nil
}

// executeInstagramFixedLink handles Instagram-specific fixed link logic.
func (sd *SmartDownloader) executeInstagramFixedLink(
	ctx context.Context,
	mediaURL string,
	strategy *ExtractionStrategy,
	internalFixedURL string,
	userFixedURL string,
	methodDuration time.Duration,
) ([]*MediaInfo, error) {
	// If the ONLY method is FixedLink, skip download attempts and return link immediately
	if len(strategy.Methods) == 1 && strategy.Methods[0] == MethodFixedLink {
		log.Printf("📎 Direct link mode - returning %s link immediately: %s", config.GetInstagramService(), userFixedURL)
		LogExtraction(strategy.Platform, mediaURL, "fixed-link-direct", true, 1, methodDuration)

		return []*MediaInfo{{
			Type:     "text",
			URL:      mediaURL,
			Caption:  userFixedURL,
			Filename: "",
		}}, nil
	}

	log.Printf("🎯 Attempting to download media from Instagram fixed link: %s", internalFixedURL)

	// Try yt-dlp on the internal fixed link (ddinstagram)
	fixedMediaInfos, fixedErr := sd.ytdlp.DownloadMedia(internalFixedURL)
	if fixedErr == nil && len(fixedMediaInfos) > 0 {
		if hasActualMediaFiles(fixedMediaInfos) {
			log.Printf("✅ Fixed link extraction succeeded with %d media items", len(fixedMediaInfos))
			LogExtraction(strategy.Platform, mediaURL, "fixed-link-media", true, len(fixedMediaInfos), methodDuration)
			appendOriginalURL(fixedMediaInfos, mediaURL)
			return fixedMediaInfos, nil
		}
	}

	var lastErr error

	if fixedErr != nil {
		if strings.Contains(fixedErr.Error(), "login required") ||
			strings.Contains(fixedErr.Error(), "rate-limit") ||
			strings.Contains(fixedErr.Error(), "authentication") {
			log.Printf("⚠️ Instagram requires authentication - cookies needed")
			lastErr = fmt.Errorf("Instagram requires authentication. Please enable Instagram cookies in .env (ENABLE_INSTAGRAM_COOKIES=true) and ensure valid cookies are available")
		}
	}

	// If yt-dlp failed, try browser extraction on the internal fixed link
	if ctx.Err() == nil {
		log.Printf("🌐 Trying browser extraction on fixed link")
		browserMediaInfos, browserErr := sd.browser.ExtractMediaWithContext(ctx, internalFixedURL)

		if browserErr == nil && len(browserMediaInfos) > 0 {
			if hasActualMediaFiles(browserMediaInfos) {
				log.Printf("✅ Fixed link browser extraction succeeded with %d media items", len(browserMediaInfos))
				LogExtraction(strategy.Platform, mediaURL, "fixed-link-browser", true, len(browserMediaInfos), methodDuration)
				appendOriginalURL(browserMediaInfos, mediaURL)
				return browserMediaInfos, nil
			}
		}
	}

	// For Instagram videos, only return fixed link if we know it can resolve
	isVideo := strings.Contains(mediaURL, "/reel/") || strings.Contains(mediaURL, "/tv/") || strings.Contains(mediaURL, "/reels/")

	if isVideo {
		if lastErr == nil || !strings.Contains(lastErr.Error(), "authentication") {
			log.Printf("❌ Instagram video extraction failed - authentication required")
			lastErr = fmt.Errorf("Instagram video requires authentication. Enable cookies: ENABLE_INSTAGRAM_COOKIES=true")
		}

		return nil, lastErr
	}

	// For photos/carousels, return the user-facing fixed link
	log.Printf("📎 Returning fixed link for Instagram photo/carousel: %s", userFixedURL)
	LogExtraction(strategy.Platform, mediaURL, "fixed-link-text", true, 1, methodDuration)

	return []*MediaInfo{{
		Type:     "text",
		URL:      mediaURL,
		Caption:  userFixedURL,
		Filename: "",
	}}, nil
}

// tryInstagramVideoShortcut handles the Instagram video embedez shortcut.
// Returns (nil, nil) if the shortcut does not apply.
func (sd *SmartDownloader) tryInstagramVideoShortcut(
	ctx context.Context,
	mediaURL string,
	strategy *ExtractionStrategy,
	startTime time.Time,
) ([]*MediaInfo, error) {
	isDirectLinkMode := len(strategy.Methods) == 1 && strategy.Methods[0] == MethodFixedLink
	isVideo := strings.Contains(mediaURL, "/reel/") || strings.Contains(mediaURL, "/tv/") || strings.Contains(mediaURL, "/reels/")

	if !isVideo || sd.ytdlp.HasInstagramCookies() || isDirectLinkMode {
		return nil, nil
	}

	log.Printf("🚀 Instagram video without cookies - using embedez extractor")

	// Try embedez extractor first
	videoURL, err := sd.embedez.ExtractInstagramVideo(mediaURL)
	if err == nil && videoURL != "" {
		log.Printf("✅ Embedez extracted video URL: %s", videoURL)

		filename := fmt.Sprintf("instagram_video_%d.mp4", time.Now().Unix())
		videoFilePath := filepath.Join(sd.tempDir, filename)

		err = sd.downloadVideoFromURL(videoURL, videoFilePath)
		if err == nil {
			log.Printf("✅ Successfully downloaded Instagram video via embedez")
			LogExtraction(strategy.Platform, mediaURL, "embedez-direct", true, 1, time.Since(startTime))

			return []*MediaInfo{{
				Type:     "video",
				URL:      mediaURL,
				Caption:  "Downloaded from Instagram",
				Filename: videoFilePath,
			}}, nil
		}

		log.Printf("⚠️ Failed to download video from embedez URL: %v", err)
	}

	// Fallback: Try yt-dlp with fixed service URL
	service := config.GetInstagramService()
	log.Printf("🔄 Falling back to yt-dlp with %s URL", service)
	fixedServiceURL := strategy.GetFixedURL(mediaURL)
	ezMediaInfos, ezErr := sd.ytdlp.DownloadMedia(fixedServiceURL)

	if ezErr == nil && len(ezMediaInfos) > 0 {
		if hasActualMediaFiles(ezMediaInfos) {
			log.Printf("✅ %s extraction via yt-dlp succeeded with %d media items", service, len(ezMediaInfos))
			LogExtraction(strategy.Platform, mediaURL, service+"-ytdlp", true, len(ezMediaInfos), time.Since(startTime))
			appendOriginalURL(ezMediaInfos, mediaURL)
			return ezMediaInfos, nil
		}
	}

	// Last resort: just send the fixed service link
	serviceName := config.GetInstagramService()
	log.Printf("📎 Sending %s link as fallback: %s", serviceName, fixedServiceURL)
	LogExtraction(strategy.Platform, mediaURL, serviceName+"-link", true, 1, time.Since(startTime))

	return []*MediaInfo{{
		Type:     "text",
		URL:      mediaURL,
		Caption:  fixedServiceURL,
		Filename: "",
	}}, nil
}

// hasActualMediaFiles checks if any media items contain actual files (not text-only).
func hasActualMediaFiles(mediaInfos []*MediaInfo) bool {
	for _, media := range mediaInfos {
		if media.Type != "text" && media.Filename != "" {
			return true
		}
	}

	return false
}

// tryYtDlpExtraction attempts to extract media using yt-dlp with platform-specific logic
func (sd *SmartDownloader) tryYtDlpExtraction(url string, platform string) ([]*MediaInfo, error) {
	// Use platform-specific methods that already exist
	switch platform {
	case "twitter":
		return sd.ytdlp.DownloadTwitterMedia(url)
	case "instagram":
		return sd.ytdlp.DownloadInstagramMedia(url)
	case "tiktok":
		return sd.ytdlp.DownloadTikTokMedia(url)
	case "youtube":
		return sd.ytdlp.DownloadYouTubeMedia(url)
	case "reddit":
		return sd.ytdlp.DownloadRedditMedia(url)
	case "facebook":
		return sd.ytdlp.DownloadFacebookMedia(url)
	case "9gag":
		return sd.ytdlp.Download9GAGMedia(url)
	default:
		return sd.ytdlp.DownloadMedia(url)
	}
}

// IsAvailable checks if the downloader is available
func (sd *SmartDownloader) IsAvailable() bool {
	return sd.ytdlp.IsAvailable()
}

// CleanupFile removes a temporary file
func (sd *SmartDownloader) CleanupFile(filename string) {
	sd.ytdlp.CleanupFile(filename)
}

// GetWorkingDir returns the working directory
func (sd *SmartDownloader) GetWorkingDir() string {
	return sd.workingDir
}

// GetTempDir returns the temp directory
func (sd *SmartDownloader) GetTempDir() string {
	return sd.tempDir
}

// appendOriginalURL adds the original URL to caption
// Note: Does NOT truncate - lets the caller (updates.go) handle long captions
// This allows Twitter long text handling to work properly
func appendOriginalURL(mediaInfos []*MediaInfo, originalURL string) {
	cleanURL := cleanURLParams(originalURL)
	urlSuffix := fmt.Sprintf("\n\n🔗 %s", cleanURL)

	for _, mediaInfo := range mediaInfos {
		// Skip text-only media (fixed links)
		if mediaInfo.Type == "text" || mediaInfo.Filename == "" {
			continue
		}

		currentCaption := mediaInfo.Caption

		if len(currentCaption) > 0 {
			// Append URL to caption - don't truncate here
			mediaInfo.Caption = currentCaption + urlSuffix
		} else {
			// No caption, just add URL (remove leading newlines)
			mediaInfo.Caption = strings.TrimPrefix(urlSuffix, "\n\n")
		}
	}
}

// downloadVideoFromURL downloads a video file from a direct URL
func (sd *SmartDownloader) downloadVideoFromURL(videoURL, filepath string) error {
	resp, err := SharedDownloadClient.Get(videoURL)
	if err != nil {
		return fmt.Errorf("failed to fetch video: %v", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status code: %d", resp.StatusCode)
	}
	
	out, err := os.Create(filepath)
	if err != nil {
		return fmt.Errorf("failed to create file: %v", err)
	}
	defer out.Close()
	
	_, err = io.Copy(out, resp.Body)
	if err != nil {
		return fmt.Errorf("failed to write file: %v", err)
	}
	
	return nil
}

// cleanURLParams removes tracking parameters and UTM tags from URLs
func cleanURLParams(originalURL string) string {
	parsedURL, err := url.Parse(originalURL)
	if err != nil {
		// If parsing fails, return original URL
		return originalURL
	}

	// List of common tracking parameters to remove
	trackingParams := map[string]bool{
		// UTM parameters
		"utm_source":    true,
		"utm_medium":    true,
		"utm_campaign":  true,
		"utm_term":      true,
		"utm_content":   true,
		"utm_id":        true,
		
		// Social media tracking
		"t":             true, // Twitter sharing parameter
		"s":             true, // Twitter sharing parameter
		"fbclid":        true, // Facebook click ID
		"gclid":         true, // Google click ID
		"msclkid":       true, // Microsoft click ID
		"igshid":        true, // Instagram share ID
		
		// General tracking
		"ref":           true,
		"ref_src":       true,
		"source":        true,
		"campaign":      true,
		"_hsenc":        true, // HubSpot
		"_hsmi":         true, // HubSpot
		"mc_cid":        true, // MailChimp
		"mc_eid":        true, // MailChimp
	}

	// Get query parameters
	values := parsedURL.Query()
	
	// Remove tracking parameters
	for param := range trackingParams {
		values.Del(param)
	}
	
	// Set cleaned query parameters
	parsedURL.RawQuery = values.Encode()
	
	return parsedURL.String()
}