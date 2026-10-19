package services

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/timoffmax/social-link-bot/config/overrides"
	"github.com/timoffmax/social-link-bot/model/settings"
)

// headBotDescriptionTemplate and headBotShortDescription are the profile
// texts at HEAD (406ba21), with every platform listed.
const headBotDescriptionTemplate = `Send a link, get the actual photos and videos right in Telegram.

📸 Instagram · 🎵 TikTok · 🐦 X · 🎬 YouTube Shorts · 🔴 Reddit · 👥 Facebook · 😂 9GAG · 🧵 Threads

💬 Private chat: send me any link.
👥 Groups: add me, and I answer every link anyone posts.

🔓 Public posts: free, no monthly limit.
%s
No ads, no forced subscriptions. Tap Start for details.`

const headBotShortDescription = "Send a link, get the photo or video: Instagram, TikTok, X, YouTube Shorts, Threads & more. Private chats & groups."

// useSettingsSnapshot sets owner-set values for one test.
func useSettingsSnapshot(t *testing.T, values map[string]any) {
	entries := map[string]overrides.Entry{}

	for key, value := range values {
		entries[key] = overrides.Entry{Value: value, Source: settings.SourceDB}
	}

	previous := overrides.Current()
	overrides.Set(overrides.NewSnapshot(entries, time.Now()))
	t.Cleanup(func() { overrides.Set(previous) })
}

// unsetPlatformEnv clears every platform switch in .env.
func unsetPlatformEnv(t *testing.T) {
	for _, envVar := range []string{"ENABLE_INSTAGRAM", "ENABLE_TIKTOK", "ENABLE_TWITTER", "ENABLE_YOUTUBE", "ENABLE_REDDIT", "ENABLE_FACEBOOK", "ENABLE_9GAG", "ENABLE_THREADS"} {
		t.Setenv(envVar, "")
	}
}

func TestProfileWithEveryPlatformOnIsTheHeadProfile(t *testing.T) {
	unsetPlatformEnv(t)
	t.Setenv("GATED_FETCH_USER_MONTHLY_ALLOWANCE", "")
	t.Setenv("GATED_FETCH_CHAT_MONTHLY_CAP", "")

	for _, token := range []string{"", "test-token"} {
		t.Setenv("APIFY_TOKEN", token)
		useSettingsSnapshot(t, map[string]any{})
		if IsSpecialDownloadsOffered() == true {
			t.Fatalf("token %q: special downloads must not be offered without a database", token)
		}

		if BotDescription() != fmt.Sprintf(headBotDescriptionTemplate, "") {
			t.Fatalf("token %q: description changed: %q", token, BotDescription())
		}

		if BotShortDescription() != headBotShortDescription {
			t.Fatalf("short description changed: %q", BotShortDescription())
		}
	}
}

// platformCombination switches the platforms on by the bits of mask.
func platformCombination(mask int) map[string]any {
	result := map[string]any{}

	for index, platform := range settings.Platforms() {
		key, _ := settings.PlatformEnabledKey(platform.ID)
		result[key] = ((mask & (1 << index)) != 0)
	}

	return result
}

func TestDescriptionsFitForEveryPlatformCombination(t *testing.T) {
	markDatabaseOpenForTest(t)
	t.Setenv("APIFY_TOKEN", "test-token")
	t.Setenv("GATED_FETCH_USER_MONTHLY_ALLOWANCE", "1000")
	t.Setenv("GATED_FETCH_CHAT_MONTHLY_CAP", "1000")
	combinations := 1 << len(settings.Platforms())

	for mask := 0; mask < combinations; mask++ {
		useSettingsSnapshot(t, platformCombination(mask))
		description := BotDescription()
		shortDescription := BotShortDescription()

		if (utf8.RuneCountInString(description) > botDescriptionMaxLength) || (utf16Length(description) > botDescriptionMaxLength) {
			t.Fatalf("mask %b: description too long: %q", mask, description)
		}

		if (utf8.RuneCountInString(shortDescription) > botShortDescriptionMaxLength) || (utf16Length(shortDescription) > botShortDescriptionMaxLength) {
			t.Fatalf("mask %b: short description too long (%d): %q", mask, utf16Length(shortDescription), shortDescription)
		}
	}
}

func TestDescriptionsFollowThePlatformSwitches(t *testing.T) {
	unsetPlatformEnv(t)
	useSettingsSnapshot(t, map[string]any{settings.KeyPlatformTikTokEnabled: false, settings.KeyPlatformRedditEnabled: false})

	if (strings.Contains(BotDescription(), "TikTok") == true) || (strings.Contains(BotDescription(), "Reddit") == true) || (strings.Contains(BotDescription(), "🧵 Threads") == false) {
		t.Fatalf("description must list only the platforms that are on: %q", BotDescription())
	}

	if BotShortDescription() != "Send a link, get the photo or video: Instagram, X, YouTube Shorts, Threads, Facebook & more. Private chats & groups." {
		t.Fatalf("unexpected short description %q", BotShortDescription())
	}

	useSettingsSnapshot(t, platformCombination(0))

	if (strings.Contains(BotDescription(), botDescriptionNoPlatformsText) == false) || (BotShortDescription() != "Send a link, get the photo or video. Private chats & groups.") {
		t.Fatalf("no platform on: %q / %q", BotDescription(), BotShortDescription())
	}

	useSettingsSnapshot(t, map[string]any{settings.KeyPlatformTikTokEnabled: false, settings.KeyPlatformRedditEnabled: false, settings.KeyPlatformFacebookEnabled: false, settings.KeyPlatform9GAGEnabled: false})

	if BotShortDescription() != "Send a link, get the photo or video: Instagram, X, YouTube Shorts & Threads. Private chats & groups." {
		t.Fatalf("unexpected short description %q", BotShortDescription())
	}

	if strings.Join(EnabledPlatforms(), ",") != "instagram,twitter,youtube,threads" {
		t.Fatalf("unexpected enabled platforms %v", EnabledPlatforms())
	}
}

func TestPushBotProfilePushesOnlyChangedParts(t *testing.T) {
	var mutex sync.Mutex
	methods := []string{}
	useBotAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
		mutex.Lock()
		methods = append(methods, r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:])
		mutex.Unlock()
	})
	t.Setenv("STARS_SUPPORT_ENABLED", "false")
	useSettingsSnapshot(t, map[string]any{})
	known, called, err := PushBotProfile(map[string]string{}, RenderBotProfile())

	if (err != nil) || (len(called) != 3) {
		t.Fatalf("a first push sends every part: %v %v", called, err)
	}

	_, called, _ = PushBotProfile(known, RenderBotProfile())

	if len(called) != 0 {
		t.Fatalf("an unchanged profile must not be pushed, got %v", called)
	}

	useSettingsSnapshot(t, map[string]any{settings.KeyStarsEnabled: true})
	_, called, _ = PushBotProfile(known, RenderBotProfile())

	if strings.Join(called, ",") != ProfileMethodCommands {
		t.Fatalf("Stars on changes only the command menu, got %v", called)
	}

	useSettingsSnapshot(t, map[string]any{settings.KeyPlatformTikTokEnabled: false})
	_, called, _ = PushBotProfile(known, RenderBotProfile())

	if strings.Join(called, ",") != "setMyDescription,setMyShortDescription" {
		t.Fatalf("a platform switch changes both descriptions, got %v", called)
	}
}
