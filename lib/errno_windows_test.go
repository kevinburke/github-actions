//go:build windows

package lib

import (
	"net"
	"net/url"

	"golang.org/x/sys/windows"
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
		{"wsaeconnreset", windows.WSAECONNRESET, true},
		{"wsaeconnreset wrapped in OpError", &net.OpError{
			Op:  "read",
			Net: "tcp",
			Err: windows.WSAECONNRESET,
		}, true},
		{"wsaeconnreset wrapped in url.Error", &url.Error{
			Op:  "Get",
			URL: "https://api.github.com/repos/foo/bar",
			Err: &net.OpError{
				Op:  "read",
				Net: "tcp",
				Err: windows.WSAECONNRESET,
			},
		}, true},
		{"wsaeconnrefused", windows.WSAECONNREFUSED, true},
	}
}
