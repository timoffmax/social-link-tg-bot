package message

import (
	"encoding/json"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/timoffmax/social-link-bot/model/api/request"
	"github.com/timoffmax/social-link-bot/model/api/request/payload"
)

// capturedCall is one Bot API request seen by the test server.
type capturedCall struct {
	path string
	body map[string]interface{}
}

// startBotAPI serves every method with the given JSON body and records the
// requests.
func startBotAPI(t *testing.T, responseBody string) *[]capturedCall {
	calls := &[]capturedCall{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := ioutil.ReadAll(r.Body)
		body := map[string]interface{}{}
		json.Unmarshal(raw, &body)
		*calls = append(*calls, capturedCall{path: r.URL.Path, body: body})
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(responseBody))
	}))

	t.Setenv("TG_BOT_TOKEN", "test-token")
	previous := request.SetAPIURLTemplate(server.URL + "/bot{{token}}/{{method_name}}")

	t.Cleanup(func() {
		request.SetAPIURLTemplate(previous)
		server.Close()
	})

	return calls
}

func TestSendInvoiceSendsAStarsInvoiceWithoutReplyMarkup(t *testing.T) {
	calls := startBotAPI(t, `{"ok":true,"result":{"message_id":7,"chat":{"id":42}}}`)

	invoice := NewSendInvoice(payload.TgPlSendInvoice{
		ChatId:      42,
		Title:       "Support the bot",
		Description: "Thank you",
		Payload:     "support:25:42",
		Currency:    payload.CurrencyStars,
		Prices:      []payload.TgLabeledPrice{{Label: "Support", Amount: 25}},
	})

	if (invoice.Err != nil) || (invoice.ResponsePayload.MessageId != 7) {
		t.Fatalf("expected the sent invoice, got %+v %v", invoice.ResponsePayload, invoice.Err)
	}

	call := (*calls)[0]
	_, hasMarkup := call.body["reply_markup"]
	prices := call.body["prices"].([]interface{})

	if (call.path != "/bottest-token/sendInvoice") || (call.body["currency"] != "XTR") || (call.body["provider_token"] != "") || (len(prices) != 1) || (hasMarkup == true) {
		t.Fatalf("unexpected invoice request: %s %+v", call.path, call.body)
	}
}

func TestAnswerPreCheckoutQuerySendsOkFalseWithTheReason(t *testing.T) {
	calls := startBotAPI(t, `{"ok":true,"result":true}`)

	answer := NewAnswerPreCheckoutQuery(payload.TgPlAnswerPreCheckoutQuery{PreCheckoutQueryId: "q1", ErrorMessage: "Not available"})

	if (answer.Err != nil) || (*answer.ResponsePayload == false) {
		t.Fatalf("expected a successful answer, got %v", answer.Err)
	}

	call := (*calls)[0]
	ok, hasOk := call.body["ok"]

	if (call.path != "/bottest-token/answerPreCheckoutQuery") || (hasOk == false) || (ok != false) || (call.body["error_message"] != "Not available") {
		t.Fatalf("unexpected answer request: %s %+v", call.path, call.body)
	}
}

func TestRefundStarPaymentReportsTheApiError(t *testing.T) {
	calls := startBotAPI(t, `{"ok":false,"error_code":400,"description":"Bad Request: CHARGE_ALREADY_REFUNDED"}`)

	refund := NewRefundStarPayment(payload.TgPlRefundStarPayment{UserId: 42, TelegramPaymentChargeId: "charge-1"})

	if (refund.Err == nil) || (strings.Contains(refund.Err.Error(), "CHARGE_ALREADY_REFUNDED") == false) {
		t.Fatalf("expected the API error to be kept, got %v", refund.Err)
	}

	call := (*calls)[0]

	if (call.path != "/bottest-token/refundStarPayment") || (call.body["user_id"] != float64(42)) || (call.body["telegram_payment_charge_id"] != "charge-1") {
		t.Fatalf("unexpected refund request: %s %+v", call.path, call.body)
	}
}
