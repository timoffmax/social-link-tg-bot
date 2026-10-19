package botsettings

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/timoffmax/social-link-bot/config"
	"github.com/timoffmax/social-link-bot/config/overrides"
	"github.com/timoffmax/social-link-bot/model/settings"
)

const (
	testLimitUserKey = "user:7"
	testLimitChatKey = "chat:-1001"
)

// limitRequest builds a claimed special-limit request: value "" is a reset,
// expected "" is missing, stored is the policy limit as JSON.
func limitRequest(key string, value string, expected string, stored string) ChangeRequest {
	result := ChangeRequest{ID: 1, Kind: settings.KindSpecialLimit, Key: key, Source: settings.ActorDashboard, StoredCurrent: json.RawMessage(stored)}

	if value != "" {
		result.Value = json.RawMessage(value)
	}

	if expected != "" {
		result.Expected = json.RawMessage(expected)
	}

	return result
}

// appliedChange returns the change of an applied decision.
func appliedChange(t *testing.T, decision Decision) specialLimitChange {
	change, isChange := decision.TypedValue.(specialLimitChange)

	if (decision.IsApplied() == false) || (isChange == false) {
		t.Fatalf("want an applied special limit, got %+v", decision)
	}

	return change
}

func TestSpecialLimitRaiseFromDefaultIsApplied(t *testing.T) {
	decision := decideRequest(limitRequest(testLimitChatKey, "30", "null", "null"), false)
	change := appliedChange(t, decision)

	if (change.Old != nil) || (change.New == nil) || (*change.New != 30) || (change.Target.ID != -1001) || (decision.Value != nil) {
		t.Fatalf("unexpected change %+v / %+v", change, decision)
	}
}

func TestSpecialLimitRaiseFromCustomIsApplied(t *testing.T) {
	change := appliedChange(t, decideRequest(limitRequest(testLimitUserKey, "60", "50", "50"), false))

	if (change.Old == nil) || (*change.Old != 50) || (*change.New != 60) || (change.Target.Scope != settings.SpecialLimitScopeUser) {
		t.Fatalf("unexpected change %+v", change)
	}
}

func TestSpecialLimitResetIsAppliedWithNilNew(t *testing.T) {
	change := appliedChange(t, decideRequest(limitRequest(testLimitUserKey, "", "50", "50"), false))

	if (change.New != nil) || (*change.Old != 50) {
		t.Fatalf("a reset must give nil: %+v", change)
	}
}

func TestSpecialLimitZeroIsApplied(t *testing.T) {
	change := appliedChange(t, decideRequest(limitRequest(testLimitChatKey, "0", "null", "null"), false))

	if *change.New != 0 {
		t.Fatalf("unexpected change %+v", change)
	}
}

func TestSpecialLimitChangedMeanwhileIsRejected(t *testing.T) {
	cases := []ChangeRequest{
		limitRequest(testLimitUserKey, "30", "null", "20"),
		limitRequest(testLimitUserKey, "30", "20", "null"),
		limitRequest(testLimitUserKey, "30", "20", "-1"),
		limitRequest(testLimitUserKey, "30", `"x"`, "20"),
		limitRequest(testLimitUserKey, "30", "20", `"x"`),
	}

	for _, request := range cases {
		decision := decideRequest(request, false)

		if (decision.Status != settings.StatusRejected) || (decision.Error != requestChangedError) {
			t.Fatalf("%s vs %s: got %+v", request.Expected, request.StoredCurrent, decision)
		}
	}
}

func TestSpecialLimitMissingExpectedIsRejected(t *testing.T) {
	decision := decideRequest(limitRequest(testLimitUserKey, "30", "", "null"), false)

	if (decision.Status != settings.StatusRejected) || (decision.Error != requestMissingExpectation) {
		t.Fatalf("got %+v", decision)
	}
}

func TestSpecialLimitSameValueIsRejected(t *testing.T) {
	cases := map[string]ChangeRequest{
		"the limit already is 30":          limitRequest(testLimitUserKey, "30", "30", "30"),
		"the limit already is the default": limitRequest(testLimitUserKey, "", "null", "null"),
	}

	for want, request := range cases {
		decision := decideRequest(request, false)

		if (decision.Status != settings.StatusRejected) || (strings.Contains(decision.Error, want) == false) {
			t.Fatalf("want %q, got %+v", want, decision)
		}
	}
}

