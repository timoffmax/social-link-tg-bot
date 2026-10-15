package app

import (
	"errors"
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
	previousCreate := createReport
	previousClaims := reportClaims
	previousReplies := reportReplies
	reportClaims = newTTLStore(reportClaimTTL)
	reportReplies = newTTLStore(reportReplyTTL)
	nextReportID := 0
	createReport = func(int64, string, string, int64, string, string) (int, error) {
		nextReportID++
		return nextReportID, nil
	}
	t.Cleanup(func() {
		botUsername = previousUsername
		createReport = previousCreate
		reportClaims = previousClaims
		reportReplies = previousReplies
	})

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
	rememberReportReply(42, reportBotMessage(5, ""), 42, 0)

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
	rememberReportReply(42, reportBotMessage(5, ""), 42, 0)

	if handleReportPromptReply(reply, &response.TgChat{Id: 42, Type: chatTypePrivate}, &response.TgUser{Id: 42}) == false {
		t.Fatalf("expected a report while the bot username is unknown")
	}

	if ownerCalls := callsTo(recorder.all(), 1); len(ownerCalls) != 1 {
		t.Fatalf("expected the report to reach the owner, got %+v", ownerCalls)
	}
}

func TestReportPostButtonReportsThePostOnceAndAsksForDetails(t *testing.T) {
	recorder := startReportTest(t)
	original := &response.TgMessage{MessageId: 3, Text: "https://www.tiktok.com/@a/video/2"}
	post := response.TgMessage{MessageId: 9, Date: 1, Caption: "🎵 Author", Chat: response.TgChat{Id: 42, Type: chatTypePrivate}, ReplyToMessage: original}
	post.CaptionEntities = []response.TgMessageEntity{{Type: "text_link", URL: "https://www.tiktok.com/@a/video/2"}}
	query := &response.TgCallbackQuery{Id: "c1", From: response.TgUser{Id: 42}, Message: post, Data: reportPostCallback}

	handleReportPostCallback(query)
	handleReportPostCallback(query)

	calls := recorder.all()
	ownerCalls := callsTo(calls, 1)
	userCalls := callsTo(calls, 42)
	ownerText, _ := ownerCalls[0].body["text"].(string)
	followUp, _ := userCalls[0].body["text"].(string)

	if (len(ownerCalls) != 1) || (strings.Contains(ownerText, reportPostTitle) == false) || (strings.Contains(ownerText, "Links: https://www.tiktok.com/@a/video/2") == false) {
		t.Fatalf("expected one report with the post's link, got %+v", ownerCalls)
	}

	if (len(userCalls) != 1) || (strings.Contains(followUp, reportFollowUpTitle) == false) {
		t.Fatalf("expected one follow-up, got %+v", userCalls)
	}
}

func TestReplyToTheFollowUpIsFiledAsAReport(t *testing.T) {
	recorder := startReportTest(t)
	reply := &response.TgMessage{MessageId: 11, Text: "no sound", ReplyToMessage: reportBotMessage(10, reportFollowUpTitle+"\n\nAnything to add?")}
	rememberReportReply(42, reportBotMessage(10, ""), 42, 3)

	if handleReportPromptReply(reply, &response.TgChat{Id: 42, Type: chatTypePrivate}, &response.TgUser{Id: 42}) == false {
		t.Fatalf("expected the details to be filed")
	}

	if ownerCalls := callsTo(recorder.all(), 1); len(ownerCalls) != 1 {
		t.Fatalf("expected the details to reach the owner, got %+v", ownerCalls)
	}
}

func TestReportPostFollowUpMentionsTheReporterInGroups(t *testing.T) {
	recorder := startReportTest(t)
	post := response.TgMessage{MessageId: 12, Date: 1, Caption: "x", Chat: response.TgChat{Id: -100, Type: "supergroup", Title: "G"}}

	handleReportPostCallback(&response.TgCallbackQuery{Id: "c1", From: response.TgUser{Id: 42, Username: "ann"}, Message: post, Data: reportPostCallback})

	groupCalls := callsTo(recorder.all(), -100)
	followUp, _ := groupCalls[0].body["text"].(string)

	if (len(groupCalls) != 1) || (strings.Contains(followUp, "@ann: ") == false) {
		t.Fatalf("expected a follow-up mentioning the reporter, got %+v", groupCalls)
	}
}

func TestDetailsRepliedToTheFollowUpNameTheReport(t *testing.T) {
	recorder := startReportTest(t)
	followUp := reportBotMessage(13, reportFollowUpTitle+"\n\nReport #57. Anything to add? Reply to this message.")
	reply := &response.TgMessage{MessageId: 14, Text: "no sound", ReplyToMessage: followUp}
	rememberReportReply(42, followUp, 42, 57)

	handleReportPromptReply(reply, &response.TgChat{Id: 42, Type: chatTypePrivate}, &response.TgUser{Id: 42})

	ownerCalls := callsTo(recorder.all(), 1)
	ownerText, _ := ownerCalls[0].body["text"].(string)

	if strings.Contains(ownerText, "Details for report #57") == false {
		t.Fatalf("expected the details to name the report, got %q", ownerText)
	}
}

