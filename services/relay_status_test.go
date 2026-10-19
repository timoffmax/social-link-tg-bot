package services

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// relayStatusTestNow is the fake clock of the relay status tests.
var relayStatusTestNow = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

// fakeRelayLog is an in-memory relayRequestStore.
type fakeRelayLog struct {
	mu         sync.Mutex
	rows       []relayRequestRow
	history    RelayHistory
	historyErr error
	prunes     []time.Time
}

func (l *fakeRelayLog) Record(_ context.Context, row relayRequestRow) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rows = append(l.rows, row)

	return nil
}

func (l *fakeRelayLog) History(context.Context, time.Time) (RelayHistory, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.history, l.historyErr
}

func (l *fakeRelayLog) Prune(_ context.Context, before time.Time) (int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.prunes = append(l.prunes, before)

	return 0, nil
}

func (l *fakeRelayLog) recorded() []relayRequestRow {
	relayRecordsInFlight.Wait()
	l.mu.Lock()
	defer l.mu.Unlock()

	return append([]relayRequestRow(nil), l.rows...)
}

// useFakeRelayLog swaps the relay history store and fixes the clock.
func useFakeRelayLog(t *testing.T) *fakeRelayLog {
	t.Helper()
	fake := &fakeRelayLog{history: RelayHistory{Windows: relayHistoryWindows, Rows: []RelayHistoryRow{}}}
	previousLog := relayRequestLog
	previousNow := relayStatusNow
	relayRequestLog = fake
	relayStatusNow = func() time.Time { return relayStatusTestNow }
	t.Cleanup(func() {
		relayRequestLog = previousLog
		relayStatusNow = previousNow
	})

	return fake
}

// switchableHealth answers /healthz with the body, or a 500 while failing.
type switchableHealth struct {
	mu        sync.Mutex
	isFailing bool
}

func (h *switchableHealth) serve(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	isFailing := h.isFailing
	h.mu.Unlock()
	status := http.StatusOK

	if isFailing == true {
		status = http.StatusInternalServerError
	}

	serveRelayHealth(status, relayHealthyBody)(w, r)
}

func (h *switchableHealth) fail() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.isFailing = true
}

func TestRelayStatusCachesTheHealthzAnswerAndKeepsItOnFailure(t *testing.T) {
	useFakeRelayLog(t)
	health := &switchableHealth{}
	fake := startRelayTestServer(t, health.serve)
	client, _ := newTestRelayClient(t, fake.server.URL)
	configureTestRelayEnv(t, fake.server.URL, client.tokenFile)
	refresher := &relayStatusRefresher{isHistoryOK: true}
	refresher.refresh(context.Background())
	status, isStatus := CurrentRelayStatus().(RelayStatus)
	youtube := status.Platforms[relayPlatformYouTube]

	if (isStatus == false) || (status.IsConfigured == false) || (status.IsReachable == false) || (status.YtDlp != "2026.08.19") || (status.Global.HourlyLimit != 30) {
		t.Fatalf("got %+v", status)
	}

	if (youtube.IsEnabled == false) || (youtube.DailyLimit != 100) || (status.CheckedAt.Equal(relayStatusTestNow) == false) || (status.History == nil) {
		t.Fatalf("got %+v", status)
	}

	if (len(status.PlatformsInUse) != 1) || (status.PlatformsInUse[0] != relayPlatformYouTube) {
		t.Fatalf("platforms in use: %v", status.PlatformsInUse)
	}

	health.fail()
	refresher.refresh(context.Background())
	status, _ = CurrentRelayStatus().(RelayStatus)

	if (status.IsReachable == true) || (strings.Contains(status.Error, "500") == false) || (status.YtDlp != "2026.08.19") {
		t.Fatalf("a failed read must keep the last answer: %+v", status)
	}

	if strings.Contains(status.Error, fake.server.URL) == true {
		t.Fatalf("the error must not carry the URL: %q", status.Error)
	}

	if (recordedFailureCount(relayTestProbeKey) != 0) || (recordedFailureCount(relayTestService) != 0) {
		t.Fatalf("the status must not feed the outage tracker")
	}

	if calls := fake.recorded(); (len(calls) != 2) || (calls[0].authorization != "") || (calls[1].path != relayHealthPath) {
		t.Fatalf("want two GET /healthz without a token: %+v", calls)
	}
}

