package services

// Hermetic tests for the periodic redirector health probes. Every request a
// probe makes is dialed to a local httptest server by probeServeHTTPS, and the
// outage tracker's notification hooks are replaced by probeCaptureAdminAlerts,
// so these tests never touch the network and can never send a Telegram message.

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// Hosts the probes see here are the production ones on purpose: the near-miss
// between a CDN host and the origin domain it belongs to
// (scontent.cdninstagram.com vs instagram.com) is exactly what the probe's
// validation has to get right. No name is ever resolved — probeServeHTTPS
// dials the local test server for every request, whatever hostname it names.
const (
	probeKKInstagramHost = "kkinstagram.com"
	probeKKTikTokHost    = "kktiktok.com"
	probeVxredditHost    = "vxreddit.com"
	probeFacebedHost     = "www.facebed.com"
	probeFacebedCDNHost  = "scontent.xx.fbcdn.net"
	probeFacebedCDNPath  = "/v/t39.30808-6/probe_n.jpg"

	probeInstagramOrigin = "instagram.com"
	probeTikTokOrigin    = "tiktok.com"

	probeInstagramCDNHost = "scontent.cdninstagram.com"
	probeTikTokCDNHost    = "v16m.tiktokcdn-us.com"

	probeInstagramCDNURL       = "https://" + probeInstagramCDNHost + "/o1/v/t2/f2/m86/probe.mp4?_nc_cat=1"
	probeTikTokCDNURL          = "https://" + probeTikTokCDNHost + "/probe/video/tos/alisg/x/?mime_type=video_mp4"
	probeOriginBounceURL       = "https://www." + probeInstagramOrigin + "/reel/" + kkInstagramProbeShortcode + "/"
	probeTikTokOriginBounceURL = "https://www." + probeTikTokOrigin + "/@i/video/" + kkTikTokProbeVideoID

	probeKKInstagramURL = "https://" + probeKKInstagramHost + "/reel/" + kkInstagramProbeShortcode + "/"
)

// Tracker keys CheckRedirectorHealth reports under, kept separate from the
// keys real user traffic uses.
const (
	probeKKInstagramService = kkInstagramServiceName + "-probe"
	probeKKTikTokService    = kkTikTokServiceName + "-probe"
	probeVxredditService    = redditVxredditServiceName + "-probe"
	probeFacebedService     = facebedServiceName + "-probe"
)

// probeMediaPayload is comfortably larger than redirectorMinMediaSize, so a
// healthy probe clears the media-size check.
var probeMediaPayload = bytes.Repeat([]byte("0"), int(redirectorMinMediaSize)+2048)

// probeRequestRecord is one request the test server saw.
type probeRequestRecord struct {
	target    string
	userAgent string
}

// probeRequestLog collects the requests a probe made, including the hops it
// followed to the CDN. It is written from the test server's goroutines, so all
// access is mutex-guarded.
type probeRequestLog struct {
	mu      sync.Mutex
	entries []probeRequestRecord
}

// record appends the request's "<host><path>" target and User-Agent.
func (l *probeRequestLog) record(r *http.Request) {
	l.mu.Lock()
	l.entries = append(l.entries, probeRequestRecord{target: r.Host + r.URL.Path, userAgent: r.UserAgent()})
	l.mu.Unlock()
}

// snapshot returns a copy of the recorded requests, safe to read from the test goroutine.
func (l *probeRequestLog) snapshot() []probeRequestRecord {
	l.mu.Lock()
	result := append([]probeRequestRecord(nil), l.entries...)
	l.mu.Unlock()

	return result
}

// sawTarget reports whether any request was made to "<host><path>".
func (l *probeRequestLog) sawTarget(target string) bool {
	result := false

	for _, entry := range l.snapshot() {
		if entry.target == target {
			result = true

			break
		}
	}

	return result
}

// probeAdminAlert is one admin notification raised through the outage tracker.
type probeAdminAlert struct {
	key  string
	text string
}

// probeAlertRecorder captures the outage tracker's admin notifications in
// place of the real Telegram calls.
type probeAlertRecorder struct {
	mu     sync.Mutex
	alerts []probeAdminAlert
	resets []string
}

