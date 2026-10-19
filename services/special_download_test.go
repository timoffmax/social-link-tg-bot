package services

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

var errSpecialTestStep = errors.New("step failed")

// failingStep is a costed step that runs and fails.
func failingStep(kind costKind, err error) costedStep {
	return costedStep{kind: kind, canRun: true, run: func() ([]*MediaInfo, error) { return nil, err }}
}

// winningStep is a costed step that returns one video.
func winningStep(kind costKind) costedStep {
	return costedStep{kind: kind, canRun: true, run: func() ([]*MediaInfo, error) {
		return []*MediaInfo{{Type: MediaTypeVideo, Caption: "🎬 author"}}, nil
	}}
}

func newTestDownload(limit specialLimit) *specialDownload {
	return newSpecialDownload("test", DownloadRequester{ChatID: 1, UserID: 1, IsPrivate: true}, limit)
}

func TestSpecialDownloadReservesOnceAcrossCostedAttempts(t *testing.T) {
	limit := &recordingLimit{}
	download := newTestDownload(limit)

	media, err := runCostedSteps(download, []costedStep{failingStep(costRelay, errSpecialTestStep), winningStep(costPaid)})

	if (err != nil) || (len(media) != 1) || (limit.reserveCount() != 1) || (len(limit.refundCalls()) != 0) {
		t.Fatalf("expected one reserve and no refund, got %v, %d reserve(s), refunds %v", err, limit.reserveCount(), limit.refundCalls())
	}
}

func TestSpecialDownloadKeepsTheUnitsAndAddsTheNoteOnACostedWin(t *testing.T) {
	limit := &recordingLimit{reservation: specialReservation{usage: GatedFetchUsage{Used: 1, Limit: 3, IsLimited: true}}}
	download := newTestDownload(limit)

	media, _ := runCostedSteps(download, []costedStep{winningStep(costRelay)})

	if (len(limit.refundCalls()) != 0) || (strings.HasSuffix(media[0].Caption, "Special downloads left this month: 2 of 3") == false) {
		t.Fatalf("expected the units kept and the note, got refunds %v, caption %q", limit.refundCalls(), media[0].Caption)
	}
}

func TestSpecialDownloadRefundsTheGlobalUnitWhenOnlyTheRelayRan(t *testing.T) {
	limit := &recordingLimit{}

	runCostedSteps(newTestDownload(limit), []costedStep{failingStep(costRelay, errSpecialTestStep)})

	if slices.Equal(limit.refundCalls(), []bool{false}) == false {
		t.Fatalf("expected one full refund, got %v", limit.refundCalls())
	}
}

func TestSpecialDownloadKeepsTheGlobalUnitWhenAPaidMethodRan(t *testing.T) {
	limit := &recordingLimit{}

	runCostedSteps(newTestDownload(limit), []costedStep{failingStep(costRelay, errSpecialTestStep), failingStep(costPaid, errSpecialTestStep)})

	if slices.Equal(limit.refundCalls(), []bool{true}) == false {
		t.Fatalf("expected a refund keeping the bot-wide unit, got %v", limit.refundCalls())
	}
}

// costedNotStartedErrors returns the real pre-spend refusals of the costed
// methods.
func costedNotStartedErrors(t *testing.T) map[string]error {
	runs := 0
	input := ""
	apify := newFakeApify(t, `[]`, &runs, &input)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, noTimeErr := fakeApifyClient(apify).runSync(ctx, "a~b", nil, time.Minute, 0.01)
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(broken.Close)
	_, budgetCheckErr := fakeApifyClient(broken).runSync(context.Background(), "a~b", nil, time.Minute, 0.01)
	shortsErr := checkYouTubeVideoURL("https://www.youtube.com/watch?v=aqz-KE-bpKQ")
	_, _, highlightsErr := parseApifyStoryURL("https://www.instagram.com/stories/highlights/123/")

	if (errors.Is(noTimeErr, context.DeadlineExceeded) == false) || (runs != 0) {
		t.Fatalf("expected the no-time refusal to keep DeadlineExceeded without a run, got %v, %d run(s)", noTimeErr, runs)
	}

	return map[string]error{"no time left": noTimeErr, "budget check failed": budgetCheckErr, "not a Short": shortsErr, "highlights": highlightsErr}
}

