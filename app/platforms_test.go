package app

import (
	"strings"
	"testing"

	"github.com/timoffmax/social-link-bot/model/settings"
)

func TestGetPlatformFromURLNamesEveryPlatform(t *testing.T) {
	cases := map[string]string{
		"https://www.instagram.com/reel/DesQ0ptp04D/": "instagram",
		"https://www.tiktok.com/@someone/video/1":     "tiktok",
		"https://x.com/someone/status/1":              "twitter",
		"https://www.youtube.com/shorts/abc":          "youtube",
		"https://www.reddit.com/r/pics/comments/abc/": "reddit",
		"https://www.facebook.com/photo/?fbid=1":      "facebook",
		"https://9gag.com/gag/aBcDeF":                 "9gag",
		"https://m.9gag.com/gag/aBcDeF":               "9gag",
		"https://www.threads.com/@someone/post/abc":   "threads",
		"https://example.com/something":               "unknown",
	}

	for url, want := range cases {
		got := getPlatformFromURL(url)

		if got != want {
			t.Fatalf("%s: got %q, want %q", url, got, want)
		}
	}
}

func TestChatSettingsPlatformsListEveryPlatformWithAnEmoji(t *testing.T) {
	got := chatSettingsPlatforms()
	want := settings.PlatformIDs()

	if len(got) != len(want) {
		t.Fatalf("got %d platforms, want %d", len(got), len(want))
	}

	for i, platform := range got {
		if (platform.Key != want[i]) || (platform.Name == "") || (platform.Emoji == "") {
			t.Fatalf("platform %d: got %+v, want key %q with a name and an emoji", i, platform, want[i])
		}
	}
}

func TestExtractOtherAuthorInfoFindsTheAuthorOrNamesThePlatform(t *testing.T) {
	longLine := strings.Repeat("a", 120)
	cases := []struct {
		platform string
		caption  string
		want     string
	}{
		{"twitter", "Some Name (@some)\nTweet text", "Some Name (@some)"},
		{"twitter", "👤 Some Name (@some)", "👤 Some Name (@some)"},
		{"twitter", "short first line\nno handle", "short first line"},
		{"twitter", longLine, "🐦 Twitter/X"},
		{"instagram", "📷 Some Name (@some)\ntext", "📷 Some Name (@some)"},
		{"instagram", "no author here", "📷 Instagram"},
		{"tiktok", "no author here", "🎵 TikTok"},
		{"youtube", " Channel \nvideo text", "Channel"},
		{"youtube", longLine, "📺 YouTube"},
		{"reddit", "title\nr/pics by u/someone", "r/pics by u/someone"},
		{"reddit", "no names", "🤖 Reddit"},
		{"facebook", "post text", "📘 Facebook"},
		{"9gag", "😂 A funny title", "😂 9GAG"},
		{"unknown", "text", "📱 Social Media"},
		{"instagram", "", ""},
	}

	for _, tc := range cases {
		got := extractOtherAuthorInfo(tc.caption, tc.platform)

		if got != tc.want {
			t.Fatalf("%s %q: got %q, want %q", tc.platform, tc.caption, got, tc.want)
		}
	}
}
