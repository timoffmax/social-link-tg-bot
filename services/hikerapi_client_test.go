package services

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/timoffmax/social-link-bot/config"
)

const hikerTestKey = "hiker-test-key"

// fakePaidLedger is an in-memory paidRequestLedger.
type fakePaidLedger struct {
	mu          sync.Mutex
	spent       float64
	rows        []PaidRequest
	readErr     error
	writeErr    error
	readDelay   time.Duration
	onRead      func()
	reading     int
	maxReading  int
	readsServed int
}

func (l *fakePaidLedger) Record(_ context.Context, request PaidRequest) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.writeErr == nil {
		l.rows = append(l.rows, request)
		l.spent += request.CostUsd
	}

	return l.writeErr
}

func (l *fakePaidLedger) MonthSpentUsd(context.Context, string, time.Time) (float64, error) {
	l.mu.Lock()
	l.reading++
	l.maxReading = max(l.maxReading, l.reading)
	delay := l.readDelay
	l.mu.Unlock()

	time.Sleep(delay)

	l.mu.Lock()
	defer l.mu.Unlock()

	l.reading--
	l.readsServed++
	spent := l.spent
	onRead := l.onRead
	l.onRead = nil

	// onRead runs once, after the spend is taken: a request that settles
	// while the read is on its way.
	if onRead != nil {
		l.mu.Unlock()
		onRead()
		l.mu.Lock()
	}

	return spent, l.readErr
}

func (l *fakePaidLedger) recorded() []PaidRequest {
	l.mu.Lock()
	defer l.mu.Unlock()

	result := make([]PaidRequest, len(l.rows))
	copy(result, l.rows)

	return result
}

// resetHikerState clears the package-level HikerAPI budget and balance state.
func resetHikerState() {
	hikerNow = time.Now
	hikerBudget.Lock()
	hikerBudget.inFlightUsd = 0
	hikerBudget.unrecordedUsd = 0
	hikerBudget.unrecordedMonth = time.Time{}
	hikerBudget.settledUsd = 0
	hikerBudget.Unlock()
	hikerBalanceState.Lock()
	hikerBalanceState.exhausted = false
	hikerBalanceState.exhaustedAt = time.Time{}
	hikerBalanceState.lowAlerted = false
	hikerBalanceState.emptyAlerted = false
	hikerBalanceState.balanceReadAlerted = false
	hikerBalanceState.Unlock()
}

// useHikerTestEnv turns HikerAPI on with the test key, a $1 budget, $0.001 a
// request and an in-memory ledger, restoring everything after the test.
func useHikerTestEnv(t *testing.T) *fakePaidLedger {
	t.Helper()
	t.Setenv("HIKERAPI_TOKEN", hikerTestKey)
	t.Setenv("HIKERAPI_MONTHLY_BUDGET_USD", "1.0")
	t.Setenv("HIKERAPI_PRICE_PER_REQUEST_USD", "0.001")
	t.Setenv("HIKERAPI_LOW_BALANCE_REQUESTS", "100")
	ledger := &fakePaidLedger{}
	previous := paidLedger
	paidLedger = ledger
	resetHikerState()

	t.Cleanup(func() {
		paidLedger = previous
		resetHikerState()
	})

	return ledger
}

// fakeHiker serves HikerAPI paths (bodies with {{BASE}} replaced by the
// server URL), /media/* files (404 under /media/missing) and counts calls.
type fakeHiker struct {
	mu       sync.Mutex
	server   *httptest.Server
	status   int
	bodies   map[string]string
	calls    map[string]int
	queries  []url.Values
	delay    time.Duration
	blocking chan struct{}
}

func newFakeHiker(t *testing.T) *fakeHiker {
	fake := &fakeHiker{status: http.StatusOK, bodies: map[string]string{}, calls: map[string]int{}}
	fake.server = httptest.NewServer(http.HandlerFunc(fake.serve))
	t.Cleanup(fake.server.Close)

	return fake
}

func (f *fakeHiker) serve(w http.ResponseWriter, r *http.Request) {
	isMedia := strings.HasPrefix(r.URL.Path, "/media/")

	if isMedia == true {
		serveFakeMedia(w, r)
	}

	if isMedia == false {
		f.serveAPI(w, r)
	}
}

