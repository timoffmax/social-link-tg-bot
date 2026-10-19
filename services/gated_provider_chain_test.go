package services

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// scriptedGatedProvider answers with a fixed result and counts its calls.
type scriptedGatedProvider struct {
	name  string
	media []*MediaInfo
	err   error
	calls int
}

func (p *scriptedGatedProvider) Name() string {
	return p.name
}

func (p *scriptedGatedProvider) FetchPost(string, bool) ([]*MediaInfo, error) {
	p.calls++

	return p.media, p.err
}

func oneSlide() []*MediaInfo {
	return []*MediaInfo{{Type: MediaTypePhoto, Filename: "x.jpg", Caption: "📷 author"}}
}

func TestFallbackTriesHikerAfterEveryApifyFailure(t *testing.T) {
	apifyOutcomes := map[string]error{
		"not configured":    errApifyNotConfigured,
		"budget":            fmt.Errorf("%w: test", errApifyBudget),
		"usage lookup":      errors.New("apify budget check failed: apify status 500: x"),
		"http error":        errors.New("apify status 403: account blocked"),
		"timeout":           context.DeadlineExceeded,
		"empty after retry": fmt.Errorf("%w for x", errApifyNoMedia),
		"no slide download": errors.New("no apify slide could be downloaded for X"),
	}

	for name, apifyErr := range apifyOutcomes {
		t.Run(name, func(t *testing.T) {
			apify := &scriptedGatedProvider{name: "apify", err: apifyErr}
			hiker := &scriptedGatedProvider{name: "hiker", media: oneSlide()}
			chain := &fallbackGatedProvider{providers: []GatedPostProvider{apify, hiker}}

			media, err := chain.FetchPost(gatedTestPostURL, true)

			if (err != nil) || (len(media) != 1) || (hiker.calls != 1) {
				t.Fatalf("expected HikerAPI to serve the post, got %v calls=%d", err, hiker.calls)
			}
		})
	}
}

func TestFallbackStopsAfterAnApifySuccess(t *testing.T) {
	results := map[string][]*MediaInfo{"full": {oneSlide()[0], oneSlide()[0]}, "partial": oneSlide()}

	for name, media := range results {
		t.Run(name, func(t *testing.T) {
			apify := &scriptedGatedProvider{name: "apify", media: media}
			hiker := &scriptedGatedProvider{name: "hiker", media: oneSlide()}
			chain := &fallbackGatedProvider{providers: []GatedPostProvider{apify, hiker}}

			got, err := chain.FetchPost(gatedTestPostURL, true)

			if (err != nil) || (len(got) != len(media)) || (hiker.calls != 0) {
				t.Fatalf("a success (even partial) must stop the chain, got %v hiker calls=%d", err, hiker.calls)
			}
		})
	}
}

func TestFallbackIsABudgetRefusalOnlyWhenNothingRan(t *testing.T) {
	budget := fmt.Errorf("%w: test", errApifyBudget)
	rows := []struct {
		name     string
		apify    error
		hiker    error
		isBudget bool
	}{
		{name: "both budget", apify: budget, hiker: errHikerBudget, isBudget: true},
		{name: "not configured and budget", apify: errApifyNotConfigured, hiker: errHikerOutOfBalance, isBudget: true},
		{name: "apify ran, hiker budget", apify: errors.New("apify status 500"), hiker: errHikerBudget, isBudget: false},
		{name: "apify budget, hiker ran", apify: budget, hiker: errors.New("hikerapi status 500"), isBudget: false},
		{name: "apify budget, hiker unavailable", apify: budget, hiker: errHikerUnavailable, isBudget: false},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			apify := &scriptedGatedProvider{name: "apify", err: row.apify}
			hiker := &scriptedGatedProvider{name: "hiker", err: row.hiker}
			chain := &fallbackGatedProvider{providers: []GatedPostProvider{apify, hiker}}

			_, err := chain.FetchPost(gatedTestPostURL, true)

			if (err == nil) || (isPaidBudgetError(err) != row.isBudget) {
				t.Fatalf("expected budget=%v, got %v", row.isBudget, err)
			}
		})
	}
}

