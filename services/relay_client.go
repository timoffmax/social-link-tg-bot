package services

// Client of the home relay: a loopback HTTP endpoint (an SSH reverse tunnel
// from a home machine) that runs yt-dlp from a residential IP for the
// platforms that wall the server's datacenter IP. The bot sends only the
// platform and the video id, never a URL; the relay builds the URL itself.
// One client serves every platform: the platform methods extract the id and
// build the caption, this file keeps the request, the checks of the answer
// and the outage verdict of every error in one place.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/timoffmax/social-link-bot/config"
)

const (
	relayFetchPath  = "/v1/fetch"
	relayHealthPath = "/healthz"

	// relayDialTimeout bounds the connect; a stopped tunnel refuses at once.
	relayDialTimeout = 3 * time.Second

	// relayNextMethodReserve is the chain time always kept for the method
	// after the relay (Apify: a budget lookup, a run, the HTTP margin).
	relayNextMethodReserve = 35 * time.Second

	// relayTransferMargin is the part of a call left after the relay's own
	// deadline, for sending the file.
	relayTransferMargin = 5 * time.Second

	// The relay refuses a deadline below 10 s and caps it at 60 s.
	relayMinDeadline = 10 * time.Second
	relayMaxDeadline = 60 * time.Second

	// relayErrorBodyLimit bounds the JSON error body that is read.
	relayErrorBodyLimit = 4096

	// relayTokenFileLimit bounds the token file that is read.
	relayTokenFileLimit = 1024

	// relayMP4BoxOffset and relayMP4Signature locate the "ftyp" box every
	// mp4 starts with; relayMP4HeadSize is the head read before any write.
	relayMP4BoxOffset = 4
	relayMP4Signature = "ftyp"
	relayMP4HeadSize  = relayMP4BoxOffset + len(relayMP4Signature)

	relayAuthorizationHeader = "Authorization"
	relayContentTypeHeader   = "Content-Type"
	relayMetaHeader          = "X-Relay-Meta"

	relayVideoType     = "video/mp4"
	relayJSONType      = "application/json"
	relayAuthScheme    = "Bearer"
	relayServiceSuffix = "-relay"
	relayFileInfix     = "relay"
	relayFileExt       = ".mp4"
)

// Platforms the relay knows. The bot has relay methods for YouTube and
// Instagram; the others are listed so their ids are checked the same way
// once a method is added.
const (
	relayPlatformYouTube   = "youtube"
	relayPlatformTikTok    = "tiktok"
	relayPlatformFacebook  = "facebook"
	relayPlatformInstagram = "instagram"
)

// relaySupportedPlatforms are the platforms that have a relay method in the
// chain; RELAY_PLATFORMS names outside it are ignored.
var relaySupportedPlatforms = map[string]bool{
	relayPlatformYouTube:   true,
	relayPlatformInstagram: true,
}

