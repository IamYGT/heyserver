package mail

import (
	"strings"
	"testing"
)

func TestSigID(t *testing.T) {
	t.Parallel()

	got := sigID("example.org", "mail-20260407")
	want := "example.org-mail-20260407"
	if got != want {
		t.Fatalf("sigID() = %q, want %q", got, want)
	}
}

func TestSettingsKey(t *testing.T) {
	t.Parallel()

	got := settingsKey("example.org-mail-20260407", "algorithm")
	want := "signature.example.org-mail-20260407.algorithm"
	if got != want {
		t.Fatalf("settingsKey() = %q, want %q", got, want)
	}
}

func TestNormaliseAlgo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want DKIMAlgorithm
	}{
		{"rsa", AlgoRSA},
		{"RSA-SHA256", AlgoRSA},
		{"rsasha256", AlgoRSA},
		{"ed25519", AlgoEd25519},
		{"Ed25519-SHA256", AlgoEd25519},
		{"  RSA  ", AlgoRSA},
		{"custom-algo", DKIMAlgorithm("custom-algo")},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			if got := normaliseAlgo(tc.in); got != tc.want {
				t.Fatalf("normaliseAlgo(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseDKIMSettings(t *testing.T) {
	t.Parallel()

	// Signature IDs must not contain dots — parser splits on first "." after "signature.".
	items := []stalwartSettingItem{
		{Key: "signature.example-org-mail-2026.domain", Value: "example.org"},
		{Key: "signature.example-org-mail-2026.selector", Value: "mail-2026"},
		{Key: "signature.example-org-mail-2026.algorithm", Value: "Ed25519"},
		{Key: "signature.other-org-mail.domain", Value: "other.org"},
		{Key: "signature.other-org-mail.selector", Value: "mail"},
		{Key: "signature.other-org-mail.algorithm", Value: "rsa"},
		{Key: "unrelated.setting", Value: "ignored"},
	}

	all := parseDKIMSettings(items, "")
	if len(all) != 2 {
		t.Fatalf("parseDKIMSettings(all) len = %d, want 2", len(all))
	}

	filtered := parseDKIMSettings(items, "example.org")
	if len(filtered) != 1 {
		t.Fatalf("parseDKIMSettings(filtered) len = %d, want 1", len(filtered))
	}
	if filtered[0].Domain != "example.org" || filtered[0].Selector != "mail-2026" {
		t.Fatalf("unexpected filtered entry: %+v", filtered[0])
	}
	if filtered[0].Algorithm != AlgoEd25519 {
		t.Fatalf("algorithm = %q, want %q", filtered[0].Algorithm, AlgoEd25519)
	}
}

func TestExtractFileMacroPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "standard_macro",
			in:   "%{file:/etc/stalwart/dkim/key.pem}%",
			want: "/etc/stalwart/dkim/key.pem",
		},
		{
			name: "without_closing_percent",
			in:   "%{file:/tmp/key.pem}",
			want: "/tmp/key.pem",
		},
		{
			name: "no_macro",
			in:   "plain-pem-content",
			want: "",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := extractFileMacroPath(tc.in); got != tc.want {
				t.Fatalf("extractFileMacroPath() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBuildDNSTXTRecord(t *testing.T) {
	t.Parallel()

	rec := buildDNSTXTRecord("example.org", "mail", AlgoRSA, "BASE64KEY")
	if rec.DNSName != "mail._domainkey.example.org" {
		t.Fatalf("DNSName = %q", rec.DNSName)
	}
	if !strings.Contains(rec.TXTRecord, "v=DKIM1; k=rsa; p=BASE64KEY") {
		t.Fatalf("TXTRecord = %q", rec.TXTRecord)
	}

	ed := buildDNSTXTRecord("example.org", "mail", AlgoEd25519, "EDKEY")
	if !strings.Contains(ed.TXTRecord, "k=ed25519") {
		t.Fatalf("Ed25519 TXTRecord = %q", ed.TXTRecord)
	}
}

func TestGenerateDKIMKeyLocal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		algo    DKIMAlgorithm
		wantErr bool
	}{
		{"rsa", AlgoRSA, false},
		{"ed25519", AlgoEd25519, false},
		{"default_empty", "", false},
		{"unsupported", DKIMAlgorithm("ecdsa"), true},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			priv, pub, err := GenerateDKIMKeyLocal(tc.algo)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("GenerateDKIMKeyLocal: %v", err)
			}
			if !strings.Contains(priv, "PRIVATE KEY") {
				t.Fatalf("private PEM missing header: %q", priv)
			}
			if pub == "" {
				t.Fatal("public key is empty")
			}

			algo := tc.algo
			if algo == "" {
				algo = AlgoRSA
			}
			roundTrip, err := extractPublicKey(priv, algo)
			if err != nil {
				t.Fatalf("extractPublicKey: %v", err)
			}
			if roundTrip != pub {
				t.Fatalf("public key mismatch: got %q, want %q", roundTrip, pub)
			}
		})
	}
}

func TestExtractPublicKeyErrors(t *testing.T) {
	t.Parallel()

	_, err := extractPublicKey("not-pem", AlgoRSA)
	if err == nil {
		t.Fatal("expected error for invalid PEM")
	}

	_, err = extractPublicKey("%{file:/tmp/key.pem}%", AlgoRSA)
	if err == nil || !strings.Contains(err.Error(), "file reference") {
		t.Fatalf("expected file reference error, got %v", err)
	}
}
