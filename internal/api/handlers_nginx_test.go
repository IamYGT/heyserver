package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	nginxsvc "github.com/IamYGT/heyserver/internal/services/nginx"
)

func TestHandleNginxSaveEnforcesChecksumAndValidation(t *testing.T) {
	root := t.TempDir()
	available := filepath.Join(root, "available")
	enabled := filepath.Join(root, "enabled")
	for _, directory := range []string{available, enabled} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	target := filepath.Join(available, "site.conf")
	if err := os.WriteFile(target, []byte("server { listen 80; }\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "nginx"), []byte("#!/bin/sh\n/bin/grep -q INVALID \"$HSERVER_TEST_NGINX_CONFIG\" && exit 1\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root)
	t.Setenv("HSERVER_TEST_NGINX_CONFIG", target)
	svc := nginxsvc.NewWithConfig(nginxsvc.ServiceConfig{SitesAvailable: available, SitesEnabled: enabled})
	observed, err := svc.GetConfig("site.conf")
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPut, "/api/nginx/configs/site.conf", strings.NewReader(`{"content":"server { listen 8080; }\n","checksum":"`+observed.Checksum+`"}`))
	request.SetPathValue("filename", "site.conf")
	response := httptest.NewRecorder()
	handleNginxSave(svc)(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"backup"`) || !strings.Contains(response.Body.String(), `"checksum"`) {
		t.Fatalf("successful save = %d %s", response.Code, response.Body.String())
	}

	stale := httptest.NewRequest(http.MethodPut, "/api/nginx/configs/site.conf", strings.NewReader(`{"content":"server {}\n","checksum":"`+observed.Checksum+`"}`))
	stale.SetPathValue("filename", "site.conf")
	staleResponse := httptest.NewRecorder()
	handleNginxSave(svc)(staleResponse, stale)
	if staleResponse.Code != http.StatusConflict {
		t.Fatalf("stale save = %d %s", staleResponse.Code, staleResponse.Body.String())
	}

	current, err := svc.GetConfig("site.conf")
	if err != nil {
		t.Fatal(err)
	}
	invalid := httptest.NewRequest(http.MethodPut, "/api/nginx/configs/site.conf", strings.NewReader(`{"content":"INVALID\n","checksum":"`+current.Checksum+`"}`))
	invalid.SetPathValue("filename", "site.conf")
	invalidResponse := httptest.NewRecorder()
	handleNginxSave(svc)(invalidResponse, invalid)
	if invalidResponse.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid save = %d %s", invalidResponse.Code, invalidResponse.Body.String())
	}

	oversized := httptest.NewRequest(http.MethodPut, "/api/nginx/configs/site.conf", strings.NewReader(`{"content":"`+strings.Repeat("x", 2097153)+`","checksum":"`+current.Checksum+`"}`))
	oversized.SetPathValue("filename", "site.conf")
	oversizedResponse := httptest.NewRecorder()
	handleNginxSave(svc)(oversizedResponse, oversized)
	if oversizedResponse.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized save = %d %s", oversizedResponse.Code, oversizedResponse.Body.String())
	}
}

func TestHandleNginxCreateRejectsUnknownFieldsAndReportsConflict(t *testing.T) {
	root := t.TempDir()
	available := filepath.Join(root, "available")
	enabled := filepath.Join(root, "enabled")
	snippets := filepath.Join(root, "snippets")
	vhosts := filepath.Join(root, "vhosts")
	for _, directory := range []string{available, enabled, snippets, vhosts} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{
		"hserver-acme-challenge.conf", "hserver-ssl-params.conf", "hserver-security-headers.conf",
		"hserver-security-deny.conf", "hserver-compression.conf", "hserver-static-cache.conf",
		"hserver-php-fpm.conf", "hserver-proxy-params.conf",
	} {
		if err := os.WriteFile(filepath.Join(snippets, name), []byte("# test\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "nginx"), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root)
	svc := nginxsvc.NewWithConfig(nginxsvc.ServiceConfig{
		SitesAvailable: available,
		SitesEnabled:   enabled,
		SnippetsDir:    snippets,
		VhostsRoot:     vhosts,
	})

	unknown := httptest.NewRequest(http.MethodPost, "/api/nginx/configs", strings.NewReader(`{"domain":"site.example","type":"static","unknown":true}`))
	unknownResponse := httptest.NewRecorder()
	handleNginxCreate(svc)(unknownResponse, unknown)
	if unknownResponse.Code != http.StatusBadRequest {
		t.Fatalf("unknown field = %d %s", unknownResponse.Code, unknownResponse.Body.String())
	}

	create := func() *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/api/nginx/configs", strings.NewReader(`{"domain":"site.example","type":"static","useSSL":false}`))
		response := httptest.NewRecorder()
		handleNginxCreate(svc)(response, request)
		return response
	}
	created := create()
	if created.Code != http.StatusCreated || !strings.Contains(created.Body.String(), `"checksum"`) {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}
	duplicate := create()
	if duplicate.Code != http.StatusConflict {
		t.Fatalf("duplicate = %d %s", duplicate.Code, duplicate.Body.String())
	}
}

func TestHandleNginxArchiveRequiresExactChecksumAndDisabledSite(t *testing.T) {
	root := t.TempDir()
	available := filepath.Join(root, "available")
	enabled := filepath.Join(root, "enabled")
	for _, directory := range []string{available, enabled} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	target := filepath.Join(available, "site.conf")
	if err := os.WriteFile(target, []byte("server {}\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "nginx"), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root)
	svc := nginxsvc.NewWithConfig(nginxsvc.ServiceConfig{SitesAvailable: available, SitesEnabled: enabled})
	observed, err := svc.GetConfig("site.conf")
	if err != nil {
		t.Fatal(err)
	}
	request := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodDelete, "/api/nginx/configs/site.conf", strings.NewReader(body))
		req.SetPathValue("filename", "site.conf")
		response := httptest.NewRecorder()
		handleNginxArchive(svc)(response, req)
		return response
	}
	for _, body := range []string{`{}`, `{"checksum":"` + observed.Checksum + `","unknown":true}`, `{"checksum":"` + observed.Checksum + `"} trailing`} {
		response := request(body)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid body %q = %d %s", body, response.Code, response.Body.String())
		}
	}
	if err := os.Symlink(target, filepath.Join(enabled, "site.conf")); err != nil {
		t.Fatal(err)
	}
	enabledResponse := request(`{"checksum":"` + observed.Checksum + `"}`)
	if enabledResponse.Code != http.StatusConflict {
		t.Fatalf("enabled archive = %d %s", enabledResponse.Code, enabledResponse.Body.String())
	}
	if err := os.Remove(filepath.Join(enabled, "site.conf")); err != nil {
		t.Fatal(err)
	}
	staleResponse := request(`{"checksum":"` + strings.Repeat("0", 64) + `"}`)
	if staleResponse.Code != http.StatusConflict {
		t.Fatalf("stale archive = %d %s", staleResponse.Code, staleResponse.Body.String())
	}
	archived := request(`{"checksum":"` + observed.Checksum + `"}`)
	if archived.Code != http.StatusOK || !strings.Contains(archived.Body.String(), `"archive"`) || !strings.Contains(archived.Body.String(), `"checksum"`) {
		t.Fatalf("archive = %d %s", archived.Code, archived.Body.String())
	}
}

func TestHandleNginxArchiveListAndRestoreRequireObservedChecksum(t *testing.T) {
	root := t.TempDir()
	available := filepath.Join(root, "available")
	enabled := filepath.Join(root, "enabled")
	for _, directory := range []string{available, enabled} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "nginx"), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root)
	archive := "site.conf.hserver-archive-20260827T120000.000000000Z"
	if err := os.WriteFile(filepath.Join(available, archive), []byte("server {}\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	svc := nginxsvc.NewWithConfig(nginxsvc.ServiceConfig{SitesAvailable: available, SitesEnabled: enabled})
	listResponse := httptest.NewRecorder()
	handleNginxArchiveList(svc)(listResponse, httptest.NewRequest(http.MethodGet, "/api/nginx/archives", nil))
	if listResponse.Code != http.StatusOK {
		t.Fatalf("archive list = %d %s", listResponse.Code, listResponse.Body.String())
	}
	var archives []nginxsvc.ConfigArchive
	if err := json.NewDecoder(listResponse.Body).Decode(&archives); err != nil || len(archives) != 1 {
		t.Fatalf("archive list = %+v, %v", archives, err)
	}
	restore := func(body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/api/nginx/archives/"+archive+"/restore", strings.NewReader(body))
		request.SetPathValue("archive", archive)
		response := httptest.NewRecorder()
		handleNginxArchiveRestore(svc)(response, request)
		return response
	}
	for _, body := range []string{`{}`, `{"checksum":"` + archives[0].Checksum + `","unknown":true}`, `{"checksum":"` + archives[0].Checksum + `"} trailing`} {
		response := restore(body)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid restore body %q = %d %s", body, response.Code, response.Body.String())
		}
	}
	stale := restore(`{"checksum":"` + strings.Repeat("0", 64) + `"}`)
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale restore = %d %s", stale.Code, stale.Body.String())
	}
	restored := restore(`{"checksum":"` + archives[0].Checksum + `"}`)
	if restored.Code != http.StatusOK || !strings.Contains(restored.Body.String(), `"filename":"site.conf"`) || !strings.Contains(restored.Body.String(), `"isEnabled":false`) {
		t.Fatalf("restore = %d %s", restored.Code, restored.Body.String())
	}
	if _, err := os.Stat(filepath.Join(available, archive)); err != nil {
		t.Fatalf("restore removed archive: %v", err)
	}
	conflict := restore(`{"checksum":"` + archives[0].Checksum + `"}`)
	if conflict.Code != http.StatusConflict {
		t.Fatalf("existing target restore = %d %s", conflict.Code, conflict.Body.String())
	}
}

func TestHandleNginxBackupListAndRestoreRequireBothChecksums(t *testing.T) {
	root := t.TempDir()
	available := filepath.Join(root, "available")
	enabled := filepath.Join(root, "enabled")
	for _, directory := range []string{available, enabled} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "nginx"), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root)
	if err := os.WriteFile(filepath.Join(available, "site.conf"), []byte("server { listen 8080; }\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	backup := "site.conf.hserver-backup-20260827T130000.000000000Z"
	if err := os.WriteFile(filepath.Join(available, backup), []byte("server { listen 80; }\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	svc := nginxsvc.NewWithConfig(nginxsvc.ServiceConfig{SitesAvailable: available, SitesEnabled: enabled})
	listResponse := httptest.NewRecorder()
	handleNginxBackupList(svc)(listResponse, httptest.NewRequest(http.MethodGet, "/api/nginx/backups", nil))
	if listResponse.Code != http.StatusOK {
		t.Fatalf("backup list = %d %s", listResponse.Code, listResponse.Body.String())
	}
	var backups []nginxsvc.ConfigBackup
	if err := json.NewDecoder(listResponse.Body).Decode(&backups); err != nil || len(backups) != 1 {
		t.Fatalf("backup list = %+v, %v", backups, err)
	}
	current, err := svc.GetConfig("site.conf")
	if err != nil {
		t.Fatal(err)
	}
	restore := func(body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/api/nginx/backups/"+backup+"/restore", strings.NewReader(body))
		request.SetPathValue("backup", backup)
		response := httptest.NewRecorder()
		handleNginxBackupRestore(svc)(response, request)
		return response
	}
	for _, body := range []string{
		`{}`,
		`{"backupChecksum":"` + backups[0].Checksum + `","currentChecksum":"` + current.Checksum + `","unknown":true}`,
		`{"backupChecksum":"` + backups[0].Checksum + `","currentChecksum":"` + current.Checksum + `"} trailing`,
	} {
		response := restore(body)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid backup restore body %q = %d %s", body, response.Code, response.Body.String())
		}
	}
	stale := restore(`{"backupChecksum":"` + backups[0].Checksum + `","currentChecksum":"` + strings.Repeat("0", 64) + `"}`)
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale backup restore = %d %s", stale.Code, stale.Body.String())
	}
	restored := restore(`{"backupChecksum":"` + backups[0].Checksum + `","currentChecksum":"` + current.Checksum + `"}`)
	if restored.Code != http.StatusOK || !strings.Contains(restored.Body.String(), `"filename":"site.conf"`) || !strings.Contains(restored.Body.String(), `"recovery"`) {
		t.Fatalf("backup restore = %d %s", restored.Code, restored.Body.String())
	}
}

func TestHandleNginxStateRequiresAndAppliesExplicitDesiredState(t *testing.T) {
	root := t.TempDir()
	available := filepath.Join(root, "available")
	enabled := filepath.Join(root, "enabled")
	for _, directory := range []string{available, enabled} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(available, "site.conf"), []byte("server {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := nginxsvc.NewWithConfig(nginxsvc.ServiceConfig{SitesAvailable: available, SitesEnabled: enabled})
	request := func(filename, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/nginx/configs/"+filename+"/toggle", strings.NewReader(body))
		req.SetPathValue("filename", filename)
		response := httptest.NewRecorder()
		handleNginxState(svc)(response, req)
		return response
	}
	for _, body := range []string{`{}`, `{"enabled":true,"unknown":false}`, `{"enabled":true} trailing`} {
		response := request("site.conf", body)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid body %q = %d %s", body, response.Code, response.Body.String())
		}
	}
	for _, desired := range []bool{true, true, false, false} {
		response := request("site.conf", fmt.Sprintf(`{"enabled":%t}`, desired))
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), fmt.Sprintf(`"isEnabled":%t`, desired)) {
			t.Fatalf("desired %t = %d %s", desired, response.Code, response.Body.String())
		}
	}
	missing := request("missing.conf", `{"enabled":true}`)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing = %d %s", missing.Code, missing.Body.String())
	}
}

func TestHandleNginxTestReturnsFailedCheckAsResult(t *testing.T) {
	dir := t.TempDir()
	nginxPath := filepath.Join(dir, "nginx")
	script := "#!/bin/sh\necho 'nginx: invalid directive' >&2\nexit 1\n"
	if err := os.WriteFile(nginxPath, []byte(script), 0755); err != nil {
		t.Fatalf("write fake nginx: %v", err)
	}
	t.Setenv("PATH", dir)

	req := httptest.NewRequest(http.MethodPost, "/api/nginx/test", nil)
	rec := httptest.NewRecorder()
	handleNginxTest(nginxsvc.New())(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var body struct {
		OK     bool   `json:"ok"`
		Output string `json:"output"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.OK {
		t.Fatal("ok = true, want false")
	}
	if body.Output != "nginx: invalid directive" {
		t.Fatalf("output = %q", body.Output)
	}
}

func TestHandleNginxListReportsUnconfiguredPathsAsUnavailable(t *testing.T) {
	t.Parallel()

	svc := nginxsvc.NewWithConfig(nginxsvc.ServiceConfig{
		SitesAvailable: "relative/available",
		SitesEnabled:   "relative/enabled",
		VhostsRoot:     "relative/sites",
	})
	recorder := httptest.NewRecorder()
	handleNginxList(svc)(recorder, httptest.NewRequest(http.MethodGet, "/api/nginx/configs", nil))

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", recorder.Code, recorder.Body.String())
	}
}
