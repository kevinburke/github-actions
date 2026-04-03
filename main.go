// The github-actions binary interacts with GitHub Actions.
//
// Usage:
//
//	github-actions command [arguments]
//
// The commands are:
//
//	version             Print the current version
//	has-workflows       Report whether GitHub Actions workflows are configured.
//	wait                Wait for workflow runs to finish on a branch.
//	open                Open the workflow run in your browser.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path"
	"time"

	"github.com/kevinburke/bigtext"
	ghactions "github.com/kevinburke/github-actions/lib"
)

const help = `The github-actions binary interacts with GitHub Actions.

Usage:

	github-actions command [arguments]

The commands are:

	cancel        Cancel older workflow runs on a branch
	has-workflows Report whether GitHub Actions workflows are configured
	open          Open the workflow run in your browser
	version       Print the current version
	wait          Wait for workflow runs to finish on a branch.

Use "github-actions [command] --help" for more information about a command.
`

func usage() {
	fmt.Fprint(os.Stderr, help)
	flag.PrintDefaults()
}

func init() {
	flag.Usage = usage
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cancelflags := flag.NewFlagSet("cancel", flag.ExitOnError)
	configuredflags := flag.NewFlagSet("has-workflows", flag.ExitOnError)
	waitflags := flag.NewFlagSet("wait", flag.ExitOnError)
	openflags := flag.NewFlagSet("open", flag.ExitOnError)

	cancelRemote := cancelflags.String("remote", "origin", "Git remote to use")
	cancelflags.Usage = func() {
		fmt.Fprintf(os.Stderr, `usage: cancel [branch]

Cancel in-progress and queued workflow runs on a branch that were triggered by
older commits. Runs for the current branch tip are left alone. By default, uses
the current branch.

`)
		cancelflags.PrintDefaults()
	}

	waitRemote := waitflags.String("remote", "origin", "Git remote to use")
	waitOutputLines := waitflags.Int("failed-output-lines", 100, "Number of lines of failed output to display")
	waitTimeout := waitflags.Duration("timeout", time.Hour, "Maximum time to wait")
	waitNoRunsTimeout := waitflags.Duration("no-runs-timeout", 2*time.Minute, "How long to wait for runs to appear before giving up (0 to disable)")
	waitQuiet := waitflags.Bool("quiet", false, "Only print final output, not periodic status updates")
	waitCancelPreviousRuns := waitflags.Bool("cancel-previous-runs", false, "Cancel older queued or in-progress workflow runs before waiting")

	waitflags.Usage = func() {
		fmt.Fprintf(os.Stderr, `usage: wait [refspec]

Wait for GitHub Actions workflow runs to complete, then print a descriptive
output on success or failure. By default, waits on the current branch,
otherwise you can pass a branch to wait for.

`)
		waitflags.PrintDefaults()
	}

	openRemote := openflags.String("remote", "origin", "Git remote to use")
	openflags.Usage = func() {
		fmt.Fprintf(os.Stderr, `usage: open [refspec]

Open the GitHub Actions workflow run for the current branch in your browser.

`)
		openflags.PrintDefaults()
	}

	configuredRemote := configuredflags.String("remote", "origin", "Git remote to use")
	configuredflags.Usage = func() {
		fmt.Fprintf(os.Stderr, `usage: has-workflows

Print one configured GitHub Actions workflow URL per line and exit 0 if any
active workflows are configured. Exits 1 if none are configured.

`)
		configuredflags.PrintDefaults()
	}

	debug := flag.Bool("debug", false, "Enable the debug log level")
	flag.Parse()

	if *debug {
		slog.SetLogLoggerLevel(slog.LevelDebug)
	}

	mainArgs := flag.Args()
	if len(mainArgs) < 1 {
		usage()
		os.Exit(2)
	}

	subargs := mainArgs[1:]

	if flag.Arg(0) == "version" {
		fmt.Fprintf(os.Stdout, "github-actions version %s\n", ghactions.Version)
		os.Exit(0)
	}

	switch flag.Arg(0) {
	case "cancel":
		cancelflags.Parse(subargs)
		args := cancelflags.Args()
		branch, err := getBranchFromArgs(ctx, args)
		checkError(err, "getting git branch")

		remote, err := getRemoteURL(ctx, *cancelRemote)
		checkError(err, "loading git info")

		host := remote.Host
		token, err := ghactions.GetToken(ctx, host)
		checkError(err, "getting GitHub token")

		client := ghactions.NewClient(token, host)

		err = doCancel(ctx, client, remote, *cancelRemote, branch)
		checkError(err, "cancelling workflow runs")

	case "has-workflows":
		configuredflags.Parse(subargs)

		remote, err := getRemoteURL(ctx, *configuredRemote)
		checkError(err, "loading git info")

		host := remote.Host
		token, err := ghactions.GetToken(ctx, host)
		checkError(err, "getting GitHub token")

		client := ghactions.NewClient(token, host)

		configured, err := doConfigured(ctx, client, remote)
		checkError(err, "checking configured workflows")
		if !configured {
			os.Exit(1)
		}

	case "wait":
		waitflags.Parse(subargs)
		args := waitflags.Args()
		branch, err := getBranchFromArgs(ctx, args)
		checkError(err, "getting git branch")

		remote, err := getRemoteURL(ctx, *waitRemote)
		checkError(err, "loading git info")

		host := remote.Host
		token, err := ghactions.GetToken(ctx, host)
		checkError(err, "getting GitHub token")

		client := ghactions.NewClient(token, host)

		ctx, cancel := context.WithTimeout(ctx, *waitTimeout)
		defer cancel()

		err = doWait(ctx, client, remote, *waitRemote, branch, *waitOutputLines, *waitQuiet, *waitCancelPreviousRuns, *waitNoRunsTimeout)
		checkError(err, "waiting for workflow runs")

	case "open":
		openflags.Parse(subargs)
		args := openflags.Args()
		branch, err := getBranchFromArgs(ctx, args)
		checkError(err, "getting git branch")

		remote, err := getRemoteURL(ctx, *openRemote)
		checkError(err, "loading git info")

		host := remote.Host
		token, err := ghactions.GetToken(ctx, host)
		checkError(err, "getting GitHub token")

		client := ghactions.NewClient(token, host)

		err = doOpen(ctx, client, remote, *openRemote, branch)
		checkError(err, "opening workflow run")

	default:
		fmt.Fprintf(os.Stderr, "github-actions: unknown command %q\n\n", flag.Arg(0))
		usage()
		os.Exit(2)
	}
}

