package app

import (
	"testing"

	"github.com/timoffmax/social-link-bot/model/api/response"
)

// approvalCall records what the approval callback did.
type approvalCall struct {
	decided   bool
	requestID int64
	messageID int64
	isApprove bool
	answer    string
}

func stubApprovalCallback(t *testing.T) *approvalCall {
	call := &approvalCall{}
	previousDecide := decideSettingsApproval
	previousAnswer := answerApprovalCallback
	decideSettingsApproval = func(requestID int64, messageID int64, isApprove bool) string {
		call.decided = true
		call.requestID = requestID
		call.messageID = messageID
		call.isApprove = isApprove

		return "decided"
	}
	answerApprovalCallback = func(callbackQueryID string, text string) {
		call.answer = text
	}
	t.Cleanup(func() {
		decideSettingsApproval = previousDecide
		answerApprovalCallback = previousAnswer
	})

	return call
}

func approvalCallback(fromID int, chatID int64, chatType string, data string) *response.TgCallbackQuery {
	result := &response.TgCallbackQuery{
		Id:      "cb",
		From:    response.TgUser{Id: fromID},
		Message: response.TgMessage{MessageId: 77, Chat: response.TgChat{Id: chatID, Type: chatType}},
		Data:    data,
	}

	return result
}

func TestOwnerApprovalInThePrivateChatIsHandedOver(t *testing.T) {
	t.Setenv("ADMIN_USER_ID", "42")
	call := stubApprovalCallback(t)
	handleSettingsApprovalCallback(approvalCallback(42, 42, chatTypePrivate, "setappr:9"))

	if (call.decided == false) || (call.requestID != 9) || (call.messageID != 77) || (call.isApprove == false) || (call.answer != "decided") {
		t.Fatalf("got %+v", call)
	}

	rejectCall := stubApprovalCallback(t)
	handleSettingsApprovalCallback(approvalCallback(42, 42, chatTypePrivate, "setrej:9"))

	if (rejectCall.decided == false) || (rejectCall.isApprove == true) {
		t.Fatalf("got %+v", rejectCall)
	}
}

func TestApprovalByAnyoneElseOrInAGroupIsRefused(t *testing.T) {
	t.Setenv("ADMIN_USER_ID", "42")
	cases := []*response.TgCallbackQuery{
		approvalCallback(43, 43, chatTypePrivate, "setappr:9"),
		approvalCallback(42, -100123, "supergroup", "setappr:9"),
		approvalCallback(42, -100123, "group", "setrej:9"),
		approvalCallback(43, 42, chatTypePrivate, "setappr:9"),
	}

	for _, callbackQuery := range cases {
		call := stubApprovalCallback(t)
		handleSettingsApprovalCallback(callbackQuery)

		if (call.decided == true) || (call.answer != approvalRefusedText) {
			t.Fatalf("%+v: got %+v", callbackQuery, call)
		}
	}
}

func TestApprovalWithoutAdminIsRefused(t *testing.T) {
	t.Setenv("ADMIN_USER_ID", "")
	call := stubApprovalCallback(t)
	handleSettingsApprovalCallback(approvalCallback(0, 0, chatTypePrivate, "setappr:9"))

	if (call.decided == true) || (call.answer != approvalRefusedText) {
		t.Fatalf("got %+v", call)
	}
}

func TestMalformedApprovalIsNotHandedOver(t *testing.T) {
	t.Setenv("ADMIN_USER_ID", "42")
	call := stubApprovalCallback(t)
	handleSettingsApprovalCallback(approvalCallback(42, 42, chatTypePrivate, "setappr:abc"))

	if (call.decided == true) || (call.answer != approvalMalformedText) {
		t.Fatalf("got %+v", call)
	}
}

func TestApprovalCallbacksAreSelfAnsweredAndNotGroupAdminOnes(t *testing.T) {
	for _, data := range []string{"setappr:1", "setrej:1"} {
		if (isSelfAnsweredCallback(data) == false) || (isGroupAdminCallback(data) == true) {
			t.Fatalf("%s: self answered %v, group admin %v", data, isSelfAnsweredCallback(data), isGroupAdminCallback(data))
		}
	}
}

func TestApprovalKeyboardIsOneRowOfCallbackButtons(t *testing.T) {
	keyboard := approvalKeyboard(nil)

	if (len(keyboard.InlineKeyboard) != 1) || (len(keyboard.InlineKeyboard[0]) != 0) {
		t.Fatalf("got %+v", keyboard)
	}
}
