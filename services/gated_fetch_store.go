package services

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/timoffmax/social-link-bot/config"
)

// GatedFetchScope names who a gated-fetch policy or usage counter belongs to.
type GatedFetchScope string

const (
	GatedFetchScopeGlobal GatedFetchScope = "global"
	GatedFetchScopeChat   GatedFetchScope = "chat"
	GatedFetchScopeUser   GatedFetchScope = "user"
)

// GatedFetchGlobalScopeID is the scope_id of the bot-wide counter.
const GatedFetchGlobalScopeID = 0

// GatedFetchDenial says why a paid gated-post fetch was not allowed; empty
// means allowed.
type GatedFetchDenial string

const (
	GatedFetchAllowed       GatedFetchDenial = ""
	GatedFetchDeniedOff     GatedFetchDenial = "off"
	GatedFetchDeniedChatOff GatedFetchDenial = "chat_off"
	GatedFetchDeniedChatCap GatedFetchDenial = "chat_cap"
	GatedFetchDeniedUserCap GatedFetchDenial = "user_allowance"
	GatedFetchDeniedGlobal  GatedFetchDenial = "global_cap"
	GatedFetchDeniedNoDB    GatedFetchDenial = "no_database"
	// GatedFetchDeniedBudget is this month's Apify budget used up: the paid
	// service is paused until the next month.
	GatedFetchDeniedBudget GatedFetchDenial = "budget"
	// GatedFetchDeniedBlocked is a user whose allowance is 0 (the owner's
	// override or the default): bonus credits can't override it. A chat with
	// a 0 cap is GatedFetchDeniedChatOff.
	GatedFetchDeniedBlocked GatedFetchDenial = "blocked"
)

// GatedFetchUnlimited is the stored monthly limit of a chat or user without a
// limit of its own (the global cap still applies).
const GatedFetchUnlimited = -1

// gatedFetchReserveTimeout caps the reservation transaction.
const gatedFetchReserveTimeout = 5 * time.Second

// gatedFetchPeriodLayout formats a usage period (a DATE column).
const gatedFetchPeriodLayout = "2006-01-02"

// GatedFetchQuota is one monthly counter a fetch must fit into. Period is
// fixed when the quota is resolved, so a refund after midnight at the end of
// the month still hits the month the unit was taken from.
type GatedFetchQuota struct {
	Scope   GatedFetchScope
	ScopeID int64
	Limit   int
	Denial  GatedFetchDenial
	Period  string
}

// GatedFetchUsage is a chat's or user's monthly usage right after a
// reservation; IsLimited is false for unlimited scopes.
type GatedFetchUsage struct {
	Used      int
	Limit     int
	IsLimited bool
}

// Remaining returns how many fetches are left this month.
func (u GatedFetchUsage) Remaining() int {
	result := u.Limit - u.Used

	if result < 0 {
		result = 0
	}

	return result
}

// GatedFetchPolicy is the stored policy of a chat or user.
type GatedFetchPolicy struct {
	Enabled      bool
	MonthlyLimit sql.NullInt64
}

// errGatedFetchNoDatabase is returned when the DB is not configured: usage
// can't be counted, so paid fetches fail closed.
var errGatedFetchNoDatabase = errors.New("database not available")

// gatedFetchPeriod returns the first day of the current UTC month.
func gatedFetchPeriod() string {
	firstDay := currentMonthStartUTC()
	result := firstDay.Format(gatedFetchPeriodLayout)

	return result
}

// ResolveGatedFetchQuotas returns the counters a fetch in this chat by this
// user must fit into: the global cap, plus the user's allowance in a private
// chat or the chat's cap in a group (unless the owner turned the group off).
func ResolveGatedFetchQuotas(chatID, userID int64, isPrivate bool) ([]GatedFetchQuota, GatedFetchDenial, error) {
	var result []GatedFetchQuota
	denial := GatedFetchAllowed
	var err error
	globalCap := config.GetGatedFetchGlobalMonthlyCap()
	isConfigured := IsGatedFetchConfigured()
	var scoped GatedFetchQuota
	period := gatedFetchPeriod()

	if isConfigured == false {
		denial = GatedFetchDeniedOff
	}

	if (denial == GatedFetchAllowed) && (IsDBInitialized() == false) {
		denial = GatedFetchDeniedNoDB
	}

	if denial == GatedFetchAllowed {
		scoped, denial, err = resolveScopedQuota(chatID, userID, isPrivate)
		scoped.Period = period
	}

	if (denial == GatedFetchAllowed) && (err == nil) {
		global := GatedFetchQuota{Scope: GatedFetchScopeGlobal, ScopeID: GatedFetchGlobalScopeID, Limit: globalCap, Denial: GatedFetchDeniedGlobal, Period: period}
		result = []GatedFetchQuota{global, scoped}
	}

	// Chats and users the owner made unlimited skip the shared fetch cap, so
	// strangers using it up can't block them; the dollar budget still applies.
	if (denial == GatedFetchAllowed) && (err == nil) && (scoped.Limit == GatedFetchUnlimited) {
		result = []GatedFetchQuota{scoped}
	}

	return result, denial, err
}

