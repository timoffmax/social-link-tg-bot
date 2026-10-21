package botsettings

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/timoffmax/social-link-bot/config"
	"github.com/timoffmax/social-link-bot/config/overrides"
	"github.com/timoffmax/social-link-bot/model/settings"
)

// assertGettersUseFallbacks checks that every key has its .env / default
// value and source.
func assertGettersUseFallbacks(t *testing.T) {
	for _, setting := range settings.All() {
		effective, _ := config.GetEffectiveSetting(setting.Key)
		fallback, _ := config.GetFallbackSetting(setting.Key)
		isFallbackSource := (effective.Source == settings.SourceEnv) || (effective.Source == settings.SourceDefault)

		if (settings.SameValue(effective.Value, fallback) == false) || (isFallbackSource == false) {
			t.Fatalf("%s: got %#v from %s, want the .env / default %#v", setting.Key, effective.Value, effective.Source, fallback)
		}
	}
}

func TestLoadWithEmptyTablesChangesNoGetter(t *testing.T) {
	clearSettingsEnv(t)
	t.Setenv("ENABLE_9GAG", "false")
	t.Setenv("STARS_SUPPORT_ENABLED", "true")
	t.Setenv("APIFY_MONTHLY_BUDGET_USD", "4.8")
	harness := newHarness(t, newFakeStore())
	harness.manager.Load(context.Background())

	if (overrides.Current() == nil) || (overrides.Current().Len() != 0) || (harness.manager.origin != originDB) {
		t.Fatalf("expected an empty snapshot from the database")
	}

	assertGettersUseFallbacks(t)

	if (config.Is9GAGEnabled() == true) || (config.IsStarsSupportEnabled() == false) || (config.GetApifyMonthlyBudgetUsd() != 4.8) {
		t.Fatalf("the .env values must apply")
	}

	for _, component := range []string{ComponentBot, ComponentSecrets, ComponentSettings, ComponentRegistry, ComponentProfile} {
		if harness.store.status(component) == nil {
			t.Fatalf("%s status not published", component)
		}
	}

	published := map[string]config.EffectiveSetting{}
	_ = json.Unmarshal(harness.store.status(ComponentSettings), &published)

	if (published[settings.KeyPlatform9GAGEnabled].Source != settings.SourceEnv) || (published[settings.KeyPlatformTikTokEnabled].Source != settings.SourceDefault) {
		t.Fatalf("unexpected published sources: %+v", published)
	}

	if len(*harness.alerts) != 0 {
		t.Fatalf("a normal start must not alert: %v", *harness.alerts)
	}
}

func TestDirectChangeAppliesAfterCommitAndIsCached(t *testing.T) {
	clearSettingsEnv(t)
	harness := newHarness(t, newFakeStore())
	harness.manager.Load(context.Background())
	request := harness.store.queue(settings.KeyPlatformTikTokEnabled, `false`, `true`)
	harness.manager.processRequests(context.Background())

	if status, err := harness.store.requestStatus(request); status != settings.StatusApplied {
		t.Fatalf("got %s: %s", status, err)
	}

	if config.IsTikTokEnabled() == true {
		t.Fatalf("TikTok must be off after the apply")
	}

	values, _, err := readCache(harness.cache)

	if (err != nil) || (len(values) != 1) || (string(values[0].Value) != "false") {
		t.Fatalf("the cache must hold the applied value: %v %v", values, err)
	}

	if harness.manager.profile.dueAt.IsZero() == true {
		t.Fatalf("an applied change must schedule a profile check")
	}
}

