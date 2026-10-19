package botsettings

// Integration tests of the special-limit requests and the policy audit
// (migration 012); skipped unless SETTINGS_TEST_DSN is set (see
// pg_integration_test.go).

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/lib/pq"

	"github.com/timoffmax/social-link-bot/model/settings"
	"github.com/timoffmax/social-link-bot/services"
)

// checkViolation is PostgreSQL's check_violation code.
const checkViolation = "23514"

// queueLimitRequest inserts a special-limit request the way the dashboard
// does: only kind, key, value (nil = SQL NULL) and expected.
func (f pgFixture) queueLimitRequest(t *testing.T, key string, value any, expected string) int64 {
	var id int64
	err := f.db.QueryRow(`INSERT INTO settings_change_requests (kind, key, value, expected) VALUES ('special_limit', $1, $2, $3) RETURNING id`, key, value, expected).Scan(&id)

	if err != nil {
		t.Fatalf("queue limit: %v", err)
	}

	return id
}

// policy reads a policy row; false when there is none.
func (f pgFixture) policy(t *testing.T, scope string, id int64) (bool, sql.NullInt64, bool) {
	var enabled bool
	var limit sql.NullInt64
	err := f.db.QueryRow(`SELECT enabled, monthly_limit FROM gated_fetch_policies WHERE scope = $1 AND scope_id = $2`, scope, id).Scan(&enabled, &limit)
	isFound := (err == nil)

	if (err != nil) && (errors.Is(err, sql.ErrNoRows) == false) {
		t.Fatalf("policy: %v", err)
	}

	return enabled, limit, isFound
}

// policyAudit is one settings_audit row of a policy.
type policyAudit struct {
	key       string
	oldValue  sql.NullString
	newValue  sql.NullString
	actor     string
	requestID sql.NullInt64
	note      sql.NullString
}

// policyAudits reads the audit rows of the special_ keys, oldest first.
func (f pgFixture) policyAudits(t *testing.T) []policyAudit {
	rows, err := f.db.Query(`SELECT key, old_value::text, new_value::text, actor, request_id, note FROM settings_audit WHERE key LIKE 'special\_%' ESCAPE '\' ORDER BY id`)

	if err != nil {
		t.Fatalf("audit: %v", err)
	}

	defer rows.Close()
	result := []policyAudit{}

	for rows.Next() == true {
		audit := policyAudit{}

		if err := rows.Scan(&audit.key, &audit.oldValue, &audit.newValue, &audit.actor, &audit.requestID, &audit.note); err != nil {
			t.Fatalf("scan audit: %v", err)
		}

		result = append(result, audit)
	}

	return result
}

// loadedPGHarness is a manager on the fixture's database, loaded.
func loadedPGHarness(t *testing.T, fixture pgFixture) testHarness {
	clearSettingsEnv(t)
	t.Setenv("GATED_FETCH_USER_MONTHLY_ALLOWANCE", "20")
	t.Setenv("GATED_FETCH_CHAT_MONTHLY_CAP", "20")
	result := newHarness(t, NewPGStore(fixture.db))
	result.manager.Load(context.Background())

	return result
}

// useServicesDatabase points the services package at the fixture.
func useServicesDatabase(t *testing.T, fixture pgFixture) {
	restore := services.UseDatabaseHandle(fixture.db)
	t.Cleanup(restore)
}

// pqCode is the PostgreSQL error code of err, or "".
func pqCode(err error) string {
	var pqErr *pq.Error
	result := ""

	if errors.As(err, &pqErr) == true {
		result = string(pqErr.Code)
	}

	return result
}

