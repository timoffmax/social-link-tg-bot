package app

import (
	"context"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"github.com/timoffmax/social-link-bot/services"
)

func Run() {
	initApp()
	initLogging()
	initDatabase()
	runStartupChecks()
	startCleanupTask()
	startHealthMonitor()

	defer services.CloseLogger()
	defer func() {
		if services.IsDBInitialized() {
			services.CloseDatabase()
		}
	}()

	// Create a cancellable context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())

	// Listen for OS signals to trigger graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigCh
		log.Printf("🛑 Received signal %v, initiating graceful shutdown...", sig)
		cancel()
	}()

	listenForUpdates(ctx)
	log.Printf("👋 Bot stopped gracefully")
}

func initApp() {
	loadEnvFile()
}

func initLogging() {
	workingDir, err := os.Getwd()
	if err != nil {
		log.Printf("Warning: Could not get working directory: %v", err)
		workingDir = "."
	}

	logDir := filepath.Join(workingDir, "logs")

	err = services.InitLogger(logDir)
	if err != nil {
		log.Printf("Warning: Could not initialize file logging: %v", err)
		log.Printf("Continuing with console-only logging...")
	}
}

func runStartupChecks() {
	checker := services.NewStartupChecker()
	checker.RunStartupChecks()

	if err := services.SetBotCommands(); err != nil {
		log.Printf("⚠️ Warning: Failed to set bot commands: %v", err)
	}

	log.Printf("✅ Startup complete")
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
		log.Printf("⚠️  Database initialization failed: %v", err)
		log.Printf("📌 Bot will continue without database features (stats, limits)")
	} else {
		log.Printf("✅ Database initialized successfully")
	}
}

func startCleanupTask() {
	downloader := services.NewYtDlpDownloader()

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

// startHealthMonitor periodically probes external dependencies (kk redirector
// services, proxy pool) so silent degradation is surfaced in logs and via
// admin alerts instead of going unnoticed until users complain.
func startHealthMonitor() {
	go func() {
		services.CheckProxyHealth()
		services.CheckRedirectorHealth()

		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				services.CheckProxyHealth()
				services.CheckRedirectorHealth()
			}
		}
	}()

	log.Println("Started health monitor task")
}
