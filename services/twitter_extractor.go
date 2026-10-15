package services

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type TwitterExtractor struct {
	client     *http.Client
	tempDir    string
	workingDir string
	// Cache extracted tweet text from successful methods to use in fallbacks
	cachedTweetText string
}

func NewTwitterExtractor(workingDir, tempDir string) *TwitterExtractor {
	return &TwitterExtractor{
		client:     SharedDownloadClient,
		tempDir:    tempDir,
		workingDir: workingDir,
	}
}

func (te *TwitterExtractor) ExtractTweet(twitterURL string) ([]*MediaInfo, error) {
	log.Printf("🐦 Starting custom Twitter extraction for: %s", twitterURL)

	// Extract tweet ID and username from URL
	tweetID, username, err := te.extractTweetDetails(twitterURL)
	if err != nil {
		return nil, fmt.Errorf("failed to extract tweet details: %v", err)
	}

	log.Printf("🔍 Extracted tweet ID: %s, username: %s", tweetID, username)

	// Try multiple extraction methods
	// Prioritize FxTwitter API first - it has full text for long tweets
	methods := []func(string, string) ([]*MediaInfo, error){
		te.extractViaFxTwitterAPI,
		te.extractViaSyndicationAPI,
		te.extractViaEmbedAPI,
		te.extractViaWebInterface,
		te.extractViaDirectScraping,
		te.extractViaNitterProxy,
		te.extractViaFixupxProxy,
		te.extractViaSimpleTextFallback,
	}

	for i, method := range methods {
		log.Printf("📡 Trying extraction method %d/%d", i+1, len(methods))
		mediaInfos, err := method(tweetID, username)
		if err == nil && len(mediaInfos) > 0 && te.isValidExtraction(mediaInfos) {
			log.Printf("✅ Success with method %d: found %d items", i+1, len(mediaInfos))
			return mediaInfos, nil
		}
		log.Printf("❌ Method %d failed: %v", i+1, err)
	}

	return nil, fmt.Errorf("all extraction methods failed")
}

// isValidExtraction checks if the extraction result is meaningful
func (te *TwitterExtractor) isValidExtraction(mediaInfos []*MediaInfo) bool {
	if len(mediaInfos) == 0 {
		return false
	}

	// If we have actual media files, it's always valid
	for _, info := range mediaInfos {
		if info.Type != "text" && info.Filename != "" {
			return true
		}
	}

	// For text-only results, check if the content is meaningful
	for _, info := range mediaInfos {
		if info.Type == "text" {
			text := strings.TrimSpace(info.Caption)

			// Reject if text is too short or just domain names
			if len(text) < 10 {
				log.Printf("🚫 Rejecting extraction: text too short (%d chars): %s", len(text), text)
				return false
			}

			// Reject if text is just domain names or error messages
			lowercaseText := strings.ToLower(text)
			badTexts := []string{
				"nitter.it", "nitter.net", "twitter.com", "x.com",
				"fixupx.com", "not found", "error", "failed",
				"access denied", "rate limit", "unavailable",
				"private to you", "suspended account", "tweet was deleted",
			}

			for _, badText := range badTexts {
				if strings.Contains(lowercaseText, badText) && len(text) < 50 {
					log.Printf("🚫 Rejecting extraction: contains bad text '%s': %s", badText, text)
					return false
				}
			}

			// If we get here, it's meaningful text content
			return true
		}
	}

	return false
}

func (te *TwitterExtractor) extractTweetDetails(twitterURL string) (string, string, error) {
	// Extract tweet ID and username from various Twitter URL formats
	patterns := []string{
		`twitter\.com/(\w+)/status/(\d+)`,
		`x\.com/(\w+)/status/(\d+)`,
		`mobile\.twitter\.com/(\w+)/status/(\d+)`,
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindStringSubmatch(twitterURL)
		if len(matches) > 2 {
			username := matches[1]
			tweetID := matches[2]
			return tweetID, username, nil
		}
	}

	return "", "", fmt.Errorf("no tweet details found in URL: %s", twitterURL)
}

func (te *TwitterExtractor) extractViaWebInterface(tweetID string, username string) ([]*MediaInfo, error) {
	log.Printf("🌐 Trying web interface extraction for tweet %s by @%s", tweetID, username)

	// Construct tweet URL
	tweetURL := fmt.Sprintf("https://x.com/i/web/status/%s", tweetID)

	// Create request
	req, err := http.NewRequest("GET", tweetURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}

	// Add headers to mimic a real browser
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	req.Header.Set("DNT", "1")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Upgrade-Insecure-Requests", "1")

	// Make request
	resp, err := te.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch tweet: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	// Handle compressed response
	var reader io.Reader = resp.Body
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gzipReader, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to create gzip reader: %v", err)
		}
		defer gzipReader.Close()
		reader = gzipReader
	}

	// Read response
	body, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %v", err)
	}

	// Debug: Log response info
	bodyStr := string(body)
	log.Printf("🔍 Response length: %d bytes", len(bodyStr))
	if len(bodyStr) > 1000 {
		log.Printf("🔍 Response preview: %s...", bodyStr[:1000])
	} else {
		log.Printf("🔍 Full response: %s", bodyStr)
	}

	// Log specific search results for debugging
	if strings.Contains(bodyStr, "pbs.twimg.com") {
		log.Printf("🖼️ Found pbs.twimg.com references in HTML")
	}
	if strings.Contains(bodyStr, "media_url") {
		log.Printf("📸 Found media_url references in HTML")
	}
	if strings.Contains(bodyStr, "tweetText") {
		log.Printf("📝 Found tweetText references in HTML")
	}

	// Extract author info from HTML
	displayName, extractedUsername := te.extractAuthorInfoFromHTML(bodyStr)
	if extractedUsername != "" {
		username = extractedUsername // Update username if we found a better one
	}

	// Parse HTML and extract media URLs with author info
	return te.parseHTMLForMediaWithAuthor(bodyStr, tweetID, username, displayName)
}

func (te *TwitterExtractor) extractViaEmbedAPI(tweetID string, username string) ([]*MediaInfo, error) {
	log.Printf("🔗 Trying embed API extraction for tweet %s by @%s", tweetID, username)

	// Use Twitter's oembed API - note: using omit_script to get cleaner HTML
	embedURL := fmt.Sprintf("https://publish.twitter.com/oembed?url=https://x.com/%s/status/%s&omit_script=true", username, tweetID)

	req, err := http.NewRequest("GET", embedURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create embed request: %v", err)
	}

	// Add user agent
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	resp, err := te.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch embed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("embed API HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read embed response: %v", err)
	}

	log.Printf("✅ Got embed response: %d bytes", len(body))

	// Parse embed response
	return te.parseEmbedResponse(string(body), tweetID)
}

func (te *TwitterExtractor) extractUsernameFromEmbedData(embedData map[string]interface{}) (string, error) {
	// Try to extract username from author_url
	if authorURL, ok := embedData["author_url"].(string); ok {
		// Extract username from URL like "https://twitter.com/politvidchannel"
		parts := strings.Split(authorURL, "/")
		if len(parts) > 0 {
			return parts[len(parts)-1], nil
		}
	}

	// Try author_name as fallback
	if authorName, ok := embedData["author_name"].(string); ok {
		return strings.ToLower(strings.ReplaceAll(authorName, " ", "")), nil
	}

	return "", fmt.Errorf("no username found in embed data")
}

// formatTweetCaption formats tweet caption with author info at the top
func (te *TwitterExtractor) formatTweetCaption(authorName, authorUsername, tweetText string) string {
	// Filter out invalid/placeholder tweet texts
	invalidTexts := []string{
		"Private to you",
		"This Tweet is from a suspended account",
		"This Tweet was deleted",
		"This Tweet is unavailable",
		"Age-restricted adult content",
	}
	for _, invalid := range invalidTexts {
		if tweetText == invalid || strings.Contains(tweetText, invalid) {
			tweetText = ""
			break
		}
	}

	// Start with author info
	caption := ""

	if authorName != "" && authorUsername != "" {
		// Both name and username available
		caption = fmt.Sprintf("👤 %s (@%s)\n\n", authorName, authorUsername)
	} else if authorUsername != "" {
		// Only username available
		caption = fmt.Sprintf("👤 @%s\n\n", authorUsername)
	} else if authorName != "" {
		// Only name available
		caption = fmt.Sprintf("👤 %s\n\n", authorName)
	}

	// Add tweet text
	caption += tweetText

	return strings.TrimSpace(caption)
}

