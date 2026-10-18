package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
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

// defaultFacebedHost is the public facebed instance.
const defaultFacebedHost = "www.facebed.com"

// GetFacebedHost returns the facebed-style embed host that reads public
// Facebook posts (Facebook walls the bot's datacenter IP).
func GetFacebedHost() string {
	rawHost := os.Getenv("FACEBED_HOST")
	host := strings.TrimSpace(rawHost)
	result := host

	if host == "" {
		result = defaultFacebedHost
	}

	return result
}

// Gated-post fetch (paid fallback for Instagram posts gated by their author).
const (
	apifyTokenKey                     = "APIFY_TOKEN"
	apifyInstagramActorKey            = "APIFY_INSTAGRAM_ACTOR"
	gatedFetchGlobalMonthlyCapKey     = "GATED_FETCH_GLOBAL_MONTHLY_CAP"
	gatedFetchUserMonthlyAllowKey     = "GATED_FETCH_USER_MONTHLY_ALLOWANCE"
	gatedFetchChatMonthlyCapKey       = "GATED_FETCH_CHAT_MONTHLY_CAP"
	defaultApifyInstagramActor        = "intropix~instagram-posts-reels-scraper"
	defaultGatedFetchGlobalMonthlyCap = GatedFetchNoGlobalCap
	defaultGatedFetchUserMonthlyAllow = 20
	defaultGatedFetchChatMonthlyCap   = 20
)

// GetApifyToken returns the Apify API token; empty turns Apify off.
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
	result := getCapEnv(gatedFetchGlobalMonthlyCapKey, defaultGatedFetchGlobalMonthlyCap)

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

