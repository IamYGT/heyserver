package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	diskservice "github.com/IamYGT/heyserver/internal/services/disk"
)

const (
	diskAnalysisStartEndpoint  = "/api/disk/analysis/start"
	diskAnalysisStatusEndpoint = "/api/disk/analysis/status"
	diskDirSizeEndpoint        = "/api/disk/dirsize"
	diskIOEndpoint             = "/api/disk/io"
	diskLargestEndpoint        = "/api/disk/largest"
	diskListEndpoint           = "/api/disk/list"
	diskMountsEndpoint         = "/api/disk/mounts"
	diskSmartEndpoint          = "/api/disk/smart/"
	diskUsageEndpoint          = "/api/disk/usage"
)

// runDiskDiagnostics contains the local-only disk observation commands. The
// API intentionally has no managed-node equivalent for these probes: a
// --node option here would either misrepresent the panel host or imply a
// remote shell capability that the API does not provide.
func runDiskDiagnostics(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: hserverctl disk analysis start|status | disk dirsize PATH | disk io | disk largest PATH | disk list [PATH] | disk mounts | disk smart DEVICE | disk usage PATH")
	}

	switch args[0] {
	case "analysis":
		if len(args) < 2 {
			return errors.New("usage: hserverctl disk analysis start|status")
		}
		switch args[1] {
		case "start":
			return runDiskAnalysisStart(ctx, client, args[2:], out)
		case "status":
			return runDiskAnalysisStatus(ctx, client, args[2:], out)
		default:
			return fmt.Errorf("unknown disk analysis command %q", args[1])
		}
	case "dirsize":
		return runDiskDirSize(ctx, client, args[1:], out)
	case "io":
		return runDiskIO(ctx, client, args[1:], out)
	case "largest":
		return runDiskLargest(ctx, client, args[1:], out)
	case "list":
		return runDiskList(ctx, client, args[1:], out)
	case "mounts":
		return runDiskMounts(ctx, client, args[1:], out)
	case "smart":
		return runDiskSmart(ctx, client, args[1:], out)
	case "usage":
		return runDiskUsage(ctx, client, args[1:], out)
	default:
		return fmt.Errorf("unknown disk command %q", args[0])
	}
}

