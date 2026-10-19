package settings

import (
	"encoding/json"
	"testing"
)

func TestParseSpecialLimitKeyAcceptsPersonAndGroup(t *testing.T) {
	cases := map[string]SpecialLimitTarget{
		"user:7":              {Scope: SpecialLimitScopeUser, ID: 7},
		"user:123456789":      {Scope: SpecialLimitScopeUser, ID: 123456789},
		"chat:-1001234567890": {Scope: SpecialLimitScopeChat, ID: -1001234567890},
		"chat:-5":             {Scope: SpecialLimitScopeChat, ID: -5},
	}

	for key, want := range cases {
		got, err := ParseSpecialLimitKey(key)

		if (err != nil) || (got != want) {
			t.Fatalf("%s: got %+v, %v; want %+v", key, got, err, want)
		}
	}
}

func TestParseSpecialLimitKeyRefusesBadKeys(t *testing.T) {
	keys := []string{
		"user:-1", "chat:5", "foo:1", "user:", "user:1:2", "user:05", "user:+5",
		"user:99999999999999999999", "gated.user:1", "user", "", "chat:-0", "user:0", "USER:1",
	}

	for _, key := range keys {
		got, err := ParseSpecialLimitKey(key)

		if (err == nil) || (got != (SpecialLimitTarget{})) {
			t.Fatalf("%q: got %+v, %v; want an error", key, got, err)
		}
	}
}

func TestValidateSpecialLimitAcceptsZeroToCeilingAndReset(t *testing.T) {
	for _, raw := range []string{"0", "1", "30", "1000"} {
		got, err := ValidateSpecialLimit(json.RawMessage(raw))

		if (err != nil) || (got == nil) {
			t.Fatalf("%s: got %v, %v", raw, got, err)
		}
	}

	got, err := ValidateSpecialLimit(nil)

	if (err != nil) || (got != nil) {
		t.Fatalf("a reset must give nil: %v, %v", got, err)
	}
}

func TestValidateSpecialLimitRefusesOutOfRangeAndNonIntegers(t *testing.T) {
	for _, raw := range []string{"-2", "1001", "10.5", `"10"`, "true", "null", "1e2", "{}", "1 2"} {
		got, err := ValidateSpecialLimit(json.RawMessage(raw))

		if (err == nil) || (got != nil) {
			t.Fatalf("%s: got %v, %v; want an error", raw, got, err)
		}
	}
}

func TestValidateSpecialLimitAcceptsUnlimited(t *testing.T) {
	got, err := ValidateSpecialLimit(json.RawMessage("-1"))

	if (err != nil) || (got == nil) || (*got != SpecialLimitUnlimited) {
		t.Fatalf("got %v, %v", got, err)
	}

	if SpecialLimits().Unlimited == false {
		t.Fatalf("the published rules must allow unlimited")
	}
}

func TestParseStoredSpecialLimitAcceptsUnlimitedAndAboveCeiling(t *testing.T) {
	cases := map[string]int64{"-1": -1, "1500": 1500, "0": 0, "20": 20}

	for raw, want := range cases {
		got, err := ParseStoredSpecialLimit(json.RawMessage(raw))

		if (err != nil) || (got == nil) || (*got != want) {
			t.Fatalf("%s: got %v, %v", raw, got, err)
		}
	}

	for _, raw := range []json.RawMessage{nil, json.RawMessage("null")} {
		got, err := ParseStoredSpecialLimit(raw)

		if (err != nil) || (got != nil) {
			t.Fatalf("%s: got %v, %v; want nil", raw, got, err)
		}
	}

	for _, raw := range []string{`"x"`, "1.5", "true"} {
		if _, err := ParseStoredSpecialLimit(json.RawMessage(raw)); err == nil {
			t.Fatalf("%s: want an error", raw)
		}
	}
}

func TestSpecialLimitMaxIsTheQuotaCeiling(t *testing.T) {
	rules := SpecialLimits()

	if (rules.Max != int64(CeilingGatedMonthlyQuota)) || (rules.Min != 0) || (rules.Kind != KindSpecialLimit) {
		t.Fatalf("unexpected rules %+v", rules)
	}
}
