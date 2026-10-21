package botsettings

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/timoffmax/social-link-bot/config/overrides"
	"github.com/timoffmax/social-link-bot/model/settings"
)

// recordingSeeder records the rows SeedServices got and when, against the
// store's publishes.
type recordingSeeder struct {
	mu    sync.Mutex
	store *fakeStore
	seeds []string
	// publishedBefore is whether 'services' was published before the seed.
	publishedBefore bool
	err             error
}

func (r *recordingSeeder) seed(data json.RawMessage) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.seeds = append(r.seeds, string(data))
	r.publishedBefore = (r.store.status(ComponentServices) != nil) && (string(r.store.status(ComponentServices)) != string(data))

	return r.err
}

func (r *recordingSeeder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	result := len(r.seeds)

	return result
}

const storedServicesRow = `{"version":1,"services":[{"key":"tikwm"}]}`

func TestHeartbeatPublishesTheServicesComponent(t *testing.T) {
	clearSettingsEnv(t)
	services := &recordingStatus{state: map[string]any{"version": 1, "services": []any{}}}
	harness := newHarness(t, newFakeStore())
	harness.manager.servicesStatus = services.read
	harness.manager.Load(context.Background())

	if string(harness.store.status(ComponentServices)) != `{"services":[],"version":1}` {
		t.Fatalf("services: got %s", harness.store.status(ComponentServices))
	}
}

func TestNilServicesStatusPublishesNothing(t *testing.T) {
	clearSettingsEnv(t)
	harness := newHarness(t, newFakeStore())
	harness.manager.Load(context.Background())

	if harness.store.status(ComponentServices) != nil {
		t.Fatalf("no reader must publish nothing")
	}

	services := &recordingStatus{}
	harness.manager.servicesStatus = services.read
	harness.manager.reload(context.Background())

	if harness.store.status(ComponentServices) != nil {
		t.Fatalf("a nil state must publish nothing")
	}
}

func TestHeartbeatReadsTheServicesStatusOncePerBeat(t *testing.T) {
	clearSettingsEnv(t)
	services := &recordingStatus{state: map[string]any{"version": 1}}
	harness := newHarness(t, newFakeStore())
	harness.manager.servicesStatus = services.read
	harness.manager.Load(context.Background())
	harness.manager.reload(context.Background())

	services.mu.Lock()
	calls := services.calls
	services.mu.Unlock()

	if calls != 2 {
		t.Fatalf("one read per heartbeat (start and reload), got %d", calls)
	}
}

func TestLoadSeedsTheServicesBeforeTheFirstPublish(t *testing.T) {
	clearSettingsEnv(t)
	store := newFakeStore()
	store.statuses[ComponentServices] = json.RawMessage(storedServicesRow)
	seeder := &recordingSeeder{store: store}
	services := &recordingStatus{state: map[string]any{"version": 1, "fresh": true}}
	harness := newHarness(t, store)
	harness.manager.seedServicesHealth = seeder.seed
	harness.manager.servicesStatus = services.read
	harness.manager.Load(context.Background())

	if (seeder.count() != 1) || (seeder.seeds[0] != storedServicesRow) || (seeder.publishedBefore == true) {
		t.Fatalf("the stored row must be seeded before the first publish: %+v", seeder.seeds)
	}

	if string(store.status(ComponentServices)) != `{"fresh":true,"version":1}` {
		t.Fatalf("the first publish follows the seed: %s", store.status(ComponentServices))
	}

	if len(*harness.alerts) != 0 {
		t.Fatalf("seeding must not alert: %v", *harness.alerts)
	}
}