func TestTwoUsersCanReportTheSamePost(t *testing.T) {
	recorder := startReportTest(t)
	post := response.TgMessage{MessageId: 15, Date: 1, Caption: "x", Chat: response.TgChat{Id: -103, Type: "supergroup"}}

	handleReportPostCallback(&response.TgCallbackQuery{Id: "c1", From: response.TgUser{Id: 42}, Message: post, Data: reportPostCallback})
	handleReportPostCallback(&response.TgCallbackQuery{Id: "c2", From: response.TgUser{Id: 43}, Message: post, Data: reportPostCallback})

	if ownerCalls := callsTo(recorder.all(), 1); len(ownerCalls) != 2 {
		t.Fatalf("expected a report from each user, got %+v", ownerCalls)
	}
}

func TestOnlyTheReporterRepliesToTheirFollowUpOnce(t *testing.T) {
	recorder := startReportTest(t)
	followUp := reportBotMessage(20, reportFollowUpTitle)
	group := &response.TgChat{Id: -110, Type: "supergroup"}
	rememberReportReply(group.Id, followUp, 42, 9)
	other := &response.TgMessage{MessageId: 21, Text: "https://www.tiktok.com/@a/video/3", ReplyToMessage: followUp}
	first := &response.TgMessage{MessageId: 22, Text: "no sound", ReplyToMessage: followUp}
	second := &response.TgMessage{MessageId: 23, Text: "lol", ReplyToMessage: followUp}

	if handleReportPromptReply(other, group, &response.TgUser{Id: 43}) == true {
		t.Fatalf("another member's reply must be handled normally")
	}

	if handleReportPromptReply(first, group, &response.TgUser{Id: 42}) == false {
		t.Fatalf("expected the reporter's details to be filed")
	}

	handleReportPromptReply(second, group, &response.TgUser{Id: 42})

	if ownerCalls := callsTo(recorder.all(), 1); len(ownerCalls) != 1 {
		t.Fatalf("only one reply is filed as details, got %+v", ownerCalls)
	}

	for _, call := range callsTo(recorder.all(), float64(group.Id)) {
		if call.body["text"] == reportPromptExpiredText {
			t.Fatalf("group replies to an answered follow-up are handled normally, got %+v", call)
		}
	}
}

func TestUnsavedPostReportCanBeRetried(t *testing.T) {
	recorder := startReportTest(t)
	createReport = func(int64, string, string, int64, string, string) (int, error) {
		return 0, errors.New("db down")
	}
	post := response.TgMessage{MessageId: 24, Date: 1, Caption: "x", Chat: response.TgChat{Id: 42, Type: chatTypePrivate}}
	query := &response.TgCallbackQuery{Id: "c1", From: response.TgUser{Id: 42}, Message: post, Data: reportPostCallback}

	handleReportPostCallback(query)
	handleReportPostCallback(query)

	toasts := 0

	for _, call := range recorder.all() {
		if (call.method == "answerCallbackQuery") && (call.body["text"] == reportPostFailedToast) {
			toasts++
		}
	}

	if toasts != 2 {
		t.Fatalf("expected both taps to try again and report the failure, got %+v", recorder.all())
	}
}

func TestUnsavedReplyKeepsThePromptWaiting(t *testing.T) {
	recorder := startReportTest(t)
	createReport = func(int64, string, string, int64, string, string) (int, error) {
		return 0, errors.New("db down")
	}
	prompt := reportBotMessage(30, reportPromptTitle)
	rememberReportReply(42, prompt, 42, 0)
	reply := &response.TgMessage{MessageId: 31, Text: "broken", ReplyToMessage: prompt}

	handleReportPromptReply(reply, &response.TgChat{Id: 42, Type: chatTypePrivate}, &response.TgUser{Id: 42})

	userCalls := callsTo(recorder.all(), 42)
	answer, _ := userCalls[0].body["text"].(string)

	if answer != reportSaveFailedText {
		t.Fatalf("expected the failure answer, got %q", answer)
	}

	if _, isWaiting := reportReplies.get(reportReplyKey(42, 30)); isWaiting == false {
		t.Fatalf("the prompt must keep waiting for a retry")
	}
}

func TestReplyToAPromptFromBeforeARestartIsAnswered(t *testing.T) {
	recorder := startReportTest(t)
	reply := &response.TgMessage{MessageId: 41, Text: "broken", ReplyToMessage: reportBotMessage(40, reportPromptTitle+"\n\nReply to this message")}

	if handleReportPromptReply(reply, &response.TgChat{Id: 42, Type: chatTypePrivate}, &response.TgUser{Id: 42}) == false {
		t.Fatalf("a reply to an untracked prompt must not be lost in silence")
	}

	userCalls := callsTo(recorder.all(), 42)
	answer, _ := userCalls[0].body["text"].(string)

	if (answer != reportPromptExpiredText) || (len(callsTo(recorder.all(), 1)) != 0) {
		t.Fatalf("expected the expired answer and no report, got %+v", recorder.all())
	}
}
