package dashboard

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"
)

// testDatabaseEnv names the throwaway database the integration tests use.
// Each test runs in its own schema, dropped afterwards.
const testDatabaseEnv = "DASHBOARD_TEST_DATABASE_URL"

const kyiv = "Europe/Kyiv"

// integrationNow is a Thursday; Kyiv is UTC+3 (summer time) on that day, so
// local today is 2026-10-15 (15:00).
var integrationNow = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

type integrationDB struct {
	db  *sql.DB
	url string
}

func openIntegrationDB(t *testing.T) integrationDB {
	t.Helper()
	baseURL := os.Getenv(testDatabaseEnv)

	if baseURL == "" {
		t.Skipf("%s is not set", testDatabaseEnv)
	}

	admin, err := sql.Open("postgres", baseURL)

	if err != nil {
		t.Fatalf("open: %v", err)
	}

	schema := fmt.Sprintf("dashboard_test_%d", time.Now().UnixNano())

	if _, err := admin.Exec(fmt.Sprintf("CREATE SCHEMA %s", schema)); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	t.Cleanup(func() {
		if _, err := admin.Exec(fmt.Sprintf("DROP SCHEMA %s CASCADE", schema)); err != nil {
			t.Errorf("drop schema: %v", err)
		}

		admin.Close()
	})

	parsed, err := url.Parse(baseURL)

	if err != nil {
		t.Fatalf("%s must be a postgres:// URL: %v", testDatabaseEnv, err)
	}

	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	db, err := sql.Open("postgres", parsed.String())

	if err != nil {
		t.Fatalf("open schema: %v", err)
	}

	t.Cleanup(func() { db.Close() })
	applyMigrations(t, db)

	return integrationDB{db: db, url: parsed.String()}
}

func applyMigrations(t *testing.T, db *sql.DB) {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("..", "migrations", "*.up.sql"))

	if (err != nil) || (len(files) == 0) {
		t.Fatalf("no migrations found: %v", err)
	}

	sort.Strings(files)

	for _, file := range files {
		content, err := os.ReadFile(file)

		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}

		if _, err := db.Exec(string(content)); err != nil {
			t.Fatalf("apply %s: %v", file, err)
		}
	}
}

func (d integrationDB) exec(t *testing.T, query string, args ...interface{}) {
	t.Helper()

	if _, err := d.db.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

func (d integrationDB) store(timezone string) *PGStore {
	store := NewPGStore(d.db, timezone)
	store.now = func() time.Time { return integrationNow }

	return store
}

// media is one media_stats fixture row; empty strings are stored as given,
// nil pointers as NULL.
type media struct {
	userID    int64
	username  *string
	chatID    int64
	title     *string
	platform  string
	mediaType string
	at        time.Time
}

func text(value string) *string {
	return &value
}

func (d integrationDB) insertMedia(t *testing.T, rows ...media) {
	t.Helper()

	for _, row := range rows {
		mediaType := row.mediaType

		if mediaType == "" {
			mediaType = "video"
		}

		d.exec(t, `INSERT INTO media_stats (user_id, username, chat_id, chat_title, platform, media_type, url, created_at)
VALUES ($1, $2, $3, $4, $5, $6, 'https://example.com/post', $7)`,
			row.userID, row.username, row.chatID, row.title, row.platform, mediaType, row.at)
	}
}

func utc(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339, value)

	if err != nil {
		panic(err)
	}

	return parsed
}

func ago(duration time.Duration) time.Time {
	return integrationNow.Add(-duration)
}

const day = 24 * time.Hour

func assertEqual(t *testing.T, name string, got interface{}, want interface{}) {
	t.Helper()

	if reflect.DeepEqual(got, want) == false {
		t.Errorf("%s:\n got  %+v\n want %+v", name, got, want)
	}
}

func zeroDays(from string, count int) []DailySplit {
	start, _ := time.Parse("2006-01-02", from)
	result := []DailySplit{}

	for i := 0; i < count; i++ {
		result = append(result, DailySplit{Day: start.AddDate(0, 0, i).Format("2006-01-02")})
	}

	return result
}

func overviewFixtures(t *testing.T, d integrationDB) {
	d.insertMedia(t,
		// 00:30 in Kyiv on 2026-10-14, still 2026-10-13 in UTC.
		media{userID: 1, username: text("alice"), chatID: 1, platform: "tiktok", at: utc("2026-10-13T21:30:00Z")},
		// 23:30 in Kyiv on 2026-10-13.
		media{userID: 1, username: text(""), chatID: 1, platform: "tiktok", at: utc("2026-10-13T20:30:00Z")},
		media{userID: 2, username: text("bob"), chatID: -100, title: text("Group A"), platform: "twitter", mediaType: "photo", at: utc("2026-10-15T09:00:00Z")},
		media{userID: 3, username: text("carol"), chatID: -100, title: text(""), platform: "tiktok", at: utc("2026-10-15T10:00:00Z")},
		media{userID: 5, chatID: -200, platform: "reddit", at: utc("2026-09-20T10:00:00Z")},
		media{userID: 4, chatID: 4, platform: "instagram", mediaType: "photo", at: utc("2026-05-01T10:00:00Z")},
	)
}

