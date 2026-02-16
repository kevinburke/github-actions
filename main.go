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
	"net"
	"net/url"
	"os"
	"time"

	"github.com/kevinburke/bigtext"
	ghactions "github.com/kevinburke/github-actions/lib"
	git "github.com/kevinburke/go-git"
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

		remote, err := git.GetRemoteURL(*waitRemote)
		checkError(err, "loading git info")

		host := remote.Host
		token, err := ghactions.GetToken(ctx, host)
		checkError(err, "getting GitHub token")

		client := ghactions.NewClient(token, host)

		ctx, cancel := context.WithTimeout(ctx, *waitTimeout)
		defer cancel()

		err = doWait(ctx, client, remote, branch, *waitOutputLines)
		checkError(err, "waiting for workflow runs")

	case "open":
		openflags.Parse(subargs)
		args := openflags.Args()
		branch, err := getBranchFromArgs(ctx, args)
		checkError(err, "getting git branch")

		remote, err := git.GetRemoteURL(*openRemote)
		checkError(err, "loading git info")

		host := remote.Host
		token, err := ghactions.GetToken(ctx, host)
		checkError(err, "getting GitHub token")

		client := ghactions.NewClient(token, host)

		err = doOpen(ctx, client, remote, branch)
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
	return git.CurrentBranch(ctx)
}

// isHttpError checks if the given error is a request timeout or a network
// failure - in those cases we want to just retry the request.
func isHttpError(err error) bool {
	if err == nil {
		return false
	}
	if uerr, ok := err.(*url.Error); ok {
		err = uerr.Err
	}
	switch err := err.(type) {
	default:
		return false
	case *net.OpError:
		return err.Op == "dial" && err.Net == "tcp"
	case *net.DNSError:
		return true
	case net.Error:
		return err.Timeout()
	}
}

var errNoWorkflowRuns = errors.New("github-actions: no workflow runs found")

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

func doOpen(ctx context.Context, client *ghactions.Client, remote *git.RemoteURL, branch string) error {
	tip, err := git.Tip(branch)
	if err != nil {
		return err
	}

	owner, repo := remote.Path, remote.RepoName

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

func doWait(ctx context.Context, client *ghactions.Client, remote *git.RemoteURL, branch string, numOutputLines int) error {
	tip, err := git.Tip(branch)
	if err != nil {
		return err
	}

	owner, repo := remote.Path, remote.RepoName

	fmt.Println("Waiting for GitHub Actions on", branch, "to complete")

	var lastPrintedAt time.Time
	startTime := time.Now()

	for {
		runs, err := client.Repo(owner, repo).FindWorkflowRunsForCommit(ctx, tip)
		if err != nil {
			if isHttpError(err) {
				fmt.Printf("Caught network error: %s. Continuing\n", err.Error())
				lastPrintedAt = time.Now()
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
			fmt.Printf("No workflow runs found for %s yet, waiting...\n", tip[:8])
			lastPrintedAt = time.Now()
			select {
			case <-ctx.Done():
				return ctx.Err()
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
		if shouldPrint(lastPrintedAt, elapsed) {
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
			return ctx.Err()
		}
	}
}
