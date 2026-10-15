package app

import (
	"errors"
	"fmt"
	"html"
	"log"
	"strconv"
	"strings"

	"github.com/timoffmax/social-link-bot/config"
	"github.com/timoffmax/social-link-bot/model/api"
	"github.com/timoffmax/social-link-bot/model/api/request/message"
	"github.com/timoffmax/social-link-bot/model/api/request/payload"
	"github.com/timoffmax/social-link-bot/model/api/response"
	"github.com/timoffmax/social-link-bot/services"
)

// Optional Telegram Stars "support the developer" commands; /support is an
// alias of /donate.
const (
	supportCommand        = "/support"
	supportCallbackPrefix = "/support_"
	paySupportCommand     = "/paysupport"
	termsCommand          = "/terms"
	starsRefundCommand    = "/starsrefund"
)

// updateTypePreCheckoutQuery is the allowed_updates name of a Stars
// pre-checkout query.
const updateTypePreCheckoutQuery = "pre_checkout_query"

// Positions of the /starsrefund words: "/starsrefund USER CHARGE [mark]".
const (
	starsRefundUserIndex   = 1
	starsRefundChargeIndex = 2
	starsRefundMarkIndex   = 3
)

// Word counts of /starsrefund without and with the "mark" argument.
const (
	starsRefundWords     = starsRefundChargeIndex + 1
	starsRefundMarkWords = starsRefundMarkIndex + 1
)

// supportInvoiceTitle is the invoice title (Telegram allows 1-32 characters).
const supportInvoiceTitle = "Support Fix Link Bot"

// supportInvoiceDescriptionTemplate is the invoice description (1-255
// characters); %d is the tier's bonus downloads.
const supportInvoiceDescriptionTemplate = "Voluntary support for the developer. Thank-you: %d bonus downloads of restricted Instagram posts. By paying you agree to /terms."

// supportInvoicePriceLabel labels the invoice's single price.
const supportInvoicePriceLabel = "Support"

// supportButtonTemplate is a tier button: Stars, then bonus downloads.
const supportButtonTemplate = "⭐ %d Stars: +%d bonus downloads"

// supportUnavailableText answers a tier button while Stars support is off.
const supportUnavailableText = "Support with Telegram Stars isn't available right now. Thank you for thinking of it!"

// paySupportText is the /paysupport reply Telegram requires of bots that
// accept payments.
const paySupportText = `💬 <b>Help with payments</b>

For any question about a Stars payment to this bot, send <code>/report</code> followed by your message: it goes straight to the bot owner. Please include the date and amount of the payment.

Telegram support can't help with purchases made in this bot.

Refunds: support is voluntary, so if you paid by mistake or changed your mind, ask via /report and the Stars are refunded in full. Bonus downloads from that payment that are still unused are removed.`

// termsText is the /terms reply.
const termsText = `📄 <b>Terms of support</b>

• Support payments in Telegram Stars are voluntary. The bot stays free without them.
• Bonus downloads of restricted Instagram posts are a thank-you, not a guaranteed service. They're used once your monthly free downloads, or the bot's monthly limit, run out. The feature, its limits and the services behind it may change or stop.
• Refunds: ask via /paysupport. The Stars are refunded in full and the unused bonus downloads of that payment are removed.
• Telegram support can't help with purchases made in this bot; use /paysupport.`

// Pre-checkout refusals, shown to the user by Telegram.
const (
	preCheckoutDisabledText  = "Support with Telegram Stars isn't available right now."
	preCheckoutInvalidText   = "This invoice is no longer valid. Send /donate to get a new one."
	preCheckoutWrongUserText = "This invoice was created for someone else. Send /donate to get your own."
)

// Successful payment replies.
const (
	supportThanksTemplate    = "⭐ Thank you for supporting Fix Link Bot!\n\nYou got %d bonus downloads of restricted posts. Bonus downloads left: %d.\nThey're used once your monthly free downloads, or the bot's monthly limit, run out."
	supportRepeatTemplate    = "⭐ Thank you! This payment was already counted. Bonus downloads left: %d."
	supportNoTierText        = "⭐ Thank you for your support! This payment didn't match a support tier, so no bonus downloads were added. If that's wrong, see /paysupport."
	supportRecordFailedText  = "⭐ Thank you! Your payment arrived, but the bonus downloads couldn't be added right now. The owner has been told and will fix it; see /paysupport."
	supportPaymentAlertText  = "⭐ Stars payment: %d Stars from user %d in chat %d (charge %s, %d credits)"
	supportRecordAlertText   = "⚠️ Stars payment of %d Stars from user %d (charge %s) could not be recorded: %v"
	supportGatedHintTemplate = "Get bonus downloads by supporting the bot: %s"
)

