'use client';

import { cn } from '@/lib/utils';
import type { MemoryStats } from '@/lib/services/monitoring';

function formatBytes(bytes: number, decimals = 1): string {
  if (bytes === 0) return '0 B';
  const k = 1024;
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return `${parseFloat((bytes / Math.pow(k, i)).toFixed(decimals))} ${sizes[i]}`;
}

interface GaugeCircleProps {
  value: number;
  size?: number;
  strokeWidth?: number;
  color: string;
  label: string;
  sublabel?: string;
}

function GaugeCircle({ value, size = 110, strokeWidth = 10, color, label, sublabel }: GaugeCircleProps) {
  const radius = (size - strokeWidth) / 2;
  const circumference = 2 * Math.PI * radius;
  const arc = (270 / 360) * circumference;
  const offset = arc - (Math.max(0, Math.min(100, value)) / 100) * arc;
  const cx = size / 2;
  const cy = size / 2;

  return (
    <div className="flex flex-col items-center gap-1.5">
      <div className="relative" style={{ width: size, height: size }}>
        <svg width={size} height={size} className="rotate-[135deg]">
          <circle
            cx={cx}
            cy={cy}
            r={radius}
            fill="none"
            className="stroke-muted"
            strokeWidth={strokeWidth}
            strokeLinecap="round"
            strokeDasharray={`${arc} ${circumference}`}
          />
          <circle
            cx={cx}
            cy={cy}
            r={radius}
            fill="none"
            className={cn('transition-all duration-700', color)}
            strokeWidth={strokeWidth}
            strokeLinecap="round"
            strokeDasharray={`${arc} ${circumference}`}
            strokeDashoffset={offset}
          />
        </svg>
        <div className="absolute inset-0 flex flex-col items-center justify-center">
          <span className="text-xl font-bold tabular-nums text-foreground">{value}%</span>
        </div>
      </div>
      <div className="text-center">
        <p className="text-sm font-semibold text-foreground">{label}</p>
        {sublabel && <p className="text-[10px] text-muted-foreground">{sublabel}</p>}
      </div>
    </div>
  );
}

interface MemoryGaugeProps {
  stats: MemoryStats | null;
  className?: string;
}

export function MemoryGauge({ stats, className }: MemoryGaugeProps) {
  const ramPct = stats?.percentage ?? 0;
  const swapPct = stats?.swap.percentage ?? 0;

  const ramColor = ramPct >= 90 ? 'stroke-red-500' : ramPct >= 70 ? 'stroke-amber-500' : 'stroke-violet-500';
  const swapColor = swapPct >= 90 ? 'stroke-red-500' : swapPct >= 70 ? 'stroke-amber-500' : 'stroke-cyan-500';

  return (
    <div className={cn('rounded-xl border border-border bg-card p-5 shadow-sm', className)}>
      <div className="mb-3 flex items-center justify-between">
        <h3 className="text-sm font-semibold text-foreground">Memory Usage</h3>
        {stats && (
          <span className="text-xs text-muted-foreground">
            {formatBytes(stats.available)} available
          </span>
        )}
      </div>

      {/* Dual gauges */}
      <div className="flex justify-around mb-4">
        <GaugeCircle
          value={ramPct}
          color={ramColor}
          label="RAM"
          sublabel={stats ? `${formatBytes(stats.used)} / ${formatBytes(stats.total)}` : undefined}
        />
        <GaugeCircle
          value={swapPct}
          color={swapColor}
          label="Swap"
          sublabel={stats ? `${formatBytes(stats.swap.used)} / ${formatBytes(stats.swap.total)}` : undefined}
        />
      </div>

      {/* Breakdown */}
      {stats && (
        <div className="space-y-2">
          {/* Stacked bar */}
          <div className="h-2.5 w-full overflow-hidden rounded-full bg-muted flex">
            {/* App used */}
            <div
              className="h-full bg-violet-500 transition-all duration-700"
              style={{ width: `${((stats.used - stats.cached - stats.buffers) / stats.total) * 100}%` }}
            />
            {/* Cached */}
            <div
              className="h-full bg-violet-400/60 transition-all duration-700"
              style={{ width: `${(stats.cached / stats.total) * 100}%` }}
            />
            {/* Buffers */}
            <div
              className="h-full bg-violet-300/40 transition-all duration-700"
              style={{ width: `${(stats.buffers / stats.total) * 100}%` }}
            />
          </div>

          {/* Legend */}
          <div className="grid grid-cols-3 gap-1">
            {[
              { label: 'Used', bytes: stats.used - stats.cached - stats.buffers, color: 'bg-violet-500' },
              { label: 'Cached', bytes: stats.cached, color: 'bg-violet-400/60' },
              { label: 'Buffers', bytes: stats.buffers, color: 'bg-violet-300' },
            ].map((item) => (
              <div key={item.label} className="flex items-center gap-1 min-w-0">
                <span className={cn('size-2 rounded-full shrink-0', item.color)} />
                <span className="text-[10px] text-muted-foreground truncate">
                  {item.label}: {formatBytes(item.bytes)}
                </span>
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}
