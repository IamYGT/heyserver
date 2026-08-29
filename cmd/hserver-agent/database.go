package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

const maxDatabaseRows = 512

const mariaDatabaseQuery = "select s.schema_name,coalesce(sum(t.data_length+t.index_length),0),count(t.table_name),coalesce((select count(*) from information_schema.processlist p where p.db=s.schema_name),0) from information_schema.schemata s left join information_schema.tables t on t.table_schema=s.schema_name where s.schema_name not in ('information_schema','performance_schema','mysql','sys') group by s.schema_name order by 2 desc"
const mariaSessionQuery = "select id,user,coalesce(db,''),command,time,coalesce(state,''),replace(replace(left(coalesce(info,''),240),'\\n',' '),'\\r',' ') from information_schema.processlist where id<>connection_id() order by time desc limit 50"
const postgresDatabaseQuery = "select d.datname,pg_database_size(d.datname),0,coalesce(s.numbackends,0) from pg_database d left join pg_stat_database s on s.datid=d.oid where d.datistemplate=false order by 2 desc"
const postgresSessionQuery = "select pid,usename,coalesce(datname,''),state,greatest(0,extract(epoch from now()-coalesce(query_start,backend_start))::int),replace(replace(left(coalesce(query,''),240),E'\\n',' '),E'\\r',' ') from pg_stat_activity where pid<>pg_backend_pid() order by 5 desc limit 50"

type managedDatabase struct {
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	Connections int    `json:"connections"`
	Objects     int    `json:"objects"`
}

type managedDatabaseSession struct {
	ID       string `json:"id"`
	User     string `json:"user"`
	Database string `json:"database,omitempty"`
	State    string `json:"state"`
	Age      int    `json:"age_seconds"`
	Query    string `json:"query,omitempty"`
}

type managedDatabaseEngine struct {
	ID        string                   `json:"id"`
	Name      string                   `json:"name"`
	Version   string                   `json:"version"`
	Unit      string                   `json:"unit"`
	Active    string                   `json:"active"`
	DataSize  int64                    `json:"data_size"`
	Databases []managedDatabase        `json:"databases"`
	Sessions  []managedDatabaseSession `json:"sessions"`
}

type databaseController struct {
	runner          commandRunner
	allowRead       bool
	allowedRestarts map[string]struct{}
	mariadb         string
	mariadbAdmin    string
	pgClusters      string
	psql            string
	pgIsReady       string
	runuser         string
}

func newDatabaseController(runner commandRunner, allowRead bool, allowedRestarts map[string]struct{}, mariadb, mariadbAdmin, pgClusters, psql, pgIsReady, runuser string) databaseController {
	return databaseController{runner: runner, allowRead: allowRead, allowedRestarts: allowedRestarts, mariadb: mariadb, mariadbAdmin: mariadbAdmin, pgClusters: pgClusters, psql: psql, pgIsReady: pgIsReady, runuser: runuser}
}

func (c databaseController) Inventory(ctx context.Context) ([]managedDatabaseEngine, error) {
	if !c.allowRead {
		return nil, errors.New("database inventory is not enabled locally")
	}
	queryCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	engines := make([]managedDatabaseEngine, 0, 2)
	if maria, ok := c.mariaInventory(queryCtx); ok {
		engines = append(engines, maria)
	}
	if postgres, ok := c.postgresInventory(queryCtx); ok {
		engines = append(engines, postgres)
	}
	return engines, nil
}

func (c databaseController) mariaInventory(ctx context.Context) (managedDatabaseEngine, bool) {
	versionOutput, err := c.runner.run(ctx, c.mariadb, "-NBe", "select version()")
	if err != nil || strings.TrimSpace(string(versionOutput)) == "" {
		return managedDatabaseEngine{}, false
	}
	databaseOutput, _ := c.runner.run(ctx, c.mariadb, "-NBe", mariaDatabaseQuery)
	sessionOutput, _ := c.runner.run(ctx, c.mariadb, "-NBe", mariaSessionQuery)
	activeOutput, _ := c.runner.run(ctx, "systemctl", "is-active", "mariadb.service")
	databases := parseManagedDatabases(databaseOutput)
	return managedDatabaseEngine{ID: "mariadb", Name: "MariaDB", Version: strings.TrimSpace(string(versionOutput)), Unit: "mariadb.service", Active: stateOrUnknown(activeOutput), DataSize: sumDatabaseSize(databases), Databases: databases, Sessions: parseMariaSessions(sessionOutput)}, true
}

