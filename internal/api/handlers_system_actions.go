package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/IamYGT/heyserver/internal/db"
	"github.com/IamYGT/heyserver/internal/services/systemactions"
)

type systemActionExecutor interface {
	TerminateProcess(pid int, signal string, expectedStartTime uint64) (systemactions.ProcessSignalResult, error)
	ControlService(ctx context.Context, service, action string) (string, error)
	ResetSwap(ctx context.Context) (string, error)
	OptimizeMemory(ctx context.Context) (string, error)
	CleanTemporaryFiles(ctx context.Context) (string, error)
	ScheduleReboot(ctx context.Context) (string, error)
	CancelScheduledReboot(ctx context.Context) (string, error)
	RebootPending(ctx context.Context) (bool, error)
}

type serviceLogReader interface {
	ServiceLogs(ctx context.Context, service string, lines int) ([]systemactions.ServiceLogEntry, error)
}

type systemActionStatusReader interface {
	MaintenanceStatus() systemactions.ActionStatus
}

type systemRebootStatusReader interface {
	RebootSchedule(ctx context.Context) (systemactions.RebootStatus, error)
}

func handleServiceControl(actions systemActionExecutor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		allowLongSystemAction(w)
		var body struct {
			Service string `json:"service"`
			Action  string `json:"action"`
		}
		if err := decodeStrictJSON(r, &body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid request body")
			return
		}

		message, err := actions.ControlService(r.Context(), body.Service, body.Action)
		if err != nil {
			auditHostActionFailure(r, "service_control", fmt.Sprintf("%s %s", body.Service, body.Action), err)
			writeSystemActionError(w, err)
			return
		}
		auditHostAction(r, "service_control", fmt.Sprintf("%s %s", body.Service, body.Action))
		jsonResponse(w, http.StatusOK, map[string]string{"message": message})
	}
}

func handleServiceLogs(reader serviceLogReader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		lines := 100
		if values, present := r.URL.Query()["lines"]; present {
			if len(values) != 1 || values[0] == "" {
				jsonError(w, http.StatusBadRequest, "lines must be provided once between 1 and 500")
				return
			}
			raw := values[0]
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed < 1 || parsed > 500 {
				jsonError(w, http.StatusBadRequest, "lines must be between 1 and 500")
				return
			}
			lines = parsed
		}

		service := r.PathValue("service")
		entries, err := reader.ServiceLogs(r.Context(), service, lines)
		if err != nil {
			writeSystemActionError(w, err)
			return
		}
		jsonResponse(w, http.StatusOK, map[string]any{
			"service": service,
			"lines":   entries,
		})
	}
}

var hostSystemActions = systemactions.New()

func handleProcessTerminate(actions systemActionExecutor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			PID       int    `json:"pid"`
			Signal    string `json:"signal"`
			StartTime uint64 `json:"startTime"`
		}
		if err := decodeStrictJSON(r, &body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if body.Signal == "" {
			jsonError(w, http.StatusBadRequest, "signal is required")
			return
		}

		result, err := actions.TerminateProcess(body.PID, body.Signal, body.StartTime)
		if err != nil {
			auditHostActionFailure(r, "process_terminate", fmt.Sprintf("PID %d start %d signal %s", body.PID, body.StartTime, body.Signal), err)
			writeSystemActionError(w, err)
			return
		}
		outcome := "still-running"
		if result.Exited {
			outcome = "exited"
		} else if !result.Confirmed {
			outcome = "unconfirmed"
		}
		auditHostAction(r, "process_terminate", fmt.Sprintf("PID %d start %d signal %s outcome %s", body.PID, body.StartTime, body.Signal, outcome))
		jsonResponse(w, http.StatusOK, result)
	}
}

func handleMemoryOptimize(actions systemActionExecutor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		allowLongSystemAction(w)
		message, err := actions.OptimizeMemory(r.Context())
		if err != nil {
			auditHostActionFailure(r, "memory_optimize", "memory optimize", err)
			writeSystemActionError(w, err)
			return
		}
		auditHostAction(r, "memory_optimize", message)
		jsonResponse(w, http.StatusOK, map[string]string{"message": message})
	}
}

