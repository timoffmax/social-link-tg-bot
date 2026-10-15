package services

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/lib/pq"
	"github.com/timoffmax/social-link-bot/config"
)

// loadTimezoneWithFallback loads a timezone with fallback support for common issues
func loadTimezoneWithFallback(timezoneStr string, chatID int64) *time.Location {
	// Try primary timezone first
	loc, err := time.LoadLocation(timezoneStr)
	if err == nil {
		return loc
	}

	// Handle Europe/Kyiv -> Europe/Kiev fallback
	if timezoneStr == "Europe/Kyiv" {
		loc, err = time.LoadLocation("Europe/Kiev")
		if err == nil {
			log.Printf("Timezone Europe/Kyiv not available for chat %d, using Europe/Kiev fallback", chatID)
			return loc
		}
	}

	// Handle Europe/Kiev -> Europe/Kyiv fallback (for future compatibility)
	if timezoneStr == "Europe/Kiev" {
		loc, err = time.LoadLocation("Europe/Kyiv")
		if err == nil {
			log.Printf("Timezone Europe/Kiev not available for chat %d, using Europe/Kyiv fallback", chatID)
			return loc
		}
	}

	// Final fallback to UTC
	log.Printf("Invalid timezone %s for chat %d, falling back to UTC", timezoneStr, chatID)
	return time.UTC
}

type DatabaseService struct {
	db *sql.DB
}

var dbService *DatabaseService

func InitDatabase() error {
	// First, try to create the database if it doesn't exist
	if err := createDatabaseIfNotExists(); err != nil {
		return fmt.Errorf("failed to create database: %w", err)
	}

	// Now connect to the specific database
	connStr := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		config.GetDBHost(),
		config.GetDBPort(),
		config.GetDBUser(),
		config.GetDBPassword(),
		config.GetDBName(),
		config.GetDBSSLMode(),
	)

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}

	// Test connection
	if err := db.Ping(); err != nil {
		return fmt.Errorf("failed to ping database: %w", err)
	}

	dbService = &DatabaseService{db: db}

	// Run migrations
	if err := runMigrations(db); err != nil {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	log.Println("Database connected and migrations applied successfully")
	return nil
}

func createDatabaseIfNotExists() error {
	// Connect to postgres database to create our database
	connStr := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=postgres sslmode=%s",
		config.GetDBHost(),
		config.GetDBPort(),
		config.GetDBUser(),
		config.GetDBPassword(),
		config.GetDBSSLMode(),
	)

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return fmt.Errorf("failed to connect to postgres database: %w", err)
	}
	defer db.Close()

	// Check if database exists
	dbName := config.GetDBName()
	var exists bool
	query := `SELECT EXISTS(SELECT datname FROM pg_database WHERE datname = $1)`
	err = db.QueryRow(query, dbName).Scan(&exists)
	if err != nil {
		return fmt.Errorf("failed to check if database exists: %w", err)
	}

	if !exists {
		// Create the database
		createQuery := fmt.Sprintf("CREATE DATABASE %s", dbName)
		_, err = db.Exec(createQuery)
		if err != nil {
			return fmt.Errorf("failed to create database %s: %w", dbName, err)
		}
		log.Printf("Created database: %s", dbName)
	} else {
		log.Printf("Database %s already exists", dbName)
	}

	return nil
}

func runMigrations(db *sql.DB) error {
	driver, err := postgres.WithInstance(db, &postgres.Config{})
	if err != nil {
		return fmt.Errorf("failed to create migration driver: %w", err)
	}

	m, err := migrate.NewWithDatabaseInstance(
		"file://migrations",
		"postgres",
		driver,
	)
	if err != nil {
		return fmt.Errorf("failed to create migration instance: %w", err)
	}

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	return nil
}

func GetDB() *sql.DB {
	if dbService == nil {
		return nil
	}
	return dbService.db
}

func CloseDatabase() error {
	if dbService != nil && dbService.db != nil {
		return dbService.db.Close()
	}
	return nil
}

// IsDBInitialized returns true if database is successfully initialized
func IsDBInitialized() bool {
	return dbService != nil && dbService.db != nil
}

