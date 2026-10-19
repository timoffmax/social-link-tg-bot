package services

// The 'paid' bot_runtime_status component: what Apify and HikerAPI may still
// spend, by the bot's own budget rules. A refresher goroutine computes it
// from cached values and one ledger query; the settings goroutine only
// publishes the last snapshot, so it never waits for I/O.

import (
	"context"
	"errors"
	"log"
	"slices"
	"sync/atomic"
	"time"

	"github.com/timoffmax/social-link-bot/config"
)

// Paid service states and reasons, by the bot's own budget rules.
const (
	PaidStateOn      = "on"
	PaidStatePaused  = "paused"
	PaidStateOff     = "off"
	PaidStateUnknown = "unknown"
)

const (
	PaidReasonNoToken         = "no_token"
	PaidReasonNotListed       = "not_listed" // not in INSTAGRAM_PAID_PROVIDERS (Apify still serves Shorts and stories)
	PaidReasonBudgetZero      = "budget_zero"
	PaidReasonBudgetUsed      = "budget_used"
	PaidReasonUsageUnreadable = "usage_unreadable" // the last Apify usage read failed: runs are refused while it fails
	PaidReasonUsageNotRead    = "usage_not_read"   // no read since start yet
	PaidReasonBalanceEmpty    = "balance_empty"    // HikerAPI 402: skipped until a recheck shows a top-up
	PaidReasonLedgerError     = "ledger_error"
	PaidReasonNoDatabase      = "no_database" // HikerAPI's budget needs the ledger: without a database it never runs
)

// Apify features and their cost caps (the existing constants).
const (
	PaidFeaturePosts   = "posts"
	PaidFeatureStories = "stories"
	PaidFeatureShorts  = "shorts"
)

// paidStatusInterval is how often the refresher recomputes the state.
const paidStatusInterval = time.Minute

// apifyFeatureOrder is the order the paused features are listed in.
var apifyFeatureOrder = []string{PaidFeaturePosts, PaidFeatureStories, PaidFeatureShorts}

// apifyFeatureCaps is the most one run of each feature may cost.
var apifyFeatureCaps = map[string]float64{
	PaidFeaturePosts:   apifyPostMaxChargeUsd,
	PaidFeatureStories: storiesApifyMaxChargeUsd,
	PaidFeatureShorts:  youtubeApifyMaxChargeUsd,
}

// PaidServicesStatus is the 'paid' component.
type PaidServicesStatus struct {
	ComputedAt time.Time          `json:"computed_at"`
	Order      []string           `json:"order"`
	Apify      ApifyServiceStatus `json:"apify"`
	Hiker      HikerServiceStatus `json:"hiker"`
}

// ApifyServiceStatus is Apify's part: the last usage read and its state.
type ApifyServiceStatus struct {
	HasToken        bool       `json:"has_token"`
	IsListed        bool       `json:"is_listed"`
	BudgetUsd       float64    `json:"budget_usd"`
	UsedUsd         *float64   `json:"used_usd"`
	InFlightUsd     float64    `json:"in_flight_usd"`
	UsageReadAt     *time.Time `json:"usage_read_at"`
	UsageError      string     `json:"usage_error,omitempty"`
	UsageErrorAt    *time.Time `json:"usage_error_at"`
	AccountLimitUsd *float64   `json:"account_limit_usd"`
	CycleStart      *time.Time `json:"cycle_start"`
	CycleEnd        *time.Time `json:"cycle_end"`
	State           string     `json:"state"`
	Reason          string     `json:"reason,omitempty"`
	PausedFeatures  []string   `json:"paused_features"`
}

// HikerServiceStatus is HikerAPI's part: this month's ledger, the last
// balance read and its state.
type HikerServiceStatus struct {
	HasToken           bool       `json:"has_token"`
	IsListed           bool       `json:"is_listed"`
	BudgetUsd          float64    `json:"budget_usd"`
	PricePerRequestUsd float64    `json:"price_per_request_usd"`
	SpentUsd           *float64   `json:"spent_usd"`
	InFlightUsd        float64    `json:"in_flight_usd"`
	Calls              int        `json:"calls"`
	ChargedCalls       int        `json:"charged_calls"`
	GatedCalls         int        `json:"gated_calls"`
	StoryCalls         int        `json:"story_calls"`
	BalanceRequests    *int       `json:"balance_requests"`
	BalanceReadAt      *time.Time `json:"balance_read_at"`
	Exhausted          bool       `json:"exhausted"`
	State              string     `json:"state"`
	Reason             string     `json:"reason,omitempty"`
	hasNoDatabase      bool
}

// paidStatusSnapshot is the last computed state, read by the heartbeat.
var paidStatusSnapshot atomic.Pointer[PaidServicesStatus]

// paidStatusLedgerFailing is set while the ledger read fails, so the
// failure is logged once per outage.
var paidStatusLedgerFailing atomic.Bool

