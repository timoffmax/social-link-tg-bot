package services

// Integration tests for the gated-fetch quota store. They need a disposable
// PostgreSQL database and are skipped unless GATED_FETCH_TEST_DSN is set, e.g.
// GATED_FETCH_TEST_DSN="postgres://postgres:test@localhost:55432/postgres?sslmode=disable"

import (
	"database/sql"
	"errors"
	"os"
	"sync"
	"testing"

	_ "github.com/lib/pq"
)

const gatedFetchTestDSNKey = "GATED_FETCH_TEST_DSN"

// gatedFetchTestMigration is applied to the test database before each test.
const gatedFetchTestMigration = "../migrations/006_create_gated_fetch.up.sql"

func setupGatedFetchTestDB(t *testing.T) {
	dsn := os.Getenv(gatedFetchTestDSNKey)

	if dsn == "" {
		t.Skipf("%s not set", gatedFetchTestDSNKey)
	}

	db, err := sql.Open("postgres", dsn)

	if err != nil {
		t.Fatalf("open: %v", err)
	}

	migration, err := os.ReadFile(gatedFetchTestMigration)

	if err != nil {
		t.Fatalf("read migration: %v", err)
	}

	mustExec(t, db, "DROP TABLE IF EXISTS gated_fetch_usage, gated_fetch_policies CASCADE")
	mustExec(t, db, string(migration))

	previous := dbService
	dbService = &DatabaseService{db: db}

	t.Cleanup(func() {
		dbService = previous
		db.Close()
	})

	t.Setenv("APIFY_TOKEN", "test-token")
	t.Setenv("GATED_FETCH_GLOBAL_MONTHLY_CAP", "100")
	t.Setenv("GATED_FETCH_USER_MONTHLY_ALLOWANCE", "3")
	t.Setenv("GATED_FETCH_CHAT_MONTHLY_CAP", "20")
}

