package app

import (
	"strings"
	"testing"

	"github.com/timoffmax/social-link-bot/model/api/response"
)

const reportTestBotUsername = "fix_link_test_bot"

// startReportTest records Bot API calls, makes the owner 1 and names the bot.
func startReportTest(t *testing.T) *botAPIRecorder {
	recorder := startTestBotAPI(t, false)
	previousUsername := botUsername
	botUsername = reportTestBotUsername
	t.Setenv("ADMIN_USER_ID", "1")
	t.Cleanup(func() { botUsername = previousUsername })

	return recorder
}

func reportBotMessage(id int, text string) *response.TgMessage {
	result := &response.TgMessage{MessageId: id, Text: text, From: response.TgUser{Id: 99, IsBot: true, Username: reportTestBotUsername}}

	return result
}

func callsTo(calls []botAPICall, chatID float64) []botAPICall {
	var result []botAPICall

	for _, call := range calls {
		if call.body["chat_id"] == chatID {
			result = append(result, call)
		}
	}

	return result
}

func TestBareReportOpensAForcedReplyPrompt(t *testing.T) {
	recorder := startReportTest(t)

	handleReportCommand(&response.TgMessage{MessageId: 5, Text: "/report"}, &response.TgChat{Id: 42, Type: chatTypePrivate}, &response.TgUser{Id: 42})

	calls := recorder.all()
	markup, _ := calls[0].body["reply_markup"].(map[string]interface{})
	text, _ := calls[0].body["text"].(string)

	if (len(calls) != 1) || (markup["force_reply"] != true) || (strings.Contains(text, reportPromptTitle) == false) {
		t.Fatalf("expected the report prompt, got %+v", calls)
	}
}

func TestReplyToThePromptIsAReportEvenWithALink(t *testing.T) {
	recorder := startReportTest(t)
	reply := &response.TgMessage{MessageId: 6, Text: "this reel fails https://www.instagram.com/reel/ABC/", ReplyToMessage: reportBotMessage(5, reportPromptTitle+"\n\nReply to this message")}

	isReport := handleReportPromptReply(reply, &response.TgChat{Id: 42, Type: chatTypePrivate}, &response.TgUser{Id: 42, FirstName: "Ann"})

	calls := recorder.all()
	ownerCalls := callsTo(calls, 1)
	userCalls := callsTo(calls, 42)
	ownerText, _ := ownerCalls[0].body["text"].(string)

	if (isReport == false) || (len(ownerCalls) != 1) || (len(userCalls) != 1) {
		t.Fatalf("expected a report and a thank-you, got %+v", calls)
	}

	if strings.Contains(ownerText, "https://www.instagram.com/reel/ABC/") == false {
		t.Fatalf("expected the link in the report, got %q", ownerText)
	}
}

func TestReplyToAnotherBotMessageIsNotAReport(t *testing.T) {
	recorder := startReportTest(t)
	reply := &response.TgMessage{MessageId: 6, Text: "nice", ReplyToMessage: reportBotMessage(5, "🎬 a video caption")}

	if handleReportPromptReply(reply, &response.TgChat{Id: 42, Type: chatTypePrivate}, &response.TgUser{Id: 42}) == true {
		t.Fatalf("a reply to a normal bot message must not be a report")
	}

	if calls := recorder.all(); len(calls) != 0 {
		t.Fatalf("expected no calls, got %+v", calls)
	}
}

func TestReportInReplyToABotMessageAttachesItsLinks(t *testing.T) {
	recorder := startReportTest(t)
	botMessage := reportBotMessage(7, "🔗 Original post")
	botMessage.CaptionEntities = []response.TgMessageEntity{{Type: "text_link", URL: "https://www.threads.com/@a/post/B"}}
	report := &response.TgMessage{MessageId: 8, Text: "/report", ReplyToMessage: botMessage}

	handleReportCommand(report, &response.TgChat{Id: -100, Type: "supergroup", Title: "Group"}, &response.TgUser{Id: 42})

	ownerCalls := callsTo(recorder.all(), 1)
	ownerText, _ := ownerCalls[0].body["text"].(string)

	if (strings.Contains(ownerText, "https://www.threads.com/@a/post/B") == false) || (strings.Contains(ownerText, reportNoDescriptionText) == false) {
		t.Fatalf("expected the bot message's link in the report, got %q", ownerText)
	}
}

