package services

// Health check for the configured YTDLP_PROXIES pool. Reddit extraction hard
// depends on these proxies (both /s/ short-link resolution fallback and the
// JSON API fetch), so dead credentials — e.g. an expired subscription
// answering HTTP 407 — silently kill the whole Reddit pipeline. This check
// makes that state visible in logs and alerts the admin.

import (
	"fmt"
	"log"
	"strings"

	"github.com/timoffmax/social-link-bot/config"
)

// proxyPoolServiceName is the outage-tracker key for the whole proxy pool.
const proxyPoolServiceName = "proxy-pool"

// CheckProxyHealth tests each configured proxy with a lightweight request and
// alerts the admin when none of them work.
func CheckProxyHealth() {
	proxies := config.GetYtDlpProxies()

	if len(proxies) == 0 {
		log.Printf("🌐 No proxies configured — skipping proxy health check")

		return
	}

	working := 0
	lastDetail := ""

	for _, proxy := range proxies {
		err := testProxy(proxy)

		if err != nil {
			lastDetail = err.Error()
			log.Printf("🌐 Proxy check FAILED for %s: %v", maskProxyCredentials(proxy), err)

			continue
		}

		working++
	}

	log.Printf("🌐 Proxy health: %d/%d working", working, len(proxies))

	if working == 0 {
		// 407 means the credentials themselves are rejected — that is
		// definitive (expired subscription), so alert on the first sweep.
		// Other errors may be transient network blips, so require two
		// consecutive failed sweeps.
		threshold := 2

		if strings.Contains(lastDetail, "407") {
			threshold = 1
		}

		detail := fmt.Sprintf(
			"all %d configured proxies are failing (last error: %s). Reddit extraction is DOWN until they are renewed/replaced (check YTDLP_PROXIES in .env)",
			len(proxies),
			lastDetail,
		)
		ReportServiceFailureWithThreshold(proxyPoolServiceName, detail, threshold)

		return
	}

	ReportServiceSuccess(proxyPoolServiceName)
}

// testProxy performs an HTTPS request through the proxy; an auth or connect
// failure (e.g. 407 on CONNECT) surfaces as an error.
func testProxy(proxyURL string) error {
	client, err := createProxyClient(proxyURL)

	if err != nil {
		return err
	}

	resp, err := client.Get("https://api.telegram.org/")

	if err != nil {
		return err
	}

	resp.Body.Close()

	return nil
}