// /starsrefund replies (owner only).
const (
	starsRefundUsageText       = "Usage (owner only): <code>/starsrefund USER_ID CHARGE_ID</code>, or <code>/starsrefund USER_ID CHARGE_ID mark</code> to only record a refund Telegram already made"
	starsRefundDoneTemplate    = "✅ Refunded charge <code>%s</code> of user <code>%d</code>. Removed %d unused bonus downloads; %d left."
	starsRefundNoRecordText    = "✅ Refunded charge <code>%s</code> of user <code>%d</code> in Telegram. There's no local record of it, so no credits were removed."
	starsRefundMarkFailedText  = "⚠️ Refunded charge <code>%s</code> in Telegram, but marking it refunded failed: %s"
	starsRefundFailedTemplate  = "❌ Refund failed: %s"
	starsRefundRefusedTemplate = "❌ %s\n\n%s"
)

// errStarsRefundWrongUser is returned when the charge belongs to another user.
var errStarsRefundWrongUser = errors.New("this charge belongs to another user")

// errStarsRefundAlreadyDone is returned for a charge already refunded.
var errStarsRefundAlreadyDone = errors.New("this charge is already refunded")

// supportTierKeyboard has one button per tier, cheapest first.
func supportTierKeyboard() *api.TgInlineKeyboardMarkup {
	result := &api.TgInlineKeyboardMarkup{}
	tiers := services.SupportTiers()

	for _, tier := range tiers {
		button := api.TgInlineKeyboardButton{
			Text:         fmt.Sprintf(supportButtonTemplate, tier.Stars, tier.Credits),
			CallbackData: fmt.Sprintf("%s%d", supportCallbackPrefix, tier.Stars),
		}
		result.InlineKeyboard = append(result.InlineKeyboard, []api.TgInlineKeyboardButton{button})
	}

	return result
}

// parseSupportTierCallback returns the tier of a "/support_<stars>" button.
func parseSupportTierCallback(data string) (services.SupportTier, bool) {
	result := services.SupportTier{}
	isFound := false
	raw := strings.TrimPrefix(data, supportCallbackPrefix)
	stars, err := strconv.Atoi(raw)
	hasPrefix := strings.HasPrefix(data, supportCallbackPrefix)

	if (hasPrefix == true) && (err == nil) {
		result, isFound = services.FindSupportTier(stars)
	}

	return result, isFound
}

// handleSupportTierCallback sends the invoice of the pressed tier.
func handleSupportTierCallback(callbackQuery *response.TgCallbackQuery) {
	tier, isFound := parseSupportTierCallback(callbackQuery.Data)
	isEnabled := config.IsStarsSupportEnabled()
	chatID := callbackQuery.Message.Chat.Id
	userID := int64(callbackQuery.From.Id)

	if isFound == false {
		log.Printf("🤷 Unknown support tier: %s", callbackQuery.Data)
	}

	if (isFound == true) && (isEnabled == false) {
		sendSupportReply(chatID, callbackQuery.Message.MessageId, supportUnavailableText)
	}

	if (isFound == true) && (isEnabled == true) {
		invoice := buildSupportInvoice(chatID, userID, tier)
		sendSupportInvoice(invoice)
	}
}

// buildSupportInvoice returns a Stars invoice for the tier: currency XTR, no
// provider token, exactly one price and no reply markup (Telegram adds the
// Pay button itself).
func buildSupportInvoice(chatID int64, userID int64, tier services.SupportTier) payload.TgPlSendInvoice {
	result := payload.TgPlSendInvoice{
		ChatId:              chatID,
		Title:               supportInvoiceTitle,
		Description:         fmt.Sprintf(supportInvoiceDescriptionTemplate, tier.Credits),
		Payload:             services.BuildSupportPayload(tier, userID),
		Currency:            payload.CurrencyStars,
		Prices:              []payload.TgLabeledPrice{{Label: supportInvoicePriceLabel, Amount: tier.Stars}},
		DisableNotification: true,
	}

	return result
}