func checkError(err error, msg string) {
	if err != nil {
		failError(err, msg)
	}
}

func failError(err error, msg string) {
	if msg == "" {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	} else {
		fmt.Fprintf(os.Stderr, "Error %s: %v\n", msg, err)
	}
	os.Exit(1)
}

func getBranchFromArgs(ctx context.Context, args []string) (string, error) {
	if len(args) >= 1 {
		return args[0], nil
	}
	return currentBranch(ctx)
}

// isHttpError checks if the given error is a request timeout or a network
// failure - in those cases we want to just retry the request.
func isHttpError(err error) bool {
	return ghactions.IsRetryableError(err)
}

var errNoWorkflowRuns = errors.New("github-actions: no workflow runs found")

// otherRemoteResult holds information about workflow runs found on a
// non-primary remote.
type otherRemoteResult struct {
	RemoteName string
	Remote     *RemoteURL
	Runs       []ghactions.WorkflowRun
}

// checkOtherRemotes queries all git remotes (except currentRemoteName) for
// workflow runs matching the given commit SHA. It returns results for any
// remote that has runs. Errors are logged but not fatal — this is best-effort.
func checkOtherRemotes(ctx context.Context, currentRemoteName, tip string) []otherRemoteResult {
	remoteNames, err := listRemoteNames(ctx)
	if err != nil {
		slog.Debug("could not list git remotes", "error", err)
		return nil
	}
	var results []otherRemoteResult
	for _, name := range remoteNames {
		if name == currentRemoteName {
			continue
		}
		remote, err := getRemoteURL(ctx, name)
		if err != nil {
			slog.Debug("could not get remote URL", "remote", name, "error", err)
			continue
		}
		token, err := ghactions.GetToken(ctx, remote.Host)
		if err != nil {
			slog.Debug("could not get token for remote", "remote", name, "host", remote.Host, "error", err)
			continue
		}
		client := ghactions.NewClient(token, remote.Host)
		runs, err := client.Repo(remote.Path, remote.RepoName).FindWorkflowRunsForCommit(ctx, tip)
		if err != nil {
			slog.Debug("could not query workflow runs on remote", "remote", name, "error", err)
			continue
		}
		if len(runs) > 0 {
			results = append(results, otherRemoteResult{
				RemoteName: name,
				Remote:     remote,
				Runs:       runs,
			})
		}
	}
	return results
}

