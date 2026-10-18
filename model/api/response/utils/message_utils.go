package utils

import (
	"fmt"

	"github.com/timoffmax/social-link-bot/config"
	"github.com/timoffmax/social-link-bot/model/api/response"
	"github.com/timoffmax/social-link-bot/services"
	"regexp"
	"strings"
)

func GetAllLinks(message *response.TgMessage) []string {
	var result []string

	if config.IsInstagramEnabled() {
		instagramLinks := GetInstagramLinks(message)
		result = append(result, instagramLinks...)
	}

	if config.IsTikTokEnabled() {
		tikTokLinks := GetTikTokLinks(message)
		result = append(result, tikTokLinks...)
	}

	if config.IsTwitterEnabled() {
		twitterLinks := GetTwitterLinks(message)
		result = append(result, twitterLinks...)
	}

	if config.IsYouTubeEnabled() {
		youtubeLinks := GetYouTubeLinks(message)
		result = append(result, youtubeLinks...)
	}

	if config.IsRedditEnabled() {
		redditLinks := GetRedditLinks(message)
		result = append(result, redditLinks...)
	}

	if config.IsFacebookEnabled() {
		facebookLinks := GetFacebookLinks(message)
		result = append(result, facebookLinks...)
	}

	if config.Is9GAGEnabled() {
		ninegagLinks := Get9GAGLinks(message)
		result = append(result, ninegagLinks...)
	}

	if config.IsThreadsEnabled() {
		threadsLinks := GetThreadsLinks(message)
		result = append(result, threadsLinks...)
	}

	return result
}

func GetAllSocialLinks(message *response.TgMessage, chatID int64) []string {
	var result []string

	if config.IsInstagramEnabled() && services.IsPlatformEnabledForChat(chatID, "instagram") {
		instagramLinks := GetOriginalInstagramLinks(message)
		result = append(result, instagramLinks...)
	}

	if config.IsTikTokEnabled() && services.IsPlatformEnabledForChat(chatID, "tiktok") {
		tikTokLinks := GetOriginalTikTokLinks(message)
		result = append(result, tikTokLinks...)
	}

	if config.IsTwitterEnabled() && services.IsPlatformEnabledForChat(chatID, "twitter") {
		twitterLinks := GetOriginalTwitterLinks(message)
		result = append(result, twitterLinks...)
	}

	if config.IsYouTubeEnabled() && services.IsPlatformEnabledForChat(chatID, "youtube") {
		youtubeLinks := GetOriginalYouTubeLinks(message)
		result = append(result, youtubeLinks...)
	}

	if config.IsRedditEnabled() && services.IsPlatformEnabledForChat(chatID, "reddit") {
		redditLinks := GetOriginalRedditLinks(message)
		result = append(result, redditLinks...)
	}

	if config.IsFacebookEnabled() && services.IsPlatformEnabledForChat(chatID, "facebook") {
		facebookLinks := GetOriginalFacebookLinks(message)
		result = append(result, facebookLinks...)
	}

	if config.Is9GAGEnabled() && services.IsPlatformEnabledForChat(chatID, "9gag") {
		ninegagLinks := GetOriginal9GAGLinks(message)
		result = append(result, ninegagLinks...)
	}

	isThreadsOn := (config.IsThreadsEnabled() == true) && (services.IsPlatformEnabledForChat(chatID, services.ThreadsPlatform) == true)

	if isThreadsOn == true {
		threadsLinks := GetOriginalThreadsLinks(message)
		result = append(result, threadsLinks...)
	}

	return result
}

func GetInstagramLinks(message *response.TgMessage) []string {
	// Match instagram.com plus any recognized proxy services (live or deprecated),
	// so forwarded preview links still flow through the pipeline.
	regex := regexp.MustCompile(`https?:\/\/(www\.)?(instagram|toinstagram|onebigram|kksave|instagramez)\.com\/[^\s]+`)
	result := regex.FindAllString(message.Text, -1)

	for i, url := range result {
		if strings.Contains(url, "instagram.com") {
			result[i] = fixInstagramLink(url)
		}
	}

	return result
}

func GetTikTokLinks(message *response.TgMessage) []string {
	regex := regexp.MustCompile(`https?:\/\/(vm\.|vt\.|www\.|m\.)?(tiktok)\.com\/[^\s]+`)
	result := regex.FindAllString(message.Text, -1)

	for i, url := range result {
		fixedUrl := fixTikTokLink(url)
		result[i] = fixedUrl
	}

	return result
}

