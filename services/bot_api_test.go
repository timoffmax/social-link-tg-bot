package services

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

const botAPITestToken = "123456:secret-test-token"

func useBotAPIServer(t *testing.T, handler http.HandlerFunc) {
	server := httptest.NewServer(handler)
	previousTemplate := botAPIURLTemplate
	botAPIURLTemplate = server.URL + "/bot%s/%s"
	t.Setenv("TG_BOT_TOKEN", botAPITestToken)
	t.Cleanup(func() {
		botAPIURLTemplate = previousTemplate
		server.Close()
	})
}

func TestPostBotAPIReportsTelegramDescription(t *testing.T) {
	useBotAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"ok":false,"description":"Bad Request: description is too long"}`))
	})

	err := postBotAPI("setMyDescription", map[string]interface{}{"description": "x"})

	if (err == nil) || (strings.Contains(err.Error(), "description is too long") == false) {
		t.Fatalf("expected Telegram's description in the error, got %v", err)
	}
}

func TestPostBotAPITransportErrorHidesToken(t *testing.T) {
	previousTemplate := botAPIURLTemplate
	botAPIURLTemplate = "http://127.0.0.1:1/bot%s/%s"
	t.Setenv("TG_BOT_TOKEN", botAPITestToken)
	t.Cleanup(func() { botAPIURLTemplate = previousTemplate })

	err := postBotAPI("setMyDescription", map[string]interface{}{"description": "x"})

	if err == nil {
		t.Fatalf("expected a transport error")
	}

	if strings.Contains(err.Error(), botAPITestToken) == true {
		t.Fatalf("error leaks the bot token: %v", err)
	}
}

func TestPushBotProfileSetsShortDescriptionWhenDescriptionFails(t *testing.T) {
	var mutex sync.Mutex
	methods := []string{}
	useBotAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
		mutex.Lock()
		methods = append(methods, r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:])
		mutex.Unlock()

		if strings.HasSuffix(r.URL.Path, "/setMyDescription") == true {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"ok":false,"description":"Too Many Requests: retry after 5"}`))
		}
	})

	known, _, err := PushBotProfile(map[string]string{}, RenderBotProfile())

	if (err == nil) || (strings.Contains(err.Error(), "Too Many Requests") == false) {
		t.Fatalf("expected the setMyDescription failure, got %v", err)
	}

	if strings.Join(methods, ",") != "setMyCommands,setMyDescription,setMyShortDescription" {
		t.Fatalf("expected every method to be called, got %v", methods)
	}

	if (known[ProfileMethodDescription] != "") || (known[ProfileMethodShortDescription] == "") || (known[ProfileMethodCommands] == "") {
		t.Fatalf("only the failed part must stay unknown, got %v", known)
	}
}
