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
	"github.com/timoffmax/social-link-bot/model/api/response/utils"
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
	reportMenuButtonText   = "🐞 Report a bug"
	reportFailedButtonText = "🐞 Report problem"
	reportPostButtonText   = "🐞 Report a bug"
)

// reportPostTitle opens a report filed with the button under a post.
const reportPostTitle = "Post reported with the button"

// Buttons of a post report prompt; they act on the prompt they sit under.
const (
	reportSendCallback      = "/report_send"
	reportCancelCallback    = "/report_cancel"
	reportSendButtonText    = "📨 Send without details"
	reportCancelButtonText  = "✖️ Cancel"
	reportPostPromptTitle   = "🐞 What's wrong with this post?"
	reportPostPromptPrivate = "<b>%s</b>\n\nType a few words in the next 10 minutes, and they go into the report together with the post. Or send it as is."
	reportPostPromptGroup   = "<b>%s</b>\n\n%sreply to this message with a few words, and they go into the report together with the post. Or send it as is."
	reportSentText          = "✅ Report sent. Thank you!"
	reportCanceledText      = "Report canceled."
	reportNotYoursToast     = "Only the person who started this report can use these buttons."
	reportPromptGoneToast   = "This report is no longer open. Tap 🐞 Report a bug under the post to start again."
	reportPromptGoneText    = "This report is no longer open."
	reportPromptFailedToast = "Couldn't start the report. Please try again later."
)

// Toasts of the report button under posts.
const (
	reportPostStartToast  = "Tell me what's wrong, or send it as is."
	reportPostRepeatToast = "You already reported this post. Thank you!"
	reportPostTooOldToast = "This post is too old to report with the button. Use /report instead."
	reportPostFailedToast = "Couldn't save the report. Please try again later."
)

// reportPromptTitle starts the prompt.
const reportPromptTitle = "🐞 What went wrong?"

// Titles of report prompts sent by earlier versions; replies to them get
// the expired note.
const (
	reportLegacyFollowUpTitle   = "🐛 Reported, thank you!"
	reportLegacyPromptTitle     = "🐛 What went wrong?"
	reportLegacyPostPromptTitle = "🐛 What's wrong with this post?"
)

// reportPromptTitles start every report prompt the bot sends or has sent.
var reportPromptTitles = []string{
	reportPromptTitle,
	reportPostPromptTitle,
	reportLegacyFollowUpTitle,
	reportLegacyPromptTitle,
	reportLegacyPostPromptTitle,
}

// reportPromptTemplate asks for the report: the title, the user mention
// (empty when the prompt replies to the user) and how to answer.
const reportPromptTemplate = "<b>%s</b>\n\n%s%s"

// How to answer the report prompt: in a private chat the next message
// counts, in a group a reply to the prompt.
const (
	reportPromptPrivateHow = "Describe the problem in your next message (within 10 minutes). Paste the link if there is one."
	reportPromptGroupHow   = "Reply to this message and describe the problem. Paste the link if there is one."
)

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

	how := reportPromptGroupHow

	if chat.Type == chatTypePrivate {
		how = reportPromptPrivateHow
	}

	prompt := payload.TgPlSendMessage{
		ChatId:              chat.Id,
		ParseMode:           payload.ParseModeHTML,
		Text:                fmt.Sprintf(reportPromptTemplate, reportPromptTitle, mention, how),
		ReplyToMessageId:    replyToMessageID,
		DisableNotification: true,
		ReplyMarkup:         &api.TgForceReply{ForceReply: true, InputFieldPlaceholder: reportPromptPlaceholder, Selective: true},
	}

	sent := message.NewSendMessage(prompt)
	awaitReport(chat, user, sent.ResponsePayload, "", "")
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

// reportReplyTTL is how long the bot waits for the user's answer to a
// report prompt.
const reportReplyTTL = 48 * time.Hour

// pendingReport is a report prompt awaiting its user's answer; Context is
// what the report is about (a post), joined with the answer into one report.
type pendingReport struct {
	UserID   int
	Context  string
	PromptID int
	ClaimKey string
}

// reportReplies are the pending reports by prompt ("chat:message"). The
// list is in memory: after a restart, an answer to an older prompt gets the
// expired note in a private chat and is handled normally in a group.
var reportReplies = newTTLStore(reportReplyTTL)

// reportNextMessageTTL is how long the user's next message in a private chat
// answers their report prompt without a reply; later ones are handled
// normally, so an ignored prompt doesn't swallow a link sent hours later.
const reportNextMessageTTL = 10 * time.Minute

