'use client';

import { useEffect, useRef, useState, useCallback } from 'react';
import dynamic from 'next/dynamic';
import { Play, Clock, AlertCircle, Unlock, Lock, History, X, ChevronUp, ChevronDown } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
  SheetTrigger,
} from '@/components/ui/sheet';
import { ScrollArea } from '@/components/ui/scroll-area';
import { cn } from '@/lib/utils';

const MonacoEditor = dynamic(() => import('@monaco-editor/react'), { ssr: false });

interface QueryResult {
  columns: string[];
  rows: Record<string, string | number | boolean | null>[];
  rowCount: number;
  executionTime: number;
}

interface QueryHistoryEntry {
  query: string;
  executedAt: string;
  success: boolean;
  rowCount?: number;
  error?: string;
  executionTime?: number;
}

interface SqlEditorProps {
  engine: 'postgresql' | 'mariadb';
  database: string;
  onExecute: (
    query: string,
    readOnly: boolean
  ) => Promise<{ success: boolean; data?: QueryResult; error?: string }>;
  defaultQuery?: string;
  className?: string;
}

const HISTORY_KEY = 'hserver_sql_history';
const MAX_HISTORY = 20;

function loadHistory(): QueryHistoryEntry[] {
  if (typeof window === 'undefined') return [];
  try { return JSON.parse(localStorage.getItem(HISTORY_KEY) ?? '[]'); }
  catch { return []; }
}

function saveHistory(entries: QueryHistoryEntry[]): void {
  if (typeof window === 'undefined') return;
  localStorage.setItem(HISTORY_KEY, JSON.stringify(entries.slice(0, MAX_HISTORY)));
}

