package services

// Integration tests for the paid-request ledger and for the quota and credit
// accounting of the provider chain. Like the gated-fetch store tests they
// are skipped unless GATED_FETCH_TEST_DSN is set.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"
)

const (
	paidLedgerUpMigration   = "../migrations/010_paid_requests.up.sql"
	paidLedgerDownMigration = "../migrations/010_paid_requests.down.sql"
)

func setupPaidLedgerTestDB(t *testing.T) *sql.DB {
	dsn := os.Getenv(gatedFetchTestDSNKey)

	if dsn == "" {
		t.Skipf("%s not set", gatedFetchTestDSNKey)
	}

	db, err := sql.Open("postgres", dsn)

	if err != nil {
		t.Fatalf("open: %v", err)
	}

	mustExec(t, db, "DROP TABLE IF EXISTS paid_requests CASCADE")
	applyMigrationFile(t, db, paidLedgerUpMigration)
	previousDB := dbService
	previousLedger := paidLedger
	dbService = &DatabaseService{db: db}
	paidLedger = pgPaidLedger{}
	resetHikerState()

	t.Cleanup(func() {
		dbService = previousDB
		paidLedger = previousLedger
		resetHikerState()
		db.Close()
	})

	return db
}

func applyMigrationFile(t *testing.T, db *sql.DB, path string) {
	migration, err := os.ReadFile(path)

	if err != nil {
		t.Fatalf("read migration: %v", err)
	}

	mustExec(t, db, string(migration))
}

func TestPaidLedgerCountsOnlyThisUTCMonth(t *testing.T) {
	db := setupPaidLedgerTestDB(t)
	monthStart := currentMonthStartUTC()
	lastMonth := monthStart.Add(-time.Second)
	ctx := context.Background()
	insert := "INSERT INTO paid_requests (provider, feature, status_code, charged, cost_usd, created_at) VALUES ($1, $2, $3, $4, $5, $6)"

	if _, err := db.Exec(insert, hikerServiceName, paidFeatureGated, 200, true, 0.5, lastMonth); err != nil {
		t.Fatalf("insert: %v", err)
	}

	if _, err := db.Exec(insert, hikerServiceName, paidFeatureStory, 200, true, 0.001, monthStart); err != nil {
		t.Fatalf("insert: %v", err)
	}

	if _, err := db.Exec(insert, "other", paidFeatureStory, 200, true, 0.7, monthStart); err != nil {
		t.Fatalf("insert: %v", err)
	}

	for _, request := range []PaidRequest{
		{Provider: hikerServiceName, Feature: paidFeatureGated, StatusCode: 404, Charged: true, CostUsd: 0.001},
		{Provider: hikerServiceName, Feature: paidFeatureStory, StatusCode: 503, Charged: false, CostUsd: 0},
	} {
		if err := paidLedger.Record(ctx, request); err != nil {
			t.Fatalf("record: %v", err)
		}
	}

	spent, err := paidLedger.MonthSpentUsd(ctx, hikerServiceName, monthStart)

	if (err != nil) || (spent < 0.001999) || (spent > 0.002001) {
		t.Fatalf("expected $0.002 this month, got %v %v", spent, err)
	}

	stats, err := GetHikerMonthStats()
	expected := PaidMonthStats{Calls: 3, ChargedCalls: 2, GatedCalls: 1, StoryCalls: 2, CostUsd: stats.CostUsd}

	if (err != nil) || (stats != expected) || (stats.CostUsd < 0.001999) || (stats.CostUsd > 0.002001) {
		t.Fatalf("unexpected stats %+v %v", stats, err)
	}
}

func TestPaidLedgerMigrationGoesDownAndUp(t *testing.T) {
	db := setupPaidLedgerTestDB(t)
	tableQuery := "SELECT to_regclass('public.paid_requests') IS NOT NULL, to_regclass('public.idx_paid_requests_provider_created_at') IS NOT NULL"
	var hasTable, hasIndex bool

	applyMigrationFile(t, db, paidLedgerDownMigration)

	if err := db.QueryRow(tableQuery).Scan(&hasTable, &hasIndex); (err != nil) || (hasTable == true) || (hasIndex == true) {
		t.Fatalf("expected no table after down, got %v %v %v", hasTable, hasIndex, err)
	}

	applyMigrationFile(t, db, paidLedgerUpMigration)

	if err := db.QueryRow(tableQuery).Scan(&hasTable, &hasIndex); (err != nil) || (hasTable == false) || (hasIndex == false) {
		t.Fatalf("expected the table and index after up, got %v %v %v", hasTable, hasIndex, err)
	}

	_, badFeature := db.Exec("INSERT INTO paid_requests (provider, feature, status_code, charged) VALUES ('hiker', 'video', 200, true)")

	if badFeature == nil {
		t.Fatalf("an unknown feature must be rejected")
	}
}

