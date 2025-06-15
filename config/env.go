package config

import (
	"github.com/joho/godotenv"
	"os"
)

const tgBotTokenKey = "TG_BOT_TOKEN"

func init() {
	loadConfig()
}

func loadConfig() {
	godotenv.Load()
}

func GetTgBotToken() string {
	return os.Getenv(tgBotTokenKey)
}

// Social media authentication configuration
func GetInstagramUsername() string {
	return os.Getenv("INSTAGRAM_USERNAME")
}

func GetInstagramPassword() string {
	return os.Getenv("INSTAGRAM_PASSWORD")
}

func GetTikTokUsername() string {
	return os.Getenv("TIKTOK_USERNAME")
}

func GetTikTokPassword() string {
	return os.Getenv("TIKTOK_PASSWORD")
}

func GetTwitterUsername() string {
	return os.Getenv("TWITTER_USERNAME")
}

func GetTwitterPassword() string {
	return os.Getenv("TWITTER_PASSWORD")
}

// Proxy configuration
func GetYtDlpProxy() string {
	return os.Getenv("YTDLP_PROXY")
}
