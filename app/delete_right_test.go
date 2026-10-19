package app

import (
	"bytes"
	"errors"
	"log"
	"strings"
	"testing"

	"github.com/timoffmax/social-link-bot/model/api/request/payload"
)

// Telegram's descriptions of deleteMessage refusals.
const (
	cantBeDeletedDescription   = "Bad Request: message can't be deleted"
	notEnoughRightsDescription = "Bad Request: not enough rights to delete a message"
	adminRequiredDescription   = "Bad Request: CHAT_ADMIN_REQUIRED"
	notFoundDescription        = "Bad Request: message to delete not found"
)

func useFetchedDeleteRight(t *testing.T, canDelete bool, err error) *int {
	calls := 0
	previousFetch := fetchBotDeleteRight
	previousRights := botDeleteRights
	previousLogged := missingDeleteRightLogged
	fetchBotDeleteRight = func(int64) (bool, error) {
		calls++

		return canDelete, err
	}
	botDeleteRights = newTTLStore(botDeleteRightTTL)
	missingDeleteRightLogged = newTTLStore(missingDeleteRightLogTTL)
	t.Cleanup(func() {
		fetchBotDeleteRight = previousFetch
		botDeleteRights = previousRights
		missingDeleteRightLogged = previousLogged
	})

	return &calls
}

func useDeleteAnswer(t *testing.T, description string, err error) {
	previous := deleteChatMessage
	deleteChatMessage = func(payload.TgPlDeleteMessage) (string, error) { return description, err }
	t.Cleanup(func() { deleteChatMessage = previous })
}

func useEffectiveUserLimit(t *testing.T, limit int, isGroupLimit bool, err error) {
	previous := effectiveUserLimit
	effectiveUserLimit = func(int64, int64) (int, bool, error) { return limit, isGroupLimit, err }
	t.Cleanup(func() { effectiveUserLimit = previous })
}

func useNoIndividualLimit(t *testing.T, limit int, isGroupLimit bool, err error) {
	previous := noIndividualLimit
	noIndividualLimit = func(int64) (int, bool, error) { return limit, isGroupLimit, err }
	t.Cleanup(func() { noIndividualLimit = previous })
}

func useAnyDailyLimitInForce(t *testing.T, isInForce bool, err error) {
	previous := anyDailyLimitInForce
	anyDailyLimitInForce = func(int64) (bool, error) { return isInForce, err }
	t.Cleanup(func() { anyDailyLimitInForce = previous })
}

func captureLog(t *testing.T) *bytes.Buffer {
	buffer := &bytes.Buffer{}
	previous := log.Writer()
	log.SetOutput(buffer)
	t.Cleanup(func() { log.SetOutput(previous) })

	return buffer
}

func TestLimitConfirmationNotesAMissingDeleteRight(t *testing.T) {
	useFetchedDeleteRight(t, false, nil)

	if limitDeleteRightNoteFor(-100, true) != limitDeleteRightNote {
		t.Fatalf("expected the note for a limit without the delete right")
	}
}

func TestLimitConfirmationHasNoNoteWithTheDeleteRight(t *testing.T) {
	useFetchedDeleteRight(t, true, nil)

	if limitDeleteRightNoteFor(-100, true) != "" {
		t.Fatalf("expected no note with the delete right")
	}
}

func TestLimitConfirmationHasNoNoteWhenTheRightIsUnknown(t *testing.T) {
	useFetchedDeleteRight(t, false, errors.New("getChatMember failed"))

	if limitDeleteRightNoteFor(-100, true) != "" {
		t.Fatalf("expected no note when the right is unknown")
	}
}

func TestLimitConfirmationWithoutALimitInForceHasNoNoteAndAsksNothing(t *testing.T) {
	calls := useFetchedDeleteRight(t, false, nil)

	if (limitDeleteRightNoteFor(-100, false) != "") || (*calls != 0) {
		t.Fatalf("expected no note and no check, got %d checks", *calls)
	}
}

