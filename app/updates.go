package app

import (
	"fmt"
	"log"
	"os"
	"strings"
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
		
		// Fallback to sending fixed link
		fixedUrl := getFixedUrl(url)
		if fixedUrl != "" {
			log.Printf("Falling back to fixed link: %s", fixedUrl)
			fallbackMsg := payload.TgPlSendMessage{
				ChatId:    origChat.Id,
				ParseMode: payload.ParseModeHTML,
				Text:      fixedUrl,
			}
			message.NewSendMessage(fallbackMsg)
		} else {
			// If no fixed link available, send error
			errorMsg := payload.TgPlSendMessage{
				ChatId: origChat.Id,
				Text:   fmt.Sprintf("❌ Failed to download media from %s", url),
			}
			message.NewSendMessage(errorMsg)
		}
		return
	}

	// Send each downloaded media file
	for _, mediaInfo := range mediaInfos {
		log.Printf("Attempting to send media file: %s (type: %s)", mediaInfo.Filename, mediaInfo.Type)
		
		// Check if file exists before sending
		if _, err := os.Stat(mediaInfo.Filename); os.IsNotExist(err) {
			log.Printf("Error: File does not exist: %s", mediaInfo.Filename)
			continue
		}
		
		err := sendMediaFile(mediaInfo, origChat)
		if err != nil {
			log.Printf("Failed to send media file %s: %v", mediaInfo.Filename, err)
			// Don't clean up failed files immediately for debugging
			log.Printf("Keeping file for debugging: %s", mediaInfo.Filename)
		} else {
			log.Printf("Successfully sent media file: %s", mediaInfo.Filename)
			// Clean up the file after successful sending
			downloader.CleanupFile(mediaInfo.Filename)
		}
	}
}

func sendMediaFile(mediaInfo *services.MediaInfo, origChat *response.TgChat) error {
	switch mediaInfo.Type {
	case "photo":
		photoPayload := payload.TgPlSendPhoto{
			ChatId:   origChat.Id,
			Photo:    "attach://photo", // Required for file uploads
			Caption:  mediaInfo.Caption,
			FilePath: mediaInfo.Filename,
		}
		message.NewSendPhoto(photoPayload)

	case "video":
		videoPayload := payload.TgPlSendVideo{
			ChatId:   origChat.Id,
			Video:    "attach://video", // Required for file uploads
			Caption:  mediaInfo.Caption,
			FilePath: mediaInfo.Filename,
		}
		message.NewSendVideo(videoPayload)

	default:
		documentPayload := payload.TgPlSendDocument{
			ChatId:   origChat.Id,
			Document: "attach://document", // Required for file uploads
			Caption:  mediaInfo.Caption,
			FilePath: mediaInfo.Filename,
		}
		message.NewSendDocument(documentPayload)
	}

	return nil
}

func getFixedUrl(url string) string {
	// Instagram
	if strings.Contains(url, "instagram.com") {
		return strings.Replace(url, "instagram.com", "instagramez.com", 1)
	}
	
	// TikTok
	if strings.Contains(url, "tiktok.com") {
		return strings.Replace(url, "tiktok.com", "vxtiktok.com", 1)
	}
	
	// Twitter/X
	if strings.Contains(url, "x.com") {
		return strings.Replace(url, "x.com", "fixupx.com", 1)
	}
	
	return ""
}
