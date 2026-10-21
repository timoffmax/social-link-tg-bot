package services

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// httpStatusError is a non-200 answer of a page fetch.
type httpStatusError struct {
	URL        string
	StatusCode int
}

// Error keeps the text the page fetch has always returned.
func (e *httpStatusError) Error() string {
	result := fmt.Sprintf("page %s status %d", e.URL, e.StatusCode)

	return result
}

// fetchCappedPage GETs a page with the given headers, reading at most
// maxBytes, and fails on a non-200 answer. Shared by the page-reading
// extractors (YouTube posts, Threads).
func fetchCappedPage(ctx context.Context, pageURL string, headers map[string]string, maxBytes int64, timeout time.Duration) (string, error) {
	result := ""
	var body []byte
	var resp *http.Response
	pageCtx, cancel := context.WithTimeout(ctx, timeout)

	defer cancel()

	req, err := http.NewRequestWithContext(pageCtx, http.MethodGet, pageURL, nil)

	if err == nil {
		for name, value := range headers {
			req.Header.Set(name, value)
		}

		resp, err = SharedHTTPClient.Do(req)
	}

	if err == nil {
		defer resp.Body.Close()

		limited := io.LimitReader(resp.Body, maxBytes)
		body, err = io.ReadAll(limited)
	}

	if (err == nil) && (resp.StatusCode != http.StatusOK) {
		err = &httpStatusError{URL: pageURL, StatusCode: resp.StatusCode}
	}

	if err == nil {
		result = string(body)
	}

	return result, err
}