// extractAuthorInfoFromHTML extracts author display name and username from Twitter HTML
func (te *TwitterExtractor) extractAuthorInfoFromHTML(html string) (displayName, username string) {
	// Pattern to extract display name from Twitter's HTML structure
	// Looking for patterns like: <span...>Display Name</span>
	displayNamePatterns := []string{
		// Modern Twitter/X structure using data-testid="User-Name"
		`data-testid="User-Name"[^>]*>.*?<span[^>]*>([^<]+)</span>.*?<span[^>]*>@([^<]+)</span>`,
		// Alternative order: username first, then display name
		`data-testid="User-Name"[^>]*>.*?<span[^>]*>@([^<]+)</span>.*?<span[^>]*>([^<]+)</span>`,
		// Modern Twitter/X structure
		`<a href="/([^"]+)"[^>]*>.*?<span[^>]*>([^<]+)</span>`,
		// Alternative structure with nested spans
		`<a href="/([^"]+)"[^>]*>.*?<span[^>]*><span[^>]*>([^<]+)</span></span>`,
		// Profile header pattern
		`"screen_name":"([^"]+)".*?"name":"([^"]+)"`,
		// Meta tag patterns
		`<meta[^>]*name="twitter:title"[^>]*content="([^"]+) \(@([^)]+)\)`,
		`<meta[^>]*property="og:title"[^>]*content="([^"]+) on X"`,
	}

	// Try each pattern
	for _, pattern := range displayNamePatterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindStringSubmatch(html)

		if len(matches) >= 3 {
			// Extract based on pattern match
			if strings.Contains(pattern, "screen_name") {
				// JSON pattern: matches[1] is username, matches[2] is display name
				username = matches[1]
				displayName = matches[2]
			} else if strings.Contains(pattern, "twitter:title") {
				// Meta tag pattern: matches[1] is display name, matches[2] is username
				displayName = matches[1]
				username = matches[2]
			} else if strings.Contains(pattern, "User-Name") && strings.Contains(pattern, "@([^<]+).*?([^<]+)") {
				// data-testid pattern with username first: matches[1] is username, matches[2] is display name
				username = matches[1]
				displayName = matches[2]
			} else if strings.Contains(pattern, "User-Name") && strings.Contains(pattern, "([^<]+).*?@([^<]+)") {
				// data-testid pattern with display name first: matches[1] is display name, matches[2] is username
				displayName = matches[1]
				username = matches[2]
			} else {
				// Link pattern: matches[1] is username, matches[2] is display name
				username = matches[1]
				displayName = matches[2]
			}

			// Clean up the display name
			displayName = strings.TrimSpace(displayName)
			username = strings.TrimSpace(username)

			// Skip if we got HTML entities or too short
			if len(displayName) > 1 && !strings.Contains(displayName, "&") {
				log.Printf("✅ Extracted author info from HTML: %s (@%s)", displayName, username)
				return displayName, username
			}
		}
	}

	// Fallback: try to extract just the username from profile links
	// Use more specific patterns to avoid false matches
	fallbackPatterns := []string{
		// Look for Twitter profile links with specific context
		`<a[^>]*href="/([^"/]+)"[^>]*>[^<]*@([^<]+)</a>`,
		// Match @username patterns in spans with specific CSS classes (2025 Twitter structure)
		`<span class="[^"]*r-poiln3[^"]*">@([a-zA-Z0-9_]+)</span>`,
		`<span[^>]*class="[^"]*poiln3[^"]*"[^>]*>@([a-zA-Z0-9_]+)</span>`,
		// Match @username patterns in spans (generic)
		`<span[^>]*>@([a-zA-Z0-9_]+)</span>`,
		// Look for specific href patterns that should contain the actual username
		`href="/([a-zA-Z0-9_]+)"[^>]*role="link"`,
		// Only use very generic pattern as last resort, but filter out common false positives
		`href="/([a-zA-Z][a-zA-Z0-9_]+)"`,
	}

	for _, pattern := range fallbackPatterns {
		re := regexp.MustCompile(pattern)
		if matches := re.FindStringSubmatch(html); len(matches) > 1 {
			candidate := matches[1]
			if len(matches) > 2 {
				candidate = matches[2] // Use the second match if it exists (usually the @username part)
			}

			// Skip common false positives
			if candidate != "i" && candidate != "search" && candidate != "home" &&
				candidate != "notifications" && candidate != "messages" &&
				candidate != "compose" && candidate != "settings" && candidate != "help" &&
				candidate != "tos" && candidate != "privacy" && candidate != "os-x.xml" &&
				len(candidate) > 2 && len(candidate) < 20 {
				username = candidate
				log.Printf("⚠️ Found username via fallback pattern: @%s", username)
				break
			}
		}
	}

	return displayName, username
}

func (te *TwitterExtractor) extractViaFxTwitterAPI(tweetID string, username string) ([]*MediaInfo, error) {
	log.Printf("🌐 Trying FxTwitter API extraction for tweet %s", tweetID)

	// FxTwitter API provides full text for long tweets
	apiURL := fmt.Sprintf("https://api.fxtwitter.com/status/%s", tweetID)

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create fxtwitter request: %v", err)
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; TelegramBot/1.0)")

	resp, err := te.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch fxtwitter: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("fxtwitter API HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read fxtwitter response: %v", err)
	}

	// Parse JSON response
	var fxData map[string]interface{}
	if err := json.Unmarshal(body, &fxData); err != nil {
		return nil, fmt.Errorf("failed to parse fxtwitter JSON: %v", err)
	}

	tweet, ok := fxData["tweet"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("no tweet data in fxtwitter response")
	}

	var mediaInfos []*MediaInfo

	// Extract full tweet text
	tweetText := ""
	if text, ok := tweet["text"].(string); ok {
		tweetText = text
		log.Printf("📝 Got FULL tweet text from FxTwitter: %d chars", len(tweetText))
	}

	// Extract author information
	authorName := ""
	authorUsername := username
	if author, ok := tweet["author"].(map[string]interface{}); ok {
		if name, ok := author["name"].(string); ok {
			authorName = name
		}
		if screenName, ok := author["screen_name"].(string); ok {
			authorUsername = screenName
		}
	}

	// Extract media
	if media, ok := tweet["media"].(map[string]interface{}); ok {
		// Check for photos
		if photos, ok := media["photos"].([]interface{}); ok && len(photos) > 0 {
			log.Printf("📸 Found %d photos in FxTwitter response", len(photos))
			for i, photo := range photos {
				if photoMap, ok := photo.(map[string]interface{}); ok {
					if url, ok := photoMap["url"].(string); ok {
						mediaInfo, err := te.downloadMedia(url, tweetID, i)
						if err != nil {
							log.Printf("⚠️ Failed to download photo %d: %v", i, err)
							continue
						}
						mediaInfo.Caption = te.formatTweetCaption(authorName, authorUsername, tweetText)
						mediaInfos = append(mediaInfos, mediaInfo)
					}
				}
			}
		}

		// Check for videos
		if videos, ok := media["videos"].([]interface{}); ok && len(videos) > 0 {
			log.Printf("🎬 Found %d videos in FxTwitter response", len(videos))
			for i, video := range videos {
				if videoMap, ok := video.(map[string]interface{}); ok {
					if url, ok := videoMap["url"].(string); ok {
						mediaInfo, err := te.downloadMedia(url, tweetID, i)
						if err != nil {
							log.Printf("⚠️ Failed to download video %d: %v", i, err)
							continue
						}
						mediaInfo.Caption = te.formatTweetCaption(authorName, authorUsername, tweetText)
						mediaInfos = append(mediaInfos, mediaInfo)
					}
				}
			}
		}
	}

	// If we found media, return it
	if len(mediaInfos) > 0 {
		return mediaInfos, nil
	}

	// Text-only tweet
	if tweetText != "" {
		textMedia := &MediaInfo{
			Type:     "text",
			URL:      fmt.Sprintf("https://x.com/i/web/status/%s", tweetID),
			Caption:  te.formatTweetCaption(authorName, authorUsername, tweetText),
			Filename: "",
		}
		return []*MediaInfo{textMedia}, nil
	}

	return nil, fmt.Errorf("no content found in fxtwitter response")
}

