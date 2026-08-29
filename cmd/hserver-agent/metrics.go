package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/IamYGT/heyserver/internal/agenthub"
)

const metricsCPUSampleInterval = 100 * time.Millisecond

type metricsCollector struct {
	readFile    func(string) ([]byte, error)
	statfs      func(string, *syscall.Statfs_t) error
	coreCount   func() int
	now         func() time.Time
	sampleDelay time.Duration
}

type cpuCounters struct {
	total uint64
	idle  uint64
}

func newMetricsCollector() metricsCollector {
	return metricsCollector{
		readFile: os.ReadFile, statfs: syscall.Statfs, coreCount: runtime.NumCPU,
		now: time.Now, sampleDelay: metricsCPUSampleInterval,
	}
}

func (c metricsCollector) Collect(ctx context.Context) (agenthub.MetricsSnapshot, error) {
	if c.readFile == nil || c.statfs == nil || c.coreCount == nil || c.now == nil {
		return agenthub.MetricsSnapshot{}, errors.New("metrics collector is unavailable")
	}
	firstCPUData, err := c.readBounded("/proc/stat")
	if err != nil {
		return agenthub.MetricsSnapshot{}, err
	}
	firstCPU, err := parseCPUCounters(firstCPUData)
	if err != nil {
		return agenthub.MetricsSnapshot{}, err
	}
	if c.sampleDelay > 0 {
		timer := time.NewTimer(c.sampleDelay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return agenthub.MetricsSnapshot{}, ctx.Err()
		case <-timer.C:
		}
	}
	secondCPUData, err := c.readBounded("/proc/stat")
	if err != nil {
		return agenthub.MetricsSnapshot{}, err
	}
	secondCPU, err := parseCPUCounters(secondCPUData)
	if err != nil {
		return agenthub.MetricsSnapshot{}, err
	}
	cpuPercent, err := cpuUsagePercent(firstCPU, secondCPU)
	if err != nil {
		return agenthub.MetricsSnapshot{}, err
	}

	loadData, err := c.readBounded("/proc/loadavg")
	if err != nil {
		return agenthub.MetricsSnapshot{}, err
	}
	load, err := parseMetricsLoad(loadData)
	if err != nil {
		return agenthub.MetricsSnapshot{}, err
	}
	memData, err := c.readBounded("/proc/meminfo")
	if err != nil {
		return agenthub.MetricsSnapshot{}, err
	}
	memory, err := parseMetricsMemory(memData)
	if err != nil {
		return agenthub.MetricsSnapshot{}, err
	}
	networkData, err := c.readBounded("/proc/net/dev")
	if err != nil {
		return agenthub.MetricsSnapshot{}, err
	}
	network, err := parseMetricsNetwork(networkData)
	if err != nil {
		return agenthub.MetricsSnapshot{}, err
	}
	var stat syscall.Statfs_t
	if err := c.statfs("/", &stat); err != nil {
		return agenthub.MetricsSnapshot{}, fmt.Errorf("read root filesystem metrics: %w", err)
	}
	diskTotal, diskUsed, diskAvailable, _ := filesystemUsage(stat)
	if diskTotal == 0 {
		return agenthub.MetricsSnapshot{}, errors.New("root filesystem metrics are unavailable")
	}
	diskPercent := 0.0
	if denominator := diskUsed + diskAvailable; denominator > 0 {
		diskPercent = float64(diskUsed) / float64(denominator) * 100
	}
	cores := c.coreCount()
	if cores < 1 {
		return agenthub.MetricsSnapshot{}, errors.New("CPU core count is unavailable")
	}

	snapshot := agenthub.MetricsSnapshot{
		ObservedAt: c.now().UTC(),
		CPU:        agenthub.MetricsCPU{UsagePercent: cpuPercent, CoreCount: cores},
		Load:       load,
		Memory:     memory,
		Network:    network,
		RootDisk: agenthub.MetricsFilesystem{
			TotalBytes: diskTotal, UsedBytes: diskUsed, AvailableBytes: diskAvailable, UsagePercent: diskPercent,
		},
	}
	if err := agenthub.ValidateMetricsSnapshot(snapshot, snapshot.ObservedAt); err != nil {
		return agenthub.MetricsSnapshot{}, err
	}
	return snapshot, nil
}

