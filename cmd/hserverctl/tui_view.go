package main

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/IamYGT/heyserver/internal/agenthub"
)

var (
	tuiAccent       = lipgloss.Color("#8B5CF6")
	tuiAccentBright = lipgloss.Color("#C4B5FD")
	tuiText         = lipgloss.Color("#F4F4F5")
	tuiMuted        = lipgloss.Color("#A1A1AA")
	tuiDim          = lipgloss.Color("#71717A")
	tuiBorder       = lipgloss.Color("#3F3F46")
	tuiGreen        = lipgloss.Color("#34D399")
	tuiAmber        = lipgloss.Color("#FBBF24")
	tuiRed          = lipgloss.Color("#FB7185")
	tuiBlue         = lipgloss.Color("#60A5FA")

	tuiTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(tuiText)
	tuiMutedStyle = lipgloss.NewStyle().Foreground(tuiMuted)
	tuiDimStyle   = lipgloss.NewStyle().Foreground(tuiDim)
	tuiKeyStyle   = lipgloss.NewStyle().Bold(true).Foreground(tuiAccentBright)
	tuiPanelStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(tuiBorder).Padding(0, 1)
)

func (model tuiModel) View() tea.View {
	width := model.width
	if width < 48 {
		width = 48
	}
	height := model.height
	if height < 18 {
		height = 18
	}

	var content string
	if model.dialog.Mode != tuiDialogNone {
		content = model.renderDialog(width, height)
	} else {
		content = model.renderScreen(width, height)
	}
	view := tea.NewView(content)
	view.AltScreen = true
	view.WindowTitle = "HServer Control Center"
	return view
}

func (model tuiModel) renderScreen(width, height int) string {
	contentWidth := width - 4
	if contentWidth > 132 {
		contentWidth = 132
	}
	if contentWidth < 44 {
		contentWidth = 44
	}

	header := model.renderHeader(contentWidth)
	tabs := model.renderTabs(contentWidth)
	bodyHeight := height - 13
	if bodyHeight < 8 {
		bodyHeight = 8
	}
	body := model.renderBody(contentWidth, bodyHeight)
	status := model.renderStatus(contentWidth)
	footer := model.renderFooter(contentWidth)

	block := strings.Join([]string{header, tabs, body, status, footer}, "\n")
	return lipgloss.NewStyle().Width(contentWidth).Render(block)
}

func (model tuiModel) renderHeader(width int) string {
	width = maxInt(1, width)
	brandText := "◆ HSERVER"
	subtitleText := "  CONTROL CENTER"
	brand := lipgloss.NewStyle().Bold(true).Foreground(tuiAccentBright).Render(brandText)
	subtitle := tuiMutedStyle.Render(subtitleText)
	brandLine := brand + subtitle
	if lipgloss.Width(brandLine) > width {
		brandLine = tuiMutedStyle.Render(truncateTUIWidth(brandText+subtitleText, width))
	}

	contextName := model.contextName
	if contextName == "" {
		contextName = "direct"
	}
	contextLine := renderTUIHeaderField("Context: ", contextName, width)
	panelLine := renderTUIHeaderField("Panel: ", valueOrNA(model.serverURL), width)

	target := model.snapshot.Selected
	state := tuiGreen
	stateLabel := "ONLINE"
	if !target.Local && !target.Online {
		state = tuiRed
		stateLabel = "OFFLINE"
	}
	if model.loading || model.resourceLoading {
		state = tuiBlue
		stateLabel = "SYNCING"
	}
	scope := "Managed"
	if target.Local {
		scope = "Local"
	}
	badgeText := "● " + stateLabel
	badge := lipgloss.NewStyle().Bold(true).Foreground(state).Render(badgeText)
	targetLine := renderTUIHeaderTarget(target.label(), scope, badge, badgeText, width)

	return strings.Join([]string{brandLine, contextLine, panelLine, targetLine}, "\n")
}

func renderTUIHeaderField(label, value string, width int) string {
	label = sanitizeTUIText(label)
	if width <= 0 {
		return ""
	}
	labelWidth := lipgloss.Width(label)
	if labelWidth >= width {
		return tuiDimStyle.Render(truncateTUIWidth(label, width))
	}
	valueWidth := width - labelWidth
	// Style the complete field as one span so the visible contract remains
	// copyable as `Context: NAME` / `Panel: URL` even when color is enabled.
	return lipgloss.NewStyle().Foreground(tuiText).Render(label + truncateTUIWidth(value, valueWidth))
}

func renderTUIHeaderTarget(label, scope, badge, badgeText string, width int) string {
	prefix := "Target: "
	if width <= 0 {
		return ""
	}
	labelWidth := lipgloss.Width(prefix)
	badgeWidth := lipgloss.Width(badgeText)
	if labelWidth >= width {
		return lipgloss.NewStyle().Foreground(tuiText).Render(truncateTUIWidth(prefix, width))
	}
	value := truncateTUIWidth(label+" · "+scope, maxInt(1, width-labelWidth-2-badgeWidth))
	line := lipgloss.NewStyle().Foreground(tuiText).Render(prefix + value)
	if lipgloss.Width(line)+2+badgeWidth <= width {
		return line + "  " + badge
	}
	if lipgloss.Width(line)+1+badgeWidth <= width {
		return line + " " + badge
	}
	// At exceptionally narrow widths, retain the badge on its own line rather
	// than allowing a field or status marker to overflow the terminal.
	badgeLine := truncateTUIWidth(badgeText, width)
	if lipgloss.Width(line) <= width {
		return line + "\n" + badgeLine
	}
	return badgeLine
}

func (model tuiModel) renderTabs(width int) string {
	if width < 116 {
		current := fmt.Sprintf("%d/%d  %s", int(model.tab)+1, len(tuiTabLabels), tuiTabLabels[model.tab])
		return tuiDimStyle.Render("‹ ") + lipgloss.NewStyle().Bold(true).Foreground(tuiAccentBright).Render(current) + tuiDimStyle.Render(" ›")
	}
	start := maxInt(0, int(model.tab)-2)
	end := minInt(len(tuiTabLabels), start+5)
	start = maxInt(0, end-5)
	parts := make([]string, 0, end-start+2)
	if start > 0 {
		parts = append(parts, tuiDimStyle.Render("‹"))
	}
	for index := start; index < end; index++ {
		label := tuiTabLabels[index]
		if width < 140 {
			switch tuiTab(index) {
			case tuiTabOverview:
				label = "Home"
			case tuiTabProcesses:
				label = "Procs"
			case tuiTabMaintenance:
				label = "Maint"
			case tuiTabContainers:
				label = "Docker"
			case tuiTabWeb:
				label = "Web"
			}
		}
		shortcut := fmt.Sprintf("%d", index+1)
		if tuiTab(index) == tuiTabWeb {
			shortcut = "0"
		}
		if tuiTab(index) == tuiTabPHP {
			shortcut = "P"
		}
		if tuiTab(index) == tuiTabDNS {
			shortcut = "Z"
		}
		if tuiTab(index) == tuiTabFirewall {
			shortcut = "F"
		}
		if tuiTab(index) == tuiTabSecurity {
			shortcut = "S"
		}
		if tuiTab(index) == tuiTabCron {
			shortcut = "C"
		}
		if tuiTab(index) == tuiTabDatabases {
			shortcut = "D"
		}
		if tuiTab(index) == tuiTabFiles {
			shortcut = "E"
		}
		if tuiTab(index) == tuiTabBackups {
			shortcut = "B"
		}
		if tuiTab(index) == tuiTabSnapshots {
			shortcut = "N"
		}
		if tuiTab(index) == tuiTabAudit {
			shortcut = "A"
		}
		if tuiTab(index) == tuiTabUpdates {
			shortcut = "U"
		}
		if tuiTab(index) == tuiTabDeploy {
			shortcut = "G"
		}
		if tuiTab(index) == tuiTabAlerts {
			shortcut = "L"
		}
		if tuiTab(index) == tuiTabCloudflare {
			shortcut = "O"
		}
		if tuiTab(index) == tuiTabUsers {
			shortcut = "I"
		}
		if tuiTab(index) == tuiTabMail {
			shortcut = "M"
		}
		text := shortcut + " " + label
		style := lipgloss.NewStyle().Padding(0, 1).Foreground(tuiMuted)
		if tuiTab(index) == model.tab {
			style = style.Bold(true).Foreground(tuiText).Background(tuiAccent)
		}
		parts = append(parts, style.Render(text))
	}
	if end < len(tuiTabLabels) {
		parts = append(parts, tuiDimStyle.Render("›"))
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, parts...)
}

func (model tuiModel) renderBody(width, height int) string {
	switch model.tab {
	case tuiTabOverview:
		return model.renderOverview(width)
	case tuiTabServers:
		return model.renderServers(width, height)
	case tuiTabServices:
		return model.renderServices(width, height)
	case tuiTabProcesses:
		return model.renderProcesses(width, height)
	case tuiTabMaintenance:
		return model.renderMaintenance(width, height)
	case tuiTabDisk:
		return model.renderDisk(width, height)
	case tuiTabContainers:
		return model.renderContainers(width, height)
	case tuiTabPM2:
		return model.renderPM2(width, height)
	case tuiTabPHP:
		return model.renderPHP(width, height)
	case tuiTabLogs:
		return model.renderLogs(width, height)
	case tuiTabWeb:
		return model.renderWeb(width, height)
	case tuiTabDNS:
		return model.renderDNS(width, height)
	case tuiTabFirewall:
		return model.renderFirewall(width, height)
	case tuiTabSecurity:
		return model.renderSecurity(width, height)
	case tuiTabCron:
		return model.renderCron(width, height)
	case tuiTabDatabases:
		return model.renderDatabases(width, height)
	case tuiTabFiles:
		return model.renderFiles(width, height)
	case tuiTabBackups:
		return model.renderBackups(width, height)
	case tuiTabSnapshots:
		return model.renderEncryptedSnapshots(width, height)
	case tuiTabAudit:
		return model.renderAudit(width, height)
	case tuiTabUpdates:
		return model.renderUpdates(width, height)
	case tuiTabDeploy:
		return model.renderDeploy(width, height)
	case tuiTabAlerts:
		return model.renderAlerts(width, height)
	case tuiTabCloudflare:
		return model.renderCloudflare(width, height)
	case tuiTabUsers:
		return model.renderUsers(width, height)
	case tuiTabMail:
		return model.renderMail(width, height)
	default:
		return ""
	}
}

func (model tuiModel) renderOverview(width int) string {
	host := model.snapshot.Host
	cpuValue := "N/A"
	cpuPercent := float64(0)
	if !model.snapshot.HostAvailable {
		cpuValue = "Unavailable"
	} else if host.CPUKnown {
		cpuValue = fmt.Sprintf("%.1f%%", host.CPUPercent)
		cpuPercent = host.CPUPercent
	}
	memoryPercent := host.MemoryPercent
	if memoryPercent <= 0 {
		memoryPercent = percentage(host.MemoryUsed, host.MemoryTotal)
	}
	diskPercent := host.DiskPercent
	if diskPercent <= 0 {
		diskPercent = percentage(host.DiskUsed, host.DiskTotal)
	}
	swapValue := "N/A"
	if model.snapshot.HostAvailable && !host.SwapKnown {
		swapValue = "Not reported"
	} else if model.snapshot.HostAvailable && host.SwapTotal == 0 {
		swapValue = "Not configured"
	} else if model.snapshot.HostAvailable && host.SwapTotal > 0 {
		swapValue = fmt.Sprintf("%s / %s", formatTUIBytes(host.SwapUsed), formatTUIBytes(host.SwapTotal))
	}
	memoryValue := "Unavailable"
	memoryDetail := "Metrics endpoint unavailable"
	diskValue := "Unavailable"
	diskDetail := "Metrics endpoint unavailable"
	loadDetail := "Metrics endpoint unavailable"
	networkDetail := "Metrics endpoint unavailable"
	if model.snapshot.HostAvailable {
		memoryValue = fmt.Sprintf("%.1f%%", memoryPercent)
		memoryDetail = fmt.Sprintf("%s available", formatTUIBytes(host.MemoryAvailable))
		diskValue = fmt.Sprintf("%.1f%%", diskPercent)
		diskDetail = fmt.Sprintf("%s / %s", formatTUIBytes(host.DiskUsed), formatTUIBytes(host.DiskTotal))
		loadDetail = fmt.Sprintf("Load  %.2f / %.2f / %.2f", host.Load1, host.Load5, host.Load15)
		networkDetail = fmt.Sprintf("RX %s · TX %s", formatTUIBytes(host.NetworkRXBytes), formatTUIBytes(host.NetworkTXBytes))
	}

	cardWidth := (width - 6) / 3
	if cardWidth < 22 {
		cardWidth = width - 2
	}
	cards := []string{
		renderMetricCard("CPU", cpuValue, loadDetail, cpuPercent, cardWidth),
		renderMetricCard("Memory", memoryValue, memoryDetail, memoryPercent, cardWidth),
		renderMetricCard("Root disk", diskValue, diskDetail, diskPercent, cardWidth),
	}
	var metrics string
	if cardWidth == width-2 {
		metrics = strings.Join(cards, "\n")
	} else {
		metrics = lipgloss.JoinHorizontal(lipgloss.Top, cards[0], " ", cards[1], " ", cards[2])
	}

	running, problems := 0, 0
	for _, service := range model.snapshot.Services {
		if normalizedServiceState(service.State) == "running" {
			running++
		} else if normalizedServiceState(service.State) != "stopped" && normalizedServiceState(service.State) != "unknown" {
			problems++
		}
	}
	serviceTone := tuiGreen
	serviceText := fmt.Sprintf("%d running", running)
	if !model.snapshot.ServicesAvailable {
		serviceTone = tuiAmber
		serviceText = "Unavailable"
	} else if problems > 0 {
		serviceTone = tuiAmber
	}
	serviceSummary := lipgloss.NewStyle().Bold(true).Foreground(serviceTone).Render(serviceText)
	if model.snapshot.ServicesAvailable && problems > 0 {
		serviceSummary += lipgloss.NewStyle().Foreground(tuiAmber).Render(fmt.Sprintf(" · %d need attention", problems))
	}

	details := []string{
		tuiTitleStyle.Render("Server snapshot"),
		fmt.Sprintf("%-12s %s", tuiMutedStyle.Render("Hostname"), valueOrNA(host.Hostname)),
		fmt.Sprintf("%-12s %s", tuiMutedStyle.Render("OS"), truncateTUI(valueOrNA(host.OS), maxInt(18, width-20))),
		fmt.Sprintf("%-12s %s", tuiMutedStyle.Render("CPU cores"), availableTUIValue(model.snapshot.HostAvailable, fmt.Sprintf("%d", host.Cores))),
		fmt.Sprintf("%-12s %s", tuiMutedStyle.Render("Network"), networkDetail),
		fmt.Sprintf("%-12s %s", tuiMutedStyle.Render("Uptime"), availableTUIValue(model.snapshot.HostAvailable, formatTUIUptime(host.Uptime))),
		fmt.Sprintf("%-12s %s", tuiMutedStyle.Render("Swap"), swapValue),
		fmt.Sprintf("%-12s %s", tuiMutedStyle.Render("Services"), serviceSummary),
	}
	if !model.snapshot.Selected.Local {
		details = append(details,
			fmt.Sprintf("%-12s %s", tuiMutedStyle.Render("Architecture"), valueOrNA(model.snapshot.Selected.Inventory.Arch)),
			fmt.Sprintf("%-12s %s", tuiMutedStyle.Render("Agent"), valueOrNA(model.snapshot.Selected.AgentVersion)),
			fmt.Sprintf("%-12s %d", tuiMutedStyle.Render("Capabilities"), len(model.snapshot.Selected.Capabilities)),
		)
	}
	return metrics + "\n" + tuiPanelStyle.Width(width-2).Render(strings.Join(details, "\n"))
}

