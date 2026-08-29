package backup

import (
	"context"
	"os"
	"os/exec"
)

func mysqlDefaultsArgs() []string {
	if filePath := os.Getenv("HSERVER_MYSQL_DEFAULTS_FILE"); filePath != "" {
		return []string{"--defaults-extra-file=" + filePath}
	}
	return nil
}

func firstAvailableBinary(candidates ...string) string {
	for _, candidate := range candidates {
		if _, err := exec.LookPath(candidate); err == nil {
			return candidate
		}
	}
	return candidates[len(candidates)-1]
}

func mysqlDumpCommand(ctx context.Context, database string) *exec.Cmd {
	args := mysqlDefaultsArgs()
	if database == "all" {
		args = append(args, "--all-databases")
	} else {
		args = append(args, database)
	}
	return exec.CommandContext(ctx, firstAvailableBinary("mariadb-dump", "mysqldump"), args...)
}

func mysqlRestoreCommand(ctx context.Context, database string) *exec.Cmd {
	args := mysqlDefaultsArgs()
	if database != "all" {
		args = append(args, database)
	}
	return exec.CommandContext(ctx, firstAvailableBinary("mariadb", "mysql"), args...)
}