func TestFallbackWithAnUnavailableProviderIsASkipButNotABudgetRefusal(t *testing.T) {
	apify := &scriptedGatedProvider{name: "apify", err: errApifyNotConfigured}
	hiker := &scriptedGatedProvider{name: "hiker", err: errHikerUnavailable}
	chain := &fallbackGatedProvider{providers: []GatedPostProvider{apify, hiker}}

	_, err := chain.FetchPost(gatedTestPostURL, true)

	if (isPaidSkip(err) == false) || (isPaidUnavailable(err) == false) || (isPaidBudgetError(err) == true) || (hiker.calls != 1) {
		t.Fatalf("expected an unavailable skip, got %v", err)
	}
}

func TestFallbackContentOutcomeIsFinal(t *testing.T) {
	apify := &scriptedGatedProvider{name: "apify", err: errors.New("apify status 500")}
	hiker := &scriptedGatedProvider{name: "hiker", err: fmt.Errorf("%w: status 404", errHikerContent)}
	third := &scriptedGatedProvider{name: "third", media: oneSlide()}
	chain := &fallbackGatedProvider{providers: []GatedPostProvider{apify, hiker, third}}

	_, err := chain.FetchPost(gatedTestPostURL, true)

	if (err == nil) || (isPaidBudgetError(err) == true) || (isPaidContentOutcome(err) == false) || (third.calls != 0) {
		t.Fatalf("a 404 must stop the chain as a non-budget error, got %v third calls=%d", err, third.calls)
	}

	if chain.Name() != "apify→hiker→third" {
		t.Fatalf("unexpected name %q", chain.Name())
	}
}

func TestGatedProvidersFollowTheConfiguredOrder(t *testing.T) {
	t.Setenv("APIFY_TOKEN", "test-token")
	t.Setenv("APIFY_MONTHLY_BUDGET_USD", "4")
	t.Setenv("HIKERAPI_TOKEN", hikerTestKey)
	t.Setenv("HIKERAPI_MONTHLY_BUDGET_USD", "1")
	t.Setenv("INSTAGRAM_PAID_PROVIDERS", "")

	defaultChain, isChain := NewGatedPostProvider(t.TempDir()).(*fallbackGatedProvider)

	if (isChain == false) || (defaultChain.Name() != "apify→hiker") {
		t.Fatalf("expected Apify then HikerAPI by default, got %+v", defaultChain)
	}

	t.Setenv("INSTAGRAM_PAID_PROVIDERS", " Hiker , bogus, apify, hiker")
	flipped := NewGatedPostProvider(t.TempDir()).(*fallbackGatedProvider)
	stories := instagramStoriesMethods()

	if (flipped.Name() != "hiker→apify") || (len(stories) != 2) || (stories[0] != MethodStoriesHiker) {
		t.Fatalf("expected the flipped order, got %q %v", flipped.Name(), stories)
	}

	t.Setenv("INSTAGRAM_PAID_PROVIDERS", "bogus")

	if NewGatedPostProvider(t.TempDir()).Name() != "apify→hiker" {
		t.Fatalf("a list without a known name keeps the default order")
	}
}

func TestStoriesNeverFallBackToAnUnlistedApify(t *testing.T) {
	t.Setenv("APIFY_TOKEN", "test-token")
	t.Setenv("APIFY_MONTHLY_BUDGET_USD", "4")
	t.Setenv("HIKERAPI_TOKEN", "")
	t.Setenv("INSTAGRAM_PAID_PROVIDERS", "hiker")

	if stories := instagramStoriesMethods(); (len(stories) != 0) || (isStoriesFetchConfigured() == true) {
		t.Fatalf("expected no stories method and Stories not offered, got %v", stories)
	}

	t.Setenv("HIKERAPI_TOKEN", hikerTestKey)
	t.Setenv("HIKERAPI_MONTHLY_BUDGET_USD", "0")

	if paused := instagramStoriesMethods(); (len(paused) != 0) || (isStoriesFetchConfigured() == true) {
		t.Fatalf("expected no stories method with a paused HikerAPI budget, got %v", paused)
	}

	t.Setenv("HIKERAPI_MONTHLY_BUDGET_USD", "1")

	if hikerOnly := instagramStoriesMethods(); (len(hikerOnly) != 1) || (hikerOnly[0] != MethodStoriesHiker) {
		t.Fatalf("expected HikerAPI alone, got %v", hikerOnly)
	}
}

