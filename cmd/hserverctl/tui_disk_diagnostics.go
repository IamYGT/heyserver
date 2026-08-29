package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	diskservice "github.com/IamYGT/heyserver/internal/services/disk"
)

// Disk diagnostics are deliberately local-only. The public API exposes these
// probes on the panel host, while managed agents currently expose only their
// bounded heartbeat disk inventory and fixed cleanup capability.
const (
	tuiDiskDiagnosticSummary  tuiDiskDiagnosticKind = "summary"
	tuiDiskDiagnosticMounts   tuiDiskDiagnosticKind = "mounts"
	tuiDiskDiagnosticUsage    tuiDiskDiagnosticKind = "usage"
	tuiDiskDiagnosticLargest  tuiDiskDiagnosticKind = "largest"
	tuiDiskDiagnosticIO       tuiDiskDiagnosticKind = "io"
	tuiDiskDiagnosticSMART    tuiDiskDiagnosticKind = "smart"
	tuiDiskDiagnosticAnalysis tuiDiskDiagnosticKind = "analysis"

	// The directory APIs require an explicit path. This is the same safe root
	// path used by the existing Disk page's initial directory view; it is sent
	// as a query value and is never represented by a pathless request.
	tuiDiskDefaultPath = "/"

	tuiDiskMaxMountRows    = 32
	tuiDiskMaxUsageRows    = 24
	tuiDiskMaxLargestRows  = 20
	tuiDiskMaxIORows       = 24
	tuiDiskMaxSMARTAttrs   = 24
	tuiDiskMaxAnalysisRows = 40

	tuiDiskMaxFieldLength = 180
)

type tuiDiskDiagnosticKind string

type tuiDiskMount struct {
	Device     string
	MountPoint string
	FSType     string
	Options    string
	Dump       int
	Pass       int
	Source     string
}

type tuiDiskUsage struct {
	Path  string
	Size  uint64
	Items int
}

type tuiDiskLargest struct {
	Path     string
	Size     uint64
	Modified string
}

type tuiDiskIO struct {
	Device          string
	ReadsCompleted  uint64
	WritesCompleted uint64
	ReadBytes       uint64
	WriteBytes      uint64
	IOInProgress    uint64
	IOTimeMS        uint64
}

type tuiDiskSMARTAttr struct {
	ID    int
	Name  string
	Value int
	Worst int
	Raw   string
}

// tuiDiskSMART intentionally omits SmartInfo.RawOutput and serial data. The
// TUI needs the bounded inventory/status fields, not a raw smartctl dump.
type tuiDiskSMART struct {
	Available bool
	Healthy   bool
	Device    string
	Model     string
	Firmware  string
	Status    string
	Message   string
	Attrs     []tuiDiskSMARTAttr
}

type tuiDiskAnalysis struct {
	ID            string
	Unit          string
	Status        string
	Message       string
	CreatedAt     string
	StartedAt     string
	FinishedAt    string
	RootSize      uint64
	RootUsed      uint64
	RootAvailable uint64
	Entries       []tuiDiskUsage
	Errors        []string
}

type tuiDiskDiagnosticsState struct {
	Loaded          bool
	Supported       bool
	UnsupportedNote string
	Path            string
	MountsLoaded    bool
	Mounts          []tuiDiskMount
	UsageLoaded     bool
	Usage           []tuiDiskUsage
	LargestLoaded   bool
	Largest         []tuiDiskLargest
	IOLoaded        bool
	IO              []tuiDiskIO
	SMARTLoaded     bool
	SMART           *tuiDiskSMART
	AnalysisLoaded  bool
	Analysis        tuiDiskAnalysis
	Warnings        []string
}

type tuiDiskDiagnosticsMsg struct {
	TargetID   string
	Kind       tuiDiskDiagnosticKind
	State      tuiDiskDiagnosticsState
	OpenDialog bool
	Mutation   bool
	Err        error
}

type tuiDiskAnalysisStartRequest struct {
	Target tuiTarget
}

