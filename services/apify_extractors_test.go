package services

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// newFakeApify serves /users/me/limits, one actor run answer (runBody, with
// {{BASE}} replaced by the server URL) and /media/* files. It counts runs.
func newFakeApify(t *testing.T, runBody string, runs *int, lastInput *string) *httptest.Server {
	var server *httptest.Server

	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		isLimits := (r.URL.Path == "/users/me/limits")
		isMedia := strings.HasPrefix(r.URL.Path, "/media/")

		if isLimits == true {
			fmt.Fprint(w, `{"data":{"current":{"monthlyUsageUsd":0.10}}}`)
		}

		if isMedia == true {
			w.Write([]byte("media-bytes"))
		}

		if (isLimits == false) && (isMedia == false) {
			body, _ := io.ReadAll(r.Body)
			*lastInput = string(body)
			*runs++
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, strings.ReplaceAll(runBody, "{{BASE}}", server.URL))
		}
	}))
	t.Cleanup(server.Close)
	t.Setenv("APIFY_MONTHLY_BUDGET_USD", "4")

	return server
}

func fakeApifyClient(server *httptest.Server) *apifyClient {
	return &apifyClient{token: "test-token", apiBase: server.URL, client: server.Client()}
}

func TestYouTubeApifyDownloadsTheVideoWithCaption(t *testing.T) {
	runs := 0
	input := ""
	server := newFakeApify(t, `[{"title":"Me at the zoo","uploader":"jawed & co","downloadUrl":"{{BASE}}/media/v.mp4","status":"success"}]`, &runs, &input)
	extractor := &YouTubeApifyExtractor{tempDir: t.TempDir(), actor: "a~b", api: fakeApifyClient(server)}

	media, err := extractor.ExtractMedia(context.Background(), "https://www.youtube.com/shorts/aqz-KE-bpKQ")

	if (err != nil) || (len(media) != 1) || (media[0].Type != MediaTypeVideo) {
		t.Fatalf("unexpected result: %+v %v", media, err)
	}

	if _, statErr := os.Stat(media[0].Filename); statErr != nil {
		t.Fatalf("missing file: %v", statErr)
	}

	if (strings.Contains(media[0].Caption, "jawed &amp; co") == false) || (strings.Contains(media[0].Caption, "Me at the zoo") == false) {
		t.Fatalf("unexpected caption: %q", media[0].Caption)
	}

	if (strings.Contains(input, `"quality":"480"`) == false) || (strings.Contains(input, `"residentialProxyMode":"disabled"`) == false) {
		t.Fatalf("unexpected actor input: %s", input)
	}
}

func TestYouTubeApifyReportsActorFailure(t *testing.T) {
	runs := 0
	input := ""
	server := newFakeApify(t, `[{"status":"failed","errorCode":"PRIVATE_VIDEO","error":"Private video"}]`, &runs, &input)
	extractor := &YouTubeApifyExtractor{tempDir: t.TempDir(), actor: "a~b", api: fakeApifyClient(server)}

	_, err := extractor.ExtractMedia(context.Background(), "https://www.youtube.com/shorts/jNQXAC9IVRw")

	if (err == nil) || (strings.Contains(err.Error(), "PRIVATE_VIDEO") == false) {
		t.Fatalf("expected the actor error, got %v", err)
	}

	if strings.Contains(input, `"quality":"480"`) == false {
		t.Fatalf("Shorts must use 480p: %s", input)
	}
}

const fakeStoriesBody = `[{"username":"nasa","status":"ok","stories":[
	{"id":"111","mediaType":"image","imageUrl":"{{BASE}}/media/a.jpg"},
	{"id":"4007835059386000871","mediaType":"video","imageUrl":"{{BASE}}/media/c.jpg","videoUrl":"{{BASE}}/media/b.mp4"}]},
	{"runSummary":true}]`

func TestStoriesApifyPicksTheLinkedStory(t *testing.T) {
	runs := 0
	input := ""
	server := newFakeApify(t, fakeStoriesBody, &runs, &input)
	extractor := &InstagramStoriesApifyExtractor{tempDir: t.TempDir(), actor: "a~b", api: fakeApifyClient(server)}

	media, err := extractor.ExtractMedia(context.Background(), "https://www.instagram.com/stories/nasa/4007835059386000871/?utm_source=ig_story_item_share")

	if (err != nil) || (len(media) != 1) || (media[0].Type != MediaTypeVideo) {
		t.Fatalf("expected the video story, got %+v %v", media, err)
	}

	if (strings.Contains(media[0].Caption, "@nasa") == false) || (strings.Contains(input, `"usernames":["nasa"]`) == false) {
		t.Fatalf("unexpected caption or input: %q %s", media[0].Caption, input)
	}
}

func TestStoriesApifyFailsForAnExpiredStory(t *testing.T) {
	runs := 0
	input := ""
	server := newFakeApify(t, fakeStoriesBody, &runs, &input)
	extractor := &InstagramStoriesApifyExtractor{tempDir: t.TempDir(), actor: "a~b", api: fakeApifyClient(server)}

	_, err := extractor.ExtractMedia(context.Background(), "https://www.instagram.com/stories/nasa/999/")

	if (err == nil) || (strings.Contains(err.Error(), "not available") == false) {
		t.Fatalf("expected a not-available error, got %v", err)
	}
}

