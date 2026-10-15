package request

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	tg_api "github.com/timoffmax/social-link-bot/model/api"
	"github.com/timoffmax/social-link-bot/model/api/request/payload"
)

func TestMultipartUploadSendsInlineButtonsAsJSON(t *testing.T) {
	t.Setenv("TG_BOT_TOKEN", "test-token")
	photoPath := filepath.Join(t.TempDir(), "photo.jpg")

	if err := os.WriteFile(photoPath, []byte("jpeg"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	button := tg_api.TgInlineKeyboardButton{Text: "💚 Support the bot", CallbackData: "/donate"}
	photo := payload.TgPlSendPhoto{
		ChatId:      42,
		Photo:       "photo.jpg",
		FilePath:    photoPath,
		ReplyMarkup: &tg_api.TgInlineKeyboardMarkup{InlineKeyboard: [][]tg_api.TgInlineKeyboardButton{{button}}},
	}
	request, err := prepareRequest(&AuthorizedRequest{ApiMethod: "sendPhoto", RequestMethod: RequestMethodPOST, RequestContentType: ContentTypeFormData, Payload: photo})

	if err != nil {
		t.Fatalf("prepare: %v", err)
	}

	if err := request.ParseMultipartForm(1 << 20); err != nil {
		t.Fatalf("parse: %v", err)
	}

	markup := struct {
		InlineKeyboard [][]map[string]interface{} `json:"inline_keyboard"`
	}{}

	if err := json.Unmarshal([]byte(request.FormValue("reply_markup")), &markup); err != nil {
		t.Fatalf("reply_markup is not JSON: %q (%v)", request.FormValue("reply_markup"), err)
	}

	if _, isSent := request.MultipartForm.Value["caption_entities"]; isSent == true {
		t.Fatalf("unused caption_entities must be left out")
	}

	if (len(markup.InlineKeyboard) != 1) || (len(markup.InlineKeyboard[0]) != 1) || (markup.InlineKeyboard[0][0]["callback_data"] != "/donate") {
		t.Fatalf("unexpected reply_markup: %+v", markup)
	}
}
