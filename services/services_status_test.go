package services

import (
	"bytes"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/timoffmax/social-link-bot/config/overrides"
)

// isolateServicesStatus gives a test a clean tracker, recorded alerts, a pinned
// clock and every switch on, with no paid snapshot and no seeded row.
func isolateServicesStatus(t *testing.T, now *time.Time) *alertRecorder {
	t.Helper()

	recorder := newAlertRecorder(t)
	useServiceClock(t, now)
	resetRelayDisabledPlatforms(t)

	previousOverrides := overrides.Current()
	previousSpecial := serviceSpecialDownloadsOn
	previousPaid := paidStatusSnapshot.Load()
	previousRestored := servicesRestoredFrom.Load()
	overrides.Set(nil)
	serviceSpecialDownloadsOn = func() bool {
		return true
	}
	paidStatusSnapshot.Store(nil)
	servicesRestoredFrom.Store(nil)

	t.Cleanup(func() {
		overrides.Set(previousOverrides)
		serviceSpecialDownloadsOn = previousSpecial
		paidStatusSnapshot.Store(previousPaid)
		servicesRestoredFrom.Store(previousRestored)
	})

	for _, envVar := range []string{"ENABLE_INSTAGRAM", "ENABLE_TIKTOK", "ENABLE_TWITTER", "ENABLE_YOUTUBE", "ENABLE_REDDIT", "ENABLE_FACEBOOK", "ENABLE_9GAG", "ENABLE_THREADS", "ENABLE_PROXIES"} {
		t.Setenv(envVar, "true")
	}

	t.Setenv("APIFY_TOKEN", "apify-token")
	t.Setenv("HIKERAPI_TOKEN", "hiker-token")
	t.Setenv("INSTAGRAM_PAID_PROVIDERS", "apify,hiker")
	t.Setenv("YTDLP_PROXIES", "http://user:pass@127.0.0.1:3128")
	t.Setenv("RELAY_URL", "http://127.0.0.1:8765")
	t.Setenv("RELAY_TOKEN_FILE", "/nonexistent/relay.token")
	t.Setenv("RELAY_PLATFORMS", "youtube,instagram")

	return recorder
}

// currentServices builds the component as the heartbeat does.
func currentServices(t *testing.T) ServicesStatus {
	t.Helper()

	result, isStatus := CurrentServicesStatus().(ServicesStatus)

	if isStatus == false {
		t.Fatalf("CurrentServicesStatus returned %T", CurrentServicesStatus())
	}

	return result
}

// serviceRow returns the row of key.
func serviceRow(t *testing.T, status ServicesStatus, key string) ServiceStatusRow {
	t.Helper()

	result := ServiceStatusRow{}
	isFound := false

	for _, row := range status.Services {
		if row.Key == key {
			result = row
			isFound = true
		}
	}

	if isFound == false {
		t.Fatalf("no row %s", key)
	}

	return result
}

// offReasonOf returns a row's off reason, "" when it is not off.
func offReasonOf(row ServiceStatusRow) string {
	result := ""

	if row.OffReason != nil {
		result = *row.OffReason
	}

	return result
}

