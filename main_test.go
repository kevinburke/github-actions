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

func TestPluralize(t *testing.T) {
	tests := []struct {
		name     string
		count    int
		singular string
		want     string
	}{
		{name: "zero", count: 0, singular: "workflow run", want: "workflow runs"},
		{name: "one", count: 1, singular: "workflow run", want: "workflow run"},
		{name: "two", count: 2, singular: "workflow run", want: "workflow runs"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pluralize(tt.count, tt.singular); got != tt.want {
				t.Fatalf("pluralize(%d, %q) = %q, want %q", tt.count, tt.singular, got, tt.want)
			}
		})
	}
}

func TestWorkflowRunIdentifier(t *testing.T) {
	tests := []struct {
		name string
		run  ghactions.WorkflowRun
		want string
	}{
		{
			name: "run_number_only",
			run: ghactions.WorkflowRun{
				RunNumber: 49,
				ID:        123456789,
			},
			want: "run 49",
		},
		{
			name: "empty",
			run:  ghactions.WorkflowRun{},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := workflowRunIdentifier(tt.run); got != tt.want {
				t.Fatalf("workflowRunIdentifier(%+v) = %q, want %q", tt.run, got, tt.want)
			}
		})
	}
}

func TestWorkflowRunDisplayName(t *testing.T) {
	run := ghactions.WorkflowRun{
		Name:      "CI",
		RunNumber: 49,
		ID:        123456789,
	}
	if got := workflowRunDisplayName(run); got != "CI [run 49]" {
		t.Fatalf("workflowRunDisplayName() = %q", got)
	}

	run = ghactions.WorkflowRun{Name: "CI"}
	if got := workflowRunDisplayName(run); got != "CI" {
		t.Fatalf("workflowRunDisplayName() without identifier = %q", got)
	}
}

func TestHasWorkflowRunsForCommit(t *testing.T) {
	tip := "tipsha"
	runs := []ghactions.WorkflowRun{
		{Name: "CI", HeadSha: "oldsha"},
		{Name: "CI", HeadSha: tip},
	}
	if !hasWorkflowRunsForCommit(tip, runs) {
		t.Fatal("hasWorkflowRunsForCommit() = false, want true")
	}
	if hasWorkflowRunsForCommit("missing", runs) {
		t.Fatal("hasWorkflowRunsForCommit() = true for missing sha, want false")
	}
}

func TestCancelableWorkflowRuns(t *testing.T) {
	tip := "tipsha"
	runs := []ghactions.WorkflowRun{
		{ID: 1, Status: "completed", HeadSha: "old-1"},
		{ID: 2, Status: "queued", HeadSha: "old-2"},
		{ID: 3, Status: "in_progress", HeadSha: tip},
		{ID: 4, Status: "in_progress", HeadSha: "old-3"},
	}

	got := cancelableWorkflowRuns(tip, runs)
	if len(got) != 2 {
		t.Fatalf("len(cancelableWorkflowRuns()) = %d, want 2", len(got))
	}
	if got[0].ID != 2 || got[1].ID != 4 {
		t.Fatalf("cancelableWorkflowRuns() = %#v, want runs 2 and 4", got)
	}
}

func TestActiveWorkflows(t *testing.T) {
	workflows := []ghactions.Workflow{
		{Name: "CI", State: "active"},
		{Name: "Docs", State: "disabled_manually"},
		{Name: "Release", State: "active"},
	}

	got := activeWorkflows(workflows)
	if len(got) != 2 {
		t.Fatalf("len(activeWorkflows()) = %d, want 2", len(got))
	}
	if got[0].Name != "CI" || got[1].Name != "Release" {
		t.Fatalf("activeWorkflows() = %#v, want CI and Release", got)
	}
}

func TestWorkflowConfigurationError(t *testing.T) {
	tests := []struct {
		name      string
		workflows *ghactions.WorkflowsResponse
		want      string
	}{
		{
			name:      "none",
			workflows: &ghactions.WorkflowsResponse{},
			want:      "no workflow files found in owner/repo",
		},
		{
			name: "disabled",
			workflows: &ghactions.WorkflowsResponse{
				TotalCount: 2,
				Workflows: []ghactions.Workflow{
					{Name: "CI", State: "disabled_manually"},
					{Name: "Docs", State: "disabled_manually"},
				},
			},
			want: "all 2 workflows in owner/repo are disabled",
		},
		{
			name: "active",
			workflows: &ghactions.WorkflowsResponse{
				TotalCount: 1,
				Workflows: []ghactions.Workflow{
					{Name: "CI", State: "active"},
				},
			},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := workflowConfigurationError("owner", "repo", tt.workflows)
			if tt.want == "" {
				if err != nil {
					t.Fatalf("workflowConfigurationError() returned %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("workflowConfigurationError() = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestConfiguredWorkflowLinks(t *testing.T) {
	remote := &RemoteURL{
		Host:     "github.com",
		Path:     "owner",
		RepoName: "repo",
	}
	workflows := []ghactions.Workflow{
		{
			Name:    "CI",
			State:   "active",
			HTMLURL: "https://github.com/owner/repo/actions/workflows/ci.yml",
		},
		{
			Name:  "Docs",
			State: "active",
			Path:  ".github/workflows/docs.yml",
		},
		{
			Name:  "Docs duplicate",
			State: "active",
			Path:  ".github/workflows/docs.yml",
		},
		{
			Name:  "Disabled",
			State: "disabled_manually",
			Path:  ".github/workflows/disabled.yml",
		},
	}

	got := configuredWorkflowLinks(remote, workflows)
	want := []string{
		"https://github.com/owner/repo/actions/workflows/ci.yml",
		"https://github.com/owner/repo/actions/workflows/docs.yml",
	}
	if len(got) != len(want) {
		t.Fatalf("len(configuredWorkflowLinks()) = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("configuredWorkflowLinks()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestHasWorkflowsHelpDocumentsExitCodes(t *testing.T) {
	for _, want := range []string{
		"exit 0 if any\nactive workflows are configured",
		"Exits 1 if none are configured",
		"Exits 2 if an\nactual error occurs while checking",
	} {
		if !strings.Contains(hasWorkflowsHelp, want) {
			t.Fatalf("hasWorkflowsHelp = %q, want substring %q", hasWorkflowsHelp, want)
		}
	}
}