// alertKeys returns the alert keys in the order they were raised.
func (r *probeAlertRecorder) alertKeys() []string {
	result := []string{}

	r.mu.Lock()

	for _, alert := range r.alerts {
		result = append(result, alert.key)
	}

	r.mu.Unlock()

	return result
}

// resetKeys returns the alert keys whose rate-limit stamp was cleared.
func (r *probeAlertRecorder) resetKeys() []string {
	r.mu.Lock()
	result := append([]string(nil), r.resets...)
	r.mu.Unlock()

	return result
}

// textFor returns the text of the first alert recorded under key.
func (r *probeAlertRecorder) textFor(key string) string {
	result := ""

	r.mu.Lock()

	for _, alert := range r.alerts {
		if alert.key == key {
			result = alert.text

			break
		}
	}

	r.mu.Unlock()

	return result
}

// probeCaptureAdminAlerts replaces the outage tracker's notification hooks for
// the duration of the test — so a probe can never reach the real Telegram API
// even when a token and admin ID are present in the environment — and returns
// the recorder holding whatever the probes raised.
func probeCaptureAdminAlerts(t *testing.T) *probeAlertRecorder {
	t.Helper()

	recorder := &probeAlertRecorder{}
	savedNotify := notifyAdmin
	savedReset := resetAdminAlert

	notifyAdmin = func(alertKey, text string) bool {
		recorder.mu.Lock()
		recorder.alerts = append(recorder.alerts, probeAdminAlert{key: alertKey, text: text})
		recorder.mu.Unlock()

		return true
	}

	resetAdminAlert = func(alertKey string) {
		recorder.mu.Lock()
		recorder.resets = append(recorder.resets, alertKey)
		recorder.mu.Unlock()
	}

	t.Cleanup(func() {
		notifyAdmin = savedNotify
		resetAdminAlert = savedReset
	})

	return recorder
}

// probeForgetTrackerState drops the tracker entries for the given services
// before and after a test, because serviceHealthState is shared package state.
func probeForgetTrackerState(t *testing.T, services ...string) {
	t.Helper()

	forget := func() {
		serviceHealthMu.Lock()

		for _, service := range services {
			delete(serviceHealthState, service)
		}

		serviceHealthMu.Unlock()
	}

	forget()
	t.Cleanup(forget)
}

// probeTrackerFailures returns the consecutive failure count the outage tracker
// holds for a service, zero when it has no entry at all.
func probeTrackerFailures(service string) int {
	result := 0

	serviceHealthMu.Lock()
	entry, exists := serviceHealthState[service]

	if exists {
		result = entry.consecutiveFailures
	}

	serviceHealthMu.Unlock()

	return result
}

// probeServeHTTPS starts a TLS test server for handler and points the shared
// HTTP client the probes use at it: every request is dialed straight to this
// server whatever hostname its URL names, so no DNS lookup and no outbound
// connection ever happens. The previous client is restored when the test ends.
func probeServeHTTPS(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()

	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)

	serverTransport, ok := server.Client().Transport.(*http.Transport)

	if ok == false {
		t.Fatalf("httptest client transport is %T, cannot reuse its TLS config", server.Client().Transport)
	}

	// The test certificate is issued for example.com, so pin the handshake to
	// that name while keeping real verification against the server's own CA.
	tlsConfig := serverTransport.TLSClientConfig.Clone()
	tlsConfig.ServerName = "example.com"

	address := server.Listener.Addr().String()
	savedClient := SharedHTTPClient

	SharedHTTPClient = &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: tlsConfig,
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				dialer := &net.Dialer{Timeout: 5 * time.Second}

				return dialer.DialContext(ctx, network, address)
			},
		},
	}

	t.Cleanup(func() {
		SharedHTTPClient = savedClient
	})

	return server
}

// probeWriteMedia serves body as video/mp4, ignoring the write error that a
// client which stops reading at the size threshold inevitably causes.
func probeWriteMedia(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", "video/mp4")
	_, _ = w.Write(body)
}

