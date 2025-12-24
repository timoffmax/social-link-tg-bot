package services

import (
	"github.com/timoffmax/social-link-bot/config"
	"log"
	"strings"
)

// getInstagramFixedDomain returns the configured Instagram service domain
func getInstagramFixedDomain() string {
	service := config.GetInstagramService()
	if service == "kkinstagram" {
		return "kkinstagram.com"
	}
	return "instagramez.com"
}

// ExtractionMethod represents a method for extracting media
type ExtractionMethod int

const (
	MethodYtDlp ExtractionMethod = iota
	MethodBrowser
	MethodFixedLink
	MethodNone
)

// ExtractionStrategy defines the order of methods to try for a platform
type ExtractionStrategy struct {
	Platform string
	Methods  []ExtractionMethod
	HasFixedLink bool
	FixedLinkDomain string
}

// GetExtractionStrategy returns the optimal extraction strategy for a given URL
func GetExtractionStrategy(url string) *ExtractionStrategy {
	// Instagram: optimized based on test results showing yt-dlp performance is excellent
	// Also handle kkinstagram.com URLs (already fixed links)
	if strings.Contains(url, "instagram.com") || strings.Contains(url, "kkinstagram.com") {
		if !config.IsInstagramEnabled() {
			return &ExtractionStrategy{
				Platform: "instagram",
				Methods: []ExtractionMethod{},
				HasFixedLink: false,
			}
		}
		// Instagram videos (reels, IGTV): directly use fixed link for speed
		if strings.Contains(url, "/reel/") || strings.Contains(url, "/tv/") || strings.Contains(url, "/reels/") {
			return &ExtractionStrategy{
				Platform: "instagram",
				Methods: []ExtractionMethod{
					MethodFixedLink,  // Direct link replacement only
				},
				HasFixedLink: true,
				FixedLinkDomain: getInstagramFixedDomain(),
			}
		}
		
		// Instagram photos (/p/ URLs): attempt download for all photos
		// This handles both single photos and carousels, with fallback to fixed link
		if strings.Contains(url, "/p/") {
			log.Printf("📸 Instagram photo - will attempt download with fallback to direct link")
			return &ExtractionStrategy{
				Platform: "instagram",
				Methods: []ExtractionMethod{
					MethodBrowser,    // Browser extraction for photos/albums
					MethodYtDlp,      // Fallback to yt-dlp
					MethodFixedLink,  // Fast fallback to fixed link
				},
				HasFixedLink: true,
				FixedLinkDomain: getInstagramFixedDomain(),
			}
		}
		// Instagram stories and other content: try downloading first
		return &ExtractionStrategy{
			Platform: "instagram",
			Methods: []ExtractionMethod{
				MethodBrowser,    // Browser extraction
				MethodYtDlp,      // Fallback to yt-dlp
				MethodFixedLink,  // Last resort
			},
			HasFixedLink: true,
			FixedLinkDomain: getInstagramFixedDomain(),
		}
	}
	
	// TikTok: different strategies for videos vs slideshows
	if strings.Contains(url, "tiktok.com") || strings.Contains(url, "vm.tiktok.com") {
		if !config.IsTikTokEnabled() {
			return &ExtractionStrategy{
				Platform: "tiktok",
				Methods: []ExtractionMethod{},
				HasFixedLink: false,
			}
		}
		// Photo slideshows: browser extraction often works better than yt-dlp for individual images
		if strings.Contains(url, "/photo/") {
			return &ExtractionStrategy{
				Platform: "tiktok",
				Methods: []ExtractionMethod{
					MethodBrowser,
					MethodYtDlp,
					MethodFixedLink,
				},
				HasFixedLink: true,
				FixedLinkDomain: "vxtiktok.com",
			}
		}
		// Regular videos: yt-dlp first, then fixed link for fallback
		return &ExtractionStrategy{
			Platform: "tiktok",
			Methods: []ExtractionMethod{
				MethodYtDlp,
				MethodFixedLink,
			},
			HasFixedLink: true,
			FixedLinkDomain: "vxtiktok.com",
		}
	}
	
	// Twitter/X: browser extractor first (FxTwitter API has full text), then yt-dlp fallback
	if strings.Contains(url, "twitter.com") || strings.Contains(url, "x.com") {
		if !config.IsTwitterEnabled() {
			return &ExtractionStrategy{
				Platform: "twitter",
				Methods: []ExtractionMethod{},
				HasFixedLink: false,
			}
		}
		return &ExtractionStrategy{
			Platform: "twitter",
			Methods: []ExtractionMethod{
				MethodBrowser, // Custom Twitter extractor with FxTwitter API (full text)
				MethodYtDlp,   // Fallback - has truncated text but reliable video download
				MethodFixedLink,
			},
			HasFixedLink: true,
			FixedLinkDomain: "fixupx.com",
		}
	}
	
	// YouTube: different strategies for videos vs community posts
	if strings.Contains(url, "youtube.com") || strings.Contains(url, "youtu.be") {
		if !config.IsYouTubeEnabled() {
			return &ExtractionStrategy{
				Platform: "youtube",
				Methods: []ExtractionMethod{},
				HasFixedLink: false,
			}
		}
		// Community posts: browser extraction only (yt-dlp doesn't support them)
		if strings.Contains(url, "/post/") {
			return &ExtractionStrategy{
				Platform: "youtube",
				Methods: []ExtractionMethod{
					MethodBrowser, // CLI browser with enhanced YouTube community post extraction
				},
				HasFixedLink: false,
			}
		}
		// Regular videos: yt-dlp first, then browser fallback
		return &ExtractionStrategy{
			Platform: "youtube",
			Methods: []ExtractionMethod{
				MethodYtDlp,
				MethodBrowser, // Fallback for when yt-dlp fails
			},
			HasFixedLink: false,
		}
	}
	
	
	// Reddit: yt-dlp works well for video posts, browser fallback for images
	if strings.Contains(url, "reddit.com") {
		if !config.IsRedditEnabled() {
			return &ExtractionStrategy{
				Platform: "reddit",
				Methods: []ExtractionMethod{},
				HasFixedLink: false,
			}
		}
		return &ExtractionStrategy{
			Platform: "reddit",
			Methods: []ExtractionMethod{
				MethodYtDlp,
				MethodBrowser,
			},
			HasFixedLink: false,
		}
	}
	
	// Facebook: yt-dlp first (works for some video posts), then browser fallback
	if strings.Contains(url, "facebook.com") {
		if !config.IsFacebookEnabled() {
			return &ExtractionStrategy{
				Platform: "facebook",
				Methods: []ExtractionMethod{},
				HasFixedLink: false,
			}
		}
		return &ExtractionStrategy{
			Platform: "facebook",
			Methods: []ExtractionMethod{
				MethodYtDlp,
				MethodBrowser,
			},
			HasFixedLink: false,
		}
	}
	
	// Default: try yt-dlp then browser
	return &ExtractionStrategy{
		Platform: "generic",
		Methods: []ExtractionMethod{
			MethodYtDlp,
			MethodBrowser,
		},
		HasFixedLink: false,
	}
}

