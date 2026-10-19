package dashboard

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"
)

// settingsRoleGrants are the production dashboard's settings grants
// (m1-grants.sql); %[1]s is the role.
var settingsRoleGrants = []string{
	"GRANT SELECT ON bot_settings, settings_audit, settings_change_requests, bot_runtime_status TO %[1]s",
	"GRANT INSERT (kind, key, value, expected) ON settings_change_requests TO %[1]s",
	"GRANT USAGE ON SEQUENCE settings_change_requests_id_seq TO %[1]s",
}

// settingsRoleStore connects as a role with only the production dashboard's
// settings grants and default_transaction_read_only on.
func (d integrationDB) settingsRoleStore(t *testing.T) *PGStore {
	t.Helper()

	return d.grantedRoleStore(t, settingsRoleGrants)
}

// grantedRoleStore connects as a new role with only the given grants (each
// a format with %[1]s for the role) and default_transaction_read_only on.
func (d integrationDB) grantedRoleStore(t *testing.T, grants []string) *PGStore {
	t.Helper()
	role := fmt.Sprintf("dashboard_settings_role_%d", time.Now().UnixNano())
	d.exec(t, fmt.Sprintf("CREATE ROLE %s NOLOGIN", role))
	t.Cleanup(func() {
		if _, err := d.db.Exec(fmt.Sprintf("DROP OWNED BY %s", role)); err != nil {
			t.Errorf("drop owned: %v", err)
		}

		if _, err := d.db.Exec(fmt.Sprintf("DROP ROLE %s", role)); err != nil {
			t.Errorf("drop role: %v", err)
		}
	})

	var schema string

	if err := d.db.QueryRow(`SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatalf("schema: %v", err)
	}

	d.exec(t, fmt.Sprintf("GRANT USAGE ON SCHEMA %s TO %s", schema, role))

	for _, grant := range grants {
		d.exec(t, fmt.Sprintf(grant, role))
	}

	parsed, err := url.Parse(d.url)

	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	query := parsed.Query()
	query.Set("default_transaction_read_only", "on")
	query.Set("role", role)
	parsed.RawQuery = query.Encode()
	db, err := sql.Open("postgres", parsed.String())

	if err != nil {
		t.Fatalf("open role: %v", err)
	}

	t.Cleanup(func() { db.Close() })
	store := NewPGStore(db, kyiv)
	store.now = func() time.Time { return integrationNow }

	return store
}

// publishSettingsFixtures writes what the bot publishes and one owner-set
// value, as the bot role.
func publishSettingsFixtures(t *testing.T, d integrationDB) {
	t.Helper()
	d.exec(t, `INSERT INTO bot_runtime_status (component, data) VALUES ('registry', $1::jsonb), ('bot', $2::jsonb), ('settings', $3::jsonb)`,
		testRegistryJSON,
		`{"heartbeat_at": "2026-10-15T11:59:00Z", "applies_requests": true}`,
		`{"platform.tiktok.enabled": {"value": false, "source": "db", "env_set": false}}`)
	d.exec(t, `BEGIN; SET LOCAL app.settings_actor = 'dashboard'; INSERT INTO bot_settings (key, value) VALUES ('platform.tiktok.enabled', 'false'); COMMIT`)
}

func TestSettingsGrantsAllowReadingAndQueuing(t *testing.T) {
	d := openIntegrationDB(t)
	publishSettingsFixtures(t, d)
	store := d.settingsRoleStore(t)
	ctx := context.Background()
	draft := SettingRequestDraft{Key: "paid.instagram.provider_order", Value: json.RawMessage(`["hiker","apify"]`), Expected: json.RawMessage(`["apify","hiker"]`)}
	request, err := store.QueueSettingRequest(ctx, draft)

	if (err != nil) || (request.Status != "queued") || (string(request.Value) != `["hiker", "apify"]`) || (request.ExpiresAt == nil) || (request.DecidedAt != nil) {
		t.Fatalf("queue: %+v, %v", request, err)
	}

	snapshot, err := store.Settings(ctx)

	if err != nil {
		t.Fatalf("settings: %v", err)
	}

	stored, isStored := snapshot.Stored["platform.tiktok.enabled"]
	change, isChanged := snapshot.LastChanges["platform.tiktok.enabled"]
	open, isOpen := snapshot.OpenRequests["paid.instagram.provider_order"]

	if (isStored == false) || (string(stored.Value) != "false") || (stored.UpdatedBy != "dashboard") || (stored.UpdatedAt == nil) {
		t.Errorf("stored = %+v", stored)
	}

	if (isChanged == false) || (change.Actor != "dashboard") || (change.OldValue != nil) || (string(change.NewValue) != "false") {
		t.Errorf("change = %+v", change)
	}

	if (isOpen == false) || (open.ID != request.ID) || (len(snapshot.Components) != 3) {
		t.Errorf("open = %+v, components = %d", open, len(snapshot.Components))
	}

	page, err := buildSettingsPage(snapshot, integrationNow)

	if (err != nil) || (len(page.Settings) != 7) || (page.Applying != true) || (page.Bot["is_stale"] != false) {
		t.Errorf("page = %+v, %v", page, err)
	}
}

func TestSecondOpenSettingRequestIsRefused(t *testing.T) {
	d := openIntegrationDB(t)
	store := d.settingsRoleStore(t)
	ctx := context.Background()
	draft := SettingRequestDraft{Key: "platform.tiktok.enabled", Value: json.RawMessage(`false`), Expected: json.RawMessage(`true`)}
	_, firstErr := store.QueueSettingRequest(ctx, draft)
	_, secondErr := store.QueueSettingRequest(ctx, draft)

	if (firstErr != nil) || (errors.Is(secondErr, ErrSettingRequestOpen) == false) {
		t.Fatalf("first = %v, second = %v", firstErr, secondErr)
	}

	d.exec(t, `UPDATE settings_change_requests SET status = 'applied', decided_at = now() WHERE key = 'platform.tiktok.enabled'`)
	_, thirdErr := store.QueueSettingRequest(ctx, draft)

	if thirdErr != nil {
		t.Errorf("after the first was decided: %v", thirdErr)
	}
}

func TestOversizedStoredValueIsRefusedAsTooLarge(t *testing.T) {
	d := openIntegrationDB(t)
	store := d.settingsRoleStore(t)
	ctx := context.Background()
	numbers := strings.Repeat("1,", (maxSettingValueBytes/2)-2)
	value := json.RawMessage(fmt.Sprintf("[%s1]", numbers))
	draft := SettingRequestDraft{Key: "platform.tiktok.enabled", Value: value, Expected: json.RawMessage(`true`)}
	_, err := store.QueueSettingRequest(ctx, draft)

	if (len(value) > maxSettingValueBytes) || (errors.Is(err, ErrSettingValueTooLarge) == false) {
		t.Fatalf("len = %d, err = %v", len(value), err)
	}
}

func TestResetRequestStoresNullValue(t *testing.T) {
	d := openIntegrationDB(t)
	store := d.settingsRoleStore(t)
	ctx := context.Background()
	draft := SettingRequestDraft{Key: "paid.apify.monthly_budget_usd", Value: nil, Expected: json.RawMessage(`4.8`)}
	request, err := store.QueueSettingRequest(ctx, draft)
	var isNull bool

	if scanErr := d.db.QueryRow(`SELECT value IS NULL FROM settings_change_requests WHERE id = $1`, request.ID).Scan(&isNull); (err != nil) || (scanErr != nil) || (isNull == false) {
		t.Fatalf("queue err = %v, scan err = %v, null = %v", err, scanErr, isNull)
	}

	loaded, err := store.SettingRequest(ctx, request.ID)

	if (err != nil) || (loaded.Value != nil) || (string(loaded.Expected) != "4.8") || (loaded.Key != "paid.apify.monthly_budget_usd") {
		t.Errorf("loaded = %+v, %v", loaded, err)
	}

	_, missingErr := store.SettingRequest(ctx, request.ID+100)

	if errors.Is(missingErr, ErrSettingRequestNotFound) == false {
		t.Errorf("missing = %v", missingErr)
	}
}

func TestSettingsAuditPagesAndFilters(t *testing.T) {
	d := openIntegrationDB(t)
	d.exec(t, `INSERT INTO bot_settings (key, value) VALUES ('platform.tiktok.enabled', 'false'), ('limits.default_daily_media', '30')`)
	d.exec(t, `UPDATE bot_settings SET value = 'true' WHERE key = 'platform.tiktok.enabled'`)
	d.exec(t, `DELETE FROM bot_settings WHERE key = 'limits.default_daily_media'`)
	store := d.settingsRoleStore(t)
	ctx := context.Background()
	all, err := store.SettingsAudit(ctx, SettingsAuditFilter{Limit: 10})

	if (err != nil) || (len(all) != 4) || (all[0].ID < all[1].ID) || (all[0].NewValue != nil) || (all[0].Actor != "manual") || (all[0].DBRole == "") {
		t.Fatalf("all = %+v, %v", all, err)
	}

	page, err := store.SettingsAudit(ctx, SettingsAuditFilter{Before: all[1].ID, Limit: 10})

	if (err != nil) || (len(page) != 2) || (page[0].ID != all[2].ID) {
		t.Errorf("page = %+v, %v", page, err)
	}

	tiktok, err := store.SettingsAudit(ctx, SettingsAuditFilter{Keys: []string{"platform.tiktok.enabled"}, Limit: 10})

	if (err != nil) || (len(tiktok) != 2) || (string(tiktok[0].OldValue) != "false") || (string(tiktok[0].NewValue) != "true") {
		t.Errorf("tiktok = %+v, %v", tiktok, err)
	}

	none, err := store.SettingsAudit(ctx, SettingsAuditFilter{Keys: []string{}, Limit: 10})

	if (err != nil) || (len(none) != 0) {
		t.Errorf("none = %+v, %v", none, err)
	}
}

func TestSettingsRoleCanNotWriteAnythingElse(t *testing.T) {
	d := openIntegrationDB(t)
	store := d.settingsRoleStore(t)
	ctx := context.Background()
	statements := []string{
		`INSERT INTO bot_settings (key, value) VALUES ('platform.tiktok.enabled', 'false')`,
		`UPDATE settings_change_requests SET status = 'applied'`,
		`INSERT INTO settings_change_requests (kind, key, value, expected, status) VALUES ('setting', 'platform.tiktok.enabled', 'false', 'true', 'applied')`,
		`INSERT INTO bot_runtime_status (component, data) VALUES ('bot', '{}')`,
	}

	for _, statement := range statements {
		err := store.readWrite(ctx, func(q writeTx) error {
			_, execErr := q.tx.ExecContext(q.ctx, statement)

			return execErr
		})

		if err == nil {
			t.Errorf("allowed: %s", statement)
		}
	}
}
