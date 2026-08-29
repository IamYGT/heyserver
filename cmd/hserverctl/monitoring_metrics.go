package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	metricsHistoryEndpoint           = "/api/metrics/history"
	metricsProcessesEndpoint         = "/api/metrics/processes"
	metricsProcessTimestampsEndpoint = "/api/metrics/processes/timestamps"
	metricsServicesHistoryEndpoint   = "/api/metrics/services/history"
	metricsSummaryEndpoint           = "/api/metrics/summary"
	monitoringProcessesEndpoint      = "/api/monitoring/processes"
	monitoringStatsEndpoint          = "/api/monitoring/stats"
)

const managedMonitoringMetricsEndpoint = "/api/nodes/%s/metrics"

var cliMetricsRanges = []string{"1h", "6h", "24h", "7d", "30d"}

// cliMetricRow and cliAggregatedMetricRow mirror the two data resolutions
// returned by GET /api/metrics/history. They intentionally contain only the
// fields in the public response contract; the CLI never derives or invents
// additional metric values.
type cliMetricRow struct {
	Timestamp       string  `json:"timestamp"`
	CPUPercent      float64 `json:"cpu_percent"`
	MemoryTotal     uint64  `json:"memory_total"`
	MemoryUsed      uint64  `json:"memory_used"`
	MemoryPercent   float64 `json:"memory_percent"`
	MemoryBuffers   uint64  `json:"memory_buffers"`
	MemoryCached    uint64  `json:"memory_cached"`
	MemoryAvailable uint64  `json:"memory_available"`
	SwapTotal       uint64  `json:"swap_total"`
	SwapUsed        uint64  `json:"swap_used"`
	SwapPercent     float64 `json:"swap_percent"`
	Load1M          float64 `json:"load_1m"`
	Load5M          float64 `json:"load_5m"`
	Load15M         float64 `json:"load_15m"`
	NetRXBytes      uint64  `json:"net_rx_bytes"`
	NetTXBytes      uint64  `json:"net_tx_bytes"`
	DiskRootPercent float64 `json:"disk_root_percent"`
}

type cliAggregatedMetricRow struct {
	Bucket      string  `json:"bucket"`
	SampleCount int     `json:"sample_count"`
	CPUAvg      float64 `json:"cpu_avg"`
	CPUMax      float64 `json:"cpu_max"`
	MemoryAvg   float64 `json:"mem_avg"`
	MemoryMax   float64 `json:"mem_max"`
	SwapAvg     float64 `json:"swap_avg"`
	SwapMax     float64 `json:"swap_max"`
	Load1MAvg   float64 `json:"load_1m_avg"`
	NetRXTotal  uint64  `json:"net_rx_total"`
	NetTXTotal  uint64  `json:"net_tx_total"`
	DiskRootAvg float64 `json:"disk_root_avg"`
	DiskRootMax float64 `json:"disk_root_max"`
}

type cliMetricsHistoryResponse struct {
	Range      string          `json:"range"`
	Resolution string          `json:"resolution"`
	Data       json.RawMessage `json:"data"`
}

type cliProcessSnapshotRow struct {
	Timestamp     string  `json:"timestamp"`
	PID           int     `json:"pid"`
	Username      string  `json:"username"`
	CPUPercent    float64 `json:"cpu_percent"`
	MemoryPercent float64 `json:"memory_percent"`
	RSS           uint64  `json:"rss"`
	Command       string  `json:"command"`
}

type cliMetricsProcessesResponse struct {
	RequestedAt string                  `json:"requested_at"`
	Processes   []cliProcessSnapshotRow `json:"processes"`
}

