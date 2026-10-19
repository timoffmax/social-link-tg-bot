package services

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/timoffmax/social-link-bot/config"
	"github.com/timoffmax/social-link-bot/model/api/request"
	"github.com/timoffmax/social-link-bot/model/settings"
)

// BotCommand represents a bot command for Telegram
type BotCommand struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

// starsSupportCommands are listed only while Telegram Stars support is on.
// /support stays an unlisted alias of /donate.
var starsSupportCommands = []BotCommand{
	{Command: "boost", Description: "Raise a group's limit of restricted posts (Stars)"},
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
		{Command: "info", Description: "How the bot works: chats, groups and limits"},
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

// botDescriptionTemplate is shown in the empty chat with the bot
// (setMyDescription, up to 512 characters): the platform line (the platforms
// that are on), then the restricted-post line with the monthly limits per
// person and per group.
const botDescriptionTemplate = `Send a link, get the actual photos and videos right in Telegram.

%s

💬 Private chat: send me any link.
👥 Groups: add me, and I answer every link anyone posts.

🔓 Public posts: free, no monthly limit.
%s
No ads, no forced subscriptions. Tap Start for details.`

// botDescriptionRestrictedTemplate is the restricted-post line of the
// description: the default monthly limits per person and per group.
const botDescriptionRestrictedTemplate = "🔞 Restricted Instagram posts (age- or region-limited): by default %d a month per person, %d per group.\n"

// botDescriptionPlatforms name the platforms in the description's platform
// line, by platform id.
var botDescriptionPlatforms = map[string]string{
	settings.PlatformInstagram: "📸 Instagram",
	settings.PlatformTikTok:    "🎵 TikTok",
	settings.PlatformTwitter:   "🐦 X",
	settings.PlatformYouTube:   "🎬 YouTube Shorts",
	settings.PlatformReddit:    "🔴 Reddit",
	settings.PlatformFacebook:  "👥 Facebook",
	settings.Platform9GAG:      "😂 9GAG",
	settings.PlatformThreads:   "🧵 Threads",
}

// Platform line parts of the description.
const (
	botDescriptionPlatformSeparator = " · "
	botDescriptionNoPlatformsText   = "⏸ Link downloads are paused right now."
)

// BotDescription returns the bot description with the platforms that are on
// and the configured default limits; the restricted-post line only while
// they can be fetched.
func BotDescription() string {
	userAllowance := config.GetGatedFetchUserMonthlyAllowance()
	chatCap := config.GetGatedFetchChatMonthlyCap()
	isRestrictedOn := IsRestrictedPostsOffered()
	platformLine := botDescriptionPlatformLine()
	restrictedLine := ""

	if isRestrictedOn == true {
		restrictedLine = fmt.Sprintf(botDescriptionRestrictedTemplate, userAllowance, chatCap)
	}

	result := fmt.Sprintf(botDescriptionTemplate, platformLine, restrictedLine)

	return result
}

// botDescriptionPlatformLine lists the platforms that are on, in registry
// order.
func botDescriptionPlatformLine() string {
	enabled := EnabledPlatforms()
	names := make([]string, 0, len(enabled))
	result := botDescriptionNoPlatformsText

	for _, platformID := range enabled {
		names = append(names, botDescriptionPlatforms[platformID])
	}

	if len(names) > 0 {
		result = strings.Join(names, botDescriptionPlatformSeparator)
	}

	return result
}

// botShortDescriptionTemplate is shown on the bot's profile and in shares
// (setMyShortDescription, up to 120 characters); %s names the platforms.
const botShortDescriptionTemplate = "Send a link, get the photo or video%s. Private chats & groups."

// Platform list parts of the short description.
const (
	botShortPlatformsPrefix    = ": "
	botShortPlatformsSeparator = ", "
	botShortPlatformsLast      = " & "
	botShortPlatformsMore      = " & more"
	botShortMaxPlatforms       = 5
)

// botShortPlatformOrder is the order the short description names platforms
// in (the best known first), with their names.
var botShortPlatformOrder = []shortPlatform{
	{id: settings.PlatformInstagram, name: "Instagram"},
	{id: settings.PlatformTikTok, name: "TikTok"},
	{id: settings.PlatformTwitter, name: "X"},
	{id: settings.PlatformYouTube, name: "YouTube Shorts"},
	{id: settings.PlatformThreads, name: "Threads"},
	{id: settings.PlatformReddit, name: "Reddit"},
	{id: settings.PlatformFacebook, name: "Facebook"},
	{id: settings.Platform9GAG, name: "9GAG"},
}

// shortPlatform is a platform id with the name the short description shows.
type shortPlatform struct {
	id   string
	name string
}

// BotShortDescription returns the short description, naming up to five of
// the platforms that are on ("& more" when some are left out) within
// Telegram's 120 characters.
func BotShortDescription() string {
	names := enabledShortPlatformNames()
	shown := min(len(names), botShortMaxPlatforms)
	result := botShortDescriptionText(names, shown)

	for (shown > 1) && (utf16Length(result) > botShortDescriptionMaxLength) {
		shown--
		result = botShortDescriptionText(names, shown)
	}

	return result
}

// enabledShortPlatformNames names the platforms that are on, in the short
// description's order.
func enabledShortPlatformNames() []string {
	result := []string{}

	for _, platform := range botShortPlatformOrder {
		if config.IsPlatformEnabled(platform.id) == true {
			result = append(result, platform.name)
		}
	}

	return result
}

// botShortDescriptionText renders the short description with the first
// shown of names.
func botShortDescriptionText(names []string, shown int) string {
	list := ""
	isCut := (shown < len(names))
	headCount := max(shown-1, 0)
	head := strings.Join(names[:headCount], botShortPlatformsSeparator)

	switch {
	case (shown > 0) && (isCut == true):
		joined := strings.Join(names[:shown], botShortPlatformsSeparator)
		list = botShortPlatformsPrefix + joined + botShortPlatformsMore
	case shown == 1:
		list = botShortPlatformsPrefix + names[0]
	case shown > 1:
		list = botShortPlatformsPrefix + head + botShortPlatformsLast + names[shown-1]
	}

	result := fmt.Sprintf(botShortDescriptionTemplate, list)

	return result
}

// utf16Length counts UTF-16 code units, the stricter way Telegram may count.
func utf16Length(text string) int {
	runes := []rune(text)
	encoded := utf16.Encode(runes)
	result := len(encoded)

	return result
}

// EnabledPlatforms returns the ids of the platforms that are on, in the
// registry's order.
func EnabledPlatforms() []string {
	result := []string{}

	for _, platform := range settings.Platforms() {
		if config.IsPlatformEnabled(platform.ID) == true {
			result = append(result, platform.ID)
		}
	}

	return result
}

// Bot API limits of the descriptions, in characters.
const (
	botDescriptionMaxLength      = 512
	botShortDescriptionMaxLength = 120
)

// BotProfile is what the bot's Telegram profile shows: the command menu and
// both descriptions, rendered from the current settings.
type BotProfile struct {
	Commands         []BotCommand `json:"commands"`
	Description      string       `json:"description"`
	ShortDescription string       `json:"short_description"`
}

// RenderBotProfile renders the profile from the current settings.
func RenderBotProfile() BotProfile {
	isStarsSupportEnabled := config.IsStarsSupportEnabled()
	commands := botCommandList(isStarsSupportEnabled)
	description := BotDescription()
	shortDescription := BotShortDescription()
	result := BotProfile{Commands: commands, Description: description, ShortDescription: shortDescription}

	return result
}

// Bot API methods of the profile parts, in the order they are pushed.
const (
	ProfileMethodCommands         = "setMyCommands"
	ProfileMethodDescription      = "setMyDescription"
	ProfileMethodShortDescription = "setMyShortDescription"
)

// profilePart is one Bot API call of the profile.
type profilePart struct {
	method string
	body   map[string]interface{}
}

// parts returns the profile's Bot API calls, in push order.
func (p BotProfile) parts() []profilePart {
	result := []profilePart{
		{method: ProfileMethodCommands, body: map[string]interface{}{"commands": p.Commands}},
		{method: ProfileMethodDescription, body: map[string]interface{}{"description": p.Description}},
		{method: ProfileMethodShortDescription, body: map[string]interface{}{"short_description": p.ShortDescription}},
	}

	return result
}

// Fingerprints returns each part's request body as JSON, by method: what
// PushBotProfile compares to skip parts Telegram already shows.
func (p BotProfile) Fingerprints() map[string]string {
	result := map[string]string{}

	for _, part := range p.parts() {
		encoded, err := json.Marshal(part.body)

		if err == nil {
			result[part.method] = string(encoded)
		}
	}

	return result
}

// profileSuccessLogs are logged when a part was pushed.
var profileSuccessLogs = map[string]string{
	ProfileMethodCommands:         "✅ Bot commands set successfully",
	ProfileMethodDescription:      "✅ Bot description set successfully",
	ProfileMethodShortDescription: "✅ Bot short description set successfully",
}

// PushBotProfile pushes the parts of next whose fingerprint differs from
// pushed (method -> fingerprint of what Telegram is known to show; empty
// pushes every part). A failure of one part doesn't skip the others. It
// returns the fingerprints now known to be on Telegram and the joined
// errors, and which methods it called.
func PushBotProfile(pushed map[string]string, next BotProfile) (map[string]string, []string, error) {
	known := map[string]string{}
	called := []string{}
	fingerprints := next.Fingerprints()
	var errs []error

	for method, fingerprint := range pushed {
		known[method] = fingerprint
	}

	for _, part := range next.parts() {
		fingerprint := fingerprints[part.method]
		isCurrent := (pushed[part.method] == fingerprint) && (fingerprint != "")

		if isCurrent == false {
			called = append(called, part.method)
			err := postBotAPI(part.method, part.body)
			known, errs = recordProfilePart(known, errs, part.method, fingerprint, err)
		}
	}

	result := errors.Join(errs...)

	return known, called, result
}

// recordProfilePart stores the outcome of one part's push: its fingerprint
// on success, none (unknown) after a failure.
func recordProfilePart(known map[string]string, errs []error, method string, fingerprint string, err error) (map[string]string, []error) {
	if err == nil {
		known[method] = fingerprint
		log.Println(profileSuccessLogs[method])
	}

	if err != nil {
		delete(known, method)
		errs = append(errs, err)
	}

	return known, errs
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
		err = request.WithoutRequestURL(method, err)
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