func TestIntegrationOverview(t *testing.T) {
	d := openIntegrationDB(t)
	overviewFixtures(t, d)
	got, err := d.store(kyiv).Overview(context.Background(), 7)

	if err != nil {
		t.Fatalf("overview: %v", err)
	}

	assertEqual(t, "totals", got.Totals, OverviewTotals{
		DownloadsAll:    6,
		DownloadsRange:  4,
		Downloads7d:     4,
		Downloads30d:    5,
		ActiveUsers7d:   3,
		ActiveUsers30d:  4,
		ActiveChats7d:   2,
		ActiveChats30d:  3,
		GroupsAll:       2,
		PrivateUsersAll: 2,
	})
	wantDaily := zeroDays("2026-10-09", 7)
	wantDaily[4].Private = 1
	wantDaily[5].Private = 1
	wantDaily[6].Group = 2
	assertEqual(t, "daily (Kyiv buckets, gap-filled)", got.Daily, wantDaily)
	assertEqual(t, "platform mix", got.PlatformMix, []PlatformCount{{"tiktok", 3}, {"twitter", 1}})
	assertEqual(t, "new chats weekly", got.NewChatsWeekly, []WeeklyNewChats{
		{Week: "2026-10-05"},
		{Week: "2026-10-12", Groups: 1, Private: 1},
	})
	assertEqual(t, "days", got.Days, 7)
}

func TestIntegrationOverviewBucketsByTheConfiguredTimezone(t *testing.T) {
	d := openIntegrationDB(t)
	overviewFixtures(t, d)
	got, err := d.store("UTC").Overview(context.Background(), 7)

	if err != nil {
		t.Fatalf("overview: %v", err)
	}

	wantDaily := zeroDays("2026-10-09", 7)
	wantDaily[4].Private = 2
	wantDaily[6].Group = 2
	assertEqual(t, "daily (UTC buckets)", got.Daily, wantDaily)
}

func TestIntegrationOverviewLongRangeIsGapFilled(t *testing.T) {
	d := openIntegrationDB(t)
	overviewFixtures(t, d)
	got, err := d.store(kyiv).Overview(context.Background(), 90)

	if err != nil {
		t.Fatalf("overview: %v", err)
	}

	if (len(got.Daily) != 90) || (got.Daily[0].Day != "2026-07-18") || (got.Daily[89].Day != "2026-10-15") {
		t.Errorf("daily = %d days from %s to %s", len(got.Daily), got.Daily[0].Day, got.Daily[len(got.Daily)-1].Day)
	}

	if got.Totals.DownloadsRange != 5 {
		t.Errorf("downloads_range = %d", got.Totals.DownloadsRange)
	}

	if (len(got.NewChatsWeekly) != 14) || (got.NewChatsWeekly[0].Week != "2026-07-13") {
		t.Errorf("new chats weekly = %+v", got.NewChatsWeekly)
	}
}

func TestIntegrationEmptyDatabase(t *testing.T) {
	d := openIntegrationDB(t)
	store := d.store(kyiv)
	ctx := context.Background()
	overview, err := store.Overview(ctx, 3)

	if err != nil {
		t.Fatalf("overview: %v", err)
	}

	assertEqual(t, "empty daily", overview.Daily, zeroDays("2026-10-13", 3))
	assertEqual(t, "empty totals", overview.Totals, OverviewTotals{})
	platforms, err := store.Platforms(ctx, 2)

	if err != nil {
		t.Fatalf("platforms: %v", err)
	}

	assertEqual(t, "empty platform days", platforms.Daily, []PlatformDay{
		{Day: "2026-10-14", Counts: map[string]int64{}},
		{Day: "2026-10-15", Counts: map[string]int64{}},
	})

	if _, err := store.Chats(ctx, 30, 20); err != nil {
		t.Errorf("chats: %v", err)
	}

	if _, err := store.Money(ctx, 12); err != nil {
		t.Errorf("money: %v", err)
	}

	if _, err := store.Reports(ctx, 50, 26, ReportStatusAll); err != nil {
		t.Errorf("reports: %v", err)
	}
}

