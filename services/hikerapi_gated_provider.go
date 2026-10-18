package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
)

// HikerAPI media types (instagrapi convention).
const (
	hikerMediaTypeVideo = 2
	hikerMediaTypeAlbum = 8
)

// hikerGatedFilePrefix starts the temp file name of a slide HikerAPI found.
const hikerGatedFilePrefix = "instagram_hiker"

// errHikerNoMedia marks a post answer without any usable media URL.
var errHikerNoMedia = errors.New("hikerapi returned no media")

// HikerInstagramProvider fetches a restricted post through HikerAPI's
// /v1/media/by/code (one billed request, every carousel slide included).
type HikerInstagramProvider struct {
	tempDir string
	api     *hikerClient
}

// hikerMedia is the part of HikerAPI's Media the bot uses.
type hikerMedia struct {
	MediaType     int                 `json:"media_type"`
	VideoURL      string              `json:"video_url"`
	ThumbnailURL  string              `json:"thumbnail_url"`
	ImageVersions []hikerImageVersion `json:"image_versions"`
	CaptionText   string              `json:"caption_text"`
	User          hikerUser           `json:"user"`
	Resources     []hikerResource     `json:"resources"`
}

// hikerResource is one carousel slide of a Media.
type hikerResource struct {
	MediaType     int                 `json:"media_type"`
	VideoURL      string              `json:"video_url"`
	ThumbnailURL  string              `json:"thumbnail_url"`
	ImageVersions []hikerImageVersion `json:"image_versions"`
}

// hikerImageVersion is one size of an image.
type hikerImageVersion struct {
	URL string `json:"url"`
}

// hikerUser is the author of a post or story.
type hikerUser struct {
	Username string `json:"username"`
	FullName string `json:"full_name"`
}

// Name implements GatedPostProvider.
func (p *HikerInstagramProvider) Name() string {
	return hikerServiceName
}

// FetchPost reads the post and downloads every slide; with spoiler
// (age-limited posts) the media and caption are sent blurred.
func (p *HikerInstagramProvider) FetchPost(instagramURL string, spoiler bool) ([]*MediaInfo, error) {
	var result []*MediaInfo
	post := gatedPost{}
	// The gated fetch runs after the chain's 90s deadline, so it has its own.
	background := context.Background()
	_, shortcode := instagramPostPath(instagramURL)
	err := fmt.Errorf("cannot extract post ID from URL: %s", instagramURL)

	if shortcode != "" {
		post, err = p.fetchPost(background, shortcode)
	}

	if err == nil {
		download := gatedDownload{TempDir: p.tempDir, Provider: hikerServiceName, FilePrefix: hikerGatedFilePrefix}
		result, err = download.slides(post, instagramURL, spoiler)
	}

	reportHikerOutcome(background, err)

	if err == nil {
		appendOriginalURL(result, instagramURL)
	}

	return result, err
}

// fetchPost reads one post by its shortcode.
func (p *HikerInstagramProvider) fetchPost(ctx context.Context, shortcode string) (gatedPost, error) {
	result := gatedPost{}
	media := hikerMedia{}
	query := url.Values{"code": {shortcode}}
	body, err := p.api.get(ctx, paidFeatureGated, hikerPathMediaByCode, query)

	if err == nil {
		err = json.Unmarshal(body, &media)
	}

	if err == nil {
		result = hikerGatedPost(media, shortcode)
		err = hikerPostMediaError(result)
	}

	return result, err
}

// hikerPostMediaError fails a post without any slide.
func hikerPostMediaError(post gatedPost) error {
	var result error

	if len(post.Slides) == 0 {
		result = fmt.Errorf("%w for %s", errHikerNoMedia, post.Shortcode)
	}

	return result
}

// hikerGatedPost maps a Media to the provider-neutral post: a carousel's
// resources, or the media itself.
func hikerGatedPost(media hikerMedia, shortcode string) gatedPost {
	own := hikerResource{MediaType: media.MediaType, VideoURL: media.VideoURL, ThumbnailURL: media.ThumbnailURL, ImageVersions: media.ImageVersions}
	resources := []hikerResource{own}

	if media.MediaType == hikerMediaTypeAlbum {
		resources = media.Resources
	}

	slides := hikerSlides(resources)
	result := gatedPost{Shortcode: shortcode, Username: media.User.Username, FullName: media.User.FullName, Caption: media.CaptionText, Slides: slides}

	return result
}

// hikerSlides keeps the resources that have a media URL.
func hikerSlides(resources []hikerResource) []gatedSlide {
	result := make([]gatedSlide, 0, len(resources))

	for _, resource := range resources {
		slide := hikerSlide(resource)

		if slide.URL != "" {
			result = append(result, slide)
		}
	}

	return result
}

// hikerSlide picks a resource's video, or its image (the thumbnail, else
// the first image version).
func hikerSlide(resource hikerResource) gatedSlide {
	isVideo := (resource.MediaType == hikerMediaTypeVideo) && (resource.VideoURL != "")
	imageURL := resource.ThumbnailURL

	if (imageURL == "") && (len(resource.ImageVersions) > 0) {
		imageURL = resource.ImageVersions[0].URL
	}

	result := gatedSlide{IsVideo: false, URL: imageURL}

	if isVideo == true {
		result = gatedSlide{IsVideo: true, URL: resource.VideoURL}
	}

	return result
}
