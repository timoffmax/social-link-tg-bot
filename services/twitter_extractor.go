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
	cookiesPath string
	tempDir    string
	workingDir string
}

type TwitterMediaItem struct {
	URL  string `json:"url"`
	Type string `json:"type"` // "photo" or "video"
}

type TwitterTweetData struct {
	Text  string             `json:"text"`
	Media []TwitterMediaItem `json:"media"`
}

func NewTwitterExtractor(workingDir, tempDir string) *TwitterExtractor {
	client := &http.Client{
		Timeout: 30 * time.Second,
	}
	
	return &TwitterExtractor{
		client:     client,
		cookiesPath: filepath.Join(workingDir, "twitter_cookies.txt"),
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
	// Prioritize embed API since it's most reliable
	methods := []func(string, string) ([]*MediaInfo, error){
		te.extractViaEmbedAPI,
		te.extractViaWebInterface,
		te.extractViaSyndicationAPI,
		te.extractViaDirectScraping,
		te.extractViaNitterProxy,
		te.extractViaFixupxProxy,
		te.extractViaSimpleTextFallback,
	}
	
	for i, method := range methods {
		log.Printf("📡 Trying extraction method %d/%d", i+1, len(methods))
		mediaInfos, err := method(tweetID, username)
		if err == nil && len(mediaInfos) > 0 {
			log.Printf("✅ Success with method %d: found %d items", i+1, len(mediaInfos))
			return mediaInfos, nil
		}
		log.Printf("❌ Method %d failed: %v", i+1, err)
	}
	
	return nil, fmt.Errorf("all extraction methods failed")
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
	
	// Add cookies if available
	te.addCookiesToRequest(req)
	
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
	
	// Parse HTML and extract media URLs
	return te.parseHTMLForMedia(bodyStr, tweetID)
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
	
	// Extract tweet text
	tweetText := ""
	if text, ok := synData["text"].(string); ok {
		tweetText = text
		log.Printf("📝 Got tweet text from syndication: %s", tweetText)
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
			Type:    "text",
			URL:     fmt.Sprintf("https://x.com/i/web/status/%s", tweetID),
			Caption: tweetText,
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
	
	te.addCookiesToRequest(req)
	
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
	
	return te.parseHTMLForMedia(string(body), tweetID)
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
			result, err := te.parseHTMLForMedia(bodyStr, tweetID)
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
	
	// Parse fixupx HTML for media and text
	return te.parseFixupxHTML(bodyStr, tweetID, username)
}

func (te *TwitterExtractor) extractViaSimpleTextFallback(tweetID string, username string) ([]*MediaInfo, error) {
	log.Printf("📝 Trying simple text fallback for tweet %s by @%s", tweetID, username)
	
	// As a last resort, just return a message with the fixed link
	fixedURL := fmt.Sprintf("https://fixupx.com/%s/status/%s", username, tweetID)
	
	textMediaInfo := &MediaInfo{
		Type:    "text",
		URL:     fmt.Sprintf("https://x.com/i/web/status/%s", tweetID),
		Caption: fmt.Sprintf("🐦 Twitter content (images may be available): %s", fixedURL),
		Filename: "",
	}
	
	return []*MediaInfo{textMediaInfo}, nil
}

func (te *TwitterExtractor) parseHTMLForMedia(html, tweetID string) ([]*MediaInfo, error) {
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
		mediaInfos = append(mediaInfos, mediaInfo)
	}
	
	// If no media found but we have text, return text-only
	if len(mediaInfos) == 0 && tweetText != "" {
		log.Printf("📝 No media found, returning text content")
		textMedia := &MediaInfo{
			Type:    "text",
			URL:     fmt.Sprintf("https://x.com/i/web/status/%s", tweetID),
			Caption: tweetText,
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
	
	// Extract tweet text from the HTML
	tweetText := ""
	textPattern := regexp.MustCompile(`<p[^>]*lang="[^"]*"[^>]*dir="[^"]*"[^>]*>(.*?)</p>`)
	if matches := textPattern.FindStringSubmatch(html); len(matches) > 1 {
		tweetText = matches[1]
		// Clean up HTML entities
		tweetText = strings.ReplaceAll(tweetText, "&#39;", "'")
		tweetText = strings.ReplaceAll(tweetText, "&amp;", "&")
		tweetText = strings.ReplaceAll(tweetText, "&lt;", "<")
		tweetText = strings.ReplaceAll(tweetText, "&gt;", ">")
		tweetText = strings.ReplaceAll(tweetText, "&quot;", `"`)
		// Remove any remaining HTML tags
		tweetText = regexp.MustCompile(`<[^>]*>`).ReplaceAllString(tweetText, "")
		tweetText = strings.TrimSpace(tweetText)
	}
	
	log.Printf("📝 Extracted tweet text from embed: %s", tweetText)
	
	// Extract pic.twitter.com URLs and convert them
	picPattern := regexp.MustCompile(`pic\.twitter\.com/([a-zA-Z0-9]+)`)
	picMatches := picPattern.FindAllStringSubmatch(html, -1)
	
	var mediaInfos []*MediaInfo
	
	// Check if we have media references
	hasMedia := len(picMatches) > 0
	
	if hasMedia {
		log.Printf("🖼️ Found %d pic.twitter.com references", len(picMatches))
		
		// For each pic.twitter.com URL, we need to try to get the actual image
		// Since we can't directly resolve these without authentication,
		// we'll add a note about media being present
		for _, match := range picMatches {
			if len(match) > 1 {
				picID := match[1]
				log.Printf("📸 Found pic.twitter.com/%s", picID)
			}
		}
	}
	
	// Create the response with appropriate caption
	caption := tweetText
	if hasMedia && caption != "" {
		// Add fixupx link for media access
		username, _ := te.extractUsernameFromEmbedData(embedData)
		if username == "" {
			username = "user"
		}
		fixedURL := fmt.Sprintf("https://fixupx.com/%s/status/%s", username, tweetID)
		caption = fmt.Sprintf("%s\n\n🖼️ Media available at: %s", tweetText, fixedURL)
	}
	
	if caption != "" {
		textMedia := &MediaInfo{
			Type:    "text",
			URL:     fmt.Sprintf("https://x.com/i/web/status/%s", tweetID),
			Caption: caption,
			Filename: "",
		}
		mediaInfos = append(mediaInfos, textMedia)
	}
	
	return mediaInfos, nil
}

func (te *TwitterExtractor) extractTweetText(html string) string {
	// Multiple patterns to extract tweet text - updated for Nitter and modern Twitter
	patterns := []string{
		// Nitter-specific patterns (high priority since it worked)
		`<div[^>]*class="[^"]*tweet-content[^"]*"[^>]*>(.*?)</div>`,
		`<div[^>]*class="[^"]*media-body[^"]*"[^>]*>(.*?)</div>`,
		`<p[^>]*class="[^"]*tweet-content[^"]*"[^>]*>(.*?)</p>`,
		`<div[^>]*class="[^"]*timeline-tweet[^"]*"[^>]*>.*?<div[^>]*class="[^"]*tweet-text[^"]*"[^>]*>(.*?)</div>`,
		
		// Modern Twitter data-testid patterns
		`<div[^>]*data-testid="tweetText"[^>]*>(.*?)</div>`,
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
	
	for _, pattern := range patterns {
		re := regexp.MustCompile(`(?s)` + pattern) // (?s) makes . match newlines
		matches := re.FindStringSubmatch(html)
		if len(matches) > 1 {
			// Clean up HTML tags and decode entities
			text := strings.ReplaceAll(matches[1], "<br>", "\n")
			text = strings.ReplaceAll(text, "<br/>", "\n")
			text = strings.ReplaceAll(text, "<br />", "\n")
			text = regexp.MustCompile(`<[^>]*>`).ReplaceAllString(text, "")
			
			// Decode JSON escape sequences
			text = strings.ReplaceAll(text, `\"`, `"`)
			text = strings.ReplaceAll(text, `\\n`, "\n")
			text = strings.ReplaceAll(text, `\\t`, "\t")
			text = strings.ReplaceAll(text, `\\r`, "\r")
			
			text = strings.TrimSpace(text)
			if text != "" && text != "null" {
				return text
			}
		}
	}
	
	return ""
}

func (te *TwitterExtractor) extractMediaURLs(html string) []string {
	var urls []string
	
	// Patterns to match image and video URLs - updated for Nitter and modern Twitter
	patterns := []string{
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
	
	// Remove duplicates
	seen := make(map[string]bool)
	var uniqueURLs []string
	for _, url := range cleanURLs {
		if !seen[url] {
			seen[url] = true
			uniqueURLs = append(uniqueURLs, url)
		}
	}
	
	return uniqueURLs
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
		Caption:  fmt.Sprintf("Downloaded from Twitter"),
		Filename: filename,
	}, nil
}

func (te *TwitterExtractor) getFileExtension(url, contentType string) string {
	// Try to get extension from URL
	if ext := filepath.Ext(url); ext != "" {
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

func (te *TwitterExtractor) addCookiesToRequest(req *http.Request) {
	// Read cookies file if it exists
	if _, err := os.Stat(te.cookiesPath); os.IsNotExist(err) {
		log.Printf("⚠️ No Twitter cookies file found at %s", te.cookiesPath)
		return
	}
	
	cookiesData, err := os.ReadFile(te.cookiesPath)
	if err != nil {
		log.Printf("⚠️ Failed to read cookies file: %v", err)
		return
	}
	
	// Parse Netscape cookie format and add to request
	lines := strings.Split(string(cookiesData), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		
		parts := strings.Split(line, "\t")
		if len(parts) >= 7 {
			domain := parts[0]
			name := parts[5]
			value := parts[6]
			
			// Only add cookies for twitter/x domains
			if strings.Contains(domain, "twitter.com") || strings.Contains(domain, "x.com") {
				// Clean up cookie value - remove quotes and invalid characters
				value = strings.Trim(value, `"`)
				value = strings.ReplaceAll(value, `\"`, `"`)
				value = strings.ReplaceAll(value, `\n`, ``)
				value = strings.ReplaceAll(value, `\r`, ``)
				value = strings.ReplaceAll(value, `\t`, ``)
				
				// Skip cookies with invalid characters
				if strings.ContainsAny(value, "\n\r\t\"") {
					log.Printf("⚠️ Skipping invalid cookie %s", name)
					continue
				}
				
				cookie := &http.Cookie{
					Name:  name,
					Value: value,
				}
				req.AddCookie(cookie)
			}
		}
	}
	
	log.Printf("🍪 Added Twitter cookies to request")
}

func (te *TwitterExtractor) parseFixupxHTML(html string, tweetID string, username string) ([]*MediaInfo, error) {
	var mediaInfos []*MediaInfo
	
	log.Printf("📝 Parsing fixupx HTML for tweet %s by @%s", tweetID, username)
	
	// Extract tweet text from fixupx page
	tweetText := te.extractFixupxTweetText(html)
	log.Printf("📝 Extracted tweet text from fixupx: %.200s", tweetText)
	
	// Extract media URLs from fixupx page
	mediaURLs := te.extractFixupxMediaURLs(html)
	log.Printf("📸 Extracted %d media URLs from fixupx: %v", len(mediaURLs), mediaURLs)
	
	// Download each media item
	for i, mediaURL := range mediaURLs {
		mediaInfo, err := te.downloadMedia(mediaURL, tweetID, i)
		if err != nil {
			log.Printf("⚠️ Failed to download media %d from fixupx: %v", i, err)
			continue
		}
		
		// Use extracted tweet text as caption
		if tweetText != "" {
			mediaInfo.Caption = tweetText
		}
		
		mediaInfos = append(mediaInfos, mediaInfo)
	}
	
	// If no media found but we have text, return text-only
	if len(mediaInfos) == 0 && tweetText != "" {
		log.Printf("📝 No media found on fixupx, returning text content")
		textMedia := &MediaInfo{
			Type:    "text",
			URL:     fmt.Sprintf("https://x.com/%s/status/%s", username, tweetID),
			Caption: tweetText,
			Filename: "",
		}
		mediaInfos = append(mediaInfos, textMedia)
	}
	
	return mediaInfos, nil
}

func (te *TwitterExtractor) extractFixupxTweetText(html string) string {
	// Fixupx-specific patterns for tweet text
	patterns := []string{
		// Look for the main tweet content in fixupx
		`<div[^>]*class="[^"]*tweet-text[^"]*"[^>]*>(.*?)</div>`,
		`<p[^>]*class="[^"]*tweet-content[^"]*"[^>]*>(.*?)</p>`,
		`<div[^>]*class="[^"]*content[^"]*"[^>]*>(.*?)</div>`,
		
		// Generic content patterns
		`<article[^>]*>.*?<p[^>]*>(.*?)</p>`,
		`<main[^>]*>.*?<p[^>]*>(.*?)</p>`,
		
		// Meta tag fallbacks
		`<meta[^>]*property="og:description"[^>]*content="([^"]*)"`,
		`<meta[^>]*name="description"[^>]*content="([^"]*)"`,
		`<title[^>]*>([^<]*?)(?: / Twitter| - Twitter|$)</title>`,
	}
	
	for _, pattern := range patterns {
		re := regexp.MustCompile(`(?s)` + pattern)
		matches := re.FindStringSubmatch(html)
		if len(matches) > 1 {
			// Clean up HTML tags and decode entities
			text := strings.ReplaceAll(matches[1], "<br>", "\n")
			text = strings.ReplaceAll(text, "<br/>", "\n")
			text = strings.ReplaceAll(text, "<br />", "\n")
			text = regexp.MustCompile(`<[^>]*>`).ReplaceAllString(text, "")
			
			// Decode HTML entities
			text = strings.ReplaceAll(text, "&amp;", "&")
			text = strings.ReplaceAll(text, "&lt;", "<")
			text = strings.ReplaceAll(text, "&gt;", ">")
			text = strings.ReplaceAll(text, "&quot;", "\"")
			text = strings.ReplaceAll(text, "&#39;", "'")
			
			text = strings.TrimSpace(text)
			if text != "" && text != "null" && !strings.Contains(text, "fixupx") {
				return text
			}
		}
	}
	
	return ""
}

func (te *TwitterExtractor) extractFixupxMediaURLs(html string) []string {
	var urls []string
	
	// Fixupx-specific patterns for media URLs
	patterns := []string{
		// Direct image/video URLs
		`<img[^>]*src="([^"]*(?:pbs\.twimg\.com|twimg\.com)[^"]*)"`,
		`<video[^>]*src="([^"]*(?:video\.twimg\.com|twimg\.com)[^"]*)"`,
		`<source[^>]*src="([^"]*(?:video\.twimg\.com|twimg\.com)[^"]*)"`,
		
		// Look for download links or media containers
		`href="([^"]*(?:pbs\.twimg\.com|video\.twimg\.com)[^"]*)"`,
		`data-src="([^"]*(?:pbs\.twimg\.com|video\.twimg\.com)[^"]*)"`,
		
		// Generic Twitter media URL patterns
		`(https://pbs\.twimg\.com/media/[A-Za-z0-9_-]+\?format=(?:jpg|jpeg|png|gif|webp)[^"\s]*)`,
		`(https://pbs\.twimg\.com/media/[A-Za-z0-9_-]+\.(?:jpg|jpeg|png|gif|webp))`,
		`(https://video\.twimg\.com/[^"\s]+\.(?:mp4|m3u8))`,
		
		// Any pbs.twimg.com or video.twimg.com references
		`(https://[^"\s]*(?:pbs|video)\.twimg\.com[^"\s]*)`,
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
	
	// Clean up URLs and add format params for images without extensions
	var cleanURLs []string
	for _, url := range urls {
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
	
	// Remove duplicates
	seen := make(map[string]bool)
	var uniqueURLs []string
	for _, url := range cleanURLs {
		if !seen[url] {
			seen[url] = true
			uniqueURLs = append(uniqueURLs, url)
		}
	}
	
	return uniqueURLs
}