package config

import (
	"github.com/joho/godotenv"
	"os"
)

const tgBotToken = "***REMOVED***"

func init() {
	loadConfig()
}

func loadConfig() {
	godotenv.Load()
}

func GetTgBotToken() string {
	return os.Getenv(tgBotToken)
}
