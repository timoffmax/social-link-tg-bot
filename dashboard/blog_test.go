package dashboard

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// blogStub records the blog calls of a stubStore.
type blogStub struct {
	calls     int
	id        int64
	draft     BlogDraft
	loadedAt  *time.Time
	published *bool
}

func (s *stubStore) BlogPosts(ctx context.Context) ([]BlogPost, error) {
	s.blog.calls++

	return []BlogPost{{ID: 1, Title: "t", Content: "c", Length: 1}}, s.err
}

func (s *stubStore) BlogPost(ctx context.Context, id int64) (BlogPost, error) {
	s.blog.calls++
	s.blog.id = id

	return BlogPost{ID: id}, s.err
}

func (s *stubStore) CreateBlogPost(ctx context.Context, draft BlogDraft) (BlogPost, error) {
	s.blog.calls++
	s.blog.draft = draft

	return BlogPost{ID: 7, Title: draft.Title, Content: draft.Content}, s.err
}

func (s *stubStore) UpdateBlogPost(ctx context.Context, id int64, draft BlogDraft, loadedAt *time.Time) (BlogPost, error) {
	s.blog.calls++
	s.blog.id = id
	s.blog.draft = draft
	s.blog.loadedAt = loadedAt

	return BlogPost{ID: id, Title: draft.Title, Content: draft.Content}, s.err
}

func (s *stubStore) SetBlogPostPublished(ctx context.Context, id int64, published bool) (BlogPost, error) {
	s.blog.calls++
	s.blog.id = id
	s.blog.published = &published

	return BlogPost{ID: id, Published: published}, s.err
}

func (s *stubStore) DeleteBlogPost(ctx context.Context, id int64) error {
	s.blog.calls++
	s.blog.id = id

	return s.err
}

// testOrigin is the origin of httptest requests (Host example.com).
const testOrigin = "http://example.com"

const validCreateBody = `{"title":"Hello","content":"<b>Hi</b>"}`

// blogRequest is a request the dashboard page would send, with options to
// break it.
type blogRequest struct {
	method      string
	target      string
	body        string
	origin      *string
	marker      *string
	contentType *string
	forwarded   string
	tls         bool
	host        string
}

func (b blogRequest) do(handler http.Handler) *httptest.ResponseRecorder {
	request := httptest.NewRequest(b.method, b.target, strings.NewReader(b.body))
	setHeader(request, headerOrigin, b.origin, testOrigin)
	setHeader(request, headerDashboardRequest, b.marker, "1")
	setHeader(request, "Content-Type", b.contentType, "application/json")

	if b.host != "" {
		request.Host = b.host
	}

	if b.forwarded != "" {
		request.Header.Set(headerForwardedProto, b.forwarded)
	}

	if b.tls == true {
		request.TLS = &tls.ConnectionState{}
	}

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	return recorder
}

// setHeader sets the default, a given value, or nothing for an empty one.
func setHeader(request *http.Request, name string, value *string, fallback string) {
	header := fallback

	if value != nil {
		header = *value
	}

	if header != "" {
		request.Header.Set(name, header)
	}
}

