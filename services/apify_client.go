package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/timoffmax/social-link-bot/config"
)

const (
	apifyAPIBase = "https://api.apify.com/v2"

	// apifyMaxErrorBody caps how much of an error answer goes into a log.
	apifyMaxErrorBody = 300

	// apifyLimitsTimeout bounds the budget lookup before a paid run.
	apifyLimitsTimeout = 10 * time.Second

	// apifyRunTimeoutMargin is added to the run timeout for the HTTP call.
	apifyRunTimeoutMargin = 10 * time.Second
)

// errApifyNotConfigured is returned by Apify methods without APIFY_TOKEN.
var errApifyNotConfigured = errors.New("apify not configured (APIFY_TOKEN empty)")

// errApifyBudget is returned when a run could push this month's Apify spend
// over APIFY_MONTHLY_BUDGET_USD.
var errApifyBudget = errors.New("apify monthly budget reached")

// apifyClient runs Apify actors synchronously. Every run first checks the
// account's real monthly usage against the budget, so all Apify use (gated
// Instagram posts, stories, YouTube) shares one spending limit.
type apifyClient struct {
	token   string
	apiBase string
	client  *http.Client
}

// apifyLimitsResponse is the part of /users/me/limits the budget check uses.
// The usage is a pointer so a missing field fails the check instead of
// reading as $0.
type apifyLimitsResponse struct {
	Data struct {
		Current struct {
			MonthlyUsageUsd *float64 `json:"monthlyUsageUsd"`
		} `json:"current"`
	} `json:"data"`
}

// apifyInFlight holds the cost caps of runs started but not yet finished:
// Apify's usage figure doesn't include them yet, so concurrent runs could
// otherwise all pass the same budget check.
var apifyInFlight = struct {
	sync.Mutex
	usd float64
}{}

// newApifyClient returns a client for the configured token, or nil.
func newApifyClient() *apifyClient {
	var result *apifyClient
	token := config.GetApifyToken()

	if token != "" {
		result = &apifyClient{token: token, apiBase: apifyAPIBase, client: &http.Client{}}
	}

	return result
}

// runSync runs an actor with input and returns its dataset items (raw JSON).
// The run is bounded by timeout and by ctx's deadline, whichever is sooner;
// maxChargeUsd caps what Apify may bill for it.
func (c *apifyClient) runSync(ctx context.Context, actor string, input interface{}, timeout time.Duration, maxChargeUsd float64) ([]byte, error) {
	var result []byte
	runTimeout := apifyRunTimeoutFor(ctx, timeout)
	err := c.reserveBudget(ctx, maxChargeUsd)
	payload, marshalErr := json.Marshal(input)
	requestURL := c.runSyncURL(actor, runTimeout, maxChargeUsd)
	runCtx, cancel := context.WithTimeout(ctx, (runTimeout + apifyRunTimeoutMargin))

	defer cancel()

	if err == nil {
		defer releaseApifyBudget(maxChargeUsd)

		err = marshalErr
	}

	if (err == nil) && (runTimeout <= 0) {
		err = fmt.Errorf("no time left for an apify run: %w", context.DeadlineExceeded)
	}

	if err == nil {
		result, err = c.do(runCtx, http.MethodPost, requestURL, payload)
	}

	return result, err
}

// runSyncURL builds the run-sync-get-dataset-items URL.
func (c *apifyClient) runSyncURL(actor string, runTimeout time.Duration, maxChargeUsd float64) string {
	query := url.Values{}
	timeoutSeconds := int64(runTimeout / time.Second)
	timeoutValue := fmt.Sprintf("%d", timeoutSeconds)
	chargeValue := fmt.Sprintf("%.2f", maxChargeUsd)
	query.Set("timeout", timeoutValue)
	query.Set("maxTotalChargeUsd", chargeValue)
	encodedQuery := query.Encode()
	result := fmt.Sprintf("%s/acts/%s/run-sync-get-dataset-items?%s", c.apiBase, actor, encodedQuery)

	return result
}

