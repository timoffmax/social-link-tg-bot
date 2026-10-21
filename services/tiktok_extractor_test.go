package services

// Tests for TikTok HTML parsing. They only use HTML with no downloadable content,
// so parseHTMLForMedia returns before any network download is attempted.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"testing"
)

// loginWallHTML mirrors what TikTok serves for "audience controls" posts to an
// anonymous client: no video, only the author's avatar and a black placeholder.
const loginWallHTML = `<!DOCTYPE html><html><head><title>TikTok - Make Your Day</title></head><body>
<img src="https://p16-common-sign.tiktokcdn-eu.com/tos-maliva-avt-0068/abc~tplv-tiktokx-cropcenter:720:720.jpeg?x-expires=1&x-signature=a">
<img src="https://p16-common-sign.tiktokcdn-eu.com/tos-maliva-avt-0068/abc~tplv-tiktokx-cropcenter:100:100.jpeg?x-expires=1&x-signature=b">
<img src="https://sf16-sg.tiktokcdn.com/obj/eden-sg/nupwbpxnuhfbv/black-placeholder-bg.png">
</body></html>`

func TestParseHTMLForMediaRejectsLoginWall(t *testing.T) {
	te := NewTikTokExtractor(t.TempDir(), t.TempDir())

	mediaInfos, err := te.parseHTMLForMedia(loginWallHTML, "7692094768695807240", "dvirna")

	if nil == err {
		t.Fatalf("expected an error so the next extraction method runs, got %d media items", len(mediaInfos))
	}

	if 0 != len(mediaInfos) {
		t.Fatalf("expected no media from a login wall, got %d items", len(mediaInfos))
	}
}

func TestClassifyTikwmCall(t *testing.T) {
	cases := []struct {
		name        string
		call        tikwmCall
		wantOutcome serviceOutcome
		wantCode    string
	}{
		{"timeout", tikwmCall{requestErr: fmt.Errorf("post: %w", context.DeadlineExceeded)}, serviceOutcomeFailure, ServiceErrorTimeout},
		{"transport", tikwmCall{requestErr: &url.Error{Op: "Post", URL: tikwmAPIURL, Err: errors.New("refused")}}, serviceOutcomeFailure, ServiceErrorTransport},
		{"429", tikwmCall{statusCode: http.StatusTooManyRequests}, serviceOutcomeFailure, ServiceErrorHTTP429},
		{"5xx", tikwmCall{statusCode: http.StatusBadGateway}, serviceOutcomeFailure, ServiceErrorHTTP5xx},
		{"4xx", tikwmCall{statusCode: http.StatusForbidden}, serviceOutcomeFailure, ServiceErrorHTTP4xx},
		{"decode error", tikwmCall{statusCode: http.StatusOK, decodeErr: errors.New("invalid character")}, serviceOutcomeFailure, ServiceErrorParse},
		{"per-second limit set off by the bot", tikwmCall{statusCode: http.StatusOK, apiCode: -1, message: "Free Api Limit: 1 request/second."}, serviceOutcomeNone, ""},
		{"rate limit message", tikwmCall{statusCode: http.StatusOK, apiCode: -1, message: "Api limit reached for today"}, serviceOutcomeFailure, ServiceErrorHTTP429},
		{"deleted video", tikwmCall{statusCode: http.StatusOK, apiCode: -1, message: "Url parsing is failed! Please check url."}, serviceOutcomeNone, ""},
		{"answer", tikwmCall{statusCode: http.StatusOK}, serviceOutcomeSuccess, ""},
	}

	for _, c := range cases {
		outcome, code := classifyTikwmCall(c.call)

		if (outcome != c.wantOutcome) || (code != c.wantCode) {
			t.Errorf("%s: got %d %q, want %d %q", c.name, outcome, code, c.wantOutcome, c.wantCode)
		}
	}
}

func TestTikwmCallsReachTheTracker(t *testing.T) {
	cases := []struct {
		name        string
		status      int
		body        string
		wantOutcome serviceOutcome
		wantCode    string
	}{
		{"5xx is a failure", http.StatusBadGateway, "", serviceOutcomeFailure, ServiceErrorHTTP5xx},
		{"a deleted video is not counted", http.StatusOK, `{"code":-1,"msg":"Url parsing is failed! Please check url."}`, serviceOutcomeNone, ""},
		{"an answer is a success", http.StatusOK, `{"code":0,"msg":"success","data":{"id":"1"}}`, serviceOutcomeSuccess, ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			newAlertRecorder(t)
			probeServeHTTPS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(c.status)
				fmt.Fprint(w, c.body)
			}))
			extractor := &TikTokExtractor{client: SharedHTTPClient, tempDir: t.TempDir()}

			_, _ = extractor.extractViaTikWMAPI("https://www.tiktok.com/@a/video/1", "1", "a")

			assertTracked(t, tikwmServiceName, c.wantOutcome, c.wantCode)
		})
	}
}
