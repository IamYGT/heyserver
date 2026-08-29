package agenthub

import (
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Task status values persisted by the hub. A task is claimable only while it
// is queued; the conditional updates below make the state transition atomic.
const (
	TaskStatusQueued    = "queued"
	TaskStatusRunning   = "running"
	TaskStatusCompleted = "completed"
	TaskStatusFailed    = "failed"
)

var (
	// ErrNotFound deliberately does not distinguish a missing node/task from a
	// resource that the caller is not allowed to observe.
	ErrNotFound = errors.New("agent hub: record not found")
	// ErrAlreadyExists is returned when a node ID is already registered.
	ErrAlreadyExists = errors.New("agent hub: record already exists")
	// ErrUnauthorized is returned for an unknown node or invalid bearer token.
	ErrUnauthorized = errors.New("agent hub: unauthorized")
	// ErrInvalidInput identifies a request that violates the v1 contract.
	ErrInvalidInput = errors.New("agent hub: invalid input")
	// ErrUnsupportedProtocol identifies a heartbeat from a protocol we do not
	// understand. It is separate so the HTTP layer can return a useful 400.
	ErrUnsupportedProtocol = errors.New("agent hub: unsupported protocol version")
	// ErrStaleHeartbeat identifies a heartbeat outside the accepted freshness
	// window.
	ErrStaleHeartbeat = errors.New("agent hub: stale heartbeat")
	// ErrTaskNotClaimed prevents a task from being completed before this node
	// has successfully claimed it, or from being completed twice.
	ErrTaskNotClaimed = errors.New("agent hub: task was not claimed")
	// ErrCapabilityUnavailable prevents the panel from queuing work the node
	// did not explicitly advertise in its last accepted heartbeat.
	ErrCapabilityUnavailable = errors.New("agent hub: capability unavailable")
	// ErrNodeOffline prevents the panel from accumulating desired work after a
	// node has stopped reporting server-observed heartbeats.
	ErrNodeOffline = errors.New("agent hub: node offline")
)

const (
	maxNodeIDLength           = 128
	maxNodeNameLength         = 255
	maxHostnameLength         = 255
	maxAgentVersionLength     = 128
	maxHeartbeatInventorySize = 64 << 10
	maxTaskPayloadValueLength = 255
	maxTaskErrorLength        = 4096
	maxTaskResultValueLength  = 5 << 20
	maxNginxConfigBytes       = 2 << 20
	maxEncodedNginxConfigSize = ((maxNginxConfigBytes + 2) / 3) * 4
	maxManagedPathLength      = 4096
	maxCronJobBytes           = 8 << 10
	maxEncodedCronJobSize     = ((maxCronJobBytes + 2) / 3) * 4
	maxFirewallRuleBytes      = 1 << 10
	maxEncodedFirewallSize    = ((maxFirewallRuleBytes + 2) / 3) * 4
)

// Repository owns the SQLite persistence boundary for the agent hub.
// Authentication and request policy live in Service; this type only stores
// already validated values and performs atomic state transitions.
type Repository struct {
	db *sql.DB
}

// NewRepository wraps an existing application SQLite connection.
func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

// Migrate creates the agent hub tables and indexes. Every statement is
// idempotent so startup and focused tests can safely call it more than once.
func Migrate(db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("agent hub: migrate: %w", ErrInvalidInput)
	}
	// The production connection already enables this, but enabling it here
	// keeps the repository safe when it is used with a plain sqlite3 DSN.
	if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		return fmt.Errorf("agent hub: enable foreign keys: %w", err)
	}

	stmts := []string{
		`CREATE TABLE IF NOT EXISTS agent_nodes (
			id               TEXT PRIMARY KEY,
			name             TEXT NOT NULL,
			hostname         TEXT NOT NULL DEFAULT '',
			agent_version    TEXT NOT NULL DEFAULT '',
			protocol_version TEXT NOT NULL DEFAULT '',
			capabilities_json TEXT NOT NULL DEFAULT '[]',
			inventory_json   TEXT NOT NULL DEFAULT '{}',
			token_hash       TEXT NOT NULL,
			last_seen_at     TEXT,
			created_at       TEXT NOT NULL,
			updated_at       TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS agent_tasks (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			node_id      TEXT NOT NULL REFERENCES agent_nodes(id) ON DELETE CASCADE,
			kind         TEXT NOT NULL,
			payload_json TEXT NOT NULL DEFAULT '{}',
			status       TEXT NOT NULL DEFAULT 'queued'
			             CHECK(status IN ('queued', 'running', 'completed', 'failed')),
			result_json  TEXT NOT NULL DEFAULT '{}',
			error        TEXT NOT NULL DEFAULT '',
			created_at   TEXT NOT NULL,
			started_at   TEXT,
			completed_at TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS agent_node_profiles (
			node_id      TEXT PRIMARY KEY REFERENCES agent_nodes(id) ON DELETE CASCADE,
			profile_json TEXT NOT NULL DEFAULT '{}',
			revision     INTEGER NOT NULL DEFAULT 0 CHECK(revision >= 0),
			updated_at   TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS agent_node_profile_observations (
			node_id      TEXT PRIMARY KEY REFERENCES agent_nodes(id) ON DELETE CASCADE,
			state        TEXT NOT NULL CHECK(state IN ('not_configured', 'pending_restart', 'applied', 'failed')),
			revision     INTEGER NOT NULL DEFAULT 0 CHECK(revision >= 0),
			error_code   TEXT NOT NULL DEFAULT '' CHECK(error_code IN ('', 'not_configured', 'invalid_profile', 'permission_denied', 'write_failed', 'restart_failed', 'apply_failed', 'profile_apply_failed', 'unsupported', 'not_supported', 'timeout', 'stale_revision', 'invalid_revision', 'agent_error', 'unknown', 'profile_missing', 'profile_corrupt', 'profile_state_corrupt', 'profile_revision_invalid', 'profile_payload_invalid', 'profile_payload_too_large', 'profile_apply_unavailable', 'profile_schedule_failed', 'profile_store_failed', 'profile_superseded')),
			updated_at   TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_tasks_node_status_id
			ON agent_tasks(node_id, status, id)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_tasks_node_created
			ON agent_tasks(node_id, created_at DESC, id DESC)`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("agent hub: migrate: %w", err)
		}
	}
	if err := ensureAgentNodeCapabilitiesColumn(db); err != nil {
		return err
	}
	return nil
}

