'use server';

import { DatabaseService, type DbEngine } from '@/lib/services/database';

export async function listDatabases(engine: DbEngine) {
  try {
    return { success: true, data: await DatabaseService.listDatabases(engine) };
  } catch (error) {
    return { success: false, error: String(error instanceof Error ? error.message : error) };
  }
}

export async function createDatabase(engine: DbEngine, name: string, owner = 'postgres') {
  try {
    if (engine === 'postgresql') {
      await DatabaseService.createPostgresDatabase(name, owner);
    } else {
      await DatabaseService.createMariaDbDatabase(name);
    }
    return { success: true };
  } catch (error) {
    return { success: false, error: String(error instanceof Error ? error.message : error) };
  }
}

export async function dropDatabase(engine: DbEngine, name: string) {
  try {
    if (engine === 'postgresql') {
      await DatabaseService.dropPostgresDatabase(name);
    } else {
      await DatabaseService.dropMariaDbDatabase(name);
    }
    return { success: true };
  } catch (error) {
    return { success: false, error: String(error instanceof Error ? error.message : error) };
  }
}

export async function listTables(engine: DbEngine, database: string) {
  try {
    return { success: true, data: await DatabaseService.listTables(engine, database) };
  } catch (error) {
    return { success: false, error: String(error instanceof Error ? error.message : error) };
  }
}

export async function getTableStructure(engine: DbEngine, database: string, table: string) {
  try {
    return { success: true, data: await DatabaseService.getTableStructure(engine, database, table) };
  } catch (error) {
    return { success: false, error: String(error instanceof Error ? error.message : error) };
  }
}

export async function queryTable(
  engine: DbEngine,
  database: string,
  query: string,
  readOnly = true
) {
  try {
    return { success: true, data: await DatabaseService.executeQuery(engine, database, query, readOnly) };
  } catch (error) {
    return { success: false, error: String(error instanceof Error ? error.message : error) };
  }
}

export async function exportDatabase(engine: DbEngine, database: string) {
  try {
    const filePath = await DatabaseService.exportDatabase(engine, database);
    return { success: true, data: { filePath } };
  } catch (error) {
    return { success: false, error: String(error instanceof Error ? error.message : error) };
  }
}

export async function listUsers(engine: DbEngine) {
  try {
    return { success: true, data: await DatabaseService.listUsers(engine) };
  } catch (error) {
    return { success: false, error: String(error instanceof Error ? error.message : error) };
  }
}

export async function createUser(
  engine: DbEngine,
  data: { username: string; password: string; canCreateDb?: boolean; isSuper?: boolean; host?: string }
) {
  try {
    if (engine === 'postgresql') {
      await DatabaseService.createPostgresUser(data.username, data.password, {
        canCreateDb: data.canCreateDb,
        isSuper: data.isSuper,
      });
    } else {
      await DatabaseService.createMariaDbUser(data.username, data.password, data.host ?? 'localhost');
    }
    return { success: true };
  } catch (error) {
    return { success: false, error: String(error instanceof Error ? error.message : error) };
  }
}

export async function dropUser(engine: DbEngine, username: string, host = 'localhost') {
  try {
    if (engine === 'postgresql') {
      await DatabaseService.dropPostgresUser(username);
    } else {
      await DatabaseService.dropMariaDbUser(username, host);
    }
    return { success: true };
  } catch (error) {
    return { success: false, error: String(error instanceof Error ? error.message : error) };
  }
}
