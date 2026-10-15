package app

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/timoffmax/social-link-bot/model/api/request"
	"github.com/timoffmax/social-link-bot/model/api/request/payload"
	"github.com/timoffmax/social-link-bot/model/api/response"
	"github.com/timoffmax/social-link-bot/services"
)

// botAPICall is one Bot API request seen by the test server.
type botAPICall struct {
	method string
	body   map[string]interface{}
}

// botAPIRecorder collects the requests sent to the test server.
type botAPIRecorder struct {
	mu    sync.Mutex
	calls []botAPICall
}

func (r *botAPIRecorder) all() []botAPICall {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]botAPICall(nil), r.calls...)
}

// waitForCalls returns the recorded calls once there are at least count of
// them, or what arrived within a second (for handlers that reply in the
// background).
func (r *botAPIRecorder) waitForCalls(count int) []botAPICall {
	deadline := time.Now().Add(time.Second)
	result := r.all()

	for (len(result) < count) && (time.Now().Before(deadline) == true) {
		time.Sleep(5 * time.Millisecond)
		result = r.all()
	}

	return result
}

// startTestBotAPI answers every Bot API method with a generic success and
// records the requests, with Stars support switched as given.
func startTestBotAPI(t *testing.T, isStarsSupportEnabled bool) *botAPIRecorder {
	recorder := &botAPIRecorder{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := ioutil.ReadAll(r.Body)
		body := map[string]interface{}{}
		json.Unmarshal(raw, &body)
		parts := strings.Split(r.URL.Path, "/")
		recorder.mu.Lock()
		method := parts[len(parts)-1]
		recorder.calls = append(recorder.calls, botAPICall{method: method, body: body})
		messageID := len(recorder.calls) + 1000
		recorder.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		answer := `{"ok":true,"result":true}`

		// Sent messages come back with an id, like Telegram's.
		if method == "sendMessage" {
			answer = fmt.Sprintf(`{"ok":true,"result":{"message_id":%d,"date":1}}`, messageID)
		}

		w.Write([]byte(answer))
	}))

	enabled := "false"

	if isStarsSupportEnabled == true {
		enabled = "true"
	}

	t.Setenv("TG_BOT_TOKEN", "test-token")
	t.Setenv("ADMIN_USER_ID", "")
	t.Setenv("STARS_SUPPORT_ENABLED", enabled)
	previous := request.SetAPIURLTemplate(server.URL + "/bot{{token}}/{{method_name}}")

	t.Cleanup(func() {
		request.SetAPIURLTemplate(previous)
		server.Close()
	})

	return recorder
}

func preCheckoutQuery(userID int, currency string, amount int, invoicePayload string) *response.TgPreCheckoutQuery {
	return &response.TgPreCheckoutQuery{
		Id:             "q1",
		From:           response.TgUser{Id: userID},
		Currency:       currency,
		TotalAmount:    amount,
		InvoicePayload: invoicePayload,
	}
}

func TestParseSupportTierCallbackFindsOnlyOfferedTiers(t *testing.T) {
	cases := map[string]int{"/support_25": 10, "/support_50": 25, "/support_100": 60}

	for data, credits := range cases {
		tier, isFound := parseSupportTierCallback(data)

		if (isFound == false) || (tier.Credits != credits) {
			t.Fatalf("%s: expected %d credits, got %+v %t", data, credits, tier, isFound)
		}
	}

	for _, data := range []string{"/support_30", "/support_", "/support_abc", "/support", "/stats_25"} {
		if _, isFound := parseSupportTierCallback(data); isFound == true {
			t.Fatalf("%s: expected no tier", data)
		}
	}
}

func TestSupportTierKeyboardHasOneButtonPerTier(t *testing.T) {
	keyboard := supportTierKeyboard()

	if len(keyboard.InlineKeyboard) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(keyboard.InlineKeyboard))
	}

	if keyboard.InlineKeyboard[0][0].CallbackData != "/support_25" {
		t.Fatalf("unexpected first button: %+v", keyboard.InlineKeyboard[0][0])
	}
}

