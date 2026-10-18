package dashboard

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/timoffmax/social-link-bot/model/reportreply"
)

// reportStub records the report calls of a stubStore.
type reportStub struct {
	calls    int
	id       int64
	text     string
	resolve  *bool
	resolved *bool
}

func (s *stubStore) Report(ctx context.Context, id int64) (Report, error) {
	s.report.calls++
	s.report.id = id

	return Report{ID: id, Replies: []ReportReply{}}, s.err
}

func (s *stubStore) QueueReportReply(ctx context.Context, id int64, text string, resolve bool) (ReportReply, Report, error) {
	s.report.calls++
	s.report.id = id
	s.report.text = text
	s.report.resolve = &resolve
	reply := ReportReply{ID: 3, Text: text, Source: "dashboard", Status: "queued"}

	return reply, Report{ID: id, Resolved: resolve, Replies: []ReportReply{reply}}, s.err
}

func (s *stubStore) SetReportResolved(ctx context.Context, id int64, resolved bool) (Report, error) {
	s.report.calls++
	s.report.id = id
	s.report.resolved = &resolved

	return Report{ID: id, Resolved: resolved, Replies: []ReportReply{}}, s.err
}

const validReplyBody = `{"text":"<b>Fixed</b>, thanks!","resolve":true}`

func TestReplyTextLimitLeavesRoomForTheHeading(t *testing.T) {
	result := ReplyTextLimit

	if (result != 4066) || (ReplyMessagePrefix != reportreply.Prefix) {
		t.Errorf("limit = %d, want 4066; prefix = %q", result, ReplyMessagePrefix)
	}
}

func TestReportsAnswerCarriesTheReplyHeadingAndLimit(t *testing.T) {
	recorder := doRequest(newTestServer(&stubStore{}), http.MethodGet, "/api/reports")
	body := decodeBody(t, recorder)

	if (recorder.Code != http.StatusOK) || (body["reply_prefix"] != reportreply.Prefix) || (body["reply_text_limit"] != float64(4066)) {
		t.Errorf("status = %d, body = %v", recorder.Code, body)
	}
}

func TestReportReplyQueuesTrimmedTextAndAnswers201(t *testing.T) {
	store := &stubStore{}
	recorder := blogRequest{method: http.MethodPost, target: "/api/reports/12/replies", body: `{"text":"  <b>Fixed</b> \n","resolve":false}`}.do(newTestServer(store))
	body := decodeBody(t, recorder)
	reply, _ := body["reply"].(map[string]interface{})
	report, _ := body["report"].(map[string]interface{})

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %v", recorder.Code, body)
	}

	if (store.report.id != 12) || (store.report.text != "<b>Fixed</b>") || (store.report.resolve == nil) || (*store.report.resolve == true) {
		t.Errorf("store got %+v", store.report)
	}

	if (reply["status"] != "queued") || (report["id"] != float64(12)) || (body["generated_at"] == nil) {
		t.Errorf("body = %v", body)
	}
}

func TestReportReplyPassesResolve(t *testing.T) {
	store := &stubStore{}
	recorder := blogRequest{method: http.MethodPost, target: "/api/reports/5/replies", body: validReplyBody}.do(newTestServer(store))

	if (recorder.Code != http.StatusCreated) || (store.report.resolve == nil) || (*store.report.resolve == false) {
		t.Errorf("status = %d, store = %+v", recorder.Code, store.report)
	}
}

func TestReportResolvedPassesTheFlag(t *testing.T) {
	for _, flag := range []bool{true, false} {
		store := &stubStore{}
		body := `{"resolved":false}`

		if flag == true {
			body = `{"resolved":true}`
		}

		recorder := blogRequest{method: http.MethodPut, target: "/api/reports/4/resolved", body: body}.do(newTestServer(store))
		answer := decodeBody(t, recorder)
		report, _ := answer["report"].(map[string]interface{})

		if (recorder.Code != http.StatusOK) || (store.report.resolved == nil) || (*store.report.resolved != flag) || (report["resolved"] != flag) {
			t.Errorf("%v: status = %d, body = %v", flag, recorder.Code, answer)
		}
	}
}

func TestReportGetNeedsNoWriteHeaders(t *testing.T) {
	store := &stubStore{}
	recorder := doRequest(newTestServer(store), http.MethodGet, "/api/reports/8")
	body := decodeBody(t, recorder)
	report, _ := body["report"].(map[string]interface{})

	if (recorder.Code != http.StatusOK) || (report["id"] != float64(8)) || (body["timezone"] != "Europe/Kyiv") {
		t.Errorf("status = %d, body = %v", recorder.Code, body)
	}

	if recorder.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q", recorder.Header().Get("Cache-Control"))
	}
}

