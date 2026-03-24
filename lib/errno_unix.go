//go:build !windows

package lib

import (
	"errors"

	"golang.org/x/sys/unix"
)

func isConnReset(err error) bool {
	return errors.Is(err, unix.ECONNRESET)
}

func isConnRefused(err error) bool {
	return errors.Is(err, unix.ECONNREFUSED)
}
