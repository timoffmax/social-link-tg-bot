package services

// Periodic health probes for the kk redirector services, so an outage is
// noticed (and the admin alerted) even when there is no user traffic. Both
// domains are the same DigitalOcean app behind Cloudflare, but they are probed
// separately in case only one platform's resolution breaks.
//
// Probes use their own outage-tracker keys ("<service>-probe"): a probe
// success must never reset failure counts accumulated from real user traffic,
// otherwise a low-traffic outage could stay below the alert threshold forever.

import (
	"fmt"
	"io"
	"log"
	"net/http"

	"github.com/timoffmax/social-link-bot/config"
)

// Known-stable public posts used as probe targets. If one of these is ever
// deleted, the redirector bounces back to the origin platform and the probe
// raises a false outage alert — replace the shortcode/ID in that case.
const (
	kkInstagramProbeShortcode = "DZW7SUjgcH0"
	kkTikTokProbeVideoID      = "7633407742224469268"
)

// CheckRedirectorHealth probes each kk redirector once and feeds the shared
// outage tracker, so repeated probe failures alert the admin and a later
// success sends a recovery notice.
func CheckRedirectorHealth() {
	probeRedirector(
		kkInstagramServiceName+"-probe",
		fmt.Sprintf("https://%s/reel/%s/", config.GetKKInstagramHost(), kkInstagramProbeShortcode),
		config.GetKKInstagramHost(),
		"instagram.com",
	)

	probeRedirector(
		kkTikTokServiceName+"-probe",
		fmt.Sprintf("https://www.%s/@i/video/%s", config.GetKKTikTokHost(), kkTikTokProbeVideoID),
		config.GetKKTikTokHost(),
		"tiktok.com",
	)
}

// probeRedirector fetches a known-good post with the bot UA exactly like the
// real extractors do (following redirects to the CDN), and validates the
// response with the same rules — final host off the redirector and origin,
// media content-type, and a plausible amount of body bytes — so failure modes
// on the CDN hop (e.g. datacenter-IP blocks) and interstitial redirects are
// detected too. Only the first ~10KB of the body are read.
func probeRedirector(service, probeURL, redirectorHost, originDomain string) {
	req, err := http.NewRequest("GET", probeURL, nil)

	if err != nil {
		return
	}

	req.Header.Set("User-Agent", redirectorBotUserAgent)

	resp, err := SharedHTTPClient.Do(req)

	if err != nil {
		log.Printf("🩺 [%s] probe request failed: %v", service, err)
		ReportServiceFailure(service, fmt.Sprintf("probe request failed: %v", err))

		return
	}

	defer resp.Body.Close()

	err = validateProbeResponse(resp, redirectorHost, originDomain)

	if err != nil {
		log.Printf("🩺 [%s] probe failed: %v", service, err)
		ReportServiceFailure(service, fmt.Sprintf("probe: %v", err))

		return
	}

	log.Printf("🩺 [%s] probe OK", service)
	ReportServiceSuccess(service)
}

// validateProbeResponse applies the traffic-equivalent media validation to a
// probe response, reading just enough of the body to pass the size threshold.
func validateProbeResponse(resp *http.Response, redirectorHost, originDomain string) error {
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("expected 200 after redirects, got HTTP %d", resp.StatusCode)
	}

	finalURL := ""

	if (resp.Request != nil) && (resp.Request.URL != nil) {
		finalURL = resp.Request.URL.String()
	}

	contentType := resp.Header.Get("Content-Type")

	read, _ := io.Copy(io.Discard, io.LimitReader(resp.Body, redirectorMinMediaSize+1))

	return validateRedirectorMedia(redirectorHost, originDomain, finalURL, contentType, read)
}
