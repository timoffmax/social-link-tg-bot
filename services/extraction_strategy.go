package services

import (
	"github.com/timoffmax/social-link-bot/config"
	"log"
	"strings"
)

// getInstagramFixedDomain returns the configured Instagram service domain
func getInstagramFixedDomain() string {
	return config.GetInstagramService() + ".com"
}

// instagramExtractionMethods builds the ordered Instagram method chain: the
// keyed FastSaver primary (only when a key is configured), the free IP-robust
// redirectors (cobalt first when photoFirst, for full carousel coverage), and
// the OG-thumbnail preview as a last resort. Methods run sequentially.
func instagramExtractionMethods(photoFirst bool) []ExtractionMethod {
	methods := make([]ExtractionMethod, 0, 4)

	if config.GetFastSaverAPIKey() != "" {
		methods = append(methods, MethodFastSaver)
	}

	if photoFirst == true {
		methods = append(methods, MethodCobalt, MethodKKInstagram)
	} else {
		methods = append(methods, MethodKKInstagram, MethodCobalt)
	}

	methods = append(methods, MethodInstagramOG)

	return methods
}

// ExtractionMethod represents a method for extracting media
type ExtractionMethod int

const (
	MethodYtDlp ExtractionMethod = iota
	MethodBrowser
	MethodFixedLink
	MethodStoriesAPI
	MethodRedditAPI
	MethodRapidAPILinks
	MethodInstaFix
	MethodKKInstagram
	MethodCobalt
	MethodFastSaver
	MethodInstagramOG
	MethodNone
)

// ExtractionStrategy defines the order of methods to try for a platform
type ExtractionStrategy struct {
	Platform        string
	Methods         []ExtractionMethod
	HasFixedLink    bool
	FixedLinkDomain string
	// Sequential forces methods to run strictly in order (no leading concurrency),
	// so an explicitly preferred primary is fully tried before any fallback.
	Sequential bool
}