func TestIntegrationPlatforms(t *testing.T) {
	d := openIntegrationDB(t)

	for k := 7; k <= 34; k++ {
		at := ago(time.Duration(k)*day + time.Hour)
		d.insertMedia(t,
			media{userID: 1, chatID: 1, platform: "facebook", at: at},
			media{userID: 1, chatID: 1, platform: "youtube", at: at},
			media{userID: 1, chatID: 1, platform: "youtube", at: at},
			media{userID: 1, chatID: 1, platform: "9gag", mediaType: "photo", at: at},
		)
	}

	for j := 0; j <= 6; j++ {
		d.insertMedia(t, media{userID: 1, chatID: 1, platform: "9gag", mediaType: "photo", at: ago(time.Duration(j)*day + time.Hour)})
	}

	for h := 1; h <= 3; h++ {
		d.insertMedia(t, media{userID: 1, chatID: 1, platform: "youtube", at: ago(time.Duration(h) * time.Hour)})
	}

	// Silent for 10 and for 40 days after 28 busy days: both stay stale.
	for k := 0; k < 28; k++ {
		d.insertMedia(t,
			media{userID: 1, chatID: 1, platform: "reddit", at: ago(time.Duration(10+k)*day + time.Hour)},
			media{userID: 1, chatID: 1, platform: "twitter", at: ago(time.Duration(40+k)*day + time.Hour)},
		)
	}

	d.insertMedia(t,
		media{userID: 1, chatID: 1, platform: "tiktok", at: ago(time.Hour)},
		// One download 40 days ago: listed, but too quiet to be stale.
		media{userID: 1, chatID: 1, platform: "unknown", at: ago(40 * day)},
		// Older than the 90-day health listing.
		media{userID: 1, chatID: 1, platform: "threads", at: ago(100 * day)},
	)
	got, err := d.store(kyiv).Platforms(context.Background(), 7)

	if err != nil {
		t.Fatalf("platforms: %v", err)
	}

	assertEqual(t, "platforms", got.Platforms, []string{"9gag", "youtube", "tiktok"})

	if len(got.Daily) != 7 {
		t.Fatalf("daily has %d days", len(got.Daily))
	}

	assertEqual(t, "first day", got.Daily[0], PlatformDay{Day: "2026-10-09", Counts: map[string]int64{"9gag": 1, "youtube": 0, "tiktok": 0}})
	assertEqual(t, "today", got.Daily[6], PlatformDay{Day: "2026-10-15", Counts: map[string]int64{"9gag": 1, "youtube": 3, "tiktok": 1}})
	assertEqual(t, "media types", got.MediaTypes, []MediaTypeSplit{
		{Platform: "9gag", Photo: 7},
		{Platform: "youtube", Video: 3},
		{Platform: "tiktok", Video: 1},
	})
	youtubeChange := -78.6
	gagChange := 0.0
	silentChange := -100.0
	assertEqual(t, "health", got.Health, []PlatformHealth{
		{Platform: "youtube", LastDownloadAt: text("2026-10-15T11:00:00Z"), Downloads24h: 3, Downloads7d: 3, AvgDailyPrev28d: 2, AvgDaily7d: 0.43, ChangePct: &youtubeChange, Status: "drop"},
		{Platform: "9gag", LastDownloadAt: text("2026-10-15T11:00:00Z"), Downloads24h: 1, Downloads7d: 7, AvgDailyPrev28d: 1, AvgDaily7d: 1, ChangePct: &gagChange, Status: "ok"},
		{Platform: "facebook", LastDownloadAt: text("2026-10-08T11:00:00Z"), Downloads24h: 0, Downloads7d: 0, AvgDailyPrev28d: 1, AvgDaily7d: 0, ChangePct: &silentChange, Status: "stale"},
		{Platform: "reddit", LastDownloadAt: text("2026-10-05T11:00:00Z"), Downloads24h: 0, Downloads7d: 0, AvgDailyPrev28d: 1, AvgDaily7d: 0, ChangePct: &silentChange, Status: "stale"},
		{Platform: "twitter", LastDownloadAt: text("2026-09-05T11:00:00Z"), Downloads24h: 0, Downloads7d: 0, AvgDailyPrev28d: 1, AvgDaily7d: 0, ChangePct: &silentChange, Status: "stale"},
		{Platform: "tiktok", LastDownloadAt: text("2026-10-15T11:00:00Z"), Downloads24h: 1, Downloads7d: 1, AvgDailyPrev28d: 0, AvgDaily7d: 0.14, ChangePct: nil, Status: "ok"},
		{Platform: "unknown", LastDownloadAt: text("2026-09-05T12:00:00Z"), Downloads24h: 0, Downloads7d: 0, AvgDailyPrev28d: 0.04, AvgDaily7d: 0, ChangePct: &silentChange, Status: "ok"},
	})
}

func TestIntegrationPlatformsServices(t *testing.T) {
	d := openIntegrationDB(t)
	d.insertMedia(t, media{userID: 1, chatID: 1, platform: "tiktok", at: ago(time.Hour)})
	store := d.store(kyiv)
	ctx := context.Background()
	without, err := store.Platforms(ctx, 7)

	if (err != nil) || (without.Services != nil) {
		t.Fatalf("without the row: services = %+v, %v", without.Services, err)
	}

	data := `{"version": 1, "services": [{"key": "tikwm", "state": "idle"}]}`
	d.exec(t, `INSERT INTO bot_runtime_status (component, data, updated_at) VALUES ('services', $1, $2)`, data, integrationNow.Add(-heartbeatStaleAfter))
	fresh, err := store.Platforms(ctx, 7)

	if (err != nil) || (fresh.Services == nil) || (fresh.Services.IsStale == true) || (string(fresh.Services.Data) != data) {
		t.Fatalf("fresh: services = %+v, %v", fresh.Services, err)
	}

	assertEqual(t, "updated at", fresh.Services.UpdatedAt, "2026-10-15T11:57:00Z")
	fresh.Services = nil
	assertEqual(t, "rest unchanged", fresh, without)
	d.exec(t, `UPDATE bot_runtime_status SET updated_at = $1 WHERE component = 'services'`, integrationNow.Add(-heartbeatStaleAfter-time.Second))
	old, err := store.Platforms(ctx, 7)

	if (err != nil) || (old.Services == nil) || (old.Services.IsStale == false) {
		t.Errorf("old: services = %+v, %v", old.Services, err)
	}
}

