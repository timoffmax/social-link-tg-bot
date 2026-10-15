package services

// Integration tests for the gated-fetch quota store. They need a disposable
// PostgreSQL database and are skipped unless GATED_FETCH_TEST_DSN is set, e.g.
// GATED_FETCH_TEST_DSN="postgres://postgres:test@localhost:55432/postgres?sslmode=disable"

import (
	"database/sql"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	_ "github.com/lib/pq"
)

const gatedFetchTestDSNKey = "GATED_FETCH_TEST_DSN"

// gatedFetchTestMigrations are applied to the test database before each test.
var gatedFetchTestMigrations = []string{
	"../migrations/006_create_gated_fetch.up.sql",
	"../migrations/007_create_support_payments.up.sql",
}

func setupGatedFetchTestDB(t *testing.T) {
	dsn := os.Getenv(gatedFetchTestDSNKey)

	if dsn == "" {
		t.Skipf("%s not set", gatedFetchTestDSNKey)
	}

	db, err := sql.Open("postgres", dsn)

	if err != nil {
		t.Fatalf("open: %v", err)
	}

	mustExec(t, db, "DROP TABLE IF EXISTS gated_fetch_usage, gated_fetch_policies, support_payments, gated_fetch_credits CASCADE")

	for _, path := range gatedFetchTestMigrations {
		migration, err := os.ReadFile(path)

		if err != nil {
			t.Fatalf("read migration: %v", err)
		}

		mustExec(t, db, string(migration))
	}

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
		denial, _, err = ReserveGatedFetch(quotas)
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

func TestGatedFetchGroupIsOnByDefaultAndHonoursCap(t *testing.T) {
	setupGatedFetchTestDB(t)
	t.Setenv("GATED_FETCH_CHAT_MONTHLY_CAP", "2")

	for i := 0; i < 2; i++ {
		if denial := reserveOnce(t, -100, int64(7+i), false); denial != GatedFetchAllowed {
			t.Fatalf("expected groups on by default, fetch %d got %q", i+1, denial)
		}
	}

	if denial := reserveOnce(t, -100, 9, false); denial != GatedFetchDeniedChatCap {
		t.Fatalf("expected the default group cap, got %q", denial)
	}
}

func TestGatedFetchSettingACapKeepsAGroupOn(t *testing.T) {
	setupGatedFetchTestDB(t)

	if err := SetGatedFetchMonthlyLimit(GatedFetchScopeChat, -200, sql.NullInt64{Int64: 1, Valid: true}); err != nil {
		t.Fatalf("cap: %v", err)
	}

	if denial := reserveOnce(t, -200, 7, false); denial != GatedFetchAllowed {
		t.Fatalf("a capped group must stay on, got %q", denial)
	}

	if denial := reserveOnce(t, -200, 8, false); denial != GatedFetchDeniedChatCap {
		t.Fatalf("expected the custom cap, got %q", denial)
	}
}

func TestGatedFetchGroupTurnedOffIsDenied(t *testing.T) {
	setupGatedFetchTestDB(t)

	if err := SetGatedFetchEnabled(GatedFetchScopeChat, -300, false); err != nil {
		t.Fatalf("off: %v", err)
	}

	if denial := reserveOnce(t, -300, 7, false); denial != GatedFetchDeniedChatOff {
		t.Fatalf("expected an explicit off to deny, got %q", denial)
	}
}

func TestGatedFetchUserOverrideZeroDisablesPrivateUse(t *testing.T) {
	setupGatedFetchTestDB(t)

	if err := SetGatedFetchMonthlyLimit(GatedFetchScopeUser, 9, sql.NullInt64{Int64: 0, Valid: true}); err != nil {
		t.Fatalf("override: %v", err)
	}

	if denial := reserveOnce(t, 9, 9, true); denial != GatedFetchDeniedBlocked {
		t.Fatalf("expected the zero allowance to block, got %q", denial)
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
			denial, _, err := ReserveGatedFetch(quotas)

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

func (failingGatedProvider) FetchPost(string, bool) ([]*MediaInfo, error) {
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

func TestGatedFetchUnlimitedUserAndChatSkipTheirOwnLimitAndTheGlobalCap(t *testing.T) {
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

	if denial := reserveOnce(t, 50, 50, true); denial != GatedFetchAllowed {
		t.Fatalf("unlimited scopes must skip the global fetch cap, got %q", denial)
	}

	if denial := reserveOnce(t, 60, 60, true); denial != GatedFetchAllowed {
		t.Fatalf("expected a regular user under the global cap, got %q", denial)
	}
}

func TestGatedFetchReservationReportsRemainingQuota(t *testing.T) {
	setupGatedFetchTestDB(t)
	t.Setenv("GATED_FETCH_USER_MONTHLY_ALLOWANCE", "20")

	quotas, _, _ := ResolveGatedFetchQuotas(70, 70, true)
	ReserveGatedFetch(quotas)
	denial, usage, err := ReserveGatedFetch(quotas)

	if (err != nil) || (denial != GatedFetchAllowed) || (usage.IsLimited == false) || (usage.Remaining() != 18) || (usage.Limit != 20) {
		t.Fatalf("expected 18 of 20 left, got %+v %q %v", usage, denial, err)
	}

	unlimited := sql.NullInt64{Int64: GatedFetchUnlimited, Valid: true}
	SetGatedFetchMonthlyLimit(GatedFetchScopeUser, 71, unlimited)
	unlimitedQuotas, _, _ := ResolveGatedFetchQuotas(71, 71, true)
	_, unlimitedUsage, _ := ReserveGatedFetch(unlimitedQuotas)

	if unlimitedUsage.IsLimited == true {
		t.Fatalf("an unlimited user must get no note: %+v", unlimitedUsage)
	}
}

// succeedingGatedProvider returns one photo with a caption.
type succeedingGatedProvider struct{}

func (succeedingGatedProvider) Name() string {
	return "succeeding"
}

func (succeedingGatedProvider) FetchPost(string, bool) ([]*MediaInfo, error) {
	return []*MediaInfo{{Type: MediaTypePhoto, Caption: "📷 author\n\n🔗 link"}}, nil
}

func TestGatedFetchCaptionEndsWithTheRemainingQuota(t *testing.T) {
	setupGatedFetchTestDB(t)
	t.Setenv("GATED_FETCH_CHAT_MONTHLY_CAP", "20")
	sd := &SmartDownloader{gated: succeedingGatedProvider{}}
	gatedErr := &InstagramGatedError{Gate: InstagramGate{Restricted: true}}

	media, err := sd.FetchGatedPost("https://www.instagram.com/p/DeYsKEDjbGB/", gatedErr, -500, 5, false)

	if (err != nil) || (strings.HasSuffix(media[0].Caption, "📊 Restricted posts left this month: 19 of 20") == false) {
		t.Fatalf("expected the quota note, got %q %v", media[0].Caption, err)
	}
}

func TestGatedFetchGroupCapZeroTurnsTheChatOff(t *testing.T) {
	setupGatedFetchTestDB(t)

	if err := SetGatedFetchMonthlyLimit(GatedFetchScopeChat, -400, sql.NullInt64{Int64: 0, Valid: true}); err != nil {
		t.Fatalf("override: %v", err)
	}

	if denial := reserveOnce(t, -400, 7, false); denial != GatedFetchDeniedChatOff {
		t.Fatalf("expected a zero group cap to turn the chat off, got %q", denial)
	}
}

func TestCanUseGatedFetchCreditsFollowsTheEffectiveUserAllowance(t *testing.T) {
	setupGatedFetchTestDB(t)

	if CanUseGatedFetchCredits(5) == false {
		t.Fatalf("expected credits to be usable with the default allowance")
	}

	if err := SetGatedFetchMonthlyLimit(GatedFetchScopeUser, 6, sql.NullInt64{Int64: 0, Valid: true}); err != nil {
		t.Fatalf("override: %v", err)
	}

	if CanUseGatedFetchCredits(6) == true {
		t.Fatalf("expected a zero override to make credits unusable")
	}

	t.Setenv("GATED_FETCH_USER_MONTHLY_ALLOWANCE", "0")

	if CanUseGatedFetchCredits(5) == true {
		t.Fatalf("expected a zero default allowance to make credits unusable")
	}

	if err := SetGatedFetchMonthlyLimit(GatedFetchScopeUser, 7, sql.NullInt64{Int64: 2, Valid: true}); err != nil {
		t.Fatalf("override: %v", err)
	}

	if CanUseGatedFetchCredits(7) == false {
		t.Fatalf("expected a user override above zero to beat the zero default")
	}
}