func TestBuildSupportInvoiceIsAStarsInvoiceWithinTelegramLimits(t *testing.T) {
	tier, _ := services.FindSupportTier(50)

	invoice := buildSupportInvoice(-100, 42, tier)

	isStars := (invoice.Currency == payload.CurrencyStars) && (invoice.ProviderToken == "")
	hasOnePrice := (len(invoice.Prices) == 1) && (invoice.Prices[0].Amount == 50)
	isWithinLimits := (utf8.RuneCountInString(invoice.Title) <= 32) && (utf8.RuneCountInString(invoice.Description) <= 255)

	if (isStars == false) || (hasOnePrice == false) || (isWithinLimits == false) || (invoice.ReplyMarkup != nil) {
		t.Fatalf("unexpected invoice: %+v", invoice)
	}

	if (invoice.Payload != "support:50:42") || (invoice.ChatId != -100) {
		t.Fatalf("unexpected payload or chat: %s %d", invoice.Payload, invoice.ChatId)
	}
}

func TestSupportPreCheckoutRefusal(t *testing.T) {
	cases := []struct {
		name      string
		query     *response.TgPreCheckoutQuery
		isEnabled bool
		want      string
	}{
		{"valid", preCheckoutQuery(42, "XTR", 25, "support:25:42"), true, ""},
		{"disabled", preCheckoutQuery(42, "XTR", 25, "support:25:42"), false, preCheckoutDisabledText},
		{"wrong currency", preCheckoutQuery(42, "USD", 25, "support:25:42"), true, preCheckoutInvalidText},
		{"wrong amount", preCheckoutQuery(42, "XTR", 1, "support:25:42"), true, preCheckoutInvalidText},
		{"unknown tier", preCheckoutQuery(42, "XTR", 30, "support:30:42"), true, preCheckoutInvalidText},
		{"garbage payload", preCheckoutQuery(42, "XTR", 25, "donate"), true, preCheckoutInvalidText},
		{"other user", preCheckoutQuery(7, "XTR", 25, "support:25:42"), true, preCheckoutWrongUserText},
	}

	for _, c := range cases {
		if got := supportPreCheckoutRefusal(c.query, c.isEnabled, true); got != c.want {
			t.Fatalf("%s: expected %q, got %q", c.name, c.want, got)
		}
	}
}

func TestSupportPaymentCreditsRequireAMatchingTier(t *testing.T) {
	paid := &response.TgSuccessfulPayment{Currency: "XTR", TotalAmount: 100, InvoicePayload: "support:100:42"}

	if credits := supportPaymentCredits(paid); credits != 60 {
		t.Fatalf("expected 60 credits, got %d", credits)
	}

	paid.TotalAmount = 50

	if credits := supportPaymentCredits(paid); credits != 0 {
		t.Fatalf("expected no credits for a mismatched amount, got %d", credits)
	}
}

func TestSupportPaymentReplyText(t *testing.T) {
	newRecord := services.SupportPaymentRecord{IsNew: true, Balance: 13}

	if text := supportPaymentReplyText(newRecord, nil, 10); strings.Contains(text, "Bonus downloads left: 13") == false {
		t.Fatalf("expected the balance, got %q", text)
	}

	if text := supportPaymentReplyText(services.SupportPaymentRecord{Balance: 13}, nil, 10); text != "⭐ Thank you! This payment was already counted. Bonus downloads left: 13." {
		t.Fatalf("expected the repeat text, got %q", text)
	}

	if text := supportPaymentReplyText(newRecord, nil, 0); text != supportNoTierText {
		t.Fatalf("expected the no-tier text, got %q", text)
	}

	if text := supportPaymentReplyText(services.SupportPaymentRecord{}, errGatedNoUser, 10); text != supportRecordFailedText {
		t.Fatalf("expected the failure text, got %q", text)
	}
}

