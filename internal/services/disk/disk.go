package disk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/IamYGT/heyserver/internal/services/shell"
)

// ── Data Types ───────────────────────────────────────────────────────────────

// Partition represents a disk partition from lsblk + df.
type Partition struct {
	Name       string  `json:"name"`
	Device     string  `json:"device"`
	MountPoint string  `json:"mountPoint"`
	FSType     string  `json:"fsType"`
	Size       uint64  `json:"size"`
	Used       uint64  `json:"used"`
	Available  uint64  `json:"available"`
	UsePct     float64 `json:"usePercent"`
	Label      string  `json:"label,omitempty"`
	UUID       string  `json:"uuid,omitempty"`
}

// IOStats represents disk I/O statistics from /proc/diskstats.
type IOStats struct {
	Device          string `json:"device"`
	ReadsCompleted  uint64 `json:"readsCompleted"`
	WritesCompleted uint64 `json:"writesCompleted"`
	SectorsRead     uint64 `json:"sectorsRead"`
	SectorsWritten  uint64 `json:"sectorsWritten"`
	ReadBytes       uint64 `json:"readBytes"`
	WriteBytes      uint64 `json:"writeBytes"`
	IOInProgress    uint64 `json:"ioInProgress"`
	IOTime          uint64 `json:"ioTimeMs"`
}

// SmartInfo represents SMART health data.
type SmartInfo struct {
	Available bool        `json:"available"`
	Healthy   bool        `json:"healthy"`
	Device    string      `json:"device"`
	Model     string      `json:"model,omitempty"`
	Serial    string      `json:"serial,omitempty"`
	Firmware  string      `json:"firmware,omitempty"`
	Status    string      `json:"status"` // PASSED, FAILED, UNKNOWN
	Message   string      `json:"message,omitempty"`
	Attrs     []SmartAttr `json:"attrs,omitempty"`
	RawOutput string      `json:"rawOutput,omitempty"`
}

// SmartAttr represents a single SMART attribute.
type SmartAttr struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Value int    `json:"value"`
	Worst int    `json:"worst"`
	Raw   string `json:"raw"`
}

// DirUsage represents disk usage of a directory.
type DirUsage struct {
	Path  string `json:"path"`
	Size  uint64 `json:"size"`
	Items int    `json:"items,omitempty"`
}

// LargestFile represents a large file found on disk.
type LargestFile struct {
	Path     string `json:"path"`
	Size     uint64 `json:"size"`
	Modified string `json:"modified,omitempty"`
}

// CleanupTarget represents a cleanable item.
type CleanupTarget struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Size        uint64 `json:"size"`
	Scope       string `json:"scope"`
	Risk        string `json:"risk"`
	Command     string `json:"-"` // not exposed to API
}

// MountEntry represents a mount point from fstab or active mounts.
type MountEntry struct {
	Device     string `json:"device"`
	MountPoint string `json:"mountPoint"`
	FSType     string `json:"fsType"`
	Options    string `json:"options"`
	Dump       int    `json:"dump,omitempty"`
	Pass       int    `json:"pass,omitempty"`
	Source     string `json:"source"` // "fstab" or "active"
}

// Overview is the combined disk overview response.
type Overview struct {
	Partitions []Partition `json:"partitions"`
	IOStats    []IOStats   `json:"ioStats"`
	TotalSize  uint64      `json:"totalSize"`
	TotalUsed  uint64      `json:"totalUsed"`
	TotalFree  uint64      `json:"totalFree"`
}

type lsblkDevice struct {
	Name       string        `json:"name"`
	Path       string        `json:"path"`
	Size       json.Number   `json:"size"`
	FSType     string        `json:"fstype"`
	MountPoint string        `json:"mountpoint"`
	Label      string        `json:"label"`
	UUID       string        `json:"uuid"`
	Type       string        `json:"type"`
	Children   []lsblkDevice `json:"children,omitempty"`
}

type lsblkInventory struct {
	BlockDevices []lsblkDevice `json:"blockdevices"`
}

// ── Partitions ───────────────────────────────────────────────────────────────

