package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPotentiallyLongSystemActionHandlersDisableWriteDeadline(t *testing.T) {
	actions := failingSystemActions{err: io.ErrUnexpectedEOF}
	tests := []struct {
		name    string
		target  string
		body    string
		handler http.Handler
	}{
		{
			name:    "service control",
			target:  "/api/system/actions/service",
			body:    `{"service":"nginx","action":"restart"}`,
			handler: handleServiceControl(actions),
		},
		{
			name:    "temporary file cleanup",
			target:  "/api/system/actions/temp-clean",
			handler: handleTemporaryFilesClean(actions),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := requestWithAuditUser(http.MethodPost, test.target)
			if test.body != "" {
				req.Body = io.NopCloser(strings.NewReader(test.body))
				req.ContentLength = int64(len(test.body))
			}
			writer := &deadlineResponseWriter{ResponseWriter: httptest.NewRecorder()}

			test.handler.ServeHTTP(writer, req)

			if !writer.deadlineSet {
				t.Fatal("handler left the server-wide write deadline active")
			}
		})
	}
}

func TestSystemActionHandlersRejectUnknownRequestFields(t *testing.T) {
	actions := failingSystemActions{err: io.ErrUnexpectedEOF}
	tests := []struct {
		name    string
		target  string
		body    string
		handler http.Handler
	}{
		{
			name:    "service control",
			target:  "/api/system/actions/service",
			body:    `{"service":"nginx","action":"restart","unexpected":true}`,
			handler: handleServiceControl(actions),
		},
		{
			name:    "process signal",
			target:  "/api/system/actions/process",
			body:    `{"pid":123,"startTime":456,"signal":"term","unexpected":true}`,
			handler: handleProcessTerminate(actions),
		},
		{
			name:    "missing process signal",
			target:  "/api/system/actions/process",
			body:    `{"pid":123,"startTime":456}`,
			handler: handleProcessTerminate(actions),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, test.target, strings.NewReader(test.body))
			recorder := httptest.NewRecorder()

			test.handler.ServeHTTP(recorder, req)

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
			}
		})
	}
}
