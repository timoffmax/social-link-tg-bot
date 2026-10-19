package botsettings

import (
	"fmt"

	"github.com/timoffmax/social-link-bot/model/settings"
)

// specialLimitChange is an applied limit request: who, and the stored limit
// before and after (nil = the default).
type specialLimitChange struct {
	Target settings.SpecialLimitTarget
	Old    *int64
	New    *int64
}

const (
	requestSpecialLimitUnchanged = "nothing to change: the limit already is %s"
	specialLimitDefaultText      = "the default"
)

// decideSpecialLimitRequest applies a limit request while the stored limit
// still equals expected and the new limit is valid and different; it never
// waits for approval (the owner raises limits himself; the digest reports it).
func decideSpecialLimitRequest(request ChangeRequest) Decision {
	target, keyErr := settings.ParseSpecialLimitKey(request.Key)
	current, currentErr := settings.ParseStoredSpecialLimit(request.StoredCurrent)
	expected, expectedErr := settings.ParseStoredSpecialLimit(request.Expected)
	next, nextErr := settings.ValidateSpecialLimit(request.Value)
	change := specialLimitChange{Target: target, Old: current, New: next}
	result := Decision{Status: settings.StatusApplied, TypedValue: change}

	switch {
	case keyErr != nil:
		result = rejectedf("%v", keyErr)
	case request.Expected == nil:
		result = rejected(requestMissingExpectation)
	case (currentErr != nil) || (expectedErr != nil):
		result = rejected(requestChangedError)
	case sameLimit(current, expected) == false:
		result = rejected(requestChangedError)
	case nextErr != nil:
		result = rejectedf("%v", nextErr)
	case sameLimit(current, next) == true:
		result = rejectedf(requestSpecialLimitUnchanged, limitText(current))
	}

	return result
}

// sameLimit reports two limits that are both the default or the same number.
func sameLimit(a *int64, b *int64) bool {
	result := ((a == nil) && (b == nil)) || ((a != nil) && (b != nil) && (*a == *b))

	return result
}

// limitText names a stored limit in a rejection.
func limitText(limit *int64) string {
	result := specialLimitDefaultText

	if limit != nil {
		result = fmt.Sprintf("%d", *limit)
	}

	return result
}
