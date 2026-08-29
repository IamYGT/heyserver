import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ExternalLink, FileCog, Globe2, Loader2, Power, RefreshCw, Search } from 'lucide-react'
import { useNavigate } from 'react-router-dom'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { cn } from '@/lib/utils'
import { requireRemoteMutationReadiness, useRemoteMutationReadiness } from './remoteMutationReadiness'

interface RemoteDomain {
  name: string; aliases: string[]; config: string; enabled: boolean; ssl: boolean
  certificate_name?: string; root?: string; proxy_target?: string; kind: string
}

export function RemoteDomains({ nodeID, online, readAvailable, actionAvailable }: { nodeID: string; online: boolean; readAvailable: boolean; actionAvailable: boolean }) {
  const controlsReady = useRemoteMutationReadiness()
  const nodeBase = `/nodes/${encodeURIComponent(nodeID)}`
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [search, setSearch] = useState('')
  const domainsQuery = useQuery<RemoteDomain[]>({
    queryKey: ['managed-node-domains', nodeID],
    queryFn: () => api.get(nodeBase + '/domains'),
    enabled: online && readAvailable,
    refetchInterval: 60_000,
  })
  const actionMutation = useMutation<{ message: string }, Error, { config: string; action: 'enable' | 'disable' }>({
    mutationFn: ({ config, action }) => { requireRemoteMutationReadiness(controlsReady); return api.post(`${nodeBase}/domains/${encodeURIComponent(config)}/actions/${action}`) },
    onSuccess: async (result) => {
      toast.success(result.message)
      await queryClient.invalidateQueries({ queryKey: ['managed-node-domains', nodeID] })
      await queryClient.invalidateQueries({ queryKey: ['managed-node-nginx-configs', nodeID] })
    },
    onError: (error) => toast.error(error.message || 'Domain action failed'),
  })
  const filtered = useMemo(() => {
    const query = search.trim().toLowerCase()
    if (!query) return domainsQuery.data ?? []
    return (domainsQuery.data ?? []).filter((domain) => [domain.name, domain.config, ...domain.aliases].some((value) => value.toLowerCase().includes(query)))
  }, [domainsQuery.data, search])
  const enabledCount = (domainsQuery.data ?? []).filter((domain) => domain.enabled).length
  const sslCount = (domainsQuery.data ?? []).filter((domain) => domain.ssl).length

  if (!online) return <DomainUnavailable title="Managed node is offline" detail="Domain inventory and actions remain unavailable until the agent reconnects." />
  if (!readAvailable) return <DomainUnavailable title="Domain inventory is disabled" detail="This agent does not advertise domain.read. Enable domain inventory locally, then restart the agent." />

  return <div className="space-y-4">
    {domainsQuery.isSuccess && !actionAvailable ? <Card className="border-amber-500/30 bg-amber-500/[0.07]"><CardContent className="p-4 text-xs text-amber-200">Domain inventory is available. Enable/disable controls are read-only because <code>domain.action</code> is disabled locally.</CardContent></Card> : null}
    <Card className="border-zinc-800 bg-zinc-900/80">
    <CardHeader className="gap-4"><div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between"><div><CardTitle className="text-sm text-zinc-200">remote server domains</CardTitle><p className="mt-1 text-[10px] text-zinc-500">Nginx-backed sites with reversible enable/disable controls</p></div><div className="flex gap-2">{domainsQuery.isSuccess && <><span className="rounded-lg border border-zinc-800 bg-zinc-950/50 px-2.5 py-1.5 text-[10px] text-zinc-400">{enabledCount} enabled</span><span className="rounded-lg border border-zinc-800 bg-zinc-950/50 px-2.5 py-1.5 text-[10px] text-zinc-400">{sslCount} SSL</span></>}<Button variant="ghost" size="icon-xs" aria-label="Refresh domain inventory" disabled={domainsQuery.isFetching} onClick={() => domainsQuery.refetch()}><RefreshCw className={cn('size-3', domainsQuery.isFetching && 'animate-spin')} /></Button></div></div>
      <label className="flex items-center gap-2 rounded-lg border border-zinc-800 bg-zinc-950/60 px-3 py-2"><Search className="size-3.5 text-zinc-600" /><input value={search} onChange={(event) => setSearch(event.target.value)} placeholder="Search domain, alias or config…" className="w-full bg-transparent text-xs text-zinc-200 outline-none placeholder:text-zinc-600" /></label>
    </CardHeader>
    <CardContent className="p-0">
      {domainsQuery.isLoading ? <div className="p-12 text-center"><Loader2 className="mx-auto size-5 animate-spin text-zinc-500" /></div> : domainsQuery.error ? <p className="p-4 text-xs text-red-400">{domainsQuery.error.message}</p> : filtered.length === 0 ? <p className="p-12 text-center text-xs text-zinc-600">{search.trim() ? 'No matching domains.' : 'No Nginx-backed domains found.'}</p> : <div className="divide-y divide-zinc-800/60">{filtered.map((domain) => {
        const pending = actionMutation.isPending && actionMutation.variables?.config === domain.config
        return <div key={domain.config} className="flex flex-col gap-3 px-4 py-3 lg:flex-row lg:items-center lg:justify-between">
          <div className="flex min-w-0 items-start gap-3"><span className={cn('mt-0.5 grid size-9 shrink-0 place-items-center rounded-lg', domain.enabled ? 'bg-emerald-500/10 text-emerald-400' : 'bg-zinc-800 text-zinc-600')}><Globe2 className="size-4" /></span><div className="min-w-0"><div className="flex flex-wrap items-center gap-2"><p className="font-mono text-xs font-semibold text-zinc-200">{domain.name}</p><span className={cn('rounded px-1.5 py-0.5 text-[9px] font-semibold', domain.enabled ? 'bg-emerald-500/10 text-emerald-400' : 'bg-zinc-800 text-zinc-500')}>{domain.enabled ? 'ENABLED' : 'DISABLED'}</span>{domain.ssl && <span className="rounded bg-blue-500/10 px-1.5 py-0.5 text-[9px] font-semibold text-blue-400">SSL</span>}<span className="rounded bg-violet-500/10 px-1.5 py-0.5 text-[9px] uppercase text-violet-400">{domain.kind}</span></div><p className="mt-1 truncate font-mono text-[10px] text-zinc-500" title={domain.aliases.join(' · ')}>{domain.aliases.join(' · ') || domain.config}</p><p className="mt-1 truncate font-mono text-[9px] text-zinc-600">{domain.proxy_target || domain.root || domain.config}</p></div></div>
          <div className="flex shrink-0 flex-wrap gap-1"><Button variant="ghost" size="xs" onClick={() => navigate('/servers?tab=nginx')}><FileCog className="size-3" /> Config</Button>{domain.enabled && domain.name !== domain.config && <Button variant="ghost" size="icon-xs" title={`Open https://${domain.name}`} onClick={() => window.open(`https://${domain.name}`, '_blank', 'noopener,noreferrer')}><ExternalLink className="size-3" /></Button>}<Button variant="outline" size="xs" title={actionAvailable ? undefined : 'domain.action is disabled'} disabled={!controlsReady || !online || !actionAvailable || actionMutation.isPending} onClick={() => { const action = domain.enabled ? 'disable' : 'enable'; if (action === 'enable' || window.confirm(`Disable ${domain.name} on remote server?`)) actionMutation.mutate({ config: domain.config, action }) }}>{pending ? <Loader2 className="size-3 animate-spin" /> : <Power className="size-3" />}{domain.enabled ? 'Disable' : 'Enable'}</Button></div>
        </div>
      })}</div>}
    </CardContent>
    </Card>
  </div>
}

function DomainUnavailable({ title, detail }: { title: string; detail: string }) {
  return <Card className="border-amber-500/30 bg-amber-500/[0.07]"><CardContent className="p-5 text-xs text-amber-200"><p className="font-semibold">{title}</p><p className="mt-1 text-amber-200/80">{detail}</p></CardContent></Card>
}