func renderMetricCard(label, value, detail string, percent float64, width int) string {
	inner := width - 4
	if inner < 10 {
		inner = 10
	}
	lines := []string{
		tuiMutedStyle.Render(label),
		lipgloss.NewStyle().Bold(true).Foreground(tuiText).Render(value),
		renderProgressBar(percent, inner),
		tuiDimStyle.Render(truncateTUI(detail, inner)),
	}
	return lipgloss.NewStyle().Width(width-2).Border(lipgloss.RoundedBorder()).BorderForeground(tuiBorder).Padding(0, 1).Render(strings.Join(lines, "\n"))
}

func renderProgressBar(percent float64, width int) string {
	if width > 28 {
		width = 28
	}
	if width < 8 {
		width = 8
	}
	if math.IsNaN(percent) || percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	filled := int(math.Round(percent / 100 * float64(width)))
	tone := tuiGreen
	if percent >= 90 {
		tone = tuiRed
	} else if percent >= 75 {
		tone = tuiAmber
	}
	return lipgloss.NewStyle().Foreground(tone).Render(strings.Repeat("━", filled)) +
		lipgloss.NewStyle().Foreground(tuiBorder).Render(strings.Repeat("─", width-filled))
}

func (model tuiModel) renderServers(width, height int) string {
	rows := []string{tuiTitleStyle.Render("Server fleet") + tuiMutedStyle.Render("  Enter selects · [ ] switches")}
	visible := maxInt(3, height-4)
	start, end := visibleRange(model.cursor, len(model.snapshot.Targets), visible)
	for index := start; index < end; index++ {
		target := model.snapshot.Targets[index]
		state := lipgloss.NewStyle().Foreground(tuiGreen).Render("● online")
		kind := "agent"
		if target.Local {
			kind = "local"
		} else if !target.Online {
			state = lipgloss.NewStyle().Foreground(tuiRed).Render("● offline")
		}
		if !target.Local && target.Inventory.Arch != "" {
			kind += "/" + target.Inventory.Arch
		}
		selected := " "
		if target.ID == model.selectedTargetID {
			selected = "◆"
		}
		row := fmt.Sprintf("%s %-22s %-24s %-13s %s", selected, truncateTUI(target.label(), 22), truncateTUI(target.Hostname, 24), truncateTUI(kind, 13), state)
		rows = append(rows, renderSelectableRow(row, index == model.cursor, width-2))
	}
	if len(model.snapshot.Targets) == 1 {
		rows = append(rows, tuiDimStyle.Render("No managed nodes are enrolled. The panel host remains fully manageable."))
	}
	return tuiPanelStyle.Width(width - 2).Render(strings.Join(rows, "\n"))
}

func (model tuiModel) renderServices(width, height int) string {
	rows := []string{tuiTitleStyle.Render("System services") + tuiMutedStyle.Render("  Enter opens Start / Restart / Stop")}
	visible := maxInt(3, height-4)
	start, end := visibleRange(model.cursor, len(model.snapshot.Services), visible)
	for index := start; index < end; index++ {
		service := model.snapshot.Services[index]
		state := renderState(service.State)
		pid := "—"
		if service.PID > 0 {
			pid = fmt.Sprint(service.PID)
		}
		nameWidth := 30
		if width < 78 {
			nameWidth = 22
		}
		row := fmt.Sprintf("%-*s %-14s PID %-7s %s", nameWidth, truncateTUI(service.Name, nameWidth), state, pid, truncateTUI(service.Detail, maxInt(0, width-nameWidth-34)))
		rows = append(rows, renderSelectableRow(row, index == model.cursor, width-2))
	}
	if len(model.snapshot.Services) == 0 {
		if model.snapshot.ServicesAvailable {
			rows = append(rows, tuiDimStyle.Render("No monitored services were returned by this server."))
		} else {
			rows = append(rows, lipgloss.NewStyle().Foreground(tuiAmber).Render("Service inventory is unavailable; press r to retry."))
		}
	}
	return tuiPanelStyle.Width(width - 2).Render(strings.Join(rows, "\n"))
}

func (model tuiModel) renderProcesses(width, height int) string {
	processes := append([]tuiProcess(nil), model.snapshot.Processes...)
	sortTUIProcesses(processes)
	rows := []string{tuiTitleStyle.Render("Top processes") + tuiMutedStyle.Render("  Enter opens TERM / KILL")}
	visible := maxInt(3, height-4)
	start, end := visibleRange(model.cursor, len(processes), visible)
	commandWidth := maxInt(16, width-48)
	for index := start; index < end; index++ {
		process := processes[index]
		row := fmt.Sprintf("%6d  %-10s %6.1f CPU %6.1f MEM %8s  %s",
			process.PID, truncateTUI(process.User, 10), process.CPU, process.Memory,
			formatTUIBytes(process.RSS), truncateTUI(process.Command, commandWidth),
		)
		rows = append(rows, renderSelectableRow(row, index == model.cursor, width-2))
	}
	if len(processes) == 0 {
		if model.snapshot.ProcessesAvailable {
			rows = append(rows, tuiDimStyle.Render("No processes were returned by this server."))
		} else {
			rows = append(rows, lipgloss.NewStyle().Foreground(tuiAmber).Render("Process inventory is unavailable; press r to retry."))
		}
	}
	return tuiPanelStyle.Width(width - 2).Render(strings.Join(rows, "\n"))
}

func availableTUIValue(available bool, value string) string {
	if !available {
		return "N/A"
	}
	return value
}

func (model tuiModel) renderMaintenance(width, height int) string {
	rows := []string{tuiTitleStyle.Render("Bounded maintenance") + tuiMutedStyle.Render("  Every mutation requires confirmation")}
	for index, action := range tuiMaintenanceActions {
		marker := "  "
		if action.Dangerous {
			marker = lipgloss.NewStyle().Foreground(tuiRed).Render("! ")
		}
		labelWidth := 24
		if width < 76 {
			labelWidth = 20
		}
		row := fmt.Sprintf("%s%-*s %s", marker, labelWidth, action.Label, truncateTUI(action.Description, maxInt(12, width-labelWidth-9)))
		rows = append(rows, renderSelectableRow(row, index == model.cursor, width-2))
	}
	if reason := model.unavailableReason("host.action"); reason != "" {
		rows = append(rows, lipgloss.NewStyle().Foreground(tuiAmber).Render("Unavailable: "+reason))
	}
	return tuiPanelStyle.Width(width - 2).Render(strings.Join(rows, "\n"))
}

func (model tuiModel) renderDisk(width, height int) string {
	selectedSize := uint64(0)
	for _, target := range model.snapshot.CleanupTargets {
		if model.diskSelected[target.ID] {
			selectedSize += target.Size
		}
	}
	header := tuiTitleStyle.Render("Measured cleanup targets") + tuiMutedStyle.Render("  S scan · Space select · X execute")
	rows := []string{header}
	if !model.snapshot.CleanupLoaded {
		rows = append(rows, tuiDimStyle.Render("Press S or Enter to scan server-measured, fixed-scope cleanup targets."))
	} else {
		visible := maxInt(3, height-10)
		start, end := visibleRange(model.cursor, len(model.snapshot.CleanupTargets), visible)
		for index := start; index < end; index++ {
			target := model.snapshot.CleanupTargets[index]
			checked := "[ ]"
			if model.diskSelected[target.ID] {
				checked = lipgloss.NewStyle().Foreground(tuiAccentBright).Bold(true).Render("[×]")
			}
			risk := lipgloss.NewStyle().Foreground(tuiGreen).Render(strings.ToUpper(valueOrNA(target.Risk)))
			if target.Risk == "medium" {
				risk = lipgloss.NewStyle().Foreground(tuiAmber).Render("MEDIUM")
			}
			row := fmt.Sprintf("%s %-20s %10s %-8s %s", checked, truncateTUI(target.Name, 20), formatTUIBytes(target.Size), risk, truncateTUI(target.Description, maxInt(10, width-54)))
			rows = append(rows, renderSelectableRow(row, index == model.cursor, width-2))
		}
		rows = append(rows, tuiMutedStyle.Render(fmt.Sprintf("Selected: %d target(s) · measured size %s", len(model.diskSelected), formatTUIBytes(selectedSize))))
		if len(model.snapshot.CleanupTargets) == 0 {
			rows = append(rows, lipgloss.NewStyle().Foreground(tuiGreen).Render("No reclaimable fixed cleanup targets were reported."))
		}
	}
	rows = append(rows, "")
	rows = append(rows, model.renderDiskDiagnostics(width, height)...)
	return tuiPanelStyle.Width(width - 2).Render(strings.Join(rows, "\n"))
}

func (model tuiModel) renderDiskDiagnostics(width, _ int) []string {
	lineWidth := maxInt(16, width-4)
	fit := func(value string) string { return truncateTUIWidth(value, lineWidth) }
	titleText := "Local disk diagnostics"
	hintText := "m mounts · u usage · w largest · i I/O · p SMART · a status · d start"
	title := tuiTitleStyle.Render(truncateTUIWidth(titleText, lineWidth))
	remaining := lineWidth - lipgloss.Width(titleText) - 2
	if remaining > 0 {
		title += tuiMutedStyle.Render("  " + truncateTUIWidth(hintText, remaining))
	}
	rows := []string{title}
	if !model.snapshot.Selected.Local || !model.diskDiagnostics.Supported {
		note := model.diskDiagnostics.UnsupportedNote
		if note == "" {
			note = "Deep disk diagnostics are unsupported on managed targets; only bounded heartbeat disk data is available."
		}
		rows = append(rows, tuiDimStyle.Render(fit(note)))
		return rows
	}
	if !model.diskDiagnostics.Loaded {
		message := "Diagnostics have not been loaded. Press r to refresh."
		if model.resourceLoading {
			message = "Loading local disk diagnostics…"
		}
		rows = append(rows, tuiDimStyle.Render(fit(message)))
		return rows
	}
	path := valueOrNA(model.diskDiagnostics.Path)
	rows = append(rows, tuiDimStyle.Render(fit("Panel host only · explicit path "+truncateTUI(path, 48))))
	mounts := "unavailable"
	if model.diskDiagnostics.MountsLoaded {
		mounts = tuiDiskDiagnosticCount(len(model.diskDiagnostics.Mounts), tuiDiskMaxMountRows)
	}
	ioStats := "unavailable"
	if model.diskDiagnostics.IOLoaded {
		ioStats = tuiDiskDiagnosticCount(len(model.diskDiagnostics.IO), tuiDiskMaxIORows)
	}
	rows = append(rows, fit("Mounts "+mounts+" · I/O "+ioStats))

	smartStatus := "unavailable"
	if model.diskDiagnostics.SMARTLoaded {
		if model.diskDiagnostics.SMART != nil {
			smartStatus = valueOrNA(model.diskDiagnostics.SMART.Status)
		}
	}
	analysisStatus := "unavailable"
	if model.diskDiagnostics.AnalysisLoaded {
		analysisStatus = valueOrNA(model.diskDiagnostics.Analysis.Status)
	}
	rows = append(rows, fit("SMART "+truncateTUI(smartStatus, 24)+" · Analysis "+truncateTUI(analysisStatus, 24)))
	usage := "not scanned"
	if model.diskDiagnostics.UsageLoaded {
		usage = tuiDiskDiagnosticCount(len(model.diskDiagnostics.Usage), tuiDiskMaxUsageRows)
	}
	rows = append(rows, tuiDimStyle.Render(fit("Usage   "+usage+" · press u for explicit "+truncateTUI(path, 32))))
	largest := "not scanned"
	if model.diskDiagnostics.LargestLoaded {
		largest = tuiDiskDiagnosticCount(len(model.diskDiagnostics.Largest), tuiDiskMaxLargestRows)
	}
	rows = append(rows, tuiDimStyle.Render(fit("Largest "+largest+" · press w for explicit "+truncateTUI(path, 32))))
	for _, warning := range model.diskDiagnostics.Warnings {
		rows = append(rows, lipgloss.NewStyle().Foreground(tuiAmber).Render(fit("! "+warning)))
		if len(rows) >= 12 {
			break
		}
	}
	return rows
}

func tuiDiskDiagnosticCount(count, maximum int) string {
	if count == 0 {
		return "0 rows"
	}
	return fmt.Sprintf("%d row(s), capped at %d", count, maximum)
}

func (model tuiModel) renderContainers(width, height int) string {
	rows := []string{tuiTitleStyle.Render("Containers") + tuiMutedStyle.Render("  Enter opens Start / Restart / Stop · R reload")}
	if !model.containersLoaded {
		message := "Container inventory has not been loaded."
		if model.resourceLoading {
			message = "Loading container inventory…"
		}
		rows = append(rows, tuiDimStyle.Render(message))
		return tuiPanelStyle.Width(width - 2).Render(strings.Join(rows, "\n"))
	}
	visible := maxInt(3, height-4)
	start, end := visibleRange(model.cursor, len(model.containers), visible)
	for index := start; index < end; index++ {
		container := model.containers[index]
		state := renderContainerState(container.State)
		nameWidth := 20
		imageWidth := 28
		if width < 88 {
			nameWidth = 16
			imageWidth = 20
		}
		metrics := ""
		if container.MemoryLimit > 0 {
			metrics = fmt.Sprintf("%5.1f%%  %s/%s", container.CPUPercent, formatTUIBytes(container.MemoryUsage), formatTUIBytes(container.MemoryLimit))
		} else if container.CPUPercent > 0 || container.MemoryUsage > 0 {
			metrics = fmt.Sprintf("%5.1f%%  %s", container.CPUPercent, formatTUIBytes(container.MemoryUsage))
		}
		detail := strings.TrimSpace(strings.Join([]string{container.Ports, metrics}, "  "))
		if detail == "" {
			detail = container.Detail
		}
		row := fmt.Sprintf("%-*s  %-*s  %-12s  %s",
			nameWidth, truncateTUI(container.Name, nameWidth),
			imageWidth, truncateTUI(container.Image, imageWidth),
			state, truncateTUI(detail, maxInt(10, width-nameWidth-imageWidth-23)),
		)
		rows = append(rows, renderSelectableRow(row, index == model.cursor, width-2))
	}
	if len(model.containers) == 0 {
		rows = append(rows, tuiDimStyle.Render("No containers were reported by this server."))
	}
	return tuiPanelStyle.Width(width - 2).Render(strings.Join(rows, "\n"))
}

