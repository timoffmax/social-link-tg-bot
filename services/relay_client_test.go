package services

// Hermetic tests of the home relay client, the YouTube relay method and its
// probe: every request goes to a local httptest server, and the outage
// tracker's notification hooks are stubbed (newAlertRecorder), so nothing
// reaches the network or Telegram.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/timoffmax/social-link-bot/config"
)

const (
	relayTestToken      = "test-relay-token"
	relayTestShortsID   = "bc-OZm4lpds"
	relayTestShortsURL  = "https://www.youtube.com/shorts/" + relayTestShortsID
	relayTestTikTokID   = "7633407742224469268"
	relayTestFacebookID = "1220500346751179"
	relayTestService    = "youtube-relay"
	relayTestProbeKey   = "youtube-relay-probe"
)

// relayTestMP4 starts with the ftyp box at offset 4, like every mp4.
var relayTestMP4 = append([]byte("\x00\x00\x00\x18ftypisom\x00\x00\x02\x00"), bytes.Repeat([]byte("v"), 4096)...)

// relayTestCall is one request the fake relay saw.
type relayTestCall struct {
	method        string
	path          string
	authorization string
	contentType   string
	contentLength int64
	body          []byte
}

// relayTestServer is a fake relay that records every request and answers
// with handler.
type relayTestServer struct {
	server *httptest.Server
	mu     sync.Mutex
	calls  []relayTestCall
}

// recorded returns a copy of the requests seen.
func (s *relayTestServer) recorded() []relayTestCall {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]relayTestCall(nil), s.calls...)
}

// startRelayTestServer serves handler on a loopback httptest server.
func startRelayTestServer(t *testing.T, handler http.HandlerFunc) *relayTestServer {
	t.Helper()

	fake := &relayTestServer{}
	fake.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		call := relayTestCall{
			method:        r.Method,
			path:          r.URL.Path,
			authorization: r.Header.Get("Authorization"),
			contentType:   r.Header.Get("Content-Type"),
			contentLength: r.ContentLength,
			body:          body,
		}

		fake.mu.Lock()
		fake.calls = append(fake.calls, call)
		fake.mu.Unlock()

		handler(w, r)
	}))
	t.Cleanup(fake.server.Close)

	return fake
}

// writeRelayTestToken writes the bearer token file and returns its path.
func writeRelayTestToken(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "relay.token")

	if err := os.WriteFile(path, []byte(relayTestToken+"\n"), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}

	return path
}

// resetRelayDisabledPlatforms clears the shared PLATFORM_DISABLED state
// before and after a test.
func resetRelayDisabledPlatforms(t *testing.T) {
	t.Helper()

	reset := func() {
		relayDisabledMu.Lock()
		relayDisabledPlatforms = make(map[string]bool)
		relayDisabledMu.Unlock()
	}

	reset()
	t.Cleanup(reset)
}

// newTestRelayClient returns a client of the fake relay with its own temp
// dir, stubbed admin alerts and clean shared state.
func newTestRelayClient(t *testing.T, baseURL string) (*relayClient, *alertRecorder) {
	t.Helper()

	alerts := newAlertRecorder(t)
	resetRelayDisabledPlatforms(t)

	client := &relayClient{
		baseURL:   baseURL,
		tokenFile: writeRelayTestToken(t),
		timeout:   45 * time.Second,
		tempDir:   t.TempDir(),
	}

	return client, alerts
}

// encodeRelayMeta encodes metadata like the relay: base64url, no padding.
func encodeRelayMeta(t *testing.T, meta map[string]any) string {
	t.Helper()

	raw, err := json.Marshal(meta)

	if err != nil {
		t.Fatalf("marshal meta: %v", err)
	}

	return base64.RawURLEncoding.EncodeToString(raw)
}

// serveRelayVideo answers like the relay does on success.
func serveRelayVideo(meta string, body []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if meta != "" {
			w.Header().Set(relayMetaHeader, meta)
		}

		w.Header().Set("Content-Type", relayVideoType)
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		_, _ = w.Write(body)
	}
}

// serveRelayError answers with the relay's JSON error shape.
func serveRelayError(status int, code string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", relayJSONType)
		w.WriteHeader(status)
		_, _ = fmt.Fprintf(w, `{"code": %q, "error": "test detail"}`, code)
	}
}

// relayTempFiles lists the files left in the client's temp dir.
func relayTempFiles(t *testing.T, client *relayClient) []string {
	t.Helper()

	entries, err := os.ReadDir(client.tempDir)

	if err != nil {
		t.Fatalf("read temp dir: %v", err)
	}

	result := []string{}

	for _, entry := range entries {
		result = append(result, entry.Name())
	}

	return result
}

