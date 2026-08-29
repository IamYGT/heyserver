package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"unicode/utf8"
)

func editCLIText(ctx context.Context, content, requestedEditor, temporaryPattern string, maxBytes int, errOut io.Writer, getenv func(string) string) (string, bool, error) {
	if len(content) > maxBytes || !utf8.ValidString(content) || strings.IndexByte(content, 0) >= 0 {
		return "", false, errors.New("server returned content that is not valid bounded UTF-8 text")
	}
	editor, err := resolveCLIEditor(requestedEditor, getenv)
	if err != nil {
		return "", false, err
	}
	file, err := os.CreateTemp("", temporaryPattern)
	if err != nil {
		return "", false, fmt.Errorf("create editor file: %w", err)
	}
	path := file.Name()
	defer os.Remove(path)
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return "", false, fmt.Errorf("protect editor file: %w", err)
	}
	if _, err := io.WriteString(file, content); err != nil {
		file.Close()
		return "", false, fmt.Errorf("write editor file: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", false, fmt.Errorf("close editor file: %w", err)
	}

	command := exec.CommandContext(ctx, editor, path)
	command.Stdin = os.Stdin
	command.Stdout = errOut
	command.Stderr = errOut
	if err := command.Run(); err != nil {
		return "", false, fmt.Errorf("editor %q failed: %w", editor, err)
	}
	edited, err := readCLIManagedTextFile(path)
	if err != nil {
		return "", false, fmt.Errorf("read edited content: %w", err)
	}
	return edited, edited != content, nil
}

func resolveCLIEditor(requested string, getenv func(string) string) (string, error) {
	if editor := strings.TrimSpace(requested); editor != "" {
		if strings.IndexByte(editor, 0) >= 0 {
			return "", errors.New("editor executable must not contain NUL")
		}
		return editor, nil
	}
	if getenv != nil {
		for _, key := range []string{"HSERVER_EDITOR", "VISUAL", "EDITOR"} {
			if editor := strings.TrimSpace(getenv(key)); editor != "" {
				if strings.IndexByte(editor, 0) >= 0 {
					return "", fmt.Errorf("%s must not contain NUL", key)
				}
				return editor, nil
			}
		}
	}
	return "", errors.New("editor is not configured; use --editor or set HSERVER_EDITOR, VISUAL, or EDITOR")
}
