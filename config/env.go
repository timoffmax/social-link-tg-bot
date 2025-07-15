package config

import (
	"github.com/joho/godotenv"
	"os"
	"strconv"
	"strings"
)

const tgBotTokenKey = "TG_BOT_TOKEN"

// Database configuration
const (
	dbHostKey     = "DB_HOST"
	dbPortKey     = "DB_PORT"
	dbNameKey     = "DB_NAME"
	dbUserKey     = "DB_USER"
	dbPasswordKey = "DB_PASSWORD"
	dbSSLModeKey  = "DB_SSLMODE"
)

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

// Database configuration functions
func GetDBHost() string {
	if host := os.Getenv(dbHostKey); host != "" {
		return host
	}
	return "localhost"
}

func GetDBPort() string {
	if port := os.Getenv(dbPortKey); port != "" {
		return port
	}
	return "5432"
}

func GetDBName() string {
	if name := os.Getenv(dbNameKey); name != "" {
		return name
	}
	return "social_link_bot"
}

func GetDBUser() string {
	if user := os.Getenv(dbUserKey); user != "" {
		return user
	}
	return "postgres"
}

func GetDBPassword() string {
	return os.Getenv(dbPasswordKey)
}

func GetDBSSLMode() string {
	if sslMode := os.Getenv(dbSSLModeKey); sslMode != "" {
		return sslMode
	}
	return "disable"
}

// Media tracking configuration
func GetDefaultDailyMediaLimit() int {
	if limit := os.Getenv("DEFAULT_DAILY_MEDIA_LIMIT"); limit != "" {
		// Parse the limit, default to 0 if invalid
		if parsed, err := strconv.Atoi(limit); err == nil {
			return parsed
		}
	}
	return 0 // 0 means no limit
}

// Helper function to parse boolean environment variables
func getBoolEnv(key string, defaultValue bool) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if value == "" {
		return defaultValue
	}
	return value == "true" || value == "1" || value == "yes" || value == "on"
}
