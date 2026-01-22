package lib

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/kevinburke/rest/restclient"
	"golang.org/x/term"
)

const userAgent = "github-actions-go/" + Version

// Error represents a GitHub API error.
type Error struct {
	StatusCode int
	Message    string
}

func (e *Error) Error() string {
	return e.Message
}

type githubErrorResponse struct {
	Message string `json:"message"`
}

// Client is a GitHub API client.
type Client struct {
	*restclient.Client
	host string
}

// NewClient creates a new GitHub API client.
func NewClient(token string, host string) *Client {
	if host == "" {
		host = "github.com"
	}
	var apiHost string
	if host == "github.com" {
		apiHost = "https://api.github.com"
	} else {
		apiHost = "https://" + host + "/api/v3"
	}

	rc := restclient.NewBearerClient(token, apiHost)
	rc.ErrorParser = func(r *http.Response) error {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			return fmt.Errorf("received HTTP error %d from GitHub and could not read the response: %v", r.StatusCode, err)
		}
		resp := new(githubErrorResponse)
		if err := json.Unmarshal(data, &resp); err != nil {
			return fmt.Errorf("could not decode %d error response as a GitHub error: %w", r.StatusCode, err)
		}
		return &Error{Message: resp.Message, StatusCode: r.StatusCode}
	}

	return &Client{
		Client: rc,
		host:   host,
	}
}

// RepoService provides access to repository-related API endpoints.
type RepoService struct {
	client *Client
	owner  string
	repo   string
}

// Repo returns a RepoService for the given owner and repo.
func (c *Client) Repo(owner, repo string) *RepoService {
	return &RepoService{
		client: c,
		owner:  owner,
		repo:   repo,
	}
}

