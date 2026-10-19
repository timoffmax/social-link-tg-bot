// Package botsettings runs the bot's settings goroutine: it loads the
// owner-set values (bot_settings) into the snapshot the config getters read,
// applies the change requests the dashboard queues, reloads every minute,
// keeps a cache file for starts while the database is down, publishes what
// the bot uses (bot_runtime_status), and re-pushes the Telegram profile when
// the rendered profile changes. One goroutine owns the snapshot, so a reload
// can never undo an apply.
package botsettings

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/timoffmax/social-link-bot/model/settings"
)

// StoredValue is one bot_settings row.
type StoredValue struct {
	Key   string
	Value json.RawMessage
}

// ChangeRequest is a claimed settings_change_requests row, with the key's
// bot_settings value at claim time (nil when it has none).
type ChangeRequest struct {
	ID            int64
	Kind          string
	Key           string
	Value         json.RawMessage
	Expected      json.RawMessage
	Source        string
	IsExpired     bool
	StoredCurrent json.RawMessage
}

// IsReset reports a request to delete the owner-set value.
func (r ChangeRequest) IsReset() bool {
	result := (r.Value == nil)

	return result
}

// Decision is what the bot does with a claimed request: apply it (Value is
// the validated JSON to store, nil to delete the row), reject it with an
// error, or expire it.
type Decision struct {
	Status        string
	Error         string
	NeedsApproval bool
	Value         json.RawMessage
	TypedValue    any
}

// IsApplied reports a decision to write bot_settings.
func (d Decision) IsApplied() bool {
	result := (d.Status == settings.StatusApplied)

	return result
}

// Store is the database side of the settings goroutine.
type Store interface {
	// LoadSettings reads every bot_settings row.
	LoadSettings(ctx context.Context) ([]StoredValue, error)
	// ProcessNextRequest claims the oldest queued request, lets decide rule
	// on it and records the outcome (with the bot_settings write when it is
	// applied) in one transaction. It returns false when none is queued.
	ProcessNextRequest(ctx context.Context, decide func(ChangeRequest) Decision) (ChangeRequest, Decision, bool, error)
	// ExpireRequests marks every queued request past its expires_at (the
	// DB's clock) expired and returns how many it marked.
	ExpireRequests(ctx context.Context) (int64, error)
	// PublishStatus upserts one bot_runtime_status component.
	PublishStatus(ctx context.Context, component string, data json.RawMessage) error
}

// pgStore is the PostgreSQL Store.
type pgStore struct {
	db *sql.DB
}

// NewPGStore returns the Store of the bot's database.
func NewPGStore(db *sql.DB) Store {
	result := pgStore{db: db}

	return result
}

const loadSettingsQuery = `SELECT key, value FROM bot_settings ORDER BY key`

// LoadSettings reads every bot_settings row.
func (s pgStore) LoadSettings(ctx context.Context) ([]StoredValue, error) {
	result := []StoredValue{}
	rows, err := s.db.QueryContext(ctx, loadSettingsQuery)

	if err == nil {
		defer rows.Close()
		result, err = scanStoredValues(rows)
	}

	return result, err
}

// scanStoredValues reads the rows of loadSettingsQuery.
func scanStoredValues(rows *sql.Rows) ([]StoredValue, error) {
	result := []StoredValue{}
	var err error

	for (err == nil) && (rows.Next() == true) {
		var value StoredValue
		var raw []byte
		err = rows.Scan(&value.Key, &raw)
		value.Value = json.RawMessage(raw)

		if err == nil {
			result = append(result, value)
		}
	}

	if err == nil {
		err = rows.Err()
	}

	return result, err
}

// claimRequestQuery locks the oldest queued request; a request past its
// expiry (the DB's clock) is reported as expired.
const claimRequestQuery = `
	SELECT id, kind, key, value, expected, source, (expires_at <= now()) AS is_expired
	FROM settings_change_requests
	WHERE status = 'queued'
	ORDER BY created_at, id
	LIMIT 1
	FOR UPDATE SKIP LOCKED`

// currentValueQuery locks the key's bot_settings row while the request is
// decided.
const currentValueQuery = `SELECT value FROM bot_settings WHERE key = $1 FOR UPDATE`

// setActorQuery names the writer for the audit trigger, for this
// transaction only.
const setActorQuery = `SELECT set_config('app.settings_actor', $1, true), set_config('app.settings_request_id', $2, true)`

const upsertSettingQuery = `
	INSERT INTO bot_settings (key, value) VALUES ($1, $2)
	ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`

const deleteSettingQuery = `DELETE FROM bot_settings WHERE key = $1`

const decideRequestQuery = `
	UPDATE settings_change_requests
	SET status = $2, error = NULLIF($3, ''), needs_approval = $4, decided_at = now()
	WHERE id = $1 AND status = 'queued'`

