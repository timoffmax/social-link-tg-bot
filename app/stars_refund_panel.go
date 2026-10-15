package app

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"log"
	"strings"
	"sync"

	"github.com/timoffmax/social-link-bot/model/api"
	"github.com/timoffmax/social-link-bot/model/api/response"
	"github.com/timoffmax/social-link-bot/services"
)

// Owner panel callbacks of the Stars payments list. A payment is named by a
// short key, since a charge id doesn't fit the 64-byte callback data.
const (
	starsPaymentsCallback      = "/owner_stars"
	starsRefundAskCallbackPref = "/owner_srask_"
	starsRefundRunCallbackPref = "/owner_srrun_"
)

// starsPaymentsButtonText opens the Stars payments list in the owner panel.
const starsPaymentsButtonText = "⭐ Stars Payments"

// starsPaymentsListLimit is the number of payments the list shows.
const starsPaymentsListLimit = 10

// starsPaymentsLookupLimit is how many recent payments a refund button can
// find its payment among.
const starsPaymentsLookupLimit = 100

// starsPaymentKeyLength is the hex length of a payment's callback key.
const starsPaymentKeyLength = 12

// starsPaymentTimeLayout formats payment times in the list.
const starsPaymentTimeLayout = "2006-01-02 15:04 UTC"

// Stars payments list texts.
const (
	starsPaymentsTitle        = "⭐ <b>Stars Payments</b> (newest first)\n\n"
	starsPaymentsEmptyText    = "<i>No payments yet.</i>"
	starsPaymentsFailedText   = "⚠️ Payments unavailable: %s"
	starsPaymentLineTemplate  = "%d. %d ⭐ from <code>%d</code> · %s · +%d bonus%s\n"
	starsPaymentRefundedMark  = " · ↩️ refunded"
	starsRefundButtonTemplate = "↩️ Refund #%d · %d ⭐"
	starsRefundAskTemplate    = "↩️ <b>Refund %d Stars to user <code>%d</code>?</b>\n\nPaid %s. The Stars go back to their Telegram balance, and the unused bonus downloads of this payment (up to %d) are removed."
	starsRefundConfirmText    = "✅ Yes, refund"
	starsRefundNotFoundText   = "⚠️ This payment is no longer in the recent list."
	starsRefundBusyText       = "⏳ This refund is already running."
	starsRefundAlreadyText    = "✅ This payment is already refunded."
	starsPaymentsBackText     = "◀️ Back"
	starsOwnerPanelBackText   = "◀️ Owner Panel"
	starsOwnerPanelCallback   = "/owner"
)

// isStarsPaymentsCallback reports a callback of the Stars payments screens.
func isStarsPaymentsCallback(data string) bool {
	isAsk := strings.HasPrefix(data, starsRefundAskCallbackPref)
	isRun := strings.HasPrefix(data, starsRefundRunCallbackPref)
	result := (data == starsPaymentsCallback) || (isAsk == true) || (isRun == true)

	return result
}

// handleStarsPaymentsCallback shows the list, asks to confirm a refund, or
// runs it. Callers check that the user is the owner.
func handleStarsPaymentsCallback(callbackQuery *response.TgCallbackQuery) {
	chatID := callbackQuery.Message.Chat.Id
	messageID := callbackQuery.Message.MessageId
	data := callbackQuery.Data
	askKey := strings.TrimPrefix(data, starsRefundAskCallbackPref)
	runKey := strings.TrimPrefix(data, starsRefundRunCallbackPref)

	switch {
	case data == starsPaymentsCallback:
		showStarsPayments(chatID, messageID)
	case askKey != data:
		askStarsRefund(chatID, messageID, askKey)
	case runKey != data:
		// Telegram's refund call can be slow: the button is answered first.
		go runStarsRefundFromPanel(chatID, messageID, runKey)
	}
}

// showStarsPayments lists the recent payments with a refund button for each
// one not refunded yet.
func showStarsPayments(chatID int64, messageID int) {
	payments, err := services.ListRecentSupportPayments(starsPaymentsListLimit)
	text := starsPaymentsText(payments, err)
	keyboard := starsPaymentsKeyboard(payments)

	editMessage(chatID, messageID, text, keyboard)
}

// starsPaymentsText renders the payments list.
func starsPaymentsText(payments []services.SupportPayment, err error) string {
	var text strings.Builder
	text.WriteString(starsPaymentsTitle)

	if (err == nil) && (len(payments) == 0) {
		text.WriteString(starsPaymentsEmptyText)
	}

	if err != nil {
		detail := html.EscapeString(err.Error())
		failedLine := fmt.Sprintf(starsPaymentsFailedText, detail)
		text.WriteString(failedLine)
	}

	for index, payment := range payments {
		paidAt := payment.CreatedAt.UTC().Format(starsPaymentTimeLayout)
		refundedMark := ""

		if payment.RefundedAt.Valid == true {
			refundedMark = starsPaymentRefundedMark
		}

		line := fmt.Sprintf(starsPaymentLineTemplate, index+1, payment.Stars, payment.UserID, paidAt, payment.Credits, refundedMark)
		text.WriteString(line)
	}

	result := text.String()

	return result
}

