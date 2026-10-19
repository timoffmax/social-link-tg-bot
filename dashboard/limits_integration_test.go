package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// limitsRoleGrants are the production dashboard's grants the Limits tab
// uses: the settings grants plus SELECT on the special-download tables.
var limitsRoleGrants = append([]string{
	"GRANT SELECT ON gated_fetch_usage, gated_fetch_policies, gated_fetch_credits, gated_fetch_chat_credits TO %[1]s",
	"GRANT SELECT ON media_stats, group_media_limits, support_payments TO %[1]s",
}, settingsRoleGrants...)

func (d integrationDB) limitsRoleStore(t *testing.T) *PGStore {
	t.Helper()

	return d.grantedRoleStore(t, limitsRoleGrants)
}

func scopeKeys(rows []LimitRow) []string {
	result := []string{}

	for _, row := range rows {
		result = append(result, limitKey(row.Scope, row.ScopeID))
	}

	return result
}

func findLimitRow(t *testing.T, rows []LimitRow, key string) LimitRow {
	t.Helper()

	for _, row := range rows {
		if limitKey(row.Scope, row.ScopeID) == key {
			return row
		}
	}

	t.Fatalf("no row %s in %v", key, scopeKeys(rows))

	return LimitRow{}
}

func TestLimitRowsListUsageOnlyPolicyOnlyCreditsOnlyAndOpenRequests(t *testing.T) {
	d := openIntegrationDB(t)
	d.insertMedia(t, media{userID: 1, username: text("alice"), chatID: 1, platform: "instagram", at: ago(day)})
	d.exec(t, `INSERT INTO gated_fetch_usage (scope, scope_id, period, used) VALUES
('global', 0, '2026-10-01', 9), ('user', 1, '2026-10-01', 3), ('user', 9, '2026-08-01', 4)`)
	d.exec(t, `INSERT INTO gated_fetch_policies (scope, scope_id, enabled, monthly_limit) VALUES ('chat', -200, true, 40), ('chat', -500, false, NULL)`)
	d.exec(t, `INSERT INTO gated_fetch_credits (user_id, credits) VALUES (2, 5), (3, 0)`)
	d.exec(t, `INSERT INTO gated_fetch_chat_credits (chat_id, credits) VALUES (-300, 7)`)
	store := d.limitsRoleStore(t)
	ctx := context.Background()
	draft := SettingRequestDraft{Key: "chat:-400", Value: json.RawMessage(`30`), Expected: json.RawMessage(`null`)}
	request, err := store.QueueLimitRequest(ctx, draft)

	if (err != nil) || (request.Kind != limitRequestKind) || (request.Status != "queued") {
		t.Fatalf("queue: %+v, %v", request, err)
	}

	snapshot, err := store.Limits(ctx, defaultLimitRows)

	if err != nil {
		t.Fatalf("limits: %v", err)
	}

	assertEqual(t, "scopes", scopeKeys(snapshot.Rows), []string{"user:1", "chat:-500", "chat:-400", "chat:-300", "chat:-200", "user:2"})
	assertEqual(t, "total", snapshot.Total, int64(6))
	assertEqual(t, "global", snapshot.GlobalUsed, int64(9))
	alice := findLimitRow(t, snapshot.Rows, "user:1")
	group := findLimitRow(t, snapshot.Rows, "chat:-200")
	off := findLimitRow(t, snapshot.Rows, "chat:-500")
	credits := findLimitRow(t, snapshot.Rows, "chat:-300")

	if (alice.Title != "alice") || (alice.Used != 3) || (alice.StoredLimit != nil) || (alice.Enabled == false) {
		t.Errorf("alice = %+v", alice)
	}

	if (group.StoredLimit == nil) || (*group.StoredLimit != 40) || (off.Enabled == true) || (credits.Credits != 7) {
		t.Errorf("group = %+v, off = %+v, credits = %+v", group, off, credits)
	}

	open, isOpen := snapshot.OpenRequests["chat:-400"]

	if (isOpen == false) || (open.ID != request.ID) {
		t.Errorf("open = %+v", snapshot.OpenRequests)
	}

	limited, err := store.Limits(ctx, 2)

	if (err != nil) || (len(limited.Rows) != 2) || (limited.Total != 6) {
		t.Errorf("limited = %+v, %v", limited, err)
	}
}