// serveFakeMedia answers a CDN file, or 404 under /media/missing.
func serveFakeMedia(w http.ResponseWriter, r *http.Request) {
	isMissing := strings.HasPrefix(r.URL.Path, "/media/missing")

	if isMissing == true {
		w.WriteHeader(http.StatusNotFound)
	}

	if isMissing == false {
		w.Write([]byte("media-bytes"))
	}
}

// serveAPI answers a HikerAPI path: 401 for a wrong key, else the scripted
// status (always 200 for the balance) and body.
func (f *fakeHiker) serveAPI(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.calls[r.URL.Path]++
	f.queries = append(f.queries, r.URL.Query())
	status := f.status
	body := strings.ReplaceAll(f.bodies[r.URL.Path], "{{BASE}}", f.server.URL)
	delay := f.delay
	blocking := f.blocking
	f.mu.Unlock()

	if blocking != nil {
		<-blocking
	}

	time.Sleep(delay)

	if r.URL.Path == hikerPathBalance {
		status = http.StatusOK
	}

	if r.Header.Get(hikerAccessKeyHeader) != hikerTestKey {
		status = http.StatusUnauthorized
		body = `{"state":false,"error":"Unauthorized request"}`
	}

	w.WriteHeader(status)
	fmt.Fprint(w, body)
}

func (f *fakeHiker) callCount(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.calls[path]
}

func (f *fakeHiker) setStatus(status int) {
	f.mu.Lock()
	f.status = status
	f.mu.Unlock()
}

func (f *fakeHiker) client() *hikerClient {
	return &hikerClient{token: hikerTestKey, apiBase: f.server.URL, client: f.server.Client()}
}

// Answers shaped like the OpenAPI Media, Story and Balance schemas.
const (
	hikerCarouselBody = `{"pk":"3456","id":"3456_789","code":"DeYsKEDjbGB","taken_at":"2026-10-01T10:00:00Z","media_type":8,"product_type":"carousel_container","thumbnail_url":null,"video_url":null,
		"user":{"pk":"789","username":"averona.space","full_name":"Averona & Co","is_private":false},"caption_text":"Hello <world>","like_count":5,
		"resources":[
			{"pk":"1","id":"1_789","media_type":1,"thumbnail_url":"{{BASE}}/media/a.jpg","video_url":null},
			{"pk":"2","id":"2_789","media_type":2,"thumbnail_url":"{{BASE}}/media/b.jpg","video_url":"{{BASE}}/media/b.mp4"}]}`
	hikerPhotoBody = `{"pk":"1","id":"1_7","code":"PHOTO1","taken_at":"2026-10-01T10:00:00Z","media_type":1,"thumbnail_url":"{{BASE}}/media/p.jpg",
		"user":{"pk":"7","username":"someone","full_name":""},"caption_text":"","like_count":1,"resources":[]}`
	hikerVideoBody = `{"pk":"2","id":"2_7","code":"VIDEO1","taken_at":"2026-10-01T10:00:00Z","media_type":2,"product_type":"clips","thumbnail_url":"{{BASE}}/media/v.jpg","video_url":"{{BASE}}/media/v.mp4",
		"user":{"pk":"7","username":"someone","full_name":"Some One"},"caption_text":"clip","like_count":1,"resources":[]}`
	hikerStoryVideoBody = `{"pk":"4007835059386000871","id":"4007835059386000871_528817151","code":"Dea","taken_at":"2026-10-18T08:00:00Z","media_type":2,
		"thumbnail_url":"{{BASE}}/media/s.jpg","video_url":"{{BASE}}/media/s.mp4","user":{"pk":"528817151","username":"nasa"},
		"mentions":[],"links":[],"hashtags":[],"locations":[],"stickers":[]}`
	hikerStoryPhotoBody = `{"pk":"111","id":"111_528817151","code":"Deb","taken_at":"2026-10-18T08:00:00Z","media_type":1,
		"thumbnail_url":"{{BASE}}/media/s.jpg","video_url":null,"user":{"pk":"528817151","username":"nasa"},
		"mentions":[],"links":[],"hashtags":[],"locations":[],"stickers":[]}`
	hikerBalanceBody = `{"requests":129627,"rate":11,"currency":"USD","amount":2592.5777}`
)