func TestLoadWithoutAStoredRowOrSeederSeedsNothing(t *testing.T) {
	clearSettingsEnv(t)
	store := newFakeStore()
	seeder := &recordingSeeder{store: store}
	harness := newHarness(t, store)
	harness.manager.seedServicesHealth = seeder.seed
	harness.manager.Load(context.Background())

	if (seeder.count() != 0) || (slices.Equal(store.loadedStatuses(), []string{ComponentServices}) == false) {
		t.Fatalf("no row, no seed: %v %v", seeder.seeds, store.loadedStatuses())
	}

	other := newFakeStore()
	other.statuses[ComponentServices] = json.RawMessage(storedServicesRow)
	withoutSeeder := newHarness(t, other)
	withoutSeeder.manager.Load(context.Background())

	if len(other.loadedStatuses()) != 0 {
		t.Fatalf("without SeedServices nothing is read: %v", other.loadedStatuses())
	}
}

func TestFailedOrHangingServicesReadLeavesTheStartUnaffected(t *testing.T) {
	cases := map[string]func(store *fakeStore){
		"failed": func(store *fakeStore) {
			store.statusErr = errors.New("permission denied for table bot_runtime_status")
		},
		"hanging": func(store *fakeStore) {
			store.isStatusHanging = true
		},
	}

	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			clearSettingsEnv(t)
			store := newFakeStore()
			store.setRow(settings.KeyPlatformTikTokEnabled, `false`)
			store.statuses[ComponentServices] = json.RawMessage(storedServicesRow)
			setup(store)
			seeder := &recordingSeeder{store: store}
			harness := newHarness(t, store)
			harness.manager.seedServicesHealth = seeder.seed
			harness.manager.seedTimeout = 50 * time.Millisecond
			startedAt := time.Now()
			harness.manager.Load(context.Background())
			elapsed := time.Since(startedAt)
			_, isSet := overrides.Lookup(settings.KeyPlatformTikTokEnabled)

			if (elapsed > time.Second) || (seeder.count() != 0) || (len(*harness.alerts) != 0) {
				t.Fatalf("took %v, seeds %v, alerts %v", elapsed, seeder.seeds, *harness.alerts)
			}

			if (isSet == false) || (harness.manager.origin != originDB) || (harness.manager.isDBFailing == true) {
				t.Fatalf("the settings must load from the database as usual")
			}

			if store.status(ComponentBot) == nil {
				t.Fatalf("the start must still publish")
			}
		})
	}
}

func TestServicesAreNotPublishedUntilAFailedStartReadIsRetried(t *testing.T) {
	clearSettingsEnv(t)
	store := newFakeStore()
	store.loadErr = errors.New("connection refused")
	store.statuses[ComponentServices] = json.RawMessage(storedServicesRow)
	seeder := &recordingSeeder{store: store}
	services := &recordingStatus{state: map[string]any{"version": 1, "fresh": true}}
	harness := newHarness(t, store)
	harness.manager.seedServicesHealth = seeder.seed
	harness.manager.servicesStatus = services.read
	harness.manager.Load(context.Background())

	if string(store.status(ComponentServices)) != storedServicesRow {
		t.Fatalf("the stored row must be kept: %s", store.status(ComponentServices))
	}

	if (store.status(ComponentBot) == nil) || (store.status(ComponentSettings) == nil) {
		t.Fatalf("the other components must still be published")
	}

	store.mu.Lock()
	store.loadErr = nil
	store.mu.Unlock()
	harness.manager.reload(context.Background())

	if (seeder.count() != 1) || (seeder.seeds[0] != storedServicesRow) || (seeder.publishedBefore == true) {
		t.Fatalf("the reload seeds before the first services publish: %+v", seeder.seeds)
	}

	if string(store.status(ComponentServices)) != `{"fresh":true,"version":1}` {
		t.Fatalf("published after the seed: %s", store.status(ComponentServices))
	}

	harness.manager.reload(context.Background())

	if seeder.count() != 1 {
		t.Fatalf("seeded once: %v", seeder.seeds)
	}
}

