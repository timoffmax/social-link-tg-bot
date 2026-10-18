package services

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

// hikerStoryFilePrefix starts the temp file name of a story HikerAPI found.
const hikerStoryFilePrefix = "instagram_story_hiker"

// hikerStoryURLTemplate is the story URL HikerAPI is asked for.
const hikerStoryURLTemplate = "https://www.instagram.com/stories/%s/%s/"

// InstagramStoriesHikerExtractor fetches one Instagram story through
// HikerAPI's /v1/story/by/url (one billed request).
type InstagramStoriesHikerExtractor struct {
	tempDir string
	api     *hikerClient
}

// hikerStory is the part of HikerAPI's Story the bot uses.
type hikerStory struct {
	MediaType    int       `json:"media_type"`
	VideoURL     string    `json:"video_url"`
	ThumbnailURL string    `json:"thumbnail_url"`
	User         hikerUser `json:"user"`
}

// NewInstagramStoriesHikerExtractor returns the extractor, or nil when
// HikerAPI is not configured.
func NewInstagramStoriesHikerExtractor(tempDir string) *InstagramStoriesHikerExtractor {
	var result *InstagramStoriesHikerExtractor
	api := newHikerClient()

	if api != nil {
		result = &InstagramStoriesHikerExtractor{tempDir: tempDir, api: api}
	}

	return result
}

// ExtractMedia downloads the story the URL points to. Highlights URLs are
// refused before any request; a 403/404 answer is a final content outcome.
func (e *InstagramStoriesHikerExtractor) ExtractMedia(ctx context.Context, storyURL string) ([]*MediaInfo, error) {
	var result []*MediaInfo
	media := storyMedia{}
	username, storyID, err := parseApifyStoryURL(storyURL)
	isParsed := (err == nil)

	if isParsed == true {
		media, err = e.fetchStory(ctx, username, storyID)
	}

	if err == nil {
		download := storyDownload{TempDir: e.tempDir, FilePrefix: hikerStoryFilePrefix}
		result, err = download.file(media, username, storyURL)
	}

	if isParsed == true {
		reportHikerOutcome(ctx, err)
	}

	if err == nil {
		appendOriginalURL(result, storyURL)
	}

	return result, err
}

// fetchStory reads one story and picks its video, or its image.
func (e *InstagramStoriesHikerExtractor) fetchStory(ctx context.Context, username, storyID string) (storyMedia, error) {
	result := storyMedia{}
	story := hikerStory{}
	requestedURL := fmt.Sprintf(hikerStoryURLTemplate, username, storyID)
	query := url.Values{"url": {requestedURL}}
	body, err := e.api.get(ctx, paidFeatureStory, hikerPathStoryByURL, query)

	if err == nil {
		err = json.Unmarshal(body, &story)
	}

	if err == nil {
		result = hikerStoryMedia(story, storyID)
		err = hikerStoryMediaError(result)
	}

	return result, err
}

// hikerStoryMedia maps a Story to the provider-neutral story.
func hikerStoryMedia(story hikerStory, storyID string) storyMedia {
	isVideo := (story.MediaType == hikerMediaTypeVideo) && (story.VideoURL != "")
	result := storyMedia{ID: storyID, ImageURL: story.ThumbnailURL}

	if isVideo == true {
		result.VideoURL = story.VideoURL
	}

	return result
}

// hikerStoryMediaError fails a story without any media URL.
func hikerStoryMediaError(story storyMedia) error {
	var result error

	if (story.ImageURL == "") && (story.VideoURL == "") {
		result = fmt.Errorf("%w for story %s", errHikerNoMedia, story.ID)
	}

	return result
}
