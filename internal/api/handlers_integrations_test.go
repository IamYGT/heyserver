package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/IamYGT/heyserver/extensions"
	"github.com/IamYGT/heyserver/internal/models"
	"github.com/IamYGT/heyserver/internal/testutil"
)

func TestIntegrationCatalogRequiresAuthentication(t *testing.T) {
	handler := integrationRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/api/integrations/catalog", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestIntegrationCatalogReturnsSchemaV1Catalog(t *testing.T) {
	handler := integrationRouter(t)
	viewer := testutil.MakeUser(1, "viewer@test.com", models.RoleViewer)
	req := testutil.NewRequest(t, http.MethodGet, "/api/integrations/catalog", testutil.MakeToken(t, viewer))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body=%s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}

	var catalog extensions.Catalog
	testutil.ParseJSON(t, rec, &catalog)
	if catalog.SchemaVersion != 1 {
		t.Fatalf("schema_version = %d, want 1", catalog.SchemaVersion)
	}
	if len(catalog.Entries) < 15 {
		t.Fatalf("entries = %d, want at least 15", len(catalog.Entries))
	}

	knownIDs := map[string]bool{
		"cloudflare.dns":         false,
		"backup.snapshot.restic": false,
		"runtime.php_fpm":        false,
		"notification.delivery":  false,
	}
	for _, entry := range catalog.Entries {
		if _, ok := knownIDs[entry.ID]; ok {
			knownIDs[entry.ID] = true
		}
	}
	for id, found := range knownIDs {
		if !found {
			t.Errorf("catalog missing known id %q", id)
		}
	}
}

func TestRuntimeCatalogRouteManifestProtected(t *testing.T) {
	for _, route := range AllRoutes() {
		if route.Method == http.MethodGet && route.Path == "/api/integrations/catalog" {
			if route.Auth != RouteProtected {
				t.Fatalf("catalog route auth = %q, want %q", route.Auth, RouteProtected)
			}
			return
		}
	}
	t.Fatal("catalog route missing from route manifest")
}
