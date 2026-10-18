package app

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/timoffmax/social-link-bot/model/api"
	"github.com/timoffmax/social-link-bot/model/api/request/message"
	"github.com/timoffmax/social-link-bot/model/api/request/payload"
	"github.com/timoffmax/social-link-bot/model/api/response"
	"github.com/timoffmax/social-link-bot/services"
)

// animationStatsType is how an animation counts in the media stats: Telegram
// stores it as a silent MP4 video.
const animationStatsType = services.MediaTypeVideo

// animationSender sends one animation; a variable so tests can replace the
// Telegram call.
var animationSender = message.NewSendAnimation

// sendAnimationFile sends a GIF (or the MP4 it was converted to) with
// sendAnimation, so it plays instead of showing a still frame. The file is
// uploaded under a .gif / .mp4 name matching what it holds (the multipart
// upload names it after FilePath; the file field repeats that name).
func sendAnimationFile(mediaInfo *services.MediaInfo, caption string, markup api.TgReplyMarkupInterface, origChat *response.TgChat, origMessage *response.TgMessage, url string) error {
	var result error
	services.NameFileByContent(mediaInfo)
	uploadName := filepath.Base(mediaInfo.Filename)
	animationPayload := payload.TgPlSendAnimation{
		ChatId:              origChat.Id,
		Animation:           uploadName,
		Caption:             caption,
		ParseMode:           payload.ParseModeHTML,
		FilePath:            mediaInfo.Filename,
		HasSpoiler:          mediaInfo.Spoiler,
		ReplyToMessageId:    origMessage.MessageId,
		DisableNotification: true,
		ReplyMarkup:         markup,
	}
	sent := animationSender(animationPayload)

	if sent.Err != nil {
		log.Printf("❌ Animation sending failed: %v", sent.Err)
		result = fmt.Errorf("failed to send animation: %w", sent.Err)
	}

	if sent.Err == nil {
		userID := int64(origMessage.From.Id)
		trackMediaSent(userID, origMessage.From.Username, origChat.Id, origChat.Title, url, animationStatsType)
	}

	return result
}

// sendAnimatedPostInOrder sends a post of several items that holds an
// animation (which can't go into an album) in post order: the photos and
// videos between animations as albums, each animation on its own, so the
// lead caption stays on the first message. It returns the items left for
// the one-by-one sending below: none for such a post, all of them otherwise.
func sendAnimatedPostInOrder(mediaInfos []*services.MediaInfo, origChat *response.TgChat, origMessage *response.TgMessage, url string, downloader *services.SmartDownloader) []*services.MediaInfo {
	result := mediaInfos
	isAnimatedPost := (len(mediaInfos) > 1) && (hasAnimation(mediaInfos) == true)

	if isAnimatedPost == true {
		runs := splitAtAnimations(mediaInfos)

		for _, run := range runs {
			sendPostRun(run, origChat, origMessage, url, downloader)
		}

		result = []*services.MediaInfo{}
	}

	return result
}

// hasAnimation reports a post with an animation.
func hasAnimation(mediaInfos []*services.MediaInfo) bool {
	result := false

	for _, item := range mediaInfos {
		result = result || (services.MediaTypeAnimation == item.Type)
	}

	return result
}

// splitAtAnimations splits a post into runs in post order: each animation
// alone, the items between animations together.
func splitAtAnimations(mediaInfos []*services.MediaInfo) [][]*services.MediaInfo {
	var result [][]*services.MediaInfo
	var run []*services.MediaInfo

	for _, item := range mediaInfos {
		isAnimation := (services.MediaTypeAnimation == item.Type)

		if (isAnimation == true) && (len(run) > 0) {
			result = append(result, run)
			run = nil
		}

		if isAnimation == true {
			result = append(result, []*services.MediaInfo{item})
		}

		if isAnimation == false {
			run = append(run, item)
		}
	}

	if len(run) > 0 {
		result = append(result, run)
	}

	return result
}

// sendPostRun sends one run: as albums when it fits them, the rest (or a
// rejected album) one by one.
func sendPostRun(run []*services.MediaInfo, origChat *response.TgChat, origMessage *response.TgMessage, url string, downloader *services.SmartDownloader) {
	unsent := sendMixedAlbums(run, origChat, origMessage, url, downloader)

	for _, item := range unsent {
		sendPostItem(item, origChat, origMessage, url, downloader)
	}
}

// sendPostItem sends one item of a several-item post (no post buttons; a
// failed file as a document) and removes its file; an item whose file is gone
// is skipped.
func sendPostItem(item *services.MediaInfo, origChat *response.TgChat, origMessage *response.TgMessage, url string, downloader *services.SmartDownloader) {
	_, statErr := os.Stat(item.Filename)
	isMissing := (isTextItem(item) == false) && (statErr != nil)

	if isMissing == true {
		log.Printf("Error: File does not exist: %s", item.Filename)
	}

	if isMissing == false {
		err := sendMediaFile(item, origChat, origMessage, url, false)
		logPostItemSent(item, err)
		sendFailedItemAsDocument(item, err, origChat, origMessage, url)
		downloader.CleanupFile(item.Filename)
	}
}

// sendFailedItemAsDocument sends a file that failed as a document, like the
// one-by-one sending of other posts.
func sendFailedItemAsDocument(item *services.MediaInfo, err error, origChat *response.TgChat, origMessage *response.TgMessage, url string) {
	isFailedFile := (err != nil) && (isTextItem(item) == false)

	if isFailedFile == true {
		sendAsDocumentFallback(item, origChat, origMessage, url)
	}
}

// logPostItemSent logs the outcome of one item.
func logPostItemSent(item *services.MediaInfo, err error) {
	if err != nil {
		log.Printf("Failed to send %s file %s: %v", item.Type, item.Filename, err)
	}

	if err == nil {
		log.Printf("Successfully sent %s file: %s", item.Type, item.Filename)
	}
}
