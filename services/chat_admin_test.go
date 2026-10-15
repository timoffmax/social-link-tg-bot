package services

import (
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

func TestGatedFetchIsConfiguredWithoutABotWideCap(t *testing.T) {
	t.Setenv("APIFY_TOKEN", "test-token")
	t.Setenv("GATED_FETCH_GLOBAL_MONTHLY_CAP", "")

	if IsGatedFetchConfigured() == false {
		t.Fatalf("no bot-wide cap (the default) must keep restricted posts on")
	}

	t.Setenv("GATED_FETCH_GLOBAL_MONTHLY_CAP", "0")

	if IsGatedFetchConfigured() == true {
		t.Fatalf("a cap of 0 turns restricted posts off")
	}
}

func TestRestrictedPostsAreNotOfferedWithZeroDefaults(t *testing.T) {
	t.Setenv("APIFY_TOKEN", "test-token")
	t.Setenv("GATED_FETCH_USER_MONTHLY_ALLOWANCE", "0")
	t.Setenv("GATED_FETCH_CHAT_MONTHLY_CAP", "0")

	if IsRestrictedPostsOffered() == true {
		t.Fatalf("zero default limits must not offer restricted posts")
	}

	t.Setenv("GATED_FETCH_GLOBAL_MONTHLY_CAP", "-1")
	t.Setenv("GATED_FETCH_CHAT_MONTHLY_CAP", "5")

	if IsRestrictedPostsOffered() == false {
		t.Fatalf("an explicit -1 bot-wide cap means no cap, not off")
	}
}