// GetExtractionStrategy returns the optimal extraction strategy for a given URL
func GetExtractionStrategy(url string) *ExtractionStrategy {
	// Instagram: RapidAPI for videos, InstaFix for photos, fixed link fallback.
	// Also accept InstaFix-service URLs so forwarded preview links are still processed.
	if IsInstagramLikeURL(url) {
		if !config.IsInstagramEnabled() {
			return &ExtractionStrategy{
				Platform:     "instagram",
				Methods:      []ExtractionMethod{},
				HasFixedLink: false,
			}
		}
		// Instagram videos (reels, IGTV): FastSaver (keyed) first, then free redirectors.
		if strings.Contains(url, "/reel/") || strings.Contains(url, "/tv/") || strings.Contains(url, "/reels/") {
			return &ExtractionStrategy{
				Platform:   "instagram",
				Methods:    instagramExtractionMethods(false),
				Sequential: true,
			}
		}

		// Instagram photos (/p/ URLs): may be carousels, so prefer cobalt (picker) among the free methods.
		if strings.Contains(url, "/p/") {
			log.Printf("📸 Instagram photo - FastSaver, then cobalt/kkinstagram")
			return &ExtractionStrategy{
				Platform:   "instagram",
				Methods:    instagramExtractionMethods(true),
				Sequential: true,
			}
		}
		// Instagram stories: use RapidAPI stories endpoint first
		if strings.Contains(url, "/stories/") {
			log.Printf("📖 Instagram story - will use Stories API with fallback")
			return &ExtractionStrategy{
				Platform: "instagram",
				Methods: []ExtractionMethod{
					MethodStoriesAPI,
					MethodBrowser,
					MethodYtDlp,
				},
				HasFixedLink: false,
			}
		}

		// Instagram other content: FastSaver first, then free redirectors.
		return &ExtractionStrategy{
			Platform:   "instagram",
			Methods:    instagramExtractionMethods(false),
			Sequential: true,
		}
	}

	// TikTok: different strategies for videos vs slideshows
	if strings.Contains(url, "tiktok.com") || strings.Contains(url, "vm.tiktok.com") {
		if !config.IsTikTokEnabled() {
			return &ExtractionStrategy{
				Platform:     "tiktok",
				Methods:      []ExtractionMethod{},
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
				HasFixedLink:    true,
				FixedLinkDomain: "tiktxk.com",
			}
		}
		// Regular videos: yt-dlp first, then fixed link for fallback
		return &ExtractionStrategy{
			Platform: "tiktok",
			Methods: []ExtractionMethod{
				MethodYtDlp,
				MethodFixedLink,
			},
			HasFixedLink:    true,
			FixedLinkDomain: "tiktxk.com",
		}
	}

	// Twitter/X: yt-dlp with hybrid approach (FxTwitter text + yt-dlp media)
	if strings.Contains(url, "twitter.com") || strings.Contains(url, "x.com") {
		if !config.IsTwitterEnabled() {
			return &ExtractionStrategy{
				Platform:     "twitter",
				Methods:      []ExtractionMethod{},
				HasFixedLink: false,
			}
		}
		return &ExtractionStrategy{
			Platform: "twitter",
			Methods: []ExtractionMethod{
				MethodYtDlp,
				MethodBrowser,
				MethodFixedLink,
			},
			HasFixedLink:    true,
			FixedLinkDomain: "fixupx.com",
		}
	}

	// YouTube: different strategies for videos vs community posts
	if strings.Contains(url, "youtube.com") || strings.Contains(url, "youtu.be") {
		if !config.IsYouTubeEnabled() {
			return &ExtractionStrategy{
				Platform:     "youtube",
				Methods:      []ExtractionMethod{},
				HasFixedLink: false,
			}
		}
		// Community posts: browser extraction only (yt-dlp doesn't support them)
		if strings.Contains(url, "/post/") {
			return &ExtractionStrategy{
				Platform: "youtube",
				Methods: []ExtractionMethod{
					MethodBrowser,
				},
				HasFixedLink: false,
			}
		}
		// Regular videos: yt-dlp first, then browser fallback
		return &ExtractionStrategy{
			Platform: "youtube",
			Methods: []ExtractionMethod{
				MethodYtDlp,
				MethodBrowser,
			},
			HasFixedLink: false,
		}
	}

	// Reddit: JSON API via proxy first, then yt-dlp and browser fallback
	if strings.Contains(url, "reddit.com") {
		if !config.IsRedditEnabled() {
			return &ExtractionStrategy{
				Platform:     "reddit",
				Methods:      []ExtractionMethod{},
				HasFixedLink: false,
			}
		}
		return &ExtractionStrategy{
			Platform: "reddit",
			Methods: []ExtractionMethod{
				MethodRedditAPI,
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
				Platform:     "facebook",
				Methods:      []ExtractionMethod{},
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

	// 9GAG: yt-dlp routes to CLI browser extraction, then universal browser fallback
	if strings.Contains(url, "9gag.com") {
		if !config.Is9GAGEnabled() {
			return &ExtractionStrategy{
				Platform:     "9gag",
				Methods:      []ExtractionMethod{},
				HasFixedLink: false,
			}
		}
		return &ExtractionStrategy{
			Platform: "9gag",
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

// IsInstagramLikeURL returns true if the URL points to Instagram or one of the
// proxy services we recognize (live or deprecated, so forwarded links still work).
func IsInstagramLikeURL(url string) bool {
	for _, host := range []string{"instagram.com", "toinstagram.com", "onebigram.com", "kksave.com", "instagramez.com"} {
		if strings.Contains(url, host) {
			return true
		}
	}

	return false
}

// GetFixedURL returns the fixed URL for platforms that support it
// This is the user-facing URL (e.g., toinstagram for Instagram)
func (es *ExtractionStrategy) GetFixedURL(originalURL string) string {
	if !es.HasFixedLink {
		return ""
	}

	switch es.Platform {
	case "instagram":
		domain := getInstagramFixedDomain()
		result := strings.Replace(originalURL, "https://www.instagram.com", "https://"+domain, 1)
		return strings.Replace(result, "https://instagram.com", "https://"+domain, 1)
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
// Uses the same domain as the user-facing URL; the legacy ddinstagram.com is dead.
func (es *ExtractionStrategy) GetInternalFixedURL(originalURL string) string {
	if !es.HasFixedLink {
		return ""
	}

	return es.GetFixedURL(originalURL)
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
		case MethodStoriesAPI:
			methodNames[i] = "stories-api"
		case MethodRedditAPI:
			methodNames[i] = "reddit-api"
		case MethodRapidAPILinks:
			methodNames[i] = "rapidapi-links"
		case MethodInstaFix:
			methodNames[i] = "instafix"
		case MethodKKInstagram:
			methodNames[i] = "kkinstagram"
		case MethodCobalt:
			methodNames[i] = "cobalt"
		case MethodFastSaver:
			methodNames[i] = "fastsaver"
		case MethodInstagramOG:
			methodNames[i] = "instagram-og"
		default:
			methodNames[i] = "unknown"
		}
	}

	log.Printf("📋 Extraction strategy for %s: %s", es.Platform, strings.Join(methodNames, " → "))
	if es.HasFixedLink {
		if es.Platform == "instagram" {
			log.Printf("   Fixed link: %s", getInstagramFixedDomain())
		} else {
			log.Printf("   Fixed link domain: %s", es.FixedLinkDomain)
		}
	}
}