// RecordMediaStat records a media send event
func RecordMediaStat(userID int64, username string, chatID int64, chatTitle string, platform string, mediaType string, url string) error {
	if !IsDBInitialized() {
		// Silently skip if database is not available
		return nil
	}
	query := `
		INSERT INTO media_stats (user_id, username, chat_id, chat_title, platform, media_type, url)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`
	_, err := GetDB().Exec(query, userID, username, chatID, chatTitle, platform, mediaType, url)
	return err
}

// GetDailyMediaCount returns the number of media items sent by a user today
func GetDailyMediaCount(userID int64, chatID int64) (int, error) {
	if !IsDBInitialized() {
		// Return 0 if database is not available
		return 0, nil
	}
	query := `
		SELECT COUNT(*) FROM media_stats 
		WHERE user_id = $1 AND chat_id = $2 AND DATE(created_at AT TIME ZONE 'UTC') = DATE(NOW() AT TIME ZONE 'UTC')
	`
	var count int
	err := GetDB().QueryRow(query, userID, chatID).Scan(&count)
	return count, err
}

// GetUserMediaLimit returns the daily media limit for a user in a chat
func GetUserMediaLimit(userID int64, chatID int64) (int, error) {
	if !IsDBInitialized() {
		return config.GetDefaultDailyMediaLimit(), nil
	}
	query := `SELECT daily_limit FROM user_media_limits WHERE user_id = $1 AND chat_id = $2`
	var limit int
	err := GetDB().QueryRow(query, userID, chatID).Scan(&limit)
	if err == sql.ErrNoRows {
		return config.GetDefaultDailyMediaLimit(), nil
	}
	return limit, err
}

// SetUserMediaLimit sets the daily media limit for a user in a chat
func SetUserMediaLimit(userID int64, chatID int64, limit int) error {
	if !IsDBInitialized() {
		return nil
	}
	query := `
		INSERT INTO user_media_limits (user_id, chat_id, daily_limit, updated_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (chat_id, user_id) 
		DO UPDATE SET daily_limit = $3, updated_at = NOW()
	`
	_, err := GetDB().Exec(query, userID, chatID, limit)
	return err
}

type PlatformStats struct {
	Platform string `json:"platform"`
	Photos   int    `json:"photos"`
	Videos   int    `json:"videos"`
	Total    int    `json:"total"`
}

type MediaStats struct {
	TotalPhotos int             `json:"total_photos"`
	TotalVideos int             `json:"total_videos"`
	Total       int             `json:"total"`
	Platforms   []PlatformStats `json:"platforms"`
}