// printOtherRemoteHints prints suggestions for any workflow runs found on other
// remotes. Returns true if any results were printed.
func printOtherRemoteHints(results []otherRemoteResult) bool {
	if len(results) == 0 {
		return false
	}
	fmt.Println()
	for _, r := range results {
		run := r.Runs[0]
		status := run.Status
		if run.IsCompleted() && run.Conclusion != nil {
			status = *run.Conclusion
		}
		fmt.Printf("Found workflow runs on remote %q (%s/%s): %s\n", r.RemoteName, r.Remote.Path, r.Remote.RepoName, status)
		fmt.Printf("  Try: github-actions wait --remote %s\n", r.RemoteName)
		fmt.Printf("  URL: %s\n", run.HTMLURL)
	}
	return true
}

func shouldPrint(lastPrinted time.Time, duration time.Duration) bool {
	now := time.Now()
	var durToUse time.Duration
	switch {
	case duration > 25*time.Minute:
		durToUse = 3 * time.Minute
	case duration > 8*time.Minute:
		durToUse = 2 * time.Minute
	case duration > 5*time.Minute:
		durToUse = 30 * time.Second
	case duration > 3*time.Minute:
		durToUse = 20 * time.Second
	case duration > time.Minute:
		durToUse = 15 * time.Second
	default:
		durToUse = 10 * time.Second
	}
	return lastPrinted.Add(durToUse).Before(now)
}

func shortRef(ref string) string {
	if len(ref) > 8 {
		return ref[:8]
	}
	return ref
}

func clampDuration(d, min, max time.Duration) time.Duration {
	if d < min {
		return min
	}
	if d > max {
		return max
	}
	return d
}

func formatWaitDuration(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	if d >= time.Second {
		return d.Round(time.Second).String()
	}
	return d.Round(100 * time.Millisecond).String()
}

func estimateRemainingWait(runs []ghactions.WorkflowRun) time.Duration {
	var longestCompleted time.Duration
	for _, run := range runs {
		if !run.IsCompleted() {
			continue
		}
		if d := run.Duration(); d > longestCompleted {
			longestCompleted = d
		}
	}

	var remaining time.Duration
	for _, run := range runs {
		if run.IsCompleted() {
			continue
		}
		elapsed := run.Duration()
		var estimate time.Duration
		switch {
		case longestCompleted > elapsed:
			estimate = longestCompleted - elapsed
		case longestCompleted > 0:
			estimate = clampDuration(elapsed/10, time.Minute, 5*time.Minute)
		case elapsed > 0:
			estimate = max(elapsed, 2*time.Minute)
		default:
			estimate = 2 * time.Minute
		}
		if estimate > remaining {
			remaining = estimate
		}
	}
	return remaining
}

func networkStallBudget(runs []ghactions.WorkflowRun) time.Duration {
	const (
		minBudget = 2 * time.Minute
		maxBudget = 20 * time.Minute
	)
	if len(runs) == 0 {
		return minBudget
	}
	remaining := estimateRemainingWait(runs)
	return clampDuration(2*time.Minute+remaining/2, minBudget, maxBudget)
}