// GetPartitions returns all disk partitions with usage info.
func GetPartitions() ([]Partition, error) {
	// Use lsblk for device info
	lsblkOut, err := shell.ExecuteRaw("lsblk", "-Jbno", "NAME,PATH,SIZE,FSTYPE,MOUNTPOINT,LABEL,UUID,TYPE")
	if err != nil {
		return nil, fmt.Errorf("lsblk: %w", err)
	}

	// Get usage from df
	dfOut, err := shell.ExecuteRaw("df", "-B1", "--output=target,size,used,avail,pcent", "-x", "tmpfs", "-x", "devtmpfs", "-x", "squashfs", "-x", "overlay")
	if err != nil {
		// df might fail for some fs, try without exclusions
		dfOut, err = shell.ExecuteRaw("df", "-B1", "--output=target,size,used,avail,pcent")
		if err != nil {
			return nil, fmt.Errorf("df: %w", err)
		}
	}

	dfMap := parseDFOutput(dfOut)

	partitions, err := parseLSBLKPartitions(lsblkOut, dfMap)
	if err != nil {
		return nil, fmt.Errorf("parse lsblk: %w", err)
	}

	// Also add tmpfs mounts (useful for /tmp monitoring)
	dfAllOut, _ := shell.ExecuteRaw("df", "-B1", "--output=target,size,used,avail,pcent,fstype")
	if dfAllOut != "" {
		for _, line := range strings.Split(dfAllOut, "\n")[1:] {
			fields := strings.Fields(line)
			if len(fields) < 6 || fields[5] != "tmpfs" {
				continue
			}
			mount := fields[0]
			if mount == "/dev/shm" || mount == "/run" || strings.HasPrefix(mount, "/sys") || strings.HasPrefix(mount, "/snap") {
				continue
			}
			size, _ := strconv.ParseUint(fields[1], 10, 64)
			used, _ := strconv.ParseUint(fields[2], 10, 64)
			avail, _ := strconv.ParseUint(fields[3], 10, 64)
			pctStr := strings.TrimSuffix(fields[4], "%")
			pct, _ := strconv.ParseFloat(pctStr, 64)
			partitions = append(partitions, Partition{
				Name: "tmpfs", Device: "tmpfs", MountPoint: mount,
				FSType: "tmpfs", Size: size, Used: used, Available: avail, UsePct: pct,
			})
		}
	}

	return partitions, nil
}

func parseLSBLKPartitions(output string, dfMap map[string]dfEntry) ([]Partition, error) {
	var inventory lsblkInventory
	if err := json.Unmarshal([]byte(output), &inventory); err != nil {
		return nil, err
	}

	partitions := make([]Partition, 0)
	seen := make(map[string]struct{})
	var walk func([]lsblkDevice)
	walk = func(devices []lsblkDevice) {
		for _, device := range devices {
			if mountedBlockDevice(device) {
				key := device.Name + "\x00" + device.MountPoint
				if _, exists := seen[key]; !exists {
					seen[key] = struct{}{}
					partitions = append(partitions, makePartition(
						device.Name,
						device.Path,
						device.MountPoint,
						device.FSType,
						device.Label,
						device.UUID,
						device.Size,
						dfMap,
					))
				}
			}
			walk(device.Children)
		}
	}
	walk(inventory.BlockDevices)
	sort.Slice(partitions, func(i, j int) bool {
		if partitions[i].MountPoint == partitions[j].MountPoint {
			return partitions[i].Device < partitions[j].Device
		}
		return partitions[i].MountPoint < partitions[j].MountPoint
	})
	return partitions, nil
}

func mountedBlockDevice(device lsblkDevice) bool {
	if device.MountPoint == "" || device.MountPoint == "[SWAP]" {
		return false
	}
	switch device.Type {
	case "loop", "rom", "zram":
		return false
	default:
		return true
	}
}

func makePartition(name, devicePath, mount, fstype, label, uuid string, sizeNum json.Number, dfMap map[string]dfEntry) Partition {
	size, err := sizeNum.Int64()
	if err != nil || size < 0 {
		size = 0
	}
	if devicePath == "" {
		devicePath = "/dev/" + name
	}
	p := Partition{
		Name: name, Device: devicePath, MountPoint: mount,
		FSType: fstype, Label: label, UUID: uuid, Size: uint64(size),
	}
	if df, ok := dfMap[mount]; ok {
		p.Size = df.size
		p.Used = df.used
		p.Available = df.avail
		p.UsePct = df.pct
	}
	return p
}

type dfEntry struct {
	size, used, avail uint64
	pct               float64
}

func parseDFOutput(out string) map[string]dfEntry {
	m := map[string]dfEntry{}
	lines := strings.Split(out, "\n")
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		mount := fields[0]
		size, _ := strconv.ParseUint(fields[1], 10, 64)
		used, _ := strconv.ParseUint(fields[2], 10, 64)
		avail, _ := strconv.ParseUint(fields[3], 10, 64)
		pctStr := strings.TrimSuffix(fields[4], "%")
		pct, _ := strconv.ParseFloat(pctStr, 64)
		m[mount] = dfEntry{size, used, avail, pct}
	}
	return m
}

// ── I/O Stats ────────────────────────────────────────────────────────────────