func (model tuiModel) renderLogs(width, height int) string {
	rows := []string{tuiTitleStyle.Render("Readable logs") + tuiMutedStyle.Render("  Enter opens latest 200 lines · R rediscover")}
	if !model.logSourcesLoaded {
		message := "Log sources have not been discovered."
		if model.resourceLoading {
			message = "Discovering readable log sources…"
		}
		rows = append(rows, tuiDimStyle.Render(message))
		return tuiPanelStyle.Width(width - 2).Render(strings.Join(rows, "\n"))
	}
	visible := maxInt(3, height-4)
	start, end := visibleRange(model.cursor, len(model.logSources), visible)
	for index := start; index < end; index++ {
		source := model.logSources[index]
		labelWidth := 28
		if width < 76 {
			labelWidth = 20
		}
		category := valueOrNA(source.Category)
		detail := source.Detail
		if detail == "" && !model.snapshot.Selected.Local {
			detail = "managed agent source"
		}
		row := fmt.Sprintf("%-*s  %-14s  %s",
			labelWidth, truncateTUI(source.Label, labelWidth),
			truncateTUI(category, 14), truncateTUI(detail, maxInt(10, width-labelWidth-23)),
		)
		rows = append(rows, renderSelectableRow(row, index == model.cursor, width-2))
	}
	if len(model.logSources) == 0 {
		rows = append(rows, tuiDimStyle.Render("No readable log sources were reported by this server."))
	}
	return tuiPanelStyle.Width(width - 2).Render(strings.Join(rows, "\n"))
}

func (model tuiModel) renderPM2(width, height int) string {
	rows := []string{tuiTitleStyle.Render("PM2 applications") + tuiMutedStyle.Render("  Enter actions · V logs · R reload")}
	if !model.pm2Loaded {
		message := "PM2 process inventory has not been loaded."
		if model.resourceLoading {
			message = "Loading PM2 process inventory…"
		}
		rows = append(rows, tuiDimStyle.Render(message))
		return tuiPanelStyle.Width(width - 2).Render(strings.Join(rows, "\n"))
	}
	visible := maxInt(3, height-4)
	start, end := visibleRange(model.cursor, len(model.pm2Processes), visible)
	for index := start; index < end; index++ {
		process := model.pm2Processes[index]
		nameWidth := 24
		if width < 82 {
			nameWidth = 18
		}
		state := renderPM2State(process.Status)
		detail := fmt.Sprintf("PID %-7d %5.1f%% %9s  up %-10s  ↻ %d  %s",
			process.PID, process.CPUPercent, formatTUIBytes(process.MemoryBytes),
			formatTUIUptime(process.UptimeSeconds), process.Restarts, valueOrNA(process.Mode),
		)
		row := fmt.Sprintf("%-*s  %-11s  %s",
			nameWidth, truncateTUI(process.Name, nameWidth), state,
			truncateTUI(detail, maxInt(16, width-nameWidth-18)),
		)
		rows = append(rows, renderSelectableRow(row, index == model.cursor, width-2))
	}
	if len(model.pm2Processes) == 0 {
		rows = append(rows, tuiDimStyle.Render("No PM2 processes were reported by this server."))
	}
	return tuiPanelStyle.Width(width - 2).Render(strings.Join(rows, "\n"))
}

func (model tuiModel) renderWeb(width, height int) string {
	rows := []string{tuiTitleStyle.Render("Web operations") + tuiMutedStyle.Render("  Nginx · Domains · SSL  · Enter actions · R reload")}
	if !model.webLoaded {
		message := "Web resource inventory has not been loaded."
		if model.resourceLoading {
			message = "Loading Nginx, domain, and SSL inventory…"
		}
		rows = append(rows, tuiDimStyle.Render(message))
		return tuiPanelStyle.Width(width - 2).Render(strings.Join(rows, "\n"))
	}
	visible := maxInt(3, height-4-len(model.webWarnings))
	start, end := visibleRange(model.cursor, len(model.webResources), visible)
	for index := start; index < end; index++ {
		resource := model.webResources[index]
		kind := strings.ToUpper(string(resource.Kind))
		nameWidth := 26
		if width < 86 {
			nameWidth = 18
		}
		row := fmt.Sprintf("%-7s  %-*s  %-10s  %s",
			kind, nameWidth, truncateTUI(resource.Name, nameWidth), renderWebState(resource.State),
			truncateTUI(resource.Detail, maxInt(10, width-nameWidth-36)),
		)
		rows = append(rows, renderSelectableRow(row, index == model.cursor, width-2))
	}
	if len(model.webResources) == 0 {
		rows = append(rows, tuiDimStyle.Render("No Nginx, domain, or SSL resources were reported by this server."))
	}
	for _, warning := range model.webWarnings {
		rows = append(rows, lipgloss.NewStyle().Foreground(tuiAmber).Render("! "+truncateTUI(warning, width-4)))
	}
	return tuiPanelStyle.Width(width - 2).Render(strings.Join(rows, "\n"))
}

func (model tuiModel) renderDNS(width, height int) string {
	hint := "A create zone · C check · T reload · Enter records · X delete · R reload"
	if model.dns.Detail != nil {
		hint = "A add record · E edit · C check · T reload · X delete · Backspace zones"
	}
	rows := []string{tuiTitleStyle.Render("Local BIND DNS") + tuiMutedStyle.Render("  "+hint)}
	if !model.dnsLoaded {
		message := "Local DNS state has not been loaded."
		if model.resourceLoading {
			message = "Loading BIND readiness and zone inventory…"
		}
		rows = append(rows, tuiDimStyle.Render(message))
		return tuiPanelStyle.Width(width - 2).Render(strings.Join(rows, "\n"))
	}
	if !model.dns.Supported {
		rows = append(rows,
			lipgloss.NewStyle().Foreground(tuiAmber).Render("Not available for this target"),
			tuiDimStyle.Render(truncateTUI(model.dns.Message, width-4)),
		)
		return tuiPanelStyle.Width(width - 2).Render(strings.Join(rows, "\n"))
	}

	status := model.dns.Status
	tone := tuiGreen
	switch string(status.State) {
	case "healthy":
		tone = tuiGreen
	case "not-configured", "not-installed", "stopped":
		tone = tuiAmber
	default:
		tone = tuiRed
	}
	stateLabel := lipgloss.NewStyle().Bold(true).Foreground(tone).Render(strings.ToUpper(valueOrNA(string(status.State))))
	readiness := "read-only"
	if status.ZoneManagementReady {
		readiness = "zone management ready"
	}
	reload := "reload unavailable"
	if status.ReloadAvailable {
		reload = "reload ready"
	}
	summary := fmt.Sprintf("%s · service %s · %s · %s", stateLabel, valueOrNA(status.ServiceState), readiness, reload)
	if status.Version != "" {
		summary += " · " + status.Version
	}
	rows = append(rows, truncateANSI(summary, width-4))
	if status.Error != "" {
		rows = append(rows, lipgloss.NewStyle().Foreground(tuiAmber).Render("! "+truncateTUI(status.Error, width-6)))
	}
	if model.dns.Check != nil {
		check := "last check passed"
		color := tuiGreen
		if !model.dns.Check.OK {
			check, color = "last check failed", tuiRed
		}
		rows = append(rows, lipgloss.NewStyle().Foreground(color).Render(check))
	}
	for _, warning := range model.dns.Warnings {
		rows = append(rows, lipgloss.NewStyle().Foreground(tuiAmber).Render("! "+truncateTUI(warning, width-6)))
	}

	if model.dns.Detail == nil {
		visible := maxInt(3, height-len(rows)-3)
		start, end := visibleRange(model.cursor, len(model.dns.Zones), visible)
		for index := start; index < end; index++ {
			zone := model.dns.Zones[index]
			row := fmt.Sprintf("%-38s serial %-12d %4d record(s)  %s",
				truncateTUI(zone.Domain, 38), zone.Serial, zone.RecordCount, zone.File,
			)
			rows = append(rows, renderSelectableRow(truncateTUI(row, width-3), index == model.cursor, width-2))
		}
		if len(model.dns.Zones) == 0 {
			message := "No local BIND zones were reported."
			if !status.ConfigAvailable {
				message = "BIND configuration is not available; zone inventory is intentionally not queried."
			}
			rows = append(rows, tuiDimStyle.Render(message))
		}
		return tuiPanelStyle.Width(width - 2).Render(strings.Join(rows, "\n"))
	}

	detail := model.dns.Detail
	rows = append(rows, tuiMutedStyle.Render(fmt.Sprintf("Zone %s · serial %d · %d record(s)", detail.Domain, detail.Serial, len(detail.Records))))
	visible := maxInt(3, height-len(rows)-3)
	start, end := visibleRange(model.cursor, len(detail.Records), visible)
	for index := start; index < end; index++ {
		record := detail.Records[index]
		ttl := valueOrNA(record.TTL)
		value := record.Value
		if record.Priority > 0 {
			value = fmt.Sprintf("%d %s", record.Priority, value)
		}
		row := fmt.Sprintf("%-22s %-8s %-7s %s", truncateTUI(record.Name, 22), truncateTUI(record.Type, 8), truncateTUI(ttl, 7), value)
		rows = append(rows, renderSelectableRow(truncateTUI(row, width-3), index == model.cursor, width-2))
	}
	if len(detail.Records) == 0 {
		rows = append(rows, tuiDimStyle.Render("This zone has no parsed resource records."))
	}
	return tuiPanelStyle.Width(width - 2).Render(strings.Join(rows, "\n"))
}

func (model tuiModel) renderFirewall(width, height int) string {
	hint := "A add common rule · Enter delete · R reload"
	if model.snapshot.Selected.Local {
		hint = "A add common rule · T enable/disable · Enter delete · R reload"
	}
	rows := []string{tuiTitleStyle.Render("Firewall") + tuiMutedStyle.Render("  "+hint)}
	if !model.firewallLoaded {
		message := "Firewall inventory has not been loaded."
		if model.resourceLoading {
			message = "Loading observed firewall policy and rules…"
		}
		rows = append(rows, tuiDimStyle.Render(message))
		return tuiPanelStyle.Width(width - 2).Render(strings.Join(rows, "\n"))
	}
	mode := "read-only"
	if model.firewall.Manageable {
		mode = "manageable"
	}
	state := model.firewall.State
	if model.snapshot.Selected.Local {
		if model.firewall.Active {
			state = "active"
		} else if state == "healthy" {
			state = "inactive"
		}
	}
	summary := fmt.Sprintf("backend %s · %s · %s", valueOrNA(model.firewall.Backend), valueOrNA(state), mode)
	if model.snapshot.Selected.Local {
		summary += fmt.Sprintf(" · defaults in %s / out %s", valueOrNA(model.firewall.DefaultIncoming), valueOrNA(model.firewall.DefaultOutgoing))
	} else {
		summary += fmt.Sprintf(" · policy %s · persistence %s", valueOrNA(model.firewall.Policy), valueOrNA(model.firewall.Persistence))
		if len(model.firewall.ProtectedPorts) > 0 {
			ports := make([]string, 0, len(model.firewall.ProtectedPorts))
			for _, port := range model.firewall.ProtectedPorts {
				ports = append(ports, fmt.Sprint(port))
			}
			summary += " · protected " + strings.Join(ports, ",")
		}
	}
	rows = append(rows, tuiDimStyle.Render(truncateTUI(summary, width-4)))
	visible := maxInt(3, height-5)
	start, end := visibleRange(model.cursor, len(model.firewall.Rules), visible)
	for index := start; index < end; index++ {
		rule := model.firewall.Rules[index]
		identity := rule.ID
		if identity == "" && rule.Number > 0 {
			identity = fmt.Sprint(rule.Number)
		}
		ownership := "observed"
		if rule.Managed {
			ownership = "managed"
		}
		row := fmt.Sprintf("%-16s %-7s %-4s %-10s from %-20s %s",
			truncateTUI(identity, 16), truncateTUI(rule.Action, 7), truncateTUI(rule.Direction, 4),
			truncateTUI(valueOrNA(rule.Target)+"/"+valueOrNA(rule.Protocol), 10), truncateTUI(valueOrNA(rule.Source), 20), ownership,
		)
		if strings.TrimSpace(rule.Comment) != "" {
			row += " · " + rule.Comment
		}
		rows = append(rows, renderSelectableRow(truncateTUI(row, width-3), index == model.cursor, width-2))
	}
	if len(model.firewall.Rules) == 0 {
		rows = append(rows, tuiDimStyle.Render("No firewall rules were reported by this server."))
	}
	if !model.firewall.Manageable {
		rows = append(rows, lipgloss.NewStyle().Foreground(tuiAmber).Render("! Rules remain visible, but mutations are unavailable for this backend or capability set."))
	}
	return tuiPanelStyle.Width(width - 2).Render(strings.Join(rows, "\n"))
}

