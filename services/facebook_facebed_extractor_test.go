package services

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	facebedTestPhotoURL = "https://www.facebook.com/photo/?fbid=29713410154913540&set=a.130784570269490"
	facebedTestMultiURL = "https://www.facebook.com/permalink.php?story_fbid=pfbid0abc&id=100079744151043"
	facebedTestReelURL  = "https://www.facebook.com/reel/588109690402315"
)

// facebedTestImage is comfortably above redirectorMinMediaSize.
var facebedTestImage = bytes.Repeat([]byte("j"), int(redirectorMinMediaSize)+1024)

// facebedFixture reads a saved facebed page and points its media at base.
func facebedFixture(t *testing.T, name, mediaBase string) string {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join("testdata", name))

	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}

	return strings.ReplaceAll(string(raw), "{{MEDIA}}", mediaBase)
}

// facebedTestServer serves a facebed page for every path outside /v/ and
// /o1/, and media under them through mediaHandler. Pages are reached on
// 127.0.0.1 and media on localhost, so the off-host check sees two hosts.
type facebedTestServer struct {
	server    *httptest.Server
	mediaBase string
	mu        sync.Mutex
	requests  []*http.Request
}

func (s *facebedTestServer) requested() []*http.Request {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]*http.Request(nil), s.requests...)
}

func serveFacebed(t *testing.T, fixture string, mediaHandler http.HandlerFunc) *facebedTestServer {
	t.Helper()

	result := &facebedTestServer{}
	result.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		result.mu.Lock()
		result.requests = append(result.requests, r)
		result.mu.Unlock()

		isMedia := strings.HasPrefix(r.URL.Path, "/v/") || strings.HasPrefix(r.URL.Path, "/o1/")

		if isMedia == true {
			mediaHandler(w, r)
		}

		if isMedia == false {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(facebedFixture(t, fixture, result.mediaBase)))
		}
	}))
	t.Cleanup(result.server.Close)

	result.mediaBase = strings.Replace(result.server.URL, "127.0.0.1", "localhost", 1)

	return result
}

func serveFacebedImage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/jpeg")
	_, _ = w.Write(facebedTestImage)
}

func newTestFacebedExtractor(t *testing.T, server *facebedTestServer) *FacebookFacebedExtractor {
	return &FacebookFacebedExtractor{tempDir: t.TempDir(), base: server.server.URL, mediaDomain: "localhost", mediaScheme: "http"}
}

// isolateFacebedTracker keeps the extractor's outage reports away from the
// real Telegram API and from other tests.
func isolateFacebedTracker(t *testing.T) {
	probeCaptureAdminAlerts(t)
	probeForgetTrackerState(t, facebedServiceName)
}

