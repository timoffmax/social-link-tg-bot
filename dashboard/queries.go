package dashboard

import (
	"context"
	"database/sql"
	"log"
	"math"
	"time"

	"github.com/lib/pq"
)

// statementTimeout caps every statement of a dashboard transaction.
const statementTimeout = "5s"

// Rolling windows of the overview totals, in days.
const (
	shortWindowDays = 7
	longWindowDays  = 30
)

// Media types split on the platforms page.
const (
	mediaTypePhoto = "photo"
	mediaTypeVideo = "video"
)

// Gated-fetch usage scopes and the timezone of their periods.
const (
	gatedScopeGlobal    = "global"
	gatedScopeChat      = "chat"
	gatedScopeUser      = "user"
	gatedPeriodTimezone = "UTC"
)

// platformSettingType is the chat_settings type whose enabled flag holds the
// value; every other type keeps it in setting_value.
const platformSettingType = "platform"

// settingFalseValues are the setting_value spellings strconv.ParseBool reads
// as false: the only non-default value of a non-platform setting.
var settingFalseValues = []string{"0", "f", "F", "false", "FALSE", "False"}

// defaultGroupTimezone is group_media_limits.timezone's column default,
// shown for chats without a group_media_limits row.
const defaultGroupTimezone = "UTC"

// Platform health windows and thresholds. A platform stays listed for
// healthListedDays after its last download; its baseline is the
// healthPreviousDays before the recent window or before its last download,
// whichever ends earlier.
const (
	healthRecentDays      = 7
	healthPreviousDays    = 28
	healthListedDays      = 90
	healthLastDayHours    = 24
	healthStaleAfter      = 48 * time.Hour
	healthMinPrevAvgDaily = 1.0
	healthDropRatio       = 0.5
	healthStatusOK        = "ok"
	healthStatusStale     = "stale"
	healthStatusDrop      = "drop"
)

// Rounding of the health numbers.
const (
	averageDecimals = 2
	changeDecimals  = 1
	percentFactor   = 100
	decimalBase     = 10
)

// PGStore reads the dashboard data from the bot's PostgreSQL database in
// read-only transactions; only the blog and report writes use read-write
// ones.
type PGStore struct {
	db       *sql.DB
	timezone string
	now      func() time.Time
}

// NewPGStore returns a store bucketing days in timezone (an IANA name
// already validated with time.LoadLocation).
func NewPGStore(db *sql.DB, timezone string) *PGStore {
	result := &PGStore{db: db, timezone: timezone, now: time.Now}

	return result
}

// readTx runs the queries of one request inside its transaction: a
// read-only one, or the read-write one a writeTx wraps.
type readTx struct {
	ctx context.Context
	tx  *sql.Tx
}

// rows runs a query and calls scan for every row.
func (q readTx) rows(query string, scan func(rows *sql.Rows) error, args ...interface{}) error {
	rows, err := q.tx.QueryContext(q.ctx, query, args...)

	if err == nil {
		defer rows.Close()
		err = scanRows(rows, scan)
	}

	return err
}

// row runs a single-row query into dest.
func (q readTx) row(query string, dest []interface{}, args ...interface{}) error {
	row := q.tx.QueryRowContext(q.ctx, query, args...)
	err := row.Scan(dest...)

	return err
}

// scanRows calls scan for each row until an error.
func scanRows(rows *sql.Rows, scan func(rows *sql.Rows) error) error {
	var err error

	for (err == nil) && (rows.Next() == true) {
		err = scan(rows)
	}

	if err == nil {
		err = rows.Err()
	}

	return err
}

// readOnly runs fn in a read-only transaction with the statement timeout.
// REPEATABLE READ gives every query of one response the same snapshot, so
// lists built to match each other cannot disagree.
func (s *PGStore) readOnly(ctx context.Context, fn func(q readTx) error) error {
	options := &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead}
	tx, err := s.db.BeginTx(ctx, options)

	if err == nil {
		err = runTx(ctx, tx, fn)
	}

	return err
}

