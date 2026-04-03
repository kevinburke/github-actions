package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	ghactions "github.com/kevinburke/github-actions/lib"
)

var spinnerFrames = [...]rune{'⠋', '⠙', '⠹', '⠸', '⠼', '⠴', '⠦', '⠧', '⠇', '⠏'}

// ANSI escape code reference (all are CSI sequences: \033[ + parameters).
// See https://en.wikipedia.org/wiki/ANSI_escape_code#CSI_(Control_Sequence_Introducer)_sequences
//
//   \033[<N>A    Move cursor up N lines.
//   \033[2K      Erase the entire current line (cursor position unchanged).
//   \033[0m      Reset all text attributes (color, bold, etc.) to default.
//   \033[31m     Set text color to red.
//   \033[32m     Set text color to green.
//   \033[33m     Set text color to yellow.
//   \033[90m     Set text color to bright black (dim/gray).

// statusRenderer handles TTY-aware status display for workflow runs.
type statusRenderer struct {
	isTTY   bool
	noColor bool
	quiet   bool

	lastLines  int // number of lines rendered last time (for cursor-up)
	spinnerIdx int

	// non-TTY throttling
	lastPrintedAt time.Time
	startTime     time.Time

	// duration estimates: workflowID -> median duration
	estimates     map[int64]time.Duration
	estimatesDone bool
}

func newStatusRenderer(quiet bool) *statusRenderer {
	isTTY := ghactions.IsATTY()
	return &statusRenderer{
		isTTY:     isTTY,
		noColor:   os.Getenv("NO_COLOR") != "",
		quiet:     quiet,
		startTime: time.Now(),
	}
}

// fetchEstimates fetches historical run durations for each distinct workflow
// and computes a median. Called once on the first poll that returns runs.
func (s *statusRenderer) fetchEstimates(ctx context.Context, repo *ghactions.RepoService, runs []ghactions.WorkflowRun) {
	if s.estimatesDone {
		return
	}
	s.estimatesDone = true
	s.estimates = make(map[int64]time.Duration)

	seen := make(map[int64]bool)
	for _, run := range runs {
		if seen[run.WorkflowID] {
			continue
		}
		seen[run.WorkflowID] = true

		params := url.Values{
			"status":     {"completed"},
			"conclusion": {"success"},
			"per_page":   {"10"},
		}
		resp, err := repo.ListWorkflowRunsByWorkflow(ctx, run.WorkflowID, params)
		if err != nil {
			slog.Debug("could not fetch historical runs", "workflow_id", run.WorkflowID, "error", err)
			continue
		}
		durations := make([]time.Duration, 0, len(resp.WorkflowRuns))
		for _, r := range resp.WorkflowRuns {
			d := r.Duration()
			conclusion := "<nil>"
			if r.Conclusion != nil {
				conclusion = *r.Conclusion
			}
			slog.Debug("historical run",
				"workflow", run.Name,
				"workflow_id", run.WorkflowID,
				"run_id", r.ID,
				"conclusion", conclusion,
				"duration", d,
				"run_started_at", r.RunStartedAt,
				"updated_at", r.UpdatedAt,
			)
			if d > 0 {
				durations = append(durations, d)
			}
		}
		if len(durations) > 0 {
			slices.Sort(durations)
			median := durations[len(durations)/2]
			slog.Debug("estimated duration",
				"workflow", run.Name,
				"workflow_id", run.WorkflowID,
				"median", median,
				"sample_size", len(durations),
				"all_durations", durations,
			)
			s.estimates[run.WorkflowID] = median
		}
	}
}

// render prints the current status of all workflow runs.
// Runs are sorted by workflow ID for stable display order across polls.
func (s *statusRenderer) render(runs []ghactions.WorkflowRun) {
	if s.quiet {
		return
	}
	sort.Slice(runs, func(i, j int) bool {
		return runs[i].WorkflowID < runs[j].WorkflowID
	})
	if s.isTTY {
		s.renderTTY(runs)
	} else {
		s.renderPlain(runs)
	}
}

