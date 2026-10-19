package botsettings

// Integration tests against a throwaway PostgreSQL (17 in prod). They are
// skipped unless SETTINGS_TEST_DSN is set, e.g.
// SETTINGS_TEST_DSN="postgres://postgres:test@127.0.0.1:55460/postgres?sslmode=disable"
// Each test runs the real migrations (golang-migrate, as the bot does) in
// its own schema, dropped afterwards.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/lib/pq"

	"github.com/timoffmax/social-link-bot/config"
	"github.com/timoffmax/social-link-bot/config/overrides"
	"github.com/timoffmax/social-link-bot/model/settings"
)

const settingsTestDSNKey = "SETTINGS_TEST_DSN"

const migrationsSource = "file://../../migrations"

// settingsMigrationVersion is migration 011; latestMigrationVersion is the
// last migration (014).
const (
	settingsMigrationVersion = 11
	latestMigrationVersion   = 14
)

// pgFixture is a schema with the migrations applied.
type pgFixture struct {
	db     *sql.DB
	admin  *sql.DB
	schema string
}

func openPGFixture(t *testing.T) pgFixture {
	baseURL := os.Getenv(settingsTestDSNKey)

	if baseURL == "" {
		t.Skipf("%s is not set", settingsTestDSNKey)
	}

	admin, err := sql.Open("postgres", baseURL)

	if err != nil {
		t.Fatalf("open: %v", err)
	}

	schema := fmt.Sprintf("settings_test_%d", time.Now().UnixNano())
	mustExecPG(t, admin, fmt.Sprintf("CREATE SCHEMA %s", schema))
	t.Cleanup(func() {
		mustExecPG(t, admin, fmt.Sprintf("DROP SCHEMA %s CASCADE", schema))
		admin.Close()
	})
	parsed, err := url.Parse(baseURL)

	if err != nil {
		t.Fatalf("%s must be a postgres:// URL: %v", settingsTestDSNKey, err)
	}

	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	db, err := sql.Open("postgres", parsed.String())

	if err != nil {
		t.Fatalf("open schema: %v", err)
	}

	t.Cleanup(func() { db.Close() })
	fixture := pgFixture{db: db, admin: admin, schema: schema}
	runMigrate(t, db, func(m *migrate.Migrate) error { return m.Up() })

	return fixture
}

// runMigrate runs a golang-migrate step on db, as services.InitDatabase does.
func runMigrate(t *testing.T, db *sql.DB, step func(m *migrate.Migrate) error) {
	driver, err := postgres.WithInstance(db, &postgres.Config{})

	if err != nil {
		t.Fatalf("migrate driver: %v", err)
	}

	m, err := migrate.NewWithDatabaseInstance(migrationsSource, "postgres", driver)

	if err != nil {
		t.Fatalf("migrate: %v", err)
	}

	if err := step(m); (err != nil) && (errors.Is(err, migrate.ErrNoChange) == false) {
		t.Fatalf("migrate step: %v", err)
	}
}

