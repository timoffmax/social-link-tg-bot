package services

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// SmartDownloader handles media downloads with platform-specific strategies
type SmartDownloader struct {
	ytdlp        *YtDlpDownloader
	browser      *UniversalBrowserExtractor
	kk           *InstagramKKInstagramExtractor
	instagramOG  *InstagramOGExtractor
	reddit       *RedditExtractor
	redditVx     *RedditVxredditExtractor
	gated        GatedPostProvider
	youtubeApify *YouTubeApifyExtractor
	storiesApify *InstagramStoriesApifyExtractor
	youtubePost  *YouTubePostExtractor
	threads      *ThreadsExtractor
	facebook     *FacebookFacebedExtractor
	tempDir      string
	workingDir   string
	// extractYtDlp replaces the yt-dlp method (tests); nil runs yt-dlp.
	extractYtDlp func(mediaURL, platform string) ([]*MediaInfo, error)
}

// lastResortError is a method's failure that still carries a lesser result
// (e.g. a post's text without the video it may hold): the chain goes on, and
// the result is sent only when every other method fails.
type lastResortError struct {
	Media  []*MediaInfo
	Reason string
}

// Error implements error.
func (e *lastResortError) Error() string {
	result := fmt.Sprintf("kept as a last resort: %s", e.Reason)

	return result
}