// paidStatusNow is the refresher's clock (a variable for tests).
var paidStatusNow = time.Now

// CurrentPaidServicesStatus returns the last computed state, or nil before
// the first compute; it never blocks.
func CurrentPaidServicesStatus() any {
	var result any
	snapshot := paidStatusSnapshot.Load()

	if snapshot != nil {
		result = *snapshot
	}

	return result
}

// StartPaidStatusRefresher computes the state at once, then every
// paidStatusInterval until ctx ends.
func StartPaidStatusRefresher(ctx context.Context) {
	go runPaidStatusRefresher(ctx)
	log.Println("Started paid status refresher")
}

// runPaidStatusRefresher is the refresher's loop.
func runPaidStatusRefresher(ctx context.Context) {
	ticker := time.NewTicker(paidStatusInterval)
	defer ticker.Stop()
	isRunning := true
	refreshPaidStatus(ctx)

	for isRunning == true {
		select {
		case <-ctx.Done():
			isRunning = false
		case <-ticker.C:
			refreshPaidStatus(ctx)
		}
	}
}

// refreshPaidStatus computes and stores one snapshot.
func refreshPaidStatus(ctx context.Context) {
	status := computePaidServicesStatus(ctx, paidLedger)
	paidStatusSnapshot.Store(&status)
}

// computePaidServicesStatus builds the state from the caches and, with a
// HikerAPI token, one ledger read; it calls no provider.
func computePaidServicesStatus(ctx context.Context, ledger paidRequestLedger) PaidServicesStatus {
	order := instagramPaidProviderOrder()
	apify := apifyServiceStatus(order)
	hiker := hikerServiceStatus(ctx, ledger, order)
	computedAt := paidStatusNow()
	result := PaidServicesStatus{ComputedAt: computedAt, Order: order, Apify: apify, Hiker: hiker}

	return result
}

// apifyServiceStatus reads the Apify usage cache and the runs in flight.
func apifyServiceStatus(order []string) ApifyServiceStatus {
	result := ApifyServiceStatus{
		HasToken:  hasApifyToken(),
		IsListed:  slices.Contains(order, PaidProviderApify),
		BudgetUsd: config.GetApifyMonthlyBudgetUsd(),
	}
	result = withApifyUsageCache(result)

	apifyInFlight.Lock()
	result.InFlightUsd = apifyInFlight.usd
	apifyInFlight.Unlock()

	result.State, result.Reason, result.PausedFeatures = apifyServiceState(result)

	return result
}

// withApifyUsageCache copies the last usage read into status; a read from
// a billing cycle that has ended since counts as no read.
func withApifyUsageCache(status ApifyServiceStatus) ApifyServiceStatus {
	result := status
	now := paidStatusNow()

	apifyUsageCache.Lock()
	defer apifyUsageCache.Unlock()

	cycleEnd := apifyUsageCache.cycleEnd
	isCycleOver := (cycleEnd != nil) && (now.After(*cycleEnd) == true)
	isUsageKnown := (apifyUsageCache.readAt.IsZero() == false) && (isCycleOver == false)

	if isUsageKnown == true {
		used := apifyUsageCache.usd
		readAt := apifyUsageCache.readAt
		result.UsedUsd = &used
		result.UsageReadAt = &readAt
		result.AccountLimitUsd = apifyUsageCache.accountLimitUsd
		result.CycleStart = apifyUsageCache.cycleStart
		result.CycleEnd = apifyUsageCache.cycleEnd
	}

	if apifyUsageCache.errAt.IsZero() == false {
		errAt := apifyUsageCache.errAt
		result.UsageError = apifyUsageCache.errText
		result.UsageErrorAt = &errAt
	}

	return result
}

// apifyServiceState follows reserveBudget's rule: off without a token,
// paused at a zero budget or while the last usage read failed, unknown
// before the first read, else each feature whose next run no longer fits
// is paused (all paused: the budget is used).
func apifyServiceState(status ApifyServiceStatus) (string, string, []string) {
	paused := apifyPausedFeatures(status)
	isUnreadable := isApifyUsageUnreadable(status)
	state := PaidStateOn
	reason := ""

	switch {
	case status.HasToken == false:
		state, reason = PaidStateOff, PaidReasonNoToken
	case status.BudgetUsd <= 0:
		state, reason = PaidStatePaused, PaidReasonBudgetZero
	case isUnreadable == true:
		state, reason = PaidStatePaused, PaidReasonUsageUnreadable
	case status.UsedUsd == nil:
		state, reason = PaidStateUnknown, PaidReasonUsageNotRead
	case len(paused) == len(apifyFeatureOrder):
		state, reason = PaidStatePaused, PaidReasonBudgetUsed
	}

	return state, reason, paused
}

// isApifyUsageUnreadable reports a last usage read that failed (newer than
// the last success, or no success at all).
func isApifyUsageUnreadable(status ApifyServiceStatus) bool {
	hasError := (status.UsageErrorAt != nil)
	result := (hasError == true) && ((status.UsageReadAt == nil) || (status.UsageErrorAt.After(*status.UsageReadAt) == true))

	return result
}

