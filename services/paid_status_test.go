package services

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// paidTestNow is the fake clock of the paid status tests.
var paidTestNow = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

// resetApifyUsageCache clears the Apify usage cache and the runs in flight
// before and after a test, and fixes the clocks.
func resetApifyUsageCache(t *testing.T) {
	t.Helper()

	reset := func() {
		apifyUsageCache.Lock()
		apifyUsageCache.usd = 0
		apifyUsageCache.readAt = time.Time{}
		apifyUsageCache.errText = ""
		apifyUsageCache.errAt = time.Time{}
		apifyUsageCache.accountLimitUsd = nil
		apifyUsageCache.cycleStart = nil
		apifyUsageCache.cycleEnd = nil
		apifyUsageCache.Unlock()
		apifyInFlight.Lock()
		apifyInFlight.usd = 0
		apifyInFlight.Unlock()
	}

	previousUsageNow := apifyUsageNow
	previousStatusNow := paidStatusNow
	apifyUsageNow = func() time.Time { return paidTestNow }
	paidStatusNow = func() time.Time { return paidTestNow }
	reset()
	t.Cleanup(func() {
		reset()
		apifyUsageNow = previousUsageNow
		paidStatusNow = previousStatusNow
	})
}

// setApifyUsage puts a usage read (and optionally a later failure) into the
// cache.
func setApifyUsage(used float64, readAt time.Time, errAt time.Time) {
	apifyUsageCache.Lock()
	defer apifyUsageCache.Unlock()
	apifyUsageCache.usd = used
	apifyUsageCache.readAt = readAt
	apifyUsageCache.errAt = errAt

	if errAt.IsZero() == false {
		apifyUsageCache.errText = "apify status 502: bad gateway"
	}
}

// usePaidStatusEnv sets the Apify and HikerAPI configuration of a test.
func usePaidStatusEnv(t *testing.T) *fakePaidLedger {
	t.Helper()
	resetApifyUsageCache(t)
	ledger := useHikerTestEnv(t)
	hikerNow = func() time.Time { return paidTestNow }
	t.Setenv("APIFY_TOKEN", "test-token")
	t.Setenv("APIFY_MONTHLY_BUDGET_USD", "10")
	t.Setenv("INSTAGRAM_PAID_PROVIDERS", "apify,hiker")

	return ledger
}

// countingLedger counts the ledger reads and can fail them.
type countingLedger struct {
	mu    sync.Mutex
	reads int
	err   error
	stats PaidMonthStats
}

func (l *countingLedger) Record(context.Context, PaidRequest) error {
	return nil
}

func (l *countingLedger) MonthSpentUsd(context.Context, string, time.Time) (float64, error) {
	return l.stats.CostUsd, l.err
}

func (l *countingLedger) MonthStats(context.Context, string, time.Time) (PaidMonthStats, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.reads++

	return l.stats, l.err
}

func TestApifyOffWithoutToken(t *testing.T) {
	usePaidStatusEnv(t)
	t.Setenv("APIFY_TOKEN", "")
	status := computePaidServicesStatus(context.Background(), &countingLedger{})

	if (status.Apify.State != PaidStateOff) || (status.Apify.Reason != PaidReasonNoToken) {
		t.Fatalf("got %+v", status.Apify)
	}
}

func TestApifyUsageFromAnEndedCycleCountsAsNotRead(t *testing.T) {
	usePaidStatusEnv(t)
	setApifyUsage(10, paidTestNow.Add(-2*time.Hour), time.Time{})
	cycleEnd := paidTestNow.Add(-time.Hour)
	apifyUsageCache.Lock()
	apifyUsageCache.cycleEnd = &cycleEnd
	apifyUsageCache.Unlock()
	status := computePaidServicesStatus(context.Background(), &countingLedger{})

	if (status.Apify.State != PaidStateUnknown) || (status.Apify.Reason != PaidReasonUsageNotRead) || (status.Apify.UsedUsd != nil) {
		t.Fatalf("got %+v", status.Apify)
	}
}

func TestApifyPausedAtZeroBudget(t *testing.T) {
	usePaidStatusEnv(t)
	t.Setenv("APIFY_MONTHLY_BUDGET_USD", "0")
	setApifyUsage(0, paidTestNow, time.Time{})
	status := computePaidServicesStatus(context.Background(), &countingLedger{})

	if (status.Apify.State != PaidStatePaused) || (status.Apify.Reason != PaidReasonBudgetZero) {
		t.Fatalf("got %+v", status.Apify)
	}
}

func TestApifyPausedWhileUsageUnreadable(t *testing.T) {
	usePaidStatusEnv(t)
	setApifyUsage(1, paidTestNow.Add(-time.Hour), paidTestNow)
	status := computePaidServicesStatus(context.Background(), &countingLedger{})

	if (status.Apify.State != PaidStatePaused) || (status.Apify.Reason != PaidReasonUsageUnreadable) || (status.Apify.UsageError == "") {
		t.Fatalf("got %+v", status.Apify)
	}

	setApifyUsage(1, paidTestNow, paidTestNow.Add(-time.Hour))
	status = computePaidServicesStatus(context.Background(), &countingLedger{})

	if status.Apify.State != PaidStateOn {
		t.Fatalf("a success after the failure must be on: %+v", status.Apify)
	}
}

