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
	case "9gag":
		emoji = "😂"
	case "twitter", "x":
		emoji = "🐦"
	}
	
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
	
	// Add original caption/text
	caption += originalCaption
	
	return caption
}