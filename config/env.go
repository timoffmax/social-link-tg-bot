package config

import (
	"github.com/joho/godotenv"
	"os"
)

const tgBotToken = "TG_BOT_TOKEN"

func init() {
	loadConfig()
}

func loadConfig() {
	godotenv.Load()
}

func GetTgBotToken() string {
	return os.Getenv(tgBotToken)
}
