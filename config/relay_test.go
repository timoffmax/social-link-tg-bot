package config

import (
	"slices"
	"testing"
)

func TestGetRelayURLAcceptsOnlyPlainLoopbackHTTP(t *testing.T) {
	cases := []struct {
		raw     string
		want    string
		wantErr bool
	}{
		{"", "", false},
		{"http://127.0.0.1:8791", "http://127.0.0.1:8791", false},
		{" http://127.0.0.1:8791/ ", "http://127.0.0.1:8791", false},
		{"http://127.0.0.2:8791", "http://127.0.0.2:8791", false},
		{"http://[::1]:8791", "http://[::1]:8791", false},
		{"http://localhost:8791", "", true},
		{"http://192.0.2.10:8791", "", true},
		{"https://127.0.0.1:8791", "", true},
		{"http://user:pass@127.0.0.1:8791", "", true},
		{"http://127.0.0.1:8791/v1", "", true},
		{"http://127.0.0.1:8791?x=1", "", true},
		{"http://127.0.0.1.example.com:8791", "", true},
		{"127.0.0.1:8791", "", true},
	}

	for _, testCase := range cases {
		t.Setenv("RELAY_URL", testCase.raw)

		got, err := GetRelayURL()

		if (got != testCase.want) || ((err != nil) != testCase.wantErr) {
			t.Errorf("%q: got %q, %v", testCase.raw, got, err)
		}
	}
}

func TestGetRelayPlatformsDefaultsToNone(t *testing.T) {
	t.Setenv("RELAY_PLATFORMS", "")

	if got := GetRelayPlatforms(); len(got) != 0 {
		t.Errorf("got %v", got)
	}

	t.Setenv("RELAY_PLATFORMS", " YouTube, ,tiktok ")

	if got := GetRelayPlatforms(); slices.Equal(got, []string{"youtube", "tiktok"}) == false {
		t.Errorf("got %v", got)
	}
}

func TestIsRelayEnabledForNeedsURLTokenFileAndPlatform(t *testing.T) {
	t.Setenv("RELAY_URL", "http://127.0.0.1:8791")
	t.Setenv("RELAY_TOKEN_FILE", "/tmp/relay.token")
	t.Setenv("RELAY_PLATFORMS", "youtube")

	if (IsRelayEnabledFor("youtube") == false) || (IsRelayEnabledFor("tiktok") == true) {
		t.Errorf("youtube must be on, tiktok off")
	}

	t.Setenv("RELAY_TOKEN_FILE", "")

	if IsRelayEnabledFor("youtube") == true {
		t.Errorf("no token file must turn the relay off")
	}
}

func TestIsRelayConfiguredNeedsAValidURLAndATokenFile(t *testing.T) {
	cases := []struct {
		url       string
		tokenFile string
		want      bool
	}{
		{"http://127.0.0.1:8791", "/tmp/relay.token", true},
		{"", "/tmp/relay.token", false},
		{"http://localhost:8791", "/tmp/relay.token", false},
		{"http://127.0.0.1:8791", "", false},
	}

	for _, testCase := range cases {
		t.Setenv("RELAY_URL", testCase.url)
		t.Setenv("RELAY_TOKEN_FILE", testCase.tokenFile)

		if got := IsRelayConfigured(); got != testCase.want {
			t.Errorf("%q, %q: got %v", testCase.url, testCase.tokenFile, got)
		}
	}
}

func TestGetRelayTimeoutSecondsReturnsRawAndClamped(t *testing.T) {
	cases := map[string]RelayTimeoutSeconds{
		"":    {Raw: 45, Clamped: 45},
		"0":   {Raw: 45, Clamped: 45},
		"-3":  {Raw: 45, Clamped: 45},
		"abc": {Raw: 45, Clamped: 45},
		"1":   {Raw: 1, Clamped: 15},
		"14":  {Raw: 14, Clamped: 15},
		"15":  {Raw: 15, Clamped: 15},
		"16":  {Raw: 16, Clamped: 16},
		"30":  {Raw: 30, Clamped: 30},
	}

	for raw, want := range cases {
		t.Setenv("RELAY_TIMEOUT_SECONDS", raw)

		if got := GetRelayTimeoutSeconds(); got != want {
			t.Errorf("%q: got %+v, want %+v", raw, got, want)
		}
	}
}

func TestRelayTimeoutIsRaisedOnlyForValuesBelow15(t *testing.T) {
	for raw, want := range map[string]bool{"": false, "0": false, "-3": false, "abc": false, "12": true, "14": true, "15": false, "45": false} {
		t.Setenv("RELAY_TIMEOUT_SECONDS", raw)

		timeout := GetRelayTimeoutSeconds()

		if got := timeout.IsRaised(); got != want {
			t.Errorf("%q: got %v, want %v", raw, got, want)
		}
	}
}
