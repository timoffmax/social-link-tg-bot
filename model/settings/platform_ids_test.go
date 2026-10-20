package settings

import "testing"

func TestPlatformIDsFollowPlatforms(t *testing.T) {
	ids := PlatformIDs()
	all := Platforms()

	if len(ids) != len(all) {
		t.Fatalf("got %d ids, want %d", len(ids), len(all))
	}

	for i, platform := range all {
		if ids[i] != platform.ID {
			t.Fatalf("id %d: got %q, want %q", i, ids[i], platform.ID)
		}
	}
}