// reportNextMessages map a private chat ("chat:user") to its pending
// report's prompt, so the user's next message answers it without a reply.
var reportNextMessages = newTTLStore(reportNextMessageTTL)

// awaitReport waits for the user's answer to a sent prompt; a send without a
// message id (failed) is ignored. It reports whether it waits.
func awaitReport(chat *response.TgChat, user *response.TgUser, sent *response.TgMessage, context string, claimKey string) bool {
	result := (sent != nil) && (sent.MessageId != 0)

	if result == true {
		pending := pendingReport{UserID: user.Id, Context: context, PromptID: sent.MessageId, ClaimKey: claimKey}
		setPendingReport(chat, pending)
		pointNextMessage(chat, pending)
	}

	return result
}

// setPendingReport stores a pending report under its prompt.
func setPendingReport(chat *response.TgChat, pending pendingReport) {
	key := reportReplyKey(chat.Id, pending.PromptID)
	reportReplies.set(key, pending)
}

// pointNextMessage makes the user's next message in a private chat answer
// this (newest) prompt.
func pointNextMessage(chat *response.TgChat, pending pendingReport) {
	if chat.Type == chatTypePrivate {
		nextKey := reportNextKey(chat.Id, pending.UserID)
		reportNextMessages.set(nextKey, pending.PromptID)
	}
}

// reportReplyKey names a prompt awaiting an answer.
func reportReplyKey(chatID int64, messageID int) string {
	result := fmt.Sprintf("%d:%d", chatID, messageID)

	return result
}

// reportNextKey names a user's pending report in a private chat.
func reportNextKey(chatID int64, userID int) string {
	result := fmt.Sprintf("%d:%d", chatID, userID)

	return result
}

// takePendingReport takes the user's pending report off its prompt,
// atomically, so it is answered once; another user's attempt leaves it.
func takePendingReport(chatID int64, promptID int, userID int) (pendingReport, bool) {
	key := reportReplyKey(chatID, promptID)
	value, isTaken := reportReplies.takeIf(key, isReplyFrom(userID))
	result, isPending := value.(pendingReport)
	isFound := (isTaken == true) && (isPending == true)

	// The next-message pointer goes only if it points at this prompt, not at
	// a newer one.
	if isFound == true {
		nextKey := reportNextKey(chatID, userID)
		reportNextMessages.takeIf(nextKey, isPrompt(promptID))
	}

	return result, isFound
}

// isPrompt accepts a next-message pointer at the prompt.
func isPrompt(promptID int) func(value interface{}) bool {
	result := func(value interface{}) bool {
		pointed, isID := value.(int)
		isSame := (isID == true) && (pointed == promptID)

		return isSame
	}

	return result
}

// reportSaveFailedText answers an answer whose report couldn't be sent; the
// prompt keeps waiting, so the user can try again.
const reportSaveFailedText = "❌ Couldn't send your report. Please try again later."

// reportPromptExpiredText answers a reply to a prompt the bot no longer
// waits on (answered already, expired, or sent before a restart).
const reportPromptExpiredText = "This report message has expired. Send /report to report again."

// handleReportPromptReply files the user's answer to their report prompt,
// together with what the report is about, as one report: a reply to the
// prompt, or in a private chat simply the next message. Only the user the
// prompt was for answers it, once; other messages are handled normally. It
// returns false for any message it didn't handle.
func handleReportPromptReply(origMessage *response.TgMessage, origChat *response.TgChat, origUser *response.TgUser) bool {
	promptID := answeredPromptID(origMessage, origChat, origUser)
	pending, isAwaited := takePendingReport(origChat.Id, promptID, origUser.Id)
	isExpired := (isAwaited == false) && (isExpiredReportReply(origMessage, origChat) == true)

	if isExpired == true {
		sendReportReply(origChat.Id, origMessage.MessageId, reportPromptExpiredText)
	}

	if isAwaited == true {
		description := messageText(origMessage)
		reportText := joinReportParts(description, pending.Context)
		outcome := submitReport(origChat, origUser, reportText)
		finishPendingReport(origChat, pending, outcome, origMessage.MessageId)
	}

	result := (isAwaited == true) || (isExpired == true)

	return result
}

