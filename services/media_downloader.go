package services

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type MediaInfo struct {
	Type        string // "photo", "video", "document"
	URL         string
	Caption     string
	Filename    string
	ContentType string
}

type MediaDownloader struct {
	TempDir string
}

func NewMediaDownloader() *MediaDownloader {
	tempDir := filepath.Join(os.TempDir(), "social-link-bot")
	os.MkdirAll(tempDir, 0755)
	
	return &MediaDownloader{
		TempDir: tempDir,
	}
}

func (md *MediaDownloader) DownloadInstagramMedia(url string) ([]*MediaInfo, error) {
	// For now, we'll implement a basic Instagram media extraction
	// In production, you'd want to use a library like yt-dlp or gallery-dl
	
	mediaInfos := []*MediaInfo{}
	
	// Extract post ID from URL
	postID, err := md.extractInstagramPostID(url)
	if err != nil {
		return nil, fmt.Errorf("failed to extract Instagram post ID: %v", err)
	}
	
	// Use Instagram's basic API (this is a simplified approach)
	// In reality, you'd need to use more sophisticated methods
	mediaURL, mediaType, err := md.getInstagramMediaURL(postID)
	if err != nil {
		return nil, fmt.Errorf("failed to get Instagram media URL: %v", err)
	}
	
	// Download the media
	filename := fmt.Sprintf("instagram_%s_%d.%s", postID, time.Now().Unix(), md.getFileExtension(mediaType))
	filePath := filepath.Join(md.TempDir, filename)
	
	err = md.downloadFile(mediaURL, filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to download Instagram media: %v", err)
	}
	
	mediaInfo := &MediaInfo{
		Type:        md.getMediaType(mediaType),
		URL:         mediaURL,
		Caption:     "",
		Filename:    filePath,
		ContentType: mediaType,
	}
	
	mediaInfos = append(mediaInfos, mediaInfo)
	
	return mediaInfos, nil
}

func (md *MediaDownloader) extractInstagramPostID(url string) (string, error) {
	// Extract post ID from Instagram URL
	re := regexp.MustCompile(`/p/([A-Za-z0-9_-]+)`)
	matches := re.FindStringSubmatch(url)
	
	if len(matches) < 2 {
		return "", fmt.Errorf("invalid Instagram URL format")
	}
	
	return matches[1], nil
}

func (md *MediaDownloader) getInstagramMediaURL(postID string) (string, string, error) {
	// This is a placeholder implementation
	// In a real implementation, you would:
	// 1. Use Instagram's API or scraping methods
	// 2. Handle authentication and rate limiting
	// 3. Parse JSON responses to extract media URLs
	
	// For now, return an error as this needs proper implementation
	return "", "", fmt.Errorf("Instagram media extraction not fully implemented - requires proper API integration")
}

func (md *MediaDownloader) downloadFile(url, filepath string) error {
	resp, err := SharedDownloadClient.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad HTTP status: %s", resp.Status)
	}
	
	// Create file
	file, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer file.Close()
	
	// Copy data
	_, err = io.Copy(file, resp.Body)
	return err
}

func (md *MediaDownloader) getFileExtension(contentType string) string {
	switch contentType {
	case "image/jpeg":
		return "jpg"
	case "image/png":
		return "png"
	case "image/gif":
		return "gif"
	case "video/mp4":
		return "mp4"
	case "video/quicktime":
		return "mov"
	default:
		return "bin"
	}
}

func (md *MediaDownloader) getMediaType(contentType string) string {
	if strings.HasPrefix(contentType, "image/") {
		return "photo"
	} else if strings.HasPrefix(contentType, "video/") {
		return "video"
	}
	return "document"
}

func (md *MediaDownloader) CleanupFile(filepath string) error {
	return os.Remove(filepath)
}

func (md *MediaDownloader) CleanupTempFiles() error {
	// Clean up files older than 1 hour
	cutoff := time.Now().Add(-1 * time.Hour)
	
	return filepath.Walk(md.TempDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Continue walking
		}
		
		if !info.IsDir() && info.ModTime().Before(cutoff) {
			os.Remove(path)
		}
		
		return nil
	})
}