func TestRequestsAreValidatedAndRejected(t *testing.T) {
	clearSettingsEnv(t)
	t.Setenv("APIFY_MONTHLY_BUDGET_USD", "4.8")
	harness := newHarness(t, newFakeStore())
	harness.manager.Load(context.Background())
	cases := []struct {
		key      string
		value    string
		expected string
		status   string
		reason   string
	}{
		{settings.KeyPlatformTikTokEnabled, `"off"`, `true`, settings.StatusRejected, "true or false"},
		{settings.KeyGatedGlobalMonthlyCap, `6000`, `-1`, settings.StatusRejected, "hard ceiling"},
		{settings.KeyDefaultDailyMediaLimit, `30`, `5`, settings.StatusRejected, requestChangedError},
		{"platform.myspace.enabled", `true`, `true`, settings.StatusRejected, "unknown setting"},
		{settings.KeyApifyMonthlyBudgetUsd, `6`, `4.8`, settings.StatusRejected, requestNoApproverError},
		{settings.KeyServiceFacebedHost, `"evil.example"`, `"www.facebed.com"`, settings.StatusRejected, requestNoApproverError},
		{settings.KeyHikerPricePerRequestUsd, `0.0005`, `0.001`, settings.StatusRejected, requestNoApproverError},
		{settings.KeyApifyMonthlyBudgetUsd, `2.5`, `4.8`, settings.StatusApplied, ""},
		{settings.KeyStarsEnabled, `true`, `false`, settings.StatusApplied, ""},
		{settings.KeyDefaultDailyMediaLimit, `30`, `0`, settings.StatusApplied, ""},
		{settings.KeyInstagramProviderOrder, `["hiker"]`, `["apify","hiker"]`, settings.StatusApplied, ""},
		{settings.KeyServiceFacebedHost, `"WWW.FACEBED.COM"`, `"www.facebed.com"`, settings.StatusApplied, ""},
	}

	for _, test := range cases {
		request := harness.store.queue(test.key, test.value, test.expected)
		harness.manager.processRequests(context.Background())
		status, reason := harness.store.requestStatus(request)

		if (status != test.status) || (strings.Contains(reason, test.reason) == false) {
			t.Fatalf("%s=%s: got %s (%s), want %s (%s)", test.key, test.value, status, reason, test.status, test.reason)
		}
	}

	if (config.GetApifyMonthlyBudgetUsd() != 2.5) || (config.IsStarsSupportEnabled() == false) || (config.GetDefaultDailyMediaLimit() != 30) || (config.GetFacebedHost() != "www.facebed.com") {
		t.Fatalf("applied values must be in use")
	}

	if config.GetGatedFetchGlobalMonthlyCap() != -1 {
		t.Fatalf("a rejected value must not apply")
	}
}

func TestRaisingABudgetBackNeedsApprovalButLoweringDoesNot(t *testing.T) {
	clearSettingsEnv(t)
	harness := newHarness(t, newFakeStore())
	harness.manager.Load(context.Background())
	lower := harness.store.queue(settings.KeyHikerMonthlyBudgetUsd, `0`, `1`)
	harness.manager.processRequests(context.Background())
	raise := harness.store.queue(settings.KeyHikerMonthlyBudgetUsd, `0.5`, `0`)
	harness.manager.processRequests(context.Background())
	reset := harness.store.queue(settings.KeyHikerMonthlyBudgetUsd, "", `0`)
	harness.manager.processRequests(context.Background())
	lowerStatus, _ := harness.store.requestStatus(lower)
	raiseStatus, _ := harness.store.requestStatus(raise)
	resetStatus, resetReason := harness.store.requestStatus(reset)

	if (lowerStatus != settings.StatusApplied) || (raiseStatus != settings.StatusRejected) || (resetStatus != settings.StatusRejected) || (resetReason != requestNoApproverError) {
		t.Fatalf("lower %s, raise %s, reset to the .env 1.0 %s (%s)", lowerStatus, raiseStatus, resetStatus, resetReason)
	}

	if config.GetHikerAPIMonthlyBudgetUsd() != 0 {
		t.Fatalf("the budget must stay at 0")
	}
}

func TestResetDeletesTheRowAndEnvApplies(t *testing.T) {
	clearSettingsEnv(t)
	t.Setenv("ENABLE_REDDIT", "false")
	store := newFakeStore()
	store.setRow(settings.KeyPlatformRedditEnabled, `true`)
	harness := newHarness(t, store)
	harness.manager.Load(context.Background())

	if config.IsRedditEnabled() == false {
		t.Fatalf("the stored value must win over .env")
	}

	request := harness.store.queue(settings.KeyPlatformRedditEnabled, "", `true`)
	harness.manager.processRequests(context.Background())
	status, reason := harness.store.requestStatus(request)
	values, _ := store.LoadSettings(context.Background())

	if (status != settings.StatusApplied) || (len(values) != 0) || (config.IsRedditEnabled() == true) {
		t.Fatalf("reset: %s (%s), rows %v, reddit %v", status, reason, values, config.IsRedditEnabled())
	}
}

