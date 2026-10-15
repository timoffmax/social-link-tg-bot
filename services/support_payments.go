package services

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/timoffmax/social-link-bot/config"
	"strconv"
	"strings"
	"time"
)

// SupportTier is one fixed "support the developer" price in Telegram Stars
// and the bonus restricted-post credits given as a thank-you.
type SupportTier struct {
	Stars   int
	Credits int
}

const (
	supportTierSmallStars    = 25
	supportTierSmallCredits  = 10
	supportTierMediumStars   = 50
	supportTierMediumCredits = 25
	supportTierLargeStars    = 100
	supportTierLargeCredits  = 60
)

// supportTiers are the offered tiers, cheapest first.
var supportTiers = []SupportTier{
	{Stars: supportTierSmallStars, Credits: supportTierSmallCredits},
	{Stars: supportTierMediumStars, Credits: supportTierMediumCredits},
	{Stars: supportTierLargeStars, Credits: supportTierLargeCredits},
}

// SupportPayloadPrefix starts the invoice payload of a support payment.
const SupportPayloadPrefix = "support"

// BoostPayloadPrefix starts the invoice payload of a group boost.
const BoostPayloadPrefix = "boost"

// supportPayloadSeparator splits "support:<stars>:<user_id>" and
// "boost:<stars>:<user_id>:<chat_id>".
const supportPayloadSeparator = ":"

// Part counts of valid payloads.
const (
	supportPayloadParts = 3
	boostPayloadParts   = 4
)

// gatedFetchCreditUnit is the number of credits one restricted-post fetch uses.
const gatedFetchCreditUnit = 1

// supportStoreTimeout caps each payment/credit transaction.
const supportStoreTimeout = 5 * time.Second

// ErrSupportPayloadInvalid is returned for a payload that isn't a known tier.
var ErrSupportPayloadInvalid = errors.New("invalid support payload")

// ErrSupportPaymentNotRefundable is returned when no unrefunded payment with
// that charge id belongs to the user.
var ErrSupportPaymentNotRefundable = errors.New("no unrefunded support payment with this charge id for this user")

// SupportInvoice is a parsed support invoice payload; BoostChatID is the
// group a boost credits (0 for personal support).
type SupportInvoice struct {
	Tier        SupportTier
	UserID      int64
	BoostChatID int64
}

// SupportPayment is one received support payment.
type SupportPayment struct {
	ChargeID   string
	UserID     int64
	ChatID     int64
	Stars      int
	Credits    int
	Payload    string
	RefundedAt sql.NullTime
	CreatedAt  time.Time
	// BoostChatID is the group a boost credited; 0 for personal support.
	BoostChatID int64
}

// SupportPaymentRecord is the outcome of recording a payment: IsNew is false
// when the charge was already recorded (no credits added again).
type SupportPaymentRecord struct {
	IsNew   bool
	Balance int
}

// SupportRefund is the outcome of marking a payment refunded: the credits
// removed from the payer, or from the boosted group (BoostChatID).
type SupportRefund struct {
	CreditsRemoved int
	Balance        int
	BoostChatID    int64
}

// sqlRowQueryer is implemented by both *sql.DB and *sql.Tx.
type sqlRowQueryer interface {
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
}

// SupportTiers returns the offered tiers, cheapest first.
func SupportTiers() []SupportTier {
	result := make([]SupportTier, len(supportTiers))
	copy(result, supportTiers)

	return result
}

// FindSupportTier returns the tier with this Stars price.
func FindSupportTier(stars int) (SupportTier, bool) {
	result := SupportTier{}
	isFound := false

	for _, tier := range supportTiers {
		if tier.Stars == stars {
			result = tier
			isFound = true
		}
	}

	return result, isFound
}

// BuildSupportPayload returns the invoice payload "support:<stars>:<user_id>".
func BuildSupportPayload(tier SupportTier, userID int64) string {
	result := fmt.Sprintf("%s%s%d%s%d", SupportPayloadPrefix, supportPayloadSeparator, tier.Stars, supportPayloadSeparator, userID)

	return result
}