func TestSignalStateRules(t *testing.T) {
	now := serviceTestStart
	fresh := now.Add(-time.Minute)
	dayOld := now.Add(-serviceFailureAgeOut)
	pastDay := dayOld.Add(-time.Second)
	cases := []struct {
		name      string
		health    ServiceHealth
		isTraffic bool
		want      string
	}{
		{"no streak is ok", ServiceHealth{LastFailureAt: pastDay, Threshold: 3}, true, ServiceStateOK},
		{"below the threshold is degraded", ServiceHealth{ConsecutiveFailures: 2, LastFailureAt: fresh, Threshold: 3}, true, ServiceStateDegraded},
		{"at the threshold is down", ServiceHealth{ConsecutiveFailures: 3, LastFailureAt: fresh, Threshold: 3}, true, ServiceStateDown},
		{"proxy threshold of one is down at once", ServiceHealth{ConsecutiveFailures: 1, LastFailureAt: fresh, Threshold: 1}, false, ServiceStateDown},
		{"missing threshold uses the default", ServiceHealth{ConsecutiveFailures: 2, LastFailureAt: fresh}, false, ServiceStateDegraded},
		{"traffic failure exactly a day old still counts", ServiceHealth{ConsecutiveFailures: 3, LastFailureAt: dayOld, Threshold: 3}, true, ServiceStateDown},
		{"traffic failure older than a day is stale", ServiceHealth{ConsecutiveFailures: 3, LastFailureAt: pastDay, Threshold: 3}, true, ServiceStateStale},
		{"degraded traffic ages out too", ServiceHealth{ConsecutiveFailures: 1, LastFailureAt: pastDay, Threshold: 3}, true, ServiceStateStale},
		{"a probe never ages out", ServiceHealth{ConsecutiveFailures: 3, LastFailureAt: pastDay, Threshold: 3}, false, ServiceStateDown},
		{"an old degraded probe stays degraded", ServiceHealth{ConsecutiveFailures: 1, LastFailureAt: pastDay, Threshold: 3}, false, ServiceStateDegraded},
	}

	for _, c := range cases {
		if got := signalState(c.health, c.isTraffic, now); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}

func TestRowStateRules(t *testing.T) {
	signal := func(state string) *ServiceSignal {
		return &ServiceSignal{State: state}
	}
	reason := serviceOffBudgetPaused
	cases := []struct {
		name string
		row  ServiceStatusRow
		want string
	}{
		{"off wins over a down signal", ServiceStatusRow{OffReason: &reason, Traffic: signal(ServiceStateDown)}, ServiceStateOff},
		{"down probe beats ok traffic", ServiceStatusRow{Traffic: signal(ServiceStateOK), Probe: signal(ServiceStateDown)}, ServiceStateDown},
		{"down traffic beats degraded probe", ServiceStatusRow{Traffic: signal(ServiceStateDown), Probe: signal(ServiceStateDegraded)}, ServiceStateDown},
		{"check OK, traffic failing", ServiceStatusRow{Traffic: signal(ServiceStateDegraded), Probe: signal(ServiceStateOK)}, ServiceStateDegraded},
		{"ok traffic alone", ServiceStatusRow{Traffic: signal(ServiceStateOK)}, ServiceStateOK},
		{"fresh probe ok over aged-out traffic", ServiceStatusRow{Traffic: signal(ServiceStateStale), Probe: signal(ServiceStateOK)}, ServiceStateOK},
		{"stale traffic alone", ServiceStatusRow{Traffic: signal(ServiceStateStale), IsFallback: true}, ServiceStateStale},
		{"fallback without signals is idle", ServiceStatusRow{IsFallback: true}, ServiceStateIdle},
		{"first step without signals is unknown", ServiceStatusRow{}, ServiceStateUnknown},
	}

	for _, c := range cases {
		if got := serviceState(c.row); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}

func TestProbeAndTrafficArePublishedApart(t *testing.T) {
	now := serviceTestStart
	isolateServicesStatus(t, &now)

	ReportServiceSuccess(kkInstagramProbeKey)
	ReportServiceFailure(kkInstagramServiceName, "kkinstagram returned non-image content-type \"text/html\"")
	row := serviceRow(t, currentServices(t), kkInstagramServiceName)

	if (row.State != ServiceStateDegraded) || (row.Probe.State != ServiceStateOK) || (row.Traffic.State != ServiceStateDegraded) {
		t.Fatalf("got row %s, probe %+v, traffic %+v", row.State, row.Probe, row.Traffic)
	}

	if (row.Traffic.LastErrorCode == nil) || (*row.Traffic.LastErrorCode != ServiceErrorWrongContent) || (row.Probe.LastErrorCode != nil) {
		t.Fatalf("codes: traffic %+v, probe %+v", row.Traffic, row.Probe)
	}

	if (row.Traffic.FailingSince == nil) || (row.Traffic.FailingSince.Equal(now) == false) || (row.Probe.FailingSince != nil) {
		t.Fatalf("failing since: traffic %+v, probe %+v", row.Traffic, row.Probe)
	}
}

func TestTrafficFailureAgesOutButTheProbeDoesNot(t *testing.T) {
	now := serviceTestStart
	isolateServicesStatus(t, &now)

	for i := 0; i < outageAlertThreshold; i++ {
		ReportServiceFailure(tikwmServiceName, "tikwm API HTTP 503")
		ReportServiceFailure(vxredditProbeKey, "probe HTTP 503")
	}

	now = now.Add(serviceFailureAgeOut)
	status := currentServices(t)

	if row := serviceRow(t, status, tikwmServiceName); row.State != ServiceStateDown {
		t.Fatalf("a day-old failure still counts: %s", row.State)
	}

	now = now.Add(time.Second)
	status = currentServices(t)
	tikwm := serviceRow(t, status, tikwmServiceName)
	vxreddit := serviceRow(t, status, redditVxredditServiceName)

	if (tikwm.State != ServiceStateStale) || (tikwm.Traffic.State != ServiceStateStale) || (tikwm.Traffic.ConsecutiveFailures != outageAlertThreshold) {
		t.Fatalf("an older traffic failure is stale: %+v %+v", tikwm, tikwm.Traffic)
	}

	if (vxreddit.State != ServiceStateDown) || (vxreddit.Probe.State != ServiceStateDown) || (vxreddit.Traffic != nil) {
		t.Fatalf("a probe is never stale: %+v %+v", vxreddit, vxreddit.Probe)
	}
}

func TestRowsWithoutEventsAreIdleOrUnknown(t *testing.T) {
	now := serviceTestStart
	isolateServicesStatus(t, &now)
	status := currentServices(t)

	if row := serviceRow(t, status, tikwmServiceName); (row.State != ServiceStateIdle) || (row.Traffic != nil) || (row.Probe != nil) {
		t.Fatalf("a fallback without events is idle: %+v", row)
	}

	if row := serviceRow(t, status, fxTwitterServiceName); row.State != ServiceStateUnknown {
		t.Fatalf("a first step without events is unknown: %+v", row)
	}
}

func TestProxyPoolIsReadAsTheProbe(t *testing.T) {
	now := serviceTestStart
	isolateServicesStatus(t, &now)

	ReportServiceFailureWithThreshold(proxyPoolServiceName, "all 2 configured proxies are failing (last error: proxyconnect tcp: 407 Proxy Authentication Required)", 1)
	row := serviceRow(t, currentServices(t), proxyPoolServiceName)

	if (row.HasTraffic == true) || (row.Traffic != nil) || (row.Probe == nil) {
		t.Fatalf("proxy-pool has only a probe: %+v", row)
	}

	if (row.State != ServiceStateDown) || (row.Probe.Threshold != 1) || (*row.Probe.LastErrorCode != ServiceErrorHTTP4xx) {
		t.Fatalf("got %+v %+v", row, row.Probe)
	}
}

func TestOffRowStillPublishesItsSignals(t *testing.T) {
	now := serviceTestStart
	isolateServicesStatus(t, &now)
	t.Setenv("ENABLE_INSTAGRAM", "false")

	ReportServiceFailure(kkInstagramServiceName, "HTTP 503")
	row := serviceRow(t, currentServices(t), kkInstagramServiceName)

	if (row.State != ServiceStateOff) || (offReasonOf(row) != serviceOffPlatform) || (row.Traffic == nil) || (row.Traffic.State != ServiceStateDegraded) {
		t.Fatalf("got %+v", row)
	}
}

func TestOffReasonsAndTheirPrecedence(t *testing.T) {
	pausedApify := func() {
		paidStatusSnapshot.Store(&PaidServicesStatus{Apify: ApifyServiceStatus{State: PaidStatePaused}})
	}
	specialOff := func() {
		serviceSpecialDownloadsOn = func() bool {
			return false
		}
	}
	cases := []struct {
		name  string
		setup func(t *testing.T)
		want  map[string]string
	}{
		{
			name: "everything on",
			setup: func(t *testing.T) {
				paidStatusSnapshot.Store(&PaidServicesStatus{Apify: ApifyServiceStatus{State: PaidStateOn}, Hiker: HikerServiceStatus{State: PaidStateOn}})
			},
			want: map[string]string{},
		},
		{
			name: "platform off wins over a missing token",
			setup: func(t *testing.T) {
				t.Setenv("ENABLE_INSTAGRAM", "false")
				t.Setenv("APIFY_TOKEN", "")
			},
			want: map[string]string{kkInstagramServiceName: serviceOffPlatform, apifyServiceName: serviceOffPlatform, storiesApifyServiceName: serviceOffPlatform, hikerServiceName: serviceOffPlatform, relayInstagramKey: serviceOffPlatform, youtubeApifyServiceName: serviceOffNotConfigured},
		},
		{
			name: "each platform switch",
			setup: func(t *testing.T) {
				t.Setenv("ENABLE_TIKTOK", "false")
				t.Setenv("ENABLE_TWITTER", "false")
				t.Setenv("ENABLE_YOUTUBE", "false")
				t.Setenv("ENABLE_REDDIT", "false")
				t.Setenv("ENABLE_FACEBOOK", "false")
				t.Setenv("ENABLE_9GAG", "false")
				t.Setenv("ENABLE_THREADS", "false")
			},
			want: map[string]string{kkTikTokServiceName: serviceOffPlatform, tikwmServiceName: serviceOffPlatform, fxTwitterServiceName: serviceOffPlatform, relayYouTubeKey: serviceOffPlatform, youtubeApifyServiceName: serviceOffPlatform, youtubePostServiceName: serviceOffPlatform, redditVxredditServiceName: serviceOffPlatform, redditProxyServiceName: serviceOffPlatform, facebedServiceName: serviceOffPlatform, ninegagAPIServiceName: serviceOffPlatform, threadsEmbedServiceName: serviceOffPlatform, vxthreadsServiceName: serviceOffPlatform},
		},
		{
			name: "no Apify token wins over special downloads off",
			setup: func(t *testing.T) {
				t.Setenv("APIFY_TOKEN", "")
				specialOff()
			},
			want: map[string]string{apifyServiceName: serviceOffNotConfigured, storiesApifyServiceName: serviceOffNotConfigured, youtubeApifyServiceName: serviceOffNotConfigured, hikerServiceName: serviceOffSpecial, relayInstagramKey: serviceOffSpecial, relayYouTubeKey: serviceOffSpecial},
		},
		{
			name: "no HikerAPI token",
			setup: func(t *testing.T) {
				t.Setenv("HIKERAPI_TOKEN", "")
			},
			want: map[string]string{hikerServiceName: serviceOffNotConfigured},
		},
		{
			name: "not listed wins over budget paused; Shorts ignore the list",
			setup: func(t *testing.T) {
				t.Setenv("INSTAGRAM_PAID_PROVIDERS", "hiker")
				pausedApify()
			},
			want: map[string]string{apifyServiceName: serviceOffNotListed, storiesApifyServiceName: serviceOffNotListed, youtubeApifyServiceName: serviceOffBudgetPaused},
		},
		{
			name: "proxies switched off",
			setup: func(t *testing.T) {
				t.Setenv("ENABLE_PROXIES", "false")
			},
			want: map[string]string{redditProxyServiceName: serviceOffProxies, proxyPoolServiceName: serviceOffProxies},
		},
		{
			name: "no proxies configured",
			setup: func(t *testing.T) {
				t.Setenv("YTDLP_PROXIES", "")
			},
			want: map[string]string{redditProxyServiceName: serviceOffProxies, proxyPoolServiceName: serviceOffProxies},
		},
		{
			name: "special downloads off wins over relay disabled and budget paused",
			setup: func(t *testing.T) {
				specialOff()
				pausedApify()
				setRelayPlatformDisabled(relayPlatformYouTube, true)
			},
			want: map[string]string{apifyServiceName: serviceOffSpecial, storiesApifyServiceName: serviceOffSpecial, hikerServiceName: serviceOffSpecial, relayInstagramKey: serviceOffSpecial, relayYouTubeKey: serviceOffSpecial, youtubeApifyServiceName: serviceOffSpecial},
		},
		{
			name: "relay not serving a platform",
			setup: func(t *testing.T) {
				t.Setenv("RELAY_PLATFORMS", "youtube")
				setRelayPlatformDisabled(relayPlatformInstagram, true)
			},
			want: map[string]string{relayInstagramKey: serviceOffNotConfigured},
		},
		{
			name: "relay not configured at all",
			setup: func(t *testing.T) {
				t.Setenv("RELAY_URL", "")
			},
			want: map[string]string{relayInstagramKey: serviceOffNotConfigured, relayYouTubeKey: serviceOffNotConfigured},
		},
		{
			name: "relay disabled on the relay side",
			setup: func(t *testing.T) {
				setRelayPlatformDisabled(relayPlatformYouTube, true)
			},
			want: map[string]string{relayYouTubeKey: serviceOffRelayDisabled},
		},
		{
			name:  "Apify paused as a whole",
			setup: func(t *testing.T) { pausedApify() },
			want:  map[string]string{apifyServiceName: serviceOffBudgetPaused, storiesApifyServiceName: serviceOffBudgetPaused, youtubeApifyServiceName: serviceOffBudgetPaused},
		},
		{
			name: "one Apify feature paused",
			setup: func(t *testing.T) {
				paidStatusSnapshot.Store(&PaidServicesStatus{Apify: ApifyServiceStatus{State: PaidStateOn, PausedFeatures: []string{PaidFeatureShorts}}})
			},
			want: map[string]string{youtubeApifyServiceName: serviceOffBudgetPaused},
		},
		{
			name: "HikerAPI paused",
			setup: func(t *testing.T) {
				paidStatusSnapshot.Store(&PaidServicesStatus{Hiker: HikerServiceStatus{State: PaidStatePaused}})
			},
			want: map[string]string{hikerServiceName: serviceOffBudgetPaused},
		},
		{
			name:  "no paid snapshot yet is not paused",
			setup: func(t *testing.T) {},
			want:  map[string]string{},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			now := serviceTestStart
			isolateServicesStatus(t, &now)
			c.setup(t)

			for _, row := range currentServices(t).Services {
				want := c.want[row.Key]
				got := offReasonOf(row)
				isOff := (row.State == ServiceStateOff)

				if (got != want) || (isOff != (want != "")) {
					t.Errorf("%s: off reason %q (state %s), want %q", row.Key, got, row.State, want)
				}
			}
		})
	}
}

func TestServicesStatusJSONShape(t *testing.T) {
	now := serviceTestStart
	isolateServicesStatus(t, &now)
	ReportServiceSuccess(kkInstagramServiceName)

	encoded, err := json.Marshal(CurrentServicesStatus())

	if err != nil {
		t.Fatal(err)
	}

	decoded := map[string]any{}
	_ = json.Unmarshal(encoded, &decoded)
	assertJSONKeys(t, "top level", decoded, []string{"version", "started_at", "computed_at", "restored_from", "services"})

	if (decoded["version"] != float64(1)) || (decoded["restored_from"] != nil) || (decoded["computed_at"] != "2026-10-20T09:00:00Z") {
		t.Fatalf("got %s", encoded)
	}

	rows, _ := decoded["services"].([]any)

	if len(rows) != len(serviceCatalog) {
		t.Fatalf("one row per catalog entry: %d", len(rows))
	}

	rowKeys := []string{"key", "label", "kind", "host", "platforms", "role", "fallback", "has_traffic", "has_probe", "state", "off_reason", "traffic", "probe"}
	signalKeys := []string{"state", "last_success_at", "last_failure_at", "failing_since", "consecutive_failures", "threshold", "last_error_code"}

	for i, raw := range rows {
		row, _ := raw.(map[string]any)
		assertJSONKeys(t, "row", row, rowKeys)

		if row["key"] != serviceCatalog[i].Key {
			t.Fatalf("row %d is %v, want catalog order %s", i, row["key"], serviceCatalog[i].Key)
		}
	}

	first, _ := rows[0].(map[string]any)
	traffic, _ := first["traffic"].(map[string]any)
	assertJSONKeys(t, "signal", traffic, signalKeys)

	if (first["host"] != "kkinstagram.com") || (first["probe"] != nil) || (traffic["last_error_code"] != nil) || (traffic["threshold"] != float64(3)) {
		t.Fatalf("got %s", encoded)
	}

	tikwm, _ := rows[6].(map[string]any)

	if (tikwm["host"] != nil) || (tikwm["traffic"] != nil) || (tikwm["off_reason"] != nil) {
		t.Fatalf("null fields must be present as null: %v", tikwm)
	}
}

// assertJSONKeys checks that object has exactly keys.
func assertJSONKeys(t *testing.T, name string, object map[string]any, keys []string) {
	t.Helper()

	got := []string{}

	for key := range object {
		got = append(got, key)
	}

	want := append([]string{}, keys...)
	sort.Strings(got)
	sort.Strings(want)

	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("%s keys: got %v, want %v", name, got, want)
	}
}

