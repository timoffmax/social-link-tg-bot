package utils

import (
	"github.com/timoffmax/social-link-bot/config"
	"github.com/timoffmax/social-link-bot/model/api/response"
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
	
	if config.Is9GAGEnabled() {
		ninegagLinks := Get9GAGLinks(message)
		result = append(result, ninegagLinks...)
	}
	
	if config.IsRedditEnabled() {
		redditLinks := GetRedditLinks(message)
		result = append(result, redditLinks...)
	}
	
	if config.IsFacebookEnabled() {
		facebookLinks := GetFacebookLinks(message)
		result = append(result, facebookLinks...)
	}

	return result
}

func GetAllSocialLinks(message *response.TgMessage) []string {
	var result []string
	
	if config.IsInstagramEnabled() {
		instagramLinks := GetOriginalInstagramLinks(message)
		result = append(result, instagramLinks...)
	}
	
	if config.IsTikTokEnabled() {
		tikTokLinks := GetOriginalTikTokLinks(message)
		result = append(result, tikTokLinks...)
	}
	
	if config.IsTwitterEnabled() {
		twitterLinks := GetOriginalTwitterLinks(message)
		result = append(result, twitterLinks...)
	}
	
	if config.IsYouTubeEnabled() {
		youtubeLinks := GetOriginalYouTubeLinks(message)
		result = append(result, youtubeLinks...)
	}
	
	if config.Is9GAGEnabled() {
		ninegagLinks := GetOriginal9GAGLinks(message)
		result = append(result, ninegagLinks...)
	}
	
	if config.IsRedditEnabled() {
		redditLinks := GetOriginalRedditLinks(message)
		result = append(result, redditLinks...)
	}
	
	if config.IsFacebookEnabled() {
		facebookLinks := GetOriginalFacebookLinks(message)
		result = append(result, facebookLinks...)
	}

	return result
}

func GetInstagramLinks(message *response.TgMessage) []string {
	regex := regexp.MustCompile(`https?:\/\/(www\.)?(instagram)\.com\/[^\s]+`)
	result := regex.FindAllString(message.Text, -1)

	for i, url := range result {
		fixedUrl := fixInstagramLink(url)
		result[i] = fixedUrl
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
	searchFor := "instagram"
	replaceWith := "instagramez"
	result := strings.Replace(link, searchFor, replaceWith, -1)

	return result
}

func fixTikTokLink(link string) string {
	searchFor := "tiktok"
	replaceWith := "vxtiktok"
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
	regex := regexp.MustCompile(`https?:\/\/(www\.)?instagram\.com\/[^\s]+`)
	return regex.FindAllString(message.Text, -1)
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

func Get9GAGLinks(message *response.TgMessage) []string {
	regex := regexp.MustCompile(`https?:\/\/(www\.)?9gag\.com\/gag\/[^\s]+`)
	result := regex.FindAllString(message.Text, -1)
	
	// No fixed link service for 9GAG, return as-is
	return result
}

func GetOriginal9GAGLinks(message *response.TgMessage) []string {
	regex := regexp.MustCompile(`https?:\/\/(www\.)?9gag\.com\/gag\/[^\s]+`)
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
