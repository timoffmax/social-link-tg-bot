package services

// Shared helpers for kk-style redirector services (kkinstagram.com,
// kktiktok.com) that 302-redirect bot user-agents to signed CDN media URLs
// resolved on their own infrastructure. When such a service breaks, it does
// NOT return an error status — it serves 200 HTML on its own host, 5xx, or a
// 302 back to the origin platform. validateRedirectorMedia turns all of those
// into loud failures so the extraction chain falls back instead of sending
// garbage to the chat.

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// errRedirectorOriginBounce marks the ambiguous failure where the redirector
// bounced back to the origin platform. For user traffic this usually means a
// deleted/private post rather than a service outage, so callers should not
// feed it into the outage tracker (probes with known-good posts should).
var errRedirectorOriginBounce = errors.New("redirector bounced to origin platform")

// redirectorBotUserAgent must be a bot UA — a normal browser UA gets a 301
// app-deeplink interstitial instead of the media redirect.
const redirectorBotUserAgent = "TelegramBot (like TwitterBot)"

// redirectorMinMediaSize guards against tiny HTML/error bodies being treated as media.
const redirectorMinMediaSize = int64(10 * 1024)

// downloadWithUA fetches a URL with a custom User-Agent, following redirects, and
// writes the body to filePath. It returns the final (post-redirect) URL, the
// response Content-Type, and the number of bytes written.
func downloadWithUA(rawURL, userAgent, filePath string) (string, string, int64, error) {
	req, err := http.NewRequest("GET", rawURL, nil)

	if err != nil {
		return "", "", 0, fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("User-Agent", userAgent)

	resp, err := SharedDownloadClient.Do(req)

	if err != nil {
		return "", "", 0, fmt.Errorf("request failed: %v", err)
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", 0, fmt.Errorf("bad status code: %d", resp.StatusCode)
	}

	finalURL := rawURL

	if (resp.Request != nil) && (resp.Request.URL != nil) {
		finalURL = resp.Request.URL.String()
	}

	contentType := resp.Header.Get("Content-Type")

	out, err := os.Create(filePath)

	if err != nil {
		return "", "", 0, fmt.Errorf("failed to create file: %v", err)
	}

	defer out.Close()

	size, err := io.Copy(out, resp.Body)

	if err != nil {
		return finalURL, contentType, size, fmt.Errorf("failed to write file: %v", err)
	}

	return finalURL, contentType, size, nil
}

// validateRedirectorMedia checks that a redirector response is real CDN media:
// the request must have been redirected off the redirector's host, must not
// have bounced back to the origin platform (the redirector's failure mode when
// it cannot resolve a post), and the body must be an image/video of plausible
// size.
func validateRedirectorMedia(redirectorHost, originDomain, finalURL, contentType string, size int64) error {
	parsed, err := url.Parse(finalURL)

	if err != nil {
		return fmt.Errorf("cannot parse final URL %s: %v", finalURL, err)
	}

	host := strings.ToLower(parsed.Hostname())

	if hostMatchesDomain(host, redirectorHost) {
		return fmt.Errorf("no redirect off %s (final URL %s) — service likely blocked or down", redirectorHost, finalURL)
	}

	if hostMatchesDomain(host, originDomain) {
		return fmt.Errorf("%w: redirected back to %s (final URL %s)", errRedirectorOriginBounce, originDomain, finalURL)
	}

	mimeType := extractMimeType(contentType)

	if (strings.HasPrefix(mimeType, "image/") == false) && (strings.HasPrefix(mimeType, "video/") == false) {
		return fmt.Errorf("non-media content-type %q from %s", contentType, host)
	}

	if size < redirectorMinMediaSize {
		return fmt.Errorf("suspiciously small media body (%d bytes) from %s", size, host)
	}

	return nil
}

// hostMatchesDomain reports whether host equals domain or is a subdomain of it.
func hostMatchesDomain(host, domain string) bool {
	domain = strings.ToLower(domain)

	return (host == domain) || strings.HasSuffix(host, "."+domain)
}

// extractMimeType returns the lowercase media type from a Content-Type header value.
func extractMimeType(contentType string) string {
	return strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
}

// classifyRedirectorMedia determines the Telegram media type and file extension
// from the response Content-Type, falling back to the final URL path.
func classifyRedirectorMedia(contentType, finalURL string) (string, string) {
	mimeType := extractMimeType(contentType)

	if strings.HasPrefix(mimeType, "video/") {
		return "video", ".mp4"
	}

	if strings.HasPrefix(mimeType, "image/") {
		_, ext := classifyMediaURL(finalURL)

		return "photo", ext
	}

	return classifyMediaURL(finalURL)
}
