package lib

import (
	"testing"
	"time"
)

func ptr(s string) *string { return &s }

func TestWorkflowRunIsCompleted(t *testing.T) {
	tests := []struct {
		status string
		want   bool
	}{
		{"completed", true},
		{"in_progress", false},
		{"queued", false},
	}
	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			r := &WorkflowRun{Status: tt.status}
			if got := r.IsCompleted(); got != tt.want {
				t.Errorf("IsCompleted() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWorkflowRunIsSuccess(t *testing.T) {
	tests := []struct {
		name       string
		status     string
		conclusion *string
		want       bool
	}{
		{"success", "completed", ptr("success"), true},
		{"failure", "completed", ptr("failure"), false},
		{"nil_conclusion", "completed", nil, false},
		{"in_progress", "in_progress", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &WorkflowRun{Status: tt.status, Conclusion: tt.conclusion}
			if got := r.IsSuccess(); got != tt.want {
				t.Errorf("IsSuccess() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWorkflowRunIsFailed(t *testing.T) {
	tests := []struct {
		name       string
		status     string
		conclusion *string
		want       bool
	}{
		{"failure", "completed", ptr("failure"), true},
		{"cancelled", "completed", ptr("cancelled"), true},
		{"timed_out", "completed", ptr("timed_out"), true},
		{"success", "completed", ptr("success"), false},
		{"skipped", "completed", ptr("skipped"), false},
		{"nil_conclusion", "completed", nil, false},
		{"in_progress", "in_progress", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &WorkflowRun{Status: tt.status, Conclusion: tt.conclusion}
			if got := r.IsFailed(); got != tt.want {
				t.Errorf("IsFailed() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWorkflowRunDuration(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name         string
		runStartedAt *time.Time
		updatedAt    time.Time
		status       string
		wantZero     bool
	}{
		{"nil_start", nil, now, "completed", true},
		{"completed", timePtr(now.Add(-5 * time.Minute)), now, "completed", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &WorkflowRun{
				Status:       tt.status,
				RunStartedAt: tt.runStartedAt,
				UpdatedAt:    tt.updatedAt,
			}
			got := r.Duration()
			if tt.wantZero && got != 0 {
				t.Errorf("Duration() = %v, want 0", got)
			}
			if !tt.wantZero && got == 0 {
				t.Errorf("Duration() = 0, want non-zero")
			}
		})
	}
}

func timePtr(t time.Time) *time.Time { return &t }