func (c metricsCollector) readBounded(path string) ([]byte, error) {
	data, err := c.readFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || len(data) > maxLocalFileBytes {
		return nil, errors.New("metrics source exceeds its size boundary")
	}
	return data, nil
}

func parseCPUCounters(data []byte) (cpuCounters, error) {
	line, _, _ := strings.Cut(string(data), "\n")
	fields := strings.Fields(line)
	if len(fields) < 5 || fields[0] != "cpu" {
		return cpuCounters{}, errors.New("invalid aggregate CPU counters")
	}
	var counters []uint64
	for _, field := range fields[1:] {
		value, err := strconv.ParseUint(field, 10, 64)
		if err != nil {
			return cpuCounters{}, errors.New("invalid aggregate CPU counter")
		}
		counters = append(counters, value)
	}
	// Linux reports guest and guest_nice after steal, but those times are
	// already included in user and nice. Sum only user through steal to avoid
	// double-counting CPU time on hosts that run guests.
	totalFields := len(counters)
	if totalFields > 8 {
		totalFields = 8
	}
	var total uint64
	for _, value := range counters[:totalFields] {
		if math.MaxUint64-total < value {
			return cpuCounters{}, errors.New("aggregate CPU counters overflow")
		}
		total += value
	}
	idle := counters[3]
	if len(counters) > 4 {
		if math.MaxUint64-idle < counters[4] {
			return cpuCounters{}, errors.New("aggregate CPU idle counters overflow")
		}
		idle += counters[4]
	}
	return cpuCounters{total: total, idle: idle}, nil
}

func cpuUsagePercent(first, second cpuCounters) (float64, error) {
	if second.total <= first.total || second.idle < first.idle {
		return 0, errors.New("aggregate CPU counters did not advance")
	}
	totalDelta := second.total - first.total
	idleDelta := second.idle - first.idle
	if idleDelta > totalDelta {
		return 0, errors.New("aggregate CPU idle counters are inconsistent")
	}
	return float64(totalDelta-idleDelta) / float64(totalDelta) * 100, nil
}

func parseMetricsLoad(data []byte) (agenthub.MetricsLoad, error) {
	fields := strings.Fields(string(data))
	if len(fields) < 3 {
		return agenthub.MetricsLoad{}, errors.New("invalid load averages")
	}
	values := make([]float64, 3)
	for i := range values {
		value, err := strconv.ParseFloat(fields[i], 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return agenthub.MetricsLoad{}, errors.New("invalid load average")
		}
		values[i] = value
	}
	return agenthub.MetricsLoad{One: values[0], Five: values[1], Fifteen: values[2]}, nil
}

func parseMetricsMemory(data []byte) (agenthub.MetricsMemory, error) {
	total, available, _, _ := parseMeminfo(data)
	if total == 0 || available > total {
		return agenthub.MetricsMemory{}, errors.New("invalid memory metrics")
	}
	used := total - available
	return agenthub.MetricsMemory{
		TotalBytes: total, UsedBytes: used, AvailableBytes: available,
		UsagePercent: float64(used) / float64(total) * 100,
	}, nil
}

func parseMetricsNetwork(data []byte) (agenthub.MetricsNetwork, error) {
	var network agenthub.MetricsNetwork
	found := false
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		name, raw, ok := strings.Cut(scanner.Text(), ":")
		if !ok || strings.TrimSpace(name) == "" {
			continue
		}
		fields := strings.Fields(raw)
		if len(fields) != 16 {
			return agenthub.MetricsNetwork{}, errors.New("invalid network metrics")
		}
		rx, rxErr := strconv.ParseUint(fields[0], 10, 64)
		tx, txErr := strconv.ParseUint(fields[8], 10, 64)
		if rxErr != nil || txErr != nil || math.MaxUint64-network.RXBytes < rx || math.MaxUint64-network.TXBytes < tx {
			return agenthub.MetricsNetwork{}, errors.New("invalid network byte counters")
		}
		network.RXBytes += rx
		network.TXBytes += tx
		found = true
	}
	if err := scanner.Err(); err != nil {
		return agenthub.MetricsNetwork{}, err
	}
	if !found {
		return agenthub.MetricsNetwork{}, errors.New("network metrics are unavailable")
	}
	return network, nil
}
