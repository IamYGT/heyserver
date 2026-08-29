package uptime

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"github.com/IamYGT/heyserver/internal/services/notify"
	"github.com/IamYGT/heyserver/internal/services/settings"
	"github.com/IamYGT/heyserver/internal/store"
)

// Engine manages all monitor goroutines.
type Engine struct {
	repo         *store.UptimeRepository
	stateManager *StateManager
	batcher      *HeartbeatBatcher
	retention    *RetentionWorker

	mu      sync.Mutex
	workers map[int64]context.CancelFunc
	wg      sync.WaitGroup
	appCtx  context.Context // stored from Start() for AddMonitor calls
	// checkLocks serializes scheduled and operator-triggered checks per monitor.
	// Different monitors remain fully concurrent.
	checkLocks sync.Map // map[int64]*sync.Mutex
}

var (
	ErrMonitorNotFound = errors.New("uptime monitor not found")
	ErrMonitorPaused   = errors.New("uptime monitor is paused")
)

func NewEngine(
	repo *store.UptimeRepository,
	channelRepo *store.NotificationChannelRepository,
	settingsSvc *settings.Service,
	receiptRepo ...notify.ReceiptRecorder,
) *Engine {
	alerter := NewAlerter(channelRepo, settingsSvc, receiptRepo...)
	sm := NewStateManager(repo, alerter)
	batcher := NewHeartbeatBatcher(repo)
	retention := NewRetentionWorker(repo, settingsSvc)

	return &Engine{
		repo:         repo,
		stateManager: sm,
		batcher:      batcher,
		retention:    retention,
		workers:      make(map[int64]context.CancelFunc),
	}
}

// Start loads all active monitors and starts their workers.
func (e *Engine) Start(ctx context.Context) {
	e.appCtx = ctx // store for later AddMonitor calls
	slog.Info("uptime: engine starting")

	// Start batcher
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		e.batcher.Run(ctx)
	}()

	// Start retention worker
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		e.retention.Run(ctx)
	}()

	// Load and start all active monitors
	monitors, err := e.repo.ListMonitors()
	if err != nil {
		slog.Error("uptime: failed to load monitors", "error", err)
		return
	}

	for i := range monitors {
		m := monitors[i]
		if m.IsActive && !m.MaintenanceMode {
			e.startWorker(ctx, &m)
		}
	}

	slog.Info("uptime: engine started", "monitors", len(monitors))

	// Wait for context cancellation
	<-ctx.Done()
	slog.Info("uptime: engine stopping")

	e.mu.Lock()
	for id, cancel := range e.workers {
		cancel()
		delete(e.workers, id)
	}
	e.mu.Unlock()

	e.wg.Wait()
	slog.Info("uptime: engine stopped")
}

func (e *Engine) startWorker(parentCtx context.Context, m *store.UptimeMonitor) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Cancel existing worker if any
	if cancel, ok := e.workers[m.ID]; ok {
		cancel()
	}

	ctx, cancel := context.WithCancel(parentCtx)
	e.workers[m.ID] = cancel

	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		monitorWorker(ctx, m, e.stateManager, e.batcher, e.runCheck)
	}()
}

func (e *Engine) monitorCheckLock(id int64) *sync.Mutex {
	lock, _ := e.checkLocks.LoadOrStore(id, &sync.Mutex{})
	return lock.(*sync.Mutex)
}

func (e *Engine) runCheck(m *store.UptimeMonitor) CheckResult {
	lock := e.monitorCheckLock(m.ID)
	lock.Lock()
	defer lock.Unlock()
	return runCheck(m, e.stateManager, e.batcher)
}

// CheckNow immediately executes and persists a check using the same state,
// heartbeat and incident pipeline as the scheduler.
func (e *Engine) CheckNow(id int64) (CheckResult, error) {
	monitor, err := e.repo.GetMonitor(id)
	if err != nil {
		return CheckResult{}, err
	}
	if monitor == nil {
		return CheckResult{}, ErrMonitorNotFound
	}
	if !monitor.IsActive || monitor.MaintenanceMode {
		return CheckResult{}, ErrMonitorPaused
	}
	result := e.runCheck(monitor)
	// An operator-triggered check should be visible immediately rather than
	// waiting for the normal five-second heartbeat batch interval.
	e.batcher.Flush()
	return result, nil
}

// workerParentCtx returns the long-lived context from Start(). When the engine has
// not been started yet (tests, early handler calls), workers are deferred until Start().
func (e *Engine) workerParentCtx() (context.Context, bool) {
	if e.appCtx == nil {
		return nil, false
	}
	return e.appCtx, true
}

// AddMonitor starts monitoring a new or updated monitor.
// Uses the engine's appCtx (not the request context) so workers survive beyond HTTP requests.
func (e *Engine) AddMonitor(ctx context.Context, m *store.UptimeMonitor) {
	if !m.IsActive || m.MaintenanceMode {
		return
	}
	parent, ok := e.workerParentCtx()
	if !ok {
		return
	}
	e.startWorker(parent, m)
}

// UpdateMonitor restarts the worker with updated config.
func (e *Engine) UpdateMonitor(ctx context.Context, m *store.UptimeMonitor) {
	e.RemoveMonitor(m.ID)
	if !m.IsActive || m.MaintenanceMode {
		return
	}
	parent, ok := e.workerParentCtx()
	if !ok {
		return
	}
	e.startWorker(parent, m)
}

// RemoveMonitor stops the worker for a monitor.
func (e *Engine) RemoveMonitor(id int64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if cancel, ok := e.workers[id]; ok {
		cancel()
		delete(e.workers, id)
	}
}

// Repo returns the repository for use in handlers.
func (e *Engine) Repo() *store.UptimeRepository {
	return e.repo
}
