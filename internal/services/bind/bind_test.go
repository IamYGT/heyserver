package bind

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestStatusFromProbe(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		probe     readinessProbe
		wantState ReadinessState
		wantReady bool
		wantAvail bool
	}{
		{
			name:      "named missing",
			probe:     readinessProbe{serviceState: "unknown"},
			wantState: StateNotInstalled,
		},
		{
			name: "version unavailable",
			probe: readinessProbe{
				installed: true, versionError: errors.New("permission denied"), serviceState: "unknown",
			},
			wantState: StateUnavailable,
		},
		{
			name: "configuration incomplete",
			probe: readinessProbe{
				installed: true, version: "BIND 9.18", serviceState: "active", serviceObservable: true,
				active: true, checkToolsAvailable: true, reloadAvailable: true,
			},
			wantState: StateNotConfigured,
			wantAvail: true,
		},
		{
			name: "service stopped",
			probe: readinessProbe{
				installed: true, version: "BIND 9.18", serviceState: "inactive", serviceObservable: true,
				configAvailable: true, checkToolsAvailable: true, reloadAvailable: true,
			},
			wantState: StateStopped,
			wantAvail: true,
		},
		{
			name: "healthy",
			probe: readinessProbe{
				installed: true, version: "BIND 9.18", active: true, serviceState: "active", serviceObservable: true,
				configAvailable: true, checkToolsAvailable: true, reloadAvailable: true,
			},
			wantState: StateHealthy,
			wantReady: true,
			wantAvail: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := statusFromProbe(tc.probe)
			if got.State != tc.wantState || got.ZoneManagementReady != tc.wantReady || got.Available != tc.wantAvail {
				t.Fatalf("statusFromProbe() = state %q ready=%v available=%v, want %q ready=%v available=%v",
					got.State, got.ZoneManagementReady, got.Available, tc.wantState, tc.wantReady, tc.wantAvail)
			}
		})
	}
}

func TestRecoveryStatusBlocksZoneManagement(t *testing.T) {
	service := New()
	service.recoveryPending = true
	service.recoveryErr = errors.New("reload failed")

	status := service.withRecoveryStatus(ServiceStatus{
		Available:           true,
		Installed:           true,
		State:               StateHealthy,
		ZoneManagementReady: true,
	})
	if status.State != StateUnavailable || status.ZoneManagementReady || !status.RecoveryPending {
		t.Fatalf("recovery status=%#v", status)
	}
	if !strings.Contains(status.Error, "needs recovery") {
		t.Fatalf("recovery error=%q", status.Error)
	}
}

func TestMissingBindRequirements(t *testing.T) {
	t.Parallel()

	got := missingBindRequirements(readinessProbe{installed: true})
	for _, want := range []string{namedConf, namedCheckBin, namedCheckZone, "rndc"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missingBindRequirements() = %q, missing %q", got, want)
		}
	}
}

func TestValidateDomain(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		domain  string
		wantErr bool
	}{
		{"apex", "example.com", false},
		{"subdomain", "api.example.com", false},
		{"wildcard", "*.example.com", false},
		{"underscore", "my_site.example.com", false},
		{"empty", "", true},
		{"double dot", "evil..com", true},
		{"slash", "evil/com", true},
		{"space", "evil com", true},
		{"leading hyphen label", "-bad.example.com", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validateDomain(tc.domain)
			if tc.wantErr && err == nil {
				t.Fatalf("validateDomain(%q) expected error", tc.domain)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("validateDomain(%q) unexpected error: %v", tc.domain, err)
			}
		})
	}
}

func TestParseRecordFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		fields []string
		want   *Record
	}{
		{
			name:   "A with TTL",
			fields: []string{"www", "3600", "IN", "A", "192.168.1.1"},
			want:   &Record{Name: "www", TTL: "3600", Class: "IN", Type: "A", Value: "192.168.1.1"},
		},
		{
			name:   "A without TTL",
			fields: []string{"@", "IN", "A", "10.0.0.1"},
			want:   &Record{Name: "@", TTL: "", Class: "IN", Type: "A", Value: "10.0.0.1"},
		},
		{
			name:   "MX with priority",
			fields: []string{"@", "3600", "IN", "MX", "10", "mail.example.com."},
			want:   &Record{Name: "@", TTL: "3600", Class: "IN", Type: "MX", Priority: 10, Value: "mail.example.com."},
		},
		{
			name:   "TXT record",
			fields: []string{"@", "IN", "TXT", `"v=spf1 mx ~all"`},
			want:   &Record{Name: "@", Class: "IN", Type: "TXT", Value: `"v=spf1 mx ~all"`},
		},
		{
			name:   "too few fields",
			fields: []string{"@", "IN"},
			want:   nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := parseRecordFields(tc.fields)
			if tc.want == nil {
				if got != nil {
					t.Fatalf("parseRecordFields(%v) = %+v, want nil", tc.fields, got)
				}
				return
			}
			if got == nil {
				t.Fatalf("parseRecordFields(%v) = nil, want %+v", tc.fields, tc.want)
			}
			if got.Name != tc.want.Name || got.TTL != tc.want.TTL || got.Class != tc.want.Class ||
				got.Type != tc.want.Type || got.Value != tc.want.Value || got.Priority != tc.want.Priority {
				t.Fatalf("parseRecordFields(%v) = %+v, want %+v", tc.fields, got, tc.want)
			}
		})
	}
}