func TestMigration012DownAndUpAgain(t *testing.T) {
	fixture := openPGFixture(t)
	fixture.queueLimitRequest(t, "chat:-1001", "30", "null")
	mustExecPG(t, fixture.db, `INSERT INTO gated_fetch_policies (scope, scope_id, enabled, monthly_limit) VALUES ('user', 7, true, 50)`)
	runMigrate(t, fixture.db, func(m *migrate.Migrate) error { return m.Migrate(settingsMigrationVersion) })
	var requests, audits int
	_ = fixture.db.QueryRow(`SELECT count(*) FROM settings_change_requests`).Scan(&requests)
	_ = fixture.db.QueryRow(`SELECT count(*) FROM settings_audit`).Scan(&audits)
	_, limit, isFound := fixture.policy(t, "user", 7)

	if (requests != 0) || (audits != 1) || (isFound == false) || (limit.Int64 != 50) || (fixture.tableExists(t, "relay_requests") == true) {
		t.Fatalf("down: %d requests, %d audits, policy %v %v", requests, audits, limit, isFound)
	}

	_, err := fixture.db.Exec(`INSERT INTO settings_change_requests (kind, key, value, expected) VALUES ('special_limit', 'user:7', '30', '50')`)

	if pqCode(err) != checkViolation {
		t.Fatalf("down must narrow the kind CHECK again: %v", err)
	}

	mustExecPG(t, fixture.db, `UPDATE gated_fetch_policies SET monthly_limit = 60`)
	_ = fixture.db.QueryRow(`SELECT count(*) FROM settings_audit`).Scan(&audits)

	if audits != 1 {
		t.Fatalf("down must drop the policy audit, got %d rows", audits)
	}

	runMigrate(t, fixture.db, func(m *migrate.Migrate) error { return m.Up() })
	fixture.queueLimitRequest(t, "user:7", "30", "60")
	var version uint
	var isDirty bool
	err = fixture.db.QueryRow(`SELECT version, dirty FROM schema_migrations`).Scan(&version, &isDirty)

	if (err != nil) || (version != latestMigrationVersion) || (isDirty == true) || (fixture.tableExists(t, "relay_requests") == false) {
		t.Fatalf("version %d dirty %v: %v", version, isDirty, err)
	}
}

