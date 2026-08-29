package agenthub

import (
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func newTestService(t *testing.T) (*Service, *sql.DB, time.Time) {
	t.Helper()
	dsn := "file:agenthub_" + t.Name() + "?mode=memory&cache=shared&_foreign_keys=on&_busy_timeout=5000"
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	now := time.Date(2026, time.August, 21, 4, 0, 0, 123456789, time.UTC)
	service := NewService(NewRepository(db))
	service.now = func() time.Time { return now }
	return service, db, now
}

func registerTestNode(t *testing.T, service *Service, id string) RegisterNodeResponse {
	t.Helper()
	registered, err := service.RegisterNode(RegisterNodeRequest{ID: id, Name: id})
	if err != nil {
		t.Fatalf("RegisterNode: %v", err)
	}
	return registered
}

func TestMigrateIsIdempotentAndCreatesAgentTables(t *testing.T) {
	service, db, _ := newTestService(t)
	if service == nil {
		t.Fatal("service is nil")
	}
	if err := Migrate(db); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	for _, table := range []string{"agent_nodes", "agent_tasks"} {
		var got string
		if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&got); err != nil {
			t.Fatalf("table %s: %v", table, err)
		}
		if got != table {
			t.Fatalf("table name: got %q, want %q", got, table)
		}
	}
}

