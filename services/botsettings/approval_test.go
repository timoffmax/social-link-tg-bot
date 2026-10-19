package botsettings

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/timoffmax/social-link-bot/config"
	"github.com/timoffmax/social-link-bot/model/settings"
)

// fakeAsk is one approval message the fake approver sent.
type fakeAsk struct {
	text    string
	buttons []ApprovalButton
}

// fakeApprover records the approval messages and their edits.
type fakeApprover struct {
	mu      sync.Mutex
	asks    []fakeAsk
	edits   map[int64][]string
	failAsk bool
}

func newFakeApprover() *fakeApprover {
	result := &fakeApprover{edits: map[int64][]string{}}

	return result
}

func (a *fakeApprover) Ask(text string, buttons []ApprovalButton) (int64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	result := int64(0)
	var err error

	if a.failAsk == true {
		err = errors.New("Forbidden: bot was blocked by the user")
	}

	if a.failAsk == false {
		a.asks = append(a.asks, fakeAsk{text: text, buttons: buttons})
		result = int64(100 + len(a.asks))
	}

	return result, err
}

func (a *fakeApprover) Edit(messageID int64, text string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.edits[messageID] = append(a.edits[messageID], text)

	return nil
}

func (a *fakeApprover) askCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()

	return len(a.asks)
}

func (a *fakeApprover) lastEdit(messageID int64) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	result := ""
	edits := a.edits[messageID]

	if len(edits) > 0 {
		result = edits[len(edits)-1]
	}

	return result
}

// newApprovalHarness is a loaded manager with an owner to ask and the Apify
// budget at 4.8 from .env.
func newApprovalHarness(t *testing.T) (testHarness, *fakeApprover) {
	clearSettingsEnv(t)
	t.Setenv("APIFY_MONTHLY_BUDGET_USD", "4.8")
	approver := newFakeApprover()
	harness := newHarness(t, newFakeStore())
	harness.manager.approver = approver
	harness.manager.applyRequests = true
	harness.manager.Load(context.Background())

	return harness, approver
}

// queueBudgetRaise queues Apify 4.8 -> 6 and processes it.
func queueBudgetRaise(harness testHarness) *fakeRequest {
	request := harness.store.queue(settings.KeyApifyMonthlyBudgetUsd, `6`, `4.8`)
	harness.manager.processRequestsIfEnabled(context.Background())

	return request
}

// runLoop runs the settings loop with ticks long enough not to interfere.
func runLoop(harness testHarness) context.CancelFunc {
	harness.manager.requestEvery = time.Hour
	harness.manager.reloadEvery = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	harness.manager.wg.Add(1)
	go harness.manager.Run(ctx)

	return cancel
}

const wantApprovalAsk = "⚙️ The dashboard asks to change a setting:\nApify monthly budget (USD) 4.8 → 6\nRequest #1, expires at 15:15 Kyiv time."

func TestChangeNeedingApprovalWaitsAndAsksTheOwner(t *testing.T) {
	harness, approver := newApprovalHarness(t)
	request := queueBudgetRaise(harness)
	status, reason := harness.store.requestStatus(request)

	if (status != settings.StatusAwaitingApproval) || (reason != "") || (config.GetApifyMonthlyBudgetUsd() != 4.8) {
		t.Fatalf("got %s (%s), budget %v", status, reason, config.GetApifyMonthlyBudgetUsd())
	}

	if approver.askCount() != 1 {
		t.Fatalf("expected one approval message, got %d", approver.askCount())
	}

	ask := approver.asks[0]
	wantButtons := []ApprovalButton{{Text: approvalApproveLabel, Data: "setappr:1"}, {Text: approvalRejectLabel, Data: "setrej:1"}}

	if (ask.text != wantApprovalAsk) || (len(ask.buttons) != 2) || (ask.buttons[0] != wantButtons[0]) || (ask.buttons[1] != wantButtons[1]) {
		t.Fatalf("approval message:\n%q\n%+v", ask.text, ask.buttons)
	}

	if harness.store.messageID(request) != 101 {
		t.Fatalf("the message id must be stored, got %d", harness.store.messageID(request))
	}
}

