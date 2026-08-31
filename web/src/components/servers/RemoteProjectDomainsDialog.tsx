import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Activity, Globe2, Loader2, LockKeyhole, Plus, RefreshCw, ShieldOff, Trash2 } from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { cn } from '@/lib/utils'
import { requireRemoteMutationReadiness, useRemoteMutationReadiness } from './remoteMutationReadiness'

interface RemoteProjectDomain {
  target_id: string
  domain: string
  host_port: number
  desired_host_port: number
  upstream: string
  status: 'active' | 'drifted'
  message: string
  tls_status: 'not_configured' | 'healthy' | 'expiring' | 'expired' | 'unavailable'
  tls_expires_at?: string
  tls_days_remaining?: number
  tls_message: string
  updated_at?: string
  enabled?: boolean
  revision?: string
}

interface RemoteProjectDomainEnsureReceipt {
  changed: boolean
  observation: RemoteProjectDomain
}

interface RemoteProjectDomainHealth {
  domain: string
  upstream: string
  status: 'healthy' | 'unhealthy' | 'unavailable'
  status_code?: number
  latency_ms: number
  message: string
  checked_at: string
}

interface RemoteProjectTarget {
  id: string
  name: string
  host_port?: number
}

type DomainOperation = 'inventory' | 'provision' | 'tls' | 'delete' | 'health'

function errorStatus(error: unknown): number | undefined {
  const status = (error as { status?: unknown } | null)?.status
  return typeof status === 'number' ? status : undefined
}

function errorText(error: unknown): string {
  return error instanceof Error ? error.message.trim().toLowerCase() : ''
}

function remoteDomainStatus(error: unknown): number | undefined {
  const status = errorStatus(error)
  if (status !== undefined) return status
  const detail = errorText(error)
  return Number(detail.match(/\b(?:http\s*)?(408|409|502|503|504)\b/)?.[1]) || undefined
}

function remoteDomainErrorMessage(error: unknown, operation: DomainOperation): string {
  const detail = errorText(error)
  const status = remoteDomainStatus(error)

  if (status === 409) return 'The remote project domain changed while you were working. Inventory was refreshed; review the current state before trying again.'
  if (status === 0 || /network|failed to fetch|connection refused/.test(detail)) return operation === 'inventory' ? 'Could not reach the remote project domain inventory.' : 'Could not reach the remote project domain service.'
  if (status === 408 || status === 504 || /timeout|timed out|deadline exceeded/.test(detail)) return operation === 'inventory' ? 'Remote project domain inventory timed out.' : 'The remote project domain operation timed out.'
  if (status === 405 || status === 501 || /unsupported|not supported|not enabled|disabled locally/.test(detail)) return operation === 'inventory' ? 'This agent does not support remote project domain inventory.' : 'This agent does not support remote project domain actions.'
  if (status === 502 || status === 503 || /unavailable|bad gateway|temporarily/.test(detail)) return operation === 'inventory' ? 'Remote project domain inventory is temporarily unavailable.' : 'Remote project domain service is temporarily unavailable.'
  if (status === 403) return 'Remote project domain actions are available to Admin users only.'

  switch (operation) {
    case 'inventory': return 'Could not load remote project domain inventory.'
    case 'provision': return 'Could not provision the remote project domain.'
    case 'health': return 'Could not probe the remote project upstream.'
    case 'tls': return 'Could not update managed TLS for the project domain.'
    case 'delete': return 'Could not remove the remote project domain.'
  }
}

function normalizedDomain(value: string): string {
  return value.trim().toLowerCase().replace(/\.$/, '')
}

function hasRevision(value: string | undefined): value is string {
  return typeof value === 'string' && /^[0-9a-f]{64}$/.test(value)
}

