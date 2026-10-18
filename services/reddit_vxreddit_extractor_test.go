package services

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"html"
	"image"
	"image/jpeg"
	"image/png"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
)

const (
	vxredditTestTextURL    = "https://www.reddit.com/r/explainlikeimfive/comments/1x1cmbg/eli5_how_is_it_that_charcoal_which_is_wood_thats/"
	vxredditTestGalleryURL = "https://www.reddit.com/r/nasa/comments/1x3lza5/fb_marketplace_nasa_find/"
	vxredditTestLinkURL    = "https://www.reddit.com/r/todayilearned/comments/1wq5b9i/til_in_1846_a_dead_snail_was_glued_to_an_index/"
	vxredditTestGIFURL     = "https://www.reddit.com/r/gifs/comments/1x8d6k4/siblings/"
	vxredditTestVideoURL   = "https://www.reddit.com/r/oddlysatisfying/comments/1x2t6a6/the_very_efficient_manner_this_pig_drinks_water/"
)

// vxredditTestJPEG and vxredditTestGIF are comfortably above
// vxredditMinMediaSize.
var (
	vxredditTestJPEG = bytes.Repeat([]byte("j"), int(vxredditMinMediaSize)+1024)
	vxredditTestGIF  = append([]byte("GIF89a"), bytes.Repeat([]byte("g"), int(vxredditMinMediaSize)+1024)...)
)

// vxredditTestServer serves a vxreddit page on every path, and the media of
// every host (the page names real media hosts) through the media handler on
// a TLS server that mediaClient dials for any address.
type vxredditTestServer struct {
	server      *httptest.Server
	media       *httptest.Server
	mediaClient *http.Client
	mu          sync.Mutex
	requests    []string
}

// mediaRequests returns the "host/path" of every media request.
func (s *vxredditTestServer) mediaRequests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return slices.Clone(s.requests)
}

func serveVxreddit(t *testing.T, page string, media http.HandlerFunc) *vxredditTestServer {
	t.Helper()

	result := &vxredditTestServer{}
	result.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(page))
	}))
	t.Cleanup(result.server.Close)
	result.media = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		result.mu.Lock()
		result.requests = append(result.requests, r.Host+r.URL.Path)
		result.mu.Unlock()

		media(w, r)
	}))
	t.Cleanup(result.media.Close)
	mediaAddress := result.media.Listener.Addr().String()
	dialer := &net.Dialer{}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, mediaAddress)
		},
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	result.mediaClient = &http.Client{Transport: transport}

	return result
}

// serveVxredditMedia answers like i.redd.it and vxreddit's video renderer.
func serveVxredditMedia(w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.HasSuffix(r.URL.Path, ".gif"):
		w.Header().Set("Content-Type", "image/gif")
		_, _ = w.Write(vxredditTestGIF)
	case strings.HasSuffix(r.URL.Path, ".mp4"):
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write(vxredditTestJPEG)
	default:
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(vxredditTestJPEG)
	}
}

func vxredditFixture(t *testing.T, name string) string {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join("testdata", name))

	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}

	return string(raw)
}

func newTestVxredditExtractor(t *testing.T, server *vxredditTestServer) *RedditVxredditExtractor {
	probeCaptureAdminAlerts(t)
	probeForgetTrackerState(t, redditVxredditServiceName)

	return &RedditVxredditExtractor{tempDir: t.TempDir(), base: server.server.URL, vxredditHost: "vxreddit.com", mediaHTTPClient: server.mediaClient}
}

