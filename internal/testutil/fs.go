package testutil

import (
	"io/fs"
	"testing"
	"testing/fstest"
)

// MinimalWebFS returns a tiny embedded FS that satisfies SPA static serving in tests.
func MinimalWebFS(t *testing.T) fs.FS {
	t.Helper()
	return fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<!doctype html><html><body>hserver</body></html>")},
	}
}
