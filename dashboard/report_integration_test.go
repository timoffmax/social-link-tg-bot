package dashboard

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

const reportFixtures = `INSERT INTO reports (id, user_id, username, first_name, chat_id, chat_title, message, resolved, resolved_at, created_at) VALUES
(1, 42, 'name', 'N', -1001, 'Group', 'open one', false, NULL, '2026-10-15T11:00:00Z'),
(2, 43, NULL, NULL, 43, NULL, 'resolved one', true, '2026-10-14T09:00:00Z', '2026-10-13T12:00:00Z'),
(3, 44, 'x', 'X', 44, NULL, 'resolved before resolved_at', true, NULL, '2026-10-10T12:00:00Z')`

const replyFixtures = `INSERT INTO report_replies (id, report_id, text, source, status, resolve_on_send, attempts, next_attempt_at, error, telegram_message_id, created_at, sent_at) VALUES
(10, 2, 'second', 'dashboard', 'failed', false, 0, NULL, 'Forbidden: bot was blocked by the user', NULL, '2026-10-14T08:00:00Z', NULL),
(11, 2, 'first', 'telegram', 'sent', false, 0, NULL, NULL, 555, '2026-10-14T07:00:00Z', '2026-10-14T07:00:01Z'),
(12, 1, 'queued', 'dashboard', 'queued', true, 1, '2026-10-15T11:31:00Z', 'Too Many Requests: retry after 30', NULL, '2026-10-15T11:30:00Z', NULL),
(13, 1, 'unknown', 'dashboard', 'unknown', false, 0, NULL, 'interrupted, delivery unknown, not resent', NULL, '2026-10-15T11:40:00Z', NULL)`

// dashboardRoleStore connects as a role with the production dashboard's
// grants on the report tables and default_transaction_read_only on;
// canResolve false leaves out the UPDATE grant on reports.
func (d integrationDB) dashboardRoleStore(t *testing.T, canResolve bool) *PGStore {
	t.Helper()
	role := fmt.Sprintf("dashboard_test_role_%d", time.Now().UnixNano())
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
	d.exec(t, fmt.Sprintf("GRANT SELECT ON ALL TABLES IN SCHEMA %s TO %s", schema, role))
	d.exec(t, fmt.Sprintf("GRANT INSERT ON report_replies TO %s", role))
	d.exec(t, fmt.Sprintf("GRANT USAGE ON SEQUENCE report_replies_id_seq TO %s", role))

	if canResolve == true {
		d.exec(t, fmt.Sprintf("GRANT UPDATE (resolved, resolved_at) ON reports TO %s", role))
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
	var current string

	if err := db.QueryRow(`SELECT current_user`).Scan(&current); (err != nil) || (current != role) {
		t.Fatalf("current_user = %q, err = %v", current, err)
	}

	store := NewPGStore(db, kyiv)
	store.now = func() time.Time { return integrationNow }

	return store
}

func (d integrationDB) countReplies(t *testing.T, reportID int64) int {
	t.Helper()
	count := 0

	if err := d.db.QueryRow(`SELECT count(*) FROM report_replies WHERE report_id = $1`, reportID).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}

	return count
}

func TestIntegrationReportsWithRepliesAndStatusFilter(t *testing.T) {
	d := openIntegrationDB(t)
	d.exec(t, reportFixtures)
	d.exec(t, replyFixtures)
	store := d.store(kyiv)
	ctx := context.Background()
	all, err := store.Reports(ctx, 10, 2, ReportStatusAll)

	if err != nil {
		t.Fatalf("reports: %v", err)
	}

	assertEqual(t, "totals", all.Totals, ReportTotals{Open: 1, Resolved: 2})
	assertEqual(t, "latest", all.Latest, []Report{
		{ID: 1, CreatedAt: text("2026-10-15T11:00:00Z"), UserID: 42, Username: "name", FirstName: "N", ChatID: -1001, ChatTitle: "Group", Message: "open one", Replies: []ReportReply{
			{ID: 12, Text: "queued", Source: "dashboard", Status: "queued", ResolveOnSend: true, Attempts: 1, NextAttemptAt: text("2026-10-15T11:31:00Z"), Error: text("Too Many Requests: retry after 30"), CreatedAt: text("2026-10-15T11:30:00Z")},
			{ID: 13, Text: "unknown", Source: "dashboard", Status: "unknown", Error: text("interrupted, delivery unknown, not resent"), CreatedAt: text("2026-10-15T11:40:00Z")},
		}},
		{ID: 2, CreatedAt: text("2026-10-13T12:00:00Z"), UserID: 43, ChatID: 43, Message: "resolved one", Resolved: true, ResolvedAt: text("2026-10-14T09:00:00Z"), Replies: []ReportReply{
			{ID: 11, Text: "first", Source: "telegram", Status: "sent", CreatedAt: text("2026-10-14T07:00:00Z"), SentAt: text("2026-10-14T07:00:01Z")},
			{ID: 10, Text: "second", Source: "dashboard", Status: "failed", Error: text("Forbidden: bot was blocked by the user"), CreatedAt: text("2026-10-14T08:00:00Z")},
		}},
		{ID: 3, CreatedAt: text("2026-10-10T12:00:00Z"), UserID: 44, Username: "x", FirstName: "X", ChatID: 44, Message: "resolved before resolved_at", Resolved: true, Replies: []ReportReply{}},
	})

	open, _ := store.Reports(ctx, 10, 2, ReportStatusOpen)
	resolved, _ := store.Reports(ctx, 10, 2, ReportStatusResolved)
	assertEqual(t, "open ids", reportIDs(open.Latest), []int64{1})
	assertEqual(t, "resolved ids", reportIDs(resolved.Latest), []int64{2, 3})
	assertEqual(t, "open status", open.Status, "open")
	assertEqual(t, "filtered totals", resolved.Totals, ReportTotals{Open: 1, Resolved: 2})
}

