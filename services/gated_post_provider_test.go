package services

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"unicode/utf8"
)

const apifyTestToken = "test-token"

// apifyTestUsageUsd is the monthly usage the fake limits endpoint reports.
var apifyTestUsageUsd = 0.10

// newApifyTestServer fakes the actor endpoint and the CDN it points to. The
// handler records the decoded run input in *gotInput.
func newApifyTestServer(t *testing.T, status int, shortcode string, gotInput *apifyRunInput) *httptest.Server {
	var server *httptest.Server

	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		isMedia := strings.HasPrefix(r.URL.Path, "/media/")
		isLimits := (r.URL.Path == "/users/me/limits")

		if isMedia == true {
			w.Write([]byte("media-bytes"))
		}

		if isLimits == true {
			fmt.Fprintf(w, `{"data":{"current":{"monthlyUsageUsd":%.2f}}}`, apifyTestUsageUsd)
		}

		if (isMedia == false) && (isLimits == false) {
			serveApifyRun(t, w, r, server.URL, status, shortcode, gotInput)
		}
	}))

	return server
}

// serveApifyRun answers a run-sync-get-dataset-items call with one post item
// whose two slides point at the fake CDN.
func serveApifyRun(t *testing.T, w http.ResponseWriter, r *http.Request, baseURL string, status int, shortcode string, gotInput *apifyRunInput) {
	if r.Header.Get("Authorization") != fmt.Sprintf("Bearer %s", apifyTestToken) {
		t.Errorf("missing bearer token, got %q", r.Header.Get("Authorization"))
	}

	body, _ := io.ReadAll(r.Body)
	json.Unmarshal(body, gotInput)
	w.WriteHeader(status)

	items := []map[string]interface{}{{
		"shortcode": shortcode,
		"username":  "averona.space",
		"full_name": "Averona & Co",
		"caption":   "Hello <world>",
		"media": []map[string]string{
			{"media_type": "image", "media_url": baseURL + "/media/a.jpg"},
			{"media_type": "video", "media_url": baseURL + "/media/b.mp4"},
		},
	}}
	json.NewEncoder(w).Encode(items)
}

func newApifyTestProvider(t *testing.T, server *httptest.Server) *ApifyInstagramProvider {
	t.Setenv("APIFY_MONTHLY_BUDGET_USD", "4")

	return &ApifyInstagramProvider{
		tempDir: t.TempDir(),
		actor:   "intropix~instagram-posts-reels-scraper",
		api:     &apifyClient{token: apifyTestToken, apiBase: server.URL, client: server.Client()},
	}
}

func TestApifyFetchPostDownloadsEverySlideAsSpoiler(t *testing.T) {
	var input apifyRunInput
	server := newApifyTestServer(t, http.StatusCreated, "DeYsKEDjbGB", &input)
	defer server.Close()

	media, err := newApifyTestProvider(t, server).FetchPost("https://www.instagram.com/p/DeYsKEDjbGB/?obrf=abc", true)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if (len(input.PostURLs) != 1) || (input.PostURLs[0] != "https://www.instagram.com/p/DeYsKEDjbGB/") {
		t.Fatalf("unexpected actor input: %+v", input)
	}

	if (len(media) != 2) || (media[0].Type != "photo") || (media[1].Type != "video") {
		t.Fatalf("unexpected media: %+v", media)
	}

	for _, item := range media {
		if item.Spoiler == false {
			t.Fatalf("expected spoiler media: %+v", item)
		}

		if _, statErr := os.Stat(item.Filename); statErr != nil {
			t.Fatalf("missing downloaded file %s: %v", item.Filename, statErr)
		}

		if strings.Contains(item.Caption, "instagram.com/p/DeYsKEDjbGB") == false {
			t.Fatalf("missing original link: %q", item.Caption)
		}
	}

	expectedCaption := "Averona &amp; Co (<a href=\"https://www.instagram.com/averona.space/\">@averona.space</a>)\n\n<tg-spoiler>Hello &lt;world&gt;</tg-spoiler>"

	if strings.Contains(media[0].Caption, expectedCaption) == false {
		t.Fatalf("unexpected first caption: %q", media[0].Caption)
	}

	if strings.Contains(media[1].Caption, "Hello") == true {
		t.Fatalf("the caption must only be on the first item: %q", media[1].Caption)
	}
}

func TestApifyFetchPostFailsOnErrorStatus(t *testing.T) {
	var input apifyRunInput
	server := newApifyTestServer(t, http.StatusPaymentRequired, "DeYsKEDjbGB", &input)
	defer server.Close()

	_, err := newApifyTestProvider(t, server).FetchPost("https://www.instagram.com/p/DeYsKEDjbGB/", true)

	if (err == nil) || (strings.Contains(err.Error(), "apify status 402") == false) {
		t.Fatalf("expected a 402 error, got %v", err)
	}
}

