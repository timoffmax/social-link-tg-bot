package services

import (
	"errors"
	"fmt"
	"log"
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
		result, err = sd.fetchGatedWithQuota(instagramURL, downloadErr, gatedErr, chatID, userID, isPrivate)
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
func (sd *SmartDownloader) fetchGatedWithQuota(instagramURL string, downloadErr error, gatedErr *InstagramGatedError, chatID, userID int64, isPrivate bool) ([]*MediaInfo, error) {
	var result []*MediaInfo
	var quotas []GatedFetchQuota
	var fetchErr error
	denial := GatedFetchDeniedOff
	var err error

	if sd.gated != nil {
		quotas, denial, err = ResolveGatedFetchQuotas(chatID, userID, isPrivate)
	}

	if (denial == GatedFetchAllowed) && (err == nil) {
		denial, err = ReserveGatedFetch(quotas)
	}

	if (denial == GatedFetchAllowed) && (err == nil) {
		providerName := sd.gated.Name()
		log.Printf("🔞 [%s] fetching gated post %s (chat %d, user %d)", providerName, instagramURL, chatID, userID)
		spoiler := gatedErr.Gate.NeedsSpoiler()
		result, fetchErr = sd.gated.FetchPost(instagramURL, spoiler)
	}

	isBudgetRefusal := isApifyBudgetError(fetchErr)

	if fetchErr != nil {
		log.Printf("❌ [gated-fetch] fetch failed for %s: %v", instagramURL, fetchErr)
	}

	if (fetchErr != nil) && (isBudgetRefusal == false) {
		sd.refundScopedGatedFetch(quotas)
	}

	// A budget refusal happens before any run, so nothing was billed: the
	// global unit goes back too.
	if isBudgetRefusal == true {
		sd.refundGatedQuotas(quotas)
	}

	if err != nil {
		log.Printf("⚠️ [gated-fetch] quota check failed for %s: %v", instagramURL, err)
	}

	if (denial != GatedFetchAllowed) || (err != nil) || (fetchErr != nil) {
		result = nil
		err = &GatedFetchDeniedError{Denial: denial, Cause: downloadErr}
	}

	return result, err
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
