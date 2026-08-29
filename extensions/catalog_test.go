package extensions

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestRuntimeCatalogLoadsEmbeddedSchemaV1(t *testing.T) {
	catalog, err := LoadCatalog()
	if err != nil {
		t.Fatalf("LoadCatalog() error = %v", err)
	}
	if catalog.SchemaVersion != 1 {
		t.Fatalf("schema_version = %d, want 1", catalog.SchemaVersion)
	}
	if len(catalog.Entries) < len(expectedIDs) {
		t.Fatalf("entries = %d, want at least %d", len(catalog.Entries), len(expectedIDs))
	}

	wantIDs := map[string]bool{
		"cloudflare.dns":        false,
		"process.pm2":           false,
		"runtime.php_fpm":       false,
		"notification.delivery": false,
	}
	for _, entry := range catalog.Entries {
		if _, ok := wantIDs[entry.ID]; ok {
			wantIDs[entry.ID] = true
		}
	}
	for id, found := range wantIDs {
		if !found {
			t.Errorf("catalog missing known id %q", id)
		}
	}
}

func TestRuntimeCatalogMatchesEmbeddedJSONSemantics(t *testing.T) {
	catalog, err := LoadCatalog()
	if err != nil {
		t.Fatalf("LoadCatalog() error = %v", err)
	}
	encoded, err := json.Marshal(catalog)
	if err != nil {
		t.Fatalf("json.Marshal(catalog): %v", err)
	}
	var got any
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("decode typed catalog: %v", err)
	}
	var want any
	if err := json.Unmarshal(embeddedCatalogJSON, &want); err != nil {
		t.Fatalf("decode embedded catalog: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("typed catalog does not preserve embedded JSON semantics")
	}
}

func TestRuntimeCatalogReturnsDefensiveCopy(t *testing.T) {
	first, err := LoadCatalog()
	if err != nil {
		t.Fatalf("first LoadCatalog() error = %v", err)
	}
	originalID := first.Entries[0].ID
	first.Entries[0].ID = "mutated"
	first.Entries[0].Classes[0] = "mutated"

	second, err := LoadCatalog()
	if err != nil {
		t.Fatalf("second LoadCatalog() error = %v", err)
	}
	if second.Entries[0].ID != originalID {
		t.Fatalf("catalog ID was mutated through returned value: %q", second.Entries[0].ID)
	}
	if second.Entries[0].Classes[0] == "mutated" {
		t.Fatal("catalog classes were mutated through returned value")
	}
}

func TestRuntimeCatalogRejectsMalformedData(t *testing.T) {
	for name, data := range map[string][]byte{
		"invalid syntax": []byte(`{"schema_version":`),
		"unknown field":  []byte(`{"schema_version":1,"unexpected":true}`),
		"duplicate key":  []byte(`{"schema_version":1,"schema_version":1}`),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseCatalog(data); err == nil {
				t.Fatal("ParseCatalog() error = nil, want malformed-data error")
			}
		})
	}
}

func TestParseCatalogAcceptsValidAdditionalEntry(t *testing.T) {
	catalog := catalogWithAdditionalEntry(t)
	data, err := json.Marshal(catalog)
	if err != nil {
		t.Fatalf("json.Marshal(additive catalog): %v", err)
	}

	parsed, err := ParseCatalog(data)
	if err != nil {
		t.Fatalf("ParseCatalog(valid additive catalog) error = %v", err)
	}
	if len(parsed.Entries) != 16 {
		t.Fatalf("entries = %d, want 16", len(parsed.Entries))
	}
	if parsed.Entries[len(parsed.Entries)-1].ID != "community.example" {
		t.Fatalf("additive entry ID = %q, want community.example", parsed.Entries[len(parsed.Entries)-1].ID)
	}
}

func TestParseCatalogRejectsMissingCoreAndInvalidAdditionalEntry(t *testing.T) {
	tests := map[string]func(*Catalog){
		"missing required core entry": func(catalog *Catalog) {
			catalog.Entries = catalog.Entries[1:]
		},
		"duplicate core entry": func(catalog *Catalog) {
			catalog.Entries = append(catalog.Entries, catalog.Entries[0])
		},
		"invalid additive entry": func(catalog *Catalog) {
			catalog.Entries = append(catalog.Entries, catalog.Entries[0])
			last := len(catalog.Entries) - 1
			catalog.Entries[last].ID = "community extension"
			catalog.Entries[last].DisplayName = "Community extension"
			catalog.Entries[last].DocsRowMarker = "optional-integrations:v1:community-extension"
		},
		"invalid additive route": func(catalog *Catalog) {
			appendAdditionalEntry(catalog)
			last := len(catalog.Entries) - 1
			catalog.Entries[last].Status.APIRoutePrefixes = []string{"/status"}
		},
		"missing additive evidence": func(catalog *Catalog) {
			appendAdditionalEntry(catalog)
			last := len(catalog.Entries) - 1
			catalog.Entries[last].Evidence.Web = nil
		},
		"invalid additive state": func(catalog *Catalog) {
			appendAdditionalEntry(catalog)
			last := len(catalog.Entries) - 1
			catalog.Entries[last].Status.CanonicalStates = []string{"healthy", "unavailable", "not_configured"}
		},
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			catalog, err := LoadCatalog()
			if err != nil {
				t.Fatalf("LoadCatalog() error = %v", err)
			}
			mutate(&catalog)
			data, err := json.Marshal(catalog)
			if err != nil {
				t.Fatalf("json.Marshal(mutated catalog): %v", err)
			}
			if _, err := ParseCatalog(data); err == nil {
				t.Fatal("ParseCatalog() error = nil, want catalog validation error")
			}
		})
	}
}

func catalogWithAdditionalEntry(t *testing.T) Catalog {
	t.Helper()
	catalog, err := LoadCatalog()
	if err != nil {
		t.Fatalf("LoadCatalog() error = %v", err)
	}

	appendAdditionalEntry(&catalog)
	return catalog
}

func appendAdditionalEntry(catalog *Catalog) {
	additional := catalog.Entries[0]
	additional.ID = "community.example"
	additional.DisplayName = "Community example"
	additional.Purpose = "A valid additive extension used only by parser tests."
	additional.DocsRowMarker = "optional-integrations:v1:community-example"
	catalog.Entries = append(catalog.Entries, additional)
}