func TestRelayFetchSendsExactlyPlatformIDAndDeadline(t *testing.T) {
	fake := startRelayTestServer(t, serveRelayVideo("", relayTestMP4))
	client, _ := newTestRelayClient(t, fake.server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	_, err := client.Fetch(ctx, relayPlatformYouTube, relayTestShortsID)

	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	calls := fake.recorded()

	if len(calls) != 1 {
		t.Fatalf("want 1 request, got %d", len(calls))
	}

	call := calls[0]

	if (call.method != http.MethodPost) || (call.path != relayFetchPath) {
		t.Errorf("request %s %s, want POST %s", call.method, call.path, relayFetchPath)
	}

	if call.authorization != "Bearer "+relayTestToken {
		t.Errorf("Authorization %q", call.authorization)
	}

	if (call.contentType != relayJSONType) || (call.contentLength != int64(len(call.body))) {
		t.Errorf("Content-Type %q, Content-Length %d for %d bytes", call.contentType, call.contentLength, len(call.body))
	}

	body := map[string]json.RawMessage{}

	if err := json.Unmarshal(call.body, &body); err != nil {
		t.Fatalf("body %q: %v", call.body, err)
	}

	keys := []string{}

	for key := range body {
		keys = append(keys, key)
	}

	slices.Sort(keys)

	if slices.Equal(keys, []string{"deadline_ms", "id", "platform"}) == false {
		t.Errorf("body keys %v", keys)
	}

	if (string(body["platform"]) != `"youtube"`) || (string(body["id"]) != `"`+relayTestShortsID+`"`) {
		t.Errorf("body %s", call.body)
	}

	if string(body["deadline_ms"]) != "40000" {
		t.Errorf("deadline_ms %s, want 40000 for a fresh 90 s chain", body["deadline_ms"])
	}
}

func TestRelayFetchSendsNumericIDsAsStrings(t *testing.T) {
	fake := startRelayTestServer(t, serveRelayVideo("", relayTestMP4))
	client, _ := newTestRelayClient(t, fake.server.URL)

	for _, testCase := range []struct{ platform, id string }{{relayPlatformTikTok, relayTestTikTokID}, {relayPlatformFacebook, relayTestFacebookID}} {
		if _, err := client.Fetch(context.Background(), testCase.platform, testCase.id); err != nil {
			t.Fatalf("%s: %v", testCase.platform, err)
		}
	}

	for _, call := range fake.recorded() {
		if (bytes.Contains(call.body, []byte(`"id":"`+relayTestTikTokID+`"`)) == false) && (bytes.Contains(call.body, []byte(`"id":"`+relayTestFacebookID+`"`)) == false) {
			t.Errorf("id not sent as a string: %s", call.body)
		}
	}
}

func TestRelayFetchStoresTheVideoWithItsMeta(t *testing.T) {
	cases := []struct {
		name     string
		platform string
		id       string
		meta     map[string]any
		want     relayMeta
	}{
		{
			name:     "youtube without width",
			platform: relayPlatformYouTube,
			id:       relayTestShortsID,
			meta:     map[string]any{"title": "A <b> title", "uploader": "Chan & Co", "height": 1080, "duration": 31},
			want:     relayMeta{Title: "A <b> title", Uploader: "Chan & Co"},
		},
		{
			name:     "tiktok with width",
			platform: relayPlatformTikTok,
			id:       relayTestTikTokID,
			meta:     map[string]any{"title": "caption text", "uploader": "user", "height": 1024, "width": 576, "duration": 12},
			want:     relayMeta{Title: "caption text", Uploader: "user"},
		},
		{
			name:     "facebook with fractional duration and nulls",
			platform: relayPlatformFacebook,
			id:       relayTestFacebookID,
			meta:     map[string]any{"title": "", "uploader": "", "height": nil, "width": nil, "duration": 86.56},
			want:     relayMeta{},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fake := startRelayTestServer(t, serveRelayVideo(encodeRelayMeta(t, testCase.meta), relayTestMP4))
			client, _ := newTestRelayClient(t, fake.server.URL)

			video, err := client.Fetch(context.Background(), testCase.platform, testCase.id)

			if err != nil {
				t.Fatalf("Fetch: %v", err)
			}

			stored, readErr := os.ReadFile(video.Path)

			if (readErr != nil) || (bytes.Equal(stored, relayTestMP4) == false) || (video.Size != int64(len(relayTestMP4))) {
				t.Errorf("stored file %s (%d bytes, err %v) differs from the body", video.Path, len(stored), readErr)
			}

			if (video.Meta.Title != testCase.want.Title) || (video.Meta.Uploader != testCase.want.Uploader) {
				t.Errorf("meta %+v, want %+v", video.Meta, testCase.want)
			}

			if strings.HasPrefix(filepath.Base(video.Path), testCase.platform+"_relay_") == false {
				t.Errorf("file name %s", video.Path)
			}
		})
	}
}

func TestRelayMetaWidthAndFractionalDuration(t *testing.T) {
	header := encodeRelayMeta(t, map[string]any{"width": 576, "height": 1024, "duration": 86.56})

	meta := parseRelayMeta(header, relayPlatformFacebook)

	if (meta.Width == nil) || (*meta.Width != 576) || (meta.Duration == nil) || (*meta.Duration != 86.56) {
		t.Errorf("meta %+v", meta)
	}

	padded := base64.URLEncoding.EncodeToString([]byte(`{"title":"x"}`))

	if parseRelayMeta(padded, relayPlatformYouTube).Title != "x" {
		t.Errorf("padded header not read")
	}
}

func TestRelayMetaMissingOrUnreadableStillSendsTheVideo(t *testing.T) {
	for _, header := range []string{"", "%%% not base64 %%%", base64.RawURLEncoding.EncodeToString([]byte("not json"))} {
		fake := startRelayTestServer(t, serveRelayVideo(header, relayTestMP4))
		client, _ := newTestRelayClient(t, fake.server.URL)

		video, err := client.Fetch(context.Background(), relayPlatformYouTube, relayTestShortsID)

		if (err != nil) || (video.Path == "") || (video.Meta != relayMeta{}) {
			t.Errorf("header %q: video %+v, err %v", header, video, err)
		}
	}
}

