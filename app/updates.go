package app

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/timoffmax/social-link-bot/config"
	"github.com/timoffmax/social-link-bot/model/api"
	"github.com/timoffmax/social-link-bot/model/api/request/message"
	"github.com/timoffmax/social-link-bot/model/api/request/payload"
	"github.com/timoffmax/social-link-bot/model/api/response"
	"github.com/timoffmax/social-link-bot/model/api/response/utils"
	"github.com/timoffmax/social-link-bot/services"
)

var (
	processedUpdates = make(map[int64]bool)
	updatesMutex     sync.RWMutex
	botUsername       string // Bot's username, fetched on startup

	// updateSemaphore limits the number of concurrent update-processing goroutines
	// to prevent resource exhaustion under heavy load.
	updateSemaphore = make(chan struct{}, 10)

	// updateWg tracks all in-flight update goroutines for graceful shutdown.
	updateWg sync.WaitGroup
)

// fetchBotInfo fetches the bot's info using the getMe API and stores the username
func fetchBotInfo() error {
	token := config.GetTgBotToken()
	url := fmt.Sprintf("https://api.telegram.org/bot%s/getMe", token)

	resp, err := (&http.Client{Timeout: 10 * time.Second}).Get(url)
	if err != nil {
		return fmt.Errorf("failed to fetch bot info: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Ok     bool             `json:"ok"`
		Result response.TgUser  `json:"result"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("failed to decode bot info: %w", err)
	}

	if !result.Ok {
		return fmt.Errorf("telegram API returned error for getMe")
	}

	botUsername = strings.ToLower(result.Result.Username)
	log.Printf("✅ Bot username fetched: @%s", botUsername)

	return nil
}

// isTimezoneValid checks if a timezone is valid with fallback support
func isTimezoneValid(timezoneStr string) bool {
	// Try primary timezone first
	_, err := time.LoadLocation(timezoneStr)
	if err == nil {
		return true
	}
	
	// Handle Europe/Kyiv -> Europe/Kiev fallback
	if timezoneStr == "Europe/Kyiv" {
		_, err = time.LoadLocation("Europe/Kiev")
		return err == nil
	}
	
	// Handle Europe/Kiev -> Europe/Kyiv fallback
	if timezoneStr == "Europe/Kiev" {
		_, err = time.LoadLocation("Europe/Kyiv")
		return err == nil
	}
	
	return false
}

// loadTimezoneWithFallback loads a timezone with fallback support for UI display
func loadTimezoneWithFallback(timezoneStr string) *time.Location {
	// Try primary timezone first
	loc, err := time.LoadLocation(timezoneStr)
	if err == nil {
		return loc
	}
	
	// Handle Europe/Kyiv -> Europe/Kiev fallback
	if timezoneStr == "Europe/Kyiv" {
		loc, err = time.LoadLocation("Europe/Kiev")
		if err == nil {
			return loc
		}
	}
	
	// Handle Europe/Kiev -> Europe/Kyiv fallback
	if timezoneStr == "Europe/Kiev" {
		loc, err = time.LoadLocation("Europe/Kyiv")
		if err == nil {
			return loc
		}
	}
	
	// Final fallback to UTC
	return time.UTC
}

func listenForUpdates(ctx context.Context) {
	// Fetch bot info on startup
	if err := fetchBotInfo(); err != nil {
		log.Printf("⚠️ Warning: Could not fetch bot info: %v", err)
		log.Printf("📌 Commands with @botname mentions will be processed")
	}

	var offset int
	isFirstRun := true

	// Start a goroutine to periodically clean up old processed update IDs
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				cleanupOldUpdates()
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			log.Printf("🛑 Shutdown signal received, stopping update polling...")
			log.Printf("⏳ Waiting for %d in-flight updates to complete...", len(updateSemaphore))
			updateWg.Wait()
			log.Printf("✅ All in-flight updates completed, shutdown clean")
			return
		default:
			lastProcessedUpd := processRecentUpdates(offset, isFirstRun)

			if nil != lastProcessedUpd {
				offset = lastProcessedUpd.UpdateId + 1
			}

			isFirstRun = false
		}
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

		// Process each update in its own goroutine, bounded by semaphore
		for i, update := range validUpdates {
			updateWg.Add(1)

			go func(index int, upd *response.TgUpdate) {
				defer updateWg.Done()

				// Acquire semaphore slot (blocks if 10 goroutines are already running)
				updateSemaphore <- struct{}{}
				defer func() { <-updateSemaphore }()

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
		AllowedUpdates: []string{"message", "callback_query"},
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

	// Handle regular messages
	if 0 != upd.Message.MessageId {
		origMessage = upd.Message
		origUser = origMessage.From
		origChat = origMessage.Chat
		log.Printf("Processing update %d, message %d from user %d in chat %d (type: %s, title: %s)", upd.UpdateId, origMessage.MessageId, origUser.Id, origChat.Id, origChat.Type, origChat.Title)
		log.Printf("Original message text: %s", origMessage.Text)
	}

	// Handle callback queries (button presses)
	if upd.CallbackQuery.Id != "" {
		log.Printf("📱 Processing callback query: %s from user %d", upd.CallbackQuery.Data, upd.CallbackQuery.From.Id)
		handleCallbackQuery(&upd.CallbackQuery)
		return
	}

	if 0 == origUser.Id {
		return
	}

	// Handle bot commands first (before processing social media links)
	if strings.HasPrefix(origMessage.Text, "/") {
		handleBotCommand(&origMessage, &origChat, &origUser)
		return
	}

	// Get original social media links
	socialLinks := utils.GetAllSocialLinks(&origMessage, origChat.Id)
	
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

	// Check daily media limit before processing
	if shouldEnforceLimit(int64(origUser.Id), origChat.Id, origUser.Username, origMessage.MessageId) {
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

	// Start sending chat action indicators so users see "uploading video..." while we work
	stopAction := make(chan struct{})

	go func() {
		action := guessChatAction(url)

		actionPayload := payload.TgPlSendChatAction{
			ChatId: origChat.Id,
			Action: action,
		}

		// Send immediately, then every 4 seconds (Telegram expires actions after 5s)
		message.NewSendChatAction(actionPayload)

		ticker := time.NewTicker(4 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-stopAction:
				return
			case <-ticker.C:
				message.NewSendChatAction(actionPayload)
			}
		}
	}()

	defer close(stopAction)

	// Send status message for Instagram photos/carousels (which may take time to download)
	if (strings.Contains(url, "instagram.com") || strings.Contains(url, "kkinstagram.com")) && strings.Contains(url, "/p/") {
		statusMsg := payload.TgPlSendMessage{
			ChatId:              origChat.Id,
			Text:                "📸 Processing Instagram photos... This isn't quick, unfortunately, may take a couple of minutes ⏳",
			ReplyToMessageId:    origMessage.MessageId,
			DisableNotification: true,
		}
		message.NewSendMessage(statusMsg)
		log.Printf("📤 Sent status message for Instagram photo/carousel processing")
	}

	// Try to download media using smart downloader
	mediaInfos, err := downloader.DownloadMedia(url)
	if err != nil {
		log.Printf("Failed to download media from %s: %v", url, err)
		
		// Skip error notifications for platforms that commonly fail (silently fail)
		if strings.Contains(url, "reddit.com") || strings.Contains(url, "facebook.com") {
			platformName := "Reddit"
			if strings.Contains(url, "reddit.com") {
				platformName = "Reddit"
			} else if strings.Contains(url, "facebook.com") {
				platformName = "Facebook"
			}
			log.Printf("🤐 Skipping error notification for %s link", platformName)
			services.LogTelegramMessage(origChat.Id, "error-skipped", false, "")
			return
		}
		
		// For Instagram videos, we already tried fixed link first, so don't fallback
		// For other platforms (TikTok, Twitter) that didn't try fixed link first, fallback
		if !strings.Contains(url, "instagram.com") || (!strings.Contains(url, "/reel/") && !strings.Contains(url, "/tv/")) {
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
		err := sendPhotosAsMediaGroup(photos, origChat, origMessage, url)
		if err != nil {
			log.Printf("Failed to send photos as media group: %v", err)
			// Fallback: send photos individually
			log.Printf("Fallback: sending photos individually")
			for i, photoInfo := range photos {
				log.Printf("Sending photo %d/%d: %s", i+1, len(photos), photoInfo.Filename)
				err := sendMediaFile(photoInfo, origChat, origMessage, url)
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
		err := sendMediaFile(photos[0], origChat, origMessage, url)
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
		
		err := sendMediaFile(mediaInfo, origChat, origMessage, url)
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
			
			// Track document send
			trackMediaSent(int64(origMessage.From.Id), origMessage.From.Username, origChat.Id, origChat.Title, url, "document")
			
			downloader.CleanupFile(mediaInfo.Filename)
		} else {
			log.Printf("Successfully sent media file: %s", mediaInfo.Filename)
			// Clean up the file after successful sending
			downloader.CleanupFile(mediaInfo.Filename)
		}
	}
}

// detectPlatformFromUrl detects the social media platform from URL
func detectPlatformFromUrl(url string) string {
	if strings.Contains(url, "instagram.com") || strings.Contains(url, "kkinstagram.com") {
		return "instagram"
	} else if strings.Contains(url, "tiktok.com") || strings.Contains(url, "tiktxk.com") {
		return "tiktok"
	} else if strings.Contains(url, "twitter.com") || strings.Contains(url, "x.com") || strings.Contains(url, "fixupx.com") {
		return "twitter"
	} else if strings.Contains(url, "youtube.com") || strings.Contains(url, "youtu.be") {
		return "youtube"
	} else if strings.Contains(url, "reddit.com") {
		return "reddit"
	} else if strings.Contains(url, "facebook.com") {
		return "facebook"
	}
	return ""
}

// extractAuthorInfo extracts only author information from caption, removing main content
func extractAuthorInfo(fullCaption, platform string) string {
	if fullCaption == "" {
		return ""
	}
	
	switch platform {
	case "twitter":
		// For Twitter, try to extract the author line (usually first line with @username)
		lines := strings.Split(fullCaption, "\n")
		for _, line := range lines {
			if strings.Contains(line, "@") && (strings.Contains(line, "👤") || len(line) < 100) {
				return strings.TrimSpace(line) // Return just the author line
			}
		}
		// Fallback: return first line if it looks like author info
		if len(lines) > 0 && len(lines[0]) < 100 {
			return strings.TrimSpace(lines[0])
		}
		
	case "instagram":
		// For Instagram, try to extract username (usually has @username)
		lines := strings.Split(fullCaption, "\n")
		for _, line := range lines {
			if strings.Contains(line, "@") && len(line) < 50 {
				return strings.TrimSpace(line)
			}
		}
		// Fallback: return a generic Instagram indicator
		return "📸 Instagram"
		
	case "tiktok":
		// For TikTok, extract author/creator info
		lines := strings.Split(fullCaption, "\n")
		for _, line := range lines {
			if strings.Contains(line, "@") && len(line) < 50 {
				return strings.TrimSpace(line)
			}
		}
		return "🎵 TikTok"
		
	case "youtube":
		// For YouTube, extract channel name (usually first line)
		lines := strings.Split(fullCaption, "\n")
		if len(lines) > 0 && len(lines[0]) < 100 {
			return strings.TrimSpace(lines[0])
		}
		return "📺 YouTube"
		
	case "reddit":
		// For Reddit, extract subreddit/author info
		lines := strings.Split(fullCaption, "\n")
		for _, line := range lines {
			if (strings.Contains(line, "r/") || strings.Contains(line, "u/")) && len(line) < 100 {
				return strings.TrimSpace(line)
			}
		}
		return "🤖 Reddit"
		
	case "facebook":
		// For Facebook, generic fallback
		return "📘 Facebook"
	}
	
	// Generic fallback
	return "📱 Social Media"
}

func sendMediaFile(mediaInfo *services.MediaInfo, origChat *response.TgChat, origMessage *response.TgMessage, url string) error {
	log.Printf("Sending media to chat ID: %d", origChat.Id)
	
	// Decode HTML entities
	caption := html.UnescapeString(mediaInfo.Caption)
	
	// Check description settings and filter caption if needed
	platform := detectPlatformFromUrl(url)
	if platform != "" && !services.IsDescriptionEnabledForChat(origChat.Id, platform) {
		log.Printf("📝 Descriptions disabled for %s in chat %d, using minimal caption", platform, origChat.Id)
		// Keep only author info, remove the main description
		caption = extractAuthorInfo(caption, platform)
	}
	
	// For Twitter/X posts, handle long text intelligently
	if (strings.Contains(mediaInfo.URL, "twitter.com") || strings.Contains(mediaInfo.URL, "x.com")) &&
		len(caption) > 1024 && mediaInfo.Type != "text" {
		log.Printf("🐦 Twitter post exceeds Telegram limit (%d chars), sending text and media separately", len(caption))

		// Extract URL from caption (it's at the end after "\n\n🔗 ")
		textPart := caption
		urlPart := ""
		if idx := strings.LastIndex(caption, "\n\n🔗 "); idx != -1 {
			textPart = caption[:idx]
			urlPart = caption[idx:]
		}

		// Send text first (only if it's not a generic message) with reply and silent notification
		if textPart != "Downloaded from Twitter/X" && textPart != "Downloaded from Twitter" && textPart != "" {
			textPayload := payload.TgPlSendMessage{
				ChatId:              origChat.Id,
				Text:                textPart,
				ReplyToMessageId:    origMessage.MessageId,
				DisableNotification: true,
			}
			message.NewSendMessage(textPayload)
			log.Printf("✅ Sent Twitter text content separately")
		}

		// Send media with just the URL as caption
		caption = strings.TrimPrefix(urlPart, "\n\n")
	} else {
		// Normal handling - limit caption length to Telegram's 1024 character limit
		if len(caption) > 1024 {
			caption = caption[:1021] + "..."
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
			
			// Track photo send
			trackMediaSent(int64(origMessage.From.Id), origMessage.From.Username, origChat.Id, origChat.Title, url, "photo")
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
					
					// Track document send
					trackMediaSent(int64(origMessage.From.Id), origMessage.From.Username, origChat.Id, origChat.Title, url, "document")
					
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
				
				// Track document send
				trackMediaSent(int64(origMessage.From.Id), origMessage.From.Username, origChat.Id, origChat.Title, url, "document")
				
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
		
		// Track video send
		trackMediaSent(int64(origMessage.From.Id), origMessage.From.Username, origChat.Id, origChat.Title, url, "video")

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

func sendPhotosAsMediaGroup(photos []*services.MediaInfo, origChat *response.TgChat, origMessage *response.TgMessage, url string) error {
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
			if len(caption) > 1024 {
				caption = caption[:1021] + "..."
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
	
	// Track each photo send
	for range photos {
		trackMediaSent(int64(origMessage.From.Id), origMessage.From.Username, origChat.Id, origChat.Title, url, "photo")
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
		service := config.GetInstagramService()
		if service == "kkinstagram" {
			result := strings.Replace(url, "https://www.instagram.com", "https://www.kkinstagram.com", 1)
			return strings.Replace(result, "https://instagram.com", "https://www.kkinstagram.com", 1)
		}
		// Default to instagramez
		return strings.Replace(strings.Replace(url, "https://www.instagram.com", "https://www.instagramez.com", 1), "https://instagram.com", "https://instagramez.com", 1)
	}
	
	// TikTok
	if strings.Contains(url, "tiktok.com") {
		return strings.Replace(url, "tiktok.com", "tiktxk.com", 1)
	}
	
	// Twitter/X
	if strings.Contains(url, "twitter.com") {
		return strings.Replace(url, "twitter.com", "fixupx.com", 1)
	}
	if strings.Contains(url, "x.com") {
		return strings.Replace(url, "x.com", "fixupx.com", 1)
	}
	
	// YouTube doesn't have fixed link services
	// Return empty string to trigger fallback message
	
	return ""
}

func getErrorMessage(url string) string {
	if strings.Contains(url, "youtube.com") || strings.Contains(url, "youtu.be") {
		// Don't send YouTube error messages to Telegram
		return ""
	} else if strings.Contains(url, "instagram.com") {
		return "❌ Cannot download this Instagram video.\n\n" +
			"The content may be private, deleted, or the account might be restricted. " +
			"Instagram has strict anti-bot measures that sometimes prevent downloads."
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

// handleBotCommand handles all bot commands
func handleBotCommand(origMessage *response.TgMessage, origChat *response.TgChat, origUser *response.TgUser) {
	commandText := strings.Fields(origMessage.Text)[0]

	// Check if command is directed at a specific bot (e.g., /start@other_bot)
	if atIndex := strings.Index(commandText, "@"); atIndex != -1 {
		mentionedBot := strings.ToLower(commandText[atIndex+1:])
		// If a specific bot is mentioned and it's not us, ignore the command
		if (botUsername != "") && (mentionedBot != botUsername) {
			log.Printf("🚫 Ignoring command directed at @%s (we are @%s)", mentionedBot, botUsername)
			return
		}
		// Remove bot mention to get the base command
		commandText = commandText[:atIndex]
	}

	command := strings.ToLower(commandText)

	log.Printf("🤖 Processing command: %s from user %d in chat %d", command, origUser.Id, origChat.Id)
	
	switch command {
	case "/start", "/help", "/menu":
		handleStartCommand(origMessage, origChat, origUser)
	case "/stats":
		handleStatsCommand(origMessage, origChat, origUser)
	case "/mylimit":
		handleMyLimitCommand(origMessage, origChat, origUser)
	case "/setlimit":
		handleSetLimitCommand(origMessage, origChat, origUser)
	case "/setgrouplimit":
		handleSetGroupLimitCommand(origMessage, origChat, origUser)
	case "/setgrouptimezone":
		handleSetGroupTimezoneCommand(origMessage, origChat, origUser)
	case "/adminstats":
		handleAdminStatsCommand(origMessage, origChat, origUser)
	case "/donate":
		handleDonateCommand(origMessage, origChat, origUser)
	default:
		// Unknown command - ignore it
		log.Printf("🚫 Ignoring unknown command: %s from user %d", command, origUser.Id)
	}
}

// handleStartCommand handles /start and /help commands with interactive menu
func handleStartCommand(origMessage *response.TgMessage, origChat *response.TgChat, origUser *response.TgUser) {
	var menuText strings.Builder
	isGroupChat := origChat.Type != "private"
	
	menuText.WriteString("🤖 <b>Social Link Bot</b>\n\n")
	menuText.WriteString("I download and send actual media files from social media platforms — no more link previews!\n\n")

	// Platform support
	menuText.WriteString("🌐 <b>Supported Platforms:</b>\n")
	menuText.WriteString("📷 <b>Instagram</b> — Photos, Videos, Albums, Stories\n")
	menuText.WriteString("🎵 <b>TikTok</b> — Videos, Photo Slideshows\n")
	menuText.WriteString("🐦 <b>Twitter/X</b> — Photos, Videos, GIFs\n")
	menuText.WriteString("📺 <b>YouTube</b> — Videos, Shorts\n")
	menuText.WriteString("🔴 <b>Reddit</b> — Videos, Images, Galleries, Text Posts\n")
	menuText.WriteString("👥 <b>Facebook</b> — Videos, Photos\n\n")

	// How to use — context-dependent
	if isGroupChat {
		menuText.WriteString("👥 <b>How to use in group chats:</b>\n")
		menuText.WriteString("Anyone posts a supported link — I'll automatically download and send the media.\n")
		menuText.WriteString("Group admins can manage limits and settings.\n\n")
	} else {
		menuText.WriteString("💬 <b>How to use in private chat:</b>\n")
		menuText.WriteString("Just send me any supported link and I'll reply with the actual media file.\n\n")
		menuText.WriteString("👥 <b>How to use in group chats:</b>\n")
		menuText.WriteString("1. Add me to your group\n")
		menuText.WriteString("2. Make sure <b>Group Privacy</b> is disabled via @BotFather\n")
		menuText.WriteString("3. Anyone posts a supported link — I'll download and send the media automatically\n\n")
	}

	// Commands
	menuText.WriteString("⚡ <b>Commands:</b>\n")
	menuText.WriteString("• /start, /help — Show this menu\n")
	menuText.WriteString("• /stats [period] — View media statistics\n")
	menuText.WriteString("  <i>Periods: today, week, month, year</i>\n")
	menuText.WriteString("• /mylimit — Check your daily media limit\n")
	menuText.WriteString("• /donate — Support the bot development\n")

	// Admin commands (only in groups)
	if isGroupChat {
		menuText.WriteString("\n👮 <b>Admin Commands:</b>\n")
		menuText.WriteString("• /setlimit @user [limit] — Set user's daily limit\n")
		menuText.WriteString("  <i>Reply to message or use user ID. Set to 0 to remove limit.</i>\n")
		menuText.WriteString("• /setgrouplimit [limit] — Set group default limit\n")
		menuText.WriteString("• /setgrouptimezone [timezone] — Set group timezone\n")
		menuText.WriteString("  <i>Example: America/New_York, Europe/London</i>\n")
	}

	// Footer
	menuText.WriteString("\n━━━━━━━━━━━━━━━━━━━━\n")
	menuText.WriteString("💚 Support the bot: /donate\n")
	menuText.WriteString("💡 <i>Tip: Just paste a link to get started!</i>")
	
	// Create interactive keyboard with menu buttons
	keyboard := createMainMenuKeyboard(isGroupChat)
	
	helpMsg := payload.TgPlSendMessage{
		ChatId:      origChat.Id,
		ParseMode:   payload.ParseModeHTML,
		Text:        menuText.String(),
		ReplyMarkup: keyboard,
	}
	
	message.NewSendMessage(helpMsg)
	log.Printf("✅ Sent interactive help/start message to user %d", origUser.Id)
}

// handleDonateCommand handles the /donate command
func handleDonateCommand(origMessage *response.TgMessage, origChat *response.TgChat, origUser *response.TgUser) {
	var text strings.Builder

	text.WriteString("💚 <b>Support Social Link Bot</b>\n\n")
	text.WriteString("If you find this bot useful, consider supporting its development!\n\n")
	text.WriteString("🪙 <b>Donate with any crypto:</b>\n")
	text.WriteString("Use the button below to donate via NOWPayments (USDT, BTC, ETH, and 300+ cryptos)\n\n")
	text.WriteString("━━━━━━━━━━━━━━━━━━━━\n\n")
	text.WriteString("💰 <b>Or send USDT directly:</b>\n\n")
	text.WriteString("<b>ERC-20:</b>\n")
	text.WriteString("<code>0xb3D102eA95Bf2376619730b8f3149131cc3F951B</code>\n\n")
	text.WriteString("<b>TRC-20:</b>\n")
	text.WriteString("<code>TWRfDPJ1TFUYJj47JcghvTEBnKcZWqgqcz</code>\n\n")
	text.WriteString("<i>Tap an address to copy it. Thank you!</i> 🙏")

	keyboard := &api.TgInlineKeyboardMarkup{
		InlineKeyboard: [][]api.TgInlineKeyboardButton{
			{
				{Text: "🪙 Donate via NOWPayments", URL: "https://nowpayments.io/donation/sociallinkbot"},
			},
			{
				{Text: "◀️ Back to Menu", CallbackData: "/help"},
			},
		},
	}

	donateMsg := payload.TgPlSendMessage{
		ChatId:              origChat.Id,
		ParseMode:           payload.ParseModeHTML,
		Text:                text.String(),
		ReplyToMessageId:    origMessage.MessageId,
		DisableNotification: true,
		ReplyMarkup:         keyboard,
	}

	message.NewSendMessage(donateMsg)
	log.Printf("💚 Sent donate info to user %d", origUser.Id)
}

// handleStatsCommand handles /stats command with actual database functionality
func handleStatsCommand(origMessage *response.TgMessage, origChat *response.TgChat, origUser *response.TgUser) {
	// Parse period from command arguments
	args := strings.Fields(origMessage.Text)
	period := "today" // Default period
	if len(args) > 1 {
		period = strings.ToLower(args[1])
	}
	
	// Validate period
	validPeriods := []string{"today", "week", "month", "year"}
	isValidPeriod := false
	for _, validPeriod := range validPeriods {
		if period == validPeriod {
			isValidPeriod = true
			break
		}
	}
	
	if !isValidPeriod {
		errorMsg := payload.TgPlSendMessage{
			ChatId:              origChat.Id,
			ParseMode:           payload.ParseModeHTML,
			Text:                "❌ Invalid period. Valid options: <code>today</code>, <code>week</code>, <code>month</code>, <code>year</code>",
			ReplyToMessageId:    origMessage.MessageId,
			DisableNotification: true,
		}
		message.NewSendMessage(errorMsg)
		return
	}
	
	// Get media statistics
	stats, err := services.GetMediaStats(origChat.Id, period)
	if err != nil {
		log.Printf("Error getting media stats: %v", err)
		errorMsg := payload.TgPlSendMessage{
			ChatId:              origChat.Id,
			ParseMode:           payload.ParseModeHTML,
			Text:                "❌ Error retrieving statistics. Please try again later.",
			ReplyToMessageId:    origMessage.MessageId,
			DisableNotification: true,
		}
		message.NewSendMessage(errorMsg)
		return
	}
	
	// Get user statistics
	userStats, err := services.GetUserMediaStats(origChat.Id, period)
	if err != nil {
		log.Printf("Error getting user media stats: %v", err)
	}
	
	// Build stats message
	var statsText strings.Builder
	
	// Period header
	periodEmoji := map[string]string{
		"today": "📅",
		"week":  "📊", 
		"month": "📉",
		"year":  "📋",
	}
	
	statsText.WriteString(fmt.Sprintf("%s <b>%s Statistics</b>\n", periodEmoji[period], strings.Title(period)))
	statsText.WriteString(fmt.Sprintf("<i>Chat: %s</i>\n\n", html.EscapeString(origChat.Title)))
	
	// Overall stats
	if stats.Total == 0 {
		statsText.WriteString("📭 No media shared " + period + ".\n")
	} else {
		statsText.WriteString(fmt.Sprintf("📊 <b>Total Media:</b> %d\n", stats.Total))
		statsText.WriteString(fmt.Sprintf("📷 Photos: %d\n", stats.TotalPhotos))
		statsText.WriteString(fmt.Sprintf("🎥 Videos: %d\n\n", stats.TotalVideos))
		
		// Platform breakdown
		if len(stats.Platforms) > 0 {
			statsText.WriteString("🌐 <b>By Platform:</b>\n")
			for _, platform := range stats.Platforms {
				platformEmoji := map[string]string{
					"instagram": "📷",
					"tiktok":    "🎵",
					"twitter":   "🐦",
					"youtube":   "📺",
					"reddit":    "🔴",
					"facebook":  "👥",
				}
				emoji := platformEmoji[strings.ToLower(platform.Platform)]
				if emoji == "" {
					emoji = "🔗"
				}
				
				statsText.WriteString(fmt.Sprintf("%s <b>%s:</b> %d", emoji, strings.Title(platform.Platform), platform.Total))
				if platform.Photos > 0 && platform.Videos > 0 {
					statsText.WriteString(fmt.Sprintf(" (📷%d, 🎥%d)", platform.Photos, platform.Videos))
				}
				statsText.WriteString("\n")
			}
		}
		
		// User breakdown (only in group chats)
		if origChat.Type != "private" && len(userStats) > 0 {
			statsText.WriteString("\n👥 <b>By User:</b>\n")
			displayCount := 10 // Show top 10 users
			if len(userStats) < displayCount {
				displayCount = len(userStats)
			}
			
			for i := 0; i < displayCount; i++ {
				user := userStats[i]
				username := user.Username
				if username == "" {
					username = fmt.Sprintf("User %d", user.UserID)
				}
				
				statsText.WriteString(fmt.Sprintf("• <b>%s:</b> %d", html.EscapeString(username), user.Total))
				if user.Photos > 0 && user.Videos > 0 {
					statsText.WriteString(fmt.Sprintf(" (📷%d, 🎥%d)", user.Photos, user.Videos))
				}
				statsText.WriteString("\n")
			}
			
			if len(userStats) > displayCount {
				statsText.WriteString(fmt.Sprintf("... and %d more users\n", len(userStats) - displayCount))
			}
		}
	}
	
	// Create stats message
	statsMsg := payload.TgPlSendMessage{
		ChatId:              origChat.Id,
		ParseMode:           payload.ParseModeHTML,
		Text:                statsText.String(),
		ReplyToMessageId:    origMessage.MessageId,
		DisableNotification: true,
	}
	
	message.NewSendMessage(statsMsg)
	log.Printf("📊 Stats command (%s) completed for user %d in chat %d", period, origUser.Id, origChat.Id)
}

// handleMyLimitCommand handles /mylimit command with actual database functionality
func handleMyLimitCommand(origMessage *response.TgMessage, origChat *response.TgChat, origUser *response.TgUser) {
	// Get user's effective limit
	limit, isGroupDefault, err := services.GetEffectiveUserLimit(int64(origUser.Id), origChat.Id)
	if err != nil {
		log.Printf("Error getting user limit: %v", err)
		errorMsg := payload.TgPlSendMessage{
			ChatId:              origChat.Id,
			ParseMode:           payload.ParseModeHTML,
			Text:                "❌ Error retrieving your limit. Please try again later.",
			ReplyToMessageId:    origMessage.MessageId,
			DisableNotification: true,
		}
		message.NewSendMessage(errorMsg)
		return
	}
	
	// Get current daily usage
	dailyCount, err := services.GetDailyMediaCount(int64(origUser.Id), origChat.Id)
	if err != nil {
		log.Printf("Error getting daily media count: %v", err)
		dailyCount = 0
	}
	
	// Build limit message
	var limitText strings.Builder
	
	// Header
	limitText.WriteString("🚫 <b>Your Media Limit</b>\n")
	if origChat.Type != "private" {
		limitText.WriteString(fmt.Sprintf("<i>Chat: %s</i>\n\n", html.EscapeString(origChat.Title)))
	} else {
		limitText.WriteString("\n")
	}
	
	// Current usage
	limitText.WriteString(fmt.Sprintf("📊 <b>Today's Usage:</b> %d media items\n", dailyCount))
	
	// Limit information
	if limit == 0 {
		limitText.WriteString("🔓 <b>Daily Limit:</b> No limit set\n")
		limitText.WriteString("💚 Status: <b>Unlimited</b>\n")
	} else {
		limitText.WriteString(fmt.Sprintf("🔒 <b>Daily Limit:</b> %d media items\n", limit))
		
		// Status
		if dailyCount >= limit {
			limitText.WriteString("🔴 Status: <b>Limit Reached</b>\n")
		} else {
			remaining := limit - dailyCount
			limitText.WriteString(fmt.Sprintf("🟢 Status: <b>%d remaining</b>\n", remaining))
		}
		
		// Limit type
		if isGroupDefault {
			limitText.WriteString("ℹ️ <i>Using group default limit</i>\n")
		} else {
			limitText.WriteString("👤 <i>Individual limit set</i>\n")
		}
	}
	
	// Reset time information
	if limit > 0 {
		resetTime, timeUntilReset, err := services.GetTimezoneAwareResetTime(origChat.Id)
		if err == nil {
			limitText.WriteString(fmt.Sprintf("\n🕐 <b>Limit resets:</b> %s\n", formatDuration(timeUntilReset)))
			limitText.WriteString(fmt.Sprintf("📅 <b>Reset time:</b> %s\n", resetTime.Format("15:04 MST")))
		}
	}
	
	// Footer
	if origChat.Type != "private" {
		limitText.WriteString("\n💡 <i>Admins can modify limits with /setlimit</i>")
	}
	
	limitMsg := payload.TgPlSendMessage{
		ChatId:              origChat.Id,
		ParseMode:           payload.ParseModeHTML,
		Text:                limitText.String(),
		ReplyToMessageId:    origMessage.MessageId,
		DisableNotification: true,
	}
	
	message.NewSendMessage(limitMsg)
	log.Printf("🚫 MyLimit command completed for user %d in chat %d (limit: %d, used: %d)", origUser.Id, origChat.Id, limit, dailyCount)
}

// formatDuration formats a duration into a human-readable string
func formatDuration(d time.Duration) string {
	if d < time.Hour {
		return fmt.Sprintf("%d minutes", int(d.Minutes()))
	} else if d < 24*time.Hour {
		hours := int(d.Hours())
		minutes := int(d.Minutes()) % 60
		if minutes == 0 {
			return fmt.Sprintf("%d hours", hours)
		}
		return fmt.Sprintf("%d hours %d minutes", hours, minutes)
	} else {
		days := int(d.Hours()) / 24
		hours := int(d.Hours()) % 24
		if hours == 0 {
			return fmt.Sprintf("%d days", days)
		}
		return fmt.Sprintf("%d days %d hours", days, hours)
	}
}

// handleSetLimitCommand handles /setlimit command with actual database functionality
func handleSetLimitCommand(origMessage *response.TgMessage, origChat *response.TgChat, origUser *response.TgUser) {
	if origChat.Type == "private" {
		errorMsg := payload.TgPlSendMessage{
			ChatId:              origChat.Id,
			Text:                "❌ This command only works in groups, not private chats.",
			ReplyToMessageId:    origMessage.MessageId,
			DisableNotification: true,
		}
		message.NewSendMessage(errorMsg)
		return
	}
	
	args := strings.Fields(origMessage.Text)
	
	// Parse arguments
	var targetUserID int64
	var limitValue int
	var err error
	
	if len(args) == 2 {
		// Format: /setlimit <limit> (for replied message)
		limitValue, err = strconv.Atoi(args[1])
		if err != nil {
			errorMsg := payload.TgPlSendMessage{
				ChatId:              origChat.Id,
				ParseMode:           payload.ParseModeHTML,
				Text:                "❌ Invalid limit value. Please use a number.\n\n<b>Usage:</b>\n• Reply to user's message + <code>/setlimit 5</code>\n• <code>/setlimit 123456789 10</code>",
				ReplyToMessageId:    origMessage.MessageId,
				DisableNotification: true,
			}
			message.NewSendMessage(errorMsg)
			return
		}
		
		// Check if this is a reply to another message
		if origMessage.ReplyToMessage != nil && origMessage.ReplyToMessage.From.Id != 0 {
			targetUserID = int64(origMessage.ReplyToMessage.From.Id)
		} else {
			errorMsg := payload.TgPlSendMessage{
				ChatId:              origChat.Id,
				ParseMode:           payload.ParseModeHTML,
				Text:                "❌ Please reply to a user's message or provide user ID.\n\n<b>Usage:</b>\n• Reply to user's message + <code>/setlimit 5</code>\n• <code>/setlimit 123456789 10</code>",
				ReplyToMessageId:    origMessage.MessageId,
				DisableNotification: true,
			}
			message.NewSendMessage(errorMsg)
			return
		}
	} else if len(args) == 3 {
		// Format: /setlimit <user_id> <limit>
		targetUserID, err = strconv.ParseInt(args[1], 10, 64)
		if err != nil {
			errorMsg := payload.TgPlSendMessage{
				ChatId:              origChat.Id,
				ParseMode:           payload.ParseModeHTML,
				Text:                "❌ Invalid user ID. Please use a valid number.\n\n<b>Usage:</b>\n• Reply to user's message + <code>/setlimit 5</code>\n• <code>/setlimit 123456789 10</code>",
				ReplyToMessageId:    origMessage.MessageId,
				DisableNotification: true,
			}
			message.NewSendMessage(errorMsg)
			return
		}
		
		limitValue, err = strconv.Atoi(args[2])
		if err != nil {
			errorMsg := payload.TgPlSendMessage{
				ChatId:              origChat.Id,
				ParseMode:           payload.ParseModeHTML,
				Text:                "❌ Invalid limit value. Please use a number.\n\n<b>Usage:</b>\n• Reply to user's message + <code>/setlimit 5</code>\n• <code>/setlimit 123456789 10</code>",
				ReplyToMessageId:    origMessage.MessageId,
				DisableNotification: true,
			}
			message.NewSendMessage(errorMsg)
			return
		}
	} else {
		// Invalid format
		errorMsg := payload.TgPlSendMessage{
			ChatId:              origChat.Id,
			ParseMode:           payload.ParseModeHTML,
			Text:                "❌ Invalid command format.\n\n<b>Usage:</b>\n• Reply to user's message + <code>/setlimit 5</code>\n• <code>/setlimit 123456789 10</code>\n• <code>/setlimit 0</code> (remove limit)",
			ReplyToMessageId:    origMessage.MessageId,
			DisableNotification: true,
		}
		message.NewSendMessage(errorMsg)
		return
	}
	
	// Validate limit value
	if limitValue < 0 {
		errorMsg := payload.TgPlSendMessage{
			ChatId:              origChat.Id,
			ParseMode:           payload.ParseModeHTML,
			Text:                "❌ Limit value cannot be negative. Use 0 to remove limit.",
			ReplyToMessageId:    origMessage.MessageId,
			DisableNotification: true,
		}
		message.NewSendMessage(errorMsg)
		return
	}
	
	// Set the limit
	err = services.SetUserMediaLimit(targetUserID, origChat.Id, limitValue)
	if err != nil {
		log.Printf("Error setting user limit: %v", err)
		errorMsg := payload.TgPlSendMessage{
			ChatId:              origChat.Id,
			ParseMode:           payload.ParseModeHTML,
			Text:                "❌ Error setting user limit. Please try again later.",
			ReplyToMessageId:    origMessage.MessageId,
			DisableNotification: true,
		}
		message.NewSendMessage(errorMsg)
		return
	}
	
	// Get current usage for the user
	dailyCount, err := services.GetDailyMediaCount(targetUserID, origChat.Id)
	if err != nil {
		log.Printf("Error getting daily media count: %v", err)
		dailyCount = 0
	}
	
	// Build success message
	var successText strings.Builder
	successText.WriteString("✅ <b>User Limit Updated</b>\n\n")
	
	if limitValue == 0 {
		successText.WriteString(fmt.Sprintf("🔓 <b>User %d:</b> Limit removed\n", targetUserID))
		successText.WriteString("💚 Status: <b>Unlimited</b>\n")
	} else {
		successText.WriteString(fmt.Sprintf("🔒 <b>User %d:</b> Limit set to %d\n", targetUserID, limitValue))
		successText.WriteString(fmt.Sprintf("📊 <b>Today's Usage:</b> %d media items\n", dailyCount))
		
		if dailyCount >= limitValue {
			successText.WriteString("🔴 Status: <b>Limit Reached</b>\n")
		} else {
			remaining := limitValue - dailyCount
			successText.WriteString(fmt.Sprintf("🟢 Status: <b>%d remaining</b>\n", remaining))
		}
	}
	
	successText.WriteString(fmt.Sprintf("\n👮 <i>Set by admin: %s</i>", html.EscapeString(origUser.Username)))
	
	successMsg := payload.TgPlSendMessage{
		ChatId:              origChat.Id,
		ParseMode:           payload.ParseModeHTML,
		Text:                successText.String(),
		ReplyToMessageId:    origMessage.MessageId,
		DisableNotification: true,
	}
	
	message.NewSendMessage(successMsg)
	log.Printf("👮 SetLimit command completed by user %d in chat %d (target: %d, limit: %d)", origUser.Id, origChat.Id, targetUserID, limitValue)
}

// handleSetGroupLimitCommand handles /setgrouplimit command with actual database functionality
func handleSetGroupLimitCommand(origMessage *response.TgMessage, origChat *response.TgChat, origUser *response.TgUser) {
	if origChat.Type == "private" {
		errorMsg := payload.TgPlSendMessage{
			ChatId:              origChat.Id,
			Text:                "❌ Group limit commands only work in groups, not private chats.",
			ReplyToMessageId:    origMessage.MessageId,
			DisableNotification: true,
		}
		message.NewSendMessage(errorMsg)
		return
	}
	
	args := strings.Fields(origMessage.Text)
	
	// Check if limit value is provided
	if len(args) != 2 {
		errorMsg := payload.TgPlSendMessage{
			ChatId:              origChat.Id,
			ParseMode:           payload.ParseModeHTML,
			Text:                "❌ Invalid command format.\n\n<b>Usage:</b>\n• <code>/setgrouplimit 5</code> (set limit to 5)\n• <code>/setgrouplimit 0</code> (remove limit)",
			ReplyToMessageId:    origMessage.MessageId,
			DisableNotification: true,
		}
		message.NewSendMessage(errorMsg)
		return
	}
	
	// Parse limit value
	limitValue, err := strconv.Atoi(args[1])
	if err != nil {
		errorMsg := payload.TgPlSendMessage{
			ChatId:              origChat.Id,
			ParseMode:           payload.ParseModeHTML,
			Text:                "❌ Invalid limit value. Please use a number.\n\n<b>Usage:</b>\n• <code>/setgrouplimit 5</code> (set limit to 5)\n• <code>/setgrouplimit 0</code> (remove limit)",
			ReplyToMessageId:    origMessage.MessageId,
			DisableNotification: true,
		}
		message.NewSendMessage(errorMsg)
		return
	}
	
	// Validate limit value
	if limitValue < 0 {
		errorMsg := payload.TgPlSendMessage{
			ChatId:              origChat.Id,
			ParseMode:           payload.ParseModeHTML,
			Text:                "❌ Limit value cannot be negative. Use 0 to remove limit.",
			ReplyToMessageId:    origMessage.MessageId,
			DisableNotification: true,
		}
		message.NewSendMessage(errorMsg)
		return
	}
	
	// Set the group limit
	err = services.SetGroupMediaLimit(origChat.Id, origChat.Title, limitValue)
	if err != nil {
		log.Printf("Error setting group limit: %v", err)
		errorMsg := payload.TgPlSendMessage{
			ChatId:              origChat.Id,
			ParseMode:           payload.ParseModeHTML,
			Text:                "❌ Error setting group limit. Please try again later.",
			ReplyToMessageId:    origMessage.MessageId,
			DisableNotification: true,
		}
		message.NewSendMessage(errorMsg)
		return
	}
	
	// Build success message
	var successText strings.Builder
	successText.WriteString("✅ <b>Group Default Limit Updated</b>\n\n")
	
	if limitValue == 0 {
		successText.WriteString("🔓 <b>Group Limit:</b> Removed\n")
		successText.WriteString("💚 <b>Status:</b> No default limit for new users\n")
	} else {
		successText.WriteString(fmt.Sprintf("🔒 <b>Group Limit:</b> %d media items per day\n", limitValue))
		successText.WriteString("📋 <b>Status:</b> Applied to all users without individual limits\n")
	}
	
	// Get current timezone
	timezone, err := services.GetGroupTimezone(origChat.Id)
	if err == nil && timezone != "" {
		successText.WriteString(fmt.Sprintf("🌍 <b>Timezone:</b> %s\n", timezone))
	}
	
	successText.WriteString(fmt.Sprintf("\n👮 <i>Set by admin: %s</i>", html.EscapeString(origUser.Username)))
	successText.WriteString("\n\n💡 <i>Individual user limits override group default</i>")
	
	successMsg := payload.TgPlSendMessage{
		ChatId:              origChat.Id,
		ParseMode:           payload.ParseModeHTML,
		Text:                successText.String(),
		ReplyToMessageId:    origMessage.MessageId,
		DisableNotification: true,
	}
	
	message.NewSendMessage(successMsg)
	log.Printf("🏢 SetGroupLimit command completed by user %d in chat %d (limit: %d)", origUser.Id, origChat.Id, limitValue)
}

// handleSetGroupTimezoneCommand handles /setgrouptimezone command with actual database functionality
func handleSetGroupTimezoneCommand(origMessage *response.TgMessage, origChat *response.TgChat, origUser *response.TgUser) {
	if origChat.Type == "private" {
		errorMsg := payload.TgPlSendMessage{
			ChatId:              origChat.Id,
			Text:                "❌ Group timezone commands only work in groups, not private chats.",
			ReplyToMessageId:    origMessage.MessageId,
			DisableNotification: true,
		}
		message.NewSendMessage(errorMsg)
		return
	}
	
	args := strings.Fields(origMessage.Text)
	
	// Check if timezone is provided
	if len(args) != 2 {
		errorMsg := payload.TgPlSendMessage{
			ChatId:              origChat.Id,
			ParseMode:           payload.ParseModeHTML,
			Text:                "❌ Invalid command format.\n\n<b>Usage:</b>\n• <code>/setgrouptimezone America/New_York</code>\n• <code>/setgrouptimezone Europe/London</code>\n• <code>/setgrouptimezone UTC</code>\n\n<b>Common timezones:</b>\n• UTC, GMT\n• America/New_York, America/Los_Angeles\n• Europe/London, Europe/Paris, Europe/Berlin\n• Asia/Tokyo, Asia/Shanghai, Asia/Dubai\n• Australia/Sydney, Australia/Melbourne",
			ReplyToMessageId:    origMessage.MessageId,
			DisableNotification: true,
		}
		message.NewSendMessage(errorMsg)
		return
	}
	
	// Parse timezone
	timezone := args[1]
	
	// Validate timezone
	if !isTimezoneValid(timezone) {
		errorMsg := payload.TgPlSendMessage{
			ChatId:              origChat.Id,
			ParseMode:           payload.ParseModeHTML,
			Text:                fmt.Sprintf("❌ Invalid timezone: <code>%s</code>\n\n<b>Usage:</b>\n• <code>/setgrouptimezone America/New_York</code>\n• <code>/setgrouptimezone Europe/London</code>\n• <code>/setgrouptimezone UTC</code>\n\n<b>Common timezones:</b>\n• UTC, GMT\n• America/New_York, America/Los_Angeles\n• Europe/London, Europe/Paris, Europe/Berlin\n• Asia/Tokyo, Asia/Shanghai, Asia/Dubai\n• Australia/Sydney, Australia/Melbourne", html.EscapeString(timezone)),
			ReplyToMessageId:    origMessage.MessageId,
			DisableNotification: true,
		}
		message.NewSendMessage(errorMsg)
		return
	}
	
	// Set the group timezone
	err := services.SetGroupTimezone(origChat.Id, origChat.Title, timezone)
	if err != nil {
		log.Printf("Error setting group timezone: %v", err)
		errorMsg := payload.TgPlSendMessage{
			ChatId:              origChat.Id,
			ParseMode:           payload.ParseModeHTML,
			Text:                "❌ Error setting group timezone. Please try again later.",
			ReplyToMessageId:    origMessage.MessageId,
			DisableNotification: true,
		}
		message.NewSendMessage(errorMsg)
		return
	}
	
	// Get current time in the new timezone
	loc := loadTimezoneWithFallback(timezone)
	currentTime := time.Now().In(loc)
	
	// Calculate next reset time
	_, timeUntilReset, err := services.GetTimezoneAwareResetTime(origChat.Id)
	if err != nil {
		log.Printf("Error getting reset time: %v", err)
	}
	
	// Build success message
	var successText strings.Builder
	successText.WriteString("✅ <b>Group Timezone Updated</b>\n\n")
	successText.WriteString(fmt.Sprintf("🌍 <b>Timezone:</b> %s\n", timezone))
	successText.WriteString(fmt.Sprintf("🕐 <b>Current time:</b> %s\n", currentTime.Format("15:04:05 MST")))
	
	if timeUntilReset > 0 {
		successText.WriteString(fmt.Sprintf("⏰ <b>Next limit reset:</b> %s\n", formatDuration(timeUntilReset)))
	}
	
	// Get current group limit
	groupLimit, err := services.GetGroupMediaLimit(origChat.Id)
	if err == nil && groupLimit > 0 {
		successText.WriteString(fmt.Sprintf("🔒 <b>Group Limit:</b> %d media items per day\n", groupLimit))
	}
	
	successText.WriteString(fmt.Sprintf("\n👮 <i>Set by admin: %s</i>", html.EscapeString(origUser.Username)))
	successText.WriteString("\n\n💡 <i>Daily limits now reset at midnight in this timezone</i>")
	
	successMsg := payload.TgPlSendMessage{
		ChatId:              origChat.Id,
		ParseMode:           payload.ParseModeHTML,
		Text:                successText.String(),
		ReplyToMessageId:    origMessage.MessageId,
		DisableNotification: true,
	}
	
	message.NewSendMessage(successMsg)
	log.Printf("🌍 SetGroupTimezone command completed by user %d in chat %d (timezone: %s)", origUser.Id, origChat.Id, timezone)
}

// handleAdminStatsCommand handles /adminstats command (admin only)
func handleAdminStatsCommand(origMessage *response.TgMessage, origChat *response.TgChat, origUser *response.TgUser) {
	// Check if user is admin
	adminUserID := config.GetAdminUserID()
	if adminUserID == 0 {
		log.Printf("⚠️ Admin stats requested but ADMIN_USER_ID not configured")
		return
	}

	if int64(origUser.Id) != adminUserID {
		log.Printf("🚫 Unauthorized admin stats request from user %d (admin is %d)", origUser.Id, adminUserID)
		return
	}

	// Parse arguments
	args := strings.Fields(origMessage.Text)

	// Check for subcommand: /adminstats chats [period]
	if (len(args) > 1) && (strings.ToLower(args[1]) == "chats") {
		period := "all"
		if len(args) > 2 {
			period = strings.ToLower(args[2])
		}
		handleAdminChatsCommand(origMessage, origChat, period)
		return
	}

	// Parse period from command arguments (default: all)
	period := "all"
	if len(args) > 1 {
		period = strings.ToLower(args[1])
	}

	// Validate period
	validPeriods := map[string]bool{"today": true, "week": true, "month": true, "year": true, "all": true}
	if !validPeriods[period] {
		errorMsg := payload.TgPlSendMessage{
			ChatId:              origChat.Id,
			ParseMode:           payload.ParseModeHTML,
			Text:                "❌ Invalid period. Use: <code>today</code>, <code>week</code>, <code>month</code>, <code>year</code>, or <code>all</code>\n\n💡 Or use <code>/adminstats chats [period]</code> to see all chats",
			ReplyToMessageId:    origMessage.MessageId,
			DisableNotification: true,
		}
		message.NewSendMessage(errorMsg)
		return
	}

	// Get global stats
	stats, err := services.GetGlobalStats(period)
	if err != nil {
		log.Printf("Error getting global stats: %v", err)
		errorMsg := payload.TgPlSendMessage{
			ChatId:              origChat.Id,
			Text:                "❌ Error fetching statistics. Please try again later.",
			ReplyToMessageId:    origMessage.MessageId,
			DisableNotification: true,
		}
		message.NewSendMessage(errorMsg)
		return
	}

	// Build response message
	var statsText strings.Builder
	periodLabels := map[string]string{
		"today": "Today",
		"week":  "Last 7 Days",
		"month": "Last 30 Days",
		"year":  "Last Year",
		"all":   "All Time",
	}

	statsText.WriteString(fmt.Sprintf("📊 <b>Bot Admin Statistics</b>\n"))
	statsText.WriteString(fmt.Sprintf("📅 <i>Period: %s</i>\n\n", periodLabels[period]))

	// Overview
	statsText.WriteString("<b>📈 Overview</b>\n")
	statsText.WriteString(fmt.Sprintf("• Chats: <b>%d</b>\n", stats.TotalChats))
	statsText.WriteString(fmt.Sprintf("• Total Media: <b>%d</b>\n", stats.TotalLinks))
	statsText.WriteString(fmt.Sprintf("  └ 📷 Photos: %d\n", stats.TotalPhotos))
	statsText.WriteString(fmt.Sprintf("  └ 🎬 Videos: %d\n", stats.TotalVideos))
	if stats.TotalDocuments > 0 {
		statsText.WriteString(fmt.Sprintf("  └ 📄 Documents: %d\n", stats.TotalDocuments))
	}

	// Platform breakdown
	if len(stats.PlatformStats) > 0 {
		statsText.WriteString("\n<b>🌐 By Platform</b>\n")
		platformEmoji := map[string]string{
			"instagram": "📷",
			"twitter":   "🐦",
			"tiktok":    "🎵",
			"youtube":   "📺",
			"reddit":    "🔴",
			"facebook":  "👥",
		}
		for _, ps := range stats.PlatformStats {
			emoji := platformEmoji[strings.ToLower(ps.Platform)]
			if emoji == "" {
				emoji = "📱"
			}
			statsText.WriteString(fmt.Sprintf("• %s %s: <b>%d</b> (📷%d 🎬%d)\n",
				emoji, ps.Platform, ps.Total, ps.Photos, ps.Videos))
		}
	}

	// Top chats
	if len(stats.TopChats) > 0 {
		statsText.WriteString("\n<b>🏆 Top Chats</b>\n")
		for i, cs := range stats.TopChats {
			if i >= 5 {
				break
			}
			chatName := cs.ChatTitle
			if len(chatName) > 25 {
				chatName = chatName[:22] + "..."
			}
			statsText.WriteString(fmt.Sprintf("%d. %s: <b>%d</b>\n", i+1, html.EscapeString(chatName), cs.Total))
		}
	}

	statsText.WriteString("\n━━━━━━━━━━━━━━━━━━━━")
	statsText.WriteString("\n💡 <code>/adminstats [period]</code> - change period")
	statsText.WriteString("\n💡 <code>/adminstats chats [period]</code> - all chats")

	statsMsg := payload.TgPlSendMessage{
		ChatId:              origChat.Id,
		ParseMode:           payload.ParseModeHTML,
		Text:                statsText.String(),
		ReplyToMessageId:    origMessage.MessageId,
		DisableNotification: true,
	}

	message.NewSendMessage(statsMsg)
	log.Printf("📊 Admin stats command completed for user %d (period: %s)", origUser.Id, period)
}

// handleAdminChatsCommand handles /adminstats chats subcommand
func handleAdminChatsCommand(origMessage *response.TgMessage, origChat *response.TgChat, period string) {
	// Validate period
	validPeriods := map[string]bool{"today": true, "week": true, "month": true, "year": true, "all": true}
	if !validPeriods[period] {
		errorMsg := payload.TgPlSendMessage{
			ChatId:              origChat.Id,
			ParseMode:           payload.ParseModeHTML,
			Text:                "❌ Invalid period. Use: <code>today</code>, <code>week</code>, <code>month</code>, <code>year</code>, or <code>all</code>",
			ReplyToMessageId:    origMessage.MessageId,
			DisableNotification: true,
		}
		message.NewSendMessage(errorMsg)
		return
	}

	// Get all chats with stats
	chats, err := services.GetAllChatsWithStats(period)
	if err != nil {
		log.Printf("Error getting all chats: %v", err)
		errorMsg := payload.TgPlSendMessage{
			ChatId:              origChat.Id,
			Text:                "❌ Error fetching chat statistics. Please try again later.",
			ReplyToMessageId:    origMessage.MessageId,
			DisableNotification: true,
		}
		message.NewSendMessage(errorMsg)
		return
	}

	periodLabels := map[string]string{
		"today": "Today",
		"week":  "Last 7 Days",
		"month": "Last 30 Days",
		"year":  "Last Year",
		"all":   "All Time",
	}

	// Build response - may need multiple messages if too long
	var statsText strings.Builder
	statsText.WriteString(fmt.Sprintf("📋 <b>All Chats Statistics</b>\n"))
	statsText.WriteString(fmt.Sprintf("📅 <i>Period: %s</i>\n", periodLabels[period]))
	statsText.WriteString(fmt.Sprintf("📊 <i>Total: %d chats</i>\n\n", len(chats)))

	if len(chats) == 0 {
		statsText.WriteString("<i>No chat activity in this period</i>")
	} else {
		for i, cs := range chats {
			chatLine := fmt.Sprintf("<b>%d.</b> %s\n    📷%d 🎬%d 📄%d = <b>%d</b>\n",
				i+1,
				html.EscapeString(cs.ChatTitle),
				cs.Photos,
				cs.Videos,
				cs.Documents,
				cs.Total,
			)

			// Check if adding this line would exceed Telegram's message limit (4096 chars)
			if statsText.Len()+len(chatLine) > 3900 {
				// Send current message and start a new one
				statsMsg := payload.TgPlSendMessage{
					ChatId:              origChat.Id,
					ParseMode:           payload.ParseModeHTML,
					Text:                statsText.String(),
					DisableNotification: true,
				}
				message.NewSendMessage(statsMsg)

				// Reset for continuation
				statsText.Reset()
				statsText.WriteString("<b>📋 All Chats (continued)</b>\n\n")
			}

			statsText.WriteString(chatLine)
		}
	}

	// Send final message
	statsMsg := payload.TgPlSendMessage{
		ChatId:              origChat.Id,
		ParseMode:           payload.ParseModeHTML,
		Text:                statsText.String(),
		ReplyToMessageId:    origMessage.MessageId,
		DisableNotification: true,
	}
	message.NewSendMessage(statsMsg)
	log.Printf("📋 Admin chats command completed (period: %s, chats: %d)", period, len(chats))
}

// createMainMenuKeyboard creates the interactive main menu keyboard
func createMainMenuKeyboard(isGroupChat bool) *api.TgInlineKeyboardMarkup {
	// First row - Today and Week stats
	buttons := [][]api.TgInlineKeyboardButton{
		{
			{Text: "📅 Today Stats", CallbackData: "/stats today"},
			{Text: "📊 Week Stats", CallbackData: "/stats week"},
		},
	}
	
	// Second row - Month and Year stats
	buttons = append(buttons, []api.TgInlineKeyboardButton{
		{Text: "📉 Month Stats", CallbackData: "/stats month"},
		{Text: "📋 Year Stats", CallbackData: "/stats year"},
	})
	
	// Third row - User actions
	if isGroupChat {
		// Group chat - show user limit check
		buttons = append(buttons, []api.TgInlineKeyboardButton{
			{Text: "🚫 My Limit", CallbackData: "/mylimit"},
		})
		
		// Fourth row - Admin actions
		buttons = append(buttons, []api.TgInlineKeyboardButton{
			{Text: "👮 Set User Limit", CallbackData: "/setlimit_start"},
			{Text: "🏢 Group Settings", CallbackData: "/group_settings"},
		})
	} else {
		// Private chat - only show user limit
		buttons = append(buttons, []api.TgInlineKeyboardButton{
			{Text: "🚫 My Limit", CallbackData: "/mylimit"},
		})
	}
	
	// Donate row
	buttons = append(buttons, []api.TgInlineKeyboardButton{
		{Text: "💚 Donate (USDT)", CallbackData: "/donate"},
	})

	// Last row - Help (refresh menu)
	buttons = append(buttons, []api.TgInlineKeyboardButton{
		{Text: "🔄 Refresh Menu", CallbackData: "/help"},
	})
	
	return &api.TgInlineKeyboardMarkup{
		InlineKeyboard: buttons,
	}
}

// handleCallbackQuery handles button presses from inline keyboards
func handleCallbackQuery(callbackQuery *response.TgCallbackQuery) {
	log.Printf("📱 Handling callback query: %s from user %d", callbackQuery.Data, callbackQuery.From.Id)
	
	// Create a fake message object to reuse existing command handlers
	fakeMessage := response.TgMessage{
		MessageId: callbackQuery.Message.MessageId,
		From:      callbackQuery.From,
		Chat:      callbackQuery.Message.Chat,
		Text:      callbackQuery.Data,
		Date:      int(time.Now().Unix()),
	}
	
	// Process the command based on callback data
	if strings.HasPrefix(callbackQuery.Data, "/stats") {
		handleStatsCommand(&fakeMessage, &callbackQuery.Message.Chat, &callbackQuery.From)
	} else if strings.HasPrefix(callbackQuery.Data, "/mylimit") {
		handleMyLimitCommand(&fakeMessage, &callbackQuery.Message.Chat, &callbackQuery.From)
	} else if callbackQuery.Data == "/donate" {
		handleDonateCommand(&fakeMessage, &callbackQuery.Message.Chat, &callbackQuery.From)
	} else if strings.HasPrefix(callbackQuery.Data, "/help") {
		handleStartCommand(&fakeMessage, &callbackQuery.Message.Chat, &callbackQuery.From)
	} else if callbackQuery.Data == "/setlimit_start" {
		handleSetLimitStartCallback(&callbackQuery.Message.Chat, &callbackQuery.From, callbackQuery.Message.MessageId)
	} else if callbackQuery.Data == "/group_settings" {
		handleGroupSettingsCallback(&callbackQuery.Message.Chat, &callbackQuery.From, callbackQuery.Message.MessageId)
	} else if strings.HasPrefix(callbackQuery.Data, "/setgrouplimit_") {
		handleSetGroupLimitCallback(callbackQuery)
	} else if callbackQuery.Data == "/timezone_menu" {
		handleTimezoneMenuCallback(&callbackQuery.Message.Chat, &callbackQuery.From)
	} else if strings.HasPrefix(callbackQuery.Data, "/tz_") {
		handleTimezoneRegionCallback(callbackQuery)
	} else if strings.HasPrefix(callbackQuery.Data, "/setgrouptimezone_") {
		handleSetTimezoneCallback(callbackQuery)
	} else if callbackQuery.Data == "/platform_settings" {
		handlePlatformSettingsCallback(&callbackQuery.Message.Chat, &callbackQuery.From, callbackQuery.Message.MessageId)
	} else if strings.HasPrefix(callbackQuery.Data, "/toggle_platform_") {
		handleTogglePlatformCallback(*callbackQuery)
	} else if callbackQuery.Data == "/platform_enable_all" {
		handlePlatformEnableAllCallback(*callbackQuery)
	} else if callbackQuery.Data == "/platform_disable_all" {
		handlePlatformDisableAllCallback(*callbackQuery)
	} else if callbackQuery.Data == "/description_settings" {
		handleDescriptionSettingsCallback(&callbackQuery.Message.Chat, &callbackQuery.From, callbackQuery.Message.MessageId)
	} else if strings.HasPrefix(callbackQuery.Data, "/toggle_description_") {
		handleToggleDescriptionCallback(*callbackQuery)
	} else if callbackQuery.Data == "/description_enable_all" {
		handleDescriptionEnableAllCallback(*callbackQuery)
	} else if callbackQuery.Data == "/description_disable_all" {
		handleDescriptionDisableAllCallback(*callbackQuery)
	} else {
		log.Printf("🤷 Unknown callback query: %s", callbackQuery.Data)
	}
	
	// Answer the callback query to remove the loading state
	answerCallbackQuery(callbackQuery.Id)
}

// handleSetLimitStartCallback handles the "Set User Limit" button press
func handleSetLimitStartCallback(origChat *response.TgChat, origUser *response.TgUser, messageId int) {
	log.Printf("👮 Admin %d initiated set limit process in chat %d", origUser.Id, origChat.Id)
	
	instructionMsg := payload.TgPlSendMessage{
		ChatId:    origChat.Id,
		ParseMode: payload.ParseModeHTML,
		Text:      "👮 <b>Set User Media Limit</b>\n\n📝 <b>How to set limits:</b>\n\n🔹 <b>Reply to user's message + send:</b>\n   <code>/setlimit [number]</code>\n\n🔹 <b>Or use user ID:</b>\n   <code>/setlimit [user_id] [number]</code>\n\n📋 <b>Examples:</b>\n• <code>/setlimit 5</code> (reply to user)\n• <code>/setlimit 123456789 10</code> (user ID)\n• <code>/setlimit 0</code> (remove limit)\n\n💡 <i>Tip: Reply to someone's message, then send the command for easiest setup!</i>",
	}
	
	message.NewSendMessage(instructionMsg)
}

// handleGroupSettingsCallback handles the "Group Settings" button press
func handleGroupSettingsCallback(origChat *response.TgChat, origUser *response.TgUser, messageId int) {
	log.Printf("🏢 Admin %d opened group settings in chat %d", origUser.Id, origChat.Id)
	
	// Get current group settings
	groupInfo, err := services.GetGroupLimitInfo(origChat.Id)
	if err != nil {
		log.Printf("Error getting group limit info: %v", err)
		errorMsg := payload.TgPlSendMessage{
			ChatId:    origChat.Id,
			ParseMode: payload.ParseModeHTML,
			Text:      "❌ Error retrieving group settings. Please try again later.",
		}
		message.NewSendMessage(errorMsg)
		return
	}
	
	// Build settings message
	var settingsText strings.Builder
	settingsText.WriteString("🏢 <b>Group Settings</b>\n")
	settingsText.WriteString(fmt.Sprintf("<i>Chat: %s</i>\n\n", html.EscapeString(origChat.Title)))
	
	// Group limit settings
	settingsText.WriteString("📊 <b>Group Default Limit:</b>\n")
	if groupInfo.DefaultDailyLimit == 0 {
		settingsText.WriteString("🔓 No default limit set\n")
		settingsText.WriteString("💚 <b>Status:</b> Users have unlimited access\n")
	} else {
		settingsText.WriteString(fmt.Sprintf("🔒 %d media items per day\n", groupInfo.DefaultDailyLimit))
		settingsText.WriteString("📋 <b>Status:</b> Applied to users without individual limits\n")
	}
	
	// Timezone settings
	settingsText.WriteString(fmt.Sprintf("\n🌍 <b>Timezone:</b> %s\n", groupInfo.Timezone))
	
	// Get current time in group timezone
	loc, err := time.LoadLocation(groupInfo.Timezone)
	if err == nil {
		currentTime := time.Now().In(loc)
		settingsText.WriteString(fmt.Sprintf("🕐 <b>Current time:</b> %s\n", currentTime.Format("15:04:05 MST")))
		
		// Calculate next reset time
		_, timeUntilReset, err := services.GetTimezoneAwareResetTime(origChat.Id)
		if err == nil && timeUntilReset > 0 {
			settingsText.WriteString(fmt.Sprintf("⏰ <b>Next limit reset:</b> %s\n", formatDuration(timeUntilReset)))
		}
	}
	
	// Management commands
	settingsText.WriteString("\n⚙️ <b>Management Commands:</b>\n")
	settingsText.WriteString("• <code>/setgrouplimit [number]</code> - Set group default limit\n")
	settingsText.WriteString("• <code>/setgrouptimezone [timezone]</code> - Set group timezone\n")
	settingsText.WriteString("• <code>/setlimit @user [number]</code> - Set individual user limit\n")
	
	// Examples
	settingsText.WriteString("\n💡 <b>Examples:</b>\n")
	settingsText.WriteString("• <code>/setgrouplimit 5</code> (5 items per day)\n")
	settingsText.WriteString("• <code>/setgrouplimit 0</code> (remove limit)\n")
	settingsText.WriteString("• <code>/setgrouptimezone Europe/London</code>\n")
	settingsText.WriteString("• <code>/setgrouptimezone America/New_York</code>\n")
	
	// Footer
	settingsText.WriteString("\n━━━━━━━━━━━━━━━━━━━━\n")
	settingsText.WriteString("📈 Use buttons below to configure settings")
	
	// Create interactive keyboard for group settings
	keyboard := createGroupSettingsKeyboard()
	
	settingsMsg := payload.TgPlSendMessage{
		ChatId:      origChat.Id,
		ParseMode:   payload.ParseModeHTML,
		Text:        settingsText.String(),
		ReplyMarkup: keyboard,
	}
	
	message.NewSendMessage(settingsMsg)
}

// createGroupSettingsKeyboard creates the interactive keyboard for group settings
func createGroupSettingsKeyboard() *api.TgInlineKeyboardMarkup {
	buttons := [][]api.TgInlineKeyboardButton{
		// First row - Set limits
		{
			{Text: "🔒 Set Limit: 5/day", CallbackData: "/setgrouplimit_5"},
			{Text: "🔒 Set Limit: 10/day", CallbackData: "/setgrouplimit_10"},
		},
		// Second row - More limit options
		{
			{Text: "🔒 Set Limit: 20/day", CallbackData: "/setgrouplimit_20"},
			{Text: "🔓 Remove Limit", CallbackData: "/setgrouplimit_0"},
		},
		// Third row - Custom limit
		{
			{Text: "⚙️ Set Custom Limit", CallbackData: "/setgrouplimit_custom"},
		},
		// Fourth row - Platform settings
		{
			{Text: "📱 Platform Settings", CallbackData: "/platform_settings"},
		},
		// Fifth row - Description settings
		{
			{Text: "📝 Description Settings", CallbackData: "/description_settings"},
		},
		// Sixth row - Timezone settings
		{
			{Text: "🌍 Change Timezone", CallbackData: "/timezone_menu"},
		},
		// Sixth row - Back to main menu
		{
			{Text: "⬅️ Back to Main Menu", CallbackData: "/help"},
		},
	}
	
	return &api.TgInlineKeyboardMarkup{
		InlineKeyboard: buttons,
	}
}

// createTimezoneMenuKeyboard creates the timezone selection menu
func createTimezoneMenuKeyboard() *api.TgInlineKeyboardMarkup {
	buttons := [][]api.TgInlineKeyboardButton{
		// Continent selection
		{
			{Text: "🌎 Americas", CallbackData: "/tz_americas"},
			{Text: "🌍 Europe", CallbackData: "/tz_europe"},
		},
		{
			{Text: "🌏 Asia", CallbackData: "/tz_asia"},
			{Text: "🌏 Australia", CallbackData: "/tz_australia"},
		},
		{
			{Text: "🌍 Africa", CallbackData: "/tz_africa"},
			{Text: "⏰ UTC/GMT", CallbackData: "/setgrouptimezone_UTC"},
		},
		// Back button
		{
			{Text: "⬅️ Back to Settings", CallbackData: "/group_settings"},
		},
	}
	
	return &api.TgInlineKeyboardMarkup{
		InlineKeyboard: buttons,
	}
}

// createAmericasTimezoneKeyboard creates timezone selection for Americas
func createAmericasTimezoneKeyboard() *api.TgInlineKeyboardMarkup {
	buttons := [][]api.TgInlineKeyboardButton{
		{
			{Text: "🗽 New York", CallbackData: "/setgrouptimezone_America/New_York"},
			{Text: "🌁 Los Angeles", CallbackData: "/setgrouptimezone_America/Los_Angeles"},
		},
		{
			{Text: "🏔️ Denver", CallbackData: "/setgrouptimezone_America/Denver"},
			{Text: "🌆 Chicago", CallbackData: "/setgrouptimezone_America/Chicago"},
		},
		{
			{Text: "🏙️ Toronto", CallbackData: "/setgrouptimezone_America/Toronto"},
			{Text: "🌮 Mexico City", CallbackData: "/setgrouptimezone_America/Mexico_City"},
		},
		{
			{Text: "🌴 São Paulo", CallbackData: "/setgrouptimezone_America/Sao_Paulo"},
			{Text: "🏔️ Buenos Aires", CallbackData: "/setgrouptimezone_America/Buenos_Aires"},
		},
		{
			{Text: "⬅️ Back", CallbackData: "/timezone_menu"},
		},
	}
	
	return &api.TgInlineKeyboardMarkup{
		InlineKeyboard: buttons,
	}
}

// createEuropeTimezoneKeyboard creates timezone selection for Europe
func createEuropeTimezoneKeyboard() *api.TgInlineKeyboardMarkup {
	buttons := [][]api.TgInlineKeyboardButton{
		{
			{Text: "🇬🇧 London", CallbackData: "/setgrouptimezone_Europe/London"},
			{Text: "🇫🇷 Paris", CallbackData: "/setgrouptimezone_Europe/Paris"},
		},
		{
			{Text: "🇩🇪 Berlin", CallbackData: "/setgrouptimezone_Europe/Berlin"},
			{Text: "🇮🇹 Rome", CallbackData: "/setgrouptimezone_Europe/Rome"},
		},
		{
			{Text: "🇪🇸 Madrid", CallbackData: "/setgrouptimezone_Europe/Madrid"},
			{Text: "🇳🇱 Amsterdam", CallbackData: "/setgrouptimezone_Europe/Amsterdam"},
		},
		{
			{Text: "🇷🇺 Moscow", CallbackData: "/setgrouptimezone_Europe/Moscow"},
			{Text: "🇺🇦 Kyiv", CallbackData: "/setgrouptimezone_Europe/Kyiv"},
		},
		{
			{Text: "⬅️ Back", CallbackData: "/timezone_menu"},
		},
	}
	
	return &api.TgInlineKeyboardMarkup{
		InlineKeyboard: buttons,
	}
}

// createAsiaTimezoneKeyboard creates timezone selection for Asia
func createAsiaTimezoneKeyboard() *api.TgInlineKeyboardMarkup {
	buttons := [][]api.TgInlineKeyboardButton{
		{
			{Text: "🇯🇵 Tokyo", CallbackData: "/setgrouptimezone_Asia/Tokyo"},
			{Text: "🇨🇳 Shanghai", CallbackData: "/setgrouptimezone_Asia/Shanghai"},
		},
		{
			{Text: "🇮🇳 Kolkata", CallbackData: "/setgrouptimezone_Asia/Kolkata"},
			{Text: "🇦🇪 Dubai", CallbackData: "/setgrouptimezone_Asia/Dubai"},
		},
		{
			{Text: "🇸🇬 Singapore", CallbackData: "/setgrouptimezone_Asia/Singapore"},
			{Text: "🇭🇰 Hong Kong", CallbackData: "/setgrouptimezone_Asia/Hong_Kong"},
		},
		{
			{Text: "🇰🇷 Seoul", CallbackData: "/setgrouptimezone_Asia/Seoul"},
			{Text: "🇮🇩 Jakarta", CallbackData: "/setgrouptimezone_Asia/Jakarta"},
		},
		{
			{Text: "⬅️ Back", CallbackData: "/timezone_menu"},
		},
	}
	
	return &api.TgInlineKeyboardMarkup{
		InlineKeyboard: buttons,
	}
}

// handleSetGroupLimitCallback handles group limit button presses
func handleSetGroupLimitCallback(callbackQuery *response.TgCallbackQuery) {
	// Extract limit value from callback data
	parts := strings.Split(callbackQuery.Data, "_")
	if len(parts) != 2 {
		return
	}
	
	limitStr := parts[1]
	
	// Handle custom limit
	if limitStr == "custom" {
		customMsg := payload.TgPlSendMessage{
			ChatId:    callbackQuery.Message.Chat.Id,
			ParseMode: payload.ParseModeHTML,
			Text:      "⚙️ <b>Set Custom Group Limit</b>\n\nTo set a custom limit, use the command:\n\n<code>/setgrouplimit [number]</code>\n\n<b>Examples:</b>\n• <code>/setgrouplimit 15</code> (15 items per day)\n• <code>/setgrouplimit 50</code> (50 items per day)\n• <code>/setgrouplimit 100</code> (100 items per day)",
		}
		message.NewSendMessage(customMsg)
		return
	}
	
	// Parse limit value
	limitValue, err := strconv.Atoi(limitStr)
	if err != nil {
		return
	}
	
	// Set the group limit
	err = services.SetGroupMediaLimit(callbackQuery.Message.Chat.Id, callbackQuery.Message.Chat.Title, limitValue)
	if err != nil {
		log.Printf("Error setting group limit: %v", err)
		return
	}
	
	// Send success message
	var successText string
	if limitValue == 0 {
		successText = "✅ <b>Group limit removed!</b>\n\n🔓 Users now have unlimited access to send media."
	} else {
		successText = fmt.Sprintf("✅ <b>Group limit updated!</b>\n\n🔒 New limit: %d media items per day\n📋 Applied to all users without individual limits", limitValue)
	}
	
	// Send success message with updated keyboard
	newMsg := payload.TgPlSendMessage{
		ChatId:      callbackQuery.Message.Chat.Id,
		ParseMode:   payload.ParseModeHTML,
		Text:        successText,
		ReplyMarkup: createGroupSettingsKeyboard(),
	}
	message.NewSendMessage(newMsg)
}

// handleTimezoneMenuCallback shows the timezone selection menu
func handleTimezoneMenuCallback(origChat *response.TgChat, origUser *response.TgUser) {
	timezoneMsg := payload.TgPlSendMessage{
		ChatId:      origChat.Id,
		ParseMode:   payload.ParseModeHTML,
		Text:        "🌍 <b>Select Timezone Region</b>\n\nChoose your region to set the group timezone:",
		ReplyMarkup: createTimezoneMenuKeyboard(),
	}
	
	message.NewSendMessage(timezoneMsg)
}

// handleTimezoneRegionCallback shows timezones for a specific region
func handleTimezoneRegionCallback(callbackQuery *response.TgCallbackQuery) {
	region := strings.TrimPrefix(callbackQuery.Data, "/tz_")
	
	var keyboard *api.TgInlineKeyboardMarkup
	var regionName string
	
	switch region {
	case "americas":
		keyboard = createAmericasTimezoneKeyboard()
		regionName = "Americas"
	case "europe":
		keyboard = createEuropeTimezoneKeyboard()
		regionName = "Europe"
	case "asia":
		keyboard = createAsiaTimezoneKeyboard()
		regionName = "Asia"
	case "australia":
		// Create Australia keyboard (simplified for now)
		keyboard = &api.TgInlineKeyboardMarkup{
			InlineKeyboard: [][]api.TgInlineKeyboardButton{
				{
					{Text: "🇦🇺 Sydney", CallbackData: "/setgrouptimezone_Australia/Sydney"},
					{Text: "🇦🇺 Melbourne", CallbackData: "/setgrouptimezone_Australia/Melbourne"},
				},
				{
					{Text: "🇦🇺 Perth", CallbackData: "/setgrouptimezone_Australia/Perth"},
					{Text: "🇦🇺 Brisbane", CallbackData: "/setgrouptimezone_Australia/Brisbane"},
				},
				{
					{Text: "⬅️ Back", CallbackData: "/timezone_menu"},
				},
			},
		}
		regionName = "Australia"
	case "africa":
		// Create Africa keyboard (simplified for now)
		keyboard = &api.TgInlineKeyboardMarkup{
			InlineKeyboard: [][]api.TgInlineKeyboardButton{
				{
					{Text: "🇿🇦 Johannesburg", CallbackData: "/setgrouptimezone_Africa/Johannesburg"},
					{Text: "🇪🇬 Cairo", CallbackData: "/setgrouptimezone_Africa/Cairo"},
				},
				{
					{Text: "🇳🇬 Lagos", CallbackData: "/setgrouptimezone_Africa/Lagos"},
					{Text: "🇰🇪 Nairobi", CallbackData: "/setgrouptimezone_Africa/Nairobi"},
				},
				{
					{Text: "⬅️ Back", CallbackData: "/timezone_menu"},
				},
			},
		}
		regionName = "Africa"
	default:
		return
	}
	
	regionMsg := payload.TgPlSendMessage{
		ChatId:      callbackQuery.Message.Chat.Id,
		ParseMode:   payload.ParseModeHTML,
		Text:        fmt.Sprintf("🌍 <b>%s Timezones</b>\n\nSelect your timezone:", regionName),
		ReplyMarkup: keyboard,
	}
	
	message.NewSendMessage(regionMsg)
}

// handleSetTimezoneCallback sets the group timezone
func handleSetTimezoneCallback(callbackQuery *response.TgCallbackQuery) {
	// Extract timezone from callback data
	timezone := strings.TrimPrefix(callbackQuery.Data, "/setgrouptimezone_")
	
	// Set the group timezone
	err := services.SetGroupTimezone(callbackQuery.Message.Chat.Id, callbackQuery.Message.Chat.Title, timezone)
	if err != nil {
		log.Printf("Error setting group timezone: %v", err)
		return
	}
	
	// Get current time in the new timezone
	loc := loadTimezoneWithFallback(timezone)
	currentTime := time.Now().In(loc)
	
	// Send success message
	successText := fmt.Sprintf("✅ <b>Timezone Updated!</b>\n\n🌍 New timezone: %s\n🕐 Current time: %s\n\n⏰ Daily limits will now reset at midnight in this timezone.",
		timezone,
		currentTime.Format("15:04:05 MST"))
	
	successMsg := payload.TgPlSendMessage{
		ChatId:      callbackQuery.Message.Chat.Id,
		ParseMode:   payload.ParseModeHTML,
		Text:        successText,
		ReplyMarkup: createGroupSettingsKeyboard(),
	}
	
	message.NewSendMessage(successMsg)
}

// answerCallbackQuery sends an answer to a callback query
func answerCallbackQuery(callbackQueryId string) {
	// For now, we'll just log this since we need to implement the answerCallbackQuery API method
	log.Printf("📱 Answering callback query: %s", callbackQueryId)
	// TODO: Implement message.NewAnswerCallbackQuery when the API method is available
}

// shouldEnforceLimit checks if a user has exceeded their daily media limit
func shouldEnforceLimit(userID int64, chatID int64, username string, messageID int) bool {
	// Get user's effective limit
	limit, _, err := services.GetEffectiveUserLimit(userID, chatID)
	if err != nil {
		log.Printf("Error getting user limit: %v", err)
		return false // Don't block on error
	}
	
	// No limit set - allow processing
	if limit == 0 {
		return false
	}
	
	// Check current usage
	dailyCount, err := services.GetDailyMediaCount(userID, chatID)
	if err != nil {
		log.Printf("Error getting daily media count: %v", err)
		return false // Don't block on error
	}
	
	// User has exceeded limit
	if dailyCount >= limit {
		log.Printf("🚫 User %d has exceeded daily limit (%d/%d)", userID, dailyCount, limit)
		
		// Delete the message that triggered the limit
		deletePayload := payload.TgPlDeleteMessage{
			ChatId:    chatID,
			MessageId: messageID,
		}
		message.NewDeleteMessage(deletePayload)
		
		// Get reset time information
		resetTime, timeUntilReset, err := services.GetTimezoneAwareResetTime(chatID)
		if err != nil {
			log.Printf("Error getting reset time: %v", err)
			timeUntilReset = 0
		}
		
		// Build notification message
		var notificationText strings.Builder
		notificationText.WriteString(fmt.Sprintf("🚫 @%s, you've reached your daily media limit!\n\n", username))
		notificationText.WriteString(fmt.Sprintf("📊 <b>Daily Limit:</b> %d media items\n", limit))
		notificationText.WriteString(fmt.Sprintf("🔴 <b>Status:</b> Limit reached (%d/%d)\n", dailyCount, limit))
		
		if timeUntilReset > 0 {
			notificationText.WriteString(fmt.Sprintf("⏰ <b>Resets in:</b> %s\n", formatDuration(timeUntilReset)))
			notificationText.WriteString(fmt.Sprintf("📅 <b>Reset time:</b> %s\n", resetTime.Format("15:04 MST")))
		}
		
		// Send notification
		notificationMsg := payload.TgPlSendMessage{
			ChatId:              chatID,
			ParseMode:           payload.ParseModeHTML,
			Text:                notificationText.String(),
			DisableNotification: true,
		}
		message.NewSendMessage(notificationMsg)
		
		return true // Block processing
	}
	
	return false // Allow processing
}

// trackMediaSent records that a media item was successfully sent
func trackMediaSent(userID int64, username string, chatID int64, chatTitle string, url string, mediaType string) {
	// Determine platform from URL
	platform := getPlatformFromURL(url)
	
	// Record the media stat
	err := services.RecordMediaStat(userID, username, chatID, chatTitle, platform, mediaType, url)
	if err != nil {
		log.Printf("Error recording media stat: %v", err)
	} else {
		log.Printf("📊 Recorded media stat: user=%d, platform=%s, type=%s", userID, platform, mediaType)
	}
}

// getPlatformFromURL determines the platform from a URL
func getPlatformFromURL(url string) string {
	if strings.Contains(url, "instagram.com") {
		return "instagram"
	} else if strings.Contains(url, "tiktok.com") {
		return "tiktok"
	} else if strings.Contains(url, "twitter.com") || strings.Contains(url, "x.com") {
		return "twitter"
	} else if strings.Contains(url, "youtube.com") || strings.Contains(url, "youtu.be") {
		return "youtube"
	} else if strings.Contains(url, "reddit.com") {
		return "reddit"
	} else if strings.Contains(url, "facebook.com") {
		return "facebook"
	} else {
		return "unknown"
	}
}

// Platform Settings Functions

// createPlatformSettingsKeyboard creates the interactive keyboard for platform settings
func createPlatformSettingsKeyboard(chatID int64) (*api.TgInlineKeyboardMarkup, error) {
	// Get current platform settings
	platformSettings, err := services.GetChatPlatformSettings(chatID)
	if err != nil {
		return nil, err
	}
	
	platforms := []struct {
		Key   string
		Name  string
		Emoji string
	}{
		{"instagram", "Instagram", "📷"},
		{"tiktok", "TikTok", "🎵"},
		{"twitter", "Twitter/X", "🐦"},
		{"youtube", "YouTube", "📺"},
		{"reddit", "Reddit", "🤖"},
		{"facebook", "Facebook", "📘"},
	}
	
	var buttons [][]api.TgInlineKeyboardButton
	
	// Create platform toggle buttons (2 per row)
	for i := 0; i < len(platforms); i += 2 {
		var row []api.TgInlineKeyboardButton
		
		for j := 0; j < 2 && i+j < len(platforms); j++ {
			platform := platforms[i+j]
			enabled := platformSettings[platform.Key]
			
			status := "❌"
			if enabled {
				status = "✅"
			}
			
			button := api.TgInlineKeyboardButton{
				Text:         fmt.Sprintf("%s %s %s", platform.Emoji, platform.Name, status),
				CallbackData: fmt.Sprintf("/toggle_platform_%s", platform.Key),
			}
			row = append(row, button)
		}
		buttons = append(buttons, row)
	}
	
	// Add control buttons
	buttons = append(buttons, []api.TgInlineKeyboardButton{
		{Text: "✅ Enable All", CallbackData: "/platform_enable_all"},
		{Text: "❌ Disable All", CallbackData: "/platform_disable_all"},
	})
	
	// Add back button
	buttons = append(buttons, []api.TgInlineKeyboardButton{
		{Text: "⬅️ Back to Group Settings", CallbackData: "/group_settings"},
	})
	
	return &api.TgInlineKeyboardMarkup{
		InlineKeyboard: buttons,
	}, nil
}

// handlePlatformSettingsCallback handles the platform settings menu
func handlePlatformSettingsCallback(origChat *response.TgChat, origUser *response.TgUser, messageId int) {
	log.Printf("📱 Admin %d opened platform settings in chat %d", origUser.Id, origChat.Id)
	
	// Initialize settings if not exist
	services.InitializeChatSettings(origChat.Id)
	
	// Get platform settings
	platformSettings, err := services.GetChatPlatformSettings(origChat.Id)
	if err != nil {
		log.Printf("Error getting platform settings: %v", err)
		return
	}
	
	// Count enabled/disabled platforms
	enabled, disabled := 0, 0
	for _, isEnabled := range platformSettings {
		if isEnabled {
			enabled++
		} else {
			disabled++
		}
	}
	
	// Build settings message
	var settingsText strings.Builder
	settingsText.WriteString("📱 <b>Platform Settings</b>\n")
	settingsText.WriteString(fmt.Sprintf("<i>Chat: %s</i>\n\n", html.EscapeString(origChat.Title)))
	settingsText.WriteString("🎯 <b>Configure which social media platforms the bot should process in this chat.</b>\n\n")
	settingsText.WriteString(fmt.Sprintf("📊 <b>Status:</b> %d enabled, %d disabled\n\n", enabled, disabled))
	settingsText.WriteString("💡 <i>Click platform buttons below to toggle on/off</i>")
	
	// Create keyboard
	keyboard, err := createPlatformSettingsKeyboard(origChat.Id)
	if err != nil {
		log.Printf("Error creating platform settings keyboard: %v", err)
		return
	}
	
	settingsMsg := payload.TgPlSendMessage{
		ChatId:      origChat.Id,
		ParseMode:   payload.ParseModeHTML,
		Text:        settingsText.String(),
		ReplyMarkup: keyboard,
	}
	
	message.NewSendMessage(settingsMsg)
}


// handleTogglePlatformCallback handles individual platform toggle
func handleTogglePlatformCallback(callbackQuery response.TgCallbackQuery) {
	// Extract platform from callback data
	platform := strings.TrimPrefix(callbackQuery.Data, "/toggle_platform_")
	chatID := callbackQuery.Message.Chat.Id
	userID := callbackQuery.From.Id
	
	log.Printf("📱 User %d toggling platform %s in chat %d", userID, platform, chatID)
	
	// Get current setting
	currentSetting := services.IsPlatformEnabledForChat(chatID, platform)
	newSetting := !currentSetting
	
	// Update platform setting
	err := services.SetChatPlatformEnabled(chatID, platform, newSetting)
	if err != nil {
		log.Printf("Error updating platform setting: %v", err)
		return
	}
	
	// Refresh the platform settings menu
	handlePlatformSettingsCallback(&callbackQuery.Message.Chat, &callbackQuery.From, callbackQuery.Message.MessageId)
}

// handlePlatformEnableAllCallback enables all platforms
func handlePlatformEnableAllCallback(callbackQuery response.TgCallbackQuery) {
	chatID := callbackQuery.Message.Chat.Id
	userID := callbackQuery.From.Id
	
	log.Printf("📱 User %d enabling all platforms in chat %d", userID, chatID)
	
	platforms := []string{"instagram", "tiktok", "twitter", "youtube", "reddit", "facebook"}
	
	for _, platform := range platforms {
		err := services.SetChatPlatformEnabled(chatID, platform, true)
		if err != nil {
			log.Printf("Error enabling platform %s: %v", platform, err)
		}
	}
	
	// Refresh the platform settings menu
	handlePlatformSettingsCallback(&callbackQuery.Message.Chat, &callbackQuery.From, callbackQuery.Message.MessageId)
}

// handlePlatformDisableAllCallback disables all platforms
func handlePlatformDisableAllCallback(callbackQuery response.TgCallbackQuery) {
	chatID := callbackQuery.Message.Chat.Id
	userID := callbackQuery.From.Id
	
	log.Printf("📱 User %d disabling all platforms in chat %d", userID, chatID)
	
	platforms := []string{"instagram", "tiktok", "twitter", "youtube", "reddit", "facebook"}
	
	for _, platform := range platforms {
		err := services.SetChatPlatformEnabled(chatID, platform, false)
		if err != nil {
			log.Printf("Error disabling platform %s: %v", platform, err)
		}
	}
	
	// Refresh the platform settings menu
	handlePlatformSettingsCallback(&callbackQuery.Message.Chat, &callbackQuery.From, callbackQuery.Message.MessageId)
}

// Description Settings Functions

// handleDescriptionSettingsCallback handles the description settings menu  
func handleDescriptionSettingsCallback(origChat *response.TgChat, origUser *response.TgUser, messageId int) {
	log.Printf("📝 User %d accessing description settings in chat %d", origUser.Id, origChat.Id)
	
	// Create keyboard
	keyboard, err := createDescriptionSettingsKeyboard(origChat.Id)
	if err != nil {
		log.Printf("Error creating description settings keyboard: %v", err)
		return
	}
	
	// Get current settings for message
	descriptionSettings, err := services.GetChatDescriptionSettings(origChat.Id)
	if err != nil {
		log.Printf("Error getting description settings: %v", err)
		descriptionSettings = make(map[string]bool)
	}
	
	// Count enabled/disabled descriptions
	enabled := 0
	disabled := 0
	platforms := []string{"instagram", "tiktok", "twitter", "youtube", "reddit", "facebook"}
	
	for _, platform := range platforms {
		if isEnabled, exists := descriptionSettings[platform]; !exists || isEnabled {
			enabled++ // Default to enabled if not set
		} else {
			disabled++
		}
	}
	
	// Build settings message
	var settingsText strings.Builder
	settingsText.WriteString("📝 <b>Description Settings</b>\n")
	settingsText.WriteString(fmt.Sprintf("<i>Chat: %s</i>\n\n", html.EscapeString(origChat.Title)))
	settingsText.WriteString("🎯 <b>Configure whether to show text descriptions/captions for each platform.</b>\n\n")
	settingsText.WriteString("💡 <b>Enabled:</b> Shows full descriptions with media\n")
	settingsText.WriteString("🔇 <b>Disabled:</b> Shows only media + author info\n\n")
	settingsText.WriteString(fmt.Sprintf("📊 <b>Status:</b> %d showing descriptions, %d minimal\n\n", enabled, disabled))
	settingsText.WriteString("💡 <i>Click platform buttons below to toggle description visibility</i>")
	
	// Create message payload  
	msgPayload := payload.TgPlSendMessage{
		ChatId:                origChat.Id,
		ParseMode:            payload.ParseModeHTML,
		Text:                 settingsText.String(),
		ReplyMarkup:          keyboard,
		DisableNotification: true,
	}
	
	// Send message
	message.NewSendMessage(msgPayload)
}

// createDescriptionSettingsKeyboard creates the keyboard for description settings
func createDescriptionSettingsKeyboard(chatID int64) (*api.TgInlineKeyboardMarkup, error) {
	// Get current description settings
	descriptionSettings, err := services.GetChatDescriptionSettings(chatID)
	if err != nil {
		log.Printf("Error getting description settings: %v", err)
		descriptionSettings = make(map[string]bool)
	}
	
	// Platform definitions with display names and emojis
	platforms := []struct {
		Key   string
		Name  string
		Emoji string
	}{
		{"instagram", "Instagram", "📸"},
		{"tiktok", "TikTok", "🎵"},
		{"twitter", "Twitter/X", "🐦"},
		{"youtube", "YouTube", "📺"},
		{"reddit", "Reddit", "🤖"},
		{"facebook", "Facebook", "📘"},
	}
	
	var rows [][]api.TgInlineKeyboardButton
	
	// Create platform toggle buttons (2 per row)
	for i := 0; i < len(platforms); i += 2 {
		var row []api.TgInlineKeyboardButton
		
		// First platform in row
		platform1 := platforms[i]
		enabled1 := true // Default to enabled
		if isEnabled, exists := descriptionSettings[platform1.Key]; exists {
			enabled1 = isEnabled
		}
		status1 := "✅"
		if !enabled1 {
			status1 = "🔇"
		}
		
		button1 := api.TgInlineKeyboardButton{
			Text:         fmt.Sprintf("%s %s %s", platform1.Emoji, platform1.Name, status1),
			CallbackData: "/toggle_description_" + platform1.Key,
		}
		row = append(row, button1)
		
		// Second platform in row (if exists)
		if i+1 < len(platforms) {
			platform2 := platforms[i+1]
			enabled2 := true // Default to enabled
			if isEnabled, exists := descriptionSettings[platform2.Key]; exists {
				enabled2 = isEnabled
			}
			status2 := "✅"
			if !enabled2 {
				status2 = "🔇"
			}
			
			button2 := api.TgInlineKeyboardButton{
				Text:         fmt.Sprintf("%s %s %s", platform2.Emoji, platform2.Name, status2),
				CallbackData: "/toggle_description_" + platform2.Key,
			}
			row = append(row, button2)
		}
		
		rows = append(rows, row)
	}
	
	// Add bulk action buttons
	rows = append(rows, []api.TgInlineKeyboardButton{
		{Text: "✅ Enable All Descriptions", CallbackData: "/description_enable_all"},
		{Text: "🔇 Disable All Descriptions", CallbackData: "/description_disable_all"},
	})
	
	// Add back button
	rows = append(rows, []api.TgInlineKeyboardButton{
		{Text: "🔙 Back to Settings", CallbackData: "/start"},
	})
	
	return &api.TgInlineKeyboardMarkup{
		InlineKeyboard: rows,
	}, nil
}

// handleToggleDescriptionCallback handles individual description toggle
func handleToggleDescriptionCallback(callbackQuery response.TgCallbackQuery) {
	// Extract platform from callback data
	platform := strings.TrimPrefix(callbackQuery.Data, "/toggle_description_")
	chatID := callbackQuery.Message.Chat.Id
	userID := callbackQuery.From.Id
	
	// Get current state
	currentState := services.IsDescriptionEnabledForChat(chatID, platform)
	newState := !currentState
	
	// Toggle setting
	err := services.SetChatDescriptionEnabled(chatID, platform, newState)
	if err != nil {
		log.Printf("Error toggling description for platform %s: %v", platform, err)
		return
	}
	
	action := "enabled"
	if !newState {
		action = "disabled"
	}
	log.Printf("📝 User %d %s descriptions for %s in chat %d", userID, action, platform, chatID)
	
	// Refresh the description settings menu
	handleDescriptionSettingsCallback(&callbackQuery.Message.Chat, &callbackQuery.From, callbackQuery.Message.MessageId)
}

// handleDescriptionEnableAllCallback enables descriptions for all platforms
func handleDescriptionEnableAllCallback(callbackQuery response.TgCallbackQuery) {
	chatID := callbackQuery.Message.Chat.Id
	userID := callbackQuery.From.Id
	
	log.Printf("📝 User %d enabling descriptions for all platforms in chat %d", userID, chatID)
	
	platforms := []string{"instagram", "tiktok", "twitter", "youtube", "reddit", "facebook"}
	
	for _, platform := range platforms {
		err := services.SetChatDescriptionEnabled(chatID, platform, true)
		if err != nil {
			log.Printf("Error enabling descriptions for platform %s: %v", platform, err)
		}
	}
	
	// Refresh the description settings menu
	handleDescriptionSettingsCallback(&callbackQuery.Message.Chat, &callbackQuery.From, callbackQuery.Message.MessageId)
}

// handleDescriptionDisableAllCallback disables descriptions for all platforms
func handleDescriptionDisableAllCallback(callbackQuery response.TgCallbackQuery) {
	chatID := callbackQuery.Message.Chat.Id
	userID := callbackQuery.From.Id
	
	log.Printf("📝 User %d disabling descriptions for all platforms in chat %d", userID, chatID)
	
	platforms := []string{"instagram", "tiktok", "twitter", "youtube", "reddit", "facebook"}

	for _, platform := range platforms {
		err := services.SetChatDescriptionEnabled(chatID, platform, false)
		if err != nil {
			log.Printf("Error disabling descriptions for platform %s: %v", platform, err)
		}
	}

	// Refresh the description settings menu
	handleDescriptionSettingsCallback(&callbackQuery.Message.Chat, &callbackQuery.From, callbackQuery.Message.MessageId)
}

// guessChatAction picks the appropriate Telegram chat action based on the URL
func guessChatAction(url string) string {
	// Photo URLs
	if strings.Contains(url, "/p/") || strings.Contains(url, "/photo/") {
		return payload.ChatActionUploadPhoto
	}

	// Reddit: could be text, image, or video — use typing since we don't know yet
	if strings.Contains(url, "reddit.com") {
		return payload.ChatActionTyping
	}

	// YouTube is always video
	if strings.Contains(url, "youtube.com") || strings.Contains(url, "youtu.be") {
		return payload.ChatActionUploadVideo
	}

	// TikTok is mostly video
	if strings.Contains(url, "tiktok.com") {
		return payload.ChatActionUploadVideo
	}

	// Instagram stories/reels are video
	if strings.Contains(url, "/reel/") || strings.Contains(url, "/reels/") || strings.Contains(url, "/stories/") {
		return payload.ChatActionUploadVideo
	}

	// Default to upload_video for other platforms
	return payload.ChatActionUploadVideo
}

