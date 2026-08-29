package disk

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseDFOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want map[string]dfEntry
	}{
		{
			name: "single_mount",
			in:   "Filesystem     1B-blocks         Used    Available Use%\n/var  1000000000  400000000  600000000  40%\n",
			want: map[string]dfEntry{
				"/var": {size: 1000000000, used: 400000000, avail: 600000000, pct: 40},
			},
		},
		{
			name: "multiple_mounts",
			in: "Filesystem     1B-blocks         Used    Available Use%\n" +
				"/       5000000000  2000000000  3000000000  40%\n" +
				"/tmp     1000000000   100000000   900000000  10%\n",
			want: map[string]dfEntry{
				"/":    {size: 5000000000, used: 2000000000, avail: 3000000000, pct: 40},
				"/tmp": {size: 1000000000, used: 100000000, avail: 900000000, pct: 10},
			},
		},
		{
			name: "header_only",
			in:   "Filesystem     1B-blocks         Used    Available Use%\n",
			want: map[string]dfEntry{},
		},
		{
			name: "short_line_skipped",
			in:   "Filesystem     1B-blocks         Used    Available Use%\n/bad\n",
			want: map[string]dfEntry{},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := parseDFOutput(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("parseDFOutput() len = %d, want %d", len(got), len(tc.want))
			}
			for mount, want := range tc.want {
				entry, ok := got[mount]
				if !ok {
					t.Fatalf("missing mount %q", mount)
				}
				if entry != want {
					t.Fatalf("mount %q: got %+v, want %+v", mount, entry, want)
				}
			}
		})
	}
}

func TestMakePartition(t *testing.T) {
	t.Parallel()

	dfMap := map[string]dfEntry{
		"/var": {size: 1000, used: 400, avail: 600, pct: 40},
	}

	p := makePartition("sda1", "/dev/sda1", "/var", "ext4", "data", "uuid-1", json.Number("2000"), dfMap)
	if p.Name != "sda1" || p.Device != "/dev/sda1" || p.MountPoint != "/var" {
		t.Fatalf("unexpected identity fields: %+v", p)
	}
	if p.Size != 1000 || p.Used != 400 || p.Available != 600 || p.UsePct != 40 {
		t.Fatalf("df overlay failed: %+v", p)
	}
	if p.FSType != "ext4" || p.Label != "data" || p.UUID != "uuid-1" {
		t.Fatalf("metadata mismatch: %+v", p)
	}

	// Without df entry, lsblk size is used.
	p2 := makePartition("vda1", "", "/boot", "xfs", "", "", json.Number("512"), dfMap)
	if p2.Size != 512 || p2.Used != 0 {
		t.Fatalf("lsblk fallback failed: %+v", p2)
	}
	p3 := makePartition("bad", "/dev/bad", "/bad", "ext4", "", "", json.Number("-1"), dfMap)
	if p3.Size != 0 {
		t.Fatalf("negative lsblk size wrapped to %d", p3.Size)
	}
}

func TestParseLSBLKPartitionsWalksProviderNeutralDeviceTrees(t *testing.T) {
	t.Parallel()

	fixture := `{
  "blockdevices": [
    {"name":"vda","path":"/dev/vda","size":1000,"fstype":"ext4","mountpoint":"/","type":"disk"},
    {"name":"nvme0n1","path":"/dev/nvme0n1","size":2000,"type":"disk","children":[
      {"name":"nvme0n1p1","path":"/dev/nvme0n1p1","size":500,"fstype":"vfat","mountpoint":"/boot","type":"part"}
    ]},
    {"name":"sdb","path":"/dev/sdb","size":3000,"type":"disk","children":[
      {"name":"sdb2","path":"/dev/sdb2","size":2500,"type":"part","children":[
        {"name":"cryptdata","path":"/dev/mapper/cryptdata","size":2400,"type":"crypt","children":[
          {"name":"vg-data","path":"/dev/mapper/vg-data","size":2300,"fstype":"xfs","mountpoint":"/var","label":"data","uuid":"uuid-data","type":"lvm"}
        ]}
      ]}
    ]},
    {"name":"loop0","path":"/dev/loop0","size":100,"fstype":"squashfs","mountpoint":"/snap/example","type":"loop"},
    {"name":"zram0","path":"/dev/zram0","size":100,"mountpoint":"[SWAP]","type":"zram"},
    {"name":"sdc","path":"/dev/sdc","size":4000,"type":"disk"}
  ]
}`
	dfMap := map[string]dfEntry{
		"/":     {size: 900, used: 300, avail: 600, pct: 33},
		"/boot": {size: 450, used: 50, avail: 400, pct: 11},
		"/var":  {size: 2200, used: 1200, avail: 1000, pct: 55},
	}

	partitions, err := parseLSBLKPartitions(fixture, dfMap)
	if err != nil {
		t.Fatal(err)
	}
	if len(partitions) != 3 {
		t.Fatalf("partitions = %#v", partitions)
	}
	want := []struct {
		name, device, mount, kind string
	}{
		{name: "vda", device: "/dev/vda", mount: "/", kind: "ext4"},
		{name: "nvme0n1p1", device: "/dev/nvme0n1p1", mount: "/boot", kind: "vfat"},
		{name: "vg-data", device: "/dev/mapper/vg-data", mount: "/var", kind: "xfs"},
	}
	for index, expected := range want {
		got := partitions[index]
		if got.Name != expected.name || got.Device != expected.device || got.MountPoint != expected.mount || got.FSType != expected.kind {
			t.Fatalf("partition %d = %#v, want %#v", index, got, expected)
		}
	}
	if partitions[2].Label != "data" || partitions[2].UUID != "uuid-data" || partitions[2].Size != 2200 {
		t.Fatalf("nested metadata or df overlay was lost: %#v", partitions[2])
	}
}