func ensureAgentNodeCapabilitiesColumn(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(agent_nodes)`)
	if err != nil {
		return fmt.Errorf("agent hub: inspect node columns: %w", err)
	}
	defer func() { _ = rows.Close() }()
	hasColumn := false
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return fmt.Errorf("agent hub: inspect node column: %w", err)
		}
		if name == "capabilities_json" {
			hasColumn = true
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("agent hub: inspect node columns: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("agent hub: close node column inspection: %w", err)
	}
	if hasColumn {
		return nil
	}
	if _, err := db.Exec(`ALTER TABLE agent_nodes ADD COLUMN capabilities_json TEXT NOT NULL DEFAULT '[]'`); err != nil {
		return fmt.Errorf("agent hub: add node capabilities: %w", err)
	}
	return nil
}

// Migrate applies the schema through this repository.
func (r *Repository) Migrate() error {
	if r == nil || r.db == nil {
		return fmt.Errorf("agent hub: repository migrate: %w", ErrInvalidInput)
	}
	return Migrate(r.db)
}

// CreateNode persists a node and its SHA-256 token hash. The token itself is
// intentionally not accepted here, which keeps the persistence API unable to
// accidentally write a bearer token.
func (r *Repository) CreateNode(req RegisterNodeRequest, tokenHash string, now time.Time) (*Node, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("agent hub: create node: %w", ErrInvalidInput)
	}
	if tokenHash == "" || req.ID == "" || req.Name == "" {
		return nil, fmt.Errorf("agent hub: create node: %w", ErrInvalidInput)
	}
	stamp := formatTime(now)
	_, err := r.db.Exec(`
		INSERT INTO agent_nodes
			(id, name, token_hash, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
	`, req.ID, req.Name, tokenHash, stamp, stamp)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique constraint") {
			return nil, ErrAlreadyExists
		}
		return nil, fmt.Errorf("agent hub: create node: %w", err)
	}
	return r.GetNode(req.ID)
}

// GetNodeProfile returns the desired profile for a node. An absent profile is
// a valid revision-zero, not-configured state; a missing node remains not
// found. The profile table never stores credentials or agent tokens.
func (r *Repository) GetNodeProfile(nodeID string) (AgentProfileRecord, error) {
	if r == nil || r.db == nil {
		return AgentProfileRecord{}, fmt.Errorf("agent hub: get node profile: %w", ErrInvalidInput)
	}
	var (
		profileJSON sql.NullString
		revision    sql.NullInt64
	)
	err := r.db.QueryRow(`
		SELECT p.profile_json, p.revision
		FROM agent_nodes n
		LEFT JOIN agent_node_profiles p ON p.node_id = n.id
		WHERE n.id = ?
	`, nodeID).Scan(&profileJSON, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return AgentProfileRecord{}, ErrNotFound
	}
	if err != nil {
		return AgentProfileRecord{}, fmt.Errorf("agent hub: get node profile: %w", err)
	}
	record := AgentProfileRecord{Profile: emptyAgentProfile()}
	if !profileJSON.Valid || profileJSON.String == "" {
		return record, nil
	}
	profile, err := decodePersistedAgentProfile([]byte(profileJSON.String))
	if err != nil {
		return AgentProfileRecord{}, fmt.Errorf("agent hub: decode node profile: %w", err)
	}
	normalized, err := NormalizeAgentProfile(profile)
	if err != nil {
		return AgentProfileRecord{}, fmt.Errorf("agent hub: validate node profile: %w", err)
	}
	record.Profile = normalized
	if !revision.Valid || revision.Int64 < 1 {
		return AgentProfileRecord{}, fmt.Errorf("agent hub: invalid node profile revision")
	}
	record.Revision = revision.Int64
	record.Configured = true
	return record, nil
}

// SaveNodeProfile performs a compare-and-swap desired-profile update. The
// node existence check, revision check, and write are one transaction so a
// stale writer cannot overwrite a newer profile. Revision zero is reserved for
// an absent profile; even an all-false/empty profile is a configured row.
func (r *Repository) SaveNodeProfile(nodeID string, profile AgentProfile, expectedRevision int64, now time.Time) (AgentProfileRecord, error) {
	if r == nil || r.db == nil {
		return AgentProfileRecord{}, fmt.Errorf("agent hub: save node profile: %w", ErrInvalidInput)
	}
	if expectedRevision < 0 {
		return AgentProfileRecord{}, fmt.Errorf("agent hub: profile revision: %w", ErrInvalidInput)
	}
	normalized, err := NormalizeAgentProfile(profile)
	if err != nil {
		return AgentProfileRecord{}, err
	}
	profileJSON, err := json.Marshal(normalized)
	if err != nil {
		return AgentProfileRecord{}, fmt.Errorf("agent hub: encode node profile: %w", err)
	}

	tx, err := r.db.Begin()
	if err != nil {
		return AgentProfileRecord{}, fmt.Errorf("agent hub: begin node profile: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	var nodeExists int
	if err := tx.QueryRow(`SELECT 1 FROM agent_nodes WHERE id = ?`, nodeID).Scan(&nodeExists); errors.Is(err, sql.ErrNoRows) {
		return AgentProfileRecord{}, ErrNotFound
	} else if err != nil {
		return AgentProfileRecord{}, fmt.Errorf("agent hub: profile node lookup: %w", err)
	}

	var currentRevision sql.NullInt64
	err = tx.QueryRow(`SELECT revision FROM agent_node_profiles WHERE node_id = ?`, nodeID).Scan(&currentRevision)
	stamp := formatTime(now)
	newRevision := int64(1)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if expectedRevision != 0 {
			return AgentProfileRecord{}, ErrProfileRevisionStale
		}
		if _, _, err := canonicalProfileApplyDocument(normalized, newRevision); err != nil {
			return AgentProfileRecord{}, err
		}
		if _, err := tx.Exec(`
			INSERT INTO agent_node_profiles (node_id, profile_json, revision, updated_at)
			VALUES (?, ?, 1, ?)
		`, nodeID, string(profileJSON), stamp); err != nil {
			return AgentProfileRecord{}, fmt.Errorf("agent hub: insert node profile: %w", err)
		}
	case err != nil:
		return AgentProfileRecord{}, fmt.Errorf("agent hub: profile revision lookup: %w", err)
	default:
		if !currentRevision.Valid || currentRevision.Int64 < 1 || currentRevision.Int64 != expectedRevision {
			return AgentProfileRecord{}, ErrProfileRevisionStale
		}
		newRevision = currentRevision.Int64 + 1
		if _, _, err := canonicalProfileApplyDocument(normalized, newRevision); err != nil {
			return AgentProfileRecord{}, err
		}
		result, err := tx.Exec(`
			UPDATE agent_node_profiles
			SET profile_json = ?, revision = ?, updated_at = ?
			WHERE node_id = ? AND revision = ?
		`, string(profileJSON), newRevision, stamp, nodeID, expectedRevision)
		if err != nil {
			return AgentProfileRecord{}, fmt.Errorf("agent hub: update node profile: %w", err)
		}
		if affected, err := result.RowsAffected(); err != nil {
			return AgentProfileRecord{}, fmt.Errorf("agent hub: profile rows affected: %w", err)
		} else if affected != 1 {
			return AgentProfileRecord{}, ErrProfileRevisionStale
		}
	}

	if err := tx.Commit(); err != nil {
		return AgentProfileRecord{}, fmt.Errorf("agent hub: commit node profile: %w", err)
	}
	committed = true
	return AgentProfileRecord{Profile: normalized, Revision: newRevision, Configured: true}, nil
}

// GetNodeProfileObservation returns the last bounded agent observation. A
// missing row is not an error: it means the latest heartbeat did not report
// the profile capability/observation and is exposed as not_reported.
func (r *Repository) GetNodeProfileObservation(nodeID string) (*AgentProfileObservationRecord, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("agent hub: get profile observation: %w", ErrInvalidInput)
	}
	var (
		state, errorCode, updatedAt sql.NullString
		revision                    sql.NullInt64
	)
	err := r.db.QueryRow(`
		SELECT o.state, o.revision, o.error_code, o.updated_at
		FROM agent_nodes n
		LEFT JOIN agent_node_profile_observations o ON o.node_id = n.id
		WHERE n.id = ?
	`, nodeID).Scan(&state, &revision, &errorCode, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("agent hub: get profile observation: %w", err)
	}
	if !state.Valid {
		return nil, nil
	}
	if !revision.Valid || revision.Int64 < 0 || !updatedAt.Valid || updatedAt.String == "" {
		return nil, fmt.Errorf("agent hub: invalid profile observation")
	}
	parsed, err := parseTime(updatedAt.String)
	if err != nil {
		return nil, fmt.Errorf("agent hub: decode profile observation timestamp: %w", err)
	}
	value := &AgentProfileObservationRecord{
		State: state.String, Revision: revision.Int64, ErrorCode: errorCode.String, UpdatedAt: parsed,
	}
	if err := validateAgentProfileObservation(&AgentProfileObservation{State: value.State, Revision: value.Revision, ErrorCode: value.ErrorCode}); err != nil {
		return nil, fmt.Errorf("agent hub: validate profile observation: %w", err)
	}
	return value, nil
}

// UpsertNodeProfileObservation persists the validated observation from a
// capability-bearing heartbeat. It is intentionally separate from desired
// profile state and is deleted when an older agent omits the observation.
func (r *Repository) UpsertNodeProfileObservation(nodeID string, observation AgentProfileObservation, now time.Time) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("agent hub: save profile observation: %w", ErrInvalidInput)
	}
	if err := validateAgentProfileObservation(&observation); err != nil {
		return err
	}
	result, err := r.db.Exec(`
		INSERT INTO agent_node_profile_observations (node_id, state, revision, error_code, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(node_id) DO UPDATE SET
			state = excluded.state,
			revision = excluded.revision,
			error_code = excluded.error_code,
			updated_at = excluded.updated_at
	`, nodeID, observation.State, observation.Revision, observation.ErrorCode, formatTime(now))
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "foreign key constraint") {
			return ErrNotFound
		}
		return fmt.Errorf("agent hub: save profile observation: %w", err)
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return ErrNotFound
	}
	return nil
}

// ClearNodeProfileObservation removes the current observation so a heartbeat
// without agent.profile.apply cannot leave a stale applied state behind.
func (r *Repository) ClearNodeProfileObservation(nodeID string) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("agent hub: clear profile observation: %w", ErrInvalidInput)
	}
	result, err := r.db.Exec(`DELETE FROM agent_node_profile_observations WHERE node_id = ?`, nodeID)
	if err != nil {
		return fmt.Errorf("agent hub: clear profile observation: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("agent hub: clear profile observation rows affected: %w", err)
	} else if affected == 0 {
		// A missing observation is already the desired state, but verify that
		// the node exists so callers get a stable not-found error.
		if _, err := r.GetNode(nodeID); err != nil {
			return err
		}
	}
	return nil
}

// AuthenticateToken hashes the supplied token and returns the node only after
// the digest has been compared in constant time with the stored hash.
func (r *Repository) AuthenticateToken(nodeID, token string) (*Node, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("agent hub: authenticate: %w", ErrInvalidInput)
	}
	var storedHash string
	err := r.db.QueryRow(`SELECT token_hash FROM agent_nodes WHERE id = ?`, nodeID).Scan(&storedHash)
	if err == sql.ErrNoRows {
		return nil, ErrUnauthorized
	}
	if err != nil {
		return nil, fmt.Errorf("agent hub: authenticate: %w", err)
	}
	digest := sha256.Sum256([]byte(token))
	expectedHash := hex.EncodeToString(digest[:])
	if !constantTimeEqual(storedHash, expectedHash) {
		return nil, ErrUnauthorized
	}
	return r.GetNode(nodeID)
}

// UpdateHeartbeat persists the complete bounded node snapshot. The caller
// supplies serverNow as LastSeenAt so a stale agent timestamp cannot make a
// healthy node appear newer than the server received it.
func (r *Repository) UpdateHeartbeat(nodeID string, req HeartbeatRequest, serverNow time.Time) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("agent hub: heartbeat: %w", ErrInvalidInput)
	}
	if err := validateAgentProfileObservation(req.Profile); err != nil {
		return err
	}
	inventory, err := json.Marshal(req.Inventory)
	if err != nil {
		return fmt.Errorf("agent hub: heartbeat inventory: %w", err)
	}
	capabilities, err := json.Marshal(req.Capabilities)
	if err != nil {
		return fmt.Errorf("agent hub: heartbeat capabilities: %w", err)
	}
	stamp := formatTime(serverNow)
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("agent hub: heartbeat begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	result, err := tx.Exec(`
		UPDATE agent_nodes SET
			hostname = ?,
			agent_version = ?,
			protocol_version = ?,
			capabilities_json = ?,
			inventory_json = ?,
			last_seen_at = ?,
			updated_at = ?
		WHERE id = ?
	`, req.Hostname, req.AgentVersion, req.ProtocolVersion, string(capabilities), string(inventory), stamp, stamp, nodeID)
	if err != nil {
		return fmt.Errorf("agent hub: heartbeat: %w", err)
	}
	if n, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("agent hub: heartbeat rows affected: %w", err)
	} else if n != 1 {
		return ErrNotFound
	}
	// The observation is accepted only when the current heartbeat both
	// advertises the capability and carries the optional observation. Older
	// agents (or a capability downgrade) clear the row to prevent stale
	// applied state from surviving indefinitely.
	if hasCapability(req.Capabilities, CapabilityProfileApply) && req.Profile != nil {
		if _, err := tx.Exec(`
			INSERT INTO agent_node_profile_observations (node_id, state, revision, error_code, updated_at)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(node_id) DO UPDATE SET
				state = excluded.state,
				revision = excluded.revision,
				error_code = excluded.error_code,
				updated_at = excluded.updated_at
		`, nodeID, req.Profile.State, req.Profile.Revision, req.Profile.ErrorCode, stamp); err != nil {
			return fmt.Errorf("agent hub: heartbeat profile observation: %w", err)
		}
	} else if _, err := tx.Exec(`DELETE FROM agent_node_profile_observations WHERE node_id = ?`, nodeID); err != nil {
		return fmt.Errorf("agent hub: heartbeat clear profile observation: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("agent hub: heartbeat commit: %w", err)
	}
	committed = true
	return nil
}

// ListNodes returns all nodes in a stable newest-first order.
func (r *Repository) ListNodes() ([]Node, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("agent hub: list nodes: %w", ErrInvalidInput)
	}
	rows, err := r.db.Query(`
		SELECT id, name, hostname, agent_version, protocol_version,
		       capabilities_json, inventory_json, last_seen_at, created_at, updated_at
		FROM agent_nodes
		ORDER BY created_at DESC, id ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("agent hub: list nodes: %w", err)
	}
	defer func() { _ = rows.Close() }()

	nodes := make([]Node, 0)
	for rows.Next() {
		node, err := scanNode(rows)
		if err != nil {
			return nil, fmt.Errorf("agent hub: list node row: %w", err)
		}
		nodes = append(nodes, *node)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("agent hub: list nodes rows: %w", err)
	}
	return nodes, nil
}

// GetNode returns one node without exposing its token hash.
func (r *Repository) GetNode(id string) (*Node, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("agent hub: get node: %w", ErrInvalidInput)
	}
	row := r.db.QueryRow(`
		SELECT id, name, hostname, agent_version, protocol_version,
		       capabilities_json, inventory_json, last_seen_at, created_at, updated_at
		FROM agent_nodes WHERE id = ?
	`, id)
	node, err := scanNode(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("agent hub: get node: %w", err)
	}
	return node, nil
}

