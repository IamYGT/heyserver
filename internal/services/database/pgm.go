package database

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"os/exec"

	"github.com/IamYGT/heyserver/internal/services/shell"
)

const defaultPGMBackupDir = "/var/lib/hserver/pgm-backups"

var (
	ErrInvalidBackupInput    = errors.New("invalid database backup input")
	ErrBackupNotFound        = errors.New("database backup not found")
	ErrBackupRootUnavailable = errors.New("database backup root unavailable")
	ErrCredentialNotFound    = errors.New("database credential not found")
)

// pgmBackupRoot is swappable in tests. Self-hosted installations can override
// the provider-neutral default with HSERVER_PGM_BACKUP_DIR. Existing installs
// retain their configured directory during in-place upgrades.
var pgmBackupRoot = configuredPGMBackupRoot()

func configuredPGMBackupRoot() string {
	if root := strings.TrimSpace(os.Getenv("HSERVER_PGM_BACKUP_DIR")); root != "" {
		return filepath.Clean(root)
	}
	return defaultPGMBackupDir
}

func requirePGMBackupRoot() error {
	info, err := os.Stat(pgmBackupRoot)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrBackupRootUnavailable, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%w: configured path is not a directory", ErrBackupRootUnavailable)
	}
	return nil
}

// PGMCredential holds a database credential from pgm_metadata.
type PGMCredential struct {
	ID         int    `json:"id"`
	DBName     string `json:"dbName"`
	DBUser     string `json:"dbUser"`
	DBPassword string `json:"dbPassword"`
	DBHost     string `json:"dbHost"`
	DBPort     int    `json:"dbPort"`
	ConnString string `json:"connectionString,omitempty"`
	Notes      string `json:"notes,omitempty"`
	IsActive   bool   `json:"isActive"`
	CreatedAt  string `json:"createdAt"`
}

// PGMBackup holds info about a pgm backup directory.
type PGMBackup struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Size      string `json:"size"`
	Databases int    `json:"databases"`
	CreatedAt string `json:"createdAt"` // parsed from dir name
}

// ListPGMCredentials returns all database credentials from pgm_metadata.
func ListPGMCredentials() ([]PGMCredential, error) {
	query := `SELECT id, db_name, db_user, db_password, COALESCE(db_host,'127.0.0.1'), COALESCE(db_port,5432), COALESCE(connection_string,''), COALESCE(notes,''), COALESCE(is_active,true), COALESCE(created_at::text,'') FROM db_credentials ORDER BY db_name`
	out, err := rawExec(30*time.Second, "sudo", "-u", "postgres", "psql", "-d", "pgm_metadata", "-t", "-A", "-F\t", "-c", query)
	if err != nil {
		return nil, fmt.Errorf("query pgm_metadata: %w", err)
	}
	return parsePGMCredentialLines(out), nil
}

func parsePGMCredentialLines(out string) []PGMCredential {
	var creds []PGMCredential
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 10 {
			continue
		}
		port := 5432
		_, _ = fmt.Sscanf(parts[5], "%d", &port)
		id := 0
		_, _ = fmt.Sscanf(parts[0], "%d", &id)

		creds = append(creds, PGMCredential{
			ID:         id,
			DBName:     parts[1],
			DBUser:     parts[2],
			DBPassword: parts[3],
			DBHost:     parts[4],
			DBPort:     port,
			ConnString: parts[6],
			Notes:      parts[7],
			IsActive:   parts[8] == "t" || parts[8] == "true",
			CreatedAt:  parts[9],
		})
	}
	return creds
}

// GetPGMCredential returns a single credential by database name.
func GetPGMCredential(dbName string) (*PGMCredential, error) {
	creds, err := ListPGMCredentials()
	if err != nil {
		return nil, err
	}
	for _, c := range creds {
		if c.DBName == dbName {
			return &c, nil
		}
	}
	return nil, fmt.Errorf("%w for %s", ErrCredentialNotFound, dbName)
}

// ListPGMBackups returns available pgm backup directories.
// formatPGMBackupDirName parses dirname like 20260409_060007 into a display timestamp.
func formatPGMBackupDirName(name string) string {
	if len(name) < 15 {
		return ""
	}
	return name[0:4] + "-" + name[4:6] + "-" + name[6:8] + " " + name[9:11] + ":" + name[11:13] + ":" + name[13:15]
}

func ListPGMBackups() ([]PGMBackup, error) {
	if err := requirePGMBackupRoot(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(pgmBackupRoot)
	if err != nil {
		return nil, fmt.Errorf("%w: read configured directory: %v", ErrBackupRootUnavailable, err)
	}

	backups := make([]PGMBackup, 0)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if err := validatePGMBackupName(name); err != nil {
			continue
		}
		path := filepath.Join(pgmBackupRoot, name)

		// Count .sql.gz files
		files, _ := filepath.Glob(filepath.Join(path, "*.sql.gz"))
		if files == nil {
			files, _ = filepath.Glob(filepath.Join(path, "*.sql"))
		}

		createdAt := formatPGMBackupDirName(name)

		// Get size
		sizeOut, _ := shell.ExecuteRaw("du", "-sh", path)
		size := "unknown"
		if parts := strings.Fields(sizeOut); len(parts) > 0 {
			size = parts[0]
		}

		backups = append(backups, PGMBackup{
			Name:      name,
			Path:      path,
			Size:      size,
			Databases: len(files),
			CreatedAt: createdAt,
		})
	}

	// Sort newest first
	sort.Slice(backups, func(i, j int) bool {
		return backups[i].Name > backups[j].Name
	})

	return backups, nil
}