func TestBuildFacebedPath(t *testing.T) {
	cases := []struct {
		name    string
		url     string
		want    string
		wantErr error
	}{
		{name: "photo with set", url: facebedTestPhotoURL, want: "/photo/?fbid=29713410154913540&set=a.130784570269490"},
		{name: "mobile photo.php drops tracking", url: "https://m.facebook.com/photo.php?fbid=1&set=a.2&mibextid=xyz&fbclid=abc", want: "/photo.php?fbid=1&set=a.2"},
		{name: "album path form is rewritten", url: "https://www.facebook.com/glukhovsky/photos/a.130784570269490/29713410154913540/", want: "/photo/?fbid=29713410154913540&set=a.130784570269490"},
		{name: "page photo tab set is rewritten", url: "https://www.facebook.com/LudusMagnusStudio/photos/pb.100064.-2207520000/1220500346751179/?type=3", want: "/photo/?fbid=1220500346751179&set=pb.100064.-2207520000"},
		{name: "photo path without set", url: "https://www.facebook.com/somepage/photos/1220500346751179", want: "/photo/?fbid=1220500346751179"},
		{name: "permalink keeps its ids", url: "https://web.facebook.com/permalink.php?story_fbid=pfbid0x&id=100&__cft__[0]=abc&__tn__=R", want: "/permalink.php?id=100&story_fbid=pfbid0x"},
		{name: "page post", url: "https://www.facebook.com/maltadaily.mt/posts/pfbid0RQi?rdid=1", want: "/maltadaily.mt/posts/pfbid0RQi"},
		{name: "group post", url: "https://www.facebook.com/groups/1495321534752609/posts/1796616717956421/", want: "/groups/1495321534752609/posts/1796616717956421/"},
		{name: "reel", url: "https://www.facebook.com/reel/588109690402315?fbclid=x", want: "/reel/588109690402315"},
		{name: "watch", url: "https://www.facebook.com/watch/?v=1481060365360701&ref=sharing", want: "/watch/?v=1481060365360701"},
		{name: "share link", url: "https://www.facebook.com/share/p/1B44qfVcyF/?mibextid=wwXIfr", want: "/share/p/1B44qfVcyF/"},
		{name: "group landing", url: "https://www.facebook.com/groups/364997627165697/", wantErr: errFacebookUnsupportedPath},
		{name: "group multi permalink", url: "https://www.facebook.com/groups/364997627165697/?multi_permalinks=1", wantErr: errFacebookUnsupportedPath},
		{name: "profile", url: "https://www.facebook.com/NASA", wantErr: errFacebookUnsupportedPath},
		{name: "profile.php", url: "https://www.facebook.com/profile.php?id=100", wantErr: errFacebookUnsupportedPath},
		{name: "site root", url: "https://www.facebook.com/", wantErr: errFacebookUnsupportedPath},
		{name: "album", url: "https://www.facebook.com/media/set/?set=a.130784570269490", wantErr: errFacebookUnsupportedPath},
		{name: "event", url: "https://www.facebook.com/events/123/", wantErr: errFacebookUnsupportedPath},
		{name: "marketplace", url: "https://www.facebook.com/marketplace/item/123/", wantErr: errFacebookUnsupportedPath},
	}

	for _, refused := range facebedRefusedURLs {
		cases = append(cases, struct {
			name    string
			url     string
			want    string
			wantErr error
		}{name: refused, url: refused, wantErr: errFacebookUnsupportedPath})
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := buildFacebedPath(testCase.url)

			if errors.Is(err, testCase.wantErr) == false {
				t.Fatalf("error %v, want %v", err, testCase.wantErr)
			}

			if got != testCase.want {
				t.Fatalf("path %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestBuildFacebedPathRefusesOtherHosts(t *testing.T) {
	_, err := buildFacebedPath("https://www.notfacebook.com/photo/?fbid=1")

	if err == nil {
		t.Fatal("a non-Facebook host must be refused")
	}
}

func TestFacebookStrategyOrdersMethodsByLinkShape(t *testing.T) {
	photoOnly := []ExtractionMethod{MethodFacebookFacebed}
	facebedFirst := []ExtractionMethod{MethodFacebookFacebed, MethodYtDlp}
	ytdlpFirst := []ExtractionMethod{MethodYtDlp, MethodFacebookFacebed}
	cases := []struct {
		url  string
		want []ExtractionMethod
	}{
		{url: facebedTestPhotoURL, want: photoOnly},
		{url: "https://m.facebook.com/photo.php?fbid=1", want: photoOnly},
		{url: "https://www.facebook.com/photo?fbid=1", want: photoOnly},
		{url: "https://www.facebook.com/glukhovsky/photos/a.1/2/", want: photoOnly},
		{url: "https://www.facebook.com/share/p/1B44qfVcyF/", want: facebedFirst},
		{url: "https://www.facebook.com/share/1GdMNgXLSG/", want: facebedFirst},
		{url: "https://www.facebook.com/page/posts/pfbid0x", want: facebedFirst},
		{url: "https://www.facebook.com/photography.page/posts/pfbid0x", want: facebedFirst},
		{url: facebedTestMultiURL, want: facebedFirst},
		{url: "https://www.facebook.com/story.php?story_fbid=1&id=2", want: facebedFirst},
		{url: "https://www.facebook.com/groups/1/posts/2/", want: facebedFirst},
		{url: "https://www.facebook.com/groups/1/posts/2/?next=/photo/?fbid=1", want: facebedFirst},
		{url: facebedTestReelURL, want: ytdlpFirst},
		{url: "https://www.facebook.com/watch/?v=1", want: ytdlpFirst},
		{url: "https://www.facebook.com/share/r/abc/", want: ytdlpFirst},
		{url: "https://www.facebook.com/share/v/abc/", want: ytdlpFirst},
		{url: "https://www.facebook.com/page/videos/271051965695290", want: ytdlpFirst},
		{url: "https://www.facebook.com/somethingelse/unknown/", want: ytdlpFirst},
	}

	for _, testCase := range cases {
		t.Run(testCase.url, func(t *testing.T) {
			strategy := GetExtractionStrategy(testCase.url)

			if (strategy.Platform != FacebookPlatform) || (strategy.Sequential == false) || (strategy.HasFixedLink == true) {
				t.Fatalf("unexpected strategy %+v", strategy)
			}

			if slices.Equal(strategy.Methods, testCase.want) == false {
				t.Fatalf("methods %v, want %v", strategy.Methods, testCase.want)
			}
		})
	}
}

func TestFacebookStrategyIsEmptyWhenFacebookIsDisabled(t *testing.T) {
	t.Setenv("ENABLE_FACEBOOK", "false")

	strategy := GetExtractionStrategy(facebedTestPhotoURL)

	if len(strategy.Methods) != 0 {
		t.Fatalf("disabled Facebook must get no methods, got %v", strategy.Methods)
	}
}

func TestParseFacebedPhotoPage(t *testing.T) {
	page := facebedFixture(t, "facebed_photo.html", "https://scontent.fhan5-1.fna.fbcdn.net")

	post, err := parseFacebedPage(page)

	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}

	if post.Author != "Dmitry Glukhovsky" {
		t.Errorf("author %q", post.Author)
	}

	if strings.HasPrefix(post.Text, "Месяц назад опубликовал «Регистрацию»") == false {
		t.Errorf("text %q", post.Text)
	}

	wantPhoto := "https://scontent.fhan5-1.fna.fbcdn.net/v/t51.82787-15/photo1_n.jpg?stp=dst-jpg_tt6&_nc_cat=101&oe=6ADA87BA"

	if (len(post.Photos) != 1) || (post.Photos[0] != wantPhoto) {
		t.Errorf("photos %v, want [%s]", post.Photos, wantPhoto)
	}

	if (post.HasVideo == true) || (checkFacebedPhotoPost(post) != nil) {
		t.Errorf("a photo post must pass the photo check")
	}
}

func TestParseFacebedMultiPhotoPageUnescapesAndDeduplicates(t *testing.T) {
	page := facebedFixture(t, "facebed_multi.html", "https://scontent.xx.fbcdn.net")

	post, err := parseFacebedPage(page)

	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}

	if post.Author != "Café & Bar <Saigon>" {
		t.Errorf("author %q", post.Author)
	}

	if post.Text != "Menu \"new\" & prices < 50k\nsecond line 'quoted'" {
		t.Errorf("text %q", post.Text)
	}

	if len(post.Photos) != 4 {
		t.Errorf("want 4 distinct photos, got %v", post.Photos)
	}
}

func TestParseFacebedVideoPageHasNoPhoto(t *testing.T) {
	page := facebedFixture(t, "facebed_video.html", "https://video.xx.fbcdn.net")

	post, err := parseFacebedPage(page)

	if (err != nil) || (post.HasVideo == false) {
		t.Fatalf("want a parsed video post, got %+v %v", post, err)
	}

	if errors.Is(checkFacebedPhotoPost(post), errFacebedNoPhoto) == false {
		t.Fatalf("a video post must fail the photo check")
	}
}

func TestParseFacebedLoginCardIsUnavailable(t *testing.T) {
	page := facebedFixture(t, "facebed_login.html", "")

	_, err := parseFacebedPage(page)

	if (errors.Is(err, errFacebedUnavailable) == false) || (errors.Is(err, errFacebedBroken) == true) {
		t.Fatalf("want the login card error, got %v", err)
	}
}

func TestParseFacebedUnknownPageIsBroken(t *testing.T) {
	_, err := parseFacebedPage("<!doctype html><title>502 Bad Gateway</title>")

	if errors.Is(err, errFacebedBroken) == false {
		t.Fatalf("want a broken-service error, got %v", err)
	}
}

func TestFacebedCaptionEscapesAndFitsTheCaptionLimit(t *testing.T) {
	// facebed caps og:description at 1024 characters.
	post := facebedPost{Author: "A & <B>", Text: strings.Repeat("x < y & \"z\" ", 85)}
	media := []*MediaInfo{{Type: MediaTypePhoto, Filename: "photo.jpg", Caption: facebedCaption(post, facebedTestPhotoURL)}}

	appendOriginalURL(media, facebedTestPhotoURL)

	caption := media[0].Caption

	if strings.HasPrefix(caption, "📘 A &amp; &lt;B&gt;\n\nx &lt; y &amp; &#34;z&#34;") == false {
		t.Fatalf("caption not escaped: %q", caption[:80])
	}

	if len(caption) > TelegramCaptionLimitBytes {
		t.Fatalf("caption is %d bytes, above %d", len(caption), TelegramCaptionLimitBytes)
	}

	if strings.HasSuffix(caption, "</a>") == false {
		t.Fatalf("original link missing: %q", caption[len(caption)-80:])
	}
}

func TestFacebedExtractorDownloadsThePhotoWithCaption(t *testing.T) {
	isolateFacebedTracker(t)
	server := serveFacebed(t, "facebed_photo.html", serveFacebedImage)

	media, err := newTestFacebedExtractor(t, server).ExtractMedia(context.Background(), facebedTestPhotoURL)

	if (err != nil) || (len(media) != 1) {
		t.Fatalf("want 1 photo, got %d %v", len(media), err)
	}

	if (media[0].Type != MediaTypePhoto) || (strings.HasSuffix(media[0].Filename, ".jpg") == false) || (media[0].URL != facebedTestPhotoURL) {
		t.Fatalf("unexpected media %+v", media[0])
	}

	content, readErr := os.ReadFile(media[0].Filename)

	if (readErr != nil) || (bytes.Equal(content, facebedTestImage) == false) {
		t.Fatalf("photo file not written: %v", readErr)
	}

	if strings.HasPrefix(media[0].Caption, "📘 Dmitry Glukhovsky\n\nМесяц назад") == false {
		t.Errorf("caption %q", media[0].Caption)
	}

	if strings.Contains(media[0].Caption, "View the original on Facebook</a>") == false {
		t.Errorf("caption without the original link: %q", media[0].Caption)
	}

	requests := server.requested()
	page := requests[0]

	if (page.URL.Path != "/photo/") || (page.URL.RawQuery != "fbid=29713410154913540&set=a.130784570269490") {
		t.Errorf("facebed asked for %s", page.URL.String())
	}

	if page.UserAgent() != redirectorBotUserAgent {
		t.Errorf("facebed page fetched with UA %q", page.UserAgent())
	}

	if (len(requests) != 2) || (strings.HasPrefix(requests[1].Host, "localhost:") == false) {
		t.Errorf("want one media request on the media host, got %d", len(requests))
	}

	if got := probeTrackerFailures(facebedServiceName); got != 0 {
		t.Errorf("a success recorded %d failure(s)", got)
	}
}

func TestFacebedExtractorDownloadsEveryDistinctPhoto(t *testing.T) {
	isolateFacebedTracker(t)
	server := serveFacebed(t, "facebed_multi.html", serveFacebedImage)

	media, err := newTestFacebedExtractor(t, server).ExtractMedia(context.Background(), facebedTestMultiURL)

	if (err != nil) || (len(media) != 4) {
		t.Fatalf("want 4 photos, got %d %v", len(media), err)
	}

	if strings.HasPrefix(media[0].Caption, "📘 Café &amp; Bar &lt;Saigon&gt;\n\n") == false {
		t.Errorf("first caption %q", media[0].Caption)
	}

	if strings.HasPrefix(media[1].Caption, "🔗 ") == false {
		t.Errorf("later photos carry only the link, got %q", media[1].Caption)
	}
}

func TestFacebedExtractorFailsWithoutAPhoto(t *testing.T) {
	cases := []struct {
		name    string
		fixture string
		url     string
		wantErr error
	}{
		{name: "login card", fixture: "facebed_login.html", url: facebedTestPhotoURL, wantErr: errFacebedLoginCard},
		{name: "video post", fixture: "facebed_video.html", url: facebedTestReelURL, wantErr: errFacebedNoPhoto},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			isolateFacebedTracker(t)
			server := serveFacebed(t, testCase.fixture, serveFacebedImage)

			media, err := newTestFacebedExtractor(t, server).ExtractMedia(context.Background(), testCase.url)

			if (errors.Is(err, testCase.wantErr) == false) || (len(media) != 0) {
				t.Fatalf("want %v, got %d items %v", testCase.wantErr, len(media), err)
			}

			if got := probeTrackerFailures(facebedServiceName); got != 0 {
				t.Errorf("a post without a photo is not an outage, tracker holds %d", got)
			}
		})
	}
}