export function RemoteProjectDomainsDialog({
  nodeID,
  target,
  open,
  onOpenChange,
  online,
  readAvailable,
  actionAvailable,
}: {
  nodeID: string
  target: RemoteProjectTarget | null
  open: boolean
  onOpenChange: (open: boolean) => void
  online: boolean
  readAvailable: boolean
  actionAvailable: boolean
}) {
  const controlsReady = useRemoteMutationReadiness()
  const queryClient = useQueryClient()
  const [domain, setDomain] = useState('')
  const [email, setEmail] = useState('')
  const [provisionError, setProvisionError] = useState<string | null>(null)
  const [health, setHealth] = useState<Record<string, RemoteProjectDomainHealth>>({})
  const targetBase = `/nodes/${encodeURIComponent(nodeID)}/deploy/${encodeURIComponent(target?.id ?? '')}`
  const queryKey = ['managed-node-deploy-domains', nodeID, target?.id]
  const domainsQuery = useQuery<RemoteProjectDomain[]>({
    queryKey,
    queryFn: () => api.get(`${targetBase}/domains`),
    enabled: open && online && readAvailable && Boolean(target),
  })

  const refresh = async () => {
    await queryClient.invalidateQueries({ queryKey })
  }
  const ensureDomain = useMutation<RemoteProjectDomainEnsureReceipt, Error, { domain: string; expectedRevision: string }>({
    mutationFn: ({ domain: requestedDomain, expectedRevision }) => {
      requireRemoteMutationReadiness(controlsReady)
      return api.put<RemoteProjectDomainEnsureReceipt>(`${targetBase}/domains/${encodeURIComponent(requestedDomain)}`, { expected_revision: expectedRevision, confirmed: true })
    },
    retry: false,
    onSuccess: async (result) => {
      setProvisionError(null)
      setDomain('')
      toast.success(result.changed ? 'Remote project domain provisioned' : 'Remote project domain is already enabled')
      await refresh()
    },
    onError: (error: Error) => {
      const message = remoteDomainErrorMessage(error, 'provision')
      setProvisionError(message)
      toast.error(message)
      if (remoteDomainStatus(error) === 409) void refresh()
    },
  })
  const enableTLS = useMutation({
    mutationFn: (item: RemoteProjectDomain) => { requireRemoteMutationReadiness(controlsReady); return api.post<RemoteProjectDomain>(`${targetBase}/domains/${encodeURIComponent(item.domain)}/tls`, { email }) },
    onSuccess: async () => {
      toast.success('Managed TLS enabled on remote server')
      await refresh()
    },
    onError: (error: Error) => toast.error(remoteDomainErrorMessage(error, 'tls')),
  })
  const disableTLS = useMutation({
    mutationFn: (item: RemoteProjectDomain) => { requireRemoteMutationReadiness(controlsReady); return api.delete<RemoteProjectDomain>(`${targetBase}/domains/${encodeURIComponent(item.domain)}/tls`) },
    onSuccess: async () => {
      toast.success('Managed TLS disabled; certificate files were preserved')
      await refresh()
    },
    onError: (error: Error) => toast.error(remoteDomainErrorMessage(error, 'tls')),
  })
  const renewTLS = useMutation({
    mutationFn: (item: RemoteProjectDomain) => { requireRemoteMutationReadiness(controlsReady); return api.post<RemoteProjectDomain>(`${targetBase}/domains/${encodeURIComponent(item.domain)}/tls/renew`) },
    onSuccess: async () => {
      toast.success('Managed TLS renewal completed')
      await refresh()
    },
    onError: (error: Error) => toast.error(remoteDomainErrorMessage(error, 'tls')),
  })
  const removeDomain = useMutation({
    mutationFn: (item: RemoteProjectDomain) => { requireRemoteMutationReadiness(controlsReady); return api.delete(`${targetBase}/domains/${encodeURIComponent(item.domain)}`) },
    onSuccess: async (_result, item) => {
      setHealth((current) => {
        const next = { ...current }
        delete next[item.domain]
        return next
      })
      toast.success('Remote project domain removed')
      await refresh()
    },
    onError: (error: Error) => toast.error(remoteDomainErrorMessage(error, 'delete')),
  })
  const probeHealth = useMutation({
    mutationFn: (item: RemoteProjectDomain) => api.get<RemoteProjectDomainHealth>(`${targetBase}/domains/${encodeURIComponent(item.domain)}/health`),
    onSuccess: (result) => setHealth((current) => ({ ...current, [result.domain]: result })),
    onError: (error: Error) => toast.error(remoteDomainErrorMessage(error, 'health')),
  })

  const busy = ensureDomain.isPending || enableTLS.isPending || disableTLS.isPending || renewTLS.isPending || removeDomain.isPending
  const domains = domainsQuery.data ?? []

  const requestEnsure = () => {
    const requestedDomain = normalizedDomain(domain)
    if (!requestedDomain || !target?.host_port || ensureDomain.isPending || !controlsReady || !online || !actionAvailable) return
    const observed = domains.find((item) => normalizedDomain(item.domain) === requestedDomain)
    const expectedRevision = observed?.revision
    if (observed && !hasRevision(expectedRevision)) {
      const message = 'The current domain revision is unavailable. Refresh inventory before trying again.'
      setProvisionError(message)
      toast.error(message)
      return
    }
    if (!window.confirm(`Provision or ensure ${requestedDomain} on the managed server?`)) return
    setProvisionError(null)
    ensureDomain.mutate({ domain: requestedDomain, expectedRevision: expectedRevision ?? 'absent' })
  }

  return <Dialog open={open} onOpenChange={onOpenChange}>
    <DialogContent className="max-h-[88vh] max-w-3xl overflow-y-auto border-zinc-800 bg-zinc-950">
      <DialogHeader>
        <DialogTitle className="flex items-center gap-2 text-zinc-100"><Globe2 className="size-4 text-violet-400" /> Remote project domains · {target?.name}</DialogTitle>
      </DialogHeader>

      <div className="rounded-xl border border-zinc-800 bg-zinc-900/70 p-4 text-xs text-zinc-400">
        <p>The upstream port is owned by this server's local deploy plan, not browser input.</p>
        <p className="mt-1 font-mono text-[10px] text-zinc-600">Declared loopback: {target?.host_port ? `http://127.0.0.1:${target.host_port}` : 'host_port is not configured'}</p>
      </div>

      {!target ? <Notice tone="amber">No deploy target is selected.</Notice> : !online ? <Notice tone="amber">The managed server is offline.</Notice> : !readAvailable ? <Notice tone="amber">This agent does not advertise <code>deploy.domain.read</code>.</Notice> : domainsQuery.isLoading ? <div className="grid h-40 place-items-center"><Loader2 className="size-5 animate-spin text-zinc-500" /></div> : domainsQuery.error ? <Notice tone="red"><div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between"><span>{remoteDomainErrorMessage(domainsQuery.error, 'inventory')}</span><Button variant="outline" size="xs" disabled={domainsQuery.isFetching} onClick={() => domainsQuery.refetch()}><RefreshCw className={cn('size-3', domainsQuery.isFetching && 'animate-spin')} /> Retry inventory</Button></div></Notice> : <>
        {!actionAvailable && <Notice tone="amber">Domain inventory is read-only because <code>deploy.domain.action</code> is disabled locally.</Notice>}

        {actionAvailable && <div className="grid gap-3 rounded-xl border border-zinc-800 bg-zinc-900/50 p-4 sm:grid-cols-[1fr_1fr_auto] sm:items-end">
          <div className="space-y-1.5"><Label htmlFor="remote-project-domain">Domain</Label><Input id="remote-project-domain" value={domain} onChange={(event) => { setDomain(event.target.value); setProvisionError(null) }} placeholder="app.example.com" /></div>
          <div className="space-y-1.5"><Label htmlFor="remote-project-acme-email">ACME email (optional)</Label><Input id="remote-project-acme-email" type="email" value={email} onChange={(event) => setEmail(event.target.value)} placeholder="admin@example.com" /></div>
          <Button disabled={!controlsReady || !actionAvailable || busy || !domain.trim() || !target.host_port} onClick={requestEnsure}><Plus className="size-3.5" /> Ensure domain</Button>
        </div>}
        {provisionError && <Notice tone="red"><span role="alert">{provisionError}</span></Notice>}

        {domains.length === 0 ? <div className="rounded-xl border border-dashed border-zinc-800 p-8 text-center text-xs text-zinc-600">No Heyserver-owned domain mappings were observed for this target.</div> : <div className="space-y-3">{domains.map((item) => {
        const observedHealth = health[item.domain]
        const tlsConfigured = item.tls_status !== 'not_configured'
        const itemBusy = (enableTLS.isPending && enableTLS.variables?.domain === item.domain) || (disableTLS.isPending && disableTLS.variables?.domain === item.domain) || (renewTLS.isPending && renewTLS.variables?.domain === item.domain) || (removeDomain.isPending && removeDomain.variables?.domain === item.domain)
        return <div key={item.domain} className="rounded-xl border border-zinc-800 bg-zinc-900/60 p-4">
          <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
            <div className="min-w-0"><div className="flex flex-wrap items-center gap-2"><a className="truncate text-sm font-semibold text-zinc-100 hover:text-violet-300" href={`${tlsConfigured ? 'https' : 'http'}://${item.domain}`} target="_blank" rel="noreferrer">{item.domain}</a><StateBadge state={item.status} /><TLSBadge state={item.tls_status} /></div><p className="mt-1 font-mono text-[10px] text-zinc-600">{item.upstream}</p><p className={cn('mt-2 text-[10px]', item.status === 'active' ? 'text-zinc-500' : 'text-amber-300')}>{item.message}</p></div>
            <div className="flex shrink-0 flex-wrap gap-1.5"><Button variant="outline" size="xs" disabled={!online || probeHealth.isPending} onClick={() => probeHealth.mutate(item)}>{probeHealth.isPending && probeHealth.variables?.domain === item.domain ? <Loader2 className="size-3 animate-spin" /> : <Activity className="size-3" />} Health</Button>{!tlsConfigured ? <Button size="xs" disabled={!controlsReady || !online || !actionAvailable || itemBusy || item.status === 'drifted'} onClick={() => enableTLS.mutate(item)}><LockKeyhole className="size-3" /> Enable TLS</Button> : <><Button variant="outline" size="xs" disabled={!controlsReady || !online || !actionAvailable || itemBusy} onClick={() => renewTLS.mutate(item)}><RefreshCw className="size-3" /> Renew TLS</Button><Button variant="outline" size="xs" disabled={!controlsReady || !online || !actionAvailable || itemBusy} onClick={() => { if (window.confirm(`Disable managed TLS for ${item.domain}? Certificate files will be preserved.`)) disableTLS.mutate(item) }}><ShieldOff className="size-3" /> Disable TLS</Button></>}<Button variant="ghost" size="xs" className="text-red-400 hover:text-red-300" disabled={!controlsReady || !online || !actionAvailable || itemBusy} onClick={() => { if (window.confirm(`Remove ${item.domain} from this remote project?`)) removeDomain.mutate(item) }}><Trash2 className="size-3" /> Remove</Button></div>
          </div>
          <div className="mt-3 grid gap-2 text-[10px] sm:grid-cols-2"><Info label="TLS observation" value={item.tls_message || item.tls_status} /><Info label="Certificate expiry" value={item.tls_expires_at ? `${formatDate(item.tls_expires_at)} · ${item.tls_days_remaining ?? '—'} days` : 'not available'} /></div>
          {observedHealth && <div className={cn('mt-3 rounded-lg border px-3 py-2 text-[10px]', observedHealth.status === 'healthy' ? 'border-emerald-500/20 bg-emerald-500/5 text-emerald-300' : observedHealth.status === 'unhealthy' ? 'border-amber-500/20 bg-amber-500/5 text-amber-300' : 'border-red-500/20 bg-red-500/5 text-red-300')}>{observedHealth.status.toUpperCase()} · {observedHealth.status_code || 'no response'} · {observedHealth.latency_ms} ms — {observedHealth.message}</div>}
        </div>
        })}</div>}
      </>}
    </DialogContent>
  </Dialog>
}