func TestParseVxredditPageReadsEveryPostShape(t *testing.T) {
	cases := []struct {
		fixture string
		url     string
		check   func(t *testing.T, post vxredditPost)
	}{
		{fixture: "vxreddit_text.html", url: vxredditTestTextURL, check: func(t *testing.T, post vxredditPost) {
			if (post.Author != "Sc4tt3r_") || (post.Subreddit != "explainlikeimfive") || (strings.HasPrefix(post.Title, "ELI5: How is it that charcoal, which is wood that's been burnt") == false) {
				t.Errorf("author, subreddit or title: %+v", post)
			}

			if strings.HasPrefix(post.Body, `Like it's just wood, how does burning wood then produce a "new thing"`) == false {
				t.Errorf("body %q", post.Body)
			}
		}},
		{fixture: "vxreddit_title_only.html", url: "https://www.reddit.com/r/AskReddit/comments/1wpn40w/x/", check: func(t *testing.T, post vxredditPost) {
			if (strings.HasPrefix(post.Title, "Napoleon said, 'Never interrupt your enemy") == false) || (post.Body != "") {
				t.Errorf("title-only post read as %+v", post)
			}
		}},
		{fixture: "vxreddit_link.html", url: vxredditTestLinkURL, check: func(t *testing.T, post vxredditPost) {
			if (post.LinkURL != "https://en.wikipedia.org/wiki/Eremina_desertorum") || (post.Body != "") || (len(post.Images) != 0) {
				t.Errorf("link post read as %+v", post)
			}

			if strings.Contains(post.PreviewImage, "external-preview.redd.it/jyL_b_naQ8AHoGA5fJ1eH8ispcYUp60iMkzxveRtQFE.jpeg?auto=webp&s=") == false {
				t.Errorf("preview %q", post.PreviewImage)
			}
		}},
		{fixture: "vxreddit_gallery.html", url: vxredditTestGalleryURL, check: func(t *testing.T, post vxredditPost) {
			if (len(post.Images) != 19) || (strings.HasSuffix(post.Images[0], "i.redd.it/iwmunmut3xuh1.jpg") == false) || (strings.HasSuffix(post.Images[18], "i.redd.it/a4pv5sn34xuh1.jpg") == false) {
				t.Errorf("gallery images %v", post.Images)
			}
		}},
		{fixture: "vxreddit_gif.html", url: vxredditTestGIFURL, check: func(t *testing.T, post vxredditPost) {
			if (len(post.Images) != 1) || (strings.HasSuffix(post.Images[0], ".gif") == false) || (post.Title != "Siblings") {
				t.Errorf("gif post read as %+v", post)
			}
		}},
		{fixture: "vxreddit_video.html", url: vxredditTestVideoURL, check: func(t *testing.T, post vxredditPost) {
			if (strings.Contains(post.VideoURL, "/redditvideo.mp4?video_url=") == false) || (len(post.Images) != 0) || (post.PreviewImage == "") {
				t.Errorf("video post read as %+v", post)
			}
		}},
	}

	for _, testCase := range cases {
		t.Run(testCase.fixture, func(t *testing.T) {
			page := vxredditFixture(t, testCase.fixture)

			testCase.check(t, parseVxredditPage(page, testCase.url))
		})
	}
}

func TestVxredditTextPostIsOneMessageWithTitleAndBody(t *testing.T) {
	server := serveVxreddit(t, vxredditFixture(t, "vxreddit_text.html"), serveVxredditMedia)
	extractor := newTestVxredditExtractor(t, server)

	media, err := extractor.ExtractMedia(context.Background(), vxredditTestTextURL)

	if (err != nil) || (len(media) != 1) || (media[0].Type != MediaTypeText) || (media[0].Filename != "") {
		t.Fatalf("want 1 text item, got %d %v", len(media), err)
	}

	want := "🟠 r/explainlikeimfive • u/Sc4tt3r_\n\nELI5: How is it that charcoal, which is wood that&#39;s been burnt, burn hotter than wood\n\nLike it&#39;s just wood"

	if strings.HasPrefix(media[0].Caption, want) == false {
		t.Errorf("caption %q", media[0].Caption)
	}

	if len(server.mediaRequests()) != 0 {
		t.Errorf("a text post downloaded %v", server.mediaRequests())
	}
}

func TestVxredditTitleOnlyPostIsItsTitle(t *testing.T) {
	server := serveVxreddit(t, vxredditFixture(t, "vxreddit_title_only.html"), serveVxredditMedia)
	extractor := newTestVxredditExtractor(t, server)

	media, err := extractor.ExtractMedia(context.Background(), "https://www.reddit.com/r/AskReddit/comments/1wpn40w/x/")

	if (err != nil) || (len(media) != 1) {
		t.Fatalf("want 1 text item, got %d %v", len(media), err)
	}

	want := "🟠 r/AskReddit • u/l1x6m-\n\nNapoleon said, &#39;Never interrupt your enemy when he is making a mistake.&#39; What’s a perfect real-life example of this?"

	if media[0].Caption != want {
		t.Errorf("caption %q", media[0].Caption)
	}
}

