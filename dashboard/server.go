package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"net/http"
	"path"
	"strings"
	"time"
)

// Route paths.
const (
	healthPath    = "/healthz"
	apiRoot       = "/api"
	apiPrefix     = "/api/"
	assetsPrefix  = "/assets/"
	overviewPath  = "/api/overview"
	platformsPath = "/api/platforms"
	chatsPath     = "/api/chats"
	moneyPath     = "/api/money"
	reportsPath   = "/api/reports"
	indexFile     = "index.html"
)

// Response headers.
const (
	headerAllow          = "Allow"
	headerCacheControl   = "Cache-Control"
	headerContentType    = "Content-Type"
	headerCSP            = "Content-Security-Policy"
	headerContentOptions = "X-Content-Type-Options"
	headerReferrerPolicy = "Referrer-Policy"
)

// Header values.
const (
	allowedMethods     = "GET, HEAD"
	contentSecurity    = "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'"
	contentTypeNoSniff = "nosniff"
	referrerNone       = "no-referrer"
	jsonContentType    = "application/json; charset=utf-8"
	cacheNoStore       = "no-store"
	cacheImmutable     = "public, max-age=31536000, immutable"
	cacheRevalidate    = "no-cache"
)

// Error messages returned to the client.
const (
	errMethodNotAllowed = "method not allowed"
	errNotFound         = "not found"
	errInternal         = "internal error"
	errFrontendMissing  = "frontend not built: run npm run build in dashboard/web"
)

// requestTimeout caps the database work of one request.
const requestTimeout = 15 * time.Second

// loader fetches a response payload.
type loader func(ctx context.Context) (payload, error)

// Server is the dashboard's HTTP handler.
type Server struct {
	store          Store
	timezone       string
	assets         fs.FS
	now            func() time.Time
	routes         map[string]http.HandlerFunc
	allowedHosts   map[string]bool
	blogCollection writeRoute
	blogPost       writeRoute
	blogPublished  writeRoute
	reportItem     writeRoute
	reportReplies  writeRoute
	reportResolved writeRoute
	// settingRequests and settingRequest are /api/settings/requests and
	// /api/settings/requests/{id}.
	settingRequests writeRoute
	settingRequest  writeRoute
}

// NewServer returns the handler serving the API from store and the SPA from
// assets; timezone is reported in every response. Only requests whose Host
// is one of allowedHosts (lower-cased host:port) are answered.
func NewServer(store Store, timezone string, allowedHosts []string, assets fs.FS) *Server {
	result := &Server{store: store, timezone: timezone, assets: assets, now: time.Now}
	result.allowedHosts = hostSet(allowedHosts)
	result.routes = map[string]http.HandlerFunc{
		healthPath:    result.handleHealth,
		overviewPath:  result.handleOverview,
		platformsPath: result.handlePlatforms,
		chatsPath:     result.handleChats,
		moneyPath:     result.handleMoney,
		reportsPath:   result.handleReports,
	}
	result.newBlogRoutes()
	result.newReportRoutes()
	result.newSettingsRoutes()

	return result
}

// ServeHTTP sets the security headers, refuses an unknown Host (a
// DNS-rebinding page) and routes the request: the blog, report and settings
// request routes accept their own methods, every other path only GET and HEAD. No
// response carries CORS headers.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	header := w.Header()
	setSecurityHeaders(header)
	host := strings.ToLower(r.Host)
	isKnownHost := s.allowedHosts[host]
	route, isWriteRoute := s.matchWriteRoute(r.URL.Path)

	switch {
	case isKnownHost == false:
		writeError(w, http.StatusForbidden, errForbidden)
	case isWriteRoute == true:
		s.serveWriteRoute(w, r, route)
	default:
		s.serveReadOnly(w, r)
	}
}

