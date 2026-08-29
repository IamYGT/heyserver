import { useState, useMemo, useCallback } from 'react'
import { useSearchParams } from 'react-router-dom'
import {
  HardDrive, Activity, Trash2, Database, FolderOpen, Folder,
  Shield, AlertTriangle, CheckCircle2, FileText, XCircle, X,
  ChevronRight, Loader2, File, RefreshCw, Search,
} from 'lucide-react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { Badge } from '@/components/ui/badge'
import { toast } from 'sonner'
import {
  useDiskOverview, useSmartInfo, useDirList, useLargestFiles,
  useCleanupScan, useCleanupExecute, useDiskMounts,
  useDiskAnalysisStart, useDiskAnalysisStatus,
  type CleanupExecutionResponse, type Partition, type SmartInfo,
} from '@/hooks/useDisk'
import { useHostActionStatus } from '@/hooks/useHostActionStatus'
import { hostActionLabel } from '@/lib/hostControls'
import { DependencyRemediation } from '@/components/DependencyRemediation'

// ── Helpers ──────────────────────────────────────────────────────────────────

function formatBytes(bytes: number): string {
  if (bytes === 0) return '0 B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return `${(bytes / Math.pow(k, i)).toFixed(1)} ${sizes[i]}`
}

function pctColor(pct: number): string {
  if (pct > 90) return 'text-red-400'
  if (pct > 75) return 'text-amber-400'
  return 'text-emerald-400'
}

function pctBarColor(pct: number): string {
  if (pct > 90) return '#ef4444'
  if (pct > 75) return '#f59e0b'
  return '#22c55e'
}

// ── Tabs ─────────────────────────────────────────────────────────────────────

type TabId = 'overview' | 'analysis' | 'cleanup' | 'mounts'

const TAB_IDS = new Set<TabId>(['overview', 'analysis', 'cleanup', 'mounts'])

const TABS: { id: TabId; label: string; icon: React.ComponentType<{ className?: string }> }[] = [
  { id: 'overview', label: 'Overview', icon: HardDrive },
  { id: 'analysis', label: 'Space Analysis', icon: FolderOpen },
  { id: 'cleanup', label: 'Cleanup', icon: Trash2 },
  { id: 'mounts', label: 'Mounts & fstab', icon: Database },
]

// ── Partition Card ───────────────────────────────────────────────────────────

function PartitionCard({ p, onClick }: { p: Partition; onClick?: () => void }) {
  return (
    <div
      onClick={onClick}
      className={`bg-zinc-800/50 rounded-xl border border-zinc-700/30 p-4 transition-colors ${
        onClick ? 'cursor-pointer hover:border-purple-500/40 hover:bg-zinc-800/80 group' : 'hover:border-zinc-600/50'
      }`}
    >
      <div className="flex items-start justify-between mb-3">
        <div>
          <div className="flex items-center gap-2">
            <HardDrive className="w-4 h-4 text-purple-400" />
            <span className="text-white text-sm font-semibold font-mono">{p.mountPoint}</span>
          </div>
          <div className="flex items-center gap-2 mt-1">
            <Badge variant="outline" className="text-[10px] px-1.5 py-0">{p.fsType}</Badge>
            <span className="text-zinc-500 text-[10px] font-mono">{p.device}</span>
            {p.label && <span className="text-zinc-500 text-[10px]">({p.label})</span>}
          </div>
        </div>
        <span className={`text-lg font-bold tabular-nums ${pctColor(p.usePercent)}`}>
          {p.usePercent.toFixed(1)}%
        </span>
      </div>

      {/* Usage bar */}
      <div className="h-2.5 bg-zinc-700/50 rounded-full overflow-hidden mb-2">
        <div
          className="h-full rounded-full transition-all duration-500"
          style={{ width: `${Math.min(100, p.usePercent)}%`, backgroundColor: pctBarColor(p.usePercent) }}
        />
      </div>

      <div className="flex justify-between text-[10px] text-zinc-500 font-mono">
        <span>{formatBytes(p.used)} used</span>
        <span>{formatBytes(p.available)} free</span>
        <span>{formatBytes(p.size)} total</span>
      </div>
      {onClick && (
        <div className="mt-2 pt-2 border-t border-zinc-700/30 flex items-center justify-center gap-1 text-[10px] text-zinc-600 group-hover:text-purple-400 transition-colors">
          <FolderOpen className="w-3 h-3" />
          <span>Click to explore directory</span>
          <ChevronRight className="w-3 h-3" />
        </div>
      )}
    </div>
  )
}

// ── Overview Tab ─────────────────────────────────────────────────────────────

