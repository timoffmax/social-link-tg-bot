package config

// HEAD (406ba21) copies of the .env getters, verbatim except for the "head"
// prefix and one `!x` written as `x == false` (same logic): the
// no-behaviour-change matrix compares every rewired getter with them. Do not
// edit them to follow the code; they are the baseline.

import (
	"os"
	"strconv"
	"strings"
)

const (
	headDefaultFacebedHost                = "www.facebed.com"
	headDefaultGatedFetchUserMonthlyAllow = 20
	headDefaultGatedFetchChatMonthlyCap   = 20
	headDefaultApifyMonthlyBudgetUsd      = 4.0
	headDefaultHikerAPIMonthlyBudgetUsd   = 1.0
	headDefaultHikerAPIPricePerRequestUsd = 0.001
	headDefaultHikerAPILowBalanceRequests = 100
	headDefaultInstagramPaidProviders     = "apify,hiker"
	headGatedFetchNoGlobalCap             = -1
)

func headIsProxyEnabled() bool {
	return headGetBoolEnv("ENABLE_PROXIES", true)
}

func headIsProxyEnabledForPlatform(platform string) bool {
	// Check global proxy setting first
	if headIsProxyEnabled() == false {
		return false
	}

	// Check platform-specific proxy settings
	switch platform {
	case "youtube":
		return headGetBoolEnv("ENABLE_YOUTUBE_PROXY", false) // YouTube defaults to no proxy
	case "twitter":
		return headGetBoolEnv("ENABLE_TWITTER_PROXY", true)
	case "tiktok":
		return headGetBoolEnv("ENABLE_TIKTOK_PROXY", true)
	case "9gag":
		return headGetBoolEnv("ENABLE_9GAG_PROXY", true)
	case "facebook":
		return headGetBoolEnv("ENABLE_FACEBOOK_PROXY", true)
	case "reddit":
		return headGetBoolEnv("ENABLE_REDDIT_PROXY", true)
	default:
		return true // Default to enabled for unknown platforms
	}
}

func headIsYouTubeEnabled() bool {
	return headGetBoolEnv("ENABLE_YOUTUBE", true)
}

func headIsTwitterEnabled() bool {
	return headGetBoolEnv("ENABLE_TWITTER", true)
}

func headIsTikTokEnabled() bool {
	return headGetBoolEnv("ENABLE_TIKTOK", true)
}

func headIsInstagramEnabled() bool {
	return headGetBoolEnv("ENABLE_INSTAGRAM", true)
}

func headIsFacebookEnabled() bool {
	return headGetBoolEnv("ENABLE_FACEBOOK", true)
}

func headIsRedditEnabled() bool {
	return headGetBoolEnv("ENABLE_REDDIT", true)
}

func headIs9GAGEnabled() bool {
	return headGetBoolEnv("ENABLE_9GAG", true)
}

func headIsThreadsEnabled() bool {
	result := headGetBoolEnv("ENABLE_THREADS", true)

	return result
}

func headGetDefaultDailyMediaLimit() int {
	if limit := os.Getenv("DEFAULT_DAILY_MEDIA_LIMIT"); limit != "" {
		// Parse the limit, default to 0 if invalid
		if parsed, err := strconv.Atoi(limit); err == nil {
			return parsed
		}
	}
	return 0 // 0 means no limit
}

func headGetInstagramService() string {
	service := strings.ToLower(strings.TrimSpace(os.Getenv("INSTAGRAM_SERVICE")))

	switch service {
	case "toinstagram", "onebigram":
		return service
	}

	return "toinstagram"
}

func headGetKKInstagramHost() string {
	host := strings.TrimSpace(os.Getenv("KKINSTAGRAM_HOST"))
	if host == "" {
		return "kkinstagram.com"
	}

	return host
}

func headGetKKTikTokHost() string {
	host := strings.TrimSpace(os.Getenv("KKTIKTOK_HOST"))
	if host == "" {
		return "kktiktok.com"
	}

	return host
}

func headGetVxredditHost() string {
	host := strings.TrimSpace(os.Getenv("VXREDDIT_HOST"))
	if host == "" {
		return "vxreddit.com"
	}

	return host
}