// writeTx runs the statements of one blog or report write; only those
// write methods get one.
type writeTx struct {
	readTx
}

// readWrite runs fn in a read-write transaction with the statement timeout.
// BEGIN READ WRITE overrides the role's default_transaction_read_only.
// READ COMMITTED makes a conditional UPDATE that waited for a concurrent
// edit re-check its WHERE against the new row.
func (s *PGStore) readWrite(ctx context.Context, fn func(q writeTx) error) error {
	options := &sql.TxOptions{ReadOnly: false, Isolation: sql.LevelReadCommitted}
	tx, err := s.db.BeginTx(ctx, options)
	run := func(q readTx) error {
		write := writeTx{readTx: q}
		runErr := fn(write)

		return runErr
	}

	if err == nil {
		err = runTx(ctx, tx, run)
	}

	return err
}

// runTx sets the timeout, runs fn and ends the transaction.
func runTx(ctx context.Context, tx *sql.Tx, fn func(q readTx) error) error {
	defer rollback(tx)
	_, err := tx.ExecContext(ctx, setStatementTimeoutSQL, statementTimeout)

	if err == nil {
		err = fn(readTx{ctx: ctx, tx: tx})
	}

	if err == nil {
		err = tx.Commit()
	}

	return err
}

// rollback ends a transaction that was not committed; after a commit it is
// a no-op, so its error is ignored.
func rollback(tx *sql.Tx) {
	_ = tx.Rollback()
}

// currentTime is the request's "now" in UTC.
func (s *PGStore) currentTime() time.Time {
	now := s.now()
	result := now.UTC()

	return result
}

// Ping checks the database connection.
func (s *PGStore) Ping(ctx context.Context) error {
	err := s.db.PingContext(ctx)

	return err
}

// Overview loads the totals, the daily split, the platform mix and the new
// chats per week for the last days.
func (s *PGStore) Overview(ctx context.Context, days int) (Overview, error) {
	result := Overview{Days: days, Daily: []DailySplit{}, PlatformMix: []PlatformCount{}, NewChatsWeekly: []WeeklyNewChats{}}
	now := s.currentTime()
	err := s.readOnly(ctx, func(q readTx) error {
		loadErr := s.loadOverviewTotals(q, now, days, &result.Totals)

		if loadErr == nil {
			result.Daily, loadErr = s.loadDailySplit(q, now, days)
		}

		if loadErr == nil {
			result.PlatformMix, loadErr = s.loadPlatformCounts(q, now, days)
		}

		if loadErr == nil {
			result.NewChatsWeekly, loadErr = s.loadNewChatsWeekly(q, now, days)
		}

		return loadErr
	})

	return result, err
}

// loadOverviewTotals reads the headline numbers.
func (s *PGStore) loadOverviewTotals(q readTx, now time.Time, days int, totals *OverviewTotals) error {
	dest := []interface{}{
		&totals.DownloadsAll,
		&totals.DownloadsRange,
		&totals.Downloads7d,
		&totals.Downloads30d,
		&totals.ActiveUsers7d,
		&totals.ActiveUsers30d,
		&totals.ActiveChats7d,
		&totals.ActiveChats30d,
		&totals.GroupsAll,
		&totals.PrivateUsersAll,
	}
	err := q.row(overviewTotalsSQL, dest, s.timezone, now, days, shortWindowDays, longWindowDays)

	return err
}

// loadDailySplit reads the gap-filled private/group downloads per day.
func (s *PGStore) loadDailySplit(q readTx, now time.Time, days int) ([]DailySplit, error) {
	result := []DailySplit{}
	err := q.rows(overviewDailySQL, func(rows *sql.Rows) error {
		item := DailySplit{}
		scanErr := rows.Scan(&item.Day, &item.Private, &item.Group)
		result = append(result, item)

		return scanErr
	}, s.timezone, now, days)

	return result, err
}

