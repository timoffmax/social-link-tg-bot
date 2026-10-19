package botsettings

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/timoffmax/social-link-bot/config"
	"github.com/timoffmax/social-link-bot/model/settings"
)

// Reasons of a rejected or expired request, shown in the dashboard.
const (
	requestExpiredError       = "expired: the bot did not apply it within 10 minutes"
	requestUnknownKindError   = "unknown request kind %q"
	requestUnknownKeyError    = "unknown setting %q"
	requestChangedError       = "changed meanwhile, reload"
	requestNoApproverError    = "needs the owner's approval, but ADMIN_USER_ID is not set"
	requestTooManyAwaiting    = "too many changes waiting for approval"
	requestAskFailedError     = "could not ask the owner in Telegram"
	requestOwnerRejectedError = "rejected by the owner in Telegram"
	requestEncodeError        = "could not encode the value: %v"
	requestMissingExpectation = "the request has no expected value"
)

// requestApprovalExpiredError: the owner did not tap within approvalTimeout.
var requestApprovalExpiredError = fmt.Sprintf(requestApprovalExpiredFormat, approvalMinutes)

// requestApprovalExpiredFormat takes the minutes of approvalTimeout.
const requestApprovalExpiredFormat = "expired: the owner did not answer in Telegram within %d minutes"

// Approval limits: how long the owner has to tap, and how many requests may
// wait at once, so the owner is not flooded.
const (
	approvalTimeout     = 15 * time.Minute
	maxAwaitingApproval = 3
)

// approvalMinutes is approvalTimeout in whole minutes, for the texts.
const approvalMinutes = int(approvalTimeout / time.Minute)

// fallbackReader returns the value a key has without an owner-set one (.env,
// then the default); a variable so tests run without .env.
var fallbackReader = config.GetFallbackSetting

// decideRequest rules on a claimed, unexpired request by its kind.
func decideRequest(request ChangeRequest, canAskApproval bool) Decision {
	result := rejectedf(requestUnknownKindError, request.Kind)

	switch request.Kind {
	case settings.KindSetting:
		result = decideSettingRequest(request, canAskApproval)
	case settings.KindSpecialLimit:
		result = decideSpecialLimitRequest(request, canAskApproval)
	}

	return result
}

// decideSettingRequest applies only a known setting whose current value is
// still the one the page showed, with a valid new value; one that needs the
// owner's approval waits for it (rejected without an owner to ask, or with
// maxAwaitingApproval waiting already). A reset (no value) deletes the row,
// so .env or the default applies; its approval rule is computed on that
// resulting value.
func decideSettingRequest(request ChangeRequest, canAskApproval bool) Decision {
	setting, isKnown := settings.Lookup(request.Key)
	current := currentValue(setting, request.StoredCurrent)
	next, nextErr := requestedValue(setting, request)
	needsApproval := setting.NeedsApproval(current, next)
	isFull := (request.AwaitingCount >= maxAwaitingApproval)
	result := Decision{Status: settings.StatusApplied, TypedValue: next}

	switch {
	case isKnown == false:
		result = rejectedf(requestUnknownKeyError, request.Key)
	case request.Expected == nil:
		result = rejected(requestMissingExpectation)
	case settings.SameValue(request.Expected, current) == false:
		result = rejected(requestChangedError)
	case nextErr != nil:
		result = rejectedf("%v", nextErr)
	case (needsApproval == true) && (canAskApproval == false):
		result = rejected(requestNoApproverError)
		result.NeedsApproval = true
	case (needsApproval == true) && (isFull == true):
		result = rejected(requestTooManyAwaiting)
		result.NeedsApproval = true
	case needsApproval == true:
		result = Decision{Status: settings.StatusAwaitingApproval, NeedsApproval: true, TypedValue: next}
	}

	result = withEncodedValue(result, request)

	return result
}

// decideApproved rules on a request the owner approved: the same checks
// again (the value must still be the one the message showed), then it
// applies whatever the approval rule says now.
func decideApproved(request ChangeRequest) Decision {
	request.AwaitingCount = 0
	result := decideRequest(request, true)

	if result.IsAwaitingApproval() == true {
		result.Status = settings.StatusApplied
		result = withEncodedValue(result, request)
	}

	return result
}

// withEncodedValue sets the JSON to store of an applied, non-reset decision.
func withEncodedValue(decision Decision, request ChangeRequest) Decision {
	result := decision
	var err error

	if (result.IsApplied() == true) && (request.IsReset() == false) {
		result.Value, err = json.Marshal(result.TypedValue)
	}

	if (result.IsApplied() == true) && (err != nil) {
		result = rejectedf(requestEncodeError, err)
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
