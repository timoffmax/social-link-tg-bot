package services

// Integration tests for the support payment and bonus credit store. Like the
// gated-fetch store tests they are skipped unless GATED_FETCH_TEST_DSN is set.

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// recordTestPayment records a payment of a tier, failing the test on errors.
func recordTestPayment(t *testing.T, chargeID string, userID int64, stars int) SupportPaymentRecord {
	tier, _ := FindSupportTier(stars)
	payment := SupportPayment{
		ChargeID: chargeID,
		UserID:   userID,
		ChatID:   userID,
		Stars:    tier.Stars,
		Credits:  tier.Credits,
		Payload:  BuildSupportPayload(tier, userID),
	}

	record, err := RecordSupportPayment(payment)

	if err != nil {
		t.Fatalf("record: %v", err)
	}

	return record
}

func TestSupportPaymentIsCreditedOnceAndBalancesAdd(t *testing.T) {
	setupGatedFetchTestDB(t)

	first := recordTestPayment(t, "charge-a", 100, 25)
	again := recordTestPayment(t, "charge-a", 100, 25)
	second := recordTestPayment(t, "charge-b", 100, 50)

	if (first.IsNew == false) || (first.Balance != 10) || (again.IsNew == true) || (again.Balance != 10) || (second.Balance != 35) {
		t.Fatalf("unexpected records: %+v %+v %+v", first, again, second)
	}

	payment, isFound, err := GetSupportPayment("charge-a")

	if (err != nil) || (isFound == false) || (payment.UserID != 100) || (payment.Credits != 10) || (payment.RefundedAt.Valid == true) {
		t.Fatalf("unexpected stored payment: %+v %v %v", payment, isFound, err)
	}
}

func TestSupportPaymentConcurrentDeliveriesCreditOnce(t *testing.T) {
	setupGatedFetchTestDB(t)
	tier, _ := FindSupportTier(100)
	payment := SupportPayment{ChargeID: "charge-dup", UserID: 101, ChatID: 101, Stars: tier.Stars, Credits: tier.Credits, Payload: BuildSupportPayload(tier, 101)}
	var wg sync.WaitGroup
	var mu sync.Mutex
	newCount := 0

	for i := 0; i < 10; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			record, err := RecordSupportPayment(payment)

			if (err == nil) && (record.IsNew == true) {
				mu.Lock()
				newCount++
				mu.Unlock()
			}
		}()
	}

	wg.Wait()
	balance, err := GetGatedFetchCredits(101)

	if (newCount != 1) || (balance != 60) || (err != nil) {
		t.Fatalf("expected one credit of 60, got new=%d balance=%d err=%v", newCount, balance, err)
	}
}

func TestGatedFetchCreditsAreNeverOverspentConcurrently(t *testing.T) {
	setupGatedFetchTestDB(t)
	recordTestPayment(t, "charge-c", 102, 25)
	var wg sync.WaitGroup
	var mu sync.Mutex
	taken := 0

	for i := 0; i < 30; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			isTaken, _, err := TakeGatedFetchCredit(102)

			if (err == nil) && (isTaken == true) {
				mu.Lock()
				taken++
				mu.Unlock()
			}
		}()
	}

	wg.Wait()
	balance, _ := GetGatedFetchCredits(102)

	if (taken != 10) || (balance != 0) {
		t.Fatalf("expected exactly 10 credits used, got taken=%d balance=%d", taken, balance)
	}
}

func TestTakeGatedFetchCreditWithoutCreditsIsNotAnError(t *testing.T) {
	setupGatedFetchTestDB(t)

	isTaken, _, err := TakeGatedFetchCredit(103)

	if (isTaken == true) || (err != nil) {
		t.Fatalf("expected no credit and no error, got %v %v", isTaken, err)
	}
}

func TestSupportRefundRemovesOnlyUnusedCreditsOnce(t *testing.T) {
	setupGatedFetchTestDB(t)
	recordTestPayment(t, "charge-d", 104, 25)

	for i := 0; i < 7; i++ {
		TakeGatedFetchCredit(104)
	}

	refund, err := MarkSupportPaymentRefunded(104, "charge-d")

	if (err != nil) || (refund.CreditsRemoved != 3) || (refund.Balance != 0) {
		t.Fatalf("expected the 3 unused credits removed, got %+v %v", refund, err)
	}

	_, again := MarkSupportPaymentRefunded(104, "charge-d")
	payment, _, _ := GetSupportPayment("charge-d")

	if (errors.Is(again, ErrSupportPaymentNotRefundable) == false) || (payment.RefundedAt.Valid == false) {
		t.Fatalf("expected a second refund to be refused, got %v %+v", again, payment)
	}
}

