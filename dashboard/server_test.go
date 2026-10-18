package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// stubStore records the parameters it was called with.
type stubStore struct {
	err    error
	days   int
	limit  int
	months int
	weeks  int
	status string
	calls  int
	blog   blogStub
	report reportStub
}

func (s *stubStore) Ping(ctx context.Context) error {
	s.calls++

	return s.err
}

func (s *stubStore) Overview(ctx context.Context, days int) (Overview, error) {
	s.calls++
	s.days = days

	return Overview{Days: days, Daily: []DailySplit{}}, s.err
}

func (s *stubStore) Platforms(ctx context.Context, days int) (Platforms, error) {
	s.calls++
	s.days = days

	return Platforms{Days: days}, s.err
}

func (s *stubStore) Chats(ctx context.Context, days int, limit int) (Chats, error) {
	s.calls++
	s.days = days
	s.limit = limit

	return Chats{Days: days, Limit: limit}, s.err
}

func (s *stubStore) Money(ctx context.Context, months int) (Money, error) {
	s.calls++
	s.months = months

	return Money{Months: months}, s.err
}

func (s *stubStore) Reports(ctx context.Context, limit int, weeks int, status string) (Reports, error) {
	s.calls++
	s.limit = limit
	s.weeks = weeks
	s.status = status

	return Reports{Limit: limit, Weeks: weeks, Status: status}, s.err
}

// testHost is the Host of httptest requests.
const testHost = "example.com"

var testNow = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

const (
	testIndexHTML = "<!doctype html><title>dashboard</title>"
	testAppJS     = "console.log('app')"
)

func newTestServer(store Store) *Server {
	assets := fstest.MapFS{
		"index.html":        &fstest.MapFile{Data: []byte(testIndexHTML)},
		"favicon.svg":       &fstest.MapFile{Data: []byte("<svg></svg>")},
		"assets/app-1a2.js": &fstest.MapFile{Data: []byte(testAppJS)},
	}
	server := NewServer(store, "Europe/Kyiv", []string{testHost}, assets)
	server.now = func() time.Time { return testNow }

	return server
}

func doRequest(handler http.Handler, method string, target string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	return recorder
}

func decodeBody(t *testing.T, recorder *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	body := map[string]interface{}{}

	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %v (%q)", err, recorder.Body.String())
	}

	return body
}

func TestSecurityHeadersAreOnEveryResponse(t *testing.T) {
	server := newTestServer(&stubStore{})
	targets := []string{"/", "/api/overview", "/assets/app-1a2.js", "/assets/missing.js", "/api/unknown", "/healthz"}

	for _, target := range targets {
		recorder := doRequest(server, http.MethodGet, target)
		header := recorder.Header()

		if header.Get("Content-Security-Policy") != contentSecurity {
			t.Errorf("%s: CSP = %q", target, header.Get("Content-Security-Policy"))
		}

		if header.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: X-Content-Type-Options = %q", target, header.Get("X-Content-Type-Options"))
		}

		if header.Get("Referrer-Policy") != "no-referrer" {
			t.Errorf("%s: Referrer-Policy = %q", target, header.Get("Referrer-Policy"))
		}
	}
}

func TestAPIResponsesAreNotCached(t *testing.T) {
	server := newTestServer(&stubStore{})

	for _, target := range []string{"/api/overview", "/api/nope", "/healthz"} {
		recorder := doRequest(server, http.MethodGet, target)

		if recorder.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s: Cache-Control = %q", target, recorder.Header().Get("Cache-Control"))
		}
	}
}

func TestWriteMethodsAreRefused(t *testing.T) {
	store := &stubStore{}
	server := newTestServer(store)

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions} {
		recorder := doRequest(server, method, "/api/overview")

		if recorder.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: status = %d", method, recorder.Code)
		}

		if recorder.Header().Get("Allow") != "GET, HEAD" {
			t.Errorf("%s: Allow = %q", method, recorder.Header().Get("Allow"))
		}

		body := decodeBody(t, recorder)

		if body["error"] != "method not allowed" {
			t.Errorf("%s: body = %v", method, body)
		}
	}

	if store.calls != 0 {
		t.Errorf("store was called %d times", store.calls)
	}
}

