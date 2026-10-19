package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/timoffmax/social-link-bot/model/api/response"
	"github.com/timoffmax/social-link-bot/services"
)

// specialSectionTitle opens the special-download section of the guide.
const specialSectionTitle = "💎 <b>Special downloads (monthly limit)</b>"

// useTestRelay configures the home relay for the given comma-separated
// platforms, with a token file that exists.
func useTestRelay(t *testing.T, platforms string) {
	tokenFile := filepath.Join(t.TempDir(), "relay.token")

	if err := os.WriteFile(tokenFile, []byte("test-relay-token\n"), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}

	t.Setenv("RELAY_URL", "http://127.0.0.1:8791")
	t.Setenv("RELAY_TOKEN_FILE", tokenFile)
	t.Setenv("RELAY_PLATFORMS", platforms)
}

// useSpecialSources sets the paid tokens and the relay platforms, with
// Instagram and YouTube on and the default limits.
func useSpecialSources(t *testing.T, apifyToken string, relayPlatforms string) {
	markDatabaseOpenForTest(t)
	useTestRelay(t, relayPlatforms)
	t.Setenv("ENABLE_INSTAGRAM", "true")
	t.Setenv("ENABLE_YOUTUBE", "true")
	t.Setenv("APIFY_TOKEN", apifyToken)
	t.Setenv("HIKERAPI_TOKEN", "")
	t.Setenv("APIFY_MONTHLY_BUDGET_USD", "")
	t.Setenv("INSTAGRAM_PAID_PROVIDERS", "")
	t.Setenv("GATED_FETCH_GLOBAL_MONTHLY_CAP", "")
	t.Setenv("GATED_FETCH_USER_MONTHLY_ALLOWANCE", "")
	t.Setenv("GATED_FETCH_CHAT_MONTHLY_CAP", "")
}

// specialSection returns the guide's special-download section.
func specialSection(t *testing.T, guide string) string {
	result := ""
	at := strings.Index(guide, specialSectionTitle)

	if at < 0 {
		t.Fatalf("no special section in %q", guide)
	}

	if at >= 0 {
		result = guide[at:]
	}

	return result
}

func TestSpecialDownloadDeniedTextPerDenial(t *testing.T) {
	hints := map[services.GatedFetchDenial]string{
		services.GatedFetchDeniedUserCap: "You've used all your free special downloads for this month.",
		services.GatedFetchDeniedChatCap: "This chat has used all its special downloads for this month.",
		services.GatedFetchDeniedGlobal:  "The bot has used all its special downloads for this month.",
		services.GatedFetchDeniedChatOff: "Special downloads are turned off in this chat.",
		services.GatedFetchDeniedBlocked: "Special downloads are turned off for you.",
	}
	supportLines := map[services.GatedFetchDenial]string{
		services.GatedFetchDeniedUserCap: "\nGet bonus downloads by supporting the bot: /donate",
		services.GatedFetchDeniedChatCap: fmt.Sprintf("\n%s", fmt.Sprintf(boostGatedHintTemplate, boostCommand)),
		services.GatedFetchDeniedGlobal:  "\nGet bonus downloads by supporting the bot: /donate",
	}

	for _, isStarsOn := range []bool{true, false} {
		t.Setenv("STARS_SUPPORT_ENABLED", fmt.Sprintf("%t", isStarsOn))

		for denial, hint := range hints {
			want := fmt.Sprintf("%s\n\n%s", specialDownloadDeniedLeadText, hint)

			if isStarsOn == true {
				want = fmt.Sprintf("%s%s", want, supportLines[denial])
			}

			if got := specialDownloadDeniedText(denial); got != want {
				t.Errorf("stars %t, %s: got %q, want %q", isStarsOn, denial, got, want)
			}
		}

		if got := specialDownloadDeniedText(services.GatedFetchDeniedBudget); got != paidBudgetPausedText {
			t.Errorf("stars %t: the budget pause must be the paused text alone, got %q", isStarsOn, got)
		}
	}

	t.Setenv("STARS_SUPPORT_ENABLED", "true")

	if strings.HasSuffix(specialDownloadDeniedText(services.GatedFetchDeniedChatCap), "/boost") == false {
		t.Errorf("chat_cap must end with /boost")
	}
}