func (te *TwitterExtractor) extractViaSyndicationAPI(tweetID string, username string) ([]*MediaInfo, error) {
	log.Printf("🔄 Trying syndication API extraction for tweet %s by @%s", tweetID, username)

	// Twitter's syndication API - sometimes works without auth
	syndicationURL := fmt.Sprintf("https://cdn.syndication.twimg.com/tweet-result?id=%s&lang=en", tweetID)

	req, err := http.NewRequest("GET", syndicationURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create syndication request: %v", err)
	}

	// Add headers
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Referer", "https://platform.twitter.com/")

	resp, err := te.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch syndication: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("syndication API HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read syndication response: %v", err)
	}

	// Parse JSON response
	var synData map[string]interface{}
	if err := json.Unmarshal(body, &synData); err != nil {
		return nil, fmt.Errorf("failed to parse syndication JSON: %v", err)
	}

	var mediaInfos []*MediaInfo

	// Extract tweet text - check note_tweet first for long tweets
	tweetText := ""

	// Check for note_tweet (contains full text for long tweets)
	if noteTweet, ok := synData["note_tweet"].(map[string]interface{}); ok {
		if noteText, ok := noteTweet["text"].(string); ok && noteText != "" {
			tweetText = noteText
			log.Printf("📝 Got FULL tweet text from note_tweet: %d chars", len(tweetText))
		}
	}

	// Fallback to regular text field if note_tweet not available
	if tweetText == "" {
		if text, ok := synData["text"].(string); ok {
			tweetText = text
			log.Printf("📝 Got tweet text from syndication: %s", tweetText)
		}
	}

	// Extract author information from syndication data
	authorName := ""
	authorUsername := username // Use passed username as default

	// Try to get user info from syndication data
	if user, ok := synData["user"].(map[string]interface{}); ok {
		if name, ok := user["name"].(string); ok {
			authorName = name
			log.Printf("📝 Got display name from syndication: %s", authorName)
		}
		if screenName, ok := user["screen_name"].(string); ok {
			authorUsername = screenName
			log.Printf("📝 Got username from syndication: @%s", authorUsername)
		}
	}

	// Alternative: Check for user_name field (sometimes present)
	if authorName == "" {
		if userName, ok := synData["user_name"].(string); ok {
			authorName = userName
			log.Printf("📝 Got display name from user_name field: %s", authorName)
		}
	}

	// Extract photos
	if photos, ok := synData["photos"].([]interface{}); ok {
		log.Printf("📸 Found %d photos in syndication data", len(photos))
		for i, photo := range photos {
			if photoMap, ok := photo.(map[string]interface{}); ok {
				if url, ok := photoMap["url"].(string); ok {
					mediaInfo, err := te.downloadMedia(url, tweetID, i)
					if err != nil {
						log.Printf("⚠️ Failed to download photo %d: %v", i, err)
						continue
					}
					// Format caption with author info
					mediaInfo.Caption = te.formatTweetCaption(authorName, authorUsername, tweetText)
					mediaInfos = append(mediaInfos, mediaInfo)
				}
			}
		}
	}

	// Extract video
	if _, ok := synData["video"].(map[string]interface{}); ok {
		log.Printf("🎥 Found video in syndication data")
		// TODO: Extract video URL from video object
	}

	// If no media but have text, return text
	if len(mediaInfos) == 0 && tweetText != "" {
		textMedia := &MediaInfo{
			Type:     "text",
			URL:      fmt.Sprintf("https://x.com/i/web/status/%s", tweetID),
			Caption:  te.formatTweetCaption(authorName, authorUsername, tweetText),
			Filename: "",
		}
		mediaInfos = append(mediaInfos, textMedia)
	}

	return mediaInfos, nil
}

func (te *TwitterExtractor) extractViaDirectScraping(tweetID string, username string) ([]*MediaInfo, error) {
	log.Printf("🔧 Trying direct scraping for tweet %s by @%s", tweetID, username)

	// Try to get tweet via mobile interface (sometimes more permissive)
	mobileURL := fmt.Sprintf("https://mobile.x.com/i/web/status/%s", tweetID)

	req, err := http.NewRequest("GET", mobileURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create mobile request: %v", err)
	}

	// Mobile user agent
	req.Header.Set("User-Agent", "Mozilla/5.0 (iPhone; CPU iPhone OS 15_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/15.0 Mobile/15E148 Safari/604.1")
	req.Header.Set("Accept-Encoding", "gzip, deflate")

	resp, err := te.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch mobile tweet: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("mobile HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	// Handle compressed response
	var reader io.Reader = resp.Body
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gzipReader, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to create mobile gzip reader: %v", err)
		}
		defer gzipReader.Close()
		reader = gzipReader
	}

	body, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to read mobile response: %v", err)
	}

	bodyStr := string(body)

	// Extract author info from mobile HTML
	displayName, extractedUsername := te.extractAuthorInfoFromHTML(bodyStr)
	if extractedUsername != "" {
		username = extractedUsername
	}

	return te.parseHTMLForMediaWithAuthor(bodyStr, tweetID, username, displayName)
}

func (te *TwitterExtractor) extractViaNitterProxy(tweetID string, username string) ([]*MediaInfo, error) {
	log.Printf("🔄 Trying Nitter-style extraction for tweet %s by @%s", tweetID, username)

	// Try some alternative URL patterns that might be more accessible
	urls := []string{
		fmt.Sprintf("https://nitter.net/%s/status/%s", username, tweetID),
		fmt.Sprintf("https://nitter.it/%s/status/%s", username, tweetID),
		fmt.Sprintf("https://mobile.twitter.com/%s/status/%s", username, tweetID),
	}

	for _, url := range urls {
		log.Printf("🔗 Trying alternative URL: %s", url)

		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			continue
		}

		req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; TwitterBot/1.0)")

		resp, err := te.client.Do(req)
		if err != nil {
			log.Printf("❌ Failed to fetch %s: %v", url, err)
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			log.Printf("❌ HTTP %d for %s", resp.StatusCode, url)
			continue
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			continue
		}

		bodyStr := string(body)
		if len(bodyStr) > 100 {
			log.Printf("✅ Got response from %s, parsing...", url)

			// Extract author info from alternative interface HTML
			displayName, extractedUsername := te.extractAuthorInfoFromHTML(bodyStr)
			if extractedUsername != "" {
				username = extractedUsername
			}

			result, err := te.parseHTMLForMediaWithAuthor(bodyStr, tweetID, username, displayName)
			if err == nil && len(result) > 0 {
				return result, nil
			}
		}
	}

	return nil, fmt.Errorf("no accessible alternative URLs found")
}

func (te *TwitterExtractor) extractViaFixupxProxy(tweetID string, username string) ([]*MediaInfo, error) {
	log.Printf("🔧 Trying fixupx.com extraction for tweet %s by @%s", tweetID, username)

	// Build fixupx URL
	fixupxURL := fmt.Sprintf("https://fixupx.com/%s/status/%s", username, tweetID)

	// Create request
	req, err := http.NewRequest("GET", fixupxURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create fixupx request: %v", err)
	}

	// Use minimal headers to get the compact response like curl
	req.Header.Set("User-Agent", "curl/7.68.0")
	req.Header.Set("Accept", "*/*")

	// Make request
	resp, err := te.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch fixupx page: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("fixupx HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	// Handle compressed response
	var reader io.Reader = resp.Body
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gzipReader, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to create gzip reader: %v", err)
		}
		defer gzipReader.Close()
		reader = gzipReader
	}

	// Read response
	body, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to read fixupx response: %v", err)
	}

	bodyStr := string(body)
	log.Printf("🔍 Fixupx response length: %d bytes", len(bodyStr))

	// Extract author info from fixupx HTML (they often include it in meta tags)
	displayName := ""

	// Try to extract display name from meta tags
	titlePattern := regexp.MustCompile(`<meta[^>]*property="og:title"[^>]*content="([^"]+)"`)
	if matches := titlePattern.FindStringSubmatch(bodyStr); len(matches) > 1 {
		// Fixupx format is often "Display Name (@username): tweet text"
		title := matches[1]
		if idx := strings.Index(title, " (@"); idx > 0 {
			displayName = title[:idx]
			log.Printf("📝 Extracted display name from fixupx title: %s", displayName)
		}
	}

	// Parse fixupx HTML for direct media URLs from meta tags (more reliable)
	return te.parseFixupxMetaTagsWithAuthor(bodyStr, tweetID, username, displayName)
}

