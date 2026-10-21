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
	"time"

	"github.com/timoffmax/social-link-bot/model/settings"
)

// StoredValue is one bot_settings row.
type StoredValue struct {
	Key   string
	Value json.RawMessage
}

// ChangeRequest is a claimed settings_change_requests row and, for a queued
// request, how many requests await the owner's approval. StoredCurrent is
// the key's stored value at claim time: the bot_settings value for a
// setting, the gated_fetch_policies.monthly_limit as JSON (null = none) for
// a special limit; nil when there is none or nothing was read.
type ChangeRequest struct {
	ID                int64
	Kind              string
	Key               string
	Value             json.RawMessage
	Expected          json.RawMessage
	Source            string
	IsExpired         bool
	StoredCurrent     json.RawMessage
	ApprovalMessageID int64
	AwaitingCount     int
}

// IsReset reports a request to delete the owner-set value.
func (r ChangeRequest) IsReset() bool {
	result := (r.Value == nil)

	return result
}

// Decision is what the bot does with a claimed request: apply it (Value is
// the validated JSON to store, nil to delete the row), ask the owner
// (ExpiresAt is the new expiry, set by the store), reject it, or expire it.
type Decision struct {
	Status        string
	Error         string
	NeedsApproval bool
	Value         json.RawMessage
	TypedValue    any
	ExpiresAt     time.Time
}

// IsApplied reports a decision to write the change.
func (d Decision) IsApplied() bool {
	result := (d.Status == settings.StatusApplied)

	return result
}

// IsAwaitingApproval reports a decision to ask the owner in Telegram.
func (d Decision) IsAwaitingApproval() bool {
	result := (d.Status == settings.StatusAwaitingApproval)

	return result
}

// ApprovalOutcome is the result of the owner's tap: the request as it is
// stored, the tap's decision (empty Status when the tap changed nothing:
// already decided or expired) and the request's status afterwards.
type ApprovalOutcome struct {
	Request  ChangeRequest
	Decision Decision
	Status   string
	Error    string
}

// IsDecided reports a tap that changed the request.
func (o ApprovalOutcome) IsDecided() bool {
	result := (o.Decision.Status != "")

	return result
}

