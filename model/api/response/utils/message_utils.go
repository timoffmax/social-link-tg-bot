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
	result := append(append(instagramLinks, tikTokLinks...), twitterLinks...)

	return result
}

func GetInstagramLinks(message *response.TgMessage) []string {
	regex := regexp.MustCompile(`https:\/\/www\.(instagram)\.com\/[\w\/\d\?\=]+`)
	result := regex.FindAllString(message.Text, -1)

	for i, url := range result {
		fixedUrl := fixInstagramLink(url)
		result[i] = fixedUrl
	}

	return result
}

func GetTikTokLinks(message *response.TgMessage) []string {
	regex := regexp.MustCompile(`https:\/\/vm\.(tiktok)\.com\/[\w\/\d\?\=]+`)
	result := regex.FindAllString(message.Text, -1)

	for i, url := range result {
		fixedUrl := fixTikTokLink(url)
		result[i] = fixedUrl
	}

	return result
}

func GetTwitterLinks(message *response.TgMessage) []string {
	regex := regexp.MustCompile(`https:\/\/(x)\.com\/[\w\/\d\?\=]+`)
	result := regex.FindAllString(message.Text, -1)

	for i, url := range result {
		fixedUrl := fixTwitterLink(url)
		result[i] = fixedUrl
	}

	return result
}

func fixInstagramLink(link string) string {
	searchFor := "instagram"
	replaceWith := "ddinstagram"
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
	searchFor := "x.com"
	replaceWith := "fixupx.com"
	result := strings.Replace(link, searchFor, replaceWith, -1)

	return result
}