// BuildBoostPayload returns the invoice payload
// "boost:<stars>:<user_id>:<chat_id>" of a group boost.
func BuildBoostPayload(tier SupportTier, userID int64, chatID int64) string {
	result := fmt.Sprintf("%s%s%d%s%d%s%d", BoostPayloadPrefix, supportPayloadSeparator, tier.Stars, supportPayloadSeparator, userID, supportPayloadSeparator, chatID)

	return result
}

// ParseSupportPayload parses "support:<stars>:<user_id>" or a group boost
// "boost:<stars>:<user_id>:<chat_id>" (a group id is negative) of a known
// tier.
func ParseSupportPayload(payload string) (SupportInvoice, error) {
	result := SupportInvoice{}
	err := fmt.Errorf("%w: %q", ErrSupportPayloadInvalid, payload)
	parts := strings.Split(payload, supportPayloadSeparator)
	isSupport := (len(parts) == supportPayloadParts) && (parts[0] == SupportPayloadPrefix)
	isBoost := (len(parts) == boostPayloadParts) && (parts[0] == BoostPayloadPrefix)
	stars := 0
	userID := int64(0)
	chatID := int64(0)
	starsErr := ErrSupportPayloadInvalid
	userErr := ErrSupportPayloadInvalid
	var chatErr error

	if (isSupport == true) || (isBoost == true) {
		stars, starsErr = strconv.Atoi(parts[1])
		userID, userErr = strconv.ParseInt(parts[2], 10, 64)
	}

	if isBoost == true {
		chatID, chatErr = strconv.ParseInt(parts[3], 10, 64)
	}

	tier, isKnown := FindSupportTier(stars)
	isChatValid := (isBoost == false) || ((chatErr == nil) && (chatID < 0))
	isValid := (starsErr == nil) && (userErr == nil) && (isKnown == true) && (userID > 0) && (isChatValid == true)

	if isValid == true {
		result = SupportInvoice{Tier: tier, UserID: userID, BoostChatID: chatID}
		err = nil
	}

	return result, err
}

// RecordSupportPayment stores a successful payment and adds its credits in
// one transaction. A charge id seen before adds nothing, so a redelivered
// successful_payment can't credit twice.
func RecordSupportPayment(payment SupportPayment) (SupportPaymentRecord, error) {
	result := SupportPaymentRecord{}
	db := GetDB()
	err := errGatedFetchNoDatabase
	var tx *sql.Tx
	background := context.Background()
	ctx, cancel := context.WithTimeout(background, supportStoreTimeout)

	defer cancel()

	if db != nil {
		tx, err = db.BeginTx(ctx, nil)
	}

	if err == nil {
		result.IsNew, err = insertSupportPayment(ctx, tx, payment)
	}

	pool, ownerID := paymentCreditPool(payment.UserID, payment.BoostChatID)

	if (err == nil) && (result.IsNew == true) {
		result.Balance, err = pool.add(ctx, tx, ownerID, payment.Credits)
	}

	if (err == nil) && (result.IsNew == false) {
		result.Balance, err = pool.get(ctx, tx, ownerID)
	}

	err = finishSupportTx(tx, err)

	return result, err
}

// insertSupportPayment inserts the payment unless its charge id exists.
func insertSupportPayment(ctx context.Context, tx *sql.Tx, payment SupportPayment) (bool, error) {
	result := false
	chargeID := ""
	query := `
		INSERT INTO support_payments (telegram_payment_charge_id, user_id, chat_id, stars, credits, payload, boosted_chat_id)
		VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, 0))
		ON CONFLICT (telegram_payment_charge_id) DO NOTHING
		RETURNING telegram_payment_charge_id`
	row := tx.QueryRowContext(ctx, query, payment.ChargeID, payment.UserID, payment.ChatID, payment.Stars, payment.Credits, payment.Payload, payment.BoostChatID)
	err := row.Scan(&chargeID)

	if errors.Is(err, sql.ErrNoRows) == true {
		err = nil
	}

	if (err == nil) && (chargeID != "") {
		result = true
	}

	return result, err
}

