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
	initDatabase()
	runStartupChecks()
	startCleanupTask()
	defer services.CloseLogger()
	defer services.CloseDatabase()
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
	
	// Set bot commands for Telegram menu
	if err := services.SetBotCommands(); err != nil {
		log.Printf("⚠️ Warning: Failed to set bot commands: %v", err)
	}
	
	// Startup testing removed - tests can be run on-demand via services.NewRealLinkTester().RunQuickTest()
	log.Printf("✅ Startup complete. Use services.NewRealLinkTester().RunQuickTest() for on-demand testing")
}

func loadEnvFile() {
	err := godotenv.Load()

	if err != nil {
		log.Fatal("Error loading .env file")
	}
}

func initDatabase() {
	err := services.InitDatabase()
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
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