func TestApproveAppliesExactlyOnce(t *testing.T) {
	harness, approver := newApprovalHarness(t)
	request := queueBudgetRaise(harness)
	cancel := runLoop(harness)
	first := harness.manager.DecideApproval(1, 101, true)
	second := harness.manager.DecideApproval(1, 101, true)
	cancel()
	harness.manager.Wait()
	status, _ := harness.store.requestStatus(request)

	if (first != tapAppliedText) || (second != tapDecidedText) || (status != settings.StatusApplied) {
		t.Fatalf("first %q, second %q, status %s", first, second, status)
	}

	if config.GetApifyMonthlyBudgetUsd() != 6 {
		t.Fatalf("the approved budget must apply, got %v", config.GetApifyMonthlyBudgetUsd())
	}

	edit := approver.lastEdit(101)

	if (strings.Contains(edit, "Apify monthly budget (USD) 4.8 → 6") == false) || (strings.HasSuffix(edit, approvalStateApplied) == false) {
		t.Fatalf("the message must keep the change and say applied: %q", edit)
	}

	digest := strings.Join(*harness.alerts, "\n")

	if (strings.Count(digest, "4.8 → 6") != 1) || (strings.Contains(digest, "dashboard, approved in Telegram") == false) {
		t.Fatalf("the digest must report the change once as approved: %q", digest)
	}
}

func TestRejectLeavesTheValue(t *testing.T) {
	harness, approver := newApprovalHarness(t)
	request := queueBudgetRaise(harness)
	answer := harness.manager.handleTap(context.Background(), approvalTap{requestID: 1, isApprove: false})
	status, reason := harness.store.requestStatus(request)

	if (answer != tapRejectedText) || (status != settings.StatusRejected) || (reason != requestOwnerRejectedError) || (config.GetApifyMonthlyBudgetUsd() != 4.8) {
		t.Fatalf("answer %q, %s (%s), budget %v", answer, status, reason, config.GetApifyMonthlyBudgetUsd())
	}

	if strings.HasSuffix(approver.lastEdit(101), approvalStateRejected) == false {
		t.Fatalf("the message must say rejected: %q", approver.lastEdit(101))
	}

	again := harness.manager.handleTap(context.Background(), approvalTap{requestID: 1, isApprove: true})

	if (again != tapDecidedText) || (config.GetApifyMonthlyBudgetUsd() != 4.8) {
		t.Fatalf("a rejected request is never revived: %q, budget %v", again, config.GetApifyMonthlyBudgetUsd())
	}
}

func TestExpiryEditsTheMessageAndRefusesALateTap(t *testing.T) {
	harness, approver := newApprovalHarness(t)
	request := queueBudgetRaise(harness)
	harness.store.expire(request)
	harness.manager.processRequestsIfEnabled(context.Background())
	status, reason := harness.store.requestStatus(request)

	if (status != settings.StatusExpired) || (reason != requestApprovalExpiredError) {
		t.Fatalf("got %s (%s)", status, reason)
	}

	edit := approver.lastEdit(101)

	if (strings.Contains(edit, "4.8 → 6") == false) || (strings.HasSuffix(edit, approvalStateExpired) == false) {
		t.Fatalf("the message must say expired: %q", edit)
	}

	answer := harness.manager.handleTap(context.Background(), approvalTap{requestID: 1, isApprove: true})

	if (answer != tapExpiredText) || (config.GetApifyMonthlyBudgetUsd() != 4.8) {
		t.Fatalf("a late tap must change nothing: %q, budget %v", answer, config.GetApifyMonthlyBudgetUsd())
	}
}

func TestTapAfterExpiryBeforeTheSweepExpires(t *testing.T) {
	harness, approver := newApprovalHarness(t)
	request := queueBudgetRaise(harness)
	harness.store.expire(request)
	answer := harness.manager.handleTap(context.Background(), approvalTap{requestID: 1, isApprove: true})
	status, _ := harness.store.requestStatus(request)

	if (answer != tapExpiredText) || (status != settings.StatusExpired) || (strings.HasSuffix(approver.lastEdit(101), approvalStateExpired) == false) {
		t.Fatalf("answer %q, status %s, edit %q", answer, status, approver.lastEdit(101))
	}
}