func TestSpecialDownloadRefundsEverythingOnAPaidSkip(t *testing.T) {
	skips := map[string]error{
		"apify not configured": errApifyNotConfigured,
		"budget":               fmt.Errorf("youtube: %w", errApifyBudget),
		"hiker unavailable":    errHikerUnavailable,
	}

	for name, err := range costedNotStartedErrors(t) {
		if errors.Is(err, errCostedNotStarted) == false {
			t.Fatalf("%s: expected errCostedNotStarted, got %v", name, err)
		}

		skips[name] = err
	}

	for name, err := range skips {
		limit := &recordingLimit{}

		runCostedSteps(newTestDownload(limit), []costedStep{failingStep(costPaid, err)})

		if slices.Equal(limit.refundCalls(), []bool{false}) == false {
			t.Errorf("%s: expected a full refund, got %v", name, limit.refundCalls())
		}
	}
}

func TestSpecialDownloadReportsABudgetPauseAsNoRefusal(t *testing.T) {
	download := newTestDownload(&recordingLimit{})

	runCostedSteps(download, []costedStep{failingStep(costPaid, errApifyBudget)})

	if (download.Denial() != GatedFetchDeniedBudget) || (IsSpecialDownloadRefusal(download.Denial()) == true) {
		t.Fatalf("expected a budget pause that is no refusal, got %q", download.Denial())
	}
}

func TestSpecialDownloadSettlesOnlyOnce(t *testing.T) {
	limit := &recordingLimit{reservation: specialReservation{usage: GatedFetchUsage{Used: 1, Limit: 3, IsLimited: true}}}
	download := newTestDownload(limit)
	download.reserve()
	media := []*MediaInfo{{Type: MediaTypeVideo, Caption: "🎬"}}

	download.settle(media, true)
	download.settle(media, true)
	download.settle(nil, false)

	if (strings.Count(media[0].Caption, "Special downloads left") != 1) || (len(limit.refundCalls()) != 0) {
		t.Fatalf("expected one note and no refund, got %q, refunds %v", media[0].Caption, limit.refundCalls())
	}
}

func TestSpecialDownloadRefundsWhenAFreeResultWins(t *testing.T) {
	limit := &recordingLimit{}
	download := newTestDownload(limit)
	download.reserve()
	media := []*MediaInfo{{Type: MediaTypeVideo, Caption: "🎬"}}

	download.settle(media, false)

	if (slices.Equal(limit.refundCalls(), []bool{false}) == false) || (media[0].Caption != "🎬") {
		t.Fatalf("expected a full refund and no note, got %v, %q", limit.refundCalls(), media[0].Caption)
	}
}

func TestSpecialDownloadRefusalOnlyMatchesLimitDenials(t *testing.T) {
	cases := map[GatedFetchDenial]bool{
		GatedFetchDeniedUserCap: true,
		GatedFetchDeniedChatCap: true,
		GatedFetchDeniedGlobal:  true,
		GatedFetchDeniedChatOff: true,
		GatedFetchDeniedBlocked: true,
		GatedFetchAllowed:       false,
		GatedFetchDeniedOff:     false,
		GatedFetchDeniedNoDB:    false,
		GatedFetchDeniedBudget:  false,
	}

	for denial, want := range cases {
		if got := IsSpecialDownloadRefusal(denial); got != want {
			t.Errorf("%q: got %t, want %t", denial, got, want)
		}
	}
}

