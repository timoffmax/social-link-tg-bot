package services

import (
	"database/sql"
	"sync"
	"testing"
	"time"
)

// allowAllLimit allows every special download and refunds nothing.
type allowAllLimit struct{}

func (allowAllLimit) reserve(DownloadRequester) specialReservation {
	return specialReservation{}
}

func (allowAllLimit) refund(specialReservation, DownloadRequester, bool) {}

// recordingLimit answers every reserve with its reservation (after delay)
// and records the reserve and refund calls.
type recordingLimit struct {
	mu          sync.Mutex
	reservation specialReservation
	delay       time.Duration
	onReserve   func()
	reserves    int
	refunds     []bool
}

func (l *recordingLimit) reserve(DownloadRequester) specialReservation {
	time.Sleep(l.delay)

	if l.onReserve != nil {
		l.onReserve()
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	l.reserves++

	return l.reservation
}

func (l *recordingLimit) refund(_ specialReservation, _ DownloadRequester, isGlobalKept bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.refunds = append(l.refunds, isGlobalKept)
}

func (l *recordingLimit) reserveCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.reserves
}

func (l *recordingLimit) refundCalls() []bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	return append([]bool(nil), l.refunds...)
}

// newAllowedDownload is a special download every reserve allows.
func newAllowedDownload(label string) *specialDownload {
	return newSpecialDownload(label, DownloadRequester{}, allowAllLimit{})
}

// markDatabaseOpenForTest makes IsDBInitialized true with a handle that is
// never connected (lib/pq connects lazily), for code that only checks it.
func markDatabaseOpenForTest(t *testing.T) {
	db, err := sql.Open("postgres", "host=127.0.0.1 port=1 sslmode=disable connect_timeout=1")

	if err != nil {
		t.Fatalf("open: %v", err)
	}

	restore := UseDatabaseHandle(db)

	t.Cleanup(func() {
		restore()
		db.Close()
	})
}

// scopeAndGlobalUsage returns this month's user (or chat) and bot-wide counters.
func scopeAndGlobalUsage(t *testing.T, scope GatedFetchScope, scopeID int64) (int, int) {
	used, err := GetGatedFetchUsage(scope, scopeID)
	globalUsed, globalErr := GetGatedFetchUsage(GatedFetchScopeGlobal, GatedFetchGlobalScopeID)

	if (err != nil) || (globalErr != nil) {
		t.Fatalf("usage: %v / %v", err, globalErr)
	}

	return used, globalUsed
}
