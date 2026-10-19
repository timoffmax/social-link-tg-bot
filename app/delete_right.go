package app

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/timoffmax/social-link-bot/model/api/request/message"
	"github.com/timoffmax/social-link-bot/model/api/request/payload"
	"github.com/timoffmax/social-link-bot/services"
)

// limitDeleteRightNote ends a limit confirmation in a group where the bot
// can't delete messages: the limit works, but messages over it stay.
const limitDeleteRightNote = "\n\n⚠️ <b>Messages over the limit will stay in the chat.</b> To have them removed, make me an admin with the \"Delete messages\" right."

// missingDeleteRightLogTTL is how long a chat's missing "Delete messages"
// right stays logged, so the log names it about once a day, not per message.
const missingDeleteRightLogTTL = 24 * time.Hour

// botDeleteRightTTL is how long the bot's "Delete messages" right in a chat
// is remembered, so a flood of over-limit messages asks Telegram once.
const botDeleteRightTTL = 5 * time.Minute

// anyUserID asks isLimitInForce about everyone in the chat (a group limit
// change) instead of one user; Telegram user ids are never 0.
const anyUserID int64 = 0

// missingDeleteRightLogged holds the chats whose missing right was logged.
var missingDeleteRightLogged = newTTLStore(missingDeleteRightLogTTL)

// botDeleteRights remembers the bot's "Delete messages" right per chat;
// the store drops entries of idle chats when it is written to.
var botDeleteRights = newTTLStore(botDeleteRightTTL)

// fetchBotDeleteRight asks Telegram for the bot's "Delete messages" right;
// a variable for tests.
var fetchBotDeleteRight = services.FetchBotDeleteRight

// effectiveUserLimit returns the daily limit shouldEnforceLimit enforces on
// a user in a chat; a variable for tests.
var effectiveUserLimit = services.GetEffectiveUserLimit

// noIndividualLimit returns the daily limit of users without an individual
// limit in a chat; a variable for tests.
var noIndividualLimit = services.GetNoIndividualLimit

// anyDailyLimitInForce reports whether anyone in a chat has a daily limit
// above 0; a variable for tests.
var anyDailyLimitInForce = services.IsAnyDailyLimitInForce

// deleteChatMessage deletes a message and returns Telegram's description of
// a refusal ("" without an answer) and the error; a variable for tests.
var deleteChatMessage = func(deletePayload payload.TgPlDeleteMessage) (string, error) {
	deletion := message.NewDeleteMessage(deletePayload)
	result := ""

	if (deletion.Err != nil) && (deletion.Response.StatusCode != 0) {
		result = deletion.Response.Message
	}

	return result, deletion.Err
}

// deleteRightRefusals are parts of Telegram's descriptions (lower case) of a
// request refused because the bot isn't an admin with the needed right.
var deleteRightRefusals = []string{"not enough rights", "chat_admin_required", "need administrator rights"}

// deleteRefusalUndecided is Telegram's description of a deleteMessage it
// refuses both when the bot lacks the "Delete messages" right in a group and
// when the message can't be deleted at all (too old, a service message), so
// the right is looked up to tell which.
const deleteRefusalUndecided = "bad request: message can't be deleted"

// Kinds of deleteMessage outcomes.
const (
	deleteDone = iota
	deleteRefusedForTheRight
	deleteRefusedUndecided
	deleteFailed
)

// isPrivateChatID tells a private chat by its id: users' ids are positive,
// groups' negative.
func isPrivateChatID(chatID int64) bool {
	result := (chatID > 0)

	return result
}

// isLimitInForce reports whether a daily limit above 0 is enforced, decided
// by the same reads shouldEnforceLimit uses (GetEffectiveUserLimit, which
// enforces nothing without a database or on a read error): on the user, or
// on anyone in the chat for anyUserID, counting users without an
// individual limit, users whose individual limit is 0 and users with one.
func isLimitInForce(chatID int64, userID int64) bool {
	var err error
	result := false

	if userID == anyUserID {
		result, err = anyDailyLimitInForce(chatID)
	}

	if userID != anyUserID {
		limit, _, limitErr := effectiveUserLimit(userID, chatID)
		result = (limit > 0)
		err = limitErr
	}

	result = (err == nil) && (result == true)

	return result
}

