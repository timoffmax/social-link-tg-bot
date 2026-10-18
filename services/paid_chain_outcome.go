package services

// paidChainMethods are the chain methods that call a paid provider.
var paidChainMethods = map[ExtractionMethod]bool{
	MethodYouTubeApify: true,
	MethodStoriesApify: true,
	MethodStoriesHiker: true,
}

// paidChainOutcome remembers how the paid methods of one chain ended, so the
// chain's error names a budget refusal only when no paid method failed for
// another reason (the reply then says the paid service is used up).
type paidChainOutcome struct {
	budgetErr  error
	failureErr error
}

// add records one method's error; non-paid methods are ignored. A provider
// unavailable before running (its ledger unreadable) counts as a failure,
// so the reply never blames a budget that is not used up.
func (o *paidChainOutcome) add(method ExtractionMethod, err error) {
	isPaid := paidChainMethods[method]
	isBudget := (isPaid == true) && (isPaidBudgetError(err) == true)
	isFailure := (isPaid == true) && (err != nil) && (isPaidRefusal(err) == false)

	if isBudget == true {
		o.budgetErr = err
	}

	if isFailure == true {
		o.failureErr = err
	}
}

// cause returns the error the chain reports instead of lastErr: when the
// last error is a paid skip (budget, not configured or unavailable), a paid
// failure (unavailable included) wins over it, then a budget refusal; any
// other last error stays.
func (o paidChainOutcome) cause(lastErr error) error {
	result := lastErr
	isSkip := isPaidSkip(lastErr)

	if (isSkip == true) && (o.budgetErr != nil) {
		result = o.budgetErr
	}

	if (isSkip == true) && (o.failureErr != nil) {
		result = o.failureErr
	}

	return result
}
