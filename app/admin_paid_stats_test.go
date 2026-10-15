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