func loadTUIDiskDiagnosticsSummaryCmd(ctx context.Context, client *apiClient, target tuiTarget) tea.Cmd {
	return func() tea.Msg {
		state, err := loadTUIDiskDiagnostics(ctx, client, target, tuiDiskDefaultPath)
		return tuiDiskDiagnosticsMsg{TargetID: target.ID, Kind: tuiDiskDiagnosticSummary, State: state, Err: err}
	}
}

// loadTUIDiskDiagnostics loads only the cheap read-only summary probes. The
// recursive usage and largest-file scans stay behind explicit key actions so
// opening the Disk tab never starts a host-wide walk by accident.
func loadTUIDiskDiagnostics(ctx context.Context, client *apiClient, target tuiTarget, path string) (tuiDiskDiagnosticsState, error) {
	state := tuiDiskDiagnosticsState{Loaded: true, Path: tuiDiskDefaultPath}
	if !target.Local {
		state.Supported = false
		state.UnsupportedNote = "Deep disk diagnostics run only on the panel host; managed targets expose bounded heartbeat disk data instead."
		return state, nil
	}
	state.Supported = true
	if strings.TrimSpace(path) != "" {
		validated, err := validateTUIDiskPath(path)
		if err != nil {
			return tuiDiskDiagnosticsState{}, err
		}
		state.Path = validated
	}

	// Keep each read independently available. A missing optional dependency or
	// one failed probe must not turn successful mounts/I/O/status observations
	// into an empty-looking diagnostics panel.
	if mounts, err := requestJSON[[]diskservice.MountEntry](ctx, client, http.MethodGet, diskMountsEndpoint, nil, true); err != nil {
		state.Warnings = append(state.Warnings, tuiDiskWarning("mount inventory", err))
	} else {
		state.MountsLoaded = true
		state.Mounts = projectTUIDiskMounts(mounts)
	}
	if stats, err := requestJSON[[]diskservice.IOStats](ctx, client, http.MethodGet, diskIOEndpoint, nil, true); err != nil {
		state.Warnings = append(state.Warnings, tuiDiskWarning("I/O stats", err))
	} else {
		state.IOLoaded = true
		state.IO = projectTUIDiskIO(stats)
	}
	if smart, err := requestJSON[diskservice.SmartInfo](ctx, client, http.MethodGet, diskSmartEndpoint+"root", nil, true); err != nil {
		state.Warnings = append(state.Warnings, tuiDiskWarning("SMART status", err))
	} else {
		state.SMARTLoaded = true
		state.SMART = projectTUIDiskSMART(smart)
	}
	if analysis, err := requestJSON[diskservice.AnalysisStatus](ctx, client, http.MethodGet, diskAnalysisStatusEndpoint, nil, true); err != nil {
		state.Warnings = append(state.Warnings, tuiDiskWarning("analysis status", err))
	} else {
		state.AnalysisLoaded = true
		state.Analysis = projectTUIDiskAnalysis(analysis)
	}
	return state, nil
}

func loadTUIDiskDiagnosticCmd(ctx context.Context, client *apiClient, target tuiTarget, kind tuiDiskDiagnosticKind, path string, openDialog bool) tea.Cmd {
	return func() tea.Msg {
		state, err := loadTUIDiskDiagnostic(ctx, client, target, kind, path)
		return tuiDiskDiagnosticsMsg{TargetID: target.ID, Kind: kind, State: state, OpenDialog: openDialog, Err: err}
	}
}

