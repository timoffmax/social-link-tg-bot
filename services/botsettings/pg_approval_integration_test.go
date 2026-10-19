package botsettings

// Integration tests of the Telegram approval's conditional transitions;
// skipped unless SETTINGS_TEST_DSN is set (see pg_integration_test.go).

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/timoffmax/social-link-bot/config"
	"github.com/timoffmax/social-link-bot/model/settings"
)

// awaitRequest inserts a request already awaiting approval, expiring after
// expiresIn on the DB clock.
func (f pgFixture) awaitRequest(t *testing.T, key string, value string, expected string, expiresIn time.Duration) int64 {
	var id int64
	query := `INSERT INTO settings_change_requests (kind, key, value, expected, status, needs_approval, approval_message_id, expires_at)
		VALUES ('setting', $1, $2, $3, 'awaiting_approval', true, 101, now() + make_interval(secs => $4)) RETURNING id`
	err := f.db.QueryRow(query, key, value, expected, expiresIn.Seconds()).Scan(&id)

	if err != nil {
		t.Fatalf("await: %v", err)
	}

	return id
}

func (f pgFixture) auditCount(t *testing.T, requestID int64) int {
	var count int

	if err := f.db.QueryRow(`SELECT count(*) FROM settings_audit WHERE request_id = $1`, requestID).Scan(&count); err != nil {
		t.Fatalf("audit count: %v", err)
	}

	return count
}

func newPGApprovalHarness(t *testing.T) (pgFixture, testHarness, *fakeApprover) {
	fixture := openPGFixture(t)
	clearSettingsEnv(t)
	t.Setenv("APIFY_MONTHLY_BUDGET_USD", "4.8")
	approver := newFakeApprover()
	harness := newHarness(t, NewPGStore(fixture.db))
	harness.manager.approver = approver
	harness.manager.applyRequests = true
	harness.manager.Load(context.Background())

	return fixture, harness, approver
}

func TestPGApprovalWaitsThenAppliesWithTheAuditNote(t *testing.T) {
	fixture, harness, approver := newPGApprovalHarness(t)
	id := fixture.queueRequest(t, settings.KeyApifyMonthlyBudgetUsd, `6`, `4.8`)
	harness.manager.processRequestsIfEnabled(context.Background())
	var status string
	var needsApproval bool
	var messageID int64
	var minutesLeft float64
	query := `SELECT status, needs_approval, approval_message_id, EXTRACT(EPOCH FROM expires_at - now()) / 60 FROM settings_change_requests WHERE id = $1`
	err := fixture.db.QueryRow(query, id).Scan(&status, &needsApproval, &messageID, &minutesLeft)

	if (err != nil) || (status != settings.StatusAwaitingApproval) || (needsApproval == false) || (messageID != 101) || (minutesLeft < 14) || (minutesLeft > 15) {
		t.Fatalf("awaiting: %s %v %d %.2f %v", status, needsApproval, messageID, minutesLeft, err)
	}

	if (approver.askCount() != 1) || (config.GetApifyMonthlyBudgetUsd() != 4.8) {
		t.Fatalf("one ask and no change yet: %d, budget %v", approver.askCount(), config.GetApifyMonthlyBudgetUsd())
	}

	answer := harness.manager.handleTap(context.Background(), approvalTap{requestID: id, isApprove: true})
	var actor, note, updatedBy string
	var value string
	query = `SELECT a.actor, a.note, s.updated_by, s.value::text FROM settings_audit a JOIN bot_settings s ON s.key = a.key WHERE a.request_id = $1`
	err = fixture.db.QueryRow(query, id).Scan(&actor, &note, &updatedBy, &value)
	finalStatus, _ := fixture.requestStatus(t, id)

	if (answer != tapAppliedText) || (finalStatus != settings.StatusApplied) || (config.GetApifyMonthlyBudgetUsd() != 6) {
		t.Fatalf("answer %q, status %s, budget %v", answer, finalStatus, config.GetApifyMonthlyBudgetUsd())
	}

	if (err != nil) || (actor != settings.ActorDashboard) || (note != approvedNote) || (updatedBy != settings.ActorDashboard) || (value != `6`) {
		t.Fatalf("audit: %s %q %s %s %v", actor, note, updatedBy, value, err)
	}
}

func TestPGRejectAndChangedMeanwhileWriteNothing(t *testing.T) {
	fixture, harness, _ := newPGApprovalHarness(t)
	rejectedID := fixture.awaitRequest(t, settings.KeyApifyMonthlyBudgetUsd, `6`, `4.8`, time.Minute)
	rejectAnswer := harness.manager.handleTap(context.Background(), approvalTap{requestID: rejectedID, isApprove: false})
	rejectStatus, rejectReason := fixture.requestStatus(t, rejectedID)
	changedID := fixture.awaitRequest(t, settings.KeyApifyMonthlyBudgetUsd, `6`, `4.8`, time.Minute)
	mustExecPG(t, fixture.db, `INSERT INTO bot_settings (key, value) VALUES ($1, '5')`, settings.KeyApifyMonthlyBudgetUsd)
	changedAnswer := harness.manager.handleTap(context.Background(), approvalTap{requestID: changedID, isApprove: true})
	changedStatus, changedReason := fixture.requestStatus(t, changedID)

	if (rejectAnswer != tapRejectedText) || (rejectStatus != settings.StatusRejected) || (rejectReason != requestOwnerRejectedError) {
		t.Fatalf("reject: %q %s (%s)", rejectAnswer, rejectStatus, rejectReason)
	}

	if (changedAnswer != tapChangedText) || (changedStatus != settings.StatusRejected) || (changedReason != requestChangedError) {
		t.Fatalf("changed: %q %s (%s)", changedAnswer, changedStatus, changedReason)
	}

	if (fixture.auditCount(t, rejectedID) != 0) || (fixture.auditCount(t, changedID) != 0) {
		t.Fatalf("neither may write bot_settings")
	}
}