func (c databaseController) postgresInventory(ctx context.Context) (managedDatabaseEngine, bool) {
	clustersOutput, err := c.runner.run(ctx, c.pgClusters, "--no-header")
	if err != nil {
		return managedDatabaseEngine{}, false
	}
	cluster, ok := selectPostgresCluster(string(clustersOutput))
	if !ok {
		return managedDatabaseEngine{}, false
	}
	versionOutput, err := c.psqlQuery(ctx, "show server_version")
	if err != nil {
		return managedDatabaseEngine{}, false
	}
	databaseOutput, _ := c.psqlQuery(ctx, postgresDatabaseQuery)
	sessionOutput, _ := c.psqlQuery(ctx, postgresSessionQuery)
	unit := "postgresql@" + cluster.version + "-" + cluster.name + ".service"
	activeOutput, _ := c.runner.run(ctx, "systemctl", "is-active", unit)
	databases := parseManagedDatabases(databaseOutput)
	return managedDatabaseEngine{ID: "postgresql", Name: "PostgreSQL", Version: strings.TrimSpace(string(versionOutput)), Unit: unit, Active: stateOrUnknown(activeOutput), DataSize: sumDatabaseSize(databases), Databases: databases, Sessions: parsePostgresSessions(sessionOutput)}, true
}

func (c databaseController) psqlQuery(ctx context.Context, query string) ([]byte, error) {
	return c.runner.run(ctx, c.runuser, "-u", "postgres", "--", c.psql, "-AtF", "\t", "-qc", query, "postgres")
}

func (c databaseController) Action(ctx context.Context, engine, action string) (string, error) {
	if action != "restart" {
		return "", errors.New("unsupported database action")
	}
	if _, allowed := c.allowedRestarts[engine]; !allowed {
		return "", errors.New("database restart is not in the local allowlist")
	}
	actionCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	switch engine {
	case "mariadb":
		if _, err := c.runner.run(actionCtx, "systemctl", "restart", "mariadb.service"); err != nil {
			return "", fmt.Errorf("MariaDB restart failed: %w", err)
		}
		if _, err := c.runner.run(actionCtx, c.mariadbAdmin, "ping", "--silent"); err != nil {
			return "", fmt.Errorf("MariaDB socket health check failed: %w", err)
		}
		return "MariaDB restarted and socket health check passed", nil
	case "postgresql":
		clustersOutput, err := c.runner.run(actionCtx, c.pgClusters, "--no-header")
		if err != nil {
			return "", errors.New("PostgreSQL cluster inventory failed")
		}
		cluster, ok := selectPostgresCluster(string(clustersOutput))
		if !ok {
			return "", errors.New("no online PostgreSQL cluster found")
		}
		unit := "postgresql@" + cluster.version + "-" + cluster.name + ".service"
		if _, err := c.runner.run(actionCtx, "systemctl", "restart", unit); err != nil {
			return "", fmt.Errorf("PostgreSQL restart failed: %w", err)
		}
		if _, err := c.runner.run(actionCtx, c.pgIsReady, "-q", "-p", cluster.port); err != nil {
			return "", fmt.Errorf("PostgreSQL readiness check failed: %w", err)
		}
		return "PostgreSQL restarted and readiness check passed", nil
	default:
		return "", errors.New("unsupported database engine")
	}
}

type postgresCluster struct{ version, name, port string }

