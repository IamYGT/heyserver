import { useState, Suspense, lazy } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  RotateCcw, RefreshCw, Activity, Server, Cpu,
  Layers, FileCode, Settings, Shield,
  BarChart2, BookOpen, Zap, CheckCircle2,
  AlertTriangle,
} from 'lucide-react'
import { Card, CardContent } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Skeleton } from '@/components/ui/skeleton'
import { Button } from '@/components/ui/button'
import {
  Tooltip, TooltipContent, TooltipTrigger,
} from '@/components/ui/tooltip'
import { api } from '@/lib/api'
import { toast } from 'sonner'
import { cn } from '@/lib/utils'
import { DependencyRemediation } from '@/components/DependencyRemediation'
import type { PHPVersion, PHPPool } from '@/lib/types'

// ─── Lazy-loaded tabs ─────────────────────────────────────────────────────────

const PoolsTab      = lazy(() => import('./php/PoolsTab'))
const IniTab        = lazy(() => import('./php/IniTab'))
const ExtensionsTab = lazy(() => import('./php/ExtensionsTab'))
const MonitoringTab = lazy(() => import('./php/MonitoringTab'))
const LogsTab       = lazy(() => import('./php/LogsTab'))
const SecurityTab   = lazy(() => import('./php/SecurityTab'))

const EMPTY_PHP_VERSIONS: PHPVersion[] = []

// ─── Tab definitions ──────────────────────────────────────────────────────────

const TABS = [
  { id: 'pools',      label: 'Pools',      icon: Layers },
  { id: 'ini',        label: 'php.ini',    icon: Settings },
  { id: 'extensions', label: 'Extensions', icon: BookOpen },
  { id: 'monitoring', label: 'Monitoring', icon: BarChart2 },
  { id: 'logs',       label: 'Logs',       icon: FileCode },
  { id: 'security',   label: 'Security',   icon: Shield },
] as const

type TabId = typeof TABS[number]['id']

// ─── Version Selector Card ────────────────────────────────────────────────────

interface VersionCardProps {
  version: PHPVersion
  isSelected: boolean
  onSelect: () => void
}

type PHPVersionAction = 'test' | 'reload' | 'restart'

