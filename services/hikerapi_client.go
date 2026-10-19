package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/timoffmax/social-link-bot/config"
)

const (
	hikerAPIBase = "https://api.hikerapi.com"

	// hikerServiceName is the provider's name in the ledger and its
	// outage-tracker key.
	hikerServiceName = "hiker"

	// hikerAccessKeyHeader carries the key, so it stays out of URLs and logs.
	hikerAccessKeyHeader = "x-access-key"

	hikerPathMediaByCode = "/v1/media/by/code"
	hikerPathStoryByURL  = "/v1/story/by/url"
	hikerPathBalance     = "/sys/balance"

	// hikerRequestTimeout bounds one paid request.
	hikerRequestTimeout = 30 * time.Second

	// hikerBalanceTimeout bounds the free balance read.
	hikerBalanceTimeout = 10 * time.Second

	// hikerMaxBody caps how much of an answer is read.
	hikerMaxBody = 5 * 1024 * 1024

	// hikerMaxErrorBody caps how much of an error answer goes into a log.
	hikerMaxErrorBody = 300

	// hikerBudgetEpsilon keeps float sums of whole prices from refusing the
	// last request that exactly fits the budget.
	hikerBudgetEpsilon = 1e-9

	// hikerNoStatus is the ledger status of a request that got no answer.
	hikerNoStatus = 0

	// hikerExhaustedRetryInterval is how long an empty balance (a 402 or a
	// balance read of 0) keeps HikerAPI skipped; the next request then probes
	// again, which costs nothing (a 402 is not billed).
	hikerExhaustedRetryInterval = time.Hour

	// hikerBudgetReadAttempts bounds the ledger reads of one budget check: a
	// read that a finished request may have made stale is repeated once.
	hikerBudgetReadAttempts = 2
)

// Admin alert keys and texts of HikerAPI. The low and the empty balance
// alert are independent, each with its own recovery notice.
const (
	hikerUnauthorizedAlertKey  = "hiker:unauthorized"
	hikerLowBalanceAlertKey    = "hiker:low-balance"
	hikerToppedUpAlertKey      = "hiker:topped-up"
	hikerEmptyBalanceAlertKey  = "hiker:empty-balance"
	hikerRefilledAlertKey      = "hiker:refilled"
	hikerBalanceReadAlertKey   = "hiker:balance-read"
	hikerUnauthorizedText      = "⚠️ HikerAPI rejected the access key (HTTP 401): check HIKERAPI_TOKEN."
	hikerLowBalanceTemplate    = "⚠️ HikerAPI balance is low: %d requests left (alert below %d). Top up at hikerapi.com."
	hikerEmptyBalanceText      = "⛔ HikerAPI balance is empty: HikerAPI is skipped and tried again every hour until a top-up at hikerapi.com."
	hikerToppedUpTemplate      = "✅ HikerAPI balance topped up: %d requests left."
	hikerRefilledTemplate      = "✅ HikerAPI balance is no longer empty: %d requests left."
	hikerBalanceReadFailedText = "⚠️ HikerAPI balance check failed (see the bot log). HikerAPI keeps running, but the balance alerts are blind until the check works again."
)

// errHikerNotConfigured is returned by HikerAPI methods without a token or
// budget.
var errHikerNotConfigured = fmt.Errorf("%w: hikerapi (HIKERAPI_TOKEN empty or budget 0)", errPaidNotConfigured)

// errHikerBudget is returned when a request could push this month's
// HikerAPI spend (the local ledger) over HIKERAPI_MONTHLY_BUDGET_USD.
var errHikerBudget = fmt.Errorf("%w: hikerapi monthly budget", errPaidBudget)

// errHikerOutOfBalance is returned for HTTP 402 and while the account is
// known to be empty: nothing was billed.
var errHikerOutOfBalance = fmt.Errorf("%w: balance empty", errHikerBudget)

// errHikerUnavailable is returned when the spend ledger can't be read: the
// request is skipped (spend must not run blind), but the budget is not used
// up.
var errHikerUnavailable = fmt.Errorf("%w: hikerapi spend ledger unreadable", errPaidUnavailable)

// errHikerContent marks a billed 403 (private) or 404 (gone) answer.
var errHikerContent = fmt.Errorf("%w: hikerapi", errPaidContentOutcome)

