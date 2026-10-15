package services

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"
)

// SharedHTTPClient is a reusable HTTP client for general-purpose service calls.
// It uses connection pooling and a 30-second timeout suitable for API requests.
var SharedHTTPClient = &http.Client{
	Timeout: 30 * time.Second,
	Transport: &http.Transport{
		MaxIdleConns:        20,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
	},
}

// SharedDownloadClient is a reusable HTTP client for downloading media files.
// It uses a longer 60-second timeout and larger connection pool.
var SharedDownloadClient = &http.Client{
	Timeout: 60 * time.Second,
	Transport: &http.Transport{
		MaxIdleConns:        30,
		MaxIdleConnsPerHost: 15,
		IdleConnTimeout:     90 * time.Second,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
	},
}

// downloadToFile downloads a URL to a local file
func downloadToFile(url, filePath string) error {
	resp, err := SharedDownloadClient.Get(url)
	if err != nil {
		return fmt.Errorf("failed to fetch: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status code: %d", resp.StatusCode)
	}

	out, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("failed to create file: %v", err)
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	if err != nil {
		return fmt.Errorf("failed to write file: %v", err)
	}

	return nil
}