func GetTwitterLinks(message *response.TgMessage) []string {
	regex := regexp.MustCompile(`https?:\/\/(twitter|x)\.com\/[^\s]+`)
	result := regex.FindAllString(message.Text, -1)

	for i, url := range result {
		fixedUrl := fixTwitterLink(url)
		result[i] = fixedUrl
	}

	return result
}

func fixInstagramLink(link string) string {
	domain := config.GetInstagramService() + ".com"
	result := strings.Replace(link, "https://www.instagram.com", "https://"+domain, 1)
	result = strings.Replace(result, "https://instagram.com", "https://"+domain, 1)

	return result
}

func fixTikTokLink(link string) string {
	searchFor := "tiktok"
	replaceWith := "tiktxk"
	result := strings.Replace(link, searchFor, replaceWith, -1)

	return result
}

func fixTwitterLink(link string) string {
	// Handle both twitter.com and x.com URLs
	if strings.Contains(link, "twitter.com") {
		result := strings.Replace(link, "twitter.com", "fixupx.com", -1)
		return result
	} else if strings.Contains(link, "x.com") {
		result := strings.Replace(link, "x.com", "fixupx.com", -1)
		return result
	}
	return link
}

func GetOriginalInstagramLinks(message *response.TgMessage) []string {
	// Match only original instagram.com URLs, skip proxy/fixed-link services
	regex := regexp.MustCompile(`https?:\/\/(www\.)?instagram\.com\/[^\s]+`)
	links := regex.FindAllString(message.Text, -1)

	// Clean tracking parameters from Instagram URLs
	for i, link := range links {
		links[i] = cleanInstagramURL(link)
	}

	return links
}

// cleanInstagramURL removes tracking parameters like igsh from Instagram URLs
func cleanInstagramURL(url string) string {
	// Remove query parameters like ?igsh=...
	if idx := strings.Index(url, "?"); idx != -1 {
		url = url[:idx]
	}

	// Ensure URL ends with trailing slash for consistency
	if !strings.HasSuffix(url, "/") {
		url = url + "/"
	}

	return url
}

func GetOriginalTikTokLinks(message *response.TgMessage) []string {
	regex := regexp.MustCompile(`https?:\/\/(vm\.|vt\.|www\.|m\.)?tiktok\.com\/[^\s]+`)
	return regex.FindAllString(message.Text, -1)
}

func GetOriginalTwitterLinks(message *response.TgMessage) []string {
	regex := regexp.MustCompile(`https?:\/\/(twitter|x)\.com\/[^\s]+`)
	return regex.FindAllString(message.Text, -1)
}

func GetYouTubeLinks(message *response.TgMessage) []string {
	// Match only YouTube Shorts and community posts (youtube.com/shorts/*, youtube.com/post/*, m. variants);
	// regular videos (watch, youtu.be) are deliberately not processed
	regex := regexp.MustCompile(`https?:\/\/(www\.|m\.)?youtube\.com\/(shorts|post)\/[^\s]+`)
	result := regex.FindAllString(message.Text, -1)

	// No fixed link service for YouTube, return as-is
	return result
}

func GetOriginalYouTubeLinks(message *response.TgMessage) []string {
	// Match only YouTube Shorts and community posts (youtube.com/shorts/*, youtube.com/post/*, m. variants);
	// regular videos (watch, youtu.be) are deliberately not processed
	regex := regexp.MustCompile(`https?:\/\/(www\.|m\.)?youtube\.com\/(shorts|post)\/[^\s]+`)
	return regex.FindAllString(message.Text, -1)
}

// redditLinkExpression matches Reddit post links (www/old/m/np hosts, /r/...
// and /comments/<id>), redditShortLinkExpression the redd.it short links
// (not the i.redd.it / v.redd.it media hosts).
const (
	redditLinkExpression      = `https?:\/\/(www\.|old\.|m\.|np\.)?reddit\.com\/(r|comments)\/[^\s]+`
	redditShortLinkExpression = `https?:\/\/(www\.)?redd\.it\/([A-Za-z0-9]+)`
)

