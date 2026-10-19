package botsettings

import (
	"context"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/timoffmax/social-link-bot/config/overrides"
	"github.com/timoffmax/social-link-bot/model/settings"
)

// Loop timing of the settings goroutine.
const (
	requestInterval   = 2 * time.Second
	reloadInterval    = 60 * time.Second
	requestBatch      = 20
	statusPublishWait = 5 * time.Second
	// startupTimeout bounds the start's settings read and its first publish;
	// a read that takes longer counts as the database being unavailable.
	startupTimeout = 5 * time.Second
)

// Owner alerts of the settings goroutine.
const (
	alertCachedStart   = "⚠️ Settings database unavailable at start: the %d setting(s) set in the dashboard come from the cached settings of %s; every other setting comes from .env or its default. %s"
	alertNoCacheStart  = "⚠️ Settings database unavailable at start and no settings cache (%v): running on .env and the defaults. %s"
	alertInvalidStored = "⚠️ The stored value of %s is no longer valid (%v); using its safe value %v until it is changed."
	cacheTimeLayout    = "2006-01-02 15:04 MST"
)

// Endings of the start alerts: how the bot gets back to the database values.
const (
	// recoveryReload: the bot connected but could not read the settings.
	recoveryReload = "The bot switches to the database values at its first successful reload (every minute)."
	// recoveryRestart: the bot could not connect at all and does not
	// reconnect.
	recoveryRestart = "The bot does not reconnect to the database: restart it once the database is back."
)

// Options wires the settings goroutine.
type Options struct {
	// Store is the database side; nil when the bot runs without a database.
	Store Store
	// DatabaseErr is, without a Store, why a configured database could not
	// be opened at start (services.InitDatabase's error); nil when no
	// database is configured. With it the start uses the cache file like a
	// failed settings read, else .env and the defaults, with one alert.
	DatabaseErr error
	// CachePath is the cache file (DefaultCachePath when empty).
	CachePath string
	// Notify sends the owner a message (one per event, unthrottled).
	Notify func(text string) bool
	// PushProfile renders the Telegram profile from the current settings and
	// pushes the parts whose fingerprint differs from known (see
	// services.PushBotProfile); nil pushes nothing.
	PushProfile func(known map[string]string) (map[string]string, []string, error)
	// Now is the clock (time.Now when nil).
	Now func() time.Time
	// ApplyRequests lets the loop apply queued change requests. Without it no
	// queued request is applied, and the loop marks one expired once its
	// expires_at has passed (checked every 2 s), so a stale request never
	// keeps its key's one open request slot.
	ApplyRequests bool
}

// Manager is the settings goroutine. Load runs once at start; Run is the
// loop. Both, and everything they call, run on one goroutine, which is the
// only writer of the snapshot.
type Manager struct {
	store       Store
	databaseErr error
	cachePath   string
	notify      func(text string) bool
	now         func() time.Time
	profile     *profileSyncer
	startedAt   time.Time
	loadedAt    time.Time
	origin      string
	stored      map[string]StoredValue
	loggedKeys  map[string]bool
	isDBFailing bool
	// isCacheFailing is set while writing the cache file fails, so the
	// failure is logged once until a write succeeds again.
	isCacheFailing bool
	wg             sync.WaitGroup

	applyRequests bool
	// digest collects the applied changes for the owner's message.
	digest *changeDigest

	requestEvery   time.Duration
	reloadEvery    time.Duration
	startupTimeout time.Duration
	// afterReloadRead runs between a reload's read and its swap (tests).
	afterReloadRead func()
}

// Snapshot origins (bot_runtime_status.bot.settings_source).
const (
	originDB    = settings.SourceDB
	originCache = settings.SourceCache
	originNone  = "none"
)

