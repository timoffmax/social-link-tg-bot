package services

import (
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SmartDownloader handles media downloads with platform-specific strategies
type SmartDownloader struct {
	ytdlp    *YtDlpDownloader
	browser  *UniversalBrowserExtractor
	tempDir  string
	workingDir string
}

// NewSmartDownloader creates a new smart downloader instance
func NewSmartDownloader() *SmartDownloader {
	workingDir, err := os.Getwd()
	if err != nil {
		log.Printf("Failed to get working directory: %v", err)
		workingDir = "."
	}
	
	tempDir := filepath.Join(workingDir, "temp")
	os.MkdirAll(tempDir, 0755)
	
	return &SmartDownloader{
		ytdlp:    NewYtDlpDownloader(),
		browser:  NewUniversalBrowserExtractor(workingDir, tempDir),
		tempDir:  tempDir,
		workingDir: workingDir,
	}
}

// DownloadMedia downloads media using the optimal strategy for the platform
func (sd *SmartDownloader) DownloadMedia(url string) ([]*MediaInfo, error) {
	startTime := time.Now()
	
	// Get the extraction strategy for this URL
	strategy := GetExtractionStrategy(url)
	strategy.LogStrategy()
	
	var lastErr error
	
	// Try each method in order
	for _, method := range strategy.Methods {
		methodStartTime := time.Now()
		
		switch method {
		case MethodYtDlp:
			log.Printf("🎯 Trying yt-dlp extraction for %s", strategy.Platform)
			mediaInfos, err := sd.tryYtDlpExtraction(url, strategy.Platform)
			methodDuration := time.Since(methodStartTime)
			
			if err == nil && len(mediaInfos) > 0 {
				log.Printf("✅ yt-dlp extraction succeeded with %d items", len(mediaInfos))
				LogExtraction(strategy.Platform, url, "yt-dlp", true, len(mediaInfos), time.Since(startTime))
				appendOriginalURL(mediaInfos, url)
				return mediaInfos, nil
			}
			lastErr = err
			log.Printf("❌ yt-dlp extraction failed: %v", err)
			LogExtraction(strategy.Platform, url, "yt-dlp", false, 0, methodDuration)
			
		case MethodBrowser:
			log.Printf("🌐 Trying browser extraction for %s", strategy.Platform)
			mediaInfos, err := sd.browser.ExtractMedia(url)
			methodDuration := time.Since(methodStartTime)
			
			if err == nil && len(mediaInfos) > 0 {
				log.Printf("✅ Browser extraction succeeded with %d items", len(mediaInfos))
				LogExtraction(strategy.Platform, url, "browser", true, len(mediaInfos), time.Since(startTime))
				appendOriginalURL(mediaInfos, url)
				return mediaInfos, nil
			}
			lastErr = err
			log.Printf("❌ Browser extraction failed: %v", err)
			LogExtraction(strategy.Platform, url, "browser", false, 0, methodDuration)
			
		case MethodFixedLink:
			if strategy.HasFixedLink {
				log.Printf("🔗 Returning fixed link as fallback")
				fixedURL := strategy.GetFixedURL(url)
				if fixedURL != "" {
					// Return a text-only MediaInfo with the fixed link
					LogExtraction(strategy.Platform, url, "fixed-link", true, 1, time.Since(startTime))
					return []*MediaInfo{{
						Type:    "text",
						URL:     url,
						Caption: fixedURL,
						Filename: "",
					}}, nil
				}
			}
		}
	}
	
	// If all methods failed, return error
	totalDuration := time.Since(startTime)
	LogExtraction(strategy.Platform, url, "all-methods", false, 0, totalDuration)
	return nil, fmt.Errorf("all extraction methods failed for %s: %v", url, lastErr)
}

// tryYtDlpExtraction attempts to extract media using yt-dlp with platform-specific logic
func (sd *SmartDownloader) tryYtDlpExtraction(url string, platform string) ([]*MediaInfo, error) {
	// Use platform-specific methods that already exist
	switch platform {
	case "twitter":
		return sd.ytdlp.DownloadTwitterMedia(url)
	case "instagram":
		return sd.ytdlp.DownloadInstagramMedia(url)
	case "tiktok":
		return sd.ytdlp.DownloadTikTokMedia(url)
	case "youtube":
		return sd.ytdlp.DownloadYouTubeMedia(url)
	case "reddit":
		return sd.ytdlp.DownloadRedditMedia(url)
	case "facebook":
		return sd.ytdlp.DownloadFacebookMedia(url)
	case "9gag":
		return sd.ytdlp.Download9GAGMedia(url)
	default:
		return sd.ytdlp.DownloadMedia(url)
	}
}

// IsAvailable checks if the downloader is available
func (sd *SmartDownloader) IsAvailable() bool {
	return sd.ytdlp.IsAvailable()
}

// CleanupFile removes a temporary file
func (sd *SmartDownloader) CleanupFile(filename string) {
	sd.ytdlp.CleanupFile(filename)
}

// GetWorkingDir returns the working directory
func (sd *SmartDownloader) GetWorkingDir() string {
	return sd.workingDir
}

// GetTempDir returns the temp directory
func (sd *SmartDownloader) GetTempDir() string {
	return sd.tempDir
}

// appendOriginalURL adds the original URL to caption while respecting Telegram limits
func appendOriginalURL(mediaInfos []*MediaInfo, originalURL string) {
	const maxCaptionLength = 1024 // Telegram limit for photo/video/document captions
	cleanURL := cleanURLParams(originalURL)
	urlSuffix := fmt.Sprintf("\n\n🔗 %s", cleanURL)
	
	for _, mediaInfo := range mediaInfos {
		// Skip text-only media (fixed links)
		if mediaInfo.Type == "text" || mediaInfo.Filename == "" {
			continue
		}
		
		currentCaption := mediaInfo.Caption
		
		// Calculate available space
		availableSpace := maxCaptionLength - len(urlSuffix)
		
		if len(currentCaption) <= availableSpace {
			// Caption fits with URL
			mediaInfo.Caption = currentCaption + urlSuffix
		} else if len(currentCaption) > 0 {
			// Truncate caption to fit with URL
			truncated := currentCaption[:availableSpace-3] + "..."
			mediaInfo.Caption = truncated + urlSuffix
		} else {
			// No caption, just add URL (remove leading newlines)
			mediaInfo.Caption = strings.TrimPrefix(urlSuffix, "\n\n")
		}
	}
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