package main

import (
	"testing"
	"time"

	ghactions "github.com/kevinburke/github-actions/lib"
)

func ptr(s string) *string { return &s }

func timePtr(t time.Time) *time.Time { return &t }

func TestStatusIcon(t *testing.T) {
	s := &statusRenderer{}

	tests := []struct {
		name     string
		run      ghactions.WorkflowRun
		wantIcon string
	}{
		{
			name: "success",
			run: ghactions.WorkflowRun{
				Status:     "completed",
				Conclusion: ptr("success"),
			},
			wantIcon: "✓",
		},
		{
			name: "failure",
			run: ghactions.WorkflowRun{
				Status:     "completed",
				Conclusion: ptr("failure"),
			},
			wantIcon: "✗",
		},
		{
			name: "cancelled",
			run: ghactions.WorkflowRun{
				Status:     "completed",
				Conclusion: ptr("cancelled"),
			},
			wantIcon: "✗",
		},
		{
			name: "skipped",
			run: ghactions.WorkflowRun{
				Status:     "completed",
				Conclusion: ptr("skipped"),
			},
			wantIcon: "-",
		},
		{
			name: "queued",
			run: ghactions.WorkflowRun{
				Status: "queued",
			},
			wantIcon: "□",
		},
		{
			name: "in_progress_spinner_frame_0",
			run: ghactions.WorkflowRun{
				Status: "in_progress",
			},
			wantIcon: "⠋",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			icon, _ := s.statusIcon(tt.run)
			if icon != tt.wantIcon {
				t.Errorf("statusIcon() icon = %q, want %q", icon, tt.wantIcon)
			}
		})
	}
}

func TestStatusIconSpinnerAdvances(t *testing.T) {
	s := &statusRenderer{spinnerIdx: 0}
	run := ghactions.WorkflowRun{Status: "in_progress"}

	icons := make([]string, 3)
	for i := range icons {
		s.spinnerIdx = i
		icon, _ := s.statusIcon(run)
		icons[i] = icon
	}

	if icons[0] == icons[1] || icons[1] == icons[2] {
		t.Errorf("spinner should advance each frame, got %v", icons)
	}
}

func TestDurationString(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{0, "0s"},
		{5 * time.Second, "5s"},
		{59 * time.Second, "59s"},
		{60 * time.Second, "1m"},
		{90 * time.Second, "1m30s"},
		{5*time.Minute + 32*time.Second, "5m32s"},
		{5*time.Minute + 33*time.Second, "5m33s"},
		{15 * time.Minute, "15m"},
		{10*time.Minute + 14*time.Second, "10m14s"},
		{43*time.Minute + 55*time.Second, "43m55s"},
		{time.Hour + 2*time.Minute, "1h2m"},
		{time.Hour + 2*time.Minute + 30*time.Second, "1h2m"},
		{2*time.Hour + 36*time.Minute, "2h36m"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := durationString(tt.d)
			if got != tt.want {
				t.Errorf("durationString(%v) = %q, want %q", tt.d, got, tt.want)
			}
		})
	}
}

func TestFormatEstimate(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{0, "0s"},
		{5 * time.Second, "5s"},
		{59 * time.Second, "59s"},
		{60 * time.Second, "1m"},
		{90 * time.Second, "1m30s"},
		// 1-10 minutes: rounds to nearest 5 seconds
		{5*time.Minute + 32*time.Second, "5m30s"},
		{5*time.Minute + 33*time.Second, "5m35s"},
		// 10-30 minutes: rounds to nearest 30 seconds
		{15 * time.Minute, "15m"},
		{10*time.Minute + 14*time.Second, "10m"},
		{10*time.Minute + 16*time.Second, "10m30s"},
		{15*time.Minute + 44*time.Second, "15m30s"},
		{15*time.Minute + 46*time.Second, "16m"},
		// 30-60 minutes: rounds to nearest minute
		{43*time.Minute + 55*time.Second, "44m"},
		{30*time.Minute + 29*time.Second, "30m"},
		// Over 1 hour: rounds to nearest 5 minutes
		{time.Hour + 2*time.Minute, "1h0m"},
		{time.Hour + 3*time.Minute, "1h5m"},
		{2*time.Hour + 36*time.Minute, "2h35m"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := formatEstimate(tt.d)
			if got != tt.want {
				t.Errorf("formatEstimate(%v) = %q, want %q", tt.d, got, tt.want)
			}
		})
	}
}

func TestRenderPlainNoOutput(t *testing.T) {
	// renderPlain should not print on the very first call (lastPrintedAt is
	// zero, but shouldPrint requires 10s to pass after the initial print)
	s := &statusRenderer{
		isTTY:     false,
		quiet:     false,
		startTime: time.Now(),
		// lastPrintedAt is zero-value, so shouldPrint checks against epoch
	}
	now := time.Now()
	runs := []ghactions.WorkflowRun{
		{
			Name:         "CI",
			Status:       "in_progress",
			RunStartedAt: timePtr(now.Add(-30 * time.Second)),
			UpdatedAt:    now,
		},
	}
	// This should not panic. The actual printing goes to os.Stdout which
	// is fine for tests - we're just checking it doesn't crash.
	s.renderPlain(runs)
}

func TestRenderTTYOutput(t *testing.T) {
	now := time.Now()
	s := &statusRenderer{
		isTTY:   true,
		noColor: true, // disable color for predictable output
		quiet:   false,
		estimates: map[int64]time.Duration{
			2: 20 * time.Minute,
		},
		estimatesDone: true,
	}

	runs := []ghactions.WorkflowRun{
		{
			Name:         "CIFuzz",
			Status:       "completed",
			Conclusion:   ptr("success"),
			WorkflowID:   1,
			RunStartedAt: timePtr(now.Add(-13 * time.Minute)),
			UpdatedAt:    now.Add(-58 * time.Second),
		},
		{
			Name:         "CI",
			Status:       "in_progress",
			WorkflowID:   2,
			RunStartedAt: timePtr(now.Add(-15 * time.Minute)),
			UpdatedAt:    now,
		},
		{
			Name:         "CI self-hosted",
			Status:       "completed",
			Conclusion:   ptr("skipped"),
			WorkflowID:   3,
			RunStartedAt: timePtr(now.Add(-1 * time.Second)),
			UpdatedAt:    now,
		},
	}

	// Just verify it doesn't panic and advances the spinner.
	s.render(runs)
	if s.spinnerIdx != 1 {
		t.Errorf("spinnerIdx = %d, want 1 after first render", s.spinnerIdx)
	}
	if s.lastLines != 3 {
		t.Errorf("lastLines = %d, want 3", s.lastLines)
	}
}

func TestClearStatusNoOp(t *testing.T) {
	// clearStatus should be a no-op when not TTY
	s := &statusRenderer{isTTY: false, lastLines: 5}
	s.clearStatus()
	if s.lastLines != 5 {
		t.Errorf("clearStatus modified lastLines for non-TTY")
	}

	// clearStatus should be a no-op when lastLines is 0
	s2 := &statusRenderer{isTTY: true, lastLines: 0}
	s2.clearStatus()
}

func TestNewStatusRendererQuiet(t *testing.T) {
	s := newStatusRenderer(true)
	if !s.quiet {
		t.Error("expected quiet=true")
	}
}
