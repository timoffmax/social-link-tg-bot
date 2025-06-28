package app

import (
	"fmt"
	"html"
	"log"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
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

	// Process ALL updates concurrently, even single ones
	if len(validUpdates) > 0 {
		log.Printf("🚀 [CONCURRENT] Starting concurrent processing of %d update(s)", len(validUpdates))
		
		// Process each update in its own goroutine
		for i, update := range validUpdates {
			go func(index int, upd *response.TgUpdate) {
				log.Printf("📱 [UPDATE-THREAD-%d] START: Processing update %d from user %d", 
					index+1, upd.UpdateId, upd.Message.From.Id)
				processUpdate(upd)
				log.Printf("✅ [UPDATE-THREAD-%d] DONE: Finished update %d from user %d", 
					index+1, upd.UpdateId, upd.Message.From.Id)
			}(i, update)
		}
		
		// Don't wait for goroutines to complete - let them run in background
		// This allows the bot to immediately fetch and process new updates
		log.Printf("🏃 [CONCURRENT] Launched %d update thread(s), continuing to poll for new updates", len(validUpdates))
		
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
		Timeout:        5,  // Short polling timeout (5 seconds) for faster response to new messages
		Limit:          100, // Get up to 100 updates at once
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

	// Check for Twitter threads before processing
	twitterThreads := detectTwitterThreads(socialLinks)
	if len(twitterThreads) > 0 {
		log.Printf("🧵 [THREAD] Detected %d Twitter thread(s)", len(twitterThreads))
		for i, thread := range twitterThreads {
			log.Printf("🧵 [THREAD-%d] Processing thread with %d tweets", i+1, len(thread.URLs))
			processTwitterThread(thread, &origChat, &origMessage, downloader)
		}
		return
	}

	// Check for automatic Twitter thread discovery from single URLs (only for 1-2 URLs to avoid overhead)
	if len(socialLinks) <= 2 {
		autoThreads := discoverTwitterThreads(socialLinks, downloader)
		if len(autoThreads) > 0 {
			log.Printf("🧵 [AUTO-THREAD] Discovered %d automatic thread(s)", len(autoThreads))
			for i, thread := range autoThreads {
				log.Printf("🧵 [AUTO-THREAD-%d] Processing discovered thread with %d tweets", i+1, len(thread.URLs))
				processTwitterThread(thread, &origChat, &origMessage, downloader)
			}
			return
		}
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
		
		// Skip error notifications for platforms that commonly fail (silently fail)
		if strings.Contains(url, "9gag.com") || strings.Contains(url, "reddit.com") || strings.Contains(url, "facebook.com") {
			platformName := "9GAG"
			if strings.Contains(url, "reddit.com") {
				platformName = "Reddit"
			} else if strings.Contains(url, "facebook.com") {
				platformName = "Facebook"
			}
			log.Printf("🤐 Skipping error notification for %s link", platformName)
			services.LogTelegramMessage(origChat.Id, "error-skipped", false, "")
			return
		}
		
		// For Instagram, TikTok, and Twitter - try fixed link fallback
		fixedUrl := getFixedUrl(url)
		if fixedUrl != "" {
			log.Printf("📎 Falling back to fixed link for %s", url)
			fixedLinkMsg := payload.TgPlSendMessage{
				ChatId:              origChat.Id,
				ParseMode:           payload.ParseModeHTML,
				Text:                fixedUrl,
				ReplyToMessageId:    origMessage.MessageId,
				DisableNotification: true,
			}
			response := message.NewSendMessage(fixedLinkMsg)
			services.LogTelegramMessage(origChat.Id, "fixed-link-fallback", response != nil, "")
			return
		}
		
		// Send error message for other platforms
		errorText := getErrorMessage(url)
		if errorText != "" {
			errorMsg := payload.TgPlSendMessage{
				ChatId:              origChat.Id,
				Text:                errorText,
				ReplyToMessageId:    origMessage.MessageId,
				DisableNotification: true,
			}
			response := message.NewSendMessage(errorMsg)
			services.LogTelegramMessage(origChat.Id, "error", response != nil, "")
		}
		return
	}
	
	// Check if this is a fixed link response (text-only with no filename)
	if len(mediaInfos) == 1 && mediaInfos[0].Type == "text" && mediaInfos[0].Filename == "" {
		// Add clean original URL to text content
		cleanURL := cleanURLParams(url)
		textWithURL := mediaInfos[0].Caption + fmt.Sprintf("\n\n🔗 %s", cleanURL)
		
		// Send the fixed link as reply with silent notification
		fixedLinkMsg := payload.TgPlSendMessage{
			ChatId:              origChat.Id,
			ParseMode:           payload.ParseModeHTML,
			Text:                textWithURL,
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
		errorText := getErrorMessage(originalUrl)
		if errorText != "" {
			errorMsg := payload.TgPlSendMessage{
				ChatId: origChat.Id,
				Text:   errorText,
			}
			message.NewSendMessage(errorMsg)
		}
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
		// Don't send YouTube error messages to Telegram
		return ""
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

// TwitterThread represents a detected Twitter thread
type TwitterThread struct {
	URLs     []string // Ordered list of Twitter URLs in the thread
	Username string   // Twitter username (same for all tweets in thread)
	TweetIDs []int64  // Ordered list of tweet IDs
}

// detectTwitterThreads analyzes a list of URLs and detects Twitter threads
func detectTwitterThreads(urls []string) []TwitterThread {
	// Regex to extract Twitter/X URLs and parse them
	twitterRegex := regexp.MustCompile(`https?://(twitter|x)\.com/([^/]+)/status/(\d+)`)
	
	// Map to group tweets by username
	tweetsByUser := make(map[string][]TwitterTweet)
	
	// Parse all Twitter URLs
	for _, url := range urls {
		matches := twitterRegex.FindStringSubmatch(url)
		if len(matches) >= 4 {
			username := matches[2]
			tweetIDStr := matches[3]
			tweetID, err := strconv.ParseInt(tweetIDStr, 10, 64)
			if err != nil {
				continue
			}
			
			tweet := TwitterTweet{
				URL:      url,
				TweetID:  tweetID,
				Username: username,
			}
			
			tweetsByUser[username] = append(tweetsByUser[username], tweet)
		}
	}
	
	var threads []TwitterThread
	
	// Check each user's tweets for thread patterns
	for username, tweets := range tweetsByUser {
		if len(tweets) < 2 {
			continue // Need at least 2 tweets for a thread
		}
		
		// Sort tweets by ID (chronological order)
		sort.Slice(tweets, func(i, j int) bool {
			return tweets[i].TweetID < tweets[j].TweetID
		})
		
		// Detect consecutive threads
		thread := TwitterThread{
			Username: username,
			URLs:     []string{tweets[0].URL},
			TweetIDs: []int64{tweets[0].TweetID},
		}
		
		for i := 1; i < len(tweets); i++ {
			// Check if this tweet could be part of the thread
			// Twitter IDs are roughly sequential, allow some gap but not too much
			idDiff := tweets[i].TweetID - tweets[i-1].TweetID
			
			if idDiff > 0 && idDiff < 10000000000 { // Reasonable gap for thread replies
				// Add to current thread
				thread.URLs = append(thread.URLs, tweets[i].URL)
				thread.TweetIDs = append(thread.TweetIDs, tweets[i].TweetID)
			} else {
				// Gap too large, finish current thread and start new one
				if len(thread.URLs) >= 2 {
					threads = append(threads, thread)
				}
				
				// Start new thread
				thread = TwitterThread{
					Username: username,
					URLs:     []string{tweets[i].URL},
					TweetIDs: []int64{tweets[i].TweetID},
				}
			}
		}
		
		// Add final thread if it has multiple tweets
		if len(thread.URLs) >= 2 {
			threads = append(threads, thread)
		}
	}
	
	return threads
}

// TwitterTweet represents a single tweet in potential thread detection
type TwitterTweet struct {
	URL      string
	TweetID  int64
	Username string
}

// processTwitterThread handles a detected Twitter thread
func processTwitterThread(thread TwitterThread, origChat *response.TgChat, origMessage *response.TgMessage, downloader *services.SmartDownloader) {
	log.Printf("🧵 [THREAD] Processing thread from @%s with %d tweets", thread.Username, len(thread.URLs))
	
	// Check if this is a content-based thread (pseudo-URLs with #thread-part-)
	isContentThread := len(thread.URLs) > 1 && strings.Contains(thread.URLs[1], "#thread-part-")
	
	if isContentThread {
		log.Printf("🧵 [CONTENT-THREAD] Detected content-based thread, extracting text parts")
		
		// Re-extract the thread content to get the actual text
		originalURL := thread.URLs[0]
		cliBrowser := services.NewCliBrowserExtractor(downloader.GetWorkingDir(), downloader.GetTempDir())
		html, err := cliBrowser.RenderPageForTwitterThread(originalURL)
		if err != nil {
			log.Printf("❌ [CONTENT-THREAD] Failed to re-render for content: %v", err)
			return
		}
		
		contentParts := parseTwitterThreadContent(html, thread.Username, "", originalURL)
		
		// Process the original tweet first (normal processing)
		log.Printf("🧵 [CONTENT-THREAD] Processing original tweet: %s", originalURL)
		processMediaDownload(originalURL, origChat, origMessage, downloader)
		
		// Then send additional content parts as text messages
		for i := 1; i < len(contentParts); i++ {
			time.Sleep(500 * time.Millisecond) // Delay between parts
			
			content := contentParts[i]
			log.Printf("🧵 [CONTENT-THREAD] Sending thread part %d: %.100s...", i, content.Caption)
			
			// Send as text message with proper formatting
			sendThreadTextMessage(content.Caption, origChat, origMessage)
		}
		
		log.Printf("🧵 [CONTENT-THREAD] Completed processing content thread from @%s", thread.Username)
	} else {
		// Standard URL-based thread processing
		for i, url := range thread.URLs {
			log.Printf("🧵 [URL-THREAD] Processing tweet %d/%d: %s", i+1, len(thread.URLs), url)
			processMediaDownload(url, origChat, origMessage, downloader)
			
			// Small delay between thread posts to maintain order
			if i < len(thread.URLs)-1 {
				time.Sleep(500 * time.Millisecond)
			}
		}
		
		log.Printf("🧵 [URL-THREAD] Completed processing thread from @%s", thread.Username)
	}
}

// sendThreadTextMessage sends a text-only message as part of a thread
func sendThreadTextMessage(text string, origChat *response.TgChat, origMessage *response.TgMessage) error {
	log.Printf("Sending thread text to chat ID: %d", origChat.Id)
	
	// Decode HTML entities
	cleanText := html.UnescapeString(text)
	
	// Limit text length to Telegram's message limit (4096 characters)
	if len(cleanText) > 4090 {
		cleanText = cleanText[:4087] + "..."
	}
	
	// Send text message with reply and silent notification
	textPayload := payload.TgPlSendMessage{
		ChatId:              origChat.Id,
		Text:                cleanText,
		ReplyToMessageId:    origMessage.MessageId,
		DisableNotification: true,
	}
	
	response := message.NewSendMessage(textPayload)
	if response == nil {
		log.Printf("❌ Failed to send thread text message")
		return fmt.Errorf("failed to send thread text message")
	}
	
	log.Printf("✅ Sent thread text message (%.100s...)", cleanText)
	return nil
}

// cleanURLParams removes tracking parameters and UTM tags from URLs
func cleanURLParams(originalURL string) string {
	parsedURL, err := url.Parse(originalURL)
	if err != nil {
		// If parsing fails, return original URL
		return originalURL
	}

	// List of common tracking parameters to remove
	trackingParams := map[string]bool{
		// UTM parameters
		"utm_source":    true,
		"utm_medium":    true,
		"utm_campaign":  true,
		"utm_term":      true,
		"utm_content":   true,
		"utm_id":        true,
		
		// Social media tracking
		"t":             true, // Twitter sharing parameter
		"s":             true, // Twitter sharing parameter
		"fbclid":        true, // Facebook click ID
		"gclid":         true, // Google click ID
		"msclkid":       true, // Microsoft click ID
		"igshid":        true, // Instagram share ID
		
		// General tracking
		"ref":           true,
		"ref_src":       true,
		"source":        true,
		"campaign":      true,
		"_hsenc":        true, // HubSpot
		"_hsmi":         true, // HubSpot
		"mc_cid":        true, // MailChimp
		"mc_eid":        true, // MailChimp
	}

	// Get query parameters
	values := parsedURL.Query()
	
	// Remove tracking parameters
	for param := range trackingParams {
		values.Del(param)
	}
	
	// Set cleaned query parameters
	parsedURL.RawQuery = values.Encode()
	
	return parsedURL.String()
}

// discoverTwitterThreads automatically finds follow-up tweets by fetching the original tweet's page
func discoverTwitterThreads(urls []string, downloader *services.SmartDownloader) []TwitterThread {
	// Regex to extract Twitter/X URLs
	twitterRegex := regexp.MustCompile(`https?://(twitter|x)\.com/([^/]+)/status/(\d+)`)
	
	var allThreads []TwitterThread
	
	for _, originalURL := range urls {
		matches := twitterRegex.FindStringSubmatch(originalURL)
		if len(matches) < 4 {
			continue // Not a Twitter URL
		}
		
		username := matches[2]
		originalTweetID := matches[3]
		
		log.Printf("🔍 [AUTO-THREAD] Checking for thread continuation from @%s (ID: %s)", username, originalTweetID)
		
		// Fetch the tweet page HTML using browser rendering (same as image extraction)
		followUpURLs, err := fetchTwitterThreadWithBrowser(originalURL, username, originalTweetID, downloader)
		if err != nil {
			log.Printf("❌ [AUTO-THREAD] Failed to fetch thread for %s: %v", originalURL, err)
			continue
		}
		
		if len(followUpURLs) == 0 {
			log.Printf("📄 [AUTO-THREAD] No follow-up tweets found for @%s", username)
			continue
		}
		
		// Create thread with original URL + discovered follow-ups
		allURLs := []string{originalURL}
		
		// Parse original tweet ID
		originalTweetIDInt, _ := strconv.ParseInt(originalTweetID, 10, 64)
		
		// Filter and sort follow-up URLs by tweet ID
		type tweetWithID struct {
			URL     string
			TweetID int64
		}
		
		var validFollowUps []tweetWithID
		for _, url := range followUpURLs {
			if urlMatches := twitterRegex.FindStringSubmatch(url); len(urlMatches) >= 4 {
				if tweetID, err := strconv.ParseInt(urlMatches[3], 10, 64); err == nil {
					// Only include tweets that come after the original (higher ID = newer)
					if tweetID > originalTweetIDInt {
						validFollowUps = append(validFollowUps, tweetWithID{URL: url, TweetID: tweetID})
					}
				}
			}
		}
		
		// Sort follow-ups by tweet ID (chronological order)
		sort.Slice(validFollowUps, func(i, j int) bool {
			return validFollowUps[i].TweetID < validFollowUps[j].TweetID
		})
		
		// Add sorted follow-ups to URLs
		for _, tweet := range validFollowUps {
			allURLs = append(allURLs, tweet.URL)
		}
		
		// Build tweet IDs array
		var tweetIDs []int64
		tweetIDs = append(tweetIDs, originalTweetIDInt)
		for _, tweet := range validFollowUps {
			tweetIDs = append(tweetIDs, tweet.TweetID)
		}
		
		thread := TwitterThread{
			URLs:     allURLs,
			Username: username,
			TweetIDs: tweetIDs,
		}
		
		log.Printf("✅ [AUTO-THREAD] Discovered thread from @%s with %d total tweets", username, len(allURLs))
		allThreads = append(allThreads, thread)
	}
	
	return allThreads
}

// fetchTwitterThreadWithBrowser uses the existing CLI browser to render Twitter and find thread URLs
func fetchTwitterThreadWithBrowser(tweetURL, authorUsername, originalTweetID string, downloader *services.SmartDownloader) ([]string, error) {
	log.Printf("🌐 [AUTO-THREAD] Using browser rendering for thread discovery")
	
	// Use the existing CLI browser extractor that's already integrated
	// Get working directory for proper cookie path resolution
	workingDir := downloader.GetWorkingDir()
	tempDir := downloader.GetTempDir()
	cliBrowser := services.NewCliBrowserExtractor(workingDir, tempDir)
	
	// Render the page using Chrome (same approach as Twitter image extraction)
	html, err := cliBrowser.RenderPageForTwitterThread(tweetURL)
	if err != nil {
		return nil, fmt.Errorf("browser rendering failed: %v", err)
	}
	
	log.Printf("🔍 [AUTO-THREAD] Browser rendered HTML: %d bytes", len(html))
	
	// Debug: Check if HTML contains the username at all
	usernameCount := strings.Count(html, authorUsername)
	log.Printf("🔍 [AUTO-THREAD] HTML contains username '%s' %d times", authorUsername, usernameCount)
	
	// Try both URL-based and content-based thread detection
	urls := parseTwitterThreadURLs(html, authorUsername, originalTweetID)
	
	// If no URLs found, try content-based approach
	if len(urls) == 0 {
		log.Printf("🔍 [AUTO-THREAD] No follow-up URLs found, trying content-based thread detection")
		contentParts := parseTwitterThreadContent(html, authorUsername, originalTweetID, tweetURL)
		if len(contentParts) > 1 { // More than just the original tweet
			log.Printf("✅ [AUTO-THREAD] Found %d thread content parts", len(contentParts))
			// Convert content parts to URLs (we'll process them as content later)
			for i := 1; i < len(contentParts); i++ { // Skip first part (original tweet)
				// Create pseudo-URLs to represent thread parts
				pseudoURL := fmt.Sprintf("%s#thread-part-%d", tweetURL, i)
				urls = append(urls, pseudoURL)
			}
		}
	}
	
	return urls, nil
}

// parseTwitterThreadContent extracts thread content from multiple tweetText elements
func parseTwitterThreadContent(html, authorUsername, originalTweetID, originalURL string) []services.MediaInfo {
	var threadItems []services.MediaInfo
	
	log.Printf("🔍 [AUTO-THREAD] Parsing tweetText elements for thread content from @%s", authorUsername)
	
	// Check if the tweet contains a thread indicator (👇 or "thread" text)
	hasThreadIndicator := strings.Contains(html, "👇") || 
						 strings.Contains(strings.ToLower(html), "thread") ||
						 strings.Contains(html, "Show this thread")
	
	if hasThreadIndicator {
		log.Printf("🧵 [AUTO-THREAD] Thread indicator found in HTML - this appears to be a thread")
	}
	
	// Pattern to find tweetText elements - handle nested content
	tweetTextPattern := regexp.MustCompile(`(?s)<div[^>]*data-testid="tweetText"[^>]*>(.*?)</div>`)
	
	// Also look for tweet containers that might contain thread parts
	tweetContainerPattern := regexp.MustCompile(`(?s)<article[^>]*data-testid="tweet"[^>]*>.*?</article>`)
	
	// Find all tweetText matches
	matches := tweetTextPattern.FindAllStringSubmatch(html, -1)
	containerMatches := tweetContainerPattern.FindAllString(html, -1)
	
	log.Printf("🔍 [AUTO-THREAD] Found %d tweetText elements and %d tweet containers in HTML", len(matches), len(containerMatches))
	
	for i, match := range matches {
		if len(match) >= 2 {
			rawContent := match[1]
			
			// Clean the tweet content (remove HTML tags, decode entities)
			cleanContent := cleanTweetText(rawContent)
			
			// Skip empty content
			if strings.TrimSpace(cleanContent) == "" {
				log.Printf("   ❌ TweetText %d: Empty content, skipping", i+1)
				continue
			}
			
			log.Printf("   ✅ TweetText %d: Found content (%.100s...)", i+1, cleanContent)
			
			// Create a MediaInfo for this thread part (text-only)
			mediaInfo := services.MediaInfo{
				Type:    "text", // Special type for thread text content
				URL:     originalURL,
				Caption: cleanContent,
				// No filename for text-only content
			}
			
			threadItems = append(threadItems, mediaInfo)
		}
	}
	
	log.Printf("🔍 [AUTO-THREAD] Extracted %d thread content items for @%s", len(threadItems), authorUsername)
	
	// If we found thread indicators but only 1 content item, the thread might be in replies
	// that aren't loaded in the current view. For now, we'll treat it as a single tweet.
	if hasThreadIndicator && len(threadItems) == 1 {
		log.Printf("🧵 [AUTO-THREAD] Thread indicator found but only 1 content item - thread parts may be in separate replies")
		
		// Check if the content has natural break points that could be split
		content := threadItems[0].Caption
		if len(content) > 500 && (strings.Contains(content, "👇") || strings.Count(content, "\n\n") >= 3) {
			log.Printf("🧵 [AUTO-THREAD] Long content with potential split points detected")
			// For now, return as single item - could implement intelligent splitting later
		}
	}
	
	return threadItems
}

// cleanTweetText removes HTML tags and decodes entities from tweet content
func cleanTweetText(rawText string) string {
	text := rawText
	
	// Remove HTML tags
	text = regexp.MustCompile(`<[^>]*>`).ReplaceAllString(text, "")
	
	// Decode HTML entities
	text = strings.ReplaceAll(text, "&amp;", "&")
	text = strings.ReplaceAll(text, "&lt;", "<")
	text = strings.ReplaceAll(text, "&gt;", ">")
	text = strings.ReplaceAll(text, "&quot;", `"`)
	text = strings.ReplaceAll(text, "&#39;", "'")
	text = strings.ReplaceAll(text, "&#34;", `"`)
	
	// Decode JSON escape sequences
	text = strings.ReplaceAll(text, `\"`, `"`)
	text = strings.ReplaceAll(text, `\\n`, "\n")
	text = strings.ReplaceAll(text, `\\t`, "\t")
	text = strings.ReplaceAll(text, `\\r`, "\r")
	
	// Remove pic.twitter.com links
	picTwitterPattern := regexp.MustCompile(`\s*pic\.twitter\.com/[a-zA-Z0-9]+\s*`)
	text = picTwitterPattern.ReplaceAllString(text, "")
	
	// Clean up multiple spaces and newlines
	text = regexp.MustCompile(`\s+`).ReplaceAllString(text, " ")
	text = strings.TrimSpace(text)
	
	return text
}

// parseTwitterThreadURLs extracts follow-up tweet URLs from Twitter HTML, excluding the original tweet
func parseTwitterThreadURLs(html, authorUsername, originalTweetID string) []string {
	var urls []string
	
	// Debug: Save HTML sample for pattern analysis
	debugSample := html
	if len(debugSample) > 2000 {
		debugSample = debugSample[:2000] + "...[TRUNCATED]"
	}
	log.Printf("🔍 [AUTO-THREAD] HTML sample (first 2000 chars): %s", debugSample)
	
	// Multiple patterns to find tweet links from the same author in conversation
	
	// Pattern 1: Basic relative links like /med_goblin/status/1934423928782377439
	linkPattern := regexp.MustCompile(fmt.Sprintf(`/(%s)/status/(\d+)`, regexp.QuoteMeta(authorUsername)))
	
	// Pattern 2: Full URLs like https://x.com/med_goblin/status/1934423928782377439
	fullURLPattern := regexp.MustCompile(fmt.Sprintf(`https?://(twitter|x)\.com/(%s)/status/(\d+)`, regexp.QuoteMeta(authorUsername)))
	
	// Pattern 3: Href attributes with quotes
	hrefPattern := regexp.MustCompile(fmt.Sprintf(`href="[^"]*?/(%s)/status/(\d+)[^"]*"`, regexp.QuoteMeta(authorUsername)))
	
	// Pattern 4: Data attributes that might contain URLs
	dataPattern := regexp.MustCompile(fmt.Sprintf(`data-[^=]*="[^"]*?/(%s)/status/(\d+)[^"]*"`, regexp.QuoteMeta(authorUsername)))
	
	// Pattern 5: JSON-style URLs (in case of embedded data)
	jsonPattern := regexp.MustCompile(fmt.Sprintf(`"[^"]*?/(%s)/status/(\d+)[^"]*"`, regexp.QuoteMeta(authorUsername)))
	
	// Pattern 6: URL with query parameters 
	queryPattern := regexp.MustCompile(fmt.Sprintf(`https?://(twitter|x)\.com/(%s)/status/(\d+)(?:\?[^"\s>]*)?`, regexp.QuoteMeta(authorUsername)))
	
	log.Printf("🔍 [AUTO-THREAD] Using %d regex patterns to find thread URLs", 6)
	
	// Find all matches
	linkMatches := linkPattern.FindAllStringSubmatch(html, -1)
	fullURLMatches := fullURLPattern.FindAllStringSubmatch(html, -1)
	hrefMatches := hrefPattern.FindAllStringSubmatch(html, -1)
	dataMatches := dataPattern.FindAllStringSubmatch(html, -1)
	jsonMatches := jsonPattern.FindAllStringSubmatch(html, -1)
	queryMatches := queryPattern.FindAllStringSubmatch(html, -1)
	
	log.Printf("🔍 [AUTO-THREAD] Pattern matches - Link: %d, Full URL: %d, Href: %d, Data: %d, JSON: %d, Query: %d", 
		len(linkMatches), len(fullURLMatches), len(hrefMatches), len(dataMatches), len(jsonMatches), len(queryMatches))
	
	// Debug: Log raw matches found
	log.Printf("🔍 [AUTO-THREAD] Raw link matches:")
	for i, match := range linkMatches {
		if i < 5 { // Log first 5 matches
			log.Printf("   Link match %d: %v", i+1, match)
		}
	}
	
	log.Printf("🔍 [AUTO-THREAD] Raw full URL matches:")
	for i, match := range fullURLMatches {
		if i < 5 { // Log first 5 matches
			log.Printf("   Full URL match %d: %v", i+1, match)
		}
	}
	
	log.Printf("🔍 [AUTO-THREAD] Raw href matches:")
	for i, match := range hrefMatches {
		if i < 5 { // Log first 5 matches
			log.Printf("   Href match %d: %v", i+1, match)
		}
	}
	
	log.Printf("🔍 [AUTO-THREAD] Raw data matches:")
	for i, match := range dataMatches {
		if i < 5 { // Log first 5 matches
			log.Printf("   Data match %d: %v", i+1, match)
		}
	}
	
	log.Printf("🔍 [AUTO-THREAD] Raw JSON matches:")
	for i, match := range jsonMatches {
		if i < 5 { // Log first 5 matches
			log.Printf("   JSON match %d: %v", i+1, match)
		}
	}
	
	log.Printf("🔍 [AUTO-THREAD] Raw query matches:")
	for i, match := range queryMatches {
		if i < 5 { // Log first 5 matches
			log.Printf("   Query match %d: %v", i+1, match)
		}
	}
	
	log.Printf("🔍 [AUTO-THREAD] Original tweet ID to exclude: %s", originalTweetID)
	
	// Convert relative links to full URLs
	seenTweetIDs := make(map[string]bool)
	
	for _, match := range linkMatches {
		if len(match) >= 3 {
			username := match[1]
			tweetID := match[2]
			
			log.Printf("🔍 [AUTO-THREAD] Processing link match: username=%s, tweetID=%s", username, tweetID)
			
			// Skip the original tweet and duplicates
			if tweetID == originalTweetID {
				log.Printf("   ❌ Skipped: matches original tweet ID (%s)", originalTweetID)
				continue
			}
			if seenTweetIDs[tweetID] {
				log.Printf("   ❌ Skipped: duplicate tweet ID (%s)", tweetID)
				continue
			}
			seenTweetIDs[tweetID] = true
			
			fullURL := fmt.Sprintf("https://x.com/%s/status/%s", username, tweetID)
			urls = append(urls, fullURL)
			log.Printf("   ✅ Added URL: %s", fullURL)
		}
	}
	
	// Add full URLs found in HTML
	for _, match := range fullURLMatches {
		if len(match) >= 4 {
			tweetID := match[3]
			
			log.Printf("🔍 [AUTO-THREAD] Processing full URL match: tweetID=%s, fullURL=%s", tweetID, match[0])
			
			// Skip the original tweet and duplicates
			if tweetID == originalTweetID {
				log.Printf("   ❌ Skipped: matches original tweet ID (%s)", originalTweetID)
				continue
			}
			if seenTweetIDs[tweetID] {
				log.Printf("   ❌ Skipped: duplicate tweet ID (%s)", tweetID)
				continue
			}
			seenTweetIDs[tweetID] = true
			
			fullURL := match[0]
			urls = append(urls, fullURL)
			log.Printf("   ✅ Added URL: %s", fullURL)
		}
	}
	
	// Add href URLs found in HTML
	for _, match := range hrefMatches {
		if len(match) >= 3 {
			username := match[1]
			tweetID := match[2]
			
			log.Printf("🔍 [AUTO-THREAD] Processing href match: username=%s, tweetID=%s", username, tweetID)
			
			// Skip the original tweet and duplicates
			if tweetID == originalTweetID {
				log.Printf("   ❌ Skipped: matches original tweet ID (%s)", originalTweetID)
				continue
			}
			if seenTweetIDs[tweetID] {
				log.Printf("   ❌ Skipped: duplicate tweet ID (%s)", tweetID)
				continue
			}
			seenTweetIDs[tweetID] = true
			
			fullURL := fmt.Sprintf("https://x.com/%s/status/%s", username, tweetID)
			urls = append(urls, fullURL)
			log.Printf("   ✅ Added URL: %s", fullURL)
		}
	}
	
	// Add data attribute URLs
	for _, match := range dataMatches {
		if len(match) >= 3 {
			username := match[1]
			tweetID := match[2]
			
			log.Printf("🔍 [AUTO-THREAD] Processing data match: username=%s, tweetID=%s", username, tweetID)
			
			// Skip the original tweet and duplicates
			if tweetID == originalTweetID {
				log.Printf("   ❌ Skipped: matches original tweet ID (%s)", originalTweetID)
				continue
			}
			if seenTweetIDs[tweetID] {
				log.Printf("   ❌ Skipped: duplicate tweet ID (%s)", tweetID)
				continue
			}
			seenTweetIDs[tweetID] = true
			
			fullURL := fmt.Sprintf("https://x.com/%s/status/%s", username, tweetID)
			urls = append(urls, fullURL)
			log.Printf("   ✅ Added URL: %s", fullURL)
		}
	}
	
	// Add JSON-style URLs
	for _, match := range jsonMatches {
		if len(match) >= 3 {
			username := match[1]
			tweetID := match[2]
			
			log.Printf("🔍 [AUTO-THREAD] Processing JSON match: username=%s, tweetID=%s", username, tweetID)
			
			// Skip the original tweet and duplicates
			if tweetID == originalTweetID {
				log.Printf("   ❌ Skipped: matches original tweet ID (%s)", originalTweetID)
				continue
			}
			if seenTweetIDs[tweetID] {
				log.Printf("   ❌ Skipped: duplicate tweet ID (%s)", tweetID)
				continue
			}
			seenTweetIDs[tweetID] = true
			
			fullURL := fmt.Sprintf("https://x.com/%s/status/%s", username, tweetID)
			urls = append(urls, fullURL)
			log.Printf("   ✅ Added URL: %s", fullURL)
		}
	}
	
	// Add query parameter URLs
	for _, match := range queryMatches {
		if len(match) >= 4 {
			tweetID := match[3]
			
			log.Printf("🔍 [AUTO-THREAD] Processing query match: tweetID=%s, fullURL=%s", tweetID, match[0])
			
			// Skip the original tweet and duplicates
			if tweetID == originalTweetID {
				log.Printf("   ❌ Skipped: matches original tweet ID (%s)", originalTweetID)
				continue
			}
			if seenTweetIDs[tweetID] {
				log.Printf("   ❌ Skipped: duplicate tweet ID (%s)", tweetID)
				continue
			}
			seenTweetIDs[tweetID] = true
			
			fullURL := match[0]
			urls = append(urls, fullURL)
			log.Printf("   ✅ Added URL: %s", fullURL)
		}
	}
	
	log.Printf("🔍 [AUTO-THREAD] Found %d potential follow-up URLs for @%s", len(urls), authorUsername)
	
	// Debug: Log first few URLs found for troubleshooting
	for i, url := range urls {
		if i < 3 { // Log first 3 URLs
			log.Printf("🔍 [AUTO-THREAD] Found URL %d: %s", i+1, url)
		}
	}
	
	return urls
}
