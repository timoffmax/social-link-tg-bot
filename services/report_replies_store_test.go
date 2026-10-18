package services

// Integration tests for the report reply store. Like the gated-fetch store
// tests they are skipped unless GATED_FETCH_TEST_DSN is set.

import (
	"database/sql"
	"os"
	"sync"
	"testing"
	"time"
)

// reportRepliesTestMigrations create the reports and report_replies tables.
var reportRepliesTestMigrations = []string{
	"../migrations/005_create_reports.up.sql",
	"../migrations/009_report_replies.up.sql",
}

// setupReportRepliesTestDB recreates the report tables and points the store at
// the test database.
func setupReportRepliesTestDB(t *testing.T) *sql.DB {
	dsn := os.Getenv(gatedFetchTestDSNKey)

	if dsn == "" {
		t.Skipf("%s not set", gatedFetchTestDSNKey)
	}

	db, err := sql.Open("postgres", dsn)

	if err != nil {
		t.Fatalf("open: %v", err)
	}

	mustExec(t, db, "DROP TABLE IF EXISTS report_replies, reports CASCADE")

	for _, path := range reportRepliesTestMigrations {
		migration, readErr := os.ReadFile(path)

		if readErr != nil {
			t.Fatalf("read migration: %v", readErr)
		}

		mustExec(t, db, string(migration))
	}

	previous := dbService
	dbService = &DatabaseService{db: db}

	t.Cleanup(func() {
		dbService = previous
		db.Close()
	})

	return db
}

// createTestReport files a report by userID, failing the test on errors.
func createTestReport(t *testing.T, userID int64) int {
	id, err := CreateReport(userID, "user", "User", userID, "chat", "it broke")

	if err != nil {
		t.Fatalf("create report: %v", err)
	}

	return id
}

// createTestReply records a reply, failing the test on errors.
func createTestReply(t *testing.T, reportID int, text, source, status string) ReportReply {
	reply, err := CreateReportReply(reportID, text, source, status)

	if err != nil {
		t.Fatalf("create reply: %v", err)
	}

	return reply
}

// claimTestReply claims the next queued reply, failing the test on errors.
func claimTestReply(t *testing.T) (ClaimedReportReply, bool) {
	claimed, isClaimed, err := ClaimNextQueuedReportReply()

	if err != nil {
		t.Fatalf("claim: %v", err)
	}

	return claimed, isClaimed
}

// listTestReplies lists a report's replies, failing the test on errors.
func listTestReplies(t *testing.T, reportID int) []ReportReply {
	replies, err := ListReportReplies(reportID)

	if err != nil {
		t.Fatalf("list: %v", err)
	}

	return replies
}

func TestClaimNextQueuedReportReplyClaimsTheOldestOnceWithTheReporter(t *testing.T) {
	setupReportRepliesTestDB(t)
	reportID := createTestReport(t, 501)
	first := createTestReply(t, reportID, "first", ReportReplySourceDashboard, ReportReplyStatusQueued)
	createTestReply(t, reportID, "second", ReportReplySourceDashboard, ReportReplyStatusQueued)

	claimed, isClaimed := claimTestReply(t)

	if (isClaimed == false) || (claimed.Reply.ID != first.ID) || (claimed.UserID != 501) || (claimed.Reply.Status != ReportReplyStatusSending) || (claimed.Reply.ClaimedAt.Valid == false) || (first.ClaimedAt.Valid == true) {
		t.Fatalf("expected the first reply claimed for user 501, got %+v %t", claimed, isClaimed)
	}

	claimTestReply(t)
	_, isClaimedAgain := claimTestReply(t)

	if isClaimedAgain == true {
		t.Fatal("expected nothing left to claim")
	}
}

func TestQueuedReportRepliesAreClaimedOnceUnderConcurrency(t *testing.T) {
	setupReportRepliesTestDB(t)
	reportID := createTestReport(t, 502)

	for i := 0; i < 5; i++ {
		createTestReply(t, reportID, "text", ReportReplySourceDashboard, ReportReplyStatusQueued)
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	seen := map[int]int{}

	for i := 0; i < 10; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			claimed, isClaimed, err := ClaimNextQueuedReportReply()

			if (err == nil) && (isClaimed == true) {
				mu.Lock()
				seen[claimed.Reply.ID]++
				mu.Unlock()
			}
		}()
	}

	wg.Wait()

	for id, count := range seen {
		if count != 1 {
			t.Fatalf("reply %d claimed %d times", id, count)
		}
	}

	if len(seen) != 5 {
		t.Fatalf("expected 5 claimed replies, got %d", len(seen))
	}
}