// ProcessNextRequest claims, decides and records the oldest queued request.
func (s pgStore) ProcessNextRequest(ctx context.Context, decide func(ChangeRequest) Decision) (ChangeRequest, Decision, bool, error) {
	request := ChangeRequest{}
	decision := Decision{}
	isClaimed := false
	tx, err := s.db.BeginTx(ctx, nil)

	if err == nil {
		request, isClaimed, err = claimRequest(ctx, tx)
	}

	if (err == nil) && (isClaimed == true) {
		decision = decideClaimed(request, decide)
		err = recordDecision(ctx, tx, request, decision)
	}

	if (err == nil) && (isClaimed == true) {
		err = tx.Commit()
	}

	if (tx != nil) && ((err != nil) || (isClaimed == false)) {
		_ = tx.Rollback()
	}

	if err != nil {
		isClaimed = false
	}

	return request, decision, isClaimed, err
}

// claimRequest locks the oldest queued request and reads the key's value.
func claimRequest(ctx context.Context, tx *sql.Tx) (ChangeRequest, bool, error) {
	result := ChangeRequest{}
	var value []byte
	var expected []byte
	row := tx.QueryRowContext(ctx, claimRequestQuery)
	err := row.Scan(&result.ID, &result.Kind, &result.Key, &value, &expected, &result.Source, &result.IsExpired)
	isClaimed := (err == nil)

	if errors.Is(err, sql.ErrNoRows) == true {
		err = nil
	}

	if isClaimed == true {
		result.Value = nullableJSON(value)
		result.Expected = nullableJSON(expected)
		result.StoredCurrent, err = readStoredValue(ctx, tx, result.Key)
	}

	return result, isClaimed, err
}

// readStoredValue reads (and locks) a key's bot_settings value; nil when
// the key has none.
func readStoredValue(ctx context.Context, tx *sql.Tx, key string) (json.RawMessage, error) {
	var raw []byte
	row := tx.QueryRowContext(ctx, currentValueQuery, key)
	err := row.Scan(&raw)
	result := nullableJSON(raw)

	if errors.Is(err, sql.ErrNoRows) == true {
		err = nil
	}

	return result, err
}

// nullableJSON keeps SQL NULL as nil.
func nullableJSON(raw []byte) json.RawMessage {
	var result json.RawMessage

	if raw != nil {
		result = json.RawMessage(raw)
	}

	return result
}

// decideClaimed expires a request past its expiry, else asks decide.
func decideClaimed(request ChangeRequest, decide func(ChangeRequest) Decision) Decision {
	result := Decision{Status: settings.StatusExpired, Error: requestExpiredError}

	if request.IsExpired == false {
		result = decide(request)
	}

	return result
}

// recordDecision writes bot_settings for an applied request (as its source,
// for the audit) and the request's outcome.
func recordDecision(ctx context.Context, tx *sql.Tx, request ChangeRequest, decision Decision) error {
	var err error

	if decision.IsApplied() == true {
		err = writeSetting(ctx, tx, request, decision)
	}

	if err == nil {
		_, err = tx.ExecContext(ctx, decideRequestQuery, request.ID, decision.Status, decision.Error, decision.NeedsApproval)
	}

	return err
}

// writeSetting upserts or deletes the key's bot_settings row.
func writeSetting(ctx context.Context, tx *sql.Tx, request ChangeRequest, decision Decision) error {
	requestID := fmt.Sprintf("%d", request.ID)
	_, err := tx.ExecContext(ctx, setActorQuery, request.Source, requestID)

	if (err == nil) && (decision.Value == nil) {
		_, err = tx.ExecContext(ctx, deleteSettingQuery, request.Key)
	}

	if (err == nil) && (decision.Value != nil) {
		value := []byte(decision.Value)
		_, err = tx.ExecContext(ctx, upsertSettingQuery, request.Key, value)
	}

	return err
}

// expireRequestsQuery expires the queued requests past their expiry, with
// the same test as claimRequestQuery (the partial index on queued requests
// keeps it cheap).
const expireRequestsQuery = `
	UPDATE settings_change_requests
	SET status = 'expired', error = $1, decided_at = now()
	WHERE status = 'queued' AND expires_at <= now()`

// ExpireRequests marks the queued requests past their expiry expired.
func (s pgStore) ExpireRequests(ctx context.Context) (int64, error) {
	result := int64(0)
	outcome, err := s.db.ExecContext(ctx, expireRequestsQuery, requestExpiredError)

	if err == nil {
		result, err = outcome.RowsAffected()
	}

	return result, err
}

const publishStatusQuery = `
	INSERT INTO bot_runtime_status (component, data, updated_at) VALUES ($1, $2, now())
	ON CONFLICT (component) DO UPDATE SET data = EXCLUDED.data, updated_at = now()`

// PublishStatus upserts one bot_runtime_status component.
func (s pgStore) PublishStatus(ctx context.Context, component string, data json.RawMessage) error {
	value := []byte(data)
	_, err := s.db.ExecContext(ctx, publishStatusQuery, component, value)

	return err
}