func TestGatedProviderRegistryBuildsOnlyTheNamedProvider(t *testing.T) {
	t.Setenv("APIFY_TOKEN", "test-token")
	t.Setenv("HIKERAPI_TOKEN", hikerTestKey)
	t.Setenv("HIKERAPI_MONTHLY_BUDGET_USD", "1")

	_, isApify := buildGatedProvider(PaidProviderApify, t.TempDir()).(*ApifyInstagramProvider)
	_, isHiker := buildGatedProvider(PaidProviderHiker, t.TempDir()).(*HikerInstagramProvider)

	if (isApify == false) || (isHiker == false) || (buildGatedProvider("bogus", t.TempDir()) != nil) {
		t.Fatalf("expected each name to build its own provider and an unknown one nothing")
	}
}

func TestHikerOnlySetupOffersRestrictedPosts(t *testing.T) {
	t.Setenv("APIFY_TOKEN", "")
	t.Setenv("HIKERAPI_TOKEN", hikerTestKey)
	t.Setenv("HIKERAPI_MONTHLY_BUDGET_USD", "1")
	t.Setenv("GATED_FETCH_GLOBAL_MONTHLY_CAP", "")

	_, isHiker := NewGatedPostProvider(t.TempDir()).(*HikerInstagramProvider)

	if (CurrentSpecialDownloadKinds().RestrictedPosts == false) || (isStoriesFetchConfigured() == false) || (isHiker == false) {
		t.Fatalf("a HikerAPI-only setup must still fetch restricted posts")
	}

	t.Setenv("INSTAGRAM_PAID_PROVIDERS", "apify")

	if CurrentSpecialDownloadKinds().RestrictedPosts == true {
		t.Fatalf("HikerAPI left out of INSTAGRAM_PAID_PROVIDERS must not fetch restricted posts")
	}
}

func TestStoriesAreOfferedOnlyWhenTheStoriesChainCanRun(t *testing.T) {
	t.Setenv("APIFY_TOKEN", "")
	t.Setenv("HIKERAPI_TOKEN", hikerTestKey)
	t.Setenv("HIKERAPI_MONTHLY_BUDGET_USD", "1")
	t.Setenv("INSTAGRAM_PAID_PROVIDERS", "apify")

	if isStoriesFetchConfigured() == true {
		t.Fatalf("HikerAPI left out of INSTAGRAM_PAID_PROVIDERS must not offer Stories")
	}

	t.Setenv("APIFY_TOKEN", "test-token")
	t.Setenv("APIFY_MONTHLY_BUDGET_USD", "4")
	t.Setenv("HIKERAPI_TOKEN", "")
	t.Setenv("INSTAGRAM_PAID_PROVIDERS", "hiker")

	if isStoriesFetchConfigured() == true {
		t.Fatalf("Apify left out of INSTAGRAM_PAID_PROVIDERS must not offer Stories")
	}
}

