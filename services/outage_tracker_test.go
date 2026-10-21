package services

// Tests for the outage tracker's alerting contract. Every test installs stub
// notification hooks before touching the tracker, so no test can reach Telegram
// or the network even when a token and admin ID are present in the environment.
// The tracker's state is package-global, so these tests must not run in
// parallel with each other.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// adminAlert is one recorded call to the stubbed notifyAdmin hook.
type adminAlert struct {
	key  string
	text string
}

// alertRecorder captures outage and recovery notifications in place of real
// Telegram sends. It carries its own mutex so the harness itself stays
// race-free while the tracker is hammered from many goroutines.
type alertRecorder struct {
	mu     sync.Mutex
	alerts []adminAlert
	resets []string
}

// notify stands in for NotifyAdmin.
func (r *alertRecorder) notify(alertKey, text string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.alerts = append(r.alerts, adminAlert{key: alertKey, text: text})

	return true
}

// reset stands in for ResetAdminAlert.
func (r *alertRecorder) reset(alertKey string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.resets = append(r.resets, alertKey)
}

// recordedAlerts returns a copy of the captured notifications.
func (r *alertRecorder) recordedAlerts() []adminAlert {
	r.mu.Lock()
	defer r.mu.Unlock()

	result := make([]adminAlert, len(r.alerts))
	copy(result, r.alerts)

	return result
}

// recordedResets returns a copy of the captured rate-limit resets.
func (r *alertRecorder) recordedResets() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	result := make([]string, len(r.resets))
	copy(result, r.resets)

	return result
}

// alertsWithKey returns the captured notifications sent under alertKey.
func (r *alertRecorder) alertsWithKey(alertKey string) []adminAlert {
	result := []adminAlert{}

	for _, alert := range r.recordedAlerts() {
		if alert.key == alertKey {
			result = append(result, alert)
		}
	}

	return result
}

// newAlertRecorder swaps the tracker's notification hooks for recording stubs
// and clears the shared health map, restoring both when the test ends so no
// test leaks state into another.
func newAlertRecorder(t *testing.T) *alertRecorder {
	t.Helper()

	originalNotify := notifyAdmin
	originalReset := resetAdminAlert

	recorder := &alertRecorder{}

	clearServiceHealthState()

	notifyAdmin = recorder.notify
	resetAdminAlert = recorder.reset

	t.Cleanup(func() {
		notifyAdmin = originalNotify
		resetAdminAlert = originalReset

		clearServiceHealthState()
	})

	return recorder
}

// clearServiceHealthState empties the package-level health map.
func clearServiceHealthState() {
	serviceHealthMu.Lock()
	serviceHealthState = make(map[string]*serviceHealthEntry)
	serviceHealthMu.Unlock()
}

// recordedFailureCount reports the tracker's consecutive failure count for a
// service, read under the tracker's own lock.
func recordedFailureCount(service string) int {
	result := 0

	serviceHealthMu.Lock()
	defer serviceHealthMu.Unlock()

	entry, exists := serviceHealthState[service]

	if exists {
		result = entry.consecutiveFailures
	}

	return result
}

func TestReportServiceFailureAlertsAtThreshold(t *testing.T) {
	const threshold = 3

	cases := []struct {
		name       string
		failures   int
		wantAlerts int
	}{
		{"single failure stays quiet", 1, 0},
		{"one short of the threshold stays quiet", threshold - 1, 0},
		{"threshold reached alerts once", threshold, 1},
		{"failures past the threshold keep alerting", threshold + 2, 3},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			recorder := newAlertRecorder(t)

			for i := 0; i < c.failures; i++ {
				ReportServiceFailureWithThreshold("kkinstagram", "HTTP 503", threshold)
			}

			alerts := recorder.alertsWithKey("outage:kkinstagram")

			if len(alerts) != c.wantAlerts {
				t.Errorf("after %d failures got %d alerts, want %d", c.failures, len(alerts), c.wantAlerts)
			}

			if got := recordedFailureCount("kkinstagram"); got != c.failures {
				t.Errorf("consecutive failure count = %d, want %d", got, c.failures)
			}
		})
	}
}