// vxredditTextPage is a vxreddit text post page with the given body.
func vxredditTextPage(title, body string) string {
	return fmt.Sprintf(`<meta property="og:site_name" content="u/someone on r/test - ⬆️ 1 | 💬 1" />
<meta property="og:title" content="%s" />
<meta property="og:description" content="%s" />`, html.EscapeString(title), html.EscapeString(body))
}

func TestVxredditTextPostEscapesHTMLAndMarkdown(t *testing.T) {
	page := vxredditTextPage("1 < 2 & 3", "**Bold** <script>&amp;")
	server := serveVxreddit(t, page, serveVxredditMedia)
	extractor := newTestVxredditExtractor(t, server)

	media, err := extractor.ExtractMedia(context.Background(), "https://www.reddit.com/r/test/comments/abc/x/")

	if (err != nil) || (len(media) != 1) {
		t.Fatalf("want 1 text item, got %d %v", len(media), err)
	}

	want := "🟠 r/test • u/someone\n\n1 &lt; 2 &amp; 3\n\nBold &lt;script&gt;&amp;amp;"

	if media[0].Caption != want {
		t.Errorf("caption %q, want %q", media[0].Caption, want)
	}
}

func TestVxredditTextPostFitsTheMessageLimit(t *testing.T) {
	postURL := "https://www.reddit.com/r/test/comments/abc/x/"
	body := strings.Repeat("Ж & ", 2000)
	server := serveVxreddit(t, vxredditTextPage("Long", body), serveVxredditMedia)
	extractor := newTestVxredditExtractor(t, server)

	media, err := extractor.ExtractMedia(context.Background(), postURL)

	if (err != nil) || (len(media) != 1) {
		t.Fatalf("want 1 text item, got %d %v", len(media), err)
	}

	message := media[0].Caption + FormatOriginalLink(postURL)
	length := telegramTextLength(message)

	if (length > telegramMessageLimit) || (length < (telegramMessageLimit - 10)) {
		t.Errorf("message is %d units long, want just under %d", length, telegramMessageLimit)
	}

	if strings.HasSuffix(media[0].Caption, redditTruncationMarker) == false {
		t.Errorf("cut body must end with the marker: %q", media[0].Caption[len(media[0].Caption)-20:])
	}
}

func TestVxredditGallerySendsEveryImageInOrder(t *testing.T) {
	server := serveVxreddit(t, vxredditFixture(t, "vxreddit_gallery.html"), serveVxredditMedia)
	extractor := newTestVxredditExtractor(t, server)

	media, err := extractor.ExtractMedia(context.Background(), vxredditTestGalleryURL)

	if (err != nil) || (len(media) != 19) {
		t.Fatalf("want 19 photos, got %d %v", len(media), err)
	}

	if strings.HasPrefix(media[0].Caption, "🟠 r/nasa • u/Zissouu\n\nFB marketplace Nasa Find\n\n🔗 <a href=") == false {
		t.Errorf("first caption %q", media[0].Caption)
	}

	for i, item := range media {
		_, statErr := os.Stat(item.Filename)
		wantSuffix := fmt.Sprintf("_%d.jpg", (i + 1))

		if (item.Type != MediaTypePhoto) || (statErr != nil) || (strings.HasSuffix(item.Filename, wantSuffix) == false) {
			t.Errorf("item %d: %+v %v", i, item, statErr)
		}

		if (i > 0) && (strings.HasPrefix(item.Caption, "🔗 <a href=") == false) {
			t.Errorf("item %d caption %q, want only the original link", i, item.Caption)
		}
	}
}

