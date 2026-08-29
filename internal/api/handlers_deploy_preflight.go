package api

import (
	"crypto/subtle"
	"net/http"
)

func handleInternalDeployPreflight(cronSecret string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cronSecret == "" {
			jsonError(w, http.StatusForbidden, "preflight disabled — set HSERVER_CRON_SECRET")
			return
		}
		if !isLoopbackRequest(r) {
			jsonError(w, http.StatusForbidden, "preflight only allowed from localhost")
			return
		}
		got := r.Header.Get("X-Cron-Secret")
		if subtle.ConstantTimeCompare([]byte(got), []byte(cronSecret)) != 1 {
			jsonError(w, http.StatusForbidden, "invalid cron secret")
			return
		}
		active := backupSvc != nil && backupSvc.HasActiveJobTypes("snapshot", "snapshot_restore")
		jsonResponse(w, http.StatusOK, map[string]any{
			"ok":             !active,
			"activeSnapshot": active,
			"deployAllowed":  !active,
		})
	}
}
