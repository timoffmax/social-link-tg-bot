package services

import (
	"context"
	"fmt"
)

// InstagramRelayExtractor downloads the video of a reel through the home
// relay (yt-dlp from a residential IP), for reels whose video Instagram
// gives the datacenter's free methods only as a cover image.
type InstagramRelayExtractor struct {
	client *relayClient
}

// NewInstagramRelayExtractor returns the extractor, or nil unless the relay
// is usable for Instagram (isRelayUsableFor).
func NewInstagramRelayExtractor(tempDir string) *InstagramRelayExtractor {
	var result *InstagramRelayExtractor
	warnRelayConfigOnce()
	isUsable := isRelayUsableFor(relayPlatformInstagram)

	if isUsable == true {
		client := newRelayClient(tempDir)
		result = &InstagramRelayExtractor{client: client}
	}

	return result
}

// ExtractMedia fetches the video of one reel or IGTV post through the relay;
// the caption carries the author and the post text from the relay's
// metadata, escaped and fitted like the paid provider's.
func (e *InstagramRelayExtractor) ExtractMedia(ctx context.Context, instagramURL string) ([]*MediaInfo, error) {
	var result []*MediaInfo
	var video relayVideo
	var err error
	_, shortcode := instagramPostPath(instagramURL)

	if shortcode == "" {
		err = fmt.Errorf("%w: no Instagram shortcode in %s", errRelayBadID, instagramURL)
	}

	if err == nil {
		video, err = e.client.Fetch(ctx, relayPlatformInstagram, shortcode)
	}

	if err == nil {
		caption := instagramRelayCaption(video.Meta, instagramURL)
		result = []*MediaInfo{{Type: MediaTypeVideo, URL: instagramURL, Caption: caption, Filename: video.Path}}
		appendOriginalURL(result, instagramURL)
	}

	return result, err
}

// instagramRelayCaption builds the author line and the post text; a user
// name that is not a plain Instagram name is left out.
func instagramRelayCaption(meta relayMeta, instagramURL string) string {
	username := meta.Channel
	isSafeUsername := profileUsernamePattern.MatchString(username)

	if isSafeUsername == false {
		username = ""
	}

	caption := authorCaption{
		Platform:    gatedPlatform,
		DisplayName: meta.Uploader,
		Username:    username,
		Text:        meta.Description,
		PostURL:     instagramURL,
		PlainText:   true,
	}
	result := buildAuthorCaption(caption)

	return result
}