func TestServicesStatusIsAnEmptyArrayNeverNull(t *testing.T) {
	previous := serviceCatalog
	serviceCatalog = nil
	t.Cleanup(func() {
		serviceCatalog = previous
	})

	encoded, _ := json.Marshal(CurrentServicesStatus())

	if strings.Contains(string(encoded), `"services":[]`) == false {
		t.Fatalf("got %s", encoded)
	}
}

func TestServiceCatalogGuards(t *testing.T) {
	keys := map[string]bool{}
	trackerKeys := map[string]bool{}

	for _, entry := range serviceCatalog {
		if keys[entry.Key] == true {
			t.Fatalf("duplicate catalog key %s", entry.Key)
		}

		keys[entry.Key] = true
		trackerKeys[entry.TrafficKey] = true
		trackerKeys[entry.ProbeKey] = true

		if (entry.TrafficKey == "") && (entry.ProbeKey == "") {
			t.Fatalf("%s has no signal", entry.Key)
		}

		for _, reason := range entry.offRules {
			if slices.Contains(serviceOffReasonOrder, reason) == false {
				t.Fatalf("%s: unknown off rule %s", entry.Key, reason)
			}
		}
	}

	tracked := []string{
		kkInstagramServiceName, kkInstagramServiceName + serviceProbeSuffix,
		kkTikTokServiceName, kkTikTokServiceName + serviceProbeSuffix,
		redditVxredditServiceName, redditVxredditServiceName + serviceProbeSuffix,
		facebedServiceName, facebedServiceName + serviceProbeSuffix,
		apifyServiceName, storiesApifyServiceName, youtubeApifyServiceName, hikerServiceName,
		youtubePostServiceName, redditProxyServiceName, proxyPoolServiceName,
		tikwmServiceName, fxTwitterServiceName, threadsEmbedServiceName, vxthreadsServiceName, ninegagAPIServiceName,
	}

	for platform := range relaySupportedPlatforms {
		tracked = append(tracked, relayServiceName(platform), relayServiceName(platform)+relayProbeSuffix)
	}

	for _, key := range tracked {
		if trackerKeys[key] == false {
			t.Errorf("tracker key %s has no catalog row", key)
		}
	}
}

