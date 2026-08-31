import { useEffect, useRef, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { useCurrentUser } from '@/hooks/useAuth'
import {
  Server,
  GitCommit,
  Calendar,
  Cpu,
  Shield,
  ExternalLink,
  RefreshCw,
  CheckCircle2,
  Clock,
  AlertTriangle,
  Download,
  Loader2,
} from 'lucide-react'
import { Button } from '@/components/ui/button'
import { buttonVariants } from '@/components/ui/button-variants'
import { displayReleaseVersion } from '@/lib/agentCompatibility'
import {
  releaseStagePresentation,
  releaseSignatureLabel,
  releaseUpdatePresentation,
  type ReleaseStage,
  type ReleaseStageResponse,
  type ReleaseUpdateStatus,
  type ReleaseUpdateTone,
} from '@/lib/releaseUpdates'

interface SystemInfo {
  os: string
  kernel: string
  hostname: string
  arch: string
  nginx: string
  php: string[]
  postgresql: string
  go_version: string
  node_version: string
  build_commit: string
  build_date: string
  panel_version: string
  project_url?: string
}

interface HealthStatus {
  status: string
  version: string
  uptime: number
  build_commit?: string
}

const releaseToneClasses: Record<ReleaseUpdateTone, string> = {
  available: 'border-blue-500/25 bg-blue-500/[0.06] text-blue-300',
  healthy: 'border-green-500/20 bg-green-500/[0.05] text-green-300',
  warning: 'border-amber-500/25 bg-amber-500/[0.05] text-amber-200',
  neutral: 'border-zinc-700 bg-zinc-900/50 text-zinc-300',
}

const signedManifestRequiredMessage = 'Signed manifest required for installation'

function formatUptime(secs: number): string {
  const d = Math.floor(secs / 86400)
  const h = Math.floor((secs % 86400) / 3600)
  const m = Math.floor((secs % 3600) / 60)
  const parts: string[] = []
  if (d > 0) parts.push(`${d}d`)
  if (h > 0) parts.push(`${h}h`)
  parts.push(`${m}m`)
  return parts.join(' ')
}

function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return 'Unknown'
  const units = ['B', 'KiB', 'MiB', 'GiB']
  const index = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1)
  return `${(bytes / 1024 ** index).toFixed(index === 0 ? 0 : 1)} ${units[index]}`
}

interface InfoRowProps {
  label: string
  value?: string | string[]
  mono?: boolean
}

function InfoRow({ label, value, mono }: InfoRowProps) {
  if (!value || (Array.isArray(value) && value.length === 0)) return null
  return (
    <div className="flex items-start justify-between py-2.5 border-b border-zinc-800/60 last:border-0 gap-4">
      <span className="text-zinc-400 text-sm shrink-0">{label}</span>
      {Array.isArray(value) ? (
        <div className="flex flex-col items-end gap-0.5">
          {value.map((v) => (
            <span key={v} className={`text-sm text-white ${mono ? 'font-mono' : ''}`}>{v}</span>
          ))}
        </div>
      ) : (
        <span className={`text-sm text-white text-right ${mono ? 'font-mono' : ''}`}>{value}</span>
      )}
    </div>
  )
}

function LoadError({ label, onRetry, retrying }: { label: string; onRetry: () => void; retrying: boolean }) {
  return (
    <div className="my-4 flex items-center justify-between gap-4 rounded-lg border border-amber-500/20 bg-amber-500/5 p-4">
      <div className="flex items-center gap-2 text-sm text-amber-200">
        <AlertTriangle className="size-4 shrink-0 text-amber-400" />
        {label}
      </div>
      <Button type="button" variant="outline" size="sm" onClick={onRetry} disabled={retrying} className="border-amber-500/30 text-amber-100">
        <RefreshCw className={`mr-2 size-3.5 ${retrying ? 'animate-spin' : ''}`} />
        Retry
      </Button>
    </div>
  )
}

