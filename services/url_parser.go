package services

import (
	"regexp"
	"strings"
)

// ExtractTikTokUsername extracts username from TikTok URL
func ExtractTikTokUsername(url string) string {
	// TikTok URL patterns:
	// https://www.tiktok.com/@username/video/ID
	// https://vm.tiktok.com/ID (short URL, no username)
	patterns := []string{
		`tiktok\.com/@([^/]+)`,
		`tiktok\.com/([^/]+)/video/`,
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindStringSubmatch(url)
		if len(matches) > 1 {
			username := matches[1]
			// Remove @ if present
			return strings.TrimPrefix(username, "@")
		}
	}
	return ""
}
