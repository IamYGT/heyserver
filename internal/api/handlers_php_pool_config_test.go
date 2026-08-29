package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	phpsvc "github.com/IamYGT/heyserver/internal/services/php"
)

func TestLocalPHPPoolConfigHandlersReadAndReplaceObservedFile(t *testing.T) {
	t.Parallel()
	service, poolPath := prepareLocalPHPHandlerService(t, true)

	readRequest := httptest.NewRequest(http.MethodGet, "/api/php/pools/8.4/www/config", nil)
	readRequest.SetPathValue("version", "8.4")
	readRequest.SetPathValue("domain", "www")
	readRecorder := httptest.NewRecorder()
	handlePHPPoolConfigGet(service).ServeHTTP(readRecorder, readRequest)
	if readRecorder.Code != http.StatusOK {
		t.Fatalf("read status = %d, body=%s", readRecorder.Code, readRecorder.Body.String())
	}
	var observed phpsvc.PoolConfigContent
	if err := json.Unmarshal(readRecorder.Body.Bytes(), &observed); err != nil {
		t.Fatal(err)
	}
	if observed.Path != poolPath || observed.Checksum == "" || observed.Content != "[www]\npm = dynamic\n" {
		t.Fatalf("observed = %#v", observed)
	}

	replacement := "[www]\npm = ondemand\n"
	body := `{"content":` + jsonString(replacement) + `,"checksum":` + jsonString(observed.Checksum) + `,"reload":false}`
	writeRequest := httptest.NewRequest(http.MethodPut, "/api/php/pools/8.4/www/config", strings.NewReader(body))
	writeRequest.SetPathValue("version", "8.4")
	writeRequest.SetPathValue("domain", "www")
	writeRecorder := httptest.NewRecorder()
	handlePHPPoolConfigSave(service).ServeHTTP(writeRecorder, writeRequest)
	if writeRecorder.Code != http.StatusOK {
		t.Fatalf("write status = %d, body=%s", writeRecorder.Code, writeRecorder.Body.String())
	}
	content, err := os.ReadFile(poolPath)
	if err != nil || string(content) != replacement {
		t.Fatalf("pool content = %q, err=%v", content, err)
	}
	var receipt phpsvc.PoolConfigReplaceReceipt
	if err := json.Unmarshal(writeRecorder.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.Backup == "" || receipt.Reloaded {
		t.Fatalf("receipt = %#v", receipt)
	}
}

func TestLocalPHPPoolConfigHandlerReportsConflictAndValidationRollback(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		validPHP   bool
		checksum   string
		wantStatus int
		wantError  string
	}{
		{name: "changed checksum", validPHP: true, checksum: strings.Repeat("a", 64), wantStatus: http.StatusConflict, wantError: "checksum changed"},
		{name: "invalid candidate", validPHP: false, wantStatus: http.StatusUnprocessableEntity, wantError: "previous pool configuration was restored"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service, poolPath := prepareLocalPHPHandlerService(t, test.validPHP)
			current, err := os.ReadFile(poolPath)
			if err != nil {
				t.Fatal(err)
			}
			checksum := sha256.Sum256(current)
			expected := hex.EncodeToString(checksum[:])
			if test.checksum != "" {
				expected = test.checksum
			}
			body := `{"content":` + jsonString("[www]\npm = static\n") + `,"checksum":` + jsonString(expected) + `,"reload":false}`
			request := httptest.NewRequest(http.MethodPut, "/api/php/pools/8.4/www/config", strings.NewReader(body))
			request.SetPathValue("version", "8.4")
			request.SetPathValue("domain", "www")
			recorder := httptest.NewRecorder()
			handlePHPPoolConfigSave(service).ServeHTTP(recorder, request)
			if recorder.Code != test.wantStatus || !strings.Contains(recorder.Body.String(), test.wantError) {
				t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
			}
			after, readErr := os.ReadFile(poolPath)
			if readErr != nil || string(after) != string(current) {
				t.Fatalf("pool was not preserved: content=%q err=%v", after, readErr)
			}
		})
	}
}

func TestLocalPHPPoolConfigHandlerRejectsUnknownJSONField(t *testing.T) {
	t.Parallel()
	service, _ := prepareLocalPHPHandlerService(t, true)
	request := httptest.NewRequest(http.MethodPut, "/api/php/pools/8.4/www/config", strings.NewReader(`{"content":"[www]","checksum":"`+strings.Repeat("a", 64)+`","reload":false,"extra":true}`))
	request.SetPathValue("version", "8.4")
	request.SetPathValue("domain", "www")
	recorder := httptest.NewRecorder()
	handlePHPPoolConfigSave(service).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
}

func prepareLocalPHPHandlerService(t *testing.T, validPHP bool) (*phpsvc.Service, string) {
	t.Helper()
	configRoot := filepath.Join(t.TempDir(), "php")
	binaryRoot := filepath.Join(t.TempDir(), "bin")
	poolPath := filepath.Join(configRoot, "8.4", "fpm", "pool.d", "www.conf")
	if err := os.MkdirAll(filepath.Dir(poolPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(poolPath, []byte("[www]\npm = dynamic\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(binaryRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	exitCode := "0"
	if !validPHP {
		exitCode = "1"
	}
	if err := os.WriteFile(filepath.Join(binaryRoot, "php-fpm8.4"), []byte("#!/bin/sh\nexit "+exitCode+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return phpsvc.NewWithConfig(phpsvc.ServiceConfig{ConfigRoot: configRoot, BinaryRoot: binaryRoot}), poolPath
}

func jsonString(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