func TestRelayStatusOfARefusedConnectionHasNoURL(t *testing.T) {
	useFakeRelayLog(t)
	fake := startRelayTestServer(t, serveRelayHealth(http.StatusOK, relayHealthyBody))
	client, _ := newTestRelayClient(t, fake.server.URL)
	address := fake.server.URL
	configureTestRelayEnv(t, address, client.tokenFile)
	fake.server.Close()
	refresher := &relayStatusRefresher{isHistoryOK: true}
	refresher.refresh(context.Background())
	status, _ := CurrentRelayStatus().(RelayStatus)

	if (status.IsReachable == true) || (status.Error == "") || (strings.Contains(status.Error, "http://") == true) {
		t.Fatalf("got %+v", status)
	}
}

func TestRelayStatusNotConfiguredReadsNoHealthz(t *testing.T) {
	useFakeRelayLog(t)
	fake := startRelayTestServer(t, serveRelayHealth(http.StatusOK, relayHealthyBody))
	_, _ = newTestRelayClient(t, fake.server.URL)
	t.Setenv("RELAY_URL", "")
	t.Setenv("RELAY_TOKEN_FILE", "")
	t.Setenv("RELAY_PLATFORMS", "")
	refresher := &relayStatusRefresher{isHistoryOK: true}
	refresher.refresh(context.Background())
	status, _ := CurrentRelayStatus().(RelayStatus)
	encoded, _ := json.Marshal(status)

	if (status.IsConfigured == true) || (status.CheckedAt != nil) || (len(fake.recorded()) != 0) {
		t.Fatalf("got %+v", status)
	}

	for _, field := range []string{`"platforms_in_use":[]`, `"breaker_open":[]`, `"platforms":{}`, `"checked_at":null`, `"history":{"windows":["24h","7d","30d"]`} {
		if strings.Contains(string(encoded), field) == false {
			t.Fatalf("want %s in %s", field, encoded)
		}
	}
}

func TestRelayStatusKeepsTheLastHistoryOnAFailedRead(t *testing.T) {
	relayLog := useFakeRelayLog(t)
	relayLog.history.Rows = []RelayHistoryRow{{Platform: relayPlatformYouTube, Code: relayOutcomeOK, Day: 3, Week: 12, Month: 40}}
	refresher := &relayStatusRefresher{isHistoryOK: true}
	refresher.refresh(context.Background())
	relayLog.mu.Lock()
	relayLog.historyErr = errors.New("connection refused")
	relayLog.mu.Unlock()
	refresher.refresh(context.Background())
	status, _ := CurrentRelayStatus().(RelayStatus)

	if (status.History == nil) || (len(status.History.Rows) != 1) || (refresher.isHistoryOK == true) {
		t.Fatalf("got %+v", status.History)
	}

	relayLog.mu.Lock()
	relayLog.historyErr = errRelayHistoryNoDatabase
	relayLog.mu.Unlock()
	refresher.refresh(context.Background())
	status, _ = CurrentRelayStatus().(RelayStatus)

	if status.History != nil {
		t.Fatalf("without a database the history is null: %+v", status.History)
	}
}

