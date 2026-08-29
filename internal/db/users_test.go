package db

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/IamYGT/heyserver/internal/auth"
	"github.com/IamYGT/heyserver/internal/models"
)

func testUserRepo(t *testing.T) *UserRepository {
	t.Helper()
	return NewUserRepository(testDB)
}

func makeTestUser(t *testing.T, suffix, role models.Role) *models.User {
	t.Helper()
	hash, err := auth.HashPassword("secret-pass-123")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	email := fmt.Sprintf("%s-%s@example.com", strings.ReplaceAll(t.Name(), "/", "-"), suffix)
	return &models.User{
		Email:    email,
		Name:     "Test User",
		Password: hash,
		Role:     role,
	}
}

func TestUserRepository_CreateAndFindByEmail(t *testing.T) {
	t.Parallel()

	repo := testUserRepo(t)
	user := makeTestUser(t, "create", models.RoleViewer)

	if err := repo.Create(user); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if user.ID == 0 {
		t.Fatal("expected user ID to be set after Create")
	}

	found, err := repo.FindByEmail(user.Email)
	if err != nil {
		t.Fatalf("FindByEmail: %v", err)
	}
	if found.ID != user.ID {
		t.Errorf("ID: got %d want %d", found.ID, user.ID)
	}
	if found.Email != user.Email {
		t.Errorf("Email: got %q want %q", found.Email, user.Email)
	}
	if found.Role != models.RoleViewer {
		t.Errorf("Role: got %q want %q", found.Role, models.RoleViewer)
	}
}

func TestUserRepository_FindByEmailCaseInsensitive(t *testing.T) {
	t.Parallel()

	repo := testUserRepo(t)
	user := makeTestUser(t, "nocase", models.RoleManager)
	if err := repo.Create(user); err != nil {
		t.Fatalf("Create: %v", err)
	}

	found, err := repo.FindByEmail(strings.ToUpper(user.Email))
	if err != nil {
		t.Fatalf("FindByEmail uppercase: %v", err)
	}
	if found.ID != user.ID {
		t.Errorf("ID: got %d want %d", found.ID, user.ID)
	}
}

func TestUserRepository_Count(t *testing.T) {
	repo := testUserRepo(t)

	user := makeTestUser(t, "count", models.RoleViewer)
	if err := repo.Create(user); err != nil {
		t.Fatalf("Create: %v", err)
	}

	n, err := repo.Count()
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if n < 1 {
		t.Errorf("Count: got %d want at least 1", n)
	}

	found, err := repo.FindByEmail(user.Email)
	if err != nil {
		t.Fatalf("FindByEmail: %v", err)
	}
	if found.ID != user.ID {
		t.Errorf("created user ID: got %d want %d", found.ID, user.ID)
	}
}

func TestUserRepository_UpdateAndDelete(t *testing.T) {
	t.Parallel()

	repo := testUserRepo(t)
	user := makeTestUser(t, "update", models.RoleViewer)
	if err := repo.Create(user); err != nil {
		t.Fatalf("Create: %v", err)
	}

	user.Name = "Updated Name"
	user.Role = models.RoleManager
	if err := repo.Update(user); err != nil {
		t.Fatalf("Update: %v", err)
	}

	found, err := repo.FindByID(user.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if found.Name != "Updated Name" {
		t.Errorf("Name: got %q want %q", found.Name, "Updated Name")
	}
	if found.Role != models.RoleManager {
		t.Errorf("Role: got %q want %q", found.Role, models.RoleManager)
	}

	if err := repo.Delete(user.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err = repo.FindByID(user.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("FindByID after delete: got %v want ErrNotFound", err)
	}
}

func TestUserRepository_DuplicateEmail(t *testing.T) {
	t.Parallel()

	repo := testUserRepo(t)
	user := makeTestUser(t, "dup", models.RoleViewer)
	if err := repo.Create(user); err != nil {
		t.Fatalf("Create: %v", err)
	}

	dup := makeTestUser(t, "dup2", models.RoleViewer)
	dup.Email = user.Email
	err := repo.Create(dup)
	if !errors.Is(err, ErrDuplicateEmail) {
		t.Fatalf("Create duplicate: got %v want ErrDuplicateEmail", err)
	}
}

func TestUserRepository_RoleConstraint(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		role string
		ok   bool
	}{
		{"admin", "admin", true},
		{"manager", "manager", true},
		{"viewer", "viewer", true},
		{"invalid", "superuser", false},
		{"empty", "", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			hash, err := auth.HashPassword("pass")
			if err != nil {
				t.Fatalf("HashPassword: %v", err)
			}
			email := fmt.Sprintf("role-%s-%s@example.com", tc.name, strings.ReplaceAll(t.Name(), "/", "-"))

			_, err = testDB.Exec(
				`INSERT INTO users(email, name, password, role) VALUES(?, ?, ?, ?)`,
				email, "Role Test", hash, tc.role,
			)
			if tc.ok && err != nil {
				t.Fatalf("expected valid role %q to insert, got: %v", tc.role, err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("expected invalid role %q to fail CHECK constraint", tc.role)
			}
		})
	}
}

func TestUserRepository_FindByEmailNotFound(t *testing.T) {
	t.Parallel()

	repo := testUserRepo(t)
	_, err := repo.FindByEmail("nobody@example.com")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("FindByEmail: got %v want ErrNotFound", err)
	}
}