// CreateTask persists a queued task for an existing node.
func (r *Repository) CreateTask(nodeID string, req TaskRequest, now time.Time) (*Task, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("agent hub: create task: %w", ErrInvalidInput)
	}
	if req.Kind == TaskProfileApply {
		return nil, fmt.Errorf("agent hub: profile apply tasks require the dedicated endpoint: %w", ErrInvalidInput)
	}
	payload, err := marshalStringMap(req.Payload)
	if err != nil {
		return nil, fmt.Errorf("agent hub: create task payload: %w", err)
	}
	stamp := formatTime(now)
	result, err := r.db.Exec(`
		INSERT INTO agent_tasks (node_id, kind, payload_json, status, result_json, created_at)
		VALUES (?, ?, ?, ?, '{}', ?)
	`, nodeID, req.Kind, payload, TaskStatusQueued, stamp)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "foreign key constraint") {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("agent hub: create task: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("agent hub: create task id: %w", err)
	}
	return r.GetTask(id)
}

// CreateProfileApplyTask atomically coalesces a queued/running profile apply
// for the same desired revision. A different in-flight revision is rejected
// before an INSERT, so retries never fan out multiple profile writes.
func (r *Repository) CreateProfileApplyTask(nodeID string, expectedRevision int64, profile AgentProfile, now time.Time) (*Task, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("agent hub: create profile apply task: %w", ErrInvalidInput)
	}
	if expectedRevision < 1 {
		return nil, fmt.Errorf("agent hub: profile apply revision: %w", ErrInvalidInput)
	}
	normalized, profileJSON, err := canonicalProfileApplyDocument(profile, expectedRevision)
	if err != nil {
		return nil, err
	}
	profileJSONB64 := base64.StdEncoding.EncodeToString(profileJSON)
	if len(profileJSON) > maxProfileJSONBytes || len(profileJSONB64) > maxProfileEncodedBytes {
		return nil, fmt.Errorf("agent hub: profile apply payload exceeds size limit: %w", ErrInvalidInput)
	}
	payload, err := marshalStringMap(map[string]string{
		"profile_json_b64": profileJSONB64,
		"revision":         strconv.FormatInt(expectedRevision, 10),
	})
	if err != nil {
		return nil, fmt.Errorf("agent hub: encode profile apply payload: %w", err)
	}

	tx, err := r.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("agent hub: begin profile apply task: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	var nodeExists int
	if err := tx.QueryRow(`SELECT 1 FROM agent_nodes WHERE id = ?`, nodeID).Scan(&nodeExists); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, fmt.Errorf("agent hub: profile apply node lookup: %w", err)
	}
	var storedRevision sql.NullInt64
	var storedProfileJSON string
	if err := tx.QueryRow(`SELECT revision, profile_json FROM agent_node_profiles WHERE node_id = ?`, nodeID).Scan(&storedRevision, &storedProfileJSON); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrProfileNotConfigured
	} else if err != nil {
		return nil, fmt.Errorf("agent hub: profile apply revision lookup: %w", err)
	} else if !storedRevision.Valid || storedRevision.Int64 != expectedRevision {
		return nil, ErrProfileRevisionStale
	}
	storedProfile, err := decodePersistedAgentProfile([]byte(storedProfileJSON))
	if err != nil {
		return nil, fmt.Errorf("agent hub: profile apply desired profile is invalid: %w", ErrInvalidInput)
	}
	storedProfile, err = NormalizeAgentProfile(storedProfile)
	if err != nil {
		return nil, fmt.Errorf("agent hub: profile apply desired profile is invalid: %w", ErrInvalidInput)
	}
	storedCanonical, err := json.Marshal(storedProfile)
	if err != nil {
		return nil, fmt.Errorf("agent hub: profile apply desired profile encoding: %w", ErrInvalidInput)
	}
	requestedCanonical, err := json.Marshal(normalized)
	if err != nil || string(storedCanonical) != string(requestedCanonical) {
		return nil, fmt.Errorf("agent hub: profile apply profile does not match desired state: %w", ErrInvalidInput)
	}
	var observedState sql.NullString
	var observedRevision sql.NullInt64
	observationErr := tx.QueryRow(`
		SELECT state, revision
		FROM agent_node_profile_observations
		WHERE node_id = ?
	`, nodeID).Scan(&observedState, &observedRevision)
	if observationErr != nil && !errors.Is(observationErr, sql.ErrNoRows) {
		return nil, fmt.Errorf("agent hub: profile apply observation lookup: %w", observationErr)
	}
	if observationErr == nil && observedState.String == ProfileObservationApplied && observedRevision.Valid && observedRevision.Int64 == expectedRevision {
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("agent hub: commit applied profile observation: %w", err)
		}
		committed = true
		return nil, nil
	}

	rows, err := tx.Query(`
		SELECT id, payload_json
		FROM agent_tasks
		WHERE node_id = ? AND kind = ? AND status IN (?, ?)
		ORDER BY id DESC
	`, nodeID, TaskProfileApply, TaskStatusQueued, TaskStatusRunning)
	if err != nil {
		return nil, fmt.Errorf("agent hub: profile apply in-flight lookup: %w", err)
	}
	var coalescedID int64
	var conflicting bool
	for rows.Next() {
		var id int64
		var existingPayload string
		if err := rows.Scan(&id, &existingPayload); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("agent hub: profile apply in-flight row: %w", err)
		}
		existingRevision, parseErr := profileApplyPayloadRevision(existingPayload)
		if parseErr != nil || existingRevision != expectedRevision {
			conflicting = true
			continue
		}
		if coalescedID == 0 {
			coalescedID = id
		}
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("agent hub: close profile apply in-flight lookup: %w", err)
	}
	if conflicting {
		return nil, ErrProfileApplyInFlight
	}
	if coalescedID != 0 {
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("agent hub: commit profile apply coalesce: %w", err)
		}
		committed = true
		return r.GetTask(coalescedID)
	}
	result, err := tx.Exec(`
		INSERT INTO agent_tasks (node_id, kind, payload_json, status, result_json, created_at)
		VALUES (?, ?, ?, ?, '{}', ?)
	`, nodeID, TaskProfileApply, payload, TaskStatusQueued, formatTime(now))
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "foreign key constraint") {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("agent hub: create profile apply task: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("agent hub: create profile apply task id: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("agent hub: commit profile apply task: %w", err)
	}
	committed = true
	return r.GetTask(id)
}