func TestLimitRowsSkipAllDefaultPolicyRows(t *testing.T) {
	d := openIntegrationDB(t)
	d.exec(t, `INSERT INTO gated_fetch_policies (scope, scope_id, enabled, monthly_limit) VALUES ('user', 10, true, NULL), ('chat', -10, true, NULL)`)
	snapshot, err := d.limitsRoleStore(t).Limits(context.Background(), defaultLimitRows)

	if (err != nil) || (len(snapshot.Rows) != 0) || (snapshot.Total != 0) {
		t.Errorf("snapshot = %+v, %v", snapshot, err)
	}
}

func TestLimitRowsKeepARowAtItsCustomLimitAheadOfBusierRows(t *testing.T) {
	d := openIntegrationDB(t)
	d.exec(t, `INSERT INTO gated_fetch_usage (scope, scope_id, period, used) VALUES
('user', 1, '2026-10-01', 9), ('user', 2, '2026-10-01', 8), ('user', 3, '2026-10-01', 2)`)
	d.exec(t, `INSERT INTO gated_fetch_policies (scope, scope_id, enabled, monthly_limit) VALUES ('user', 3, true, 2)`)
	snapshot, err := d.limitsRoleStore(t).Limits(context.Background(), 2)

	if err != nil {
		t.Fatalf("limits: %v", err)
	}

	assertEqual(t, "scopes", scopeKeys(snapshot.Rows), []string{"user:1", "user:3"})
	assertEqual(t, "total", snapshot.Total, int64(3))
}

func TestLimitRowsLastMonthBoostsAndLastChange(t *testing.T) {
	d := openIntegrationDB(t)
	d.insertMedia(t, media{userID: 2, chatID: -100, title: text("Group A"), platform: "instagram", at: ago(day)})
	d.exec(t, `INSERT INTO gated_fetch_usage (scope, scope_id, period, used) VALUES ('chat', -100, '2026-10-01', 5), ('chat', -100, '2026-09-01', 4)`)
	d.exec(t, `INSERT INTO support_payments (telegram_payment_charge_id, user_id, chat_id, stars, credits, payload, refunded_at, boosted_chat_id) VALUES
('b1', 2, -100, 50, 25, 'support:50:2', NULL, -100), ('b2', 3, -100, 50, 25, 'support:50:3', '2026-10-02T10:00:00Z', -100), ('b3', 4, -100, 25, 10, 'support:25:4', NULL, NULL)`)
	d.exec(t, `INSERT INTO gated_fetch_policies (scope, scope_id, enabled, monthly_limit) VALUES ('chat', -100, true, 20)`)
	d.exec(t, `BEGIN; SET LOCAL app.settings_actor = 'telegram'; UPDATE gated_fetch_policies SET monthly_limit = 25 WHERE scope = 'chat' AND scope_id = -100; COMMIT`)
	snapshot, err := d.limitsRoleStore(t).Limits(context.Background(), defaultLimitRows)

	if (err != nil) || (len(snapshot.Rows) != 1) {
		t.Fatalf("snapshot = %+v, %v", snapshot, err)
	}

	row := snapshot.Rows[0]
	change := row.LastChange

	if (row.Title != "Group A") || (row.Used != 5) || (row.UsedLastMonth != 4) || (row.Boosts != 1) || (*row.StoredLimit != 25) {
		t.Errorf("row = %+v", row)
	}

	if (change == nil) || (change.Actor != "telegram") || (string(change.OldValue) != "20") || (string(change.NewValue) != "25") || (change.At == "") {
		t.Errorf("change = %+v", change)
	}
}