func TestCurrentServicesStatusDoesNoIO(t *testing.T) {
	now := serviceTestStart
	isolateServicesStatus(t, &now)
	serviceSpecialDownloadsOn = IsSpecialDownloadsOn
	previousShared := SharedHTTPClient
	previousDownload := SharedDownloadClient
	failing := &http.Client{Transport: failingTransport{t: t}}
	SharedHTTPClient = failing
	SharedDownloadClient = failing
	t.Cleanup(func() {
		SharedHTTPClient = previousShared
		SharedDownloadClient = previousDownload
	})

	startedAt := time.Now()
	status := currentServices(t)

	if (time.Since(startedAt) > time.Second) || (len(status.Services) != len(serviceCatalog)) {
		t.Fatalf("building the status must be instant and complete")
	}
}

// failingTransport fails the test on any request.
type failingTransport struct {
	t *testing.T
}

func (f failingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	f.t.Errorf("unexpected request to %s", req.URL)

	return nil, errors.New("no network in this test")
}

// publishedServices marshals the current component.
func publishedServices(t *testing.T) json.RawMessage {
	t.Helper()

	result, err := json.Marshal(CurrentServicesStatus())

	if err != nil {
		t.Fatal(err)
	}

	return result
}

// signalsJSON returns the traffic and probe of every row as JSON.
func signalsJSON(t *testing.T, status ServicesStatus) string {
	t.Helper()

	signals := []any{}

	for _, row := range status.Services {
		signals = append(signals, row.Traffic, row.Probe)
	}

	result, _ := json.Marshal(signals)

	return string(result)
}

