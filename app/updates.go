package app

import (
	"fmt"
	"html"
	"log"
	"os"
	"strings"
	"sync"
	"time"
	"github.com/timoffmax/social-link-bot/model/api/request/message"
	"github.com/timoffmax/social-link-bot/model/api/request/payload"
	"github.com/timoffmax/social-link-bot/model/api/response"
	"github.com/timoffmax/social-link-bot/model/api/response/utils"
	"github.com/timoffmax/social-link-bot/services"
)

var (
	processedUpdates = make(map[int64]bool)
	updatesMutex     sync.RWMutex
)

func listenForUpdates() {
	var offset int
	isFirstRun := true

	// Start a goroutine to periodically clean up old processed update IDs
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			cleanupOldUpdates()
		}
	}()

	for {
		lastProcessedUpd := processRecentUpdates(offset, isFirstRun)

		if nil != lastProcessedUpd {
			offset = lastProcessedUpd.UpdateId + 1
		}
		
		isFirstRun = false
	}
}

func processRecentUpdates(offset int, isFirstRun bool) *response.TgUpdate {
	var lastProcessedUpd *response.TgUpdate
	updates := getRecentUpdates(offset)

	if len(updates) == 0 {
		return nil
	}

	// Filter out old updates on first run
	var validUpdates []*response.TgUpdate
	for _, update := range updates {
		if isFirstRun && isUpdateTooOld(update) {
			log.Printf("Skipping old update %d (too old for first run)", update.UpdateId)
			lastProcessedUpd = update
			continue
		}
		validUpdates = append(validUpdates, update)
	}

	// Process updates concurrently for better performance
	if len(validUpdates) == 1 {
		// Single update - process directly
		log.Printf("📱 [SINGLE UPDATE] Processing update %d", validUpdates[0].UpdateId)
		processUpdate(validUpdates[0])
		lastProcessedUpd = validUpdates[0]
	} else if len(validUpdates) > 1 {
		// Multiple updates - process concurrently
		log.Printf("🚀 [MULTI-UPDATE] Starting concurrent processing of %d updates", len(validUpdates))
		var wg sync.WaitGroup
		
		for i, update := range validUpdates {
			wg.Add(1)
			go func(index int, upd *response.TgUpdate) {
				defer wg.Done()
				log.Printf("📱 [UPDATE-THREAD-%d] START: Processing update %d", index+1, upd.UpdateId)
				processUpdate(upd)
				log.Printf("✅ [UPDATE-THREAD-%d] DONE: Finished update %d", index+1, upd.UpdateId)
			}(i, update)
		}
		
		log.Printf("⏳ [MULTI-UPDATE] Waiting for all %d update threads to complete...", len(validUpdates))
		wg.Wait()
		log.Printf("✅ [MULTI-UPDATE] All %d updates processed successfully", len(validUpdates))
		
		// Return the last update in the batch for offset tracking
		lastProcessedUpd = validUpdates[len(validUpdates)-1]
	}

	// If we had updates but all were filtered out, return the last filtered one
	if lastProcessedUpd == nil && len(updates) > 0 {
		lastProcessedUpd = updates[len(updates)-1]
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
	// Check for duplicate updates
	updatesMutex.Lock()
	if processedUpdates[int64(upd.UpdateId)] {
		log.Printf("🔄 [DUPLICATE] Skipping already processed update %d", upd.UpdateId)
		updatesMutex.Unlock()
		return
	}
	processedUpdates[int64(upd.UpdateId)] = true
	updatesMutex.Unlock()

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

	// Initialize smart downloader
	downloader := services.NewSmartDownloader()
	
	// Check if downloader is available, fallback to fixed links if not
	if !downloader.IsAvailable() {
		log.Println("Downloader not available, falling back to fixed links")
		sendFixedLinks(&origMessage, &origChat)
		return
	}

	// Process social media links concurrently for better performance
	if len(socialLinks) == 1 {
		// Single link - process directly (no need for goroutine overhead)
		log.Printf("🔗 [SINGLE] Processing link: %s", socialLinks[0])
		processMediaDownload(socialLinks[0], &origChat, &origMessage, downloader)
		log.Printf("🏁 [SINGLE] Finished processing link: %s", socialLinks[0])
	} else {
		// Multiple links - process concurrently with goroutines
		log.Printf("🚀 [MULTI-THREAD] Starting concurrent processing of %d links", len(socialLinks))
		var wg sync.WaitGroup
		
		for i, socialUrl := range socialLinks {
			wg.Add(1)
			go func(index int, url string) {
				defer wg.Done()
				log.Printf("🔗 [THREAD-%d] START: Processing link %d/%d: %s", index+1, index+1, len(socialLinks), url)
				processMediaDownload(url, &origChat, &origMessage, downloader)
				log.Printf("🏁 [THREAD-%d] DONE: Finished processing link %d/%d: %s", index+1, index+1, len(socialLinks), url)
			}(i, socialUrl)
		}
		
		log.Printf("⏳ [MULTI-THREAD] Waiting for all %d threads to complete...", len(socialLinks))
		wg.Wait()
		log.Printf("✅ [MULTI-THREAD] All %d links processed successfully", len(socialLinks))
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

		response := message.NewSendMessage(requestPayload)
		services.LogTelegramMessage(origChat.Id, "fixed-link", response != nil, "")
	}
}

func processMediaDownload(url string, origChat *response.TgChat, origMessage *response.TgMessage, downloader *services.SmartDownloader) {
	log.Printf("🚀 [DEBUG] Starting processMediaDownload for URL: %s", url)
	// Send "typing" status to indicate processing
	// (You could implement sendChatAction here)

	// Try to download media using smart downloader
	mediaInfos, err := downloader.DownloadMedia(url)
	if err != nil {
		log.Printf("Failed to download media from %s: %v", url, err)
		
		// Skip error notifications for 9GAG (silently fail)
		if strings.Contains(url, "9gag.com") {
			log.Printf("🤐 Skipping error notification for 9GAG link")
			services.LogTelegramMessage(origChat.Id, "error-skipped", false, "")
			return
		}
		
		// Send error message for other platforms
		errorMsg := payload.TgPlSendMessage{
			ChatId:              origChat.Id,
			Text:                getErrorMessage(url),
			ReplyToMessageId:    origMessage.MessageId,
			DisableNotification: true,
		}
		response := message.NewSendMessage(errorMsg)
		services.LogTelegramMessage(origChat.Id, "error", response != nil, "")
		return
	}
	
	// Check if this is a fixed link response (text-only with no filename)
	if len(mediaInfos) == 1 && mediaInfos[0].Type == "text" && mediaInfos[0].Filename == "" {
		// Send the fixed link as reply with silent notification
		fixedLinkMsg := payload.TgPlSendMessage{
			ChatId:              origChat.Id,
			ParseMode:           payload.ParseModeHTML,
			Text:                mediaInfos[0].Caption,
			ReplyToMessageId:    origMessage.MessageId,
			DisableNotification: true,
		}
		response := message.NewSendMessage(fixedLinkMsg)
		services.LogTelegramMessage(origChat.Id, "fixed-link", response != nil, "")
		return
	}

	// Skip confirmation message - send media directly for better UX

	// Group photos and send them together, send other media individually
	log.Printf("Downloaded %d media files from %s", len(mediaInfos), url)
	
	// Debug: Log all media items
	for i, media := range mediaInfos {
		captionPreview := media.Caption
		if len(captionPreview) > 50 {
			captionPreview = captionPreview[:50] + "..."
		}
		log.Printf("🔍 [DEBUG] Media %d: Type=%s, URL=%s, Caption=%s, Filename=%s", 
			i+1, media.Type, media.URL, captionPreview, media.Filename)
	}
	
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
	
	// For TikTok slideshows, ensure only the first photo has the generic caption
	if strings.Contains(url, "tiktok.com") && len(photos) > 1 {
		for i := 1; i < len(photos); i++ {
			// Clear caption for subsequent photos if it's the generic "Downloaded from TikTok"
			if photos[i].Caption == "Downloaded from TikTok" {
				photos[i].Caption = ""
			}
		}
	}
	
	// Send photos as a media group if there are multiple photos
	if len(photos) > 1 {
		log.Printf("Sending %d photos as media group", len(photos))
		err := sendPhotosAsMediaGroup(photos, origChat, origMessage)
		if err != nil {
			log.Printf("Failed to send photos as media group: %v", err)
			// Fallback: send photos individually
			log.Printf("Fallback: sending photos individually")
			for i, photoInfo := range photos {
				log.Printf("Sending photo %d/%d: %s", i+1, len(photos), photoInfo.Filename)
				err := sendMediaFile(photoInfo, origChat, origMessage)
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
		err := sendMediaFile(photos[0], origChat, origMessage)
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
		
		err := sendMediaFile(mediaInfo, origChat, origMessage)
		if err != nil {
			log.Printf("Failed to send media file %s: %v", mediaInfo.Filename, err)
			
			// Try fallback: send as document instead of video
			log.Printf("Trying fallback: sending video as document...")
			
			documentPayload := payload.TgPlSendDocument{
				ChatId:              origChat.Id,
				Document:            "video.mp4",
				Caption:             "📹 Downloaded media (sent as document)",
				FilePath:            mediaInfo.Filename,
				ReplyToMessageId:    origMessage.MessageId,
				DisableNotification: true,
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

func sendMediaFile(mediaInfo *services.MediaInfo, origChat *response.TgChat, origMessage *response.TgMessage) error {
	log.Printf("Sending media to chat ID: %d", origChat.Id)
	
	// Decode HTML entities
	caption := html.UnescapeString(mediaInfo.Caption)
	
	// For Twitter/X posts, handle long text intelligently
	if (strings.Contains(mediaInfo.URL, "twitter.com") || strings.Contains(mediaInfo.URL, "x.com")) && 
	   len(caption) > 1024 && mediaInfo.Type != "text" {
		log.Printf("🐦 Twitter post exceeds Telegram limit (%d chars), sending text and media separately", len(caption))
		
		// Send text first (only if it's not a generic message) with reply and silent notification
		if caption != "Downloaded from Twitter/X" && caption != "Downloaded from Twitter" && caption != "" {
			textPayload := payload.TgPlSendMessage{
				ChatId:              origChat.Id,
				Text:                caption,
				ReplyToMessageId:    origMessage.MessageId,
				DisableNotification: true,
			}
			message.NewSendMessage(textPayload)
			log.Printf("✅ Sent Twitter text content separately")
		}
		
		// Send media without caption 
		caption = ""
	} else {
		// Normal handling - limit caption length to Telegram's 1024 character limit
		if len(caption) > 1020 {
			caption = caption[:1017] + "..."
		}
	}
	
	switch mediaInfo.Type {
	case "text":
		// Send text-only content as a message with reply and silent notification
		textPayload := payload.TgPlSendMessage{
			ChatId:              origChat.Id,
			Text:                mediaInfo.Caption,
			ReplyToMessageId:    origMessage.MessageId,
			DisableNotification: true,
		}
		message.NewSendMessage(textPayload)
		// Note: NewSendMessage automatically sends the message
		
	case "photo":
		photoPayload := payload.TgPlSendPhoto{
			ChatId:              origChat.Id,
			Photo:               "photo.jpg", // Simple filename for multipart upload
			Caption:             caption,
			FilePath:            mediaInfo.Filename,
			ReplyToMessageId:    origMessage.MessageId,
			DisableNotification: true,
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
		finalVideoPath := mediaInfo.Filename
		
		// Check if this is a WebM file that needs conversion
		if strings.HasSuffix(strings.ToLower(mediaInfo.Filename), ".webm") {
			log.Printf("🎬 WebM video detected, attempting conversion to MP4")
			
			// Initialize video converter with proper working directory
			workingDir, _ := os.Getwd()
			videoConverter := services.NewVideoConverter(workingDir, os.TempDir())
			
			if videoConverter.IsAvailable() {
				// Try to convert WebM to MP4
				convertedPath, err := videoConverter.ConvertWebMToMP4(mediaInfo.Filename)
				if err != nil {
					log.Printf("❌ WebM conversion failed: %v, falling back to document", err)
					
					// Fallback: send as document
					documentPayload := payload.TgPlSendDocument{
						ChatId:              origChat.Id,
						Document:            "video.webm",
						Caption:             fmt.Sprintf("🎥 Video (WebM format - conversion failed)\n\n%s", caption),
						FilePath:            mediaInfo.Filename,
						ReplyToMessageId:    origMessage.MessageId,
						DisableNotification: true,
					}
					message.NewSendDocument(documentPayload)
					return nil
				} else {
					log.Printf("✅ Successfully converted WebM to MP4: %s", convertedPath)
					finalVideoPath = convertedPath
					
					// Clean up original WebM file after successful conversion
					defer func() {
						if err := os.Remove(mediaInfo.Filename); err != nil {
							log.Printf("⚠️ Failed to cleanup original WebM file: %v", err)
						}
						// Clean up converted MP4 file after sending
						defer func() {
							if err := os.Remove(convertedPath); err != nil {
								log.Printf("⚠️ Failed to cleanup converted MP4 file: %v", err)
							}
						}()
					}()
				}
			} else {
				log.Printf("⚠️ ffmpeg not available, sending WebM as document")
				
				// Fallback: send as document
				documentPayload := payload.TgPlSendDocument{
					ChatId:              origChat.Id,
					Document:            "video.webm",
					Caption:             fmt.Sprintf("🎥 Video (WebM format)\n\n%s", caption),
					FilePath:            mediaInfo.Filename,
					ReplyToMessageId:    origMessage.MessageId,
					DisableNotification: true,
				}
				message.NewSendDocument(documentPayload)
				return nil
			}
		}
		
		// Send as video (either original MP4 or converted from WebM) with reply and silent notification
		videoPayload := payload.TgPlSendVideo{
			ChatId:              origChat.Id,
			Video:               "video.mp4", // Simple filename for multipart upload
			Caption:             caption,
			FilePath:            finalVideoPath,
			ReplyToMessageId:    origMessage.MessageId,
			DisableNotification: true,
		}
		message.NewSendVideo(videoPayload)
		// Note: NewSendVideo automatically sends the message

	default:
		// Determine the document type for better user experience
		var documentName string
		var enhancedCaption string
		
		if strings.HasSuffix(strings.ToLower(mediaInfo.Filename), ".webm") {
			documentName = "video.webm"
			enhancedCaption = fmt.Sprintf("🎥 Video (WebM format)\n\n%s", caption)
			log.Printf("📹 Sending WebM video as document for better compatibility")
		} else {
			documentName = "document.bin"
			enhancedCaption = caption
		}
		
		documentPayload := payload.TgPlSendDocument{
			ChatId:              origChat.Id,
			Document:            documentName,
			Caption:             enhancedCaption,
			FilePath:            mediaInfo.Filename,
			ReplyToMessageId:    origMessage.MessageId,
			DisableNotification: true,
		}
		message.NewSendDocument(documentPayload)
		// Note: NewSendDocument automatically sends the message
	}

	return nil
}

func sendPhotosAsMediaGroup(photos []*services.MediaInfo, origChat *response.TgChat, origMessage *response.TgMessage) error {
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
		if i == 0 && photoInfo.Caption != "" {
			// Decode HTML entities and limit caption length to Telegram's 1024 character limit
			caption := html.UnescapeString(photoInfo.Caption)
			if len(caption) > 1020 {
				caption = caption[:1017] + "..."
			}
			mediaItem.Caption = caption
		}
		
		mediaArray = append(mediaArray, mediaItem)
		filePaths = append(filePaths, photoInfo.Filename)
	}
	
	// Create the media group payload with reply and silent notification
	mediaGroupPayload := payload.TgPlSendMediaGroup{
		ChatId:              origChat.Id,
		Media:               mediaArray,
		FilePaths:           filePaths,
		ReplyToMessageId:    origMessage.MessageId,
		DisableNotification: true,
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
	if strings.Contains(url, "twitter.com") {
		return strings.Replace(url, "twitter.com", "fixupx.com", 1)
	}
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
			"9GAG content may be region-restricted or unavailable."
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


// isUpdateTooOld checks if an update is older than 1 minute
func isUpdateTooOld(update *response.TgUpdate) bool {
	if update.Message.Date == 0 {
		return false
	}
	
	updateTime := time.Unix(int64(update.Message.Date), 0)
	oneMinuteAgo := time.Now().Add(-1 * time.Minute)
	
	return updateTime.Before(oneMinuteAgo)
}

// cleanupOldUpdates removes processed update IDs older than 1 hour to prevent memory leaks
func cleanupOldUpdates() {
	updatesMutex.Lock()
	defer updatesMutex.Unlock()
	
	// Keep only the last 1000 update IDs to prevent unbounded memory growth
	if len(processedUpdates) > 1000 {
		log.Printf("🧹 Cleaning up old processed update IDs (current count: %d)", len(processedUpdates))
		
		// Clear the map and start fresh
		processedUpdates = make(map[int64]bool)
		
		log.Printf("✅ Cleaned up processed update IDs")
	}
}
