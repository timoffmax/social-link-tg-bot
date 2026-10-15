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
	case "9gag":
		return getBoolEnv("ENABLE_9GAG_PROXY", true)
	case "facebook":
		return getBoolEnv("ENABLE_FACEBOOK_PROXY", true)
	case "reddit":
		return getBoolEnv("ENABLE_REDDIT_PROXY", true)
	default:
		return true // Default to enabled for unknown platforms
	}
}

// Unified proxy configuration for all platforms except YouTube
// YouTube uses a direct connection unless ENABLE_YOUTUBE_PROXY is set
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

// IsThreadsEnabled reports whether Threads links are processed.
func IsThreadsEnabled() bool {
	result := getBoolEnv("ENABLE_THREADS", true)

	return result
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

// Instagram service configuration
// Returns the service name (without .com) to use for Instagram preview links.
// Recognizes: toinstagram, onebigram. Dead aliases (kksave, kkinstagram,
// instagramez, ddinstagram) are migrated to toinstagram.
func GetInstagramService() string {
	service := strings.ToLower(strings.TrimSpace(os.Getenv("INSTAGRAM_SERVICE")))

	switch service {
	case "toinstagram", "onebigram":
		return service
	}

	return "toinstagram"
}

// GetAdminUserID returns the Telegram user ID of the bot admin
func GetAdminUserID() int64 {
	idStr := os.Getenv("ADMIN_USER_ID")
	if idStr == "" {
		return 0
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return 0
	}
	return id
}

// GetKKInstagramHost returns the kkinstagram-style redirector host used to
// resolve Instagram media via a 302 to the Instagram CDN.
func GetKKInstagramHost() string {
	host := strings.TrimSpace(os.Getenv("KKINSTAGRAM_HOST"))
	if host == "" {
		return "kkinstagram.com"
	}

	return host
}

// GetKKTikTokHost returns the kktiktok-style redirector host used to resolve
// TikTok videos via a 302 to the TikTok CDN.
func GetKKTikTokHost() string {
	host := strings.TrimSpace(os.Getenv("KKTIKTOK_HOST"))
	if host == "" {
		return "kktiktok.com"
	}

	return host
}

// GetVxredditHost returns the vxreddit-style frontend host used to resolve
// Reddit media (video re-served from its own CDN) from datacenter IPs.
func GetVxredditHost() string {
	host := strings.TrimSpace(os.Getenv("VXREDDIT_HOST"))
	if host == "" {
		return "vxreddit.com"
	}

	return host
}

// Gated-post fetch (paid fallback for Instagram posts gated by their author).
const (
	apifyTokenKey                     = "APIFY_TOKEN"
	apifyInstagramActorKey            = "APIFY_INSTAGRAM_ACTOR"
	gatedFetchGlobalMonthlyCapKey     = "GATED_FETCH_GLOBAL_MONTHLY_CAP"
	gatedFetchUserMonthlyAllowKey     = "GATED_FETCH_USER_MONTHLY_ALLOWANCE"
	gatedFetchChatMonthlyCapKey       = "GATED_FETCH_CHAT_MONTHLY_CAP"
	defaultApifyInstagramActor        = "intropix~instagram-posts-reels-scraper"
	defaultGatedFetchGlobalMonthlyCap = 100
	defaultGatedFetchUserMonthlyAllow = 20
	defaultGatedFetchChatMonthlyCap   = 20
)

// GetApifyToken returns the Apify API token; empty disables the gated-post fetch.
func GetApifyToken() string {
	raw := os.Getenv(apifyTokenKey)
	result := strings.TrimSpace(raw)

	return result
}

// GetApifyInstagramActor returns the Apify actor id ("user~name") that fetches
// a single Instagram post by URL.
func GetApifyInstagramActor() string {
	raw := os.Getenv(apifyInstagramActorKey)
	result := strings.TrimSpace(raw)

	if result == "" {
		result = defaultApifyInstagramActor
	}

	return result
}

// GetGatedFetchGlobalMonthlyCap returns the bot-wide monthly cap of paid
// gated-post fetches; 0 disables the feature.
func GetGatedFetchGlobalMonthlyCap() int {
	result := getIntEnv(gatedFetchGlobalMonthlyCapKey, defaultGatedFetchGlobalMonthlyCap)

	return result
}

// GetGatedFetchUserMonthlyAllowance returns the default monthly allowance of
// gated-post fetches per user in private chats; 0 disables private use.
func GetGatedFetchUserMonthlyAllowance() int {
	result := getIntEnv(gatedFetchUserMonthlyAllowKey, defaultGatedFetchUserMonthlyAllow)

	return result
}

// GetGatedFetchChatMonthlyCap returns the default monthly cap of gated-post
// fetches for a group chat the owner enabled.
func GetGatedFetchChatMonthlyCap() int {
	result := getIntEnv(gatedFetchChatMonthlyCapKey, defaultGatedFetchChatMonthlyCap)

	return result
}

// Optional Telegram Stars "support the developer" payments.
const (
	starsSupportEnabledKey     = "STARS_SUPPORT_ENABLED"
	defaultStarsSupportEnabled = false
)

// IsStarsSupportEnabled reports whether /support sells Telegram Stars support
// tiers; off by default.
func IsStarsSupportEnabled() bool {
	result := getBoolEnv(starsSupportEnabledKey, defaultStarsSupportEnabled)

	return result
}

// Apify budget and per-platform actors.
const (
	apifyMonthlyBudgetUsdKey     = "APIFY_MONTHLY_BUDGET_USD"
	apifyYouTubeActorKey         = "APIFY_YOUTUBE_ACTOR"
	apifyStoriesActorKey         = "APIFY_STORIES_ACTOR"
	defaultApifyMonthlyBudgetUsd = 4.0
	defaultApifyYouTubeActor     = "marielise.dev~youtube-video-downloader"
	defaultApifyStoriesActor     = "goat255~instagram-stories-highlights-scraper"
)

// GetApifyMonthlyBudgetUsd returns the monthly Apify spend limit; a run that
// could exceed it is refused (the free plan has $5 of credit).
func GetApifyMonthlyBudgetUsd() float64 {
	result := defaultApifyMonthlyBudgetUsd
	value := os.Getenv(apifyMonthlyBudgetUsdKey)
	raw := strings.TrimSpace(value)
	parsed, err := strconv.ParseFloat(raw, 64)

	if (raw != "") && (err == nil) && (parsed >= 0) {
		result = parsed
	}

	return result
}

// GetApifyYouTubeActor returns the Apify actor that downloads YouTube videos.
func GetApifyYouTubeActor() string {
	result := getStringEnv(apifyYouTubeActorKey, defaultApifyYouTubeActor)

	return result
}

// GetApifyStoriesActor returns the Apify actor that scrapes Instagram stories.
func GetApifyStoriesActor() string {
	result := getStringEnv(apifyStoriesActorKey, defaultApifyStoriesActor)

	return result
}

// getStringEnv returns a trimmed env variable, or defaultValue when unset.
func getStringEnv(key, defaultValue string) string {
	value := os.Getenv(key)
	result := strings.TrimSpace(value)

	if result == "" {
		result = defaultValue
	}

	return result
}

// getIntEnv parses a non-negative integer env variable, falling back to
// defaultValue when it is unset or invalid.
func getIntEnv(key string, defaultValue int) int {
	result := defaultValue
	value := os.Getenv(key)
	raw := strings.TrimSpace(value)
	parsed, err := strconv.Atoi(raw)

	if (raw != "") && (err == nil) && (parsed >= 0) {
		result = parsed
	}

	return result
}

// Helper function to parse boolean environment variables
func getBoolEnv(key string, defaultValue bool) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if value == "" {
		return defaultValue
	}
	return value == "true" || value == "1" || value == "yes" || value == "on"
}