func TestIntegrationPlatformsLoadWithoutTheStatusTable(t *testing.T) {
	d := openIntegrationDB(t)
	d.insertMedia(t, media{userID: 1, chatID: 1, platform: "tiktok", at: ago(time.Hour)})
	d.exec(t, `DROP TABLE bot_runtime_status CASCADE`)

	got, err := d.store(kyiv).Platforms(context.Background(), 7)

	if (err != nil) || (got.Services != nil) {
		t.Fatalf("services = %+v, %v", got.Services, err)
	}

	assertEqual(t, "platforms", got.Platforms, []string{"tiktok"})
}

func TestIntegrationChats(t *testing.T) {
	d := openIntegrationDB(t)
	d.insertMedia(t,
		media{userID: 2, username: text("bob"), chatID: -100, title: text("Old A"), platform: "tiktok", at: ago(5 * day)},
		media{userID: 2, username: text("bob"), chatID: -100, title: text("Group A"), platform: "tiktok", at: ago(2 * day)},
		media{userID: 3, username: text("carol"), chatID: -100, title: text(""), platform: "tiktok", at: ago(time.Hour)},
		media{userID: 2, chatID: -200, platform: "tiktok", at: ago(day)},
		media{userID: 6, chatID: -300, title: text("Group C"), platform: "tiktok", at: ago(40 * day)},
		media{userID: 1, username: text("alice"), chatID: 1, platform: "tiktok", at: ago(2 * day)},
		media{userID: 1, username: text(""), chatID: 1, platform: "tiktok", at: ago(time.Hour)},
		media{userID: 5, chatID: 5, platform: "tiktok", at: ago(day)},
		// Last month: outside the 30-day range.
		media{userID: 1, chatID: 1, platform: "tiktok", at: utc("2026-09-15T10:00:00Z")},
		media{userID: 7, chatID: 7, platform: "tiktok", at: utc("2026-09-10T10:00:00Z")},
		// 00:30 on 1 October in Kyiv, still September in UTC.
		media{userID: 8, chatID: 8, platform: "tiktok", at: utc("2026-09-30T21:30:00Z")},
	)
	d.exec(t, `INSERT INTO group_media_limits (chat_id, chat_title, default_daily_limit, timezone) VALUES
(-200, 'Group B', 0, 'UTC'), (-400, 'Group D', 5, 'Europe/Kyiv')`)
	d.exec(t, `INSERT INTO chat_settings (chat_id, setting_type, setting_key, setting_value, enabled) VALUES
(-100, 'platform', 'tiktok', 'false', true), (-100, 'platform', 'instagram', 'true', false), (-100, 'feature', 'x', '1', true),
(-600, 'platform', 'tiktok', 'true', true), (-600, 'description', 'tiktok', 'true', true), (-600, 'buttons', 'report', 'yes', true),
(-700, 'platform', 'tiktok', 'true', true), (-700, 'description', 'instagram', 'false', true), (-700, 'buttons', 'support', 'F', true), (-700, 'buttons', 'report', 'true', true)`)
	d.exec(t, `INSERT INTO user_media_limits (chat_id, user_id, daily_limit) VALUES (-100, 2, 3), (-100, 3, 4), (-500, 9, 1)`)
	got, err := d.store(kyiv).Chats(context.Background(), 30, 20)

	if err != nil {
		t.Fatalf("chats: %v", err)
	}

	assertEqual(t, "top groups", got.TopGroups, []TopGroup{
		{ChatID: -100, Title: "Group A", Downloads: 3, Users: 2, LastAt: text("2026-10-15T11:00:00Z")},
		{ChatID: -200, Title: "Group B", Downloads: 1, Users: 1, LastAt: text("2026-10-14T12:00:00Z")},
	})
	assertEqual(t, "top users", got.TopUsers, []TopUser{
		{UserID: 1, Username: "alice", Downloads: 2, LastAt: text("2026-10-15T11:00:00Z")},
		{UserID: 5, Username: "", Downloads: 1, LastAt: text("2026-10-14T12:00:00Z")},
		{UserID: 8, Username: "", Downloads: 1, LastAt: text("2026-09-30T21:30:00Z")},
	})
	assertEqual(t, "retention", got.Retention, Retention{Month: "2026-10", ActiveThisMonth: 5, ActiveLastMonth: 3, Retained: 1})
	assertEqual(t, "custom settings", got.CustomSettings, []CustomChat{
		{ChatID: -700, Title: "", DailyLimit: 0, Timezone: "UTC", UserLimits: 0, Settings: []ChatSetting{
			{Type: "buttons", Key: "support", Value: "F", Enabled: true},
			{Type: "description", Key: "instagram", Value: "false", Enabled: true},
		}},
		{ChatID: -500, Title: "", DailyLimit: 0, Timezone: "UTC", UserLimits: 1, Settings: []ChatSetting{}},
		{ChatID: -400, Title: "Group D", DailyLimit: 5, Timezone: "Europe/Kyiv", UserLimits: 0, Settings: []ChatSetting{}},
		{ChatID: -100, Title: "Group A", DailyLimit: 0, Timezone: "UTC", UserLimits: 2, Settings: []ChatSetting{
			{Type: "platform", Key: "instagram", Value: "true", Enabled: false},
		}},
	})
	limited, err := d.store(kyiv).Chats(context.Background(), 30, 1)

	if err != nil {
		t.Fatalf("chats limit 1: %v", err)
	}

	if (len(limited.TopGroups) != 1) || (len(limited.TopUsers) != 1) || (limited.TopUsers[0].UserID != 1) {
		t.Errorf("limit 1: groups %+v users %+v", limited.TopGroups, limited.TopUsers)
	}
}