func TestApifyUnknownBeforeFirstRead(t *testing.T) {
	usePaidStatusEnv(t)
	status := computePaidServicesStatus(context.Background(), &countingLedger{})

	if (status.Apify.State != PaidStateUnknown) || (status.Apify.Reason != PaidReasonUsageNotRead) || (status.Apify.UsedUsd != nil) {
		t.Fatalf("got %+v", status.Apify)
	}
}

func TestApifyPausesOnlyFeaturesThatNoLongerFit(t *testing.T) {
	usePaidStatusEnv(t)
	setApifyUsage(9.92, paidTestNow, time.Time{})
	status := computePaidServicesStatus(context.Background(), &countingLedger{})
	want := []string{PaidFeatureStories, PaidFeatureShorts}

	if (status.Apify.State != PaidStateOn) || (slices.Equal(status.Apify.PausedFeatures, want) == false) {
		t.Fatalf("got %+v", status.Apify)
	}

	apifyInFlight.Lock()
	apifyInFlight.usd = 0.05
	apifyInFlight.Unlock()
	status = computePaidServicesStatus(context.Background(), &countingLedger{})

	if (status.Apify.State != PaidStatePaused) || (status.Apify.Reason != PaidReasonBudgetUsed) || (status.Apify.InFlightUsd != 0.05) {
		t.Fatalf("the runs in flight must count: %+v", status.Apify)
	}
}

func TestApifyPausedWhenNoFeatureFits(t *testing.T) {
	usePaidStatusEnv(t)
	setApifyUsage(10, paidTestNow, time.Time{})
	status := computePaidServicesStatus(context.Background(), &countingLedger{})

	if (status.Apify.State != PaidStatePaused) || (status.Apify.Reason != PaidReasonBudgetUsed) || (len(status.Apify.PausedFeatures) != 3) {
		t.Fatalf("got %+v", status.Apify)
	}
}

func TestHikerOffWithoutTokenSkipsTheLedger(t *testing.T) {
	usePaidStatusEnv(t)
	t.Setenv("HIKERAPI_TOKEN", "")
	ledger := &countingLedger{}
	status := computePaidServicesStatus(context.Background(), ledger)

	if (status.Hiker.State != PaidStateOff) || (status.Hiker.Reason != PaidReasonNoToken) || (ledger.reads != 0) {
		t.Fatalf("got %+v after %d reads", status.Hiker, ledger.reads)
	}
}

func TestHikerOffWhenNotListed(t *testing.T) {
	usePaidStatusEnv(t)
	t.Setenv("INSTAGRAM_PAID_PROVIDERS", "apify")
	status := computePaidServicesStatus(context.Background(), &countingLedger{})

	if (status.Hiker.State != PaidStateOff) || (status.Hiker.Reason != PaidReasonNotListed) || (slices.Equal(status.Order, []string{PaidProviderApify}) == false) {
		t.Fatalf("got %+v / %v", status.Hiker, status.Order)
	}
}

func TestHikerPausedWhileExhausted(t *testing.T) {
	usePaidStatusEnv(t)
	newAlertRecorder(t)
	markHikerBalanceExhausted()
	status := computePaidServicesStatus(context.Background(), &countingLedger{})

	if (status.Hiker.State != PaidStatePaused) || (status.Hiker.Reason != PaidReasonBalanceEmpty) || (status.Hiker.Exhausted == false) {
		t.Fatalf("got %+v", status.Hiker)
	}
}

func TestHikerPausedWithoutADatabase(t *testing.T) {
	usePaidStatusEnv(t)
	ledger := &countingLedger{err: errGatedFetchNoDatabase}
	status := computePaidServicesStatus(context.Background(), ledger)

	if (status.Hiker.State != PaidStatePaused) || (status.Hiker.Reason != PaidReasonNoDatabase) || (status.Hiker.SpentUsd != nil) {
		t.Fatalf("got %+v", status.Hiker)
	}
}

func TestHikerPausedWhenNextRequestExceedsBudget(t *testing.T) {
	usePaidStatusEnv(t)
	ledger := &countingLedger{stats: PaidMonthStats{Calls: 1001, ChargedCalls: 1000, GatedCalls: 900, StoryCalls: 101, CostUsd: 1.0}}
	status := computePaidServicesStatus(context.Background(), ledger)

	if (status.Hiker.State != PaidStatePaused) || (status.Hiker.Reason != PaidReasonBudgetUsed) || (*status.Hiker.SpentUsd != 1.0) || (status.Hiker.ChargedCalls != 1000) {
		t.Fatalf("got %+v", status.Hiker)
	}

	ledger.stats.CostUsd = 0.998
	status = computePaidServicesStatus(context.Background(), ledger)

	if status.Hiker.State != PaidStateOn {
		t.Fatalf("one more request fits: %+v", status.Hiker)
	}
}