// GetLatestProfileApplyTask returns the newest profile apply task, including
// completed/failed history for state derivation.
func (r *Repository) GetLatestProfileApplyTask(nodeID string) (*Task, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("agent hub: get latest profile apply task: %w", ErrInvalidInput)
	}
	row := r.db.QueryRow(`
		SELECT id, node_id, kind, payload_json, status, result_json, error,
		       created_at, started_at, completed_at
		FROM agent_tasks
		WHERE node_id = ? AND kind = ?
		ORDER BY id DESC
		LIMIT 1
	`, nodeID, TaskProfileApply)
	task, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("agent hub: get latest profile apply task: %w", err)
	}
	return task, nil
}

func profileApplyPayloadRevision(payload string) (int64, error) {
	fields, err := decodeStrictJSONObject([]byte(payload))
	if err != nil {
		return 0, err
	}
	if len(fields) != 2 {
		return 0, fmt.Errorf("invalid profile apply payload")
	}
	var revisionValue string
	if err := decodeRequiredJSONField(fields, "revision", &revisionValue); err != nil {
		return 0, fmt.Errorf("invalid profile apply revision")
	}
	var encoded string
	if err := decodeRequiredJSONField(fields, "profile_json_b64", &encoded); err != nil {
		return 0, fmt.Errorf("invalid profile apply profile payload")
	}
	canonical, err := json.Marshal(map[string]string{"profile_json_b64": encoded, "revision": revisionValue})
	if err != nil || string(canonical) != payload {
		return 0, fmt.Errorf("invalid profile apply payload")
	}
	revision, err := strconv.ParseInt(revisionValue, 10, 64)
	if err != nil || revision < 1 || strconv.FormatInt(revision, 10) != revisionValue {
		return 0, fmt.Errorf("invalid profile apply revision")
	}
	profileJSON, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || base64.StdEncoding.EncodeToString(profileJSON) != encoded || len(profileJSON) == 0 || len(profileJSON) > maxProfileJSONBytes || len(encoded) > maxProfileEncodedBytes {
		return 0, fmt.Errorf("invalid profile apply profile payload")
	}
	document, err := decodeProfileApplyDocument(profileJSON)
	if err != nil || document.Revision != revision {
		return 0, fmt.Errorf("invalid profile apply profile payload")
	}
	return revision, nil
}

