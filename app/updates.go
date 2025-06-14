package app

import (
	"fmt"
	"log"
	"github.com/timoffmax/social-link-bot/model/api/request/message"
	"github.com/timoffmax/social-link-bot/model/api/request/payload"
	"github.com/timoffmax/social-link-bot/model/api/response"
	"github.com/timoffmax/social-link-bot/model/api/response/utils"
	"github.com/timoffmax/social-link-bot/services"
)

func listenForUpdates() {
	var offset int

	for {
		lastProcessedUpd := processRecentUpdates(offset)

		if nil != lastProcessedUpd {
			offset = lastProcessedUpd.UpdateId + 1
		}
	}
}

func processRecentUpdates(offset int) *response.TgUpdate {
	var lastProcessedUpd *response.TgUpdate
	updates := getRecentUpdates(offset)

	for _, update := range updates {
		processUpdate(update)
		lastProcessedUpd = update
	}

	return lastProcessedUpd
}

func getRecentUpdates(offset int) []*response.TgUpdate {
	updates := message.NewGetUpdates(payload.TgPlGetUpdates{
		Offset:         offset,
		AllowedUpdates: []string{"message"},
	})

	respPayload := updates.ResponsePayload
	result := respPayload.Items

	return result
}

func processUpdate(upd *response.TgUpdate) {
	var origMessage response.TgMessage
	var origUser response.TgUser
	var origChat response.TgChat

	if 0 != upd.Message.MessageId {
		origMessage = upd.Message
		origUser = origMessage.From
		origChat = origMessage.Chat
	}

	if 0 == origUser.Id {
		return
	}

	// Get original social media links
	socialLinks := utils.GetAllSocialLinks(&origMessage)

	if len(socialLinks) == 0 {
		return
	}

	// Initialize downloader
	downloader := services.NewYtDlpDownloader()
	
	// Check if yt-dlp is available, fallback to fixed links if not
	if !downloader.IsAvailable() {
		log.Println("yt-dlp not available, falling back to fixed links")
		sendFixedLinks(&origMessage, &origChat)
		return
	}

	// Process each social media link
	for _, socialUrl := range socialLinks {
		processMediaDownload(socialUrl, &origChat, downloader)
	}
}

func sendFixedLinks(origMessage *response.TgMessage, origChat *response.TgChat) {
	fixedLinks := utils.GetAllLinks(origMessage)

	for _, fixedUrl := range fixedLinks {
		requestPayload := payload.TgPlSendMessage{
			ChatId:    origChat.Id,
			ParseMode: payload.ParseModeHTML,
		}

		requestPayload.Text = fixedUrl

		message.NewSendMessage(requestPayload)
	}
}

func processMediaDownload(url string, origChat *response.TgChat, downloader *services.YtDlpDownloader) {
	// Send "typing" status to indicate processing
	// (You could implement sendChatAction here)

	// Try to download media
	mediaInfos, err := downloader.DownloadMedia(url)
	if err != nil {
		log.Printf("Failed to download media from %s: %v", url, err)
		
		// Fallback to sending error message
		errorMsg := payload.TgPlSendMessage{
			ChatId: origChat.Id,
			Text:   fmt.Sprintf("❌ Failed to download media from %s", url),
		}
		message.NewSendMessage(errorMsg)
		return
	}

	// Send each downloaded media file
	for _, mediaInfo := range mediaInfos {
		err := sendMediaFile(mediaInfo, origChat)
		if err != nil {
			log.Printf("Failed to send media file %s: %v", mediaInfo.Filename, err)
		}

		// Clean up the file after sending
		downloader.CleanupFile(mediaInfo.Filename)
	}
}

func sendMediaFile(mediaInfo *services.MediaInfo, origChat *response.TgChat) error {
	switch mediaInfo.Type {
	case "photo":
		photoPayload := payload.TgPlSendPhoto{
			ChatId:   origChat.Id,
			Caption:  mediaInfo.Caption,
			FilePath: mediaInfo.Filename,
		}
		message.NewSendPhoto(photoPayload)

	case "video":
		videoPayload := payload.TgPlSendVideo{
			ChatId:   origChat.Id,
			Caption:  mediaInfo.Caption,
			FilePath: mediaInfo.Filename,
		}
		message.NewSendVideo(videoPayload)

	default:
		documentPayload := payload.TgPlSendDocument{
			ChatId:   origChat.Id,
			Caption:  mediaInfo.Caption,
			FilePath: mediaInfo.Filename,
		}
		message.NewSendDocument(documentPayload)
	}

	return nil
}
