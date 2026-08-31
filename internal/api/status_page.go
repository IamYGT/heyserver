package api

import (
	"fmt"
	"html/template"
	"math"
	"net/http"
	"time"

	uptime "github.com/IamYGT/heyserver/internal/services/uptime"
	"github.com/IamYGT/heyserver/internal/store"
)

// ── Data Structures ───────────────────────────────────────────────────────────

type statusPageData struct {
	Page          *store.UptimeStatusPage
	OverallStatus string // "operational", "partial", "major"
	LastChecked   string // relative time string
	Monitors      []statusPageMonitor
	Incidents     []store.UptimeIncident
}

type statusPageMonitor struct {
	Name      string
	Status    int     // 0=down, 1=up, 2=pending
	UptimePct float64 // 90-day uptime percentage
	AvgPingMs float64 // 24h average ping
	DailyBars []int   // 90 entries: 1=up, 0=down, -1=no data
}

// ── Template ─────────────────────────────────────────────────────────────────

const statusPageHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>{{.Page.Title}} — Status</title>
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{background:#0a0a0a;color:#e4e4e7;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;min-height:100vh;padding:0 0 48px}
a{color:#a1a1aa;text-decoration:none}
.container{max-width:780px;margin:0 auto;padding:0 16px}

/* Header */
.header{padding:40px 0 32px;text-align:center}
.header img{max-height:48px;margin-bottom:16px;display:block;margin-left:auto;margin-right:auto}
.header h1{font-size:1.75rem;font-weight:700;color:#fafafa;letter-spacing:-0.02em}
.header p{margin-top:8px;color:#71717a;font-size:0.9rem}

/* Overall Banner */
.banner{border-radius:10px;padding:18px 24px;margin-bottom:28px;display:flex;align-items:center;gap:12px}
.banner.operational{background:#052e16;border:1px solid #166534}
.banner.partial{background:#1c1400;border:1px solid #854d0e}
.banner.major{background:#1a0000;border:1px solid #991b1b}
.banner-dot{width:12px;height:12px;border-radius:50%;flex-shrink:0}
.operational .banner-dot{background:#22c55e;box-shadow:0 0 8px #22c55e80}
.partial .banner-dot{background:#eab308;box-shadow:0 0 8px #eab30880}
.major .banner-dot{background:#ef4444;box-shadow:0 0 8px #ef444480}
.banner-text h2{font-size:1rem;font-weight:600;color:#fafafa}
.banner-text p{font-size:0.8rem;color:#71717a;margin-top:2px}

/* Monitor List */
.section-title{font-size:0.75rem;font-weight:600;color:#71717a;text-transform:uppercase;letter-spacing:0.06em;margin-bottom:12px}
.monitors{display:flex;flex-direction:column;gap:2px;margin-bottom:36px}
.monitor-row{background:#18181b;border:1px solid #27272a;border-radius:8px;padding:14px 16px}
.monitor-header{display:flex;align-items:center;justify-content:space-between;margin-bottom:10px}
.monitor-left{display:flex;align-items:center;gap:8px}
.status-dot{width:8px;height:8px;border-radius:50%;flex-shrink:0}
.dot-up{background:#22c55e}
.dot-down{background:#ef4444}
.dot-pending{background:#71717a}
.monitor-name{font-size:0.9rem;font-weight:500;color:#e4e4e7}
.monitor-right{display:flex;align-items:center;gap:12px}
.ping{font-size:0.75rem;color:#71717a}
.uptime-badge{font-size:0.75rem;font-weight:600;padding:2px 7px;border-radius:4px}
.badge-up{background:#052e16;color:#22c55e}
.badge-down{background:#1a0000;color:#ef4444}
.badge-pending{background:#1f1f23;color:#71717a}

/* Daily bars */
.bars{display:flex;gap:2px;align-items:flex-end;height:28px}
.bar{flex:1;border-radius:2px;min-width:2px}
.bar-up{background:#22c55e}
.bar-down{background:#ef4444}
.bar-nodata{background:#27272a}

/* Incidents */
.incidents{margin-bottom:36px}
.incident-item{background:#18181b;border:1px solid #27272a;border-radius:8px;padding:14px 16px;margin-bottom:6px}
.incident-header{display:flex;align-items:center;justify-content:space-between;margin-bottom:4px}
.incident-monitor{font-size:0.85rem;font-weight:600;color:#fafafa}
.incident-type{font-size:0.7rem;font-weight:600;text-transform:uppercase;letter-spacing:0.05em;padding:2px 6px;border-radius:4px;background:#1a0000;color:#f87171}
.incident-cause{font-size:0.8rem;color:#a1a1aa;margin-bottom:6px}
.incident-meta{display:flex;gap:16px;font-size:0.75rem;color:#52525b}
.resolved-badge{font-size:0.7rem;font-weight:600;padding:2px 6px;border-radius:4px;background:#052e16;color:#22c55e}
.unresolved-badge{font-size:0.7rem;font-weight:600;padding:2px 6px;border-radius:4px;background:#1a0000;color:#f87171}

/* Footer */
footer{text-align:center;color:#3f3f46;font-size:0.75rem;padding-top:32px;border-top:1px solid #18181b;margin-top:8px}
footer span{color:#52525b}

@media(max-width:600px){
  .monitor-right{gap:8px}
  .bars{gap:1px}
}
</style>
</head>
<body>
<div class="container">

  <!-- Header -->
  <header class="header">
    {{if .Page.LogoURL}}<img src="{{.Page.LogoURL}}" alt="logo">{{end}}
    <h1>{{.Page.Title}}</h1>
    {{if .Page.Description}}<p>{{.Page.Description}}</p>{{end}}
  </header>

  <!-- Overall Status Banner -->
  <div class="banner {{.OverallStatus}}">
    <div class="banner-dot"></div>
    <div class="banner-text">
      {{if eq .OverallStatus "operational"}}<h2>All Systems Operational</h2>{{end}}
      {{if eq .OverallStatus "partial"}}<h2>Partial Outage</h2>{{end}}
      {{if eq .OverallStatus "major"}}<h2>Major Outage</h2>{{end}}
      <p>{{.LastChecked}}</p>
    </div>
  </div>

  <!-- Monitor List -->
  {{if .Monitors}}
  <p class="section-title">Services</p>
  <div class="monitors">
    {{range .Monitors}}
    <div class="monitor-row">
      <div class="monitor-header">
        <div class="monitor-left">
          <div class="status-dot {{if eq .Status 1}}dot-up{{else if eq .Status 0}}dot-down{{else}}dot-pending{{end}}"></div>
          <span class="monitor-name">{{.Name}}</span>
        </div>
        <div class="monitor-right">
          {{if gt .AvgPingMs 0.0}}<span class="ping">{{printf "%.0f" .AvgPingMs}}ms</span>{{end}}
          <span class="uptime-badge {{if eq .Status 1}}badge-up{{else if eq .Status 0}}badge-down{{else}}badge-pending{{end}}">
            {{printf "%.2f" .UptimePct}}%
          </span>
        </div>
      </div>
      <div class="bars">
        {{range .DailyBars}}<div class="bar {{if eq . 1}}bar-up{{else if eq . 0}}bar-down{{else}}bar-nodata{{end}}" style="height:{{barHeight .}}px" title="{{barTitle .}}"></div>{{end}}
      </div>
    </div>
    {{end}}
  </div>
  {{end}}

  <!-- Incident History -->
  {{if .Incidents}}
  <p class="section-title">Recent Incidents</p>
  <div class="incidents">
    {{range .Incidents}}
    <div class="incident-item">
      <div class="incident-header">
        <span class="incident-monitor">{{.MonitorName}}</span>
        <div style="display:flex;gap:8px;align-items:center">
          {{if .ResolvedAt}}
          <span class="resolved-badge">Resolved</span>
          {{else}}
          <span class="unresolved-badge">Ongoing</span>
          {{end}}
          <span class="incident-type">{{.Type}}</span>
        </div>
      </div>
      {{if .Cause}}<p class="incident-cause">{{.Cause}}</p>{{end}}
      <div class="incident-meta">
        <span>Started: {{formatTime .StartedAt}}</span>
        {{if .DurationSecs}}<span>Duration: {{formatDuration .DurationSecs}}</span>{{end}}
      </div>
    </div>
    {{end}}
  </div>
  {{end}}

  <!-- Footer -->
  <footer>
    <p>90-day history &middot; Powered by <span>Heyserver Panel</span></p>
  </footer>

</div>
</body>
</html>`

// statusPageTmpl is the compiled template, initialized at startup.
var statusPageTmpl = template.Must(
	template.New("status").Funcs(template.FuncMap{
		"barHeight": func(v int) int {
			switch v {
			case 1:
				return 24
			case 0:
				return 16
			default:
				return 8
			}
		},
		"barTitle": func(v int) string {
			switch v {
			case 1:
				return "Up"
			case 0:
				return "Down"
			default:
				return "No data"
			}
		},
		"formatTime": func(s string) string {
			t, err := time.Parse(time.RFC3339, s)
			if err != nil {
				return s
			}
			return t.UTC().Format("2006-01-02 15:04 UTC")
		},
		"formatDuration": func(secs *int) string {
			if secs == nil {
				return ""
			}
			d := *secs
			if d < 60 {
				return fmt.Sprintf("%ds", d)
			}
			if d < 3600 {
				return fmt.Sprintf("%dm %ds", d/60, d%60)
			}
			h := d / 3600
			m := (d % 3600) / 60
			return fmt.Sprintf("%dh %dm", h, m)
		},
	}).Parse(statusPageHTML),
)

// ── Render Helper ─────────────────────────────────────────────────────────────

// buildStatusPageData assembles template data from the uptime engine for a given status page.
func buildStatusPageData(engine *uptime.Engine, sp *store.UptimeStatusPage) (*statusPageData, error) {
	data := &statusPageData{
		Page: sp,
	}

	// Build monitor list
	var downCount, upCount int
	for _, entry := range sp.Monitors {
		m, err := engine.Repo().GetMonitor(entry.MonitorID)
		if err != nil || m == nil {
			continue
		}

		spm := statusPageMonitor{
			Name:      m.Name,
			Status:    2, // pending by default
			DailyBars: buildDailyBars(engine.Repo(), entry.MonitorID),
		}
		if entry.DisplayName != "" {
			spm.Name = entry.DisplayName
		}

		// Current status from monitor state
		if m.CurrentStatus != nil {
			spm.Status = *m.CurrentStatus
		}

		// Stats
		stats, err := uptime.ComputeStats(engine.Repo(), entry.MonitorID)
		if err == nil && stats != nil {
			spm.UptimePct = roundFloat(stats.Uptime90d, 2)
			spm.AvgPingMs = roundFloat(stats.AvgPingMs, 1)
		}

		switch spm.Status {
		case 0:
			downCount++
		case 1:
			upCount++
		}

		data.Monitors = append(data.Monitors, spm)
	}

	// Overall status
	total := len(data.Monitors)
	switch {
	case total == 0 || downCount == 0:
		data.OverallStatus = "operational"
	case downCount == total:
		data.OverallStatus = "major"
	default:
		data.OverallStatus = "partial"
	}

	// Last checked — find the most recent last_check_at across all monitors
	data.LastChecked = computeLastChecked(engine.Repo(), sp)

	// Recent incidents (last 10)
	incidents, err := engine.Repo().ListIncidents(0, 10)
	if err == nil {
		// Filter to only monitors on this page
		pageMonitorIDs := make(map[int64]bool, len(sp.Monitors))
		for _, e := range sp.Monitors {
			pageMonitorIDs[e.MonitorID] = true
		}
		for _, inc := range incidents {
			if pageMonitorIDs[inc.MonitorID] {
				data.Incidents = append(data.Incidents, inc)
			}
		}
	}

	return data, nil
}

// buildDailyBars returns 90 int values (1=up, 0=down, -1=no data) for the last 90 days.
func buildDailyBars(repo *store.UptimeRepository, monitorID int64) []int {
	bars := make([]int, 90)
	for i := range bars {
		bars[i] = -1 // default: no data
	}

	// Query daily uptime from raw heartbeats for last 90 days
	dailyRows, err := repo.DailyHeartbeatStats(monitorID, 90)
	if err != nil || len(dailyRows) == 0 {
		return bars
	}

	// Build a map of date → bar value
	dayMap := make(map[string]int, len(dailyRows))
	for _, r := range dailyRows {
		if r.Total == 0 {
			dayMap[r.Day] = -1
		} else if float64(r.Up)/float64(r.Total) > 0.5 {
			dayMap[r.Day] = 1
		} else {
			dayMap[r.Day] = 0
		}
	}

	// Fill bars from oldest (index 0) to most recent (index 89)
	now := time.Now().UTC()
	for i := 0; i < 90; i++ {
		day := now.AddDate(0, 0, -(89 - i)).Format("2006-01-02")
		if v, ok := dayMap[day]; ok {
			bars[i] = v
		}
	}

	return bars
}

// computeLastChecked returns a human-readable "last checked X ago" string.
func computeLastChecked(repo *store.UptimeRepository, sp *store.UptimeStatusPage) string {
	var latest time.Time
	for _, entry := range sp.Monitors {
		m, err := repo.GetMonitor(entry.MonitorID)
		if err != nil || m == nil || m.LastCheckAt == nil {
			continue
		}
		t, err := time.Parse(time.RFC3339, *m.LastCheckAt)
		if err != nil {
			continue
		}
		if t.After(latest) {
			latest = t
		}
	}
	if latest.IsZero() {
		return "Not yet checked"
	}
	diff := time.Since(latest)
	switch {
	case diff < time.Minute:
		return fmt.Sprintf("Last checked %ds ago", int(diff.Seconds()))
	case diff < time.Hour:
		return fmt.Sprintf("Last checked %dm ago", int(diff.Minutes()))
	default:
		return fmt.Sprintf("Last checked %dh ago", int(diff.Hours()))
	}
}

func roundFloat(v float64, decimals int) float64 {
	p := math.Pow(10, float64(decimals))
	return math.Round(v*p) / p
}

// renderStatusPage writes the HTML template to the response writer.
func renderStatusPage(w http.ResponseWriter, data *statusPageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=30")
	if err := statusPageTmpl.Execute(w, data); err != nil {
		http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
	}
}