func (te *TwitterExtractor) extractViaSimpleTextFallback(tweetID string, username string) ([]*MediaInfo, error) {
	log.Printf("📝 Trying simple text fallback for tweet %s by @%s", tweetID, username)

	// As a last resort, just return a message with the fixed link
	fixedURL := fmt.Sprintf("https://fixupx.com/%s/status/%s", username, tweetID)

	textMediaInfo := &MediaInfo{
		Type:     "text",
		URL:      fmt.Sprintf("https://x.com/i/web/status/%s", tweetID),
		Caption:  fmt.Sprintf("🐦 Twitter content (images may be available): %s", fixedURL),
		Filename: "",
	}

	return []*MediaInfo{textMediaInfo}, nil
}

func (te *TwitterExtractor) parseHTMLForMediaWithAuthor(html, tweetID, username, displayName string) ([]*MediaInfo, error) {
	var mediaInfos []*MediaInfo

	// Extract tweet text
	tweetText := te.extractTweetText(html)
	log.Printf("📝 Extracted tweet text: %.200s", tweetText)

	// Extract media URLs using regex patterns
	mediaURLs := te.extractMediaURLs(html)
	log.Printf("📸 Extracted %d media URLs: %v", len(mediaURLs), mediaURLs)

	log.Printf("📊 Found %d media URLs and tweet text: %.100s", len(mediaURLs), tweetText)

	// Download each media item
	for i, mediaURL := range mediaURLs {
		mediaInfo, err := te.downloadMedia(mediaURL, tweetID, i)
		if err != nil {
			log.Printf("⚠️ Failed to download media %d: %v", i, err)
			continue
		}
		// Format caption with author info
		mediaInfo.Caption = te.formatTweetCaption(displayName, username, tweetText)
		mediaInfos = append(mediaInfos, mediaInfo)
	}

	// If no media found but we have text, return text-only
	if len(mediaInfos) == 0 && tweetText != "" {
		log.Printf("📝 No media found, returning text content")
		textMedia := &MediaInfo{
			Type:     "text",
			URL:      fmt.Sprintf("https://x.com/i/web/status/%s", tweetID),
			Caption:  te.formatTweetCaption(displayName, username, tweetText),
			Filename: "",
		}
		mediaInfos = append(mediaInfos, textMedia)
	}

	return mediaInfos, nil
}

func (te *TwitterExtractor) parseEmbedResponse(response, tweetID string) ([]*MediaInfo, error) {
	// Parse JSON response from embed API
	var embedData map[string]interface{}
	if err := json.Unmarshal([]byte(response), &embedData); err != nil {
		return nil, fmt.Errorf("failed to parse embed JSON: %v", err)
	}

	// Extract HTML from embed response
	html, ok := embedData["html"].(string)
	if !ok {
		return nil, fmt.Errorf("no HTML in embed response")
	}

	// Extract author information
	authorName := ""
	authorUsername := ""
	if name, ok := embedData["author_name"].(string); ok {
		authorName = name
	}
	if username, err := te.extractUsernameFromEmbedData(embedData); err == nil {
		authorUsername = username
	}

	// Extract tweet text from the HTML
	tweetText := ""
	rawTweetText := ""
	textPattern := regexp.MustCompile(`<p[^>]*lang="[^"]*"[^>]*dir="[^"]*"[^>]*>(.*?)</p>`)
	if matches := textPattern.FindStringSubmatch(html); len(matches) > 1 {
		rawTweetText = matches[1]
		// Use the proper cleaning function that removes pic.twitter.com links
		tweetText = te.cleanAndFormatTweetText(rawTweetText)
	}

	log.Printf("📝 Extracted tweet text from embed: %s", tweetText)

	// Look for quoted tweets in the embed HTML and resolve them
	quotedTweetText, quotedTweetMedia := te.extractAndResolveQuotedTweets(html, tweetText)
	if quotedTweetText != "" {
		// Merge main tweet text with quoted tweet content
		tweetText = te.mergeMainAndQuotedContent(tweetText, quotedTweetText)
		log.Printf("📝 Merged with quoted tweet content: %s", tweetText)
	}

	// Extract actual media URLs from embed HTML
	var mediaInfos []*MediaInfo

	// Add quoted tweet media first (if any)
	for _, quotedMedia := range quotedTweetMedia {
		mediaInfos = append(mediaInfos, quotedMedia)
	}

	// Try to extract actual pbs.twimg.com URLs from the embed HTML
	mediaURLs := te.extractMediaURLs(html)
	log.Printf("📸 Found %d media URLs in embed HTML: %v", len(mediaURLs), mediaURLs)

	// Download each media item
	for i, mediaURL := range mediaURLs {
		mediaInfo, err := te.downloadMedia(mediaURL, tweetID, i)
		if err != nil {
			log.Printf("⚠️ Failed to download media %d: %v", i, err)
			continue
		}

		// Format caption with author info
		mediaInfo.Caption = te.formatTweetCaption(authorName, authorUsername, tweetText)

		mediaInfos = append(mediaInfos, mediaInfo)
	}

	// If we found actual media, return it
	if len(mediaInfos) > 0 {
		return mediaInfos, nil
	}

	// If no media found and no text, return error
	if tweetText == "" {
		return nil, fmt.Errorf("no media or text found in embed")
	}

	// Check if the original raw text contained pic.twitter.com references (indicating photos exist)
	picPattern := regexp.MustCompile(`pic\.twitter\.com/([a-zA-Z0-9]+)`)
	picMatches := picPattern.FindAllStringSubmatch(rawTweetText, -1)
	hasPhotoReferences := len(picMatches) > 0

	// Return an error if photos exist but we couldn't extract them from embed
	// This will allow the extraction chain to try other methods (including fixupx)
	// But cache the tweet text for use in subsequent methods
	if hasPhotoReferences {
		log.Printf("🖼️ Found %d pic.twitter.com references but no direct media URLs in embed", len(picMatches))

		// Cache the good tweet text for use in subsequent methods
		if tweetText != "" && tweetText != "Private to you" {
			te.cachedTweetText = tweetText
			log.Printf("💾 Cached tweet text from embed: %s", tweetText)
		}

		return nil, fmt.Errorf("photos detected but not extractable from embed API")
	}

	// Text-only tweet - return formatted text with author info
	textMedia := &MediaInfo{
		Type:     "text",
		URL:      fmt.Sprintf("https://x.com/i/web/status/%s", tweetID),
		Caption:  te.formatTweetCaption(authorName, authorUsername, tweetText),
		Filename: "",
	}
	mediaInfos = append(mediaInfos, textMedia)

	return mediaInfos, nil
}

// extractAndResolveQuotedTweets finds quoted tweet URLs and resolves their content
func (te *TwitterExtractor) extractAndResolveQuotedTweets(html, mainText string) (string, []*MediaInfo) {
	// Look for quoted tweet URLs in the HTML and text
	quotedURLs := te.findQuotedTweetURLs(html, mainText)
	if len(quotedURLs) == 0 {
		return "", nil
	}

	log.Printf("🔍 Found %d quoted tweet URLs to resolve", len(quotedURLs))

	// Resolve the first quoted tweet (avoid complexity of multiple quotes)
	quotedURL := quotedURLs[0]
	quotedText, quotedMedia, err := te.resolveQuotedTweetContent(quotedURL)
	if err != nil {
		log.Printf("❌ Failed to resolve quoted tweet %s: %v", quotedURL, err)
		return "", nil
	}

	return quotedText, quotedMedia
}

// findQuotedTweetURLs extracts quoted tweet URLs from HTML and text
func (te *TwitterExtractor) findQuotedTweetURLs(html, text string) []string {
	var urls []string

	// Patterns to find quoted tweet URLs
	urlPatterns := []string{
		// Direct Twitter URLs in text
		`https://(?:twitter\.com|x\.com)/[^/\s]+/status/(\d+)`,
		// From HTML href attributes
		`href="(https://(?:twitter\.com|x\.com)/[^"]+/status/\d+)"`,
		// From data attributes
		`data-tweet-url="(https://[^"]+)"`,
		// From embed data
		`"url":"(https://(?:twitter\.com|x\.com)/[^"]+/status/\d+)"`,
	}

	seen := make(map[string]bool)

	for _, pattern := range urlPatterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindAllStringSubmatch(html+" "+text, -1)
		for _, match := range matches {
			if len(match) > 1 {
				url := match[1]
				if !seen[url] {
					urls = append(urls, url)
					seen[url] = true
				}
			}
		}
	}

	return urls
}

