package services

import (
	"fmt"
	"html"
	"strings"
)

// EscapeForTelegramHTML normalizes text for Telegram HTML parse mode: existing
// entities are unescaped first so already-escaped input (e.g. Reddit API
// titles) is not double-escaped.
func EscapeForTelegramHTML(text string) string {
	return html.EscapeString(html.UnescapeString(text))
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
		if (displayName != "") && (username != "") {
			authorLine = fmt.Sprintf("%s %s (@%s)", emoji, displayName, username)
		} else if username != "" {
			authorLine = fmt.Sprintf("%s @%s", emoji, username)
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
