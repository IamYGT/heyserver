package main

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/IamYGT/heyserver/internal/agenthub"
	"github.com/IamYGT/heyserver/internal/agentterminal"
	"github.com/IamYGT/heyserver/internal/api"
	"github.com/IamYGT/heyserver/internal/auth"
	"github.com/IamYGT/heyserver/internal/config"
	"github.com/IamYGT/heyserver/internal/db"
	"github.com/IamYGT/heyserver/internal/models"
	"github.com/IamYGT/heyserver/internal/services/monitor"
	"github.com/IamYGT/heyserver/internal/services/notify"
	"github.com/IamYGT/heyserver/internal/services/settings"
	uptime "github.com/IamYGT/heyserver/internal/services/uptime"
	"github.com/IamYGT/heyserver/internal/store"
)

//go:embed all:web
var webAssets embed.FS

func main() {
	if len(os.Args) == 2 && (os.Args[1] == "--version" || os.Args[1] == "version") {
		fmt.Printf("hserver-panel %s (commit %s, built %s)\n", config.Version, config.BuildCommit, config.BuildDate)
		return
	}

	cfg := config.Load()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(logger)

	webFS, err := fs.Sub(webAssets, "web/dist")
	if err != nil {
		slog.Error("failed to load embedded web assets", "error", err)
		os.Exit(1)
	}

	// ── Auth DB (internal/db — users, audit, sessions) ──────────────────────
	if _, err := db.Open(cfg.DBPath); err != nil {
		slog.Error("failed to open auth database", "error", err)
		os.Exit(1)
	}
	slog.Info("auth database ready")

	// Admin seed on first boot
	userRepo := db.NewUserRepository(db.Instance())
	userCount, _ := userRepo.Count()
	if userCount == 0 {
		adminPass := cfg.AdminPass
		if adminPass == "" {
			slog.Error("HSERVER_ADMIN_PASS is required to create the first administrator")
			os.Exit(1)
		}
		hashed, err := auth.HashPassword(adminPass)
		if err != nil {
			slog.Error("failed to hash admin password", "error", err)
			os.Exit(1)
		}
		admin := &models.User{
			Email:    cfg.AdminEmail,
			Name:     "Admin",
			Password: hashed,
			Role:     models.RoleAdmin,
		}
		if err := userRepo.Create(admin); err != nil {
			slog.Error("failed to create admin user", "error", err)
			os.Exit(1)
		}
		slog.Info("admin user created", "email", cfg.AdminEmail)
	}

	// ── Store DB (notify, settings) ──────────────────────────────────────────
	sqliteDB, err := sql.Open("sqlite3", cfg.DBPath+"?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000")
	// SQLite allows one writer at a time; keep one shared connection to avoid app-internal lock storms.
	sqliteDB.SetMaxOpenConns(1)
	sqliteDB.SetMaxIdleConns(1)
	if err != nil {
		slog.Error("failed to open sqlite", "path", cfg.DBPath, "error", err)
		os.Exit(1)
	}
	defer func() { _ = sqliteDB.Close() }()

	if err := store.MigrateNotify(sqliteDB); err != nil {
		slog.Error("notify migration failed", "error", err)
		os.Exit(1)
	}
	if err := store.MigrateSettings(sqliteDB); err != nil {
		slog.Error("settings migration failed", "error", err)
		os.Exit(1)
	}
	agentHubSvc, err := agenthub.New(sqliteDB)
	if err != nil {
		slog.Error("agent hub migration failed", "error", err)
		os.Exit(1)
	}
	agentTerminalHub := agentterminal.NewHub()

	// ── Repositories ──────────────────────────────────────────────────────────
	channelRepo, channelRepoErr := store.NewNotificationChannelRepository(sqliteDB, cfg.DataDir)
	if channelRepoErr != nil {
		slog.Warn("notification channels unavailable until the protected secret store is repaired", "error", channelRepoErr)
	}
	ruleRepo := store.NewAlertRuleRepository(sqliteDB)
	historyRepo := store.NewAlertHistoryRepository(sqliteDB)
	deliveryRepo := store.NewNotificationDeliveryReceiptRepository(sqliteDB)
	settingsRepo := store.NewSettingsRepository(sqliteDB)

	// ── Services ──────────────────────────────────────────────────────────────
	settingsSvc := settings.New(settingsRepo, config.Version)
	if err := api.InitBindService(cfg.DataDir); err != nil {
		slog.Warn("BIND lifecycle recovery needs attention", "error", err)
	}

	// ── Alert checker ─────────────────────────────────────────────────────────
	appCtx, appCancel := context.WithCancel(context.Background())
	defer appCancel()
	streamCtx, streamCancel := context.WithCancel(context.Background())
	defer streamCancel()

	checker := notify.NewAlertChecker(channelRepo, ruleRepo, historyRepo, deliveryRepo)
	go checker.Run(appCtx)

	// ── Deploy service ────────────────────────────────────────────────────────
	if err := api.InitDeployServiceWithRuntimeConfig(
		sqliteDB, cfg.DataDir, cfg.NginxSitesAvailable, cfg.NginxSitesEnabled,
		cfg.CertbotBin, cfg.CertbotConfigDir, cfg.ACMEWebroot,
	); err != nil {
		slog.Warn("deploy service init failed (non-fatal)", "error", err)
	} else {
		api.StartDeployTLSMaintenance(appCtx, 12*time.Hour)
	}

	// ── Backup + Google Drive offsite ─────────────────────────────────────────
	gdriveService, snapshotService := api.InitBackupServices(cfg, settingsRepo, channelRepo)
	if gdriveService != nil {
		gdriveService.SetReceiptRecorder(deliveryRepo)
	}

	// ── Uptime monitoring ─────────────────────────────────────────────────────
	if err := store.MigrateUptime(sqliteDB); err != nil {
		slog.Error("uptime migration failed", "error", err)
		os.Exit(1)
	}

	uptimeRepo := store.NewUptimeRepository(sqliteDB)
	uptimeEngine := uptime.NewEngine(uptimeRepo, channelRepo, settingsSvc, deliveryRepo)
	go uptimeEngine.Start(appCtx)

	// ── Metrics collection (separate DB) ──────────────────────────────────────
	metricsDBPath := filepath.Join(cfg.DataDir, "metrics.db")
	metricsDB, err := sql.Open("sqlite3", metricsDBPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		slog.Error("failed to open metrics database", "path", metricsDBPath, "error", err)
		os.Exit(1)
	}
	defer func() { _ = metricsDB.Close() }()

	if err := store.MigrateMetrics(metricsDB); err != nil {
		slog.Error("metrics migration failed", "error", err)
		os.Exit(1)
	}

	metricsRepo := store.NewMetricsRepository(metricsDB)
	metricsCollector := monitor.NewCollector(api.MonitorCache(), metricsRepo)
	go metricsCollector.Start(appCtx)
	slog.Info("metrics collector ready", "db", metricsDBPath)

	// ── HTTP router ───────────────────────────────────────────────────────────
	deps := &api.Deps{
		ChannelRepo:    channelRepo,
		DeliveryRepo:   deliveryRepo,
		RuleRepo:       ruleRepo,
		HistoryRepo:    historyRepo,
		Settings:       settingsSvc,
		UptimeEngine:   uptimeEngine,
		MetricsRepo:    metricsRepo,
		AgentHub:       agentHubSvc,
		AgentTerminals: agentTerminalHub,
		GDrive:         gdriveService,
		Snapshot:       snapshotService,
		AppCtx:         appCtx,
		ShutdownCtx:    streamCtx,
	}
	router := api.NewRouter(cfg, webFS, deps)

	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		slog.Info("HServer Panel starting", "port", cfg.Port, "version", config.Version)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server failed", "error", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("shutting down...")
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutCancel()

	streamCancel()
	appCancel()
	if err := server.Shutdown(shutCtx); err != nil {
		slog.Error("forced shutdown", "error", err)
	}
	slog.Info("server stopped")
}
