package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	cronsvc "github.com/IamYGT/heyserver/internal/services/cron"
)

func TestHandleCronStatusReportsMissingDependency(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	recorder := httptest.NewRecorder()
	handleCronStatus()(recorder, httptest.NewRequest(http.MethodGet, "/api/cron/status", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var status cronsvc.Status
	if err := json.NewDecoder(recorder.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if status.State != cronsvc.StateNotInstalled || status.Installed || status.Available {
		t.Fatalf("status = %#v", status)
	}
}

func TestHandleCronListRejectsMissingDependency(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	recorder := httptest.NewRecorder()
	handleCronList()(recorder, httptest.NewRequest(http.MethodGet, "/api/cron/jobs", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestCronMutationHandlersRequireExactPayloads(t *testing.T) {
	tests := []struct {
		name string
		h    http.HandlerFunc
		body string
		want string
	}{
		{
			name: "create rejects unknown fields",
			h:    handleCronCreate(),
			body: `{"schedule":"0 * * * *","command":"/usr/bin/true","unexpected":true}`,
			want: "invalid JSON body",
		},
		{
			name: "update rejects unknown fields",
			h:    handleCronUpdate(),
			body: `{"schedule":"0 * * * *","command":"/usr/bin/true","description":"","isActive":true,"unexpected":true}`,
			want: "invalid JSON body",
		},
		{
			name: "update requires explicit active state",
			h:    handleCronUpdate(),
			body: `{"schedule":"0 * * * *","command":"/usr/bin/true","description":""}`,
			want: "description and isActive are required",
		},
		{
			name: "update requires explicit description",
			h:    handleCronUpdate(),
			body: `{"schedule":"0 * * * *","command":"/usr/bin/true","isActive":true}`,
			want: "description and isActive are required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/cron/jobs/job-1", bytes.NewBufferString(tt.body))
			request.SetPathValue("id", "job-1")
			tt.h(recorder, request)
			if recorder.Code != http.StatusBadRequest || !bytes.Contains(recorder.Body.Bytes(), []byte(tt.want)) {
				t.Fatalf("response = %d %s, want 400 containing %q", recorder.Code, recorder.Body.String(), tt.want)
			}
		})
	}
}
