package services

// Tests for TikTok HTML parsing. They only use HTML with no downloadable content,
// so parseHTMLForMedia returns before any network download is attempted.

import (
	"testing"
)

// loginWallHTML mirrors what TikTok serves for "audience controls" posts to an
// anonymous client: no video, only the author's avatar and a black placeholder.
const loginWallHTML = `<!DOCTYPE html><html><head><title>TikTok - Make Your Day</title></head><body>
<img src="https://p16-common-sign.tiktokcdn-eu.com/tos-maliva-avt-0068/abc~tplv-tiktokx-cropcenter:720:720.jpeg?x-expires=1&x-signature=a">
<img src="https://p16-common-sign.tiktokcdn-eu.com/tos-maliva-avt-0068/abc~tplv-tiktokx-cropcenter:100:100.jpeg?x-expires=1&x-signature=b">
<img src="https://sf16-sg.tiktokcdn.com/obj/eden-sg/nupwbpxnuhfbv/black-placeholder-bg.png">
</body></html>`

func TestParseHTMLForMediaRejectsLoginWall(t *testing.T) {
	te := NewTikTokExtractor(t.TempDir(), t.TempDir())

	mediaInfos, err := te.parseHTMLForMedia(loginWallHTML, "7692094768695807240", "dvirna")

	if nil == err {
		t.Fatalf("expected an error so the next extraction method runs, got %d media items", len(mediaInfos))
	}

	if 0 != len(mediaInfos) {
		t.Fatalf("expected no media from a login wall, got %d items", len(mediaInfos))
	}
}
