package config

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/timoffmax/social-link-bot/config/overrides"
	"github.com/timoffmax/social-link-bot/model/settings"
)

// unsetEnv marks envVar as not set for the rest of the test (t.Setenv
// restores the old value afterwards).
func unsetEnv(t *testing.T, envVar string) {
	t.Setenv(envVar, "")

	if err := os.Unsetenv(envVar); err != nil {
		t.Fatalf("unset %s: %v", envVar, err)
	}
}

// setEnvCase sets envVar to value, or unsets it for unsetValue.
func setEnvCase(t *testing.T, envVar string, value string) {
	if value == unsetValue {
		unsetEnv(t, envVar)
	}

	if value != unsetValue {
		t.Setenv(envVar, value)
	}
}

// unsetValue marks the "variable not set" case of the matrix.
const unsetValue = "<unset>"

// useSnapshot sets the overrides snapshot for one test.
func useSnapshot(t *testing.T, snapshot *overrides.Snapshot) {
	previous := overrides.Current()
	overrides.Set(snapshot)
	t.Cleanup(func() { overrides.Set(previous) })
}

// emptySnapshots are the states of empty settings tables: nothing loaded
// yet, and an empty load.
func emptySnapshots() map[string]*overrides.Snapshot {
	result := map[string]*overrides.Snapshot{
		"no snapshot":    nil,
		"empty snapshot": overrides.NewSnapshot(map[string]overrides.Entry{}, time.Now()),
	}

	return result
}

var (
	boolEnvValues     = []string{unsetValue, "", "  ", "true", "TRUE", " yes ", "1", "on", "false", "0", "no", "off", "garbage"}
	intEnvValues      = []string{unsetValue, "", " ", "0", "5", " 7 ", "-1", "-2", "-5", "abc", "1.5", "999999", "+3"}
	floatEnvValues    = []string{unsetValue, "", " ", "0", "0.5", "4.8", " 2 ", "-1", "abc", "1e1", "NaN", "Inf", "0.0000001"}
	hostEnvValues     = []string{unsetValue, "", " ", "x.example", " y.example ", "UPPER.example", "http://z.example/path"}
	serviceEnvValues  = []string{unsetValue, "", "toinstagram", "ONEBIGRAM", " onebigram ", "kksave", "garbage"}
	providerEnvValues = []string{unsetValue, "", " ", "hiker", "HIKER, apify", ",,", "foo,apify", "apify,,hiker"}
)

// getterCase compares one rewired getter with its HEAD copy.
type getterCase struct {
	key    string
	envVar string
	values []string
	getter func() any
	head   func() any
}

func asAny[T any](getter func() T) func() any {
	result := func() any {
		value := getter()

		return value
	}

	return result
}

