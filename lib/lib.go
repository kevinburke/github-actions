// Package lib implements core functionality for the github-actions CLI tool.
package lib

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"
)

// Version is the github-actions version. Run "make release" to bump this number.
const Version = "0.2.1"

// WorkflowRunsResponse represents the response from listing workflow runs.
type WorkflowRunsResponse struct {
	TotalCount   int           `json:"total_count"`
	WorkflowRuns []WorkflowRun `json:"workflow_runs"`
}

// PullRequestRef represents a pull request reference in a workflow run.
type PullRequestRef struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
}

// WorkflowRun represents a GitHub Actions workflow run.
type WorkflowRun struct {
	ID           int64      `json:"id"`
	Name         string     `json:"name"`
	HeadBranch   string     `json:"head_branch"`
	HeadSha      string     `json:"head_sha"`
	Path         string     `json:"path"`
	RunNumber    int        `json:"run_number"`
	RunAttempt   int        `json:"run_attempt"`
	Event        string     `json:"event"`
	DisplayTitle string     `json:"display_title"`
	Status       string     `json:"status"`     // queued, in_progress, completed
	Conclusion   *string    `json:"conclusion"` // success, failure, cancelled, skipped, etc.
	WorkflowID   int64      `json:"workflow_id"`
	URL          string     `json:"url"`
	HTMLURL      string     `json:"html_url"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	RunStartedAt *time.Time `json:"run_started_at"`
	JobsURL      string     `json:"jobs_url"`

	PullRequests []PullRequestRef `json:"pull_requests"`
}

// JobsResponse represents the response from listing jobs for a workflow run.
type JobsResponse struct {
	TotalCount int   `json:"total_count"`
	Jobs       []Job `json:"jobs"`
}

// Job represents a job within a workflow run.
type Job struct {
	ID          int64      `json:"id"`
	RunID       int64      `json:"run_id"`
	Name        string     `json:"name"`
	Status      string     `json:"status"`
	Conclusion  *string    `json:"conclusion"`
	StartedAt   *time.Time `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at"`
	Steps       []Step     `json:"steps"`
	HTMLURL     string     `json:"html_url"`
}

// Failed returns true if the job failed.
func (j Job) Failed() bool {
	return j.Conclusion != nil && *j.Conclusion == "failure"
}

// Step represents a step within a job.
type Step struct {
	Name        string     `json:"name"`
	Status      string     `json:"status"`
	Conclusion  *string    `json:"conclusion"`
	Number      int        `json:"number"`
	StartedAt   *time.Time `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at"`
}

// Host represents a GitHub host configuration.
type Host struct {
	Token string `toml:"token"`
}

// FileConfig represents the configuration file structure.
type FileConfig struct {
	Default string          `toml:"default"`
	Hosts   map[string]Host `toml:"hosts"`
}

func checkFile(path string) bool {
	_, err := os.Stat(path)
	return !errors.Is(err, os.ErrNotExist)
}

// getCfgPath finds the config file path.
// Check for the following config paths:
// - $XDG_CONFIG_HOME/github-actions
// - $HOME/cfg/github-actions
// - $HOME/.github-actions
func getCfgPath() (string, error) {
	checkedLocations := make([]string, 0)

	xdgPath, ok := os.LookupEnv("XDG_CONFIG_HOME")
	filePath := filepath.Join(xdgPath, "github-actions")
	checkedLocations = append(checkedLocations, filePath)
	if ok && checkFile(filePath) {
		return filePath, nil
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("retrieving home directory info: %w", err)
	}

	cfgPath := filepath.Join(homeDir, "cfg", "github-actions")
	checkedLocations = append(checkedLocations, cfgPath)
	if checkFile(cfgPath) {
		return cfgPath, nil
	}

	localPath := filepath.Join(homeDir, ".github-actions")
	checkedLocations = append(checkedLocations, localPath)
	if checkFile(localPath) {
		return localPath, nil
	}

	return "", nil // No config file found, but that's OK - we'll use env vars
}

// GetToken retrieves a GitHub token for the given host.
// It checks in order:
// 1. GH_TOKEN environment variable
// 2. GITHUB_TOKEN environment variable
// 3. Config file
func GetToken(ctx context.Context, host string) (string, error) {
	// Check environment variables first
	if token := os.Getenv("GH_TOKEN"); token != "" {
		return token, nil
	}
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		return token, nil
	}

	// Try config file
	cfgPath, err := getCfgPath()
	if err != nil {
		return "", err
	}
	if cfgPath == "" {
		return "", errors.New(tokenNotFoundMessage(host))
	}

	f, err := os.Open(cfgPath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	deadline, deadlineOk := ctx.Deadline()
	if deadlineOk {
		f.SetDeadline(deadline)
	}

	var cfg FileConfig
	if _, err := toml.NewDecoder(bufio.NewReader(f)).Decode(&cfg); err != nil {
		return "", err
	}

	// Try exact host match
	if h, ok := cfg.Hosts[host]; ok && h.Token != "" {
		return h.Token, nil
	}

	// Try default host
	if cfg.Default != "" {
		if h, ok := cfg.Hosts[cfg.Default]; ok && h.Token != "" {
			return h.Token, nil
		}
	}

	// Try github.com as fallback
	if h, ok := cfg.Hosts["github.com"]; ok && h.Token != "" {
		return h.Token, nil
	}

	return "", errors.New(tokenNotFoundMessage(host))
}

func tokenNotFoundMessage(host string) string {
	return fmt.Sprintf(`Couldn't find a GitHub token for host %q.

Set the GH_TOKEN or GITHUB_TOKEN environment variable, or add a configuration file:

$XDG_CONFIG_HOME/github-actions or $HOME/cfg/github-actions or $HOME/.github-actions

default = "github.com"

[hosts]

[hosts."github.com"]
token = "ghp_xxxx"

Go to https://github.com/settings/tokens to create a token.
`, host)
}

// IsCompleted returns true if the workflow run has completed.
func (r *WorkflowRun) IsCompleted() bool {
	return r.Status == "completed"
}

// IsSuccess returns true if the workflow run completed successfully.
func (r *WorkflowRun) IsSuccess() bool {
	return r.IsCompleted() && r.Conclusion != nil && *r.Conclusion == "success"
}

// IsFailed returns true if the workflow run failed.
func (r *WorkflowRun) IsFailed() bool {
	if !r.IsCompleted() || r.Conclusion == nil {
		return false
	}
	c := *r.Conclusion
	return c == "failure" || c == "cancelled" || c == "timed_out"
}

// Duration returns the duration of the workflow run.
func (r *WorkflowRun) Duration() time.Duration {
	if r.RunStartedAt == nil {
		return 0
	}
	var end time.Time
	if r.IsCompleted() {
		end = r.UpdatedAt
	} else {
		end = time.Now()
	}
	return end.Sub(*r.RunStartedAt).Round(time.Second)
}
