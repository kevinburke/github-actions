package main

import (
	"context"
	"fmt"
	"net/url"
	"os/exec"
	"strings"
	"time"
)

// gitTimeout is the maximum time to wait for a local git command to complete.
const gitTimeout = 10 * time.Second

// RemoteURL holds parsed components of a git remote URL.
type RemoteURL struct {
	// Host is the hostname (e.g. "github.com")
	Host string
	// Path is the user or organization (e.g. "kevinburke" in github.com/kevinburke/repo)
	Path string
	// RepoName is the repository name (e.g. "repo")
	RepoName string
}

// getRemoteURL returns a parsed RemoteURL for the named git remote.
func getRemoteURL(ctx context.Context, remoteName string) (*RemoteURL, error) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "config", "--get", fmt.Sprintf("remote.%s.url", remoteName)).Output()
	if err != nil {
		return nil, fmt.Errorf("getting remote %q URL: %w", remoteName, err)
	}
	return parseRemoteURL(strings.TrimSpace(string(out)))
}

// parseRemoteURL extracts the host, org/user and repo name from a git remote URL.
// Supports:
//   - SSH short form: git@github.com:user/repo.git
//   - HTTPS: https://github.com/user/repo.git
//   - SSH long form: ssh://git@github.com/user/repo.git
func parseRemoteURL(raw string) (*RemoteURL, error) {
	raw = strings.TrimSpace(raw)

	// SSH short form: git@host:path/repo.git
	if i := strings.Index(raw, "@"); i >= 0 && !strings.Contains(raw, "://") {
		// Everything after the ":"
		colonIdx := strings.Index(raw[i:], ":")
		if colonIdx < 0 {
			return nil, fmt.Errorf("could not parse git remote URL %q", raw)
		}
		host := raw[i+1 : i+colonIdx]
		pathRepo := raw[i+colonIdx+1:]
		return splitPathRepo(host, pathRepo, raw)
	}

	// URL form (https://, ssh://, etc.)
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("could not parse git remote URL %q: %w", raw, err)
	}
	pathRepo := strings.TrimPrefix(u.Path, "/")
	return splitPathRepo(u.Hostname(), pathRepo, raw)
}

func splitPathRepo(host, pathRepo, raw string) (*RemoteURL, error) {
	pathRepo = strings.TrimSuffix(pathRepo, "/")
	pathRepo = strings.TrimSuffix(pathRepo, ".git")
	parts := strings.Split(pathRepo, "/")
	if len(parts) < 2 {
		return nil, fmt.Errorf("could not parse git remote URL %q: expected user/repo path, got %q", raw, pathRepo)
	}
	return &RemoteURL{
		Host:     host,
		Path:     strings.Join(parts[:len(parts)-1], "/"),
		RepoName: parts[len(parts)-1],
	}, nil
}

// listRemoteNames returns the names of all configured git remotes.
func listRemoteNames(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "remote").Output()
	if err != nil {
		return nil, fmt.Errorf("listing git remotes: %w", err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var remotes []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			remotes = append(remotes, line)
		}
	}
	return remotes, nil
}

// currentBranch returns the name of the current Git branch.
func currentBranch(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "symbolic-ref", "--short", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("getting current branch: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// gitTip returns the full commit SHA for the given branch or ref.
// If branch is empty, defaults to HEAD.
func gitTip(ctx context.Context, branch string) (string, error) {
	if branch == "" {
		branch = "HEAD"
	}
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "rev-parse", branch).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("getting tip of %q: %s", branch, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}