func TestMarkReportReplySentStoresTheMessageAndFailedDoesNotOverwriteIt(t *testing.T) {
	setupReportRepliesTestDB(t)
	reportID := createTestReport(t, 503)
	reply := createTestReply(t, reportID, "hi", ReportReplySourceTelegram, ReportReplyStatusSending)

	sentErr := MarkReportReplySent(reply.ID, 777)
	failedErr := MarkReportReplyFailed(reply.ID, "late failure")
	replies := listTestReplies(t, reportID)
	stored := replies[0]

	if (sentErr != nil) || (failedErr != nil) || (stored.Status != ReportReplyStatusSent) || (stored.TelegramMessageID.Int64 != 777) || (stored.SentAt.Valid == false) || (stored.Error.Valid == true) {
		t.Fatalf("expected a sent reply, got %+v %v %v", stored, sentErr, failedErr)
	}
}

func TestMarkReportReplyFailedStoresTheReason(t *testing.T) {
	setupReportRepliesTestDB(t)
	reportID := createTestReport(t, 504)
	reply := createTestReply(t, reportID, "hi", ReportReplySourceDashboard, ReportReplyStatusSending)

	err := MarkReportReplyFailed(reply.ID, "Forbidden: bot was blocked by the user")
	replies := listTestReplies(t, reportID)
	stored := replies[0]

	if (err != nil) || (stored.Status != ReportReplyStatusFailed) || (stored.Error.String != "Forbidden: bot was blocked by the user") || (stored.SentAt.Valid == true) {
		t.Fatalf("expected a failed reply, got %+v %v", stored, err)
	}
}

func TestMarkInterruptedReportRepliesMarksOnlyRepliesClaimedLongAgoUnknown(t *testing.T) {
	db := setupReportRepliesTestDB(t)
	reportID := createTestReport(t, 505)
	old := createTestReply(t, reportID, "old", ReportReplySourceDashboard, ReportReplyStatusSending)
	fresh := createTestReply(t, reportID, "fresh", ReportReplySourceDashboard, ReportReplyStatusSending)
	queued := createTestReply(t, reportID, "queued", ReportReplySourceDashboard, ReportReplyStatusQueued)
	mustExec(t, db, "UPDATE report_replies SET created_at = now() - interval '1 hour'")
	mustExec(t, db, "UPDATE report_replies SET claimed_at = now() - interval '6 minutes' WHERE text = 'old'")

	interrupted, err := MarkInterruptedReportReplies()
	replies := listTestReplies(t, reportID)
	statuses := map[int]string{}

	for _, reply := range replies {
		statuses[reply.ID] = reply.Status
	}

	if (err != nil) || (len(interrupted) != 1) || (interrupted[0].ID != old.ID) || (interrupted[0].Status != ReportReplyStatusUnknown) || (interrupted[0].Error.String != ReportReplyInterruptedError) {
		t.Fatalf("expected only the reply claimed long ago marked unknown, got %+v %v", interrupted, err)
	}

	if (statuses[fresh.ID] != ReportReplyStatusSending) || (statuses[queued.ID] != ReportReplyStatusQueued) {
		t.Fatalf("expected the freshly claimed and queued replies untouched, got %+v", statuses)
	}
}

func TestMarkReportReplyUnknownStoresTheReason(t *testing.T) {
	setupReportRepliesTestDB(t)
	reportID := createTestReport(t, 511)
	reply := createTestReply(t, reportID, "hi", ReportReplySourceDashboard, ReportReplyStatusSending)

	err := MarkReportReplyUnknown(reply.ID, "delivery unknown (no answer from Telegram): timeout")
	replies := listTestReplies(t, reportID)
	stored := replies[0]

	if (err != nil) || (stored.Status != ReportReplyStatusUnknown) || (stored.Error.String != "delivery unknown (no answer from Telegram): timeout") {
		t.Fatalf("expected an unknown reply, got %+v %v", stored, err)
	}
}

// reportResolveState reads a report's resolved flag and time.
func reportResolveState(t *testing.T, db *sql.DB, reportID int) (bool, sql.NullTime) {
	var isResolved bool
	var resolvedAt sql.NullTime
	err := db.QueryRow("SELECT resolved, resolved_at FROM reports WHERE id = $1", reportID).Scan(&isResolved, &resolvedAt)

	if err != nil {
		t.Fatalf("read report: %v", err)
	}

	return isResolved, resolvedAt
}

