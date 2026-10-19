package settings

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// mvpKeys are the keys of the MVP (design section 14).
var mvpKeys = []string{
	KeyPlatformInstagramEnabled, KeyPlatformTikTokEnabled, KeyPlatformTwitterEnabled, KeyPlatformYouTubeEnabled,
	KeyPlatformRedditEnabled, KeyPlatformFacebookEnabled, KeyPlatform9GAGEnabled, KeyPlatformThreadsEnabled,
	KeyInstagramFixedLinkService, KeyProxyEnabled, KeyProxyYouTubeEnabled, KeyProxyTwitterEnabled,
	KeyProxyTikTokEnabled, KeyProxyFacebookEnabled, KeyProxyRedditEnabled, KeyProxy9GAGEnabled,
	KeyApifyMonthlyBudgetUsd, KeyHikerMonthlyBudgetUsd, KeyHikerPricePerRequestUsd, KeyInstagramProviderOrder,
	KeyHikerLowBalanceRequests, KeyGatedGlobalMonthlyCap, KeyGatedUserMonthlyAllowance, KeyGatedChatMonthlyCap,
	KeyStarsEnabled, KeyDefaultDailyMediaLimit, KeyServiceKKInstagramHost, KeyServiceKKTikTokHost,
	KeyServiceVxredditHost, KeyServiceFacebedHost,
}

func mustLookup(t *testing.T, key string) Setting {
	setting, isKnown := Lookup(key)

	if isKnown == false {
		t.Fatalf("%s is not in the registry", key)
	}

	return setting
}

func rawJSON(t *testing.T, value any) json.RawMessage {
	raw, err := json.Marshal(value)

	if err != nil {
		t.Fatalf("marshal %#v: %v", value, err)
	}

	return raw
}

func TestRegistryHoldsExactlyTheMVPKeys(t *testing.T) {
	keys := map[string]bool{}

	for _, setting := range All() {
		keys[setting.Key] = true
	}

	for _, key := range mvpKeys {
		if keys[key] == false {
			t.Fatalf("%s is missing", key)
		}
	}

	if len(All()) != len(mvpKeys) {
		t.Fatalf("%d settings, the MVP has %d", len(All()), len(mvpKeys))
	}
}

func TestEverySettingIsCompleteAndItsDefaultValid(t *testing.T) {
	keys := map[string]bool{}
	envVars := map[string]bool{}

	for _, setting := range All() {
		if (keys[setting.Key] == true) || (envVars[setting.EnvVar] == true) {
			t.Fatalf("%s or %s is listed twice", setting.Key, setting.EnvVar)
		}

		keys[setting.Key] = true
		envVars[setting.EnvVar] = true

		if (setting.Label == "") || (setting.Impact == "") || (setting.EnvVar == "") || (setting.Group == "") || (setting.Approval == "") {
			t.Fatalf("%s lacks a label, impact, env var, group or approval rule", setting.Key)
		}

		value, err := setting.Validate(rawJSON(t, setting.Default))

		if (err != nil) || (SameValue(value, setting.Default) == false) {
			t.Fatalf("%s default %#v is not valid: %v", setting.Key, setting.Default, err)
		}

		if setting.SafeValue != nil {
			if _, err := setting.Validate(rawJSON(t, setting.SafeValue)); err != nil {
				t.Fatalf("%s safe value is not valid: %v", setting.Key, err)
			}
		}
	}
}

func TestMoneyAndPaymentKeysHaveASafeValue(t *testing.T) {
	safe := map[string]any{
		KeyApifyMonthlyBudgetUsd:   0.0,
		KeyHikerMonthlyBudgetUsd:   0.0,
		KeyHikerPricePerRequestUsd: 1.0,
		KeyStarsEnabled:            false,
	}

	for key, want := range safe {
		if mustLookup(t, key).SafeValue != want {
			t.Fatalf("%s safe value %#v, want %#v", key, mustLookup(t, key).SafeValue, want)
		}
	}
}

