package services

import (
	"database/sql"
	"os"
	"testing"
)

func TestAnyDailyLimitIsInForceWhenOneOfTheLimitsIsAboveZero(t *testing.T) {
	cases := []struct {
		name               string
		noIndividualLimit  int
		groupLimit         int
		hasIndividualLimit bool
		expected           bool
	}{
		{"no limit anywhere", 0, 0, false, false},
		{"default limit", 30, 0, false, true},
		{"group limit", 0, 10, false, true},
		{"individual limit only", 0, 0, true, true},
		{"all limits", 30, 10, true, true},
	}

	for _, item := range cases {
		if isAnyDailyLimitInForce(item.noIndividualLimit, item.groupLimit, item.hasIndividualLimit) != item.expected {
			t.Fatalf("%s: expected %t", item.name, item.expected)
		}
	}
}

func TestNoDailyLimitIsInForceWithoutADatabase(t *testing.T) {
	previous := dbService
	dbService = nil
	t.Cleanup(func() { dbService = previous })
	t.Setenv("DEFAULT_DAILY_MEDIA_LIMIT", "30")

	isInForce, err := IsAnyDailyLimitInForce(-100)
	limit, _, limitErr := GetNoIndividualLimit(-100)

	if (isInForce == true) || (err != nil) || (limit != 0) || (limitErr != nil) {
		t.Fatalf("expected no limit without a database, got %t %d (%v %v)", isInForce, limit, err, limitErr)
	}
}

// dailyLimitTestMigrations create the limit tables.
var dailyLimitTestMigrations = []string{
	"../migrations/001_create_media_stats.up.sql",
	"../migrations/002_create_group_limits.up.sql",
}

// setupDailyLimitTestDB recreates the limit tables in the database named by
// GATED_FETCH_TEST_DSN and points the services at it; skipped without it.
func setupDailyLimitTestDB(t *testing.T) *sql.DB {
	dsn := os.Getenv(gatedFetchTestDSNKey)

	if dsn == "" {
		t.Skipf("%s not set", gatedFetchTestDSNKey)
	}

	db, err := sql.Open("postgres", dsn)

	if err != nil {
		t.Fatalf("open: %v", err)
	}

	mustExec(t, db, "DROP TABLE IF EXISTS media_stats, user_media_limits, group_media_limits CASCADE")

	for _, path := range dailyLimitTestMigrations {
		migration, readErr := os.ReadFile(path)

		if readErr != nil {
			t.Fatalf("read migration: %v", readErr)
		}

		mustExec(t, db, string(migration))
	}

	previous := dbService
	dbService = &DatabaseService{db: db}

	t.Cleanup(func() {
		dbService = previous
		db.Close()
	})

	return db
}

func TestDailyLimitInForceFollowsTheEnforcedLimits(t *testing.T) {
	setupDailyLimitTestDB(t)
	t.Setenv("DEFAULT_DAILY_MEDIA_LIMIT", "0")

	if isInForce, err := IsAnyDailyLimitInForce(-100); (isInForce == true) || (err != nil) {
		t.Fatalf("expected no limit in an empty chat, got %t (%v)", isInForce, err)
	}

	if err := SetUserMediaLimit(7, -100, 0); err != nil {
		t.Fatalf("set user limit: %v", err)
	}

	if isInForce, _ := IsAnyDailyLimitInForce(-100); isInForce == true {
		t.Fatalf("a removed individual limit must not count as a limit")
	}

	if err := SetUserMediaLimit(8, -100, 5); err != nil {
		t.Fatalf("set user limit: %v", err)
	}

	if isInForce, _ := IsAnyDailyLimitInForce(-100); isInForce == false {
		t.Fatalf("an individual limit above 0 must count")
	}

	if isInForce, _ := IsAnyDailyLimitInForce(-200); isInForce == true {
		t.Fatalf("another chat's individual limit must not count")
	}

	if err := SetGroupMediaLimit(-200, "group", 10); err != nil {
		t.Fatalf("set group limit: %v", err)
	}

	limit, isGroupLimit, err := GetNoIndividualLimit(-200)

	if (limit != 10) || (isGroupLimit == false) || (err != nil) {
		t.Fatalf("expected the group limit for users without their own, got %d %t (%v)", limit, isGroupLimit, err)
	}

	t.Setenv("DEFAULT_DAILY_MEDIA_LIMIT", "30")
	limit, isGroupLimit, err = GetNoIndividualLimit(-200)
	effective, _, effectiveErr := GetEffectiveUserLimit(99, -200)

	if (limit != 30) || (isGroupLimit == true) || (err != nil) || (effective != limit) || (effectiveErr != nil) {
		t.Fatalf("expected the default limit, as enforced, got %d %t / enforced %d (%v %v)", limit, isGroupLimit, effective, err, effectiveErr)
	}
}