// probeWriteHTML serves an HTML page, the shape a broken redirector returns
// with a 200 status instead of an error.
func probeWriteHTML(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, body)
}

// probeHealthyKKInstagramHandler answers like a working kk redirector: a 302
// off its own host to a CDN that serves real-sized video.
func probeHealthyKKInstagramHandler(w http.ResponseWriter, r *http.Request) {
	if r.Host == probeKKInstagramHost {
		http.Redirect(w, r, probeInstagramCDNURL, http.StatusFound)

		return
	}

	probeWriteMedia(w, probeMediaPayload)
}

// probeVxredditVideoHandler answers like vxreddit does for a video post: an
// OpenGraph page advertising the mp4 it re-serves from its own CDN.
func probeVxredditVideoHandler(w http.ResponseWriter, r *http.Request) {
	probeWriteHTML(w, `<html><head><meta property="og:video" content="https://`+probeVxredditHost+`/video/probe.mp4"></head></html>`)
}

// probeFacebedPhotoPage is facebed's answer for a public photo post.
const probeFacebedPhotoPage = `<meta property="og:title" content="Ludus Magnus Studio"/>
<meta property="og:site_name" content="facebed by pi.kt
⌚ 2026/10/17 17:52:06 UTC+07"/>
<meta property="og:url" content="https://www.facebook.com/photo?fbid=1220500346751179"/>
<meta property="og:image" content="https://` + probeFacebedCDNHost + probeFacebedCDNPath + `?_nc_cat=1&amp;oe=1"/>`

// probeFacebedLoginCard is facebed's answer for every post it cannot read.
const probeFacebedLoginCard = `<meta property="og:title" content="Log in or sign up to view"/>
<meta property="og:description" content="See posts, photos and more on Facebook."/>`

// probeFacebedPhotoHandler answers like facebed does for a public photo.
func probeFacebedPhotoHandler(w http.ResponseWriter, r *http.Request) {
	probeWriteHTML(w, probeFacebedPhotoPage)
}

// probeFacebedImageHandler serves the probe photo like the CDN does.
func probeFacebedImageHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/jpeg")
	_, _ = w.Write(probeMediaPayload)
}

// probeAllServicesHandler answers every probe target CheckRedirectorHealth
// visits and 404s anything else, so a wrong probe URL fails loudly.
func probeAllServicesHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Host {
	case probeKKInstagramHost:
		http.Redirect(w, r, probeInstagramCDNURL, http.StatusFound)
	case "www." + probeKKTikTokHost:
		http.Redirect(w, r, probeTikTokCDNURL, http.StatusFound)
	case probeVxredditHost:
		probeVxredditVideoHandler(w, r)
	case probeFacebedHost:
		probeFacebedPhotoHandler(w, r)
	case probeFacebedCDNHost:
		probeFacebedImageHandler(w, r)
	case probeInstagramCDNHost, probeTikTokCDNHost:
		probeWriteMedia(w, probeMediaPayload)
	default:
		http.NotFound(w, r)
	}
}

