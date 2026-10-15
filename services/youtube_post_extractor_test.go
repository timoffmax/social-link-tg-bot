package services

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// youtubePostPage wraps a backstagePostRenderer in a minimal post page.
func youtubePostPage(renderer string) string {
	return fmt.Sprintf(`<html><script>var ytInitialData = {"contents":{"items":[{"backstagePostThreadRenderer":{"post":{"backstagePostRenderer":%s}}}]}};</script></html>`, renderer)
}

const youtubeImagePost = `{"postId":"UgkxIMG","authorText":{"runs":[{"text":"YouTube"}]},
	"contentText":{"runs":[{"text":"el jefe "},{"text":"has returned & more"}]},
	"backstageAttachment":{"backstageImageRenderer":{"image":{"thumbnails":[{"url":"{{BASE}}/small.jpg"},{"url":"{{BASE}}/large.jpg"}]}}}}`

const youtubeMultiImagePost = `{"postId":"UgkxMULTI","authorText":{"runs":[{"text":"Chan"}]},"contentText":{"runs":[{"text":"two pics"}]},
	"backstageAttachment":{"postMultiImageRenderer":{"images":[
		{"backstageImageRenderer":{"image":{"thumbnails":[{"url":"{{BASE}}/a.jpg"}]}}},
		{"backstageImageRenderer":{"image":{"thumbnails":[{"url":"{{BASE}}/b.jpg"}]}}}]}}}`

const youtubeTextPost = `{"postId":"UgkxTEXT","authorText":{"runs":[{"text":"Chan"}]},"contentText":{"runs":[{"text":"just <text>"}]},
	"backstageAttachment":{"videoRenderer":{"videoId":"x"}}}`

func serveYouTubePost(t *testing.T, renderer string) *httptest.Server {
	var server *httptest.Server
	var requested []string

	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		isPage := strings.HasPrefix(r.URL.Path, "/post/")
		requested = append(requested, r.URL.Path)

		if isPage == true {
			body := strings.ReplaceAll(renderer, "{{BASE}}", server.URL)
			fmt.Fprint(w, youtubePostPage(body))
		}

		if isPage == false {
			w.Write([]byte("image-bytes"))
		}
	}))
	t.Cleanup(server.Close)

	return server
}

// fetchTestPost runs ExtractMedia against the fake server for a post URL.
func fetchTestPost(t *testing.T, server *httptest.Server, postURL string) ([]*MediaInfo, error) {
	extractor := &YouTubePostExtractor{tempDir: t.TempDir(), pageBase: server.URL}

	return extractor.ExtractMedia(context.Background(), postURL)
}

func TestYouTubePostWithImageUsesTheLargestThumbnailAndCaption(t *testing.T) {
	server := serveYouTubePost(t, youtubeImagePost)

	media, err := fetchTestPost(t, server, "https://www.youtube.com/post/UgkxIMG")

	if (err != nil) || (len(media) != 1) || (media[0].Type != MediaTypePhoto) {
		t.Fatalf("unexpected result: %+v %v", media, err)
	}

	if (strings.Contains(media[0].Caption, "YouTube") == false) || (strings.Contains(media[0].Caption, "el jefe has returned &amp; more") == false) {
		t.Fatalf("unexpected caption: %q", media[0].Caption)
	}
}

func TestYouTubePostWithSeveralImagesReturnsThemAll(t *testing.T) {
	server := serveYouTubePost(t, youtubeMultiImagePost)

	media, err := fetchTestPost(t, server, "https://www.youtube.com/post/UgkxMULTI")

	if (err != nil) || (len(media) != 2) {
		t.Fatalf("expected 2 images, got %d %v", len(media), err)
	}

	if strings.Contains(media[1].Caption, "two pics") == true {
		t.Fatalf("only the first image carries the text: %q", media[1].Caption)
	}
}

func TestYouTubePostWithoutImageIsSentAsText(t *testing.T) {
	server := serveYouTubePost(t, youtubeTextPost)

	media, err := fetchTestPost(t, server, "https://www.youtube.com/post/UgkxTEXT")

	if (err != nil) || (len(media) != 1) || (media[0].Type != MediaTypeText) || (media[0].Filename != "") {
		t.Fatalf("expected one text item, got %+v %v", media, err)
	}

	if strings.Contains(media[0].Caption, "just &lt;text&gt;") == false {
		t.Fatalf("unexpected text: %q", media[0].Caption)
	}
}

func TestYouTubePostFailsForAnotherPostID(t *testing.T) {
	server := serveYouTubePost(t, youtubeImagePost)

	_, err := fetchTestPost(t, server, "https://www.youtube.com/post/UgkxOTHER")

	if (err == nil) || (strings.Contains(err.Error(), "not found") == false) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestYouTubePostFromAMobileLinkIsReadFromTheDesktopPage(t *testing.T) {
	server := serveYouTubePost(t, youtubeImagePost)

	media, err := fetchTestPost(t, server, "https://m.youtube.com/post/UgkxIMG?si=abc")

	if (err != nil) || (len(media) != 1) {
		t.Fatalf("expected the post, got %d %v", len(media), err)
	}
}

func TestYouTubeResharedPostReturnsTheOriginal(t *testing.T) {
	shared := `{"sharedPostRenderer":{"postId":"UgkxSHARE","originalPost":{"backstagePostRenderer":` + youtubeImagePost + `}}}`
	page := fmt.Sprintf(`<html><script>var ytInitialData = {"contents":{"items":[%s]}};</script></html>`, shared)
	var server *httptest.Server

	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		isPage := strings.HasPrefix(r.URL.Path, "/post/")

		if isPage == true {
			fmt.Fprint(w, strings.ReplaceAll(page, "{{BASE}}", server.URL))
		}

		if isPage == false {
			w.Write([]byte("image-bytes"))
		}
	}))
	defer server.Close()

	media, err := fetchTestPost(t, server, "https://www.youtube.com/post/UgkxSHARE")

	if (err != nil) || (len(media) != 1) || (strings.Contains(media[0].Caption, "el jefe") == false) {
		t.Fatalf("expected the shared original, got %+v %v", media, err)
	}
}
