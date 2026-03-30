# Changelog

## Unreleased

Changes after the `v0.3.0` tag (2026-03-24):

### 2026-03-29

- Added `--cancel-previous-runs` to `github-actions wait`, reusing the same
  cancellation logic as `cancel`, but only after a workflow run for the
  current tip commit has appeared so older runs are not cancelled too early.
  The output now includes GitHub run numbers and clearer
  singular/plural wording when reporting cancelled runs.
- Added `github-actions cancel [branch]` to cancel queued or in-progress
  workflow runs for older commits on a branch, while leaving the current tip's
  runs alone.
- Added a TTY-aware status renderer for `wait` with in-place updates,
  color-coded icons, braille spinners, and workflow duration estimates based on
  recent completed runs. Non-TTY output still uses appended plain text, and
  `NO_COLOR` is supported.
- Improved `wait` timeout reporting to distinguish an explicit `--timeout` from
  retryable network stalls, include elapsed time, and scale retry budgets based
  on observed run durations.
- Added early job failure detection in `wait`, so failed jobs can be reported
  before the overall workflow run reaches a terminal state. Job checks start
  after 30 seconds, run every 15 seconds, and fall back to normal polling if
  job inspection fails.

### 2026-03-28

- Added `--quiet` to `github-actions wait` to suppress periodic progress and
  retry messages while still printing the final success or failure output.

### 2026-03-25

- When no workflow runs are found on the requested remote, `open` and `wait`
  now check other configured Git remotes for the same commit and print a
  suggestion if the workflows exist elsewhere, instead of polling the wrong
  remote indefinitely.

### 2026-03-24

- Improved Windows support by splitting retryable connection error handling
  into platform-specific `errno` implementations and tests, avoiding
  `x/sys/unix` on Windows.
- Updated the release Makefile to accept `major` and `patch` arguments when
  bumping versions.
