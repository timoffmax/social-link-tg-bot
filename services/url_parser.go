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

// ExtractYouTubeChannel extracts channel name from YouTube URL
func ExtractYouTubeChannel(url string) string {
	// YouTube URL patterns:
	// https://www.youtube.com/@channelname/shorts/ID
	// https://www.youtube.com/@channelname/post/ID
	// https://www.youtube.com/channel/CHANNEL_ID
	// https://www.youtube.com/c/channelname
	patterns := []string{
		`youtube\.com/@([^/]+)`,
		`youtube\.com/c/([^/]+)`,
		`youtube\.com/user/([^/]+)`,
	}
	
	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindStringSubmatch(url)
		if len(matches) > 1 {
			return matches[1]
		}
	}
	return ""
}

// ExtractRedditUsername extracts username/subreddit from Reddit URL
func ExtractRedditUsername(url string) string {
	// Reddit URL patterns:
	// https://www.reddit.com/r/subreddit/comments/ID/title/
	// https://www.reddit.com/user/username/comments/ID/title/
	patterns := []string{
		`reddit\.com/r/([^/]+)`,      // Subreddit
		`reddit\.com/user/([^/]+)`,    // User post
		`reddit\.com/u/([^/]+)`,       // User post (short)
	}
	
	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindStringSubmatch(url)
		if len(matches) > 1 {
			username := matches[1]
			// Add r/ prefix for subreddits
			if strings.Contains(url, "/r/") {
				return "r/" + username
			}
			// Add u/ prefix for users
			return "u/" + username
		}
	}
	return ""
}

// ExtractFacebookUsername extracts username/page from Facebook URL
func ExtractFacebookUsername(url string) string {
	// Facebook URL patterns:
	// https://www.facebook.com/username/posts/ID
	// https://www.facebook.com/pages/pagename/ID
	// https://www.facebook.com/profile.php?id=ID (numeric profile)
	patterns := []string{
		`facebook\.com/([^/?]+)/?(?:posts|photos|videos)?`,
		`facebook\.com/pages/([^/]+)`,
	}
	
	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindStringSubmatch(url)
		if len(matches) > 1 {
			username := matches[1]
			// Skip special paths
			if username != "pages" && username != "groups" && username != "watch" && 
			   username != "profile.php" && username != "photo.php" && username != "video.php" {
				return username
			}
		}
	}
	
	// Handle numeric profiles
	if strings.Contains(url, "profile.php?id=") {
		re := regexp.MustCompile(`id=(\d+)`)
		matches := re.FindStringSubmatch(url)
		if len(matches) > 1 {
			return "profile:" + matches[1]
		}
	}
	
	return ""
}

// Extract9GAGUsername extracts username from 9GAG URL
func Extract9GAGUsername(url string) string {
	// 9GAG URL patterns:
	// https://9gag.com/gag/ID (no username in URL)
	// https://9gag.com/u/username
	patterns := []string{
		`9gag\.com/u/([^/]+)`,
	}
	
	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindStringSubmatch(url)
		if len(matches) > 1 {
			return matches[1]
		}
	}
	
	// 9GAG posts don't include username in URL
	return ""
}