type cliServiceHistoryRow struct {
	Timestamp string `json:"timestamp"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	PID       int    `json:"pid"`
}

type cliMetricsSummaryResponse struct {
	TotalSamples    int64  `json:"total_samples"`
	OldestTimestamp string `json:"oldest_timestamp"`
	NewestTimestamp string `json:"newest_timestamp"`
	DBSizeBytes     int64  `json:"db_size_bytes"`
}

type cliMonitoringCPU struct {
	Usage float64 `json:"usage"`
	Cores int     `json:"cores"`
	Model string  `json:"model"`
}

type cliMonitoringMemory struct {
	Total          uint64  `json:"total"`
	Used           uint64  `json:"used"`
	Free           uint64  `json:"free"`
	Percentage     float64 `json:"percentage"`
	Buffers        uint64  `json:"buffers"`
	Cached         uint64  `json:"cached"`
	Available      uint64  `json:"available"`
	SwapTotal      uint64  `json:"swapTotal"`
	SwapUsed       uint64  `json:"swapUsed"`
	SwapFree       uint64  `json:"swapFree"`
	SwapPercentage float64 `json:"swapPercentage"`
}

type cliMonitoringDisk struct {
	Mount      string  `json:"mount"`
	Total      uint64  `json:"total"`
	Used       uint64  `json:"used"`
	Free       uint64  `json:"free"`
	Percentage float64 `json:"percentage"`
}

type cliMonitoringNetwork struct {
	Interface string `json:"interface"`
	BytesIn   uint64 `json:"bytesIn"`
	BytesOut  uint64 `json:"bytesOut"`
}

type cliMonitoringStatsResponse struct {
	CPU      cliMonitoringCPU       `json:"cpu"`
	Memory   cliMonitoringMemory    `json:"memory"`
	Disk     []cliMonitoringDisk    `json:"disk"`
	Load     [3]float64             `json:"load"`
	Uptime   int64                  `json:"uptime"`
	Hostname string                 `json:"hostname"`
	OS       string                 `json:"os"`
	Network  []cliMonitoringNetwork `json:"network"`
}

type cliMonitoringProcess struct {
	PID       int     `json:"pid"`
	StartTime uint64  `json:"startTime"`
	User      string  `json:"user"`
	CPU       float64 `json:"cpu"`
	Memory    float64 `json:"memory"`
	VSZ       uint64  `json:"vsz"`
	RSS       uint64  `json:"rss"`
	Stat      string  `json:"stat"`
	Command   string  `json:"command"`
}

// cliManagedMonitoringStatsResponse is the fixed projection of the
// provider-neutral metrics.read response. Keep this separate from the local
// monitoring DTO: the local endpoint has a legacy, richer shape while managed
// metrics deliberately expose only this bounded observation.
type cliManagedMonitoringStatsResponse struct {
	ObservedAt string                         `json:"observed_at"`
	CPU        cliManagedMonitoringCPU        `json:"cpu"`
	Load       cliManagedMonitoringLoad       `json:"load"`
	Memory     cliManagedMonitoringMemory     `json:"memory"`
	Network    cliManagedMonitoringNetwork    `json:"network"`
	RootDisk   cliManagedMonitoringFilesystem `json:"root_disk"`
}

type cliManagedMonitoringCPU struct {
	UsagePercent float64 `json:"usage_percent"`
	CoreCount    int     `json:"core_count"`
}

type cliManagedMonitoringLoad struct {
	One     float64 `json:"one"`
	Five    float64 `json:"five"`
	Fifteen float64 `json:"fifteen"`
}

type cliManagedMonitoringMemory struct {
	TotalBytes     uint64  `json:"total_bytes"`
	UsedBytes      uint64  `json:"used_bytes"`
	AvailableBytes uint64  `json:"available_bytes"`
	UsagePercent   float64 `json:"usage_percent"`
}

type cliManagedMonitoringNetwork struct {
	RXBytes uint64 `json:"rx_bytes"`
	TXBytes uint64 `json:"tx_bytes"`
}

type cliManagedMonitoringFilesystem struct {
	TotalBytes     uint64  `json:"total_bytes"`
	UsedBytes      uint64  `json:"used_bytes"`
	AvailableBytes uint64  `json:"available_bytes"`
	UsagePercent   float64 `json:"usage_percent"`
}

func runMetrics(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: hserverctl metrics history|processes|services|summary")
	}
	switch args[0] {
	case "history":
		return runMetricsHistory(ctx, client, args[1:], out)
	case "processes":
		if len(args) > 1 && args[1] == "timestamps" {
			return runMetricsProcessTimestamps(ctx, client, args[2:], out)
		}
		return runMetricsProcesses(ctx, client, args[1:], out)
	case "services":
		if len(args) == 1 || args[1] != "history" {
			return errors.New("usage: hserverctl metrics services history [--range 1h|6h|24h|7d|30d] [--format json|table]")
		}
		return runMetricsServiceHistory(ctx, client, args[2:], out)
	case "summary":
		return runMetricsSummary(ctx, client, args[1:], out)
	default:
		return fmt.Errorf("unknown metrics command %q", args[0])
	}
}

func runMonitoring(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: hserverctl monitoring stats|processes")
	}
	switch args[0] {
	case "stats":
		format, nodeID, err := parseMonitoringStatsArgs(args[1:])
		if err != nil {
			return err
		}
		if nodeID != "" {
			value, err := requestManagedMonitoringStats(ctx, client, nodeID)
			if err != nil {
				return err
			}
			if format == "json" {
				return writeMetricsJSON(out, value)
			}
			return writeManagedMonitoringStatsTable(out, value)
		}
		value, err := requestMetricsObject[cliMonitoringStatsResponse](ctx, client, monitoringStatsEndpoint, "cpu", "memory", "disk", "load", "uptime", "hostname", "os", "network")
		if err != nil {
			return err
		}
		if format == "json" {
			return writeMetricsJSON(out, value)
		}
		return writeMonitoringStatsTable(out, value)
	case "processes":
		format, err := parseMetricsOutputFormat("monitoring processes", args[1:])
		if err != nil {
			return err
		}
		value, err := requestMetrics[[]cliMonitoringProcess](ctx, client, monitoringProcessesEndpoint)
		if err != nil {
			return err
		}
		if format == "json" {
			return writeMetricsJSON(out, value)
		}
		return writeMonitoringProcessesTable(out, value)
	default:
		return fmt.Errorf("unknown monitoring command %q", args[0])
	}
}

func parseMonitoringStatsArgs(args []string) (format, nodeID string, err error) {
	flags := flag.NewFlagSet("monitoring stats", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	node := flags.String("node", "", "managed node ID; omit for the local host")
	formatValue := flags.String("format", "json", "output format: json or table")
	if err := flags.Parse(args); err != nil {
		return "", "", err
	}
	for _, name := range []string{"node", "format"} {
		if countMonitoringStatsFlag(args, name) > 1 {
			return "", "", fmt.Errorf("monitoring stats --%s may be specified only once", name)
		}
	}
	if len(flags.Args()) != 0 {
		return "", "", errors.New("monitoring stats does not accept positional arguments")
	}
	format, err = normalizeMetricsOutputFormat(*formatValue)
	if err != nil {
		return "", "", err
	}
	if flagWasSet(flags, "node") {
		nodeID = strings.TrimSpace(*node)
		if nodeID == "" {
			return "", "", errors.New("monitoring stats --node must not be empty")
		}
	}
	return format, nodeID, nil
}

func countMonitoringStatsFlag(args []string, name string) int {
	prefix := "--" + name
	count := 0
	for _, arg := range args {
		if arg == prefix || strings.HasPrefix(arg, prefix+"=") {
			count++
		}
	}
	return count
}

func requestManagedMonitoringStats(ctx context.Context, client *apiClient, nodeID string) (cliManagedMonitoringStatsResponse, error) {
	endpoint := fmt.Sprintf(managedMonitoringMetricsEndpoint, url.PathEscape(nodeID))
	raw, err := client.request(ctx, http.MethodGet, endpoint, nil, true)
	if err != nil {
		return cliManagedMonitoringStatsResponse{}, err
	}
	return decodeManagedMonitoringStats(raw, endpoint)
}

func decodeManagedMonitoringStats(raw []byte, endpoint string) (cliManagedMonitoringStatsResponse, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return cliManagedMonitoringStatsResponse{}, fmt.Errorf("decode %s: %w", endpoint, err)
	}
	if fields == nil {
		return cliManagedMonitoringStatsResponse{}, fmt.Errorf("decode %s: response must be an object", endpoint)
	}
	for _, field := range []string{"observed_at", "cpu", "load", "memory", "network", "root_disk"} {
		if _, ok := fields[field]; !ok {
			return cliManagedMonitoringStatsResponse{}, fmt.Errorf("decode %s: missing %s", endpoint, field)
		}
	}

	observedAt, err := decodeManagedMonitoringObservedAt(fields["observed_at"], endpoint)
	if err != nil {
		return cliManagedMonitoringStatsResponse{}, err
	}
	cpu, err := decodeManagedMonitoringObject(fields["cpu"], "cpu", endpoint)
	if err != nil {
		return cliManagedMonitoringStatsResponse{}, err
	}
	load, err := decodeManagedMonitoringObject(fields["load"], "load", endpoint)
	if err != nil {
		return cliManagedMonitoringStatsResponse{}, err
	}
	memory, err := decodeManagedMonitoringObject(fields["memory"], "memory", endpoint)
	if err != nil {
		return cliManagedMonitoringStatsResponse{}, err
	}
	network, err := decodeManagedMonitoringObject(fields["network"], "network", endpoint)
	if err != nil {
		return cliManagedMonitoringStatsResponse{}, err
	}
	rootDisk, err := decodeManagedMonitoringObject(fields["root_disk"], "root_disk", endpoint)
	if err != nil {
		return cliManagedMonitoringStatsResponse{}, err
	}

	if err := validateManagedMonitoringFloat(cpu, "usage_percent", "cpu.usage_percent", endpoint); err != nil {
		return cliManagedMonitoringStatsResponse{}, err
	}
	if err := validateManagedMonitoringInteger(cpu, "core_count", "cpu.core_count", endpoint); err != nil {
		return cliManagedMonitoringStatsResponse{}, err
	}
	for _, field := range []string{"one", "five", "fifteen"} {
		if err := validateManagedMonitoringFloat(load, field, "load."+field, endpoint); err != nil {
			return cliManagedMonitoringStatsResponse{}, err
		}
	}
	for _, field := range []string{"total_bytes", "used_bytes", "available_bytes"} {
		if err := validateManagedMonitoringUint(memory, field, "memory."+field, endpoint); err != nil {
			return cliManagedMonitoringStatsResponse{}, err
		}
	}
	if err := validateManagedMonitoringFloat(memory, "usage_percent", "memory.usage_percent", endpoint); err != nil {
		return cliManagedMonitoringStatsResponse{}, err
	}
	for _, field := range []string{"rx_bytes", "tx_bytes"} {
		if err := validateManagedMonitoringUint(network, field, "network."+field, endpoint); err != nil {
			return cliManagedMonitoringStatsResponse{}, err
		}
	}
	for _, field := range []string{"total_bytes", "used_bytes", "available_bytes"} {
		if err := validateManagedMonitoringUint(rootDisk, field, "root_disk."+field, endpoint); err != nil {
			return cliManagedMonitoringStatsResponse{}, err
		}
	}
	if err := validateManagedMonitoringFloat(rootDisk, "usage_percent", "root_disk.usage_percent", endpoint); err != nil {
		return cliManagedMonitoringStatsResponse{}, err
	}

	var value cliManagedMonitoringStatsResponse
	if err := json.Unmarshal(raw, &value); err != nil {
		return cliManagedMonitoringStatsResponse{}, fmt.Errorf("decode %s: %w", endpoint, err)
	}
	value.ObservedAt = observedAt
	return value, nil
}

func decodeManagedMonitoringObservedAt(raw json.RawMessage, endpoint string) (string, error) {
	if rawJSONIsNull(raw) {
		return "", fmt.Errorf("decode %s: observed_at is required", endpoint)
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", fmt.Errorf("decode %s: observed_at must be an RFC3339 timestamp", endpoint)
	}
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("decode %s: observed_at is required", endpoint)
	}
	if _, err := time.Parse(time.RFC3339Nano, value); err != nil {
		return "", fmt.Errorf("decode %s: observed_at must be an RFC3339 timestamp", endpoint)
	}
	return value, nil
}

func decodeManagedMonitoringObject(raw json.RawMessage, name, endpoint string) (map[string]json.RawMessage, error) {
	if rawJSONIsNull(raw) {
		return nil, fmt.Errorf("decode %s: %s must be an object", endpoint, name)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return nil, fmt.Errorf("decode %s: %s must be an object", endpoint, name)
	}
	return fields, nil
}

func managedMonitoringField(fields map[string]json.RawMessage, rawField, field, endpoint string) (json.RawMessage, error) {
	raw, ok := fields[rawField]
	if !ok {
		return nil, fmt.Errorf("decode %s: missing %s", endpoint, field)
	}
	if rawJSONIsNull(raw) {
		return nil, fmt.Errorf("decode %s: %s must not be null", endpoint, field)
	}
	return raw, nil
}

func validateManagedMonitoringFloat(fields map[string]json.RawMessage, rawField, field, endpoint string) error {
	raw, err := managedMonitoringField(fields, rawField, field, endpoint)
	if err != nil {
		return err
	}
	var value float64
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("decode %s: %s must be a finite number", endpoint, field)
	}
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return fmt.Errorf("decode %s: %s must be finite", endpoint, field)
	}
	if value < 0 {
		return fmt.Errorf("decode %s: %s must not be negative", endpoint, field)
	}
	return nil
}

func validateManagedMonitoringInteger(fields map[string]json.RawMessage, rawField, field, endpoint string) error {
	raw, err := managedMonitoringField(fields, rawField, field, endpoint)
	if err != nil {
		return err
	}
	var value int
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("decode %s: %s must be a non-negative integer", endpoint, field)
	}
	if value < 0 {
		return fmt.Errorf("decode %s: %s must not be negative", endpoint, field)
	}
	return nil
}

func validateManagedMonitoringUint(fields map[string]json.RawMessage, rawField, field, endpoint string) error {
	raw, err := managedMonitoringField(fields, rawField, field, endpoint)
	if err != nil {
		return err
	}
	var value uint64
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("decode %s: %s must be a non-negative integer", endpoint, field)
	}
	return nil
}

func runMetricsHistory(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("metrics history", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	rangeValue := flags.String("range", "", "history range: 1h, 6h, 24h, 7d, or 30d")
	formatValue := flags.String("format", "json", "output format: json or table")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 0 {
		return errors.New("usage: hserverctl metrics history [--range 1h|6h|24h|7d|30d] [--format json|table]")
	}
	format, err := normalizeMetricsOutputFormat(*formatValue)
	if err != nil {
		return err
	}
	validatedRange, err := validateMetricsRange(*rangeValue, flags, "metrics history")
	if err != nil {
		return err
	}
	endpoint := metricsRangeEndpoint(metricsHistoryEndpoint, validatedRange)
	value, err := requestMetricsObject[cliMetricsHistoryResponse](ctx, client, endpoint, "range", "resolution", "data")
	if err != nil {
		return err
	}
	if format == "json" {
		return writeMetricsJSON(out, value)
	}
	return writeMetricsHistoryTable(out, value, endpoint)
}

func runMetricsProcesses(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("metrics processes", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	at := flags.String("at", "", "RFC3339 timestamp for the nearest snapshot; omit for the latest")
	formatValue := flags.String("format", "json", "output format: json or table")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 0 {
		return errors.New("usage: hserverctl metrics processes [--at RFC3339] [--format json|table]")
	}
	format, err := normalizeMetricsOutputFormat(*formatValue)
	if err != nil {
		return err
	}
	atValue, err := validateMetricsAt(*at, flags)
	if err != nil {
		return err
	}
	endpoint := metricsProcessesEndpoint
	if atValue != "" {
		endpoint += "?" + url.Values{"at": []string{atValue}}.Encode()
	}
	value, err := requestMetricsObject[cliMetricsProcessesResponse](ctx, client, endpoint, "requested_at", "processes")
	if err != nil {
		return err
	}
	if format == "json" {
		return writeMetricsJSON(out, value)
	}
	return writeMetricsProcessesTable(out, value)
}

func runMetricsProcessTimestamps(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("metrics processes timestamps", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	rangeValue := flags.String("range", "", "history range: 1h, 6h, 24h, 7d, or 30d")
	formatValue := flags.String("format", "json", "output format: json or table")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 0 {
		return errors.New("usage: hserverctl metrics processes timestamps [--range 1h|6h|24h|7d|30d] [--format json|table]")
	}
	format, err := normalizeMetricsOutputFormat(*formatValue)
	if err != nil {
		return err
	}
	validatedRange, err := validateMetricsRange(*rangeValue, flags, "metrics processes timestamps")
	if err != nil {
		return err
	}
	endpoint := metricsRangeEndpoint(metricsProcessTimestampsEndpoint, validatedRange)
	value, err := requestMetrics[[]string](ctx, client, endpoint)
	if err != nil {
		return err
	}
	if format == "json" {
		return writeMetricsJSON(out, value)
	}
	return writeMetricsTimestampsTable(out, value)
}

func runMetricsServiceHistory(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("metrics services history", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	rangeValue := flags.String("range", "", "history range: 1h, 6h, 24h, 7d, or 30d")
	formatValue := flags.String("format", "json", "output format: json or table")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 0 {
		return errors.New("usage: hserverctl metrics services history [--range 1h|6h|24h|7d|30d] [--format json|table]")
	}
	format, err := normalizeMetricsOutputFormat(*formatValue)
	if err != nil {
		return err
	}
	validatedRange, err := validateMetricsRange(*rangeValue, flags, "metrics services history")
	if err != nil {
		return err
	}
	endpoint := metricsRangeEndpoint(metricsServicesHistoryEndpoint, validatedRange)
	value, err := requestMetrics[[]cliServiceHistoryRow](ctx, client, endpoint)
	if err != nil {
		return err
	}
	if format == "json" {
		return writeMetricsJSON(out, value)
	}
	return writeMetricsServiceHistoryTable(out, value)
}

func runMetricsSummary(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("metrics summary", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	formatValue := flags.String("format", "json", "output format: json or table")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 0 {
		return errors.New("usage: hserverctl metrics summary [--format json|table]")
	}
	format, err := normalizeMetricsOutputFormat(*formatValue)
	if err != nil {
		return err
	}
	value, err := requestMetricsObject[cliMetricsSummaryResponse](ctx, client, metricsSummaryEndpoint, "total_samples", "oldest_timestamp", "newest_timestamp", "db_size_bytes")
	if err != nil {
		return err
	}
	if format == "json" {
		return writeMetricsJSON(out, value)
	}
	return writeMetricsSummaryTable(out, value)
}

func parseMetricsOutputFormat(command string, args []string) (string, error) {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	format := flags.String("format", "json", "output format: json or table")
	if err := flags.Parse(args); err != nil {
		return "", err
	}
	if len(flags.Args()) != 0 {
		return "", fmt.Errorf("%s does not accept positional arguments", command)
	}
	return normalizeMetricsOutputFormat(*format)
}

func normalizeMetricsOutputFormat(format string) (string, error) {
	format = strings.ToLower(strings.TrimSpace(format))
	switch format {
	case "json":
		return "json", nil
	case "table", "text":
		// `text` is accepted as a compatibility alias for the table view used
		// by the other human-readable CLI commands.
		return "table", nil
	default:
		return "", errors.New("metrics format must be json or table")
	}
}

func validateMetricsRange(value string, flags *flag.FlagSet, command string) (string, error) {
	if !flagWasSet(flags, "range") {
		return "", nil
	}
	for _, allowed := range cliMetricsRanges {
		if value == allowed {
			return value, nil
		}
	}
	return "", fmt.Errorf("%s range must be 1h, 6h, 24h, 7d, or 30d", command)
}

func validateMetricsAt(value string, flags *flag.FlagSet) (string, error) {
	if !flagWasSet(flags, "at") {
		return "", nil
	}
	if value == "" || strings.TrimSpace(value) != value {
		return "", errors.New("metrics processes --at must be an RFC3339 timestamp")
	}
	if _, err := time.Parse(time.RFC3339, value); err != nil {
		return "", fmt.Errorf("metrics processes --at must be RFC3339: %w", err)
	}
	return value, nil
}

func metricsRangeEndpoint(path, rangeValue string) string {
	if rangeValue == "" {
		return path
	}
	return path + "?" + url.Values{"range": []string{rangeValue}}.Encode()
}

func requestMetrics[T any](ctx context.Context, client *apiClient, endpoint string) (T, error) {
	return requestMetricsRaw[T](ctx, client, endpoint)
}

func requestMetricsObject[T any](ctx context.Context, client *apiClient, endpoint string, required ...string) (T, error) {
	var value T
	raw, err := client.request(ctx, http.MethodGet, endpoint, nil, true)
	if err != nil {
		return value, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return value, fmt.Errorf("decode %s: %w", endpoint, err)
	}
	if fields == nil {
		return value, fmt.Errorf("decode %s: response must be an object", endpoint)
	}
	for _, field := range required {
		if _, ok := fields[field]; !ok {
			return value, fmt.Errorf("decode %s: missing %s", endpoint, field)
		}
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return value, fmt.Errorf("decode %s: %w", endpoint, err)
	}
	return value, nil
}

func requestMetricsRaw[T any](ctx context.Context, client *apiClient, endpoint string) (T, error) {
	var value T
	raw, err := client.request(ctx, http.MethodGet, endpoint, nil, true)
	if err != nil {
		return value, err
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return value, fmt.Errorf("decode %s: %w", endpoint, err)
	}
	return value, nil
}

func writeMetricsJSON(out io.Writer, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode monitoring response: %w", err)
	}
	return prettyJSON(out, raw)
}

func writeMetricsHistoryTable(out io.Writer, value cliMetricsHistoryResponse, endpoint string) error {
	if value.Range == "" || value.Resolution == "" || len(value.Data) == 0 {
		return fmt.Errorf("decode %s: response must contain range, resolution, and data", endpoint)
	}
	fmt.Fprintf(out, "Range: %s\n", metricsTableValue(value.Range))
	fmt.Fprintf(out, "Resolution: %s\n", metricsTableValue(value.Resolution))
	switch value.Resolution {
	case "raw":
		rows, err := decodeMetricsArray[cliMetricRow](value.Data, endpoint)
		if err != nil {
			return err
		}
		fmt.Fprintln(out, "TIMESTAMP\tCPU_PERCENT\tMEMORY_TOTAL\tMEMORY_USED\tMEMORY_PERCENT\tMEMORY_BUFFERS\tMEMORY_CACHED\tMEMORY_AVAILABLE\tSWAP_TOTAL\tSWAP_USED\tSWAP_PERCENT\tLOAD_1M\tLOAD_5M\tLOAD_15M\tNET_RX_BYTES\tNET_TX_BYTES\tDISK_ROOT_PERCENT")
		if len(rows) == 0 {
			fmt.Fprintln(out, "none")
			return nil
		}
		for _, row := range rows {
			fmt.Fprintf(out, "%s\t%s\t%d\t%d\t%s\t%d\t%d\t%d\t%d\t%d\t%s\t%s\t%s\t%s\t%d\t%d\t%s\n",
				metricsTableValue(row.Timestamp), metricsFloat(row.CPUPercent), row.MemoryTotal, row.MemoryUsed,
				metricsFloat(row.MemoryPercent), row.MemoryBuffers, row.MemoryCached, row.MemoryAvailable,
				row.SwapTotal, row.SwapUsed, metricsFloat(row.SwapPercent), metricsFloat(row.Load1M),
				metricsFloat(row.Load5M), metricsFloat(row.Load15M), row.NetRXBytes, row.NetTXBytes,
				metricsFloat(row.DiskRootPercent))
		}
	case "minute", "hourly":
		rows, err := decodeMetricsArray[cliAggregatedMetricRow](value.Data, endpoint)
		if err != nil {
			return err
		}
		fmt.Fprintln(out, "BUCKET\tSAMPLE_COUNT\tCPU_AVG\tCPU_MAX\tMEM_AVG\tMEM_MAX\tSWAP_AVG\tSWAP_MAX\tLOAD_1M_AVG\tNET_RX_TOTAL\tNET_TX_TOTAL\tDISK_ROOT_AVG\tDISK_ROOT_MAX")
		if len(rows) == 0 {
			fmt.Fprintln(out, "none")
			return nil
		}
		for _, row := range rows {
			fmt.Fprintf(out, "%s\t%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%d\t%d\t%s\t%s\n",
				metricsTableValue(row.Bucket), row.SampleCount, metricsFloat(row.CPUAvg), metricsFloat(row.CPUMax),
				metricsFloat(row.MemoryAvg), metricsFloat(row.MemoryMax), metricsFloat(row.SwapAvg), metricsFloat(row.SwapMax),
				metricsFloat(row.Load1MAvg), row.NetRXTotal, row.NetTXTotal, metricsFloat(row.DiskRootAvg),
				metricsFloat(row.DiskRootMax))
		}
	default:
		return fmt.Errorf("decode %s: unsupported resolution %q", endpoint, value.Resolution)
	}
	return nil
}

func writeMetricsProcessesTable(out io.Writer, value cliMetricsProcessesResponse) error {
	fmt.Fprintf(out, "Requested at: %s\n", metricsTableValue(value.RequestedAt))
	fmt.Fprintln(out, "TIMESTAMP\tPID\tUSERNAME\tCPU_PERCENT\tMEMORY_PERCENT\tRSS\tCOMMAND")
	if len(value.Processes) == 0 {
		fmt.Fprintln(out, "none")
		return nil
	}
	for _, row := range value.Processes {
		fmt.Fprintf(out, "%s\t%d\t%s\t%s\t%s\t%d\t%s\n",
			metricsTableValue(row.Timestamp), row.PID, metricsTableValue(row.Username), metricsFloat(row.CPUPercent),
			metricsFloat(row.MemoryPercent), row.RSS, metricsTableValue(row.Command))
	}
	return nil
}

func writeMetricsTimestampsTable(out io.Writer, timestamps []string) error {
	fmt.Fprintln(out, "TIMESTAMP")
	if len(timestamps) == 0 {
		fmt.Fprintln(out, "none")
		return nil
	}
	for _, timestamp := range timestamps {
		fmt.Fprintln(out, metricsTableValue(timestamp))
	}
	return nil
}

func writeMetricsServiceHistoryTable(out io.Writer, rows []cliServiceHistoryRow) error {
	fmt.Fprintln(out, "TIMESTAMP\tNAME\tSTATUS\tPID")
	if len(rows) == 0 {
		fmt.Fprintln(out, "none")
		return nil
	}
	for _, row := range rows {
		fmt.Fprintf(out, "%s\t%s\t%s\t%d\n", metricsTableValue(row.Timestamp), metricsTableValue(row.Name), metricsTableValue(row.Status), row.PID)
	}
	return nil
}

func writeMetricsSummaryTable(out io.Writer, value cliMetricsSummaryResponse) error {
	fmt.Fprintln(out, "FIELD\tVALUE")
	fmt.Fprintf(out, "total_samples\t%d\n", value.TotalSamples)
	fmt.Fprintf(out, "oldest_timestamp\t%s\n", metricsTableValue(value.OldestTimestamp))
	fmt.Fprintf(out, "newest_timestamp\t%s\n", metricsTableValue(value.NewestTimestamp))
	fmt.Fprintf(out, "db_size_bytes\t%d\n", value.DBSizeBytes)
	return nil
}

func writeMonitoringStatsTable(out io.Writer, value cliMonitoringStatsResponse) error {
	fmt.Fprintln(out, "CPU")
	fmt.Fprintln(out, "USAGE\tCORES\tMODEL")
	fmt.Fprintf(out, "%s\t%d\t%s\n", metricsFloat(value.CPU.Usage), value.CPU.Cores, metricsTableValue(value.CPU.Model))
	fmt.Fprintln(out, "MEMORY")
	fmt.Fprintln(out, "TOTAL\tUSED\tFREE\tPERCENTAGE\tBUFFERS\tCACHED\tAVAILABLE\tSWAP_TOTAL\tSWAP_USED\tSWAP_FREE\tSWAP_PERCENTAGE")
	fmt.Fprintf(out, "%d\t%d\t%d\t%s\t%d\t%d\t%d\t%d\t%d\t%d\t%s\n",
		value.Memory.Total, value.Memory.Used, value.Memory.Free, metricsFloat(value.Memory.Percentage), value.Memory.Buffers,
		value.Memory.Cached, value.Memory.Available, value.Memory.SwapTotal, value.Memory.SwapUsed, value.Memory.SwapFree,
		metricsFloat(value.Memory.SwapPercentage))
	fmt.Fprintln(out, "SYSTEM")
	fmt.Fprintln(out, "HOSTNAME\tOS\tUPTIME\tLOAD_1M\tLOAD_5M\tLOAD_15M")
	fmt.Fprintf(out, "%s\t%s\t%d\t%s\t%s\t%s\n", metricsTableValue(value.Hostname), metricsTableValue(value.OS), value.Uptime,
		metricsFloat(value.Load[0]), metricsFloat(value.Load[1]), metricsFloat(value.Load[2]))
	fmt.Fprintln(out, "DISK")
	fmt.Fprintln(out, "MOUNT\tTOTAL\tUSED\tFREE\tPERCENTAGE")
	if len(value.Disk) == 0 {
		fmt.Fprintln(out, "none")
	} else {
		for _, disk := range value.Disk {
			fmt.Fprintf(out, "%s\t%d\t%d\t%d\t%s\n", metricsTableValue(disk.Mount), disk.Total, disk.Used, disk.Free, metricsFloat(disk.Percentage))
		}
	}
	fmt.Fprintln(out, "NETWORK")
	fmt.Fprintln(out, "INTERFACE\tBYTES_IN\tBYTES_OUT")
	if len(value.Network) == 0 {
		fmt.Fprintln(out, "none")
	} else {
		for _, network := range value.Network {
			fmt.Fprintf(out, "%s\t%d\t%d\n", metricsTableValue(network.Interface), network.BytesIn, network.BytesOut)
		}
	}
	return nil
}

func writeManagedMonitoringStatsTable(out io.Writer, value cliManagedMonitoringStatsResponse) error {
	fmt.Fprintf(out, "Observed at: %s\n", metricsTableValue(value.ObservedAt))
	fmt.Fprintln(out, "CPU")
	fmt.Fprintln(out, "USAGE_PERCENT\tCORE_COUNT")
	fmt.Fprintf(out, "%s\t%d\n", metricsFloat(value.CPU.UsagePercent), value.CPU.CoreCount)
	fmt.Fprintln(out, "LOAD")
	fmt.Fprintln(out, "ONE\tFIVE\tFIFTEEN")
	fmt.Fprintf(out, "%s\t%s\t%s\n", metricsFloat(value.Load.One), metricsFloat(value.Load.Five), metricsFloat(value.Load.Fifteen))
	fmt.Fprintln(out, "MEMORY")
	fmt.Fprintln(out, "TOTAL_BYTES\tUSED_BYTES\tAVAILABLE_BYTES\tUSAGE_PERCENT")
	fmt.Fprintf(out, "%d\t%d\t%d\t%s\n", value.Memory.TotalBytes, value.Memory.UsedBytes, value.Memory.AvailableBytes, metricsFloat(value.Memory.UsagePercent))
	fmt.Fprintln(out, "NETWORK")
	fmt.Fprintln(out, "RX_BYTES\tTX_BYTES")
	fmt.Fprintf(out, "%d\t%d\n", value.Network.RXBytes, value.Network.TXBytes)
	fmt.Fprintln(out, "ROOT_DISK")
	fmt.Fprintln(out, "TOTAL_BYTES\tUSED_BYTES\tAVAILABLE_BYTES\tUSAGE_PERCENT")
	fmt.Fprintf(out, "%d\t%d\t%d\t%s\n", value.RootDisk.TotalBytes, value.RootDisk.UsedBytes, value.RootDisk.AvailableBytes, metricsFloat(value.RootDisk.UsagePercent))
	return nil
}

func writeMonitoringProcessesTable(out io.Writer, rows []cliMonitoringProcess) error {
	fmt.Fprintln(out, "PID\tSTART_TIME\tUSER\tCPU\tMEMORY\tVSZ\tRSS\tSTAT\tCOMMAND")
	if len(rows) == 0 {
		fmt.Fprintln(out, "none")
		return nil
	}
	for _, row := range rows {
		fmt.Fprintf(out, "%d\t%d\t%s\t%s\t%s\t%d\t%d\t%s\t%s\n", row.PID, row.StartTime, metricsTableValue(row.User), metricsFloat(row.CPU),
			metricsFloat(row.Memory), row.VSZ, row.RSS, metricsTableValue(row.Stat), metricsTableValue(row.Command))
	}
	return nil
}

func decodeMetricsArray[T any](raw json.RawMessage, endpoint string) ([]T, error) {
	if strings.TrimSpace(string(raw)) == "null" || len(strings.TrimSpace(string(raw))) == 0 {
		return nil, fmt.Errorf("decode %s: expected an array", endpoint)
	}
	var value []T
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("decode %s data: %w", endpoint, err)
	}
	return value, nil
}

func metricsTableValue(value string) string {
	value = strings.Join(strings.Fields(sanitizeTUIText(value)), " ")
	if value == "" {
		return "N/A"
	}
	return value
}

func metricsFloat(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}
