package app

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/timoffmax/social-link-bot/model/api/request/message"
	"github.com/timoffmax/social-link-bot/model/api/request/payload"
	"github.com/timoffmax/social-link-bot/model/api/response"
	"github.com/timoffmax/social-link-bot/services"
)

// documentRecorder holds what the replaced document and stats calls saw.
type documentRecorder struct {
	payloads []payload.TgPlSendDocument
	stats    []string
}

// recordDocuments replaces the document and stats calls; sendErr is what
// every document send answers.
func recordDocuments(t *testing.T, sendErr error) *documentRecorder {
	recorder := &documentRecorder{}
	previousSender := documentSender
	previousTracker := documentStatsTracker
	documentSender = func(documentPayload payload.TgPlSendDocument) *message.SendDocument {
		recorder.payloads = append(recorder.payloads, documentPayload)

		return &message.SendDocument{ResponsePayload: &response.TgMessage{MessageId: 9}, Err: sendErr}
	}
	documentStatsTracker = func(userID int64, username string, chatID int64, chatTitle string, url string, mediaType string) {
		recorder.stats = append(recorder.stats, mediaType)
	}
	t.Cleanup(func() {
		documentSender = previousSender
		documentStatsTracker = previousTracker
	})

	return recorder
}

func writeAppTestFile(t *testing.T, name string, content []byte) string {
	path := filepath.Join(t.TempDir(), name)

	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	return path
}

func TestSendAsDocumentFallbackNamesTheFileByItsContent(t *testing.T) {
	chat, origMessage := testChatAndMessage()
	cases := []struct {
		name      string
		content   []byte
		mediaType string
		file      string
		caption   string
	}{
		{name: "1_ABC_9gag.jpg", content: []byte("GIF89a....."), mediaType: services.MediaTypePhoto, file: "1_ABC_9gag.gif", caption: "🎞️ GIF (sent as a file)"},
		{name: "reddit_vx_1_1.jpg", content: []byte("\xff\xd8\xff\xe0\x00\x10JFIF"), mediaType: services.MediaTypePhoto, file: "reddit_vx_1_1.jpg", caption: "🖼️ Photo (sent as a file)"},
		{name: "clip_x1.mp4", content: []byte("\x00\x00\x00\x18ftypmp42\x00\x00\x00\x00mp42isom"), mediaType: services.MediaTypeVideo, file: "clip_x1.mp4", caption: "📹 Video (sent as a file)"},
	}

	for _, testCase := range cases {
		recorder := recordDocuments(t, nil)
		path := writeAppTestFile(t, testCase.name, testCase.content)
		item := &services.MediaInfo{Type: testCase.mediaType, Filename: path, Caption: "<b>post caption</b>"}

		err := sendAsDocumentFallback(item, chat, origMessage, "https://9gag.com/gag/x")

		if (err != nil) || (len(recorder.payloads) != 1) {
			t.Fatalf("%s: want 1 document, got %d %v", testCase.name, len(recorder.payloads), err)
		}

		sent := recorder.payloads[0]

		if (filepath.Base(sent.FilePath) != testCase.file) || (sent.Document != testCase.file) || (item.Filename != sent.FilePath) {
			t.Errorf("%s: sent %s as %s, item %s", testCase.name, sent.FilePath, sent.Document, item.Filename)
		}

		if (sent.Caption != testCase.caption) || (sent.ParseMode != "") {
			t.Errorf("%s: caption %q (%q)", testCase.name, sent.Caption, sent.ParseMode)
		}

		if (len(recorder.stats) != 1) || (recorder.stats[0] != documentStatsType) {
			t.Errorf("%s: stats %v", testCase.name, recorder.stats)
		}
	}
}

func TestSendAsDocumentFallbackCountsNothingWhenTelegramRefuses(t *testing.T) {
	chat, origMessage := testChatAndMessage()
	recorder := recordDocuments(t, errors.New("API error: 413 - Request Entity Too Large"))
	path := writeAppTestFile(t, "clip_x1.mp4", []byte("video"))
	item := &services.MediaInfo{Type: services.MediaTypeVideo, Filename: path}

	err := sendAsDocumentFallback(item, chat, origMessage, "https://9gag.com/gag/x")

	if (err == nil) || (len(recorder.payloads) != 1) {
		t.Fatalf("want the refusal back, got %v (%d sends)", err, len(recorder.payloads))
	}

	if len(recorder.stats) != 0 {
		t.Errorf("a refused document must not count, got %v", recorder.stats)
	}
}

func TestSendMediaFileSendsADocumentItemWithThePostCaption(t *testing.T) {
	startTestBotAPI(t, false)
	chat, origMessage := testChatAndMessage()
	recorder := recordDocuments(t, nil)
	path := writeAppTestFile(t, "1_ABC_9gag.jpg", []byte("\xff\xd8\xff\xe0\x00\x10JFIF"))
	item := &services.MediaInfo{Type: services.MediaTypeDocument, URL: "https://9gag.com/gag/x", Filename: path, Caption: "😂 Tall post"}

	err := sendMediaFile(item, chat, origMessage, item.URL, true)

	if (err != nil) || (len(recorder.payloads) != 1) {
		t.Fatalf("want 1 document, got %d %v", len(recorder.payloads), err)
	}

	sent := recorder.payloads[0]

	if (sent.Caption != "😂 Tall post") || (sent.ParseMode != payload.ParseModeHTML) || (sent.Document != "1_ABC_9gag.jpg") || (sent.ReplyToMessageId != 7) {
		t.Errorf("sent %+v", sent)
	}
}

func TestSendMediaFileReportsARefusedDocument(t *testing.T) {
	startTestBotAPI(t, false)
	chat, origMessage := testChatAndMessage()
	recordDocuments(t, errors.New("API error: 400 - Bad Request"))
	path := writeAppTestFile(t, "strip.png", []byte("\x89PNG\r\n\x1a\n...."))
	item := &services.MediaInfo{Type: services.MediaTypeDocument, Filename: path}

	if err := sendMediaFile(item, chat, origMessage, "https://9gag.com/gag/x", true); err == nil {
		t.Error("want the refusal back")
	}
}
