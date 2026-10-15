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
// free IP-robust kkinstagram redirector, then the OG-thumbnail preview as a
// last resort. Methods run sequentially.
func instagramExtractionMethods() []ExtractionMethod {
	return []ExtractionMethod{MethodKKInstagram, MethodInstagramOG}
}

// threadsExtractionStrategy reads the post from Meta's embed (every item, in
// order), then from the vxthreads mirror (datacenter-robust, but only one
// video and image of a mixed carousel). Disabled Threads gets no methods.
func threadsExtractionStrategy() *ExtractionStrategy {
	result := &ExtractionStrategy{
		Platform:   ThreadsPlatform,
		Methods:    []ExtractionMethod{MethodThreadsEmbed, MethodThreadsVxthreads},
		Sequential: true,
	}

	if config.IsThreadsEnabled() == false {
		result = &ExtractionStrategy{Platform: ThreadsPlatform, Methods: []ExtractionMethod{}}
	}

	return result
}

// ExtractionMethod represents a method for extracting media
type ExtractionMethod int

const (
	MethodYtDlp ExtractionMethod = iota
	MethodBrowser
	MethodFixedLink
	MethodRedditAPI
	MethodRedditVxreddit
	MethodRapidAPILinks
	MethodInstaFix
	MethodKKInstagram
	MethodInstagramOG
	MethodYouTubeApify
	MethodStoriesApify
	MethodYouTubePost
	MethodThreadsEmbed
	MethodThreadsVxthreads
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

// GetExtractionStrategy returns the optimal extraction strategy for a given
// URL. Threads is checked first, by host: a username such as "@fox.com" must
// not route a Threads post to another platform.
func GetExtractionStrategy(url string) *ExtractionStrategy {
	result := threadsExtractionStrategy()
	isThreads := IsThreadsURL(url)

	if isThreads == false {
		result = getPlatformExtractionStrategy(url)
	}

	return result
}

// getPlatformExtractionStrategy returns the strategy of the other platforms.
func getPlatformExtractionStrategy(url string) *ExtractionStrategy {
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

		// Instagram stories: the paid Apify stories scraper (the RapidAPI
		// endpoint, browser and yt-dlp all fail: stories need a login).
		if strings.Contains(url, "/stories/") {
			log.Printf("📖 Instagram story - Apify stories scraper")
			return &ExtractionStrategy{
				Platform: "instagram",
				Methods: []ExtractionMethod{
					MethodStoriesApify,
				},
				HasFixedLink: false,
			}
		}

		// Instagram posts, reels and other content: kkinstagram, then OG preview.
		return &ExtractionStrategy{
			Platform:   "instagram",
			Methods:    instagramExtractionMethods(),
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
		// Community posts: text and images read from the post page (served to
		// the datacenter IP; yt-dlp doesn't support posts).
		if strings.Contains(url, "/post/") {
			return &ExtractionStrategy{
				Platform: "youtube",
				Methods: []ExtractionMethod{
					MethodYouTubePost,
				},
				HasFixedLink: false,
			}
		}
		// Shorts (the only videos detected): yt-dlp, which YouTube blocks from
		// datacenter IPs since 2026-08, then the paid Apify downloader.
		return &ExtractionStrategy{
			Platform: "youtube",
			Methods: []ExtractionMethod{
				MethodYtDlp,
				MethodYouTubeApify,
			},
			HasFixedLink: false,
			Sequential:   true,
		}
	}

	// Reddit: vxreddit first (works from the datacenter IP, resolves /s/ share
	// links), then the JSON API (text posts; Reddit returns 403 to datacenter
	// IPs), then yt-dlp.
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
				MethodRedditVxreddit,
				MethodRedditAPI,
				MethodYtDlp,
			},
			HasFixedLink: false,
			Sequential:   true,
		}
	}

	// Facebook: yt-dlp (videos and reels; photo posts are unsupported)
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
			},
			HasFixedLink: false,
		}
	}

	// 9GAG: yt-dlp, then the 9GAG API helper for photo posts
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
		case MethodRedditAPI:
			methodNames[i] = "reddit-api"
		case MethodRedditVxreddit:
			methodNames[i] = "vxreddit"
		case MethodRapidAPILinks:
			methodNames[i] = "rapidapi-links"
		case MethodInstaFix:
			methodNames[i] = "instafix"
		case MethodKKInstagram:
			methodNames[i] = "kkinstagram"
		case MethodInstagramOG:
			methodNames[i] = "instagram-og"
		case MethodYouTubeApify:
			methodNames[i] = "youtube-apify"
		case MethodStoriesApify:
			methodNames[i] = "stories-apify"
		case MethodYouTubePost:
			methodNames[i] = "youtube-post"
		case MethodThreadsEmbed:
			methodNames[i] = "threads-embed"
		case MethodThreadsVxthreads:
			methodNames[i] = "threads-vxthreads"
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
