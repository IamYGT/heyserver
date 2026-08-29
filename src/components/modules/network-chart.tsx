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
} from 'recharts';
import { cn } from '@/lib/utils';
import type { NetworkHistoryPoint, NetworkDelta } from '@/lib/hooks/use-system-stats';

function formatBytes(bytes: number): string {
  if (bytes === 0) return '0 B';
  const k = 1024;
  const sizes = ['B', 'KB', 'MB', 'GB'];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return `${parseFloat((bytes / Math.pow(k, i)).toFixed(1))} ${sizes[i]}`;
}

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
          <span className="font-medium text-foreground">{formatBytes(entry.value)}/s</span>
        </div>
      ))}
    </div>
  );
}

const IFACE_COLORS = [
  { rx: '#3b82f6', tx: '#10b981' },
  { rx: '#f59e0b', tx: '#ef4444' },
  { rx: '#8b5cf6', tx: '#ec4899' },
];

interface NetworkChartProps {
  history: NetworkHistoryPoint[];
  deltas: NetworkDelta[];
  className?: string;
}

export function NetworkChart({ history, deltas, className }: NetworkChartProps) {
  const activeInterfaces = deltas
    .filter((d) =>
      history.some(
        (p) => (p[`rx_${d.name}`] as number) > 0 || (p[`tx_${d.name}`] as number) > 0
      )
    )
    .slice(0, 3);

  const displayInterfaces = activeInterfaces.length > 0 ? activeInterfaces : deltas.slice(0, 2);

  return (
    <div className={cn('rounded-xl border border-border bg-card p-5 shadow-sm', className)}>
      <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between mb-4">
        <h3 className="text-sm font-semibold text-foreground">Network I/O</h3>
        <div className="flex flex-wrap gap-3">
          {deltas.slice(0, 3).map((delta) => (
            <div key={delta.name} className="text-right">
              <p className="text-xs font-medium text-foreground">{delta.name}</p>
              <p className="text-[10px] text-muted-foreground">
                ↓ {formatBytes(delta.rxBytesPerSec)}/s · ↑ {formatBytes(delta.txBytesPerSec)}/s
              </p>
            </div>
          ))}
        </div>
      </div>

      <ResponsiveContainer width="100%" height={200}>
        <AreaChart data={history} margin={{ top: 4, right: 4, left: 0, bottom: 0 }}>
          <defs>
            {displayInterfaces.map((delta, idx) => {
              const colors = IFACE_COLORS[idx] ?? IFACE_COLORS[0];
              return (
                <g key={delta.name}>
                  <linearGradient id={`rxGrad${idx}`} x1="0" y1="0" x2="0" y2="1">
                    <stop offset="5%" stopColor={colors.rx} stopOpacity={0.3} />
                    <stop offset="95%" stopColor={colors.rx} stopOpacity={0} />
                  </linearGradient>
                  <linearGradient id={`txGrad${idx}`} x1="0" y1="0" x2="0" y2="1">
                    <stop offset="5%" stopColor={colors.tx} stopOpacity={0.3} />
                    <stop offset="95%" stopColor={colors.tx} stopOpacity={0} />
                  </linearGradient>
                </g>
              );
            })}
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
            tickFormatter={(v: number) => formatBytes(v)}
            width={62}
          />
          <Tooltip content={<CustomTooltip />} />
          <Legend
            iconType="circle"
            iconSize={8}
            wrapperStyle={{ fontSize: '11px', paddingTop: '8px' }}
          />
          {displayInterfaces.flatMap((delta, idx) => {
            const colors = IFACE_COLORS[idx] ?? IFACE_COLORS[0];
            return [
              <Area
                key={`rx_${delta.name}`}
                type="monotone"
                dataKey={`rx_${delta.name}`}
                name={`↓ ${delta.name}`}
                stroke={colors.rx}
                fill={`url(#rxGrad${idx})`}
                strokeWidth={1.5}
                dot={false}
                isAnimationActive={false}
              />,
              <Area
                key={`tx_${delta.name}`}
                type="monotone"
                dataKey={`tx_${delta.name}`}
                name={`↑ ${delta.name}`}
                stroke={colors.tx}
                fill={`url(#txGrad${idx})`}
                strokeWidth={1.5}
                dot={false}
                isAnimationActive={false}
              />,
            ];
          })}
        </AreaChart>
      </ResponsiveContainer>
    </div>
  );
}