func TestExpiredRequestIsNotApplied(t *testing.T) {
	clearSettingsEnv(t)
	harness := newHarness(t, newFakeStore())
	harness.manager.Load(context.Background())
	request := harness.store.queue(settings.KeyPlatformTikTokEnabled, `false`, `true`)
	request.request.IsExpired = true
	harness.manager.processRequests(context.Background())

	if status, _ := harness.store.requestStatus(request); (status != settings.StatusExpired) || (config.IsTikTokEnabled() == false) {
		t.Fatalf("got %s, TikTok %v", status, config.IsTikTokEnabled())
	}
}

func TestReloadPicksUpManualEditsAndKeepsSnapshotWhenDBFails(t *testing.T) {
	clearSettingsEnv(t)
	store := newFakeStore()
	harness := newHarness(t, store)
	harness.manager.Load(context.Background())
	store.setRow(settings.KeyPlatform9GAGEnabled, `false`)
	harness.manager.reload(context.Background())

	if config.Is9GAGEnabled() == true {
		t.Fatalf("the reload must pick up the manual edit")
	}

	store.mu.Lock()
	store.loadErr = errors.New("connection refused")
	store.mu.Unlock()
	harness.manager.reload(context.Background())

	if config.Is9GAGEnabled() == true {
		t.Fatalf("a failed reload must keep the snapshot")
	}
}

func TestInvalidStoredMoneyValueGivesItsSafeValue(t *testing.T) {
	clearSettingsEnv(t)
	t.Setenv("APIFY_MONTHLY_BUDGET_USD", "4.8")
	t.Setenv("DEFAULT_DAILY_MEDIA_LIMIT", "5")
	store := newFakeStore()
	store.setRow(settings.KeyApifyMonthlyBudgetUsd, `50`)
	store.setRow(settings.KeyDefaultDailyMediaLimit, `"lots"`)
	harness := newHarness(t, store)
	harness.manager.Load(context.Background())
	harness.manager.reload(context.Background())
	budget, _ := config.GetEffectiveSetting(settings.KeyApifyMonthlyBudgetUsd)

	if (budget.Value != 0.0) || (budget.Source != settings.SourceSafe) {
		t.Fatalf("an invalid stored budget must give 0, got %+v", budget)
	}

	if config.GetDefaultDailyMediaLimit() != 5 {
		t.Fatalf("an invalid non-money value falls back to .env")
	}

	if (len(*harness.alerts) != 1) || (strings.Contains((*harness.alerts)[0], settings.KeyApifyMonthlyBudgetUsd) == false) {
		t.Fatalf("expected one alert about the budget, got %v", *harness.alerts)
	}
}

func TestDatabaseDownAtStartUsesTheCacheFile(t *testing.T) {
	clearSettingsEnv(t)
	t.Setenv("ENABLE_TIKTOK", "true")
	store := newFakeStore()
	first := newHarness(t, store)
	first.manager.Load(context.Background())
	first.store.queue(settings.KeyPlatformTikTokEnabled, `false`, `true`)
	first.manager.processRequests(context.Background())
	overrides.Set(nil)
	store.mu.Lock()
	store.loadErr = errors.New("connection refused")
	store.mu.Unlock()
	second := newHarness(t, store)
	second.manager.cachePath = first.cache
	second.manager.Load(context.Background())
	tiktok, _ := config.GetEffectiveSetting(settings.KeyPlatformTikTokEnabled)
	reddit, _ := config.GetEffectiveSetting(settings.KeyPlatformRedditEnabled)

	if (tiktok.Value != false) || (tiktok.Source != settings.SourceCache) {
		t.Fatalf("the cache must keep TikTok off, got %+v", tiktok)
	}

	if reddit.Source != settings.SourceDefault {
		t.Fatalf("a key not in the cache keeps its .env / default, got %+v", reddit)
	}

	if (len(*second.alerts) != 1) || (strings.Contains((*second.alerts)[0], "cached settings") == false) || (strings.Contains((*second.alerts)[0], "every other setting comes from .env") == false) {
		t.Fatalf("expected one cache alert, got %v", *second.alerts)
	}
}

