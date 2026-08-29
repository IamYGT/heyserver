package api

import (
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strings"
)

// isLoopbackRequest returns true when the TCP connection originates from localhost.
// X-Forwarded-For is intentionally ignored for internal cron endpoints.
func isLoopbackRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	host = strings.Trim(host, "[]")
	return host == "127.0.0.1" || host == "::1"
}

// resolvePathUnderBase resolves symlinks and ensures resolved path stays under baseDir.
func resolvePathUnderBase(baseDir, filePath string) (string, error) {
	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return "", err
	}
	absFile, err := filepath.Abs(filePath)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absFile)
	if err != nil {
		return "", fmt.Errorf("cannot resolve backup path: %w", err)
	}
	rel, err := filepath.Rel(absBase, resolved)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("backup path escapes backup directory")
	}
	return resolved, nil
}