// facebedRefusedURLs are refused before any request.
var facebedRefusedURLs = []string{
	"https://www.facebook.com/groups/364997627165697",
	"https://www.facebook.com/glukhovsky/photos/",
	"https://www.facebook.com/glukhovsky/photos/a.130784570269490/",
	"https://www.facebook.com/glukhovsky/photos/albums/130784570269490/",
	"https://www.facebook.com/photos/albums/130784570269490",
	"https://www.facebook.com/people/Some-Name/100064/",
}

func TestFacebedExtractorRefusesShapesWithoutARequest(t *testing.T) {
	for _, refused := range facebedRefusedURLs {
		t.Run(refused, func(t *testing.T) {
			isolateFacebedTracker(t)
			server := serveFacebed(t, "facebed_photo.html", serveFacebedImage)

			_, err := newTestFacebedExtractor(t, server).ExtractMedia(context.Background(), refused)

			if (errors.Is(err, errFacebookUnsupportedPath) == false) || (len(server.requested()) != 0) {
				t.Fatalf("want a refusal before any request, got %v after %d request(s)", err, len(server.requested()))
			}
		})
	}
}

func TestRequestedFacebookPostID(t *testing.T) {
	cases := []struct {
		url  string
		want string
	}{
		{url: facebedTestPhotoURL, want: "29713410154913540"},
		{url: "https://m.facebook.com/photo.php?fbid=11&set=a.2", want: "11"},
		{url: "https://www.facebook.com/glukhovsky/photos/a.130784570269490/29713410154913540/", want: "29713410154913540"},
		{url: "https://www.facebook.com/somepage/photos/1220500346751179", want: "1220500346751179"},
		{url: "https://www.facebook.com/story.php?story_fbid=457992036868931&id=100079744151043", want: "457992036868931"},
		{url: "https://www.facebook.com/maltadaily.mt/posts/1485418313406667", want: "1485418313406667"},
		{url: "https://www.facebook.com/PhucLong/posts/inspiring-workspace/122223830864155872/", want: "122223830864155872"},
		{url: "https://www.facebook.com/groups/1495321534752609/posts/1796616717956421/", want: "1796616717956421"},
		{url: facebedTestReelURL, want: "588109690402315"},
		{url: "https://www.facebook.com/SansoneDeejay/videos/271051965695290", want: "271051965695290"},
		{url: "https://www.facebook.com/maltadaily.mt/posts/pfbid0RQiyPS3ZKr", want: ""},
		{url: facebedTestMultiURL, want: ""},
		{url: "https://www.facebook.com/share/p/1B44qfVcyF/", want: ""},
		{url: "https://www.facebook.com/watch/?v=1481060365360701", want: ""},
	}

	for _, testCase := range cases {
		if got := requestedFacebookPostID(testCase.url); got != testCase.want {
			t.Errorf("requestedFacebookPostID(%q) = %q, want %q", testCase.url, got, testCase.want)
		}
	}
}

