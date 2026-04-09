package lib

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"sync/atomic"
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

// RateLimitError is returned when GitHub rejects a request because the API rate
// limit has been exhausted. Reset is the time at which the rate limit window
// resets and a new request can be made.
type RateLimitError struct {
	StatusCode int
	Message    string
	Reset      time.Time
	Limit      int
	Resource   string
}

func (e *RateLimitError) Error() string {
	if !e.Reset.IsZero() {
		return fmt.Sprintf("github API rate limit exceeded (resets in %s): %s", time.Until(e.Reset).Round(time.Second), e.Message)
	}
	return "github API rate limit exceeded: " + e.Message
}

// IsRateLimitError reports whether err is a *RateLimitError.
func IsRateLimitError(err error) (*RateLimitError, bool) {
	var rle *RateLimitError
	if errors.As(err, &rle) {
		return rle, true
	}
	return nil, false
}

// RateLimit captures the most recent rate limit headers reported by GitHub.
type RateLimit struct {
	Limit      int
	Remaining  int
	Reset      time.Time
	Resource   string
	ObservedAt time.Time
}

type githubErrorResponse struct {
	Message string `json:"message"`
}

// Client is a GitHub API client.
type Client struct {
	*restclient.Client
	host    string
	rateLim atomic.Pointer[RateLimit]
}

// RateLimit returns the most recently observed rate limit, or nil if no
// response with rate limit headers has been seen yet.
func (c *Client) RateLimit() *RateLimit {
	return c.rateLim.Load()
}

// rateLimitTransport records X-RateLimit-* headers from each response onto the
// owning Client.
type rateLimitTransport struct {
	base   http.RoundTripper
	client *Client
}

func (t *rateLimitTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if resp != nil {
		if rl := parseRateLimit(resp.Header); rl != nil {
			t.client.rateLim.Store(rl)
		}
	}
	return resp, err
}

func parseRateLimit(h http.Header) *RateLimit {
	rem := h.Get("X-RateLimit-Remaining")
	if rem == "" {
		return nil
	}
	remaining, err := strconv.Atoi(rem)
	if err != nil {
		return nil
	}
	limit, _ := strconv.Atoi(h.Get("X-RateLimit-Limit"))
	resetUnix, _ := strconv.ParseInt(h.Get("X-RateLimit-Reset"), 10, 64)
	rl := &RateLimit{
		Limit:      limit,
		Remaining:  remaining,
		Resource:   h.Get("X-RateLimit-Resource"),
		ObservedAt: time.Now(),
	}
	if resetUnix > 0 {
		rl.Reset = time.Unix(resetUnix, 0)
	}
	return rl
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
	c := &Client{
		Client: rc,
		host:   host,
	}
	rc.Client.Transport = &rateLimitTransport{
		base: &retryTransport{
			base:       rc.Client.Transport,
			maxRetries: 3,
		},
		client: c,
	}
	rc.ErrorParser = func(r *http.Response) error {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			return fmt.Errorf("received HTTP error %d from GitHub and could not read the response: %v", r.StatusCode, err)
		}
		resp := new(githubErrorResponse)
		if err := json.Unmarshal(data, &resp); err != nil {
			return fmt.Errorf("could not decode %d error response as a GitHub error: %w", r.StatusCode, err)
		}
		// 403 or 429 with X-RateLimit-Remaining: 0 means we hit the
		// primary rate limit. Surface a typed error so callers can
		// sleep until reset rather than failing immediately.
		if r.StatusCode == http.StatusForbidden || r.StatusCode == http.StatusTooManyRequests {
			if rl := parseRateLimit(r.Header); rl != nil && rl.Remaining == 0 {
				return &RateLimitError{
					StatusCode: r.StatusCode,
					Message:    resp.Message,
					Reset:      rl.Reset,
					Limit:      rl.Limit,
					Resource:   rl.Resource,
				}
			}
		}
		return &Error{Message: resp.Message, StatusCode: r.StatusCode}
	}

	return c
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

// newRequest creates an HTTP request, prepending our product token to the
// User-Agent that the restclient already sets (like browsers do).
func (r *RepoService) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	req, err := r.client.NewRequestWithContext(ctx, method, path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent+" "+req.Header.Get("User-Agent"))
	return req, nil
}

