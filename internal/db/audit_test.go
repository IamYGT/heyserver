package db_test

import (
	"path/filepath"
	"testing"

	"github.com/IamYGT/heyserver/internal/auth"
	"github.com/IamYGT/heyserver/internal/db"
	"github.com/IamYGT/heyserver/internal/models"
)

func TestAuditRepository_InsertAndList(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.db")
	sqlDB, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	users := db.NewUserRepository(sqlDB)
	hash, _ := auth.HashPassword("secret-pass-123")
	u := &models.User{Email: "audit@test.local", Name: "Auditor", Password: hash, Role: models.RoleAdmin}
	if err := users.Create(u); err != nil {
		t.Fatal(err)
	}

	repo := db.NewAuditRepository(sqlDB)
	entry := &models.AuditLog{
		UserID:   u.ID,
		UserName: u.Name,
		Action:   "login",
		Resource: "auth",
		Details:  "ok",
		IP:       "127.0.0.1",
	}
	if err := repo.Insert(entry); err != nil {
		t.Fatal(err)
	}
	if entry.ID == 0 {
		t.Fatal("expected audit ID")
	}

	list, total, err := repo.List(db.AuditFilter{UserID: u.ID}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(list) != 1 || list[0].Action != "login" {
		t.Errorf("list=%+v total=%d", list, total)
	}
}