func loadTUIDiskDiagnostic(ctx context.Context, client *apiClient, target tuiTarget, kind tuiDiskDiagnosticKind, path string) (tuiDiskDiagnosticsState, error) {
	if !target.Local {
		return tuiDiskDiagnosticsState{
			Loaded:          true,
			Supported:       false,
			UnsupportedNote: "Deep disk diagnostics run only on the panel host; managed targets expose bounded heartbeat disk data instead.",
		}, nil
	}

	state := tuiDiskDiagnosticsState{Loaded: true, Supported: true, Path: tuiDiskDefaultPath}
	switch kind {
	case tuiDiskDiagnosticMounts:
		mounts, err := requestJSON[[]diskservice.MountEntry](ctx, client, http.MethodGet, diskMountsEndpoint, nil, true)
		if err != nil {
			return tuiDiskDiagnosticsState{}, err
		}
		state.MountsLoaded = true
		state.Mounts = projectTUIDiskMounts(mounts)
	case tuiDiskDiagnosticUsage:
		validated, err := requireTUIDiskPath(path, "disk usage")
		if err != nil {
			return tuiDiskDiagnosticsState{}, err
		}
		state.Path = validated
		query := url.Values{"path": []string{validated}, "depth": []string{"1"}}
		endpoint := diskUsageEndpoint + "?" + query.Encode()
		usage, err := requestJSON[[]diskservice.DirUsage](ctx, client.withTimeout(45*time.Second), http.MethodGet, endpoint, nil, true)
		if err != nil {
			return tuiDiskDiagnosticsState{}, err
		}
		state.UsageLoaded = true
		state.Usage = projectTUIDiskUsage(usage)
	case tuiDiskDiagnosticLargest:
		validated, err := requireTUIDiskPath(path, "largest disk entries")
		if err != nil {
			return tuiDiskDiagnosticsState{}, err
		}
		state.Path = validated
		query := url.Values{"path": []string{validated}, "limit": []string{"20"}}
		endpoint := diskLargestEndpoint + "?" + query.Encode()
		entries, err := requestJSON[[]diskservice.LargestFile](ctx, client.withTimeout(75*time.Second), http.MethodGet, endpoint, nil, true)
		if err != nil {
			return tuiDiskDiagnosticsState{}, err
		}
		state.LargestLoaded = true
		state.Largest = projectTUIDiskLargest(entries)
	case tuiDiskDiagnosticIO:
		stats, err := requestJSON[[]diskservice.IOStats](ctx, client, http.MethodGet, diskIOEndpoint, nil, true)
		if err != nil {
			return tuiDiskDiagnosticsState{}, err
		}
		state.IOLoaded = true
		state.IO = projectTUIDiskIO(stats)
	case tuiDiskDiagnosticSMART:
		// "root" is an explicit API-supported selector. The server resolves it
		// to one physical device and refuses to choose arbitrarily for RAID or
		// multi-disk roots.
		smart, err := requestJSON[diskservice.SmartInfo](ctx, client, http.MethodGet, diskSmartEndpoint+"root", nil, true)
		if err != nil {
			return tuiDiskDiagnosticsState{}, err
		}
		state.SMARTLoaded = true
		state.SMART = projectTUIDiskSMART(smart)
	case tuiDiskDiagnosticAnalysis:
		analysis, err := requestJSON[diskservice.AnalysisStatus](ctx, client, http.MethodGet, diskAnalysisStatusEndpoint, nil, true)
		if err != nil {
			return tuiDiskDiagnosticsState{}, err
		}
		state.AnalysisLoaded = true
		state.Analysis = projectTUIDiskAnalysis(analysis)
	default:
		return tuiDiskDiagnosticsState{}, fmt.Errorf("unsupported disk diagnostic %q", kind)
	}
	return state, nil
}

func runTUIDiskAnalysisStartCmd(ctx context.Context, client *apiClient, target tuiTarget) tea.Cmd {
	return func() tea.Msg {
		if !target.Local {
			return tuiDiskDiagnosticsMsg{TargetID: target.ID, Kind: tuiDiskDiagnosticAnalysis, Mutation: true, Err: errors.New("deep disk analysis runs only on the panel host")}
		}
		status, err := requestJSON[diskservice.AnalysisStatus](ctx, client.withTimeout(30*time.Second), http.MethodPost, diskAnalysisStartEndpoint, nil, true)
		state := tuiDiskDiagnosticsState{Loaded: true, Supported: true, Path: tuiDiskDefaultPath}
		if err == nil {
			state.AnalysisLoaded = true
			state.Analysis = projectTUIDiskAnalysis(status)
		}
		return tuiDiskDiagnosticsMsg{TargetID: target.ID, Kind: tuiDiskDiagnosticAnalysis, State: state, Mutation: true, Err: err}
	}
}