func TestRelayFetchRejectsABodyShorterThanContentLength(t *testing.T) {
	fake := startRelayTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", relayVideoType)
		w.Header().Set("Content-Length", strconv.Itoa(len(relayTestMP4)+1000))
		_, _ = w.Write(relayTestMP4)
	})
	client, _ := newTestRelayClient(t, fake.server.URL)

	_, err := client.Fetch(context.Background(), relayPlatformYouTube, relayTestShortsID)

	var failure *relayError

	if (errors.As(err, &failure) == false) || (failure.IsOutage == false) {
		t.Fatalf("want an outage error, got %v", err)
	}

	if files := relayTempFiles(t, client); len(files) != 0 {
		t.Errorf("cut file kept: %v", files)
	}

	if recordedFailureCount(relayTestService) != 1 {
		t.Errorf("cut transfer not reported as an outage")
	}
}

func TestRelayFetchRejectsADeclaredSizeOverTheLimit(t *testing.T) {
	fake := startRelayTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", relayVideoType)
		w.Header().Set("Content-Length", strconv.Itoa(telegramBotUploadLimit+1))
		_, _ = w.Write(relayTestMP4)
	})
	client, _ := newTestRelayClient(t, fake.server.URL)

	_, err := client.Fetch(context.Background(), relayPlatformYouTube, relayTestShortsID)

	if errors.Is(err, errRelayTooLarge) == false {
		t.Fatalf("want errRelayTooLarge, got %v", err)
	}

	if recordedFailureCount(relayTestService) != 0 {
		t.Errorf("too large is no outage")
	}
}

func TestRelayFetchRejectsA200WithoutContentLength(t *testing.T) {
	fake := startRelayTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", relayVideoType)
		_, _ = w.Write(relayTestMP4)
		w.(http.Flusher).Flush()
		_, _ = w.Write(relayTestMP4)
	})
	client, _ := newTestRelayClient(t, fake.server.URL)

	_, err := client.Fetch(context.Background(), relayPlatformYouTube, relayTestShortsID)

	if errors.Is(err, errRelayBroken) == false {
		t.Fatalf("want errRelayBroken, got %v", err)
	}

	if files := relayTempFiles(t, client); len(files) != 0 {
		t.Errorf("file kept: %v", files)
	}

	if recordedFailureCount(relayTestService) != 1 {
		t.Errorf("a 200 without Content-Length must count as an outage")
	}
}

// relayTestFillReader is an endless body of "v" bytes.
type relayTestFillReader struct{}

// Read fills p.
func (relayTestFillReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'v'
	}

	return len(p), nil
}

// relayTestVideoResponse is a 200 mp4 answer with body and contentLength.
func relayTestVideoResponse(body io.Reader, contentLength int64) *http.Response {
	header := http.Header{}
	header.Set("Content-Type", relayVideoType)

	return &http.Response{StatusCode: http.StatusOK, Header: header, ContentLength: contentLength, Body: io.NopCloser(body)}
}

func TestRelayStoreVideoCutsABodyLongerThanTheLimit(t *testing.T) {
	client, _ := newTestRelayClient(t, "http://127.0.0.1:1")
	body := io.MultiReader(bytes.NewReader(relayTestMP4), relayTestFillReader{})
	resp := relayTestVideoResponse(body, telegramBotUploadLimit)

	_, err := client.storeVideo(context.Background(), resp, relayPlatformYouTube)

	if errors.Is(err, errRelayTooLarge) == false {
		t.Fatalf("want errRelayTooLarge, got %v", err)
	}

	if files := relayTempFiles(t, client); len(files) != 0 {
		t.Errorf("over-limit file kept: %v", files)
	}
}

func TestRelayStoreVideoReadsOnlyTheHeadOfANonMP4Body(t *testing.T) {
	client, _ := newTestRelayClient(t, "http://127.0.0.1:1")
	page := bytes.Repeat([]byte("<html>"), 1024*1024)
	reader := bytes.NewReader(page)
	resp := relayTestVideoResponse(reader, int64(len(page)))

	_, err := client.storeVideo(context.Background(), resp, relayPlatformYouTube)

	if errors.Is(err, errRelayBroken) == false {
		t.Fatalf("want errRelayBroken, got %v", err)
	}

	if read := len(page) - reader.Len(); read != relayMP4HeadSize {
		t.Errorf("read %d bytes of a non-mp4 body, want %d", read, relayMP4HeadSize)
	}

	if files := relayTempFiles(t, client); len(files) != 0 {
		t.Errorf("file written for a non-mp4 body: %v", files)
	}
}

func TestRelayFetchRejectsABodyWithoutFtyp(t *testing.T) {
	bodies := map[string][]byte{
		"html page":  []byte("<!doctype html><html><body>error</body></html>"),
		"tiny body":  []byte("abc"),
		"empty body": {},
	}

	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			fake := startRelayTestServer(t, serveRelayVideo("", body))
			client, _ := newTestRelayClient(t, fake.server.URL)

			_, err := client.Fetch(context.Background(), relayPlatformYouTube, relayTestShortsID)

			if errors.Is(err, errRelayBroken) == false {
				t.Fatalf("want errRelayBroken, got %v", err)
			}

			if files := relayTempFiles(t, client); len(files) != 0 {
				t.Errorf("rejected file kept: %v", files)
			}

			if recordedFailureCount(relayTestService) != 1 {
				t.Errorf("a non-mp4 body must count as an outage")
			}
		})
	}
}

