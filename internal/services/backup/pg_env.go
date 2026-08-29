package backup

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const minDatabaseBackupBytes = 128
const databaseTargetHeader = "-- HSERVER_DATABASE_TARGET "

func databaseBackupHeader(database string) string {
	encoded := base64.RawURLEncoding.EncodeToString([]byte(database))
	return databaseTargetHeader + encoded + "\n"
}

func databaseTargetFromBackup(filePath, engine string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	var reader io.Reader = f
	if strings.HasSuffix(filePath, ".gz") {
		gz, gzipErr := gzip.NewReader(f)
		if gzipErr != nil {
			return "", gzipErr
		}
		defer func() { _ = gz.Close() }()
		reader = gz
	}
	scanner := bufio.NewScanner(io.LimitReader(reader, 64*1024))
	if scanner.Scan() && strings.HasPrefix(scanner.Text(), databaseTargetHeader) {
		encoded := strings.TrimSpace(strings.TrimPrefix(scanner.Text(), databaseTargetHeader))
		decoded, decodeErr := base64.RawURLEncoding.DecodeString(encoded)
		if decodeErr != nil {
			return "", fmt.Errorf("invalid database target metadata: %w", decodeErr)
		}
		target, targetErr := validateDatabaseTarget(string(decoded))
		if targetErr != nil {
			return "", targetErr
		}
		base := filepath.Base(filePath)
		if strings.Contains(base, "-db-") {
			suffix := "-db-" + target + "-" + engine + ".sql"
			withoutGzip := strings.TrimSuffix(base, ".gz")
			if !strings.HasSuffix(withoutGzip, suffix) {
				return "", fmt.Errorf("database target metadata %q does not match backup filename %q", target, base)
			}
		}
		return target, nil
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}

	base := filepath.Base(filePath)
	base = strings.TrimSuffix(base, ".gz")
	base = strings.TrimSuffix(base, ".sql")
	base = strings.TrimSuffix(base, "-"+engine)
	marker := strings.LastIndex(base, "-db-")
	if marker < 0 {
		return "", fmt.Errorf("database target metadata is missing from %q", filepath.Base(filePath))
	}
	return validateDatabaseTarget(base[marker+len("-db-"):])
}

func validateDatabaseTarget(database string) (string, error) {
	if database == "all" {
		return database, nil
	}
	if database == "" || sanitize(database) != database {
		return "", fmt.Errorf("invalid database restore target %q", database)
	}
	return database, nil
}

// pgRunAs returns the OS user for pg_dump when the panel runs as root (peer auth).
func pgRunAs() string {
	if v := os.Getenv("HSERVER_PG_RUN_AS"); v != "" {
		return v
	}
	return "postgres"
}

// pgDumpEnv returns environment variables for PostgreSQL dump tools.
// HSERVER_PG_BACKUP_USER overrides PGUSER; default is postgres (not root).
func pgDumpEnv() []string {
	user := os.Getenv("HSERVER_PG_BACKUP_USER")
	if user == "" {
		user = os.Getenv("PGUSER")
	}
	if user == "" {
		user = "postgres"
	}
	host := os.Getenv("HSERVER_PG_BACKUP_HOST")
	port := os.Getenv("HSERVER_PG_BACKUP_PORT")

	env := make([]string, 0, len(os.Environ())+3)
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "PGUSER=") || strings.HasPrefix(e, "PGHOST=") || strings.HasPrefix(e, "PGPORT=") {
			continue
		}
		env = append(env, e)
	}
	env = append(env, "PGUSER="+user)
	if host != "" {
		env = append(env, "PGHOST="+host)
	}
	if port != "" {
		env = append(env, "PGPORT="+port)
	}
	return env
}

func pgUserFromEnv() string {
	user := os.Getenv("HSERVER_PG_BACKUP_USER")
	if user == "" {
		user = os.Getenv("PGUSER")
	}
	if user == "" {
		user = "postgres"
	}
	return user
}

func pgDumpLogUser() string {
	if runAs := pgRunAs(); runAs != "" {
		return runAs + " (sudo)"
	}
	return pgUserFromEnv()
}

func pgClientEnvAssignments() []string {
	assignments := []string{"PGUSER=" + pgUserFromEnv()}
	if host := os.Getenv("HSERVER_PG_BACKUP_HOST"); host != "" {
		assignments = append(assignments, "PGHOST="+host)
	}
	if port := os.Getenv("HSERVER_PG_BACKUP_PORT"); port != "" {
		assignments = append(assignments, "PGPORT="+port)
	}
	if passfile := os.Getenv("HSERVER_PG_PASSFILE"); passfile != "" {
		assignments = append(assignments, "PGPASSFILE="+passfile)
	}
	return assignments
}

// pgDumpCommand builds pg_dump/pg_dumpall via sudo -u postgres so peer auth works when panel runs as root.
func pgDumpCommand(ctx context.Context, db, engine string) *exec.Cmd {
	if engine != "postgresql" {
		return mysqlDumpCommand(ctx, db)
	}
	args := []string{"-n", "-u", pgRunAs(), "--", "env"}
	args = append(args, pgClientEnvAssignments()...)
	if db != "all" {
		args = append(args, "pg_dump", "--clean", "--if-exists", "-Fp", db)
	} else {
		args = append(args, "pg_dumpall", "--clean", "--if-exists")
	}
	return exec.CommandContext(ctx, "sudo", args...)
}

func pgRestoreCommand(ctx context.Context, database string) *exec.Cmd {
	args := []string{"-n", "-u", pgRunAs(), "--", "env"}
	args = append(args, pgClientEnvAssignments()...)
	args = append(args, "psql", "-v", "ON_ERROR_STOP=1", database)
	return exec.CommandContext(ctx, "sudo", args...)
}

func validateDatabaseBackupSize(size int64) error {
	if size < minDatabaseBackupBytes {
		return fmt.Errorf("database backup too small (%d bytes) — pg_dump likely failed; check PostgreSQL peer auth / sudo", size)
	}
	return nil
}