func TestVxredditGalleryKeepsTheImagesThatDownloaded(t *testing.T) {
	failing := func(w http.ResponseWriter, r *http.Request) {
		isBroken := strings.HasSuffix(r.URL.Path, "/s98zhput3xuh1.jpg")

		if isBroken == true {
			w.WriteHeader(http.StatusNotFound)
		}

		if isBroken == false {
			serveVxredditMedia(w, r)
		}
	}
	server := serveVxreddit(t, vxredditFixture(t, "vxreddit_gallery.html"), failing)
	extractor := newTestVxredditExtractor(t, server)

	media, err := extractor.ExtractMedia(context.Background(), vxredditTestGalleryURL)

	if (err != nil) || (len(media) != 18) {
		t.Fatalf("want 18 photos, got %d %v", len(media), err)
	}

	if strings.Contains(media[0].Caption, "FB marketplace Nasa Find") == false {
		t.Errorf("first caption %q", media[0].Caption)
	}
}

func TestVxredditGIFIsAnAnimation(t *testing.T) {
	server := serveVxreddit(t, vxredditFixture(t, "vxreddit_gif.html"), serveVxredditMedia)
	extractor := newTestVxredditExtractor(t, server)

	media, err := extractor.ExtractMedia(context.Background(), vxredditTestGIFURL)

	if (err != nil) || (len(media) != 1) {
		t.Fatalf("want 1 item, got %d %v", len(media), err)
	}

	if (media[0].Type != MediaTypeAnimation) || (strings.HasSuffix(media[0].Filename, ".gif") == false) || (IsGIFFile(media[0].Filename) == false) {
		t.Errorf("want a .gif animation, got %+v", media[0])
	}

	if strings.HasPrefix(media[0].Caption, "🟠 r/gifs • u/_RoBy_90\n\nSiblings") == false {
		t.Errorf("caption %q", media[0].Caption)
	}
}

func TestVxredditVideoPostSendsTheVideo(t *testing.T) {
	server := serveVxreddit(t, vxredditFixture(t, "vxreddit_video.html"), serveVxredditMedia)
	extractor := newTestVxredditExtractor(t, server)

	media, err := extractor.ExtractMedia(context.Background(), vxredditTestVideoURL)

	if (err != nil) || (len(media) != 1) || (media[0].Type != MediaTypeVideo) {
		t.Fatalf("want 1 video, got %d %v", len(media), err)
	}

	requested := server.mediaRequests()

	if (len(requested) != 1) || (requested[0] != "vxreddit.com/redditvideo.mp4") {
		t.Errorf("want only the video downloaded, got %v", requested)
	}
}

func TestVxredditLinkPostSendsThePreviewWithTitleAndLink(t *testing.T) {
	server := serveVxreddit(t, vxredditFixture(t, "vxreddit_link.html"), serveVxredditMedia)
	extractor := newTestVxredditExtractor(t, server)

	media, err := extractor.ExtractMedia(context.Background(), vxredditTestLinkURL)

	if (err != nil) || (len(media) != 1) || (media[0].Type != MediaTypePhoto) {
		t.Fatalf("want the preview photo, got %d %v", len(media), err)
	}

	want := "🟠 r/todayilearned • u/OperationSuch5054\n\nTIL In 1846, a dead snail was glued to an index card at the British Museum."

	if (strings.HasPrefix(media[0].Caption, want) == false) || (strings.Contains(media[0].Caption, "\n\nhttps://en.wikipedia.org/wiki/Eremina_desertorum\n\n🔗 <a href=") == false) {
		t.Errorf("caption %q", media[0].Caption)
	}
}

func TestVxredditLinkPostWithoutItsPreviewIsText(t *testing.T) {
	missing := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}
	server := serveVxreddit(t, vxredditFixture(t, "vxreddit_link.html"), missing)
	extractor := newTestVxredditExtractor(t, server)

	media, err := extractor.ExtractMedia(context.Background(), vxredditTestLinkURL)

	if (err != nil) || (len(media) != 1) || (media[0].Type != MediaTypeText) || (media[0].Filename != "") {
		t.Fatalf("want 1 text item, got %d %v", len(media), err)
	}

	if strings.HasSuffix(media[0].Caption, "the remaining 2 years in a jar eating cabbage.\n\nhttps://en.wikipedia.org/wiki/Eremina_desertorum") == false {
		t.Errorf("caption %q", media[0].Caption)
	}
}