// limitDeleteRightNoteFor returns the note for a limit confirmation: set
// only while a limit is still enforced after the change (see
// isLimitInForce) in a group where the bot is known to lack the "Delete
// messages" right (an unknown right claims nothing). The right is asked
// afresh, so a right granted a moment ago is never reported missing.
func limitDeleteRightNoteFor(chatID int64, isInForce bool) string {
	result := ""
	isAsked := (isInForce == true) && (isPrivateChatID(chatID) == false)

	if isAsked == true {
		canDelete, err := refreshBotDeleteRight(chatID)
		isMissing := (err == nil) && (canDelete == false)

		if isMissing == true {
			result = limitDeleteRightNote
		}
	}

	return result
}

// Status lines of a removed limit: the limit that applies afterwards.
const (
	userLimitFallsBackTemplate  = "🔒 Status: falls back to the group limit of <b>%d</b> media items per day\n"
	userLimitStillTemplate      = "🔒 Status: a limit of <b>%d</b> media items per day still applies\n"
	userLimitUnlimitedText      = "💚 Status: <b>Unlimited</b>\n"
	groupLimitDefaultTemplate   = "🔒 <b>Status:</b> the default limit of %d media items per day applies to users never given an individual limit; users whose individual limit was removed now have no limit\n"
	groupLimitStillTemplate     = "🔒 <b>Status:</b> a limit of %d media items per day applies to users without an individual limit\n"
	groupLimitNoneText          = "💚 <b>Status:</b> no limit for users without an individual limit\n"
	removedLimitUnreadText      = "❔ Status: the limit now in force couldn't be read\n"
	removedGroupLimitUnreadText = "❔ <b>Status:</b> the limit now in force couldn't be read\n"
)

// removedUserLimitStatus returns the status line after /setlimit 0: the
// limit the user really has now (an individual limit of 0 falls back to the
// group limit).
func removedUserLimitStatus(userID int64, chatID int64) string {
	limit, isGroupLimit, err := effectiveUserLimit(userID, chatID)
	result := userLimitUnlimitedText

	if limit > 0 {
		result = fmt.Sprintf(userLimitStillTemplate, limit)
	}

	if (limit > 0) && (isGroupLimit == true) {
		result = fmt.Sprintf(userLimitFallsBackTemplate, limit)
	}

	if err != nil {
		result = removedLimitUnreadText
	}

	return result
}

// removedGroupLimitStatus returns the status line after the group limit is
// removed: the limit users without an individual limit really have now
// (DEFAULT_DAILY_MEDIA_LIMIT when it is above 0, for users without a limit
// row; a removed individual limit, a row of 0, follows the group limit).
func removedGroupLimitStatus(chatID int64) string {
	limit, isGroupLimit, err := noIndividualLimit(chatID)
	result := groupLimitNoneText

	if limit > 0 {
		result = fmt.Sprintf(groupLimitStillTemplate, limit)
	}

	if (limit > 0) && (isGroupLimit == false) {
		result = fmt.Sprintf(groupLimitDefaultTemplate, limit)
	}

	if err != nil {
		result = removedGroupLimitUnreadText
	}

	return result
}

// canBotDeleteMessages returns the bot's "Delete messages" right in the
// chat, remembered for botDeleteRightTTL, for the over-limit flood path. A
// failed check returns false with the error and is not remembered.
func canBotDeleteMessages(chatID int64) (bool, error) {
	var err error
	result, isRemembered := rememberedBotDeleteRight(chatID)

	if isRemembered == false {
		result, err = refreshBotDeleteRight(chatID)
	}

	return result, err
}

// refreshBotDeleteRight asks Telegram for the bot's "Delete messages" right
// afresh and remembers the answer; for limit confirmations, so a right
// granted or removed a moment ago is never reported stale.
func refreshBotDeleteRight(chatID int64) (bool, error) {
	result, err := fetchBotDeleteRight(chatID)

	if err == nil {
		rememberBotDeleteRight(chatID, result)
	}

	return result, err
}

