'use client';

import { useMemo } from 'react';
import {
  Activity,
  RefreshCw,
  Clock,
  Cpu,
  Server,
  Wifi,
} from 'lucide-react';
import { CpuGauge } from '@/components/modules/monitoring/cpu-gauge';
import { MemoryGauge } from '@/components/modules/monitoring/memory-gauge';
import { DiskUsageBar } from '@/components/modules/monitoring/disk-usage-bar';
import { NetworkChart } from '@/components/modules/monitoring/network-chart';
import { LoadChart } from '@/components/modules/monitoring/load-chart';
import { ServiceStatus } from '@/components/modules/monitoring/service-status';
import { ProcessTable } from '@/components/modules/monitoring/process-table';
import { useSystemStats } from '@/lib/hooks/use-system-stats';

function formatUptime(seconds: number): string {
  const days = Math.floor(seconds / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  if (days > 0) return `${days}d ${hours}h ${minutes}m`;
  if (hours > 0) return `${hours}h ${minutes}m`;
  return `${minutes}m`;
}

function UptimeBadge({ seconds }: { seconds: number }) {
  return (
    <div className="flex items-center gap-1.5 rounded-full border border-border bg-card px-3 py-1 text-xs">
      <Clock className="size-3 text-muted-foreground" />
      <span className="text-muted-foreground">Uptime:</span>
      <span className="font-medium text-foreground">{formatUptime(seconds)}</span>
    </div>
  );
}

function LoadBadge({ load }: { load: [number, number, number] }) {
  const color = load[0] > 4 ? 'text-red-500' : load[0] > 2 ? 'text-amber-500' : 'text-emerald-500';
  return (
    <div className="flex items-center gap-1.5 rounded-full border border-border bg-card px-3 py-1 text-xs">
      <Activity className="size-3 text-muted-foreground" />
      <span className="text-muted-foreground">Load:</span>
      <span className={`font-medium tabular-nums ${color}`}>
        {load[0].toFixed(2)} · {load[1].toFixed(2)} · {load[2].toFixed(2)}
      </span>
    </div>
  );
}

export default function MonitoringPage() {
  const {
    stats,
    isLoading,
    isError,
    history,
    networkHistory,
    networkDeltas,
    refresh,
  } = useSystemStats();

  const loadAvg = useMemo(
    () => (stats?.loadAvg ?? [0, 0, 0]) as [number, number, number],
    [stats?.loadAvg]
  );

  return (
    <div className="space-y-5">
      {/* Page header */}
      <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div>
          <h1 className="text-2xl font-bold tracking-tight text-foreground flex items-center gap-2">
            <Activity className="size-6 text-blue-500" />
            System Monitoring
          </h1>
          <p className="text-sm text-muted-foreground mt-0.5">
            Real-time metrics · updates every 2 seconds
          </p>
        </div>

        {/* Status badges */}
        <div className="flex flex-wrap items-center gap-2">
          {stats && <UptimeBadge seconds={stats.uptime} />}
          {stats && <LoadBadge load={loadAvg} />}
          {stats && (
            <div className="flex items-center gap-1.5 rounded-full border border-border bg-card px-3 py-1 text-xs">
              <Server className="size-3 text-muted-foreground" />
              <span className="font-medium text-foreground">{stats.hostname}</span>
            </div>
          )}
          {stats && (
            <div className="flex items-center gap-1.5 rounded-full border border-border bg-card px-3 py-1 text-xs">
              <Wifi className="size-3 text-muted-foreground" />
              <span className="text-muted-foreground">{stats.osInfo}</span>
            </div>
          )}
          <button
            onClick={refresh}
            className="flex items-center gap-1.5 rounded-full border border-border bg-card px-3 py-1 text-xs text-muted-foreground hover:bg-muted hover:text-foreground transition-colors"
          >
            <RefreshCw className="size-3" />
            Refresh
          </button>
        </div>
      </div>

      {/* Error state */}
      {isError && (
        <div className="rounded-lg border border-red-500/20 bg-red-500/10 px-4 py-3 text-sm text-red-600 dark:text-red-400">
          Failed to load system stats. Retrying...
        </div>
      )}

      {/* Loading skeleton */}
      {isLoading && !stats && (
        <div className="grid grid-cols-1 gap-5 lg:grid-cols-2">
          {Array.from({ length: 4 }).map((_, i) => (
            <div key={i} className="h-64 animate-pulse rounded-xl border border-border bg-card" />
          ))}
        </div>
      )}

      {/* Main content */}
      {(stats || !isLoading) && (
        <>
          {/* Row 1: CPU + Memory gauges */}
          <div className="grid grid-cols-1 gap-5 lg:grid-cols-2">
            <CpuGauge stats={stats?.cpu ?? null} />
            <MemoryGauge stats={stats?.memory ?? null} />
          </div>

          {/* Row 2: Load/CPU chart + Network chart */}
          <div className="grid grid-cols-1 gap-5 xl:grid-cols-2">
            <LoadChart
              history={history}
              cpuCores={stats?.cpu.count}
            />
            <NetworkChart
              history={networkHistory}
              deltas={networkDeltas}
            />
          </div>

          {/* Row 3: Services + Disk */}
          <div className="grid grid-cols-1 gap-5 lg:grid-cols-2">
            <ServiceStatus />
            <DiskUsageBar disks={stats?.disks ?? []} />
          </div>

          {/* Row 4: Process table (full width) */}
          <ProcessTable />

          {/* Row 5: Disk I/O info */}
          {stats?.diskIO && stats.diskIO.length > 0 && (
            <div className="rounded-xl border border-border bg-card shadow-sm">
              <div className="border-b border-border px-5 py-3.5">
                <h3 className="text-sm font-semibold text-foreground flex items-center gap-2">
                  <Cpu className="size-4 text-muted-foreground" />
                  Disk I/O Counters
                </h3>
              </div>
              <div className="overflow-x-auto">
                <table className="w-full text-sm">
                  <thead className="border-b border-border bg-muted/30">
                    <tr>
                      <th className="px-4 py-2 text-left text-xs font-medium text-muted-foreground">Device</th>
                      <th className="px-4 py-2 text-right text-xs font-medium text-muted-foreground">Reads</th>
                      <th className="px-4 py-2 text-right text-xs font-medium text-muted-foreground">Writes</th>
                      <th className="px-4 py-2 text-right text-xs font-medium text-muted-foreground">Read Total</th>
                      <th className="px-4 py-2 text-right text-xs font-medium text-muted-foreground">Write Total</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-border">
                    {stats.diskIO.map((d) => (
                      <tr key={d.device} className="hover:bg-muted/30 transition-colors">
                        <td className="px-4 py-2.5 font-medium text-foreground">{d.device}</td>
                        <td className="px-4 py-2.5 text-right tabular-nums text-muted-foreground">
                          {d.readsCompleted.toLocaleString()}
                        </td>
                        <td className="px-4 py-2.5 text-right tabular-nums text-muted-foreground">
                          {d.writesCompleted.toLocaleString()}
                        </td>
                        <td className="px-4 py-2.5 text-right tabular-nums text-muted-foreground">
                          {(d.readBytes / 1024 / 1024 / 1024).toFixed(2)} GB
                        </td>
                        <td className="px-4 py-2.5 text-right tabular-nums text-muted-foreground">
                          {(d.writeBytes / 1024 / 1024 / 1024).toFixed(2)} GB
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>
          )}
        </>
      )}
    </div>
  );
}
