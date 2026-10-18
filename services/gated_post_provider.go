package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/timoffmax/social-link-bot/config"
)

// apifyEmptyRetryDelay is the pause before retrying an empty answer (a
// variable so tests can skip it).
var apifyEmptyRetryDelay = 3 * time.Second

// errApifyNoMedia marks a run that delivered no item for the post.
var errApifyNoMedia = errors.New("apify returned no media")

// GatedPostProvider fetches an Instagram post its author gated (e.g. an age
// limit) through a service with its own logged-in sessions. It is an interface
// so the provider can be swapped (Apify today, HikerAPI if Apify blocks us).
type GatedPostProvider interface {
	Name() string
	FetchPost(instagramURL string, spoiler bool) ([]*MediaInfo, error)
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

// FetchPost runs the actor for one post and downloads every slide; with
// spoiler (age-limited posts) the media and caption are sent blurred.
func (p *ApifyInstagramProvider) FetchPost(instagramURL string, spoiler bool) ([]*MediaInfo, error) {
	var result []*MediaInfo
	var item apifyPostItem
	postURL, err := canonicalInstagramPostURL(instagramURL)

	if err == nil {
		item, err = p.runActor(postURL)
	}

	if err == nil {
		result, err = p.downloadSlides(item, instagramURL, spoiler)
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

// runActor runs the actor for one post and returns the post's item. An empty
// dataset is retried once after a short pause: on the free plan a run is
// turned away (with an empty dataset) while the shared free capacity is busy.
// A non-empty answer without the post is final. If the retry is refused (e.g.
// budget), the first run's result stands, since that run may be billed.
func (p *ApifyInstagramProvider) runActor(postURL string) (apifyPostItem, error) {
	result, isEmpty, err := p.runActorOnce(postURL)

	if isEmpty == true {
		log.Printf("🔁 [apify] empty answer for %s, retrying once", postURL)
		time.Sleep(apifyEmptyRetryDelay)
		result, err = p.retryActor(postURL, err)
	}

	return result, err
}

// retryActor runs the second attempt, keeping firstErr when it is refused.
func (p *ApifyInstagramProvider) retryActor(postURL string, firstErr error) (apifyPostItem, error) {
	result, _, err := p.runActorOnce(postURL)

	if isApifyBudgetError(err) == true {
		err = firstErr
	}

	return result, err
}

// runActorOnce runs the actor once for one post; isEmpty reports an empty
// dataset.
func (p *ApifyInstagramProvider) runActorOnce(postURL string) (apifyPostItem, bool, error) {
	result := apifyPostItem{}
	isEmpty := false
	var items []apifyPostItem
	input := apifyRunInput{PostURLs: []string{postURL}, IncludeViews: false}
	// The gated fetch runs after the chain's 90s deadline, so it has its own.
	background := context.Background()
	body, err := p.api.runSync(background, p.actor, input, apifyRunTimeout, apifyPostMaxChargeUsd)

	if err == nil {
		err = json.Unmarshal(body, &items)
	}

	if err == nil {
		isEmpty = (len(items) == 0)
		result, err = pickApifyPostItem(items, postURL)
	}

	return result, isEmpty, err
}

// pickApifyPostItem returns the item for the requested post.
func pickApifyPostItem(items []apifyPostItem, postURL string) (apifyPostItem, error) {
	result := apifyPostItem{}
	err := fmt.Errorf("%w for %s", errApifyNoMedia, postURL)
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
func (p *ApifyInstagramProvider) downloadSlides(item apifyPostItem, instagramURL string, spoiler bool) ([]*MediaInfo, error) {
	var result []*MediaInfo
	var err error
	timestamp := time.Now().UnixNano()
	caption := gatedPostCaption(item, instagramURL, spoiler)

	for i, slide := range item.Media {
		mediaInfo, slideErr := p.downloadSlide(slide, item.Shortcode, timestamp, i, spoiler)

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
// behind spoilers like the media when spoiler is set; room is kept for the
// remaining-quota note appended after the fetch.
func gatedPostCaption(item apifyPostItem, instagramURL string, spoiler bool) string {
	caption := authorCaption{
		Platform:    gatedPlatform,
		DisplayName: item.FullName,
		Username:    item.Username,
		Text:        item.Caption,
		PostURL:     instagramURL,
		Reserve:     gatedQuotaNoteReserve,
		Spoiler:     spoiler,
	}
	result := buildAuthorCaption(caption)

	return result
}

// authorCaption is what buildAuthorCaption formats.
type authorCaption struct {
	Platform string
	// DisplayName is raw; buildAuthorCaption escapes it.
	DisplayName string
	Username    string
	// Text is the raw post text.
	Text    string
	PostURL string
	// MaxText caps the escaped text; 0 fills what the caption limit leaves.
	MaxText int
	// Reserve is kept free next to the original link (e.g. a quota note).
	Reserve int
	Spoiler bool
	// PlainText marks DisplayName and Text as plain text whose entities are
	// already decoded (e.g. facebed's og tags): they are escaped without
	// decoding entities again, so a literal "&lt;br&gt;" stays as written.
	PlainText bool
}

// buildAuthorCaption formats the author line plus the post text (escaped for
// HTML, spoiler-wrapped when Spoiler is set). The text is fitted into MaxText
// bytes, or into what Telegram's caption limit leaves next to the author
// line, the original link and Reserve, so no later cut splits a tag or an
// entity.
func buildAuthorCaption(caption authorCaption) string {
	escape := captionEscaper(caption.PlainText)
	displayName := escape(caption.DisplayName)
	authorLine := FormatAuthorCaption(caption.Platform, displayName, caption.Username, "")
	linkSuffix := FormatOriginalLink(caption.PostURL)
	budget := caption.MaxText

	if caption.MaxText == 0 {
		budget = TelegramCaptionLimitBytes - len(authorLine) - len(gatedCaptionSeparator) - len(linkSuffix) - caption.Reserve
	}

	fittedText := fitEscapedCaption(caption.Text, budget, caption.Spoiler, escape)
	result := FormatAuthorCaption(caption.Platform, displayName, caption.Username, fittedText)

	return result
}

// captionEscaper returns the HTML escaping of caption text: plain text is
// escaped as it is, other text has its entities decoded first so it is not
// escaped twice.
func captionEscaper(plainText bool) func(string) string {
	result := EscapeForTelegramHTML

	if plainText == true {
		result = html.EscapeString
	}

	return result
}

// fitSpoilerCaption returns the escaped, spoiler-wrapped caption of the
// longest prefix of raw (cut at a rune boundary) whose result fits budget
// bytes; a negative budget gives "".
func fitSpoilerCaption(raw string, budget int, spoiler bool) string {
	result := fitEscapedCaption(raw, budget, spoiler, EscapeForTelegramHTML)

	return result
}

// fitEscapedCaption is fitSpoilerCaption with the given escaping.
func fitEscapedCaption(raw string, budget int, spoiler bool, escape func(string) string) string {
	result := ""
	measure := func(prefix string) int {
		wrapped := escapeCaptionLines(prefix, spoiler, escape)

		return len(wrapped)
	}
	end := fitPrefixEnd(raw, budget, measure)

	if end > 0 {
		result = escapeCaptionLines(raw[:end], spoiler, escape)
	}

	return result
}

// fitPrefixEnd returns the end of the longest prefix of raw (cut at a rune
// boundary) whose measure fits budget, or -1 when not even the empty prefix
// fits. measure must not shrink as the prefix grows, so a binary search over
// the rune boundaries finds that prefix exactly.
func fitPrefixEnd(raw string, budget int, measure func(prefix string) int) int {
	result := -1
	boundaries := runeBoundaries(raw)
	firstTooLong := sort.Search(len(boundaries), func(i int) bool {
		prefix := raw[:boundaries[i]]
		size := measure(prefix)

		return (size > budget)
	})

	if firstTooLong > 0 {
		result = boundaries[firstTooLong-1]
	}

	return result
}

// runeBoundaries returns the byte offset of every rune start in s plus
// len(s), so s[:offset] is a valid prefix for each of them.
func runeBoundaries(s string) []int {
	result := make([]int, 0, (len(s) + 1))

	for offset := range s {
		result = append(result, offset)
	}

	result = append(result, len(s))

	return result
}

// wrapSpoilerLines escapes each caption line and, with spoiler, wraps it in
// its own spoiler, so any single line taken out of the caption (e.g. the
// author-only caption when descriptions are off) is still balanced HTML.
func wrapSpoilerLines(raw string, spoiler bool) string {
	result := escapeCaptionLines(raw, spoiler, EscapeForTelegramHTML)

	return result
}

// escapeCaptionLines is wrapSpoilerLines with the given escaping.
func escapeCaptionLines(raw string, spoiler bool, escape func(string) string) string {
	lines := strings.Split(raw, "\n")
	wrapped := make([]string, 0, len(lines))

	for _, line := range lines {
		escaped := escape(line)
		isBlank := (strings.TrimSpace(line) == "")
		wrappedLine := ""

		if (isBlank == false) && (spoiler == true) {
			wrappedLine = fmt.Sprintf(gatedSpoilerCaptionTemplate, escaped)
		}

		if (isBlank == false) && (spoiler == false) {
			wrappedLine = escaped
		}

		wrapped = append(wrapped, wrappedLine)
	}

	joined := strings.Join(wrapped, "\n")
	result := strings.TrimSpace(joined)

	return result
}

// downloadSlide downloads one slide to a temp file.
func (p *ApifyInstagramProvider) downloadSlide(slide apifyPostMedia, shortcode string, timestamp int64, index int, spoiler bool) (*MediaInfo, error) {
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
		result = &MediaInfo{Type: mediaType, Filename: filePath, Spoiler: spoiler}
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