func TestVxredditThumbnailOnlyPostIsLeftToTheOtherMethods(t *testing.T) {
	page := `<meta property="og:site_name" content="u/someone on r/test - ⬆️ 1 | 💬 1" />
<meta property="og:title" content="A video vxreddit could not resolve" />
<meta property="og:image" content="https://external-preview.redd.it/thumb.png" />`
	server := serveVxreddit(t, page, serveVxredditMedia)
	extractor := newTestVxredditExtractor(t, server)

	media, err := extractor.ExtractMedia(context.Background(), "https://www.reddit.com/r/test/comments/abc/x/")

	if (err == nil) || (len(media) != 0) {
		t.Fatalf("want a failure, got %d item(s) %v", len(media), err)
	}
}

func TestBuildVxredditURLKeepsPathAndQuery(t *testing.T) {
	extractor := &RedditVxredditExtractor{base: "https://vxreddit.com"}
	cases := map[string]string{
		"https://www.reddit.com/r/aww/comments/1wpz4e4/x/":      "https://vxreddit.com/r/aww/comments/1wpz4e4/x/",
		"https://old.reddit.com/r/aww/comments/1wpz4e4/x/":      "https://vxreddit.com/r/aww/comments/1wpz4e4/x/",
		"https://www.reddit.com/r/ukraine_dev/s/YPgdlPVkgi?a=1": "https://vxreddit.com/r/ukraine_dev/s/YPgdlPVkgi?a=1",
		"https://www.reddit.com/comments/1wzcjw8":               "https://vxreddit.com/comments/1wzcjw8",
	}

	for redditURL, want := range cases {
		got, err := extractor.buildVxredditURL(redditURL)

		if (err != nil) || (got != want) {
			t.Errorf("%s: got %q %v, want %q", redditURL, got, err, want)
		}
	}

	if _, err := extractor.buildVxredditURL("https://notreddit.com/r/x/"); err == nil {
		t.Error("a non-Reddit host must be refused")
	}
}

func TestRedditStrategyUsesTheJSONAPIOnlyWithProxies(t *testing.T) {
	cases := []struct {
		enabled string
		proxies string
		want    []ExtractionMethod
	}{
		{enabled: "true", proxies: "", want: []ExtractionMethod{MethodRedditVxreddit, MethodYtDlp}},
		{enabled: "false", proxies: "http://proxy:1", want: []ExtractionMethod{MethodRedditVxreddit, MethodYtDlp}},
		{enabled: "true", proxies: "http://proxy:1", want: []ExtractionMethod{MethodRedditVxreddit, MethodRedditAPI, MethodYtDlp}},
	}

	for _, testCase := range cases {
		t.Setenv("ENABLE_PROXIES", testCase.enabled)
		t.Setenv("YTDLP_PROXIES", testCase.proxies)

		strategy := GetExtractionStrategy(vxredditTestTextURL)

		if (slices.Equal(strategy.Methods, testCase.want) == false) || (strategy.Sequential == false) {
			t.Errorf("proxies %q on=%s: methods %v, want %v", testCase.proxies, testCase.enabled, strategy.Methods, testCase.want)
		}
	}
}

func TestVxredditErrorPageIsNoPost(t *testing.T) {
	page := `<meta property="og:title" content="vxReddit" />
<meta property="og:url" content="https://www.reddit.com/comments/zzzzzzz9" />
<meta property="og:description" content="Failed to get data from Reddit" />`
	server := serveVxreddit(t, page, serveVxredditMedia)
	extractor := newTestVxredditExtractor(t, server)

	media, err := extractor.ExtractMedia(context.Background(), "https://www.reddit.com/r/aww/comments/zzzzzzz9/nope/")

	if (err == nil) || (len(media) != 0) || (strings.Contains(err.Error(), "Failed to get data from Reddit") == false) {
		t.Fatalf("want errVxredditNoPost, got %d item(s) %v", len(media), err)
	}
}

// vxredditImagePage is a vxreddit image post page with one og:image.
func vxredditImagePage(imageURL string) string {
	return fmt.Sprintf(`<meta property="og:site_name" content="u/someone on r/test - ⬆️ 1 | 💬 1" />
<meta property="og:title" content="An image" />
<meta property="og:image" content="%s" />`, imageURL)
}

