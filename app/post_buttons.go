package app

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/timoffmax/social-link-bot/model/api"
	"github.com/timoffmax/social-link-bot/model/api/request/message"
	"github.com/timoffmax/social-link-bot/model/api/request/payload"
	"github.com/timoffmax/social-link-bot/model/api/response"
	"github.com/timoffmax/social-link-bot/services"
)

// groupSupportEvery is how many group posts share one support button.
const groupSupportEvery = 10

// postButtonsMarkup returns the buttons under a message that is a whole post
// (albums can't carry buttons). Private chats get support (opens /donate) and
// report (reports this post). Groups get report, and support under about one
// post in groupSupportEvery; each button can be turned off in Group
// Settings. It is nil when no button is shown, so the message has none.
func postButtonsMarkup(chat *response.TgChat, isWholePost bool) api.TgReplyMarkupInterface {
	var result api.TgReplyMarkupInterface
	var row []api.TgInlineKeyboardButton
	buttons := services.ChatButtonSettings{}

	if isWholePost == true {
		buttons = postButtonsFor(chat)
	}

	if buttons.IsSupportEnabled == true {
		supportButton := postSupportButton(chat)
		row = append(row, supportButton)
	}

	if buttons.IsReportEnabled == true {
		reportButton := api.TgInlineKeyboardButton{Text: reportPostButtonText, CallbackData: reportPostCallback}
		row = append(row, reportButton)
	}

	if len(row) > 0 {
		result = &api.TgInlineKeyboardMarkup{InlineKeyboard: [][]api.TgInlineKeyboardButton{row}}
	}

	return result
}

// postButtonsFor says which buttons this post gets: both in private chats;
// in a group the group's settings, with support only on its turn and only
// when the bot's username is known (the button links to the private chat).
func postButtonsFor(chat *response.TgChat) services.ChatButtonSettings {
	result := services.ChatButtonSettings{IsReportEnabled: true, IsSupportEnabled: true}
	isGroup := (chat.Type != chatTypePrivate)

	if isGroup == true {
		result = cachedGroupButtonSettings(chat.Id)
		isTurn := isGroupSupportTurn(chat.Id)
		result.IsSupportEnabled = (result.IsSupportEnabled == true) && (isTurn == true) && (botUsername != "")
	}

	return result
}

// donateStartPayload is the /start payload that opens /donate.
const donateStartPayload = "donate"

// donateStartURLTemplate opens the bot's private chat on /donate; %s is the
// bot's username.
const donateStartURLTemplate = "https://t.me/%s?start=%s"

// postSupportButton opens /donate: in a private chat in place, in a group as
// a link to the private chat, so the donate screen and Stars invoices never
// appear in the group.
func postSupportButton(chat *response.TgChat) api.TgInlineKeyboardButton {
	result := api.TgInlineKeyboardButton{Text: supportMenuButtonText, CallbackData: donateCommand}

	if chat.Type != chatTypePrivate {
		url := fmt.Sprintf(donateStartURLTemplate, botUsername, donateStartPayload)
		result = api.TgInlineKeyboardButton{Text: supportMenuButtonText, URL: url}
	}

	return result
}

// groupPostCounts counts each group's posts since its last support button
// (one small entry per group the bot posts in).
var (
	groupPostCountsMu sync.Mutex
	groupPostCounts   = map[int64]int{}
)

// isGroupSupportTurn counts a group post and reports whether it is the one
// that gets the support button. The count is in memory: a restart starts it
// again, and a post that then fails to send still counts, so the rate is
// approximate.
func isGroupSupportTurn(chatID int64) bool {
	groupPostCountsMu.Lock()
	defer groupPostCountsMu.Unlock()

	count := groupPostCounts[chatID] + 1
	result := (count >= groupSupportEvery)

	if result == true {
		count = 0
	}

	groupPostCounts[chatID] = count

	return result
}

// Group button settings; variables so tests run without a database.
var (
	getGroupButtonSettings = services.GetChatButtonSettings
	setGroupSupportEnabled = services.SetChatSupportButtonEnabled
	setGroupReportEnabled  = services.SetChatReportButtonEnabled
)

// groupButtonSettingsTTL is how long a group's button settings are cached:
// posts read them on every send, toggles clear the group's entry.
const groupButtonSettingsTTL = 5 * time.Minute

// groupButtonSettings caches the groups' button settings by chat id.
var groupButtonSettings = newTTLStore(groupButtonSettingsTTL)

