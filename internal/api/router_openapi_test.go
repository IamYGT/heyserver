package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/IamYGT/heyserver/internal/testutil"
)

func TestRouterServesEmbeddedOpenAPIContract(t *testing.T) {
	t.Parallel()
	webFS := fstest.MapFS{
		"index.html":   {Data: []byte("<html>panel</html>")},
		"openapi.json": {Data: []byte(`{"openapi":"3.1.0","x-hserver-route-count":395}`)},
	}
	handler := NewRouter(testutil.TestConfig(), webFS, contractTestDeps(t))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/openapi.json", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /openapi.json status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if contentType := recorder.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
		t.Fatalf("GET /openapi.json Content-Type=%q", contentType)
	}
	if !strings.Contains(recorder.Body.String(), `"openapi":"3.1.0"`) {
		t.Fatalf("GET /openapi.json body=%s", recorder.Body.String())
	}
}
