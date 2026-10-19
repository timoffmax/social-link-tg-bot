package app

import (
	"errors"
	"fmt"
	"html"
	"log"
	"os"
	"regexp"
	"strings"

	"github.com/timoffmax/social-link-bot/model/api/request/message"
	"github.com/timoffmax/social-link-bot/model/api/request/payload"
	"github.com/timoffmax/social-link-bot/model/api/response"
	"github.com/timoffmax/social-link-bot/services"
)

// Telegram media group size limits.
const (
	mediaGroupMinItems = 2
	mediaGroupMaxItems = 10
)

// albumVideoExtension is the only video container sent inside albums; other
// containers keep sendMediaFile's conversion and naming.
const albumVideoExtension = ".mp4"

// albumAttachTemplate names an item's upload in the media JSON; the multipart
// encoder writes the files as photo0, photo1, ... for photos and videos alike.
const albumAttachTemplate = "attach://photo%d"

// albumCaptionCut is where an over-long album caption is cut before "...".
const albumCaptionCut = services.TelegramCaptionLimitBytes - 3

// htmlTagPattern matches HTML tags, removed from captions sent as plain text.
var htmlTagPattern = regexp.MustCompile(`<[^>]*>`)

// errAlbumRejected marks an album Telegram answered with an error. Any other
// failure (a timeout, a dropped connection) may still have been delivered, so
// its items must not be sent again.
var errAlbumRejected = errors.New("telegram rejected the album")

// albumSender sends one album; a variable so tests can replace the Telegram call.
var albumSender = sendMediaAsGroup

// sendMixedAlbums sends a post's photos and videos as evenly split albums of
// up to 10 items in post order, and returns the items it did not send: all of
// them when the post doesn't fit an album (see canSendAsMixedAlbum), and
// everything from an album Telegram rejected on.
func sendMixedAlbums(mediaInfos []*services.MediaInfo, origChat *response.TgChat, origMessage *response.TgMessage, url string, downloader *services.SmartDownloader) []*services.MediaInfo {
	result := mediaInfos
	isAlbumPost := canSendAsMixedAlbum(mediaInfos)

	if isAlbumPost == true {
		result = sendAlbumChunks(mediaInfos, origChat, origMessage, url, downloader)
	}

	return result
}

// sendAlbumChunks sends the albums in order. Only the first album carries the
// caption. A rejected album hands it and the rest back for one-by-one sending;
// an album with an unknown outcome is not resent.
func sendAlbumChunks(mediaInfos []*services.MediaInfo, origChat *response.TgChat, origMessage *response.TgMessage, url string, downloader *services.SmartDownloader) []*services.MediaInfo {
	var result []*services.MediaInfo
	chunkSize := albumChunkSize(len(mediaInfos))

	for start := 0; (start < len(mediaInfos)) && (result == nil); start += chunkSize {
		end := start + chunkSize

		if end > len(mediaInfos) {
			end = len(mediaInfos)
		}

		chunk := albumChunk(mediaInfos[start:end], (start > 0))
		log.Printf("Sending %d photos/videos as one album", len(chunk))
		err := albumSender(chunk, origChat, origMessage, url)
		isRejected := errors.Is(err, errAlbumRejected)

		if isRejected == true {
			log.Printf("⚠️ Album rejected, sending the remaining %d items separately: %v", (len(mediaInfos) - start), err)
			result = mediaInfos[start:]
		}

		if (err != nil) && (isRejected == false) {
			log.Printf("⚠️ Album outcome unknown, not resending its %d items: %v", len(chunk), err)
		}

		if isRejected == false {
			cleanupMediaFiles(chunk, downloader)
		}
	}

	if result == nil {
		result = []*services.MediaInfo{}
	}

	return result
}

// albumChunkSize splits count items into the fewest albums of near-equal size
// (11 -> 6+5), so no item is ever left alone.
func albumChunkSize(count int) int {
	chunkCount := (count + mediaGroupMaxItems - 1) / mediaGroupMaxItems
	result := count

	if chunkCount > 1 {
		result = (count + chunkCount - 1) / chunkCount
	}

	return result
}

// albumChunk returns the chunk to send; later chunks get a caption-less copy of
// their lead item, so the post caption appears once.
func albumChunk(items []*services.MediaInfo, isContinuation bool) []*services.MediaInfo {
	result := items

	if isContinuation == true {
		lead := *items[0]
		lead.Caption = ""
		result = append([]*services.MediaInfo{&lead}, items[1:]...)
	}

	return result
}

// canSendAsMixedAlbum reports whether every item can go into an album without
// losing text: the album only shows the first caption, so every other caption
// must be empty or already part of it (e.g. the original-link line), and the
// first caption must fit Telegram's limit.
func canSendAsMixedAlbum(mediaInfos []*services.MediaInfo) bool {
	result := (len(mediaInfos) >= mediaGroupMinItems)
	leadCaption := ""

	if result == true {
		leadCaption = mediaInfos[0].Caption
	}

	// A caption over Telegram's limit needs sendMediaFile's text split (Twitter)
	// instead of the album's truncation.
	isCaptionShort := (len(leadCaption) <= services.TelegramCaptionLimitBytes)
	result = result && isCaptionShort

	for _, item := range mediaInfos {
		isCaptionCovered := strings.Contains(leadCaption, item.Caption)
		result = result && isAlbumMedia(item) && isCaptionCovered
	}

	return result
}

