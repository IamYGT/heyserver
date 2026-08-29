'use client';

import { useState, useEffect, use, useTransition, useCallback } from 'react';
import { useRouter } from 'next/navigation';
import {
  ArrowLeft,
  Table2,
  Key,
  Columns,
  RefreshCw,
  Download,
  ChevronLeft,
  ChevronRight,
  AlertTriangle,
} from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { ScrollArea } from '@/components/ui/scroll-area';
import { SqlEditor } from '@/components/modules/database/sql-editor';
import { listTables, getTableStructure, queryTable, exportDatabase } from '../../actions';
import type { TableInfo, TableStructure, QueryResult } from '@/lib/services/database';

type DbEngine = 'postgresql' | 'mariadb';

const ROWS_PER_PAGE = 50;

export default function DatabaseBrowserPage({
  params,
}: {
  params: Promise<{ engine: string; name: string }>;
}) {
  const { engine: engineParam, name } = use(params);
  const engine = engineParam as DbEngine;
  const dbName = decodeURIComponent(name);
  const router = useRouter();

  const [tables, setTables] = useState<TableInfo[]>([]);
  const [selectedTable, setSelectedTable] = useState<string | null>(null);
  const [structure, setStructure] = useState<TableStructure | null>(null);
  const [tableData, setTableData] = useState<QueryResult | null>(null);
  const [dataPage, setDataPage] = useState(1);
  const [activeTab, setActiveTab] = useState('data');
  const [loading, setLoading] = useState(true);
  const [structureLoading, setStructureLoading] = useState(false);
  const [dataLoading, setDataLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [exporting, setExporting] = useState(false);
  const [, startTransition] = useTransition();

  useEffect(() => {
    setLoading(true);
    startTransition(async () => {
      const res = await listTables(engine, dbName);
      if (res.success && res.data) {
        setTables(res.data);
        if (res.data.length > 0) setSelectedTable(res.data[0]?.name ?? null);
      } else {
        setError(res.error ?? 'Failed to load tables');
      }
      setLoading(false);
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [engine, dbName]);

  const loadTableStructure = useCallback(async (tableName: string) => {
    setStructureLoading(true);
    const res = await getTableStructure(engine, dbName, tableName);
    if (res.success && res.data) setStructure(res.data);
    else setError(res.error ?? 'Failed to load structure');
    setStructureLoading(false);
  }, [engine, dbName]);

  const loadTableData = useCallback(async (tableName: string, page: number) => {
    setDataLoading(true);
    const offset = (page - 1) * ROWS_PER_PAGE;
    const quoted = engine === 'postgresql'
      ? `"${tableName.replace(/"/g, '""')}"`
      : `\`${tableName.replace(/`/g, '``')}\``;
    const query = `SELECT * FROM ${quoted} LIMIT ${ROWS_PER_PAGE} OFFSET ${offset}`;
    const res = await queryTable(engine, dbName, query, true);
    if (res.success && res.data) setTableData(res.data);
    else setError(res.error ?? 'Failed to load data');
    setDataLoading(false);
  }, [engine, dbName]);

  useEffect(() => {
    if (!selectedTable) return;
    setDataPage(1);
    loadTableStructure(selectedTable);
    loadTableData(selectedTable, 1);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedTable]);

  useEffect(() => {
    if (!selectedTable) return;
    loadTableData(selectedTable, dataPage);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [dataPage]);

  const handleExport = async () => {
    setExporting(true);
    const res = await exportDatabase(engine, dbName);
    if (res.success && res.data) alert(`Export saved to: ${res.data.filePath}`);
    else setError(res.error ?? 'Export failed');
    setExporting(false);
  };

  const selectedTableInfo = tables.find(t => t.name === selectedTable);

  return (
    <div className="flex flex-col gap-4 p-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <Button variant="ghost" size="sm" onClick={() => router.push('/databases')}>
            <ArrowLeft className="size-4" />
          </Button>
          <div>
            <h1 className="text-xl font-semibold tracking-tight flex items-center gap-2">
              <span className="font-mono">{dbName}</span>
              <Badge variant="outline" className="text-xs font-normal">
                {engine === 'postgresql' ? 'PostgreSQL' : 'MariaDB'}
              </Badge>
            </h1>
            <p className="text-xs text-muted-foreground mt-0.5">{tables.length} tables</p>
          </div>
        </div>
        <Button variant="outline" size="sm" onClick={handleExport} disabled={exporting}>
          <Download className="size-4 mr-1.5" />
          {exporting ? 'Exporting...' : 'Export DB'}
        </Button>
      </div>

      {/* Error */}
      {error && (
        <div className="flex items-center gap-2 rounded-md border border-destructive/50 bg-destructive/10 p-3 text-sm text-destructive">
          <AlertTriangle className="size-4 shrink-0" />
          {error}
          <button onClick={() => setError(null)} className="ml-auto text-xs underline">Dismiss</button>
        </div>
      )}

      <div className="flex gap-4 min-h-0">
        {/* Table sidebar */}
        <Card className="w-56 shrink-0">
          <CardHeader className="pb-2">
            <CardTitle className="text-xs font-medium text-muted-foreground uppercase tracking-wider">
              Tables ({tables.length})
            </CardTitle>
          </CardHeader>
          <CardContent className="p-0">
            {loading ? (
              <div className="flex items-center justify-center h-24 text-sm text-muted-foreground">Loading...</div>
            ) : (
              <ScrollArea className="h-[calc(100vh-280px)]">
                <ul className="py-1">
                  {tables.map(table => (
                    <li key={table.name}>
                      <button
                        onClick={() => setSelectedTable(table.name)}
                        className={`w-full flex items-center gap-2 px-3 py-2 text-sm text-left transition-colors hover:bg-accent ${
                          selectedTable === table.name ? 'bg-accent font-medium' : ''
                        }`}
                      >
                        <Table2 className="size-3.5 shrink-0 text-muted-foreground" />
                        <span className="font-mono text-xs truncate">{table.name}</span>
                      </button>
                    </li>
                  ))}
                </ul>
              </ScrollArea>
            )}
          </CardContent>
        </Card>

        {/* Main content */}
        <div className="flex-1 min-w-0">
          {selectedTable ? (
            <Tabs value={activeTab} onValueChange={setActiveTab}>
              <div className="flex items-center justify-between mb-3">
                <TabsList>
                  <TabsTrigger value="data" className="gap-1.5">
                    <Table2 className="size-3.5" />Data
                  </TabsTrigger>
                  <TabsTrigger value="structure" className="gap-1.5">
                    <Columns className="size-3.5" />Structure
                  </TabsTrigger>
                  <TabsTrigger value="sql" className="gap-1.5">
                    <Key className="size-3.5" />SQL Editor
                  </TabsTrigger>
                </TabsList>
                {activeTab === 'data' && (
                  <Button variant="ghost" size="sm" onClick={() => selectedTable && loadTableData(selectedTable, dataPage)} disabled={dataLoading}>
                    <RefreshCw className={`size-3.5 ${dataLoading ? 'animate-spin' : ''}`} />
                  </Button>
                )}
              </div>

              {/* Data Tab */}
              <TabsContent value="data">
                <Card>
                  <CardHeader className="pb-2">
                    <div className="flex items-center justify-between">
                      <CardTitle className="text-sm font-mono">{selectedTable}</CardTitle>
                      {selectedTableInfo && (
                        <span className="text-xs text-muted-foreground">
                          ~{selectedTableInfo.rowCount.toLocaleString()} rows · {selectedTableInfo.size}
                        </span>
                      )}
                    </div>
                  </CardHeader>
                  <CardContent>
                    {dataLoading ? (
                      <div className="flex items-center justify-center h-32 text-sm text-muted-foreground">Loading data...</div>
                    ) : tableData && tableData.rows.length > 0 ? (
                      <>
                        <ScrollArea className="h-[calc(100vh-440px)] rounded-md border">
                          <Table>
                            <TableHeader className="sticky top-0 bg-muted/80 backdrop-blur-sm z-10">
                              <TableRow>
                                <TableHead className="w-10 text-center text-xs text-muted-foreground">#</TableHead>
                                {tableData.columns.map(col => (
                                  <TableHead key={col} className="text-xs font-mono">{col}</TableHead>
                                ))}
                              </TableRow>
                            </TableHeader>
                            <TableBody>
                              {tableData.rows.map((row, i) => (
                                <TableRow key={i} className="hover:bg-muted/30">
                                  <TableCell className="text-center text-xs text-muted-foreground">
                                    {(dataPage - 1) * ROWS_PER_PAGE + i + 1}
                                  </TableCell>
                                  {tableData.columns.map(col => (
                                    <TableCell key={col} className="font-mono text-xs max-w-xs truncate">
                                      {row[col] === null
                                        ? <span className="text-muted-foreground italic">NULL</span>
                                        : String(row[col])
                                      }
                                    </TableCell>
                                  ))}
                                </TableRow>
                              ))}
                            </TableBody>
                          </Table>
                        </ScrollArea>
                        <div className="flex items-center justify-between mt-3">
                          <span className="text-xs text-muted-foreground">
                            Page {dataPage} · {tableData.rowCount} rows shown
                          </span>
                          <div className="flex items-center gap-1">
                            <Button variant="outline" size="sm" onClick={() => setDataPage(p => Math.max(1, p - 1))} disabled={dataPage <= 1}>
                              <ChevronLeft className="size-4" />
                            </Button>
                            <Button variant="outline" size="sm" onClick={() => setDataPage(p => p + 1)} disabled={tableData.rowCount < ROWS_PER_PAGE}>
                              <ChevronRight className="size-4" />
                            </Button>
                          </div>
                        </div>
                      </>
                    ) : (
                      <div className="flex items-center justify-center h-32 text-sm text-muted-foreground">
                        {tableData ? 'No rows in this table' : 'Select a table to browse data'}
                      </div>
                    )}
                  </CardContent>
                </Card>
              </TabsContent>

              {/* Structure Tab */}
              <TabsContent value="structure">
                <div className="flex flex-col gap-4">
                  <Card>
                    <CardHeader className="pb-2">
                      <CardTitle className="text-sm flex items-center gap-2">
                        <Columns className="size-4" />Columns
                      </CardTitle>
                    </CardHeader>
                    <CardContent>
                      {structureLoading ? (
                        <div className="flex items-center justify-center h-24 text-sm text-muted-foreground">Loading...</div>
                      ) : structure ? (
                        <Table>
                          <TableHeader>
                            <TableRow>
                              <TableHead className="text-xs">Column</TableHead>
                              <TableHead className="text-xs">Type</TableHead>
                              <TableHead className="text-xs">Nullable</TableHead>
                              <TableHead className="text-xs">Default</TableHead>
                              <TableHead className="text-xs">Key</TableHead>
                            </TableRow>
                          </TableHeader>
                          <TableBody>
                            {structure.columns.map(col => (
                              <TableRow key={col.name}>
                                <TableCell className="font-mono text-xs font-medium">{col.name}</TableCell>
                                <TableCell>
                                  <Badge variant="secondary" className="text-xs font-mono">{col.type}</Badge>
                                </TableCell>
                                <TableCell>
                                  <Badge variant={col.nullable ? 'outline' : 'secondary'} className="text-xs">
                                    {col.nullable ? 'YES' : 'NO'}
                                  </Badge>
                                </TableCell>
                                <TableCell className="font-mono text-xs text-muted-foreground">
                                  {col.default ?? '—'}
                                </TableCell>
                                <TableCell>
                                  {col.isPrimary && (
                                    <Badge className="text-xs bg-amber-500/20 text-amber-700 dark:text-amber-400 border border-amber-500/30">PK</Badge>
                                  )}
                                </TableCell>
                              </TableRow>
                            ))}
                          </TableBody>
                        </Table>
                      ) : null}
                    </CardContent>
                  </Card>

                  {structure && structure.indexes.length > 0 && (
                    <Card>
                      <CardHeader className="pb-2">
                        <CardTitle className="text-sm flex items-center gap-2">
                          <Key className="size-4" />Indexes
                        </CardTitle>
                      </CardHeader>
                      <CardContent>
                        <Table>
                          <TableHeader>
                            <TableRow>
                              <TableHead className="text-xs">Name</TableHead>
                              <TableHead className="text-xs">Columns</TableHead>
                              <TableHead className="text-xs">Unique</TableHead>
                              <TableHead className="text-xs">Primary</TableHead>
                            </TableRow>
                          </TableHeader>
                          <TableBody>
                            {structure.indexes.map(idx => (
                              <TableRow key={idx.name}>
                                <TableCell className="font-mono text-xs">{idx.name}</TableCell>
                                <TableCell>
                                  <div className="flex flex-wrap gap-1">
                                    {idx.columns.map(col => (
                                      <Badge key={col} variant="outline" className="text-xs font-mono">{col}</Badge>
                                    ))}
                                  </div>
                                </TableCell>
                                <TableCell>
                                  <Badge variant={idx.isUnique ? 'default' : 'outline'} className="text-xs">
                                    {idx.isUnique ? 'YES' : 'NO'}
                                  </Badge>
                                </TableCell>
                                <TableCell>
                                  {idx.isPrimary && (
                                    <Badge className="text-xs bg-amber-500/20 text-amber-700 dark:text-amber-400 border border-amber-500/30">PK</Badge>
                                  )}
                                </TableCell>
                              </TableRow>
                            ))}
                          </TableBody>
                        </Table>
                      </CardContent>
                    </Card>
                  )}
                </div>
              </TabsContent>

              {/* SQL Editor Tab */}
              <TabsContent value="sql">
                <Card>
                  <CardContent className="pt-4">
                    <SqlEditor
                      engine={engine}
                      database={dbName}
                      onExecute={(q, ro) => queryTable(engine, dbName, q, ro)}
                      defaultQuery={
                        selectedTable
                          ? engine === 'postgresql'
                            ? `SELECT * FROM "${selectedTable}" LIMIT 50;`
                            : `SELECT * FROM \`${selectedTable}\` LIMIT 50;`
                          : 'SELECT 1;'
                      }
                    />
                  </CardContent>
                </Card>
              </TabsContent>
            </Tabs>
          ) : (
            <div className="flex items-center justify-center h-64 text-sm text-muted-foreground">
              Select a table from the sidebar
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