func (s *statusRenderer) renderTTY(runs []ghactions.WorkflowRun) {
	var buf strings.Builder

	// Move cursor up N lines to overwrite the previous status block.
	if s.lastLines > 0 {
		fmt.Fprintf(&buf, "\033[%dA", s.lastLines) // cursor up
	}

	// First pass: compute column widths for alignment.
	// Name and identifier are separate columns so that identifiers like
	// "[run 67]" right-align even when workflow names differ in length.
	// Durations are split into major (hours/minutes) and minor (seconds)
	// parts so that the minute boundary aligns across rows.
	maxName := 0
	maxId := 0
	maxDurMajor := 0
	maxDurMinor := 0
	maxEst := 0
	for _, run := range runs {
		if len(run.Name) > maxName {
			maxName = len(run.Name)
		}
		if id := workflowRunIdentifier(run); id != "" {
			idStr := fmt.Sprintf("[%s]", id)
			if len(idStr) > maxId {
				maxId = len(idStr)
			}
		}
		major, minor := durationParts(run.Duration())
		if len(major) > maxDurMajor {
			maxDurMajor = len(major)
		}
		if len(minor) > maxDurMinor {
			maxDurMinor = len(minor)
		}
		if run.Status == "in_progress" || run.Status == "queued" {
			if est, ok := s.estimates[run.WorkflowID]; ok {
				estStr := fmt.Sprintf("(~%s est)", formatEstimate(est))
				if len(estStr) > maxEst {
					maxEst = len(estStr)
				}
			}
		}
	}

	lines := 0
	for _, run := range runs {
		icon, color := s.statusIcon(run)

		// Format duration with minute-boundary alignment: right-align the
		// major part (minutes/hours) and left-align the minor part (seconds).
		// When no durations have a major part, fall back to simple right-align.
		major, minor := durationParts(run.Duration())
		var durStr string
		if maxDurMajor > 0 {
			durStr = fmt.Sprintf("%*s%-*s", maxDurMajor, major, maxDurMinor, minor)
		} else {
			durStr = fmt.Sprintf("%*s", maxDurMinor, minor)
		}

		var idStr string
		if id := workflowRunIdentifier(run); id != "" {
			idStr = fmt.Sprintf("[%s]", id)
		}

		var estimate string
		if run.Status == "in_progress" || run.Status == "queued" {
			if est, ok := s.estimates[run.WorkflowID]; ok {
				estimate = fmt.Sprintf("(~%s est)", formatEstimate(est))
			}
		}

		statusText := run.Status
		if run.IsCompleted() && run.Conclusion != nil {
			statusText = *run.Conclusion
		}

		// Columns: icon | name (left) | id (right) | status (left) | dur (aligned) | est (right)
		if color != "" && !s.noColor {
			// Apply color to the icon and status text, with \033[0m (reset)
			// after each colored span to return to default terminal colors.
			fmt.Fprintf(&buf, "\033[2K  %s%s\033[0m %-*s %*s  %s%-12s\033[0m %s  %*s\n",
				color, icon, maxName, run.Name, maxId, idStr, color, statusText, durStr, maxEst, estimate)
		} else {
			fmt.Fprintf(&buf, "\033[2K  %s %-*s %*s  %-12s %s  %*s\n",
				icon, maxName, run.Name, maxId, idStr, statusText, durStr, maxEst, estimate)
		}
		lines++
	}

	s.lastLines = lines
	s.spinnerIdx++
	os.Stdout.WriteString(buf.String())
}

func (s *statusRenderer) renderPlain(runs []ghactions.WorkflowRun) {
	elapsed := time.Since(s.startTime).Round(time.Second)
	if !shouldPrint(s.lastPrintedAt, elapsed) {
		return
	}
	for _, run := range runs {
		status := run.Status
		if run.IsCompleted() && run.Conclusion != nil {
			status = *run.Conclusion
		}
		fmt.Printf("Workflow %q %s (%s elapsed)\n", workflowRunDisplayName(run), status, run.Duration().String())
	}
	s.lastPrintedAt = time.Now()
}

// clearStatus erases the in-place status block before printing final output.
// It moves the cursor up, clears each line, then moves back up so the final
// output prints where the status block was.
func (s *statusRenderer) clearStatus() {
	if !s.isTTY || s.lastLines == 0 {
		return
	}
	var buf strings.Builder
	fmt.Fprintf(&buf, "\033[%dA", s.lastLines) // cursor up to top of status block
	for range s.lastLines {
		fmt.Fprintf(&buf, "\033[2K\n") // erase each line
	}
	fmt.Fprintf(&buf, "\033[%dA", s.lastLines) // cursor back up to start
	os.Stdout.WriteString(buf.String())
	s.lastLines = 0
}

func (s *statusRenderer) statusIcon(run ghactions.WorkflowRun) (icon string, color string) {
	if run.IsCompleted() && run.Conclusion != nil {
		switch *run.Conclusion {
		case "success":
			return "✓", "\033[32m" // green
		case "failure", "cancelled", "timed_out":
			return "✗", "\033[31m" // red
		case "skipped":
			return "-", "\033[90m" // dim
		default:
			return "?", "\033[90m" // dim
		}
	}
	switch run.Status {
	case "queued", "waiting", "pending":
		return "□", "\033[33m" // yellow
	case "in_progress":
		frame := spinnerFrames[s.spinnerIdx%len(spinnerFrames)]
		return string(frame), "\033[33m" // yellow
	default:
		return "?", ""
	}
}

// durationString formats a duration in a compact human-readable form.
// Rounds to the nearest second, but drops seconds for durations over an hour.
func durationString(d time.Duration) string {
	d = d.Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		m := int(d.Minutes())
		s := int(d.Seconds()) % 60
		if s == 0 {
			return fmt.Sprintf("%dm", m)
		}
		return fmt.Sprintf("%dm%ds", m, s)
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	return fmt.Sprintf("%dh%dm", h, m)
}

// durationParts splits a formatted duration into a "major" part (hours and/or
// minutes) and a "minor" part (seconds), so that columns of durations can
// align on the minute boundary.
//
//	12m2s → ("12m", "2s")
//	15m   → ("15m", "")
//	27s   → ("",    "27s")
//	1h2m  → ("1h2m", "")
func durationParts(d time.Duration) (major, minor string) {
	d = d.Round(time.Second)
	if d < time.Minute {
		return "", fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		m := int(d.Minutes())
		s := int(d.Seconds()) % 60
		if s == 0 {
			return fmt.Sprintf("%dm", m), ""
		}
		return fmt.Sprintf("%dm", m), fmt.Sprintf("%ds", s)
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	return fmt.Sprintf("%dh%dm", h, m), ""
}

// formatEstimate formats an estimated duration with coarser rounding,
// since estimates are inherently imprecise.
func formatEstimate(d time.Duration) string {
	switch {
	case d >= time.Hour:
		d = d.Round(5 * time.Minute)
	case d >= 30*time.Minute:
		d = d.Round(time.Minute)
	case d >= 10*time.Minute:
		d = d.Round(30 * time.Second)
	case d >= time.Minute:
		d = d.Round(5 * time.Second)
	default:
		d = d.Round(time.Second)
	}
	return durationString(d)
}