// ClaimNextTask atomically claims the oldest queued task for a node. SQLite's
// UPDATE ... RETURNING statement makes selection and claim one write operation,
// so two concurrent polls cannot receive the same task.
func (r *Repository) ClaimNextTask(nodeID string, now time.Time) (*Task, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("agent hub: claim task: %w", ErrInvalidInput)
	}
	row := r.db.QueryRow(`
		UPDATE agent_tasks
		SET status = ?, started_at = ?
		WHERE id = (
			SELECT id FROM agent_tasks
			WHERE node_id = ? AND status = ?
			ORDER BY id ASC
			LIMIT 1
		)
		RETURNING id, node_id, kind, payload_json, status, result_json, error,
		          created_at, started_at, completed_at
	`, TaskStatusRunning, formatTime(now), nodeID, TaskStatusQueued)
	task, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("agent hub: claim task: %w", err)
	}
	return task, nil
}

// CompleteTask records a result only for a running task owned by nodeID. The
// conditional update also makes duplicate completion idempotently fail.
func (r *Repository) CompleteTask(nodeID string, taskID int64, req TaskResultRequest, now time.Time) (*Task, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("agent hub: complete task: %w", ErrInvalidInput)
	}
	if task, lookupErr := r.GetTaskForNode(nodeID, taskID); lookupErr == nil && task.Kind == TaskProfileApply {
		if err := ValidateProfileApplyTaskResult(*task, req); err != nil {
			return nil, err
		}
	}
	resultJSON, err := marshalStringMap(req.Result)
	if err != nil {
		return nil, fmt.Errorf("agent hub: complete task result: %w", err)
	}
	res, err := r.db.Exec(`
		UPDATE agent_tasks SET
			status = ?,
			result_json = ?,
			error = ?,
			completed_at = ?
		WHERE id = ? AND node_id = ? AND status = ?
	`, req.Status, resultJSON, req.Error, formatTime(now), taskID, nodeID, TaskStatusRunning)
	if err != nil {
		return nil, fmt.Errorf("agent hub: complete task: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("agent hub: complete task rows affected: %w", err)
	}
	if n != 1 {
		return nil, ErrTaskNotClaimed
	}
	return r.GetTask(taskID)
}