// hikerStatusCharges lists the statuses whose billing differs from the
// default (2xx and other 4xx billed, 3xx and 5xx not).
var hikerStatusCharges = map[int]bool{
	http.StatusBadRequest:      true,
	http.StatusUnauthorized:    false,
	http.StatusPaymentRequired: false,
	http.StatusForbidden:       true,
	http.StatusNotFound:        true,
	http.StatusTooManyRequests: false,
}

// hikerContentStatuses are answers about the content, not service failures.
var hikerContentStatuses = map[int]bool{
	http.StatusForbidden: true,
	http.StatusNotFound:  true,
}

// hikerNow is the clock of the budget and balance state (a variable so
// tests can move it across a month or past the retry interval).
var hikerNow = time.Now

// hikerClient calls HikerAPI. Every paid request first passes the monthly
// budget check against the local ledger and is then written to it.
type hikerClient struct {
	token   string
	apiBase string
	client  *http.Client
}

// hikerBudget holds the prices of requests started but not yet in the
// ledger, the costs of this month whose ledger write failed (so a lost row
// never frees budget), and the running total of costs written to the
// ledger (so a budget check can tell its ledger read may be stale).
var hikerBudget = struct {
	sync.Mutex
	inFlightUsd     float64
	unrecordedUsd   float64
	unrecordedMonth time.Time
	settledUsd      float64
}{}

// hikerBalanceState is what the hourly balance check and 402 answers know
// about the prepaid account; each alert flag is set once its alert was
// delivered. lastRequests and lastReadAt are the last balance read, for the
// 'paid' status.
var hikerBalanceState = struct {
	sync.Mutex
	exhausted          bool
	exhaustedAt        time.Time
	lowAlerted         bool
	emptyAlerted       bool
	balanceReadAlerted bool
	lastRequests       int
	lastReadAt         time.Time
	isBalanceRead      bool
}{}

// HikerBalance is the free /sys/balance answer.
type HikerBalance struct {
	Requests int
	Amount   float64
	Currency string
}

// hikerBalanceResponse is /sys/balance; pointers make a missing field fail.
type hikerBalanceResponse struct {
	Requests *int    `json:"requests"`
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
}

// hikerAnswer is what one request got: the body of a 2xx answer, the status
// (hikerNoStatus without an answer), and whether the request may have
// reached HikerAPI.
type hikerAnswer struct {
	Body   []byte
	Status int
	IsSent bool
}

// newHikerClient returns a client when HikerAPI is configured, or nil.
func newHikerClient() *hikerClient {
	var result *hikerClient

	if isHikerConfigured() == true {
		result = newHikerClientWithToken()
	}

	return result
}

// newHikerBalanceClient returns a client for the free balance read whenever
// a token is set (a budget of 0 pauses paid requests, not the read), or nil.
func newHikerBalanceClient() *hikerClient {
	var result *hikerClient

	if hasHikerToken() == true {
		result = newHikerClientWithToken()
	}

	return result
}

// newHikerClientWithToken builds a client with the configured token. It never
// follows redirects, because Go would send the custom key header to any host.
func newHikerClientWithToken() *hikerClient {
	token := config.GetHikerAPIToken()
	httpClient := &http.Client{CheckRedirect: refuseHikerRedirect}
	result := &hikerClient{token: token, apiBase: hikerAPIBase, client: httpClient}

	return result
}

// refuseHikerRedirect keeps a 3xx answer as the response, so it fails as a
// non-2xx status and the key never leaves for the redirect target.
func refuseHikerRedirect(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}

// get runs one paid request for feature: budget check, the call, a ledger
// row. A refusal makes no HTTP call.
func (c *hikerClient) get(ctx context.Context, feature, path string, query url.Values) ([]byte, error) {
	var result []byte
	price := config.GetHikerAPIPricePerRequestUsd()
	err := reserveHikerBudget(ctx, price)

	if err == nil {
		requestCtx, cancel := context.WithTimeout(ctx, hikerRequestTimeout)
		var answer hikerAnswer
		answer, err = c.do(requestCtx, path, query)
		cancel()
		result = answer.Body
		settleHikerRequest(feature, answer, price)
		handleHikerStatus(answer.Status)
	}

	return result, err
}