// grantDashboardRole creates a role with the dashboard's grants (plus the
// SELECT on the policies it already has) and returns a runner of
// statements as that role.
func grantDashboardRole(t *testing.T, fixture pgFixture) func(statement string) error {
	role := fmt.Sprintf("sl_dash_limits_%d", time.Now().UnixNano())
	mustExecPG(t, fixture.admin, fmt.Sprintf("CREATE ROLE %s", role))
	t.Cleanup(func() {
		mustExecPG(t, fixture.admin, fmt.Sprintf("DROP OWNED BY %s", role))
		mustExecPG(t, fixture.admin, fmt.Sprintf("DROP ROLE %s", role))
	})
	mustExecPG(t, fixture.db, fmt.Sprintf("GRANT USAGE ON SCHEMA %s TO %s", fixture.schema, role))
	mustExecPG(t, fixture.db, fmt.Sprintf(dashboardGrants, role))
	mustExecPG(t, fixture.db, fmt.Sprintf("GRANT SELECT ON gated_fetch_policies TO %s", role))
	result := func(statement string) error {
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

	return result
}

func TestPGDashboardRoleQueuesSpecialLimitWithExistingGrants(t *testing.T) {
	fixture := openPGFixture(t)
	asRole := grantDashboardRole(t, fixture)
	statements := []string{
		`INSERT INTO settings_change_requests (kind, key, value, expected) VALUES ('special_limit', 'chat:-1001', '30', 'null')`,
		`INSERT INTO settings_change_requests (kind, key, value, expected) VALUES ('special_limit', 'user:7', NULL, '50')`,
	}

	for _, statement := range statements {
		if err := asRole(statement); err != nil {
			t.Fatalf("the dashboard role must queue a limit request: %v", err)
		}
	}
}

func TestPGDashboardRoleCanNotWritePolicies(t *testing.T) {
	fixture := openPGFixture(t)
	asRole := grantDashboardRole(t, fixture)
	refused := []string{
		`INSERT INTO gated_fetch_policies (scope, scope_id, enabled, monthly_limit) VALUES ('user', 7, true, 50)`,
		`UPDATE gated_fetch_policies SET monthly_limit = 1`,
		`DELETE FROM gated_fetch_policies`,
	}

	for _, statement := range refused {
		if err := asRole(statement); (err == nil) || (strings.Contains(err.Error(), "permission denied") == false) {
			t.Fatalf("must be refused: %s: %v", statement, err)
		}
	}
}

func TestPGSpecialLimitChecksRefuseBadKeyValueAndExpected(t *testing.T) {
	fixture := openPGFixture(t)
	cases := []struct {
		key      string
		value    any
		expected string
	}{
		{"user:-1", "30", "null"},
		{"chat:5", "30", "null"},
		{"user:05", "30", "null"},
		{"gated.user:1", "30", "null"},
		{"user:7", "1001", "null"},
		{"user:7", "-1", "null"},
		{"user:7", "10.5", "null"},
		{"user:7", `"10"`, "null"},
		{"user:7", `{"a":1}`, "null"},
		{"user:7", "null", "null"},
		{"user:7", "30", `"x"`},
		{"user:7", "30", "-2"},
		{"user:7", "30", "1.5"},
	}

	for _, test := range cases {
		_, err := fixture.db.Exec(`INSERT INTO settings_change_requests (kind, key, value, expected) VALUES ('special_limit', $1, $2, $3)`, test.key, test.value, test.expected)

		if pqCode(err) != checkViolation {
			t.Fatalf("%s=%v expected %s: want a check violation, got %v", test.key, test.value, test.expected, err)
		}
	}

	fixture.queueLimitRequest(t, "user:7", "1000", "1500")
	fixture.queueLimitRequest(t, "chat:-1001234567890", "0", "-1")
}

func TestPGOneOpenSpecialLimitPerScope(t *testing.T) {
	fixture := openPGFixture(t)
	fixture.queueLimitRequest(t, "user:7", "30", "null")
	_, err := fixture.db.Exec(`INSERT INTO settings_change_requests (kind, key, value, expected) VALUES ('special_limit', 'user:7', '40', 'null')`)

	if pqCode(err) != "23505" {
		t.Fatalf("a second open request must be a unique violation, got %v", err)
	}

	fixture.queueLimitRequest(t, "user:8", "30", "null")
	fixture.queueRequest(t, "user:7", `30`, `null`)
}

func TestPGSpecialLimitApplyWritesPolicyAndAudit(t *testing.T) {
	fixture := openPGFixture(t)
	harness := loadedPGHarness(t, fixture)
	id := fixture.queueLimitRequest(t, "chat:-1001", "30", "null")
	harness.manager.processRequests(context.Background())
	status, reason := fixture.requestStatus(t, id)
	enabled, limit, isFound := fixture.policy(t, "chat", -1001)
	audits := fixture.policyAudits(t)

	if (status != settings.StatusApplied) || (isFound == false) || (enabled == false) || (limit.Int64 != 30) {
		t.Fatalf("got %s (%s), policy %v %v %v", status, reason, enabled, limit, isFound)
	}

	isAudited := (len(audits) == 1) && (audits[0].key == "special_limit:chat:-1001") && (audits[0].oldValue.Valid == false) && (audits[0].newValue.String == "30")

	if (isAudited == false) || (audits[0].actor != settings.ActorDashboard) || (audits[0].requestID.Int64 != id) {
		t.Fatalf("unexpected audit %+v", audits)
	}

	var decidedAt, auditAt time.Time
	_ = fixture.db.QueryRow(`SELECT decided_at FROM settings_change_requests WHERE id = $1`, id).Scan(&decidedAt)
	_ = fixture.db.QueryRow(`SELECT created_at FROM settings_audit WHERE request_id = $1`, id).Scan(&auditAt)

	if decidedAt.Equal(auditAt) == false {
		t.Fatalf("the write and the outcome must commit together: %v vs %v", decidedAt, auditAt)
	}

	entries := harness.manager.digest.entries

	if (len(entries) != 1) || (entries[0].Text != "Special downloads a month, group -1001: default (20) → 30") {
		t.Fatalf("digest %+v", entries)
	}
}

func TestPGSpecialLimitKeepsGroupDisabled(t *testing.T) {
	fixture := openPGFixture(t)
	harness := loadedPGHarness(t, fixture)
	mustExecPG(t, fixture.db, `INSERT INTO gated_fetch_policies (scope, scope_id, enabled) VALUES ('chat', -1001, false)`)
	id := fixture.queueLimitRequest(t, "chat:-1001", "30", "null")
	harness.manager.processRequests(context.Background())
	status, _ := fixture.requestStatus(t, id)
	enabled, limit, _ := fixture.policy(t, "chat", -1001)

	if (status != settings.StatusApplied) || (enabled == true) || (limit.Int64 != 30) {
		t.Fatalf("got %s, enabled %v, limit %v", status, enabled, limit)
	}
}

func TestPGRejectedSpecialLimitLeavesNoRowAndNoAudit(t *testing.T) {
	fixture := openPGFixture(t)
	harness := loadedPGHarness(t, fixture)
	id := fixture.queueLimitRequest(t, "user:7", "30", "50")
	harness.manager.processRequests(context.Background())
	status, reason := fixture.requestStatus(t, id)
	_, _, isFound := fixture.policy(t, "user", 7)

	if (status != settings.StatusRejected) || (reason != requestChangedError) || (isFound == true) || (len(fixture.policyAudits(t)) != 0) {
		t.Fatalf("got %s (%s), row %v, audits %+v", status, reason, isFound, fixture.policyAudits(t))
	}
}

func TestPGExpiredSpecialLimitTouchesNoPolicy(t *testing.T) {
	fixture := openPGFixture(t)
	harness := loadedPGHarness(t, fixture)
	id := fixture.queueLimitRequest(t, "user:7", "30", "null")
	mustExecPG(t, fixture.db, `UPDATE settings_change_requests SET expires_at = now() - INTERVAL '1 second' WHERE id = $1`, id)
	harness.manager.processRequests(context.Background())
	status, _ := fixture.requestStatus(t, id)
	_, _, isFound := fixture.policy(t, "user", 7)

	if (status != settings.StatusExpired) || (isFound == true) || (len(fixture.policyAudits(t)) != 0) {
		t.Fatalf("got %s, row %v", status, isFound)
	}
}

func TestPGResetOfEnabledScopeRemovesTheRow(t *testing.T) {
	fixture := openPGFixture(t)
	harness := loadedPGHarness(t, fixture)
	mustExecPG(t, fixture.db, `INSERT INTO gated_fetch_policies (scope, scope_id, enabled, monthly_limit) VALUES ('user', 7, true, 50)`)
	id := fixture.queueLimitRequest(t, "user:7", nil, "50")
	harness.manager.processRequests(context.Background())
	status, reason := fixture.requestStatus(t, id)
	_, _, isFound := fixture.policy(t, "user", 7)
	audits := fixture.policyAudits(t)
	isResetAudited := (len(audits) == 2) && (audits[1].oldValue.String == "50") && (audits[1].newValue.Valid == false)

	if (status != settings.StatusApplied) || (isFound == true) || (isResetAudited == false) {
		t.Fatalf("got %s (%s), row %v, audits %+v", status, reason, isFound, audits)
	}
}

func TestPGGatedWriteBetweenQueueAndClaimRejectsTheRequest(t *testing.T) {
	fixture := openPGFixture(t)
	harness := loadedPGHarness(t, fixture)
	useServicesDatabase(t, fixture)
	id := fixture.queueLimitRequest(t, "chat:-1001", "30", "null")
	limit := sql.NullInt64{Int64: 100, Valid: true}

	if err := services.SetGatedFetchMonthlyLimit(services.GatedFetchScopeChat, -1001, limit); err != nil {
		t.Fatalf("/gated: %v", err)
	}

	harness.manager.processRequests(context.Background())
	status, reason := fixture.requestStatus(t, id)
	_, stored, _ := fixture.policy(t, "chat", -1001)

	if (status != settings.StatusRejected) || (reason != requestChangedError) || (stored.Int64 != 100) {
		t.Fatalf("got %s (%s), limit %v", status, reason, stored)
	}
}

func TestPGGatedCommandAuditedAsTelegram(t *testing.T) {
	fixture := openPGFixture(t)
	useServicesDatabase(t, fixture)
	limit := sql.NullInt64{Int64: -1, Valid: true}

	if err := services.SetGatedFetchMonthlyLimit(services.GatedFetchScopeUser, 7, limit); err != nil {
		t.Fatalf("limit: %v", err)
	}

	if err := services.SetGatedFetchEnabled(services.GatedFetchScopeChat, -1001, false); err != nil {
		t.Fatalf("enabled: %v", err)
	}

	audits := fixture.policyAudits(t)
	isLimit := (len(audits) == 2) && (audits[0].key == "special_limit:user:7") && (audits[0].newValue.String == "-1")
	isEnabled := (len(audits) == 2) && (audits[1].key == "special_enabled:chat:-1001") && (audits[1].oldValue.String == "true") && (audits[1].newValue.String == "false")

	if (isLimit == false) || (isEnabled == false) {
		t.Fatalf("unexpected audit %+v", audits)
	}

	for _, audit := range audits {
		if (audit.actor != settings.ActorTelegram) || (audit.note.String != "/gated") || (audit.requestID.Valid == true) {
			t.Fatalf("a /gated write must be audited as telegram: %+v", audit)
		}
	}
}

func TestPGGroupMoveAuditedAsBot(t *testing.T) {
	fixture := openPGFixture(t)
	useServicesDatabase(t, fixture)
	mustExecPG(t, fixture.db, `INSERT INTO gated_fetch_policies (scope, scope_id, enabled, monthly_limit) VALUES ('chat', -5, true, 40)`)

	if err := services.MoveGroupCredits(-5, -1005); err != nil {
		t.Fatalf("move: %v", err)
	}

	audits := fixture.policyAudits(t)
	last := audits[len(audits)-1]

	if (last.key != "special_limit:chat:-1005") || (last.actor != settings.ActorBot) || (last.note.String != "group upgraded to supergroup") || (last.newValue.String != "40") {
		t.Fatalf("unexpected audit %+v", audits)
	}
}

func TestPGManualEditAuditedAsManual(t *testing.T) {
	fixture := openPGFixture(t)
	mustExecPG(t, fixture.db, `INSERT INTO gated_fetch_policies (scope, scope_id, enabled, monthly_limit) VALUES ('user', 7, true, 50)`)
	mustExecPG(t, fixture.db, `DELETE FROM gated_fetch_policies WHERE scope = 'user' AND scope_id = 7`)
	mustExecPG(t, fixture.db, `INSERT INTO gated_fetch_policies (scope, scope_id, enabled) VALUES ('user', 8, true)`)
	audits := fixture.policyAudits(t)

	if (len(audits) != 2) || (audits[0].actor != settings.ActorManual) || (audits[1].oldValue.String != "50") || (audits[1].newValue.Valid == true) {
		t.Fatalf("an all-default row logs nothing, edits are manual: %+v", audits)
	}
}

func TestPGTruncateOfPoliciesIsRefused(t *testing.T) {
	fixture := openPGFixture(t)

	if _, err := fixture.db.Exec(`TRUNCATE gated_fetch_policies`); err == nil {
		t.Fatalf("TRUNCATE must be refused")
	}
}
