package services

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestInstagramRelayExtractorSendsTheShortcodeAndBuildsTheCaption(t *testing.T) {
	meta := encodeRelayMeta(t, map[string]any{"title": "Video by nattyfarmsmy", "uploader": "Natty & Farms", "channel": "nattyfarmsmy", "description": "Ep <7>", "height": nil, "width": nil, "duration": nil})
	fake := startRelayTestServer(t, serveRelayVideo(meta, relayTestMP4))
	client, _ := newTestRelayClient(t, fake.server.URL)
	extractor := &InstagramRelayExtractor{client: client}

	media, err := extractor.ExtractMedia(context.Background(), reelVideoTestURL+"?igsh=abc")

	if (err != nil) || (len(media) != 1) || (media[0].Type != MediaTypeVideo) {
		t.Fatalf("got %+v, %v", media, err)
	}

	var sent relayRequest
	calls := fake.recorded()

	if (len(calls) != 1) || (json.Unmarshal(calls[0].body, &sent) != nil) {
		t.Fatalf("expected one readable request, got %d", len(calls))
	}

	if (sent.Platform != relayPlatformInstagram) || (sent.ID != "DbcgVdQziXu") {
		t.Errorf("request %+v", sent)
	}

	for _, want := range []string{"Natty &amp; Farms", "instagram.com/nattyfarmsmy", "Ep &lt;7&gt;", "the original on Instagram"} {
		if strings.Contains(media[0].Caption, want) == false {
			t.Errorf("caption %q lacks %q", media[0].Caption, want)
		}
	}
}

func TestInstagramRelayCaptionLeavesOutAnUnsafeUserName(t *testing.T) {
	caption := instagramRelayCaption(relayMeta{Uploader: "Name", Channel: `x"><b>`}, reelVideoTestURL)

	if (strings.Contains(caption, "<b>") == true) || (strings.Contains(caption, "@") == true) {
		t.Errorf("caption %q", caption)
	}
}

func TestInstagramRelayExtractorExistsOnlyWhenTheRelayListsInstagram(t *testing.T) {
	tokenFile := writeRelayTestToken(t)
	cases := map[string]bool{"youtube": false, "youtube,instagram": true, "Instagram": true, "": false}

	for platforms, want := range cases {
		t.Setenv("RELAY_URL", "http://127.0.0.1:8791")
		t.Setenv("RELAY_TOKEN_FILE", tokenFile)
		t.Setenv("RELAY_PLATFORMS", platforms)

		extractor := NewInstagramRelayExtractor(t.TempDir())

		if (extractor != nil) != want {
			t.Errorf("RELAY_PLATFORMS=%q: extractor %v, want one: %t", platforms, extractor, want)
		}
	}
}

func TestFetchReelVideoPrefersTheRelayOverThePaidProvider(t *testing.T) {
	fake := startRelayTestServer(t, serveRelayVideo("", relayTestMP4))
	client, _ := newTestRelayClient(t, fake.server.URL)
	provider := &recordingGatedProvider{}
	sd := &SmartDownloader{instagramRelay: &InstagramRelayExtractor{client: client}, gated: provider}
	cover := newReelCover(t)

	got := sd.FetchReelVideo(reelVideoTestURL, []*MediaInfo{cover}, 1, 1, true)

	if (len(got) != 1) || (got[0].Type != MediaTypeVideo) || (provider.calls != 0) {
		t.Fatalf("expected the relay video and no paid call, got %+v, %d call(s)", got, provider.calls)
	}

	if _, err := os.Stat(cover.Filename); os.IsNotExist(err) == false {
		t.Errorf("expected the cover file removed, stat: %v", err)
	}
}

func TestFetchReelVideoFallsBackWhenTheRelayFails(t *testing.T) {
	fake := startRelayTestServer(t, serveRelayError(http.StatusUnprocessableEntity, relayCodePrivateVideo))
	client, _ := newTestRelayClient(t, fake.server.URL)
	sd := &SmartDownloader{instagramRelay: &InstagramRelayExtractor{client: client}}
	cover := newReelCover(t)

	got := sd.FetchReelVideo(reelVideoTestURL, []*MediaInfo{cover}, 1, 1, true)

	if (len(fake.recorded()) != 1) || (len(got) != 1) || (got[0] != cover) {
		t.Fatalf("expected one relay call and the cover back, got %d call(s), %+v", len(fake.recorded()), got)
	}

	if strings.HasSuffix(cover.Caption, reelCoverNote) == false {
		t.Errorf("expected the cover note, got %q", cover.Caption)
	}
}