func TestLimitConfirmationInAPrivateChatAsksNothing(t *testing.T) {
	calls := useFetchedDeleteRight(t, false, nil)

	if (limitDeleteRightNoteFor(7, true) != "") || (*calls != 0) {
		t.Fatalf("expected no note and no check in a private chat, got %d checks", *calls)
	}
}

func TestLimitConfirmationAsksTheRightAfreshNotTheRememberedAnswer(t *testing.T) {
	calls := useFetchedDeleteRight(t, false, nil)
	rememberBotDeleteRight(-100, true)

	if (limitDeleteRightNoteFor(-100, true) != limitDeleteRightNote) || (*calls != 1) {
		t.Fatalf("expected one fresh check that finds the right missing, got %d checks", *calls)
	}
}

func TestUserLimitIsInForceByTheEnforcedLimit(t *testing.T) {
	cases := []struct {
		name     string
		limit    int
		err      error
		expected bool
	}{
		{"individual or group limit", 20, nil, true},
		{"no limit", 0, nil, false},
		{"unreadable limit", 20, errors.New("db down"), false},
	}

	for _, item := range cases {
		useEffectiveUserLimit(t, item.limit, true, item.err)

		if isLimitInForce(-100, 7) != item.expected {
			t.Fatalf("%s: expected %t", item.name, item.expected)
		}
	}
}

func TestChatLimitIsInForceWhenAnyoneHasALimit(t *testing.T) {
	cases := []struct {
		name      string
		isInForce bool
		err       error
		expected  bool
	}{
		{"default, group or individual limit", true, nil, true},
		{"no limit for anyone", false, nil, false},
		{"unreadable limits", true, errors.New("db down"), false},
	}

	for _, item := range cases {
		useAnyDailyLimitInForce(t, item.isInForce, item.err)

		if isLimitInForce(-100, anyUserID) != item.expected {
			t.Fatalf("%s: expected %t", item.name, item.expected)
		}
	}
}

func TestNoLimitIsInForceWithoutADatabase(t *testing.T) {
	t.Setenv("DEFAULT_DAILY_MEDIA_LIMIT", "30")

	isUserInForce := isLimitInForce(-100, 7)
	isChatInForce := isLimitInForce(-100, anyUserID)
	userStatus := removedUserLimitStatus(7, -100)
	groupStatus := removedGroupLimitStatus(-100)

	if (isUserInForce == true) || (isChatInForce == true) || (userStatus != userLimitUnlimitedText) || (groupStatus != groupLimitNoneText) {
		t.Fatalf("expected no limit without a database, got %t %t %q %q", isUserInForce, isChatInForce, userStatus, groupStatus)
	}
}

func TestRemovedUserLimitStatusNamesTheLimitThatApplies(t *testing.T) {
	cases := []struct {
		name         string
		limit        int
		isGroupLimit bool
		err          error
		expected     string
	}{
		{"group limit", 10, true, nil, "falls back to the group limit of <b>10</b>"},
		{"no limit", 0, true, nil, "Unlimited"},
		{"unreadable limit", 0, false, errors.New("db down"), "couldn't be read"},
	}

	for _, item := range cases {
		useEffectiveUserLimit(t, item.limit, item.isGroupLimit, item.err)
		status := removedUserLimitStatus(7, -100)

		if strings.Contains(status, item.expected) == false {
			t.Fatalf("%s: expected %q, got %q", item.name, item.expected, status)
		}
	}
}

func TestRemovedGroupLimitStatusNamesTheLimitThatApplies(t *testing.T) {
	cases := []struct {
		name         string
		limit        int
		isGroupLimit bool
		err          error
		expected     string
	}{
		{"default limit", 30, false, nil, "the default limit of 30 media items per day applies to users never given an individual limit; users whose individual limit was removed now have no limit"},
		{"no limit", 0, true, nil, "no limit for users without an individual limit"},
		{"unreadable limit", 0, false, errors.New("db down"), "couldn't be read"},
	}

	for _, item := range cases {
		useNoIndividualLimit(t, item.limit, item.isGroupLimit, item.err)
		status := removedGroupLimitStatus(-100)

		if (strings.Contains(status, item.expected) == false) || (strings.Contains(status, "Unlimited") == true) {
			t.Fatalf("%s: expected %q, got %q", item.name, item.expected, status)
		}
	}
}

