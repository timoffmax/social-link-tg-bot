package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/timoffmax/social-link-bot/config"
)

// YouTubeApifyExtractor downloads YouTube videos through an Apify actor. YouTube
// walls the server's datacenter IP ("Sign in to confirm you're not a bot"), so
// yt-dlp fails there; the actor downloads elsewhere and returns a signed link
// to the file in Apify storage.
type YouTubeApifyExtractor struct {
	tempDir string
	actor   string
	api     *apifyClient
}

const (
	// youtubeApifyServiceName is the outage-tracker key of the actor.
	youtubeApifyServiceName = "apify-youtube"

	// youtubeApifyRunTimeout bounds the run (a 2-minute Short took ~12s).
	youtubeApifyRunTimeout = 120 * time.Second

	// youtubeApifyMaxChargeUsd caps one run: ~15 minutes at 360p
	// ($0.01/minute) or ~10 at 480p.
	youtubeApifyMaxChargeUsd = 0.15

	// Shorts are requested at 480p; a file over the 50MB upload limit is
	// retried once at 360p.
	youtubeQualityShorts   = "480"
	youtubeQualityFallback = "360"

	youtubeApifyFormat        = "default"
	youtubeApifyProxyDisabled = "disabled"
	youtubeApifyStatusSuccess = "success"
	youtubeShortsPath         = "/shorts/"
	youtubePlatform           = "youtube"
	youtubeApifyFilePrefix    = "youtube_apify"
	youtubeApifyFileExt       = ".mp4"
)

// youtubeErrorFileTooLarge is the actor's own over-size error code.
const youtubeErrorFileTooLarge = "FILE_TOO_LARGE"

// youtubeVideoURLPattern matches Shorts, the only videos the bot handles:
// regular videos are not detected, and long ones would waste paid runs.
var youtubeVideoURLPattern = regexp.MustCompile(`youtube\.com/shorts/[A-Za-z0-9_-]{6,}`)

// errYouTubeTooLarge marks a video whose file exceeds Telegram's upload limit
// (a property of the video, not a service failure).
var errYouTubeTooLarge = errors.New("video file exceeds the Telegram upload limit")

// youtubeContentErrors are actor error codes about the video itself (not the
// service): they don't feed the outage tracker.
var youtubeContentErrors = map[string]bool{
	"VIDEO_UNAVAILABLE":    true,
	"PRIVATE_VIDEO":        true,
	"AGE_RESTRICTED":       true,
	"LIVE_STREAM":          true,
	"PREMIERE_NOT_STARTED": true,
	"COPYRIGHT_BLOCKED":    true,
	"FILE_TOO_LARGE":       true,
	"QUALITY_UNAVAILABLE":  true,
}

// youtubeApifyURL is one entry of the actor's urls input.
type youtubeApifyURL struct {
	URL string `json:"url"`
}

// youtubeApifyInput is the actor input for one video.
type youtubeApifyInput struct {
	URLs                 []youtubeApifyURL `json:"urls"`
	Quality              string            `json:"quality"`
	Format               string            `json:"format"`
	ResidentialProxyMode string            `json:"residentialProxyMode"`
}

// youtubeApifyItem is the part of a dataset row the bot uses.
type youtubeApifyItem struct {
	Title         string `json:"title"`
	FileSizeBytes int64  `json:"fileSizeBytes"`
	Uploader      string `json:"uploader"`
	DownloadURL   string `json:"downloadUrl"`
	Status        string `json:"status"`
	ErrorCode     string `json:"errorCode"`
	Error         string `json:"error"`
}

// NewYouTubeApifyExtractor returns the extractor, or nil without APIFY_TOKEN.
func NewYouTubeApifyExtractor(tempDir string) *YouTubeApifyExtractor {
	var result *YouTubeApifyExtractor
	api := newApifyClient()

	if api != nil {
		result = &YouTubeApifyExtractor{tempDir: tempDir, actor: config.GetApifyYouTubeActor(), api: api}
	}

	return result
}

