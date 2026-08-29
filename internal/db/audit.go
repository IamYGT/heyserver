package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/IamYGT/heyserver/internal/models"
)

// AuditRepository provides read/write access for the audit_logs table.
type AuditRepository struct {
	db *sql.DB
}

// NewAuditRepository returns an AuditRepository backed by the given connection.
func NewAuditRepository(db *sql.DB) *AuditRepository {
	return &AuditRepository{db: db}
}

// Insert writes a new audit log entry.
func (r *AuditRepository) Insert(entry *models.AuditLog) error {
	res, err := r.db.Exec(
		`INSERT INTO audit_logs(user_id, user_name, action, resource, details, ip_address)
		 VALUES(?, ?, ?, ?, ?, ?)`,
		entry.UserID, entry.UserName, entry.Action,
		entry.Resource, entry.Details, entry.IP,
	)
	if err != nil {
		return fmt.Errorf("insert audit log: %w", err)
	}
	id, _ := res.LastInsertId()
	entry.ID = id
	return nil
}

// AuditFilter holds optional filters for the List query.
type AuditFilter struct {
	UserID         int64
	UserName       string
	Action         string
	ActionContains string
	Resource       string
	Server         string
	From           *time.Time
	To             *time.Time
}

// List returns a paginated list of audit log entries matching the given filters.
func (r *AuditRepository) List(filter AuditFilter, limit, offset int) ([]models.AuditLog, int, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	where, args := buildAuditWhere(filter)

	// Count total matching rows for pagination metadata.
	countSQL := `SELECT COUNT(*) FROM audit_logs` + where
	var total int
	if err := r.db.QueryRow(countSQL, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count audit logs: %w", err)
	}

	querySQL := `SELECT id, user_id, user_name, action, resource, details, ip_address, created_at
		FROM audit_logs` + where + ` ORDER BY created_at DESC LIMIT ? OFFSET ?`

	rows, err := r.db.Query(querySQL, append(args, limit, offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("list audit logs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var entries []models.AuditLog
	for rows.Next() {
		var e models.AuditLog
		if err := rows.Scan(
			&e.ID, &e.UserID, &e.UserName, &e.Action,
			&e.Resource, &e.Details, &e.IP, &e.CreatedAt,
		); err != nil {
			return nil, 0, err
		}
		entries = append(entries, e)
	}

	return entries, total, rows.Err()
}

// buildAuditWhere constructs a parameterised WHERE clause from the filter.
func buildAuditWhere(f AuditFilter) (string, []any) {
	var conds []string
	var args []any

	if f.UserID > 0 {
		conds = append(conds, "user_id = ?")
		args = append(args, f.UserID)
	}
	if f.UserName != "" {
		conds = append(conds, "LOWER(user_name) LIKE LOWER(?) ESCAPE '\\'")
		args = append(args, "%"+escapeAuditLike(f.UserName)+"%")
	}
	if f.Action != "" {
		conds = append(conds, "action = ?")
		args = append(args, f.Action)
	}
	if f.ActionContains != "" {
		conds = append(conds, "LOWER(action) LIKE LOWER(?) ESCAPE '\\'")
		args = append(args, "%"+escapeAuditLike(f.ActionContains)+"%")
	}
	if f.Resource != "" {
		conds = append(conds, "resource = ?")
		args = append(args, f.Resource)
	}
	switch {
	case f.Server == "local":
		conds = append(conds, "resource = 'system'", "action NOT LIKE 'remote\\_%' ESCAPE '\\'")
	case f.Server != "":
		conds = append(conds, "resource = 'system'", "action LIKE 'remote\\_%' ESCAPE '\\'", "LOWER(LTRIM(details)) LIKE LOWER(?) ESCAPE '\\'")
		args = append(args, escapeAuditLike(f.Server)+":%")
	}
	if f.From != nil {
		conds = append(conds, "datetime(created_at) >= datetime(?)")
		args = append(args, f.From.UTC().Format(time.RFC3339))
	}
	if f.To != nil {
		conds = append(conds, "datetime(created_at) <= datetime(?)")
		args = append(args, f.To.UTC().Format(time.RFC3339))
	}

	if len(conds) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

func escapeAuditLike(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "%", "\\%")
	return strings.ReplaceAll(value, "_", "\\_")
}
