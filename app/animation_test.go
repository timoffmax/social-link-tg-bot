package app

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/timoffmax/social-link-bot/model/api/request/message"
	"github.com/timoffmax/social-link-bot/model/api/request/payload"
	"github.com/timoffmax/social-link-bot/model/api/response"
	"github.com/timoffmax/social-link-bot/services"
)

func testChatAndMessage() (*response.TgChat, *response.TgMessage) {
	chat := &response.TgChat{Id: 42, Type: chatTypePrivate}
	origMessage := &response.TgMessage{MessageId: 7, From: response.TgUser{Id: 5}}

	return chat, origMessage
}

func TestSendLeadingTextSendsTheTextThenReturnsTheMedia(t *testing.T) {
	recorder := startTestBotAPI(t, false)
	chat, origMessage := testChatAndMessage()
	photo := &services.MediaInfo{Type: services.MediaTypePhoto, Filename: "a.jpg"}
	items := []*services.MediaInfo{{Type: services.MediaTypeText, Caption: "😂 Title\n\nBody"}, photo}

	rest := sendLeadingText(items, chat, origMessage, "https://9gag.com/gag/advy56B")

	if (len(rest) != 1) || (rest[0] != photo) {
		t.Fatalf("want the photo back, got %v", rest)
	}

	calls := recorder.all()

	if (len(calls) != 1) || (calls[0].method != "sendMessage") {
		t.Fatalf("want 1 sendMessage, got %+v", calls)
	}

	text, _ := calls[0].body["text"].(string)

	if (strings.HasPrefix(text, "😂 Title\n\nBody\n\n🔗 <a href=\"https://9gag.com/gag/advy56B\">") == false) || (calls[0].body["parse_mode"] != payload.ParseModeHTML) || (calls[0].body["disable_web_page_preview"] != true) {
		t.Errorf("sent %+v", calls[0].body)
	}
}

func TestSendLeadingTextLeavesOtherPostsAlone(t *testing.T) {
	recorder := startTestBotAPI(t, false)
	chat, origMessage := testChatAndMessage()
	photos := []*services.MediaInfo{{Type: services.MediaTypePhoto, Filename: "a.jpg"}, {Type: services.MediaTypePhoto, Filename: "b.jpg"}}
	single := []*services.MediaInfo{{Type: services.MediaTypeText, Caption: "text only"}}

	for _, items := range [][]*services.MediaInfo{photos, single} {
		rest := sendLeadingText(items, chat, origMessage, "https://9gag.com/gag/x")

		if len(rest) != len(items) {
			t.Errorf("got %d item(s) back from %d", len(rest), len(items))
		}
	}

	if len(recorder.all()) != 0 {
		t.Errorf("nothing must be sent, got %+v", recorder.all())
	}
}