// Store is the database side of the settings goroutine.
type Store interface {
	// LoadSettings reads every bot_settings row.
	LoadSettings(ctx context.Context) ([]StoredValue, error)
	// ProcessNextRequest claims the oldest queued request, lets decide rule
	// on it and records the outcome (with the change's write when it is
	// applied) in one transaction. It returns false when none is queued.
	ProcessNextRequest(ctx context.Context, decide func(ChangeRequest) Decision) (ChangeRequest, Decision, bool, error)
	// ExpireRequests marks every queued or awaiting request past its
	// expires_at (the DB's clock) expired and returns them.
	ExpireRequests(ctx context.Context) ([]ChangeRequest, error)
	// SetApprovalMessage stores the Telegram message that asks the owner.
	SetApprovalMessage(ctx context.Context, requestID int64, messageID int64) error
	// RejectAwaiting rejects a request still awaiting approval; false when it
	// was no longer awaiting.
	RejectAwaiting(ctx context.Context, requestID int64, reason string) (bool, error)
	// DecideApproval records the owner's Approve or Reject in one transaction:
	// a conditional claim of the awaiting, unexpired request, then for
	// Approve decide's ruling and the change's write.
	DecideApproval(ctx context.Context, requestID int64, isApprove bool, decide func(ChangeRequest) Decision) (ApprovalOutcome, error)
	// PublishStatus upserts one bot_runtime_status component.
	PublishStatus(ctx context.Context, component string, data json.RawMessage) error
	// LoadStatus reads one bot_runtime_status component; false when it has
	// no row.
	LoadStatus(ctx context.Context, component string) (json.RawMessage, bool, error)
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

// awaitingCountQuery counts the requests waiting for the owner's approval.
const awaitingCountQuery = `SELECT count(*) FROM settings_change_requests WHERE status = 'awaiting_approval'`

// setActorQuery names the writer, the request and the note for the audit
// trigger, for this transaction only.
const setActorQuery = `SELECT set_config('app.settings_actor', $1, true), set_config('app.settings_request_id', $2, true), set_config('app.settings_note', $3, true)`

const upsertSettingQuery = `
	INSERT INTO bot_settings (key, value) VALUES ($1, $2)
	ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`

const deleteSettingQuery = `DELETE FROM bot_settings WHERE key = $1`

// ensurePolicyRowQuery creates a missing policy row (enabled, default limit:
// the same as no row, so the audit trigger logs nothing) for the next
// statement to lock; pruneDefaultPolicy removes it again unless it holds a
// change.
const ensurePolicyRowQuery = `
	INSERT INTO gated_fetch_policies (scope, scope_id, enabled) VALUES ($1, $2, true)
	ON CONFLICT (scope, scope_id) DO NOTHING`

// policyLimitQuery locks a scope's policy and reads its limit as JSON.
const policyLimitQuery = `SELECT COALESCE(to_jsonb(monthly_limit), 'null'::jsonb) FROM gated_fetch_policies WHERE scope = $1 AND scope_id = $2 FOR UPDATE`

// setPolicyLimitQuery stores a limit (NULL = default) and keeps enabled.
const setPolicyLimitQuery = `UPDATE gated_fetch_policies SET monthly_limit = $3 WHERE scope = $1 AND scope_id = $2`

// pruneDefaultPolicyQuery removes the scope's row while it holds only the
// defaults (enabled, no limit), so no claim lock row or reset row is left
// behind for the group-upgrade copy to carry over.
const pruneDefaultPolicyQuery = `DELETE FROM gated_fetch_policies WHERE scope = $1 AND scope_id = $2 AND enabled = true AND monthly_limit IS NULL`

const decideRequestQuery = `
	UPDATE settings_change_requests
	SET status = $2, error = NULLIF($3, ''), needs_approval = $4, decided_at = now()
	WHERE id = $1 AND status = 'queued'`

// awaitApprovalQuery moves a claimed request to awaiting_approval with a
// fresh expiry on the DB's clock.
const awaitApprovalQuery = `
	UPDATE settings_change_requests
	SET status = 'awaiting_approval', needs_approval = true, error = NULL, expires_at = now() + make_interval(secs => $2)
	WHERE id = $1 AND status = 'queued'
	RETURNING expires_at`

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
		decision, err = recordDecision(ctx, tx, request, decision)
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

// claimRequest locks the oldest queued request and, unless it expired,
// reads (and locks) the key's stored value.
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
	}

	if (isClaimed == true) && (result.IsExpired == false) {
		result, err = readCurrentFor(ctx, tx, result)
	}

	if (err == nil) && (isClaimed == true) {
		row = tx.QueryRowContext(ctx, awaitingCountQuery)
		err = row.Scan(&result.AwaitingCount)
	}

	return result, isClaimed, err
}

// readCurrentFor reads (and locks) the stored value a request changes: the
// policy limit of a special limit, the bot_settings value of a setting;
// another kind reads nothing (decide rejects it).
func readCurrentFor(ctx context.Context, tx *sql.Tx, request ChangeRequest) (ChangeRequest, error) {
	result := request
	var err error
	target, keyErr := specialLimitTargetOf(request)

	if (request.Kind == settings.KindSpecialLimit) && (keyErr == nil) {
		result.StoredCurrent, err = readPolicyLimit(ctx, tx, target)
	}

	if request.Kind == settings.KindSetting {
		result.StoredCurrent, err = readStoredValue(ctx, tx, request.Key)
	}

	return result, err
}

// specialLimitTargetOf parses the key of a special-limit request; any other
// kind gives an error.
func specialLimitTargetOf(request ChangeRequest) (settings.SpecialLimitTarget, error) {
	result := settings.SpecialLimitTarget{}
	err := errNotSpecialLimit

	if request.Kind == settings.KindSpecialLimit {
		result, err = settings.ParseSpecialLimitKey(request.Key)
	}

	return result, err
}

// errNotSpecialLimit marks a request of another kind.
var errNotSpecialLimit = errors.New("not a special-limit request")

