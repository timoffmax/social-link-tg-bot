package app

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/timoffmax/social-link-bot/config/overrides"
	"github.com/timoffmax/social-link-bot/model/settings"
)

// headStartPlatformsTemplate is the /start platform list at HEAD (406ba21).
const headStartPlatformsTemplate = `🌐 <b>Supported platforms:</b>
📷 <b>Instagram</b> — Posts, Reels%s
🎵 <b>TikTok</b> — Videos, Photo Slideshows
🐦 <b>Twitter/X</b> — Photos, Videos, GIFs, Tweet Text
📺 <b>YouTube</b> — Shorts, Community Posts
🔴 <b>Reddit</b> — Videos, Images, Galleries, GIFs, Text and Link Posts
👥 <b>Facebook</b> — Videos, Reels, Photos, Text Posts
😂 <b>9GAG</b> — Videos, Images, Articles
🧵 <b>Threads</b> — Text, Photos, Videos, Carousels

`

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

func TestStartPlatformsWithEveryPlatformOnAreTheHeadList(t *testing.T) {
	for _, envVar := range []string{"ENABLE_INSTAGRAM", "ENABLE_TIKTOK", "ENABLE_TWITTER", "ENABLE_YOUTUBE", "ENABLE_REDDIT", "ENABLE_FACEBOOK", "ENABLE_9GAG", "ENABLE_THREADS"} {
		t.Setenv(envVar, "")
	}

	useSettingsSnapshot(t, map[string]any{})

	for token, storiesItem := range map[string]string{"": "", "test-token": startStoriesItem} {
		t.Setenv("APIFY_TOKEN", token)

		if startPlatformsText() != fmt.Sprintf(headStartPlatformsTemplate, storiesItem) {
			t.Fatalf("token %q: the list changed: %q", token, startPlatformsText())
		}
	}
}

func TestStartPlatformsFollowTheSwitches(t *testing.T) {
	t.Setenv("APIFY_TOKEN", "test-token")
	t.Setenv("ENABLE_REDDIT", "false")
	useSettingsSnapshot(t, map[string]any{settings.KeyPlatformInstagramEnabled: false, settings.KeyPlatformRedditEnabled: true, settings.KeyPlatformThreadsEnabled: false})
	text := startPlatformsText()

	if (strings.Contains(text, "Instagram") == true) || (strings.Contains(text, "Threads") == true) || (strings.Contains(text, "<b>Reddit</b>") == false) || (strings.Contains(text, "<b>TikTok</b>") == false) {
		t.Fatalf("the list must follow the switches (owner value over .env): %q", text)
	}

	offValues := map[string]any{}

	for _, platform := range settings.Platforms() {
		key, _ := settings.PlatformEnabledKey(platform.ID)
		offValues[key] = false
	}

	useSettingsSnapshot(t, offValues)

	if strings.Contains(startPlatformsText(), startNoPlatformsText) == false {
		t.Fatalf("expected the paused line, got %q", startPlatformsText())
	}
}
