package agenthub

import (
	cryptoRand "crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/IamYGT/heyserver/internal/managedintegrationstatus"
	"github.com/IamYGT/heyserver/internal/releaseversion"
)

// HeartbeatFreshnessWindow is the maximum absolute clock difference accepted
// for an agent heartbeat.
const HeartbeatFreshnessWindow = 5 * time.Minute

// NodeOnlineWindow is the maximum age of a server-observed heartbeat for a
// node to accept new desired work. It deliberately exceeds the default
// five-second agent interval while keeping offline controls responsive.
const NodeOnlineWindow = 45 * time.Second

// Service contains the agent-hub application policy above Repository. It is
// intentionally small: the HTTP layer owns transport/session authentication,
// while this service owns node bearer tokens and task invariants.
type Service struct {
	repo                 *Repository
	now                  func() time.Time
	integrationStatusMu  sync.Mutex
	metricsMu            sync.Mutex
	profileApplyMu       sync.Mutex
	deployDomainEnsureMu sync.Mutex
}

// ErrDeployDomainEnsureConflict prevents two different desired revisions for
// the same managed domain from being in flight at once.
var ErrDeployDomainEnsureConflict = errors.New("agent hub: deploy domain ensure conflict")

// NewService constructs a service around an already migrated repository.
func NewService(repo *Repository) *Service {
	return &Service{repo: repo, now: time.Now}
}

// New opens the service-facing constructor for callers that want migration to
// happen as part of setup. NewService remains useful for tests and composition.
func New(db *sql.DB) (*Service, error) {
	if err := Migrate(db); err != nil {
		return nil, err
	}
	return NewService(NewRepository(db)), nil
}

// NewServiceWithDB is an explicit alias for New and keeps the database-taking
// constructor discoverable without changing NewService's repository contract.
func NewServiceWithDB(db *sql.DB) (*Service, error) {
	return New(db)
}

// RegisterNode creates a node and returns a bearer token exactly once. Only
// the SHA-256 hash is sent to the repository, so the persistence layer cannot
// accidentally retain the plaintext token.
func (s *Service) RegisterNode(req RegisterNodeRequest) (RegisterNodeResponse, error) {
	if s == nil || s.repo == nil {
		return RegisterNodeResponse{}, fmt.Errorf("agent hub: register node: %w", ErrInvalidInput)
	}
	if err := validateRegisterNodeRequest(req); err != nil {
		return RegisterNodeResponse{}, err
	}

	rawToken := make([]byte, 32)
	if _, err := cryptoRand.Read(rawToken); err != nil {
		return RegisterNodeResponse{}, fmt.Errorf("agent hub: generate node token: %w", err)
	}
	token := hex.EncodeToString(rawToken)
	tokenDigest := sha256.Sum256([]byte(token))
	now := s.currentTime()
	node, err := s.repo.CreateNode(req, hex.EncodeToString(tokenDigest[:]), now)
	if err != nil {
		return RegisterNodeResponse{}, err
	}
	return RegisterNodeResponse{Node: *node, Token: token}, nil
}

// AuthenticateNode validates a node bearer token using constant-time digest
// comparison and returns the public node record on success.
func (s *Service) AuthenticateNode(nodeID, token string) (*Node, error) {
	if s == nil || s.repo == nil {
		return nil, fmt.Errorf("agent hub: authenticate node: %w", ErrInvalidInput)
	}
	if !validNodeID(nodeID) || token == "" {
		return nil, ErrUnauthorized
	}
	return s.repo.AuthenticateToken(nodeID, token)
}

// Heartbeat authenticates and persists a node heartbeat after checking the
// protocol version and the absolute timestamp freshness window.
func (s *Service) Heartbeat(nodeID, token string, req HeartbeatRequest) (HeartbeatResponse, error) {
	if s == nil || s.repo == nil {
		return HeartbeatResponse{}, fmt.Errorf("agent hub: heartbeat: %w", ErrInvalidInput)
	}
	if !validNodeID(nodeID) || req.NodeID != nodeID {
		return HeartbeatResponse{}, ErrUnauthorized
	}
	// Authenticate first so an invalid token cannot be used to probe protocol
	// or timestamp validation behavior for a node ID.
	if _, err := s.AuthenticateNode(nodeID, token); err != nil {
		return HeartbeatResponse{}, err
	}
	if req.ProtocolVersion != ProtocolVersion {
		return HeartbeatResponse{}, ErrUnsupportedProtocol
	}
	if err := validateHeartbeatFields(req); err != nil {
		return HeartbeatResponse{}, err
	}
	now := s.currentTime()
	if heartbeatAge(now, req.SentAt) > HeartbeatFreshnessWindow {
		return HeartbeatResponse{}, ErrStaleHeartbeat
	}
	s.profileApplyMu.Lock()
	defer s.profileApplyMu.Unlock()
	if err := s.repo.UpdateHeartbeat(nodeID, req, now); err != nil {
		return HeartbeatResponse{}, err
	}
	return HeartbeatResponse{Accepted: true, ServerAt: now}, nil
}

// ValidateHeartbeat validates a heartbeat without changing state. It is
// useful to transport adapters that authenticate separately and want the same
// protocol/freshness policy as Heartbeat.
func (s *Service) ValidateHeartbeat(req HeartbeatRequest) error {
	if req.ProtocolVersion != ProtocolVersion {
		return ErrUnsupportedProtocol
	}
	if err := validateHeartbeatFields(req); err != nil {
		return err
	}
	if heartbeatAge(s.currentTime(), req.SentAt) > HeartbeatFreshnessWindow {
		return ErrStaleHeartbeat
	}
	return nil
}

// ListNodes returns public node records. Token hashes are never part of Node.
func (s *Service) ListNodes() ([]Node, error) {
	if s == nil || s.repo == nil {
		return nil, fmt.Errorf("agent hub: list nodes: %w", ErrInvalidInput)
	}
	return s.repo.ListNodes()
}

// GetNode returns one public node record.
func (s *Service) GetNode(id string) (*Node, error) {
	if s == nil || s.repo == nil {
		return nil, fmt.Errorf("agent hub: get node: %w", ErrInvalidInput)
	}
	if !validNodeID(id) {
		return nil, ErrNotFound
	}
	return s.repo.GetNode(id)
}

// GetNodeProfile returns the panel-owned desired profile together with the
// latest raw node observation and the derived apply lifecycle state.
func (s *Service) GetNodeProfile(nodeID string) (AgentProfileResponse, error) {
	if s == nil || s.repo == nil {
		return AgentProfileResponse{}, fmt.Errorf("agent hub: get node profile: %w", ErrInvalidInput)
	}
	node, err := s.GetNode(nodeID)
	if err != nil {
		return AgentProfileResponse{}, err
	}
	record, err := s.repo.GetNodeProfile(nodeID)
	if err != nil {
		return AgentProfileResponse{}, err
	}
	observation, err := s.repo.GetNodeProfileObservation(nodeID)
	if err != nil {
		return AgentProfileResponse{}, err
	}
	latestTask, err := s.repo.GetLatestProfileApplyTask(nodeID)
	if err != nil {
		return AgentProfileResponse{}, err
	}
	return profileResponse(record, *node, s.IsNodeOnline(*node), observation, latestTask), nil
}

// UpdateNodeProfile stores desired profile state with compare-and-swap
// semantics. It does not enqueue an agent task or claim that the managed node
// applied the profile; a later agent protocol can add that transition without
// changing this desired-state boundary.
func (s *Service) UpdateNodeProfile(nodeID string, profile AgentProfile, expectedRevision int64) (AgentProfileResponse, error) {
	if s == nil || s.repo == nil {
		return AgentProfileResponse{}, fmt.Errorf("agent hub: update node profile: %w", ErrInvalidInput)
	}
	if _, err := s.GetNode(nodeID); err != nil {
		return AgentProfileResponse{}, err
	}
	if expectedRevision < 0 {
		return AgentProfileResponse{}, fmt.Errorf("agent hub: profile revision: %w", ErrInvalidInput)
	}
	if _, err := NormalizeAgentProfile(profile); err != nil {
		return AgentProfileResponse{}, err
	}
	if _, err := s.repo.SaveNodeProfile(nodeID, profile, expectedRevision, s.currentTime()); err != nil {
		return AgentProfileResponse{}, err
	}
	return s.GetNodeProfile(nodeID)
}

