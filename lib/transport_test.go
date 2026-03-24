package lib

import (
	"errors"
	"io"
	"net"
	"testing"
)

func TestIsRetryableError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"generic", errors.New("something"), false},

		{"unexpected eof", io.ErrUnexpectedEOF, true},
		{"eof", io.EOF, true},

		{"dns error", &net.DNSError{Err: "no such host", Name: "example.com"}, true},

		{"dial OpError", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("dial failed")}, true},
		{"read OpError", &net.OpError{Op: "read", Net: "tcp", Err: errors.New("read failed")}, true},

		// GitHub API errors should not be retried.
		{"github api error", &Error{StatusCode: 404, Message: "Not Found"}, false},
	}
	// Append platform-specific errno test cases.
	tests = append(tests, connResetTestCases()...)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsRetryableError(tt.err)
			if got != tt.want {
				t.Errorf("IsRetryableError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