func TestAppendCaptionNoteFitsTheCaptionLimit(t *testing.T) {
	note := "📊 Special downloads left this month: 2 of 3"
	room := TelegramCaptionLimitBytes - len(note) - len(captionNoteSeparator) - len(captionCutMark)
	head := fmt.Sprintf("%s&amp;", strings.Repeat("a", room-2))
	caption := fmt.Sprintf("%s%s", head, strings.Repeat("a", 1020-len(head)))
	item := &MediaInfo{Caption: caption}

	AppendCaptionNote(item, note)

	if (len(item.Caption) > TelegramCaptionLimitBytes) || (strings.HasSuffix(item.Caption, note) == false) {
		t.Fatalf("expected at most %d bytes ending with the note, got %d: %q", TelegramCaptionLimitBytes, len(item.Caption), item.Caption)
	}

	body := strings.TrimSuffix(item.Caption, note)

	if (strings.Contains(body, "&") == true) || (strings.Contains(body, "…") == false) {
		t.Fatalf("expected a cut body without a cut entity, got %q", body)
	}
}

func TestAppendCaptionNoteNeverLeavesALinkUnclosed(t *testing.T) {
	link := FormatOriginalLink(reelVideoTestURL)
	padding := strings.Repeat("b", 1000-len(link))
	room := len(padding) + strings.Index(link, "View") + len("View")
	noteLength := TelegramCaptionLimitBytes - len(captionNoteSeparator) - len(captionCutMark) - room
	item := &MediaInfo{Caption: padding + link}

	AppendCaptionNote(item, strings.Repeat("n", noteLength))

	if (len(item.Caption) > TelegramCaptionLimitBytes) || (strings.Contains(item.Caption, "<a ") == true) {
		t.Fatalf("expected the cut link dropped, got %q", item.Caption)
	}
}