func TestRemovedGroupLimitWithIndividualLimitsKeepsTheNote(t *testing.T) {
	useFetchedDeleteRight(t, false, nil)
	useNoIndividualLimit(t, 0, true, nil)
	useAnyDailyLimitInForce(t, true, nil)

	isInForce := isLimitInForce(-100, anyUserID)
	note := limitDeleteRightNoteFor(-100, isInForce)
	status := removedGroupLimitStatus(-100)

	if (note != limitDeleteRightNote) || (status != groupLimitNoneText) {
		t.Fatalf("expected the note next to the status of users without a limit, got %q %q", note, status)
	}
}

func TestDeleteOutcomeFollowsTelegramsDescription(t *testing.T) {
	failure := errors.New("API error")
	cases := []struct {
		name        string
		chatID      int64
		description string
		err         error
		expected    int
	}{
		{"deleted", -100, "", nil, deleteDone},
		{"refused for the right", -100, notEnoughRightsDescription, failure, deleteRefusedForTheRight},
		{"admin required", -100, adminRequiredDescription, failure, deleteRefusedForTheRight},
		{"can't be deleted", -100, cantBeDeletedDescription, failure, deleteRefusedUndecided},
		{"not found", -100, notFoundDescription, failure, deleteFailed},
		{"no answer", -100, "", failure, deleteFailed},
		{"private chat", 7, notEnoughRightsDescription, failure, deleteFailed},
		{"private chat, can't be deleted", 7, cantBeDeletedDescription, failure, deleteFailed},
	}

	for _, item := range cases {
		if deleteOutcomeOf(item.chatID, item.description, item.err) != item.expected {
			t.Fatalf("%s: expected outcome %d", item.name, item.expected)
		}
	}
}

func TestRightRefusalIsLoggedOncePerChatWithoutALookup(t *testing.T) {
	calls := useFetchedDeleteRight(t, false, nil)
	useDeleteAnswer(t, notEnoughRightsDescription, errors.New("API error"))
	logged := captureLog(t)

	deleteOverLimitMessage(-100, 1)
	deleteOverLimitMessage(-100, 2)
	deleteOverLimitMessage(-200, 3)

	if (strings.Count(logged.String(), "Delete messages") != 2) || (*calls != 0) {
		t.Fatalf("expected one log line per chat and no lookup, got %d lookups, %q", *calls, logged.String())
	}
}

func TestUndecidedRefusalLooksTheRightUpOnlyUntilTheChatIsLogged(t *testing.T) {
	calls := useFetchedDeleteRight(t, false, nil)
	useDeleteAnswer(t, cantBeDeletedDescription, errors.New("API error"))
	logged := captureLog(t)

	deleteOverLimitMessage(-100, 1)
	botDeleteRights = newTTLStore(botDeleteRightTTL)
	deleteOverLimitMessage(-100, 2)

	if (strings.Count(logged.String(), "Delete messages") != 1) || (*calls != 1) {
		t.Fatalf("expected one lookup and one log line, got %d lookups, %q", *calls, logged.String())
	}
}

func TestUndecidedRefusalWithTheRightIsLoggedAsAFailure(t *testing.T) {
	useFetchedDeleteRight(t, true, nil)
	useDeleteAnswer(t, cantBeDeletedDescription, errors.New("API error"))
	logged := captureLog(t)

	deleteOverLimitMessage(-100, 1)

	if (strings.Contains(logged.String(), "Delete messages") == true) || (strings.Contains(logged.String(), "not deleted") == false) {
		t.Fatalf("expected a failure, not a missing right, got %q", logged.String())
	}
}

