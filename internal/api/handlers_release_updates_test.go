package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/IamYGT/heyserver/internal/releaseversion"
	"github.com/IamYGT/heyserver/internal/services/releaseupdates"
)

const apiTestSHA256 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

type releaseUpdateCheckerStub struct {
	result releaseupdates.Result
	called bool
}

type releaseUpdateManagerStub struct {
	staged         releaseupdates.Stage
	latest         *releaseupdates.Stage
	stageErr       error
	latestErr      error
	scheduleErr    error
	scheduleCalled bool
	stageID        string
	version        string
	confirmed      bool
}

func (stub *releaseUpdateManagerStub) Stage(context.Context) (releaseupdates.Stage, error) {
	return stub.staged, stub.stageErr
}

func (stub *releaseUpdateManagerStub) Latest(context.Context) (*releaseupdates.Stage, error) {
	return stub.latest, stub.latestErr
}

func (stub *releaseUpdateManagerStub) Schedule(_ context.Context, stageID, version string, confirmed bool) (releaseupdates.Stage, error) {
	stub.scheduleCalled = true
	stub.stageID = stageID
	stub.version = version
	stub.confirmed = confirmed
	return stub.staged, stub.scheduleErr
}

func (stub *releaseUpdateCheckerStub) Check(context.Context) releaseupdates.Result {
	stub.called = true
	return stub.result
}

func TestReleaseUpdateStageHandlersReturnVerifiedState(t *testing.T) {
	stage := releaseupdates.Stage{ID: "v1.3.0-0123456789ab", Version: "v1.3.0", SHA256: apiTestSHA256, Status: releaseupdates.StageStaged}
	stub := &releaseUpdateManagerStub{staged: stage, latest: &stage}

	stageRecorder := httptest.NewRecorder()
	handleReleaseUpdateStage(stub).ServeHTTP(stageRecorder, httptest.NewRequest(http.MethodPost, "/api/system/update/stage", nil))
	if stageRecorder.Code != http.StatusOK {
		t.Fatalf("stage status = %d, body = %s", stageRecorder.Code, stageRecorder.Body.String())
	}

	statusRecorder := httptest.NewRecorder()
	handleReleaseUpdateStageStatus(stub).ServeHTTP(statusRecorder, httptest.NewRequest(http.MethodGet, "/api/system/update/stage", nil))
	if statusRecorder.Code != http.StatusOK {
		t.Fatalf("status status = %d, body = %s", statusRecorder.Code, statusRecorder.Body.String())
	}
	var response struct {
		Stage *releaseupdates.Stage `json:"stage"`
	}
	if err := json.NewDecoder(statusRecorder.Body).Decode(&response); err != nil || response.Stage == nil || response.Stage.ID != stage.ID {
		t.Fatalf("status response = %#v, %v", response, err)
	}
}

func TestReleaseUpdateStageRequiresEmptyBody(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
		want int
	}{
		{name: "empty", want: http.StatusOK},
		{name: "whitespace", body: " \n\t", want: http.StatusOK},
		{name: "json", body: `{}`, want: http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			stub := &releaseUpdateManagerStub{staged: releaseupdates.Stage{Status: releaseupdates.StageStaged}}
			recorder := httptest.NewRecorder()
			handleReleaseUpdateStage(stub).ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/system/update/stage", bytes.NewBufferString(test.body)))
			if recorder.Code != test.want {
				t.Fatalf("status = %d, want %d, body = %s", recorder.Code, test.want, recorder.Body.String())
			}
		})
	}
}

func TestReleaseUpdateInstallRequiresExactConfirmationPayload(t *testing.T) {
	stage := releaseupdates.Stage{ID: "v1.3.0-0123456789ab", Version: "v1.3.0", Status: releaseupdates.StageScheduled}
	stub := &releaseUpdateManagerStub{staged: stage}
	body := []byte(`{"stage_id":"v1.3.0-0123456789ab","version":"v1.3.0","confirmed":true}`)
	recorder := httptest.NewRecorder()
	handleReleaseUpdateInstall(stub).ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/system/update/install", bytes.NewReader(body)))

	if recorder.Code != http.StatusAccepted || !stub.scheduleCalled || stub.stageID != stage.ID || stub.version != stage.Version || !stub.confirmed {
		t.Fatalf("status = %d, stub = %#v, body = %s", recorder.Code, stub, recorder.Body.String())
	}

	invalidStub := &releaseUpdateManagerStub{}
	invalidRecorder := httptest.NewRecorder()
	invalidBody := []byte(`{"stage_id":"x","version":"v1.3.0","confirmed":true,"force":true}`)
	handleReleaseUpdateInstall(invalidStub).ServeHTTP(invalidRecorder, httptest.NewRequest(http.MethodPost, "/api/system/update/install", bytes.NewReader(invalidBody)))
	if invalidRecorder.Code != http.StatusBadRequest || invalidStub.scheduleCalled {
		t.Fatalf("invalid status = %d, called = %t", invalidRecorder.Code, invalidStub.scheduleCalled)
	}
}