func TestReplySpecialDownloadDeniedSkipsGatedPostsAndFailures(t *testing.T) {
	recorder := startTestBotAPI(t, false)
	chat := &response.TgChat{Id: 42, Type: chatTypePrivate}
	origMessage := &response.TgMessage{MessageId: 7, Chat: *chat}
	gatedCause := &services.InstagramGatedError{Gate: services.InstagramGate{Restricted: true, Title: "x"}}
	skipped := []error{
		&services.GatedFetchDeniedError{Denial: services.GatedFetchDeniedUserCap, Cause: gatedCause},
		&services.GatedFetchDeniedError{Denial: services.GatedFetchAllowed, Cause: errors.New("failed")},
		&services.GatedFetchDeniedError{Denial: services.GatedFetchDeniedOff, Cause: errors.New("failed")},
		&services.GatedFetchDeniedError{Denial: services.GatedFetchDeniedBudget, Cause: errors.New("failed")},
		errors.New("plain failure"),
	}

	for _, downloadErr := range skipped {
		if replySpecialDownloadDenied(downloadErr, origMessage, chat) == true {
			t.Fatalf("%v: must not be answered as a limit refusal", downloadErr)
		}
	}

	if len(recorder.all()) != 0 {
		t.Fatalf("expected no message, got %+v", recorder.all())
	}

	refused := fmt.Errorf("chain: %w", &services.GatedFetchDeniedError{Denial: services.GatedFetchDeniedUserCap, Cause: errors.New("refused")})

	if replySpecialDownloadDenied(refused, origMessage, chat) == false {
		t.Fatalf("a limit refusal must be answered")
	}

	calls := recorder.all()
	text, _ := calls[0].body["text"].(string)

	if (len(calls) != 1) || (text != specialDownloadDeniedText(services.GatedFetchDeniedUserCap)) {
		t.Fatalf("expected the limit message, got %+v", calls)
	}
}

func TestInstagramGateReplyTextUsesTheSpecialDownloadHints(t *testing.T) {
	t.Setenv("STARS_SUPPORT_ENABLED", "true")
	gate := services.InstagramGate{Restricted: true, Title: "x"}
	err := &services.GatedFetchDeniedError{Denial: services.GatedFetchDeniedUserCap, Cause: &services.InstagramGatedError{Gate: gate}}
	want := fmt.Sprintf("%s\n\nYou've used all your free special downloads for this month.\nGet bonus downloads by supporting the bot: /donate", services.InstagramGateMessage(gate))

	if got := instagramGateReplyText(err, gate); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}

	off := &services.GatedFetchDeniedError{Denial: services.GatedFetchDeniedOff, Cause: &services.InstagramGatedError{Gate: gate}}

	if got := instagramGateReplyText(off, gate); got != services.InstagramGateMessage(gate) {
		t.Fatalf("off must add no hint, got %q", got)
	}
}

func TestReelDenialNoteFitsTheCaption(t *testing.T) {
	t.Setenv("STARS_SUPPORT_ENABLED", "false")
	caption := strings.Repeat("a", 1000)
	cover := &services.MediaInfo{Caption: fmt.Sprintf("%s\n\n%s", caption, services.ReelCoverNote)}

	appendReelDenialNote([]*services.MediaInfo{cover}, services.GatedFetchDeniedUserCap)

	deniedText := specialDownloadDeniedText(services.GatedFetchDeniedUserCap)
	coverNoted := fmt.Sprintf("%s\n\n%s", services.ReelCoverNote, deniedText)

	if (len(cover.Caption) > services.TelegramCaptionLimitBytes) || (strings.HasSuffix(cover.Caption, coverNoted) == false) {
		t.Fatalf("expected the cover note and the limit message within the limit, got %d bytes: %q", len(cover.Caption), cover.Caption)
	}
}

func TestReelDenialNoteSkipsFailuresThatAreNoRefusal(t *testing.T) {
	for _, denial := range []services.GatedFetchDenial{services.GatedFetchAllowed, services.GatedFetchDeniedOff, services.GatedFetchDeniedNoDB} {
		cover := &services.MediaInfo{Caption: services.ReelCoverNote}

		appendReelDenialNote([]*services.MediaInfo{cover}, denial)

		if cover.Caption != services.ReelCoverNote {
			t.Fatalf("%q: the caption must stay as it is, got %q", denial, cover.Caption)
		}
	}

	cover := &services.MediaInfo{Caption: services.ReelCoverNote}

	appendReelDenialNote([]*services.MediaInfo{cover}, services.GatedFetchDeniedBudget)

	if strings.HasSuffix(cover.Caption, paidBudgetPausedText) == false {
		t.Fatalf("a paused budget must be noted, got %q", cover.Caption)
	}
}

