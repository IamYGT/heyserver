package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/IamYGT/heyserver/internal/services/dockerctl"
)

type fakeDockerOperations struct {
	status       dockerctl.Status
	logs         dockerctl.Logs
	logID        string
	logTail      int
	actionID     string
	action       string
	pulledImage  string
	removedImage string
}

func (f *fakeDockerOperations) Status(context.Context) (dockerctl.Status, error) {
	return f.status, nil
}
func (f *fakeDockerOperations) Containers(context.Context) ([]dockerctl.Container, error) {
	return []dockerctl.Container{}, nil
}
func (f *fakeDockerOperations) ContainerLogs(_ context.Context, id string, tail int) (dockerctl.Logs, error) {
	f.logID, f.logTail = id, tail
	return f.logs, nil
}
func (f *fakeDockerOperations) Images(context.Context) ([]dockerctl.Image, error) {
	return []dockerctl.Image{}, nil
}
func (f *fakeDockerOperations) ContainerAction(_ context.Context, id, action string) error {
	f.actionID, f.action = id, action
	return nil
}
func (f *fakeDockerOperations) PullImage(_ context.Context, image string) error {
	f.pulledImage = image
	return nil
}
func (f *fakeDockerOperations) RemoveImage(_ context.Context, image string) error {
	f.removedImage = image
	return nil
}

func withDockerOperations(t *testing.T, operations dockerOperations) {
	t.Helper()
	previous := dockerService
	dockerService = operations
	t.Cleanup(func() { dockerService = previous })
}

func TestHandleDockerStatusUsesFrontendContract(t *testing.T) {
	fake := &fakeDockerOperations{status: dockerctl.Status{Installed: true, Running: true, Version: "27.3.1", ContainersTotal: 3, ContainersRunning: 2, ImageCount: 4}}
	withDockerOperations(t, fake)
	recorder := httptest.NewRecorder()
	handleDockerStatus()(recorder, httptest.NewRequest(http.MethodGet, "/api/docker/status", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	var body map[string]any
	if err := json.NewDecoder(recorder.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["containersTotal"] != float64(3) || body["containersRunning"] != float64(2) || body["imageCount"] != float64(4) {
		t.Fatalf("body = %#v", body)
	}
	if _, legacy := body["running_ct"]; legacy {
		t.Fatalf("legacy response field remains: %#v", body)
	}
}

func TestHandleDockerLogsBoundsTailAndReturnsEnvelope(t *testing.T) {
	fake := &fakeDockerOperations{logs: dockerctl.Logs{Logs: "ready\n", Tail: 250}}
	withDockerOperations(t, fake)
	request := httptest.NewRequest(http.MethodGet, "/api/docker/containers/web-1/logs?tail=250", nil)
	request.SetPathValue("id", "web-1")
	recorder := httptest.NewRecorder()
	handleDockerContainerLogs()(recorder, request)
	if recorder.Code != http.StatusOK || fake.logID != "web-1" || fake.logTail != 250 || !strings.Contains(recorder.Body.String(), `"logs":"ready\n"`) {
		t.Fatalf("status=%d id=%q tail=%d body=%s", recorder.Code, fake.logID, fake.logTail, recorder.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/api/docker/containers/web-1/logs?tail=1001", nil)
	request.SetPathValue("id", "web-1")
	recorder = httptest.NewRecorder()
	handleDockerContainerLogs()(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("oversized tail status = %d", recorder.Code)
	}
}

func TestHandleDockerMutationsReachFixedOperations(t *testing.T) {
	fake := &fakeDockerOperations{}
	withDockerOperations(t, fake)

	request := httptest.NewRequest(http.MethodPost, "/api/docker/containers/web-1/remove", nil)
	request.SetPathValue("id", "web-1")
	request.SetPathValue("action", "remove")
	recorder := httptest.NewRecorder()
	handleDockerContainerControl()(recorder, request)
	if recorder.Code != http.StatusOK || fake.actionID != "web-1" || fake.action != "remove" {
		t.Fatalf("container mutation: status=%d id=%q action=%q", recorder.Code, fake.actionID, fake.action)
	}

	recorder = httptest.NewRecorder()
	handleDockerImagePull()(recorder, httptest.NewRequest(http.MethodPost, "/api/docker/images/pull", strings.NewReader(`{"name":"nginx:1.27"}`)))
	if recorder.Code != http.StatusOK || fake.pulledImage != "nginx:1.27" {
		t.Fatalf("pull: status=%d image=%q", recorder.Code, fake.pulledImage)
	}

	request = httptest.NewRequest(http.MethodDelete, "/api/docker/images/sha256:abc", nil)
	request.SetPathValue("id", "sha256:abc")
	recorder = httptest.NewRecorder()
	handleDockerImageDelete()(recorder, request)
	if recorder.Code != http.StatusOK || fake.removedImage != "sha256:abc" {
		t.Fatalf("delete: status=%d image=%q", recorder.Code, fake.removedImage)
	}
}

func TestHandleDockerImagePullRejectsUnknownFields(t *testing.T) {
	fake := &fakeDockerOperations{}
	withDockerOperations(t, fake)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/docker/images/pull", strings.NewReader(`{"name":"nginx:1.27","platform":"linux/amd64"}`))

	handleDockerImagePull()(recorder, request)

	if recorder.Code != http.StatusBadRequest || fake.pulledImage != "" || !strings.Contains(recorder.Body.String(), "invalid JSON") {
		t.Fatalf("response=%d %s pulled=%q", recorder.Code, recorder.Body.String(), fake.pulledImage)
	}
}
