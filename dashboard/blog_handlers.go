package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Blog routes: /api/blog-posts, /api/blog-posts/{id} and
// /api/blog-posts/{id}/published.
const (
	blogPostsPath    = "/api/blog-posts"
	blogPostPrefix   = "/api/blog-posts/"
	publishedSegment = "published"
	pathSeparator    = "/"
	rowIDBase        = 10
	rowIDBits        = 32
)

// Allow headers of the blog routes.
const (
	allowBlogCollection = "GET, HEAD, POST"
	allowBlogPost       = "GET, HEAD, PUT, DELETE"
	allowBlogPublished  = "PUT"
)

// maxTitleLength is blog_posts.title's VARCHAR(255), counted in characters.
const maxTitleLength = 255

// broadcastPreviewFrame is the text the bot's owner panel puts around a
// post on its Broadcast preview, the screen with the send buttons
// (showBroadcastPreview in app/updates.go; tags removed, as Telegram does
// not count them), with seven-digit user and group counts for headroom.
const broadcastPreviewFrame = "📢 Broadcast Preview\n\n━━━━━━━━━━━━━━━━━━━━\n\n\n\n━━━━━━━━━━━━━━━━━━━━\n\n👤 Individual users: 9999999\n👥 Groups: 9999999\n\nChoose audience:"

// BroadcastContentLimit is the longest content, in Telegram length, whose
// Broadcast preview still fits Telegram's message limit; a longer post
// could never be sent from the owner panel.
var BroadcastContentLimit = TelegramMessageLimit - utf16Length(broadcastPreviewFrame)

// nulCharacter cannot be stored in PostgreSQL text.
const nulCharacter = '\x00'

// Validation and conflict messages.
const (
	errChangedElsewhere  = "changed elsewhere"
	errTitleRequired     = "title is required"
	errTitleTooLong      = "title is too long: %d characters, at most %d"
	errTitleNUL          = "title must not contain NUL characters"
	errContentRequired   = "content is required"
	errContentNUL        = "content must not contain NUL characters"
	errContentHTML       = "content: %s"
	errContentTooLong    = "content is too long: %d characters after formatting, at most %d (Telegram allows %d, the bot's Broadcast preview adds up to %d)"
	errUpdatedAtRequired = "updated_at is required: send the value the post was loaded with"
	errUpdatedAtInvalid  = "invalid updated_at: must be the RFC3339 timestamp the post was loaded with, or null"
	errPublishedRequired = "published is required: true or false"
)

// routeHandler serves one method of a write route (blog posts, reports);
// id is the row of the path, 0 for a collection.
type routeHandler func(w http.ResponseWriter, r *http.Request, id int64)

// writeRoute is a matched blog or report path with the handlers of its
// methods.
type writeRoute struct {
	id       int64
	handlers map[string]routeHandler
	allow    string
}

// blogPostRequest is the body of POST /api/blog-posts.
type blogPostRequest struct {
	Title   string `json:"title"`
	Content string `json:"content"`
}

// blogPostUpdateRequest is the body of PUT /api/blog-posts/{id}; updated_at
// stays raw to tell a missing value from null.
type blogPostUpdateRequest struct {
	Title     string          `json:"title"`
	Content   string          `json:"content"`
	UpdatedAt json.RawMessage `json:"updated_at"`
}

// blogPublishedRequest is the body of PUT /api/blog-posts/{id}/published.
type blogPublishedRequest struct {
	Published *bool `json:"published"`
}

// newBlogRoutes binds the blog handlers of the server.
func (s *Server) newBlogRoutes() {
	s.blogCollection = writeRoute{
		handlers: map[string]routeHandler{
			http.MethodGet:  s.handleListBlogPosts,
			http.MethodHead: s.handleListBlogPosts,
			http.MethodPost: s.handleCreateBlogPost,
		},
		allow: allowBlogCollection,
	}
	s.blogPost = writeRoute{
		handlers: map[string]routeHandler{
			http.MethodGet:    s.handleGetBlogPost,
			http.MethodHead:   s.handleGetBlogPost,
			http.MethodPut:    s.handleUpdateBlogPost,
			http.MethodDelete: s.handleDeleteBlogPost,
		},
		allow: allowBlogPost,
	}
	s.blogPublished = writeRoute{
		handlers: map[string]routeHandler{http.MethodPut: s.handleSetBlogPostPublished},
		allow:    allowBlogPublished,
	}
}

