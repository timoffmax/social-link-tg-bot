package services

import (
	"net"
	"net/http"
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
