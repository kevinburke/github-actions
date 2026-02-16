package main

import (
	"net"
	"net/url"
	"testing"
	"time"
)

func TestShouldPrint(t *testing.T) {
	tests := []struct {
		name        string
		elapsed     time.Duration
		sinceLastPr time.Duration
		want        bool
	}{
		{"under_1m_before_10s", 30 * time.Second, 5 * time.Second, false},
		{"under_1m_after_10s", 30 * time.Second, 11 * time.Second, true},
		{"1m_to_3m_before_15s", 2 * time.Minute, 10 * time.Second, false},
		{"1m_to_3m_after_15s", 2 * time.Minute, 16 * time.Second, true},
		{"3m_to_5m_before_20s", 4 * time.Minute, 15 * time.Second, false},
		{"3m_to_5m_after_20s", 4 * time.Minute, 21 * time.Second, true},
		{"5m_to_8m_before_30s", 6 * time.Minute, 25 * time.Second, false},
		{"5m_to_8m_after_30s", 6 * time.Minute, 31 * time.Second, true},
		{"8m_to_25m_before_2m", 10 * time.Minute, time.Minute, false},
		{"8m_to_25m_after_2m", 10 * time.Minute, 2*time.Minute + time.Second, true},
		{"over_25m_before_3m", 30 * time.Minute, 2 * time.Minute, false},
		{"over_25m_after_3m", 30 * time.Minute, 3*time.Minute + time.Second, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lastPrinted := time.Now().Add(-tt.sinceLastPr)
			got := shouldPrint(lastPrinted, tt.elapsed)
			if got != tt.want {
				t.Errorf("shouldPrint(elapsed=%s, sinceLastPrint=%s) = %v, want %v",
					tt.elapsed, tt.sinceLastPr, got, tt.want)
			}
		})
	}
}

func TestIsHttpError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"generic_error", net.UnknownNetworkError("foo"), false},
		{"dns_error", &net.DNSError{Err: "no such host", Name: "example.com"}, true},
		{"dial_tcp_error", &net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{}}, true},
		{"read_tcp_error", &net.OpError{Op: "read", Net: "tcp", Err: &net.DNSError{}}, false},
		{"url_error_wrapping_dns", &url.Error{Op: "Get", URL: "https://example.com", Err: &net.DNSError{}}, true},
		{"url_error_wrapping_generic", &url.Error{Op: "Get", URL: "https://example.com", Err: net.UnknownNetworkError("foo")}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isHttpError(tt.err)
			if got != tt.want {
				t.Errorf("isHttpError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
