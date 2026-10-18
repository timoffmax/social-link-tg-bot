package request

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// sendOutcomeToken is the bot token of these tests; no error may show it.
const sendOutcomeToken = "outcome-secret-token"

// sendTestMessage sends a JSON request to the Bot API at template.
func sendTestMessage(t *testing.T, template string) (ResponseInfo, error) {
	t.Setenv("TG_BOT_TOKEN", sendOutcomeToken)
	previous := SetAPIURLTemplate(template)
	t.Cleanup(func() { SetAPIURLTemplate(previous) })
	request := AuthorizedRequest{
		ApiMethod:          "sendMessage",
		RequestMethod:      RequestMethodPOST,
		RequestContentType: ContentTypeJson,
		Payload:            map[string]string{"text": "hi"},
	}
	result := map[string]interface{}{}

	return request.Send(&result)
}

func TestSendThatCanNotBeBuiltIsNotSent(t *testing.T) {
	info, err := sendTestMessage(t, "http://bad host/bot{{token}}/{{method_name}}")

	if (errors.Is(err, ErrNotSent) == false) || (info.StatusCode != 0) {
		t.Fatalf("expected ErrNotSent without an answer, got %v %+v", err, info)
	}

	if strings.Contains(err.Error(), sendOutcomeToken) == true {
		t.Fatalf("the error shows the token: %v", err)
	}
}

func TestSendWithoutAnAnswerIsNotMarkedNotSent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _, hijackErr := w.(http.Hijacker).Hijack()

		if hijackErr == nil {
			conn.Close()
		}
	}))
	defer server.Close()

	info, err := sendTestMessage(t, server.URL+"/bot{{token}}/{{method_name}}")

	if (err == nil) || (errors.Is(err, ErrNotSent) == true) || (info.StatusCode != 0) {
		t.Fatalf("expected a transport error that may have been received, got %v %+v", err, info)
	}
}

func TestSendKeepsTheHTTPStatusAndRetryAfterOfARefusal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 7","parameters":{"retry_after":7}}`))
	}))
	defer server.Close()

	info, err := sendTestMessage(t, server.URL+"/bot{{token}}/{{method_name}}")

	if (err == nil) || (errors.Is(err, ErrNotSent) == true) || (info.HTTPStatus != http.StatusTooManyRequests) || (info.RetryAfter != 7) || (info.Message != "Too Many Requests: retry after 7") {
		t.Fatalf("expected the 429 with retry_after, got %v %+v", err, info)
	}
}

func TestSendWithAnAnswerCutOffBeforeItsEndHasNoAnswer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _, hijackErr := w.(http.Hijacker).Hijack()

		if hijackErr == nil {
			conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 300\r\n\r\n{\"ok\":true,\"res"))
			conn.Close()
		}
	}))
	defer server.Close()

	info, err := sendTestMessage(t, server.URL+"/bot{{token}}/{{method_name}}")

	if (err == nil) || (errors.Is(err, ErrNotSent) == true) || (info.StatusCode != 0) || (info.HTTPStatus != http.StatusOK) || (strings.Contains(err.Error(), sendOutcomeToken) == true) {
		t.Fatalf("expected a cut-off answer to count as no answer, got %v %+v", err, info)
	}
}
