package app

import (
	"fmt"
	"html"
	"log"
	"strings"
	"sync"

	"github.com/timoffmax/social-link-bot/config"
	"github.com/timoffmax/social-link-bot/model/api"
	"github.com/timoffmax/social-link-bot/model/api/request/message"
	"github.com/timoffmax/social-link-bot/model/api/request/payload"
	"github.com/timoffmax/social-link-bot/model/api/response"
	"github.com/timoffmax/social-link-bot/services"
)

// reportCommand files a bug report; it is also the menu button's callback.
const reportCommand = "/report"

// reportFailedCallback is the button under a failed download's error message.
const reportFailedCallback = "/report_failed"

// Report buttons.
const (
	reportMenuButtonText   = "🐛 Report a bug"
	reportFailedButtonText = "🐛 Report problem"
)

// reportPromptTitle starts the prompt; a reply to a bot message starting with
// it is a report. Telegram returns the message text without HTML tags.
const reportPromptTitle = "🐛 What went wrong?"

// reportPromptTemplate asks for the report: the title, then the user mention
// (empty when the prompt replies to the user).
const reportPromptTemplate = "<b>%s</b>\n\n%sReply to this message and describe the problem. Paste the link if there is one."

// reportPromptPlaceholder is shown in the input field of the reply.
const reportPromptPlaceholder = "Describe the problem"

// reportThanksText confirms a report to the user.
const reportThanksText = "✅ Your report has been sent to the developer. Thank you!"

// reportFailedDoneSuffix replaces the button of a reported error message.
const reportFailedDoneSuffix = "\n\n✅ Reported to the developer. Thank you!"

// reportFailedTitle opens a report filed with the error message button.
const reportFailedTitle = "Download failed (reported with the button)"

// reportNoDescriptionText stands in for a report without a description.
const reportNoDescriptionText = "(no description)"

// reportExcerptLimit bounds quoted messages in a report, in runes.
const reportExcerptLimit = 500

// reportPrivateChatTitle is stored as the chat title of private reports.
const reportPrivateChatTitle = "Private Chat"

// handleReportCommand files "/report text". A bare /report opens the report
// prompt; /report in reply to a bot message reports that message, with its
// text and links attached.
func handleReportCommand(origMessage *response.TgMessage, origChat *response.TgChat, origUser *response.TgUser) {
	description := reportCommandText(origMessage.Text)
	botMessage := repliedBotMessage(origMessage)
	isPrompt := ((description == "") && (botMessage == nil))

	if isPrompt == true {
		sendReportPrompt(origChat, origUser, origMessage.MessageId)
	}

	if isPrompt == false {
		reportContext := botMessageContext(botMessage)
		reportText := joinReportParts(description, reportContext)
		submitReport(origChat, origUser, reportText)
		sendReportThanks(origChat.Id, origMessage.MessageId)
	}
}

// reportCommandText returns the text after the command word.
func reportCommandText(text string) string {
	result := ""
	index := strings.Index(text, " ")

	if index >= 0 {
		result = strings.TrimSpace(text[index+1:])
	}

	return result
}

// repliedBotMessage returns the message of this bot the user replied to.
func repliedBotMessage(origMessage *response.TgMessage) *response.TgMessage {
	var result *response.TgMessage
	replied := origMessage.ReplyToMessage

	if (replied != nil) && (isOwnBotMessage(replied) == true) {
		result = replied
	}

	return result
}

// isOwnBotMessage reports a message sent by this bot. When the bot's
// username is unknown (getMe failed at startup), any bot's message counts:
// in a group, users can only reply to bots that are members.
func isOwnBotMessage(msg *response.TgMessage) bool {
	username := strings.ToLower(msg.From.Username)
	isThisBot := (botUsername == "") || (username == botUsername)
	result := (msg.From.IsBot == true) && (isThisBot == true)

	return result
}

// sendReportPrompt asks the user to describe the problem in a reply. It
// replies to replyToMessageID when it is the user's own message; otherwise
// (a menu button in a group) it @mentions the user, so the selective forced
// reply targets them (a user without a username gets a link mention).
func sendReportPrompt(chat *response.TgChat, user *response.TgUser, replyToMessageID int) {
	mention := ""
	isGroupButton := (replyToMessageID == 0) && (chat.Type != chatTypePrivate)

	if (isGroupButton == true) && (user.Username != "") {
		mention = fmt.Sprintf("@%s: ", user.Username)
	}

	if (isGroupButton == true) && (user.Username == "") {
		name := html.EscapeString(user.FirstName)
		mention = fmt.Sprintf("<a href=\"tg://user?id=%d\">%s</a>: ", user.Id, name)
	}

	prompt := payload.TgPlSendMessage{
		ChatId:              chat.Id,
		ParseMode:           payload.ParseModeHTML,
		Text:                fmt.Sprintf(reportPromptTemplate, reportPromptTitle, mention),
		ReplyToMessageId:    replyToMessageID,
		DisableNotification: true,
		ReplyMarkup:         &api.TgForceReply{ForceReply: true, InputFieldPlaceholder: reportPromptPlaceholder, Selective: true},
	}

	message.NewSendMessage(prompt)
}