export function SmartHealthCard({
  smart,
  loading,
  error,
  retry,
  retrying,
}: {
  smart?: SmartInfo
  loading: boolean
  error: Error | null
  retry: () => void
  retrying: boolean
}) {
  const failed = smart?.status === 'FAILED'
  const passed = smart?.status === 'PASSED' && smart.healthy

  return (
    <Card className="bg-zinc-900/80 border-zinc-800/80">
      <CardHeader className="pb-2 pt-3">
        <CardTitle className="text-zinc-200 text-xs font-semibold uppercase tracking-wider flex items-center gap-2">
          <Shield className="w-3.5 h-3.5 text-emerald-400" />
          Root Disk SMART Health
        </CardTitle>
      </CardHeader>
      <CardContent>
        {loading ? (
          <Skeleton className="h-16 bg-zinc-800/50 rounded-xl" />
        ) : error ? (
          <DependencyRemediation
            title="SMART health could not be observed"
            summary="HServer could not resolve or inspect the physical disk behind the root filesystem. No health result is assumed."
            error={error.message}
            retry={retry}
            retrying={retrying}
            steps={[
              'Verify lsblk, df, and smartctl are available to the HServer service.',
              'Confirm the root storage exposes a physical disk and SMART data.',
              'Inspect the panel service log, then retry detection.',
            ]}
          />
        ) : !smart?.available ? (
          <DependencyRemediation
            state="not-configured"
            title="SMART health is unavailable for the root storage"
            summary={smart?.message || 'The observed root filesystem does not expose one physical disk with readable SMART data.'}
            retry={retry}
            retrying={retrying}
            steps={[
              'For a physical server, install smartmontools and verify smartctl can read the root disk.',
              'For virtual, RAID, or multi-disk storage, use the provider or controller health source.',
              'Retry detection after the storage visibility changes.',
            ]}
          />
        ) : (
          <div className="flex items-center gap-4 py-2">
            <div className={`w-10 h-10 rounded-xl flex items-center justify-center ${passed ? 'bg-emerald-500/10' : failed ? 'bg-red-500/10' : 'bg-amber-500/10'}`}>
              {passed ? <CheckCircle2 className="w-5 h-5 text-emerald-400" /> : <AlertTriangle className={`w-5 h-5 ${failed ? 'text-red-400' : 'text-amber-400'}`} />}
            </div>
            <div>
              <p className={`text-sm font-semibold ${passed ? 'text-emerald-400' : failed ? 'text-red-400' : 'text-amber-300'}`}>
                {smart.status}
              </p>
              <p className="text-zinc-600 text-[10px] font-mono">{smart.device}</p>
              {smart.model && <p className="text-zinc-500 text-xs">{smart.model}</p>}
              {smart.serial && <p className="text-zinc-600 text-[10px] font-mono">S/N: {smart.serial}</p>}
              {smart.message && <p className="mt-1 text-xs text-amber-300/70">{smart.message}</p>}
            </div>
          </div>
        )}
      </CardContent>
    </Card>
  )
}

