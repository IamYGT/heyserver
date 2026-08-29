package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/IamYGT/heyserver/internal/agenthub"
	"github.com/IamYGT/heyserver/internal/db"
)

func TestRemoteDiskInventoryComesFromAgentHeartbeat(t *testing.T) {
	hub, err := agenthub.New(db.Instance())
	if err != nil {
		t.Fatalf("agenthub.New: %v", err)
	}
	registered, err := hub.RegisterNode(agenthub.RegisterNodeRequest{ID: "disk-agent", Name: "Disk Agent"})
	if err != nil {
		t.Fatalf("RegisterNode: %v", err)
	}
	if _, err := hub.Heartbeat("disk-agent", registered.Token, agenthub.HeartbeatRequest{
		ProtocolVersion: agenthub.ProtocolVersion,
		NodeID:          "disk-agent",
		AgentVersion:    "agent-test",
		Capabilities:    []string{agenthub.CapabilityInventory},
		Hostname:        "disk.example",
		SentAt:          time.Now().UTC(),
		Inventory: agenthub.Inventory{
			DiskTotal: 1000, DiskUsed: 600, DiskAvailable: 400, DiskUsePercent: 60,
			DiskMounts: []agenthub.DiskMount{{Filesystem: "/dev/vda1", Size: 1000, Used: 600, Available: 400, UsePercent: 60, Mountpoint: "/"}},
		},
	}); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/nodes/disk-agent/disk", nil)
	request.SetPathValue("id", "disk-agent")
	response := httptest.NewRecorder()
	handleRemoteNodeDisk(hub).ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"filesystem":"/dev/vda1"`) || !strings.Contains(response.Body.String(), `"mountpoint":"/"`) {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
}