func (model tuiModel) loadDiskDiagnosticsSummary() (tea.Model, tea.Cmd) {
	target := model.snapshot.Selected
	if !target.Local {
		model.diskDiagnosticsTarget = target.ID
		model.diskDiagnostics = tuiDiskDiagnosticsState{
			Loaded:          true,
			Supported:       false,
			UnsupportedNote: "Deep disk diagnostics run only on the panel host; managed targets expose bounded heartbeat disk data instead.",
		}
		model.notice = model.diskDiagnostics.UnsupportedNote
		model.noticeError = false
		return model, nil
	}
	model.resourceLoading = true
	model.diskDiagnosticsTarget = target.ID
	model.notice = "Loading local disk diagnostics…"
	model.noticeError = false
	return model, loadTUIDiskDiagnosticsSummaryCmd(model.ctx, model.client, target)
}

func (model tuiModel) openDiskDiagnostic(kind tuiDiskDiagnosticKind) (tea.Model, tea.Cmd) {
	target := model.snapshot.Selected
	if !target.Local {
		model.notice = "Deep disk diagnostics are unsupported on managed targets; no local endpoint was requested."
		model.noticeError = false
		return model, nil
	}
	path := model.diskDiagnostics.Path
	if path == "" {
		path = tuiDiskDefaultPath
	}
	if kind == tuiDiskDiagnosticUsage || kind == tuiDiskDiagnosticLargest {
		if _, err := requireTUIDiskPath(path, string(kind)); err != nil {
			model.setErrorNotice(err)
			return model, nil
		}
	}
	model.resourceLoading = true
	model.diskDiagnosticsTarget = target.ID
	model.notice = "Loading " + tuiDiskDiagnosticLabel(kind) + "…"
	model.noticeError = false
	return model, loadTUIDiskDiagnosticCmd(model.ctx, model.client, target, kind, path, true)
}

func (model *tuiModel) openDiskAnalysisConfirmation() {
	if !model.snapshot.Selected.Local {
		model.notice = "Deep disk analysis is unsupported on managed targets; no local endpoint was requested."
		model.noticeError = false
		return
	}
	model.dialog = tuiDialog{
		Mode:      tuiDialogConfirm,
		Title:     "Confirm deep disk analysis",
		Operation: tuiOperation{Label: "Queue deep disk analysis"},
		Body: []string{
			"Queue one bounded, low-priority local systemd analysis.",
			"The worker scans fixed roots: /var/lib, /var/www, /opt and /root.",
			"Only the panel host is supported; Y confirms and N/Esc cancels.",
		},
		DiskAnalysisStart: &tuiDiskAnalysisStartRequest{Target: model.snapshot.Selected},
	}
}

func (model tuiModel) mergeTUIDiskDiagnostics(kind tuiDiskDiagnosticKind, state tuiDiskDiagnosticsState) tuiDiskDiagnosticsState {
	current := model.diskDiagnostics
	if state.Path != "" {
		current.Path = state.Path
	}
	current.Loaded = state.Loaded
	current.Supported = state.Supported
	current.UnsupportedNote = state.UnsupportedNote
	if state.Warnings != nil {
		current.Warnings = state.Warnings
	}
	switch kind {
	case tuiDiskDiagnosticSummary:
		current.MountsLoaded, current.Mounts = state.MountsLoaded, state.Mounts
		current.IOLoaded, current.IO = state.IOLoaded, state.IO
		current.SMARTLoaded, current.SMART = state.SMARTLoaded, state.SMART
		current.AnalysisLoaded, current.Analysis = state.AnalysisLoaded, state.Analysis
	case tuiDiskDiagnosticMounts:
		current.MountsLoaded, current.Mounts = state.MountsLoaded, state.Mounts
	case tuiDiskDiagnosticUsage:
		current.UsageLoaded, current.Usage = state.UsageLoaded, state.Usage
	case tuiDiskDiagnosticLargest:
		current.LargestLoaded, current.Largest = state.LargestLoaded, state.Largest
	case tuiDiskDiagnosticIO:
		current.IOLoaded, current.IO = state.IOLoaded, state.IO
	case tuiDiskDiagnosticSMART:
		current.SMARTLoaded, current.SMART = state.SMARTLoaded, state.SMART
	case tuiDiskDiagnosticAnalysis:
		current.AnalysisLoaded, current.Analysis = state.AnalysisLoaded, state.Analysis
	}
	return current
}