func TestApifyFetchPostFailsWhenPostIsMissing(t *testing.T) {
	var input apifyRunInput
	server := newApifyTestServer(t, http.StatusCreated, "SomeOtherPost", &input)
	defer server.Close()

	_, err := newApifyTestProvider(t, server).FetchPost("https://www.instagram.com/p/DeYsKEDjbGB/", true)

	if (err == nil) || (strings.Contains(err.Error(), "no media") == false) {
		t.Fatalf("expected a no-media error, got %v", err)
	}
}

func TestFetchGatedPostPassesNonGatedErrorsThrough(t *testing.T) {
	downloadErr := fmt.Errorf("kkinstagram: bad status code: 504")
	sd := &SmartDownloader{}

	media, err := sd.FetchGatedPost("https://www.instagram.com/p/DeYsKEDjbGB/", downloadErr, -100, 1, false)

	if (media != nil) || (err != downloadErr) {
		t.Fatalf("expected the original error back, got %v", err)
	}
}

func TestFetchGatedPostDeniesWithoutProvider(t *testing.T) {
	gatedErr := &InstagramGatedError{Gate: InstagramGate{Restricted: true, AgeLimited: true}}
	downloadErr := fmt.Errorf("all extraction methods failed: %w", gatedErr)
	sd := &SmartDownloader{}

	_, err := sd.FetchGatedPost("https://www.instagram.com/p/DeYsKEDjbGB/", downloadErr, 5, 5, true)
	deniedErr, isDenied := err.(*GatedFetchDeniedError)

	if (isDenied == false) || (deniedErr.Denial != GatedFetchDeniedOff) {
		t.Fatalf("expected an off denial, got %v", err)
	}

	var unwrapped *InstagramGatedError

	if errors.As(err, &unwrapped) == false {
		t.Fatalf("denied error must still unwrap to the gate error")
	}
}

func TestGatedPostCaptionSurvivesEscapingAndLinkSuffix(t *testing.T) {
	url := "https://www.instagram.com/p/DeYsKEDjbGB/?obrf=NTFzM2t5M2VsN2E1"
	captions := []string{
		strings.Repeat("it's ", 120),
		strings.Repeat("\"a\" & <b> ", 300),
		strings.Repeat("Дентал-арт 🤭\n", 200),
	}

	for _, raw := range captions {
		item := gatedPost{Username: "averona.space", FullName: "МАНІКЮР|ПЕДИКЮР|Виноградар🇺🇦|Варшавський +", Caption: raw}
		media := []*MediaInfo{{Type: "photo", Filename: "x.jpg", Caption: gatedPostCaption(item, url, true)}}
		appendOriginalURL(media, url)
		caption := media[0].Caption

		if len(caption) > TelegramCaptionLimitBytes {
			t.Fatalf("caption is %d bytes", len(caption))
		}

		if strings.Count(caption, "<tg-spoiler>") != strings.Count(caption, "</tg-spoiler>") {
			t.Fatalf("unbalanced spoiler tags: %q", caption)
		}

		if strings.Contains(caption, "View the original") == false {
			t.Fatalf("lost the original link: %q", caption)
		}

		for _, line := range strings.Split(caption, "\n") {
			if strings.Count(line, "<tg-spoiler>") != strings.Count(line, "</tg-spoiler>") {
				t.Fatalf("line with unbalanced spoiler: %q", line)
			}
		}
	}
}

func TestGatedPostCaptionWithoutTextIsAuthorOnly(t *testing.T) {
	item := gatedPost{Username: "someone"}
	caption := gatedPostCaption(item, "https://www.instagram.com/p/X/", true)

	if (strings.Contains(caption, "tg-spoiler") == true) || (strings.Contains(caption, "@someone") == false) {
		t.Fatalf("unexpected caption: %q", caption)
	}
}

func TestApifyRunIsRefusedOverBudget(t *testing.T) {
	var input apifyRunInput
	server := newApifyTestServer(t, http.StatusCreated, "DeYsKEDjbGB", &input)
	defer server.Close()

	provider := newApifyTestProvider(t, server)
	t.Setenv("APIFY_MONTHLY_BUDGET_USD", "0.12")

	_, err := provider.FetchPost("https://www.instagram.com/p/DeYsKEDjbGB/", true)

	if errors.Is(err, errApifyBudget) == false {
		t.Fatalf("expected the budget error, got %v", err)
	}

	if len(input.PostURLs) != 0 {
		t.Fatalf("the actor must not run over budget")
	}
}