func TestHikerStatusCodesMapToErrorsAndBilling(t *testing.T) {
	rows := []struct {
		status    int
		isOK      bool
		isBudget  bool
		isContent bool
		charged   bool
	}{
		{status: http.StatusOK, isOK: true, charged: true},
		{status: http.StatusBadRequest, charged: true},
		{status: http.StatusUnauthorized},
		{status: http.StatusPaymentRequired, isBudget: true},
		{status: http.StatusForbidden, isContent: true, charged: true},
		{status: http.StatusNotFound, isContent: true, charged: true},
		{status: http.StatusTooManyRequests},
		{status: http.StatusInternalServerError},
		{status: http.StatusServiceUnavailable},
	}

	for _, row := range rows {
		t.Run(fmt.Sprintf("%d", row.status), func(t *testing.T) {
			ledger := useHikerTestEnv(t)
			fake := newFakeHiker(t)
			fake.status = row.status
			fake.bodies[hikerPathMediaByCode] = hikerPhotoBody

			_, err := fake.client().get(context.Background(), paidFeatureGated, hikerPathMediaByCode, url.Values{"code": {"PHOTO1"}})
			rowsWritten := ledger.recorded()

			if (err == nil) != row.isOK {
				t.Fatalf("unexpected error: %v", err)
			}

			if (isPaidBudgetError(err) != row.isBudget) || (isPaidContentOutcome(err) != row.isContent) {
				t.Fatalf("wrong class: budget=%v content=%v err=%v", isPaidBudgetError(err), isPaidContentOutcome(err), err)
			}

			if (len(rowsWritten) != 1) || (rowsWritten[0].StatusCode != row.status) || (rowsWritten[0].Charged != row.charged) || (rowsWritten[0].Feature != paidFeatureGated) {
				t.Fatalf("unexpected ledger rows: %+v", rowsWritten)
			}

			expectedCost := 0.0

			if row.charged == true {
				expectedCost = 0.001
			}

			if rowsWritten[0].CostUsd != expectedCost {
				t.Fatalf("expected cost %v, got %+v", expectedCost, rowsWritten[0])
			}
		})
	}
}

func TestHikerRequestWithoutAnAnswerIsBilledAtFullPrice(t *testing.T) {
	ledger := useHikerTestEnv(t)
	fake := newFakeHiker(t)
	fake.delay = 300 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := fake.client().get(ctx, paidFeatureStory, hikerPathStoryByURL, url.Values{})
	rows := ledger.recorded()

	if (err == nil) || (isPaidSkip(err) == true) || (isPaidContentOutcome(err) == true) {
		t.Fatalf("expected a plain failure, got %v", err)
	}

	if (len(rows) != 1) || (rows[0].StatusCode != hikerNoStatus) || (rows[0].Charged == false) || (rows[0].CostUsd != 0.001) {
		t.Fatalf("expected one row billed at full price, got %+v", rows)
	}
}

func TestHikerRequestThatNeverLeftIsFree(t *testing.T) {
	closed := httptest.NewServer(http.NotFoundHandler())
	closedURL := closed.URL
	closed.Close()
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	rows := []struct {
		name    string
		apiBase string
		ctx     context.Context
	}{
		{name: "request build error", apiBase: "http://[::1", ctx: context.Background()},
		{name: "context done before sending", apiBase: "", ctx: cancelled},
		{name: "connection refused", apiBase: closedURL, ctx: context.Background()},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			ledger := useHikerTestEnv(t)
			fake := newFakeHiker(t)
			client := fake.client()

			if row.apiBase != "" {
				client.apiBase = row.apiBase
			}

			_, err := client.get(row.ctx, paidFeatureStory, hikerPathStoryByURL, url.Values{})
			written := ledger.recorded()

			if (err == nil) || (fake.callCount(hikerPathStoryByURL) != 0) {
				t.Fatalf("expected a failure without a request, got %v", err)
			}

			if (len(written) != 1) || (written[0].Charged == true) || (written[0].CostUsd != 0) {
				t.Fatalf("expected one free row, got %+v", written)
			}
		})
	}
}

