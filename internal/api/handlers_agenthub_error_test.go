package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/IamYGT/heyserver/internal/agenthub"
)

func TestWriteAgentHubErrorMapsOfflineNodeToConflict(t *testing.T) {
	recorder := httptest.NewRecorder()
	writeAgentHubError(recorder, agenthub.ErrNodeOffline)

	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusConflict)
	}
	var payload struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if !strings.Contains(payload.Error, "offline") {
		t.Fatalf("error = %q, want offline detail", payload.Error)
	}
}

func TestWriteRemoteAgentUpdateErrorPreservesOfflineConflict(t *testing.T) {
	recorder := httptest.NewRecorder()
	writeRemoteAgentUpdateError(recorder, agenthub.ErrNodeOffline)

	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusConflict)
	}
}
