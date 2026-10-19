package services

// History of the calls the bot sent to the home relay (migration 013): one
// row per call that got past the bot's own checks, with no user, chat or
// video ids. The relay status refresher aggregates it for the dashboard and
// prunes it once a day.

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"
)

// Outcome codes of a relay call besides the relay's own error codes.
const (
	relayOutcomeOK        = "OK"
	relayOutcomeTransport = "TRANSPORT"
	relayOutcomeRejected  = "REJECTED"
	relayOutcomeSkipped   = "SKIPPED"
)

// Relay history windows and retention.
const (
	relayHistoryWindowDay   = "24h"
	relayHistoryWindowWeek  = "7d"
	relayHistoryWindowMonth = "30d"
	relayHistoryDay         = 24 * time.Hour
	relayHistoryWeek        = 7 * relayHistoryDay
	relayHistoryMonth       = 30 * relayHistoryDay
	relayHistoryRetention   = 90 * relayHistoryDay
	relayRequestsTimeout    = 5 * time.Second
)

// relayHistoryWindows are the windows of the published history.
var relayHistoryWindows = []string{relayHistoryWindowDay, relayHistoryWindowWeek, relayHistoryWindowMonth}

// errRelayHistoryNoDatabase marks a history read without a database.
var errRelayHistoryNoDatabase = errors.New("relay history needs the database")

// relayRequestRow is one recorded relay call; Bytes is set for a delivered
// video only.
type relayRequestRow struct {
	Platform   string
	Code       string
	Bytes      *int64
	DurationMs int64
}

// RelayHistory is the 'relay' component's history: counts per platform and
// outcome code, plus the data served and the average duration of the
// delivered videos.
type RelayHistory struct {
	Windows []string             `json:"windows"`
	Rows    []RelayHistoryRow    `json:"rows"`
	OKBytes RelayHistoryBytes    `json:"ok_bytes"`
	OKAvgMs RelayHistoryDuration `json:"ok_avg_ms"`
}

// RelayHistoryRow counts one platform's calls with one outcome code.
type RelayHistoryRow struct {
	Platform string `json:"platform"`
	Code     string `json:"code"`
	Day      int64  `json:"24h"`
	Week     int64  `json:"7d"`
	Month    int64  `json:"30d"`
}

// RelayHistoryBytes is the data of the delivered videos per window.
type RelayHistoryBytes struct {
	Day  int64 `json:"24h"`
	Week int64 `json:"7d"`
}

// RelayHistoryDuration is the average duration of the delivered videos per
// window; null without any.
type RelayHistoryDuration struct {
	Day  *int64 `json:"24h"`
	Week *int64 `json:"7d"`
}

// relayHistorySums is one aggregate row: its counts and the sums of
// bytes and duration for the day and the week.
type relayHistorySums struct {
	row          RelayHistoryRow
	dayBytes     int64
	weekBytes    int64
	dayDuration  int64
	weekDuration int64
}

// relayRequestStore records, aggregates and prunes the relay calls; an
// interface so the relay client and the refresher run without a database.
type relayRequestStore interface {
	Record(ctx context.Context, row relayRequestRow) error
	History(ctx context.Context, now time.Time) (RelayHistory, error)
	Prune(ctx context.Context, before time.Time) (int64, error)
}

// pgRelayRequestStore is the PostgreSQL store; without a database it
// records nothing.
type pgRelayRequestStore struct{}

// relayRequestLog is the store the relay client and the refresher use.
var relayRequestLog relayRequestStore = pgRelayRequestStore{}

// relayRecordsInFlight counts the call records still being written.
var relayRecordsInFlight sync.WaitGroup

const insertRelayRequestQuery = `INSERT INTO relay_requests (platform, code, bytes, duration_ms) VALUES ($1, $2, $3, $4)`

// relayHistoryQuery: $1 day start, $2 week start, $3 month start, $4 the OK
// code; one row per platform and code of the last 30 days.
const relayHistoryQuery = `
	SELECT platform, code,
		COUNT(*) FILTER (WHERE created_at >= $1),
		COUNT(*) FILTER (WHERE created_at >= $2),
		COUNT(*),
		COALESCE(SUM(bytes) FILTER (WHERE created_at >= $1 AND code = $4), 0)::bigint,
		COALESCE(SUM(bytes) FILTER (WHERE created_at >= $2 AND code = $4), 0)::bigint,
		COALESCE(SUM(duration_ms) FILTER (WHERE created_at >= $1 AND code = $4), 0)::bigint,
		COALESCE(SUM(duration_ms) FILTER (WHERE created_at >= $2 AND code = $4), 0)::bigint
	FROM relay_requests
	WHERE created_at >= $3
	GROUP BY platform, code
	ORDER BY platform, code`

const pruneRelayRequestsQuery = `DELETE FROM relay_requests WHERE created_at < $1`

// Record implements relayRequestStore.
func (pgRelayRequestStore) Record(ctx context.Context, row relayRequestRow) error {
	db := GetDB()
	var err error

	if db != nil {
		_, err = db.ExecContext(ctx, insertRelayRequestQuery, row.Platform, row.Code, row.Bytes, row.DurationMs)
	}

	return err
}

