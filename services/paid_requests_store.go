package services

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Features of a paid request in the ledger (paid_requests.feature).
const (
	paidFeatureGated = "gated"
	paidFeatureStory = "story"
)

// paidLedgerTimeout bounds one ledger read or write.
const paidLedgerTimeout = 5 * time.Second

// PaidRequest is one row of the paid-request ledger.
type PaidRequest struct {
	Provider   string
	Feature    string
	StatusCode int
	Charged    bool
	CostUsd    float64
}

// PaidMonthStats sums one provider's ledger rows of this UTC month.
type PaidMonthStats struct {
	Calls        int
	ChargedCalls int
	GatedCalls   int
	StoryCalls   int
	CostUsd      float64
}

// paidRequestLedger stores paid requests; an interface so tests can run the
// budget guard without a database.
type paidRequestLedger interface {
	Record(ctx context.Context, request PaidRequest) error
	MonthSpentUsd(ctx context.Context, provider string, monthStart time.Time) (float64, error)
}

// pgPaidLedger is the PostgreSQL ledger (migration 010).
type pgPaidLedger struct{}

// paidLedger is the ledger the paid clients use.
var paidLedger paidRequestLedger = pgPaidLedger{}

// Record implements paidRequestLedger.
func (pgPaidLedger) Record(ctx context.Context, request PaidRequest) error {
	db := GetDB()
	err := errGatedFetchNoDatabase
	query := `INSERT INTO paid_requests (provider, feature, status_code, charged, cost_usd) VALUES ($1, $2, $3, $4, $5)`

	if db != nil {
		_, err = db.ExecContext(ctx, query, request.Provider, request.Feature, request.StatusCode, request.Charged, request.CostUsd)
	}

	if err != nil {
		err = fmt.Errorf("failed to record a paid request: %w", err)
	}

	return err
}

// MonthSpentUsd implements paidRequestLedger: the provider's cost since
// monthStart.
func (pgPaidLedger) MonthSpentUsd(ctx context.Context, provider string, monthStart time.Time) (float64, error) {
	result := 0.0
	db := GetDB()
	err := errGatedFetchNoDatabase
	query := `SELECT COALESCE(SUM(cost_usd), 0)::float8 FROM paid_requests WHERE provider = $1 AND created_at >= $2`

	if db != nil {
		row := db.QueryRowContext(ctx, query, provider, monthStart)
		err = row.Scan(&result)
	}

	if err != nil {
		err = fmt.Errorf("failed to read paid spend: %w", err)
	}

	return result, err
}

// GetPaidMonthStats returns one provider's ledger totals of this UTC month.
func GetPaidMonthStats(provider string) (PaidMonthStats, error) {
	result := PaidMonthStats{}
	db := GetDB()
	err := errGatedFetchNoDatabase
	monthStart := currentMonthStartUTC()
	background := context.Background()
	ctx, cancel := context.WithTimeout(background, paidLedgerTimeout)
	query := `
		SELECT
			COUNT(*),
			COUNT(*) FILTER (WHERE charged),
			COUNT(*) FILTER (WHERE feature = $3),
			COUNT(*) FILTER (WHERE feature = $4),
			COALESCE(SUM(cost_usd), 0)::float8
		FROM paid_requests WHERE provider = $1 AND created_at >= $2`

	defer cancel()

	if db != nil {
		row := db.QueryRowContext(ctx, query, provider, monthStart, paidFeatureGated, paidFeatureStory)
		err = scanPaidMonthStats(row, &result)
	}

	return result, err
}

// scanPaidMonthStats reads the totals row of GetPaidMonthStats.
func scanPaidMonthStats(row *sql.Row, stats *PaidMonthStats) error {
	err := row.Scan(&stats.Calls, &stats.ChargedCalls, &stats.GatedCalls, &stats.StoryCalls, &stats.CostUsd)

	if err != nil {
		err = fmt.Errorf("failed to read paid request stats: %w", err)
	}

	return err
}