// cachedGroupButtonSettings returns the group's button settings, reading the
// database once per groupButtonSettingsTTL. A failed read shows both buttons
// and isn't cached.
func cachedGroupButtonSettings(chatID int64) services.ChatButtonSettings {
	key := groupButtonsKey(chatID)
	value, isCached := groupButtonSettings.get(key)
	result, isSettings := value.(services.ChatButtonSettings)

	if (isCached == false) || (isSettings == false) {
		result = readGroupButtonSettings(chatID, key)
	}

	return result
}

// groupButtonsKey names a group in the button caches and locks.
func groupButtonsKey(chatID int64) string {
	result := fmt.Sprintf("%d", chatID)

	return result
}

// groupButtonsVersions counts each group's toggles, so a read that started
// before a toggle doesn't cache the old settings after it.
var (
	groupButtonsVersionsMu sync.Mutex
	groupButtonsVersions   = map[string]int{}
)

// groupButtonsVersion returns the group's toggle count.
func groupButtonsVersion(key string) int {
	groupButtonsVersionsMu.Lock()
	defer groupButtonsVersionsMu.Unlock()

	result := groupButtonsVersions[key]

	return result
}

// invalidateGroupButtons counts a toggle and clears the group's cache entry.
func invalidateGroupButtons(key string) {
	groupButtonsVersionsMu.Lock()
	defer groupButtonsVersionsMu.Unlock()

	groupButtonsVersions[key]++
	groupButtonSettings.remove(key)
}

// readGroupButtonSettings reads the group's button settings and caches them
// unless a toggle happened during the read.
func readGroupButtonSettings(chatID int64, key string) services.ChatButtonSettings {
	versionBefore := groupButtonsVersion(key)
	result, err := getGroupButtonSettings(chatID)

	if err == nil {
		cacheGroupButtons(key, result, versionBefore)
	}

	return result
}

// cacheGroupButtons caches the settings if no toggle happened since the
// read started.
func cacheGroupButtons(key string, settings services.ChatButtonSettings, versionBefore int) {
	groupButtonsVersionsMu.Lock()
	defer groupButtonsVersionsMu.Unlock()

	isCurrent := (groupButtonsVersions[key] == versionBefore)

	if isCurrent == true {
		groupButtonSettings.set(key, settings)
	}
}

// groupButtonToggleLocks hold one lock per group, so two admins tapping at
// once flip the setting twice instead of writing the same value, without
// other groups waiting.
var groupButtonToggleLocks sync.Map

// lockGroupButtonToggle locks the group's toggles and returns the unlock.
func lockGroupButtonToggle(key string) func() {
	value, _ := groupButtonToggleLocks.LoadOrStore(key, &sync.Mutex{})
	lock := value.(*sync.Mutex)
	lock.Lock()

	return lock.Unlock
}

// groupSettingsTitle starts the Group Settings message, which a toggle
// refreshes in place; other messages with the same keyboard are left as is.
const groupSettingsTitle = "🏢 Group Settings"

// Group Settings toggles of the buttons under posts.
const (
	reportButtonToggleText      = "🐞 Report Button: On/Off"
	reportButtonToggleCallback  = "/toggle_report_button"
	supportButtonToggleText     = "💚 Support Button: On/Off"
	supportButtonToggleCallback = "/toggle_support_button"
)

// Group Settings lines and toggle confirmations of the buttons under posts.
const (
	reportButtonOnLine          = "\n🐞 <b>Report button:</b> on (under every post)\n"
	reportButtonOffLine         = "\n🐞 <b>Report button:</b> off\n"
	supportButtonOnLine         = "💚 <b>Support button:</b> on (under about 1 in %d posts)\n"
	supportButtonOffLine        = "💚 <b>Support button:</b> off\n"
	reportButtonNowOnText       = "🐞 Report button on: it appears under every post."
	reportButtonNowOffText      = "🐞 Report button off. Failed downloads still offer it, and /report always works."
	supportButtonNowOnText      = "💚 Support button on: it appears under about 1 in %d posts."
	supportButtonNowOffText     = "💚 Support button off in this chat."
	postButtonFailedText        = "❌ Couldn't change the button setting. Please try again later."
	postButtonsUnavailableLines = "\n🐞💚 <b>Buttons under posts:</b> <i>unavailable right now</i>\n"
)