func selectPostgresCluster(output string) (postgresCluster, bool) {
	clusters := make([]postgresCluster, 0, 4)
	for _, raw := range strings.Split(output, "\n") {
		parts := strings.Fields(raw)
		if len(parts) < 4 || parts[3] != "online" || !validNumericVersion(parts[0]) || !agentNginxConfigNamePattern.MatchString(parts[1]) {
			continue
		}
		port, err := strconv.Atoi(parts[2])
		if err != nil || port < 1 || port > 65535 {
			continue
		}
		clusters = append(clusters, postgresCluster{version: parts[0], name: parts[1], port: parts[2]})
	}
	if len(clusters) == 0 {
		return postgresCluster{}, false
	}
	sort.Slice(clusters, func(i, j int) bool { return compareNumericVersion(clusters[i].version, clusters[j].version) > 0 })
	return clusters[0], true
}

func validNumericVersion(value string) bool {
	parts := strings.Split(value, ".")
	if len(parts) > 3 || value == "" {
		return false
	}
	for _, part := range parts {
		if _, err := strconv.Atoi(part); err != nil || part == "" {
			return false
		}
	}
	return true
}

func compareNumericVersion(left, right string) int {
	l, r := strings.Split(left, "."), strings.Split(right, ".")
	for index := 0; index < len(l) || index < len(r); index++ {
		lv, rv := 0, 0
		if index < len(l) {
			lv, _ = strconv.Atoi(l[index])
		}
		if index < len(r) {
			rv, _ = strconv.Atoi(r[index])
		}
		if lv != rv {
			return lv - rv
		}
	}
	return 0
}

func parseManagedDatabases(output []byte) []managedDatabase {
	rows := make([]managedDatabase, 0)
	for _, raw := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if raw == "" || len(rows) >= maxDatabaseRows {
			break
		}
		parts := strings.Split(raw, "\t")
		if len(parts) < 4 || len(parts[0]) > 128 || strings.ContainsAny(parts[0], "\r\n\x00") {
			continue
		}
		size, sizeErr := strconv.ParseInt(parts[1], 10, 64)
		objects, objectErr := strconv.Atoi(parts[2])
		connections, connectionErr := strconv.Atoi(parts[3])
		if sizeErr != nil || objectErr != nil || connectionErr != nil || size < 0 || objects < 0 || connections < 0 {
			continue
		}
		rows = append(rows, managedDatabase{Name: parts[0], Size: size, Objects: objects, Connections: connections})
	}
	return rows
}

func parseMariaSessions(output []byte) []managedDatabaseSession {
	return parseDatabaseSessions(output, true)
}
func parsePostgresSessions(output []byte) []managedDatabaseSession {
	return parseDatabaseSessions(output, false)
}

func parseDatabaseSessions(output []byte, maria bool) []managedDatabaseSession {
	rows := make([]managedDatabaseSession, 0, 50)
	for _, raw := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if raw == "" || len(rows) >= 50 {
			break
		}
		parts := strings.Split(raw, "\t")
		minimum := 6
		if maria {
			minimum = 7
		}
		if len(parts) < minimum {
			continue
		}
		ageIndex, state, query := 4, parts[3], parts[5]
		if maria {
			state, query = parts[3], parts[6]
			if parts[5] != "" {
				state += " · " + parts[5]
			}
		}
		age, err := strconv.Atoi(parts[ageIndex])
		if err != nil || age < 0 || len(parts[0]) > 64 || len(parts[1]) > 128 || len(parts[2]) > 128 || len(state) > 256 || len(query) > 240 {
			continue
		}
		rows = append(rows, managedDatabaseSession{ID: parts[0], User: parts[1], Database: parts[2], State: state, Age: age, Query: query})
	}
	return rows
}

func sumDatabaseSize(databases []managedDatabase) int64 {
	var total int64
	for _, database := range databases {
		if database.Size <= int64(^uint64(0)>>1)-total {
			total += database.Size
		}
	}
	return total
}
func stateOrUnknown(output []byte) string {
	if state := strings.TrimSpace(string(output)); state != "" && len(state) <= 32 {
		return state
	}
	return "unknown"
}