func TestGatedProvidersWithTokenKeepAPausedProvider(t *testing.T) {
	t.Setenv("APIFY_TOKEN", "test-token")
	t.Setenv("APIFY_MONTHLY_BUDGET_USD", "0")
	t.Setenv("HIKERAPI_TOKEN", hikerTestKey)
	t.Setenv("HIKERAPI_MONTHLY_BUDGET_USD", "1")
	t.Setenv("INSTAGRAM_PAID_PROVIDERS", "")

	want := []GatedProviderState{{Name: PaidProviderApify, IsOn: false}, {Name: PaidProviderHiker, IsOn: true}}

	if got := GatedProvidersWithToken(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

func newHikerTestProvider(t *testing.T, fake *fakeHiker) *HikerInstagramProvider {
	return &HikerInstagramProvider{tempDir: t.TempDir(), api: fake.client()}
}

func TestHikerFetchPostDownloadsEveryCarouselSlideAsSpoiler(t *testing.T) {
	ledger := useHikerTestEnv(t)
	fake := newFakeHiker(t)
	fake.bodies[hikerPathMediaByCode] = hikerCarouselBody

	media, err := newHikerTestProvider(t, fake).FetchPost("https://www.instagram.com/p/DeYsKEDjbGB/?igsh=abc", true)

	if (err != nil) || (len(media) != 2) || (media[0].Type != MediaTypePhoto) || (media[1].Type != MediaTypeVideo) {
		t.Fatalf("unexpected result: %+v %v", media, err)
	}

	expectedCaption := "Averona &amp; Co (<a href=\"https://www.instagram.com/averona.space/\">@averona.space</a>)\n\n<tg-spoiler>Hello &lt;world&gt;</tg-spoiler>"

	if (strings.Contains(media[0].Caption, expectedCaption) == false) || (strings.Contains(media[1].Caption, "Hello") == true) {
		t.Fatalf("unexpected captions: %q / %q", media[0].Caption, media[1].Caption)
	}

	for _, item := range media {
		if _, statErr := os.Stat(item.Filename); (statErr != nil) || (item.Spoiler == false) || (strings.Contains(item.Caption, "View the original") == false) {
			t.Fatalf("unexpected item %+v (%v)", item, statErr)
		}
	}

	rows := ledger.recorded()

	if (len(rows) != 1) || (rows[0].Feature != paidFeatureGated) || (fake.queries[0].Get("code") != "DeYsKEDjbGB") {
		t.Fatalf("expected one gated request for the shortcode, got %+v %+v", rows, fake.queries)
	}
}

func TestHikerFetchPostReadsSinglePhotosAndVideos(t *testing.T) {
	bodies := map[string]string{MediaTypePhoto: hikerPhotoBody, MediaTypeVideo: hikerVideoBody}

	for mediaType, body := range bodies {
		t.Run(mediaType, func(t *testing.T) {
			useHikerTestEnv(t)
			fake := newFakeHiker(t)
			fake.bodies[hikerPathMediaByCode] = body

			media, err := newHikerTestProvider(t, fake).FetchPost("https://www.instagram.com/reel/VIDEO1/", false)

			if (err != nil) || (len(media) != 1) || (media[0].Type != mediaType) || (media[0].Spoiler == true) {
				t.Fatalf("unexpected result: %+v %v", media, err)
			}
		})
	}
}

func TestHikerFetchPostLeavesNoFilesWhenEverySlideFails(t *testing.T) {
	useHikerTestEnv(t)
	fake := newFakeHiker(t)
	fake.bodies[hikerPathMediaByCode] = strings.ReplaceAll(hikerCarouselBody, "/media/", "/media/missing/")
	provider := newHikerTestProvider(t, fake)

	_, err := provider.FetchPost(gatedTestPostURL, true)
	entries, _ := os.ReadDir(provider.tempDir)

	if (err == nil) || (isPaidSkip(err) == true) || (len(entries) != 0) {
		t.Fatalf("expected a failure without files, got %v files=%d", err, len(entries))
	}
}

func TestHikerFetchPostKeepsTheSlidesThatDownloaded(t *testing.T) {
	useHikerTestEnv(t)
	fake := newFakeHiker(t)
	fake.bodies[hikerPathMediaByCode] = strings.Replace(hikerCarouselBody, "/media/a.jpg", "/media/missing/a.jpg", 1)

	media, err := newHikerTestProvider(t, fake).FetchPost(gatedTestPostURL, true)

	if (err != nil) || (len(media) != 1) || (media[0].Type != MediaTypeVideo) || (strings.Contains(media[0].Caption, "Hello") == false) {
		t.Fatalf("expected the video with the caption, got %+v %v", media, err)
	}
}

func TestHikerFetchPostWithoutMediaFails(t *testing.T) {
	useHikerTestEnv(t)
	fake := newFakeHiker(t)
	fake.bodies[hikerPathMediaByCode] = `{"pk":"1","code":"X","media_type":8,"user":{"pk":"1"},"resources":[]}`

	_, err := newHikerTestProvider(t, fake).FetchPost(gatedTestPostURL, true)

	if errors.Is(err, errHikerNoMedia) == false {
		t.Fatalf("expected no media, got %v", err)
	}
}

func TestHikerFetchPostReportsFailuresButNotContentOrBudget(t *testing.T) {
	statuses := map[int]int{http.StatusInternalServerError: 1, http.StatusNotFound: 0, http.StatusPaymentRequired: 0}

	for status, expectedFailures := range statuses {
		t.Run(fmt.Sprintf("%d", status), func(t *testing.T) {
			useHikerTestEnv(t)
			newAlertRecorder(t)
			fake := newFakeHiker(t)
			fake.status = status

			newHikerTestProvider(t, fake).FetchPost(gatedTestPostURL, true)

			if recordedFailureCount(hikerServiceName) != expectedFailures {
				t.Fatalf("expected %d outage failures, got %d", expectedFailures, recordedFailureCount(hikerServiceName))
			}
		})
	}
}

// newFailingApify answers every actor run with HTTP 500.
func newFailingApify(t *testing.T) *httptest.Server {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		isLimits := (r.URL.Path == "/users/me/limits")

		if isLimits == true {
			fmt.Fprint(w, `{"data":{"current":{"monthlyUsageUsd":0.10}}}`)
		}

		if isLimits == false {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"error":"actor crashed"}`)
		}
	}))
	t.Cleanup(server.Close)
	t.Setenv("APIFY_MONTHLY_BUDGET_USD", "4")

	return server
}

const storyTestURL = "https://www.instagram.com/stories/nasa/4007835059386000871/"

// newStoriesTestDownloader builds a downloader whose stories chain runs the
// given Apify fake (nil = not configured) and the HikerAPI fake.
func newStoriesTestDownloader(t *testing.T, apifyServer *httptest.Server, fake *fakeHiker) *SmartDownloader {
	sd := &SmartDownloader{storiesHiker: &InstagramStoriesHikerExtractor{tempDir: t.TempDir(), api: fake.client()}}

	if apifyServer != nil {
		sd.storiesApify = &InstagramStoriesApifyExtractor{tempDir: t.TempDir(), actor: "a~b", api: fakeApifyClient(apifyServer)}
	}

	return sd
}

func TestStoriesChainStopsOnAPrivateAccount(t *testing.T) {
	useHikerTestEnv(t)
	runs := 0
	input := ""
	apifyServer := newFakeApify(t, `[{"username":"nasa","status":"private","stories":[]}]`, &runs, &input)
	fake := newFakeHiker(t)
	fake.bodies[hikerPathStoryByURL] = hikerStoryVideoBody
	sd := newStoriesTestDownloader(t, apifyServer, fake)

	_, err := sd.downloadMediaWithContext(context.Background(), storyTestURL, newAllowedDownload(storyTestURL))

	if (err == nil) || (isPaidContentOutcome(err) == false) || (fake.callCount(hikerPathStoryByURL) != 0) {
		t.Fatalf("a private account must stop the chain, got %v hiker calls=%d", err, fake.callCount(hikerPathStoryByURL))
	}
}

func TestStoriesChainUsesHikerAfterAnApifyError(t *testing.T) {
	ledger := useHikerTestEnv(t)
	fake := newFakeHiker(t)
	fake.bodies[hikerPathStoryByURL] = hikerStoryVideoBody
	sd := newStoriesTestDownloader(t, newFailingApify(t), fake)

	sharedURL := fmt.Sprintf("%s?utm_source=ig_story_item_share", storyTestURL)
	media, err := sd.downloadMediaWithContext(context.Background(), sharedURL, newAllowedDownload(sharedURL))

	if (err != nil) || (len(media) != 1) || (media[0].Type != MediaTypeVideo) || (strings.Contains(media[0].Caption, "@nasa") == false) {
		t.Fatalf("expected the HikerAPI story, got %+v %v", media, err)
	}

	rows := ledger.recorded()

	if (len(rows) != 1) || (rows[0].Feature != paidFeatureStory) || (fake.queries[0].Get("url") != storyTestURL) {
		t.Fatalf("expected one story request for the clean URL, got %+v %+v", rows, fake.queries)
	}
}

func TestStoriesChainBudgetReplyOnlyWhenEveryProviderWasSkipped(t *testing.T) {
	rows := []struct {
		name        string
		apifyBudget bool
		apifyFails  bool
		noApify     bool
		hikerBudget bool
		ledgerDown  bool
		isBudget    bool
	}{
		{name: "apify budget, hiker error", apifyBudget: true, isBudget: false},
		{name: "apify error, hiker budget", apifyFails: true, hikerBudget: true, isBudget: false},
		{name: "both budget", apifyBudget: true, hikerBudget: true, isBudget: true},
		{name: "no apify, hiker budget", noApify: true, hikerBudget: true, isBudget: true},
		{name: "apify budget, hiker ledger unreadable", apifyBudget: true, ledgerDown: true, isBudget: false},
		{name: "no apify, hiker ledger unreadable", noApify: true, ledgerDown: true, isBudget: false},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			ledger := useHikerTestEnv(t)
			fake := newFakeHiker(t)
			fake.status = http.StatusInternalServerError
			runs := 0
			input := ""
			apifyServer := newFakeApify(t, `[]`, &runs, &input)

			if row.apifyFails == true {
				apifyServer = newFailingApify(t)
			}

			if row.noApify == true {
				apifyServer = nil
			}

			if row.apifyBudget == true {
				t.Setenv("APIFY_MONTHLY_BUDGET_USD", "0.05")
			}

			if row.hikerBudget == true {
				ledger.spent = 5
			}

			if row.ledgerDown == true {
				ledger.readErr = errors.New("database down")
			}

			sd := newStoriesTestDownloader(t, apifyServer, fake)

			_, err := sd.downloadMediaWithContext(context.Background(), storyTestURL, newAllowedDownload(storyTestURL))

			if (err == nil) || (IsPaidBudgetError(err) != row.isBudget) {
				t.Fatalf("expected budget=%v, got %v", row.isBudget, err)
			}
		})
	}
}

func TestHikerStoryReadsAPhotoAndRefusesHighlights(t *testing.T) {
	useHikerTestEnv(t)
	fake := newFakeHiker(t)
	fake.bodies[hikerPathStoryByURL] = hikerStoryPhotoBody
	extractor := &InstagramStoriesHikerExtractor{tempDir: t.TempDir(), api: fake.client()}

	media, err := extractor.ExtractMedia(context.Background(), "https://www.instagram.com/stories/nasa/111/")
	_, highlightErr := extractor.ExtractMedia(context.Background(), "https://www.instagram.com/stories/highlights/17900000000000000/")

	if (err != nil) || (len(media) != 1) || (media[0].Type != MediaTypePhoto) {
		t.Fatalf("expected the photo story, got %+v %v", media, err)
	}

	if (highlightErr == nil) || (fake.callCount(hikerPathStoryByURL) != 1) {
		t.Fatalf("highlights must be refused without a request, got %v calls=%d", highlightErr, fake.callCount(hikerPathStoryByURL))
	}
}

func TestHikerStoryNotFoundIsAContentOutcome(t *testing.T) {
	useHikerTestEnv(t)
	fake := newFakeHiker(t)
	fake.status = http.StatusNotFound
	extractor := &InstagramStoriesHikerExtractor{tempDir: t.TempDir(), api: fake.client()}

	_, err := extractor.ExtractMedia(context.Background(), storyTestURL)

	if isPaidContentOutcome(err) == false {
		t.Fatalf("expected a content outcome, got %v", err)
	}
}
