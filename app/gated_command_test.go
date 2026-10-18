package app

import (
	"strings"
	"testing"

	"github.com/timoffmax/social-link-bot/model/api/response"
	"github.com/timoffmax/social-link-bot/services"
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

func TestParseGatedLimitAcceptsUnlimited(t *testing.T) {
	limit, err := parseGatedLimit([]string{"Unlimited"}, 0)

	if (err != nil) || (limit.Valid == false) || (limit.Int64 != services.GatedFetchUnlimited) {
		t.Fatalf("expected the unlimited marker, got %+v %v", limit, err)
	}

	if formatGatedLimit(limit.Int64) != "unlimited" {
		t.Fatalf("unexpected rendering: %q", formatGatedLimit(limit.Int64))
	}
}

func TestExtractAuthorInfoFindsTheProfileLinkLine(t *testing.T) {
	caption := services.FormatAuthorCaption("instagram", "Insight Gastrobar Kyiv Official", "insight.gastrobar", "caption text")
	line := extractAuthorInfo(caption, "instagram")

	if strings.Contains(line, `href="https://www.instagram.com/insight.gastrobar/"`) == false {
		t.Fatalf("expected the author line, got %q", line)
	}
}

func TestInstagramGateReplyExplainsTheReason(t *testing.T) {
	gate := services.InstagramGate{Restricted: true, Title: "x"}
	cases := map[services.GatedFetchDenial]string{
		services.GatedFetchDeniedUserCap: gatedAllowanceUsedHint,
		services.GatedFetchDeniedChatCap: gatedChatCapReachedHint,
		services.GatedFetchDeniedGlobal:  gatedGlobalCapReachedHint,
		services.GatedFetchDeniedChatOff: gatedChatOffHint,
		services.GatedFetchDeniedBlocked: gatedUserBlockedHint,
	}

	for denial, hint := range cases {
		err := &services.GatedFetchDeniedError{Denial: denial, Cause: &services.InstagramGatedError{Gate: gate}}

		if strings.Contains(instagramGateReplyText(err, gate), hint) == false {
			t.Fatalf("%s: missing hint %q", denial, hint)
		}
	}
}

func TestThreadsLinkWithAFacebookUsernameIsNotQuiet(t *testing.T) {
	if isQuietFailurePlatform("https://www.threads.com/@news.facebook.com/post/ABC123") == true {
		t.Fatalf("a Threads link must not be treated as Facebook")
	}

	if isQuietFailurePlatform("https://www.facebook.com/reel/1") == false {
		t.Fatalf("Facebook links stay quiet")
	}

	if detectPlatformFromUrl("https://www.threads.com/@fox.com/post/ABC123") != services.ThreadsPlatform {
		t.Fatalf("expected threads")
	}
}

func TestBudgetDenialExplainsThePause(t *testing.T) {
	gate := services.InstagramGate{Restricted: true, Title: "x"}
	err := &services.GatedFetchDeniedError{Denial: services.GatedFetchDeniedBudget, Cause: &services.InstagramGatedError{Gate: gate}}

	if strings.Contains(instagramGateReplyText(err, gate), paidBudgetPausedText) == false {
		t.Fatalf("expected the budget pause note")
	}
}

func TestGlobalCapTextShowsNoCap(t *testing.T) {
	if (globalCapText(-1) != globalNoCapText) || (globalCapText(100) != "100") {
		t.Fatalf("unexpected cap texts %q %q", globalCapText(-1), globalCapText(100))
	}
}

func TestGatedProvidersTextListsTheProvidersInOrder(t *testing.T) {
	t.Setenv("APIFY_TOKEN", "")
	t.Setenv("HIKERAPI_TOKEN", "")

	if gatedProvidersText() != gatedProvidersNoneText {
		t.Fatalf("expected no providers, got %q", gatedProvidersText())
	}

	t.Setenv("APIFY_TOKEN", "test-token")
	t.Setenv("APIFY_MONTHLY_BUDGET_USD", "4")
	t.Setenv("APIFY_INSTAGRAM_ACTOR", "intropix~posts")
	t.Setenv("HIKERAPI_TOKEN", "test-key")
	t.Setenv("HIKERAPI_MONTHLY_BUDGET_USD", "1")

	if text := gatedProvidersText(); text != "Apify (<code>intropix~posts</code>) → HikerAPI" {
		t.Fatalf("unexpected providers %q", text)
	}

	t.Setenv("INSTAGRAM_PAID_PROVIDERS", "hiker")

	if text := gatedProvidersText(); text != "HikerAPI" {
		t.Fatalf("unexpected providers %q", text)
	}
}

func TestGatedProvidersTextMarksAProviderPausedByABudgetOfZero(t *testing.T) {
	t.Setenv("APIFY_TOKEN", "test-token")
	t.Setenv("APIFY_MONTHLY_BUDGET_USD", "0")
	t.Setenv("APIFY_INSTAGRAM_ACTOR", "intropix~posts")
	t.Setenv("HIKERAPI_TOKEN", "")

	if text := gatedProvidersText(); text != "Apify (<code>intropix~posts</code>) (budget 0: paused)" {
		t.Fatalf("unexpected providers %q", text)
	}
}
