package database

import (
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/IamYGT/heyserver/internal/services/shell"
)

// rawExec is swappable in tests to mock shell output without hitting the host DB.
var rawExec = shell.ExecuteRawTrusted

// Engine represents a database engine type.
type Engine string

const (
	EnginePostgres Engine = "postgres"
	EngineMariaDB  Engine = "mariadb"
)

// SourceState classifies database inventory readiness for operator recovery.
type SourceState string

const (
	SourceHealthy              SourceState = "healthy"
	SourceClientMissing        SourceState = "client-missing"
	SourceStopped              SourceState = "stopped"
	SourceAuthenticationFailed SourceState = "authentication-failed"
	SourceUnavailable          SourceState = "unavailable"
)

// ClassifySourceError maps local client errors into stable API states without
// hiding the original diagnostic returned to the operator.
func ClassifySourceError(err error) SourceState {
	if err == nil {
		return SourceHealthy
	}

	message := strings.ToLower(err.Error())
	if strings.Contains(message, "executable file not found") ||
		strings.Contains(message, "command not found") ||
		strings.Contains(message, "not found in $path") {
		return SourceClientMissing
	}

	if strings.Contains(message, "authentication failed") ||
		strings.Contains(message, "peer authentication failed") ||
		strings.Contains(message, "password authentication failed") ||
		strings.Contains(message, "access denied for user") {
		return SourceAuthenticationFailed
	}

	if strings.Contains(message, "connection refused") ||
		strings.Contains(message, "could not connect to server") ||
		strings.Contains(message, "can't connect to local server") ||
		strings.Contains(message, "can't connect to server") ||
		strings.Contains(message, "server is not running") ||
		strings.Contains(message, "lost connection") ||
		strings.Contains(message, "no such file or directory") {
		return SourceStopped
	}

	return SourceUnavailable
}

// NormalizeEngine maps aliases to canonical engine names.
// Returns the canonical engine and true, or empty and false for unknown values.
func NormalizeEngine(raw string) (Engine, bool) {
	switch Engine(strings.ToLower(raw)) {
	case EnginePostgres, "postgresql":
		return EnginePostgres, true
	case EngineMariaDB, "mysql":
		return EngineMariaDB, true
	default:
		return "", false
	}
}

// DBInfo holds metadata about a single database.
type DBInfo struct {
	Name   string `json:"name"`
	Engine Engine `json:"engine"`
	Owner  string `json:"owner"`
	Size   string `json:"size"`
	Tables int    `json:"tables"`
}

// TableInfo holds metadata about a single table.
type TableInfo struct {
	Name      string `json:"name"`
	Schema    string `json:"schema"`
	RowsEst   int64  `json:"rowsEstimate"`
	Size      string `json:"size"`
	TableType string `json:"tableType"`
}

// ColumnInfo describes a single column in a table.
type ColumnInfo struct {
	Name       string `json:"name"`
	DataType   string `json:"dataType"`
	IsNullable bool   `json:"isNullable"`
	Default    string `json:"default,omitempty"`
	IsPrimary  bool   `json:"isPrimary"`
}

// QueryResult holds the result of a SQL query execution.
type QueryResult struct {
	Columns  []string        `json:"columns"`
	Rows     [][]interface{} `json:"rows"`
	RowCount int             `json:"rowCount"`
}

// DBUser represents a database user/role.
type DBUser struct {
	Name       string `json:"name"`
	Engine     Engine `json:"engine"`
	SuperUser  bool   `json:"superUser"`
	CanLogin   bool   `json:"canLogin"`
	CreateDB   bool   `json:"createDb"`
	ValidUntil string `json:"validUntil,omitempty"`
}

// systemDatabases are internal Postgres databases that should not be shown.
var systemDatabases = map[string]bool{
	"template0": true,
	"template1": true,
}

// systemMariaDBDatabases are internal MariaDB/MySQL databases to hide.
var systemMariaDBDatabases = map[string]bool{
	"information_schema": true,
	"performance_schema": true,
	"mysql":              true,
	"sys":                true,
}

type dbMetaEnricher func(name string) (size string, tables int)