// resolveScopedQuota returns the user's private allowance or the group's cap.
func resolveScopedQuota(chatID, userID int64, isPrivate bool) (GatedFetchQuota, GatedFetchDenial, error) {
	result := GatedFetchQuota{Scope: GatedFetchScopeUser, ScopeID: userID, Denial: GatedFetchDeniedUserCap}
	denial := GatedFetchAllowed
	defaultLimit := config.GetGatedFetchUserMonthlyAllowance()

	if isPrivate == false {
		result = GatedFetchQuota{Scope: GatedFetchScopeChat, ScopeID: chatID, Denial: GatedFetchDeniedChatCap}
		defaultLimit = config.GetGatedFetchChatMonthlyCap()
	}

	policy, err := GetGatedFetchPolicy(result.Scope, result.ScopeID)
	result.Limit = effectiveGatedFetchLimit(policy, defaultLimit)

	if (err == nil) && (isPrivate == false) && (policy.Enabled == false) {
		denial = GatedFetchDeniedChatOff
	}

	if (err == nil) && (denial == GatedFetchAllowed) && (result.Limit == 0) {
		denial = zeroLimitDenial(isPrivate)
	}

	return result, denial, err
}

// zeroLimitDenial is the denial of a 0 limit: the chat is off in a group, the
// user is blocked in a private chat.
func zeroLimitDenial(isPrivate bool) GatedFetchDenial {
	result := GatedFetchDeniedChatOff

	if isPrivate == true {
		result = GatedFetchDeniedBlocked
	}

	return result
}

// effectiveGatedFetchLimit returns the stored override, or the default.
func effectiveGatedFetchLimit(policy GatedFetchPolicy, defaultLimit int) int {
	result := defaultLimit

	if policy.MonthlyLimit.Valid == true {
		result = int(policy.MonthlyLimit.Int64)
	}

	return result
}

// ReserveGatedFetch atomically takes one unit from every quota for the current
// month. It returns the denial of the first full quota (nothing is taken then).
func ReserveGatedFetch(quotas []GatedFetchQuota) (GatedFetchDenial, GatedFetchUsage, error) {
	result := GatedFetchAllowed
	usage := GatedFetchUsage{}
	db := GetDB()
	err := errGatedFetchNoDatabase
	var tx *sql.Tx
	background := context.Background()
	ctx, cancel := context.WithTimeout(background, gatedFetchReserveTimeout)

	defer cancel()

	if db != nil {
		tx, err = db.BeginTx(ctx, nil)
	}

	if err == nil {
		result, usage, err = reserveGatedFetchQuotas(ctx, tx, quotas)
	}

	if (tx != nil) && ((err != nil) || (result != GatedFetchAllowed)) {
		tx.Rollback()
	}

	if (tx != nil) && (err == nil) && (result == GatedFetchAllowed) {
		err = tx.Commit()
	}

	return result, usage, err
}

// reserveGatedFetchQuotas increments each counter inside tx unless it is full,
// and reports the chat's or user's usage after the reservation.
func reserveGatedFetchQuotas(ctx context.Context, tx *sql.Tx, quotas []GatedFetchQuota) (GatedFetchDenial, GatedFetchUsage, error) {
	result := GatedFetchAllowed
	usage := GatedFetchUsage{}
	var err error

	for _, quota := range quotas {
		isTaken := false
		used := 0

		if (result == GatedFetchAllowed) && (err == nil) {
			isTaken, used, err = takeGatedFetchUnit(ctx, tx, quota)
		}

		if (result == GatedFetchAllowed) && (err == nil) && (isTaken == false) {
			result = quota.Denial
		}

		isScopedLimit := (quota.Scope != GatedFetchScopeGlobal) && (quota.Limit != GatedFetchUnlimited)

		if (isTaken == true) && (isScopedLimit == true) {
			usage = GatedFetchUsage{Used: used, Limit: quota.Limit, IsLimited: true}
		}
	}

	return result, usage, err
}

// takeGatedFetchUnit adds one to a counter if it is below its limit and
// returns the counter's new value.
func takeGatedFetchUnit(ctx context.Context, tx *sql.Tx, quota GatedFetchQuota) (bool, int, error) {
	result := false
	used := 0
	query := `
		INSERT INTO gated_fetch_usage (scope, scope_id, period, used)
		VALUES ($1, $2, $3, 1)
		ON CONFLICT (scope, scope_id, period)
		DO UPDATE SET used = gated_fetch_usage.used + 1
		WHERE ($4 < 0 OR gated_fetch_usage.used < $4)
		RETURNING used`
	row := tx.QueryRowContext(ctx, query, string(quota.Scope), quota.ScopeID, quota.Period, quota.Limit)
	err := row.Scan(&used)

	if errors.Is(err, sql.ErrNoRows) == true {
		err = nil
	}

	if (err == nil) && (used > 0) {
		result = true
	}

	return result, used, err
}