// loadPlatformCounts reads the downloads per platform in the range.
func (s *PGStore) loadPlatformCounts(q readTx, now time.Time, days int) ([]PlatformCount, error) {
	result := []PlatformCount{}
	err := q.rows(platformMixSQL, func(rows *sql.Rows) error {
		item := PlatformCount{}
		scanErr := rows.Scan(&item.Platform, &item.Downloads)
		result = append(result, item)

		return scanErr
	}, s.timezone, now, days)

	return result, err
}

// loadNewChatsWeekly reads the gap-filled first-download weeks of chats.
func (s *PGStore) loadNewChatsWeekly(q readTx, now time.Time, days int) ([]WeeklyNewChats, error) {
	result := []WeeklyNewChats{}
	err := q.rows(newChatsWeeklySQL, func(rows *sql.Rows) error {
		item := WeeklyNewChats{}
		scanErr := rows.Scan(&item.Week, &item.Groups, &item.Private)
		result = append(result, item)

		return scanErr
	}, s.timezone, now, days)

	return result, err
}

// Platforms loads the platform stats, then in its own transaction the bot's
// services health, passed through undecoded.
func (s *PGStore) Platforms(ctx context.Context, days int) (Platforms, error) {
	result := Platforms{Days: days, Platforms: []string{}, Daily: []PlatformDay{}, MediaTypes: []MediaTypeSplit{}, Health: []PlatformHealth{}}
	now := s.currentTime()
	err := s.readOnly(ctx, func(q readTx) error {
		counts, loadErr := s.loadPlatformCounts(q, now, days)
		result.Platforms = platformNames(counts)

		if loadErr == nil {
			result.Daily, loadErr = s.loadPlatformDaily(q, now, days, result.Platforms)
		}

		if loadErr == nil {
			result.MediaTypes, loadErr = s.loadMediaTypes(q, now, days)
		}

		if loadErr == nil {
			result.Health, loadErr = loadPlatformHealth(q, now)
		}

		return loadErr
	})

	if err == nil {
		result.Services = s.loadServicesOrNil(ctx, now)
	}

	return result, err
}

// loadServicesOrNil reads the services component in its own transaction; a
// failed read is logged and gives nil, so the rest of the tab still loads.
func (s *PGStore) loadServicesOrNil(ctx context.Context, now time.Time) *PublishedComponent {
	var services *PublishedComponent
	err := s.readOnly(ctx, func(q readTx) error {
		var loadErr error
		services, loadErr = loadServicesComponent(q, now)

		return loadErr
	})
	result := servicesOrNil(services, err)

	return result
}

// servicesOrNil returns services, or nil (logged) after a failed read.
func servicesOrNil(services *PublishedComponent, err error) *PublishedComponent {
	result := services

	if err != nil {
		log.Printf("dashboard: services health not loaded: %v", err)
		result = nil
	}

	return result
}

// loadServicesComponent reads the bot's services component; nil when it was
// never published.
func loadServicesComponent(q readTx, now time.Time) (*PublishedComponent, error) {
	components, err := loadStatusComponents(q, []string{componentServices})
	result := publishedComponent(components, componentServices, now)

	return result, err
}

// platformNames keeps the order of the counts.
func platformNames(counts []PlatformCount) []string {
	result := []string{}

	for _, count := range counts {
		result = append(result, count.Platform)
	}

	return result
}

// loadPlatformDaily reads the gap-filled downloads per day and platform;
// every day has a count for every platform of the range.
func (s *PGStore) loadPlatformDaily(q readTx, now time.Time, days int, platforms []string) ([]PlatformDay, error) {
	result := []PlatformDay{}
	err := q.rows(platformDailySQL, func(rows *sql.Rows) error {
		var day string
		var platform sql.NullString
		var downloads int64
		scanErr := rows.Scan(&day, &platform, &downloads)
		lastIndex := len(result) - 1
		isNewDay := (lastIndex < 0) || (result[lastIndex].Day != day)

		if isNewDay == true {
			platformDay := newPlatformDay(day, platforms)
			result = append(result, platformDay)
			lastIndex = len(result) - 1
		}

		if platform.Valid == true {
			result[lastIndex].Counts[platform.String] = downloads
		}

		return scanErr
	}, s.timezone, now, days)

	return result, err
}

