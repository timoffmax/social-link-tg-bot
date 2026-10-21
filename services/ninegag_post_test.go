package services

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	ninegagTestArticleURL      = "https://9gag.com/gag/advy56B"
	ninegagTestVideoArticleURL = "https://9gag.com/gag/aBZ91V1"
	ninegagTestCDN             = "https://img-9gag-fun.9cache.com"
)

func readNinegagFixture(t *testing.T, name string) []byte {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join("testdata", name))

	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}

	return raw
}

func parseNinegagFixture(t *testing.T, name string) ninegagPost {
	t.Helper()

	post, err := parseNinegagPost(readNinegagFixture(t, name))

	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}

	return post
}

func TestNinegagCaptionEscapesTheTitle(t *testing.T) {
	cases := map[string]string{
		"Balkan friendship be like": "😂 Balkan friendship be like",
		"Tom &amp; Jerry <3":        "😂 Tom &amp; Jerry &lt;3",
		`This "Nyangaroo Hoodie"`:   "😂 This &#34;Nyangaroo Hoodie&#34;",
		"くたばれ、また明日な":                "😂 くたばれ、また明日な",
		"  ":                        "",
	}

	for title, want := range cases {
		if got := NinegagCaption(title); got != want {
			t.Errorf("%q: got %q, want %q", title, got, want)
		}
	}
}

func TestNinegagMetadataCaptionReadsTheYtDlpTitle(t *testing.T) {
	metadata := map[string]interface{}{"title": "Balkan friendship be like", "id": "axyp6p1"}

	if got := ninegagMetadataCaption(metadata); got != "😂 Balkan friendship be like" {
		t.Errorf("got %q", got)
	}

	if got := ninegagMetadataCaption(nil); got != "" {
		t.Errorf("no metadata: got %q", got)
	}
}

func TestNinegagArticleFilesFollowTheBlocks(t *testing.T) {
	post := parseNinegagFixture(t, "ninegag_article.json")

	files := ninegagArticleFiles(post.Article)

	if (isNinegagArticle(post) == false) || (len(files) != 10) {
		t.Fatalf("want an article with 10 files, got %d", len(files))
	}

	if (files[0].URL != ninegagTestCDN+"/photo/a4npR4m_700b.jpg") || (files[1].URL != ninegagTestCDN+"/photo/aYGj7px_700b.jpg") || (files[9].URL != ninegagTestCDN+"/photo/aWpBEZK_700b.jpg") {
		t.Errorf("files out of block order: %v", files)
	}

	for _, file := range files {
		if file.MediaType != MediaTypePhoto {
			t.Errorf("%s: type %s", file.URL, file.MediaType)
		}
	}
}

func TestNinegagArticleFilesTakeTheMP4AndSkipEmbeddedVideos(t *testing.T) {
	post := parseNinegagFixture(t, "ninegag_article_video.json")

	files := ninegagArticleFiles(post.Article)

	if (len(files) != 6) || (files[0].URL != ninegagTestCDN+"/photo/amWqz1j_460svh265.mp4") || (files[0].MediaType != MediaTypeVideo) {
		t.Fatalf("want the MP4 first and 6 files, got %v", files)
	}

	for _, file := range files {
		if strings.Contains(file.URL, "avW2neO") == true {
			t.Errorf("the embedded YouTube video's thumbnail must not be sent: %s", file.URL)
		}
	}
}

func TestNinegagArticleTextHasTitleBlocksAndLinks(t *testing.T) {
	post := parseNinegagFixture(t, "ninegag_article.json")

	text := ninegagArticleText(post, ninegagTestArticleURL)

	wantStart := "😂 This &#34;Nyangaroo Hoodie&#34; Lets You Cuddle Your Cat While Gaming\n\nBIBILAB, a Japanese company, has created a hoodie"

	if strings.HasPrefix(text, wantStart) == false {
		t.Errorf("text starts %q", text[:200])
	}

	if strings.Contains(text, `listed for ¥8,900 ($56.37) on the <a href="https://www.amazon.co.jp/BIBILAB-`) == false {
		t.Errorf("the markdown link must become a link: %q", text)
	}

	if strings.Contains(text, "[official BIBILAB store on Amazon]") == true {
		t.Errorf("markdown left in the text")
	}

	message := text + FormatOriginalLink(ninegagTestArticleURL)

	if telegramTextLength(message) > telegramMessageLimit {
		t.Errorf("message is %d units long", telegramTextLength(message))
	}
}