// relayIDPatterns are the ids the relay accepts per platform, checked
// before a request so a bad id never reaches it.
var relayIDPatterns = map[string]*regexp.Regexp{
	relayPlatformYouTube:   regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`),
	relayPlatformTikTok:    regexp.MustCompile(`^[1-9][0-9]{14,19}$`),
	relayPlatformFacebook:  regexp.MustCompile(`^[1-9][0-9]{9,19}$`),
	relayPlatformInstagram: regexp.MustCompile(`^[A-Za-z0-9_-]{5,40}$`),
}

// Relay error codes (the "code" of a JSON error body).
const (
	relayCodeBadRequest       = "BAD_REQUEST"
	relayCodeUnauthorized     = "UNAUTHORIZED"
	relayCodePlatformDisabled = "PLATFORM_DISABLED"
	relayCodeNotFound         = "NOT_FOUND"
	relayCodeVideoUnavailable = "VIDEO_UNAVAILABLE"
	relayCodePrivateVideo     = "PRIVATE_VIDEO"
	relayCodeAgeRestricted    = "AGE_RESTRICTED"
	relayCodeLiveStream       = "LIVE_STREAM"
	relayCodeTooLong          = "TOO_LONG"
	relayCodeTooLarge         = "TOO_LARGE"
	relayCodeRateLimited      = "RATE_LIMITED"
	relayCodeBotCheck         = "BOT_CHECK"
	relayCodeYtDlpFailed      = "YTDLP_FAILED"
	relayCodeBusy             = "BUSY"
	relayCodeTimeout          = "TIMEOUT"
)

// relayCodeIsOutage tells for every relay error code whether it is a failure
// of the relay itself (fed to the outage tracker) or an answer about the
// video, a cap or a setting. A code not listed counts as an outage.
var relayCodeIsOutage = map[string]bool{
	relayCodeBadRequest:       true,
	relayCodeUnauthorized:     true,
	relayCodePlatformDisabled: false,
	relayCodeNotFound:         true,
	relayCodeVideoUnavailable: false,
	relayCodePrivateVideo:     false,
	relayCodeAgeRestricted:    false,
	relayCodeLiveStream:       false,
	relayCodeTooLong:          false,
	relayCodeTooLarge:         false,
	relayCodeRateLimited:      false,
	relayCodeBotCheck:         true,
	relayCodeYtDlpFailed:      true,
	relayCodeBusy:             false,
	relayCodeTimeout:          true,
}

var (
	// errRelayNotConfigured marks a chain whose relay method has no client.
	errRelayNotConfigured = errors.New("relay not configured")

	// errRelayNoBudget marks a chain with too little time left for a relay
	// call that still leaves the next method its reserve.
	errRelayNoBudget = errors.New("not enough chain time left for the relay")

	// errRelayPlatformDisabled marks a platform the relay said is off; it is
	// skipped until the hourly probe sees it enabled again.
	errRelayPlatformDisabled = errors.New("platform disabled on the relay")

	// errRelayBadID marks an id the relay would refuse.
	errRelayBadID = errors.New("id not accepted by the relay")

	// errRelayTooLarge marks a file over Telegram's upload limit.
	errRelayTooLarge = errors.New("relay file exceeds the Telegram upload limit")

	// errRelayBroken marks an answer the relay must never give (no mp4,
	// cut short, an unknown status).
	errRelayBroken = errors.New("relay answer rejected")

	// errRelayToken marks a call that could not be sent: the token file is
	// missing or empty.
	errRelayToken = errors.New("relay token unreadable")
)

// relayError is one failed relay call: the relay's code (or "" for a
// transport failure or a rejected answer) and whether it is an outage.
type relayError struct {
	Code     string
	Status   int
	Detail   string
	IsOutage bool
	cause    error
}

// Error implements error.
func (e *relayError) Error() string {
	result := fmt.Sprintf("relay %s (HTTP %d): %s", e.Code, e.Status, e.Detail)

	if e.Code == "" {
		result = fmt.Sprintf("relay: %s", e.Detail)
	}

	return result
}

// Unwrap returns the underlying error.
func (e *relayError) Unwrap() error {
	result := e.cause

	return result
}

// relayRequest is the exact body of POST /v1/fetch (the id is a string for
// every platform: JSON numbers lose digits).
type relayRequest struct {
	Platform   string `json:"platform"`
	ID         string `json:"id"`
	DeadlineMs int64  `json:"deadline_ms"`
}

// relayErrorBody is a JSON error answer.
type relayErrorBody struct {
	Code  string `json:"code"`
	Error string `json:"error"`
}

// relayMeta is the X-Relay-Meta header: strings may be empty, numbers null.
type relayMeta struct {
	Title    string `json:"title"`
	Uploader string `json:"uploader"`
	// Channel and Description come for Instagram only: the user name and
	// the post's caption text.
	Channel     string   `json:"channel"`
	Description string   `json:"description"`
	Height      *float64 `json:"height"`
	Width       *float64 `json:"width"`
	Duration    *float64 `json:"duration"`
}

// relayVideo is a fetched video: the checked temp file and its metadata
// (zero when the header was missing or unreadable).
type relayVideo struct {
	Path string
	Size int64
	Meta relayMeta
}

// relayClient calls the relay through relayHTTPClient. It has no overall
// timeout: every call gets its deadline from the chain's ctx.
type relayClient struct {
	baseURL   string
	tokenFile string
	timeout   time.Duration
	tempDir   string
}

// relayBudget is one call's timeout and the deadline sent to the relay,
// computed once per call.
type relayBudget struct {
	timeout  time.Duration
	deadline time.Duration
}

// relayHTTPClient is the one HTTP client (and transport) of every relay
// call and probe, built once per process.
var relayHTTPClient = newRelayHTTPClient()

// relayDisabledPlatforms holds the platforms the relay answered
// PLATFORM_DISABLED for, shared by the chain and the hourly probe.
var (
	relayDisabledMu        sync.Mutex
	relayDisabledPlatforms = make(map[string]bool)
)

// newRelayClient returns the client, or nil unless RELAY_URL is a loopback
// URL and RELAY_TOKEN_FILE is set.
func newRelayClient(tempDir string) *relayClient {
	var result *relayClient
	isConfigured := config.IsRelayConfigured()

	if isConfigured == true {
		baseURL, _ := config.GetRelayURL()
		tokenFile := config.GetRelayTokenFile()
		timeoutSeconds := config.GetRelayTimeoutSeconds()
		timeout := time.Duration(timeoutSeconds.Clamped) * time.Second
		result = &relayClient{baseURL: baseURL, tokenFile: tokenFile, timeout: timeout, tempDir: tempDir}
	}

	return result
}

// relayConfigWarnings logs the relay's configuration problems once per
// process: the configuration is read for every message with links.
var relayConfigWarnings sync.Once

// warnRelayConfigOnce logs the relay's configuration problems on the first
// call only.
func warnRelayConfigOnce() {
	relayConfigWarnings.Do(warnRelayConfig)
}

// warnRelayConfig logs every problem relayConfigProblems finds.
func warnRelayConfig() {
	_, urlErr := config.GetRelayURL()
	timeout := config.GetRelayTimeoutSeconds()
	platforms := relayEnabledPlatforms()
	isConfigured := config.IsRelayConfigured()
	isRelayOn := (isConfigured == true) && (len(platforms) > 0)
	problems := relayConfigProblems(urlErr, timeout, isRelayOn)

	for _, problem := range problems {
		log.Printf("⚠️ [relay] %s", problem)
	}
}

// relayConfigProblems returns an invalid RELAY_URL and, only while the
// relay is on, a RELAY_TIMEOUT_SECONDS that was raised to the minimum.
func relayConfigProblems(urlErr error, timeout config.RelayTimeoutSeconds, isRelayOn bool) []string {
	result := []string{}
	isRaised := (isRelayOn == true) && (timeout.IsRaised() == true)

	if urlErr != nil {
		problem := fmt.Sprintf("%v: relay off", urlErr)
		result = append(result, problem)
	}

	if isRaised == true {
		problem := fmt.Sprintf("RELAY_TIMEOUT_SECONDS=%d is below %d s, the shortest call the relay accepts: using %d s", timeout.Raw, config.MinRelayTimeoutSeconds, timeout.Clamped)
		result = append(result, problem)
	}

	return result
}

// newRelayHTTPClient dials only the configured loopback address (no proxy
// from the environment), with a short connect timeout, one request per
// connection (the relay answers HTTP/1.0) and no redirects.
func newRelayHTTPClient() *http.Client {
	dialer := &net.Dialer{Timeout: relayDialTimeout}
	transport := &http.Transport{
		Proxy:             nil,
		DialContext:       dialer.DialContext,
		DisableKeepAlives: true,
	}
	result := &http.Client{Transport: transport, CheckRedirect: refuseRelayRedirect}

	return result
}

// refuseRelayRedirect keeps a redirect answer as it is (it is then rejected
// as an unknown status).
func refuseRelayRedirect(req *http.Request, via []*http.Request) error {
	result := http.ErrUseLastResponse

	return result
}

// relayServiceName is the outage-tracker key of a platform's relay traffic.
func relayServiceName(platform string) string {
	result := fmt.Sprintf("%s%s", platform, relayServiceSuffix)

	return result
}

// relayEnabledPlatforms returns the RELAY_PLATFORMS entries that have a
// relay method.
func relayEnabledPlatforms() []string {
	platforms := config.GetRelayPlatforms()
	result := make([]string, 0, len(platforms))

	for _, platform := range platforms {
		if relaySupportedPlatforms[platform] == true {
			result = append(result, platform)
		}
	}

	return result
}

// isRelayUsableFor is the one answer to "does platform go to the relay":
// the relay is configured (a loopback URL and a token file), RELAY_PLATFORMS
// lists platform and the bot has a relay method for it. The chain and the
// platform's extractor both use it, so the relay method is in the chain
// exactly when its extractor exists.
func isRelayUsableFor(platform string) bool {
	isEnabled := config.IsRelayEnabledFor(platform)
	result := (isEnabled == true) && (relaySupportedPlatforms[platform] == true)

	return result
}

// isRelayPlatformDisabled reports a platform the relay switched off.
func isRelayPlatformDisabled(platform string) bool {
	relayDisabledMu.Lock()
	result := relayDisabledPlatforms[platform]
	relayDisabledMu.Unlock()

	return result
}

// setRelayPlatformDisabled records the relay's setting for platform and
// logs only a change.
func setRelayPlatformDisabled(platform string, isDisabled bool) {
	relayDisabledMu.Lock()
	wasDisabled := relayDisabledPlatforms[platform]
	relayDisabledPlatforms[platform] = isDisabled
	relayDisabledMu.Unlock()

	if (isDisabled == true) && (wasDisabled == false) {
		log.Printf("🚫 [relay] %s is disabled on the relay: skipped until the probe sees it enabled", platform)
	}

	if (isDisabled == false) && (wasDisabled == true) {
		log.Printf("✅ [relay] %s is enabled on the relay again", platform)
	}
}

// relayCallBudget returns the timeout of one call and the deadline sent to
// the relay: the call takes at most the configured timeout and leaves the
// next method its reserve, and the relay stops yt-dlp a transfer margin
// before the call ends. ok is false when the relay's minimum deadline does
// not fit.
func relayCallBudget(ctx context.Context, maxTimeout time.Duration, now time.Time) (time.Duration, time.Duration, bool) {
	timeout := maxTimeout
	deadline, hasDeadline := ctx.Deadline()

	if hasDeadline == true {
		available := deadline.Sub(now) - relayNextMethodReserve
		timeout = min(maxTimeout, available)
	}

	relayDeadline := min((timeout - relayTransferMargin), relayMaxDeadline)
	isEnough := (relayDeadline >= relayMinDeadline)

	return timeout, relayDeadline, isEnough
}

// Fetch downloads one video through the relay into a checked temp file,
// records a call that passed the checks in the relay history and feeds the
// platform's outage tracker. Every error means the next method should run;
// the caller removes the file once it is sent.
func (c *relayClient) Fetch(ctx context.Context, platform, id string) (relayVideo, error) {
	result := relayVideo{}
	now := time.Now()
	budget, err := c.checkFetch(ctx, platform, id, now)

	if err == nil {
		result, err = c.fetchChecked(ctx, platform, id, budget)
		duration := time.Since(now)
		recordRelayCallLater(platform, result.Size, err, duration)
	}

	reportRelayOutcome(platform, err)

	return result, err
}

// checkFetch computes the call's budget at now and refuses a call before
// any request: an id the relay would refuse, a platform it switched off, or
// too little chain time (a deadline below the relay's minimum).
func (c *relayClient) checkFetch(ctx context.Context, platform, id string, now time.Time) (relayBudget, error) {
	var result error
	pattern := relayIDPatterns[platform]
	isValidID := (pattern != nil) && (pattern.MatchString(id) == true)
	timeout, relayDeadline, hasBudget := relayCallBudget(ctx, c.timeout, now)
	budget := relayBudget{timeout: timeout, deadline: relayDeadline}

	if hasBudget == false {
		result = errRelayNoBudget
	}

	if isRelayPlatformDisabled(platform) == true {
		result = errRelayPlatformDisabled
	}

	if isValidID == false {
		result = fmt.Errorf("%w: %s %q", errRelayBadID, platform, id)
	}

	return budget, result
}

// fetchChecked runs the request under the budget checkFetch accepted and
// stores the answer.
func (c *relayClient) fetchChecked(ctx context.Context, platform, id string, budget relayBudget) (relayVideo, error) {
	result := relayVideo{}
	callCtx, cancel := context.WithTimeout(ctx, budget.timeout)
	defer cancel()

	resp, err := c.post(callCtx, platform, id, budget.deadline)

	if err != nil {
		err = relayTransportError(ctx, err)
	}

	if err == nil {
		defer resp.Body.Close()

		result, err = c.readAnswer(ctx, resp, platform)
	}

	return result, err
}

// post sends POST /v1/fetch in one go (a fixed Content-Length, never
// chunked) with the bearer token.
func (c *relayClient) post(ctx context.Context, platform, id string, relayDeadline time.Duration) (*http.Response, error) {
	var result *http.Response
	var req *http.Request
	token, err := c.readToken()
	payload := relayRequest{Platform: platform, ID: id, DeadlineMs: relayDeadline.Milliseconds()}
	var body []byte

	if err == nil {
		body, err = json.Marshal(payload)
	}

	if err == nil {
		fetchURL := fmt.Sprintf("%s%s", c.baseURL, relayFetchPath)
		reader := bytes.NewReader(body)
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, fetchURL, reader)
	}

	if err == nil {
		authorization := fmt.Sprintf("%s %s", relayAuthScheme, token)
		req.Header.Set(relayAuthorizationHeader, authorization)
		req.Header.Set(relayContentTypeHeader, relayJSONType)
		result, err = relayHTTPClient.Do(req)
	}

	return result, err
}

// readToken reads the bearer token; a missing or empty file is a
// configuration failure.
func (c *relayClient) readToken() (string, error) {
	result := ""
	file, err := os.Open(c.tokenFile)
	var raw []byte

	if err == nil {
		defer file.Close()

		limited := io.LimitReader(file, relayTokenFileLimit)
		raw, err = io.ReadAll(limited)
	}

	if err == nil {
		result = strings.TrimSpace(string(raw))
	}

	if (err == nil) && (result == "") {
		err = fmt.Errorf("token file %s is empty", c.tokenFile)
	}

	if err != nil {
		detail := fmt.Sprintf("token: %v", err)
		cause := fmt.Errorf("%w: %w", errRelayToken, err)
		err = &relayError{Detail: detail, IsOutage: true, cause: cause}
	}

	return result, err
}

// relayTransportError classifies a request that got no answer: a refused
// connection (relay or tunnel down) and the chain's own end are not
// outages; any other failure, the call's own timeout included, is.
func relayTransportError(chainCtx context.Context, err error) error {
	var existing *relayError
	result := err
	isClassified := errors.As(err, &existing)
	isRefused := errors.Is(err, syscall.ECONNREFUSED)
	isChainOver := (chainCtx.Err() != nil)

	if isClassified == false {
		isOutage := (isRefused == false) && (isChainOver == false)
		detail := fmt.Sprintf("request failed: %v", err)
		result = &relayError{Detail: detail, IsOutage: isOutage, cause: err}
	}

	return result
}

// readAnswer turns the relay's answer into a video or a relayError.
func (c *relayClient) readAnswer(chainCtx context.Context, resp *http.Response, platform string) (relayVideo, error) {
	result := relayVideo{}
	var err error
	isVideo := (resp.StatusCode == http.StatusOK)

	if isVideo == true {
		result, err = c.storeVideo(chainCtx, resp, platform)
	}

	if isVideo == false {
		err = readRelayError(resp, platform)
	}

	return result, err
}

// readRelayError reads a JSON error answer. PLATFORM_DISABLED switches the
// platform off until the probe sees it enabled; an unreadable body or an
// unknown code is an outage.
func readRelayError(resp *http.Response, platform string) error {
	body := relayErrorBody{}
	limited := io.LimitReader(resp.Body, relayErrorBodyLimit)
	raw, err := io.ReadAll(limited)

	if err == nil {
		err = json.Unmarshal(raw, &body)
	}

	isOutage, isKnown := relayCodeIsOutage[body.Code]
	isReadable := (err == nil) && (isKnown == true)
	detail := body.Error
	var cause error

	if isReadable == false {
		isOutage = true
		detail = fmt.Sprintf("unexpected answer: %q", raw)
		cause = errRelayBroken
	}

	if body.Code == relayCodePlatformDisabled {
		setRelayPlatformDisabled(platform, true)
	}

	result := &relayError{Code: body.Code, Status: resp.StatusCode, Detail: detail, IsOutage: isOutage, cause: cause}

	return result
}

// storeVideo checks a 200 answer's headers and its first bytes (the mp4
// signature) before anything is written, then streams it into a temp file
// cut one byte past the upload limit and checks the size against
// Content-Length (the relay is a separate trust zone). A rejected file is
// removed.
func (c *relayClient) storeVideo(chainCtx context.Context, resp *http.Response, platform string) (relayVideo, error) {
	result := relayVideo{}
	timestamp := time.Now().UnixNano()
	filename := fmt.Sprintf("%s_%s_%d%s", platform, relayFileInfix, timestamp, relayFileExt)
	filePath := filepath.Join(c.tempDir, filename)
	err := checkRelayVideoHeaders(resp)
	var head []byte
	var size int64

	if err == nil {
		head, err = readMP4Head(chainCtx, resp.Body)
	}

	if err == nil {
		size, err = copyRelayBody(chainCtx, resp, head, filePath)

		if err != nil {
			os.Remove(filePath)
		}
	}

	if err == nil {
		header := resp.Header.Get(relayMetaHeader)
		meta := parseRelayMeta(header, platform)
		result = relayVideo{Path: filePath, Size: size, Meta: meta}
	}

	return result, err
}

// checkRelayVideoHeaders refuses a 200 that is not an mp4, has no
// Content-Length (the relay always sends one) or declares a size over the
// upload limit, before anything is downloaded.
func checkRelayVideoHeaders(resp *http.Response) error {
	var result error
	contentType := resp.Header.Get(relayContentTypeHeader)
	isMP4 := strings.HasPrefix(contentType, relayVideoType)
	isLengthMissing := (resp.ContentLength < 0)
	isTooLarge := (resp.ContentLength > telegramBotUploadLimit)

	if isMP4 == false {
		detail := fmt.Sprintf("200 with Content-Type %q", contentType)
		result = &relayError{Status: resp.StatusCode, Detail: detail, IsOutage: true, cause: errRelayBroken}
	}

	if (isMP4 == true) && (isLengthMissing == true) {
		detail := "200 without Content-Length"
		result = &relayError{Status: resp.StatusCode, Detail: detail, IsOutage: true, cause: errRelayBroken}
	}

	if isTooLarge == true {
		detail := fmt.Sprintf("Content-Length %d is over the %d-byte limit", resp.ContentLength, telegramBotUploadLimit)
		result = &relayError{Status: resp.StatusCode, Detail: detail, cause: errRelayTooLarge}
	}

	return result
}

// readMP4Head reads the body's first relayMP4HeadSize bytes and requires
// the "ftyp" box at offset 4, so an HTML or JSON page sent as 200 is refused
// before any write and never reaches Telegram. A body shorter than the head
// is no mp4; any other read failure is a transport error.
func readMP4Head(chainCtx context.Context, body io.Reader) ([]byte, error) {
	head := make([]byte, relayMP4HeadSize)
	read, err := io.ReadFull(body, head)
	isEOF := errors.Is(err, io.EOF)
	isShort := (isEOF == true) || (errors.Is(err, io.ErrUnexpectedEOF) == true)
	signature := string(head[relayMP4BoxOffset:])
	isMP4 := (err == nil) && (signature == relayMP4Signature)
	isReadFailure := (err != nil) && (isShort == false)

	if isReadFailure == true {
		err = relayTransportError(chainCtx, err)
	}

	if (isReadFailure == false) && (isMP4 == false) {
		detail := fmt.Sprintf("body is not an mp4 (head %q)", head[:read])
		err = &relayError{Status: http.StatusOK, Detail: detail, IsOutage: true, cause: errRelayBroken}
	}

	return head, err
}

// copyRelayBody writes the checked head and the rest of the body to
// filePath through a reader cut one byte past the upload limit and returns
// the bytes written; more than the limit is too large, less than
// Content-Length is a cut transfer.
func copyRelayBody(chainCtx context.Context, resp *http.Response, head []byte, filePath string) (int64, error) {
	result := int64(0)
	out, err := os.Create(filePath)

	if err == nil {
		headReader := bytes.NewReader(head)
		body := io.MultiReader(headReader, resp.Body)
		limited := io.LimitReader(body, (telegramBotUploadLimit + 1))
		result, err = io.Copy(out, limited)
		closeErr := out.Close()

		if err == nil {
			err = closeErr
		}
	}

	if err != nil {
		err = relayTransportError(chainCtx, err)
	}

	isTooLarge := (err == nil) && (result > telegramBotUploadLimit)
	isCut := (err == nil) && (result != resp.ContentLength)

	if isTooLarge == true {
		detail := fmt.Sprintf("body over the %d-byte limit", telegramBotUploadLimit)
		err = &relayError{Status: resp.StatusCode, Detail: detail, cause: errRelayTooLarge}
	}

	if (isTooLarge == false) && (isCut == true) {
		detail := fmt.Sprintf("body has %d of %d bytes", result, resp.ContentLength)
		err = &relayError{Status: resp.StatusCode, Detail: detail, IsOutage: true, cause: errRelayBroken}
	}

	return result, err
}

// parseRelayMeta decodes X-Relay-Meta (base64url without padding of UTF-8
// JSON); a missing or unreadable header gives empty metadata, so the video
// is still sent, without a caption.
func parseRelayMeta(header, platform string) relayMeta {
	result := relayMeta{}
	trimmed := strings.TrimSpace(header)
	unpadded := strings.TrimRight(trimmed, "=")
	raw, err := base64.RawURLEncoding.DecodeString(unpadded)

	if err == nil {
		err = json.Unmarshal(raw, &result)
	}

	if unpadded == "" {
		log.Printf("⚠️ [relay] %s: no %s header, sending without a caption", platform, relayMetaHeader)
		err = nil
	}

	if err != nil {
		log.Printf("⚠️ [relay] %s: unreadable %s header, sending without a caption: %v", platform, relayMetaHeader, err)
		result = relayMeta{}
	}

	return result
}

// reportRelayOutcome feeds the platform's outage tracker: a success, or a
// failure only for errors that are outages.
func reportRelayOutcome(platform string, err error) {
	service := relayServiceName(platform)
	var failure *relayError
	isRelayError := errors.As(err, &failure)
	isOutage := (isRelayError == true) && (failure.IsOutage == true)

	if err == nil {
		ReportServiceSuccess(service)
	}

	if isOutage == true {
		detail := err.Error()
		ReportServiceFailure(service, detail)
	}
}
