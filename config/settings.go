package config

import (
	"os"
	"strings"

	"github.com/timoffmax/social-link-bot/config/overrides"
	"github.com/timoffmax/social-link-bot/model/settings"
)

// settingValue returns the owner-set value of key from the settings
// snapshot, else what fromEnv reads (.env, then the getter's default, parsed
// exactly as before the dashboard). An owner-set value of another type
// (never stored: the bot validates every value) also falls back to fromEnv.
func settingValue[T any](key string, fromEnv func() T) T {
	entry, isSet := overrides.Lookup(key)
	result, isTyped := entry.Value.(T)

	if (isSet == false) || (isTyped == false) {
		result = fromEnv()
	}

	return result
}

// fromEnvAsAny wraps a typed .env reader for the settingFromEnvReaders table.
func fromEnvAsAny[T any](fromEnv func() T) func() any {
	result := func() any {
		value := fromEnv()

		return value
	}

	return result
}

// managedSetting is one setting the owner manages: its registry key and its
// .env reader (the getter's parsing and default, as before the dashboard).
type managedSetting[T any] struct {
	key     string
	fromEnv func() T
}

// value returns the owner-set value, else the .env one, else the default.
func (s managedSetting[T]) value() T {
	result := settingValue(s.key, s.fromEnv)

	return result
}

// settingFromEnvReaders reads each managed setting from .env with its
// getter's parsing and default: the value a key has while the owner set
// none, or after the owner resets it. manage fills it from the declarations
// below, the same readers the getters use, so a getter can not read a key
// GetEffectiveSetting does not know.
var settingFromEnvReaders = map[string]func() any{}

// manage declares a managed setting and adds its .env reader to the table.
func manage[T any](key string, fromEnv func() T) managedSetting[T] {
	reader := fromEnvAsAny(fromEnv)
	settingFromEnvReaders[key] = reader
	result := managedSetting[T]{key: key, fromEnv: fromEnv}

	return result
}

// manageBool declares a managed on/off setting read from a boolean env
// variable.
func manageBool(key string, envVar string, defaultValue bool) managedSetting[bool] {
	fromEnv := boolFromEnv(envVar, defaultValue)
	result := manage(key, fromEnv)

	return result
}

// The managed settings, one per registry key; each getter reads its own.
var (
	platformInstagramEnabledSetting  = manageBool(settings.KeyPlatformInstagramEnabled, "ENABLE_INSTAGRAM", true)
	platformTikTokEnabledSetting     = manageBool(settings.KeyPlatformTikTokEnabled, "ENABLE_TIKTOK", true)
	platformTwitterEnabledSetting    = manageBool(settings.KeyPlatformTwitterEnabled, "ENABLE_TWITTER", true)
	platformYouTubeEnabledSetting    = manageBool(settings.KeyPlatformYouTubeEnabled, "ENABLE_YOUTUBE", true)
	platformRedditEnabledSetting     = manageBool(settings.KeyPlatformRedditEnabled, "ENABLE_REDDIT", true)
	platformFacebookEnabledSetting   = manageBool(settings.KeyPlatformFacebookEnabled, "ENABLE_FACEBOOK", true)
	platform9GAGEnabledSetting       = manageBool(settings.KeyPlatform9GAGEnabled, "ENABLE_9GAG", true)
	platformThreadsEnabledSetting    = manageBool(settings.KeyPlatformThreadsEnabled, "ENABLE_THREADS", true)
	instagramFixedLinkServiceSetting = manage(settings.KeyInstagramFixedLinkService, getInstagramServiceFromEnv)
	serviceKKInstagramHostSetting    = manage(settings.KeyServiceKKInstagramHost, getKKInstagramHostFromEnv)
	serviceKKTikTokHostSetting       = manage(settings.KeyServiceKKTikTokHost, getKKTikTokHostFromEnv)
	serviceVxredditHostSetting       = manage(settings.KeyServiceVxredditHost, getVxredditHostFromEnv)
	serviceFacebedHostSetting        = manage(settings.KeyServiceFacebedHost, getFacebedHostFromEnv)
	apifyMonthlyBudgetUsdSetting     = manage(settings.KeyApifyMonthlyBudgetUsd, getApifyMonthlyBudgetUsdFromEnv)
	hikerMonthlyBudgetUsdSetting     = manage(settings.KeyHikerMonthlyBudgetUsd, getHikerAPIMonthlyBudgetUsdFromEnv)
	hikerPricePerRequestUsdSetting   = manage(settings.KeyHikerPricePerRequestUsd, getHikerAPIPricePerRequestUsdFromEnv)
	instagramProviderOrderSetting    = manage(settings.KeyInstagramProviderOrder, getInstagramPaidProvidersFromEnv)
	hikerLowBalanceRequestsSetting   = manage(settings.KeyHikerLowBalanceRequests, getHikerAPILowBalanceRequestsFromEnv)
	gatedGlobalMonthlyCapSetting     = manage(settings.KeyGatedGlobalMonthlyCap, getGatedFetchGlobalMonthlyCapFromEnv)
	gatedUserMonthlyAllowanceSetting = manage(settings.KeyGatedUserMonthlyAllowance, getGatedFetchUserMonthlyAllowanceFromEnv)
	gatedChatMonthlyCapSetting       = manage(settings.KeyGatedChatMonthlyCap, getGatedFetchChatMonthlyCapFromEnv)
	starsEnabledSetting              = manage(settings.KeyStarsEnabled, isStarsSupportEnabledFromEnv)
	defaultDailyMediaLimitSetting    = manage(settings.KeyDefaultDailyMediaLimit, getDefaultDailyMediaLimitFromEnv)
)