func TestFacebedExtractorRejectsAnotherPostWithoutAnOutage(t *testing.T) {
	// facebed_photo.html answers for photo fbid=29713410154913540.
	urls := []string{
		"https://www.facebook.com/photo/?fbid=2971341015491354",
		"https://www.facebook.com/photo.php?fbid=1",
		"https://www.facebook.com/glukhovsky/photos/a.29713410154913540/5/",
		"https://www.facebook.com/story.php?story_fbid=6&id=29713410154913540",
		"https://www.facebook.com/permalink.php?story_fbid=7&id=8",
		"https://www.facebook.com/glukhovsky/posts/9",
		"https://www.facebook.com/groups/29713410154913540/posts/10/",
		"https://www.facebook.com/reel/11",
		"https://www.facebook.com/glukhovsky/videos/12",
		// Ids og:url carries outside its post-id position: the set, words.
		"https://www.facebook.com/photo/?fbid=130784570269490",
		"https://www.facebook.com/glukhovsky/photos/a.1/130784570269490/",
		"https://www.facebook.com/glukhovsky/posts/photo",
		"https://www.facebook.com/glukhovsky/posts/www",
	}

	for _, postURL := range urls {
		t.Run(postURL, func(t *testing.T) {
			isolateFacebedTracker(t)
			server := serveFacebed(t, "facebed_photo.html", serveFacebedImage)

			media, err := newTestFacebedExtractor(t, server).ExtractMedia(context.Background(), postURL)

			if (errors.Is(err, errFacebedWrongPost) == false) || (errors.Is(err, errFacebedUnavailable) == false) || (len(media) != 0) {
				t.Fatalf("want the wrong-post error, got %d items %v", len(media), err)
			}

			if got := len(server.requested()); got != 1 {
				t.Errorf("want only the page request, got %d", got)
			}

			if got := probeTrackerFailures(facebedServiceName); got != 0 {
				t.Errorf("another post is not an outage, tracker holds %d", got)
			}
		})
	}
}

