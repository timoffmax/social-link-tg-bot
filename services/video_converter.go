package services

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type VideoConverter struct {
	tempDir    string
	workingDir string
	ffmpegPath string
}

func NewVideoConverter(workingDir, tempDir string) *VideoConverter {
	vc := &VideoConverter{
		tempDir:    tempDir,
		workingDir: workingDir,
	}
	
	// Try to find ffmpeg
	vc.ffmpegPath = vc.findFfmpeg()
	
	if vc.ffmpegPath != "" {
		log.Printf("🎬 VideoConverter initialized with ffmpeg at: %s", vc.ffmpegPath)
	} else {
		log.Printf("⚠️ VideoConverter initialized without ffmpeg - conversion not available")
	}
	
	return vc
}

func (vc *VideoConverter) findFfmpeg() string {
	// Try different possible ffmpeg locations
	possiblePaths := []string{
		"ffmpeg",                                    // System PATH
		"/usr/bin/ffmpeg",                          // Standard system location
		"/usr/local/bin/ffmpeg",                    // Local installation
		"/opt/ffmpeg/bin/ffmpeg",                   // Custom installation
		filepath.Join(vc.workingDir, "bin/ffmpeg"), // Local bin directory
		"/snap/bin/ffmpeg",                         // Snap installation
	}
	
	for _, path := range possiblePaths {
		if vc.testFfmpeg(path) {
			return path
		}
	}
	
	return ""
}

func (vc *VideoConverter) testFfmpeg(path string) bool {
	cmd := exec.Command(path, "-version")
	err := cmd.Run()
	return err == nil
}

func (vc *VideoConverter) IsAvailable() bool {
	return vc.ffmpegPath != ""
}

// ConvertWebMToMP4 converts a WebM video file to MP4 format
func (vc *VideoConverter) ConvertWebMToMP4(webmPath string) (string, error) {
	if !vc.IsAvailable() {
		return "", fmt.Errorf("ffmpeg not available for conversion")
	}
	
	// Ensure temp directory exists
	if err := os.MkdirAll(vc.tempDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create temp directory: %v", err)
	}
	
	// Create output filename
	timestamp := time.Now().Unix()
	baseName := strings.TrimSuffix(filepath.Base(webmPath), filepath.Ext(webmPath))
	mp4Path := filepath.Join(vc.tempDir, fmt.Sprintf("%d_%s_converted.mp4", timestamp, baseName))
	
	log.Printf("🎬 Converting WebM to MP4: %s -> %s", webmPath, mp4Path)
	
	// Build ffmpeg command for WebM to MP4 conversion
	// Use libx264 codec for best compatibility with Telegram
	args := []string{
		"-i", webmPath,                    // Input file
		"-c:v", "libx264",                 // Video codec: H.264 for compatibility
		"-c:a", "aac",                     // Audio codec: AAC for compatibility
		"-preset", "fast",                 // Encoding preset (balance between speed and compression)
		"-crf", "23",                      // Constant Rate Factor (good quality)
		"-maxrate", "2M",                  // Max bitrate for Telegram compatibility
		"-bufsize", "4M",                  // Buffer size
		"-movflags", "+faststart",         // Optimize for web/streaming playback
		"-avoid_negative_ts", "make_zero", // Fix timestamp issues
		"-y",                              // Overwrite output file if exists
		mp4Path,                           // Output file
	}
	
	cmd := exec.Command(vc.ffmpegPath, args...)
	
	// Log the command (with safe args)
	log.Printf("🎬 Executing: %s %s", vc.ffmpegPath, strings.Join(args, " "))
	
	// Set timeout for conversion (2 minutes should be enough for most videos)
	cmd.Dir = vc.workingDir
	
	// Run the conversion
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("❌ ffmpeg conversion failed: %v", err)
		log.Printf("ffmpeg output: %s", string(output))
		
		// Clean up failed conversion file
		os.Remove(mp4Path)
		return "", fmt.Errorf("video conversion failed: %v", err)
	}
	
	// Check if output file was created and has content
	if stat, err := os.Stat(mp4Path); err != nil || stat.Size() == 0 {
		log.Printf("❌ Conversion output file is missing or empty: %s", mp4Path)
		os.Remove(mp4Path)
		return "", fmt.Errorf("conversion produced empty or missing file")
	}
	
	log.Printf("✅ Successfully converted WebM to MP4: %s (%d bytes)", mp4Path, func() int64 {
		if stat, err := os.Stat(mp4Path); err == nil {
			return stat.Size()
		}
		return 0
	}())
	
	return mp4Path, nil
}

// ConvertVideoToMP4 converts any supported video format to MP4
func (vc *VideoConverter) ConvertVideoToMP4(inputPath string) (string, error) {
	if !vc.IsAvailable() {
		return "", fmt.Errorf("ffmpeg not available for conversion")
	}
	
	ext := strings.ToLower(filepath.Ext(inputPath))
	
	// Only convert if it's not already MP4
	if ext == ".mp4" {
		log.Printf("📹 File is already MP4, no conversion needed: %s", inputPath)
		return inputPath, nil
	}
	
	// For now, focus on WebM conversion, but this can be extended
	switch ext {
	case ".webm":
		return vc.ConvertWebMToMP4(inputPath)
	default:
		return "", fmt.Errorf("conversion not supported for format: %s", ext)
	}
}

// CleanupFile removes a temporary file
func (vc *VideoConverter) CleanupFile(filePath string) error {
	if filePath != "" {
		return os.Remove(filePath)
	}
	return nil
}