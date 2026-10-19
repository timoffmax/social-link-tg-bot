package dashboard

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/lib/pq"
)

// bot_runtime_status components the Settings tab reads.
const (
	componentBot      = "bot"
	componentProfile  = "profile"
	componentSecrets  = "secrets"
	componentSettings = "settings"
	componentRegistry = "registry"
)

// settingsComponents are the components GET /api/settings loads.
var settingsComponents = []string{componentBot, componentProfile, componentSecrets, componentSettings, componentRegistry}

// Change requests: the only kind, and the statuses of an open one.
const (
	settingRequestKind           = "setting"
	settingStatusQueued          = "queued"
	settingStatusAwaitingApprove = "awaiting_approval"
)

// openSettingStatuses are the statuses the one-open-request index covers.
var openSettingStatuses = []string{settingStatusQueued, settingStatusAwaitingApprove}

// openRequestIndex is the unique index allowing one open request per key.
const openRequestIndex = "idx_settings_change_requests_one_open"

// uniqueViolation is PostgreSQL's unique_violation code.
const uniqueViolation = "23505"

// checkViolation is PostgreSQL's check_violation code.
const checkViolation = "23514"

// sizeCheckConstraints are the request table's pg_column_size checks, which
// can refuse a value the length precheck let through (stored JSONB size
// differs from the text length).
var sizeCheckConstraints = []string{
	"settings_change_requests_value_check",
	"settings_change_requests_expected_check",
}

// Errors of the settings requests; the server maps them to 404, 409 and 400.
var (
	ErrSettingRequestNotFound = errors.New("settings request not found")
	ErrSettingRequestOpen     = errors.New("a change of this setting is already waiting")
	ErrSettingValueTooLarge   = fmt.Errorf("value or expected is too large to store: at most %d bytes as stored", maxSettingValueBytes)
)

// Settings loads everything GET /api/settings shows in one snapshot: the
// published components, the stored values, the open requests and the newest
// audit row of every key.
func (s *PGStore) Settings(ctx context.Context) (SettingsSnapshot, error) {
	result := SettingsSnapshot{}
	err := s.readOnly(ctx, func(q readTx) error {
		var loadErr error
		result.Components, loadErr = loadStatusComponents(q, settingsComponents)

		if loadErr == nil {
			result.Stored, loadErr = loadStoredSettings(q)
		}

		if loadErr == nil {
			result.OpenRequests, loadErr = loadOpenSettingRequests(q, settingRequestKind)
		}

		if loadErr == nil {
			result.LastChanges, loadErr = loadLastSettingChanges(q)
		}

		return loadErr
	})

	return result, err
}

// SettingsRegistry loads the registry the bot published; empty when it has
// not published one yet.
func (s *PGStore) SettingsRegistry(ctx context.Context) (SettingsRegistry, error) {
	result := SettingsRegistry{}
	err := s.readOnly(ctx, func(q readTx) error {
		components, loadErr := loadStatusComponents(q, []string{componentRegistry})

		if loadErr == nil {
			result, loadErr = decodeRegistry(components)
		}

		return loadErr
	})

	return result, err
}

// QueueSettingRequest inserts a change request for the bot to apply;
// ErrSettingRequestOpen while another one for the key is open.
func (s *PGStore) QueueSettingRequest(ctx context.Context, draft SettingRequestDraft) (SettingRequest, error) {
	result := SettingRequest{}
	value := nullableJSON(draft.Value)
	expected := string(draft.Expected)
	err := s.readWrite(ctx, func(q writeTx) error {
		row := q.tx.QueryRowContext(q.ctx, queueSettingRequestSQL, settingRequestKind, draft.Key, value, expected)
		var scanErr error
		result, scanErr = scanSettingRequest(row)

		return scanErr
	})

	if isOpenRequestViolation(err) == true {
		err = ErrSettingRequestOpen
	}

	if isSizeCheckViolation(err) == true {
		err = ErrSettingValueTooLarge
	}

	return result, err
}

// SettingRequest loads one change request; ErrSettingRequestNotFound when
// there is none.
func (s *PGStore) SettingRequest(ctx context.Context, id int64) (SettingRequest, error) {
	result := SettingRequest{}
	err := s.readOnly(ctx, func(q readTx) error {
		row := q.tx.QueryRowContext(q.ctx, settingRequestSQL, id)
		var scanErr error
		result, scanErr = scanSettingRequest(row)

		return scanErr
	})

	if errors.Is(err, sql.ErrNoRows) == true {
		err = ErrSettingRequestNotFound
	}

	return result, err
}

// SettingsAudit pages the audit log, newest first.
func (s *PGStore) SettingsAudit(ctx context.Context, filter SettingsAuditFilter) ([]SettingsAuditEntry, error) {
	result := []SettingsAuditEntry{}
	keys := pq.Array(filter.Keys)
	prefix := nullableText(filter.KeyPrefix)
	err := s.readOnly(ctx, func(q readTx) error {
		loadErr := q.rows(settingsAuditSQL, func(rows *sql.Rows) error {
			entry, scanErr := scanSettingsAudit(rows)
			result = append(result, entry)

			return scanErr
		}, keys, filter.Before, filter.Limit, prefix)

		return loadErr
	})

	return result, err
}

// loadStatusComponents reads the named bot_runtime_status rows.
func loadStatusComponents(q readTx, names []string) (map[string]StatusComponent, error) {
	result := map[string]StatusComponent{}
	nameArray := pq.Array(names)
	err := q.rows(statusComponentsSQL, func(rows *sql.Rows) error {
		var name string
		var data []byte
		var updatedAt time.Time
		scanErr := rows.Scan(&name, &data, &updatedAt)
		result[name] = StatusComponent{Data: json.RawMessage(data), UpdatedAt: updatedAt}

		return scanErr
	}, nameArray)

	return result, err
}

