package store

import (
	"database/sql"
	"time"
)

// ── Status Page Operations ───────────────────────────────────────────────────

func (r *UptimeRepository) ListStatusPages() ([]UptimeStatusPage, error) {
	rows, err := r.db.Query(`SELECT id, slug, title, description, theme, logo_url, is_public, history_days, created_at FROM uptime_status_pages ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []UptimeStatusPage
	for rows.Next() {
		var sp UptimeStatusPage
		var desc, logo sql.NullString
		if err := rows.Scan(&sp.ID, &sp.Slug, &sp.Title, &desc, &sp.Theme, &logo, &sp.IsPublic, &sp.HistoryDays, &sp.CreatedAt); err != nil {
			return nil, err
		}
		if desc.Valid {
			sp.Description = desc.String
		}
		if logo.Valid {
			sp.LogoURL = logo.String
		}
		out = append(out, sp)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for index := range out {
		if err := r.loadStatusPageMonitors(&out[index]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (r *UptimeRepository) GetStatusPage(idOrSlug interface{}) (*UptimeStatusPage, error) {
	var sp UptimeStatusPage
	var desc, logo sql.NullString
	var q string
	switch v := idOrSlug.(type) {
	case int64:
		q = `SELECT id, slug, title, description, theme, logo_url, is_public, history_days, created_at FROM uptime_status_pages WHERE id = ?`
		if err := r.db.QueryRow(q, v).Scan(&sp.ID, &sp.Slug, &sp.Title, &desc, &sp.Theme, &logo, &sp.IsPublic, &sp.HistoryDays, &sp.CreatedAt); err != nil {
			if err == sql.ErrNoRows {
				return nil, nil
			}
			return nil, err
		}
	case string:
		q = `SELECT id, slug, title, description, theme, logo_url, is_public, history_days, created_at FROM uptime_status_pages WHERE slug = ?`
		if err := r.db.QueryRow(q, v).Scan(&sp.ID, &sp.Slug, &sp.Title, &desc, &sp.Theme, &logo, &sp.IsPublic, &sp.HistoryDays, &sp.CreatedAt); err != nil {
			if err == sql.ErrNoRows {
				return nil, nil
			}
			return nil, err
		}
	}
	if desc.Valid {
		sp.Description = desc.String
	}
	if logo.Valid {
		sp.LogoURL = logo.String
	}

	if err := r.loadStatusPageMonitors(&sp); err != nil {
		return nil, err
	}
	return &sp, nil
}

func (r *UptimeRepository) loadStatusPageMonitors(sp *UptimeStatusPage) error {
	rows, err := r.db.Query(`SELECT monitor_id, display_name, sort_order FROM uptime_status_page_monitors WHERE status_page_id = ? ORDER BY sort_order ASC`, sp.ID)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	sp.Monitors = nil
	for rows.Next() {
		var e StatusPageMonitorEntry
		var dn sql.NullString
		if err := rows.Scan(&e.MonitorID, &dn, &e.SortOrder); err != nil {
			return err
		}
		if dn.Valid {
			e.DisplayName = dn.String
		}
		sp.Monitors = append(sp.Monitors, e)
	}
	return rows.Err()
}

func (r *UptimeRepository) CreateStatusPage(sp *UptimeStatusPage) error {
	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.Exec(`
		INSERT INTO uptime_status_pages (slug, title, description, theme, logo_url, is_public, history_days, created_at)
		VALUES (?,?,?,?,?,?,?,?)
	`, sp.Slug, sp.Title, nullStr(sp.Description), sp.Theme, nullStr(sp.LogoURL), sp.IsPublic, sp.HistoryDays, now)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	if err := replaceStatusPageMonitors(tx, id, sp.Monitors); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	sp.ID = id
	sp.CreatedAt = now
	return nil
}

func (r *UptimeRepository) UpdateStatusPage(sp *UptimeStatusPage) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.Exec(`
		UPDATE uptime_status_pages SET slug=?, title=?, description=?, theme=?, logo_url=?, is_public=?, history_days=? WHERE id=?
	`, sp.Slug, sp.Title, nullStr(sp.Description), sp.Theme, nullStr(sp.LogoURL), sp.IsPublic, sp.HistoryDays, sp.ID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return sql.ErrNoRows
	}
	if err := replaceStatusPageMonitors(tx, sp.ID, sp.Monitors); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *UptimeRepository) DeleteStatusPage(id int64) error {
	_, err := r.db.Exec(`DELETE FROM uptime_status_pages WHERE id = ?`, id)
	return err
}

func replaceStatusPageMonitors(tx *sql.Tx, pageID int64, monitors []StatusPageMonitorEntry) error {
	if _, err := tx.Exec(`DELETE FROM uptime_status_page_monitors WHERE status_page_id = ?`, pageID); err != nil {
		return err
	}
	for _, m := range monitors {
		if _, err := tx.Exec(`INSERT INTO uptime_status_page_monitors (status_page_id, monitor_id, display_name, sort_order) VALUES (?,?,?,?)`,
			pageID, m.MonitorID, nullStr(m.DisplayName), m.SortOrder); err != nil {
			return err
		}
	}
	return nil
}
