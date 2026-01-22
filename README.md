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
    open      Open the workflow run in your browser
    version   Print the current version
    wait      Wait for workflow runs to finish on a branch
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

Examples:
```bash
# Wait for workflows on current branch
github-actions wait

# Wait for workflows on a specific branch
github-actions wait main

# Wait with a 30 minute timeout
github-actions wait --timeout 30m
```

### open

Open the GitHub Actions workflow run for the current branch in your browser.

```bash
github-actions open [flags] [branch]
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