// GetMediaStats returns media statistics for a given time period
func GetMediaStats(chatID int64, period string) (*MediaStats, error) {
	if !IsDBInitialized() {
		return &MediaStats{}, nil
	}
	var whereClause string
	switch period {
	case "today":
		whereClause = "DATE(created_at AT TIME ZONE 'UTC') = DATE(NOW() AT TIME ZONE 'UTC')"
	case "week":
		whereClause = "created_at >= NOW() - INTERVAL '7 days'"
	case "month":
		whereClause = "created_at >= NOW() - INTERVAL '30 days'"
	case "year":
		whereClause = "created_at >= NOW() - INTERVAL '365 days'"
	default:
		return nil, fmt.Errorf("invalid period: %s", period)
	}

	// Get platform stats
	query := fmt.Sprintf(`
		SELECT platform, media_type, COUNT(*) as count
		FROM media_stats 
		WHERE chat_id = $1 AND %s
		GROUP BY platform, media_type
		ORDER BY platform, media_type
	`, whereClause)

	rows, err := GetDB().Query(query, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	platformMap := make(map[string]*PlatformStats)
	var totalPhotos, totalVideos int

	for rows.Next() {
		var platform, mediaType string
		var count int
		if err := rows.Scan(&platform, &mediaType, &count); err != nil {
			return nil, err
		}

		if _, exists := platformMap[platform]; !exists {
			platformMap[platform] = &PlatformStats{Platform: platform}
		}

		if mediaType == "photo" {
			platformMap[platform].Photos = count
			totalPhotos += count
		} else if mediaType == "video" {
			platformMap[platform].Videos = count
			totalVideos += count
		}
		platformMap[platform].Total = platformMap[platform].Photos + platformMap[platform].Videos
	}

	// Convert map to slice
	var platforms []PlatformStats
	for _, stats := range platformMap {
		platforms = append(platforms, *stats)
	}

	return &MediaStats{
		TotalPhotos: totalPhotos,
		TotalVideos: totalVideos,
		Total:       totalPhotos + totalVideos,
		Platforms:   platforms,
	}, nil
}

// GroupLimitInfo represents group limit and timezone settings
type GroupLimitInfo struct {
	ChatID            int64
	ChatTitle         string
	DefaultDailyLimit int
	Timezone          string
}

// SetGroupMediaLimit sets the default daily media limit for a group
func SetGroupMediaLimit(chatID int64, chatTitle string, limit int) error {
	if !IsDBInitialized() {
		return nil
	}

	query := `
		INSERT INTO group_media_limits (chat_id, chat_title, default_daily_limit)
		VALUES ($1, $2, $3)
		ON CONFLICT (chat_id)
		DO UPDATE SET
			default_daily_limit = EXCLUDED.default_daily_limit,
			chat_title = EXCLUDED.chat_title,
			updated_at = CURRENT_TIMESTAMP
	`

	_, err := GetDB().Exec(query, chatID, chatTitle, limit)
	return err
}

// GetGroupMediaLimit gets the default daily media limit for a group
func GetGroupMediaLimit(chatID int64) (int, error) {
	if !IsDBInitialized() {
		return 0, nil
	}

	var limit int
	query := `SELECT default_daily_limit FROM group_media_limits WHERE chat_id = $1`
	err := GetDB().QueryRow(query, chatID).Scan(&limit)

	if err == sql.ErrNoRows {
		return 0, nil // No group limit set
	}

	return limit, err
}

// SetGroupTimezone sets the timezone for a group
func SetGroupTimezone(chatID int64, chatTitle string, timezone string) error {
	if !IsDBInitialized() {
		return nil
	}

	query := `
		INSERT INTO group_media_limits (chat_id, chat_title, timezone)
		VALUES ($1, $2, $3)
		ON CONFLICT (chat_id)
		DO UPDATE SET
			timezone = EXCLUDED.timezone,
			chat_title = EXCLUDED.chat_title,
			updated_at = CURRENT_TIMESTAMP
	`

	_, err := GetDB().Exec(query, chatID, chatTitle, timezone)
	return err
}

// GetGroupTimezone gets the timezone for a group
func GetGroupTimezone(chatID int64) (string, error) {
	if !IsDBInitialized() {
		return "UTC", nil
	}

	var timezone string
	query := `SELECT timezone FROM group_media_limits WHERE chat_id = $1`
	err := GetDB().QueryRow(query, chatID).Scan(&timezone)

	if err == sql.ErrNoRows {
		return "UTC", nil // Default to UTC if no timezone set
	}

	return timezone, err
}

// GetGroupLimitInfo gets complete group limit information
func GetGroupLimitInfo(chatID int64) (*GroupLimitInfo, error) {
	if !IsDBInitialized() {
		// Return default values if database not available
		return &GroupLimitInfo{
			ChatID:            chatID,
			ChatTitle:         "",
			DefaultDailyLimit: 0,
			Timezone:          "UTC",
		}, nil
	}

	var info GroupLimitInfo
	query := `SELECT chat_id, chat_title, default_daily_limit, timezone FROM group_media_limits WHERE chat_id = $1`
	err := GetDB().QueryRow(query, chatID).Scan(&info.ChatID, &info.ChatTitle, &info.DefaultDailyLimit, &info.Timezone)

	if err == sql.ErrNoRows {
		// Return default values if no group settings exist
		return &GroupLimitInfo{
			ChatID:            chatID,
			ChatTitle:         "",
			DefaultDailyLimit: 0,
			Timezone:          "UTC",
		}, nil
	}

	return &info, err
}

// GetEffectiveUserLimit gets the effective limit for a user (individual limit or group default)
func GetEffectiveUserLimit(userID int64, chatID int64) (int, bool, error) {
	if !IsDBInitialized() {
		// Return 0 (no limit) if database is not available
		return 0, false, nil
	}

	// First check if user has an individual limit
	userLimit, err := GetUserMediaLimit(userID, chatID)
	if err != nil {
		return 0, false, err
	}

	if userLimit > 0 {
		return userLimit, false, nil // Individual limit found
	}

	// No individual limit, check group default
	groupLimit, err := GetGroupMediaLimit(chatID)
	if err != nil {
		return 0, false, err
	}

	return groupLimit, true, nil // Group limit (or 0 if no limit)
}

// GetTimezoneAwareResetTime calculates when the daily limit resets based on group timezone
func GetTimezoneAwareResetTime(chatID int64) (time.Time, time.Duration, error) {
	// Get group timezone
	timezoneStr, err := GetGroupTimezone(chatID)
	if err != nil {
		return time.Time{}, 0, err
	}

	// Load timezone with fallback support
	loc := loadTimezoneWithFallback(timezoneStr, chatID)

	// Get current time in group's timezone
	now := time.Now().In(loc)

	// Calculate next midnight in group's timezone
	tomorrow := now.AddDate(0, 0, 1)
	resetTime := time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), 0, 0, 0, 0, loc)

	// Calculate duration until reset
	timeUntilReset := resetTime.Sub(now)

	return resetTime, timeUntilReset, nil
}