func TestMatchRecord(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		fields []string
		rname  string
		rtype  string
		value  string
		want   bool
	}{
		{
			name:   "A exact match",
			fields: []string{"www", "3600", "IN", "A", "1.2.3.4"},
			rname:  "www", rtype: "A", value: "1.2.3.4", want: true,
		},
		{
			name:   "name mismatch",
			fields: []string{"www", "3600", "IN", "A", "1.2.3.4"},
			rname:  "mail", rtype: "A", value: "1.2.3.4", want: false,
		},
		{
			name:   "MX full value",
			fields: []string{"@", "3600", "IN", "MX", "10", "mail.example.com."},
			rname:  "@", rtype: "MX", value: "10 mail.example.com.", want: true,
		},
		{
			name:   "MX value without priority",
			fields: []string{"@", "3600", "IN", "MX", "10", "mail.example.com."},
			rname:  "@", rtype: "MX", value: "mail.example.com.", want: true,
		},
		{
			name:   "type case insensitive",
			fields: []string{"@", "IN", "TXT", "hello"},
			rname:  "@", rtype: "txt", value: "hello", want: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := matchRecord(tc.fields, tc.rname, tc.rtype, tc.value); got != tc.want {
				t.Fatalf("matchRecord(%v, %q, %q, %q) = %v, want %v",
					tc.fields, tc.rname, tc.rtype, tc.value, got, tc.want)
			}
		})
	}
}

func TestBumpSerialValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		current uint32
		wantMin uint32
		wantMax uint32
	}{
		{"date-based increments", 2024010101, 2024010102, 2024010102},
		{"unix below now bumps to now", 1, 1, 1 << 31},
		{"unix above now increments", 4000000000, 4000000001, 4000000001},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := bumpSerialValue(tc.current)
			if tc.wantMin == tc.wantMax {
				if got != tc.wantMin {
					t.Fatalf("bumpSerialValue(%d) = %d, want %d", tc.current, got, tc.wantMin)
				}
				return
			}
			if got < tc.wantMin || got > tc.wantMax {
				t.Fatalf("bumpSerialValue(%d) = %d, want in [%d, %d]", tc.current, got, tc.wantMin, tc.wantMax)
			}
		})
	}
}

func TestBumpSerialContent(t *testing.T) {
	t.Parallel()

	content := `@ IN SOA ns1.example.com. hostmaster.example.com. (
	2026082601 3600 900 604800 300 )
www IN A 192.0.2.10
`
	got, err := bumpSerialContent(content)
	if err != nil {
		t.Fatalf("bumpSerialContent() error: %v", err)
	}
	if !strings.Contains(got, "2026082602") || strings.Contains(got, "2026082601") {
		t.Fatalf("bumpSerialContent() did not replace the serial:\n%s", got)
	}
	if !strings.Contains(got, "www IN A 192.0.2.10") {
		t.Fatalf("bumpSerialContent() changed unrelated records:\n%s", got)
	}
}

func TestBumpSerialContentRequiresSOA(t *testing.T) {
	t.Parallel()

	if _, err := bumpSerialContent("www IN A 192.0.2.10\n"); err == nil {
		t.Fatal("bumpSerialContent() without SOA expected error")
	}
}

func TestBuildZoneFile(t *testing.T) {
	t.Parallel()

	content := buildZoneFile("example.com", "203.0.113.10", 2024010101)

	checks := []string{
		"$TTL 3600",
		"SOA",
		"ns1.example.com.",
		"hostmaster.example.com.",
		"2024010101",
		"@	IN	A	203.0.113.10",
		"www	IN	A	203.0.113.10",
	}
	for _, want := range checks {
		if !strings.Contains(content, want) {
			t.Fatalf("buildZoneFile missing %q in:\n%s", want, content)
		}
	}
}

func TestParseNamedConfContent(t *testing.T) {
	t.Parallel()

	content := `
zone "example.com" {
	type master;
	file "/etc/bind/zones/db.example.com";
};

zone "test.org" {
	type master;
	file "/etc/bind/zones/db.test.org";
};
`
	zones := parseNamedConfContent(content)
	if len(zones) != 2 {
		t.Fatalf("parseNamedConfContent len = %d, want 2", len(zones))
	}
	if zones[0].Domain != "example.com" || zones[0].File != "/etc/bind/zones/db.example.com" {
		t.Fatalf("zones[0] = %+v", zones[0])
	}
	if zones[1].Domain != "test.org" {
		t.Fatalf("zones[1] = %+v", zones[1])
	}
}

