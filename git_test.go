package main

import (
	"testing"
)

func TestParseRemoteURL(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		wantHost string
		wantPath string
		wantRepo string
	}{
		{
			name:     "ssh short form",
			raw:      "git@github.com:kevinburke/github-actions.git",
			wantHost: "github.com",
			wantPath: "kevinburke",
			wantRepo: "github-actions",
		},
		{
			name:     "ssh short form no .git",
			raw:      "git@github.com:kevinburke/github-actions",
			wantHost: "github.com",
			wantPath: "kevinburke",
			wantRepo: "github-actions",
		},
		{
			name:     "https",
			raw:      "https://github.com/kevinburke/github-actions.git",
			wantHost: "github.com",
			wantPath: "kevinburke",
			wantRepo: "github-actions",
		},
		{
			name:     "https no .git",
			raw:      "https://github.com/kevinburke/github-actions",
			wantHost: "github.com",
			wantPath: "kevinburke",
			wantRepo: "github-actions",
		},
		{
			name:     "ssh long form",
			raw:      "ssh://git@github.com/kevinburke/github-actions.git",
			wantHost: "github.com",
			wantPath: "kevinburke",
			wantRepo: "github-actions",
		},
		{
			name:     "nested path",
			raw:      "git@gitlab.com:org/subgroup/repo.git",
			wantHost: "gitlab.com",
			wantPath: "org/subgroup",
			wantRepo: "repo",
		},
		{
			name:     "https with trailing slash",
			raw:      "https://github.com/kevinburke/github-actions/",
			wantHost: "github.com",
			wantPath: "kevinburke",
			wantRepo: "github-actions",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseRemoteURL(tt.raw)
			if err != nil {
				t.Fatalf("parseRemoteURL(%q) returned error: %v", tt.raw, err)
			}
			if got.Host != tt.wantHost {
				t.Errorf("Host = %q, want %q", got.Host, tt.wantHost)
			}
			if got.Path != tt.wantPath {
				t.Errorf("Path = %q, want %q", got.Path, tt.wantPath)
			}
			if got.RepoName != tt.wantRepo {
				t.Errorf("RepoName = %q, want %q", got.RepoName, tt.wantRepo)
			}
		})
	}
}

func TestParseRemoteURLErrors(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{"empty string", ""},
		{"just a hostname", "github.com"},
		{"ssh missing colon", "git@github.com/repo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseRemoteURL(tt.raw)
			if err == nil {
				t.Errorf("parseRemoteURL(%q) expected error, got nil", tt.raw)
			}
		})
	}
}
