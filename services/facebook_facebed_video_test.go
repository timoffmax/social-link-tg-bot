package services

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// facebedTestVideoPostURL is the post of the facebed_video_post.html fixture
// (Mark Zuckerberg, "A decade of smoking meats").
const facebedTestVideoPostURL = "https://www.facebook.com/zuck/posts/10118046114638401"

func serveFacebedVideo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "video/mp4")
	_, _ = w.Write(facebedTestImage)
}

// facebedPageRequests counts the requests for facebed pages (not media).
func facebedPageRequests(server *facebedTestServer) int {
	result := 0

	for _, request := range server.requested() {
		isMedia := strings.HasPrefix(request.URL.Path, "/o1/") || strings.HasPrefix(request.URL.Path, "/v/")

		if isMedia == false {
			result++
		}
	}

	return result
}

func TestFacebookChainSendsTheFacebedVideoWhenYtDlpFails(t *testing.T) {
	isolateFacebedTracker(t)
	calls := 0
	server := serveFacebed(t, "facebed_video_post.html", serveFacebedVideo)
	downloader := newFacebookTestDownloader(t, server, failingYtDlp, &calls)

	media, err := downloader.downloadMediaWithContext(context.Background(), facebedTestVideoPostURL)

	if (err != nil) || (len(media) != 1) || (media[0].Type != MediaTypeVideo) {
		t.Fatalf("want the facebed video, got %d item(s) %v", len(media), err)
	}

	if strings.HasPrefix(media[0].Caption, "📘 Mark Zuckerberg\n\nA decade of smoking meats\n\n🔗 <a href=") == false {
		t.Errorf("caption %q", media[0].Caption)
	}

	if strings.HasSuffix(media[0].Filename, ".mp4") == false {
		t.Errorf("file %q", media[0].Filename)
	}

	if (calls != 1) || (facebedPageRequests(server) != 1) {
		t.Errorf("yt-dlp ran %d time(s) and facebed was read %d time(s), want 1 and 1", calls, facebedPageRequests(server))
	}
}

func TestFacebookChainPrefersTheYtDlpVideoOfAVideoPost(t *testing.T) {
	isolateFacebedTracker(t)
	calls := 0
	video := &MediaInfo{Type: MediaTypeVideo, Filename: "video.mp4"}
	videoYtDlp := func() ([]*MediaInfo, error) {
		return []*MediaInfo{video}, nil
	}
	server := serveFacebed(t, "facebed_video_post.html", serveFacebedVideo)
	downloader := newFacebookTestDownloader(t, server, videoYtDlp, &calls)

	media, err := downloader.downloadMediaWithContext(context.Background(), facebedTestVideoPostURL)

	if (err != nil) || (len(media) != 1) || (media[0] != video) {
		t.Fatalf("want the yt-dlp video, got %d item(s) %v", len(media), err)
	}

	if len(server.requested()) != facebedPageRequests(server) {
		t.Errorf("the facebed video must not download when yt-dlp succeeds")
	}
}

func TestFacebedVideoOfAPostLinkNeedsTheEarlierRead(t *testing.T) {
	isolateFacebedTracker(t)
	server := serveFacebed(t, "facebed_video_post.html", serveFacebedVideo)
	extractor := newTestFacebedExtractor(t, server)

	media, err := extractor.ExtractVideo(context.Background(), facebedTestVideoPostURL)

	if (err == nil) || (len(media) != 0) || (len(server.requested()) != 0) {
		t.Fatalf("want errFacebedNoVideo without a request, got %d item(s), %d request(s), %v", len(media), len(server.requested()), err)
	}
}

func TestFacebedVideoReadsTheReelPage(t *testing.T) {
	isolateFacebedTracker(t)
	server := serveFacebed(t, "facebed_video.html", serveFacebedVideo)
	extractor := newTestFacebedExtractor(t, server)

	media, err := extractor.ExtractVideo(context.Background(), facebedTestReelURL)

	if (err != nil) || (len(media) != 1) || (media[0].Type != MediaTypeVideo) {
		t.Fatalf("want the reel video, got %d item(s) %v", len(media), err)
	}

	if strings.HasPrefix(media[0].Caption, "📘 Sympa\n\nStylisation du levier de vitesse façon Barbie") == false {
		t.Errorf("caption %q", media[0].Caption)
	}
}

func TestFacebedVideoIsRefusedWhenItIsNotAVideo(t *testing.T) {
	isolateFacebedTracker(t)
	server := serveFacebed(t, "facebed_video.html", serveFacebedImage)
	extractor := newTestFacebedExtractor(t, server)

	media, err := extractor.ExtractVideo(context.Background(), facebedTestReelURL)

	if (err == nil) || (len(media) != 0) || (strings.Contains(err.Error(), "is not video media") == false) {
		t.Fatalf("want a content-type failure, got %d item(s) %v", len(media), err)
	}
}

func TestFacebedVideoAboveTheUploadLimitIsRefused(t *testing.T) {
	extractor := &FacebookFacebedExtractor{base: "https://www.facebed.com", mediaDomain: facebookMediaDomain}
	video := facebedPhoto{FinalURL: "https://video.xx.fbcdn.net/o1/v.mp4", ContentType: "video/mp4", Size: (TelegramUploadLimitBytes + 1)}

	err := extractor.validateMedia(video, facebedVideoKind)

	if (err == nil) || (strings.Contains(err.Error(), "above the") == false) {
		t.Fatalf("want a size failure, got %v", err)
	}

	video.Size = TelegramUploadLimitBytes

	if err := extractor.validateMedia(video, facebedVideoKind); err != nil {
		t.Fatalf("a video at the limit must pass: %v", err)
	}
}