func reportIDs(reports []Report) []int64 {
	result := []int64{}

	for _, report := range reports {
		result = append(result, report.ID)
	}

	return result
}

func TestIntegrationQueueReplyWithResolveLeavesTheReportOpenUntilDelivered(t *testing.T) {
	d := openIntegrationDB(t)
	d.exec(t, reportFixtures)
	store := d.dashboardRoleStore(t, true)
	ctx := context.Background()
	reply, report, err := store.QueueReportReply(ctx, 1, "<b>Fixed</b>", true)

	if err != nil {
		t.Fatalf("queue: %v", err)
	}

	if (reply.ID == 0) || (reply.Status != "queued") || (reply.Source != "dashboard") || (reply.Text != "<b>Fixed</b>") || (reply.ResolveOnSend == false) || (reply.Attempts != 0) || (reply.NextAttemptAt != nil) || (reply.CreatedAt == nil) || (reply.SentAt != nil) || (reply.Error != nil) {
		t.Errorf("reply = %+v", reply)
	}

	if (report.Resolved == true) || (report.ResolvedAt != nil) {
		t.Errorf("report = %+v, want it open until the bot has delivered the reply", report)
	}

	assertEqual(t, "replies", report.Replies, []ReportReply{reply})

	var status string
	var resolveOnSend bool
	var messageID sql.NullInt64

	if err := d.db.QueryRow(`SELECT status, resolve_on_send, telegram_message_id FROM report_replies WHERE id = $1`, reply.ID).Scan(&status, &resolveOnSend, &messageID); (err != nil) || (status != "queued") || (resolveOnSend == false) || (messageID.Valid == true) {
		t.Errorf("stored status = %q, resolve_on_send = %t, message id = %v, err = %v", status, resolveOnSend, messageID, err)
	}

	open, report, err := store.QueueReportReply(ctx, 2, "more", false)

	if (err != nil) || (open.Status != "queued") || (open.ResolveOnSend == true) || (report.Resolved == false) || (*report.ResolvedAt != "2026-10-14T09:00:00Z") {
		t.Errorf("reply without resolving: %+v, %+v, %v", open, report, err)
	}
}

func TestIntegrationQueueReplyNeedsNoUpdateGrantOnReports(t *testing.T) {
	d := openIntegrationDB(t)
	d.exec(t, reportFixtures)
	store := d.dashboardRoleStore(t, false)

	for _, resolve := range []bool{true, false} {
		if _, _, err := store.QueueReportReply(context.Background(), 1, "text", resolve); err != nil {
			t.Errorf("queue with resolve %t: %v", resolve, err)
		}
	}

	if count := d.countReplies(t, 1); count != 2 {
		t.Errorf("replies = %d, want 2", count)
	}

	if _, err := store.SetReportResolved(context.Background(), 1, true); (err == nil) || (strings.Contains(err.Error(), "permission denied") == false) {
		t.Errorf("resolve without the grant: err = %v", err)
	}
}

