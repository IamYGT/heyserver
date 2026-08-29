package settings

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestExportPortableUsesPositiveAllowlistAndSkipsInvalidValues(t *testing.T) {
	svc := newTestService(t)
	svc.now = func() time.Time { return time.Date(2026, 8, 26, 20, 0, 0, 0, time.UTC) }
	if err := svc.SetMany(map[string]string{
		"hostnameDisplay":        "Primary server",
		"notifyOnError":          "true",
		"webmail_url":            "javascript:alert(1)",
		"gdrive_settings":        `{"folder":"backups","refresh_token":"must-not-export"}`,
		"provider_client_secret": "must-not-export",
		"onboarding_completed":   "true",
	}); err != nil {
		t.Fatal(err)
	}

	bundle, err := svc.ExportPortable()
	if err != nil {
		t.Fatalf("ExportPortable() error = %v", err)
	}
	if bundle.SchemaVersion != PortableSchemaVersion || bundle.SourceVersion != "test-panel-1.0.0" || bundle.ExportedAt.Format(time.RFC3339) != "2026-08-26T20:00:00Z" {
		t.Fatalf("bundle metadata = %#v", bundle)
	}
	want := map[string]string{"hostnameDisplay": "Primary server", "notifyOnError": "true"}
	if !reflect.DeepEqual(bundle.Settings, want) {
		t.Fatalf("portable settings = %#v, want %#v", bundle.Settings, want)
	}
	serialized := strings.Join(bundle.Warnings, " ")
	if !strings.Contains(serialized, "webmail_url") || strings.Contains(serialized, "must-not-export") {
		t.Fatalf("warnings = %#v", bundle.Warnings)
	}
}

func TestPreviewAndConfirmedImportAreSeparate(t *testing.T) {
	svc := newTestService(t)
	if err := svc.SetMany(map[string]string{
		"hostnameDisplay": "Old server",
		"notifyOnLogin":   "false",
	}); err != nil {
		t.Fatal(err)
	}
	bundle := validPortableTestBundle(map[string]string{
		"hostnameDisplay": "New server",
		"notifyOnLogin":   "false",
		"timezone":        "Europe/Istanbul",
	})

	preview, err := svc.PreviewPortable(bundle)
	if err != nil {
		t.Fatalf("PreviewPortable() error = %v", err)
	}
	if preview.ImportedKeys != 3 || preview.ChangedKeys != 2 || preview.UnchangedKeys != 1 {
		t.Fatalf("preview = %#v", preview)
	}
	if got, _ := svc.Get("hostnameDisplay", ""); got != "Old server" {
		t.Fatalf("preview mutated hostnameDisplay to %q", got)
	}

	if _, err := svc.ImportPortable(bundle, false); !errors.Is(err, ErrPortableConfirmation) {
		t.Fatalf("unconfirmed import error = %v", err)
	}
	if _, err := svc.ImportPortable(bundle, true); err != nil {
		t.Fatalf("confirmed import error = %v", err)
	}
	if got, _ := svc.Get("hostnameDisplay", ""); got != "New server" {
		t.Fatalf("hostnameDisplay = %q", got)
	}
	if got, _ := svc.Get("timezone", ""); got != "Europe/Istanbul" {
		t.Fatalf("timezone = %q", got)
	}
}

func TestPortableBundleRejectsUnknownInvalidAndUnsupportedInput(t *testing.T) {
	svc := newTestService(t)
	tests := []struct {
		name   string
		bundle PortableBundle
		want   error
	}{
		{name: "unsupported schema", bundle: func() PortableBundle {
			value := validPortableTestBundle(map[string]string{"notifyOnError": "true"})
			value.SchemaVersion = 2
			return value
		}(), want: ErrPortableSchema},
		{name: "unknown setting", bundle: validPortableTestBundle(map[string]string{"api_token": "secret"}), want: ErrPortableSetting},
		{name: "invalid URL", bundle: validPortableTestBundle(map[string]string{"webmail_url": "file:///etc/passwd"}), want: ErrPortableSetting},
		{name: "invalid boolean", bundle: validPortableTestBundle(map[string]string{"notifyOnError": "yes"}), want: ErrPortableSetting},
		{name: "empty", bundle: validPortableTestBundle(map[string]string{}), want: ErrPortableEmpty},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := svc.PreviewPortable(test.bundle); !errors.Is(err, test.want) {
				t.Fatalf("PreviewPortable() error = %v, want %v", err, test.want)
			}
		})
	}
}

func validPortableTestBundle(values map[string]string) PortableBundle {
	return PortableBundle{
		SchemaVersion: PortableSchemaVersion,
		ExportedAt:    time.Date(2026, 8, 26, 20, 0, 0, 0, time.UTC),
		SourceVersion: "v1.0.0",
		Settings:      values,
	}
}