func (model tuiModel) diskDiagnosticsDialog(kind tuiDiskDiagnosticKind) tuiDialog {
	lines := diskDiagnosticLines(kind, model.diskDiagnostics)
	return tuiDialog{
		Mode:               tuiDialogLogs,
		Title:              "Disk · " + tuiDiskDiagnosticLabel(kind),
		LogLines:           lines,
		LogScroll:          maxInt(0, len(lines)-1),
		DiskDiagnosticKind: kind,
		DiskDiagnosticPath: model.diskDiagnostics.Path,
	}
}

func tuiDiskDiagnosticLabel(kind tuiDiskDiagnosticKind) string {
	switch kind {
	case tuiDiskDiagnosticMounts:
		return "mounts"
	case tuiDiskDiagnosticUsage:
		return "usage"
	case tuiDiskDiagnosticLargest:
		return "largest entries"
	case tuiDiskDiagnosticIO:
		return "I/O"
	case tuiDiskDiagnosticSMART:
		return "SMART"
	case tuiDiskDiagnosticAnalysis:
		return "analysis status"
	default:
		return "diagnostics"
	}
}

func tuiDiskWarning(label string, err error) string {
	message := clientErrorMessage(err)
	if message == "" {
		message = "unavailable"
	}
	return truncateTUI(label+" unavailable: "+message, tuiDiskMaxFieldLength)
}

func requireTUIDiskPath(path, command string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("%s requires an explicit safe path", command)
	}
	validated, err := validateTUIDiskPath(path)
	if err != nil {
		return "", fmt.Errorf("%s: %w", command, err)
	}
	return validated, nil
}

func validateTUIDiskPath(path string) (string, error) {
	clean := filepath.Clean(strings.TrimSpace(path))
	if clean == "." || !filepath.IsAbs(clean) {
		return "", errors.New("disk diagnostic path must be absolute")
	}
	allowed := []string{"/", "/var", "/tmp", "/home", "/opt", "/etc", "/usr", "/root", "/srv", "/boot"}
	for _, root := range allowed {
		if clean == root || strings.HasPrefix(clean, root+string(filepath.Separator)) {
			return clean, nil
		}
	}
	return "", errors.New("disk diagnostic path is outside the fixed safe roots")
}

func projectTUIDiskMounts(rows []diskservice.MountEntry) []tuiDiskMount {
	projected := make([]tuiDiskMount, 0, minInt(len(rows), tuiDiskMaxMountRows))
	for _, row := range rows {
		projected = append(projected, tuiDiskMount{
			Device: truncateTUI(row.Device, 80), MountPoint: truncateTUI(row.MountPoint, 100),
			FSType: truncateTUI(row.FSType, 32), Options: truncateTUI(row.Options, 100),
			Dump: row.Dump, Pass: row.Pass, Source: truncateTUI(row.Source, 20),
		})
		if len(projected) >= tuiDiskMaxMountRows {
			break
		}
	}
	sort.SliceStable(projected, func(i, j int) bool {
		if projected[i].MountPoint != projected[j].MountPoint {
			return projected[i].MountPoint < projected[j].MountPoint
		}
		return projected[i].Device < projected[j].Device
	})
	return projected
}

func projectTUIDiskUsage(rows []diskservice.DirUsage) []tuiDiskUsage {
	return projectTUIDiskUsageRows(rows, tuiDiskMaxUsageRows)
}

func projectTUIDiskLargest(rows []diskservice.LargestFile) []tuiDiskLargest {
	projected := make([]tuiDiskLargest, 0, minInt(len(rows), tuiDiskMaxLargestRows))
	for _, row := range rows {
		projected = append(projected, tuiDiskLargest{Path: truncateTUI(row.Path, tuiDiskMaxFieldLength), Size: row.Size, Modified: truncateTUI(row.Modified, 40)})
		if len(projected) >= tuiDiskMaxLargestRows {
			break
		}
	}
	return projected
}