func TestRelayHistoryIsPrunedOncePerDay(t *testing.T) {
	relayLog := useFakeRelayLog(t)
	now := relayStatusTestNow
	relayStatusNow = func() time.Time { return now }
	refresher := &relayStatusRefresher{isHistoryOK: true}
	refresher.refresh(context.Background())
	now = now.Add(time.Hour)
	refresher.refresh(context.Background())
	now = now.Add(relayPruneInterval)
	refresher.refresh(context.Background())

	if len(relayLog.prunes) != 2 {
		t.Fatalf("want 2 prunes, got %v", relayLog.prunes)
	}

	if relayLog.prunes[0].Equal(relayStatusTestNow.Add(-relayHistoryRetention)) == false {
		t.Fatalf("the prune must keep 90 days: %v", relayLog.prunes[0])
	}
}

func TestRelayFetchRecordsASentCallAndNotARefusal(t *testing.T) {
	relayLog := useFakeRelayLog(t)
	fake := startRelayTestServer(t, serveRelayVideo("", relayTestMP4))
	client, _ := newTestRelayClient(t, fake.server.URL)

	if _, err := client.Fetch(context.Background(), relayPlatformYouTube, relayTestShortsID); err != nil {
		t.Fatalf("fetch: %v", err)
	}

	_, _ = client.Fetch(context.Background(), relayPlatformYouTube, "not-an-id")
	rows := relayLog.recorded()

	if (len(rows) != 1) || (rows[0].Code != relayOutcomeOK) || (rows[0].Bytes == nil) || (*rows[0].Bytes != int64(len(relayTestMP4))) || (rows[0].Platform != relayPlatformYouTube) {
		t.Fatalf("want one OK row, got %+v", rows)
	}
}

func TestRelayFetchRecordsTheOutcomeCode(t *testing.T) {
	cases := []struct {
		name    string
		handler http.HandlerFunc
		want    string
	}{
		{"relay code", serveRelayError(http.StatusTooManyRequests, relayCodeRateLimited), relayCodeRateLimited},
		{"unknown code", serveRelayError(http.StatusBadGateway, "SOMETHING_NEW"), relayOutcomeRejected},
		{"not an mp4", serveRelayHealth(http.StatusOK, "{}"), relayOutcomeRejected},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			relayLog := useFakeRelayLog(t)
			fake := startRelayTestServer(t, testCase.handler)
			client, _ := newTestRelayClient(t, fake.server.URL)
			_, _ = client.Fetch(context.Background(), relayPlatformYouTube, relayTestShortsID)
			rows := relayLog.recorded()

			if (len(rows) != 1) || (rows[0].Code != testCase.want) || (rows[0].Bytes != nil) {
				t.Fatalf("got %+v", rows)
			}
		})
	}
}

func TestRelayOutcomeCodeOfBotSideFailures(t *testing.T) {
	tokenErr := &relayError{Detail: "token", IsOutage: true, cause: fmt.Errorf("%w: %w", errRelayToken, os.ErrNotExist)}
	transportErr := &relayError{Detail: "request failed", IsOutage: true, cause: errors.New("EOF")}
	cases := map[string]error{
		relayOutcomeOK:        nil,
		relayOutcomeSkipped:   tokenErr,
		relayOutcomeTransport: transportErr,
		relayCodeBotCheck:     &relayError{Code: relayCodeBotCheck, IsOutage: true},
	}

	for want, err := range cases {
		if got := relayOutcomeCode(err); got != want {
			t.Fatalf("%v: got %s, want %s", err, got, want)
		}
	}
}

func TestBuildRelayHistoryAveragesOnlyDeliveredVideos(t *testing.T) {
	sums := []relayHistorySums{
		{row: RelayHistoryRow{Platform: relayPlatformYouTube, Code: relayOutcomeOK, Day: 2, Week: 4, Month: 9}, dayBytes: 2000, weekBytes: 5000, dayDuration: 3000, weekDuration: 8000},
		{row: RelayHistoryRow{Platform: relayPlatformYouTube, Code: relayCodeTimeout, Day: 1, Week: 1, Month: 1}},
	}
	history := buildRelayHistory(sums)

	if (history.OKBytes.Day != 2000) || (*history.OKAvgMs.Day != 1500) || (*history.OKAvgMs.Week != 2000) || (len(history.Rows) != 2) {
		t.Fatalf("got %+v", history)
	}

	empty := buildRelayHistory(nil)
	encoded, _ := json.Marshal(empty)

	if string(encoded) != `{"windows":["24h","7d","30d"],"rows":[],"ok_bytes":{"24h":0,"7d":0},"ok_avg_ms":{"24h":null,"7d":null}}` {
		t.Fatalf("got %s", encoded)
	}
}

