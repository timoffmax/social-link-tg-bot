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
	tempDir := filepath.Join(os.TempDir(), "social-link-bot")
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
	
	// Download command
	args := []string{
		"--no-playlist",
		"--output", outputTemplate,
		"--format", "best[filesize<50M]/best", // Prefer smaller files, fallback to best
		url,
	}
	
	cmd := exec.Command(yd.BinaryPath, args...)
	
	log.Printf("Executing command: %s %v", yd.BinaryPath, args)
	
	// Execute download
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("Command failed with error: %v", err)
		log.Printf("Command output: %s", string(output))
		return nil, fmt.Errorf("download failed: %v\nOutput: %s", err, string(output))
	}
	
	log.Printf("Download completed successfully")
	
	// Find downloaded files
	downloadedFiles, err := yd.findDownloadedFiles(timestamp)
	if err != nil {
		return nil, fmt.Errorf("failed to find downloaded files: %v", err)
	}
	
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