// sendSupportInvoice sends the invoice and logs a failure.
func sendSupportInvoice(invoice payload.TgPlSendInvoice) {
	sent := message.NewSendInvoice(invoice)

	if sent.Err != nil {
		log.Printf("❌ [stars] invoice %s to chat %d failed: %v", invoice.Payload, invoice.ChatId, sent.Err)
	}

	if sent.Err == nil {
		log.Printf("⭐ [stars] sent invoice %s to chat %d", invoice.Payload, invoice.ChatId)
	}
}

// handlePreCheckoutQuery answers a pre-checkout query (Telegram cancels the
// payment unless it's answered within 10 seconds, so nothing slow runs here).
func handlePreCheckoutQuery(query *response.TgPreCheckoutQuery) {
	isEnabled := config.IsStarsSupportEnabled()
	payerID := int64(query.From.Id)
	isCreditsUsable := supportCreditsUsable(payerID)
	refusal := supportPreCheckoutRefusal(query, isEnabled, isCreditsUsable)
	answer := payload.TgPlAnswerPreCheckoutQuery{
		PreCheckoutQueryId: query.Id,
		Ok:                 (refusal == ""),
		ErrorMessage:       refusal,
	}
	sent := message.NewAnswerPreCheckoutQuery(answer)

	log.Printf("⭐ [stars] pre-checkout %s from user %d (%s, %d): ok=%t %s", query.Id, query.From.Id, query.InvoicePayload, query.TotalAmount, answer.Ok, refusal)

	if sent.Err != nil {
		log.Printf("❌ [stars] answering pre-checkout %s failed: %v", query.Id, sent.Err)
	}
}

// supportCreditsUsable reports whether payments can be recorded and the payer
// could ever use the bonus credits (database, paid fetch configured, payer
// not blocked); a variable so tests can run the accept path without them.
var supportCreditsUsable = services.CanUseGatedFetchCredits

// isPaymentRelatedUpdate reports a pre-checkout query or a completed payment.
func isPaymentRelatedUpdate(update *response.TgUpdate) bool {
	hasPreCheckout := (update.PreCheckoutQuery.Id != "")
	result := (hasPreCheckout == true) || (isPaymentUpdate(update) == true)

	return result
}

// handlePaymentUpdate answers a pre-checkout query or records a payment.
func handlePaymentUpdate(update *response.TgUpdate) {
	hasPreCheckout := (update.PreCheckoutQuery.Id != "")

	if hasPreCheckout == true {
		go handlePreCheckoutQuery(&update.PreCheckoutQuery)
	}

	if hasPreCheckout == false {
		handleSuccessfulPayment(&update.Message, &update.Message.Chat, &update.Message.From)
	}
}

// isPaymentUpdate reports an update carrying a completed payment: it must
// never be skipped as too old after a restart, or the payer loses the credits.
func isPaymentUpdate(update *response.TgUpdate) bool {
	result := (update.Message.SuccessfulPayment != nil)

	return result
}

// supportPreCheckoutRefusal returns why the payment must be refused, or ""
// when it may go ahead: support is on, the payload is a known tier for this
// user, and the currency and amount match it.
func supportPreCheckoutRefusal(query *response.TgPreCheckoutQuery, isEnabled, isDBReady bool) string {
	result := ""
	invoice, err := services.ParseSupportPayload(query.InvoicePayload)
	isPriceValid := (err == nil) && (query.Currency == payload.CurrencyStars) && (query.TotalAmount == invoice.Tier.Stars)
	isOtherUser := (err == nil) && (invoice.UserID != int64(query.From.Id))

	if isOtherUser == true {
		result = preCheckoutWrongUserText
	}

	if isPriceValid == false {
		result = preCheckoutInvalidText
	}

	// Without the database the payment can't be recorded or credited, so it
	// must not be taken.
	if isDBReady == false {
		result = preCheckoutDisabledText
	}

	if isEnabled == false {
		result = preCheckoutDisabledText
	}

	return result
}

