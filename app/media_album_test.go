package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/timoffmax/social-link-bot/model/api/request"
	"github.com/timoffmax/social-link-bot/model/api/request/message"
	"github.com/timoffmax/social-link-bot/model/api/response"
	"github.com/timoffmax/social-link-bot/services"
)

// albumTestItems creates count temp files alternating photo/video.
func albumTestItems(t *testing.T, count int) []*services.MediaInfo {
	dir := t.TempDir()
	items := make([]*services.MediaInfo, 0, count)

	for i := 0; i < count; i++ {
		mediaType := services.MediaTypePhoto
		ext := ".jpg"

		if (i % 2) == 1 {
			mediaType = services.MediaTypeVideo
			ext = ".mp4"
		}

		path := filepath.Join(dir, fmt.Sprintf("item%d%s", i, ext))

		if err := os.WriteFile(path, []byte("x"), 0644); err != nil {
			t.Fatalf("write: %v", err)
		}

		items = append(items, &services.MediaInfo{Type: mediaType, Filename: path})
	}

	return items
}

// recordAlbums replaces the Telegram call; album number failAt fails with err.
func recordAlbums(t *testing.T, failAt int, failErr error) *[][]*services.MediaInfo {
	sent := &[][]*services.MediaInfo{}
	attempts := 0
	previous := albumSender

	albumSender = func(items []*services.MediaInfo, _ *response.TgChat, _ *response.TgMessage, _ string) error {
		var err error

		if attempts == failAt {
			err = failErr
		}

		attempts++

		if err == nil {
			*sent = append(*sent, items)
		}

		return err
	}

	t.Cleanup(func() {
		albumSender = previous
	})

	return sent
}

func sendTestAlbums(items []*services.MediaInfo) []*services.MediaInfo {
	chat := &response.TgChat{Id: 1}
	msg := &response.TgMessage{}

	return sendMixedAlbums(items, chat, msg, "https://www.instagram.com/p/X/", &services.SmartDownloader{})
}

func albumSizes(albums [][]*services.MediaInfo) []int {
	sizes := []int{}

	for _, album := range albums {
		sizes = append(sizes, len(album))
	}

	return sizes
}

func TestSendMixedAlbumsSendsPhotosAndVideosAsOneAlbumInOrder(t *testing.T) {
	sent := recordAlbums(t, -1, nil)
	items := albumTestItems(t, 4)

	rest := sendTestAlbums(items)

	if (len(rest) != 0) || (len(*sent) != 1) || (len((*sent)[0]) != 4) {
		t.Fatalf("expected one album of 4, got %v, %d left", albumSizes(*sent), len(rest))
	}

	for i, item := range (*sent)[0] {
		if item != items[i] {
			t.Fatalf("album order changed at %d", i)
		}
	}
}

func TestSendMixedAlbumsSplitsEvenlyWithoutALoneItem(t *testing.T) {
	cases := map[int]string{11: "[6 5]", 20: "[10 10]", 21: "[7 7 7]", 10: "[10]"}

	for count, expected := range cases {
		sent := recordAlbums(t, -1, nil)
		rest := sendTestAlbums(albumTestItems(t, count))

		if (fmt.Sprint(albumSizes(*sent)) != expected) || (len(rest) != 0) {
			t.Fatalf("%d items: expected %s, got %v with %d left", count, expected, albumSizes(*sent), len(rest))
		}
	}
}

func TestSendMixedAlbumsPutsTheCaptionOnTheFirstAlbumOnly(t *testing.T) {
	sent := recordAlbums(t, -1, nil)
	items := albumTestItems(t, 12)

	for _, item := range items {
		item.Caption = "post caption"
	}

	sendTestAlbums(items)

	if ((*sent)[0][0].Caption != "post caption") || ((*sent)[1][0].Caption != "") {
		t.Fatalf("expected the caption on album 1 only")
	}

	if items[6].Caption != "post caption" {
		t.Fatalf("the original item must not be modified")
	}
}