// resolveQuotedTweetContent extracts content from a quoted tweet URL using embed API
func (te *TwitterExtractor) resolveQuotedTweetContent(quotedURL string) (string, []*MediaInfo, error) {
	log.Printf("🔄 Resolving quoted tweet content from: %s", quotedURL)

	// Extract tweet ID and username from the quoted URL
	tweetID, username, err := te.extractTweetDetails(quotedURL)
	if err != nil {
		return "", nil, fmt.Errorf("failed to extract quoted tweet details: %v", err)
	}

	// Use embed API to get quoted tweet content (simpler than full extraction)
	quotedText, quotedMedia, err := te.extractQuotedTweetViaEmbed(tweetID, username)
	if err != nil {
		log.Printf("❌ Failed to extract quoted tweet via embed: %v", err)
		return "", nil, err
	}

	log.Printf("✅ Resolved quoted tweet: %d chars text, %d media items", len(quotedText), len(quotedMedia))
	return quotedText, quotedMedia, nil
}

// extractQuotedTweetViaEmbed uses embed API specifically for quoted tweets
func (te *TwitterExtractor) extractQuotedTweetViaEmbed(tweetID, username string) (string, []*MediaInfo, error) {
	// Use Twitter's oembed API for the quoted tweet
	embedURL := fmt.Sprintf("https://publish.twitter.com/oembed?url=https://x.com/%s/status/%s&omit_script=true", username, tweetID)

	req, err := http.NewRequest("GET", embedURL, nil)
	if err != nil {
		return "", nil, fmt.Errorf("failed to create embed request: %v", err)
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	resp, err := te.client.Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("failed to fetch embed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", nil, fmt.Errorf("embed API returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, fmt.Errorf("failed to read embed response: %v", err)
	}

	// Parse JSON response
	var embedResp struct {
		HTML string `json:"html"`
	}

	if err := json.Unmarshal(body, &embedResp); err != nil {
		return "", nil, fmt.Errorf("failed to parse embed JSON: %v", err)
	}

	html := embedResp.HTML

	// Extract text from quoted tweet embed
	quotedText := ""
	textPattern := regexp.MustCompile(`<p[^>]*lang="[^"]*"[^>]*dir="[^"]*"[^>]*>(.*?)</p>`)
	if matches := textPattern.FindStringSubmatch(html); len(matches) > 1 {
		quotedText = te.cleanAndFormatTweetText(matches[1])
	}

	// Extract media URLs from quoted tweet
	var quotedMedia []*MediaInfo
	mediaURLs := te.extractMediaURLs(html)

	for i, mediaURL := range mediaURLs {
		mediaInfo, err := te.downloadMedia(mediaURL, tweetID, i)
		if err != nil {
			log.Printf("⚠️ Failed to download quoted tweet media %d: %v", i, err)
			continue
		}

		// Mark as quoted tweet media
		mediaInfo.Caption = "📄 From quoted tweet: " + quotedText
		quotedMedia = append(quotedMedia, mediaInfo)
	}

	return quotedText, quotedMedia, nil
}

// mergeMainAndQuotedContent combines main tweet and quoted tweet text properly
func (te *TwitterExtractor) mergeMainAndQuotedContent(mainText, quotedText string) string {
	if mainText == "" {
		return quotedText
	}
	if quotedText == "" {
		return mainText
	}

	// Clean up any existing quoted tweet placeholders in main text
	mainText = strings.ReplaceAll(mainText, "📄 [Contains quoted/linked tweet]", "")
	mainText = strings.TrimSpace(mainText)

	// Format with clear separation
	return mainText + "\n\n📄 Quoted Tweet:\n" + quotedText
}

// separateQuotedTweet identifies and formats quoted/retweeted content separately
func (te *TwitterExtractor) separateQuotedTweet(text string) string {
	// Patterns for identifying quoted tweets in various formats
	quotedTweetPatterns := []struct {
		pattern     string
		replacement string
	}{
		// HTML structure patterns for quoted tweets
		{
			pattern:     `<div[^>]*class="[^"]*QuoteTweet[^"]*"[^>]*>(.*?)</div>`,
			replacement: "\n\n📄 Quoted Tweet:\n$1",
		},
		{
			pattern:     `<div[^>]*class="[^"]*quote[^"]*"[^>]*>(.*?)</div>`,
			replacement: "\n\n📄 Quoted Tweet:\n$1",
		},
		{
			pattern:     `<blockquote[^>]*>(.*?)</blockquote>`,
			replacement: "\n\n📄 Quoted Tweet:\n$1",
		},
		// Twitter API JSON patterns for quoted tweets
		{
			pattern:     `"quoted_status"[^}]*"full_text"\s*:\s*"([^"]*)"`,
			replacement: "\n\n📄 Quoted Tweet:\n$1",
		},
		{
			pattern:     `"retweeted_status"[^}]*"full_text"\s*:\s*"([^"]*)"`,
			replacement: "\n\n🔄 Retweeted:\n$1",
		},
		// Modern Twitter structure patterns
		{
			pattern:     `<div[^>]*data-testid="quoteTweet"[^>]*>(.*?)</div>`,
			replacement: "\n\n📄 Quoted Tweet:\n$1",
		},
		{
			pattern:     `<article[^>]*data-testid="tweet"[^>]*>.*?<article[^>]*data-testid="tweet"[^>]*>(.*?)</article>.*?</article>`,
			replacement: "$1\n\n📄 Quoted Tweet:\n$2", // This would need more complex handling
		},
	}

	// Apply patterns to separate quoted content
	for _, pattern := range quotedTweetPatterns {
		re := regexp.MustCompile(`(?s)` + pattern.pattern)
		if re.MatchString(text) {
			text = re.ReplaceAllString(text, pattern.replacement)
			break // Only apply the first matching pattern
		}
	}

	// Handle common quote tweet indicators in plain text
	// Look for patterns like "RT @username:" or "QT @username:"
	rtPattern := regexp.MustCompile(`(?m)^(RT @[^:]+:)\s*(.*)`)
	text = rtPattern.ReplaceAllString(text, "\n🔄 Retweeted ($1)\n$2")

	qtPattern := regexp.MustCompile(`(?m)^(QT @[^:]+:)\s*(.*)`)
	text = qtPattern.ReplaceAllString(text, "\n📄 Quoted Tweet ($1)\n$2")

	// Handle embedded tweet URLs that indicate quotes/retweets
	embedUrlPattern := regexp.MustCompile(`https://t\.co/[a-zA-Z0-9]+`)
	embedMatches := embedUrlPattern.FindAllString(text, -1)
	if len(embedMatches) > 0 {
		// If we find t.co links, they might be quoted tweets
		for _, embedUrl := range embedMatches {
			// Replace t.co links with a note about quoted content
			text = strings.ReplaceAll(text, embedUrl, "\n\n📄 [Contains quoted/linked tweet]")
		}
	}

	// Handle specific patterns for embedded quote tweet URLs from Twitter
	twitterUrlPattern := regexp.MustCompile(`https://(?:twitter\.com|x\.com)/[^/]+/status/\d+`)
	twitterMatches := twitterUrlPattern.FindAllString(text, -1)
	if len(twitterMatches) > 0 {
		// If we find actual Twitter URLs embedded in the text, mark them as quoted tweets
		for _, twitterUrl := range twitterMatches {
			text = strings.ReplaceAll(text, twitterUrl, "\n\n📄 Quoted Tweet: "+twitterUrl)
		}
	}

	// Detect and format conversation-style content (like the translation example)
	// This handles cases where there's a main tweet content followed by dialogue/conversation
	if strings.Contains(text, ":") && strings.Contains(text, "\n") {
		// Check if this looks like a conversation format
		lines := strings.Split(text, "\n")
		var mainContent []string
		var conversationContent []string
		var inConversation bool

		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				if inConversation {
					conversationContent = append(conversationContent, "")
				} else {
					mainContent = append(mainContent, "")
				}
				continue
			}

			// Detect conversation patterns (Speaker: text format)
			if regexp.MustCompile(`^[A-Za-z\s]+:\s`).MatchString(line) ||
				regexp.MustCompile(`^A fan:\s`).MatchString(line) ||
				regexp.MustCompile(`^Faye:\s`).MatchString(line) ||
				strings.HasPrefix(line, "Starts at minute") {
				inConversation = true
			}

			if inConversation {
				conversationContent = append(conversationContent, line)
			} else {
				mainContent = append(mainContent, line)
			}
		}

		// If we detected conversation content, format it properly
		if len(conversationContent) > 0 && len(mainContent) > 0 {
			mainText := strings.Join(mainContent, "\n")
			conversationText := strings.Join(conversationContent, "\n")

			if strings.TrimSpace(mainText) != "" && strings.TrimSpace(conversationText) != "" {
				text = strings.TrimSpace(mainText) + "\n\n📄 Referenced content:\n" + strings.TrimSpace(conversationText)
			}
		}
	}

	// Handle hashtags that might indicate the original content source
	// This helps separate attribution from main content
	hashtagPattern := regexp.MustCompile(`\n\s*(#[A-Za-z0-9_]+)\s*$`)
	if match := hashtagPattern.FindStringSubmatch(text); len(match) > 1 {
		// Move hashtag to separate line with context
		hashtag := match[1]
		text = hashtagPattern.ReplaceAllString(text, "")
		text = strings.TrimSpace(text) + "\n\n📌 " + hashtag
	}

	return text
}

