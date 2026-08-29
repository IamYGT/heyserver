package backup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/IamYGT/heyserver/internal/models"
)

func formatJobBytes(n int64) string {
	if n <= 0 {
		return "0 B"
	}
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

func formatJobSpeed(bps float64) string {
	if bps <= 0 {
		return ""
	}
	return formatJobBytes(int64(bps)) + "/s"
}

func (s *Service) failJob(jobID string, opts CreateOptions, namePart string, err error) {
	s.appendJobLog(jobID, "FATAL: "+err.Error())
	if s.onNotify != nil {
		s.onNotify(false, opts.Type, namePart, err.Error())
	}
	s.setJob(jobID, JobFailed, err.Error())
}

func (s *Service) createDatabaseTracked(jobID, namePart string, opts CreateOptions, level int) (*models.BackupInfo, error) {
	engine := opts.Engine
	if engine == "" {
		engine = "postgresql"
	}
	db := opts.Database
	if db == "" {
		db = "all"
	}
	db = sanitize(db)

	filename := fmt.Sprintf("%s-db-%s-%s.sql.gz", namePart, db, engine)
	fullPath := filepath.Join(s.backupDir, filename)
	completed := false
	defer func() {
		if !completed {
			_ = os.Remove(fullPath)
		}
	}()

	var dumpCmd string
	if engine == "postgresql" {
		if db != "all" {
			dumpCmd = fmt.Sprintf("sudo -n -u %s pg_dump -Fp %s -> gzip -%d -> %s", pgRunAs(), db, level, fullPath)
		} else {
			dumpCmd = fmt.Sprintf("sudo -n -u %s pg_dumpall -> gzip -%d -> %s", pgRunAs(), level, fullPath)
		}
	} else if db != "all" {
		dumpCmd = fmt.Sprintf("mysqldump %s -> gzip -%d -> %s", db, level, fullPath)
	} else {
		dumpCmd = fmt.Sprintf("mysqldump --all-databases -> gzip -%d -> %s", level, fullPath)
	}

	s.setJobCommand(jobID, dumpCmd)
	s.appendJobLog(jobID, fmt.Sprintf("PHASE=database engine=%s db=%s gzip_level=%d", engine, db, level))
	s.appendJobLog(jobID, "EXEC (checked pipeline): "+dumpCmd)
	s.appendJobLog(jobID, fmt.Sprintf("timeout=%s output=%s", dbTimeout, fullPath))
	s.appendJobLog(jobID, fmt.Sprintf("env run_as=%s", pgDumpLogUser()))

	ctx, cancel := context.WithTimeout(context.Background(), dbTimeout)
	defer cancel()

	done := make(chan error, 1)
	started := time.Now()
	go func() {
		err := pgDumpGzipWithLog(db, engine, fullPath, level, dbTimeout, func(line string) {
			s.appendJobLog(jobID, line)
		})
		done <- err
	}()

	ticker := time.NewTicker(1500 * time.Millisecond)
	defer ticker.Stop()
	var lastSize int64

	for {
		select {
		case err := <-done:
			if err != nil {
				s.appendJobLog(jobID, "stderr/result: "+err.Error())
				return nil, err
			}
			info := s.statBackup(filename, "database")
			if err := validateDatabaseBackupSize(info.Size); err != nil {
				s.appendJobLog(jobID, "VALIDATION: "+err.Error())
				return nil, err
			}
			s.appendJobLog(jobID, fmt.Sprintf("OK database backup size=%s (%d bytes) elapsed=%s",
				formatJobBytes(info.Size), info.Size, time.Since(started).Round(time.Second)))
			s.updateJobDetail(jobID, PhaseDatabase, 35, "Veritabanı yedeklendi: "+formatJobBytes(info.Size), info.Size, info.Size, "")
			completed = true
			return info, nil

		case <-ctx.Done():
			s.appendJobLog(jobID, "TIMEOUT: database backup exceeded "+dbTimeout.String())
			return nil, ctx.Err()

		case <-ticker.C:
			if st, err := os.Stat(fullPath); err == nil {
				size := st.Size()
				if size != lastSize {
					lastSize = size
					prog := 12 + min(22, int(size/(10*1024*1024)))
					s.updateJobDetail(jobID, PhaseDatabase, prog, "DB dump yazıyor: "+formatJobBytes(size), size, 0, "")
					s.appendJobLog(jobID, fmt.Sprintf("output growing size=%s elapsed=%s", formatJobBytes(size), time.Since(started).Round(time.Second)))
				}
			}
		}
	}
}

func (s *Service) createFilesTracked(jobID, namePart string, opts CreateOptions, _ int) (*models.BackupInfo, error) {
	filename := fmt.Sprintf("%s-files.tar.gz", namePart)
	fullPath := filepath.Join(s.backupDir, filename)
	completed := false
	defer func() {
		if !completed {
			_ = os.Remove(fullPath)
		}
	}()
	root := s.vhostsRoot
	if len(opts.Vhosts) > 0 {
		root += " (" + strings.Join(opts.Vhosts, ", ") + ")"
	}

	args := s.filesTarArgs(fullPath, s.backupDir, opts)
	cmdLine := "tar " + strings.Join(args, " ")

	s.setJobCommand(jobID, cmdLine)
	s.appendJobLog(jobID, fmt.Sprintf("PHASE=files root=%s exclude=%s", root, s.backupDir))
	s.appendJobLog(jobID, "EXEC: "+cmdLine)
	s.appendJobLog(jobID, fmt.Sprintf("timeout=%s watching=%s", filesTimeout, fullPath))

	ctx, cancel := context.WithTimeout(context.Background(), filesTimeout)
	defer cancel()

	done := make(chan error, 1)
	started := time.Now()
	go func() {
		_, err := executeChecked(filesTimeout, "tar", args...)
		done <- err
	}()

	ticker := time.NewTicker(1500 * time.Millisecond)
	defer ticker.Stop()

	var lastSize int64
	var lastAt time.Time
	pollN := 0

	for {
		select {
		case err := <-done:
			if err != nil {
				s.appendJobLog(jobID, "tar exit error: "+err.Error())
				return nil, fmt.Errorf("files backup failed: %w", err)
			}
			info := s.statBackup(filename, "files")
			elapsed := time.Since(started).Round(time.Second)
			s.appendJobLog(jobID, fmt.Sprintf("OK tar finished size=%s elapsed=%s", formatJobBytes(info.Size), elapsed))
			s.updateJobDetail(jobID, PhaseFiles, 88, "Dosya arşivi tamam: "+formatJobBytes(info.Size), info.Size, info.Size, "")
			completed = true
			return info, nil

		case <-ctx.Done():
			s.appendJobLog(jobID, "TIMEOUT: files backup exceeded "+filesTimeout.String())
			return nil, ctx.Err()

		case <-ticker.C:
			pollN++
			st, err := os.Stat(fullPath)
			if err != nil {
				if pollN%4 == 0 {
					s.appendJobLog(jobID, fmt.Sprintf("poll #%d: output not created yet (%v elapsed)", pollN, time.Since(started).Round(time.Second)))
				}
				continue
			}
			size := st.Size()
			delta := int64(0)
			speed := ""
			if !lastAt.IsZero() && size >= lastSize {
				delta = size - lastSize
				dt := time.Since(lastAt).Seconds()
				if dt > 0 && delta > 0 {
					speed = formatJobSpeed(float64(delta) / dt)
				}
			}
			lastSize = size
			lastAt = time.Now()
			prog := 20 + min(65, int(size/(50*1024*1024))) // +1% per 50MB
			elapsed := time.Since(started).Round(time.Second)
			msg := fmt.Sprintf("tar yazıyor: %s (%v)", formatJobBytes(size), elapsed)
			s.updateJobDetail(jobID, PhaseFiles, prog, msg, size, 0, speed)
			s.appendJobLog(jobID, fmt.Sprintf("poll #%d size=%s +%s speed=%s elapsed=%s",
				pollN, formatJobBytes(size), formatJobBytes(delta), speed, elapsed))
		}
	}
}

func (s *Service) createFullTracked(jobID, namePart string, opts CreateOptions, level int) (*models.BackupInfo, error) {
	s.appendJobLog(jobID, "PHASE=full backup pipeline start")
	partialPaths := s.fullPartialPaths(namePart, opts)
	filename := fmt.Sprintf("%s-full.tar.gz", namePart)
	fullPath := filepath.Join(s.backupDir, filename)
	completed := false
	defer func() {
		for _, path := range partialPaths {
			if err := os.Remove(path); err == nil {
				s.appendJobLog(jobID, "cleanup partial: "+path)
			} else if !os.IsNotExist(err) {
				s.appendJobLog(jobID, "WARN cleanup partial failed: "+err.Error())
			}
		}
		if !completed {
			_ = os.Remove(fullPath)
		}
	}()

	s.updateJob(jobID, PhaseDatabase, 10, "Veritabanı aşaması…")
	dbInfo, dbErr := s.createDatabaseTracked(jobID, namePart+"-partial", opts, level)
	if dbErr != nil {
		s.appendJobLog(jobID, "WARN database partial failed: "+dbErr.Error())
	} else if dbInfo != nil {
		s.appendJobLog(jobID, "partial db: "+dbInfo.Path)
	}

	s.updateJob(jobID, PhaseFiles, 38, "Dosya aşaması…")
	filesInfo, filesErr := s.createFilesTracked(jobID, namePart+"-partial", opts, level)
	if filesErr != nil {
		s.appendJobLog(jobID, "WARN files partial failed: "+filesErr.Error())
	} else if filesInfo != nil {
		s.appendJobLog(jobID, "partial files: "+filesInfo.Path)
	}

	s.updateJob(jobID, PhaseArchive, 78, "Tam arşiv birleştiriliyor…")
	if dbErr != nil || dbInfo == nil || filesErr != nil || filesInfo == nil {
		var errs []string
		if dbErr != nil {
			errs = append(errs, "database: "+dbErr.Error())
		} else if dbInfo == nil {
			errs = append(errs, "database: output missing")
		}
		if filesErr != nil {
			errs = append(errs, "files: "+filesErr.Error())
		} else if filesInfo == nil {
			errs = append(errs, "files: output missing")
		}
		return nil, fmt.Errorf("full backup failed — both database and files are required: %s", strings.Join(errs, "; "))
	}

	parts := []string{dbInfo.Path, filesInfo.Path}
	args := fullArchiveArgs(fullPath, s.backupDir, parts)
	cmdLine := "tar " + strings.Join(args, " ")
	s.setJobCommand(jobID, cmdLine)
	s.appendJobLog(jobID, "EXEC: "+cmdLine)

	if _, err := executeChecked(filesTimeout, "tar", args...); err != nil {
		s.appendJobLog(jobID, "archive error: "+err.Error())
		return nil, fmt.Errorf("full backup archive failed: %w", err)
	}
	info := s.statBackup(filename, "full")
	if info.Status != "completed" {
		return nil, fmt.Errorf("full backup archive not created at %s", fullPath)
	}
	s.appendJobLog(jobID, fmt.Sprintf("OK full archive size=%s", formatJobBytes(info.Size)))

	if opts.RetentionCount > 0 {
		s.updateJob(jobID, PhaseRetention, 94, "Eski yedekler temizleniyor…")
		s.appendJobLog(jobID, fmt.Sprintf("retention keep=%d type=full", opts.RetentionCount))
		s.applyRetention("full", opts.RetentionCount) //nolint:errcheck
	}
	completed = true
	return info, nil
}
