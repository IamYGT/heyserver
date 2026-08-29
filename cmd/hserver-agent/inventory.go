package main

import (
	"bufio"
	"context"
	"errors"
	"math"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"unicode/utf8"

	"github.com/IamYGT/heyserver/internal/agenthub"
)

const maxLocalFileBytes = 1 << 20
const swapSafetyReserve = uint64(512 * 1024 * 1024)

type inventoryCollector struct {
	services serviceController
	readFile func(string) ([]byte, error)
}

func newInventoryCollector(services serviceController) inventoryCollector {
	return inventoryCollector{services: services, readFile: os.ReadFile}
}

func (c inventoryCollector) collect(ctx context.Context, observed []string) (agenthub.Inventory, error) {
	inv := agenthub.Inventory{Arch: runtime.GOARCH}
	var errs []error

	if data, err := c.readBounded("/etc/os-release"); err == nil {
		inv.OS = osReleaseName(data)
	} else {
		errs = append(errs, err)
	}
	if inv.OS == "" {
		inv.OS = runtime.GOOS
	}

	var uts syscall.Utsname
	if err := syscall.Uname(&uts); err == nil {
		inv.Kernel = charsToString(uts.Release[:])
	} else {
		errs = append(errs, err)
	}
	if data, err := c.readBounded("/proc/sys/kernel/random/boot_id"); err == nil {
		inv.BootID = strings.TrimSpace(string(data))
	} else {
		errs = append(errs, err)
	}
	if data, err := c.readBounded("/proc/uptime"); err == nil {
		fields := strings.Fields(string(data))
		if len(fields) > 0 {
			if value, parseErr := strconv.ParseFloat(fields[0], 64); parseErr == nil {
				inv.UptimeSeconds = int64(value)
			}
		}
	} else {
		errs = append(errs, err)
	}
	if data, err := c.readBounded("/proc/loadavg"); err == nil {
		fields := strings.Fields(string(data))
		if len(fields) > 0 {
			inv.Load1, _ = strconv.ParseFloat(fields[0], 64)
		}
	} else {
		errs = append(errs, err)
	}
	if data, err := c.readBounded("/proc/meminfo"); err == nil {
		inv.MemoryTotal, inv.MemoryAvailable, inv.SwapTotal, inv.SwapFree = parseMeminfo(data)
		if inv.SwapFree > inv.SwapTotal {
			inv.SwapFree = inv.SwapTotal
		}
		inv.SwapUsed = inv.SwapTotal - inv.SwapFree
		inv.SwapResetEligible, inv.SwapResetReason = swapResetAvailability(inv.MemoryAvailable, inv.SwapTotal, inv.SwapUsed)
	} else {
		errs = append(errs, err)
	}

	var disk syscall.Statfs_t
	if err := syscall.Statfs("/", &disk); err == nil {
		inv.DiskTotal, inv.DiskUsed, inv.DiskAvailable, inv.DiskUsePercent = filesystemUsage(disk)
	} else {
		errs = append(errs, err)
	}
	if mounts, err := c.collectDiskMounts(ctx); err == nil {
		inv.DiskMounts = mounts
	} else {
		errs = append(errs, err)
	}
	if _, err := os.Stat("/usr/local/psa/version"); err == nil {
		inv.PleskPresent = true
	}

	inv.Services = make([]agenthub.ServiceState, 0, len(observed))
	for _, service := range observed {
		state, err := c.services.status(ctx, service)
		if err != nil {
			inv.Services = append(inv.Services, agenthub.ServiceState{Name: service, Active: "unknown"})
			continue
		}
		inv.Services = append(inv.Services, state)
	}
	if processes, err := c.collectProcesses(ctx); err == nil {
		inv.Processes = processes
	} else {
		errs = append(errs, err)
	}

	return inv, errors.Join(errs...)
}

