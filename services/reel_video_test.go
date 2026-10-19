package services

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const reelVideoTestURL = "https://www.instagram.com/reel/DbcgVdQziXu/"

// recordingGatedProvider returns one video and records the calls it got.
type recordingGatedProvider struct {
	calls    int
	spoilers []bool
}

func (p *recordingGatedProvider) Name() string {
	return "recording"
}

func (p *recordingGatedProvider) FetchPost(_ string, spoiler bool) ([]*MediaInfo, error) {
	p.calls++
	p.spoilers = append(p.spoilers, spoiler)

	return []*MediaInfo{{Type: MediaTypeVideo, Caption: "🎬 author"}}, nil
}

func newReelCover(t *testing.T) *MediaInfo {
	path := filepath.Join(t.TempDir(), "cover.jpg")

	if err := os.WriteFile(path, []byte("jpeg"), 0o600); err != nil {
		t.Fatalf("write cover: %v", err)
	}

	return &MediaInfo{Type: MediaTypePhoto, URL: reelVideoTestURL, Caption: "📷 author", Filename: path, VideoCover: true}
}

func TestReelAndIGTVAreVideoPostTypes(t *testing.T) {
	cases := map[string]bool{"reel": true, "tv": true, "p": false, "": false}

	for postType, want := range cases {
		if got := isInstagramVideoPostType(postType); got != want {
			t.Fatalf("isInstagramVideoPostType(%q) = %t, want %t", postType, got, want)
		}
	}
}

func TestFetchReelVideoLeavesOtherDownloadsAlone(t *testing.T) {
	provider := &recordingGatedProvider{}
	sd := &SmartDownloader{gated: provider}
	photo := &MediaInfo{Type: MediaTypePhoto, Caption: "📷 author"}
	coverOfMany := newReelCover(t)
	cases := [][]*MediaInfo{{photo}, {coverOfMany, photo}, nil}

	for _, media := range cases {
		got, _ := sd.FetchReelVideo(reelVideoTestURL, media, DownloadRequester{ChatID: 1, UserID: 1, IsPrivate: true})

		if len(got) != len(media) {
			t.Fatalf("expected the download unchanged, got %d items", len(got))
		}
	}

	if (provider.calls != 0) || (photo.Caption != "📷 author") || (coverOfMany.Caption != "📷 author") {
		t.Fatalf("expected no paid call and no note, got %d calls, captions %q / %q", provider.calls, photo.Caption, coverOfMany.Caption)
	}
}

func TestFetchReelVideoKeepsTheCoverWithANoteWhenSpecialDownloadsAreOff(t *testing.T) {
	t.Setenv("GATED_FETCH_GLOBAL_MONTHLY_CAP", "0")
	cover := newReelCover(t)
	sd, fake := newReelRelayDownloader(t, serveRelayVideo("", relayTestMP4), &recordingGatedProvider{})

	got, denial := sd.FetchReelVideo(reelVideoTestURL, []*MediaInfo{cover}, DownloadRequester{ChatID: 1, UserID: 1, IsPrivate: true})

	if (len(got) != 1) || (got[0] != cover) || (denial != GatedFetchDeniedOff) || (len(fake.recorded()) != 0) {
		t.Fatalf("expected the cover back, denial off and no relay call, got %+v, %q, %d call(s)", got, denial, len(fake.recorded()))
	}

	if strings.HasSuffix(cover.Caption, reelCoverNote) == false {
		t.Fatalf("expected the cover note, got %q", cover.Caption)
	}

	if _, err := os.Stat(cover.Filename); err != nil {
		t.Fatalf("the cover file must stay for sending: %v", err)
	}
}

// coverGatedProvider answers with a photo only, like a provider that missed the video.
type coverGatedProvider struct{}

func (coverGatedProvider) Name() string {
	return "cover"
}

func (coverGatedProvider) FetchPost(string, bool) ([]*MediaInfo, error) {
	return []*MediaInfo{{Type: MediaTypePhoto, Caption: "📷 author"}}, nil
}

func TestHasVideoItemNeedsAVideo(t *testing.T) {
	if (hasVideoItem([]*MediaInfo{{Type: MediaTypePhoto}}) == true) || (hasVideoItem(nil) == true) {
		t.Fatalf("a photo-only or empty result has no video")
	}

	if hasVideoItem([]*MediaInfo{{Type: MediaTypePhoto}, {Type: MediaTypeVideo}}) == false {
		t.Fatalf("expected the video found")
	}
}

func TestReelCoverNoteStandsAloneOnAnEmptyCaption(t *testing.T) {
	cover := newReelCover(t)
	cover.Caption = ""
	sd := &SmartDownloader{}

	sd.FetchReelVideo(reelVideoTestURL, []*MediaInfo{cover}, DownloadRequester{ChatID: 1, UserID: 1, IsPrivate: true})

	if cover.Caption != reelCoverNote {
		t.Fatalf("expected only the note, got %q", cover.Caption)
	}
}
