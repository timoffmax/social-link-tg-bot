package services

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// threadsEmbedFixture mirrors the embed: a replied-to parent block first, then
// the post's own "Full" block with text, media and a quoted post.
const threadsEmbedFixture = `<div class="OuterContainer OuterContainerFollowButton"><a class="HeaderLink"><span>parent.user</span></a>
<span class="BodyTextContainer"><span>parent text</span></span><img class="img" src="{{BASE}}/parent-t51.2-15.jpg"></div>
<div class="OuterContainer OuterContainerFull OuterContainerFollowButton"><img class="img" src="{{BASE}}/avatar-t51.2-19.jpg">
<a href="https://www.threads.com/&#064;fox.com" class="HeaderLink"><span>fox.com</span></a>
<span class="BodyTextContainer"><span>Line one &amp; more<br>line two</span></span>
<div class="MediaContainer"><img class="img" src="{{BASE}}/a-t51.82787-15.webp?x=1&amp;y=2"><video><source src="{{BASE}}/b.mp4"></video><img class="img" src="{{BASE}}/c-t51.82787-15.jpg"></div>
<div class="QuotePostContainer"><video><source src="{{BASE}}/quoted.mp4"></video></div>
<div class="PostDateContainer">Oct 15</div></div>`

const threadsTextFixture = `<div class="OuterContainer OuterContainerFull"><a class="HeaderLink"><span>writer</span></a>
<span class="BodyTextContainer"><span>only &lt;text&gt;</span></span><div class="PostDateContainer"></div></div>`

const threadsVxFixture = `<meta name="twitter:creator" content="@zuck"><meta property="og:description" content="Hi &amp; bye">
<meta property="og:video:secure_url" content="{{BASE}}/v.mp4"><meta property="og:image" content="{{BASE}}/poster-t51.2-15.jpg">
<meta name="twitter:image" content="{{BASE}}/poster-t51.2-15.jpg">`

// serveThreads serves the embed and vxthreads pages, media files, and a
// /t/ short link redirect; it records requested paths.
func serveThreads(t *testing.T, embed string, requested *[]string) *httptest.Server {
	var server *httptest.Server

	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*requested = append(*requested, r.URL.Path)
		isEmbed := strings.HasSuffix(r.URL.Path, "/embed")
		isShort := strings.HasPrefix(r.URL.Path, "/t/") || strings.HasPrefix(r.URL.Path, "/share/")
		isVx := strings.HasPrefix(r.URL.Path, "/vx/")

		if isEmbed == true {
			fmt.Fprint(w, strings.ReplaceAll(embed, "{{BASE}}", server.URL))
		}

		if isShort == true {
			w.Header().Set("Location", "/@fox.com/post/CODE123")
			w.WriteHeader(http.StatusMovedPermanently)
		}

		if isVx == true {
			fmt.Fprint(w, strings.ReplaceAll(threadsVxFixture, "{{BASE}}", server.URL))
		}

		if (isEmbed == false) && (isShort == false) && (isVx == false) {
			w.Write([]byte("media-bytes"))
		}
	}))
	t.Cleanup(server.Close)

	return server
}

func newTestThreadsExtractor(t *testing.T, server *httptest.Server) *ThreadsExtractor {
	return &ThreadsExtractor{tempDir: t.TempDir(), pageBase: server.URL, vxBase: server.URL + "/vx"}
}

func TestThreadsEmbedReadsTheOwnPostInOrderWithoutQuotedMedia(t *testing.T) {
	var requested []string
	server := serveThreads(t, threadsEmbedFixture, &requested)

	media, err := newTestThreadsExtractor(t, server).ExtractEmbed(context.Background(), "https://www.threads.com/@fox.com/post/CODE123?xmt=1")

	if (err != nil) || (len(media) != 3) {
		t.Fatalf("expected 3 items, got %d %v", len(media), err)
	}

	if (media[0].Type != MediaTypePhoto) || (media[1].Type != MediaTypeVideo) || (media[2].Type != MediaTypePhoto) {
		t.Fatalf("unexpected order: %s %s %s", media[0].Type, media[1].Type, media[2].Type)
	}

	isCaptionOK := strings.Contains(media[0].Caption, `<a href="https://www.threads.com/@fox.com">@fox.com</a>`) && strings.Contains(media[0].Caption, "Line one &amp; more\nline two")

	if (isCaptionOK == false) || (strings.Contains(media[0].Caption, "parent") == true) {
		t.Fatalf("unexpected caption: %q", media[0].Caption)
	}

	for _, path := range requested {
		if strings.Contains(path, "quoted") || strings.Contains(path, "parent") || strings.Contains(path, "avatar") {
			t.Fatalf("must not download %s", path)
		}
	}
}

