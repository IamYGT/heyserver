package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/IamYGT/heyserver/internal/services/systemactions"
)

type fakeServiceLogReader struct {
	service string
	lines   int
	result  []systemactions.ServiceLogEntry
	err     error
}

func (f *fakeServiceLogReader) ServiceLogs(_ context.Context, service string, lines int) ([]systemactions.ServiceLogEntry, error) {
	f.service = service
	f.lines = lines
	return f.result, f.err
}

func TestHandleServiceLogsReturnsRequestedJournalLines(t *testing.T) {
	reader := &fakeServiceLogReader{result: []systemactions.ServiceLogEntry{
		{Timestamp: "2026-08-25T20:00:00Z", Unit: "nginx.service", Priority: 6, Message: "first"},
		{Timestamp: "2026-08-25T20:01:00Z", Unit: "nginx.service", Priority: 3, Message: "second"},
	}}
	req := httptest.NewRequest(http.MethodGet, "/api/system/services/nginx/logs?lines=80", nil)
	req.SetPathValue("service", "nginx")
	rec := httptest.NewRecorder()

	handleServiceLogs(reader).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Service string                          `json:"service"`
		Lines   []systemactions.ServiceLogEntry `json:"lines"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if reader.service != "nginx" || reader.lines != 80 || body.Service != "nginx" || !reflect.DeepEqual(body.Lines, reader.result) {
		t.Fatalf("reader=%s/%d body=%#v", reader.service, reader.lines, body)
	}
}

func TestHandleServiceLogsRejectsInvalidBoundsAndService(t *testing.T) {
	reader := &fakeServiceLogReader{}
	req := httptest.NewRequest(http.MethodGet, "/api/system/services/nginx/logs?lines=501", nil)
	req.SetPathValue("service", "nginx")
	rec := httptest.NewRecorder()
	handleServiceLogs(reader).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid lines status = %d", rec.Code)
	}

	reader.err = systemactions.ErrInvalidService
	req = httptest.NewRequest(http.MethodGet, "/api/system/services/hserver/logs", nil)
	req.SetPathValue("service", "hserver")
	rec = httptest.NewRecorder()
	handleServiceLogs(reader).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid service status = %d", rec.Code)
	}
	if !errors.Is(reader.err, systemactions.ErrInvalidService) {
		t.Fatal("fake error changed unexpectedly")
	}
}

func TestHandleServiceLogsRejectsAmbiguousLineLimit(t *testing.T) {
	reader := &fakeServiceLogReader{}
	for _, target := range []string{
		"/api/system/services/nginx/logs?lines=",
		"/api/system/services/nginx/logs?lines=10&lines=20",
	} {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req.SetPathValue("service", "nginx")
		recorder := httptest.NewRecorder()
		handleServiceLogs(reader).ServeHTTP(recorder, req)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("target %q status = %d, body = %s", target, recorder.Code, recorder.Body.String())
		}
	}
}