// GetTask returns a task by ID, including its bounded payload and result.
func (r *Repository) GetTask(id int64) (*Task, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("agent hub: get task: %w", ErrInvalidInput)
	}
	row := r.db.QueryRow(`
		SELECT id, node_id, kind, payload_json, status, result_json, error,
		       created_at, started_at, completed_at
		FROM agent_tasks WHERE id = ?
	`, id)
	task, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("agent hub: get task: %w", err)
	}
	return task, nil
}

// GetTaskForNode returns a task only when it belongs to nodeID. Keeping the
// ownership condition in the query prevents a valid task ID from crossing the
// managed-node boundary.
func (r *Repository) GetTaskForNode(nodeID string, id int64) (*Task, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("agent hub: get node task: %w", ErrInvalidInput)
	}
	row := r.db.QueryRow(`
		SELECT id, node_id, kind, payload_json, status, result_json, error,
		       created_at, started_at, completed_at
		FROM agent_tasks WHERE id = ? AND node_id = ?
	`, id, nodeID)
	task, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("agent hub: get node task: %w", err)
	}
	return task, nil
}

// ListTasksForNode returns recent tasks for one managed node, newest first.
func (r *Repository) ListTasksForNode(nodeID string, limit int) ([]Task, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("agent hub: list node tasks: %w", ErrInvalidInput)
	}
	rows, err := r.db.Query(`
		SELECT id, node_id, kind, payload_json, status, result_json, error,
		       created_at, started_at, completed_at
		FROM agent_tasks
		WHERE node_id = ?
		ORDER BY id DESC
		LIMIT ?
	`, nodeID, limit)
	if err != nil {
		return nil, fmt.Errorf("agent hub: list node tasks: %w", err)
	}
	defer func() { _ = rows.Close() }()

	tasks := make([]Task, 0)
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return nil, fmt.Errorf("agent hub: list node task row: %w", err)
		}
		tasks = append(tasks, *task)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("agent hub: list node tasks rows: %w", err)
	}
	return tasks, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanNode(s scanner) (*Node, error) {
	var (
		node                             Node
		capabilitiesJSON, inventoryJSON  string
		lastSeenAt, createdAt, updatedAt sql.NullString
	)
	if err := s.Scan(
		&node.ID, &node.Name, &node.Hostname, &node.AgentVersion,
		&node.ProtocolVersion, &capabilitiesJSON, &inventoryJSON, &lastSeenAt, &createdAt, &updatedAt,
	); err != nil {
		return nil, err
	}
	if inventoryJSON == "" {
		inventoryJSON = "{}"
	}
	if capabilitiesJSON == "" {
		capabilitiesJSON = "[]"
	}
	if err := json.Unmarshal([]byte(capabilitiesJSON), &node.Capabilities); err != nil {
		return nil, fmt.Errorf("decode capabilities: %w", err)
	}
	if err := json.Unmarshal([]byte(inventoryJSON), &node.Inventory); err != nil {
		return nil, fmt.Errorf("decode inventory: %w", err)
	}
	var err error
	if node.CreatedAt, err = parseTime(createdAt.String); err != nil {
		return nil, fmt.Errorf("decode created_at: %w", err)
	}
	if node.UpdatedAt, err = parseTime(updatedAt.String); err != nil {
		return nil, fmt.Errorf("decode updated_at: %w", err)
	}
	if lastSeenAt.Valid && lastSeenAt.String != "" {
		lastSeen, err := parseTime(lastSeenAt.String)
		if err != nil {
			return nil, fmt.Errorf("decode last_seen_at: %w", err)
		}
		node.LastSeenAt = &lastSeen
	}
	return &node, nil
}