func TestSendMediaFileSendsAnAnimationWithItsCaption(t *testing.T) {
	startTestBotAPI(t, false)
	chat, origMessage := testChatAndMessage()
	gifPath := filepath.Join(t.TempDir(), "reddit_vx_1_1.gif")

	if err := os.WriteFile(gifPath, []byte("GIF89a"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	var sentPayload payload.TgPlSendAnimation
	previous := animationSender
	animationSender = func(animationPayload payload.TgPlSendAnimation) *message.SendAnimation {
		sentPayload = animationPayload

		return &message.SendAnimation{ResponsePayload: &response.TgMessage{MessageId: 9}}
	}
	t.Cleanup(func() { animationSender = previous })
	item := &services.MediaInfo{Type: services.MediaTypeAnimation, URL: "https://www.reddit.com/r/gifs/comments/1x8d6k4/siblings/", Caption: "🟠 r/gifs\n\nSiblings", Filename: gifPath}

	err := sendMediaFile(item, chat, origMessage, item.URL, true)

	if err != nil {
		t.Fatalf("send: %v", err)
	}

	if (sentPayload.FilePath != gifPath) || (sentPayload.Caption != item.Caption) || (sentPayload.ParseMode != payload.ParseModeHTML) || (sentPayload.ReplyToMessageId != 7) {
		t.Errorf("sent %+v", sentPayload)
	}
}

// recordPostOrder replaces the album and animation calls and records what
// goes out, in order, as "album:<items>:<lead caption>" or
// "animation:<caption>".
func recordPostOrder(t *testing.T) *[]string {
	sent := &[]string{}
	previousAlbum := albumSender
	previousAnimation := animationSender
	albumSender = func(items []*services.MediaInfo, origChat *response.TgChat, origMessage *response.TgMessage, url string) error {
		*sent = append(*sent, fmt.Sprintf("album:%d:%s", len(items), items[0].Caption))

		return nil
	}
	animationSender = func(animationPayload payload.TgPlSendAnimation) *message.SendAnimation {
		*sent = append(*sent, fmt.Sprintf("animation:%s", animationPayload.Caption))

		return &message.SendAnimation{ResponsePayload: &response.TgMessage{MessageId: 9}}
	}
	t.Cleanup(func() {
		albumSender = previousAlbum
		animationSender = previousAnimation
	})

	return sent
}

// postTestItems creates one temp file per type ("photo" or "animation"),
// the first one captioned with the title.
func postTestItems(t *testing.T, types ...string) []*services.MediaInfo {
	tempDir := t.TempDir()
	items := []*services.MediaInfo{}

	for i, mediaType := range types {
		path := filepath.Join(tempDir, fmt.Sprintf("item_%d", i))
		os.WriteFile(path, []byte("x"), 0600)
		caption := "🔗 link"

		if i == 0 {
			caption = "TITLE\n\n🔗 link"
		}

		items = append(items, &services.MediaInfo{Type: mediaType, Caption: caption, Filename: path})
	}

	return items
}

func TestAnimatedPostGoesOutInPostOrder(t *testing.T) {
	startTestBotAPI(t, false)
	chat, origMessage := testChatAndMessage()
	sent := recordPostOrder(t)
	photo := services.MediaTypePhoto
	animation := services.MediaTypeAnimation
	cases := []struct {
		types []string
		want  []string
	}{
		{types: []string{animation, photo, photo}, want: []string{"animation:TITLE\n\n🔗 link", "album:2:🔗 link"}},
		{types: []string{photo, photo, animation, photo, photo}, want: []string{"album:2:TITLE\n\n🔗 link", "animation:🔗 link", "album:2:🔗 link"}},
		{types: []string{photo, photo, animation, animation}, want: []string{"album:2:TITLE\n\n🔗 link", "animation:🔗 link", "animation:🔗 link"}},
	}

	for _, testCase := range cases {
		*sent = nil
		items := postTestItems(t, testCase.types...)

		rest := sendAnimatedPostInOrder(items, chat, origMessage, "https://www.reddit.com/r/x/comments/1/", &services.SmartDownloader{})

		if (len(rest) != 0) || (slices.Equal(*sent, testCase.want) == false) {
			t.Errorf("%v: sent %q, %d left, want %q", testCase.types, *sent, len(rest), testCase.want)
		}
	}
}

func TestPostWithoutAnAnimationIsLeftToTheUsualSending(t *testing.T) {
	chat, origMessage := testChatAndMessage()
	sent := recordPostOrder(t)
	photos := postTestItems(t, services.MediaTypePhoto, services.MediaTypePhoto)
	single := postTestItems(t, services.MediaTypeAnimation)

	for _, items := range [][]*services.MediaInfo{photos, single} {
		rest := sendAnimatedPostInOrder(items, chat, origMessage, "https://www.reddit.com/r/x/comments/1/", &services.SmartDownloader{})

		if len(rest) != len(items) {
			t.Errorf("got %d item(s) back from %d", len(rest), len(items))
		}
	}

	if len(*sent) != 0 {
		t.Errorf("nothing must be sent, got %q", *sent)
	}
}

func TestSendAnimationFileUploadsAGIFUnderAGIFName(t *testing.T) {
	startTestBotAPI(t, false)
	chat, origMessage := testChatAndMessage()
	gifPath := filepath.Join(t.TempDir(), "1_ABC_9gag.jpg")

	if err := os.WriteFile(gifPath, []byte("GIF89a....."), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	var sentPayload payload.TgPlSendAnimation
	previous := animationSender
	animationSender = func(animationPayload payload.TgPlSendAnimation) *message.SendAnimation {
		sentPayload = animationPayload

		return &message.SendAnimation{ResponsePayload: &response.TgMessage{MessageId: 9}}
	}
	t.Cleanup(func() { animationSender = previous })
	item := &services.MediaInfo{Type: services.MediaTypeAnimation, Filename: gifPath}

	err := sendMediaFile(item, chat, origMessage, "https://9gag.com/gag/x", true)

	if (err != nil) || (filepath.Base(sentPayload.FilePath) != "1_ABC_9gag.gif") || (sentPayload.Animation != "1_ABC_9gag.gif") || (item.Filename != sentPayload.FilePath) {
		t.Errorf("sent %s as %s: %v", sentPayload.FilePath, sentPayload.Animation, err)
	}
}

func TestSendAnimationFileKeepsAnMP4Name(t *testing.T) {
	startTestBotAPI(t, false)
	chat, origMessage := testChatAndMessage()
	mp4Path := filepath.Join(t.TempDir(), "reddit_vx_1_1_gif.mp4")

	if err := os.WriteFile(mp4Path, []byte("\x00\x00\x00\x18ftypmp42\x00\x00\x00\x00mp42isom"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	var sentPayload payload.TgPlSendAnimation
	previous := animationSender
	animationSender = func(animationPayload payload.TgPlSendAnimation) *message.SendAnimation {
		sentPayload = animationPayload

		return &message.SendAnimation{ResponsePayload: &response.TgMessage{MessageId: 9}}
	}
	t.Cleanup(func() { animationSender = previous })
	item := &services.MediaInfo{Type: services.MediaTypeAnimation, Filename: mp4Path}

	err := sendMediaFile(item, chat, origMessage, "https://9gag.com/gag/x", true)

	if (err != nil) || (sentPayload.FilePath != mp4Path) || (sentPayload.Animation != "reddit_vx_1_1_gif.mp4") {
		t.Errorf("sent %s as %s: %v", sentPayload.FilePath, sentPayload.Animation, err)
	}
}
