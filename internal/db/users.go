package db

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"

	"github.com/IamYGT/heyserver/internal/auth"
	"github.com/IamYGT/heyserver/internal/models"
)

// ErrNotFound is returned by repository functions when a record does not exist.
var ErrNotFound = errors.New("record not found")

// ErrDuplicateEmail is returned when trying to create a user with an existing email.
var ErrDuplicateEmail = errors.New("email already in use")

// ErrLastAdmin is returned when a mutation would leave the panel without an
// administrator account.
var ErrLastAdmin = errors.New("at least one admin account is required")

// UserRepository provides data access for the users table.
type UserRepository struct {
	db *sql.DB
}

// NewUserRepository returns a UserRepository backed by the given connection.
func NewUserRepository(db *sql.DB) *UserRepository {
	return &UserRepository{db: db}
}

// FindByID returns the user with the given primary key.
func (r *UserRepository) FindByID(id int64) (*models.User, error) {
	row := r.db.QueryRow(
		`SELECT id, email, name, password, role, totp_secret, totp_enabled, created_at, updated_at
		   FROM users WHERE id = ?`, id,
	)
	return scanUser(row)
}

// FindByEmail returns the user matching the given e-mail address (case-insensitive).
func (r *UserRepository) FindByEmail(email string) (*models.User, error) {
	row := r.db.QueryRow(
		`SELECT id, email, name, password, role, totp_secret, totp_enabled, created_at, updated_at
		   FROM users WHERE email = ? COLLATE NOCASE`, email,
	)
	return scanUser(row)
}

// List returns a paginated list of users.
func (r *UserRepository) List(limit, offset int) ([]models.User, error) {
	if limit <= 0 || limit > 200 {
		limit = 20
	}

	rows, err := r.db.Query(
		`SELECT id, email, name, password, role, totp_secret, totp_enabled, created_at, updated_at
		   FROM users
		  ORDER BY created_at DESC
		  LIMIT ? OFFSET ?`, limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var users []models.User
	for rows.Next() {
		u, err := scanUserRow(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, *u)
	}
	return users, rows.Err()
}

// Count returns the total number of users.
func (r *UserRepository) Count() (int, error) {
	var n int
	err := r.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// Create inserts a new user. The Password field must already be bcrypt-hashed.
func (r *UserRepository) Create(u *models.User) error {
	res, err := r.db.Exec(
		`INSERT INTO users(email, name, password, role)
		 VALUES(?, ?, ?, ?)`,
		u.Email, u.Name, u.Password, u.Role,
	)
	if err != nil {
		if isUniqueConstraint(err) {
			return ErrDuplicateEmail
		}
		return fmt.Errorf("create user: %w", err)
	}

	id, _ := res.LastInsertId()
	u.ID = id
	return nil
}

// Update persists name, email and role changes without changing the password.
func (r *UserRepository) Update(u *models.User) error {
	return r.UpdateWithPassword(u, nil)
}

// UpdateWithPassword atomically persists profile fields and an optional plain
// password. It refuses to demote the final administrator account.
func (r *UserRepository) UpdateWithPassword(u *models.User, plainPassword *string) error {
	if u == nil {
		return errors.New("user is required")
	}
	passwordHash := ""
	if plainPassword != nil {
		var err error
		passwordHash, err = auth.HashPassword(*plainPassword)
		if err != nil {
			return fmt.Errorf("hash password: %w", err)
		}
	}

	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("begin user update: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	result, err := tx.Exec(
		`UPDATE users
		    SET email      = ?,
		        name       = ?,
		        role       = ?,
		        updated_at = datetime('now')
		  WHERE id = ?
		    AND (
		      role <> ?
		      OR ? = ?
		      OR EXISTS (
		        SELECT 1 FROM users AS other
		         WHERE other.role = ? AND other.id <> ?
		      )
		    )`,
		u.Email, u.Name, u.Role, u.ID,
		models.RoleAdmin, u.Role, models.RoleAdmin,
		models.RoleAdmin, u.ID,
	)
	if err != nil {
		if isUniqueConstraint(err) {
			return ErrDuplicateEmail
		}
		return fmt.Errorf("update user: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect user update: %w", err)
	}
	if changed != 1 {
		var currentRole models.Role
		if err := tx.QueryRow(`SELECT role FROM users WHERE id = ?`, u.ID).Scan(&currentRole); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("inspect protected user update: %w", err)
		}
		if currentRole == models.RoleAdmin && u.Role != models.RoleAdmin {
			return ErrLastAdmin
		}
		return ErrNotFound
	}

	if plainPassword != nil {
		result, err = tx.Exec(
			`UPDATE users SET password = ?, updated_at = datetime('now') WHERE id = ?`,
			passwordHash, u.ID,
		)
		if err != nil {
			return fmt.Errorf("update user password: %w", err)
		}
		changed, err = result.RowsAffected()
		if err != nil {
			return fmt.Errorf("inspect password update: %w", err)
		}
		if changed != 1 {
			return ErrNotFound
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit user update: %w", err)
	}
	return nil
}

// UpdatePassword hashes and stores a new password for the given user.
func (r *UserRepository) UpdatePassword(id int64, plainPassword string) error {
	hash, err := auth.HashPassword(plainPassword)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	_, err = r.db.Exec(
		`UPDATE users SET password = ?, updated_at = datetime('now') WHERE id = ?`,
		hash, id,
	)
	return err
}

// Delete removes a user by ID.
func (r *UserRepository) Delete(id int64) error {
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("begin user deletion: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	result, err := tx.Exec(
		`DELETE FROM users
		  WHERE id = ?
		    AND (
		      role <> ?
		      OR EXISTS (
		        SELECT 1 FROM users AS other
		         WHERE other.role = ? AND other.id <> ?
		      )
		    )`,
		id, models.RoleAdmin, models.RoleAdmin, id,
	)
	if err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect user deletion: %w", err)
	}
	if changed != 1 {
		var currentRole models.Role
		if err := tx.QueryRow(`SELECT role FROM users WHERE id = ?`, id).Scan(&currentRole); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("inspect protected user deletion: %w", err)
		}
		if currentRole == models.RoleAdmin {
			return ErrLastAdmin
		}
		return ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit user deletion: %w", err)
	}
	return nil
}