// ListPGMBackupFiles returns the list of .sql.gz / .sql files inside a backup directory.
func ListPGMBackupFiles(backupName string) ([]string, error) {
	// Validate backup name — only directory names inside pgmBackupDir are accepted.
	if err := validatePGMBackupName(backupName); err != nil {
		return nil, err
	}
	if err := requirePGMBackupRoot(); err != nil {
		return nil, err
	}
	dir := filepath.Join(pgmBackupRoot, backupName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", ErrBackupNotFound, backupName)
		}
		return nil, fmt.Errorf("read backup dir: %w", err)
	}
	files := make([]string, 0)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".sql.gz") || strings.HasSuffix(name, ".sql") {
			files = append(files, name)
		}
	}
	sort.Strings(files)
	return files, nil
}

// RestorePGMBackup restores a single database from a pgm backup.
// backupPath must be a full path to a .sql.gz or .sql file inside pgmBackupDir.
// The target database must already exist; this issues a plain restore (not pg_restore).
func RestorePGMBackup(dbName, backupPath string) error {
	if err := ValidateIdentifier(dbName); err != nil {
		return fmt.Errorf("%w: database name: %v", ErrInvalidBackupInput, err)
	}
	if !strings.HasSuffix(backupPath, ".sql") && !strings.HasSuffix(backupPath, ".sql.gz") {
		return fmt.Errorf("%w: backup file must end with .sql or .sql.gz", ErrInvalidBackupInput)
	}
	if len(backupPath) > 4096 {
		return fmt.Errorf("%w: backup path exceeds 4096 characters", ErrInvalidBackupInput)
	}

	// Safety: both the lexical and resolved paths must stay inside the configured
	// backup root. A symlinked backup file is rejected rather than followed.
	clean := filepath.Clean(backupPath)
	allowed := filepath.Clean(pgmBackupRoot)
	if !pathInsideRoot(allowed, clean) {
		return fmt.Errorf("%w: backup path outside allowed directory", ErrInvalidBackupInput)
	}
	if err := requirePGMBackupRoot(); err != nil {
		return err
	}

	info, err := os.Lstat(clean)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %s", ErrBackupNotFound, filepath.Base(clean))
		}
		return fmt.Errorf("inspect backup file: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: backup path must be a regular file", ErrInvalidBackupInput)
	}
	resolvedRoot, err := filepath.EvalSymlinks(allowed)
	if err != nil {
		return fmt.Errorf("resolve backup root: %w", err)
	}
	resolvedFile, err := filepath.EvalSymlinks(clean)
	if err != nil {
		return fmt.Errorf("resolve backup file: %w", err)
	}
	if !pathInsideRoot(resolvedRoot, resolvedFile) {
		return fmt.Errorf("%w: resolved backup path outside allowed directory", ErrInvalidBackupInput)
	}

	if strings.HasSuffix(clean, ".sql.gz") {
		// gunzip | psql
		gunzip := exec.Command("gunzip", "--stdout", clean)
		psql := exec.Command("sudo", "-u", "postgres", "psql", "-d", dbName, "-v", "ON_ERROR_STOP=1")
		pipe, err := gunzip.StdoutPipe()
		if err != nil {
			return fmt.Errorf("pipe setup: %w", err)
		}
		psql.Stdin = pipe
		if err := gunzip.Start(); err != nil {
			return fmt.Errorf("gunzip start: %w", err)
		}
		out, err := psql.CombinedOutput()
		if werr := gunzip.Wait(); werr != nil && err == nil {
			return fmt.Errorf("gunzip: %w", werr)
		}
		if err != nil {
			return fmt.Errorf("psql restore: %s", strings.TrimSpace(string(out)))
		}
		return nil
	}

	// Plain .sql file
	psql := exec.Command("sudo", "-u", "postgres", "psql", "-d", dbName, "-v", "ON_ERROR_STOP=1", "-f", clean)
	out, err := psql.CombinedOutput()
	if err != nil {
		return fmt.Errorf("psql restore: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

func pathInsideRoot(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	if err != nil || rel == "." || rel == ".." {
		return false
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func validatePGMBackupName(name string) error {
	if name == "" || len(name) > 128 {
		return fmt.Errorf("%w: backup name must use 1-128 characters", ErrInvalidBackupInput)
	}
	for _, c := range name {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '_' && c != '-' {
			return fmt.Errorf("%w: invalid backup name", ErrInvalidBackupInput)
		}
	}
	return nil
}