func TestHeadIsAllowed(t *testing.T) {
	server := newTestServer(&stubStore{})
	recorder := doRequest(server, http.MethodHead, "/api/overview")

	if recorder.Code != http.StatusOK {
		t.Errorf("status = %d", recorder.Code)
	}
}

func TestSPAFallbackServesIndexForClientRoutes(t *testing.T) {
	server := newTestServer(&stubStore{})

	for _, target := range []string{"/", "/platforms", "/some/deep/route", "/assets"} {
		recorder := doRequest(server, http.MethodGet, target)

		if recorder.Code != http.StatusOK {
			t.Errorf("%s: status = %d", target, recorder.Code)
		}

		if recorder.Body.String() != testIndexHTML {
			t.Errorf("%s: body = %q", target, recorder.Body.String())
		}

		if strings.HasPrefix(recorder.Header().Get("Content-Type"), "text/html") == false {
			t.Errorf("%s: Content-Type = %q", target, recorder.Header().Get("Content-Type"))
		}

		if recorder.Header().Get("Cache-Control") != "no-cache" {
			t.Errorf("%s: Cache-Control = %q", target, recorder.Header().Get("Cache-Control"))
		}
	}
}

func TestRootFilesAreServed(t *testing.T) {
	server := newTestServer(&stubStore{})
	recorder := doRequest(server, http.MethodGet, "/favicon.svg")

	if recorder.Body.String() != "<svg></svg>" {
		t.Errorf("body = %q", recorder.Body.String())
	}
}

func TestAssetsHaveLongCacheAndMissingAssetsAre404(t *testing.T) {
	server := newTestServer(&stubStore{})
	recorder := doRequest(server, http.MethodGet, "/assets/app-1a2.js")

	if (recorder.Code != http.StatusOK) || (recorder.Body.String() != testAppJS) {
		t.Errorf("asset: status = %d, body = %q", recorder.Code, recorder.Body.String())
	}

	if recorder.Header().Get("Cache-Control") != cacheImmutable {
		t.Errorf("asset: Cache-Control = %q", recorder.Header().Get("Cache-Control"))
	}

	missing := doRequest(server, http.MethodGet, "/assets/missing.js")

	if missing.Code != http.StatusNotFound {
		t.Errorf("missing asset: status = %d", missing.Code)
	}

	if strings.Contains(missing.Body.String(), testIndexHTML) == true {
		t.Errorf("missing asset served the SPA")
	}
}

func TestUnknownHostIsRefusedOnEveryPath(t *testing.T) {
	store := &stubStore{}
	server := newTestServer(store)

	for _, target := range []string{"/", "/api/overview", "/api/blog-posts", "/healthz", "/assets/app-1a2.js"} {
		request := httptest.NewRequest(http.MethodGet, target, nil)
		request.Host = "evil.example:8090"
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusForbidden {
			t.Errorf("%s: status = %d", target, recorder.Code)
		}

		body := decodeBody(t, recorder)

		if body["error"] != "forbidden" {
			t.Errorf("%s: body = %v", target, body)
		}
	}

	if store.calls != 0 {
		t.Errorf("store was called %d times", store.calls)
	}
}

func TestMissingFrontendBuildAnswers503(t *testing.T) {
	server := NewServer(&stubStore{}, "Europe/Kyiv", []string{testHost}, fstest.MapFS{".gitkeep": &fstest.MapFile{}})
	recorder := doRequest(server, http.MethodGet, "/")

	if recorder.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d", recorder.Code)
	}
}

func TestUnknownAPIPathIsJSON404(t *testing.T) {
	server := newTestServer(&stubStore{})

	for _, target := range []string{"/api/unknown", "/api", "/api/"} {
		recorder := doRequest(server, http.MethodGet, target)

		if recorder.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d", target, recorder.Code)
		}

		body := decodeBody(t, recorder)

		if body["error"] != "not found" {
			t.Errorf("%s: body = %v", target, body)
		}
	}
}

func TestResponsesCarryGeneratedAtAndTimezone(t *testing.T) {
	server := newTestServer(&stubStore{})

	for _, target := range []string{"/api/overview", "/api/platforms", "/api/chats", "/api/money", "/api/reports", "/healthz"} {
		recorder := doRequest(server, http.MethodGet, target)
		body := decodeBody(t, recorder)

		if body["generated_at"] != "2026-10-15T12:00:00Z" {
			t.Errorf("%s: generated_at = %v", target, body["generated_at"])
		}

		if body["timezone"] != "Europe/Kyiv" {
			t.Errorf("%s: timezone = %v", target, body["timezone"])
		}

		if strings.HasPrefix(recorder.Header().Get("Content-Type"), "application/json") == false {
			t.Errorf("%s: Content-Type = %q", target, recorder.Header().Get("Content-Type"))
		}
	}
}

