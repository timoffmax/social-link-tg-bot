package botsettings

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/timoffmax/social-link-bot/config"
	"github.com/timoffmax/social-link-bot/model/settings"
)

// alertCount reads the number of owner messages so far.
func alertCount(harness testHarness) int {
	return len(*harness.alerts)
}

func TestAppliedChangesAreBatchedIntoOneDigest(t *testing.T) {
	clearSettingsEnv(t)
	harness := newHarness(t, newFakeStore())
	harness.manager.applyRequests = true
	harness.manager.Load(context.Background())
	harness.store.queue(settings.KeyPlatformTikTokEnabled, `false`, `true`)
	harness.store.queue(settings.KeyDefaultDailyMediaLimit, `30`, `0`)
	harness.manager.processRequestsIfEnabled(context.Background())
	harness.manager.flushDigestIfDue()

	if alertCount(harness) != 1 {
		t.Fatalf("want one digest, got %v", *harness.alerts)
	}

	// 12:00 UTC is 15:00 in Kyiv (EEST) on 19 October.
	want := "⚙️ Settings changed:\n• TikTok off (15:00, dashboard)\n• Default daily media limit per person 0 → 30 (15:00, dashboard)"

	if (*harness.alerts)[0] != want {
		t.Fatalf("got %q, want %q", (*harness.alerts)[0], want)
	}
}

func TestDigestIsSentAtMostOncePerFiveMinutes(t *testing.T) {
	clearSettingsEnv(t)
	harness := newHarness(t, newFakeStore())
	harness.manager.applyRequests = true
	harness.manager.Load(context.Background())
	harness.store.queue(settings.KeyPlatformTikTokEnabled, `false`, `true`)
	harness.manager.processRequestsIfEnabled(context.Background())
	harness.manager.flushDigestIfDue()
	harness.clock.Advance(time.Minute)
	harness.store.queue(settings.KeyPlatformRedditEnabled, `false`, `true`)
	harness.manager.processRequestsIfEnabled(context.Background())
	harness.manager.flushDigestIfDue()

	if alertCount(harness) != 1 {
		t.Fatalf("a second digest within 5 minutes must wait: %v", *harness.alerts)
	}

	harness.clock.Advance(4 * time.Minute)
	harness.manager.flushDigestIfDue()

	if (alertCount(harness) != 2) || (strings.Contains((*harness.alerts)[1], "Reddit off (15:01, dashboard)") == false) {
		t.Fatalf("the pending change must go out once the window opens: %v", *harness.alerts)
	}

	harness.clock.Advance(10 * time.Minute)
	harness.manager.flushDigestIfDue()

	if alertCount(harness) != 2 {
		t.Fatalf("no digest without changes: %v", *harness.alerts)
	}
}

func TestFailedDigestIsKeptForTheNextWindow(t *testing.T) {
	clearSettingsEnv(t)
	harness := newHarness(t, newFakeStore())
	harness.manager.applyRequests = true
	harness.manager.Load(context.Background())
	isFailing := true
	sent := []string{}
	harness.manager.notify = func(text string) bool {
		if isFailing == false {
			sent = append(sent, text)
		}

		return (isFailing == false)
	}
	harness.store.queue(settings.KeyPlatformTikTokEnabled, `false`, `true`)
	harness.manager.processRequestsIfEnabled(context.Background())
	harness.manager.flushDigestIfDue()
	isFailing = false
	harness.manager.flushDigestIfDue()

	if len(sent) != 0 {
		t.Fatalf("a failed send must not retry before the window opens: %v", sent)
	}

	harness.clock.Advance(5 * time.Minute)
	harness.manager.flushDigestIfDue()

	if (len(sent) != 1) || (strings.Contains(sent[0], "TikTok off") == false) {
		t.Fatalf("the change must be resent in the next window: %v", sent)
	}
}

func TestReloadedOutsideChangesAndResetsAreInTheDigest(t *testing.T) {
	clearSettingsEnv(t)
	store := newFakeStore()
	store.setRow(settings.KeyGatedChatMonthlyCap, `15`)
	harness := newHarness(t, store)
	harness.manager.Load(context.Background())
	store.setRow(settings.KeyPlatform9GAGEnabled, `false`)
	store.mu.Lock()
	delete(store.rows, settings.KeyGatedChatMonthlyCap)
	store.mu.Unlock()
	harness.manager.reload(context.Background())
	harness.manager.flushDigestIfDue()

	if alertCount(harness) != 1 {
		t.Fatalf("want one digest, got %v", *harness.alerts)
	}

	text := (*harness.alerts)[0]
	wantLines := []string{"• 9GAG off (15:00, outside the bot)", "15 → 20 (reset to .env / default) (15:00, outside the bot)"}

	for _, line := range wantLines {
		if strings.Contains(text, line) == false {
			t.Fatalf("missing %q in %q", line, text)
		}
	}
}