// New returns the settings goroutine; nothing runs until Load.
func New(options Options) *Manager {
	now := options.Now
	cachePath := options.CachePath

	if now == nil {
		now = time.Now
	}

	if cachePath == "" {
		cachePath = DefaultCachePath
	}

	result := &Manager{
		store:        options.Store,
		databaseErr:  options.DatabaseErr,
		cachePath:    cachePath,
		notify:       options.Notify,
		now:          now,
		profile:      newProfileSyncer(options.PushProfile, now),
		startedAt:    now(),
		origin:       originNone,
		stored:       map[string]StoredValue{},
		loggedKeys:   map[string]bool{},
		requestEvery: requestInterval,
		reloadEvery:  reloadInterval,

		startupTimeout: startupTimeout,

		applyRequests: options.ApplyRequests,
		digest:        newChangeDigest(),
	}

	return result
}

// Load reads the owner-set values at start. Without a database configured
// the settings are inert: no snapshot, no cache file read or written, no
// alert and nothing published, so every getter reads .env and its default as
// before the dashboard. With a configured database that could not be opened
// (DatabaseErr) it uses the cache file (one owner alert), else .env and the
// defaults (one owner alert), and publishes nothing. With a database, it
// reads bot_settings within startupTimeout; if that fails it uses the cache
// file or .env the same way. Then it publishes the bot's state, also within
// startupTimeout.
func (m *Manager) Load(ctx context.Context) {
	isUnavailable := (m.store == nil) && (m.databaseErr != nil)
	isNotConfigured := (m.store == nil) && (m.databaseErr == nil)

	if m.store != nil {
		m.loadFromStore(ctx)
	}

	if isUnavailable == true {
		m.loadFromCache(m.databaseErr, recoveryRestart)
	}

	if isNotConfigured == true {
		log.Println("⚙️ Settings: no database configured, using .env and the defaults")
	}
}

// loadFromStore is Load with a database.
func (m *Manager) loadFromStore(ctx context.Context) {
	loadCtx, cancelLoad := context.WithTimeout(ctx, m.startupTimeout)
	values, err := m.store.LoadSettings(loadCtx)
	cancelLoad()

	if err == nil {
		m.loadedAt = m.now()
		m.swap(values, originDB)
		m.saveCache(values)
		log.Printf("⚙️ Settings loaded: %d owner-set value(s) from the database", len(values))
	}

	if err != nil {
		m.loadFromCache(err, recoveryReload)
	}

	publishCtx, cancelPublish := context.WithTimeout(ctx, m.startupTimeout)
	m.publishAll(publishCtx)
	cancelPublish()
}

// loadFromCache uses the cache file after the database failed at start;
// recovery ends the owner alert with how the bot gets back to the database.
func (m *Manager) loadFromCache(dbErr error, recovery string) {
	values, savedAt, err := readCache(m.cachePath)

	if err == nil {
		m.loadedAt = m.now()
		m.swap(values, originCache)
		savedText := savedAt.Format(cacheTimeLayout)
		log.Printf("⚠️ Settings database unavailable (%v): using %d cached value(s) from %s", dbErr, len(values), savedText)
		alertText := fmt.Sprintf(alertCachedStart, len(values), savedText, recovery)
		m.alert(alertText)
	}

	if err != nil {
		log.Printf("⚠️ Settings database unavailable (%v) and no usable cache (%v): using .env and the defaults", dbErr, err)
		alertText := fmt.Sprintf(alertNoCacheStart, err, recovery)
		m.alert(alertText)
	}
}

// Start runs the loop on its own goroutine until ctx is done; Wait waits
// for it. Without a database (none configured, or one that could not be
// opened at start) there is nothing to apply or reload, and the loop does not
// start (the bot does not reconnect).
func (m *Manager) Start(ctx context.Context) {
	if m.store != nil {
		m.wg.Add(1)
		go m.Run(ctx)
		log.Println("Started settings loop")
	}

	if (m.store == nil) && (m.databaseErr == nil) {
		log.Println("Settings loop not started: no database configured")
	}

	if (m.store == nil) && (m.databaseErr != nil) {
		log.Println("Settings loop not started: the database was unavailable at start")
	}
}

// Wait returns once the loop has stopped.
func (m *Manager) Wait() {
	m.wg.Wait()
}