func TestHardCeilings(t *testing.T) {
	ceilings := map[string]float64{
		KeyApifyMonthlyBudgetUsd:     20,
		KeyHikerMonthlyBudgetUsd:     10,
		KeyGatedGlobalMonthlyCap:     5000,
		KeyGatedUserMonthlyAllowance: 1000,
		KeyGatedChatMonthlyCap:       1000,
	}

	for key, ceiling := range ceilings {
		setting := mustLookup(t, key)

		if (setting.Ceiling == nil) || (*setting.Ceiling != ceiling) {
			t.Fatalf("%s ceiling %v, want %v", key, setting.Ceiling, ceiling)
		}

		if _, err := setting.Validate(rawJSON(t, ceiling)); err != nil {
			t.Fatalf("%s: the ceiling itself must be valid: %v", key, err)
		}

		_, err := setting.Validate(rawJSON(t, ceiling+1))

		if (err == nil) || (strings.Contains(err.Error(), "hard ceiling") == false) {
			t.Fatalf("%s: above the ceiling must be refused, got %v", key, err)
		}
	}
}

func TestValidateByType(t *testing.T) {
	cases := []struct {
		key     string
		raw     string
		want    any
		isValid bool
	}{
		{KeyPlatformTikTokEnabled, `false`, false, true},
		{KeyPlatformTikTokEnabled, `"false"`, nil, false},
		{KeyPlatformTikTokEnabled, `0`, nil, false},
		{KeyPlatformTikTokEnabled, `true true`, nil, false},
		{KeyDefaultDailyMediaLimit, `30`, 30, true},
		{KeyDefaultDailyMediaLimit, `1000`, 1000, true},
		{KeyDefaultDailyMediaLimit, `1001`, nil, false},
		{KeyDefaultDailyMediaLimit, `-1`, nil, false},
		{KeyDefaultDailyMediaLimit, `1.5`, nil, false},
		{KeyDefaultDailyMediaLimit, `"30"`, nil, false},
		{KeyGatedGlobalMonthlyCap, `-1`, -1, true},
		{KeyGatedGlobalMonthlyCap, `-2`, nil, false},
		{KeyGatedGlobalMonthlyCap, `0`, 0, true},
		{KeyApifyMonthlyBudgetUsd, `4.8`, 4.8, true},
		{KeyApifyMonthlyBudgetUsd, `0`, 0.0, true},
		{KeyApifyMonthlyBudgetUsd, `4.805`, nil, false},
		{KeyApifyMonthlyBudgetUsd, `-0.01`, nil, false},
		{KeyHikerPricePerRequestUsd, `0.000123`, 0.000123, true},
		{KeyHikerPricePerRequestUsd, `0`, nil, false},
		{KeyHikerPricePerRequestUsd, `0.0000001`, nil, false},
		{KeyHikerPricePerRequestUsd, `1.5`, nil, false},
		{KeyInstagramFixedLinkService, `"onebigram"`, "onebigram", true},
		{KeyInstagramFixedLinkService, `"kksave"`, nil, false},
		{KeyInstagramProviderOrder, `["hiker","apify"]`, []string{"hiker", "apify"}, true},
		{KeyInstagramProviderOrder, `["hiker"]`, []string{"hiker"}, true},
		{KeyInstagramProviderOrder, `[]`, nil, false},
		{KeyInstagramProviderOrder, `["apify","apify"]`, nil, false},
		{KeyInstagramProviderOrder, `["rapidapi"]`, nil, false},
		{KeyInstagramProviderOrder, `"apify"`, nil, false},
		{KeyServiceFacebedHost, `"Facebed.COM"`, "facebed.com", true},
		{KeyServiceFacebedHost, `5`, nil, false},
	}

	for _, test := range cases {
		setting := mustLookup(t, test.key)
		got, err := setting.Validate(json.RawMessage(test.raw))

		if (err == nil) != test.isValid {
			t.Fatalf("%s %s: valid=%v, err %v", test.key, test.raw, err == nil, err)
		}

		if reflect.DeepEqual(got, test.want) == false {
			t.Fatalf("%s %s: got %#v, want %#v", test.key, test.raw, got, test.want)
		}
	}
}

func TestValidateHost(t *testing.T) {
	cases := map[string]error{
		"kkinstagram.com":                 nil,
		"www.facebed.com":                 nil,
		"a-b.example.co.uk":               nil,
		"":                                ErrHostEmpty,
		"127.0.0.1":                       ErrHostIP,
		"10.0.0.2":                        ErrHostIP,
		"::1":                             ErrHostIP,
		"[::1]":                           ErrHostCharacter,
		"example.com:8080":                ErrHostCharacter,
		"https://example.com":             ErrHostCharacter,
		"example.com/path":                ErrHostCharacter,
		"user@example.com":                ErrHostCharacter,
		"exämple.com":                     ErrHostCharacter,
		"xn--exmple-cua.com":              nil,
		"example.com.":                    ErrHostLabel,
		"localhost":                       ErrHostLabels,
		"-bad.example.com":                ErrHostLabel,
		"bad-.example.com":                ErrHostLabel,
		"a..example.com":                  ErrHostLabel,
		strings.Repeat("a", 64) + ".com":  ErrHostLabel,
		strings.Repeat("a.", 127) + "com": ErrHostTooLong,
		"example .com":                    ErrHostCharacter,
		"1.2.3":                           ErrHostIP,
	}

	for host, want := range cases {
		_, err := ValidateHost(host)

		if errors.Is(err, want) == false {
			t.Fatalf("%q: got %v, want %v", host, err, want)
		}
	}
}