// GetIOStats reads /proc/diskstats for I/O counters.
func GetIOStats() ([]IOStats, error) {
	data, err := os.ReadFile("/proc/diskstats")
	if err != nil {
		return nil, err
	}

	var stats []IOStats
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 14 {
			continue
		}
		name := fields[2]
		// Skip loop, dm, ram devices
		if strings.HasPrefix(name, "loop") || strings.HasPrefix(name, "dm-") || strings.HasPrefix(name, "ram") {
			continue
		}

		readsCompleted, _ := strconv.ParseUint(fields[3], 10, 64)
		sectorsRead, _ := strconv.ParseUint(fields[5], 10, 64)
		writesCompleted, _ := strconv.ParseUint(fields[7], 10, 64)
		sectorsWritten, _ := strconv.ParseUint(fields[9], 10, 64)
		ioInProgress, _ := strconv.ParseUint(fields[11], 10, 64)
		ioTime, _ := strconv.ParseUint(fields[12], 10, 64)

		stats = append(stats, IOStats{
			Device:          name,
			ReadsCompleted:  readsCompleted,
			WritesCompleted: writesCompleted,
			SectorsRead:     sectorsRead,
			SectorsWritten:  sectorsWritten,
			ReadBytes:       sectorsRead * 512,
			WriteBytes:      sectorsWritten * 512,
			IOInProgress:    ioInProgress,
			IOTime:          ioTime,
		})
	}
	return stats, nil
}

// ── SMART ────────────────────────────────────────────────────────────────────

// GetRootSmartInfo resolves the physical block device behind the root
// filesystem instead of assuming a provider-specific device name such as sda.
func GetRootSmartInfo() (*SmartInfo, error) {
	dfOut, err := shell.ExecuteRaw("df", "--output=source", "/")
	if err != nil {
		return nil, fmt.Errorf("resolve root filesystem source: %w", err)
	}
	source, err := rootFilesystemSource(dfOut)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(source, "/dev/") {
		return &SmartInfo{
			Device:  source,
			Status:  "UNAVAILABLE",
			Message: fmt.Sprintf("Root filesystem source %s is not a physical block device.", source),
		}, nil
	}

	lsblkOut, err := shell.ExecuteRaw("lsblk", "-s", "-lnpo", "PATH,TYPE", source)
	if err != nil {
		return nil, fmt.Errorf("resolve root physical device: %w", err)
	}
	device, message := rootPhysicalDevice(lsblkOut)
	if device == "" {
		return &SmartInfo{Device: source, Status: "UNAVAILABLE", Message: message}, nil
	}
	return GetSmartInfo(device)
}

func rootFilesystemSource(output string) (string, error) {
	fields := strings.Fields(output)
	if len(fields) < 2 {
		return "", errors.New("root filesystem source was not reported by df")
	}
	return fields[len(fields)-1], nil
}

func rootPhysicalDevice(output string) (string, string) {
	devices := make(map[string]struct{})
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == "disk" && strings.HasPrefix(fields[0], "/dev/") {
			devices[fields[0]] = struct{}{}
		}
	}
	if len(devices) == 0 {
		return "", "No physical disk could be resolved behind the root filesystem."
	}
	if len(devices) > 1 {
		return "", "The root filesystem spans multiple physical disks, so HServer will not choose one arbitrarily."
	}
	for device := range devices {
		return device, ""
	}
	return "", "No physical disk could be resolved behind the root filesystem."
}

// GetSmartInfo returns SMART health info for a device.
func GetSmartInfo(device string) (*SmartInfo, error) {
	// Validate device path
	if !strings.HasPrefix(device, "/dev/") {
		device = "/dev/" + device
	}
	// Basic validation — only allow /dev/sdX, /dev/vdX, /dev/nvmeXnY
	base := filepath.Base(device)
	if !isValidDevice(base) {
		return nil, fmt.Errorf("invalid device: %s", device)
	}

	info := &SmartInfo{Device: device}

	result, err := shell.ExecuteWithTimeout(15*time.Second, "smartctl", "-H", "-i", "-A", device)
	if err != nil {
		info.Status = "UNAVAILABLE"
		info.Message = "smartctl could not read this device. Install smartmontools and verify that the storage exposes SMART data."
		info.RawOutput = fmt.Sprintf("smartctl error: %v", err)
		return info, nil
	}

	info.Available = true
	info.RawOutput = result.Stdout

	// Parse health
	if strings.Contains(result.Stdout, "PASSED") {
		info.Healthy = true
		info.Status = "PASSED"
	} else if strings.Contains(result.Stdout, "FAILED") {
		info.Status = "FAILED"
	} else {
		info.Status = "UNKNOWN"
		info.Message = "smartctl returned data but did not report a definite health result."
	}

	// Parse model, serial, firmware
	for _, line := range strings.Split(result.Stdout, "\n") {
		if strings.HasPrefix(line, "Model Family:") || strings.HasPrefix(line, "Device Model:") {
			info.Model = strings.TrimSpace(strings.SplitN(line, ":", 2)[1])
		}
		if strings.HasPrefix(line, "Serial Number:") {
			info.Serial = strings.TrimSpace(strings.SplitN(line, ":", 2)[1])
		}
		if strings.HasPrefix(line, "Firmware Version:") {
			info.Firmware = strings.TrimSpace(strings.SplitN(line, ":", 2)[1])
		}
	}

	return info, nil
}