func (model tuiModel) renderBackups(width, height int) string {
	titleHint := "Enter actions · R reload"
	if model.snapshot.Selected.Local {
		titleHint = "C create backup · Enter job/artifact details · R reload inventory + schedules"
	}
	rows := []string{tuiTitleStyle.Render("Backups") + tuiMutedStyle.Render("  "+titleHint)}
	if !model.backupsLoaded {
		message := "Backup inventory has not been loaded."
		if model.resourceLoading {
			message = "Loading backup artifacts and managed plans…"
		}
		rows = append(rows, tuiDimStyle.Render(message))
		return tuiPanelStyle.Width(width - 2).Render(strings.Join(rows, "\n"))
	}
	if model.snapshot.Selected.Local {
		storage := fmt.Sprintf("stored %s · invalid %s · orphaned %s · volume %.1f%% used · %s available",
			formatTUIBytes(uint64(maxInt64(model.backupStorage.CompletedBytes, 0))),
			formatTUIBytes(uint64(maxInt64(model.backupStorage.InvalidBytes, 0))),
			formatTUIBytes(uint64(maxInt64(model.backupStorage.OrphanedBytes, 0))),
			model.backupStorage.BackupUsePercentage, formatTUIBytes(model.backupStorage.BackupAvailable),
		)
		rows = append(rows, tuiDimStyle.Render(truncateTUI(storage, width-4)))
	} else {
		// The central schedule endpoint is local to the panel host.  Managed
		// targets expose their own plan/timer rows instead; do not imply that
		// panel-owned schedules were queried for a remote node.
		rows = append(rows, tuiDimStyle.Render(truncateTUI("Panel-owned backup schedules: not configured for managed targets; managed plan timers are shown below.", width-4)))
	}
	for _, warning := range model.backupWarnings {
		if strings.HasPrefix(warning, tuiBackupScheduleDisplayPrefix) || strings.HasPrefix(warning, tuiBackupScheduleStatePrefix+" empty") {
			rows = append(rows, tuiDimStyle.Render(truncateTUI(warning, width-4)))
			continue
		}
		rows = append(rows, lipgloss.NewStyle().Foreground(tuiAmber).Render("! "+truncateTUI(warning, width-6)))
	}
	total := len(model.backupJobs) + len(model.backups)
	visible := maxInt(3, height-5-len(model.backupWarnings))
	start, end := visibleRange(model.cursor, total, visible)
	for index := start; index < end; index++ {
		nameWidth := 28
		if width < 88 {
			nameWidth = 20
		}
		if index < len(model.backupJobs) {
			job := model.backupJobs[index]
			progress := minInt(100, maxInt(0, job.Progress))
			detail := fmt.Sprintf("%3d%% · %-10s · %s", progress, valueOrNA(job.Phase), valueOrNA(job.Message))
			if job.active() && job.ETASeconds > 0 {
				detail = fmt.Sprintf("%3d%% · %-10s · ETA %s · %s", progress, valueOrNA(job.Phase), compactDuration(time.Duration(job.ETASeconds)*time.Second), valueOrNA(job.Message))
			}
			name := "JOB " + valueOrNA(job.Type)
			row := fmt.Sprintf("%-*s  %-11s  %s", nameWidth, truncateTUI(name, nameWidth), renderBackupState(job.Status, false), truncateTUI(detail, maxInt(12, width-nameWidth-20)))
			rows = append(rows, renderSelectableRow(row, index == model.cursor, width-2))
			continue
		}
		item := model.backups[index-len(model.backupJobs)]
		state := renderBackupState(item.Status, item.Verified)
		detail := ""
		if item.Managed {
			detail = fmt.Sprintf("%s · %s · %d file(s) · next %s", formatTUIBytes(uint64(maxInt64(item.Size, 0))), valueOrNA(item.Enabled), item.FileCount, formatBackupSchedule(item.NextRun))
		} else {
			detail = fmt.Sprintf("%-8s · %s · %s", strings.ToUpper(valueOrNA(item.Type)), formatTUIBytes(uint64(maxInt64(item.Size, 0))), formatBackupCreatedAt(item.CreatedAt))
		}
		row := fmt.Sprintf("%-*s  %-11s  %s", nameWidth, truncateTUI(item.Name, nameWidth), state, truncateTUI(detail, maxInt(12, width-nameWidth-20)))
		rows = append(rows, renderSelectableRow(row, index == model.cursor, width-2))
	}
	if total == 0 {
		rows = append(rows, tuiDimStyle.Render("No backup artifacts or managed plans were reported."))
	}
	return tuiPanelStyle.Width(width - 2).Render(strings.Join(rows, "\n"))
}

func (model tuiModel) renderEncryptedSnapshots(width, height int) string {
	rows := []string{tuiTitleStyle.Render("Encrypted snapshots") + tuiMutedStyle.Render("  C create · D destination · Enter restore to staging · R reload")}
	if !model.encryptedSnapshotsLoaded {
		message := "Encrypted snapshot state has not been loaded."
		if model.resourceLoading {
			message = "Probing the encrypted snapshot repository…"
		}
		rows = append(rows, tuiDimStyle.Render(message))
		return tuiPanelStyle.Width(width - 2).Render(strings.Join(rows, "\n"))
	}
	state := model.encryptedSnapshots
	if !state.Supported {
		rows = append(rows,
			lipgloss.NewStyle().Foreground(tuiAmber).Render("Not available for this target"),
			tuiDimStyle.Render(truncateTUI(state.DestinationMessage, width-4)),
		)
		return tuiPanelStyle.Width(width - 2).Render(strings.Join(rows, "\n"))
	}
	statusTone := tuiGreen
	if state.DestinationStatus == "unavailable" {
		statusTone = tuiRed
	} else if state.DestinationStatus != "healthy" {
		statusTone = tuiAmber
	}
	status := lipgloss.NewStyle().Bold(true).Foreground(statusTone).Render(strings.ToUpper(valueOrNA(state.DestinationStatus)))
	repo := "not initialized"
	if state.RepoInitialized {
		repo = "initialized"
	}
	password := "missing"
	if state.PasswordSet {
		password = "protected"
	}
	restic := "missing"
	if state.ResticFound {
		restic = "available"
	}
	rows = append(rows, fmt.Sprintf("%-19s %s  %s", snapshotDestinationLabel(state.Destination), status, tuiDimStyle.Render("restic "+restic+" · password "+password+" · repo "+repo)))
	if state.RepoStats != nil {
		stats := fmt.Sprintf("%d snapshot(s) · %s encrypted repository data · %s logical files",
			state.RepoStats.SnapshotCount,
			formatTUIBytes(uint64(maxInt64(state.RepoStats.TotalSize, 0))),
			formatTUIBytes(uint64(maxInt64(state.RepoStats.TotalFileSize, 0))),
		)
		rows = append(rows, tuiDimStyle.Render(truncateTUI(stats, width-4)))
	}
	if state.DestinationMessage != "" && state.DestinationStatus != "healthy" {
		rows = append(rows, lipgloss.NewStyle().Foreground(tuiAmber).Render("! "+truncateTUI(state.DestinationMessage, width-6)))
	}
	for index, warning := range state.Warnings {
		if index >= 2 {
			break
		}
		rows = append(rows, lipgloss.NewStyle().Foreground(tuiAmber).Render("! "+truncateTUI(warning, width-6)))
	}
	visible := maxInt(3, height-len(rows)-3)
	start, end := visibleRange(model.cursor, len(state.Snapshots), visible)
	for index := start; index < end; index++ {
		snapshot := state.Snapshots[index]
		identityWidth := 18
		if width < 82 {
			identityWidth = 12
		}
		created := "unknown time"
		if !snapshot.Time.IsZero() {
			created = snapshot.Time.Local().Format("2006-01-02 15:04")
		}
		detail := fmt.Sprintf("%s · %s · %d path(s)", created, valueOrNA(snapshot.Hostname), snapshot.Paths)
		if snapshot.Size > 0 {
			detail += " · " + formatTUIBytes(uint64(snapshot.Size))
		}
		row := fmt.Sprintf("%-*s  %s", identityWidth, truncateTUI(snapshot.ID, identityWidth), truncateTUI(detail, maxInt(16, width-identityWidth-6)))
		rows = append(rows, renderSelectableRow(row, index == model.cursor, width-2))
	}
	if len(state.Snapshots) == 0 {
		message := "No encrypted snapshots have been created yet."
		if !state.ready() {
			message = "Snapshot creation remains disabled until restic, password, and the selected provider are healthy."
		}
		rows = append(rows, tuiDimStyle.Render(message))
	}
	return tuiPanelStyle.Width(width - 2).Render(strings.Join(rows, "\n"))
}

func (model tuiModel) renderCron(width, height int) string {
	rows := []string{tuiTitleStyle.Render("Cron jobs") + tuiMutedStyle.Render("  Enter actions · R reload · scripted CLI creates and fully edits jobs")}
	if !model.cronLoaded {
		message := "Cron inventory has not been loaded."
		if model.resourceLoading {
			message = "Loading scheduled jobs and cron service state…"
		}
		rows = append(rows, tuiDimStyle.Render(message))
		return tuiPanelStyle.Width(width - 2).Render(strings.Join(rows, "\n"))
	}
	mode := "read-only"
	if model.cron.Manageable {
		mode = "manageable"
	}
	runMode := "scheduled only"
	if model.cron.Runnable {
		runMode = "manual run enabled"
	}
	summary := fmt.Sprintf("service %s · %s · %s · %d job(s)", valueOrNA(model.cron.Service), mode, runMode, len(model.cron.Jobs))
	if len(model.cron.Sources) > 0 {
		managedSources := 0
		for _, source := range model.cron.Sources {
			if source.Managed {
				managedSources++
			}
		}
		summary += fmt.Sprintf(" · %d source(s), %d managed", len(model.cron.Sources), managedSources)
	}
	rows = append(rows, tuiDimStyle.Render(truncateTUI(summary, width-4)))
	visible := maxInt(3, height-5)
	start, end := visibleRange(model.cursor, len(model.cron.Jobs), visible)
	for index := start; index < end; index++ {
		job := model.cron.Jobs[index]
		state := "enabled"
		if !job.Enabled {
			state = "disabled"
		}
		row := fmt.Sprintf("%-17s %-12s %-10s %-8s %s", truncateTUI(job.ID, 17), truncateTUI(job.Schedule, 12), truncateTUI(job.User, 10), state, job.Command)
		if strings.TrimSpace(job.Description) != "" {
			row += " · " + job.Description
		}
		rows = append(rows, renderSelectableRow(truncateTUI(row, width-3), index == model.cursor, width-2))
	}
	if len(model.cron.Jobs) == 0 {
		rows = append(rows, tuiDimStyle.Render("No cron jobs were reported by this server."))
	}
	if !model.cron.Manageable {
		rows = append(rows, lipgloss.NewStyle().Foreground(tuiAmber).Render("! Jobs remain visible, but mutations are unavailable for this runtime or capability set."))
	}
	return tuiPanelStyle.Width(width - 2).Render(strings.Join(rows, "\n"))
}

func (model tuiModel) renderSecurity(width, height int) string {
	rows := []string{tuiTitleStyle.Render("Security") + tuiMutedStyle.Render("  a add local IP · Enter unban/delete selected IP · S jump · R reload")}
	if !model.securityLoaded {
		message := "Security inventory has not been loaded."
		if model.resourceLoading {
			message = "Loading security score, Fail2Ban, and local IP access lists…"
		}
		rows = append(rows, tuiDimStyle.Render(message))
		return tuiPanelStyle.Width(width - 2).Render(strings.Join(rows, "\n"))
	}
	if !model.security.Supported {
		rows = append(rows,
			lipgloss.NewStyle().Foreground(tuiAmber).Render("! Managed-node security inventory is not currently an agent capability."),
			tuiDimStyle.Render(truncateTUI(model.security.UnsupportedNote, width-4)),
		)
		return tuiPanelStyle.Width(width - 2).Render(strings.Join(rows, "\n"))
	}
	if model.security.ScoreLoaded {
		rows = append(rows, tuiDimStyle.Render(fmt.Sprintf("Security score %d/%d · %d check(s)", model.security.Score.Score, model.security.Score.MaxScore, len(model.security.Score.Checks))))
	} else {
		rows = append(rows, tuiDimStyle.Render("Security score unavailable"))
	}
	if model.security.Fail2BanLoaded {
		status := fmt.Sprintf("Fail2Ban %s · daemon %s · %d jail(s)", valueOrNA(model.security.Fail2Ban.State), valueOrNA(model.security.Fail2Ban.DaemonState), len(model.security.Fail2Ban.Jails))
		if model.security.Fail2Ban.Error != "" {
			status += " · " + model.security.Fail2Ban.Error
		}
		rows = append(rows, tuiDimStyle.Render(truncateTUI(status, width-4)))
	} else {
		rows = append(rows, tuiDimStyle.Render("Fail2Ban inventory unavailable"))
	}
	if model.security.AccessListsLoaded {
		access := model.security.AccessLists
		accessSummary := fmt.Sprintf("Persistent local IP access lists · blacklist %d · whitelist %d", len(access.Blacklist), len(access.Whitelist))
		if len(access.Blacklist) > tuiSecurityAccessDefaultMaxRows || len(access.Whitelist) > tuiSecurityAccessDefaultMaxRows {
			accessSummary += fmt.Sprintf(" · showing first %d per list", tuiSecurityAccessDefaultMaxRows)
		}
		rows = append(rows, tuiDimStyle.Render(accessSummary))
		if access.BlacklistLoaded && len(access.Blacklist) == 0 {
			rows = append(rows, tuiDimStyle.Render("BLACK  No entries."))
		}
		if access.WhitelistLoaded && len(access.Whitelist) == 0 {
			rows = append(rows, tuiDimStyle.Render("WHITE  No entries."))
		}
	}
	visible := maxInt(3, height-10-len(model.security.Warnings))
	start, end := visibleRange(model.cursor, len(model.security.Items), visible)
	for index := start; index < end; index++ {
		item := model.security.Items[index]
		row := ""
		switch item.Kind {
		case tuiSecurityCheckItem:
			row = fmt.Sprintf("CHECK  %-8s %-22s %s", strings.ToUpper(valueOrNA(item.Status)), truncateTUI(item.Name, 22), item.Detail)
		case tuiSecurityJailItem:
			row = fmt.Sprintf("JAIL   %-24s failed %d · banned %d · total %d", truncateTUI(item.Jail, 24), item.CurrentlyFailed, item.CurrentlyBanned, item.TotalBanned)
		case tuiSecurityBannedIPItem:
			row = fmt.Sprintf("  IP   %-39s jail %s · Enter to unban", item.IP, item.Jail)
		case tuiSecurityBlacklistItem, tuiSecurityWhitelistItem:
			listLabel := "BLACK"
			if item.Kind == tuiSecurityWhitelistItem {
				listLabel = "WHITE"
			}
			entry := item.AccessEntry
			if entry.IP == "" {
				entry.IP = item.IP
			}
			entryRows := renderTUISecurityAccessListRows([]cliSecurityIPEntry{entry}, width-10, 1)
			row = listLabel + "  " + entryRows[0] + " · Enter to delete"
		}
		rows = append(rows, renderSelectableRow(truncateTUIWidth(row, width-3), index == model.cursor, width-2))
	}
	if len(model.security.Items) == 0 && !model.security.AccessListsLoaded {
		rows = append(rows, tuiDimStyle.Render("No security checks or complete Fail2Ban jail inventory were reported."))
	}
	for _, warning := range model.security.Warnings {
		rows = append(rows, lipgloss.NewStyle().Foreground(tuiAmber).Render("! "+truncateTUI(warning, width-4)))
	}
	if model.security.Fail2BanLoaded && (!model.security.Fail2Ban.Available || model.security.Fail2Ban.State != "healthy") {
		rows = append(rows, lipgloss.NewStyle().Foreground(tuiAmber).Render("! Fail2Ban IP actions remain disabled until readiness and complete jail inventory are healthy."))
	}
	rows = append(rows, tuiDimStyle.Render("New bans use: hserverctl security fail2ban ban --confirm JAIL IP · access-list changes require Y"))
	return tuiPanelStyle.Width(width - 2).Render(strings.Join(rows, "\n"))
}