// balance reads the free /sys/balance.
func (c *hikerClient) balance(ctx context.Context) (HikerBalance, error) {
	result := HikerBalance{}
	answer := hikerBalanceResponse{}
	balanceCtx, cancel := context.WithTimeout(ctx, hikerBalanceTimeout)

	defer cancel()

	response, err := c.do(balanceCtx, hikerPathBalance, url.Values{})

	if response.Status == http.StatusUnauthorized {
		notifyAdmin(hikerUnauthorizedAlertKey, hikerUnauthorizedText)
	}

	if err == nil {
		err = json.Unmarshal(response.Body, &answer)
	}

	if (err == nil) && (answer.Requests == nil) {
		err = errors.New("hikerapi balance answer has no requests")
	}

	if err == nil {
		result = HikerBalance{Requests: *answer.Requests, Amount: answer.Amount, Currency: answer.Currency}
	}

	return result, err
}

// do sends an authorized GET. A request that could not be built, whose
// context was already done, or whose connection could not be opened never
// left the bot; any other request may have reached HikerAPI.
func (c *hikerClient) do(ctx context.Context, path string, query url.Values) (hikerAnswer, error) {
	result := hikerAnswer{Status: hikerNoStatus}
	var resp *http.Response
	requestURL := hikerRequestURL(c.apiBase, path, query)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)

	if err == nil {
		err = ctx.Err()
	}

	if err == nil {
		req.Header.Set(hikerAccessKeyHeader, c.token)
		req.Header.Set("Accept", "application/json")
		resp, err = c.client.Do(req)
		result.IsSent = (isHikerDialError(err) == false)
	}

	if err == nil {
		defer resp.Body.Close()

		result.Status = resp.StatusCode
		limited := io.LimitReader(resp.Body, hikerMaxBody)
		result.Body, err = io.ReadAll(limited)
	}

	if err == nil {
		detail := truncateAtRuneBoundary(string(result.Body), hikerMaxErrorBody)
		err = hikerStatusError(result.Status, detail)
	}

	return result, err
}

// isHikerDialError reports a connection that could not be opened (DNS,
// refused): the request never left the bot.
func isHikerDialError(err error) bool {
	var opErr *net.OpError
	isOpError := errors.As(err, &opErr)
	result := (isOpError == true) && (opErr.Op == "dial")

	return result
}

// hikerRequestURL joins the base, path and query.
func hikerRequestURL(apiBase, path string, query url.Values) string {
	result := fmt.Sprintf("%s%s", apiBase, path)
	encoded := query.Encode()

	if encoded != "" {
		result = fmt.Sprintf("%s?%s", result, encoded)
	}

	return result
}

// hikerStatusError maps an HTTP status to the client's error: nil for 2xx,
// content outcome for 403/404, budget-like for 402, a failure otherwise.
func hikerStatusError(status int, detail string) error {
	var result error
	isOK := (status >= http.StatusOK) && (status < http.StatusMultipleChoices)
	isContent := hikerContentStatuses[status]
	isOutOfBalance := (status == http.StatusPaymentRequired)

	if isOK == false {
		result = fmt.Errorf("hikerapi status %d: %s", status, detail)
	}

	if isContent == true {
		result = fmt.Errorf("%w: status %d: %s", errHikerContent, status, detail)
	}

	if isOutOfBalance == true {
		result = fmt.Errorf("%w (status 402): %s", errHikerOutOfBalance, detail)
	}

	return result
}

// hikerIsCharged reports whether HikerAPI may bill a request: one that never
// left is free; one without an answer (timeout, dropped connection) is
// counted at full price, as it may have been served; a 3xx (a redirect our
// client refuses) and a 5xx are not billed.
func hikerIsCharged(answer hikerAnswer) bool {
	status := answer.Status
	isRedirect := (status >= http.StatusMultipleChoices) && (status < http.StatusBadRequest)
	isServerError := (status >= http.StatusInternalServerError)
	charged, isListed := hikerStatusCharges[status]
	result := (answer.IsSent == true) && (isRedirect == false) && (isServerError == false)

	if (answer.IsSent == true) && (isListed == true) {
		result = charged
	}

	return result
}

// handleHikerStatus alerts the owner on a rejected key and marks the
// account empty on 402.
func handleHikerStatus(status int) {
	if status == http.StatusUnauthorized {
		notifyAdmin(hikerUnauthorizedAlertKey, hikerUnauthorizedText)
	}

	if status == http.StatusPaymentRequired {
		markHikerBalanceExhausted()
	}
}

