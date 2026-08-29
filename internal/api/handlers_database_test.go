package api

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/IamYGT/heyserver/internal/models"
	"github.com/IamYGT/heyserver/internal/services/database"
	"github.com/IamYGT/heyserver/internal/testutil"
)

func dbAdminToken(t *testing.T) string {
	t.Helper()
	user := testutil.MakeUser(1, "admin@test.com", models.RoleAdmin)
	return testutil.MakeToken(t, user)
}

func TestIntegration_DatabaseList(t *testing.T) {
	handler := integrationRouter(t)
	req := testutil.NewRequest(t, http.MethodGet, "/api/databases", dbAdminToken(t))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code == http.StatusUnauthorized {
		t.Fatal("expected authenticated access")
	}
	if rec.Code >= 500 {
		t.Fatalf("GET /api/databases status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Sources map[string]databaseSourceStatus `json:"sources"`
	}
	testutil.ParseJSON(t, rec, &body)
	for _, engine := range []string{"postgresql", "mariadb"} {
		source, ok := body.Sources[engine]
		if !ok {
			t.Errorf("missing %s source status: %#v", engine, body.Sources)
			continue
		}
		if source.State == "" {
			t.Errorf("%s source has no structured state: %#v", engine, source)
		}
	}
}

func TestIntegration_DatabaseCreateValidation(t *testing.T) {
	token := dbAdminToken(t)

	tests := []struct {
		name string
		body string
		want int
	}{
		{"invalid json", "{", http.StatusBadRequest},
		{"empty name", `{"engine":"postgres","name":"  "}`, http.StatusBadRequest},
		{"bad engine", `{"engine":"sqlite","name":"app"}`, http.StatusBadRequest},
		{"invalid owner", `{"engine":"postgres","name":"app","owner":"bad owner"}`, http.StatusBadRequest},
		{"unknown field", `{"engine":"postgres","name":"app","extra":true}`, http.StatusBadRequest},
		{"trailing json", `{"engine":"postgres","name":"app"}{}`, http.StatusBadRequest},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			handler := integrationRouter(t)
			req := httptest.NewRequest(http.MethodPost, "/api/databases", strings.NewReader(tc.body))
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status=%d want %d body=%s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

func TestIntegration_DatabaseCreateRejectsOversizedBody(t *testing.T) {
	handler := integrationRouter(t)
	reader := io.MultiReader(
		strings.NewReader(`{"engine":"postgres","name":"app","padding":"`),
		strings.NewReader(strings.Repeat("x", databaseRequestBodyLimit)),
		strings.NewReader(`"}`),
	)
	req := httptest.NewRequest(http.MethodPost, "/api/databases", reader)
	req.Header.Set("Authorization", "Bearer "+dbAdminToken(t))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d want 413 body=%s", rec.Code, rec.Body.String())
	}
}

func TestIntegration_DatabaseDropValidation(t *testing.T) {
	handler := integrationRouter(t)
	token := dbAdminToken(t)

	req := httptest.NewRequest(http.MethodDelete, "/api/databases/postgres/mydb", strings.NewReader(`{"confirm":"WRONG"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400 body=%s", rec.Code, rec.Body.String())
	}
}

func TestIntegration_DatabaseDropRejectsAmbiguousBodyAndName(t *testing.T) {
	handler := integrationRouter(t)
	token := dbAdminToken(t)
	for _, tc := range []struct {
		name string
		path string
		body string
	}{
		{name: "unknown field", path: "/api/databases/postgres/mydb", body: `{"confirm":"DROP mydb","extra":true}`},
		{name: "trailing json", path: "/api/databases/postgres/mydb", body: `{"confirm":"DROP mydb"}{}`},
		{name: "invalid database name", path: "/api/databases/postgres/bad%20name", body: `{"confirm":"DROP bad name"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodDelete, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status=%d want 400 body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestIntegration_DatabaseQueryValidation(t *testing.T) {
	handler := integrationRouter(t)
	token := dbAdminToken(t)

	req := httptest.NewRequest(http.MethodPost, "/api/databases/postgres/mydb/query", strings.NewReader(`{"query":""}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty query: status=%d body=%s", rec.Code, rec.Body.String())
	}

	writeReq := httptest.NewRequest(http.MethodPost, "/api/databases/postgres/mydb/query",
		strings.NewReader(`{"query":"SELECT 1","write_mode":true}`))
	writeReq.Header.Set("Authorization", "Bearer "+token)
	writeReq.Header.Set("Content-Type", "application/json")
	writeRec := httptest.NewRecorder()
	handler.ServeHTTP(writeRec, writeReq)
	if writeRec.Code != http.StatusBadRequest || !strings.Contains(writeRec.Body.String(), "writable terminal") {
		t.Fatalf("write mode: status=%d body=%s", writeRec.Code, writeRec.Body.String())
	}
}

func TestIntegration_DatabaseQueryRejectsAmbiguousOrUnsafeInput(t *testing.T) {
	handler := integrationRouter(t)
	token := dbAdminToken(t)
	for _, tc := range []struct {
		name string
		path string
		body string
	}{
		{name: "unknown field", path: "/api/databases/postgres/app/query", body: `{"query":"SELECT 1","extra":true}`},
		{name: "trailing json", path: "/api/databases/postgres/app/query", body: `{"query":"SELECT 1"}{}`},
		{name: "nul byte", path: "/api/databases/postgres/app/query", body: `{"query":"SELECT \u0000"}`},
		{name: "invalid database name", path: "/api/databases/postgres/bad%20name/query", body: `{"query":"SELECT 1"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status=%d want 400 body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestIntegration_DatabaseTablesBadEngine(t *testing.T) {
	handler := integrationRouter(t)
	req := testutil.NewRequest(t, http.MethodGet, "/api/databases/invalid/mydb/tables", dbAdminToken(t))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400", rec.Code)
	}
}

func TestIntegration_DatabaseUsers(t *testing.T) {
	handler := integrationRouter(t)
	req := testutil.NewRequest(t, http.MethodGet, "/api/databases/users", dbAdminToken(t))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestIntegration_PGMRestoreValidation(t *testing.T) {
	handler := integrationRouter(t)
	token := dbAdminToken(t)
	req := httptest.NewRequest(http.MethodPost, "/api/databases/pgm-restore", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400", rec.Code)
	}
}

func TestIntegration_PGMRestoreRejectsAmbiguousAndInvalidInput(t *testing.T) {
	handler := integrationRouter(t)
	token := dbAdminToken(t)
	for _, body := range []string{
		`{"database":"app","backupPath":"/etc/passwd","extra":true}`,
		`{"database":"app","backupPath":"/etc/passwd"}{}`,
		`{"database":"app","backupPath":"/etc/passwd"}`,
	} {
		req := httptest.NewRequest(http.MethodPost, "/api/databases/pgm-restore", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status=%d want 400 body=%s", rec.Code, rec.Body.String())
		}
	}
}

func TestIntegration_PGMBackupFilesClassifiesInput(t *testing.T) {
	handler := integrationRouter(t)
	req := testutil.NewRequest(t, http.MethodGet, "/api/databases/pgm-backup-files/bad%20name", dbAdminToken(t))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want=400 body=%s", rec.Code, rec.Body.String())
	}
}

func TestPGMBackupErrorStatus(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{name: "invalid", err: database.ErrInvalidBackupInput, want: http.StatusBadRequest},
		{name: "missing backup", err: database.ErrBackupNotFound, want: http.StatusNotFound},
		{name: "unavailable root", err: database.ErrBackupRootUnavailable, want: http.StatusServiceUnavailable},
		{name: "unexpected", err: errors.New("unexpected"), want: http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := pgmBackupErrorStatus(tc.err); got != tc.want {
				t.Fatalf("pgmBackupErrorStatus()=%d want=%d", got, tc.want)
			}
		})
	}
}

func TestIntegration_PGMCredentialGetMissing(t *testing.T) {
	handler := integrationRouter(t)
	req := testutil.NewRequest(t, http.MethodGet, "/api/databases/credentials/bad%20name", dbAdminToken(t))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want=400 body=%s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control=%q want no-store", rec.Header().Get("Cache-Control"))
	}
}