// Run is the loop: every 2 s the queued requests (applied when applying them
// is on, else only the expired ones marked expired), a due profile push and
// a due change digest; a full reload with the heartbeat every 60 s. It stops
// once ctx is done, sending the pending digest first.
func (m *Manager) Run(ctx context.Context) {
	defer m.wg.Done()
	requestTicker := time.NewTicker(m.requestEvery)
	reloadTicker := time.NewTicker(m.reloadEvery)
	isRunning := true
	defer requestTicker.Stop()
	defer reloadTicker.Stop()

	for isRunning == true {
		select {
		case <-ctx.Done():
			isRunning = false
			m.flushDigest()
			log.Println("Settings loop stopped")
		case <-requestTicker.C:
			m.processRequestsIfEnabled(ctx)
			m.syncProfileIfDue(ctx)
			m.flushDigestIfDue()
		case <-reloadTicker.C:
			m.reload(ctx)
			m.flushDigestIfDue()
		}
	}
}

// processRequestsIfEnabled processes the queued requests when the manager
// was built with ApplyRequests; without it, it only expires the queued
// requests whose expires_at has passed.
func (m *Manager) processRequestsIfEnabled(ctx context.Context) {
	if m.applyRequests == true {
		m.processRequests(ctx)
	}

	if m.applyRequests == false {
		m.expireRequests(ctx)
	}
}

// expireRequests marks the queued requests past their expires_at expired.
func (m *Manager) expireRequests(ctx context.Context) int64 {
	result, err := m.store.ExpireRequests(ctx)
	m.recordDBResult(err, "expire settings requests")

	if result > 0 {
		log.Printf("⚙️ Expired %d settings request(s): applying requests is off", result)
	}

	return result
}

// processRequests applies or rejects queued requests, oldest first, until
// none is queued, the batch is full or ctx is done. While the snapshot still
// comes from the cache file (the database was down at start), requests wait
// for the first successful reload, so an apply never mixes cached values
// into the database snapshot.
func (m *Manager) processRequests(ctx context.Context) int {
	result := 0
	isClaimed := (m.origin == originDB)

	for (isClaimed == true) && (result < requestBatch) && (ctx.Err() == nil) {
		isClaimed = m.processNextRequest(ctx)

		if isClaimed == true {
			result++
		}
	}

	return result
}

// processNextRequest handles the oldest queued request. The snapshot changes
// only after the transaction that applied it committed.
func (m *Manager) processNextRequest(ctx context.Context) bool {
	request, decision, isClaimed, err := m.store.ProcessNextRequest(ctx, decideRequest)
	m.recordDBResult(err, "process settings requests")

	if (isClaimed == true) && (decision.IsApplied() == true) {
		m.applyCommitted(ctx, request, decision)
	}

	if (isClaimed == true) && (decision.IsApplied() == false) {
		log.Printf("⚙️ Settings request #%d (%s) %s: %s", request.ID, request.Key, decision.Status, decision.Error)
	}

	return isClaimed
}

// applyCommitted puts an applied request into the snapshot, the cache and
// the published state.
func (m *Manager) applyCommitted(ctx context.Context, request ChangeRequest, decision Decision) {
	oldValue := effectiveValue(request.Key)
	values := m.storedValues()
	values = withoutKey(values, request.Key)

	if decision.Value != nil {
		values = append(values, StoredValue{Key: request.Key, Value: decision.Value})
	}

	m.swap(values, originDB)
	m.saveCache(values)
	m.profile.schedule()
	m.publishSettings(ctx)
	text := describeChange(request.Key, oldValue, decision.TypedValue, request.IsReset())
	appliedAt := m.now()
	m.digest.add(digestEntry{At: appliedAt, Text: text, Actor: request.Source})
	log.Printf("⚙️ Settings request #%d applied: %s = %s (from %s)", request.ID, request.Key, describeValue(decision), request.Source)
}

// describeValue prints an applied value; a reset says so.
func describeValue(decision Decision) string {
	result := fmt.Sprintf("%v (reset to .env / default)", decision.TypedValue)

	if decision.Value != nil {
		result = string(decision.Value)
	}

	return result
}