func TestApifyFetchPostWithoutSpoilerSendsPlainMedia(t *testing.T) {
	var input apifyRunInput
	server := newApifyTestServer(t, http.StatusCreated, "DeYsKEDjbGB", &input)
	defer server.Close()

	media, err := newApifyTestProvider(t, server).FetchPost("https://www.instagram.com/p/DeYsKEDjbGB/", false)

	if (err != nil) || (len(media) != 2) {
		t.Fatalf("unexpected result: %d %v", len(media), err)
	}

	if (media[0].Spoiler == true) || (strings.Contains(media[0].Caption, "tg-spoiler") == true) || (strings.Contains(media[0].Caption, "Hello &lt;world&gt;") == false) {
		t.Fatalf("expected plain media and caption, got spoiler=%v caption=%q", media[0].Spoiler, media[0].Caption)
	}
}

func TestApifyFetchPostRetriesAnEmptyAnswerOnce(t *testing.T) {
	runs := 0
	input := ""
	server := newFakeApify(t, `[]`, &runs, &input)
	provider := &ApifyInstagramProvider{tempDir: t.TempDir(), actor: "a~b", api: fakeApifyClient(server)}
	apifyEmptyRetryDelay = 0

	_, err := provider.FetchPost("https://www.instagram.com/p/DeYsKEDjbGB/", false)

	if (errors.Is(err, errApifyNoMedia) == false) || (runs != 2) {
		t.Fatalf("expected one retry, got runs=%d err=%v", runs, err)
	}
}

func TestApifyFetchPostDoesNotRetryANonEmptyAnswer(t *testing.T) {
	runs := 0
	input := ""
	server := newFakeApify(t, `[{"shortcode":"Other","media":[{"media_type":"image","media_url":"x"}]}]`, &runs, &input)
	provider := &ApifyInstagramProvider{tempDir: t.TempDir(), actor: "a~b", api: fakeApifyClient(server)}

	_, err := provider.FetchPost("https://www.instagram.com/p/DeYsKEDjbGB/", false)

	if (errors.Is(err, errApifyNoMedia) == false) || (runs != 1) {
		t.Fatalf("expected no retry for a non-empty answer, got runs=%d err=%v", runs, err)
	}
}

func TestFitSpoilerCaptionFitsEscapedTextExactly(t *testing.T) {
	raw := strings.Repeat("&", 300) + strings.Repeat("x", 300)

	got := fitSpoilerCaption(raw, 1000, false)

	if got != strings.Repeat("&amp;", 200) {
		t.Fatalf("want 200 escaped ampersands (1000 bytes), got %d bytes %q", len(got), got[:40])
	}
}

// longestFittingCaption is the linear reference fitSpoilerCaption must match.
func longestFittingCaption(raw string, budget int, spoiler bool) string {
	result := ""

	for end := 0; end <= len(raw); end++ {
		isRuneBoundary := utf8.ValidString(raw[:end])
		wrapped := wrapSpoilerLines(raw[:end], spoiler)

		if (isRuneBoundary == true) && (len(wrapped) <= budget) {
			result = wrapped
		}
	}

	return result
}

func TestFitSpoilerCaptionIsTheLongestFitAtRuneBoundaries(t *testing.T) {
	texts := []string{
		strings.Repeat("Привет, «мир» & друзья\n", 6),
		strings.Repeat("🤭 emoji <b>👍🏻</b> ", 8),
		"\"quoted\" 'single' & <tag>\n\n  second line  ",
	}
	budgets := []int{-1, 0, 1, 5, 17, 64, 101, 255, 4096}

	for _, raw := range texts {
		for _, budget := range budgets {
			for _, spoiler := range []bool{false, true} {
				got := fitSpoilerCaption(raw, budget, spoiler)
				want := longestFittingCaption(raw, budget, spoiler)

				if got != want {
					t.Fatalf("budget %d spoiler %v: got %q, want %q", budget, spoiler, got, want)
				}

				if (utf8.ValidString(got) == false) || (len(got) > max(budget, 0)) {
					t.Fatalf("budget %d: invalid or too long result %q", budget, got)
				}
			}
		}
	}
}

func TestFitPrefixEndReportsWhenNothingFits(t *testing.T) {
	measure := func(prefix string) int {
		return len(prefix)
	}

	if got := fitPrefixEnd("abc", -1, measure); got != -1 {
		t.Fatalf("negative budget: got %d, want -1", got)
	}

	if got := fitPrefixEnd("aя", 2, measure); got != 1 {
		t.Fatalf("budget inside a rune: got %d, want 1", got)
	}

	if got := fitPrefixEnd("abc", 3, measure); got != 3 {
		t.Fatalf("exact budget: got %d, want 3", got)
	}
}
