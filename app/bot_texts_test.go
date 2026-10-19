package app

import (
	"strings"
	"testing"
)

func TestStartGuideExplainsChatsAndContentWithTheConfiguredLimits(t *testing.T) {
	markDatabaseOpenForTest(t)
	t.Setenv("APIFY_TOKEN", "test-token")
	t.Setenv("GATED_FETCH_USER_MONTHLY_ALLOWANCE", "7")
	t.Setenv("GATED_FETCH_CHAT_MONTHLY_CAP", "11")

	guide := startGuideText(false)

	for _, part := range []string{"In a private chat", "In a group", "Free, no monthly limit", "Special downloads (monthly limit)", "7 a month for you", "11 a month by default, shared"} {
		if strings.Contains(guide, part) == false {
			t.Fatalf("expected %q in the guide, got %q", part, guide)
		}
	}

	if strings.Contains(guide, "Stars") == true {
		t.Fatalf("bonus downloads must not be offered while Stars is off")
	}

	if strings.Contains(startGuideText(true), startBonusStarsText) == false {
		t.Fatalf("expected the bonus line while Stars is on")
	}
}

func TestStartGuideDoesNotPromiseSpecialDownloadsWhenTheyAreOff(t *testing.T) {
	t.Setenv("APIFY_TOKEN", "")

	guide := startGuideText(true)

	if (strings.Contains(guide, startSpecialOffText) == false) || (strings.Contains(guide, "a month for you") == true) {
		t.Fatalf("expected special downloads to be described as unavailable, got %q", guide)
	}
}

func TestInfoShowsTheGuideWithTheReasonForLimits(t *testing.T) {
	markDatabaseOpenForTest(t)
	t.Setenv("APIFY_TOKEN", "test-token")
	text := infoText()

	for _, part := range []string{"Supported platforms", "In a private chat", "In a group", "Why the limits?", "/donate"} {
		if strings.Contains(text, part) == false {
			t.Fatalf("expected %q in /info, got %q", part, text)
		}
	}

	if strings.Contains(text, "overall monthly total") == true {
		t.Fatalf("/info must not mention a bot-wide total")
	}
}

func TestFreeSectionFollowsTheConfig(t *testing.T) {
	t.Setenv("APIFY_TOKEN", "")
	t.Setenv("DEFAULT_DAILY_MEDIA_LIMIT", "30")

	freeText := startFreeText()
	platforms := startPlatformsText()

	if (strings.Contains(freeText, "30 media a day") == false) || (strings.Contains(freeText, "Stories") == true) || (strings.Contains(platforms, "Stories") == true) {
		t.Fatalf("expected the daily limit and no Stories without the paid service, got %q / %q", freeText, platforms)
	}

	if (monthlyLimitText(0) != monthlyLimitNoneText) || (monthlyLimitText(-1) != monthlyLimitOffText) || (monthlyLimitText(20) != "20 a month") {
		t.Fatalf("unexpected monthly limit wordings")
	}
}

func TestGuideSaysGroupsNeedNoAdminRightsOnlyWithGroupPrivacyOff(t *testing.T) {
	t.Setenv("APIFY_TOKEN", "test-token")
	useGroupPrivacy(t, groupPrivacyOff)

	for _, limit := range []string{"0", "30"} {
		t.Setenv("DEFAULT_DAILY_MEDIA_LIMIT", limit)
		guide := startGuideText(false)

		for _, part := range []string{startGroupNoAdminRightsText, "\"Delete messages\" right"} {
			if strings.Count(guide, part) != 1 {
				t.Fatalf("limit %s: expected %q once in the guide, got %q", limit, part, guide)
			}
		}
	}
}

func TestGuideClaimsNoAdminRightsFreedomWhileGroupPrivacyIsOnOrUnknown(t *testing.T) {
	t.Setenv("APIFY_TOKEN", "test-token")

	for _, state := range []int32{groupPrivacyOn, groupPrivacyUnknown} {
		useGroupPrivacy(t, state)
		guide := startGuideText(false)

		if (strings.Contains(guide, "don't need admin rights") == true) || (strings.Contains(guide, startGroupAdminHintText) == false) {
			t.Fatalf("state %d: expected the admin hint and no claim, got %q", state, guide)
		}
	}
}