func TestSpecialLimitOutOfRangeIsRejected(t *testing.T) {
	for _, value := range []string{"-1", "1001", "10.5", `"10"`, "null"} {
		decision := decideRequest(limitRequest(testLimitUserKey, value, "null", "null"), false)

		if (decision.Status != settings.StatusRejected) || (strings.Contains(decision.Error, "whole number from 0 to 1000") == false) {
			t.Fatalf("%s: got %+v", value, decision)
		}
	}

	decision := decideRequest(limitRequest("user:-7", "30", "null", "null"), false)

	if (decision.Status != settings.StatusRejected) || (strings.Contains(decision.Error, "invalid limit key") == false) {
		t.Fatalf("a bad key must be rejected: %+v", decision)
	}
}

func TestSpecialLimitNeverWaitsForApproval(t *testing.T) {
	for _, canAsk := range []bool{false, true} {
		request := limitRequest(testLimitUserKey, "1000", "null", "null")
		request.AwaitingCount = maxAwaitingApproval
		decision := decideRequest(request, canAsk)

		if (decision.IsApplied() == false) || (decision.NeedsApproval == true) {
			t.Fatalf("canAsk %v: got %+v", canAsk, decision)
		}
	}
}

func TestUnknownKindIsRejected(t *testing.T) {
	request := limitRequest(testLimitUserKey, "30", "null", "null")
	request.Kind = "other"
	decision := decideRequest(request, true)

	if (decision.Status != settings.StatusRejected) || (strings.Contains(decision.Error, `unknown request kind "other"`) == false) {
		t.Fatalf("got %+v", decision)
	}
}

func TestSettingKindStillDecidedAsBefore(t *testing.T) {
	clearSettingsEnv(t)
	t.Setenv("APIFY_MONTHLY_BUDGET_USD", "4.8")
	request := ChangeRequest{Kind: settings.KindSetting, Key: settings.KeyPlatformTikTokEnabled, Value: json.RawMessage("false"), Expected: json.RawMessage("true")}
	decision := decideRequest(request, false)

	if (decision.IsApplied() == false) || (string(decision.Value) != "false") {
		t.Fatalf("got %+v", decision)
	}

	request.Key = settings.KeyApifyMonthlyBudgetUsd
	request.Value = json.RawMessage("6")
	request.Expected = json.RawMessage("4.8")
	decision = decideRequest(request, true)

	if decision.IsAwaitingApproval() == false {
		t.Fatalf("a budget raise must still wait for approval: %+v", decision)
	}
}

func TestAppliedSpecialLimitAddsOneDigestLineAndLeavesTheSnapshot(t *testing.T) {
	clearSettingsEnv(t)
	t.Setenv("GATED_FETCH_CHAT_MONTHLY_CAP", "20")
	harness := newHarness(t, newFakeStore())
	harness.manager.Load(context.Background())
	snapshot := overrides.Current()
	publishedSettings := harness.store.status(ComponentSettings)
	harness.store.setRow(settings.KeyStarsEnabled, "true")
	request := harness.store.queueLimit(testLimitChatKey, "30", "null")
	harness.manager.processRequests(context.Background())

	if status, reason := harness.store.requestStatus(request); status != settings.StatusApplied {
		t.Fatalf("got %s: %s", status, reason)
	}

	if string(harness.store.limit(testLimitChatKey)) != "30" {
		t.Fatalf("the policy limit must be 30, got %s", harness.store.limit(testLimitChatKey))
	}

	if overrides.Current() != snapshot {
		t.Fatalf("a limit must not swap the settings snapshot")
	}

	values, _, err := readCache(harness.cache)

	if (err != nil) || (len(values) != 0) {
		t.Fatalf("a limit must not rewrite the cache: %v %v", values, err)
	}

	if harness.manager.profile.dueAt.IsZero() == false {
		t.Fatalf("a limit must not schedule a profile check")
	}

	if string(harness.store.status(ComponentSettings)) != string(publishedSettings) {
		t.Fatalf("a limit must not publish the settings")
	}

	entries := harness.manager.digest.entries

	if (len(entries) != 1) || (entries[0].Actor != settings.ActorDashboard) || (entries[0].Text != "Special downloads a month, group -1001: default (20) → 30") {
		t.Fatalf("unexpected digest %+v", entries)
	}
}

