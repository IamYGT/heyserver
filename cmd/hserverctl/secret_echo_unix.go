//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris || zos

package main

import (
	xterm "github.com/charmbracelet/x/term"
	"golang.org/x/sys/unix"
)

// disableTerminalEcho changes only echo flags. Canonical input, signals and
// CR-to-NL translation remain active while the prompt is published, avoiding
// both an echo race and a raw-input race.
func disableTerminalEcho(fd uintptr) (func() error, error) {
	original, err := xterm.GetState(fd)
	if err != nil {
		return nil, err
	}
	hidden, err := xterm.GetState(fd)
	if err != nil {
		return nil, err
	}
	hidden.Termios.Lflag &^= unix.ECHO | unix.ECHONL
	if err := xterm.SetState(fd, hidden); err != nil {
		return nil, err
	}
	return func() error { return xterm.Restore(fd, original) }, nil
}