func TestHikerBudgetGuardReadsThePostgresLedger(t *testing.T) {
	setupPaidLedgerTestDB(t)
	t.Setenv("HIKERAPI_TOKEN", hikerTestKey)
	t.Setenv("HIKERAPI_MONTHLY_BUDGET_USD", "0.002")
	t.Setenv("HIKERAPI_PRICE_PER_REQUEST_USD", "0.001")
	fake := newFakeHiker(t)
	client := fake.client()

	_, first := client.get(context.Background(), paidFeatureStory, hikerPathStoryByURL, nil)
	_, second := client.get(context.Background(), paidFeatureStory, hikerPathStoryByURL, nil)
	_, third := client.get(context.Background(), paidFeatureStory, hikerPathStoryByURL, nil)

	if (first != nil) || (second != nil) || (errors.Is(third, errHikerBudget) == false) || (fake.callCount(hikerPathStoryByURL) != 2) {
		t.Fatalf("expected two requests then a refusal, got %v / %v / %v", first, second, third)
	}
}

func TestHikerBudgetFailsClosedWithoutTheLedgerTable(t *testing.T) {
	db := setupPaidLedgerTestDB(t)
	t.Setenv("HIKERAPI_TOKEN", hikerTestKey)
	fake := newFakeHiker(t)
	applyMigrationFile(t, db, paidLedgerDownMigration)

	_, err := fake.client().get(context.Background(), paidFeatureStory, hikerPathStoryByURL, nil)

	if (isPaidUnavailable(err) == false) || (isPaidBudgetError(err) == true) || (fake.callCount(hikerPathStoryByURL) != 0) {
		t.Fatalf("expected an unavailable skip without a request, got %v", err)
	}
}

// fetchThroughChain runs one restricted-post fetch for a private user
// through Apify then HikerAPI (scripted).
func fetchThroughChain(userID int64, apify, hiker GatedPostProvider) error {
	sd := &SmartDownloader{gated: &fallbackGatedProvider{providers: []GatedPostProvider{apify, hiker}}}
	gatedErr := &InstagramGatedError{Gate: InstagramGate{Restricted: true}}
	_, err := sd.FetchGatedPost(gatedTestPostURL, gatedErr, userID, userID, true)

	return err
}

func gatedUsage(t *testing.T, userID int64) (int, int) {
	userUsed, userErr := GetGatedFetchUsage(GatedFetchScopeUser, userID)
	globalUsed, globalErr := GetGatedFetchUsage(GatedFetchScopeGlobal, 0)

	if (userErr != nil) || (globalErr != nil) {
		t.Fatalf("usage: %v %v", userErr, globalErr)
	}

	return userUsed, globalUsed
}

func TestChainChargesOneQuotaUnitWhenHikerServesThePost(t *testing.T) {
	setupGatedFetchTestDB(t)
	apify := &scriptedGatedProvider{name: "apify", err: errors.New("apify status 500")}
	hiker := &scriptedGatedProvider{name: "hiker", media: oneSlide()}

	err := fetchThroughChain(300, apify, hiker)
	userUsed, globalUsed := gatedUsage(t, 300)

	if (err != nil) || (userUsed != 1) || (globalUsed != 1) {
		t.Fatalf("expected one unit used, got %v user=%d global=%d", err, userUsed, globalUsed)
	}
}

func TestChainUsesOneCreditWhenHikerServesThePost(t *testing.T) {
	setupGatedFetchTestDB(t)
	useUpAllowance(t, 301)
	recordTestPayment(t, "charge-chain-a", 301, 25)
	apify := &scriptedGatedProvider{name: "apify", err: fmt.Errorf("%w: test", errApifyBudget)}
	hiker := &scriptedGatedProvider{name: "hiker", media: oneSlide()}

	err := fetchThroughChain(301, apify, hiker)
	balance, _ := GetGatedFetchCredits(301)
	userUsed, globalUsed := gatedUsage(t, 301)

	if (err != nil) || (balance != 9) || (userUsed != 1) || (globalUsed != 1) {
		t.Fatalf("expected one credit used and the counters untouched, got %v balance=%d user=%d global=%d", err, balance, userUsed, globalUsed)
	}
}

func TestChainBudgetRefusalRefundsEverythingAndReturnsTheCredit(t *testing.T) {
	setupGatedFetchTestDB(t)
	apify := &scriptedGatedProvider{name: "apify", err: fmt.Errorf("%w: test", errApifyBudget)}
	hiker := &scriptedGatedProvider{name: "hiker", err: errHikerBudget}

	err := fetchThroughChain(302, apify, hiker)
	userUsed, globalUsed := gatedUsage(t, 302)
	var deniedErr *GatedFetchDeniedError

	if (errors.As(err, &deniedErr) == false) || (deniedErr.Denial != GatedFetchDeniedBudget) || (userUsed != 0) || (globalUsed != 0) {
		t.Fatalf("expected a budget denial with every unit back, got %v user=%d global=%d", err, userUsed, globalUsed)
	}

	useUpAllowance(t, 303)
	recordTestPayment(t, "charge-chain-b", 303, 25)
	fetchThroughChain(303, apify, hiker)
	balance, _ := GetGatedFetchCredits(303)

	if balance != 10 {
		t.Fatalf("expected the credit returned, got balance=%d", balance)
	}
}

