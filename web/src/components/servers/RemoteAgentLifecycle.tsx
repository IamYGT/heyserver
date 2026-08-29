import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { AlertTriangle, CheckCircle2, ExternalLink, Loader2, RefreshCw, RotateCcw, ShieldCheck } from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { cn } from '@/lib/utils'
import { releaseSignatureLabel, type ReleaseSignatureStatus } from '@/lib/releaseUpdates'
import { requireRemoteMutationReadiness, useRemoteMutationReadiness } from './remoteMutationReadiness'

const signedManifestRequiredMessage = 'Signed manifest required for installation'

export interface RemoteAgentUpdateStatus {
  release_status: 'not_configured' | 'unavailable' | 'healthy'
  signature_status: ReleaseSignatureStatus
  current_version: string
  latest_version?: string
  latest_version_state?: 'current' | 'behind' | 'ahead' | 'unknown'
  update_available: boolean
  platform: 'linux_amd64' | 'linux_arm64'
  release_notes_url?: string
  release_message: string
  release_checked_at: string
  operation: '' | 'upgrade' | 'rollback'
  operation_status: 'idle' | 'scheduled' | 'running' | 'completed' | 'failed'
  operation_version?: string
  operation_detail: string
  operation_updated_at?: string
  rollback_available: boolean
}

export function RemoteAgentLifecycle({ nodeID, serverLabel, online, readAvailable, actionAvailable }: { nodeID: string; serverLabel: string; online: boolean; readAvailable: boolean; actionAvailable: boolean }) {
  const controlsReady = useRemoteMutationReadiness()
  const queryClient = useQueryClient()
  const queryKey = ['managed-node-agent-update', nodeID]
  const statusQuery = useQuery<RemoteAgentUpdateStatus>({
    queryKey,
    queryFn: () => api.get(`/nodes/${encodeURIComponent(nodeID)}/agent-update`),
    enabled: online && readAvailable,
    refetchInterval: (query) => ['scheduled', 'running'].includes(query.state.data?.operation_status ?? '') ? 3_000 : 30_000,
  })
  const operationActive = statusQuery.data?.operation_status === 'scheduled' || statusQuery.data?.operation_status === 'running'
  const upgrade = useMutation<RemoteAgentUpdateStatus, Error, string>({
    mutationFn: (version) => {
      requireRemoteMutationReadiness(controlsReady)
      if (statusQuery.data?.signature_status !== 'verified') throw new Error(signedManifestRequiredMessage)
      return api.post(`/nodes/${encodeURIComponent(nodeID)}/agent-update/upgrade`, { version, confirmed: true })
    },
    onSuccess: async (status) => {
      queryClient.setQueryData(queryKey, status)
      toast.success(`Agent ${status.operation_version} upgrade scheduled`)
      await queryClient.invalidateQueries({ queryKey: ['managed-nodes'] })
    },
    onError: (error) => toast.error(error.message || 'Could not schedule agent upgrade'),
  })
  const rollback = useMutation<RemoteAgentUpdateStatus, Error>({
    mutationFn: () => { requireRemoteMutationReadiness(controlsReady); return api.post(`/nodes/${encodeURIComponent(nodeID)}/agent-update/rollback`, { confirmed: true }) },
    onSuccess: async (status) => {
      queryClient.setQueryData(queryKey, status)
      toast.success('Agent rollback scheduled')
      await queryClient.invalidateQueries({ queryKey: ['managed-nodes'] })
    },
    onError: (error) => toast.error(error.message || 'Could not schedule agent rollback'),
  })
  const pending = upgrade.isPending || rollback.isPending || operationActive
  const status = statusQuery.data

  const confirmUpgrade = () => {
    if (!status?.latest_version || status.signature_status !== 'verified') return
    const confirmed = window.confirm(`Upgrade the HServer agent on ${serverLabel} to ${status.latest_version}?\n\nThe agent service will restart. The verified lifecycle installer automatically restores the previous binary if the new service does not become active.`)
    if (confirmed) upgrade.mutate(status.latest_version)
  }
  const confirmRollback = () => {
    const confirmed = window.confirm(`Rollback the HServer agent on ${serverLabel} to its latest pre-upgrade snapshot?\n\nThe agent service will restart and may be briefly offline.`)
    if (confirmed) rollback.mutate()
  }

  return <Card className="border-zinc-800 bg-zinc-900/80">
    <CardHeader className="flex-row items-center justify-between gap-3">
      <div><CardTitle className="text-sm text-zinc-200">Agent lifecycle</CardTitle><p className="mt-1 text-[10px] text-zinc-500">Release discovery and installation are controlled by this server&apos;s local agent policy</p></div>
      <Button type="button" variant="ghost" size="xs" disabled={!online || !readAvailable || statusQuery.isFetching} onClick={() => statusQuery.refetch()}><RefreshCw className={cn('size-3', statusQuery.isFetching && 'animate-spin')} /> Refresh</Button>
    </CardHeader>
    <CardContent className="space-y-3">
      {!readAvailable ? <Notice tone="amber" icon={AlertTriangle}>This agent does not advertise <code>agent.update.read</code>. Lifecycle controls remain unavailable until the server opts in locally.</Notice>
        : !online ? <Notice tone="amber" icon={AlertTriangle}>The selected server is offline; its release and rollback state cannot be verified.</Notice>
          : statusQuery.isLoading ? <div className="flex items-center gap-2 py-3 text-xs text-zinc-500"><Loader2 className="size-4 animate-spin" /> Checking the agent&apos;s local release policy…</div>
            : statusQuery.error ? <Notice tone="red" icon={AlertTriangle}><div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between"><span>{statusQuery.error.message || 'Could not read the managed agent lifecycle state.'}</span><Button type="button" variant="outline" size="xs" disabled={statusQuery.isFetching} onClick={() => statusQuery.refetch()}><RefreshCw className={cn('size-3', statusQuery.isFetching && 'animate-spin')} /> Retry status</Button></div></Notice>
              : status && <>
                <div className="grid gap-2 md:grid-cols-2 xl:grid-cols-4">
                  <StatusCell label="Installed" value={status.current_version} detail={status.platform.replace('_', ' / ')} />
                  <StatusCell label="Available" value={status.latest_version || status.release_status.replace('_', ' ')} detail={status.release_message} />
                  <StatusCell label="Manifest trust" value={status.signature_status === 'verified' ? 'Verified' : status.signature_status.replace('_', ' ')} detail={releaseSignatureLabel(status.signature_status)} />
                  <StatusCell label="Rollback" value={status.rollback_available ? 'Ready' : 'Unavailable'} detail={status.rollback_available ? 'Latest verified pre-upgrade snapshot' : 'Created automatically after the first managed upgrade'} />
                </div>
                {status.release_status === 'not_configured' && <Notice tone="amber" icon={AlertTriangle}>Configure <code>HSERVER_AGENT_UPDATE_MANIFEST_URL</code> on this server to enable provider-neutral release discovery.</Notice>}
                {status.release_status === 'unavailable' && <Notice tone="red" icon={AlertTriangle}>{status.release_message}</Notice>}
                {status.signature_status !== 'verified' && <Notice tone="amber" icon={ShieldCheck}>{signedManifestRequiredMessage}</Notice>}
                {status.operation_status !== 'idle' && <Notice tone={status.operation_status === 'failed' ? 'red' : status.operation_status === 'completed' ? 'green' : 'blue'} icon={status.operation_status === 'failed' ? AlertTriangle : status.operation_status === 'completed' ? CheckCircle2 : Loader2} spin={status.operation_status === 'scheduled' || status.operation_status === 'running'}>
                  <strong className="capitalize">{status.operation || 'Lifecycle'} {status.operation_status}</strong><span className="ml-1 opacity-75">{status.operation_detail}</span>
                </Notice>}
                {!actionAvailable && <Notice tone="amber" icon={ShieldCheck}>Lifecycle status is read-only because this server does not advertise <code>agent.update.action</code>.</Notice>}
                <div className="flex flex-wrap items-center gap-2 border-t border-zinc-800 pt-3">
                  <Button type="button" size="sm" disabled={!controlsReady || !online || !actionAvailable || status.signature_status !== 'verified' || !status.update_available || !status.latest_version || pending} onClick={confirmUpgrade}>{upgrade.isPending || (status.operation === 'upgrade' && operationActive) ? <Loader2 className="size-4 animate-spin" /> : <RefreshCw className="size-4" />} {status.update_available && status.latest_version ? `Upgrade to ${status.latest_version}` : 'Agent is current'}</Button>
                  <Button type="button" variant="outline" size="sm" disabled={!controlsReady || !online || !actionAvailable || !status.rollback_available || pending} onClick={confirmRollback}>{rollback.isPending || (status.operation === 'rollback' && operationActive) ? <Loader2 className="size-4 animate-spin" /> : <RotateCcw className="size-4" />} Rollback agent</Button>
                  {status.release_notes_url && <a href={status.release_notes_url} target="_blank" rel="noreferrer" className="ml-auto inline-flex items-center gap-1 text-[10px] text-blue-400 hover:text-blue-300">Release notes <ExternalLink className="size-3" /></a>}
                </div>
              </>}
    </CardContent>
  </Card>
}