func TestSeedingRestoresThePublishedSignals(t *testing.T) {
	now := serviceTestStart
	recorder := isolateServicesStatus(t, &now)

	ReportServiceSuccess(kkInstagramProbeKey)
	ReportServiceFailure(kkInstagramServiceName, "HTTP 503")
	now = now.Add(time.Minute)

	for i := 0; i < outageAlertThreshold; i++ {
		ReportServiceFailureWithThreshold(proxyPoolServiceName, "407 Proxy Authentication Required", 1)
	}

	ReportServiceFailureCode(tikwmServiceName, ServiceErrorHTTP429, "limit")
	ReportServiceSuccess(tikwmServiceName)
	before := currentServices(t)
	data := publishedServices(t)
	clearServiceHealthState()
	alertsBefore := len(recorder.recordedAlerts())
	now = now.Add(time.Hour)

	if err := SeedServicesHealth(data); err != nil {
		t.Fatal(err)
	}

	after := currentServices(t)

	if signalsJSON(t, after) != signalsJSON(t, before) {
		t.Fatalf("seeding changed the signals:\n%s\n%s", signalsJSON(t, before), signalsJSON(t, after))
	}

	if (after.RestoredFrom == nil) || (after.RestoredFrom.Equal(before.ComputedAt) == false) {
		t.Fatalf("restored_from must be the seeded row's computed_at: %v", after.RestoredFrom)
	}

	if (len(recorder.recordedAlerts()) != alertsBefore) || (len(recorder.recordedResets()) != 0) {
		t.Fatalf("seeding must not notify: %+v %v", recorder.recordedAlerts(), recorder.recordedResets())
	}
}