func TestReportServiceFailureAlertContent(t *testing.T) {
	recorder := newAlertRecorder(t)

	ReportServiceFailureWithThreshold("vxreddit", "first failure", 3)
	ReportServiceFailureWithThreshold("vxreddit", "second failure", 3)
	ReportServiceFailureWithThreshold("vxreddit", "HTTP 403 from cdn", 3)

	alerts := recorder.recordedAlerts()

	if len(alerts) != 1 {
		t.Fatalf("got %d alerts, want 1: %+v", len(alerts), alerts)
	}

	if alerts[0].key != "outage:vxreddit" {
		t.Errorf("alert key = %q, want %q", alerts[0].key, "outage:vxreddit")
	}

	wantFragments := []string{"vxreddit", "3", "HTTP 403 from cdn"}

	for _, fragment := range wantFragments {
		if strings.Contains(alerts[0].text, fragment) == false {
			t.Errorf("alert text %q is missing %q", alerts[0].text, fragment)
		}
	}

	// The alert must report the failure that triggered it, not an earlier one.
	if strings.Contains(alerts[0].text, "first failure") {
		t.Errorf("alert text carries a stale error detail: %q", alerts[0].text)
	}
}

func TestReportServiceSuccessResetsFailureStreak(t *testing.T) {
	const threshold = 3

	recorder := newAlertRecorder(t)

	ReportServiceFailureWithThreshold("kktiktok", "HTTP 502", threshold)
	ReportServiceFailureWithThreshold("kktiktok", "HTTP 502", threshold)
	ReportServiceSuccess("kktiktok")

	if got := recordedFailureCount("kktiktok"); got != 0 {
		t.Fatalf("failure count after success = %d, want 0", got)
	}

	// The streak restarts from zero, so two more failures must still be silent.
	ReportServiceFailureWithThreshold("kktiktok", "HTTP 502", threshold)
	ReportServiceFailureWithThreshold("kktiktok", "HTTP 502", threshold)

	if alerts := recorder.recordedAlerts(); len(alerts) != 0 {
		t.Fatalf("got %d alerts before the threshold was re-reached: %+v", len(alerts), alerts)
	}

	ReportServiceFailureWithThreshold("kktiktok", "HTTP 502", threshold)

	alerts := recorder.alertsWithKey("outage:kktiktok")

	if len(alerts) != 1 {
		t.Fatalf("got %d alerts after the streak was re-reached, want 1: %+v", len(alerts), alerts)
	}

	if strings.Contains(alerts[0].text, "3 consecutive") == false {
		t.Errorf("alert must count the new streak from zero, got %q", alerts[0].text)
	}
}

func TestReportServiceSuccessRecoveryNotice(t *testing.T) {
	const threshold = 3

	cases := []struct {
		name           string
		failures       int
		wantResets     []string
		wantRecoveries int
	}{
		{"never failed", 0, []string{}, 0},
		{"failed below the threshold", threshold - 1, []string{}, 0},
		{"failed past the threshold", threshold, []string{"outage:proxy-pool"}, 1},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			recorder := newAlertRecorder(t)

			for i := 0; i < c.failures; i++ {
				ReportServiceFailureWithThreshold("proxy-pool", "all proxies answered 407", threshold)
			}

			// Called twice on purpose: recovery is reported once per outage, so
			// the second success must be silent.
			ReportServiceSuccess("proxy-pool")
			ReportServiceSuccess("proxy-pool")

			resets := recorder.recordedResets()

			if len(resets) != len(c.wantResets) {
				t.Fatalf("got %d rate-limit resets, want %d: %v", len(resets), len(c.wantResets), resets)
			}

			for i, want := range c.wantResets {
				if resets[i] != want {
					t.Errorf("reset[%d] = %q, want %q", i, resets[i], want)
				}
			}

			recoveries := recorder.alertsWithKey("recovery:proxy-pool")

			if len(recoveries) != c.wantRecoveries {
				t.Fatalf("got %d recovery notices, want %d: %+v", len(recoveries), c.wantRecoveries, recoveries)
			}

			if len(recoveries) > 0 {
				if strings.Contains(recoveries[0].text, "proxy-pool") == false {
					t.Errorf("recovery text %q is missing the service name", recoveries[0].text)
				}
			}

			if got := recordedFailureCount("proxy-pool"); got != 0 {
				t.Errorf("failure count after success = %d, want 0", got)
			}
		})
	}
}

