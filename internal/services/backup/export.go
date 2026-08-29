package backup

import (
	"os"
	"time"
)

// PgDumpGzipExported runs pg_dump/mysqldump to a gzip file (exported for snapshot staging).
func PgDumpGzipExported(db, engine, outPath string, level int, timeout time.Duration, onLog func(string)) error {
	return pgDumpGzipWithLog(db, engine, outPath, level, timeout, onLog)
}

// ValidateDatabaseBackupSizeFile rejects empty or stub database dumps.
func ValidateDatabaseBackupSizeFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	return validateDatabaseBackupSize(info.Size())
}