// ApplyNodeProfile queues the desired profile revision for a capable,
// online node. The profile is read from the panel-owned desired row and is
// never accepted from this request; nil,nil is returned when the latest
// heartbeat already observed this revision as applied.
func (s *Service) ApplyNodeProfile(nodeID string, expectedRevision int64) (*Task, error) {
	if s == nil || s.repo == nil {
		return nil, fmt.Errorf("agent hub: apply node profile: %w", ErrInvalidInput)
	}
	if !validNodeID(nodeID) {
		return nil, ErrNotFound
	}
	if expectedRevision < 1 {
		return nil, fmt.Errorf("agent hub: profile apply revision: %w", ErrInvalidInput)
	}

	// Keep the read/check/enqueue sequence single-flight within a panel
	// process. Repository-level checks repeat the revision/in-flight guards so
	// direct callers and future workers cannot bypass the invariant.
	s.profileApplyMu.Lock()
	defer s.profileApplyMu.Unlock()

	node, err := s.repo.GetNode(nodeID)
	if err != nil {
		return nil, err
	}
	record, err := s.repo.GetNodeProfile(nodeID)
	if err != nil {
		return nil, err
	}
	if !record.Configured {
		return nil, ErrProfileNotConfigured
	}
	if record.Revision != expectedRevision {
		return nil, ErrProfileRevisionStale
	}
	if !hasCapability(node.Capabilities, CapabilityProfileApply) {
		return nil, fmt.Errorf("agent hub: node does not advertise %s: %w", CapabilityProfileApply, ErrCapabilityUnavailable)
	}
	observation, err := s.repo.GetNodeProfileObservation(nodeID)
	if err != nil {
		return nil, err
	}
	if observation != nil && observation.State == ProfileObservationApplied && observation.Revision == expectedRevision {
		return nil, nil
	}
	if !s.IsNodeOnline(*node) {
		return nil, fmt.Errorf("agent hub: cannot queue %s for %s: %w", TaskProfileApply, nodeID, ErrNodeOffline)
	}
	// A completed task remains awaiting_heartbeat until the agent reports the
	// requested revision. Treat that receipt as idempotent too, rather than
	// queueing a second restart while the first transition is still pending.
	// A same-revision failed observation deliberately permits a retry.
	if latestTask, latestErr := s.repo.GetLatestProfileApplyTask(nodeID); latestErr != nil {
		return nil, latestErr
	} else if latestTask != nil && latestTask.Status == TaskStatusCompleted {
		if taskRevision, parseErr := profileApplyPayloadRevisionFromTask(*latestTask); parseErr == nil && taskRevision == expectedRevision {
			failedObservation := observation != nil && observation.State == ProfileObservationFailed && observation.Revision == expectedRevision
			if !failedObservation {
				return latestTask, nil
			}
		}
	}
	return s.repo.CreateProfileApplyTask(nodeID, expectedRevision, record.Profile, s.currentTime())
}

// IsNodeOnline reports connectivity from the hub clock and the last heartbeat
// timestamp persisted by the server. Agent or browser clocks cannot influence
// this decision.
func (s *Service) IsNodeOnline(node Node) bool {
	if node.LastSeenAt == nil {
		return false
	}
	age := s.currentTime().Sub(node.LastSeenAt.UTC())
	return age >= 0 && age <= NodeOnlineWindow
}

// EnqueueTask validates the v1 allowlist/payload shape before persistence.
func (s *Service) EnqueueTask(nodeID string, req TaskRequest) (*Task, error) {
	if s == nil || s.repo == nil {
		return nil, fmt.Errorf("agent hub: enqueue task: %w", ErrInvalidInput)
	}
	if req.Kind == TaskDeployDomainAction && req.Payload != nil && req.Payload["action"] == "ensure" {
		return s.enqueueDeployDomainEnsure(nodeID, req)
	}
	if !validNodeID(nodeID) {
		return nil, ErrNotFound
	}
	node, err := s.repo.GetNode(nodeID)
	if err != nil {
		return nil, err
	}
	if err := ValidateTaskRequest(req); err != nil {
		return nil, err
	}
	if required := capabilityForTask(req.Kind); required != "" && !hasCapability(node.Capabilities, required) {
		return nil, fmt.Errorf("agent hub: node does not advertise %s: %w", required, ErrCapabilityUnavailable)
	}
	if !s.IsNodeOnline(*node) {
		return nil, fmt.Errorf("agent hub: cannot queue %s for %s: %w", req.Kind, nodeID, ErrNodeOffline)
	}
	return s.repo.CreateTask(nodeID, req, s.currentTime())
}

// EnqueueDeployDomainEnsure queues an idempotent managed project-domain
// ensure operation. The expected revision is panel-owned input and is kept in
// the task payload so the agent can reject stale observations locally.
func (s *Service) EnqueueDeployDomainEnsure(nodeID, targetID, domain, expectedRevision string) (*Task, error) {
	if expectedRevision == "" {
		expectedRevision = "absent"
	}
	payload := map[string]string{
		"target":            targetID,
		"domain":            domain,
		"action":            "ensure",
		"expected_revision": expectedRevision,
	}
	return s.EnqueueTask(nodeID, TaskRequest{Kind: TaskDeployDomainAction, Payload: payload})
}

func (s *Service) enqueueDeployDomainEnsure(nodeID string, req TaskRequest) (*Task, error) {
	// Validate before looking up capability/online state. This keeps malformed
	// desired work from being persisted or hidden behind a node-state error.
	if err := ValidateTaskRequest(req); err != nil {
		return nil, err
	}
	if !validNodeID(nodeID) {
		return nil, ErrNotFound
	}

	s.deployDomainEnsureMu.Lock()
	defer s.deployDomainEnsureMu.Unlock()

	node, err := s.repo.GetNode(nodeID)
	if err != nil {
		return nil, err
	}
	if !hasCapability(node.Capabilities, CapabilityDeployDomainAction) {
		return nil, fmt.Errorf("agent hub: node does not advertise %s: %w", CapabilityDeployDomainAction, ErrCapabilityUnavailable)
	}
	if !s.IsNodeOnline(*node) {
		return nil, fmt.Errorf("agent hub: cannot queue %s for %s: %w", TaskDeployDomainAction, nodeID, ErrNodeOffline)
	}

	expectedRevision := req.Payload["expected_revision"]
	tasks, err := s.repo.ListTasksForNode(nodeID, 50)
	if err != nil {
		return nil, err
	}
	for _, task := range tasks {
		if task.Kind != TaskDeployDomainAction || (task.Status != TaskStatusQueued && task.Status != TaskStatusRunning) ||
			task.Payload["action"] != "ensure" || task.Payload["target"] != req.Payload["target"] || task.Payload["domain"] != req.Payload["domain"] {
			continue
		}
		if task.Payload["expected_revision"] != expectedRevision {
			return nil, ErrDeployDomainEnsureConflict
		}
		return &task, nil
	}
	return s.repo.CreateTask(nodeID, req, s.currentTime())
}

// EnqueueIntegrationStatusTask queues the read-only managed-node status task,
// coalescing a queued or running task for the same node. Capability and
// heartbeat checks happen before the queue is touched, so old agents remain
// compatible and unsupported requests never persist a task.
func (s *Service) EnqueueIntegrationStatusTask(nodeID string) (*Task, error) {
	if s == nil || s.repo == nil {
		return nil, fmt.Errorf("agent hub: enqueue integration status: %w", ErrInvalidInput)
	}
	if !validNodeID(nodeID) {
		return nil, ErrNotFound
	}

	// The service is a process-local singleton in the panel. Keeping the
	// coalescing check and insert under one lock prevents concurrent HTTP GETs
	// from creating duplicate probe tasks.
	s.integrationStatusMu.Lock()
	defer s.integrationStatusMu.Unlock()

	node, err := s.repo.GetNode(nodeID)
	if err != nil {
		return nil, err
	}
	if !hasCapability(node.Capabilities, CapabilityIntegrationStatus) {
		return nil, fmt.Errorf("agent hub: node does not advertise %s: %w", CapabilityIntegrationStatus, ErrCapabilityUnavailable)
	}
	if !s.IsNodeOnline(*node) {
		return nil, fmt.Errorf("agent hub: cannot queue %s for %s: %w", TaskIntegrationStatus, nodeID, ErrNodeOffline)
	}
	if tasks, listErr := s.repo.ListTasksForNode(nodeID, 50); listErr != nil {
		return nil, listErr
	} else {
		for _, task := range tasks {
			if task.Kind == TaskIntegrationStatus && (task.Status == TaskStatusQueued || task.Status == TaskStatusRunning) {
				return &task, nil
			}
		}
	}
	return s.repo.CreateTask(nodeID, TaskRequest{Kind: TaskIntegrationStatus, Payload: map[string]string{}}, s.currentTime())
}

