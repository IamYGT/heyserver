package api

import (
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/IamYGT/heyserver/internal/services/database"
)

const (
	databaseRequestBodyLimit = 16 << 10
	databaseQueryBodyLimit   = 128 << 10
	databaseQueryTextLimit   = 64 << 10
)

func decodeDatabaseJSON(w http.ResponseWriter, r *http.Request, limit int64, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	if err := decodeStrictJSON(r, target); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			jsonError(w, http.StatusRequestEntityTooLarge, "database request body is too large")
		} else {
			jsonError(w, http.StatusBadRequest, "invalid request body")
		}
		return false
	}
	return true
}

type databaseSourceStatus struct {
	Available bool                 `json:"available"`
	State     database.SourceState `json:"state"`
	Error     string               `json:"error,omitempty"`
}

func availableDatabaseSource() databaseSourceStatus {
	return databaseSourceStatus{Available: true, State: database.SourceHealthy}
}

func unavailableDatabaseSource(err error) databaseSourceStatus {
	return databaseSourceStatus{
		Available: false,
		State:     database.ClassifySourceError(err),
		Error:     err.Error(),
	}
}

// handleDBList lists all databases from both PostgreSQL and MariaDB (if available).
// GET /api/databases
func handleDBList() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var all []database.DBInfo
		sources := map[string]databaseSourceStatus{}
		requested := strings.TrimSpace(r.URL.Query().Get("engine"))
		if requested != "" {
			if _, ok := database.NormalizeEngine(requested); !ok {
				jsonError(w, http.StatusBadRequest, "engine must be 'postgres' or 'mariadb'")
				return
			}
		}

		if requested == "" || requested == "postgres" || requested == "postgresql" {
			pgDBs, err := database.ListPostgresDatabases()
			if err == nil {
				all = append(all, pgDBs...)
				sources["postgresql"] = availableDatabaseSource()
			} else {
				sources["postgresql"] = unavailableDatabaseSource(err)
			}
		}

		if requested == "" || requested == "mariadb" || requested == "mysql" {
			myDBs, err := database.ListMariaDBDatabases()
			if err == nil {
				all = append(all, myDBs...)
				sources["mariadb"] = availableDatabaseSource()
			} else {
				sources["mariadb"] = unavailableDatabaseSource(err)
			}
		}

		if all == nil {
			all = []database.DBInfo{}
		}

		jsonResponse(w, http.StatusOK, map[string]interface{}{
			"databases": all,
			"sources":   sources,
		})
	}
}

// handleDBCreate creates a new database.
// POST /api/databases
// Body: { "engine": "postgres"|"mariadb", "name": "mydb", "owner": "myuser" }
func handleDBCreate() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Engine string `json:"engine"`
			Name   string `json:"name"`
			Owner  string `json:"owner"`
		}
		if !decodeDatabaseJSON(w, r, databaseRequestBodyLimit, &req) {
			return
		}

		req.Name = strings.TrimSpace(req.Name)
		req.Owner = strings.TrimSpace(req.Owner)

		if req.Name == "" {
			jsonError(w, http.StatusBadRequest, "database name is required")
			return
		}
		if err := database.ValidateIdentifier(req.Name); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid database name")
			return
		}
		if req.Owner != "" {
			if err := database.ValidateIdentifier(req.Owner); err != nil {
				jsonError(w, http.StatusBadRequest, "invalid database owner")
				return
			}
		}

		engine, ok := database.NormalizeEngine(req.Engine)
		if !ok {
			jsonError(w, http.StatusBadRequest, "engine must be 'postgres' or 'mariadb'")
			return
		}

		if err := database.CreateDatabase(engine, req.Name, req.Owner); err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}

		jsonResponse(w, http.StatusCreated, map[string]string{
			"message": "database created successfully",
			"name":    req.Name,
			"engine":  string(engine),
		})
	}
}