func TestMigrateAddsCapabilitiesToExistingNodeTable(t *testing.T) {
	db, err := sql.Open("sqlite3", "file:agenthub_legacy_capabilities?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE agent_nodes (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		hostname TEXT NOT NULL DEFAULT '',
		agent_version TEXT NOT NULL DEFAULT '',
		protocol_version TEXT NOT NULL DEFAULT '',
		inventory_json TEXT NOT NULL DEFAULT '{}',
		token_hash TEXT NOT NULL,
		last_seen_at TEXT,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`); err != nil {
		t.Fatalf("create legacy agent_nodes: %v", err)
	}
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate legacy schema: %v", err)
	}
	var found bool
	rows, err := db.Query(`PRAGMA table_info(agent_nodes)`)
	if err != nil {
		t.Fatalf("table_info: %v", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatalf("scan column: %v", err)
		}
		found = found || name == "capabilities_json"
	}
	if !found {
		t.Fatal("capabilities_json column was not added")
	}
}

func TestRegisterNodeStoresOnlySHA256TokenHashAndAuthenticatesConstantly(t *testing.T) {
	service, db, _ := newTestService(t)
	registered := registerTestNode(t, service, "contabo")
	if len(registered.Token) != 64 {
		t.Fatalf("token length: got %d, want 64 hex characters", len(registered.Token))
	}
	if registered.Node.ID != "contabo" || registered.Node.Name != "contabo" {
		t.Fatalf("registered node: %#v", registered.Node)
	}

	digest := sha256.Sum256([]byte(registered.Token))
	wantHash := hex.EncodeToString(digest[:])
	var storedHash string
	if err := db.QueryRow(`SELECT token_hash FROM agent_nodes WHERE id = ?`, "contabo").Scan(&storedHash); err != nil {
		t.Fatalf("token hash query: %v", err)
	}
	if storedHash != wantHash {
		t.Fatalf("stored token hash: got %q, want SHA-256 %q", storedHash, wantHash)
	}
	if storedHash == registered.Token {
		t.Fatal("plaintext token was stored")
	}

	if _, err := service.AuthenticateNode("contabo", registered.Token); err != nil {
		t.Fatalf("AuthenticateNode(valid): %v", err)
	}
	if _, err := service.AuthenticateNode("contabo", registered.Token+"x"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("AuthenticateNode(wrong token): got %v, want ErrUnauthorized", err)
	}
	if _, err := service.AuthenticateNode("unknown", registered.Token); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("AuthenticateNode(unknown node): got %v, want ErrUnauthorized", err)
	}
	if _, err := service.RegisterNode(RegisterNodeRequest{ID: "contabo", Name: "again"}); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("duplicate RegisterNode: got %v, want ErrAlreadyExists", err)
	}
}

func TestHeartbeatValidatesProtocolFreshnessAndPersistsSnapshot(t *testing.T) {
	service, _, now := newTestService(t)
	registered := registerTestNode(t, service, "contabo")
	req := HeartbeatRequest{
		ProtocolVersion: ProtocolVersion,
		NodeID:          "contabo",
		AgentVersion:    "agent-1.2.3",
		Capabilities:    []string{CapabilityInventory, CapabilityServiceStatus, CapabilityServiceAction},
		Hostname:        "contabo.example",
		SentAt:          now.Add(-30 * time.Second),
		Inventory: Inventory{
			OS:             "Ubuntu",
			Arch:           "arm64",
			Kernel:         "6.8",
			BootID:         "boot-1",
			UptimeSeconds:  900,
			MemoryTotal:    100,
			DiskTotal:      500,
			DiskUsed:       100,
			DiskAvailable:  350,
			DiskUsePercent: 23,
			Services:       []ServiceState{{Name: "nginx", Active: "active", Sub: "running"}},
			FileReadRoots:  []string{"/srv/apps"},
			FileWriteRoots: []string{"/srv/apps"},
		},
	}
	response, err := service.Heartbeat("contabo", registered.Token, req)
	if err != nil {
		t.Fatalf("Heartbeat(valid): %v", err)
	}
	if !response.Accepted || !response.ServerAt.Equal(now) {
		t.Fatalf("heartbeat response: %#v", response)
	}
	node, err := service.GetNode("contabo")
	if err != nil {
		t.Fatalf("GetNode after heartbeat: %v", err)
	}
	if node.Hostname != req.Hostname || node.AgentVersion != req.AgentVersion || node.ProtocolVersion != ProtocolVersion {
		t.Fatalf("persisted heartbeat fields: %#v", node)
	}
	if len(node.Capabilities) != 3 || node.Capabilities[2] != CapabilityServiceAction {
		t.Fatalf("persisted capabilities: %#v", node.Capabilities)
	}
	if node.LastSeenAt == nil || !node.LastSeenAt.Equal(now) {
		t.Fatalf("last seen: got %v, want %v", node.LastSeenAt, now)
	}
	if node.Inventory.OS != "Ubuntu" || node.Inventory.Arch != "arm64" || node.Inventory.DiskUsed != 100 || node.Inventory.DiskUsePercent != 23 ||
		len(node.Inventory.FileReadRoots) != 1 || node.Inventory.FileReadRoots[0] != "/srv/apps" || len(node.Inventory.FileWriteRoots) != 1 ||
		len(node.Inventory.Services) != 1 || node.Inventory.Services[0].Active != "active" {
		t.Fatalf("persisted inventory: %#v", node.Inventory)
	}

	tooOld := req
	tooOld.SentAt = now.Add(-HeartbeatFreshnessWindow - time.Nanosecond)
	if _, err := service.Heartbeat("contabo", registered.Token, tooOld); !errors.Is(err, ErrStaleHeartbeat) {
		t.Fatalf("stale heartbeat: got %v, want ErrStaleHeartbeat", err)
	}
	tooNew := req
	tooNew.SentAt = now.Add(HeartbeatFreshnessWindow + time.Nanosecond)
	if _, err := service.Heartbeat("contabo", registered.Token, tooNew); !errors.Is(err, ErrStaleHeartbeat) {
		t.Fatalf("future heartbeat: got %v, want ErrStaleHeartbeat", err)
	}
	wrongProtocol := req
	wrongProtocol.ProtocolVersion = "v99"
	if _, err := service.Heartbeat("contabo", registered.Token, wrongProtocol); !errors.Is(err, ErrUnsupportedProtocol) {
		t.Fatalf("wrong protocol: got %v, want ErrUnsupportedProtocol", err)
	}
	wrongNode := req
	wrongNode.NodeID = "other"
	if _, err := service.Heartbeat("contabo", registered.Token, wrongNode); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("wrong node ID: got %v, want ErrUnauthorized", err)
	}
	unknownCapability := req
	unknownCapability.Capabilities = []string{"arbitrary.shell"}
	if _, err := service.Heartbeat("contabo", registered.Token, unknownCapability); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unknown capability: got %v, want ErrInvalidInput", err)
	}
	duplicateCapability := req
	duplicateCapability.Capabilities = []string{CapabilityInventory, CapabilityInventory}
	if _, err := service.Heartbeat("contabo", registered.Token, duplicateCapability); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("duplicate capability: got %v, want ErrInvalidInput", err)
	}
	invalidArchitecture := req
	invalidArchitecture.Inventory.Arch = "amd64\nspoofed"
	if _, err := service.Heartbeat("contabo", registered.Token, invalidArchitecture); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid architecture: got %v, want ErrInvalidInput", err)
	}
	oversized := req
	oversized.Inventory.OS = strings.Repeat("x", maxHeartbeatInventorySize)
	if _, err := service.Heartbeat("contabo", registered.Token, oversized); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("oversized inventory: got %v, want ErrInvalidInput", err)
	}

	for name, mutate := range map[string]func(*HeartbeatRequest){
		"percentage above 100": func(req *HeartbeatRequest) { req.Inventory.DiskUsePercent = 101 },
		"used exceeds total":   func(req *HeartbeatRequest) { req.Inventory.DiskUsed = 501 },
		"available exceeds total": func(req *HeartbeatRequest) {
			req.Inventory.DiskAvailable = 501
		},
		"used plus available exceeds total": func(req *HeartbeatRequest) {
			req.Inventory.DiskUsed = 200
			req.Inventory.DiskAvailable = 350
		},
		"relative managed file root": func(req *HeartbeatRequest) {
			req.Inventory.FileReadRoots = []string{"srv/apps"}
		},
		"write root outside read roots": func(req *HeartbeatRequest) {
			req.Inventory.FileWriteRoots = []string{"/opt/apps"}
		},
	} {
		t.Run("invalid inventory/"+name, func(t *testing.T) {
			invalid := req
			mutate(&invalid)
			if _, err := service.Heartbeat("contabo", registered.Token, invalid); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("Heartbeat: got %v, want ErrInvalidInput", err)
			}
		})
	}

	legacy := req
	legacy.AgentVersion = "agent-1.0.0"
	legacy.Inventory.DiskUsed = 0
	legacy.Inventory.DiskUsePercent = 0
	if _, err := service.Heartbeat("contabo", registered.Token, legacy); err != nil {
		t.Fatalf("Heartbeat(legacy disk fields): %v", err)
	}
}

func TestTaskAllowlistAtomicClaimAndResultCompletion(t *testing.T) {
	service, _, now := newTestService(t)
	registered := registerTestNode(t, service, "contabo")
	if _, err := service.Heartbeat("contabo", registered.Token, HeartbeatRequest{
		ProtocolVersion: ProtocolVersion,
		NodeID:          "contabo",
		AgentVersion:    "agent-test",
		Capabilities:    []string{CapabilityServiceStatus, CapabilityServiceAction},
		Hostname:        "node.example",
		SentAt:          now,
	}); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if _, err := service.EnqueueTask("contabo", TaskRequest{Kind: "shell", Payload: map[string]string{"command": "id"}}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unsupported task kind: got %v, want ErrInvalidInput", err)
	}
	if _, err := service.EnqueueTask("contabo", TaskRequest{Kind: TaskServiceStatus, Payload: map[string]string{"service": "nginx.service", "extra": "no"}}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("extra status payload: got %v, want ErrInvalidInput", err)
	}
	if _, err := service.EnqueueTask("contabo", TaskRequest{Kind: TaskServiceStatus, Payload: map[string]string{"unit": "nginx.service"}}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unit status payload: got %v, want ErrInvalidInput", err)
	}
	if _, err := service.EnqueueTask("contabo", TaskRequest{Kind: TaskServiceAction, Payload: map[string]string{"service": "nginx.service", "action": "reload"}}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unsupported action: got %v, want ErrInvalidInput", err)
	}

	enqueued, err := service.EnqueueTask("contabo", TaskRequest{
		Kind:    TaskServiceAction,
		Payload: map[string]string{"service": "nginx.service", "action": "restart"},
	})
	if err != nil {
		t.Fatalf("EnqueueTask: %v", err)
	}
	if enqueued.Status != TaskStatusQueued || enqueued.NodeID != "contabo" {
		t.Fatalf("queued task: %#v", enqueued)
	}

	claimed, err := service.PollTask("contabo", registered.Token)
	if err != nil {
		t.Fatalf("PollTask: %v", err)
	}
	if claimed == nil || claimed.ID != enqueued.ID || claimed.Status != TaskStatusRunning || claimed.StartedAt == nil {
		t.Fatalf("claimed task: %#v", claimed)
	}
	if next, err := service.PollTask("contabo", registered.Token); err != nil {
		t.Fatalf("second PollTask: %v", err)
	} else if next != nil {
		t.Fatalf("second PollTask claimed task again: %#v", next)
	}

	completed, err := service.CompleteTask("contabo", registered.Token, claimed.ID, TaskResultRequest{
		Status: TaskStatusCompleted,
		Result: map[string]string{"active": "active", "sub": "running"},
	})
	if err != nil {
		t.Fatalf("CompleteTask: %v", err)
	}
	if completed.Status != TaskStatusCompleted || completed.CompletedAt == nil || completed.Result["active"] != "active" {
		t.Fatalf("completed task: %#v", completed)
	}
	if _, err := service.CompleteTask("contabo", registered.Token, claimed.ID, TaskResultRequest{Status: TaskStatusCompleted}); !errors.Is(err, ErrTaskNotClaimed) {
		t.Fatalf("duplicate CompleteTask: got %v, want ErrTaskNotClaimed", err)
	}
}

func TestTaskAllowlistAcceptsBothContractKinds(t *testing.T) {
	valid := []TaskRequest{
		{Kind: TaskServiceStatus, Payload: map[string]string{"service": "nginx.service"}},
		{Kind: TaskServiceAction, Payload: map[string]string{"service": "nginx.service", "action": "restart"}},
		{Kind: TaskHostAction, Payload: map[string]string{"action": "swap-reset"}},
		{Kind: TaskProcessSignal, Payload: map[string]string{"pid": "42", "start_time": "987654", "signal": "term"}},
		{Kind: TaskLogsRead, Payload: map[string]string{"source": "php", "lines": "200"}},
		{Kind: TaskContainerList, Payload: map[string]string{}},
		{Kind: TaskContainerAction, Payload: map[string]string{"container": "web-1", "action": "restart"}},
		{Kind: TaskNginxAction, Payload: map[string]string{"action": "reload"}},
		{Kind: TaskNginxConfigList, Payload: map[string]string{}},
		{Kind: TaskNginxConfigRead, Payload: map[string]string{"name": "example.conf"}},
		{Kind: TaskNginxConfigWrite, Payload: map[string]string{"name": "example.conf", "content_b64": "c2VydmVyIHt9Cg==", "checksum": strings.Repeat("a", 64), "reload": "true"}},
		{Kind: TaskPHPInventory, Payload: map[string]string{}},
		{Kind: TaskPHPConfigRead, Payload: map[string]string{"version": "8.3", "pool": "www"}},
		{Kind: TaskPHPConfigWrite, Payload: map[string]string{"version": "8.3", "pool": "www", "content_b64": "W3d3d10K", "checksum": strings.Repeat("a", 64), "reload": "true"}},
		{Kind: TaskPHPAction, Payload: map[string]string{"version": "8.3", "action": "restart"}},
		{Kind: TaskPM2List, Payload: map[string]string{}},
		{Kind: TaskPM2Logs, Payload: map[string]string{"name": "api:blue", "lines": "200"}},
		{Kind: TaskPM2Action, Payload: map[string]string{"name": "api:blue", "action": "reload"}},
		{Kind: TaskCronInventory, Payload: map[string]string{}},
		{Kind: TaskCronCreate, Payload: map[string]string{"job_b64": base64.StdEncoding.EncodeToString([]byte(`{"schedule":"0 * * * *","user":"root","command":"/usr/bin/true","enabled":true}`)), "revision": strings.Repeat("a", 64)}},
		{Kind: TaskCronUpdate, Payload: map[string]string{"job_b64": base64.StdEncoding.EncodeToString([]byte(`{"id":"cron-0123456789ab","schedule":"0 * * * *","user":"root","command":"/usr/bin/true","enabled":true}`)), "revision": strings.Repeat("a", 64)}},
		{Kind: TaskCronDelete, Payload: map[string]string{"id": "cron-0123456789ab", "revision": strings.Repeat("a", 64)}},
		{Kind: TaskCronRun, Payload: map[string]string{"id": "cron-0123456789ab"}},
		{Kind: TaskFirewallInventory, Payload: map[string]string{}},
		{Kind: TaskFirewallAdd, Payload: map[string]string{"rule_b64": base64.StdEncoding.EncodeToString([]byte(`{"action":"ACCEPT","protocol":"tcp","port":443,"source":"203.0.113.0/24","comment":"web"}`)), "revision": strings.Repeat("a", 64)}},
		{Kind: TaskFirewallDelete, Payload: map[string]string{"id": "fw-0123456789ab", "revision": strings.Repeat("a", 64)}},
		{Kind: TaskDomainInventory, Payload: map[string]string{}},
		{Kind: TaskDomainAction, Payload: map[string]string{"config": "example.conf", "action": "enable"}},
		{Kind: TaskSSLInventory, Payload: map[string]string{}},
		{Kind: TaskSSLAction, Payload: map[string]string{"name": "example.com", "action": "renew"}},
		{Kind: TaskDatabaseInventory, Payload: map[string]string{}},
		{Kind: TaskDatabaseAction, Payload: map[string]string{"engine": "postgresql", "action": "restart"}},
		{Kind: TaskBackupInventory, Payload: map[string]string{}},
		{Kind: TaskBackupRun, Payload: map[string]string{"plan": "database-export"}},
		{Kind: TaskFilesBrowse, Payload: map[string]string{"path": "/srv/apps"}},
		{Kind: TaskFilesRead, Payload: map[string]string{"path": "/srv/apps/readme.txt"}},
		{Kind: TaskFilesWrite, Payload: map[string]string{"path": "/srv/apps/readme.txt", "content_b64": "aGVsbG8K", "checksum": strings.Repeat("a", 64)}},
		{Kind: TaskDeployInventory, Payload: map[string]string{}},
		{Kind: TaskDeployAction, Payload: map[string]string{"target": "example-app", "action": "deploy"}},
		{Kind: TaskDeployDomainInventory, Payload: map[string]string{"target": "example-app"}},
		{Kind: TaskDeployDomainHealth, Payload: map[string]string{"target": "example-app", "domain": "app.example.com"}},
		{Kind: TaskDeployDomainAction, Payload: map[string]string{"target": "example-app", "domain": "app.example.com", "action": "create"}},
		{Kind: TaskDeployDomainAction, Payload: map[string]string{"target": "example-app", "domain": "app.example.com", "action": "ensure", "expected_revision": "absent"}},
		{Kind: TaskDeployDomainAction, Payload: map[string]string{"target": "example-app", "domain": "app.example.com", "action": "ensure", "expected_revision": strings.Repeat("a", 64)}},
		{Kind: TaskDeployDomainAction, Payload: map[string]string{"target": "example-app", "domain": "app.example.com", "action": "tls-enable", "email": "admin@example.com"}},
		{Kind: TaskAgentUpdateStatus, Payload: map[string]string{}},
		{Kind: TaskAgentUpdateAction, Payload: map[string]string{"action": "upgrade", "version": "v1.2.3"}},
		{Kind: TaskAgentUpdateAction, Payload: map[string]string{"action": "rollback"}},
	}
	for _, req := range valid {
		if err := ValidateTaskRequest(req); err != nil {
			t.Errorf("ValidateTaskRequest(%#v): %v", req, err)
		}
	}
	for _, req := range []TaskRequest{
		{Kind: TaskServiceStatus, Payload: map[string]string{"unit": "nginx.service"}},
		{Kind: TaskProcessSignal, Payload: map[string]string{"pid": "1", "start_time": "987654", "signal": "kill"}},
		{Kind: TaskLogsRead, Payload: map[string]string{"source": "arbitrary", "lines": "200"}},
		{Kind: TaskLogsRead, Payload: map[string]string{"source": "system", "lines": "501"}},
		{Kind: TaskLogsRead, Payload: map[string]string{"source": "system", "lines": "020"}},
		{Kind: TaskContainerAction, Payload: map[string]string{"container": "web;id", "action": "restart"}},
		{Kind: TaskContainerAction, Payload: map[string]string{"container": "web-1", "action": "exec"}},
		{Kind: TaskNginxAction, Payload: map[string]string{"action": "restart"}},
		{Kind: TaskNginxConfigRead, Payload: map[string]string{"name": "../nginx.conf"}},
		{Kind: TaskNginxConfigWrite, Payload: map[string]string{"name": "example.conf", "content_b64": "not-base64", "checksum": strings.Repeat("a", 64), "reload": "false"}},
		{Kind: TaskPHPConfigRead, Payload: map[string]string{"version": "8.3;id", "pool": "www"}},
		{Kind: TaskPHPConfigRead, Payload: map[string]string{"version": "8.3", "pool": "../www"}},
		{Kind: TaskPHPConfigWrite, Payload: map[string]string{"version": "8.3", "pool": "www", "content_b64": "not-base64", "checksum": strings.Repeat("a", 64), "reload": "false"}},
		{Kind: TaskPHPAction, Payload: map[string]string{"version": "8.3", "action": "stop"}},
		{Kind: TaskPM2Logs, Payload: map[string]string{"name": "../api", "lines": "200"}},
		{Kind: TaskPM2Logs, Payload: map[string]string{"name": "api", "lines": "020"}},
		{Kind: TaskPM2Action, Payload: map[string]string{"name": "api", "action": "delete"}},
		{Kind: TaskSSLInventory, Payload: map[string]string{"name": "example.com"}},
		{Kind: TaskSSLAction, Payload: map[string]string{"name": "../example.com", "action": "renew"}},
		{Kind: TaskSSLAction, Payload: map[string]string{"name": "example.com", "action": "delete"}},
		{Kind: TaskDatabaseInventory, Payload: map[string]string{"engine": "mariadb"}},
		{Kind: TaskDatabaseAction, Payload: map[string]string{"engine": "sqlite", "action": "restart"}},
		{Kind: TaskDatabaseAction, Payload: map[string]string{"engine": "mariadb", "action": "stop"}},
		{Kind: TaskBackupInventory, Payload: map[string]string{"plan": "database-export"}},
		{Kind: TaskBackupRun, Payload: map[string]string{"plan": "../database-export"}},
		{Kind: TaskFilesRead, Payload: map[string]string{"path": "/srv/../etc/passwd"}},
		{Kind: TaskFilesWrite, Payload: map[string]string{"path": "/srv/readme.txt", "content_b64": "not-base64", "checksum": strings.Repeat("a", 64)}},
		{Kind: TaskDeployInventory, Payload: map[string]string{"target": "example-app"}},
		{Kind: TaskDeployAction, Payload: map[string]string{"target": "../example-app", "action": "deploy"}},
		{Kind: TaskDeployAction, Payload: map[string]string{"target": "example-app", "action": "destroy"}},
		{Kind: TaskDeployDomainInventory, Payload: map[string]string{}},
		{Kind: TaskDeployDomainHealth, Payload: map[string]string{"target": "example-app", "domain": "../example.com"}},
		{Kind: TaskDeployDomainAction, Payload: map[string]string{"target": "example-app", "domain": "app.example.com", "action": "destroy"}},
		{Kind: TaskDeployDomainAction, Payload: map[string]string{"target": "example-app", "domain": "app.example.com", "action": "delete", "email": "admin@example.com"}},
		{Kind: TaskDeployDomainAction, Payload: map[string]string{"target": "example-app", "domain": "app.example.com", "action": "ensure", "expected_revision": strings.Repeat("A", 64)}},
		{Kind: TaskDeployDomainAction, Payload: map[string]string{"target": "example-app", "domain": "app.example.com", "action": "ensure", "expected_revision": strings.Repeat("a", 64), "confirmed": "true"}},
		{Kind: TaskAgentUpdateStatus, Payload: map[string]string{"version": "v1.2.3"}},
		{Kind: TaskAgentUpdateAction, Payload: map[string]string{"action": "upgrade", "version": "dev"}},
		{Kind: TaskAgentUpdateAction, Payload: map[string]string{"action": "rollback", "version": "v1.2.3"}},
		{Kind: TaskAgentUpdateAction, Payload: map[string]string{"action": "install"}},
		{Kind: TaskCronCreate, Payload: map[string]string{"job_b64": "not-base64", "revision": strings.Repeat("a", 64)}},
		{Kind: TaskCronUpdate, Payload: map[string]string{"job_b64": base64.StdEncoding.EncodeToString([]byte(`{"id":"bad","schedule":"0 * * * *","user":"root","command":"true","enabled":true}`)), "revision": strings.Repeat("a", 64)}},
		{Kind: TaskCronRun, Payload: map[string]string{"id": "../cron"}},
		{Kind: TaskFirewallAdd, Payload: map[string]string{"rule_b64": base64.StdEncoding.EncodeToString([]byte(`{"action":"ALLOW","protocol":"tcp","port":443}`)), "revision": strings.Repeat("a", 64)}},
		{Kind: TaskFirewallAdd, Payload: map[string]string{"rule_b64": base64.StdEncoding.EncodeToString([]byte(`{"action":"DROP","protocol":"tcp","source":"2001:db8::/64"}`)), "revision": strings.Repeat("a", 64)}},
		{Kind: TaskFirewallDelete, Payload: map[string]string{"id": "../firewall", "revision": strings.Repeat("a", 64)}},
		{Kind: TaskDomainAction, Payload: map[string]string{"config": "../example.conf", "action": "enable"}},
		{Kind: TaskDomainAction, Payload: map[string]string{"config": "example.conf", "action": "delete"}},
		{Kind: "shell", Payload: map[string]string{"service": "nginx.service"}},
	} {
		if err := ValidateTaskRequest(req); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("ValidateTaskRequest(%#v): got %v, want ErrInvalidInput", req, err)
		}
	}
}

func TestTaskResultAllowsBoundedStructuredLogPayload(t *testing.T) {
	if err := ValidateTaskResultRequest(TaskResultRequest{
		Status: TaskStatusCompleted,
		Result: map[string]string{"data": strings.Repeat("x", 128<<10)},
	}); err != nil {
		t.Fatalf("bounded structured result: %v", err)
	}
	if err := ValidateTaskResultRequest(TaskResultRequest{
		Status: TaskStatusCompleted,
		Result: map[string]string{"data": strings.Repeat("x", maxTaskResultValueLength+1)},
	}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("oversized structured result: got %v, want ErrInvalidInput", err)
	}
}

func TestTaskOwnershipAndFailedResult(t *testing.T) {
	service, _, now := newTestService(t)
	nodeA := registerTestNode(t, service, "a")
	nodeB := registerTestNode(t, service, "b")
	if _, err := service.Heartbeat("a", nodeA.Token, HeartbeatRequest{
		ProtocolVersion: ProtocolVersion,
		NodeID:          "a",
		AgentVersion:    "agent-test",
		Capabilities:    []string{CapabilityServiceStatus},
		Hostname:        "a.example",
		SentAt:          now,
	}); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	task, err := service.EnqueueTask("a", TaskRequest{Kind: TaskServiceStatus, Payload: map[string]string{"service": "redis.service"}})
	if err != nil {
		t.Fatalf("EnqueueTask: %v", err)
	}
	if claimed, err := service.PollTask("b", nodeB.Token); err != nil {
		t.Fatalf("PollTask(other node): %v", err)
	} else if claimed != nil {
		t.Fatalf("other node claimed task: %#v", claimed)
	}
	if visible, err := service.GetTaskForNode("a", task.ID); err != nil || visible.ID != task.ID {
		t.Fatalf("owner GetTaskForNode: task=%#v err=%v", visible, err)
	}
	if _, err := service.GetTaskForNode("b", task.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-node GetTaskForNode: got %v, want ErrNotFound", err)
	}
	listedA, err := service.ListTasksForNode("a", 10)
	if err != nil || len(listedA) != 1 || listedA[0].ID != task.ID {
		t.Fatalf("owner ListTasksForNode: tasks=%#v err=%v", listedA, err)
	}
	listedB, err := service.ListTasksForNode("b", 10)
	if err != nil || len(listedB) != 0 {
		t.Fatalf("cross-node ListTasksForNode: tasks=%#v err=%v", listedB, err)
	}
	claimed, err := service.PollTask("a", nodeA.Token)
	if err != nil || claimed == nil {
		t.Fatalf("PollTask(owner): task=%#v err=%v", claimed, err)
	}
	failed, err := service.CompleteTask("a", nodeA.Token, task.ID, TaskResultRequest{
		Status: TaskStatusFailed,
		Error:  "systemd returned exit status 1",
	})
	if err != nil {
		t.Fatalf("failed CompleteTask: %v", err)
	}
	if failed.Status != TaskStatusFailed || failed.Error == "" || failed.CompletedAt == nil {
		t.Fatalf("failed task: %#v", failed)
	}
	if _, err := service.CompleteTask("b", nodeB.Token, task.ID, TaskResultRequest{Status: TaskStatusCompleted}); !errors.Is(err, ErrTaskNotClaimed) {
		t.Fatalf("cross-node CompleteTask: got %v, want ErrTaskNotClaimed", err)
	}
}

func TestEnqueueTaskRequiresAdvertisedCapability(t *testing.T) {
	service, _, _ := newTestService(t)
	registerTestNode(t, service, "node")
	_, err := service.EnqueueTask("node", TaskRequest{
		Kind:    TaskHostAction,
		Payload: map[string]string{"action": "swap-reset"},
	})
	if !errors.Is(err, ErrCapabilityUnavailable) {
		t.Fatalf("EnqueueTask error = %v, want ErrCapabilityUnavailable", err)
	}
}

func TestDeployDomainEnsureQueuesExactPayloadAndCoalescesRevision(t *testing.T) {
	service, _, now := newTestService(t)
	registered := registerTestNode(t, service, "ensure-node")
	if _, err := service.Heartbeat("ensure-node", registered.Token, HeartbeatRequest{
		ProtocolVersion: ProtocolVersion,
		NodeID:          "ensure-node",
		AgentVersion:    "agent-test",
		Capabilities:    []string{CapabilityDeployDomainAction},
		Hostname:        "ensure.example",
		SentAt:          now,
	}); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	revision := strings.Repeat("b", 64)
	first, err := service.EnqueueDeployDomainEnsure("ensure-node", "example-app", "app.example.com", revision)
	if err != nil {
		t.Fatalf("first ensure: %v", err)
	}
	if first.Kind != TaskDeployDomainAction || len(first.Payload) != 4 || first.Payload["target"] != "example-app" || first.Payload["domain"] != "app.example.com" || first.Payload["action"] != "ensure" || first.Payload["expected_revision"] != revision {
		t.Fatalf("ensure payload: %#v", first.Payload)
	}
	second, err := service.EnqueueDeployDomainEnsure("ensure-node", "example-app", "app.example.com", revision)
	if err != nil || second == nil || second.ID != first.ID {
		t.Fatalf("same revision did not coalesce: second=%#v err=%v", second, err)
	}
	if _, err := service.EnqueueDeployDomainEnsure("ensure-node", "example-app", "app.example.com", strings.Repeat("c", 64)); !errors.Is(err, ErrDeployDomainEnsureConflict) {
		t.Fatalf("different in-flight revision error = %v, want ErrDeployDomainEnsureConflict", err)
	}
	tasks, err := service.ListTasksForNode("ensure-node", 10)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("ensure task count = %d, err=%v tasks=%#v", len(tasks), err, tasks)
	}
}

func TestDeployDomainEnsureChecksValidationCapabilityAndOnlineBeforePersistence(t *testing.T) {
	service, _, now := newTestService(t)
	registered := registerTestNode(t, service, "ensure-guards")
	revision := strings.Repeat("d", 64)
	if _, err := service.EnqueueTask("ensure-guards", TaskRequest{Kind: TaskDeployDomainAction, Payload: map[string]string{
		"target": "example-app", "domain": "app.example.com", "action": "ensure", "expected_revision": revision, "confirmed": "true",
	}}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid ensure payload error = %v, want ErrInvalidInput", err)
	}
	if _, err := service.EnqueueDeployDomainEnsure("ensure-guards", "example-app", "app.example.com", revision); !errors.Is(err, ErrCapabilityUnavailable) {
		t.Fatalf("missing capability error = %v, want ErrCapabilityUnavailable", err)
	}
	if tasks, err := service.ListTasksForNode("ensure-guards", 10); err != nil || len(tasks) != 0 {
		t.Fatalf("task persisted before capability check: %#v err=%v", tasks, err)
	}
	if _, err := service.Heartbeat("ensure-guards", registered.Token, HeartbeatRequest{
		ProtocolVersion: ProtocolVersion,
		NodeID:          "ensure-guards",
		AgentVersion:    "agent-test",
		Capabilities:    []string{CapabilityDeployDomainAction},
		Hostname:        "guards.example",
		SentAt:          now,
	}); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	service.now = func() time.Time { return now.Add(NodeOnlineWindow + time.Nanosecond) }
	if _, err := service.EnqueueDeployDomainEnsure("ensure-guards", "example-app", "app.example.com", revision); !errors.Is(err, ErrNodeOffline) {
		t.Fatalf("offline ensure error = %v, want ErrNodeOffline", err)
	}
	if tasks, err := service.ListTasksForNode("ensure-guards", 10); err != nil || len(tasks) != 0 {
		t.Fatalf("task persisted while offline: %#v err=%v", tasks, err)
	}
}

func TestNodeOnlineWindowUsesServerObservedHeartbeat(t *testing.T) {
	service, _, now := newTestService(t)
	registered := registerTestNode(t, service, "node")
	if service.IsNodeOnline(registered.Node) {
		t.Fatal("node without a heartbeat is online")
	}
	if _, err := service.Heartbeat("node", registered.Token, HeartbeatRequest{
		ProtocolVersion: ProtocolVersion,
		NodeID:          "node",
		AgentVersion:    "agent-test",
		Capabilities:    []string{CapabilityServiceStatus},
		Hostname:        "node.example",
		SentAt:          now,
	}); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	node, err := service.GetNode("node")
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	service.now = func() time.Time { return now.Add(NodeOnlineWindow) }
	if !service.IsNodeOnline(*node) {
		t.Fatal("node is offline at the inclusive heartbeat window boundary")
	}
	service.now = func() time.Time { return now.Add(NodeOnlineWindow + time.Nanosecond) }
	if service.IsNodeOnline(*node) {
		t.Fatal("node is online after the heartbeat window")
	}
}

func TestEnqueueTaskRejectsOfflineNodeWithoutPersistence(t *testing.T) {
	service, _, now := newTestService(t)
	registered := registerTestNode(t, service, "node")
	if _, err := service.Heartbeat("node", registered.Token, HeartbeatRequest{
		ProtocolVersion: ProtocolVersion,
		NodeID:          "node",
		AgentVersion:    "agent-test",
		Capabilities:    []string{CapabilityServiceStatus},
		Hostname:        "node.example",
		SentAt:          now,
	}); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	service.now = func() time.Time { return now.Add(NodeOnlineWindow + time.Nanosecond) }
	_, err := service.EnqueueTask("node", TaskRequest{
		Kind:    TaskServiceStatus,
		Payload: map[string]string{"service": "nginx.service"},
	})
	if !errors.Is(err, ErrNodeOffline) {
		t.Fatalf("EnqueueTask error = %v, want ErrNodeOffline", err)
	}
	tasks, err := service.ListTasksForNode("node", 10)
	if err != nil {
		t.Fatalf("ListTasksForNode: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("offline task was persisted: %#v", tasks)
	}
}

func TestValidateTaskRequestAllowsOnlyStructuredDiskCleanupTargets(t *testing.T) {
	valid := []TaskRequest{
		{Kind: TaskDiskCleanupScan, Payload: map[string]string{}},
		{Kind: TaskDiskCleanupExecute, Payload: map[string]string{"targets": "apt-cache,journal"}},
	}
	for _, request := range valid {
		if err := ValidateTaskRequest(request); err != nil {
			t.Fatalf("valid request %#v: %v", request, err)
		}
	}
	invalid := []TaskRequest{
		{Kind: TaskDiskCleanupScan, Payload: map[string]string{"path": "/"}},
		{Kind: TaskDiskCleanupExecute, Payload: map[string]string{"targets": "journal,journal"}},
		{Kind: TaskDiskCleanupExecute, Payload: map[string]string{"targets": "root-files"}},
	}
	for _, request := range invalid {
		if err := ValidateTaskRequest(request); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("invalid request %#v error = %v", request, err)
		}
	}
}
