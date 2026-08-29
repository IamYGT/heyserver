import { useState, useEffect, useRef } from 'react'
import { useQuery } from '@tanstack/react-query'
import { RefreshCw, ChevronDown, ChevronRight, AlertCircle, Clock, FileText } from 'lucide-react'
import { Card, CardContent } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { api } from '@/lib/api'
import { cn } from '@/lib/utils'
import type { PHPLogEntry, PHPSlowLogEntry, PHPPool } from '@/lib/types'

// ─── Error Log ────────────────────────────────────────────────────────────────

const LOG_LEVEL_STYLES: Record<string, string> = {
  'PHP Fatal error':   'text-red-400',
  'PHP Error':         'text-red-400',
  'PHP Warning':       'text-amber-400',
  'PHP Notice':        'text-blue-400',
  'PHP Deprecated':    'text-zinc-500',
  'PHP Parse error':   'text-red-400',
}

function LogLevelBadge({ level }: { level: string }) {
  const safeLevel = level ?? 'Unknown'
  const color = LOG_LEVEL_STYLES[safeLevel] ?? 'text-zinc-400'
  return <span className={cn('text-[10px] font-medium uppercase', color)}>{safeLevel.replace('PHP ', '')}</span>
}

function LogLoadError({ title, error, retry, retrying }: { title: string; error: Error; retry: () => void; retrying: boolean }) {
  return (
    <Card className="border-red-500/25 bg-red-500/[0.05]">
      <CardContent className="p-6 text-center">
        <AlertCircle className="mx-auto size-5 text-red-400" />
        <p className="mt-2 text-sm text-red-300">{title}</p>
        <p className="mt-1 text-xs text-zinc-600">{error.message}</p>
        <Button type="button" variant="outline" size="sm" className="mt-4 border-red-500/30 text-red-200" onClick={retry} disabled={retrying}>
          <RefreshCw className={cn('mr-2 size-3.5', retrying && 'animate-spin')} />Retry
        </Button>
      </CardContent>
    </Card>
  )
}

function ErrorLog({ version }: { version: string }) {
  const scrollRef = useRef<HTMLDivElement>(null)
  const [autoScroll, setAutoScroll] = useState(true)
  const [linesCount, setLinesCount] = useState(100)

  const { data: entries = [], isLoading, isError, error, refetch, isFetching } = useQuery<PHPLogEntry[]>({
    queryKey: ['php', 'logs', 'error', version, linesCount],
    queryFn: async () => await api.get<PHPLogEntry[]>(`/php/logs/${version}/error?lines=${linesCount}`) ?? [],
    refetchInterval: 30_000,
  })

  useEffect(() => {
    if (autoScroll && scrollRef.current) {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight
    }
  }, [entries, autoScroll])

  if (isLoading) {
    return <Skeleton className="h-64 bg-zinc-800 rounded-xl" />
  }
  if (isError) return <LogLoadError title="PHP error log could not be loaded." error={error} retry={() => { void refetch() }} retrying={isFetching} />

  return (
    <div className="space-y-2">
      <div className="flex items-center gap-3">
        <div className="flex items-center gap-1.5 border border-zinc-700 rounded-md overflow-hidden">
          {([50, 100, 200, 500] as const).map(n => (
            <button
              key={n}
              onClick={() => setLinesCount(n)}
              className={cn(
                'px-2.5 py-1 text-xs transition-colors',
                linesCount === n ? 'bg-zinc-700 text-white' : 'text-zinc-400 hover:text-zinc-200'
              )}
            >
              {n}
            </button>
          ))}
        </div>
        <button
          onClick={() => setAutoScroll(v => !v)}
          className={cn(
            'flex items-center gap-1.5 px-2.5 py-1 rounded-md border text-xs transition-colors',
            autoScroll ? 'border-blue-500 bg-blue-500/10 text-blue-300' : 'border-zinc-700 text-zinc-400'
          )}
        >
          Auto-scroll
        </button>
        <Button
          variant="ghost"
          size="icon"
          className="h-7 w-7 text-zinc-400 hover:text-white"
          onClick={() => refetch()}
          disabled={isFetching}
        >
          <RefreshCw className={cn('w-3.5 h-3.5', isFetching && 'animate-spin')} />
        </Button>
        <span className="text-zinc-600 text-xs ml-auto">{entries.length} entries</span>
      </div>

      <div
        ref={scrollRef}
        className="bg-zinc-950 border border-zinc-800 rounded-xl font-mono text-xs overflow-y-auto h-[480px] p-3 space-y-0.5"
      >
        {entries.length === 0 ? (
          <div className="flex flex-col items-center justify-center h-full text-zinc-600">
            <FileText className="w-8 h-8 mb-2 opacity-40" />
            <p>No error log entries</p>
          </div>
        ) : (
          entries.map((entry, i) => (
            <div key={i} className="flex gap-3 py-0.5 hover:bg-zinc-900/50 rounded px-1 transition-colors">
              <span className="text-zinc-600 flex-shrink-0 w-36">
                {new Date(entry.timestamp).toLocaleTimeString('en-GB', { hour12: false })}
              </span>
              <LogLevelBadge level={entry.level} />
              <span className="text-zinc-300 break-all leading-relaxed">{entry.message}</span>
              {entry.file && (
                <span className="text-zinc-600 flex-shrink-0 truncate max-w-40" title={`${entry.file}:${entry.line}`}>
                  {entry.file.split('/').pop()}:{entry.line}
                </span>
              )}
            </div>
          ))
        )}
      </div>
    </div>
  )
}

