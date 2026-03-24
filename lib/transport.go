package lib

import (
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// IsRetryableError reports whether err is a transient network error that
// warrants retrying the request. It recognizes connection resets, DNS failures,
// timeouts, unexpected EOFs, and TLS handshake failures.
func IsRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// Unwrap *url.Error (returned by http.Client.Do).
	var uerr *url.Error
	if errors.As(err, &uerr) {
		err = uerr.Err
	}

	// Connection reset by peer.
	if errors.Is(err, unix.ECONNRESET) {
		return true
	}
	// Connection refused (server not listening yet, or ephemeral failure).
	if errors.Is(err, unix.ECONNREFUSED) {
		return true
	}

	// Unexpected EOF mid-stream (server dropped the connection).
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	// Plain EOF from a read can also indicate a reset connection.
	if errors.Is(err, io.EOF) {
		return true
	}

	// DNS resolution failure.
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true
	}

	// Any net.OpError (dial, read, write) — covers connection refused, broken
	// pipe, and similar transient failures.
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}

	// Generic timeout.
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}

	// TLS handshake failures are often transient.
	if strings.Contains(err.Error(), "TLS handshake") {
		return true
	}

	return false
}

// retryTransport wraps an http.RoundTripper and retries requests that fail
// with transient network errors. It uses exponential backoff and only retries
// methods that are safe to retry (GET, HEAD, OPTIONS).
type retryTransport struct {
	base       http.RoundTripper
	maxRetries int
}

func isIdempotent(method string) bool {
	switch method {
	case "GET", "HEAD", "OPTIONS":
		return true
	}
	return false
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !isIdempotent(req.Method) {
		return t.base.RoundTrip(req)
	}

	var lastErr error
	for attempt := range t.maxRetries + 1 {
		resp, err := t.base.RoundTrip(req)
		if err == nil {
			return resp, nil
		}
		if !IsRetryableError(err) {
			return nil, err
		}
		lastErr = err

		if attempt < t.maxRetries {
			// Exponential backoff: 500ms, 1s, 2s, ...
			backoff := time.Duration(math.Pow(2, float64(attempt))) * 500 * time.Millisecond
			select {
			case <-time.After(backoff):
			case <-req.Context().Done():
				return nil, req.Context().Err()
			}
		}
	}
	return nil, lastErr
}