func projectTUIDiskIO(rows []diskservice.IOStats) []tuiDiskIO {
	projected := make([]tuiDiskIO, 0, minInt(len(rows), tuiDiskMaxIORows))
	for _, row := range rows {
		projected = append(projected, tuiDiskIO{
			Device: truncateTUI(row.Device, 40), ReadsCompleted: row.ReadsCompleted, WritesCompleted: row.WritesCompleted,
			ReadBytes: row.ReadBytes, WriteBytes: row.WriteBytes, IOInProgress: row.IOInProgress, IOTimeMS: row.IOTime,
		})
		if len(projected) >= tuiDiskMaxIORows {
			break
		}
	}
	return projected
}

func projectTUIDiskSMART(info diskservice.SmartInfo) *tuiDiskSMART {
	projected := &tuiDiskSMART{
		Available: info.Available, Healthy: info.Healthy, Device: truncateTUI(info.Device, 80),
		Model: truncateTUI(info.Model, 100), Firmware: truncateTUI(info.Firmware, 40),
		Status: truncateTUI(info.Status, 24), Message: truncateTUI(info.Message, tuiDiskMaxFieldLength),
		Attrs: make([]tuiDiskSMARTAttr, 0, minInt(len(info.Attrs), tuiDiskMaxSMARTAttrs)),
	}
	for _, attr := range info.Attrs {
		projected.Attrs = append(projected.Attrs, tuiDiskSMARTAttr{
			ID: attr.ID, Name: truncateTUI(attr.Name, 64), Value: attr.Value, Worst: attr.Worst, Raw: truncateTUI(attr.Raw, 64),
		})
		if len(projected.Attrs) >= tuiDiskMaxSMARTAttrs {
			break
		}
	}
	return projected
}

func projectTUIDiskAnalysis(status diskservice.AnalysisStatus) tuiDiskAnalysis {
	return tuiDiskAnalysis{
		ID: truncateTUI(status.ID, 48), Unit: truncateTUI(status.Unit, 100), Status: truncateTUI(status.Status, 24),
		Message: truncateTUI(status.Message, tuiDiskMaxFieldLength), CreatedAt: truncateTUI(status.CreatedAt, 40),
		StartedAt: truncateTUI(status.StartedAt, 40), FinishedAt: truncateTUI(status.FinishedAt, 40),
		RootSize: status.RootSize, RootUsed: status.RootUsed, RootAvailable: status.RootAvailable,
		Entries: projectTUIDiskUsageRows(status.Entries, tuiDiskMaxAnalysisRows), Errors: projectTUIDiskStrings(status.Errors, tuiDiskMaxUsageRows),
	}
}

func projectTUIDiskUsageRows(rows []diskservice.DirUsage, maximum int) []tuiDiskUsage {
	if maximum <= 0 {
		return nil
	}
	projected := make([]tuiDiskUsage, 0, minInt(len(rows), maximum))
	for _, row := range rows {
		projected = append(projected, tuiDiskUsage{Path: truncateTUI(row.Path, tuiDiskMaxFieldLength), Size: row.Size, Items: maxInt(0, row.Items)})
		if len(projected) >= maximum {
			break
		}
	}
	return projected
}

func projectTUIDiskStrings(values []string, maximum int) []string {
	if maximum <= 0 {
		return nil
	}
	projected := make([]string, 0, minInt(len(values), maximum))
	for _, value := range values {
		projected = append(projected, truncateTUI(value, tuiDiskMaxFieldLength))
		if len(projected) >= maximum {
			break
		}
	}
	return projected
}