// handleSuccessfulPayment records a payment, adds its bonus credits once and
// thanks the payer. It runs even when support is off, since the payment was
// already taken.
func handleSuccessfulPayment(origMessage *response.TgMessage, origChat *response.TgChat, origUser *response.TgUser) {
	paid := origMessage.SuccessfulPayment
	userID := int64(origUser.Id)
	credits := supportPaymentCredits(paid)
	payment := services.SupportPayment{
		ChargeID: paid.TelegramPaymentChargeId,
		UserID:   userID,
		ChatID:   origChat.Id,
		Stars:    paid.TotalAmount,
		Credits:  credits,
		Payload:  paid.InvoicePayload,
	}
	record, err := services.RecordSupportPayment(payment)
	text := supportPaymentReplyText(record, err, credits)

	alert := ""

	if err != nil {
		log.Printf("❌ [stars] recording charge %s failed: %v", payment.ChargeID, err)
		alert = fmt.Sprintf(supportRecordAlertText, payment.Stars, userID, payment.ChargeID, err)
	}

	if (err == nil) && (record.IsNew == true) {
		log.Printf("⭐ [stars] user %d paid %d Stars (charge %s), +%d credits, balance %d", userID, payment.Stars, payment.ChargeID, credits, record.Balance)
		alert = fmt.Sprintf(supportPaymentAlertText, payment.Stars, userID, origChat.Id, payment.ChargeID, credits)
	}

	// The payment is recorded; the replies don't hold up the polling loop.
	go sendSupportPaymentReplies(origChat.Id, origMessage.MessageId, text, alert)
}

// sendSupportPaymentReplies thanks the payer and alerts the owner (when there
// is an alert). Every payment alerts: one alert per charge, not rate-limited.
func sendSupportPaymentReplies(chatID int64, replyToMessageID int, text, alert string) {
	if alert != "" {
		services.NotifyAdminUnthrottled(alert)
	}

	sendSupportReply(chatID, replyToMessageID, text)
}

// supportPaymentCredits returns the credits a payment earns: its tier's, when
// the payload, currency and amount match a tier; otherwise none.
func supportPaymentCredits(paid *response.TgSuccessfulPayment) int {
	result := 0
	invoice, err := services.ParseSupportPayload(paid.InvoicePayload)
	isTierPaid := (err == nil) && (paid.Currency == payload.CurrencyStars) && (paid.TotalAmount == invoice.Tier.Stars)

	if isTierPaid == true {
		result = invoice.Tier.Credits
	}

	return result
}

// supportPaymentReplyText thanks the payer, with the credits balance.
func supportPaymentReplyText(record services.SupportPaymentRecord, err error, credits int) string {
	result := fmt.Sprintf(supportThanksTemplate, credits, record.Balance)

	if record.IsNew == false {
		result = fmt.Sprintf(supportRepeatTemplate, record.Balance)
	}

	if (record.IsNew == true) && (credits == 0) {
		result = supportNoTierText
	}

	if err != nil {
		result = supportRecordFailedText
	}

	return result
}

// handlePaySupportCommand explains how to get help with a payment. It always
// answers (Telegram requires /paysupport from a bot that has taken
// payments), also while support is off.
func handlePaySupportCommand(origMessage *response.TgMessage, origChat *response.TgChat, origUser *response.TgUser) {
	sendSupportReply(origChat.Id, origMessage.MessageId, paySupportText)
}

// handleTermsCommand shows the support terms; it always answers, so people
// who paid earlier can read them while support is off.
func handleTermsCommand(origMessage *response.TgMessage, origChat *response.TgChat, origUser *response.TgUser) {
	sendSupportReply(origChat.Id, origMessage.MessageId, termsText)
}

// handleStarsRefundCommand refunds a support payment (owner only; others are
// ignored silently). It works while support is off, so past payments stay
// refundable.
func handleStarsRefundCommand(origMessage *response.TgMessage, origChat *response.TgChat, origUser *response.TgUser) {
	isAllowed := isOwner(origUser.Id)

	if isAllowed == false {
		log.Printf("🚫 Unauthorized %s attempt by user %d", starsRefundCommand, origUser.Id)
	}

	if isAllowed == true {
		text := runStarsRefund(origMessage.Text)
		sendSupportReply(origChat.Id, origMessage.MessageId, text)
	}
}

// runStarsRefund refunds "/starsrefund <user_id> <charge_id>" in Telegram,
// then marks it refunded and removes its unused credits.
func runStarsRefund(commandText string) string {
	userID, chargeID, err := parseStarsRefundArgs(commandText)
	payment := services.SupportPayment{}
	isRecorded := false
	result := ""

	if err == nil {
		payment, isRecorded, err = services.GetSupportPayment(chargeID)
	}

	if err == nil {
		err = checkStarsRefundable(payment, isRecorded, userID)
	}

	if err != nil {
		detail := html.EscapeString(err.Error())
		result = fmt.Sprintf(starsRefundRefusedTemplate, detail, starsRefundUsageText)
	}

	isMarkOnly := isStarsRefundMarkOnly(commandText)

	if (err == nil) && (isMarkOnly == false) {
		result = refundStarsPayment(userID, chargeID, isRecorded)
	}

	// "mark": Telegram already refunded (e.g. a timed-out or half-finished
	// refund), so only the local record and unused credits are updated.
	if (err == nil) && (isMarkOnly == true) && (isRecorded == true) {
		result = markStarsRefunded(userID, chargeID)
	}

	if (err == nil) && (isMarkOnly == true) && (isRecorded == false) {
		result = fmt.Sprintf(starsRefundRefusedTemplate, "no local record of this charge", starsRefundUsageText)
	}

	return result
}