// boolSettingValue returns a managed on/off setting by key, on the getters'
// path (owner-set value, else its .env reader); false for an unknown key.
func boolSettingValue(key string) bool {
	result := false
	fromEnv, isKnown := settingFromEnvReaders[key]

	if isKnown == true {
		envBool := boolReader(fromEnv)
		setting := managedSetting[bool]{key: key, fromEnv: envBool}
		result = setting.value()
	}

	return result
}

// boolReader narrows a table reader to a boolean one (false for another
// type).
func boolReader(fromEnv func() any) func() bool {
	result := func() bool {
		value := fromEnv()
		isOn, _ := value.(bool)

		return isOn
	}

	return result
}

// IsPlatformEnabled reports whether a platform's links are processed
// bot-wide: its platform.<id>.enabled switch (dashboard, then .env, then
// on). An unknown platform id is off.
func IsPlatformEnabled(platformID string) bool {
	key, isKnown := settings.PlatformEnabledKey(platformID)
	result := (isKnown == true) && (boolSettingValue(key) == true)

	return result
}

// EffectiveSetting is the value the bot uses for a managed setting and where
// it comes from (see the settings.Source* constants).
type EffectiveSetting struct {
	Value    any    `json:"value"`
	Source   string `json:"source"`
	IsEnvSet bool   `json:"env_set"`
}

// GetEffectiveSetting returns the value the bot uses for key: the owner-set
// one, else the .env one, else the default. isKnown is false for a key the
// registry does not list.
func GetEffectiveSetting(key string) (EffectiveSetting, bool) {
	result := EffectiveSetting{}
	fromEnv, isKnown := settingFromEnvReaders[key]
	entry, isSet := overrides.Lookup(key)
	setting, _ := settings.Lookup(key)
	result.IsEnvSet = isEnvVarSet(setting.EnvVar)

	if (isKnown == true) && (isSet == true) {
		result.Value = entry.Value
		result.Source = entry.Source
	}

	if (isKnown == true) && (isSet == false) {
		result.Value = fromEnv()
		result.Source = fallbackSource(result.IsEnvSet)
	}

	return result, isKnown
}

// GetFallbackSetting returns the value key has without an owner-set value:
// the .env one, else the default.
func GetFallbackSetting(key string) (any, bool) {
	var result any
	fromEnv, isKnown := settingFromEnvReaders[key]

	if isKnown == true {
		result = fromEnv()
	}

	return result, isKnown
}

// fallbackSource names where a value without an owner-set one comes from.
func fallbackSource(isEnvSet bool) string {
	result := settings.SourceDefault

	if isEnvSet == true {
		result = settings.SourceEnv
	}

	return result
}

// isEnvVarSet reports a non-blank env variable.
func isEnvVarSet(envVar string) bool {
	value := os.Getenv(envVar)
	trimmed := strings.TrimSpace(value)
	result := (envVar != "") && (trimmed != "")

	return result
}
