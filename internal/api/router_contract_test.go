package api

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/IamYGT/heyserver/internal/agenthub"
	"github.com/IamYGT/heyserver/internal/auth"
	"github.com/IamYGT/heyserver/internal/db"
	"github.com/IamYGT/heyserver/internal/models"
	"github.com/IamYGT/heyserver/internal/services/settings"
	uptime "github.com/IamYGT/heyserver/internal/services/uptime"
	"github.com/IamYGT/heyserver/internal/store"
	"github.com/IamYGT/heyserver/internal/testutil"
)

var contractTestDataDir string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "hserver-api-contract-*")
	if err != nil {
		panic(err)
	}
	contractTestDataDir = dir
	tmp := filepath.Join(dir, "hserver.db")
	sqlDB, err := db.Open(tmp)
	if err != nil {
		panic("db.Open: " + err.Error())
	}
	if err := InitDeployService(sqlDB); err != nil {
		panic("InitDeployService: " + err.Error())
	}
	if err := store.MigrateMetrics(sqlDB); err != nil {
		panic("MigrateMetrics: " + err.Error())
	}
	if err := store.MigrateNotify(sqlDB); err != nil {
		panic("MigrateNotify: " + err.Error())
	}
	if err := store.MigrateSettings(sqlDB); err != nil {
		panic("MigrateSettings: " + err.Error())
	}
	if err := store.MigrateUptime(sqlDB); err != nil {
		panic("MigrateUptime: " + err.Error())
	}
	if err := seedContractTestAdmin(sqlDB); err != nil {
		panic("seedContractTestAdmin: " + err.Error())
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func seedContractTestAdmin(sqlDB *sql.DB) error {
	hash, err := auth.HashPassword("testpass")
	if err != nil {
		return err
	}
	return db.NewUserRepository(sqlDB).Create(&models.User{
		Email:    "admin@test.com",
		Name:     "Test User",
		Password: hash,
		Role:     models.RoleAdmin,
	})
}

func contractTestDeps(t *testing.T) *Deps {
	t.Helper()
	sqlDB := db.Instance()
	agentHub, err := agenthub.New(sqlDB)
	if err != nil {
		t.Fatalf("agenthub.New: %v", err)
	}
	settingsRepo := store.NewSettingsRepository(sqlDB)
	settingsSvc := settings.New(settingsRepo, "test")
	channelRepo, err := store.NewNotificationChannelRepository(sqlDB, contractTestDataDir)
	if err != nil {
		t.Fatalf("notification repository: %v", err)
	}
	uptimeRepo := store.NewUptimeRepository(sqlDB)
	return &Deps{
		MetricsRepo:  store.NewMetricsRepository(sqlDB),
		Settings:     settingsSvc,
		ChannelRepo:  channelRepo,
		DeliveryRepo: store.NewNotificationDeliveryReceiptRepository(sqlDB),
		RuleRepo:     store.NewAlertRuleRepository(sqlDB),
		HistoryRepo:  store.NewAlertHistoryRepository(sqlDB),
		UptimeEngine: uptime.NewEngine(uptimeRepo, channelRepo, settingsSvc),
		AgentHub:     agentHub,
	}
}

func TestAllRoutes_RegisteredNot404(t *testing.T) {
	handler := NewRouter(testutil.TestConfig(), testutil.MinimalWebFS(t), contractTestDeps(t))
	routes := AllRoutes()

	for _, spec := range routes {
		spec := spec
		t.Run(spec.Method+" "+spec.Path, func(t *testing.T) {
			path := fillRoutePath(spec.Path)
			req := httptest.NewRequest(spec.Method, path, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code == http.StatusNotFound {
				// Public status pages return 404 when slug is unknown — route still registered.
				if strings.HasPrefix(spec.Path, "/status/") || strings.HasPrefix(spec.Path, "/api/status/") {
					return
				}
				t.Fatalf("route not registered: %s %s → 404", spec.Method, path)
			}
		})
	}
}

func TestAllRoutes_AuthContract(t *testing.T) {
	handler := NewRouter(testutil.TestConfig(), testutil.MinimalWebFS(t), contractTestDeps(t))

	for _, spec := range AllRoutes() {
		spec := spec
		t.Run(spec.Method+" "+spec.Path, func(t *testing.T) {
			path := fillRoutePath(spec.Path)

			switch spec.Auth {
			case RouteProtected, RouteManager, RouteAdmin:
				req := httptest.NewRequest(spec.Method, path, nil)
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)
				if rec.Code != http.StatusUnauthorized {
					t.Errorf("unauthenticated: got %d want 401 for %s %s", rec.Code, spec.Method, path)
				}

			case RoutePublic:
				req := httptest.NewRequest(spec.Method, path, nil)
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)
				if rec.Code == http.StatusUnauthorized {
					t.Errorf("public route returned 401: %s %s", spec.Method, path)
				}

			case RouteAgent:
				req := httptest.NewRequest(spec.Method, path, nil)
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)
				if rec.Code != http.StatusUnauthorized {
					t.Errorf("agent route without credentials: got %d want 401 for %s %s", rec.Code, spec.Method, path)
				}

			case RouteInternalCron:
				req := httptest.NewRequest(spec.Method, path, nil)
				req.RemoteAddr = "127.0.0.1:12345"
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)
				if rec.Code == http.StatusUnauthorized {
					t.Errorf("internal cron without secret should not return 401: got %d", rec.Code)
				}
			}
		})
	}
}

func TestRouteManifest_Count(t *testing.T) {
	routes := AllRoutes()
	if len(routes) < 200 {
		t.Errorf("route manifest too small: got %d want >= 200", len(routes))
	}
}
