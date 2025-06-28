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

// Proxy configuration with global and per-platform disable options
func IsProxyEnabled() bool {
	return getBoolEnv("ENABLE_PROXIES", true)
}

func IsProxyEnabledForPlatform(platform string) bool {
	// Check global proxy setting first
	if !IsProxyEnabled() {
		return false
	}
	
	// Check platform-specific proxy settings
	switch platform {
	case "youtube":
		return getBoolEnv("ENABLE_YOUTUBE_PROXY", false) // YouTube defaults to no proxy
	case "twitter":
		return getBoolEnv("ENABLE_TWITTER_PROXY", true)
	case "tiktok":
		return getBoolEnv("ENABLE_TIKTOK_PROXY", true)
	case "instagram":
		return getBoolEnv("ENABLE_INSTAGRAM_PROXY", true)
	case "facebook":
		return getBoolEnv("ENABLE_FACEBOOK_PROXY", true)
	case "reddit":
		return getBoolEnv("ENABLE_REDDIT_PROXY", true)
	case "9gag":
		return getBoolEnv("ENABLE_9GAG_PROXY", true)
	default:
		return true // Default to enabled for unknown platforms
	}
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

// Platform enablement configuration
// Returns true if platform is enabled, false if disabled
func IsYouTubeEnabled() bool {
	return getBoolEnv("ENABLE_YOUTUBE", true)
}

func IsTwitterEnabled() bool {
	return getBoolEnv("ENABLE_TWITTER", true)
}

func IsTikTokEnabled() bool {
	return getBoolEnv("ENABLE_TIKTOK", true)
}

func IsInstagramEnabled() bool {
	return getBoolEnv("ENABLE_INSTAGRAM", true)
}

func IsFacebookEnabled() bool {
	return getBoolEnv("ENABLE_FACEBOOK", true)
}

func IsRedditEnabled() bool {
	return getBoolEnv("ENABLE_REDDIT", true)
}

func Is9GAGEnabled() bool {
	return getBoolEnv("ENABLE_9GAG", true)
}

// Cookie usage configuration per platform
func IsCookiesEnabledForPlatform(platform string) bool {
	switch platform {
	case "youtube":
		return getBoolEnv("ENABLE_YOUTUBE_COOKIES", true)
	case "twitter":
		return getBoolEnv("ENABLE_TWITTER_COOKIES", true)
	case "tiktok":
		return getBoolEnv("ENABLE_TIKTOK_COOKIES", true)
	case "instagram":
		return getBoolEnv("ENABLE_INSTAGRAM_COOKIES", false) // Default to false for Instagram
	case "facebook":
		return getBoolEnv("ENABLE_FACEBOOK_COOKIES", true)
	case "reddit":
		return getBoolEnv("ENABLE_REDDIT_COOKIES", true)
	case "9gag":
		return getBoolEnv("ENABLE_9GAG_COOKIES", true)
	default:
		return true // Default to enabled for unknown platforms
	}
}

// Helper function to parse boolean environment variables
func getBoolEnv(key string, defaultValue bool) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if value == "" {
		return defaultValue
	}
	return value == "true" || value == "1" || value == "yes" || value == "on"
}
