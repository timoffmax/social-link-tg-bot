package dashboard

import (
	"database/sql"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestCheckListenAddress(t *testing.T) {
	cases := []struct {
		addr        string
		allowPublic bool
		wantPublic  bool
		wantErr     bool
	}{
		{"127.0.0.1:8090", false, false, false},
		{"127.0.0.2:8090", false, false, false},
		{"localhost:8090", false, false, false},
		{"LOCALHOST:8090", false, false, false},
		{"[::1]:8090", false, false, false},
		{"0.0.0.0:8090", false, true, true},
		{":8090", false, true, true},
		{"192.0.2.10:8090", false, true, true},
		{"[::]:8090", false, true, true},
		{"example.com:8090", false, true, true},
		{"192.0.2.10:8090", true, false, false},
		{":8090", true, false, false},
		{"no-port", false, false, true},
	}

	for _, tc := range cases {
		err := CheckListenAddress(tc.addr, tc.allowPublic)

		if (err != nil) != tc.wantErr {
			t.Errorf("%s allow=%v: err = %v", tc.addr, tc.allowPublic, err)
		}

		if errors.Is(err, ErrPublicAddress) != tc.wantPublic {
			t.Errorf("%s allow=%v: public error = %v", tc.addr, tc.allowPublic, err)
		}
	}
}

func envMap(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

func TestLoadConfigDefaults(t *testing.T) {
	config, err := LoadConfig(envMap(map[string]string{}))

	if err != nil {
		t.Fatalf("err = %v", err)
	}

	if (config.Addr != DefaultAddr) || (config.DatabaseURL != DefaultDatabaseURL) || (config.Timezone != DefaultTimezone) {
		t.Errorf("config = %+v", config)
	}

	if config.Location.String() != DefaultTimezone {
		t.Errorf("location = %v", config.Location)
	}
}

func TestLoadConfigRefusesPublicAddressUnlessAllowed(t *testing.T) {
	_, err := LoadConfig(envMap(map[string]string{EnvAddr: "0.0.0.0:8090"}))

	if errors.Is(err, ErrPublicAddress) == false {
		t.Errorf("err = %v", err)
	}

	_, err = LoadConfig(envMap(map[string]string{EnvAddr: "0.0.0.0:8090", EnvAllowPublic: "yes"}))

	if errors.Is(err, ErrPublicAddress) == false {
		t.Errorf("non-true allow value: err = %v", err)
	}

	config, err := LoadConfig(envMap(map[string]string{EnvAddr: "0.0.0.0:8090", EnvAllowPublic: "true"}))

	if (err != nil) || (config.Addr != "0.0.0.0:8090") {
		t.Errorf("allowed: config = %+v, err = %v", config, err)
	}
}

func TestLoadConfigRejectsUnknownTimezone(t *testing.T) {
	_, err := LoadConfig(envMap(map[string]string{EnvTimezone: "Mars/Olympus"}))

	if err == nil {
		t.Errorf("unknown timezone accepted")
	}
}

func TestLoadConfigRejectsLocalTimezone(t *testing.T) {
	_, err := LoadConfig(envMap(map[string]string{EnvTimezone: "Local"}))

	if errors.Is(err, ErrLocalTimezone) == false {
		t.Errorf("err = %v, want ErrLocalTimezone", err)
	}
}

func TestIntParamParse(t *testing.T) {
	param := intParam{name: "days", fallback: 90, min: 1, max: 365}
	cases := []struct {
		raw     string
		want    int
		wantErr bool
	}{
		{"", 90, false},
		{" ", 90, false},
		{"30", 30, false},
		{" 30 ", 30, false},
		{"0", 1, false},
		{"-7", 1, false},
		{"365", 365, false},
		{"366", 365, false},
		{"abc", 90, true},
		{"7d", 90, true},
	}

	for _, tc := range cases {
		values := url.Values{}

		if tc.raw != "" {
			values.Set("days", tc.raw)
		}

		got, err := param.parse(values)

		if (got != tc.want) || ((err != nil) != tc.wantErr) {
			t.Errorf("%q: got %d, err %v", tc.raw, got, err)
		}
	}
}

func TestFirstError(t *testing.T) {
	first := errors.New("first")
	second := errors.New("second")

	if firstError(nil, first, second) != first {
		t.Errorf("first error not returned")
	}

	if firstError(nil, nil) != nil {
		t.Errorf("nil expected")
	}
}

func TestHealthStatus(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	recent := sql.NullTime{Time: now.Add(-time.Hour), Valid: true}
	old := sql.NullTime{Time: now.Add(-49 * time.Hour), Valid: true}
	cases := []struct {
		name        string
		lastAt      sql.NullTime
		avgPrevious float64
		avgRecent   float64
		want        string
	}{
		{"steady", recent, 5, 4.5, "ok"},
		{"quiet platform never stale", old, 0.5, 0, "ok"},
		{"stale", old, 1, 0, "stale"},
		{"stale wins over drop", old, 10, 1, "stale"},
		{"drop", recent, 10, 4.9, "drop"},
		{"exactly half is no drop", recent, 10, 5, "ok"},
		{"quiet platform never drops", recent, 0.9, 0, "ok"},
		{"no downloads at all", sql.NullTime{}, 2, 0, "stale"},
	}

	for _, tc := range cases {
		got := healthStatus(tc.lastAt, tc.avgPrevious, tc.avgRecent, now)

		if got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.name, got, tc.want)
		}
	}
}

