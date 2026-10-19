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

func TestFetchReelVideoNeverAsksTheRelayWithoutADatabase(t *testing.T) {
	fake := startRelayTestServer(t, serveRelayVideo("", relayTestMP4))
	client, _ := newTestRelayClient(t, fake.server.URL)
	sd := &SmartDownloader{instagramRelay: &InstagramRelayExtractor{client: client}}
	cover := newReelCover(t)

	got, denial := sd.FetchReelVideo(reelVideoTestURL, []*MediaInfo{cover}, DownloadRequester{ChatID: 1, UserID: 1, IsPrivate: true})

	if (len(fake.recorded()) != 0) || (len(got) != 1) || (got[0] != cover) || (denial != GatedFetchDeniedNoDB) {
		t.Fatalf("expected no relay call, the cover and no_database, got %d call(s), %+v, %q", len(fake.recorded()), got, denial)
	}

	if strings.HasSuffix(cover.Caption, reelCoverNote) == false {
		t.Errorf("expected the cover note, got %q", cover.Caption)
	}
}

// newReelRelayDownloader returns a downloader whose relay answers with
// handler and whose paid provider is provider.
func newReelRelayDownloader(t *testing.T, handler http.HandlerFunc, provider GatedPostProvider) (*SmartDownloader, *relayTestServer) {
	fake := startRelayTestServer(t, handler)
	client, _ := newTestRelayClient(t, fake.server.URL)
	sd := &SmartDownloader{instagramRelay: &InstagramRelayExtractor{client: client}, gated: provider}

	return sd, fake
}

func TestFetchReelVideoFromTheRelayUsesTheQuotaAndSkipsThePaidProvider(t *testing.T) {
	setupGatedFetchTestDB(t)
	provider := &recordingGatedProvider{}
	sd, fake := newReelRelayDownloader(t, serveRelayVideo("", relayTestMP4), provider)
	cover := newReelCover(t)

	got, denial := sd.FetchReelVideo(reelVideoTestURL, []*MediaInfo{cover}, DownloadRequester{ChatID: 51, UserID: 51, IsPrivate: true})

	if (len(got) != 1) || (got[0].Type != MediaTypeVideo) || (provider.calls != 0) || (len(fake.recorded()) != 1) || (denial != GatedFetchAllowed) {
		t.Fatalf("expected the relay video and no paid call, got %+v, %d paid call(s), %q", got, provider.calls, denial)
	}

	userUsed, globalUsed := scopeAndGlobalUsage(t, GatedFetchScopeUser, 51)

	if (userUsed != 1) || (globalUsed != 1) || (strings.Contains(got[0].Caption, "Special downloads left this month: 2 of 3") == false) {
		t.Fatalf("expected one unit used and noted, got user=%d global=%d, caption %q", userUsed, globalUsed, got[0].Caption)
	}

	if _, err := os.Stat(cover.Filename); os.IsNotExist(err) == false {
		t.Fatalf("expected the cover file removed, stat: %v", err)
	}
}

func TestFetchReelVideoFromTheRelayAloneWorksWithoutAPaidProvider(t *testing.T) {
	setupGatedFetchTestDB(t)
	t.Setenv("APIFY_TOKEN", "")
	t.Setenv("HIKERAPI_TOKEN", "")
	sd, _ := newReelRelayDownloader(t, serveRelayVideo("", relayTestMP4), nil)

	got, _ := sd.FetchReelVideo(reelVideoTestURL, []*MediaInfo{newReelCover(t)}, DownloadRequester{ChatID: 56, UserID: 56, IsPrivate: true})
	userUsed, globalUsed := scopeAndGlobalUsage(t, GatedFetchScopeUser, 56)

	if (len(got) != 1) || (got[0].Type != MediaTypeVideo) || (userUsed != 1) || (globalUsed != 1) {
		t.Fatalf("expected the relay video for one unit, got %+v, user=%d global=%d", got, userUsed, globalUsed)
	}
}

func TestFetchReelVideoFromTheRelayCountsAgainstTheGroupCap(t *testing.T) {
	setupGatedFetchTestDB(t)
	sd, _ := newReelRelayDownloader(t, serveRelayVideo("", relayTestMP4), nil)

	sd.FetchReelVideo(reelVideoTestURL, []*MediaInfo{newReelCover(t)}, DownloadRequester{ChatID: -5200, UserID: 52, IsPrivate: false})

	chatUsed, globalUsed := scopeAndGlobalUsage(t, GatedFetchScopeChat, -5200)

	if (chatUsed != 1) || (globalUsed != 1) {
		t.Fatalf("expected the group and the bot-wide counter used, got chat=%d global=%d", chatUsed, globalUsed)
	}
}

