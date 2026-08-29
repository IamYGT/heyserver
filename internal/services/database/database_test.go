package database

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestClassifySourceError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want SourceState
	}{
		{name: "healthy", want: SourceHealthy},
		{name: "client missing", err: errors.New(`exec: "mysql": executable file not found in $PATH`), want: SourceClientMissing},
		{name: "postgres stopped", err: errors.New("could not connect to server: Connection refused"), want: SourceStopped},
		{name: "mariadb stopped", err: errors.New("Can't connect to local server through socket '/run/mysqld/mysqld.sock'"), want: SourceStopped},
		{name: "postgres authentication", err: errors.New("peer authentication failed for user postgres"), want: SourceAuthenticationFailed},
		{name: "mariadb authentication", err: errors.New("Access denied for user 'root'@'localhost'"), want: SourceAuthenticationFailed},
		{name: "unknown", err: errors.New("database inventory timed out"), want: SourceUnavailable},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifySourceError(tc.err); got != tc.want {
				t.Fatalf("ClassifySourceError(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}

func TestIsValidIdentifier(t *testing.T) {
	valid := []string{"mydb", "my_db", "MyDB123", "db-name", "a"}
	for _, v := range valid {
		if !isValidIdentifier(v) {
			t.Errorf("expected %q to be valid", v)
		}
	}

	invalid := []string{
		"", "my db", "my;db", "DROP TABLE", "../../etc", "my'db",
		"my\"db", "my`db", "my(db)", "very_long_name_that_exceeds_the_limit_of_64_characters_somehow_extra",
	}
	for _, v := range invalid {
		if isValidIdentifier(v) {
			t.Errorf("expected %q to be invalid", v)
		}
	}
}

func TestValidateReadOnlyQuery(t *testing.T) {
	allowed := []string{
		"SELECT * FROM users",
		"select id, name from products where id = 1",
		"WITH cte AS (SELECT 1) SELECT * FROM cte",
	}
	for _, q := range allowed {
		if err := validateReadOnlyQuery(q); err != nil {
			t.Errorf("query %q should be allowed, got error: %v", q, err)
		}
	}

	denied := []string{
		"INSERT INTO users VALUES (1, 'test')",
		"UPDATE users SET name='x'",
		"DELETE FROM users",
		"DROP TABLE users",
		"CREATE TABLE foo (id int)",
		"ALTER TABLE users ADD COLUMN x int",
		"TRUNCATE users",
		"GRANT ALL ON users TO admin",
	}
	for _, q := range denied {
		if err := validateReadOnlyQuery(q); err == nil {
			t.Errorf("query %q should be denied but was allowed", q)
		}
	}
}

func TestEscapeSQLString(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"normal", "normal"},
		{"it's", "it''s"},
		{"", ""},
		{"O'Brien", "O''Brien"},
	}
	for _, c := range cases {
		got := escapeSQLString(c.input)
		if got != c.want {
			t.Errorf("escapeSQLString(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestParseTabularOutput_PipeDelimited(t *testing.T) {
	out := `id|name|email
1|Alice|alice@example.com
2|Bob|bob@example.com
(2 rows)`

	result := parseTabularOutput(out, "|")
	if len(result.Columns) != 3 {
		t.Fatalf("expected 3 columns, got %d", len(result.Columns))
	}
	if result.Columns[0] != "id" {
		t.Errorf("column 0: want 'id', got %q", result.Columns[0])
	}
	if result.RowCount != 2 {
		t.Errorf("expected 2 rows, got %d", result.RowCount)
	}
	if result.Rows[0][1] != "Alice" {
		t.Errorf("row 0 col 1: want 'Alice', got %v", result.Rows[0][1])
	}
}

func TestParseTabularOutput_FilterTransactionNoise(t *testing.T) {
	out := `BEGIN
id|name
1|Alice
ROLLBACK`

	result := parseTabularOutput(out, "|")
	if len(result.Columns) != 2 {
		t.Fatalf("expected 2 columns after filtering noise, got %d: %v", len(result.Columns), result.Columns)
	}
	if result.RowCount != 1 {
		t.Errorf("expected 1 row, got %d", result.RowCount)
	}
}

func TestNormalizeEngine(t *testing.T) {
	t.Parallel()
	tests := []struct {
		raw  string
		want Engine
		ok   bool
	}{
		{"postgres", EnginePostgres, true},
		{"postgresql", EnginePostgres, true},
		{"mariadb", EngineMariaDB, true},
		{"mysql", EngineMariaDB, true},
		{"sqlite", "", false},
		{"", "", false},
	}
	for _, tc := range tests {
		got, ok := NormalizeEngine(tc.raw)
		if ok != tc.ok || got != tc.want {
			t.Errorf("NormalizeEngine(%q) = (%q, %v), want (%q, %v)", tc.raw, got, ok, tc.want, tc.ok)
		}
	}
}

func TestParseTabularOutput_Empty(t *testing.T) {
	result := parseTabularOutput("", "|")
	if result.RowCount != 0 || len(result.Columns) != 0 {
		t.Errorf("empty output: columns=%v rows=%d", result.Columns, result.RowCount)
	}
	if result.Columns == nil || result.Rows == nil {
		t.Fatalf("empty output must use non-null arrays: %+v", result)
	}
}

func TestParseTabularOutput_HeaderOnly(t *testing.T) {
	result := parseTabularOutput("id|name\n", "|")
	if len(result.Columns) != 2 {
		t.Fatalf("columns = %v", result.Columns)
	}
	if result.RowCount != 0 {
		t.Errorf("row count = %d, want 0", result.RowCount)
	}
}

func TestListPostgresDatabases_mockedExec(t *testing.T) {
	old := rawExec
	defer func() { rawExec = old }()
	rawExec = func(_ time.Duration, command string, args ...string) (string, error) {
		joined := strings.Join(args, " ")
		if command == "sudo" && strings.Contains(joined, "psql") && strings.Contains(joined, "pg_database") {
			return "appdb\tpostgres\t10 MB\n", nil
		}
		return "", nil
	}
	dbs, err := ListPostgresDatabases()
	if err != nil {
		t.Fatal(err)
	}
	if len(dbs) != 1 || dbs[0].Name != "appdb" || dbs[0].Size != "10 MB" {
		t.Fatalf("dbs = %+v", dbs)
	}
}

func TestListMariaDBDatabases_mockedExec(t *testing.T) {
	old := rawExec
	defer func() { rawExec = old }()
	calls := 0
	rawExec = func(_ time.Duration, command string, args ...string) (string, error) {
		calls++
		if command == "mysql" && strings.Contains(strings.Join(args, " "), "information_schema.SCHEMATA") {
			return "shop\t12.34\t5\n", nil
		}
		return "", nil
	}
	dbs, err := ListMariaDBDatabases()
	if err != nil {
		t.Fatal(err)
	}
	if len(dbs) != 1 || dbs[0].Name != "shop" || dbs[0].Size != "12.34 MB" || dbs[0].Tables != 5 {
		t.Fatalf("dbs = %+v", dbs)
	}
	if calls != 1 {
		t.Fatalf("expected one batched mysql query, got %d calls", calls)
	}
}

func TestExecuteReadOnly_mockedExec(t *testing.T) {
	old := rawExec
	defer func() { rawExec = old }()
	rawExec = func(_ time.Duration, command string, args ...string) (string, error) {
		if command == "sudo" && strings.Contains(strings.Join(args, " "), "psql") {
			return "id\tname\n1\tAlice\n(1 row)\n", nil
		}
		return "", nil
	}
	result, err := ExecuteReadOnly(EnginePostgres, "appdb", "SELECT id, name FROM users")
	if err != nil {
		t.Fatal(err)
	}
	if result.RowCount != 1 || result.Columns[0] != "id" {
		t.Fatalf("result = %+v", result)
	}
}

func TestListTables_postgres_mockedExec(t *testing.T) {
	old := rawExec
	defer func() { rawExec = old }()
	rawExec = func(_ time.Duration, command string, args ...string) (string, error) {
		if command == "sudo" && strings.Contains(strings.Join(args, " "), "pg_class") {
			return "public\tusers\t100\t128 kB\ttable\n", nil
		}
		return "", nil
	}
	tables, err := ListTables(EnginePostgres, "appdb")
	if err != nil {
		t.Fatal(err)
	}
	if len(tables) != 1 || tables[0].Name != "users" {
		t.Fatalf("tables = %+v", tables)
	}
}

func TestListTables_invalidDBName(t *testing.T) {
	_, err := ListTables(EnginePostgres, "bad name")
	if err == nil {
		t.Fatal("expected invalid database name error")
	}
}

func TestGetTableColumns_invalidIdentifier(t *testing.T) {
	_, err := GetTableColumns(EnginePostgres, "app", "bad col")
	if err == nil {
		t.Fatal("expected invalid identifier error")
	}
}

func TestListUsers_postgres_mockedExec(t *testing.T) {
	old := rawExec
	defer func() { rawExec = old }()
	rawExec = func(_ time.Duration, command string, args ...string) (string, error) {
		if command == "sudo" && strings.Contains(strings.Join(args, " "), "pg_roles") {
			return "alice\tt\tt\tf\t2026-01-01\n", nil
		}
		return "", nil
	}
	users, err := ListUsers(EnginePostgres)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 || users[0].Name != "alice" {
		t.Fatalf("users = %+v", users)
	}
}

func TestParsePostgresDBListOutput(t *testing.T) {
	out := "appdb\tpostgres\tUTF8\n template0\tpostgres\tUTF8\n"
	dbs := parsePostgresDBListOutput(out, func(name string) (string, int) {
		return "10 MB", 3
	})
	if len(dbs) != 1 {
		t.Fatalf("got %d dbs, want 1", len(dbs))
	}
	if dbs[0].Name != "appdb" || dbs[0].Tables != 3 {
		t.Errorf("db = %+v", dbs[0])
	}
}

func TestParseMariaDBShowDatabases(t *testing.T) {
	out := "Database\napp\ninformation_schema\nmysql\n"
	dbs := parseMariaDBShowDatabases(out, func(string) string { return "1M" })
	if len(dbs) != 1 || dbs[0].Name != "app" {
		t.Fatalf("dbs = %+v", dbs)
	}
}

func TestParsePostgresUserLines(t *testing.T) {
	alice := parsePostgresUserLines("alice\tt\tt\tf\t2026-01-01")
	if len(alice) != 1 || !alice[0].SuperUser || alice[0].Name != "alice" {
		t.Fatalf("alice = %+v", alice)
	}
	bob := parsePostgresUserLines("bob\tf\tt\tf\t2026-06-08")
	if len(bob) != 1 || !bob[0].CanLogin || bob[0].SuperUser {
		t.Fatalf("bob = %+v", bob)
	}
}

func TestParseMariaDBUserLines(t *testing.T) {
	out := "User\tSuper_priv\nroot\tY\napp\tN\n"
	users := parseMariaDBUserLines(out)
	if len(users) != 2 || !users[0].SuperUser || users[1].SuperUser {
		t.Fatalf("users = %+v", users)
	}
}

func TestCreateDatabaseInvalidName(t *testing.T) {
	if err := CreateDatabase(EnginePostgres, "bad name", ""); err == nil {
		t.Fatal("expected error for invalid identifier")
	}
}

func TestCreateDatabaseInvalidOwner(t *testing.T) {
	if err := CreateDatabase(EnginePostgres, "app", "bad owner"); !errors.Is(err, ErrInvalidIdentifier) {
		t.Fatalf("err=%v want ErrInvalidIdentifier", err)
	}
}

func TestDropDatabaseInvalidName(t *testing.T) {
	if err := DropDatabase(EngineMariaDB, "'; DROP--"); err == nil {
		t.Fatal("expected error for invalid identifier")
	}
}

func TestExportDatabaseInvalidName(t *testing.T) {
	if err := ExportDatabase(EnginePostgres, "../etc", "/tmp/x.sql"); err == nil {
		t.Fatal("expected error for invalid identifier")
	}
}

func TestListUsersUnsupportedEngine(t *testing.T) {
	_, err := ListUsers(Engine("sqlite"))
	if err == nil {
		t.Fatal("expected unsupported engine error")
	}
}

func TestValidateReadOnlyQueryDangerousFunctions(t *testing.T) {
	dangerous := []string{
		"SELECT pg_read_file('/etc/passwd')",
		"SELECT lo_import('/tmp/x')",
		"COPY users TO '/tmp/leak'",
	}
	for _, q := range dangerous {
		if err := validateReadOnlyQuery(q); err == nil {
			t.Errorf("query should be denied: %q", q)
		}
	}
}

func TestOwnerClause(t *testing.T) {
	if got := ownerClause("alice"); got != ` OWNER "alice"` {
		t.Errorf("ownerClause('alice') = %q", got)
	}
	if got := ownerClause(""); got != "" {
		t.Errorf("ownerClause('') = %q, want empty", got)
	}
	// Invalid identifier should return empty.
	if got := ownerClause("bad user!"); got != "" {
		t.Errorf("ownerClause('bad user!') = %q, want empty", got)
	}
}

func TestListTables_mariadb_mockedExec(t *testing.T) {
	old := rawExec
	defer func() { rawExec = old }()
	rawExec = func(_ time.Duration, command string, args ...string) (string, error) {
		if command == "mysql" && strings.Contains(strings.Join(args, " "), "information_schema.TABLES") {
			return "TABLE_NAME TABLE_ROWS ROUND((DATA_LENGTH+INDEX_LENGTH)/1024/1024,2) TABLE_TYPE\nusers 42 1.50 BASE TABLE\n", nil
		}
		return "", nil
	}
	tables, err := ListTables(EngineMariaDB, "shop")
	if err != nil {
		t.Fatal(err)
	}
	if len(tables) != 1 || tables[0].Name != "users" {
		t.Fatalf("tables = %+v", tables)
	}
}

func TestGetTableColumns_postgres_mockedExec(t *testing.T) {
	old := rawExec
	defer func() { rawExec = old }()
	rawExec = func(_ time.Duration, command string, args ...string) (string, error) {
		if command == "sudo" && strings.Contains(strings.Join(args, " "), "pg_attribute") {
			return "id\tinteger\tNO\t\tYES\nname\ttext\tYES\t''\tNO\n", nil
		}
		return "", nil
	}
	cols, err := GetTableColumns(EnginePostgres, "appdb", "users")
	if err != nil {
		t.Fatal(err)
	}
	if len(cols) != 2 || !cols[0].IsPrimary || !cols[1].IsNullable {
		t.Fatalf("cols = %+v", cols)
	}
}

func TestGetTableColumns_mariadb_mockedExec(t *testing.T) {
	old := rawExec
	defer func() { rawExec = old }()
	rawExec = func(_ time.Duration, command string, args ...string) (string, error) {
		if command == "mysql" && strings.Contains(strings.Join(args, " "), "information_schema.COLUMNS") {
			return "COLUMN_NAME DATA_TYPE IS_NULLABLE COLUMN_DEFAULT COLUMN_KEY\nid int NO NULL PRI\n", nil
		}
		return "", nil
	}
	cols, err := GetTableColumns(EngineMariaDB, "shop", "users")
	if err != nil {
		t.Fatal(err)
	}
	if len(cols) != 1 || !cols[0].IsPrimary {
		t.Fatalf("cols = %+v", cols)
	}
}

func TestExecuteReadOnly_mariadb_mockedExec(t *testing.T) {
	old := rawExec
	defer func() { rawExec = old }()
	rawExec = func(_ time.Duration, command string, args ...string) (string, error) {
		if command == "mysql" && strings.Contains(strings.Join(args, " "), "START TRANSACTION") {
			return "id\tname\n1\tAlice\n", nil
		}
		return "", nil
	}
	result, err := ExecuteReadOnly(EngineMariaDB, "shop", "SELECT id, name FROM users")
	if err != nil {
		t.Fatal(err)
	}
	if result.RowCount != 1 || result.Columns[0] != "id" {
		t.Fatalf("result = %+v", result)
	}
}

func TestListUsers_mariadb_mockedExec(t *testing.T) {
	old := rawExec
	defer func() { rawExec = old }()
	rawExec = func(_ time.Duration, command string, args ...string) (string, error) {
		if command == "mysql" && strings.Contains(strings.Join(args, " "), "mysql.user") {
			return "User Super_priv Grant_priv\nroot Y Y\napp N N\n", nil
		}
		return "", nil
	}
	users, err := ListUsers(EngineMariaDB)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 2 || !users[0].SuperUser || users[1].SuperUser {
		t.Fatalf("users = %+v", users)
	}
}
