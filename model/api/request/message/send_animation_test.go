package message

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/timoffmax/social-link-bot/model/api/request"
	"github.com/timoffmax/social-link-bot/model/api/request/payload"
)

func TestSendAnimationUploadsTheFileAsAnimation(t *testing.T) {
	gifPath := filepath.Join(t.TempDir(), "reddit_vx_1_1.gif")

	if err := os.WriteFile(gifPath, []byte("GIF89a-test"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	var method string
	var upload []byte
	var caption string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = filepath.Base(r.URL.Path)
		r.ParseMultipartForm(1 << 20)
		file, _, err := r.FormFile("animation")

		if err == nil {
			upload, _ = io.ReadAll(file)
		}

		caption = r.FormValue("caption")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true,"result":{"message_id":9,"chat":{"id":42}}}`))
	}))
	t.Cleanup(server.Close)
	t.Setenv("TG_BOT_TOKEN", "test-token")
	previous := request.SetAPIURLTemplate(server.URL + "/bot{{token}}/{{method_name}}")
	t.Cleanup(func() { request.SetAPIURLTemplate(previous) })

	sent := NewSendAnimation(payload.TgPlSendAnimation{ChatId: 42, Animation: "animation.gif", Caption: "🟠 r/gifs", FilePath: gifPath})

	if (sent.Err != nil) || (sent.ResponsePayload.MessageId != 9) {
		t.Fatalf("want the sent message, got %+v %v", sent.ResponsePayload, sent.Err)
	}

	if (method != RouteSendAnimation) || (string(upload) != "GIF89a-test") || (caption != "🟠 r/gifs") {
		t.Errorf("method %q, upload %q, caption %q", method, upload, caption)
	}
}