function StateBadge({ state }: { state: RemoteProjectDomain['status'] }) {
  return <span className={cn('rounded px-2 py-1 text-[9px] font-semibold uppercase', state === 'active' ? 'bg-emerald-500/10 text-emerald-400' : 'bg-amber-500/10 text-amber-400')}>{state}</span>
}

function TLSBadge({ state }: { state: RemoteProjectDomain['tls_status'] }) {
  const healthy = state === 'healthy'
  const pending = state === 'not_configured'
  return <span className={cn('rounded px-2 py-1 text-[9px] font-semibold uppercase', healthy ? 'bg-blue-500/10 text-blue-400' : pending ? 'bg-zinc-800 text-zinc-500' : 'bg-amber-500/10 text-amber-400')}>TLS · {state.replace('_', ' ')}</span>
}

function Notice({ tone, children }: { tone: 'amber' | 'red'; children: React.ReactNode }) {
  return <div className={cn('rounded-xl border px-4 py-3 text-xs', tone === 'red' ? 'border-red-500/20 bg-red-500/5 text-red-300' : 'border-amber-500/20 bg-amber-500/5 text-amber-200')}>{children}</div>
}

function Info({ label, value }: { label: string; value: string }) {
  return <div className="rounded-lg border border-zinc-800 bg-zinc-950/50 px-3 py-2"><p className="uppercase tracking-wider text-zinc-600">{label}</p><p className="mt-1 truncate text-zinc-400" title={value}>{value}</p></div>
}

function formatDate(value: string) {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}
