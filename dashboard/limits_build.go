package dashboard

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"time"
)

// Formats of the UTC usage period and of a limit request key.
const (
	limitPeriodLayout = "2006-01"
	limitKeyFormat    = "%s:%d"
)

// limitStateRanks orders the rows: nearest the limit first, then the rows
// without a limit to reach.
var limitStateRanks = map[string]int{
	limitStateFull:      0,
	limitStateNear:      1,
	limitStateOK:        2,
	limitStateUnlimited: 3,
	limitStateOff:       4,
	limitStateBlocked:   4,
}

// effectiveSettingValue is the value field of one published effective
// setting; the rest of the entry is not needed here.
type effectiveSettingValue struct {
	Value json.RawMessage `json:"value"`
}

// limitState resolves a row's limit the way the bot does: a group switched
// off is off; -1 is unlimited; 0 turns a group off and blocks a person;
// else the stored or the default limit (nil while the bot has not
// published its settings).
func limitState(row LimitRow, defaults LimitDefaults) LimitRow {
	result := row
	result.Source = limitSource(row.StoredLimit)
	result.EffectiveLimit = scopeDefault(row.Scope, defaults)

	if row.StoredLimit != nil {
		stored := *row.StoredLimit
		result.EffectiveLimit = &stored
	}

	result.Remaining = remainingLimit(result.EffectiveLimit, row.Used)
	result.State = rowState(result)

	return result
}

// limitSource names where a row's limit comes from.
func limitSource(stored *int64) string {
	result := limitSourceDefault

	if stored != nil {
		result = limitSourceCustom
	}

	if (stored != nil) && (*stored == limitUnlimited) {
		result = limitSourceUnlimited
	}

	return result
}

// scopeDefault is the default limit of a person or a group.
func scopeDefault(scope string, defaults LimitDefaults) *int64 {
	result := defaults.User

	if scope == limitScopeChat {
		result = defaults.Chat
	}

	return result
}

// remainingLimit is what is left of a limit, at least 0; nil when the limit
// is unknown or unlimited.
func remainingLimit(limit *int64, used int64) *int64 {
	var result *int64
	isCounted := (limit != nil) && (*limit != limitUnlimited)

	if isCounted == true {
		left := max(*limit-used, 0)
		result = &left
	}

	return result
}

// rowState is the state of a resolved row (see limitState).
func rowState(row LimitRow) string {
	result := limitStateOK
	limit := row.EffectiveLimit
	isChat := (row.Scope == limitScopeChat)
	isKnown := (limit != nil)
	isUnlimited := (isKnown == true) && (*limit == limitUnlimited)
	isZero := (isKnown == true) && (*limit == 0)
	isCounted := (isKnown == true) && (isUnlimited == false) && (isZero == false)

	switch {
	case (isChat == true) && (row.Enabled == false):
		result = limitStateOff
	case isUnlimited == true:
		result = limitStateUnlimited
	case (isChat == true) && (isZero == true):
		result = limitStateOff
	case isZero == true:
		result = limitStateBlocked
	case (isCounted == true) && (row.Used >= *limit):
		result = limitStateFull
	case (isCounted == true) && (float64(row.Used) >= (limitNearRatio * float64(*limit))):
		result = limitStateNear
	}

	return result
}

// sortLimitRows orders the rows nearest the limit first: full, near and ok
// by share of the limit used, then unlimited, then off and blocked; ties by
// use, then scope and id.
func sortLimitRows(rows []LimitRow) {
	slices.SortStableFunc(rows, compareLimitRows)
}

// compareLimitRows is the order of sortLimitRows.
func compareLimitRows(left LimitRow, right LimitRow) int {
	result := cmp.Compare(limitStateRanks[left.State], limitStateRanks[right.State])
	leftShare := usedShare(left)
	rightShare := usedShare(right)

	if result == 0 {
		result = cmp.Compare(rightShare, leftShare)
	}

	if result == 0 {
		result = cmp.Compare(right.Used, left.Used)
	}

	if result == 0 {
		result = cmp.Compare(left.Scope, right.Scope)
	}

	if result == 0 {
		result = cmp.Compare(left.ScopeID, right.ScopeID)
	}

	return result
}

// usedShare is the share of a positive limit used; 0 without one.
func usedShare(row LimitRow) float64 {
	result := 0.0
	limit := row.EffectiveLimit
	isPositive := (limit != nil) && (*limit > 0)

	if isPositive == true {
		result = float64(row.Used) / float64(*limit)
	}

	return result
}

// buildLimitsPage resolves and sorts the rows and adds what the bot
// published: its heartbeat, the rules, the defaults, the cap and the paid
// and relay state.
func buildLimitsPage(snapshot LimitsSnapshot, now time.Time) (*LimitsPage, error) {
	common, err := buildLimitsCommon(snapshot, now)
	period, resetsAt := limitPeriod(now)
	result := &LimitsPage{
		Period:   period,
		ResetsAt: resetsAt,
		Bot:      common.bot,
		Applying: common.isApplying,
		Rules:    common.rules,
		Defaults: common.defaults,
		Global:   GlobalUsage{Used: snapshot.GlobalUsed, Cap: common.globalCap, IsOff: common.isGlobalOff},
		Paid:     publishedComponent(snapshot.Components, componentPaid, now),
		Relay:    publishedComponent(snapshot.Components, componentRelay, now),
		Rows:     resolveLimitRows(snapshot, common.defaults),
		Total:    snapshot.Total,
	}
	sortLimitRows(result.Rows)
	result.Truncated = (snapshot.Total > int64(len(result.Rows)))

	return result, err
}

