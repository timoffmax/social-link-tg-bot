package services

import (
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type CliBrowserExtractor struct {
	tempDir    string
	workingDir string
}

func NewCliBrowserExtractor(workingDir, tempDir string) *CliBrowserExtractor {
	return &CliBrowserExtractor{
		tempDir:    tempDir,
		workingDir: workingDir,
	}
}

// ExtractTwitterImages uses command-line Chrome with virtual-time-budget for Twitter
func (cbe *CliBrowserExtractor) ExtractTwitterImages(twitterURL string) ([]*MediaInfo, error) {
	log.Printf("🚀 Extracting Twitter images with CLI Chrome + virtual-time-budget")

	// Find Chrome executable
	chromePaths := []string{
		"/usr/bin/google-chrome",
		"/usr/bin/google-chrome-stable",
		"/usr/bin/chromium-browser",
		"/usr/bin/chromium",
		"/opt/google/chrome/chrome",
		"/snap/bin/chromium",
	}

	chromePath := ""
	for _, path := range chromePaths {
		if _, err := os.Stat(path); err == nil {
			chromePath = path
			break
		}
	}

	if chromePath == "" {
		return nil, fmt.Errorf("no Chrome/Chromium executable found")
	}

	// Create temporary file for HTML output
	tempFile := filepath.Join(cbe.tempDir, fmt.Sprintf("twitter_chrome_%d.html", time.Now().Unix()))
	defer os.Remove(tempFile)

	// Chrome command with virtual-time-budget
	cmd := exec.Command(chromePath,
		"--headless",
		"--disable-gpu",
		"--no-sandbox",
		"--virtual-time-budget=10000",
		"--dump-dom",
		"--user-agent=Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36",
		twitterURL,
	)

	// Capture output
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("Chrome execution failed: %v", err)
	}

	// Save HTML for analysis
	if err := ioutil.WriteFile(tempFile, output, 0644); err != nil {
		return nil, fmt.Errorf("failed to write HTML output: %v", err)
	}

	html := string(output)
	log.Printf("📄 Chrome rendered HTML: %d bytes", len(html))

	// Extract individual images
	return cbe.extractTwitterImagesFromHTML(html, twitterURL)
}

func (cbe *CliBrowserExtractor) extractTwitterImagesFromHTML(html, twitterURL string) ([]*MediaInfo, error) {
	// Extract individual image URLs
	imagePatterns := []string{
		// Twitter image patterns with css-9pa8cd class (2025 structure)
		`<img[^>]*alt="Image"[^>]*src="(https://pbs\.twimg\.com/media/[^"]*)"[^>]*class="css-9pa8cd"`,
		`<img[^>]*class="css-9pa8cd"[^>]*alt="Image"[^>]*src="(https://pbs\.twimg\.com/media/[^"]*)"`,
		`<img[^>]*draggable="true"[^>]*src="(https://pbs\.twimg\.com/media/[^"]*)"[^>]*class="css-9pa8cd"`,
		// More generic patterns
		`<img[^>]*src="(https://pbs\.twimg\.com/media/[^"?]+\?format=jpg[^"]*)"[^>]*alt="Image"`,
		`<img[^>]*alt="Image"[^>]*src="(https://pbs\.twimg\.com/media/[^"]*)"`,
		// Any img tag with pbs.twimg.com/media/ (broader pattern)
		`<img[^>]*src="(https://pbs\.twimg\.com/media/[^"]*)"[^>]*>`,
		`<img[^>]*src="(https://pbs\.twimg\.com/media/[^"&]*)[^"]*"`,
	}

	var imageURLs []string
	for _, pattern := range imagePatterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindAllStringSubmatch(html, -1)
		for _, match := range matches {
			if len(match) > 1 {
				imageURLs = append(imageURLs, match[1])
			}
		}
	}

	// Filter out profile images and other non-tweet images
	var tweetImages []string
	seen := make(map[string]bool)
	for _, url := range imageURLs {
		if !strings.Contains(url, "profile_images") && !strings.Contains(url, "_normal") {
			// Clean up URL (remove HTML entities)
			cleanURL := strings.ReplaceAll(url, "&amp;", "&")
			// Upgrade to high quality
			cleanURL = cbe.upgradeTwitterImageURL(cleanURL)
			
			if !seen[cleanURL] {
				tweetImages = append(tweetImages, cleanURL)
				seen[cleanURL] = true
			}
		}
	}

	log.Printf("🖼️ Found %d individual Twitter images (from %d total URLs)", len(tweetImages), len(imageURLs))
	
	// Debug: log if we found any pbs.twimg.com at all
	if len(imageURLs) == 0 {
		pbsCount := strings.Count(html, "pbs.twimg.com")
		tweetTextCount := strings.Count(html, `data-testid="tweetText"`)
		log.Printf("🔍 Debug: HTML contains %d pbs.twimg.com references and %d tweetText elements", pbsCount, tweetTextCount)
	}

	if len(tweetImages) == 0 {
		return nil, fmt.Errorf("no Twitter images found in rendered HTML")
	}

	// Extract caption from HTML
	caption := cbe.extractTwitterCaption(html)
	
	// If caption extraction failed, fall back to Twitter extractor for text
	if caption == "Downloaded from Twitter" {
		log.Printf("🔍 CLI browser caption extraction failed, trying Twitter extractor for text")
		twitterExtractor := NewTwitterExtractor(cbe.workingDir, cbe.tempDir)
		if textResult, err := twitterExtractor.ExtractTweet(twitterURL); err == nil && len(textResult) > 0 {
			if textResult[0].Caption != "" {
				caption = textResult[0].Caption
				log.Printf("✅ Got caption from Twitter extractor: %.100s...", caption)
			}
		}
	}

	// Download each image
	var mediaInfos []*MediaInfo
	for i, imageURL := range tweetImages {
		filename, err := cbe.downloadMediaFile(imageURL, "twitter", "photo", i)
		if err != nil {
			log.Printf("❌ Failed to download Twitter image %d: %v", i+1, err)
			continue
		}

		mediaInfo := &MediaInfo{
			Type:     "photo",
			URL:      twitterURL,
			Caption:  caption,
			Filename: filename,
		}
		mediaInfos = append(mediaInfos, mediaInfo)
	}

	return mediaInfos, nil
}

