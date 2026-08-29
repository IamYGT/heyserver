package snapshot

import "testing"

func TestDefaultManifest_hasVhosts(t *testing.T) {
	var foundVhosts, foundData bool
	for _, e := range DefaultManifest("/var/lib/hserver", "/srv/sites") {
		if e.ID == "vhosts" && e.Path == "/srv/sites" {
			foundVhosts = true
		}
		if e.ID == "hserver-data" && e.Path == "/var/lib/hserver" {
			foundData = true
		}
	}
	if !foundVhosts || !foundData {
		t.Fatalf("configured roots missing: vhosts=%v data=%v", foundVhosts, foundData)
	}
}

func TestManifestForUI_requiredAlwaysEnabled(t *testing.T) {
	s := &Service{dataDir: t.TempDir(), vhostsRoot: t.TempDir(), localDir: t.TempDir()}
	entries := s.manifestForUI(Settings{})
	for _, e := range entries {
		if e.ID == "vhosts" && !e.Enabled {
			t.Fatal("vhosts must always be enabled")
		}
	}
}

func TestCollectExcludes_hserverData(t *testing.T) {
	ex := collectExcludes(DefaultManifest("/var/lib/hserver", "/srv/sites"))
	if len(ex) < 2 {
		t.Fatalf("expected excludes for hserver-data, got %v", ex)
	}
}

func TestManifestBackupPaths_includesStaging(t *testing.T) {
	paths := manifestBackupPaths(DefaultManifest("/var/lib/hserver", "/srv/sites"), "/tmp/staging")
	if len(paths) < 2 {
		t.Fatalf("expected staging + paths, got %v", paths)
	}
	if paths[0] != "/tmp/staging" {
		t.Fatalf("staging first: %v", paths)
	}
}
