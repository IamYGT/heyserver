// Sync an existing admin user's password from HSERVER_ADMIN_PASS in data/.env.
// Usage: HSERVER_DATA_DIR=/var/lib/hserver go run ./scripts/reset-admin-password
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/IamYGT/heyserver/internal/db"
)

func main() {
	dataDir := os.Getenv("HSERVER_DATA_DIR")
	if dataDir == "" {
		dataDir = "data"
	}
	envPath := filepath.Join(dataDir, ".env")
	dbPath := os.Getenv("HSERVER_DB_PATH")
	if dbPath == "" {
		dbPath = filepath.Join(dataDir, "hserver.db")
	}

	pass := os.Getenv("HSERVER_ADMIN_PASS")
	email := os.Getenv("HSERVER_ADMIN_EMAIL")
	if pass == "" {
		pass = readEnvFile(envPath, "HSERVER_ADMIN_PASS")
	}
	if email == "" {
		email = readEnvFile(envPath, "HSERVER_ADMIN_EMAIL")
	}
	if email == "" {
		email = "admin@localhost"
	}

	if pass == "" {
		fmt.Fprintln(os.Stderr, "HSERVER_ADMIN_PASS is missing; set it in the environment file before running this utility")
		os.Exit(1)
	}

	if _, err := db.Open(dbPath); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	users := db.NewUserRepository(db.Instance())
	user, err := users.FindByEmail(email)
	if err != nil {
		fmt.Fprintln(os.Stderr, "user not found:", email, err)
		os.Exit(1)
	}
	if err := users.UpdatePassword(user.ID, pass); err != nil {
		fmt.Fprintln(os.Stderr, "update failed:", err)
		os.Exit(1)
	}
	fmt.Println("admin password synced for", email)
	fmt.Println("password value was not printed")
}

func readEnvFile(path, key string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	prefix := key + "="
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}