// apifyPausedFeatures lists the features whose next run would take the
// known usage plus the runs in flight over the budget.
func apifyPausedFeatures(status ApifyServiceStatus) []string {
	result := []string{}

	for _, feature := range apifyFeatureOrder {
		isOver := (status.UsedUsd != nil) && ((*status.UsedUsd + status.InFlightUsd + apifyFeatureCaps[feature]) > status.BudgetUsd)

		if isOver == true {
			result = append(result, feature)
		}
	}

	return result
}

// hikerServiceStatus reads the ledger (with a token only), the budget
// state and the balance cache.
func hikerServiceStatus(ctx context.Context, ledger paidRequestLedger, order []string) HikerServiceStatus {
	result := HikerServiceStatus{
		HasToken:           hasHikerToken(),
		IsListed:           slices.Contains(order, PaidProviderHiker),
		BudgetUsd:          config.GetHikerAPIMonthlyBudgetUsd(),
		PricePerRequestUsd: config.GetHikerAPIPricePerRequestUsd(),
		Exhausted:          isHikerBalanceExhausted(),
	}

	if result.HasToken == true {
		result = withHikerLedger(ctx, ledger, result)
	}

	result = withHikerBalanceCache(result)
	result.State, result.Reason = hikerServiceState(result)

	return result
}

// withHikerLedger adds this month's ledger totals, the unrecorded costs and
// the requests in flight; a failed read leaves SpentUsd nil.
func withHikerLedger(ctx context.Context, ledger paidRequestLedger, status HikerServiceStatus) HikerServiceStatus {
	result := status
	monthStart := hikerMonthStart()
	ledgerCtx, cancel := context.WithTimeout(ctx, paidLedgerTimeout)
	stats, err := ledger.MonthStats(ledgerCtx, hikerServiceName, monthStart)
	cancel()
	result.hasNoDatabase = errors.Is(err, errGatedFetchNoDatabase)

	if result.hasNoDatabase == false {
		logPaidLedgerResult(err)
	}

	hikerBudget.Lock()
	unrecorded := hikerUnrecordedUsdLocked(monthStart)
	result.InFlightUsd = hikerBudget.inFlightUsd
	hikerBudget.Unlock()

	if err == nil {
		spent := (stats.CostUsd + unrecorded)
		result.SpentUsd = &spent
		result.Calls = stats.Calls
		result.ChargedCalls = stats.ChargedCalls
		result.GatedCalls = stats.GatedCalls
		result.StoryCalls = stats.StoryCalls
	}

	return result
}

// logPaidLedgerResult logs the first failed ledger read of an outage and
// the recovery.
func logPaidLedgerResult(err error) {
	wasFailing := paidStatusLedgerFailing.Swap(err != nil)

	if (err != nil) && (wasFailing == false) {
		log.Printf("⚠️ [paid] ledger read for the paid status failed: %v", err)
	}

	if (err == nil) && (wasFailing == true) {
		log.Printf("✅ [paid] ledger readable again")
	}
}

// withHikerBalanceCache adds the last balance read.
func withHikerBalanceCache(status HikerServiceStatus) HikerServiceStatus {
	result := status

	hikerBalanceState.Lock()
	defer hikerBalanceState.Unlock()

	if hikerBalanceState.isBalanceRead == true {
		requests := hikerBalanceState.lastRequests
		readAt := hikerBalanceState.lastReadAt
		result.BalanceRequests = &requests
		result.BalanceReadAt = &readAt
	}

	return result
}

// hikerServiceState follows reserveHikerBudget's rule: off without a token
// or outside the provider order, paused at a zero budget or an empty
// balance, unknown when the ledger could not be read, paused when the next
// request no longer fits the budget.
func hikerServiceState(status HikerServiceStatus) (string, string) {
	state := PaidStateOn
	reason := ""
	isOver := (status.SpentUsd != nil) && ((*status.SpentUsd + status.InFlightUsd + status.PricePerRequestUsd) > (status.BudgetUsd + hikerBudgetEpsilon))

	switch {
	case status.HasToken == false:
		state, reason = PaidStateOff, PaidReasonNoToken
	case status.IsListed == false:
		state, reason = PaidStateOff, PaidReasonNotListed
	case status.BudgetUsd <= 0:
		state, reason = PaidStatePaused, PaidReasonBudgetZero
	case status.Exhausted == true:
		state, reason = PaidStatePaused, PaidReasonBalanceEmpty
	case status.hasNoDatabase == true:
		state, reason = PaidStatePaused, PaidReasonNoDatabase
	case status.SpentUsd == nil:
		state, reason = PaidStateUnknown, PaidReasonLedgerError
	case isOver == true:
		state, reason = PaidStatePaused, PaidReasonBudgetUsed
	}

	return state, reason
}