func TestSupportRefundKeepsCreditsOfOtherPayments(t *testing.T) {
	setupGatedFetchTestDB(t)
	recordTestPayment(t, "charge-e", 105, 25)
	recordTestPayment(t, "charge-f", 105, 50)

	refund, err := MarkSupportPaymentRefunded(105, "charge-e")

	if (err != nil) || (refund.CreditsRemoved != 10) || (refund.Balance != 25) {
		t.Fatalf("expected 10 removed and 25 kept, got %+v %v", refund, err)
	}
}

func TestSupportRefundRejectsAnotherUsersCharge(t *testing.T) {
	setupGatedFetchTestDB(t)
	recordTestPayment(t, "charge-g", 106, 25)

	_, err := MarkSupportPaymentRefunded(107, "charge-g")
	balance, _ := GetGatedFetchCredits(106)

	if (errors.Is(err, ErrSupportPaymentNotRefundable) == false) || (balance != 10) {
		t.Fatalf("expected the foreign refund refused, got %v balance=%d", err, balance)
	}
}

// budgetRefusingGatedProvider refuses the run before it starts.
type budgetRefusingGatedProvider struct{}

func (budgetRefusingGatedProvider) Name() string {
	return "budget"
}

func (budgetRefusingGatedProvider) FetchPost(string, bool) ([]*MediaInfo, error) {
	return nil, fmt.Errorf("%w: test", errApifyBudget)
}

// useUpAllowance spends a private user's whole allowance of 1.
func useUpAllowance(t *testing.T, userID int64) {
	t.Setenv("GATED_FETCH_USER_MONTHLY_ALLOWANCE", "1")

	if denial := reserveOnce(t, userID, userID, true); denial != GatedFetchAllowed {
		t.Fatalf("expected the allowance, got %q", denial)
	}
}

const gatedTestPostURL = "https://www.instagram.com/p/DeYsKEDjbGB/"

func TestGatedFetchUsesACreditAfterTheAllowanceAndNotesIt(t *testing.T) {
	setupGatedFetchTestDB(t)
	useUpAllowance(t, 110)
	recordTestPayment(t, "charge-h", 110, 25)
	sd := &SmartDownloader{gated: succeedingGatedProvider{}}
	gatedErr := &InstagramGatedError{Gate: InstagramGate{Restricted: true}}

	media, err := sd.FetchGatedPost(gatedTestPostURL, gatedErr, 110, 110, true)

	if (err != nil) || (strings.HasSuffix(media[0].Caption, "🎟️ Bonus downloads left: 9") == false) || (strings.Contains(media[0].Caption, "Restricted posts left") == true) {
		t.Fatalf("expected the credit note, got %v %v", media, err)
	}

	userUsed, _ := GetGatedFetchUsage(GatedFetchScopeUser, 110)
	globalUsed, _ := GetGatedFetchUsage(GatedFetchScopeGlobal, 0)

	if (userUsed != 1) || (globalUsed != 1) {
		t.Fatalf("a credit must not touch the monthly counters: user=%d global=%d", userUsed, globalUsed)
	}
}

func TestGatedFetchInAGroupUsesTheSendersCredit(t *testing.T) {
	setupGatedFetchTestDB(t)
	t.Setenv("GATED_FETCH_CHAT_MONTHLY_CAP", "1")
	recordTestPayment(t, "charge-i", 111, 25)
	sd := &SmartDownloader{gated: succeedingGatedProvider{}}
	gatedErr := &InstagramGatedError{Gate: InstagramGate{Restricted: true}}

	sd.FetchGatedPost(gatedTestPostURL, gatedErr, -111, 111, false)
	_, err := sd.FetchGatedPost(gatedTestPostURL, gatedErr, -111, 111, false)
	balance, _ := GetGatedFetchCredits(111)

	if (err != nil) || (balance != 9) {
		t.Fatalf("expected the sender's credit used, got %v balance=%d", err, balance)
	}
}