// parsePostgresDBListOutput parses psql -l tab output into DBInfo rows.
func parsePostgresDBListOutput(out string, enrich dbMetaEnricher) []DBInfo {
	if enrich == nil {
		enrich = func(string) (string, int) { return "unknown", 0 }
	}
	var dbs []DBInfo
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 3 {
			continue
		}
		name := strings.TrimSpace(parts[0])
		if name == "" || systemDatabases[name] {
			continue
		}
		owner := strings.TrimSpace(parts[1])
		size, tableCount := enrich(name)
		dbs = append(dbs, DBInfo{
			Name:   name,
			Engine: EnginePostgres,
			Owner:  owner,
			Size:   size,
			Tables: tableCount,
		})
	}
	return dbs
}

// ListPostgresDatabases returns all user-visible PostgreSQL databases.
// Uses: psql -U postgres -l -t -A -F'|'
func ListPostgresDatabases() ([]DBInfo, error) {
	query := "SELECT datname, pg_get_userbyid(datdba), pg_size_pretty(pg_database_size(datname)) FROM pg_database WHERE NOT datistemplate AND datallowconn ORDER BY datname;"
	out, err := rawExec(30*time.Second, "sudo", "-u", "postgres", "psql", "-d", "postgres", "-t", "-A", "-F\t", "-c", query)
	if err != nil {
		return nil, fmt.Errorf("listing postgres databases: %w", err)
	}
	return parsePostgresInventoryOutput(out), nil
}

func parsePostgresInventoryOutput(out string) []DBInfo {
	var dbs []DBInfo
	for _, line := range strings.Split(out, "\n") {
		parts := strings.Split(strings.TrimSpace(line), "\t")
		if len(parts) < 3 {
			continue
		}
		name := strings.TrimSpace(parts[0])
		if name == "" || systemDatabases[name] {
			continue
		}
		dbs = append(dbs, DBInfo{
			Name: name, Engine: EnginePostgres, Owner: strings.TrimSpace(parts[1]),
			Size: strings.TrimSpace(parts[2]), Tables: 0,
		})
	}
	return dbs
}

// getPostgresTableCount returns the number of user tables in a PostgreSQL database.
// Queries pg_stat_user_tables which only counts actual user-created tables
// (excludes system catalog tables in pg_catalog and information_schema).
func getPostgresTableCount(dbName string) int {
	if !isValidIdentifier(dbName) {
		return 0
	}
	cmd := exec.Command("sudo", "-u", "postgres", "psql", "-d", dbName, "-t", "-A", "-c",
		"SELECT count(*) FROM pg_stat_user_tables;")
	outBytes, err := cmd.Output()
	if err != nil {
		return 0
	}
	count, _ := strconv.Atoi(strings.TrimSpace(string(outBytes)))
	return count
}

// getPostgresDBSize returns human-readable size of a postgres database.
// It first tries pg_database_size() (requires CONNECT privilege), and falls
// back to pg_catalog.pg_database for databases the current role cannot access.
func getPostgresDBSize(dbName string) string {
	if !isValidIdentifier(dbName) {
		return "unknown"
	}

	// Primary: use pg_size_pretty with pg_database_size().
	// This requires the calling role to have CONNECT privilege on the database.
	query := fmt.Sprintf("SELECT pg_size_pretty(pg_database_size('%s'));", dbName)
	cmd := exec.Command("sudo", "-u", "postgres", "psql", "-t", "-A", "-c", query)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	outBytes, err := cmd.Output()
	if err == nil {
		if result := strings.TrimSpace(string(outBytes)); result != "" {
			return result
		}
	}

	// Fallback: read raw size bytes from pg_catalog.pg_database.
	// This does not require CONNECT on the target database — postgres superuser
	// can always read pg_catalog from any connection (e.g. the default "postgres" db).
	fallbackQuery := fmt.Sprintf(
		"SELECT pg_size_pretty(size) FROM (SELECT pg_database_size(datname) AS size FROM pg_catalog.pg_database WHERE datname = '%s') t;",
		dbName,
	)
	cmd2 := exec.Command("sudo", "-u", "postgres", "psql", "-d", "postgres", "-t", "-A", "-c", fallbackQuery)
	outBytes2, err2 := cmd2.Output()
	if err2 == nil {
		if result := strings.TrimSpace(string(outBytes2)); result != "" {
			return result
		}
	}

	return "unknown"
}