func TestProbeRedirectorOutcomes(t *testing.T) {
	cases := []struct {
		name        string
		handler     http.HandlerFunc
		wantFailure bool
	}{
		{
			name:    "302 off the redirector to CDN media",
			handler: probeHealthyKKInstagramHandler,
		},
		{
			name: "error status from the redirector",
			handler: func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "service unavailable", http.StatusServiceUnavailable)
			},
			wantFailure: true,
		},
		{
			name: "200 HTML on the redirector's own host",
			handler: func(w http.ResponseWriter, r *http.Request) {
				probeWriteHTML(w, "<!doctype html><title>kkinstagram</title>")
			},
			wantFailure: true,
		},
		{
			name: "bounce back to the origin platform",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.Host == probeKKInstagramHost {
					http.Redirect(w, r, probeOriginBounceURL, http.StatusFound)

					return
				}

				probeWriteHTML(w, "<!doctype html><title>Instagram</title>")
			},
			wantFailure: true,
		},
		{
			name: "media content-type with a suspiciously small body",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.Host == probeKKInstagramHost {
					http.Redirect(w, r, probeInstagramCDNURL, http.StatusFound)

					return
				}

				probeWriteMedia(w, probeMediaPayload[:512])
			},
			wantFailure: true,
		},
		{
			// The datacenter-IP block: the redirect itself works, and the CDN
			// answers with a media content-type and a full-sized body, so only
			// the status code gives the failure away.
			name: "CDN rejects the media request after a correct redirect",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.Host == probeKKInstagramHost {
					http.Redirect(w, r, probeInstagramCDNURL, http.StatusFound)

					return
				}

				w.Header().Set("Content-Type", "video/mp4")
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write(probeMediaPayload)
			},
			wantFailure: true,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			alerts := probeCaptureAdminAlerts(t)
			probeForgetTrackerState(t, probeKKInstagramService)
			probeServeHTTPS(t, testCase.handler)

			probeRedirector(probeKKInstagramService, probeKKInstagramURL, probeKKInstagramHost, probeInstagramOrigin)

			wantFailures := 0

			if testCase.wantFailure {
				wantFailures = 1
			}

			if got := probeTrackerFailures(probeKKInstagramService); got != wantFailures {
				t.Errorf("tracker holds %d consecutive failure(s) for %s, want %d", got, probeKKInstagramService, wantFailures)
			}

			if got := alerts.alertKeys(); len(got) != 0 {
				t.Errorf("a single probe must stay below the alert threshold, got alerts %v", got)
			}
		})
	}
}

func TestProbeRedirectorSendsBotUserAgent(t *testing.T) {
	// The redirectors serve media only to a bot UA — a browser UA gets a
	// 301 app-deeplink interstitial — so every hop has to carry it.
	requests := &probeRequestLog{}

	probeCaptureAdminAlerts(t)
	probeForgetTrackerState(t, probeKKInstagramService)
	probeServeHTTPS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.record(r)
		probeHealthyKKInstagramHandler(w, r)
	}))

	probeRedirector(probeKKInstagramService, probeKKInstagramURL, probeKKInstagramHost, probeInstagramOrigin)

	recorded := requests.snapshot()

	if len(recorded) != 2 {
		t.Fatalf("expected the redirector hop and the CDN hop, got %d request(s): %+v", len(recorded), recorded)
	}

	for _, entry := range recorded {
		if entry.userAgent != redirectorBotUserAgent {
			t.Errorf("request to %s used User-Agent %q, want %q", entry.target, entry.userAgent, redirectorBotUserAgent)
		}
	}
}

func TestProbeRedirectorTransportFailure(t *testing.T) {
	alerts := probeCaptureAdminAlerts(t)
	probeForgetTrackerState(t, probeKKInstagramService)

	server := probeServeHTTPS(t, http.HandlerFunc(probeHealthyKKInstagramHandler))

	// Closing the listener makes the probe's connection be refused.
	server.Close()

	probeRedirector(probeKKInstagramService, probeKKInstagramURL, probeKKInstagramHost, probeInstagramOrigin)

	if got := probeTrackerFailures(probeKKInstagramService); got != 1 {
		t.Errorf("a refused connection must be reported as a failure, tracker holds %d", got)
	}

	if got := alerts.alertKeys(); len(got) != 0 {
		t.Errorf("a single probe must stay below the alert threshold, got alerts %v", got)
	}
}

