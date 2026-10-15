package app

import (
	"fmt"
	"html"
	"log"
	"strings"
	"time"

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

// reportPostCallback is the report button under a post in a private chat.
const reportPostCallback = "/report_post"

// Report buttons.
const (
	reportMenuButtonText   = "🐛 Report a bug"
	reportFailedButtonText = "🐛 Report problem"
	reportPostButtonText   = "🐛 Report"
)

// reportPostTitle opens a report filed with the button under a post.
const reportPostTitle = "Post reported with the button"

// reportFollowUpTitle starts the thank-you for a post report; the reporter's
// reply to it adds details to the report.
const reportFollowUpTitle = "🐛 Reported, thank you!"

// reportFollowUpTemplate thanks for a post report and invites optional
// details: the title, the reporter's mention (empty in private chats) and
// the report number line. It is no forced reply, so the user's next link
// isn't taken for details by accident.
const reportFollowUpTemplate = "<b>%s</b>\n\n%s%sAnything to add? Reply to this message."

// reportNumberTemplate names the report in the follow-up.
const reportNumberTemplate = "Report #%d. "

// reportDetailsTemplate opens details replied to a post report's follow-up.
const reportDetailsTemplate = "Details for report #%d"

// Toasts of the report button under posts.
const (
	reportPostDoneToast   = "🐛 Reported, thank you!"
	reportPostRepeatToast = "You already reported this post. Thank you!"
	reportPostTooOldToast = "This post is too old to report with the button. Use /report instead."
	reportPostFailedToast = "Couldn't save the report. Please try again later."
)

// reportPromptTitle starts the prompt.
const reportPromptTitle = "🐛 What went wrong?"

// reportPromptTemplate asks for the report: the title, then the user mention
// (empty when the prompt replies to the user).
const reportPromptTemplate = "<b>%s</b>\n\n%sReply to this message and describe the problem. Paste the link if there is one."

// reportPromptPlaceholder is shown in the input field of the reply.
const reportPromptPlaceholder = "Describe the problem"

// reportThanksText confirms a report to the user.
const reportThanksText = "✅ Thanks! Your report has been received."

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
	description := commandArgsText(origMessage.Text)
	botMessage := repliedBotMessage(origMessage)
	isPrompt := ((description == "") && (botMessage == nil))

	if isPrompt == true {
		sendReportPrompt(origChat, origUser, origMessage.MessageId)
	}

	if isPrompt == false {
		reportContext := botMessageContext(botMessage)
		reportText := joinReportParts(description, reportContext)
		outcome := submitReport(origChat, origUser, reportText)
		answer := reportAnswerText(outcome)
		sendReportReply(origChat.Id, origMessage.MessageId, answer)
	}
}

// reportCommandFailedText answers a /report that was neither saved nor
// delivered.
const reportCommandFailedText = "❌ Couldn't send your report. Please try again later."

// reportAnswerText thanks for a delivered /report or says it failed.
func reportAnswerText(outcome reportOutcome) string {
	result := reportCommandFailedText

	if outcome.IsDelivered == true {
		result = reportThanksText
	}

	return result
}

// commandArgsText returns the text after the command word.
func commandArgsText(text string) string {
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

	if replyToMessageID == 0 {
		mention = reportUserMention(chat, user)
	}

	prompt := payload.TgPlSendMessage{
		ChatId:              chat.Id,
		ParseMode:           payload.ParseModeHTML,
		Text:                fmt.Sprintf(reportPromptTemplate, reportPromptTitle, mention),
		ReplyToMessageId:    replyToMessageID,
		DisableNotification: true,
		ReplyMarkup:         &api.TgForceReply{ForceReply: true, InputFieldPlaceholder: reportPromptPlaceholder, Selective: true},
	}

	sent := message.NewSendMessage(prompt)
	rememberReportReply(chat.Id, sent.ResponsePayload, user.Id, 0)
}

// reportUserMention @mentions the user in a group (a link mention without a
// username), so a selective forced reply targets them; empty in private chats.
func reportUserMention(chat *response.TgChat, user *response.TgUser) string {
	result := ""
	isGroup := (chat.Type != chatTypePrivate)

	if (isGroup == true) && (user.Username != "") {
		result = fmt.Sprintf("@%s: ", user.Username)
	}

	if (isGroup == true) && (user.Username == "") {
		name := html.EscapeString(user.FirstName)
		result = fmt.Sprintf("<a href=\"tg://user?id=%d\">%s</a>: ", user.Id, name)
	}

	return result
}

