'use client';

import { cn } from '@/lib/utils';
import { Server, Code, Play, Database, Archive, Mail, RefreshCw } from 'lucide-react';
import { useServices } from '@/lib/hooks/use-processes';

const serviceIcons: Record<string, React.ElementType> = {
  nginx: Server,
  'php-fpm-8.4': Code,
  'php-fpm-8.5': Code,
  'php-fpm-7.4': Code,
  pm2: Play,
  postgresql: Database,
  redis: Archive,
  stalwart: Mail,
};

type ServiceStatus = 'running' | 'stopped' | 'error' | 'unknown';

function StatusDot({ status }: { status: ServiceStatus }) {
  return (
    <span
      className={cn(
        'inline-block size-2.5 rounded-full shrink-0',
        status === 'running' && 'bg-emerald-500 shadow-[0_0_8px_2px] shadow-emerald-500/50',
        status === 'stopped' && 'bg-zinc-500',
        status === 'error' && 'bg-red-500 shadow-[0_0_8px_2px] shadow-red-500/50',
        status === 'unknown' && 'bg-zinc-400'
      )}
    />
  );
}

function statusBadgeClass(status: ServiceStatus): string {
  return cn(
    'inline-flex items-center rounded-full px-2 py-0.5 text-[10px] font-medium capitalize',
    status === 'running' && 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-400',
    status === 'stopped' && 'bg-zinc-500/10 text-zinc-500',
    status === 'error' && 'bg-red-500/10 text-red-600 dark:text-red-400',
    status === 'unknown' && 'bg-zinc-500/10 text-zinc-400'
  );
}

interface ServiceStatusProps {
  className?: string;
}

export function ServiceStatus({ className }: ServiceStatusProps) {
  const { services, isLoading, isError } = useServices();

  const runningCount = services.filter((s) => s.status === 'running').length;

  return (
    <div className={cn('rounded-xl border border-border bg-card shadow-sm', className)}>
      <div className="flex items-center justify-between border-b border-border px-5 py-3.5">
        <h3 className="text-sm font-semibold text-foreground">Services</h3>
        <div className="flex items-center gap-2">
          {isLoading && <RefreshCw className="size-3.5 animate-spin text-muted-foreground" />}
          {!isLoading && !isError && services.length > 0 && (
            <span className="text-xs text-muted-foreground">
              <span className="text-emerald-500 font-medium">{runningCount}</span>
              <span>/{services.length}</span>
            </span>
          )}
        </div>
      </div>

      <div className="divide-y divide-border">
        {isError ? (
          <div className="flex items-center justify-center py-6 text-sm text-muted-foreground">
            Failed to load service status
          </div>
        ) : isLoading ? (
          Array.from({ length: 7 }).map((_, i) => (
            <div key={i} className="flex items-center gap-3 px-5 py-3 animate-pulse">
              <div className="size-8 rounded-md bg-muted" />
              <div className="flex-1 space-y-1.5">
                <div className="h-3 w-28 rounded bg-muted" />
                <div className="h-2.5 w-16 rounded bg-muted" />
              </div>
              <div className="h-5 w-16 rounded-full bg-muted" />
            </div>
          ))
        ) : (
          services.map((svc) => {
            const Icon = serviceIcons[svc.name] ?? Server;
            return (
              <div key={svc.name} className="flex items-center gap-3 px-5 py-3">
                <div className="flex size-8 shrink-0 items-center justify-center rounded-md bg-muted">
                  <Icon className="size-4 text-muted-foreground" />
                </div>
                <div className="flex-1 min-w-0">
                  <p className="text-sm font-medium text-foreground">{svc.displayName}</p>
                  {svc.pid && svc.pid > 0 && (
                    <p className="text-[10px] text-muted-foreground">PID: {svc.pid}</p>
                  )}
                </div>
                <div className="flex items-center gap-2">
                  <StatusDot status={svc.status} />
                  <span className={statusBadgeClass(svc.status)}>{svc.status}</span>
                </div>
              </div>
            );
          })
        )}
      </div>
    </div>
  );
}