func TestNinegagArticleTextQuotesAndLinksTheEmbeddedVideo(t *testing.T) {
	post := parseNinegagFixture(t, "ninegag_article_video.json")

	text := ninegagArticleText(post, ninegagTestVideoArticleURL)

	if strings.Contains(text, "<blockquote>We live in North Harford, so it’s very rural back here.") == false {
		t.Errorf("quote line not quoted: %q", text)
	}

	if strings.Contains(text, "▶️ https://www.youtube.com/watch?v=lQ_JJjI71Rk") == false {
		t.Errorf("embedded video not linked: %q", text)
	}
}

func TestNinegagArticleTextFitsTheMessageLimit(t *testing.T) {
	long := strings.Repeat("A [link](https://example.com/a?b=1&c=2) & more. ", 300)
	article := &ninegagArticle{Blocks: []ninegagBlock{{Type: ninegagBlockText, Content: long}}}
	post := ninegagPost{Title: "Long", Type: ninegagTypeArticle, Article: article}

	text := ninegagArticleText(post, ninegagTestArticleURL)
	message := text + FormatOriginalLink(ninegagTestArticleURL)

	if (telegramTextLength(message) > telegramMessageLimit) || (strings.HasSuffix(text, ninegagTruncationMarker) == false) {
		t.Errorf("message is %d units long, ends %q", telegramTextLength(message), text[len(text)-40:])
	}

	if strings.Contains(text, "&amp;amp;") == true {
		t.Errorf("double escaped: %q", text[:200])
	}
}

// serveNinegagCDN answers every file of the 9GAG CDN and records the paths.
// ninegagTestPrefix is the download prefix of the tests' files.
const ninegagTestPrefix = "1792000000000000000-1_ABCDEF12_"

// serveNinegagTLS serves handler over TLS and returns a 9GAG downloader
// whose client sends every host (the real CDN hosts included) to it.
func serveNinegagTLS(t *testing.T, handler http.Handler) *YtDlpDownloader {
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	address := server.Listener.Addr().String()
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var dialer net.Dialer

			return dialer.DialContext(ctx, network, address)
		},
	}
	downloader := &YtDlpDownloader{TempDir: t.TempDir(), ninegagClient: &http.Client{Transport: transport}}

	return downloader
}

func serveNinegagCDN(t *testing.T) (*YtDlpDownloader, *[]string) {
	var mu sync.Mutex
	paths := []string{}
	downloader := serveNinegagTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		_, _ = w.Write([]byte("file"))
	}))

	return downloader, &paths
}

func TestBuild9GAGPostSendsTheArticleTextThenItsFiles(t *testing.T) {
	downloader, paths := serveNinegagCDN(t)
	post := parseNinegagFixture(t, "ninegag_article_video.json")

	media, err := downloader.build9GAGPost(context.Background(), post, ninegagTestVideoArticleURL, ninegagTestPrefix)

	if (err != nil) || (len(media) != 7) {
		t.Fatalf("want text + 6 files, got %d %v", len(media), err)
	}

	if (media[0].Type != MediaTypeText) || (media[0].Filename != "") || (strings.HasPrefix(media[0].Caption, "😂 Longtime School Bus Driver Fired") == false) {
		t.Errorf("first item %+v", media[0])
	}

	if (media[1].Type != MediaTypeVideo) || (strings.HasSuffix(media[1].Filename, "_9gag_1.mp4") == false) || (media[2].Type != MediaTypePhoto) {
		t.Errorf("files %+v %+v", media[1], media[2])
	}

	if (len(*paths) != 6) || (slices.Contains(*paths, "/photo/amWqz1j_460svh265.mp4") == false) {
		t.Errorf("downloaded %v", *paths)
	}
}