// readPolicyLimit makes sure the scope has a policy row to lock, then locks
// it and reads its limit as JSON.
func readPolicyLimit(ctx context.Context, tx *sql.Tx, target settings.SpecialLimitTarget) (json.RawMessage, error) {
	var result json.RawMessage
	_, err := tx.ExecContext(ctx, ensurePolicyRowQuery, target.Scope, target.ID)

	if err == nil {
		var raw []byte
		row := tx.QueryRowContext(ctx, policyLimitQuery, target.Scope, target.ID)
		err = row.Scan(&raw)
		result = nullableJSON(raw)
	}

	return result, err
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

// recordDecision writes an applied request's change (as its source, for the
// audit), drops a special limit's all-default policy row, and records the
// request's outcome; a request that waits for the owner gets its new
// expiry, returned in the decision.
func recordDecision(ctx context.Context, tx *sql.Tx, request ChangeRequest, decision Decision) (Decision, error) {
	result := decision
	var err error
	isAwaiting := decision.IsAwaitingApproval()

	if decision.IsApplied() == true {
		err = writeChange(ctx, tx, request, decision, "")
	}

	if err == nil {
		err = pruneDefaultPolicy(ctx, tx, request)
	}

	if (err == nil) && (isAwaiting == true) {
		timeout := approvalTimeout.Seconds()
		row := tx.QueryRowContext(ctx, awaitApprovalQuery, request.ID, timeout)
		err = row.Scan(&result.ExpiresAt)
	}

	if (err == nil) && (isAwaiting == false) {
		_, err = tx.ExecContext(ctx, decideRequestQuery, request.ID, decision.Status, decision.Error, decision.NeedsApproval)
	}

	return result, err
}

// writeChange writes an applied request by its kind; note goes into the
// audit row.
func writeChange(ctx context.Context, tx *sql.Tx, request ChangeRequest, decision Decision, note string) error {
	var err error

	if request.Kind == settings.KindSpecialLimit {
		err = writeSpecialLimit(ctx, tx, request, decision, note)
	}

	if request.Kind != settings.KindSpecialLimit {
		err = writeSetting(ctx, tx, request, decision, note)
	}

	return err
}

// writeSpecialLimit stores the new limit (NULL = the default) on the
// scope's locked policy row, keeping enabled; the trigger audits it.
func writeSpecialLimit(ctx context.Context, tx *sql.Tx, request ChangeRequest, decision Decision, note string) error {
	requestID := fmt.Sprintf("%d", request.ID)
	change, isChange := decision.TypedValue.(specialLimitChange)
	limit := sql.NullInt64{}
	err := errSpecialLimitNoChange

	if isChange == true {
		_, err = tx.ExecContext(ctx, setActorQuery, request.Source, requestID, note)
	}

	if (isChange == true) && (change.New != nil) {
		limit = sql.NullInt64{Int64: *change.New, Valid: true}
	}

	if err == nil {
		_, err = tx.ExecContext(ctx, setPolicyLimitQuery, change.Target.Scope, change.Target.ID, limit)
	}

	return err
}

// errSpecialLimitNoChange marks an applied special limit without its change
// (a decision that did not come from decideSpecialLimitRequest).
var errSpecialLimitNoChange = errors.New("applied special limit without its change")

// pruneDefaultPolicy removes a special limit's policy row while it holds
// only the defaults (the claim's lock row or a reset), whatever the
// decision; settings are left alone.
func pruneDefaultPolicy(ctx context.Context, tx *sql.Tx, request ChangeRequest) error {
	var err error
	target, keyErr := specialLimitTargetOf(request)

	if keyErr == nil {
		_, err = tx.ExecContext(ctx, pruneDefaultPolicyQuery, target.Scope, target.ID)
	}

	return err
}

// writeSetting upserts or deletes the key's bot_settings row; note goes
// into the audit row.
func writeSetting(ctx context.Context, tx *sql.Tx, request ChangeRequest, decision Decision, note string) error {
	requestID := fmt.Sprintf("%d", request.ID)
	_, err := tx.ExecContext(ctx, setActorQuery, request.Source, requestID, note)

	if (err == nil) && (decision.Value == nil) {
		_, err = tx.ExecContext(ctx, deleteSettingQuery, request.Key)
	}

	if (err == nil) && (decision.Value != nil) {
		value := []byte(decision.Value)
		_, err = tx.ExecContext(ctx, upsertSettingQuery, request.Key, value)
	}

	return err
}

// expireRequestsQuery expires the queued and awaiting requests past their
// expiry, with the same test as claimRequestQuery.
const expireRequestsQuery = `
	UPDATE settings_change_requests
	SET status = 'expired', error = CASE WHEN status = 'queued' THEN $1 ELSE $2 END, decided_at = now()
	WHERE status IN ('queued', 'awaiting_approval') AND expires_at <= now()
	RETURNING id, kind, key, value, expected, source, COALESCE(approval_message_id, 0)`

// ExpireRequests marks the queued and awaiting requests past their expiry
// expired and returns them.
func (s pgStore) ExpireRequests(ctx context.Context) ([]ChangeRequest, error) {
	result := []ChangeRequest{}
	rows, err := s.db.QueryContext(ctx, expireRequestsQuery, requestExpiredError, requestApprovalExpiredError)

	if err == nil {
		defer rows.Close()
		result, err = scanRequests(rows)
	}

	return result, err
}

// scanRequests reads rows of id, kind, key, value, expected, source and
// approval message id.
func scanRequests(rows *sql.Rows) ([]ChangeRequest, error) {
	result := []ChangeRequest{}
	var err error

	for (err == nil) && (rows.Next() == true) {
		var request ChangeRequest
		var value []byte
		var expected []byte
		err = rows.Scan(&request.ID, &request.Kind, &request.Key, &value, &expected, &request.Source, &request.ApprovalMessageID)
		request.Value = nullableJSON(value)
		request.Expected = nullableJSON(expected)

		if err == nil {
			result = append(result, request)
		}
	}

	if err == nil {
		err = rows.Err()
	}

	return result, err
}

const setApprovalMessageQuery = `
	UPDATE settings_change_requests SET approval_message_id = $2
	WHERE id = $1 AND status = 'awaiting_approval'`

// SetApprovalMessage stores the Telegram message that asks the owner.
func (s pgStore) SetApprovalMessage(ctx context.Context, requestID int64, messageID int64) error {
	_, err := s.db.ExecContext(ctx, setApprovalMessageQuery, requestID, messageID)

	return err
}

const rejectAwaitingQuery = `
	UPDATE settings_change_requests SET status = 'rejected', error = $2, decided_at = now()
	WHERE id = $1 AND status = 'awaiting_approval'`

// RejectAwaiting rejects a request still awaiting approval.
func (s pgStore) RejectAwaiting(ctx context.Context, requestID int64, reason string) (bool, error) {
	outcome, err := s.db.ExecContext(ctx, rejectAwaitingQuery, requestID, reason)
	affected := int64(0)

	if err == nil {
		affected, err = outcome.RowsAffected()
	}

	result := (affected > 0)

	return result, err
}

// approveRequestQuery claims an awaiting, unexpired request for Approve;
// only the statement that changes the row acts (a double tap or a tap
// racing the expiry matches no row).
const approveRequestQuery = `
	UPDATE settings_change_requests SET status = 'applied', error = NULL, decided_at = now()
	WHERE id = $1 AND status = 'awaiting_approval' AND expires_at > now()
	RETURNING id, kind, key, value, expected, source, COALESCE(approval_message_id, 0)`

// rejectByOwnerQuery is approveRequestQuery for Reject.
const rejectByOwnerQuery = `
	UPDATE settings_change_requests SET status = 'rejected', error = $2, decided_at = now()
	WHERE id = $1 AND status = 'awaiting_approval' AND expires_at > now()
	RETURNING id, kind, key, value, expected, source, COALESCE(approval_message_id, 0)`

// overruleApprovedQuery replaces the claim's 'applied' when the re-check
// after Approve refuses the change, in the same transaction.
const overruleApprovedQuery = `
	UPDATE settings_change_requests SET status = $2, error = NULLIF($3, '')
	WHERE id = $1 AND status = 'applied'`

// expireTappedQuery expires a tapped request past its expiry that the
// sweep has not reached yet.
const expireTappedQuery = `
	UPDATE settings_change_requests SET status = 'expired', error = $2, decided_at = now()
	WHERE id = $1 AND status = 'awaiting_approval' AND expires_at <= now()`

const readRequestQuery = `
	SELECT id, kind, key, value, expected, source, COALESCE(approval_message_id, 0), status, COALESCE(error, '')
	FROM settings_change_requests WHERE id = $1`

// approvedNote is the audit note of a change the owner approved.
const approvedNote = "approved in Telegram"

// DecideApproval records the owner's tap in one transaction.
func (s pgStore) DecideApproval(ctx context.Context, requestID int64, isApprove bool, decide func(ChangeRequest) Decision) (ApprovalOutcome, error) {
	result := ApprovalOutcome{}
	isClaimed := false
	tx, err := s.db.BeginTx(ctx, nil)

	if err == nil {
		result.Request, isClaimed, err = claimAwaiting(ctx, tx, requestID, isApprove)
	}

	if (err == nil) && (isClaimed == true) {
		result, err = decideAwaiting(ctx, tx, result.Request, isApprove, decide)
	}

	if (err == nil) && (isClaimed == false) {
		result, err = readUndecided(ctx, tx, requestID)
	}

	if err == nil {
		err = tx.Commit()
	}

	if (tx != nil) && (err != nil) {
		_ = tx.Rollback()
	}

	return result, err
}

// claimAwaiting runs the conditional claim of Approve or Reject.
func claimAwaiting(ctx context.Context, tx *sql.Tx, requestID int64, isApprove bool) (ChangeRequest, bool, error) {
	query := approveRequestQuery
	args := []any{requestID}

	if isApprove == false {
		query = rejectByOwnerQuery
		args = append(args, requestOwnerRejectedError)
	}

	row := tx.QueryRowContext(ctx, query, args...)
	result, err := scanRequest(row)
	isClaimed := (err == nil)

	if errors.Is(err, sql.ErrNoRows) == true {
		err = nil
	}

	return result, isClaimed, err
}

// scanRequest reads one row of id, kind, key, value, expected, source and
// approval message id, plus any extra destinations.
func scanRequest(row *sql.Row, extra ...any) (ChangeRequest, error) {
	result := ChangeRequest{}
	var value []byte
	var expected []byte
	destinations := []any{&result.ID, &result.Kind, &result.Key, &value, &expected, &result.Source, &result.ApprovalMessageID}
	destinations = append(destinations, extra...)
	err := row.Scan(destinations...)
	result.Value = nullableJSON(value)
	result.Expected = nullableJSON(expected)

	return result, err
}

// decideAwaiting finishes a claimed tap: Reject is already recorded;
// Approve re-checks the request and writes the change, or overrules the
// claim with the rejection.
func decideAwaiting(ctx context.Context, tx *sql.Tx, request ChangeRequest, isApprove bool, decide func(ChangeRequest) Decision) (ApprovalOutcome, error) {
	decision := rejected(requestOwnerRejectedError)
	var err error

	if isApprove == true {
		request, err = readCurrentFor(ctx, tx, request)
	}

	if (err == nil) && (isApprove == true) {
		decision = decide(request)
	}

	if (err == nil) && (isApprove == true) && (decision.IsApplied() == true) {
		err = writeChange(ctx, tx, request, decision, approvedNote)
	}

	if (err == nil) && (isApprove == true) {
		err = pruneDefaultPolicy(ctx, tx, request)
	}

	if (err == nil) && (isApprove == true) && (decision.IsApplied() == false) {
		_, err = tx.ExecContext(ctx, overruleApprovedQuery, request.ID, decision.Status, decision.Error)
	}

	result := ApprovalOutcome{Request: request, Decision: decision, Status: decision.Status, Error: decision.Error}

	return result, err
}

// readUndecided expires a tapped request past its expiry and reads the
// request's state after a tap that changed nothing.
func readUndecided(ctx context.Context, tx *sql.Tx, requestID int64) (ApprovalOutcome, error) {
	result := ApprovalOutcome{}
	_, err := tx.ExecContext(ctx, expireTappedQuery, requestID, requestApprovalExpiredError)

	if err == nil {
		row := tx.QueryRowContext(ctx, readRequestQuery, requestID)
		result.Request, err = scanRequest(row, &result.Status, &result.Error)
	}

	if errors.Is(err, sql.ErrNoRows) == true {
		err = nil
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

const loadStatusQuery = `SELECT data FROM bot_runtime_status WHERE component = $1`

// LoadStatus reads one bot_runtime_status component; false when it has no
// row.
func (s pgStore) LoadStatus(ctx context.Context, component string) (json.RawMessage, bool, error) {
	var data []byte
	isFound := false
	row := s.db.QueryRowContext(ctx, loadStatusQuery, component)
	err := row.Scan(&data)

	if err == nil {
		isFound = true
	}

	if errors.Is(err, sql.ErrNoRows) == true {
		err = nil
	}

	result := json.RawMessage(data)

	return result, isFound, err
}
