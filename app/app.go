package app

import (
	"github.com/joho/godotenv"
	"github.com/timoffmax/social-link-bot/services"
	"log"
	"os"
	"path/filepath"
	"time"
)

func Run() {
	initApp()
	initLogging()
	runStartupChecks()
	startCleanupTask()
	defer services.CloseLogger()
	listenForUpdates()
}

func initApp() {
	loadEnvFile()
}

func initLogging() {
	// Get current working directory
	workingDir, err := os.Getwd()
	if err != nil {
		log.Printf("Warning: Could not get working directory: %v", err)
		workingDir = "."
	}
	
	// Create logs directory path
	logDir := filepath.Join(workingDir, "logs")
	
	// Initialize file logging
	err = services.InitLogger(logDir)
	if err != nil {
		log.Printf("Warning: Could not initialize file logging: %v", err)
		log.Printf("Continuing with console-only logging...")
	}
}

func runStartupChecks() {
	checker := services.NewStartupChecker()
	checker.RunStartupChecks()
	
	// Run a quick functional test
	log.Printf("\n🧪 Running quick functional test...")
	tester := services.NewRealLinkTester()
	tester.RunQuickTest()
}

func loadEnvFile() {
	err := godotenv.Load()

	if err != nil {
		log.Fatal("Error loading .env file")
	}
}

func startCleanupTask() {
	downloader := services.NewYtDlpDownloader()
	
	// Start a goroutine to clean up temp files every hour
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		
		for {
			select {
			case <-ticker.C:
				err := downloader.CleanupTempFiles()
				if err != nil {
					log.Printf("Error cleaning up temp files: %v", err)
				} else {
					log.Println("Cleaned up temporary files")
				}
			}
		}
	}()
	
	log.Println("Started cleanup task")
}
