package services

import (
	"errors"
	"fmt"
	"github.com/timoffmax/social-link-bot/config"
	"log"
	"strings"
)

// GatedFetchDeniedError wraps the InstagramGatedError of a post the bot was not
// allowed to fetch through the paid provider, keeping the reason for the reply.
type GatedFetchDeniedError struct {
	Denial GatedFetchDenial
	Cause  error
}

// Error implements error.
func (e *GatedFetchDeniedError) Error() string {
	result := fmt.Sprintf("gated fetch denied (%s): %v", e.Denial, e.Cause)

	return result
}

// Unwrap keeps errors.As(err, *InstagramGatedError) working.
func (e *GatedFetchDeniedError) Unwrap() error {
	return e.Cause
}

// FetchGatedPost retries a failed Instagram download through the paid provider
// when the failure is an author-gated post and the chat/user quota allows it.
// It returns the downloadErr unchanged when the post isn't gated. When the
// fetch isn't allowed or fails, a region/audience-limited post falls back to
// its OG preview; otherwise the error is wrapped in a GatedFetchDeniedError.
func (sd *SmartDownloader) FetchGatedPost(instagramURL string, downloadErr error, chatID, userID int64, isPrivate bool) ([]*MediaInfo, error) {
	var result []*MediaInfo
	err := downloadErr
	var gatedErr *InstagramGatedError
	isGated := errors.As(downloadErr, &gatedErr)

	if isGated == true {
		spoiler := gatedErr.Gate.NeedsSpoiler()
		result, err = sd.fetchGatedWithQuota(instagramURL, downloadErr, spoiler, chatID, userID, isPrivate)
	}

	// A post limited by region or audience still has a public preview: when
	// the full post can't be fetched, send the thumbnail and caption rather
	// than an explanation. Other gates keep the explanation.
	isPreviewable := (isGated == true) && (err != nil) && (gatedErr.Gate.AudienceLimited == true) && (sd.instagramOG != nil)

	if isPreviewable == true {
		result, err = sd.gatedPreviewFallback(instagramURL, err)
	}

	return result, err
}

// fetchGatedWithQuota reserves the quota, calls the provider and refunds the
// quota when the provider fails.
func (sd *SmartDownloader) fetchGatedWithQuota(instagramURL string, downloadErr error, spoiler bool, chatID, userID int64, isPrivate bool) ([]*MediaInfo, error) {
	var result []*MediaInfo
	var quotas []GatedFetchQuota
	var fetchErr error
	var usage GatedFetchUsage
	denial := GatedFetchDeniedOff
	var err error

	if sd.gated != nil {
		quotas, denial, err = ResolveGatedFetchQuotas(chatID, userID, isPrivate)
	}

	if (denial == GatedFetchAllowed) && (err == nil) {
		denial, usage, err = ReserveGatedFetch(quotas)
	}

	credit := gatedCredit{}
	isCreditEligible := (err == nil) && (IsCreditDenial(denial) == true)

	if isCreditEligible == true {
		credit, err = takeGatedCredit(chatID, userID, denial)
	}

	isCreditUsed := credit.IsUsed

	// A bonus credit replaces the monthly counters: the denied reservation
	// took nothing, so nothing must be refunded from them. Like unlimited
	// scopes, credits skip the shared fetch cap; the dollar budget applies.
	if isCreditUsed == true {
		log.Printf("🎟️ [gated-fetch] chat %d, user %d used a bonus credit (group pool: %t, %d left)", chatID, userID, credit.IsGroup, credit.Left)
		denial = GatedFetchAllowed
		quotas = nil
		usage = GatedFetchUsage{}
	}

	if (denial == GatedFetchAllowed) && (err == nil) {
		providerName := sd.gated.Name()
		log.Printf("🔞 [%s] fetching gated post %s (chat %d, user %d)", providerName, instagramURL, chatID, userID)
		result, fetchErr = sd.gated.FetchPost(instagramURL, spoiler)
	}

	isBudgetRefusal := isPaidBudgetError(fetchErr)
	isNothingRun := isPaidSkip(fetchErr)

	if isBudgetRefusal == true {
		denial = GatedFetchDeniedBudget
	}

	if fetchErr != nil {
		log.Printf("❌ [gated-fetch] fetch failed for %s: %v", instagramURL, fetchErr)
	}

	if (fetchErr != nil) && (isNothingRun == false) {
		sd.refundScopedGatedFetch(quotas)
	}

	// A skip (a budget refusal, or a provider unavailable before running,
	// e.g. an unreadable spend ledger) happens before any run, so nothing
	// was billed: the global unit goes back too. Only a budget refusal gets
	// the "budget used up" reply.
	if isNothingRun == true {
		sd.refundGatedQuotas(quotas)
	}

	// A paid-for credit goes back on any failure, budget refusal included.
	if (fetchErr != nil) && (isCreditUsed == true) {
		sd.returnGatedCredit(credit, chatID, userID)
	}

	if err != nil {
		log.Printf("⚠️ [gated-fetch] quota check failed for %s: %v", instagramURL, err)
	}

	if (denial != GatedFetchAllowed) || (err != nil) || (fetchErr != nil) {
		result = nil
		err = &GatedFetchDeniedError{Denial: denial, Cause: downloadErr}
	}

	if (err == nil) && (len(result) > 0) && (usage.IsLimited == true) {
		result[0].Caption = appendGatedQuotaNote(result[0].Caption, usage)
	}

	if (err == nil) && (len(result) > 0) && (isCreditUsed == true) {
		result[0].Caption = appendGatedCreditNote(result[0].Caption, credit)
	}

	return result, err
}