func headGetFacebedHost() string {
	rawHost := os.Getenv("FACEBED_HOST")
	host := strings.TrimSpace(rawHost)
	result := host

	if host == "" {
		result = headDefaultFacebedHost
	}

	return result
}

func headGetGatedFetchGlobalMonthlyCap() int {
	result := headGetCapEnv("GATED_FETCH_GLOBAL_MONTHLY_CAP", headGatedFetchNoGlobalCap)

	return result
}

func headGetGatedFetchUserMonthlyAllowance() int {
	result := headGetIntEnv("GATED_FETCH_USER_MONTHLY_ALLOWANCE", headDefaultGatedFetchUserMonthlyAllow)

	return result
}

func headGetGatedFetchChatMonthlyCap() int {
	result := headGetIntEnv("GATED_FETCH_CHAT_MONTHLY_CAP", headDefaultGatedFetchChatMonthlyCap)

	return result
}

func headIsStarsSupportEnabled() bool {
	result := headGetBoolEnv("STARS_SUPPORT_ENABLED", false)

	return result
}

func headGetApifyMonthlyBudgetUsd() float64 {
	result := headDefaultApifyMonthlyBudgetUsd
	value := os.Getenv("APIFY_MONTHLY_BUDGET_USD")
	raw := strings.TrimSpace(value)
	parsed, err := strconv.ParseFloat(raw, 64)

	if (raw != "") && (err == nil) && (parsed >= 0) {
		result = parsed
	}

	return result
}

func headGetHikerAPIMonthlyBudgetUsd() float64 {
	result := headGetNonNegativeFloatEnv("HIKERAPI_MONTHLY_BUDGET_USD", headDefaultHikerAPIMonthlyBudgetUsd)

	return result
}

func headGetHikerAPIPricePerRequestUsd() float64 {
	result := headGetPositiveFloatEnv("HIKERAPI_PRICE_PER_REQUEST_USD", headDefaultHikerAPIPricePerRequestUsd)

	return result
}

func headGetHikerAPILowBalanceRequests() int {
	result := headGetIntEnv("HIKERAPI_LOW_BALANCE_REQUESTS", headDefaultHikerAPILowBalanceRequests)

	return result
}

func headGetInstagramPaidProviders() []string {
	raw := headGetStringEnv("INSTAGRAM_PAID_PROVIDERS", headDefaultInstagramPaidProviders)
	lowered := strings.ToLower(raw)
	parts := strings.Split(lowered, ",")
	result := make([]string, 0, len(parts))

	for _, part := range parts {
		name := strings.TrimSpace(part)

		if name != "" {
			result = append(result, name)
		}
	}

	return result
}

func headGetStringEnv(key, defaultValue string) string {
	value := os.Getenv(key)
	result := strings.TrimSpace(value)

	if result == "" {
		result = defaultValue
	}

	return result
}

func headGetIntEnv(key string, defaultValue int) int {
	result := defaultValue
	value := os.Getenv(key)
	raw := strings.TrimSpace(value)
	parsed, err := strconv.Atoi(raw)

	if (raw != "") && (err == nil) && (parsed >= 0) {
		result = parsed
	}

	return result
}

func headGetCapEnv(key string, defaultValue int) int {
	result := defaultValue
	value := os.Getenv(key)
	raw := strings.TrimSpace(value)
	parsed, err := strconv.Atoi(raw)

	if (raw != "") && (err == nil) && (parsed >= headGatedFetchNoGlobalCap) {
		result = parsed
	}

	return result
}

func headGetBoolEnv(key string, defaultValue bool) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if value == "" {
		return defaultValue
	}
	return value == "true" || value == "1" || value == "yes" || value == "on"
}

func headGetPositiveFloatEnv(key string, defaultValue float64) float64 {
	result := defaultValue
	parsed := headGetNonNegativeFloatEnv(key, defaultValue)

	if parsed > 0 {
		result = parsed
	}

	return result
}

func headGetNonNegativeFloatEnv(key string, defaultValue float64) float64 {
	result := defaultValue
	value := os.Getenv(key)
	raw := strings.TrimSpace(value)
	parsed, err := strconv.ParseFloat(raw, 64)

	if (raw != "") && (err == nil) && (parsed >= 0) {
		result = parsed
	}

	return result
}
