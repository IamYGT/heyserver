package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/IamYGT/heyserver/internal/agenthub"
)

// Managed profiles are deliberately a small, versioned local contract.  The
// profile is not an environment-file overlay supplied by the hub: it is a
// strict wrapper containing only the seven fields in agenthub.AgentProfile.
const (
	profileSchemaVersion     = 1
	maxProfileDocumentBytes  = 16 << 10
	profileDirectoryName     = "profile"
	profileCandidateFileName = "candidate.json"
	profileActiveFileName    = "active.json"
	profilePreviousFileName  = "previous.json"
	profileStateFileName     = "state.json"
	// Heartbeat state names are part of the additive agenthub contract.  The
	// local state file still uses profileStateActive ("active"), but the wire
	// observation uses the hub's "applied" value.
	profileObservationActive   = agenthub.ProfileObservationApplied
	profileObservationPending  = agenthub.ProfileObservationPendingRestart
	profileObservationFailed   = agenthub.ProfileObservationFailed
	profileObservationMissing  = agenthub.ProfileObservationNotConfigured
	profileStatePendingRestart = "pending_restart"
	profileStateActive         = "active"
	profileStateFailed         = "failed"
)

var (
	errProfileMissing       = errors.New("profile is not installed")
	errProfileCorrupt       = errors.New("profile state is corrupt")
	errProfileStateCorrupt  = errors.New("profile state metadata is corrupt")
	errProfileRevision      = errors.New("profile revision is invalid")
	errProfilePayload       = errors.New("profile payload is invalid")
	errProfilePayloadTooBig = errors.New("profile payload exceeds the size limit")
)

// profileWrapper is the on-disk and task payload envelope.  Keep the JSON
// names explicit because this wrapper is also the boundary used by the
// detached lifecycle installer.
type profileWrapper struct {
	SchemaVersion int                   `json:"schema_version"`
	Revision      int64                 `json:"revision"`
	Profile       agenthub.AgentProfile `json:"profile"`
}

// profileStateDocument is intentionally separate from profileWrapper.  The
// state file records the lifecycle transition while candidate/active/previous
// files carry the actual fixed profile.
type profileStateDocument struct {
	SchemaVersion int    `json:"schema_version"`
	State         string `json:"state"`
	Revision      int64  `json:"revision"`
	ErrorCode     string `json:"error_code,omitempty"`
}

// profileObservation is the additive heartbeat observation contract owned by
// agenthub. A nil HeartbeatRequest.Profile keeps the legacy wire shape when
// local profile apply readiness is absent.
type profileObservation = agenthub.AgentProfileObservation

type profileStore struct {
	mu       sync.Mutex
	stateDir string
	readFile func(string) ([]byte, error)
}

func newProfileStore(stateDir string) *profileStore {
	return &profileStore{stateDir: stateDir, readFile: os.ReadFile}
}

func newProfileStoreWithReader(stateDir string, readFile func(string) ([]byte, error)) *profileStore {
	if readFile == nil {
		readFile = os.ReadFile
	}
	return &profileStore{stateDir: stateDir, readFile: readFile}
}

func (s *profileStore) directory() string {
	return filepath.Join(s.stateDir, profileDirectoryName)
}

func (s *profileStore) candidatePath() string {
	return filepath.Join(s.directory(), profileCandidateFileName)
}

func (s *profileStore) activePath() string {
	return filepath.Join(s.directory(), profileActiveFileName)
}

func (s *profileStore) previousPath() string {
	return filepath.Join(s.directory(), profilePreviousFileName)
}

func (s *profileStore) statePath() string {
	return filepath.Join(s.directory(), profileStateFileName)
}

// ensureDirectory creates the profile directory with the permissions expected
// from a root-owned system service.  The ownership call is only needed when
// the process is root (the normal service mode); this keeps unit tests and
// direct development launches usable under an unprivileged account.
func (s *profileStore) ensureDirectory() error {
	if s.stateDir == "" || !filepath.IsAbs(s.stateDir) || filepath.Clean(s.stateDir) != s.stateDir {
		return errors.New("profile state directory is invalid")
	}
	if err := ensurePrivateDirectory(s.stateDir); err != nil {
		return fmt.Errorf("prepare profile state directory: %w", err)
	}
	if err := ensurePrivateDirectory(s.directory()); err != nil {
		return fmt.Errorf("prepare profile directory: %w", err)
	}
	return nil
}

