package lib

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func TestFailedJobURL(t *testing.T) {
	tests := []struct {
		name string
		job  *Job
		want string
	}{
		{
			name: "failed step anchor",
			job: &Job{
				HTMLURL: "https://github.com/o/r/actions/runs/1/job/2",
				Steps: []Step{
					{Number: 1},
					{Number: 17, Conclusion: stringPtr("failure")},
				},
			},
			want: "https://github.com/o/r/actions/runs/1/job/2#step:17:1",
		},
		{
			name: "job url only",
			job: &Job{
				HTMLURL: "https://github.com/o/r/actions/runs/1/job/2",
			},
			want: "https://github.com/o/r/actions/runs/1/job/2",
		},
		{
			name: "missing job",
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := failedJobURL(tt.job)
			if got != tt.want {
				t.Fatalf("failedJobURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func stringPtr(s string) *string { return &s }

func TestFindBuildFailure(t *testing.T) {
	tests := []struct {
		name           string
		log            string
		numOutputLines int
		want           string
	}{
		{"empty", "", 10, ""},
		{"fewer_lines_than_requested", "line1\nline2\n", 10, "line1\nline2\n"},
		{"exact_lines", "line1\nline2\nline3\n", 3, "line1\nline2\nline3\n"},
		{"last_two", "line1\nline2\nline3\n", 2, "line2\nline3\n"},
		{"last_one", "line1\nline2\nline3\n", 1, "line3\n"},
		{"five_lines_last_three", "a\nb\nc\nd\ne\n", 3, "c\nd\ne\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := string(findBuildFailure([]byte(tt.log), tt.numOutputLines))
			if got != tt.want {
				t.Errorf("findBuildFailure(%q, %d) = %q, want %q",
					tt.log, tt.numOutputLines, got, tt.want)
			}
		})
	}
}

func TestParseRateLimit(t *testing.T) {
	t.Run("missing header returns nil", func(t *testing.T) {
		if got := parseRateLimit(http.Header{}); got != nil {
			t.Errorf("parseRateLimit(empty) = %+v, want nil", got)
		}
	})

	t.Run("populated headers", func(t *testing.T) {
		reset := time.Now().Add(30 * time.Minute).Unix()
		h := http.Header{}
		h.Set("X-RateLimit-Limit", "5000")
		h.Set("X-RateLimit-Remaining", "4321")
		h.Set("X-RateLimit-Reset", strconv.FormatInt(reset, 10))
		h.Set("X-RateLimit-Resource", "core")

		rl := parseRateLimit(h)
		if rl == nil {
			t.Fatal("parseRateLimit returned nil")
		}
		if rl.Limit != 5000 || rl.Remaining != 4321 {
			t.Errorf("limit/remaining = %d/%d, want 5000/4321", rl.Limit, rl.Remaining)
		}
		if rl.Reset.Unix() != reset {
			t.Errorf("reset = %d, want %d", rl.Reset.Unix(), reset)
		}
		if rl.Resource != "core" {
			t.Errorf("resource = %q, want core", rl.Resource)
		}
		if rl.ObservedAt.IsZero() {
			t.Error("ObservedAt should be set")
		}
	})

	t.Run("garbage remaining returns nil", func(t *testing.T) {
		h := http.Header{}
		h.Set("X-RateLimit-Remaining", "not-a-number")
		if got := parseRateLimit(h); got != nil {
			t.Errorf("parseRateLimit(garbage) = %+v, want nil", got)
		}
	})
}

// TestClientRecordsRateLimitOnSuccess verifies that the rateLimitTransport
// captures rate limit headers from successful responses, not just errors.
func TestClientRecordsRateLimitOnSuccess(t *testing.T) {
	reset := time.Now().Add(15 * time.Minute).Unix()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Limit", "5000")
		w.Header().Set("X-RateLimit-Remaining", "1234")
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(reset, 10))
		w.Header().Set("X-RateLimit-Resource", "core")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`{"total_count":0,"workflow_runs":[]}`))
	}))
	defer srv.Close()

	c := NewClient("token", "github.com")
	// Redirect the client at our test server.
	c.Client.Base = srv.URL

	if _, err := c.Repo("o", "r").FindWorkflowRunsForCommit(context.Background(), "deadbeef"); err != nil {
		t.Fatalf("FindWorkflowRunsForCommit: %v", err)
	}

	rl := c.RateLimit()
	if rl == nil {
		t.Fatal("RateLimit() returned nil after successful request")
	}
	if rl.Remaining != 1234 || rl.Limit != 5000 {
		t.Errorf("rate limit = %d/%d, want 1234/5000", rl.Remaining, rl.Limit)
	}
}

// TestClientReturnsRateLimitErrorOn403 verifies that the ErrorParser returns
// a typed *RateLimitError when GitHub returns 403 with X-RateLimit-Remaining: 0.
func TestClientReturnsRateLimitErrorOn403(t *testing.T) {
	reset := time.Now().Add(20 * time.Minute).Unix()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Limit", "5000")
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(reset, 10))
		w.Header().Set("X-RateLimit-Resource", "core")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(403)
		w.Write([]byte(`{"message":"API rate limit exceeded"}`))
	}))
	defer srv.Close()

	c := NewClient("token", "github.com")
	c.Client.Base = srv.URL

	_, err := c.Repo("o", "r").FindWorkflowRunsForCommit(context.Background(), "deadbeef")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	rle, ok := IsRateLimitError(err)
	if !ok {
		t.Fatalf("expected RateLimitError, got %T: %v", err, err)
	}
	if rle.Reset.Unix() != reset {
		t.Errorf("Reset = %d, want %d", rle.Reset.Unix(), reset)
	}
	if rle.Limit != 5000 || rle.Resource != "core" {
		t.Errorf("limit/resource = %d/%q, want 5000/core", rle.Limit, rle.Resource)
	}

	// Also verify a generic 403 (remaining > 0) does NOT become a
	// RateLimitError.
	var generic *Error
	if errors.As(err, &generic) {
		t.Errorf("403 with remaining=0 should not also be a generic *Error: %v", generic)
	}
}

func TestClientReturnsGenericErrorOn403WithRemaining(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "100")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(403)
		w.Write([]byte(`{"message":"Forbidden"}`))
	}))
	defer srv.Close()

	c := NewClient("token", "github.com")
	c.Client.Base = srv.URL

	_, err := c.Repo("o", "r").FindWorkflowRunsForCommit(context.Background(), "deadbeef")
	if err == nil {
		t.Fatal("expected error")
	}
	if _, ok := IsRateLimitError(err); ok {
		t.Errorf("403 with remaining=100 should not be a RateLimitError: %v", err)
	}
}
