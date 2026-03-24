//go:build !windows

package lib

import (
	"net"
	"net/url"

	"golang.org/x/sys/unix"
)

func connResetTestCases() []struct {
	name string
	err  error
	want bool
} {
	return []struct {
		name string
		err  error
		want bool
	}{
		// Connection reset by peer — the original bug.
		{"econnreset", unix.ECONNRESET, true},
		{"econnreset wrapped in OpError", &net.OpError{
			Op:  "read",
			Net: "tcp",
			Err: unix.ECONNRESET,
		}, true},
		{"econnreset wrapped in url.Error", &url.Error{
			Op:  "Get",
			URL: "https://api.github.com/repos/foo/bar",
			Err: &net.OpError{
				Op:  "read",
				Net: "tcp",
				Err: unix.ECONNRESET,
			},
		}, true},
		{"econnrefused", unix.ECONNREFUSED, true},
	}
}