func TestProbeRedirectorAlertsAfterThresholdAndRecovers(t *testing.T) {
	alerts := probeCaptureAdminAlerts(t)
	probeForgetTrackerState(t, probeKKInstagramService)

	// The redirector is up but no longer resolving: 200 HTML on its own host.
	probeServeHTTPS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probeWriteHTML(w, "<!doctype html><title>kkinstagram</title>")
	}))

	for attempt := 0; attempt < outageAlertThreshold; attempt++ {
		probeRedirector(probeKKInstagramService, probeKKInstagramURL, probeKKInstagramHost, probeInstagramOrigin)
	}

	if got := probeTrackerFailures(probeKKInstagramService); got != outageAlertThreshold {
		t.Fatalf("tracker holds %d consecutive failures, want %d", got, outageAlertThreshold)
	}

	outageKey := "outage:" + probeKKInstagramService

	if got := alerts.alertKeys(); (len(got) != 1) || (got[0] != outageKey) {
		t.Fatalf("crossing the threshold must raise exactly one %q alert, got %v", outageKey, got)
	}

	if text := alerts.textFor(outageKey); strings.Contains(text, probeKKInstagramService) == false {
		t.Errorf("outage alert does not name the failing service: %q", text)
	}

	// The redirector starts resolving again.
	probeServeHTTPS(t, http.HandlerFunc(probeHealthyKKInstagramHandler))

	probeRedirector(probeKKInstagramService, probeKKInstagramURL, probeKKInstagramHost, probeInstagramOrigin)

	if got := probeTrackerFailures(probeKKInstagramService); got != 0 {
		t.Errorf("a healthy probe must clear the failure count, tracker holds %d", got)
	}

	recoveryKey := "recovery:" + probeKKInstagramService

	if got := alerts.alertKeys(); (len(got) != 2) || (got[1] != recoveryKey) {
		t.Errorf("recovery must raise a %q notice, got alerts %v", recoveryKey, got)
	}

	if got := alerts.resetKeys(); (len(got) != 1) || (got[0] != outageKey) {
		t.Errorf("recovery must clear the outage alert's rate-limit stamp, got resets %v", got)
	}
}

func TestProbeVxredditOutcomes(t *testing.T) {
	cases := []struct {
		name        string
		handler     http.HandlerFunc
		wantFailure bool
	}{
		{
			name:    "og:video advertises a proxied video",
			handler: probeVxredditVideoHandler,
		},
		{
			name: "no og:video means video stopped resolving",
			handler: func(w http.ResponseWriter, r *http.Request) {
				probeWriteHTML(w, `<html><head><meta property="og:title" content="a post"></head></html>`)
			},
			wantFailure: true,
		},
		{
			// A stale error page still carrying the OpenGraph template: the
			// og:video check passes, so only the status code catches it.
			name: "error status behind an og:video page",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(http.StatusBadGateway)
				_, _ = io.WriteString(w, `<html><head><meta property="og:video" content="https://`+probeVxredditHost+`/video/probe.mp4"></head></html>`)
			},
			wantFailure: true,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			// GetVxredditHost reads this on every probe call.
			t.Setenv("VXREDDIT_HOST", probeVxredditHost)

			requests := &probeRequestLog{}

			alerts := probeCaptureAdminAlerts(t)
			probeForgetTrackerState(t, probeVxredditService)
			probeServeHTTPS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.record(r)
				testCase.handler(w, r)
			}))

			probeVxreddit()

			wantFailures := 0

			if testCase.wantFailure {
				wantFailures = 1
			}

			if got := probeTrackerFailures(probeVxredditService); got != wantFailures {
				t.Errorf("tracker holds %d consecutive failure(s) for %s, want %d", got, probeVxredditService, wantFailures)
			}

			if got := alerts.alertKeys(); len(got) != 0 {
				t.Errorf("a single probe must stay below the alert threshold, got alerts %v", got)
			}

			recorded := requests.snapshot()

			if len(recorded) != 1 {
				t.Fatalf("expected one probe request, got %d: %+v", len(recorded), recorded)
			}

			if want := probeVxredditHost + vxredditProbePath; recorded[0].target != want {
				t.Errorf("probed %s, want %s", recorded[0].target, want)
			}

			if recorded[0].userAgent != redirectorBotUserAgent {
				t.Errorf("probe used User-Agent %q, want %q", recorded[0].userAgent, redirectorBotUserAgent)
			}
		})
	}
}

func TestProbeVxredditTransportFailure(t *testing.T) {
	t.Setenv("VXREDDIT_HOST", probeVxredditHost)
	t.Setenv("FACEBED_HOST", probeFacebedHost)

	alerts := probeCaptureAdminAlerts(t)
	probeForgetTrackerState(t, probeVxredditService)

	server := probeServeHTTPS(t, http.HandlerFunc(probeVxredditVideoHandler))

	// Closing the listener makes the probe's connection be refused, the shape
	// an outage of a small third-party frontend usually takes.
	server.Close()

	probeVxreddit()

	if got := probeTrackerFailures(probeVxredditService); got != 1 {
		t.Errorf("a refused connection must be reported as a failure, tracker holds %d", got)
	}

	if got := alerts.alertKeys(); len(got) != 0 {
		t.Errorf("a single probe must stay below the alert threshold, got alerts %v", got)
	}
}