func TestChainKeepsTheGlobalUnitWhenAProviderRan(t *testing.T) {
	setupGatedFetchTestDB(t)
	apify := &scriptedGatedProvider{name: "apify", err: errors.New("apify status 500")}
	hiker := &scriptedGatedProvider{name: "hiker", err: errHikerBudget}

	err := fetchThroughChain(304, apify, hiker)
	userUsed, globalUsed := gatedUsage(t, 304)
	var deniedErr *GatedFetchDeniedError

	if (errors.As(err, &deniedErr) == false) || (deniedErr.Denial == GatedFetchDeniedBudget) || (userUsed != 0) || (globalUsed != 1) {
		t.Fatalf("expected a non-budget denial, user refunded and global kept, got %v user=%d global=%d", err, userUsed, globalUsed)
	}

	useUpAllowance(t, 305)
	recordTestPayment(t, "charge-chain-c", 305, 25)
	fetchThroughChain(305, apify, hiker)
	balance, _ := GetGatedFetchCredits(305)

	if balance != 10 {
		t.Fatalf("expected the credit returned after a failed chain, got balance=%d", balance)
	}
}

func TestChainHiker404KeepsTheGlobalUnitWithoutAThirdCall(t *testing.T) {
	setupGatedFetchTestDB(t)
	ledger := useHikerTestEnv(t)
	fake := newFakeHiker(t)
	fake.status = http.StatusNotFound
	apify := &scriptedGatedProvider{name: "apify", err: errors.New("apify status 500")}
	hiker := newHikerTestProvider(t, fake)
	third := &scriptedGatedProvider{name: "third", media: oneSlide()}
	sd := &SmartDownloader{gated: &fallbackGatedProvider{providers: []GatedPostProvider{apify, hiker, third}}}
	gatedErr := &InstagramGatedError{Gate: InstagramGate{Restricted: true}}

	_, err := sd.FetchGatedPost(gatedTestPostURL, gatedErr, 306, 306, true)
	userUsed, globalUsed := gatedUsage(t, 306)
	rows := ledger.recorded()
	var deniedErr *GatedFetchDeniedError

	if (errors.As(err, &deniedErr) == false) || (deniedErr.Denial == GatedFetchDeniedBudget) || (userUsed != 0) || (globalUsed != 1) {
		t.Fatalf("expected the normal failure reply with global kept, got %v user=%d global=%d", err, userUsed, globalUsed)
	}

	if (third.calls != 0) || (fake.callCount(hikerPathMediaByCode) != 1) || (len(rows) != 1) || (rows[0].Charged == false) {
		t.Fatalf("expected one billed HikerAPI call and nothing after it, got third=%d rows=%+v", third.calls, rows)
	}
}

func TestUnreadableHikerLedgerRefundsEverythingWithoutABudgetReply(t *testing.T) {
	setupGatedFetchTestDB(t)
	ledger := useHikerTestEnv(t)
	ledger.readErr = errors.New("database down")
	fake := newFakeHiker(t)
	apifyBudget := &scriptedGatedProvider{name: "apify", err: fmt.Errorf("%w: test", errApifyBudget)}
	chains := map[string]GatedPostProvider{
		"hiker alone":              newHikerTestProvider(t, fake),
		"apify budget, then hiker": &fallbackGatedProvider{providers: []GatedPostProvider{apifyBudget, newHikerTestProvider(t, fake)}},
	}
	userID := int64(310)

	for name, provider := range chains {
		t.Run(name, func(t *testing.T) {
			userID++
			sd := &SmartDownloader{gated: provider}
			gatedErr := &InstagramGatedError{Gate: InstagramGate{Restricted: true}}
			_, globalBefore := gatedUsage(t, userID)

			_, err := sd.FetchGatedPost(gatedTestPostURL, gatedErr, userID, userID, true)
			userUsed, globalUsed := gatedUsage(t, userID)
			var deniedErr *GatedFetchDeniedError

			if (errors.As(err, &deniedErr) == false) || (deniedErr.Denial == GatedFetchDeniedBudget) || (userUsed != 0) || (globalUsed != globalBefore) {
				t.Fatalf("expected a non-budget denial with every unit back, got %v user=%d global=%d", err, userUsed, globalUsed)
			}

			userID++
			useUpAllowance(t, userID)
			recordTestPayment(t, fmt.Sprintf("charge-ledger-%d", userID), userID, 25)
			sd.FetchGatedPost(gatedTestPostURL, gatedErr, userID, userID, true)
			balance, _ := GetGatedFetchCredits(userID)

			if (balance != 10) || (fake.callCount(hikerPathMediaByCode) != 0) {
				t.Fatalf("expected the credit returned and no HikerAPI request, got balance=%d calls=%d", balance, fake.callCount(hikerPathMediaByCode))
			}
		})
	}
}