func isValidDevice(name string) bool {
	// Allow: sda, sdb, vda, vdb, nvme0n1, etc.
	if len(name) < 3 {
		return false
	}
	for _, c := range name {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

// ── Directory Listing (instant) ──────────────────────────────────────────────

// FileEntry represents a single file or directory in a listing.
type FileEntry struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	IsDir    bool   `json:"isDir"`
	Size     int64  `json:"size"`
	Modified string `json:"modified"`
	Mode     string `json:"mode"`
	Children int    `json:"children,omitempty"` // number of direct children (dirs only)
}

// ListDir lists the contents of a directory instantly using os.ReadDir.
// This is O(n) where n = number of direct entries — no recursive scanning.
func ListDir(dirPath string) ([]FileEntry, error) {
	if !isAllowedPath(dirPath) {
		return nil, fmt.Errorf("path not allowed: %s", dirPath)
	}

	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return nil, fmt.Errorf("read dir: %w", err)
	}

	var result []FileEntry
	for _, entry := range entries {
		name := entry.Name()
		// Skip hidden system files at root level
		if dirPath == "/" && (name == "proc" || name == "sys" || name == "dev" || name == "run" || name == "snap") {
			continue
		}

		fullPath := filepath.Join(dirPath, name)
		info, err := entry.Info()
		if err != nil {
			continue // skip unreadable entries
		}

		fe := FileEntry{
			Name:     name,
			Path:     fullPath,
			IsDir:    entry.IsDir(),
			Size:     info.Size(),
			Modified: info.ModTime().UTC().Format(time.RFC3339),
			Mode:     info.Mode().String(),
		}

		// For directories, count direct children (fast — just ReadDir, no stat)
		if entry.IsDir() {
			if sub, err := os.ReadDir(fullPath); err == nil {
				fe.Children = len(sub)
			}
		}

		result = append(result, fe)
	}

	// Sort: directories first (by name), then files (by size desc)
	sort.Slice(result, func(i, j int) bool {
		if result[i].IsDir != result[j].IsDir {
			return result[i].IsDir // dirs first
		}
		if result[i].IsDir {
			return result[i].Name < result[j].Name // dirs alphabetically
		}
		return result[i].Size > result[j].Size // files by size desc
	})

	return result, nil
}

// GetDirSize returns the total size of a single directory (uses du -sb).
// This can be slow for large directories — call per-directory on demand.
func GetDirSize(dirPath string) (int64, error) {
	if !isAllowedPath(dirPath) {
		return 0, fmt.Errorf("path not allowed: %s", dirPath)
	}
	result, err := shell.ExecuteWithTimeout(15*time.Second, "du", "-sb", "--max-depth=0", dirPath)
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(result.Stdout)
	if len(fields) < 1 {
		return 0, nil
	}
	size, _ := strconv.ParseInt(fields[0], 10, 64)
	return size, nil
}

// ── Directory Usage (slow — kept for backward compat) ────────────────────────

// GetDirUsage returns disk usage for subdirectories of the given path.
func GetDirUsage(dirPath string, maxDepth int) ([]DirUsage, error) {
	// Security: restrict to safe paths
	if !isAllowedPath(dirPath) {
		return nil, fmt.Errorf("path not allowed: %s", dirPath)
	}

	depthArg := fmt.Sprintf("--max-depth=%d", maxDepth)
	// Stay on the selected filesystem. Without -x, a request for / also walked
	// mounted backup volumes and turned a local usage request into a host-wide
	// scan.
	result, err := shell.ExecuteWithTimeout(30*time.Second, "du", "-x", "-b", depthArg, dirPath)
	if err != nil {
		return nil, fmt.Errorf("du: %w", err)
	}

	var usages []DirUsage
	for _, line := range strings.Split(strings.TrimSpace(result.Stdout), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		size, _ := strconv.ParseUint(fields[0], 10, 64)
		path := strings.Join(fields[1:], " ")
		if path == dirPath {
			continue // skip the root dir itself
		}
		usages = append(usages, DirUsage{Path: path, Size: size})
	}

	// Sort by size descending
	sort.Slice(usages, func(i, j int) bool {
		return usages[i].Size > usages[j].Size
	})

	return usages, nil
}