func TestHikerRedirectIsNotBilled(t *testing.T) {
	for _, status := range []int{http.StatusMovedPermanently, http.StatusFound, http.StatusTemporaryRedirect} {
		t.Run(fmt.Sprintf("%d", status), func(t *testing.T) {
			ledger := useHikerTestEnv(t)
			fake := newFakeHiker(t)
			fake.status = status

			_, err := fake.client().get(context.Background(), paidFeatureGated, hikerPathMediaByCode, url.Values{})
			written := ledger.recorded()

			if (err == nil) || (len(written) != 1) || (written[0].StatusCode != status) || (written[0].Charged == true) {
				t.Fatalf("expected an unbilled failure, got %v %+v", err, written)
			}
		})
	}
}

func TestHikerUnauthorizedAlertsTheOwner(t *testing.T) {
	useHikerTestEnv(t)
	recorder := newAlertRecorder(t)
	fake := newFakeHiker(t)
	client := fake.client()
	client.token = "wrong"

	_, err := client.get(context.Background(), paidFeatureGated, hikerPathMediaByCode, url.Values{})

	if (err == nil) || (isPaidSkip(err) == true) || (len(recorder.alertsWithKey(hikerUnauthorizedAlertKey)) != 1) {
		t.Fatalf("expected a failure and one alert, got %v %+v", err, recorder.recordedAlerts())
	}
}

func TestHikerPaymentRequiredSkipsHikerUntilATopUp(t *testing.T) {
	useHikerTestEnv(t)
	recorder := newAlertRecorder(t)
	fake := newFakeHiker(t)
	fake.status = http.StatusPaymentRequired
	fake.bodies[hikerPathBalance] = `{"requests":0,"rate":11,"currency":"USD","amount":0.0}`
	client := fake.client()

	client.get(context.Background(), paidFeatureGated, hikerPathMediaByCode, url.Values{})
	_, skipped := client.get(context.Background(), paidFeatureGated, hikerPathMediaByCode, url.Values{})

	if (errors.Is(skipped, errHikerOutOfBalance) == false) || (fake.callCount(hikerPathMediaByCode) != 1) {
		t.Fatalf("expected the second call skipped, got %v calls=%d", skipped, fake.callCount(hikerPathMediaByCode))
	}

	checkHikerBalanceWith(client)

	if (isHikerBalanceExhausted() == false) || (len(recorder.alertsWithKey(hikerEmptyBalanceAlertKey)) != 1) || (len(recorder.alertsWithKey(hikerLowBalanceAlertKey)) != 0) {
		t.Fatalf("an empty balance must keep HikerAPI skipped and alert once, got %+v", recorder.recordedAlerts())
	}

	fake.mu.Lock()
	fake.bodies[hikerPathBalance] = hikerBalanceBody
	fake.mu.Unlock()
	fake.setStatus(http.StatusOK)
	checkHikerBalanceWith(client)
	_, err := client.get(context.Background(), paidFeatureGated, hikerPathMediaByCode, url.Values{})

	if (err != nil) || (len(recorder.alertsWithKey(hikerRefilledAlertKey)) != 1) || (len(recorder.alertsWithKey(hikerToppedUpAlertKey)) != 0) {
		t.Fatalf("expected HikerAPI back after the top-up, got %v %+v", err, recorder.recordedAlerts())
	}
}

