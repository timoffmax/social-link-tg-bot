package app

import (
	"strings"
	"testing"

	"github.com/timoffmax/social-link-bot/model/api/response"
)

func TestParseGatedUserTargetPrefersExplicitIDOverReply(t *testing.T) {
	msg := &response.TgMessage{ReplyToMessage: &response.TgMessage{From: response.TgUser{Id: 555}}}

	userID, rest, err := parseGatedUserTarget([]string{"123456", "5"}, msg)

	if (err != nil) || (userID != 123456) || (len(rest) != 1) || (rest[0] != "5") {
		t.Fatalf("expected the explicit id, got %d %v %v", userID, rest, err)
	}
}

func TestParseGatedUserTargetUsesReplyWithOneParam(t *testing.T) {
	msg := &response.TgMessage{ReplyToMessage: &response.TgMessage{From: response.TgUser{Id: 555}}}

	userID, rest, err := parseGatedUserTarget([]string{"5"}, msg)

	if (err != nil) || (userID != 555) || (rest[0] != "5") {
		t.Fatalf("expected the replied-to user, got %d %v %v", userID, rest, err)
	}
}

func TestParseGatedUserTargetRejectsBotReply(t *testing.T) {
	msg := &response.TgMessage{ReplyToMessage: &response.TgMessage{From: response.TgUser{Id: 1087968824, IsBot: true}}}

	_, _, err := parseGatedUserTarget([]string{"5"}, msg)

	if err != errGatedBotTarget {
		t.Fatalf("expected the bot-target error, got %v", err)
	}
}

func TestParseGatedUserTargetRequiresATarget(t *testing.T) {
	_, _, err := parseGatedUserTarget([]string{"5"}, &response.TgMessage{})

	if err != errGatedNoUser {
		t.Fatalf("expected the no-user error, got %v", err)
	}
}

func TestParseGatedGroupIDRejectsZeroAndPositiveIDs(t *testing.T) {
	chat := &response.TgChat{Id: -100}

	for _, raw := range []string{"0", "12345"} {
		if _, err := parseGatedGroupID([]string{raw}, 0, chat); err != errGatedNotGroup {
			t.Fatalf("%s: expected the not-group error, got %v", raw, err)
		}
	}

	if id, err := parseGatedGroupID(nil, 0, chat); (err != nil) || (id != -100) {
		t.Fatalf("expected the current group, got %d %v", id, err)
	}
}

func TestParseGatedLimitAcceptsDefaultAndRejectsNegative(t *testing.T) {
	limit, err := parseGatedLimit([]string{"default"}, 0)

	if (err != nil) || (limit.Valid == true) {
		t.Fatalf("expected a reset to default, got %+v %v", limit, err)
	}

	if _, err := parseGatedLimit([]string{"-1"}, 0); err == nil {
		t.Fatalf("expected a negative limit to fail")
	}
}

func TestGatedResultTextEscapesErrorsAndSkipsStatus(t *testing.T) {
	statusCalled := false
	status := func(int64) string {
		statusCalled = true

		return "status"
	}

	text := gatedResultText(errGatedInput("invalid limit: <5>"), 0, status)

	if (statusCalled == true) || (strings.Contains(text, "&lt;5&gt;") == false) {
		t.Fatalf("expected an escaped error and no status read, got %q", text)
	}
}

type errGatedInput string

func (e errGatedInput) Error() string {
	return string(e)
}

func TestExtractAuthorInfoKeepsGatedCaptionLinesBalanced(t *testing.T) {
	caption := "📷 Анастасія Петрівна Коваленко (@anastasia_petrovna_k)\n\n<tg-spoiler>Hi @friend</tg-spoiler>\n<tg-spoiler>second line</tg-spoiler>"
	line := extractAuthorInfo(caption, "instagram")

	if strings.Count(line, "<tg-spoiler>") != strings.Count(line, "</tg-spoiler>") {
		t.Fatalf("unbalanced line: %q", line)
	}
}