func TestVxredditRefusesMediaOffTheMediaHosts(t *testing.T) {
	imageURLs := []string{
		"https://images.example.com/cat.jpg",
		"https://127.0.0.1/latest/meta-data/",
		"http://i.redd.it/cat.jpg",
		"https://i.redd.it.example.com/cat.jpg",
	}

	for _, imageURL := range imageURLs {
		t.Run(imageURL, func(t *testing.T) {
			server := serveVxreddit(t, vxredditImagePage(imageURL), serveVxredditMedia)
			extractor := newTestVxredditExtractor(t, server)

			media, err := extractor.ExtractMedia(context.Background(), "https://www.reddit.com/r/test/comments/abc/x/")

			if (errors.Is(err, errVxredditOffCDN) == false) || (len(media) != 0) {
				t.Fatalf("want errVxredditOffCDN, got %d item(s) %v", len(media), err)
			}

			if len(server.mediaRequests()) != 0 {
				t.Errorf("requested %v", server.mediaRequests())
			}
		})
	}
}

func TestVxredditRefusesARedirectOffTheMediaHosts(t *testing.T) {
	targets := []string{"https://internal.example/latest/meta-data/iam", "http://i.redd.it/plain.jpg"}

	for _, target := range targets {
		t.Run(target, func(t *testing.T) {
			redirecting := func(w http.ResponseWriter, r *http.Request) {
				if r.Host == "i.redd.it" {
					http.Redirect(w, r, target, http.StatusFound)
				}

				if r.Host != "i.redd.it" {
					serveVxredditMedia(w, r)
				}
			}
			server := serveVxreddit(t, vxredditImagePage("https://i.redd.it/cat.jpg"), redirecting)
			extractor := newTestVxredditExtractor(t, server)

			media, err := extractor.ExtractMedia(context.Background(), "https://www.reddit.com/r/test/comments/abc/x/")

			if (errors.Is(err, errVxredditOffCDN) == false) || (len(media) != 0) {
				t.Fatalf("want errVxredditOffCDN, got %d item(s) %v", len(media), err)
			}

			if slices.Equal(server.mediaRequests(), []string{"i.redd.it/cat.jpg"}) == false {
				t.Errorf("requested %v, want only the image", server.mediaRequests())
			}
		})
	}
}

func TestVxredditPreviewImageIsJudgedByItsHost(t *testing.T) {
	cases := map[string]bool{
		"https://external-preview.redd.it/thumb.png?auto=webp":        true,
		"https://images.example.com/x.jpg?u=external-preview.redd.it": false,
		"https://i.redd.it/external-preview.redd.it.jpg":              false,
	}

	for imageURL, want := range cases {
		if got := isVxredditPreviewImage(imageURL); got != want {
			t.Errorf("%s: got %t, want %t", imageURL, got, want)
		}
	}
}

func TestBreaksSendPhotoDimensionsAppliesTelegramsRules(t *testing.T) {
	cases := []struct {
		width  int
		height int
		want   bool
	}{
		{width: 1100, height: 9500, want: true},
		{width: 5000, height: 5001, want: true},
		{width: 100, height: 2001, want: true},
		{width: 2001, height: 100, want: true},
		{width: 100, height: 2000, want: false},
		{width: 2000, height: 3000, want: false},
	}

	for _, testCase := range cases {
		if got := breaksSendPhotoDimensions(testCase.width, testCase.height); got != testCase.want {
			t.Errorf("%dx%d: got %t, want %t", testCase.width, testCase.height, got, testCase.want)
		}
	}
}

func writeTestPNG(t *testing.T, path string, width, height int) {
	t.Helper()

	file, err := os.Create(path)

	if err != nil {
		t.Fatal(err)
	}

	defer file.Close()

	picture := image.NewGray(image.Rect(0, 0, width, height))

	if err := png.Encode(file, picture); err != nil {
		t.Fatal(err)
	}
}

