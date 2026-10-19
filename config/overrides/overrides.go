// Package overrides holds the snapshot of the owner-set setting values the
// config getters read before .env. It is a leaf package (stdlib only): the
// config package reads it and the bot's settings goroutine fills it, so
// config never imports the services. Reads are lock-free.
package overrides

import (
	"sync/atomic"
	"time"
)

// Entry is one owner-set value, typed as the registry's Validate returns it
// (bool, int, float64, string or []string), and where it came from (the
// database, the cache file, or the safe value of an invalid money key).
type Entry struct {
	Value  any
	Source string
}

// Snapshot is an immutable set of owner-set values.
type Snapshot struct {
	entries  map[string]Entry
	loadedAt time.Time
}

// NewSnapshot copies entries into a new snapshot.
func NewSnapshot(entries map[string]Entry, loadedAt time.Time) *Snapshot {
	copied := make(map[string]Entry, len(entries))

	for key, entry := range entries {
		entry.Value = cloneValue(entry.Value)
		copied[key] = entry
	}

	result := &Snapshot{entries: copied, loadedAt: loadedAt}

	return result
}

// Entry returns the owner-set value of key.
func (s *Snapshot) Entry(key string) (Entry, bool) {
	result := Entry{}
	isSet := false

	if s != nil {
		result, isSet = s.entries[key]
	}

	result.Value = cloneValue(result.Value)

	return result, isSet
}

// Entries returns a copy of every owner-set value.
func (s *Snapshot) Entries() map[string]Entry {
	result := map[string]Entry{}

	if s != nil {
		for key, entry := range s.entries {
			entry.Value = cloneValue(entry.Value)
			result[key] = entry
		}
	}

	return result
}

// LoadedAt is when the values were read from their source.
func (s *Snapshot) LoadedAt() time.Time {
	result := time.Time{}

	if s != nil {
		result = s.loadedAt
	}

	return result
}

// Len is the number of owner-set values.
func (s *Snapshot) Len() int {
	result := 0

	if s != nil {
		result = len(s.entries)
	}

	return result
}

// cloneValue copies list values, so a caller can not change a snapshot.
func cloneValue(value any) any {
	result := value
	list, isList := value.([]string)

	if isList == true {
		result = append([]string(nil), list...)
	}

	return result
}

// current is the snapshot the getters read; nil until the bot loads one.
var current atomic.Pointer[Snapshot]

// Set replaces the snapshot the getters read.
func Set(snapshot *Snapshot) {
	current.Store(snapshot)
}

// Current returns the snapshot the getters read (nil before the first load).
func Current() *Snapshot {
	result := current.Load()

	return result
}

// Lookup returns the owner-set value of key from the current snapshot.
func Lookup(key string) (Entry, bool) {
	snapshot := current.Load()
	result, isSet := snapshot.Entry(key)

	return result, isSet
}
