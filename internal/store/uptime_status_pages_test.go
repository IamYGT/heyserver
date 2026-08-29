package store_test

import (
	"testing"

	"github.com/IamYGT/heyserver/internal/store"
)

func TestUptimeStatusPageWritesAreAtomicAndListIncludesMonitors(t *testing.T) {
	t.Parallel()
	database := openUptimeDB(t)
	repository := store.NewUptimeRepository(database)
	monitor := &store.UptimeMonitor{
		Name: "API", Type: "http", URL: "https://example.com/health", Method: "GET",
		IntervalSecs: 60, TimeoutSecs: 30, Retries: 1, RetryInterval: 30,
		AcceptedStatusCodes: `["200-299"]`, TLSExpiryWarnDays: 14, IsActive: true, MaxRedirects: 5,
	}
	if err := repository.CreateMonitor(monitor); err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	page := &store.UptimeStatusPage{
		Slug: "operations", Title: "Operations", Theme: "auto", IsPublic: true, HistoryDays: 90,
		Monitors: []store.StatusPageMonitorEntry{{MonitorID: monitor.ID, DisplayName: "Public API", SortOrder: 1}},
	}
	if err := repository.CreateStatusPage(page); err != nil {
		t.Fatalf("CreateStatusPage: %v", err)
	}
	pages, err := repository.ListStatusPages()
	if err != nil {
		t.Fatalf("ListStatusPages: %v", err)
	}
	if len(pages) != 1 || len(pages[0].Monitors) != 1 || pages[0].Monitors[0].MonitorID != monitor.ID {
		t.Fatalf("status pages = %#v", pages)
	}

	failedCreate := &store.UptimeStatusPage{
		Slug: "broken", Title: "Broken", Theme: "auto", IsPublic: true, HistoryDays: 90,
		Monitors: []store.StatusPageMonitorEntry{{MonitorID: monitor.ID + 9999, SortOrder: 1}},
	}
	if err := repository.CreateStatusPage(failedCreate); err == nil {
		t.Fatal("status page with missing monitor unexpectedly succeeded")
	}
	if failedCreate.ID != 0 || failedCreate.CreatedAt != "" {
		t.Fatalf("failed create leaked rolled-back identity: %#v", failedCreate)
	}
	broken, err := repository.GetStatusPage("broken")
	if err != nil {
		t.Fatalf("GetStatusPage(broken): %v", err)
	}
	if broken != nil {
		t.Fatalf("failed create left status page: %#v", broken)
	}

	page.Title = "Must Roll Back"
	page.Monitors = []store.StatusPageMonitorEntry{{MonitorID: monitor.ID + 9999, SortOrder: 1}}
	if err := repository.UpdateStatusPage(page); err == nil {
		t.Fatal("status page update with missing monitor unexpectedly succeeded")
	}
	stored, err := repository.GetStatusPage(page.ID)
	if err != nil {
		t.Fatalf("GetStatusPage: %v", err)
	}
	if stored == nil || stored.Title != "Operations" || len(stored.Monitors) != 1 || stored.Monitors[0].MonitorID != monitor.ID {
		t.Fatalf("failed update was not rolled back: %#v", stored)
	}
}
