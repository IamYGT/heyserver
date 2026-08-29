package bind

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

const (
	lifecycleJournalVersion = 1
	lifecycleJournalMaxSize = 16 << 20
)

type lifecycleOperation string
type lifecycleStage string

const (
	operationCreate lifecycleOperation = "create"
	operationDelete lifecycleOperation = "delete"

	stagePrepared        lifecycleStage = "prepared"
	stageZoneCommitted   lifecycleStage = "zone-committed"
	stageConfigCommitted lifecycleStage = "config-committed"
	stageReloaded        lifecycleStage = "reloaded"
)

type journalFileSnapshot struct {
	Content []byte `json:"content"`
	Mode    uint32 `json:"mode"`
	UID     int    `json:"uid"`
	GID     int    `json:"gid"`
}

type lifecycleJournal struct {
	Version       int                  `json:"version"`
	Operation     lifecycleOperation   `json:"operation"`
	Stage         lifecycleStage       `json:"stage"`
	ConfigPath    string               `json:"configPath"`
	ZonePath      string               `json:"zonePath"`
	TombstonePath string               `json:"tombstonePath,omitempty"`
	Config        journalFileSnapshot  `json:"config"`
	Zone          *journalFileSnapshot `json:"zone,omitempty"`
	CreatedAt     time.Time            `json:"createdAt"`
}

type lifecycleJournalStore struct {
	dir  string
	path string
}

func newLifecycleJournalStore(dataDir string) *lifecycleJournalStore {
	dir := filepath.Join(dataDir, "bind")
	return &lifecycleJournalStore{
		dir:  dir,
		path: filepath.Join(dir, "lifecycle-transaction.json"),
	}
}

func (s *lifecycleJournalStore) prepareCreate(configPath, zonePath string, config fileSnapshot) error {
	if err := s.requireVacant(); err != nil {
		return err
	}
	return s.write(lifecycleJournal{
		Version:    lifecycleJournalVersion,
		Operation:  operationCreate,
		Stage:      stagePrepared,
		ConfigPath: configPath,
		ZonePath:   zonePath,
		Config:     snapshotToJournal(config),
		CreatedAt:  time.Now().UTC(),
	})
}

func (s *lifecycleJournalStore) prepareDelete(configPath, zonePath, tombstonePath string, config, zone fileSnapshot) error {
	if err := s.requireVacant(); err != nil {
		return err
	}
	zoneRecord := snapshotToJournal(zone)
	return s.write(lifecycleJournal{
		Version:       lifecycleJournalVersion,
		Operation:     operationDelete,
		Stage:         stagePrepared,
		ConfigPath:    configPath,
		ZonePath:      zonePath,
		TombstonePath: tombstonePath,
		Config:        snapshotToJournal(config),
		Zone:          &zoneRecord,
		CreatedAt:     time.Now().UTC(),
	})
}

func (s *lifecycleJournalStore) requireVacant() error {
	exists, err := s.exists()
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("a BIND lifecycle transaction is already pending recovery")
	}
	return nil
}

func (s *lifecycleJournalStore) updateStage(stage lifecycleStage) error {
	journal, err := s.load()
	if err != nil {
		return err
	}
	journal.Stage = stage
	return s.write(journal)
}

