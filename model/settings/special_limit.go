package settings

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// KindSpecialLimit is the request kind of one person's or group's monthly
// special-download limit (applied to gated_fetch_policies, never to bot_settings).
const KindSpecialLimit = "special_limit"

// Scopes of a special-download limit and the separator of a request key.
const (
	SpecialLimitScopeUser    = "user"
	SpecialLimitScopeChat    = "chat"
	SpecialLimitKeySeparator = ":"
)

// SpecialLimitMin and SpecialLimitMax bound a limit set by a request; -1
// (unlimited) and higher values are set only with /gated in Telegram.
const (
	SpecialLimitMin = int64(0)
	SpecialLimitMax = int64(CeilingGatedMonthlyQuota)
)

// Errors of a limit request (shown in the dashboard as the rejection reason).
const (
	errSpecialLimitKey    = "invalid limit key %q: user:<id> with id above 0 or chat:<id> with id below 0"
	errSpecialLimitValue  = "the monthly limit must be a whole number from %d to %d (unlimited and higher only with /gated in Telegram)"
	errSpecialLimitStored = "the stored limit %s is not a whole number or null"
)

// specialLimitIDBase is the base of the id in a request key.
const specialLimitIDBase = 10

// SpecialLimitTarget is the person or group a limit request names.
type SpecialLimitTarget struct {
	Scope string
	ID    int64
}

// SpecialLimitRules are published in the registry so the dashboard checks
// requests against the running bot's rules.
type SpecialLimitRules struct {
	Kind string `json:"kind"`
	Min  int64  `json:"min"`
	Max  int64  `json:"max"`
}

// SpecialLimits returns the rules of a limit request.
func SpecialLimits() SpecialLimitRules {
	result := SpecialLimitRules{Kind: KindSpecialLimit, Min: SpecialLimitMin, Max: SpecialLimitMax}

	return result
}

// ParseSpecialLimitKey reads user:<id> (id > 0) or chat:<id> (id < 0); the
// id must be written the canonical way (no sign, zeros or separators extra).
func ParseSpecialLimitKey(key string) (SpecialLimitTarget, error) {
	result := SpecialLimitTarget{}
	scope, idText, hasSeparator := strings.Cut(key, SpecialLimitKeySeparator)
	id, parseErr := strconv.ParseInt(idText, specialLimitIDBase, 64)
	canonical := strconv.FormatInt(id, specialLimitIDBase)
	isUser := (scope == SpecialLimitScopeUser) && (id > 0)
	isChat := (scope == SpecialLimitScopeChat) && (id < 0)
	isValid := (hasSeparator == true) && (parseErr == nil) && (canonical == idText) && ((isUser == true) || (isChat == true))
	err := fmt.Errorf(errSpecialLimitKey, key)

	if isValid == true {
		result = SpecialLimitTarget{Scope: scope, ID: id}
		err = nil
	}

	return result, err
}

// ValidateSpecialLimit checks a requested limit: nil is a reset (back to the
// default), else a whole number from SpecialLimitMin to SpecialLimitMax.
func ValidateSpecialLimit(raw json.RawMessage) (*int64, error) {
	var result *int64
	var err error

	if raw != nil {
		result, err = decodeWholeNumber(raw)
	}

	isOutOfRange := (result != nil) && ((*result < SpecialLimitMin) || (*result > SpecialLimitMax))
	isRefused := (raw != nil) && ((err != nil) || (result == nil) || (isOutOfRange == true))

	if isRefused == true {
		result = nil
		err = fmt.Errorf(errSpecialLimitValue, SpecialLimitMin, SpecialLimitMax)
	}

	return result, err
}

// ParseStoredSpecialLimit decodes a stored or expected limit: JSON null (or
// nothing) is the default, any whole number is kept (-1 and values above the
// ceiling come from /gated).
func ParseStoredSpecialLimit(raw json.RawMessage) (*int64, error) {
	var result *int64
	var err error

	if raw != nil {
		result, err = decodeWholeNumber(raw)
	}

	if err != nil {
		err = fmt.Errorf(errSpecialLimitStored, raw)
	}

	return result, err
}

// decodeWholeNumber decodes one JSON whole number, or nil for JSON null.
func decodeWholeNumber(raw json.RawMessage) (*int64, error) {
	var result *int64
	decoded, err := decodeStrict(raw)
	number, isNumber := decoded.(json.Number)
	isNull := (err == nil) && (decoded == nil)

	if (err == nil) && (isNull == false) && (isNumber == false) {
		err = ErrNotWholeNumber
	}

	if (err == nil) && (isNumber == true) {
		result, err = parseWholeNumber(number)
	}

	return result, err
}

// parseWholeNumber parses a JSON number that must be an int64.
func parseWholeNumber(number json.Number) (*int64, error) {
	var result *int64
	numberText := number.String()
	value, err := strconv.ParseInt(numberText, specialLimitIDBase, 64)

	if err != nil {
		err = ErrNotWholeNumber
	}

	if err == nil {
		result = &value
	}

	return result, err
}