// GetSupportPayment returns a stored payment by its charge id; the bool is
// false when there is none.
func GetSupportPayment(chargeID string) (SupportPayment, bool, error) {
	result := SupportPayment{}
	isFound := false
	db := GetDB()
	err := errGatedFetchNoDatabase
	query := `
		SELECT telegram_payment_charge_id, user_id, chat_id, stars, credits, payload, refunded_at, COALESCE(boosted_chat_id, 0)
		FROM support_payments WHERE telegram_payment_charge_id = $1`

	if db != nil {
		row := db.QueryRow(query, chargeID)
		err = row.Scan(&result.ChargeID, &result.UserID, &result.ChatID, &result.Stars, &result.Credits, &result.Payload, &result.RefundedAt, &result.BoostChatID)
	}

	if err == nil {
		isFound = true
	}

	if errors.Is(err, sql.ErrNoRows) == true {
		err = nil
	}

	return result, isFound, err
}

// recentSupportPaymentsQuery lists payments newest first; a row without a
// creation time (the column is nullable) sorts last as the Unix epoch.
const recentSupportPaymentsQuery = `SELECT telegram_payment_charge_id, user_id, chat_id, stars, credits, payload, refunded_at, COALESCE(created_at, to_timestamp(0)), COALESCE(boosted_chat_id, 0) FROM support_payments ORDER BY created_at DESC NULLS LAST LIMIT $1`

// ListRecentSupportPayments returns the newest payments first, at most
// limit of them, refunded ones included.
func ListRecentSupportPayments(limit int) ([]SupportPayment, error) {
	var result []SupportPayment
	var rows *sql.Rows
	db := GetDB()
	err := errGatedFetchNoDatabase
	background := context.Background()
	ctx, cancel := context.WithTimeout(background, supportStoreTimeout)
	query := recentSupportPaymentsQuery

	defer cancel()

	if db != nil {
		rows, err = db.QueryContext(ctx, query, limit)
	}

	if err == nil {
		defer rows.Close()
		result, err = scanSupportPayments(rows)
	}

	return result, err
}

// scanSupportPayments reads the rows of ListRecentSupportPayments.
func scanSupportPayments(rows *sql.Rows) ([]SupportPayment, error) {
	var result []SupportPayment
	var err error

	for (err == nil) && (rows.Next() == true) {
		payment := SupportPayment{}
		err = rows.Scan(&payment.ChargeID, &payment.UserID, &payment.ChatID, &payment.Stars, &payment.Credits, &payment.Payload, &payment.RefundedAt, &payment.CreatedAt, &payment.BoostChatID)

		if err == nil {
			result = append(result, payment)
		}
	}

	if err == nil {
		err = rows.Err()
	}

	return result, err
}

// MarkSupportPaymentRefunded marks the user's payment refunded and removes
// the credits it granted that are still unused (the balance never goes below
// zero). It returns ErrSupportPaymentNotRefundable for an unknown, foreign or
// already refunded charge.
func MarkSupportPaymentRefunded(userID int64, chargeID string) (SupportRefund, error) {
	result := SupportRefund{}
	db := GetDB()
	err := errGatedFetchNoDatabase
	var tx *sql.Tx
	granted := 0
	background := context.Background()
	ctx, cancel := context.WithTimeout(background, supportStoreTimeout)

	defer cancel()

	if db != nil {
		tx, err = db.BeginTx(ctx, nil)
	}

	boostChatID := int64(0)

	if err == nil {
		granted, boostChatID, err = markSupportPaymentRefunded(ctx, tx, userID, chargeID)
	}

	pool, ownerID := paymentCreditPool(userID, boostChatID)

	if err == nil {
		result, err = pool.remove(ctx, tx, ownerID, granted)
		result.BoostChatID = boostChatID
	}

	err = finishSupportTx(tx, err)

	return result, err
}

