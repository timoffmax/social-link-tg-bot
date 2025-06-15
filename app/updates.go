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
		log.Printf("Processing message from user %d in chat %d (type: %s, title: %s)", origUser.Id, origChat.Id, origChat.Type, origChat.Title)
		log.Printf("Original message text: %s", origMessage.Text)
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

	// Send confirmation message first to test chat access
	confirmMsg := payload.TgPlSendMessage{
		ChatId: origChat.Id,
		Text:   "📥 Downloaded video, testing file upload capability...",
	}
	confirmSend := message.NewSendMessage(confirmMsg)
	_, confirmErr := confirmSend.Request.Send(confirmSend.ResponsePayload)
	if confirmErr != nil {
		log.Printf("Failed to send confirmation message to chat %d: %v", origChat.Id, confirmErr)
		return
	}
	log.Printf("✅ Successfully sent confirmation message to chat %d", origChat.Id)
	
	// Test basic file upload capability with a tiny text file
	testFile := "temp/upload_test.txt"
	os.WriteFile(testFile, []byte("Test file upload capability"), 0644)
	defer os.Remove(testFile)
	
	testPayload := payload.TgPlSendDocument{
		ChatId:   origChat.Id,
		Document: "upload_test.txt",
		Caption:  "🧪 Testing basic file upload",
		FilePath: testFile,
	}
	testSend := message.NewSendDocument(testPayload)
	_, testErr := testSend.Request.Send(testSend.ResponsePayload)
	if testErr != nil {
		log.Printf("❌ Basic file upload test failed: %v", testErr)
		log.Printf("File upload capability not working, falling back to fixed link immediately")
		sendFixedLinkFallback(url, origChat)
		return
	} else {
		log.Printf("✅ Basic file upload test successful - proceeding with media upload")
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
			
			// Try fallback: send as document instead of video
			log.Printf("Trying fallback: sending video as document...")
			
			documentPayload := payload.TgPlSendDocument{
				ChatId:   origChat.Id,
				Document: "video.mp4",
				Caption:  "📹 Downloaded media (sent as document)",
				FilePath: mediaInfo.Filename,
			}
			sendDocument := message.NewSendDocument(documentPayload)
			_, docErr := sendDocument.Request.Send(sendDocument.ResponsePayload)
			if docErr != nil {
				log.Printf("Document fallback also failed: %v", docErr)
				
				// Final fallback: send fixed link and clean up file
				log.Printf("Falling back to fixed link due to upload failure")
				sendFixedLinkFallback(url, origChat)
				downloader.CleanupFile(mediaInfo.Filename)
			} else {
				log.Printf("✅ Successfully sent video as document: %s", mediaInfo.Filename)
				downloader.CleanupFile(mediaInfo.Filename)
			}
		} else {
			log.Printf("Successfully sent media file: %s", mediaInfo.Filename)
			// Clean up the file after successful sending
			downloader.CleanupFile(mediaInfo.Filename)
		}
	}
}

func sendMediaFile(mediaInfo *services.MediaInfo, origChat *response.TgChat) error {
	log.Printf("Sending media to chat ID: %d", origChat.Id)
	switch mediaInfo.Type {
	case "photo":
		photoPayload := payload.TgPlSendPhoto{
			ChatId:   origChat.Id,
			Photo:    "photo.jpg", // Simple filename for multipart upload
			Caption:  mediaInfo.Caption,
			FilePath: mediaInfo.Filename,
		}
		sendPhoto := message.NewSendPhoto(photoPayload)
		if sendPhoto.Request.Send(sendPhoto.ResponsePayload); sendPhoto.ResponsePayload == nil {
			return fmt.Errorf("failed to send photo")
		}

	case "video":
		videoPayload := payload.TgPlSendVideo{
			ChatId:   origChat.Id,
			Video:    "video.mp4", // Simple filename for multipart upload
			Caption:  mediaInfo.Caption,
			FilePath: mediaInfo.Filename,
		}
		sendVideo := message.NewSendVideo(videoPayload)
		_, err := sendVideo.Request.Send(sendVideo.ResponsePayload)
		if err != nil {
			return fmt.Errorf("failed to send video: %v", err)
		}

	default:
		documentPayload := payload.TgPlSendDocument{
			ChatId:   origChat.Id,
			Document: "document.bin", // Simple filename for multipart upload
			Caption:  mediaInfo.Caption,
			FilePath: mediaInfo.Filename,
		}
		sendDocument := message.NewSendDocument(documentPayload)
		_, err := sendDocument.Request.Send(sendDocument.ResponsePayload)
		if err != nil {
			return fmt.Errorf("failed to send document: %v", err)
		}
	}

	return nil
}

func sendFixedLinkFallback(originalUrl string, origChat *response.TgChat) {
	fixedUrl := getFixedUrl(originalUrl)
	if fixedUrl != "" {
		log.Printf("Sending fixed link fallback: %s", fixedUrl)
		fallbackMsg := payload.TgPlSendMessage{
			ChatId:    origChat.Id,
			ParseMode: payload.ParseModeHTML,
			Text:      fmt.Sprintf("⚠️ Media upload failed, here's a fixed link:\n%s", fixedUrl),
		}
		message.NewSendMessage(fallbackMsg)
	} else {
		// If no fixed link available, send error
		errorMsg := payload.TgPlSendMessage{
			ChatId: origChat.Id,
			Text:   fmt.Sprintf("❌ Failed to download and send media from %s", originalUrl),
		}
		message.NewSendMessage(errorMsg)
	}
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
