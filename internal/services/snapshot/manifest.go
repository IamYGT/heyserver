package snapshot

import (
	"os"
	"path/filepath"
	"strings"
)

// DefaultManifest returns the standard Heyserver server snapshot paths using the
// installation's configured data and virtual-host roots.
func DefaultManifest(dataDir, vhostsRoot string) []ManifestEntry {
	return []ManifestEntry{
		{ID: "vhosts", Path: vhostsRoot, Label: "Web siteleri (vhosts)", Required: true, Enabled: true},
		{ID: "nginx", Path: "/etc/nginx", Label: "Nginx yapılandırması", Enabled: true},
		{ID: "letsencrypt", Path: "/etc/letsencrypt", Label: "SSL (Let's Encrypt)", Enabled: true},
		{ID: "postgresql-cfg", Path: "/etc/postgresql", Label: "PostgreSQL config", Enabled: true},
		{ID: "mysql-cfg", Path: "/etc/mysql", Label: "MariaDB/MySQL config", Enabled: true},
		{ID: "php", Path: "/etc/php", Label: "PHP-FPM ayarları", Enabled: true},
		{ID: "hserver-data", Path: dataDir, Label: "Heyserver panel verisi", Enabled: true,
			Exclude: []string{"backups", "snapshot-staging"}},
		{ID: "cron-d", Path: "/etc/cron.d", Label: "Cron görevleri (/etc/cron.d)", Enabled: true},
		{ID: "systemd", Path: "/etc/systemd/system", Label: "Systemd unit dosyaları", Enabled: true},
		{ID: "root-crontab", Path: "", Label: "Root crontab (staging export)", Enabled: true},
	}
}

func (s *Service) manifestForUI(settings Settings) []ManifestEntry {
	base := DefaultManifest(s.dataDir, s.vhostsRoot)
	var allowed map[string]bool
	if settings.EnabledPaths == nil {
		allowed = defaultEnabledIDs(base)
	} else {
		allowed = make(map[string]bool, len(settings.EnabledPaths))
		for _, id := range settings.EnabledPaths {
			allowed[id] = true
		}
	}
	out := make([]ManifestEntry, 0, len(base))
	for _, e := range base {
		e.Enabled = allowed[e.ID]
		if e.Required {
			e.Enabled = true
		}
		e.Available = pathAvailable(e)
		out = append(out, e)
	}
	return out
}

func (s *Service) resolvedManifest(settings Settings) []ManifestEntry {
	var out []ManifestEntry
	for _, e := range s.manifestForUI(settings) {
		if !e.Enabled {
			continue
		}
		if e.Available {
			out = append(out, e)
		}
	}
	return out
}

func defaultEnabledIDs(entries []ManifestEntry) map[string]bool {
	m := make(map[string]bool)
	for _, e := range entries {
		m[e.ID] = true
	}
	return m
}

func pathAvailable(e ManifestEntry) bool {
	if e.Path == "" || e.ID == "root-crontab" {
		return true
	}
	_, err := os.Stat(e.Path)
	return err == nil
}

func collectExcludes(entries []ManifestEntry) []string {
	var out []string
	for _, e := range entries {
		if !e.Enabled || e.Path == "" || len(e.Exclude) == 0 {
			continue
		}
		base := strings.TrimRight(e.Path, "/")
		for _, ex := range e.Exclude {
			pattern := filepath.Join(base, ex)
			out = append(out, pattern)
			out = append(out, pattern+"/**")
		}
	}
	return out
}

func manifestBackupPaths(entries []ManifestEntry, stagingDir string) []string {
	paths := []string{stagingDir}
	for _, e := range entries {
		if e.Path != "" && e.Enabled {
			paths = append(paths, e.Path)
		}
	}
	return paths
}