func TestReportWriteProtection(t *testing.T) {
	writes := []blogRequest{
		{method: http.MethodPost, target: "/api/reports/3/replies", body: validReplyBody},
		{method: http.MethodPut, target: "/api/reports/3/resolved", body: `{"resolved":true}`},
	}
	broken := []blogRequest{
		{origin: text("")},
		{origin: text("http://evil.example")},
		{marker: text("")},
		{contentType: text("text/plain")},
		{host: "evil.example:8090", origin: text("http://evil.example:8090")},
	}

	for _, write := range writes {
		for _, change := range broken {
			store := &stubStore{}
			request := write
			request.origin = change.origin
			request.marker = change.marker
			request.contentType = change.contentType
			request.host = change.host
			recorder := request.do(newTestServer(store))

			if (recorder.Code != http.StatusForbidden) || (store.report.calls != 0) {
				t.Errorf("%s %s %+v: status = %d, calls = %d", write.method, write.target, change, recorder.Code, store.report.calls)
			}
		}
	}
}

func TestReportBodyRules(t *testing.T) {
	cases := []struct {
		name   string
		target string
		body   string
		status int
	}{
		{"oversized", "/api/reports/3/replies", `{"text":"` + strings.Repeat("a", maxRequestBody) + `","resolve":true}`, http.StatusRequestEntityTooLarge},
		{"unknown field", "/api/reports/3/replies", `{"text":"t","resolve":true,"status":"sent"}`, http.StatusBadRequest},
		{"trailing data", "/api/reports/3/replies", validReplyBody + `{}`, http.StatusBadRequest},
		{"resolved unknown field", "/api/reports/3/resolved", `{"resolved":true,"resolved_at":null}`, http.StatusBadRequest},
		{"empty body", "/api/reports/3/resolved", ` `, http.StatusBadRequest},
	}

	for _, tc := range cases {
		store := &stubStore{}
		method := http.MethodPost

		if strings.HasSuffix(tc.target, "/resolved") == true {
			method = http.MethodPut
		}

		recorder := blogRequest{method: method, target: tc.target, body: tc.body}.do(newTestServer(store))

		if (recorder.Code != tc.status) || (store.report.calls != 0) {
			t.Errorf("%s: status = %d, calls = %d (%s)", tc.name, recorder.Code, store.report.calls, recorder.Body.String())
		}
	}
}

func TestReportValidationErrors(t *testing.T) {
	atLimit := strings.Repeat("a", 4066)
	cases := []struct {
		target string
		body   string
		prefix string
	}{
		{"/api/reports/1/replies", `{"text":"  \n ","resolve":true}`, "text is required"},
		{"/api/reports/1/replies", `{"resolve":true}`, "text is required"},
		{"/api/reports/1/replies", `{"text":"a\u0000","resolve":true}`, "text must not contain NUL characters"},
		{"/api/reports/1/replies", `{"text":"<b>x</i>","resolve":true}`, "text: line 1, column 5: </i> closes <b> opened at line 1, column 1"},
		{"/api/reports/1/replies", `{"text":"1 < 2","resolve":true}`, "text: line 1, column 3"},
		{"/api/reports/1/replies", `{"text":"` + atLimit + `a","resolve":true}`, "text is too long: 4067 characters after formatting, at most 4066 (Telegram allows 4096, the reply's heading takes 30)"},
		{"/api/reports/1/replies", `{"text":"t"}`, "resolve is required"},
		{"/api/reports/1/replies", `{"text":"t","resolve":null}`, "resolve is required"},
		{"/api/reports/1/replies", `{"text":"t","resolve":"yes"}`, "invalid request body"},
		{"/api/reports/1/resolved", `{}`, "resolved is required"},
		{"/api/reports/1/resolved", `{"resolved":null}`, "resolved is required"},
		{"/api/reports/1/resolved", `{"resolved":1}`, "invalid request body"},
	}

	for _, tc := range cases {
		store := &stubStore{}
		method := http.MethodPost

		if strings.HasSuffix(tc.target, "/resolved") == true {
			method = http.MethodPut
		}

		recorder := blogRequest{method: method, target: tc.target, body: tc.body}.do(newTestServer(store))
		body := decodeBody(t, recorder)
		message, _ := body["error"].(string)

		if (recorder.Code != http.StatusBadRequest) || (strings.HasPrefix(message, tc.prefix) == false) || (store.report.calls != 0) {
			t.Errorf("%s %s: status = %d, error = %q, calls = %d", tc.target, tc.body, recorder.Code, message, store.report.calls)
		}
	}
}