// EnqueueMetricsTask queues only the fixed, empty-payload metrics.read task.
// Capability and online checks happen before any task can be persisted.
func (s *Service) EnqueueMetricsTask(nodeID string) (*Task, error) {
	if s == nil || s.repo == nil {
		return nil, fmt.Errorf("agent hub: enqueue metrics: %w", ErrInvalidInput)
	}
	if !validNodeID(nodeID) {
		return nil, ErrNotFound
	}
	s.metricsMu.Lock()
	defer s.metricsMu.Unlock()

	node, err := s.repo.GetNode(nodeID)
	if err != nil {
		return nil, err
	}
	if !hasCapability(node.Capabilities, CapabilityMetricsRead) {
		return nil, fmt.Errorf("agent hub: node does not advertise %s: %w", CapabilityMetricsRead, ErrCapabilityUnavailable)
	}
	if !s.IsNodeOnline(*node) {
		return nil, fmt.Errorf("agent hub: cannot queue %s for %s: %w", TaskMetricsRead, nodeID, ErrNodeOffline)
	}
	tasks, err := s.repo.ListTasksForNode(nodeID, 50)
	if err != nil {
		return nil, err
	}
	for _, task := range tasks {
		if task.Kind == TaskMetricsRead && (task.Status == TaskStatusQueued || task.Status == TaskStatusRunning) {
			return &task, nil
		}
	}
	return s.repo.CreateTask(nodeID, TaskRequest{Kind: TaskMetricsRead, Payload: map[string]string{}}, s.currentTime())
}

func capabilityForTask(kind string) string {
	switch kind {
	case TaskServiceStatus:
		return CapabilityServiceStatus
	case TaskServiceAction:
		return CapabilityServiceAction
	case TaskHostAction:
		return CapabilityHostAction
	case TaskProcessSignal:
		return CapabilityProcessSignal
	case TaskDiskCleanupScan, TaskDiskCleanupExecute:
		return CapabilityDiskCleanup
	case TaskLogsRead:
		return CapabilityLogsRead
	case TaskContainerList:
		return CapabilityContainerRead
	case TaskContainerAction:
		return CapabilityContainerAction
	case TaskNginxAction:
		return CapabilityNginxAction
	case TaskNginxConfigList, TaskNginxConfigRead:
		return CapabilityNginxConfigRead
	case TaskNginxConfigWrite:
		return CapabilityNginxConfigWrite
	case TaskPHPInventory, TaskPHPConfigRead:
		return CapabilityPHPRead
	case TaskPHPConfigWrite:
		return CapabilityPHPWrite
	case TaskPHPAction:
		return CapabilityPHPAction
	case TaskPM2List, TaskPM2Logs:
		return CapabilityPM2Read
	case TaskPM2Action:
		return CapabilityPM2Action
	case TaskCronInventory:
		return CapabilityCronRead
	case TaskCronCreate, TaskCronUpdate, TaskCronDelete:
		return CapabilityCronWrite
	case TaskCronRun:
		return CapabilityCronRun
	case TaskFirewallInventory:
		return CapabilityFirewallRead
	case TaskFirewallAdd, TaskFirewallDelete:
		return CapabilityFirewallWrite
	case TaskDomainInventory:
		return CapabilityDomainRead
	case TaskDomainAction:
		return CapabilityDomainAction
	case TaskSSLInventory:
		return CapabilitySSLRead
	case TaskSSLAction:
		return CapabilitySSLAction
	case TaskDatabaseInventory:
		return CapabilityDatabaseRead
	case TaskDatabaseAction:
		return CapabilityDatabaseAction
	case TaskBackupInventory:
		return CapabilityBackupRead
	case TaskBackupRun:
		return CapabilityBackupRun
	case TaskFilesBrowse, TaskFilesRead:
		return CapabilityFilesRead
	case TaskFilesWrite:
		return CapabilityFilesWrite
	case TaskDeployInventory:
		return CapabilityDeployRead
	case TaskDeployAction:
		return CapabilityDeployAction
	case TaskDeployDomainInventory, TaskDeployDomainHealth:
		return CapabilityDeployDomainRead
	case TaskDeployDomainAction:
		return CapabilityDeployDomainAction
	case TaskAgentUpdateStatus:
		return CapabilityAgentUpdateRead
	case TaskAgentUpdateAction:
		return CapabilityAgentUpdateAction
	case TaskIntegrationStatus:
		return CapabilityIntegrationStatus
	case TaskMetricsRead:
		return CapabilityMetricsRead
	case TaskProfileApply:
		return CapabilityProfileApply
	default:
		return ""
	}
}

func hasCapability(capabilities []string, capability string) bool {
	for _, candidate := range capabilities {
		if candidate == capability {
			return true
		}
	}
	return false
}

// GetTaskForNode returns panel-visible task progress without allowing callers
// to use a task ID to inspect another managed node's work.
func (s *Service) GetTaskForNode(nodeID string, taskID int64) (*Task, error) {
	if s == nil || s.repo == nil {
		return nil, fmt.Errorf("agent hub: get node task: %w", ErrInvalidInput)
	}
	if !validNodeID(nodeID) || taskID <= 0 {
		return nil, ErrNotFound
	}
	return s.repo.GetTaskForNode(nodeID, taskID)
}

// ListTasksForNode returns a bounded node-owned operation history for the
// control plane.
func (s *Service) ListTasksForNode(nodeID string, limit int) ([]Task, error) {
	if s == nil || s.repo == nil {
		return nil, fmt.Errorf("agent hub: list node tasks: %w", ErrInvalidInput)
	}
	if !validNodeID(nodeID) {
		return nil, ErrNotFound
	}
	if _, err := s.repo.GetNode(nodeID); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}
	return s.repo.ListTasksForNode(nodeID, limit)
}

// PollTask authenticates the node and atomically claims its oldest queued
// task. A nil task and nil error means the queue is currently empty.
func (s *Service) PollTask(nodeID, token string) (*Task, error) {
	if _, err := s.AuthenticateNode(nodeID, token); err != nil {
		return nil, err
	}
	return s.PollTaskForNode(nodeID)
}

// PollTaskForNode claims a task after an adapter has already authenticated the
// node. Agent-facing handlers can use this after shared auth middleware.
func (s *Service) PollTaskForNode(nodeID string) (*Task, error) {
	if s == nil || s.repo == nil {
		return nil, fmt.Errorf("agent hub: poll task: %w", ErrInvalidInput)
	}
	if !validNodeID(nodeID) {
		return nil, ErrUnauthorized
	}
	return s.repo.ClaimNextTask(nodeID, s.currentTime())
}

// CompleteTask authenticates the node and persists the result of a task it
// previously claimed.
func (s *Service) CompleteTask(nodeID, token string, taskID int64, req TaskResultRequest) (*Task, error) {
	if _, err := s.AuthenticateNode(nodeID, token); err != nil {
		return nil, err
	}
	return s.CompleteTaskForNode(nodeID, taskID, req)
}

// CompleteTaskForNode persists a result after the transport adapter has
// authenticated the node separately.
func (s *Service) CompleteTaskForNode(nodeID string, taskID int64, req TaskResultRequest) (*Task, error) {
	if s == nil || s.repo == nil {
		return nil, fmt.Errorf("agent hub: complete task: %w", ErrInvalidInput)
	}
	if !validNodeID(nodeID) {
		return nil, ErrUnauthorized
	}
	if taskID <= 0 {
		return nil, fmt.Errorf("agent hub: complete task: %w", ErrInvalidInput)
	}
	if err := ValidateTaskResultRequest(req); err != nil {
		return nil, err
	}
	task, err := s.repo.GetTaskForNode(nodeID, taskID)
	if err != nil {
		// Preserve the repository's existing ownership/state error semantics
		// for cross-node or already-absent tasks.
		if errors.Is(err, ErrNotFound) {
			return s.repo.CompleteTask(nodeID, taskID, req, s.currentTime())
		}
		return nil, err
	}
	if task.Kind == TaskIntegrationStatus {
		if err := ValidateIntegrationStatusTaskResult(req); err != nil {
			return nil, err
		}
	}
	if task.Kind == TaskMetricsRead {
		if err := ValidateMetricsTaskResult(req, s.currentTime()); err != nil {
			return nil, err
		}
	}
	if task.Kind == TaskProfileApply {
		if err := ValidateProfileApplyTaskResult(*task, req); err != nil {
			return nil, err
		}
	}
	return s.repo.CompleteTask(nodeID, taskID, req, s.currentTime())
}

