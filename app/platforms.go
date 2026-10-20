package app

import (
	"fmt"

	"github.com/timoffmax/social-link-bot/config"
	"github.com/timoffmax/social-link-bot/model/settings"
)

// chatSettingsPlatform is one platform button of the group settings keyboards.
type chatSettingsPlatform struct {
	Key   string
	Name  string
	Emoji string
}

// platformEmoji is the emoji of each platform in buttons and stats.
var platformEmoji = map[string]string{
	settings.PlatformInstagram: "📷",
	settings.PlatformTikTok:    "🎵",
	settings.PlatformTwitter:   "🐦",
	settings.PlatformYouTube:   "📺",
	settings.PlatformReddit:    "🤖",
	settings.PlatformFacebook:  "📘",
	settings.Platform9GAG:      "😂",
	settings.PlatformThreads:   "🧵",
}

// chatSettingsPlatforms lists the platforms the owner has on, for the group
// settings keyboards.
func chatSettingsPlatforms() []chatSettingsPlatform {
	all := settings.Platforms()
	result := make([]chatSettingsPlatform, 0, len(all))

	for _, platform := range all {
		if config.IsPlatformEnabled(platform.ID) == true {
			emoji := platformEmoji[platform.ID]
			item := chatSettingsPlatform{Key: platform.ID, Name: platform.Label, Emoji: emoji}
			result = append(result, item)
		}
	}

	return result
}

// platformLabelLine is a platform's emoji and name ("😂 9GAG"), the author
// line of a caption without one.
func platformLabelLine(platformID string) string {
	result := socialMediaAuthorFallback
	all := settings.Platforms()

	for _, platform := range all {
		if platform.ID == platformID {
			emoji := platformEmoji[platform.ID]
			result = fmt.Sprintf("%s %s", emoji, platform.Label)
		}
	}

	return result
}
