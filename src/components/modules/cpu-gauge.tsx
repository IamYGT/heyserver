'use client';

import { cn } from '@/lib/utils';
import type { CpuStats } from '@/lib/services/monitoring';

function getUsageColor(usage: number): string {
  if (usage >= 90) return 'stroke-red-500';
  if (usage >= 70) return 'stroke-amber-500';
  return 'stroke-blue-500';
}

interface GaugeCircleProps {
  value: number;
  size?: number;
  strokeWidth?: number;
  color: string;
  centerText?: string;
}

function GaugeCircle({ value, size = 140, strokeWidth = 12, color, centerText }: GaugeCircleProps) {
  const radius = (size - strokeWidth) / 2;
  const circumference = 2 * Math.PI * radius;
  const arc = (270 / 360) * circumference;
  const offset = arc - (Math.max(0, Math.min(100, value)) / 100) * arc;
  const cx = size / 2;
  const cy = size / 2;

  return (
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
        <span className="text-2xl font-bold tabular-nums text-foreground">{value}%</span>
        {centerText && (
          <span className="text-[10px] text-muted-foreground mt-0.5">{centerText}</span>
        )}
      </div>
    </div>
  );
}

interface CpuGaugeProps {
  stats: CpuStats | null;
  className?: string;
}

export function CpuGauge({ stats, className }: CpuGaugeProps) {
  const usage = stats?.usage ?? 0;
  const color = getUsageColor(usage);

  return (
    <div className={cn('rounded-xl border border-border bg-card p-5 shadow-sm', className)}>
      <div className="mb-3 flex items-center justify-between">
        <h3 className="text-sm font-semibold text-foreground">CPU Usage</h3>
        {stats && (
          <span className="text-xs text-muted-foreground">
            {stats.count} cores · {stats.model.split('@')[0]?.trim()}
          </span>
        )}
      </div>

      <div className="flex items-center justify-center mb-4">
        <GaugeCircle
          value={usage}
          size={140}
          strokeWidth={12}
          color={color}
          centerText={stats?.freq ? `${stats.freq} MHz` : undefined}
        />
      </div>

      {/* Per-core mini gauges */}
      {stats && stats.cores.length > 0 && (
        <div className="grid grid-cols-4 gap-2">
          {stats.cores.map((core) => {
            const coreArc = (270 / 360) * 2 * Math.PI * 12;
            const coreOffset = coreArc - (Math.max(0, Math.min(100, core.usage)) / 100) * coreArc;
            const coreColor = getUsageColor(core.usage);

            return (
              <div key={core.core} className="flex flex-col items-center gap-0.5">
                <div className="relative" style={{ width: 36, height: 36 }}>
                  <svg width="36" height="36" className="rotate-[135deg]">
                    <circle
                      cx="18"
                      cy="18"
                      r="12"
                      fill="none"
                      className="stroke-muted"
                      strokeWidth="4"
                      strokeLinecap="round"
                      strokeDasharray={`${coreArc} ${2 * Math.PI * 12}`}
                    />
                    <circle
                      cx="18"
                      cy="18"
                      r="12"
                      fill="none"
                      className={cn('transition-all duration-700', coreColor)}
                      strokeWidth="4"
                      strokeLinecap="round"
                      strokeDasharray={`${coreArc} ${2 * Math.PI * 12}`}
                      strokeDashoffset={coreOffset}
                    />
                  </svg>
                  <div className="absolute inset-0 flex items-center justify-center">
                    <span className="text-[9px] font-bold tabular-nums text-foreground">
                      {core.usage}
                    </span>
                  </div>
                </div>
                <span className="text-[9px] text-muted-foreground">C{core.core}</span>
              </div>
            );
          })}
        </div>
      )}

      {/* Usage bar */}
      <div className="mt-3">
        <div className="flex items-center justify-between text-xs text-muted-foreground mb-1">
          <span>Total</span>
          <span className={cn(
            'font-medium',
            usage >= 90 ? 'text-red-500' : usage >= 70 ? 'text-amber-500' : 'text-foreground'
          )}>
            {usage}%
          </span>
        </div>
        <div className="h-1.5 w-full overflow-hidden rounded-full bg-muted">
          <div
            className={cn(
              'h-full rounded-full transition-all duration-700',
              usage >= 90 ? 'bg-red-500' : usage >= 70 ? 'bg-amber-500' : 'bg-blue-500'
            )}
            style={{ width: `${usage}%` }}
          />
        </div>
      </div>
    </div>
  );
}
