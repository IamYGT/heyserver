package backup

import "strings"

const minFilesBackupBytes = 1024

// BackupValidity returns whether a backup file on disk looks genuine (not a failed dump stub).
func BackupValidity(filename string, size int64) string {
	if size <= 0 {
		return "invalid"
	}
	lower := strings.ToLower(filename)
	if strings.HasSuffix(lower, ".sql.gz") || strings.HasSuffix(lower, ".sql") {
		if size < minDatabaseBackupBytes {
			return "invalid"
		}
		return "completed"
	}
	if strings.HasSuffix(lower, ".tar.gz") {
		if size < minFilesBackupBytes {
			return "invalid"
		}
		return "completed"
	}
	return "completed"
}
