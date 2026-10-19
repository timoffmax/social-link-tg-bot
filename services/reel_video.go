package services

import (
	"context"
	"errors"
	"log"
	"os"
	"time"
)

// reelCoverNote ends the caption of a reel cover sent without its video.
const reelCoverNote = "🎬 Instagram shows this video only to logged-in users, so here is its cover."

// ReelCoverNote is reelCoverNote for the app, which keeps it above the limit
// message it adds under a refused reel's cover.
const ReelCoverNote = reelCoverNote

// reelRelayWindow bounds the relay call for a reel's video, which runs after
// the download chain: the relay keeps relayNextMethodReserve of it free, so
// the call itself takes at most 25 s before the paid provider gets its turn.
const reelRelayWindow = 60 * time.Second

// errReelVideoMissing is the error of a reel video fetch that got no video.
var errReelVideoMissing = errors.New("instagram gave only the cover of the video")

// FetchReelVideo replaces a reel's cover by its video from the home relay or
// the paid provider (one special download), else keeps the cover with a note.
func (sd *SmartDownloader) FetchReelVideo(instagramURL string, media []*MediaInfo, requester DownloadRequester) ([]*MediaInfo, GatedFetchDenial) {
	result := media
	denial := GatedFetchAllowed
	isCoverOnly := isVideoCoverOnly(media)

	if isCoverOnly == true {
		result, denial = sd.replaceReelCover(instagramURL, media[0], requester)
	}

	return result, denial
}

// replaceReelCover returns the reel's video within the limit, removing the
// cover file, or the cover with reelCoverNote and the denial.
func (sd *SmartDownloader) replaceReelCover(instagramURL string, cover *MediaInfo, requester DownloadRequester) ([]*MediaInfo, GatedFetchDenial) {
	result := []*MediaInfo{cover}
	denial := GatedFetchAllowed
	download := newSpecialDownload(instagramURL, requester, dbSpecialLimit{})
	steps := sd.reelVideoSteps(instagramURL)
	video, err := runCostedSteps(download, steps)

	if err == nil {
		os.Remove(cover.Filename)
		result = video
	}

	if err != nil {
		log.Printf("🖼️ [reel-video] sending the cover of %s: %v", instagramURL, err)
		AppendCaptionNote(cover, reelCoverNote)
		denial = download.Denial()
	}

	return result, denial
}

// reelVideoSteps are the sources of a reel's video: the home relay, then the
// paid provider.
func (sd *SmartDownloader) reelVideoSteps(instagramURL string) []costedStep {
	isRelayReady := (sd.instagramRelay != nil) && (isRelayPlatformDisabled(relayPlatformInstagram) == false)
	isPaidReady := sd.canRunGatedProvider()
	relayRun := func() ([]*MediaInfo, error) {
		media, err := sd.fetchReelVideoFromRelay(instagramURL)
		result, err := videoOrMissing(media, err)

		return result, err
	}
	paidRun := func() ([]*MediaInfo, error) {
		log.Printf("🎬 [reel-video] trying the paid provider for the video of %s", instagramURL)
		media, err := sd.gated.FetchPost(instagramURL, false)
		result, err := videoOrMissing(media, err)

		return result, err
	}
	relayStep := costedStep{kind: costRelay, canRun: isRelayReady, run: relayRun}
	paidStep := costedStep{kind: costPaid, canRun: isPaidReady, run: paidRun}
	result := []costedStep{relayStep, paidStep}

	return result
}

// videoOrMissing turns a result without a video into errReelVideoMissing,
// removing its files.
func videoOrMissing(media []*MediaInfo, err error) ([]*MediaInfo, error) {
	result := media
	isMissing := (err == nil) && (hasVideoItem(media) == false)

	if isMissing == true {
		removeMediaFiles(media)
		result = nil
		err = errReelVideoMissing
	}

	return result, err
}

// fetchReelVideoFromRelay asks the home relay for the video.
func (sd *SmartDownloader) fetchReelVideoFromRelay(instagramURL string) ([]*MediaInfo, error) {
	var result []*MediaInfo
	err := errRelayNotConfigured

	if sd.instagramRelay != nil {
		log.Printf("🎬 [reel-video] trying the home relay for the video of %s", instagramURL)
		background := context.Background()
		ctx, cancel := context.WithTimeout(background, reelRelayWindow)
		result, err = sd.instagramRelay.ExtractMedia(ctx, instagramURL)
		cancel()
	}

	if err != nil {
		log.Printf("⚠️ [reel-video] relay failed for %s: %v", instagramURL, err)
		result = nil
	}

	return result, err
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

// removeMediaFiles deletes the downloaded files of a result that is not sent.
func removeMediaFiles(media []*MediaInfo) {
	for _, item := range media {
		if item.Filename != "" {
			os.Remove(item.Filename)
		}
	}
}
