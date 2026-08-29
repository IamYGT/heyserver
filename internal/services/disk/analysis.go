package disk

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	diskAnalysisDir       = "/var/lib/hserver/disk-analysis"
	diskAnalysisStatePath = diskAnalysisDir + "/state.json"
	diskAnalysisWorker    = diskAnalysisDir + "/worker.py"
)

type AnalysisStatus struct {
	ID            string     `json:"id,omitempty"`
	Unit          string     `json:"unit,omitempty"`
	Status        string     `json:"status"`
	Message       string     `json:"message"`
	CreatedAt     string     `json:"created_at,omitempty"`
	StartedAt     string     `json:"started_at,omitempty"`
	FinishedAt    string     `json:"finished_at,omitempty"`
	RootSize      uint64     `json:"root_size,omitempty"`
	RootUsed      uint64     `json:"root_used,omitempty"`
	RootAvailable uint64     `json:"root_available,omitempty"`
	Entries       []DirUsage `json:"entries"`
	Errors        []string   `json:"errors,omitempty"`
}

var ErrAnalysisRunning = errors.New("disk analysis is already running")

func analysisID() (string, error) {
	buffer := make([]byte, 6)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return "disk-" + hex.EncodeToString(buffer), nil
}

func StartAnalysis(ctx context.Context) (AnalysisStatus, error) {
	current, _ := GetAnalysisStatus()
	if current.Status == "queued" || current.Status == "running" {
		check := exec.CommandContext(ctx, "/usr/bin/systemctl", "is-active", "--quiet", current.Unit)
		if check.Run() == nil {
			return current, ErrAnalysisRunning
		}
	}

	id, err := analysisID()
	if err != nil {
		return AnalysisStatus{}, fmt.Errorf("generate analysis id: %w", err)
	}
	unit := "hserver-disk-analysis-" + strings.TrimPrefix(id, "disk-") + ".service"
	createdAt := time.Now().UTC().Format(time.RFC3339)
	status := AnalysisStatus{
		ID: id, Unit: unit, Status: "queued", Message: "Deep disk analysis queued",
		CreatedAt: createdAt, Entries: []DirUsage{},
	}
	if err := os.MkdirAll(diskAnalysisDir, 0700); err != nil {
		return AnalysisStatus{}, fmt.Errorf("create disk analysis directory: %w", err)
	}
	if err := os.Chmod(diskAnalysisDir, 0700); err != nil {
		return AnalysisStatus{}, fmt.Errorf("secure disk analysis directory: %w", err)
	}
	if err := os.WriteFile(diskAnalysisWorker, []byte(diskAnalysisWorkerSource), 0700); err != nil {
		return AnalysisStatus{}, fmt.Errorf("write disk analysis worker: %w", err)
	}
	if err := writeAnalysisStatus(status); err != nil {
		return AnalysisStatus{}, err
	}

	startCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(startCtx, "/usr/bin/systemd-run",
		"--quiet", "--unit="+strings.TrimSuffix(unit, ".service"),
		"--property=RuntimeMaxSec=30min", "--property=Nice=10", "--property=IOSchedulingClass=idle",
		"/usr/bin/python3", diskAnalysisWorker, diskAnalysisStatePath, id, unit,
	)
	if output, runErr := cmd.CombinedOutput(); runErr != nil {
		status.Status = "failed"
		status.Message = "Could not start deep disk analysis"
		status.FinishedAt = time.Now().UTC().Format(time.RFC3339)
		_ = writeAnalysisStatus(status)
		return status, fmt.Errorf("start disk analysis: %w: %s", runErr, strings.TrimSpace(string(output)))
	}
	return status, nil
}

func GetAnalysisStatus() (AnalysisStatus, error) {
	data, err := os.ReadFile(diskAnalysisStatePath)
	if os.IsNotExist(err) {
		return AnalysisStatus{Status: "idle", Message: "No deep disk analysis has run yet", Entries: []DirUsage{}}, nil
	}
	if err != nil {
		return AnalysisStatus{}, fmt.Errorf("read disk analysis state: %w", err)
	}
	if len(data) > 2<<20 {
		return AnalysisStatus{}, errors.New("disk analysis state exceeds 2 MiB")
	}
	var status AnalysisStatus
	if err := json.Unmarshal(data, &status); err != nil {
		return AnalysisStatus{}, fmt.Errorf("parse disk analysis state: %w", err)
	}
	if status.Entries == nil {
		status.Entries = []DirUsage{}
	}
	return status, nil
}

func writeAnalysisStatus(status AnalysisStatus) error {
	data, err := json.Marshal(status)
	if err != nil {
		return fmt.Errorf("encode disk analysis state: %w", err)
	}
	tmp := diskAnalysisStatePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("write disk analysis state: %w", err)
	}
	if err := os.Chmod(tmp, 0600); err != nil {
		return fmt.Errorf("secure disk analysis state: %w", err)
	}
	if err := os.Rename(tmp, diskAnalysisStatePath); err != nil {
		return fmt.Errorf("publish disk analysis state: %w", err)
	}
	return nil
}

const diskAnalysisWorkerSource = `#!/usr/bin/python3
import json
import os
import subprocess
import sys
from datetime import datetime, timezone

state_path, job_id, unit = sys.argv[1:4]
roots = ["/var/lib", "/var/www", "/opt", "/root"]

def now():
    return datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")

def load_state():
    try:
        with open(state_path, encoding="utf-8") as handle:
            return json.load(handle)
    except (OSError, ValueError, json.JSONDecodeError):
        return {"id": job_id, "unit": unit, "created_at": now(), "entries": []}

def publish(payload):
    tmp = state_path + ".tmp"
    with open(tmp, "w", encoding="utf-8") as handle:
        json.dump(payload, handle, separators=(",", ":"))
    os.chmod(tmp, 0o600)
    os.replace(tmp, state_path)

state = load_state()
state.update({"status": "running", "message": "Scanning /var/lib, /var/www, /opt and /root", "started_at": now(), "entries": [], "errors": []})
publish(state)

entries = {}
errors = []
try:
    for root in roots:
        if not os.path.isdir(root):
            continue
        try:
            result = subprocess.run(
                ["/usr/bin/du", "-x", "-B1", "--max-depth=2", "--threshold=104857600", root],
                stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, timeout=900, check=False,
            )
            for line in result.stdout.splitlines():
                parts = line.split("\t", 1)
                if len(parts) != 2:
                    continue
                try:
                    size = int(parts[0])
                except ValueError:
                    continue
                path = parts[1]
                if path != root:
                    entries[path] = max(size, entries.get(path, 0))
            if result.returncode not in (0, 1):
                errors.append(f"{root}: du exited {result.returncode}")
        except subprocess.TimeoutExpired:
            errors.append(f"{root}: scan timed out after 15 minutes")

    stat = os.statvfs("/")
    root_size = stat.f_blocks * stat.f_frsize
    root_available = stat.f_bavail * stat.f_frsize
    rows = [{"path": path, "size": size} for path, size in entries.items()]
    rows.sort(key=lambda row: row["size"], reverse=True)
    state.update({
        "status": "completed", "message": f"Deep analysis completed with {len(rows[:100])} entries",
        "finished_at": now(), "root_size": root_size, "root_used": root_size - root_available,
        "root_available": root_available, "entries": rows[:100], "errors": errors,
    })
except Exception as exc:
    state.update({"status": "failed", "message": f"Deep analysis failed: {exc}", "finished_at": now(), "errors": errors + [str(exc)]})
publish(state)
`