// handleReportPromptReply files a reply to the report prompt as a report.
// It returns false for any other message.
func handleReportPromptReply(origMessage *response.TgMessage, origChat *response.TgChat, origUser *response.TgUser) bool {
	botMessage := repliedBotMessage(origMessage)
	result := (botMessage != nil) && (strings.HasPrefix(botMessage.Text, reportPromptTitle) == true)

	if result == true {
		description := messageText(origMessage)
		reportText := joinReportParts(description, "")
		submitReport(origChat, origUser, reportText)
		sendReportThanks(origChat.Id, origMessage.MessageId)
	}

	return result
}

// messageText returns a message's text, or its caption.
func messageText(msg *response.TgMessage) string {
	result := strings.TrimSpace(msg.Text)

	if result == "" {
		result = strings.TrimSpace(msg.Caption)
	}

	return result
}

// reportFailedMarkup is the report button under a failed download's error.
func reportFailedMarkup() *api.TgInlineKeyboardMarkup {
	button := api.TgInlineKeyboardButton{Text: reportFailedButtonText, CallbackData: reportFailedCallback}
	result := &api.TgInlineKeyboardMarkup{InlineKeyboard: [][]api.TgInlineKeyboardButton{{button}}}

	return result
}

// handleReportFailedCallback reports a failed download with one tap: the
// error message replies to the user's message with the link, so both go into
// the report. The button is then replaced by a thank-you, so it is pressed
// once.
// A message Telegram no longer gives the bot (older than 48 hours, or
// deleted) arrives without a date and text, so there is nothing to report.
func handleReportFailedCallback(callbackQuery *response.TgCallbackQuery) {
	errorMessage := callbackQuery.Message
	isAccessible := (errorMessage.Date != 0)
	isClaimed := false

	if isAccessible == true {
		isClaimed = claimFailedReport(errorMessage.Chat.Id, errorMessage.MessageId)
	}

	if isClaimed == true {
		reportText := failedDownloadReportText(&errorMessage)
		escapedError := html.EscapeString(errorMessage.Text)
		doneText := fmt.Sprintf("%s%s", escapedError, reportFailedDoneSuffix)
		submitReport(&errorMessage.Chat, &callbackQuery.From, reportText)
		editMessage(errorMessage.Chat.Id, errorMessage.MessageId, doneText, nil)
	}

	if isAccessible == false {
		log.Printf("🐛 Report button on an inaccessible message by user %d, ignored", callbackQuery.From.Id)
	}
}

// reportedErrorMessages holds the error messages already reported, as
// "chat:message" keys, so a double tap or two members tapping at once file
// one report.
var reportedErrorMessages sync.Map

// claimFailedReport reports whether this call is the first to report the
// error message.
func claimFailedReport(chatID int64, messageID int) bool {
	key := fmt.Sprintf("%d:%d", chatID, messageID)
	_, isLoaded := reportedErrorMessages.LoadOrStore(key, true)
	result := (isLoaded == false)

	return result
}

// failedDownloadReportText describes a failed download from its error
// message and the user's message it replies to.
func failedDownloadReportText(errorMessage *response.TgMessage) string {
	var text strings.Builder
	text.WriteString(reportFailedTitle)
	original := errorMessage.ReplyToMessage

	if original != nil {
		originalText := messageText(original)
		excerpt := reportExcerpt(originalText)
		userLine := fmt.Sprintf("\n\nUser's message: %s", excerpt)
		text.WriteString(userLine)
	}

	errorExcerpt := reportExcerpt(errorMessage.Text)
	errorLine := fmt.Sprintf("\n\nBot replied: %s", errorExcerpt)
	text.WriteString(errorLine)
	result := text.String()

	return result
}