func TestSupportGatedHintOnlyForCapDenialsWhenEnabled(t *testing.T) {
	capDenials := []services.GatedFetchDenial{services.GatedFetchDeniedUserCap, services.GatedFetchDeniedChatCap, services.GatedFetchDeniedGlobal}

	for _, denial := range capDenials {
		if hint := supportGatedHint(denial, true); strings.Contains(hint, "/donate") == false {
			t.Fatalf("%v: expected the /donate hint, got %q", denial, hint)
		}

		if hint := supportGatedHint(denial, false); hint != "" {
			t.Fatalf("%v: expected no hint while disabled, got %q", denial, hint)
		}
	}

	if hint := supportGatedHint(services.GatedFetchDeniedChatOff, true); hint != "" {
		t.Fatalf("expected no hint when the chat is off, got %q", hint)
	}
}

func TestInstagramGateReplyTextMentionsSupportOnlyWhenEnabled(t *testing.T) {
	denied := &services.GatedFetchDeniedError{Denial: services.GatedFetchDeniedUserCap}
	gate := services.InstagramGate{AgeLimited: true}

	t.Setenv("STARS_SUPPORT_ENABLED", "false")

	if text := instagramGateReplyText(denied, gate); strings.Contains(text, "/donate") == true {
		t.Fatalf("expected no /donate hint while disabled, got %q", text)
	}

	t.Setenv("STARS_SUPPORT_ENABLED", "true")

	if text := instagramGateReplyText(denied, gate); strings.Contains(text, "/donate") == false {
		t.Fatalf("expected the /donate hint, got %q", text)
	}
}

func TestSupportTierCallbackWhenDisabledSendsNoInvoice(t *testing.T) {
	recorder := startTestBotAPI(t, false)
	callback := &response.TgCallbackQuery{Data: "/support_25", From: response.TgUser{Id: 42}, Message: response.TgMessage{MessageId: 9, Chat: response.TgChat{Id: 42}}}

	handleSupportTierCallback(callback)

	calls := recorder.all()

	if (len(calls) != 1) || (calls[0].method != "sendMessage") || (calls[0].body["text"] != supportUnavailableText) {
		t.Fatalf("expected only the unavailable reply, got %+v", calls)
	}
}

func TestSupportTierCallbackWhenEnabledSendsTheInvoiceWithoutReplyMarkup(t *testing.T) {
	recorder := startTestBotAPI(t, true)
	callback := &response.TgCallbackQuery{Data: "/support_100", From: response.TgUser{Id: 42}, Message: response.TgMessage{MessageId: 9, Chat: response.TgChat{Id: -100}}}

	handleSupportTierCallback(callback)

	calls := recorder.all()
	_, hasMarkup := calls[0].body["reply_markup"]

	if (len(calls) != 1) || (calls[0].method != "sendInvoice") || (calls[0].body["payload"] != "support:100:42") || (calls[0].body["chat_id"] != float64(-100)) || (hasMarkup == true) {
		t.Fatalf("unexpected invoice call: %+v", calls)
	}
}

func TestPreCheckoutQueryWhenDisabledIsRefused(t *testing.T) {
	recorder := startTestBotAPI(t, false)

	handlePreCheckoutQuery(preCheckoutQuery(42, "XTR", 25, "support:25:42"))

	calls := recorder.all()

	if (len(calls) != 1) || (calls[0].method != "answerPreCheckoutQuery") || (calls[0].body["ok"] != false) || (calls[0].body["error_message"] != preCheckoutDisabledText) {
		t.Fatalf("expected an ok=false answer, got %+v", calls)
	}
}

func TestPreCheckoutQueryWhenEnabledIsAccepted(t *testing.T) {
	recorder := startTestBotAPI(t, true)
	assumeSupportDBReady(t)

	handlePreCheckoutQuery(preCheckoutQuery(42, "XTR", 25, "support:25:42"))

	calls := recorder.all()
	_, hasError := calls[0].body["error_message"]

	if (len(calls) != 1) || (calls[0].body["ok"] != true) || (calls[0].body["pre_checkout_query_id"] != "q1") || (hasError == true) {
		t.Fatalf("expected an ok=true answer, got %+v", calls)
	}
}