// withoutKey drops key from values.
func withoutKey(values []StoredValue, key string) []StoredValue {
	result := make([]StoredValue, 0, len(values))

	for _, value := range values {
		if value.Key != key {
			result = append(result, value)
		}
	}

	return result
}

// reload re-reads bot_settings (picking up manual psql edits) and publishes
// the heartbeat. A failed read keeps the current snapshot. The cache file is
// written only when the values changed.
func (m *Manager) reload(ctx context.Context) {
	values, err := m.store.LoadSettings(ctx)
	m.recordDBResult(err, "reload settings")

	if m.afterReloadRead != nil {
		m.afterReloadRead()
	}

	if err == nil {
		m.loadedAt = m.now()
		isChanged := m.swapIfChanged(values)

		if isChanged == true {
			m.saveCache(values)
		}
	}

	m.publishHeartbeat(ctx)
}

// swapIfChanged swaps the snapshot (and schedules a profile check) only
// when the stored values differ from the ones in use, or the ones in use
// came from the cache file; it reports whether it swapped.
func (m *Manager) swapIfChanged(values []StoredValue) bool {
	current := m.storedValues()
	result := (m.origin != originDB) || (sameStoredValues(current, values) == false)

	if result == true {
		m.swapWithDigest(current, values)
		m.profile.schedule()
		log.Printf("⚙️ Settings reloaded: %d owner-set value(s), changed outside the bot", len(values))
	}

	return result
}

// swapWithDigest swaps in the reloaded values and adds each key whose stored
// value changed to the digest. Without a snapshot before (.env and the
// defaults after a failed start) there is nothing to compare, so nothing is
// added.
func (m *Manager) swapWithDigest(current []StoredValue, values []StoredValue) {
	keys := []string{}
	oldValues := map[string]any{}

	if m.origin != originNone {
		keys = changedKeys(current, values)
	}

	for _, key := range keys {
		oldValues[key] = effectiveValue(key)
	}

	m.swap(values, originDB)
	reloadedAt := m.now()
	reloaded := storedByKey(values)

	for _, key := range keys {
		_, isStored := reloaded[key]
		newValue := effectiveValue(key)
		text := describeChange(key, oldValues[key], newValue, (isStored == false))
		m.digest.add(digestEntry{At: reloadedAt, Text: text, Actor: digestActorOutside})
	}
}

// flushDigestIfDue sends the pending changes once the digest window is open.
func (m *Manager) flushDigestIfDue() {
	now := m.now()

	if m.digest.isDue(now) == true {
		m.digest.flush(now, m.notify)
	}
}

// flushDigest sends the pending changes at shutdown, whatever the window.
func (m *Manager) flushDigest() {
	now := m.now()

	if len(m.digest.entries) > 0 {
		m.digest.flush(now, m.notify)
	}
}

// sameStoredValues compares two sets of stored values as JSON.
func sameStoredValues(left []StoredValue, right []StoredValue) bool {
	leftByKey := storedByKey(left)
	rightByKey := storedByKey(right)
	result := (len(leftByKey) == len(rightByKey))

	for key, value := range leftByKey {
		other, isPresent := rightByKey[key]
		result = result && (isPresent == true) && (settings.SameValue(value.Value, other.Value) == true)
	}

	return result
}

// storedByKey indexes stored values by key.
func storedByKey(values []StoredValue) map[string]StoredValue {
	result := map[string]StoredValue{}

	for _, value := range values {
		result[value.Key] = value
	}

	return result
}

// storedValues returns the stored values in use, sorted by key.
func (m *Manager) storedValues() []StoredValue {
	result := make([]StoredValue, 0, len(m.stored))

	for _, value := range m.stored {
		result = append(result, value)
	}

	sort.Slice(result, func(i, j int) bool { return result[i].Key < result[j].Key })

	return result
}