// isAlbumMedia reports whether one item is an existing photo or MP4 video.
func isAlbumMedia(item *services.MediaInfo) bool {
	lowerName := strings.ToLower(item.Filename)
	isMP4 := strings.HasSuffix(lowerName, albumVideoExtension)
	isPhoto := (services.MediaTypePhoto == item.Type)
	isVideo := ((services.MediaTypeVideo == item.Type) && (isMP4 == true))
	_, statErr := os.Stat(item.Filename)
	result := ((isPhoto == true) || (isVideo == true)) && (statErr == nil)

	return result
}

// sendMediaAsGroup sends photos and/or videos as one album (2-10 items). A
// Telegram answer with an error wraps errAlbumRejected.
func sendMediaAsGroup(items []*services.MediaInfo, origChat *response.TgChat, origMessage *response.TgMessage, url string) error {
	var err error
	mediaArray := make([]payload.InputMedia, 0, len(items))
	filePaths := make([]string, 0, len(items))

	if len(items) == 0 {
		err = fmt.Errorf("no media to send")
	}

	for i, item := range items {
		mediaItem := albumInputMedia(item, i, origChat.Id, url)
		mediaArray = append(mediaArray, mediaItem)
		filePaths = append(filePaths, item.Filename)
	}

	if err == nil {
		groupPayload := payload.TgPlSendMediaGroup{
			ChatId:              origChat.Id,
			Media:               mediaArray,
			FilePaths:           filePaths,
			ReplyToMessageId:    origMessage.MessageId,
			DisableNotification: true,
		}
		sent := message.NewSendMediaGroup(groupPayload)
		err = albumSendError(sent)
	}

	if err == nil {
		trackAlbumSent(items, origChat, origMessage, url)
	}

	return err
}

// albumInputMedia builds one album entry; only the first one has a caption.
func albumInputMedia(item *services.MediaInfo, index int, chatID int64, url string) payload.InputMedia {
	isVideo := (services.MediaTypeVideo == item.Type)
	result := payload.InputMedia{
		Type:              services.MediaTypePhoto,
		Media:             fmt.Sprintf(albumAttachTemplate, index),
		HasSpoiler:        item.Spoiler,
		SupportsStreaming: isVideo,
	}

	if isVideo == true {
		result.Type = services.MediaTypeVideo
	}

	if (index == 0) && (item.Caption != "") {
		result.Caption, result.ParseMode = albumCaption(item, chatID, url)
	}

	return result
}

// albumCaption applies the chat's description setting (author line and the
// bot's notes only when descriptions are off) and Telegram's caption limit.
// Captions are sent with ParseModeHTML, so entities stay escaped; an
// over-long caption is cut at a rune boundary and sent as plain text, since a
// blind cut could split a tag.
func albumCaption(item *services.MediaInfo, chatID int64, url string) (string, string) {
	result := item.Caption
	parseMode := payload.ParseModeHTML
	platform := detectPlatformFromUrl(url)
	isDescriptionOff := (platform != "") && (services.IsDescriptionEnabledForChat(chatID, platform) == false)

	if isDescriptionOff == true {
		result = minimalCaption(item, platform)
	}

	if len(result) > services.TelegramCaptionLimitBytes {
		log.Printf("⚠️ Caption exceeds %d bytes (%d); truncating and disabling HTML parse mode", services.TelegramCaptionLimitBytes, len(result))
		cut := albumCaptionCut

		for (cut > 0) && ((result[cut] & 0xC0) == 0x80) {
			cut--
		}

		// Plain text: drop tags (profile and original links) and entities.
		withoutTags := htmlTagPattern.ReplaceAllString(result[:cut], "")
		plain := html.UnescapeString(withoutTags)
		result = fmt.Sprintf("%s...", plain)
		parseMode = ""
	}

	return result, parseMode
}

// albumSendError returns nil, errAlbumRejected (Telegram answered with an
// error) or the transport failure (no answer: the album may be delivered).
func albumSendError(sent *message.SendMediaGroup) error {
	var result error
	isAnswered := (sent.Response.StatusCode != 0)

	if sent.Err != nil {
		result = fmt.Errorf("sendMediaGroup failed: %w", sent.Err)
	}

	if (sent.Err != nil) && (isAnswered == true) {
		result = fmt.Errorf("%w: %v", errAlbumRejected, sent.Err)
	}

	return result
}

// trackAlbumSent records each sent item in the media stats.
func trackAlbumSent(items []*services.MediaInfo, origChat *response.TgChat, origMessage *response.TgMessage, url string) {
	userID := int64(origMessage.From.Id)

	for _, item := range items {
		trackMediaSent(userID, origMessage.From.Username, origChat.Id, origChat.Title, url, item.Type)
	}
}

// cleanupMediaFiles removes the temp files of sent items.
func cleanupMediaFiles(items []*services.MediaInfo, downloader *services.SmartDownloader) {
	for _, item := range items {
		downloader.CleanupFile(item.Filename)
	}
}
