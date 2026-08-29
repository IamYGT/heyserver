package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/IamYGT/heyserver/internal/services/disk"
)

type diskMaintenanceCoordinator interface {
	BeginMaintenance(action string) (release func(), err error)
}

func handleDiskAnalysisStatus() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		status, err := disk.GetAnalysisStatus()
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, status)
	}
}

func handleDiskAnalysisStart() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		status, err := disk.StartAnalysis(r.Context())
		if errors.Is(err, disk.ErrAnalysisRunning) {
			jsonResponse(w, http.StatusOK, status)
			return
		}
		if err != nil {
			auditHostAction(r, "disk_analysis", "failed to start")
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		auditHostAction(r, "disk_analysis", status.ID+": queued")
		jsonResponse(w, http.StatusAccepted, status)
	}
}

// GET /api/disk/overview
func handleDiskOverview() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		partitions, err := disk.GetPartitions()
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "failed to get partitions: "+err.Error())
			return
		}

		ioStats, _ := disk.GetIOStats()

		var totalSize, totalUsed, totalFree uint64
		for _, p := range partitions {
			if p.FSType != "tmpfs" {
				totalSize += p.Size
				totalUsed += p.Used
				totalFree += p.Available
			}
		}

		jsonResponse(w, http.StatusOK, disk.Overview{
			Partitions: partitions,
			IOStats:    ioStats,
			TotalSize:  totalSize,
			TotalUsed:  totalUsed,
			TotalFree:  totalFree,
		})
	}
}

// GET /api/disk/io
func handleDiskIO() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		stats, err := disk.GetIOStats()
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "failed to get I/O stats: "+err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, stats)
	}
}

// GET /api/disk/smart/{device}
func handleDiskSmart() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		device := r.PathValue("device")
		if device == "" {
			jsonError(w, http.StatusBadRequest, "disk device is required")
			return
		}

		var info *disk.SmartInfo
		var err error
		if device == "root" {
			info, err = disk.GetRootSmartInfo()
		} else {
			info, err = disk.GetSmartInfo(device)
		}
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "smart check failed: "+err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, info)
	}
}

// GET /api/disk/list?path=/var — instant directory listing
func handleDiskList() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dirPath := r.URL.Query().Get("path")
		if dirPath == "" {
			dirPath = "/"
		}

		entries, err := disk.ListDir(dirPath)
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, map[string]interface{}{
			"path":    dirPath,
			"entries": entries,
			"count":   len(entries),
		})
	}
}

// GET /api/disk/dirsize?path=/var — single directory size (can be slow)
func handleDiskDirSize() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dirPath := r.URL.Query().Get("path")
		if dirPath == "" {
			jsonError(w, http.StatusBadRequest, "path is required")
			return
		}

		size, err := disk.GetDirSize(dirPath)
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, map[string]interface{}{
			"path": dirPath,
			"size": size,
		})
	}
}

// GET /api/disk/usage?path=/var&depth=1
// The path is deliberately required: an accidental pathless request used to
// start a full recursive scan of / and could hold an API worker for 30 seconds.
func handleDiskUsage() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dirPath := r.URL.Query().Get("path")
		if dirPath == "" {
			jsonError(w, http.StatusBadRequest, "path is required")
			return
		}

		depth := 1
		if d := r.URL.Query().Get("depth"); d != "" {
			depth, _ = strconv.Atoi(d)
			if depth < 1 || depth > 3 {
				depth = 1
			}
		}

		usages, err := disk.GetDirUsage(dirPath, depth)
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, usages)
	}
}

// GET /api/disk/largest?path=/var&limit=20
// The path is required so an incomplete client request cannot start a recursive
// scan of the entire root filesystem.
func handleDiskLargest() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dirPath := r.URL.Query().Get("path")
		if dirPath == "" {
			jsonError(w, http.StatusBadRequest, "path is required")
			return
		}

		limit := 20
		if l := r.URL.Query().Get("limit"); l != "" {
			limit, _ = strconv.Atoi(l)
		}

		files, err := disk.GetLargestFiles(dirPath, limit)
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, files)
	}
}

// GET /api/disk/cleanup/scan
func handleDiskCleanupScan() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		targets, err := disk.ScanCleanup()
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "cleanup scan failed: "+err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, targets)
	}
}

// POST /api/disk/cleanup/execute
func handleDiskCleanupExecute(maintenance diskMaintenanceCoordinator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		allowLongSystemAction(w)
		var body struct {
			Targets []string `json:"targets"`
		}
		if err := decodeStrictJSON(r, &body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if len(body.Targets) == 0 || len(body.Targets) > 20 {
			jsonError(w, http.StatusBadRequest, "select between 1 and 20 cleanup targets")
			return
		}
		seen := make(map[string]struct{}, len(body.Targets))
		for _, target := range body.Targets {
			if !disk.IsCleanupTarget(target) {
				jsonError(w, http.StatusBadRequest, "unknown cleanup target: "+target)
				return
			}
			if _, exists := seen[target]; exists {
				jsonError(w, http.StatusBadRequest, "duplicate cleanup target: "+target)
				return
			}
			seen[target] = struct{}{}
		}
		release, err := maintenance.BeginMaintenance("disk-cleanup")
		if err != nil {
			writeSystemActionError(w, err)
			return
		}
		defer release()

		beforeTargets, _ := disk.ScanCleanup()
		beforeSizes := make(map[string]uint64, len(beforeTargets))
		for _, target := range beforeTargets {
			beforeSizes[target.ID] = target.Size
		}
		rootBefore := rootAvailableBytes()
		var results []map[string]any
		for _, id := range body.Targets {
			msg, err := disk.ExecuteCleanup(id)
			if err != nil {
				results = append(results, map[string]any{"id": id, "status": "error", "message": err.Error(), "reclaimed": uint64(0)})
			} else {
				results = append(results, map[string]any{"id": id, "status": "ok", "message": msg})
			}
		}
		afterTargets, _ := disk.ScanCleanup()
		afterSizes := make(map[string]uint64, len(afterTargets))
		for _, target := range afterTargets {
			afterSizes[target.ID] = target.Size
		}
		for _, result := range results {
			id, _ := result["id"].(string)
			before, after := beforeSizes[id], afterSizes[id]
			reclaimed := uint64(0)
			if before > after {
				reclaimed = before - after
			}
			result["reclaimed"] = reclaimed
			if result["status"] == "ok" {
				auditHostAction(r, "disk_cleanup", id+": reclaimed "+strconv.FormatUint(reclaimed, 10)+" bytes")
			} else {
				auditHostAction(r, "disk_cleanup", id+": failed")
			}
		}
		rootAfter := rootAvailableBytes()

		jsonResponse(w, http.StatusOK, map[string]interface{}{
			"results":               results,
			"root_available_before": rootBefore,
			"root_available_after":  rootAfter,
		})
	}
}

func rootAvailableBytes() uint64 {
	partitions, err := disk.GetPartitions()
	if err != nil {
		return 0
	}
	for _, partition := range partitions {
		if partition.MountPoint == "/" {
			return partition.Available
		}
	}
	return 0
}

// GET /api/disk/mounts
func handleDiskMounts() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		mounts, err := disk.GetMounts()
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "failed to get mounts: "+err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, mounts)
	}
}