func TestBlogWriteProtection(t *testing.T) {
	cases := []struct {
		name    string
		request blogRequest
		status  int
	}{
		{"valid", blogRequest{}, http.StatusCreated},
		{"missing origin", blogRequest{origin: text("")}, http.StatusForbidden},
		{"other origin", blogRequest{origin: text("http://evil.example")}, http.StatusForbidden},
		{"other port", blogRequest{origin: text("http://example.com:8080")}, http.StatusForbidden},
		{"null origin", blogRequest{origin: text("null")}, http.StatusForbidden},
		{"https origin over http", blogRequest{origin: text("https://example.com")}, http.StatusForbidden},
		{"https origin behind proxy", blogRequest{origin: text("https://example.com"), forwarded: "https"}, http.StatusCreated},
		{"first forwarded proto wins", blogRequest{origin: text("https://example.com"), forwarded: "https, http"}, http.StatusCreated},
		{"http origin behind https proxy", blogRequest{forwarded: "https"}, http.StatusForbidden},
		{"https origin over tls", blogRequest{origin: text("https://example.com"), tls: true}, http.StatusCreated},
		{"origin case-insensitive", blogRequest{origin: text("http://EXAMPLE.com")}, http.StatusCreated},
		{"host case-insensitive", blogRequest{host: "EXAMPLE.com"}, http.StatusCreated},
		{"rebinding host with its own origin", blogRequest{host: "evil.example:8090", origin: text("http://evil.example:8090")}, http.StatusForbidden},
		{"missing marker header", blogRequest{marker: text("")}, http.StatusForbidden},
		{"wrong marker header", blogRequest{marker: text("0")}, http.StatusForbidden},
		{"missing content type", blogRequest{contentType: text("")}, http.StatusForbidden},
		{"text content type", blogRequest{contentType: text("text/plain")}, http.StatusForbidden},
		{"form content type", blogRequest{contentType: text("application/x-www-form-urlencoded")}, http.StatusForbidden},
		{"json with charset", blogRequest{contentType: text("application/json; charset=utf-8")}, http.StatusCreated},
		{"oversized body", blogRequest{body: `{"title":"t","content":"` + strings.Repeat("a", maxRequestBody) + `"}`}, http.StatusRequestEntityTooLarge},
		{"unknown field", blogRequest{body: `{"title":"t","content":"c","published":true}`}, http.StatusBadRequest},
		{"trailing data", blogRequest{body: validCreateBody + `{}`}, http.StatusBadRequest},
		{"trailing garbage", blogRequest{body: validCreateBody + `x`}, http.StatusBadRequest},
		{"empty body", blogRequest{body: " "}, http.StatusBadRequest},
		{"not json", blogRequest{body: "title=t"}, http.StatusBadRequest},
		{"wrong field type", blogRequest{body: `{"title":1,"content":"c"}`}, http.StatusBadRequest},
	}

	for _, tc := range cases {
		store := &stubStore{}
		server := newTestServer(store)
		request := tc.request
		request.method = http.MethodPost
		request.target = "/api/blog-posts"

		if request.body == "" {
			request.body = validCreateBody
		}

		recorder := request.do(server)

		if recorder.Code != tc.status {
			t.Errorf("%s: status = %d, want %d (%s)", tc.name, recorder.Code, tc.status, recorder.Body.String())
		}

		isAccepted := tc.status == http.StatusCreated

		if (isAccepted == false) && (store.blog.calls != 0) {
			t.Errorf("%s: store was called", tc.name)
		}

		body := decodeBody(t, recorder)

		if (tc.status == http.StatusForbidden) && (body["error"] != "forbidden") {
			t.Errorf("%s: body = %v", tc.name, body)
		}

		if (tc.status == http.StatusRequestEntityTooLarge) && (body["error"] != "request body too large") {
			t.Errorf("%s: body = %v", tc.name, body)
		}
	}
}

func TestBlogUnknownFieldIsNamed(t *testing.T) {
	recorder := blogRequest{method: http.MethodPost, target: "/api/blog-posts", body: `{"title":"t","content":"c","extra":1}`}.do(newTestServer(&stubStore{}))
	body := decodeBody(t, recorder)

	if body["error"] != `invalid request body: unknown field "extra"` {
		t.Errorf("body = %v", body)
	}
}

func TestBlogWriteProtectionAppliesToEveryWrite(t *testing.T) {
	writes := []blogRequest{
		{method: http.MethodPut, target: "/api/blog-posts/3", body: `{"title":"t","content":"c","updated_at":null}`},
		{method: http.MethodPut, target: "/api/blog-posts/3/published", body: `{"published":true}`},
		{method: http.MethodDelete, target: "/api/blog-posts/3"},
	}

	for _, write := range writes {
		for _, broken := range []blogRequest{{origin: text("")}, {origin: text("http://evil.example")}, {marker: text("")}} {
			store := &stubStore{}
			request := write
			request.origin = broken.origin
			request.marker = broken.marker
			recorder := request.do(newTestServer(store))

			if (recorder.Code != http.StatusForbidden) || (store.blog.calls != 0) {
				t.Errorf("%s %s: status = %d, calls = %d", write.method, write.target, recorder.Code, store.blog.calls)
			}
		}

		if hasBody(write.method) == true {
			assertTextBodyIsRefused(t, write)
		}
	}
}

func assertTextBodyIsRefused(t *testing.T, write blogRequest) {
	t.Helper()
	store := &stubStore{}
	request := write
	request.contentType = text("text/plain")
	recorder := request.do(newTestServer(store))

	if (recorder.Code != http.StatusForbidden) || (store.blog.calls != 0) {
		t.Errorf("%s %s text/plain: status = %d", write.method, write.target, recorder.Code)
	}
}