func TestUserRepository_ListHonorsDocumentedMaximum(t *testing.T) {
	repo := testUserRepo(t)
	hash, err := auth.HashPassword("secret-pass-123")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	ids := make([]int64, 0, 120)
	for i := 0; i < 120; i++ {
		user := &models.User{
			Email:    fmt.Sprintf("list-max-%d-%s@example.com", i, strings.ReplaceAll(t.Name(), "/", "-")),
			Name:     "List Maximum",
			Password: hash,
			Role:     models.RoleViewer,
		}
		if err := repo.Create(user); err != nil {
			t.Fatalf("Create user %d: %v", i, err)
		}
		ids = append(ids, user.ID)
	}
	t.Cleanup(func() {
		for _, id := range ids {
			_ = repo.Delete(id)
		}
	})

	users, err := repo.List(200, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(users) < 120 {
		t.Fatalf("List(200) returned %d users, want at least 120", len(users))
	}
}

func TestUserRepository_PreservesAtLeastOneAdministrator(t *testing.T) {
	repo, _ := isolatedUserRepo(t)
	admin := makeTestUser(t, "only-admin", models.RoleAdmin)
	admin.Name = "Only Admin"
	if err := repo.Create(admin); err != nil {
		t.Fatalf("Create only admin: %v", err)
	}

	admin.Name = "Demoted Admin"
	admin.Role = models.RoleViewer
	if err := repo.Update(admin); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("demote only admin: got %v want ErrLastAdmin", err)
	}
	stored, err := repo.FindByID(admin.ID)
	if err != nil {
		t.Fatalf("FindByID after blocked demotion: %v", err)
	}
	if stored.Role != models.RoleAdmin || stored.Name != "Only Admin" {
		t.Fatalf("blocked demotion changed user: role=%q name=%q", stored.Role, stored.Name)
	}
	if err := repo.Delete(admin.ID); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("delete only admin: got %v want ErrLastAdmin", err)
	}

	second := makeTestUser(t, "second-admin", models.RoleAdmin)
	if err := repo.Create(second); err != nil {
		t.Fatalf("Create second admin: %v", err)
	}
	if err := repo.Update(admin); err != nil {
		t.Fatalf("demote admin while another exists: %v", err)
	}
	if err := repo.Delete(second.ID); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("delete new final admin: got %v want ErrLastAdmin", err)
	}

	admin.Role = models.RoleAdmin
	if err := repo.Update(admin); err != nil {
		t.Fatalf("restore second admin: %v", err)
	}
	if err := repo.Delete(second.ID); err != nil {
		t.Fatalf("delete admin while another exists: %v", err)
	}
}

func TestUserRepository_ProfileAndPasswordUpdateIsAtomic(t *testing.T) {
	repo, database := isolatedUserRepo(t)
	user := makeTestUser(t, "atomic-profile-password", models.RoleViewer)
	user.Name = "Original Name"
	if err := repo.Create(user); err != nil {
		t.Fatalf("Create user: %v", err)
	}
	if _, err := database.Exec(fmt.Sprintf(`
		CREATE TRIGGER reject_test_password_update
		BEFORE UPDATE OF password ON users
		WHEN OLD.id = %d
		BEGIN
			SELECT RAISE(ABORT, 'injected password failure');
		END`, user.ID)); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}

	user.Name = "Changed Name"
	newPassword := "replacement-pass-123"
	if err := repo.UpdateWithPassword(user, &newPassword); err == nil || !strings.Contains(err.Error(), "injected password failure") {
		t.Fatalf("injected password update error = %v", err)
	}
	stored, err := repo.FindByID(user.ID)
	if err != nil {
		t.Fatalf("FindByID after rollback: %v", err)
	}
	if stored.Name != "Original Name" {
		t.Fatalf("profile escaped failed transaction: name=%q", stored.Name)
	}
	if !auth.CheckPassword(stored.Password, "secret-pass-123") || auth.CheckPassword(stored.Password, newPassword) {
		t.Fatal("password changed despite failed transaction")
	}

	if _, err := database.Exec(`DROP TRIGGER reject_test_password_update`); err != nil {
		t.Fatalf("drop failure trigger: %v", err)
	}
	if err := repo.UpdateWithPassword(user, &newPassword); err != nil {
		t.Fatalf("successful profile/password update: %v", err)
	}
	stored, err = repo.FindByID(user.ID)
	if err != nil {
		t.Fatalf("FindByID after update: %v", err)
	}
	if stored.Name != "Changed Name" || !auth.CheckPassword(stored.Password, newPassword) {
		t.Fatalf("atomic update not persisted: name=%q passwordMatches=%t", stored.Name, auth.CheckPassword(stored.Password, newPassword))
	}
}

func isolatedUserRepo(t *testing.T) (*UserRepository, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "users.db")
	database, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000", path))
	if err != nil {
		t.Fatalf("open isolated sqlite: %v", err)
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	t.Cleanup(func() { _ = database.Close() })
	if err := database.Ping(); err != nil {
		t.Fatalf("ping isolated sqlite: %v", err)
	}
	if err := runMigrations(database); err != nil {
		t.Fatalf("migrate isolated sqlite: %v", err)
	}
	return NewUserRepository(database), database
}
