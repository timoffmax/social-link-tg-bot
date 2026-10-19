package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/timoffmax/social-link-bot/config"
)

// InstagramStoriesApifyExtractor fetches one Instagram story through an Apify
// actor. Stories need a login (the RapidAPI endpoint died, yt-dlp and the
// browser always failed), and the actor takes a username and returns the
// account's whole current story tray, so the story is picked by id.
type InstagramStoriesApifyExtractor struct {
	tempDir string
	actor   string
	api     *apifyClient
}

const (
	// storiesApifyServiceName is the outage-tracker key of the actor.
	storiesApifyServiceName = "apify-stories"

	// storiesApifyRunTimeout bounds the run (a 3-story tray took ~7s).
	storiesApifyRunTimeout = 60 * time.Second

	// storiesApifyMaxChargeUsd caps one run (~$0.0035 per story in the tray).
	storiesApifyMaxChargeUsd = 0.10

	storiesStatusOK           = "ok"
	storiesInstagramPlatform  = "instagram"
	storiesApifyFilePrefix    = "instagram_story_apify"
	storiesApifyVideoFileExt  = ".mp4"
	storiesApifyPhotoFileExt  = ".jpg"
	storiesHighlightsUsername = "highlights"
)

// storiesApifyInput is the actor input for one account's story tray.
type storiesApifyInput struct {
	Usernames            []string `json:"usernames"`
	IncludeStories       bool     `json:"includeStories"`
	IncludeHighlights    bool     `json:"includeHighlights"`
	ExpandHighlightItems bool     `json:"expandHighlightItems"`
	CompactOutput        bool     `json:"compactOutput"`
}

// storiesApifyRow is one account row of the dataset (a trailing summary row
// has no status and is skipped).
type storiesApifyRow struct {
	Status  string             `json:"status"`
	Stories []storiesApifyItem `json:"stories"`
}

// storiesApifyItem is one story of the tray.
type storiesApifyItem struct {
	ID       string `json:"id"`
	ImageURL string `json:"imageUrl"`
	VideoURL string `json:"videoUrl"`
}

// NewInstagramStoriesApifyExtractor returns the extractor, or nil without
// APIFY_TOKEN.
func NewInstagramStoriesApifyExtractor(tempDir string) *InstagramStoriesApifyExtractor {
	var result *InstagramStoriesApifyExtractor
	api := newApifyClient()

	if api != nil {
		result = &InstagramStoriesApifyExtractor{tempDir: tempDir, actor: config.GetApifyStoriesActor(), api: api}
	}

	return result
}

// storiesContentStatuses are account outcomes, not service failures.
var storiesContentStatuses = map[string]bool{
	"private":           true,
	"not_found":         true,
	"no_active_stories": true,
}

// ExtractMedia downloads the story the URL points to.
func (e *InstagramStoriesApifyExtractor) ExtractMedia(ctx context.Context, storyURL string) ([]*MediaInfo, error) {
	var result []*MediaInfo
	var story storiesApifyItem
	var status string
	isStoryMissing := false
	username, storyID, err := parseApifyStoryURL(storyURL)
	isParsed := (err == nil)

	if isParsed == true {
		story, status, isStoryMissing, err = e.findStory(ctx, username, storyID)
	}

	if err == nil {
		media := storyMedia{ID: story.ID, ImageURL: story.ImageURL, VideoURL: story.VideoURL}
		download := storyDownload{TempDir: e.tempDir, FilePrefix: storiesApifyFilePrefix}
		result, err = download.file(media, username, storyURL)
	}

	isContentOutcome := (err != nil) && ((storiesContentStatuses[status] == true) || (isStoryMissing == true))
	isDeadline := errors.Is(err, context.DeadlineExceeded)
	isServiceFailure := (err != nil) && (isParsed == true) && (isContentOutcome == false) && (isPaidBudgetError(err) == false) && (isDeadline == false)

	// Another paid provider would be billed for the same answer: final.
	if isContentOutcome == true {
		err = fmt.Errorf("%w: %w", errPaidContentOutcome, err)
	}

	if isServiceFailure == true {
		detail := err.Error()
		ReportServiceFailure(storiesApifyServiceName, detail)
	}

	if err == nil {
		ReportServiceSuccess(storiesApifyServiceName)
		appendOriginalURL(result, storyURL)
	}

	return result, err
}

