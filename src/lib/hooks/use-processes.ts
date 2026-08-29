'use client';

import { useQuery } from '@tanstack/react-query';
import type { ProcessInfo, ServiceStatusInfo } from '@/lib/services/monitoring';

interface ProcessesResponse {
  processes: ProcessInfo[];
  timestamp: number;
}

interface ServicesResponse {
  services: ServiceStatusInfo[];
  timestamp: number;
}

export function useProcesses() {
  const query = useQuery({
    queryKey: ['monitoring', 'processes'],
    queryFn: async (): Promise<ProcessesResponse> => {
      const res = await fetch('/api/monitoring/processes?limit=20', { cache: 'no-store' });
      if (!res.ok) throw new Error('Failed to fetch processes');
      return res.json() as Promise<ProcessesResponse>;
    },
    refetchInterval: 5000,
    staleTime: 4900,
    gcTime: 30_000,
    retry: 2,
    retryDelay: 1000,
  });

  return {
    processes: query.data?.processes ?? [],
    isLoading: query.isLoading,
    isError: query.isError,
    error: query.error,
    timestamp: query.data?.timestamp,
  };
}

export function useServices() {
  const query = useQuery({
    queryKey: ['monitoring', 'services'],
    queryFn: async (): Promise<ServicesResponse> => {
      const res = await fetch('/api/monitoring/services', { cache: 'no-store' });
      if (!res.ok) throw new Error('Failed to fetch services');
      return res.json() as Promise<ServicesResponse>;
    },
    refetchInterval: 10_000,
    staleTime: 9_000,
    gcTime: 30_000,
    retry: 2,
  });

  return {
    services: query.data?.services ?? [],
    isLoading: query.isLoading,
    isError: query.isError,
  };
}