// matchWriteRoute finds the blog, report or settings request route of a
// path; false for any other path, which then stays GET/HEAD only.
func (s *Server) matchWriteRoute(urlPath string) (writeRoute, bool) {
	result, isMatch := s.matchBlogRoute(urlPath)

	if isMatch == false {
		result, isMatch = s.matchReportRoute(urlPath)
	}

	if isMatch == false {
		result, isMatch = s.matchSettingsRoute(urlPath)
	}

	return result, isMatch
}

// hostSet indexes the allowed hosts, lower-cased.
func hostSet(hosts []string) map[string]bool {
	result := make(map[string]bool, len(hosts))

	for _, host := range hosts {
		result[strings.ToLower(host)] = true
	}

	return result
}

// serveReadOnly refuses writes and routes the request.
func (s *Server) serveReadOnly(w http.ResponseWriter, r *http.Request) {
	isAllowed := (r.Method == http.MethodGet) || (r.Method == http.MethodHead)

	if isAllowed == false {
		w.Header().Set(headerAllow, allowedMethods)
		writeError(w, http.StatusMethodNotAllowed, errMethodNotAllowed)
	}

	if isAllowed == true {
		s.route(w, r)
	}
}

// setSecurityHeaders adds the headers every response carries.
func setSecurityHeaders(header http.Header) {
	header.Set(headerCSP, contentSecurity)
	header.Set(headerContentOptions, contentTypeNoSniff)
	header.Set(headerReferrerPolicy, referrerNone)
}

// route dispatches to the API, the static assets or the SPA.
func (s *Server) route(w http.ResponseWriter, r *http.Request) {
	urlPath := r.URL.Path
	handler, isRoute := s.routes[urlPath]
	isAPI := (urlPath == healthPath) || (urlPath == apiRoot) || strings.HasPrefix(urlPath, apiPrefix)

	if isAPI == true {
		w.Header().Set(headerCacheControl, cacheNoStore)
	}

	switch {
	case isRoute:
		handler(w, r)
	case isAPI:
		writeError(w, http.StatusNotFound, errNotFound)
	case strings.HasPrefix(urlPath, assetsPrefix):
		s.serveAsset(w, r)
	default:
		s.serveApp(w, r)
	}
}

// serveAsset serves a hashed build file with a long cache; a missing asset
// is a 404, never the SPA.
func (s *Server) serveAsset(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/")
	isServed := s.serveFile(w, r, name, cacheImmutable)

	if isServed == false {
		http.NotFound(w, r)
	}
}

// serveApp serves a file from the build root (e.g. a favicon) or falls back
// to index.html, so client-side routes load the SPA.
func (s *Server) serveApp(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/")
	isServed := false

	if name != "" {
		isServed = s.serveFile(w, r, name, cacheRevalidate)
	}

	if isServed == false {
		isServed = s.serveFile(w, r, indexFile, cacheRevalidate)
	}

	if isServed == false {
		http.Error(w, errFrontendMissing, http.StatusServiceUnavailable)
	}
}

// serveFile writes a regular file from the assets; false when there is none.
func (s *Server) serveFile(w http.ResponseWriter, r *http.Request, name string, cacheControl string) bool {
	result := false
	cleaned := path.Clean(name)
	isValid := (fs.ValidPath(cleaned) == true) && (cleaned != ".")

	if isValid == true {
		result = s.serveExistingFile(w, r, cleaned, cacheControl)
	}

	return result
}

// serveExistingFile opens a cleaned path and writes it when it is a file.
func (s *Server) serveExistingFile(w http.ResponseWriter, r *http.Request, name string, cacheControl string) bool {
	result := false
	file, err := s.assets.Open(name)

	if err == nil {
		defer file.Close()
		result = writeFile(w, r, file, name, cacheControl)
	}

	return result
}

