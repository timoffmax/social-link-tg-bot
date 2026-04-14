package utils

import (
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

	return result
}

func GetInstagramLinks(message *response.TgMessage) []string {
	// Match both instagram.com and kksave.com URLs
	regex := regexp.MustCompile(`https?:\/\/(www\.)?(instagram|kksave)\.com\/[^\s]+`)
	result := regex.FindAllString(message.Text, -1)

	for i, url := range result {
		// Only fix Instagram URLs, leave kksave URLs as-is (they're already fixed)
		if strings.Contains(url, "instagram.com") {
			fixedUrl := fixInstagramLink(url)
			result[i] = fixedUrl
		}
		// kksave URLs are already "fixed", just return them
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
	service := config.GetInstagramService()
	
	if service == "kksave" {
		// Transform https://www.instagram.com/reel/XXX to https://www.kksave.com/reel/XXX
		result := strings.Replace(link, "https://www.instagram.com", "https://www.kksave.com", 1)
		result = strings.Replace(result, "https://instagram.com", "https://www.kksave.com", 1)
		return result
	}
	
	// Default to instagramez
	searchFor := "instagram"
	replaceWith := "instagramez"
	result := strings.Replace(link, searchFor, replaceWith, -1)

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
	// Match only original instagram.com URLs, skip fixed link services (kksave, instagramez)
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
	// Match YouTube Shorts, Posts, and Community posts: youtube.com/shorts/*, youtube.com/post/*, youtube.com/channel/*/community*, youtu.be/*, m.youtube.com/shorts/*
	regex := regexp.MustCompile(`https?:\/\/(www\.|m\.)?(youtube\.com\/(shorts|post)\/|youtube\.com\/channel\/[^\/]+\/community|youtu\.be\/)[^\s]+`)
	result := regex.FindAllString(message.Text, -1)
	
	// No fixed link service for YouTube, return as-is
	return result
}

func GetOriginalYouTubeLinks(message *response.TgMessage) []string {
	// Match YouTube Shorts, Posts, and Community posts: youtube.com/shorts/*, youtube.com/post/*, youtube.com/channel/*/community*, youtu.be/*, m.youtube.com/shorts/*
	regex := regexp.MustCompile(`https?:\/\/(www\.|m\.)?(youtube\.com\/(shorts|post)\/|youtube\.com\/channel\/[^\/]+\/community|youtu\.be\/)[^\s]+`)
	return regex.FindAllString(message.Text, -1)
}


func GetRedditLinks(message *response.TgMessage) []string {
	regex := regexp.MustCompile(`https?:\/\/(www\.|old\.|m\.|np\.)?reddit\.com\/r\/[^\s]+`)
	result := regex.FindAllString(message.Text, -1)
	
	// No fixed link service for Reddit, return as-is
	return result
}

func GetOriginalRedditLinks(message *response.TgMessage) []string {
	regex := regexp.MustCompile(`https?:\/\/(www\.|old\.|m\.|np\.)?reddit\.com\/r\/[^\s]+`)
	return regex.FindAllString(message.Text, -1)
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

func Get9GAGLinks(message *response.TgMessage) []string {
	regex := regexp.MustCompile(`https?:\/\/(www\.)?9gag\.com\/gag\/[^\s]+`)
	return regex.FindAllString(message.Text, -1)
}

func GetOriginal9GAGLinks(message *response.TgMessage) []string {
	regex := regexp.MustCompile(`https?:\/\/(www\.)?9gag\.com\/gag\/[^\s]+`)
	return regex.FindAllString(message.Text, -1)
}