func TestFetchReelVideoTriesThePaidProviderWhenTheRelayFails(t *testing.T) {
	setupGatedFetchTestDB(t)
	provider := &recordingGatedProvider{}
	sd, _ := newReelRelayDownloader(t, serveRelayError(http.StatusUnprocessableEntity, relayCodePrivateVideo), provider)

	got, _ := sd.FetchReelVideo(reelVideoTestURL, []*MediaInfo{newReelCover(t)}, DownloadRequester{ChatID: 53, UserID: 53, IsPrivate: true})
	userUsed, globalUsed := scopeAndGlobalUsage(t, GatedFetchScopeUser, 53)

	if (len(got) != 1) || (got[0].Type != MediaTypeVideo) || (provider.calls != 1) || (userUsed != 1) || (globalUsed != 1) {
		t.Fatalf("expected the paid video for one unit, got %+v, %d call(s), user=%d global=%d", got, provider.calls, userUsed, globalUsed)
	}
}

func TestFetchReelVideoRefundsEveryUnitWhenOnlyTheRelayFailed(t *testing.T) {
	setupGatedFetchTestDB(t)
	sd, fake := newReelRelayDownloader(t, serveRelayError(http.StatusBadGateway, relayCodeYtDlpFailed), nil)
	cover := newReelCover(t)

	got, denial := sd.FetchReelVideo(reelVideoTestURL, []*MediaInfo{cover}, DownloadRequester{ChatID: 57, UserID: 57, IsPrivate: true})
	userUsed, globalUsed := scopeAndGlobalUsage(t, GatedFetchScopeUser, 57)

	if (len(fake.recorded()) != 1) || (len(got) != 1) || (got[0] != cover) || (denial != GatedFetchAllowed) {
		t.Fatalf("expected one relay call and the cover without a refusal, got %d call(s), %+v, %q", len(fake.recorded()), got, denial)
	}

	if (userUsed != 0) || (globalUsed != 0) {
		t.Fatalf("expected every unit refunded, got user=%d global=%d", userUsed, globalUsed)
	}
}

func TestFetchReelVideoKeepsTheGlobalUnitWhenThePaidProviderRanAndFailed(t *testing.T) {
	setupGatedFetchTestDB(t)
	sd, _ := newReelRelayDownloader(t, serveRelayError(http.StatusUnprocessableEntity, relayCodePrivateVideo), failingGatedProvider{})
	cover := newReelCover(t)

	got, _ := sd.FetchReelVideo(reelVideoTestURL, []*MediaInfo{cover}, DownloadRequester{ChatID: 54, UserID: 54, IsPrivate: true})
	userUsed, globalUsed := scopeAndGlobalUsage(t, GatedFetchScopeUser, 54)

	if (len(got) != 1) || (got[0] != cover) || (userUsed != 0) || (globalUsed != 1) {
		t.Fatalf("expected the cover, the user refunded and the bot-wide unit kept, got %+v, user=%d global=%d", got, userUsed, globalUsed)
	}
}

func TestFetchReelVideoRefusalKeepsTheCoverAndReturnsTheDenial(t *testing.T) {
	setupGatedFetchTestDB(t)
	t.Setenv("GATED_FETCH_USER_MONTHLY_ALLOWANCE", "1")
	sd, fake := newReelRelayDownloader(t, serveRelayVideo("", relayTestMP4), nil)
	requester := DownloadRequester{ChatID: 55, UserID: 55, IsPrivate: true}

	first, _ := sd.FetchReelVideo(reelVideoTestURL, []*MediaInfo{newReelCover(t)}, requester)
	cover := newReelCover(t)
	second, denial := sd.FetchReelVideo(reelVideoTestURL, []*MediaInfo{cover}, requester)

	if (first[0].Type != MediaTypeVideo) || (second[0] != cover) || (len(fake.recorded()) != 1) {
		t.Fatalf("expected one relay video, then the cover without a relay call; got %d call(s)", len(fake.recorded()))
	}

	if (denial != GatedFetchDeniedUserCap) || (strings.HasSuffix(cover.Caption, reelCoverNote) == false) {
		t.Fatalf("expected user_allowance and the cover note, got %q, %q", denial, cover.Caption)
	}
}

func TestReelVideoIsNotReservedWhileTheRelayDisabledInstagram(t *testing.T) {
	t.Setenv("APIFY_TOKEN", "")
	t.Setenv("HIKERAPI_TOKEN", "")
	sd, fake := newReelRelayDownloader(t, serveRelayVideo("", relayTestMP4), nil)
	setRelayPlatformDisabled(relayPlatformInstagram, true)
	limit := &recordingLimit{}

	_, err := runCostedSteps(newTestDownload(limit), sd.reelVideoSteps(reelVideoTestURL))

	if (err == nil) || (limit.reserveCount() != 0) || (len(fake.recorded()) != 0) {
		t.Fatalf("expected no reserve and no relay call, got %v, %d reserve(s), %d call(s)", err, limit.reserveCount(), len(fake.recorded()))
	}
}