func TestPaySupportAndTermsAnswerEvenWhenDisabled(t *testing.T) {
	recorder := startTestBotAPI(t, false)

	handlePaySupportCommand(&response.TgMessage{MessageId: 5}, &response.TgChat{Id: 42}, &response.TgUser{Id: 42})
	handleTermsCommand(&response.TgMessage{MessageId: 5}, &response.TgChat{Id: 42}, &response.TgUser{Id: 42})

	if calls := recorder.all(); len(calls) != 2 {
		t.Fatalf("Telegram requires /paysupport to answer after payments, got %+v", calls)
	}
}

// assumeSupportDBReady makes the pre-checkout see a working database and
// usable credits.
func assumeSupportDBReady(t *testing.T) {
	previousUsable := supportCreditsUsable
	supportCreditsUsable = func(int64) bool {
		return true
	}

	t.Cleanup(func() {
		supportCreditsUsable = previousUsable
	})
}

func TestPaySupportWhenEnabledExplainsTelegramSupportCantHelp(t *testing.T) {
	recorder := startTestBotAPI(t, true)

	handlePaySupportCommand(&response.TgMessage{MessageId: 5}, &response.TgChat{Id: 42}, &response.TgUser{Id: 42})

	calls := recorder.all()
	text, _ := calls[0].body["text"].(string)

	if (len(calls) != 1) || (strings.Contains(text, "Telegram support can't help") == false) {
		t.Fatalf("unexpected reply: %+v", calls)
	}
}

func TestStarsRefundIgnoresNonOwners(t *testing.T) {
	recorder := startTestBotAPI(t, true)
	t.Setenv("ADMIN_USER_ID", "1")

	handleStarsRefundCommand(&response.TgMessage{MessageId: 5, Text: "/starsrefund 42 ch1"}, &response.TgChat{Id: 42}, &response.TgUser{Id: 42})

	if calls := recorder.all(); len(calls) != 0 {
		t.Fatalf("expected no calls, got %+v", calls)
	}
}

func TestStarsRefundFailsClosedWithoutDatabase(t *testing.T) {
	recorder := startTestBotAPI(t, true)

	text := runStarsRefund("/starsrefund 42 ch1")

	if (strings.HasPrefix(text, "❌") == false) || (len(recorder.all()) != 0) {
		t.Fatalf("expected a refusal without a refund call, got %q %+v", text, recorder.all())
	}
}

func TestParseStarsRefundArgs(t *testing.T) {
	userID, chargeID, err := parseStarsRefundArgs("/starsrefund 42 ch1")

	if (err != nil) || (userID != 42) || (chargeID != "ch1") {
		t.Fatalf("unexpected parse: %d %q %v", userID, chargeID, err)
	}

	for _, text := range []string{"/starsrefund", "/starsrefund 42", "/starsrefund x ch1", "/starsrefund 0 ch1", "/starsrefund 42 ch1 extra"} {
		if _, _, err := parseStarsRefundArgs(text); err == nil {
			t.Fatalf("%q: expected an error", text)
		}
	}
}

func TestCheckStarsRefundable(t *testing.T) {
	payment := services.SupportPayment{UserID: 42}

	if err := checkStarsRefundable(payment, true, 7); err != errStarsRefundWrongUser {
		t.Fatalf("expected the wrong-user error, got %v", err)
	}

	payment.RefundedAt.Valid = true

	if err := checkStarsRefundable(payment, true, 42); err != errStarsRefundAlreadyDone {
		t.Fatalf("expected the already-refunded error, got %v", err)
	}

	if err := checkStarsRefundable(services.SupportPayment{}, false, 42); err != nil {
		t.Fatalf("expected an unrecorded charge to go to Telegram, got %v", err)
	}
}