// vxredditTestRenderer is a host of vxreddit's video renderer.
const vxredditTestRenderer = "abc123.lambda-url.us-east-1.on.aws"

// serveVxredditRenderChain answers like vxreddit's video chain:
// vxreddit.com -> renderer -> renders.vxreddit.com; i.redd.it redirects to
// the renderer.
func serveVxredditRenderChain(w http.ResponseWriter, r *http.Request) {
	switch r.Host {
	case "vxreddit.com":
		http.Redirect(w, r, "https://"+vxredditTestRenderer+"/?video_url=x", http.StatusTemporaryRedirect)
	case "i.redd.it":
		http.Redirect(w, r, "https://"+vxredditTestRenderer+"/?video_url=x", http.StatusFound)
	case vxredditTestRenderer:
		http.Redirect(w, r, "https://renders.vxreddit.com/axrwmetm5quh1.mp4", http.StatusTemporaryRedirect)
	default:
		serveVxredditMedia(w, r)
	}
}

func TestVxredditVideoFollowsTheRendererRedirects(t *testing.T) {
	server := serveVxreddit(t, vxredditFixture(t, "vxreddit_video.html"), serveVxredditRenderChain)
	extractor := newTestVxredditExtractor(t, server)

	media, err := extractor.ExtractMedia(context.Background(), vxredditTestVideoURL)

	if (err != nil) || (len(media) != 1) || (media[0].Type != MediaTypeVideo) {
		t.Fatalf("want 1 video, got %d %v", len(media), err)
	}

	want := []string{"vxreddit.com/redditvideo.mp4", vxredditTestRenderer + "/", "renders.vxreddit.com/axrwmetm5quh1.mp4"}

	if slices.Equal(server.mediaRequests(), want) == false {
		t.Errorf("requested %v, want %v", server.mediaRequests(), want)
	}
}

func TestVxredditRendererIsRefusedUnlessVxredditRedirectsThere(t *testing.T) {
	imageURLs := []string{"https://" + vxredditTestRenderer + "/cat.jpg", "https://i.redd.it/cat.jpg"}

	for _, imageURL := range imageURLs {
		server := serveVxreddit(t, vxredditImagePage(imageURL), serveVxredditRenderChain)
		extractor := newTestVxredditExtractor(t, server)

		_, err := extractor.ExtractMedia(context.Background(), "https://www.reddit.com/r/test/comments/abc/x/")
		requested := server.mediaRequests()

		if (errors.Is(err, errVxredditOffCDN) == false) || (slices.Contains(requested, vxredditTestRenderer+"/") == true) {
			t.Errorf("%s: want errVxredditOffCDN without a renderer request, got %v %v", imageURL, requested, err)
		}
	}
}

func TestShrinkPhotoHandlesAnOddSizedJPEG(t *testing.T) {
	if findFFmpegPath() == "" {
		t.Skip("ffmpeg not available")
	}

	path := filepath.Join(t.TempDir(), "photo.jpg")
	file, err := os.Create(path)

	if err != nil {
		t.Fatal(err)
	}

	picture := image.NewRGBA(image.Rect(0, 0, 539, 655))

	if err := jpeg.Encode(file, picture, nil); err != nil {
		t.Fatal(err)
	}

	file.Close()

	shrunkPath, err := shrinkPhoto(path)

	if err != nil {
		t.Fatalf("shrink: %v", err)
	}

	if _, statErr := os.Stat(shrunkPath); statErr != nil {
		t.Errorf("no shrunk photo: %v", statErr)
	}
}

// vxredditEntityPattern matches what may follow "&" in an escaped caption.
var vxredditEntityPattern = regexp.MustCompile(`&(amp|lt|gt|quot|#39);`)

// requireFittedCaption checks a caption Telegram accepts as it is: within
// the caption limit by bytes and by Telegram's count, the original link
// intact at its end, and every "&" starting a whole entity.
func requireFittedCaption(t *testing.T, caption string) {
	t.Helper()

	withoutEntities := vxredditEntityPattern.ReplaceAllString(caption, "")

	if (len(caption) > TelegramCaptionLimitBytes) || (telegramTextLength(caption) > TelegramCaptionLimitBytes) {
		t.Errorf("caption of %d bytes, %d Telegram units: %q", len(caption), telegramTextLength(caption), caption)
	}

	if (strings.HasSuffix(caption, "</a>") == false) || (strings.Count(caption, "🔗 <a href=") != 1) {
		t.Errorf("original link not intact: %q", caption)
	}

	if strings.Contains(withoutEntities, "&") == true {
		t.Errorf("a split entity in %q", caption)
	}
}

