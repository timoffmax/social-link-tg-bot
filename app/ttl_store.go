package app

import (
	"sync"
	"time"
)

// ttlStorePruneInterval is the least time between two prunes of a store, so
// a large store isn't scanned on every write.
const ttlStorePruneInterval = time.Minute

// ttlStore is a small in-memory map whose entries expire after ttl. Expired
// entries are dropped on writes, at most once per ttlStorePruneInterval, so
// the map holds about one ttl of entries.
type ttlStore struct {
	mu        sync.Mutex
	ttl       time.Duration
	items     map[string]ttlStoreItem
	lastPrune time.Time
}

// ttlStoreItem is a stored value and when it was stored.
type ttlStoreItem struct {
	value    interface{}
	storedAt time.Time
}

// newTTLStore returns an empty store whose entries live for ttl.
func newTTLStore(ttl time.Duration) *ttlStore {
	result := &ttlStore{ttl: ttl, items: map[string]ttlStoreItem{}}

	return result
}

// add stores the value under key unless a live entry is there; it reports
// whether it stored it.
func (store *ttlStore) add(key string, value interface{}) bool {
	store.mu.Lock()
	defer store.mu.Unlock()

	now := time.Now()
	store.pruneIfDue(now)
	_, isLive := store.liveItem(key, now)
	result := (isLive == false)

	if result == true {
		store.items[key] = ttlStoreItem{value: value, storedAt: now}
	}

	return result
}

// get returns the live value under key.
func (store *ttlStore) get(key string) (interface{}, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()

	now := time.Now()
	item, result := store.liveItem(key, now)

	return item.value, result
}

// remove drops the entry under key.
func (store *ttlStore) remove(key string) {
	store.mu.Lock()
	defer store.mu.Unlock()

	delete(store.items, key)
}

// liveItem returns the entry under key if it hasn't expired; the caller
// holds mu.
func (store *ttlStore) liveItem(key string, now time.Time) (ttlStoreItem, bool) {
	item, exists := store.items[key]
	age := now.Sub(item.storedAt)
	result := (exists == true) && (age < store.ttl)

	return item, result
}

// pruneIfDue drops expired entries when the last prune is old enough; the
// caller holds mu.
func (store *ttlStore) pruneIfDue(now time.Time) {
	sincePrune := now.Sub(store.lastPrune)

	if sincePrune >= ttlStorePruneInterval {
		cutoff := now.Add(-store.ttl)
		store.dropBefore(cutoff)
		store.lastPrune = now
	}
}

// dropBefore deletes entries stored before the cutoff; the caller holds mu.
func (store *ttlStore) dropBefore(cutoff time.Time) {
	for key, item := range store.items {
		if item.storedAt.Before(cutoff) == true {
			delete(store.items, key)
		}
	}
}

// takeIf returns the live value under key and removes it when accept says
// yes, atomically, so only one caller gets it; otherwise it is left in place.
func (store *ttlStore) takeIf(key string, accept func(value interface{}) bool) (interface{}, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()

	now := time.Now()
	item, isLive := store.liveItem(key, now)
	result := (isLive == true) && (accept(item.value) == true)

	if result == true {
		delete(store.items, key)
	}

	return item.value, result
}

// set stores the value under key, replacing a live entry.
func (store *ttlStore) set(key string, value interface{}) {
	store.mu.Lock()
	defer store.mu.Unlock()

	now := time.Now()
	store.pruneIfDue(now)
	store.items[key] = ttlStoreItem{value: value, storedAt: now}
}