func TestBlogDeleteNeedsNoContentType(t *testing.T) {
	store := &stubStore{}
	recorder := blogRequest{method: http.MethodDelete, target: "/api/blog-posts/3", contentType: text("")}.do(newTestServer(store))

	if (recorder.Code != http.StatusNoContent) || (recorder.Body.Len() != 0) || (store.blog.id != 3) {
		t.Errorf("status = %d, body = %q, id = %d", recorder.Code, recorder.Body.String(), store.blog.id)
	}
}

func TestBlogResponsesHaveNoCORSHeaders(t *testing.T) {
	server := newTestServer(&stubStore{})
	requests := []blogRequest{
		{method: http.MethodPost, target: "/api/blog-posts", body: validCreateBody},
		{method: http.MethodOptions, target: "/api/blog-posts"},
		{method: http.MethodOptions, target: "/api/blog-posts/1"},
		{method: http.MethodGet, target: "/api/blog-posts"},
		{method: http.MethodPost, target: "/api/blog-posts", origin: text("http://evil.example")},
	}

	for _, request := range requests {
		recorder := request.do(server)

		for name := range recorder.Header() {
			if strings.HasPrefix(strings.ToLower(name), "access-control-") == true {
				t.Errorf("%s %s: CORS header %s", request.method, request.target, name)
			}
		}

		if recorder.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s %s: Cache-Control = %q", request.method, request.target, recorder.Header().Get("Cache-Control"))
		}

		if recorder.Header().Get("Content-Security-Policy") != contentSecurity {
			t.Errorf("%s %s: no CSP", request.method, request.target)
		}
	}
}

func TestBlogMethodRules(t *testing.T) {
	cases := []struct {
		method string
		target string
		status int
		allow  string
	}{
		{http.MethodOptions, "/api/blog-posts", http.StatusMethodNotAllowed, "GET, HEAD, POST"},
		{http.MethodPut, "/api/blog-posts", http.StatusMethodNotAllowed, "GET, HEAD, POST"},
		{http.MethodDelete, "/api/blog-posts", http.StatusMethodNotAllowed, "GET, HEAD, POST"},
		{http.MethodPost, "/api/blog-posts/1", http.StatusMethodNotAllowed, "GET, HEAD, PUT, DELETE"},
		{http.MethodPatch, "/api/blog-posts/1", http.StatusMethodNotAllowed, "GET, HEAD, PUT, DELETE"},
		{http.MethodGet, "/api/blog-posts/1/published", http.StatusMethodNotAllowed, "PUT"},
		{http.MethodPost, "/api/blog-posts/1/published", http.StatusMethodNotAllowed, "PUT"},
		{http.MethodPost, "/api/blog-posts/abc", http.StatusMethodNotAllowed, "GET, HEAD"},
		{http.MethodPut, "/api/blog-posts/1/other", http.StatusMethodNotAllowed, "GET, HEAD"},
		{http.MethodPost, "/api/blog-posts/", http.StatusMethodNotAllowed, "GET, HEAD"},
		{http.MethodPost, "/api/blog-posts-x", http.StatusMethodNotAllowed, "GET, HEAD"},
		{http.MethodPost, "/api/overview", http.StatusMethodNotAllowed, "GET, HEAD"},
		{http.MethodPut, "/", http.StatusMethodNotAllowed, "GET, HEAD"},
		{http.MethodGet, "/api/blog-posts/abc", http.StatusNotFound, ""},
		{http.MethodGet, "/api/blog-posts/0", http.StatusNotFound, ""},
		{http.MethodGet, "/api/blog-posts/07", http.StatusNotFound, ""},
		{http.MethodGet, "/api/blog-posts/+7", http.StatusNotFound, ""},
		{http.MethodGet, "/api/blog-posts/99999999999", http.StatusNotFound, ""},
		{http.MethodGet, "/api/blog-posts/1/other", http.StatusNotFound, ""},
		{http.MethodGet, "/api/blog-posts", http.StatusOK, ""},
		{http.MethodHead, "/api/blog-posts", http.StatusOK, ""},
		{http.MethodGet, "/api/blog-posts/5", http.StatusOK, ""},
		{http.MethodHead, "/api/blog-posts/5", http.StatusOK, ""},
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

		isRefused := tc.status != http.StatusOK

		if (isRefused == true) && (store.blog.calls != 0) {
			t.Errorf("%s %s: store was called", tc.method, tc.target)
		}
	}
}

