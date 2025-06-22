package config

import (
	"github.com/joho/godotenv"
	"log"
	"math/rand"
	"os"
	"strings"
	"time"
)

const tgBotTokenKey = "TG_BOT_TOKEN"

func init() {
	loadConfig()
}

func loadConfig() {
	godotenv.Load()
}

func GetTgBotToken() string {
	return os.Getenv(tgBotTokenKey)
}

// Social media authentication configuration
func GetInstagramUsername() string {
	return os.Getenv("INSTAGRAM_USERNAME")
}

func GetInstagramPassword() string {
	return os.Getenv("INSTAGRAM_PASSWORD")
}

func GetTikTokUsername() string {
	return os.Getenv("TIKTOK_USERNAME")
}

func GetTikTokPassword() string {
	return os.Getenv("TIKTOK_PASSWORD")
}

func GetTwitterUsername() string {
	return os.Getenv("TWITTER_USERNAME")
}

func GetTwitterPassword() string {
	return os.Getenv("TWITTER_PASSWORD")
}

// 9GAG authentication configuration
func GetNineGagUsername() string {
	return os.Getenv("NINEGAG_USERNAME")
}

func GetNineGagPassword() string {
	return os.Getenv("NINEGAG_PASSWORD")
}

// Unified proxy configuration for all platforms except YouTube
// YouTube uses direct connection with server-IP authenticated cookies
func GetYtDlpProxies() []string {
	proxiesStr := os.Getenv("YTDLP_PROXIES")
	if proxiesStr == "" {
		return []string{}
	}
	
	// Split by comma and trim spaces
	var proxies []string
	for _, proxy := range strings.Split(proxiesStr, ",") {
		proxy = strings.TrimSpace(proxy)
		if proxy != "" {
			proxies = append(proxies, proxy)
		}
	}
	
	return proxies
}

// Get YouTube download delay in seconds (rate limiting)
func GetYouTubeDelay() string {
	delay := os.Getenv("YOUTUBE_DELAY")
	if delay == "" {
		return "5" // Default 5 second delay
	}
	return delay
}