// parseMariaDBShowDatabases parses SHOW DATABASES output.
func parseMariaDBShowDatabases(out string, sizeFn func(name string) string) []DBInfo {
	if sizeFn == nil {
		sizeFn = func(string) string { return "unknown" }
	}
	var dbs []DBInfo
	for _, line := range strings.Split(out, "\n") {
		name := strings.TrimSpace(line)
		if name == "" || name == "Database" || systemMariaDBDatabases[name] {
			continue
		}
		dbs = append(dbs, DBInfo{
			Name:   name,
			Engine: EngineMariaDB,
			Size:   sizeFn(name),
		})
	}
	return dbs
}

// ListMariaDBDatabases returns all user-visible MariaDB databases.
// Uses: mysql -u root -e "SHOW DATABASES;"
func ListMariaDBDatabases() ([]DBInfo, error) {
	query := "SELECT s.SCHEMA_NAME, COALESCE(ROUND(SUM(t.DATA_LENGTH + t.INDEX_LENGTH) / 1024 / 1024, 2), 0), COUNT(t.TABLE_NAME) FROM information_schema.SCHEMATA s LEFT JOIN information_schema.TABLES t ON t.TABLE_SCHEMA = s.SCHEMA_NAME WHERE s.SCHEMA_NAME NOT IN ('information_schema','performance_schema','mysql','sys') GROUP BY s.SCHEMA_NAME ORDER BY s.SCHEMA_NAME;"
	out, err := rawExec(30*time.Second, "mysql", "-u", "root", "-N", "-B", "-e", query)
	if err != nil {
		return nil, fmt.Errorf("listing mariadb databases: %w", err)
	}
	return parseMariaDBInventoryOutput(out), nil
}

func parseMariaDBInventoryOutput(out string) []DBInfo {
	var dbs []DBInfo
	for _, line := range strings.Split(out, "\n") {
		parts := strings.Split(strings.TrimSpace(line), "\t")
		if len(parts) < 3 {
			continue
		}
		name := strings.TrimSpace(parts[0])
		if name == "" || systemMariaDBDatabases[name] {
			continue
		}
		size := strings.TrimSpace(parts[1])
		if size == "" || strings.EqualFold(size, "NULL") {
			size = "0"
		}
		tables, _ := strconv.Atoi(strings.TrimSpace(parts[2]))
		dbs = append(dbs, DBInfo{
			Name: name, Engine: EngineMariaDB, Size: size + " MB", Tables: tables,
		})
	}
	return dbs
}

// getMariaDBSize returns human-readable size of a MariaDB database.
func getMariaDBSize(dbName string) string {
	if !isValidIdentifier(dbName) {
		return "unknown"
	}
	query := fmt.Sprintf(
		"SELECT ROUND(SUM(data_length + index_length) / 1024 / 1024, 2) FROM information_schema.TABLES WHERE table_schema = '%s';",
		escapeSQLString(dbName),
	)
	out, err := rawExec(30*time.Second, "mysql", "-u", "root", "-e", query)
	if err != nil {
		return "unknown"
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) >= 2 {
		val := strings.TrimSpace(lines[1])
		if val == "NULL" {
			return "0 MB"
		}
		return val + " MB"
	}
	return "unknown"
}

// ListTables returns tables in a given database for the specified engine.
func ListTables(engine Engine, dbName string) ([]TableInfo, error) {
	if !isValidIdentifier(dbName) {
		return nil, fmt.Errorf("invalid database name")
	}
	switch engine {
	case EnginePostgres:
		return listPostgresTables(dbName)
	case EngineMariaDB:
		return listMariaDBTables(dbName)
	default:
		return nil, fmt.Errorf("unsupported engine: %s", engine)
	}
}

func listPostgresTables(dbName string) ([]TableInfo, error) {
	query := "SELECT n.nspname AS schema, c.relname AS name, c.reltuples::bigint AS rows_est, pg_size_pretty(pg_total_relation_size(c.oid)) AS size, CASE c.relkind WHEN 'r' THEN 'table' WHEN 'v' THEN 'view' WHEN 'm' THEN 'materialized view' END AS type FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE c.relkind IN ('r','v','m') AND n.nspname NOT IN ('pg_catalog','information_schema','pg_toast') ORDER BY n.nspname, c.relname;"

	out, err := rawExec(30*time.Second, "sudo", "-u", "postgres", "psql", "-d", dbName, "-t", "-A", "-F\t", "-c", query)
	if err != nil {
		return nil, fmt.Errorf("listing postgres tables in %s: %w", dbName, err)
	}

	var tables []TableInfo
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 5 {
			continue
		}
		rowsEst, _ := strconv.ParseInt(strings.TrimSpace(parts[2]), 10, 64)
		tables = append(tables, TableInfo{
			Schema:    strings.TrimSpace(parts[0]),
			Name:      strings.TrimSpace(parts[1]),
			RowsEst:   rowsEst,
			Size:      strings.TrimSpace(parts[3]),
			TableType: strings.TrimSpace(parts[4]),
		})
	}
	return tables, nil
}

