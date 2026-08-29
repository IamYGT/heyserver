package mail

import (
	"strings"
	"testing"
)

func TestParseDMARCPolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"quarantine", "v=DMARC1; p=quarantine; rua=mailto:dmarc@example.org", "quarantine"},
		{"reject", "v=DMARC1; p=reject", "reject"},
		{"none", "v=DMARC1; p=none; sp=reject", "none"},
		{"uppercase", "v=DMARC1; P=REJECT", "reject"},
		{"missing", "v=DMARC1; rua=mailto:a@b.com", ""},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := parseDMARCPolicy(tc.in); got != tc.want {
				t.Fatalf("parseDMARCPolicy() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestComputeScore(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		report     DNSHealthReport
		wantScore  int
		wantSugLen int
	}{
		{
			name: "perfect_score",
			report: DNSHealthReport{
				Domain: "example.org",
				MX:     DNSCheckResult{Found: true, Valid: true},
				SPF:    DNSCheckResult{Found: true, Valid: true},
				DKIM:   DNSCheckResult{Found: true, Valid: true},
				DMARC:  DNSCheckResult{Found: true, Valid: true, Policy: "reject"},
				ReverseDNS: DNSCheckResult{Found: true, Valid: true},
			},
			wantScore:  100,
			wantSugLen: 0,
		},
		{
			name: "nothing_configured",
			report: DNSHealthReport{
				Domain: "example.org",
				MX:     DNSCheckResult{Suggestion: "add mx"},
				SPF:    DNSCheckResult{Suggestion: "add spf"},
				DKIM:   DNSCheckResult{Suggestion: "add dkim"},
				DMARC:  DNSCheckResult{Suggestion: "add dmarc"},
				ReverseDNS: DNSCheckResult{Suggestion: "add ptr"},
			},
			wantScore:  0,
			wantSugLen: 5,
		},
		{
			name: "dmarc_none_partial_credit",
			report: DNSHealthReport{
				Domain: "example.org",
				MX:     DNSCheckResult{Found: true, Valid: true},
				SPF:    DNSCheckResult{Found: true, Valid: true},
				DKIM:   DNSCheckResult{Found: true, Valid: true},
				DMARC:  DNSCheckResult{Found: true, Valid: true, Policy: "none", Suggestion: "upgrade policy"},
				ReverseDNS: DNSCheckResult{Found: true, Valid: true},
			},
			wantScore:  90,
			wantSugLen: 1,
		},
		{
			name: "rdns_forward_mismatch_partial",
			report: DNSHealthReport{
				Domain: "example.org",
				MX:     DNSCheckResult{Found: true, Valid: true},
				SPF:    DNSCheckResult{Found: true, Valid: true},
				DKIM:   DNSCheckResult{Found: true, Valid: true},
				DMARC:  DNSCheckResult{Found: true, Valid: true, Policy: "quarantine"},
				ReverseDNS: DNSCheckResult{Found: true, Valid: false, Suggestion: "fix forward confirm"},
			},
			wantScore:  93,
			wantSugLen: 1,
		},
		{
			name: "spf_found_but_invalid",
			report: DNSHealthReport{
				Domain: "example.org",
				MX:     DNSCheckResult{Found: true, Valid: true},
				SPF:    DNSCheckResult{Found: true, Valid: false, Suggestion: "fix spf all"},
				DKIM:   DNSCheckResult{Found: true, Valid: true},
				DMARC:  DNSCheckResult{Found: true, Valid: true, Policy: "reject"},
				ReverseDNS: DNSCheckResult{Found: true, Valid: true},
			},
			wantScore:  90,
			wantSugLen: 1,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			score, suggestions := computeScore(&tc.report)
			if score != tc.wantScore {
				t.Fatalf("computeScore() score = %d, want %d", score, tc.wantScore)
			}
			if len(suggestions) != tc.wantSugLen {
				t.Fatalf("computeScore() suggestions len = %d, want %d (%v)", len(suggestions), tc.wantSugLen, suggestions)
			}
		})
	}
}

func TestCheckReverseDNSEmptyIP(t *testing.T) {
	t.Parallel()

	result := CheckReverseDNS("")
	if result.Found {
		t.Fatal("expected Found=false for empty IP")
	}
	if result.Error != "no IP address provided" {
		t.Fatalf("Error = %q", result.Error)
	}
	if result.Suggestion == "" {
		t.Fatal("expected suggestion for empty IP")
	}
}

func TestCheckAllWithoutServerIP(t *testing.T) {
	t.Parallel()

	report := CheckAll("invalid-dns-test-domain-12345.invalid")
	if report.Domain != "invalid-dns-test-domain-12345.invalid" {
		t.Fatalf("Domain = %q", report.Domain)
	}
	if report.ReverseDNS.Error != "no IP address provided" {
		t.Fatalf("ReverseDNS error = %q", report.ReverseDNS.Error)
	}
	if report.Score < 0 || report.Score > 100 {
		t.Fatalf("score out of range: %d", report.Score)
	}
}

func TestComputeScoreSuggestionPrefixes(t *testing.T) {
	t.Parallel()

	report := DNSHealthReport{
		Domain: "example.org",
		MX:     DNSCheckResult{Suggestion: "fix mx"},
	}
	_, suggestions := computeScore(&report)
	if len(suggestions) == 0 || !strings.HasPrefix(suggestions[0], "MX: ") {
		t.Fatalf("expected MX-prefixed suggestion, got %v", suggestions)
	}
}
