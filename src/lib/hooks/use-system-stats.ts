'use client';

import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useEffect, useRef, useState } from 'react';
import type { MonitoringStats, NetworkInterface } from '@/lib/services/monitoring';

const STATS_QUERY_KEY = ['monitoring', 'stats'];
const POLL_INTERVAL = 2000;
const MAX_HISTORY = 60;

export interface NetworkDelta {
  name: string;
  rxBytesPerSec: number;
  txBytesPerSec: number;
}

export interface StatsHistoryPoint {
  timestamp: number;
  label: string;
  cpu: number;
  memory: number;
  load1: number;
  load5: number;
  load15: number;
}

export interface NetworkHistoryPoint {
  timestamp: number;
  label: string;
  [key: string]: number | string;
}

async function fetchStats(): Promise<MonitoringStats> {
  const res = await fetch('/api/monitoring/stats', { cache: 'no-store' });
  if (!res.ok) throw new Error('Failed to fetch stats');
  return res.json() as Promise<MonitoringStats>;
}

function formatTime(ts: number): string {
  return new Date(ts).toLocaleTimeString('en-GB', {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  });
}

function calculateNetworkDeltas(
  current: NetworkInterface[],
  previous: NetworkInterface[] | null,
  elapsedSec: number
): NetworkDelta[] {
  if (!previous || elapsedSec <= 0) {
    return current.map((iface) => ({ name: iface.name, rxBytesPerSec: 0, txBytesPerSec: 0 }));
  }

  return current.map((curr) => {
    const prev = previous.find((p) => p.name === curr.name);
    if (!prev) return { name: curr.name, rxBytesPerSec: 0, txBytesPerSec: 0 };

    const rxDelta = Math.max(0, curr.bytesRecv - prev.bytesRecv);
    const txDelta = Math.max(0, curr.bytesSent - prev.bytesSent);

    return {
      name: curr.name,
      rxBytesPerSec: Math.round(rxDelta / elapsedSec),
      txBytesPerSec: Math.round(txDelta / elapsedSec),
    };
  });
}

export function useSystemStats() {
  const queryClient = useQueryClient();
  const prevStatsRef = useRef<MonitoringStats | null>(null);
  const prevTimestampRef = useRef<number>(0);

  const [history, setHistory] = useState<StatsHistoryPoint[]>([]);
  const [networkHistory, setNetworkHistory] = useState<NetworkHistoryPoint[]>([]);
  const [networkDeltas, setNetworkDeltas] = useState<NetworkDelta[]>([]);

  const query = useQuery({
    queryKey: STATS_QUERY_KEY,
    queryFn: fetchStats,
    refetchInterval: POLL_INTERVAL,
    staleTime: POLL_INTERVAL - 100,
    gcTime: 30_000,
    retry: 2,
    retryDelay: 1000,
  });

  useEffect(() => {
    if (!query.data) return;

    const stats = query.data;
    const now = stats.timestamp;
    const elapsedSec =
      prevTimestampRef.current > 0 ? (now - prevTimestampRef.current) / 1000 : 2;

    const deltas = calculateNetworkDeltas(
      stats.network,
      prevStatsRef.current?.network ?? null,
      elapsedSec
    );
    setNetworkDeltas(deltas);

    const label = formatTime(now);

    setHistory((prev) => {
      const point: StatsHistoryPoint = {
        timestamp: now,
        label,
        cpu: stats.cpu.usage,
        memory: stats.memory.percentage,
        load1: stats.loadAvg[0],
        load5: stats.loadAvg[1],
        load15: stats.loadAvg[2],
      };
      const next = [...prev, point];
      return next.length > MAX_HISTORY ? next.slice(next.length - MAX_HISTORY) : next;
    });

    setNetworkHistory((prev) => {
      const point: NetworkHistoryPoint = { timestamp: now, label };
      for (const delta of deltas) {
        point[`rx_${delta.name}`] = delta.rxBytesPerSec;
        point[`tx_${delta.name}`] = delta.txBytesPerSec;
      }
      const next = [...prev, point];
      return next.length > MAX_HISTORY ? next.slice(next.length - MAX_HISTORY) : next;
    });

    prevStatsRef.current = stats;
    prevTimestampRef.current = now;
  }, [query.data]);

  const refresh = () => {
    void queryClient.invalidateQueries({ queryKey: STATS_QUERY_KEY });
  };

  return {
    stats: query.data ?? null,
    isLoading: query.isLoading,
    isError: query.isError,
    error: query.error,
    history,
    networkHistory,
    networkDeltas,
    refresh,
  };
}