func TestIntegrationMoney(t *testing.T) {
	d := openIntegrationDB(t)
	d.insertMedia(t,
		media{userID: 1, username: text("alice"), chatID: 1, platform: "instagram", at: ago(day)},
		media{userID: 2, chatID: -100, title: text("Group A"), platform: "instagram", at: ago(day)},
	)
	d.exec(t, `INSERT INTO group_media_limits (chat_id, chat_title) VALUES (-200, 'Group B')`)
	d.exec(t, `INSERT INTO gated_fetch_usage (scope, scope_id, period, used) VALUES
('global', 0, '2026-10-01', 12), ('chat', -100, '2026-10-01', 5), ('chat', -200, '2026-10-01', 3), ('user', 1, '2026-10-01', 3),
('global', 0, '2026-09-01', 4), ('chat', -100, '2026-09-01', 4)`)
	d.exec(t, `INSERT INTO gated_fetch_policies (scope, scope_id, enabled, monthly_limit) VALUES ('chat', -100, true, 20), ('chat', -200, true, NULL), ('user', 1, true, -1)`)
	d.exec(t, `INSERT INTO support_payments (telegram_payment_charge_id, user_id, chat_id, stars, credits, payload, created_at, refunded_at, boosted_chat_id) VALUES
('p1', 1, 1, 25, 10, 'support:25:1', '2026-10-05T10:00:00Z', NULL, NULL),
('p2', 2, -100, 50, 25, 'support:50:2', '2026-10-06T10:00:00Z', NULL, -100),
('p3', 3, 3, 100, 60, 'support:100:3', '2026-09-10T10:00:00Z', '2026-10-02T10:00:00Z', NULL),
('p4', 4, 4, 25, 10, 'support:25:4', '2026-09-30T21:30:00Z', NULL, NULL),
('p5', 5, 5, 50, 25, 'support:50:5', '2026-09-20T10:00:00Z', NULL, NULL)`)
	d.exec(t, `INSERT INTO gated_fetch_credits (user_id, credits) VALUES (1, 10), (2, 0)`)
	d.exec(t, `INSERT INTO gated_fetch_chat_credits (chat_id, credits) VALUES (-100, 25)`)
	got, err := d.store(kyiv).Money(context.Background(), 3)

	if err != nil {
		t.Fatalf("money: %v", err)
	}

	assertEqual(t, "gated monthly", got.GatedMonthly, []GatedMonth{
		{Month: "2026-08"},
		{Month: "2026-09", GlobalUsed: 4, ChatsUsed: 4},
		{Month: "2026-10", GlobalUsed: 12, ChatsUsed: 8, UsersUsed: 3},
	})
	assertEqual(t, "stars monthly", got.StarsMonthly, []StarsMonth{
		{Month: "2026-08"},
		{Month: "2026-09", Payments: 1, Stars: 50},
		{Month: "2026-10", Payments: 3, Stars: 100, Refunds: 1, RefundedStars: 100, Boosts: 1},
	})
	assertEqual(t, "credits", got.Credits, Credits{UsersWithCredits: 1, UserCredits: 10, ChatsWithCredits: 1, ChatCredits: 25})
}

func TestIntegrationReports(t *testing.T) {
	d := openIntegrationDB(t)
	d.exec(t, `INSERT INTO reports (id, user_id, username, first_name, chat_id, chat_title, message, resolved, created_at) VALUES
(1, 42, 'name', 'N', -1001, 'Group', 'text', false, '2026-10-15T11:00:00Z'),
(2, 43, NULL, NULL, 43, NULL, 'second', true, '2026-10-13T12:00:00Z'),
(3, 44, 'x', 'X', 44, NULL, 'monday in Kyiv', false, '2026-10-11T21:30:00Z'),
(4, 45, 'y', 'Y', 45, NULL, 'old week', true, '2026-10-06T10:00:00Z'),
(5, 46, 'z', 'Z', 46, NULL, 'long ago', false, '2026-09-01T10:00:00Z')`)
	got, err := d.store(kyiv).Reports(context.Background(), 2, 2, ReportStatusAll)

	if err != nil {
		t.Fatalf("reports: %v", err)
	}

	assertEqual(t, "totals", got.Totals, ReportTotals{Open: 3, Resolved: 2})
	assertEqual(t, "weekly", got.Weekly, []ReportWeek{
		{Week: "2026-10-05", Open: 0, Resolved: 1},
		{Week: "2026-10-12", Open: 2, Resolved: 1},
	})
	assertEqual(t, "latest", got.Latest, []Report{
		{ID: 1, CreatedAt: text("2026-10-15T11:00:00Z"), UserID: 42, Username: "name", FirstName: "N", ChatID: -1001, ChatTitle: "Group", Message: "text", Resolved: false, Replies: []ReportReply{}},
		{ID: 2, CreatedAt: text("2026-10-13T12:00:00Z"), UserID: 43, ChatID: 43, Message: "second", Resolved: true, Replies: []ReportReply{}},
	})
}

