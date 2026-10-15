package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/timoffmax/social-link-bot/config"
)

// GatedPostProvider fetches an Instagram post its author gated (e.g. an age
// limit) through a service with its own logged-in sessions. It is an interface
// so the provider can be swapped (Apify today, HikerAPI if Apify blocks us).
type GatedPostProvider interface {
	Name() string
	FetchPost(instagramURL string) ([]*MediaInfo, error)
}

// NewGatedPostProvider returns the configured provider, or nil when none is.
func NewGatedPostProvider(tempDir string) GatedPostProvider {
	var result GatedPostProvider
	token := config.GetApifyToken()

	if token != "" {
		result = &ApifyInstagramProvider{
			tempDir: tempDir,
			actor:   config.GetApifyInstagramActor(),
			api:     newApifyClient(),
		}
	}

	return result
}

const (
	// apifyServiceName is the outage-tracker key: consecutive failures (a
	// blocked account, an exhausted plan, a dead actor) alert the owner.
	apifyServiceName = "apify"

	// apifyRunTimeout bounds the synchronous actor run (a post takes ~11s).
	apifyRunTimeout = 90 * time.Second

	// apifyPostMaxChargeUsd caps one post run (~$0.009 observed).
	apifyPostMaxChargeUsd = 0.05

	// telegramBotUploadLimit is the Bot API upload limit per file.
	telegramBotUploadLimit = 50 * 1024 * 1024

	apifyMediaTypeVideo = "video"

	// gatedCaptionSeparator joins the author line and the caption text.
	gatedCaptionSeparator = "\n\n"

	// gatedFilePrefix starts the temp file name of a downloaded slide.
	gatedFilePrefix = "instagram_apify"

	gatedPlatform = "instagram"
	gatedExtPhoto = ".jpg"
	gatedExtVideo = ".mp4"

	// gatedSpoilerCaptionTemplate hides the caption text behind a spoiler, like
	// the media it belongs to.
	gatedSpoilerCaptionTemplate = "<tg-spoiler>%s</tg-spoiler>"
)

// ApifyInstagramProvider runs an Apify actor that returns one dataset item per
// Instagram post with every slide's direct CDN URL.
type ApifyInstagramProvider struct {
	tempDir string
	actor   string
	api     *apifyClient
}

// apifyRunInput is the actor input for fetching specific posts.
type apifyRunInput struct {
	PostURLs     []string `json:"postUrls"`
	IncludeViews bool     `json:"includeViews"`
}

// apifyPostItem is the part of a dataset item the bot uses.
type apifyPostItem struct {
	Shortcode string           `json:"shortcode"`
	Username  string           `json:"username"`
	FullName  string           `json:"full_name"`
	Caption   string           `json:"caption"`
	Media     []apifyPostMedia `json:"media"`
}

// apifyPostMedia is one slide of a post.
type apifyPostMedia struct {
	MediaType string `json:"media_type"`
	MediaURL  string `json:"media_url"`
}

// Name implements GatedPostProvider.
func (p *ApifyInstagramProvider) Name() string {
	return apifyServiceName
}

// FetchPost runs the actor for one post and downloads every slide, marked as
// spoiler media.
func (p *ApifyInstagramProvider) FetchPost(instagramURL string) ([]*MediaInfo, error) {
	var result []*MediaInfo
	var item apifyPostItem
	postURL, err := canonicalInstagramPostURL(instagramURL)

	if err == nil {
		item, err = p.runActor(postURL)
	}

	if err == nil {
		result, err = p.downloadSlides(item, instagramURL)
	}

	if (err != nil) && (isApifyBudgetError(err) == false) {
		detail := err.Error()
		ReportServiceFailure(apifyServiceName, detail)
	}

	if err == nil {
		ReportServiceSuccess(apifyServiceName)
		appendOriginalURL(result, instagramURL)
	}

	return result, err
}

// runActor runs the actor for one post and returns the post's item.
func (p *ApifyInstagramProvider) runActor(postURL string) (apifyPostItem, error) {
	result := apifyPostItem{}
	var items []apifyPostItem
	input := apifyRunInput{PostURLs: []string{postURL}, IncludeViews: false}
	// The gated fetch runs after the chain's 90s deadline, so it has its own.
	background := context.Background()
	body, err := p.api.runSync(background, p.actor, input, apifyRunTimeout, apifyPostMaxChargeUsd)

	if err == nil {
		err = json.Unmarshal(body, &items)
	}

	if err == nil {
		result, err = pickApifyPostItem(items, postURL)
	}

	return result, err
}

// pickApifyPostItem returns the item for the requested post.
func pickApifyPostItem(items []apifyPostItem, postURL string) (apifyPostItem, error) {
	result := apifyPostItem{}
	err := fmt.Errorf("apify returned no media for %s", postURL)
	_, shortcode := instagramPostPath(postURL)

	for _, item := range items {
		isMatch := ((item.Shortcode == shortcode) && (len(item.Media) > 0))

		if isMatch == true {
			result = item
			err = nil
		}
	}

	return result, err
}

