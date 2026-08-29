package mail

import "fmt"

// MailGroup represents a Stalwart group principal.
type MailGroup struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Emails      []string `json:"emails,omitempty"`
	Members     []string `json:"members,omitempty"`
}

// ListAliases returns all list-type principals, optionally filtered by domain.
// Stalwart type=list covers both simple aliases and mailing lists.
func (s *Service) ListAliases(domain string) ([]MailAlias, error) {
	var list stalwartListResponse
	if err := s.get("/api/principal?type=list&limit=1000", &list); err != nil {
		return nil, fmt.Errorf("list aliases: %w", err)
	}
	aliases := make([]MailAlias, 0, len(list.Items))
	for _, name := range list.Items {
		var p stalwartPrincipal
		if err := s.get("/api/principal/"+name, &p); err != nil {
			continue
		}
		addr := primaryEmail(p.Emails)
		if addr == "" {
			addr = name
		}
		// Apply optional domain filter.
		if domain != "" && !hasDomain(addr, domain) {
			continue
		}
		aliases = append(aliases, MailAlias{
			ID:           name,
			Address:      addr,
			Destinations: p.Members,
			Description:  p.Description,
		})
	}
	return aliases, nil
}

// CreateAlias creates a new list principal (alias / mailing list).
func (s *Service) CreateAlias(alias, destination string) error {
	if alias == "" || destination == "" {
		return fmt.Errorf("alias and destination are required")
	}
	p := stalwartPrincipal{
		Name:    alias,
		Type:    "list",
		Emails:  []string{alias},
		Members: []string{destination},
	}
	if err := s.post("/api/principal", p, nil); err != nil {
		return fmt.Errorf("create alias %s: %w", alias, err)
	}
	return nil
}

// DeleteAlias removes a list principal by its name / address.
func (s *Service) DeleteAlias(alias string) error {
	if alias == "" {
		return fmt.Errorf("alias name is required")
	}
	if err := s.delete("/api/principal/" + alias); err != nil {
		return fmt.Errorf("delete alias %s: %w", alias, err)
	}
	return nil
}

// ListGroups returns all group-type principals.
func (s *Service) ListGroups() ([]MailGroup, error) {
	var list stalwartListResponse
	if err := s.get("/api/principal?type=group&limit=1000", &list); err != nil {
		return nil, fmt.Errorf("list groups: %w", err)
	}
	groups := make([]MailGroup, 0, len(list.Items))
	for _, name := range list.Items {
		var p stalwartPrincipal
		if err := s.get("/api/principal/"+name, &p); err != nil {
			continue
		}
		groups = append(groups, MailGroup{
			Name:        name,
			Description: p.Description,
			Emails:      p.Emails,
			Members:     p.Members,
		})
	}
	return groups, nil
}

// CreateGroup creates a new group principal with the given members.
func (s *Service) CreateGroup(name string, members []string) error {
	if name == "" {
		return fmt.Errorf("group name is required")
	}
	p := stalwartPrincipal{
		Name:    name,
		Type:    "group",
		Members: members,
	}
	if err := s.post("/api/principal", p, nil); err != nil {
		return fmt.Errorf("create group %s: %w", name, err)
	}
	return nil
}

// UpdateGroupMembers replaces the member list of an existing group principal.
// Stalwart PATCH accepts an array of JSON Patch-style operations:
//
//	[{"action":"set","field":"members","value":["user1","user2"]}]
func (s *Service) UpdateGroupMembers(name string, members []string) error {
	if name == "" {
		return fmt.Errorf("group name is required")
	}
	ops := []map[string]interface{}{
		{
			"action": "set",
			"field":  "members",
			"value":  members,
		},
	}
	if err := s.patch("/api/principal/"+name, ops, nil); err != nil {
		return fmt.Errorf("update group members %s: %w", name, err)
	}
	return nil
}

// hasDomain reports whether addr belongs to the given domain.
func hasDomain(addr, domain string) bool {
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == '@' {
			return addr[i+1:] == domain
		}
	}
	return false
}