func ensurePrivateDirectory(directory string) error {
	if filepath.Clean(directory) == string(filepath.Separator) {
		return errors.New("refusing to use the filesystem root as profile storage")
	}
	if info, err := os.Lstat(directory); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("directory must not be a symlink")
		}
		if !info.IsDir() {
			return errors.New("path is not a directory")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("directory is not a private directory")
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return err
	}
	if os.Geteuid() == 0 {
		if err := os.Chown(directory, 0, 0); err != nil {
			return err
		}
	}
	return nil
}

// writeAtomicProfileFile publishes one state file only after its bytes have
// reached disk.  The temporary file lives beside the destination, so rename
// is atomic on the same filesystem; syncing the parent directory persists the
// directory entry as well.
func writeAtomicProfileFile(filename string, data []byte) error {
	directory := filepath.Dir(filename)
	if err := ensurePrivateDirectory(directory); err != nil {
		return err
	}
	if info, err := os.Lstat(filename); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("profile file must not be a symlink")
		}
		if !info.Mode().IsRegular() {
			return errors.New("profile path is not a regular file")
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	temporary, err := os.CreateTemp(directory, ".hserver-profile-*")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	removeTemporary := true
	defer func() {
		_ = temporary.Close()
		if removeTemporary {
			_ = os.Remove(temporaryName)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return err
	}
	if os.Geteuid() == 0 {
		if err := temporary.Chown(0, 0); err != nil {
			return err
		}
	}
	if _, err := temporary.Write(data); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryName, filename); err != nil {
		return err
	}
	removeTemporary = false
	if err := syncProfileDirectory(directory); err != nil {
		return err
	}
	return nil
}

func syncProfileDirectory(directory string) error {
	file, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}

func marshalProfileWrapper(wrapper profileWrapper) ([]byte, error) {
	if wrapper.SchemaVersion != profileSchemaVersion || wrapper.Revision <= 0 {
		return nil, errProfileRevision
	}
	normalized, err := agenthub.NormalizeAgentProfile(wrapper.Profile)
	if err != nil {
		return nil, err
	}
	wrapper.Profile = normalized
	data, err := json.Marshal(wrapper)
	if err != nil {
		return nil, err
	}
	if len(data) > maxProfileDocumentBytes {
		return nil, errProfilePayloadTooBig
	}
	return data, nil
}

// decodeProfileWrapper enforces the exact wrapper key set and validates the
// profile through AgentProfile's strict JSON decoder.  A token decoder is used
// for the outer object so duplicate keys are not silently accepted.
func decodeProfileWrapper(data []byte) (profileWrapper, error) {
	if len(data) == 0 || len(data) > maxProfileDocumentBytes {
		return profileWrapper{}, errProfilePayloadTooBig
	}
	fields, err := strictJSONObject(data)
	if err != nil {
		return profileWrapper{}, err
	}
	for field := range fields {
		switch field {
		case "schema_version", "revision", "profile":
		default:
			return profileWrapper{}, fmt.Errorf("unknown profile wrapper field %q", field)
		}
	}
	schemaRaw, ok := fields["schema_version"]
	if !ok || bytes.Equal(bytes.TrimSpace(schemaRaw), []byte("null")) {
		return profileWrapper{}, errors.New("profile wrapper schema_version is required")
	}
	var schema int
	if err := json.Unmarshal(schemaRaw, &schema); err != nil || schema != profileSchemaVersion {
		return profileWrapper{}, errors.New("unsupported profile wrapper schema_version")
	}
	revisionRaw, ok := fields["revision"]
	if !ok || bytes.Equal(bytes.TrimSpace(revisionRaw), []byte("null")) {
		return profileWrapper{}, errProfileRevision
	}
	var revision int64
	if err := json.Unmarshal(revisionRaw, &revision); err != nil || revision <= 0 {
		return profileWrapper{}, errProfileRevision
	}
	profileRaw, ok := fields["profile"]
	if !ok || bytes.Equal(bytes.TrimSpace(profileRaw), []byte("null")) {
		return profileWrapper{}, errors.New("profile wrapper profile is required")
	}
	var profile agenthub.AgentProfile
	if err := json.Unmarshal(profileRaw, &profile); err != nil {
		return profileWrapper{}, err
	}
	normalized, err := agenthub.NormalizeAgentProfile(profile)
	if err != nil {
		return profileWrapper{}, err
	}
	wrapper := profileWrapper{SchemaVersion: profileSchemaVersion, Revision: revision, Profile: normalized}
	// The hub and the detached installer exchange one canonical byte form.
	// Accepting alternate whitespace/orderings here would make the same
	// revision have multiple durable representations and would diverge from
	// the task admission contract.
	canonical, err := marshalProfileWrapper(wrapper)
	if err != nil {
		return profileWrapper{}, err
	}
	if !bytes.Equal(data, canonical) {
		return profileWrapper{}, errors.New("profile wrapper is not canonical")
	}
	return wrapper, nil
}