func TestOutageTrackerIsolatesServices(t *testing.T) {
	const threshold = 3

	recorder := newAlertRecorder(t)

	// kktiktok fails alongside kkinstagram but never reaches the threshold.
	ReportServiceFailureWithThreshold("kkinstagram", "HTTP 503", threshold)
	ReportServiceFailureWithThreshold("kktiktok", "HTTP 503", threshold)
	ReportServiceFailureWithThreshold("kkinstagram", "HTTP 503", threshold)
	ReportServiceFailureWithThreshold("kktiktok", "HTTP 503", threshold)
	ReportServiceFailureWithThreshold("kkinstagram", "HTTP 503", threshold)

	alerts := recorder.recordedAlerts()

	if len(alerts) != 1 {
		t.Fatalf("got %d alerts, want 1: %+v", len(alerts), alerts)
	}

	if alerts[0].key != "outage:kkinstagram" {
		t.Errorf("alert key = %q, want %q", alerts[0].key, "outage:kkinstagram")
	}

	if got := recordedFailureCount("kktiktok"); got != 2 {
		t.Errorf("kktiktok failure count = %d, want 2", got)
	}

	// Recovering one service must not clear or report the other.
	ReportServiceSuccess("kkinstagram")

	if got := recordedFailureCount("kktiktok"); got != 2 {
		t.Errorf("kktiktok failure count after kkinstagram recovery = %d, want 2", got)
	}

	if recoveries := recorder.alertsWithKey("recovery:kktiktok"); len(recoveries) != 0 {
		t.Errorf("kktiktok must not report a recovery: %+v", recoveries)
	}
}

func TestReportServiceFailureThresholdSelection(t *testing.T) {
	cases := []struct {
		name       string
		report     func(service, detail string)
		failures   int
		wantAlerts int
	}{
		{
			name:       "default threshold stays quiet one failure short",
			report:     ReportServiceFailure,
			failures:   outageAlertThreshold - 1,
			wantAlerts: 0,
		},
		{
			name:       "default threshold alerts on the third failure",
			report:     ReportServiceFailure,
			failures:   outageAlertThreshold,
			wantAlerts: 1,
		},
		{
			name: "custom threshold of one alerts immediately",
			report: func(service, detail string) {
				ReportServiceFailureWithThreshold(service, detail, 1)
			},
			failures:   1,
			wantAlerts: 1,
		},
		{
			name: "custom threshold above the default defers the alert",
			report: func(service, detail string) {
				ReportServiceFailureWithThreshold(service, detail, 5)
			},
			failures:   4,
			wantAlerts: 0,
		},
		{
			name: "custom threshold above the default alerts once reached",
			report: func(service, detail string) {
				ReportServiceFailureWithThreshold(service, detail, 5)
			},
			failures:   5,
			wantAlerts: 1,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			recorder := newAlertRecorder(t)

			for i := 0; i < c.failures; i++ {
				c.report("vxreddit", "HTTP 503")
			}

			if alerts := recorder.recordedAlerts(); len(alerts) != c.wantAlerts {
				t.Errorf("after %d failures got %d alerts, want %d: %+v", c.failures, len(alerts), c.wantAlerts, alerts)
			}
		})
	}
}

func TestOutageTrackerConcurrentFailuresAreCountedExactly(t *testing.T) {
	const reporters = 2000

	recorder := newAlertRecorder(t)

	var waitGroup sync.WaitGroup

	// A threshold of one puts every reporter through the alerting path, so the
	// counter and the notification hook are both exercised under contention.
	for i := 0; i < reporters; i++ {
		waitGroup.Add(1)

		go func() {
			defer waitGroup.Done()

			ReportServiceFailureWithThreshold("kkinstagram", "HTTP 503", 1)
		}()
	}

	waitGroup.Wait()

	if got := recordedFailureCount("kkinstagram"); got != reporters {
		t.Errorf("consecutive failure count = %d, want %d", got, reporters)
	}

	alerts := recorder.recordedAlerts()

	if len(alerts) != reporters {
		t.Fatalf("got %d alerts, want %d", len(alerts), reporters)
	}

	// The counts carried by the alerts must be exactly 1..reporters: a repeated
	// or missing count means an increment was lost to a race.
	seen := make(map[int]bool, reporters)

	for _, alert := range alerts {
		count := alertTextFailureCount(t, alert.text)

		if seen[count] {
			t.Fatalf("failure count %d was reported twice, an increment was lost", count)
		}

		seen[count] = true
	}

	for i := 1; i <= reporters; i++ {
		if seen[i] == false {
			t.Fatalf("no alert reported failure count %d", i)
		}
	}
}

