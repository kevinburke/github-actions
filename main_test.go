package main

import (
	"context"
	"net"
	"net/url"
	"strings"
	"testing"
	"time"

	ghactions "github.com/kevinburke/github-actions/lib"
)

func stringPtr(s string) *string { return &s }

func timeRef(t time.Time) *time.Time { return &t }

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
		{"read_tcp_error", &net.OpError{Op: "read", Net: "tcp", Err: &net.DNSError{}}, true},
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

func TestNetworkStallBudget(t *testing.T) {
	now := time.Now()
	completedConclusion := stringPtr("success")

	if got := networkStallBudget(nil); got != 2*time.Minute {
		t.Fatalf("networkStallBudget(nil) = %s, want 2m", got)
	}

	longRunning := networkStallBudget([]ghactions.WorkflowRun{
		{
			Status:       "in_progress",
			RunStartedAt: timeRef(now.Add(-12 * time.Minute)),
		},
	})
	if longRunning < 7*time.Minute || longRunning > 9*time.Minute {
		t.Fatalf("networkStallBudget(long running) = %s, want roughly 8m", longRunning)
	}

	nearDone := networkStallBudget([]ghactions.WorkflowRun{
		{
			Status:       "completed",
			Conclusion:   completedConclusion,
			RunStartedAt: timeRef(now.Add(-20 * time.Minute)),
			UpdatedAt:    now,
		},
		{
			Status:       "in_progress",
			RunStartedAt: timeRef(now.Add(-18 * time.Minute)),
		},
	})
	if nearDone < 2*time.Minute || nearDone > 4*time.Minute {
		t.Fatalf("networkStallBudget(near done) = %s, want roughly 3m", nearDone)
	}
	if nearDone >= longRunning {
		t.Fatalf("expected near-done budget %s to be smaller than long-running budget %s", nearDone, longRunning)
	}
}

func TestWaitTimeoutError(t *testing.T) {
	now := time.Now()
	err := waitTimeoutError(now.Add(-7*time.Minute), now.Add(-5*time.Minute), "0123456789abcdef", nil, &url.Error{
		Op:  "Get",
		URL: "https://api.github.com/repos/kevinburke/github-actions/actions/runs",
		Err: context.DeadlineExceeded,
	})
	msg := err.Error()
	for _, want := range []string{
		"could not reach GitHub for 5m",
		"waited 7m",
		"last error: request timed out",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("waitTimeoutError() = %q, missing %q", msg, want)
		}
	}
}

func TestWaitTimeoutErrorTopLevelTimeout(t *testing.T) {
	now := time.Now()
	err := waitTimeoutError(now.Add(-65*time.Minute), now, "0123456789abcdef", []ghactions.WorkflowRun{{Status: "in_progress"}}, nil)
	msg := err.Error()
	for _, want := range []string{
		"timed out after waiting 1h5m",
		"workflow runs to complete",
		"hit --timeout, not a network error",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("waitTimeoutError() = %q, missing %q", msg, want)
		}
	}
}

func TestWaitTimeoutErrorNoRuns(t *testing.T) {
	now := time.Now()
	err := waitTimeoutError(now.Add(-3*time.Minute), now, "0123456789abcdef", nil, nil)
	msg := err.Error()
	for _, want := range []string{
		"timed out after waiting 3m",
		"workflow runs to appear for 01234567",
		"hit --timeout, not a network error",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("waitTimeoutError() = %q, missing %q", msg, want)
		}
	}
}

func TestWaitErrorForRetryablePollFailurePrefersTopLevelTimeout(t *testing.T) {
	now := time.Now()
	ctx, cancel := context.WithDeadline(context.Background(), now.Add(-time.Second))
	defer cancel()

	err := waitErrorForRetryablePollFailure(ctx, now.Add(-10*time.Second), now.Add(-3*time.Second), "0123456789abcdef", []ghactions.WorkflowRun{{Status: "in_progress"}}, &url.Error{
		Op:  "Get",
		URL: "https://api.github.com/repos/kevinburke/github-actions/actions/runs",
		Err: context.DeadlineExceeded,
	})
	msg := err.Error()

	if strings.Contains(msg, "could not reach GitHub") {
		t.Fatalf("waitErrorForRetryablePollFailure() = %q, should prefer top-level timeout", msg)
	}
	for _, want := range []string{
		"timed out after waiting 10s",
		"hit --timeout, not a network error",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("waitErrorForRetryablePollFailure() = %q, missing %q", msg, want)
		}
	}
}
