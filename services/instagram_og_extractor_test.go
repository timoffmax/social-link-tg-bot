package services

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

// ogTestPage is an Instagram post page as served to the crawler User-Agent.
const ogTestPage = `<html><head><meta property="og:title" content="Abandoned Films on Instagram: &quot;Multiverse Tavern&quot;" /><meta property="og:image" content="https://scontent.cdninstagram.com/v/cover.jpg" /></head></html>`

// ogTestLogoPage is a page whose og:image is Instagram's logo, not post media.
const ogTestLogoPage = `<html><head><meta property="og:title" content="Instagram" /><meta property="og:image" content="https://static.cdninstagram.com/rsrc/logo.png" /></head></html>`

// serveInstagramPost answers like Instagram for a public post: a public
// oEmbed answer, the og page and its image. Both shared clients are pointed
// at the stub.
func serveInstagramPost(t *testing.T, page string) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/v1/oembed/"):
			_, _ = io.WriteString(w, `{"version":"1.0","author_name":"abandonedmovies"}`)
		case (r.URL.Path == "/v/cover.jpg") || (r.URL.Path == "/rsrc/logo.png"):
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte("\xff\xd8\xff\xe0 cover"))
		default:
			_, _ = io.WriteString(w, page)
		}
	})
	probeServeHTTPS(t, handler)
	savedDownloadClient := SharedDownloadClient
	SharedDownloadClient = SharedHTTPClient

	t.Cleanup(func() {
		SharedDownloadClient = savedDownloadClient
	})
}

func TestOGPreviewOfAReelIsMarkedAsItsVideoCover(t *testing.T) {
	serveInstagramPost(t, ogTestPage)
	extractor := NewInstagramOGExtractor(t.TempDir())

	for _, instagramURL := range []string{"https://www.instagram.com/reel/DesQ0ptp04D/?srtk=x", "https://www.instagram.com/reels/DesQ0ptp04D/", "https://www.instagram.com/tv/DesQ0ptp04D/"} {
		media, err := extractor.ExtractMedia(instagramURL)

		if err != nil {
			t.Fatalf("%s: unexpected error: %v", instagramURL, err)
		}

		if (len(media) != 1) || (media[0].VideoCover == false) {
			t.Fatalf("%s: expected one video cover, got %+v", instagramURL, media)
		}

		if isVideoCoverOnly(media) == false {
			t.Fatalf("%s: FetchReelVideo would not run for %+v", instagramURL, media)
		}
	}
}

func TestOGPreviewOfAPostIsNotAVideoCover(t *testing.T) {
	serveInstagramPost(t, ogTestPage)
	extractor := NewInstagramOGExtractor(t.TempDir())
	media, err := extractor.ExtractMedia("https://www.instagram.com/p/DesQ0ptp04D/")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if (len(media) != 1) || (media[0].VideoCover == true) {
		t.Fatalf("expected one plain photo, got %+v", media)
	}
}

func TestOGPreviewOfAReelWithALogoImageIsNotAVideoCover(t *testing.T) {
	serveInstagramPost(t, ogTestLogoPage)
	extractor := NewInstagramOGExtractor(t.TempDir())
	media, err := extractor.ExtractMedia("https://www.instagram.com/reel/DesQ0ptp04D/")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if (len(media) != 1) || (media[0].VideoCover == true) {
		t.Fatalf("expected the logo without the cover flag, got %+v", media)
	}
}

func TestGatedPreviewOfAReelIsNotAVideoCover(t *testing.T) {
	serveInstagramPost(t, ogTestPage)
	sd := &SmartDownloader{instagramOG: NewInstagramOGExtractor(t.TempDir())}
	media, err := sd.gatedPreviewFallback("https://www.instagram.com/reel/DesQ0ptp04D/", errors.New("paid fetch failed"))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if (len(media) != 1) || (media[0].VideoCover == true) {
		t.Fatalf("expected the gated preview without the cover flag, got %+v", media)
	}
}
