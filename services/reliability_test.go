package services

// Tests for the redirector media validation, health probes, and Reddit
// short-URL resolution. Live network checks are skipped unless VERIFY_LIVE=1:
//   VERIFY_LIVE=1 go test ./services -run 'TestVerify' -v

import (
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestVerifyValidateRedirectorMedia(t *testing.T) {
	cases := []struct {
		name        string
		finalURL    string
		contentType string
		size        int64
		wantErr     bool
	}{
		{"healthy instagram video", "https://scontent.cdninstagram.com/o1/v/t2/f2/m86/xyz.mp4?x=1", "video/mp4", 5349270, false},
		{"healthy tiktok video", "https://v16m.tiktokcdn-us.com/abc/video/tos/alisg/x/?mime_type=video_mp4", "video/mp4", 1342638, false},
		{"200 HTML on kk host (blocked)", "https://kkinstagram.com/reel/DZW7SUjgcH0/", "text/html; charset=utf-8", 4192, true},
		{"bounce back to instagram", "https://www.instagram.com/reel/DZW7SUjgcH0/", "text/html", 50000, true},
		{"kkclip interstitial", "https://kkclip.com/open/ig/123/DZW7SUjgcH0", "text/html; charset=utf-8", 4192, true},
		{"tiny body", "https://scontent.cdninstagram.com/x.jpg", "image/jpeg", 512, true},
		{"healthy photo", "https://scontent.cdninstagram.com/x.jpg?y=1", "image/jpeg", 250000, false},
	}

	for _, c := range cases {
		origin := "instagram.com"
		redirector := "kkinstagram.com"

		if c.name == "healthy tiktok video" {
			origin = "tiktok.com"
			redirector = "kktiktok.com"
		}

		err := validateRedirectorMedia(redirector, origin, c.finalURL, c.contentType, c.size)

		if (err != nil) != c.wantErr {
			t.Errorf("%s: got err=%v, wantErr=%v", c.name, err, c.wantErr)
		} else {
			t.Logf("%s: OK (err=%v)", c.name, err)
		}
	}
}

func TestVerifyHostMatching(t *testing.T) {
	if hostMatchesDomain("scontent.cdninstagram.com", "instagram.com") {
		t.Error("cdninstagram.com must NOT match instagram.com")
	}

	if !hostMatchesDomain("www.instagram.com", "instagram.com") {
		t.Error("www.instagram.com must match instagram.com")
	}

	if hostMatchesDomain("v16m.tiktokcdn-us.com", "tiktok.com") {
		t.Error("tiktokcdn-us.com must NOT match tiktok.com")
	}
}

func TestVerifyClassifyRedirectorMedia(t *testing.T) {
	mediaType, ext := classifyRedirectorMedia("video/mp4", "https://x.com/no-extension/path")

	if (mediaType != "video") || (ext != ".mp4") {
		t.Errorf("video/mp4 classified as %s %s", mediaType, ext)
	}

	mediaType, ext = classifyRedirectorMedia("image/jpeg", "https://x.com/a.jpg?q=1")

	if (mediaType != "photo") || (ext != ".jpg") {
		t.Errorf("image/jpeg classified as %s %s", mediaType, ext)
	}
}

func TestVerifyEscapeForTelegramHTML(t *testing.T) {
	if got := EscapeForTelegramHTML("a < b & c"); got != "a &lt; b &amp; c" {
		t.Errorf("raw text: got %q", got)
	}

	// Already-escaped input (Reddit title convention) must not double-escape
	if got := EscapeForTelegramHTML("a &lt; b &amp; c"); got != "a &lt; b &amp; c" {
		t.Errorf("pre-escaped text: got %q", got)
	}
}

func TestVerifyOriginBounceSentinel(t *testing.T) {
	err := validateRedirectorMedia("kkinstagram.com", "instagram.com", "https://www.instagram.com/reel/x/", "text/html", 50000)

	if !errors.Is(err, errRedirectorOriginBounce) {
		t.Errorf("origin bounce must be detectable via errors.Is, got: %v", err)
	}

	err = validateRedirectorMedia("kkinstagram.com", "instagram.com", "https://kkinstagram.com/reel/x/", "text/html", 4192)

	if errors.Is(err, errRedirectorOriginBounce) {
		t.Errorf("no-redirect failure must NOT be classified as origin bounce: %v", err)
	}
}

func TestVerifyRedditPostLevelError(t *testing.T) {
	notFound := &redditHTTPError{StatusCode: http.StatusNotFound, Excerpt: "not found"}

	if !isRedditPostLevelError(notFound) {
		t.Error("404 must be post-level")
	}

	blocked := &redditHTTPError{StatusCode: http.StatusForbidden, Excerpt: "blocked"}

	if isRedditPostLevelError(blocked) {
		t.Error("403 (proxy blocked) must NOT be post-level")
	}

	if isRedditPostLevelError(errors.New("proxy request failed: connect timeout")) {
		t.Error("transport errors must NOT be post-level")
	}
}

func TestVerifyTruncateAtRuneBoundary(t *testing.T) {
	long := strings.Repeat("є", 3000) // 2-byte runes, 6000 bytes

	got := truncateAtRuneBoundary(long, 3500)

	if len(got) > 3500 {
		t.Errorf("truncated to %d bytes, want <= 3500", len(got))
	}

	if !strings.HasSuffix(got, "є") {
		t.Error("truncation split a UTF-8 rune")
	}
}

func TestVerifyLiveRedirectorProbes(t *testing.T) {
	if os.Getenv("VERIFY_LIVE") == "" {
		t.Skip("set VERIFY_LIVE=1 for live network checks")
	}

	// Feeds the outage tracker; with no TG_BOT_TOKEN set, NotifyAdmin is a no-op.
	CheckRedirectorHealth()
}

func TestVerifyLiveKKExtraction(t *testing.T) {
	if os.Getenv("VERIFY_LIVE") == "" {
		t.Skip("set VERIFY_LIVE=1 for live network checks")
	}

	kk := NewInstagramKKInstagramExtractor(t.TempDir())

	items, err := kk.ExtractMedia("https://www.instagram.com/reel/DZW7SUjgcH0/")

	if err != nil {
		t.Fatalf("kkinstagram extraction failed: %v", err)
	}

	t.Logf("kkinstagram: %d item(s), type=%s file=%s", len(items), items[0].Type, items[0].Filename)

	te := NewTikTokExtractor(t.TempDir(), t.TempDir())

	items, err = te.extractViaKKTikTok("https://vt.tiktok.com/ZSCsvW8Lp/", "7633407742224469268", "someuser")

	if err != nil {
		t.Fatalf("kktiktok extraction failed: %v", err)
	}

	t.Logf("kktiktok: %d item(s), type=%s file=%s caption=%q", len(items), items[0].Type, items[0].Filename, items[0].Caption)
}

func TestVerifyLiveRedditDirectExtraction(t *testing.T) {
	if os.Getenv("VERIFY_LIVE") == "" {
		t.Skip("set VERIFY_LIVE=1 for live network checks")
	}

	t.Setenv("ENABLE_PROXIES", "false")

	re := NewRedditExtractor(t.TempDir())

	items, err := re.ExtractRedditPost("https://www.reddit.com/r/ukraine_dev/s/YPgdlPVkgi")

	if err != nil {
		t.Fatalf("direct Reddit extraction failed: %v", err)
	}

	if (len(items) != 1) || (items[0].Type != "text") {
		t.Fatalf("expected 1 text item, got %+v", items)
	}

	if strings.Contains(items[0].Caption, "r/ukraine_dev") == false {
		t.Errorf("caption missing subreddit: %.120s", items[0].Caption)
	}

	t.Logf("extracted text post (%d chars): %.200s", len(items[0].Caption), items[0].Caption)
}

func TestVerifyLiveRedditShortURL(t *testing.T) {
	if os.Getenv("VERIFY_LIVE") == "" {
		t.Skip("set VERIFY_LIVE=1 for live network checks")
	}

	re := NewRedditExtractor(t.TempDir())

	resolved, err := re.resolveShortURLDirect("https://www.reddit.com/r/ukraine_dev/s/YPgdlPVkgi")

	if err != nil {
		t.Fatalf("direct short URL resolution failed: %v", err)
	}

	t.Logf("resolved: %s", resolved)
}
