package config

import (
	"github.com/joho/godotenv"
	"os"
	"strings"
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

// Proxy configuration
func GetYtDlpProxy() string {
	return os.Getenv("YTDLP_PROXY")
}

// Get list of proxies from YTDLP_PROXIES environment variable
// Format: proxy1,proxy2,proxy3 (comma-separated)
func GetYtDlpProxies() []string {
	proxiesStr := os.Getenv("YTDLP_PROXIES")
	if proxiesStr == "" {
		// Fallback to single proxy if available
		if proxy := GetYtDlpProxy(); proxy != "" {
			return []string{proxy}
		}
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