// markSupportPaymentRefunded sets refunded_at and returns the payment's
// granted credits and the group it boosted (0 for personal support).
func markSupportPaymentRefunded(ctx context.Context, tx *sql.Tx, userID int64, chargeID string) (int, int64, error) {
	result := 0
	boostChatID := int64(0)
	query := `
		UPDATE support_payments SET refunded_at = CURRENT_TIMESTAMP
		WHERE telegram_payment_charge_id = $1 AND user_id = $2 AND refunded_at IS NULL
		RETURNING credits, COALESCE(boosted_chat_id, 0)`
	row := tx.QueryRowContext(ctx, query, chargeID, userID)
	err := row.Scan(&result, &boostChatID)

	if errors.Is(err, sql.ErrNoRows) == true {
		err = ErrSupportPaymentNotRefundable
	}

	return result, boostChatID, err
}

// creditPool is a balance table of bonus restricted-post credits: one for
// users, one for groups. Its names are constants, never user input.
type creditPool struct {
	table     string
	keyColumn string
}

// Credit pools of users and groups.
var (
	userCreditPool  = creditPool{table: "gated_fetch_credits", keyColumn: "user_id"}
	groupCreditPool = creditPool{table: "gated_fetch_chat_credits", keyColumn: "chat_id"}
)

// paymentCreditPool returns the pool and owner a payment credits: the
// boosted group, or the payer.
func paymentCreditPool(userID int64, boostChatID int64) (creditPool, int64) {
	result := userCreditPool
	ownerID := userID

	if boostChatID != 0 {
		result = groupCreditPool
		ownerID = boostChatID
	}

	return result, ownerID
}

// get reads a balance; an owner without a row has none.
func (pool creditPool) get(ctx context.Context, queryer sqlRowQueryer, ownerID int64) (int, error) {
	result := 0
	query := fmt.Sprintf(`SELECT credits FROM %s WHERE %s = $1`, pool.table, pool.keyColumn)
	row := queryer.QueryRowContext(ctx, query, ownerID)
	err := row.Scan(&result)

	if errors.Is(err, sql.ErrNoRows) == true {
		err = nil
	}

	return result, err
}

// add atomically adds credits and returns the new balance.
func (pool creditPool) add(ctx context.Context, queryer sqlRowQueryer, ownerID int64, amount int) (int, error) {
	result := 0
	query := fmt.Sprintf(`
		INSERT INTO %s (%s, credits)
		VALUES ($1, $2)
		ON CONFLICT (%s) DO UPDATE SET credits = %s.credits + EXCLUDED.credits
		RETURNING credits`, pool.table, pool.keyColumn, pool.keyColumn, pool.table)
	row := queryer.QueryRowContext(ctx, query, ownerID, amount)
	err := row.Scan(&result)

	return result, err
}

// remove takes up to amount credits, locking the row so a concurrent use or
// top-up isn't lost.
func (pool creditPool) remove(ctx context.Context, tx *sql.Tx, ownerID int64, amount int) (SupportRefund, error) {
	result := SupportRefund{}
	selectQuery := fmt.Sprintf(`SELECT credits FROM %s WHERE %s = $1 FOR UPDATE`, pool.table, pool.keyColumn)
	updateQuery := fmt.Sprintf(`UPDATE %s SET credits = credits - $2 WHERE %s = $1 RETURNING credits`, pool.table, pool.keyColumn)
	balance := 0
	row := tx.QueryRowContext(ctx, selectQuery, ownerID)
	err := row.Scan(&balance)

	if errors.Is(err, sql.ErrNoRows) == true {
		err = nil
	}

	result.CreditsRemoved = amount

	if balance < amount {
		result.CreditsRemoved = balance
	}

	if (err == nil) && (result.CreditsRemoved > 0) {
		updateRow := tx.QueryRowContext(ctx, updateQuery, ownerID, result.CreditsRemoved)
		err = updateRow.Scan(&balance)
	}

	result.Balance = balance

	return result, err
}