func TestFacebedExtractorAcceptsPostsWithoutAComparableID(t *testing.T) {
	urls := []string{
		"https://www.facebook.com/glukhovsky/posts/pfbid02Aggw4udS1d",
		"https://www.facebook.com/permalink.php?story_fbid=pfbid0abc&id=5",
		"https://www.facebook.com/share/p/1B44qfVcyF/",
	}

	for _, postURL := range urls {
		t.Run(postURL, func(t *testing.T) {
			isolateFacebedTracker(t)
			server := serveFacebed(t, "facebed_photo.html", serveFacebedImage)

			media, err := newTestFacebedExtractor(t, server).ExtractMedia(context.Background(), postURL)

			if (err != nil) || (len(media) != 1) {
				t.Fatalf("want 1 photo, got %d %v", len(media), err)
			}
		})
	}
}

func TestFacebedExtractorRejectsBadPhotosWithoutAnOutage(t *testing.T) {
	cases := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{
			name: "HTML instead of an image",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				_, _ = w.Write(facebedTestImage)
			},
		},
		{
			name: "video instead of an image",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "video/mp4")
				_, _ = w.Write(facebedTestImage)
			},
		},
		{
			name: "tiny body",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "image/jpeg")
				_, _ = w.Write(facebedTestImage[:512])
			},
		},
		{
			name: "above the photo limit",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "image/jpeg")
				_, _ = w.Write(bytes.Repeat([]byte("j"), int(facebedMaxPhotoSize)+1))
			},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			isolateFacebedTracker(t)
			server := serveFacebed(t, "facebed_photo.html", testCase.handler)
			extractor := newTestFacebedExtractor(t, server)

			media, err := extractor.ExtractMedia(context.Background(), facebedTestPhotoURL)

			if (err == nil) || (errors.Is(err, errFacebedBroken) == true) || (len(media) != 0) {
				t.Fatalf("want a per-post photo error, got %d items %v", len(media), err)
			}

			if got := probeTrackerFailures(facebedServiceName); got != 0 {
				t.Errorf("a bad photo is not a facebed outage, tracker holds %d", got)
			}

			leftovers, _ := os.ReadDir(extractor.tempDir)

			if len(leftovers) != 0 {
				t.Errorf("rejected photo left %d file(s) behind", len(leftovers))
			}
		})
	}
}

