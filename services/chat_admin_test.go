package services

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestIsChatAdminAcceptsOnlyCreatorsAndAdministrators(t *testing.T) {
	statuses := map[string]bool{"creator": true, "administrator": true, "member": false, "restricted": false, "left": false}

	for status, expected := range statuses {
		answer := `{"ok":true,"result":{"status":"` + status + `"}}`
		useBotAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(answer))
		})

		isAdmin, err := IsChatAdmin(-100, 7)

		if (err != nil) || (isAdmin != expected) {
			t.Fatalf("%s: expected %t, got %t (%v)", status, expected, isAdmin, err)
		}
	}
}

func TestIsChatAdminRefusesOnTelegramError(t *testing.T) {
	useBotAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"ok":false,"description":"Bad Request: chat not found"}`))
	})

	isAdmin, err := IsChatAdmin(-100, 7)

	if (isAdmin == true) || (err == nil) || (strings.Contains(err.Error(), "chat not found") == false) {
		t.Fatalf("expected a refusal with Telegram's reason, got %t %v", isAdmin, err)
	}
}

func TestChatButtonsAreOnUnlessTurnedOff(t *testing.T) {
	buttons := map[string]interface{}{chatSettingReportButton: false, chatSettingSupportButton: "garbage"}

	if isChatButtonOn(buttons, chatSettingReportButton) == true {
		t.Fatalf("expected the report button off")
	}

	if (isChatButtonOn(buttons, chatSettingSupportButton) == false) || (isChatButtonOn(nil, chatSettingReportButton) == false) {
		t.Fatalf("expected missing or invalid settings to mean on")
	}
}

func TestSpecialDownloadsAreConfiguredWithoutABotWideCap(t *testing.T) {
	t.Setenv("APIFY_TOKEN", "test-token")
	t.Setenv("GATED_FETCH_GLOBAL_MONTHLY_CAP", "")

	if IsSpecialDownloadsConfigured() == false {
		t.Fatalf("no bot-wide cap (the default) must keep special downloads on")
	}

	t.Setenv("GATED_FETCH_GLOBAL_MONTHLY_CAP", "0")

	if IsSpecialDownloadsConfigured() == true {
		t.Fatalf("a cap of 0 turns special downloads off")
	}
}

func TestSpecialDownloadsAreNotOfferedWithZeroDefaults(t *testing.T) {
	markDatabaseOpenForTest(t)
	t.Setenv("APIFY_TOKEN", "test-token")
	t.Setenv("GATED_FETCH_USER_MONTHLY_ALLOWANCE", "0")
	t.Setenv("GATED_FETCH_CHAT_MONTHLY_CAP", "0")

	if IsSpecialDownloadsOffered() == true {
		t.Fatalf("zero default limits must not offer special downloads")
	}

	t.Setenv("GATED_FETCH_GLOBAL_MONTHLY_CAP", "-1")
	t.Setenv("GATED_FETCH_CHAT_MONTHLY_CAP", "5")

	if IsSpecialDownloadsOffered() == false {
		t.Fatalf("an explicit -1 bot-wide cap means no cap, not off")
	}
}

func TestFetchBotDeleteRightNeedsCreatorOrTheDeleteRight(t *testing.T) {
	answers := map[string]bool{
		`{"ok":true,"result":{"status":"creator"}}`:                                   true,
		`{"ok":true,"result":{"status":"administrator","can_delete_messages":true}}`:  true,
		`{"ok":true,"result":{"status":"administrator","can_delete_messages":false}}`: false,
		`{"ok":true,"result":{"status":"member"}}`:                                    false,
	}

	for answer, expected := range answers {
		askedUserID := ""
		useBotAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
			body := map[string]interface{}{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			askedUserID = fmt.Sprint(body["user_id"])
			_, _ = w.Write([]byte(answer))
		})

		canDelete, err := FetchBotDeleteRight(-100)

		if (err != nil) || (canDelete != expected) {
			t.Fatalf("%s: expected %t, got %t (%v)", answer, expected, canDelete, err)
		}

		if askedUserID != "123456" {
			t.Fatalf("expected the bot's own id from the token, got %q", askedUserID)
		}
	}
}

func TestFetchBotDeleteRightIsUnknownOnTelegramError(t *testing.T) {
	useBotAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"ok":false,"description":"Bad Request: chat not found"}`))
	})

	canDelete, err := FetchBotDeleteRight(-100)

	if (canDelete == true) || (err == nil) || (strings.Contains(err.Error(), botAPITestToken) == true) {
		t.Fatalf("expected an unknown answer without the token, got %t %v", canDelete, err)
	}
}

func TestFetchBotDeleteRightIsUnknownWithoutABotIDInTheToken(t *testing.T) {
	useBotAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("no request expected without a bot id")
	})
	t.Setenv("TG_BOT_TOKEN", "not-a-token")

	canDelete, err := FetchBotDeleteRight(-100)

	if (canDelete == true) || (err == nil) || (strings.Contains(err.Error(), "not-a-token") == true) {
		t.Fatalf("expected an unknown answer without the token, got %t %v", canDelete, err)
	}
}