func TestProbeVxredditAlertsAfterThresholdAndRecovers(t *testing.T) {
	t.Setenv("VXREDDIT_HOST", probeVxredditHost)
	t.Setenv("FACEBED_HOST", probeFacebedHost)

	alerts := probeCaptureAdminAlerts(t)
	probeForgetTrackerState(t, probeVxredditService)

	// vxreddit answers, but has stopped resolving video for the post.
	probeServeHTTPS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probeWriteHTML(w, `<html><head><meta property="og:title" content="a post"></head></html>`)
	}))

	for attempt := 0; attempt < outageAlertThreshold; attempt++ {
		probeVxreddit()
	}

	outageKey := "outage:" + probeVxredditService

	if got := alerts.alertKeys(); (len(got) != 1) || (got[0] != outageKey) {
		t.Fatalf("crossing the threshold must raise exactly one %q alert, got %v", outageKey, got)
	}

	// Video resolves again.
	probeServeHTTPS(t, http.HandlerFunc(probeVxredditVideoHandler))

	probeVxreddit()

	if got := probeTrackerFailures(probeVxredditService); got != 0 {
		t.Errorf("a healthy probe must clear the failure count, tracker holds %d", got)
	}

	recoveryKey := "recovery:" + probeVxredditService

	if got := alerts.alertKeys(); (len(got) != 2) || (got[1] != recoveryKey) {
		t.Errorf("recovery must raise a %q notice, got alerts %v", recoveryKey, got)
	}

	if got := alerts.resetKeys(); (len(got) != 1) || (got[0] != outageKey) {
		t.Errorf("recovery must clear the outage alert's rate-limit stamp, got resets %v", got)
	}
}

func TestCheckRedirectorHealthPinsEachProbeToItsOwnOrigin(t *testing.T) {
	// Each probe must validate against the origin domain of its own platform:
	// kktiktok giving up and bouncing to tiktok.com has to be caught by the
	// TikTok probe alone, without disturbing the other two services.
	t.Setenv("KKINSTAGRAM_HOST", probeKKInstagramHost)
	t.Setenv("KKTIKTOK_HOST", probeKKTikTokHost)
	t.Setenv("VXREDDIT_HOST", probeVxredditHost)
	t.Setenv("FACEBED_HOST", probeFacebedHost)

	alerts := probeCaptureAdminAlerts(t)
	probeForgetTrackerState(t, probeKKInstagramService, probeKKTikTokService, probeVxredditService, probeFacebedService)
	probeServeHTTPS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host == "www."+probeKKTikTokHost {
			http.Redirect(w, r, probeTikTokOriginBounceURL, http.StatusFound)

			return
		}

		if r.Host == "www."+probeTikTokOrigin {
			probeWriteHTML(w, "<!doctype html><title>TikTok</title>")

			return
		}

		probeAllServicesHandler(w, r)
	}))

	for attempt := 0; attempt < outageAlertThreshold; attempt++ {
		CheckRedirectorHealth()
	}

	if got := probeTrackerFailures(probeKKTikTokService); got != outageAlertThreshold {
		t.Fatalf("a bounce back to %s must fail the TikTok probe, tracker holds %d", probeTikTokOrigin, got)
	}

	for _, service := range []string{probeKKInstagramService, probeVxredditService, probeFacebedService} {
		if got := probeTrackerFailures(service); got != 0 {
			t.Errorf("%s must be unaffected by the TikTok bounce, tracker holds %d", service, got)
		}
	}

	outageKey := "outage:" + probeKKTikTokService

	if got := alerts.alertKeys(); (len(got) != 1) || (got[0] != outageKey) {
		t.Fatalf("only the TikTok probe should alert, got %v", got)
	}

	// The reason matters, not just the failure: a probe validating against the
	// wrong origin domain would still reject this response, for the wrong reason.
	if text := alerts.textFor(outageKey); strings.Contains(text, "redirected back to "+probeTikTokOrigin) == false {
		t.Errorf("TikTok probe must report an origin bounce, got: %q", text)
	}
}

