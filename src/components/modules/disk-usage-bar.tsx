'use client';

import { cn } from '@/lib/utils';
import { HardDrive } from 'lucide-react';
import type { DiskMount } from '@/lib/services/monitoring';

function formatBytes(bytes: number): string {
  if (bytes === 0) return '0 B';
  const k = 1024;
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return `${parseFloat((bytes / Math.pow(k, i)).toFixed(1))} ${sizes[i]}`;
}

function getBarColor(pct: number): string {
  if (pct >= 90) return 'bg-red-500';
  if (pct >= 75) return 'bg-amber-500';
  if (pct >= 50) return 'bg-blue-500';
  return 'bg-emerald-500';
}

function getTextColor(pct: number): string {
  if (pct >= 90) return 'text-red-500';
  if (pct >= 75) return 'text-amber-500';
  return 'text-foreground';
}

interface DiskUsageBarProps {
  disks: DiskMount[];
  className?: string;
}

export function DiskUsageBar({ disks, className }: DiskUsageBarProps) {
  return (
    <div className={cn('rounded-xl border border-border bg-card shadow-sm', className)}>
      <div className="border-b border-border px-5 py-3.5">
        <h3 className="text-sm font-semibold text-foreground">Disk Partitions</h3>
      </div>
      <div className="divide-y divide-border">
        {disks.length === 0 ? (
          <div className="flex items-center justify-center py-8 text-sm text-muted-foreground">
            No disk data available
          </div>
        ) : (
          disks.map((disk) => (
            <div key={disk.mountPoint} className="px-5 py-3.5">
              <div className="flex items-start gap-3 mb-2">
                <HardDrive className="size-4 shrink-0 text-muted-foreground mt-0.5" />
                <div className="flex-1 min-w-0">
                  <div className="flex items-center justify-between gap-2">
                    <span className="text-sm font-medium text-foreground">{disk.mountPoint}</span>
                    <span className={cn('text-sm font-bold tabular-nums shrink-0', getTextColor(disk.percentage))}>
                      {disk.percentage}%
                    </span>
                  </div>
                  <div className="flex items-center justify-between gap-2 mt-0.5">
                    <span className="text-xs text-muted-foreground truncate">
                      {disk.device} · {disk.fsType}
                    </span>
                    <span className="text-xs text-muted-foreground shrink-0">
                      {formatBytes(disk.used)} / {formatBytes(disk.total)}
                    </span>
                  </div>
                </div>
              </div>
              <div className="ml-7">
                <div className="h-2 w-full overflow-hidden rounded-full bg-muted">
                  <div
                    className={cn('h-full rounded-full transition-all duration-700', getBarColor(disk.percentage))}
                    style={{ width: `${disk.percentage}%` }}
                  />
                </div>
                <div className="flex justify-between mt-0.5 text-[10px] text-muted-foreground">
                  <span>{formatBytes(disk.free)} free</span>
                  <span>Total: {formatBytes(disk.total)}</span>
                </div>
              </div>
            </div>
          ))
        )}
      </div>
    </div>
  );
}