func TestGatedFetchCreditsSkipTheGlobalCap(t *testing.T) {
	setupGatedFetchTestDB(t)
	t.Setenv("GATED_FETCH_GLOBAL_MONTHLY_CAP", "1")
	useUpAllowance(t, 112)
	recordTestPayment(t, "charge-j", 113, 25)
	sd := &SmartDownloader{gated: succeedingGatedProvider{}}
	gatedErr := &InstagramGatedError{Gate: InstagramGate{Restricted: true}}

	_, err := sd.FetchGatedPost(gatedTestPostURL, gatedErr, 113, 113, true)

	if err != nil {
		t.Fatalf("expected a credit to skip the full global cap, got %v", err)
	}
}

func TestGatedFetchWithoutCreditsIsStillDenied(t *testing.T) {
	setupGatedFetchTestDB(t)
	useUpAllowance(t, 114)
	sd := &SmartDownloader{gated: succeedingGatedProvider{}}
	gatedErr := &InstagramGatedError{Gate: InstagramGate{Restricted: true}}

	_, err := sd.FetchGatedPost(gatedTestPostURL, gatedErr, 114, 114, true)
	var deniedErr *GatedFetchDeniedError

	if (errors.As(err, &deniedErr) == false) || (deniedErr.Denial != GatedFetchDeniedUserCap) {
		t.Fatalf("expected the allowance denial, got %v", err)
	}
}

func TestGatedFetchReturnsTheCreditWhenTheFetchFails(t *testing.T) {
	providers := map[string]GatedPostProvider{"failure": failingGatedProvider{}, "budget": budgetRefusingGatedProvider{}}

	for name, provider := range providers {
		t.Run(name, func(t *testing.T) {
			setupGatedFetchTestDB(t)
			useUpAllowance(t, 115)
			recordTestPayment(t, "charge-k", 115, 25)
			sd := &SmartDownloader{gated: provider}
			gatedErr := &InstagramGatedError{Gate: InstagramGate{Restricted: true}}

			_, err := sd.FetchGatedPost(gatedTestPostURL, gatedErr, 115, 115, true)
			balance, _ := GetGatedFetchCredits(115)
			userUsed, _ := GetGatedFetchUsage(GatedFetchScopeUser, 115)

			if (err == nil) || (balance != 10) || (userUsed != 1) {
				t.Fatalf("expected the credit back and the allowance untouched, got %v balance=%d used=%d", err, balance, userUsed)
			}
		})
	}
}

func TestGatedFetchOwnerBlockIsNotOverriddenByCredits(t *testing.T) {
	setupGatedFetchTestDB(t)
	recordTestPayment(t, "charge-z", 120, 25)
	SetGatedFetchMonthlyLimit(GatedFetchScopeUser, 120, sql.NullInt64{Int64: 0, Valid: true})
	sd := &SmartDownloader{gated: succeedingGatedProvider{}}
	gatedErr := &InstagramGatedError{Gate: InstagramGate{Restricted: true}}

	_, err := sd.FetchGatedPost(gatedTestPostURL, gatedErr, 120, 120, true)
	balance, _ := GetGatedFetchCredits(120)

	if (err == nil) || (balance != 10) {
		t.Fatalf("an owner block must not be overridden by credits: err=%v balance=%d", err, balance)
	}
}

func TestListRecentSupportPaymentsReturnsNewestFirstWithRefunds(t *testing.T) {
	setupGatedFetchTestDB(t)

	for _, chargeID := range []string{"old", "new"} {
		payment := SupportPayment{ChargeID: chargeID, UserID: 42, ChatID: 42, Stars: 25, Credits: 10, Payload: "support:25:42"}

		if _, err := RecordSupportPayment(payment); err != nil {
			t.Fatalf("record: %v", err)
		}
	}

	mustExec(t, GetDB(), "UPDATE support_payments SET created_at = created_at - INTERVAL '1 hour' WHERE telegram_payment_charge_id = 'old'")

	if _, err := MarkSupportPaymentRefunded(42, "old"); err != nil {
		t.Fatalf("refund: %v", err)
	}

	payments, err := ListRecentSupportPayments(10)

	if (err != nil) || (len(payments) != 2) || (payments[0].ChargeID != "new") {
		t.Fatalf("expected the newest first, got %+v (%v)", payments, err)
	}

	if (payments[1].RefundedAt.Valid == false) || (payments[0].CreatedAt.IsZero() == true) {
		t.Fatalf("expected the refund mark and creation time, got %+v", payments)
	}
}