func TestProcessUpdateAnswersPreCheckoutQueries(t *testing.T) {
	recorder := startTestBotAPI(t, true)
	assumeSupportDBReady(t)
	update := &response.TgUpdate{UpdateId: 990001, PreCheckoutQuery: *preCheckoutQuery(42, "XTR", 25, "support:25:42")}

	handlePaymentUpdate(update)

	calls := recorder.waitForCalls(1)

	if (len(calls) != 1) || (calls[0].method != "answerPreCheckoutQuery") || (calls[0].body["ok"] != true) {
		t.Fatalf("expected the pre-checkout answer, got %+v", calls)
	}
}

func TestPaymentUpdateHandlesSuccessfulPaymentWithoutDatabase(t *testing.T) {
	recorder := startTestBotAPI(t, true)
	paid := &response.TgSuccessfulPayment{Currency: "XTR", TotalAmount: 25, InvoicePayload: "support:25:42", TelegramPaymentChargeId: "ch1"}
	update := &response.TgUpdate{
		UpdateId: 990002,
		Message:  response.TgMessage{MessageId: 3, From: response.TgUser{Id: 42}, Chat: response.TgChat{Id: 42, Type: "private"}, SuccessfulPayment: paid},
	}

	handlePaymentUpdate(update)

	calls := recorder.waitForCalls(1)

	if (len(calls) != 1) || (calls[0].method != "sendMessage") || (calls[0].body["text"] != supportRecordFailedText) {
		t.Fatalf("expected the record-failed reply, got %+v", calls)
	}
}

func TestPaymentUpdatesAreNeverSkippedAsOld(t *testing.T) {
	paid := &response.TgUpdate{Message: response.TgMessage{Date: 1, SuccessfulPayment: &response.TgSuccessfulPayment{}}}
	plain := &response.TgUpdate{Message: response.TgMessage{Date: 1}}

	if (isPaymentUpdate(paid) == false) || (isPaymentUpdate(plain) == true) {
		t.Fatalf("payment detection is wrong")
	}
}

func TestPreCheckoutIsRefusedWithoutDatabase(t *testing.T) {
	query := &response.TgPreCheckoutQuery{InvoicePayload: "support:25:7", Currency: "XTR", TotalAmount: 25, From: response.TgUser{Id: 7}}

	if supportPreCheckoutRefusal(query, true, false) == "" {
		t.Fatalf("a payment must be refused while the database is unavailable")
	}

	if supportPreCheckoutRefusal(query, true, true) != "" {
		t.Fatalf("a valid payment must be accepted")
	}
}

func TestPreCheckoutIsRefusedWhenCreditsCantBeUsed(t *testing.T) {
	recorder := startTestBotAPI(t, true)
	assumeSupportDBReady(t)
	supportCreditsUsable = func(int64) bool {
		return false
	}

	handlePreCheckoutQuery(preCheckoutQuery(42, "XTR", 25, "support:25:42"))

	calls := recorder.all()

	if (len(calls) != 1) || (calls[0].body["ok"] != false) {
		t.Fatalf("a payment for unusable credits must be declined, got %+v", calls)
	}
}

func TestStarsRefundMarkModeIsRecognised(t *testing.T) {
	if (isStarsRefundMarkOnly("/starsrefund 42 ch1 mark") == false) || (isStarsRefundMarkOnly("/starsrefund 42 ch1") == true) {
		t.Fatalf("mark mode detection is wrong")
	}

	userID, chargeID, err := parseStarsRefundArgs("/starsrefund 42 ch1 mark")

	if (err != nil) || (userID != 42) || (chargeID != "ch1") {
		t.Fatalf("mark mode must parse the user and charge: %d %q %v", userID, chargeID, err)
	}
}

func TestPaymentRelatedUpdatesAreRecognised(t *testing.T) {
	preCheckout := &response.TgUpdate{PreCheckoutQuery: response.TgPreCheckoutQuery{Id: "q1"}}
	plain := &response.TgUpdate{Message: response.TgMessage{Text: "hi"}}

	if (isPaymentRelatedUpdate(preCheckout) == false) || (isPaymentRelatedUpdate(plain) == true) {
		t.Fatalf("payment update detection is wrong")
	}
}
