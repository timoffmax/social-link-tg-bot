package services

// Tests for the outage tracker's alerting contract. Every test installs stub
// notification hooks before touching the tracker, so no test can reach Telegram
// or the network even when a token and admin ID are present in the environment.
// The tracker's state is package-global, so these tests must not run in
// parallel with each other.

import (
	"strconv"
	"strings"
	"sync"
	"testing"
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
