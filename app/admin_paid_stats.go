package app

import (
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/timoffmax/social-link-bot/config"
	"github.com/timoffmax/social-link-bot/services"
)

// Lines of the owner's "Paid fetching" stats section.
const (
	paidStatsHeading            = "\n<b>💳 Paid fetching</b>\n"
	paidStatsFetchesTemplate    = "• Restricted posts this month (counted ones): <b>%d</b> of %s\n"
	paidStatsFetchesUnknown     = "• Restricted posts this month: <i>unavailable</i> (cap %s)\n"
	paidStatsApifyTemplate      = "• Apify this month: <b>$%.2f</b> of $%.2f\n"
	paidStatsApifyUnknown       = "• Apify budget: $%.2f <i>(usage unavailable)</i>\n"
	paidStatsStarsMonthTemplate = "• Stars this month: <b>%d</b> payments, <b>%d</b> ⭐\n"
	paidStatsStarsTotalTemplate = "• Stars all time: <b>%d</b> payments, <b>%d</b> ⭐\n"
	paidStatsCreditsTemplate    = "• Unused bonus downloads: <b>%d</b>\n"
	paidStatsStarsUnknown       = "• Stars support: <i>unavailable</i>\n"
)

// paidStatsReads is the number of parallel reads of loadPaidFetchingStats.
const paidStatsReads = 3

// paidFetchingStats are the values of the "Paid fetching" section; a non-nil
// error leaves its value out.
type paidFetchingStats struct {
	FetchesUsed  int
	FetchesErr   error
	FetchesCap   int
	ApifyUsedUsd float64
	ApifyErr     error
	ApifyBudget  float64
	Support      services.SupportStats
	SupportErr   error
}

// paidFetchingStatsText reads and formats the owner's "Paid fetching"
// section; every read fails soft.
func paidFetchingStatsText() string {
	stats := loadPaidFetchingStats()
	result := formatPaidFetchingStats(stats)

	return result
}

// loadPaidFetchingStats reads the counters, Apify usage and Stars totals in
// parallel, so a slow Apify answer doesn't add to the database reads.
func loadPaidFetchingStats() paidFetchingStats {
	var wg sync.WaitGroup
	result := paidFetchingStats{}
	result.FetchesCap = config.GetGatedFetchGlobalMonthlyCap()
	result.ApifyBudget = config.GetApifyMonthlyBudgetUsd()

	wg.Add(paidStatsReads)

	go func() {
		defer wg.Done()
		result.FetchesUsed, result.FetchesErr = services.GetGatedFetchUsage(services.GatedFetchScopeGlobal, services.GatedFetchGlobalScopeID)
	}()

	go func() {
		defer wg.Done()
		result.ApifyUsedUsd, result.ApifyErr = services.GetApifyMonthlyUsageUsd()
	}()

	go func() {
		defer wg.Done()
		result.Support, result.SupportErr = services.GetSupportStats()
	}()

	wg.Wait()

	logPaidStatsError("restricted-post usage", result.FetchesErr)
	logPaidStatsError("Apify usage", result.ApifyErr)
	logPaidStatsError("Stars support totals", result.SupportErr)

	return result
}

// logPaidStatsError logs a stats read that failed.
func logPaidStatsError(name string, err error) {
	if err != nil {
		log.Printf("⚠️ [adminstats] %s unavailable: %v", name, err)
	}
}

// formatPaidFetchingStats renders the "Paid fetching" section.
func formatPaidFetchingStats(stats paidFetchingStats) string {
	var text strings.Builder
	capText := globalCapText(stats.FetchesCap)
	fetchesLine := fmt.Sprintf(paidStatsFetchesTemplate, stats.FetchesUsed, capText)
	apifyLine := fmt.Sprintf(paidStatsApifyTemplate, stats.ApifyUsedUsd, stats.ApifyBudget)
	supportLines := formatSupportStats(stats.Support)

	if stats.FetchesErr != nil {
		fetchesLine = fmt.Sprintf(paidStatsFetchesUnknown, capText)
	}

	if stats.ApifyErr != nil {
		apifyLine = fmt.Sprintf(paidStatsApifyUnknown, stats.ApifyBudget)
	}

	if stats.SupportErr != nil {
		supportLines = paidStatsStarsUnknown
	}

	text.WriteString(paidStatsHeading)
	text.WriteString(fetchesLine)
	text.WriteString(apifyLine)
	text.WriteString(supportLines)

	return text.String()
}

// formatSupportStats renders the Stars totals and unused bonus downloads.
func formatSupportStats(support services.SupportStats) string {
	var text strings.Builder

	fmt.Fprintf(&text, paidStatsStarsMonthTemplate, support.MonthPayments, support.MonthStars)
	fmt.Fprintf(&text, paidStatsStarsTotalTemplate, support.TotalPayments, support.TotalStars)
	fmt.Fprintf(&text, paidStatsCreditsTemplate, support.OutstandingCredits)

	return text.String()
}