// GetLargestFiles finds the largest files under a path.
// Uses exec.Command directly (like collectDisks in monitor.go) because
// find -printf contains \n which is blocked by the shell injection filter.
func GetLargestFiles(dirPath string, limit int) ([]LargestFile, error) {
	if !isAllowedPath(dirPath) {
		return nil, fmt.Errorf("path not allowed: %s", dirPath)
	}
	if limit <= 0 || limit > 50 {
		limit = 20
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// find -xdev -type f -printf '%s %T@ %p\n' — must use exec.Command directly
	cmd := exec.CommandContext(ctx, "find", dirPath,
		"-xdev", "-type", "f", "-printf", "%s %T@ %p\n")
	out, err := cmd.Output()
	if err != nil {
		// find may return exit 1 for permission errors but still output results
		if len(out) == 0 {
			return nil, fmt.Errorf("find: %w", err)
		}
	}

	type fileEntry struct {
		size    uint64
		modTime float64
		path    string
	}
	var files []fileEntry
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 3)
		if len(parts) < 3 {
			continue
		}
		size, _ := strconv.ParseUint(parts[0], 10, 64)
		modTime, _ := strconv.ParseFloat(parts[1], 64)
		files = append(files, fileEntry{size, modTime, parts[2]})
	}

	sort.Slice(files, func(i, j int) bool { return files[i].size > files[j].size })

	var results []LargestFile
	for i, f := range files {
		if i >= limit {
			break
		}
		mod := ""
		if f.modTime > 0 {
			mod = time.Unix(int64(f.modTime), 0).UTC().Format(time.RFC3339)
		}
		results = append(results, LargestFile{Path: f.path, Size: f.size, Modified: mod})
	}
	return results, nil
}

func isAllowedPath(p string) bool {
	allowed := []string{"/", "/var", "/tmp", "/home", "/opt", "/etc", "/usr", "/root", "/srv", "/boot"}
	clean := filepath.Clean(p)
	for _, a := range allowed {
		if clean == a || strings.HasPrefix(clean, a+"/") {
			return true
		}
	}
	return false
}

// ── Cleanup ──────────────────────────────────────────────────────────────────