// getCapEnv reads a cap that may be GatedFetchNoGlobalCap (-1, no cap) or
// any value from 0 up; other values keep the default.
func getCapEnv(key string, defaultValue int) int {
	result := defaultValue
	value := os.Getenv(key)
	raw := strings.TrimSpace(value)
	parsed, err := strconv.Atoi(raw)

	if (raw != "") && (err == nil) && (parsed >= GatedFetchNoGlobalCap) {
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

// GatedFetchNoGlobalCap is the bot-wide monthly cap of restricted posts that
// means no cap: fetches are still counted, and only the paid providers'
// dollar budgets stop them. 0 turns the paid fetch off.
const GatedFetchNoGlobalCap = -1

// HikerAPI: the backup paid provider for restricted Instagram posts and
// stories. It is off unless HIKERAPI_TOKEN is set.
const (
	hikerAPITokenKey                  = "HIKERAPI_TOKEN"
	hikerAPIMonthlyBudgetUsdKey       = "HIKERAPI_MONTHLY_BUDGET_USD"
	hikerAPIPricePerRequestUsdKey     = "HIKERAPI_PRICE_PER_REQUEST_USD"
	hikerAPILowBalanceRequestsKey     = "HIKERAPI_LOW_BALANCE_REQUESTS"
	instagramPaidProvidersKey         = "INSTAGRAM_PAID_PROVIDERS"
	defaultHikerAPIMonthlyBudgetUsd   = 1.0
	defaultHikerAPIPricePerRequestUsd = 0.001
	defaultHikerAPILowBalanceRequests = 100
	defaultInstagramPaidProviders     = "apify,hiker"
	instagramPaidProvidersSeparator   = ","
)

// GetHikerAPIToken returns the HikerAPI access key; empty turns HikerAPI off.
func GetHikerAPIToken() string {
	raw := os.Getenv(hikerAPITokenKey)
	result := strings.TrimSpace(raw)

	return result
}

// GetHikerAPIMonthlyBudgetUsd returns the monthly HikerAPI spend limit
// (counted by the bot's own ledger); 0 turns HikerAPI off.
func GetHikerAPIMonthlyBudgetUsd() float64 {
	result := getNonNegativeFloatEnv(hikerAPIMonthlyBudgetUsdKey, defaultHikerAPIMonthlyBudgetUsd)

	return result
}

// GetHikerAPIPricePerRequestUsd returns what one billed HikerAPI request
// costs, for the ledger and the budget check. It is always above 0 (0 or an
// invalid value keeps the default), so the monthly budget always caps spend.
func GetHikerAPIPricePerRequestUsd() float64 {
	result := getPositiveFloatEnv(hikerAPIPricePerRequestUsdKey, defaultHikerAPIPricePerRequestUsd)

	return result
}

// GetHikerAPILowBalanceRequests returns the requests left on the HikerAPI
// account below which the owner is alerted.
func GetHikerAPILowBalanceRequests() int {
	result := getIntEnv(hikerAPILowBalanceRequestsKey, defaultHikerAPILowBalanceRequests)

	return result
}

// GetInstagramPaidProviders returns the paid Instagram providers in the order
// they are tried (lowercase names; unknown ones are left to the caller).
func GetInstagramPaidProviders() []string {
	raw := getStringEnv(instagramPaidProvidersKey, defaultInstagramPaidProviders)
	lowered := strings.ToLower(raw)
	parts := strings.Split(lowered, instagramPaidProvidersSeparator)
	result := make([]string, 0, len(parts))

	for _, part := range parts {
		name := strings.TrimSpace(part)

		if name != "" {
			result = append(result, name)
		}
	}

	return result
}

// getPositiveFloatEnv parses a float env variable above 0, falling back to
// defaultValue when it is unset, invalid, 0 or negative.
func getPositiveFloatEnv(key string, defaultValue float64) float64 {
	result := defaultValue
	parsed := getNonNegativeFloatEnv(key, defaultValue)

	if parsed > 0 {
		result = parsed
	}

	return result
}

// getNonNegativeFloatEnv parses a float env variable of 0 or more, falling
// back to defaultValue when it is unset or invalid.
func getNonNegativeFloatEnv(key string, defaultValue float64) float64 {
	result := defaultValue
	value := os.Getenv(key)
	raw := strings.TrimSpace(value)
	parsed, err := strconv.ParseFloat(raw, 64)

	if (raw != "") && (err == nil) && (parsed >= 0) {
		result = parsed
	}

	return result
}

// Home relay: a loopback HTTP endpoint (an SSH tunnel to a home machine)
// that downloads videos for the platforms that wall the server's IP. It is
// off unless RELAY_URL, RELAY_TOKEN_FILE and RELAY_PLATFORMS are all set.
const (
	relayURLKey                = "RELAY_URL"
	relayTokenFileKey          = "RELAY_TOKEN_FILE"
	relayPlatformsKey          = "RELAY_PLATFORMS"
	relayTimeoutSecondsKey     = "RELAY_TIMEOUT_SECONDS"
	defaultRelayTimeoutSeconds = 45
	relayPlatformsSeparator    = ","
	relayURLScheme             = "http"
	relayURLRootPath           = "/"
)

// errRelayURLNotLoopback refuses a relay URL that could reach another host.
var errRelayURLNotLoopback = errors.New("RELAY_URL must be http://<loopback IP>[:port] without a path")

// GetRelayURL returns the relay base URL without a trailing slash, "" when
// RELAY_URL is unset, and an error (with "") for a URL that is not plain
// http on a loopback IP: a typo must never send ids and the token to a
// public host.
func GetRelayURL() (string, error) {
	result := ""
	raw := getStringEnv(relayURLKey, "")
	var err error

	if raw != "" {
		result, err = checkRelayURL(raw)
	}

	return result, err
}

// checkRelayURL accepts http://<loopback IP>[:port] with an empty or "/"
// path and nothing else (no user, query or fragment, no host name).
func checkRelayURL(raw string) (string, error) {
	result := ""
	parsed, err := url.Parse(raw)
	isLoopback := false

	if err == nil {
		hostname := parsed.Hostname()
		ip := net.ParseIP(hostname)
		isLoopback = (ip != nil) && (ip.IsLoopback() == true)
	}

	isPlain := (err == nil) && (parsed.Scheme == relayURLScheme) && (parsed.User == nil)
	isPlain = isPlain && (parsed.RawQuery == "") && (parsed.Fragment == "") && (parsed.Opaque == "")
	hasNoPath := (err == nil) && ((parsed.Path == "") || (parsed.Path == relayURLRootPath))
	isAccepted := (isPlain == true) && (isLoopback == true) && (hasNoPath == true)

	if isAccepted == true {
		result = fmt.Sprintf("%s://%s", parsed.Scheme, parsed.Host)
	}

	if isAccepted == false {
		err = errRelayURLNotLoopback
	}

	return result, err
}

// GetRelayTokenFile returns the path of the file that holds the relay's
// bearer token; empty turns the relay off.
func GetRelayTokenFile() string {
	result := getStringEnv(relayTokenFileKey, "")

	return result
}

// GetRelayPlatforms returns the platforms (lower case) the bot sends to the
// relay; none by default.
func GetRelayPlatforms() []string {
	raw := getStringEnv(relayPlatformsKey, "")
	lowered := strings.ToLower(raw)
	parts := strings.Split(lowered, relayPlatformsSeparator)
	result := make([]string, 0, len(parts))

	for _, part := range parts {
		name := strings.TrimSpace(part)

		if name != "" {
			result = append(result, name)
		}
	}

	return result
}

// IsRelayConfigured reports whether the relay has a valid loopback URL and
// a token file.
func IsRelayConfigured() bool {
	relayURL, err := GetRelayURL()
	tokenFile := GetRelayTokenFile()
	result := (err == nil) && (relayURL != "") && (tokenFile != "")

	return result
}

// IsRelayEnabledFor reports whether the relay is configured (a loopback
// URL and a token file) and switched on for platform.
func IsRelayEnabledFor(platform string) bool {
	platforms := GetRelayPlatforms()
	isConfigured := IsRelayConfigured()
	result := (isConfigured == true) && (slices.Contains(platforms, platform) == true)

	return result
}

// MinRelayTimeoutSeconds is the shortest relay call that can reach the
// relay at all: its 10 s minimum deadline plus the 5 s transfer margin.
const MinRelayTimeoutSeconds = 15

// RelayTimeoutSeconds is RELAY_TIMEOUT_SECONDS as configured (Raw: the
// default when unset, 0 or invalid) and as used (Clamped: at least
// MinRelayTimeoutSeconds).
type RelayTimeoutSeconds struct {
	Raw     int
	Clamped int
}

// IsRaised reports a configured value below MinRelayTimeoutSeconds that
// was raised to it.
func (t RelayTimeoutSeconds) IsRaised() bool {
	result := (t.Clamped != t.Raw)

	return result
}

// GetRelayTimeoutSeconds returns the longest time one relay call may take
// (default 45 s, at least MinRelayTimeoutSeconds), raw and clamped; the
// chain's remaining time can shorten it.
func GetRelayTimeoutSeconds() RelayTimeoutSeconds {
	raw := getIntEnv(relayTimeoutSecondsKey, defaultRelayTimeoutSeconds)

	if raw == 0 {
		raw = defaultRelayTimeoutSeconds
	}

	clamped := max(raw, MinRelayTimeoutSeconds)
	result := RelayTimeoutSeconds{Raw: raw, Clamped: clamped}

	return result
}