// botMessageContext describes the bot message a report is about: its text or
// caption and its links (post links are usually text links).
func botMessageContext(botMessage *response.TgMessage) string {
	result := ""
	var links []string

	if botMessage != nil {
		botText := messageText(botMessage)
		excerpt := reportExcerpt(botText)
		links = messageLinks(botMessage)
		result = fmt.Sprintf("About the bot's message #%d: %s", botMessage.MessageId, excerpt)
	}

	if len(links) > 0 {
		joinedLinks := strings.Join(links, " ")
		result = fmt.Sprintf("%s\nLinks: %s", result, joinedLinks)
	}

	return result
}

// messageLinks returns the text-link URLs of a message's text and caption.
func messageLinks(msg *response.TgMessage) []string {
	var result []string
	entities := append([]response.TgMessageEntity{}, msg.Entities...)
	entities = append(entities, msg.CaptionEntities...)

	for _, entity := range entities {
		if entity.URL != "" {
			result = append(result, entity.URL)
		}
	}

	return result
}

// reportExcerpt shortens text to reportExcerptLimit runes.
func reportExcerpt(text string) string {
	runes := []rune(text)
	result := text

	if len(runes) > reportExcerptLimit {
		result = fmt.Sprintf("%s…", string(runes[:reportExcerptLimit]))
	}

	return result
}

// joinReportParts joins the user's description and the context of the
// message the report is about.
func joinReportParts(description, reportContext string) string {
	result := description

	if result == "" {
		result = reportNoDescriptionText
	}

	if reportContext != "" {
		result = fmt.Sprintf("%s\n\n%s", result, reportContext)
	}

	return result
}

// submitReport saves the report and sends it to the owner with a /reply hint.
func submitReport(chat *response.TgChat, user *response.TgUser, reportText string) {
	adminUserID := config.GetAdminUserID()
	chatTitle := chat.Title
	isPrivate := (chat.Type == chatTypePrivate)

	if isPrivate == true {
		chatTitle = reportPrivateChatTitle
	}

	userID := int64(user.Id)
	reportID, dbErr := services.CreateReport(userID, user.Username, user.FirstName, chat.Id, chatTitle, reportText)

	if dbErr != nil {
		log.Printf("❌ Failed to save report to DB: %v", dbErr)
	}

	ownerText := ownerReportText(chat, user, reportText, reportID, dbErr)

	if adminUserID != 0 {
		ownerMessage := payload.TgPlSendMessage{
			ChatId:                adminUserID,
			ParseMode:             payload.ParseModeHTML,
			Text:                  ownerText,
			DisableWebPagePreview: true,
		}
		message.NewSendMessage(ownerMessage)
	}

	log.Printf("🐛 Report #%d from user %d: %s", reportID, user.Id, reportText)
}

// ownerReportText formats a report for the owner.
func ownerReportText(chat *response.TgChat, user *response.TgUser, reportText string, reportID int, dbErr error) string {
	var report strings.Builder
	firstName := html.EscapeString(user.FirstName)
	header := fmt.Sprintf("🐛 <b>New Report</b>\n\n👤 From: %s", firstName)
	report.WriteString(header)

	if user.LastName != "" {
		lastName := html.EscapeString(user.LastName)
		lastNamePart := fmt.Sprintf(" %s", lastName)
		report.WriteString(lastNamePart)
	}

	if user.Username != "" {
		usernamePart := fmt.Sprintf(" (@%s)", user.Username)
		report.WriteString(usernamePart)
	}

	userIDLine := fmt.Sprintf("\n🆔 User ID: <code>%d</code>\n", user.Id)
	report.WriteString(userIDLine)

	if chat.Type != chatTypePrivate {
		chatTitle := html.EscapeString(chat.Title)
		chatLine := fmt.Sprintf("💬 Chat: %s (<code>%d</code>)\n", chatTitle, chat.Id)
		report.WriteString(chatLine)
	}

	escapedText := html.EscapeString(reportText)
	messageBlock := fmt.Sprintf("\n📝 <b>Message:</b>\n%s", escapedText)
	report.WriteString(messageBlock)

	if dbErr == nil {
		replyHint := fmt.Sprintf("\n\n📌 Report ID: <code>#%d</code>\n💬 Reply: <code>/reply %d your answer</code>", reportID, reportID)
		report.WriteString(replyHint)
	}

	result := report.String()

	return result
}

// sendReportThanks confirms a report to the user.
func sendReportThanks(chatID int64, replyToMessageID int) {
	thanks := payload.TgPlSendMessage{
		ChatId:              chatID,
		ParseMode:           payload.ParseModeHTML,
		Text:                reportThanksText,
		ReplyToMessageId:    replyToMessageID,
		DisableNotification: true,
	}

	message.NewSendMessage(thanks)
}