func listMariaDBTables(dbName string) ([]TableInfo, error) {
	query := fmt.Sprintf(
		"SELECT TABLE_NAME, TABLE_ROWS, ROUND((DATA_LENGTH+INDEX_LENGTH)/1024/1024,2), TABLE_TYPE "+
			"FROM information_schema.TABLES WHERE TABLE_SCHEMA='%s' ORDER BY TABLE_NAME;",
		escapeSQLString(dbName),
	)

	out, err := rawExec(30*time.Second, "mysql", "-u", "root", "-e", query)
	if err != nil {
		return nil, fmt.Errorf("listing mariadb tables in %s: %w", dbName, err)
	}

	var tables []TableInfo
	lines := strings.Split(out, "\n")
	for i, line := range lines {
		if i == 0 {
			continue // skip header
		}
		parts := strings.Fields(line)
		if len(parts) < 3 {
			continue
		}
		rowsEst, _ := strconv.ParseInt(parts[1], 10, 64)
		tableType := "table"
		if len(parts) >= 4 && strings.EqualFold(parts[3], "view") {
			tableType = "view"
		}
		tables = append(tables, TableInfo{
			Schema:    dbName,
			Name:      parts[0],
			RowsEst:   rowsEst,
			Size:      parts[2] + " MB",
			TableType: tableType,
		})
	}
	return tables, nil
}

// GetTableColumns returns column definitions for a table.
func GetTableColumns(engine Engine, dbName, tableName string) ([]ColumnInfo, error) {
	if !isValidIdentifier(dbName) || !isValidIdentifier(tableName) {
		return nil, fmt.Errorf("invalid identifier")
	}
	switch engine {
	case EnginePostgres:
		return getPostgresColumns(dbName, tableName)
	case EngineMariaDB:
		return getMariaDBColumns(dbName, tableName)
	default:
		return nil, fmt.Errorf("unsupported engine: %s", engine)
	}
}

func getPostgresColumns(dbName, tableName string) ([]ColumnInfo, error) {
	query := fmt.Sprintf(`
SELECT
    a.attname AS column_name,
    pg_catalog.format_type(a.atttypid, a.atttypmod) AS data_type,
    CASE WHEN a.attnotnull THEN 'NO' ELSE 'YES' END AS is_nullable,
    COALESCE(pg_get_expr(d.adbin, d.adrelid), '') AS column_default,
    CASE WHEN pk.contype IS NOT NULL THEN 'YES' ELSE 'NO' END AS is_primary
FROM pg_attribute a
JOIN pg_class c ON c.oid = a.attrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
LEFT JOIN pg_constraint pk ON pk.conrelid = c.oid AND a.attnum = ANY(pk.conkey) AND pk.contype = 'p'
WHERE c.relname = '%s'
  AND a.attnum > 0
  AND NOT a.attisdropped
ORDER BY a.attnum;`, escapeSQLString(tableName))

	out, err := rawExec(30*time.Second, "sudo", "-u", "postgres", "psql", "-d", dbName, "-t", "-A", "-F\t", "-c", query)
	if err != nil {
		return nil, fmt.Errorf("getting postgres columns: %w", err)
	}

	var cols []ColumnInfo
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 5 {
			continue
		}
		cols = append(cols, ColumnInfo{
			Name:       strings.TrimSpace(parts[0]),
			DataType:   strings.TrimSpace(parts[1]),
			IsNullable: strings.TrimSpace(parts[2]) == "YES",
			Default:    strings.TrimSpace(parts[3]),
			IsPrimary:  strings.TrimSpace(parts[4]) == "YES",
		})
	}
	return cols, nil
}