// apifyRunTimeoutFor shortens timeout to what is left before ctx's deadline
// (minus the HTTP margin), so a run never outlives the caller.
func apifyRunTimeoutFor(ctx context.Context, timeout time.Duration) time.Duration {
	result := timeout
	deadline, hasDeadline := ctx.Deadline()

	if hasDeadline == true {
		remaining := time.Until(deadline) - apifyRunTimeoutMargin

		if remaining < result {
			result = remaining
		}
	}

	return result
}

// reserveBudget denies a run that could take this month's real Apify usage
// plus the runs in flight over APIFY_MONTHLY_BUDGET_USD, and otherwise adds
// its cap to the in-flight total (releaseApifyBudget takes it off). A failed
// or malformed lookup denies too: spend must not run blind.
func (c *apifyClient) reserveBudget(ctx context.Context, maxChargeUsd float64) error {
	used, err := c.monthlyUsage(ctx)
	budget := config.GetApifyMonthlyBudgetUsd()

	if err != nil {
		err = fmt.Errorf("apify budget check failed: %w", err)
	}

	apifyInFlight.Lock()
	defer apifyInFlight.Unlock()

	projected := used + apifyInFlight.usd + maxChargeUsd

	if (err == nil) && (projected > budget) {
		err = fmt.Errorf("%w: $%.2f used + $%.2f in flight of $%.2f, run may cost $%.2f", errApifyBudget, used, apifyInFlight.usd, budget, maxChargeUsd)
	}

	if err == nil {
		apifyInFlight.usd += maxChargeUsd
	}

	return err
}

// releaseApifyBudget takes a finished run's cap off the in-flight total.
func releaseApifyBudget(maxChargeUsd float64) {
	apifyInFlight.Lock()
	defer apifyInFlight.Unlock()

	apifyInFlight.usd -= maxChargeUsd
}

// monthlyUsage reads this month's Apify usage in USD.
func (c *apifyClient) monthlyUsage(ctx context.Context) (float64, error) {
	result := 0.0
	limits := apifyLimitsResponse{}
	requestURL := fmt.Sprintf("%s/users/me/limits", c.apiBase)
	limitsCtx, cancel := context.WithTimeout(ctx, apifyLimitsTimeout)

	defer cancel()

	body, err := c.do(limitsCtx, http.MethodGet, requestURL, nil)

	if err == nil {
		err = json.Unmarshal(body, &limits)
	}

	usage := limits.Data.Current.MonthlyUsageUsd

	if (err == nil) && (usage == nil) {
		err = errors.New("limits answer has no monthlyUsageUsd")
	}

	if err == nil {
		result = *usage
	}

	return result, err
}

// IsPaidBudgetError reports that a download failed because this month's
// budget for the paid service is used up, anywhere in the error chain.
func IsPaidBudgetError(err error) bool {
	result := isApifyBudgetError(err)

	return result
}

// isApifyBudgetError reports a deliberate budget refusal: nothing was billed
// and it is not a service failure.
func isApifyBudgetError(err error) bool {
	result := errors.Is(err, errApifyBudget)

	return result
}

// do sends an authorized request and returns the body of a 2xx answer.
func (c *apifyClient) do(ctx context.Context, method, requestURL string, payload []byte) ([]byte, error) {
	var result []byte
	var resp *http.Response
	reader := bytes.NewReader(payload)
	authorization := fmt.Sprintf("Bearer %s", c.token)
	req, err := http.NewRequestWithContext(ctx, method, requestURL, reader)

	if err == nil {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", authorization)
		resp, err = c.client.Do(req)
	}

	if err == nil {
		defer resp.Body.Close()

		result, err = io.ReadAll(resp.Body)
	}

	if (err == nil) && ((resp.StatusCode < http.StatusOK) || (resp.StatusCode >= http.StatusMultipleChoices)) {
		detail := truncateAtRuneBoundary(string(result), apifyMaxErrorBody)
		err = fmt.Errorf("apify status %d: %s", resp.StatusCode, detail)
	}

	return result, err
}