// ScanCleanup identifies cleanable disk space.
func ScanCleanup() ([]CleanupTarget, error) {
	var targets []CleanupTarget

	// 1. APT cache
	aptSize := dirSize("/var/cache/apt/archives")
	if aptSize > 0 {
		targets = append(targets, CleanupTarget{
			ID: "apt-cache", Name: "APT Package Cache",
			Description: "Downloaded .deb packages no longer needed",
			Size:        aptSize, Scope: "root filesystem", Risk: "low",
		})
	}

	// 2. Journal logs
	journalOut, _ := shell.ExecuteRaw("journalctl", "--disk-usage")
	journalSize := parseJournalSize(journalOut)
	if journalSize > 0 {
		targets = append(targets, CleanupTarget{
			ID: "journal", Name: "Systemd Journal Logs",
			Description: "System logs older than 7 days will be removed",
			Size:        journalSize, Scope: "root filesystem", Risk: "medium",
		})
	}

	// 3. Temporary files. Keep /tmp and /var/tmp separate so the measured
	// filesystem scope and reclaimed space stay truthful on hosts where /tmp is
	// a dedicated mount.
	tmpSize := oldFileSize("/tmp", 7)
	if tmpSize > 1024*1024 { // > 1MB
		targets = append(targets, CleanupTarget{
			ID: "tmp", Name: "Temporary Files (/tmp)",
			Description: "Files in /tmp older than 7 days",
			Size:        tmpSize, Scope: filesystemScope("/tmp"), Risk: "low",
		})
	}
	varTmpSize := oldFileSize("/var/tmp", 7)
	if varTmpSize > 1024*1024 {
		targets = append(targets, CleanupTarget{
			ID: "var-tmp", Name: "Temporary Files (/var/tmp)",
			Description: "Files in /var/tmp older than 7 days",
			Size:        varTmpSize, Scope: filesystemScope("/var/tmp"), Risk: "low",
		})
	}

	// 4. Old nginx logs (rotated)
	nginxLogSize := globSize("/var/log/nginx/*.gz")
	if nginxLogSize > 0 {
		targets = append(targets, CleanupTarget{
			ID: "nginx-logs", Name: "Rotated Nginx Logs",
			Description: "Compressed .gz log files from nginx log rotation",
			Size:        nginxLogSize, Scope: "root filesystem", Risk: "medium",
		})
	}

	// 5. Old PHP-FPM logs
	phpLogSize := globSize("/var/log/php*.log.*")
	if phpLogSize > 0 {
		targets = append(targets, CleanupTarget{
			ID: "php-logs", Name: "Old PHP-FPM Logs",
			Description: "Rotated PHP-FPM log files",
			Size:        phpLogSize, Scope: "root filesystem", Risk: "medium",
		})
	}

	// 6. Thumbnails / old crash reports
	crashSize := dirSize("/var/crash")
	if crashSize > 0 {
		targets = append(targets, CleanupTarget{
			ID: "crash", Name: "Crash Reports",
			Description: "Old application crash reports",
			Size:        crashSize, Scope: "root filesystem", Risk: "medium",
		})
	}

	// 7. Re-downloadable developer caches. These do not contain application data.
	npmSize := cachePathsSize(npmCachePaths)
	if npmSize > 1024*1024 {
		targets = append(targets, CleanupTarget{
			ID: "npm-cache", Name: "NPM Download Cache",
			Description: "Re-downloadable package cache; installed applications are not changed",
			Size:        npmSize, Scope: "root filesystem", Risk: "low",
		})
	}
	puppeteerSize := dirSize("/root/.cache/puppeteer")
	if puppeteerSize > 1024*1024 {
		targets = append(targets, CleanupTarget{
			ID: "puppeteer-cache", Name: "Puppeteer Browser Cache",
			Description: "Downloaded browser binaries; Puppeteer can download them again when needed",
			Size:        puppeteerSize, Scope: "root filesystem", Risk: "low",
		})
	}
	goBuildSize := cachePathsSize(goBuildCachePaths)
	if goBuildSize > 1024*1024 {
		targets = append(targets, CleanupTarget{
			ID: "go-build-cache", Name: "Go Build Cache",
			Description: "Rebuildable Go compiler cache; source code and downloaded modules stay unchanged",
			Size:        goBuildSize, Scope: "root filesystem", Risk: "low",
		})
	}

	// 8. Keep the five newest HServer rollback binaries and offer older copies.
	_, oldBinarySize := oldHServerBinaries()
	if oldBinarySize > 0 {
		targets = append(targets, CleanupTarget{
			ID: "hserver-old-binaries", Name: "Old HServer Rollback Binaries",
			Description: "Keeps the five newest rollback binaries and removes only older copies",
			Size:        oldBinarySize, Scope: "root filesystem", Risk: "low",
		})
	}

	// 9. HServer builds intentionally use uniquely named /tmp workspaces and a
	// dedicated Go cache. They are never runtime data, but can accumulate quickly
	// during frequent panel releases. Keep this separate from the age-based /tmp
	// target so operators can reclaim it immediately and explicitly.
	_, hserverTempSize := hserverTemporaryArtifacts("/tmp")
	if hserverTempSize > 1024*1024 {
		targets = append(targets, CleanupTarget{
			ID: "hserver-build-artifacts", Name: "HServer Temporary Build Artifacts",
			Description: "Disposable HServer build workspaces and Go build caches under /tmp; live panel data is not included",
			Size:        hserverTempSize, Scope: filesystemScope("/tmp"), Risk: "low",
		})
	}

	return targets, nil
}

// ExecuteCleanup runs a specific cleanup action.
func ExecuteCleanup(id string) (string, error) {
	if path, ok := temporaryCleanupPaths[id]; ok {
		result, err := shell.ExecuteWithTimeout(30*time.Second, "find", path, "-xdev", "-type", "f", "-mtime", "+7", "-delete")
		if err != nil {
			return "", fmt.Errorf("temporary file cleanup %s: %w", path, err)
		}
		return fmt.Sprintf("Files in %s older than 7 days removed. %s", path, result.Stdout), nil
	}

	switch id {
	case "apt-cache":
		_, err := shell.ExecuteWithTimeout(60*time.Second, "apt", "clean")
		if err != nil {
			return "", fmt.Errorf("apt clean: %w", err)
		}
		return "APT cache cleaned", nil

	case "journal":
		out, err := shell.ExecuteRaw("journalctl", "--vacuum-time=7d")
		if err != nil {
			return "", fmt.Errorf("journal vacuum: %w", err)
		}
		return out, nil

	case "nginx-logs":
		result, err := shell.ExecuteWithTimeout(15*time.Second, "find", "/var/log/nginx", "-name", "*.gz", "-delete")
		if err != nil {
			return "", fmt.Errorf("nginx log cleanup: %w", err)
		}
		return "Rotated nginx logs removed. " + result.Stdout, nil

	case "php-logs":
		result, err := shell.ExecuteWithTimeout(15*time.Second, "find", "/var/log", "-name", "php*.log.*", "-delete")
		if err != nil {
			return "", fmt.Errorf("php log cleanup: %w", err)
		}
		return "Old PHP logs removed. " + result.Stdout, nil

	case "crash":
		result, err := shell.ExecuteWithTimeout(15*time.Second, "find", "/var/crash", "-type", "f", "-delete")
		if err != nil {
			return "", fmt.Errorf("crash cleanup: %w", err)
		}
		return "Crash reports removed. " + result.Stdout, nil

	case "npm-cache":
		if err := removeCachePaths(npmCachePaths); err != nil {
			return "", fmt.Errorf("npm cache cleanup: %w", err)
		}
		return "NPM download cache cleaned", nil

	case "puppeteer-cache":
		if err := os.RemoveAll("/root/.cache/puppeteer"); err != nil {
			return "", fmt.Errorf("puppeteer cache cleanup: %w", err)
		}
		return "Puppeteer browser cache removed", nil

	case "go-build-cache":
		if err := removeCachePaths(goBuildCachePaths); err != nil {
			return "", fmt.Errorf("Go build cache cleanup: %w", err)
		}
		return "Go build cache removed; future builds will recreate it", nil

	case "hserver-old-binaries":
		paths, _ := oldHServerBinaries()
		for _, path := range paths {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return "", fmt.Errorf("remove old HServer binary: %w", err)
			}
		}
		return fmt.Sprintf("%d old HServer rollback binaries removed; newest five retained", len(paths)), nil

	case "hserver-build-artifacts":
		count, err := removeHServerTemporaryArtifacts("/tmp")
		if err != nil {
			return "", fmt.Errorf("HServer temporary build cleanup: %w", err)
		}
		return fmt.Sprintf("%d HServer temporary build artifacts removed", count), nil

	default:
		return "", fmt.Errorf("unknown cleanup target: %s", id)
	}
}