func TestFacebedExtractorRequestsNoPhotoOffTheMediaCDN(t *testing.T) {
	cases := []struct {
		name        string
		mediaDomain string
		mediaScheme string
	}{
		{name: "other host", mediaDomain: facebookMediaDomain, mediaScheme: "http"},
		{name: "plain http", mediaDomain: "localhost", mediaScheme: facebookMediaScheme},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			isolateFacebedTracker(t)
			server := serveFacebed(t, "facebed_photo.html", serveFacebedImage)
			extractor := newTestFacebedExtractor(t, server)
			extractor.mediaDomain = testCase.mediaDomain
			extractor.mediaScheme = testCase.mediaScheme

			_, err := extractor.ExtractMedia(context.Background(), facebedTestPhotoURL)

			if (err == nil) || (strings.Contains(err.Error(), "photo is not served from") == false) {
				t.Fatalf("want a media-host error, got %v", err)
			}

			if got := len(server.requested()); got != 1 {
				t.Errorf("want only the page request, got %d request(s)", got)
			}
		})
	}
}

func TestFacebedExtractorStopsAPhotoRedirectOffTheMediaCDN(t *testing.T) {
	isolateFacebedTracker(t)
	var server *facebedTestServer
	server = serveFacebed(t, "facebed_photo.html", func(w http.ResponseWriter, r *http.Request) {
		target := fmt.Sprintf("%s/internal", server.server.URL)
		http.Redirect(w, r, target, http.StatusFound)
	})

	_, err := newTestFacebedExtractor(t, server).ExtractMedia(context.Background(), facebedTestPhotoURL)

	if (err == nil) || (strings.Contains(err.Error(), "photo is not served from") == false) {
		t.Fatalf("want a media-host error on the redirect, got %v", err)
	}

	for _, request := range server.requested() {
		if request.URL.Path == "/internal" {
			t.Fatalf("the off-CDN redirect target was requested")
		}
	}
}

