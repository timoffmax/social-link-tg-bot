package services

import (
	"github.com/timoffmax/social-link-bot/config"
	"log"
	"strings"
)

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
	if strings.Contains(url, "instagram.com") {
		if !config.IsInstagramEnabled() {
			return &ExtractionStrategy{
				Platform: "instagram",
				Methods: []ExtractionMethod{},
				HasFixedLink: false,
			}
		}
		// All Instagram content: yt-dlp first (fast and reliable), browser fallback, fixed link last resort
		return &ExtractionStrategy{
			Platform: "instagram",
			Methods: []ExtractionMethod{
				MethodYtDlp,
				MethodBrowser,
				MethodFixedLink,
			},
			HasFixedLink: true,
			FixedLinkDomain: "instagramez.com",
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
	
	// Twitter/X: yt-dlp first, then custom browser extractor for quotes, then fixed link
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
				MethodYtDlp,
				MethodBrowser, // Custom Twitter extractor with quote resolution
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
	
	// 9GAG: configurable enablement via ENABLE_9GAG environment variable
	if strings.Contains(url, "9gag.com") {
		if !config.Is9GAGEnabled() {
			// 9GAG disabled via configuration
			return &ExtractionStrategy{
				Platform: "9gag",
				Methods: []ExtractionMethod{},
				HasFixedLink: false,
			}
		}
		
		// 9GAG enabled - use browser extraction with proxy support
		return &ExtractionStrategy{
			Platform: "9gag",
			Methods: []ExtractionMethod{
				MethodBrowser, // CLI browser with proxy support
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
func (es *ExtractionStrategy) GetFixedURL(originalURL string) string {
	if !es.HasFixedLink {
		return ""
	}
	
	switch es.Platform {
	case "instagram":
		return strings.Replace(originalURL, "instagram.com", es.FixedLinkDomain, 1)
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
		log.Printf("   Fixed link domain: %s", es.FixedLinkDomain)
	}
}