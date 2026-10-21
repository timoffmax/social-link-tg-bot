package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// outageAlertThreshold is the default number of consecutive failures after
// which a service is considered down and the admin is alerted.
const outageAlertThreshold = 3

// serviceMaxStatus is the highest HTTP status.
const serviceMaxStatus = 599

// serviceProbeSuffix ends the outage-tracker key of a service's hourly probe
// ("<service>-probe"), kept apart from its traffic key.
const serviceProbeSuffix = "-probe"

// Error codes published for a failure (the 'services' status component);
// the raw detail goes only to the owner's Telegram alert.
const (
	ServiceErrorTimeout       = "timeout"
	ServiceErrorHTTP4xx       = "http_4xx"
	ServiceErrorHTTP5xx       = "http_5xx"
	ServiceErrorHTTP429       = "http_429"
	ServiceErrorTransport     = "transport"
	ServiceErrorParse         = "parse"
	ServiceErrorWrongContent  = "wrong_content"
	ServiceErrorNotConfigured = "not_configured"
	ServiceErrorOther         = "other"
)

// serviceErrorCodes are the codes a failure may carry; anything else is
// stored as ServiceErrorOther.
var serviceErrorCodes = []string{
	ServiceErrorTimeout,
	ServiceErrorHTTP4xx,
	ServiceErrorHTTP5xx,
	ServiceErrorHTTP429,
	ServiceErrorTransport,
	ServiceErrorParse,
	ServiceErrorWrongContent,
	ServiceErrorNotConfigured,
	ServiceErrorOther,
}

// Detail fragments (lower case) of each code, checked in the order of
// classifyServiceDetail.
var (
	serviceTimeoutFragments       = []string{"deadline exceeded", "timeout", "timed out"}
	serviceTooManyFragments       = []string{"too many requests"}
	serviceClientErrorFragments   = []string{"proxy authentication required"}
	serviceNotConfiguredFragments = []string{"not configured", "not found at", "no python"}
	serviceTransportFragments     = []string{"connection refused", "connection reset", "no such host", "eof", "tls", "proxyconnect", "dial tcp", "network is unreachable", "broken pipe", "failed to perform"}
	serviceParseFragments         = []string{"unmarshal", "invalid character", "decode", "parse", "json"}
	serviceWrongContentFragments  = []string{"content-type", "content type", "og:", "no media", "not media", "html", "wrong post"}
)

// URLs stripped from a detail, and its HTTP status: right after "http",
// "status" or "code", or in Go's resp.Status form ("407 Proxy Authentication Required").
var (
	serviceDetailURLPattern   = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://\S+`)
	serviceNamedStatusPattern = regexp.MustCompile(`\b(?:status code|http|status|code)(?::\s*|\s+)([1-5]\d\d)\b`)
	serviceStatusTextPattern  = regexp.MustCompile(`\b([1-5]\d\d) [A-Z]`)
)

// serviceHealthEntry is one tracker key; seededFailures is the streak
// restored at start, which does not count towards a new alert.
type serviceHealthEntry struct {
	consecutiveFailures int
	seededFailures      int
	alerted             bool
	lastSuccessAt       time.Time
	lastFailureAt       time.Time
	failingSince        time.Time
	threshold           int
	lastErrorCode       string
}

// ServiceHealth is a copy of one tracker entry; a zero time means none.
type ServiceHealth struct {
	ConsecutiveFailures int
	Threshold           int
	LastSuccessAt       time.Time
	LastFailureAt       time.Time
	FailingSince        time.Time
	LastErrorCode       string
}

var (
	serviceHealthMu    sync.Mutex
	serviceHealthState = make(map[string]*serviceHealthEntry)
)

// Notification hooks, indirected so tests can observe outage and recovery
// alerts without sending Telegram messages.
var (
	notifyAdmin     = NotifyAdmin
	resetAdminAlert = ResetAdminAlert
)

// ReportServiceFailure records a failure of an external service with the
// default outage threshold.
func ReportServiceFailure(service, detail string) {
	ReportServiceFailureWithThreshold(service, detail, outageAlertThreshold)
}

// ReportServiceFailureCode records a failure with an already classified
// code and the default outage threshold; detail goes only to the alert.
func ReportServiceFailureCode(service, code, detail string) {
	reportServiceFailure(service, code, detail, outageAlertThreshold)
}

// ReportServiceFailureWithThreshold records a failure of an external service.
// Once failures become consecutive enough to look like an outage, the admin is
// notified (NotifyAdmin's rate limit turns this into a periodic reminder while
// the outage lasts).
func ReportServiceFailureWithThreshold(service, detail string, threshold int) {
	code := classifyServiceDetail(detail)
	reportServiceFailure(service, code, detail, threshold)
}