// answeredPromptID returns the prompt a message answers: a prompt it replies
// to, or in a private chat the user's newest prompt of the last 10 minutes
// (also when they reply to the post instead). A message that is only a link,
// or has no text, isn't taken as an answer that way: it is downloaded or
// ignored as usual. 0 for none.
func answeredPromptID(origMessage *response.TgMessage, origChat *response.TgChat, origUser *response.TgUser) int {
	result := 0
	botMessage := repliedBotMessage(origMessage)
	isPrivate := (origChat.Type == chatTypePrivate)
	isTrackedReply := false

	if botMessage != nil {
		key := reportReplyKey(origChat.Id, botMessage.MessageId)
		_, isTrackedReply = reportReplies.get(key)
	}

	if isTrackedReply == true {
		result = botMessage.MessageId
	}

	isNextAnswer := (isTrackedReply == false) && (isPrivate == true) && (isTextAnswer(origMessage, origChat) == true)

	if isNextAnswer == true {
		nextKey := reportNextKey(origChat.Id, origUser.Id)
		value, _ := reportNextMessages.get(nextKey)
		result, _ = value.(int)
	}

	return result
}

// isTextAnswer reports a message with words in it: not empty (a sticker, a
// photo without caption) and not just a link to download.
func isTextAnswer(origMessage *response.TgMessage, origChat *response.TgChat) bool {
	text := messageText(origMessage)
	links := utils.GetAllSocialLinks(origMessage, origChat.Id)
	isOnlyLink := (len(links) == 1) && (strings.TrimSpace(text) == strings.TrimSpace(links[0]))
	result := (text != "") && (isOnlyLink == false)

	return result
}

// finishPendingReport closes a sent report: the prompt loses its buttons and
// says it was sent, and an answer message gets the thank-you. A report that
// was neither saved nor delivered keeps waiting and says so.
func finishPendingReport(chat *response.TgChat, pending pendingReport, outcome reportOutcome, answerMessageID int) {
	if (outcome.IsDelivered == true) && (pending.Context != "") {
		editMessage(chat.Id, pending.PromptID, reportSentText, nil)
	}

	if (outcome.IsDelivered == true) && (answerMessageID != 0) {
		sendReportThanks(chat.Id, answerMessageID)
	}

	// An undelivered report keeps waiting; the next-message pointer comes
	// back only if no newer prompt took it.
	if outcome.IsDelivered == false {
		setPendingReport(chat, pending)
		restoreNextMessage(chat, pending)
	}

	if (outcome.IsDelivered == false) && (answerMessageID != 0) {
		sendReportReply(chat.Id, answerMessageID, reportSaveFailedText)
	}
}

// restoreNextMessage points the next message back at the prompt unless a
// newer prompt is pointed at.
func restoreNextMessage(chat *response.TgChat, pending pendingReport) {
	if chat.Type == chatTypePrivate {
		nextKey := reportNextKey(chat.Id, pending.UserID)
		reportNextMessages.add(nextKey, pending.PromptID)
	}
}

// isExpiredReportReply reports a reply, in a private chat, to one of the
// bot's report prompts that no answer is awaited on any more, so it isn't
// lost in silence (a link in it isn't downloaded either: it was meant as a
// report). In groups such replies are handled normally, since they may come
// from anyone.
func isExpiredReportReply(origMessage *response.TgMessage, origChat *response.TgChat) bool {
	result := false
	botMessage := repliedBotMessage(origMessage)
	isPrivate := (origChat.Type == chatTypePrivate)

	if (botMessage != nil) && (isPrivate == true) {
		key := reportReplyKey(origChat.Id, botMessage.MessageId)
		_, isTracked := reportReplies.get(key)
		isPrompt := isReportPromptText(botMessage.Text)
		result = (isTracked == false) && (isPrompt == true)
	}

	return result
}

// isReportPromptText reports a message that starts with a report prompt
// title.
func isReportPromptText(text string) bool {
	result := false

	for _, title := range reportPromptTitles {
		result = result || strings.HasPrefix(text, title)
	}

	return result
}

// isReplyFrom accepts a pending report started by the user.
func isReplyFrom(userID int) func(value interface{}) bool {
	result := func(value interface{}) bool {
		pending, isPending := value.(pendingReport)
		isFromUser := (isPending == true) && (pending.UserID == userID)

		return isFromUser
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
		log.Printf("🐞 Report button on an inaccessible message by user %d, ignored", callbackQuery.From.Id)
	}
}

// handleReportPostCallback starts a report about the post under which the
// button was tapped: it asks what's wrong, and the answer (or "Send without
// details") sends one report with the post's caption, links and the user's
// message with the link. Each user starts one report per post (others still
// can).
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
		toast = startPostReport(&post, &callbackQuery.From, claimKey)
	}

	answerCallbackQueryText(callbackQuery.Id, toast)
}