func strictJSONObject(data []byte) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delim, ok := token.(json.Delim)
	if !ok || delim != '{' {
		return nil, errors.New("profile wrapper must be a JSON object")
	}
	fields := make(map[string]json.RawMessage)
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, ok := keyToken.(string)
		if !ok {
			return nil, errors.New("profile wrapper key is invalid")
		}
		if _, exists := fields[key]; exists {
			return nil, fmt.Errorf("duplicate profile wrapper field %q", key)
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return nil, err
		}
		fields[key] = raw
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("profile wrapper has trailing JSON")
		}
		return nil, err
	}
	return fields, nil
}

func (s *profileStore) readWrapper(filename string) (profileWrapper, bool, error) {
	if s.readFile == nil {
		s.readFile = os.ReadFile
	}
	// Avoid invoking injected readers for a missing path.  Existing config
	// tests provide a token-only reader; this also makes absent profile state a
	// harmless legacy condition.
	info, statErr := os.Lstat(filename)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			return profileWrapper{}, false, nil
		}
		return profileWrapper{}, false, statErr
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return profileWrapper{}, true, errProfileCorrupt
	}
	data, err := s.readFile(filename)
	if err != nil {
		return profileWrapper{}, true, err
	}
	if len(data) > maxProfileDocumentBytes {
		return profileWrapper{}, true, errProfilePayloadTooBig
	}
	wrapper, err := decodeProfileWrapper(data)
	if err != nil {
		return profileWrapper{}, true, err
	}
	return wrapper, true, nil
}

func (s *profileStore) readState() (profileStateDocument, bool, error) {
	info, statErr := os.Lstat(s.statePath())
	if statErr != nil {
		if os.IsNotExist(statErr) {
			return profileStateDocument{}, false, nil
		}
		return profileStateDocument{}, false, statErr
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return profileStateDocument{}, true, errProfileStateCorrupt
	}
	data, err := s.readFile(s.statePath())
	if err != nil {
		return profileStateDocument{}, true, err
	}
	if len(data) > maxProfileDocumentBytes {
		return profileStateDocument{}, true, errProfileStateCorrupt
	}
	var state profileStateDocument
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return profileStateDocument{}, true, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return profileStateDocument{}, true, errors.New("profile state has trailing JSON")
	}
	if state.SchemaVersion != profileSchemaVersion || state.Revision < 1 {
		return profileStateDocument{}, true, errProfileStateCorrupt
	}
	switch state.State {
	case profileStatePendingRestart, profileStateActive, profileStateFailed:
	default:
		return profileStateDocument{}, true, errProfileStateCorrupt
	}
	if state.State != profileStateFailed && state.ErrorCode != "" {
		return profileStateDocument{}, true, errProfileStateCorrupt
	}
	if state.ErrorCode != "" && safeProfileErrorCode(state.ErrorCode, "") != state.ErrorCode {
		return profileStateDocument{}, true, errProfileStateCorrupt
	}
	canonical, err := json.Marshal(state)
	if err != nil {
		return profileStateDocument{}, true, errProfileStateCorrupt
	}
	if !bytes.Equal(data, canonical) && !bytes.Equal(data, append(canonical, '\n')) {
		return profileStateDocument{}, true, errProfileStateCorrupt
	}
	return state, true, nil
}

