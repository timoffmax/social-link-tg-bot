package services

// Video posts through facebed: facebed's page of a video post carries the
// signed fbcdn MP4 in og:video. yt-dlp stays first for video posts (it picks
// a format under Telegram's limit); facebed's video is the fallback when
// yt-dlp fails, which it does for most /posts/ links ("Cannot parse data",
// "only available for registered users").

import (
	"context"
	"fmt"
	"time"
)

// facebedVideoFileIndex is the index of a video in its temp file name.
const facebedVideoFileIndex = 0

// ExtractVideo sends the video of a facebed video post with its author
// caption. For a post link it uses the page ExtractMedia read earlier in the
// chain (no second request) and fails when that page had no video; for other
// links it reads the page, and a page without video is handled like
// ExtractMedia (photos, text post, failures).
func (e *FacebookFacebedExtractor) ExtractVideo(ctx context.Context, postURL string) ([]*MediaInfo, error) {
	var result []*MediaInfo
	var err error
	post, isRemembered := e.rememberedVideoPost(postURL)
	kind := facebookLinkKindOf(postURL)
	isPostLink := (kind == facebookLinkPost)

	if (isRemembered == false) && (isPostLink == true) {
		err = fmt.Errorf("%w: %s", errFacebedNoVideo, postURL)
	}

	if (isRemembered == false) && (isPostLink == false) {
		post, err = e.readPost(ctx, postURL)
	}

	hasVideo := (err == nil) && (post.VideoURL != "")

	if hasVideo == true {
		result, err = e.downloadVideo(ctx, post, postURL)
	}

	if (err == nil) && (hasVideo == false) {
		result, err = e.buildPostWithoutVideo(ctx, post, postURL)
	}

	isRead := (isRemembered == true) || (isPostLink == false)

	if isRead == true {
		reportFacebedOutcome(err)
	}

	return result, err
}

// buildPostWithoutVideo handles a page without video like ExtractMedia.
func (e *FacebookFacebedExtractor) buildPostWithoutVideo(ctx context.Context, post facebedPost, postURL string) ([]*MediaInfo, error) {
	var result []*MediaInfo
	err := checkFacebedPost(post, postURL)

	if err == nil {
		result, err = e.buildMedia(ctx, post, postURL)
	}

	return result, err
}

// downloadVideo downloads the post's video (fbcdn only, within the upload
// limit) with the author caption and the original link.
func (e *FacebookFacebedExtractor) downloadVideo(ctx context.Context, post facebedPost, postURL string) ([]*MediaInfo, error) {
	var result []*MediaInfo
	timestamp := time.Now().UnixNano()
	basePath := e.photoBasePath(timestamp, facebedVideoFileIndex)
	item, err := e.downloadMediaFile(ctx, post.VideoURL, basePath, facebedVideoKind)

	if err == nil {
		item.URL = postURL
		item.Caption = facebedCaption(post, postURL)
		result = []*MediaInfo{item}
		appendOriginalURL(result, postURL)
	}

	return result, err
}

// rememberVideoPost keeps a video post for ExtractVideo, dropping expired
// entries.
func (e *FacebookFacebedExtractor) rememberVideoPost(postURL string, post facebedPost) {
	e.videoMu.Lock()
	defer e.videoMu.Unlock()

	now := time.Now()

	if e.videoPosts == nil {
		e.videoPosts = map[string]facebedVideoPost{}
	}

	for key, entry := range e.videoPosts {
		age := now.Sub(entry.ReadAt)

		if age > facebedVideoPostTTL {
			delete(e.videoPosts, key)
		}
	}

	e.videoPosts[postURL] = facebedVideoPost{Post: post, ReadAt: now}
}

// rememberedVideoPost returns the live video post kept for the link.
func (e *FacebookFacebedExtractor) rememberedVideoPost(postURL string) (facebedPost, bool) {
	e.videoMu.Lock()
	defer e.videoMu.Unlock()

	entry, isFound := e.videoPosts[postURL]
	age := time.Since(entry.ReadAt)
	isLive := (isFound == true) && (age <= facebedVideoPostTTL)
	result := facebedPost{}

	if isLive == true {
		result = entry.Post
	}

	return result, isLive
}