func TestPGFourthApprovalRequestIsRejected(t *testing.T) {
	fixture, harness, approver := newPGApprovalHarness(t)
	fixture.awaitRequest(t, settings.KeyHikerMonthlyBudgetUsd, `2`, `0`, time.Minute)
	fixture.awaitRequest(t, settings.KeyServiceFacebedHost, `"facebed.example"`, `"www.facebed.com"`, time.Minute)
	fixture.awaitRequest(t, settings.KeyServiceVxredditHost, `"vxreddit.example"`, `"vxreddit.com"`, time.Minute)
	id := fixture.queueRequest(t, settings.KeyApifyMonthlyBudgetUsd, `6`, `4.8`)
	harness.manager.processRequestsIfEnabled(context.Background())
	status, reason := fixture.requestStatus(t, id)

	if (status != settings.StatusRejected) || (reason != requestTooManyAwaiting) || (approver.askCount() != 0) {
		t.Fatalf("got %s (%s), %d asks", status, reason, approver.askCount())
	}
}

func TestPGConcurrentApprovesApplyOnce(t *testing.T) {
	fixture, _, _ := newPGApprovalHarness(t)
	store := NewPGStore(fixture.db)

	for round := 0; round < 10; round++ {
		id := fixture.awaitRequest(t, settings.KeyApifyMonthlyBudgetUsd, `6`, `4.8`, time.Minute)
		outcomes := make([]ApprovalOutcome, 2)
		errs := make([]error, 2)
		start := make(chan struct{})
		var wg sync.WaitGroup

		for index := range outcomes {
			wg.Add(1)
			go func(index int) {
				defer wg.Done()
				<-start
				outcomes[index], errs[index] = store.DecideApproval(context.Background(), id, true, decideApproved)
			}(index)
		}

		close(start)
		wg.Wait()
		decided := 0

		for index, outcome := range outcomes {
			if errs[index] != nil {
				t.Fatalf("round %d: %v", round, errs[index])
			}

			if outcome.IsDecided() == true {
				decided++
			}
		}

		if (decided != 1) || (fixture.auditCount(t, id) != 1) {
			t.Fatalf("round %d: %d taps decided, %d audit rows", round, decided, fixture.auditCount(t, id))
		}

		mustExecPG(t, fixture.db, `DELETE FROM bot_settings`)
	}
}

func TestPGApproveRacingTheExpiryEndsInOneState(t *testing.T) {
	fixture, _, _ := newPGApprovalHarness(t)
	store := NewPGStore(fixture.db)
	outcomes := map[string]int{}

	for round := 0; round < 20; round++ {
		id := fixture.awaitRequest(t, settings.KeyApifyMonthlyBudgetUsd, `6`, `4.8`, 20*time.Millisecond)
		time.Sleep(18 * time.Millisecond)
		start := make(chan struct{})
		var wg sync.WaitGroup
		var approveErr, expireErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, approveErr = store.DecideApproval(context.Background(), id, true, decideApproved)
		}()
		go func() {
			defer wg.Done()
			<-start
			_, expireErr = store.ExpireRequests(context.Background())
		}()
		close(start)
		wg.Wait()

		if (approveErr != nil) || (expireErr != nil) {
			t.Fatalf("round %d: %v %v", round, approveErr, expireErr)
		}

		// The sweep runs again after both, as the loop would.
		if _, err := store.ExpireRequests(context.Background()); err != nil {
			t.Fatalf("sweep: %v", err)
		}

		status, _ := fixture.requestStatus(t, id)
		audits := fixture.auditCount(t, id)
		isApplied := (status == settings.StatusApplied) && (audits == 1)
		isExpired := (status == settings.StatusExpired) && (audits == 0)

		if (isApplied == false) && (isExpired == false) {
			t.Fatalf("round %d: status %s with %d audit rows", round, status, audits)
		}

		outcomes[status]++
		mustExecPG(t, fixture.db, `DELETE FROM bot_settings`)
	}

	t.Logf("outcomes over 20 rounds: %v", outcomes)
}

func TestPGTapAfterExpiryIsExpiredAndSweepSkipsDecided(t *testing.T) {
	fixture, _, _ := newPGApprovalHarness(t)
	store := NewPGStore(fixture.db)
	expiredID := fixture.awaitRequest(t, settings.KeyApifyMonthlyBudgetUsd, `6`, `4.8`, -time.Second)
	outcome, err := store.DecideApproval(context.Background(), expiredID, true, decideApproved)

	if (err != nil) || (outcome.IsDecided() == true) || (outcome.Status != settings.StatusExpired) || (outcome.Request.ApprovalMessageID != 101) {
		t.Fatalf("late tap: %+v %v", outcome, err)
	}

	appliedID := fixture.awaitRequest(t, settings.KeyApifyMonthlyBudgetUsd, `6`, `4.8`, time.Minute)
	_, err = store.DecideApproval(context.Background(), appliedID, true, decideApproved)
	mustExecPG(t, fixture.db, `UPDATE settings_change_requests SET expires_at = now() - INTERVAL '1 second' WHERE id = $1`, appliedID)
	expired, sweepErr := store.ExpireRequests(context.Background())
	status, _ := fixture.requestStatus(t, appliedID)

	if (err != nil) || (sweepErr != nil) || (len(expired) != 0) || (status != settings.StatusApplied) {
		t.Fatalf("the sweep must not touch a decided request: %v %v %d %s", err, sweepErr, len(expired), status)
	}
}