func diskDiagnosticLines(kind tuiDiskDiagnosticKind, state tuiDiskDiagnosticsState) []string {
	if !state.Supported {
		return []string{
			sanitizeTUILogLine(truncateTUI("UNSUPPORTED", tuiDiskMaxFieldLength)),
			sanitizeTUILogLine(truncateTUI(state.UnsupportedNote, tuiDiskMaxFieldLength)),
		}
	}
	lines := make([]string, 0, 48)
	switch kind {
	case tuiDiskDiagnosticMounts:
		lines = append(lines, "DEVICE\tMOUNT\tFS\tSOURCE\tOPTIONS")
		for _, row := range state.Mounts {
			lines = append(lines, fmt.Sprintf("%s\t%s\t%s\t%s\t%s", row.Device, row.MountPoint, row.FSType, row.Source, row.Options))
		}
		if len(state.Mounts) == 0 {
			lines = append(lines, "No mount entries were observed.")
		}
	case tuiDiskDiagnosticUsage:
		lines = append(lines, "PATH\tSIZE\tITEMS", "Root: "+valueOrNA(state.Path))
		for _, row := range state.Usage {
			lines = append(lines, fmt.Sprintf("%s\t%s\t%d", row.Path, formatTUIBytes(row.Size), row.Items))
		}
		if len(state.Usage) == 0 {
			lines = append(lines, "No child usage entries were returned.")
		}
	case tuiDiskDiagnosticLargest:
		lines = append(lines, "PATH\tSIZE\tMODIFIED", "Root: "+valueOrNA(state.Path))
		for _, row := range state.Largest {
			lines = append(lines, fmt.Sprintf("%s\t%s\t%s", row.Path, formatTUIBytes(row.Size), valueOrNA(row.Modified)))
		}
		if len(state.Largest) == 0 {
			lines = append(lines, "No files were returned.")
		}
	case tuiDiskDiagnosticIO:
		lines = append(lines, "DEVICE\tREADS\tWRITES\tREAD BYTES\tWRITE BYTES\tQUEUE\tIO MS")
		for _, row := range state.IO {
			lines = append(lines, fmt.Sprintf("%s\t%d\t%d\t%s\t%s\t%d\t%d", row.Device, row.ReadsCompleted, row.WritesCompleted, formatTUIBytes(row.ReadBytes), formatTUIBytes(row.WriteBytes), row.IOInProgress, row.IOTimeMS))
		}
		if len(state.IO) == 0 {
			lines = append(lines, "No physical I/O devices were observed.")
		}
	case tuiDiskDiagnosticSMART:
		if state.SMART == nil {
			return []string{"SMART status was not observed."}
		}
		smart := state.SMART
		lines = append(lines, "status\t"+valueOrNA(smart.Status), "available\t"+strconv.FormatBool(smart.Available), "healthy\t"+strconv.FormatBool(smart.Healthy), "device\t"+valueOrNA(smart.Device))
		if smart.Model != "" {
			lines = append(lines, "model\t"+smart.Model)
		}
		if smart.Firmware != "" {
			lines = append(lines, "firmware\t"+smart.Firmware)
		}
		if smart.Message != "" {
			lines = append(lines, "message\t"+smart.Message)
		}
		if len(smart.Attrs) > 0 {
			lines = append(lines, "", "ATTRIBUTES", "ID\tNAME\tVALUE\tWORST\tRAW")
			for _, attr := range smart.Attrs {
				lines = append(lines, fmt.Sprintf("%d\t%s\t%d\t%d\t%s", attr.ID, attr.Name, attr.Value, attr.Worst, attr.Raw))
			}
		}
	case tuiDiskDiagnosticAnalysis:
		analysis := state.Analysis
		lines = append(lines, "status\t"+valueOrNA(analysis.Status), "message\t"+valueOrNA(analysis.Message))
		if analysis.ID != "" {
			lines = append(lines, "id\t"+analysis.ID)
		}
		if analysis.Unit != "" {
			lines = append(lines, "unit\t"+analysis.Unit)
		}
		if analysis.RootSize > 0 {
			lines = append(lines, "root size\t"+formatTUIBytes(analysis.RootSize), "root used\t"+formatTUIBytes(analysis.RootUsed), "root available\t"+formatTUIBytes(analysis.RootAvailable))
		}
		if len(analysis.Entries) > 0 {
			lines = append(lines, "", "TOP PATHS", "PATH\tSIZE")
			for _, row := range analysis.Entries {
				lines = append(lines, fmt.Sprintf("%s\t%s", row.Path, formatTUIBytes(row.Size)))
			}
		}
		for _, err := range analysis.Errors {
			lines = append(lines, "error\t"+err)
		}
	default:
		return []string{"No disk diagnostic output was selected."}
	}
	for index := range lines {
		lines[index] = sanitizeTUILogLine(truncateTUI(lines[index], tuiDiskMaxFieldLength*2))
	}
	return lines
}