func scanTask(s scanner) (*Task, error) {
	var (
		task                              Task
		payloadJSON, resultJSON           string
		createdAt, startedAt, completedAt sql.NullString
	)
	if err := s.Scan(
		&task.ID, &task.NodeID, &task.Kind, &payloadJSON, &task.Status,
		&resultJSON, &task.Error, &createdAt, &startedAt, &completedAt,
	); err != nil {
		return nil, err
	}
	if payloadJSON == "" || payloadJSON == "null" {
		payloadJSON = "{}"
	}
	if resultJSON == "" || resultJSON == "null" {
		resultJSON = "{}"
	}
	if err := json.Unmarshal([]byte(payloadJSON), &task.Payload); err != nil {
		return nil, fmt.Errorf("decode payload: %w", err)
	}
	if err := json.Unmarshal([]byte(resultJSON), &task.Result); err != nil {
		return nil, fmt.Errorf("decode result: %w", err)
	}
	var err error
	if task.CreatedAt, err = parseTime(createdAt.String); err != nil {
		return nil, fmt.Errorf("decode created_at: %w", err)
	}
	if task.StartedAt, err = parseNullableTime(startedAt); err != nil {
		return nil, fmt.Errorf("decode started_at: %w", err)
	}
	if task.CompletedAt, err = parseNullableTime(completedAt); err != nil {
		return nil, fmt.Errorf("decode completed_at: %w", err)
	}
	return &task, nil
}

func marshalStringMap(values map[string]string) (string, error) {
	if values == nil {
		values = map[string]string{}
	}
	b, err := json.Marshal(values)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func parseTime(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, fmt.Errorf("empty timestamp")
	}
	return time.Parse(time.RFC3339Nano, value)
}

func parseNullableTime(value sql.NullString) (*time.Time, error) {
	if !value.Valid || value.String == "" {
		return nil, nil
	}
	t, err := parseTime(value.String)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// constantTimeEqual compares fixed-size token digests without an
// early-exit byte-by-byte comparison.
func constantTimeEqual(stored, expected string) bool {
	return subtle.ConstantTimeCompare([]byte(stored), []byte(expected)) == 1
}