// newPlatformDay starts a day with a zero count for every platform.
func newPlatformDay(day string, platforms []string) PlatformDay {
	result := PlatformDay{Day: day, Counts: map[string]int64{}}

	for _, platform := range platforms {
		result.Counts[platform] = 0
	}

	return result
}

// loadMediaTypes reads the photo/video split per platform in the range.
func (s *PGStore) loadMediaTypes(q readTx, now time.Time, days int) ([]MediaTypeSplit, error) {
	result := []MediaTypeSplit{}
	err := q.rows(mediaTypesSQL, func(rows *sql.Rows) error {
		item := MediaTypeSplit{}
		scanErr := rows.Scan(&item.Platform, &item.Photo, &item.Video)
		result = append(result, item)

		return scanErr
	}, s.timezone, now, days, mediaTypePhoto, mediaTypeVideo)

	return result, err
}

// healthRow is one platform's raw health counts.
type healthRow struct {
	platform  string
	lastAt    sql.NullTime
	lastDay   int64
	recent    int64
	previous  int64
	reference time.Time
}

// loadPlatformHealth reads every platform seen in the last
// healthListedDays.
func loadPlatformHealth(q readTx, now time.Time) ([]PlatformHealth, error) {
	result := []PlatformHealth{}
	err := q.rows(platformHealthSQL, func(rows *sql.Rows) error {
		row := healthRow{reference: now}
		scanErr := rows.Scan(&row.platform, &row.lastAt, &row.lastDay, &row.recent, &row.previous)
		health := buildPlatformHealth(row)
		result = append(result, health)

		return scanErr
	}, now, healthRecentDays, healthPreviousDays, healthLastDayHours, healthListedDays)

	return result, err
}

// buildPlatformHealth turns the counts into averages, change and status.
func buildPlatformHealth(row healthRow) PlatformHealth {
	avgPrevious := float64(row.previous) / healthPreviousDays
	avgRecent := float64(row.recent) / healthRecentDays
	result := PlatformHealth{
		Platform:        row.platform,
		LastDownloadAt:  formatNullTime(row.lastAt),
		Downloads24h:    row.lastDay,
		Downloads7d:     row.recent,
		AvgDailyPrev28d: roundTo(avgPrevious, averageDecimals),
		AvgDaily7d:      roundTo(avgRecent, averageDecimals),
		Status:          healthStatus(row.lastAt, avgPrevious, avgRecent, row.reference),
	}

	if avgPrevious > 0 {
		change := roundTo(((avgRecent - avgPrevious) / avgPrevious * percentFactor), changeDecimals)
		result.ChangePct = &change
	}

	return result
}

// healthStatus is "stale" when a platform that averaged at least one
// download a day over its baseline had none for healthStaleAfter, "drop" when its last week
// fell under healthDropRatio of the weeks before, else "ok".
func healthStatus(lastAt sql.NullTime, avgPrevious float64, avgRecent float64, now time.Time) string {
	result := healthStatusOK
	isBusy := (avgPrevious >= healthMinPrevAvgDaily)
	isQuiet := (lastAt.Valid == false) || (now.Sub(lastAt.Time) > healthStaleAfter)
	isStale := (isBusy == true) && (isQuiet == true)
	isDrop := (isBusy == true) && (avgRecent < (avgPrevious * healthDropRatio))

	if isDrop == true {
		result = healthStatusDrop
	}

	if isStale == true {
		result = healthStatusStale
	}

	return result
}

// roundTo rounds value to the given number of decimals.
func roundTo(value float64, decimals int) float64 {
	factor := math.Pow(decimalBase, float64(decimals))
	rounded := math.Round(value * factor)
	result := rounded / factor

	return result
}

// formatNullTime renders a nullable timestamp as RFC3339 UTC or nil.
func formatNullTime(value sql.NullTime) *string {
	var result *string

	if value.Valid == true {
		formatted := formatTime(value.Time)
		result = &formatted
	}

	return result
}

