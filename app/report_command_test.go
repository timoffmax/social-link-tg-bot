package app

import (
	"errors"
	"strings"
	"testing"
	"time"

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
	previousNext := reportNextMessages
	reportClaims = newTTLStore(reportClaimTTL)
	reportReplies = newTTLStore(reportReplyTTL)
	reportNextMessages = newTTLStore(reportReplyTTL)
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
		reportNextMessages = previousNext
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

// sentMessageID returns the id the fake Bot API gave the n-th call (a
// sendMessage), 0-based.
func sentMessageID(index int) int {
	result := index + 1 + 1000

	return result
}

// lastSendIndex returns the index of the last sendMessage call to the chat.
func lastSendIndex(calls []botAPICall, chatID float64) int {
	result := -1

	for index, call := range calls {
		if (call.method == "sendMessage") && (call.body["chat_id"] == chatID) {
			result = index
		}
	}

	return result
}

// tapReportUnderPost taps 🐛 Report under a post and returns the prompt.
func tapReportUnderPost(t *testing.T, recorder *botAPIRecorder, chat response.TgChat, user response.TgUser, postID int) *response.TgMessage {
	original := &response.TgMessage{MessageId: postID - 1, Text: "https://www.tiktok.com/@a/video/2"}
	post := response.TgMessage{MessageId: postID, Date: 1, Caption: "🎵 Author", Chat: chat, ReplyToMessage: original}
	post.CaptionEntities = []response.TgMessageEntity{{Type: "text_link", URL: "https://www.tiktok.com/@a/video/2"}}

	handleReportPostCallback(&response.TgCallbackQuery{Id: "c1", From: user, Message: post, Data: reportPostCallback})

	index := lastSendIndex(recorder.all(), float64(chat.Id))

	if index < 0 {
		t.Fatalf("expected a prompt, got %+v", recorder.all())
	}

	prompt := reportBotMessage(sentMessageID(index), reportPostPromptTitle)
	prompt.Chat = chat
	prompt.ReplyToMessage = &post

	return prompt
}

func TestReportUnderPostAsksFirstAndSendsNothingYet(t *testing.T) {
	recorder := startReportTest(t)
	tapReportUnderPost(t, recorder, response.TgChat{Id: 42, Type: chatTypePrivate}, response.TgUser{Id: 42}, 10)

	calls := recorder.all()
	prompt := calls[lastSendIndex(calls, 42)]
	markup, _ := prompt.body["reply_markup"].(map[string]interface{})

	if (len(callsTo(calls, 1)) != 0) || (markup == nil) {
		t.Fatalf("expected a prompt with buttons and no report yet, got %+v", calls)
	}
}

func TestNextPrivateMessageIsSentTogetherWithThePost(t *testing.T) {
	recorder := startReportTest(t)
	chat := response.TgChat{Id: 42, Type: chatTypePrivate}
	tapReportUnderPost(t, recorder, chat, response.TgUser{Id: 42}, 10)
	note := &response.TgMessage{MessageId: 30, Text: "the video has no sound"}

	if handleReportPromptReply(note, &chat, &response.TgUser{Id: 42}) == false {
		t.Fatalf("expected the next message to answer the prompt")
	}

	ownerCalls := callsTo(recorder.all(), 1)
	ownerText, _ := ownerCalls[0].body["text"].(string)

	if (len(ownerCalls) != 1) || (strings.Contains(ownerText, "the video has no sound") == false) || (strings.Contains(ownerText, "Links: https://www.tiktok.com/@a/video/2") == false) {
		t.Fatalf("expected one report with the note and the post, got %+v", ownerCalls)
	}

	later := &response.TgMessage{MessageId: 31, Text: "https://www.instagram.com/reel/ABC/"}

	if handleReportPromptReply(later, &chat, &response.TgUser{Id: 42}) == true {
		t.Fatalf("only the first message after the prompt belongs to the report")
	}
}

func TestSendWithoutDetailsSendsThePostAlone(t *testing.T) {
	recorder := startReportTest(t)
	chat := response.TgChat{Id: 42, Type: chatTypePrivate}
	prompt := tapReportUnderPost(t, recorder, chat, response.TgUser{Id: 42}, 10)

	handleReportPromptCallback(&response.TgCallbackQuery{Id: "c2", From: response.TgUser{Id: 42}, Message: *prompt, Data: reportSendCallback})

	ownerCalls := callsTo(recorder.all(), 1)
	ownerText, _ := ownerCalls[0].body["text"].(string)

	if (len(ownerCalls) != 1) || (strings.Contains(ownerText, reportNoDescriptionText) == false) {
		t.Fatalf("expected one report without a note, got %+v", ownerCalls)
	}

	note := &response.TgMessage{MessageId: 30, Text: "late note"}

	if handleReportPromptReply(note, &chat, &response.TgUser{Id: 42}) == true {
		t.Fatalf("a sent report doesn't take more messages")
	}
}

func TestCancelSendsNothingAndAllowsReportingAgain(t *testing.T) {
	recorder := startReportTest(t)
	chat := response.TgChat{Id: 42, Type: chatTypePrivate}
	prompt := tapReportUnderPost(t, recorder, chat, response.TgUser{Id: 42}, 10)

	handleReportPromptCallback(&response.TgCallbackQuery{Id: "c2", From: response.TgUser{Id: 42}, Message: *prompt, Data: reportCancelCallback})

	if len(callsTo(recorder.all(), 1)) != 0 {
		t.Fatalf("a canceled report must not be sent")
	}

	tapReportUnderPost(t, recorder, chat, response.TgUser{Id: 42}, 10)
}

func TestInAGroupOnlyTheReportersReplyIsTheNote(t *testing.T) {
	recorder := startReportTest(t)
	group := response.TgChat{Id: -110, Type: "supergroup", Title: "G"}
	prompt := tapReportUnderPost(t, recorder, group, response.TgUser{Id: 42, Username: "ann"}, 10)
	other := &response.TgMessage{MessageId: 21, Text: "https://www.tiktok.com/@a/video/3", ReplyToMessage: prompt}
	notReply := &response.TgMessage{MessageId: 22, Text: "chatting"}
	answer := &response.TgMessage{MessageId: 23, Text: "no sound", ReplyToMessage: prompt}

	if handleReportPromptReply(other, &group, &response.TgUser{Id: 43}) == true {
		t.Fatalf("another member's reply must be handled normally")
	}

	if handleReportPromptReply(notReply, &group, &response.TgUser{Id: 42}) == true {
		t.Fatalf("in a group only a reply to the prompt counts")
	}

	handleReportPostCallback(&response.TgCallbackQuery{Id: "c3", From: response.TgUser{Id: 43}, Message: *prompt.ReplyToMessage, Data: reportPostCallback})
	handleReportPromptCallback(&response.TgCallbackQuery{Id: "c4", From: response.TgUser{Id: 43}, Message: *prompt, Data: reportSendCallback})

	if handleReportPromptReply(answer, &group, &response.TgUser{Id: 42}) == false {
		t.Fatalf("expected the reporter's reply to be the note")
	}

	ownerCalls := callsTo(recorder.all(), 1)

	if len(ownerCalls) != 1 {
		t.Fatalf("expected only the reporter's report, got %+v", ownerCalls)
	}

	groupCalls := callsTo(recorder.all(), float64(group.Id))
	promptText, _ := groupCalls[0].body["text"].(string)

	if strings.Contains(promptText, "@ann: ") == false {
		t.Fatalf("expected the prompt to mention the reporter, got %q", promptText)
	}
}

func TestUndeliveredNoteKeepsThePromptWaiting(t *testing.T) {
	recorder := startReportTest(t)
	t.Setenv("ADMIN_USER_ID", "")
	createReport = func(int64, string, string, int64, string, string) (int, error) {
		return 0, errors.New("db down")
	}
	chat := response.TgChat{Id: 42, Type: chatTypePrivate}
	tapReportUnderPost(t, recorder, chat, response.TgUser{Id: 42}, 10)
	note := &response.TgMessage{MessageId: 30, Text: "broken"}

	handleReportPromptReply(note, &chat, &response.TgUser{Id: 42})

	calls := recorder.all()
	answer, _ := calls[len(calls)-1].body["text"].(string)

	if answer != reportSaveFailedText {
		t.Fatalf("expected the failure answer, got %q", answer)
	}

	retry := &response.TgMessage{MessageId: 31, Text: "broken again"}

	if handleReportPromptReply(retry, &chat, &response.TgUser{Id: 42}) == false {
		t.Fatalf("the prompt must keep waiting for a retry")
	}
}

func TestReplyToAPromptFromBeforeARestartIsAnswered(t *testing.T) {
	recorder := startReportTest(t)
	reply := &response.TgMessage{MessageId: 41, Text: "broken", ReplyToMessage: reportBotMessage(40, reportPromptTitle+"\n\nDescribe the problem")}

	if handleReportPromptReply(reply, &response.TgChat{Id: 42, Type: chatTypePrivate}, &response.TgUser{Id: 42}) == false {
		t.Fatalf("a reply to an untracked prompt must not be lost in silence")
	}

	userCalls := callsTo(recorder.all(), 42)
	answer, _ := userCalls[0].body["text"].(string)

	if (answer != reportPromptExpiredText) || (len(callsTo(recorder.all(), 1)) != 0) {
		t.Fatalf("expected the expired answer and no report, got %+v", recorder.all())
	}
}

func TestBareReportPromptTakesTheNextPrivateMessage(t *testing.T) {
	recorder := startReportTest(t)
	chat := response.TgChat{Id: 42, Type: chatTypePrivate}

	handleReportCommand(&response.TgMessage{MessageId: 5, Text: "/report"}, &chat, &response.TgUser{Id: 42})
	handleReportPromptReply(&response.TgMessage{MessageId: 6, Text: "stories never load"}, &chat, &response.TgUser{Id: 42})

	ownerCalls := callsTo(recorder.all(), 1)
	ownerText, _ := ownerCalls[0].body["text"].(string)

	if (len(ownerCalls) != 1) || (strings.Contains(ownerText, "stories never load") == false) {
		t.Fatalf("expected the next message as the report, got %+v", ownerCalls)
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

func TestALinkAfterThePromptIsDownloadedNotFiled(t *testing.T) {
	recorder := startReportTest(t)
	chat := response.TgChat{Id: 42, Type: chatTypePrivate}
	tapReportUnderPost(t, recorder, chat, response.TgUser{Id: 42}, 10)
	link := &response.TgMessage{MessageId: 30, Text: "https://www.tiktok.com/@a/video/9"}
	sticker := &response.TgMessage{MessageId: 31}

	if handleReportPromptReply(link, &chat, &response.TgUser{Id: 42}) == true {
		t.Fatalf("a bare link must be downloaded, not filed")
	}

	if handleReportPromptReply(sticker, &chat, &response.TgUser{Id: 42}) == true {
		t.Fatalf("a message without text must not be filed")
	}

	note := &response.TgMessage{MessageId: 32, Text: "no sound"}

	if handleReportPromptReply(note, &chat, &response.TgUser{Id: 42}) == false {
		t.Fatalf("the prompt must still wait for words")
	}
}

func TestReplyToThePostAnswersThePrivatePrompt(t *testing.T) {
	recorder := startReportTest(t)
	chat := response.TgChat{Id: 42, Type: chatTypePrivate}
	prompt := tapReportUnderPost(t, recorder, chat, response.TgUser{Id: 42}, 10)
	post := reportBotMessage(prompt.ReplyToMessage.MessageId, "🎵 Author")
	reply := &response.TgMessage{MessageId: 30, Text: "no sound", ReplyToMessage: post}

	if handleReportPromptReply(reply, &chat, &response.TgUser{Id: 42}) == false {
		t.Fatalf("a reply to the post must answer the open prompt")
	}

	if len(callsTo(recorder.all(), 1)) != 1 {
		t.Fatalf("expected one report")
	}
}

func TestAnsweringAnOlderPromptKeepsTheNewestAsNextMessage(t *testing.T) {
	recorder := startReportTest(t)
	chat := response.TgChat{Id: 42, Type: chatTypePrivate}
	older := tapReportUnderPost(t, recorder, chat, response.TgUser{Id: 42}, 10)
	tapReportUnderPost(t, recorder, chat, response.TgUser{Id: 42}, 20)

	handleReportPromptCallback(&response.TgCallbackQuery{Id: "c2", From: response.TgUser{Id: 42}, Message: *older, Data: reportSendCallback})

	note := &response.TgMessage{MessageId: 30, Text: "second post has no sound"}

	if handleReportPromptReply(note, &chat, &response.TgUser{Id: 42}) == false {
		t.Fatalf("the newest prompt must still take the next message")
	}
}

func TestNextMessageWindowIsShort(t *testing.T) {
	if reportNextMessageTTL > 15*time.Minute {
		t.Fatalf("the next-message window must stay short, got %v", reportNextMessageTTL)
	}
}