func TestUndecidedRefusalWithAnUnknownRightIsLoggedAsAFailure(t *testing.T) {
	useFetchedDeleteRight(t, false, errors.New("getChatMember failed"))
	useDeleteAnswer(t, cantBeDeletedDescription, errors.New("API error"))
	logged := captureLog(t)

	deleteOverLimitMessage(-100, 1)

	if (strings.Contains(logged.String(), "Delete messages") == true) || (strings.Contains(logged.String(), "not deleted") == false) {
		t.Fatalf("an unknown right must not be logged as missing, got %q", logged.String())
	}
}

func TestOtherDeleteFailuresAreLoggedWithoutALookup(t *testing.T) {
	calls := useFetchedDeleteRight(t, false, nil)
	useDeleteAnswer(t, notFoundDescription, errors.New("API error"))
	logged := captureLog(t)

	deleteOverLimitMessage(-100, 1)

	if (strings.Contains(logged.String(), "Delete messages") == true) || (strings.Contains(logged.String(), "not deleted") == false) || (*calls != 0) {
		t.Fatalf("expected a failure without a lookup, got %d lookups, %q", *calls, logged.String())
	}
}

func TestPrivateChatDeleteFailureLooksNothingUp(t *testing.T) {
	calls := useFetchedDeleteRight(t, false, nil)
	useDeleteAnswer(t, cantBeDeletedDescription, errors.New("API error"))
	logged := captureLog(t)

	deleteOverLimitMessage(7, 1)

	if (strings.Contains(logged.String(), "Delete messages") == true) || (*calls != 0) {
		t.Fatalf("expected no lookup in a private chat, got %d lookups, %q", *calls, logged.String())
	}
}

func TestConfirmedRightClearsTheLoggedMarkerSoARevokeIsLoggedAgain(t *testing.T) {
	useFetchedDeleteRight(t, false, nil)
	logged := captureLog(t)
	useDeleteAnswer(t, notEnoughRightsDescription, errors.New("API error"))
	deleteOverLimitMessage(-100, 1)
	useDeleteAnswer(t, "", nil)
	deleteOverLimitMessage(-100, 2)
	useDeleteAnswer(t, notEnoughRightsDescription, errors.New("API error"))
	deleteOverLimitMessage(-100, 3)

	if strings.Count(logged.String(), "Delete messages") != 2 {
		t.Fatalf("expected the revoke to be logged again, got %q", logged.String())
	}
}

func TestFreshCheckWithTheRightClearsTheLoggedMarker(t *testing.T) {
	useFetchedDeleteRight(t, true, nil)
	missingDeleteRightLogged.add("-100", true)

	limitDeleteRightNoteFor(-100, true)

	if _, isLogged := missingDeleteRightLogged.get("-100"); isLogged == true {
		t.Fatalf("expected the marker cleared after the right was confirmed")
	}
}

func TestRememberedRightIsAskedOnceAndAFailureIsNotRemembered(t *testing.T) {
	calls := useFetchedDeleteRight(t, false, errors.New("getChatMember failed"))

	canBotDeleteMessages(-100)
	canBotDeleteMessages(-100)

	if *calls != 2 {
		t.Fatalf("expected a failed check not to be remembered, got %d checks", *calls)
	}

	fetchBotDeleteRight = func(int64) (bool, error) {
		*calls++

		return true, nil
	}
	first, _ := canBotDeleteMessages(-100)
	second, _ := canBotDeleteMessages(-100)

	if (first == false) || (second == false) || (*calls != 3) {
		t.Fatalf("expected one remembered check, got %d checks", *calls)
	}
}

func TestSuccessfulGroupDeleteRemembersTheRightWithoutALookup(t *testing.T) {
	calls := useFetchedDeleteRight(t, false, nil)
	useDeleteAnswer(t, "", nil)

	deleteOverLimitMessage(-100, 1)
	canDelete, isRemembered := rememberedBotDeleteRight(-100)

	if (*calls != 0) || (canDelete == false) || (isRemembered == false) {
		t.Fatalf("expected the right remembered without a lookup, got %d lookups", *calls)
	}
}