func TestHikerEmptyBalanceIsProbedAgainAfterTheRetryInterval(t *testing.T) {
	useHikerTestEnv(t)
	newAlertRecorder(t)
	fake := newFakeHiker(t)
	fake.status = http.StatusPaymentRequired
	client := fake.client()
	start := time.Now()
	hikerNow = func() time.Time { return start }

	client.get(context.Background(), paidFeatureGated, hikerPathMediaByCode, url.Values{})
	hikerNow = func() time.Time { return start.Add(hikerExhaustedRetryInterval - time.Minute) }
	_, skipped := client.get(context.Background(), paidFeatureGated, hikerPathMediaByCode, url.Values{})

	if (errors.Is(skipped, errHikerOutOfBalance) == false) || (fake.callCount(hikerPathMediaByCode) != 1) {
		t.Fatalf("expected HikerAPI skipped within the interval, got %v calls=%d", skipped, fake.callCount(hikerPathMediaByCode))
	}

	fake.setStatus(http.StatusOK)
	hikerNow = func() time.Time { return start.Add(hikerExhaustedRetryInterval) }
	_, err := client.get(context.Background(), paidFeatureGated, hikerPathMediaByCode, url.Values{})

	if (err != nil) || (fake.callCount(hikerPathMediaByCode) != 2) {
		t.Fatalf("expected a probe after the interval, got %v calls=%d", err, fake.callCount(hikerPathMediaByCode))
	}
}

func TestHikerFailedBalanceReadAlertsOnceAndKeepsHikerOn(t *testing.T) {
	useHikerTestEnv(t)
	recorder := newAlertRecorder(t)
	fake := newFakeHiker(t)
	fake.bodies[hikerPathBalance] = "not json"
	client := fake.client()

	checkHikerBalanceWith(client)
	checkHikerBalanceWith(client)
	_, err := client.get(context.Background(), paidFeatureGated, hikerPathMediaByCode, url.Values{})

	if (len(recorder.alertsWithKey(hikerBalanceReadAlertKey)) != 1) || (isHikerBalanceExhausted() == true) || (err != nil) {
		t.Fatalf("expected one alert and HikerAPI still on, got %v %+v", err, recorder.recordedAlerts())
	}

	fake.mu.Lock()
	fake.bodies[hikerPathBalance] = hikerBalanceBody
	fake.mu.Unlock()
	checkHikerBalanceWith(client)
	fake.mu.Lock()
	fake.bodies[hikerPathBalance] = "not json"
	fake.mu.Unlock()
	checkHikerBalanceWith(client)

	if (len(recorder.alertsWithKey(hikerBalanceReadAlertKey)) != 2) || (len(recorder.recordedResets()) != 1) {
		t.Fatalf("a read that works must re-arm the alert, got %+v %v", recorder.recordedAlerts(), recorder.recordedResets())
	}
}

func TestHikerBudgetRefusesWithoutARequest(t *testing.T) {
	ledger := useHikerTestEnv(t)
	fake := newFakeHiker(t)
	ledger.spent = 1.0

	_, err := fake.client().get(context.Background(), paidFeatureGated, hikerPathMediaByCode, url.Values{})

	if (errors.Is(err, errHikerBudget) == false) || (IsPaidBudgetError(err) == false) || (fake.callCount(hikerPathMediaByCode) != 0) || (len(ledger.recorded()) != 0) {
		t.Fatalf("expected a refusal without a request, got %v calls=%d", err, fake.callCount(hikerPathMediaByCode))
	}
}

func TestHikerBudgetAllowsTheLastRequestThatFits(t *testing.T) {
	ledger := useHikerTestEnv(t)
	fake := newFakeHiker(t)
	ledger.spent = 0.999

	_, err := fake.client().get(context.Background(), paidFeatureGated, hikerPathMediaByCode, url.Values{})

	if err != nil {
		t.Fatalf("the 1000th request of a $1 budget must run, got %v", err)
	}
}

func TestHikerUnreadableLedgerSkipsTheRequestAsUnavailable(t *testing.T) {
	ledger := useHikerTestEnv(t)
	fake := newFakeHiker(t)
	ledger.readErr = errors.New("database down")

	_, err := fake.client().get(context.Background(), paidFeatureGated, hikerPathMediaByCode, url.Values{})

	if (isPaidUnavailable(err) == false) || (isPaidSkip(err) == false) || (isPaidBudgetError(err) == true) || (fake.callCount(hikerPathMediaByCode) != 0) {
		t.Fatalf("expected an unavailable skip without a request, got %v", err)
	}
}

