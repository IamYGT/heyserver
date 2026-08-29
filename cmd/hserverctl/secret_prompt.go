package main

import (
	"errors"
	"fmt"
	"io"
	"strings"

	xterm "github.com/charmbracelet/x/term"
)

type fileDescriptorReader interface {
	io.Reader
	Fd() uintptr
}

func readInteractiveSecret(input io.Reader, promptOut io.Writer, prompt, fileFlag string, maxBytes int64) (string, error) {
	terminal, ok := input.(fileDescriptorReader)
	if !ok || !xterm.IsTerminal(terminal.Fd()) {
		return "", fmt.Errorf("secret input requires an interactive TTY; use %s for automation", fileFlag)
	}
	if promptOut == nil {
		promptOut = io.Discard
	}
	restoreEcho, err := disableTerminalEcho(terminal.Fd())
	if err != nil {
		return "", fmt.Errorf("disable terminal echo: %w", err)
	}
	restored := false
	restore := func() error {
		if restored {
			return nil
		}
		if err := restoreEcho(); err != nil {
			return err
		}
		restored = true
		return nil
	}
	defer func() {
		_ = restore()
	}()
	// Echo must already be disabled when the prompt becomes visible. Printing
	// first and then calling ReadPassword leaves a real window where fast PTY
	// input can be echoed into logs or transcripts.
	if _, err := fmt.Fprint(promptOut, prompt); err != nil {
		return "", fmt.Errorf("write secret prompt: %w", err)
	}
	data, readErr := xterm.ReadPassword(terminal.Fd())
	restoreErr := restore()
	_, newlineErr := fmt.Fprintln(promptOut)
	if readErr != nil {
		if restoreErr != nil {
			return "", fmt.Errorf("read secret from terminal: %v (restore terminal echo: %w)", readErr, restoreErr)
		}
		return "", fmt.Errorf("read secret from terminal: %w", readErr)
	}
	if restoreErr != nil {
		return "", fmt.Errorf("restore terminal echo: %w", restoreErr)
	}
	if newlineErr != nil {
		return "", fmt.Errorf("finish secret prompt: %w", newlineErr)
	}
	if int64(len(data)) > maxBytes {
		return "", fmt.Errorf("secret input exceeds %d bytes", maxBytes)
	}
	value := strings.TrimRight(string(data), "\r\n")
	if value == "" {
		return "", errors.New("secret input is empty")
	}
	return value, nil
}
