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

// LogExtraction logs media extraction attempts
func LogExtraction(platform, url, method string, success bool, mediaCount int, duration time.Duration) {
	status := "✅ SUCCESS"
	if !success {
		status = "❌ FAILED"
	}

	log.Printf("🔍 [%s] %s | %s | %s | %d media | %v",
		platform, method, status, url, mediaCount, duration)
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