func getterCases() []getterCase {
	result := []getterCase{
		{settings.KeyPlatformInstagramEnabled, "ENABLE_INSTAGRAM", boolEnvValues, asAny(IsInstagramEnabled), asAny(headIsInstagramEnabled)},
		{settings.KeyPlatformTikTokEnabled, "ENABLE_TIKTOK", boolEnvValues, asAny(IsTikTokEnabled), asAny(headIsTikTokEnabled)},
		{settings.KeyPlatformTwitterEnabled, "ENABLE_TWITTER", boolEnvValues, asAny(IsTwitterEnabled), asAny(headIsTwitterEnabled)},
		{settings.KeyPlatformYouTubeEnabled, "ENABLE_YOUTUBE", boolEnvValues, asAny(IsYouTubeEnabled), asAny(headIsYouTubeEnabled)},
		{settings.KeyPlatformRedditEnabled, "ENABLE_REDDIT", boolEnvValues, asAny(IsRedditEnabled), asAny(headIsRedditEnabled)},
		{settings.KeyPlatformFacebookEnabled, "ENABLE_FACEBOOK", boolEnvValues, asAny(IsFacebookEnabled), asAny(headIsFacebookEnabled)},
		{settings.KeyPlatform9GAGEnabled, "ENABLE_9GAG", boolEnvValues, asAny(Is9GAGEnabled), asAny(headIs9GAGEnabled)},
		{settings.KeyPlatformThreadsEnabled, "ENABLE_THREADS", boolEnvValues, asAny(IsThreadsEnabled), asAny(headIsThreadsEnabled)},
		{settings.KeyInstagramFixedLinkService, "INSTAGRAM_SERVICE", serviceEnvValues, asAny(GetInstagramService), asAny(headGetInstagramService)},
		{settings.KeyProxyEnabled, "ENABLE_PROXIES", boolEnvValues, asAny(IsProxyEnabled), asAny(headIsProxyEnabled)},
		{settings.KeyServiceKKInstagramHost, "KKINSTAGRAM_HOST", hostEnvValues, asAny(GetKKInstagramHost), asAny(headGetKKInstagramHost)},
		{settings.KeyServiceKKTikTokHost, "KKTIKTOK_HOST", hostEnvValues, asAny(GetKKTikTokHost), asAny(headGetKKTikTokHost)},
		{settings.KeyServiceVxredditHost, "VXREDDIT_HOST", hostEnvValues, asAny(GetVxredditHost), asAny(headGetVxredditHost)},
		{settings.KeyServiceFacebedHost, "FACEBED_HOST", hostEnvValues, asAny(GetFacebedHost), asAny(headGetFacebedHost)},
		{settings.KeyApifyMonthlyBudgetUsd, "APIFY_MONTHLY_BUDGET_USD", floatEnvValues, asAny(GetApifyMonthlyBudgetUsd), asAny(headGetApifyMonthlyBudgetUsd)},
		{settings.KeyHikerMonthlyBudgetUsd, "HIKERAPI_MONTHLY_BUDGET_USD", floatEnvValues, asAny(GetHikerAPIMonthlyBudgetUsd), asAny(headGetHikerAPIMonthlyBudgetUsd)},
		{settings.KeyHikerPricePerRequestUsd, "HIKERAPI_PRICE_PER_REQUEST_USD", floatEnvValues, asAny(GetHikerAPIPricePerRequestUsd), asAny(headGetHikerAPIPricePerRequestUsd)},
		{settings.KeyInstagramProviderOrder, "INSTAGRAM_PAID_PROVIDERS", providerEnvValues, asAny(GetInstagramPaidProviders), asAny(headGetInstagramPaidProviders)},
		{settings.KeyHikerLowBalanceRequests, "HIKERAPI_LOW_BALANCE_REQUESTS", intEnvValues, asAny(GetHikerAPILowBalanceRequests), asAny(headGetHikerAPILowBalanceRequests)},
		{settings.KeyGatedGlobalMonthlyCap, "GATED_FETCH_GLOBAL_MONTHLY_CAP", intEnvValues, asAny(GetGatedFetchGlobalMonthlyCap), asAny(headGetGatedFetchGlobalMonthlyCap)},
		{settings.KeyGatedUserMonthlyAllowance, "GATED_FETCH_USER_MONTHLY_ALLOWANCE", intEnvValues, asAny(GetGatedFetchUserMonthlyAllowance), asAny(headGetGatedFetchUserMonthlyAllowance)},
		{settings.KeyGatedChatMonthlyCap, "GATED_FETCH_CHAT_MONTHLY_CAP", intEnvValues, asAny(GetGatedFetchChatMonthlyCap), asAny(headGetGatedFetchChatMonthlyCap)},
		{settings.KeyStarsEnabled, "STARS_SUPPORT_ENABLED", boolEnvValues, asAny(IsStarsSupportEnabled), asAny(headIsStarsSupportEnabled)},
		{settings.KeyDefaultDailyMediaLimit, "DEFAULT_DAILY_MEDIA_LIMIT", intEnvValues, asAny(GetDefaultDailyMediaLimit), asAny(headGetDefaultDailyMediaLimit)},
	}

	return result
}

// TestGettersMatchHeadWithEmptySettingsTables is the no-behaviour-change
// matrix: with no owner-set value, every getter returns what the HEAD getter
// returns for the same .env, set, unset, blank and malformed.
func TestGettersMatchHeadWithEmptySettingsTables(t *testing.T) {
	for snapshotName, snapshot := range emptySnapshots() {
		for _, getter := range getterCases() {
			for _, value := range getter.values {
				name := fmt.Sprintf("%s/%s=%q", snapshotName, getter.envVar, value)

				t.Run(name, func(t *testing.T) {
					useSnapshot(t, snapshot)
					setEnvCase(t, getter.envVar, value)
					got := getter.getter()
					want := getter.head()

					if reflect.DeepEqual(got, want) == false {
						t.Fatalf("got %#v, HEAD returns %#v", got, want)
					}
				})
			}
		}
	}
}