func TestLimitRowsUTCMonthBoundary(t *testing.T) {
	d := openIntegrationDB(t)
	d.exec(t, `INSERT INTO gated_fetch_usage (scope, scope_id, period, used) VALUES
('global', 0, '2026-10-01', 11), ('global', 0, '2026-11-01', 2), ('user', 1, '2026-10-01', 3), ('user', 1, '2026-11-01', 9), ('user', 1, '2026-09-01', 6)`)
	store := d.limitsRoleStore(t)
	// Already 1 November in Kyiv, still October in UTC, where the counters run.
	store.now = func() time.Time { return time.Date(2026, 10, 31, 23, 30, 0, 0, time.UTC) }
	snapshot, err := store.Limits(context.Background(), defaultLimitRows)

	if (err != nil) || (len(snapshot.Rows) != 1) || (snapshot.GlobalUsed != 11) {
		t.Fatalf("snapshot = %+v, %v", snapshot, err)
	}

	row := snapshot.Rows[0]

	if (row.Used != 3) || (row.UsedLastMonth != 6) {
		t.Errorf("row = %+v", row)
	}
}

func TestLimitScopeReturnsStoredRowForUnlistedId(t *testing.T) {
	d := openIntegrationDB(t)
	d.exec(t, `INSERT INTO gated_fetch_policies (scope, scope_id, enabled, monthly_limit) VALUES ('chat', -900, true, 1500)`)
	store := d.limitsRoleStore(t)
	ctx := context.Background()
	stored, err := store.LimitScope(ctx, limitScopeChat, -900)

	if (err != nil) || (len(stored.Rows) != 1) || (stored.Rows[0].StoredLimit == nil) || (*stored.Rows[0].StoredLimit != 1500) || (stored.Rows[0].LastChange == nil) {
		t.Fatalf("stored = %+v, %v", stored, err)
	}

	unknown, err := store.LimitScope(ctx, limitScopeUser, 77)

	if (err != nil) || (len(unknown.Rows) != 1) {
		t.Fatalf("unknown = %+v, %v", unknown, err)
	}

	row := unknown.Rows[0]

	if (row.Scope != "user") || (row.ScopeID != 77) || (row.Used != 0) || (row.StoredLimit != nil) || (row.Enabled == false) || (row.LastChange != nil) || (row.Title != "") {
		t.Errorf("row = %+v", row)
	}
}

func TestPaidComponentPassedThroughWithStaleness(t *testing.T) {
	d := openIntegrationDB(t)
	d.exec(t, `INSERT INTO bot_runtime_status (component, data, updated_at) VALUES
('paid', '{"order": ["apify", "hiker"]}', '2026-10-15T11:50:00Z'),
('relay', '{"configured": false}', '2026-10-15T11:59:00Z'),
('registry', '{"special_limit": {"kind": "special_limit", "min": 0, "max": 1000}}', '2026-10-15T11:59:00Z')`)
	snapshot, err := d.limitsRoleStore(t).Limits(context.Background(), defaultLimitRows)

	if err != nil {
		t.Fatalf("limits: %v", err)
	}

	page, err := buildLimitsPage(snapshot, integrationNow)

	if (err != nil) || (page.Paid == nil) || (page.Paid.IsStale == false) || (string(page.Paid.Data) != `{"order": ["apify", "hiker"]}`) {
		t.Fatalf("paid = %+v, %v", page.Paid, err)
	}

	if (page.Relay == nil) || (page.Relay.IsStale == true) || (page.Rules == nil) || (page.Paid.UpdatedAt != "2026-10-15T11:50:00Z") {
		t.Errorf("relay = %+v, rules = %+v", page.Relay, page.Rules)
	}
}

func TestSettingRequestJSONHasKind(t *testing.T) {
	d := openIntegrationDB(t)
	store := d.settingsRoleStore(t)
	ctx := context.Background()
	draft := SettingRequestDraft{Key: "platform.tiktok.enabled", Value: json.RawMessage(`false`), Expected: json.RawMessage(`true`)}
	request, err := store.QueueSettingRequest(ctx, draft)

	if err != nil {
		t.Fatalf("queue: %v", err)
	}

	loaded, err := store.SettingRequest(ctx, request.ID)
	encoded, _ := json.Marshal(loaded)
	body := map[string]interface{}{}
	_ = json.Unmarshal(encoded, &body)

	if (err != nil) || (body["kind"] != "setting") || (request.Kind != "setting") {
		t.Errorf("body = %v, %v", body, err)
	}
}