func TestRejectedSpecialLimitAddsNoDigestLine(t *testing.T) {
	clearSettingsEnv(t)
	harness := newHarness(t, newFakeStore())
	harness.manager.Load(context.Background())
	harness.store.setLimit(testLimitUserKey, "50")
	request := harness.store.queueLimit(testLimitUserKey, "60", "null")
	harness.manager.processRequests(context.Background())
	status, reason := harness.store.requestStatus(request)

	if (status != settings.StatusRejected) || (reason != requestChangedError) {
		t.Fatalf("got %s: %s", status, reason)
	}

	if (len(harness.manager.digest.entries) != 0) || (string(harness.store.limit(testLimitUserKey)) != "50") {
		t.Fatalf("a rejected limit must change nothing: %+v", harness.manager.digest.entries)
	}
}

func TestDescribeSpecialLimitChange(t *testing.T) {
	t.Setenv("GATED_FETCH_USER_MONTHLY_ALLOWANCE", "20")
	t.Setenv("GATED_FETCH_CHAT_MONTHLY_CAP", "20")
	thirty := int64(30)
	fifty := int64(50)
	hundred := int64(100)
	unlimited := int64(-1)
	user := settings.SpecialLimitTarget{Scope: settings.SpecialLimitScopeUser, ID: 7}
	chat := settings.SpecialLimitTarget{Scope: settings.SpecialLimitScopeChat, ID: -1001}
	cases := map[string]specialLimitChange{
		"Special downloads a month, user 7: default (20) → 30":      {Target: user, New: &thirty},
		"Special downloads a month, group -1001: 50 → default (20)": {Target: chat, Old: &fifty},
		"Special downloads a month, user 7: unlimited → 100":        {Target: user, Old: &unlimited, New: &hundred},
	}

	for want, change := range cases {
		if got := describeSpecialLimitChange(change); got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	}

	if config.GetGatedFetchChatMonthlyCap() != 20 {
		t.Fatalf("the test needs the chat default of 20")
	}
}

// recordingStatus is a state reader that records its calls and returns at
// once.
type recordingStatus struct {
	mu    sync.Mutex
	calls int
	state any
}

func (r *recordingStatus) read() any {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++

	return r.state
}

func TestHeartbeatPublishesThePaidComponent(t *testing.T) {
	clearSettingsEnv(t)
	paid := &recordingStatus{state: map[string]any{"order": []string{"apify", "hiker"}}}
	relay := &recordingStatus{state: map[string]any{"configured": false}}
	harness := newHarness(t, newFakeStore())
	harness.manager.paidStatus = paid.read
	harness.manager.relayStatus = relay.read
	harness.manager.Load(context.Background())

	if string(harness.store.status(ComponentPaid)) != `{"order":["apify","hiker"]}` {
		t.Fatalf("paid: got %s", harness.store.status(ComponentPaid))
	}

	if string(harness.store.status(ComponentRelay)) != `{"configured":false}` {
		t.Fatalf("relay: got %s", harness.store.status(ComponentRelay))
	}
}

func TestNilPaidStatusPublishesNothing(t *testing.T) {
	clearSettingsEnv(t)
	paid := &recordingStatus{}
	harness := newHarness(t, newFakeStore())
	harness.manager.paidStatus = paid.read
	harness.manager.Load(context.Background())

	if (harness.store.status(ComponentPaid) != nil) || (harness.store.status(ComponentRelay) != nil) {
		t.Fatalf("a nil state must publish nothing")
	}
}

func TestRegistryPublishesSpecialLimitRules(t *testing.T) {
	clearSettingsEnv(t)
	harness := newHarness(t, newFakeStore())
	harness.manager.Load(context.Background())
	published := struct {
		SpecialLimit settings.SpecialLimitRules `json:"special_limit"`
	}{}
	_ = json.Unmarshal(harness.store.status(ComponentRegistry), &published)

	if published.SpecialLimit != settings.SpecialLimits() {
		t.Fatalf("got %+v", published.SpecialLimit)
	}
}

func TestHeartbeatDoesNotCallPaidStatusWithIO(t *testing.T) {
	clearSettingsEnv(t)
	paid := &recordingStatus{state: map[string]any{"order": []string{}}}
	harness := newHarness(t, newFakeStore())
	harness.manager.paidStatus = paid.read
	harness.manager.Load(context.Background())
	harness.manager.reload(context.Background())

	paid.mu.Lock()
	calls := paid.calls
	paid.mu.Unlock()

	if calls != 2 {
		t.Fatalf("one read per heartbeat (start and reload), got %d", calls)
	}
}