func TestStoredRowOfARemovedKeyIsIgnoredWithoutAnAlert(t *testing.T) {
	clearSettingsEnv(t)
	store := newFakeStore()
	store.rows["proxy.enabled"] = json.RawMessage(`false`)
	store.rows[settings.KeyPlatformTikTokEnabled] = json.RawMessage(`false`)
	harness := newHarness(t, store)
	harness.manager.Load(context.Background())
	harness.manager.Load(context.Background())
	_, hasRemoved := overrides.Current().Entry("proxy.enabled")

	if (hasRemoved == true) || (overrides.Current().Len() != 1) || (config.IsTikTokEnabled() == true) {
		t.Fatalf("only the known row may apply: %v", overrides.Current().Entries())
	}

	if len(*harness.alerts) != 0 {
		t.Fatalf("a removed key must not alert: %v", *harness.alerts)
	}
}

func TestNoDatabaseConfiguredWithoutCacheIsInert(t *testing.T) {
	clearSettingsEnv(t)
	t.Setenv("ENABLE_9GAG", "false")
	harness := newHarness(t, nil)
	overrides.Set(nil)
	harness.manager.Load(context.Background())
	assertGettersUseFallbacks(t)

	if (overrides.Current() != nil) || (harness.manager.origin != originNone) || (config.Is9GAGEnabled() == true) {
		t.Fatalf("without a database no snapshot may be set: %v, origin %s", overrides.Current(), harness.manager.origin)
	}

	if len(*harness.alerts) != 0 {
		t.Fatalf("a bot without a database must not alert: %v", *harness.alerts)
	}

	if _, err := os.Stat(harness.cache); err == nil {
		t.Fatalf("a bot without a database must not write the cache")
	}
}

func TestNoDatabaseConfiguredIgnoresALeftoverCache(t *testing.T) {
	clearSettingsEnv(t)
	harness := newHarness(t, nil)
	overrides.Set(nil)
	stale := []StoredValue{{Key: settings.KeyPlatformTikTokEnabled, Value: json.RawMessage(`false`)}}

	if err := writeCache(harness.cache, stale, harness.clock.Now()); err != nil {
		t.Fatalf("write the stale cache: %v", err)
	}

	harness.manager.Load(context.Background())
	assertGettersUseFallbacks(t)

	if (overrides.Current() != nil) || (config.IsTikTokEnabled() == false) || (len(*harness.alerts) != 0) {
		t.Fatalf("a leftover cache must be ignored without a database: TikTok %v, alerts %v", config.IsTikTokEnabled(), *harness.alerts)
	}
}

func TestDatabaseUnavailableAtStartUsesTheCacheFile(t *testing.T) {
	clearSettingsEnv(t)
	t.Setenv("ENABLE_TIKTOK", "true")
	harness := newHarness(t, nil)
	harness.manager.databaseErr = errors.New("failed to ping database: connection refused")
	overrides.Set(nil)
	cached := []StoredValue{{Key: settings.KeyPlatformTikTokEnabled, Value: json.RawMessage(`false`)}}

	if err := writeCache(harness.cache, cached, harness.clock.Now()); err != nil {
		t.Fatalf("write the cache: %v", err)
	}

	harness.manager.Load(context.Background())
	harness.manager.Start(context.Background())
	harness.manager.Wait()
	tiktok, _ := config.GetEffectiveSetting(settings.KeyPlatformTikTokEnabled)
	reddit, _ := config.GetEffectiveSetting(settings.KeyPlatformRedditEnabled)

	if (tiktok.Value != false) || (tiktok.Source != settings.SourceCache) || (harness.manager.origin != originCache) {
		t.Fatalf("the cache must keep TikTok off while the database is down, got %+v", tiktok)
	}

	if reddit.Source != settings.SourceDefault {
		t.Fatalf("a key not in the cache keeps its .env / default, got %+v", reddit)
	}

	if (len(*harness.alerts) != 1) || (strings.Contains((*harness.alerts)[0], "cached settings") == false) || (strings.Contains((*harness.alerts)[0], "restart it") == false) {
		t.Fatalf("expected one cache alert asking for a restart, got %v", *harness.alerts)
	}
}