// reserveHikerBudget refuses a request that could take this month's ledger
// spend plus the requests in flight over HIKERAPI_MONTHLY_BUDGET_USD, and
// otherwise adds its price to the in-flight total. An account known to be
// empty refuses as a budget skip, an unreadable ledger as an unavailable
// service. The ledger is read without the lock, so requests don't queue
// behind the query; a read that a request settled meanwhile may have made
// stale counts that request's cost too, and is repeated once when only that
// takes the request over the budget.
func reserveHikerBudget(ctx context.Context, price float64) error {
	err := hikerExhaustedError()
	isReserved := false

	for attempt := 0; (err == nil) && (isReserved == false) && (attempt < hikerBudgetReadAttempts); attempt++ {
		isLastAttempt := (attempt == (hikerBudgetReadAttempts - 1))
		isReserved, err = tryReserveHikerBudget(ctx, price, isLastAttempt)
	}

	return err
}

// hikerExhaustedError refuses while the account is known to be empty.
func hikerExhaustedError() error {
	var result error
	isExhausted := isHikerBalanceExhausted()

	if isExhausted == true {
		result = fmt.Errorf("%w: skipped until a top-up or the hourly retry", errHikerOutOfBalance)
	}

	return result
}

// tryReserveHikerBudget reads the ledger and reserves the price; false
// without an error asks for another read (this one may be stale).
func tryReserveHikerBudget(ctx context.Context, price float64, isLastAttempt bool) (bool, error) {
	isReserved := false
	settledBefore := hikerSettledUsd()
	monthStart := hikerMonthStart()
	spent, err := readHikerMonthSpend(ctx, monthStart)

	if err == nil {
		isReserved, err = reserveHikerSpend(spent, settledBefore, price, isLastAttempt)
	}

	return isReserved, err
}

// hikerSettledUsd is the running total of costs written to the ledger.
func hikerSettledUsd() float64 {
	hikerBudget.Lock()
	defer hikerBudget.Unlock()

	result := hikerBudget.settledUsd

	return result
}

// reserveHikerSpend decides under the lock: spent (read before it) plus
// the costs settled since that read, the requests in flight, this month's
// unrecorded costs and price must fit the budget.
func reserveHikerSpend(spent, settledBefore, price float64, isLastAttempt bool) (bool, error) {
	var err error
	budget := config.GetHikerAPIMonthlyBudgetUsd()
	monthStart := hikerMonthStart()

	hikerBudget.Lock()
	defer hikerBudget.Unlock()

	unrecorded := hikerUnrecordedUsdLocked(monthStart)
	settledSince := (hikerBudget.settledUsd - settledBefore)
	inFlight := hikerBudget.inFlightUsd
	projected := (spent + settledSince + inFlight + unrecorded + price)
	isOver := (projected > (budget + hikerBudgetEpsilon))
	isStale := (settledSince > hikerBudgetEpsilon)
	isRetry := (isOver == true) && (isStale == true) && (isLastAttempt == false)
	isReserved := (isOver == false)

	if (isOver == true) && (isRetry == false) {
		err = fmt.Errorf("%w: $%.3f spent + $%.3f in flight of $%.2f, request costs $%.3f", errHikerBudget, (spent + settledSince + unrecorded), inFlight, budget, price)
	}

	if isReserved == true {
		hikerBudget.inFlightUsd += price
	}

	return isReserved, err
}

// hikerUnrecordedUsdLocked returns this month's unrecorded costs, dropping
// an earlier month's; the caller holds hikerBudget's lock.
func hikerUnrecordedUsdLocked(monthStart time.Time) float64 {
	isSameMonth := hikerBudget.unrecordedMonth.Equal(monthStart)

	if isSameMonth == false {
		hikerBudget.unrecordedUsd = 0
		hikerBudget.unrecordedMonth = monthStart
	}

	result := hikerBudget.unrecordedUsd

	return result
}

// hikerMonthStart is the start of the current UTC month on hikerNow.
func hikerMonthStart() time.Time {
	now := hikerNow()
	utc := now.UTC()
	year := utc.Year()
	month := utc.Month()
	result := time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)

	return result
}

// readHikerMonthSpend reads this month's HikerAPI spend from the ledger; a
// failed read makes HikerAPI unavailable for the request (spend must not
// run blind).
func readHikerMonthSpend(ctx context.Context, monthStart time.Time) (float64, error) {
	ledgerCtx, cancel := context.WithTimeout(ctx, paidLedgerTimeout)

	defer cancel()

	result, err := paidLedger.MonthSpentUsd(ledgerCtx, hikerServiceName, monthStart)

	if err != nil {
		err = fmt.Errorf("%w: %v", errHikerUnavailable, err)
	}

	return result, err
}