// reportReplyTTL is how long the bot waits for a reply to its report prompt
// or follow-up.
const reportReplyTTL = 48 * time.Hour

// reportReplyTarget is a report prompt or follow-up awaiting its user's
// reply; ReportID is the report a follow-up's reply adds details to.
type reportReplyTarget struct {
	UserID   int
	ReportID int
}

// reportReplies are the prompts and follow-ups awaiting a reply, by
// "chat:message". The list is in memory: after a restart, a reply to an
// older prompt is handled as a normal message.
var reportReplies = newTTLStore(reportReplyTTL)

// rememberReportReply waits for the user's reply to a sent prompt or
// follow-up; a send without a message id (failed) is ignored.
func rememberReportReply(chatID int64, sent *response.TgMessage, userID int, reportID int) {
	isSent := (sent != nil) && (sent.MessageId != 0)

	if isSent == true {
		key := reportReplyKey(chatID, sent.MessageId)
		reportReplies.set(key, reportReplyTarget{UserID: userID, ReportID: reportID})
	}
}

// reportReplyKey names a prompt or follow-up awaiting a reply.
func reportReplyKey(chatID int64, messageID int) string {
	result := fmt.Sprintf("%d:%d", chatID, messageID)

	return result
}

// reportSaveFailedText answers a reply whose report couldn't be saved; the
// prompt keeps waiting, so the user can reply again.
const reportSaveFailedText = "❌ Couldn't save your report. Please reply again later."

// reportPromptExpiredText answers a reply to a prompt or follow-up the bot no
// longer waits on (answered already, expired, or sent before a restart).
const reportPromptExpiredText = "This report message has expired. Send /report to report again."

// handleReportPromptReply files the user's reply to their report prompt or
// follow-up as a report; one reply each, and only from the user it was sent
// to, so other members' replies (or links) are handled normally. The
// awaited reply is taken atomically, and put back when the report couldn't
// be saved. It returns false for any other message.
func handleReportPromptReply(origMessage *response.TgMessage, origChat *response.TgChat, origUser *response.TgUser) bool {
	target, isAwaited := takeAwaitedReportReply(origMessage, origChat, origUser)
	isExpired := (isAwaited == false) && (isExpiredReportReply(origMessage, origChat) == true)

	if isExpired == true {
		sendReportReply(origChat.Id, origMessage.MessageId, reportPromptExpiredText)
	}

	if isAwaited == true {
		description := messageText(origMessage)
		detailsContext := reportDetailsContext(target.ReportID)
		reportText := joinReportParts(description, detailsContext)
		outcome := submitReport(origChat, origUser, reportText)
		finishReportReply(origMessage, origChat, target, outcome)
	}

	result := (isAwaited == true) || (isExpired == true)

	return result
}

// isExpiredReportReply reports a reply, in a private chat, to one of the
// bot's report prompts or follow-ups that no reply is awaited on any more,
// so it isn't lost in silence (a link in it isn't downloaded either: it was
// meant as a report). In groups such replies are handled normally, since
// they may come from anyone.
func isExpiredReportReply(origMessage *response.TgMessage, origChat *response.TgChat) bool {
	result := false
	botMessage := repliedBotMessage(origMessage)
	isPrivate := (origChat.Type == chatTypePrivate)

	if (botMessage != nil) && (isPrivate == true) {
		key := reportReplyKey(origChat.Id, botMessage.MessageId)
		_, isTracked := reportReplies.get(key)
		isPrompt := strings.HasPrefix(botMessage.Text, reportPromptTitle)
		isFollowUp := strings.HasPrefix(botMessage.Text, reportFollowUpTitle)
		result = (isTracked == false) && ((isPrompt == true) || (isFollowUp == true))
	}

	return result
}