func TestParseLSBLKPartitionsRejectsMalformedInventory(t *testing.T) {
	t.Parallel()
	if _, err := parseLSBLKPartitions(`{"blockdevices":`, nil); err == nil {
		t.Fatal("malformed lsblk inventory unexpectedly succeeded")
	}
}

func TestIsValidDevice(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"sda", "sda", true},
		{"nvme0n1", "nvme0n1", true},
		{"vdb2", "vdb2", true},
		{"too_short", "sd", false},
		{"invalid_chars", "sda!", false},
		{"path_traversal", "../dev", false},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isValidDevice(tc.in); got != tc.want {
				t.Fatalf("isValidDevice(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestIsAllowedPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		path string
		want bool
	}{
		{"/", true},
		{"/var/log/nginx", true},
		{"/tmp/cache", true},
		{"/home/user", true},
		{"/opt/hserver-panel", true},
		{"/etc/nginx", true},
		{"/usr/local", true},
		{"/root/.ssh", true},
		{"/srv/data", true},
		{"/boot/efi", true},
		{"/var/../etc/passwd", true}, // Clean resolves under /var
		{"/proc/1", false},
		{"/sys/kernel", false},
		{"/mnt/external", false},
		{"", false},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.path, func(t *testing.T) {
			t.Parallel()
			if got := isAllowedPath(tc.path); got != tc.want {
				t.Fatalf("isAllowedPath(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

func TestParseJournalSize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want uint64
	}{
		{
			name: "megabytes",
			in:   "Archived and active journals take up 256.0M in the file system.",
			want: 256 * 1024 * 1024,
		},
		{
			name: "gigabytes",
			in:   "Archived and active journals take up 1.5G in the file system.",
			want: uint64(1.5 * 1024 * 1024 * 1024),
		},
		{
			name: "kilobytes",
			in:   "Archived and active journals take up 512K in the file system.",
			want: 512 * 1024,
		},
		{
			name: "no_match",
			in:   "no size information here",
			want: 0,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := parseJournalSize(tc.in); got != tc.want {
				t.Fatalf("parseJournalSize() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestGetSmartInfoInvalidDevice(t *testing.T) {
	t.Parallel()

	_, err := GetSmartInfo("sda!")
	if err == nil {
		t.Fatal("expected error for invalid device")
	}
	if !strings.Contains(err.Error(), "invalid device") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRootFilesystemSourceUsesObservedDevice(t *testing.T) {
	t.Parallel()

	source, err := rootFilesystemSource("Filesystem\n/dev/nvme0n1p2\n")
	if err != nil {
		t.Fatal(err)
	}
	if source != "/dev/nvme0n1p2" {
		t.Fatalf("source = %q", source)
	}
	if _, err := rootFilesystemSource("Filesystem\n"); err == nil {
		t.Fatal("header-only df output unexpectedly succeeded")
	}
}

func TestRootPhysicalDeviceDoesNotAssumeSDA(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		output      string
		wantDevice  string
		wantMessage string
	}{
		{name: "virtio", output: "/dev/vda1 part\n/dev/vda disk\n", wantDevice: "/dev/vda"},
		{name: "nvme", output: "/dev/nvme0n1p2 part\n/dev/nvme0n1 disk\n", wantDevice: "/dev/nvme0n1"},
		{name: "lvm", output: "/dev/mapper/vg-root lvm\n/dev/sdb2 part\n/dev/sdb disk\n", wantDevice: "/dev/sdb"},
		{name: "no physical disk", output: "/dev/dm-0 crypt\n", wantMessage: "No physical disk"},
		{name: "multiple disks", output: "/dev/md0 raid1\n/dev/sda disk\n/dev/sdb disk\n", wantMessage: "multiple physical disks"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			device, message := rootPhysicalDevice(test.output)
			if device != test.wantDevice {
				t.Fatalf("device = %q, want %q", device, test.wantDevice)
			}
			if test.wantMessage != "" && !strings.Contains(message, test.wantMessage) {
				t.Fatalf("message = %q, want substring %q", message, test.wantMessage)
			}
		})
	}
}

func TestListDirRejectsDisallowedPath(t *testing.T) {
	t.Parallel()

	_, err := ListDir("/proc")
	if err == nil {
		t.Fatal("expected error for disallowed path")
	}
}
