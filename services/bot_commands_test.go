package services

import (
	"strings"
	"testing"
	"unicode/utf16"
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

// utf16Length counts UTF-16 code units, the stricter way Telegram may count.
func utf16Length(text string) int {
	return len(utf16.Encode([]rune(text)))
}

func TestBotDescriptionsFitTelegramLimits(t *testing.T) {
	t.Setenv("APIFY_TOKEN", "test-token")
	t.Setenv("GATED_FETCH_USER_MONTHLY_ALLOWANCE", "1000")
	t.Setenv("GATED_FETCH_CHAT_MONTHLY_CAP", "1000")

	descriptions := []struct {
		text      string
		maxLength int
	}{
		{BotDescription(), botDescriptionMaxLength},
		{BotShortDescription, botShortDescriptionMaxLength},
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

func TestBotDescriptionMentionsRestrictedPostsOnlyWhenTheyCanBeFetched(t *testing.T) {
	t.Setenv("APIFY_TOKEN", "test-token")

	if strings.Contains(BotDescription(), "Restricted Instagram posts") == false {
		t.Fatalf("expected the restricted line while paid fetching is configured")
	}

	t.Setenv("APIFY_TOKEN", "")

	if strings.Contains(BotDescription(), "Restricted Instagram posts") == true {
		t.Fatalf("the restricted line must go while paid fetching is off")
	}
}