// ValidateTaskRequest is the single allowlist gate for panel-created tasks.
// The agent remains responsible for its local configured service allowlist;
// this gate prevents arbitrary shell-like payloads from reaching it.
func ValidateTaskRequest(req TaskRequest) error {
	if req.Kind != TaskServiceStatus && req.Kind != TaskServiceAction && req.Kind != TaskHostAction && req.Kind != TaskProcessSignal && req.Kind != TaskDiskCleanupScan && req.Kind != TaskDiskCleanupExecute && req.Kind != TaskLogsRead && req.Kind != TaskContainerList && req.Kind != TaskContainerAction && req.Kind != TaskNginxAction && req.Kind != TaskNginxConfigList && req.Kind != TaskNginxConfigRead && req.Kind != TaskNginxConfigWrite && req.Kind != TaskPHPInventory && req.Kind != TaskPHPConfigRead && req.Kind != TaskPHPConfigWrite && req.Kind != TaskPHPAction && req.Kind != TaskPM2List && req.Kind != TaskPM2Logs && req.Kind != TaskPM2Action && req.Kind != TaskCronInventory && req.Kind != TaskCronCreate && req.Kind != TaskCronUpdate && req.Kind != TaskCronDelete && req.Kind != TaskCronRun && req.Kind != TaskFirewallInventory && req.Kind != TaskFirewallAdd && req.Kind != TaskFirewallDelete && req.Kind != TaskDomainInventory && req.Kind != TaskDomainAction && req.Kind != TaskSSLInventory && req.Kind != TaskSSLAction && req.Kind != TaskDatabaseInventory && req.Kind != TaskDatabaseAction && req.Kind != TaskBackupInventory && req.Kind != TaskBackupRun && req.Kind != TaskFilesBrowse && req.Kind != TaskFilesRead && req.Kind != TaskFilesWrite && req.Kind != TaskDeployInventory && req.Kind != TaskDeployAction && req.Kind != TaskDeployDomainInventory && req.Kind != TaskDeployDomainHealth && req.Kind != TaskDeployDomainAction && req.Kind != TaskAgentUpdateStatus && req.Kind != TaskAgentUpdateAction && req.Kind != TaskIntegrationStatus && req.Kind != TaskMetricsRead {
		return fmt.Errorf("agent hub: task kind %q: %w", req.Kind, ErrInvalidInput)
	}
	if (len(req.Payload) == 0 && req.Kind != TaskDiskCleanupScan && req.Kind != TaskContainerList && req.Kind != TaskNginxConfigList && req.Kind != TaskPHPInventory && req.Kind != TaskPM2List && req.Kind != TaskCronInventory && req.Kind != TaskFirewallInventory && req.Kind != TaskDomainInventory && req.Kind != TaskSSLInventory && req.Kind != TaskDatabaseInventory && req.Kind != TaskBackupInventory && req.Kind != TaskDeployInventory && req.Kind != TaskAgentUpdateStatus && req.Kind != TaskIntegrationStatus && req.Kind != TaskMetricsRead) || len(req.Payload) > 6 {
		return fmt.Errorf("agent hub: task payload: %w", ErrInvalidInput)
	}
	for key, value := range req.Payload {
		valueLimit := maxTaskPayloadValueLength
		configContent := (req.Kind == TaskNginxConfigWrite || req.Kind == TaskPHPConfigWrite) && key == "content_b64"
		cronJob := (req.Kind == TaskCronCreate || req.Kind == TaskCronUpdate) && key == "job_b64"
		firewallRule := req.Kind == TaskFirewallAdd && key == "rule_b64"
		fileContent := req.Kind == TaskFilesWrite && key == "content_b64"
		managedPath := (req.Kind == TaskFilesBrowse || req.Kind == TaskFilesRead || req.Kind == TaskFilesWrite) && key == "path"
		if configContent {
			valueLimit = maxEncodedNginxConfigSize
		} else if cronJob {
			valueLimit = maxEncodedCronJobSize
		} else if firewallRule {
			valueLimit = maxEncodedFirewallSize
		} else if fileContent {
			valueLimit = maxEncodedNginxConfigSize
		} else if managedPath {
			valueLimit = maxManagedPathLength
		}
		if key == "" || len(key) > maxTaskPayloadValueLength || !safeToken(key) ||
			(!configContent && !cronJob && !firewallRule && !fileContent && value == "") || len(value) > valueLimit || containsControl(value) {
			return fmt.Errorf("agent hub: task payload: %w", ErrInvalidInput)
		}
	}

	switch req.Kind {
	case TaskServiceStatus:
		if len(req.Payload) != 1 || req.Payload["service"] == "" {
			return fmt.Errorf("agent hub: service.status requires service: %w", ErrInvalidInput)
		}
		if !validUnitName(req.Payload["service"]) {
			return fmt.Errorf("agent hub: invalid systemd unit: %w", ErrInvalidInput)
		}
	case TaskServiceAction:
		if len(req.Payload) != 2 || req.Payload["service"] == "" || req.Payload["action"] == "" {
			return fmt.Errorf("agent hub: service.action requires service and action: %w", ErrInvalidInput)
		}
		if !validUnitName(req.Payload["service"]) {
			return fmt.Errorf("agent hub: invalid systemd unit: %w", ErrInvalidInput)
		}
		switch req.Payload["action"] {
		case "start", "stop", "restart":
		default:
			return fmt.Errorf("agent hub: unsupported service action: %w", ErrInvalidInput)
		}
	case TaskHostAction:
		if len(req.Payload) != 1 || req.Payload["action"] == "" {
			return fmt.Errorf("agent hub: host.action requires action: %w", ErrInvalidInput)
		}
		switch req.Payload["action"] {
		case "memory-optimize", "swap-reset", "temp-clean", "reboot", "reboot-cancel":
		default:
			return fmt.Errorf("agent hub: unsupported host action: %w", ErrInvalidInput)
		}
	case TaskProcessSignal:
		if len(req.Payload) != 3 || req.Payload["pid"] == "" || req.Payload["start_time"] == "" || req.Payload["signal"] == "" {
			return fmt.Errorf("agent hub: process.signal requires pid, start_time, and signal: %w", ErrInvalidInput)
		}
		pid, pidErr := strconv.Atoi(req.Payload["pid"])
		startTime, startErr := strconv.ParseUint(req.Payload["start_time"], 10, 64)
		if pidErr != nil || pid <= 1 || startErr != nil || startTime == 0 {
			return fmt.Errorf("agent hub: invalid process identity: %w", ErrInvalidInput)
		}
		if req.Payload["signal"] != "term" && req.Payload["signal"] != "kill" {
			return fmt.Errorf("agent hub: unsupported process signal: %w", ErrInvalidInput)
		}
	case TaskDiskCleanupScan:
		if len(req.Payload) != 0 {
			return fmt.Errorf("agent hub: disk.cleanup.scan does not accept payload fields: %w", ErrInvalidInput)
		}
	case TaskDiskCleanupExecute:
		if len(req.Payload) != 1 || !validDiskCleanupTargets(req.Payload["targets"]) {
			return fmt.Errorf("agent hub: disk.cleanup.execute requires valid targets: %w", ErrInvalidInput)
		}
	case TaskLogsRead:
		lines, err := strconv.Atoi(req.Payload["lines"])
		if len(req.Payload) != 2 || !validLogSource(req.Payload["source"]) || err != nil || lines < 1 || lines > 500 || strconv.Itoa(lines) != req.Payload["lines"] {
			return fmt.Errorf("agent hub: logs.read requires a valid source and lines between 1 and 500: %w", ErrInvalidInput)
		}
	case TaskContainerList:
		if len(req.Payload) != 0 {
			return fmt.Errorf("agent hub: container.list does not accept payload fields: %w", ErrInvalidInput)
		}
	case TaskContainerAction:
		if len(req.Payload) != 2 || !validContainerName(req.Payload["container"]) || !validContainerAction(req.Payload["action"]) {
			return fmt.Errorf("agent hub: container.action requires a valid container and action: %w", ErrInvalidInput)
		}
	case TaskNginxAction:
		if len(req.Payload) != 1 || (req.Payload["action"] != "test" && req.Payload["action"] != "reload") {
			return fmt.Errorf("agent hub: nginx.action requires test or reload: %w", ErrInvalidInput)
		}
	case TaskNginxConfigList:
		if len(req.Payload) != 0 {
			return fmt.Errorf("agent hub: nginx.config.list does not accept payload fields: %w", ErrInvalidInput)
		}
	case TaskNginxConfigRead:
		if len(req.Payload) != 1 || !validNginxConfigName(req.Payload["name"]) {
			return fmt.Errorf("agent hub: nginx.config.read requires a valid name: %w", ErrInvalidInput)
		}
	case TaskNginxConfigWrite:
		if len(req.Payload) != 4 || !validNginxConfigName(req.Payload["name"]) || !validSHA256(req.Payload["checksum"]) || (req.Payload["reload"] != "true" && req.Payload["reload"] != "false") {
			return fmt.Errorf("agent hub: nginx.config.write requires valid name, content, checksum, and reload fields: %w", ErrInvalidInput)
		}
		content, err := base64.StdEncoding.DecodeString(req.Payload["content_b64"])
		if err != nil || len(content) > maxNginxConfigBytes || !validNginxConfigContent(content) {
			return fmt.Errorf("agent hub: nginx.config.write content is invalid or too large: %w", ErrInvalidInput)
		}
	case TaskPHPInventory:
		if len(req.Payload) != 0 {
			return fmt.Errorf("agent hub: php.inventory does not accept payload fields: %w", ErrInvalidInput)
		}
	case TaskPHPConfigRead:
		if len(req.Payload) != 2 || !validPHPVersion(req.Payload["version"]) || !validPHPPool(req.Payload["pool"]) {
			return fmt.Errorf("agent hub: php.config.read requires a valid version and pool: %w", ErrInvalidInput)
		}
	case TaskPHPConfigWrite:
		if len(req.Payload) != 5 || !validPHPVersion(req.Payload["version"]) || !validPHPPool(req.Payload["pool"]) || !validSHA256(req.Payload["checksum"]) || (req.Payload["reload"] != "true" && req.Payload["reload"] != "false") {
			return fmt.Errorf("agent hub: php.config.write requires valid version, pool, content, checksum, and reload fields: %w", ErrInvalidInput)
		}
		content, err := base64.StdEncoding.DecodeString(req.Payload["content_b64"])
		if err != nil || len(content) > maxNginxConfigBytes || !validNginxConfigContent(content) {
			return fmt.Errorf("agent hub: php.config.write content is invalid or too large: %w", ErrInvalidInput)
		}
	case TaskPHPAction:
		if len(req.Payload) != 2 || !validPHPVersion(req.Payload["version"]) || !validPHPAction(req.Payload["action"]) {
			return fmt.Errorf("agent hub: php.action requires a valid version and action: %w", ErrInvalidInput)
		}
	case TaskPM2List:
		if len(req.Payload) != 0 {
			return fmt.Errorf("agent hub: pm2.list does not accept payload fields: %w", ErrInvalidInput)
		}
	case TaskPM2Logs:
		lines, err := strconv.Atoi(req.Payload["lines"])
		if len(req.Payload) != 2 || !validPM2Name(req.Payload["name"]) || err != nil || lines < 1 || lines > 500 || strconv.Itoa(lines) != req.Payload["lines"] {
			return fmt.Errorf("agent hub: pm2.logs requires a valid name and canonical line count: %w", ErrInvalidInput)
		}
	case TaskPM2Action:
		if len(req.Payload) != 2 || !validPM2Name(req.Payload["name"]) || !validPM2Action(req.Payload["action"]) {
			return fmt.Errorf("agent hub: pm2.action requires a valid name and action: %w", ErrInvalidInput)
		}
	case TaskCronInventory:
		if len(req.Payload) != 0 {
			return fmt.Errorf("agent hub: cron.inventory does not accept payload fields: %w", ErrInvalidInput)
		}
	case TaskCronCreate, TaskCronUpdate:
		if len(req.Payload) != 2 || !validSHA256(req.Payload["revision"]) || !validCronJobPayload(req.Payload["job_b64"], req.Kind == TaskCronUpdate) {
			return fmt.Errorf("agent hub: cron write requires a valid job and revision: %w", ErrInvalidInput)
		}
	case TaskCronDelete:
		if len(req.Payload) != 2 || !validCronID(req.Payload["id"]) || !validSHA256(req.Payload["revision"]) {
			return fmt.Errorf("agent hub: cron.delete requires a valid id and revision: %w", ErrInvalidInput)
		}
	case TaskCronRun:
		if len(req.Payload) != 1 || !validCronID(req.Payload["id"]) {
			return fmt.Errorf("agent hub: cron.run requires a valid id: %w", ErrInvalidInput)
		}
	case TaskFirewallInventory:
		if len(req.Payload) != 0 {
			return fmt.Errorf("agent hub: firewall.inventory does not accept payload fields: %w", ErrInvalidInput)
		}
	case TaskFirewallAdd:
		if len(req.Payload) != 2 || !validSHA256(req.Payload["revision"]) || !validFirewallRulePayload(req.Payload["rule_b64"]) {
			return fmt.Errorf("agent hub: firewall.add requires a valid rule and revision: %w", ErrInvalidInput)
		}
	case TaskFirewallDelete:
		if len(req.Payload) != 2 || !validFirewallID(req.Payload["id"]) || !validSHA256(req.Payload["revision"]) {
			return fmt.Errorf("agent hub: firewall.delete requires a valid id and revision: %w", ErrInvalidInput)
		}
	case TaskDomainInventory:
		if len(req.Payload) != 0 {
			return fmt.Errorf("agent hub: domain.inventory does not accept payload fields: %w", ErrInvalidInput)
		}
	case TaskDomainAction:
		if len(req.Payload) != 2 || !validNginxConfigName(req.Payload["config"]) || req.Payload["action"] != "enable" && req.Payload["action"] != "disable" {
			return fmt.Errorf("agent hub: domain.action requires a valid config and action: %w", ErrInvalidInput)
		}
	case TaskSSLInventory:
		if len(req.Payload) != 0 {
			return fmt.Errorf("agent hub: ssl.inventory does not accept payload fields: %w", ErrInvalidInput)
		}
	case TaskSSLAction:
		if len(req.Payload) != 2 || !validNginxConfigName(req.Payload["name"]) || req.Payload["action"] != "check" && req.Payload["action"] != "renew" {
			return fmt.Errorf("agent hub: ssl.action requires a valid certificate name and action: %w", ErrInvalidInput)
		}
	case TaskDatabaseInventory:
		if len(req.Payload) != 0 {
			return fmt.Errorf("agent hub: database.inventory does not accept payload fields: %w", ErrInvalidInput)
		}
	case TaskDatabaseAction:
		if len(req.Payload) != 2 || !validDatabaseEngine(req.Payload["engine"]) || req.Payload["action"] != "restart" {
			return fmt.Errorf("agent hub: database.action requires a valid engine and restart action: %w", ErrInvalidInput)
		}
	case TaskBackupInventory:
		if len(req.Payload) != 0 {
			return fmt.Errorf("agent hub: backup.inventory does not accept payload fields: %w", ErrInvalidInput)
		}
	case TaskBackupRun:
		if len(req.Payload) != 1 || !validNginxConfigName(req.Payload["plan"]) {
			return fmt.Errorf("agent hub: backup.run requires a valid plan ID: %w", ErrInvalidInput)
		}
	case TaskFilesBrowse, TaskFilesRead:
		if len(req.Payload) != 1 || !validManagedAbsolutePath(req.Payload["path"]) {
			return fmt.Errorf("agent hub: file read requires a valid absolute path: %w", ErrInvalidInput)
		}
	case TaskFilesWrite:
		if len(req.Payload) != 3 || !validManagedAbsolutePath(req.Payload["path"]) || !validSHA256(req.Payload["checksum"]) {
			return fmt.Errorf("agent hub: files.write requires valid path, content, and checksum fields: %w", ErrInvalidInput)
		}
		content, err := base64.StdEncoding.DecodeString(req.Payload["content_b64"])
		if err != nil || len(content) > maxNginxConfigBytes || !validNginxConfigContent(content) {
			return fmt.Errorf("agent hub: files.write content is invalid or too large: %w", ErrInvalidInput)
		}
	case TaskDeployInventory:
		if len(req.Payload) != 0 {
			return fmt.Errorf("agent hub: deploy.inventory does not accept payload fields: %w", ErrInvalidInput)
		}
	case TaskDeployAction:
		if len(req.Payload) != 2 || !validNginxConfigName(req.Payload["target"]) || !validDeployAction(req.Payload["action"]) {
			return fmt.Errorf("agent hub: deploy.action requires a valid target and action: %w", ErrInvalidInput)
		}
	case TaskDeployDomainInventory:
		if len(req.Payload) != 1 || !validNginxConfigName(req.Payload["target"]) {
			return fmt.Errorf("agent hub: deploy.domain.inventory requires a valid target: %w", ErrInvalidInput)
		}
	case TaskDeployDomainHealth:
		if len(req.Payload) != 2 || !validNginxConfigName(req.Payload["target"]) || !validDeployDomainName(req.Payload["domain"]) {
			return fmt.Errorf("agent hub: deploy.domain.health requires a valid target and domain: %w", ErrInvalidInput)
		}
	case TaskDeployDomainAction:
		action := req.Payload["action"]
		if action == "ensure" {
			if len(req.Payload) != 4 || !validNginxConfigName(req.Payload["target"]) || !validDeployDomainName(req.Payload["domain"]) ||
				!validSHA256(req.Payload["expected_revision"]) && req.Payload["expected_revision"] != "absent" {
				return fmt.Errorf("agent hub: deploy.domain.action ensure requires a valid target, domain, and expected revision: %w", ErrInvalidInput)
			}
			for key := range req.Payload {
				if key != "target" && key != "domain" && key != "action" && key != "expected_revision" {
					return fmt.Errorf("agent hub: deploy.domain.action ensure has an invalid field: %w", ErrInvalidInput)
				}
			}
			break
		}
		minimumFields := 3
		if action == "tls-enable" && req.Payload["email"] != "" {
			minimumFields = 4
		}
		if len(req.Payload) != minimumFields || !validNginxConfigName(req.Payload["target"]) || !validDeployDomainName(req.Payload["domain"]) || !validDeployDomainAction(action) {
			return fmt.Errorf("agent hub: deploy.domain.action requires a valid target, domain, and fixed action: %w", ErrInvalidInput)
		}
	case TaskAgentUpdateStatus:
		if len(req.Payload) != 0 {
			return fmt.Errorf("agent hub: agent.update.status does not accept payload fields: %w", ErrInvalidInput)
		}
	case TaskAgentUpdateAction:
		action := req.Payload["action"]
		switch action {
		case "upgrade":
			if len(req.Payload) != 2 || releaseversion.Compare(req.Payload["version"], req.Payload["version"]) != releaseversion.Current {
				return fmt.Errorf("agent hub: agent.update.action upgrade requires an exact stable version: %w", ErrInvalidInput)
			}
		case "rollback":
			if len(req.Payload) != 1 {
				return fmt.Errorf("agent hub: agent.update.action rollback accepts only the action field: %w", ErrInvalidInput)
			}
		default:
			return fmt.Errorf("agent hub: agent.update.action requires upgrade or rollback: %w", ErrInvalidInput)
		}
	case TaskIntegrationStatus:
		if len(req.Payload) != 0 {
			return fmt.Errorf("agent hub: integration.status does not accept payload fields: %w", ErrInvalidInput)
		}
	case TaskMetricsRead:
		if len(req.Payload) != 0 {
			return fmt.Errorf("agent hub: metrics.read does not accept payload fields: %w", ErrInvalidInput)
		}
	}
	return nil
}

