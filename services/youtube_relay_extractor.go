package services

import (
	"context"
	"fmt"
	"regexp"
)

// YouTubeRelayExtractor downloads Shorts through the home relay (yt-dlp
// from a residential IP), ahead of the paid Apify downloader.
type YouTubeRelayExtractor struct {
	client *relayClient
}

// youtubeShortsIDPattern takes the id of a Shorts link; the relay accepts
// exactly 11 characters, so a longer or shorter one is never sent.
var youtubeShortsIDPattern = regexp.MustCompile(`youtube\.com/shorts/([A-Za-z0-9_-]+)`)

// youtubeShortsIDGroup is the id's capture group in youtubeShortsIDPattern.
const youtubeShortsIDGroup = 1

// NewYouTubeRelayExtractor returns the extractor, or nil unless the relay
// is usable for YouTube (isRelayUsableFor, the same check as the chain's).
func NewYouTubeRelayExtractor(tempDir string) *YouTubeRelayExtractor {
	var result *YouTubeRelayExtractor
	warnRelayConfigOnce()
	isUsable := isRelayUsableFor(relayPlatformYouTube)

	if isUsable == true {
		client := newRelayClient(tempDir)
		result = &YouTubeRelayExtractor{client: client}
	}

	return result
}

// ExtractMedia fetches one Short through the relay; the caption carries the
// uploader and title from the relay's metadata, HTML-escaped.
func (e *YouTubeRelayExtractor) ExtractMedia(ctx context.Context, youtubeURL string) ([]*MediaInfo, error) {
	var result []*MediaInfo
	var video relayVideo
	id, err := youtubeShortsID(youtubeURL)

	if err == nil {
		video, err = e.client.Fetch(ctx, relayPlatformYouTube, id)
	}

	if err == nil {
		escapedUploader := EscapeForTelegramHTML(video.Meta.Uploader)
		escapedTitle := EscapeForTelegramHTML(video.Meta.Title)
		caption := FormatAuthorCaption(youtubePlatform, escapedUploader, "", escapedTitle)
		result = []*MediaInfo{{Type: MediaTypeVideo, URL: youtubeURL, Caption: caption, Filename: video.Path}}
		appendOriginalURL(result, youtubeURL)
	}

	return result, err
}

// youtubeShortsID returns the 11-character id of a Shorts link.
func youtubeShortsID(youtubeURL string) (string, error) {
	result := ""
	var err error
	match := youtubeShortsIDPattern.FindStringSubmatch(youtubeURL)
	hasID := (len(match) > youtubeShortsIDGroup)

	if hasID == true {
		result = match[youtubeShortsIDGroup]
	}

	pattern := relayIDPatterns[relayPlatformYouTube]
	isValid := pattern.MatchString(result)

	if isValid == false {
		err = fmt.Errorf("%w: no 11-character Shorts id in %s", errRelayBadID, youtubeURL)
		result = ""
	}

	return result, err
}
