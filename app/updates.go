package app

import (
	"fmt"
	"html"
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
		log.Printf("Processing update %d, message %d from user %d in chat %d (type: %s, title: %s)", upd.UpdateId, origMessage.MessageId, origUser.Id, origChat.Id, origChat.Type, origChat.Title)
		log.Printf("Original message text: %s", origMessage.Text)
	}

	if 0 == origUser.Id {
		return
	}

	// Get original social media links
	socialLinks := utils.GetAllSocialLinks(&origMessage)
	
	log.Printf("Found %d social media links: %v", len(socialLinks), socialLinks)

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
	for i, socialUrl := range socialLinks {
		log.Printf("Processing link %d/%d: %s", i+1, len(socialLinks), socialUrl)
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

	// Try to download media using platform-specific methods
	var mediaInfos []*services.MediaInfo
	var err error
	
	if strings.Contains(url, "twitter.com") || strings.Contains(url, "x.com") {
		mediaInfos, err = downloader.DownloadTwitterMedia(url)
	} else if strings.Contains(url, "instagram.com") {
		mediaInfos, err = downloader.DownloadInstagramMedia(url)
	} else if strings.Contains(url, "tiktok.com") {
		mediaInfos, err = downloader.DownloadTikTokMedia(url)
	} else {
		mediaInfos, err = downloader.DownloadMedia(url)
	}
	if err != nil {
		log.Printf("Failed to download media from %s: %v", url, err)
		
		// For Twitter/X posts, try to get text content even if no media found
		if strings.Contains(url, "twitter.com") || strings.Contains(url, "x.com") {
			if tweetText := tryGetTweetText(url, downloader); tweetText != "" {
				log.Printf("No media found, but extracted tweet text: %s", tweetText)
				textMsg := payload.TgPlSendMessage{
					ChatId:    origChat.Id,
					ParseMode: payload.ParseModeHTML,
					Text:      fmt.Sprintf("📝 Tweet content:\n\n%s\n\n🔗 <a href=\"%s\">View original</a>", tweetText, url),
				}
				message.NewSendMessage(textMsg)
				return
			}
		}
		
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

	// Check if we actually have any downloadable media files (not just text)
	hasActualMedia := false
	for _, mediaInfo := range mediaInfos {
		if mediaInfo.Type != "text" && mediaInfo.Filename != "" {
			hasActualMedia = true
			break
		}
	}

	// Only send confirmation message if we have actual media files
	if hasActualMedia {
		confirmMsg := payload.TgPlSendMessage{
			ChatId: origChat.Id,
			Text:   "📥 Downloaded media, sending...",
		}
		message.NewSendMessage(confirmMsg)
		// Note: NewSendMessage automatically sends the message
		log.Printf("✅ Successfully sent confirmation message to chat %d", origChat.Id)
	}

	// Group photos and send them together, send other media individually
	log.Printf("Downloaded %d media files from %s", len(mediaInfos), url)
	
	// Separate photos from other media types
	var photos []*services.MediaInfo
	var otherMedia []*services.MediaInfo
	
	for _, mediaInfo := range mediaInfos {
		if mediaInfo.Type == "photo" {
			// Check if file exists before adding to photos group
			if _, err := os.Stat(mediaInfo.Filename); os.IsNotExist(err) {
				log.Printf("Error: Photo file does not exist: %s", mediaInfo.Filename)
				continue
			}
			photos = append(photos, mediaInfo)
		} else {
			otherMedia = append(otherMedia, mediaInfo)
		}
	}
	
	// Send photos as a media group if there are multiple photos
	if len(photos) > 1 {
		log.Printf("Sending %d photos as media group", len(photos))
		err := sendPhotosAsMediaGroup(photos, origChat)
		if err != nil {
			log.Printf("Failed to send photos as media group: %v", err)
			// Fallback: send photos individually
			log.Printf("Fallback: sending photos individually")
			for i, photoInfo := range photos {
				log.Printf("Sending photo %d/%d: %s", i+1, len(photos), photoInfo.Filename)
				err := sendMediaFile(photoInfo, origChat)
				if err != nil {
					log.Printf("Failed to send photo file %s: %v", photoInfo.Filename, err)
				} else {
					log.Printf("Successfully sent photo file: %s", photoInfo.Filename)
				}
				downloader.CleanupFile(photoInfo.Filename)
			}
		} else {
			log.Printf("Successfully sent %d photos as media group", len(photos))
			// Clean up all photo files after successful sending
			for _, photoInfo := range photos {
				downloader.CleanupFile(photoInfo.Filename)
			}
		}
	} else if len(photos) == 1 {
		// Send single photo normally
		log.Printf("Sending single photo: %s", photos[0].Filename)
		err := sendMediaFile(photos[0], origChat)
		if err != nil {
			log.Printf("Failed to send photo file %s: %v", photos[0].Filename, err)
		} else {
			log.Printf("Successfully sent photo file: %s", photos[0].Filename)
		}
		downloader.CleanupFile(photos[0].Filename)
	}
	
	// Send other media types individually
	for i, mediaInfo := range otherMedia {
		log.Printf("Sending media file %d/%d: %s (type: %s)", i+1, len(otherMedia), mediaInfo.Filename, mediaInfo.Type)
		
		// Check if file exists before sending (skip for text-only content)
		if mediaInfo.Type != "text" {
			if _, err := os.Stat(mediaInfo.Filename); os.IsNotExist(err) {
				log.Printf("Error: File does not exist: %s", mediaInfo.Filename)
				continue
			}
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
			message.NewSendDocument(documentPayload)
			// Note: NewSendDocument automatically sends the message
			log.Printf("✅ Successfully sent video as document: %s", mediaInfo.Filename)
			downloader.CleanupFile(mediaInfo.Filename)
		} else {
			log.Printf("Successfully sent media file: %s", mediaInfo.Filename)
			// Clean up the file after successful sending
			downloader.CleanupFile(mediaInfo.Filename)
		}
	}
}

func sendMediaFile(mediaInfo *services.MediaInfo, origChat *response.TgChat) error {
	log.Printf("Sending media to chat ID: %d", origChat.Id)
	
	// Decode HTML entities and limit caption length to Telegram's 1024 character limit
	caption := html.UnescapeString(mediaInfo.Caption)
	if len(caption) > 1000 {
		caption = caption[:997] + "..."
	}
	
	switch mediaInfo.Type {
	case "text":
		// Send text-only content as a message
		textPayload := payload.TgPlSendMessage{
			ChatId: origChat.Id,
			Text:   mediaInfo.Caption,
		}
		message.NewSendMessage(textPayload)
		// Note: NewSendMessage automatically sends the message
		
	case "photo":
		photoPayload := payload.TgPlSendPhoto{
			ChatId:   origChat.Id,
			Photo:    "photo.jpg", // Simple filename for multipart upload
			Caption:  caption,
			FilePath: mediaInfo.Filename,
		}
		response := message.NewSendPhoto(photoPayload)
		// Check if the photo was sent successfully
		if response.ResponsePayload != nil && response.ResponsePayload.MessageId != 0 {
			log.Printf("✅ Photo sent successfully! Message ID: %d", response.ResponsePayload.MessageId)
		} else {
			log.Printf("❌ Photo sending failed or no response received")
			log.Printf("Response details: %+v", response)
			return fmt.Errorf("failed to send photo")
		}

	case "video":
		videoPayload := payload.TgPlSendVideo{
			ChatId:   origChat.Id,
			Video:    "video.mp4", // Simple filename for multipart upload
			Caption:  caption,
			FilePath: mediaInfo.Filename,
		}
		message.NewSendVideo(videoPayload)
		// Note: NewSendVideo automatically sends the message

	default:
		documentPayload := payload.TgPlSendDocument{
			ChatId:   origChat.Id,
			Document: "document.bin", // Simple filename for multipart upload
			Caption:  caption,
			FilePath: mediaInfo.Filename,
		}
		message.NewSendDocument(documentPayload)
		// Note: NewSendDocument automatically sends the message
	}

	return nil
}

func sendPhotosAsMediaGroup(photos []*services.MediaInfo, origChat *response.TgChat) error {
	if len(photos) == 0 {
		return fmt.Errorf("no photos to send")
	}
	
	// Create media array for the media group
	var mediaArray []payload.InputMediaPhoto
	var filePaths []string
	
	for i, photoInfo := range photos {
		// Use attach:// prefix for file uploads in media groups
		mediaItem := payload.InputMediaPhoto{
			Type:  "photo",
			Media: fmt.Sprintf("attach://photo%d", i),
		}
		
		// Only add caption to the first photo to avoid clutter
		if i == 0 && photoInfo.Caption != "" && !strings.HasPrefix(photoInfo.Caption, "#") {
			mediaItem.Caption = photoInfo.Caption
		}
		
		mediaArray = append(mediaArray, mediaItem)
		filePaths = append(filePaths, photoInfo.Filename)
	}
	
	// Create the media group payload
	mediaGroupPayload := payload.TgPlSendMediaGroup{
		ChatId:    origChat.Id,
		Media:     mediaArray,
		FilePaths: filePaths,
	}
	
	// Send the media group
	message.NewSendMediaGroup(mediaGroupPayload)
	
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
			Text:   getErrorMessage(originalUrl),
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
	
	// 9GAG and YouTube don't have fixed link services
	// Return empty string to trigger fallback message
	
	return ""
}

func getErrorMessage(url string) string {
	if strings.Contains(url, "youtube.com") || strings.Contains(url, "youtu.be") {
		return "❌ YouTube download failed due to bot detection.\n\n" +
			"To fix this, add YouTube cookies:\n" +
			"1. Export cookies from browser\n" +
			"2. Save as youtube_cookies.txt\n" +
			"3. Place in bot directory"
	} else if strings.Contains(url, "9gag.com") {
		return "❌ Failed to download from 9GAG.\n\n" +
			"9GAG download support is limited."
	}
	
	return fmt.Sprintf("❌ Failed to download media from %s", url)
}

func tryGetTweetText(url string, downloader *services.YtDlpDownloader) string {
	// Try to get tweet info using yt-dlp JSON dump
	info, err := downloader.GetMediaInfo(url)
	if err != nil {
		log.Printf("Failed to get tweet info: %v", err)
		return ""
	}
	
	// Return the description (tweet text) if available
	if info.Description != "" {
		return info.Description
	}
	
	// Fallback to title if description is empty
	if info.Title != "" {
		return info.Title
	}
	
	return ""
}