// swap validates values with the registry and replaces the snapshot the
// getters read. An unknown key, or an invalid value of a key without a safe
// value, is skipped (logged once), so .env or the default applies; an
// invalid money or payment value gives its safe value, with one owner alert.
func (m *Manager) swap(values []StoredValue, origin string) {
	entries := map[string]overrides.Entry{}
	source := settings.SourceDB

	if origin == originCache {
		source = settings.SourceCache
	}

	for _, value := range values {
		m.addEntry(entries, value, source)
	}

	m.stored = storedByKey(values)
	m.origin = origin
	swappedAt := m.now()
	snapshot := overrides.NewSnapshot(entries, swappedAt)
	overrides.Set(snapshot)
}

// addEntry validates one stored value into entries.
func (m *Manager) addEntry(entries map[string]overrides.Entry, value StoredValue, source string) {
	setting, isKnown := settings.Lookup(value.Key)
	typed, resolvedSource, err := resolveStored(setting, value.Value)

	if isKnown == false {
		text := fmt.Sprintf("⚠️ Ignoring the stored setting %s: not in the registry", value.Key)
		m.logOnce(value.Key, text)
	}

	if (isKnown == true) && (err == nil) {
		entries[value.Key] = overrides.Entry{Value: typed, Source: source}
	}

	if (isKnown == true) && (err != nil) && (resolvedSource == settings.SourceSafe) {
		entries[value.Key] = overrides.Entry{Value: typed, Source: settings.SourceSafe}
		text := fmt.Sprintf(alertInvalidStored, value.Key, err, typed)
		m.alertOnce(value.Key, text)
	}

	if (isKnown == true) && (err != nil) && (resolvedSource != settings.SourceSafe) {
		text := fmt.Sprintf("⚠️ Ignoring the stored setting %s (%v): using .env or the default", value.Key, err)
		m.logOnce(value.Key, text)
	}
}

// logOnce logs a problem with a stored key once per process.
func (m *Manager) logOnce(key string, text string) {
	if m.loggedKeys[key] == false {
		log.Println(text)
	}

	m.loggedKeys[key] = true
}

// alertOnce logs and alerts a problem with a stored key once per process.
func (m *Manager) alertOnce(key string, text string) {
	if m.loggedKeys[key] == false {
		log.Println(text)
		m.alert(text)
	}

	m.loggedKeys[key] = true
}

// alert tells the owner.
func (m *Manager) alert(text string) {
	isSent := false

	if m.notify != nil {
		isSent = m.notify(text)
	}

	if isSent == false {
		log.Printf("⚠️ Could not alert the owner: %s", text)
	}
}

// saveCache writes the cache file; a failure only costs the DB-down start.
// A failure is logged once, until a write succeeds again.
func (m *Manager) saveCache(values []StoredValue) {
	savedAt := m.now()
	err := writeCache(m.cachePath, values, savedAt)

	if (err != nil) && (m.isCacheFailing == false) {
		log.Printf("⚠️ Could not write the settings cache %s: %v", m.cachePath, err)
	}

	if (err == nil) && (m.isCacheFailing == true) {
		log.Printf("✅ Settings cache %s written again", m.cachePath)
	}

	m.isCacheFailing = (err != nil)
}

// recordDBResult logs the first failure of a database outage and the
// recovery, not every 2 s attempt in between.
func (m *Manager) recordDBResult(err error, action string) {
	if (err != nil) && (m.isDBFailing == false) {
		log.Printf("❌ Failed to %s (keeping the current settings): %v", action, err)
	}

	if (err == nil) && (m.isDBFailing == true) {
		log.Printf("✅ Settings database reachable again")
	}

	m.isDBFailing = (err != nil)
}

// PushInitialProfile pushes the whole Telegram profile at start, as the bot
// did before the settings, and remembers what Telegram now shows.
func (m *Manager) PushInitialProfile(ctx context.Context) error {
	result := m.profile.pushInitial()
	m.publishProfile(ctx)

	return result
}

// syncProfileIfDue pushes the profile parts that changed, once the debounce
// after the last change has passed.
func (m *Manager) syncProfileIfDue(ctx context.Context) {
	isPushed := m.profile.syncIfDue(m.alert)

	if isPushed == true {
		m.publishProfile(ctx)
	}
}