// alertTextFailureCount extracts the consecutive failure count an outage alert
// text reports, failing the test if the text does not carry one.
func alertTextFailureCount(t *testing.T, text string) int {
	t.Helper()

	const marker = " consecutive failures"

	end := strings.Index(text, marker)

	if end < 0 {
		t.Fatalf("alert text reports no failure count: %q", text)
	}

	start := strings.LastIndexByte(text[:end], ' ') + 1

	result, err := strconv.Atoi(text[start:end])

	if err != nil {
		t.Fatalf("unparsable failure count %q in alert text %q", text[start:end], text)
	}

	return result
}

// serviceTestStart is the fake clock's start in the tracker tests.
var serviceTestStart = time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)

// useServiceClock pins the tracker's clock to *now for the test.
func useServiceClock(t *testing.T, now *time.Time) {
	t.Helper()

	previous := serviceHealthNow
	serviceHealthNow = func() time.Time {
		return *now
	}

	t.Cleanup(func() {
		serviceHealthNow = previous
	})
}

// serviceHealthOf returns the snapshot entry of service.
func serviceHealthOf(t *testing.T, service string) ServiceHealth {
	t.Helper()

	result, isFound := ServiceHealthSnapshot()[service]

	if isFound == false {
		t.Fatalf("no tracker entry for %s", service)
	}

	return result
}

func TestFailureRecordsTimesStreakStartThresholdAndCode(t *testing.T) {
	newAlertRecorder(t)
	now := serviceTestStart
	useServiceClock(t, &now)

	ReportServiceFailureWithThreshold("vxreddit", "probe HTTP 530", 2)
	first := now
	now = now.Add(time.Minute)
	ReportServiceFailureCode("vxreddit", ServiceErrorParse, "bad page")

	health := serviceHealthOf(t, "vxreddit")

	if (health.FailingSince.Equal(first) == false) || (health.LastFailureAt.Equal(now) == false) {
		t.Fatalf("failing since must be the first failure, last failure the latest: %+v", health)
	}

	if (health.ConsecutiveFailures != 2) || (health.Threshold != outageAlertThreshold) || (health.LastErrorCode != ServiceErrorParse) {
		t.Fatalf("got %+v", health)
	}

	if health.LastSuccessAt.IsZero() == false {
		t.Fatalf("no success yet: %+v", health)
	}
}

func TestFailureDetailIsClassifiedAndUnknownCodesAreOther(t *testing.T) {
	newAlertRecorder(t)

	ReportServiceFailure("kkinstagram", "probe HTTP 530")
	ReportServiceFailureCode("kktiktok", "made-up", "x")

	if code := serviceHealthOf(t, "kkinstagram").LastErrorCode; code != ServiceErrorHTTP5xx {
		t.Fatalf("detail code: got %s", code)
	}

	if code := serviceHealthOf(t, "kktiktok").LastErrorCode; code != ServiceErrorOther {
		t.Fatalf("unknown code: got %s", code)
	}
}

func TestSuccessClearsTheStreakAndKeepsTheLastFailure(t *testing.T) {
	newAlertRecorder(t)
	now := serviceTestStart
	useServiceClock(t, &now)

	ReportServiceFailureCode("tikwm", ServiceErrorHTTP429, "limit")
	failedAt := now
	now = now.Add(time.Minute)
	ReportServiceSuccess("tikwm")

	health := serviceHealthOf(t, "tikwm")

	if (health.ConsecutiveFailures != 0) || (health.FailingSince.IsZero() == false) || (health.LastSuccessAt.Equal(now) == false) {
		t.Fatalf("got %+v", health)
	}

	if (health.LastFailureAt.Equal(failedAt) == false) || (health.LastErrorCode != ServiceErrorHTTP429) {
		t.Fatalf("the last failure must be kept: %+v", health)
	}

	now = now.Add(time.Minute)
	ReportServiceFailureCode("tikwm", ServiceErrorTimeout, "slow")

	if since := serviceHealthOf(t, "tikwm").FailingSince; since.Equal(now) == false {
		t.Fatalf("a new streak starts at its first failure: %v", since)
	}
}

