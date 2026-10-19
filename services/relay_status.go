package services

// The 'relay' bot_runtime_status component: the home relay's last /healthz
// answer and the history of the bot's relay calls. A refresher goroutine
// reads /healthz (no token, no platform traffic) and the history every
// minute and prunes the history once a day; the settings goroutine only
// publishes the last snapshot. It feeds neither the outage tracker nor the
// hourly probe.

import (
	"context"
	"errors"
	"log"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/timoffmax/social-link-bot/config"
)

// Relay status timing and limits.
const (
	relayStatusInterval   = time.Minute
	relayPruneInterval    = 24 * time.Hour
	relayStatusErrorLimit = 200
)

// RelayStatus is the 'relay' component.
type RelayStatus struct {
	IsConfigured   bool                           `json:"configured"`
	PlatformsInUse []string                       `json:"platforms_in_use"`
	CheckedAt      *time.Time                     `json:"checked_at"`
	IsReachable    bool                           `json:"reachable"`
	Error          string                         `json:"error"`
	YtDlp          string                         `json:"yt_dlp"`
	BreakerOpen    []string                       `json:"breaker_open"`
	Global         RelayCounters                  `json:"global"`
	Platforms      map[string]RelayPlatformStatus `json:"platforms"`
	History        *RelayHistory                  `json:"history"`
}

// RelayCounters are the relay's calls this hour and day against its limits.
type RelayCounters struct {
	Hour        int `json:"hour"`
	Day         int `json:"day"`
	HourlyLimit int `json:"hourly_limit"`
	DailyLimit  int `json:"daily_limit"`
}

// RelayPlatformStatus is one platform of the relay's /healthz.
type RelayPlatformStatus struct {
	IsEnabled    bool   `json:"enabled"`
	Breaker      string `json:"breaker"`
	BreakerLeftS int    `json:"breaker_left_s"`
	Hour         int    `json:"hour"`
	Day          int    `json:"day"`
	HourlyLimit  int    `json:"hourly_limit"`
	DailyLimit   int    `json:"daily_limit"`
}

// relayStatusSnapshot is the last computed state, read by the heartbeat.
var relayStatusSnapshot atomic.Pointer[RelayStatus]

// relayStatusNow is the refresher's clock (a variable for tests).
var relayStatusNow = time.Now

// relayStatusRefresher is the state the refresher keeps between rounds.
type relayStatusRefresher struct {
	last        RelayStatus
	prunedAt    time.Time
	isHistoryOK bool
}

// CurrentRelayStatus returns the last computed state, or nil before the
// first compute; it never blocks.
func CurrentRelayStatus() any {
	var result any
	snapshot := relayStatusSnapshot.Load()

	if snapshot != nil {
		result = *snapshot
	}

	return result
}

// StartRelayStatusRefresher computes the state at once, then every
// relayStatusInterval until ctx ends.
func StartRelayStatusRefresher(ctx context.Context) {
	go runRelayStatusRefresher(ctx)
	log.Println("Started relay status refresher")
}

// runRelayStatusRefresher is the refresher's loop.
func runRelayStatusRefresher(ctx context.Context) {
	ticker := time.NewTicker(relayStatusInterval)
	defer ticker.Stop()
	refresher := &relayStatusRefresher{isHistoryOK: true}
	isRunning := true
	refresher.refresh(ctx)

	for isRunning == true {
		select {
		case <-ctx.Done():
			isRunning = false
		case <-ticker.C:
			refresher.refresh(ctx)
		}
	}
}

// refresh computes and stores one snapshot, pruning the history when due.
func (r *relayStatusRefresher) refresh(ctx context.Context) {
	now := relayStatusNow()
	r.pruneIfDue(ctx, now)
	status := r.compute(ctx, now)
	r.last = status
	relayStatusSnapshot.Store(&status)
}

// compute builds the state: the configuration, a /healthz read when the
// relay is configured (the last answer kept when one fails) and the history.
func (r *relayStatusRefresher) compute(ctx context.Context, now time.Time) RelayStatus {
	result := r.last
	result.IsConfigured = config.IsRelayConfigured()
	result.PlatformsInUse = relayEnabledPlatforms()
	client := newRelayClient("")

	if client != nil {
		health, err := client.readHealth()
		result = withRelayHealth(result, health, err, now)
	}

	if client == nil {
		result = RelayStatus{IsConfigured: result.IsConfigured, PlatformsInUse: result.PlatformsInUse}
	}

	result = withRelayDefaults(result)
	result.History = r.readHistory(ctx, now)

	return result
}

