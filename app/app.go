package app

import (
	"github.com/joho/godotenv"
	"log"
)

func Run() {
	initApp()
	listenForUpdates()
}

func initApp() {
	loadEnvFile()
}

func loadEnvFile() {
	err := godotenv.Load()

	if err != nil {
		log.Fatal("Error loading .env file")
	}
}