// reportServiceFailure records one failure and alerts once the failures since
// the start reach threshold (alert rate-limit stamps do not survive a restart).
func reportServiceFailure(service, code, detail string, threshold int) {
	now := serviceHealthNow()
	knownCode := knownServiceErrorCode(code)
	serviceHealthMu.Lock()

	entry, exists := serviceHealthState[service]

	if exists == false {
		entry = &serviceHealthEntry{}
		serviceHealthState[service] = entry
	}

	if entry.consecutiveFailures == 0 {
		entry.failingSince = now
	}

	entry.consecutiveFailures++
	entry.lastFailureAt = now
	entry.threshold = threshold
	entry.lastErrorCode = knownCode
	failures := entry.consecutiveFailures
	newFailures := failures - entry.seededFailures
	shouldAlert := newFailures >= threshold

	if shouldAlert {
		entry.alerted = true
	}

	serviceHealthMu.Unlock()

	if shouldAlert {
		text := fmt.Sprintf("⚠️ %s looks DOWN: %d consecutive failures.\nLast error: %s", service, failures, detail)
		notifyAdmin("outage:"+service, text)
	}
}

// ReportServiceSuccess records a success and sends the recovery notice after
// an alert, clearing its rate-limit stamp so a repeat outage alerts at once.
func ReportServiceSuccess(service string) {
	now := serviceHealthNow()
	serviceHealthMu.Lock()

	entry, exists := serviceHealthState[service]

	if exists == false {
		entry = &serviceHealthEntry{threshold: outageAlertThreshold}
		serviceHealthState[service] = entry
	}

	wasAlerted := entry.alerted
	entry.consecutiveFailures = 0
	entry.seededFailures = 0
	entry.alerted = false
	entry.lastSuccessAt = now
	entry.failingSince = time.Time{}

	serviceHealthMu.Unlock()

	if wasAlerted {
		resetAdminAlert("outage:" + service)
		notifyAdmin("recovery:"+service, fmt.Sprintf("✅ %s recovered and is resolving media again.", service))
	}
}

// ServiceHealthSnapshot returns a copy of every tracker entry.
func ServiceHealthSnapshot() map[string]ServiceHealth {
	serviceHealthMu.Lock()
	defer serviceHealthMu.Unlock()

	result := make(map[string]ServiceHealth, len(serviceHealthState))

	for service, entry := range serviceHealthState {
		result[service] = ServiceHealth{
			ConsecutiveFailures: entry.consecutiveFailures,
			Threshold:           entry.threshold,
			LastSuccessAt:       entry.lastSuccessAt,
			LastFailureAt:       entry.lastFailureAt,
			FailingSince:        entry.failingSince,
			LastErrorCode:       entry.lastErrorCode,
		}
	}

	return result
}

// knownServiceErrorCode returns code when it is a known one, else
// ServiceErrorOther.
func knownServiceErrorCode(code string) string {
	result := ServiceErrorOther

	if slices.Contains(serviceErrorCodes, code) == true {
		result = code
	}

	return result
}

// classifyServiceErr gives the code of an error a service call returned:
// by its type first, then by its text.
func classifyServiceErr(err error) string {
	result := ""
	var statusErr *httpStatusError
	var netErr net.Error
	var urlErr *url.Error
	var opErr *net.OpError
	var syntaxErr *json.SyntaxError
	var typeErr *json.UnmarshalTypeError

	if err == nil {
		result = ServiceErrorOther
	}

	isTimeout := (err != nil) && (isTimeoutErr(err, &netErr) == true)
	isStatus := (err != nil) && (errors.As(err, &statusErr) == true)
	isTransport := (err != nil) && ((errors.As(err, &urlErr) == true) || (errors.As(err, &opErr) == true))
	isParse := (err != nil) && ((errors.As(err, &syntaxErr) == true) || (errors.As(err, &typeErr) == true))

	if (result == "") && (isTimeout == true) {
		result = ServiceErrorTimeout
	}

	if (result == "") && (isStatus == true) {
		result = classifyServiceStatus(statusErr.StatusCode)
	}

	if (result == "") && (isTransport == true) {
		result = ServiceErrorTransport
	}

	if (result == "") && (isParse == true) {
		result = ServiceErrorParse
	}

	if result == "" {
		detail := err.Error()
		result = classifyServiceDetail(detail)
	}

	return result
}

// isTimeoutErr reports a deadline or a network timeout.
func isTimeoutErr(err error, netErr *net.Error) bool {
	isDeadline := errors.Is(err, context.DeadlineExceeded)
	isNetTimeout := (errors.As(err, netErr) == true) && ((*netErr).Timeout() == true)
	result := (isDeadline == true) || (isNetTimeout == true)

	return result
}