func TestThreadsEmbedTextOnlyPostIsOneTextItem(t *testing.T) {
	var requested []string
	server := serveThreads(t, threadsTextFixture, &requested)

	media, err := newTestThreadsExtractor(t, server).ExtractEmbed(context.Background(), "https://www.threads.net/@writer/post/ABCDEF1")

	if (err != nil) || (len(media) != 1) || (media[0].Type != MediaTypeText) || (strings.Contains(media[0].Caption, "only &lt;text&gt;") == false) {
		t.Fatalf("expected one escaped text item, got %+v %v", media, err)
	}
}

func TestThreadsEmbedResolvesShortLinks(t *testing.T) {
	var requested []string
	server := serveThreads(t, threadsEmbedFixture, &requested)

	_, err := newTestThreadsExtractor(t, server).ExtractEmbed(context.Background(), "https://www.threads.com/t/CODE123")

	if (err != nil) || (strings.Contains(strings.Join(requested, " "), "/@fox.com/post/CODE123/embed") == false) {
		t.Fatalf("expected the resolved embed, got %v %v", requested, err)
	}
}

func TestThreadsEmbedFailsOnALoginWall(t *testing.T) {
	var requested []string
	server := serveThreads(t, "<html>Log in</html>", &requested)

	_, err := newTestThreadsExtractor(t, server).ExtractEmbed(context.Background(), "https://www.threads.com/@a/post/ABCDEF1")

	if err == nil {
		t.Fatalf("expected an error for a page without the post")
	}
}

func TestThreadsVxthreadsKeepsTheVideoNotItsPoster(t *testing.T) {
	var requested []string
	server := serveThreads(t, "", &requested)

	media, err := newTestThreadsExtractor(t, server).ExtractVxthreads(context.Background(), "https://www.threads.com/@zuck/post/ABCDEF1")

	if (err != nil) || (len(media) != 1) || (media[0].Type != MediaTypeVideo) || (strings.Contains(media[0].Caption, "Hi &amp; bye") == false) {
		t.Fatalf("expected one video with caption, got %+v %v", media, err)
	}
}

func TestThreadsURLRoutingAndFixedLink(t *testing.T) {
	url := "https://www.threads.com/@fox.com/post/CODE123?xmt=1"
	strategy := GetExtractionStrategy(url)

	if (IsThreadsURL(url) == false) || (strategy.Platform != ThreadsPlatform) || (platformDisplayName(url) != "Threads") {
		t.Fatalf("a Threads link with x.com in the username must route to Threads, got %q / %q", strategy.Platform, platformDisplayName(url))
	}

	if IsThreadsURL("https://x.com/threads.com") == true {
		t.Fatalf("an X link must not count as Threads")
	}

	if BuildThreadsFixedURL(url) != "https://vxthreads.com/@fox.com/post/CODE123" {
		t.Fatalf("unexpected fixed link: %q", BuildThreadsFixedURL(url))
	}
}

func TestThreadsEmbedPhotoPostDoesNotTakeTheQuotedText(t *testing.T) {
	page := `<div class="OuterContainer OuterContainerFull"><a class="HeaderLink"><span>poster</span></a>
<div class="MediaContainer"><img class="img" src="{{BASE}}/own-t51.82787-15.jpg"></div>
<div class="QuotePostContainer"><span class="BodyTextContainer"><span>quoted words</span></span></div></div>`
	var requested []string
	server := serveThreads(t, page, &requested)

	media, err := newTestThreadsExtractor(t, server).ExtractEmbed(context.Background(), "https://www.threads.com/@poster/post/ABCDEF1")

	if (err != nil) || (len(media) != 1) || (strings.Contains(media[0].Caption, "quoted words") == true) {
		t.Fatalf("expected the photo without the quoted text, got %+v %v", media, err)
	}
}