// Chats loads the top groups and users, the monthly retention and the chats
// with custom settings.
func (s *PGStore) Chats(ctx context.Context, days int, limit int) (Chats, error) {
	result := Chats{Days: days, Limit: limit, TopGroups: []TopGroup{}, TopUsers: []TopUser{}, CustomSettings: []CustomChat{}}
	now := s.currentTime()
	err := s.readOnly(ctx, func(q readTx) error {
		var loadErr error
		result.TopGroups, loadErr = s.loadTopGroups(q, now, days, limit)

		if loadErr == nil {
			result.TopUsers, loadErr = s.loadTopUsers(q, now, days, limit)
		}

		if loadErr == nil {
			loadErr = s.loadRetention(q, now, &result.Retention)
		}

		if loadErr == nil {
			result.CustomSettings, loadErr = loadCustomChats(q)
		}

		return loadErr
	})

	return result, err
}

// loadTopGroups reads the groups with the most downloads in the range.
func (s *PGStore) loadTopGroups(q readTx, now time.Time, days int, limit int) ([]TopGroup, error) {
	result := []TopGroup{}
	err := q.rows(topGroupsSQL, func(rows *sql.Rows) error {
		item := TopGroup{}
		var lastAt sql.NullTime
		scanErr := rows.Scan(&item.ChatID, &item.Title, &item.Downloads, &item.Users, &lastAt)
		item.LastAt = formatNullTime(lastAt)
		result = append(result, item)

		return scanErr
	}, s.timezone, now, days, limit)

	return result, err
}

// loadTopUsers reads the private-chat users with the most downloads.
func (s *PGStore) loadTopUsers(q readTx, now time.Time, days int, limit int) ([]TopUser, error) {
	result := []TopUser{}
	err := q.rows(topUsersSQL, func(rows *sql.Rows) error {
		item := TopUser{}
		var lastAt sql.NullTime
		scanErr := rows.Scan(&item.UserID, &item.Username, &item.Downloads, &lastAt)
		item.LastAt = formatNullTime(lastAt)
		result = append(result, item)

		return scanErr
	}, s.timezone, now, days, limit)

	return result, err
}

// loadRetention reads this month's and last month's active chats.
func (s *PGStore) loadRetention(q readTx, now time.Time, retention *Retention) error {
	dest := []interface{}{&retention.Month, &retention.ActiveThisMonth, &retention.ActiveLastMonth, &retention.Retained}
	err := q.row(retentionSQL, dest, s.timezone, now)

	return err
}

// loadCustomChats reads the chats with non-default settings or limits and
// attaches their non-default settings.
func loadCustomChats(q readTx) ([]CustomChat, error) {
	result := []CustomChat{}
	falseValues := pq.Array(settingFalseValues)
	err := q.rows(customChatsSQL, func(rows *sql.Rows) error {
		item := CustomChat{Settings: []ChatSetting{}}
		scanErr := rows.Scan(&item.ChatID, &item.Title, &item.DailyLimit, &item.Timezone, &item.UserLimits)
		result = append(result, item)

		return scanErr
	}, platformSettingType, falseValues, defaultGroupTimezone)

	if err == nil {
		err = attachChatSettings(q, result, falseValues)
	}

	return result, err
}

// attachChatSettings adds every non-default chat setting to its chat.
func attachChatSettings(q readTx, chats []CustomChat, falseValues interface{}) error {
	indexByChat := map[int64]int{}

	for index, chat := range chats {
		indexByChat[chat.ChatID] = index
	}

	err := q.rows(chatSettingsSQL, func(rows *sql.Rows) error {
		var chatID int64
		setting := ChatSetting{}
		scanErr := rows.Scan(&chatID, &setting.Type, &setting.Key, &setting.Value, &setting.Enabled)
		index, isKnown := indexByChat[chatID]

		if (scanErr == nil) && (isKnown == true) {
			chats[index].Settings = append(chats[index].Settings, setting)
		}

		return scanErr
	}, platformSettingType, falseValues)

	return err
}