func TestRelayFetchRejectsA200ThatIsNotVideo(t *testing.T) {
	fake := startRelayTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write(relayTestMP4)
	})
	client, _ := newTestRelayClient(t, fake.server.URL)

	_, err := client.Fetch(context.Background(), relayPlatformYouTube, relayTestShortsID)

	if errors.Is(err, errRelayBroken) == false {
		t.Fatalf("want errRelayBroken, got %v", err)
	}
}

func TestRelayErrorCodesFallThroughWithTheirOutageVerdict(t *testing.T) {
	cases := []struct {
		status     int
		code       string
		wantOutage bool
	}{
		{http.StatusBadRequest, relayCodeBadRequest, true},
		{http.StatusUnauthorized, relayCodeUnauthorized, true},
		{http.StatusForbidden, relayCodePlatformDisabled, false},
		{http.StatusNotFound, relayCodeNotFound, true},
		{http.StatusUnprocessableEntity, relayCodeVideoUnavailable, false},
		{http.StatusUnprocessableEntity, relayCodePrivateVideo, false},
		{http.StatusUnprocessableEntity, relayCodeAgeRestricted, false},
		{http.StatusUnprocessableEntity, relayCodeLiveStream, false},
		{http.StatusUnprocessableEntity, relayCodeTooLong, false},
		{http.StatusUnprocessableEntity, relayCodeTooLarge, false},
		{http.StatusTooManyRequests, relayCodeRateLimited, false},
		{http.StatusBadGateway, relayCodeBotCheck, true},
		{http.StatusBadGateway, relayCodeYtDlpFailed, true},
		{http.StatusServiceUnavailable, relayCodeBusy, false},
		{http.StatusGatewayTimeout, relayCodeTimeout, true},
		{http.StatusInternalServerError, "SOMETHING_NEW", true},
	}

	for _, testCase := range cases {
		t.Run(testCase.code, func(t *testing.T) {
			fake := startRelayTestServer(t, serveRelayError(testCase.status, testCase.code))
			client, _ := newTestRelayClient(t, fake.server.URL)

			video, err := client.Fetch(context.Background(), relayPlatformYouTube, relayTestShortsID)

			var failure *relayError

			if (errors.As(err, &failure) == false) || (video.Path != "") {
				t.Fatalf("want a relayError and no file, got %+v, %v", video, err)
			}

			if (failure.Code != testCase.code) || (failure.Status != testCase.status) || (failure.IsOutage != testCase.wantOutage) {
				t.Errorf("got code %s status %d outage %v", failure.Code, failure.Status, failure.IsOutage)
			}

			wantFailures := 0

			if testCase.wantOutage == true {
				wantFailures = 1
			}

			if got := recordedFailureCount(relayTestService); got != wantFailures {
				t.Errorf("outage tracker has %d failure(s), want %d", got, wantFailures)
			}
		})
	}
}

func TestRelayUnreadableErrorBodyIsAnOutage(t *testing.T) {
	fake := startRelayTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, "<html>proxy error</html>")
	})
	client, _ := newTestRelayClient(t, fake.server.URL)

	_, err := client.Fetch(context.Background(), relayPlatformYouTube, relayTestShortsID)

	if (errors.Is(err, errRelayBroken) == false) || (recordedFailureCount(relayTestService) != 1) {
		t.Errorf("want an outage errRelayBroken, got %v", err)
	}
}

func TestRelayPlatformDisabledSkipsThePlatformUntilTheProbeSeesItEnabled(t *testing.T) {
	enabled := false
	var mu sync.Mutex
	fake := startRelayTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		isEnabled := enabled
		mu.Unlock()

		if r.URL.Path == relayHealthPath {
			_, _ = fmt.Fprintf(w, `{"ok":true,"breaker_open":[],"platforms":{"youtube":{"enabled":%v,"breaker":"closed"}}}`, isEnabled)

			return
		}

		if isEnabled == false {
			serveRelayError(http.StatusForbidden, relayCodePlatformDisabled)(w, r)

			return
		}

		serveRelayVideo("", relayTestMP4)(w, r)
	})
	client, alerts := newTestRelayClient(t, fake.server.URL)
	configureTestRelayEnv(t, fake.server.URL, client.tokenFile)

	_, firstErr := client.Fetch(context.Background(), relayPlatformYouTube, relayTestShortsID)
	_, secondErr := client.Fetch(context.Background(), relayPlatformYouTube, relayTestShortsID)

	if (firstErr == nil) || (errors.Is(secondErr, errRelayPlatformDisabled) == false) {
		t.Fatalf("first %v, second %v", firstErr, secondErr)
	}

	if got := len(fake.recorded()); got != 1 {
		t.Errorf("a disabled platform must not be called again, %d requests", got)
	}

	probeRelay()

	if (isRelayPlatformDisabled(relayPlatformYouTube) == false) || (recordedFailureCount(relayTestProbeKey) != 0) {
		t.Errorf("disabled platform: probe must keep it skipped and count no miss")
	}

	mu.Lock()
	enabled = true
	mu.Unlock()

	probeRelay()

	if _, err := client.Fetch(context.Background(), relayPlatformYouTube, relayTestShortsID); err != nil {
		t.Errorf("after the probe saw it enabled: %v", err)
	}

	if got := alerts.recordedAlerts(); len(got) != 0 {
		t.Errorf("no alert expected, got %v", got)
	}
}

