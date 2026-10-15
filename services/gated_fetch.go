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
// It returns the downloadErr unchanged when the post isn't gated, wrapped in a
// GatedFetchDeniedError when the fetch isn't allowed or fails.
func (sd *SmartDownloader) FetchGatedPost(instagramURL string, downloadErr error, chatID, userID int64, isPrivate bool) ([]*MediaInfo, error) {
	var result []*MediaInfo
	err := downloadErr
	var gatedErr *InstagramGatedError
	isGated := errors.As(downloadErr, &gatedErr)

	if isGated == true {
		result, err = sd.fetchGatedWithQuota(instagramURL, downloadErr, chatID, userID, isPrivate)
	}

	return result, err
}

// fetchGatedWithQuota reserves the quota, calls the provider and refunds the
// quota when the provider fails.
func (sd *SmartDownloader) fetchGatedWithQuota(instagramURL string, downloadErr error, chatID, userID int64, isPrivate bool) ([]*MediaInfo, error) {
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
		result, fetchErr = sd.gated.FetchPost(instagramURL)
	}

	if fetchErr != nil {
		log.Printf("❌ [gated-fetch] fetch failed for %s: %v", instagramURL, fetchErr)
		sd.refundScopedGatedFetch(quotas)
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