// takeAwaitedReportReply takes the prompt or follow-up the message replies
// to, when it awaits this user's reply; another user's reply leaves it.
func takeAwaitedReportReply(origMessage *response.TgMessage, origChat *response.TgChat, origUser *response.TgUser) (reportReplyTarget, bool) {
	result := reportReplyTarget{}
	isAwaited := false
	botMessage := repliedBotMessage(origMessage)
	key := ""

	if botMessage != nil {
		key = reportReplyKey(origChat.Id, botMessage.MessageId)
		value, isTaken := reportReplies.takeIf(key, isReplyFrom(origUser.Id))
		result, isAwaited = value.(reportReplyTarget)
		isAwaited = (isTaken == true) && (isAwaited == true)
	}

	return result, isAwaited
}

// isReplyFrom accepts an awaited reply addressed to the user.
func isReplyFrom(userID int) func(value interface{}) bool {
	result := func(value interface{}) bool {
		target, isTarget := value.(reportReplyTarget)
		isFromUser := (isTarget == true) && (target.UserID == userID)

		return isFromUser
	}

	return result
}

// finishReportReply thanks for a delivered reply; one that neither was saved
// nor reached the owner puts the awaited reply back and says so.
func finishReportReply(origMessage *response.TgMessage, origChat *response.TgChat, target reportReplyTarget, outcome reportOutcome) {
	if outcome.IsDelivered == true {
		sendReportThanks(origChat.Id, origMessage.MessageId)
	}

	if outcome.IsDelivered == false {
		key := reportReplyKey(origChat.Id, origMessage.ReplyToMessage.MessageId)
		reportReplies.set(key, target)
		sendReportReply(origChat.Id, origMessage.MessageId, reportSaveFailedText)
	}
}

