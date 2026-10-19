package services

import (
	"github.com/timoffmax/social-link-bot/config"
)

// individualDailyLimitQuery tells whether a user in the chat has an
// individual daily limit above 0.
const individualDailyLimitQuery = `SELECT EXISTS (SELECT 1 FROM user_media_limits WHERE chat_id = $1 AND daily_limit > 0)`

// GetNoIndividualLimit returns the daily limit GetEffectiveUserLimit gives a
// user without an individual limit row in the chat, and whether it is the
// group limit: GetUserMediaLimit answers DEFAULT_DAILY_MEDIA_LIMIT for a
// missing row, which wins when above 0; otherwise the group limit applies.
// Without a database it is 0, as no limit is enforced then.
func GetNoIndividualLimit(chatID int64) (int, bool, error) {
	var err error
	result := 0
	isGroupLimit := false

	if IsDBInitialized() == true {
		result, isGroupLimit, err = noIndividualLimit(chatID)
	}

	return result, isGroupLimit, err
}

// noIndividualLimit picks the limit of GetNoIndividualLimit; the database is
// available.
func noIndividualLimit(chatID int64) (int, bool, error) {
	var err error
	result := config.GetDefaultDailyMediaLimit()
	isGroupLimit := (result <= 0)

	if isGroupLimit == true {
		result, err = GetGroupMediaLimit(chatID)
	}

	return result, isGroupLimit, err
}

// IsAnyDailyLimitInForce reports whether GetEffectiveUserLimit, which
// shouldEnforceLimit enforces, is above 0 for anyone in the chat: for users
// without an individual limit (GetNoIndividualLimit), for users whose
// individual limit is 0 (the group limit), or for users with an individual
// limit above 0. Without a database nothing is enforced, so it is false. A
// failed read returns false with the error.
func IsAnyDailyLimitInForce(chatID int64) (bool, error) {
	var err error
	result := false

	if IsDBInitialized() == true {
		result, err = anyDailyLimitInForce(chatID)
	}

	return result, err
}

// anyDailyLimitInForce reads what IsAnyDailyLimitInForce decides on; the
// database is available.
func anyDailyLimitInForce(chatID int64) (bool, error) {
	hasIndividualLimit := false
	groupLimit := 0
	noIndividual, _, err := noIndividualLimit(chatID)

	if err == nil {
		groupLimit, err = GetGroupMediaLimit(chatID)
	}

	if err == nil {
		err = GetDB().QueryRow(individualDailyLimitQuery, chatID).Scan(&hasIndividualLimit)
	}

	result := (err == nil) && isAnyDailyLimitInForce(noIndividual, groupLimit, hasIndividualLimit)

	return result, err
}

// isAnyDailyLimitInForce decides IsAnyDailyLimitInForce from the limit of
// users without an individual limit, the group limit (for users whose
// individual limit is 0) and whether an individual limit above 0 exists.
func isAnyDailyLimitInForce(noIndividualLimit int, groupLimit int, hasIndividualLimit bool) bool {
	result := (noIndividualLimit > 0) || (groupLimit > 0) || (hasIndividualLimit == true)

	return result
}
