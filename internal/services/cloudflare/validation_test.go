package cloudflare

import (
	"strings"
	"testing"
)

func TestValidateAndNormalizeRecordRequest(t *testing.T) {
	t.Parallel()

	request, err := ValidateAndNormalizeRecordRequest(CreateRecordRequest{
		Type: " cname ", Name: " app.example.com ", Content: " origin.example.com ",
		TTL: 30, Proxied: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if request.Type != "CNAME" || request.Name != "app.example.com" || request.Content != "origin.example.com" {
		t.Fatalf("normalized request = %#v", request)
	}
}

func TestValidateAndNormalizeRecordRequestRejectsInvalidContracts(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		request CreateRecordRequest
		want    string
	}{
		{name: "type", request: CreateRecordRequest{Type: "A/AAAA", Name: "@", Content: "192.0.2.1", TTL: 1}, want: "only letters"},
		{name: "name", request: CreateRecordRequest{Type: "A", Content: "192.0.2.1", TTL: 1}, want: "name is required"},
		{name: "content", request: CreateRecordRequest{Type: "A", Name: "@", TTL: 1}, want: "content is required"},
		{name: "control", request: CreateRecordRequest{Type: "TXT", Name: "@", Content: "line\nbreak", TTL: 1}, want: "control characters"},
		{name: "ttl", request: CreateRecordRequest{Type: "A", Name: "@", Content: "192.0.2.1", TTL: 29}, want: "TTL"},
		{name: "priority", request: CreateRecordRequest{Type: "MX", Name: "@", Content: "mail.example.com", TTL: 1, Priority: -1}, want: "priority"},
		{name: "proxy", request: CreateRecordRequest{Type: "MX", Name: "@", Content: "mail.example.com", TTL: 1, Proxied: true}, want: "only for A, AAAA, or CNAME"},
	}
	for _, item := range cases {
		item := item
		t.Run(item.name, func(t *testing.T) {
			t.Parallel()
			_, err := ValidateAndNormalizeRecordRequest(item.request)
			if err == nil || !strings.Contains(err.Error(), item.want) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestNormalizeDomain(t *testing.T) {
	t.Parallel()
	normalized, err := NormalizeDomain("API.Example.COM.")
	if err != nil || normalized != "api.example.com" {
		t.Fatalf("normalized=%q err=%v", normalized, err)
	}
	for _, value := range []string{"localhost", "-api.example.com", "api..example.com", "api_example.com"} {
		if _, err := NormalizeDomain(value); err == nil {
			t.Fatalf("NormalizeDomain(%q) succeeded", value)
		}
	}
}
