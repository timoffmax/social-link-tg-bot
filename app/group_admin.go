package app

import (
	"log"
	"strings"

	"github.com/timoffmax/social-link-bot/model/api/request/message"
	"github.com/timoffmax/social-link-bot/model/api/request/payload"
	"github.com/timoffmax/social-link-bot/model/api/response"
	"github.com/timoffmax/social-link-bot/services"
)

// groupAdminOnlyText refuses a group setting change by a non-admin.
const groupAdminOnlyText = "Only group admins can change the group's settings."

// Group Settings callbacks, shared by the menus, the dispatcher and the
// admin check.
const (
	callbackSetLimitStart         = "/setlimit_start"
	callbackGroupSettings         = "/group_settings"
	callbackTimezoneMenu          = "/timezone_menu"
	callbackPlatformSettings      = "/platform_settings"
	callbackPlatformEnableAll     = "/platform_enable_all"
	callbackPlatformDisableAll    = "/platform_disable_all"
	callbackDescriptionSettings   = "/description_settings"
	callbackDescriptionEnableAll  = "/description_enable_all"
	callbackDescriptionDisableAll = "/description_disable_all"
)

// Prefixes of Group Settings callbacks that carry a value.
const (
	callbackSetGroupLimitPrefix     = "/setgrouplimit_"
	callbackTimezoneRegionPrefix    = "/tz_"
	callbackSetTimezonePrefix       = "/setgrouptimezone_"
	callbackTogglePlatformPrefix    = "/toggle_platform_"
	callbackToggleDescriptionPrefix = "/toggle_description_"
)

// groupAdminCallbacks are the Group Settings buttons only admins may use.
var groupAdminCallbacks = map[string]bool{
	callbackSetLimitStart:         true,
	callbackGroupSettings:         true,
	callbackTimezoneMenu:          true,
	callbackPlatformSettings:      true,
	callbackPlatformEnableAll:     true,
	callbackPlatformDisableAll:    true,
	callbackDescriptionSettings:   true,
	callbackDescriptionEnableAll:  true,
	callbackDescriptionDisableAll: true,
	reportButtonToggleCallback:    true,
	supportButtonToggleCallback:   true,
}

// groupAdminCallbackPrefixes start Group Settings buttons that carry a value.
var groupAdminCallbackPrefixes = []string{
	callbackSetGroupLimitPrefix,
	callbackTimezoneRegionPrefix,
	callbackSetTimezonePrefix,
	callbackTogglePlatformPrefix,
	callbackToggleDescriptionPrefix,
}

// refusedCommand replaces a refused command, so its case does nothing.
const refusedCommand = "(refused)"

// groupAdminCommands are the commands that change a group's settings.
var groupAdminCommands = map[string]bool{
	"/setlimit":         true,
	"/setgrouplimit":    true,
	"/setgrouptimezone": true,
}

// isGroupAdminCallback reports a Group Settings button.
func isGroupAdminCallback(data string) bool {
	result := groupAdminCallbacks[data]

	for _, prefix := range groupAdminCallbackPrefixes {
		result = (result == true) || (strings.HasPrefix(data, prefix) == true)
	}

	return result
}

// isGroupAdminCommand reports a command that changes a group's settings.
func isGroupAdminCommand(command string) bool {
	result := groupAdminCommands[command]

	return result
}

// isChatAdmin asks Telegram for the user's role; a variable for tests.
var isChatAdmin = services.IsChatAdmin

// isAnonymousGroupAdmin reports a message sent by an anonymous admin of the
// group: Telegram sends it on behalf of the group itself.
func isAnonymousGroupAdmin(origMessage *response.TgMessage, origChat *response.TgChat) bool {
	result := (origChat.Id != 0) && (origMessage.SenderChat.Id == origChat.Id)

	return result
}

// canManageGroup reports whether the user may change the chat's settings:
// anyone in a private chat, the bot owner, or the group's admins. A failed
// check refuses.
func canManageGroup(chat *response.TgChat, userID int) bool {
	isPrivate := (chat.Type == chatTypePrivate)
	result := (isPrivate == true) || (isOwner(userID) == true)

	if result == false {
		isAdmin, err := isChatAdmin(chat.Id, int64(userID))
		result = (isAdmin == true) && (err == nil)

		if err != nil {
			log.Printf("⚠️ Admin check of user %d in chat %d failed: %v", userID, chat.Id, err)
		}
	}

	return result
}

// isSelfAnsweredCallback reports a callback whose handler answers it with a
// toast, so the dispatcher doesn't answer it again.
func isSelfAnsweredCallback(data string) bool {
	result := (data == reportPostCallback) || (isPostButtonToggleCallback(data) == true)

	return result
}

// answerCallbackQueryText answers a button press with a short toast.
func answerCallbackQueryText(callbackQueryID string, text string) {
	answer := payload.TgPlAnswerCallbackQuery{
		CallbackQueryId: callbackQueryID,
		Text:            text,
	}

	message.NewAnswerCallbackQuery(answer)
}

// replyGroupAdminOnly refuses a group admin command by a non-admin.
func replyGroupAdminOnly(origMessage *response.TgMessage, origChat *response.TgChat) {
	refusal := payload.TgPlSendMessage{
		ChatId:              origChat.Id,
		Text:                groupAdminOnlyText,
		ReplyToMessageId:    origMessage.MessageId,
		DisableNotification: true,
	}

	message.NewSendMessage(refusal)
}
