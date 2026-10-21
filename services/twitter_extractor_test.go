package services

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
)

func TestClassifyFxTwitterCall(t *testing.T) {
	cases := []struct {
		name        string
		call        fxTwitterCall
		wantOutcome serviceOutcome
		wantCode    string
	}{
		{"timeout", fxTwitterCall{fetchErr: fmt.Errorf("get: %w", context.DeadlineExceeded)}, serviceOutcomeFailure, ServiceErrorTimeout},
		{"transport", fxTwitterCall{fetchErr: errors.New("dial tcp: connection refused")}, serviceOutcomeFailure, ServiceErrorTransport},
		{"429", fxTwitterCall{statusCode: http.StatusTooManyRequests}, serviceOutcomeFailure, ServiceErrorHTTP429},
		{"5xx", fxTwitterCall{statusCode: http.StatusBadGateway}, serviceOutcomeFailure, ServiceErrorHTTP5xx},
		{"404 missing tweet", fxTwitterCall{statusCode: http.StatusNotFound}, serviceOutcomeNone, ""},
		{"401 private tweet", fxTwitterCall{statusCode: http.StatusUnauthorized}, serviceOutcomeNone, ""},
		{"body read error", fxTwitterCall{statusCode: http.StatusOK, readErr: errors.New("unexpected EOF")}, serviceOutcomeFailure, ServiceErrorTransport},
		{"bad JSON", fxTwitterCall{statusCode: http.StatusOK, parseErr: errors.New("invalid character")}, serviceOutcomeFailure, ServiceErrorParse},
		{"no tweet object", fxTwitterCall{statusCode: http.StatusOK}, serviceOutcomeNone, ""},
		{"tweet", fxTwitterCall{statusCode: http.StatusOK, hasTweet: true}, serviceOutcomeSuccess, ""},
	}

	for _, c := range cases {
		outcome, code := classifyFxTwitterCall(c.call)

		if (outcome != c.wantOutcome) || (code != c.wantCode) {
			t.Errorf("%s: got %d %q, want %d %q", c.name, outcome, code, c.wantOutcome, c.wantCode)
		}
	}
}

func TestFxTwitterCallsReachTheTracker(t *testing.T) {
	cases := []struct {
		name        string
		status      int
		body        string
		wantOutcome serviceOutcome
		wantCode    string
	}{
		{"5xx is a failure", http.StatusServiceUnavailable, "", serviceOutcomeFailure, ServiceErrorHTTP5xx},
		{"404 is not counted", http.StatusNotFound, `{"code":404}`, serviceOutcomeNone, ""},
		{"bad JSON is a failure", http.StatusOK, `{"tweet":`, serviceOutcomeFailure, ServiceErrorParse},
		{"a tweet is a success", http.StatusOK, `{"tweet":{"text":"hello","author":{"name":"A","screen_name":"a"}}}`, serviceOutcomeSuccess, ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			newAlertRecorder(t)
			probeServeHTTPS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(c.status)
				fmt.Fprint(w, c.body)
			}))
			extractor := &TwitterExtractor{client: SharedHTTPClient, tempDir: t.TempDir(), reportsFxTwitter: true}

			_, _ = extractor.extractViaFxTwitterAPI("1234567890", "a")

			assertTracked(t, fxTwitterServiceName, c.wantOutcome, c.wantCode)
		})
	}
}

func TestFxTwitterCallsOfTheOtherStepsAreNotCounted(t *testing.T) {
	newAlertRecorder(t)
	probeServeHTTPS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	extractor := &TwitterExtractor{client: SharedHTTPClient, tempDir: t.TempDir()}

	_, _ = extractor.extractViaFxTwitterAPI("1234567890", "a")

	assertTracked(t, fxTwitterServiceName, serviceOutcomeNone, "")
}