func waitTimeoutError(startTime, lastSuccessfulPollAt time.Time, tip string, runs []ghactions.WorkflowRun, lastRetryableErr error) error {
	totalWait := formatWaitDuration(time.Since(startTime))
	if lastRetryableErr != nil {
		stalledFor := formatWaitDuration(time.Since(lastSuccessfulPollAt))
		return fmt.Errorf("could not reach GitHub for %s after retrying (waited %s total; last error: %s)", stalledFor, totalWait, ghactions.ShortRetryableError(lastRetryableErr))
	}
	if len(runs) == 0 {
		return fmt.Errorf("timed out after waiting %s for workflow runs to appear for %s (hit --timeout, not a network error; increase it if they usually start later)", totalWait, shortRef(tip))
	}
	return fmt.Errorf("timed out after waiting %s for workflow runs to complete (hit --timeout, not a network error; increase it if this branch usually runs longer)", totalWait)
}

func waitErrorForRetryablePollFailure(ctx context.Context, startTime, lastSuccessfulPollAt time.Time, tip string, runs []ghactions.WorkflowRun, lastRetryableErr error) error {
	if ctx.Err() != nil {
		return waitTimeoutError(startTime, lastSuccessfulPollAt, tip, runs, nil)
	}
	if time.Since(lastSuccessfulPollAt) >= networkStallBudget(runs) {
		return waitTimeoutError(startTime, lastSuccessfulPollAt, tip, runs, lastRetryableErr)
	}
	return nil
}

func pluralize(count int, singular string) string {
	if count == 1 {
		return singular
	}
	return singular + "s"
}

func workflowRunIdentifier(run ghactions.WorkflowRun) string {
	switch {
	case run.RunNumber > 0:
		return fmt.Sprintf("run %d", run.RunNumber)
	default:
		return ""
	}
}

func workflowRunDisplayName(run ghactions.WorkflowRun) string {
	id := workflowRunIdentifier(run)
	if id == "" {
		return run.Name
	}
	return fmt.Sprintf("%s [%s]", run.Name, id)
}

func hasWorkflowRunsForCommit(tip string, runs []ghactions.WorkflowRun) bool {
	for _, run := range runs {
		if run.HeadSha == tip {
			return true
		}
	}
	return false
}

func cancelableWorkflowRuns(tip string, runs []ghactions.WorkflowRun) []ghactions.WorkflowRun {
	cancelable := make([]ghactions.WorkflowRun, 0, len(runs))
	for _, run := range runs {
		if run.IsCompleted() {
			continue
		}
		if run.HeadSha == tip {
			continue
		}
		cancelable = append(cancelable, run)
	}
	return cancelable
}

func activeWorkflows(workflows []ghactions.Workflow) []ghactions.Workflow {
	active := make([]ghactions.Workflow, 0, len(workflows))
	for _, workflow := range workflows {
		if workflow.State == "active" {
			active = append(active, workflow)
		}
	}
	return active
}

func workflowConfigurationError(owner, repo string, workflows *ghactions.WorkflowsResponse) error {
	active := activeWorkflows(workflows.Workflows)
	if len(active) > 0 {
		return nil
	}
	if workflows.TotalCount == 0 {
		return fmt.Errorf("no workflow files found in %s/%s; add a .github/workflows/*.yml file to enable GitHub Actions", owner, repo)
	}
	return fmt.Errorf("all %d workflows in %s/%s are disabled; enable at least one to run GitHub Actions", workflows.TotalCount, owner, repo)
}

func workflowURL(remote *RemoteURL, workflow ghactions.Workflow) string {
	if workflow.HTMLURL != "" {
		return workflow.HTMLURL
	}
	if workflow.Path != "" {
		return fmt.Sprintf("https://%s/%s/%s/actions/workflows/%s", remote.Host, remote.Path, remote.RepoName, path.Base(workflow.Path))
	}
	return fmt.Sprintf("https://%s/%s/%s/actions", remote.Host, remote.Path, remote.RepoName)
}