func TestProbeKeysStaySeparateFromUserTraffic(t *testing.T) {
	// The invariant documented at the top of redirector_health.go: a probe
	// success must never reset failure counts accumulated from real user
	// traffic, or a low-traffic outage would stay below the threshold forever.
	trafficServices := []string{kkInstagramServiceName, kkTikTokServiceName, redditVxredditServiceName, facebedServiceName}
	probeServices := []string{probeKKInstagramService, probeKKTikTokService, probeVxredditService, probeFacebedService}

	t.Setenv("KKINSTAGRAM_HOST", probeKKInstagramHost)
	t.Setenv("KKTIKTOK_HOST", probeKKTikTokHost)
	t.Setenv("VXREDDIT_HOST", probeVxredditHost)
	t.Setenv("FACEBED_HOST", probeFacebedHost)

	alerts := probeCaptureAdminAlerts(t)
	probeForgetTrackerState(t, trafficServices...)
	probeForgetTrackerState(t, probeServices...)
	probeServeHTTPS(t, http.HandlerFunc(probeAllServicesHandler))

	// Two failures from real traffic on every service, still below the threshold.
	for _, service := range trafficServices {
		ReportServiceFailure(service, "user traffic failed")
		ReportServiceFailure(service, "user traffic failed")
	}

	CheckRedirectorHealth()

	for _, service := range trafficServices {
		if got := probeTrackerFailures(service); got != 2 {
			t.Errorf("probe success left %s at %d failures, want the 2 from user traffic", service, got)
		}
	}

	for _, service := range probeServices {
		if got := probeTrackerFailures(service); got != 0 {
			t.Errorf("%s holds %d failure(s) after a healthy probe", service, got)
		}
	}

	if got := alerts.alertKeys(); len(got) != 0 {
		t.Errorf("nothing should have alerted yet, got %v", got)
	}
}

func TestCheckRedirectorHealthProbesEveryService(t *testing.T) {
	t.Setenv("KKINSTAGRAM_HOST", probeKKInstagramHost)
	t.Setenv("KKTIKTOK_HOST", probeKKTikTokHost)
	t.Setenv("VXREDDIT_HOST", probeVxredditHost)
	t.Setenv("FACEBED_HOST", probeFacebedHost)

	requests := &probeRequestLog{}

	alerts := probeCaptureAdminAlerts(t)
	probeForgetTrackerState(t, probeKKInstagramService, probeKKTikTokService, probeVxredditService, probeFacebedService)
	probeServeHTTPS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.record(r)
		probeAllServicesHandler(w, r)
	}))

	CheckRedirectorHealth()

	for _, service := range []string{probeKKInstagramService, probeKKTikTokService, probeVxredditService, probeFacebedService} {
		if got := probeTrackerFailures(service); got != 0 {
			t.Errorf("%s: healthy probe recorded %d failure(s)", service, got)
		}
	}

	wantTargets := []string{
		probeKKInstagramHost + "/reel/" + kkInstagramProbeShortcode + "/",
		"www." + probeKKTikTokHost + "/@i/video/" + kkTikTokProbeVideoID,
		probeVxredditHost + vxredditProbePath,
		probeFacebedHost + "/photo/",
		probeFacebedCDNHost + probeFacebedCDNPath,
	}

	for _, target := range wantTargets {
		if requests.sawTarget(target) == false {
			t.Errorf("no probe request to %s, got %+v", target, requests.snapshot())
		}
	}

	if got := alerts.alertKeys(); len(got) != 0 {
		t.Errorf("healthy probes must not alert, got %v", got)
	}
}

