package main

import (
	"context"
	"encoding/base64"
	"errors"
	"strconv"
	"time"

	"github.com/IamYGT/heyserver/internal/agenthub"
)

const (
	profileApplyTaskKind   = agenthub.TaskProfileApply
	profileApplyCapability = agenthub.CapabilityProfileApply

	profileErrorMissing         = agenthub.ProfileErrorCodeMissing
	profileErrorCorrupt         = agenthub.ProfileErrorCodeCorrupt
	profileErrorStateCorrupt    = agenthub.ProfileErrorCodeStateCorrupt
	profileErrorRevision        = agenthub.ProfileErrorCodeRevisionInvalid
	profileErrorPayload         = agenthub.ProfileErrorCodePayloadInvalid
	profileErrorPayloadTooLarge = agenthub.ProfileErrorCodePayloadTooLarge
	profileErrorNotReady        = agenthub.ProfileErrorCodeApplyUnavailable
	profileErrorSchedule        = agenthub.ProfileErrorCodeScheduleFailed
	profileErrorStore           = agenthub.ProfileErrorCodeStoreFailed
	profileErrorSuperseded      = agenthub.ProfileErrorCodeSuperseded

	profileResultRestartScheduled = agenthub.ProfileApplyResultRestartScheduled
	profileResultAlreadyActive    = agenthub.ProfileApplyResultAlreadyActive
)

var errProfileApplyUnavailable = errors.New("profile apply is not locally ready")

func profileApplyReady(cfg config) bool {
	return regularExecutable(cfg.agentLifecycleInstaller) && regularExecutable(cfg.systemdRunBinary)
}

type profileApplyController struct {
	store      *profileStore
	installer  string
	systemdRun string
	runner     commandRunner
	readyFn    func() bool
}

func newProfileApplyController(stateDir, installer, systemdRun string, runner commandRunner) *profileApplyController {
	controller := &profileApplyController{
		store:      newProfileStore(stateDir),
		installer:  installer,
		systemdRun: systemdRun,
		runner:     runner,
	}
	controller.readyFn = controller.executableReady
	return controller
}

func (c *profileApplyController) executableReady() bool {
	return c != nil && c.runner != nil && regularExecutable(c.installer) && regularExecutable(c.systemdRun)
}

func (c *profileApplyController) ready() bool {
	if c == nil {
		return false
	}
	if c.readyFn != nil {
		return c.readyFn()
	}
	return c.executableReady()
}

// scheduleProfileApply invokes only the configured local systemd-run binary
// and the fixed installer action.  The profile itself is already on disk, so
// no hub-provided command, argv, path, shell, or payload crosses this call.
func scheduleProfileApply(ctx context.Context, runner commandRunner, systemdRun, installer string) error {
	if runner == nil || systemdRun == "" || installer == "" {
		return errProfileApplyUnavailable
	}
	args := []string{
		"--collect",
		"--quiet",
		"--unit=hserver-agent-profile",
		"--on-active=3s",
		"--timer-property=AccuracySec=1s",
		installer,
		"apply-profile",
	}
	commandCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := runner.run(commandCtx, systemdRun, args...); err != nil {
		return err
	}
	return nil
}

type profileApplyOutcome struct {
	State    string
	Revision int64
}