// gatedQuotaNoteTemplate tells a limited chat or user how many restricted
// posts are left this month; it ends the post's caption.
const gatedQuotaNoteTemplate = "\n\n📊 Restricted posts left this month: %d of %d"

// Credit notes end the post's caption instead of the monthly note: how many
// of the poster's or the group's bonus downloads are left.
const (
	gatedCreditNoteTemplate      = "\n\n🎟️ Bonus downloads left: %d"
	gatedGroupCreditNoteTemplate = "\n\n🚀 Group bonus downloads left: %d"
)

// gatedCredit is a bonus credit used for a fetch: from the group's pool or
// the poster's, with the balance left.
type gatedCredit struct {
	IsUsed  bool
	IsGroup bool
	Left    int
}

// takeGatedCredit uses a bonus credit for a denied fetch. The group's pool
// is used only when the group's own monthly cap is what ran out (so free
// downloads always come first), then the poster's own; otherwise the
// poster's own.
func takeGatedCredit(chatID, userID int64, denial GatedFetchDenial) (gatedCredit, error) {
	result := gatedCredit{}
	var err error
	isGroupCap := (denial == GatedFetchDeniedChatCap) && (chatID < 0)
	isGlobalInGroup := (denial == GatedFetchDeniedGlobal) && (chatID < 0)

	// The bot-wide cap is checked first, so a full one hides a group cap that
	// is used up too: the group's pool applies then as well.
	if isGlobalInGroup == true {
		isGroupCap = isGroupCapUsedUp(chatID)
	}

	if isGroupCap == true {
		result.IsUsed, result.Left, err = TakeGroupFetchCredit(chatID)
		result.IsGroup = result.IsUsed
	}

	if (result.IsUsed == false) && (err == nil) && (userID > 0) {
		result.IsUsed, result.Left, err = TakeGatedFetchCredit(userID)
	}

	return result, err
}

// isGroupCapUsedUp reports whether the group has used its own monthly cap
// this month; an unlimited group or a failed read counts as no.
func isGroupCapUsedUp(chatID int64) bool {
	policy, policyErr := GetGatedFetchPolicy(GatedFetchScopeChat, chatID)
	defaultCap := config.GetGatedFetchChatMonthlyCap()
	limit := effectiveGatedFetchLimit(policy, defaultCap)
	used, usageErr := GetGatedFetchUsage(GatedFetchScopeChat, chatID)
	result := (policyErr == nil) && (usageErr == nil) && (limit > 0) && (used >= limit)

	return result
}

