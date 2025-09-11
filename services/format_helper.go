package services

import (
	"fmt"
	"strings"
)

// FormatAuthorCaption formats caption with author info at the top for any social media platform
func FormatAuthorCaption(platform, displayName, username, originalCaption string) string {
	// Start with author info
	caption := ""
	
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
		if displayName != "" && username != "" {
			// Both subreddit and author available
			caption = fmt.Sprintf("%s r/%s • u/%s\n\n", emoji, displayName, username)
		} else if displayName != "" {
			// Only subreddit available
			caption = fmt.Sprintf("%s r/%s\n\n", emoji, displayName)
		} else if username != "" {
			// Only author available (rare case)
			caption = fmt.Sprintf("%s u/%s\n\n", emoji, username)
		}
	} else {
		if displayName != "" && username != "" {
			// Both name and username available
			caption = fmt.Sprintf("%s %s (@%s)\n\n", emoji, displayName, username)
		} else if username != "" {
			// Only username available
			caption = fmt.Sprintf("%s @%s\n\n", emoji, username)
		} else if displayName != "" {
			// Only name available
			caption = fmt.Sprintf("%s %s\n\n", emoji, displayName)
		}
	}
	
	// Add original caption/text
	caption += originalCaption
	
	return caption
}