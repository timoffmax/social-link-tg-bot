package services

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"sync"
	"testing"
)

func TestClassifyNinegagHelper(t *testing.T) {
	exitErr := errors.New("exit status 1")
	cases := []struct {
		name        string
		call        ninegagHelperCall
		output      string
		wantOutcome serviceOutcome
		wantCode    string
	}{
		{"no Python", ninegagHelperCall{setupErr: errors.New("no Python with curl_cffi found")}, "", serviceOutcomeNone, ""},
		{"no script", ninegagHelperCall{setupErr: errors.New("9gag_fetch.py not found at /x")}, "", serviceOutcomeNone, ""},
		{"curl_cffi missing", ninegagHelperCall{execErr: exitErr}, `{"error": "curl_cffi not installed. Run: pip install curl_cffi"}`, serviceOutcomeNone, ""},
		{"own timeout", ninegagHelperCall{isHelperTimeout: true, execErr: errors.New("signal: killed")}, "", serviceOutcomeFailure, ServiceErrorTimeout},
		{"caller deadline", ninegagHelperCall{callerErr: context.DeadlineExceeded, isHelperTimeout: true, execErr: errors.New("signal: killed")}, "", serviceOutcomeNone, ""},
		{"caller cancel", ninegagHelperCall{callerErr: context.Canceled, execErr: errors.New("signal: killed")}, "", serviceOutcomeNone, ""},
		{"exec error", ninegagHelperCall{execErr: exitErr}, "", serviceOutcomeFailure, ServiceErrorOther},
		{"not JSON", ninegagHelperCall{}, "Traceback (most recent call last)", serviceOutcomeFailure, ServiceErrorParse},
		{"429", ninegagHelperCall{}, `{"error": "HTTP 429"}`, serviceOutcomeFailure, ServiceErrorHTTP429},
		{"5xx", ninegagHelperCall{}, `{"error": "HTTP 503"}`, serviceOutcomeFailure, ServiceErrorHTTP5xx},
		{"Cloudflare 403", ninegagHelperCall{}, `{"error": "HTTP 403"}`, serviceOutcomeFailure, ServiceErrorHTTP4xx},
		{"404 missing post", ninegagHelperCall{}, `{"error": "HTTP 404"}`, serviceOutcomeNone, ""},
		{"post not in the answer", ninegagHelperCall{}, `{"error": "Post not found in response"}`, serviceOutcomeNone, ""},
		{"exception text", ninegagHelperCall{}, `{"error": "Failed to perform, curl: (28) Operation timed out"}`, serviceOutcomeFailure, ServiceErrorTimeout},
		{"post", ninegagHelperCall{}, `{"id": "a1", "title": "t", "type": "Photo"}`, serviceOutcomeSuccess, ""},
	}

	for _, c := range cases {
		call := c.call
		call.post, call.parseErr = parseNinegagPost([]byte(c.output))
		outcome, code := classifyNinegagHelper(call)

		if (outcome != c.wantOutcome) || (code != c.wantCode) {
			t.Errorf("%s: got %d %q, want %d %q", c.name, outcome, code, c.wantOutcome, c.wantCode)
		}
	}
}

func TestNinegagHelperSetupProblemIsLoggedOnceAndNotTracked(t *testing.T) {
	recorder := newAlertRecorder(t)
	ninegagSetupLogged = sync.Once{}
	var logged bytes.Buffer
	previousWriter := log.Writer()
	log.SetOutput(&logged)
	t.Cleanup(func() {
		log.SetOutput(previousWriter)
	})
	curlCall := ninegagHelperCall{execErr: errors.New("exit status 1")}
	curlCall.post, curlCall.parseErr = parseNinegagPost([]byte(`{"error": "curl_cffi not installed. Run: pip install curl_cffi"}`))
	calls := []ninegagHelperCall{
		{setupErr: errors.New("no Python with curl_cffi found")},
		{setupErr: errors.New("9gag_fetch.py not found at /x")},
		curlCall,
	}

	for i := 0; i < outageAlertThreshold; i++ {
		for _, call := range calls {
			reportNinegagHelper(call)
		}
	}

	if _, isTracked := ServiceHealthSnapshot()[ninegagAPIServiceName]; isTracked == true {
		t.Fatal("a setup problem must not reach the tracker")
	}

	if alerts := recorder.recordedAlerts(); len(alerts) != 0 {
		t.Fatalf("got %+v", alerts)
	}

	if count := strings.Count(logged.String(), "9GAG API helper can not run"); count != 1 {
		t.Fatalf("logged %d times: %q", count, logged.String())
	}
}

func TestNinegagHelperAPIErrorStillAlerts(t *testing.T) {
	recorder := newAlertRecorder(t)
	call := ninegagHelperCall{execErr: errors.New("exit status 1")}
	call.post, call.parseErr = parseNinegagPost([]byte(`{"error": "HTTP 503"}`))

	for i := 0; i < outageAlertThreshold; i++ {
		reportNinegagHelper(call)
	}

	alerts := recorder.alertsWithKey("outage:9gag-api")

	if (len(alerts) != 1) || (strings.Contains(alerts[0].text, "HTTP 503") == false) {
		t.Fatalf("got %+v", alerts)
	}

	if code := serviceHealthOf(t, ninegagAPIServiceName).LastErrorCode; code != ServiceErrorHTTP5xx {
		t.Fatalf("code %q", code)
	}
}

func TestNinegagHelperDetailIsTheHelperError(t *testing.T) {
	call := ninegagHelperCall{}
	call.post, call.parseErr = parseNinegagPost([]byte(`{"error": "HTTP 403"}`))

	if detail := ninegagHelperDetail(call); detail != "9GAG API error: HTTP 403" {
		t.Fatalf("got %q", detail)
	}
}
