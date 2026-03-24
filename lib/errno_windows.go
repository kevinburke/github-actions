//go:build windows

package lib

import (
	"errors"

	"golang.org/x/sys/windows"
)

func isConnReset(err error) bool {
	return errors.Is(err, windows.WSAECONNRESET)
}

func isConnRefused(err error) bool {
	return errors.Is(err, windows.WSAECONNREFUSED)
}
