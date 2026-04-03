# History

This file summarizes the notable user-facing changes in each release.
Thanks to burkebot and dependabot[bot] for contributions reflected here.

## v0.6.0 (Unreleased)

Changes on `main` after `0.5.0`:

- Added `github-actions has-workflows`, a scripting-friendly subcommand that
  lists active GitHub Actions workflow URLs and exits successfully only when a
  repository actually has workflows configured for the current remote.
- Refactored the workflow-discovery helpers used by `wait` so the new command
  and the existing wait path share the same configuration checks and messages.

## v0.5.0 (2026-04-03)

- Improved `wait` so it fails early when a commit will not trigger any GitHub
  Actions workflows instead of waiting for runs that will never appear.
- Tightened the TTY status display by aligning run IDs, durations, and
  estimated times so active polling output is easier to scan.
- Switched Dependabot updates to a daily cadence for faster dependency and
  workflow maintenance.

## v0.4.0 (2026-03-30)

- Added `github-actions cancel [branch]` to cancel queued or in-progress runs
  for older commits on a branch while leaving the current tip alone.
- Added `--cancel-previous-runs` to `github-actions wait`, using the same
  cancellation logic only after the current tip's workflow run appears.
- Added a TTY-aware `wait` status renderer with in-place updates, color-aware
  symbols, and workflow duration estimates.
- Added early job failure detection in `wait` so broken jobs can surface before
  the overall workflow run reaches a terminal state.
- Improved timeout reporting and retry budgeting in `wait`, and added `--quiet`
  to suppress routine progress output.
- Taught `open` and `wait` to check other configured Git remotes when the
  selected remote has no runs for the requested commit.
- Improved Windows support by splitting retryable errno handling into
  platform-specific implementations and updated the release Makefile so version
  bumps can target `major` or `patch` explicitly.

## v0.3.0 (2026-03-24)

- Added retry logic around transient network failures when talking to the
  GitHub Actions API.
- Added `context.Context` plus a 10-second timeout to all Git subprocess
  execution to avoid hanging git invocations.
- Removed the `go-git` dependency and replaced it with local Git helpers.
- Enabled Dependabot for both Go modules and GitHub Actions updates.

## v0.2.1 (2026-02-16)

- Added package documentation for the `lib` package.

## v0.2.0 (2026-02-16)

- Initial public release of the `github-actions` CLI.
- Added a GitHub Actions CI workflow, expanded test coverage, and improved the
  displayed output to include pull request URLs.
- Added a `make release` target to automate version bumps.
