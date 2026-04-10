package lib

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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
		// No ##[error] lines, so we just get the tail with a gap marker.
		{"last_two_no_errors", "line1\nline2\nline3\n", 2,
			"\n... (omitting lines 1..1, use --failed-output-lines to show more output) ...\n\nline2\nline3\n"},
		{"last_one_no_errors", "line1\nline2\nline3\n", 1,
			"\n... (omitting lines 1..2, use --failed-output-lines to show more output) ...\n\nline3\n"},
		{"five_lines_last_three_no_errors", "a\nb\nc\nd\ne\n", 3,
			"\n... (omitting lines 1..2, use --failed-output-lines to show more output) ...\n\nc\nd\ne\n"},
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

// buildLog creates a log with numbered lines, inserting special content at
// specified positions. Lines are 1-indexed to match the gap marker output.
func buildLog(totalLines int, special map[int]string) string {
	var buf bytes.Buffer
	for i := 1; i <= totalLines; i++ {
		if s, ok := special[i]; ok {
			buf.WriteString(s)
		} else {
			fmt.Fprintf(&buf, "line %d\n", i)
		}
	}
	return buf.String()
}

func TestFindBuildFailureErrorContext(t *testing.T) {
	// 50-line log with an ##[error] at line 30. Budget = 10.
	// Error context: lines 10..30 (21 lines, but capped by budget interaction).
	// With budget 10: error region = lines 10..30 = 21 lines which exceeds
	// budget. But error regions are always included; remaining tail budget
	// would be negative, so only error context is shown.
	log := buildLog(50, map[int]string{
		30: "##[error]something broke\n",
	})

	got := findBuildFailure([]byte(log), 25)

	// Error context: lines 10-30 = 21 lines. Tail budget: 25-21 = 4 lines
	// (lines 47-50). Lines 31-46 are omitted.
	if !bytes.Contains([]byte(got), []byte("##[error]something broke")) {
		t.Error("output should contain the error line")
	}
	if !bytes.Contains([]byte(got), []byte("line 10\n")) {
		t.Errorf("output should contain line 10 (start of error context)\ngot:\n%s", got)
	}
	if bytes.Contains([]byte(got), []byte("line 9\n")) {
		t.Error("output should NOT contain line 9 (before error context)")
	}
	if !bytes.Contains([]byte(got), []byte("line 50\n")) {
		t.Error("output should contain line 50 (tail)")
	}
	if !bytes.Contains([]byte(got), []byte("omitting lines")) {
		t.Errorf("output should contain a gap marker\ngot:\n%s", got)
	}
}

func TestFindBuildFailureErrorAtEnd(t *testing.T) {
	// Error near the tail — context region overlaps with the tail, so no gap
	// between them.
	log := buildLog(50, map[int]string{
		48: "##[error]late failure\n",
	})

	got := findBuildFailure([]byte(log), 30)

	// Error context: lines 28-48 = 21 lines. Tail budget: 30-21 = 9 lines
	// (lines 42-50). These overlap with the error region, producing a single
	// contiguous block from line 28 to 50 (23 lines).
	if bytes.Count([]byte(got), []byte("omitting lines")) != 1 {
		t.Errorf("expected exactly one gap marker (at the start)\ngot:\n%s", got)
	}
	if !bytes.Contains([]byte(got), []byte("line 28\n")) {
		t.Errorf("output should contain line 28 (start of error context)\ngot:\n%s", got)
	}
}

func TestFindBuildFailureTwoErrors(t *testing.T) {
	// Two errors far apart.
	log := buildLog(100, map[int]string{
		25: "##[error]first error\n",
		75: "##[error]second error\n",
	})

	got := findBuildFailure([]byte(log), 60)

	// First error context: lines 5-25 = 21 lines.
	// Second error context: lines 55-75 = 21 lines.
	// Error budget: 42. Tail budget: 60-42 = 18 lines (lines 83-100).
	// Three gaps: before line 5, between 26-54, between 76-82.
	if !bytes.Contains([]byte(got), []byte("##[error]first error")) {
		t.Error("should contain first error")
	}
	if !bytes.Contains([]byte(got), []byte("##[error]second error")) {
		t.Error("should contain second error")
	}
	if !bytes.Contains([]byte(got), []byte("line 100\n")) {
		t.Error("should contain last line")
	}

	gapCount := bytes.Count([]byte(got), []byte("omitting lines"))
	if gapCount != 3 {
		t.Errorf("expected 3 gap markers, got %d\ngot:\n%s", gapCount, got)
	}
}

func TestFindBuildFailureNoErrorsFallback(t *testing.T) {
	// No ##[error] lines at all — should behave like the old tail-only logic.
	log := buildLog(50, nil)
	got := findBuildFailure([]byte(log), 10)

	if !bytes.Contains([]byte(got), []byte("line 50\n")) {
		t.Error("should contain last line")
	}
	if !bytes.Contains([]byte(got), []byte("line 41\n")) {
		t.Errorf("should contain line 41 (start of tail)\ngot:\n%s", got)
	}
	if bytes.Contains([]byte(got), []byte("line 40\n")) {
		t.Error("should NOT contain line 40")
	}
}

func TestFindBuildFailureErrorBudgetExceedsTotal(t *testing.T) {
	// Error context alone exceeds the budget — tail gets 0 extra lines,
	// but all error context is still shown.
	log := buildLog(50, map[int]string{
		25: "##[error]err1\n",
		30: "##[error]err2\n",
	})
	// err1 context: 5-25 = 21 lines. err2 context: 10-30 = 21 lines.
	// Overlap: 10-25 = 16 lines shared. Unique: 21+21-16 = 26 lines.
	// Budget = 10 → tail budget negative → only error context shown.
	got := findBuildFailure([]byte(log), 10)

	if !bytes.Contains([]byte(got), []byte("##[error]err1")) {
		t.Error("should contain first error")
	}
	if !bytes.Contains([]byte(got), []byte("##[error]err2")) {
		t.Error("should contain second error")
	}
	// No tail lines beyond the error context should appear.
	if bytes.Contains([]byte(got), []byte("line 50\n")) {
		t.Errorf("should NOT contain line 50 (no tail budget)\ngot:\n%s", got)
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