func IsCleanupTarget(id string) bool {
	switch id {
	case "apt-cache", "journal", "tmp", "var-tmp", "nginx-logs", "php-logs", "crash", "npm-cache", "puppeteer-cache", "go-build-cache", "hserver-old-binaries", "hserver-build-artifacts":
		return true
	default:
		return false
	}
}

var temporaryCleanupPaths = map[string]string{
	"tmp":     "/tmp",
	"var-tmp": "/var/tmp",
}

var npmCachePaths = []string{
	"/root/.npm/_cacache",
	"/root/.npm/_logs",
	"/root/.npm/_npx",
	"/root/.npm/_update-notifier-last-checked",
}

var goBuildCachePaths = []string{
	"/root/.cache/go-build",
}

func cachePathsSize(paths []string) uint64 {
	var total uint64
	for _, path := range paths {
		total += dirSize(path)
	}
	return total
}

func removeCachePaths(paths []string) error {
	for _, path := range paths {
		clean := filepath.Clean(path)
		if clean == "." || clean == "/" {
			return fmt.Errorf("refusing unsafe cache path %q", path)
		}
		if err := os.RemoveAll(clean); err != nil {
			return err
		}
	}
	return nil
}

func oldFileSize(path string, days int) uint64 {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "find", path, "-xdev", "-type", "f", "-mtime", fmt.Sprintf("+%d", days), "-printf", "%s\n")
	output, err := cmd.Output()
	if err != nil && len(output) == 0 {
		return 0
	}
	var total uint64
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		size, _ := strconv.ParseUint(strings.TrimSpace(line), 10, 64)
		total += size
	}
	return total
}

func filesystemScope(path string) string {
	rootInfo, rootErr := os.Stat("/")
	pathInfo, pathErr := os.Stat(path)
	if rootErr != nil || pathErr != nil {
		return "filesystem"
	}
	rootStat, rootOK := rootInfo.Sys().(*syscall.Stat_t)
	pathStat, pathOK := pathInfo.Sys().(*syscall.Stat_t)
	if rootOK && pathOK && rootStat.Dev == pathStat.Dev {
		return "root filesystem"
	}
	return "separate filesystem"
}

func oldHServerBinaries() ([]string, uint64) {
	executable, err := os.Executable()
	if err != nil {
		return nil, 0
	}
	return oldHServerBinariesIn(filepath.Dir(executable), 5)
}

func oldHServerBinariesIn(directory string, retain int) ([]string, uint64) {
	if retain < 1 {
		return nil, 0
	}
	matches, _ := filepath.Glob(filepath.Join(directory, "hserver-panel.pre-*"))
	type candidate struct {
		path string
		mod  time.Time
		size uint64
	}
	items := make([]candidate, 0, len(matches))
	for _, path := range matches {
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		items = append(items, candidate{path: path, mod: info.ModTime(), size: uint64(info.Size())})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].mod.After(items[j].mod) })
	if len(items) <= retain {
		return nil, 0
	}
	paths := make([]string, 0, len(items)-retain)
	var total uint64
	for _, item := range items[retain:] {
		paths = append(paths, item.path)
		total += item.size
	}
	sort.Strings(paths)
	return paths, total
}