func TestBlogReadsNeedNoWriteHeaders(t *testing.T) {
	store := &stubStore{}
	recorder := doRequest(newTestServer(store), http.MethodGet, "/api/blog-posts/5")
	body := decodeBody(t, recorder)
	post, _ := body["post"].(map[string]interface{})

	if (recorder.Code != http.StatusOK) || (post["id"] != float64(5)) || (store.blog.id != 5) {
		t.Errorf("status = %d, body = %v", recorder.Code, body)
	}

	if (body["generated_at"] != "2026-10-15T12:00:00Z") || (body["timezone"] != "Europe/Kyiv") {
		t.Errorf("meta = %v", body)
	}

	list := decodeBody(t, doRequest(newTestServer(store), http.MethodGet, "/api/blog-posts"))
	posts, _ := list["posts"].([]interface{})

	if (len(posts) != 1) || (list["timezone"] != "Europe/Kyiv") {
		t.Errorf("list = %v", list)
	}
}

func TestBlogStoreErrorsMapToStatus(t *testing.T) {
	cases := []struct {
		err     error
		status  int
		message string
	}{
		{ErrBlogPostNotFound, http.StatusNotFound, "not found"},
		{ErrBlogPostChanged, http.StatusConflict, "changed elsewhere"},
		{errors.New("pq: permission denied for table blog_posts"), http.StatusInternalServerError, "internal error"},
	}
	requests := []blogRequest{
		{method: http.MethodGet, target: "/api/blog-posts/3"},
		{method: http.MethodPut, target: "/api/blog-posts/3", body: `{"title":"t","content":"c","updated_at":"2026-10-15T12:00:00.123456Z"}`},
		{method: http.MethodPut, target: "/api/blog-posts/3/published", body: `{"published":false}`},
		{method: http.MethodDelete, target: "/api/blog-posts/3"},
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

func TestBlogCreateTrimsAndAnswers201(t *testing.T) {
	store := &stubStore{}
	recorder := blogRequest{method: http.MethodPost, target: "/api/blog-posts", body: `{"title":"  Hello \n","content":"\n <b>Hi</b> \n"}`}.do(newTestServer(store))
	body := decodeBody(t, recorder)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %v", recorder.Code, body)
	}

	want := BlogDraft{Title: "Hello", Content: "<b>Hi</b>"}

	if store.blog.draft != want {
		t.Errorf("draft = %+v", store.blog.draft)
	}

	post, _ := body["post"].(map[string]interface{})

	if (post["id"] != float64(7)) || (body["generated_at"] == nil) {
		t.Errorf("body = %v", body)
	}
}

func TestBlogUpdatePassesTheLoadedTimestampAtMicroseconds(t *testing.T) {
	cases := []struct {
		updatedAt string
		want      *time.Time
	}{
		{`"2026-10-15T12:00:00.123456Z"`, timePtr(time.Date(2026, 10, 15, 12, 0, 0, 123456000, time.UTC))},
		{`"2026-10-15T15:00:00.123456789+03:00"`, timePtr(time.Date(2026, 10, 15, 12, 0, 0, 123456000, time.UTC))},
		{`"2026-10-15T12:00:00Z"`, timePtr(time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC))},
		{`null`, nil},
	}

	for _, tc := range cases {
		store := &stubStore{}
		body := `{"title":"t","content":"c","updated_at":` + tc.updatedAt + `}`
		recorder := blogRequest{method: http.MethodPut, target: "/api/blog-posts/9", body: body}.do(newTestServer(store))

		if (recorder.Code != http.StatusOK) || (store.blog.id != 9) {
			t.Errorf("%s: status = %d, id = %d", tc.updatedAt, recorder.Code, store.blog.id)
		}

		got := store.blog.loadedAt
		isSame := ((got == nil) && (tc.want == nil)) || ((got != nil) && (tc.want != nil) && (got.Equal(*tc.want) == true))

		if isSame == false {
			t.Errorf("%s: loadedAt = %v, want %v", tc.updatedAt, got, tc.want)
		}
	}
}

func timePtr(value time.Time) *time.Time {
	return &value
}

func TestBlogPublishedPassesTheFlag(t *testing.T) {
	for _, flag := range []bool{true, false} {
		store := &stubStore{}
		body := `{"published":false}`

		if flag == true {
			body = `{"published":true}`
		}

		recorder := blogRequest{method: http.MethodPut, target: "/api/blog-posts/4/published", body: body}.do(newTestServer(store))

		if (recorder.Code != http.StatusOK) || (store.blog.published == nil) || (*store.blog.published != flag) {
			t.Errorf("%v: status = %d", flag, recorder.Code)
		}
	}
}