export function OverviewTab({ onExplore, onCleanup }: { onExplore: (path: string) => void; onCleanup: () => void }) {
  const overviewQuery = useDiskOverview()
  const overview = overviewQuery.data
  const smartQuery = useSmartInfo('root')

  if (overviewQuery.isLoading) {
    return <div className="space-y-3">{[1,2,3].map(i => <Skeleton key={i} className="h-32 bg-zinc-900" />)}</div>
  }

  if (overviewQuery.isError) {
    return (
      <div className="space-y-4">
        <DependencyRemediation
          title="Disk overview could not be observed"
          summary="Mounted filesystems, capacity totals, and I/O counters remain unknown. HServer will not describe the failed inventory as an empty server."
          error={overviewQuery.error.message}
          retry={() => { void overviewQuery.refetch() }}
          retrying={overviewQuery.isFetching}
          steps={[
            'Verify lsblk, df, and /proc/diskstats are readable by the HServer service.',
            'Inspect the panel service log for the original inventory failure.',
            'Retry detection after host storage visibility is restored.',
          ]}
        />
        <SmartHealthCard
          smart={smartQuery.data}
          loading={smartQuery.isLoading}
          error={smartQuery.error}
          retry={() => { void smartQuery.refetch() }}
          retrying={smartQuery.isFetching}
        />
      </div>
    )
  }

  const partitions = overview?.partitions ?? []
  const ioStats = overview?.ioStats ?? []
  const rootPartition = partitions.find((partition) => partition.mountPoint === '/')

  return (
    <div className="space-y-4">
      {rootPartition && rootPartition.usePercent >= 95 && <div className="flex flex-col gap-3 rounded-xl border border-red-500/40 bg-red-500/10 p-4 sm:flex-row sm:items-center sm:justify-between"><div className="flex items-start gap-3"><AlertTriangle className="mt-0.5 size-5 shrink-0 text-red-400" /><div><p className="text-sm font-semibold text-red-300">Root filesystem is critically full: {rootPartition.usePercent.toFixed(1)}%</p><p className="mt-1 text-xs text-red-200/60">Only {formatBytes(rootPartition.available)} remains. Review measured cleanup candidates before services lose write access.</p></div></div><Button variant="destructive" onClick={onCleanup}><Trash2 className="size-4" /> Open cleanup</Button></div>}
      {/* Total summary */}
      {overview && (
        <div className="grid grid-cols-3 gap-3">
          <Card className="bg-zinc-900/80 border-zinc-800/80">
            <CardContent className="py-4 text-center">
              <p className="text-zinc-500 text-[10px] uppercase tracking-wider font-semibold">Total Disk</p>
              <p className="text-white text-2xl font-bold mt-1">{formatBytes(overview.totalSize)}</p>
            </CardContent>
          </Card>
          <Card className="bg-zinc-900/80 border-zinc-800/80">
            <CardContent className="py-4 text-center">
              <p className="text-zinc-500 text-[10px] uppercase tracking-wider font-semibold">Used</p>
              <p className="text-amber-400 text-2xl font-bold mt-1">{formatBytes(overview.totalUsed)}</p>
            </CardContent>
          </Card>
          <Card className="bg-zinc-900/80 border-zinc-800/80">
            <CardContent className="py-4 text-center">
              <p className="text-zinc-500 text-[10px] uppercase tracking-wider font-semibold">Free</p>
              <p className="text-emerald-400 text-2xl font-bold mt-1">{formatBytes(overview.totalFree)}</p>
            </CardContent>
          </Card>
        </div>
      )}

      {/* Partitions */}
      <Card className="bg-zinc-900/80 border-zinc-800/80">
        <CardHeader className="pb-2 pt-3">
          <CardTitle className="text-zinc-200 text-xs font-semibold uppercase tracking-wider flex items-center gap-2">
            <HardDrive className="w-3.5 h-3.5 text-purple-400" />
            Partitions & Filesystems
          </CardTitle>
        </CardHeader>
        <CardContent>
          {partitions.length > 0 ? (
            <div className="grid grid-cols-1 lg:grid-cols-2 gap-3">
              {partitions.map((p) => (
                <PartitionCard
                  key={`${p.device}:${p.mountPoint}`}
                  p={p}
                  onClick={p.mountPoint !== 'none' ? () => onExplore(p.mountPoint) : undefined}
                />
              ))}
            </div>
          ) : (
            <p className="rounded-lg border border-zinc-800 bg-zinc-950/50 px-4 py-8 text-center text-sm text-zinc-500">
              The host returned a successful inventory with no mounted block devices.
            </p>
          )}
        </CardContent>
      </Card>

      {/* I/O Stats */}
      {ioStats.length > 0 && (
        <Card className="bg-zinc-900/80 border-zinc-800/80">
          <CardHeader className="pb-2 pt-3">
            <CardTitle className="text-zinc-200 text-xs font-semibold uppercase tracking-wider flex items-center gap-2">
              <Activity className="w-3.5 h-3.5 text-blue-400" />
              Disk I/O (cumulative since boot)
            </CardTitle>
          </CardHeader>
          <CardContent>
            <div className="overflow-x-auto">
              <table className="w-full text-xs">
                <thead>
                  <tr className="text-zinc-500 border-b border-zinc-800">
                    <th className="text-left py-1.5 px-2 font-medium">Device</th>
                    <th className="text-right py-1.5 px-2 font-medium">Reads</th>
                    <th className="text-right py-1.5 px-2 font-medium">Writes</th>
                    <th className="text-right py-1.5 px-2 font-medium">Read</th>
                    <th className="text-right py-1.5 px-2 font-medium">Written</th>
                    <th className="text-right py-1.5 px-2 font-medium">IO Queue</th>
                  </tr>
                </thead>
                <tbody>
                  {ioStats.map(io => (
                    <tr key={io.device} className="border-b border-zinc-800/30 hover:bg-zinc-800/20">
                      <td className="py-1.5 px-2 text-zinc-300 font-mono">{io.device}</td>
                      <td className="py-1.5 px-2 text-right text-zinc-400 tabular-nums">{io.readsCompleted.toLocaleString()}</td>
                      <td className="py-1.5 px-2 text-right text-zinc-400 tabular-nums">{io.writesCompleted.toLocaleString()}</td>
                      <td className="py-1.5 px-2 text-right text-blue-400 font-mono tabular-nums">{formatBytes(io.readBytes)}</td>
                      <td className="py-1.5 px-2 text-right text-orange-400 font-mono tabular-nums">{formatBytes(io.writeBytes)}</td>
                      <td className="py-1.5 px-2 text-right text-zinc-400 tabular-nums">{io.ioInProgress}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </CardContent>
        </Card>
      )}

      <SmartHealthCard
        smart={smartQuery.data}
        loading={smartQuery.isLoading}
        error={smartQuery.error}
        retry={() => { void smartQuery.refetch() }}
        retrying={smartQuery.isFetching}
      />
    </div>
  )
}

// ── Space Analysis Tab (File Manager Style) ─────────────────────────────────

export function DeepAnalysisCard() {
  const statusQuery = useDiskAnalysisStatus()
  const startMutation = useDiskAnalysisStart()
  const status = statusQuery.data
  const active = status?.status === 'queued' || status?.status === 'running'
  const statusUnavailable = statusQuery.isLoading || statusQuery.isError
  const entries = status?.entries ?? []
  const maxSize = entries[0]?.size || 1
  const start = () => startMutation.mutate(undefined, {
    onSuccess: (result) => toast.success(result.status === 'running' ? 'Deep analysis is already running' : `Deep analysis queued: ${result.id}`),
    onError: (error) => toast.error(error.message || 'Could not start deep analysis'),
  })

  return <Card className="border-blue-500/20 bg-zinc-900/80">
    <CardHeader className="flex-row items-start justify-between gap-4"><div><CardTitle className="flex items-center gap-2 text-sm text-zinc-200"><Search className="size-4 text-blue-400" /> Persistent deep analysis</CardTitle><p className="mt-1 text-[10px] text-zinc-500">Scans large paths in a low-priority systemd job. Results survive page refresh and HServer restarts.</p></div><div className="flex gap-2"><Button variant="ghost" size="xs" onClick={() => statusQuery.refetch()} disabled={statusQuery.isFetching}><RefreshCw className={`size-3 ${statusQuery.isFetching ? 'animate-spin' : ''}`} /> Refresh</Button><Button size="xs" disabled={statusUnavailable || active || startMutation.isPending} onClick={start}>{active || startMutation.isPending ? <Loader2 className="size-3 animate-spin" /> : <Search className="size-3" />}{status?.status === 'completed' || status?.status === 'failed' ? 'Run again' : 'Start analysis'}</Button></div></CardHeader>
    <CardContent className="space-y-3">{statusQuery.isLoading ? <Skeleton className="h-20 bg-zinc-800/50" /> : statusQuery.isError ? <DependencyRemediation title="Deep analysis status could not be observed" summary="HServer cannot safely start another persistent scan until the current job state is known." error={statusQuery.error?.message} retry={() => statusQuery.refetch()} retrying={statusQuery.isFetching} steps={['Retry status detection.', 'Confirm the HServer service is reachable.', 'Verify the local analysis worker and systemd are available.']} /> : !status || status.status === 'idle' ? <p className="py-6 text-center text-xs text-zinc-600">No deep analysis has run yet.</p> : <><div className="flex flex-wrap items-center gap-2 text-[10px]"><span className={`rounded px-2 py-1 font-semibold uppercase ${status.status === 'completed' ? 'bg-emerald-500/10 text-emerald-400' : status.status === 'failed' ? 'bg-red-500/10 text-red-400' : 'bg-blue-500/10 text-blue-400'}`}>{status.status}</span><span className="text-zinc-500">{status.message}</span>{status.id && <span className="font-mono text-zinc-700">{status.id}</span>}{status.finished_at && <span className="text-zinc-600">Finished {new Date(status.finished_at).toLocaleString()}</span>}</div>{active && <div className="flex items-center gap-2 rounded-lg border border-blue-500/20 bg-blue-500/5 p-3 text-xs text-blue-300"><Loader2 className="size-4 animate-spin" />Scanning `/var/lib`, `/var/www`, `/opt` and `/root` in the background…</div>}{entries.length > 0 && <><p className="text-[10px] text-zinc-600">Top paths over 100 MiB. Parent and child paths may overlap; values are attribution, not a sum.</p><div className="max-h-[520px] overflow-auto rounded-xl border border-zinc-800">{entries.slice(0, 40).map((entry, index) => <div key={entry.path} className="relative flex items-center gap-3 overflow-hidden border-b border-zinc-800/60 px-3 py-2 last:border-0"><div className="absolute inset-y-0 left-0 bg-blue-500/[0.06]" style={{ width: `${Math.max(1, (entry.size / maxSize) * 100)}%` }} /><span className="relative w-6 shrink-0 text-right font-mono text-[9px] text-zinc-700">{index + 1}</span><span className="relative min-w-0 flex-1 truncate font-mono text-[10px] text-zinc-300" title={entry.path}>{entry.path}</span><span className="relative shrink-0 font-mono text-[10px] font-semibold text-blue-400">{formatBytes(entry.size)}</span></div>)}</div></>}{status.errors && status.errors.length > 0 && <div className="rounded-lg border border-amber-500/20 bg-amber-500/5 p-3 text-[10px] text-amber-400">{status.errors.join(' · ')}</div>}</>}</CardContent>
  </Card>
}

export function SpaceAnalysisTab({ initialPath = '/' }: { initialPath?: string }) {
  const [currentPath, setCurrentPath] = useState(initialPath)
  const [largestScanPath, setLargestScanPath] = useState<string | null>(null)
  const { data: listing, isLoading, isFetching, isError, error, refetch } = useDirList(currentPath)
  const largestRequested = largestScanPath === currentPath
  const { data: largest, isFetching: largestLoading, error: largestError } = useLargestFiles(currentPath, 20, largestRequested)

  const navigateTo = (path: string) => {
    setCurrentPath(path)
    setLargestScanPath(null)
  }

  const entries = listing?.entries ?? []
  const dirs = entries.filter(e => e.isDir)
  const files = entries.filter(e => !e.isDir)

  // Breadcrumb
  const breadcrumbs = useMemo(() => {
    const parts = currentPath.split('/').filter(Boolean)
    const crumbs = [{ label: '/', path: '/' }]
    let acc = ''
    for (const part of parts) {
      acc += '/' + part
      crumbs.push({ label: part, path: acc })
    }
    return crumbs
  }, [currentPath])

  // Largest file size for bar scaling — computed inline (cheap, avoids memoization warning)
  const maxFileSize = files.reduce((max, f) => (f.size > max ? f.size : max), 1)

  return (
    <div className="space-y-4">
      <DeepAnalysisCard />
      {/* Breadcrumb */}
      <div className="flex items-center gap-1 text-xs text-zinc-400 flex-wrap bg-zinc-800/30 rounded-lg px-3 py-2 border border-zinc-700/20">
        {breadcrumbs.map((crumb, i) => (
          <span key={crumb.path} className="flex items-center gap-1">
            {i > 0 && <ChevronRight className="w-3 h-3 text-zinc-600" />}
            <button
              onClick={() => navigateTo(crumb.path)}
              className={`hover:text-white transition-colors px-1.5 py-0.5 rounded ${
                crumb.path === currentPath ? 'text-white bg-zinc-700/50 font-medium' : 'hover:bg-zinc-700/30'
              }`}
            >
              {crumb.label}
            </button>
          </span>
        ))}
        <span className="ml-auto text-zinc-600 text-[10px]">{isError ? 'Directory unavailable' : `${entries.length} items`}</span>
      </div>

      {/* Directory listing */}
      <Card className="bg-zinc-900/80 border-zinc-800/80">
        <CardContent className="p-0">
          {isLoading ? (
            <div className="p-4 space-y-2">{[1,2,3,4,5].map(i => <Skeleton key={i} className="h-9 bg-zinc-800/50" />)}</div>
          ) : isError ? (
            <div className="p-4">
              <DependencyRemediation title="Directory contents could not be observed" summary={`HServer did not return a successful listing for ${currentPath}. An unavailable path is not treated as an empty directory.`} error={error?.message} retry={() => refetch()} retrying={isFetching} steps={['Retry the directory listing.', 'Confirm the path still exists on this server.', 'Verify the HServer service account can read the path.']} />
            </div>
          ) : entries.length === 0 ? (
            <p className="text-zinc-500 text-sm py-8 text-center">Empty directory</p>
          ) : (
            <div className="divide-y divide-zinc-800/40">
              {/* Back button */}
              {currentPath !== '/' && (
                <button
                  onClick={() => {
                    const parent = currentPath.split('/').slice(0, -1).join('/') || '/'
                    navigateTo(parent)
                  }}
                  className="w-full flex items-center gap-3 px-4 py-2.5 hover:bg-zinc-800/30 transition-colors text-left"
                >
                  <ChevronRight className="w-3.5 h-3.5 text-zinc-600 rotate-180" />
                  <Folder className="w-4 h-4 text-zinc-500" />
                  <span className="text-zinc-400 text-xs font-mono">..</span>
                </button>
              )}

              {/* Directories */}
              {dirs.map((entry) => (
                <button
                  key={entry.path}
                  onClick={() => navigateTo(entry.path)}
                  className="w-full flex items-center gap-3 px-4 py-2.5 hover:bg-zinc-800/30 transition-colors text-left group"
                >
                  <ChevronRight className="w-3.5 h-3.5 text-zinc-700 group-hover:text-zinc-400 shrink-0" />
                  <Folder className="w-4 h-4 text-amber-500/70 group-hover:text-amber-400 shrink-0" />
                  <span className="text-zinc-200 text-xs font-mono truncate flex-1 group-hover:text-white">
                    {entry.name}
                  </span>
                  {entry.children !== undefined && (
                    <span className="text-zinc-600 text-[10px] tabular-nums shrink-0">
                      {entry.children} items
                    </span>
                  )}
                  <span className="text-zinc-500 text-[10px] font-mono w-16 text-right shrink-0 hidden sm:block">
                    {entry.mode}
                  </span>
                </button>
              ))}

              {/* Separator if both dirs and files exist */}
              {dirs.length > 0 && files.length > 0 && (
                <div className="px-4 py-1.5 bg-zinc-800/20">
                  <span className="text-zinc-600 text-[10px] uppercase tracking-wider font-semibold">
                    Files ({files.length})
                  </span>
                </div>
              )}

              {/* Files */}
              {files.map((entry) => {
                const sizePct = maxFileSize > 0 ? (entry.size / maxFileSize) * 100 : 0
                return (
                  <div
                    key={entry.path}
                    className="flex items-center gap-3 px-4 py-2 hover:bg-zinc-800/20 transition-colors"
                  >
                    <span className="w-3.5 shrink-0" /> {/* spacer to align with dirs */}
                    <File className="w-4 h-4 text-zinc-600 shrink-0" />
                    <span className="text-zinc-400 text-xs font-mono truncate flex-1" title={entry.path}>
                      {entry.name}
                    </span>
                    {/* Size bar */}
                    <div className="w-20 h-1.5 bg-zinc-800 rounded-full overflow-hidden shrink-0 hidden sm:block">
                      <div
                        className="h-full bg-blue-500/40 rounded-full"
                        style={{ width: `${sizePct}%` }}
                      />
                    </div>
                    <span className="text-zinc-300 text-xs font-mono tabular-nums w-20 text-right shrink-0">
                      {formatBytes(entry.size)}
                    </span>
                    <span className="text-zinc-600 text-[10px] w-20 text-right shrink-0 hidden sm:block">
                      {new Date(entry.modified).toLocaleDateString('en-US', { month: 'short', day: 'numeric' })}
                    </span>
                  </div>
                )
              })}
            </div>
          )}
        </CardContent>
      </Card>

      {/* Largest files in current path */}
      <Card className="bg-zinc-900/80 border-zinc-800/80">
        <CardHeader className="flex-row items-center justify-between gap-3 pb-2 pt-3">
          <div><CardTitle className="text-zinc-200 text-xs font-semibold uppercase tracking-wider flex items-center gap-2">
            <FileText className="w-3.5 h-3.5 text-rose-400" />
            Largest Files (recursive) — {currentPath}
          </CardTitle><p className="mt-1 text-[10px] text-zinc-600">Recursive scans run only when requested; browsing directories stays instant.</p></div>
          <Button size="xs" variant="outline" disabled={largestLoading} onClick={() => setLargestScanPath(currentPath)}>
            {largestLoading ? <Loader2 className="size-3 animate-spin" /> : <Search className="size-3" />}
            {largestRequested ? 'Scan again' : 'Scan files'}
          </Button>
        </CardHeader>
        <CardContent>
          {!largestRequested ? (
            <p className="py-5 text-center text-xs text-zinc-500">Select “Scan files” to search this folder recursively. For the entire root filesystem, prefer the persistent deep analysis above.</p>
          ) : largestLoading ? (
            <div className="space-y-2">{[1,2,3].map(i => <Skeleton key={i} className="h-8 bg-zinc-800/50" />)}</div>
          ) : largestError ? (
            <div className="flex items-start gap-2 rounded-lg border border-red-500/30 bg-red-500/10 p-3 text-xs text-red-300"><AlertTriangle className="mt-0.5 size-4 shrink-0" /><span>{largestError.message}</span></div>
          ) : !largest || largest.length === 0 ? (
            <p className="text-zinc-500 text-sm py-4 text-center">No files found</p>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-xs">
                <thead>
                  <tr className="text-zinc-500 border-b border-zinc-800">
                    <th className="text-left py-1.5 px-2 font-medium w-8">#</th>
                    <th className="text-left py-1.5 px-2 font-medium">File</th>
                    <th className="text-right py-1.5 px-2 font-medium w-24">Size</th>
                    <th className="text-right py-1.5 px-2 font-medium w-28">Modified</th>
                  </tr>
                </thead>
                <tbody>
                  {largest.map((f, i) => (
                    <tr key={f.path} className="border-b border-zinc-800/30 hover:bg-zinc-800/20">
                      <td className="py-1.5 px-2 text-zinc-600 tabular-nums">{i + 1}</td>
                      <td className="py-1.5 px-2">
                        <div className="flex items-center gap-1.5">
                          <File className="w-3 h-3 text-zinc-600 shrink-0" />
                          <span className="text-zinc-300 font-mono truncate max-w-[350px]" title={f.path}>{f.path}</span>
                        </div>
                      </td>
                      <td className="py-1.5 px-2 text-right text-amber-400 font-mono tabular-nums">{formatBytes(f.size)}</td>
                      <td className="py-1.5 px-2 text-right text-zinc-500 text-[10px]">
                        {f.modified ? new Date(f.modified).toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: 'numeric' }) : '—'}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  )
}

// ── Cleanup Tab ──────────────────────────────────────────────────────────────

function CleanupTab() {
  const { data: targets, isLoading, isFetching, error, refetch } = useCleanupScan()
  const { data: overview } = useDiskOverview()
  const cleanupMutation = useCleanupExecute()
	const actionStatus = useHostActionStatus('local')
	const maintenanceRunning = actionStatus.data?.running === true
	const maintenanceStatusUnavailable = actionStatus.isLoading || actionStatus.isError
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [confirmed, setConfirmed] = useState(false)
  const [lastRun, setLastRun] = useState<CleanupExecutionResponse | null>(null)

  const safeRootTargets = useMemo(() => {
    return (targets ?? []).filter(t => t.scope === 'root filesystem' && t.risk === 'low' && t.size > 0)
  }, [targets])
  const safeRootBytes = useMemo(() => {
    return safeRootTargets.reduce((sum, target) => sum + target.size, 0)
  }, [safeRootTargets])
  const rootAvailable = overview?.partitions.find(partition => partition.mountPoint === '/')?.available

  const selectSafeRoot = () => {
    setSelected(new Set(safeRootTargets.map(target => target.id)))
    setConfirmed(false)
  }

  const clearSelection = () => {
    setSelected(new Set())
    setConfirmed(false)
  }

  const toggleTarget = (id: string) => {
    setSelected(prev => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
    setConfirmed(false)
  }

  const totalSelected = useMemo(() => {
    return (targets ?? []).filter(t => selected.has(t.id)).reduce((sum, t) => sum + t.size, 0)
  }, [targets, selected])
  const rootSelected = useMemo(() => {
    return (targets ?? []).filter(t => selected.has(t.id) && t.scope === 'root filesystem').reduce((sum, t) => sum + t.size, 0)
  }, [targets, selected])

  const handleCleanup = () => {
    if (!confirmed || selected.size === 0) return
    cleanupMutation.mutate(Array.from(selected), {
      onSuccess: (data) => {
        setLastRun(data)
        const results = data?.results ?? []
        const ok = results.filter(r => r.status === 'ok').length
        const fail = results.filter(r => r.status === 'error').length
        const reclaimed = results.reduce((sum, result) => sum + (result.reclaimed ?? 0), 0)
        if (ok > 0) toast.success(`${ok} cleanup task(s) completed · ${formatBytes(reclaimed)} actually reclaimed`)
        if (fail > 0) toast.error(`${fail} cleanup task(s) failed`)
        setSelected(new Set())
        setConfirmed(false)
      },
      onError: () => toast.error('Cleanup failed'),
    })
  }

  return (
    <Card className="bg-zinc-900/80 border-zinc-800/80">
      <CardHeader className="pb-2 pt-3">
        <div className="flex items-center justify-between">
          <CardTitle className="text-zinc-200 text-xs font-semibold uppercase tracking-wider flex items-center gap-2">
            <Trash2 className="w-3.5 h-3.5 text-red-400" />
            Disk Cleanup
          </CardTitle>
          <div className="flex items-center gap-3">
            {selected.size > 0 && (
              <span className="text-amber-400 text-xs font-mono">
                {selected.size} selected · up to {formatBytes(totalSelected)} · root {formatBytes(rootSelected)}
              </span>
            )}
            <Button type="button" variant="ghost" size="xs" onClick={() => refetch()} disabled={isFetching} aria-label="Refresh cleanup scan">
              <RefreshCw className={`size-3 ${isFetching ? 'animate-spin' : ''}`} /> Refresh
            </Button>
          </div>
        </div>
      </CardHeader>
      <CardContent>
				{maintenanceRunning && (
					<div className="mb-4 flex items-center gap-2 rounded-xl border border-blue-500/20 bg-blue-500/[0.06] px-4 py-3 text-xs text-blue-300">
						<Loader2 className="size-4 shrink-0 animate-spin" />
						<strong>{hostActionLabel(actionStatus.data?.action)} is running on HServer.</strong>
						<span className="text-blue-300/60">New cleanup requests are paused until it finishes.</span>
					</div>
				)}
				{maintenanceStatusUnavailable && (
					<div className="mb-4 flex items-center justify-between gap-3 rounded-xl border border-amber-500/20 bg-amber-500/[0.06] px-4 py-3 text-xs text-amber-300">
						<span>{actionStatus.isError ? 'Could not verify active host maintenance. Cleanup is paused.' : 'Checking active host maintenance before enabling cleanup…'}</span>
						{actionStatus.isError && <Button type="button" variant="ghost" size="xs" onClick={() => actionStatus.refetch()}>Retry</Button>}
					</div>
				)}
        {lastRun && (
          <div data-testid="cleanup-last-run" aria-live="polite" className="mb-4 overflow-hidden rounded-xl border border-blue-500/20 bg-blue-500/[0.04]">
            <div className="flex items-start justify-between gap-3 border-b border-blue-500/15 px-4 py-3">
              <div>
                <p className="flex items-center gap-2 text-xs font-semibold text-blue-300"><CheckCircle2 className="size-3.5" /> Last cleanup result</p>
                <p className="mt-1 text-[10px] text-zinc-500">
                  Root free space: {formatBytes(lastRun.root_available_before)} → {formatBytes(lastRun.root_available_after)}
                  {lastRun.root_available_after > lastRun.root_available_before && ` · +${formatBytes(lastRun.root_available_after - lastRun.root_available_before)}`}
                </p>
              </div>
              <button type="button" onClick={() => setLastRun(null)} aria-label="Dismiss cleanup result" className="grid size-7 place-items-center rounded-md text-zinc-500 transition hover:bg-zinc-800 hover:text-white"><X className="size-3.5" /></button>
            </div>
            <div className="divide-y divide-zinc-800/60">
              {lastRun.results.map((result) => {
                const target = targets?.find((item) => item.id === result.id)
                return (
                  <div key={result.id} className="flex flex-col gap-2 px-4 py-3 sm:flex-row sm:items-start sm:justify-between">
                    <div className="flex min-w-0 items-start gap-2.5">
                      {result.status === 'ok' ? <CheckCircle2 className="mt-0.5 size-4 shrink-0 text-emerald-400" /> : <XCircle className="mt-0.5 size-4 shrink-0 text-red-400" />}
                      <div className="min-w-0">
                        <p className="text-xs font-medium text-zinc-200">{target?.name ?? result.id}</p>
                        <p className={`mt-0.5 break-words text-[10px] ${result.status === 'ok' ? 'text-zinc-500' : 'text-red-300'}`}>{result.message || (result.status === 'ok' ? 'Cleanup completed.' : 'Cleanup failed.')}</p>
                      </div>
                    </div>
                    <span className={`shrink-0 font-mono text-xs font-semibold ${result.status === 'ok' ? 'text-emerald-300' : 'text-red-300'}`}>
                      {result.status === 'ok' ? `${formatBytes(result.reclaimed)} reclaimed` : 'Failed'}
                    </span>
                  </div>
                )
              })}
            </div>
          </div>
        )}
        {isLoading ? (
          <div className="space-y-3">{[1,2,3].map(i => <Skeleton key={i} className="h-16 bg-zinc-800/50" />)}</div>
        ) : error ? (
          <div className="flex flex-col items-center justify-center gap-3 rounded-xl border border-red-500/25 bg-red-500/[0.06] px-4 py-8 text-center">
            <AlertTriangle className="size-5 text-red-400" />
            <div><p className="text-sm font-medium text-red-300">Cleanup scan could not be loaded</p><p className="mt-1 text-xs text-zinc-500">{error.message || 'The server did not return cleanup candidates.'}</p></div>
            <Button type="button" variant="outline" size="sm" onClick={() => refetch()} disabled={isFetching}><RefreshCw className={`size-3.5 ${isFetching ? 'animate-spin' : ''}`} /> Retry scan</Button>
          </div>
        ) : !targets || targets.length === 0 ? (
          <div className="flex items-center gap-3 py-8 justify-center text-zinc-500">
            <CheckCircle2 className="w-5 h-5 text-emerald-400" />
            <span className="text-sm">Disk is clean — nothing to reclaim</span>
          </div>
        ) : (
          <div className="space-y-2">
            <div className="mb-3 flex flex-col gap-3 rounded-xl border border-emerald-500/20 bg-emerald-500/[0.04] p-3 sm:flex-row sm:items-center sm:justify-between">
              <div className="min-w-0">
                <div className="flex items-center gap-2 text-xs font-semibold text-emerald-300">
                  <Shield className="h-3.5 w-3.5" />
                  Recommended rescue preset
                </div>
                <p className="mt-1 text-[11px] leading-relaxed text-zinc-500">
                  Selects only measured low-risk candidates on the root filesystem. Nothing is deleted until you separately confirm and run cleanup.
                </p>
              </div>
              <div className="flex shrink-0 items-center gap-2">
                {selected.size > 0 && (
                  <Button type="button" variant="ghost" size="sm" onClick={clearSelection}>
                    Clear selection
                  </Button>
                )}
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={selectSafeRoot}
					disabled={safeRootTargets.length === 0 || maintenanceRunning || maintenanceStatusUnavailable}
                  className="border-emerald-500/30 text-emerald-300 hover:bg-emerald-500/10 hover:text-emerald-200"
                >
                  Select safe root · {safeRootTargets.length} · {formatBytes(safeRootBytes)}
                </Button>
              </div>
            </div>

            {targets.map(t => (
              <label
                key={t.id}
                className={`flex items-center gap-4 p-3 rounded-xl border cursor-pointer transition-all ${
                  selected.has(t.id)
                    ? 'bg-red-500/5 border-red-500/20'
                    : 'bg-zinc-800/30 border-zinc-800/50 hover:border-zinc-700/50'
                }`}
              >
                <input
                  type="checkbox"
					disabled={maintenanceRunning || maintenanceStatusUnavailable}
                  checked={selected.has(t.id)}
                  onChange={() => toggleTarget(t.id)}
                  className="w-4 h-4 rounded border-zinc-600 bg-zinc-800 text-red-500 focus:ring-red-500/20"
                />
                <div className="flex-1 min-w-0">
                  <p className="text-zinc-200 text-sm font-medium">{t.name}</p>
                  <p className="text-zinc-500 text-xs mt-0.5">{t.description}</p>
                  <div className="mt-1.5 flex gap-1.5"><span className="rounded bg-zinc-700/50 px-1.5 py-0.5 text-[9px] uppercase text-zinc-500">{t.scope}</span><span className={`rounded px-1.5 py-0.5 text-[9px] font-semibold uppercase ${t.risk === 'low' ? 'bg-emerald-500/10 text-emerald-400' : 'bg-amber-500/10 text-amber-400'}`}>{t.risk} risk</span></div>
                </div>
                <span className="text-amber-400 text-sm font-bold font-mono tabular-nums shrink-0">
                  {formatBytes(t.size)}
                </span>
              </label>
            ))}

            {selected.size > 0 && (
              <div className="mt-4 pt-4 border-t border-zinc-800 space-y-3">
                {typeof rootAvailable === 'number' && (
                  <div className="grid gap-2 rounded-lg border border-zinc-800 bg-zinc-950/40 p-3 text-xs sm:grid-cols-2">
                    <div>
                      <span className="block text-zinc-500">Root free now</span>
                      <strong className="mt-0.5 block font-mono text-zinc-200">{formatBytes(rootAvailable)}</strong>
                    </div>
                    <div>
                      <span className="block text-zinc-500">After selected cleanup, up to</span>
                      <strong className="mt-0.5 block font-mono text-emerald-300">{formatBytes(rootAvailable + rootSelected)}</strong>
                    </div>
                    <p className="text-[10px] text-zinc-600 sm:col-span-2">
                      Estimate only; actual reclaimed space is measured after execution.
                    </p>
                  </div>
                )}
                <label className="flex items-center gap-2 text-xs text-zinc-400 cursor-pointer">
                  <input
                    type="checkbox"
					disabled={maintenanceRunning || maintenanceStatusUnavailable}
                    checked={confirmed}
                    onChange={(e) => setConfirmed(e.target.checked)}
                    className="w-3.5 h-3.5 rounded border-zinc-600 bg-zinc-800 text-red-500"
                  />
                  I reviewed the selected scopes and understand cleanup is irreversible
                </label>
                <Button
                  onClick={handleCleanup}
					disabled={!confirmed || cleanupMutation.isPending || maintenanceRunning || maintenanceStatusUnavailable}
                  variant="destructive"
                  className="w-full"
                >
                  {cleanupMutation.isPending && <Loader2 className="w-4 h-4 mr-2 animate-spin" />}
						{maintenanceStatusUnavailable ? 'Checking host maintenance…' : maintenanceRunning ? `${hostActionLabel(actionStatus.data?.action)} in progress` : `Clean Selected (${formatBytes(totalSelected)})`}
                </Button>
              </div>
            )}
          </div>
        )}
      </CardContent>
    </Card>
  )
}

// ── Mounts Tab ───────────────────────────────────────────────────────────────

export function MountsTab() {
  const { data: mounts, isLoading, isFetching, isError, error, refetch } = useDiskMounts()

  return (
    <Card className="bg-zinc-900/80 border-zinc-800/80">
      <CardHeader className="pb-2 pt-3">
        <CardTitle className="text-zinc-200 text-xs font-semibold uppercase tracking-wider flex items-center gap-2">
          <Database className="w-3.5 h-3.5 text-cyan-400" />
          /etc/fstab Entries
        </CardTitle>
      </CardHeader>
      <CardContent>
        {isLoading ? (
          <Skeleton className="h-32 bg-zinc-800/50" />
        ) : isError ? (
          <DependencyRemediation title="Mount configuration could not be observed" summary="HServer did not return a successful /etc/fstab inventory. An unavailable inventory is not treated as an empty configuration." error={error?.message} retry={() => refetch()} retrying={isFetching} steps={['Retry mount detection.', 'Confirm the HServer service is reachable.', 'Verify the service account can read /etc/fstab.']} />
        ) : !mounts || mounts.length === 0 ? (
          <p className="text-zinc-500 text-sm py-4 text-center">No mount entries found</p>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-xs">
              <thead>
                <tr className="text-zinc-500 border-b border-zinc-800">
                  <th className="text-left py-1.5 px-2 font-medium">Device</th>
                  <th className="text-left py-1.5 px-2 font-medium">Mount Point</th>
                  <th className="text-left py-1.5 px-2 font-medium">FS Type</th>
                  <th className="text-left py-1.5 px-2 font-medium">Options</th>
                  <th className="text-center py-1.5 px-2 font-medium">Dump</th>
                  <th className="text-center py-1.5 px-2 font-medium">Pass</th>
                </tr>
              </thead>
              <tbody>
                {mounts.map((m, i) => (
                  <tr key={i} className="border-b border-zinc-800/30 hover:bg-zinc-800/20">
                    <td className="py-1.5 px-2 text-zinc-300 font-mono truncate max-w-[200px]" title={m.device}>{m.device}</td>
                    <td className="py-1.5 px-2 text-white font-mono">{m.mountPoint}</td>
                    <td className="py-1.5 px-2">
                      <Badge variant="outline" className="text-[10px] px-1.5 py-0">{m.fsType}</Badge>
                    </td>
                    <td className="py-1.5 px-2 text-zinc-400 font-mono text-[10px] max-w-[200px] truncate" title={m.options}>{m.options}</td>
                    <td className="py-1.5 px-2 text-center text-zinc-500">{m.dump}</td>
                    <td className="py-1.5 px-2 text-center text-zinc-500">{m.pass}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </CardContent>
    </Card>
  )
}

// ── Main Page ────────────────────────────────────────────────────────────────

export default function DiskManagement() {
  const [searchParams, setSearchParams] = useSearchParams()
  const requestedTab = searchParams.get('tab') as TabId | null
  const activeTab: TabId = requestedTab && TAB_IDS.has(requestedTab) ? requestedTab : 'overview'
  const [explorePath, setExplorePath] = useState('/')

  const selectTab = useCallback((tab: TabId) => {
    setSearchParams(tab === 'overview' ? {} : { tab })
  }, [setSearchParams])

  const handleExplore = useCallback((path: string) => {
    setExplorePath(path)
    selectTab('analysis')
  }, [selectTab])

  return (
    <div className="space-y-4">
      {/* Header */}
      <div>
        <h2 className="text-white text-xl font-bold flex items-center gap-2">
          <HardDrive className="w-5 h-5 text-purple-400" />
          Disk Management
        </h2>
        <p className="text-zinc-500 text-sm mt-0.5">
          Monitor disk health, analyze space usage, and clean up storage
        </p>
      </div>

      {/* Tabs */}
      <div className="flex items-center gap-1 bg-zinc-800/40 rounded-xl p-1 border border-zinc-700/30 w-fit">
        {TABS.map(tab => (
          <button
            key={tab.id}
            onClick={() => selectTab(tab.id)}
            className={`flex items-center gap-1.5 px-3 py-1.5 text-xs rounded-lg transition-all font-medium ${
              activeTab === tab.id
                ? 'bg-zinc-600/80 text-white shadow-sm'
                : 'text-zinc-500 hover:text-zinc-300 hover:bg-zinc-700/30'
            }`}
          >
            <tab.icon className="w-3.5 h-3.5" />
            {tab.label}
          </button>
        ))}
      </div>

      {/* Tab Content */}
      {activeTab === 'overview' && <OverviewTab onExplore={handleExplore} onCleanup={() => selectTab('cleanup')} />}
      {activeTab === 'analysis' && <SpaceAnalysisTab key={explorePath} initialPath={explorePath} />}
      {activeTab === 'cleanup' && <CleanupTab />}
      {activeTab === 'mounts' && <MountsTab />}
    </div>
  )
}
