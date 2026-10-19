package dashboard

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/lib/pq"
)

// kindCheckConstraint is the request table's kind CHECK: before the bot's
// migration 012 it refuses the special_limit kind.
const kindCheckConstraint = "settings_change_requests_kind_check"

// Errors of the limit requests; the server maps them to 409, 409 and 400.
var (
	ErrLimitRequestOpen   = errors.New("a change of this limit is already waiting")
	ErrLimitsNotSupported = errors.New("the running bot does not take limit changes yet: deploy the bot first")
	ErrLimitRejectedByDB  = errors.New(errLimitRejectedByDB)
)

// Limits loads the Limits tab in one snapshot: the published components,
// the bot-wide use, at most rows people and groups (busiest first) with
// their total, and the open limit requests.
func (s *PGStore) Limits(ctx context.Context, rows int) (LimitsSnapshot, error) {
	result := LimitsSnapshot{}
	now := s.currentTime()
	statuses := pq.Array(openSettingStatuses)
	err := s.readOnly(ctx, func(q readTx) error {
		loadErr := loadLimitsCommon(q, now, &result)

		if loadErr == nil {
			result.Rows, result.Total, loadErr = loadLimitRows(q, limitRowsSQL, now, rows, limitRequestKind, statuses)
		}

		return loadErr
	})

	return result, err
}

// LimitScope loads one person or group as stored, also one without any
// row, with the same components as Limits.
func (s *PGStore) LimitScope(ctx context.Context, scope string, id int64) (LimitsSnapshot, error) {
	result := LimitsSnapshot{}
	now := s.currentTime()
	err := s.readOnly(ctx, func(q readTx) error {
		loadErr := loadLimitsCommon(q, now, &result)

		if loadErr == nil {
			result.Rows, result.Total, loadErr = loadLimitRows(q, limitOneScopeSQL, now, 1, scope, id)
		}

		return loadErr
	})

	return result, err
}

// QueueLimitRequest inserts a limit request for the bot to apply.
// ErrLimitRequestOpen while another one for the scope is open,
// ErrLimitsNotSupported before the bot's migration allows the kind,
// ErrLimitRejectedByDB for any other CHECK refusing it.
func (s *PGStore) QueueLimitRequest(ctx context.Context, draft SettingRequestDraft) (SettingRequest, error) {
	result := SettingRequest{}
	value := nullableJSON(draft.Value)
	expected := string(draft.Expected)
	err := s.readWrite(ctx, func(q writeTx) error {
		row := q.tx.QueryRowContext(q.ctx, queueSettingRequestSQL, limitRequestKind, draft.Key, value, expected)
		var scanErr error
		result, scanErr = scanSettingRequest(row)

		return scanErr
	})
	err = limitQueueError(err)

	return result, err
}

// limitQueueError maps the request table's refusals of a limit request.
func limitQueueError(err error) error {
	result := err
	var pqErr *pq.Error
	isPQ := errors.As(err, &pqErr)
	isCheck := (isPQ == true) && (string(pqErr.Code) == checkViolation)

	switch {
	case isOpenRequestViolation(err) == true:
		result = ErrLimitRequestOpen
	case (isCheck == true) && (pqErr.Constraint == kindCheckConstraint):
		result = ErrLimitsNotSupported
	case isCheck == true:
		result = ErrLimitRejectedByDB
	}

	return result
}

// loadLimitsCommon reads the components, the bot-wide use and the open
// limit requests.
func loadLimitsCommon(q readTx, now time.Time, snapshot *LimitsSnapshot) error {
	var err error
	snapshot.Components, err = loadStatusComponents(q, limitsComponents)

	if err == nil {
		dest := []interface{}{&snapshot.GlobalUsed}
		err = q.row(globalUsageSQL, dest, now, gatedPeriodTimezone, gatedScopeGlobal)
	}

	if err == nil {
		snapshot.OpenRequests, err = loadOpenSettingRequests(q, limitRequestKind)
	}

	return err
}

// loadLimitRows runs limitRowsSQL or limitOneScopeSQL with the shared
// parameters, the query's own two ($7, $8) and the near-limit ratio ($9).
func loadLimitRows(q readTx, query string, now time.Time, rows int, own1 interface{}, own2 interface{}) ([]LimitRow, int64, error) {
	result := []LimitRow{}
	var total int64
	args := []interface{}{now, gatedPeriodTimezone, limitScopeChat, limitScopeUser, rows, limitAuditPrefix, own1, own2, limitNearRatio}
	err := q.rows(query, func(scanned *sql.Rows) error {
		row, rowTotal, scanErr := scanLimitRow(scanned)
		result = append(result, row)
		total = rowTotal

		return scanErr
	}, args...)

	return result, total, err
}

// scanLimitRow reads one limitRowsBody row and its total.
func scanLimitRow(rows *sql.Rows) (LimitRow, int64, error) {
	result := LimitRow{}
	var total int64
	var stored sql.NullInt64
	var changedAt sql.NullTime
	var actor sql.NullString
	var oldValue []byte
	var newValue []byte
	err := rows.Scan(
		&result.Scope,
		&result.ScopeID,
		&result.Title,
		&result.Used,
		&result.UsedLastMonth,
		&stored,
		&result.Enabled,
		&result.Credits,
		&result.Boosts,
		&total,
		&changedAt,
		&actor,
		&oldValue,
		&newValue,
	)

	if err == nil {
		result.StoredLimit = nullInt64(stored)
		result.LastChange = limitChange(changedAt, actor, oldValue, newValue)
	}

	return result, total, err
}

// limitChange is the newest audit row of a limit; nil without one.
func limitChange(changedAt sql.NullTime, actor sql.NullString, oldValue []byte, newValue []byte) *LimitChange {
	var result *LimitChange

	if changedAt.Valid == true {
		at := formatTime(changedAt.Time)
		result = &LimitChange{At: at, Actor: actor.String, OldValue: rawJSONOrNull(oldValue), NewValue: rawJSONOrNull(newValue)}
	}

	return result
}