// TestProxyForPlatformMatchesHeadWithEmptySettingsTables covers the global
// switch, every platform switch and platforms without a switch.
func TestProxyForPlatformMatchesHeadWithEmptySettingsTables(t *testing.T) {
	platformVars := map[string]string{
		"youtube":  "ENABLE_YOUTUBE_PROXY",
		"twitter":  "ENABLE_TWITTER_PROXY",
		"tiktok":   "ENABLE_TIKTOK_PROXY",
		"9gag":     "ENABLE_9GAG_PROXY",
		"facebook": "ENABLE_FACEBOOK_PROXY",
		"reddit":   "ENABLE_REDDIT_PROXY",
	}
	platforms := []string{"youtube", "twitter", "tiktok", "9gag", "facebook", "reddit", "instagram", "threads", "unknown", ""}

	for snapshotName, snapshot := range emptySnapshots() {
		for _, globalValue := range []string{unsetValue, "true", "false", "garbage"} {
			for _, platformValue := range []string{unsetValue, "true", "false", "garbage"} {
				name := fmt.Sprintf("%s/ENABLE_PROXIES=%q/platform=%q", snapshotName, globalValue, platformValue)

				t.Run(name, func(t *testing.T) {
					useSnapshot(t, snapshot)
					setEnvCase(t, "ENABLE_PROXIES", globalValue)

					for _, envVar := range platformVars {
						setEnvCase(t, envVar, platformValue)
					}

					for _, platform := range platforms {
						if IsProxyEnabledForPlatform(platform) != headIsProxyEnabledForPlatform(platform) {
							t.Fatalf("%q: got %v, HEAD returns %v", platform, IsProxyEnabledForPlatform(platform), headIsProxyEnabledForPlatform(platform))
						}
					}
				})
			}
		}
	}
}

// TestEveryRegistryKeyHasAGetterCase keeps the matrix complete.
func TestEveryRegistryKeyHasAGetterCase(t *testing.T) {
	covered := map[string]bool{}

	for _, getter := range getterCases() {
		covered[getter.key] = true
	}

	for _, setting := range settings.All() {
		_, isProxyPlatform := proxyPlatformSettings[setting.Key]

		if (covered[setting.Key] == false) && (isProxyPlatform == false) {
			t.Fatalf("%s has no getter case", setting.Key)
		}

		if _, isKnown := settingFromEnvReaders[setting.Key]; isKnown == false {
			t.Fatalf("%s has no .env reader", setting.Key)
		}
	}

	if len(settingFromEnvReaders) != len(settings.All()) {
		t.Fatalf("%d .env readers for %d registry keys", len(settingFromEnvReaders), len(settings.All()))
	}
}

// TestRegistryDefaultsMatchTheGetters: with the variable unset, the .env
// reader gives the registry default, so the published source "default" is
// true.
func TestRegistryDefaultsMatchTheGetters(t *testing.T) {
	for _, setting := range settings.All() {
		unsetEnv(t, setting.EnvVar)
		got, _ := GetFallbackSetting(setting.Key)

		if settings.SameValue(got, setting.Default) == false {
			t.Fatalf("%s: getter default %#v, registry default %#v", setting.Key, got, setting.Default)
		}
	}
}

// otherEnvValue is a valid value other than the default, as .env text and
// as the typed value the getter returns.
func otherEnvValue(setting settings.Setting) (string, any) {
	text := "hiker"
	var value any = []string{"hiker"}
	lastEnum := ""

	if len(setting.Enum) > 0 {
		lastEnum = setting.Enum[len(setting.Enum)-1]
	}

	switch setting.Type {
	case settings.TypeBool:
		isOn := (setting.Default == false)
		text = fmt.Sprintf("%v", isOn)
		value = isOn
	case settings.TypeInt:
		next := setting.Default.(int) + 1
		text = fmt.Sprintf("%d", next)
		value = next
	case settings.TypeDecimal:
		text = "0.5"
		value = 0.5
	case settings.TypeEnum:
		text = lastEnum
		value = lastEnum
	case settings.TypeHost:
		text = "example.org"
		value = "example.org"
	}

	return text, value
}