// Money loads the special-download usage, the Stars payments and the unused
// bonus credits.
func (s *PGStore) Money(ctx context.Context, months int) (Money, error) {
	result := Money{Months: months, GatedMonthly: []GatedMonth{}, StarsMonthly: []StarsMonth{}}
	now := s.currentTime()
	err := s.readOnly(ctx, func(q readTx) error {
		var loadErr error
		result.GatedMonthly, loadErr = loadGatedMonthly(q, now, months)

		if loadErr == nil {
			result.StarsMonthly, loadErr = s.loadStarsMonthly(q, now, months)
		}

		if loadErr == nil {
			loadErr = loadCredits(q, &result.Credits)
		}

		return loadErr
	})

	return result, err
}

// loadGatedMonthly reads the gap-filled monthly usage per scope.
func loadGatedMonthly(q readTx, now time.Time, months int) ([]GatedMonth, error) {
	result := []GatedMonth{}
	err := q.rows(gatedMonthlySQL, func(rows *sql.Rows) error {
		item := GatedMonth{}
		scanErr := rows.Scan(&item.Month, &item.GlobalUsed, &item.ChatsUsed, &item.UsersUsed)
		result = append(result, item)

		return scanErr
	}, now, gatedPeriodTimezone, months, gatedScopeGlobal, gatedScopeChat, gatedScopeUser)

	return result, err
}

// loadStarsMonthly reads the gap-filled Stars payments per month.
func (s *PGStore) loadStarsMonthly(q readTx, now time.Time, months int) ([]StarsMonth, error) {
	result := []StarsMonth{}
	err := q.rows(starsMonthlySQL, func(rows *sql.Rows) error {
		item := StarsMonth{}
		scanErr := rows.Scan(&item.Month, &item.Payments, &item.Stars, &item.Refunds, &item.RefundedStars, &item.Boosts)
		result = append(result, item)

		return scanErr
	}, s.timezone, now, months)

	return result, err
}

// loadCredits reads the unused bonus credits.
func loadCredits(q readTx, credits *Credits) error {
	dest := []interface{}{&credits.UsersWithCredits, &credits.UserCredits, &credits.ChatsWithCredits, &credits.ChatCredits}
	err := q.row(creditsSQL, dest)

	return err
}

// Reports loads the report totals, the weekly counts and the latest
// reports matching status (open, resolved or all) with their replies.
func (s *PGStore) Reports(ctx context.Context, limit int, weeks int, status string) (Reports, error) {
	result := Reports{Limit: limit, Weeks: weeks, Status: status, Weekly: []ReportWeek{}, Latest: []Report{}}
	now := s.currentTime()
	err := s.readOnly(ctx, func(q readTx) error {
		dest := []interface{}{&result.Totals.Open, &result.Totals.Resolved}
		loadErr := q.row(reportTotalsSQL, dest)

		if loadErr == nil {
			result.Weekly, loadErr = s.loadReportsWeekly(q, now, weeks)
		}

		if loadErr == nil {
			result.Latest, loadErr = loadLatestReports(q, limit, status)
		}

		return loadErr
	})

	return result, err
}

// loadReportsWeekly reads the gap-filled reports per created week.
func (s *PGStore) loadReportsWeekly(q readTx, now time.Time, weeks int) ([]ReportWeek, error) {
	result := []ReportWeek{}
	err := q.rows(reportsWeeklySQL, func(rows *sql.Rows) error {
		item := ReportWeek{}
		scanErr := rows.Scan(&item.Week, &item.Open, &item.Resolved)
		result = append(result, item)

		return scanErr
	}, s.timezone, now, weeks)

	return result, err
}

// loadLatestReports reads the newest reports matching status, with their
// replies.
func loadLatestReports(q readTx, limit int, status string) ([]Report, error) {
	result := []Report{}
	resolvedFilter := reportStatusFilter(status)
	err := q.rows(latestReportsSQL, func(rows *sql.Rows) error {
		item, scanErr := scanReport(rows)
		result = append(result, item)

		return scanErr
	}, limit, resolvedFilter)

	if err == nil {
		err = attachReportReplies(q, result)
	}

	return result, err
}