func TestReportFailedButtonReportsTheLinkAndRemovesItself(t *testing.T) {
	recorder := startReportTest(t)
	original := &response.TgMessage{MessageId: 3, Text: "https://www.tiktok.com/@a/video/1"}
	errorMessage := response.TgMessage{MessageId: 4, Date: 1, Text: "❌ Failed to download media from https://www.tiktok.com/@a/video/1", Chat: response.TgChat{Id: -100, Type: "supergroup", Title: "Group"}, ReplyToMessage: original}

	handleReportFailedCallback(&response.TgCallbackQuery{Id: "c1", From: response.TgUser{Id: 42}, Message: errorMessage, Data: reportFailedCallback})

	calls := recorder.all()
	ownerCalls := callsTo(calls, 1)
	ownerText, _ := ownerCalls[0].body["text"].(string)
	edit := calls[len(calls)-1]
	editText, _ := edit.body["text"].(string)

	if strings.Contains(ownerText, "message: https://www.tiktok.com/@a/video/1") == false {
		t.Fatalf("expected the user's link in the report, got %q", ownerText)
	}

	if (edit.method != "editMessageText") || (edit.body["reply_markup"] != nil) || (strings.HasSuffix(editText, reportFailedDoneSuffix) == false) {
		t.Fatalf("expected the button to be replaced by a thank-you, got %+v", edit)
	}
}

func TestReportFailedButtonFilesOneReportWhenTappedTwice(t *testing.T) {
	recorder := startReportTest(t)
	errorMessage := response.TgMessage{MessageId: 41, Date: 1, Text: "❌ Failed", Chat: response.TgChat{Id: -101, Type: "supergroup"}}
	query := &response.TgCallbackQuery{Id: "c1", From: response.TgUser{Id: 42}, Message: errorMessage, Data: reportFailedCallback}

	handleReportFailedCallback(query)
	handleReportFailedCallback(query)

	if ownerCalls := callsTo(recorder.all(), 1); len(ownerCalls) != 1 {
		t.Fatalf("expected one report, got %+v", ownerCalls)
	}
}

func TestReportFailedButtonOnAnInaccessibleMessageIsIgnored(t *testing.T) {
	recorder := startReportTest(t)
	query := &response.TgCallbackQuery{Id: "c1", From: response.TgUser{Id: 42}, Message: response.TgMessage{MessageId: 42, Chat: response.TgChat{Id: -102}}, Data: reportFailedCallback}

	handleReportFailedCallback(query)

	if calls := recorder.all(); len(calls) != 0 {
		t.Fatalf("expected no report, got %+v", calls)
	}
}

func TestMenuReportPromptMentionsTheUserOnlyInGroups(t *testing.T) {
	recorder := startReportTest(t)

	sendReportPrompt(&response.TgChat{Id: 42, Type: chatTypePrivate}, &response.TgUser{Id: 42, FirstName: "Ann", Username: "ann"}, 0)
	sendReportPrompt(&response.TgChat{Id: -100, Type: "supergroup"}, &response.TgUser{Id: 42, FirstName: "Ann", Username: "ann"}, 0)

	calls := recorder.all()
	privateText, _ := calls[0].body["text"].(string)
	groupText, _ := calls[1].body["text"].(string)

	if (strings.Contains(privateText, "@ann") == true) || (strings.Contains(groupText, "@ann: ") == false) {
		t.Fatalf("expected a mention only in the group, got %q / %q", privateText, groupText)
	}
}

func TestReplyToThePromptCountsWithoutTheBotUsername(t *testing.T) {
	recorder := startReportTest(t)
	botUsername = ""
	reply := &response.TgMessage{MessageId: 6, Text: "broken", ReplyToMessage: reportBotMessage(5, reportPromptTitle)}

	if handleReportPromptReply(reply, &response.TgChat{Id: 42, Type: chatTypePrivate}, &response.TgUser{Id: 42}) == false {
		t.Fatalf("expected a report while the bot username is unknown")
	}

	if ownerCalls := callsTo(recorder.all(), 1); len(ownerCalls) != 1 {
		t.Fatalf("expected the report to reach the owner, got %+v", ownerCalls)
	}
}
