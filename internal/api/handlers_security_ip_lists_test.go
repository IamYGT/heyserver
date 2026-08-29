package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/IamYGT/heyserver/internal/config"
	securityservice "github.com/IamYGT/heyserver/internal/services/security"
)

func TestIPWhitelistHandlersLifecycle(t *testing.T) {
	previousManager := defaultIPMgr
	defaultIPMgr = nil
	t.Cleanup(func() { defaultIPMgr = previousManager })

	cfg := &config.Config{DBPath: filepath.Join(t.TempDir(), "hserver.db")}

	addRequest := httptest.NewRequest(http.MethodPost, "/api/security/ip-whitelist", bytes.NewBufferString(`{"ip":"203.0.113.10","comment":"office"}`))
	addResponse := httptest.NewRecorder()
	handleIPWhitelistAdd(cfg).ServeHTTP(addResponse, addRequest)
	if addResponse.Code != http.StatusCreated {
		t.Fatalf("add whitelist status = %d, want %d; body=%s", addResponse.Code, http.StatusCreated, addResponse.Body.String())
	}

	var added securityservice.IPEntry
	if err := json.Unmarshal(addResponse.Body.Bytes(), &added); err != nil {
		t.Fatalf("decode added whitelist entry: %v", err)
	}
	if added.ListType != securityservice.ListWhitelist || added.IP != "203.0.113.10" || added.Comment != "office" {
		t.Fatalf("unexpected added whitelist entry: %+v", added)
	}

	listResponse := httptest.NewRecorder()
	handleIPWhitelistList(cfg).ServeHTTP(listResponse, httptest.NewRequest(http.MethodGet, "/api/security/ip-whitelist", nil))
	if listResponse.Code != http.StatusOK {
		t.Fatalf("list whitelist status = %d, want %d; body=%s", listResponse.Code, http.StatusOK, listResponse.Body.String())
	}
	var whitelist []securityservice.IPEntry
	if err := json.Unmarshal(listResponse.Body.Bytes(), &whitelist); err != nil {
		t.Fatalf("decode whitelist: %v", err)
	}
	if len(whitelist) != 1 || whitelist[0].ListType != securityservice.ListWhitelist {
		t.Fatalf("whitelist = %+v, want one whitelist entry", whitelist)
	}

	blacklistResponse := httptest.NewRecorder()
	handleIPBlacklistList(cfg).ServeHTTP(blacklistResponse, httptest.NewRequest(http.MethodGet, "/api/security/ip-blacklist", nil))
	var blacklist []securityservice.IPEntry
	if err := json.Unmarshal(blacklistResponse.Body.Bytes(), &blacklist); err != nil {
		t.Fatalf("decode blacklist: %v", err)
	}
	if len(blacklist) != 0 {
		t.Fatalf("blacklist = %+v, want empty list", blacklist)
	}
	if string(bytes.TrimSpace(blacklistResponse.Body.Bytes())) != "[]" {
		t.Fatalf("empty blacklist body = %q, want []", blacklistResponse.Body.String())
	}

	deleteRequest := httptest.NewRequest(http.MethodDelete, "/api/security/ip-whitelist/203.0.113.10", nil)
	deleteRequest.SetPathValue("ip", "203.0.113.10")
	deleteResponse := httptest.NewRecorder()
	handleIPWhitelistDelete(cfg).ServeHTTP(deleteResponse, deleteRequest)
	if deleteResponse.Code != http.StatusNoContent {
		t.Fatalf("delete whitelist status = %d, want %d; body=%s", deleteResponse.Code, http.StatusNoContent, deleteResponse.Body.String())
	}
}