func TestMarkReportReplySentResolvesTheReportOnlyWhenTheReplyAskedForIt(t *testing.T) {
	db := setupReportRepliesTestDB(t)
	resolvingID := createTestReport(t, 512)
	plainID := createTestReport(t, 513)
	resolving := createTestReply(t, resolvingID, "fixed", ReportReplySourceDashboard, ReportReplyStatusSending)
	plain := createTestReply(t, plainID, "question", ReportReplySourceDashboard, ReportReplyStatusSending)
	mustExec(t, db, "UPDATE report_replies SET resolve_on_send = true WHERE text = 'fixed'")

	resolvingErr := MarkReportReplySent(resolving.ID, 1)
	plainErr := MarkReportReplySent(plain.ID, 2)
	isResolved, resolvedAt := reportResolveState(t, db, resolvingID)
	isPlainResolved, _ := reportResolveState(t, db, plainID)

	if (resolvingErr != nil) || (plainErr != nil) || (isResolved == false) || (resolvedAt.Valid == false) || (isPlainResolved == true) {
		t.Fatalf("expected only the resolving reply's report resolved, got %t %+v %t %v %v", isResolved, resolvedAt, isPlainResolved, resolvingErr, plainErr)
	}
}

func TestMarkReportReplySentKeepsAnEarlierResolveTime(t *testing.T) {
	db := setupReportRepliesTestDB(t)
	reportID := createTestReport(t, 514)
	reply := createTestReply(t, reportID, "fixed", ReportReplySourceDashboard, ReportReplyStatusSending)
	mustExec(t, db, "UPDATE report_replies SET resolve_on_send = true")
	mustExec(t, db, "UPDATE reports SET resolved = true, resolved_at = '2026-01-02T03:04:05Z'")

	err := MarkReportReplySent(reply.ID, 1)
	_, resolvedAt := reportResolveState(t, db, reportID)
	original := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	if (err != nil) || (resolvedAt.Time.Equal(original) == false) {
		t.Fatalf("expected the original resolve time, got %+v %v", resolvedAt, err)
	}
}

func TestUndeliveredRepliesLeaveTheReportOpen(t *testing.T) {
	db := setupReportRepliesTestDB(t)
	reportID := createTestReport(t, 515)
	failed := createTestReply(t, reportID, "a", ReportReplySourceDashboard, ReportReplyStatusSending)
	unknown := createTestReply(t, reportID, "b", ReportReplySourceDashboard, ReportReplyStatusSending)
	mustExec(t, db, "UPDATE report_replies SET resolve_on_send = true")

	failedErr := MarkReportReplyFailed(failed.ID, "Forbidden: bot was blocked by the user")
	unknownErr := MarkReportReplyUnknown(unknown.ID, "delivery unknown")
	isResolved, _ := reportResolveState(t, db, reportID)

	if (failedErr != nil) || (unknownErr != nil) || (isResolved == true) {
		t.Fatalf("expected the report open, got %t %v %v", isResolved, failedErr, unknownErr)
	}
}

func TestRequeueReportReplyCountsTheAttemptAndWaitsUntilItIsDue(t *testing.T) {
	setupReportRepliesTestDB(t)
	reportID := createTestReport(t, 516)
	createTestReply(t, reportID, "hi", ReportReplySourceDashboard, ReportReplyStatusQueued)
	claimed, _ := claimTestReply(t)
	notBefore := time.Now().Add(time.Hour)

	err := RequeueReportReply(claimed.Reply.ID, "Too Many Requests: retry after 3600", notBefore)
	replies := listTestReplies(t, reportID)
	stored := replies[0]
	_, isClaimedEarly := claimTestReply(t)

	if (err != nil) || (stored.Status != ReportReplyStatusQueued) || (stored.Attempts != 1) || (stored.NextAttemptAt.Valid == false) || (stored.ClaimedAt.Valid == true) || (stored.Error.String != "Too Many Requests: retry after 3600") {
		t.Fatalf("expected a requeued reply, got %+v %v", stored, err)
	}

	if isClaimedEarly == true {
		t.Fatal("expected the reply not claimed before next_attempt_at")
	}

	pastErr := RequeueReportReply(claimed.Reply.ID, "late", time.Now())

	if pastErr != nil {
		t.Fatalf("requeue of a queued reply: %v", pastErr)
	}

	stillQueued := listTestReplies(t, reportID)

	if (stillQueued[0].Attempts != 1) || (stillQueued[0].Error.String != "Too Many Requests: retry after 3600") {
		t.Fatalf("expected a reply no longer sending left alone, got %+v", stillQueued[0])
	}
}

