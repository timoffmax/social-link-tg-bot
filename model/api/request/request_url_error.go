package request

import (
	"errors"
	"fmt"
	neturl "net/url"
)

// WithoutRequestURL replaces a transport error, whose *url.Error text holds
// the request URL with the bot token, by its cause.
func WithoutRequestURL(method string, err error) error {
	result := err
	var urlErr *neturl.Error

	if errors.As(err, &urlErr) == true {
		result = fmt.Errorf("%s request failed: %w", method, urlErr.Err)
	}

	return result
}