func TestVideoOrMissingRemovesTheFilesOfANonVideoResult(t *testing.T) {
	path := filepath.Join(t.TempDir(), "photo.jpg")

	if err := os.WriteFile(path, []byte("jpeg"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	media, err := videoOrMissing([]*MediaInfo{{Type: MediaTypePhoto, Filename: path}}, nil)

	if (media != nil) || (errors.Is(err, errReelVideoMissing) == false) {
		t.Fatalf("expected errReelVideoMissing, got %+v, %v", media, err)
	}

	if _, statErr := os.Stat(path); os.IsNotExist(statErr) == false {
		t.Fatalf("expected the photo removed, stat: %v", statErr)
	}
}

func TestRunCostedStepsRemovesTheFilesOfAFailedStep(t *testing.T) {
	path := filepath.Join(t.TempDir(), "partial.mp4")

	if err := os.WriteFile(path, []byte("mp4"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	partial := costedStep{kind: costRelay, canRun: true, run: func() ([]*MediaInfo, error) {
		return []*MediaInfo{{Type: MediaTypeVideo, Filename: path}}, errSpecialTestStep
	}}

	runCostedSteps(newTestDownload(&recordingLimit{}), []costedStep{partial, winningStep(costPaid)})

	if _, statErr := os.Stat(path); os.IsNotExist(statErr) == false {
		t.Fatalf("expected the failed step's file removed, stat: %v", statErr)
	}
}

// costedTail reports whether every costed method of methods comes after
// every free one.
func costedTail(methods []ExtractionMethod) bool {
	result := true
	isInTail := false

	for _, method := range methods {
		_, isCosted := costedChainMethods[method]
		result = result && ((isInTail == false) || (isCosted == true))
		isInTail = isInTail || isCosted
	}

	return result
}

func TestCostedChainsAreSequential(t *testing.T) {
	tokenFile := writeRelayTestToken(t)
	t.Setenv("ENABLE_YOUTUBE", "true")
	t.Setenv("ENABLE_INSTAGRAM", "true")
	t.Setenv("APIFY_TOKEN", "test-token")
	t.Setenv("HIKERAPI_TOKEN", hikerTestKey)
	t.Setenv("HIKERAPI_MONTHLY_BUDGET_USD", "1")
	var strategies []*ExtractionStrategy

	configureTestRelayEnv(t, "http://127.0.0.1:8791", tokenFile)
	markDatabaseOpenForTest(t)

	for _, platforms := range []string{"", "youtube"} {
		for _, globalCap := range []string{"-1", "0"} {
			t.Setenv("RELAY_PLATFORMS", platforms)
			t.Setenv("GATED_FETCH_GLOBAL_MONTHLY_CAP", globalCap)
			strategies = append(strategies, GetExtractionStrategy(relayTestShortsURL))
		}
	}

	for _, order := range []string{"apify,hiker", "hiker,apify", "apify", "hiker"} {
		t.Setenv("INSTAGRAM_PAID_PROVIDERS", order)
		strategies = append(strategies, GetExtractionStrategy(storyTestURL))
	}

	for _, strategy := range strategies {
		isCosted := hasCostedMethod(strategy.Methods)

		if (isCosted == true) && ((strategy.Sequential == false) || (costedTail(strategy.Methods) == false)) {
			t.Errorf("%s %v: a costed chain must be sequential with the costed methods last", strategy.Platform, strategy.Methods)
		}
	}
}

func TestShortsChainUsesYtDlpWhileSpecialDownloadsAreOff(t *testing.T) {
	markDatabaseOpenForTest(t)
	configureTestRelayEnv(t, "http://127.0.0.1:8791", writeRelayTestToken(t))
	t.Setenv("ENABLE_YOUTUBE", "true")
	t.Setenv("GATED_FETCH_GLOBAL_MONTHLY_CAP", "0")

	strategy := GetExtractionStrategy(relayTestShortsURL)

	if slices.Equal(strategy.Methods, []ExtractionMethod{MethodYtDlp, MethodYouTubeApify}) == false {
		t.Fatalf("expected yt-dlp then Apify, got %v", strategy.Methods)
	}
}

// useNoPaidTokens turns every paid provider off.
func useNoPaidTokens(t *testing.T) {
	t.Setenv("APIFY_TOKEN", "")
	t.Setenv("HIKERAPI_TOKEN", "")
}

func TestSpecialDownloadKindsFollowTheConfig(t *testing.T) {
	t.Setenv("ENABLE_YOUTUBE", "true")
	t.Setenv("ENABLE_INSTAGRAM", "true")
	useNoPaidTokens(t)
	configureTestRelayEnv(t, "http://127.0.0.1:8791", writeRelayTestToken(t))

	relayOnly := CurrentSpecialDownloadKinds()
	wantRelayOnly := SpecialDownloadKindSet{Shorts: true, AllShorts: true, ViaRelay: true}

	if relayOnly != wantRelayOnly {
		t.Errorf("relay for YouTube only: got %+v", relayOnly)
	}

	t.Setenv("RELAY_PLATFORMS", "")
	t.Setenv("APIFY_TOKEN", "test-token")
	t.Setenv("APIFY_MONTHLY_BUDGET_USD", "4")
	t.Setenv("INSTAGRAM_PAID_PROVIDERS", "apify")

	apifyOnly := CurrentSpecialDownloadKinds()
	wantApifyOnly := SpecialDownloadKindSet{RestrictedPosts: true, ReelVideos: true, Stories: true, Shorts: true, ViaPaid: true}

	if apifyOnly != wantApifyOnly {
		t.Errorf("Apify only: got %+v", apifyOnly)
	}

	t.Setenv("ENABLE_INSTAGRAM", "false")

	noInstagram := CurrentSpecialDownloadKinds()

	if (noInstagram.RestrictedPosts == true) || (noInstagram.ReelVideos == true) || (noInstagram.Stories == true) || (noInstagram.Shorts == false) {
		t.Errorf("Instagram disabled: got %+v", noInstagram)
	}
}

func TestRelayOnlyCountsAsConfigured(t *testing.T) {
	t.Setenv("ENABLE_INSTAGRAM", "true")
	useNoPaidTokens(t)
	configureTestRelayEnv(t, "http://127.0.0.1:8791", writeRelayTestToken(t))
	t.Setenv("RELAY_PLATFORMS", relayPlatformInstagram)
	t.Setenv("GATED_FETCH_GLOBAL_MONTHLY_CAP", "")

	if (IsSpecialDownloadsConfigured() == false) || (slices.Equal(RelayPlatformsInUse(), []string{relayPlatformInstagram}) == false) {
		t.Fatalf("expected a relay-only setup configured, relay platforms %v", RelayPlatformsInUse())
	}

	t.Setenv("GATED_FETCH_GLOBAL_MONTHLY_CAP", "0")

	if IsSpecialDownloadsConfigured() == true {
		t.Fatalf("a bot-wide cap of 0 turns special downloads off")
	}
}

// newRelayShortsDownloader runs the Shorts relay chain against a fake relay
// answering with handler; Apify is not configured and yt-dlp is stubbed.
func newRelayShortsDownloader(t *testing.T, handler http.HandlerFunc) (*SmartDownloader, *relayTestServer) {
	fake := startRelayTestServer(t, handler)
	client, _ := newTestRelayClient(t, fake.server.URL)
	configureTestRelayEnv(t, fake.server.URL, client.tokenFile)
	t.Setenv("ENABLE_YOUTUBE", "true")
	t.Setenv("APIFY_TOKEN", "")
	sd := &SmartDownloader{youtubeRelay: &YouTubeRelayExtractor{client: client}, tempDir: client.tempDir, extractYtDlp: blockedYtDlp}

	return sd, fake
}

// blockedYtDlp is a yt-dlp stub that never gets anything.
func blockedYtDlp(context.Context, string, string) ([]*MediaInfo, error) {
	return nil, errors.New("yt-dlp: blocked")
}

// runTestChain runs the chain of mediaURL under a 90 s deadline with limit.
func runTestChain(sd *SmartDownloader, mediaURL string, limit specialLimit) ([]*MediaInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	return sd.downloadMediaWithContext(ctx, mediaURL, newSpecialDownload(mediaURL, DownloadRequester{ChatID: 1, UserID: 1, IsPrivate: true}, limit))
}

func TestFreeYtDlpShortNeverReserves(t *testing.T) {
	t.Setenv("ENABLE_YOUTUBE", "true")
	t.Setenv("RELAY_PLATFORMS", "")
	limit := &recordingLimit{reservation: specialReservation{usage: GatedFetchUsage{Used: 1, Limit: 3, IsLimited: true}}}
	sd := &SmartDownloader{extractYtDlp: func(context.Context, string, string) ([]*MediaInfo, error) {
		return []*MediaInfo{{Type: MediaTypeVideo, Caption: "🎬 short", Filename: "short.mp4"}}, nil
	}}

	media, err := runTestChain(sd, relayTestShortsURL, limit)

	if (err != nil) || (len(media) != 1) || (limit.reserveCount() != 0) || (strings.Contains(media[0].Caption, "Special downloads left") == true) {
		t.Fatalf("expected the free Short without a reserve or note, got %+v, %v, %d reserve(s)", media, err, limit.reserveCount())
	}
}

func TestRelayShortReservesBeforeTheRelayCall(t *testing.T) {
	markDatabaseOpenForTest(t)
	sd, fake := newRelayShortsDownloader(t, serveRelayVideo("", relayTestMP4))
	callsAtReserve := -1
	limit := &recordingLimit{onReserve: func() { callsAtReserve = len(fake.recorded()) }}

	media, err := runTestChain(sd, relayTestShortsURL, limit)

	if (err != nil) || (len(media) != 1) || (callsAtReserve != 0) || (len(fake.recorded()) != 1) || (limit.reserveCount() != 1) {
		t.Fatalf("expected one reserve before the one relay call, got %v, %d call(s) at reserve, %d in all", err, callsAtReserve, len(fake.recorded()))
	}
}

func TestRefusedShortNeverCallsTheRelay(t *testing.T) {
	markDatabaseOpenForTest(t)
	sd, fake := newRelayShortsDownloader(t, serveRelayVideo("", relayTestMP4))
	limit := &recordingLimit{reservation: specialReservation{denial: GatedFetchDeniedUserCap}}

	_, err := runTestChain(sd, relayTestShortsURL, limit)
	var deniedErr *GatedFetchDeniedError

	if (len(fake.recorded()) != 0) || (errors.As(err, &deniedErr) == false) || (deniedErr.Denial != GatedFetchDeniedUserCap) {
		t.Fatalf("expected a user_allowance refusal without a relay call, got %v, %d call(s)", err, len(fake.recorded()))
	}

	if (errors.Is(err, errSpecialDownloadDenied) == false) || (len(limit.refundCalls()) != 0) {
		t.Fatalf("expected the chain ended by the limit and nothing to refund, got %v, refunds %v", err, limit.refundCalls())
	}
}

func TestRefusedStoryNeverCallsApify(t *testing.T) {
	runs := 0
	input := ""
	apify := newFakeApify(t, `[]`, &runs, &input)
	t.Setenv("APIFY_TOKEN", "test-token")
	t.Setenv("HIKERAPI_TOKEN", "")
	t.Setenv("INSTAGRAM_PAID_PROVIDERS", "apify")
	sd := &SmartDownloader{storiesApify: &InstagramStoriesApifyExtractor{tempDir: t.TempDir(), actor: "a~b", api: fakeApifyClient(apify)}}
	limit := &recordingLimit{reservation: specialReservation{denial: GatedFetchDeniedChatCap}}

	_, err := runTestChain(sd, storyTestURL, limit)
	var deniedErr *GatedFetchDeniedError

	if (runs != 0) || (errors.As(err, &deniedErr) == false) || (deniedErr.Denial != GatedFetchDeniedChatCap) {
		t.Fatalf("expected a chat_cap refusal without an Apify run, got %v, %d run(s)", err, runs)
	}
}

func TestUnconfiguredCostedMethodRunsWithoutAReservation(t *testing.T) {
	t.Setenv("ENABLE_YOUTUBE", "true")
	t.Setenv("RELAY_PLATFORMS", "")
	useNoPaidTokens(t)
	limit := &recordingLimit{}
	sd := &SmartDownloader{extractYtDlp: blockedYtDlp}

	_, err := runTestChain(sd, relayTestShortsURL, limit)

	if (limit.reserveCount() != 0) || (errors.Is(err, errApifyNotConfigured) == false) {
		t.Fatalf("expected no reserve and errApifyNotConfigured, got %d reserve(s), %v", limit.reserveCount(), err)
	}
}

func TestDisabledRelayPlatformIsNotReserved(t *testing.T) {
	markDatabaseOpenForTest(t)
	sd, fake := newRelayShortsDownloader(t, serveRelayVideo("", relayTestMP4))
	setRelayPlatformDisabled(relayPlatformYouTube, true)
	limit := &recordingLimit{}

	runTestChain(sd, relayTestShortsURL, limit)

	if (limit.reserveCount() != 0) || (len(fake.recorded()) != 0) {
		t.Fatalf("expected no reserve and no relay call, got %d reserve(s), %d call(s)", limit.reserveCount(), len(fake.recorded()))
	}
}

func TestReservationThatUsesUpTheChainTimeRefundsEverything(t *testing.T) {
	markDatabaseOpenForTest(t)
	sd, fake := newRelayShortsDownloader(t, serveRelayVideo("", relayTestMP4))
	limit := &recordingLimit{delay: 200 * time.Millisecond}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := sd.downloadMediaWithContext(ctx, relayTestShortsURL, newSpecialDownload(relayTestShortsURL, DownloadRequester{ChatID: 1, UserID: 1, IsPrivate: true}, limit))

	if (err == nil) || (len(fake.recorded()) != 0) || (slices.Equal(limit.refundCalls(), []bool{false}) == false) {
		t.Fatalf("expected no relay call and a full refund, got %v, %d call(s), refunds %v", err, len(fake.recorded()), limit.refundCalls())
	}
}

func TestOffChainErrorIsNotARefusal(t *testing.T) {
	markDatabaseOpenForTest(t)
	sd, fake := newRelayShortsDownloader(t, serveRelayVideo("", relayTestMP4))
	limit := &recordingLimit{reservation: specialReservation{denial: GatedFetchDeniedOff}}

	_, err := runTestChain(sd, relayTestShortsURL, limit)
	var deniedErr *GatedFetchDeniedError

	if (errors.As(err, &deniedErr) == true) || (errors.Is(err, errSpecialDownloadsUnavailable) == false) || (len(fake.recorded()) != 0) {
		t.Fatalf("expected an unavailable chain without a refusal, got %v, %d call(s)", err, len(fake.recorded()))
	}
}

func TestBudgetRefusalKeepsThePaidChainReply(t *testing.T) {
	runs := 0
	input := ""
	apify := newFakeApify(t, `[]`, &runs, &input)
	t.Setenv("APIFY_TOKEN", "test-token")
	t.Setenv("APIFY_MONTHLY_BUDGET_USD", "0.05")
	t.Setenv("HIKERAPI_TOKEN", "")
	t.Setenv("INSTAGRAM_PAID_PROVIDERS", "apify")
	sd := &SmartDownloader{storiesApify: &InstagramStoriesApifyExtractor{tempDir: t.TempDir(), actor: "a~b", api: fakeApifyClient(apify)}}
	limit := &recordingLimit{}

	_, err := runTestChain(sd, storyTestURL, limit)
	var deniedErr *GatedFetchDeniedError

	if (IsPaidBudgetError(err) == false) || (errors.As(err, &deniedErr) == true) || (runs != 0) {
		t.Fatalf("expected the budget error unwrapped, got %v, %d run(s)", err, runs)
	}

	if slices.Equal(limit.refundCalls(), []bool{false}) == false {
		t.Fatalf("expected a full refund, got %v", limit.refundCalls())
	}
}

func TestGatedChainKeepsTheGlobalUnitWhenOneProviderRanAfterANotStartedOne(t *testing.T) {
	notStarted := fmt.Errorf("%w: apify budget check failed: %w", errCostedNotStarted, errSpecialTestStep)
	apify := &scriptedGatedProvider{name: "apify", err: notStarted}
	hiker := &scriptedGatedProvider{name: "hiker", err: errSpecialTestStep}
	chain := &fallbackGatedProvider{providers: []GatedPostProvider{apify, hiker}}
	limit := &recordingLimit{}
	paid := costedStep{kind: costPaid, canRun: true, run: func() ([]*MediaInfo, error) { return chain.FetchPost(gatedTestPostURL, false) }}

	runCostedSteps(newTestDownload(limit), []costedStep{paid})

	if slices.Equal(limit.refundCalls(), []bool{true}) == false {
		t.Fatalf("expected the bot-wide unit kept, got %v", limit.refundCalls())
	}

	_, onlyNotStartedErr := (&fallbackGatedProvider{providers: []GatedPostProvider{apify}}).FetchPost(gatedTestPostURL, false)

	if isCostedSkip(onlyNotStartedErr) == false {
		t.Fatalf("a chain whose only provider never started spent nothing: %v", onlyNotStartedErr)
	}
}

func TestReservationThatUsesUpTheChainTimeRefundsEverythingBeforeAPaidMethod(t *testing.T) {
	runs := 0
	input := ""
	apify := newFakeApify(t, `[]`, &runs, &input)
	t.Setenv("APIFY_TOKEN", "test-token")
	t.Setenv("HIKERAPI_TOKEN", "")
	t.Setenv("INSTAGRAM_PAID_PROVIDERS", "apify")
	sd := &SmartDownloader{storiesApify: &InstagramStoriesApifyExtractor{tempDir: t.TempDir(), actor: "a~b", api: fakeApifyClient(apify)}}
	limit := &recordingLimit{delay: 200 * time.Millisecond}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := sd.downloadMediaWithContext(ctx, storyTestURL, newSpecialDownload(storyTestURL, DownloadRequester{ChatID: 1, UserID: 1, IsPrivate: true}, limit))

	if (err == nil) || (runs != 0) || (slices.Equal(limit.refundCalls(), []bool{false}) == false) {
		t.Fatalf("expected no Apify run and a full refund, got %v, %d run(s), refunds %v", err, runs, limit.refundCalls())
	}
}

func TestStoryURLWithoutAnIDNeverReserves(t *testing.T) {
	runs := 0
	input := ""
	apify := newFakeApify(t, `[]`, &runs, &input)
	t.Setenv("APIFY_TOKEN", "test-token")
	t.Setenv("HIKERAPI_TOKEN", "")
	t.Setenv("INSTAGRAM_PAID_PROVIDERS", "apify")
	sd := &SmartDownloader{storiesApify: &InstagramStoriesApifyExtractor{tempDir: t.TempDir(), actor: "a~b", api: fakeApifyClient(apify)}}
	limit := &recordingLimit{}

	runTestChain(sd, "https://www.instagram.com/stories/someuser/", limit)

	if (runs != 0) || (limit.reserveCount() != 0) || (len(limit.refundCalls()) != 0) {
		t.Fatalf("expected no Apify run and no reservation, got %d run(s), %d reserve(s), refunds %v", runs, limit.reserveCount(), limit.refundCalls())
	}
}

func TestCaptionNotesSurviveTakingTheCaptionApart(t *testing.T) {
	item := &MediaInfo{Caption: "👤 @author\n\nA long description"}
	AppendCaptionNote(item, reelCoverNote)
	AppendCaptionNote(item, "📊 Special downloads left this month: 2 of 3")

	body := CaptionWithoutNotes(item)
	rebuilt := CaptionWithNotes("👤 @author", item.Notes)

	if body != "👤 @author\n\nA long description" {
		t.Fatalf("expected the caption without its notes, got %q", body)
	}

	if rebuilt != fmt.Sprintf("👤 @author\n\n%s\n\n📊 Special downloads left this month: 2 of 3", reelCoverNote) {
		t.Fatalf("expected the author line and both notes, got %q", rebuilt)
	}
}

func TestDropCaptionNoteTakesTheNoteOffTheCaptionAndTheNotes(t *testing.T) {
	item := &MediaInfo{Caption: "👤 @author"}
	AppendCaptionNote(item, reelCoverNote)

	isDropped := DropCaptionNote(item, reelCoverNote)
	isDroppedAgain := DropCaptionNote(item, reelCoverNote)

	if (isDropped == false) || (isDroppedAgain == true) || (item.Caption != "👤 @author") || (len(item.Notes) != 0) {
		t.Fatalf("expected the note dropped once, got %t/%t, %q, %v", isDropped, isDroppedAgain, item.Caption, item.Notes)
	}
}

func TestCapDenialSurvivesAFailedBonusCreditRead(t *testing.T) {
	limit := &recordingLimit{reservation: specialReservation{denial: GatedFetchDeniedUserCap, err: errSpecialTestStep}}
	download := newTestDownload(limit)

	download.reserve()

	if got := download.Denial(); got != GatedFetchDeniedUserCap {
		t.Fatalf("expected the allowance denial, got %q", got)
	}
}

func TestStoryHighlightsNeverReserve(t *testing.T) {
	t.Setenv("APIFY_TOKEN", "test-token")
	t.Setenv("APIFY_MONTHLY_BUDGET_USD", "5")
	sd := &SmartDownloader{storiesApify: &InstagramStoriesApifyExtractor{}}

	if sd.canRunCostedMethod(MethodStoriesApify, "https://www.instagram.com/stories/highlights/17900000000000000/") == true {
		t.Fatalf("a highlights link must not be chargeable")
	}

	if sd.canRunCostedMethod(MethodStoriesApify, "https://www.instagram.com/stories/someone/3400000000000000000/") == false {
		t.Fatalf("a story link must be chargeable")
	}
}