func TestProbeFacebedOutcomes(t *testing.T) {
	pageTarget := probeFacebedHost + "/photo/"
	imageTarget := probeFacebedCDNHost + probeFacebedCDNPath
	cases := []struct {
		name         string
		pageHandler  http.HandlerFunc
		imageHandler http.HandlerFunc
		wantFailure  bool
		wantTargets  []string
	}{
		{name: "public photo page", pageHandler: probeFacebedPhotoHandler, imageHandler: probeFacebedImageHandler, wantTargets: []string{pageTarget, imageTarget}},
		{
			name: "login card",
			pageHandler: func(w http.ResponseWriter, r *http.Request) {
				probeWriteHTML(w, probeFacebedLoginCard)
			},
			wantFailure: true,
			wantTargets: []string{pageTarget},
		},
		{
			name: "another post",
			pageHandler: func(w http.ResponseWriter, r *http.Request) {
				page := strings.Replace(probeFacebedPhotoPage, facebedProbePhotoID, "1220500346751178", 1)
				probeWriteHTML(w, page)
			},
			wantFailure: true,
			wantTargets: []string{pageTarget},
		},
		{
			name: "post page without an image",
			pageHandler: func(w http.ResponseWriter, r *http.Request) {
				page := strings.Split(probeFacebedPhotoPage, "\n<meta property=\"og:image\"")[0]
				probeWriteHTML(w, page)
			},
			wantFailure: true,
			wantTargets: []string{pageTarget},
		},
		{
			name: "error status",
			pageHandler: func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "bad gateway", http.StatusBadGateway)
			},
			wantFailure: true,
			wantTargets: []string{pageTarget},
		},
		{
			name:        "CDN refuses the photo",
			pageHandler: probeFacebedPhotoHandler,
			imageHandler: func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "forbidden", http.StatusForbidden)
			},
			wantFailure: true,
			wantTargets: []string{pageTarget, imageTarget},
		},
		{
			name:        "CDN answers HTML",
			pageHandler: probeFacebedPhotoHandler,
			imageHandler: func(w http.ResponseWriter, r *http.Request) {
				probeWriteHTML(w, string(probeMediaPayload))
			},
			wantFailure: true,
			wantTargets: []string{pageTarget, imageTarget},
		},
		{
			name:        "CDN answers a video",
			pageHandler: probeFacebedPhotoHandler,
			imageHandler: func(w http.ResponseWriter, r *http.Request) {
				probeWriteMedia(w, probeMediaPayload)
			},
			wantFailure: true,
			wantTargets: []string{pageTarget, imageTarget},
		},
		{
			name: "photo off the CDN",
			pageHandler: func(w http.ResponseWriter, r *http.Request) {
				page := strings.Replace(probeFacebedPhotoPage, probeFacebedCDNHost, "evil.example.com", 1)
				probeWriteHTML(w, page)
			},
			wantFailure: true,
			wantTargets: []string{pageTarget},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("FACEBED_HOST", probeFacebedHost)

			requests := &probeRequestLog{}
			probeCaptureAdminAlerts(t)
			probeForgetTrackerState(t, probeFacebedService, facebedServiceName)
			probeServeHTTPS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.record(r)

				switch r.Host {
				case probeFacebedHost:
					testCase.pageHandler(w, r)
				case probeFacebedCDNHost:
					testCase.imageHandler(w, r)
				default:
					http.NotFound(w, r)
				}
			}))

			probeFacebed()

			wantFailures := 0

			if testCase.wantFailure == true {
				wantFailures = 1
			}

			if got := probeTrackerFailures(probeFacebedService); got != wantFailures {
				t.Errorf("tracker holds %d failure(s), want %d", got, wantFailures)
			}

			if got := probeTrackerFailures(facebedServiceName); got != 0 {
				t.Errorf("the probe must not touch the traffic key, it holds %d", got)
			}

			recorded := requests.snapshot()

			if len(recorded) != len(testCase.wantTargets) {
				t.Fatalf("unexpected probe requests %+v, want %v", recorded, testCase.wantTargets)
			}

			for i, record := range recorded {
				if (record.target != testCase.wantTargets[i]) || (record.userAgent != redirectorBotUserAgent) {
					t.Errorf("request %d: %+v, want %s with the bot UA", i, record, testCase.wantTargets[i])
				}
			}
		})
	}
}