func TestRelayConnectionRefusedIsNoOutageAndFast(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")

	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	address := listener.Addr().String()
	listener.Close()

	client, _ := newTestRelayClient(t, "http://"+address)
	start := time.Now()

	_, fetchErr := client.Fetch(context.Background(), relayPlatformYouTube, relayTestShortsID)

	var failure *relayError

	if (errors.As(fetchErr, &failure) == false) || (failure.IsOutage == true) {
		t.Fatalf("want a non-outage relayError, got %v", fetchErr)
	}

	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("refused connection took %v", elapsed)
	}

	if recordedFailureCount(relayTestService) != 0 {
		t.Errorf("refused connection reported as an outage")
	}
}

func TestRelayChainCancelledIsNoOutage(t *testing.T) {
	release := make(chan struct{})
	fake := startRelayTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		<-release
	})
	t.Cleanup(func() { close(release) })
	client, _ := newTestRelayClient(t, fake.server.URL)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)

	_, err := client.Fetch(ctx, relayPlatformYouTube, relayTestShortsID)

	var failure *relayError

	if (errors.As(err, &failure) == false) || (failure.IsOutage == true) {
		t.Fatalf("want a non-outage relayError, got %v", err)
	}
}

func TestRelayTransportErrorVerdicts(t *testing.T) {
	alive := context.Background()
	over, cancel := context.WithCancel(context.Background())
	cancel()
	refused := &net.OpError{Op: "dial", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}

	cases := []struct {
		name       string
		chainCtx   context.Context
		err        error
		wantOutage bool
	}{
		{"call timeout with the chain alive", alive, context.DeadlineExceeded, true},
		{"chain over", over, context.Canceled, false},
		{"connection refused", alive, refused, false},
		{"connection reset", alive, errors.New("connection reset by peer"), true},
	}

	for _, testCase := range cases {
		var failure *relayError

		err := relayTransportError(testCase.chainCtx, testCase.err)

		if (errors.As(err, &failure) == false) || (failure.IsOutage != testCase.wantOutage) {
			t.Errorf("%s: got %v", testCase.name, err)
		}
	}
}

func TestRelayCallBudget(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name         string
		chainLeft    time.Duration
		hasDeadline  bool
		maxTimeout   time.Duration
		wantTimeout  time.Duration
		wantDeadline time.Duration
		wantOK       bool
	}{
		{"fresh 90 s chain", 90 * time.Second, true, 45 * time.Second, 45 * time.Second, 40 * time.Second, true},
		{"chain shortens the call", 60 * time.Second, true, 45 * time.Second, 25 * time.Second, 20 * time.Second, true},
		{"exactly the minimum", 50 * time.Second, true, 45 * time.Second, 15 * time.Second, 10 * time.Second, true},
		{"below the minimum", 49 * time.Second, true, 45 * time.Second, 14 * time.Second, 9 * time.Second, false},
		{"no time left", 10 * time.Second, true, 45 * time.Second, -25 * time.Second, -30 * time.Second, false},
		{"no chain deadline", 0, false, 45 * time.Second, 45 * time.Second, 40 * time.Second, true},
		{"deadline capped at 60 s", 0, false, 120 * time.Second, 120 * time.Second, 60 * time.Second, true},
	}

	for _, testCase := range cases {
		ctx := context.Background()
		cancel := func() {}

		if testCase.hasDeadline == true {
			ctx, cancel = context.WithDeadline(ctx, now.Add(testCase.chainLeft))
		}

		timeout, deadline, ok := relayCallBudget(ctx, testCase.maxTimeout, now)
		cancel()

		if (timeout != testCase.wantTimeout) || (deadline != testCase.wantDeadline) || (ok != testCase.wantOK) {
			t.Errorf("%s: got %v, %v, %v", testCase.name, timeout, deadline, ok)
		}
	}
}

func TestRelayMinimumConfiguredTimeoutFitsTheRelayMinimumDeadline(t *testing.T) {
	minTimeout := time.Duration(config.MinRelayTimeoutSeconds) * time.Second
	_, deadline, ok := relayCallBudget(context.Background(), minTimeout, time.Now())

	if (ok == false) || (deadline != relayMinDeadline) {
		t.Errorf("minimum configured timeout %v: got deadline %v, ok %v", minTimeout, deadline, ok)
	}
}

func TestRelayCheckFetchBudgetBoundary(t *testing.T) {
	client, _ := newTestRelayClient(t, "http://127.0.0.1:1")
	now := time.Now()
	minimumLeft := relayNextMethodReserve + relayTransferMargin + relayMinDeadline
	cases := []struct {
		name         string
		chainLeft    time.Duration
		wantErr      error
		wantDeadline time.Duration
	}{
		{"exactly the minimum", minimumLeft, nil, relayMinDeadline},
		{"one millisecond short", minimumLeft - time.Millisecond, errRelayNoBudget, 0},
	}

	for _, testCase := range cases {
		ctx, cancel := context.WithDeadline(context.Background(), now.Add(testCase.chainLeft))
		budget, err := client.checkFetch(ctx, relayPlatformYouTube, relayTestShortsID, now)
		cancel()

		if errors.Is(err, testCase.wantErr) == false {
			t.Errorf("%s: got %v, want %v", testCase.name, err, testCase.wantErr)
		}

		if (err == nil) && ((budget.deadline != testCase.wantDeadline) || (budget.timeout != (relayMinDeadline + relayTransferMargin))) {
			t.Errorf("%s: budget %+v", testCase.name, budget)
		}
	}
}