function VersionCard({ version, isSelected, onSelect }: VersionCardProps) {
  const queryClient = useQueryClient()

  const actionMutation = useMutation<{ message: string }, Error, PHPVersionAction>({
    mutationFn: (action) => api.post(`/php/versions/${encodeURIComponent(version.version)}/actions/${action}`),
    onSuccess: (receipt, action) => {
      queryClient.invalidateQueries({ queryKey: ['php'] })
      toast.success(receipt.message || `PHP ${version.version} FPM ${action} completed`)
    },
    onError: (error: Error, action) => toast.error(error.message || `Failed to ${action} PHP ${version.version}`),
  })

  const actions: Array<{
    action: PHPVersionAction
    label: string
    tooltip: string
    icon: typeof CheckCircle2
  }> = [
    { action: 'test', label: `Test PHP-FPM ${version.version}`, tooltip: `Test PHP-FPM ${version.version} configuration`, icon: CheckCircle2 },
    { action: 'reload', label: `Reload PHP-FPM ${version.version}`, tooltip: `Gracefully reload PHP-FPM ${version.version}`, icon: RefreshCw },
    { action: 'restart', label: `Restart PHP-FPM ${version.version}`, tooltip: `Restart PHP-FPM ${version.version}`, icon: RotateCcw },
  ]

  const isRunning = version.active
  const statusColor = isRunning ? 'bg-green-500' : 'bg-red-500'
  const statusLabel = isRunning ? 'Running' : 'Stopped'
  const statusBadge = isRunning
    ? 'bg-green-500/10 text-green-400 border-green-500/20'
    : 'bg-red-500/10 text-red-400 border-red-500/20'

  return (
    <button
      onClick={onSelect}
      className={cn(
        'relative rounded-xl border p-4 text-left transition-all group',
        isSelected
          ? 'border-blue-500 bg-blue-500/5 ring-1 ring-blue-500/30'
          : 'border-zinc-800 bg-zinc-900 hover:border-zinc-700 hover:bg-zinc-800/50'
      )}
    >
      <div className="flex items-start justify-between mb-3">
        <div className="flex items-center gap-2">
          <span className={cn('w-2 h-2 rounded-full flex-shrink-0', statusColor)} />
          <span className={cn('text-lg font-bold', isSelected ? 'text-white' : 'text-zinc-200')}>
            PHP {version.version}
          </span>
        </div>
        <span className={cn(
          'flex items-center gap-0.5 opacity-100 transition-opacity sm:opacity-0 sm:group-hover:opacity-100 sm:group-focus-within:opacity-100',
          actionMutation.isPending && 'sm:opacity-100',
        )}>
          {actions.map(({ action, label, tooltip, icon: Icon }) => {
            const pending = actionMutation.isPending && actionMutation.variables === action
            return (
              <Tooltip key={action}>
                <TooltipTrigger
                  render={<span
                    role="button"
                    aria-label={label}
                    aria-disabled={actionMutation.isPending}
                    tabIndex={actionMutation.isPending ? -1 : 0}
                    className={cn(
                      'z-10 flex h-7 w-7 items-center justify-center rounded text-zinc-400 transition-colors',
                      'hover:bg-blue-400/10 hover:text-blue-400 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blue-500/70',
                      actionMutation.isPending && !pending && 'opacity-40',
                    )}
                    onClick={event => {
                      event.stopPropagation()
                      if (!actionMutation.isPending) actionMutation.mutate(action)
                    }}
                    onKeyDown={event => {
                      if ((event.key === 'Enter' || event.key === ' ') && !actionMutation.isPending) {
                        event.preventDefault()
                        event.stopPropagation()
                        actionMutation.mutate(action)
                      }
                    }}
                  >
                    <Icon className={cn('h-3.5 w-3.5', pending && 'animate-spin')} />
                  </span>}
                />
                <TooltipContent>{tooltip}</TooltipContent>
              </Tooltip>
            )
          })}
        </span>
      </div>
      <div className="space-y-1.5">
        <Badge className={cn('text-xs border', statusBadge)}>
          {statusLabel}
        </Badge>
        <div className="flex items-center gap-3 text-xs text-zinc-500 mt-2">
          <span>{version.pool_count ?? 0} pool{(version.pool_count ?? 0) !== 1 ? 's' : ''}</span>
        </div>
      </div>
      {isSelected && (
        <span className="absolute bottom-0 left-4 right-4 h-0.5 bg-blue-500 rounded-full" />
      )}
    </button>
  )
}

// ─── Tab loading fallback ─────────────────────────────────────────────────────

function TabSkeleton() {
  return (
    <div className="space-y-3 mt-2">
      {Array.from({ length: 4 }).map((_, i) => (
        <Skeleton key={i} className="h-16 w-full bg-zinc-800 rounded-xl" />
      ))}
    </div>
  )
}

// ─── Summary Stats ────────────────────────────────────────────────────────────

function SummaryStats({ versions, pools }: { versions: PHPVersion[]; pools: PHPPool[] }) {
  const running  = versions.filter(v => v.active).length
  const active   = pools.filter(p => p.socket_exists).length
  const workers  = pools.reduce((sum, p) => sum + (p.pm_settings?.max_children ?? 0), 0)
  const healthScores = pools.flatMap(p => typeof p.healthScore === 'number' ? [p.healthScore] : [])
  const avgHealth = healthScores.length > 0
    ? Math.round(healthScores.reduce((sum, score) => sum + score, 0) / healthScores.length)
    : null

  return (
    <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
      {[
        { label: 'Versions Running', value: `${running}/${versions.length}`, icon: Server, color: 'text-green-400' },
        { label: 'Active Pools', value: `${active}/${pools.length}`, icon: Layers, color: 'text-blue-400' },
        { label: 'Worker Capacity', value: String(workers), icon: Cpu, color: 'text-purple-400' },
        { label: 'Avg Health', value: avgHealth === null ? '—' : `${avgHealth}/100`, icon: Activity, color: avgHealth === null ? 'text-zinc-500' : avgHealth >= 90 ? 'text-green-400' : avgHealth >= 70 ? 'text-amber-400' : 'text-red-400' },
      ].map(({ label, value, icon: Icon, color }) => (
        <Card key={label} className="bg-zinc-900 border-zinc-800 hover:border-zinc-700 hover:shadow-md transition-all duration-200">
          <CardContent className="py-3 px-4">
            <div className="flex items-center gap-2 mb-1">
              <Icon className={cn('w-3.5 h-3.5', color)} />
              <span className="text-zinc-500 text-xs">{label}</span>
            </div>
            <p className={cn('text-xl font-bold', color)}>{value}</p>
          </CardContent>
        </Card>
      ))}
    </div>
  )
}

