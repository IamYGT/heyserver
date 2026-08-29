package bind

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

type fileSnapshot struct {
	content []byte
	mode    os.FileMode
	uid     int
	gid     int
}

// applyZoneFileTransaction validates replacement content from a same-directory
// temporary file, atomically replaces the live file, and restores the original
// file when the optional runtime reload fails.
func applyZoneFileTransaction(
	zoneFile string,
	content []byte,
	validate func(candidatePath string) error,
	reload func() error,
) error {
	snapshot, err := snapshotRegularFile(zoneFile)
	if err != nil {
		return err
	}

	candidate, err := stageFile(zoneFile, content, snapshot)
	if err != nil {
		return fmt.Errorf("staging zone file: %w", err)
	}
	defer func() { _ = os.Remove(candidate) }()

	if validate != nil {
		if err := validate(candidate); err != nil {
			return fmt.Errorf("staged zone validation failed: %w", err)
		}
	}

	if err := commitStagedFile(candidate, zoneFile); err != nil {
		return fmt.Errorf("committing zone file: %w", err)
	}

	if reload == nil {
		return nil
	}
	if err := reload(); err == nil {
		return nil
	} else {
		reloadErr := err
		if rollbackErr := restoreFile(zoneFile, snapshot); rollbackErr != nil {
			return fmt.Errorf("zone reload failed: %v; disk rollback failed: %w", reloadErr, rollbackErr)
		}
		if rollbackReloadErr := reload(); rollbackReloadErr != nil {
			return fmt.Errorf("zone reload failed: %v; original zone file restored but runtime rollback reload failed: %w", reloadErr, rollbackReloadErr)
		}
		return fmt.Errorf("zone reload failed: %w; original zone file restored and reloaded", reloadErr)
	}
}

func snapshotRegularFile(path string) (fileSnapshot, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return fileSnapshot{}, fmt.Errorf("reading zone file metadata: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fileSnapshot{}, fmt.Errorf("zone file %s is not a regular file", path)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return fileSnapshot{}, fmt.Errorf("reading zone file: %w", err)
	}

	snapshot := fileSnapshot{content: content, mode: info.Mode().Perm(), uid: -1, gid: -1}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		snapshot.uid = int(stat.Uid)
		snapshot.gid = int(stat.Gid)
	}
	return snapshot, nil
}

func stageFile(target string, content []byte, snapshot fileSnapshot) (path string, err error) {
	dir := filepath.Dir(target)
	file, err := os.CreateTemp(dir, "."+filepath.Base(target)+".hserver-*")
	if err != nil {
		return "", err
	}
	path = file.Name()
	defer func() {
		if closeErr := file.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
		if err != nil {
			_ = os.Remove(path)
		}
	}()

	if err = file.Chmod(snapshot.mode); err != nil {
		return "", err
	}
	if snapshot.uid >= 0 && snapshot.gid >= 0 {
		if err = file.Chown(snapshot.uid, snapshot.gid); err != nil {
			return "", err
		}
	}
	if _, err = file.Write(content); err != nil {
		return "", err
	}
	if err = file.Sync(); err != nil {
		return "", err
	}
	return path, nil
}

func commitStagedFile(candidate, target string) error {
	if err := os.Rename(candidate, target); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(target))
}

func restoreFile(target string, snapshot fileSnapshot) error {
	candidate, err := stageFile(target, snapshot.content, snapshot)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(candidate) }()
	return commitStagedFile(candidate, target)
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	return dir.Sync()
}