// buildLimitScope resolves the one row of a find-by-id snapshot.
func buildLimitScope(snapshot LimitsSnapshot, now time.Time) (*LimitScopeResponse, error) {
	common, err := buildLimitsCommon(snapshot, now)
	rows := resolveLimitRows(snapshot, common.defaults)
	result := &LimitScopeResponse{Defaults: common.defaults, Rules: common.rules, Applying: common.isApplying}

	if len(rows) > 0 {
		result.Row = rows[0]
	}

	return result, err
}

// limitsCommon is what both Limits answers read from the components.
type limitsCommon struct {
	bot         map[string]interface{}
	isApplying  bool
	rules       *SpecialLimitRules
	defaults    LimitDefaults
	globalCap   *int64
	isGlobalOff bool
}

// buildLimitsCommon decodes the heartbeat, the rules and the published
// limits.
func buildLimitsCommon(snapshot LimitsSnapshot, now time.Time) (limitsCommon, error) {
	result := limitsCommon{}
	var rulesErr error
	bot, botErr := botState(snapshot.Components, now)
	result.bot = bot
	result.isApplying = (bot != nil) && (bot[botFieldIsStale] == false) && (bot[botFieldAppliesRequests] == true)
	result.rules, rulesErr = decodeLimitRules(snapshot.Components)
	effective, settingsErr := decodeEffectiveSettings(snapshot.Components)
	result.defaults = LimitDefaults{User: effectiveInt(effective, settingUserAllowance), Chat: effectiveInt(effective, settingChatCap)}
	globalCap := effectiveInt(effective, settingGlobalCap)
	result.isGlobalOff = (globalCap != nil) && (*globalCap == 0)
	result.globalCap = globalCap

	if (globalCap != nil) && (*globalCap == limitUnlimited) {
		result.globalCap = nil
	}

	err := firstError(botErr, rulesErr, settingsErr)

	return result, err
}

// resolveLimitRows attaches the open requests and resolves every row.
func resolveLimitRows(snapshot LimitsSnapshot, defaults LimitDefaults) []LimitRow {
	result := make([]LimitRow, 0, len(snapshot.Rows))

	for _, row := range snapshot.Rows {
		key := limitKey(row.Scope, row.ScopeID)
		request, isOpen := snapshot.OpenRequests[key]

		if isOpen == true {
			row.OpenRequest = &request
		}

		resolved := limitState(row, defaults)
		result = append(result, resolved)
	}

	return result
}

// limitKey is the request key of a person or group.
func limitKey(scope string, id int64) string {
	result := fmt.Sprintf(limitKeyFormat, scope, id)

	return result
}

// limitPeriod is the UTC month of now and the start of the next one.
func limitPeriod(now time.Time) (string, string) {
	utc := now.UTC()
	start := time.Date(utc.Year(), utc.Month(), 1, 0, 0, 0, 0, time.UTC)
	next := start.AddDate(0, 1, 0)
	period := start.Format(limitPeriodLayout)
	resetsAt := formatTime(next)

	return period, resetsAt
}

// decodeLimitRules reads registry.special_limit; nil while the running bot
// does not publish it (it does not take limit requests yet).
func decodeLimitRules(components map[string]StatusComponent) (*SpecialLimitRules, error) {
	registry, err := decodeRegistry(components)
	result := supportedLimitRules(registry)

	return result, err
}

// supportedLimitRules are the registry's limit rules when they are for the
// kind this dashboard sends; nil otherwise.
func supportedLimitRules(registry SettingsRegistry) *SpecialLimitRules {
	result := registry.SpecialLimit

	if (result != nil) && (result.Kind != limitRequestKind) {
		result = nil
	}

	return result
}

// decodeEffectiveSettings reads the published effective values by key; an
// empty map when they are not published.
func decodeEffectiveSettings(components map[string]StatusComponent) (map[string]effectiveSettingValue, error) {
	result := map[string]effectiveSettingValue{}
	var err error
	component, isPublished := components[componentSettings]

	if isPublished == true {
		err = json.Unmarshal(component.Data, &result)
	}

	return result, err
}

// effectiveInt is a published whole-number setting; nil when it is missing
// or not a whole number.
func effectiveInt(effective map[string]effectiveSettingValue, key string) *int64 {
	var result *int64
	entry, isPublished := effective[key]
	text := string(bytes.TrimSpace(entry.Value))
	value, err := strconv.ParseInt(text, intValueBase, intValueBits)

	if (isPublished == true) && (err == nil) {
		result = &value
	}

	return result
}

// publishedComponent is a component with its staleness; nil when the bot
// never published it.
func publishedComponent(components map[string]StatusComponent, name string, now time.Time) *PublishedComponent {
	var result *PublishedComponent
	component, isPublished := components[name]

	if isPublished == true {
		age := now.Sub(component.UpdatedAt)
		result = &PublishedComponent{Data: component.Data, UpdatedAt: formatTime(component.UpdatedAt), IsStale: (age > heartbeatStaleAfter)}
	}

	return result
}