func TestHikerUnknownOnLedgerError(t *testing.T) {
	usePaidStatusEnv(t)
	ledger := &countingLedger{err: errors.New("connection refused")}
	status := computePaidServicesStatus(context.Background(), ledger)

	if (status.Hiker.State != PaidStateUnknown) || (status.Hiker.Reason != PaidReasonLedgerError) || (status.Hiker.SpentUsd != nil) {
		t.Fatalf("got %+v", status.Hiker)
	}
}

func TestRememberApifyUsageKeepsLastValueOnFailure(t *testing.T) {
	resetApifyUsageCache(t)
	isFailing := false
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		if isFailing == true {
			w.WriteHeader(http.StatusBadGateway)
			fmt.Fprint(w, "bad gateway")
		}

		if isFailing == false {
			fmt.Fprint(w, `{"data":{"current":{"monthlyUsageUsd":4.12},"limits":{"maxMonthlyUsageUsd":8},"monthlyUsageCycle":{"startAt":"2026-10-01T00:00:00.000Z","endAt":"2026-10-31T23:59:59.999Z"}}}`)
		}
	}))
	t.Cleanup(server.Close)
	client := &apifyClient{token: "test-token", apiBase: server.URL, client: server.Client()}

	if _, err := client.monthlyUsage(context.Background()); err != nil {
		t.Fatalf("usage: %v", err)
	}

	mu.Lock()
	isFailing = true
	mu.Unlock()

	if _, err := client.monthlyUsage(context.Background()); err == nil {
		t.Fatalf("the second read must fail")
	}

	status := withApifyUsageCache(ApifyServiceStatus{})
	isCycleRead := (status.CycleStart != nil) && (status.CycleEnd != nil) && (status.CycleStart.Day() == 1) && (status.CycleEnd.Day() == 31)

	if (status.UsedUsd == nil) || (*status.UsedUsd != 4.12) || (status.AccountLimitUsd == nil) || (*status.AccountLimitUsd != 8) || (isCycleRead == false) {
		t.Fatalf("the last good read must stay: %+v", status)
	}

	if (status.UsageErrorAt == nil) || (strings.Contains(status.UsageError, "502") == false) {
		t.Fatalf("the failure must be recorded: %+v", status)
	}
}

// fakeTransport answers every request of http.DefaultTransport and counts
// them.
type fakeTransport struct {
	mu    sync.Mutex
	calls []string
	body  string
}

func (f *fakeTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	f.mu.Lock()
	f.calls = append(f.calls, r.URL.String())
	f.mu.Unlock()
	recorder := httptest.NewRecorder()
	fmt.Fprint(recorder, f.body)

	return recorder.Result(), nil
}

func (f *fakeTransport) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return len(f.calls)
}

// useFakeDefaultTransport replaces http.DefaultTransport for a test.
func useFakeDefaultTransport(t *testing.T, body string) *fakeTransport {
	t.Helper()
	fake := &fakeTransport{body: body}
	previous := http.DefaultTransport
	http.DefaultTransport = fake
	t.Cleanup(func() { http.DefaultTransport = previous })

	return fake
}

func TestHikerBalanceCacheSetByApplyAndGet(t *testing.T) {
	usePaidStatusEnv(t)
	newAlertRecorder(t)
	applyHikerBalance(HikerBalance{Requests: 1520})
	status := computePaidServicesStatus(context.Background(), &countingLedger{})

	if (status.Hiker.BalanceRequests == nil) || (*status.Hiker.BalanceRequests != 1520) || (status.Hiker.BalanceReadAt.Equal(paidTestNow) == false) {
		t.Fatalf("apply: got %+v", status.Hiker)
	}

	useFakeDefaultTransport(t, `{"requests":617,"amount":1.2,"currency":"USD"}`)

	if _, err := GetHikerBalance(); err != nil {
		t.Fatalf("balance: %v", err)
	}

	status = computePaidServicesStatus(context.Background(), &countingLedger{})

	if *status.Hiker.BalanceRequests != 617 {
		t.Fatalf("get: got %+v", status.Hiker)
	}
}

func TestPaidStatusRefresherNeverCallsProviders(t *testing.T) {
	usePaidStatusEnv(t)
	transport := useFakeDefaultTransport(t, `{}`)
	ledger := &countingLedger{}
	previous := paidLedger
	paidLedger = ledger
	t.Cleanup(func() { paidLedger = previous })
	refreshPaidStatus(context.Background())
	snapshot, isStatus := CurrentPaidServicesStatus().(PaidServicesStatus)

	if (isStatus == false) || (snapshot.ComputedAt.Equal(paidTestNow) == false) {
		t.Fatalf("no snapshot stored: %#v", CurrentPaidServicesStatus())
	}

	if (transport.count() != 0) || (ledger.reads != 1) {
		t.Fatalf("got %d provider call(s) and %d ledger read(s)", transport.count(), ledger.reads)
	}
}