// starsPaymentsKeyboard has a refund button per refundable payment, then back.
func starsPaymentsKeyboard(payments []services.SupportPayment) *api.TgInlineKeyboardMarkup {
	result := &api.TgInlineKeyboardMarkup{}

	for index, payment := range payments {
		if payment.RefundedAt.Valid == false {
			key := starsPaymentKey(payment.ChargeID)
			label := fmt.Sprintf(starsRefundButtonTemplate, index+1, payment.Stars)
			callbackData := fmt.Sprintf("%s%s", starsRefundAskCallbackPref, key)
			button := api.TgInlineKeyboardButton{Text: label, CallbackData: callbackData}
			result.InlineKeyboard = append(result.InlineKeyboard, []api.TgInlineKeyboardButton{button})
		}
	}

	backButton := api.TgInlineKeyboardButton{Text: starsOwnerPanelBackText, CallbackData: starsOwnerPanelCallback}
	result.InlineKeyboard = append(result.InlineKeyboard, []api.TgInlineKeyboardButton{backButton})

	return result
}

// starsPaymentKey is the short callback key of a charge id.
func starsPaymentKey(chargeID string) string {
	sum := sha256.Sum256([]byte(chargeID))
	encoded := hex.EncodeToString(sum[:])
	result := encoded[:starsPaymentKeyLength]

	return result
}

// findStarsPayment finds a recent payment by its callback key.
func findStarsPayment(key string) (services.SupportPayment, bool, error) {
	result := services.SupportPayment{}
	isFound := false
	payments, err := services.ListRecentSupportPayments(starsPaymentsLookupLimit)

	for index := 0; (index < len(payments)) && (isFound == false); index++ {
		payment := payments[index]
		isFound = (starsPaymentKey(payment.ChargeID) == key)
		result = payment
	}

	if isFound == false {
		result = services.SupportPayment{}
	}

	return result, isFound, err
}

// askStarsRefund asks the owner to confirm a refund.
func askStarsRefund(chatID int64, messageID int, key string) {
	payment, isFound, err := findStarsPayment(key)
	text := starsRefundNotFoundText
	backButton := api.TgInlineKeyboardButton{Text: starsPaymentsBackText, CallbackData: starsPaymentsCallback}
	keyboard := &api.TgInlineKeyboardMarkup{InlineKeyboard: [][]api.TgInlineKeyboardButton{{backButton}}}

	isRefundable := (err == nil) && (isFound == true) && (payment.RefundedAt.Valid == false)

	if isRefundable == true {
		paidAt := payment.CreatedAt.UTC().Format(starsPaymentTimeLayout)
		text = fmt.Sprintf(starsRefundAskTemplate, payment.Stars, payment.UserID, paidAt, payment.Credits)
		callbackData := fmt.Sprintf("%s%s", starsRefundRunCallbackPref, key)
		confirmButton := api.TgInlineKeyboardButton{Text: starsRefundConfirmText, CallbackData: callbackData}
		keyboard.InlineKeyboard = [][]api.TgInlineKeyboardButton{{confirmButton, backButton}}
	}

	if (isFound == true) && (payment.RefundedAt.Valid == true) {
		text = starsRefundAlreadyText
	}

	if err != nil {
		detail := html.EscapeString(err.Error())
		text = fmt.Sprintf(starsPaymentsFailedText, detail)
	}

	editMessage(chatID, messageID, text, keyboard)
}

// errStarsRefundNotRecorded refuses a listed payment whose row is gone.
var errStarsRefundNotRecorded = errors.New("this payment is no longer recorded")

// runningStarsRefunds holds the charge ids being refunded, so a double tap on
// "Yes, refund" calls Telegram once.
var runningStarsRefunds sync.Map

// runStarsRefundFromPanel refunds a confirmed payment like /starsrefund does
// and shows the outcome.
func runStarsRefundFromPanel(chatID int64, messageID int, key string) {
	payment, isFound, err := findStarsPayment(key)
	text := starsRefundNotFoundText
	backButton := api.TgInlineKeyboardButton{Text: starsPaymentsBackText, CallbackData: starsPaymentsCallback}
	keyboard := &api.TgInlineKeyboardMarkup{InlineKeyboard: [][]api.TgInlineKeyboardButton{{backButton}}}

	if (err == nil) && (isFound == true) {
		text = refundFoundStarsPayment(payment)
	}

	if err != nil {
		detail := html.EscapeString(err.Error())
		text = fmt.Sprintf(starsPaymentsFailedText, detail)
	}

	editMessage(chatID, messageID, text, keyboard)
}

// refundFoundStarsPayment refunds a listed payment once, refusing one already
// refunded or being refunded.
func refundFoundStarsPayment(payment services.SupportPayment) string {
	_, isRunning := runningStarsRefunds.LoadOrStore(payment.ChargeID, true)
	result := starsRefundBusyText

	if isRunning == false {
		defer runningStarsRefunds.Delete(payment.ChargeID)
		result = refundCheckedStarsPayment(payment)
	}

	log.Printf("⭐ [stars] panel refund of charge %s for user %d (already running: %t)", payment.ChargeID, payment.UserID, isRunning)

	return result
}

// refundCheckedStarsPayment refunds a payment after the same checks as
// /starsrefund, on a fresh read of it: a refund that finished since the list
// was read is refused, not sent to Telegram again.
func refundCheckedStarsPayment(listed services.SupportPayment) string {
	payment, isRecorded, err := services.GetSupportPayment(listed.ChargeID)
	result := ""

	if (err == nil) && (isRecorded == false) {
		err = errStarsRefundNotRecorded
	}

	if err == nil {
		err = checkStarsRefundable(payment, isRecorded, listed.UserID)
	}

	if err == nil {
		result = refundStarsPayment(payment.UserID, payment.ChargeID, true)
	}

	if err != nil {
		detail := html.EscapeString(err.Error())
		result = fmt.Sprintf(starsRefundFailedTemplate, detail)
	}

	return result
}