function StatusCell({ label, value, detail }: { label: string; value: string; detail: string }) {
  return <div className="rounded-xl border border-zinc-800 bg-zinc-950/40 p-3"><p className="text-[9px] font-semibold uppercase tracking-wider text-zinc-600">{label}</p><p className="mt-1 font-mono text-xs font-semibold text-zinc-200">{value}</p><p className="mt-1 line-clamp-2 text-[10px] leading-relaxed text-zinc-500" title={detail}>{detail}</p></div>
}

function Notice({ tone, icon: Icon, spin = false, children }: { tone: 'amber' | 'red' | 'green' | 'blue'; icon: typeof AlertTriangle; spin?: boolean; children: ReactNode }) {
  const tones = { amber: 'border-amber-500/20 bg-amber-500/5 text-amber-300', red: 'border-red-500/20 bg-red-500/5 text-red-300', green: 'border-emerald-500/20 bg-emerald-500/5 text-emerald-300', blue: 'border-blue-500/20 bg-blue-500/5 text-blue-300' }
  return <div className={cn('flex items-start gap-2 rounded-lg border p-3 text-xs', tones[tone])}><Icon className={cn('mt-0.5 size-3.5 shrink-0', spin && 'animate-spin')} /><div className="leading-relaxed">{children}</div></div>
}
