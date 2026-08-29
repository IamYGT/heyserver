package security

import (
	"database/sql"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// ListType distinguishes whitelist from blacklist entries.
type ListType string

const (
	ListBlacklist ListType = "blacklist"
	ListWhitelist ListType = "whitelist"
)

// IPEntry is a single record in the ip_lists table.
type IPEntry struct {
	ID        int64      `json:"id"`
	IP        string     `json:"ip"`
	ListType  ListType   `json:"listType"`
	Comment   string     `json:"comment"`
	CreatedAt time.Time  `json:"createdAt"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
}

// IPManager maintains IP whitelist/blacklist in SQLite with an in-memory cache.
type IPManager struct {
	db    *sql.DB
	mu    sync.RWMutex
	cache map[string]ListType
	nets  []cachedNet
}

type cachedNet struct {
	network  *net.IPNet
	listType ListType
}

func NewIPManager(dbPath string) (*IPManager, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("ipmanager open db: %w", err)
	}
	if err := ipMigrate(db); err != nil {
		return nil, fmt.Errorf("ipmanager migrate: %w", err)
	}
	m := &IPManager{db: db, cache: make(map[string]ListType)}
	return m, m.rebuildCache()
}

// Check returns the ListType for rawIP (whitelist wins over blacklist).
func (m *IPManager) Check(rawIP string) ListType {
	ip := net.ParseIP(strings.TrimSpace(rawIP))
	if ip == nil {
		return ""
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if lt, ok := m.cache[ip.String()]; ok {
		return lt
	}
	var found ListType
	for _, cn := range m.nets {
		if cn.network.Contains(ip) {
			if cn.listType == ListWhitelist {
				return ListWhitelist
			}
			found = cn.listType
		}
	}
	return found
}

func (m *IPManager) IsBlocked(ip string) bool { return m.Check(ip) == ListBlacklist }

func (m *IPManager) Add(ip string, lt ListType, comment string, expiresAt *time.Time) (*IPEntry, error) {
	ip = strings.TrimSpace(ip)
	if err := validateIPOrCIDR(ip); err != nil {
		return nil, err
	}
	var expiresStr *string
	if expiresAt != nil {
		s := expiresAt.UTC().Format(time.RFC3339)
		expiresStr = &s
	}
	now := time.Now().UTC()
	var id int64
	err := m.db.QueryRow(
		`INSERT INTO ip_lists (ip, list_type, comment, created_at, expires_at) VALUES (?, ?, ?, ?, ?) RETURNING id`,
		ip, string(lt), comment, now.Format(time.RFC3339), expiresStr,
	).Scan(&id)
	if err != nil {
		return nil, fmt.Errorf("ipmanager add: %w", err)
	}
	if err := m.rebuildCache(); err != nil {
		return nil, err
	}
	return &IPEntry{ID: id, IP: ip, ListType: lt, Comment: comment, CreatedAt: now, ExpiresAt: expiresAt}, nil
}

func (m *IPManager) Remove(ip string) error {
	_, err := m.db.Exec(`DELETE FROM ip_lists WHERE ip = ?`, strings.TrimSpace(ip))
	if err != nil {
		return fmt.Errorf("ipmanager remove: %w", err)
	}
	return m.rebuildCache()
}

// RemoveFromList deletes an entry only when it belongs to the requested list.
// This keeps the blacklist and whitelist HTTP resources from deleting each
// other's entries when a client uses the wrong endpoint.
func (m *IPManager) RemoveFromList(ip string, lt ListType) error {
	result, err := m.db.Exec(
		`DELETE FROM ip_lists WHERE ip = ? AND list_type = ?`,
		strings.TrimSpace(ip), string(lt),
	)
	if err != nil {
		return fmt.Errorf("ipmanager remove from %s: %w", lt, err)
	}
	removed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("ipmanager remove from %s rows affected: %w", lt, err)
	}
	if removed == 0 {
		return fmt.Errorf("IP not found in %s", lt)
	}
	return m.rebuildCache()
}

func (m *IPManager) List(lt ListType) ([]IPEntry, error) {
	rows, err := m.db.Query(
		`SELECT id, ip, list_type, comment, created_at, expires_at FROM ip_lists WHERE list_type = ? ORDER BY created_at DESC`,
		string(lt))
	if err != nil {
		return nil, fmt.Errorf("ipmanager list: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var entries []IPEntry
	for rows.Next() {
		var e IPEntry
		var createdStr, listStr string
		var expiresStr *string
		if err := rows.Scan(&e.ID, &e.IP, &listStr, &e.Comment, &createdStr, &expiresStr); err != nil {
			return nil, err
		}
		e.ListType = ListType(listStr)
		if t, err := time.Parse(time.RFC3339, createdStr); err == nil {
			e.CreatedAt = t
		}
		if expiresStr != nil {
			if t, err := time.Parse(time.RFC3339, *expiresStr); err == nil {
				e.ExpiresAt = &t
			}
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

func (m *IPManager) PruneExpired() error {
	_, err := m.db.Exec(
		`DELETE FROM ip_lists WHERE expires_at IS NOT NULL AND expires_at < ?`,
		time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("ipmanager prune: %w", err)
	}
	return m.rebuildCache()
}

func ipMigrate(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS ip_lists (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		ip         TEXT NOT NULL UNIQUE,
		list_type  TEXT NOT NULL CHECK(list_type IN ('blacklist','whitelist')),
		comment    TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		expires_at TEXT
	)`)
	return err
}

func (m *IPManager) rebuildCache() error {
	rows, err := m.db.Query(`SELECT ip, list_type FROM ip_lists`)
	if err != nil {
		return fmt.Errorf("ipmanager rebuild cache: %w", err)
	}
	defer func() { _ = rows.Close() }()
	newCache := make(map[string]ListType)
	var newNets []cachedNet
	for rows.Next() {
		var rawIP, lt string
		if err := rows.Scan(&rawIP, &lt); err != nil {
			return err
		}
		listType := ListType(lt)
		if strings.Contains(rawIP, "/") {
			_, network, err := net.ParseCIDR(rawIP)
			if err == nil {
				newNets = append(newNets, cachedNet{network: network, listType: listType})
			}
		} else if ip := net.ParseIP(rawIP); ip != nil {
			newCache[ip.String()] = listType
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	m.cache = newCache
	m.nets = newNets
	m.mu.Unlock()
	return nil
}

func validateIPOrCIDR(s string) error {
	if strings.Contains(s, "/") {
		_, _, err := net.ParseCIDR(s)
		if err != nil {
			return fmt.Errorf("invalid CIDR: %s", s)
		}
		return nil
	}
	if net.ParseIP(s) == nil {
		return fmt.Errorf("invalid IP address: %s", s)
	}
	return nil
}