func TestDatabaseUnavailableAtStartWithoutCacheUsesEnvAndAlerts(t *testing.T) {
	clearSettingsEnv(t)
	t.Setenv("ENABLE_9GAG", "false")
	harness := newHarness(t, nil)
	harness.manager.databaseErr = errors.New("failed to ping database: connection refused")
	overrides.Set(nil)
	harness.manager.Load(context.Background())
	assertGettersUseFallbacks(t)

	if (config.Is9GAGEnabled() == true) || (harness.manager.origin != originNone) {
		t.Fatalf("without a cache the .env values must apply, origin %s", harness.manager.origin)
	}

	if (len(*harness.alerts) != 1) || (strings.Contains((*harness.alerts)[0], "no settings cache") == false) || (strings.Contains((*harness.alerts)[0], "restart it") == false) {
		t.Fatalf("expected one no-cache alert asking for a restart, got %v", *harness.alerts)
	}

	if _, err := os.Stat(harness.cache); err == nil {
		t.Fatalf("an unavailable database must not write the cache")
	}
}

func TestHangingDatabaseAtStartCountsAsUnavailable(t *testing.T) {
	clearSettingsEnv(t)
	store := newFakeStore()
	store.setRow(settings.KeyPlatformTikTokEnabled, `false`)
	first := newHarness(t, store)
	first.manager.Load(context.Background())
	overrides.Set(nil)
	store.mu.Lock()
	store.isHanging = true
	store.mu.Unlock()
	second := newHarness(t, store)
	second.manager.cachePath = first.cache
	second.manager.startupTimeout = 50 * time.Millisecond
	startedAt := time.Now()
	second.manager.Load(context.Background())
	elapsed := time.Since(startedAt)
	tiktok, _ := config.GetEffectiveSetting(settings.KeyPlatformTikTokEnabled)

	if elapsed > time.Second {
		t.Fatalf("the start must not wait for a hanging database, took %v", elapsed)
	}

	if (tiktok.Value != false) || (tiktok.Source != settings.SourceCache) || (len(*second.alerts) != 1) {
		t.Fatalf("a timed-out load must use the cache with one alert: %+v %v", tiktok, *second.alerts)
	}
}

func TestDatabaseDownWithoutCacheUsesEnvAndAlerts(t *testing.T) {
	clearSettingsEnv(t)
	store := newFakeStore()
	store.loadErr = errors.New("connection refused")
	harness := newHarness(t, store)
	harness.manager.Load(context.Background())
	assertGettersUseFallbacks(t)

	if (len(*harness.alerts) != 1) || (strings.Contains((*harness.alerts)[0], "no settings cache") == false) || (strings.Contains((*harness.alerts)[0], "first successful reload") == false) {
		t.Fatalf("expected one no-cache alert, got %v", *harness.alerts)
	}

	if _, err := os.Stat(harness.cache); err == nil {
		t.Fatalf("a failed load must not write the cache")
	}
}

func TestProfileChangesAreDebouncedIntoOnePush(t *testing.T) {
	clearSettingsEnv(t)
	harness := newHarness(t, newFakeStore())
	harness.manager.Load(context.Background())
	_ = harness.manager.PushInitialProfile(context.Background())
	initial := harness.pusher.count()

	for _, key := range []string{settings.KeyPlatformTikTokEnabled, settings.KeyPlatformRedditEnabled, settings.KeyPlatform9GAGEnabled} {
		harness.store.queue(key, `false`, `true`)
		harness.manager.processRequests(context.Background())
		harness.clock.Advance(3 * time.Second)
		harness.manager.syncProfileIfDue(context.Background())
	}

	if harness.pusher.count() != initial {
		t.Fatalf("no push before the debounce has passed")
	}

	harness.clock.Advance(profileDebounce)
	harness.manager.syncProfileIfDue(context.Background())
	harness.manager.syncProfileIfDue(context.Background())

	if harness.pusher.count() != (initial + 1) {
		t.Fatalf("three changes must push once, got %d", harness.pusher.count()-initial)
	}
}

func TestProfilePushRetriesThenAlertsOnce(t *testing.T) {
	clearSettingsEnv(t)
	harness := newHarness(t, newFakeStore())
	harness.manager.Load(context.Background())
	harness.pusher.failN = 100
	harness.manager.profile.schedule()

	for attempt := 0; attempt < 10; attempt++ {
		harness.clock.Advance(time.Hour)
		harness.manager.syncProfileIfDue(context.Background())
	}

	if (harness.pusher.count() != profileMaxAttempts) || (len(*harness.alerts) != 1) {
		t.Fatalf("got %d pushes and alerts %v", harness.pusher.count(), *harness.alerts)
	}

	status := harness.manager.profile.status()

	if strings.Contains(status.Error, "Too Many Requests") == false {
		t.Fatalf("the status must carry the error, got %+v", status)
	}
}

