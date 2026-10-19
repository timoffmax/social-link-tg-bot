package dashboard

import (
	"context"
	"encoding/json"
	"time"
)

// SettingsStore reads the settings the bot publishes and queues the
// owner's change requests; the bot validates and applies them.
type SettingsStore interface {
	Settings(ctx context.Context) (SettingsSnapshot, error)
	SettingsRegistry(ctx context.Context) (SettingsRegistry, error)
	QueueSettingRequest(ctx context.Context, draft SettingRequestDraft) (SettingRequest, error)
	SettingRequest(ctx context.Context, id int64) (SettingRequest, error)
	SettingsAudit(ctx context.Context, filter SettingsAuditFilter) ([]SettingsAuditEntry, error)
}

// RegistrySetting is one setting of the registry the bot publishes in
// bot_runtime_status 'registry' (the JSON of the bot's settings.Setting).
// Default and SafeValue stay raw, so the page gets them as the bot wrote
// them.
type RegistrySetting struct {
	Key          string          `json:"key"`
	Group        string          `json:"group"`
	Type         string          `json:"type"`
	EnvVar       string          `json:"env_var,omitempty"`
	Default      json.RawMessage `json:"default"`
	Min          *float64        `json:"min,omitempty"`
	MinExclusive bool            `json:"min_exclusive,omitempty"`
	Max          *float64        `json:"max,omitempty"`
	Ceiling      *float64        `json:"ceiling,omitempty"`
	Scale        int             `json:"scale,omitempty"`
	Enum         []string        `json:"enum,omitempty"`
	Approval     string          `json:"approval"`
	SafeValue    json.RawMessage `json:"safe_value,omitempty"`
	Label        string          `json:"label"`
	Impact       string          `json:"impact"`
}

// RegistryPlatform is one platform of the published registry.
type RegistryPlatform struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// SettingsRegistry is the published registry; empty until the bot has
// published it.
type SettingsRegistry struct {
	Groups    []string           `json:"groups"`
	Platforms []RegistryPlatform `json:"platforms"`
	Settings  []RegistrySetting  `json:"settings"`
}

// StatusComponent is one bot_runtime_status row.
type StatusComponent struct {
	Data      json.RawMessage
	UpdatedAt time.Time
}

// StoredSetting is a bot_settings row: the value the owner set.
type StoredSetting struct {
	Value     json.RawMessage `json:"value"`
	UpdatedAt *string         `json:"updated_at"`
	UpdatedBy string          `json:"updated_by"`
}

// SettingRequest is one settings_change_requests row. Value null is a
// reset to .env / the default.
type SettingRequest struct {
	ID            int64           `json:"id"`
	Key           string          `json:"key"`
	Status        string          `json:"status"`
	Value         json.RawMessage `json:"value"`
	Expected      json.RawMessage `json:"expected"`
	NeedsApproval *bool           `json:"needs_approval"`
	Error         *string         `json:"error"`
	CreatedAt     *string         `json:"created_at"`
	ExpiresAt     *string         `json:"expires_at"`
	DecidedAt     *string         `json:"decided_at"`
}

// SettingRequestDraft is a validated request to queue: Value nil resets the
// key; both values are compact JSON.
type SettingRequestDraft struct {
	Key      string
	Value    json.RawMessage
	Expected json.RawMessage
}

// SettingsAuditEntry is one settings_audit row; a null value means unset
// (old) or reset (new).
type SettingsAuditEntry struct {
	ID        int64           `json:"id"`
	Key       string          `json:"key"`
	OldValue  json.RawMessage `json:"old_value"`
	NewValue  json.RawMessage `json:"new_value"`
	Actor     string          `json:"actor"`
	DBRole    string          `json:"db_role"`
	RequestID *int64          `json:"request_id"`
	Note      *string         `json:"note"`
	CreatedAt *string         `json:"created_at"`
}

// SettingsAuditFilter selects audit rows: Keys nil for every key, Before 0
// for the newest, at most Limit rows.
type SettingsAuditFilter struct {
	Keys   []string
	Before int64
	Limit  int
}

// SettingsSnapshot is what GET /api/settings is built from, read in one
// transaction: the published components by name, and the stored values,
// open requests and newest audit rows by key.
type SettingsSnapshot struct {
	Components   map[string]StatusComponent
	Stored       map[string]StoredSetting
	OpenRequests map[string]SettingRequest
	LastChanges  map[string]SettingsAuditEntry
}

// SettingRow is one setting of the page: the registry fields with the value
// the bot uses (null until it published one), the stored value, the open
// request and the newest audit row.
type SettingRow struct {
	RegistrySetting
	Effective   json.RawMessage     `json:"effective"`
	Stored      *StoredSetting      `json:"stored"`
	OpenRequest *SettingRequest     `json:"open_request"`
	LastChange  *SettingsAuditEntry `json:"last_change"`
}

// SettingsPage is the GET /api/settings answer. Bot is the bot's heartbeat
// with is_stale added (null when it never published); Applying reports that
// the bot applies requests.
type SettingsPage struct {
	Meta
	Bot       map[string]interface{} `json:"bot"`
	Profile   json.RawMessage        `json:"profile"`
	Secrets   json.RawMessage        `json:"secrets"`
	Groups    []string               `json:"groups"`
	Platforms []RegistryPlatform     `json:"platforms"`
	Applying  bool                   `json:"applying"`
	Settings  []SettingRow           `json:"settings"`
}

// SettingRequestAnswer is the answer about one change request.
type SettingRequestAnswer struct {
	Meta
	Request SettingRequest `json:"request"`
}

// SettingsAuditPage is the GET /api/settings/audit answer; NextBefore is
// the before value of the next page, null on the last one.
type SettingsAuditPage struct {
	Meta
	Entries    []SettingsAuditEntry `json:"entries"`
	NextBefore *int64               `json:"next_before"`
}
