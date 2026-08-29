package security_test

import (
	"github.com/IamYGT/heyserver/internal/services/security"
	"path/filepath"
	"testing"
	"time"
)

func newTestIPManager(t *testing.T) *security.IPManager {
	t.Helper()
	m, err := security.NewIPManager(filepath.Join(t.TempDir(), "ip.db"))
	if err != nil {
		t.Fatalf("NewIPManager: %v", err)
	}
	return m
}

func TestIPManager_AddAndCheck(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, ip      string
		lt, wantCheck security.ListType
	}{
		{"bl ipv4", "203.0.113.5", security.ListBlacklist, security.ListBlacklist},
		{"wl ipv4", "10.0.0.1", security.ListWhitelist, security.ListWhitelist},
		{"bl ipv6", "2001:db8::ff", security.ListBlacklist, security.ListBlacklist},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := newTestIPManager(t)
			if _, err := m.Add(tc.ip, tc.lt, "", nil); err != nil {
				t.Fatalf("Add: %v", err)
			}
			if got := m.Check(tc.ip); got != tc.wantCheck {
				t.Errorf("Check(%q): got %q want %q", tc.ip, got, tc.wantCheck)
			}
		})
	}
}

func TestIPManager_IsBlocked(t *testing.T) {
	t.Parallel()
	m := newTestIPManager(t)
	_, _ = m.Add("1.2.3.4", security.ListBlacklist, "", nil)
	_, _ = m.Add("5.6.7.8", security.ListWhitelist, "", nil)
	for _, tc := range []struct {
		ip   string
		want bool
	}{
		{"1.2.3.4", true}, {"5.6.7.8", false}, {"9.9.9.9", false},
	} {
		if got := m.IsBlocked(tc.ip); got != tc.want {
			t.Errorf("IsBlocked(%q): got %v want %v", tc.ip, got, tc.want)
		}
	}
}

func TestIPManager_CIDR(t *testing.T) {
	t.Parallel()
	m := newTestIPManager(t)
	_, _ = m.Add("192.168.100.0/24", security.ListBlacklist, "", nil)
	for _, tc := range []struct {
		ip   string
		want bool
	}{
		{"192.168.100.1", true}, {"192.168.100.254", true}, {"192.168.101.1", false},
	} {
		if got := m.IsBlocked(tc.ip); got != tc.want {
			t.Errorf("CIDR check %q: got %v want %v", tc.ip, got, tc.want)
		}
	}
}

func TestIPManager_Remove(t *testing.T) {
	t.Parallel()
	m := newTestIPManager(t)
	_, _ = m.Add("10.0.0.1", security.ListBlacklist, "", nil)
	if !m.IsBlocked("10.0.0.1") {
		t.Fatal("should be blocked")
	}
	_ = m.Remove("10.0.0.1")
	if m.IsBlocked("10.0.0.1") {
		t.Error("should not be blocked after remove")
	}
}

func TestIPManager_RemoveFromListPreservesOtherList(t *testing.T) {
	t.Parallel()
	m := newTestIPManager(t)
	_, _ = m.Add("10.0.0.1", security.ListWhitelist, "office", nil)
	if err := m.RemoveFromList("10.0.0.1", security.ListBlacklist); err == nil {
		t.Fatal("blacklist removal should reject a whitelist entry")
	}
	if got := m.Check("10.0.0.1"); got != security.ListWhitelist {
		t.Fatalf("entry moved or disappeared after wrong-list removal: got %q", got)
	}
	if err := m.RemoveFromList("10.0.0.1", security.ListWhitelist); err != nil {
		t.Fatalf("remove from whitelist: %v", err)
	}
	if got := m.Check("10.0.0.1"); got != "" {
		t.Fatalf("entry remains after whitelist removal: got %q", got)
	}
}

func TestIPManager_List(t *testing.T) {
	t.Parallel()
	m := newTestIPManager(t)
	_, _ = m.Add("1.1.1.1", security.ListBlacklist, "", nil)
	_, _ = m.Add("2.2.2.2", security.ListBlacklist, "", nil)
	_, _ = m.Add("3.3.3.3", security.ListWhitelist, "", nil)
	bl, _ := m.List(security.ListBlacklist)
	if len(bl) != 2 {
		t.Errorf("blacklist: got %d want 2", len(bl))
	}
	wl, _ := m.List(security.ListWhitelist)
	if len(wl) != 1 {
		t.Errorf("whitelist: got %d want 1", len(wl))
	}
}

func TestIPManager_PruneExpired(t *testing.T) {
	t.Parallel()
	m := newTestIPManager(t)
	past := time.Now().Add(-1 * time.Hour)
	future := time.Now().Add(1 * time.Hour)
	_, _ = m.Add("10.0.0.1", security.ListBlacklist, "expired", &past)
	_, _ = m.Add("10.0.0.2", security.ListBlacklist, "active", &future)
	_ = m.PruneExpired()
	list, _ := m.List(security.ListBlacklist)
	if len(list) != 1 {
		t.Errorf("after prune: got %d want 1", len(list))
	}
	if list[0].IP != "10.0.0.2" {
		t.Errorf("wrong entry remaining: %q", list[0].IP)
	}
}

func TestIPManager_InvalidIP(t *testing.T) {
	t.Parallel()
	for _, ip := range []string{"", "not-an-ip", "1.2.3.4; rm", "300.0.0.0/24"} {
		ip := ip
		t.Run(ip, func(t *testing.T) {
			t.Parallel()
			m := newTestIPManager(t)
			_, err := m.Add(ip, security.ListBlacklist, "", nil)
			if err == nil {
				t.Errorf("Add(%q) should fail", ip)
			}
		})
	}
}

func TestIPManager_UnknownIP(t *testing.T) {
	t.Parallel()
	m := newTestIPManager(t)
	if got := m.Check("99.99.99.99"); got != "" {
		t.Errorf("unknown IP: got %q want empty", got)
	}
}

func TestIPManager_Duplicate(t *testing.T) {
	t.Parallel()
	m := newTestIPManager(t)
	_, _ = m.Add("5.5.5.5", security.ListBlacklist, "", nil)
	_, err := m.Add("5.5.5.5", security.ListWhitelist, "", nil)
	if err == nil {
		t.Error("duplicate IP should fail")
	}
}