func vxredditLongPage(title, video, link string) string {
	page := fmt.Sprintf(`<meta property="og:site_name" content="u/автор on r/ukraina - ⬆️ 1 | 💬 1" />
<meta property="og:title" content="%s" />`, html.EscapeString(title))

	if video != "" {
		page = fmt.Sprintf(`%s
<meta property="og:video" content="%s" />`, page, video)
	}

	if link != "" {
		page = fmt.Sprintf(`%s
<meta property="og:description" content="%s" />`, page, html.EscapeString(link))
	}

	return page
}

func TestVxredditVideoCaptionFitsALongCyrillicTitle(t *testing.T) {
	title := strings.Repeat("Довгий заголовок & <тест> ", 60)
	page := vxredditLongPage(title, "https://vxreddit.com/redditvideo.mp4?video_url=x", "")
	server := serveVxreddit(t, page, serveVxredditMedia)
	extractor := newTestVxredditExtractor(t, server)

	media, err := extractor.ExtractMedia(context.Background(), "https://www.reddit.com/r/ukraina/comments/abc/x/")

	if (err != nil) || (len(media) != 1) || (media[0].Type != MediaTypeVideo) {
		t.Fatalf("want 1 video, got %d %v", len(media), err)
	}

	requireFittedCaption(t, media[0].Caption)

	if (strings.HasPrefix(media[0].Caption, "🟠 r/ukraina • u/автор\n\nДовгий заголовок &amp; &lt;тест&gt; ") == false) || (strings.Contains(media[0].Caption, "…\n\n🔗 <a href=") == false) {
		t.Errorf("want the cut title with the marker, got %q", media[0].Caption)
	}
}

func TestVxredditLinkCaptionFitsAnAmpersandHeavyURL(t *testing.T) {
	link := "https://example.com/search?" + strings.Repeat("a=1&b=2&", 150)
	title := strings.Repeat("Посилання на статтю ", 40)
	page := vxredditLongPage(title, "", link)
	missing := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}
	server := serveVxreddit(t, page, missing)
	extractor := newTestVxredditExtractor(t, server)
	postURL := "https://www.reddit.com/r/ukraina/comments/abc/x/"

	media, err := extractor.ExtractMedia(context.Background(), postURL)

	if (err != nil) || (len(media) != 1) || (media[0].Type != MediaTypeText) {
		t.Fatalf("want 1 text item, got %d %v", len(media), err)
	}

	withLink := fmt.Sprintf("%s%s", media[0].Caption, FormatOriginalLink(postURL))
	requireFittedCaption(t, withLink)

	if strings.Contains(media[0].Caption, "Посилання") == true {
		t.Errorf("a link this long leaves no room for the title: %q", media[0].Caption)
	}
}

func TestVxredditLinkCaptionCutsTheTitleBeforeTheLink(t *testing.T) {
	link := "https://example.com/search?" + strings.Repeat("a=1&b=2&", 20)
	title := strings.Repeat("Посилання на статтю & ", 40)
	post := vxredditPost{Subreddit: "ukraina", Author: "автор", Title: title, LinkURL: link}
	postURL := "https://www.reddit.com/r/ukraina/comments/abc/x/"

	caption := vxredditLinkCaption(post, postURL)
	items := []*MediaInfo{{Type: MediaTypePhoto, Filename: "photo.jpg", Caption: caption}}
	appendOriginalURL(items, postURL)

	requireFittedCaption(t, items[0].Caption)

	escapedLink := html.EscapeString(link)

	if strings.Contains(items[0].Caption, fmt.Sprintf("…\n\n%s\n\n🔗 <a href=", escapedLink)) == false {
		t.Errorf("want the cut title, then the whole link: %q", items[0].Caption)
	}
}