// startPostReport sends the "what's wrong" prompt under the post and waits
// for the answer; a prompt that couldn't be sent releases the claim.
func startPostReport(post *response.TgMessage, user *response.TgUser, claimKey string) string {
	context := postReportText(post)
	text := fmt.Sprintf(reportPostPromptPrivate, reportPostPromptTitle)

	if post.Chat.Type != chatTypePrivate {
		mention := reportUserMention(&post.Chat, user)
		text = fmt.Sprintf(reportPostPromptGroup, reportPostPromptTitle, mention)
	}

	prompt := payload.TgPlSendMessage{
		ChatId:              post.Chat.Id,
		ParseMode:           payload.ParseModeHTML,
		Text:                text,
		ReplyToMessageId:    post.MessageId,
		DisableNotification: true,
		ReplyMarkup:         reportPromptMarkup(),
	}
	sent := message.NewSendMessage(prompt)
	isWaiting := awaitReport(&post.Chat, user, sent.ResponsePayload, context, claimKey)
	result := reportPostStartToast

	if isWaiting == false {
		reportClaims.remove(claimKey)
		result = reportPromptFailedToast
	}

	return result
}

// reportPromptMarkup has the prompt's send-as-is and cancel buttons.
func reportPromptMarkup() *api.TgInlineKeyboardMarkup {
	sendButton := api.TgInlineKeyboardButton{Text: reportSendButtonText, CallbackData: reportSendCallback}
	cancelButton := api.TgInlineKeyboardButton{Text: reportCancelButtonText, CallbackData: reportCancelCallback}
	result := &api.TgInlineKeyboardMarkup{InlineKeyboard: [][]api.TgInlineKeyboardButton{{sendButton, cancelButton}}}

	return result
}

// isReportPromptCallback reports the send-as-is and cancel buttons.
func isReportPromptCallback(data string) bool {
	result := (data == reportSendCallback) || (data == reportCancelCallback)

	return result
}

// handleReportPromptCallback sends the pending report as it is, or cancels
// it; only the user who started it can.
func handleReportPromptCallback(callbackQuery *response.TgCallbackQuery) {
	prompt := callbackQuery.Message
	user := callbackQuery.From
	pending, isMine := takePendingReport(prompt.Chat.Id, prompt.MessageId, user.Id)
	key := reportReplyKey(prompt.Chat.Id, prompt.MessageId)
	_, isStillPending := reportReplies.get(key)
	toast := reportPromptGoneToast
	isSend := (callbackQuery.Data == reportSendCallback)

	if (isMine == false) && (isStillPending == true) {
		toast = reportNotYoursToast
	}

	// A prompt nobody waits on any more (sent, canceled, expired or from
	// before a restart) loses its buttons.
	if (isMine == false) && (isStillPending == false) {
		editMessage(prompt.Chat.Id, prompt.MessageId, reportPromptGoneText, nil)
	}

	if (isMine == true) && (isSend == true) {
		reportText := joinReportParts("", pending.Context)
		outcome := submitReport(&prompt.Chat, &user, reportText)
		finishPendingReport(&prompt.Chat, pending, outcome, 0)
		toast = reportSendToast(outcome)
	}

	if (isMine == true) && (isSend == false) {
		cancelPendingReport(&prompt, pending)
		toast = reportCanceledText
	}

	answerCallbackQueryText(callbackQuery.Id, toast)
}

// reportSendToast confirms a report sent as it is, or says it failed.
func reportSendToast(outcome reportOutcome) string {
	result := reportPostFailedToast

	if outcome.IsDelivered == true {
		result = reportSentText
	}

	return result
}

// cancelPendingReport drops a pending report: the prompt says so, and the
// post can be reported again.
func cancelPendingReport(prompt *response.TgMessage, pending pendingReport) {
	editMessage(prompt.Chat.Id, prompt.MessageId, reportCanceledText, nil)

	if pending.ClaimKey != "" {
		reportClaims.remove(pending.ClaimKey)
	}
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

	log.Printf("🐞 Report #%d from user %d (sent to owner: %t): %s", reportID, user.Id, isSentToOwner, reportText)
	result := reportOutcome{ID: reportID, IsDelivered: (reportID > 0) || (isSentToOwner == true)}

	return result
}

// ownerReportText formats a report for the owner.
func ownerReportText(chat *response.TgChat, user *response.TgUser, reportText string, reportID int, dbErr error) string {
	var report strings.Builder
	firstName := html.EscapeString(user.FirstName)
	header := fmt.Sprintf("🐞 <b>New Report</b>\n\n👤 From: %s", firstName)
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