// handleDBDrop drops a database. Requires explicit "confirm" field.
// DELETE /api/databases/{engine}/{name}
// Body: { "confirm": "DROP mydb" }
func handleDBDrop() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		engineParam := r.PathValue("engine")
		dbName := r.PathValue("name")

		if dbName == "" || engineParam == "" {
			jsonError(w, http.StatusBadRequest, "engine and database name are required")
			return
		}

		// Require explicit confirmation body.
		var req struct {
			Confirm string `json:"confirm"`
		}
		if !decodeDatabaseJSON(w, r, databaseRequestBodyLimit, &req) {
			return
		}

		expected := "DROP " + dbName
		if strings.TrimSpace(req.Confirm) != expected {
			jsonError(w, http.StatusBadRequest,
				`confirmation required: set "confirm" to "DROP `+dbName+`"`)
			return
		}

		engine, ok := database.NormalizeEngine(engineParam)
		if !ok {
			jsonError(w, http.StatusBadRequest, "engine must be 'postgres' or 'mariadb'")
			return
		}
		if err := database.ValidateIdentifier(dbName); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid database name")
			return
		}

		if err := database.DropDatabase(engine, dbName); err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}

		jsonResponse(w, http.StatusOK, map[string]string{
			"message": "database dropped",
			"name":    dbName,
		})
	}
}

// handleDBTables lists tables in a database.
// GET /api/databases/{engine}/{name}/tables
func handleDBTables() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		engineParam := r.PathValue("engine")
		dbName := r.PathValue("name")

		if dbName == "" || engineParam == "" {
			jsonError(w, http.StatusBadRequest, "engine and database name are required")
			return
		}

		engine, ok := database.NormalizeEngine(engineParam)
		if !ok {
			jsonError(w, http.StatusBadRequest, "engine must be 'postgres' or 'mariadb'")
			return
		}
		if err := database.ValidateIdentifier(dbName); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid database name")
			return
		}

		tables, err := database.ListTables(engine, dbName)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}

		if tables == nil {
			tables = []database.TableInfo{}
		}

		jsonResponse(w, http.StatusOK, map[string]interface{}{
			"database": dbName,
			"engine":   string(engine),
			"tables":   tables,
		})
	}
}

// handleDBQuery executes a SQL query in read-only mode.
// write_mode is retained in the request shape for compatibility but must be false.
// POST /api/databases/{engine}/{name}/query
// Body: { "query": "SELECT ...", "write_mode": false }
func handleDBQuery() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		engineParam := r.PathValue("engine")
		dbName := r.PathValue("name")

		if dbName == "" || engineParam == "" {
			jsonError(w, http.StatusBadRequest, "engine and database name are required")
			return
		}

		var req struct {
			Query     string `json:"query"`
			WriteMode bool   `json:"write_mode"`
		}
		if !decodeDatabaseJSON(w, r, databaseQueryBodyLimit, &req) {
			return
		}

		req.Query = strings.TrimSpace(req.Query)
		if req.Query == "" {
			jsonError(w, http.StatusBadRequest, "query is required")
			return
		}
		if len(req.Query) > databaseQueryTextLimit || !utf8.ValidString(req.Query) || strings.ContainsRune(req.Query, '\x00') {
			jsonError(w, http.StatusBadRequest, "query must be valid UTF-8 text without NUL bytes and at most 64 KiB")
			return
		}
		if req.WriteMode {
			jsonError(w, http.StatusBadRequest, "write_mode is not supported; use the writable terminal for database mutations")
			return
		}

		engine, ok := database.NormalizeEngine(engineParam)
		if !ok {
			jsonError(w, http.StatusBadRequest, "engine must be 'postgres' or 'mariadb'")
			return
		}
		if err := database.ValidateIdentifier(dbName); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid database name")
			return
		}

		// Query execution remains transaction-enforced read-only for every role.
		result, err := database.ExecuteReadOnly(engine, dbName, req.Query)
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		if result.Columns == nil {
			result.Columns = []string{}
		}
		if result.Rows == nil {
			result.Rows = [][]interface{}{}
		}

		jsonResponse(w, http.StatusOK, map[string]interface{}{
			"result": result,
		})
	}
}