func (c inventoryCollector) collectDiskMounts(ctx context.Context) ([]agenthub.DiskMount, error) {
	commandCtx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	output, err := c.services.runner.run(commandCtx, "df", "-B1", "-P", "-x", "tmpfs", "-x", "devtmpfs", "-x", "overlay", "-x", "squashfs")
	if err != nil {
		return nil, err
	}
	mounts := make([]agenthub.DiskMount, 0, 16)
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 6 || fields[0] == "Filesystem" {
			continue
		}
		size, sizeErr := strconv.ParseUint(fields[1], 10, 64)
		used, usedErr := strconv.ParseUint(fields[2], 10, 64)
		available, availableErr := strconv.ParseUint(fields[3], 10, 64)
		percent, percentErr := strconv.Atoi(strings.TrimSuffix(fields[4], "%"))
		filesystem := truncateUTF8(strings.ToValidUTF8(fields[0], ""), 255)
		mountpoint := truncateUTF8(strings.ToValidUTF8(strings.Join(fields[5:], " "), ""), 512)
		if sizeErr != nil || usedErr != nil || availableErr != nil || percentErr != nil || size == 0 || used > size || available > size || used > size-available || percent < 0 || percent > 100 || filesystem == "" || mountpoint == "" {
			continue
		}
		mounts = append(mounts, agenthub.DiskMount{
			Filesystem: filesystem, Size: size, Used: used, Available: available,
			UsePercent: percent, Mountpoint: mountpoint,
		})
		if len(mounts) == 64 {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return mounts, nil
}

func (c inventoryCollector) collectProcesses(ctx context.Context) ([]agenthub.Process, error) {
	commandCtx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	output, err := c.services.runner.run(commandCtx, "ps", "-eo", "pid=,user=,pcpu=,pmem=,rss=,args=", "--sort=-pmem", "-ww")
	if err != nil {
		return nil, err
	}
	processes := make([]agenthub.Process, 0, 50)
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 6 {
			continue
		}
		pid, pidErr := strconv.Atoi(fields[0])
		cpu, cpuErr := strconv.ParseFloat(fields[2], 64)
		memory, memoryErr := strconv.ParseFloat(fields[3], 64)
		rssKiB, rssErr := strconv.ParseUint(fields[4], 10, 64)
		if pidErr != nil || pid <= 0 || cpuErr != nil || cpu < 0 || memoryErr != nil || memory < 0 || rssErr != nil {
			continue
		}
		stat, statErr := c.readBounded("/proc/" + strconv.Itoa(pid) + "/stat")
		if statErr != nil {
			continue
		}
		startTime, startErr := parseProcessStartTime(stat)
		if startErr != nil {
			continue
		}
		user := truncateUTF8(strings.ToValidUTF8(fields[1], ""), 64)
		command := truncateUTF8(strings.ToValidUTF8(strings.Join(fields[5:], " "), ""), 512)
		if user == "" || command == "" {
			continue
		}
		processes = append(processes, agenthub.Process{
			PID: pid, StartTime: startTime, User: user, CPU: cpu, Memory: memory,
			RSS: rssKiB * 1024, Command: command,
		})
		if len(processes) == 50 {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return processes, nil
}

func parseProcessStartTime(raw []byte) (uint64, error) {
	end := strings.LastIndexByte(string(raw), ')')
	if end < 0 {
		return 0, errors.New("invalid process stat")
	}
	fields := strings.Fields(string(raw[end+1:]))
	if len(fields) <= 19 {
		return 0, errors.New("process stat is missing start time")
	}
	startTime, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil || startTime == 0 {
		return 0, errors.New("invalid process start time")
	}
	return startTime, nil
}

func truncateUTF8(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	value = value[:maxBytes]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

func filesystemUsage(stat syscall.Statfs_t) (total, used, available uint64, usePercent float64) {
	blockSize := uint64(stat.Bsize)
	total = stat.Blocks * blockSize
	usedBlocks := uint64(0)
	if stat.Blocks > stat.Bfree {
		usedBlocks = stat.Blocks - stat.Bfree
	}
	used = usedBlocks * blockSize
	available = stat.Bavail * blockSize
	denominator := used + available
	if denominator > 0 {
		// GNU df reports filesystem Use% rounded upward. Match that value so the
		// heartbeat, remote Disk tab and server-health thresholds stay identical.
		usePercent = math.Ceil(float64(used) / float64(denominator) * 100)
	}
	return total, used, available, usePercent
}

func (c inventoryCollector) readBounded(path string) ([]byte, error) {
	data, err := c.readFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) > maxLocalFileBytes {
		return nil, errors.New("local inventory file exceeds size limit")
	}
	return data, nil
}

func osReleaseName(data []byte) string {
	values := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), "=")
		if !ok || (key != "PRETTY_NAME" && key != "NAME" && key != "VERSION_ID") {
			continue
		}
		values[key] = strings.Trim(strings.TrimSpace(value), `"`)
	}
	if values["PRETTY_NAME"] != "" {
		return values["PRETTY_NAME"]
	}
	return strings.TrimSpace(values["NAME"] + " " + values["VERSION_ID"])
}

func parseMeminfo(data []byte) (total, available, swapTotal, swapFree uint64) {
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		switch strings.TrimSuffix(fields[0], ":") {
		case "MemTotal":
			total = value * 1024
		case "MemAvailable":
			available = value * 1024
		case "SwapTotal":
			swapTotal = value * 1024
		case "SwapFree":
			swapFree = value * 1024
		}
	}
	return total, available, swapTotal, swapFree
}

func swapResetAvailability(memoryAvailable, swapTotal, swapUsed uint64) (bool, string) {
	if swapTotal == 0 {
		return false, "No configured swap is active"
	}
	if swapUsed == 0 {
		return true, "Swap is already empty"
	}
	required := swapUsed + swapSafetyReserve
	if required < swapUsed || memoryAvailable < required {
		return false, "Available memory cannot safely absorb used swap"
	}
	return true, ""
}

func charsToString(chars []int8) string {
	bytes := make([]byte, 0, len(chars))
	for _, char := range chars {
		if char == 0 {
			break
		}
		bytes = append(bytes, byte(char))
	}
	return string(bytes)
}