func (model tuiModel) renderDatabases(width, height int) string {
	hint := "Enter drops an observed local database · R reload · scripted CLI creates, inspects tables, and runs read-only queries"
	if !model.snapshot.Selected.Local {
		hint = "Enter restarts an engine with health check · database/session rows are read-only · R reload"
	}
	rows := []string{tuiTitleStyle.Render("Databases") + tuiMutedStyle.Render("  "+hint)}
	if !model.databasesLoaded {
		message := "Database inventory has not been loaded."
		if model.resourceLoading {
			message = "Loading database engines, databases, and connection state…"
		}
		rows = append(rows, tuiDimStyle.Render(message))
		return tuiPanelStyle.Width(width - 2).Render(strings.Join(rows, "\n"))
	}
	mode := "read-only"
	if model.snapshot.Selected.Local && model.databases.Manageable {
		mode = "local create/drop available"
	} else if !model.snapshot.Selected.Local && model.databases.Restartable {
		mode = "engine restart available"
	}
	engineCount, databaseCount := 0, 0
	for _, item := range model.databases.Items {
		if item.Kind == tuiDatabaseEngineItem {
			engineCount++
		} else {
			databaseCount++
		}
	}
	rows = append(rows, tuiDimStyle.Render(fmt.Sprintf("%d engine(s) · %d database(s) · %s", engineCount, databaseCount, mode)))
	visible := maxInt(3, height-5)
	start, end := visibleRange(model.cursor, len(model.databases.Items), visible)
	for index := start; index < end; index++ {
		item := model.databases.Items[index]
		row := ""
		if item.Kind == tuiDatabaseEngineItem {
			detail := valueOrNA(item.Active)
			if item.Version != "" {
				detail += " · " + item.Version
			}
			if item.SizeBytes > 0 {
				detail += " · " + formatTUIBytes(uint64(item.SizeBytes))
			}
			if item.SessionCount > 0 {
				detail += fmt.Sprintf(" · %d session(s)", item.SessionCount)
			}
			row = fmt.Sprintf("ENGINE  %-12s %s", truncateTUI(item.EngineName, 12), detail)
			if item.Unit != "" {
				row += " · " + item.Unit
			}
		} else {
			size := item.SizeText
			if size == "" && item.SizeBytes > 0 {
				size = formatTUIBytes(uint64(item.SizeBytes))
			}
			row = fmt.Sprintf("  DB    %-12s %-24s %-10s", truncateTUI(item.EngineName, 12), truncateTUI(item.Name, 24), valueOrNA(size))
			if model.snapshot.Selected.Local {
				row += fmt.Sprintf(" · owner %s · %d table(s)", valueOrNA(item.Owner), item.Tables)
			} else {
				row += fmt.Sprintf(" · %d connection(s) · %d object(s)", item.Connections, item.Objects)
			}
		}
		rows = append(rows, renderSelectableRow(truncateTUI(row, width-3), index == model.cursor, width-2))
	}
	if len(model.databases.Items) == 0 {
		rows = append(rows, tuiDimStyle.Render("No supported database engine or database was reported by this server."))
	}
	sourceEngines := make([]string, 0, len(model.databases.Sources))
	for engine := range model.databases.Sources {
		sourceEngines = append(sourceEngines, engine)
	}
	sort.Strings(sourceEngines)
	for _, engine := range sourceEngines {
		source := model.databases.Sources[engine]
		if source.Available {
			continue
		}
		message := engine + " · " + valueOrNA(source.State)
		if source.Error != "" {
			message += " · " + source.Error
		}
		rows = append(rows, lipgloss.NewStyle().Foreground(tuiAmber).Render("! "+truncateTUI(message, width-4)))
	}
	return tuiPanelStyle.Width(width - 2).Render(strings.Join(rows, "\n"))
}

func (model tuiModel) renderFiles(width, height int) string {
	rows := []string{tuiTitleStyle.Render("Files") + tuiMutedStyle.Render("  Enter open · U parent · ,/. roots · X delete local · R reload")}
	if !model.filesLoaded {
		message := "File roots and directory inventory have not been loaded."
		if model.resourceLoading {
			message = "Loading configured file roots and directory inventory…"
		}
		rows = append(rows, tuiDimStyle.Render(message))
		return tuiPanelStyle.Width(width - 2).Render(strings.Join(rows, "\n"))
	}
	mode := "read-only browser"
	if model.snapshot.Selected.Local && model.files.Manageable {
		mode = "local browser · confirmed recursive delete available"
	} else if model.snapshot.Selected.capability(agenthub.CapabilityFilesWrite) && len(model.snapshot.Selected.Inventory.FileWriteRoots) > 0 {
		mode = "remote browser · checksum-protected edits available in scripted CLI"
	}
	rows = append(rows,
		tuiDimStyle.Render(truncateTUI(fmt.Sprintf("%s · root %s · %d configured root(s)", mode, model.files.CurrentRoot, len(model.files.Roots)), width-4)),
		tuiMutedStyle.Render(truncateTUI(model.files.CurrentPath, width-4)),
	)
	visible := maxInt(3, height-6)
	start, end := visibleRange(model.cursor, len(model.files.Entries), visible)
	for index := start; index < end; index++ {
		entry := model.files.Entries[index]
		kind := "FILE"
		if entry.Type == "directory" {
			kind = "DIR "
		} else if entry.Type == "symlink" {
			kind = "LINK"
		}
		modeText := entry.Mode
		if modeText == "" {
			modeText = entry.Permissions
		}
		size := formatTUIBytes(uint64(maxInt64(entry.Size, 0)))
		if entry.Type == "directory" {
			size = "—"
		}
		row := fmt.Sprintf("%-4s  %-34s  %-11s  %s", kind, truncateTUI(entry.Name, 34), size, valueOrNA(modeText))
		if entry.Owner != "" {
			row += " · " + entry.Owner
			if entry.Group != "" {
				row += ":" + entry.Group
			}
		}
		rows = append(rows, renderSelectableRow(truncateTUI(row, width-3), index == model.cursor, width-2))
	}
	if len(model.files.Entries) == 0 {
		rows = append(rows, tuiDimStyle.Render("This directory is empty."))
	}
	if !model.snapshot.Selected.Local {
		rows = append(rows, tuiDimStyle.Render("Remote create, rename, and delete are not agent capabilities; existing text saves require files save --checksum."))
	}
	return tuiPanelStyle.Width(width - 2).Render(strings.Join(rows, "\n"))
}

func (model tuiModel) renderPHP(width, height int) string {
	rows := []string{tuiTitleStyle.Render("PHP-FPM") + tuiMutedStyle.Render("  Enter version actions / pool config · P jump · R reload")}
	if !model.phpLoaded {
		message := "PHP-FPM inventory has not been loaded."
		if model.resourceLoading {
			message = "Loading PHP-FPM versions and pool inventory…"
		}
		rows = append(rows, tuiDimStyle.Render(message))
		return tuiPanelStyle.Width(width - 2).Render(strings.Join(rows, "\n"))
	}
	versionCount, poolCount := 0, 0
	for _, item := range model.php.Items {
		if item.Kind == tuiPHPVersionItem {
			versionCount++
		} else {
			poolCount++
		}
	}
	mode := "read-only"
	if model.php.Actionable {
		mode = "lifecycle actions available"
	}
	if model.php.Writable {
		mode += " · checksum-protected pool saves via scripted CLI"
	}
	rows = append(rows, tuiDimStyle.Render(fmt.Sprintf("%d version(s) · %d pool(s) · %s", versionCount, poolCount, mode)))
	visible := maxInt(3, height-5)
	start, end := visibleRange(model.cursor, len(model.php.Items), visible)
	for index := start; index < end; index++ {
		item := model.php.Items[index]
		row := ""
		if item.Kind == tuiPHPVersionItem {
			state := valueOrNA(item.Active)
			if item.Masked {
				state = "masked"
			} else if !item.Runtime {
				state += " · runtime missing"
			}
			row = fmt.Sprintf("PHP   %-7s %-12s %-12s", item.Version, state, valueOrNA(item.Enabled))
			if item.Unit != "" {
				row += " · " + item.Unit
			}
		} else {
			row = fmt.Sprintf("  POOL %-7s %-24s %-10s", item.Version, truncateTUI(item.Name, 24), valueOrNA(item.PM))
			if item.MaxChildren > 0 {
				row += fmt.Sprintf(" · max %d", item.MaxChildren)
			}
			if item.User != "" {
				row += " · " + item.User
			}
			if item.Listen != "" {
				row += " · " + item.Listen
			}
		}
		rows = append(rows, renderSelectableRow(truncateTUI(row, width-3), index == model.cursor, width-2))
	}
	if len(model.php.Items) == 0 {
		rows = append(rows, tuiDimStyle.Render("No supported PHP-FPM version or pool was reported by this server."))
	}
	if !model.snapshot.Selected.Local && !model.php.Actionable {
		rows = append(rows, lipgloss.NewStyle().Foreground(tuiAmber).Render("! Pool configuration remains readable, but php.action is unavailable."))
	}
	return tuiPanelStyle.Width(width - 2).Render(strings.Join(rows, "\n"))
}

func (model tuiModel) renderAudit(width, height int) string {
	rows := []string{tuiTitleStyle.Render("Audit history") + tuiMutedStyle.Render("  / filter loaded events · A jump · R reload")}
	if !model.auditLoaded {
		message := "Target-scoped audit history has not been loaded."
		if model.resourceLoading {
			message = "Loading recent target-scoped audit history…"
		}
		rows = append(rows, tuiDimStyle.Render(message))
		return tuiPanelStyle.Width(width - 2).Render(strings.Join(rows, "\n"))
	}
	entries := filteredTUIAuditEntries(model.audit.Entries, model.auditFilter)
	scope := model.snapshot.Selected.label()
	filter := "no local filter"
	if model.auditFilter != "" {
		filter = fmt.Sprintf("filter %q", truncateTUI(model.auditFilter, 36))
	}
	rows = append(rows, tuiDimStyle.Render(truncateTUI(fmt.Sprintf(
		"%d matching · %d loaded · %d total for %s · %s",
		len(entries), len(model.audit.Entries), model.audit.Total, scope, filter,
	), width-4)))
	visible := maxInt(3, height-5)
	start, end := visibleRange(model.cursor, len(entries), visible)
	for index := start; index < end; index++ {
		entry := entries[index]
		timestamp := "—"
		if !entry.CreatedAt.IsZero() {
			timestamp = entry.CreatedAt.UTC().Format("2006-01-02 15:04:05Z")
		}
		detail := sanitizeTUILogLine(entry.Details)
		if detail == "" {
			detail = "—"
		}
		row := ""
		if width < 104 {
			row = fmt.Sprintf("%-20s  %-20s  %-13s  %s",
				timestamp, truncateTUI(entry.Action, 20), truncateTUI(entry.Resource, 13), detail)
		} else {
			row = fmt.Sprintf("%-20s  %-16s  %-22s  %-13s  %s",
				timestamp, truncateTUI(entry.UserName, 16), truncateTUI(entry.Action, 22), truncateTUI(entry.Resource, 13), detail)
		}
		row = truncateTUI(row, width-3)
		if strings.Contains(strings.ToLower(entry.Action+" "+entry.Details), "fail") || strings.Contains(strings.ToLower(entry.Details), "error") {
			row = lipgloss.NewStyle().Foreground(tuiRed).Render(row)
		}
		rows = append(rows, renderSelectableRow(row, index == model.cursor, width-2))
	}
	if len(entries) == 0 {
		message := "No audit events were recorded for this target."
		if model.auditFilter != "" {
			message = "No loaded audit events match the current filter."
		}
		rows = append(rows, tuiDimStyle.Render(message))
	}
	if model.audit.Total > len(model.audit.Entries) {
		rows = append(rows, tuiDimStyle.Render(fmt.Sprintf("Showing the newest %d events; use scripted audit list pagination for older history.", len(model.audit.Entries))))
	}
	return tuiPanelStyle.Width(width - 2).Render(strings.Join(rows, "\n"))
}

func (model tuiModel) renderStatus(width int) string {
	parts := make([]string, 0, 2)
	if model.notice != "" {
		color := tuiGreen
		prefix := "✓ "
		if model.noticeError {
			color = tuiRed
			prefix = "! "
		} else if model.loading || model.operating || model.resourceLoading {
			color = tuiBlue
			prefix = "◌ "
		}
		parts = append(parts, lipgloss.NewStyle().Foreground(color).Render(renderTUIStatusNotice(model.notice, prefix, width-2)))
	}
	for _, warning := range model.snapshot.Warnings {
		parts = append(parts, lipgloss.NewStyle().Foreground(tuiAmber).Render("! "+truncateTUI(warning, width-2)))
		if len(parts) == 2 {
			break
		}
	}
	if len(parts) == 0 {
		age := time.Since(model.snapshot.FetchedAt)
		if model.snapshot.FetchedAt.IsZero() {
			parts = append(parts, tuiDimStyle.Render("Waiting for the first server snapshot…"))
		} else {
			parts = append(parts, tuiDimStyle.Render(fmt.Sprintf("Last refresh %s ago · %s", compactDuration(age), model.serverURL)))
		}
	}
	return strings.Join(parts, "\n")
}

// renderTUIStatusNotice keeps each actionable error field readable on its own
// line. A single-line truncation would hide state, recovery advice, or the
// selected server as soon as the notice exceeded the terminal width.
func renderTUIStatusNotice(notice, prefix string, width int) string {
	notice = strings.Join(strings.Fields(sanitizeTUIText(notice)), " ")
	if notice == "" {
		return prefix
	}
	width = maxInt(1, width)
	segments := strings.Split(notice, "; ")
	lines := make([]string, 0, len(segments))
	first := true
	for index, segment := range segments {
		segment = strings.TrimSpace(segment)
		if segment == "" {
			continue
		}
		if index < len(segments)-1 {
			segment += ";"
		}
		linePrefix := "  "
		if first {
			linePrefix = prefix
		}
		available := width - lipgloss.Width(linePrefix)
		if available < 1 {
			available = 1
		}
		for _, line := range wrapTUIStatusText(segment, available) {
			if first {
				lines = append(lines, prefix+line)
				first = false
			} else {
				lines = append(lines, "  "+line)
			}
		}
	}
	if len(lines) == 0 {
		return prefix
	}
	return strings.Join(lines, "\n")
}

