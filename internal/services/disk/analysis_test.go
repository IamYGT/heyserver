package disk

import (
	"os/exec"
	"strings"
	"testing"
)

func TestDiskAnalysisWorkerSyntaxAndBoundaries(t *testing.T) {
	cmd := exec.Command("/usr/bin/python3", "-c", `import sys; compile(sys.stdin.read(), "disk-analysis-worker", "exec")`)
	cmd.Stdin = strings.NewReader(diskAnalysisWorkerSource)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("worker syntax: %v: %s", err, output)
	}
	for _, expected := range []string{
		`roots = ["/var/lib", "/var/www", "/opt", "/root"]`,
		`"--max-depth=2"`, `"--threshold=104857600"`, `timeout=900`, `rows[:100]`,
	} {
		if !strings.Contains(diskAnalysisWorkerSource, expected) {
			t.Fatalf("worker missing boundary %q", expected)
		}
	}
}

func TestAnalysisIDFormat(t *testing.T) {
	id, err := analysisID()
	if err != nil || len(id) != len("disk-")+12 || !strings.HasPrefix(id, "disk-") {
		t.Fatalf("analysisID=%q err=%v", id, err)
	}
}