// handleDBUsers lists database users/roles from all available engines.
// GET /api/databases/users
func handleDBUsers() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		engineParam := r.URL.Query().Get("engine")
		if engineParam != "" {
			if _, ok := database.NormalizeEngine(engineParam); !ok {
				jsonError(w, http.StatusBadRequest, "engine must be 'postgres' or 'mariadb'")
				return
			}
		}

		var allUsers []database.DBUser
		sources := map[string]databaseSourceStatus{}

		if engineParam == "" || engineParam == "postgres" || engineParam == "postgresql" {
			pgUsers, err := database.ListUsers(database.EnginePostgres)
			if err == nil {
				allUsers = append(allUsers, pgUsers...)
				sources["postgresql"] = availableDatabaseSource()
			} else {
				sources["postgresql"] = unavailableDatabaseSource(err)
			}
		}

		if engineParam == "" || engineParam == "mariadb" || engineParam == "mysql" {
			myUsers, err := database.ListUsers(database.EngineMariaDB)
			if err == nil {
				allUsers = append(allUsers, myUsers...)
				sources["mariadb"] = availableDatabaseSource()
			} else {
				sources["mariadb"] = unavailableDatabaseSource(err)
			}
		}

		if allUsers == nil {
			allUsers = []database.DBUser{}
		}

		jsonResponse(w, http.StatusOK, map[string]interface{}{
			"users":   allUsers,
			"sources": sources,
		})
	}
}

// handlePGMCredentials lists all database credentials from pgm_metadata.
func handlePGMCredentials() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		creds, err := database.ListPGMCredentials()
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if creds == nil {
			creds = []database.PGMCredential{}
		}
		jsonResponse(w, http.StatusOK, creds)
	}
}

// handlePGMCredentialGet returns a single credential by db name.
func handlePGMCredentialGet() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		name := r.PathValue("name")
		if err := database.ValidateIdentifier(name); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid database name")
			return
		}
		cred, err := database.GetPGMCredential(name)
		if err != nil {
			if errors.Is(err, database.ErrCredentialNotFound) {
				jsonError(w, http.StatusNotFound, err.Error())
			} else {
				jsonError(w, http.StatusInternalServerError, err.Error())
			}
			return
		}
		jsonResponse(w, http.StatusOK, cred)
	}
}

// handlePGMCredentialsList lists all database credentials from pgm_metadata.
// GET /api/databases/pgm-credentials
// Alias for handlePGMCredentials — registered under a separate path for clarity.
func handlePGMCredentialsList() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		creds, err := database.ListPGMCredentials()
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if creds == nil {
			creds = []database.PGMCredential{}
		}
		jsonResponse(w, http.StatusOK, creds)
	}
}

// handlePGMBackups lists all pgm backup directories.
func handlePGMBackups() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		backups, err := database.ListPGMBackups()
		if err != nil {
			jsonError(w, pgmBackupErrorStatus(err), err.Error())
			return
		}
		if backups == nil {
			backups = []database.PGMBackup{}
		}
		jsonResponse(w, http.StatusOK, backups)
	}
}

// handlePGMBackupFiles lists .sql.gz / .sql files inside a specific backup directory.
// GET /api/databases/pgm-backup-files/{name}
func handlePGMBackupFiles() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if name == "" {
			jsonError(w, http.StatusBadRequest, "backup name is required")
			return
		}
		files, err := database.ListPGMBackupFiles(name)
		if err != nil {
			jsonError(w, pgmBackupErrorStatus(err), err.Error())
			return
		}
		if files == nil {
			files = []string{}
		}
		jsonResponse(w, http.StatusOK, files)
	}
}

// handlePGMRestore restores a single database from a pgm backup.
// POST /api/databases/pgm-restore
// Body: { "database": "mydb", "backupPath": "/configured/backup/root/20260409_060007/mydb.sql.gz" }
func handlePGMRestore() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Database   string `json:"database"`
			BackupPath string `json:"backupPath"`
		}
		if !decodeDatabaseJSON(w, r, databaseRequestBodyLimit, &req) {
			return
		}
		req.Database = strings.TrimSpace(req.Database)
		req.BackupPath = strings.TrimSpace(req.BackupPath)
		if req.Database == "" || req.BackupPath == "" {
			jsonError(w, http.StatusBadRequest, "database and backupPath are required")
			return
		}

		if err := database.RestorePGMBackup(req.Database, req.BackupPath); err != nil {
			jsonError(w, pgmBackupErrorStatus(err), err.Error())
			return
		}

		jsonResponse(w, http.StatusOK, map[string]string{
			"message":  "restore completed successfully",
			"database": req.Database,
		})
	}
}

func pgmBackupErrorStatus(err error) int {
	switch {
	case errors.Is(err, database.ErrInvalidBackupInput), errors.Is(err, database.ErrInvalidIdentifier):
		return http.StatusBadRequest
	case errors.Is(err, database.ErrBackupNotFound):
		return http.StatusNotFound
	case errors.Is(err, database.ErrBackupRootUnavailable):
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}