// settleHikerRequest writes the ledger row of one request (cost 0 when not
// billed), then, under one lock, adds a written cost to the settled total
// (or a lost row's cost to this month's unrecorded costs) and takes the
// price off the in-flight total.
func settleHikerRequest(feature string, answer hikerAnswer, price float64) {
	charged := hikerIsCharged(answer)
	cost := 0.0

	if charged == true {
		cost = price
	}

	request := PaidRequest{Provider: hikerServiceName, Feature: feature, StatusCode: answer.Status, Charged: charged, CostUsd: cost}
	background := context.Background()
	ctx, cancel := context.WithTimeout(background, paidLedgerTimeout)

	defer cancel()

	err := paidLedger.Record(ctx, request)

	if err != nil {
		log.Printf("❌ [hiker] ledger write failed, counting $%.3f locally: %v", cost, err)
	}

	isRecorded := (err == nil)
	monthStart := hikerMonthStart()

	hikerBudget.Lock()
	defer hikerBudget.Unlock()

	if isRecorded == true {
		hikerBudget.settledUsd += cost
	}

	if isRecorded == false {
		unrecorded := hikerUnrecordedUsdLocked(monthStart)
		hikerBudget.unrecordedUsd = (unrecorded + cost)
	}

	hikerBudget.inFlightUsd -= price
}

// isHikerBalanceExhausted reports an account known to be empty within the
// last hikerExhaustedRetryInterval.
func isHikerBalanceExhausted() bool {
	hikerBalanceState.Lock()
	defer hikerBalanceState.Unlock()

	now := hikerNow()
	age := now.Sub(hikerBalanceState.exhaustedAt)
	result := (hikerBalanceState.exhausted == true) && (age < hikerExhaustedRetryInterval)

	return result
}

// markHikerBalanceExhausted records a 402: HikerAPI is skipped for
// hikerExhaustedRetryInterval (or until a balance read shows a top-up), and
// the owner is told once (a failed send is retried on the next 402).
func markHikerBalanceExhausted() {
	now := hikerNow()

	hikerBalanceState.Lock()
	wasAlerted := hikerBalanceState.emptyAlerted
	hikerBalanceState.exhausted = true
	hikerBalanceState.exhaustedAt = now
	hikerBalanceState.Unlock()

	if wasAlerted == false {
		alertHikerEmptyBalance()
	}
}

// alertHikerEmptyBalance tells the owner the account is empty and records a
// delivered alert.
func alertHikerEmptyBalance() {
	isSent := notifyAdmin(hikerEmptyBalanceAlertKey, hikerEmptyBalanceText)

	if isSent == true {
		hikerBalanceState.Lock()
		hikerBalanceState.emptyAlerted = true
		hikerBalanceState.Unlock()
	}
}

// alertHikerLowBalance tells the owner the balance is low and records a
// delivered alert; an undelivered one is retried on the next read.
func alertHikerLowBalance(balance HikerBalance, threshold int) {
	text := fmt.Sprintf(hikerLowBalanceTemplate, balance.Requests, threshold)
	isSent := notifyAdmin(hikerLowBalanceAlertKey, text)

	if isSent == true {
		hikerBalanceState.Lock()
		hikerBalanceState.lowAlerted = true
		hikerBalanceState.Unlock()
	}
}

// applyHikerBalance updates the account state from a balance read: an empty
// account is skipped and alerts once, a low one (not empty) alerts once, and
// each alert gets its own recovery notice when the balance is back.
func applyHikerBalance(balance HikerBalance) {
	threshold := config.GetHikerAPILowBalanceRequests()
	isEmpty := (balance.Requests <= 0)
	isLow := (balance.Requests < threshold)
	now := hikerNow()

	hikerBalanceState.Lock()
	wasLowAlerted := hikerBalanceState.lowAlerted
	wasEmptyAlerted := hikerBalanceState.emptyAlerted
	hikerBalanceState.exhausted = isEmpty
	hikerBalanceState.exhaustedAt = now
	hikerBalanceState.lowAlerted = (isLow == true) && (wasLowAlerted == true)
	hikerBalanceState.emptyAlerted = (isEmpty == true) && (wasEmptyAlerted == true)
	hikerBalanceState.Unlock()
	rememberHikerBalance(balance)

	if (isEmpty == true) && (wasEmptyAlerted == false) {
		alertHikerEmptyBalance()
	}

	if (isLow == true) && (isEmpty == false) && (wasLowAlerted == false) {
		alertHikerLowBalance(balance, threshold)
	}

	if (isLow == false) && (wasLowAlerted == true) {
		text := fmt.Sprintf(hikerToppedUpTemplate, balance.Requests)
		resetAdminAlert(hikerLowBalanceAlertKey)
		notifyAdmin(hikerToppedUpAlertKey, text)
	}

	if (isEmpty == false) && (wasEmptyAlerted == true) {
		text := fmt.Sprintf(hikerRefilledTemplate, balance.Requests)
		resetAdminAlert(hikerEmptyBalanceAlertKey)
		notifyAdmin(hikerRefilledAlertKey, text)
	}
}