// starsRefundMarkArg makes /starsrefund only record a refund done earlier.
const starsRefundMarkArg = "mark"

// isStarsRefundMarkOnly reports "/starsrefund USER CHARGE mark".
func isStarsRefundMarkOnly(commandText string) bool {
	args := strings.Fields(commandText)
	result := (len(args) == starsRefundMarkWords) && (args[starsRefundMarkIndex] == starsRefundMarkArg)

	return result
}

// parseStarsRefundArgs returns the user id and charge id of the command.
func parseStarsRefundArgs(commandText string) (int64, string, error) {
	userID := int64(0)
	chargeID := ""
	err := fmt.Errorf("expected a user id and a charge id")
	args := strings.Fields(commandText)

	if (len(args) == starsRefundWords) || (isStarsRefundMarkOnly(commandText) == true) {
		userID, err = strconv.ParseInt(args[starsRefundUserIndex], 10, 64)
		chargeID = args[starsRefundChargeIndex]
	}

	if (err == nil) && (userID <= 0) {
		err = fmt.Errorf("invalid user id: %s", args[starsRefundUserIndex])
	}

	return userID, chargeID, err
}

// checkStarsRefundable refuses a recorded charge of another user or one
// already refunded; an unrecorded charge is left to Telegram to check.
func checkStarsRefundable(payment services.SupportPayment, isRecorded bool, userID int64) error {
	var result error

	if (isRecorded == true) && (payment.UserID != userID) {
		result = errStarsRefundWrongUser
	}

	if (isRecorded == true) && (payment.RefundedAt.Valid == true) {
		result = errStarsRefundAlreadyDone
	}

	return result
}

// refundStarsPayment calls refundStarPayment and, once Telegram confirms,
// marks the recorded payment refunded.
func refundStarsPayment(userID int64, chargeID string, isRecorded bool) string {
	refund := message.NewRefundStarPayment(payload.TgPlRefundStarPayment{UserId: userID, TelegramPaymentChargeId: chargeID})
	escapedID := html.EscapeString(chargeID)
	result := fmt.Sprintf(starsRefundNoRecordText, escapedID, userID)

	if (refund.Err == nil) && (isRecorded == true) {
		result = markStarsRefunded(userID, chargeID)
	}

	if refund.Err != nil {
		detail := html.EscapeString(refund.Err.Error())
		result = fmt.Sprintf(starsRefundFailedTemplate, detail)
	}

	log.Printf("⭐ [stars] owner refund of charge %s for user %d: %v", chargeID, userID, refund.Err)

	return result
}

// markStarsRefunded records a confirmed refund and removes unused credits.
func markStarsRefunded(userID int64, chargeID string) string {
	refunded, err := services.MarkSupportPaymentRefunded(userID, chargeID)
	escapedID := html.EscapeString(chargeID)
	result := fmt.Sprintf(starsRefundDoneTemplate, escapedID, userID, refunded.CreditsRemoved, refunded.Balance)

	if err != nil {
		detail := html.EscapeString(err.Error())
		result = fmt.Sprintf(starsRefundMarkFailedText, escapedID, detail)
	}

	return result
}

// supportGatedHint suggests /donate when a bonus credit could have replaced
// the denied gated fetch; empty while support is off.
func supportGatedHint(denial services.GatedFetchDenial, isEnabled bool) string {
	result := ""
	isCapDenial := services.IsCreditDenial(denial)

	if (isEnabled == true) && (isCapDenial == true) {
		result = fmt.Sprintf(supportGatedHintTemplate, donateCommand)
	}

	return result
}

// sendSupportReply sends a silent HTML reply.
func sendSupportReply(chatID int64, replyToMessageID int, text string) {
	reply := payload.TgPlSendMessage{
		ChatId:              chatID,
		ParseMode:           payload.ParseModeHTML,
		Text:                text,
		ReplyToMessageId:    replyToMessageID,
		DisableNotification: true,
	}

	message.NewSendMessage(reply)
}