// reportDetailsContext names the report that details belong to; empty for a
// reply to the prompt.
func reportDetailsContext(reportID int) string {
	result := ""

	if reportID > 0 {
		result = fmt.Sprintf(reportDetailsTemplate, reportID)
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

	claimKey := reportReplyKey(errorMessage.Chat.Id, errorMessage.MessageId)

	if isAccessible == true {
		isClaimed = reportClaims.add(claimKey, true)
	}

	if isClaimed == true {
		reportText := failedDownloadReportText(&errorMessage)
		outcome := submitReport(&errorMessage.Chat, &callbackQuery.From, reportText)
		finishFailedReport(&errorMessage, claimKey, outcome)
	}

	if isAccessible == false {
		log.Printf("🐛 Report button on an inaccessible message by user %d, ignored", callbackQuery.From.Id)
	}
}

// handleReportPostCallback reports the post under which the button was
// tapped: its caption and links, and the user's message with the link it
// replies to. Each user reports a post once (others still can); the button
// is answered with a toast and the report is followed by an invitation to
// add details.
func handleReportPostCallback(callbackQuery *response.TgCallbackQuery) {
	post := callbackQuery.Message
	isAccessible := (post.Date != 0)
	isClaimed := false
	toast := reportPostTooOldToast

	postKey := reportReplyKey(post.Chat.Id, post.MessageId)
	claimKey := fmt.Sprintf("%s:%d", postKey, callbackQuery.From.Id)

	if isAccessible == true {
		isClaimed = reportClaims.add(claimKey, true)
		toast = reportPostRepeatToast
	}

	if isClaimed == true {
		reportText := postReportText(&post)
		outcome := submitReport(&post.Chat, &callbackQuery.From, reportText)
		toast = finishPostReport(&post, &callbackQuery.From, claimKey, outcome)
	}

	answerCallbackQueryText(callbackQuery.Id, toast)
}

// finishFailedReport replaces the error message's button with a thank-you
// once the report is delivered; an undelivered report releases its claim,
// so the button can be tapped again.
func finishFailedReport(errorMessage *response.TgMessage, claimKey string, outcome reportOutcome) {
	escapedError := html.EscapeString(errorMessage.Text)
	doneText := fmt.Sprintf("%s%s", escapedError, reportFailedDoneSuffix)

	if outcome.IsDelivered == true {
		editMessage(errorMessage.Chat.Id, errorMessage.MessageId, doneText, nil)
	}

	if outcome.IsDelivered == false {
		reportClaims.remove(claimKey)
	}
}

// finishPostReport invites details once the report is delivered and returns
// the toast; an undelivered report releases its claim, so the user can tap
// again.
func finishPostReport(post *response.TgMessage, user *response.TgUser, claimKey string, outcome reportOutcome) string {
	result := reportPostFailedToast

	if outcome.IsDelivered == true {
		sendReportFollowUp(&post.Chat, user, post.MessageId, outcome.ID)
		result = reportPostDoneToast
	}

	if outcome.IsDelivered == false {
		reportClaims.remove(claimKey)
	}

	return result
}

// postReportText describes a reported post and the user's message with the
// link it replies to.
func postReportText(post *response.TgMessage) string {
	var text strings.Builder
	text.WriteString(reportPostTitle)
	original := post.ReplyToMessage

	if original != nil {
		originalText := messageText(original)
		excerpt := reportExcerpt(originalText)
		userLine := fmt.Sprintf("\n\nUser's message: %s", excerpt)
		text.WriteString(userLine)
	}

	postContext := botMessageContext(post)
	postLine := fmt.Sprintf("\n\n%s", postContext)
	text.WriteString(postLine)
	result := text.String()

	return result
}

// sendReportFollowUp thanks for a delivered post report, names its number
// (when it was saved) and invites the reporter's details. It replies to the
// bot's post; in a group it mentions the reporter.
func sendReportFollowUp(chat *response.TgChat, user *response.TgUser, postMessageID int, reportID int) {
	mention := reportUserMention(chat, user)
	numberLine := ""

	if reportID > 0 {
		numberLine = fmt.Sprintf(reportNumberTemplate, reportID)
	}

	followUp := payload.TgPlSendMessage{
		ChatId:              chat.Id,
		ParseMode:           payload.ParseModeHTML,
		Text:                fmt.Sprintf(reportFollowUpTemplate, reportFollowUpTitle, mention, numberLine),
		ReplyToMessageId:    postMessageID,
		DisableNotification: true,
	}

	sent := message.NewSendMessage(followUp)
	rememberReportReply(chat.Id, sent.ResponsePayload, user.Id, reportID)
}

// reportClaimTTL is how long a claim is kept: Telegram stops sending a
// message with button presses after 48 hours.
const reportClaimTTL = 48 * time.Hour

// reportClaims are the error messages ("chat:message") and posts
// ("chat:message:user") already reported, so a double tap or two members
// tapping at once file one report.
var reportClaims = newTTLStore(reportClaimTTL)

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

// createReport saves a report; a variable so tests run without a database.
var createReport = services.CreateReport

// reportOutcome says what became of a report: its saved number (0 when the
// save failed) and whether it reached the owner, saved or not.
type reportOutcome struct {
	ID          int
	IsDelivered bool
}

// submitReport saves the report and sends it to the owner with a /reply hint.
func submitReport(chat *response.TgChat, user *response.TgUser, reportText string) reportOutcome {
	adminUserID := config.GetAdminUserID()
	chatTitle := chat.Title
	isPrivate := (chat.Type == chatTypePrivate)

	if isPrivate == true {
		chatTitle = reportPrivateChatTitle
	}

	userID := int64(user.Id)
	reportID, dbErr := createReport(userID, user.Username, user.FirstName, chat.Id, chatTitle, reportText)

	if dbErr != nil {
		log.Printf("❌ Failed to save report to DB: %v", dbErr)
	}

	ownerText := ownerReportText(chat, user, reportText, reportID, dbErr)

	isSentToOwner := false

	if adminUserID != 0 {
		ownerMessage := payload.TgPlSendMessage{
			ChatId:                adminUserID,
			ParseMode:             payload.ParseModeHTML,
			Text:                  ownerText,
			DisableWebPagePreview: true,
		}
		sent := message.NewSendMessage(ownerMessage)
		isSentToOwner = (sent.ResponsePayload != nil) && (sent.ResponsePayload.MessageId != 0)
	}

	log.Printf("🐛 Report #%d from user %d (sent to owner: %t): %s", reportID, user.Id, isSentToOwner, reportText)
	result := reportOutcome{ID: reportID, IsDelivered: (reportID > 0) || (isSentToOwner == true)}

	return result
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
	sendReportReply(chatID, replyToMessageID, reportThanksText)
}

// sendReportReply answers the user's report message.
func sendReportReply(chatID int64, replyToMessageID int, text string) {
	reply := payload.TgPlSendMessage{
		ChatId:              chatID,
		ParseMode:           payload.ParseModeHTML,
		Text:                text,
		ReplyToMessageId:    replyToMessageID,
		DisableNotification: true,
	}

	message.NewSendMessage(reply)
}
