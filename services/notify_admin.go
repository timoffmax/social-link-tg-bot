package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
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

	result = sendAdminMessage(token, adminID, alertKey, text)

	if result == false {
		rollbackAdminAlert(alertKey)
	}

	return result
}

// unthrottledAlertLabel names unthrottled alerts in the log.
const unthrottledAlertLabel = "unthrottled"

// NotifyAdminUnthrottled sends a plain-text message to the configured admin
// without rate limiting or keeping any state, for one-off events such as a
// payment. Returns true when the message was sent.
func NotifyAdminUnthrottled(text string) bool {
	result := false
	token := config.GetTgBotToken()
	adminID := config.GetAdminUserID()

	if (token != "") && (adminID != 0) {
		result = sendAdminMessage(token, adminID, unthrottledAlertLabel, text)
	}

	return result
}

// sendAdminMessage sends text to the admin; label names the alert in the log.
// Transport errors are logged without the request URL, which holds the token.
func sendAdminMessage(token string, adminID int64, label, text string) bool {
	var resp *http.Response
	url := fmt.Sprintf(botAPIURLTemplate, token, adminSendMethod)

	if len(text) > adminAlertMaxLen {
		text = truncateAtRuneBoundary(text, adminAlertMaxLen) + "…"
	}

	body, err := json.Marshal(map[string]interface{}{
		"chat_id":                  adminID,
		"text":                     text,
		"disable_web_page_preview": true,
	})

	if err == nil {
		reader := bytes.NewReader(body)
		resp, err = SharedHTTPClient.Post(url, "application/json", reader)
		err = withoutRequestURL(adminSendMethod, err)
	}

	if err == nil {
		defer resp.Body.Close()
	}

	if (err == nil) && (resp.StatusCode != http.StatusOK) {
		err = fmt.Errorf("rejected by Telegram: HTTP %d", resp.StatusCode)
	}

	if err != nil {
		log.Printf("❌ Failed to send admin alert %q: %v", label, err)
	}

	if err == nil {
		log.Printf("📣 Admin alert sent: %s", label)
	}

	result := (err == nil)

	return result
}

// adminSendMethod is the Bot API method of admin alerts.
const adminSendMethod = "sendMessage"

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