func TestReleaseUpdateInstallRejectsTrailingAndOversizedJSON(t *testing.T) {
	for _, test := range []struct {
		name string
		body []byte
		want int
	}{
		{name: "trailing", body: []byte(`{"stage_id":"x","version":"v1.3.0","confirmed":true}{}`), want: http.StatusBadRequest},
		{name: "oversized", body: append([]byte(`{"stage_id":"`), append(bytes.Repeat([]byte("x"), 4096), []byte(`","version":"v1.3.0","confirmed":true}`)...)...), want: http.StatusRequestEntityTooLarge},
	} {
		t.Run(test.name, func(t *testing.T) {
			stub := &releaseUpdateManagerStub{}
			recorder := httptest.NewRecorder()
			handleReleaseUpdateInstall(stub).ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/system/update/install", bytes.NewReader(test.body)))
			if recorder.Code != test.want || stub.scheduleCalled {
				t.Fatalf("status = %d, want %d, called = %t, body = %s", recorder.Code, test.want, stub.scheduleCalled, recorder.Body.String())
			}
		})
	}
}

func TestReleaseUpdateErrorsHaveStableHTTPBoundaries(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "confirmation", err: releaseupdates.ErrConfirmationRequired, want: http.StatusBadRequest},
		{name: "missing", err: os.ErrNotExist, want: http.StatusNotFound},
		{name: "no update", err: releaseupdates.ErrNoUpdateAvailable, want: http.StatusConflict},
		{name: "integrity", err: releaseupdates.ErrStageIntegrity, want: http.StatusConflict},
		{name: "discovery", err: releaseupdates.ErrDiscoveryUnavailable, want: http.StatusServiceUnavailable},
		{name: "signed manifest", err: releaseupdates.ErrSignedManifestRequired, want: http.StatusServiceUnavailable},
		{name: "schedule", err: releaseupdates.ErrUpgradeSchedule, want: http.StatusServiceUnavailable},
		{name: "internal", err: errors.New("private failure"), want: http.StatusInternalServerError},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			writeReleaseUpdateError(recorder, test.err)
			if recorder.Code != test.want {
				t.Fatalf("status = %d, want %d", recorder.Code, test.want)
			}
			if test.want == http.StatusInternalServerError && bytes.Contains(recorder.Body.Bytes(), []byte("private failure")) {
				t.Fatalf("internal error leaked: %s", recorder.Body.String())
			}
		})
	}
}

func TestReleaseUpdateSignedManifestErrorDoesNotLeakInternalDetail(t *testing.T) {
	recorder := httptest.NewRecorder()
	writeReleaseUpdateError(recorder, errors.Join(releaseupdates.ErrSignedManifestRequired, errors.New("private key detail")))

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
	if got, want := recorder.Body.String(), "{\"error\":\"A verified signed release manifest is required\"}\n"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestReleaseUpdateCheckReturnsExplicitDiscoveryState(t *testing.T) {
	stub := &releaseUpdateCheckerStub{result: releaseupdates.Result{
		Status:             releaseupdates.StatusHealthy,
		CurrentVersion:     "1.2.3",
		LatestVersion:      "1.3.0",
		LatestVersionState: releaseversion.Ahead,
		UpdateAvailable:    true,
		Platform:           "linux_amd64",
		Message:            "A newer HServer release is available.",
		SignatureStatus:    releaseupdates.SignatureVerified,
	}}
	recorder := httptest.NewRecorder()
	handleReleaseUpdateCheck(stub).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/system/update", nil))

	if recorder.Code != http.StatusOK || !stub.called {
		t.Fatalf("status = %d, called = %t", recorder.Code, stub.called)
	}
	var result releaseupdates.Result
	if err := json.NewDecoder(recorder.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result.Status != releaseupdates.StatusHealthy || !result.UpdateAvailable || result.LatestVersionState != releaseversion.Ahead {
		t.Fatalf("result = %#v", result)
	}
}
