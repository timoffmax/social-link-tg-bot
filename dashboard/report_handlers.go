package dashboard

import (
	"context"
	"net/http"
	"strings"

	"github.com/timoffmax/social-link-bot/model/reportreply"
)

// Report routes: /api/reports/{id}, /api/reports/{id}/replies and
// /api/reports/{id}/resolved. GET /api/reports itself stays a read-only
// route.
const (
	reportPrefix    = "/api/reports/"
	repliesSegment  = "replies"
	resolvedSegment = "resolved"
)

// Allow headers of the report routes.
const (
	allowReportItem     = "GET, HEAD"
	allowReportReplies  = "POST"
	allowReportResolved = "PUT"
)

// ReplyMessagePrefix is the heading the bot puts above a reply to a report
// (reportreply.Prefix, shared with the bot that sends it); the reply text
// follows it. GET /api/reports returns it, so the page shows the same one.
const ReplyMessagePrefix = reportreply.Prefix

// ReplyTextLimit is the longest reply text, in Telegram length, whose
// message with ReplyMessagePrefix still fits Telegram's limit.
var ReplyTextLimit = TelegramMessageLimit - telegramLength(ReplyMessagePrefix)

// Report status filter of GET /api/reports.
const (
	paramStatus          = "status"
	ReportStatusAll      = "all"
	ReportStatusOpen     = "open"
	ReportStatusResolved = "resolved"
	defaultReportStatus  = ReportStatusAll
)

// Validation messages of the report writes.
const (
	errStatusInvalid    = "invalid status: must be open, resolved or all"
	errReplyRequired    = "text is required"
	errReplyNUL         = "text must not contain NUL characters"
	errReplyHTML        = "text: %s"
	errReplyTooLong     = "text is too long: %d characters after formatting, at most %d (Telegram allows %d, the reply's heading takes %d)"
	errResolveRequired  = "resolve is required: true or false"
	errResolvedRequired = "resolved is required: true or false"
)

// reportStatuses are the accepted values of the status filter.
var reportStatuses = map[string]bool{
	ReportStatusAll:      true,
	ReportStatusOpen:     true,
	ReportStatusResolved: true,
}

// reportReplyRequest is the body of POST /api/reports/{id}/replies.
type reportReplyRequest struct {
	Text    string `json:"text"`
	Resolve *bool  `json:"resolve"`
}

// reportResolvedRequest is the body of PUT /api/reports/{id}/resolved.
type reportResolvedRequest struct {
	Resolved *bool `json:"resolved"`
}

// newReportRoutes binds the report handlers of the server.
func (s *Server) newReportRoutes() {
	s.reportItem = writeRoute{
		handlers: map[string]routeHandler{
			http.MethodGet:  s.handleGetReport,
			http.MethodHead: s.handleGetReport,
		},
		allow: allowReportItem,
	}
	s.reportReplies = writeRoute{
		handlers: map[string]routeHandler{http.MethodPost: s.handleQueueReportReply},
		allow:    allowReportReplies,
	}
	s.reportResolved = writeRoute{
		handlers: map[string]routeHandler{http.MethodPut: s.handleSetReportResolved},
		allow:    allowReportResolved,
	}
}

// matchReportRoute matches "/api/reports/{id}" with an optional "replies"
// or "resolved" segment; false for any other path.
func (s *Server) matchReportRoute(urlPath string) (writeRoute, bool) {
	result := s.reportItem
	rest, isItem := strings.CutPrefix(urlPath, reportPrefix)
	idText, suffix, hasSuffix := strings.Cut(rest, pathSeparator)
	id, isID := parseRowID(idText)

	if hasSuffix == true {
		result = s.reportSubRoute(suffix)
	}

	isKnownSuffix := (hasSuffix == false) || (result.allow != "")
	isMatch := (isItem == true) && (isID == true) && (isKnownSuffix == true)
	result.id = id

	return result, isMatch
}

// reportSubRoute is the route of a segment after the report id; an empty
// route for an unknown one.
func (s *Server) reportSubRoute(suffix string) writeRoute {
	result := writeRoute{}

	switch suffix {
	case repliesSegment:
		result = s.reportReplies
	case resolvedSegment:
		result = s.reportResolved
	}

	return result
}

// parseReportStatus reads the status filter: the default when it is missing
// or empty, an error for an unknown value.
func parseReportStatus(r *http.Request) (string, error) {
	result := defaultReportStatus
	var err error
	query := r.URL.Query()
	value := query.Get(paramStatus)
	raw := strings.TrimSpace(value)
	status := strings.ToLower(raw)

	if (status != "") && (reportStatuses[status] == false) {
		err = badRequest(errStatusInvalid)
	}

	if (status != "") && (err == nil) {
		result = status
	}

	return result, err
}

// handleGetReport serves GET /api/reports/{id}: the report with its
// replies, polled by the page while a queued reply waits for the bot.
func (s *Server) handleGetReport(w http.ResponseWriter, r *http.Request, id int64) {
	load := func(ctx context.Context) (payload, error) {
		report, err := s.store.Report(ctx, id)
		data := &ReportAnswer{Report: report}

		return data, err
	}

	s.respondLoaded(w, r, http.StatusOK, load)
}

// handleQueueReportReply serves POST /api/reports/{id}/replies: queues a
// reply for the bot to send; with resolve the bot resolves the report once
// the reply is delivered.
func (s *Server) handleQueueReportReply(w http.ResponseWriter, r *http.Request, id int64) {
	request := reportReplyRequest{}
	replyText := ""
	err := decodeJSONBody(w, r, &request)

	if err == nil {
		replyText, err = validateReplyText(request.Text)
	}

	if (err == nil) && (request.Resolve == nil) {
		err = badRequest(errResolveRequired)
	}

	load := func(ctx context.Context) (payload, error) {
		resolve := *request.Resolve
		reply, report, queueErr := s.store.QueueReportReply(ctx, id, replyText, resolve)
		data := &ReportReplyAnswer{Reply: reply, Report: report}

		return data, queueErr
	}

	s.respondWithStatus(w, r, http.StatusCreated, err, load)
}

// handleSetReportResolved serves PUT /api/reports/{id}/resolved.
func (s *Server) handleSetReportResolved(w http.ResponseWriter, r *http.Request, id int64) {
	request := reportResolvedRequest{}
	err := decodeJSONBody(w, r, &request)

	if (err == nil) && (request.Resolved == nil) {
		err = badRequest(errResolvedRequired)
	}

	load := func(ctx context.Context) (payload, error) {
		resolved := *request.Resolved
		report, setErr := s.store.SetReportResolved(ctx, id, resolved)
		data := &ReportAnswer{Report: report}

		return data, setErr
	}

	s.respond(w, r, err, load)
}

// validateReplyText trims a reply and checks it against Telegram's HTML
// parse mode and the length left after the reply's heading.
func validateReplyText(text string) (string, error) {
	var err error
	result := strings.TrimSpace(text)
	length, htmlErr := CheckTelegramHTML(result)

	switch {
	case result == "":
		err = badRequest(errReplyRequired)
	case strings.ContainsRune(result, nulCharacter):
		err = badRequest(errReplyNUL)
	case htmlErr != nil:
		htmlMessage := htmlErr.Error()
		err = badRequest(errReplyHTML, htmlMessage)
	case length > ReplyTextLimit:
		prefixLength := TelegramMessageLimit - ReplyTextLimit
		err = badRequest(errReplyTooLong, length, ReplyTextLimit, TelegramMessageLimit, prefixLength)
	}

	return result, err
}