func TestSendMixedAlbumsReturnsEverythingFromARejectedAlbumOn(t *testing.T) {
	rejected := fmt.Errorf("%w: API error: 400 - bad request", errAlbumRejected)
	sent := recordAlbums(t, 1, rejected)
	items := albumTestItems(t, 14)

	rest := sendTestAlbums(items)

	if (len(*sent) != 1) || (len(rest) != 7) || (rest[0] != items[7]) {
		t.Fatalf("expected the second album's items back, got %v, %d left", albumSizes(*sent), len(rest))
	}
}

func TestSendMixedAlbumsDoesNotResendAnAlbumWithUnknownOutcome(t *testing.T) {
	sent := recordAlbums(t, 0, errors.New("request failed: timeout"))
	items := albumTestItems(t, 14)

	rest := sendTestAlbums(items)

	if (len(rest) != 0) || (len(*sent) != 1) {
		t.Fatalf("expected no resend and the next album sent, got %v, %d left", albumSizes(*sent), len(rest))
	}
}

func TestSendMixedAlbumsLeavesNonAlbumPostsUntouched(t *testing.T) {
	sent := recordAlbums(t, -1, nil)
	withDocument := albumTestItems(t, 3)
	withDocument[1].Type = "document"
	withMKV := albumTestItems(t, 2)
	withMKV[1].Filename = strings.Replace(withMKV[1].Filename, ".mp4", ".mkv", 1)
	withOwnCaption := albumTestItems(t, 2)
	withOwnCaption[0].Caption = "main tweet"
	withOwnCaption[1].Caption = "📄 From quoted tweet"
	withLongCaption := albumTestItems(t, 2)
	withLongCaption[0].Caption = strings.Repeat("a", 1025)
	single := albumTestItems(t, 1)

	for _, items := range [][]*services.MediaInfo{withDocument, withMKV, withOwnCaption, withLongCaption, single} {
		if rest := sendTestAlbums(items); len(rest) != len(items) {
			t.Fatalf("expected all %d items back, got %d", len(items), len(rest))
		}
	}

	if len(*sent) != 0 {
		t.Fatalf("expected no album, got %d", len(*sent))
	}
}

func TestSendMixedAlbumsAcceptsTheLinkOnlyCaptionOfLaterItems(t *testing.T) {
	sent := recordAlbums(t, -1, nil)
	items := albumTestItems(t, 2)
	items[0].Caption = "📷 author\n\n🔗 <a href=\"u\">View</a>"
	items[1].Caption = "🔗 <a href=\"u\">View</a>"

	if rest := sendTestAlbums(items); (len(rest) != 0) || (len(*sent) != 1) {
		t.Fatalf("expected one album, got %v", albumSizes(*sent))
	}
}

func TestAlbumSendErrorTellsRejectionFromUnknownOutcome(t *testing.T) {
	rejected := &message.SendMediaGroup{Err: errors.New("API error: 400 - bad"), Response: request.ResponseInfo{StatusCode: 400}}
	noAnswer := &message.SendMediaGroup{Err: errors.New("request failed: timeout")}
	ok := &message.SendMediaGroup{Response: request.ResponseInfo{StatusCode: 200}}

	if errors.Is(albumSendError(rejected), errAlbumRejected) == false {
		t.Fatalf("an answered error must count as rejected")
	}

	if (albumSendError(noAnswer) == nil) || (errors.Is(albumSendError(noAnswer), errAlbumRejected) == true) {
		t.Fatalf("a failure without an answer must not count as rejected")
	}

	if albumSendError(ok) != nil {
		t.Fatalf("a sent album must not fail")
	}
}

func TestAlbumCaptionStripsTagsWhenSentAsPlainText(t *testing.T) {
	long := `📷 <a href="https://www.instagram.com/a.b/">@a.b</a>` + "\n\n" + strings.Repeat("x &amp; y ", 200)
	caption, parseMode := albumCaption(long, 1, "https://example.com/post")

	if (parseMode != "") || (strings.Contains(caption, "<a href") == true) || (strings.Contains(caption, "@a.b") == false) || (strings.Contains(caption, "x & y") == false) {
		t.Fatalf("expected plain text without tags, got mode=%q caption=%q", parseMode, caption[:80])
	}
}
