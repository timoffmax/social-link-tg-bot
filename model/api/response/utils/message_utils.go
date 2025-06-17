package utils

import (
	"github.com/timoffmax/social-link-bot/model/api/response"
	"regexp"
	"strings"
)

func GetAllLinks(message *response.TgMessage) []string {
	instagramLinks := GetInstagramLinks(message)
	tikTokLinks := GetTikTokLinks(message)
	twitterLinks := GetTwitterLinks(message)
	youtubeLinks := GetYouTubeLinks(message)
	ninegagLinks := Get9GAGLinks(message)
	
	result := append(append(instagramLinks, tikTokLinks...), twitterLinks...)
	result = append(result, youtubeLinks...)
	result = append(result, ninegagLinks...)

	return result
}

func GetAllSocialLinks(message *response.TgMessage) []string {
	instagramLinks := GetOriginalInstagramLinks(message)
	tikTokLinks := GetOriginalTikTokLinks(message)
	twitterLinks := GetOriginalTwitterLinks(message)
	youtubeLinks := GetOriginalYouTubeLinks(message)
	ninegagLinks := GetOriginal9GAGLinks(message)
	
	result := append(append(instagramLinks, tikTokLinks...), twitterLinks...)
	result = append(result, youtubeLinks...)
	result = append(result, ninegagLinks...)

	return result
}

func GetInstagramLinks(message *response.TgMessage) []string {
	regex := regexp.MustCompile(`https:\/\/(www\.)?(instagram)\.com\/[^\s]+`)
	result := regex.FindAllString(message.Text, -1)

	for i, url := range result {
		fixedUrl := fixInstagramLink(url)
		result[i] = fixedUrl
	}

	return result
}

func GetTikTokLinks(message *response.TgMessage) []string {
	regex := regexp.MustCompile(`https:\/\/(vm\.|www\.|m\.)?(tiktok)\.com\/[^\s]+`)
	result := regex.FindAllString(message.Text, -1)

	for i, url := range result {
		fixedUrl := fixTikTokLink(url)
		result[i] = fixedUrl
	}

	return result
}

func GetTwitterLinks(message *response.TgMessage) []string {
	regex := regexp.MustCompile(`https:\/\/(twitter|x)\.com\/[^\s]+`)
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
	regex := regexp.MustCompile(`https:\/\/(www\.)?instagram\.com\/[^\s]+`)
	return regex.FindAllString(message.Text, -1)
}

func GetOriginalTikTokLinks(message *response.TgMessage) []string {
	regex := regexp.MustCompile(`https:\/\/(vm\.|www\.|m\.)?tiktok\.com\/[^\s]+`)
	return regex.FindAllString(message.Text, -1)
}

func GetOriginalTwitterLinks(message *response.TgMessage) []string {
	regex := regexp.MustCompile(`https:\/\/(twitter|x)\.com\/[^\s]+`)
	return regex.FindAllString(message.Text, -1)
}


func GetYouTubeLinks(message *response.TgMessage) []string {
	// Match YouTube Shorts URLs: youtube.com/shorts/*, youtu.be/*, m.youtube.com/shorts/*
	regex := regexp.MustCompile(`https:\/\/(www\.|m\.)?(youtube\.com\/shorts\/|youtu\.be\/)[^\s]+`)
	result := regex.FindAllString(message.Text, -1)
	
	// No fixed link service for YouTube, return as-is
	return result
}

func GetOriginalYouTubeLinks(message *response.TgMessage) []string {
	// Match YouTube Shorts URLs: youtube.com/shorts/*, youtu.be/*, m.youtube.com/shorts/*
	regex := regexp.MustCompile(`https:\/\/(www\.|m\.)?(youtube\.com\/shorts\/|youtu\.be\/)[^\s]+`)
	return regex.FindAllString(message.Text, -1)
}

func Get9GAGLinks(message *response.TgMessage) []string {
	regex := regexp.MustCompile(`https:\/\/(www\.)?9gag\.com\/gag\/[^\s]+`)
	result := regex.FindAllString(message.Text, -1)
	
	// No fixed link service for 9GAG, return as-is
	return result
}

func GetOriginal9GAGLinks(message *response.TgMessage) []string {
	regex := regexp.MustCompile(`https:\/\/(www\.)?9gag\.com\/gag\/[^\s]+`)
	return regex.FindAllString(message.Text, -1)
}