func TestIntegrationTransactionsAreReadOnly(t *testing.T) {
	d := openIntegrationDB(t)
	store := d.store(kyiv)
	err := store.readOnly(context.Background(), func(q readTx) error {
		_, execErr := q.tx.ExecContext(q.ctx, `INSERT INTO reports (user_id, chat_id, message) VALUES (1, 1, 'x')`)

		return execErr
	})

	if err == nil {
		t.Fatalf("write inside the dashboard transaction succeeded")
	}

	var timeout string
	err = store.readOnly(context.Background(), func(q readTx) error {
		return q.row(`SHOW statement_timeout`, []interface{}{&timeout})
	})

	if (err != nil) || (timeout != "5s") {
		t.Errorf("statement_timeout = %q, err = %v", timeout, err)
	}
}

func TestIntegrationHTTPEndToEnd(t *testing.T) {
	d := openIntegrationDB(t)
	overviewFixtures(t, d)
	server := newTestServer(d.store(kyiv))

	for _, target := range []string{"/healthz", "/api/overview?days=7", "/api/platforms", "/api/chats", "/api/money", "/api/reports"} {
		recorder := doRequest(server, http.MethodGet, target)

		if recorder.Code != http.StatusOK {
			t.Errorf("%s: status = %d body = %s", target, recorder.Code, recorder.Body.String())
		}
	}
}

// readOnlyRoleStore connects like the production role, whose
// default_transaction_read_only is on, so only explicit READ WRITE
// transactions can write.
func (d integrationDB) readOnlyRoleStore(t *testing.T) *PGStore {
	t.Helper()
	parsed, err := url.Parse(d.url)

	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	query := parsed.Query()
	query.Set("default_transaction_read_only", "on")
	parsed.RawQuery = query.Encode()
	db, err := sql.Open("postgres", parsed.String())

	if err != nil {
		t.Fatalf("open read-only role: %v", err)
	}

	t.Cleanup(func() { db.Close() })
	var readOnly string

	if err := db.QueryRow(`SHOW default_transaction_read_only`).Scan(&readOnly); (err != nil) || (readOnly != "on") {
		t.Fatalf("default_transaction_read_only = %q, err = %v", readOnly, err)
	}

	store := NewPGStore(db, kyiv)
	store.now = func() time.Time { return integrationNow }

	return store
}

func TestIntegrationBlogPostLifecycle(t *testing.T) {
	d := openIntegrationDB(t)
	store := d.readOnlyRoleStore(t)
	ctx := context.Background()
	created, err := store.CreateBlogPost(ctx, BlogDraft{Title: "News", Content: "<b>Hi</b> 😀"})

	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if (created.ID == 0) || (created.Published == true) || (created.Length != 5) || (created.UpdatedAt == nil) || (created.CreatedAt == nil) {
		t.Errorf("created = %+v", created)
	}

	loaded, err := store.BlogPost(ctx, created.ID)
	assertEqual(t, "loaded", loaded, created)

	if err != nil {
		t.Fatalf("get: %v", err)
	}

	loadedAt, _ := time.Parse(time.RFC3339Nano, *loaded.UpdatedAt)
	updated, err := store.UpdateBlogPost(ctx, created.ID, BlogDraft{Title: "News 2", Content: "<i>Edited</i>"}, &loadedAt)

	if (err != nil) || (updated.Title != "News 2") || (updated.Content != "<i>Edited</i>") || (updated.Length != 6) {
		t.Fatalf("update: %+v, %v", updated, err)
	}

	if *updated.UpdatedAt == *loaded.UpdatedAt {
		t.Errorf("updated_at did not change: %s", *updated.UpdatedAt)
	}

	_, err = store.UpdateBlogPost(ctx, created.ID, BlogDraft{Title: "Stale", Content: "stale"}, &loadedAt)

	if errors.Is(err, ErrBlogPostChanged) == false {
		t.Errorf("stale update: err = %v", err)
	}

	published, err := store.SetBlogPostPublished(ctx, created.ID, true)

	if (err != nil) || (published.Published == false) || (published.Content != "<i>Edited</i>") {
		t.Errorf("publish: %+v, %v", published, err)
	}

	again, err := store.SetBlogPostPublished(ctx, created.ID, true)

	if (err != nil) || (again.Published == false) {
		t.Errorf("publish twice keeps it published: %+v, %v", again, err)
	}

	posts, err := store.BlogPosts(ctx)

	if (err != nil) || (len(posts) != 1) || (posts[0].ID != created.ID) || (posts[0].Published == false) {
		t.Errorf("list: %+v, %v", posts, err)
	}

	if err := store.DeleteBlogPost(ctx, created.ID); err != nil {
		t.Errorf("delete: %v", err)
	}

	assertBlogPostMissing(t, store, created.ID, &loadedAt)
}