// TestApplyDuringReloadKeepsTheValue: the loop is the only writer of the
// snapshot, so a request queued while a reload is between its read and its
// swap is applied only after the swap, and the value holds through later
// reloads (the draft's bug: a reload swapping in an older read after the
// apply).
func TestApplyDuringReloadKeepsTheValue(t *testing.T) {
	clearSettingsEnv(t)
	store := newFakeStore()
	harness := newHarness(t, store)
	harness.manager.Load(context.Background())
	harness.manager.applyRequests = true
	harness.manager.requestEvery = time.Millisecond
	harness.manager.reloadEvery = 5 * time.Millisecond
	release := make(chan struct{})
	queued := make(chan *fakeRequest, 1)
	isFirst := true
	harness.manager.afterReloadRead = func() {
		if isFirst == true {
			isFirst = false
			queued <- store.queue(settings.KeyPlatformTikTokEnabled, `false`, `true`)
			<-release
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	harness.manager.wg.Add(1)
	go harness.manager.Run(ctx)
	request := <-queued
	time.Sleep(20 * time.Millisecond)
	statusDuringReload, _ := store.requestStatus(request)
	close(release)
	deadline := time.Now().Add(2 * time.Second)

	for (time.Now().Before(deadline) == true) && (config.IsTikTokEnabled() == true) {
		time.Sleep(time.Millisecond)
	}

	time.Sleep(30 * time.Millisecond)
	cancel()
	harness.manager.Wait()
	statusAfter, _ := store.requestStatus(request)

	if statusDuringReload != settings.StatusQueued {
		t.Fatalf("no request may be applied while a reload is in flight, got %s", statusDuringReload)
	}

	if (statusAfter != settings.StatusApplied) || (config.IsTikTokEnabled() == true) {
		t.Fatalf("the applied value must hold through the reloads: %s, TikTok %v", statusAfter, config.IsTikTokEnabled())
	}
}

func TestRequestsWaitWhileTheSnapshotComesFromTheCache(t *testing.T) {
	clearSettingsEnv(t)
	store := newFakeStore()
	store.setRow(settings.KeyPlatformRedditEnabled, `false`)
	first := newHarness(t, store)
	first.manager.Load(context.Background())
	store.mu.Lock()
	store.loadErr = errors.New("connection refused")
	store.mu.Unlock()
	second := newHarness(t, store)
	second.manager.cachePath = first.cache
	second.manager.Load(context.Background())
	request := store.queue(settings.KeyPlatformTikTokEnabled, `false`, `true`)

	if second.manager.processRequests(context.Background()) != 0 {
		t.Fatalf("no request may be applied on cached settings")
	}

	store.mu.Lock()
	store.loadErr = nil
	store.mu.Unlock()
	second.manager.reload(context.Background())
	second.manager.processRequests(context.Background())

	if status, _ := store.requestStatus(request); (status != settings.StatusApplied) || (config.IsRedditEnabled() == true) || (config.IsTikTokEnabled() == true) {
		t.Fatalf("after the reload: %s, reddit %v, tiktok %v", status, config.IsRedditEnabled(), config.IsTikTokEnabled())
	}
}

// TestLoopLeavesRequestsQueuedWithoutApplyRequests: in M1 the loop runs
// (reloads, heartbeat, profile) but never applies a queued request.
func TestLoopLeavesRequestsQueuedWithoutApplyRequests(t *testing.T) {
	clearSettingsEnv(t)
	store := newFakeStore()
	harness := newHarness(t, store)
	harness.manager.Load(context.Background())
	harness.manager.requestEvery = time.Millisecond
	harness.manager.reloadEvery = 5 * time.Millisecond
	request := store.queue(settings.KeyPlatformTikTokEnabled, `false`, `true`)
	ctx, cancel := context.WithCancel(context.Background())
	harness.manager.wg.Add(1)
	go harness.manager.Run(ctx)
	time.Sleep(50 * time.Millisecond)
	cancel()
	harness.manager.Wait()
	status, _ := store.requestStatus(request)

	if (status != settings.StatusQueued) || (config.IsTikTokEnabled() == false) {
		t.Fatalf("the request must stay queued and TikTok on: %s, TikTok %v", status, config.IsTikTokEnabled())
	}
}

func TestLoopExpiresStaleRequestsWithoutApplyRequests(t *testing.T) {
	clearSettingsEnv(t)
	store := newFakeStore()
	harness := newHarness(t, store)
	harness.manager.Load(context.Background())
	stale := store.queue(settings.KeyPlatformTikTokEnabled, `false`, `true`)
	stale.request.IsExpired = true
	fresh := store.queue(settings.KeyPlatformRedditEnabled, `false`, `true`)
	harness.manager.processRequestsIfEnabled(context.Background())
	staleStatus, staleErr := store.requestStatus(stale)
	freshStatus, _ := store.requestStatus(fresh)

	if (staleStatus != settings.StatusExpired) || (staleErr != requestExpiredError) || (freshStatus != settings.StatusQueued) {
		t.Fatalf("got stale %s (%s), fresh %s", staleStatus, staleErr, freshStatus)
	}

	if (config.IsTikTokEnabled() == false) || (config.IsRedditEnabled() == false) {
		t.Fatalf("no request may be applied")
	}
}

func TestUnchangedReloadDoesNotRewriteTheCache(t *testing.T) {
	clearSettingsEnv(t)
	store := newFakeStore()
	harness := newHarness(t, store)
	harness.manager.Load(context.Background())

	if err := os.Remove(harness.cache); err != nil {
		t.Fatalf("the start must write the cache: %v", err)
	}

	harness.manager.reload(context.Background())

	if _, err := os.Stat(harness.cache); err == nil {
		t.Fatalf("an unchanged reload must not write the cache")
	}

	store.setRow(settings.KeyPlatform9GAGEnabled, `false`)
	harness.manager.reload(context.Background())
	values, _, err := readCache(harness.cache)

	if (err != nil) || (len(values) != 1) || (values[0].Key != settings.KeyPlatform9GAGEnabled) {
		t.Fatalf("a changed reload must write the cache: %v %v", values, err)
	}
}

func TestCacheWriteFailureIsLoggedOnceUntilItSucceeds(t *testing.T) {
	clearSettingsEnv(t)
	harness := newHarness(t, newFakeStore())
	var logged strings.Builder
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	blocker := t.TempDir() + "/not-a-dir"

	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("write the blocker: %v", err)
	}

	goodPath := harness.cache
	harness.manager.cachePath = blocker + "/settings-cache.json"
	harness.manager.saveCache(nil)
	harness.manager.saveCache(nil)
	failures := strings.Count(logged.String(), "Could not write the settings cache")

	if failures != 1 {
		t.Fatalf("two failed writes must log once, got %d: %s", failures, logged.String())
	}

	harness.manager.cachePath = goodPath
	harness.manager.saveCache(nil)
	harness.manager.cachePath = blocker + "/settings-cache.json"
	harness.manager.saveCache(nil)
	failures = strings.Count(logged.String(), "Could not write the settings cache")

	if (failures != 2) || (strings.Contains(logged.String(), "written again") == false) {
		t.Fatalf("a failure after a success must log again: %s", logged.String())
	}
}

func TestReloadRecordsTheLoadTimeEvenWithoutChanges(t *testing.T) {
	clearSettingsEnv(t)
	store := newFakeStore()
	harness := newHarness(t, store)
	harness.manager.Load(context.Background())
	harness.clock.Advance(time.Minute)
	harness.manager.reload(context.Background())
	published := botStatus{}
	_ = json.Unmarshal(store.status(ComponentBot), &published)

	if (harness.manager.loadedAt.Equal(harness.clock.Now()) == false) || (published.SettingsLoadedAt.Equal(harness.clock.Now()) == false) {
		t.Fatalf("settings_loaded_at must be the last successful load: %v, published %v, want %v", harness.manager.loadedAt, published.SettingsLoadedAt, harness.clock.Now())
	}

	loadedAt := harness.clock.Now()
	harness.clock.Advance(time.Minute)
	store.mu.Lock()
	store.loadErr = errors.New("connection refused")
	store.mu.Unlock()
	harness.manager.reload(context.Background())

	if harness.manager.loadedAt.Equal(loadedAt) == false {
		t.Fatalf("a failed reload must keep the last load time, got %v", harness.manager.loadedAt)
	}
}
