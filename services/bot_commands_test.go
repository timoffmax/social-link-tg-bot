package services

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func botCommandNames(commands []BotCommand) map[string]bool {
	result := map[string]bool{}

	for _, command := range commands {
		result[command.Command] = true
	}

	return result
}

func TestBotCommandListHidesStarsSupportWhenDisabled(t *testing.T) {
	names := botCommandNames(botCommandList(false))

	for _, name := range []string{"support", "paysupport", "terms", "starsrefund"} {
		if names[name] == true {
			t.Fatalf("expected %s to be hidden", name)
		}
	}

	if names["donate"] == false {
		t.Fatalf("expected donate to stay listed")
	}
}

func TestBotCommandListShowsStarsSupportWhenEnabled(t *testing.T) {
	names := botCommandNames(botCommandList(true))

	for _, name := range []string{"paysupport", "terms"} {
		if names[name] == false {
			t.Fatalf("expected %s to be listed", name)
		}
	}

	if names["support"] == true {
		t.Fatalf("expected the /support alias to stay unlisted")
	}

	if names["starsrefund"] == true {
		t.Fatalf("expected the owner-only starsrefund to stay hidden")
	}
}

func donateDescription(commands []BotCommand) string {
	result := ""

	for _, command := range commands {
		if command.Command == "donate" {
			result = command.Description
		}
	}

	return result
}

func TestDonateDescriptionMentionsStarsOnlyWhenEnabled(t *testing.T) {
	if description := donateDescription(botCommandList(true)); description != donateStarsDescription {
		t.Fatalf("expected the Stars description, got %q", description)
	}

	if description := donateDescription(botCommandList(false)); description != donateCryptoDescription {
		t.Fatalf("expected the crypto description, got %q", description)
	}
}

func TestBotDescriptionsFitTelegramLimits(t *testing.T) {
	markDatabaseOpenForTest(t)
	unsetPlatformEnv(t)
	t.Setenv("APIFY_TOKEN", "test-token")
	t.Setenv("GATED_FETCH_USER_MONTHLY_ALLOWANCE", "1000")
	t.Setenv("GATED_FETCH_CHAT_MONTHLY_CAP", "1000")
	useSettingsSnapshot(t, map[string]any{})

	if strings.Contains(BotDescription(), "💎 Special downloads") == false {
		t.Fatalf("the worst case must include the special-download line: %q", BotDescription())
	}

	if utf16Length(BotDescription()) != 474 {
		t.Fatalf("expected the worst case at 474 UTF-16 units, got %d", utf16Length(BotDescription()))
	}

	descriptions := []struct {
		text      string
		maxLength int
	}{
		{BotDescription(), botDescriptionMaxLength},
		{BotShortDescription(), botShortDescriptionMaxLength},
	}

	for _, description := range descriptions {
		if utf8.RuneCountInString(description.text) > description.maxLength {
			t.Fatalf("%d characters over the limit of %d: %q", utf8.RuneCountInString(description.text), description.maxLength, description.text)
		}

		if utf16Length(description.text) > description.maxLength {
			t.Fatalf("%d UTF-16 units over the limit of %d: %q", utf16Length(description.text), description.maxLength, description.text)
		}
	}
}

func TestBotDescriptionMentionsSpecialDownloadsOnlyWhileOffered(t *testing.T) {
	markDatabaseOpenForTest(t)
	t.Setenv("APIFY_TOKEN", "test-token")
	t.Setenv("GATED_FETCH_USER_MONTHLY_ALLOWANCE", "")
	t.Setenv("GATED_FETCH_CHAT_MONTHLY_CAP", "")
	offered := BotDescription()

	if (strings.Contains(offered, "💎 Special downloads (they cost the developer, see /info): by default 20 a month per person, 20 per group.") == false) || (strings.Contains(offered, botDescriptionFreeOfferedText) == false) {
		t.Fatalf("expected the special line and \"Most public posts\" while offered, got %q", offered)
	}

	t.Setenv("APIFY_TOKEN", "")
	t.Setenv("HIKERAPI_TOKEN", "")
	t.Setenv("RELAY_PLATFORMS", "")
	off := BotDescription()

	if (strings.Contains(off, "Special downloads") == true) || (strings.Contains(off, botDescriptionFreeText) == false) {
		t.Fatalf("the special line must go and the free line stay plain while nothing is offered, got %q", off)
	}
}

func TestBotDescriptionWordsANoneDefault(t *testing.T) {
	markDatabaseOpenForTest(t)
	t.Setenv("APIFY_TOKEN", "test-token")
	t.Setenv("GATED_FETCH_USER_MONTHLY_ALLOWANCE", "5")
	t.Setenv("GATED_FETCH_CHAT_MONTHLY_CAP", "0")

	if strings.Contains(BotDescription(), "by default 5 a month per person, none per group.") == false {
		t.Fatalf("unexpected limit wording: %q", BotDescription())
	}
}