func TestParseNamedConfContent_Empty(t *testing.T) {
	t.Parallel()

	if zones := parseNamedConfContent(""); len(zones) != 0 {
		t.Fatalf("expected empty zones, got %d", len(zones))
	}
}

func TestRemoveZoneBlockContent(t *testing.T) {
	t.Parallel()

	content := `// installation-owned zones
zone "example.com" {
	type master;
	file "/etc/bind/zones/db.example.com";
};

zone "kept.example" {
	type master;
	file "/etc/bind/zones/db.kept.example";
};
`
	got, removed := removeZoneBlockContent(content, "example.com")
	if !removed {
		t.Fatal("removeZoneBlockContent() did not report removal")
	}
	if strings.Contains(got, `zone "example.com"`) {
		t.Fatalf("removed zone remains in config:\n%s", got)
	}
	if !strings.Contains(got, `zone "kept.example"`) || !strings.Contains(got, "// installation-owned zones") {
		t.Fatalf("unrelated config was removed:\n%s", got)
	}

	unchanged, removed := removeZoneBlockContent(got, "missing.example")
	if removed || unchanged != got {
		t.Fatalf("missing zone changed config: removed=%v\n%s", removed, unchanged)
	}
}

// parseNamedConfContent mirrors the zone declaration parser used by parseNamedConf.
func parseNamedConfContent(data string) []Zone {
	zoneRe := regexp.MustCompile(`zone\s+"([^"]+)"\s*\{[^}]*file\s+"([^"]+)"`)
	matches := zoneRe.FindAllStringSubmatch(data, -1)
	var zones []Zone
	for _, m := range matches {
		zones = append(zones, Zone{Domain: m[1], File: m[2]})
	}
	return zones
}

func TestReadSerialAndParseSOA(t *testing.T) {
	t.Parallel()

	zoneContent := `$TTL 3600
@ IN SOA ns1.example.com. hostmaster.example.com. (
	2024061501 3600 900 604800 300 )
www IN A 192.168.1.1
`
	zoneFile := filepath.Join(t.TempDir(), "db.example.com")
	if err := os.WriteFile(zoneFile, []byte(zoneContent), 0644); err != nil {
		t.Fatal(err)
	}

	serial, err := readSerial(zoneFile)
	if err != nil {
		t.Fatalf("readSerial: %v", err)
	}
	if serial != 2024061501 {
		t.Fatalf("readSerial = %d, want 2024061501", serial)
	}

	soa, err := parseSOA(zoneFile)
	if err != nil {
		t.Fatalf("parseSOA: %v", err)
	}
	if soa.PrimaryNs != "ns1.example.com." || soa.Hostmaster != "hostmaster.example.com." {
		t.Fatalf("parseSOA ns fields = %+v", soa)
	}
	if soa.Serial != 2024061501 || soa.Refresh != 3600 || soa.Retry != 900 ||
		soa.Expire != 604800 || soa.Minimum != 300 {
		t.Fatalf("parseSOA numbers = %+v", soa)
	}
}

func TestParseZoneFile(t *testing.T) {
	t.Parallel()

	zoneContent := `$TTL 7200
@	IN	SOA	ns1.example.com.	hostmaster.example.com. (
			1	; Serial
			3600	; Refresh
			900	; Retry
			604800	; Expire
			300 )	; Minimum TTL
; comment line
www	IN	A	10.0.0.1
mail	3600	IN	MX	10 mail.example.com.
`
	zoneFile := filepath.Join(t.TempDir(), "db.test.com")
	if err := os.WriteFile(zoneFile, []byte(zoneContent), 0644); err != nil {
		t.Fatal(err)
	}

	records, err := parseZoneFile(zoneFile)
	if err != nil {
		t.Fatalf("parseZoneFile: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("parseZoneFile len = %d, want 2; records=%+v", len(records), records)
	}

	www := records[0]
	if www.Name != "www" || www.Type != "A" || www.Value != "10.0.0.1" || www.TTL != "7200" {
		t.Fatalf("www record = %+v", www)
	}

	mx := records[1]
	if mx.Name != "mail" || mx.Type != "MX" || mx.Priority != 10 || mx.Value != "mail.example.com." {
		t.Fatalf("mx record = %+v", mx)
	}
}

func TestParseZoneFile_MissingFile(t *testing.T) {
	t.Parallel()

	_, err := parseZoneFile(filepath.Join(t.TempDir(), "missing.zone"))
	if err == nil {
		t.Fatal("parseZoneFile on missing file expected error")
	}
}