var (
	redditShortLinkPattern = regexp.MustCompile(redditShortLinkExpression)

	// redditAnyLinkExpression and redditAnyLinkPattern find both shapes in
	// text order.
	redditAnyLinkExpression = fmt.Sprintf("%s|%s", redditLinkExpression, redditShortLinkExpression)
	redditAnyLinkPattern    = regexp.MustCompile(redditAnyLinkExpression)
)

// redditShortLinkTarget is where a redd.it/<id> link leads.
const redditShortLinkTarget = "https://www.reddit.com/comments/%s"

// GetRedditLinks returns the Reddit post links of a message; redd.it short
// links come as reddit.com/comments/<id>, where they redirect.
func GetRedditLinks(message *response.TgMessage) []string {
	result := GetOriginalRedditLinks(message)

	return result
}

// GetOriginalRedditLinks returns the Reddit post links of a message in text
// order; redd.it/<id> comes as reddit.com/comments/<id>.
func GetOriginalRedditLinks(message *response.TgMessage) []string {
	result := []string{}
	links := redditAnyLinkPattern.FindAllString(message.Text, -1)

	for _, link := range links {
		normalized := normalizeRedditLink(link)
		result = append(result, normalized)
	}

	return result
}

// normalizeRedditLink turns a redd.it/<id> link into reddit.com/comments/<id>.
func normalizeRedditLink(link string) string {
	result := link
	match := redditShortLinkPattern.FindStringSubmatch(link)
	isShort := (len(match) == 3) && (match[0] == link)

	if isShort == true {
		result = fmt.Sprintf(redditShortLinkTarget, match[2])
	}

	return result
}

func GetFacebookLinks(message *response.TgMessage) []string {
	regex := regexp.MustCompile(`https?:\/\/(www\.|m\.|mobile\.)?facebook\.com\/[^\s]+`)
	result := regex.FindAllString(message.Text, -1)

	// No fixed link service for Facebook, return as-is
	return result
}

func GetOriginalFacebookLinks(message *response.TgMessage) []string {
	regex := regexp.MustCompile(`https?:\/\/(www\.|m\.|mobile\.)?facebook\.com\/[^\s]+`)
	return regex.FindAllString(message.Text, -1)
}

// ninegagLinkPattern matches 9GAG post links (www. and the m. mobile host).
var ninegagLinkPattern = regexp.MustCompile(`https?:\/\/(www\.|m\.)?9gag\.com\/gag\/[^\s]+`)

// ninegagMobileHostPattern matches the scheme and m. host of a 9GAG link.
var ninegagMobileHostPattern = regexp.MustCompile(`^https?:\/\/m\.9gag\.com\/`)

// ninegagDesktopRoot replaces the m. host (the API helper and yt-dlp read
// 9gag.com links only).
const ninegagDesktopRoot = "https://9gag.com/"

// Get9GAGLinks returns the 9GAG post links of a message (see
// GetOriginal9GAGLinks).
func Get9GAGLinks(message *response.TgMessage) []string {
	result := GetOriginal9GAGLinks(message)

	return result
}

// GetOriginal9GAGLinks returns the 9GAG post links of a message; an
// m.9gag.com link comes as its 9gag.com form.
func GetOriginal9GAGLinks(message *response.TgMessage) []string {
	result := []string{}
	links := ninegagLinkPattern.FindAllString(message.Text, -1)

	for _, link := range links {
		desktop := ninegagMobileHostPattern.ReplaceAllString(link, ninegagDesktopRoot)
		result = append(result, desktop)
	}

	return result
}

// threadsLinkPattern matches Threads post links (threads.com / threads.net,
// "/@user/post/<code>", short "/t/<code>" and app "/share/<id>" links).
var threadsLinkPattern = regexp.MustCompile(`https?:\/\/(www\.)?threads\.(com|net)\/(@[A-Za-z0-9._]+\/post|t|share)\/[A-Za-z0-9_-]+[^\s]*`)

// GetThreadsLinks returns the Threads post links of a message.
func GetThreadsLinks(message *response.TgMessage) []string {
	result := threadsLinkPattern.FindAllString(message.Text, -1)

	return result
}

// GetOriginalThreadsLinks returns the Threads post links of a message (the
// same as GetThreadsLinks: Threads has no fixer-domain rewriting).
func GetOriginalThreadsLinks(message *response.TgMessage) []string {
	result := GetThreadsLinks(message)

	return result
}
