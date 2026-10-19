package services

import (
	"errors"
	"fmt"
	"log"
	"strings"
)

// gatedProviderNameSeparator joins the provider names of a chain.
const gatedProviderNameSeparator = "→"

// fallbackGatedProvider tries its providers in order until one returns the
// post. The quota and credit logic around it runs once, so a user is charged
// once whichever provider serves the post.
type fallbackGatedProvider struct {
	providers []GatedPostProvider
}

// Name implements GatedPostProvider.
func (p *fallbackGatedProvider) Name() string {
	names := make([]string, 0, len(p.providers))

	for _, provider := range p.providers {
		name := provider.Name()
		names = append(names, name)
	}

	result := strings.Join(names, gatedProviderNameSeparator)

	return result
}

// FetchPost implements GatedPostProvider. Any failure moves on to the next
// provider except a content outcome (charged, and the next one would give
// the same answer). A post with at least one slide is a success and stops.
func (p *fallbackGatedProvider) FetchPost(instagramURL string, spoiler bool) ([]*MediaInfo, error) {
	var result []*MediaInfo
	attempts := gatedAttempts{}
	isDone := false

	for i := 0; (i < len(p.providers)) && (isDone == false); i++ {
		provider := p.providers[i]
		media, err := provider.FetchPost(instagramURL, spoiler)
		name := provider.Name()
		attempts.add(name, err)

		// Any other failure, an empty Apify dataset (errApifyNoMedia)
		// included, moves on: the paid path runs only after the oEmbed gate
		// check confirmed the post exists and is gated, so an empty answer
		// is the provider's failure and the backup must run.
		isDone = (err == nil) || (isPaidContentOutcome(err) == true)

		if err == nil {
			result = media
		}

		if isDone == false {
			log.Printf("↪️ [gated-fetch] %s did not fetch %s: %v", name, instagramURL, err)
		}
	}

	err := attempts.err()

	return result, err
}

// gatedAttempts collects the outcome of each provider of a chain.
type gatedAttempts struct {
	succeeded bool
	hasRun    bool
	skips     []error
	failures  []error
}

// add records one provider's outcome: success, a skip (it did not run) or a
// failure (it ran and may have been billed).
func (a *gatedAttempts) add(name string, err error) {
	isSkip := (err != nil) && (isPaidSkip(err) == true)
	isFailure := (err != nil) && (isSkip == false)
	isRun := (isFailure == true) && (errors.Is(err, errCostedNotStarted) == false)

	if isRun == true {
		a.hasRun = true
	}

	if err == nil {
		a.succeeded = true
	}

	if isSkip == true {
		skip := fmt.Errorf("%s: %w", name, err)
		a.skips = append(a.skips, skip)
	}

	if isFailure == true {
		failure := fmt.Errorf("%s: %w", name, err)
		a.failures = append(a.failures, failure)
	}
}

// err is the chain's error: nil after a success; when every provider was
// skipped (nothing ran, nothing was billed), an error of the unavailable
// ones if any (not a budget refusal: they may work on the next request),
// else of every skip (a budget error when one was refused by its budget);
// otherwise a non-budget error of the providers that ran, so the caller
// keeps the global unit.
func (a gatedAttempts) err() error {
	var result error
	isAllSkipped := (a.succeeded == false) && (len(a.failures) == 0) && (len(a.skips) > 0)
	isFailed := (a.succeeded == false) && (len(a.failures) > 0)
	unavailable := unavailableSkips(a.skips)
	isUnavailable := (isAllSkipped == true) && (len(unavailable) > 0)

	if (isAllSkipped == true) && (isUnavailable == false) {
		skipped := errors.Join(a.skips...)
		result = fmt.Errorf("every gated provider was skipped: %w", skipped)
	}

	if isUnavailable == true {
		joined := errors.Join(unavailable...)
		skipped := skipSummary(a.skips)
		result = fmt.Errorf("every gated provider was skipped%s: %w", skipped, joined)
	}

	if isFailed == true {
		failed := errors.Join(a.failures...)
		skipped := skipSummary(a.skips)
		result = fmt.Errorf("every gated provider failed%s: %w", skipped, failed)
	}

	// A provider that got past its pre-spend checks may have billed the run,
	// even when another one of the chain was refused before any spend.
	if (isFailed == true) && (a.hasRun == true) {
		result = &costedRanError{err: result}
	}

	return result
}

// unavailableSkips keeps the skips of providers that could not run for a
// fault of their own.
func unavailableSkips(skips []error) []error {
	result := make([]error, 0, len(skips))

	for _, skip := range skips {
		if isPaidUnavailable(skip) == true {
			result = append(result, skip)
		}
	}

	return result
}

// skipSummary names the skipped providers as text only, so their budget
// errors don't make a failed or unavailable chain read as a budget refusal.
func skipSummary(skips []error) string {
	result := ""
	texts := make([]string, 0, len(skips))

	for _, skip := range skips {
		text := skip.Error()
		texts = append(texts, text)
	}

	if len(texts) > 0 {
		joined := strings.Join(texts, "; ")
		result = fmt.Sprintf(" (skipped: %s)", joined)
	}

	return result
}
