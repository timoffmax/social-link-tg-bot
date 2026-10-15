package app

import (
	"testing"
	"time"
)

func TestTTLStoreKeepsFirstAddUntilItExpires(t *testing.T) {
	store := newTTLStore(time.Hour)

	if (store.add("k", 1) == false) || (store.add("k", 2) == true) {
		t.Fatalf("expected only the first add to store the key")
	}

	value, isLive := store.get("k")

	if (isLive == false) || (value != 1) {
		t.Fatalf("expected the first value, got %v %t", value, isLive)
	}

	store.items["k"] = ttlStoreItem{value: 1, storedAt: time.Now().Add(-2 * time.Hour)}

	if _, isLive := store.get("k"); isLive == true {
		t.Fatalf("an expired entry must not be returned")
	}

	if store.add("k", 3) == false {
		t.Fatalf("an expired key can be added again")
	}
}

func TestTTLStorePrunesExpiredEntries(t *testing.T) {
	store := newTTLStore(time.Hour)
	store.items["old"] = ttlStoreItem{value: 1, storedAt: time.Now().Add(-2 * time.Hour)}

	store.add("new", 1)

	if _, exists := store.items["old"]; exists == true {
		t.Fatalf("expected the expired entry to be pruned")
	}
}
