//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris && !windows && !zos

package main

import xterm "github.com/charmbracelet/x/term"

func disableTerminalEcho(fd uintptr) (func() error, error) {
	original, err := xterm.MakeRaw(fd)
	if err != nil {
		return nil, err
	}
	return func() error { return xterm.Restore(fd, original) }, nil
}