func TestHikerUnrecordedSpendCountsOnlyInItsMonth(t *testing.T) {
	ledger := useHikerTestEnv(t)
	t.Setenv("HIKERAPI_MONTHLY_BUDGET_USD", "0.001")
	fake := newFakeHiker(t)
	ledger.writeErr = errors.New("insert failed")
	client := fake.client()
	endOfOctober := time.Date(2026, time.October, 31, 23, 59, 0, 0, time.UTC)
	hikerNow = func() time.Time { return endOfOctober }

	_, first := client.get(context.Background(), paidFeatureGated, hikerPathMediaByCode, url.Values{})
	_, refused := client.get(context.Background(), paidFeatureGated, hikerPathMediaByCode, url.Values{})

	if (first != nil) || (isPaidBudgetError(refused) == false) {
		t.Fatalf("expected the unrecorded spend counted in October, got %v / %v", first, refused)
	}

	hikerNow = func() time.Time { return endOfOctober.Add(2 * time.Minute) }
	_, november := client.get(context.Background(), paidFeatureGated, hikerPathMediaByCode, url.Values{})

	if (november != nil) || (fake.callCount(hikerPathMediaByCode) != 2) {
		t.Fatalf("October's unrecorded spend must not count in November, got %v calls=%d", november, fake.callCount(hikerPathMediaByCode))
	}
}

func TestHikerBudgetReadsTheLedgerWithoutHoldingTheLock(t *testing.T) {
	ledger := useHikerTestEnv(t)
	t.Setenv("HIKERAPI_MONTHLY_BUDGET_USD", "0.005")
	ledger.readDelay = 100 * time.Millisecond
	fake := newFakeHiker(t)
	fake.blocking = make(chan struct{})
	client := fake.client()
	results := make(chan error, 10)

	for i := 0; i < 10; i++ {
		go func() {
			_, err := client.get(context.Background(), paidFeatureStory, hikerPathStoryByURL, url.Values{})
			results <- err
		}()
	}

	refused := 0

	for i := 0; i < 5; i++ {
		if err := <-results; isPaidBudgetError(err) == true {
			refused++
		}
	}

	close(fake.blocking)
	succeeded := 0

	for i := 0; i < 5; i++ {
		if err := <-results; err == nil {
			succeeded++
		}
	}

	ledger.mu.Lock()
	maxReading := ledger.maxReading
	ledger.mu.Unlock()

	if maxReading < 2 {
		t.Fatalf("the ledger reads must run in parallel, got at most %d at once", maxReading)
	}

	if (refused != 5) || (succeeded != 5) || (fake.callCount(hikerPathStoryByURL) != 5) {
		t.Fatalf("expected 5 runs and 5 refusals, got refused=%d succeeded=%d calls=%d", refused, succeeded, fake.callCount(hikerPathStoryByURL))
	}
}

func TestHikerBudgetCountsARequestSettledDuringTheLedgerRead(t *testing.T) {
	ledger := useHikerTestEnv(t)
	t.Setenv("HIKERAPI_MONTHLY_BUDGET_USD", "0.001")

	hikerBudget.Lock()
	hikerBudget.inFlightUsd = 0.001
	hikerBudget.Unlock()

	ledger.onRead = func() {
		settleHikerRequest(paidFeatureGated, hikerAnswer{Status: http.StatusOK, IsSent: true}, 0.001)
	}

	err := reserveHikerBudget(context.Background(), 0.001)

	if (isPaidBudgetError(err) == false) || (ledger.readsServed != 2) {
		t.Fatalf("the request settled during the read must count, got %v after %d reads", err, ledger.readsServed)
	}
}

func TestHikerLostLedgerRowStillCountsAgainstTheBudget(t *testing.T) {
	ledger := useHikerTestEnv(t)
	t.Setenv("HIKERAPI_MONTHLY_BUDGET_USD", "0.001")
	fake := newFakeHiker(t)
	ledger.writeErr = errors.New("insert failed")
	client := fake.client()

	_, first := client.get(context.Background(), paidFeatureGated, hikerPathMediaByCode, url.Values{})
	_, second := client.get(context.Background(), paidFeatureGated, hikerPathMediaByCode, url.Values{})

	if (first != nil) || (isPaidBudgetError(second) == false) {
		t.Fatalf("expected the unrecorded spend counted, got %v / %v", first, second)
	}
}

