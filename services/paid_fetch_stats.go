package services

import (
	"context"
	"time"
)

// apifyUsageLookupTimeout bounds the read-only Apify usage lookup of the
// owner stats, so a slow Apify never holds up /adminstats for long.
const apifyUsageLookupTimeout = 5 * time.Second

// SupportStats sums the Stars support payments that weren't refunded, plus
// the bonus credits still unused.
type SupportStats struct {
	MonthPayments      int
	MonthStars         int
	TotalPayments      int
	TotalStars         int
	OutstandingCredits int
}

// GetSupportStats returns the support totals of this UTC month and of all
// time (refunded payments excluded) and the unused bonus credits.
func GetSupportStats() (SupportStats, error) {
	result := SupportStats{}
	db := GetDB()
	err := errGatedFetchNoDatabase
	monthStart := currentMonthStartUTC()
	background := context.Background()
	ctx, cancel := context.WithTimeout(background, supportStoreTimeout)
	paymentsQuery := `
		SELECT
			COUNT(*) FILTER (WHERE created_at >= $1),
			COALESCE(SUM(stars) FILTER (WHERE created_at >= $1), 0),
			COUNT(*),
			COALESCE(SUM(stars), 0)
		FROM support_payments WHERE refunded_at IS NULL`
	creditsQuery := `SELECT (SELECT COALESCE(SUM(credits), 0) FROM gated_fetch_credits) + (SELECT COALESCE(SUM(credits), 0) FROM gated_fetch_chat_credits)`

	defer cancel()

	if db != nil {
		row := db.QueryRowContext(ctx, paymentsQuery, monthStart)
		err = row.Scan(&result.MonthPayments, &result.MonthStars, &result.TotalPayments, &result.TotalStars)
	}

	if err == nil {
		row := db.QueryRowContext(ctx, creditsQuery)
		err = row.Scan(&result.OutstandingCredits)
	}

	return result, err
}

// currentMonthStartUTC returns midnight of the first day of this UTC month,
// the same month gatedFetchPeriod counts.
func currentMonthStartUTC() time.Time {
	now := time.Now().UTC()
	year := now.Year()
	month := now.Month()
	result := time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)

	return result
}

// GetApifyMonthlyUsageUsd reads this month's Apify spend (read-only; nothing
// is reserved). It fails without APIFY_TOKEN or when Apify doesn't answer in
// time.
func GetApifyMonthlyUsageUsd() (float64, error) {
	result := 0.0
	err := errApifyNotConfigured
	client := newApifyClient()
	background := context.Background()
	ctx, cancel := context.WithTimeout(background, apifyUsageLookupTimeout)

	defer cancel()

	if client != nil {
		result, err = client.monthlyUsage(ctx)
	}

	return result, err
}