// cleanAndFormatTweetText cleans HTML and preserves proper formatting
func (te *TwitterExtractor) cleanAndFormatTweetText(rawText string) string {
	text := rawText

	// First, try to separate main tweet from quoted tweet
	text = te.separateQuotedTweet(text)

	// Preserve line breaks from various HTML elements
	text = strings.ReplaceAll(text, "<br>", "\n")
	text = strings.ReplaceAll(text, "<br/>", "\n")
	text = strings.ReplaceAll(text, "<br />", "\n")
	text = strings.ReplaceAll(text, "</div><div", "</div>\n<div")
	text = strings.ReplaceAll(text, "</p>", "</p>\n")
	text = strings.ReplaceAll(text, "</span></div>", "</span>\n</div>")

	// Handle Twitter-specific elements
	text = regexp.MustCompile(`<div[^>]*class="[^"]*css-175oi2r[^"]*"[^>]*>`).ReplaceAllString(text, "\n")

	// Clean up links and mentions - put them on new lines
	text = regexp.MustCompile(`</span><div[^>]*><span[^>]*><a[^>]*href="/([^"]*)"[^>]*>@([^<]*)</a></span></div><span[^>]*>`).ReplaceAllString(text, "\n@$2\n")

	// Remove all remaining HTML tags
	text = regexp.MustCompile(`<[^>]*>`).ReplaceAllString(text, "")

	// Decode JSON escape sequences
	text = strings.ReplaceAll(text, `\"`, `"`)
	text = strings.ReplaceAll(text, `\\n`, "\n")
	text = strings.ReplaceAll(text, `\\t`, "\t")
	text = strings.ReplaceAll(text, `\\r`, "\r")

	// Decode HTML entities
	text = strings.ReplaceAll(text, "&amp;", "&")
	text = strings.ReplaceAll(text, "&lt;", "<")
	text = strings.ReplaceAll(text, "&gt;", ">")
	text = strings.ReplaceAll(text, "&quot;", `"`)
	text = strings.ReplaceAll(text, "&#39;", "'")

	// Remove pic.twitter.com links
	picTwitterPattern := regexp.MustCompile(`\s*pic\.twitter\.com/[a-zA-Z0-9]+\s*`)
	text = picTwitterPattern.ReplaceAllString(text, "")

	// Clean up multiple consecutive newlines but preserve intentional spacing
	text = regexp.MustCompile(`\n{3,}`).ReplaceAllString(text, "\n\n")

	// Clean up spaces but preserve line structure and formatting
	lines := strings.Split(text, "\n")
	var cleanedLines []string

	for i, line := range lines {
		cleanedLine := strings.TrimSpace(line)

		// Always include non-empty lines
		if cleanedLine != "" {
			cleanedLines = append(cleanedLines, cleanedLine)
		} else {
			// For empty lines, preserve them if they're not at the start/end
			// and if they're not consecutive (avoid multiple empty lines)
			if i > 0 && i < len(lines)-1 {
				// Only add empty line if the previous added line wasn't empty
				if len(cleanedLines) > 0 && cleanedLines[len(cleanedLines)-1] != "" {
					cleanedLines = append(cleanedLines, "")
				}
			}
		}
	}

	text = strings.Join(cleanedLines, "\n")
	text = strings.TrimSpace(text)

	return text
}

func (te *TwitterExtractor) extractTweetText(html string) string {
	// Multiple patterns to extract tweet text - updated for Nitter and modern Twitter
	patterns := []string{
		// Nitter-specific patterns (high priority since it worked)
		`<div[^>]*class="[^"]*tweet-content[^"]*"[^>]*>(.*?)</div>`,
		`<div[^>]*class="[^"]*media-body[^"]*"[^>]*>(.*?)</div>`,
		`<p[^>]*class="[^"]*tweet-content[^"]*"[^>]*>(.*?)</p>`,
		`<div[^>]*class="[^"]*timeline-tweet[^"]*"[^>]*>.*?<div[^>]*class="[^"]*tweet-text[^"]*"[^>]*>(.*?)</div>`,

		// Modern Twitter data-testid patterns (2025 X/Twitter structure)
		// More flexible pattern that captures everything between opening and closing div
		`(?s)<div[^>]*data-testid="tweetText"[^>]*>(.*?)</div>`,
		// Match the exact structure with nested spans
		`<div[^>]*data-testid="tweetText"[^>]*>((?:<span[^>]*>[^<]*</span>[\s]*)+)</div>`,
		// With specific class pattern
		`<div[^>]*data-testid="tweetText"[^>]*class="[^"]*css-146c3p1[^"]*"[^>]*>(.*?)</div>`,
		`<div[^>]*class="[^"]*css-146c3p1[^"]*"[^>]*data-testid="tweetText"[^>]*>(.*?)</div>`,
		// Simple span within tweetText div
		`data-testid="tweetText"[^>]*><span[^>]*>([^<]+)</span>`,
		`<span[^>]*data-testid="tweetText"[^>]*>(.*?)</span>`,

		// Classic patterns
		`<div[^>]*class="[^"]*tweet-text[^"]*"[^>]*>(.*?)</div>`,
		`<p[^>]*class="[^"]*TweetTextSize[^"]*"[^>]*>(.*?)</p>`,

		// JSON embedded text (most common in modern Twitter)
		`"full_text"\s*:\s*"([^"]*)"`,
		`"text"\s*:\s*"([^"]*)"`,
		`"note_tweet"\s*:[^}]*"text"\s*:\s*"([^"]*)"`,

		// OpenGraph and meta tags
		`<meta[^>]*property="og:description"[^>]*content="([^"]*)"`,
		`<meta[^>]*name="description"[^>]*content="([^"]*)"`,
		`<title[^>]*>([^<]*)</title>`,

		// Script tag JSON data
		`window\.__INITIAL_STATE__[^}]*"text"\s*:\s*"([^"]*)"`,
		`"legacy"[^}]*"full_text"\s*:\s*"([^"]*)"`,

		// Tweet URL patterns in title/description
		`"([^"]*)" / X`,
		`"([^"]*)" on X:`,

		// Additional Nitter patterns
		`<div[^>]*class="[^"]*quote-text[^"]*"[^>]*>(.*?)</div>`,
		`<span[^>]*class="[^"]*tweet-text[^"]*"[^>]*>(.*?)</span>`,
	}

	// Check if we have the data-testid="tweetText" in HTML
	if strings.Contains(html, `data-testid="tweetText"`) {
		log.Printf("✅ Found data-testid=\"tweetText\" in HTML")

		// Try to extract a sample
		if idx := strings.Index(html, `data-testid="tweetText"`); idx >= 0 {
			endIdx := idx + 500
			if endIdx > len(html) {
				endIdx = len(html)
			}
			sample := html[idx:endIdx]
			log.Printf("📄 Sample around tweetText: %.300s", sample)
		}
	} else {
		log.Printf("❌ No data-testid=\"tweetText\" found in HTML")
	}

	for i, pattern := range patterns {
		re := regexp.MustCompile(`(?s)` + pattern) // (?s) makes . match newlines
		matches := re.FindStringSubmatch(html)
		if len(matches) > 1 {
			log.Printf("🎯 Pattern %d matched! Raw content: %.100s", i+1, matches[1])
			// Clean and format the text properly
			text := te.cleanAndFormatTweetText(matches[1])
			if text != "" && text != "null" {
				// Check if we have cached text from embed API that's better than generic "Private to you"
				if te.cachedTweetText != "" && (text == "Private to you" || strings.Contains(text, "Private to you")) {
					log.Printf("✅ Using cached embed text instead of generic pattern result: %s", te.cachedTweetText)
					return te.cachedTweetText
				}
				log.Printf("✅ Extracted tweet text via pattern %d: %.200s", i+1, text)
				return text
			}
		}
	}

	log.Printf("⚠️ No tweet text patterns matched")

	// Use cached tweet text from embed API if available and we didn't find anything better
	if te.cachedTweetText != "" {
		log.Printf("✅ Using cached tweet text from embed API: %s", te.cachedTweetText)
		return te.cachedTweetText
	}

	return ""
}

