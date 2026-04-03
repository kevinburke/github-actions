# github-actions

A CLI tool to wait for GitHub Actions workflow runs to complete and open them in
your browser.

## Installation

```bash
go install github.com/kevinburke/github-actions@latest
```

## Usage

```
github-actions command [arguments]

Commands:
    cancel        Cancel older workflow runs on a branch
    has-workflows Report whether GitHub Actions workflows are configured
    open          Open the workflow run in your browser
    version       Print the current version
    wait          Wait for workflow runs to finish on a branch
```

### wait

Wait for all GitHub Actions workflow runs on the current commit to complete.

```bash
github-actions wait [flags] [branch]
```

Flags:
- `--remote` - Git remote to use (default "origin")
- `--timeout` - Maximum time to wait (default 1h)
- `--failed-output-lines` - Number of lines of failed output to display (default 100)
- `--quiet` - Only print final output, not periodic status updates
- `--cancel-previous-runs` - Cancel older queued or in-progress workflow runs before waiting

When stdout is a terminal, `wait` displays an in-place status table with
spinners and color-coded icons that updates every 3 seconds. When piped or
redirected, it falls back to appended plain-text lines.

Examples:
```bash
# Wait for workflows on current branch
github-actions wait

# Wait for workflows on a specific branch
github-actions wait main

# Wait with a 30 minute timeout
github-actions wait --timeout 30m

# Cancel older runs before waiting for the current commit
github-actions wait --cancel-previous-runs
```

### cancel

Cancel queued or in-progress workflow runs from older commits on a branch while
leaving the current branch tip alone.

```bash
github-actions cancel [flags] [branch]
```

Flags:
- `--remote` - Git remote to use (default "origin")

### open

Open the GitHub Actions workflow run for the current branch in your browser.

```bash
github-actions open [flags] [branch]
```

Flags:
- `--remote` - Git remote to use (default "origin")

### has-workflows

Print one active workflow URL per line. The command exits `0` when the
repository has at least one active workflow, exits `1` when it does not, and
exits `2` when a real error occurs while checking.

```bash
github-actions has-workflows [flags]
```

Flags:
- `--remote` - Git remote to use (default "origin")

## Authentication

The tool looks for a GitHub token in the following order:

1. `GH_TOKEN` environment variable
2. `GITHUB_TOKEN` environment variable
3. Config file

### Config file locations

- `$XDG_CONFIG_HOME/github-actions`
- `$HOME/cfg/github-actions`
- `$HOME/.github-actions`

### Config file format (TOML)

```toml
default = "github.com"

[hosts]

[hosts."github.com"]
token = "ghp_xxxx"

[hosts."github.mycompany.com"]
token = "ghp_yyyy"
```

## Token permissions

The token needs the `repo` scope (or `actions:read` for public repositories) to
access workflow run information.

Create a token at: https://github.com/settings/tokens

## Environment variables

- `GH_TOKEN` or `GITHUB_TOKEN` - GitHub API token
- `NO_COLOR` - Set to any value to disable colored output (see https://no-color.org)
