package services

import (
	"encoding/json"
	"fmt"
	"log"
	"strconv"
)

// ChatSettings represents all settings for a chat
type ChatSettings struct {
	ChatID   int64                             `db:"chat_id"`
	Settings map[string]map[string]interface{} // settingType -> (settingKey -> value)
}

// ChatSetting represents a single setting
type ChatSetting struct {
	ID           int64  `db:"id"`
	ChatID       int64  `db:"chat_id"`
	SettingType  string `db:"setting_type"`
	SettingKey   string `db:"setting_key"`
	SettingValue string `db:"setting_value"`
	Enabled      bool   `db:"enabled"`
}

// GetChatSettings retrieves all settings for a chat
func GetChatSettings(chatID int64) (*ChatSettings, error) {
	db := GetDB()
	if db == nil {
		return nil, fmt.Errorf("database not available")
	}

	settings := &ChatSettings{
		ChatID:   chatID,
		Settings: make(map[string]map[string]interface{}),
	}

	// Get all settings for this chat
	rows, err := db.Query(`
		SELECT setting_type, setting_key, setting_value, enabled 
		FROM chat_settings 
		WHERE chat_id = $1
	`, chatID)

	if err != nil {
		return nil, fmt.Errorf("failed to query chat settings: %v", err)
	}
	defer rows.Close()

	// Initialize platform settings with defaults if no settings exist
	if _, exists := settings.Settings["platform"]; !exists {
		settings.Settings["platform"] = make(map[string]interface{})
		defaultPlatforms := []string{"instagram", "tiktok", "twitter", "youtube", "reddit", "facebook"}
		for _, platform := range defaultPlatforms {
			settings.Settings["platform"][platform] = true // Default to enabled
		}
	}

	// Override with database settings
	for rows.Next() {
		var settingType, settingKey, settingValue string
		var enabled bool
		if err := rows.Scan(&settingType, &settingKey, &settingValue, &enabled); err != nil {
			log.Printf("Error scanning chat settings: %v", err)
			continue
		}

		// Initialize setting type map if not exists
		if _, exists := settings.Settings[settingType]; !exists {
			settings.Settings[settingType] = make(map[string]interface{})
		}

		// Parse value based on enabled flag (for platform settings, use enabled flag)
		if settingType == "platform" {
			settings.Settings[settingType][settingKey] = enabled
		} else {
			// Try to parse as different types
			if val, err := strconv.ParseBool(settingValue); err == nil {
				settings.Settings[settingType][settingKey] = val
			} else if val, err := strconv.ParseInt(settingValue, 10, 64); err == nil {
				settings.Settings[settingType][settingKey] = val
			} else if val, err := strconv.ParseFloat(settingValue, 64); err == nil {
				settings.Settings[settingType][settingKey] = val
			} else {
				// Try JSON
				var jsonVal interface{}
				if err := json.Unmarshal([]byte(settingValue), &jsonVal); err == nil {
					settings.Settings[settingType][settingKey] = jsonVal
				} else {
					// Fallback to string
					settings.Settings[settingType][settingKey] = settingValue
				}
			}
		}
	}

	return settings, nil
}

// SetChatSetting sets a generic setting for a chat
func SetChatSetting(chatID int64, settingType, settingKey string, value interface{}) error {
	db := GetDB()
	if db == nil {
		return fmt.Errorf("database not available")
	}

	var settingValue string
	var enabled bool = true

	// Handle platform settings specially
	if settingType == "platform" {
		if boolVal, ok := value.(bool); ok {
			enabled = boolVal
			settingValue = strconv.FormatBool(boolVal)
		} else {
			return fmt.Errorf("platform settings must be boolean")
		}
	} else {
		// For other settings, serialize to JSON or string
		switch v := value.(type) {
		case bool:
			settingValue = strconv.FormatBool(v)
		case int, int32, int64:
			settingValue = fmt.Sprintf("%d", v)
		case float32, float64:
			settingValue = fmt.Sprintf("%f", v)
		case string:
			settingValue = v
		default:
			// Serialize to JSON for complex types
			jsonBytes, err := json.Marshal(v)
			if err != nil {
				return fmt.Errorf("failed to serialize setting value: %v", err)
			}
			settingValue = string(jsonBytes)
		}
	}

	// Upsert setting
	_, err := db.Exec(`
		INSERT INTO chat_settings (chat_id, setting_type, setting_key, setting_value, enabled) 
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (chat_id, setting_type, setting_key) 
		DO UPDATE SET setting_value = $4, enabled = $5, updated_at = CURRENT_TIMESTAMP
	`, chatID, settingType, settingKey, settingValue, enabled)

	if err != nil {
		return fmt.Errorf("failed to update setting: %v", err)
	}

	log.Printf("⚙️ Setting %s.%s = %v for chat %d", settingType, settingKey, value, chatID)
	return nil
}