func (s *profileStore) writeWrapper(filename string, wrapper profileWrapper) error {
	data, err := marshalProfileWrapper(wrapper)
	if err != nil {
		return err
	}
	return writeAtomicProfileFile(filename, data)
}

func (s *profileStore) writeState(state profileStateDocument) error {
	if state.SchemaVersion == 0 {
		state.SchemaVersion = profileSchemaVersion
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if len(data) > maxProfileDocumentBytes {
		return errProfileStateCorrupt
	}
	return writeAtomicProfileFile(s.statePath(), append(data, '\n'))
}

func (s *profileStore) loadActiveProfile() (profileWrapper, profileObservation, error) {
	active, exists, err := s.readWrapper(s.activePath())
	if err != nil {
		return profileWrapper{}, profileObservation{State: profileObservationFailed, ErrorCode: profileErrorCorrupt}, errProfileCorrupt
	}
	state, stateExists, stateErr := s.readState()
	if stateErr != nil {
		return active, profileObservation{State: profileObservationFailed, Revision: active.Revision, ErrorCode: profileErrorStateCorrupt}, stateErr
	}
	if !exists {
		if !stateExists {
			return profileWrapper{}, profileObservation{State: profileObservationMissing}, errProfileMissing
		}
		switch state.State {
		case profileStatePendingRestart:
			return profileWrapper{}, profileObservation{State: profileObservationPending, Revision: state.Revision}, nil
		case profileStateFailed:
			return profileWrapper{}, profileObservation{State: profileObservationFailed, Revision: state.Revision, ErrorCode: safeProfileErrorCode(state.ErrorCode, profileErrorStore)}, nil
		case profileStateActive:
			return profileWrapper{}, profileObservation{State: profileObservationFailed, Revision: state.Revision, ErrorCode: profileErrorStateCorrupt}, errProfileStateCorrupt
		}
	}
	if stateExists {
		switch state.State {
		case profileStatePendingRestart:
			return active, profileObservation{State: profileObservationPending, Revision: state.Revision}, nil
		case profileStateFailed:
			return active, profileObservation{State: profileObservationFailed, Revision: state.Revision, ErrorCode: safeProfileErrorCode(state.ErrorCode, profileErrorStore)}, nil
		case profileStateActive:
			if state.Revision != active.Revision {
				return active, profileObservation{State: profileObservationFailed, Revision: active.Revision, ErrorCode: profileErrorStateCorrupt}, errProfileStateCorrupt
			}
			return active, profileObservation{State: profileObservationActive, Revision: active.Revision}, nil
		}
	}
	return active, profileObservation{State: profileObservationActive, Revision: active.Revision}, nil
}

func safeProfileErrorCode(code, fallback string) string {
	switch code {
	case profileErrorMissing, profileErrorCorrupt, profileErrorStateCorrupt, profileErrorRevision,
		profileErrorPayload, profileErrorPayloadTooLarge, profileErrorNotReady, profileErrorSchedule,
		profileErrorStore, profileErrorSuperseded:
		return code
	default:
		return fallback
	}
}

// applyProfileOverlay copies only the seven fixed profile fields.  All other
// agent configuration (hub identity, token, lifecycle paths, and unrelated
// capabilities) remains local environment configuration.
func applyProfileOverlay(cfg *config, profile agenthub.AgentProfile) {
	cfg.allowDeployRead = profile.AllowDeployRead
	cfg.allowDeployActions = profile.AllowDeployActions
	cfg.allowDeployDomainRead = profile.AllowDeployDomainRead
	cfg.allowDeployDomainActions = profile.AllowDeployDomainActions
	cfg.deployPlansPath = profile.DeployPlansFile
	cfg.deployACMEWebroot = profile.DeployAcmeWebroot
	cfg.deployWriteRoots = append([]string(nil), profile.DeployWriteRoots...)
}

// attachProfileObservation keeps the wire field additive. Callers gate this
// helper on local installer/systemd-run readiness so legacy agents omit it.
func attachProfileObservation(request *agenthub.HeartbeatRequest, observation profileObservation) {
	if request == nil {
		return
	}
	request.Profile = &agenthub.AgentProfileObservation{
		State:     observation.State,
		Revision:  observation.Revision,
		ErrorCode: observation.ErrorCode,
	}
}
