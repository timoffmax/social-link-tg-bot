package services

import (
	"github.com/timoffmax/social-link-bot/config"
	"log"
	"strings"
)

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

// redditExtractionStrategy is vxreddit, the JSON API when proxies are on and
// configured (Reddit blocks its .json without them), then yt-dlp. Disabled
// Reddit gets no methods.
func redditExtractionStrategy() *ExtractionStrategy {
	methods := []ExtractionMethod{MethodRedditVxreddit}
	proxies := config.GetYtDlpProxies()
	hasProxies := (config.IsProxyEnabled() == true) && (len(proxies) > 0)

	if hasProxies == true {
		methods = append(methods, MethodRedditAPI)
	}

	methods = append(methods, MethodYtDlp)

	if config.IsRedditEnabled() == false {
		methods = []ExtractionMethod{}
	}

	result := &ExtractionStrategy{Platform: RedditPlatform, Methods: methods, Sequential: true}

	return result
}

// facebookExtractionStrategy orders the methods by link shape
// (facebookLinkMethods: photos via facebed only, posts via facebed, yt-dlp,
// then facebed's video, videos via yt-dlp then facebed with its video).
// Disabled Facebook gets no methods.
func facebookExtractionStrategy(rawURL string) *ExtractionStrategy {
	methods := facebookLinkMethods(rawURL)

	if config.IsFacebookEnabled() == false {
		methods = []ExtractionMethod{}
	}

	result := &ExtractionStrategy{Platform: FacebookPlatform, Methods: methods, Sequential: true}

	return result
}

// youtubeShortsStrategy is the chain of Shorts (the only videos detected):
// the home relay then the paid Apify downloader when the relay is usable
// for YouTube (isRelayUsableFor, the same check that builds its extractor),
// else yt-dlp (YouTube blocks it from datacenter IPs since 2026-08) then
// Apify.
func youtubeShortsStrategy() *ExtractionStrategy {
	methods := []ExtractionMethod{MethodYtDlp, MethodYouTubeApify}
	isRelayOn := isRelayUsableFor(relayPlatformYouTube)

	// With the relay configured the server's yt-dlp stays out of the chain
	// even while the relay is down or disabled on its side (the owner's
	// decision): it has not fetched a Short from the datacenter IP since
	// 2026-08, so it would only spend chain time before Apify.
	if isRelayOn == true {
		methods = []ExtractionMethod{MethodYouTubeRelay, MethodYouTubeApify}
	}

	result := &ExtractionStrategy{Platform: youtubePlatform, Methods: methods, Sequential: true}

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
	MethodKKInstagram
	MethodInstagramOG
	MethodYouTubeApify
	MethodStoriesApify
	MethodYouTubePost
	MethodThreadsEmbed
	MethodThreadsVxthreads
	MethodFacebookFacebed
	MethodStoriesHiker
	MethodFacebookFacebedVideo
	MethodYouTubeRelay
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
	// Instagram: Apify for stories, kkinstagram then the OG preview for the rest.
	// Also accept preview-service URLs so forwarded preview links are still processed.
	if IsInstagramLikeURL(url) {
		if !config.IsInstagramEnabled() {
			return &ExtractionStrategy{
				Platform:     "instagram",
				Methods:      []ExtractionMethod{},
				HasFixedLink: false,
			}
		}

		// Instagram stories: the paid providers in INSTAGRAM_PAID_PROVIDERS
		// order (the RapidAPI endpoint, browser and yt-dlp all fail: stories
		// need a login), strictly one after the other.
		if strings.Contains(url, "/stories/") {
			log.Printf("📖 Instagram story - paid stories providers")
			return &ExtractionStrategy{
				Platform:     "instagram",
				Methods:      instagramStoriesMethods(),
				HasFixedLink: false,
				Sequential:   true,
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
		return youtubeShortsStrategy()
	}

	// Reddit: vxreddit (works from the datacenter IP, resolves /s/ share links,
	// reads every post shape), then the JSON API only with proxies (Reddit
	// answers .json with "blocked by network security" even after its JS
	// challenge is solved), then yt-dlp.
	if strings.Contains(url, "reddit.com") {
		return redditExtractionStrategy()
	}

	// Facebook: photos via facebed, posts via facebed, yt-dlp, then facebed's video, videos via yt-dlp then facebed.
	if strings.Contains(url, "facebook.com") {
		return facebookExtractionStrategy(url)
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
// (TikTok and Twitter/X).
func (es *ExtractionStrategy) GetFixedURL(originalURL string) string {
	if !es.HasFixedLink {
		return ""
	}

	switch es.Platform {
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
		case MethodFacebookFacebed:
			methodNames[i] = "facebed"
		case MethodStoriesHiker:
			methodNames[i] = "stories-hiker"
		case MethodFacebookFacebedVideo:
			methodNames[i] = "facebed-video"
		case MethodYouTubeRelay:
			methodNames[i] = "youtube-relay"
		default:
			methodNames[i] = "unknown"
		}
	}

	log.Printf("📋 Extraction strategy for %s: %s", es.Platform, strings.Join(methodNames, " → "))
	if es.HasFixedLink {
		log.Printf("   Fixed link domain: %s", es.FixedLinkDomain)
	}
}