// ValidateTaskResultRequest validates the result state and keeps error text
// bounded before it is stored in SQLite.
func ValidateTaskResultRequest(req TaskResultRequest) error {
	if req.Status != TaskStatusCompleted && req.Status != TaskStatusFailed {
		return fmt.Errorf("agent hub: task result status: %w", ErrInvalidInput)
	}
	if len(req.Error) > maxTaskErrorLength || containsControl(req.Error) {
		return fmt.Errorf("agent hub: task result error: %w", ErrInvalidInput)
	}
	if req.Status == TaskStatusFailed && strings.TrimSpace(req.Error) == "" {
		return fmt.Errorf("agent hub: failed task requires error: %w", ErrInvalidInput)
	}
	if len(req.Result) > 100 {
		return fmt.Errorf("agent hub: task result: %w", ErrInvalidInput)
	}
	for key, value := range req.Result {
		if key == "" || len(key) > maxTaskPayloadValueLength || containsControl(key) ||
			len(value) > maxTaskResultValueLength || containsControl(value) {
			return fmt.Errorf("agent hub: task result: %w", ErrInvalidInput)
		}
	}
	return nil
}

// ValidateIntegrationStatusTaskResult applies the stricter result contract to
// the integration.status task without changing validation for older task
// kinds. A completed result must be exactly one bounded typed JSON value; a
// failed result may carry only the fixed safe fatal code.
func ValidateIntegrationStatusTaskResult(req TaskResultRequest) error {
	switch req.Status {
	case TaskStatusCompleted:
		if req.Error != "" || len(req.Result) != 1 {
			return fmt.Errorf("agent hub: integration.status completed result is invalid: %w", ErrInvalidInput)
		}
		data, ok := req.Result["data"]
		if !ok || len(data) == 0 {
			return fmt.Errorf("agent hub: integration.status result data is required: %w", ErrInvalidInput)
		}
		if _, err := managedintegrationstatus.Decode([]byte(data)); err != nil {
			return fmt.Errorf("agent hub: integration.status result data is invalid: %w", ErrInvalidInput)
		}
	case TaskStatusFailed:
		if req.Error != managedintegrationstatus.FatalErrorCode || len(req.Result) != 0 {
			return fmt.Errorf("agent hub: integration.status failed result is invalid: %w", ErrInvalidInput)
		}
	default:
		return fmt.Errorf("agent hub: integration.status task result status is invalid: %w", ErrInvalidInput)
	}
	return nil
}

