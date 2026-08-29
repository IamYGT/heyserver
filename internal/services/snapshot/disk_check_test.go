package snapshot

import "testing"

func TestRootDiskInfo_readable(t *testing.T) {
	info, err := getRootDiskInfo()
	if err != nil {
		t.Fatal(err)
	}
	if info.UsedPercent < 0 || info.UsedPercent > 100 {
		t.Fatalf("unexpected used percent: %d", info.UsedPercent)
	}
	if info.FreeBytes == 0 {
		t.Fatal("expected positive free bytes")
	}
}