export function SqlEditor({ engine, database, onExecute, defaultQuery = 'SELECT 1;', className }: SqlEditorProps) {
  const [query, setQuery] = useState(defaultQuery);
  const [result, setResult] = useState<QueryResult | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [isRunning, setIsRunning] = useState(false);
  const [readOnly, setReadOnly] = useState(true);
  const [history, setHistory] = useState<QueryHistoryEntry[]>([]);
  const [sortCol, setSortCol] = useState<string | null>(null);
  const [sortDir, setSortDir] = useState<'asc' | 'desc'>('asc');
  const editorRef = useRef<unknown>(null);

  useEffect(() => { setHistory(loadHistory()); }, []);

  const execute = useCallback(async () => {
    const q = query.trim();
    if (!q) return;
    setIsRunning(true);
    setError(null);

    const res = await onExecute(q, readOnly);

    const entry: QueryHistoryEntry = {
      query: q,
      executedAt: new Date().toISOString(),
      success: res.success,
      rowCount: res.data?.rowCount,
      error: res.error,
      executionTime: res.data?.executionTime,
    };
    const newHistory = [entry, ...history].slice(0, MAX_HISTORY);
    setHistory(newHistory);
    saveHistory(newHistory);

    if (res.success && res.data) { setResult(res.data); setSortCol(null); }
    else { setError(res.error ?? 'Unknown error'); setResult(null); }
    setIsRunning(false);
  }, [query, readOnly, onExecute, history]);

  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') execute();
    };
    window.addEventListener('keydown', handler);
    return () => window.removeEventListener('keydown', handler);
  }, [execute]);

  const handleSort = (col: string) => {
    if (sortCol === col) setSortDir(d => d === 'asc' ? 'desc' : 'asc');
    else { setSortCol(col); setSortDir('asc'); }
  };

  const sortedRows = result
    ? [...result.rows].sort((a, b) => {
        if (!sortCol) return 0;
        const av = String(a[sortCol] ?? ''), bv = String(b[sortCol] ?? '');
        return sortDir === 'asc' ? av.localeCompare(bv) : bv.localeCompare(av);
      })
    : [];

  return (
    <div className={cn('flex flex-col gap-4', className)}>
      {/* Toolbar */}
      <div className="flex items-center justify-between gap-2 flex-wrap">
        <div className="flex items-center gap-2">
          <Badge variant="outline" className="text-xs">
            {engine === 'postgresql' ? 'PostgreSQL' : 'MariaDB'}
          </Badge>
          <Badge variant="secondary" className="text-xs font-mono">{database}</Badge>
        </div>
        <div className="flex items-center gap-2">
          <Button
            variant={readOnly ? 'outline' : 'destructive'}
            size="sm"
            onClick={() => setReadOnly(v => !v)}
          >
            {readOnly
              ? <><Lock className="size-3.5 mr-1" />Read-only</>
              : <><Unlock className="size-3.5 mr-1" />Write mode</>
            }
          </Button>

          <Sheet>
            <SheetTrigger asChild>
              <Button variant="outline" size="sm">
                <History className="size-3.5 mr-1" />History
                {history.length > 0 && (
                  <Badge variant="secondary" className="ml-1 text-xs px-1 py-0">{history.length}</Badge>
                )}
              </Button>
            </SheetTrigger>
            <SheetContent side="right" className="w-[480px]">
              <SheetHeader>
                <SheetTitle>Query History</SheetTitle>
              </SheetHeader>
              <ScrollArea className="h-full mt-4 pr-4">
                <div className="flex flex-col gap-3">
                  {history.length === 0 && (
                    <p className="text-sm text-muted-foreground">No queries yet.</p>
                  )}
                  {history.map((entry, i) => (
                    <div
                      key={i}
                      className="rounded-md border p-3 cursor-pointer hover:bg-accent transition-colors"
                      onClick={() => setQuery(entry.query)}
                    >
                      <div className="flex items-center justify-between mb-1">
                        <Badge variant={entry.success ? 'default' : 'destructive'} className="text-xs">
                          {entry.success ? 'OK' : 'Error'}
                        </Badge>
                        <span className="text-xs text-muted-foreground">
                          {new Date(entry.executedAt).toLocaleTimeString()}
                        </span>
                      </div>
                      <pre className="text-xs font-mono text-foreground whitespace-pre-wrap break-all line-clamp-3">
                        {entry.query}
                      </pre>
                      {entry.success && entry.rowCount !== undefined && (
                        <p className="text-xs text-muted-foreground mt-1">
                          {entry.rowCount} rows · {entry.executionTime}ms
                        </p>
                      )}
                      {!entry.success && (
                        <p className="text-xs text-destructive mt-1 line-clamp-1">{entry.error}</p>
                      )}
                    </div>
                  ))}
                </div>
              </ScrollArea>
            </SheetContent>
          </Sheet>

          <Button size="sm" onClick={execute} disabled={isRunning}>
            <Play className="size-3.5 mr-1" />
            {isRunning ? 'Running...' : 'Run'}
            <span className="ml-1 text-xs text-primary-foreground/60 hidden sm:inline">(Ctrl+↵)</span>
          </Button>
        </div>
      </div>

      {/* Monaco Editor */}
      <div className="h-48 rounded-md border overflow-hidden">
        <MonacoEditor
          height="100%"
          language={engine === 'postgresql' ? 'pgsql' : 'sql'}
          theme="vs-dark"
          value={query}
          onChange={v => setQuery(v ?? '')}
          onMount={editor => { editorRef.current = editor; }}
          options={{
            minimap: { enabled: false },
            fontSize: 13,
            fontFamily: 'var(--font-geist-mono, monospace)',
            lineNumbers: 'on',
            scrollBeyondLastLine: false,
            wordWrap: 'on',
            padding: { top: 8, bottom: 8 },
          }}
        />
      </div>

      {/* Error */}
      {error && (
        <div className="flex items-start gap-2 rounded-md border border-destructive/50 bg-destructive/10 p-3 text-sm text-destructive">
          <AlertCircle className="size-4 mt-0.5 shrink-0" />
          <pre className="whitespace-pre-wrap break-all font-mono text-xs flex-1">{error}</pre>
          <button onClick={() => setError(null)} className="shrink-0 hover:opacity-70">
            <X className="size-4" />
          </button>
        </div>
      )}

      {/* Results */}
      {result && (
        <div className="flex flex-col gap-2">
          <div className="flex items-center gap-3 text-xs text-muted-foreground">
            <span className="flex items-center gap-1"><Clock className="size-3" />{result.executionTime}ms</span>
            <span>{result.rowCount} rows</span>
            <span>{result.columns.length} columns</span>
          </div>

          {result.rows.length > 0 ? (
            <ScrollArea className="h-80 rounded-md border">
              <Table>
                <TableHeader className="sticky top-0 bg-muted/80 backdrop-blur-sm z-10">
                  <TableRow>
                    <TableHead className="w-10 text-center text-xs text-muted-foreground">#</TableHead>
                    {result.columns.map(col => (
                      <TableHead
                        key={col}
                        className="cursor-pointer hover:text-foreground text-xs select-none"
                        onClick={() => handleSort(col)}
                      >
                        <span className="flex items-center gap-1">
                          {col}
                          {sortCol === col && (
                            sortDir === 'asc'
                              ? <ChevronUp className="size-3" />
                              : <ChevronDown className="size-3" />
                          )}
                        </span>
                      </TableHead>
                    ))}
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {sortedRows.map((row, rowIdx) => (
                    <TableRow key={rowIdx} className="hover:bg-muted/30">
                      <TableCell className="text-center text-xs text-muted-foreground">{rowIdx + 1}</TableCell>
                      {result.columns.map(col => (
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
          ) : (
            <div className="flex items-center justify-center h-20 rounded-md border text-sm text-muted-foreground">
              Query executed successfully. No rows returned.
            </div>
          )}
        </div>
      )}
    </div>
  );
}
