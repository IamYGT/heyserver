package gdrive

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriteConfig_includesDriveTuning(t *testing.T) {
	dir := t.TempDir()
	r := newRcloneRunner(dir, "rclone")
	td := &tokenData{
		AccessToken:  "at",
		RefreshToken: "rt",
		TokenType:    "Bearer",
		Expiry:       time.Now().Add(time.Hour),
	}
	if err := r.writeConfig(td); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, rcloneConfName))
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	for _, want := range []string{"chunk_size = 64Mi", "pacer_burst = 75", "fast_list = true", "use_trash = false"} {
		if !strings.Contains(s, want) {
			t.Fatalf("rclone.conf missing %q:\n%s", want, s)
		}
	}
}