// downloadSlides downloads every slide that fits Telegram's upload limit.
func (p *ApifyInstagramProvider) downloadSlides(item apifyPostItem, instagramURL string) ([]*MediaInfo, error) {
	var result []*MediaInfo
	var err error
	timestamp := time.Now().UnixNano()
	caption := gatedPostCaption(item, instagramURL)

	for i, slide := range item.Media {
		mediaInfo, slideErr := p.downloadSlide(slide, item.Shortcode, timestamp, i)

		if slideErr != nil {
			log.Printf("⚠️ [apify] skipping slide %d of %s: %v", (i + 1), item.Shortcode, slideErr)
		}

		if slideErr == nil {
			mediaInfo.URL = instagramURL
			result = append(result, mediaInfo)
		}
	}

	// Only the first item carries the caption: the others get just the link
	// (appendOriginalURL), so separately sent videos don't repeat it.
	if len(result) > 0 {
		result[0].Caption = caption
	}

	if len(result) == 0 {
		err = fmt.Errorf("no apify slide could be downloaded for %s", item.Shortcode)
	}

	return result, err
}

// gatedPostCaption builds the author line plus the post caption, hidden
// behind spoilers like the media. The caption is fitted (after HTML escaping)
// into what Telegram's caption limit leaves next to the author line and the
// original link, so no later truncation can cut a tag or an entity.
func gatedPostCaption(item apifyPostItem, instagramURL string) string {
	escapedName := EscapeForTelegramHTML(item.FullName)
	authorLine := FormatAuthorCaption(gatedPlatform, escapedName, item.Username, "")
	linkSuffix := FormatOriginalLink(instagramURL)
	budget := TelegramCaptionLimitBytes - len(authorLine) - len(gatedCaptionSeparator) - len(linkSuffix)
	spoilerText := fitSpoilerCaption(item.Caption, budget)
	result := FormatAuthorCaption(gatedPlatform, escapedName, item.Username, spoilerText)

	return result
}

// fitSpoilerCaption returns the escaped, spoiler-wrapped caption, shortened
// (at rune boundaries of the raw text) until it fits budget bytes. Escaping
// only ever grows text, so cutting the overshoot from the raw text shrinks the
// result by at least as much; the loop ends at the latest with an empty text.
func fitSpoilerCaption(raw string, budget int) string {
	remaining := raw
	result := wrapSpoilerLines(remaining)

	for (len(result) > budget) && (remaining != "") {
		overshoot := len(result) - budget
		keep := len(remaining) - overshoot

		if keep < 0 {
			keep = 0
		}

		remaining = truncateAtRuneBoundary(remaining, keep)
		result = wrapSpoilerLines(remaining)
	}

	return result
}

// wrapSpoilerLines escapes each caption line and wraps it in its own spoiler,
// so any single line taken out of the caption (e.g. the author-only caption
// when descriptions are off) is still balanced HTML.
func wrapSpoilerLines(raw string) string {
	lines := strings.Split(raw, "\n")
	wrapped := make([]string, 0, len(lines))

	for _, line := range lines {
		escaped := EscapeForTelegramHTML(line)
		isBlank := (strings.TrimSpace(line) == "")
		wrappedLine := ""

		if isBlank == false {
			wrappedLine = fmt.Sprintf(gatedSpoilerCaptionTemplate, escaped)
		}

		wrapped = append(wrapped, wrappedLine)
	}

	joined := strings.Join(wrapped, "\n")
	result := strings.TrimSpace(joined)

	return result
}

// downloadSlide downloads one slide to a temp file.
func (p *ApifyInstagramProvider) downloadSlide(slide apifyPostMedia, shortcode string, timestamp int64, index int) (*MediaInfo, error) {
	var result *MediaInfo
	mediaType := MediaTypePhoto
	ext := gatedExtPhoto

	if slide.MediaType == apifyMediaTypeVideo {
		mediaType = MediaTypeVideo
		ext = gatedExtVideo
	}

	filename := fmt.Sprintf("%s_%s_%d_%d%s", gatedFilePrefix, shortcode, timestamp, (index + 1), ext)
	filePath := filepath.Join(p.tempDir, filename)
	err := downloadToFile(slide.MediaURL, filePath)

	if err == nil {
		err = checkUploadSize(filePath)
	}

	if err != nil {
		os.Remove(filePath)
	}

	if err == nil {
		result = &MediaInfo{Type: mediaType, Filename: filePath, Spoiler: true}
	}

	return result, err
}

// checkUploadSize rejects files above Telegram's bot upload limit.
func checkUploadSize(filePath string) error {
	info, err := os.Stat(filePath)

	size := int64(0)

	if err == nil {
		size = info.Size()
	}

	if size > telegramBotUploadLimit {
		err = fmt.Errorf("file is %d bytes, above the %d-byte upload limit", size, telegramBotUploadLimit)
	}

	return err
}