// ExtractMedia downloads a Short at 480p; one too large is retried once at
// 360p while ctx has time left. Only Shorts URLs are sent to the paid actor.
func (e *YouTubeApifyExtractor) ExtractMedia(ctx context.Context, youtubeURL string) ([]*MediaInfo, error) {
	var result []*MediaInfo
	var item youtubeApifyItem
	err := checkYouTubeVideoURL(youtubeURL)
	quality := youtubeQualityShorts
	isVideoURL := (err == nil)

	if isVideoURL == true {
		item, err = e.runActor(ctx, youtubeURL, quality)
	}

	isTooLarge := (errors.Is(err, errYouTubeTooLarge) == true) || (item.ErrorCode == youtubeErrorFileTooLarge)
	isRetryable := (isTooLarge == true) && (quality != youtubeQualityFallback)

	if isRetryable == true {
		log.Printf("📉 [apify-youtube] %sp is over the upload limit, retrying at %sp", quality, youtubeQualityFallback)
		item, err = e.runActor(ctx, youtubeURL, youtubeQualityFallback)
	}

	if err == nil {
		result, err = e.download(item, youtubeURL)
	}

	if (err != nil) && (isVideoURL == true) && (isYouTubeServiceFailure(err, item) == true) {
		detail := err.Error()
		ReportServiceFailure(youtubeApifyServiceName, detail)
	}

	if err == nil {
		ReportServiceSuccess(youtubeApifyServiceName)
		appendOriginalURL(result, youtubeURL)
	}

	return result, err
}

// isYouTubeServiceFailure reports failures of the service itself: not the
// video (private, too large...), not the budget, not the bot's deadline.
func isYouTubeServiceFailure(err error, item youtubeApifyItem) bool {
	isContentError := (youtubeContentErrors[item.ErrorCode] == true) || (errors.Is(err, errYouTubeTooLarge) == true)
	isDeadline := errors.Is(err, context.DeadlineExceeded)
	result := (isContentError == false) && (isApifyBudgetError(err) == false) && (isDeadline == false)

	return result
}

// checkYouTubeVideoURL accepts Shorts URLs only.
func checkYouTubeVideoURL(youtubeURL string) error {
	var result error
	isVideo := youtubeVideoURLPattern.MatchString(youtubeURL)

	if isVideo == false {
		result = fmt.Errorf("not a YouTube Shorts URL: %s", youtubeURL)
	}

	return result
}

// runActor runs the actor for one video at quality and returns its row; a
// file over the upload limit is reported without downloading it.
func (e *YouTubeApifyExtractor) runActor(ctx context.Context, youtubeURL, quality string) (youtubeApifyItem, error) {
	result := youtubeApifyItem{}
	var items []youtubeApifyItem
	input := youtubeApifyInput{
		URLs:                 []youtubeApifyURL{{URL: youtubeURL}},
		Quality:              quality,
		Format:               youtubeApifyFormat,
		ResidentialProxyMode: youtubeApifyProxyDisabled,
	}
	body, err := e.api.runSync(ctx, e.actor, input, youtubeApifyRunTimeout, youtubeApifyMaxChargeUsd)

	if err == nil {
		err = json.Unmarshal(body, &items)
	}

	if (err == nil) && (len(items) == 0) {
		err = fmt.Errorf("youtube actor returned no row for %s", youtubeURL)
	}

	if err == nil {
		result = items[0]
	}

	if (err == nil) && (result.Status != youtubeApifyStatusSuccess) {
		err = fmt.Errorf("youtube actor failed (%s): %s", result.ErrorCode, result.Error)
	}

	if (err == nil) && (result.FileSizeBytes > telegramBotUploadLimit) {
		err = fmt.Errorf("%w: %d bytes at %sp", errYouTubeTooLarge, result.FileSizeBytes, quality)
	}

	return result, err
}

// download fetches the stored file and checks Telegram's upload limit.
func (e *YouTubeApifyExtractor) download(item youtubeApifyItem, youtubeURL string) ([]*MediaInfo, error) {
	var result []*MediaInfo
	timestamp := time.Now().UnixNano()
	filename := fmt.Sprintf("%s_%d%s", youtubeApifyFilePrefix, timestamp, youtubeApifyFileExt)
	filePath := filepath.Join(e.tempDir, filename)
	err := downloadToFile(item.DownloadURL, filePath)

	if err == nil {
		err = checkUploadSize(filePath)
	}

	if err != nil {
		os.Remove(filePath)
	}

	if err == nil {
		escapedUploader := EscapeForTelegramHTML(item.Uploader)
		escapedTitle := EscapeForTelegramHTML(item.Title)
		caption := FormatAuthorCaption(youtubePlatform, escapedUploader, "", escapedTitle)
		result = []*MediaInfo{{Type: MediaTypeVideo, URL: youtubeURL, Caption: caption, Filename: filePath}}
	}

	return result, err
}