// UserStats represents media statistics for a specific user
type UserStats struct {
	UserID    int64
	Username  string
	Photos    int
	Videos    int
	Total     int
	Platforms []PlatformStats
}

// GetUserMediaStats gets media statistics for all users in a chat for a specific period
func GetUserMediaStats(chatID int64, period string) ([]UserStats, error) {
	db := GetDB()
	if db == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	var whereClause string
	switch period {
	case "today":
		whereClause = "DATE(created_at) = CURRENT_DATE"
	case "week":
		whereClause = "created_at >= NOW() - INTERVAL '7 days'"
	case "month":
		whereClause = "created_at >= NOW() - INTERVAL '30 days'"
	case "year":
		whereClause = "created_at >= NOW() - INTERVAL '365 days'"
	default:
		return nil, fmt.Errorf("invalid period: %s", period)
	}

	query := fmt.Sprintf(`
		SELECT 
			user_id,
			COALESCE(username, '') as username,
			platform,
			media_type,
			COUNT(*) as count
		FROM media_stats 
		WHERE chat_id = $1 AND %s
		GROUP BY user_id, username, platform, media_type
		ORDER BY user_id, platform, media_type
	`, whereClause)

	rows, err := GetDB().Query(query, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	userMap := make(map[int64]*UserStats)

	for rows.Next() {
		var userID int64
		var username, platform, mediaType string
		var count int

		if err := rows.Scan(&userID, &username, &platform, &mediaType, &count); err != nil {
			return nil, err
		}

		// Get or create user stats
		if _, exists := userMap[userID]; !exists {
			userMap[userID] = &UserStats{
				UserID:    userID,
				Username:  username,
				Photos:    0,
				Videos:    0,
				Total:     0,
				Platforms: []PlatformStats{},
			}
		}

		user := userMap[userID]

		// Update user totals
		if mediaType == "photo" {
			user.Photos += count
		} else if mediaType == "video" {
			user.Videos += count
		}
		user.Total += count

		// Find or create platform stats for this user
		var platformStat *PlatformStats
		for i := range user.Platforms {
			if user.Platforms[i].Platform == platform {
				platformStat = &user.Platforms[i]
				break
			}
		}

		if platformStat == nil {
			user.Platforms = append(user.Platforms, PlatformStats{Platform: platform})
			platformStat = &user.Platforms[len(user.Platforms)-1]
		}

		// Update platform stats
		if mediaType == "photo" {
			platformStat.Photos += count
		} else if mediaType == "video" {
			platformStat.Videos += count
		}
		platformStat.Total = platformStat.Photos + platformStat.Videos
	}

	// Convert map to slice and sort by total (descending)
	var users []UserStats
	for _, user := range userMap {
		users = append(users, *user)
	}

	// Sort users by total media count (descending)
	for i := 0; i < len(users)-1; i++ {
		for j := i + 1; j < len(users); j++ {
			if users[i].Total < users[j].Total {
				users[i], users[j] = users[j], users[i]
			}
		}
	}

	return users, nil
}

// AdminGlobalStats represents global bot statistics for admin
type AdminGlobalStats struct {
	TotalChats     int
	TotalLinks     int
	TotalPhotos    int
	TotalVideos    int
	TotalDocuments int
	PlatformStats  []PlatformStats
	TopChats       []ChatStats
}

// ChatStats represents statistics for a single chat
type ChatStats struct {
	ChatID    int64
	ChatTitle string
	Total     int
	Photos    int
	Videos    int
	Documents int
}

// GetGlobalStats returns global statistics across all chats
func GetGlobalStats(period string) (*AdminGlobalStats, error) {
	if !IsDBInitialized() {
		return &AdminGlobalStats{}, nil
	}

	var whereClause string
	switch period {
	case "today":
		whereClause = "WHERE DATE(created_at AT TIME ZONE 'UTC') = DATE(NOW() AT TIME ZONE 'UTC')"
	case "week":
		whereClause = "WHERE created_at >= NOW() - INTERVAL '7 days'"
	case "month":
		whereClause = "WHERE created_at >= NOW() - INTERVAL '30 days'"
	case "year":
		whereClause = "WHERE created_at >= NOW() - INTERVAL '365 days'"
	case "all":
		whereClause = ""
	default:
		return nil, fmt.Errorf("invalid period: %s", period)
	}

	stats := &AdminGlobalStats{}

	// Get total unique chats
	chatCountQuery := fmt.Sprintf("SELECT COUNT(DISTINCT chat_id) FROM media_stats %s", whereClause)
	err := GetDB().QueryRow(chatCountQuery).Scan(&stats.TotalChats)
	if err != nil {
		return nil, fmt.Errorf("failed to get chat count: %w", err)
	}

	// Get totals by media type
	totalsQuery := fmt.Sprintf(`
		SELECT
			COUNT(*) as total,
			SUM(CASE WHEN media_type = 'photo' THEN 1 ELSE 0 END) as photos,
			SUM(CASE WHEN media_type = 'video' THEN 1 ELSE 0 END) as videos,
			SUM(CASE WHEN media_type = 'document' THEN 1 ELSE 0 END) as documents
		FROM media_stats %s
	`, whereClause)
	err = GetDB().QueryRow(totalsQuery).Scan(&stats.TotalLinks, &stats.TotalPhotos, &stats.TotalVideos, &stats.TotalDocuments)
	if err != nil {
		return nil, fmt.Errorf("failed to get totals: %w", err)
	}

	// Get platform stats
	platformQuery := fmt.Sprintf(`
		SELECT platform,
			SUM(CASE WHEN media_type = 'photo' THEN 1 ELSE 0 END) as photos,
			SUM(CASE WHEN media_type = 'video' THEN 1 ELSE 0 END) as videos,
			COUNT(*) as total
		FROM media_stats %s
		GROUP BY platform
		ORDER BY total DESC
	`, whereClause)

	rows, err := GetDB().Query(platformQuery)
	if err != nil {
		return nil, fmt.Errorf("failed to get platform stats: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var ps PlatformStats
		if err := rows.Scan(&ps.Platform, &ps.Photos, &ps.Videos, &ps.Total); err != nil {
			return nil, err
		}
		stats.PlatformStats = append(stats.PlatformStats, ps)
	}

	// Get top chats
	topChatsQuery := fmt.Sprintf(`
		SELECT chat_id, COALESCE(chat_title, 'Private Chat') as chat_title,
			COUNT(*) as total,
			SUM(CASE WHEN media_type = 'photo' THEN 1 ELSE 0 END) as photos,
			SUM(CASE WHEN media_type = 'video' THEN 1 ELSE 0 END) as videos,
			SUM(CASE WHEN media_type = 'document' THEN 1 ELSE 0 END) as documents
		FROM media_stats %s
		GROUP BY chat_id, chat_title
		ORDER BY total DESC
		LIMIT 10
	`, whereClause)

	chatRows, err := GetDB().Query(topChatsQuery)
	if err != nil {
		return nil, fmt.Errorf("failed to get top chats: %w", err)
	}
	defer chatRows.Close()

	for chatRows.Next() {
		var cs ChatStats
		if err := chatRows.Scan(&cs.ChatID, &cs.ChatTitle, &cs.Total, &cs.Photos, &cs.Videos, &cs.Documents); err != nil {
			return nil, err
		}
		stats.TopChats = append(stats.TopChats, cs)
	}

	return stats, nil
}

// GetAllChatsWithStats returns all chats with their statistics
func GetAllChatsWithStats(period string) ([]ChatStats, error) {
	if !IsDBInitialized() {
		return nil, nil
	}

	var whereClause string
	switch period {
	case "today":
		whereClause = "WHERE DATE(created_at AT TIME ZONE 'UTC') = DATE(NOW() AT TIME ZONE 'UTC')"
	case "week":
		whereClause = "WHERE created_at >= NOW() - INTERVAL '7 days'"
	case "month":
		whereClause = "WHERE created_at >= NOW() - INTERVAL '30 days'"
	case "year":
		whereClause = "WHERE created_at >= NOW() - INTERVAL '365 days'"
	case "all":
		whereClause = ""
	default:
		return nil, fmt.Errorf("invalid period: %s", period)
	}

	query := fmt.Sprintf(`
		SELECT chat_id, COALESCE(chat_title, 'Private Chat') as chat_title,
			COUNT(*) as total,
			SUM(CASE WHEN media_type = 'photo' THEN 1 ELSE 0 END) as photos,
			SUM(CASE WHEN media_type = 'video' THEN 1 ELSE 0 END) as videos,
			SUM(CASE WHEN media_type = 'document' THEN 1 ELSE 0 END) as documents
		FROM media_stats %s
		GROUP BY chat_id, chat_title
		ORDER BY total DESC
	`, whereClause)

	rows, err := GetDB().Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []ChatStats
	for rows.Next() {
		var cs ChatStats
		if err := rows.Scan(&cs.ChatID, &cs.ChatTitle, &cs.Total, &cs.Photos, &cs.Videos, &cs.Documents); err != nil {
			return nil, err
		}
		result = append(result, cs)
	}

	return result, nil
}

// Report represents a user report
type Report struct {
	ID        int
	UserID    int64
	Username  string
	FirstName string
	ChatID    int64
	ChatTitle string
	Message   string
	Resolved  bool
	CreatedAt time.Time
}

// CreateReport saves a user report to the database
func CreateReport(userID int64, username, firstName string, chatID int64, chatTitle, msg string) (int, error) {
	if !IsDBInitialized() {
		return 0, fmt.Errorf("database not initialized")
	}

	var id int
	query := `INSERT INTO reports (user_id, username, first_name, chat_id, chat_title, message) VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`
	err := GetDB().QueryRow(query, userID, username, firstName, chatID, chatTitle, msg).Scan(&id)

	return id, err
}

// GetOpenReports returns all unresolved reports
func GetOpenReports() ([]*Report, error) {
	if !IsDBInitialized() {
		return nil, nil
	}

	query := `SELECT id, user_id, username, first_name, chat_id, chat_title, message, resolved, created_at FROM reports WHERE resolved = false ORDER BY created_at DESC`
	rows, err := GetDB().Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []*Report
	for rows.Next() {
		var r Report
		if err := rows.Scan(&r.ID, &r.UserID, &r.Username, &r.FirstName, &r.ChatID, &r.ChatTitle, &r.Message, &r.Resolved, &r.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, &r)
	}

	return result, nil
}

// GetReport returns a single report by ID
func GetReport(id int) (*Report, error) {
	if !IsDBInitialized() {
		return nil, fmt.Errorf("database not initialized")
	}

	var r Report
	query := `SELECT id, user_id, username, first_name, chat_id, chat_title, message, resolved, created_at FROM reports WHERE id = $1`
	err := GetDB().QueryRow(query, id).Scan(&r.ID, &r.UserID, &r.Username, &r.FirstName, &r.ChatID, &r.ChatTitle, &r.Message, &r.Resolved, &r.CreatedAt)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("report %d not found", id)
	}

	return &r, err
}

// ResolveReport marks a report as resolved
func ResolveReport(id int) error {
	if !IsDBInitialized() {
		return fmt.Errorf("database not initialized")
	}

	query := `UPDATE reports SET resolved = true WHERE id = $1`
	_, err := GetDB().Exec(query, id)

	return err
}

// BlogPost represents a blog post
type BlogPost struct {
	ID        int
	Title     string
	Content   string
	Published bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// CreateBlogPost creates a new blog post and returns its ID
func CreateBlogPost(title, content string) (int, error) {
	if !IsDBInitialized() {
		return 0, fmt.Errorf("database not initialized")
	}

	var id int
	query := `INSERT INTO blog_posts (title, content) VALUES ($1, $2) RETURNING id`
	err := GetDB().QueryRow(query, title, content).Scan(&id)

	return id, err
}

// GetBlogPost returns a single blog post by ID
func GetBlogPost(id int) (*BlogPost, error) {
	if !IsDBInitialized() {
		return nil, fmt.Errorf("database not initialized")
	}

	var post BlogPost
	query := `SELECT id, title, content, published, created_at, updated_at FROM blog_posts WHERE id = $1`
	err := GetDB().QueryRow(query, id).Scan(&post.ID, &post.Title, &post.Content, &post.Published, &post.CreatedAt, &post.UpdatedAt)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("blog post %d not found", id)
	}

	return &post, err
}

// GetAllBlogPosts returns all blog posts ordered by creation date (newest first)
func GetAllBlogPosts() ([]*BlogPost, error) {
	if !IsDBInitialized() {
		return nil, nil
	}

	query := `SELECT id, title, content, published, created_at, updated_at FROM blog_posts ORDER BY created_at DESC`
	rows, err := GetDB().Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []*BlogPost
	for rows.Next() {
		var post BlogPost
		if err := rows.Scan(&post.ID, &post.Title, &post.Content, &post.Published, &post.CreatedAt, &post.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, &post)
	}

	return result, nil
}

// GetPublishedBlogPosts returns only published blog posts
func GetPublishedBlogPosts() ([]*BlogPost, error) {
	if !IsDBInitialized() {
		return nil, nil
	}

	query := `SELECT id, title, content, published, created_at, updated_at FROM blog_posts WHERE published = true ORDER BY created_at DESC`
	rows, err := GetDB().Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []*BlogPost
	for rows.Next() {
		var post BlogPost
		if err := rows.Scan(&post.ID, &post.Title, &post.Content, &post.Published, &post.CreatedAt, &post.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, &post)
	}

	return result, nil
}

// UpdateBlogPost updates a blog post's title and content
func UpdateBlogPost(id int, title, content string) error {
	if !IsDBInitialized() {
		return fmt.Errorf("database not initialized")
	}

	query := `UPDATE blog_posts SET title = $1, content = $2 WHERE id = $3`
	result, err := GetDB().Exec(query, title, content, id)
	if err != nil {
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rowsAffected == 0 {
		return fmt.Errorf("blog post %d not found", id)
	}

	return nil
}

// ToggleBlogPostPublished toggles the published flag of a blog post
func ToggleBlogPostPublished(id int) error {
	if !IsDBInitialized() {
		return fmt.Errorf("database not initialized")
	}

	query := `UPDATE blog_posts SET published = NOT published WHERE id = $1`
	result, err := GetDB().Exec(query, id)
	if err != nil {
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rowsAffected == 0 {
		return fmt.Errorf("blog post %d not found", id)
	}

	return nil
}

// DeleteBlogPost deletes a blog post by ID
func DeleteBlogPost(id int) error {
	if !IsDBInitialized() {
		return fmt.Errorf("database not initialized")
	}

	query := `DELETE FROM blog_posts WHERE id = $1`
	result, err := GetDB().Exec(query, id)
	if err != nil {
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rowsAffected == 0 {
		return fmt.Errorf("blog post %d not found", id)
	}

	return nil
}

// GetAllDistinctChatIDs returns all unique chat IDs from media_stats (for broadcast to groups)
func GetAllDistinctChatIDs() ([]int64, error) {
	if !IsDBInitialized() {
		return nil, nil
	}

	query := `SELECT DISTINCT chat_id FROM media_stats WHERE chat_id < 0 ORDER BY chat_id`
	rows, err := GetDB().Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []int64
	for rows.Next() {
		var chatID int64
		if err := rows.Scan(&chatID); err != nil {
			return nil, err
		}
		result = append(result, chatID)
	}

	return result, nil
}

// GetAllDistinctUserIDs returns all unique user IDs from media_stats (for broadcast to individual users)
func GetAllDistinctUserIDs() ([]int64, error) {
	if !IsDBInitialized() {
		return nil, nil
	}

	query := `SELECT DISTINCT user_id FROM media_stats WHERE user_id > 0 ORDER BY user_id`
	rows, err := GetDB().Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []int64
	for rows.Next() {
		var userID int64
		if err := rows.Scan(&userID); err != nil {
			return nil, err
		}
		result = append(result, userID)
	}

	return result, nil
}
