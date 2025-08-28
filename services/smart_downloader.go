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
	
	// Instagram video optimization: prioritize fixed link when cookies are not available
	if strategy.Platform == "instagram" && strategy.HasFixedLink {
		// Check if this is a video URL (reel, tv, reels)
		isVideo := strings.Contains(url, "/reel/") || strings.Contains(url, "/tv/") || strings.Contains(url, "/reels/")
		if isVideo && !sd.ytdlp.HasInstagramCookies() {
			log.Printf("🚀 Instagram video without cookies detected - prioritizing ddinstagram for download")
			// Reorder methods to prioritize fixed link first
			strategy.Methods = []ExtractionMethod{
				MethodFixedLink,  // Try fixed link first (fast)
				MethodBrowser,    // Browser as fallback
				MethodYtDlp,      // yt-dlp as last resort
			}
		}
	}
	
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
				log.Printf("🔗 Trying fixed link approach for %s", strategy.Platform)
				// Get both internal (for downloading) and external (for user) URLs
				internalFixedURL := strategy.GetInternalFixedURL(url)
				userFixedURL := strategy.GetFixedURL(url)
				methodDuration := time.Since(methodStartTime)
				
				if internalFixedURL != "" {
					// For Instagram, try to download media from the fixed link first
					if strategy.Platform == "instagram" {
						log.Printf("🎯 Attempting to download media from Instagram fixed link: %s", internalFixedURL)
						
						// Try yt-dlp on the internal fixed link (ddinstagram)
						fixedMediaInfos, fixedErr := sd.ytdlp.DownloadMedia(internalFixedURL)
						if fixedErr == nil && len(fixedMediaInfos) > 0 {
							// Check if we got actual media files
							hasActualMedia := false
							for _, media := range fixedMediaInfos {
								if media.Type != "text" && media.Filename != "" {
									hasActualMedia = true
									break
								}
							}
							
							if hasActualMedia {
								log.Printf("✅ Fixed link extraction succeeded with %d media items", len(fixedMediaInfos))
								LogExtraction(strategy.Platform, url, "fixed-link-media", true, len(fixedMediaInfos), methodDuration)
								appendOriginalURL(fixedMediaInfos, url)
								return fixedMediaInfos, nil
							}
						}
						
						// If yt-dlp failed, try browser extraction on the internal fixed link
						log.Printf("🌐 Trying browser extraction on fixed link")
						browserMediaInfos, browserErr := sd.browser.ExtractMedia(internalFixedURL)
						if browserErr == nil && len(browserMediaInfos) > 0 {
							// Check if we got actual media files
							hasActualMedia := false
							for _, media := range browserMediaInfos {
								if media.Type != "text" && media.Filename != "" {
									hasActualMedia = true
									break
								}
							}
							
							if hasActualMedia {
								log.Printf("✅ Fixed link browser extraction succeeded with %d media items", len(browserMediaInfos))
								LogExtraction(strategy.Platform, url, "fixed-link-browser", true, len(browserMediaInfos), methodDuration)
								appendOriginalURL(browserMediaInfos, url)
								return browserMediaInfos, nil
							}
						}
						
						// For Instagram videos, only return fixed link if we know it can resolve
						// Check if this is a video URL (reel, tv, reels)
						isVideo := strings.Contains(url, "/reel/") || strings.Contains(url, "/tv/") || strings.Contains(url, "/reels/")
						
						if isVideo {
							// For videos without working cookies, we should not send a fixed link that won't work
							// Instead, indicate the failure so the user knows to check their cookies
							log.Printf("❌ Instagram video extraction failed - cookies may be invalid or expired")
							lastErr = fmt.Errorf("Instagram video extraction failed - cookies may be invalid or expired")
							// Continue to next method or fail
						} else {
							// For photos/carousels, return the user-facing fixed link
							log.Printf("📎 Returning fixed link for Instagram photo/carousel: %s", userFixedURL)
							LogExtraction(strategy.Platform, url, "fixed-link-text", true, 1, methodDuration)
							return []*MediaInfo{{
								Type:    "text",
								URL:     url,
								Caption: userFixedURL,
								Filename: "",
							}}, nil
						}
					} else {
						// For other platforms, return fixed link as text
						LogExtraction(strategy.Platform, url, "fixed-link", true, 1, time.Since(startTime))
						return []*MediaInfo{{
							Type:    "text",
							URL:     url,
							Caption: userFixedURL,
							Filename: "",
						}}, nil
					}
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