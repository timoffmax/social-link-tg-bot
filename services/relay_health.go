package services

// Hourly probe of the home relay's /healthz (no token, no platform traffic),
// run from CheckRedirectorHealth. Each platform the bot sends to the relay
// has its own probe key ("<platform>-relay-probe"), apart from the traffic
// key, so a probe success never hides failures of real requests.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

const (
	// relayProbeTimeout bounds one /healthz request.
	relayProbeTimeout = 10 * time.Second

	// relayProbeAlertThreshold is the number of consecutive misses that
	// alert the admin (a short home outage or a reboot stays quiet).
	relayProbeAlertThreshold = 3

	// relayHealthBodyLimit bounds the /healthz body that is read.
	relayHealthBodyLimit = 16 * 1024

	relayBreakerOpen   = "open"
	relayProbeSuffix   = serviceProbeSuffix
	relayHealthLogName = "relay-probe"
)

// errRelayHealthNotOK marks a /healthz answer without "ok":true.
var errRelayHealthNotOK = errors.New("healthz says ok=false")

// relayHealth is the part of /healthz the bot reads; the probe uses ok and
// the platforms' enabled and breaker, the relay status all of it.
type relayHealth struct {
	OK          bool                           `json:"ok"`
	YtDlp       string                         `json:"yt_dlp"`
	BreakerOpen []string                       `json:"breaker_open"`
	Global      relayHealthCounters            `json:"global"`
	Platforms   map[string]relayPlatformHealth `json:"platforms"`
}

// relayHealthCounters are the relay's calls this hour and day against its
// limits.
type relayHealthCounters struct {
	Hour        int `json:"hour"`
	Day         int `json:"day"`
	HourlyLimit int `json:"hourly_limit"`
	DailyLimit  int `json:"daily_limit"`
}

// relayPlatformHealth is one platform's entry of /healthz.
type relayPlatformHealth struct {
	Enabled      bool   `json:"enabled"`
	Breaker      string `json:"breaker"`
	BreakerLeftS int    `json:"breaker_left_s"`
	Hour         int    `json:"hour"`
	Day          int    `json:"day"`
	HourlyLimit  int    `json:"hourly_limit"`
	DailyLimit   int    `json:"daily_limit"`
}

// probeRelay reads /healthz once when the relay is configured and reports
// every platform the bot sends to it: a failed request, a bad answer, a
// missing platform or an open breaker is a miss; a platform the relay has
// switched off is no miss (the owner's setting) and is skipped by the chain
// until a later probe sees it enabled.
func probeRelay() {
	warnRelayConfigOnce()
	client := newRelayClient("")
	platforms := relayEnabledPlatforms()
	isConfigured := (client != nil) && (len(platforms) > 0)

	if isConfigured == true {
		health, err := client.readHealth()
		reportRelayHealth(health, err, platforms)
	}
}

// readHealth GETs /healthz under relayProbeTimeout through the shared
// relayHTTPClient and decodes it.
func (c *relayClient) readHealth() (relayHealth, error) {
	result := relayHealth{}
	ctx, cancel := context.WithTimeout(context.Background(), relayProbeTimeout)
	defer cancel()

	healthURL := fmt.Sprintf("%s%s", c.baseURL, relayHealthPath)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
	var resp *http.Response

	if err == nil {
		resp, err = relayHTTPClient.Do(req)
	}

	if err == nil {
		defer resp.Body.Close()

		err = decodeRelayHealth(resp, &result)
	}

	if (err == nil) && (result.OK == false) {
		err = errRelayHealthNotOK
	}

	return result, err
}

// decodeRelayHealth requires HTTP 200 and decodes the JSON body.
func decodeRelayHealth(resp *http.Response, health *relayHealth) error {
	limited := io.LimitReader(resp.Body, relayHealthBodyLimit)
	raw, result := io.ReadAll(limited)

	if (result == nil) && (resp.StatusCode != http.StatusOK) {
		result = fmt.Errorf("healthz HTTP %d: %q", resp.StatusCode, raw)
	}

	if result == nil {
		result = json.Unmarshal(raw, health)
	}

	return result
}

// reportRelayHealth feeds each platform's probe key with the probe's
// verdict and records which platforms the relay has switched off.
func reportRelayHealth(health relayHealth, err error, platforms []string) {
	for _, platform := range platforms {
		reportRelayPlatformHealth(health, err, platform)
	}
}

// reportRelayPlatformHealth reports one platform's verdict.
func reportRelayPlatformHealth(health relayHealth, err error, platform string) {
	trafficService := relayServiceName(platform)
	service := fmt.Sprintf("%s%s", trafficService, relayProbeSuffix)
	entry, isListed := health.Platforms[platform]
	isDisabled := (err == nil) && (isListed == true) && (entry.Enabled == false)
	missErr := relayHealthMiss(err, entry, isListed, platform)

	if (err == nil) && (isListed == true) {
		setRelayPlatformDisabled(platform, isDisabled)
	}

	if isDisabled == true {
		log.Printf("🩺 [%s] %s is disabled on the relay, not a miss", relayHealthLogName, platform)
	}

	if (isDisabled == false) && (missErr != nil) {
		log.Printf("🩺 [%s] probe failed: %v", service, missErr)
		detail := fmt.Sprintf("probe: %v", missErr)
		ReportServiceFailureWithThreshold(service, detail, relayProbeAlertThreshold)
	}

	if (isDisabled == false) && (missErr == nil) {
		log.Printf("🩺 [%s] probe OK", service)
		ReportServiceSuccess(service)
	}
}

// relayHealthMiss returns why the probe counts as a miss for platform, or
// nil: the request failed, the platform is missing, or its breaker is open.
func relayHealthMiss(err error, entry relayPlatformHealth, isListed bool, platform string) error {
	result := err

	if (result == nil) && (isListed == false) {
		result = fmt.Errorf("healthz does not list %s", platform)
	}

	if (result == nil) && (entry.Breaker == relayBreakerOpen) {
		result = fmt.Errorf("%s breaker is open", platform)
	}

	return result
}