func handleSwapReset(actions systemActionExecutor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		allowLongSystemAction(w)
		message, err := actions.ResetSwap(r.Context())
		if err != nil {
			auditHostActionFailure(r, "swap_reset", "swap reset", err)
			writeSystemActionError(w, err)
			return
		}
		auditHostAction(r, "swap_reset", "configured swap targets cycled")
		jsonResponse(w, http.StatusOK, map[string]string{"message": message})
	}
}

func allowLongSystemAction(w http.ResponseWriter) {
	// Bounded host operations such as swap evacuation, systemctl actions and
	// tmpfiles cleanup can legitimately exceed the server-wide 30 second write
	// deadline. Keep the request open until the service-level timeout returns.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
}

func handleTemporaryFilesClean(actions systemActionExecutor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		allowLongSystemAction(w)
		message, err := actions.CleanTemporaryFiles(r.Context())
		if err != nil {
			auditHostActionFailure(r, "temporary_files_clean", "temporary files clean", err)
			writeSystemActionError(w, err)
			return
		}
		auditHostAction(r, "temporary_files_clean", "host tmpfiles policy applied")
		jsonResponse(w, http.StatusOK, map[string]string{"message": message})
	}
}

func handleServerReboot(actions systemActionExecutor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		message, err := actions.ScheduleReboot(r.Context())
		if err != nil {
			auditHostActionFailure(r, "server_reboot", "server reboot", err)
			writeSystemActionError(w, err)
			return
		}
		auditHostAction(r, "server_reboot", "reboot scheduled in 10 seconds")
		jsonResponse(w, http.StatusAccepted, map[string]string{"message": message})
	}
}

func handleServerRebootCancel(actions systemActionExecutor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		message, err := actions.CancelScheduledReboot(r.Context())
		if err != nil {
			auditHostActionFailure(r, "server_reboot_cancel", "server reboot cancellation", err)
			writeSystemActionError(w, err)
			return
		}
		auditHostAction(r, "server_reboot_cancel", "pending reboot cancellation requested")
		jsonResponse(w, http.StatusOK, map[string]string{"message": message})
	}
}

func handleServerRebootStatus(actions systemRebootStatusReader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		status, err := actions.RebootSchedule(r.Context())
		if err != nil {
			writeSystemActionError(w, err)
			return
		}
		jsonResponse(w, http.StatusOK, status)
	}
}

func handleSystemActionStatus(actions systemActionStatusReader) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		jsonResponse(w, http.StatusOK, actions.MaintenanceStatus())
	}
}

func writeSystemActionError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, systemactions.ErrInvalidPID), errors.Is(err, systemactions.ErrInvalidSignal), errors.Is(err, systemactions.ErrProcessIdentity),
		errors.Is(err, systemactions.ErrInvalidService), errors.Is(err, systemactions.ErrInvalidAction):
		status = http.StatusBadRequest
	case errors.Is(err, systemactions.ErrProcessNotFound):
		status = http.StatusNotFound
	case errors.Is(err, systemactions.ErrProtectedProcess), errors.Is(err, systemactions.ErrProcessChanged), errors.Is(err, systemactions.ErrInsufficientMemory), errors.Is(err, systemactions.ErrActionInProgress):
		status = http.StatusConflict
	}
	jsonError(w, status, err.Error())
}

func auditHostAction(r *http.Request, action, details string) {
	user := getUserFromContext(r.Context())
	if user == nil || db.Instance() == nil {
		return
	}
	audit := db.NewAuditRepository(db.Instance())
	_ = audit.Insert(buildAuditEntry(user.ID, user.Name, action, "system", details, r))
}

func auditHostActionFailure(r *http.Request, action, details string, err error) {
	if err == nil {
		return
	}
	auditHostAction(r, action, fmt.Sprintf("%s failed: %v", details, err))
}