func TestApprovalRules(t *testing.T) {
	cases := []struct {
		key       string
		current   any
		next      any
		wantsWait bool
	}{
		{KeyApifyMonthlyBudgetUsd, 4.8, 6.0, true},
		{KeyApifyMonthlyBudgetUsd, 0.0, 1.0, true},
		{KeyApifyMonthlyBudgetUsd, 4.8, 4.8, false},
		{KeyApifyMonthlyBudgetUsd, 4.8, 2.0, false},
		{KeyApifyMonthlyBudgetUsd, 4.8, 0.0, false},
		{KeyHikerMonthlyBudgetUsd, 1.0, 1.5, true},
		{KeyHikerMonthlyBudgetUsd, 1.0, 0.5, false},
		{KeyHikerPricePerRequestUsd, 0.001, 0.0005, true},
		{KeyHikerPricePerRequestUsd, 0.001, 0.002, false},
		{KeyServiceFacebedHost, "www.facebed.com", "facebed.example", true},
		{KeyServiceFacebedHost, "www.facebed.com", "www.facebed.com", false},
		{KeyStarsEnabled, false, true, false},
		{KeyStarsEnabled, true, false, false},
		{KeyPlatformTikTokEnabled, true, false, false},
		{KeyGatedGlobalMonthlyCap, -1, 5000, false},
		{KeyGatedUserMonthlyAllowance, 20, 1000, false},
		{KeyInstagramProviderOrder, []string{"apify", "hiker"}, []string{"hiker"}, false},
	}

	for _, test := range cases {
		setting := mustLookup(t, test.key)

		if setting.NeedsApproval(test.current, test.next) != test.wantsWait {
			t.Fatalf("%s %v -> %v: needs approval %v, want %v", test.key, test.current, test.next, setting.NeedsApproval(test.current, test.next), test.wantsWait)
		}
	}
}

func TestApprovalOfANonNumberBudgetIsRequired(t *testing.T) {
	if mustLookup(t, KeyApifyMonthlyBudgetUsd).NeedsApproval(nil, 1.0) == false {
		t.Fatalf("an unknown current budget must wait for approval")
	}
}

func TestSameValue(t *testing.T) {
	if (SameValue(20, 20.0) == false) || (SameValue(json.RawMessage(`20`), 20) == false) || (SameValue([]string{"a"}, json.RawMessage(`["a"]`)) == false) {
		t.Fatalf("equal JSON values must be the same")
	}

	if (SameValue(true, "true") == true) || (SameValue([]string{"a", "b"}, []string{"b", "a"}) == true) {
		t.Fatalf("different JSON values must differ")
	}
}

func TestAllReturnsACopy(t *testing.T) {
	first := All()
	first[0].Label = "changed"
	order := mustLookup(t, KeyInstagramProviderOrder)
	order.Enum[0] = "changed"
	defaults := order.Default.([]string)
	defaults[0] = "changed"

	if (All()[0].Label == "changed") || (mustLookup(t, KeyInstagramProviderOrder).Enum[0] == "changed") || (mustLookup(t, KeyInstagramProviderOrder).Default.([]string)[0] == "changed") {
		t.Fatalf("callers must not change the registry")
	}
}

func TestPlatformKeysCoverEveryPlatform(t *testing.T) {
	for _, platform := range Platforms() {
		key, isKnown := PlatformEnabledKey(platform.ID)

		if (isKnown == false) || (mustLookup(t, key).Group != GroupPlatforms) {
			t.Fatalf("%s has no platform switch", platform.ID)
		}
	}

	if _, hasProxy := ProxyEnabledKey(PlatformInstagram); hasProxy == true {
		t.Fatalf("Instagram has no proxy switch at HEAD")
	}
}
