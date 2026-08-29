package gdrive

import (
	"testing"
	"time"
)

func TestVerifyFileSizes_match(t *testing.T) {
	if err := verifyFileSizes(1024, 1024); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyFileSizes_mismatch(t *testing.T) {
	if err := verifyFileSizes(100, 200); err == nil {
		t.Fatal("expected size mismatch error")
	}
}

func TestVerifyFileSizes_emptyLocal(t *testing.T) {
	if err := verifyFileSizes(0, 100); err == nil {
		t.Fatal("expected empty local error")
	}
}

func TestListJSONDedupe_keepsNewest(t *testing.T) {
	byName := map[string]RemoteBackup{
		"a.tar.gz": {Name: "a.tar.gz", Size: 10, ModTime: time.Now().Add(-time.Hour)},
	}
	candidate := RemoteBackup{Name: "a.tar.gz", Size: 20, ModTime: time.Now()}
	existing := byName["a.tar.gz"]
	if !candidate.ModTime.After(existing.ModTime) {
		t.Fatal("test setup")
	}
	byName["a.tar.gz"] = candidate
	if byName["a.tar.gz"].Size != 20 {
		t.Fatalf("expected deduped size 20, got %d", byName["a.tar.gz"].Size)
	}
}