func TestRequeuedReplyIsClaimedOnceDue(t *testing.T) {
	db := setupReportRepliesTestDB(t)
	reportID := createTestReport(t, 517)
	reply := createTestReply(t, reportID, "hi", ReportReplySourceDashboard, ReportReplyStatusQueued)
	mustExec(t, db, "UPDATE report_replies SET attempts = 2, next_attempt_at = now() - interval '1 second'")

	claimed, isClaimed := claimTestReply(t)

	if (isClaimed == false) || (claimed.Reply.ID != reply.ID) || (claimed.Reply.Attempts != 2) {
		t.Fatalf("expected the due reply claimed with its attempts, got %+v %t", claimed, isClaimed)
	}
}

func TestCreateReportReplyAsSendingStampsTheClaim(t *testing.T) {
	setupReportRepliesTestDB(t)
	reportID := createTestReport(t, 518)

	sending := createTestReply(t, reportID, "now", ReportReplySourceTelegram, ReportReplyStatusSending)
	queued := createTestReply(t, reportID, "later", ReportReplySourceDashboard, ReportReplyStatusQueued)

	if (sending.ClaimedAt.Valid == false) || (queued.ClaimedAt.Valid == true) || (sending.ResolveOnSend == true) || (sending.Attempts != 0) {
		t.Fatalf("expected only the sending reply claimed, got %+v %+v", sending, queued)
	}
}

func TestListReportRepliesReturnsOnlyTheReportsRepliesOldestFirst(t *testing.T) {
	setupReportRepliesTestDB(t)
	reportID := createTestReport(t, 506)
	otherID := createTestReport(t, 507)
	first := createTestReply(t, reportID, "first", ReportReplySourceDashboard, ReportReplyStatusQueued)
	createTestReply(t, otherID, "other", ReportReplySourceDashboard, ReportReplyStatusQueued)
	second := createTestReply(t, reportID, "second", ReportReplySourceTelegram, ReportReplyStatusSending)

	replies := listTestReplies(t, reportID)

	if (len(replies) != 2) || (replies[0].ID != first.ID) || (replies[1].ID != second.ID) {
		t.Fatalf("expected the two replies oldest first, got %+v", replies)
	}
}

func TestReportReplyRejectsAnUnknownSourceOrStatus(t *testing.T) {
	setupReportRepliesTestDB(t)
	reportID := createTestReport(t, 508)

	_, sourceErr := CreateReportReply(reportID, "hi", "email", ReportReplyStatusQueued)
	_, statusErr := CreateReportReply(reportID, "hi", ReportReplySourceDashboard, "lost")
	_, unknownErr := CreateReportReply(reportID, "hi", ReportReplySourceDashboard, ReportReplyStatusUnknown)

	if (sourceErr == nil) || (statusErr == nil) || (unknownErr != nil) {
		t.Fatalf("expected the unknown source and status rejected and 'unknown' accepted, got %v %v %v", sourceErr, statusErr, unknownErr)
	}
}

func TestDeletingAReportDeletesItsReplies(t *testing.T) {
	db := setupReportRepliesTestDB(t)
	reportID := createTestReport(t, 509)
	createTestReply(t, reportID, "hi", ReportReplySourceDashboard, ReportReplyStatusQueued)
	mustExec(t, db, "DELETE FROM reports")

	replies := listTestReplies(t, reportID)

	if len(replies) != 0 {
		t.Fatalf("expected the replies deleted, got %+v", replies)
	}
}

func TestResolveReportRecordsTheResolveTime(t *testing.T) {
	db := setupReportRepliesTestDB(t)
	reportID := createTestReport(t, 510)

	err := ResolveReport(reportID)
	var isResolved bool
	var resolvedAt sql.NullTime
	scanErr := db.QueryRow("SELECT resolved, resolved_at FROM reports WHERE id = $1", reportID).Scan(&isResolved, &resolvedAt)

	if (err != nil) || (scanErr != nil) || (isResolved == false) || (resolvedAt.Valid == false) {
		t.Fatalf("expected a resolved report with a time, got %t %+v %v %v", isResolved, resolvedAt, err, scanErr)
	}

	mustExec(t, db, "UPDATE reports SET resolved_at = '2026-01-02T03:04:05Z'")
	againErr := ResolveReport(reportID)
	_, kept := reportResolveState(t, db, reportID)
	original := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	if (againErr != nil) || (kept.Time.Equal(original) == false) {
		t.Fatalf("expected the original resolve time kept, got %+v %v", kept, againErr)
	}
}