func TestUnknownStoredKeyIsNamedAsIgnoredInTheDigest(t *testing.T) {
	clearSettingsEnv(t)
	store := newFakeStore()
	harness := newHarness(t, store)
	harness.manager.Load(context.Background())
	store.setRow("foo.bar", `1`)
	harness.manager.reload(context.Background())
	harness.manager.flushDigestIfDue()

	if alertCount(harness) != 1 {
		t.Fatalf("want one digest, got %v", *harness.alerts)
	}

	text := (*harness.alerts)[0]
	wantLine := "• foo.bar (not a known setting, ignored) (15:00, outside the bot)"

	if strings.Contains(text, wantLine) == false {
		t.Fatalf("missing %q in %q", wantLine, text)
	}

	if strings.Contains(text, "<nil>") == true {
		t.Fatalf("the digest must not print <nil>: %q", text)
	}
}

func TestUnchangedReloadAddsNothingToTheDigest(t *testing.T) {
	clearSettingsEnv(t)
	store := newFakeStore()
	store.setRow(settings.KeyPlatform9GAGEnabled, `false`)
	harness := newHarness(t, store)
	harness.manager.Load(context.Background())
	harness.manager.reload(context.Background())
	harness.manager.flushDigestIfDue()

	if alertCount(harness) != 0 {
		t.Fatalf("an unchanged reload must not send a digest: %v", *harness.alerts)
	}
}

func TestLoopAppliesDirectChangesRejectsApprovalOnesAndFlushesAtShutdown(t *testing.T) {
	clearSettingsEnv(t)
	t.Setenv("APIFY_MONTHLY_BUDGET_USD", "4.8")
	store := newFakeStore()
	harness := newHarness(t, store)
	harness.manager.applyRequests = true
	harness.manager.Load(context.Background())
	harness.manager.requestEvery = time.Hour
	harness.manager.reloadEvery = time.Hour
	direct := store.queue(settings.KeyPlatformTikTokEnabled, `false`, `true`)
	raise := store.queue(settings.KeyApifyMonthlyBudgetUsd, `6`, `4.8`)
	harness.manager.processRequestsIfEnabled(context.Background())
	ctx, cancel := context.WithCancel(context.Background())
	harness.manager.wg.Add(1)
	go harness.manager.Run(ctx)
	cancel()
	harness.manager.Wait()
	directStatus, _ := store.requestStatus(direct)
	raiseStatus, raiseErr := store.requestStatus(raise)

	if (directStatus != settings.StatusApplied) || (config.IsTikTokEnabled() == true) {
		t.Fatalf("the direct change must apply: %s, TikTok %v", directStatus, config.IsTikTokEnabled())
	}

	if (raiseStatus != settings.StatusRejected) || (raiseErr != requestNoApproverError) || (config.GetApifyMonthlyBudgetUsd() != 4.8) {
		t.Fatalf("the budget raise must wait for approval: %s (%s), budget %v", raiseStatus, raiseErr, config.GetApifyMonthlyBudgetUsd())
	}

	if (alertCount(harness) != 1) || (strings.Contains((*harness.alerts)[0], "TikTok off") == false) {
		t.Fatalf("the shutdown must send the pending digest: %v", *harness.alerts)
	}
}

func TestHeartbeatPublishesWhetherRequestsAreApplied(t *testing.T) {
	clearSettingsEnv(t)

	for _, isApplying := range []bool{true, false} {
		harness := newHarness(t, newFakeStore())
		harness.manager.applyRequests = isApplying
		harness.manager.Load(context.Background())
		published := botStatus{}
		_ = json.Unmarshal(harness.store.status(ComponentBot), &published)

		if published.AppliesRequests != isApplying {
			t.Fatalf("applies_requests: got %v, want %v", published.AppliesRequests, isApplying)
		}
	}
}

func TestDigestValuesAreRenderedForTheOwner(t *testing.T) {
	cases := []struct {
		key     string
		old     any
		new     any
		isReset bool
		want    string
	}{
		{settings.KeyStarsEnabled, false, true, false, "Telegram Stars support on"},
		{settings.KeyApifyMonthlyBudgetUsd, 4.8, 2.5, false, "Apify monthly budget (USD) 4.8 → 2.5"},
		{settings.KeyInstagramProviderOrder, []string{"apify", "hiker"}, []string{}, false, "→ none"},
		{settings.KeyServiceFacebedHost, "www.facebed.com", "facebed.com", false, "facebed host www.facebed.com → facebed.com"},
		{"platform.unknown.enabled", nil, nil, true, "platform.unknown.enabled (not a known setting, ignored)"},
	}

	for _, test := range cases {
		got := describeChange(test.key, test.old, test.new, test.isReset)

		if strings.Contains(got, test.want) == false {
			t.Fatalf("%s: got %q, want it to contain %q", test.key, got, test.want)
		}
	}
}

func TestDigestShowsAtMostTwentyLines(t *testing.T) {
	digest := newChangeDigest()
	at := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

	for index := 0; index < 25; index++ {
		digest.add(digestEntry{At: at, Text: "TikTok off", Actor: settings.ActorDashboard})
	}

	text := digest.render()
	lines := strings.Split(text, "\n")

	if (len(lines) != 22) || (strings.Contains(lines[21], "5 more") == false) {
		t.Fatalf("got %d lines: %q", len(lines), text)
	}
}