func TestFacebedExtractorCancelsAPhotoDownloadWithTheContext(t *testing.T) {
	isolateFacebedTracker(t)
	server := serveFacebed(t, "facebed_photo.html", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(facebedTestImage[:512])
		w.(http.Flusher).Flush()

		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()

	_, err := newTestFacebedExtractor(t, server).ExtractMedia(ctx, facebedTestPhotoURL)

	if (err == nil) || (time.Since(start) > 3*time.Second) {
		t.Fatalf("want the download cancelled at the deadline, got %v after %s", err, time.Since(start))
	}
}

func TestFacebedExtractorRejectsPhotosOnFacebedItself(t *testing.T) {
	isolateFacebedTracker(t)
	server := serveFacebed(t, "facebed_photo.html", serveFacebedImage)
	server.mediaBase = server.server.URL
	extractor := newTestFacebedExtractor(t, server)
	extractor.mediaDomain = "127.0.0.1"

	_, err := extractor.ExtractMedia(context.Background(), facebedTestPhotoURL)

	if (err == nil) || (strings.Contains(err.Error(), "no redirect off") == false) {
		t.Fatalf("want an off-host error, got %v", err)
	}
}

func TestFacebedExtractorReportsAnErrorStatus(t *testing.T) {
	isolateFacebedTracker(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad gateway", http.StatusBadGateway)
	}))
	t.Cleanup(server.Close)
	extractor := &FacebookFacebedExtractor{tempDir: t.TempDir(), base: server.URL, mediaDomain: "localhost"}

	_, err := extractor.ExtractMedia(context.Background(), facebedTestPhotoURL)

	if errors.Is(err, errFacebedBroken) == false {
		t.Fatalf("want a broken-service error, got %v", err)
	}

	if got := probeTrackerFailures(facebedServiceName); got != 1 {
		t.Errorf("tracker holds %d failure(s), want 1", got)
	}
}

func TestFacebedExtractorCountsCDNFailuresAsAnOutage(t *testing.T) {
	cases := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{
			name: "CDN refuses",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "image/jpeg")
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write(facebedTestImage)
			},
		},
		{
			name: "CDN drops the connection",
			handler: func(w http.ResponseWriter, r *http.Request) {
				conn, _, _ := w.(http.Hijacker).Hijack()
				_ = conn.Close()
			},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			isolateFacebedTracker(t)
			server := serveFacebed(t, "facebed_photo.html", testCase.handler)
			extractor := newTestFacebedExtractor(t, server)

			media, err := extractor.ExtractMedia(context.Background(), facebedTestPhotoURL)

			if (errors.Is(err, errFacebedBroken) == false) || (len(media) != 0) {
				t.Fatalf("want a broken-service error, got %d items %v", len(media), err)
			}

			if got := probeTrackerFailures(facebedServiceName); got != 1 {
				t.Errorf("tracker holds %d failure(s), want 1", got)
			}

			leftovers, _ := os.ReadDir(extractor.tempDir)

			if len(leftovers) != 0 {
				t.Errorf("failed photo left %d file(s) behind", len(leftovers))
			}
		})
	}
}

// facebedTestPhotoBody is the body served for photo pN, so tests can check
// the order of the downloaded files.
func facebedTestPhotoBody(path string) []byte {
	name := filepath.Base(path)

	return append([]byte(name), facebedTestImage...)
}

// serveFacebedSlowImage serves each photo after delay, p2 with failStatus
// when it is not 0.
func serveFacebedSlowImage(delay time.Duration, failStatus int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(delay):
		}

		w.Header().Set("Content-Type", "image/jpeg")

		if (failStatus != 0) && (filepath.Base(r.URL.Path) == "p2_n.jpg") {
			w.WriteHeader(failStatus)
		}

		_, _ = w.Write(facebedTestPhotoBody(r.URL.Path))
	}
}

func TestFacebedExtractorDownloadsPhotosInParallelInOrder(t *testing.T) {
	isolateFacebedTracker(t)
	delay := 400 * time.Millisecond
	server := serveFacebed(t, "facebed_multi.html", serveFacebedSlowImage(delay, 0))
	start := time.Now()

	media, err := newTestFacebedExtractor(t, server).ExtractMedia(context.Background(), facebedTestMultiURL)
	elapsed := time.Since(start)

	if (err != nil) || (len(media) != 4) {
		t.Fatalf("want 4 photos, got %d %v", len(media), err)
	}

	if elapsed > 3*delay {
		t.Errorf("4 photos of %s each took %s: not downloaded in parallel", delay, elapsed)
	}

	for i, item := range media {
		content, _ := os.ReadFile(item.Filename)
		want := fmt.Sprintf("p%d_n.jpg", (i + 1))

		if bytes.HasPrefix(content, []byte(want)) == false {
			t.Errorf("item %d holds %q, want %s", i, content[:8], want)
		}
	}
}

