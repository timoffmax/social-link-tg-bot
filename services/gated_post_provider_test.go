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
)

const apifyTestToken = "test-token"

// newApifyTestServer fakes the actor endpoint and the CDN it points to. The
// handler records the decoded run input in *gotInput.
func newApifyTestServer(t *testing.T, status int, shortcode string, gotInput *apifyRunInput) *httptest.Server {
	var server *httptest.Server

	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		isMedia := strings.HasPrefix(r.URL.Path, "/media/")

		if isMedia == true {
			w.Write([]byte("media-bytes"))
		}

		if isMedia == false {
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
	return &ApifyInstagramProvider{
		tempDir: t.TempDir(),
		token:   apifyTestToken,
		actor:   "intropix~instagram-posts-reels-scraper",
		apiBase: server.URL,
		client:  server.Client(),
	}
}

func TestApifyFetchPostDownloadsEverySlideAsSpoiler(t *testing.T) {
	var input apifyRunInput
	server := newApifyTestServer(t, http.StatusCreated, "DeYsKEDjbGB", &input)
	defer server.Close()

	media, err := newApifyTestProvider(t, server).FetchPost("https://www.instagram.com/p/DeYsKEDjbGB/?obrf=abc")

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

	expectedCaption := "Averona &amp; Co (@averona.space)\n\n<tg-spoiler>Hello &lt;world&gt;</tg-spoiler>"

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

	_, err := newApifyTestProvider(t, server).FetchPost("https://www.instagram.com/p/DeYsKEDjbGB/")

	if (err == nil) || (strings.Contains(err.Error(), "apify status 402") == false) {
		t.Fatalf("expected a 402 error, got %v", err)
	}
}

func TestApifyFetchPostFailsWhenPostIsMissing(t *testing.T) {
	var input apifyRunInput
	server := newApifyTestServer(t, http.StatusCreated, "SomeOtherPost", &input)
	defer server.Close()

	_, err := newApifyTestProvider(t, server).FetchPost("https://www.instagram.com/p/DeYsKEDjbGB/")

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
		item := apifyPostItem{Username: "averona.space", FullName: "МАНІКЮР|ПЕДИКЮР|Виноградар🇺🇦|Варшавський +", Caption: raw}
		media := []*MediaInfo{{Type: "photo", Filename: "x.jpg", Caption: gatedPostCaption(item, url)}}
		appendOriginalURL(media, url)
		caption := media[0].Caption

		if len(caption) > telegramCaptionLimitBytes {
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
	item := apifyPostItem{Username: "someone"}
	caption := gatedPostCaption(item, "https://www.instagram.com/p/X/")

	if (strings.Contains(caption, "tg-spoiler") == true) || (strings.Contains(caption, "@someone") == false) {
		t.Fatalf("unexpected caption: %q", caption)
	}
}