func (te *TwitterExtractor) extractMediaURLs(html string) []string {
	var urls []string

	// Patterns to match image and video URLs - updated for Nitter and modern Twitter
	patterns := []string{
		// Modern X/Twitter patterns (2025) - highest priority
		`<img[^>]*class="[^"]*css-9pa8cd[^"]*"[^>]*src="([^"]*pbs\.twimg\.com[^"]*)"`,
		`<img[^>]*src="([^"]*pbs\.twimg\.com[^"]*)"[^>]*class="[^"]*css-9pa8cd[^"]*"`,
		`<img[^>]*draggable="true"[^>]*src="([^"]*pbs\.twimg\.com[^"]*)"`,
		`<img[^>]*alt="Image"[^>]*src="([^"]*pbs\.twimg\.com[^"]*)"`,

		// Nitter-specific patterns (high priority)
		`<img[^>]*src="([^"]*nitter[^"]*pic[^"]*\.jpg[^"]*)"`,
		`<img[^>]*src="([^"]*nitter[^"]*pic[^"]*\.png[^"]*)"`,
		`<img[^>]*src="([^"]*nitter[^"]*pic[^"]*\.gif[^"]*)"`,
		`<img[^>]*src="([^"]*nitter[^"]*pic[^"]*\.webp[^"]*)"`,
		`<video[^>]*src="([^"]*nitter[^"]*vid[^"]*\.mp4[^"]*)"`,
		`<source[^>]*src="([^"]*nitter[^"]*vid[^"]*\.mp4[^"]*)"`,

		// Nitter proxy URLs for Twitter media
		`https://[^/]*nitter[^/]*/pic/media%2F[A-Za-z0-9_%-]+`,
		`https://[^/]*nitter[^/]*/pic/orig/media%2F[A-Za-z0-9_%-]+`,
		`/pic/media%2F[A-Za-z0-9_%-]+`,
		`/pic/orig/media%2F[A-Za-z0-9_%-]+`,

		// Standard Twitter media URLs
		`https://pbs\.twimg\.com/media/[A-Za-z0-9_-]+\?format=(?:jpg|jpeg|png|gif|webp)`,
		`https://pbs\.twimg\.com/media/[A-Za-z0-9_-]+\.(?:jpg|jpeg|png|gif|webp)`,
		`https://video\.twimg\.com/[^"'\s]+\.(?:mp4|m3u8)`,
		`https://pbs\.twimg\.com/tweet_video/[^"'\s]+\.(?:mp4|gif)`,

		// JSON embedded media URLs (most common in modern Twitter)
		`"media_url_https":"(https://pbs\.twimg\.com/media/[^"]+)"`,
		`"video_info"[^}]*"url":"([^"]*\.mp4[^"]*)"`,

		// JavaScript variable assignments
		`media_url_https['"]\s*:\s*['"]([^'"]*pbs\.twimg\.com[^'"]*)['"]*`,

		// Data attributes and modern patterns
		`data-image-url=["']([^"']*pbs\.twimg\.com[^"']*)["']`,
		`"(?:src|data-src|srcset)"\s*:\s*"(https://pbs\.twimg\.com/[^"]+)"`,

		// Script tag content with media
		`pbs\.twimg\.com/media/[A-Za-z0-9_-]+[^"'\s]*`,

		// CSS background images
		`background-image\s*:\s*url\s*\(\s*['"](https://pbs\.twimg\.com/[^'"]+)['"]\s*\)`,

		// Additional flexible patterns
		`https://pbs\.twimg\.com/media/[A-Za-z0-9_-]+`,
		`"(https://pbs\.twimg\.com/media/[A-Za-z0-9_%-]+)"`,

		// Generic image patterns in Nitter
		`<img[^>]*src="([^"]*\.(jpg|jpeg|png|gif|webp)[^"]*)"`,
		`src="([^"]*pic[^"]*\.(jpg|jpeg|png|gif|webp)[^"]*)"`,
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		if strings.Contains(pattern, "(") {
			// Pattern has capture group
			matches := re.FindAllStringSubmatch(html, -1)
			for _, match := range matches {
				if len(match) > 1 {
					urls = append(urls, match[1])
				}
			}
		} else {
			// Pattern matches full URL
			matches := re.FindAllString(html, -1)
			urls = append(urls, matches...)
		}
	}

	// Clean up and add format params for images without extensions
	var cleanURLs []string
	for _, url := range urls {
		// Convert Nitter proxy URLs to direct Twitter URLs if possible
		if strings.Contains(url, "nitter") && strings.Contains(url, "/pic/") {
			// Extract media ID from Nitter URL and convert to Twitter URL
			if strings.Contains(url, "media%2F") {
				re := regexp.MustCompile(`media%2F([A-Za-z0-9_-]+)`)
				matches := re.FindStringSubmatch(url)
				if len(matches) > 1 {
					mediaID := matches[1]
					url = fmt.Sprintf("https://pbs.twimg.com/media/%s?format=jpg&name=large", mediaID)
				}
			}
		}

		// Add format parameter if missing for pbs.twimg.com URLs
		if strings.Contains(url, "pbs.twimg.com/media/") && !strings.Contains(url, "format=") && !strings.Contains(url, ".jpg") && !strings.Contains(url, ".png") {
			if strings.Contains(url, "?") {
				url += "&format=jpg&name=large"
			} else {
				url += "?format=jpg&name=large"
			}
		}
		cleanURLs = append(cleanURLs, url)
	}

	// Remove duplicates based on media ID, not full URL
	// This prevents duplicates from URL variations like:
	// - https://pbs.twimg.com/media/ABC123?format=jpg&name=small
	// - https://pbs.twimg.com/media/ABC123?format=jpg&name=large
	seenMediaIDs := make(map[string]string) // mediaID -> best URL
	for _, url := range cleanURLs {
		mediaID := te.extractMediaID(url)
		if mediaID == "" {
			// For non-Twitter URLs (videos, etc.), use full URL as key
			mediaID = url
		}

		if existingURL, exists := seenMediaIDs[mediaID]; exists {
			// Keep the URL with higher quality (prefer orig > large > medium > small)
			if te.getURLQualityScore(url) > te.getURLQualityScore(existingURL) {
				seenMediaIDs[mediaID] = url
			}
		} else {
			seenMediaIDs[mediaID] = url
		}
	}

	// Collect unique URLs preserving order from original list
	seen := make(map[string]bool)
	var uniqueURLs []string
	for _, url := range cleanURLs {
		mediaID := te.extractMediaID(url)
		if mediaID == "" {
			mediaID = url
		}
		if !seen[mediaID] {
			seen[mediaID] = true
			uniqueURLs = append(uniqueURLs, seenMediaIDs[mediaID])
		}
	}

	return uniqueURLs
}

// extractMediaID extracts the media identifier from a Twitter image URL
func (te *TwitterExtractor) extractMediaID(url string) string {
	// Pattern: https://pbs.twimg.com/media/ABC123...
	re := regexp.MustCompile(`pbs\.twimg\.com/media/([A-Za-z0-9_-]+)`)
	matches := re.FindStringSubmatch(url)
	if len(matches) > 1 {
		return matches[1]
	}

	return ""
}

// getURLQualityScore returns a quality score for URL (higher is better)
func (te *TwitterExtractor) getURLQualityScore(url string) int {
	if strings.Contains(url, "name=orig") {
		return 4
	}
	if strings.Contains(url, "name=large") {
		return 3
	}
	if strings.Contains(url, "name=medium") {
		return 2
	}
	if strings.Contains(url, "name=small") {
		return 1
	}

	return 0 // No quality parameter
}

