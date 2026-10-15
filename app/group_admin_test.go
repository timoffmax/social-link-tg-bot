package app

import (
	"errors"
	"testing"

	"github.com/timoffmax/social-link-bot/model/api/response"
)

func stubChatAdmin(t *testing.T, isAdmin bool, err error) {
	previous := isChatAdmin
	isChatAdmin = func(int64, int64) (bool, error) { return isAdmin, err }
	t.Cleanup(func() { isChatAdmin = previous })
}

func TestGroupSettingsButtonsAreAdminOnly(t *testing.T) {
	for _, data := range []string{"/group_settings", "/toggle_platform_tiktok", "/setgrouplimit_5", reportButtonToggleCallback, supportButtonToggleCallback} {
		if isGroupAdminCallback(data) == false {
			t.Fatalf("expected %q to need an admin", data)
		}
	}

	for _, data := range []string{"/stats", donateCommand, reportPostCallback, "/help"} {
		if isGroupAdminCallback(data) == true {
			t.Fatalf("expected %q to be open to everyone", data)
		}
	}
}

func TestNonAdminIsRefusedWithAToastAndNothingChanges(t *testing.T) {
	recorder := startTestBotAPI(t, false)
	stubChatAdmin(t, false, nil)
	stubGroupButtons(t, true, true)
	isChanged := false
	previousSet := setGroupReportEnabled
	setGroupReportEnabled = func(int64, bool) error {
		isChanged = true
		return nil
	}
	t.Cleanup(func() { setGroupReportEnabled = previousSet })

	handleCallbackQuery(&response.TgCallbackQuery{Id: "c1", From: response.TgUser{Id: 7}, Message: response.TgMessage{Chat: response.TgChat{Id: -904, Type: "supergroup"}}, Data: reportButtonToggleCallback})

	calls := recorder.all()

	if (isChanged == true) || (len(calls) != 1) || (calls[0].body["text"] != groupAdminOnlyText) {
		t.Fatalf("expected a refusal toast and no change, got %+v", calls)
	}
}

func TestFailedAdminCheckRefuses(t *testing.T) {
	stubChatAdmin(t, true, errors.New("timeout"))

	if canManageGroup(&response.TgChat{Id: -905, Type: "supergroup"}, 7) == true {
		t.Fatalf("a failed admin check must refuse")
	}

	if canManageGroup(&response.TgChat{Id: 7, Type: chatTypePrivate}, 7) == false {
		t.Fatalf("private chats need no admin check")
	}
}

func TestNonAdminGroupCommandIsRefused(t *testing.T) {
	recorder := startTestBotAPI(t, false)
	stubChatAdmin(t, false, nil)

	handleBotCommand(&response.TgMessage{MessageId: 5, Text: "/setgrouplimit 5"}, &response.TgChat{Id: -906, Type: "supergroup"}, &response.TgUser{Id: 7})

	calls := recorder.all()

	if (len(calls) != 1) || (calls[0].body["text"] != groupAdminOnlyText) {
		t.Fatalf("expected only the refusal, got %+v", calls)
	}
}

func TestAnonymousGroupAdminCommandIsNotRefused(t *testing.T) {
	recorder := startTestBotAPI(t, false)
	stubChatAdmin(t, false, nil)
	group := &response.TgChat{Id: -907, Type: "supergroup"}
	command := &response.TgMessage{MessageId: 5, Text: "/setgrouplimit", SenderChat: *group}

	handleBotCommand(command, group, &response.TgUser{Id: 1087968824, IsBot: true})

	for _, call := range recorder.all() {
		if call.body["text"] == groupAdminOnlyText {
			t.Fatalf("an anonymous admin must not be refused, got %+v", recorder.all())
		}
	}
}
