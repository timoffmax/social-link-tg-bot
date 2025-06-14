package app

import (
	"github.com/joho/godotenv"
	"github.com/timoffmax/social-link-bot/services"
	"log"
	"time"
)

func Run() {
	initApp()
	startCleanupTask()
	listenForUpdates()
}

func initApp() {
	loadEnvFile()
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
