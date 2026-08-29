package api

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestRouteManifest_MatchesRouter ensures routes_manifest.go stays in sync with router.go.
func TestRouteManifest_MatchesRouter(t *testing.T) {
	t.Parallel()
	routerSrc, err := os.ReadFile("router.go")
	if err != nil {
		t.Fatalf("read router.go: %v", err)
	}

	re := regexp.MustCompile(`mux\.HandleFunc\("([A-Z]+) ([^"]+)"`)
	matches := re.FindAllStringSubmatch(string(routerSrc), -1)
	if len(matches) == 0 {
		t.Fatal("no routes found in router.go")
	}

	routerRoutes := make(map[string]struct{}, len(matches))
	for _, m := range matches {
		key := m[1] + " " + m[2]
		// SPA catch-all — not part of API contract manifest.
		if key == "GET /" {
			continue
		}
		routerRoutes[key] = struct{}{}
	}

	manifestRoutes := make(map[string]struct{}, len(AllRoutes()))
	for _, spec := range AllRoutes() {
		key := spec.Method + " " + spec.Path
		manifestRoutes[key] = struct{}{}
	}

	var missingInManifest []string
	for key := range routerRoutes {
		if _, ok := manifestRoutes[key]; !ok {
			missingInManifest = append(missingInManifest, key)
		}
	}
	var extraInManifest []string
	for key := range manifestRoutes {
		if _, ok := routerRoutes[key]; !ok {
			extraInManifest = append(extraInManifest, key)
		}
	}

	if len(missingInManifest) > 0 {
		t.Errorf("routes in router.go missing from manifest (%d):\n  %s",
			len(missingInManifest), strings.Join(missingInManifest, "\n  "))
	}
	if len(extraInManifest) > 0 {
		t.Errorf("routes in manifest not in router.go (%d):\n  %s",
			len(extraInManifest), strings.Join(extraInManifest, "\n  "))
	}
}