func wrapTUIStatusText(value string, maximum int) []string {
	value = strings.Join(strings.Fields(value), " ")
	if value == "" {
		return nil
	}
	maximum = maxInt(1, maximum)
	words := strings.Fields(value)
	lines := make([]string, 0, len(words))
	current := ""
	flush := func() {
		if current != "" {
			lines = append(lines, current)
			current = ""
		}
	}
	for _, word := range words {
		if lipgloss.Width(word) > maximum {
			flush()
			runes := []rune(word)
			for len(runes) > maximum {
				lines = append(lines, string(runes[:maximum]))
				runes = runes[maximum:]
			}
			if len(runes) > 0 {
				current = string(runes)
			}
			continue
		}
		candidate := word
		if current != "" {
			candidate = current + " " + word
		}
		if lipgloss.Width(candidate) <= maximum {
			current = candidate
			continue
		}
		flush()
		current = word
	}
	flush()
	return lines
}

func (model tuiModel) renderFooter(width int) string {
	shortcuts := []string{
		tuiKeyStyle.Render("←/→") + tuiDimStyle.Render(" tabs"),
		tuiKeyStyle.Render("j/k") + tuiDimStyle.Render(" move"),
		tuiKeyStyle.Render("[ ]") + tuiDimStyle.Render(" server"),
		tuiKeyStyle.Render("r") + tuiDimStyle.Render(" refresh"),
		tuiKeyStyle.Render("ctrl+k") + tuiDimStyle.Render(" actions"),
		tuiKeyStyle.Render("?") + tuiDimStyle.Render(" help"),
		tuiKeyStyle.Render("q") + tuiDimStyle.Render(" quit"),
	}
	if model.tab == tuiTabPM2 {
		shortcuts = append(shortcuts[:3], append([]string{tuiKeyStyle.Render("v") + tuiDimStyle.Render(" logs")}, shortcuts[3:]...)...)
	}
	if model.tab == tuiTabDisk {
		diskKeys := tuiKeyStyle.Render("s") + tuiDimStyle.Render(" scan cleanup   ") +
			tuiKeyStyle.Render("m") + tuiDimStyle.Render(" mounts   ") +
			tuiKeyStyle.Render("u") + tuiDimStyle.Render(" usage   ") +
			tuiKeyStyle.Render("w") + tuiDimStyle.Render(" largest   ") +
			tuiKeyStyle.Render("i") + tuiDimStyle.Render(" I/O   ") +
			tuiKeyStyle.Render("p") + tuiDimStyle.Render(" SMART   ") +
			tuiKeyStyle.Render("a") + tuiDimStyle.Render(" status   ") +
			tuiKeyStyle.Render("d") + tuiDimStyle.Render(" start analysis")
		shortcuts = append(shortcuts[:3], append([]string{diskKeys}, shortcuts[3:]...)...)
	}
	if model.tab == tuiTabPHP {
		shortcuts = append(shortcuts[:3], append([]string{tuiKeyStyle.Render("Enter") + tuiDimStyle.Render(" version action / pool config")}, shortcuts[3:]...)...)
	}
	if model.tab == tuiTabDNS {
		dnsKeys := tuiKeyStyle.Render("a") + tuiDimStyle.Render(" create   ") + tuiKeyStyle.Render("c") + tuiDimStyle.Render(" check   ") + tuiKeyStyle.Render("t") + tuiDimStyle.Render(" reload   ") + tuiKeyStyle.Render("x") + tuiDimStyle.Render(" delete")
		if model.dns.Detail != nil {
			dnsKeys += tuiDimStyle.Render("   ") + tuiKeyStyle.Render("e") + tuiDimStyle.Render(" edit   ") + tuiKeyStyle.Render("Backspace") + tuiDimStyle.Render(" zones")
		}
		shortcuts = append(shortcuts[:3], append([]string{dnsKeys}, shortcuts[3:]...)...)
	}
	if model.tab == tuiTabBackups && model.snapshot.Selected.Local {
		shortcuts = append(shortcuts[:3], append([]string{tuiKeyStyle.Render("c") + tuiDimStyle.Render(" create")}, shortcuts[3:]...)...)
	}
	if model.tab == tuiTabSnapshots {
		shortcuts = append(shortcuts[:3], append([]string{tuiKeyStyle.Render("c") + tuiDimStyle.Render(" create   ") + tuiKeyStyle.Render("d") + tuiDimStyle.Render(" destination")}, shortcuts[3:]...)...)
	}
	if model.tab == tuiTabFirewall {
		firewallKeys := tuiKeyStyle.Render("a") + tuiDimStyle.Render(" add")
		if model.snapshot.Selected.Local {
			firewallKeys += tuiDimStyle.Render("   ") + tuiKeyStyle.Render("t") + tuiDimStyle.Render(" toggle")
		}
		shortcuts = append(shortcuts[:3], append([]string{firewallKeys}, shortcuts[3:]...)...)
	}
	if model.tab == tuiTabSecurity {
		shortcuts = append(shortcuts[:3], append([]string{tuiKeyStyle.Render("a") + tuiDimStyle.Render(" add blacklist/whitelist entry   ") + tuiKeyStyle.Render("Enter") + tuiDimStyle.Render(" unban/delete selected IP")}, shortcuts[3:]...)...)
	}
	if model.tab == tuiTabCron {
		shortcuts = append(shortcuts[:3], append([]string{tuiKeyStyle.Render("Enter") + tuiDimStyle.Render(" manage job")}, shortcuts[3:]...)...)
	}
	if model.tab == tuiTabDatabases {
		shortcuts = append(shortcuts[:3], append([]string{tuiKeyStyle.Render("Enter") + tuiDimStyle.Render(" database action")}, shortcuts[3:]...)...)
	}
	if model.tab == tuiTabFiles {
		shortcuts = append(shortcuts[:3], append([]string{tuiKeyStyle.Render("Enter") + tuiDimStyle.Render(" open   ") + tuiKeyStyle.Render("u") + tuiDimStyle.Render(" parent   ") + tuiKeyStyle.Render("x") + tuiDimStyle.Render(" delete")}, shortcuts[3:]...)...)
	}
	if model.tab == tuiTabAudit {
		shortcuts = append(shortcuts[:3], append([]string{tuiKeyStyle.Render("/") + tuiDimStyle.Render(" filter loaded history")}, shortcuts[3:]...)...)
	}
	if model.tab == tuiTabUpdates {
		updateKeys := tuiKeyStyle.Render("r") + tuiDimStyle.Render(" reload")
		if model.snapshot.Selected.Local {
			updateKeys += tuiDimStyle.Render("   ") + tuiKeyStyle.Render("s") + tuiDimStyle.Render(" stage   ") + tuiKeyStyle.Render("i") + tuiDimStyle.Render(" install")
		} else {
			updateKeys += tuiDimStyle.Render("   ") + tuiKeyStyle.Render("u") + tuiDimStyle.Render(" upgrade   ") + tuiKeyStyle.Render("o") + tuiDimStyle.Render(" rollback")
		}
		shortcuts = append(shortcuts[:3], append([]string{updateKeys}, shortcuts[3:]...)...)
	}
	if model.tab == tuiTabDeploy {
		shortcuts = append(shortcuts[:3], append([]string{tuiKeyStyle.Render("Enter") + tuiDimStyle.Render(" inspect/action/logs")}, shortcuts[3:]...)...)
	}
	if model.tab == tuiTabAlerts {
		shortcuts = append(shortcuts[:3], append([]string{tuiKeyStyle.Render("Enter") + tuiDimStyle.Render(" inspect/action")}, shortcuts[3:]...)...)
	}
	if model.tab == tuiTabCloudflare {
		shortcuts = append(shortcuts[:3], append([]string{tuiKeyStyle.Render("Enter") + tuiDimStyle.Render(" zone/record action   ") + tuiKeyStyle.Render("Backspace") + tuiDimStyle.Render(" zones")}, shortcuts[3:]...)...)
	}
	if model.tab == tuiTabUsers {
		shortcuts = append(shortcuts[:3], append([]string{tuiKeyStyle.Render("a") + tuiDimStyle.Render(" create   ") + tuiKeyStyle.Render("Enter") + tuiDimStyle.Render(" manage panel user")}, shortcuts[3:]...)...)
	}
	if model.tab == tuiTabMail {
		shortcuts = append(shortcuts[:3], append([]string{tuiKeyStyle.Render("Enter") + tuiDimStyle.Render(" queue/domain/account detail")}, shortcuts[3:]...)...)
	}
	return truncateANSI(strings.Join(shortcuts, tuiDimStyle.Render("   ")), width)
}

func (model tuiModel) renderDialog(width, height int) string {
	if model.dialog.Mode == tuiDialogLogs {
		return model.renderLogDialog(width, height)
	}
	if model.dialog.Mode == tuiDialogHelp {
		return model.renderHelpDialog(width, height)
	}
	if model.dialog.Mode == tuiDialogPalette {
		return model.renderPaletteDialog(width, height)
	}
	if model.dialog.Mode == tuiDialogBackupValidation {
		return model.renderBackupValidationDialog(width, height)
	}
	if model.dialog.Mode == tuiDialogBackupVhosts {
		return model.renderBackupVhostDialog(width, height)
	}
	if model.dialog.Mode == tuiDialogSnapshotSelectors {
		return model.renderSnapshotSelectorDialog(width, height)
	}
	if model.dialog.Mode == tuiDialogAuditFilter {
		return model.renderAuditFilterDialog(width, height)
	}
	if model.dialog.Mode == tuiDialogDNSForm {
		return model.renderDNSFormDialog(width, height)
	}
	if model.dialog.Mode == tuiDialogUserForm {
		return model.renderUserFormDialog(width, height)
	}
	if model.dialog.Mode == tuiDialogSecurityAccessForm {
		return model.renderSecurityAccessFormDialog(width, height)
	}
	dialogWidth := 70
	if width < 78 {
		dialogWidth = width - 8
	}
	if dialogWidth < 40 {
		dialogWidth = 40
	}
	lines := []string{lipgloss.NewStyle().Bold(true).Foreground(tuiText).Render(truncateTUI(model.dialog.Title, dialogWidth-6))}
	for _, body := range model.dialog.Body {
		lines = append(lines, tuiMutedStyle.Render(truncateTUI(body, dialogWidth-6)))
	}
	lines = append(lines, "")
	if model.dialog.Mode == tuiDialogChoices {
		for index, option := range model.dialog.Options {
			prefix := "  "
			if option.Dangerous {
				prefix = "! "
			}
			lines = append(lines, renderSelectableRow(prefix+option.Label, index == model.dialog.Cursor, dialogWidth-4))
		}
		lines = append(lines, "", tuiMutedStyle.Render("Enter choose · Esc cancel"))
	} else {
		operation := model.dialog.Operation
		tone := tuiAccentBright
		if operation.Dangerous {
			tone = tuiRed
		}
		operationLabel := operation.Label
		if model.dialog.SecurityAccessOperation != nil {
			operationLabel = model.dialog.SecurityAccessOperation.Label
			tone = tuiRed
		}
		lines = append(lines,
			lipgloss.NewStyle().Bold(true).Foreground(tone).Render(truncateTUI(operationLabel, dialogWidth-6)),
			"",
			lipgloss.NewStyle().Bold(true).Foreground(tuiGreen).Render("Y")+tuiMutedStyle.Render(" confirm   ")+
				lipgloss.NewStyle().Bold(true).Foreground(tuiRed).Render("N / Esc")+tuiMutedStyle.Render(" cancel"),
		)
	}
	box := lipgloss.NewStyle().Width(dialogWidth).Border(lipgloss.DoubleBorder()).BorderForeground(tuiAccent).Padding(1, 2).Render(strings.Join(lines, "\n"))
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}

type tuiHelpEntry struct {
	key     string
	meaning string
}

func tuiHelpEntries() []tuiHelpEntry {
	return []tuiHelpEntry{
		{},
		{key: "← / → · Tab", meaning: "switch sections"},
		{key: "j / k · ↑ / ↓", meaning: "move through rows"},
		{key: "[ / ]", meaning: "switch the active server from any section"},
		{key: "Enter", meaning: "select or open a bounded action"},
		{key: "Ctrl+K", meaning: "search sections, servers, and bounded actions"},
		{key: "r", meaning: "refresh the active server"},
		{key: "1 … 9 · 0 · P · Z · F · S · C · D · E · B · N · A · U · G · L · O · I · M", meaning: "jump directly; 0 Web Ops · P PHP · Z DNS · F Firewall · S Security · C Cron · D Databases · E Files · B Backups · N Snapshots · A Audit · U Updates · G Deploy · L Alerts · O Cloudflare · I Users · M Mail"},
		{key: "Space", meaning: "select a disk cleanup target"},
		{key: "x", meaning: "confirm selected cleanup, file, DNS zone, or DNS record deletion"},
		{key: "Disk", meaning: "m mounts · u usage · w largest entries · i I/O · p SMART · a analysis status · d confirmed deep analysis"},
		{key: "v", meaning: "open logs for the selected PM2 process"},
		{key: "c", meaning: "DNS configuration check or local backup/snapshot creation"},
		{key: "a / e / t", meaning: "DNS create/edit/reload or contextual firewall actions"},
		{key: "File browser", meaning: "Enter open · U parent · ,/. roots · X confirmed local delete"},
		{key: "PHP-FPM", meaning: "Enter version actions or inspect an observed pool configuration"},
		{key: "Security", meaning: "a add a persistent local blacklist/whitelist entry; Enter unbans or deletes the selected observed IP; y confirms"},
		{key: "Backup restore", meaning: "validate · R restore · Y confirm"},
		{key: "Snapshots", meaning: "C create · D destination · Enter full/manifest/vhost staging restore · Y confirm"},
		{key: "Local DNS", meaning: "A create zone/record · E edit record/SOA · C check · T reload · X delete · Backspace zones"},
		{key: "Audit history", meaning: "/ filter the newest loaded target events · Ctrl+U clear · R reload"},
		{key: "Updates", meaning: "local: s stage · i install; managed: u upgrade · o rollback; y confirms"},
		{key: "Deploy", meaning: "Enter inspects a target, opens recent job output, or selects an advertised action; y confirms"},
		{key: "Alerts", meaning: "Enter opens a channel/rule action or alert event; y confirms mutations"},
		{key: "Cloudflare", meaning: "Enter opens zone/record actions · Backspace zones · y confirms provider mutations"},
		{key: "Users", meaning: "central panel only · a create · Enter profile/password/role/delete · y confirms"},
		{key: "Mail", meaning: "panel host only · bounded logs/queue/domain/account inventory · Enter account delivery history · y confirms queue retry/delete"},
		{key: "Log viewer", meaning: "j/k scroll · PgUp/PgDn page · g/G edges · r reload"},
		{key: "q · Ctrl+C", meaning: "quit"},
		{},
		{meaning: "Mutations never run from a single list key: every action opens a confirmation."},
	}
}