func TestHealthz(t *testing.T) {
	server := newTestServer(&stubStore{})
	recorder := doRequest(server, http.MethodGet, "/healthz")
	body := decodeBody(t, recorder)

	if (recorder.Code != http.StatusOK) || (body["ok"] != true) {
		t.Errorf("status = %d, body = %v", recorder.Code, body)
	}
}

func TestDatabaseErrorIsGeneric500(t *testing.T) {
	store := &stubStore{err: errors.New("pq: password authentication failed for user secret")}
	server := newTestServer(store)

	for _, target := range []string{"/api/overview", "/api/platforms", "/api/chats", "/api/money", "/api/reports", "/healthz"} {
		recorder := doRequest(server, http.MethodGet, target)

		if recorder.Code != http.StatusInternalServerError {
			t.Errorf("%s: status = %d", target, recorder.Code)
		}

		body := decodeBody(t, recorder)

		if (len(body) != 1) || (body["error"] != "internal error") {
			t.Errorf("%s: body = %v", target, body)
		}
	}
}

func TestBadParamIs400WithoutCallingTheStore(t *testing.T) {
	targets := []string{
		"/api/overview?days=abc",
		"/api/platforms?days=1.5",
		"/api/chats?limit=x",
		"/api/chats?days=-",
		"/api/money?months=twelve",
		"/api/reports?weeks=1e3",
		"/api/reports?limit=99999999999999999999999",
	}

	for _, target := range targets {
		store := &stubStore{}
		server := newTestServer(store)
		recorder := doRequest(server, http.MethodGet, target)

		if recorder.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d", target, recorder.Code)
		}

		body := decodeBody(t, recorder)
		message, _ := body["error"].(string)

		if (len(body) != 1) || (strings.HasPrefix(message, "invalid ") == false) {
			t.Errorf("%s: body = %v", target, body)
		}

		if store.calls != 0 {
			t.Errorf("%s: store was called", target)
		}
	}
}

func TestHandlersPassDefaultsAndClampedParams(t *testing.T) {
	cases := []struct {
		target string
		want   stubStore
	}{
		{"/api/overview", stubStore{days: 90}},
		{"/api/overview?days=7", stubStore{days: 7}},
		{"/api/overview?days=0", stubStore{days: 1}},
		{"/api/overview?days=1000", stubStore{days: 365}},
		{"/api/overview?days=", stubStore{days: 90}},
		{"/api/overview?other=1", stubStore{days: 90}},
		{"/api/platforms", stubStore{days: 90}},
		{"/api/platforms?days=-5", stubStore{days: 1}},
		{"/api/chats", stubStore{days: 30, limit: 20}},
		{"/api/chats?days=400&limit=500", stubStore{days: 365, limit: 200}},
		{"/api/chats?limit=0", stubStore{days: 30, limit: 1}},
		{"/api/money", stubStore{months: 12}},
		{"/api/money?months=100", stubStore{months: 36}},
		{"/api/reports", stubStore{limit: 50, weeks: 26}},
		{"/api/reports?limit=5&weeks=500", stubStore{limit: 5, weeks: 104}},
	}

	for _, tc := range cases {
		store := &stubStore{}
		server := newTestServer(store)
		recorder := doRequest(server, http.MethodGet, tc.target)

		if recorder.Code != http.StatusOK {
			t.Errorf("%s: status = %d", tc.target, recorder.Code)
		}

		got := stubStore{days: store.days, limit: store.limit, months: store.months, weeks: store.weeks}

		if got != tc.want {
			t.Errorf("%s: store got %+v, want %+v", tc.target, got, tc.want)
		}
	}
}

func TestEmptySlicesAreJSONArrays(t *testing.T) {
	server := newTestServer(&stubStore{})
	recorder := doRequest(server, http.MethodGet, "/api/overview")

	if strings.Contains(recorder.Body.String(), `"daily":[]`) == false {
		t.Errorf("body = %s", recorder.Body.String())
	}
}
