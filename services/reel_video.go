package services

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"
)

// reelCoverNote ends the caption of a reel cover sent without its video.
const reelCoverNote = "🎬 Instagram shows this video only to logged-in users, so here is its cover."

// reelRelayWindow bounds the relay call for a reel's video, which runs after
// the download chain: the relay keeps relayNextMethodReserve of it free, so
// the call itself takes at most 25 s before the paid provider gets its turn.
const reelRelayWindow = 60 * time.Second

// errReelVideoMissing is the download error a denied reel video fetch wraps.
var errReelVideoMissing = errors.New("instagram gave only the cover of the video")

// FetchReelVideo replaces the cover of a reel the free methods got without its
// video: first by the video from the home relay, then from the paid provider
// within the restricted-post quotas; when neither works the cover is kept
// with a note.
func (sd *SmartDownloader) FetchReelVideo(instagramURL string, media []*MediaInfo, chatID, userID int64, isPrivate bool) []*MediaInfo {
	result := media
	isCoverOnly := isVideoCoverOnly(media)

	if isCoverOnly == true {
		result = sd.replaceReelCover(instagramURL, media[0], chatID, userID, isPrivate)
	}

	return result
}

// replaceReelCover returns the reel's video from the relay or the paid
// provider, removing the cover file, or the cover with reelCoverNote.
func (sd *SmartDownloader) replaceReelCover(instagramURL string, cover *MediaInfo, chatID, userID int64, isPrivate bool) []*MediaInfo {
	result := []*MediaInfo{cover}
	video := sd.fetchReelVideoFromRelay(instagramURL)
	var err error

	if hasVideoItem(video) == false {
		log.Printf("🎬 [reel-video] trying the paid provider for the video of %s", instagramURL)
		video, err = sd.fetchGatedWithQuota(instagramURL, errReelVideoMissing, false, chatID, userID, isPrivate)
	}

	isReplaced := (err == nil) && (hasVideoItem(video) == true)

	if isReplaced == true {
		os.Remove(cover.Filename)
		result = video
	}

	if isReplaced == false {
		log.Printf("🖼️ [reel-video] sending the cover of %s (paid fetch: %v)", instagramURL, err)
		captionWithNote := fmt.Sprintf("%s\n\n%s", cover.Caption, reelCoverNote)
		cover.Caption = strings.TrimSpace(captionWithNote)
	}

	return result
}

// fetchReelVideoFromRelay asks the home relay for the video; nil when the
// relay does not serve Instagram or fails.
func (sd *SmartDownloader) fetchReelVideoFromRelay(instagramURL string) []*MediaInfo {
	var result []*MediaInfo
	var err error

	if sd.instagramRelay != nil {
		log.Printf("🎬 [reel-video] trying the home relay for the video of %s", instagramURL)
		ctx, cancel := context.WithTimeout(context.Background(), reelRelayWindow)
		result, err = sd.instagramRelay.ExtractMedia(ctx, instagramURL)
		cancel()
	}

	if err != nil {
		log.Printf("⚠️ [reel-video] relay failed for %s: %v", instagramURL, err)
		result = nil
	}

	return result
}

// isVideoCoverOnly reports whether a download is just the cover of a video.
func isVideoCoverOnly(media []*MediaInfo) bool {
	result := (len(media) == 1) && (media[0].VideoCover == true)

	return result
}

// hasVideoItem reports whether a result holds a video.
func hasVideoItem(media []*MediaInfo) bool {
	result := false

	for _, item := range media {
		result = result || (MediaTypeVideo == item.Type)
	}

	return result
}
