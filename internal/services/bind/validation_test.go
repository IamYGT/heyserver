package bind

import (
	"strings"
	"testing"
)

func TestValidateAndNormalizeCreateZone(t *testing.T) {
	t.Parallel()

	request, err := ValidateAndNormalizeCreateZone(CreateZoneRequest{Domain: "Example.COM.", IP: "192.0.2.10"})
	if err != nil {
		t.Fatal(err)
	}
	if request.Domain != "example.com" || request.IP != "192.0.2.10" {
		t.Fatalf("normalized request=%#v", request)
	}

	for _, input := range []CreateZoneRequest{
		{Domain: "*.example.com", IP: "192.0.2.10"},
		{Domain: "../example.com", IP: "192.0.2.10"},
		{Domain: "example.com", IP: "2001:db8::1"},
	} {
		if _, err := ValidateAndNormalizeCreateZone(input); err == nil {
			t.Fatalf("ValidateAndNormalizeCreateZone(%#v) accepted invalid input", input)
		}
	}
}

func TestValidateAndNormalizeRecordRequests(t *testing.T) {
	t.Parallel()

	added, err := ValidateAndNormalizeAddRecord(AddRecordRequest{
		Name: " WWW ", Type: "a", Value: "192.0.2.20", AutoReload: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if added.Name != "www" || added.Type != "A" || added.TTL != "3600" || added.Value != "192.0.2.20" {
		t.Fatalf("normalized add=%#v", added)
	}

	updated, err := ValidateAndNormalizeUpdateRecord(UpdateRecordRequest{
		Name: "_sip._tcp", Type: "srv", OldValue: "0 443 old.example.com.", NewValue: "0 443 new.example.com.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Type != "SRV" || updated.Priority != 10 {
		t.Fatalf("normalized update=%#v", updated)
	}

	deleted, err := ValidateAndNormalizeDeleteRecord(DeleteRecordRequest{Name: "@", Type: "mx", Value: "mail.example.com."})
	if err != nil {
		t.Fatal(err)
	}
	if deleted.Type != "MX" || deleted.Value != "mail.example.com." {
		t.Fatalf("normalized delete=%#v", deleted)
	}
}

func TestRecordValidationRejectsUnsafeOrInvalidFields(t *testing.T) {
	t.Parallel()

	tests := []AddRecordRequest{
		{Name: "www", Type: "A", Value: "not-an-ip"},
		{Name: "www", Type: "AAAA", Value: "192.0.2.1"},
		{Name: "www", Type: "A/AAAA", Value: "192.0.2.1"},
		{Name: "www", Type: "A", Value: "192.0.2.1\nmalicious"},
		{Name: "www", Type: "A", Value: "192.0.2.1", TTL: "1h"},
		{Name: "www", Type: "TXT", Value: "ok", Priority: 10},
	}
	for _, input := range tests {
		if _, err := ValidateAndNormalizeAddRecord(input); err == nil {
			t.Fatalf("ValidateAndNormalizeAddRecord(%#v) accepted invalid input", input)
		}
	}
}

func TestValidateAndNormalizeSOA(t *testing.T) {
	t.Parallel()

	request, err := ValidateAndNormalizeSOA(UpdateSOARequest{
		PrimaryNs: "NS1.Example.COM.", Hostmaster: "Hostmaster.Example.COM.",
		Refresh: 3600, Retry: 900, Expire: 604800, Minimum: 300,
	})
	if err != nil {
		t.Fatal(err)
	}
	if request.PrimaryNs != "ns1.example.com." || request.Hostmaster != "hostmaster.example.com." {
		t.Fatalf("normalized SOA=%#v", request)
	}

	request.Expire = maximumDNSTTL + 1
	if _, err := ValidateAndNormalizeSOA(request); err == nil || !strings.Contains(err.Error(), "expire") {
		t.Fatalf("invalid SOA error=%v", err)
	}
}
