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