package snapshot

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// parseResticStatusLine extracts backup progress from restic --json status lines.
// Maps percent_done (0–1) into job progress 35–90 (files phase band).
func parseResticStatusLine(line string) (progress int, bytesDone, bytesTotal int64, ok bool) {
	line = strings.TrimSpace(line)
	if !strings.Contains(line, `"message_type":"status"`) {
		return 0, 0, 0, false
	}
	idx := strings.Index(line, "{")
	if idx < 0 {
		return 0, 0, 0, false
	}
	var raw struct {
		PercentDone     float64 `json:"percent_done"`
		BytesDone       int64   `json:"bytes_done"`
		TotalBytes      int64   `json:"total_bytes"`
		SecondsElapsed  float64 `json:"seconds_elapsed"`
		SecondsRemaining float64 `json:"seconds_remaining"`
	}
	if err := json.Unmarshal([]byte(line[idx:]), &raw); err != nil {
		return 0, 0, 0, false
	}
	pct := 35 + int(raw.PercentDone*55)
	if pct < 35 {
		pct = 35
	}
	if pct > 90 {
		pct = 90
	}
	return pct, raw.BytesDone, raw.TotalBytes, true
}

// resticUploadSpeed estimates throughput from consecutive status lines.
func resticUploadSpeed(prevBytes int64, prevAt time.Time, bytesDone int64) string {
	if prevAt.IsZero() || bytesDone <= prevBytes {
		return ""
	}
	dt := time.Since(prevAt).Seconds()
	if dt <= 0 {
		return ""
	}
	bps := float64(bytesDone-prevBytes) / dt
	if bps < 1024 {
		return ""
	}
	const unit = 1024.0
	if bps < unit*unit {
		return fmt.Sprintf("%.1f KB/s", bps/unit)
	}
	if bps < unit*unit*unit {
		return fmt.Sprintf("%.1f MB/s", bps/(unit*unit))
	}
	return fmt.Sprintf("%.2f GB/s", bps/(unit*unit*unit))
}

func resticLogWorthy(line string) bool {
	if strings.Contains(line, `"message_type":"status"`) {
		return false
	}
	if strings.Contains(line, `"message_type":"verbose_status"`) {
		return false
	}
	return strings.TrimSpace(line) != ""
}