func TestIntegrationReportWritesOfAMissingReport(t *testing.T) {
	d := openIntegrationDB(t)
	d.exec(t, reportFixtures)
	store := d.dashboardRoleStore(t, true)
	ctx := context.Background()
	_, getErr := store.Report(ctx, 99)
	_, _, queueErr := store.QueueReportReply(ctx, 99, "text", true)
	_, resolveErr := store.SetReportResolved(ctx, 99, true)

	for name, err := range map[string]error{"get": getErr, "queue": queueErr, "resolve": resolveErr} {
		if errors.Is(err, ErrReportNotFound) == false {
			t.Errorf("%s of a missing report: err = %v", name, err)
		}
	}

	var count int

	if err := d.db.QueryRow(`SELECT count(*) FROM report_replies`).Scan(&count); (err != nil) || (count != 0) {
		t.Errorf("replies = %d, err = %v", count, err)
	}
}

func TestIntegrationResolveAndReopen(t *testing.T) {
	d := openIntegrationDB(t)
	d.exec(t, reportFixtures)
	store := d.dashboardRoleStore(t, true)
	ctx := context.Background()
	resolved, err := store.SetReportResolved(ctx, 1, true)

	if (err != nil) || (resolved.Resolved == false) || (resolved.ResolvedAt == nil) || (resolved.Message != "open one") {
		t.Fatalf("resolve: %+v, %v", resolved, err)
	}

	reopened, err := store.SetReportResolved(ctx, 1, false)

	if (err != nil) || (reopened.Resolved == true) || (reopened.ResolvedAt != nil) {
		t.Errorf("reopen: %+v, %v", reopened, err)
	}

	loaded, err := store.Report(ctx, 1)
	assertEqual(t, "loaded", loaded, reopened)

	if err != nil {
		t.Errorf("get: %v", err)
	}

	again, err := store.SetReportResolved(ctx, 2, true)

	if (err != nil) || (again.ResolvedAt == nil) || (*again.ResolvedAt != "2026-10-14T09:00:00Z") {
		t.Errorf("resolving a resolved report must keep its time: %+v, %v", again, err)
	}
}

func TestIntegrationReportHTTPEndToEnd(t *testing.T) {
	d := openIntegrationDB(t)
	d.exec(t, reportFixtures)
	server := newTestServer(d.dashboardRoleStore(t, true))
	queued := blogRequest{method: http.MethodPost, target: "/api/reports/1/replies", body: `{"text":" Thanks, <i>fixed</i> ","resolve":true}`}.do(server)

	if queued.Code != http.StatusCreated {
		t.Fatalf("queue: %d %s", queued.Code, queued.Body.String())
	}

	body := decodeBody(t, queued)
	reply := body["reply"].(map[string]interface{})
	report := body["report"].(map[string]interface{})

	if (reply["text"] != "Thanks, <i>fixed</i>") || (reply["status"] != "queued") || (reply["resolve_on_send"] != true) || (report["resolved"] != false) || (report["resolved_at"] != nil) {
		t.Errorf("body = %v", body)
	}

	polled := doRequest(server, http.MethodGet, "/api/reports/1")

	if (polled.Code != http.StatusOK) || (strings.Contains(polled.Body.String(), `"status":"queued"`) == false) {
		t.Errorf("poll: %d %s", polled.Code, polled.Body.String())
	}

	reopened := blogRequest{method: http.MethodPut, target: "/api/reports/1/resolved", body: `{"resolved":false}`}.do(server)
	reopenedReport := decodeBody(t, reopened)["report"].(map[string]interface{})

	if (reopened.Code != http.StatusOK) || (reopenedReport["resolved"] != false) || (reopenedReport["resolved_at"] != nil) {
		t.Errorf("reopen: %d %s", reopened.Code, reopened.Body.String())
	}

	openList := doRequest(server, http.MethodGet, "/api/reports?status=open")

	if (openList.Code != http.StatusOK) || (strings.Contains(openList.Body.String(), `"replies":[{`) == false) || (strings.Contains(openList.Body.String(), `"reply_text_limit":4066`) == false) {
		t.Errorf("open list: %d %s", openList.Code, openList.Body.String())
	}

	missing := blogRequest{method: http.MethodPost, target: "/api/reports/99/replies", body: `{"text":"t","resolve":false}`}.do(server)
	invalid := blogRequest{method: http.MethodPost, target: "/api/reports/1/replies", body: `{"text":"<b>t","resolve":false}`}.do(server)

	if (missing.Code != http.StatusNotFound) || (invalid.Code != http.StatusBadRequest) {
		t.Errorf("missing: %d, invalid: %d", missing.Code, invalid.Code)
	}

	if count := d.countReplies(t, 1); count != 1 {
		t.Errorf("replies = %d, want 1", count)
	}
}