func TestBuild9GAGPostSendsAPhotoPostWithItsTitle(t *testing.T) {
	downloader, _ := serveNinegagCDN(t)
	raw := []byte(`{"id":"aryPZ7d","title":"I am the pillow now","type":"Photo","images":{"image700":{"url":"` + ninegagTestCDN + `/photo/aryPZ7d_700b.jpg"},"image460":{"url":"` + ninegagTestCDN + `/photo/aryPZ7d_460s.jpg"}}}`)
	post, _ := parseNinegagPost(raw)

	media, err := downloader.build9GAGPost(context.Background(), post, "https://9gag.com/gag/aryPZ7d", ninegagTestPrefix)

	if (err != nil) || (len(media) != 1) || (media[0].Type != MediaTypePhoto) || (media[0].Caption != "😂 I am the pillow now") {
		t.Fatalf("want 1 photo with the title, got %+v %v", media, err)
	}

	if strings.HasSuffix(media[0].URL, "/photo/aryPZ7d_700b.jpg") == false {
		t.Errorf("want the large image, got %s", media[0].URL)
	}

	if filepath.Base(media[0].Filename) != (ninegagTestPrefix + "9gag.jpg") {
		t.Errorf("want the download's prefix, got %s", media[0].Filename)
	}
}

func requireEmptyDir(t *testing.T, dir string) {
	t.Helper()

	entries, err := os.ReadDir(dir)

	if (err != nil) || (len(entries) != 0) {
		t.Errorf("want no files left, got %v %v", entries, err)
	}
}

func TestDownload9GAGFileRemovesAPartialFile(t *testing.T) {
	downloader := serveNinegagTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000000")
		_, _ = w.Write(bytes.Repeat([]byte("v"), 5000))
	}))

	_, err := downloader.download9GAGFileNamed(context.Background(), ninegagTestCDN+"/photo/a.mp4", "1_ABC_9gag_1.mp4", MediaTypeVideo)

	if err == nil {
		t.Fatal("want a failure for a cut body")
	}

	requireEmptyDir(t, downloader.TempDir)
}

func TestDownload9GAGFileRefusesAPhotoAboveTheUploadLimit(t *testing.T) {
	tooBig := strconv.FormatInt((TelegramUploadLimitBytes + 1), 10)
	downloader := serveNinegagTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", tooBig)
		w.WriteHeader(http.StatusOK)
	}))

	_, err := downloader.download9GAGFileNamed(context.Background(), ninegagTestCDN+"/photo/a.jpg", "1_ABC_9gag_1.jpg", MediaTypePhoto)

	if (err == nil) || (strings.Contains(err.Error(), "limit") == false) {
		t.Fatalf("want the limit refusal, got %v", err)
	}

	requireEmptyDir(t, downloader.TempDir)
}

func TestBuild9GAGPostStopsTheArticleDownloadsAtTheDeadline(t *testing.T) {
	downloader := serveNinegagTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
	}))
	post := parseNinegagFixture(t, "ninegag_article_video.json")
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()

	media, err := downloader.build9GAGPost(ctx, post, ninegagTestVideoArticleURL, ninegagTestPrefix)

	if elapsed := time.Since(start); elapsed > (2 * time.Second) {
		t.Errorf("took %v, want the deadline to stop it", elapsed)
	}

	if (err != nil) || (len(media) != 1) || (media[0].Type != MediaTypeText) {
		t.Errorf("want only the text, got %d items %v", len(media), err)
	}

	requireEmptyDir(t, downloader.TempDir)
}

