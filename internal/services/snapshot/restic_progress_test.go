package snapshot

import "testing"

func TestParseResticStatusLine(t *testing.T) {
	line := `{"message_type":"status","percent_done":0.72,"bytes_done":41901889407,"total_bytes":58134534582}`
	pct, bd, bt, ok := parseResticStatusLine(line)
	if !ok || pct < 70 || pct > 75 || bd == 0 || bt == 0 {
		t.Fatalf("parse failed: ok=%v pct=%d bd=%d bt=%d", ok, pct, bd, bt)
	}
}

func TestResticLogWorthy_filtersNoise(t *testing.T) {
	if resticLogWorthy(`{"message_type":"verbose_status","action":"new"}`) {
		t.Fatal("verbose_status should be filtered")
	}
	if !resticLogWorthy("Fatal: something broke") {
		t.Fatal("fatal lines should be kept")
	}
}