// History implements relayRequestStore.
func (pgRelayRequestStore) History(ctx context.Context, now time.Time) (RelayHistory, error) {
	result := RelayHistory{}
	sums := []relayHistorySums{}
	db := GetDB()
	err := errRelayHistoryNoDatabase

	if db != nil {
		sums, err = queryRelayHistory(ctx, now)
	}

	if err == nil {
		result = buildRelayHistory(sums)
	}

	return result, err
}

// queryRelayHistory runs relayHistoryQuery.
func queryRelayHistory(ctx context.Context, now time.Time) ([]relayHistorySums, error) {
	result := []relayHistorySums{}
	dayStart := now.Add(-relayHistoryDay)
	weekStart := now.Add(-relayHistoryWeek)
	monthStart := now.Add(-relayHistoryMonth)
	db := GetDB()
	rows, err := db.QueryContext(ctx, relayHistoryQuery, dayStart, weekStart, monthStart, relayOutcomeOK)

	if err == nil {
		defer rows.Close()
	}

	for (err == nil) && (rows.Next() == true) {
		sums := relayHistorySums{}
		err = rows.Scan(&sums.row.Platform, &sums.row.Code, &sums.row.Day, &sums.row.Week, &sums.row.Month, &sums.dayBytes, &sums.weekBytes, &sums.dayDuration, &sums.weekDuration)

		if err == nil {
			result = append(result, sums)
		}
	}

	if err == nil {
		err = rows.Err()
	}

	if err != nil {
		err = fmt.Errorf("failed to read the relay history: %w", err)
	}

	return result, err
}

// buildRelayHistory turns the aggregate rows into the published history.
func buildRelayHistory(sums []relayHistorySums) RelayHistory {
	result := RelayHistory{Windows: relayHistoryWindows, Rows: []RelayHistoryRow{}}
	dayCalls := int64(0)
	weekCalls := int64(0)
	dayDuration := int64(0)
	weekDuration := int64(0)

	for _, sum := range sums {
		result.Rows = append(result.Rows, sum.row)
		result.OKBytes.Day += sum.dayBytes
		result.OKBytes.Week += sum.weekBytes
		dayDuration += sum.dayDuration
		weekDuration += sum.weekDuration

		if sum.row.Code == relayOutcomeOK {
			dayCalls += sum.row.Day
			weekCalls += sum.row.Week
		}
	}

	result.OKAvgMs.Day = averageMs(dayDuration, dayCalls)
	result.OKAvgMs.Week = averageMs(weekDuration, weekCalls)

	return result
}

// averageMs is total / calls, or nil without calls.
func averageMs(total int64, calls int64) *int64 {
	var result *int64

	if calls > 0 {
		average := (total / calls)
		result = &average
	}

	return result
}

// Prune implements relayRequestStore.
func (pgRelayRequestStore) Prune(ctx context.Context, before time.Time) (int64, error) {
	result := int64(0)
	db := GetDB()
	var err error

	if db != nil {
		result, err = pruneRelayRequests(ctx, before)
	}

	return result, err
}

// pruneRelayRequests deletes the rows created before before.
func pruneRelayRequests(ctx context.Context, before time.Time) (int64, error) {
	result := int64(0)
	db := GetDB()
	outcome, err := db.ExecContext(ctx, pruneRelayRequestsQuery, before)

	if err == nil {
		result, err = outcome.RowsAffected()
	}

	return result, err
}

// recordRelayCallLater stores the call in the background, so a slow
// database never delays the download.
func recordRelayCallLater(platform string, size int64, err error, duration time.Duration) {
	relayRecordsInFlight.Add(1)

	go func() {
		defer relayRecordsInFlight.Done()
		recordRelayCall(platform, size, err, duration)
	}()
}

// recordRelayCall stores one call that got past the bot's checks (best
// effort: a failure is logged and never fails the download).
func recordRelayCall(platform string, size int64, err error, duration time.Duration) {
	code := relayOutcomeCode(err)
	row := relayRequestRow{Platform: platform, Code: code, DurationMs: duration.Milliseconds()}

	if err == nil {
		row.Bytes = &size
	}

	background := context.Background()
	ctx, cancel := context.WithTimeout(background, relayRequestsTimeout)
	defer cancel()

	recordErr := relayRequestLog.Record(ctx, row)

	if recordErr != nil {
		log.Printf("⚠️ [relay] could not record a %s call: %v", platform, recordErr)
	}
}

// relayOutcomeCode names a call's outcome: OK, the relay's own error code,
// REJECTED for an answer the bot refused, SKIPPED for a call the bot could
// not send (no token), TRANSPORT for a request without an answer.
func relayOutcomeCode(err error) string {
	result := relayOutcomeTransport
	var failure *relayError
	isRelayError := errors.As(err, &failure)
	isKnownCode := (isRelayError == true) && (isKnownRelayCode(failure.Code) == true)
	isRejected := (errors.Is(err, errRelayBroken) == true) || (errors.Is(err, errRelayTooLarge) == true)

	switch {
	case err == nil:
		result = relayOutcomeOK
	case isKnownCode == true:
		result = failure.Code
	case isRejected == true:
		result = relayOutcomeRejected
	case errors.Is(err, errRelayToken) == true:
		result = relayOutcomeSkipped
	}

	return result
}

// isKnownRelayCode reports a code the relay documents.
func isKnownRelayCode(code string) bool {
	_, result := relayCodeIsOutage[code]

	return result
}