func TestHikerBudgetCountsRequestsInFlight(t *testing.T) {
	useHikerTestEnv(t)
	t.Setenv("HIKERAPI_MONTHLY_BUDGET_USD", "0.005")
	fake := newFakeHiker(t)
	fake.blocking = make(chan struct{})
	client := fake.client()
	results := make(chan error, 10)

	for i := 0; i < 10; i++ {
		go func() {
			_, err := client.get(context.Background(), paidFeatureStory, hikerPathStoryByURL, url.Values{})
			results <- err
		}()
	}

	refused := 0

	for i := 0; i < 5; i++ {
		if err := <-results; isPaidBudgetError(err) == true {
			refused++
		}
	}

	close(fake.blocking)
	succeeded := 0

	for i := 0; i < 5; i++ {
		if err := <-results; err == nil {
			succeeded++
		}
	}

	if (refused != 5) || (succeeded != 5) || (fake.callCount(hikerPathStoryByURL) != 5) {
		t.Fatalf("expected 5 runs and 5 refusals, got refused=%d succeeded=%d calls=%d", refused, succeeded, fake.callCount(hikerPathStoryByURL))
	}
}

func TestHikerBalanceParsesTheAnswer(t *testing.T) {
	useHikerTestEnv(t)
	fake := newFakeHiker(t)
	fake.bodies[hikerPathBalance] = hikerBalanceBody

	balance, err := fake.client().balance(context.Background())

	if (err != nil) || (balance.Requests != 129627) || (balance.Currency != "USD") || (balance.Amount != 2592.5777) {
		t.Fatalf("unexpected balance: %+v %v", balance, err)
	}

	fake.bodies[hikerPathBalance] = `{"rate":11}`

	if _, missing := fake.client().balance(context.Background()); missing == nil {
		t.Fatalf("a balance without requests must fail")
	}
}

func TestHikerLowBalanceAlertsOnceThenRecovers(t *testing.T) {
	useHikerTestEnv(t)
	recorder := newAlertRecorder(t)

	applyHikerBalance(HikerBalance{Requests: 58})
	applyHikerBalance(HikerBalance{Requests: 50})
	lowAlerts := recorder.alertsWithKey(hikerLowBalanceAlertKey)

	if (len(lowAlerts) != 1) || (strings.Contains(lowAlerts[0].text, "58 requests left") == false) {
		t.Fatalf("expected one low-balance alert, got %+v", recorder.recordedAlerts())
	}

	applyHikerBalance(HikerBalance{Requests: 5000})
	applyHikerBalance(HikerBalance{Requests: 4999})

	if (len(recorder.alertsWithKey(hikerToppedUpAlertKey)) != 1) || (len(recorder.recordedResets()) != 1) {
		t.Fatalf("expected one recovery notice, got %+v %v", recorder.recordedAlerts(), recorder.recordedResets())
	}

	applyHikerBalance(HikerBalance{Requests: 0})
	applyHikerBalance(HikerBalance{Requests: 0})

	if (isHikerBalanceExhausted() == false) || (len(recorder.alertsWithKey(hikerEmptyBalanceAlertKey)) != 1) || (len(recorder.alertsWithKey(hikerLowBalanceAlertKey)) != 1) {
		t.Fatalf("an empty balance must alert once on its own key and skip HikerAPI, got %+v", recorder.recordedAlerts())
	}

	applyHikerBalance(HikerBalance{Requests: 50})

	if (isHikerBalanceExhausted() == true) || (len(recorder.alertsWithKey(hikerRefilledAlertKey)) != 1) || (len(recorder.alertsWithKey(hikerLowBalanceAlertKey)) != 2) {
		t.Fatalf("a refill must send its own notice and the low alert must follow its own state, got %+v", recorder.recordedAlerts())
	}
}