// classifyServiceStatus gives the code of an HTTP status that is a failure;
// any other status is ServiceErrorOther.
func classifyServiceStatus(statusCode int) string {
	result := ServiceErrorOther
	isClientError := (statusCode >= http.StatusBadRequest) && (statusCode < http.StatusInternalServerError)
	isServerError := (statusCode >= http.StatusInternalServerError) && (statusCode <= serviceMaxStatus)

	if isClientError == true {
		result = ServiceErrorHTTP4xx
	}

	if isServerError == true {
		result = ServiceErrorHTTP5xx
	}

	if statusCode == http.StatusTooManyRequests {
		result = ServiceErrorHTTP429
	}

	return result
}

// classifyServiceDetail gives the code of a failure detail without its URLs;
// an explicit HTTP status wins over the text fragments (docs/settings.md).
func classifyServiceDetail(detail string) string {
	result := ""
	withoutURLs := serviceDetailURLPattern.ReplaceAllString(detail, " ")
	lowered := strings.ToLower(withoutURLs)
	statusCode := serviceDetailStatus(withoutURLs, lowered)
	statusResult := classifyServiceStatus(statusCode)

	if statusResult != ServiceErrorOther {
		result = statusResult
	}

	if (result == "") && (containsAnyFragment(lowered, serviceTimeoutFragments) == true) {
		result = ServiceErrorTimeout
	}

	if (result == "") && (containsAnyFragment(lowered, serviceTooManyFragments) == true) {
		result = ServiceErrorHTTP429
	}

	if (result == "") && (containsAnyFragment(lowered, serviceClientErrorFragments) == true) {
		result = ServiceErrorHTTP4xx
	}

	result = firstFragmentCode(result, lowered)

	return result
}

// firstFragmentCode returns result when set, else the code of the first
// fragment list lowered contains, else ServiceErrorOther.
func firstFragmentCode(result string, lowered string) string {
	if (result == "") && (containsAnyFragment(lowered, serviceNotConfiguredFragments) == true) {
		result = ServiceErrorNotConfigured
	}

	if (result == "") && (containsAnyFragment(lowered, serviceTransportFragments) == true) {
		result = ServiceErrorTransport
	}

	if (result == "") && (containsAnyFragment(lowered, serviceParseFragments) == true) {
		result = ServiceErrorParse
	}

	if (result == "") && (containsAnyFragment(lowered, serviceWrongContentFragments) == true) {
		result = ServiceErrorWrongContent
	}

	if result == "" {
		result = ServiceErrorOther
	}

	return result
}

// serviceDetailStatus finds the HTTP status a detail names, or 0.
func serviceDetailStatus(detail string, lowered string) int {
	result := 0
	matches := serviceNamedStatusPattern.FindStringSubmatch(lowered)

	if len(matches) == 2 {
		result, _ = strconv.Atoi(matches[1])
	}

	if result == 0 {
		result = serviceStatusTextCode(detail)
	}

	return result
}

// serviceStatusTextCode finds a status followed by its own reason phrase
// (Go's resp.Status), or 0; "404 Likes" is not one.
func serviceStatusTextCode(detail string) int {
	result := 0
	locations := serviceStatusTextPattern.FindAllStringSubmatchIndex(detail, -1)

	for _, location := range locations {
		code, _ := strconv.Atoi(detail[location[2]:location[3]])
		reason := http.StatusText(code)
		rest := detail[location[3]+1:]
		isStatus := (result == 0) && (reason != "") && (strings.HasPrefix(rest, reason) == true)

		if isStatus == true {
			result = code
		}
	}

	return result
}

// containsAnyFragment reports whether text contains one of fragments.
func containsAnyFragment(text string, fragments []string) bool {
	result := false

	for _, fragment := range fragments {
		result = result || (strings.Contains(text, fragment) == true)
	}

	return result
}

// serviceOutcome is what one call of a newly tracked service tells the
// tracker: nothing (a content or deadline outcome), a success or a failure.
type serviceOutcome int

const (
	serviceOutcomeNone serviceOutcome = iota
	serviceOutcomeSuccess
	serviceOutcomeFailure
)

// reportServiceOutcome feeds one classified outcome into the tracker.
func reportServiceOutcome(service string, outcome serviceOutcome, code string, detail string) {
	if outcome == serviceOutcomeSuccess {
		ReportServiceSuccess(service)
	}

	if outcome == serviceOutcomeFailure {
		ReportServiceFailureCode(service, code, detail)
	}
}

// classifyServiceAnswerStatus counts a 429 or 5xx answer as a failure; any
// other status is about the content (deleted, private) and not counted.
func classifyServiceAnswerStatus(statusCode int) (serviceOutcome, string) {
	outcome := serviceOutcomeNone
	code := ""
	statusResult := classifyServiceStatus(statusCode)
	isFailure := (statusResult == ServiceErrorHTTP429) || (statusResult == ServiceErrorHTTP5xx)

	if isFailure == true {
		outcome = serviceOutcomeFailure
		code = statusResult
	}

	return outcome, code
}