func TestSuccessOfAnUnknownServiceCreatesItsEntryQuietly(t *testing.T) {
	recorder := newAlertRecorder(t)
	now := serviceTestStart
	useServiceClock(t, &now)

	ReportServiceSuccess("fxtwitter")

	health := serviceHealthOf(t, "fxtwitter")

	if (health.LastSuccessAt.Equal(now) == false) || (health.Threshold != outageAlertThreshold) || (health.LastErrorCode != "") {
		t.Fatalf("got %+v", health)
	}

	if (len(recorder.recordedAlerts()) != 0) || (len(recorder.recordedResets()) != 0) {
		t.Fatalf("a first success must not notify: %+v %v", recorder.recordedAlerts(), recorder.recordedResets())
	}
}

func TestServiceHealthSnapshotIsACopy(t *testing.T) {
	newAlertRecorder(t)

	ReportServiceFailure("facebed", "HTTP 503")
	snapshot := ServiceHealthSnapshot()
	entry := snapshot["facebed"]
	entry.ConsecutiveFailures = 99
	snapshot["facebed"] = entry
	delete(snapshot, "facebed")

	if got := recordedFailureCount("facebed"); got != 1 {
		t.Fatalf("changing the snapshot changed the tracker: %d", got)
	}
}

func TestAlertKeepsTheRawDetailWithACode(t *testing.T) {
	recorder := newAlertRecorder(t)

	for i := 0; i < outageAlertThreshold; i++ {
		ReportServiceFailureCode("vxthreads", ServiceErrorWrongContent, "no Threads post on the vxthreads page")
	}

	alerts := recorder.alertsWithKey("outage:vxthreads")

	if (len(alerts) != 1) || (strings.Contains(alerts[0].text, "no Threads post on the vxthreads page") == false) {
		t.Fatalf("got %+v", alerts)
	}
}

func TestClassifyServiceDetail(t *testing.T) {
	cases := map[string]string{
		"probe HTTP 530": ServiceErrorHTTP5xx,
		"all 3 configured proxies are failing (last error: Get \"https://api.telegram.org/\": Proxy Authentication Required). Reddit extraction is DOWN": ServiceErrorHTTP4xx,
		"proxyconnect tcp: 407 Proxy Authentication Required": ServiceErrorHTTP4xx,
		"context deadline exceeded":                           ServiceErrorTimeout,
		"probe request failed: net/http: request canceled (Client.Timeout exceeded while awaiting headers)": ServiceErrorTimeout,
		"dial tcp: connection refused":                            ServiceErrorTransport,
		"failed to parse fxtwitter JSON: unexpected end":          ServiceErrorParse,
		"no og:video in probe response":                           ServiceErrorWrongContent,
		"fxtwitter API HTTP 429":                                  ServiceErrorHTTP429,
		"Too Many Requests":                                       ServiceErrorHTTP429,
		"apify status 502: bad gateway":                           ServiceErrorHTTP5xx,
		"page https://www.threads.com/@a/post/X/embed status 403": ServiceErrorHTTP4xx,
		"hikerapi status code: 404":                               ServiceErrorHTTP4xx,
		"9gag_fetch.py not found at /srv/scripts/9gag_fetch.py":   ServiceErrorNotConfigured,
		"no Python with curl_cffi found":                          ServiceErrorNotConfigured,
		"kktiktok returned non-video content-type \"text/html\"":  ServiceErrorWrongContent,
		"unexpected EOF":                                          ServiceErrorTransport,
		"something odd happened":                                  ServiceErrorOther,
		"tweet 1234567 has 404 likes":                             ServiceErrorOther,
		"probe HTTP 504 Gateway Timeout":                          ServiceErrorHTTP5xx,
		"GET https://t.co/404 failed: connection reset":           ServiceErrorTransport,
		"decode error at 502":                                     ServiceErrorParse,
		"facebed probe status 530":                                ServiceErrorHTTP5xx,
		"bad status code: 404":                                    ServiceErrorHTTP4xx,
		"upstream status code: 504 after timeout":                 ServiceErrorHTTP5xx,
		"Get \"https://x/\": 503 Service Unavailable (timeout)":   ServiceErrorHTTP5xx,
		"Reply 404 Likes":                                         ServiceErrorOther,
		"unicode 5040 chars":                                      ServiceErrorOther,
		"status 5040":                                             ServiceErrorOther,
		"https://status.example/http/503 timed out":               ServiceErrorTimeout,
		"": ServiceErrorOther,
	}

	for detail, want := range cases {
		if got := classifyServiceDetail(detail); got != want {
			t.Errorf("%q: got %s, want %s", detail, got, want)
		}
	}
}