func TestHikerIsOffWithoutAKeyOrBudget(t *testing.T) {
	t.Setenv("APIFY_TOKEN", "")
	t.Setenv("HIKERAPI_TOKEN", "")
	sd := &SmartDownloader{}

	_, storyErr := sd.storiesHikerExtract(context.Background(), "https://www.instagram.com/stories/nasa/1/")
	_, balanceErr := GetHikerBalance()

	if (newHikerClient() != nil) || (NewInstagramStoriesHikerExtractor("") != nil) || (NewGatedPostProvider("") != nil) {
		t.Fatalf("nothing of HikerAPI may be built without a key")
	}

	if (errors.Is(storyErr, errHikerNotConfigured) == false) || (errors.Is(balanceErr, errHikerNotConfigured) == false) {
		t.Fatalf("expected not-configured errors, got %v / %v", storyErr, balanceErr)
	}

	if (IsPaidServiceConfigured() == true) || (len(instagramStoriesMethods()) != 1) {
		t.Fatalf("expected no paid service and Apify alone in the stories chain")
	}

	t.Setenv("HIKERAPI_TOKEN", hikerTestKey)
	t.Setenv("HIKERAPI_MONTHLY_BUDGET_USD", "0")

	if (newHikerClient() != nil) || (IsPaidServiceConfigured() == true) {
		t.Fatalf("a budget of 0 turns HikerAPI off")
	}

	if newHikerBalanceClient() == nil {
		t.Fatalf("the free balance read must work with a paused budget")
	}
}

func TestHikerPriceOfZeroKeepsTheDefault(t *testing.T) {
	t.Setenv("HIKERAPI_PRICE_PER_REQUEST_USD", "0")

	if config.GetHikerAPIPricePerRequestUsd() != 0.001 {
		t.Fatalf("a price of 0 must keep the default, got %v", config.GetHikerAPIPricePerRequestUsd())
	}

	t.Setenv("HIKERAPI_PRICE_PER_REQUEST_USD", "0.0005")

	if config.GetHikerAPIPricePerRequestUsd() != 0.0005 {
		t.Fatalf("a positive price must be kept, got %v", config.GetHikerAPIPricePerRequestUsd())
	}
}

func TestHikerClientDoesNotFollowRedirects(t *testing.T) {
	useHikerTestEnv(t)
	leakedKeys := make(chan string, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leakedKeys <- r.Header.Get(hikerAccessKeyHeader)
		fmt.Fprint(w, hikerBalanceBody)
	}))
	t.Cleanup(target.Close)
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.Path, http.StatusFound)
	}))
	t.Cleanup(redirector.Close)
	client := newHikerClientWithToken()
	client.apiBase = redirector.URL

	_, err := client.balance(context.Background())

	if (err == nil) || (len(leakedKeys) != 0) {
		t.Fatalf("a redirect must fail without reaching its target, got err=%v leaked=%d", err, len(leakedKeys))
	}
}

func TestHikerLowBalanceAlertIsRetriedAfterAFailedSend(t *testing.T) {
	useHikerTestEnv(t)
	recorder := newAlertRecorder(t)
	attempts := 0
	notifyAdmin = func(alertKey, text string) bool {
		attempts++

		return false
	}

	applyHikerBalance(HikerBalance{Requests: 80})
	applyHikerBalance(HikerBalance{Requests: 20})
	markHikerBalanceExhausted()

	if attempts != 3 {
		t.Fatalf("an undelivered alert must be retried, got %d attempts", attempts)
	}

	notifyAdmin = recorder.notify
	applyHikerBalance(HikerBalance{Requests: 10})
	applyHikerBalance(HikerBalance{Requests: 5})

	if len(recorder.alertsWithKey(hikerLowBalanceAlertKey)) != 1 {
		t.Fatalf("a delivered alert must be sent once, got %+v", recorder.recordedAlerts())
	}
}

func TestHikerRequestsSendTheKeyInAHeaderOnly(t *testing.T) {
	useHikerTestEnv(t)
	fake := newFakeHiker(t)

	fake.client().get(context.Background(), paidFeatureGated, hikerPathMediaByCode, url.Values{"code": {"DeYsKEDjbGB"}})

	if (len(fake.queries) != 1) || (fake.queries[0].Get("code") != "DeYsKEDjbGB") || (fake.queries[0].Get("access_key") != "") {
		t.Fatalf("unexpected query: %+v", fake.queries)
	}
}