func TestBuildPlatformHealthChange(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	lastAt := sql.NullTime{Time: now.Add(-time.Hour), Valid: true}
	withPrevious := buildPlatformHealth(healthRow{platform: "youtube", lastAt: lastAt, recent: 3, previous: 56, reference: now})

	if (withPrevious.ChangePct == nil) || (*withPrevious.ChangePct != -78.6) || (withPrevious.AvgDaily7d != 0.43) {
		t.Errorf("got %+v", withPrevious)
	}

	withoutPrevious := buildPlatformHealth(healthRow{platform: "tiktok", lastAt: lastAt, recent: 1, reference: now})

	if withoutPrevious.ChangePct != nil {
		t.Errorf("change_pct = %v, want nil", *withoutPrevious.ChangePct)
	}
}

func TestBuildPlatformHealthSilentPlatformStaysStale(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

	for _, silentDays := range []int{10, 40} {
		lastAt := sql.NullTime{Time: now.Add(-time.Duration(silentDays) * day), Valid: true}
		got := buildPlatformHealth(healthRow{platform: "reddit", lastAt: lastAt, previous: 28, reference: now})

		if (got.Status != "stale") || (got.AvgDailyPrev28d != 1) || (got.LastDownloadAt == nil) || (got.ChangePct == nil) || (*got.ChangePct != -100) {
			t.Errorf("silent %d days: got %+v", silentDays, got)
		}
	}
}

func TestLoadConfigAllowedHosts(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want []string
	}{
		{"default listen address", map[string]string{}, []string{"localhost:8090", "127.0.0.1:8090", "[::1]:8090"}},
		{"vpn listen address", map[string]string{EnvAddr: "10.0.0.1:8091", EnvAllowPublic: "true"}, []string{"localhost:8091", "127.0.0.1:8091", "[::1]:8091", "10.0.0.1:8091"}},
		{"all interfaces adds no host", map[string]string{EnvAddr: "0.0.0.0:8090", EnvAllowPublic: "true"}, []string{"localhost:8090", "127.0.0.1:8090", "[::1]:8090"}},
		{"port 80 adds bare hosts", map[string]string{EnvAddr: "10.0.0.1:80", EnvAllowPublic: "true"}, []string{"localhost:80", "127.0.0.1:80", "[::1]:80", "10.0.0.1:80", "localhost", "127.0.0.1", "[::1]", "10.0.0.1"}},
		{"port 443 adds bare hosts", map[string]string{EnvAddr: "127.0.0.1:443"}, []string{"localhost:443", "127.0.0.1:443", "[::1]:443", "localhost", "127.0.0.1", "[::1]"}},
		{"configured list", map[string]string{EnvAllowedHosts: " Stats.Example.com , ,127.0.0.1:8090"}, []string{"stats.example.com", "127.0.0.1:8090"}},
	}

	for _, tc := range cases {
		config, err := LoadConfig(envMap(tc.env))

		if err != nil {
			t.Fatalf("%s: err = %v", tc.name, err)
		}

		if strings.Join(config.AllowedHosts, " ") != strings.Join(tc.want, " ") {
			t.Errorf("%s: hosts = %v, want %v", tc.name, config.AllowedHosts, tc.want)
		}
	}
}