// timeoutNetErr is a net.Error that timed out.
type timeoutNetErr struct{}

func (timeoutNetErr) Error() string {
	return "i/o wait"
}

func (timeoutNetErr) Timeout() bool {
	return true
}

func (timeoutNetErr) Temporary() bool {
	return true
}

func TestClassifyServiceErr(t *testing.T) {
	var syntaxErr *json.SyntaxError
	syntaxErr = json.Unmarshal([]byte("{"), &struct{}{}).(*json.SyntaxError)
	typeErr := json.Unmarshal([]byte(`{"a":"x"}`), &struct{ A int }{})
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"deadline", context.DeadlineExceeded, ServiceErrorTimeout},
		{"wrapped deadline", fmt.Errorf("fetch: %w", context.DeadlineExceeded), ServiceErrorTimeout},
		{"net timeout", &url.Error{Op: "Get", URL: "https://x", Err: timeoutNetErr{}}, ServiceErrorTimeout},
		{"status 503", &httpStatusError{URL: "https://x", StatusCode: 503}, ServiceErrorHTTP5xx},
		{"status 429", &httpStatusError{URL: "https://x", StatusCode: 429}, ServiceErrorHTTP429},
		{"status 404", fmt.Errorf("embed: %w", &httpStatusError{URL: "https://x", StatusCode: 404}), ServiceErrorHTTP4xx},
		{"url error", &url.Error{Op: "Get", URL: "https://x", Err: errors.New("boom")}, ServiceErrorTransport},
		{"op error", &net.OpError{Op: "dial", Err: errors.New("refused")}, ServiceErrorTransport},
		{"json syntax", syntaxErr, ServiceErrorParse},
		{"json type", typeErr, ServiceErrorParse},
		{"text", errors.New("no media in the answer"), ServiceErrorWrongContent},
		{"nil", nil, ServiceErrorOther},
	}

	for _, c := range cases {
		if got := classifyServiceErr(c.err); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}

func TestFetchCappedPageStatusErrorKeepsItsText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusGone)
	}))
	t.Cleanup(server.Close)
	pageURL := server.URL + "/post"

	_, err := fetchCappedPage(context.Background(), pageURL, nil, 1024, time.Second)
	var statusErr *httpStatusError

	if (errors.As(err, &statusErr) == false) || (statusErr.StatusCode != http.StatusGone) {
		t.Fatalf("got %v", err)
	}

	if err.Error() != fmt.Sprintf("page %s status %d", pageURL, http.StatusGone) {
		t.Fatalf("the error text changed: %q", err.Error())
	}
}

// assertTracked checks what one call left in the tracker: no entry when
// nothing was recorded, a success, or one failure with wantCode.
func assertTracked(t *testing.T, service string, want serviceOutcome, wantCode string) {
	t.Helper()

	health, isFound := ServiceHealthSnapshot()[service]
	isNone := (want == serviceOutcomeNone) && (isFound == false)
	isSuccess := (want == serviceOutcomeSuccess) && (isFound == true) && (health.ConsecutiveFailures == 0) && (health.LastSuccessAt.IsZero() == false)
	isFailure := (want == serviceOutcomeFailure) && (isFound == true) && (health.ConsecutiveFailures == 1) && (health.LastErrorCode == wantCode)

	if (isNone == false) && (isSuccess == false) && (isFailure == false) {
		t.Fatalf("%s: want outcome %d %q, got entry %v %+v", service, want, wantCode, isFound, health)
	}
}