// matchBlogRoute finds the blog route of a path; false for any other path,
// which then stays GET/HEAD only.
func (s *Server) matchBlogRoute(urlPath string) (writeRoute, bool) {
	result := s.blogCollection
	isMatch := (urlPath == blogPostsPath)
	rest, isItem := strings.CutPrefix(urlPath, blogPostPrefix)

	if isItem == true {
		result, isMatch = s.matchBlogItem(rest)
	}

	return result, isMatch
}

// matchBlogItem matches "{id}" and "{id}/published".
func (s *Server) matchBlogItem(rest string) (writeRoute, bool) {
	idText, suffix, hasSuffix := strings.Cut(rest, pathSeparator)
	id, isID := parseRowID(idText)
	result := s.blogPost

	if hasSuffix == true {
		result = s.blogPublished
	}

	isKnownSuffix := (hasSuffix == false) || (suffix == publishedSegment)
	isMatch := (isID == true) && (isKnownSuffix == true)
	result.id = id

	return result, isMatch
}

// parseRowID accepts a canonical positive SERIAL id ("7", not "07").
func parseRowID(text string) (int64, bool) {
	id, err := strconv.ParseInt(text, rowIDBase, rowIDBits)
	canonical := strconv.FormatInt(id, rowIDBase)
	isValid := (err == nil) && (id > 0) && (canonical == text)

	return id, isValid
}

// serveWriteRoute checks the method and, for a write, that the dashboard
// page sent it; these answers are never cached.
func (s *Server) serveWriteRoute(w http.ResponseWriter, r *http.Request, route writeRoute) {
	w.Header().Set(headerCacheControl, cacheNoStore)
	handler, isAllowed := route.handlers[r.Method]
	isRead := (r.Method == http.MethodGet) || (r.Method == http.MethodHead)
	isTrusted := (isRead == true) || (isTrustedWrite(r) == true)

	switch {
	case isAllowed == false:
		w.Header().Set(headerAllow, route.allow)
		writeError(w, http.StatusMethodNotAllowed, errMethodNotAllowed)
	case isTrusted == false:
		writeError(w, http.StatusForbidden, errForbidden)
	default:
		handler(w, r, route.id)
	}
}

// handleListBlogPosts serves GET /api/blog-posts.
func (s *Server) handleListBlogPosts(w http.ResponseWriter, r *http.Request, id int64) {
	load := func(ctx context.Context) (payload, error) {
		posts, err := s.store.BlogPosts(ctx)
		data := &BlogPosts{Posts: posts}

		return data, err
	}

	s.respondLoaded(w, r, http.StatusOK, load)
}

// handleGetBlogPost serves GET /api/blog-posts/{id}.
func (s *Server) handleGetBlogPost(w http.ResponseWriter, r *http.Request, id int64) {
	load := blogPostLoader(func(ctx context.Context) (BlogPost, error) {
		post, err := s.store.BlogPost(ctx, id)

		return post, err
	})

	s.respondLoaded(w, r, http.StatusOK, load)
}

// handleCreateBlogPost serves POST /api/blog-posts.
func (s *Server) handleCreateBlogPost(w http.ResponseWriter, r *http.Request, id int64) {
	request := blogPostRequest{}
	draft := BlogDraft{}
	err := decodeJSONBody(w, r, &request)

	if err == nil {
		draft, err = validateBlogDraft(request.Title, request.Content)
	}

	load := blogPostLoader(func(ctx context.Context) (BlogPost, error) {
		post, createErr := s.store.CreateBlogPost(ctx, draft)

		return post, createErr
	})

	s.respondWithStatus(w, r, http.StatusCreated, err, load)
}

// handleUpdateBlogPost serves PUT /api/blog-posts/{id}.
func (s *Server) handleUpdateBlogPost(w http.ResponseWriter, r *http.Request, id int64) {
	request := blogPostUpdateRequest{}
	draft := BlogDraft{}
	var loadedAt *time.Time
	err := decodeJSONBody(w, r, &request)

	if err == nil {
		draft, err = validateBlogDraft(request.Title, request.Content)
	}

	if err == nil {
		loadedAt, err = parseLoadedAt(request.UpdatedAt)
	}

	load := blogPostLoader(func(ctx context.Context) (BlogPost, error) {
		post, updateErr := s.store.UpdateBlogPost(ctx, id, draft, loadedAt)

		return post, updateErr
	})

	s.respond(w, r, err, load)
}

