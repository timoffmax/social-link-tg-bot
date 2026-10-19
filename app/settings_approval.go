package app

import (
	"errors"
	"log"
	"time"

	"github.com/timoffmax/social-link-bot/config"
	tg_api "github.com/timoffmax/social-link-bot/model/api"
	"github.com/timoffmax/social-link-bot/model/api/request/message"
	"github.com/timoffmax/social-link-bot/model/api/request/payload"
	"github.com/timoffmax/social-link-bot/model/api/response"
	"github.com/timoffmax/social-link-bot/services/botsettings"
)

// Answers to an approval button that is not the owner's to decide.
const (
	approvalRefusedText     = "Only the bot owner can decide this, in the private chat with the bot."
	approvalMalformedText   = "Unknown request."
	approvalUnavailableText = "Settings are not available."
)

// approvalCallTimeout caps each Telegram call of the approver, since it runs
// on the settings goroutine.
const approvalCallTimeout = 15 * time.Second

// errApprovalNotSent: Telegram answered without a message to edit later.
var errApprovalNotSent = errors.New("telegram returned no message id")

// telegramApprover asks the owner in the private chat with the bot, in
// plain text (no parse mode).
type telegramApprover struct {
	chatID int64
}

// Ask sends the approval message with its inline buttons.
func (a telegramApprover) Ask(text string, buttons []botsettings.ApprovalButton) (int64, error) {
	keyboard := approvalKeyboard(buttons)
	request := payload.TgPlSendMessage{ChatId: a.chatID, Text: text, DisableWebPagePreview: true, ReplyMarkup: keyboard}
	sent := message.NewSendMessageWithin(request, approvalCallTimeout)
	result := int64(sent.ResponsePayload.MessageId)
	err := sent.Err

	if (err == nil) && (result == 0) {
		err = errApprovalNotSent
	}

	return result, err
}

// Edit replaces the message's text and removes its buttons.
func (a telegramApprover) Edit(messageID int64, text string) error {
	noButtons := &tg_api.TgInlineKeyboardMarkup{InlineKeyboard: [][]tg_api.TgInlineKeyboardButton{}}
	request := payload.TgPlEditMessageText{ChatId: a.chatID, MessageId: int(messageID), Text: text, ReplyMarkup: noButtons}
	edited := message.NewEditMessageTextWithin(request, approvalCallTimeout)

	return edited.Err
}

// approvalKeyboard puts the buttons in one row.
func approvalKeyboard(buttons []botsettings.ApprovalButton) *tg_api.TgInlineKeyboardMarkup {
	row := make([]tg_api.TgInlineKeyboardButton, 0, len(buttons))

	for _, button := range buttons {
		row = append(row, tg_api.TgInlineKeyboardButton{Text: button.Text, CallbackData: button.Data})
	}

	result := &tg_api.TgInlineKeyboardMarkup{InlineKeyboard: [][]tg_api.TgInlineKeyboardButton{row}}

	return result
}

// newSettingsApprover returns the owner's approver; nil without
// ADMIN_USER_ID, so changes that need approval are rejected.
func newSettingsApprover() botsettings.Approver {
	var result botsettings.Approver
	adminID := config.GetAdminUserID()

	if adminID != 0 {
		result = telegramApprover{chatID: adminID}
	}

	return result
}

// decideSettingsApproval hands a tap to the settings goroutine; a variable
// for tests.
var decideSettingsApproval = func(requestID int64, messageID int64, isApprove bool) string {
	result := approvalUnavailableText

	if settingsManager != nil {
		result = settingsManager.DecideApproval(requestID, messageID, isApprove)
	}

	return result
}

// answerApprovalCallback answers a tap with a toast; a variable for tests.
var answerApprovalCallback = answerCallbackQueryText

// handleSettingsApprovalCallback handles Approve / Reject: only the owner,
// only in the private chat with the bot; anything else changes nothing.
func handleSettingsApprovalCallback(callbackQuery *response.TgCallbackQuery) {
	requestID, isApprove, isValid := botsettings.ParseApprovalCallback(callbackQuery.Data)
	isOwner := isOwnerPrivateCallback(callbackQuery)
	answer := approvalRefusedText

	if (isOwner == true) && (isValid == false) {
		answer = approvalMalformedText
	}

	if (isOwner == true) && (isValid == true) {
		messageID := int64(callbackQuery.Message.MessageId)
		answer = decideSettingsApproval(requestID, messageID, isApprove)
	}

	if isOwner == false {
		log.Printf("🚫 User %d may not decide settings requests (chat %d)", callbackQuery.From.Id, callbackQuery.Message.Chat.Id)
	}

	answerApprovalCallback(callbackQuery.Id, answer)
}

// isOwnerPrivateCallback reports a tap by ADMIN_USER_ID in the private chat
// with the bot.
func isOwnerPrivateCallback(callbackQuery *response.TgCallbackQuery) bool {
	adminID := config.GetAdminUserID()
	chat := callbackQuery.Message.Chat
	isFromOwner := (adminID != 0) && (int64(callbackQuery.From.Id) == adminID)
	result := (isFromOwner == true) && (chat.Id == adminID) && (chat.Type == chatTypePrivate)

	return result
}

// isSettingsApprovalCallback reports the data of an Approve / Reject button.
func isSettingsApprovalCallback(data string) bool {
	result := botsettings.IsApprovalCallback(data)

	return result
}
