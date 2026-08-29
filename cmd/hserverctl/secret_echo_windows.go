//go:build windows

package main

import "golang.org/x/sys/windows"

func disableTerminalEcho(fd uintptr) (func() error, error) {
	handle := windows.Handle(fd)
	var original uint32
	if err := windows.GetConsoleMode(handle, &original); err != nil {
		return nil, err
	}
	hidden := original &^ windows.ENABLE_ECHO_INPUT
	if err := windows.SetConsoleMode(handle, hidden); err != nil {
		return nil, err
	}
	return func() error { return windows.SetConsoleMode(handle, original) }, nil
}