func TestReportReplyAtTheLimitIsAccepted(t *testing.T) {
	store := &stubStore{}
	content := strings.Repeat("😀", 2033)
	recorder := blogRequest{method: http.MethodPost, target: "/api/reports/1/replies", body: `{"text":"` + content + `","resolve":true}`}.do(newTestServer(store))

	if recorder.Code != http.StatusCreated {
		t.Errorf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestReportMethodRules(t *testing.T) {
	cases := []struct {
		method string
		target string
		status int
		allow  string
	}{
		{http.MethodPost, "/api/reports", http.StatusMethodNotAllowed, "GET, HEAD"},
		{http.MethodPut, "/api/reports/1", http.StatusMethodNotAllowed, "GET, HEAD"},
		{http.MethodDelete, "/api/reports/1", http.StatusMethodNotAllowed, "GET, HEAD"},
		{http.MethodGet, "/api/reports/1/replies", http.StatusMethodNotAllowed, "POST"},
		{http.MethodPut, "/api/reports/1/replies", http.StatusMethodNotAllowed, "POST"},
		{http.MethodGet, "/api/reports/1/resolved", http.StatusMethodNotAllowed, "PUT"},
		{http.MethodPost, "/api/reports/1/resolved", http.StatusMethodNotAllowed, "PUT"},
		{http.MethodPost, "/api/reports/abc/replies", http.StatusMethodNotAllowed, "GET, HEAD"},
		{http.MethodPost, "/api/reports/1/other", http.StatusMethodNotAllowed, "GET, HEAD"},
		{http.MethodPost, "/api/reports/", http.StatusMethodNotAllowed, "GET, HEAD"},
		{http.MethodPost, "/api/reports/1/replies/2", http.StatusMethodNotAllowed, "GET, HEAD"},
		{http.MethodGet, "/api/reports/0", http.StatusNotFound, ""},
		{http.MethodGet, "/api/reports/07", http.StatusNotFound, ""},
		{http.MethodGet, "/api/reports/1/other", http.StatusNotFound, ""},
		{http.MethodGet, "/api/reports/", http.StatusNotFound, ""},
		{http.MethodGet, "/api/reports/1", http.StatusOK, ""},
		{http.MethodHead, "/api/reports/1", http.StatusOK, ""},
	}

	for _, tc := range cases {
		store := &stubStore{}
		recorder := blogRequest{method: tc.method, target: tc.target, body: "{}"}.do(newTestServer(store))

		if recorder.Code != tc.status {
			t.Errorf("%s %s: status = %d, want %d", tc.method, tc.target, recorder.Code, tc.status)
		}

		if recorder.Header().Get("Allow") != tc.allow {
			t.Errorf("%s %s: Allow = %q, want %q", tc.method, tc.target, recorder.Header().Get("Allow"), tc.allow)
		}

		isRefused := (tc.status != http.StatusOK)

		if (isRefused == true) && ((store.report.calls != 0) || (store.calls != 0)) {
			t.Errorf("%s %s: store was called", tc.method, tc.target)
		}
	}
}

func TestReportStoreErrorsMapToStatus(t *testing.T) {
	cases := []struct {
		err     error
		status  int
		message string
	}{
		{ErrReportNotFound, http.StatusNotFound, "not found"},
		{errors.New("pq: permission denied for table report_replies"), http.StatusInternalServerError, "internal error"},
	}
	requests := []blogRequest{
		{method: http.MethodGet, target: "/api/reports/3"},
		{method: http.MethodPost, target: "/api/reports/3/replies", body: validReplyBody},
		{method: http.MethodPut, target: "/api/reports/3/resolved", body: `{"resolved":false}`},
	}

	for _, tc := range cases {
		for _, request := range requests {
			recorder := request.do(newTestServer(&stubStore{err: tc.err}))
			body := decodeBody(t, recorder)

			if (recorder.Code != tc.status) || (len(body) != 1) || (body["error"] != tc.message) {
				t.Errorf("%s %s with %v: status = %d, body = %v", request.method, request.target, tc.err, recorder.Code, body)
			}
		}
	}
}

func TestReportsStatusParam(t *testing.T) {
	cases := []struct {
		target string
		status int
		want   string
	}{
		{"/api/reports", http.StatusOK, "all"},
		{"/api/reports?status=", http.StatusOK, "all"},
		{"/api/reports?status=open", http.StatusOK, "open"},
		{"/api/reports?status=Resolved", http.StatusOK, "resolved"},
		{"/api/reports?status=all", http.StatusOK, "all"},
		{"/api/reports?status=closed", http.StatusBadRequest, ""},
	}

	for _, tc := range cases {
		store := &stubStore{}
		recorder := doRequest(newTestServer(store), http.MethodGet, tc.target)
		body := decodeBody(t, recorder)
		isOK := (tc.status == http.StatusOK)

		if (recorder.Code != tc.status) || (store.status != tc.want) {
			t.Errorf("%s: status = %d, store got %q", tc.target, recorder.Code, store.status)
		}

		if (isOK == true) && (body["status"] != tc.want) {
			t.Errorf("%s: body = %v", tc.target, body)
		}

		if (isOK == false) && ((body["error"] != errStatusInvalid) || (store.calls != 0)) {
			t.Errorf("%s: body = %v, calls = %d", tc.target, body, store.calls)
		}
	}
}