func TestCheckNinegagMediaURLAcceptsOnlyHTTPSOnTheCDN(t *testing.T) {
	cases := map[string]bool{
		"https://img-9gag-fun.9cache.com/photo/a_700b.jpg":   true,
		"https://miscmedia-9gag-fun.9cache.com/images/a.jpg": true,
		"https://IMG-9GAG-FUN.9CACHE.COM/photo/a.jpg":        true,
		"http://img-9gag-fun.9cache.com/photo/a.jpg":         false,
		"https://127.0.0.1/photo/a.jpg":                      false,
		"https://169.254.169.254/latest/meta-data/":          false,
		"https://localhost/a.jpg":                            false,
		"https://example.com/a.jpg":                          false,
		"https://9cache.com.example.com/a.jpg":               false,
		"https://evil9cache.com/a.jpg":                       false,
		"https://img-9gag-fun.9cache.com@127.0.0.1/a.jpg":    false,
		"file:///etc/passwd":                                 false,
		"":                                                   false,
	}

	for mediaURL, want := range cases {
		err := checkNinegagMediaURL(mediaURL, "")
		isAccepted := (err == nil)

		if isAccepted != want {
			t.Errorf("%q: accepted %t, want %t (%v)", mediaURL, isAccepted, want, err)
		}

		if (isAccepted == false) && (errors.Is(err, errNinegagOffCDN) == false) {
			t.Errorf("%q: want errNinegagOffCDN, got %v", mediaURL, err)
		}
	}
}

func TestDownload9GAGFileRefusesAURLOffTheCDNBeforeAnyRequest(t *testing.T) {
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write(bytes.Repeat([]byte("v"), 5000))
	}))
	t.Cleanup(server.Close)
	downloader := &YtDlpDownloader{TempDir: t.TempDir()}
	urls := []string{server.URL + "/a.jpg", "https://169.254.169.254/latest/meta-data/", "https://example.com/a.jpg"}

	for _, fileURL := range urls {
		_, err := downloader.download9GAGFileNamed(context.Background(), fileURL, "1_ABC_9gag.jpg", MediaTypePhoto)

		if errors.Is(err, errNinegagOffCDN) == false {
			t.Errorf("%s: want errNinegagOffCDN, got %v", fileURL, err)
		}
	}

	if hits != 0 {
		t.Errorf("want no request, got %d", hits)
	}

	requireEmptyDir(t, downloader.TempDir)
}

func TestDownload9GAGFileRefusesARedirectOffTheCDN(t *testing.T) {
	targets := []string{
		"http://127.0.0.1/a.jpg",
		"https://127.0.0.1/a.jpg",
		"https://169.254.169.254/latest/meta-data/",
		"http://img-9gag-fun.9cache.com/photo/a.jpg",
		"https://example.com/a.jpg",
	}

	for _, target := range targets {
		var mu sync.Mutex
		paths := []string{}
		downloader := serveNinegagTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			paths = append(paths, r.URL.Path)
			mu.Unlock()
			http.Redirect(w, r, target, http.StatusFound)
		}))

		_, err := downloader.download9GAGFileNamed(context.Background(), ninegagTestCDN+"/photo/a.jpg", "1_ABC_9gag.jpg", MediaTypePhoto)

		if errors.Is(err, errNinegagOffCDN) == false {
			t.Errorf("%s: want errNinegagOffCDN, got %v", target, err)
		}

		if (len(paths) != 1) || (paths[0] != "/photo/a.jpg") {
			t.Errorf("%s: want only the CDN request, got %v", target, paths)
		}

		requireEmptyDir(t, downloader.TempDir)
	}
}

func TestDownload9GAGFileFollowsARedirectWithinTheCDN(t *testing.T) {
	downloader := serveNinegagTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		isFirstHop := (r.URL.Path == "/photo/a.jpg")

		if isFirstHop == true {
			http.Redirect(w, r, "https://miscmedia-9gag-fun.9cache.com/photo/b.jpg", http.StatusFound)
		}

		if isFirstHop == false {
			_, _ = w.Write(bytes.Repeat([]byte("v"), 5000))
		}
	}))

	path, err := downloader.download9GAGFileNamed(context.Background(), ninegagTestCDN+"/photo/a.jpg", "1_ABC_9gag.jpg", MediaTypePhoto)

	if (err != nil) || (filepath.Base(path) != "1_ABC_9gag.jpg") {
		t.Fatalf("want the file, got %s %v", path, err)
	}
}