func configuredWorkflowLinks(remote *RemoteURL, workflows []ghactions.Workflow) []string {
	active := activeWorkflows(workflows)
	links := make([]string, 0, len(active))
	seen := make(map[string]struct{}, len(active))
	for _, workflow := range active {
		link := workflowURL(remote, workflow)
		if _, ok := seen[link]; ok {
			continue
		}
		seen[link] = struct{}{}
		links = append(links, link)
	}
	return links
}

func doConfigured(ctx context.Context, client *ghactions.Client, remote *RemoteURL) (bool, error) {
	workflows, err := client.Repo(remote.Path, remote.RepoName).ListWorkflows(ctx)
	if err != nil {
		return false, err
	}
	links := configuredWorkflowLinks(remote, workflows.Workflows)
	for _, link := range links {
		fmt.Println(link)
	}
	return len(links) > 0, nil
}

func cancelPreviousRunsForTip(ctx context.Context, client *ghactions.Client, remote *RemoteURL, remoteName, branch, tip string, quietWhenNoRuns bool) error {
	owner, repo := remote.Path, remote.RepoName
	runs, err := client.Repo(owner, repo).FindWorkflowRunsForBranch(ctx, branch)
	if err != nil {
		return fmt.Errorf("listing workflow runs: %w", err)
	}

	if len(runs) == 0 {
		if !quietWhenNoRuns {
			fmt.Printf("No workflow runs found for %s on %s/%s\n", branch, owner, repo)
			results := checkOtherRemotes(ctx, remoteName, tip)
			printOtherRemoteHints(results)
		}
		return errNoWorkflowRuns
	}

	cancelable := cancelableWorkflowRuns(tip, runs)
	var cancelled int
	for _, run := range cancelable {
		slog.Debug("cancelling run", "id", run.ID, "name", run.Name, "sha", shortRef(run.HeadSha))
		if err := client.Repo(owner, repo).CancelWorkflowRun(ctx, run.ID); err != nil {
			return fmt.Errorf("cancelling run %d (%s): %w", run.ID, run.Name, err)
		}
		identifier := workflowRunIdentifier(run)
		if identifier == "" {
			fmt.Printf("Cancelled %q (commit %s)\n", run.Name, shortRef(run.HeadSha))
		} else {
			fmt.Printf("Cancelled %q (%s, commit %s)\n", run.Name, identifier, shortRef(run.HeadSha))
		}
		cancelled++
	}

	if cancelled == 0 {
		fmt.Printf("No older workflow runs to cancel on %s (tip: %s)\n", branch, shortRef(tip))
	} else {
		fmt.Printf("Cancelled %d older %s on %s\n", cancelled, pluralize(cancelled, "workflow run"), branch)
	}
	return nil
}

func cancelPreviousRunsOnBranch(ctx context.Context, client *ghactions.Client, remote *RemoteURL, remoteName, branch string, quietWhenNoRuns bool) (string, error) {
	tip, err := gitTip(ctx, branch)
	if err != nil {
		return "", err
	}
	return tip, cancelPreviousRunsForTip(ctx, client, remote, remoteName, branch, tip, quietWhenNoRuns)
}

func doCancel(ctx context.Context, client *ghactions.Client, remote *RemoteURL, remoteName, branch string) error {
	_, err := cancelPreviousRunsOnBranch(ctx, client, remote, remoteName, branch, false)
	return err
}

func doOpen(ctx context.Context, client *ghactions.Client, remote *RemoteURL, remoteName, branch string) error {
	tip, err := gitTip(ctx, branch)
	if err != nil {
		return err
	}

	owner, repo := remote.Path, remote.RepoName
	checkedOtherRemotes := false

	for {
		runs, err := client.Repo(owner, repo).FindWorkflowRunsForCommit(ctx, tip)
		if err != nil {
			if isHttpError(err) {
				fmt.Printf("Caught network error: %s. Continuing\n", err.Error())
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(2 * time.Second):
				}
				continue
			}
			return err
		}

		if len(runs) == 0 {
			if !checkedOtherRemotes {
				checkedOtherRemotes = true
				results := checkOtherRemotes(ctx, remoteName, tip)
				if printOtherRemoteHints(results) {
					return errNoWorkflowRuns
				}
			}
			fmt.Printf("No workflow runs found for %s yet, waiting...\n", tip[:8])
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Second):
			}
			continue
		}

		// Open the first (most recent) run
		run := runs[0]
		if err := openURL(run.HTMLURL); err != nil {
			return err
		}
		return nil
	}
}

