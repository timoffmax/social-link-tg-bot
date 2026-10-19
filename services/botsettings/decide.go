package botsettings

import (
	"encoding/json"
	"fmt"

	"github.com/timoffmax/social-link-bot/config"
	"github.com/timoffmax/social-link-bot/model/settings"
)

// Reasons of a rejected or expired request, shown in the dashboard.
const (
	requestExpiredError       = "expired: the bot did not apply it within 10 minutes"
	requestUnknownKindError   = "unknown request kind %q"
	requestUnknownKeyError    = "unknown setting %q"
	requestChangedError       = "changed meanwhile, reload"
	requestApprovalError      = "needs the owner's approval in Telegram, which is not available yet"
	requestEncodeError        = "could not encode the value: %v"
	requestMissingExpectation = "the request has no expected value"
)

// fallbackReader returns the value a key has without an owner-set one (.env,
// then the default); a variable so tests run without .env.
var fallbackReader = config.GetFallbackSetting

// decideRequest rules on a claimed, unexpired request: it applies only a
// known setting whose current value is still the one the page showed, with
// a valid new value that does not need the owner's approval. A reset (no
// value) deletes the row, so .env or the default applies; its approval rule
// is computed on that resulting value.
func decideRequest(request ChangeRequest) Decision {
	setting, isKnown := settings.Lookup(request.Key)
	current := currentValue(setting, request.StoredCurrent)
	next, nextErr := requestedValue(setting, request)
	result := Decision{Status: settings.StatusApplied, TypedValue: next}

	switch {
	case request.Kind != settings.KindSetting:
		result = rejectedf(requestUnknownKindError, request.Kind)
	case isKnown == false:
		result = rejectedf(requestUnknownKeyError, request.Key)
	case request.Expected == nil:
		result = rejected(requestMissingExpectation)
	case settings.SameValue(request.Expected, current) == false:
		result = rejected(requestChangedError)
	case nextErr != nil:
		result = rejectedf("%v", nextErr)
	case setting.NeedsApproval(current, next) == true:
		result = rejected(requestApprovalError)
		result.NeedsApproval = true
	}

	if (result.IsApplied() == true) && (request.IsReset() == false) {
		result.Value, nextErr = json.Marshal(next)
	}

	if (result.IsApplied() == true) && (nextErr != nil) {
		result = rejectedf(requestEncodeError, nextErr)
	}

	return result
}

// rejected is a rejection with its reason.
func rejected(reason string) Decision {
	result := Decision{Status: settings.StatusRejected, Error: reason}

	return result
}

// rejectedf is a rejection with a formatted reason.
func rejectedf(format string, args ...any) Decision {
	reason := fmt.Sprintf(format, args...)
	result := rejected(reason)

	return result
}

// currentValue is the value the bot uses for the key, from its stored
// value as it is in the database right now (see resolveStored).
func currentValue(setting settings.Setting, stored json.RawMessage) any {
	result, _, _ := resolveStored(setting, stored)

	return result
}

// requestedValue is the validated new value, or for a reset the .env /
// default value the key falls back to.
func requestedValue(setting settings.Setting, request ChangeRequest) (any, error) {
	var result any
	var err error

	if request.IsReset() == true {
		result, _ = fallbackReader(setting.Key)
	}

	if request.IsReset() == false {
		result, err = setting.Validate(request.Value)
	}

	return result, err
}

// resolveStored turns a stored value into the value the bot uses: the valid
// stored value; for an invalid one, the safe value of a money or payment
// key, else the .env / default value; without one, the .env / default
// value. It also returns the source and the validation error.
func resolveStored(setting settings.Setting, stored json.RawMessage) (any, string, error) {
	var err error
	var result any
	source := settings.SourceDB
	isStored := (stored != nil)

	if isStored == true {
		result, err = setting.Validate(stored)
	}

	if (isStored == true) && (err != nil) && (setting.SafeValue != nil) {
		result = setting.SafeValue
		source = settings.SourceSafe
	}

	if (isStored == false) || ((err != nil) && (setting.SafeValue == nil)) {
		result, _ = fallbackReader(setting.Key)
		source = ""
	}

	return result, source, err
}