func TestAtMostThreeRequestsAwaitApproval(t *testing.T) {
	harness, approver := newApprovalHarness(t)
	t.Setenv("HIKERAPI_MONTHLY_BUDGET_USD", "1")
	requests := []*fakeRequest{
		harness.store.queue(settings.KeyApifyMonthlyBudgetUsd, `6`, `4.8`),
		harness.store.queue(settings.KeyHikerMonthlyBudgetUsd, `2`, `1`),
		harness.store.queue(settings.KeyServiceFacebedHost, `"facebed.example"`, `"www.facebed.com"`),
		harness.store.queue(settings.KeyServiceVxredditHost, `"vxreddit.example"`, `"vxreddit.com"`),
	}
	harness.manager.processRequestsIfEnabled(context.Background())

	for index, request := range requests[:3] {
		if status, reason := harness.store.requestStatus(request); status != settings.StatusAwaitingApproval {
			t.Fatalf("request %d: %s (%s)", index, status, reason)
		}
	}

	status, reason := harness.store.requestStatus(requests[3])

	if (status != settings.StatusRejected) || (reason != requestTooManyAwaiting) || (approver.askCount() != 3) {
		t.Fatalf("the fourth must be rejected: %s (%s), %d asks", status, reason, approver.askCount())
	}
}

func TestFailedAskRejectsTheRequest(t *testing.T) {
	harness, approver := newApprovalHarness(t)
	approver.failAsk = true
	request := queueBudgetRaise(harness)
	status, reason := harness.store.requestStatus(request)

	if (status != settings.StatusRejected) || (reason != requestAskFailedError) || (config.GetApifyMonthlyBudgetUsd() != 4.8) {
		t.Fatalf("got %s (%s), budget %v", status, reason, config.GetApifyMonthlyBudgetUsd())
	}
}

func TestApproveOnCachedSettingsKeepsTheApproval(t *testing.T) {
	harness, _ := newApprovalHarness(t)
	request := queueBudgetRaise(harness)
	harness.manager.origin = originCache
	answer := harness.manager.handleTap(context.Background(), approvalTap{requestID: 1, isApprove: true})
	status, _ := harness.store.requestStatus(request)

	if (answer != tapCachedText) || (status != settings.StatusAwaitingApproval) || (config.GetApifyMonthlyBudgetUsd() != 4.8) {
		t.Fatalf("answer %q, status %s, budget %v", answer, status, config.GetApifyMonthlyBudgetUsd())
	}

	harness.manager.origin = originDB

	if again := harness.manager.handleTap(context.Background(), approvalTap{requestID: 1, isApprove: true}); again != tapAppliedText {
		t.Fatalf("the approval must still work after the reconnect: %q", again)
	}
}

func TestApproveAfterTheValueChangedIsRejected(t *testing.T) {
	harness, approver := newApprovalHarness(t)
	request := queueBudgetRaise(harness)
	harness.store.setRow(settings.KeyApifyMonthlyBudgetUsd, `5`)
	answer := harness.manager.handleTap(context.Background(), approvalTap{requestID: 1, isApprove: true})
	status, reason := harness.store.requestStatus(request)
	rows, _ := harness.store.LoadSettings(context.Background())

	if (answer != tapChangedText) || (status != settings.StatusRejected) || (reason != requestChangedError) {
		t.Fatalf("answer %q, %s (%s)", answer, status, reason)
	}

	if (len(rows) != 1) || (string(rows[0].Value) != `5`) || (config.GetApifyMonthlyBudgetUsd() != 4.8) {
		t.Fatalf("nothing may be written or swapped: %v, budget %v", rows, config.GetApifyMonthlyBudgetUsd())
	}

	if strings.HasSuffix(approver.lastEdit(101), approvalStateChanged) == false {
		t.Fatalf("the message must say changed meanwhile: %q", approver.lastEdit(101))
	}
}

