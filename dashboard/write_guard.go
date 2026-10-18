package dashboard

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
)

// Request headers and values a blog or report write must carry. A
// cross-site page cannot send the custom header without a CORS preflight,
// which the dashboard never answers.
const (
	headerOrigin           = "Origin"
	headerForwardedProto   = "X-Forwarded-Proto"
	headerDashboardRequest = "X-Dashboard-Request"
	dashboardRequestValue  = "1"
	requestJSONMediaType   = "application/json"
	schemeHTTP             = "http"
	schemeHTTPS            = "https"
	schemeSeparator        = "://"
	forwardedListSeparator = ","
)

// maxRequestBody caps a write's JSON body (64 KiB).
const maxRequestBody = 64 << 10

// Error messages of rejected writes.
const (
	errForbidden       = "forbidden"
	errBodyTooLarge    = "request body too large"
	errBodyEmpty       = "request body is empty"
	errBodyTrailing    = "invalid request body: unexpected data after the JSON object"
	invalidBodyMessage = "invalid request body: %s"
	jsonErrorPrefix    = "json: "
)

// requestError is a client error with its status code.
type requestError struct {
	status  int
	message string
}

// Error returns the message sent to the client.
func (e *requestError) Error() string {
	return e.message
}

// badRequest is a 400 with a formatted message.
func badRequest(format string, args ...interface{}) error {
	message := fmt.Sprintf(format, args...)
	result := &requestError{status: http.StatusBadRequest, message: message}

	return result
}

// requestErrorStatus is the status of a client error; 400 for a plain one.
func requestErrorStatus(err error) int {
	result := http.StatusBadRequest
	var clientErr *requestError

	if errors.As(err, &clientErr) == true {
		result = clientErr.status
	}

	return result
}

// isTrustedWrite reports a write sent by the dashboard page itself: the
// custom header, an Origin equal to the request's own origin and, for a
// body, the JSON content type.
func isTrustedWrite(r *http.Request) bool {
	marker := r.Header.Get(headerDashboardRequest)
	result := (marker == dashboardRequestValue)
	result = result && (isSameOrigin(r) == true)

	if hasBody(r.Method) == true {
		result = result && (hasJSONContentType(r) == true)
	}

	return result
}

// hasBody reports a method whose request carries a JSON body.
func hasBody(method string) bool {
	result := (method == http.MethodPost) || (method == http.MethodPut)

	return result
}

// isSameOrigin compares the Origin header with the request's own origin.
// ServeHTTP has already refused a Host outside the allowed hosts, so a
// DNS-rebinding page, whose Origin matches its own Host, never gets here.
func isSameOrigin(r *http.Request) bool {
	origin := r.Header.Get(headerOrigin)
	expected := requestOrigin(r)
	result := (origin != "") && (r.Host != "") && (strings.EqualFold(origin, expected) == true)

	return result
}

// requestOrigin is scheme://host of the request: the scheme from
// X-Forwarded-Proto behind the proxy, else from TLS; the host from Host.
func requestOrigin(r *http.Request) string {
	scheme := requestScheme(r)
	result := scheme + schemeSeparator + r.Host

	return result
}

// requestScheme reads the first X-Forwarded-Proto value, else TLS.
func requestScheme(r *http.Request) string {
	result := schemeHTTP
	forwarded := r.Header.Get(headerForwardedProto)
	first, _, _ := strings.Cut(forwarded, forwardedListSeparator)
	first = strings.TrimSpace(first)

	if r.TLS != nil {
		result = schemeHTTPS
	}

	if first != "" {
		result = strings.ToLower(first)
	}

	return result
}

// hasJSONContentType accepts application/json with any parameters.
func hasJSONContentType(r *http.Request) bool {
	contentType := r.Header.Get(headerContentType)
	mediaType, _, err := mime.ParseMediaType(contentType)
	result := (err == nil) && (mediaType == requestJSONMediaType)

	return result
}

// decodeJSONBody reads at most maxRequestBody bytes of one JSON object
// into dest, rejecting unknown fields and trailing data.
func decodeJSONBody(w http.ResponseWriter, r *http.Request, dest interface{}) error {
	body := http.MaxBytesReader(w, r.Body, maxRequestBody)
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()
	err := decoder.Decode(dest)

	if err == nil {
		err = expectEnd(decoder)
	}

	if err != nil {
		err = bodyError(err)
	}

	return err
}

// errTrailingData is the error of a body with more than one JSON value.
var errTrailingData = errors.New(errBodyTrailing)

// expectEnd fails when anything but whitespace follows the object.
func expectEnd(decoder *json.Decoder) error {
	var extra json.RawMessage
	result := decoder.Decode(&extra)
	var tooLarge *http.MaxBytesError
	isEnd := errors.Is(result, io.EOF)
	isTooLarge := errors.As(result, &tooLarge)

	if (isEnd == false) && (isTooLarge == false) {
		result = errTrailingData
	}

	if isEnd == true {
		result = nil
	}

	return result
}

// bodyError turns a decoding error into the client error.
func bodyError(err error) error {
	var tooLarge *http.MaxBytesError
	message := err.Error()
	detail := strings.TrimPrefix(message, jsonErrorPrefix)
	result := badRequest(invalidBodyMessage, detail)

	switch {
	case errors.As(err, &tooLarge):
		result = &requestError{status: http.StatusRequestEntityTooLarge, message: errBodyTooLarge}
	case errors.Is(err, io.EOF):
		result = badRequest(errBodyEmpty)
	case errors.Is(err, errTrailingData):
		result = badRequest(errBodyTrailing)
	}

	return result
}