func doWait(ctx context.Context, client *ghactions.Client, remote *RemoteURL, remoteName, branch string, numOutputLines int, quiet, cancelPreviousRuns bool, noRunsTimeout time.Duration) error {
	tip, err := gitTip(ctx, branch)
	if err != nil {
		return err
	}

	owner, repo := remote.Path, remote.RepoName
	repoSvc := client.Repo(owner, repo)

	// Check upfront whether the repo has any workflow files at all. If not,
	// there is no point polling — error immediately.
	workflows, err := repoSvc.ListWorkflows(ctx)
	if err != nil {
		slog.Debug("could not list workflows", "error", err)
		// Non-fatal: fall through to the normal polling loop.
	} else {
		if err := workflowConfigurationError(owner, repo, workflows); err != nil {
			return err
		}
	}

	renderer := newStatusRenderer(quiet)
	cancelledPreviousRuns := false

	if !quiet {
		fmt.Println("Waiting for GitHub Actions on", branch, "to complete")
	}

	var lastJobCheckAt time.Time
	startTime := time.Now()
	checkedOtherRemotes := false
	lastSuccessfulPollAt := startTime
	var lastObservedRuns []ghactions.WorkflowRun
	var lastRetryableErr error

	for {
		runs, err := repoSvc.FindWorkflowRunsForCommit(ctx, tip)
		if err != nil {
			if isHttpError(err) {
				lastRetryableErr = err
				if waitErr := waitErrorForRetryablePollFailure(ctx, startTime, lastSuccessfulPollAt, tip, lastObservedRuns, lastRetryableErr); waitErr != nil {
					return waitErr
				}
				if !quiet {
					fmt.Printf("GitHub request failed: %s. Retrying\n", ghactions.ShortRetryableError(err))
				}
				select {
				case <-ctx.Done():
					return waitTimeoutError(startTime, lastSuccessfulPollAt, tip, lastObservedRuns, nil)
				case <-time.After(2 * time.Second):
				}
				continue
			}
			return err
		}
		lastSuccessfulPollAt = time.Now()
		lastRetryableErr = nil
		lastObservedRuns = append(lastObservedRuns[:0], runs...)

		if len(runs) == 0 {
			if !checkedOtherRemotes {
				checkedOtherRemotes = true
				results := checkOtherRemotes(ctx, remoteName, tip)
				if printOtherRemoteHints(results) {
					return errNoWorkflowRuns
				}
			}
			if noRunsTimeout > 0 && time.Since(startTime) >= noRunsTimeout {
				return fmt.Errorf("no workflow runs appeared for %s after %s (workflows exist but none triggered for this commit; check workflow trigger conditions, or increase --no-runs-timeout)", shortRef(tip), formatWaitDuration(time.Since(startTime)))
			}
			if !quiet {
				fmt.Printf("No workflow runs found for %s yet, waiting...\n", shortRef(tip))
			}
			select {
			case <-ctx.Done():
				return waitTimeoutError(startTime, lastSuccessfulPollAt, tip, lastObservedRuns, lastRetryableErr)
			case <-time.After(5 * time.Second):
			}
			continue
		}

		if cancelPreviousRuns && !cancelledPreviousRuns && hasWorkflowRunsForCommit(tip, runs) {
			if err := cancelPreviousRunsForTip(ctx, client, remote, remoteName, branch, tip, true); err != nil {
				return err
			}
			cancelledPreviousRuns = true
		}

		// Fetch duration estimates once
		if !renderer.estimatesDone {
			renderer.fetchEstimates(ctx, repoSvc, runs)
		}

		// Check if all runs are complete
		allComplete := true
		anyFailed := false
		var failedRun *ghactions.WorkflowRun

		for i := range runs {
			run := &runs[i]
			if !run.IsCompleted() {
				allComplete = false
			}
			if run.IsFailed() {
				anyFailed = true
				if failedRun == nil {
					failedRun = run
				}
			}
		}

		elapsed := time.Since(startTime).Round(time.Second)

		// Check for early job failures in in-progress runs. We throttle
		// these checks to avoid excessive API calls - check every 15
		// seconds, and only after the runs have been going for at least
		// 30 seconds (to let jobs start up).
		if !allComplete && !anyFailed && elapsed > 30*time.Second && time.Since(lastJobCheckAt) > 15*time.Second {
			lastJobCheckAt = time.Now()
			for i := range runs {
				run := &runs[i]
				if run.IsCompleted() {
					continue
				}
				failedJob, err := repoSvc.FindFailedJob(ctx, run.ID)
				if err != nil {
					// Non-fatal: log and continue polling normally.
					if !quiet {
						fmt.Printf("Error checking jobs for %q: %v\n", run.Name, err)
					}
					continue
				}
				if failedJob != nil {
					anyFailed = true
					failedRun = run
					if !quiet {
						fmt.Printf("Job %q failed in workflow %q (run still in progress)\n", failedJob.Name, run.Name)
					}
					break
				}
			}
		}

		if allComplete || anyFailed {
			renderer.clearStatus()
			c := bigtext.Client{
				Name: "github-actions (" + repo + ")",
			}

			if anyFailed && failedRun != nil {
				data := client.BuildSummary(ctx, owner, repo, *failedRun, numOutputLines)
				os.Stdout.Write(data)
				fmt.Printf("\nURL:\n%s\n", failedRun.HTMLURL)
				c.Display("build failed")
				return fmt.Errorf("Build on %s failed!", branch)
			}

			// All succeeded
			var totalDuration time.Duration
			for _, run := range runs {
				d := run.Duration()
				if d > totalDuration {
					totalDuration = d
				}
			}

			for _, run := range runs {
				identifier := workflowRunIdentifier(run)
				if identifier == "" {
					fmt.Printf("\nWorkflow %q\n", run.Name)
				} else {
					fmt.Printf("\nWorkflow %q (%s)\n", run.Name, identifier)
				}
				summary := client.BuildJobsSummary(ctx, owner, repo, run)
				if len(summary) > 0 && summary[0] == '\n' {
					summary = summary[1:]
				}
				os.Stdout.Write(summary)
			}

			// Print summary
			fmt.Printf("\n")
			fmt.Println(string(bytes.Repeat([]byte{'='}, 40)))
			fmt.Printf("Tests on %s took %s. Quitting.\n", branch, totalDuration.String())

			if len(runs) > 0 {
				fmt.Printf("%s\n", runs[0].HTMLURL)
				if len(runs[0].PullRequests) > 0 {
					pr := runs[0].PullRequests[0]
					fmt.Printf("https://%s/%s/%s/pull/%d\n", remote.Host, owner, repo, pr.Number)
				} else {
					fmt.Printf("https://%s/%s/%s/tree/%s\n", remote.Host, owner, repo, branch)
				}
			}

			c.Display(branch + " build complete!")
			return nil
		}

		// Still running - render immediately, then redraw every second
		// until the next poll. In TTY mode this keeps the elapsed
		// durations ticking smoothly; in non-TTY mode render() applies
		// its own shouldPrint throttle so extra calls are no-ops.
		renderer.render(runs)

		for range 3 {
			select {
			case <-time.After(1 * time.Second):
				renderer.render(runs)
			case <-ctx.Done():
				return waitTimeoutError(startTime, lastSuccessfulPollAt, tip, lastObservedRuns, lastRetryableErr)
			}
		}
	}
}
