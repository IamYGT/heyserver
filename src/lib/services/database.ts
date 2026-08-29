import { exec } from 'child_process';
import { promisify } from 'util';

const execAsync = promisify(exec);

export type DbEngine = 'postgresql' | 'mariadb';

export interface DatabaseInfo {
  name: string;
  owner: string;
  size: string;
  sizeBytes: number;
  tableCount: number;
}

export interface TableInfo {
  name: string;
  schema: string;
  rowCount: number;
  sizeBytes: number;
  size: string;
  type: 'table' | 'view' | 'materialized_view';
}

export interface ColumnInfo {
  name: string;
  type: string;
  nullable: boolean;
  default: string | null;
  isPrimary: boolean;
}

export interface IndexInfo {
  name: string;
  columns: string[];
  isUnique: boolean;
  isPrimary: boolean;
}

export interface TableStructure {
  columns: ColumnInfo[];
  indexes: IndexInfo[];
}

export interface QueryResult {
  columns: string[];
  rows: Record<string, string | number | boolean | null>[];
  rowCount: number;
  executionTime: number;
}

export interface DbUser {
  username: string;
  isSuper: boolean;
  canCreateDb: boolean;
  canCreateRole: boolean;
  connectionLimit: number;
}

function formatBytes(bytes: number): string {
  if (bytes === 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.floor(Math.log(bytes) / Math.log(1024));
  return `${(bytes / Math.pow(1024, i)).toFixed(1)} ${units[i]}`;
}

function parseCSVLine(line: string): string[] {
  const result: string[] = [];
  let current = '';
  let inQuotes = false;
  for (let i = 0; i < line.length; i++) {
    const char = line[i];
    if (char === '"') {
      if (inQuotes && line[i + 1] === '"') { current += '"'; i++; }
      else { inQuotes = !inQuotes; }
    } else if (char === ',' && !inQuotes) {
      result.push(current);
      current = '';
    } else {
      current += char;
    }
  }
  result.push(current);
  return result;
}

async function runPsql(query: string, database = 'postgres'): Promise<string> {
  const safeDb = database.replace(/[^a-zA-Z0-9_\-]/g, '');
  const { stdout, stderr } = await execAsync(
    `psql -U postgres -d ${safeDb} -t -A -F '|' -c ${JSON.stringify(query)}`,
    { timeout: 30000, maxBuffer: 1024 * 1024 * 50 }
  );
  if (stderr && !stderr.includes('NOTICE') && !stderr.includes('WARNING')) {
    throw new Error(stderr.trim());
  }
  return stdout;
}

async function runMysql(query: string, database = ''): Promise<string> {
  const dbFlag = database ? `-D ${database.replace(/[^a-zA-Z0-9_\-]/g, '')}` : '';
  const { stdout, stderr } = await execAsync(
    `mysql -u root --batch --skip-column-names ${dbFlag} -e ${JSON.stringify(query)}`,
    { timeout: 30000, maxBuffer: 1024 * 1024 * 50 }
  );
  if (stderr && !stderr.toLowerCase().includes('warning')) {
    throw new Error(stderr.trim());
  }
  return stdout;
}

export class DatabaseService {
  // ── PostgreSQL ───────────────────────────────────────────────────────────────

  static async listPostgresDatabases(): Promise<DatabaseInfo[]> {
    const out = await runPsql(`
      SELECT d.datname,
             pg_catalog.pg_get_userbyid(d.datdba) AS owner,
             pg_catalog.pg_database_size(d.datname) AS size_bytes
      FROM pg_catalog.pg_database d
      WHERE d.datistemplate = false
      ORDER BY d.datname
    `);
    const rows = out.trim().split('\n').filter(Boolean);
    const results: DatabaseInfo[] = [];
    for (const row of rows) {
      const [name, owner, sizeBytesStr] = row.split('|');
      const sizeBytes = parseInt(sizeBytesStr ?? '0', 10) || 0;
      let tableCount = 0;
      try {
        const tc = await runPsql(
          `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema='public' AND table_type='BASE TABLE'`,
          name
        );
        tableCount = parseInt(tc.trim(), 10) || 0;
      } catch { tableCount = 0; }
      results.push({ name: name ?? '', owner: owner ?? '', sizeBytes, size: formatBytes(sizeBytes), tableCount });
    }
    return results;
  }

  static async createPostgresDatabase(name: string, owner: string): Promise<void> {
    const safeName = name.replace(/[^a-zA-Z0-9_\-]/g, '');
    const safeOwner = owner.replace(/[^a-zA-Z0-9_\-]/g, '');
    await runPsql(`CREATE DATABASE "${safeName}" OWNER "${safeOwner}"`);
  }

  static async dropPostgresDatabase(name: string): Promise<void> {
    const safeName = name.replace(/[^a-zA-Z0-9_\-]/g, '');
    await runPsql(`DROP DATABASE IF EXISTS "${safeName}"`);
  }

  static async listPostgresTables(database: string): Promise<TableInfo[]> {
    const out = await runPsql(`
      SELECT t.table_name, t.table_schema, t.table_type,
             COALESCE(pg_total_relation_size(quote_ident(t.table_schema)||'.'||quote_ident(t.table_name)),0) AS size_bytes
      FROM information_schema.tables t
      WHERE t.table_schema NOT IN ('pg_catalog','information_schema')
      ORDER BY t.table_schema, t.table_name
    `, database);
    return out.trim().split('\n').filter(Boolean).map(row => {
      const [tableName, schema, tableType, sizeBytesStr] = row.split('|');
      const sizeBytes = parseInt(sizeBytesStr ?? '0', 10) || 0;
      const type: TableInfo['type'] = tableType === 'VIEW' ? 'view' :
        tableType === 'MATERIALIZED VIEW' ? 'materialized_view' : 'table';
      return { name: tableName ?? '', schema: schema ?? '', rowCount: 0, sizeBytes, size: formatBytes(sizeBytes), type };
    });
  }

  static async getPostgresTableStructure(database: string, table: string, schema = 'public'): Promise<TableStructure> {
    const safeTable = table.replace(/'/g, "''");
    const safeSchema = schema.replace(/'/g, "''");
    const colOut = await runPsql(`
      SELECT c.column_name, c.udt_name, c.is_nullable, c.column_default,
             CASE WHEN pk.column_name IS NOT NULL THEN 'YES' ELSE 'NO' END
      FROM information_schema.columns c
      LEFT JOIN (
        SELECT kcu.column_name FROM information_schema.table_constraints tc
        JOIN information_schema.key_column_usage kcu
          ON tc.constraint_name=kcu.constraint_name AND tc.table_schema=kcu.table_schema
        WHERE tc.constraint_type='PRIMARY KEY' AND tc.table_name='${safeTable}' AND tc.table_schema='${safeSchema}'
      ) pk ON c.column_name=pk.column_name
      WHERE c.table_name='${safeTable}' AND c.table_schema='${safeSchema}'
      ORDER BY c.ordinal_position
    `, database);
    const columns: ColumnInfo[] = colOut.trim().split('\n').filter(Boolean).map(row => {
      const [name, type, nullable, def, isPk] = row.split('|');
      return { name: name ?? '', type: type ?? '', nullable: nullable === 'YES', default: def && def !== '' ? def : null, isPrimary: isPk === 'YES' };
    });
    const idxOut = await runPsql(`
      SELECT i.relname, ix.indisunique, ix.indisprimary,
             array_to_string(ARRAY(SELECT a.attname FROM pg_attribute a WHERE a.attrelid=t.oid AND a.attnum=ANY(ix.indkey)),',')
      FROM pg_class t JOIN pg_index ix ON t.oid=ix.indrelid
      JOIN pg_class i ON i.oid=ix.indexrelid
      JOIN pg_namespace n ON t.relnamespace=n.oid
      WHERE t.relname='${safeTable}' AND n.nspname='${safeSchema}'
      ORDER BY i.relname
    `, database);
    const indexes: IndexInfo[] = idxOut.trim().split('\n').filter(Boolean).map(row => {
      const [name, isUnique, isPrimary, cols] = row.split('|');
      return { name: name ?? '', isUnique: isUnique === 't', isPrimary: isPrimary === 't', columns: (cols ?? '').split(',').filter(Boolean) };
    });
    return { columns, indexes };
  }

  static async queryPostgres(database: string, query: string, readOnly = true): Promise<QueryResult> {
    const safeName = database.replace(/[^a-zA-Z0-9_\-]/g, '');
    if (readOnly) {
      const upper = query.trim().toUpperCase();
      if (!['SELECT', 'WITH', 'EXPLAIN', 'SHOW'].some(s => upper.startsWith(s))) {
        throw new Error('Only SELECT/WITH/EXPLAIN/SHOW are allowed in read-only mode');
      }
    }
    const start = Date.now();
    const { stdout, stderr } = await execAsync(
      `psql -U postgres -d ${safeName} --csv -c ${JSON.stringify(query)}`,
      { timeout: 30000, maxBuffer: 1024 * 1024 * 50 }
    );
    if (stderr && !stderr.includes('NOTICE') && !stderr.includes('WARNING')) throw new Error(stderr.trim());
    const executionTime = Date.now() - start;
    const lines = stdout.trim().split('\n').filter(Boolean);
    if (lines.length === 0) return { columns: [], rows: [], rowCount: 0, executionTime };
    const columns = parseCSVLine(lines[0] ?? '');
    const rows = lines.slice(1).map(line => {
      const values = parseCSVLine(line);
      const row: Record<string, string | null> = {};
      columns.forEach((col, i) => { row[col] = values[i] ?? null; });
      return row;
    });
    return { columns, rows, rowCount: rows.length, executionTime };
  }

  static async listPostgresUsers(): Promise<DbUser[]> {
    const out = await runPsql(`SELECT rolname,rolsuper,rolcreatedb,rolcreaterole,rolconnlimit FROM pg_roles WHERE rolname NOT LIKE 'pg_%' ORDER BY rolname`);
    return out.trim().split('\n').filter(Boolean).map(row => {
      const [username, isSuper, canCreateDb, canCreateRole, connLimit] = row.split('|');
      return { username: username ?? '', isSuper: isSuper === 't', canCreateDb: canCreateDb === 't', canCreateRole: canCreateRole === 't', connectionLimit: parseInt(connLimit ?? '-1', 10) };
    });
  }

  static async createPostgresUser(username: string, password: string, options: { canCreateDb?: boolean; isSuper?: boolean } = {}): Promise<void> {
    const safeUser = username.replace(/[^a-zA-Z0-9_]/g, '');
    const safePass = password.replace(/'/g, "''");
    const perms = `${options.canCreateDb ? 'CREATEDB' : 'NOCREATEDB'} ${options.isSuper ? 'SUPERUSER' : 'NOSUPERUSER'}`;
    await runPsql(`CREATE ROLE "${safeUser}" WITH LOGIN PASSWORD '${safePass}' ${perms}`);
  }

  static async dropPostgresUser(username: string): Promise<void> {
    await runPsql(`DROP ROLE IF EXISTS "${username.replace(/[^a-zA-Z0-9_]/g, '')}"`);
  }

  static async exportPostgresDatabase(database: string): Promise<string> {
    const safeName = database.replace(/[^a-zA-Z0-9_\-]/g, '');
    const filename = `/tmp/pgdump_${safeName}_${Date.now()}.sql`;
    await execAsync(`pg_dump -U postgres -f ${filename} ${safeName}`, { timeout: 120000, maxBuffer: 1024 * 1024 * 500 });
    return filename;
  }

  // ── MariaDB ──────────────────────────────────────────────────────────────────

  static async listMariaDbDatabases(): Promise<DatabaseInfo[]> {
    const out = await runMysql(`
      SELECT s.schema_name,
             COALESCE(SUM(t.data_length+t.index_length),0) AS size_bytes,
             COUNT(t.table_name) AS table_count
      FROM information_schema.schemata s
      LEFT JOIN information_schema.tables t ON s.schema_name=t.table_schema
      WHERE s.schema_name NOT IN ('information_schema','performance_schema','mysql','sys')
      GROUP BY s.schema_name ORDER BY s.schema_name
    `);
    return out.trim().split('\n').filter(Boolean).map(row => {
      const [name, sizeBytesStr, tableCountStr] = row.split('\t');
      const sizeBytes = parseInt(sizeBytesStr ?? '0', 10) || 0;
      return { name: name ?? '', owner: 'root', sizeBytes, size: formatBytes(sizeBytes), tableCount: parseInt(tableCountStr ?? '0', 10) || 0 };
    });
  }

  static async createMariaDbDatabase(name: string): Promise<void> {
    await runMysql(`CREATE DATABASE IF NOT EXISTS \`${name.replace(/[^a-zA-Z0-9_\-]/g, '')}\``);
  }

  static async dropMariaDbDatabase(name: string): Promise<void> {
    await runMysql(`DROP DATABASE IF EXISTS \`${name.replace(/[^a-zA-Z0-9_\-]/g, '')}\``);
  }

  static async listMariaDbTables(database: string): Promise<TableInfo[]> {
    const out = await runMysql(`
      SELECT table_name, table_type, COALESCE(data_length+index_length,0), COALESCE(table_rows,0)
      FROM information_schema.tables
      WHERE table_schema='${database.replace(/'/g, "''")}'
      ORDER BY table_name
    `);
    return out.trim().split('\n').filter(Boolean).map(row => {
      const [tableName, tableType, sizeBytesStr, rowCountStr] = row.split('\t');
      const sizeBytes = parseInt(sizeBytesStr ?? '0', 10) || 0;
      return { name: tableName ?? '', schema: database, rowCount: parseInt(rowCountStr ?? '0', 10) || 0, sizeBytes, size: formatBytes(sizeBytes), type: tableType === 'VIEW' ? 'view' : 'table' as TableInfo['type'] };
    });
  }

  static async getMariaDbTableStructure(database: string, table: string): Promise<TableStructure> {
    const safeDb = database.replace(/'/g, "''");
    const safeTable = table.replace(/'/g, "''");
    const colOut = await runMysql(`SELECT column_name,column_type,is_nullable,column_default,column_key FROM information_schema.columns WHERE table_schema='${safeDb}' AND table_name='${safeTable}' ORDER BY ordinal_position`);
    const columns: ColumnInfo[] = colOut.trim().split('\n').filter(Boolean).map(row => {
      const [name, type, nullable, def, key] = row.split('\t');
      return { name: name ?? '', type: type ?? '', nullable: nullable === 'YES', default: def && def !== 'NULL' ? def : null, isPrimary: key === 'PRI' };
    });
    const idxOut = await runMysql(`SELECT index_name,GROUP_CONCAT(column_name ORDER BY seq_in_index),non_unique FROM information_schema.statistics WHERE table_schema='${safeDb}' AND table_name='${safeTable}' GROUP BY index_name,non_unique ORDER BY index_name`);
    const indexes: IndexInfo[] = idxOut.trim().split('\n').filter(Boolean).map(row => {
      const [name, cols, nonUnique] = row.split('\t');
      return { name: name ?? '', columns: (cols ?? '').split(',').filter(Boolean), isUnique: nonUnique === '0', isPrimary: name === 'PRIMARY' };
    });
    return { columns, indexes };
  }

  static async queryMariaDb(database: string, query: string, readOnly = true): Promise<QueryResult> {
    const safeName = database.replace(/[^a-zA-Z0-9_\-]/g, '');
    if (readOnly) {
      const upper = query.trim().toUpperCase();
      if (!['SELECT', 'WITH', 'EXPLAIN', 'SHOW', 'DESCRIBE', 'DESC'].some(s => upper.startsWith(s))) {
        throw new Error('Only SELECT/EXPLAIN/SHOW/DESCRIBE are allowed in read-only mode');
      }
    }
    const start = Date.now();
    const { stdout, stderr } = await execAsync(`mysql -u root --batch -D ${safeName} -e ${JSON.stringify(query)}`, { timeout: 30000, maxBuffer: 1024 * 1024 * 50 });
    if (stderr && !stderr.toLowerCase().includes('warning')) throw new Error(stderr.trim());
    const executionTime = Date.now() - start;
    const lines = stdout.trim().split('\n').filter(Boolean);
    if (lines.length === 0) return { columns: [], rows: [], rowCount: 0, executionTime };
    const columns = (lines[0] ?? '').split('\t');
    const rows = lines.slice(1).map(line => {
      const values = line.split('\t');
      const row: Record<string, string | null> = {};
      columns.forEach((col, i) => { row[col] = values[i] === 'NULL' ? null : (values[i] ?? null); });
      return row;
    });
    return { columns, rows, rowCount: rows.length, executionTime };
  }

  static async listMariaDbUsers(): Promise<DbUser[]> {
    const out = await runMysql(`SELECT DISTINCT user,host,super_priv,Create_db_priv FROM mysql.user WHERE user!='' ORDER BY user`);
    return out.trim().split('\n').filter(Boolean).map(row => {
      const [username, , isSuper, canCreateDb] = row.split('\t');
      return { username: username ?? '', isSuper: isSuper === 'Y', canCreateDb: canCreateDb === 'Y', canCreateRole: false, connectionLimit: -1 };
    });
  }

  static async createMariaDbUser(username: string, password: string, host = 'localhost'): Promise<void> {
    const safeUser = username.replace(/[^a-zA-Z0-9_]/g, '');
    const safeHost = host.replace(/[^a-zA-Z0-9_.\-%]/g, '');
    const safePass = password.replace(/'/g, "\\'");
    await runMysql(`CREATE USER IF NOT EXISTS '${safeUser}'@'${safeHost}' IDENTIFIED BY '${safePass}'`);
  }

  static async dropMariaDbUser(username: string, host = 'localhost'): Promise<void> {
    const safeUser = username.replace(/[^a-zA-Z0-9_]/g, '');
    const safeHost = host.replace(/[^a-zA-Z0-9_.\-%]/g, '');
    await runMysql(`DROP USER IF EXISTS '${safeUser}'@'${safeHost}'`);
  }

  static async exportMariaDbDatabase(database: string): Promise<string> {
    const safeName = database.replace(/[^a-zA-Z0-9_\-]/g, '');
    const filename = `/tmp/mysqldump_${safeName}_${Date.now()}.sql`;
    await execAsync(`mysqldump -u root ${safeName} > ${filename}`, { timeout: 120000, maxBuffer: 1024 * 1024 * 500 });
    return filename;
  }

  // ── Unified API ─────────────────────────────────────────────────────────────

  static async listDatabases(engine: DbEngine): Promise<DatabaseInfo[]> {
    return engine === 'postgresql' ? this.listPostgresDatabases() : this.listMariaDbDatabases();
  }

  static async listTables(engine: DbEngine, database: string): Promise<TableInfo[]> {
    return engine === 'postgresql' ? this.listPostgresTables(database) : this.listMariaDbTables(database);
  }

  static async getTableStructure(engine: DbEngine, database: string, table: string): Promise<TableStructure> {
    return engine === 'postgresql' ? this.getPostgresTableStructure(database, table) : this.getMariaDbTableStructure(database, table);
  }

  static async executeQuery(engine: DbEngine, database: string, query: string, readOnly = true): Promise<QueryResult> {
    return engine === 'postgresql' ? this.queryPostgres(database, query, readOnly) : this.queryMariaDb(database, query, readOnly);
  }

  static async listUsers(engine: DbEngine): Promise<DbUser[]> {
    return engine === 'postgresql' ? this.listPostgresUsers() : this.listMariaDbUsers();
  }

  static async exportDatabase(engine: DbEngine, database: string): Promise<string> {
    return engine === 'postgresql' ? this.exportPostgresDatabase(database) : this.exportMariaDbDatabase(database);
  }
}