func runDiskAnalysisStart(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("disk analysis start", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	confirmed := flags.Bool("confirm", false, "confirm queueing a local deep disk analysis")
	formatValue := flags.String("format", "json", "output format: json or table")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 0 {
		return errors.New("usage: hserverctl disk analysis start --confirm [--format json|table]")
	}
	if !*confirmed {
		return errors.New("disk analysis start requires explicit --confirm")
	}
	format, err := normalizeDiskDiagnosticsFormat("disk analysis start", *formatValue)
	if err != nil {
		return err
	}
	if format == "json" {
		return printRequest(ctx, client, out, http.MethodPost, diskAnalysisStartEndpoint, nil, true)
	}
	status, err := requestJSON[diskservice.AnalysisStatus](ctx, client, http.MethodPost, diskAnalysisStartEndpoint, nil, true)
	if err != nil {
		return err
	}
	return writeDiskAnalysisStatusTable(out, status)
}

func runDiskAnalysisStatus(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("disk analysis status", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	formatValue := flags.String("format", "json", "output format: json or table")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 0 {
		return errors.New("usage: hserverctl disk analysis status [--format json|table]")
	}
	format, err := normalizeDiskDiagnosticsFormat("disk analysis status", *formatValue)
	if err != nil {
		return err
	}
	if format == "json" {
		return printRequest(ctx, client, out, http.MethodGet, diskAnalysisStatusEndpoint, nil, true)
	}
	status, err := requestJSON[diskservice.AnalysisStatus](ctx, client, http.MethodGet, diskAnalysisStatusEndpoint, nil, true)
	if err != nil {
		return err
	}
	return writeDiskAnalysisStatusTable(out, status)
}

func runDiskDirSize(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("disk dirsize", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	formatValue := flags.String("format", "json", "output format: json or table")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 1 {
		return errors.New("usage: hserverctl disk dirsize [--format json|table] PATH")
	}
	dirPath, err := explicitDiskPath("disk dirsize", flags.Args()[0])
	if err != nil {
		return err
	}
	format, err := normalizeDiskDiagnosticsFormat("disk dirsize", *formatValue)
	if err != nil {
		return err
	}
	endpoint := diskEndpointWithQuery(diskDirSizeEndpoint, url.Values{"path": []string{dirPath}})
	if format == "json" {
		return printRequest(ctx, client.withTimeout(30*time.Second), out, http.MethodGet, endpoint, nil, true)
	}
	value, err := requestJSON[cliDiskDirSizeResponse](ctx, client.withTimeout(30*time.Second), http.MethodGet, endpoint, nil, true)
	if err != nil {
		return err
	}
	return writeDiskDirSizeTable(out, value)
}

func runDiskIO(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	format, err := parseDiskDiagnosticsFormat("disk io", args)
	if err != nil {
		return err
	}
	if format == "json" {
		return printRequest(ctx, client, out, http.MethodGet, diskIOEndpoint, nil, true)
	}
	stats, err := requestJSON[[]diskservice.IOStats](ctx, client, http.MethodGet, diskIOEndpoint, nil, true)
	if err != nil {
		return err
	}
	return writeDiskIOTable(out, stats)
}

func runDiskLargest(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("disk largest", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	limit := flags.Int("limit", 0, "maximum files to return; the API defaults to 20 and accepts 1-50")
	formatValue := flags.String("format", "json", "output format: json or table")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 1 {
		return errors.New("usage: hserverctl disk largest [--limit N] [--format json|table] PATH")
	}
	dirPath, err := explicitDiskPath("disk largest", flags.Args()[0])
	if err != nil {
		return err
	}
	if flagWasSet(flags, "limit") && (*limit < 1 || *limit > 50) {
		return errors.New("disk largest limit must be between 1 and 50")
	}
	format, err := normalizeDiskDiagnosticsFormat("disk largest", *formatValue)
	if err != nil {
		return err
	}
	query := url.Values{"path": []string{dirPath}}
	if flagWasSet(flags, "limit") {
		query.Set("limit", strconv.Itoa(*limit))
	}
	endpoint := diskEndpointWithQuery(diskLargestEndpoint, query)
	if format == "json" {
		return printRequest(ctx, client.withTimeout(75*time.Second), out, http.MethodGet, endpoint, nil, true)
	}
	files, err := requestJSON[[]diskservice.LargestFile](ctx, client.withTimeout(75*time.Second), http.MethodGet, endpoint, nil, true)
	if err != nil {
		return err
	}
	return writeDiskLargestTable(out, files)
}

func runDiskList(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("disk list", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	formatValue := flags.String("format", "json", "output format: json or table")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) > 1 {
		return errors.New("usage: hserverctl disk list [--format json|table] [PATH]")
	}
	format, err := normalizeDiskDiagnosticsFormat("disk list", *formatValue)
	if err != nil {
		return err
	}
	// The API deliberately defaults a pathless list request to its own root
	// policy. Do not materialize "/" here; that keeps the CLI from inventing a
	// path if the server policy changes.
	endpoint := diskListEndpoint
	if len(flags.Args()) == 1 {
		dirPath, pathErr := explicitDiskPath("disk list", flags.Args()[0])
		if pathErr != nil {
			return pathErr
		}
		endpoint = diskEndpointWithQuery(endpoint, url.Values{"path": []string{dirPath}})
	}
	if format == "json" {
		return printRequest(ctx, client, out, http.MethodGet, endpoint, nil, true)
	}
	value, err := requestJSON[cliDiskListResponse](ctx, client, http.MethodGet, endpoint, nil, true)
	if err != nil {
		return err
	}
	return writeDiskListTable(out, value)
}

func runDiskMounts(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	format, err := parseDiskDiagnosticsFormat("disk mounts", args)
	if err != nil {
		return err
	}
	if format == "json" {
		return printRequest(ctx, client, out, http.MethodGet, diskMountsEndpoint, nil, true)
	}
	mounts, err := requestJSON[[]diskservice.MountEntry](ctx, client, http.MethodGet, diskMountsEndpoint, nil, true)
	if err != nil {
		return err
	}
	return writeDiskMountsTable(out, mounts)
}

func runDiskSmart(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("disk smart", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	formatValue := flags.String("format", "json", "output format: json or table")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 1 {
		return errors.New("usage: hserverctl disk smart [--format json|table] DEVICE")
	}
	device := strings.TrimSpace(flags.Args()[0])
	if device == "" {
		return errors.New("disk smart requires an explicit device")
	}
	format, err := normalizeDiskDiagnosticsFormat("disk smart", *formatValue)
	if err != nil {
		return err
	}
	// PathEscape protects the device path segment without selecting a default
	// device. The API accepts the special explicit device name "root" or a
	// validated Linux block-device basename (and also preserves /dev/... input).
	endpoint := diskSmartEndpoint + url.PathEscape(device)
	if format == "json" {
		return printRequest(ctx, client, out, http.MethodGet, endpoint, nil, true)
	}
	info, err := requestJSON[diskservice.SmartInfo](ctx, client, http.MethodGet, endpoint, nil, true)
	if err != nil {
		return err
	}
	return writeDiskSmartTable(out, info)
}

func runDiskUsage(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("disk usage", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	depth := flags.Int("depth", 0, "directory depth; the API defaults to 1 and accepts 1-3")
	formatValue := flags.String("format", "json", "output format: json or table")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 1 {
		return errors.New("usage: hserverctl disk usage [--depth N] [--format json|table] PATH")
	}
	dirPath, err := explicitDiskPath("disk usage", flags.Args()[0])
	if err != nil {
		return err
	}
	if flagWasSet(flags, "depth") && (*depth < 1 || *depth > 3) {
		return errors.New("disk usage depth must be between 1 and 3")
	}
	format, err := normalizeDiskDiagnosticsFormat("disk usage", *formatValue)
	if err != nil {
		return err
	}
	query := url.Values{"path": []string{dirPath}}
	if flagWasSet(flags, "depth") {
		query.Set("depth", strconv.Itoa(*depth))
	}
	endpoint := diskEndpointWithQuery(diskUsageEndpoint, query)
	if format == "json" {
		return printRequest(ctx, client.withTimeout(45*time.Second), out, http.MethodGet, endpoint, nil, true)
	}
	usage, err := requestJSON[[]diskservice.DirUsage](ctx, client.withTimeout(45*time.Second), http.MethodGet, endpoint, nil, true)
	if err != nil {
		return err
	}
	return writeDiskUsageTable(out, usage)
}

func explicitDiskPath(command, value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%s requires an explicit path", command)
	}
	return value, nil
}

func diskEndpointWithQuery(endpoint string, query url.Values) string {
	if len(query) == 0 {
		return endpoint
	}
	return endpoint + "?" + query.Encode()
}

func parseDiskDiagnosticsFormat(command string, args []string) (string, error) {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	formatValue := flags.String("format", "json", "output format: json or table")
	if err := flags.Parse(args); err != nil {
		return "", err
	}
	if len(flags.Args()) != 0 {
		return "", fmt.Errorf("%s does not accept positional arguments", command)
	}
	return normalizeDiskDiagnosticsFormat(command, *formatValue)
}

func normalizeDiskDiagnosticsFormat(command, format string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "json":
		return "json", nil
	case "table", "text":
		return "table", nil
	default:
		return "", fmt.Errorf("%s format must be json or table", command)
	}
}

type cliDiskDirSizeResponse struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

type cliDiskListResponse struct {
	Path    string                  `json:"path"`
	Entries []diskservice.FileEntry `json:"entries"`
	Count   int                     `json:"count"`
}

func writeDiskAnalysisStatusTable(out io.Writer, status diskservice.AnalysisStatus) error {
	fmt.Fprintln(out, "FIELD\tVALUE")
	fmt.Fprintf(out, "id\t%s\n", metricsTableValue(status.ID))
	fmt.Fprintf(out, "unit\t%s\n", metricsTableValue(status.Unit))
	fmt.Fprintf(out, "status\t%s\n", metricsTableValue(status.Status))
	fmt.Fprintf(out, "message\t%s\n", metricsTableValue(status.Message))
	fmt.Fprintf(out, "created_at\t%s\n", metricsTableValue(status.CreatedAt))
	fmt.Fprintf(out, "started_at\t%s\n", metricsTableValue(status.StartedAt))
	fmt.Fprintf(out, "finished_at\t%s\n", metricsTableValue(status.FinishedAt))
	fmt.Fprintf(out, "root_size\t%d\n", status.RootSize)
	fmt.Fprintf(out, "root_used\t%d\n", status.RootUsed)
	fmt.Fprintf(out, "root_available\t%d\n", status.RootAvailable)
	fmt.Fprintln(out, "ENTRIES")
	fmt.Fprintln(out, "PATH\tSIZE\tITEMS")
	if len(status.Entries) == 0 {
		fmt.Fprintln(out, "none")
	} else {
		for _, entry := range status.Entries {
			fmt.Fprintf(out, "%s\t%d\t%d\n", metricsTableValue(entry.Path), entry.Size, entry.Items)
		}
	}
	fmt.Fprintln(out, "ERRORS")
	if len(status.Errors) == 0 {
		fmt.Fprintln(out, "none")
	} else {
		for _, item := range status.Errors {
			fmt.Fprintln(out, metricsTableValue(item))
		}
	}
	return nil
}

func writeDiskDirSizeTable(out io.Writer, value cliDiskDirSizeResponse) error {
	fmt.Fprintln(out, "PATH\tSIZE")
	fmt.Fprintf(out, "%s\t%d\n", metricsTableValue(value.Path), value.Size)
	return nil
}

func writeDiskIOTable(out io.Writer, stats []diskservice.IOStats) error {
	fmt.Fprintln(out, "DEVICE\tREADS_COMPLETED\tWRITES_COMPLETED\tSECTORS_READ\tSECTORS_WRITTEN\tREAD_BYTES\tWRITE_BYTES\tIO_IN_PROGRESS\tIO_TIME_MS")
	if len(stats) == 0 {
		fmt.Fprintln(out, "none")
		return nil
	}
	for _, stat := range stats {
		fmt.Fprintf(out, "%s\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\n",
			metricsTableValue(stat.Device), stat.ReadsCompleted, stat.WritesCompleted,
			stat.SectorsRead, stat.SectorsWritten, stat.ReadBytes, stat.WriteBytes,
			stat.IOInProgress, stat.IOTime)
	}
	return nil
}

func writeDiskLargestTable(out io.Writer, files []diskservice.LargestFile) error {
	fmt.Fprintln(out, "PATH\tSIZE\tMODIFIED")
	if len(files) == 0 {
		fmt.Fprintln(out, "none")
		return nil
	}
	for _, file := range files {
		fmt.Fprintf(out, "%s\t%d\t%s\n", metricsTableValue(file.Path), file.Size, metricsTableValue(file.Modified))
	}
	return nil
}

func writeDiskListTable(out io.Writer, value cliDiskListResponse) error {
	fmt.Fprintf(out, "Path: %s\n", metricsTableValue(value.Path))
	fmt.Fprintf(out, "Count: %d\n", value.Count)
	fmt.Fprintln(out, "TYPE\tNAME\tPATH\tSIZE\tMODIFIED\tMODE\tCHILDREN")
	if len(value.Entries) == 0 {
		fmt.Fprintln(out, "none")
		return nil
	}
	for _, entry := range value.Entries {
		typeName := "file"
		if entry.IsDir {
			typeName = "directory"
		}
		fmt.Fprintf(out, "%s\t%s\t%s\t%d\t%s\t%s\t%d\n",
			typeName, metricsTableValue(entry.Name), metricsTableValue(entry.Path), entry.Size,
			metricsTableValue(entry.Modified), metricsTableValue(entry.Mode), entry.Children)
	}
	return nil
}

func writeDiskMountsTable(out io.Writer, mounts []diskservice.MountEntry) error {
	fmt.Fprintln(out, "DEVICE\tMOUNT_POINT\tFS_TYPE\tOPTIONS\tDUMP\tPASS\tSOURCE")
	if len(mounts) == 0 {
		fmt.Fprintln(out, "none")
		return nil
	}
	for _, mount := range mounts {
		fmt.Fprintf(out, "%s\t%s\t%s\t%s\t%d\t%d\t%s\n",
			metricsTableValue(mount.Device), metricsTableValue(mount.MountPoint),
			metricsTableValue(mount.FSType), metricsTableValue(mount.Options), mount.Dump,
			mount.Pass, metricsTableValue(mount.Source))
	}
	return nil
}

func writeDiskSmartTable(out io.Writer, info diskservice.SmartInfo) error {
	fmt.Fprintln(out, "FIELD\tVALUE")
	fmt.Fprintf(out, "available\t%t\n", info.Available)
	fmt.Fprintf(out, "healthy\t%t\n", info.Healthy)
	fmt.Fprintf(out, "device\t%s\n", metricsTableValue(info.Device))
	fmt.Fprintf(out, "model\t%s\n", metricsTableValue(info.Model))
	fmt.Fprintf(out, "serial\t%s\n", metricsTableValue(info.Serial))
	fmt.Fprintf(out, "firmware\t%s\n", metricsTableValue(info.Firmware))
	fmt.Fprintf(out, "status\t%s\n", metricsTableValue(info.Status))
	fmt.Fprintf(out, "message\t%s\n", metricsTableValue(info.Message))
	if len(info.Attrs) > 0 {
		fmt.Fprintln(out, "ATTRS")
		fmt.Fprintln(out, "ID\tNAME\tVALUE\tWORST\tRAW")
		for _, attr := range info.Attrs {
			fmt.Fprintf(out, "%d\t%s\t%d\t%d\t%s\n", attr.ID, metricsTableValue(attr.Name), attr.Value, attr.Worst, metricsTableValue(attr.Raw))
		}
	}
	return nil
}

func writeDiskUsageTable(out io.Writer, usage []diskservice.DirUsage) error {
	fmt.Fprintln(out, "PATH\tSIZE\tITEMS")
	if len(usage) == 0 {
		fmt.Fprintln(out, "none")
		return nil
	}
	for _, entry := range usage {
		fmt.Fprintf(out, "%s\t%d\t%d\n", metricsTableValue(entry.Path), entry.Size, entry.Items)
	}
	return nil
}