// storyURLPattern captures the username and numeric id of a story URL.
var storyURLPattern = regexp.MustCompile(`instagram\.com/stories/([^/]+)/([0-9]+)`)

// parseStoryURL extracts username and storyId from an Instagram story URL
func parseStoryURL(url string) (string, string, error) {
	matches := storyURLPattern.FindStringSubmatch(url)
	if (matches == nil) || (len(matches) < 3) {
		return "", "", fmt.Errorf("cannot parse story URL: %s", url)
	}

	return matches[1], matches[2], nil
}

// parseApifyStoryURL parses a story URL with the shared parseStoryURL and
// rejects highlights URLs (/stories/highlights/<id>), which would otherwise be
// looked up as an account named "highlights". Both refusals happen before any
// spend.
func parseApifyStoryURL(storyURL string) (string, string, error) {
	username, storyID, err := parseStoryURL(storyURL)
	isHighlights := (err == nil) && (username == storiesHighlightsUsername)

	if err != nil {
		err = fmt.Errorf("%w: %w", errCostedNotStarted, err)
	}

	if isHighlights == true {
		err = fmt.Errorf("%w: story highlights are not supported: %s", errCostedNotStarted, storyURL)
	}

	return username, storyID, err
}

// findStory runs the actor for the account and picks the story by id. It
// also returns the account status (private, not_found, no_active_stories...)
// and whether the account answered but the story was not in its tray.
func (e *InstagramStoriesApifyExtractor) findStory(ctx context.Context, username, storyID string) (storiesApifyItem, string, bool, error) {
	result := storiesApifyItem{}
	status := ""
	isMissing := false
	var rows []storiesApifyRow
	input := storiesApifyInput{
		Usernames:            []string{username},
		IncludeStories:       true,
		IncludeHighlights:    false,
		ExpandHighlightItems: false,
		CompactOutput:        true,
	}
	body, err := e.api.runSync(ctx, e.actor, input, storiesApifyRunTimeout, storiesApifyMaxChargeUsd)

	if err == nil {
		err = json.Unmarshal(body, &rows)
	}

	if err == nil {
		result, status, err = pickStory(rows, username, storyID)
		isMissing = ((err != nil) && (status == storiesStatusOK))
	}

	return result, status, isMissing, err
}

// pickStory finds storyID in the account rows.
func pickStory(rows []storiesApifyRow, username, storyID string) (storiesApifyItem, string, error) {
	result := storiesApifyItem{}
	status := ""
	isFound := false

	for _, row := range rows {
		if row.Status != "" {
			status = row.Status
		}

		for _, story := range row.Stories {
			if story.ID == storyID {
				result = story
				isFound = true
			}
		}
	}

	err := fmt.Errorf("story %s of @%s not available (account status: %s)", storyID, username, status)

	if isFound == true {
		err = nil
	}

	return result, status, err
}

// storyMedia is one story as any provider returns it (direct CDN URLs).
type storyMedia struct {
	ID       string
	ImageURL string
	VideoURL string
}

// storyDownload downloads a provider's story into TempDir.
type storyDownload struct {
	TempDir    string
	FilePrefix string
}

// file fetches the story's video, or its image when it has none; a failed
// download leaves no file.
func (d storyDownload) file(story storyMedia, username, storyURL string) ([]*MediaInfo, error) {
	var result []*MediaInfo
	mediaURL := story.ImageURL
	mediaType := MediaTypePhoto
	ext := storiesApifyPhotoFileExt

	if story.VideoURL != "" {
		mediaURL = story.VideoURL
		mediaType = MediaTypeVideo
		ext = storiesApifyVideoFileExt
	}

	timestamp := time.Now().UnixNano()
	filename := fmt.Sprintf("%s_%s_%d%s", d.FilePrefix, story.ID, timestamp, ext)
	filePath := filepath.Join(d.TempDir, filename)
	err := downloadToFile(mediaURL, filePath)

	if err == nil {
		err = checkUploadSize(filePath)
	}

	if err != nil {
		os.Remove(filePath)
	}

	if err == nil {
		caption := FormatAuthorCaption(storiesInstagramPlatform, "", username, "")
		result = []*MediaInfo{{Type: mediaType, URL: storyURL, Caption: caption, Filename: filePath}}
	}

	return result, err
}
