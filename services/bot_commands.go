package services

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	neturl "net/url"
	"time"

	"github.com/timoffmax/social-link-bot/config"
)

// BotCommand represents a bot command for Telegram
type BotCommand struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

// starsSupportCommands are listed only while Telegram Stars support is on.
// /support stays an unlisted alias of /donate.
var starsSupportCommands = []BotCommand{
	{Command: "paysupport", Description: "Help with Stars payments"},
	{Command: "terms", Description: "Terms of Stars support"},
}

// /donate descriptions, by Stars support state.
const (
	donateStarsDescription  = "Support the bot (Telegram Stars, card or crypto)"
	donateCryptoDescription = "Support the bot (card or crypto)"
)

// botCommandList returns the menu commands; the Stars support ones only when
// isStarsSupportEnabled.
func botCommandList(isStarsSupportEnabled bool) []BotCommand {
	donateDescription := donateCryptoDescription

	if isStarsSupportEnabled == true {
		donateDescription = donateStarsDescription
	}

	result := []BotCommand{
		{Command: "menu", Description: "Show main menu"},
		{Command: "start", Description: "Show main menu and help"},
		{Command: "help", Description: "Show main menu and help"},
		{Command: "stats", Description: "View media statistics (today/week/month/year)"},
		{Command: "mylimit", Description: "Check your daily media limit"},
		{Command: "setlimit", Description: "Set user's daily limit (admin only)"},
		{Command: "setgrouplimit", Description: "Set group default limit (admin only)"},
		{Command: "setgrouptimezone", Description: "Set group timezone (admin only)"},
		{Command: "donate", Description: donateDescription},
		{Command: "report", Description: "Report a bug or issue"},
		{Command: "owner", Description: "Owner panel (owner only)"},
	}

	if isStarsSupportEnabled == true {
		result = append(result, starsSupportCommands...)
	}

	return result
}

// SetBotCommands sets the bot commands for the Telegram bot menu
func SetBotCommands() error {
	isStarsSupportEnabled := config.IsStarsSupportEnabled()
	commands := botCommandList(isStarsSupportEnabled)
	err := postBotAPI("setMyCommands", map[string]interface{}{"commands": commands})

	if err == nil {
		log.Println("✅ Bot commands set successfully")
	}

	return err
}

// BotDescription is shown in the empty chat with the bot (setMyDescription,
// up to 512 characters).
const BotDescription = `I download photos, videos and other media from social networks and send them to you or your group chat.

Supported platforms:
📸 Instagram — Posts, Reels, Stories, Restricted Posts
🎵 TikTok — Videos, Photo Slideshows
🐦 Twitter/X — Photos, Videos, GIFs, Text
🎬 YouTube — Shorts, Community Posts
🔴 Reddit — Videos, Images
👥 Facebook — Videos, Reels
😂 9GAG — Videos, Images
🧵 Threads — Text, Photos, Videos

No ads, no forced subscriptions. Just send a link!

💚 Support the bot: /donate`

// BotShortDescription is shown on the bot's profile and in shares
// (setMyShortDescription, up to 120 characters).
const BotShortDescription = "Downloads media from Instagram, TikTok, X, YouTube Shorts, Reddit, Facebook, 9GAG & Threads. No ads. Just send a link!"

// Bot API limits of the descriptions, in characters.
const (
	botDescriptionMaxLength      = 512
	botShortDescriptionMaxLength = 120
)

// SetBotDescriptions sets the bot's description and short description; a
// failure of one doesn't skip the other.
func SetBotDescriptions() error {
	descriptionErr := postBotAPI("setMyDescription", map[string]interface{}{"description": BotDescription})
	shortDescriptionErr := postBotAPI("setMyShortDescription", map[string]interface{}{"short_description": BotShortDescription})
	result := descriptionErr

	if result == nil {
		result = shortDescriptionErr
	}

	if (descriptionErr != nil) && (shortDescriptionErr != nil) {
		result = fmt.Errorf("%v; %v", descriptionErr, shortDescriptionErr)
	}

	if result == nil {
		log.Println("✅ Bot descriptions set successfully")
	}

	return result
}

// botAPITimeout bounds the startup Bot API calls, so a hanging connection
// doesn't block startup.
const botAPITimeout = 15 * time.Second

// botAPIClient sends the startup Bot API calls.
var botAPIClient = &http.Client{Timeout: botAPITimeout}

// botAPIURLTemplate is the Bot API URL (token, method); tests point it at an
// httptest server.
var botAPIURLTemplate = "https://api.telegram.org/bot%s/%s"

// botAPIAnswer is the part of a Bot API answer used to report failures.
type botAPIAnswer struct {
	Ok          bool   `json:"ok"`
	Description string `json:"description"`
}

// postBotAPI posts a JSON body to a Bot API method and fails on a non-200
// answer with Telegram's reason. Errors never contain the URL, which holds
// the bot token.
func postBotAPI(method string, body map[string]interface{}) error {
	var resp *http.Response
	token := config.GetTgBotToken()
	url := fmt.Sprintf(botAPIURLTemplate, token, method)
	jsonBody, err := json.Marshal(body)

	if err != nil {
		err = fmt.Errorf("failed to marshal %s: %w", method, err)
	}

	if err == nil {
		reader := bytes.NewReader(jsonBody)
		resp, err = botAPIClient.Post(url, "application/json", reader)
		err = withoutRequestURL(method, err)
	}

	if err == nil {
		defer resp.Body.Close()
	}

	if (err == nil) && (resp.StatusCode != http.StatusOK) {
		answer := botAPIAnswer{}
		_ = json.NewDecoder(resp.Body).Decode(&answer)
		err = fmt.Errorf("%s failed, status code: %d: %s", method, resp.StatusCode, answer.Description)
	}

	return err
}

// withoutRequestURL replaces a transport error, whose *url.Error text holds
// the request URL with the bot token, by its cause.
func withoutRequestURL(method string, err error) error {
	result := err
	var urlErr *neturl.Error

	if errors.As(err, &urlErr) == true {
		result = fmt.Errorf("%s request failed: %w", method, urlErr.Err)
	}

	return result
}