func assertBlogPostMissing(t *testing.T, store *PGStore, id int64, loadedAt *time.Time) {
	t.Helper()
	ctx := context.Background()
	_, getErr := store.BlogPost(ctx, id)
	_, updateErr := store.UpdateBlogPost(ctx, id, BlogDraft{Title: "t", Content: "c"}, loadedAt)
	_, publishErr := store.SetBlogPostPublished(ctx, id, false)
	deleteErr := store.DeleteBlogPost(ctx, id)

	for name, err := range map[string]error{"get": getErr, "update": updateErr, "publish": publishErr, "delete": deleteErr} {
		if errors.Is(err, ErrBlogPostNotFound) == false {
			t.Errorf("%s of a missing post: err = %v", name, err)
		}
	}
}

func TestIntegrationBlogPostsNewestFirst(t *testing.T) {
	d := openIntegrationDB(t)
	d.exec(t, `INSERT INTO blog_posts (id, title, content, published, created_at, updated_at) VALUES
(1, 'old', 'a &amp; b', true, '2026-10-01T10:00:00Z', '2026-10-01T10:00:00Z'),
(2, 'new', '1 < 2', false, '2026-10-10T10:00:00Z', '2026-10-12T10:00:00.5Z'),
(3, 'no dates', 'x', false, NULL, NULL)`)
	posts, err := d.store(kyiv).BlogPosts(context.Background())

	if err != nil {
		t.Fatalf("list: %v", err)
	}

	assertEqual(t, "posts", posts, []BlogPost{
		{ID: 2, Title: "new", Content: "1 < 2", CreatedAt: text("2026-10-10T10:00:00Z"), UpdatedAt: text("2026-10-12T10:00:00.5Z"), Length: 5},
		{ID: 1, Title: "old", Content: "a &amp; b", Published: true, CreatedAt: text("2026-10-01T10:00:00Z"), UpdatedAt: text("2026-10-01T10:00:00Z"), Length: 5},
		{ID: 3, Title: "no dates", Content: "x", Length: 1},
	})
}

func TestIntegrationBlogUpdateComparesMicroseconds(t *testing.T) {
	d := openIntegrationDB(t)
	d.exec(t, `INSERT INTO blog_posts (id, title, content, updated_at) VALUES (1, 't', 'c', '2026-10-15T12:00:00.123456Z')`)
	d.exec(t, `INSERT INTO blog_posts (id, title, content, updated_at) VALUES (2, 't', 'c', NULL)`)
	store := d.store(kyiv)
	ctx := context.Background()
	loaded, _ := store.BlogPost(ctx, 1)

	if (loaded.UpdatedAt == nil) || (*loaded.UpdatedAt != "2026-10-15T12:00:00.123456Z") {
		t.Fatalf("updated_at = %v", loaded.UpdatedAt)
	}

	seconds := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	milliseconds := time.Date(2026, 10, 15, 12, 0, 0, 123000000, time.UTC)

	for _, wrong := range []*time.Time{&seconds, &milliseconds, nil} {
		_, err := store.UpdateBlogPost(ctx, 1, BlogDraft{Title: "x", Content: "x"}, wrong)

		if errors.Is(err, ErrBlogPostChanged) == false {
			t.Errorf("loaded %v: err = %v", wrong, err)
		}
	}

	exact, _ := parseLoadedTime(*loaded.UpdatedAt)

	if _, err := store.UpdateBlogPost(ctx, 1, BlogDraft{Title: "x", Content: "x"}, exact); err != nil {
		t.Errorf("exact updated_at: %v", err)
	}

	if _, err := store.UpdateBlogPost(ctx, 2, BlogDraft{Title: "x", Content: "x"}, &seconds); errors.Is(err, ErrBlogPostChanged) == false {
		t.Errorf("NULL row with a timestamp: err = %v", err)
	}

	if _, err := store.UpdateBlogPost(ctx, 2, BlogDraft{Title: "y", Content: "y"}, nil); err != nil {
		t.Errorf("NULL row with null: %v", err)
	}
}

func TestIntegrationBlogKeepsAConcurrentTelegramEdit(t *testing.T) {
	d := openIntegrationDB(t)
	store := d.store(kyiv)
	ctx := context.Background()
	created, _ := store.CreateBlogPost(ctx, BlogDraft{Title: "t", Content: "dashboard"})
	loadedAt, _ := parseLoadedTime(*created.UpdatedAt)
	// The bot's owner panel: services.UpdateBlogPost and ToggleBlogPostPublished.
	d.exec(t, `UPDATE blog_posts SET title = $1, content = $2 WHERE id = $3`, "tg", "telegram edit", created.ID)
	d.exec(t, `UPDATE blog_posts SET published = NOT published WHERE id = $1`, created.ID)
	_, err := store.UpdateBlogPost(ctx, created.ID, BlogDraft{Title: "t", Content: "overwrite"}, loadedAt)

	if errors.Is(err, ErrBlogPostChanged) == false {
		t.Errorf("update over a Telegram edit: err = %v", err)
	}

	unpublished, err := store.SetBlogPostPublished(ctx, created.ID, false)

	if (err != nil) || (unpublished.Content != "telegram edit") || (unpublished.Title != "tg") || (unpublished.Published == true) {
		t.Errorf("publish toggle lost the Telegram edit: %+v, %v", unpublished, err)
	}
}