func mustExecPG(t *testing.T, db *sql.DB, query string, args ...any) {
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

func (f pgFixture) tableExists(t *testing.T, table string) bool {
	var exists bool
	err := f.db.QueryRow(`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = $1 AND table_name = $2)`, f.schema, table).Scan(&exists)

	if err != nil {
		t.Fatalf("table check: %v", err)
	}

	return exists
}

// queueRequest inserts a request the way the dashboard will: only kind,
// key, value and expected.
func (f pgFixture) queueRequest(t *testing.T, key string, value any, expected string) int64 {
	var id int64
	err := f.db.QueryRow(`INSERT INTO settings_change_requests (kind, key, value, expected) VALUES ('setting', $1, $2, $3) RETURNING id`, key, value, expected).Scan(&id)

	if err != nil {
		t.Fatalf("queue: %v", err)
	}

	return id
}

func (f pgFixture) requestStatus(t *testing.T, id int64) (string, string) {
	var status string
	var reason sql.NullString
	err := f.db.QueryRow(`SELECT status, error FROM settings_change_requests WHERE id = $1`, id).Scan(&status, &reason)

	if err != nil {
		t.Fatalf("status: %v", err)
	}

	return status, reason.String
}

var settingsTables = []string{"bot_settings", "settings_audit", "settings_change_requests", "bot_runtime_status"}

func TestMigration011DownAndUpAgain(t *testing.T) {
	fixture := openPGFixture(t)

	for _, table := range settingsTables {
		if fixture.tableExists(t, table) == false {
			t.Fatalf("%s missing after up", table)
		}
	}

	runMigrate(t, fixture.db, func(m *migrate.Migrate) error { return m.Migrate(settingsMigrationVersion - 1) })

	for _, table := range settingsTables {
		if fixture.tableExists(t, table) == true {
			t.Fatalf("%s left after down", table)
		}
	}

	if fixture.tableExists(t, "paid_requests") == false {
		t.Fatalf("down 011 must keep migration 010's table")
	}

	runMigrate(t, fixture.db, func(m *migrate.Migrate) error { return m.Up() })
	var version uint
	var isDirty bool
	err := fixture.db.QueryRow(`SELECT version, dirty FROM schema_migrations`).Scan(&version, &isDirty)

	if (err != nil) || (version != latestMigrationVersion) || (isDirty == true) {
		t.Fatalf("version %d dirty %v: %v", version, isDirty, err)
	}

	for _, table := range settingsTables {
		var count int

		if err := fixture.db.QueryRow(fmt.Sprintf("SELECT count(*) FROM %s", table)).Scan(&count); (err != nil) || (count != 0) {
			t.Fatalf("%s must start empty: %d %v", table, count, err)
		}
	}
}

func TestEmptyDatabaseLoadChangesNoGetter(t *testing.T) {
	fixture := openPGFixture(t)
	clearSettingsEnv(t)
	t.Setenv("ENABLE_PROXIES", "false")
	t.Setenv("STARS_SUPPORT_ENABLED", "true")
	t.Setenv("APIFY_MONTHLY_BUDGET_USD", "4.8")
	harness := newHarness(t, NewPGStore(fixture.db))
	harness.manager.Load(context.Background())
	assertGettersUseFallbacks(t)
	var settingsBlob []byte
	err := fixture.db.QueryRow(`SELECT data FROM bot_runtime_status WHERE component = 'settings'`).Scan(&settingsBlob)
	published := map[string]config.EffectiveSetting{}

	if err == nil {
		err = json.Unmarshal(settingsBlob, &published)
	}

	if (err != nil) || (published[settings.KeyApifyMonthlyBudgetUsd].Value != 4.8) || (published[settings.KeyApifyMonthlyBudgetUsd].Source != settings.SourceEnv) || (len(published) != len(settings.All())) {
		t.Fatalf("published settings: %v %+v", err, published)
	}

	var components int
	_ = fixture.db.QueryRow(`SELECT count(*) FROM bot_runtime_status`).Scan(&components)

	if components != 5 {
		t.Fatalf("expected 5 status components, got %d", components)
	}
}

func TestPGApplyWritesTheAuditWithActorAndRole(t *testing.T) {
	fixture := openPGFixture(t)
	clearSettingsEnv(t)
	harness := newHarness(t, NewPGStore(fixture.db))
	harness.manager.Load(context.Background())
	id := fixture.queueRequest(t, settings.KeyPlatformTikTokEnabled, `false`, `true`)
	harness.manager.processRequests(context.Background())

	if status, reason := fixture.requestStatus(t, id); (status != settings.StatusApplied) || (config.IsTikTokEnabled() == true) {
		t.Fatalf("got %s (%s), TikTok %v", status, reason, config.IsTikTokEnabled())
	}

	var actor, role, updatedBy, writer string
	var requestID int64
	_ = fixture.db.QueryRow(`SELECT current_user`).Scan(&writer)
	err := fixture.db.QueryRow(`SELECT a.actor, a.db_role, a.request_id, s.updated_by FROM settings_audit a JOIN bot_settings s ON s.key = a.key WHERE a.key = $1`, settings.KeyPlatformTikTokEnabled).Scan(&actor, &role, &requestID, &updatedBy)

	if (err != nil) || (actor != settings.ActorDashboard) || (role != writer) || (requestID != id) || (updatedBy != settings.ActorDashboard) {
		t.Fatalf("audit: %s %s %d %s %v", actor, role, requestID, updatedBy, err)
	}

	reset := fixture.queueRequest(t, settings.KeyPlatformTikTokEnabled, nil, `false`)
	harness.manager.processRequests(context.Background())
	var rows, audits int
	_ = fixture.db.QueryRow(`SELECT count(*) FROM bot_settings`).Scan(&rows)
	_ = fixture.db.QueryRow(`SELECT count(*) FROM settings_audit WHERE new_value IS NULL AND request_id = $1`, reset).Scan(&audits)

	if (rows != 0) || (audits != 1) || (config.IsTikTokEnabled() == false) {
		t.Fatalf("reset: %d rows, %d audit rows, TikTok %v", rows, audits, config.IsTikTokEnabled())
	}
}

func TestPGManualEditIsAuditedAsManualAndReloaded(t *testing.T) {
	fixture := openPGFixture(t)
	clearSettingsEnv(t)
	harness := newHarness(t, NewPGStore(fixture.db))
	harness.manager.Load(context.Background())
	mustExecPG(t, fixture.db, `INSERT INTO bot_settings (key, value, updated_by) VALUES ($1, 'false', 'dashboard')`, settings.KeyPlatformRedditEnabled)
	harness.manager.reload(context.Background())
	var actor, updatedBy string
	_ = fixture.db.QueryRow(`SELECT a.actor, s.updated_by FROM settings_audit a JOIN bot_settings s ON s.key = a.key`).Scan(&actor, &updatedBy)

	if (actor != settings.ActorManual) || (updatedBy != settings.ActorManual) || (config.IsRedditEnabled() == true) {
		t.Fatalf("a psql edit must be audited as manual and reloaded: %s %s %v", actor, updatedBy, config.IsRedditEnabled())
	}
}

func TestDuplicateOpenRequestIsRefusedByTheDatabase(t *testing.T) {
	fixture := openPGFixture(t)
	clearSettingsEnv(t)
	fixture.queueRequest(t, settings.KeyDefaultDailyMediaLimit, `30`, `0`)
	_, err := fixture.db.Exec(`INSERT INTO settings_change_requests (kind, key, value, expected) VALUES ('setting', $1, '40', '0')`, settings.KeyDefaultDailyMediaLimit)
	var pqErr *pq.Error

	if (errors.As(err, &pqErr) == false) || (pqErr.Code != "23505") {
		t.Fatalf("a second open request must be a unique violation, got %v", err)
	}

	harness := newHarness(t, NewPGStore(fixture.db))
	harness.manager.Load(context.Background())
	harness.manager.processRequests(context.Background())
	fixture.queueRequest(t, settings.KeyDefaultDailyMediaLimit, `40`, `30`)
	fixture.queueRequest(t, settings.KeyStarsEnabled, `true`, `false`)
}

func TestPGExpiredRequestIsNotApplied(t *testing.T) {
	fixture := openPGFixture(t)
	clearSettingsEnv(t)
	harness := newHarness(t, NewPGStore(fixture.db))
	harness.manager.Load(context.Background())
	id := fixture.queueRequest(t, settings.KeyPlatformTikTokEnabled, `false`, `true`)
	mustExecPG(t, fixture.db, `UPDATE settings_change_requests SET expires_at = now() - INTERVAL '1 second' WHERE id = $1`, id)
	harness.manager.processRequests(context.Background())

	if status, _ := fixture.requestStatus(t, id); (status != settings.StatusExpired) || (config.IsTikTokEnabled() == false) {
		t.Fatalf("got %s, TikTok %v", status, config.IsTikTokEnabled())
	}
}

func TestPGConstraints(t *testing.T) {
	fixture := openPGFixture(t)
	mustExecPG(t, fixture.db, `INSERT INTO bot_settings (key, value) VALUES ('stars.enabled', 'true')`)
	refused := []string{
		`UPDATE settings_audit SET actor = 'dashboard'`,
		`DELETE FROM settings_audit`,
		`TRUNCATE settings_audit`,
		`TRUNCATE bot_settings`,
		`UPDATE bot_settings SET key = 'stars.other'`,
		`INSERT INTO bot_settings (key, value) VALUES ('Bad Key', 'true')`,
		fmt.Sprintf(`INSERT INTO settings_change_requests (key, value, expected) VALUES ('a.b', '"%s"', 'true')`, strings.Repeat("x", 3000)),
		`INSERT INTO settings_change_requests (key, value) VALUES ('a.c', 'true')`,
		`INSERT INTO settings_change_requests (kind, key, value, expected) VALUES ('chat_policy', 'a.d', 'true', 'true')`,
	}

	for _, statement := range refused {
		if _, err := fixture.db.Exec(statement); err == nil {
			t.Fatalf("must be refused: %s", statement)
		}
	}
}

// dashboardGrants are the grants of m1-grants.sql (an owner step on the
// server), for role %[1]s.
const dashboardGrants = `
	GRANT SELECT ON bot_settings, settings_audit, settings_change_requests, bot_runtime_status TO %[1]s;
	GRANT INSERT (kind, key, value, expected) ON settings_change_requests TO %[1]s;
	GRANT USAGE ON SEQUENCE settings_change_requests_id_seq TO %[1]s;`

func TestDashboardGrantsAllowOnlyQueuingARequest(t *testing.T) {
	fixture := openPGFixture(t)
	role := fmt.Sprintf("sl_dash_m1_%d", time.Now().UnixNano())
	mustExecPG(t, fixture.admin, fmt.Sprintf("CREATE ROLE %s", role))
	t.Cleanup(func() {
		mustExecPG(t, fixture.admin, fmt.Sprintf("DROP OWNED BY %s", role))
		mustExecPG(t, fixture.admin, fmt.Sprintf("DROP ROLE %s", role))
	})
	mustExecPG(t, fixture.db, fmt.Sprintf("GRANT USAGE ON SCHEMA %s TO %s", fixture.schema, role))
	mustExecPG(t, fixture.db, fmt.Sprintf(dashboardGrants, role))
	asRole := func(statement string) error {
		tx, err := fixture.db.Begin()

		if err == nil {
			_, err = tx.Exec(fmt.Sprintf("SET LOCAL ROLE %s", role))
		}

		if err == nil {
			_, err = tx.Exec(statement)
		}

		if err == nil {
			err = tx.Commit()
		}

		if err != nil {
			_ = tx.Rollback()
		}

		return err
	}
	allowed := []string{
		`INSERT INTO settings_change_requests (kind, key, value, expected) VALUES ('setting', 'stars.enabled', 'true', 'false')`,
		`SELECT * FROM bot_settings, settings_audit, bot_runtime_status`,
		`SELECT * FROM settings_change_requests`,
	}
	refused := []string{
		`INSERT INTO settings_change_requests (key, value, expected, status) VALUES ('proxy.enabled', 'true', 'true', 'applied')`,
		`INSERT INTO settings_change_requests (key, value, expected, source) VALUES ('proxy.enabled', 'true', 'true', 'telegram')`,
		`INSERT INTO settings_change_requests (key, value, expected, requested_role) VALUES ('proxy.enabled', 'true', 'true', 'bot')`,
		`INSERT INTO settings_change_requests (key, value, expected, expires_at) VALUES ('proxy.enabled', 'true', 'true', now() + INTERVAL '1 day')`,
		`UPDATE settings_change_requests SET status = 'applied'`,
		`INSERT INTO bot_settings (key, value) VALUES ('stars.enabled', 'true')`,
		`UPDATE bot_settings SET value = 'true'`,
		`DELETE FROM bot_settings`,
		`INSERT INTO settings_audit (key, actor) VALUES ('stars.enabled', 'bot')`,
		`INSERT INTO bot_runtime_status (component, data) VALUES ('bot', '{}')`,
	}

	for _, statement := range allowed {
		if err := asRole(statement); err != nil {
			t.Fatalf("the dashboard role must be allowed: %s: %v", statement, err)
		}
	}

	for _, statement := range refused {
		if err := asRole(statement); (err == nil) || (strings.Contains(err.Error(), "permission denied") == false) {
			t.Fatalf("the dashboard role must be refused: %s: %v", statement, err)
		}
	}

	var requestedRole string
	_ = fixture.db.QueryRow(`SELECT requested_role FROM settings_change_requests`).Scan(&requestedRole)

	if requestedRole != role {
		t.Fatalf("requested_role must be the inserting role, got %q", requestedRole)
	}
}

func TestPGApplyDuringReloadKeepsTheValue(t *testing.T) {
	fixture := openPGFixture(t)
	clearSettingsEnv(t)
	harness := newHarness(t, NewPGStore(fixture.db))
	harness.manager.Load(context.Background())
	harness.manager.applyRequests = true
	harness.manager.requestEvery = 2 * time.Millisecond
	harness.manager.reloadEvery = 10 * time.Millisecond
	release := make(chan struct{})
	queued := make(chan int64, 1)
	isFirst := true
	harness.manager.afterReloadRead = func() {
		if isFirst == true {
			isFirst = false
			queued <- fixture.queueRequest(t, settings.KeyPlatformTikTokEnabled, `false`, `true`)
			<-release
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	harness.manager.wg.Add(1)
	go harness.manager.Run(ctx)
	id := <-queued
	time.Sleep(30 * time.Millisecond)
	statusDuringReload, _ := fixture.requestStatus(t, id)
	close(release)
	deadline := time.Now().Add(3 * time.Second)

	for (time.Now().Before(deadline) == true) && (config.IsTikTokEnabled() == true) {
		time.Sleep(2 * time.Millisecond)
	}

	time.Sleep(50 * time.Millisecond)
	cancel()
	harness.manager.Wait()
	statusAfter, _ := fixture.requestStatus(t, id)

	if (statusDuringReload != settings.StatusQueued) || (statusAfter != settings.StatusApplied) || (config.IsTikTokEnabled() == true) {
		t.Fatalf("during reload %s, after %s, TikTok %v", statusDuringReload, statusAfter, config.IsTikTokEnabled())
	}
}

func TestPGDatabaseDownAtStartUsesTheCacheFile(t *testing.T) {
	fixture := openPGFixture(t)
	clearSettingsEnv(t)
	first := newHarness(t, NewPGStore(fixture.db))
	first.manager.Load(context.Background())
	fixture.queueRequest(t, settings.KeyPlatformTikTokEnabled, `false`, `true`)
	first.manager.processRequests(context.Background())
	overrides.Set(nil)
	closed, err := sql.Open("postgres", os.Getenv(settingsTestDSNKey))

	if err != nil {
		t.Fatalf("open: %v", err)
	}

	closed.Close()
	second := newHarness(t, NewPGStore(closed))
	second.manager.cachePath = first.cache
	second.manager.Load(context.Background())
	tiktok, _ := config.GetEffectiveSetting(settings.KeyPlatformTikTokEnabled)

	if (tiktok.Value != false) || (tiktok.Source != settings.SourceCache) || (len(*second.alerts) != 1) {
		t.Fatalf("the cache must keep TikTok off with one alert: %+v %v", tiktok, *second.alerts)
	}
}

func TestPGLoopLeavesRequestsQueuedWithoutApplyRequests(t *testing.T) {
	fixture := openPGFixture(t)
	clearSettingsEnv(t)
	harness := newHarness(t, NewPGStore(fixture.db))
	harness.manager.Load(context.Background())
	harness.manager.requestEvery = 2 * time.Millisecond
	harness.manager.reloadEvery = 10 * time.Millisecond
	id := fixture.queueRequest(t, settings.KeyPlatformTikTokEnabled, `false`, `true`)
	ctx, cancel := context.WithCancel(context.Background())
	harness.manager.wg.Add(1)
	go harness.manager.Run(ctx)
	time.Sleep(100 * time.Millisecond)
	cancel()
	harness.manager.Wait()
	status, _ := fixture.requestStatus(t, id)

	if (status != settings.StatusQueued) || (config.IsTikTokEnabled() == false) {
		t.Fatalf("the request must stay queued and TikTok on: %s, TikTok %v", status, config.IsTikTokEnabled())
	}
}

// TestPGExpiryFreesTheKeyWithoutApplyRequests: with applying off, a queued
// request past its expires_at is marked expired, which frees its key for a
// new request; a fresh one stays queued.
func TestPGExpiryFreesTheKeyWithoutApplyRequests(t *testing.T) {
	fixture := openPGFixture(t)
	clearSettingsEnv(t)
	harness := newHarness(t, NewPGStore(fixture.db))
	harness.manager.Load(context.Background())
	stale := fixture.queueRequest(t, settings.KeyPlatformTikTokEnabled, `false`, `true`)
	fresh := fixture.queueRequest(t, settings.KeyPlatformRedditEnabled, `false`, `true`)
	mustExecPG(t, fixture.db, `UPDATE settings_change_requests SET expires_at = now() - INTERVAL '1 minute' WHERE id = $1`, stale)
	harness.manager.processRequestsIfEnabled(context.Background())
	staleStatus, staleErr := fixture.requestStatus(t, stale)
	freshStatus, _ := fixture.requestStatus(t, fresh)

	if (staleStatus != settings.StatusExpired) || (staleErr != requestExpiredError) || (freshStatus != settings.StatusQueued) {
		t.Fatalf("got stale %s (%s), fresh %s", staleStatus, staleErr, freshStatus)
	}

	again := fixture.queueRequest(t, settings.KeyPlatformTikTokEnabled, `false`, `true`)

	if againStatus, _ := fixture.requestStatus(t, again); (againStatus != settings.StatusQueued) || (config.IsTikTokEnabled() == false) {
		t.Fatalf("the key must take a new request and nothing may apply: %s, TikTok %v", againStatus, config.IsTikTokEnabled())
	}
}