func TestThreadsVxthreadsSkipsAvatarImages(t *testing.T) {
	post := applyThreadsVxTag(threadsPost{}, "og:image", "https://scontent.cdninstagram.com/v/t51.2885-19/avatar.jpg")

	if len(post.Media) != 0 {
		t.Fatalf("an avatar must not become post media: %+v", post.Media)
	}
}

func TestThreadsCaptionFitsTheLimitWithoutSplittingEntities(t *testing.T) {
	url := "https://www.threads.com/@writer/post/ABCDEF1"
	post := threadsPost{Author: "writer", Text: strings.Repeat("rock & roll ", 200)}
	media := []*MediaInfo{{Type: MediaTypePhoto, Filename: "x.jpg", Caption: threadsCaption(post, 0, url)}}
	appendOriginalURL(media, url)
	caption := media[0].Caption

	if (len(caption) > TelegramCaptionLimitBytes) || (strings.Contains(caption, "View the original") == false) {
		t.Fatalf("caption is %d bytes or lost its link", len(caption))
	}

	if strings.Count(caption, "&") != strings.Count(caption, "&amp;") {
		t.Fatalf("an escaped entity was split: %q", caption[len(caption)-200:])
	}
}

func TestThreadsVxthreadsKeepsARealPhotoNextToAVideo(t *testing.T) {
	media := dropThreadsPoster([]threadsMedia{
		{Type: MediaTypeVideo, URL: "v.mp4"},
		{Type: MediaTypePhoto, URL: "poster.jpg"},
		{Type: MediaTypePhoto, URL: "photo.jpg"},
	}, "poster.jpg")

	if (len(media) != 2) || (media[1].URL != "photo.jpg") {
		t.Fatalf("expected the video and the real photo, got %+v", media)
	}
}

func TestThreadsEmbedWithNothingFailsOverToTheNextMethod(t *testing.T) {
	page := `<div class="OuterContainer OuterContainerFull"><a class="HeaderLink"><span>someone</span></a><div class="PostDateContainer"></div></div>`
	var requested []string
	server := serveThreads(t, page, &requested)

	_, err := newTestThreadsExtractor(t, server).ExtractEmbed(context.Background(), "https://www.threads.com/@someone/post/ABCDEF1")

	if err == nil {
		t.Fatalf("an embed with neither text nor media must fail")
	}
}

func TestThreadsVxthreadsUsesTheShortLinkWhenThreadsCantResolveIt(t *testing.T) {
	var requested []string
	server := serveThreads(t, "", &requested)
	extractor := newTestThreadsExtractor(t, server)
	extractor.pageBase = "http://127.0.0.1:1"

	media, err := extractor.ExtractVxthreads(context.Background(), "https://www.threads.com/t/CODE123")

	if (err != nil) || (len(media) != 1) || (strings.Contains(strings.Join(requested, " "), "/vx/t/CODE123") == false) {
		t.Fatalf("expected the mirror to get the short link, got %v %v", requested, err)
	}
}

func TestThreadsEmbedResolvesShareLinks(t *testing.T) {
	var requested []string
	server := serveThreads(t, threadsEmbedFixture, &requested)

	media, err := newTestThreadsExtractor(t, server).ExtractEmbed(context.Background(), "https://www.threads.com/share/BAV_IiebyI/")

	if (err != nil) || (len(media) != 3) || (strings.Contains(strings.Join(requested, " "), "/@fox.com/post/CODE123/embed") == false) {
		t.Fatalf("expected the share link resolved to the post, got %v %v", requested, err)
	}
}

func TestThreadsRedirectPostPathReadsOnlyThePath(t *testing.T) {
	cases := map[string]string{
		"/@a.b/post/CODE1?xmt=1":                     "@a.b/post/CODE1",
		"https://www.threads.com/@a.b/post/CODE1":    "@a.b/post/CODE1",
		"/login?next=/@a.b/post/CODE1":               "",
		"https://www.threads.com/login?next=%2F@a.b": "",
	}

	for location, expected := range cases {
		if threadsRedirectPostPath(location) != expected {
			t.Fatalf("%s: expected %q, got %q", location, expected, threadsRedirectPostPath(location))
		}
	}
}

