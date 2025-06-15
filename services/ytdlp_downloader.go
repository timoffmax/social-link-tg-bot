package services

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	
	"github.com/timoffmax/social-link-bot/config"
)

type YtDlpInfo struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	Ext         string `json:"ext"`
	Description string `json:"description"`
	Uploader    string `json:"uploader"`
	Duration    int    `json:"duration"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	Filesize    int64  `json:"filesize"`
}

type YtDlpDownloader struct {
	TempDir    string
	BinaryPath string
}

func NewYtDlpDownloader() *YtDlpDownloader {
	// Use persistent temp directory instead of system temp
	tempDir := "./temp"
	os.MkdirAll(tempDir, 0755)
	
	// Try to find yt-dlp binary - test in order of preference
	candidatePaths := []string{
		"./bin/yt-dlp",                       // Local development
		"/var/www/social-link-tg-bot/bin/yt-dlp", // Production bin
		"yt-dlp",                             // System PATH
		"./yt-dlp",                           // Current dir (often broken)
		"/var/www/social-link-tg-bot/yt-dlp", // Production root
		"youtube-dl",                         // Fallback
	}
	
	binaryPath := "yt-dlp" // Default fallback
	
	for _, candidate := range candidatePaths {
		log.Printf("Testing yt-dlp candidate: %s", candidate)
		if testYtDlpBinary(candidate) {
			log.Printf("✅ Found working yt-dlp: %s", candidate)
			binaryPath = candidate
			break
		} else {
			log.Printf("❌ Failed: %s", candidate)
		}
	}
	
	log.Printf("YtDlpDownloader initialized with binary path: %s", binaryPath)
	
	return &YtDlpDownloader{
		TempDir:    tempDir,
		BinaryPath: binaryPath,
	}
}

func (yd *YtDlpDownloader) GetMediaInfo(url string) (*YtDlpInfo, error) {
	// Use yt-dlp to extract information without downloading
	cmd := exec.Command(yd.BinaryPath, "--dump-json", "--no-playlist", url)
	
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get media info: %v", err)
	}
	
	var info YtDlpInfo
	err = json.Unmarshal(output, &info)
	if err != nil {
		return nil, fmt.Errorf("failed to parse media info: %v", err)
	}
	
	return &info, nil
}

func (yd *YtDlpDownloader) DownloadMedia(url string) ([]*MediaInfo, error) {
	log.Printf("Starting media download from: %s", url)
	
	// Ensure temp directory exists
	if err := os.MkdirAll(yd.TempDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %v", err)
	}
	
	// Create unique filename with timestamp
	timestamp := time.Now().Unix()
	outputTemplate := filepath.Join(yd.TempDir, fmt.Sprintf("%d_%%(title)s.%%(ext)s", timestamp))
	
	log.Printf("Using output template: %s", outputTemplate)
	
	// Get list of proxies to try
	proxies := config.GetYtDlpProxies()
	
	// For TikTok, skip proxies and try direct connection first
	if strings.Contains(url, "tiktok.com") {
		proxies = []string{} // Start with no proxy for TikTok
	}
	
	// Add empty string to try without proxy as last resort
	proxies = append(proxies, "")
	
	var lastErr error
	for i, proxy := range proxies {
		if proxy == "" {
			log.Printf("Attempt %d/%d: Trying without proxy", i+1, len(proxies))
		} else {
			log.Printf("Attempt %d/%d: Trying with proxy %s", i+1, len(proxies), proxy)
		}
		
		// Try download with current proxy
		result, err := yd.attemptDownload(url, outputTemplate, proxy)
		if err == nil {
			return result, nil
		}
		
		lastErr = err
		log.Printf("Download failed with proxy '%s': %v", proxy, err)
	}
	
	return nil, fmt.Errorf("all download attempts failed, last error: %v", lastErr)
}

// attemptDownload tries to download with a specific proxy (or empty string for no proxy)
func (yd *YtDlpDownloader) attemptDownload(url string, outputTemplate string, proxy string) ([]*MediaInfo, error) {
	// Extract timestamp from output template
	var timestamp int64
	fmt.Sscanf(filepath.Base(outputTemplate), "%d_", &timestamp)
	
	// Build arguments with specific proxy
	args := yd.buildDownloadArgsWithProxy(url, outputTemplate, proxy)
	
	cmd := exec.Command(yd.BinaryPath, args...)
	
	log.Printf("Executing command: %s %v", yd.BinaryPath, args)
	
	// Execute download
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("download failed: %v\nOutput: %s", err, string(output))
	}
	
	log.Printf("Download completed successfully")
	log.Printf("Output: %s", string(output))
	
	// List all files in temp directory for debugging
	if files, err := os.ReadDir(yd.TempDir); err == nil {
		log.Printf("Files in temp directory after download:")
		for _, file := range files {
			log.Printf("  - %s", file.Name())
		}
	}
	
	// Find downloaded files
	downloadedFiles, err := yd.findDownloadedFiles(timestamp)
	if err != nil {
		return nil, fmt.Errorf("failed to find downloaded files: %v", err)
	}
	
	log.Printf("Found %d downloaded files: %v", len(downloadedFiles), downloadedFiles)
	
	// Convert to MediaInfo
	var mediaInfos []*MediaInfo
	for _, filePath := range downloadedFiles {
		mediaType := yd.detectMediaType(filePath)
		
		mediaInfo := &MediaInfo{
			Type:     mediaType,
			URL:      url,
			Caption:  "Downloaded from " + yd.getPlatformName(url),
			Filename: filePath,
		}
		
		mediaInfos = append(mediaInfos, mediaInfo)
	}
	
	return mediaInfos, nil
}

func (yd *YtDlpDownloader) DownloadInstagramMedia(url string) ([]*MediaInfo, error) {
	return yd.DownloadMedia(url)
}

func (yd *YtDlpDownloader) DownloadTikTokMedia(url string) ([]*MediaInfo, error) {
	return yd.DownloadMedia(url)
}

func (yd *YtDlpDownloader) DownloadTwitterMedia(url string) ([]*MediaInfo, error) {
	return yd.DownloadMedia(url)
}

func (yd *YtDlpDownloader) findDownloadedFiles(timestamp int64) ([]string, error) {
	var files []string
	
	err := filepath.Walk(yd.TempDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		
		if !info.IsDir() {
			filename := info.Name()
			if strings.HasPrefix(filename, fmt.Sprintf("%d_", timestamp)) {
				files = append(files, path)
			}
		}
		
		return nil
	})
	
	return files, err
}

// buildDownloadArgsWithProxy builds download args with a specific proxy
func (yd *YtDlpDownloader) buildDownloadArgsWithProxy(url string, outputTemplate string, proxy string) []string {
	args := yd.buildDownloadArgs(url, outputTemplate)
	
	// Remove any existing proxy from args (it's added at the end by buildDownloadArgs)
	var cleanArgs []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--proxy" {
			i++ // Skip the proxy value
			continue
		}
		cleanArgs = append(cleanArgs, args[i])
	}
	
	// Add specific proxy if provided
	if proxy != "" {
		cleanArgs = append(cleanArgs, "--proxy", proxy)
	}
	
	return cleanArgs
}

// buildDownloadArgs creates platform-specific arguments for yt-dlp
func (yd *YtDlpDownloader) buildDownloadArgs(url string, outputTemplate string) []string {
	baseArgs := []string{
		"--no-playlist",
		"--output", outputTemplate,
		"--format", "best[filesize<50M]/best",
		"--extractor-retries", "2",        // Reduced retries for faster response
		"--geo-bypass",                    // Bypass geographic restrictions
		"--geo-bypass-country", "US",      // Use US geolocation
		"--ignore-errors",                 // Continue on download errors
		"--no-check-certificate",          // Ignore SSL certificate errors (for proxies)
	}
	
	// Add cookies if available
	if yd.cookiesExist() {
		baseArgs = append(baseArgs, "--cookies", "./cookies.txt")
	}
	
	// Platform-specific optimizations
	if strings.Contains(url, "instagram.com") {
		// Instagram-specific args with enhanced anti-blocking
		instagramArgs := []string{
			"--user-agent", "Mozilla/5.0 (iPhone; CPU iPhone OS 15_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/15.0 Mobile/15E148 Safari/604.1",
			"--referer", "https://www.instagram.com/",
			"--no-check-certificate",
			"--extractor-args", "instagram:api_version=v1",  // Use older API
			"--add-header", "Accept-Language:en-US,en;q=0.9",
			"--add-header", "Accept:text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
		}
		
		// Add Instagram credentials if available
		if username := config.GetInstagramUsername(); username != "" {
			if password := config.GetInstagramPassword(); password != "" {
				log.Printf("Using Instagram credentials for user: %s", username)
				instagramArgs = append(instagramArgs, "--username", username, "--password", password)
			}
		}
		
		baseArgs = append(baseArgs, instagramArgs...)
	} else if strings.Contains(url, "tiktok.com") {
		// TikTok-specific args with enhanced anti-blocking and no proxy for better compatibility
		tiktokArgs := []string{
			"--user-agent", "Mozilla/5.0 (iPhone; CPU iPhone OS 15_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/15.0 Mobile/15E148 Safari/604.1",
			"--referer", "https://www.tiktok.com/",
			"--add-header", "Accept-Language:en-US,en;q=0.9",
			"--add-header", "Accept:text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
			"--add-header", "sec-fetch-dest:document", 
			"--add-header", "sec-fetch-mode:navigate",
			"--add-header", "sec-fetch-site:none",
			"--add-header", "sec-fetch-user:?1",
		}
		
		// Add TikTok credentials if available
		if username := config.GetTikTokUsername(); username != "" {
			if password := config.GetTikTokPassword(); password != "" {
				log.Printf("Using TikTok credentials for user: %s", username)
				tiktokArgs = append(tiktokArgs, "--username", username, "--password", password)
			}
		}
		
		baseArgs = append(baseArgs, tiktokArgs...)
	} else if strings.Contains(url, "twitter.com") || strings.Contains(url, "x.com") {
		// Twitter/X-specific args
		twitterArgs := []string{
			"--user-agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36",
		}
		
		// Add Twitter credentials if available
		if username := config.GetTwitterUsername(); username != "" {
			if password := config.GetTwitterPassword(); password != "" {
				log.Printf("Using Twitter credentials for user: %s", username)
				twitterArgs = append(twitterArgs, "--username", username, "--password", password)
			}
		}
		
		baseArgs = append(baseArgs, twitterArgs...)
	}
	
	// Don't add proxy here, it's handled by buildDownloadArgsWithProxy
	
	baseArgs = append(baseArgs, url)
	return baseArgs
}

// cookiesExist checks if cookies file exists
func (yd *YtDlpDownloader) cookiesExist() bool {
	_, err := os.Stat("./cookies.txt")
	return err == nil
}

func (yd *YtDlpDownloader) detectMediaType(filePath string) string {
	ext := strings.ToLower(filepath.Ext(filePath))
	
	switch ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp":
		return "photo"
	case ".mp4", ".mov", ".avi", ".mkv", ".webm":
		return "video"
	default:
		return "document"
	}
}

func (yd *YtDlpDownloader) getPlatformName(url string) string {
	if strings.Contains(url, "instagram.com") {
		return "Instagram"
	} else if strings.Contains(url, "tiktok.com") {
		return "TikTok"
	} else if strings.Contains(url, "twitter.com") || strings.Contains(url, "x.com") {
		return "Twitter"
	}
	return "social media"
}

func (yd *YtDlpDownloader) CleanupFile(filepath string) error {
	return os.Remove(filepath)
}

func (yd *YtDlpDownloader) CleanupTempFiles() error {
	// Clean up files older than 1 hour
	cutoff := time.Now().Add(-1 * time.Hour)
	
	return filepath.Walk(yd.TempDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		
		if !info.IsDir() && info.ModTime().Before(cutoff) {
			os.Remove(path)
		}
		
		return nil
	})
}

func (yd *YtDlpDownloader) IsAvailable() bool {
	return testYtDlpBinary(yd.BinaryPath)
}

// testYtDlpBinary tests if a yt-dlp binary path works by running --version
func testYtDlpBinary(path string) bool {
	cmd := exec.Command(path, "--version")
	err := cmd.Run()
	return err == nil
}