// handleSetBlogPostPublished serves PUT /api/blog-posts/{id}/published.
func (s *Server) handleSetBlogPostPublished(w http.ResponseWriter, r *http.Request, id int64) {
	request := blogPublishedRequest{}
	err := decodeJSONBody(w, r, &request)

	if (err == nil) && (request.Published == nil) {
		err = badRequest(errPublishedRequired)
	}

	load := blogPostLoader(func(ctx context.Context) (BlogPost, error) {
		published := *request.Published
		post, setErr := s.store.SetBlogPostPublished(ctx, id, published)

		return post, setErr
	})

	s.respond(w, r, err, load)
}

// handleDeleteBlogPost serves DELETE /api/blog-posts/{id}: 204 without a
// body.
func (s *Server) handleDeleteBlogPost(w http.ResponseWriter, r *http.Request, id int64) {
	requestCtx := r.Context()
	ctx, cancel := context.WithTimeout(requestCtx, requestTimeout)
	defer cancel()
	err := s.store.DeleteBlogPost(ctx, id)

	if err != nil {
		writeLoadError(w, r, err)
	}

	if err == nil {
		w.WriteHeader(http.StatusNoContent)
	}
}

// blogPostLoader wraps a one-post load into the {"post": ...} answer.
func blogPostLoader(load func(ctx context.Context) (BlogPost, error)) loader {
	result := func(ctx context.Context) (payload, error) {
		post, err := load(ctx)
		data := &BlogPostAnswer{Post: post}

		return data, err
	}

	return result
}

// validateBlogDraft trims the title and content and checks them.
func validateBlogDraft(title string, content string) (BlogDraft, error) {
	trimmedTitle := strings.TrimSpace(title)
	trimmedContent := strings.TrimSpace(content)
	result := BlogDraft{Title: trimmedTitle, Content: trimmedContent}
	err := validateTitle(result.Title)

	if err == nil {
		err = validateContent(result.Content)
	}

	return result, err
}

// validateTitle checks a trimmed title against VARCHAR(255).
func validateTitle(title string) error {
	var result error
	length := utf8.RuneCountInString(title)

	switch {
	case length == 0:
		result = badRequest(errTitleRequired)
	case length > maxTitleLength:
		result = badRequest(errTitleTooLong, length, maxTitleLength)
	case strings.ContainsRune(title, nulCharacter):
		result = badRequest(errTitleNUL)
	}

	return result
}

// validateContent checks trimmed content against Telegram's HTML parse
// mode and the length the owner panel's Broadcast preview can show.
func validateContent(content string) error {
	var result error
	length, htmlErr := CheckTelegramHTML(content)

	switch {
	case content == "":
		result = badRequest(errContentRequired)
	case strings.ContainsRune(content, nulCharacter):
		result = badRequest(errContentNUL)
	case htmlErr != nil:
		htmlMessage := htmlErr.Error()
		result = badRequest(errContentHTML, htmlMessage)
	case length > BroadcastContentLimit:
		frameLength := TelegramMessageLimit - BroadcastContentLimit
		result = badRequest(errContentTooLong, length, BroadcastContentLimit, TelegramMessageLimit, frameLength)
	}

	return result
}

// parseLoadedAt reads the updated_at the editor loaded: a timestamp, or
// null for a post without one. A missing field is an error.
func parseLoadedAt(raw json.RawMessage) (*time.Time, error) {
	var result *time.Time
	var text *string
	var err error
	isMissing := (len(raw) == 0)

	if isMissing == true {
		err = badRequest(errUpdatedAtRequired)
	}

	if isMissing == false {
		err = unmarshalLoadedAt(raw, &text)
	}

	if (err == nil) && (text != nil) {
		result, err = parseLoadedTime(*text)
	}

	return result, err
}

// unmarshalLoadedAt decodes a JSON string or null.
func unmarshalLoadedAt(raw json.RawMessage, text **string) error {
	var result error
	err := json.Unmarshal(raw, text)

	if err != nil {
		result = badRequest(errUpdatedAtInvalid)
	}

	return result
}

// parseLoadedTime parses an RFC3339 timestamp at PostgreSQL's microsecond
// precision, so it compares equal to the stored updated_at.
func parseLoadedTime(text string) (*time.Time, error) {
	var result *time.Time
	var err error
	parsed, parseErr := time.Parse(time.RFC3339Nano, text)

	if parseErr != nil {
		err = badRequest(errUpdatedAtInvalid)
	}

	if parseErr == nil {
		truncated := parsed.Truncate(time.Microsecond)
		result = &truncated
	}

	return result, err
}