// SetChatPlatformEnabled is a convenience function for platform settings
func SetChatPlatformEnabled(chatID int64, platform string, enabled bool) error {
	return SetChatSetting(chatID, "platform", platform, enabled)
}

// GetChatSetting gets a specific setting value
func GetChatSetting(chatID int64, settingType, settingKey string) (interface{}, bool) {
	settings, err := GetChatSettings(chatID)
	if err != nil {
		log.Printf("Error getting chat settings: %v", err)
		return nil, false
	}

	if typeSettings, exists := settings.Settings[settingType]; exists {
		if value, exists := typeSettings[settingKey]; exists {
			return value, true
		}
	}

	return nil, false
}

// IsPlatformEnabledForChat checks if a platform is enabled for a specific chat
func IsPlatformEnabledForChat(chatID int64, platform string) bool {
	// For private chats (positive chat_id), always use global settings
	if chatID > 0 {
		return true
	}

	value, exists := GetChatSetting(chatID, "platform", platform)
	if !exists {
		return true // Default to enabled if not set
	}

	if enabled, ok := value.(bool); ok {
		return enabled
	}

	return true // Default to enabled on parse error
}

// GetChatPlatformSettings returns all platform settings for a chat (convenience function)
func GetChatPlatformSettings(chatID int64) (map[string]bool, error) {
	settings, err := GetChatSettings(chatID)
	if err != nil {
		return nil, err
	}

	platformSettings := make(map[string]bool)
	if platforms, exists := settings.Settings["platform"]; exists {
		for key, value := range platforms {
			if enabled, ok := value.(bool); ok {
				platformSettings[key] = enabled
			}
		}
	}

	return platformSettings, nil
}

// GetAllChatPlatformStats returns platform usage statistics
func GetAllChatPlatformStats() (map[string]int, error) {
	db := GetDB()
	if db == nil {
		return nil, fmt.Errorf("database not available")
	}

	stats := make(map[string]int)

	rows, err := db.Query(`
		SELECT setting_key, COUNT(*) as chat_count
		FROM chat_settings 
		WHERE setting_type = 'platform' AND enabled = true
		GROUP BY setting_key
		ORDER BY setting_key
	`)

	if err != nil {
		return nil, fmt.Errorf("failed to query platform stats: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var platform string
		var count int
		if err := rows.Scan(&platform, &count); err != nil {
			log.Printf("Error scanning platform stats: %v", err)
			continue
		}
		stats[platform] = count
	}

	return stats, nil
}

// InitializeChatSettings creates default settings for a new chat
func InitializeChatSettings(chatID int64) error {
	db := GetDB()
	if db == nil {
		return fmt.Errorf("database not available")
	}

	defaultPlatforms := []string{"instagram", "tiktok", "twitter", "youtube", "reddit", "facebook"}

	// Start transaction
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to start transaction: %v", err)
	}
	defer tx.Rollback()

	// Insert default enabled platforms
	for _, platform := range defaultPlatforms {
		_, err = tx.Exec(`
			INSERT INTO chat_settings (chat_id, setting_type, setting_key, setting_value, enabled) 
			VALUES ($1, 'platform', $2, 'true', true)
			ON CONFLICT (chat_id, setting_type, setting_key) DO NOTHING
		`, chatID, platform)

		if err != nil {
			return fmt.Errorf("failed to initialize platform %s: %v", platform, err)
		}
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %v", err)
	}

	log.Printf("🔧 Initialized settings for chat %d", chatID)
	return nil
}

// Description settings functions

// SetChatDescriptionEnabled sets description visibility for a specific platform in a chat
func SetChatDescriptionEnabled(chatID int64, platform string, enabled bool) error {
	return SetChatSetting(chatID, "description", platform, enabled)
}

// IsDescriptionEnabledForChat checks if descriptions are enabled for a specific platform in a chat
func IsDescriptionEnabledForChat(chatID int64, platform string) bool {
	// For private chats (positive chat_id), always show descriptions by default
	if chatID > 0 {
		return true
	}

	value, exists := GetChatSetting(chatID, "description", platform)
	if !exists {
		return true // Default to enabled if not set
	}

	if enabled, ok := value.(bool); ok {
		return enabled
	}

	return true // Default to enabled on parse error
}

// GetChatDescriptionSettings returns all description settings for a chat (convenience function)
func GetChatDescriptionSettings(chatID int64) (map[string]bool, error) {
	settings, err := GetChatSettings(chatID)
	if err != nil {
		return nil, err
	}

	descriptionSettings := make(map[string]bool)
	if descriptions, exists := settings.Settings["description"]; exists {
		for key, value := range descriptions {
			if enabled, ok := value.(bool); ok {
				descriptionSettings[key] = enabled
			}
		}
	}

	return descriptionSettings, nil
}
