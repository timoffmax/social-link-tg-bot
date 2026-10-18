package services

// Reddit gates anonymous access behind a lightweight JS challenge: the post
// page returns an interstitial with a hidden GET form (jsc_token, jsc_orig_r
// and js_challenge=1; the token field was named "token" until 2026-10) and a
// script computing solution = seed + seed. Submitting the solved form
// grants ~24h of anonymous session cookies (domain .reddit.com), after which
// the public .json endpoints answer normally — no proxy needed. This is the
// same JS the headless-browser fallback executes anyway; solving it inline
// just skips spawning Chrome. When the session expires the next fetch gets
// blocked again and the challenge is re-solved on demand. Since 2026-10 the
// .json endpoints answer "blocked by network security" (403) even with a
// solved session, from residential and datacenter IPs alike, so this path
// only works through proxies; vxreddit carries Reddit on its own.

import (
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// redditBrowserUserAgent is used for all direct (no-proxy) Reddit requests;
// it must stay consistent between the challenge solve and the .json fetches.
const redditBrowserUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

var (
	redditChallengeTokenPattern  = regexp.MustCompile(`name="((?:jsc_)?token)"\s+value="([0-9a-fA-F]+)"`)
	redditChallengeOrigPattern   = regexp.MustCompile(`name="jsc_orig_r"\s+value="([^"]*)"`)
	redditChallengeSeedPattern   = regexp.MustCompile(`e=>e\+e\)\("([0-9a-fA-F]+)"\)`)
	redditChallengeSeedFallback  = regexp.MustCompile(`\("([0-9a-fA-F]{16})"\)`)
	redditChallengeActionPattern = regexp.MustCompile(`<form[^>]*action="([^"]+)"`)
)

var (
	redditDirectMu     sync.Mutex
	redditDirectClient *http.Client
)

// getRedditDirectClient returns the shared direct-connection client with a
// cookie jar that holds the anonymous session across requests.
func getRedditDirectClient() *http.Client {
	redditDirectMu.Lock()
	defer redditDirectMu.Unlock()

	if redditDirectClient == nil {
		jar, err := cookiejar.New(nil)

		if err != nil {
			jar = nil
		}

		redditDirectClient = &http.Client{
			Timeout: 20 * time.Second,
			Jar:     jar,
		}
	}

	return redditDirectClient
}

// fetchRedditJSONDirect fetches a Reddit post's .json without proxies,
// solving the JS challenge once if the anonymous session is missing or
// expired. postURL must be a canonical (non-/s/) reddit.com post URL.
func fetchRedditJSONDirect(postURL string) ([]byte, error) {
	client := getRedditDirectClient()

	jsonURL, err := buildDirectJSONURL(postURL)

	if err != nil {
		return nil, err
	}

	body, status, err := redditDirectGet(client, jsonURL)

	if err != nil {
		return nil, err
	}

	if status == http.StatusOK {
		return body, nil
	}

	// Deleted/nonexistent posts are post-level failures — no point solving
	// the challenge for them.
	if (status == http.StatusNotFound) || (status == http.StatusGone) {
		return nil, &redditHTTPError{StatusCode: status, Excerpt: "post not found"}
	}

	log.Printf("🟠 Direct Reddit .json blocked (HTTP %d) — solving JS challenge", status)

	err = solveRedditChallenge(client, postURL)

	if err != nil {
		return nil, fmt.Errorf("challenge solve failed after HTTP %d: %v", status, err)
	}

	body, status, err = redditDirectGet(client, jsonURL)

	if err != nil {
		return nil, err
	}

	if status != http.StatusOK {
		return nil, fmt.Errorf("Reddit .json still blocked after challenge solve: HTTP %d", status)
	}

	log.Printf("✅ Reddit JS challenge solved — anonymous session established")

	return body, nil
}

// solveRedditChallenge fetches the canonical post page, extracts the
// challenge form (token, seed), and submits solution = seed + seed so the
// client's cookie jar receives the anonymous session. Single-flight: when a
// concurrent goroutine already solved it, the extra solve is harmless.
func solveRedditChallenge(client *http.Client, postURL string) error {
	body, status, err := redditDirectGet(client, postURL)

	if err != nil {
		return err
	}

	page := string(body)

	if strings.Contains(page, "js_challenge") == false {
		return fmt.Errorf("no JS challenge served (HTTP %d) — likely a hard block for this IP", status)
	}

	tokenMatch := redditChallengeTokenPattern.FindStringSubmatch(page)

	if len(tokenMatch) < 3 {
		return fmt.Errorf("challenge page has no token field")
	}

	seedMatch := redditChallengeSeedPattern.FindStringSubmatch(page)

	if len(seedMatch) < 2 {
		seedMatch = redditChallengeSeedFallback.FindStringSubmatch(page)
	}

	if len(seedMatch) < 2 {
		return fmt.Errorf("challenge page has no seed — Reddit may have changed the challenge format")
	}

	action := ""
	actionMatch := redditChallengeActionPattern.FindStringSubmatch(page)

	if len(actionMatch) >= 2 {
		action = actionMatch[1]
	}

	form := redditChallengeForm{
		Action:     action,
		Seed:       seedMatch[1],
		TokenField: tokenMatch[1],
		Token:      tokenMatch[2],
	}
	origMatch := redditChallengeOrigPattern.FindStringSubmatch(page)

	if len(origMatch) == 2 {
		form.HasOrigR = true
		form.OrigR = html.UnescapeString(origMatch[1])
	}

	solveURL, err := buildChallengeSolveURL(postURL, form)

	if err != nil {
		return err
	}

	_, status, err = redditDirectGet(client, solveURL)

	if err != nil {
		return fmt.Errorf("challenge submission failed: %v", err)
	}

	if status >= 400 {
		return fmt.Errorf("challenge submission rejected: HTTP %d", status)
	}

	return nil
}

// redditChallengeForm is the hidden form of the challenge page.
type redditChallengeForm struct {
	Action     string
	Seed       string
	TokenField string
	Token      string
	OrigR      string
	HasOrigR   bool
}

// buildChallengeSolveURL assembles the GET request that submits the solved
// challenge form (solution = seed doubled), with the token under the page's
// own field name and jsc_orig_r when the page has it.
func buildChallengeSolveURL(postURL string, form redditChallengeForm) (string, error) {
	result := ""
	var actionURL *url.URL
	base, err := url.Parse(postURL)

	if err != nil {
		err = fmt.Errorf("cannot parse post URL: %v", err)
	}

	if (err == nil) && (form.Action != "") {
		var actionErr error
		action := html.UnescapeString(form.Action)
		actionURL, actionErr = url.Parse(action)

		if actionErr == nil {
			base = base.ResolveReference(actionURL)
		}
	}

	if err == nil {
		solution := fmt.Sprintf("%s%s", form.Seed, form.Seed)
		params := url.Values{}
		params.Set("solution", solution)
		params.Set("js_challenge", "1")
		params.Set(form.TokenField, form.Token)

		if form.HasOrigR == true {
			params.Set("jsc_orig_r", form.OrigR)
		}

		base.RawQuery = params.Encode()
		result = base.String()
	}

	return result, err
}

// buildDirectJSONURL converts a canonical post URL to its www.reddit.com
// .json equivalent (same host as the challenge cookies).
func buildDirectJSONURL(postURL string) (string, error) {
	parsed, err := url.Parse(postURL)

	if err != nil {
		return "", fmt.Errorf("cannot parse post URL: %v", err)
	}

	path := strings.TrimRight(parsed.Path, "/")

	return fmt.Sprintf("https://www.reddit.com%s/.json", path), nil
}

// redditDirectGet performs a browser-UA GET with the shared client and
// returns the body (capped at 5MB) and status code.
func redditDirectGet(client *http.Client, rawURL string) ([]byte, int, error) {
	req, err := http.NewRequest("GET", rawURL, nil)

	if err != nil {
		return nil, 0, err
	}

	req.Header.Set("User-Agent", redditBrowserUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/json")

	resp, err := client.Do(req)

	if err != nil {
		return nil, 0, err
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))

	if err != nil {
		return nil, resp.StatusCode, err
	}

	return body, resp.StatusCode, nil
}