func TestStartGuideFollowsTheSpecialDownloadSources(t *testing.T) {
	useSpecialSources(t, "test-token", "")
	paid := specialSection(t, startGuideText(false))

	for _, part := range []string{"Instagram posts whose author limited them by age or region, reels whose video Instagram shows only to logged-in users, Instagram Stories and YouTube Shorts I can't get for free: I can only get these through a paid third-party service,", "Age-limited posts arrive blurred", "The paid service charges for each one"} {
		if strings.Contains(paid, part) == false {
			t.Fatalf("paid only: expected %q in %q", part, paid)
		}
	}

	if strings.Contains(paid, "home connection") == true {
		t.Fatalf("paid only: no home connection, got %q", paid)
	}

	useSpecialSources(t, "", "instagram,youtube")
	relay := specialSection(t, startGuideText(false))

	if strings.Contains(relay, "Reels whose video Instagram shows only to logged-in users and YouTube Shorts: I can only get these through the developer's own home connection,") == false {
		t.Fatalf("relay only: unexpected kinds or channel in %q", relay)
	}

	isPaidMentioned := (strings.Contains(relay, "paid") == true) || (strings.Contains(relay, "blurred") == true)

	if (isPaidMentioned == true) || (strings.Contains(relay, startSpecialWhyTexts[specialRouteRelay]) == false) {
		t.Fatalf("relay only: expected the relay reason and nothing paid, got %q", relay)
	}

	useSpecialSources(t, "test-token", "youtube")
	both := specialSection(t, startGuideText(false))

	for _, part := range []string{"and YouTube Shorts: I can only get these through a paid third-party service or the developer's own home connection,", startSpecialWhyTexts[specialRouteBoth]} {
		if strings.Contains(both, part) == false {
			t.Fatalf("both: expected %q in %q", part, both)
		}
	}

	for _, guide := range []string{paid, relay, both, startGuideText(true)} {
		if strings.Contains(guide, "Stories are free") == true {
			t.Fatalf("no Stories-are-free line, got %q", guide)
		}
	}

	if strings.Contains(startFreeText(), startSpecialExceptText) == false {
		t.Fatalf("expected the special downloads excepted from the free section")
	}
}

func TestStartGuideShowsTheOffTextAtCapZero(t *testing.T) {
	useSpecialSources(t, "test-token", "youtube")
	t.Setenv("GATED_FETCH_GLOBAL_MONTHLY_CAP", "0")
	guide := startGuideText(true)

	isOffShown := strings.Contains(guide, startSpecialOffText)
	hasSpecialLines := (strings.Contains(guide, specialSectionTitle) == true) || (strings.Contains(guide, "• Special downloads:") == true)

	if (isOffShown == false) || (hasSpecialLines == true) || (strings.Contains(guide, startSpecialExceptText) == true) {
		t.Fatalf("expected only the off text at cap 0, got %q", guide)
	}
}

func TestStartPlatformsHideStoriesWhileSpecialDownloadsAreOff(t *testing.T) {
	useSpecialSources(t, "test-token", "")

	if strings.Contains(startPlatformsText(), startStoriesItem) == false {
		t.Fatalf("expected Stories while they can be fetched")
	}

	t.Setenv("GATED_FETCH_GLOBAL_MONTHLY_CAP", "0")

	if strings.Contains(startPlatformsText(), startStoriesItem) == true {
		t.Fatalf("Stories must go while special downloads are off")
	}
}

func TestStartGuideLeavesOutTheLeftCountWithoutADefaultLimit(t *testing.T) {
	useSpecialSources(t, "test-token", "")
	t.Setenv("GATED_FETCH_USER_MONTHLY_ALLOWANCE", "0")
	t.Setenv("GATED_FETCH_CHAT_MONTHLY_CAP", "0")
	t.Setenv("GATED_FETCH_GLOBAL_MONTHLY_CAP", "50")

	if strings.Contains(startGuideText(false), startSpecialLeftText) == true {
		t.Fatalf("no limit by default, so no left count")
	}
}

func TestJoinWithAndJoinsTheLastItemWithAnd(t *testing.T) {
	cases := map[string][]string{
		"":           {},
		"a":          {"a"},
		"a and b":    {"a", "b"},
		"a, b and c": {"a", "b", "c"},
	}

	for want, items := range cases {
		if got := joinWithAnd(items); got != want {
			t.Errorf("%v: got %q, want %q", items, got, want)
		}
	}
}

