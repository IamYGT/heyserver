'use client';

import {
  AreaChart,
  Area,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  ResponsiveContainer,
  Legend,
  ReferenceLine,
} from 'recharts';
import { cn } from '@/lib/utils';
import type { StatsHistoryPoint } from '@/lib/hooks/use-system-stats';

interface TooltipProps {
  active?: boolean;
  payload?: Array<{ name: string; value: number; color: string }>;
  label?: string;
}

function CustomTooltip({ active, payload, label }: TooltipProps) {
  if (!active || !payload?.length) return null;

  return (
    <div className="rounded-lg border border-border bg-card px-3 py-2 shadow-lg text-xs">
      <p className="text-muted-foreground mb-1.5">{label}</p>
      {payload.map((entry) => (
        <div key={entry.name} className="flex items-center gap-2 mb-0.5">
          <span className="size-2 rounded-full shrink-0" style={{ backgroundColor: entry.color }} />
          <span className="text-muted-foreground">{entry.name}:</span>
          <span className="font-medium text-foreground">
            {entry.name.includes('%') ? `${entry.value}%` : entry.value.toFixed(2)}
          </span>
        </div>
      ))}
    </div>
  );
}

interface LoadChartProps {
  history: StatsHistoryPoint[];
  cpuCores?: number;
  className?: string;
}

export function LoadChart({ history, cpuCores, className }: LoadChartProps) {
  const latest = history[history.length - 1];

  return (
    <div className={cn('rounded-xl border border-border bg-card p-5 shadow-sm', className)}>
      <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between mb-4">
        <h3 className="text-sm font-semibold text-foreground">Load Average &amp; CPU</h3>
        {latest && (
          <div className="flex items-center gap-3 text-xs flex-wrap">
            {[
              { label: '1m', value: latest.load1, color: '#3b82f6' },
              { label: '5m', value: latest.load5, color: '#8b5cf6' },
              { label: '15m', value: latest.load15, color: '#06b6d4' },
            ].map((item) => (
              <div key={item.label} className="flex items-center gap-1">
                <span className="size-2 rounded-full" style={{ backgroundColor: item.color }} />
                <span className="text-muted-foreground">{item.label}:</span>
                <span className="font-medium tabular-nums text-foreground">
                  {item.value.toFixed(2)}
                </span>
              </div>
            ))}
          </div>
        )}
      </div>

      <ResponsiveContainer width="100%" height={220}>
        <AreaChart data={history} margin={{ top: 4, right: 4, left: 0, bottom: 0 }}>
          <defs>
            <linearGradient id="cpuAreaGrad" x1="0" y1="0" x2="0" y2="1">
              <stop offset="5%" stopColor="#3b82f6" stopOpacity={0.25} />
              <stop offset="95%" stopColor="#3b82f6" stopOpacity={0} />
            </linearGradient>
            <linearGradient id="memAreaGrad" x1="0" y1="0" x2="0" y2="1">
              <stop offset="5%" stopColor="#10b981" stopOpacity={0.25} />
              <stop offset="95%" stopColor="#10b981" stopOpacity={0} />
            </linearGradient>
            <linearGradient id="loadAreaGrad" x1="0" y1="0" x2="0" y2="1">
              <stop offset="5%" stopColor="#8b5cf6" stopOpacity={0.25} />
              <stop offset="95%" stopColor="#8b5cf6" stopOpacity={0} />
            </linearGradient>
          </defs>
          <CartesianGrid strokeDasharray="3 3" className="stroke-border" opacity={0.4} />
          <XAxis
            dataKey="label"
            tick={{ fontSize: 10, fill: 'hsl(var(--muted-foreground))' }}
            tickLine={false}
            axisLine={false}
            interval="preserveStartEnd"
          />
          <YAxis
            tick={{ fontSize: 10, fill: 'hsl(var(--muted-foreground))' }}
            tickLine={false}
            axisLine={false}
            domain={[0, 'auto']}
            width={35}
          />
          <Tooltip content={<CustomTooltip />} />
          <Legend
            iconType="circle"
            iconSize={8}
            wrapperStyle={{ fontSize: '11px', paddingTop: '8px' }}
          />
          {cpuCores && cpuCores > 0 && (
            <ReferenceLine
              y={cpuCores}
              stroke="#ef4444"
              strokeDasharray="4 4"
              strokeOpacity={0.4}
              label={{ value: `${cpuCores}c`, fill: '#ef4444', fontSize: 9, position: 'right' }}
            />
          )}
          <Area
            type="monotone"
            dataKey="cpu"
            name="CPU %"
            stroke="#3b82f6"
            fill="url(#cpuAreaGrad)"
            strokeWidth={1.5}
            dot={false}
            isAnimationActive={false}
          />
          <Area
            type="monotone"
            dataKey="memory"
            name="RAM %"
            stroke="#10b981"
            fill="url(#memAreaGrad)"
            strokeWidth={1.5}
            dot={false}
            isAnimationActive={false}
          />
          <Area
            type="monotone"
            dataKey="load1"
            name="Load 1m"
            stroke="#8b5cf6"
            fill="url(#loadAreaGrad)"
            strokeWidth={1.5}
            dot={false}
            isAnimationActive={false}
          />
        </AreaChart>
      </ResponsiveContainer>
    </div>
  );
}