// GetFixedURL returns the fixed URL for platforms that support it
// This is the user-facing URL (e.g., instagramez for Instagram)
func (es *ExtractionStrategy) GetFixedURL(originalURL string) string {
	if !es.HasFixedLink {
		return ""
	}
	
	switch es.Platform {
	case "instagram":
		// Replace instagram.com with the configured service domain
		service := config.GetInstagramService()
		if service == "kkinstagram" {
			result := strings.Replace(originalURL, "https://www.instagram.com", "https://www.kkinstagram.com", 1)
			return strings.Replace(result, "https://instagram.com", "https://www.kkinstagram.com", 1)
		}
		// Default to instagramez
		return strings.Replace(strings.Replace(originalURL, "https://www.instagram.com", "https://www.instagramez.com", 1), "https://instagram.com", "https://instagramez.com", 1)
	case "tiktok":
		return strings.Replace(originalURL, "tiktok.com", es.FixedLinkDomain, 1)
	case "twitter":
		if strings.Contains(originalURL, "twitter.com") {
			return strings.Replace(originalURL, "twitter.com", es.FixedLinkDomain, 1)
		}
		return strings.Replace(originalURL, "x.com", es.FixedLinkDomain, 1)
	}
	
	return ""
}

// GetInternalFixedURL returns the internal fixed URL for downloading
// This may differ from the user-facing URL (e.g., ddinstagram for internal downloading)
func (es *ExtractionStrategy) GetInternalFixedURL(originalURL string) string {
	if !es.HasFixedLink {
		return ""
	}
	
	switch es.Platform {
	case "instagram":
		// Use ddinstagram for internal downloading (it works better)
		return strings.Replace(originalURL, "https://www.instagram.com", "https://d.ddinstagram.com", 1)
	case "tiktok":
		return strings.Replace(originalURL, "tiktok.com", es.FixedLinkDomain, 1)
	case "twitter":
		if strings.Contains(originalURL, "twitter.com") {
			return strings.Replace(originalURL, "twitter.com", es.FixedLinkDomain, 1)
		}
		return strings.Replace(originalURL, "x.com", es.FixedLinkDomain, 1)
	}
	
	return ""
}

// ShouldUseBrowser returns true if browser extraction should be tried for this strategy
func (es *ExtractionStrategy) ShouldUseBrowser() bool {
	for _, method := range es.Methods {
		if method == MethodBrowser {
			return true
		}
	}
	return false
}

// LogStrategy logs the extraction strategy for debugging
func (es *ExtractionStrategy) LogStrategy() {
	methodNames := make([]string, len(es.Methods))
	for i, method := range es.Methods {
		switch method {
		case MethodYtDlp:
			methodNames[i] = "yt-dlp"
		case MethodBrowser:
			methodNames[i] = "browser"
		case MethodFixedLink:
			methodNames[i] = "fixed-link"
		default:
			methodNames[i] = "unknown"
		}
	}
	
	log.Printf("📋 Extraction strategy for %s: %s", es.Platform, strings.Join(methodNames, " → "))
	if es.HasFixedLink {
		if es.Platform == "instagram" {
			service := config.GetInstagramService()
			if service == "kkinstagram" {
				log.Printf("   Fixed link: ddinstagram (internal), kkinstagram (user-facing)")
			} else {
				log.Printf("   Fixed link: ddinstagram (internal), instagramez (user-facing)")
			}
		} else {
			log.Printf("   Fixed link domain: %s", es.FixedLinkDomain)
		}
	}
}