func (model tuiModel) renderHelpDialog(width, height int) string {
	dialogWidth := 70
	if width < 78 {
		dialogWidth = width - 8
	}
	if dialogWidth < 40 {
		dialogWidth = 40
	}
	dialogHeight := tuiHelpDialogHeight(height)
	contentWidth := maxInt(1, dialogWidth-6)
	body := make([]string, 0, len(tuiHelpEntries()))
	for _, entry := range tuiHelpEntries() {
		if entry.key == "" && entry.meaning == "" {
			body = append(body, "")
			continue
		}
		if entry.key == "" {
			body = append(body, tuiDimStyle.Render(truncateTUI(entry.meaning, contentWidth)))
			continue
		}
		body = append(body, renderTUIHelpLine(entry.key, entry.meaning, contentWidth))
	}

	pageSize := tuiHelpPageSize(height)
	maxScroll := maxInt(0, len(body)-pageSize)
	scroll := minInt(maxInt(0, model.dialog.HelpScroll), maxScroll)
	end := minInt(len(body), scroll+pageSize)

	lines := []string{lipgloss.NewStyle().Bold(true).Foreground(tuiText).Render(truncateTUI(model.dialog.Title, contentWidth))}
	lines = append(lines, body[scroll:end]...)
	lines = append(lines, tuiHelpFooter(contentWidth))
	innerHeight := maxInt(1, dialogHeight-2)
	for len(lines) < innerHeight {
		lines = append(lines, "")
	}
	if len(lines) > innerHeight {
		lines = lines[:innerHeight]
	}

	box := lipgloss.NewStyle().Width(dialogWidth).Height(dialogHeight).Border(lipgloss.DoubleBorder()).BorderForeground(tuiAccent).Padding(0, 2).Render(strings.Join(lines, "\n"))
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}

func renderTUIHelpLine(key, meaning string, width int) string {
	if width <= 0 {
		return ""
	}
	keyWidth := minInt(18, maxInt(1, width-2))
	meaningWidth := maxInt(1, width-keyWidth-1)
	keyText := truncateTUI(key, keyWidth)
	keyText += strings.Repeat(" ", maxInt(0, keyWidth-lipgloss.Width(keyText)))
	return tuiKeyStyle.Render(keyText) + " " + tuiMutedStyle.Render(truncateTUI(meaning, meaningWidth))
}

func tuiHelpFooter(width int) string {
	candidates := []string{
		tuiKeyStyle.Render("j/k") + tuiDimStyle.Render(" scroll · ") + tuiKeyStyle.Render("PgUp/PgDn") + tuiDimStyle.Render(" page · ") + tuiKeyStyle.Render("g/G") + tuiDimStyle.Render(" edges · ") + tuiKeyStyle.Render("q/Esc") + tuiDimStyle.Render(" close"),
		tuiKeyStyle.Render("j/k") + tuiDimStyle.Render(" · ") + tuiKeyStyle.Render("PgUp/PgDn") + tuiDimStyle.Render(" · ") + tuiKeyStyle.Render("g/G") + tuiDimStyle.Render(" · ") + tuiKeyStyle.Render("q/Esc") + tuiDimStyle.Render(" close"),
		tuiKeyStyle.Render("j/k") + tuiDimStyle.Render(" · ") + tuiKeyStyle.Render("PgUp/PgDn") + tuiDimStyle.Render(" · ") + tuiKeyStyle.Render("g/G") + tuiDimStyle.Render(" · close"),
	}
	for _, candidate := range candidates {
		if lipgloss.Width(candidate) <= width {
			return candidate
		}
	}
	return tuiKeyStyle.Render("j/k") + tuiDimStyle.Render(" scroll")
}

func tuiHelpDialogHeight(height int) int {
	if height < 18 {
		height = 18
	}
	return minInt(28, maxInt(12, height-4))
}

func tuiHelpPageSize(height int) int {
	return maxInt(1, tuiHelpDialogHeight(height)-4)
}

func tuiHelpScrollLimit(height int) int {
	return maxInt(0, len(tuiHelpEntries())-tuiHelpPageSize(height))
}

func (model tuiModel) renderDNSFormDialog(width, height int) string {
	dialogWidth := minInt(88, width-4)
	if dialogWidth < 50 {
		dialogWidth = 50
	}
	form := model.dialog.DNSForm
	lines := []string{
		lipgloss.NewStyle().Bold(true).Foreground(tuiText).Render(truncateTUI(model.dialog.Title, dialogWidth-6)),
	}
	for _, body := range model.dialog.Body {
		lines = append(lines, tuiMutedStyle.Render(truncateTUI(body, dialogWidth-6)))
	}
	lines = append(lines, "")
	for index, field := range form.Fields {
		value := field.Value
		if value == "" {
			value = field.Placeholder
		}
		labelStyle := tuiMutedStyle
		valueStyle := lipgloss.NewStyle().Foreground(tuiText).Background(lipgloss.Color("#18181B"))
		marker := "  "
		if index == form.Cursor {
			labelStyle = lipgloss.NewStyle().Bold(true).Foreground(tuiAccentBright)
			valueStyle = valueStyle.Foreground(tuiAccentBright)
			marker = "› "
			value += "▏"
		} else if field.Value == "" {
			valueStyle = valueStyle.Foreground(tuiMuted)
		}
		label := marker + fmt.Sprintf("%-14s", truncateTUI(field.Label, 14))
		available := maxInt(16, dialogWidth-26)
		renderedValue := truncateTUIInput(value, available-2)
		if index == form.Cursor {
			renderedValue = truncateTUIInputTail(value, available-2)
		}
		lines = append(lines, labelStyle.Render(label)+" "+valueStyle.Width(available).Render(renderedValue))
	}
	if form.Error != "" {
		lines = append(lines, "", lipgloss.NewStyle().Foreground(tuiRed).Render("! "+truncateTUI(form.Error, dialogWidth-8)))
	}
	lines = append(lines, "",
		tuiKeyStyle.Render("Tab / ↑↓")+tuiDimStyle.Render(" field   ")+
			tuiKeyStyle.Render("Enter")+tuiDimStyle.Render(" validate and review   ")+
			tuiKeyStyle.Render("Ctrl+U")+tuiDimStyle.Render(" clear   ")+
			tuiKeyStyle.Render("Esc")+tuiDimStyle.Render(" cancel"),
		tuiMutedStyle.Render("No mutation runs from this form; a separate Y confirmation always follows."),
	)
	box := lipgloss.NewStyle().Width(dialogWidth).Border(lipgloss.DoubleBorder()).BorderForeground(tuiAccent).Padding(1, 2).Render(strings.Join(lines, "\n"))
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}

func (model tuiModel) renderAuditFilterDialog(width, height int) string {
	dialogWidth := minInt(84, width-4)
	if dialogWidth < 48 {
		dialogWidth = 48
	}
	query := model.dialog.AuditFilter
	placeholder := query
	if placeholder == "" {
		placeholder = "Type user, action, resource, detail, or IP terms…"
	}
	lines := []string{
		lipgloss.NewStyle().Bold(true).Foreground(tuiText).Render(truncateTUI(model.dialog.Title, dialogWidth-6)),
		"",
		lipgloss.NewStyle().Foreground(tuiAccentBright).Background(lipgloss.Color("#18181B")).Width(dialogWidth - 6).Render("› " + truncateTUI(placeholder, dialogWidth-10)),
		"",
		tuiMutedStyle.Render("Terms are matched case-insensitively across the newest loaded target events."),
		tuiDimStyle.Render("This local view does not issue a mutation or broaden the selected server scope."),
		"",
		tuiKeyStyle.Render("Enter") + tuiDimStyle.Render(" apply   ") +
			tuiKeyStyle.Render("Ctrl+U") + tuiDimStyle.Render(" clear   ") +
			tuiKeyStyle.Render("Esc") + tuiDimStyle.Render(" cancel"),
	}
	box := lipgloss.NewStyle().Width(dialogWidth).Border(lipgloss.DoubleBorder()).BorderForeground(tuiAccent).Padding(1, 2).Render(strings.Join(lines, "\n"))
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}

func (model tuiModel) renderBackupVhostDialog(width, height int) string {
	dialogWidth := minInt(84, width-4)
	if dialogWidth < 48 {
		dialogWidth = 48
	}
	dialogHeight := minInt(24, maxInt(12, height-4))
	visible := maxInt(4, dialogHeight-7)
	cursor := minInt(model.dialog.Cursor, maxInt(0, len(model.dialog.BackupVhosts)-1))
	start, end := visibleRange(cursor, len(model.dialog.BackupVhosts), visible)
	lines := []string{
		lipgloss.NewStyle().Bold(true).Foreground(tuiText).Render(truncateTUI(model.dialog.Title, dialogWidth-6)),
		tuiMutedStyle.Render(fmt.Sprintf("%d selected · maximum %d · only server-observed direct folders", len(model.dialog.BackupSelected), model.dialog.BackupVhostMax)),
		"",
	}
	for index := start; index < end; index++ {
		name := model.dialog.BackupVhosts[index]
		marker := "[ ] "
		if model.dialog.BackupSelected[name] {
			marker = "[x] "
		}
		lines = append(lines, renderSelectableRow(marker+truncateTUI(name, dialogWidth-12), index == cursor, dialogWidth-4))
	}
	for len(lines) < dialogHeight-2 {
		lines = append(lines, "")
	}
	footer := tuiKeyStyle.Render("Space") + tuiDimStyle.Render(" toggle   ") +
		tuiKeyStyle.Render("A") + tuiDimStyle.Render(" all   ") +
		tuiKeyStyle.Render("Enter") + tuiDimStyle.Render(" continue   ") +
		tuiKeyStyle.Render("Esc") + tuiDimStyle.Render(" cancel")
	lines = append(lines, truncateANSI(footer, dialogWidth-6))
	box := lipgloss.NewStyle().Width(dialogWidth).Height(dialogHeight).Border(lipgloss.DoubleBorder()).BorderForeground(tuiAccent).Padding(0, 2).Render(strings.Join(lines, "\n"))
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}

func (model tuiModel) renderSnapshotSelectorDialog(width, height int) string {
	dialogWidth := minInt(84, width-4)
	if dialogWidth < 48 {
		dialogWidth = 48
	}
	dialogHeight := minInt(24, maxInt(12, height-4))
	visible := maxInt(4, dialogHeight-8)
	cursor := minInt(model.dialog.Cursor, maxInt(0, len(model.dialog.SnapshotSelectors)-1))
	start, end := visibleRange(cursor, len(model.dialog.SnapshotSelectors), visible)
	lines := []string{
		lipgloss.NewStyle().Bold(true).Foreground(tuiText).Render(truncateTUI(model.dialog.Title, dialogWidth-6)),
		tuiMutedStyle.Render(fmt.Sprintf("%d selected · maximum %d · server-observed bounded identities", len(model.dialog.SnapshotSelected), model.dialog.SnapshotMax)),
		"",
	}
	for _, body := range model.dialog.Body {
		lines = append(lines, tuiDimStyle.Render(truncateTUI(body, dialogWidth-6)))
	}
	for index := start; index < end; index++ {
		selector := model.dialog.SnapshotSelectors[index]
		marker := "[ ] "
		if model.dialog.SnapshotSelected[selector.Action] {
			marker = "[x] "
		}
		lines = append(lines, renderSelectableRow(marker+truncateTUI(selector.Label, dialogWidth-12), index == cursor, dialogWidth-4))
	}
	for len(lines) < dialogHeight-2 {
		lines = append(lines, "")
	}
	footer := tuiKeyStyle.Render("Space") + tuiDimStyle.Render(" toggle   ") +
		tuiKeyStyle.Render("A") + tuiDimStyle.Render(" all   ") +
		tuiKeyStyle.Render("Enter") + tuiDimStyle.Render(" confirm scope   ") +
		tuiKeyStyle.Render("Esc") + tuiDimStyle.Render(" cancel")
	lines = append(lines, truncateANSI(footer, dialogWidth-6))
	box := lipgloss.NewStyle().Width(dialogWidth).Height(dialogHeight).Border(lipgloss.DoubleBorder()).BorderForeground(tuiAccent).Padding(0, 2).Render(strings.Join(lines, "\n"))
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}

func (model tuiModel) renderBackupValidationDialog(width, height int) string {
	dialogWidth := minInt(84, width-4)
	if dialogWidth < 48 {
		dialogWidth = 48
	}
	validation := model.dialog.BackupValidation
	lines := []string{
		lipgloss.NewStyle().Bold(true).Foreground(tuiText).Render(truncateTUI(model.dialog.Title, dialogWidth-6)),
		"",
		tuiMutedStyle.Render(backupValidationSummary(validation)),
		"",
		fmt.Sprintf("Includes database  %s", yesNo(validation.IncludesDatabase)),
		fmt.Sprintf("Database recovery %s", yesNo(validation.DatabaseRecovery)),
		fmt.Sprintf("Includes files     %s", yesNo(validation.IncludesFiles)),
		fmt.Sprintf("Files rollback     %s", yesNo(validation.FilesRollback)),
		"",
		lipgloss.NewStyle().Foreground(tuiGreen).Render("Artifact validation passed without mutation."),
		tuiMutedStyle.Render("Restore will recheck the artifact and create the declared recovery boundary."),
		"",
		tuiKeyStyle.Render("R") + tuiMutedStyle.Render(" open restore confirmation   ") + tuiKeyStyle.Render("Q / Esc") + tuiMutedStyle.Render(" close"),
	}
	box := lipgloss.NewStyle().Width(dialogWidth).Border(lipgloss.DoubleBorder()).BorderForeground(tuiAccent).Padding(1, 2).Render(strings.Join(lines, "\n"))
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}

