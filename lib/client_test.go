package lib

import (
	"testing"
)

func TestFailedJobURL(t *testing.T) {
	tests := []struct {
		name string
		job  *Job
		want string
	}{
		{
			name: "failed step anchor",
			job: &Job{
				HTMLURL: "https://github.com/o/r/actions/runs/1/job/2",
				Steps: []Step{
					{Number: 1},
					{Number: 17, Conclusion: stringPtr("failure")},
				},
			},
			want: "https://github.com/o/r/actions/runs/1/job/2#step:17:1",
		},
		{
			name: "job url only",
			job: &Job{
				HTMLURL: "https://github.com/o/r/actions/runs/1/job/2",
			},
			want: "https://github.com/o/r/actions/runs/1/job/2",
		},
		{
			name: "missing job",
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := failedJobURL(tt.job)
			if got != tt.want {
				t.Fatalf("failedJobURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func stringPtr(s string) *string { return &s }

func TestFindBuildFailure(t *testing.T) {
	tests := []struct {
		name           string
		log            string
		numOutputLines int
		want           string
	}{
		{"empty", "", 10, ""},
		{"fewer_lines_than_requested", "line1\nline2\n", 10, "line1\nline2\n"},
		{"exact_lines", "line1\nline2\nline3\n", 3, "line1\nline2\nline3\n"},
		{"last_two", "line1\nline2\nline3\n", 2, "line2\nline3\n"},
		{"last_one", "line1\nline2\nline3\n", 1, "line3\n"},
		{"five_lines_last_three", "a\nb\nc\nd\ne\n", 3, "c\nd\ne\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := string(findBuildFailure([]byte(tt.log), tt.numOutputLines))
			if got != tt.want {
				t.Errorf("findBuildFailure(%q, %d) = %q, want %q",
					tt.log, tt.numOutputLines, got, tt.want)
			}
		})
	}
}