// ListWorkflowRuns lists workflow runs for the repository.
func (r *RepoService) ListWorkflowRuns(ctx context.Context, params url.Values) (*WorkflowRunsResponse, error) {
	path := fmt.Sprintf("/repos/%s/%s/actions/runs", r.owner, r.repo)
	if params != nil {
		path += "?" + params.Encode()
	}

	req, err := r.client.NewRequestWithContext(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)

	var resp WorkflowRunsResponse
	if err := r.client.Do(req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetWorkflowRun gets a specific workflow run.
func (r *RepoService) GetWorkflowRun(ctx context.Context, runID int64) (*WorkflowRun, error) {
	path := fmt.Sprintf("/repos/%s/%s/actions/runs/%d", r.owner, r.repo, runID)

	req, err := r.client.NewRequestWithContext(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)

	var resp WorkflowRun
	if err := r.client.Do(req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ListJobs lists jobs for a workflow run.
func (r *RepoService) ListJobs(ctx context.Context, runID int64, params url.Values) (*JobsResponse, error) {
	path := fmt.Sprintf("/repos/%s/%s/actions/runs/%d/jobs", r.owner, r.repo, runID)
	if params != nil {
		path += "?" + params.Encode()
	}

	req, err := r.client.NewRequestWithContext(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)

	var resp JobsResponse
	if err := r.client.Do(req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetJobLogs downloads the logs for a job.
func (r *RepoService) GetJobLogs(ctx context.Context, jobID int64) ([]byte, error) {
	path := fmt.Sprintf("/repos/%s/%s/actions/jobs/%d/logs", r.owner, r.repo, jobID)

	req, err := r.client.NewRequestWithContext(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := r.client.Client.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// GitHub returns a redirect to a download URL
	if resp.StatusCode == http.StatusFound {
		location := resp.Header.Get("Location")
		if location == "" {
			return nil, fmt.Errorf("redirect without location header")
		}
		req2, err := http.NewRequestWithContext(ctx, "GET", location, nil)
		if err != nil {
			return nil, err
		}
		resp2, err := http.DefaultClient.Do(req2)
		if err != nil {
			return nil, err
		}
		defer resp2.Body.Close()
		return readPossiblyCompressed(resp2)
	}

	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	return readPossiblyCompressed(resp)
}

func readPossiblyCompressed(resp *http.Response) ([]byte, error) {
	var reader io.Reader = resp.Body
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gr, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, err
		}
		defer gr.Close()
		reader = gr
	}
	return io.ReadAll(reader)
}

func isatty() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}

func buildJobsSummary(jobs []Job) ([]byte, *Job) {
	var buf bytes.Buffer
	buf.WriteByte('\n')
	writer := tabwriter.NewWriter(&buf, 0, 0, 1, ' ', 0)

	var failedJob *Job
	for i := range jobs {
		job := &jobs[i]
		var duration time.Duration
		if job.StartedAt != nil && job.CompletedAt != nil {
			duration = job.CompletedAt.Sub(*job.StartedAt)
		}
		if duration > time.Minute {
			duration = duration.Round(time.Second)
		} else {
			duration = duration.Round(10 * time.Millisecond)
		}

		durString := duration.String()
		if job.Failed() && isatty() {
			durString = fmt.Sprintf("\033[38;05;160m%-8s\033[0m", duration.String())
		}

		if job.Failed() && failedJob == nil {
			failedJob = job
		}

		fmt.Fprintf(writer, "%s\t%s\n", job.Name, durString)
	}
	writer.Flush()

	return buf.Bytes(), failedJob
}

// BuildJobsSummary generates a summary of a workflow run's jobs.
func (c *Client) BuildJobsSummary(ctx context.Context, owner, repo string, run WorkflowRun) []byte {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	jobs, err := c.Repo(owner, repo).ListJobs(ctx, run.ID, url.Values{"per_page": []string{"100"}})
	if err != nil {
		var buf bytes.Buffer
		buf.WriteByte('\n')
		fmt.Fprintf(&buf, "Error fetching jobs: %v\n", err)
		return buf.Bytes()
	}

	summary, _ := buildJobsSummary(jobs.Jobs)
	return summary
}

// BuildSummary generates a summary of a workflow run's jobs.
func (c *Client) BuildSummary(ctx context.Context, owner, repo string, run WorkflowRun, numOutputLines int) []byte {
	var buf bytes.Buffer
	buf.WriteByte('\n')

	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	jobs, err := c.Repo(owner, repo).ListJobs(ctx, run.ID, url.Values{"per_page": []string{"100"}})
	if err != nil {
		fmt.Fprintf(&buf, "Error fetching jobs: %v\n", err)
		return buf.Bytes()
	}

	summary, failedJob := buildJobsSummary(jobs.Jobs)

	var failure []byte
	if failedJob != nil {
		logs, err := c.Repo(owner, repo).GetJobLogs(ctx, failedJob.ID)
		if err == nil {
			failure = findBuildFailure(logs, numOutputLines)
		}
	}

	linelen := bytes.IndexByte(summary[1:], '\n')
	var buf2 bytes.Buffer
	buf2.WriteByte('\n')
	if linelen > 0 {
		buf2.Write(bytes.Repeat([]byte{'='}, linelen))
	}
	if len(failure) > 0 {
		fmt.Fprintf(&buf2, "\nLast %d lines of failed build output:\n\n", numOutputLines)
		buf2.Write(failure)
	}
	return append(summary, buf2.Bytes()...)
}

// findBuildFailure extracts the last N lines from the log.
func findBuildFailure(log []byte, numOutputLines int) []byte {
	if len(log) == 0 {
		return log
	}

	// Count newlines from the end
	newlineIdx := len(log)
	for count := 0; count < numOutputLines; count++ {
		prevNewlineIdx := bytes.LastIndexByte(log[:newlineIdx-1], '\n')
		if prevNewlineIdx == -1 {
			return log
		}
		newlineIdx = prevNewlineIdx + 1
	}
	return log[newlineIdx:]
}

// FindWorkflowRunsForCommit finds workflow runs matching a specific commit SHA.
func (r *RepoService) FindWorkflowRunsForCommit(ctx context.Context, sha string) ([]WorkflowRun, error) {
	params := url.Values{
		"head_sha": []string{sha},
		"per_page": []string{strconv.Itoa(20)},
	}
	resp, err := r.ListWorkflowRuns(ctx, params)
	if err != nil {
		return nil, err
	}
	return resp.WorkflowRuns, nil
}