var hserverTemporaryArtifactPatterns = []string{
	"hserver-panel-*",
	"hserver-go-cache",
}

// hserverTemporaryArtifacts returns only direct children of root matching the
// fixed HServer build patterns. Symlinks are measured as links and never
// followed, preventing a crafted /tmp link from expanding the cleanup scope.
func hserverTemporaryArtifacts(root string) ([]string, uint64) {
	cleanRoot := filepath.Clean(root)
	if !filepath.IsAbs(cleanRoot) || cleanRoot == "/" {
		return nil, 0
	}

	seen := make(map[string]struct{})
	paths := make([]string, 0)
	var total uint64
	for _, pattern := range hserverTemporaryArtifactPatterns {
		matches, _ := filepath.Glob(filepath.Join(cleanRoot, pattern))
		for _, match := range matches {
			clean := filepath.Clean(match)
			if filepath.Dir(clean) != cleanRoot {
				continue
			}
			if _, ok := seen[clean]; ok {
				continue
			}
			info, err := os.Lstat(clean)
			if err != nil {
				continue
			}
			seen[clean] = struct{}{}
			paths = append(paths, clean)
			total += apparentPathSize(clean, info)
		}
	}
	sort.Strings(paths)
	return paths, total
}

func apparentPathSize(path string, rootInfo os.FileInfo) uint64 {
	if !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		if rootInfo.Size() > 0 {
			return uint64(rootInfo.Size())
		}
		return 0
	}

	var total uint64
	_ = filepath.WalkDir(path, func(current string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if entry != nil && entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		if info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			if info.Size() > 0 {
				total += uint64(info.Size())
			}
		}
		return nil
	})
	return total
}

func removeHServerTemporaryArtifacts(root string) (int, error) {
	paths, _ := hserverTemporaryArtifacts(root)
	cleanRoot := filepath.Clean(root)
	for _, path := range paths {
		// Re-check immediately before removal instead of trusting a stale scan.
		if filepath.Dir(filepath.Clean(path)) != cleanRoot {
			return 0, fmt.Errorf("refusing artifact outside %s: %s", cleanRoot, path)
		}
		if err := os.RemoveAll(path); err != nil {
			return 0, err
		}
	}
	return len(paths), nil
}

// ── Mounts ───────────────────────────────────────────────────────────────────

// GetMounts returns fstab entries and active mounts.
func GetMounts() ([]MountEntry, error) {
	var entries []MountEntry

	// Parse fstab
	fstabData, err := os.ReadFile("/etc/fstab")
	if err == nil {
		for _, line := range strings.Split(string(fstabData), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) < 4 {
				continue
			}
			dump, pass := 0, 0
			if len(fields) >= 5 {
				dump, _ = strconv.Atoi(fields[4])
			}
			if len(fields) >= 6 {
				pass, _ = strconv.Atoi(fields[5])
			}
			entries = append(entries, MountEntry{
				Device: fields[0], MountPoint: fields[1],
				FSType: fields[2], Options: fields[3],
				Dump: dump, Pass: pass, Source: "fstab",
			})
		}
	}

	return entries, nil
}

// ── Helpers ──────────────────────────────────────────────────────────────────

func dirSize(path string) uint64 {
	result, err := shell.ExecuteWithTimeout(10*time.Second, "du", "-sb", path)
	if err != nil {
		return 0
	}
	fields := strings.Fields(result.Stdout)
	if len(fields) < 1 {
		return 0
	}
	size, _ := strconv.ParseUint(fields[0], 10, 64)
	return size
}

func globSize(pattern string) uint64 {
	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) == 0 {
		return 0
	}
	var total uint64
	for _, m := range matches {
		info, err := os.Stat(m)
		if err == nil {
			total += uint64(info.Size())
		}
	}
	return total
}

func parseJournalSize(out string) uint64 {
	// Format: "Archived and active journals take up 256.0M in the file system."
	for _, part := range strings.Fields(out) {
		part = strings.TrimSuffix(part, ".")
		if strings.HasSuffix(part, "M") {
			v, _ := strconv.ParseFloat(strings.TrimSuffix(part, "M"), 64)
			return uint64(v * 1024 * 1024)
		}
		if strings.HasSuffix(part, "G") {
			v, _ := strconv.ParseFloat(strings.TrimSuffix(part, "G"), 64)
			return uint64(v * 1024 * 1024 * 1024)
		}
		if strings.HasSuffix(part, "K") {
			v, _ := strconv.ParseFloat(strings.TrimSuffix(part, "K"), 64)
			return uint64(v * 1024)
		}
	}
	return 0
}