// RefundGatedFetch gives back one unit to every given quota, in the month it
// was reserved in.
func RefundGatedFetch(quotas []GatedFetchQuota) error {
	db := GetDB()
	err := errGatedFetchNoDatabase
	query := `
		UPDATE gated_fetch_usage SET used = used - 1
		WHERE scope = $1 AND scope_id = $2 AND period = $3 AND used > 0`

	if db != nil {
		err = nil
	}

	for _, quota := range quotas {
		if err == nil {
			_, err = db.Exec(query, string(quota.Scope), quota.ScopeID, quota.Period)
		}
	}

	return err
}

// GetGatedFetchUsage returns this month's used count of a counter.
func GetGatedFetchUsage(scope GatedFetchScope, scopeID int64) (int, error) {
	result := 0
	db := GetDB()
	err := errGatedFetchNoDatabase
	query := `SELECT used FROM gated_fetch_usage WHERE scope = $1 AND scope_id = $2 AND period = $3`
	period := gatedFetchPeriod()

	if db != nil {
		err = db.QueryRow(query, string(scope), scopeID, period).Scan(&result)
	}

	if errors.Is(err, sql.ErrNoRows) == true {
		err = nil
	}

	return result, err
}

// GetGatedFetchPolicy returns the stored policy; a missing row is the default
// policy (enabled, default limit). Only an explicit /gated off disables it.
func GetGatedFetchPolicy(scope GatedFetchScope, scopeID int64) (GatedFetchPolicy, error) {
	background := context.Background()
	result, err := getGatedFetchPolicy(background, scope, scopeID)

	return result, err
}

// getGatedFetchPolicy reads a scope's policy within ctx; no row means the
// defaults.
func getGatedFetchPolicy(ctx context.Context, scope GatedFetchScope, scopeID int64) (GatedFetchPolicy, error) {
	result := GatedFetchPolicy{Enabled: true}
	db := GetDB()
	err := errGatedFetchNoDatabase
	query := `SELECT enabled, monthly_limit FROM gated_fetch_policies WHERE scope = $1 AND scope_id = $2`

	if db != nil {
		err = db.QueryRowContext(ctx, query, string(scope), scopeID).Scan(&result.Enabled, &result.MonthlyLimit)
	}

	if errors.Is(err, sql.ErrNoRows) == true {
		err = nil
	}

	return result, err
}

// SetGatedFetchEnabled turns the paid fetch on or off for a scope.
func SetGatedFetchEnabled(scope GatedFetchScope, scopeID int64, enabled bool) error {
	query := `
		INSERT INTO gated_fetch_policies (scope, scope_id, enabled)
		VALUES ($1, $2, $3)
		ON CONFLICT (scope, scope_id) DO UPDATE SET enabled = EXCLUDED.enabled`
	err := execGatedFetchPolicy(query, string(scope), scopeID, enabled)

	return err
}

// SetGatedFetchMonthlyLimit stores a monthly limit override for a scope; an
// invalid (NULL) limit resets it to the env default.
func SetGatedFetchMonthlyLimit(scope GatedFetchScope, scopeID int64, limit sql.NullInt64) error {
	query := `
		INSERT INTO gated_fetch_policies (scope, scope_id, enabled, monthly_limit)
		VALUES ($1, $2, true, $3)
		ON CONFLICT (scope, scope_id) DO UPDATE SET monthly_limit = EXCLUDED.monthly_limit`
	err := execGatedFetchPolicy(query, string(scope), scopeID, limit)

	return err
}

// execGatedFetchPolicy runs a policy upsert.
func execGatedFetchPolicy(query string, args ...interface{}) error {
	db := GetDB()
	err := errGatedFetchNoDatabase

	if db != nil {
		_, err = db.Exec(query, args...)
	}

	if err != nil {
		err = fmt.Errorf("failed to save gated fetch policy: %w", err)
	}

	return err
}

// IsRestrictedPostsOffered reports whether restricted posts are offered at
// all: they can be fetched, and private chats or groups get some by default.
func IsRestrictedPostsOffered() bool {
	userAllowance := config.GetGatedFetchUserMonthlyAllowance()
	chatCap := config.GetGatedFetchChatMonthlyCap()
	isConfigured := IsGatedFetchConfigured()
	result := (isConfigured == true) && ((userAllowance != 0) || (chatCap != 0))

	return result
}

// IsPaidServiceConfigured reports whether a paid service of the stories
// chain (Apify or HikerAPI, as INSTAGRAM_PAID_PROVIDERS lists them) can run
// at all: a token and a monthly budget above 0. It decides whether Stories
// are offered.
func IsPaidServiceConfigured() bool {
	result := isStoriesFetchConfigured()

	return result
}

// IsGatedFetchConfigured reports whether restricted posts can be fetched at
// all: a provider of INSTAGRAM_PAID_PROVIDERS that can run and a bot-wide
// monthly cap that isn't 0 (no cap by default).
func IsGatedFetchConfigured() bool {
	globalCap := config.GetGatedFetchGlobalMonthlyCap()
	providers := ConfiguredGatedProviders()
	result := (len(providers) > 0) && (globalCap != 0)

	return result
}
