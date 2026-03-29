// The github-actions binary interacts with GitHub Actions.
//
// Usage:
//
//	github-actions command [arguments]
//
// The commands are:
//
//	version             Print the current version
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
	"time"

	"github.com/kevinburke/bigtext"
	ghactions "github.com/kevinburke/github-actions/lib"
)

const help = `The github-actions binary interacts with GitHub Actions.

Usage:

	github-actions command [arguments]

The commands are:

	open                Open the workflow run in your browser
	version             Print the current version
	wait                Wait for workflow runs to finish on a branch.

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

	waitflags := flag.NewFlagSet("wait", flag.ExitOnError)
	openflags := flag.NewFlagSet("open", flag.ExitOnError)

	waitRemote := waitflags.String("remote", "origin", "Git remote to use")
	waitOutputLines := waitflags.Int("failed-output-lines", 100, "Number of lines of failed output to display")
	waitTimeout := waitflags.Duration("timeout", time.Hour, "Maximum time to wait")
	waitQuiet := waitflags.Bool("quiet", false, "Only print final output, not periodic status updates")

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

		err = doWait(ctx, client, remote, *waitRemote, branch, *waitOutputLines, *waitQuiet)
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

func doWait(ctx context.Context, client *ghactions.Client, remote *RemoteURL, remoteName, branch string, numOutputLines int, quiet bool) error {
	tip, err := gitTip(ctx, branch)
	if err != nil {
		return err
	}

	owner, repo := remote.Path, remote.RepoName

	if !quiet {
		fmt.Println("Waiting for GitHub Actions on", branch, "to complete")
	}

	var lastPrintedAt time.Time
	startTime := time.Now()
	checkedOtherRemotes := false
	lastSuccessfulPollAt := startTime
	var lastObservedRuns []ghactions.WorkflowRun
	var lastRetryableErr error

	for {
		runs, err := client.Repo(owner, repo).FindWorkflowRunsForCommit(ctx, tip)
		if err != nil {
			if isHttpError(err) {
				lastRetryableErr = err
				lastPrintedAt = time.Now()
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
			if !quiet {
				fmt.Printf("No workflow runs found for %s yet, waiting...\n", shortRef(tip))
			}
			lastPrintedAt = time.Now()
			select {
			case <-ctx.Done():
				return waitTimeoutError(startTime, lastSuccessfulPollAt, tip, lastObservedRuns, lastRetryableErr)
			case <-time.After(5 * time.Second):
			}
			continue
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

		if allComplete {
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
				fmt.Printf("\nWorkflow %q (run %d)\n", run.Name, run.RunNumber)
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

		// Still running - print status periodically
		if !quiet && shouldPrint(lastPrintedAt, elapsed) {
			for _, run := range runs {
				status := run.Status
				if run.IsCompleted() && run.Conclusion != nil {
					status = *run.Conclusion
				}
				fmt.Printf("Workflow %q %s (%s elapsed)\n", run.Name, status, run.Duration().String())
			}
			lastPrintedAt = time.Now()
		}

		select {
		case <-time.After(3 * time.Second):
		case <-ctx.Done():
			return waitTimeoutError(startTime, lastSuccessfulPollAt, tip, lastObservedRuns, lastRetryableErr)
		}
	}
}