// ─── Main Page ────────────────────────────────────────────────────────────────

export default function PHP() {
  const [requestedVersion, setRequestedVersion] = useState<string>('all')
  const [activeTab, setActiveTab] = useState<TabId>('pools')

  const versionsQuery = useQuery<PHPVersion[]>({
    queryKey: ['php', 'versions'],
    queryFn: () => api.get<PHPVersion[]>('/php/versions'),
    refetchInterval: 30_000,
  })
  const versions = versionsQuery.data ?? EMPTY_PHP_VERSIONS

  const poolsQuery = useQuery<PHPPool[]>({
    queryKey: ['php', 'pools'],
    queryFn: () => api.get<PHPPool[]>('/php/pools'),
    refetchInterval: 30_000,
  })
  const pools = poolsQuery.data ?? []

  const selectedVersion = versions.some(version => version.version === requestedVersion)
    ? requestedVersion
    : versions[0]?.version ?? 'all'

  const isLoading = versionsQuery.isLoading || poolsQuery.isLoading

  // Build domain list for ini tab
  const domainList = pools.map(p => ({ version: p.version, name: p.name }))

  // Effective version for tabs that need a single version
  const effectiveVersion = selectedVersion === 'all'
    ? versions[0]?.version ?? ''
    : selectedVersion

  return (
    <div className="space-y-5">
      {/* Header */}
      <div className="flex items-center justify-between flex-wrap gap-3">
        <div>
          <h2 className="text-white text-xl font-bold flex items-center gap-2">
            <Zap className="w-5 h-5 text-blue-400" />
            PHP Management
          </h2>
          <p className="text-zinc-500 text-sm mt-0.5">
            {isLoading
              ? 'Loading…'
              : versionsQuery.isError || poolsQuery.isError
                ? 'PHP inventory partially unavailable'
                : `${versions.length} PHP version${versions.length !== 1 ? 's' : ''} · ${pools.length} pools`}
          </p>
        </div>
        <div className="flex items-center gap-1.5 text-zinc-600">
          <RefreshCw className="w-3 h-3" />
          <span className="text-xs">Auto-refresh active</span>
        </div>
      </div>

      {poolsQuery.isError && activeTab !== 'pools' && (
        <Card className="border-red-500/25 bg-red-500/[0.05]">
          <CardContent className="flex flex-col gap-3 p-4 sm:flex-row sm:items-start sm:justify-between">
            <div className="flex min-w-0 items-start gap-3">
              <AlertTriangle className="mt-0.5 size-5 shrink-0 text-red-400" />
              <div>
                <p className="text-sm text-red-300">PHP pool inventory could not be loaded. Pool-dependent controls are paused.</p>
                <p className="mt-1 break-words font-mono text-xs text-red-400/70">{poolsQuery.error.message}</p>
              </div>
            </div>
            <Button type="button" size="sm" variant="outline" onClick={() => { void poolsQuery.refetch() }} disabled={poolsQuery.isFetching}>
              <RefreshCw className={`size-3.5 ${poolsQuery.isFetching ? 'animate-spin' : ''}`} /> Retry
            </Button>
          </CardContent>
        </Card>
      )}

      {/* Summary stats */}
      {!isLoading && !versionsQuery.isError && !poolsQuery.isError && versions.length > 0 && (
        <SummaryStats versions={versions} pools={pools} />
      )}

      {/* Version selector */}
      {versionsQuery.isLoading ? (
        <div className="grid grid-cols-3 gap-3">
          {Array.from({ length: 3 }).map((_, i) => (
            <Skeleton key={i} className="h-24 bg-zinc-800 rounded-xl" />
          ))}
        </div>
      ) : versionsQuery.isError ? (
        <DependencyRemediation
          title="PHP-FPM inventory is unavailable"
          summary="HServer could not inspect installed PHP-FPM versions. Version and pool controls remain paused until detection succeeds."
          state="unavailable"
          steps={[
            <>Run the packaged HServer doctor and inspect the HServer service logs.</>,
            <>Verify HServer can read <code>/etc/php/*/fpm</code> and execute the installed PHP-FPM binaries.</>,
            <>Check the relevant <code>php&lt;VERSION&gt;-fpm</code> systemd unit, then retry detection.</>,
          ]}
          error={versionsQuery.error.message}
          retry={() => { void versionsQuery.refetch() }}
          retrying={versionsQuery.isFetching}
        />
      ) : versions.length === 0 ? (
        <DependencyRemediation
          title="PHP-FPM is not installed"
          summary="No supported PHP-FPM installation was detected. HServer does not install runtimes automatically."
          state="not-configured"
          steps={[
            <>Install the required <code>php&lt;VERSION&gt;-fpm</code> package from the supported Ubuntu repositories.</>,
            <>Verify the version has an <code>/etc/php/&lt;VERSION&gt;/fpm</code> configuration directory.</>,
            <>Enable and start <code>php&lt;VERSION&gt;-fpm</code>, then retry detection.</>,
          ]}
          retry={() => { void versionsQuery.refetch() }}
          retrying={versionsQuery.isFetching}
        />
      ) : (
        <div className={cn(
          'grid gap-3',
          versions.length === 1 ? 'grid-cols-1 max-w-xs'
          : versions.length === 2 ? 'grid-cols-2'
          : 'grid-cols-2 sm:grid-cols-3'
        )}>
          {versions.map(v => (
            <VersionCard
              key={v.version}
              version={v}
              isSelected={selectedVersion === v.version}
              onSelect={() => setRequestedVersion(v.version)}
            />
          ))}
        </div>
      )}

      {/* Tab bar */}
      {!versionsQuery.isError && versions.length > 0 && (
        <div>
          <div className="flex items-center gap-1 border-b border-zinc-800 overflow-x-auto pb-0 no-scrollbar">
            {TABS.map(tab => {
              const Icon = tab.icon
              return (
                <button
                  key={tab.id}
                  onClick={() => setActiveTab(tab.id)}
                  className={cn(
                    'flex items-center gap-1.5 px-3 py-2.5 text-sm font-medium border-b-2 whitespace-nowrap transition-all duration-200',
                    activeTab === tab.id
                      ? 'border-blue-500 text-white bg-blue-500/5'
                      : 'border-transparent text-zinc-400 hover:text-zinc-200 hover:border-zinc-600 hover:bg-zinc-800/40'
                  )}
                >
                  <Icon className="w-3.5 h-3.5" />
                  {tab.label}
                </button>
              )
            })}
          </div>

          {/* Tab content */}
          <div className="mt-5">
            <Suspense fallback={<TabSkeleton />}>
              {activeTab === 'pools' && (
                <PoolsTab
                  selectedVersion={selectedVersion}
                  versions={versions}
                />
              )}
              {activeTab === 'ini' && (
                <IniTab
                  selectedVersion={effectiveVersion}
                  selectedDomain={pools.find(p => p.version === effectiveVersion)?.name ?? ''}
                  domains={domainList}
                />
              )}
              {activeTab === 'extensions' && (
                <ExtensionsTab selectedVersion={effectiveVersion} />
              )}
              {activeTab === 'monitoring' && (
                <MonitoringTab selectedVersion={effectiveVersion} pools={pools} />
              )}
              {activeTab === 'logs' && (
                <LogsTab selectedVersion={effectiveVersion} pools={pools} />
              )}
              {activeTab === 'security' && (
                <SecurityTab selectedVersion={effectiveVersion} pools={pools} />
              )}
            </Suspense>
          </div>
        </div>
      )}
    </div>
  )
}