func recordTestBoost(t *testing.T, chargeID string, userID int64, chatID int64, stars int) {
	tier, _ := FindSupportTier(stars)
	payment := SupportPayment{ChargeID: chargeID, UserID: userID, ChatID: userID, Stars: stars, Credits: tier.Credits, Payload: BuildBoostPayload(tier, userID, chatID), BoostChatID: chatID}

	if _, err := RecordSupportPayment(payment); err != nil {
		t.Fatalf("record boost: %v", err)
	}
}

func TestBoostCreditsTheGroupNotThePayer(t *testing.T) {
	setupGatedFetchTestDB(t)
	recordTestBoost(t, "boost-a", 211, -211, 50)

	groupBalance, _ := GetGroupFetchCredits(-211)
	userBalance, _ := GetGatedFetchCredits(211)
	payment, isFound, _ := GetSupportPayment("boost-a")

	if (groupBalance != 25) || (userBalance != 0) || (isFound == false) || (payment.BoostChatID != -211) {
		t.Fatalf("expected the group credited, got group=%d user=%d payment=%+v", groupBalance, userBalance, payment)
	}
}

func TestGroupBonusIsUsedBeforeTheSendersOwn(t *testing.T) {
	setupGatedFetchTestDB(t)
	t.Setenv("GATED_FETCH_CHAT_MONTHLY_CAP", "1")
	recordTestPayment(t, "charge-own", 212, 25)
	recordTestBoost(t, "boost-b", 213, -212, 25)
	sd := &SmartDownloader{gated: succeedingGatedProvider{}}
	gatedErr := &InstagramGatedError{Gate: InstagramGate{Restricted: true}}

	sd.FetchGatedPost(gatedTestPostURL, gatedErr, -212, 212, false)
	media, err := sd.FetchGatedPost(gatedTestPostURL, gatedErr, -212, 212, false)
	groupBalance, _ := GetGroupFetchCredits(-212)
	userBalance, _ := GetGatedFetchCredits(212)

	if (err != nil) || (groupBalance != 9) || (userBalance != 10) {
		t.Fatalf("expected the group's credit used first, got %v group=%d user=%d", err, groupBalance, userBalance)
	}

	if strings.Contains(media[0].Caption, "Group bonus downloads left: 9") == false {
		t.Fatalf("expected the group note, got %q", media[0].Caption)
	}
}

func TestRefundingABoostRemovesTheGroupsUnusedCredits(t *testing.T) {
	setupGatedFetchTestDB(t)
	recordTestBoost(t, "boost-c", 214, -214, 25)

	refund, err := MarkSupportPaymentRefunded(214, "boost-c")
	groupBalance, _ := GetGroupFetchCredits(-214)

	if (err != nil) || (refund.CreditsRemoved != 10) || (refund.BoostChatID != -214) || (groupBalance != 0) {
		t.Fatalf("expected the group's credits removed, got %+v %v group=%d", refund, err, groupBalance)
	}
}

func TestBoostNeedsAGroupThatIsNotTurnedOff(t *testing.T) {
	setupGatedFetchTestDB(t)

	if CanUseGroupFetchCredits(-215) == false {
		t.Fatalf("a group with the default cap can be boosted")
	}

	if err := SetGatedFetchEnabled(GatedFetchScopeChat, -215, false); err != nil {
		t.Fatalf("off: %v", err)
	}

	if (CanUseGroupFetchCredits(-215) == true) || (CanUseGroupFetchCredits(215) == true) {
		t.Fatalf("a group turned off, or a private chat, can't be boosted")
	}
}

func TestSupportStatsCountGroupCredits(t *testing.T) {
	setupGatedFetchTestDB(t)
	recordTestPayment(t, "charge-s", 216, 25)
	recordTestBoost(t, "boost-s", 217, -216, 25)

	stats, err := GetSupportStats()

	if (err != nil) || (stats.OutstandingCredits != 20) {
		t.Fatalf("expected personal and group credits counted, got %+v %v", stats, err)
	}
}