func mustExec(t *testing.T, db *sql.DB, query string) {
	if _, err := db.Exec(query); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

// reserveOnce resolves and reserves one fetch, failing the test on errors.
func reserveOnce(t *testing.T, chatID, userID int64, isPrivate bool) GatedFetchDenial {
	quotas, denial, err := ResolveGatedFetchQuotas(chatID, userID, isPrivate)

	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if denial == GatedFetchAllowed {
		denial, err = ReserveGatedFetch(quotas)
	}

	if err != nil {
		t.Fatalf("reserve: %v", err)
	}

	return denial
}

func TestGatedFetchPrivateAllowanceRunsOutAndRefunds(t *testing.T) {
	setupGatedFetchTestDB(t)

	for i := 0; i < 3; i++ {
		if denial := reserveOnce(t, 42, 42, true); denial != GatedFetchAllowed {
			t.Fatalf("fetch %d denied: %s", i+1, denial)
		}
	}

	if denial := reserveOnce(t, 42, 42, true); denial != GatedFetchDeniedUserCap {
		t.Fatalf("expected the allowance to run out, got %q", denial)
	}

	quotas, _, _ := ResolveGatedFetchQuotas(42, 42, true)

	if err := RefundGatedFetch(quotas); err != nil {
		t.Fatalf("refund: %v", err)
	}

	if denial := reserveOnce(t, 42, 42, true); denial != GatedFetchAllowed {
		t.Fatalf("expected a fetch after the refund, got %q", denial)
	}
}

func TestGatedFetchGroupIsDeniedUntilEnabledAndHonoursCap(t *testing.T) {
	setupGatedFetchTestDB(t)

	if denial := reserveOnce(t, -100, 7, false); denial != GatedFetchDeniedChatOff {
		t.Fatalf("expected groups off by default, got %q", denial)
	}

	if err := SetGatedFetchEnabled(GatedFetchScopeChat, -100, true); err != nil {
		t.Fatalf("enable: %v", err)
	}

	if err := SetGatedFetchMonthlyLimit(GatedFetchScopeChat, -100, sql.NullInt64{Int64: 1, Valid: true}); err != nil {
		t.Fatalf("cap: %v", err)
	}

	if denial := reserveOnce(t, -100, 7, false); denial != GatedFetchAllowed {
		t.Fatalf("expected the first group fetch, got %q", denial)
	}

	if denial := reserveOnce(t, -100, 8, false); denial != GatedFetchDeniedChatCap {
		t.Fatalf("expected the group cap, got %q", denial)
	}
}

func TestGatedFetchUserOverrideZeroDisablesPrivateUse(t *testing.T) {
	setupGatedFetchTestDB(t)

	if err := SetGatedFetchMonthlyLimit(GatedFetchScopeUser, 9, sql.NullInt64{Int64: 0, Valid: true}); err != nil {
		t.Fatalf("override: %v", err)
	}

	if denial := reserveOnce(t, 9, 9, true); denial != GatedFetchDeniedUserCap {
		t.Fatalf("expected the zero allowance to deny, got %q", denial)
	}
}

func TestGatedFetchGlobalCapDeniesWithoutTakingOtherQuotas(t *testing.T) {
	setupGatedFetchTestDB(t)
	t.Setenv("GATED_FETCH_GLOBAL_MONTHLY_CAP", "1")

	if denial := reserveOnce(t, 1, 1, true); denial != GatedFetchAllowed {
		t.Fatalf("expected the first fetch, got %q", denial)
	}

	if denial := reserveOnce(t, 2, 2, true); denial != GatedFetchDeniedGlobal {
		t.Fatalf("expected the global cap, got %q", denial)
	}

	used, err := GetGatedFetchUsage(GatedFetchScopeUser, 2)

	if (err != nil) || (used != 0) {
		t.Fatalf("a denied fetch must not take the user's allowance: used=%d err=%v", used, err)
	}
}

func TestGatedFetchConcurrentReservationsNeverExceedCap(t *testing.T) {
	setupGatedFetchTestDB(t)
	t.Setenv("GATED_FETCH_USER_MONTHLY_ALLOWANCE", "5")

	var wg sync.WaitGroup
	var mu sync.Mutex
	allowed := 0

	for i := 0; i < 30; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			quotas, _, _ := ResolveGatedFetchQuotas(77, 77, true)
			denial, err := ReserveGatedFetch(quotas)

			if (err == nil) && (denial == GatedFetchAllowed) {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}

	wg.Wait()

	if allowed != 5 {
		t.Fatalf("expected exactly 5 reservations, got %d", allowed)
	}
}

func TestGatedFetchIsOffWithoutToken(t *testing.T) {
	setupGatedFetchTestDB(t)
	t.Setenv("APIFY_TOKEN", "")

	if denial := reserveOnce(t, 1, 1, true); denial != GatedFetchDeniedOff {
		t.Fatalf("expected off without a token, got %q", denial)
	}
}

// failingGatedProvider simulates a billed run whose media could not be used.
type failingGatedProvider struct{}

func (failingGatedProvider) Name() string {
	return "failing"
}

func (failingGatedProvider) FetchPost(string) ([]*MediaInfo, error) {
	return nil, errGatedTestFetch
}

var errGatedTestFetch = errors.New("media download failed")

func TestGatedFetchFailureRefundsUserButKeepsGlobalSpend(t *testing.T) {
	setupGatedFetchTestDB(t)
	sd := &SmartDownloader{gated: failingGatedProvider{}}
	gatedErr := &InstagramGatedError{Gate: InstagramGate{Restricted: true}}

	_, err := sd.FetchGatedPost("https://www.instagram.com/p/DeYsKEDjbGB/", gatedErr, 31, 31, true)

	if err == nil {
		t.Fatalf("expected the failed fetch to return an error")
	}

	userUsed, _ := GetGatedFetchUsage(GatedFetchScopeUser, 31)
	globalUsed, _ := GetGatedFetchUsage(GatedFetchScopeGlobal, 0)

	if (userUsed != 0) || (globalUsed != 1) {
		t.Fatalf("expected user refunded and global kept, got user=%d global=%d", userUsed, globalUsed)
	}
}

func TestGatedFetchUnlimitedUserAndChatSkipTheirOwnLimit(t *testing.T) {
	setupGatedFetchTestDB(t)
	t.Setenv("GATED_FETCH_USER_MONTHLY_ALLOWANCE", "1")
	t.Setenv("GATED_FETCH_CHAT_MONTHLY_CAP", "1")
	t.Setenv("GATED_FETCH_GLOBAL_MONTHLY_CAP", "6")
	unlimited := sql.NullInt64{Int64: GatedFetchUnlimited, Valid: true}

	if err := SetGatedFetchMonthlyLimit(GatedFetchScopeUser, 50, unlimited); err != nil {
		t.Fatalf("user: %v", err)
	}

	if err := SetGatedFetchEnabled(GatedFetchScopeChat, -50, true); err != nil {
		t.Fatalf("enable: %v", err)
	}

	if err := SetGatedFetchMonthlyLimit(GatedFetchScopeChat, -50, unlimited); err != nil {
		t.Fatalf("chat: %v", err)
	}

	for i := 0; i < 3; i++ {
		if denial := reserveOnce(t, 50, 50, true); denial != GatedFetchAllowed {
			t.Fatalf("unlimited user denied at %d: %s", i+1, denial)
		}

		if denial := reserveOnce(t, -50, 51, false); denial != GatedFetchAllowed {
			t.Fatalf("unlimited chat denied at %d: %s", i+1, denial)
		}
	}

	if denial := reserveOnce(t, 50, 50, true); denial != GatedFetchDeniedGlobal {
		t.Fatalf("expected the global cap to still apply, got %q", denial)
	}
}