// loadStoredSettings reads bot_settings by key.
func loadStoredSettings(q readTx) (map[string]StoredSetting, error) {
	result := map[string]StoredSetting{}
	err := q.rows(storedSettingsSQL, func(rows *sql.Rows) error {
		var key string
		var value []byte
		var updatedAt sql.NullTime
		stored := StoredSetting{}
		scanErr := rows.Scan(&key, &value, &updatedAt, &stored.UpdatedBy)
		stored.Value = json.RawMessage(value)
		stored.UpdatedAt = formatNullTime(updatedAt)
		result[key] = stored

		return scanErr
	})

	return result, err
}

// loadOpenSettingRequests reads the open requests of a kind by key.
func loadOpenSettingRequests(q readTx, kind string) (map[string]SettingRequest, error) {
	result := map[string]SettingRequest{}
	statuses := pq.Array(openSettingStatuses)
	err := q.rows(openSettingRequestsSQL, func(rows *sql.Rows) error {
		request, scanErr := scanSettingRequest(rows)
		result[request.Key] = request

		return scanErr
	}, kind, statuses)

	return result, err
}

// loadLastSettingChanges reads the newest audit row by key.
func loadLastSettingChanges(q readTx) (map[string]SettingsAuditEntry, error) {
	result := map[string]SettingsAuditEntry{}
	err := q.rows(lastSettingChangesSQL, func(rows *sql.Rows) error {
		entry, scanErr := scanSettingsAudit(rows)
		result[entry.Key] = entry

		return scanErr
	})

	return result, err
}

// decodeRegistry decodes the published registry; an empty one when it is
// missing.
func decodeRegistry(components map[string]StatusComponent) (SettingsRegistry, error) {
	result := SettingsRegistry{}
	var err error
	component, isPublished := components[componentRegistry]

	if isPublished == true {
		err = json.Unmarshal(component.Data, &result)
	}

	return result, err
}

// scanSettingRequest reads the settingRequestColumns.
func scanSettingRequest(row rowScanner) (SettingRequest, error) {
	result := SettingRequest{}
	var value []byte
	var expected []byte
	var needsApproval sql.NullBool
	var requestError sql.NullString
	var createdAt sql.NullTime
	var expiresAt sql.NullTime
	var decidedAt sql.NullTime
	err := row.Scan(
		&result.ID,
		&result.Kind,
		&result.Key,
		&result.Status,
		&value,
		&expected,
		&needsApproval,
		&requestError,
		&createdAt,
		&expiresAt,
		&decidedAt,
	)

	if err == nil {
		result.Value = rawJSONOrNull(value)
		result.Expected = rawJSONOrNull(expected)
		result.NeedsApproval = nullBool(needsApproval)
		result.Error = nullString(requestError)
		result.CreatedAt = formatNullTime(createdAt)
		result.ExpiresAt = formatNullTime(expiresAt)
		result.DecidedAt = formatNullTime(decidedAt)
	}

	return result, err
}

// scanSettingsAudit reads the settingsAuditColumns.
func scanSettingsAudit(row rowScanner) (SettingsAuditEntry, error) {
	result := SettingsAuditEntry{}
	var oldValue []byte
	var newValue []byte
	var requestID sql.NullInt64
	var note sql.NullString
	var createdAt sql.NullTime
	err := row.Scan(
		&result.ID,
		&result.Key,
		&oldValue,
		&newValue,
		&result.Actor,
		&result.DBRole,
		&requestID,
		&note,
		&createdAt,
	)

	if err == nil {
		result.OldValue = rawJSONOrNull(oldValue)
		result.NewValue = rawJSONOrNull(newValue)
		result.RequestID = nullInt64(requestID)
		result.Note = nullString(note)
		result.CreatedAt = formatNullTime(createdAt)
	}

	return result, err
}

// rawJSONOrNull keeps a JSONB column as raw JSON; NULL becomes JSON null.
func rawJSONOrNull(value []byte) json.RawMessage {
	var result json.RawMessage

	if value != nil {
		result = json.RawMessage(value)
	}

	return result
}

// nullableJSON is a JSONB bind parameter: NULL for a nil value.
func nullableJSON(value json.RawMessage) interface{} {
	var result interface{}

	if value != nil {
		result = string(value)
	}

	return result
}

// nullableText is a text bind parameter: NULL for an empty value.
func nullableText(value string) interface{} {
	var result interface{}

	if value != "" {
		result = value
	}

	return result
}

// nullBool turns a nullable boolean column into a JSON boolean or null.
func nullBool(value sql.NullBool) *bool {
	var result *bool

	if value.Valid == true {
		flag := value.Bool
		result = &flag
	}

	return result
}

// nullInt64 turns a nullable integer column into a JSON number or null.
func nullInt64(value sql.NullInt64) *int64 {
	var result *int64

	if value.Valid == true {
		number := value.Int64
		result = &number
	}

	return result
}

// isOpenRequestViolation reports the one-open-request index refusing an
// insert.
func isOpenRequestViolation(err error) bool {
	var pqErr *pq.Error
	isPQ := errors.As(err, &pqErr)
	result := (isPQ == true) && (string(pqErr.Code) == uniqueViolation) && (pqErr.Constraint == openRequestIndex)

	return result
}

// isSizeCheckViolation reports a pg_column_size check of the request table
// refusing an insert.
func isSizeCheckViolation(err error) bool {
	var pqErr *pq.Error
	isPQ := errors.As(err, &pqErr)
	result := (isPQ == true) && (string(pqErr.Code) == checkViolation) && (slices.Contains(sizeCheckConstraints, pqErr.Constraint) == true)

	return result
}