// writeFile serves an open file with ServeContent (ranges, HEAD, type).
func writeFile(w http.ResponseWriter, r *http.Request, file fs.File, name string, cacheControl string) bool {
	result := false
	info, err := file.Stat()
	seeker, isSeeker := file.(io.ReadSeeker)
	isRegular := (err == nil) && (info.IsDir() == false) && (isSeeker == true)

	if isRegular == true {
		w.Header().Set(headerCacheControl, cacheControl)
		modTime := info.ModTime()
		http.ServeContent(w, r, name, modTime, seeker)
		result = true
	}

	return result
}

// respond answers a request error (400 unless it carries its own status),
// otherwise loads the payload and answers 200.
func (s *Server) respond(w http.ResponseWriter, r *http.Request, paramErr error, load loader) {
	s.respondWithStatus(w, r, http.StatusOK, paramErr, load)
}

// respondWithStatus is respond with the success status (201 on create).
func (s *Server) respondWithStatus(w http.ResponseWriter, r *http.Request, status int, paramErr error, load loader) {
	if paramErr != nil {
		errStatus := requestErrorStatus(paramErr)
		message := paramErr.Error()
		writeError(w, errStatus, message)
	}

	if paramErr == nil {
		s.respondLoaded(w, r, status, load)
	}
}

// respondLoaded writes the payload with status, or the load error.
func (s *Server) respondLoaded(w http.ResponseWriter, r *http.Request, status int, load loader) {
	requestCtx := r.Context()
	ctx, cancel := context.WithTimeout(requestCtx, requestTimeout)
	defer cancel()
	data, err := load(ctx)

	if err != nil {
		writeLoadError(w, r, err)
	}

	if err == nil {
		meta := s.meta()
		data.setMeta(meta)
		writeJSON(w, status, data)
	}
}

// writeLoadError answers a client error found while loading with its own
// status, 404 or 409 for a missing or concurrently changed blog post, 404
// for a missing report or settings request, 409 while a change of the
// setting is already waiting, 400 for a value too large to store; any other
// error is logged and answered with a generic 500.
func writeLoadError(w http.ResponseWriter, r *http.Request, err error) {
	var clientErr *requestError

	switch {
	case errors.As(err, &clientErr):
		writeError(w, clientErr.status, clientErr.message)
	case errors.Is(err, ErrSettingRequestNotFound):
		writeError(w, http.StatusNotFound, errNotFound)
	case errors.Is(err, ErrSettingRequestOpen):
		message := ErrSettingRequestOpen.Error()
		writeError(w, http.StatusConflict, message)
	case errors.Is(err, ErrSettingValueTooLarge):
		message := ErrSettingValueTooLarge.Error()
		writeError(w, http.StatusBadRequest, message)
	case errors.Is(err, ErrBlogPostNotFound):
		writeError(w, http.StatusNotFound, errNotFound)
	case errors.Is(err, ErrReportNotFound):
		writeError(w, http.StatusNotFound, errNotFound)
	case errors.Is(err, ErrBlogPostChanged):
		writeError(w, http.StatusConflict, errChangedElsewhere)
	default:
		log.Printf("dashboard: %s %s failed: %v", r.Method, r.URL.Path, err)
		writeError(w, http.StatusInternalServerError, errInternal)
	}
}

// meta stamps a response with the current time and the dashboard timezone.
func (s *Server) meta() Meta {
	now := s.now()
	result := Meta{GeneratedAt: formatTime(now), Timezone: s.timezone}

	return result
}

// writeError writes the JSON error shape.
func writeError(w http.ResponseWriter, status int, message string) {
	body := errorBody{Error: message}
	writeJSON(w, status, body)
}

// writeJSON encodes value with the status code.
func writeJSON(w http.ResponseWriter, status int, value interface{}) {
	w.Header().Set(headerContentType, jsonContentType)
	w.WriteHeader(status)
	err := json.NewEncoder(w).Encode(value)

	if err != nil {
		log.Printf("dashboard: writing the response failed: %v", err)
	}
}

// formatTime renders a timestamp as RFC3339 in UTC.
func formatTime(value time.Time) string {
	utc := value.UTC()
	result := utc.Format(time.RFC3339)

	return result
}