func TestTapWithDatabaseErrorChangesNothing(t *testing.T) {
	harness, _ := newApprovalHarness(t)
	request := queueBudgetRaise(harness)
	harness.store.decideErr = errors.New("connection refused")
	answer := harness.manager.handleTap(context.Background(), approvalTap{requestID: 1, isApprove: true})
	status, _ := harness.store.requestStatus(request)

	if (answer != tapFailedText) || (status != settings.StatusAwaitingApproval) || (config.GetApifyMonthlyBudgetUsd() != 4.8) {
		t.Fatalf("answer %q, status %s, budget %v", answer, status, config.GetApifyMonthlyBudgetUsd())
	}
}

func TestTapWithoutTheLoopIsBusy(t *testing.T) {
	harness, _ := newApprovalHarness(t)
	request := queueBudgetRaise(harness)
	harness.manager.tapWait = 10 * time.Millisecond
	answer := harness.manager.DecideApproval(1, 101, true)
	status, _ := harness.store.requestStatus(request)

	if (answer != tapBusyText) || (status != settings.StatusAwaitingApproval) {
		t.Fatalf("answer %q, status %s", answer, status)
	}
}

func TestTapWithoutDatabaseIsUnavailable(t *testing.T) {
	clearSettingsEnv(t)
	harness := newHarness(t, nil)

	if answer := harness.manager.DecideApproval(1, 101, true); answer != tapUnavailableText {
		t.Fatalf("got %q", answer)
	}
}

func TestParseApprovalCallback(t *testing.T) {
	cases := []struct {
		data      string
		id        int64
		isApprove bool
		isValid   bool
	}{
		{"setappr:42", 42, true, true},
		{"setrej:7", 7, false, true},
		{"setappr:", 0, false, false},
		{"setappr:-1", 0, false, false},
		{"setrej:1x", 0, false, false},
		{"setappr:1:2", 0, false, false},
		{"/stats", 0, false, false},
	}

	for _, test := range cases {
		id, isApprove, isValid := ParseApprovalCallback(test.data)

		if (id != test.id) || (isApprove != test.isApprove) || (isValid != test.isValid) {
			t.Fatalf("%s: got %d %v %v", test.data, id, isApprove, isValid)
		}
	}

	if (IsApprovalCallback("setrej:x") == false) || (IsApprovalCallback("/setrej:1") == true) {
		t.Fatalf("prefix check")
	}
}

func TestTapBeforeTheFirstDatabaseReadKeepsTheApproval(t *testing.T) {
	harness, _ := newApprovalHarness(t)
	request := queueBudgetRaise(harness)
	harness.manager.origin = originNone
	answer := harness.manager.handleTap(context.Background(), approvalTap{requestID: 1, isApprove: true})
	status, _ := harness.store.requestStatus(request)

	if (answer != tapFallbackText) || (status != settings.StatusAwaitingApproval) {
		t.Fatalf("answer %q, status %s", answer, status)
	}
}

func TestSecondTapDoesNotEditTheMessageAgain(t *testing.T) {
	harness, approver := newApprovalHarness(t)
	queueBudgetRaise(harness)
	harness.manager.handleTap(context.Background(), approvalTap{requestID: 1, messageID: 101, isApprove: true})
	answer := harness.manager.handleTap(context.Background(), approvalTap{requestID: 1, messageID: 101, isApprove: true})

	if (answer != tapDecidedText) || (len(approver.edits[101]) != 1) {
		t.Fatalf("answer %q, edits %v", answer, approver.edits[101])
	}
}

func TestApprovalExpiredErrorNamesTheTimeout(t *testing.T) {
	want := "expired: the owner did not answer in Telegram within 15 minutes"

	if requestApprovalExpiredError != want {
		t.Fatalf("got %q", requestApprovalExpiredError)
	}
}

func TestTapOnAnUnrecordedMessageRemovesItsButtons(t *testing.T) {
	harness, approver := newApprovalHarness(t)
	approver.failAsk = true
	queueBudgetRaise(harness)
	answer := harness.manager.handleTap(context.Background(), approvalTap{requestID: 1, messageID: 101, isApprove: true})

	if (answer != tapDecidedText) || (strings.Contains(approver.lastEdit(101), requestAskFailedError) == false) {
		t.Fatalf("answer %q, edit %q", answer, approver.lastEdit(101))
	}
}