func TestRelayFetchNeverSendsADeadlineBelowTheMinimum(t *testing.T) {
	fake := startRelayTestServer(t, serveRelayVideo("", relayTestMP4))
	client, _ := newTestRelayClient(t, fake.server.URL)
	minimumLeft := relayNextMethodReserve + relayTransferMargin + relayMinDeadline

	for _, extra := range []time.Duration{0, time.Millisecond, 5 * time.Millisecond, 50 * time.Millisecond} {
		ctx, cancel := context.WithTimeout(context.Background(), minimumLeft+extra)
		_, _ = client.Fetch(ctx, relayPlatformYouTube, relayTestShortsID)
		cancel()
	}

	calls := fake.recorded()

	if len(calls) == 0 {
		t.Fatalf("no call reached the relay")
	}

	for _, call := range calls {
		sent := relayRequest{}

		if err := json.Unmarshal(call.body, &sent); err != nil {
			t.Fatalf("decode %q: %v", call.body, err)
		}

		if sent.DeadlineMs < relayMinDeadline.Milliseconds() {
			t.Errorf("sent deadline_ms %d, below the relay's minimum", sent.DeadlineMs)
		}
	}
}

func TestRelayWithoutBudgetSendsNothing(t *testing.T) {
	fake := startRelayTestServer(t, serveRelayVideo("", relayTestMP4))
	client, _ := newTestRelayClient(t, fake.server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	_, err := client.Fetch(ctx, relayPlatformYouTube, relayTestShortsID)

	if (errors.Is(err, errRelayNoBudget) == false) || (len(fake.recorded()) != 0) {
		t.Errorf("want errRelayNoBudget and no request, got %v, %d request(s)", err, len(fake.recorded()))
	}
}

func TestRelayMissingTokenFileIsAnOutage(t *testing.T) {
	fake := startRelayTestServer(t, serveRelayVideo("", relayTestMP4))
	client, _ := newTestRelayClient(t, fake.server.URL)
	client.tokenFile = filepath.Join(t.TempDir(), "missing.token")

	_, err := client.Fetch(context.Background(), relayPlatformYouTube, relayTestShortsID)

	if (err == nil) || (len(fake.recorded()) != 0) || (recordedFailureCount(relayTestService) != 1) {
		t.Errorf("want an outage before any request, got %v", err)
	}
}

func TestRelayRefusesIDsTheRelayWouldReject(t *testing.T) {
	fake := startRelayTestServer(t, serveRelayVideo("", relayTestMP4))
	client, _ := newTestRelayClient(t, fake.server.URL)
	cases := []struct{ platform, id string }{
		{relayPlatformYouTube, "bc-OZm4lpd"},
		{relayPlatformYouTube, "bc-OZm4lpdsX"},
		{relayPlatformTikTok, "12345"},
		{relayPlatformFacebook, "pfbid0abc"},
		{"vimeo", "12345678901"},
	}

	for _, testCase := range cases {
		if _, err := client.Fetch(context.Background(), testCase.platform, testCase.id); errors.Is(err, errRelayBadID) == false {
			t.Errorf("%s %q: got %v", testCase.platform, testCase.id, err)
		}
	}

	if got := len(fake.recorded()); got != 0 {
		t.Errorf("bad ids reached the relay: %d request(s)", got)
	}
}

// configureTestRelayEnv points the relay config at the fake relay for
// YouTube.
func configureTestRelayEnv(t *testing.T, baseURL, tokenFile string) {
	t.Helper()

	t.Setenv("RELAY_URL", baseURL)
	t.Setenv("RELAY_TOKEN_FILE", tokenFile)
	t.Setenv("RELAY_PLATFORMS", relayPlatformYouTube)
}

func TestYouTubeRelayExtractorSendsTheShortWithAnEscapedCaption(t *testing.T) {
	meta := encodeRelayMeta(t, map[string]any{"title": "Tom & <Jerry>", "uploader": "A<b>", "height": 1080, "duration": 20})
	fake := startRelayTestServer(t, serveRelayVideo(meta, relayTestMP4))
	client, _ := newTestRelayClient(t, fake.server.URL)
	extractor := &YouTubeRelayExtractor{client: client}

	media, err := extractor.ExtractMedia(context.Background(), relayTestShortsURL+"?feature=share")

	if (err != nil) || (len(media) != 1) {
		t.Fatalf("got %d item(s), %v", len(media), err)
	}

	item := media[0]

	if (item.Type != MediaTypeVideo) || (item.Filename == "") {
		t.Errorf("item %+v", item)
	}

	for _, want := range []string{"Tom &amp; &lt;Jerry&gt;", "A&lt;b&gt;", "the original on YouTube"} {
		if strings.Contains(item.Caption, want) == false {
			t.Errorf("caption %q lacks %q", item.Caption, want)
		}
	}

	if (strings.Contains(item.Caption, "<Jerry>") == true) || (recordedFailureCount(relayTestService) != 0) {
		t.Errorf("caption not escaped or outage recorded: %q", item.Caption)
	}
}

func TestYouTubeRelayExtractorNeverSendsAnIDOtherThan11Characters(t *testing.T) {
	fake := startRelayTestServer(t, serveRelayVideo("", relayTestMP4))
	client, _ := newTestRelayClient(t, fake.server.URL)
	extractor := &YouTubeRelayExtractor{client: client}

	for _, link := range []string{"https://www.youtube.com/shorts/bc-OZm4lpd", "https://www.youtube.com/shorts/bc-OZm4lpdsXY", "https://www.youtube.com/post/Ugkx123456789"} {
		if _, err := extractor.ExtractMedia(context.Background(), link); errors.Is(err, errRelayBadID) == false {
			t.Errorf("%s: got %v", link, err)
		}
	}

	if got := len(fake.recorded()); got != 0 {
		t.Errorf("relay called %d time(s)", got)
	}
}

func TestYouTubeShortsStrategyDependsOnTheRelayConfig(t *testing.T) {
	markDatabaseOpenForTest(t)
	tokenFile := writeRelayTestToken(t)
	headChain := []ExtractionMethod{MethodYtDlp, MethodYouTubeApify}
	relayChain := []ExtractionMethod{MethodYouTubeRelay, MethodYouTubeApify}
	cases := []struct {
		name      string
		url       string
		tokenFile string
		platforms string
		want      []ExtractionMethod
	}{
		{"unconfigured", "", "", "", headChain},
		{"configured", "http://127.0.0.1:8791", tokenFile, "youtube", relayChain},
		{"trailing slash", "http://127.0.0.1:8791/", tokenFile, "TikTok, YouTube", relayChain},
		{"youtube not listed", "http://127.0.0.1:8791", tokenFile, "tiktok", headChain},
		{"no token file", "http://127.0.0.1:8791", "", "youtube", headChain},
		{"public host refused", "http://203.0.113.5:8791", tokenFile, "youtube", headChain},
		{"host name refused", "http://localhost:8791", tokenFile, "youtube", headChain},
		{"https refused", "https://127.0.0.1:8791", tokenFile, "youtube", headChain},
		{"path refused", "http://127.0.0.1:8791/v1", tokenFile, "youtube", headChain},
	}

	for _, testCase := range cases {
		t.Setenv("ENABLE_YOUTUBE", "true")
		t.Setenv("RELAY_URL", testCase.url)
		t.Setenv("RELAY_TOKEN_FILE", testCase.tokenFile)
		t.Setenv("RELAY_PLATFORMS", testCase.platforms)

		strategy := GetExtractionStrategy(relayTestShortsURL)
		extractor := NewYouTubeRelayExtractor(t.TempDir())
		isRelayChain := slices.Equal(testCase.want, relayChain)

		if (slices.Equal(strategy.Methods, testCase.want) == false) || (strategy.Sequential == false) {
			t.Errorf("%s: methods %v, want %v", testCase.name, strategy.Methods, testCase.want)
		}

		if (extractor != nil) != isRelayChain {
			t.Errorf("%s: extractor %v, want one only with the relay chain", testCase.name, extractor)
		}
	}
}

func TestYouTubeShortsStrategyHasNoRelayMethodWithoutARelayExtractor(t *testing.T) {
	configureTestRelayEnv(t, "http://127.0.0.1:8791", writeRelayTestToken(t))
	t.Setenv("ENABLE_YOUTUBE", "true")
	relaySupportedPlatforms[relayPlatformYouTube] = false
	t.Cleanup(func() { relaySupportedPlatforms[relayPlatformYouTube] = true })

	strategy := GetExtractionStrategy(relayTestShortsURL)
	extractor := NewYouTubeRelayExtractor(t.TempDir())

	if (extractor != nil) || (slices.Contains(strategy.Methods, MethodYouTubeRelay) == true) {
		t.Errorf("unsupported platform: extractor %v, methods %v", extractor, strategy.Methods)
	}
}

func TestRelayConfigProblemsWarnAboutARaisedTimeoutOnlyWhileTheRelayIsOn(t *testing.T) {
	raised := config.RelayTimeoutSeconds{Raw: 10, Clamped: 15}
	urlErr := errors.New("bad url")

	if got := relayConfigProblems(nil, raised, false); len(got) != 0 {
		t.Errorf("relay off: got %v", got)
	}

	if got := relayConfigProblems(nil, raised, true); (len(got) != 1) || (strings.Contains(got[0], "RELAY_TIMEOUT_SECONDS=10") == false) {
		t.Errorf("relay on: got %v", got)
	}

	if got := relayConfigProblems(urlErr, raised, false); (len(got) != 1) || (strings.Contains(got[0], "bad url") == false) {
		t.Errorf("bad url: got %v", got)
	}
}

func TestYouTubePostStrategyIgnoresTheRelay(t *testing.T) {
	configureTestRelayEnv(t, "http://127.0.0.1:8791", writeRelayTestToken(t))

	strategy := GetExtractionStrategy("https://www.youtube.com/post/UgkxAbCdEf")

	if slices.Equal(strategy.Methods, []ExtractionMethod{MethodYouTubePost}) == false {
		t.Errorf("methods %v", strategy.Methods)
	}
}

func TestRelayChainFallsThroughToTheNextMethod(t *testing.T) {
	fake := startRelayTestServer(t, serveRelayError(http.StatusServiceUnavailable, relayCodeBusy))
	client, _ := newTestRelayClient(t, fake.server.URL)
	configureTestRelayEnv(t, fake.server.URL, client.tokenFile)
	markDatabaseOpenForTest(t)
	downloader := &SmartDownloader{youtubeRelay: &YouTubeRelayExtractor{client: client}, tempDir: client.tempDir}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	_, err := downloader.downloadMediaWithContext(ctx, relayTestShortsURL, newAllowedDownload(relayTestShortsURL))

	if (err == nil) || (errors.Is(err, errApifyNotConfigured) == false) {
		t.Errorf("want the chain to reach Apify (not configured here), got %v", err)
	}

	if got := len(fake.recorded()); got != 1 {
		t.Errorf("relay called %d time(s), want 1", got)
	}
}

func TestRelayNotConfiguredMethodFails(t *testing.T) {
	downloader := &SmartDownloader{}

	_, err := downloader.youtubeRelayExtract(context.Background(), relayTestShortsURL)

	if errors.Is(err, errRelayNotConfigured) == false {
		t.Errorf("got %v", err)
	}
}

// serveRelayHealth answers /healthz with body.
func serveRelayHealth(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", relayJSONType)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}
}

const (
	relayHealthyBody     = `{"ok":true,"yt_dlp":"2026.08.19","breaker_open":[],"global":{"hour":0,"day":0,"hourly_limit":30,"daily_limit":150},"platforms":{"youtube":{"enabled":true,"breaker":"closed","breaker_left_s":0,"hour":0,"day":0,"hourly_limit":20,"daily_limit":100},"tiktok":{"enabled":false,"breaker":"closed","breaker_left_s":0,"hour":0,"day":0,"hourly_limit":10,"daily_limit":50}}}`
	relayBreakerOpenBody = `{"ok":true,"breaker_open":["youtube"],"platforms":{"youtube":{"enabled":true,"breaker":"open","breaker_left_s":21000}}}`
	relayNotOKBody       = `{"ok":false,"platforms":{"youtube":{"enabled":true,"breaker":"closed"}}}`
	relayNoYouTubeBody   = `{"ok":true,"breaker_open":[],"platforms":{"tiktok":{"enabled":true,"breaker":"closed"}}}`
)

func TestRelayProbeOutcomes(t *testing.T) {
	cases := []struct {
		name        string
		handler     http.HandlerFunc
		wantFailure bool
	}{
		{"healthy", serveRelayHealth(http.StatusOK, relayHealthyBody), false},
		{"breaker open", serveRelayHealth(http.StatusOK, relayBreakerOpenBody), true},
		{"ok false", serveRelayHealth(http.StatusOK, relayNotOKBody), true},
		{"platform missing", serveRelayHealth(http.StatusOK, relayNoYouTubeBody), true},
		{"HTTP 500", serveRelayHealth(http.StatusInternalServerError, relayHealthyBody), true},
		{"not JSON", serveRelayHealth(http.StatusOK, "<html>"), true},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fake := startRelayTestServer(t, testCase.handler)
			client, _ := newTestRelayClient(t, fake.server.URL)
			configureTestRelayEnv(t, fake.server.URL, client.tokenFile)

			probeRelay()

			calls := fake.recorded()

			if (len(calls) != 1) || (calls[0].path != relayHealthPath) || (calls[0].authorization != "") {
				t.Fatalf("probe requests %+v, want one GET /healthz without a token", calls)
			}

			wantFailures := 0

			if testCase.wantFailure == true {
				wantFailures = 1
			}

			if got := recordedFailureCount(relayTestProbeKey); got != wantFailures {
				t.Errorf("probe failures %d, want %d", got, wantFailures)
			}

			if recordedFailureCount(relayTestService) != 0 {
				t.Errorf("the probe must not touch the traffic key")
			}
		})
	}
}

