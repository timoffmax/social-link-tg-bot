package dashboard

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
)

// The lowest stored limit expected may name: -1 (unlimited, set with /gated).
const limitExpectedMin = limitUnlimited

// Query parameters of GET /api/limits/scope.
const (
	paramScope = "scope"
	paramID    = "id"
)

// Validation messages of the limit requests.
const (
	errLimitScope        = "scope must be user or chat"
	errLimitID           = "id must be a person's id (above 0) or a group's id (below 0)"
	errLimitRequired     = "limit is required (null sets the default again)"
	errLimitExpected     = "expected is required: send the stored limit the page showed (null when none)"
	errLimitValue        = "limit must be a whole number from %d to %d, or null for the default; unlimited only with /gated in Telegram"
	errLimitExpectedForm = "expected must be null or a whole number from -1"
	errLimitRejectedByDB = "the database refused this limit request"
)

// errNotWholeLimit is the failure of a limit that is not a whole number.
var errNotWholeLimit = errors.New(errSettingNotWhole)

// limitRequestDraft checks a limit request against the rules the bot
// published and turns it into the request to queue: key scope:id, value
// the limit (nil for null, a reset), expected the stored limit or null.
func limitRequestDraft(body limitRequestBody, rules *SpecialLimitRules) (SettingRequestDraft, error) {
	var err error
	scope := strings.TrimSpace(body.Scope)
	limit, isLimitNull, limitErr := parseLimitJSON(body.Limit)
	expected, isExpectedNull, expectedErr := parseLimitJSON(body.Expected)
	isScope := (scope == limitScopeUser) || (scope == limitScopeChat)
	isLimitInRange := (rules != nil) && (limit >= rules.Min) && (limit <= rules.Max)
	isLimitValid := (isLimitNull == true) || ((limitErr == nil) && (isLimitInRange == true))
	isExpectedValid := (isExpectedNull == true) || ((expectedErr == nil) && (expected >= limitExpectedMin))

	switch {
	case rules == nil:
		err = ErrLimitsNotSupported
	case isScope == false:
		err = badRequest(errLimitScope)
	case isLimitScopeID(scope, body.ID) == false:
		err = badRequest(errLimitID)
	case len(body.Limit) == 0:
		err = badRequest(errLimitRequired)
	case len(body.Expected) == 0:
		err = badRequest(errLimitExpected)
	case isLimitValid == false:
		err = badRequest(errLimitValue, rules.Min, rules.Max)
	case isExpectedValid == false:
		err = badRequest(errLimitExpectedForm)
	}

	result := SettingRequestDraft{
		Key:      limitKey(scope, body.ID),
		Value:    limitJSON(limit, isLimitNull),
		Expected: expectedJSON(expected, isExpectedNull),
	}

	return result, err
}

// isLimitScopeID reports a person's id above 0 or a group's id below 0.
func isLimitScopeID(scope string, id int64) bool {
	isUser := (scope == limitScopeUser) && (id > 0)
	isChat := (scope == limitScopeChat) && (id < 0)
	result := (isUser == true) || (isChat == true)

	return result
}

// parseLimitJSON reads a raw limit: JSON null, or one whole number; an
// error for anything else (also a missing value).
func parseLimitJSON(raw json.RawMessage) (int64, bool, error) {
	var result int64
	isNull := (strings.TrimSpace(string(raw)) == jsonNull)
	number, err := decodeAs[json.Number](raw, errNotWholeLimit)

	if err == nil {
		text := number.String()
		result, err = strconv.ParseInt(text, intValueBase, intValueBits)
	}

	if isNull == true {
		err = nil
	}

	return result, isNull, err
}

// limitJSON is the request value: nil (SQL NULL, a reset) for null, else
// the number.
func limitJSON(limit int64, isNull bool) json.RawMessage {
	var result json.RawMessage

	if isNull == false {
		text := strconv.FormatInt(limit, intValueBase)
		result = json.RawMessage(text)
	}

	return result
}

// expectedJSON is the request's expected: JSON null or the number.
func expectedJSON(expected int64, isNull bool) json.RawMessage {
	result := json.RawMessage(jsonNull)

	if isNull == false {
		text := strconv.FormatInt(expected, intValueBase)
		result = json.RawMessage(text)
	}

	return result
}

// parseLimitScope reads scope and id of GET /api/limits/scope.
func parseLimitScope(scopeText string, idText string) (string, int64, error) {
	var err error
	scope := strings.TrimSpace(scopeText)
	trimmedID := strings.TrimSpace(idText)
	id, idErr := strconv.ParseInt(trimmedID, intValueBase, intValueBits)
	isScope := (scope == limitScopeUser) || (scope == limitScopeChat)

	switch {
	case isScope == false:
		err = badRequest(errLimitScope)
	case (idErr != nil) || (isLimitScopeID(scope, id) == false):
		err = badRequest(errLimitID)
	}

	return scope, id, err
}