func TestIntegrationBlogWritesUseTheirOwnTransactionOnly(t *testing.T) {
	d := openIntegrationDB(t)
	store := d.readOnlyRoleStore(t)
	err := store.readOnly(context.Background(), func(q readTx) error {
		_, execErr := q.tx.ExecContext(q.ctx, `INSERT INTO blog_posts (title, content) VALUES ('x', 'y')`)

		return execErr
	})

	if (err == nil) || (strings.Contains(err.Error(), "read-only") == false) {
		t.Errorf("write in a read-only transaction: err = %v", err)
	}

	var timeout string
	var readOnly string
	err = store.readWrite(context.Background(), func(q writeTx) error {
		return q.row(`SELECT current_setting('statement_timeout'), current_setting('transaction_read_only')`, []interface{}{&timeout, &readOnly})
	})

	if (err != nil) || (timeout != "5s") || (readOnly != "off") {
		t.Errorf("write transaction: timeout = %q, read only = %q, err = %v", timeout, readOnly, err)
	}
}

func TestIntegrationBlogHTTPEndToEnd(t *testing.T) {
	d := openIntegrationDB(t)
	server := newTestServer(d.readOnlyRoleStore(t))
	created := blogRequest{method: http.MethodPost, target: "/api/blog-posts", body: `{"title":" Hello ","content":"<b>Hi</b>"}`}.do(server)

	if created.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}

	post := decodeBody(t, created)["post"].(map[string]interface{})
	id := int64(post["id"].(float64))
	updatedAt := post["updated_at"].(string)
	target := fmt.Sprintf("/api/blog-posts/%d", id)

	if (post["title"] != "Hello") || (post["published"] != false) || (post["length"] != float64(2)) {
		t.Errorf("created = %v", post)
	}

	body := fmt.Sprintf(`{"title":"Hello","content":"<i>Edited</i>","updated_at":%q}`, updatedAt)
	updated := blogRequest{method: http.MethodPut, target: target, body: body}.do(server)

	if updated.Code != http.StatusOK {
		t.Errorf("update: %d %s", updated.Code, updated.Body.String())
	}

	conflict := blogRequest{method: http.MethodPut, target: target, body: body}.do(server)

	if (conflict.Code != http.StatusConflict) || (decodeBody(t, conflict)["error"] != "changed elsewhere") {
		t.Errorf("stale update: %d %s", conflict.Code, conflict.Body.String())
	}

	published := blogRequest{method: http.MethodPut, target: target + "/published", body: `{"published":true}`}.do(server)
	publishedPost := decodeBody(t, published)["post"].(map[string]interface{})

	if (published.Code != http.StatusOK) || (publishedPost["published"] != true) || (publishedPost["content"] != "<i>Edited</i>") {
		t.Errorf("publish: %d %s", published.Code, published.Body.String())
	}

	list := doRequest(server, http.MethodGet, "/api/blog-posts")

	if (list.Code != http.StatusOK) || (strings.Contains(list.Body.String(), `"title":"Hello"`) == false) {
		t.Errorf("list: %d %s", list.Code, list.Body.String())
	}

	deleted := blogRequest{method: http.MethodDelete, target: target, contentType: text("")}.do(server)
	missing := blogRequest{method: http.MethodDelete, target: target}.do(server)
	gone := doRequest(server, http.MethodGet, target)

	if (deleted.Code != http.StatusNoContent) || (missing.Code != http.StatusNotFound) || (gone.Code != http.StatusNotFound) {
		t.Errorf("delete: %d, again: %d, get: %d", deleted.Code, missing.Code, gone.Code)
	}
}

func TestIntegrationBlogUpdateWaitingOnAConcurrentEditIsAConflict(t *testing.T) {
	d := openIntegrationDB(t)
	store := d.store(kyiv)
	ctx := context.Background()
	created, _ := store.CreateBlogPost(ctx, BlogDraft{Title: "t", Content: "dashboard"})
	loadedAt, _ := parseLoadedTime(*created.UpdatedAt)
	telegram, err := d.db.Begin()

	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	if _, err := telegram.Exec(`UPDATE blog_posts SET content = 'telegram edit' WHERE id = $1`, created.ID); err != nil {
		t.Fatalf("telegram edit: %v", err)
	}

	done := make(chan error, 1)

	go func() {
		_, updateErr := store.UpdateBlogPost(ctx, created.ID, BlogDraft{Title: "t", Content: "overwrite"}, loadedAt)
		done <- updateErr
	}()

	waitForLockWait(t, d)

	if err := telegram.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if err := <-done; errors.Is(err, ErrBlogPostChanged) == false {
		t.Errorf("update that waited for the Telegram edit: err = %v", err)
	}

	kept, _ := store.BlogPost(ctx, created.ID)

	if kept.Content != "telegram edit" {
		t.Errorf("content = %q", kept.Content)
	}
}

// waitForLockWait waits until a session of the test database waits for a
// row lock.
func waitForLockWait(t *testing.T, d integrationDB) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	waiting := 0

	for (waiting == 0) && (time.Now().Before(deadline) == true) {
		if err := d.db.QueryRow(`SELECT count(*) FROM pg_stat_activity WHERE wait_event_type = 'Lock' AND datname = current_database()`).Scan(&waiting); err != nil {
			t.Fatalf("pg_stat_activity: %v", err)
		}

		time.Sleep(20 * time.Millisecond)
	}

	if waiting == 0 {
		t.Fatalf("the dashboard update never waited for the row lock")
	}
}