func TestClassifyThreadsCall(t *testing.T) {
	cases := []struct {
		name        string
		call        threadsCall
		wantOutcome serviceOutcome
		wantCode    string
	}{
		{"429", threadsCall{fetchErr: &httpStatusError{StatusCode: http.StatusTooManyRequests}, parseCode: ServiceErrorParse}, serviceOutcomeFailure, ServiceErrorHTTP429},
		{"5xx", threadsCall{fetchErr: &httpStatusError{StatusCode: http.StatusServiceUnavailable}, parseCode: ServiceErrorParse}, serviceOutcomeFailure, ServiceErrorHTTP5xx},
		{"404 missing post", threadsCall{fetchErr: &httpStatusError{StatusCode: http.StatusNotFound}, parseCode: ServiceErrorParse}, serviceOutcomeNone, ""},
		{"410 gone post", threadsCall{fetchErr: &httpStatusError{StatusCode: http.StatusGone}, parseCode: ServiceErrorParse}, serviceOutcomeNone, ""},
		{"403", threadsCall{fetchErr: &httpStatusError{StatusCode: http.StatusForbidden}, parseCode: ServiceErrorParse}, serviceOutcomeNone, ""},
		{"own timeout", threadsCall{fetchErr: fmt.Errorf("get: %w", context.DeadlineExceeded), parseCode: ServiceErrorParse}, serviceOutcomeFailure, ServiceErrorTimeout},
		{"transport", threadsCall{fetchErr: &url.Error{Op: "Get", URL: "https://x", Err: errors.New("refused")}, parseCode: ServiceErrorParse}, serviceOutcomeFailure, ServiceErrorTransport},
		{"caller deadline", threadsCall{ctxErr: context.DeadlineExceeded, fetchErr: fmt.Errorf("get: %w", context.DeadlineExceeded), parseCode: ServiceErrorParse}, serviceOutcomeNone, ""},
		{"caller cancel on a 5xx", threadsCall{ctxErr: context.Canceled, fetchErr: &httpStatusError{StatusCode: http.StatusBadGateway}, parseCode: ServiceErrorParse}, serviceOutcomeNone, ""},
		{"embed changed", threadsCall{parseErr: errors.New("no Threads post in the embed"), parseCode: ServiceErrorParse}, serviceOutcomeFailure, ServiceErrorParse},
		{"mirror tags gone", threadsCall{parseErr: errors.New("no Threads post on the vxthreads page"), parseCode: ServiceErrorWrongContent}, serviceOutcomeFailure, ServiceErrorWrongContent},
		{"post not available", threadsCall{parseErr: errThreadsPostUnavailable, parseCode: ServiceErrorParse}, serviceOutcomeNone, ""},
		{"embed without text or media", threadsCall{parseErr: errThreadsPostEmpty, parseCode: ServiceErrorParse}, serviceOutcomeFailure, ServiceErrorParse},
		{"quote-only post", threadsCall{parseErr: errThreadsPostQuoteOnly, parseCode: ServiceErrorParse}, serviceOutcomeNone, ""},
		{"mirror login page", threadsCall{parseErr: errThreadsVxLoginPage, parseCode: ServiceErrorWrongContent}, serviceOutcomeFailure, ServiceErrorWrongContent},
		{"post", threadsCall{parseCode: ServiceErrorParse}, serviceOutcomeSuccess, ""},
	}

	for _, c := range cases {
		outcome, code := classifyThreadsCall(c.call)

		if (outcome != c.wantOutcome) || (code != c.wantCode) {
			t.Errorf("%s: got %d %q, want %d %q", c.name, outcome, code, c.wantOutcome, c.wantCode)
		}
	}
}

// threadsEmptyEmbedFixture is an embed with an author but no text or media.
const threadsEmptyEmbedFixture = `<div class="OuterContainer OuterContainerFull"><a class="HeaderLink"><span>writer</span></a>
<div class="PostDateContainer"></div></div>`

// threadsQuoteOnlyEmbedFixture is an embed of a post that only quotes another.
const threadsQuoteOnlyEmbedFixture = `<div class="OuterContainer OuterContainerFull"><a class="HeaderLink"><span>writer</span></a>
<div class="QuotePostContainer"><span>quoted text</span></div>
<div class="PostDateContainer"></div></div>`