func TestStoriesApifyRejectsHighlightsWithoutARun(t *testing.T) {
	runs := 0
	input := ""
	server := newFakeApify(t, fakeStoriesBody, &runs, &input)
	extractor := &InstagramStoriesApifyExtractor{tempDir: t.TempDir(), actor: "a~b", api: fakeApifyClient(server)}

	_, err := extractor.ExtractMedia(context.Background(), "https://www.instagram.com/stories/highlights/17900000000000000/")

	if (err == nil) || (runs != 0) {
		t.Fatalf("expected a refusal without a paid run, got runs=%d err=%v", runs, err)
	}
}

func TestApifyMethodsAreOffWithoutToken(t *testing.T) {
	sd := &SmartDownloader{}

	_, ytErr := sd.youtubeApifyExtract(context.Background(), "https://www.youtube.com/shorts/jNQXAC9IVRw")
	_, storyErr := sd.storiesApifyExtract(context.Background(), "https://www.instagram.com/stories/nasa/1/")

	if (errors.Is(ytErr, errApifyNotConfigured) == false) || (errors.Is(storyErr, errApifyNotConfigured) == false) {
		t.Fatalf("expected not-configured errors, got %v / %v", ytErr, storyErr)
	}
}

func TestYouTubeApifyRetriesATooLargeShortAt360p(t *testing.T) {
	runs := 0
	input := ""
	server := newFakeApify(t, `[{"title":"t","downloadUrl":"{{BASE}}/media/v.mp4","status":"success","fileSizeBytes":59273387}]`, &runs, &input)
	extractor := &YouTubeApifyExtractor{tempDir: t.TempDir(), actor: "a~b", api: fakeApifyClient(server)}

	_, err := extractor.ExtractMedia(context.Background(), "https://www.youtube.com/shorts/aqz-KE-bpKQ")

	if (errors.Is(err, errYouTubeTooLarge) == false) || (runs != 2) || (strings.Contains(input, `"quality":"360"`) == false) {
		t.Fatalf("expected one 360p retry then too-large, got runs=%d err=%v input=%s", runs, err, input)
	}
}

func TestYouTubeApifySkipsNonShortsWithoutARun(t *testing.T) {
	runs := 0
	input := ""
	server := newFakeApify(t, `[]`, &runs, &input)
	extractor := &YouTubeApifyExtractor{tempDir: t.TempDir(), actor: "a~b", api: fakeApifyClient(server)}

	_, channelErr := extractor.ExtractMedia(context.Background(), "https://youtube.com/channel/UCx/community")
	_, err := extractor.ExtractMedia(context.Background(), "https://www.youtube.com/watch?v=jNQXAC9IVRw")

	if (channelErr == nil) || (err == nil) || (runs != 0) {
		t.Fatalf("expected a refusal without a paid run, got runs=%d err=%v", runs, err)
	}
}

func TestYouTubeApifyRetriesOnTheActorsFileTooLarge(t *testing.T) {
	runs := 0
	input := ""
	server := newFakeApify(t, `[{"status":"failed","errorCode":"FILE_TOO_LARGE"}]`, &runs, &input)
	extractor := &YouTubeApifyExtractor{tempDir: t.TempDir(), actor: "a~b", api: fakeApifyClient(server)}

	_, err := extractor.ExtractMedia(context.Background(), "https://www.youtube.com/shorts/aqz-KE-bpKQ")

	if (err == nil) || (runs != 2) || (strings.Contains(input, `"quality":"360"`) == false) {
		t.Fatalf("expected a 360p retry, got runs=%d err=%v", runs, err)
	}
}

func TestApifyRunIsBoundedByTheCallersDeadline(t *testing.T) {
	runs := 0
	input := ""
	server := newFakeApify(t, `[]`, &runs, &input)
	client := fakeApifyClient(server)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := client.runSync(ctx, "a~b", map[string]string{}, youtubeApifyRunTimeout, 0.01)

	if (errors.Is(err, context.DeadlineExceeded) == false) || (runs != 0) {
		t.Fatalf("expected no run when the deadline leaves no time, got runs=%d err=%v", runs, err)
	}

	timeout := apifyRunTimeoutFor(context.Background(), youtubeApifyRunTimeout)

	if timeout != youtubeApifyRunTimeout {
		t.Fatalf("without a deadline the run timeout must stay %v, got %v", youtubeApifyRunTimeout, timeout)
	}
}

func TestApifyBudgetCountsRunsInFlight(t *testing.T) {
	runs := 0
	input := ""
	server := newFakeApify(t, `[]`, &runs, &input)
	client := fakeApifyClient(server)
	t.Setenv("APIFY_MONTHLY_BUDGET_USD", "0.30")

	first := client.reserveBudget(context.Background(), 0.15)
	second := client.reserveBudget(context.Background(), 0.15)
	releaseApifyBudget(0.15)

	if (first != nil) || (errors.Is(second, errApifyBudget) == false) {
		t.Fatalf("expected the second in-flight run to be refused, got %v / %v", first, second)
	}
}

func TestApifyBudgetFailsClosedOnAMalformedAnswer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":{"current":{}}}`)
	}))
	defer server.Close()

	err := fakeApifyClient(server).reserveBudget(context.Background(), 0.01)

	if (err == nil) || (isPaidBudgetError(err) == true) {
		t.Fatalf("expected a lookup failure, got %v", err)
	}
}

func TestYtDlpPlatformMatchesTwitterByHostOnly(t *testing.T) {
	if (ytDlpPlatform("https://x.com/user/status/1") != "twitter") || (ytDlpPlatform("https://mobile.twitter.com/u/status/1") != "twitter") {
		t.Fatalf("twitter hosts must match")
	}

	if ytDlpPlatform("https://www.netflix.com/watch/1") == "twitter" {
		t.Fatalf("a URL containing x.com must not count as Twitter")
	}
}