func TestSupportInvoiceDescriptionFitsAtTheTopTier(t *testing.T) {
	tiers := services.SupportTiers()
	top := tiers[len(tiers)-1]
	invoice := buildSupportInvoice(42, 42, top)

	if (utf8.RuneCountInString(invoice.Description) != 180) || (strings.Contains(invoice.Description, "bonus special downloads") == false) {
		t.Fatalf("unexpected description (%d): %q", utf8.RuneCountInString(invoice.Description), invoice.Description)
	}
}

func TestBoostInvoiceDescriptionFitsWithTheLongestTitle(t *testing.T) {
	tiers := services.SupportTiers()
	top := tiers[len(tiers)-1]
	title := strings.Repeat("я", 200)
	invoice := buildBoostInvoice(42, 42, -1001, title, top)

	if (utf8.RuneCountInString(invoice.Description) > 255) || (strings.HasPrefix(invoice.Description, "Bonus special downloads for ") == false) {
		t.Fatalf("unexpected description (%d): %q", utf8.RuneCountInString(invoice.Description), invoice.Description)
	}
}

func TestStarsAndBoostTextsSaySpecialDownloads(t *testing.T) {
	texts := []string{termsText, supportThanksTemplate, donateStarsText, boostGroupText, boostClosedText, boostScreenTemplate, boostThanksTemplate, boostGroupNoticeTemplate}

	for _, text := range texts {
		isOldWording := (strings.Contains(text, "restricted") == true) || (strings.Contains(text, "fetch the post") == true)

		if isOldWording == true {
			t.Fatalf("old wording left in %q", text)
		}
	}

	if (strings.Contains(boostGroupNoticeTemplate, "+%d special downloads") == false) || (strings.Contains(boostScreenTemplate, "a link I can't fetch doesn't use one") == false) {
		t.Fatalf("unexpected boost texts")
	}
}

func TestGatedStatusShowsTheSourcesAndCountedKinds(t *testing.T) {
	useSpecialSources(t, "", "")
	chat := &response.TgChat{Id: 42, Type: chatTypePrivate}
	origMessage := &response.TgMessage{MessageId: 7, Chat: *chat}
	status := handleGatedStatus(nil, origMessage, chat)

	for _, part := range []string{"💎 <b>Special downloads</b>", "Paid providers:", "Home relay: off\n", "Counted: nothing\n", "allow or stop special downloads in a group"} {
		if strings.Contains(status, part) == false {
			t.Fatalf("expected %q in %q", part, status)
		}
	}

	useSpecialSources(t, "test-token", "instagram,youtube")
	status = handleGatedStatus(nil, origMessage, chat)

	for _, part := range []string{"Home relay: instagram, youtube\n", "Counted: restricted posts, reel videos, Stories, Shorts\n"} {
		if strings.Contains(status, part) == false {
			t.Fatalf("expected %q in %q", part, status)
		}
	}

	useSpecialSources(t, "test-token", "")

	if gatedCountedText() != "restricted posts, reel videos, Stories, Shorts after yt-dlp" {
		t.Fatalf("unexpected counted kinds %q", gatedCountedText())
	}
}

func TestMinimalCaptionKeepsTheLimitMessageUnderARefusedReelCover(t *testing.T) {
	t.Setenv("STARS_SUPPORT_ENABLED", "false")
	cover := &services.MediaInfo{Caption: "👤 @author\n\nA long description of the reel"}
	services.AppendCaptionNote(cover, services.ReelCoverNote)
	appendReelDenialNote([]*services.MediaInfo{cover}, services.GatedFetchDeniedUserCap)

	got := minimalCaption(cover, "instagram")
	deniedText := specialDownloadDeniedText(services.GatedFetchDeniedUserCap)
	want := fmt.Sprintf("👤 @author\n\n%s\n\n%s", services.ReelCoverNote, deniedText)

	if got != want {
		t.Fatalf("expected the author line, the cover note and the limit message, got %q", got)
	}
}

func TestMinimalCaptionKeepsTheQuotaNoteOfAShort(t *testing.T) {
	short := &services.MediaInfo{Caption: "📺 Channel: Title\nA description line"}
	note := "📊 Special downloads left this month: 2 of 3"
	services.AppendCaptionNote(short, note)

	got := minimalCaption(short, "youtube")

	if got != fmt.Sprintf("📺 Channel: Title\n\n%s", note) {
		t.Fatalf("expected the first line and the quota note, got %q", got)
	}
}