// seedRow returns a published row of key with the given traffic.
func seedRow(key string, traffic *ServiceSignal, probe *ServiceSignal) json.RawMessage {
	status := ServicesStatus{
		Version:    servicesStatusVersion,
		ComputedAt: serviceTestStart.Add(-time.Minute),
		Services:   []ServiceStatusRow{{Key: key, Traffic: traffic, Probe: probe}},
	}
	result, _ := json.Marshal(status)

	return result
}

// failingSignal is a published failing signal with failures at threshold.
func failingSignal(failures int, threshold int) *ServiceSignal {
	failedAt := serviceTestStart.Add(-2 * time.Minute)
	code := ServiceErrorHTTP5xx
	result := &ServiceSignal{
		State:               ServiceStateDown,
		LastFailureAt:       &failedAt,
		FailingSince:        &failedAt,
		ConsecutiveFailures: failures,
		Threshold:           threshold,
		LastErrorCode:       &code,
	}

	return result
}

func TestSeededOutageRecoversOnceAndDoesNotAlertOnItsOwn(t *testing.T) {
	now := serviceTestStart
	recorder := isolateServicesStatus(t, &now)
	data := seedRow(kkInstagramServiceName, failingSignal(3, 3), nil)

	if err := SeedServicesHealth(data); err != nil {
		t.Fatal(err)
	}

	if (len(recorder.recordedAlerts()) != 0) || (len(recorder.recordedResets()) != 0) {
		t.Fatalf("seeding must not notify: %+v", recorder.recordedAlerts())
	}

	ReportServiceSuccess(kkInstagramServiceName)
	ReportServiceSuccess(kkInstagramServiceName)

	if (len(recorder.alertsWithKey("recovery:kkinstagram")) != 1) || (len(recorder.recordedResets()) != 1) {
		t.Fatalf("a seeded outage recovers exactly once: %+v %v", recorder.recordedAlerts(), recorder.recordedResets())
	}
}

func TestSeededAlertedStreakNeedsThresholdNewFailuresToAlert(t *testing.T) {
	now := serviceTestStart
	recorder := isolateServicesStatus(t, &now)
	data := seedRow(tikwmServiceName, failingSignal(3, 3), nil)

	if err := SeedServicesHealth(data); err != nil {
		t.Fatal(err)
	}

	ReportServiceFailure(tikwmServiceName, "tikwm API HTTP 502")
	ReportServiceFailure(tikwmServiceName, "tikwm API HTTP 502")

	if alerts := recorder.alertsWithKey("outage:tikwm"); len(alerts) != 0 {
		t.Fatalf("failures right after a restart must not re-alert: %+v", alerts)
	}

	ReportServiceFailure(tikwmServiceName, "tikwm API HTTP 502")
	alerts := recorder.alertsWithKey("outage:tikwm")

	if (len(alerts) != 1) || (strings.Contains(alerts[0].text, "6 consecutive") == false) {
		t.Fatalf("the third new failure alerts with the whole streak: %+v", alerts)
	}

	if failures := ServiceHealthSnapshot()[tikwmServiceName].ConsecutiveFailures; failures != 6 {
		t.Fatalf("the seeded streak stays shown: %d", failures)
	}
}

