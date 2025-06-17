package services

import (
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
	// Instagram: different strategies for videos vs photos/stories
	if strings.Contains(url, "instagram.com") {
		// Videos (reels) work well with yt-dlp
		if strings.Contains(url, "/reel/") {
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
		// Photos and stories: fixed link first, then browser
		return &ExtractionStrategy{
			Platform: "instagram",
			Methods: []ExtractionMethod{
				MethodFixedLink,
				MethodBrowser,
				MethodYtDlp,
			},
			HasFixedLink: true,
			FixedLinkDomain: "instagramez.com",
		}
	}
	
	// TikTok: different strategies for videos vs slideshows
	if strings.Contains(url, "tiktok.com") {
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
		// Regular videos: yt-dlp first, then fixed link (browser blocks regular videos)
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
	
	// Twitter/X: yt-dlp first (works with cookies), fixed link as immediate fallback (no browser)
	if strings.Contains(url, "twitter.com") || strings.Contains(url, "x.com") {
		return &ExtractionStrategy{
			Platform: "twitter",
			Methods: []ExtractionMethod{
				MethodYtDlp,
				MethodFixedLink, // Skip browser, go straight to fixed link
			},
			HasFixedLink: true,
			FixedLinkDomain: "fixupx.com",
		}
	}
	
	// YouTube: yt-dlp only (no fixed link service)
	if strings.Contains(url, "youtube.com") || strings.Contains(url, "youtu.be") {
		return &ExtractionStrategy{
			Platform: "youtube",
			Methods: []ExtractionMethod{
				MethodYtDlp,
			},
			HasFixedLink: false,
		}
	}
	
	// 9GAG: browser only (yt-dlp never works for 9GAG, no fixed link service)
	if strings.Contains(url, "9gag.com") {
		return &ExtractionStrategy{
			Platform: "9gag",
			Methods: []ExtractionMethod{
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