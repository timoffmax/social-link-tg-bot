package services

import (
	"errors"
	"testing"
	"time"
)

func TestSupportStatsFailWithoutDatabase(t *testing.T) {
	previous := dbService
	dbService = nil

	t.Cleanup(func() {
		dbService = previous
	})

	stats, err := GetSupportStats()

	if (errors.Is(err, errGatedFetchNoDatabase) == false) || (stats != SupportStats{}) {
		t.Fatalf("expected the no-database error and empty stats, got %+v %v", stats, err)
	}
}

func TestApifyMonthlyUsageFailsWithoutToken(t *testing.T) {
	t.Setenv("APIFY_TOKEN", "")

	_, err := GetApifyMonthlyUsageUsd()

	if errors.Is(err, errApifyNotConfigured) == false {
		t.Fatalf("expected errApifyNotConfigured, got %v", err)
	}
}

func TestCurrentMonthStartUTCMatchesTheGatedFetchPeriod(t *testing.T) {
	start := currentMonthStartUTC()
	period := start.Format(gatedFetchPeriodLayout)

	if (start.Day() != 1) || (start.Hour() != 0) || (start.Location() != time.UTC) || (period != gatedFetchPeriod()) {
		t.Fatalf("unexpected month start %v (period %s)", start, gatedFetchPeriod())
	}
}

func TestSupportStatsCountOnlyUnrefundedPaymentsAndUnusedCredits(t *testing.T) {
	setupGatedFetchTestDB(t)

	recordTestPayment(t, "charge-month", 200, 25)
	recordTestPayment(t, "charge-old", 200, 100)
	recordTestPayment(t, "charge-refunded", 201, 50)
	recordTestPayment(t, "charge-other", 202, 50)

	db := GetDB()
	lastMonth := currentMonthStartUTC().AddDate(0, 0, -1)

	if _, err := db.Exec(`UPDATE support_payments SET created_at = $1 WHERE telegram_payment_charge_id = 'charge-old'`, lastMonth); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	if _, err := MarkSupportPaymentRefunded(201, "charge-refunded"); err != nil {
		t.Fatalf("refund: %v", err)
	}

	if isTaken, _, err := TakeGatedFetchCredit(202); (err != nil) || (isTaken == false) {
		t.Fatalf("take credit: %v %v", isTaken, err)
	}

	stats, err := GetSupportStats()
	expected := SupportStats{MonthPayments: 2, MonthStars: 75, TotalPayments: 3, TotalStars: 175, OutstandingCredits: 94}

	if (err != nil) || (stats != expected) {
		t.Fatalf("expected %+v, got %+v %v", expected, stats, err)
	}
}

func TestSupportStatsAreZeroWithoutPayments(t *testing.T) {
	setupGatedFetchTestDB(t)

	stats, err := GetSupportStats()

	if (err != nil) || (stats != SupportStats{}) {
		t.Fatalf("expected zero stats, got %+v %v", stats, err)
	}
}
