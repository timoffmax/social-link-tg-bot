package app

import (
	"errors"
	"strings"
	"testing"

	"github.com/timoffmax/social-link-bot/services"
)

func TestFormatPaidFetchingStatsShowsEveryValue(t *testing.T) {
	stats := paidFetchingStats{
		FetchesUsed:  12,
		FetchesCap:   100,
		ApifyUsedUsd: 1.234,
		ApifyBudget:  4,
		Support:      services.SupportStats{MonthPayments: 2, MonthStars: 75, TotalPayments: 5, TotalStars: 250, OutstandingCredits: 40},
	}
	expected := `
<b>💳 Paid fetching</b>
• Restricted posts this month (counted ones): <b>12</b> of 100
• Apify this month: <b>$1.23</b> of $4.00
• Stars this month: <b>2</b> payments, <b>75</b> ⭐
• Stars all time: <b>5</b> payments, <b>250</b> ⭐
• Unused bonus downloads: <b>40</b>
`

	if text := formatPaidFetchingStats(stats); text != expected {
		t.Fatalf("unexpected section:\n%s", text)
	}
}

func TestFormatPaidFetchingStatsFailsSoft(t *testing.T) {
	failure := errors.New("down")
	stats := paidFetchingStats{FetchesErr: failure, FetchesCap: 100, ApifyErr: failure, ApifyBudget: 4, SupportErr: failure}
	text := formatPaidFetchingStats(stats)

	for _, line := range []string{"<i>unavailable</i> (cap 100)", "$4.00 <i>(usage unavailable)</i>", "Stars support: <i>unavailable</i>"} {
		if strings.Contains(text, line) == false {
			t.Fatalf("expected %q in:\n%s", line, text)
		}
	}
}

func TestPaidFetchingStatsWithoutDatabaseOrApifyFailSoft(t *testing.T) {
	t.Setenv("APIFY_TOKEN", "")

	text := paidFetchingStatsText()

	if (strings.Contains(text, "Paid fetching") == false) || (strings.Contains(text, "Stars support: <i>unavailable</i>") == false) {
		t.Fatalf("unexpected section:\n%s", text)
	}
}

func TestFormatPaidFetchingStatsShowsHikerSpendAndBalance(t *testing.T) {
	stats := paidFetchingStats{
		FetchesCap:   100,
		ApifyBudget:  4,
		HikerOn:      true,
		HikerBudget:  1,
		HikerStats:   services.PaidMonthStats{Calls: 42, ChargedCalls: 42, GatedCalls: 3, StoryCalls: 39, CostUsd: 0.042},
		HikerBalance: services.HikerBalance{Requests: 58},
	}
	line := "• HikerAPI this month: <b>$0.042</b> of $1.00 (42 calls, 3 gated, 39 stories); balance: 58 requests left\n"

	if text := formatPaidFetchingStats(stats); strings.Contains(text, line) == false {
		t.Fatalf("expected %q in:\n%s", line, text)
	}

	failure := errors.New("down")
	stats.HikerStatsErr = failure
	stats.HikerBalanceErr = failure
	failedLine := "• HikerAPI this month: <i>unavailable</i> (budget $1.00); balance <i>unavailable</i>\n"

	if text := formatPaidFetchingStats(stats); strings.Contains(text, failedLine) == false {
		t.Fatalf("expected %q in:\n%s", failedLine, text)
	}

	stats.HikerOn = false

	if text := formatPaidFetchingStats(stats); strings.Contains(text, "HikerAPI") == true {
		t.Fatalf("the HikerAPI line must be hidden without a token:\n%s", text)
	}
}

func TestPaidFetchingStatsWithoutHikerTokenHideHiker(t *testing.T) {
	t.Setenv("APIFY_TOKEN", "")
	t.Setenv("HIKERAPI_TOKEN", "")

	if text := paidFetchingStatsText(); strings.Contains(text, "HikerAPI") == true {
		t.Fatalf("unexpected HikerAPI line:\n%s", text)
	}
}

// stubHikerBalance replaces the HikerAPI balance read for one test.
func stubHikerBalance(t *testing.T, balance services.HikerBalance, err error) {
	t.Helper()
	previous := readHikerBalance
	readHikerBalance = func() (services.HikerBalance, error) {
		return balance, err
	}

	t.Cleanup(func() {
		readHikerBalance = previous
	})
}

func TestPaidFetchingStatsWithHikerTokenFailSoftWithoutDatabase(t *testing.T) {
	t.Setenv("APIFY_TOKEN", "")
	t.Setenv("HIKERAPI_TOKEN", "test-key")
	t.Setenv("HIKERAPI_MONTHLY_BUDGET_USD", "0")
	stubHikerBalance(t, services.HikerBalance{}, errors.New("balance down"))

	text := paidFetchingStatsText()

	if (strings.Contains(text, "HikerAPI this month: <i>unavailable</i>") == false) || (strings.Contains(text, "balance <i>unavailable</i>") == false) {
		t.Fatalf("expected the HikerAPI line to fail soft:\n%s", text)
	}
}

func TestPaidFetchingStatsShowHikerBalanceWhileTheBudgetIsPaused(t *testing.T) {
	t.Setenv("APIFY_TOKEN", "")
	t.Setenv("HIKERAPI_TOKEN", "test-key")
	t.Setenv("HIKERAPI_MONTHLY_BUDGET_USD", "0")
	stubHikerBalance(t, services.HikerBalance{Requests: 4321}, nil)

	text := paidFetchingStatsText()

	if strings.Contains(text, "balance: 4321 requests left") == false {
		t.Fatalf("expected the balance with a paused budget:\n%s", text)
	}
}