// take atomically uses one credit. It returns false when there is none, and
// the balance left after taking one.
func (pool creditPool) take(ownerID int64) (bool, int, error) {
	result := false
	remaining := 0
	db := GetDB()
	err := errGatedFetchNoDatabase
	query := fmt.Sprintf(`
		UPDATE %s SET credits = credits - $2
		WHERE %s = $1 AND credits >= $2
		RETURNING credits`, pool.table, pool.keyColumn)
	background := context.Background()
	ctx, cancel := context.WithTimeout(background, supportStoreTimeout)

	defer cancel()

	if db != nil {
		row := db.QueryRowContext(ctx, query, ownerID, gatedFetchCreditUnit)
		err = row.Scan(&remaining)
	}

	if err == nil {
		result = true
	}

	if errors.Is(err, sql.ErrNoRows) == true {
		err = nil
	}

	return result, remaining, err
}

// giveBack returns a credit taken for a fetch that failed.
func (pool creditPool) giveBack(ownerID int64) error {
	db := GetDB()
	err := errGatedFetchNoDatabase
	background := context.Background()
	ctx, cancel := context.WithTimeout(background, supportStoreTimeout)

	defer cancel()

	if db != nil {
		_, err = pool.add(ctx, db, ownerID, gatedFetchCreditUnit)
	}

	return err
}

// balance reads an owner's balance outside a transaction.
func (pool creditPool) balance(ownerID int64) (int, error) {
	result := 0
	db := GetDB()
	err := errGatedFetchNoDatabase
	background := context.Background()
	ctx, cancel := context.WithTimeout(background, supportStoreTimeout)

	defer cancel()

	if db != nil {
		result, err = pool.get(ctx, db, ownerID)
	}

	return result, err
}

// GetGatedFetchCredits returns the user's bonus credit balance.
func GetGatedFetchCredits(userID int64) (int, error) {
	result, err := userCreditPool.balance(userID)

	return result, err
}

// TakeGatedFetchCredit atomically uses one of the user's credits. It returns
// false when the user has none, and the balance left after taking one.
func TakeGatedFetchCredit(userID int64) (bool, int, error) {
	isTaken, remaining, err := userCreditPool.take(userID)

	return isTaken, remaining, err
}

// ReturnGatedFetchCredit gives back a credit taken for a fetch that failed.
func ReturnGatedFetchCredit(userID int64) error {
	err := userCreditPool.giveBack(userID)

	return err
}

// GetGroupFetchCredits returns the group's bonus credit balance.
func GetGroupFetchCredits(chatID int64) (int, error) {
	result, err := groupCreditPool.balance(chatID)

	return result, err
}

// TakeGroupFetchCredit atomically uses one of the group's credits.
func TakeGroupFetchCredit(chatID int64) (bool, int, error) {
	isTaken, remaining, err := groupCreditPool.take(chatID)

	return isTaken, remaining, err
}

// ReturnGroupFetchCredit gives back a group credit taken for a fetch that
// failed.
func ReturnGroupFetchCredit(chatID int64) error {
	err := groupCreditPool.giveBack(chatID)

	return err
}

// finishSupportTx commits tx when err is nil and rolls it back otherwise.
func finishSupportTx(tx *sql.Tx, err error) error {
	result := err

	if (tx != nil) && (err != nil) {
		tx.Rollback()
	}

	if (tx != nil) && (err == nil) {
		result = tx.Commit()
	}

	return result
}

// creditsCheckTimeout bounds the policy read of CanUseGatedFetchCredits,
// which runs within Telegram's 10-second pre-checkout deadline.
const creditsCheckTimeout = 3 * time.Second

// CanUseGroupFetchCredits reports whether bonus credits bought for a group
// could ever be used: the paid fetch is configured, and the group isn't
// turned off (an explicit off, or a monthly cap of 0) or unlimited (it never
// runs out, so credits would never apply). Boosts are declined otherwise.
func CanUseGroupFetchCredits(chatID int64) bool {
	isConfigured := (IsGatedFetchConfigured() == true) && (IsDBInitialized() == true) && (chatID < 0)
	result := false

	if isConfigured == true {
		result = isGroupOpenForCredits(chatID)
	}

	return result
}