// EnsureAdmin creates the admin user if no users exist in the database.
// If adminPass is empty, a random 16-character hex password is generated and
// printed to stdout.
func (r *UserRepository) EnsureAdmin(adminEmail, adminPass string) error {
	count, err := r.Count()
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	if adminPass == "" {
		b := make([]byte, 8)
		if _, err := rand.Read(b); err != nil {
			return fmt.Errorf("generate password: %w", err)
		}
		adminPass = hex.EncodeToString(b)
		// Print once to stdout so the operator can retrieve it.
		fmt.Printf("\n==========================================================\n")
		fmt.Printf("  FIRST BOOT — admin account created\n")
		fmt.Printf("  Email   : %s\n", adminEmail)
		fmt.Printf("  Password: %s\n", adminPass)
		fmt.Printf("  Change this password immediately after first login.\n")
		fmt.Printf("==========================================================\n\n")
	}

	hash, err := auth.HashPassword(adminPass)
	if err != nil {
		return err
	}

	admin := &models.User{
		Email:    adminEmail,
		Name:     "Administrator",
		Password: hash,
		Role:     models.RoleAdmin,
	}

	if err := r.Create(admin); err != nil {
		return fmt.Errorf("create admin: %w", err)
	}

	slog.Info("admin user created", "email", adminEmail)
	return nil
}

// ---- helpers ---------------------------------------------------------------

type scanner interface {
	Scan(dest ...any) error
}

func scanUser(s scanner) (*models.User, error) {
	u, err := scanUserRow(s)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return u, err
}

func scanUserRow(s scanner) (*models.User, error) {
	var u models.User
	var totpEnabled int
	err := s.Scan(
		&u.ID, &u.Email, &u.Name, &u.Password, &u.Role,
		&u.TOTPSecret, &totpEnabled,
		&u.CreatedAt, &u.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	u.TOTPEnabled = totpEnabled != 0
	return &u, nil
}

// UpdateUserTOTP persists the TOTP secret and enabled state for the given user.
// Pass an empty secret and enabled=false to disable/clear TOTP.
func (r *UserRepository) UpdateUserTOTP(id int64, secret string, enabled bool) error {
	enabledInt := 0
	if enabled {
		enabledInt = 1
	}
	_, err := r.db.Exec(
		`UPDATE users SET totp_secret = ?, totp_enabled = ?, updated_at = datetime('now') WHERE id = ?`,
		secret, enabledInt, id,
	)
	if err != nil {
		return fmt.Errorf("update totp: %w", err)
	}
	return nil
}

// SaveRecoveryCodes deletes all existing recovery codes for the user and
// inserts freshly-hashed codes in a single transaction.
// plainCodes are the raw codes shown to the user — only bcrypt hashes are stored.
func (r *UserRepository) SaveRecoveryCodes(userID int64, plainCodes []string) error {
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.Exec(`DELETE FROM totp_recovery_codes WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("delete old recovery codes: %w", err)
	}

	for _, code := range plainCodes {
		hash, err := auth.HashPassword(code)
		if err != nil {
			return fmt.Errorf("hash recovery code: %w", err)
		}
		if _, err := tx.Exec(
			`INSERT INTO totp_recovery_codes(user_id, code_hash) VALUES(?, ?)`,
			userID, hash,
		); err != nil {
			return fmt.Errorf("insert recovery code: %w", err)
		}
	}

	return tx.Commit()
}

// UseRecoveryCode verifies a plain-text recovery code against stored hashes for
// the user. If a match is found and the code has not been used, it is marked as
// used atomically and true is returned. Returns false if no valid code matches.
func (r *UserRepository) UseRecoveryCode(userID int64, plainCode string) (bool, error) {
	rows, err := r.db.Query(
		`SELECT id, code_hash FROM totp_recovery_codes WHERE user_id = ? AND used = 0`,
		userID,
	)
	if err != nil {
		return false, fmt.Errorf("query recovery codes: %w", err)
	}
	defer func() { _ = rows.Close() }()

	type row struct {
		id   int64
		hash string
	}
	var candidates []row
	for rows.Next() {
		var c row
		if err := rows.Scan(&c.id, &c.hash); err != nil {
			return false, err
		}
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		return false, err
	}

	for _, c := range candidates {
		if auth.CheckPassword(c.hash, plainCode) {
			_, err := r.db.Exec(
				`UPDATE totp_recovery_codes SET used = 1, used_at = datetime('now') WHERE id = ?`,
				c.id,
			)
			if err != nil {
				return false, fmt.Errorf("mark recovery code used: %w", err)
			}
			return true, nil
		}
	}
	return false, nil
}

// isUniqueConstraint detects SQLite UNIQUE constraint violation errors.
func isUniqueConstraint(err error) bool {
	if err == nil {
		return false
	}
	// go-sqlite3 returns "UNIQUE constraint failed: ..." messages.
	return containsStr(err.Error(), "UNIQUE constraint failed")
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && findStr(s, sub))
}

func findStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
