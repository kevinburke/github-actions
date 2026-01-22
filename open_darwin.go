//go:build darwin

package main

import (
	"os"
	"os/exec"
)

func openURL(url string) error {
	cmd := exec.Command("open", url)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