func TestRelayProbeAlertsAfterThreeMissesAndRecovers(t *testing.T) {
	healthy := false
	var mu sync.Mutex
	fake := startRelayTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		isHealthy := healthy
		mu.Unlock()

		body := relayBreakerOpenBody

		if isHealthy == true {
			body = relayHealthyBody
		}

		serveRelayHealth(http.StatusOK, body)(w, r)
	})
	client, alerts := newTestRelayClient(t, fake.server.URL)
	configureTestRelayEnv(t, fake.server.URL, client.tokenFile)

	probeRelay()
	probeRelay()

	if got := alerts.alertsWithKey("outage:" + relayTestProbeKey); len(got) != 0 {
		t.Fatalf("alerted after 2 misses: %v", got)
	}

	probeRelay()

	if got := alerts.alertsWithKey("outage:" + relayTestProbeKey); len(got) != 1 {
		t.Fatalf("want one alert after 3 misses, got %v", got)
	}

	mu.Lock()
	healthy = true
	mu.Unlock()

	probeRelay()

	if got := alerts.alertsWithKey("recovery:" + relayTestProbeKey); len(got) != 1 {
		t.Errorf("want a recovery notice, got %v", alerts.recordedAlerts())
	}
}

func TestRelayProbeCountsARefusedConnectionAsAMiss(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")

	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	address := listener.Addr().String()
	listener.Close()

	_, _ = newTestRelayClient(t, "http://"+address)
	configureTestRelayEnv(t, "http://"+address, writeRelayTestToken(t))

	probeRelay()

	if recordedFailureCount(relayTestProbeKey) != 1 {
		t.Errorf("refused /healthz must be a probe miss")
	}
}

func TestRelayProbeSkippedWhenUnconfigured(t *testing.T) {
	fake := startRelayTestServer(t, serveRelayHealth(http.StatusOK, relayHealthyBody))
	_, _ = newTestRelayClient(t, fake.server.URL)
	t.Setenv("RELAY_URL", fake.server.URL)
	t.Setenv("RELAY_TOKEN_FILE", writeRelayTestToken(t))
	t.Setenv("RELAY_PLATFORMS", "")

	probeRelay()

	if got := len(fake.recorded()); got != 0 {
		t.Errorf("unconfigured relay probed %d time(s)", got)
	}
}