func (te *TwitterExtractor) downloadMedia(mediaURL, tweetID string, index int) (*MediaInfo, error) {
	log.Printf("⬇️ Downloading media: %s", mediaURL)

	// Create request
	req, err := http.NewRequest("GET", mediaURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create download request: %v", err)
	}

	// Add headers
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Referer", "https://x.com/")

	// Download
	resp, err := te.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to download: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("download HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	// Determine file extension
	ext := te.getFileExtension(mediaURL, resp.Header.Get("Content-Type"))

	// Create filename
	timestamp := time.Now().Unix()
	filename := filepath.Join(te.tempDir, fmt.Sprintf("%d_twitter_%s_%d%s", timestamp, tweetID, index, ext))

	// Create file
	file, err := os.Create(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to create file: %v", err)
	}
	defer file.Close()

	// Copy data
	_, err = io.Copy(file, resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to save file: %v", err)
	}

	// Determine media type
	mediaType := "document"
	if strings.Contains(ext, ".jpg") || strings.Contains(ext, ".jpeg") || strings.Contains(ext, ".png") || strings.Contains(ext, ".gif") || strings.Contains(ext, ".webp") {
		mediaType = "photo"
	} else if strings.Contains(ext, ".mp4") || strings.Contains(ext, ".mov") || strings.Contains(ext, ".avi") {
		mediaType = "video"
	}

	log.Printf("✅ Downloaded %s: %s", mediaType, filename)

	return &MediaInfo{
		Type:     mediaType,
		URL:      fmt.Sprintf("https://x.com/i/web/status/%s", tweetID),
		Caption:  "",
		Filename: filename,
	}, nil
}

func (te *TwitterExtractor) getFileExtension(url, contentType string) string {
	// Handle mosaic.fxtwitter.com URLs specially
	if strings.Contains(url, "mosaic.fxtwitter.com") {
		if strings.Contains(url, "/jpeg/") {
			return ".jpg"
		}
		if strings.Contains(url, "/png/") {
			return ".png"
		}
		if strings.Contains(url, "/gif/") {
			return ".gif"
		}
		if strings.Contains(url, "/webp/") {
			return ".webp"
		}
	}

	// Strip query params before getting extension
	cleanURL := url
	if idx := strings.Index(url, "?"); idx != -1 {
		cleanURL = url[:idx]
	}

	// Try to get extension from URL
	if ext := filepath.Ext(cleanURL); ext != "" {
		return ext
	}

	// Fall back to content type
	switch contentType {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "video/mp4":
		return ".mp4"
	default:
		return ".bin"
	}
}

func (te *TwitterExtractor) parseFixupxMetaTagsWithAuthor(html string, tweetID string, username string, displayName string) ([]*MediaInfo, error) {
	var mediaInfos []*MediaInfo

	log.Printf("📝 Parsing fixupx meta tags for tweet %s by @%s", tweetID, username)

	// Log response size for debugging
	log.Printf("🔍 Fixupx response length: %d bytes", len(html))

	// Extract tweet text from og:description meta tag
	tweetText := ""
	descPattern := regexp.MustCompile(`<meta[^>]*property="og:description"[^>]*content="([^"]*)"`)
	if matches := descPattern.FindStringSubmatch(html); len(matches) > 1 {
		tweetText = matches[1]
		// Decode HTML entities
		tweetText = strings.ReplaceAll(tweetText, "&amp;", "&")
		tweetText = strings.ReplaceAll(tweetText, "&lt;", "<")
		tweetText = strings.ReplaceAll(tweetText, "&gt;", ">")
		tweetText = strings.ReplaceAll(tweetText, "&quot;", "\"")
		tweetText = strings.ReplaceAll(tweetText, "&#39;", "'")
		tweetText = strings.TrimSpace(tweetText)
	}

	log.Printf("📝 Extracted tweet text from fixupx: %s", tweetText)

	// Check if fixupx has the media URLs
	if !strings.Contains(html, "pbs.twimg.com") && !strings.Contains(html, "mosaic.fxtwitter.com") {
		log.Printf("❌ No pbs.twimg.com or mosaic.fxtwitter.com found in fixupx HTML")
	}

	// Extract media URLs from og:image and twitter:image meta tags
	var mediaURLs []string

	// Look for og:image and twitter:image meta tags - handle both compact and full response formats
	imagePatterns := []string{
		// Fixupx mosaic.fxtwitter.com patterns (2025 format)
		`<meta[^>]*property="og:image"[^>]*content="([^"]*mosaic\.fxtwitter\.com[^"]*)"`,
		`<meta[^>]*property="twitter:image"[^>]*content="([^"]*mosaic\.fxtwitter\.com[^"]*)"`,
		`<meta[^>]*name="twitter:image"[^>]*content="([^"]*mosaic\.fxtwitter\.com[^"]*)"`,
		`<meta property="og:image" content="([^"]*mosaic\.fxtwitter\.com[^"]*)"`,
		`<meta property="twitter:image" content="([^"]*mosaic\.fxtwitter\.com[^"]*)"`,
		`<meta name="twitter:image" content="([^"]*mosaic\.fxtwitter\.com[^"]*)"`,
		// Standard pbs.twimg.com format
		`<meta[^>]*property="og:image"[^>]*content="([^"]*pbs\.twimg\.com[^"]*)"`,
		`<meta[^>]*property="twitter:image"[^>]*content="([^"]*pbs\.twimg\.com[^"]*)"`,
		`<meta[^>]*name="twitter:image"[^>]*content="([^"]*pbs\.twimg\.com[^"]*)"`,
		// Compact format (without spaces)
		`<meta property="og:image" content="([^"]*pbs\.twimg\.com[^"]*)"`,
		`<meta property="twitter:image" content="([^"]*pbs\.twimg\.com[^"]*)"`,
		`<meta name="twitter:image" content="([^"]*pbs\.twimg\.com[^"]*)"`,
		// Handle different quote styles
		`property="og:image"[^>]*content="(https://pbs\.twimg\.com[^"]*)"`,
		`property="twitter:image"[^>]*content="(https://pbs\.twimg\.com[^"]*)"`,
	}

	for _, pattern := range imagePatterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindAllStringSubmatch(html, -1)
		for _, match := range matches {
			if len(match) > 1 {
				mediaURL := match[1]
				// Ensure we have the large version
				if !strings.Contains(mediaURL, "format=") && !strings.Contains(mediaURL, ".jpg") && !strings.Contains(mediaURL, ".png") {
					if strings.Contains(mediaURL, "?") {
						mediaURL += "&format=jpg&name=large"
					} else {
						mediaURL += "?format=jpg&name=large"
					}
				}
				mediaURLs = append(mediaURLs, mediaURL)
			}
		}
	}

	// Remove duplicates based on media ID
	seenMediaIDs := make(map[string]string)
	for _, url := range mediaURLs {
		mediaID := te.extractMediaID(url)
		if mediaID == "" {
			mediaID = url
		}
		if existingURL, exists := seenMediaIDs[mediaID]; exists {
			if te.getURLQualityScore(url) > te.getURLQualityScore(existingURL) {
				seenMediaIDs[mediaID] = url
			}
		} else {
			seenMediaIDs[mediaID] = url
		}
	}

	seen := make(map[string]bool)
	var uniqueURLs []string
	for _, url := range mediaURLs {
		mediaID := te.extractMediaID(url)
		if mediaID == "" {
			mediaID = url
		}
		if !seen[mediaID] {
			seen[mediaID] = true
			uniqueURLs = append(uniqueURLs, seenMediaIDs[mediaID])
		}
	}

	log.Printf("📸 Extracted %d media URLs from fixupx meta tags: %v", len(uniqueURLs), uniqueURLs)

	// Download each media item
	for i, mediaURL := range uniqueURLs {
		mediaInfo, err := te.downloadMedia(mediaURL, tweetID, i)
		if err != nil {
			log.Printf("⚠️ Failed to download media %d from fixupx: %v", i, err)
			continue
		}

		// Format caption with author info
		mediaInfo.Caption = te.formatTweetCaption(displayName, username, tweetText)

		mediaInfos = append(mediaInfos, mediaInfo)
	}

	// If we found media, return it
	if len(mediaInfos) > 0 {
		return mediaInfos, nil
	}

	// If no media found but we have text, return text-only
	if tweetText != "" {
		log.Printf("📝 No media found on fixupx, returning text content")
		textMedia := &MediaInfo{
			Type:     "text",
			URL:      fmt.Sprintf("https://x.com/%s/status/%s", username, tweetID),
			Caption:  te.formatTweetCaption(displayName, username, tweetText),
			Filename: "",
		}
		mediaInfos = append(mediaInfos, textMedia)
	}

	return mediaInfos, nil
}
