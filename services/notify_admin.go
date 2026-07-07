package services

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/timoffmax/social-link-bot/config"
)

// adminAlertInterval is the minimum time between admin alerts with the same key.
const adminAlertInterval = 6 * time.Hour

// adminAlertMaxLen keeps alert texts under Telegram's 4096-char sendMessage
// limit even after JSON encoding, so oversized error details can't make the
// alert undeliverable.
const adminAlertMaxLen = 3500

var (
	adminAlertMu   sync.Mutex
	adminAlertLast = make(map[string]time.Time)
)

// NotifyAdmin sends a plain-text Telegram message to the configured admin,
// rate-limited to one message per alertKey per 6 hours. Returns true when the
// message was actually sent.
func NotifyAdmin(alertKey, text string) bool {
	result := false

	token := config.GetTgBotToken()
	adminID := config.GetAdminUserID()

	if (token == "") || (adminID == 0) {
		return result
	}

	// Reserve the rate-limit slot atomically so concurrent callers can't send
	// duplicates; the reservation is rolled back if the send fails.
	adminAlertMu.Lock()
	last, seen := adminAlertLast[alertKey]

	if seen && (time.Since(last) < adminAlertInterval) {
		adminAlertMu.Unlock()

		return result
	}

	adminAlertLast[alertKey] = time.Now()
	adminAlertMu.Unlock()

	if len(text) > adminAlertMaxLen {
		text = truncateAtRuneBoundary(text, adminAlertMaxLen) + "…"
	}

	body, err := json.Marshal(map[string]interface{}{
		"chat_id":                  adminID,
		"text":                     text,
		"disable_web_page_preview": true,
	})

	if err != nil {
		rollbackAdminAlert(alertKey)

		return result
	}

	resp, err := SharedHTTPClient.Post("https://api.telegram.org/bot"+token+"/sendMessage", "application/json", strings.NewReader(string(body)))

	if err != nil {
		log.Printf("❌ Failed to send admin alert %q: %v", alertKey, err)
		rollbackAdminAlert(alertKey)

		return result
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("❌ Admin alert %q rejected by Telegram: HTTP %d", alertKey, resp.StatusCode)
		rollbackAdminAlert(alertKey)

		return result
	}

	log.Printf("📣 Admin alert sent: %s", alertKey)

	result = true

	return result
}

// ResetAdminAlert clears the rate-limit stamp for an alert key, so the next
// occurrence alerts immediately (used when a service recovers, so a repeat
// outage within the rate-limit window is not silently missed).
func ResetAdminAlert(alertKey string) {
	adminAlertMu.Lock()
	delete(adminAlertLast, alertKey)
	adminAlertMu.Unlock()
}

// rollbackAdminAlert releases a reserved rate-limit slot after a failed send.
func rollbackAdminAlert(alertKey string) {
	adminAlertMu.Lock()
	delete(adminAlertLast, alertKey)
	adminAlertMu.Unlock()
}

// truncateAtRuneBoundary shortens s to at most maxLen bytes without splitting
// a UTF-8 rune.
func truncateAtRuneBoundary(s string, maxLen int) string {
	result := s

	if len(result) <= maxLen {
		return result
	}

	result = result[:maxLen]

	for (len(result) > 0) && (utf8.ValidString(result) == false) {
		result = result[:len(result)-1]
	}

	return result
}