// ListWorkflowRuns lists workflow runs for the repository.
// https://docs.github.com/en/rest/actions/workflow-runs#list-workflow-runs-for-a-repository
func (r *RepoService) ListWorkflowRuns(ctx context.Context, params url.Values) (*WorkflowRunsResponse, error) {
	path := fmt.Sprintf("/repos/%s/%s/actions/runs", r.owner, r.repo)
	if params != nil {
		path += "?" + params.Encode()
	}

	req, err := r.newRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var resp WorkflowRunsResponse
	if err := r.client.Do(req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ListWorkflows lists workflows for the repository.
// https://docs.github.com/en/rest/actions/workflows#list-repository-workflows
func (r *RepoService) ListWorkflows(ctx context.Context) (*WorkflowsResponse, error) {
	path := fmt.Sprintf("/repos/%s/%s/actions/workflows", r.owner, r.repo)

	req, err := r.newRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var resp WorkflowsResponse
	if err := r.client.Do(req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetWorkflowRun gets a specific workflow run.
// https://docs.github.com/en/rest/actions/workflow-runs#get-a-workflow-run
func (r *RepoService) GetWorkflowRun(ctx context.Context, runID int64) (*WorkflowRun, error) {
	path := fmt.Sprintf("/repos/%s/%s/actions/runs/%d", r.owner, r.repo, runID)

	req, err := r.newRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var resp WorkflowRun
	if err := r.client.Do(req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ListJobs lists jobs for a workflow run. Results are paginated; set "page"
// and "per_page" in params to control pagination.
// https://docs.github.com/en/rest/actions/workflow-jobs#list-jobs-for-a-workflow-run
func (r *RepoService) ListJobs(ctx context.Context, runID int64, params url.Values) (*JobsResponse, error) {
	path := fmt.Sprintf("/repos/%s/%s/actions/runs/%d/jobs", r.owner, r.repo, runID)
	if params != nil {
		path += "?" + params.Encode()
	}

	req, err := r.newRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var resp JobsResponse
	if err := r.client.Do(req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// FindFailedJob checks a workflow run's jobs (across all pages) and returns
// the first failed job, or nil if no jobs have failed yet.
func (r *RepoService) FindFailedJob(ctx context.Context, runID int64) (*Job, error) {
	page := 1
	for {
		params := url.Values{
			"per_page": []string{"100"},
			"page":     []string{strconv.Itoa(page)},
		}
		resp, err := r.ListJobs(ctx, runID, params)
		if err != nil {
			return nil, err
		}
		for i := range resp.Jobs {
			if resp.Jobs[i].Failed() {
				return &resp.Jobs[i], nil
			}
		}
		if len(resp.Jobs) == 0 || page*100 >= resp.TotalCount {
			break
		}
		page++
	}
	return nil, nil
}

// GetJobLogs downloads the logs for a job.
// https://docs.github.com/en/rest/actions/workflow-jobs#download-job-logs-for-a-workflow-run
func (r *RepoService) GetJobLogs(ctx context.Context, jobID int64) ([]byte, error) {
	path := fmt.Sprintf("/repos/%s/%s/actions/jobs/%d/logs", r.owner, r.repo, jobID)

	req, err := r.newRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
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

// IsATTY returns true if stdout is connected to a terminal.
func IsATTY() bool {
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
		if job.Failed() && IsATTY() && os.Getenv("NO_COLOR") == "" {
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

func failedJobURL(job *Job) string {
	if job == nil || job.HTMLURL == "" {
		return ""
	}
	for _, step := range job.Steps {
		if step.Conclusion != nil && *step.Conclusion == "failure" && step.Number > 0 {
			return fmt.Sprintf("%s#step:%d:1", job.HTMLURL, step.Number)
		}
	}
	return job.HTMLURL
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
	if url := failedJobURL(failedJob); url != "" {
		fmt.Fprintf(&buf, "Failed job URL:\n%s\n", url)
	}

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
	for range numOutputLines {
		prevNewlineIdx := bytes.LastIndexByte(log[:newlineIdx-1], '\n')
		if prevNewlineIdx == -1 {
			return log
		}
		newlineIdx = prevNewlineIdx + 1
	}
	return log[newlineIdx:]
}

// ListWorkflowRunsByWorkflow lists workflow runs for a specific workflow.
// https://docs.github.com/en/rest/actions/workflow-runs#list-workflow-runs-for-a-workflow
func (r *RepoService) ListWorkflowRunsByWorkflow(ctx context.Context, workflowID int64, params url.Values) (*WorkflowRunsResponse, error) {
	path := fmt.Sprintf("/repos/%s/%s/actions/workflows/%d/runs", r.owner, r.repo, workflowID)
	if params != nil {
		path += "?" + params.Encode()
	}

	req, err := r.newRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var resp WorkflowRunsResponse
	if err := r.client.Do(req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
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

// FindWorkflowRunsForBranch finds workflow runs for a branch name.
func (r *RepoService) FindWorkflowRunsForBranch(ctx context.Context, branch string) ([]WorkflowRun, error) {
	params := url.Values{
		"branch":   []string{branch},
		"per_page": []string{strconv.Itoa(100)},
	}
	resp, err := r.ListWorkflowRuns(ctx, params)
	if err != nil {
		return nil, err
	}
	return resp.WorkflowRuns, nil
}

// CancelWorkflowRun cancels a workflow run.
// https://docs.github.com/en/rest/actions/workflow-runs#cancel-a-workflow-run
func (r *RepoService) CancelWorkflowRun(ctx context.Context, runID int64) error {
	path := fmt.Sprintf("/repos/%s/%s/actions/runs/%d/cancel", r.owner, r.repo, runID)

	req, err := r.newRequest(ctx, "POST", path, nil)
	if err != nil {
		return err
	}

	resp, err := r.client.Client.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusAccepted {
		return nil
	}
	// 409 means it's already completed, which is fine
	if resp.StatusCode == http.StatusConflict {
		return nil
	}
	body, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("cancelling run %d: HTTP %d: %s", runID, resp.StatusCode, string(body))
}