// ValidateProfileApplyTaskResult applies a closed result contract to
// agent.profile.apply. A completed task is only a transport acknowledgement;
// the applied state still comes from a subsequent heartbeat observation.
func ValidateProfileApplyTaskResult(task Task, req TaskResultRequest) error {
	if task.Kind != TaskProfileApply {
		return fmt.Errorf("agent hub: profile apply result has wrong task kind: %w", ErrInvalidInput)
	}
	revision, err := profileApplyPayloadRevisionFromTask(task)
	if err != nil {
		return fmt.Errorf("agent hub: profile apply task payload is invalid: %w", ErrInvalidInput)
	}
	if err := ValidateTaskResultRequest(req); err != nil {
		return err
	}
	switch req.Status {
	case TaskStatusCompleted:
		if req.Error != "" {
			return fmt.Errorf("agent hub: profile apply completed result cannot carry an error: %w", ErrInvalidInput)
		}
		seenRevision := false
		seenState := false
		for key, value := range req.Result {
			switch key {
			case "revision":
				if value != strconv.FormatInt(revision, 10) {
					return fmt.Errorf("agent hub: profile apply result revision is invalid: %w", ErrInvalidInput)
				}
				seenRevision = true
			case "state":
				if !validProfileApplyResultState(value) {
					return fmt.Errorf("agent hub: profile apply result state is invalid: %w", ErrInvalidInput)
				}
				seenState = true
			case "error_code":
				if !isSafeProfileErrorCode(value) {
					return fmt.Errorf("agent hub: profile apply result error code is invalid: %w", ErrInvalidInput)
				}
			default:
				return fmt.Errorf("agent hub: profile apply result field is invalid: %w", ErrInvalidInput)
			}
		}
		if !seenRevision || !seenState {
			return fmt.Errorf("agent hub: profile apply completed result is incomplete: %w", ErrInvalidInput)
		}
	case TaskStatusFailed:
		if len(req.Result) != 0 || !isSafeProfileErrorCode(req.Error) || req.Error == "" {
			return fmt.Errorf("agent hub: profile apply failed result must use a safe error code: %w", ErrInvalidInput)
		}
	default:
		return fmt.Errorf("agent hub: profile apply result status is invalid: %w", ErrInvalidInput)
	}
	return nil
}

// ValidateAgentProfileTaskResult is a descriptive alias for integrations
// that use the domain name rather than the wire task name.
func ValidateAgentProfileTaskResult(task Task, req TaskResultRequest) error {
	return ValidateProfileApplyTaskResult(task, req)
}