func TestSeededAlertedStreakStillRecoversAfterNewFailures(t *testing.T) {
	now := serviceTestStart
	recorder := isolateServicesStatus(t, &now)
	data := seedRow(tikwmServiceName, failingSignal(3, 3), nil)

	if err := SeedServicesHealth(data); err != nil {
		t.Fatal(err)
	}

	ReportServiceFailure(tikwmServiceName, "tikwm API HTTP 502")
	ReportServiceSuccess(tikwmServiceName)

	if alerts := recorder.alertsWithKey("outage:tikwm"); len(alerts) != 0 {
		t.Fatalf("got %+v", alerts)
	}

	if recoveries := recorder.alertsWithKey("recovery:tikwm"); len(recoveries) != 1 {
		t.Fatalf("the seeded alerted outage recovers once: %+v", recoveries)
	}
}

func TestSeededStreakBelowThresholdNeedsThresholdNewFailures(t *testing.T) {
	now := serviceTestStart
	recorder := isolateServicesStatus(t, &now)
	data := seedRow(tikwmServiceName, failingSignal(2, 3), nil)

	if err := SeedServicesHealth(data); err != nil {
		t.Fatal(err)
	}

	ReportServiceFailure(tikwmServiceName, "tikwm API HTTP 502")
	ReportServiceFailure(tikwmServiceName, "tikwm API HTTP 502")

	if alerts := recorder.alertsWithKey("outage:tikwm"); len(alerts) != 0 {
		t.Fatalf("got %+v", alerts)
	}

	ReportServiceFailure(tikwmServiceName, "tikwm API HTTP 502")

	if alerts := recorder.alertsWithKey("outage:tikwm"); len(alerts) != 1 {
		t.Fatalf("the third new failure alerts: %+v", alerts)
	}

	if recoveries := recorder.alertsWithKey("recovery:tikwm"); len(recoveries) != 0 {
		t.Fatalf("got %+v", recoveries)
	}
}

func TestStreakAfterASeededRecoveryAlertsAtThreshold(t *testing.T) {
	now := serviceTestStart
	recorder := isolateServicesStatus(t, &now)
	data := seedRow(tikwmServiceName, failingSignal(2, 3), nil)

	if err := SeedServicesHealth(data); err != nil {
		t.Fatal(err)
	}

	ReportServiceSuccess(tikwmServiceName)

	for i := 0; i < outageAlertThreshold; i++ {
		ReportServiceFailure(tikwmServiceName, "tikwm API HTTP 502")
	}

	if alerts := recorder.alertsWithKey("outage:tikwm"); len(alerts) != 1 {
		t.Fatalf("a streak after a success alerts at the threshold: %+v", alerts)
	}
}

func TestSeedingSanitisesThePublishedValues(t *testing.T) {
	now := serviceTestStart
	isolateServicesStatus(t, &now)
	future := now.Add(time.Hour)
	past := now.Add(-time.Hour)
	unknownCode := "teapot"
	data := seedRow(kkInstagramServiceName, &ServiceSignal{
		LastSuccessAt:       &past,
		LastFailureAt:       &future,
		FailingSince:        &past,
		ConsecutiveFailures: -4,
		Threshold:           0,
		LastErrorCode:       &unknownCode,
	}, &ServiceSignal{ConsecutiveFailures: 2})

	if err := SeedServicesHealth(data); err != nil {
		t.Fatal(err)
	}

	snapshot := ServiceHealthSnapshot()
	health := snapshot[kkInstagramServiceName]

	if (health.ConsecutiveFailures != 0) || (health.Threshold != outageAlertThreshold) || (health.LastErrorCode != ServiceErrorOther) {
		t.Fatalf("got %+v", health)
	}

	if (health.LastFailureAt.IsZero() == false) || (health.FailingSince.IsZero() == false) || (health.LastSuccessAt.Equal(past) == false) {
		t.Fatalf("future and streakless times must be dropped: %+v", health)
	}

	if _, isSeeded := snapshot[kkInstagramProbeKey]; isSeeded == true {
		t.Fatalf("a signal without any time must be skipped")
	}
}