// groupPostButtonsStatusLines are the Group Settings lines of the buttons.
func groupPostButtonsStatusLines(buttons services.ChatButtonSettings) string {
	reportLine := reportButtonOffLine
	supportLine := supportButtonOffLine

	if buttons.IsReportEnabled == true {
		reportLine = reportButtonOnLine
	}

	if buttons.IsSupportEnabled == true {
		supportLine = fmt.Sprintf(supportButtonOnLine, groupSupportEvery)
	}

	result := fmt.Sprintf("%s%s", reportLine, supportLine)

	return result
}

// isPostButtonToggleCallback reports a Group Settings button toggle.
func isPostButtonToggleCallback(data string) bool {
	result := (data == reportButtonToggleCallback) || (data == supportButtonToggleCallback)

	return result
}

// handlePostButtonToggleCallback turns the group's report or support button
// on or off, refreshes the Group Settings message in place and confirms the
// change in the button's toast. The admin check runs before dispatch.
func handlePostButtonToggleCallback(callbackQuery *response.TgCallbackQuery) {
	chat := callbackQuery.Message.Chat
	toast, err := toggleGroupButton(chat.Id, callbackQuery.Data)
	isSettingsMessage := strings.HasPrefix(callbackQuery.Message.Text, groupSettingsTitle)

	if err != nil {
		log.Printf("❌ Button toggle %s failed in chat %d: %v", callbackQuery.Data, chat.Id, err)
	}

	if err == nil {
		log.Printf("⚙️ User %d used %s in chat %d", callbackQuery.From.Id, callbackQuery.Data, chat.Id)
	}

	if (err == nil) && (isSettingsMessage == true) {
		isRefreshed := refreshGroupSettings(&chat, callbackQuery.Message.MessageId)
		toast = postButtonToastWithRefresh(toast, isRefreshed)
	}

	answerCallbackQueryText(callbackQuery.Id, toast)
}

// toggleGroupButton flips one of the group's buttons from its stored state
// and returns the toast; a failed read changes nothing.
func toggleGroupButton(chatID int64, data string) (string, error) {
	key := groupButtonsKey(chatID)
	unlock := lockGroupButtonToggle(key)
	defer unlock()

	buttons, err := getGroupButtonSettings(chatID)
	isReport := (data == reportButtonToggleCallback)
	newState := (buttons.IsSupportEnabled == false)
	onText := fmt.Sprintf(supportButtonNowOnText, groupSupportEvery)
	offText := supportButtonNowOffText
	setEnabled := setGroupSupportEnabled

	if isReport == true {
		newState = (buttons.IsReportEnabled == false)
		onText = reportButtonNowOnText
		offText = reportButtonNowOffText
		setEnabled = setGroupReportEnabled
	}

	if err == nil {
		err = setEnabled(chatID, newState)
		invalidateGroupButtons(key)
	}

	result := postButtonToggleText(newState, onText, offText)

	if err != nil {
		result = postButtonFailedText
	}

	return result, err
}

// postButtonToggleText picks the confirmation of the new state.
func postButtonToggleText(newState bool, onText, offText string) string {
	result := offText

	if newState == true {
		result = onText
	}

	return result
}

// refreshGroupSettings re-renders the Group Settings message in place and
// reports whether Telegram accepted the edit.
func refreshGroupSettings(chat *response.TgChat, messageID int) bool {
	text, err := buildGroupSettingsText(chat)
	result := false

	if err == nil {
		result = editGroupSettingsMessage(chat.Id, messageID, text)
	}

	return result
}

// editGroupSettingsMessage replaces the settings message and reports whether
// Telegram returned the edited message.
func editGroupSettingsMessage(chatID int64, messageID int, text string) bool {
	edit := payload.TgPlEditMessageText{
		ChatId:      chatID,
		MessageId:   messageID,
		Text:        text,
		ParseMode:   payload.ParseModeHTML,
		ReplyMarkup: createGroupSettingsKeyboard(),
	}
	edited := message.NewEditMessageText(edit)
	result := (edited.ResponsePayload != nil) && (edited.ResponsePayload.MessageId != 0)

	return result
}

// postButtonStaleSettingsText tells the admin the settings message above is
// out of date.
const postButtonStaleSettingsText = " Reopen Group Settings to see the change."

// postButtonToastWithRefresh adds a hint when the settings message couldn't
// be refreshed.
func postButtonToastWithRefresh(toast string, isRefreshed bool) string {
	result := toast

	if isRefreshed == false {
		result = fmt.Sprintf("%s%s", toast, postButtonStaleSettingsText)
	}

	return result
}