func (model tuiModel) renderPaletteDialog(width, height int) string {
	dialogWidth := minInt(92, width-4)
	if dialogWidth < 48 {
		dialogWidth = 48
	}
	dialogHeight := minInt(22, maxInt(12, height-4))
	items := filteredPaletteItems(model.dialog.PaletteItems, model.dialog.PaletteQuery)
	visible := maxInt(4, dialogHeight-7)
	cursor := minInt(model.dialog.Cursor, maxInt(0, len(items)-1))
	start, end := visibleRange(cursor, len(items), visible)
	query := model.dialog.PaletteQuery
	if query == "" {
		query = "Type to filter actions…"
	}
	lines := []string{
		lipgloss.NewStyle().Bold(true).Foreground(tuiText).Render("Quick actions"),
		lipgloss.NewStyle().Foreground(tuiAccentBright).Background(lipgloss.Color("#18181B")).Width(dialogWidth - 6).Render("› " + truncateTUI(query, dialogWidth-10)),
		tuiMutedStyle.Render(fmt.Sprintf("%d result(s) · active server %s", len(items), truncateTUI(model.snapshot.Selected.label(), 36))),
		"",
	}
	if len(items) == 0 {
		lines = append(lines, tuiDimStyle.Render("No matching section, server, or bounded action."))
	} else {
		for index := start; index < end; index++ {
			item := items[index]
			row := fmt.Sprintf("%-34s  %s", truncateTUI(item.Label, 34), truncateTUI(item.Description, maxInt(10, dialogWidth-46)))
			lines = append(lines, renderSelectableRow(row, index == cursor, dialogWidth-4))
		}
	}
	for len(lines) < dialogHeight-2 {
		lines = append(lines, "")
	}
	footer := tuiKeyStyle.Render("type") + tuiDimStyle.Render(" filter   ") +
		tuiKeyStyle.Render("↑/↓") + tuiDimStyle.Render(" move   ") +
		tuiKeyStyle.Render("Enter") + tuiDimStyle.Render(" choose   ") +
		tuiKeyStyle.Render("Esc") + tuiDimStyle.Render(" close")
	lines = append(lines, truncateANSI(footer, dialogWidth-6))
	box := lipgloss.NewStyle().Width(dialogWidth).Height(dialogHeight).Border(lipgloss.DoubleBorder()).BorderForeground(tuiAccent).Padding(0, 2).Render(strings.Join(lines, "\n"))
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}

func (model tuiModel) renderLogDialog(width, height int) string {
	dialogWidth := minInt(120, width-4)
	if dialogWidth < 44 {
		dialogWidth = 44
	}
	dialogHeight := maxInt(12, height-4)
	lineWidth := maxInt(20, dialogWidth-12)
	visible := maxInt(4, dialogHeight-7)
	count := len(model.dialog.LogLines)
	anchor := minInt(maxInt(0, count-1), model.dialog.LogScroll)
	start := maxInt(0, anchor-visible+1)
	end := minInt(count, start+visible)
	if anchor == 0 {
		end = minInt(count, visible)
	}

	lines := []string{
		lipgloss.NewStyle().Bold(true).Foreground(tuiText).Render(truncateTUI(model.dialog.Title, dialogWidth-8)),
		tuiMutedStyle.Render(fmt.Sprintf("%d line(s) · showing %d–%d", count, visibleStart(start, count), end)),
		"",
	}
	if count == 0 {
		lines = append(lines, tuiDimStyle.Render("(no log output)"))
	} else {
		for index := start; index < end; index++ {
			line := sanitizeTUILogLine(model.dialog.LogLines[index])
			row := fmt.Sprintf("%5d  %s", index+1, truncateTUILogLine(line, lineWidth))
			style := tuiMutedStyle
			if index == anchor {
				style = lipgloss.NewStyle().Foreground(tuiText).Background(lipgloss.Color("#27272A"))
			}
			lines = append(lines, style.Width(dialogWidth-6).Render(row))
		}
	}
	for len(lines) < dialogHeight-2 {
		lines = append(lines, "")
	}
	footer := tuiKeyStyle.Render("j/k") + tuiDimStyle.Render(" scroll   ") +
		tuiKeyStyle.Render("PgUp/PgDn") + tuiDimStyle.Render(" page   ") +
		tuiKeyStyle.Render("g/G") + tuiDimStyle.Render(" edges   ")
	if model.dialog.LogReloadNotice == "" {
		footer += tuiKeyStyle.Render("r") + tuiDimStyle.Render(" reload   ")
	}
	footer += tuiKeyStyle.Render("q/Esc") + tuiDimStyle.Render(" close")
	lines = append(lines, truncateANSI(footer, dialogWidth-6))
	box := lipgloss.NewStyle().Width(dialogWidth).Height(dialogHeight).Border(lipgloss.DoubleBorder()).BorderForeground(tuiAccent).Padding(0, 2).Render(strings.Join(lines, "\n"))
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}

func visibleStart(start, count int) int {
	if count == 0 {
		return 0
	}
	return start + 1
}

func renderContainerState(state string) string {
	normalized := strings.ToLower(strings.TrimSpace(state))
	color := tuiMuted
	switch normalized {
	case "running", "up":
		color = tuiGreen
	case "restarting", "paused", "created":
		color = tuiAmber
	case "dead", "failed":
		color = tuiRed
	}
	return lipgloss.NewStyle().Bold(true).Foreground(color).Render(truncateTUI(strings.ToUpper(valueOrNA(normalized)), 12))
}

func renderPM2State(state string) string {
	normalized := strings.ToLower(strings.TrimSpace(state))
	color := tuiMuted
	switch normalized {
	case "online", "running":
		color = tuiGreen
	case "launching", "stopping", "waiting restart":
		color = tuiAmber
	case "errored", "failed":
		color = tuiRed
	}
	return lipgloss.NewStyle().Bold(true).Foreground(color).Render(truncateTUI(strings.ToUpper(valueOrNA(normalized)), 11))
}

func renderWebState(state string) string {
	normalized := strings.ToLower(strings.TrimSpace(state))
	color := tuiMuted
	switch normalized {
	case "enabled", "valid":
		color = tuiGreen
	case "expiring":
		color = tuiAmber
	case "expired":
		color = tuiRed
	}
	return lipgloss.NewStyle().Bold(true).Foreground(color).Render(truncateTUI(strings.ToUpper(valueOrNA(normalized)), 10))
}

func renderBackupState(state string, verified bool) string {
	normalized := strings.ToLower(strings.TrimSpace(state))
	if verified && (normalized == "success" || normalized == "completed") {
		normalized = "verified"
	}
	color := tuiMuted
	switch normalized {
	case "completed", "success", "verified":
		color = tuiGreen
	case "pending", "running", "active":
		color = tuiBlue
	case "invalid", "failed", "orphaned":
		color = tuiRed
	}
	return lipgloss.NewStyle().Bold(true).Foreground(color).Render(truncateTUI(strings.ToUpper(valueOrNA(normalized)), 11))
}

func sanitizeTUILogLine(value string) string {
	return strings.TrimSpace(strings.Map(func(character rune) rune {
		if character == '\t' {
			return ' '
		}
		if unicode.IsControl(character) {
			return -1
		}
		return character
	}, value))
}

func truncateTUILogLine(value string, maximum int) string {
	if maximum <= 0 {
		return ""
	}
	if utf8.RuneCountInString(value) <= maximum {
		return value
	}
	if maximum == 1 {
		return "…"
	}
	runes := []rune(value)
	return string(runes[:maximum-1]) + "…"
}

func helpLine(key, meaning string) string {
	return fmt.Sprintf("%-18s %s", tuiKeyStyle.Render(key), tuiMutedStyle.Render(meaning))
}

func renderSelectableRow(row string, selected bool, width int) string {
	if !selected {
		return "  " + row
	}
	return lipgloss.NewStyle().Width(maxInt(1, width-2)).Bold(true).Foreground(tuiText).Background(lipgloss.Color("#312E81")).Render("› " + row)
}

func renderState(state string) string {
	normalized := normalizedServiceState(state)
	color := tuiDim
	label := strings.ToUpper(valueOrNA(normalized))
	switch normalized {
	case "running":
		color = tuiGreen
	case "degraded", "starting", "stopping":
		color = tuiAmber
	case "failed":
		color = tuiRed
	case "stopped":
		color = tuiMuted
	}
	return lipgloss.NewStyle().Bold(true).Foreground(color).Render(truncateTUI(label, 12))
}

func normalizedServiceState(state string) string {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "active", "running":
		return "running"
	case "inactive", "dead", "stopped":
		return "stopped"
	case "failed":
		return "failed"
	case "activating", "starting":
		return "starting"
	case "deactivating", "stopping":
		return "stopping"
	case "degraded":
		return "degraded"
	default:
		return "unknown"
	}
}

func visibleRange(cursor, count, limit int) (int, int) {
	if count <= limit {
		return 0, count
	}
	start := cursor - limit/2
	if start < 0 {
		start = 0
	}
	if start+limit > count {
		start = count - limit
	}
	return start, start + limit
}

func percentage(used, total uint64) float64 {
	if total == 0 {
		return 0
	}
	return float64(used) / float64(total) * 100
}

func formatTUIBytes(value uint64) string {
	if value == 0 {
		return "0 B"
	}
	units := []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB"}
	amount := float64(value)
	unit := 0
	for amount >= 1024 && unit < len(units)-1 {
		amount /= 1024
		unit++
	}
	if amount >= 100 || unit == 0 {
		return fmt.Sprintf("%.0f %s", amount, units[unit])
	}
	return fmt.Sprintf("%.1f %s", amount, units[unit])
}

func formatTUIUptime(seconds int64) string {
	if seconds <= 0 {
		return "N/A"
	}
	days := seconds / 86400
	hours := (seconds % 86400) / 3600
	minutes := (seconds % 3600) / 60
	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hours, minutes)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	return fmt.Sprintf("%dm", minutes)
}

func compactDuration(duration time.Duration) string {
	if duration < time.Second {
		return "<1s"
	}
	if duration < time.Minute {
		return fmt.Sprintf("%ds", int(duration.Seconds()))
	}
	return fmt.Sprintf("%dm", int(duration.Minutes()))
}

func truncateTUI(value string, maximum int) string {
	value = strings.Join(strings.Fields(sanitizeTUIText(value)), " ")
	if maximum <= 0 {
		return ""
	}
	if utf8.RuneCountInString(value) <= maximum {
		return value
	}
	if maximum == 1 {
		return "…"
	}
	runes := []rune(value)
	return string(runes[:maximum-1]) + "…"
}

// truncateTUIWidth is the display-width counterpart to truncateTUI. Header
// identity values can contain operator-supplied Unicode, so rune count alone
// is not enough to keep a narrow terminal line bounded.
func truncateTUIWidth(value string, maximum int) string {
	value = strings.Join(strings.Fields(sanitizeTUIText(value)), " ")
	if maximum <= 0 {
		return ""
	}
	if lipgloss.Width(value) <= maximum {
		return value
	}
	if maximum == 1 {
		return "…"
	}
	runes := []rune(value)
	for len(runes) > 0 {
		candidate := string(runes) + "…"
		if lipgloss.Width(candidate) <= maximum {
			return candidate
		}
		runes = runes[:len(runes)-1]
	}
	return "…"
}

func truncateTUIInput(value string, maximum int) string {
	value = sanitizeTUIText(value)
	if maximum <= 0 {
		return ""
	}
	if utf8.RuneCountInString(value) <= maximum {
		return value
	}
	if maximum == 1 {
		return "…"
	}
	runes := []rune(value)
	return string(runes[:maximum-1]) + "…"
}

func truncateTUIInputTail(value string, maximum int) string {
	value = sanitizeTUIText(value)
	if maximum <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= maximum {
		return value
	}
	if maximum == 1 {
		return "…"
	}
	return "…" + string(runes[len(runes)-maximum+1:])
}

func truncateANSI(value string, maximum int) string {
	if lipgloss.Width(value) <= maximum {
		return value
	}
	// Footer content is controlled and short. A compact replacement avoids
	// slicing through ANSI sequences on narrow terminals.
	return tuiKeyStyle.Render("? ") + tuiDimStyle.Render("help   ") + tuiKeyStyle.Render("q ") + tuiDimStyle.Render("quit")
}

func valueOrNA(value string) string {
	value = strings.TrimSpace(sanitizeTUIText(value))
	if value == "" {
		return "N/A"
	}
	return value
}

func sanitizeTUIText(value string) string {
	return strings.Map(func(character rune) rune {
		if character == '\n' || character == '\r' || character == '\t' {
			return ' '
		}
		if unicode.IsControl(character) {
			return -1
		}
		return character
	}, value)
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func (model tuiModel) renderSecurityAccessFormDialog(width, height int) string {
	dialogWidth := minInt(92, width-4)
	if dialogWidth < 52 {
		dialogWidth = 52
	}
	form := model.dialog.SecurityAccessForm
	lines := []string{
		lipgloss.NewStyle().Bold(true).Foreground(tuiText).Render(truncateTUI(model.dialog.Title, dialogWidth-6)),
	}
	for _, body := range model.dialog.Body {
		lines = append(lines, tuiMutedStyle.Render(truncateTUI(body, dialogWidth-6)))
	}
	lines = append(lines, "")
	for index, field := range form.Fields {
		value := field.Value
		placeholder := false
		if value == "" {
			value = field.Placeholder
			placeholder = true
		}
		labelStyle := tuiMutedStyle
		valueStyle := lipgloss.NewStyle().Foreground(tuiText).Background(lipgloss.Color("#18181B"))
		marker := "  "
		if index == form.Cursor {
			labelStyle = lipgloss.NewStyle().Bold(true).Foreground(tuiAccentBright)
			valueStyle = valueStyle.Foreground(tuiAccentBright)
			marker = "› "
			value += "▏"
		} else if placeholder {
			valueStyle = valueStyle.Foreground(tuiMuted)
		}
		label := marker + fmt.Sprintf("%-14s", truncateTUI(field.Label, 14))
		available := maxInt(16, dialogWidth-26)
		renderedValue := truncateTUIInput(value, available-2)
		if index == form.Cursor {
			renderedValue = truncateTUIInputTail(value, available-2)
		}
		lines = append(lines, labelStyle.Render(label)+" "+valueStyle.Width(available).Render(renderedValue))
	}
	if form.Error != "" {
		lines = append(lines, "", lipgloss.NewStyle().Foreground(tuiRed).Render("! "+truncateTUI(form.Error, dialogWidth-8)))
	}
	lines = append(lines, "",
		tuiKeyStyle.Render("Tab / ↑↓")+tuiDimStyle.Render(" field   ")+
			tuiKeyStyle.Render("Enter")+tuiDimStyle.Render(" validate and review   ")+
			tuiKeyStyle.Render("Ctrl+U")+tuiDimStyle.Render(" clear   ")+
			tuiKeyStyle.Render("Esc")+tuiDimStyle.Render(" cancel"),
		tuiMutedStyle.Render("No mutation runs from this form; a separate Y confirmation always follows."),
	)
	box := lipgloss.NewStyle().Width(dialogWidth).Border(lipgloss.DoubleBorder()).BorderForeground(tuiAccent).Padding(1, 2).Render(strings.Join(lines, "\n"))
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}