// withRelayHealth applies one /healthz read: a decoded answer replaces the
// last one (also ok=false), a failure keeps it; either way checked_at,
// reachable and error describe this read.
func withRelayHealth(status RelayStatus, health relayHealth, err error, now time.Time) RelayStatus {
	result := status
	isDecoded := (err == nil) || (errors.Is(err, errRelayHealthNotOK) == true)
	checkedAt := now
	result.CheckedAt = &checkedAt
	result.IsReachable = (err == nil)
	result.Error = ""

	if isDecoded == true {
		result.YtDlp = health.YtDlp
		result.BreakerOpen = health.BreakerOpen
		result.Global = RelayCounters(health.Global)
		result.Platforms = relayPlatformStatuses(health.Platforms)
	}

	if err != nil {
		result.Error = shortRelayStatusError(err)
	}

	return result
}

// relayPlatformStatuses copies the platforms of a /healthz answer.
func relayPlatformStatuses(platforms map[string]relayPlatformHealth) map[string]RelayPlatformStatus {
	result := make(map[string]RelayPlatformStatus, len(platforms))

	for name, platform := range platforms {
		result[name] = RelayPlatformStatus{
			IsEnabled:    platform.Enabled,
			Breaker:      platform.Breaker,
			BreakerLeftS: platform.BreakerLeftS,
			Hour:         platform.Hour,
			Day:          platform.Day,
			HourlyLimit:  platform.HourlyLimit,
			DailyLimit:   platform.DailyLimit,
		}
	}

	return result
}

// withRelayDefaults publishes empty lists and maps instead of null.
func withRelayDefaults(status RelayStatus) RelayStatus {
	result := status

	if result.PlatformsInUse == nil {
		result.PlatformsInUse = []string{}
	}

	if result.BreakerOpen == nil {
		result.BreakerOpen = []string{}
	}

	if result.Platforms == nil {
		result.Platforms = map[string]RelayPlatformStatus{}
	}

	return result
}

// shortRelayStatusError is a read failure without the request URL, cut to
// relayStatusErrorLimit bytes.
func shortRelayStatusError(err error) string {
	cause := err
	var urlErr *url.Error

	if errors.As(err, &urlErr) == true {
		cause = urlErr.Err
	}

	text := cause.Error()
	result := truncateAtRuneBoundary(text, relayStatusErrorLimit)

	return result
}

// readHistory aggregates the relay history; without a database it is null,
// and a failed read keeps the last one (logged once per outage).
func (r *relayStatusRefresher) readHistory(ctx context.Context, now time.Time) *RelayHistory {
	result := r.last.History
	historyCtx, cancel := context.WithTimeout(ctx, relayRequestsTimeout)
	history, err := relayRequestLog.History(historyCtx, now)
	cancel()
	isNoDatabase := errors.Is(err, errRelayHistoryNoDatabase)

	if err == nil {
		result = &history
	}

	if isNoDatabase == true {
		result = nil
	}

	r.logHistoryResult(err, isNoDatabase)

	return result
}

// logHistoryResult logs the first failed history read of an outage and the
// recovery.
func (r *relayStatusRefresher) logHistoryResult(err error, isNoDatabase bool) {
	isFailed := (err != nil) && (isNoDatabase == false)

	if (isFailed == true) && (r.isHistoryOK == true) {
		log.Printf("⚠️ [relay] history read failed: %v", err)
	}

	if (err == nil) && (r.isHistoryOK == false) {
		log.Printf("✅ [relay] history readable again")
	}

	r.isHistoryOK = (isFailed == false)
}

// pruneIfDue deletes the history older than relayHistoryRetention once per
// relayPruneInterval.
func (r *relayStatusRefresher) pruneIfDue(ctx context.Context, now time.Time) {
	isDue := (r.prunedAt.IsZero() == true) || (now.Sub(r.prunedAt) >= relayPruneInterval)

	if isDue == true {
		r.prune(ctx, now)
	}
}

// prune deletes the old history rows; a failure is retried next round.
func (r *relayStatusRefresher) prune(ctx context.Context, now time.Time) {
	before := now.Add(-relayHistoryRetention)
	pruneCtx, cancel := context.WithTimeout(ctx, relayRequestsTimeout)
	removed, err := relayRequestLog.Prune(pruneCtx, before)
	cancel()

	if err != nil {
		log.Printf("⚠️ [relay] history prune failed: %v", err)
	}

	if err == nil {
		r.prunedAt = now
	}

	if removed > 0 {
		days := int(relayHistoryRetention / relayHistoryDay)
		log.Printf("🧹 [relay] pruned %d history row(s) older than %d days", removed, days)
	}
}
