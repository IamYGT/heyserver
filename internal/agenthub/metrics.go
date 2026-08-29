package agenthub

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"time"
)

const (
	MetricsSnapshotMaxAge     = 2 * time.Minute
	MetricsSnapshotFutureSkew = 30 * time.Second
	MetricsUnavailableError   = "metrics_unavailable"
	maxMetricsSnapshotBytes   = 8 << 10
)

// DecodeMetricsSnapshot accepts exactly one bounded JSON object and rejects
// unknown fields so the hub never forwards an untyped agent result.
func DecodeMetricsSnapshot(data []byte) (MetricsSnapshot, error) {
	var snapshot MetricsSnapshot
	if len(data) == 0 || len(data) > maxMetricsSnapshotBytes {
		return snapshot, fmt.Errorf("agent hub: metrics snapshot size: %w", ErrInvalidInput)
	}
	if err := validateMetricsJSONShape(data); err != nil {
		return snapshot, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&snapshot); err != nil {
		return MetricsSnapshot{}, fmt.Errorf("agent hub: decode metrics snapshot: %w", ErrInvalidInput)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return MetricsSnapshot{}, fmt.Errorf("agent hub: trailing metrics snapshot data: %w", ErrInvalidInput)
	}
	return snapshot, nil
}

func validateMetricsJSONShape(data []byte) error {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("agent hub: decode metrics snapshot: %w", ErrInvalidInput)
	}
	objects := map[string][]string{
		"cpu":       {"usage_percent", "core_count"},
		"load":      {"one", "five", "fifteen"},
		"memory":    {"total_bytes", "used_bytes", "available_bytes", "usage_percent"},
		"network":   {"rx_bytes", "tx_bytes"},
		"root_disk": {"total_bytes", "used_bytes", "available_bytes", "usage_percent"},
	}
	if len(root) != len(objects)+1 {
		return fmt.Errorf("agent hub: metrics snapshot fields are invalid: %w", ErrInvalidInput)
	}
	if _, ok := root["observed_at"]; !ok {
		return fmt.Errorf("agent hub: metrics observed_at is required: %w", ErrInvalidInput)
	}
	for name, fields := range objects {
		raw, ok := root[name]
		if !ok {
			return fmt.Errorf("agent hub: metrics %s is required: %w", name, ErrInvalidInput)
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal(raw, &object); err != nil || len(object) != len(fields) {
			return fmt.Errorf("agent hub: metrics %s fields are invalid: %w", name, ErrInvalidInput)
		}
		for _, field := range fields {
			if _, ok := object[field]; !ok {
				return fmt.Errorf("agent hub: metrics %s.%s is required: %w", name, field, ErrInvalidInput)
			}
		}
	}
	return nil
}

// ValidateMetricsSnapshot bounds every observed metric and requires a recent
// UTC observation. Percentages must agree with their byte counters.
func ValidateMetricsSnapshot(snapshot MetricsSnapshot, now time.Time) error {
	now = now.UTC()
	if snapshot.ObservedAt.IsZero() {
		return fmt.Errorf("agent hub: metrics observed_at is required: %w", ErrInvalidInput)
	}
	_, offset := snapshot.ObservedAt.Zone()
	if offset != 0 || snapshot.ObservedAt.Before(now.Add(-MetricsSnapshotMaxAge)) || snapshot.ObservedAt.After(now.Add(MetricsSnapshotFutureSkew)) {
		return fmt.Errorf("agent hub: metrics observed_at is stale: %w", ErrInvalidInput)
	}
	if snapshot.CPU.CoreCount < 1 || snapshot.CPU.CoreCount > 65536 || !validMetricsPercent(snapshot.CPU.UsagePercent) {
		return fmt.Errorf("agent hub: metrics cpu is invalid: %w", ErrInvalidInput)
	}
	if !validMetricsLoad(snapshot.Load.One) || !validMetricsLoad(snapshot.Load.Five) || !validMetricsLoad(snapshot.Load.Fifteen) {
		return fmt.Errorf("agent hub: metrics load is invalid: %w", ErrInvalidInput)
	}
	if snapshot.Memory.TotalBytes == 0 || snapshot.Memory.AvailableBytes > snapshot.Memory.TotalBytes ||
		snapshot.Memory.UsedBytes != snapshot.Memory.TotalBytes-snapshot.Memory.AvailableBytes ||
		!validMetricsPercent(snapshot.Memory.UsagePercent) ||
		!closeMetricsPercent(snapshot.Memory.UsagePercent, float64(snapshot.Memory.UsedBytes)/float64(snapshot.Memory.TotalBytes)*100) {
		return fmt.Errorf("agent hub: metrics memory is invalid: %w", ErrInvalidInput)
	}
	if snapshot.RootDisk.TotalBytes == 0 || snapshot.RootDisk.UsedBytes > snapshot.RootDisk.TotalBytes ||
		snapshot.RootDisk.AvailableBytes > snapshot.RootDisk.TotalBytes ||
		snapshot.RootDisk.UsedBytes > snapshot.RootDisk.TotalBytes-snapshot.RootDisk.AvailableBytes ||
		!validMetricsPercent(snapshot.RootDisk.UsagePercent) {
		return fmt.Errorf("agent hub: metrics root disk is invalid: %w", ErrInvalidInput)
	}
	diskDenominator := snapshot.RootDisk.UsedBytes + snapshot.RootDisk.AvailableBytes
	wantDiskPercent := 0.0
	if diskDenominator > 0 {
		wantDiskPercent = float64(snapshot.RootDisk.UsedBytes) / float64(diskDenominator) * 100
	}
	if !closeMetricsPercent(snapshot.RootDisk.UsagePercent, wantDiskPercent) {
		return fmt.Errorf("agent hub: metrics root disk percentage is invalid: %w", ErrInvalidInput)
	}
	return nil
}

// ValidateMetricsTaskResult applies the fixed task result contract. It keeps
// failures generic and completed results strictly typed.
func ValidateMetricsTaskResult(req TaskResultRequest, now time.Time) error {
	switch req.Status {
	case TaskStatusCompleted:
		if req.Error != "" || len(req.Result) != 1 {
			return fmt.Errorf("agent hub: metrics.read completed result is invalid: %w", ErrInvalidInput)
		}
		data, ok := req.Result["data"]
		if !ok {
			return fmt.Errorf("agent hub: metrics.read result data is required: %w", ErrInvalidInput)
		}
		snapshot, err := DecodeMetricsSnapshot([]byte(data))
		if err != nil {
			return err
		}
		return ValidateMetricsSnapshot(snapshot, now)
	case TaskStatusFailed:
		if req.Error != MetricsUnavailableError || len(req.Result) != 0 {
			return fmt.Errorf("agent hub: metrics.read failed result is invalid: %w", ErrInvalidInput)
		}
		return nil
	default:
		return fmt.Errorf("agent hub: metrics.read result status is invalid: %w", ErrInvalidInput)
	}
}

func validMetricsPercent(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= 100
}

func validMetricsLoad(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= 1_000_000
}

func closeMetricsPercent(got, want float64) bool {
	return math.Abs(got-want) <= 0.01
}
