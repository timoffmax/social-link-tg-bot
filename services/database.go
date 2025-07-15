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
		log.Fatal("Database service not initialized")
	}
	return dbService.db
}

func CloseDatabase() error {
	if dbService != nil && dbService.db != nil {
		return dbService.db.Close()
	}
	return nil
}

type MediaStat struct {
	ID        int64     `json:"id"`
	UserID    int64     `json:"user_id"`
	Username  string    `json:"username"`
	ChatID    int64     `json:"chat_id"`
	ChatTitle string    `json:"chat_title"`
	Platform  string    `json:"platform"`
	MediaType string    `json:"media_type"`
	URL       string    `json:"url"`
	CreatedAt time.Time `json:"created_at"`
}

type UserMediaLimit struct {
	ID         int64     `json:"id"`
	ChatID     int64     `json:"chat_id"`
	UserID     int64     `json:"user_id"`
	DailyLimit int       `json:"daily_limit"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// RecordMediaStat records a media send event
func RecordMediaStat(userID int64, username string, chatID int64, chatTitle string, platform string, mediaType string, url string) error {
	query := `
		INSERT INTO media_stats (user_id, username, chat_id, chat_title, platform, media_type, url)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`
	_, err := GetDB().Exec(query, userID, username, chatID, chatTitle, platform, mediaType, url)
	return err
}

// GetDailyMediaCount returns the number of media items sent by a user today
func GetDailyMediaCount(userID int64, chatID int64) (int, error) {
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
	db := GetDB()
	if db == nil {
		return fmt.Errorf("database not initialized")
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

	_, err := db.Exec(query, chatID, chatTitle, limit)
	return err
}

// GetGroupMediaLimit gets the default daily media limit for a group
func GetGroupMediaLimit(chatID int64) (int, error) {
	db := GetDB()
	if db == nil {
		return 0, fmt.Errorf("database not initialized")
	}

	var limit int
	query := `SELECT default_daily_limit FROM group_media_limits WHERE chat_id = $1`
	err := db.QueryRow(query, chatID).Scan(&limit)
	
	if err == sql.ErrNoRows {
		return 0, nil // No group limit set
	}
	
	return limit, err
}

// SetGroupTimezone sets the timezone for a group
func SetGroupTimezone(chatID int64, chatTitle string, timezone string) error {
	db := GetDB()
	if db == nil {
		return fmt.Errorf("database not initialized")
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

	_, err := db.Exec(query, chatID, chatTitle, timezone)
	return err
}

// GetGroupTimezone gets the timezone for a group
func GetGroupTimezone(chatID int64) (string, error) {
	db := GetDB()
	if db == nil {
		return "UTC", fmt.Errorf("database not initialized")
	}

	var timezone string
	query := `SELECT timezone FROM group_media_limits WHERE chat_id = $1`
	err := db.QueryRow(query, chatID).Scan(&timezone)
	
	if err == sql.ErrNoRows {
		return "UTC", nil // Default to UTC if no timezone set
	}
	
	return timezone, err
}

// GetGroupLimitInfo gets complete group limit information
func GetGroupLimitInfo(chatID int64) (*GroupLimitInfo, error) {
	db := GetDB()
	if db == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	var info GroupLimitInfo
	query := `SELECT chat_id, chat_title, default_daily_limit, timezone FROM group_media_limits WHERE chat_id = $1`
	err := db.QueryRow(query, chatID).Scan(&info.ChatID, &info.ChatTitle, &info.DefaultDailyLimit, &info.Timezone)
	
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
	
	// Load timezone
	loc, err := time.LoadLocation(timezoneStr)
	if err != nil {
		log.Printf("Invalid timezone %s for chat %d, falling back to UTC", timezoneStr, chatID)
		loc = time.UTC
	}
	
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

	rows, err := db.Query(query, chatID)
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

// GetTopUsersMediaStats gets top N users by media count for a specific period
func GetTopUsersMediaStats(chatID int64, period string, limit int) ([]UserStats, error) {
	allUsers, err := GetUserMediaStats(chatID, period)
	if err != nil {
		return nil, err
	}

	// Return top N users
	if len(allUsers) > limit {
		return allUsers[:limit], nil
	}
	return allUsers, nil
}