// CheckHikerBalance reads the free HikerAPI balance (from the hourly health
// monitor) and applies it; without HikerAPI it does nothing.
func CheckHikerBalance() {
	client := newHikerClient()

	if client != nil {
		checkHikerBalanceWith(client)
	}
}

// checkHikerBalanceWith reads and applies the balance through client. A
// failed read is logged and alerts the owner once; it leaves the account
// state as it was, so it never keeps HikerAPI off.
func checkHikerBalanceWith(client *hikerClient) {
	background := context.Background()
	balance, err := client.balance(background)

	if err != nil {
		log.Printf("⚠️ [hiker] balance check failed: %v", err)
		alertHikerBalanceReadFailed()
	}

	if err == nil {
		log.Printf("🩺 [hiker] balance: %d requests left", balance.Requests)
		clearHikerBalanceReadAlert()
		applyHikerBalance(balance)
	}
}

// alertHikerBalanceReadFailed alerts the owner once about failing balance
// reads (until a read works again).
func alertHikerBalanceReadFailed() {
	hikerBalanceState.Lock()
	wasAlerted := hikerBalanceState.balanceReadAlerted
	hikerBalanceState.Unlock()

	isSent := false

	if wasAlerted == false {
		isSent = notifyAdmin(hikerBalanceReadAlertKey, hikerBalanceReadFailedText)
	}

	if isSent == true {
		hikerBalanceState.Lock()
		hikerBalanceState.balanceReadAlerted = true
		hikerBalanceState.Unlock()
	}
}

// clearHikerBalanceReadAlert re-arms the balance-read alert after a read
// that worked.
func clearHikerBalanceReadAlert() {
	hikerBalanceState.Lock()
	wasAlerted := hikerBalanceState.balanceReadAlerted
	hikerBalanceState.balanceReadAlerted = false
	hikerBalanceState.Unlock()

	if wasAlerted == true {
		resetAdminAlert(hikerBalanceReadAlertKey)
	}
}

// GetHikerBalance reads the HikerAPI balance (free, read-only) for the
// owner's stats; it needs only the token, so a paused budget still shows it.
func GetHikerBalance() (HikerBalance, error) {
	result := HikerBalance{}
	err := errHikerNotConfigured
	client := newHikerBalanceClient()
	background := context.Background()

	if client != nil {
		result, err = client.balance(background)
	}

	if err == nil {
		rememberHikerBalance(result)
	}

	return result, err
}

// rememberHikerBalance keeps the last balance read for the 'paid' status.
func rememberHikerBalance(balance HikerBalance) {
	now := hikerNow()

	hikerBalanceState.Lock()
	defer hikerBalanceState.Unlock()

	hikerBalanceState.lastRequests = balance.Requests
	hikerBalanceState.lastReadAt = now
	hikerBalanceState.isBalanceRead = true
}

// reportHikerOutcome feeds the outage tracker: budget refusals, content
// outcomes and the caller's own expired deadline are not service failures.
func reportHikerOutcome(callerCtx context.Context, err error) {
	isCallerExpired := (callerCtx.Err() != nil)
	isQuiet := (isPaidBudgetError(err) == true) || (isPaidContentOutcome(err) == true) || (isCallerExpired == true)

	if err == nil {
		ReportServiceSuccess(hikerServiceName)
	}

	if (err != nil) && (isQuiet == false) {
		detail := err.Error()
		ReportServiceFailure(hikerServiceName, detail)
	}
}

// GetHikerMonthStats returns this UTC month's HikerAPI ledger totals.
func GetHikerMonthStats() (PaidMonthStats, error) {
	result, err := GetPaidMonthStats(hikerServiceName)

	return result, err
}