func profileApplyPayloadRevisionFromTask(task Task) (int64, error) {
	payloadJSON, err := json.Marshal(task.Payload)
	if err != nil {
		return 0, err
	}
	return profileApplyPayloadRevision(string(payloadJSON))
}

func validProfileApplyResultState(value string) bool {
	switch value {
	case ProfileApplyResultRestartScheduled, ProfileApplyResultAlreadyActive:
		return true
	default:
		return false
	}
}

func validateRegisterNodeRequest(req RegisterNodeRequest) error {
	if !validNodeID(req.ID) {
		return fmt.Errorf("agent hub: node id: %w", ErrInvalidInput)
	}
	if strings.TrimSpace(req.Name) == "" || len(req.Name) > maxNodeNameLength || containsControl(req.Name) {
		return fmt.Errorf("agent hub: node name: %w", ErrInvalidInput)
	}
	return nil
}

func validateHeartbeatFields(req HeartbeatRequest) error {
	if !validNodeID(req.NodeID) {
		return ErrInvalidInput
	}
	if err := validateAgentProfileObservation(req.Profile); err != nil {
		return err
	}
	if len(req.Hostname) > maxHostnameLength || containsControl(req.Hostname) {
		return fmt.Errorf("agent hub: heartbeat hostname: %w", ErrInvalidInput)
	}
	if len(req.AgentVersion) > maxAgentVersionLength || containsControl(req.AgentVersion) {
		return fmt.Errorf("agent hub: heartbeat agent version: %w", ErrInvalidInput)
	}
	if len(req.Inventory.Arch) > 32 || containsControl(req.Inventory.Arch) {
		return fmt.Errorf("agent hub: heartbeat architecture: %w", ErrInvalidInput)
	}
	seenCapabilities := make(map[string]struct{}, len(req.Capabilities))
	for _, capability := range req.Capabilities {
		switch capability {
		case CapabilityInventory, CapabilityServiceStatus, CapabilityServiceAction, CapabilityHostAction, CapabilityProcessRead, CapabilityProcessSignal, CapabilityTerminal, CapabilityDiskCleanup, CapabilityLogsRead, CapabilityContainerRead, CapabilityContainerAction, CapabilityNginxAction, CapabilityNginxConfigRead, CapabilityNginxConfigWrite, CapabilityPHPRead, CapabilityPHPWrite, CapabilityPHPAction, CapabilityPM2Read, CapabilityPM2Action, CapabilityCronRead, CapabilityCronWrite, CapabilityCronRun, CapabilityFirewallRead, CapabilityFirewallWrite, CapabilityDomainRead, CapabilityDomainAction, CapabilitySSLRead, CapabilitySSLAction, CapabilityDatabaseRead, CapabilityDatabaseAction, CapabilityBackupRead, CapabilityBackupRun, CapabilityFilesRead, CapabilityFilesWrite, CapabilityDeployRead, CapabilityDeployAction, CapabilityDeployDomainRead, CapabilityDeployDomainAction, CapabilityAgentUpdateRead, CapabilityAgentUpdateAction, CapabilityIntegrationStatus, CapabilityMetricsRead, CapabilityProfileApply:
		default:
			return fmt.Errorf("agent hub: unsupported capability %q: %w", capability, ErrInvalidInput)
		}
		if _, exists := seenCapabilities[capability]; exists {
			return fmt.Errorf("agent hub: duplicate capability %q: %w", capability, ErrInvalidInput)
		}
		seenCapabilities[capability] = struct{}{}
	}
	if math.IsNaN(req.Inventory.DiskUsePercent) || math.IsInf(req.Inventory.DiskUsePercent, 0) ||
		req.Inventory.DiskUsePercent < 0 || req.Inventory.DiskUsePercent > 100 {
		return fmt.Errorf("agent hub: heartbeat disk use percent: %w", ErrInvalidInput)
	}
	if req.Inventory.DiskUsed > req.Inventory.DiskTotal ||
		req.Inventory.DiskAvailable > req.Inventory.DiskTotal ||
		req.Inventory.DiskUsed > req.Inventory.DiskTotal-req.Inventory.DiskAvailable {
		return fmt.Errorf("agent hub: heartbeat disk inventory: %w", ErrInvalidInput)
	}
	if len(req.Inventory.DiskMounts) > 64 {
		return fmt.Errorf("agent hub: heartbeat disk mounts: %w", ErrInvalidInput)
	}
	for _, mount := range req.Inventory.DiskMounts {
		if mount.Filesystem == "" || mount.Mountpoint == "" || len(mount.Filesystem) > 255 || len(mount.Mountpoint) > 512 ||
			containsControl(mount.Filesystem) || containsControl(mount.Mountpoint) || mount.UsePercent < 0 || mount.UsePercent > 100 ||
			mount.Used > mount.Size || mount.Available > mount.Size || mount.Used > mount.Size-mount.Available {
			return fmt.Errorf("agent hub: heartbeat disk mounts: %w", ErrInvalidInput)
		}
	}
	if req.Inventory.SwapUsed > req.Inventory.SwapTotal ||
		req.Inventory.SwapFree > req.Inventory.SwapTotal ||
		req.Inventory.SwapUsed > req.Inventory.SwapTotal-req.Inventory.SwapFree {
		return fmt.Errorf("agent hub: heartbeat swap inventory: %w", ErrInvalidInput)
	}
	if len(req.Inventory.Processes) > 50 {
		return fmt.Errorf("agent hub: heartbeat process inventory: %w", ErrInvalidInput)
	}
	if len(req.Inventory.LogSources) > 7 {
		return fmt.Errorf("agent hub: heartbeat log sources: %w", ErrInvalidInput)
	}
	seenLogSources := make(map[string]struct{}, len(req.Inventory.LogSources))
	for _, source := range req.Inventory.LogSources {
		if !validLogSource(source) {
			return fmt.Errorf("agent hub: heartbeat log sources: %w", ErrInvalidInput)
		}
		if _, exists := seenLogSources[source]; exists {
			return fmt.Errorf("agent hub: duplicate heartbeat log source %q: %w", source, ErrInvalidInput)
		}
		seenLogSources[source] = struct{}{}
	}
	if !validManagedFileRootInventory(req.Inventory.FileReadRoots, nil) ||
		!validManagedFileRootInventory(req.Inventory.FileWriteRoots, req.Inventory.FileReadRoots) {
		return fmt.Errorf("agent hub: heartbeat managed file roots: %w", ErrInvalidInput)
	}
	for _, process := range req.Inventory.Processes {
		if process.PID <= 0 || process.StartTime == 0 || len(process.User) > 64 || len(process.Command) > 512 ||
			containsControl(process.User) || containsControl(process.Command) ||
			math.IsNaN(process.CPU) || math.IsInf(process.CPU, 0) || process.CPU < 0 ||
			math.IsNaN(process.Memory) || math.IsInf(process.Memory, 0) || process.Memory < 0 {
			return fmt.Errorf("agent hub: heartbeat process inventory: %w", ErrInvalidInput)
		}
	}
	inventory, err := json.Marshal(req.Inventory)
	if err != nil {
		return fmt.Errorf("agent hub: heartbeat inventory: %w", ErrInvalidInput)
	}
	if len(inventory) > maxHeartbeatInventorySize {
		return fmt.Errorf("agent hub: heartbeat inventory exceeds size limit: %w", ErrInvalidInput)
	}
	return nil
}

func validManagedFileRootInventory(roots, allowedParents []string) bool {
	if len(roots) > 16 {
		return false
	}
	allowed := make(map[string]struct{}, len(allowedParents))
	for _, root := range allowedParents {
		allowed[root] = struct{}{}
	}
	seen := make(map[string]struct{}, len(roots))
	for _, root := range roots {
		if root == "/" || len(root) > 512 || containsControl(root) || !filepath.IsAbs(root) || filepath.Clean(root) != root {
			return false
		}
		if _, exists := seen[root]; exists {
			return false
		}
		if allowedParents != nil {
			if _, exists := allowed[root]; !exists {
				return false
			}
		}
		seen[root] = struct{}{}
	}
	return true
}

func validDiskCleanupTargets(value string) bool {
	parts := strings.Split(value, ",")
	if len(parts) == 0 || len(parts) > 4 {
		return false
	}
	seen := make(map[string]struct{}, len(parts))
	for _, target := range parts {
		switch target {
		case "apt-cache", "journal", "tmp-old", "rotated-logs":
		default:
			return false
		}
		if _, exists := seen[target]; exists {
			return false
		}
		seen[target] = struct{}{}
	}
	return true
}

