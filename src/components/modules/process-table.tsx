'use client';

import { useState } from 'react';
import { cn } from '@/lib/utils';
import { ArrowUp, ArrowDown, ArrowUpDown, RefreshCw } from 'lucide-react';
import { useProcesses } from '@/lib/hooks/use-processes';
import type { ProcessInfo } from '@/lib/services/monitoring';

type SortKey = 'cpu' | 'memory' | 'pid' | 'name';
type SortDir = 'asc' | 'desc';

function SortIcon({ active, dir }: { active: boolean; dir: SortDir }) {
  if (!active) return <ArrowUpDown className="size-3 text-muted-foreground/40" />;
  return dir === 'desc'
    ? <ArrowDown className="size-3 text-blue-500" />
    : <ArrowUp className="size-3 text-blue-500" />;
}

function cpuColor(cpu: number): string {
  if (cpu >= 80) return 'text-red-500';
  if (cpu >= 40) return 'text-amber-500';
  if (cpu >= 10) return 'text-blue-400';
  return 'text-muted-foreground';
}

function memColor(mem: number): string {
  if (mem >= 20) return 'text-red-500';
  if (mem >= 10) return 'text-amber-500';
  if (mem >= 3) return 'text-blue-400';
  return 'text-muted-foreground';
}

function statusInfo(s: string): { label: string; cls: string } {
  switch (s[0]) {
    case 'R': return { label: 'Run', cls: 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-400' };
    case 'S': return { label: 'Sleep', cls: 'bg-blue-500/10 text-blue-500' };
    case 'D': return { label: 'Wait', cls: 'bg-amber-500/10 text-amber-500' };
    case 'Z': return { label: 'Zombie', cls: 'bg-red-500/10 text-red-500' };
    case 'T': return { label: 'Stop', cls: 'bg-zinc-500/10 text-zinc-500' };
    default: return { label: s[0] ?? '?', cls: 'bg-zinc-500/10 text-zinc-400' };
  }
}

interface ProcessTableProps {
  className?: string;
}

export function ProcessTable({ className }: ProcessTableProps) {
  const { processes, isLoading, isError } = useProcesses();
  const [sortKey, setSortKey] = useState<SortKey>('cpu');
  const [sortDir, setSortDir] = useState<SortDir>('desc');

  const handleSort = (key: SortKey) => {
    if (sortKey === key) {
      setSortDir((d) => (d === 'desc' ? 'asc' : 'desc'));
    } else {
      setSortKey(key);
      setSortDir('desc');
    }
  };

  const sorted = [...processes].sort((a: ProcessInfo, b: ProcessInfo) => {
    const av = a[sortKey];
    const bv = b[sortKey];
    if (typeof av === 'string' && typeof bv === 'string') {
      return sortDir === 'desc' ? bv.localeCompare(av) : av.localeCompare(bv);
    }
    if (typeof av === 'number' && typeof bv === 'number') {
      return sortDir === 'desc' ? bv - av : av - bv;
    }
    return 0;
  });

  const Col = ({
    label,
    sortable,
    sortColKey,
    className: colClass,
  }: {
    label: string;
    sortable?: SortKey;
    sortColKey?: SortKey;
    className?: string;
  }) => (
    <th
      className={cn(
        'px-3 py-2.5 text-left text-xs font-medium text-muted-foreground',
        sortable && 'cursor-pointer hover:text-foreground select-none transition-colors',
        colClass
      )}
      onClick={sortable ? () => handleSort(sortable) : undefined}
    >
      <div className="flex items-center gap-1">
        {label}
        {sortable && (
          <SortIcon active={sortColKey === sortKey} dir={sortDir} />
        )}
      </div>
    </th>
  );

  return (
    <div className={cn('rounded-xl border border-border bg-card shadow-sm', className)}>
      <div className="flex items-center justify-between border-b border-border px-5 py-3.5">
        <h3 className="text-sm font-semibold text-foreground">Top Processes</h3>
        <div className="flex items-center gap-2">
          {isLoading && <RefreshCw className="size-3.5 animate-spin text-muted-foreground" />}
          <span className="text-xs text-muted-foreground">{processes.length} processes</span>
        </div>
      </div>

      <div className="overflow-x-auto">
        {isError ? (
          <div className="flex items-center justify-center py-8 text-sm text-muted-foreground">
            Failed to load process list
          </div>
        ) : (
          <table className="w-full text-sm">
            <thead className="border-b border-border bg-muted/30">
              <tr>
                <Col label="PID" sortable="pid" sortColKey="pid" />
                <Col label="Name" sortable="name" sortColKey="name" />
                <Col label="User" />
                <Col label="CPU%" sortable="cpu" sortColKey="cpu" />
                <Col label="MEM%" sortable="memory" sortColKey="memory" />
                <Col label="RSS" />
                <Col label="Status" />
                <Col label="Command" colClass="hidden lg:table-cell" />
              </tr>
            </thead>
            <tbody className="divide-y divide-border">
              {isLoading
                ? Array.from({ length: 12 }).map((_, i) => (
                    <tr key={i} className="animate-pulse">
                      {Array.from({ length: 7 }).map((__, j) => (
                        <td key={j} className="px-3 py-2.5">
                          <div className="h-3 rounded bg-muted w-full max-w-[80px]" />
                        </td>
                      ))}
                    </tr>
                  ))
                : sorted.slice(0, 15).map((proc) => {
                    const st = statusInfo(proc.status);
                    return (
                      <tr key={proc.pid} className="hover:bg-muted/30 transition-colors">
                        <td className="px-3 py-2.5 tabular-nums text-xs text-muted-foreground">
                          {proc.pid}
                        </td>
                        <td className="px-3 py-2.5 font-medium text-foreground">
                          <span className="block max-w-[120px] truncate">{proc.name}</span>
                        </td>
                        <td className="px-3 py-2.5 text-xs text-muted-foreground">
                          {proc.user.length > 12 ? proc.user.slice(0, 12) + '…' : proc.user}
                        </td>
                        <td className={cn('px-3 py-2.5 tabular-nums font-medium', cpuColor(proc.cpu))}>
                          {proc.cpu.toFixed(1)}%
                        </td>
                        <td className={cn('px-3 py-2.5 tabular-nums font-medium', memColor(proc.memory))}>
                          {proc.memory.toFixed(1)}%
                        </td>
                        <td className="px-3 py-2.5 tabular-nums text-xs text-muted-foreground">
                          {proc.memoryMb} MB
                        </td>
                        <td className="px-3 py-2.5">
                          <span className={cn('inline-flex items-center rounded-full px-1.5 py-0.5 text-[10px] font-medium', st.cls)}>
                            {st.label}
                          </span>
                        </td>
                        <td className="px-3 py-2.5 hidden lg:table-cell text-xs text-muted-foreground">
                          <span className="block max-w-[240px] truncate">{proc.command}</span>
                        </td>
                      </tr>
                    );
                  })}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}
