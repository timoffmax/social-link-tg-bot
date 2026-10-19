package services

import (
	"errors"
	"fmt"
	"github.com/timoffmax/social-link-bot/config"
	"log"
	"strings"
)

// GatedFetchDeniedError is a special download the limit refused, or a gated
// post that could not be fetched; keeps the reason for the reply.
type GatedFetchDeniedError struct {
	Denial GatedFetchDenial
	Cause  error
}

// Error implements error.
func (e *GatedFetchDeniedError) Error() string {
	result := fmt.Sprintf("special download denied (%s): %v", e.Denial, e.Cause)

	return result
}

// Unwrap keeps errors.As(err, *InstagramGatedError) working.
func (e *GatedFetchDeniedError) Unwrap() error {
	return e.Cause
}

// FetchGatedPost fetches an author-gated post through the paid provider within
// the special-download limit; other download errors pass through unchanged.
func (sd *SmartDownloader) FetchGatedPost(instagramURL string, downloadErr error, requester DownloadRequester) ([]*MediaInfo, error) {
	var result []*MediaInfo
	err := downloadErr
	var gatedErr *InstagramGatedError
	isGated := errors.As(downloadErr, &gatedErr)

	if isGated == true {
		result, err = sd.fetchGatedPostWithinLimit(instagramURL, downloadErr, gatedErr, requester)
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

// fetchGatedPostWithinLimit fetches a gated post through the paid provider
// as one special download; a failure keeps the reason for the reply (off
// when no provider can run).
func (sd *SmartDownloader) fetchGatedPostWithinLimit(instagramURL string, downloadErr error, gatedErr *InstagramGatedError, requester DownloadRequester) ([]*MediaInfo, error) {
	var err error
	spoiler := gatedErr.Gate.NeedsSpoiler()
	download := newSpecialDownload(instagramURL, requester, dbSpecialLimit{})
	fetch := sd.gatedPostFetch(instagramURL, spoiler, requester)
	isProviderReady := sd.canRunGatedProvider()
	steps := []costedStep{{kind: costPaid, canRun: isProviderReady, run: fetch}}
	result, stepErr := runCostedSteps(download, steps)

	if stepErr != nil {
		log.Printf("❌ [gated-fetch] fetch failed for %s: %v", instagramURL, stepErr)
		denial := download.Denial()
		isNothingAsked := (download.isAsked == false) && (denial == GatedFetchAllowed)

		if isNothingAsked == true {
			denial = GatedFetchDeniedOff
		}

		result = nil
		err = &GatedFetchDeniedError{Denial: denial, Cause: downloadErr}
	}

	return result, err
}

// canRunGatedProvider reports a paid provider that can fetch a post now.
func (sd *SmartDownloader) canRunGatedProvider() bool {
	providers := ConfiguredGatedProviders()
	result := (sd.gated != nil) && (len(providers) > 0)

	return result
}

// gatedPostFetch returns the paid fetch of a gated post.
func (sd *SmartDownloader) gatedPostFetch(instagramURL string, spoiler bool, requester DownloadRequester) func() ([]*MediaInfo, error) {
	result := func() ([]*MediaInfo, error) {
		providerName := sd.gated.Name()
		log.Printf("🔞 [%s] fetching gated post %s (chat %d, user %d)", providerName, instagramURL, requester.ChatID, requester.UserID)
		media, err := sd.gated.FetchPost(instagramURL, spoiler)

		return media, err
	}

	return result
}

// gatedQuotaNoteTemplate tells a limited chat or user how many special
// downloads are left this month; it ends the post's caption.
const gatedQuotaNoteTemplate = "\n\n📊 Special downloads left this month: %d of %d"

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
func returnGatedCredit(credit gatedCredit, chatID, userID int64) {
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

// gatedCreditNote is the remaining bonus credits line.
func gatedCreditNote(credit gatedCredit) string {
	template := gatedCreditNoteTemplate

	if credit.IsGroup == true {
		template = gatedGroupCreditNoteTemplate
	}

	note := fmt.Sprintf(template, credit.Left)
	result := strings.TrimSpace(note)

	return result
}

// gatedQuotaNoteReserve is the caption space kept free for the note.
const gatedQuotaNoteReserve = 80

// gatedQuotaNote is the remaining-quota line.
func gatedQuotaNote(usage GatedFetchUsage) string {
	remaining := usage.Remaining()
	note := fmt.Sprintf(gatedQuotaNoteTemplate, remaining, usage.Limit)
	result := strings.TrimSpace(note)

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
func refundGatedQuotas(quotas []GatedFetchQuota) {
	err := RefundGatedFetch(quotas)

	if err != nil {
		log.Printf("⚠️ [gated-fetch] quota refund failed: %v", err)
	}
}

// refundScopedGatedFetch gives the user's or chat's unit back after a failed
// fetch, so they aren't charged for it. The global unit is kept: the provider
// may already have billed the run (e.g. the post resolved but its media failed
// to download), and the global cap is what bounds real spending.
func refundScopedGatedFetch(quotas []GatedFetchQuota) {
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