// lastResortMediaOf returns the result an error keeps as a last resort, or
// current when it keeps none (the first kept result stays).
func lastResortMediaOf(current []*MediaInfo, err error) []*MediaInfo {
	result := current
	var lastResort *lastResortError
	isLastResort := errors.As(err, &lastResort)

	if (result == nil) && (isLastResort == true) {
		result = lastResort.Media
	}

	return result
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
		ytdlp:        NewYtDlpDownloader(),
		browser:      NewUniversalBrowserExtractor(workingDir, tempDir),
		kk:           NewInstagramKKInstagramExtractor(tempDir),
		instagramOG:  NewInstagramOGExtractor(tempDir),
		reddit:       NewRedditExtractor(tempDir),
		redditVx:     NewRedditVxredditExtractor(tempDir),
		gated:        NewGatedPostProvider(tempDir),
		youtubeApify: NewYouTubeApifyExtractor(tempDir),
		storiesApify: NewInstagramStoriesApifyExtractor(tempDir),
		youtubePost:  NewYouTubePostExtractor(tempDir),
		threads:      NewThreadsExtractor(tempDir),
		facebook:     NewFacebookFacebedExtractor(tempDir),
		tempDir:      tempDir,
		workingDir:   workingDir,
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

	strategy.LogStrategy()

	var lastErr error
	var lastResort []*MediaInfo

	// Run the first 2 strategies concurrently if there are at least 2 non-FixedLink
	// methods, unless the strategy demands strict priority ordering.
	concurrentCount := 0

	if strategy.Sequential == false {
		concurrentCount = sd.getConcurrentCount(strategy.Methods)
	}

	if concurrentCount >= 2 {
		result, err := sd.tryConcurrentStrategies(ctx, mediaURL, strategy, startTime)
		if result != nil {
			return result, nil
		}

		if err != nil {
			lastErr = err
		}

		lastResort = lastResortMediaOf(lastResort, err)
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

		lastResort = lastResortMediaOf(lastResort, err)
	}

	totalDuration := time.Since(startTime)
	var chainErr error

	if lastResort != nil {
		log.Printf("📝 Every media method failed for %s, sending the result kept as a last resort", mediaURL)
		LogExtraction(strategy.Platform, mediaURL, "last-resort", true, len(lastResort), totalDuration)
	}

	// If all methods failed and nothing was kept, return error
	if lastResort == nil {
		LogExtraction(strategy.Platform, mediaURL, "all-methods", false, 0, totalDuration)
		chainErr = fmt.Errorf("all extraction methods failed for %s: %w", mediaURL, lastErr)
	}

	return lastResort, chainErr
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
	var lastResortErr error

	for res := range resultCh {
		if res.media != nil {
			concurrentCancel()
			return res.media, nil
		}

		if res.err != nil {
			lastErr = res.err
		}

		isLastResort := (lastResortMediaOf(nil, res.err) != nil)

		if (lastResortErr == nil) && (isLastResort == true) {
			lastResortErr = res.err
		}
	}

	// A result kept as a last resort must reach the caller.
	if lastResortErr != nil {
		lastErr = lastResortErr
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
		mediaInfos, err := sd.runYtDlp(mediaURL, strategy.Platform)
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

	case MethodRedditVxreddit:
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		log.Printf("🟠 Trying vxreddit extraction (datacenter-robust)")
		mediaInfos, err := sd.redditVx.ExtractMedia(mediaURL)
		methodDuration := time.Since(methodStartTime)

		if (err == nil) && (len(mediaInfos) > 0) {
			log.Printf("✅ vxreddit extraction succeeded with %d items", len(mediaInfos))
			LogExtraction(strategy.Platform, mediaURL, "vxreddit", true, len(mediaInfos), time.Since(overallStart))
			appendOriginalURL(mediaInfos, mediaURL)
			return mediaInfos, nil
		}

		log.Printf("❌ vxreddit extraction failed: %v", err)
		LogExtraction(strategy.Platform, mediaURL, "vxreddit", false, 0, methodDuration)
		return nil, err

	case MethodKKInstagram:
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		log.Printf("📥 Trying kkinstagram redirect extraction")
		mediaInfos, err := sd.kk.ExtractMedia(mediaURL)
		methodDuration := time.Since(methodStartTime)

		if (err == nil) && (len(mediaInfos) > 0) {
			log.Printf("✅ kkinstagram extraction succeeded with %d items", len(mediaInfos))
			LogExtraction(strategy.Platform, mediaURL, "kkinstagram", true, len(mediaInfos), time.Since(overallStart))
			appendOriginalURL(mediaInfos, mediaURL)
			return mediaInfos, nil
		}

		log.Printf("❌ kkinstagram extraction failed: %v", err)
		LogExtraction(strategy.Platform, mediaURL, "kkinstagram", false, 0, methodDuration)
		return nil, err

	case MethodYouTubeApify:
		mediaInfos, err := sd.runExtractorMethod(ctx, strategy, mediaURL, "youtube-apify", sd.youtubeApifyExtract, overallStart, methodStartTime)

		return mediaInfos, err

	case MethodStoriesApify:
		mediaInfos, err := sd.runExtractorMethod(ctx, strategy, mediaURL, "stories-apify", sd.storiesApifyExtract, overallStart, methodStartTime)

		return mediaInfos, err

	case MethodYouTubePost:
		mediaInfos, err := sd.runExtractorMethod(ctx, strategy, mediaURL, "youtube-post", sd.youtubePost.ExtractMedia, overallStart, methodStartTime)

		return mediaInfos, err

	case MethodThreadsEmbed:
		mediaInfos, err := sd.runExtractorMethod(ctx, strategy, mediaURL, "threads-embed", sd.threads.ExtractEmbed, overallStart, methodStartTime)

		return mediaInfos, err

	case MethodThreadsVxthreads:
		mediaInfos, err := sd.runExtractorMethod(ctx, strategy, mediaURL, "threads-vxthreads", sd.threads.ExtractVxthreads, overallStart, methodStartTime)

		return mediaInfos, err

	case MethodFacebookFacebed:
		mediaInfos, err := sd.runExtractorMethod(ctx, strategy, mediaURL, "facebed", sd.facebook.ExtractMedia, overallStart, methodStartTime)

		return mediaInfos, err

	case MethodInstagramOG:
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		log.Printf("🖼️ Trying Instagram OG-thumbnail fallback")
		mediaInfos, err := sd.instagramOG.ExtractMedia(mediaURL)
		methodDuration := time.Since(methodStartTime)

		if (err == nil) && (len(mediaInfos) > 0) {
			log.Printf("✅ OG fallback succeeded with %d items", len(mediaInfos))
			LogExtraction(strategy.Platform, mediaURL, "instagram-og", true, len(mediaInfos), time.Since(overallStart))
			appendOriginalURL(mediaInfos, mediaURL)
			return mediaInfos, nil
		}

		log.Printf("❌ OG fallback failed: %v", err)
		LogExtraction(strategy.Platform, mediaURL, "instagram-og", false, 0, methodDuration)
		return nil, err

	case MethodFixedLink:
		return sd.executeFixedLinkStrategy(mediaURL, strategy, overallStart)
	}

	return nil, nil
}