func TestFailedServicesReadIsRetriedBeforeTheFirstServicesPublish(t *testing.T) {
	clearSettingsEnv(t)
	store := newFakeStore()
	store.statusErr = errors.New("permission denied for table bot_runtime_status")
	store.statuses[ComponentServices] = json.RawMessage(storedServicesRow)
	seeder := &recordingSeeder{store: store}
	services := &recordingStatus{state: map[string]any{"version": 1, "fresh": true}}
	harness := newHarness(t, store)
	harness.manager.seedServicesHealth = seeder.seed
	harness.manager.servicesStatus = services.read
	harness.manager.Load(context.Background())
	harness.manager.reload(context.Background())

	if (string(store.status(ComponentServices)) != storedServicesRow) || (store.status(ComponentBot) == nil) {
		t.Fatalf("a failing read keeps the stored row and publishes the rest: %s", store.status(ComponentServices))
	}

	store.mu.Lock()
	store.statusErr = nil
	store.mu.Unlock()
	harness.manager.reload(context.Background())

	if (seeder.count() != 1) || (seeder.publishedBefore == true) {
		t.Fatalf("the read is retried and seeded: %v", seeder.seeds)
	}

	if string(store.status(ComponentServices)) != `{"fresh":true,"version":1}` {
		t.Fatalf("published after the seed: %s", store.status(ComponentServices))
	}

	if len(*harness.alerts) != 0 {
		t.Fatalf("a status read failure must not alert: %v", *harness.alerts)
	}
}

func TestMalformedServicesRowIsLoggedNotAlerted(t *testing.T) {
	clearSettingsEnv(t)
	store := newFakeStore()
	store.statuses[ComponentServices] = json.RawMessage(storedServicesRow)
	seeder := &recordingSeeder{store: store, err: errors.New("unsupported services status version: 2")}
	harness := newHarness(t, store)
	harness.manager.seedServicesHealth = seeder.seed
	harness.manager.Load(context.Background())

	if (seeder.count() != 1) || (len(*harness.alerts) != 0) || (harness.manager.isDBFailing == true) {
		t.Fatalf("seeds %v, alerts %v", seeder.seeds, *harness.alerts)
	}
}

func TestNoServicesSeedingWithoutADatabaseRead(t *testing.T) {
	clearSettingsEnv(t)
	failing := newFakeStore()
	failing.loadErr = errors.New("connection refused")
	failing.statuses[ComponentServices] = json.RawMessage(storedServicesRow)
	seeder := &recordingSeeder{store: failing}
	cached := newHarness(t, failing)
	cached.manager.seedServicesHealth = seeder.seed
	cached.manager.Load(context.Background())

	if (len(failing.loadedStatuses()) != 0) || (seeder.count() != 0) {
		t.Fatalf("the cache path must not seed: %v", failing.loadedStatuses())
	}

	unavailable := newHarness(t, nil)
	unavailable.manager.databaseErr = errors.New("dial tcp: connection refused")
	unavailable.manager.seedServicesHealth = seeder.seed
	unavailable.manager.Load(context.Background())
	unconfigured := newHarness(t, nil)
	unconfigured.manager.seedServicesHealth = seeder.seed
	unconfigured.manager.Load(context.Background())

	if seeder.count() != 0 {
		t.Fatalf("no database, no seed: %v", seeder.seeds)
	}
}

func TestPGLoadStatusReadsAPublishedComponent(t *testing.T) {
	fixture := openPGFixture(t)
	store := NewPGStore(fixture.db)
	ctx := context.Background()
	_, isFound, err := store.LoadStatus(ctx, ComponentServices)

	if (err != nil) || (isFound == true) {
		t.Fatalf("no row yet: %v %v", isFound, err)
	}

	err = store.PublishStatus(ctx, ComponentServices, json.RawMessage(storedServicesRow))

	if err != nil {
		t.Fatal(err)
	}

	data, isFound, err := store.LoadStatus(ctx, ComponentServices)
	published := map[string]any{}
	_ = json.Unmarshal(data, &published)

	if (err != nil) || (isFound == false) || (published["version"] != float64(1)) {
		t.Fatalf("got %s %v %v", data, isFound, err)
	}
}
