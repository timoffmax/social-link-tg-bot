package dashboard

import (
	"context"
	"encoding/json"
)

// Limits tab: the request kind, the scopes, the bot_runtime_status
// components it reads and its routes.
const (
	limitRequestKind  = "special_limit"
	limitScopeUser    = "user"
	limitScopeChat    = "chat"
	limitAuditPrefix  = "special_limit:"
	componentPaid     = "paid"
	componentRelay    = "relay"
	componentServices = "services"
	limitNearRatio    = 0.8
	defaultLimitRows  = 300
	maxLimitRows      = 1000
	limitsPath        = "/api/limits"
	limitScopePath    = "/api/limits/scope"
	limitRequestsPath = "/api/limits/requests"
)

// Limit sources and row states.
const (
	limitSourceDefault   = "default"
	limitSourceCustom    = "custom"
	limitSourceUnlimited = "unlimited"
	limitStateOK         = "ok"
	limitStateNear       = "near"
	limitStateFull       = "full"
	limitStateBlocked    = "blocked"
	limitStateOff        = "off"
	limitStateUnlimited  = "unlimited"
)

// The stored monthly_limit of an unlimited scope.
const limitUnlimited = int64(-1)

// Effective settings the Limits tab reads from the 'settings' component.
const (
	settingUserAllowance = "gated.user_monthly_allowance"
	settingChatCap       = "gated.chat_monthly_cap"
	settingGlobalCap     = "gated.global_monthly_cap"
)

// limitsComponents are the components GET /api/limits loads.
var limitsComponents = []string{componentBot, componentSettings, componentRegistry, componentPaid, componentRelay}

// limitRowsParam is the page size of GET /api/limits.
var limitRowsParam = intParam{name: paramLimit, fallback: defaultLimitRows, min: minLimit, max: maxLimitRows}

// LimitsStore reads the per-person and per-group special-download limits
// and queues the owner's limit changes; the bot applies them.
type LimitsStore interface {
	Limits(ctx context.Context, rows int) (LimitsSnapshot, error)
	LimitScope(ctx context.Context, scope string, id int64) (LimitsSnapshot, error)
	QueueLimitRequest(ctx context.Context, draft SettingRequestDraft) (SettingRequest, error)
}

// LimitsSnapshot is what the Limits answers are built from, read in one
// transaction: the published components, this month's bot-wide use, the
// rows as stored (not yet resolved) and the open limit requests by key.
type LimitsSnapshot struct {
	Components   map[string]StatusComponent
	GlobalUsed   int64
	Rows         []LimitRow
	Total        int64
	OpenRequests map[string]SettingRequest
}

// SpecialLimitRules are the limit request rules the bot publishes in
// registry.special_limit.
type SpecialLimitRules struct {
	Kind      string `json:"kind"`
	Min       int64  `json:"min"`
	Max       int64  `json:"max"`
	Unlimited bool   `json:"unlimited"`
}

// LimitDefaults are the default monthly limits; null until the bot has
// published its settings.
type LimitDefaults struct {
	User *int64 `json:"user"`
	Chat *int64 `json:"chat"`
}

// GlobalUsage is this month's bot-wide use against the cap (null: no cap).
type GlobalUsage struct {
	Used  int64  `json:"used"`
	Cap   *int64 `json:"cap"`
	IsOff bool   `json:"off"`
}

// PublishedComponent is a bot_runtime_status component as published, with
// its age check: stale when older than heartbeatStaleAfter.
type PublishedComponent struct {
	Data      json.RawMessage `json:"data"`
	UpdatedAt string          `json:"updated_at"`
	IsStale   bool            `json:"is_stale"`
}

// LimitChange is the newest audit row of a scope's limit.
type LimitChange struct {
	At       string          `json:"at"`
	Actor    string          `json:"actor"`
	OldValue json.RawMessage `json:"old_value"`
	NewValue json.RawMessage `json:"new_value"`
}

// LimitRow is one person or group: this and last month's use, the stored
// limit (sent back as expected) and the limit the bot applies.
type LimitRow struct {
	Scope          string          `json:"scope"`
	ScopeID        int64           `json:"scope_id"`
	Title          string          `json:"title"`
	Used           int64           `json:"used"`
	UsedLastMonth  int64           `json:"used_last_month"`
	StoredLimit    *int64          `json:"stored_limit"`
	Source         string          `json:"limit_source"`
	EffectiveLimit *int64          `json:"effective_limit"`
	Remaining      *int64          `json:"remaining"`
	Enabled        bool            `json:"enabled"`
	State          string          `json:"state"`
	Credits        int64           `json:"credits"`
	Boosts         int64           `json:"boosts"`
	OpenRequest    *SettingRequest `json:"open_request"`
	LastChange     *LimitChange    `json:"last_change"`
}

// LimitsPage is the GET /api/limits answer. Period is the UTC month the
// counters run in; Paid and Relay are null until the bot publishes them.
type LimitsPage struct {
	Meta
	Period    string                 `json:"period"`
	ResetsAt  string                 `json:"resets_at"`
	Bot       map[string]interface{} `json:"bot"`
	Applying  bool                   `json:"applying"`
	Rules     *SpecialLimitRules     `json:"rules"`
	Defaults  LimitDefaults          `json:"defaults"`
	Global    GlobalUsage            `json:"global"`
	Paid      *PublishedComponent    `json:"paid"`
	Relay     *PublishedComponent    `json:"relay"`
	Rows      []LimitRow             `json:"rows"`
	Total     int64                  `json:"total"`
	Truncated bool                   `json:"truncated"`
}

// LimitScopeResponse is the GET /api/limits/scope answer: one person or
// group as stored, also when nothing is stored for it.
type LimitScopeResponse struct {
	Meta
	Row      LimitRow           `json:"row"`
	Defaults LimitDefaults      `json:"defaults"`
	Rules    *SpecialLimitRules `json:"rules"`
	Applying bool               `json:"applying"`
}

// limitRequestBody is the body of POST /api/limits/requests; limit and
// expected stay raw, so a missing field is told from null.
type limitRequestBody struct {
	Scope    string          `json:"scope"`
	ID       int64           `json:"id"`
	Limit    json.RawMessage `json:"limit"`
	Expected json.RawMessage `json:"expected"`
}
