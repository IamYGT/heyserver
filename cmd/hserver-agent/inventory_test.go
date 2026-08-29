package main

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

func TestCollectReportsBuildArchitecture(t *testing.T) {
	collector := inventoryCollector{
		services: serviceController{runner: &fakeRunner{}},
		readFile: func(string) ([]byte, error) { return nil, fmt.Errorf("fixture unavailable") },
	}
	inventory, _ := collector.collect(context.Background(), nil)
	if inventory.Arch != runtime.GOARCH {
		t.Fatalf("inventory architecture = %q, want %q", inventory.Arch, runtime.GOARCH)
	}
}

func TestInventoryParsersExposeOnlyBoundedFields(t *testing.T) {
	osData := []byte("NAME=Ubuntu\nVERSION_ID=24.04\nPRETTY_NAME=\"Ubuntu 24.04.4 LTS\"\nSECRET=value\n")
	if got := osReleaseName(osData); got != "Ubuntu 24.04.4 LTS" {
		t.Fatalf("osReleaseName() = %q", got)
	}
	total, available, swapTotal, swapFree := parseMeminfo([]byte("MemTotal: 100 kB\nMemAvailable: 25 kB\nSwapTotal: 99 kB\nSwapFree: 9 kB\n"))
	if total != 102400 || available != 25600 || swapTotal != 101376 || swapFree != 9216 {
		t.Fatalf("parseMeminfo() = %d, %d, %d, %d", total, available, swapTotal, swapFree)
	}
}

func TestCollectProcessesReportsStableBoundedIdentity(t *testing.T) {
	runner := &fakeRunner{outputs: [][]byte{[]byte("42 app 12.5 3.2 1024 /usr/bin/php worker\n")}}
	collector := inventoryCollector{
		services: serviceController{runner: runner},
		readFile: func(path string) ([]byte, error) {
			if path != "/proc/42/stat" {
				return nil, fmt.Errorf("unexpected path %s", path)
			}
			return []byte("42 (php worker) S" + strings.Repeat(" 0", 18) + " 987654 0\n"), nil
		},
	}
	processes, err := collector.collectProcesses(context.Background())
	if err != nil {
		t.Fatalf("collectProcesses() error = %v", err)
	}
	if len(processes) != 1 || processes[0].PID != 42 || processes[0].StartTime != 987654 || processes[0].RSS != 1024*1024 || processes[0].Command != "/usr/bin/php worker" {
		t.Fatalf("processes = %#v", processes)
	}
}

func TestCollectDiskMountsReturnsBoundedDfInventory(t *testing.T) {
	runner := &fakeRunner{outputs: [][]byte{[]byte("Filesystem 1-blocks Used Available Capacity Mounted on\n/dev/vda1 1000 600 400 60% /\n/dev/vdb1 2000 250 1750 13% /srv/data\n")}}
	collector := inventoryCollector{services: serviceController{runner: runner}}
	mounts, err := collector.collectDiskMounts(context.Background())
	if err != nil {
		t.Fatalf("collectDiskMounts() error = %v", err)
	}
	if len(mounts) != 2 || mounts[0].Filesystem != "/dev/vda1" || mounts[0].Mountpoint != "/" || mounts[1].Available != 1750 || mounts[1].UsePercent != 13 {
		t.Fatalf("mounts = %#v", mounts)
	}
}

func TestSwapResetAvailabilityUsesSafetyReserve(t *testing.T) {
	if eligible, _ := swapResetAvailability(2<<30, 0, 0); eligible {
		t.Fatal("missing swap was eligible")
	}
	if eligible, _ := swapResetAvailability(2<<30, 1<<30, 0); !eligible {
		t.Fatal("empty swap was not eligible")
	}
	if eligible, _ := swapResetAvailability(600<<20, 1<<30, 200<<20); eligible {
		t.Fatal("unsafe swap reset was eligible")
	}
	if eligible, reason := swapResetAvailability(1<<30, 1<<30, 200<<20); !eligible || reason != "" {
		t.Fatalf("safe swap reset = %v, %q", eligible, reason)
	}
}

func TestFilesystemUsageExcludesReservedBlocksFromPercentageDenominator(t *testing.T) {
	total, used, available, percentage := filesystemUsage(syscall.Statfs_t{
		Blocks: 1000,
		Bfree:  300,
		Bavail: 200,
		Bsize:  4096,
	})
	if total != 4_096_000 || used != 2_867_200 || available != 819_200 {
		t.Fatalf("usage = total:%d used:%d available:%d", total, used, available)
	}
	if percentage != 78 {
		t.Fatalf("percentage = %v, want df-compatible 78; used/total would incorrectly report 70", percentage)
	}
}