// returnGatedCredit gives a used credit back to the one pool it came from,
// logging a failure.
func (sd *SmartDownloader) returnGatedCredit(credit gatedCredit, chatID, userID int64) {
	var err error

	if credit.IsGroup == true {
		err = ReturnGroupFetchCredit(chatID)
	}

	if credit.IsGroup == false {
		err = ReturnGatedFetchCredit(userID)
	}

	if err != nil {
		log.Printf("⚠️ [gated-fetch] bonus credit return failed (chat %d, user %d, group pool %t): %v", chatID, userID, credit.IsGroup, err)
	}
}

// IsCreditDenial reports whether a bonus credit may replace the denied
// fetch: the user's allowance or the chat's cap is used up, or the shared
// fetch cap is (which credits skip).
func IsCreditDenial(denial GatedFetchDenial) bool {
	result := (denial == GatedFetchDeniedUserCap) || (denial == GatedFetchDeniedChatCap) || (denial == GatedFetchDeniedGlobal)

	return result
}

// appendGatedCreditNote adds the remaining bonus credits line to a caption.
func appendGatedCreditNote(caption string, credit gatedCredit) string {
	template := gatedCreditNoteTemplate

	if credit.IsGroup == true {
		template = gatedGroupCreditNoteTemplate
	}

	note := fmt.Sprintf(template, credit.Left)
	result := strings.TrimSpace(caption + note)

	return result
}

// gatedQuotaNoteReserve is the caption space kept free for the note.
const gatedQuotaNoteReserve = 80

// appendGatedQuotaNote adds the remaining-quota line to a caption.
func appendGatedQuotaNote(caption string, usage GatedFetchUsage) string {
	remaining := usage.Remaining()
	note := fmt.Sprintf(gatedQuotaNoteTemplate, remaining, usage.Limit)
	result := strings.TrimSpace(caption + note)

	return result
}

// gatedPreviewFallback returns the OG preview of a post, or keeps fetchErr
// when there is none.
func (sd *SmartDownloader) gatedPreviewFallback(instagramURL string, fetchErr error) ([]*MediaInfo, error) {
	var result []*MediaInfo
	err := fetchErr
	// The preview must be the post's own image (Instagram's media CDN), not
	// a login wall or logo.
	preview, previewErr := sd.instagramOG.extractPreview(instagramURL, true)

	if (previewErr == nil) && (len(preview) > 0) {
		log.Printf("🖼️ [gated-fetch] sending the preview of %s", instagramURL)
		appendOriginalURL(preview, instagramURL)
		result = preview
		err = nil
	}

	if previewErr != nil {
		log.Printf("⚠️ [gated-fetch] no preview for %s: %v", instagramURL, previewErr)
	}

	return result, err
}

// refundGatedQuotas gives every reserved unit back, logging a failed refund.
func (sd *SmartDownloader) refundGatedQuotas(quotas []GatedFetchQuota) {
	err := RefundGatedFetch(quotas)

	if err != nil {
		log.Printf("⚠️ [gated-fetch] quota refund failed: %v", err)
	}
}

// refundScopedGatedFetch gives the user's or chat's unit back after a failed
// fetch, so they aren't charged for it. The global unit is kept: the provider
// may already have billed the run (e.g. the post resolved but its media failed
// to download), and the global cap is what bounds real spending.
func (sd *SmartDownloader) refundScopedGatedFetch(quotas []GatedFetchQuota) {
	var scoped []GatedFetchQuota

	for _, quota := range quotas {
		if quota.Scope != GatedFetchScopeGlobal {
			scoped = append(scoped, quota)
		}
	}

	err := RefundGatedFetch(scoped)

	if err != nil {
		log.Printf("⚠️ [gated-fetch] quota refund failed: %v", err)
	}
}