func TestSettingsLastChangesIgnoreLimitKeys(t *testing.T) {
	d := openIntegrationDB(t)
	d.exec(t, `INSERT INTO bot_settings (key, value) VALUES ('platform.tiktok.enabled', 'false')`)
	d.exec(t, `INSERT INTO gated_fetch_policies (scope, scope_id, enabled, monthly_limit) VALUES ('chat', -100, false, 20)`)
	snapshot, err := d.settingsRoleStore(t).Settings(context.Background())
	keys := []string{}

	for key := range snapshot.LastChanges {
		keys = append(keys, key)
	}

	if (err != nil) || (len(keys) != 1) || (keys[0] != "platform.tiktok.enabled") {
		t.Errorf("keys = %v, %v", keys, err)
	}
}

func TestAuditLimitsGroupFilter(t *testing.T) {
	d := openIntegrationDB(t)
	d.exec(t, `INSERT INTO bot_settings (key, value) VALUES ('platform.tiktok.enabled', 'false')`)
	d.exec(t, `INSERT INTO gated_fetch_policies (scope, scope_id, enabled, monthly_limit) VALUES ('chat', -100, false, 20)`)
	store := d.settingsRoleStore(t)
	ctx := context.Background()
	limits, err := store.SettingsAudit(ctx, SettingsAuditFilter{KeyPrefix: specialAuditKeyPattern, Limit: 10})
	keys := []string{}

	for _, entry := range limits {
		keys = append(keys, entry.Key)
	}

	if (err != nil) || (len(keys) != 2) || (keys[0] != "special_enabled:chat:-100") || (keys[1] != "special_limit:chat:-100") {
		t.Errorf("keys = %v, %v", keys, err)
	}

	all, err := store.SettingsAudit(ctx, SettingsAuditFilter{Limit: 10})

	if (err != nil) || (len(all) != 3) {
		t.Errorf("all = %+v, %v", all, err)
	}
}

func TestLimitRequestRefusalsUnderTheGrants(t *testing.T) {
	d := openIntegrationDB(t)
	store := d.limitsRoleStore(t)
	ctx := context.Background()
	draft := SettingRequestDraft{Key: "user:7", Value: json.RawMessage(`30`), Expected: json.RawMessage(`-1`)}
	_, firstErr := store.QueueLimitRequest(ctx, draft)
	_, secondErr := store.QueueLimitRequest(ctx, draft)
	odd := SettingRequestDraft{Key: "user:8", Value: json.RawMessage(`{"a": 1}`), Expected: json.RawMessage(`null`)}
	_, oddErr := store.QueueLimitRequest(ctx, odd)

	if (firstErr != nil) || (errors.Is(secondErr, ErrLimitRequestOpen) == false) || (errors.Is(oddErr, ErrLimitRejectedByDB) == false) {
		t.Fatalf("first = %v, second = %v, odd = %v", firstErr, secondErr, oddErr)
	}

	setting := SettingRequestDraft{Key: "user:7", Value: json.RawMessage(`false`), Expected: json.RawMessage(`true`)}
	_, settingErr := store.QueueSettingRequest(ctx, setting)

	if settingErr != nil {
		t.Errorf("an open setting request of the same key must not collide: %v", settingErr)
	}
}

func TestLimitRequestBeforeTheBotMigrationIsNotSupported(t *testing.T) {
	d := openIntegrationDB(t)
	d.exec(t, `DELETE FROM settings_change_requests`)
	d.exec(t, `ALTER TABLE settings_change_requests DROP CONSTRAINT settings_change_requests_kind_check`)
	d.exec(t, `ALTER TABLE settings_change_requests ADD CONSTRAINT settings_change_requests_kind_check CHECK (kind IN ('setting'))`)
	draft := SettingRequestDraft{Key: "user:7", Value: json.RawMessage(`30`), Expected: json.RawMessage(`null`)}
	_, err := d.limitsRoleStore(t).QueueLimitRequest(context.Background(), draft)

	if errors.Is(err, ErrLimitsNotSupported) == false {
		t.Errorf("err = %v", err)
	}
}
