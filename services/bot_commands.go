package services

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/timoffmax/social-link-bot/config"
)

// BotCommand represents a bot command for Telegram
type BotCommand struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

// SetBotCommands sets the bot commands for the Telegram bot menu
func SetBotCommands() error {
	commands := []BotCommand{
		{Command: "menu", Description: "Show main menu"},
		{Command: "start", Description: "Show main menu and help"},
		{Command: "help", Description: "Show main menu and help"},
		{Command: "stats", Description: "View media statistics (today/week/month/year)"},
		{Command: "mylimit", Description: "Check your daily media limit"},
		{Command: "setlimit", Description: "Set user's daily limit (admin only)"},
		{Command: "setgrouplimit", Description: "Set group default limit (admin only)"},
		{Command: "setgrouptimezone", Description: "Set group timezone (admin only)"},
		{Command: "donate", Description: "Support the bot development (USDT)"},
		{Command: "report", Description: "Report a bug or issue"},
		{Command: "owner", Description: "Owner panel (owner only)"},
	}

	// Prepare the API request
	token := config.GetTgBotToken()
	url := fmt.Sprintf("https://api.telegram.org/bot%s/setMyCommands", token)

	// Prepare the request body
	requestBody := map[string]interface{}{
		"commands": commands,
	}

	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		return fmt.Errorf("failed to marshal commands: %w", err)
	}

	// Make the request
	resp, err := http.Post(url, "application/json", strings.NewReader(string(jsonBody)))
	if err != nil {
		return fmt.Errorf("failed to set bot commands: %w", err)
	}
	defer resp.Body.Close()

	// Check response
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to set bot commands, status code: %d", resp.StatusCode)
	}

	log.Println("✅ Bot commands set successfully")
	return nil
}