func TestSeedingRestartsAStreakWhoseFailureTimeWasDropped(t *testing.T) {
	now := serviceTestStart
	recorder := isolateServicesStatus(t, &now)
	future := now.Add(time.Hour)
	past := now.Add(-time.Hour)
	traffic := failingSignal(3, 3)
	traffic.LastSuccessAt = &past
	traffic.LastFailureAt = &future
	traffic.FailingSince = &future
	data := seedRow(kkInstagramServiceName, traffic, nil)

	if err := SeedServicesHealth(data); err != nil {
		t.Fatal(err)
	}

	health := serviceHealthOf(t, kkInstagramServiceName)

	if (health.ConsecutiveFailures != 0) || (health.FailingSince.IsZero() == false) {
		t.Fatalf("got %+v", health)
	}

	row := buildServicesStatus().Services[0]

	if (row.Traffic == nil) || (row.Traffic.State != ServiceStateOK) {
		t.Fatalf("got %+v", row.Traffic)
	}

	ReportServiceSuccess(kkInstagramServiceName)

	if recoveries := recorder.alertsWithKey("recovery:kkinstagram"); len(recoveries) != 0 {
		t.Fatalf("a restarted streak is not an alerted outage: %+v", recoveries)
	}
}

func TestSeedingSkipsASignalWithOnlyAStreaklessFailingSince(t *testing.T) {
	now := serviceTestStart
	isolateServicesStatus(t, &now)
	past := now.Add(-time.Hour)
	data := seedRow(kkInstagramServiceName, &ServiceSignal{FailingSince: &past}, nil)

	if err := SeedServicesHealth(data); err != nil {
		t.Fatal(err)
	}

	if _, isSeeded := ServiceHealthSnapshot()[kkInstagramServiceName]; isSeeded == true {
		t.Fatalf("a signal left without any time must be skipped")
	}
}

func TestSeedingNeverOverwritesAnEntryAndIgnoresUnknownKeys(t *testing.T) {
	now := serviceTestStart
	isolateServicesStatus(t, &now)
	ReportServiceSuccess(kkInstagramServiceName)
	status := ServicesStatus{
		Version:    servicesStatusVersion,
		ComputedAt: now,
		Services: []ServiceStatusRow{
			{Key: kkInstagramServiceName, Traffic: failingSignal(5, 3)},
			{Key: "retired-service", Traffic: failingSignal(5, 3)},
		},
	}
	data, _ := json.Marshal(status)

	if err := SeedServicesHealth(data); err != nil {
		t.Fatal(err)
	}

	snapshot := ServiceHealthSnapshot()

	if (snapshot[kkInstagramServiceName].ConsecutiveFailures != 0) || (len(snapshot) != 1) {
		t.Fatalf("got %+v", snapshot)
	}
}

func TestSeedingRejectsAnotherVersionOrMalformedData(t *testing.T) {
	now := serviceTestStart
	isolateServicesStatus(t, &now)
	other := ServicesStatus{Version: 2, ComputedAt: now, Services: []ServiceStatusRow{{Key: tikwmServiceName, Traffic: failingSignal(1, 3)}}}
	otherData, _ := json.Marshal(other)

	for _, data := range []json.RawMessage{otherData, json.RawMessage(`{"version":`), json.RawMessage(`[]`)} {
		if err := SeedServicesHealth(data); err == nil {
			t.Fatalf("%s must be rejected", data)
		}
	}

	if (len(ServiceHealthSnapshot()) != 0) || (servicesRestoredFrom.Load() != nil) {
		t.Fatalf("a rejected row must change nothing")
	}
}

func TestBuildDoesNotLogUnknownPaidProviders(t *testing.T) {
	now := serviceTestStart
	isolateServicesStatus(t, &now)
	t.Setenv("INSTAGRAM_PAID_PROVIDERS", "apify,hikr")
	var logged bytes.Buffer
	previousWriter := log.Writer()
	log.SetOutput(&logged)
	t.Cleanup(func() {
		log.SetOutput(previousWriter)
	})

	status := buildServicesStatus()

	if count := strings.Count(logged.String(), "unknown provider"); count != 0 {
		t.Fatalf("logged the unknown provider %d times", count)
	}

	if reason := offReasonOf(serviceRow(t, status, hikerServiceName)); reason != serviceOffNotListed {
		t.Fatalf("got %q", reason)
	}
}

func TestPaidProviderOrderStillLogsUnknownProviders(t *testing.T) {
	t.Setenv("INSTAGRAM_PAID_PROVIDERS", "apify,hikr")
	var logged bytes.Buffer
	previousWriter := log.Writer()
	log.SetOutput(&logged)
	t.Cleanup(func() {
		log.SetOutput(previousWriter)
	})

	order := instagramPaidProviderOrder()
	quietOrder := quietInstagramPaidProviderOrder()

	if count := strings.Count(logged.String(), "unknown provider"); count != 1 {
		t.Fatalf("logged the unknown provider %d times", count)
	}

	if (slices.Equal(order, []string{PaidProviderApify}) == false) || (slices.Equal(quietOrder, order) == false) {
		t.Fatalf("got %v and %v", order, quietOrder)
	}
}