// setupRelayRequestsTestDB applies migration 013 to the throwaway database
// of GATED_FETCH_TEST_DSN and points the services at it.
func setupRelayRequestsTestDB(t *testing.T) *sql.DB {
	dsn := os.Getenv(gatedFetchTestDSNKey)

	if dsn == "" {
		t.Skipf("%s not set", gatedFetchTestDSNKey)
	}

	db, err := sql.Open("postgres", dsn)

	if err != nil {
		t.Fatalf("open: %v", err)
	}

	mustExec(t, db, "DROP TABLE IF EXISTS relay_requests")
	migration, err := os.ReadFile("../migrations/013_relay_requests.up.sql")

	if err != nil {
		t.Fatalf("read migration: %v", err)
	}

	mustExec(t, db, string(migration))
	previous := dbService
	dbService = &DatabaseService{db: db}
	t.Cleanup(func() {
		dbService = previous
		db.Close()
	})

	return db
}

func TestPGRelayRequestsHistoryAndPrune(t *testing.T) {
	db := setupRelayRequestsTestDB(t)
	store := pgRelayRequestStore{}
	ctx := context.Background()
	size := int64(1000)
	rows := []relayRequestRow{
		{Platform: relayPlatformYouTube, Code: relayOutcomeOK, Bytes: &size, DurationMs: 4000},
		{Platform: relayPlatformYouTube, Code: relayOutcomeOK, Bytes: &size, DurationMs: 2000},
		{Platform: relayPlatformYouTube, Code: relayCodeTimeout, DurationMs: 45000},
		{Platform: relayPlatformInstagram, Code: relayOutcomeTransport, DurationMs: 10},
	}

	for _, row := range rows {
		if err := store.Record(ctx, row); err != nil {
			t.Fatalf("record: %v", err)
		}
	}

	mustExec(t, db, "INSERT INTO relay_requests (platform, code, bytes, duration_ms, created_at) VALUES ('youtube', 'OK', 500, 1000, now() - INTERVAL '3 days'), ('youtube', 'OK', 500, 1000, now() - INTERVAL '20 days'), ('youtube', 'OK', 500, 1000, now() - INTERVAL '100 days')")
	history, err := store.History(ctx, time.Now())

	if err != nil {
		t.Fatalf("history: %v", err)
	}

	want := []RelayHistoryRow{
		{Platform: relayPlatformInstagram, Code: relayOutcomeTransport, Day: 1, Week: 1, Month: 1},
		{Platform: relayPlatformYouTube, Code: relayOutcomeOK, Day: 2, Week: 3, Month: 4},
		{Platform: relayPlatformYouTube, Code: relayCodeTimeout, Day: 1, Week: 1, Month: 1},
	}

	if fmt.Sprint(history.Rows) != fmt.Sprint(want) {
		t.Fatalf("got %+v, want %+v", history.Rows, want)
	}

	if (history.OKBytes.Day != 2000) || (history.OKBytes.Week != 2500) || (*history.OKAvgMs.Day != 3000) || (*history.OKAvgMs.Week != 2333) {
		t.Fatalf("got %+v / %v %v", history.OKBytes, *history.OKAvgMs.Day, *history.OKAvgMs.Week)
	}

	removed, err := store.Prune(ctx, time.Now().Add(-relayHistoryRetention))

	if (err != nil) || (removed != 1) {
		t.Fatalf("prune removed %d: %v", removed, err)
	}
}