// ─── Slow Log ────────────────────────────────────────────────────────────────

function SlowLog({ version, poolName }: { version: string; poolName: string }) {
  const [expanded, setExpanded] = useState<number | null>(null)

  const { data: entries = [], isLoading, isError, error, refetch, isFetching } = useQuery<PHPSlowLogEntry[]>({
    queryKey: ['php', 'logs', 'slow', version, poolName],
    queryFn: async () => await api.get<PHPSlowLogEntry[]>(`/php/logs/${version}/${poolName}/slow`) ?? [],
    refetchInterval: 30_000,
    enabled: !!poolName,
  })

  if (!poolName) {
    return <div className="text-center py-10 text-zinc-500 text-sm">Select a pool to view slow log</div>
  }

  if (isLoading) {
    return <Skeleton className="h-48 bg-zinc-800 rounded-xl" />
  }
  if (isError) return <LogLoadError title="PHP slow log could not be loaded." error={error} retry={() => { void refetch() }} retrying={isFetching} />

  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2 text-zinc-400 text-xs">
          <AlertCircle className="w-3.5 h-3.5 text-amber-400" />
          {entries.length} slow request{entries.length !== 1 ? 's' : ''}
        </div>
        <Button
          variant="ghost" size="icon"
          className="h-7 w-7 text-zinc-400 hover:text-white"
          onClick={() => refetch()}
          disabled={isFetching}
        >
          <RefreshCw className={cn('w-3.5 h-3.5', isFetching && 'animate-spin')} />
        </Button>
      </div>

      {entries.length === 0 ? (
        <Card className="bg-zinc-900 border-zinc-800">
          <CardContent className="flex flex-col items-center justify-center py-12 text-zinc-600">
            <Clock className="w-8 h-8 mb-2 opacity-40" />
            <p className="text-sm">No slow requests recorded</p>
          </CardContent>
        </Card>
      ) : (
        <div className="space-y-2">
          {entries.map((entry, i) => (
            <Card key={i} className="bg-zinc-900 border-zinc-800 overflow-hidden">
              <button
                className="w-full px-4 py-3 flex items-center justify-between gap-3 hover:bg-zinc-800/40 transition-colors text-left"
                onClick={() => setExpanded(expanded === i ? null : i)}
              >
                <div className="flex items-center gap-3 min-w-0">
                  <div className={cn(
                    'px-2 py-0.5 rounded text-xs font-mono font-semibold flex-shrink-0',
                    entry.durationMs > 5000 ? 'bg-red-500/10 text-red-400'
                    : entry.durationMs > 2000 ? 'bg-amber-500/10 text-amber-400'
                    : 'bg-zinc-700 text-zinc-300'
                  )}>
                    {(entry.durationMs / 1000).toFixed(2)}s
                  </div>
                  <span className="text-zinc-300 text-sm font-mono truncate">{entry.script}</span>
                </div>
                <div className="flex items-center gap-2 flex-shrink-0">
                  <span className="text-zinc-500 text-xs">
                    {new Date(entry.timestamp).toLocaleString('en-GB', { hour12: false })}
                  </span>
                  {expanded === i
                    ? <ChevronDown className="w-3.5 h-3.5 text-zinc-500" />
                    : <ChevronRight className="w-3.5 h-3.5 text-zinc-500" />
                  }
                </div>
              </button>
              {expanded === i && entry.backtrace.length > 0 && (
                <div className="px-4 pb-3 border-t border-zinc-800">
                  <p className="text-zinc-500 text-[11px] uppercase tracking-wider my-2">Backtrace</p>
                  <div className="bg-zinc-950 rounded-lg p-3 font-mono text-xs space-y-0.5 max-h-48 overflow-y-auto">
                    {entry.backtrace.map((line, j) => (
                      <div key={j} className="text-zinc-400">{line}</div>
                    ))}
                  </div>
                </div>
              )}
            </Card>
          ))}
        </div>
      )}
    </div>
  )
}

