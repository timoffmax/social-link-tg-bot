package services

import (
	"regexp"
	"strings"
)

// ExtractInstagramUsername extracts username from Instagram URL
func ExtractInstagramUsername(url string) string {
	// Instagram URL patterns:
	// https://www.instagram.com/username/
	// https://www.instagram.com/username/p/POST_ID/
	// https://www.instagram.com/username/reel/REEL_ID/
	patterns := []string{
		`instagram\.com/([^/]+)/?`,
		`instagram\.com/([^/]+)/(?:p|reel|tv)/`,
		`instagram\.com/stories/([^/]+)/`,
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindStringSubmatch(url)
		if len(matches) > 1 {
			username := matches[1]
			// Skip special paths
			if username != "p" && username != "reel" && username != "tv" && username != "stories" {
				return username
			}
		}
	}
	return ""
}

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