func validLogSource(value string) bool {
	switch value {
	case "system", "nginx", "php", "mariadb", "postgresql", "pm2", "docker":
		return true
	default:
		return false
	}
}

func validContainerName(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for index, char := range value {
		if asciiLetterOrDigit(char) || (index > 0 && strings.ContainsRune("_.-", char)) {
			continue
		}
		return false
	}
	return true
}

func validContainerAction(value string) bool {
	return value == "start" || value == "stop" || value == "restart"
}

func validDeployAction(value string) bool {
	return value == "preflight" || value == "deploy" || value == "restart" || value == "rollback"
}

func validDeployDomainAction(value string) bool {
	return value == "create" || value == "delete" || value == "ensure" || value == "tls-enable" || value == "tls-disable" || value == "tls-renew"
}

func validDeployDomainName(value string) bool {
	if value == "" || len(value) > 253 || strings.HasPrefix(value, ".") || strings.HasSuffix(value, ".") {
		return false
	}
	labels := strings.Split(value, ".")
	if len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, char := range label {
			if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '-' {
				continue
			}
			return false
		}
	}
	return true
}

func validNginxConfigName(value string) bool {
	if value == "" || len(value) > 255 {
		return false
	}
	for index, char := range value {
		if asciiLetterOrDigit(char) || (index > 0 && strings.ContainsRune("._-", char)) {
			continue
		}
		return false
	}
	return true
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, char := range value {
		if (char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') {
			continue
		}
		return false
	}
	return true
}

func validNginxConfigContent(content []byte) bool {
	if !utf8.Valid(content) {
		return false
	}
	for _, value := range content {
		if value < 0x20 && value != '\n' && value != '\r' && value != '\t' {
			return false
		}
	}
	return true
}

func validPHPVersion(value string) bool {
	parts := strings.Split(value, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return false
	}
	for _, part := range parts {
		for _, char := range part {
			if char < '0' || char > '9' {
				return false
			}
		}
	}
	return len(value) <= 16
}

func validPHPPool(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for index, char := range value {
		if asciiLetterOrDigit(char) || (index > 0 && strings.ContainsRune("._-", char)) {
			continue
		}
		return false
	}
	return true
}

func validPHPAction(value string) bool {
	return value == "test" || value == "reload" || value == "restart"
}

func validPM2Name(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for index, char := range value {
		if asciiLetterOrDigit(char) || (index > 0 && strings.ContainsRune("._:@-", char)) {
			continue
		}
		return false
	}
	return true
}

func validPM2Action(value string) bool {
	return value == "start" || value == "stop" || value == "restart" || value == "reload"
}

func validDatabaseEngine(value string) bool {
	return value == "mariadb" || value == "postgresql"
}

func validManagedAbsolutePath(value string) bool {
	return len(value) > 1 && len(value) <= maxManagedPathLength && filepath.IsAbs(value) && filepath.Clean(value) == value && !strings.ContainsRune(value, '\x00')
}

type cronTaskJob struct {
	ID          string `json:"id"`
	Schedule    string `json:"schedule"`
	User        string `json:"user"`
	Command     string `json:"command"`
	Description string `json:"description,omitempty"`
	Enabled     bool   `json:"enabled"`
}

func validCronJobPayload(encoded string, requireID bool) bool {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(raw) == 0 || len(raw) > maxCronJobBytes || !utf8.Valid(raw) {
		return false
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || len(fields) < 4 || len(fields) > 6 {
		return false
	}
	for key := range fields {
		switch key {
		case "id", "schedule", "user", "command", "description", "enabled":
		default:
			return false
		}
	}
	var job cronTaskJob
	if err := json.Unmarshal(raw, &job); err != nil {
		return false
	}
	for _, required := range []string{"schedule", "user", "command", "enabled"} {
		if _, ok := fields[required]; !ok {
			return false
		}
	}
	if (requireID && !validCronID(job.ID)) || (!requireID && job.ID != "") || !validSystemUser(job.User) || len(job.Command) == 0 || len(job.Command) > 4096 || strings.ContainsAny(job.Command, "\r\n\x00") || len(job.Description) > 160 || strings.ContainsAny(job.Description, "\r\n\x00") {
		return false
	}
	return len(job.Schedule) <= 160 && !strings.ContainsAny(job.Schedule, "\r\n\x00") && len(strings.Fields(job.Schedule)) == 5
}

func validCronID(value string) bool {
	if len(value) != len("cron-")+12 || !strings.HasPrefix(value, "cron-") {
		return false
	}
	for _, char := range value[len("cron-"):] {
		if (char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') {
			continue
		}
		return false
	}
	return true
}

type firewallTaskRule struct {
	Action   string `json:"action"`
	Protocol string `json:"protocol"`
	Port     int    `json:"port,omitempty"`
	Source   string `json:"source,omitempty"`
	Comment  string `json:"comment,omitempty"`
}

func validFirewallRulePayload(encoded string) bool {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(raw) == 0 || len(raw) > maxFirewallRuleBytes || !utf8.Valid(raw) {
		return false
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || len(fields) < 2 || len(fields) > 5 {
		return false
	}
	for key := range fields {
		switch key {
		case "action", "protocol", "port", "source", "comment":
		default:
			return false
		}
	}
	if _, ok := fields["action"]; !ok {
		return false
	}
	if _, ok := fields["protocol"]; !ok {
		return false
	}
	var rule firewallTaskRule
	if json.Unmarshal(raw, &rule) != nil || rule.Action != "ACCEPT" && rule.Action != "DROP" || rule.Protocol != "tcp" && rule.Protocol != "udp" && rule.Protocol != "all" {
		return false
	}
	if rule.Protocol == "all" && rule.Port != 0 || rule.Protocol != "all" && (rule.Port < 0 || rule.Port > 65535) || len(rule.Comment) > 80 || strings.ContainsAny(rule.Comment, "\r\n\x00") {
		return false
	}
	if rule.Source == "" {
		return true
	}
	if ip := net.ParseIP(rule.Source); ip != nil {
		return ip.To4() != nil
	}
	ip, _, err := net.ParseCIDR(rule.Source)
	return err == nil && ip.To4() != nil
}

func validFirewallID(value string) bool {
	if len(value) != len("fw-")+12 || !strings.HasPrefix(value, "fw-") {
		return false
	}
	for _, char := range value[len("fw-"):] {
		if (char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') {
			continue
		}
		return false
	}
	return true
}

func validSystemUser(value string) bool {
	if value == "" || len(value) > 32 {
		return false
	}
	for index, char := range value {
		if (char >= 'a' && char <= 'z') || char == '_' || (index > 0 && ((char >= '0' && char <= '9') || char == '-')) {
			continue
		}
		return false
	}
	return true
}

func validNodeID(value string) bool {
	if value == "" || len(value) > maxNodeIDLength || strings.TrimSpace(value) != value {
		return false
	}
	for index, r := range value {
		if index == 0 {
			if !asciiLetterOrDigit(r) {
				return false
			}
			continue
		}
		if !asciiLetterOrDigit(r) && r != '.' && r != '_' && r != '-' {
			return false
		}
	}
	return true
}

func validUnitName(value string) bool {
	if value == "" || len(value) > maxTaskPayloadValueLength || strings.TrimSpace(value) != value {
		return false
	}
	for index, r := range value {
		if index == 0 {
			if !asciiLetterOrDigit(r) {
				return false
			}
			continue
		}
		if (!asciiLetterOrDigit(r) && !strings.ContainsRune(":_.@-", r)) ||
			unicode.IsSpace(r) || unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func asciiLetterOrDigit(value rune) bool {
	return (value >= 'a' && value <= 'z') || (value >= 'A' && value <= 'Z') ||
		(value >= '0' && value <= '9')
}

func safeToken(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-' {
			return false
		}
	}
	return true
}

func containsControl(value string) bool {
	for _, r := range value {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

func heartbeatAge(now, sentAt time.Time) time.Duration {
	if sentAt.IsZero() {
		return HeartbeatFreshnessWindow + time.Nanosecond
	}
	if sentAt.After(now) {
		return sentAt.Sub(now)
	}
	return now.Sub(sentAt)
}

func (s *Service) currentTime() time.Time {
	if s != nil && s.now != nil {
		return s.now().UTC()
	}
	return time.Now().UTC()
}