func getMariaDBColumns(dbName, tableName string) ([]ColumnInfo, error) {
	query := fmt.Sprintf(
		"SELECT COLUMN_NAME, DATA_TYPE, IS_NULLABLE, COLUMN_DEFAULT, COLUMN_KEY "+
			"FROM information_schema.COLUMNS WHERE TABLE_SCHEMA='%s' AND TABLE_NAME='%s' ORDER BY ORDINAL_POSITION;",
		escapeSQLString(dbName), escapeSQLString(tableName),
	)
	out, err := rawExec(30*time.Second, "mysql", "-u", "root", "-e", query)
	if err != nil {
		return nil, fmt.Errorf("getting mariadb columns: %w", err)
	}

	var cols []ColumnInfo
	lines := strings.Split(out, "\n")
	for i, line := range lines {
		if i == 0 {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 3 {
			continue
		}
		defVal := ""
		if len(parts) >= 4 && parts[3] != "NULL" {
			defVal = parts[3]
		}
		isPrimary := len(parts) >= 5 && strings.EqualFold(parts[4], "pri")
		cols = append(cols, ColumnInfo{
			Name:       parts[0],
			DataType:   parts[1],
			IsNullable: strings.EqualFold(parts[2], "yes"),
			Default:    defVal,
			IsPrimary:  isPrimary,
		})
	}
	return cols, nil
}

// ExecuteReadOnly executes a SQL query wrapped in a READ ONLY transaction.
// Only SELECT and WITH (CTE) statements are allowed; anything else is rejected.
func ExecuteReadOnly(engine Engine, dbName, query string) (*QueryResult, error) {
	if err := ValidateIdentifier(dbName); err != nil {
		return nil, fmt.Errorf("database name: %w", err)
	}
	if err := validateReadOnlyQuery(query); err != nil {
		return nil, err
	}

	switch engine {
	case EnginePostgres:
		return executePostgresReadOnly(dbName, query)
	case EngineMariaDB:
		return executeMariaDBReadOnly(dbName, query)
	default:
		return nil, fmt.Errorf("unsupported engine: %s", engine)
	}
}

// validateReadOnlyQuery rejects any query that is not a SELECT or WITH statement.
// C-2 fix: pg_read_file, COPY, lo_import, lo_export and related PostgreSQL
// file-access functions are explicitly blocked to prevent arbitrary file reads
// even inside a BEGIN READ ONLY transaction.
func validateReadOnlyQuery(query string) error {
	trimmed := strings.TrimSpace(strings.ToUpper(query))
	if !strings.HasPrefix(trimmed, "SELECT") && !strings.HasPrefix(trimmed, "WITH") {
		return fmt.Errorf("only SELECT queries are permitted in read-only mode")
	}
	// Reject DML/DDL keywords and PostgreSQL file/system access functions.
	// Note: keyword matching uses uppercase (trimmed is already uppercased).
	// Trailing space is used for keywords that could be substrings (e.g. "DROP" in "DROPDOWN"),
	// but function names use "(" suffix to match calls directly.
	dangerousKeywords := []string{
		// DML / DDL
		"INSERT ", "UPDATE ", "DELETE ", "DROP ", "CREATE ", "ALTER ",
		"TRUNCATE ", "GRANT ", "REVOKE ", "EXECUTE ", "CALL ",
		// PostgreSQL file-access functions — readable even in READ ONLY transactions
		"PG_READ_FILE(", "PG_WRITE_FILE(", "PG_READ_BINARY_FILE(",
		// Large-object file I/O
		"LO_IMPORT(", "LO_EXPORT(",
		// COPY can read/write files even inside read-only transactions on superuser connections
		"COPY ", "COPY\t", "COPY\n",
		// Misc dangerous server-side functions
		"PG_EXEC(", "PG_STAT_FILE(", "DBLINK(",
	}
	for _, kw := range dangerousKeywords {
		if strings.Contains(trimmed, kw) {
			return fmt.Errorf("query contains disallowed keyword or function: %s", strings.TrimSpace(kw))
		}
	}
	return nil
}

func executePostgresReadOnly(dbName, query string) (*QueryResult, error) {
	// Wrap in a read-only transaction so even if validation is bypassed, the DB refuses writes.
	wrappedQuery := fmt.Sprintf("BEGIN READ ONLY; %s; ROLLBACK;", query)
	out, err := rawExec(30*time.Second, "sudo", "-u", "postgres", "psql", "-d", dbName, "-A", "-F\t", "-c", wrappedQuery)
	if err != nil {
		return nil, fmt.Errorf("query execution failed: %w", err)
	}
	return parseTabularOutput(out, "\t"), nil
}

func executeMariaDBReadOnly(dbName, query string) (*QueryResult, error) {
	// MariaDB: start a read-only transaction
	wrappedQuery := fmt.Sprintf("SET TRANSACTION READ ONLY; START TRANSACTION; %s; ROLLBACK;", query)
	out, err := rawExec(30*time.Second, "mysql", "-u", "root", dbName, "-e", wrappedQuery)
	if err != nil {
		return nil, fmt.Errorf("query execution failed: %w", err)
	}
	return parseTabularOutput(out, "\t"), nil
}

// parseTabularOutput converts psql/mysql tab or pipe-delimited output to QueryResult.
func parseTabularOutput(out, sep string) *QueryResult {
	result := &QueryResult{Columns: []string{}, Rows: [][]interface{}{}}
	lines := strings.Split(strings.TrimSpace(out), "\n")

	// Filter out transaction noise lines (BEGIN, ROLLBACK, START TRANSACTION etc.)
	var dataLines []string
	for _, l := range lines {
		trim := strings.TrimSpace(l)
		if trim == "" || trim == "BEGIN" || trim == "ROLLBACK" ||
			trim == "START TRANSACTION" || trim == "SET" || trim == "COMMIT" {
			continue
		}
		dataLines = append(dataLines, l)
	}

	if len(dataLines) == 0 {
		return result
	}

	// First line is column headers
	result.Columns = strings.Split(dataLines[0], sep)
	for i := range result.Columns {
		result.Columns[i] = strings.TrimSpace(result.Columns[i])
	}

	for _, line := range dataLines[1:] {
		if strings.HasPrefix(line, "(") { // e.g. "(5 rows)"
			continue
		}
		parts := strings.Split(line, sep)
		row := make([]interface{}, len(parts))
		for i, p := range parts {
			row[i] = strings.TrimSpace(p)
		}
		result.Rows = append(result.Rows, row)
	}
	result.RowCount = len(result.Rows)
	return result
}

// CreateDatabase creates a new database.
func CreateDatabase(engine Engine, dbName, owner string) error {
	if err := ValidateIdentifier(dbName); err != nil {
		return fmt.Errorf("database name: %w", err)
	}
	if owner != "" {
		if err := ValidateIdentifier(owner); err != nil {
			return fmt.Errorf("database owner: %w", err)
		}
	}

	switch engine {
	case EnginePostgres:
		_, err := rawExec(30*time.Second, "sudo", "-u", "postgres", "psql", "-c",
			fmt.Sprintf("CREATE DATABASE \"%s\"%s;", dbName, ownerClause(owner)))
		if err != nil {
			return fmt.Errorf("creating postgres database: %w", err)
		}
		return nil
	case EngineMariaDB:
		query := fmt.Sprintf("CREATE DATABASE `%s`;", dbName)
		_, err := rawExec(30*time.Second, "mysql", "-u", "root", "-e", query)
		if err != nil {
			return fmt.Errorf("creating mariadb database: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("unsupported engine: %s", engine)
	}
}

func ownerClause(owner string) string {
	if owner != "" && isValidIdentifier(owner) {
		return fmt.Sprintf(" OWNER \"%s\"", owner)
	}
	return ""
}

// DropDatabase drops a database by name. The caller MUST confirm intent before calling.
func DropDatabase(engine Engine, dbName string) error {
	if err := ValidateIdentifier(dbName); err != nil {
		return fmt.Errorf("database name: %w", err)
	}

	switch engine {
	case EnginePostgres:
		_, err := rawExec(30*time.Second, "sudo", "-u", "postgres", "psql", "-c",
			fmt.Sprintf("DROP DATABASE IF EXISTS \"%s\";", dbName))
		if err != nil {
			return fmt.Errorf("dropping postgres database: %w", err)
		}
		return nil
	case EngineMariaDB:
		_, err := rawExec(30*time.Second, "mysql", "-u", "root", "-e",
			fmt.Sprintf("DROP DATABASE IF EXISTS `%s`;", dbName))
		if err != nil {
			return fmt.Errorf("dropping mariadb database: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("unsupported engine: %s", engine)
	}
}

// ListUsers returns database users/roles for a given engine.
func ListUsers(engine Engine) ([]DBUser, error) {
	switch engine {
	case EnginePostgres:
		return listPostgresUsers()
	case EngineMariaDB:
		return listMariaDBUsers()
	default:
		return nil, fmt.Errorf("unsupported engine: %s", engine)
	}
}

func parsePostgresUserLines(out string) []DBUser {
	var users []DBUser
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 5 {
			continue
		}
		users = append(users, DBUser{
			Name:       strings.TrimSpace(parts[0]),
			Engine:     EnginePostgres,
			SuperUser:  strings.TrimSpace(parts[1]) == "t",
			CanLogin:   strings.TrimSpace(parts[2]) == "t",
			CreateDB:   strings.TrimSpace(parts[3]) == "t",
			ValidUntil: strings.TrimSpace(parts[4]),
		})
	}
	return users
}

func listPostgresUsers() ([]DBUser, error) {
	query := "SELECT rolname, rolsuper, rolcanlogin, rolcreatedb, COALESCE(rolvaliduntil::text, '') FROM pg_roles WHERE rolname NOT LIKE 'pg_%' ORDER BY rolname;"
	out, err := rawExec(30*time.Second, "sudo", "-u", "postgres", "psql", "-t", "-A", "-F\t", "-c", query)
	if err != nil {
		return nil, fmt.Errorf("listing postgres users: %w", err)
	}
	return parsePostgresUserLines(out), nil
}

func parseMariaDBUserLines(out string) []DBUser {
	var users []DBUser
	lines := strings.Split(out, "\n")
	for i, line := range lines {
		if i == 0 {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 1 {
			continue
		}
		isSuper := len(parts) >= 2 && strings.EqualFold(parts[1], "y")
		users = append(users, DBUser{
			Name:      parts[0],
			Engine:    EngineMariaDB,
			SuperUser: isSuper,
			CanLogin:  true,
		})
	}
	return users
}

func listMariaDBUsers() ([]DBUser, error) {
	query := "SELECT User, Super_priv, Grant_priv FROM mysql.user ORDER BY User;"
	out, err := rawExec(30*time.Second, "mysql", "-u", "root", "-e", query)
	if err != nil {
		return nil, fmt.Errorf("listing mariadb users: %w", err)
	}
	return parseMariaDBUserLines(out), nil
}

// ExportDatabase runs pg_dump / mysqldump and returns the output path.
func ExportDatabase(engine Engine, dbName, outputPath string) error {
	if !isValidIdentifier(dbName) {
		return fmt.Errorf("invalid database name")
	}
	switch engine {
	case EnginePostgres:
		_, err := shell.ExecuteWithTimeout(300e9, "pg_dump",
			"-U", "postgres", "-F", "c", "-f", outputPath, dbName)
		return err
	case EngineMariaDB:
		_, err := shell.ExecuteWithTimeout(300e9, "mysqldump",
			"-u", "root", "--single-transaction", "-r", outputPath, dbName)
		return err
	default:
		return fmt.Errorf("unsupported engine: %s", engine)
	}
}

// isValidIdentifier checks that a name is safe for use in SQL identifiers.
// Allows only alphanumeric, underscore, and hyphen characters (no spaces, quotes, etc.).
var validIdentifierRe = regexp.MustCompile(`^[A-Za-z0-9_\-]{1,64}$`)

// ErrInvalidIdentifier marks an operator-supplied database or owner identity
// that cannot safely be represented as a local PostgreSQL/MariaDB identifier.
var ErrInvalidIdentifier = errors.New("invalid database identifier")

// ValidateIdentifier applies the public database identity contract shared by
// the API, CLI, and command-backed service implementation.
func ValidateIdentifier(s string) error {
	if !isValidIdentifier(s) {
		return fmt.Errorf("%w: use 1-64 alphanumeric, underscore, or hyphen characters", ErrInvalidIdentifier)
	}
	return nil
}

func isValidIdentifier(s string) bool {
	return validIdentifierRe.MatchString(s)
}

// escapeSQLString escapes single quotes in a string value.
// NOTE: This is only for use inside pre-validated identifier-bounded SQL strings.
func escapeSQLString(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}
