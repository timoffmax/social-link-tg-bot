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

// supportPayloadSeparator splits "support:<stars>:<user_id>".
const supportPayloadSeparator = ":"

// supportPayloadParts is the number of parts of a valid payload.
const supportPayloadParts = 3

// gatedFetchCreditUnit is the number of credits one restricted-post fetch uses.
const gatedFetchCreditUnit = 1

// supportStoreTimeout caps each payment/credit transaction.
const supportStoreTimeout = 5 * time.Second

// ErrSupportPayloadInvalid is returned for a payload that isn't a known tier.
var ErrSupportPayloadInvalid = errors.New("invalid support payload")

// ErrSupportPaymentNotRefundable is returned when no unrefunded payment with
// that charge id belongs to the user.
var ErrSupportPaymentNotRefundable = errors.New("no unrefunded support payment with this charge id for this user")

// SupportInvoice is a parsed support invoice payload.
type SupportInvoice struct {
	Tier   SupportTier
	UserID int64
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
}

// SupportPaymentRecord is the outcome of recording a payment: IsNew is false
// when the charge was already recorded (no credits added again).
type SupportPaymentRecord struct {
	IsNew   bool
	Balance int
}

// SupportRefund is the outcome of marking a payment refunded.
type SupportRefund struct {
	CreditsRemoved int
	Balance        int
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

// ParseSupportPayload parses "support:<stars>:<user_id>" of a known tier.
func ParseSupportPayload(payload string) (SupportInvoice, error) {
	result := SupportInvoice{}
	err := fmt.Errorf("%w: %q", ErrSupportPayloadInvalid, payload)
	parts := strings.Split(payload, supportPayloadSeparator)
	isShaped := (len(parts) == supportPayloadParts) && (parts[0] == SupportPayloadPrefix)
	stars := 0
	userID := int64(0)
	starsErr := ErrSupportPayloadInvalid
	userErr := ErrSupportPayloadInvalid

	if isShaped == true {
		stars, starsErr = strconv.Atoi(parts[1])
		userID, userErr = strconv.ParseInt(parts[2], 10, 64)
	}

	tier, isKnown := FindSupportTier(stars)
	isValid := (starsErr == nil) && (userErr == nil) && (isKnown == true) && (userID > 0)

	if isValid == true {
		result = SupportInvoice{Tier: tier, UserID: userID}
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

	if (err == nil) && (result.IsNew == true) {
		result.Balance, err = addGatedFetchCredits(ctx, tx, payment.UserID, payment.Credits)
	}

	if (err == nil) && (result.IsNew == false) {
		result.Balance, err = getGatedFetchCredits(ctx, tx, payment.UserID)
	}

	err = finishSupportTx(tx, err)

	return result, err
}

// insertSupportPayment inserts the payment unless its charge id exists.
func insertSupportPayment(ctx context.Context, tx *sql.Tx, payment SupportPayment) (bool, error) {
	result := false
	chargeID := ""
	query := `
		INSERT INTO support_payments (telegram_payment_charge_id, user_id, chat_id, stars, credits, payload)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (telegram_payment_charge_id) DO NOTHING
		RETURNING telegram_payment_charge_id`
	row := tx.QueryRowContext(ctx, query, payment.ChargeID, payment.UserID, payment.ChatID, payment.Stars, payment.Credits, payment.Payload)
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
		SELECT telegram_payment_charge_id, user_id, chat_id, stars, credits, payload, refunded_at
		FROM support_payments WHERE telegram_payment_charge_id = $1`

	if db != nil {
		row := db.QueryRow(query, chargeID)
		err = row.Scan(&result.ChargeID, &result.UserID, &result.ChatID, &result.Stars, &result.Credits, &result.Payload, &result.RefundedAt)
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
const recentSupportPaymentsQuery = `SELECT telegram_payment_charge_id, user_id, chat_id, stars, credits, payload, refunded_at, COALESCE(created_at, to_timestamp(0)) FROM support_payments ORDER BY created_at DESC NULLS LAST LIMIT $1`

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
		err = rows.Scan(&payment.ChargeID, &payment.UserID, &payment.ChatID, &payment.Stars, &payment.Credits, &payment.Payload, &payment.RefundedAt, &payment.CreatedAt)

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

	if err == nil {
		granted, err = markSupportPaymentRefunded(ctx, tx, userID, chargeID)
	}

	if err == nil {
		result, err = removeGatedFetchCredits(ctx, tx, userID, granted)
	}

	err = finishSupportTx(tx, err)

	return result, err
}

// markSupportPaymentRefunded sets refunded_at and returns the payment's
// granted credits.
func markSupportPaymentRefunded(ctx context.Context, tx *sql.Tx, userID int64, chargeID string) (int, error) {
	result := 0
	query := `
		UPDATE support_payments SET refunded_at = CURRENT_TIMESTAMP
		WHERE telegram_payment_charge_id = $1 AND user_id = $2 AND refunded_at IS NULL
		RETURNING credits`
	row := tx.QueryRowContext(ctx, query, chargeID, userID)
	err := row.Scan(&result)

	if errors.Is(err, sql.ErrNoRows) == true {
		err = ErrSupportPaymentNotRefundable
	}

	return result, err
}

// removeGatedFetchCredits takes up to amount credits from the user, locking
// the row so a concurrent use or top-up isn't lost.
func removeGatedFetchCredits(ctx context.Context, tx *sql.Tx, userID int64, amount int) (SupportRefund, error) {
	result := SupportRefund{}
	selectQuery := `SELECT credits FROM gated_fetch_credits WHERE user_id = $1 FOR UPDATE`
	updateQuery := `UPDATE gated_fetch_credits SET credits = credits - $2 WHERE user_id = $1 RETURNING credits`
	balance := 0
	row := tx.QueryRowContext(ctx, selectQuery, userID)
	err := row.Scan(&balance)

	if errors.Is(err, sql.ErrNoRows) == true {
		err = nil
	}

	result.CreditsRemoved = amount

	if balance < amount {
		result.CreditsRemoved = balance
	}

	if (err == nil) && (result.CreditsRemoved > 0) {
		updateRow := tx.QueryRowContext(ctx, updateQuery, userID, result.CreditsRemoved)
		err = updateRow.Scan(&balance)
	}

	result.Balance = balance

	return result, err
}

// GetGatedFetchCredits returns the user's bonus credit balance.
func GetGatedFetchCredits(userID int64) (int, error) {
	result := 0
	db := GetDB()
	err := errGatedFetchNoDatabase
	background := context.Background()
	ctx, cancel := context.WithTimeout(background, supportStoreTimeout)

	defer cancel()

	if db != nil {
		result, err = getGatedFetchCredits(ctx, db, userID)
	}

	return result, err
}

// getGatedFetchCredits reads a balance; a user without a row has none.
func getGatedFetchCredits(ctx context.Context, queryer sqlRowQueryer, userID int64) (int, error) {
	result := 0
	query := `SELECT credits FROM gated_fetch_credits WHERE user_id = $1`
	row := queryer.QueryRowContext(ctx, query, userID)
	err := row.Scan(&result)

	if errors.Is(err, sql.ErrNoRows) == true {
		err = nil
	}

	return result, err
}

// addGatedFetchCredits atomically adds credits and returns the new balance.
func addGatedFetchCredits(ctx context.Context, queryer sqlRowQueryer, userID int64, amount int) (int, error) {
	result := 0
	query := `
		INSERT INTO gated_fetch_credits (user_id, credits)
		VALUES ($1, $2)
		ON CONFLICT (user_id) DO UPDATE SET credits = gated_fetch_credits.credits + EXCLUDED.credits
		RETURNING credits`
	row := queryer.QueryRowContext(ctx, query, userID, amount)
	err := row.Scan(&result)

	return result, err
}

// TakeGatedFetchCredit atomically uses one of the user's credits. It returns
// false when the user has none, and the balance left after taking one.
func TakeGatedFetchCredit(userID int64) (bool, int, error) {
	result := false
	remaining := 0
	db := GetDB()
	err := errGatedFetchNoDatabase
	query := `
		UPDATE gated_fetch_credits SET credits = credits - $2
		WHERE user_id = $1 AND credits >= $2
		RETURNING credits`
	background := context.Background()
	ctx, cancel := context.WithTimeout(background, supportStoreTimeout)

	defer cancel()

	if db != nil {
		row := db.QueryRowContext(ctx, query, userID, gatedFetchCreditUnit)
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

// ReturnGatedFetchCredit gives back a credit taken for a fetch that failed.
func ReturnGatedFetchCredit(userID int64) error {
	db := GetDB()
	err := errGatedFetchNoDatabase
	background := context.Background()
	ctx, cancel := context.WithTimeout(background, supportStoreTimeout)

	defer cancel()

	if db != nil {
		_, err = addGatedFetchCredits(ctx, db, userID, gatedFetchCreditUnit)
	}

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

// CanUseGatedFetchCredits reports whether bonus credits bought by a user could
// ever be used: the paid fetch is configured (Apify token, a bot-wide cap
// above 0, a database) and the user's effective allowance (the owner's
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