func TestBlogValidationErrors(t *testing.T) {
	longTitle := strings.Repeat("é", 256)
	longContent := strings.Repeat("a", 3964)
	cases := []struct {
		method string
		target string
		body   string
		prefix string
	}{
		{http.MethodPost, "/api/blog-posts", `{"title":"   ","content":"c"}`, "title is required"},
		{http.MethodPost, "/api/blog-posts", `{"content":"c"}`, "title is required"},
		{http.MethodPost, "/api/blog-posts", `{"title":"` + longTitle + `","content":"c"}`, "title is too long: 256 characters, at most 255"},
		{http.MethodPost, "/api/blog-posts", `{"title":"a\u0000b","content":"c"}`, "title must not contain NUL characters"},
		{http.MethodPost, "/api/blog-posts", `{"title":"t","content":" \n "}`, "content is required"},
		{http.MethodPost, "/api/blog-posts", `{"title":"t"}`, "content is required"},
		{http.MethodPost, "/api/blog-posts", `{"title":"t","content":"a\u0000"}`, "content must not contain NUL characters"},
		{http.MethodPost, "/api/blog-posts", `{"title":"t","content":"<b>x</i>"}`, "content: line 1, column 5: </i> closes <b> opened at line 1, column 1"},
		{http.MethodPost, "/api/blog-posts", `{"title":"t","content":"` + longContent + `"}`, "content is too long: 3964 characters after formatting, at most 3963 (Telegram allows 4096, the bot's Broadcast preview adds up to 133)"},
		{http.MethodPut, "/api/blog-posts/1", `{"title":"t","content":"c"}`, "updated_at is required"},
		{http.MethodPut, "/api/blog-posts/1", `{"title":"t","content":"c","updated_at":"yesterday"}`, "invalid updated_at"},
		{http.MethodPut, "/api/blog-posts/1", `{"title":"t","content":"c","updated_at":5}`, "invalid updated_at"},
		{http.MethodPut, "/api/blog-posts/1", `{"title":"","content":"c","updated_at":null}`, "title is required"},
		{http.MethodPut, "/api/blog-posts/1/published", `{}`, "published is required"},
		{http.MethodPut, "/api/blog-posts/1/published", `{"published":null}`, "published is required"},
		{http.MethodPut, "/api/blog-posts/1/published", `{"published":"yes"}`, "invalid request body"},
	}

	for _, tc := range cases {
		store := &stubStore{}
		recorder := blogRequest{method: tc.method, target: tc.target, body: tc.body}.do(newTestServer(store))
		body := decodeBody(t, recorder)
		message, _ := body["error"].(string)

		if (recorder.Code != http.StatusBadRequest) || (strings.HasPrefix(message, tc.prefix) == false) {
			t.Errorf("%s %s %s: status = %d, error = %q", tc.method, tc.target, tc.body, recorder.Code, message)
		}

		if store.blog.calls != 0 {
			t.Errorf("%s: store was called", tc.body)
		}
	}
}

func TestBlogTitleCountsCharacters(t *testing.T) {
	store := &stubStore{}
	title := strings.Repeat("é", 255)
	recorder := blogRequest{method: http.MethodPost, target: "/api/blog-posts", body: `{"title":"` + title + `","content":"c"}`}.do(newTestServer(store))

	if recorder.Code != http.StatusCreated {
		t.Errorf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestBroadcastContentLimitLeavesRoomForThePreview(t *testing.T) {
	if BroadcastContentLimit != 3963 {
		t.Errorf("limit = %d, want 3963", BroadcastContentLimit)
	}
}

func TestBlogContentAtTheLimitIsAccepted(t *testing.T) {
	store := &stubStore{}
	content := strings.Repeat("😀", 1981) + "a"
	recorder := blogRequest{method: http.MethodPost, target: "/api/blog-posts", body: `{"title":"t","content":"` + content + `"}`}.do(newTestServer(store))

	if recorder.Code != http.StatusCreated {
		t.Errorf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	over := blogRequest{method: http.MethodPost, target: "/api/blog-posts", body: `{"title":"t","content":"` + content + `a"}`}.do(newTestServer(&stubStore{}))

	if over.Code != http.StatusBadRequest {
		t.Errorf("over the limit: status = %d", over.Code)
	}
}