export default function About() {
  const queryClient = useQueryClient()
  const { data: currentUser } = useCurrentUser()
  const canManage = currentUser?.role === 'admin'
  const [upgradeConfirmed, setUpgradeConfirmed] = useState(false)
  const previousStageStatus = useRef<string | undefined>(undefined)
  const {
    data: info,
    isLoading: infoLoading,
    isError: infoError,
    refetch: refetchInfo,
    isFetching: infoFetching,
  } = useQuery<SystemInfo>({
    queryKey: ['system-info'],
    queryFn: () => api.get<SystemInfo>('/system/info'),
    staleTime: 60_000,
  })

  const {
    data: health,
    isLoading: healthLoading,
    isError: healthError,
    refetch: refetchHealth,
    isFetching: healthFetching,
  } = useQuery<HealthStatus>({
    queryKey: ['health'],
    queryFn: () => api.get<HealthStatus>('/health'),
    staleTime: 30_000,
  })

  const {
    data: releaseUpdate,
    isLoading: releaseUpdateLoading,
    isError: releaseUpdateError,
    refetch: refetchReleaseUpdate,
    isFetching: releaseUpdateFetching,
  } = useQuery<ReleaseUpdateStatus>({
    queryKey: ['release-update'],
    queryFn: () => api.get<ReleaseUpdateStatus>('/system/update'),
    staleTime: 15 * 60_000,
    retry: false,
  })

  const {
    data: releaseStageResponse,
    refetch: refetchReleaseStage,
    isFetching: releaseStageFetching,
    isError: releaseStageError,
  } = useQuery<ReleaseStageResponse>({
    queryKey: ['release-update-stage'],
    queryFn: () => api.get<ReleaseStageResponse>('/system/update/stage'),
    enabled: canManage,
    retry: false,
    refetchInterval: (query) => {
      const status = query.state.data?.stage?.status
      return status === 'scheduled' || status === 'running' ? 2_000 : false
    },
  })
  const signedManifestReady = releaseUpdate?.signature_status === 'verified'

  const stageMutation = useMutation({
    mutationFn: () => {
      if (!signedManifestReady) throw new Error(signedManifestRequiredMessage)
      return api.post<ReleaseStage>('/system/update/stage')
    },
    onSuccess: (stage) => {
      queryClient.setQueryData<ReleaseStageResponse>(['release-update-stage'], { stage })
      setUpgradeConfirmed(false)
      toast.success(`${displayReleaseVersion(stage.version)} downloaded and verified`)
    },
    onError: (error) => toast.error(error instanceof Error ? error.message : 'Release staging failed'),
  })

  const installMutation = useMutation({
    mutationFn: (stage: ReleaseStage) => {
      if (!signedManifestReady) throw new Error(signedManifestRequiredMessage)
      return api.post<ReleaseStage>('/system/update/install', {
        stage_id: stage.id,
        version: stage.version,
        confirmed: true,
      })
    },
    onSuccess: (stage) => {
      queryClient.setQueryData<ReleaseStageResponse>(['release-update-stage'], { stage })
      setUpgradeConfirmed(false)
      toast.success(`${displayReleaseVersion(stage.version)} upgrade scheduled`)
    },
    onError: (error) => toast.error(error instanceof Error ? error.message : 'Upgrade could not be scheduled'),
  })

  const version = info?.panel_version || health?.version
  const commit = info?.build_commit ?? health?.build_commit
  const buildDate = info?.build_date
  const refreshing = infoFetching || healthFetching || releaseUpdateFetching || releaseStageFetching
  const releasePresentation = releaseUpdate ? releaseUpdatePresentation(releaseUpdate) : undefined
  const releaseStage = releaseStageResponse?.stage ?? null
  const matchingStage = releaseStage && releaseStage.version === releaseUpdate?.latest_version ? releaseStage : null
  const stagePresentation = releaseStage ? releaseStagePresentation(releaseStage) : null
  const releaseStageActive = releaseStage?.status === 'scheduled' || releaseStage?.status === 'running'
  const upgradeReady = matchingStage?.status === 'staged' || matchingStage?.status === 'failed'

  useEffect(() => {
    if (!releaseStage) {
      previousStageStatus.current = undefined
      return
    }
    const status = releaseStage.status
    const previous = previousStageStatus.current
    previousStageStatus.current = status
    if (!previous || previous === status) return
    if (status === 'completed') {
      toast.success(`${displayReleaseVersion(releaseStage.version)} upgrade completed`)
      void queryClient.invalidateQueries({ queryKey: ['system-info'] })
      void queryClient.invalidateQueries({ queryKey: ['health'] })
      void queryClient.invalidateQueries({ queryKey: ['release-update'] })
    } else if (status === 'failed') {
      toast.error(`${displayReleaseVersion(releaseStage.version)} upgrade failed; inspect the Heyserver service journal`)
    }
  }, [queryClient, releaseStage])

  function refreshAll() {
    void refetchInfo()
    void refetchHealth()
    void refetchReleaseUpdate()
    if (canManage) void refetchReleaseStage()
  }

  return (
    <div className="max-w-3xl mx-auto space-y-6 p-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold text-white">About</h1>
          <p className="text-zinc-400 text-sm mt-1">Heyserver Panel — system information and build details</p>
        </div>
        <Button
          variant="outline"
          size="sm"
          onClick={refreshAll}
          disabled={refreshing}
          className="border-zinc-700 text-zinc-300 hover:bg-zinc-800 hover:text-white"
        >
          <RefreshCw className={`w-4 h-4 mr-2 ${refreshing ? 'animate-spin' : ''}`} />
          Refresh
        </Button>
      </div>

      {/* Panel identity */}
      <div className="rounded-xl border border-zinc-800 bg-zinc-900/50 overflow-hidden">
        <div className="flex items-center gap-4 p-6 border-b border-zinc-800">
          <div className="w-14 h-14 rounded-xl bg-blue-600/10 border border-blue-500/20 flex items-center justify-center shrink-0">
            <Server className="w-7 h-7 text-blue-400" />
          </div>
          <div>
            <h2 className="text-xl font-semibold text-white">Heyserver Panel</h2>
            <p className="text-zinc-400 text-sm mt-0.5">
              Self-hosted server management — single control-plane binary with optional host integrations
            </p>
          </div>
          <div className="ml-auto flex items-center gap-2">
            <span className={`inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-xs font-medium ${health?.status === 'ok' ? 'border-green-500/20 bg-green-500/10 text-green-400' : healthError ? 'border-amber-500/20 bg-amber-500/10 text-amber-300' : 'border-zinc-700 bg-zinc-800 text-zinc-300'}`}>
              {health?.status === 'ok' ? <CheckCircle2 className="w-3 h-3" /> : healthError ? <AlertTriangle className="w-3 h-3" /> : <Clock className="w-3 h-3" />}
              {version ? displayReleaseVersion(version) : healthLoading ? 'Loading version' : 'Version unavailable'}
            </span>
          </div>
        </div>

        <div className="p-6 space-y-0">
          {commit && commit !== 'dev' && (
            <InfoRow label="Build commit" value={commit} mono />
          )}
          {buildDate && buildDate !== 'unknown' && (
            <InfoRow label="Build date" value={buildDate} />
          )}
          {health && (
            <div className="flex items-start justify-between py-2.5 border-b border-zinc-800/60 gap-4">
              <span className="text-zinc-400 text-sm shrink-0 flex items-center gap-1.5">
                <Clock className="w-3.5 h-3.5" />
                Panel uptime
              </span>
              <span className="text-sm text-white">{formatUptime(health.uptime)}</span>
            </div>
          )}
          <InfoRow label="License" value="Apache License 2.0" />
          <InfoRow label="Project" value="Community-maintained open-source software" />
        </div>
      </div>

      {/* Release discovery */}
      <div className="rounded-xl border border-zinc-800 bg-zinc-900/50 overflow-hidden">
        <div className="flex items-center justify-between gap-3 px-6 py-4 border-b border-zinc-800">
          <div className="flex items-center gap-3">
            <RefreshCw className="w-4 h-4 text-zinc-400" />
            <div>
              <h3 className="text-sm font-medium text-white">Release updates</h3>
              <p className="mt-0.5 text-xs text-zinc-500">Provider-neutral manifest discovery; no silent download, installation, or restart</p>
            </div>
          </div>
          <Button type="button" variant="ghost" size="sm" onClick={() => void refetchReleaseUpdate()} disabled={releaseUpdateFetching}>
            <RefreshCw className={`mr-2 size-3.5 ${releaseUpdateFetching ? 'animate-spin' : ''}`} />
            Check now
          </Button>
        </div>
        <div className="p-6">
          {releaseUpdateLoading ? (
            <div className="py-4 text-center text-sm text-zinc-500">Checking the configured release manifest…</div>
          ) : releaseUpdateError ? (
            <LoadError label="Release discovery API is unavailable." onRetry={() => void refetchReleaseUpdate()} retrying={releaseUpdateFetching} />
          ) : releaseUpdate && releasePresentation ? (
            <div className="space-y-4">
              <div className={`rounded-lg border p-4 ${releaseToneClasses[releasePresentation.tone]}`}>
                <div className="flex items-start gap-3">
                  {releasePresentation.tone === 'healthy' ? <CheckCircle2 className="mt-0.5 size-4 shrink-0" /> : releasePresentation.tone === 'warning' ? <AlertTriangle className="mt-0.5 size-4 shrink-0" /> : <RefreshCw className="mt-0.5 size-4 shrink-0" />}
                  <div>
                    <p className="text-sm font-semibold">{releasePresentation.title}</p>
                    <p className="mt-1 text-xs opacity-80">{releaseUpdate.message}</p>
                  </div>
                </div>
              </div>
              <div className="space-y-0">
                <InfoRow label="Current release" value={displayReleaseVersion(releaseUpdate.current_version)} mono />
                {releaseUpdate.latest_version && <InfoRow label="Latest release" value={displayReleaseVersion(releaseUpdate.latest_version)} mono />}
                <InfoRow label="Platform" value={releaseUpdate.platform} mono />
                <InfoRow label="Manifest trust" value={releaseSignatureLabel(releaseUpdate.signature_status)} />
                {releaseUpdate.artifact?.sha256 && <InfoRow label="SHA-256" value={releaseUpdate.artifact.sha256} mono />}
                {canManage && !signedManifestReady && <p className="mt-3 rounded-lg border border-amber-500/20 bg-amber-500/5 p-3 text-xs text-amber-200">{signedManifestRequiredMessage}</p>}
              </div>
              {canManage && releaseStageError && !releaseStageActive && (
                <LoadError label="Verified upgrade stage status is unavailable." onRetry={() => void refetchReleaseStage()} retrying={releaseStageFetching} />
              )}
              {canManage && releaseStage && stagePresentation && (
                <div className={`rounded-lg border p-4 ${releaseToneClasses[stagePresentation.tone]}`}>
                  <div className="flex items-start gap-3">
                    {releaseStage.status === 'completed' ? <CheckCircle2 className="mt-0.5 size-4 shrink-0" /> : releaseStageActive ? <Loader2 className="mt-0.5 size-4 shrink-0 animate-spin" /> : <Download className="mt-0.5 size-4 shrink-0" />}
                    <div className="min-w-0 flex-1">
                      <p className="text-sm font-semibold">{stagePresentation.title}</p>
                      <p className="mt-1 text-xs opacity-80">{releaseStage.status_detail || stagePresentation.detail}</p>
                      <div className="mt-3 grid gap-1 text-xs opacity-80 sm:grid-cols-2">
                        <span>Release: <span className="font-mono">{displayReleaseVersion(releaseStage.version)}</span></span>
                        <span>Archive: {formatBytes(releaseStage.size_bytes)}</span>
                        <span className="break-all font-mono sm:col-span-2">SHA-256: {releaseStage.sha256}</span>
                      </div>
                    </div>
                  </div>
                </div>
              )}
              {releaseUpdate.update_available && releaseUpdate.artifact && (
                <div className="space-y-4 rounded-lg border border-blue-500/20 bg-blue-500/[0.04] p-4">
                  <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
                    <p className="text-xs leading-relaxed text-zinc-400">
                      {canManage
                        ? 'Stage downloads the archive on this server, verifies its declared size and SHA-256, rejects unsafe tar paths, and confirms the ELF architecture. It does not restart Heyserver.'
                        : 'An administrator can download and verify this release on the server before installation.'}
                    </p>
                    <div className="flex shrink-0 flex-wrap gap-2">
                      {releaseUpdate.release_notes_url && <a href={releaseUpdate.release_notes_url} target="_blank" rel="noopener noreferrer" className={buttonVariants({ variant: 'outline', size: 'sm' })}><ExternalLink className="size-3.5" /> Release notes</a>}
                      <a href={releaseUpdate.artifact.url} target="_blank" rel="noopener noreferrer" className={buttonVariants({ variant: 'outline', size: 'sm' })}><ExternalLink className="size-3.5" /> Download archive</a>
                      {canManage && !matchingStage && (
                        <Button type="button" size="sm" onClick={() => stageMutation.mutate()} disabled={stageMutation.isPending || releaseStageActive || !signedManifestReady}>
                          {stageMutation.isPending ? <Loader2 className="size-3.5 animate-spin" /> : <Download className="size-3.5" />}
                          Stage & verify
                        </Button>
                      )}
                    </div>
                  </div>

                  {canManage && matchingStage && upgradeReady && (
                    <div className="space-y-3 border-t border-blue-500/15 pt-4">
                      <label className="flex cursor-pointer items-start gap-3 text-xs leading-relaxed text-zinc-300">
                        <input
                          type="checkbox"
                          checked={upgradeConfirmed}
                          onChange={(event) => setUpgradeConfirmed(event.target.checked)}
                          className="mt-0.5 size-4 rounded border-zinc-600 bg-zinc-900 accent-blue-500"
                        />
                        <span>I understand that Heyserver will restart, may be briefly unavailable, and the packaged installer will automatically restore the previous binary and database snapshot if the new release fails its health check.</span>
                      </label>
                      <div className="flex justify-end">
                        <Button
                          type="button"
                          size="sm"
                          disabled={!upgradeConfirmed || installMutation.isPending || !signedManifestReady}
                          onClick={() => {
                            if (!upgradeConfirmed) return
                            if (!window.confirm(`Install ${displayReleaseVersion(matchingStage.version)} now? Heyserver will restart.`)) return
                            installMutation.mutate(matchingStage)
                          }}
                        >
                          {installMutation.isPending ? <Loader2 className="size-3.5 animate-spin" /> : <RefreshCw className="size-3.5" />}
                          Install verified release
                        </Button>
                      </div>
                    </div>
                  )}
                </div>
              )}
            </div>
          ) : null}
        </div>
      </div>

      {/* Server info */}
      <div className="rounded-xl border border-zinc-800 bg-zinc-900/50 overflow-hidden">
        <div className="flex items-center gap-3 px-6 py-4 border-b border-zinc-800">
          <Cpu className="w-4 h-4 text-zinc-400" />
          <h3 className="text-sm font-medium text-white">Server</h3>
        </div>
        <div className="px-6 py-2">
          {infoLoading ? (
            <div className="py-6 text-center text-zinc-500 text-sm">Loading server information…</div>
          ) : infoError ? (
            <LoadError label="Server information is unavailable." onRetry={() => void refetchInfo()} retrying={infoFetching} />
          ) : (
            <>
              <InfoRow label="Hostname" value={info?.hostname} />
              <InfoRow label="Operating system" value={info?.os} />
              <InfoRow label="Kernel" value={info?.kernel} mono />
              <InfoRow label="Architecture" value={info?.arch} />
            </>
          )}
        </div>
      </div>

      {/* Runtime info */}
      <div className="rounded-xl border border-zinc-800 bg-zinc-900/50 overflow-hidden">
        <div className="flex items-center gap-3 px-6 py-4 border-b border-zinc-800">
          <GitCommit className="w-4 h-4 text-zinc-400" />
          <h3 className="text-sm font-medium text-white">Runtime Versions</h3>
        </div>
        <div className="px-6 py-2">
          {infoLoading ? (
            <div className="py-6 text-center text-zinc-500 text-sm">Loading runtime information…</div>
          ) : infoError ? (
            <LoadError label="Runtime versions are unavailable." onRetry={() => void refetchInfo()} retrying={infoFetching} />
          ) : (
            <>
              <InfoRow label="Nginx" value={info?.nginx} mono />
              <InfoRow label="PHP" value={info?.php} mono />
              <InfoRow label="PostgreSQL" value={info?.postgresql} mono />
              <InfoRow label="Go" value={info?.go_version} mono />
              <InfoRow label="Node.js" value={info?.node_version} mono />
            </>
          )}
        </div>
      </div>

      {/* Security & compliance */}
      <div className="rounded-xl border border-zinc-800 bg-zinc-900/50 overflow-hidden">
        <div className="flex items-center gap-3 px-6 py-4 border-b border-zinc-800">
          <Shield className="w-4 h-4 text-zinc-400" />
          <h3 className="text-sm font-medium text-white">Security</h3>
        </div>
        <div className="px-6 py-2">
          <InfoRow label="Authentication" value="JWT HS256 + bcrypt + optional TOTP 2FA" />
          <InfoRow label="Authorization" value="RBAC — admin / manager / viewer" />
          <InfoRow label="Transport" value="TLS is provided by the installation reverse proxy" />
          <InfoRow label="Shell access" value="Writable PTY for authorized roles; remote access requires an explicit agent capability" />
        </div>
      </div>

      {/* Links */}
      <div className="rounded-xl border border-zinc-800 bg-zinc-900/50 overflow-hidden">
        <div className="flex items-center gap-3 px-6 py-4 border-b border-zinc-800">
          <Calendar className="w-4 h-4 text-zinc-400" />
          <h3 className="text-sm font-medium text-white">Links</h3>
        </div>
        <div className="p-4 flex flex-wrap gap-3">
          {info?.project_url && (
            <a
              href={info.project_url}
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-1.5 text-sm text-blue-400 hover:text-blue-300 transition-colors"
            >
              <ExternalLink className="w-3.5 h-3.5" />
              Source code
            </a>
          )}
          <a
            href="https://www.apache.org/licenses/LICENSE-2.0"
            target="_blank"
            rel="noopener noreferrer"
            className="inline-flex items-center gap-1.5 text-sm text-blue-400 hover:text-blue-300 transition-colors"
          >
            <ExternalLink className="w-3.5 h-3.5" />
            Apache License 2.0
          </a>
        </div>
      </div>
    </div>
  )
}
