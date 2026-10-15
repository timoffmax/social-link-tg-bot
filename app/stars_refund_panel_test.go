package app

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/timoffmax/social-link-bot/services"
)

func starsPanelPayments() []services.SupportPayment {
	paidAt := time.Date(2026, 10, 15, 21, 53, 0, 0, time.UTC)
	result := []services.SupportPayment{
		{ChargeID: "charge-open", UserID: 42, Stars: 25, Credits: 10, CreatedAt: paidAt},
		{ChargeID: "charge-done", UserID: 43, Stars: 50, Credits: 25, CreatedAt: paidAt, RefundedAt: sql.NullTime{Time: paidAt, Valid: true}},
	}

	return result
}

func TestStarsPaymentsListOffersRefundOnlyForUnrefundedPayments(t *testing.T) {
	payments := starsPanelPayments()
	keyboard := starsPaymentsKeyboard(payments)
	text := starsPaymentsText(payments, nil)

	if len(keyboard.InlineKeyboard) != 2 {
		t.Fatalf("expected one refund button and back, got %+v", keyboard.InlineKeyboard)
	}

	refundData := keyboard.InlineKeyboard[0][0].CallbackData

	if (refundData != starsRefundAskCallbackPref+starsPaymentKey("charge-open")) || (len(refundData) > 64) {
		t.Fatalf("unexpected refund callback %q", refundData)
	}

	if (strings.Contains(text, "1. 25 ⭐ from <code>42</code> · 2026-10-15 21:53 UTC") == false) || (strings.Contains(text, starsPaymentRefundedMark) == false) {
		t.Fatalf("unexpected list %q", text)
	}
}

func TestStarsPaymentsListShowsEmptyAndFailedStates(t *testing.T) {
	if text := starsPaymentsText(nil, nil); strings.Contains(text, starsPaymentsEmptyText) == false {
		t.Fatalf("expected the empty text, got %q", text)
	}

	if text := starsPaymentsText(nil, errors.New("db down")); strings.Contains(text, "db down") == false {
		t.Fatalf("expected the error, got %q", text)
	}
}

func TestStarsPaymentsCallbacksAreRecognised(t *testing.T) {
	for _, data := range []string{starsPaymentsCallback, starsRefundAskCallbackPref + "abc", starsRefundRunCallbackPref + "abc"} {
		if isStarsPaymentsCallback(data) == false {
			t.Fatalf("expected %q to be a Stars payments callback", data)
		}
	}

	if isStarsPaymentsCallback("/owner_reports") == true {
		t.Fatalf("other owner callbacks must not match")
	}
}

func TestPanelRefundWithoutAFreshRecordDoesNotCallTelegram(t *testing.T) {
	recorder := startTestBotAPI(t, true)
	payments := starsPanelPayments()

	text := refundFoundStarsPayment(payments[0])

	if (strings.HasPrefix(text, "❌") == false) || (len(recorder.all()) != 0) {
		t.Fatalf("expected a refusal without calling Telegram, got %q %+v", text, recorder.all())
	}
}

func TestPanelRefundIsRefusedWhileTheSameRefundRuns(t *testing.T) {
	recorder := startTestBotAPI(t, true)
	payments := starsPanelPayments()
	runningStarsRefunds.Store(payments[0].ChargeID, true)
	t.Cleanup(func() { runningStarsRefunds.Delete(payments[0].ChargeID) })

	text := refundFoundStarsPayment(payments[0])

	if (text != starsRefundBusyText) || (len(recorder.all()) != 0) {
		t.Fatalf("expected the busy text without calling Telegram, got %q", text)
	}
}