func TestFailedFetchReturnsAGroupCreditOnlyToTheGroup(t *testing.T) {
	setupGatedFetchTestDB(t)
	t.Setenv("GATED_FETCH_CHAT_MONTHLY_CAP", "1")
	recordTestBoost(t, "boost-d", 218, -218, 25)
	sd := &SmartDownloader{gated: succeedingGatedProvider{}}
	gatedErr := &InstagramGatedError{Gate: InstagramGate{Restricted: true}}
	sd.FetchGatedPost(gatedTestPostURL, gatedErr, -218, 219, false)
	sd.gated = failingGatedProvider{}

	sd.FetchGatedPost(gatedTestPostURL, gatedErr, -218, 219, false)

	groupBalance, _ := GetGroupFetchCredits(-218)
	userBalance, _ := GetGatedFetchCredits(219)

	if (groupBalance != 10) || (userBalance != 0) {
		t.Fatalf("expected the credit back in the group only, got group=%d user=%d", groupBalance, userBalance)
	}
}

func TestGroupCreditsWaitForTheGroupsOwnCap(t *testing.T) {
	setupGatedFetchTestDB(t)
	t.Setenv("GATED_FETCH_GLOBAL_MONTHLY_CAP", "1")
	recordTestBoost(t, "boost-e", 220, -220, 25)
	sd := &SmartDownloader{gated: succeedingGatedProvider{}}
	gatedErr := &InstagramGatedError{Gate: InstagramGate{Restricted: true}}

	sd.FetchGatedPost(gatedTestPostURL, gatedErr, -221, 221, false)
	_, err := sd.FetchGatedPost(gatedTestPostURL, gatedErr, -220, 222, false)
	groupBalance, _ := GetGroupFetchCredits(-220)

	if (err == nil) || (groupBalance != 10) {
		t.Fatalf("the bot-wide cap must not spend group credits while the group has free ones, got %v group=%d", err, groupBalance)
	}
}

func TestUnlimitedGroupsCantBeBoosted(t *testing.T) {
	setupGatedFetchTestDB(t)

	if err := SetGatedFetchMonthlyLimit(GatedFetchScopeChat, -223, sql.NullInt64{Int64: GatedFetchUnlimited, Valid: true}); err != nil {
		t.Fatalf("unlimited: %v", err)
	}

	if CanUseGroupFetchCredits(-223) == true {
		t.Fatalf("an unlimited group never uses credits, so it can't be boosted")
	}
}

func TestMoveGroupCreditsToTheSupergroup(t *testing.T) {
	setupGatedFetchTestDB(t)
	recordTestBoost(t, "boost-f", 224, -224, 25)

	if err := SetGatedFetchMonthlyLimit(GatedFetchScopeChat, -224, sql.NullInt64{Int64: 5, Valid: true}); err != nil {
		t.Fatalf("policy: %v", err)
	}

	if err := MoveGroupCredits(-224, -1000224); err != nil {
		t.Fatalf("move: %v", err)
	}

	oldBalance, _ := GetGroupFetchCredits(-224)
	newBalance, _ := GetGroupFetchCredits(-1000224)
	policy, _ := GetGatedFetchPolicy(GatedFetchScopeChat, -1000224)
	refund, err := MarkSupportPaymentRefunded(224, "boost-f")

	if (oldBalance != 0) || (newBalance != 10) || (policy.MonthlyLimit.Int64 != 5) {
		t.Fatalf("expected credits and policy moved, got old=%d new=%d policy=%+v", oldBalance, newBalance, policy)
	}

	if (err != nil) || (refund.BoostChatID != -1000224) || (refund.CreditsRemoved != 10) {
		t.Fatalf("a refund must find the moved credits, got %+v %v", refund, err)
	}
}

func TestGroupCreditsApplyWhenBothCapsAreUsedUp(t *testing.T) {
	setupGatedFetchTestDB(t)
	t.Setenv("GATED_FETCH_GLOBAL_MONTHLY_CAP", "1")
	t.Setenv("GATED_FETCH_CHAT_MONTHLY_CAP", "1")
	recordTestBoost(t, "boost-g", 225, -225, 25)
	sd := &SmartDownloader{gated: succeedingGatedProvider{}}
	gatedErr := &InstagramGatedError{Gate: InstagramGate{Restricted: true}}

	sd.FetchGatedPost(gatedTestPostURL, gatedErr, -225, 226, false)
	_, err := sd.FetchGatedPost(gatedTestPostURL, gatedErr, -225, 227, false)
	groupBalance, _ := GetGroupFetchCredits(-225)

	if (err != nil) || (groupBalance != 9) {
		t.Fatalf("a full bot-wide cap must not strand a used-up group's credits, got %v group=%d", err, groupBalance)
	}
}
