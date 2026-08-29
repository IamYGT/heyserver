package mail

// stalwart.go — Stalwart-specific account & domain management methods.
//
// The Service struct and HTTP helpers live in mail.go.
// This file adds the remaining CRUD operations exposed by the
// Stalwart REST Management API (/api/principal).

import "fmt"

// patchOp models one element of the PATCH /api/principal/{id} array body.
// Stalwart expects a JSON array of these objects.
type patchOp struct {
	Action string      `json:"action"` // "set" | "addItem" | "removeItem"
	Field  string      `json:"field"`
	Value  interface{} `json:"value"`
}

// --- Account helpers -------------------------------------------------------

// UpdateQuota updates the storage quota (in bytes) for an existing account.
// Passing 0 removes any quota restriction.
func (s *Service) UpdateQuota(email string, quota int64) error {
	if email == "" {
		return fmt.Errorf("email is required")
	}
	ops := []patchOp{{Action: "set", Field: "quota", Value: quota}}
	if err := s.patch("/api/principal/"+email, ops, nil); err != nil {
		return fmt.Errorf("update quota for %s: %w", email, err)
	}
	return nil
}

// --- Domain helpers --------------------------------------------------------

// CreateDomain registers a new mail domain as a "domain" principal.
// Stalwart does not require a password for domain principals.
func (s *Service) CreateDomain(domain string) error {
	if domain == "" {
		return fmt.Errorf("domain name is required")
	}
	p := stalwartPrincipal{
		Name: domain,
		Type: "domain",
	}
	if err := s.post("/api/principal", p, nil); err != nil {
		return fmt.Errorf("create domain %s: %w", domain, err)
	}
	return nil
}

// DeleteDomain removes a domain principal from Stalwart.
func (s *Service) DeleteDomain(domain string) error {
	if domain == "" {
		return fmt.Errorf("domain name is required")
	}
	if err := s.delete("/api/principal/" + domain); err != nil {
		return fmt.Errorf("delete domain %s: %w", domain, err)
	}
	return nil
}