// TestRegistryEnvVarsMatchTheGetters: each registry env var is the one its
// getter reads.
func TestRegistryEnvVarsMatchTheGetters(t *testing.T) {
	useSnapshot(t, nil)
	seen := map[string]bool{}

	for _, setting := range settings.All() {
		if seen[setting.EnvVar] == true {
			t.Fatalf("env var %s is listed twice", setting.EnvVar)
		}

		seen[setting.EnvVar] = true
		text, want := otherEnvValue(setting)
		t.Setenv(setting.EnvVar, text)
		effective, _ := GetEffectiveSetting(setting.Key)

		if (settings.SameValue(effective.Value, want) == false) || (effective.Source != settings.SourceEnv) {
			t.Fatalf("%s=%s: got %#v from %s, want %#v from env", setting.EnvVar, text, effective.Value, effective.Source, want)
		}
	}
}

// TestOwnerSetValueWinsOverEnv: DB, then .env, then the default.
func TestOwnerSetValueWinsOverEnv(t *testing.T) {
	t.Setenv("ENABLE_TIKTOK", "true")
	t.Setenv("APIFY_MONTHLY_BUDGET_USD", "4.8")
	t.Setenv("INSTAGRAM_PAID_PROVIDERS", "apify,hiker")
	unsetEnv(t, "ENABLE_REDDIT")
	entries := map[string]overrides.Entry{
		settings.KeyPlatformTikTokEnabled:  {Value: false, Source: settings.SourceDB},
		settings.KeyApifyMonthlyBudgetUsd:  {Value: 2.5, Source: settings.SourceCache},
		settings.KeyInstagramProviderOrder: {Value: []string{"hiker"}, Source: settings.SourceDB},
	}
	useSnapshot(t, overrides.NewSnapshot(entries, time.Now()))

	if (IsTikTokEnabled() == true) || (GetApifyMonthlyBudgetUsd() != 2.5) || (reflect.DeepEqual(GetInstagramPaidProviders(), []string{"hiker"}) == false) {
		t.Fatalf("owner-set values must win: %v %v %v", IsTikTokEnabled(), GetApifyMonthlyBudgetUsd(), GetInstagramPaidProviders())
	}

	providers := GetInstagramPaidProviders()
	providers[0] = "changed"

	if GetInstagramPaidProviders()[0] != "hiker" {
		t.Fatalf("a caller must not change the snapshot")
	}

	tiktok, _ := GetEffectiveSetting(settings.KeyPlatformTikTokEnabled)
	budget, _ := GetEffectiveSetting(settings.KeyApifyMonthlyBudgetUsd)
	reddit, _ := GetEffectiveSetting(settings.KeyPlatformRedditEnabled)
	fallback, _ := GetFallbackSetting(settings.KeyPlatformTikTokEnabled)

	if (tiktok.Source != settings.SourceDB) || (tiktok.IsEnvSet == false) || (budget.Source != settings.SourceCache) || (reddit.Source != settings.SourceDefault) || (reddit.Value != true) || (fallback != true) {
		t.Fatalf("unexpected sources: %+v %+v %+v fallback %v", tiktok, budget, reddit, fallback)
	}
}

// TestProxyPlatformSwitchFollowsTheOwnerValueOnlyWhileProxiesAreOn.
func TestProxyPlatformSwitchFollowsTheOwnerValueOnlyWhileProxiesAreOn(t *testing.T) {
	t.Setenv("ENABLE_PROXIES", "false")
	t.Setenv("ENABLE_TIKTOK_PROXY", "true")
	entries := map[string]overrides.Entry{
		settings.KeyProxyYouTubeEnabled: {Value: true, Source: settings.SourceDB},
		settings.KeyProxyTikTokEnabled:  {Value: false, Source: settings.SourceDB},
	}
	useSnapshot(t, overrides.NewSnapshot(entries, time.Now()))

	if IsProxyEnabledForPlatform("youtube") == true {
		t.Fatalf("proxies are off in .env")
	}

	entries[settings.KeyProxyEnabled] = overrides.Entry{Value: true, Source: settings.SourceDB}
	useSnapshot(t, overrides.NewSnapshot(entries, time.Now()))

	if (IsProxyEnabledForPlatform("youtube") == false) || (IsProxyEnabledForPlatform("tiktok") == true) || (IsProxyEnabledForPlatform("instagram") == false) {
		t.Fatalf("got youtube %v, tiktok %v, instagram %v", IsProxyEnabledForPlatform("youtube"), IsProxyEnabledForPlatform("tiktok"), IsProxyEnabledForPlatform("instagram"))
	}
}

