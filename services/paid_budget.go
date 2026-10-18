package services

import (
	"errors"
	"log"

	"github.com/timoffmax/social-link-bot/config"
)

// errPaidBudget is wrapped by every paid provider's deliberate refusal to
// run (budget reached, empty balance): nothing ran, so nothing was billed.
var errPaidBudget = errors.New("paid service budget refused the request")

// errPaidNotConfigured is wrapped by a paid provider that has no token.
var errPaidNotConfigured = errors.New("paid service not configured")

// errPaidUnavailable is wrapped by a paid provider that could not run for a
// fault of its own (its spend ledger unreadable): nothing ran, so nothing
// was billed, but the budget is not used up either.
var errPaidUnavailable = errors.New("paid service unavailable")

// errPaidContentOutcome is wrapped by a paid provider's answer about the
// content itself (private account, deleted post, no such story): another
// paid provider would only be billed for the same answer, so it is final.
var errPaidContentOutcome = errors.New("paid service content outcome")

// Names of the paid Instagram providers in INSTAGRAM_PAID_PROVIDERS.
const (
	PaidProviderApify = "apify"
	PaidProviderHiker = "hiker"
)

// IsPaidBudgetError reports that a download failed because this month's
// budget for the paid service is used up, anywhere in the error chain.
func IsPaidBudgetError(err error) bool {
	result := isPaidBudgetError(err)

	return result
}

// isPaidBudgetError reports a deliberate budget refusal: nothing was billed
// and it is not a service failure.
func isPaidBudgetError(err error) bool {
	result := errors.Is(err, errPaidBudget)

	return result
}

// isPaidContentOutcome reports a paid provider's final answer about the
// content (not a failure of the service).
func isPaidContentOutcome(err error) bool {
	result := errors.Is(err, errPaidContentOutcome)

	return result
}

// isPaidUnavailable reports a paid provider that could not run for a
// fault of its own (not a budget refusal).
func isPaidUnavailable(err error) bool {
	result := errors.Is(err, errPaidUnavailable)

	return result
}

// isPaidRefusal reports a paid provider that refused to run by design:
// its budget or no token.
func isPaidRefusal(err error) bool {
	isBudget := isPaidBudgetError(err)
	isNotConfigured := errors.Is(err, errPaidNotConfigured)
	result := (isBudget == true) || (isNotConfigured == true)

	return result
}

// isPaidSkip reports a paid provider that did not run at all: refused by
// its budget, not configured, or unavailable.
func isPaidSkip(err error) bool {
	isRefusal := isPaidRefusal(err)
	isUnavailable := isPaidUnavailable(err)
	result := (isRefusal == true) || (isUnavailable == true)

	return result
}

// instagramPaidProviderOrder returns the known names of
// INSTAGRAM_PAID_PROVIDERS in order, without repeats; unknown names are
// logged and skipped, and a list without any known name keeps the default.
func instagramPaidProviderOrder() []string {
	names := config.GetInstagramPaidProviders()
	result := knownPaidProviders(names)

	if len(result) == 0 {
		result = []string{PaidProviderApify, PaidProviderHiker}
	}

	return result
}

// knownPaidProviders keeps the known provider names, first occurrence only.
func knownPaidProviders(names []string) []string {
	result := make([]string, 0, len(names))
	seen := map[string]bool{}

	for _, name := range names {
		_, isKnown := paidProviders[name]

		if isKnown == false {
			log.Printf("⚠️ [paid] unknown provider %q in INSTAGRAM_PAID_PROVIDERS ignored", name)
		}

		if (isKnown == true) && (seen[name] == false) {
			result = append(result, name)
			seen[name] = true
		}
	}

	return result
}

// isApifyConfigured reports an Apify token and a monthly budget above 0.
func isApifyConfigured() bool {
	budget := config.GetApifyMonthlyBudgetUsd()
	token := config.GetApifyToken()
	result := (token != "") && (budget > 0)

	return result
}

// isHikerConfigured reports a HikerAPI token and a monthly budget above 0.
func isHikerConfigured() bool {
	budget := config.GetHikerAPIMonthlyBudgetUsd()
	token := config.GetHikerAPIToken()
	result := (token != "") && (budget > 0)

	return result
}