func (cbe *CliBrowserExtractor) upgradeTwitterImageURL(url string) string {
	// Upgrade image quality
	url = strings.ReplaceAll(url, "&name=small", "&name=orig")
	url = strings.ReplaceAll(url, "&name=medium", "&name=orig")
	url = strings.ReplaceAll(url, "&name=large", "&name=orig")
	url = strings.ReplaceAll(url, "&name=360x360", "&name=orig")
	if !strings.Contains(url, "&name=") && strings.Contains(url, "?format=") {
		url += "&name=orig"
	}
	return url
}

func (cbe *CliBrowserExtractor) extractTwitterCaption(html string) string {
	// Try to extract the main tweet text content using multiple patterns
	patterns := []string{
		// Try data-testid="tweetText" first (most reliable in 2025)
		`<div[^>]*data-testid="tweetText"[^>]*>(.*?)</div>`,
		`<span[^>]*data-testid="tweetText"[^>]*>(.*?)</span>`,
		// Try Open Graph description (reliable)
		`<meta[^>]*property="og:description"[^>]*content="([^"]*)"`,
		// Try regular description meta tag
		`<meta[^>]*name="description"[^>]*content="([^"]*)"`,
		// Try title tag content (sometimes contains tweet text)
		`<title[^>]*>([^<]+?) / X</title>`,
		`<title[^>]*>([^<]+?) on X:</title>`,
		// JSON embedded text patterns
		`"full_text"\s*:\s*"([^"]*)"`,
		`"text"\s*:\s*"([^"]*)"`,
		// Try broader patterns for tweet content
		`"([^"]*)" / X`,
		`"([^"]*)" on X:`,
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(`(?s)` + pattern) // (?s) makes . match newlines
		if match := re.FindStringSubmatch(html); len(match) > 1 {
			caption := strings.TrimSpace(match[1])
			if caption != "" && caption != "null" && len(caption) > 10 {
				// Skip if it's just domain names or generic text
				if !strings.Contains(strings.ToLower(caption), "twitter") && 
				   !strings.Contains(strings.ToLower(caption), "log in") &&
				   !strings.Contains(strings.ToLower(caption), "sign up") {
					
					// Clean and decode the caption
					caption = cbe.cleanTweetText(caption)
					if len(caption) > 10 {
						return caption
					}
				}
			}
		}
	}

	return "Downloaded from Twitter"
}

func (cbe *CliBrowserExtractor) cleanTweetText(rawText string) string {
	text := rawText
	
	// Remove HTML tags
	text = regexp.MustCompile(`<[^>]*>`).ReplaceAllString(text, "")
	
	// Decode HTML entities
	text = strings.ReplaceAll(text, "&amp;", "&")
	text = strings.ReplaceAll(text, "&lt;", "<")
	text = strings.ReplaceAll(text, "&gt;", ">")
	text = strings.ReplaceAll(text, "&quot;", "\"")
	text = strings.ReplaceAll(text, "&#39;", "'")
	text = strings.ReplaceAll(text, "&#34;", "\"")
	
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

func (cbe *CliBrowserExtractor) downloadMediaFile(mediaURL, platform, mediaType string, index int) (string, error) {
	// Create filename
	timestamp := time.Now().Unix()
	extension := ".jpg"
	if mediaType == "video" {
		extension = ".mp4"
	}
	
	filename := filepath.Join(cbe.tempDir, fmt.Sprintf("%d_%s_%s_%d%s", timestamp, platform, mediaType, index, extension))

	// Download file
	cmd := exec.Command("curl", "-s", "-o", filename, 
		"-H", "User-Agent: Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36",
		"-H", "Referer: https://twitter.com/",
		mediaURL)

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to download %s: %v", mediaURL, err)
	}

	// Check if file was downloaded successfully
	if fileInfo, err := os.Stat(filename); err != nil || fileInfo.Size() == 0 {
		os.Remove(filename)
		return "", fmt.Errorf("downloaded file is empty or missing")
	}

	return filename, nil
}