// rememberedBotDeleteRight returns the chat's remembered right and whether
// one is remembered.
func rememberedBotDeleteRight(chatID int64) (bool, bool) {
	result := false
	chatKey := strconv.FormatInt(chatID, 10)
	value, isRemembered := botDeleteRights.get(chatKey)

	if isRemembered == true {
		result = (value == true)
	}

	return result, isRemembered
}

// rememberBotDeleteRight stores the chat's right; a confirmed right clears
// the chat's logged missing right, so a later revoke is logged again.
func rememberBotDeleteRight(chatID int64, canDelete bool) {
	chatKey := strconv.FormatInt(chatID, 10)
	botDeleteRights.set(chatKey, canDelete)

	if canDelete == true {
		missingDeleteRightLogged.remove(chatKey)
	}
}

// deleteOutcomeOf sorts a deleteMessage outcome by Telegram's description.
// A private chat never counts as a refusal for the right.
func deleteOutcomeOf(chatID int64, description string, err error) int {
	result := deleteFailed
	text := strings.ToLower(description)

	if err == nil {
		result = deleteDone
	}

	if (err != nil) && (text == deleteRefusalUndecided) {
		result = deleteRefusedUndecided
	}

	if (err != nil) && (isRightRefusal(text) == true) {
		result = deleteRefusedForTheRight
	}

	if (err != nil) && (isPrivateChatID(chatID) == true) {
		result = deleteFailed
	}

	return result
}

// isRightRefusal reports whether a lower-case description says the bot
// lacks admin rights.
func isRightRefusal(text string) bool {
	result := false

	for _, refusal := range deleteRightRefusals {
		result = (result == true) || (strings.Contains(text, refusal) == true)
	}

	return result
}

// deleteOverLimitMessage deletes a message sent over a daily limit. A
// deletion in a group confirms the right; a refusal for the right is logged
// once per chat (see reportMissingDeleteRight); other failures are logged
// as they are.
func deleteOverLimitMessage(chatID int64, messageID int) {
	deletePayload := payload.TgPlDeleteMessage{ChatId: chatID, MessageId: messageID}
	description, err := deleteChatMessage(deletePayload)
	outcome := deleteOutcomeOf(chatID, description, err)
	isGroupDeletion := (outcome == deleteDone) && (isPrivateChatID(chatID) == false)
	isRightRefusal := (outcome == deleteRefusedForTheRight) || (outcome == deleteRefusedUndecided)
	isLogged := false

	if isGroupDeletion == true {
		rememberBotDeleteRight(chatID, true)
	}

	if isRightRefusal == true {
		isLogged = reportMissingDeleteRight(chatID, outcome == deleteRefusedUndecided)
	}

	if (outcome == deleteFailed) || ((outcome == deleteRefusedUndecided) && (isLogged == false)) {
		log.Printf("⚠️ Chat %d: message %d over the daily limit not deleted: %v", chatID, messageID, err)
	}
}

// reportMissingDeleteRight logs a chat where the bot can't delete messages,
// once per missingDeleteRightLogTTL: the logged marker is checked first, so
// a logged chat asks Telegram nothing; an undecided refusal is then checked
// against the right. It reports whether the chat was already logged or is
// logged now, so an undecided refusal with the right is logged as a failure.
func reportMissingDeleteRight(chatID int64, isUndecided bool) bool {
	chatKey := strconv.FormatInt(chatID, 10)
	_, result := missingDeleteRightLogged.get(chatKey)
	isMissing := false

	if result == false {
		isMissing = isDeleteRightMissing(chatID, isUndecided)
	}

	if isMissing == true {
		result = true
		isMissing = missingDeleteRightLogged.add(chatKey, true)
	}

	if isMissing == true {
		log.Printf("🚫 Chat %d: can't remove messages over the daily limit, the bot isn't an admin with the \"Delete messages\" right", chatID)
	}

	return result
}

// isDeleteRightMissing reports whether the bot lacks the right after a
// refusal: Telegram said so, or, for an undecided refusal, the right is
// known to be missing (an unknown right claims nothing).
func isDeleteRightMissing(chatID int64, isUndecided bool) bool {
	result := true

	if isUndecided == true {
		canDelete, err := canBotDeleteMessages(chatID)
		result = (err == nil) && (canDelete == false)
	}

	return result
}