// threadsTextVxFixture is a vxthreads page of a text-only post.
const threadsTextVxFixture = `<meta name="twitter:creator" content="@zuck"><meta property="og:description" content="Hi">`

func TestThreadsCallsReachTheTracker(t *testing.T) {
	unavailableEmbed := vxredditFixture(t, "threads_embed_unavailable.html")
	loginMirror := vxredditFixture(t, "threads_vx_login.html")
	cases := []struct {
		name        string
		service     string
		status      int
		body        string
		wantOutcome serviceOutcome
		wantCode    string
	}{
		{"embed 5xx is a failure", threadsEmbedServiceName, http.StatusServiceUnavailable, "", serviceOutcomeFailure, ServiceErrorHTTP5xx},
		{"embed 404 is not counted", threadsEmbedServiceName, http.StatusNotFound, "", serviceOutcomeNone, ""},
		{"changed embed is a parse failure", threadsEmbedServiceName, http.StatusOK, "<html>Log in</html>", serviceOutcomeFailure, ServiceErrorParse},
		{"embed without text or media is a parse failure", threadsEmbedServiceName, http.StatusOK, threadsEmptyEmbedFixture, serviceOutcomeFailure, ServiceErrorParse},
		{"quote-only embed is not counted", threadsEmbedServiceName, http.StatusOK, threadsQuoteOnlyEmbedFixture, serviceOutcomeNone, ""},
		{"embed post is a success", threadsEmbedServiceName, http.StatusOK, threadsTextFixture, serviceOutcomeSuccess, ""},
		{"unavailable post embed is not counted", threadsEmbedServiceName, http.StatusOK, unavailableEmbed, serviceOutcomeNone, ""},
		{"mirror 5xx is a failure", vxthreadsServiceName, http.StatusBadGateway, "", serviceOutcomeFailure, ServiceErrorHTTP5xx},
		{"mirror 404 is not counted", vxthreadsServiceName, http.StatusNotFound, "", serviceOutcomeNone, ""},
		{"mirror without tags is wrong content", vxthreadsServiceName, http.StatusOK, "<html></html>", serviceOutcomeFailure, ServiceErrorWrongContent},
		{"mirror post is a success", vxthreadsServiceName, http.StatusOK, threadsTextVxFixture, serviceOutcomeSuccess, ""},
		{"mirror login page is wrong content", vxthreadsServiceName, http.StatusOK, loginMirror, serviceOutcomeFailure, ServiceErrorWrongContent},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			newAlertRecorder(t)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(c.status)
				fmt.Fprint(w, c.body)
			}))
			t.Cleanup(server.Close)
			extractor := newTestThreadsExtractor(t, server)
			postURL := "https://www.threads.com/@a/post/ABCDEF1"

			if c.service == threadsEmbedServiceName {
				_, _ = extractor.ExtractEmbed(context.Background(), postURL)
			}

			if c.service == vxthreadsServiceName {
				_, _ = extractor.ExtractVxthreads(context.Background(), postURL)
			}

			assertTracked(t, c.service, c.wantOutcome, c.wantCode)
		})
	}
}

func TestThreadsUnresolvedLinkIsNotCounted(t *testing.T) {
	newAlertRecorder(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)

	_, _ = newTestThreadsExtractor(t, server).ExtractEmbed(context.Background(), "https://www.threads.com/t/CODE123")

	assertTracked(t, threadsEmbedServiceName, serviceOutcomeNone, "")
}

func TestThreadsEmbedOfAnUnavailablePostFails(t *testing.T) {
	page := vxredditFixture(t, "threads_embed_unavailable.html")

	_, err := parseThreadsEmbed(page)

	if errors.Is(err, errThreadsPostUnavailable) == false {
		t.Fatalf("got %v", err)
	}
}

func TestThreadsVxthreadsLoginPageIsNotAPost(t *testing.T) {
	page := vxredditFixture(t, "threads_vx_login.html")

	_, err := parseThreadsVxPage(page)

	if errors.Is(err, errThreadsVxLoginPage) == false {
		t.Fatalf("got %v", err)
	}
}
