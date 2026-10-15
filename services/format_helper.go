package services

import (
	"fmt"
	"html"
	"regexp"
	"strings"
)

// EscapeForTelegramHTML normalizes text for Telegram HTML parse mode: existing
// entities are unescaped first so already-escaped input (e.g. Reddit API
// titles) is not double-escaped.
func EscapeForTelegramHTML(text string) string {
	return html.EscapeString(html.UnescapeString(text))
}

// profileURLTemplates link a username to its profile on the platforms whose
// usernames may contain dots (which Telegram cuts the mention at). Twitter
// is left out (no dots; its text is sent raw in places that don't use HTML
// parse mode), and YouTube too: callers often have only a channel display
// name, which would link to the wrong account.
var profileURLTemplates = map[string]string{
	"instagram": "https://www.instagram.com/%s/",
	"tiktok":    "https://www.tiktok.com/@%s",
}

// profileUsernamePattern accepts usernames that are safe inside a URL and an
// HTML attribute (letters, digits, dots, underscores).
var profileUsernamePattern = regexp.MustCompile(`^[A-Za-z0-9._]{1,64}$`)

// profileMentionMarker is where a profile link's text starts; it identifies
// the author line built by FormatProfileMention.
const profileMentionMarker = `">@`

// profileMentionTemplate renders a username as a link to its profile.
const profileMentionTemplate = `<a href="%s` + profileMentionMarker + `%s</a>`

// FormatProfileMention renders "@username" as an HTML link to the profile on
// the platform. A bare "@insight.gastrobar" is auto-linked by Telegram as the
// Telegram user "@insight" (Telegram usernames have no dots), and even a
// dot-free name would point at an unrelated Telegram account; text inside a
// link is not auto-linked. Unknown platforms and unsafe names stay plain.
func FormatProfileMention(platform, username string) string {
	result := fmt.Sprintf("@%s", username)
	lowerPlatform := strings.ToLower(platform)
	template, hasProfile := profileURLTemplates[lowerPlatform]
	isSafe := profileUsernamePattern.MatchString(username)

	if (hasProfile == true) && (isSafe == true) {
		profileURL := fmt.Sprintf(template, username)
		result = fmt.Sprintf(profileMentionTemplate, profileURL, username)
	}

	return result
}

// IsProfileMentionLine reports whether a caption line holds a profile link
// made by FormatProfileMention (the author line).
func IsProfileMentionLine(line string) bool {
	result := strings.Contains(line, profileMentionMarker)

	return result
}

// FormatAuthorCaption formats caption with author info at the top for any social media platform
func FormatAuthorCaption(platform, displayName, username, originalCaption string) string {
	authorLine := ""

	// Choose emoji based on platform
	emoji := "👤" // Default
	switch strings.ToLower(platform) {
	case "instagram":
		emoji = "📷"
	case "tiktok":
		emoji = "🎵"
	case "youtube":
		emoji = "📺"
	case "reddit":
		emoji = "🟠"
	case "facebook":
		emoji = "📘"
	case "twitter", "x":
		emoji = "🐦"
	}

	// Special handling for Reddit where displayName = subreddit, username = author
	if strings.ToLower(platform) == "reddit" {
		if (displayName != "") && (username != "") {
			authorLine = fmt.Sprintf("%s r/%s • u/%s", emoji, displayName, username)
		} else if displayName != "" {
			authorLine = fmt.Sprintf("%s r/%s", emoji, displayName)
		} else if username != "" {
			authorLine = fmt.Sprintf("%s u/%s", emoji, username)
		}
	} else {
		mention := FormatProfileMention(platform, username)

		if (displayName != "") && (username != "") {
			authorLine = fmt.Sprintf("%s %s (%s)", emoji, displayName, mention)
		} else if username != "" {
			authorLine = fmt.Sprintf("%s %s", emoji, mention)
		} else if displayName != "" {
			authorLine = fmt.Sprintf("%s %s", emoji, displayName)
		}
	}

	// Only add separator between author and caption when both exist
	if (authorLine != "") && (originalCaption != "") {
		return authorLine + "\n\n" + originalCaption
	}

	if authorLine != "" {
		return authorLine
	}

	return originalCaption
}