// isGroupOpenForCredits reports whether the group's policy lets credits be
// used; a failed read counts as no.
func isGroupOpenForCredits(chatID int64) bool {
	background := context.Background()
	ctx, cancel := context.WithTimeout(background, creditsCheckTimeout)

	defer cancel()

	policy, err := getGatedFetchPolicy(ctx, GatedFetchScopeChat, chatID)
	defaultCap := config.GetGatedFetchChatMonthlyCap()
	limit := effectiveGatedFetchLimit(policy, defaultCap)
	result := (err == nil) && (policy.Enabled == true) && (limit > 0)

	return result
}

// MoveGroupCredits moves a group's bonus credits, its paid-fetch policy and
// its boosts to the new id Telegram gives it when it becomes a supergroup,
// so paid credits and the owner's settings aren't left on the old id.
func MoveGroupCredits(oldChatID int64, newChatID int64) error {
	db := GetDB()
	err := errGatedFetchNoDatabase
	var tx *sql.Tx
	background := context.Background()
	ctx, cancel := context.WithTimeout(background, supportStoreTimeout)

	defer cancel()

	if db != nil {
		tx, err = db.BeginTx(ctx, nil)
	}

	if err == nil {
		err = moveGroupRows(ctx, tx, oldChatID, newChatID)
	}

	err = finishSupportTx(tx, err)

	return err
}

// groupMoveStatements move a group's rows to its new id: credits are added
// to any the new id has, a policy is copied unless the new id has one, and
// boosts point at the new id for refunds.
var groupMoveStatements = []string{
	`INSERT INTO gated_fetch_chat_credits (chat_id, credits)
		SELECT $2, credits FROM gated_fetch_chat_credits WHERE chat_id = $1
		ON CONFLICT (chat_id) DO UPDATE SET credits = gated_fetch_chat_credits.credits + EXCLUDED.credits`,
	`DELETE FROM gated_fetch_chat_credits WHERE chat_id = $1 AND chat_id <> $2`,
	`INSERT INTO gated_fetch_policies (scope, scope_id, enabled, monthly_limit)
		SELECT scope, $2, enabled, monthly_limit FROM gated_fetch_policies WHERE scope = 'chat' AND scope_id = $1
		ON CONFLICT (scope, scope_id) DO NOTHING`,
	`UPDATE support_payments SET boosted_chat_id = $2 WHERE boosted_chat_id = $1`,
}

// moveGroupRows runs the move statements in the transaction.
func moveGroupRows(ctx context.Context, tx *sql.Tx, oldChatID int64, newChatID int64) error {
	var err error

	for _, statement := range groupMoveStatements {
		if err == nil {
			_, err = tx.ExecContext(ctx, statement, oldChatID, newChatID)
		}
	}

	return err
}

// CanUseGatedFetchCredits reports whether bonus credits bought by a user could
// ever be used: the paid fetch is configured (Apify token, a bot-wide cap
// that isn't 0, a database) and the user's effective allowance (the owner's
// override, else the default) isn't 0, which blocks credits too. Credits work
// in the user's private chat, so a group turned off doesn't make them
// unusable. Payments are declined otherwise, so nobody pays for unusable
// credits.
func CanUseGatedFetchCredits(userID int64) bool {
	isConfigured := (IsGatedFetchConfigured() == true) && (IsDBInitialized() == true)
	result := false

	if isConfigured == true {
		result = isUserAllowanceAboveZero(userID)
	}

	return result
}

// isUserAllowanceAboveZero reports whether the user's effective monthly
// allowance isn't 0; a failed read counts as no.
func isUserAllowanceAboveZero(userID int64) bool {
	background := context.Background()
	ctx, cancel := context.WithTimeout(background, creditsCheckTimeout)

	defer cancel()

	policy, err := getGatedFetchPolicy(ctx, GatedFetchScopeUser, userID)
	defaultLimit := config.GetGatedFetchUserMonthlyAllowance()
	limit := effectiveGatedFetchLimit(policy, defaultLimit)
	result := (err == nil) && (limit != 0)

	return result
}