// hasApifyToken reports a set APIFY_TOKEN.
func hasApifyToken() bool {
	token := config.GetApifyToken()
	result := (token != "")

	return result
}

// hasHikerToken reports a set HIKERAPI_TOKEN.
func hasHikerToken() bool {
	token := config.GetHikerAPIToken()
	result := (token != "")

	return result
}

// isAlwaysListed lists a provider's stories method whatever its
// configuration (Apify answers "not configured" itself without a token).
func isAlwaysListed() bool {
	return true
}

// paidProvider is everything the bot knows about one paid Instagram
// provider of INSTAGRAM_PAID_PROVIDERS.
type paidProvider struct {
	// configured reports a token and a monthly budget above 0.
	configured func() bool

	// hasToken reports a token, whatever the budget.
	hasToken func() bool

	// gatedProvider builds the restricted-post provider, or nil without
	// its token.
	gatedProvider func(tempDir string) GatedPostProvider

	// storiesMethod is the provider's method of the stories chain.
	storiesMethod ExtractionMethod

	// storiesListed reports whether the stories chain lists the method.
	storiesListed func() bool
}

// paidProviders is the registry of the known paid Instagram providers.
var paidProviders = map[string]paidProvider{
	PaidProviderApify: {
		configured:    isApifyConfigured,
		hasToken:      hasApifyToken,
		gatedProvider: newApifyGatedProvider,
		storiesMethod: MethodStoriesApify,
		storiesListed: isAlwaysListed,
	},
	PaidProviderHiker: {
		configured:    isHikerConfigured,
		hasToken:      hasHikerToken,
		gatedProvider: newHikerGatedProvider,
		storiesMethod: MethodStoriesHiker,
		storiesListed: isHikerConfigured,
	},
}

// isPaidProviderConfigured reports whether the named provider can run.
func isPaidProviderConfigured(name string) bool {
	result := false
	provider, isKnown := paidProviders[name]

	if isKnown == true {
		result = provider.configured()
	}

	return result
}

// GatedProviderState is a paid provider that has a token, for the owner's
// status: IsOn is false while its monthly budget is 0.
type GatedProviderState struct {
	Name string
	IsOn bool
}

// GatedProvidersWithToken returns the providers of INSTAGRAM_PAID_PROVIDERS
// that have a token, in the order they are tried; a budget of 0 pauses a
// provider without hiding it.
func GatedProvidersWithToken() []GatedProviderState {
	order := instagramPaidProviderOrder()
	result := make([]GatedProviderState, 0, len(order))

	for _, name := range order {
		provider := paidProviders[name]
		hasToken := provider.hasToken()
		isOn := provider.configured()

		if hasToken == true {
			result = append(result, GatedProviderState{Name: name, IsOn: isOn})
		}
	}

	return result
}

// instagramStoriesMethods orders the paid stories methods exactly like
// INSTAGRAM_PAID_PROVIDERS: Apify whenever it is named (it answers "not
// configured" without a token, as before), HikerAPI only when it is named
// and configured, so it stays completely off without HIKERAPI_TOKEN. A list
// that leaves no method offers no stories.
func instagramStoriesMethods() []ExtractionMethod {
	order := instagramPaidProviderOrder()
	result := make([]ExtractionMethod, 0, len(order))

	for _, name := range order {
		provider := paidProviders[name]

		if provider.storiesListed() == true {
			result = append(result, provider.storiesMethod)
		}
	}

	return result
}

// isStoriesFetchConfigured reports whether a provider of the stories chain
// can run.
func isStoriesFetchConfigured() bool {
	result := false
	order := instagramPaidProviderOrder()

	for _, name := range order {
		provider := paidProviders[name]
		isOn := (provider.storiesListed() == true) && (provider.configured() == true)
		result = (result == true) || (isOn == true)
	}

	return result
}

// ConfiguredGatedProviders returns the names of the providers that fetch
// restricted posts, in the order they are tried.
func ConfiguredGatedProviders() []string {
	order := instagramPaidProviderOrder()
	result := make([]string, 0, len(order))

	for _, name := range order {
		if isPaidProviderConfigured(name) == true {
			result = append(result, name)
		}
	}

	return result
}
