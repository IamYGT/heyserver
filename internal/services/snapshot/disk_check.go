package snapshot

import (
	"fmt"
	"syscall"
)

type rootDiskInfo struct {
	UsedPercent int
	FreeBytes   uint64
}

func getRootDiskInfo() (rootDiskInfo, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs("/", &stat); err != nil {
		return rootDiskInfo{}, err
	}
	bsize := uint64(stat.Bsize)
	total := stat.Blocks * bsize
	free := stat.Bavail * bsize
	if total == 0 {
		return rootDiskInfo{}, nil
	}
	used := total - free
	return rootDiskInfo{
		UsedPercent: int(used * 100 / total),
		FreeBytes:   free,
	}, nil
}

// minFreeBytesForSnapshot is the minimum free root space before blocking a new snapshot.
const minFreeBytesForSnapshot = 5 * 1024 * 1024 * 1024 // 5 GiB

func checkDiskForSnapshot() error {
	info, err := getRootDiskInfo()
	if err != nil {
		return nil // don't block if stat fails
	}
	if info.FreeBytes < minFreeBytesForSnapshot {
		freeGB := info.FreeBytes / (1024 * 1024 * 1024)
		return fmt.Errorf("yalnızca ~%d GB boş disk — snapshot için en az 5 GB gerekli; yerel arşivleri temizleyin", freeGB)
	}
	return nil
}