func TestFacebedExtractorSendsThePhotosThatDownloaded(t *testing.T) {
	isolateFacebedTracker(t)
	server := serveFacebed(t, "facebed_multi.html", serveFacebedSlowImage(10*time.Millisecond, http.StatusForbidden))

	media, err := newTestFacebedExtractor(t, server).ExtractMedia(context.Background(), facebedTestMultiURL)

	if (err != nil) || (len(media) != 3) {
		t.Fatalf("want 3 photos, got %d %v", len(media), err)
	}

	for i, want := range []string{"p1_n.jpg", "p3_n.jpg", "p4_n.jpg"} {
		content, _ := os.ReadFile(media[i].Filename)

		if bytes.HasPrefix(content, []byte(want)) == false {
			t.Errorf("item %d holds %q, want %s", i, content[:8], want)
		}
	}

	if strings.HasPrefix(media[0].Caption, "📘 Café &amp; Bar") == false {
		t.Errorf("the first sent photo must carry the caption, got %q", media[0].Caption)
	}

	if got := probeTrackerFailures(facebedServiceName); got != 0 {
		t.Errorf("a partly downloaded post is a success, tracker holds %d", got)
	}
}

func TestFacebedExtractorStopsSlowPhotosAtTheDeadline(t *testing.T) {
	isolateFacebedTracker(t)
	server := serveFacebed(t, "facebed_multi.html", serveFacebedSlowImage(5*time.Second, 0))
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()

	_, err := newTestFacebedExtractor(t, server).ExtractMedia(ctx, facebedTestMultiURL)

	if (err == nil) || (time.Since(start) > 3*time.Second) {
		t.Fatalf("want the downloads cancelled at the deadline, got %v after %s", err, time.Since(start))
	}

	if got := probeTrackerFailures(facebedServiceName); got != 0 {
		t.Errorf("the chain's deadline is not an outage, tracker holds %d", got)
	}
}

func TestCheckFacebedPostMatchesComparesTheAnsweredPostID(t *testing.T) {
	cases := []struct {
		name      string
		canonical string
		postID    string
		wantMatch bool
	}{
		{name: "photo fbid", canonical: "https://www.facebook.com/photo?fbid=29713410154913540&set=a.130784570269490", postID: "29713410154913540", wantMatch: true},
		{name: "photo set id", canonical: "https://www.facebook.com/photo?fbid=29713410154913540&set=a.130784570269490", postID: "130784570269490", wantMatch: false},
		{name: "story as page post", canonical: "https://www.facebook.com/Latribudesnacs/posts/457992036868931", postID: "457992036868931", wantMatch: true},
		{name: "page id of a page post", canonical: "https://www.facebook.com/61568885281801/posts/122120943914629509", postID: "61568885281801", wantMatch: false},
		{name: "permalink story", canonical: "https://www.facebook.com/permalink.php?story_fbid=7&id=8", postID: "7", wantMatch: true},
		{name: "permalink owner", canonical: "https://www.facebook.com/permalink.php?story_fbid=7&id=8", postID: "8", wantMatch: false},
		{name: "group post", canonical: "https://www.facebook.com/groups/1495321534752609/posts/1796616717956421", postID: "1796616717956421", wantMatch: true},
		{name: "group id", canonical: "https://www.facebook.com/groups/1495321534752609/posts/1796616717956421", postID: "1495321534752609", wantMatch: false},
		{name: "reel", canonical: "https://www.facebook.com/reel/588109690402315", postID: "588109690402315", wantMatch: true},
		{name: "id in another parameter", canonical: "https://www.facebook.com/photo?fbid=1&ref=588109690402315", postID: "588109690402315", wantMatch: false},
		{name: "no comparable id", canonical: "https://www.facebook.com/photo?fbid=1", postID: "", wantMatch: true},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			post := facebedPost{CanonicalURL: testCase.canonical}

			err := checkFacebedPostMatches(post, testCase.postID)

			if (err == nil) != testCase.wantMatch {
				t.Fatalf("checkFacebedPostMatches(%q, %q) = %v, want match %v", testCase.canonical, testCase.postID, err, testCase.wantMatch)
			}
		})
	}
}