// ─── LogsTab ──────────────────────────────────────────────────────────────────

interface LogsTabProps {
  selectedVersion: string
  pools: PHPPool[]
}

export default function LogsTab({ selectedVersion, pools }: LogsTabProps) {
  const [logType, setLogType] = useState<'error' | 'slow'>('error')
  const [selectedPool, setSelectedPool] = useState<string>('')

  const versionPools = pools.filter(p => p.version === selectedVersion)
  const currentPool = selectedPool || versionPools[0]?.name || ''

  return (
    <div className="space-y-4">
      {/* Sub-tab bar */}
      <div className="flex items-center justify-between gap-3 flex-wrap">
        <div className="flex items-center gap-0 border border-zinc-700 rounded-lg overflow-hidden">
          <button
            onClick={() => setLogType('error')}
            className={cn(
              'px-3 py-1.5 text-sm transition-colors',
              logType === 'error' ? 'bg-zinc-700 text-white' : 'text-zinc-400 hover:text-zinc-200'
            )}
          >
            Error Log
          </button>
          <button
            onClick={() => setLogType('slow')}
            className={cn(
              'px-3 py-1.5 text-sm transition-colors border-l border-zinc-700',
              logType === 'slow' ? 'bg-zinc-700 text-white' : 'text-zinc-400 hover:text-zinc-200'
            )}
          >
            Slow Log
          </button>
        </div>

        {logType === 'slow' && versionPools.length > 0 && (
          <div className="flex items-center gap-2">
            <span className="text-zinc-500 text-xs">Pool:</span>
            <select
              value={currentPool}
              onChange={e => setSelectedPool(e.target.value)}
              className="bg-zinc-800 border border-zinc-700 text-zinc-300 text-xs rounded-md px-2 py-1.5 h-7"
            >
              {versionPools.map(p => (
                <option key={p.name} value={p.name}>{p.name}</option>
              ))}
            </select>
          </div>
        )}
      </div>

      {logType === 'error'
        ? <ErrorLog version={selectedVersion} />
        : <SlowLog version={selectedVersion} poolName={currentPool} />
      }
    </div>
  )
}
