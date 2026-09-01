package services

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"
)

// Logger provides centralized logging for the bot
type Logger struct {
	file      *os.File
	logWriter io.Writer
}

var globalLogger *Logger

// InitLogger sets up file logging
func InitLogger(logDir string) error {
	// Create logs directory if it doesn't exist
	err := os.MkdirAll(logDir, 0755)
	if err != nil {
		return fmt.Errorf("failed to create log directory: %v", err)
	}

	// Create log file with timestamp
	logFileName := fmt.Sprintf("bot_%s.log", time.Now().Format("2006-01-02"))
	logFilePath := filepath.Join(logDir, logFileName)

	// Open log file (create or append)
	logFile, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		return fmt.Errorf("failed to open log file: %v", err)
	}

	// Create multi-writer for both console and file
	multiWriter := io.MultiWriter(os.Stdout, logFile)

	// Set up global logger
	globalLogger = &Logger{
		file:      logFile,
		logWriter: multiWriter,
	}

	// Configure Go's standard log package to use our multi-writer
	log.SetOutput(multiWriter)
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	log.Printf("📝 Logging initialized - writing to: %s", logFilePath)

	return nil
}

// CloseLogger closes the log file
func CloseLogger() {
	if globalLogger != nil && globalLogger.file != nil {
		log.Printf("📝 Closing log file")
		globalLogger.file.Close()
	}
}

// GetLogger returns the global logger instance
func GetLogger() *Logger {
	return globalLogger
}

// LogExtraction logs media extraction attempts
func LogExtraction(platform, url, method string, success bool, mediaCount int, duration time.Duration) {
	status := "✅ SUCCESS"
	if !success {
		status = "❌ FAILED"
	}

	log.Printf("🔍 [%s] %s | %s | %s | %d media | %v",
		platform, method, status, url, mediaCount, duration)
}

// LogDownload logs file downloads
func LogDownload(platform, filename, mediaType string, success bool, fileSize int64) {
	status := "✅ SUCCESS"
	if !success {
		status = "❌ FAILED"
	}

	log.Printf("⬇️ [%s] %s | %s | %s | %d bytes",
		platform, status, mediaType, filename, fileSize)
}

// LogTelegramMessage logs message sending attempts
func LogTelegramMessage(chatID int64, messageType string, success bool, error string) {
	status := "✅ SENT"
	if !success {
		status = "❌ FAILED"
	}

	if error != "" {
		log.Printf("📤 [TELEGRAM] %s | Chat %d | %s | Error: %s",
			status, chatID, messageType, error)
	} else {
		log.Printf("📤 [TELEGRAM] %s | Chat %d | %s",
			status, chatID, messageType)
	}
}

// LogError logs errors with context
func LogError(component, operation string, err error) {
	log.Printf("💥 [ERROR] %s.%s: %v", component, operation, err)
}

// LogDebug logs debug information (only when debug mode is enabled)
func LogDebug(component, message string) {
	// For now, always log debug info. In the future, this could be controlled by an env var
	log.Printf("🐛 [DEBUG] %s: %s", component, message)
}

// LogStats logs bot statistics
func LogStats(processed, successful, failed int, uptime time.Duration) {
	log.Printf("📊 [STATS] Processed: %d | Success: %d | Failed: %d | Uptime: %v",
		processed, successful, failed, uptime)
}