func (c *profileApplyController) apply(ctx context.Context, payload map[string]string) (profileApplyOutcome, string) {
	wrapper, code := decodeProfileTaskPayload(payload)
	if code != "" {
		return profileApplyOutcome{}, code
	}
	if c == nil || c.store == nil || !c.ready() {
		return profileApplyOutcome{}, profileErrorNotReady
	}

	store := c.store
	store.mu.Lock()
	defer store.mu.Unlock()

	active, activeExists, activeErr := store.readWrapper(store.activePath())
	if activeErr != nil {
		return profileApplyOutcome{}, profileErrorCorrupt
	}
	state, stateExists, stateErr := store.readState()
	if stateErr != nil {
		return profileApplyOutcome{}, profileErrorStateCorrupt
	}
	if activeExists {
		switch {
		case wrapper.Revision < active.Revision:
			return profileApplyOutcome{}, profileErrorSuperseded
		case wrapper.Revision == active.Revision:
			return profileApplyOutcome{State: profileResultAlreadyActive, Revision: active.Revision}, ""
		}
	}
	if stateExists && state.State == profileStatePendingRestart {
		switch {
		case wrapper.Revision < state.Revision:
			return profileApplyOutcome{}, profileErrorSuperseded
		case wrapper.Revision == state.Revision:
			return profileApplyOutcome{State: profileResultRestartScheduled, Revision: state.Revision}, ""
		}
	}

	if err := store.ensureDirectory(); err != nil {
		return profileApplyOutcome{}, profileErrorStore
	}
	if activeExists {
		if err := store.writeWrapper(store.previousPath(), active); err != nil {
			return profileApplyOutcome{}, profileErrorStore
		}
	}
	if err := store.writeWrapper(store.candidatePath(), wrapper); err != nil {
		return profileApplyOutcome{}, profileErrorStore
	}
	if err := store.writeState(profileStateDocument{
		SchemaVersion: profileSchemaVersion,
		State:         profileStatePendingRestart,
		Revision:      wrapper.Revision,
	}); err != nil {
		return profileApplyOutcome{}, profileErrorStore
	}
	if err := scheduleProfileApply(ctx, c.runner, c.systemdRun, c.installer); err != nil {
		// Keep the candidate for an operator retry, but publish a bounded state
		// marker.  The raw runner error is never returned to the hub.
		_ = store.writeState(profileStateDocument{
			SchemaVersion: profileSchemaVersion,
			State:         profileStateFailed,
			Revision:      wrapper.Revision,
			ErrorCode:     profileErrorSchedule,
		})
		return profileApplyOutcome{}, profileErrorSchedule
	}
	return profileApplyOutcome{State: profileResultRestartScheduled, Revision: wrapper.Revision}, ""
}

func (c *profileApplyController) observation() profileObservation {
	if c == nil || c.store == nil {
		return profileObservation{State: profileObservationFailed, ErrorCode: profileErrorNotReady}
	}
	c.store.mu.Lock()
	defer c.store.mu.Unlock()
	_, observation, _ := c.store.loadActiveProfile()
	return observation
}

func decodeProfileTaskPayload(payload map[string]string) (profileWrapper, string) {
	if len(payload) != 2 {
		return profileWrapper{}, profileErrorPayload
	}
	rawRevision, revisionOK := payload["revision"]
	encoded, encodedOK := payload["profile_json_b64"]
	if !revisionOK || !encodedOK || rawRevision == "" || encoded == "" {
		return profileWrapper{}, profileErrorPayload
	}
	// ParseInt accepts a leading plus sign.  Requiring the canonical decimal
	// spelling prevents alternate representations of the same revision from
	// reaching the persisted wrapper.
	revision, err := strconv.ParseInt(rawRevision, 10, 64)
	if err != nil || revision <= 0 || strconv.FormatInt(revision, 10) != rawRevision {
		return profileWrapper{}, profileErrorRevision
	}
	if len(encoded) > base64.StdEncoding.EncodedLen(maxProfileDocumentBytes) {
		return profileWrapper{}, profileErrorPayloadTooLarge
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || base64.StdEncoding.EncodeToString(decoded) != encoded {
		return profileWrapper{}, profileErrorPayload
	}
	if len(decoded) > maxProfileDocumentBytes {
		return profileWrapper{}, profileErrorPayloadTooLarge
	}
	wrapper, err := decodeProfileWrapper(decoded)
	if err != nil {
		switch {
		case errors.Is(err, errProfilePayloadTooBig):
			return profileWrapper{}, profileErrorPayloadTooLarge
		case errors.Is(err, errProfileRevision):
			return profileWrapper{}, profileErrorRevision
		default:
			return profileWrapper{}, profileErrorPayload
		}
	}
	if wrapper.Revision != revision {
		return profileWrapper{}, profileErrorRevision
	}
	return wrapper, ""
}

func profileApplyTaskResult(outcome profileApplyOutcome) agenthub.TaskResultRequest {
	return agenthub.TaskResultRequest{
		Status: agenthub.TaskStatusCompleted,
		Result: map[string]string{
			"state":    outcome.State,
			"revision": strconv.FormatInt(outcome.Revision, 10),
		},
	}
}

func failedProfileTaskResult(code string) agenthub.TaskResultRequest {
	return agenthub.TaskResultRequest{Status: agenthub.TaskStatusFailed, Error: safeProfileErrorCode(code, profileErrorStore)}
}
