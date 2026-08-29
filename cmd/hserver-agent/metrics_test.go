package main

import (
	"context"
	"encoding/json"
	"fmt"
	"syscall"
	"testing"
	"time"

	"github.com/IamYGT/heyserver/internal/agenthub"
)

func TestMetricsCollectorReturnsStrictProviderNeutralSnapshot(t *testing.T) {
	now := time.Date(2026, time.August, 29, 12, 0, 0, 123, time.UTC)
	cpuReads := 0
	collector := metricsCollector{
		readFile: func(path string) ([]byte, error) {
			switch path {
			case "/proc/stat":
				cpuReads++
				if cpuReads == 1 {
					return []byte("cpu  100 0 200 650 50 0 0 0\n"), nil
				}
				return []byte("cpu  150 0 230 670 50 0 0 0\n"), nil
			case "/proc/loadavg":
				return []byte("1.25 0.75 0.50 1/100 42\n"), nil
			case "/proc/meminfo":
				return []byte("MemTotal: 1000 kB\nMemAvailable: 250 kB\n"), nil
			case "/proc/net/dev":
				return []byte("Inter-| Receive | Transmit\n eth0: 100 1 2 3 4 5 6 7 200 9 10 11 12 13 14 15\n lo: 10 1 2 3 4 5 6 7 20 9 10 11 12 13 14 15\n"), nil
			default:
				return nil, fmt.Errorf("unexpected metrics path %s", path)
			}
		},
		statfs: func(path string, stat *syscall.Statfs_t) error {
			if path != "/" {
				t.Fatalf("statfs path = %q", path)
			}
			*stat = syscall.Statfs_t{Blocks: 1000, Bfree: 400, Bavail: 400, Bsize: 4096}
			return nil
		},
		coreCount: func() int { return 8 }, now: func() time.Time { return now }, sampleDelay: 0,
	}
	snapshot, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if snapshot.ObservedAt != now || snapshot.CPU.UsagePercent != 80 || snapshot.CPU.CoreCount != 8 {
		t.Fatalf("CPU snapshot = %#v", snapshot)
	}
	if snapshot.Load.One != 1.25 || snapshot.Load.Five != .75 || snapshot.Load.Fifteen != .5 {
		t.Fatalf("load snapshot = %#v", snapshot.Load)
	}
	if snapshot.Memory.TotalBytes != 1_024_000 || snapshot.Memory.UsedBytes != 768_000 || snapshot.Memory.AvailableBytes != 256_000 || snapshot.Memory.UsagePercent != 75 {
		t.Fatalf("memory snapshot = %#v", snapshot.Memory)
	}
	if snapshot.Network.RXBytes != 110 || snapshot.Network.TXBytes != 220 {
		t.Fatalf("network snapshot = %#v", snapshot.Network)
	}
	if snapshot.RootDisk.TotalBytes != 4_096_000 || snapshot.RootDisk.UsedBytes != 2_457_600 || snapshot.RootDisk.AvailableBytes != 1_638_400 || snapshot.RootDisk.UsagePercent != 60 {
		t.Fatalf("root disk snapshot = %#v", snapshot.RootDisk)
	}
}

func TestMetricsTaskExecutorRequiresFixedEmptyTask(t *testing.T) {
	reader := stubMetricsReader{snapshot: validAgentMetricsSnapshot()}
	executor := taskExecutor{metrics: reader}
	result := executor.execute(context.Background(), &agenthub.Task{Kind: agenthub.TaskMetricsRead, Payload: map[string]string{}})
	if result.Status != agenthub.TaskStatusCompleted || len(result.Result) != 1 {
		t.Fatalf("metrics result = %#v", result)
	}
	var decoded agenthub.MetricsSnapshot
	if err := json.Unmarshal([]byte(result.Result["data"]), &decoded); err != nil || decoded.CPU.CoreCount != 4 {
		t.Fatalf("typed metrics data = %#v, err=%v", decoded, err)
	}

	for _, task := range []*agenthub.Task{
		{Kind: agenthub.TaskMetricsRead, Payload: map[string]string{"path": "/proc/secret"}},
		{Kind: agenthub.TaskMetricsRead, Payload: map[string]string{}},
	} {
		candidate := executor
		if len(task.Payload) == 0 {
			candidate.metrics = nil
		}
		failed := candidate.execute(context.Background(), task)
		if failed.Status != agenthub.TaskStatusFailed || failed.Error != agenthub.MetricsUnavailableError || len(failed.Result) != 0 {
			t.Fatalf("closed metrics failure = %#v", failed)
		}
	}
}

func TestParseCPUCountersDoesNotDoubleCountGuestTime(t *testing.T) {
	counters, err := parseCPUCounters([]byte("cpu 100 20 30 400 10 5 6 7 80 9\n"))
	if err != nil {
		t.Fatalf("parseCPUCounters: %v", err)
	}
	if counters.total != 578 || counters.idle != 410 {
		t.Fatalf("CPU counters = %#v", counters)
	}
}

type stubMetricsReader struct {
	snapshot agenthub.MetricsSnapshot
	err      error
}

func (s stubMetricsReader) Collect(context.Context) (agenthub.MetricsSnapshot, error) {
	return s.snapshot, s.err
}

func validAgentMetricsSnapshot() agenthub.MetricsSnapshot {
	return agenthub.MetricsSnapshot{
		ObservedAt: time.Now().UTC(),
		CPU:        agenthub.MetricsCPU{UsagePercent: 12.5, CoreCount: 4},
		Load:       agenthub.MetricsLoad{One: .1, Five: .2, Fifteen: .3},
		Memory:     agenthub.MetricsMemory{TotalBytes: 1000, UsedBytes: 600, AvailableBytes: 400, UsagePercent: 60},
		Network:    agenthub.MetricsNetwork{RXBytes: 123, TXBytes: 456},
		RootDisk:   agenthub.MetricsFilesystem{TotalBytes: 1000, UsedBytes: 500, AvailableBytes: 500, UsagePercent: 50},
	}
}