// executeFixedLinkStrategy returns the platform's fixed link as a text
// message; platforms without a fixed link get no result.
func (sd *SmartDownloader) executeFixedLinkStrategy(
	mediaURL string,
	strategy *ExtractionStrategy,
	overallStart time.Time,
) ([]*MediaInfo, error) {
	var result []*MediaInfo
	fixedURL := strategy.GetFixedURL(mediaURL)

	if fixedURL != "" {
		log.Printf("🔗 Returning fixed link for %s", strategy.Platform)
		LogExtraction(strategy.Platform, mediaURL, "fixed-link", true, 1, time.Since(overallStart))
		result = []*MediaInfo{{Type: MediaTypeText, URL: mediaURL, Caption: fixedURL, Filename: ""}}
	}

	return result, nil
}

// runYtDlp runs the yt-dlp method, or its test replacement.
func (sd *SmartDownloader) runYtDlp(mediaURL, platform string) ([]*MediaInfo, error) {
	extract := sd.tryYtDlpExtraction

	if sd.extractYtDlp != nil {
		extract = sd.extractYtDlp
	}

	result, err := extract(mediaURL, platform)

	return result, err
}

// tryYtDlpExtraction attempts to extract media using yt-dlp with platform-specific logic
func (sd *SmartDownloader) tryYtDlpExtraction(url string, platform string) ([]*MediaInfo, error) {
	// Use platform-specific methods that already exist
	switch platform {
	case "twitter":
		return sd.ytdlp.DownloadTwitterMedia(url)
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

// platformDisplayName returns the human-readable platform name for a URL
func platformDisplayName(originalURL string) string {
	result := threadsDisplayName
	isThreads := IsThreadsURL(originalURL)

	if isThreads == false {
		result = otherPlatformDisplayName(originalURL)
	}

	return result
}

// threadsDisplayName is the display name of Threads in "View the original".
const threadsDisplayName = "Threads"

// otherPlatformDisplayName returns the display name of the other platforms.
func otherPlatformDisplayName(originalURL string) string {
	urlLower := strings.ToLower(originalURL)

	switch {
	case strings.Contains(urlLower, "instagram.com") || strings.Contains(urlLower, "kksave.com"):
		return "Instagram"
	case strings.Contains(urlLower, "tiktok.com"):
		return "TikTok"
	case strings.Contains(urlLower, "twitter.com") || strings.Contains(urlLower, "x.com") || strings.Contains(urlLower, "fixupx.com"):
		return "X"
	case strings.Contains(urlLower, "youtube.com") || strings.Contains(urlLower, "youtu.be"):
		return "YouTube"
	case strings.Contains(urlLower, "reddit.com") || strings.Contains(urlLower, "redd.it"):
		return "Reddit"
	case strings.Contains(urlLower, "facebook.com") || strings.Contains(urlLower, "fb.watch"):
		return "Facebook"
	case strings.Contains(urlLower, "9gag.com"):
		return "9GAG"
	default:
		return "source"
	}
}

// FormatOriginalLink returns a formatted "View the original on Platform" HTML link
func FormatOriginalLink(originalURL string) string {
	cleanURL := cleanURLParams(originalURL)
	platform := platformDisplayName(originalURL)

	linkVerb := "View"
	if strings.Contains(strings.ToLower(originalURL), "youtu") ||
		strings.Contains(strings.ToLower(originalURL), "tiktok") {
		linkVerb = "Watch"
	}

	return fmt.Sprintf("\n\n🔗 <a href=\"%s\">%s the original on %s</a>", cleanURL, linkVerb, platform)
}

// appendOriginalURL adds the original URL to caption, keeping the HTML
// <a> suffix intact within Telegram's 1024-byte caption limit. If the body
// plus suffix would exceed the limit, the body is truncated at a rune
// boundary before the suffix is appended, so the closing </a> never gets
// chopped off (which would produce "Can't find end tag" from Telegram).
func appendOriginalURL(mediaInfos []*MediaInfo, originalURL string) {
	urlSuffix := FormatOriginalLink(originalURL)

	for _, mediaInfo := range mediaInfos {
		// Skip text-only media (fixed links)
		if mediaInfo.Type == "text" || mediaInfo.Filename == "" {
			continue
		}

		currentCaption := mediaInfo.Caption

		if len(currentCaption) > 0 {
			mediaInfo.Caption = truncateBodyForSuffix(currentCaption, urlSuffix) + urlSuffix
		} else {
			// No caption, just add URL (remove leading newlines)
			mediaInfo.Caption = strings.TrimPrefix(urlSuffix, "\n\n")
		}
	}
}

// truncateBodyForSuffix returns `body` shortened (at a UTF-8 rune boundary)
// so that len(body) + len(suffix) <= telegramCaptionLimit. An ellipsis is
// added when truncation happens.
func truncateBodyForSuffix(body, suffix string) string {
	const telegramCaptionLimit = 1024
	const ellipsis = "..."

	if len(body)+len(suffix) <= telegramCaptionLimit {
		return body
	}

	maxBody := telegramCaptionLimit - len(suffix) - len(ellipsis)
	if maxBody <= 0 {
		return ""
	}

	if maxBody >= len(body) {
		return body
	}

	// Walk back to a rune boundary so we don't slice mid-codepoint.
	for maxBody > 0 && (body[maxBody]&0xC0) == 0x80 {
		maxBody--
	}

	return body[:maxBody] + ellipsis
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
		"utm_source":   true,
		"utm_medium":   true,
		"utm_campaign": true,
		"utm_term":     true,
		"utm_content":  true,
		"utm_id":       true,

		// Social media tracking
		"t":       true, // Twitter sharing parameter
		"s":       true, // Twitter sharing parameter
		"fbclid":  true, // Facebook click ID
		"gclid":   true, // Google click ID
		"msclkid": true, // Microsoft click ID
		"igshid":  true, // Instagram share ID

		// General tracking
		"ref":      true,
		"ref_src":  true,
		"source":   true,
		"campaign": true,
		"_hsenc":   true, // HubSpot
		"_hsmi":    true, // HubSpot
		"mc_cid":   true, // MailChimp
		"mc_eid":   true, // MailChimp
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

// runExtractorMethod runs one context-aware extractor method (the Apify-backed
// ones and the YouTube post reader) with the chain's usual logging.
// appendOriginalURL is applied by the extractors themselves.
func (sd *SmartDownloader) runExtractorMethod(
	ctx context.Context,
	strategy *ExtractionStrategy,
	mediaURL string,
	methodName string,
	extract func(context.Context, string) ([]*MediaInfo, error),
	overallStart time.Time,
	methodStartTime time.Time,
) ([]*MediaInfo, error) {
	var result []*MediaInfo
	err := ctx.Err()

	if err == nil {
		log.Printf("Trying %s extraction", methodName)
		result, err = extract(ctx, mediaURL)
	}

	isSuccess := ((err == nil) && (len(result) > 0))

	if isSuccess == true {
		total := time.Since(overallStart)
		LogExtraction(strategy.Platform, mediaURL, methodName, true, len(result), total)
	}

	if isSuccess == false {
		methodDuration := time.Since(methodStartTime)
		log.Printf("❌ %s extraction failed: %v", methodName, err)
		LogExtraction(strategy.Platform, mediaURL, methodName, false, 0, methodDuration)
		result = nil
	}

	return result, err
}

// youtubeApifyExtract runs the YouTube actor when Apify is configured.
func (sd *SmartDownloader) youtubeApifyExtract(ctx context.Context, mediaURL string) ([]*MediaInfo, error) {
	var result []*MediaInfo
	err := errApifyNotConfigured

	if sd.youtubeApify != nil {
		result, err = sd.youtubeApify.ExtractMedia(ctx, mediaURL)
	}

	return result, err
}

// storiesApifyExtract runs the stories actor when Apify is configured.
func (sd *SmartDownloader) storiesApifyExtract(ctx context.Context, mediaURL string) ([]*MediaInfo, error) {
	var result []*MediaInfo
	err := errApifyNotConfigured

	if sd.storiesApify != nil {
		result, err = sd.storiesApify.ExtractMedia(ctx, mediaURL)
	}

	return result, err
}