func (s *lifecycleJournalStore) load() (lifecycleJournal, error) {
	info, err := os.Lstat(s.path)
	if err != nil {
		return lifecycleJournal{}, err
	}
	if !info.Mode().IsRegular() {
		return lifecycleJournal{}, fmt.Errorf("BIND lifecycle journal is not a regular file")
	}
	if info.Mode().Perm()&0o177 != 0 {
		return lifecycleJournal{}, fmt.Errorf("BIND lifecycle journal permissions must be 0600 or stricter")
	}
	if info.Size() > lifecycleJournalMaxSize {
		return lifecycleJournal{}, fmt.Errorf("BIND lifecycle journal exceeds %d bytes", lifecycleJournalMaxSize)
	}

	file, err := os.Open(s.path)
	if err != nil {
		return lifecycleJournal{}, err
	}
	defer func() { _ = file.Close() }()
	var journal lifecycleJournal
	decoder := json.NewDecoder(io.LimitReader(file, lifecycleJournalMaxSize+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&journal); err != nil {
		return lifecycleJournal{}, fmt.Errorf("decoding BIND lifecycle journal: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return lifecycleJournal{}, fmt.Errorf("decoding BIND lifecycle journal: trailing JSON value")
		}
		return lifecycleJournal{}, fmt.Errorf("decoding BIND lifecycle journal: trailing content: %w", err)
	}
	if err := validateLifecycleJournal(journal); err != nil {
		return lifecycleJournal{}, err
	}
	return journal, nil
}

func (s *lifecycleJournalStore) exists() (bool, error) {
	_, err := os.Lstat(s.path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func (s *lifecycleJournalStore) clear() error {
	if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
		return err
	} else if os.IsNotExist(err) {
		return nil
	}
	return syncDirectory(s.dir)
}

func (s *lifecycleJournalStore) write(journal lifecycleJournal) error {
	if err := validateLifecycleJournal(journal); err != nil {
		return err
	}
	if err := ensureProtectedJournalDir(s.dir); err != nil {
		return err
	}
	content, err := json.Marshal(journal)
	if err != nil {
		return err
	}
	if len(content) > lifecycleJournalMaxSize {
		return fmt.Errorf("BIND lifecycle journal exceeds %d bytes", lifecycleJournalMaxSize)
	}

	file, err := os.CreateTemp(s.dir, ".lifecycle-transaction-*")
	if err != nil {
		return err
	}
	candidate := file.Name()
	cleanup := true
	defer func() {
		_ = file.Close()
		if cleanup {
			_ = os.Remove(candidate)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		return err
	}
	if _, err := file.Write(content); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(candidate, s.path); err != nil {
		return err
	}
	cleanup = false
	return syncDirectory(s.dir)
}

func ensureProtectedJournalDir(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("BIND lifecycle journal directory is not a regular directory")
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return err
	}
	return nil
}

func validateLifecycleJournal(journal lifecycleJournal) error {
	if journal.Version != lifecycleJournalVersion {
		return fmt.Errorf("unsupported BIND lifecycle journal version %d", journal.Version)
	}
	if journal.Operation != operationCreate && journal.Operation != operationDelete {
		return fmt.Errorf("unsupported BIND lifecycle operation %q", journal.Operation)
	}
	switch journal.Stage {
	case stagePrepared, stageZoneCommitted, stageConfigCommitted, stageReloaded:
	default:
		return fmt.Errorf("unsupported BIND lifecycle stage %q", journal.Stage)
	}
	for label, path := range map[string]string{"config": journal.ConfigPath, "zone": journal.ZonePath} {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return fmt.Errorf("BIND lifecycle %s path must be absolute and normalized", label)
		}
	}
	if journal.Operation == operationDelete {
		if journal.Zone == nil {
			return fmt.Errorf("BIND delete journal is missing the zone snapshot")
		}
		if !filepath.IsAbs(journal.TombstonePath) || filepath.Clean(journal.TombstonePath) != journal.TombstonePath {
			return fmt.Errorf("BIND lifecycle tombstone path must be absolute and normalized")
		}
	}
	return nil
}

func snapshotToJournal(snapshot fileSnapshot) journalFileSnapshot {
	return journalFileSnapshot{
		Content: snapshot.content,
		Mode:    uint32(snapshot.mode.Perm()),
		UID:     snapshot.uid,
		GID:     snapshot.gid,
	}
}

func snapshotFromJournal(snapshot journalFileSnapshot) fileSnapshot {
	return fileSnapshot{
		content: snapshot.Content,
		mode:    os.FileMode(snapshot.Mode).Perm(),
		uid:     snapshot.UID,
		gid:     snapshot.GID,
	}
}