// TestEffectiveSettingEncodesAsJSON: the published blob must marshal.
func TestEffectiveSettingEncodesAsJSON(t *testing.T) {
	useSnapshot(t, nil)

	for _, setting := range settings.All() {
		effective, _ := GetEffectiveSetting(setting.Key)

		if _, err := json.Marshal(effective); err != nil {
			t.Fatalf("%s: %v", setting.Key, err)
		}
	}
}

// TestEveryRegistryKeyResolvesThroughGetEffectiveSetting: the getters'
// managed settings and the registry list the same keys.
func TestEveryRegistryKeyResolvesThroughGetEffectiveSetting(t *testing.T) {
	useSnapshot(t, nil)

	for _, setting := range settings.All() {
		if _, isKnown := GetEffectiveSetting(setting.Key); isKnown == false {
			t.Fatalf("%s does not resolve", setting.Key)
		}
	}

	for key := range settingFromEnvReaders {
		if _, isKnown := settings.Lookup(key); isKnown == false {
			t.Fatalf("%s is managed but not in the registry", key)
		}
	}

	if _, isKnown := GetEffectiveSetting("platform.myspace.enabled"); isKnown == true {
		t.Fatalf("an unknown key must not resolve")
	}
}

// TestEveryPlatformResolvesThroughIsPlatformEnabled: every registry
// platform follows its switch (owner value, then .env, then on), and an
// unknown platform is off.
func TestEveryPlatformResolvesThroughIsPlatformEnabled(t *testing.T) {
	for _, platform := range settings.Platforms() {
		key, isKnown := settings.PlatformEnabledKey(platform.ID)
		setting, _ := settings.Lookup(key)

		if isKnown == false {
			t.Fatalf("%s has no switch", platform.ID)
		}

		useSnapshot(t, nil)
		unsetEnv(t, setting.EnvVar)

		if IsPlatformEnabled(platform.ID) == false {
			t.Fatalf("%s must be on by default", platform.ID)
		}

		t.Setenv(setting.EnvVar, "false")

		if IsPlatformEnabled(platform.ID) == true {
			t.Fatalf("%s must follow %s", platform.ID, setting.EnvVar)
		}

		entries := map[string]overrides.Entry{key: {Value: true, Source: settings.SourceDB}}
		useSnapshot(t, overrides.NewSnapshot(entries, time.Now()))

		if IsPlatformEnabled(platform.ID) == false {
			t.Fatalf("%s must follow the owner-set value", platform.ID)
		}
	}

	if IsPlatformEnabled("myspace") == true {
		t.Fatalf("an unknown platform must be off")
	}
}

// TestInstagramPaidProvidersAreNotAliased: a caller changing the returned
// list changes neither the snapshot nor the next .env read.
func TestInstagramPaidProvidersAreNotAliased(t *testing.T) {
	t.Setenv("INSTAGRAM_PAID_PROVIDERS", "apify,hiker")
	useSnapshot(t, nil)
	fromEnv := GetInstagramPaidProviders()
	fromEnv[0] = "changed"

	if reflect.DeepEqual(GetInstagramPaidProviders(), []string{"apify", "hiker"}) == false {
		t.Fatalf("the .env list must not be shared: %v", GetInstagramPaidProviders())
	}

	owned := []string{"hiker", "apify"}
	entries := map[string]overrides.Entry{settings.KeyInstagramProviderOrder: {Value: owned, Source: settings.SourceDB}}
	useSnapshot(t, overrides.NewSnapshot(entries, time.Now()))
	owned[0] = "changed"
	fromSnapshot := GetInstagramPaidProviders()
	fromSnapshot[1] = "changed"

	if reflect.DeepEqual(GetInstagramPaidProviders(), []string{"hiker", "apify"}) == false {
		t.Fatalf("the snapshot list must not be shared: %v", GetInstagramPaidProviders())
	}
}
