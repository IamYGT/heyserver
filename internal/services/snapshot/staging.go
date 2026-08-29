package snapshot

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/IamYGT/heyserver/internal/services/backup"
)

func (s *Service) stagingDir() string {
	return filepath.Join(s.localDir, "snapshot-staging")
}

func (s *Service) prepareStaging(ctx context.Context, jobID string) (string, error) {
	dir := s.stagingDir()
	if err := os.RemoveAll(dir); err != nil {
		return "", fmt.Errorf("clean staging: %w", err)
	}
	for _, sub := range []string{"databases", "exports"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o750); err != nil {
			return "", err
		}
	}

	s.log(jobID, "PHASE=staging dir="+dir)

	if err := s.exportCrontab(ctx, dir, jobID); err != nil {
		s.log(jobID, "WARN crontab export: "+err.Error())
	}
	if err := s.exportNginx(ctx, dir, jobID); err != nil {
		s.log(jobID, "WARN nginx export: "+err.Error())
	}

	if err := s.dumpPostgreSQL(ctx, dir, jobID); err != nil {
		return "", fmt.Errorf("postgresql dump: %w", err)
	}
	if err := s.dumpMariaDB(ctx, dir, jobID); err != nil {
		s.log(jobID, "WARN mariadb dump: "+err.Error())
	}

	return dir, nil
}

func (s *Service) exportCrontab(ctx context.Context, staging, jobID string) error {
	outPath := filepath.Join(staging, "exports", "crontab-root.txt")
	cmd := exec.CommandContext(ctx, "crontab", "-l")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return err
	}
	s.log(jobID, "EXPORT crontab-root -> "+outPath)
	return os.WriteFile(outPath, out, 0o600)
}

func (s *Service) exportNginx(ctx context.Context, staging, jobID string) error {
	outPath := filepath.Join(staging, "exports", "nginx-T.conf")
	cmd := exec.CommandContext(ctx, "nginx", "-T")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return err
	}
	s.log(jobID, "EXPORT nginx -T -> "+outPath)
	return os.WriteFile(outPath, out, 0o600)
}

func (s *Service) dumpPostgreSQL(ctx context.Context, staging, jobID string) error {
	outPath := filepath.Join(staging, "databases", "postgresql-all.sql.gz")
	s.log(jobID, "DUMP postgresql -> "+outPath)
	done := make(chan error, 1)
	go func() {
		err := backup.PgDumpGzipExported("all", "postgresql", outPath, 6, 10*time.Minute, func(line string) {
			s.log(jobID, line)
		})
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			return err
		}
	case <-ctx.Done():
		return ctx.Err()
	}
	return backup.ValidateDatabaseBackupSizeFile(outPath)
}

func (s *Service) dumpMariaDB(ctx context.Context, staging, jobID string) error {
	if _, err := exec.LookPath("mysqldump"); err != nil {
		return fmt.Errorf("mysqldump not installed")
	}
	outPath := filepath.Join(staging, "databases", "mariadb-all.sql.gz")
	s.log(jobID, "DUMP mariadb -> "+outPath)
	done := make(chan error, 1)
	go func() {
		err := backup.PgDumpGzipExported("all", "mariadb", outPath, 6, 10*time.Minute, func(line string) {
			s.log(jobID, line)
		})
		done <- err
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
