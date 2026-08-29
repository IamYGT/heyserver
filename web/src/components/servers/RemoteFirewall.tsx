import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { AlertTriangle, ChevronDown, ChevronUp, Loader2, Lock, Plus, RefreshCw, ShieldCheck, Trash2 } from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { cn } from '@/lib/utils'
import { requireRemoteMutationReadiness, useRemoteMutationReadiness } from './remoteMutationReadiness'

interface FirewallRule { id: string; action: 'ACCEPT' | 'DROP'; protocol: 'tcp' | 'udp' | 'all'; port?: number; source?: string; comment?: string; managed: boolean; raw?: string }
interface FirewallInventory { backend: string; policy: string; persistence: string; rules: FirewallRule[]; revision: string; protected_sources: string[]; protected_ports: number[] }
interface ActionResult { id?: string; message: string }

export function RemoteFirewall({ nodeID, online, readAvailable, writeAvailable }: { nodeID: string; online: boolean; readAvailable: boolean; writeAvailable: boolean }) {
  const controlsReady = useRemoteMutationReadiness()
  const nodeBase = `/nodes/${encodeURIComponent(nodeID)}`
  const queryClient = useQueryClient()
  const [showSystem, setShowSystem] = useState(false)
  const [mutationConflict, setMutationConflict] = useState(false)
  const [form, setForm] = useState({ action: 'ACCEPT' as const as 'ACCEPT' | 'DROP', protocol: 'tcp' as const as 'tcp' | 'udp' | 'all', port: '443', source: '', comment: '' })
  const firewallQuery = useQuery<FirewallInventory>({ queryKey: ['managed-node-firewall', nodeID], queryFn: () => api.get(nodeBase + '/firewall'), enabled: online && readAvailable, refetchInterval: 30_000 })
  const refresh = () => queryClient.invalidateQueries({ queryKey: ['managed-node-firewall', nodeID] })
  const addMutation = useMutation<ActionResult, Error, { form: typeof form; revision: string }>({
    mutationFn: ({ form: submitted, revision }) => { requireRemoteMutationReadiness(controlsReady); return api.post(nodeBase + '/firewall', { ...submitted, port: submitted.protocol === 'all' || !submitted.port ? 0 : Number(submitted.port), revision }) },
    onSuccess: async (result) => { toast.success(result.message); setMutationConflict(false); setForm((current) => ({ ...current, source: '', comment: '' })); await refresh() },
    onError: async (error) => {
      if (error.message.includes('firewall rules changed on the server')) {
        setMutationConflict(true)
        toast.error('Firewall rules changed on remote server. Your form was preserved; review the current rules before adding it again.')
        await firewallQuery.refetch()
        return
      }
      toast.error(error.message || 'Firewall rule failed')
    },
  })
  const deleteMutation = useMutation<ActionResult, Error, { rule: FirewallRule; revision: string }>({
    mutationFn: ({ rule, revision }) => { requireRemoteMutationReadiness(controlsReady); return api.delete(`${nodeBase}/firewall/${rule.id}`, { revision }) },
    onSuccess: async (result) => { toast.success(result.message); setMutationConflict(false); await refresh() },
    onError: async (error) => {
      if (error.message.includes('firewall rules changed on the server')) setMutationConflict(true)
      toast.error(error.message)
      await firewallQuery.refetch()
    },
  })
  const rules = firewallQuery.data?.rules ?? []
  const managed = rules.filter((rule) => rule.managed)
  const system = rules.filter((rule) => !rule.managed)
  const port = Number(form.port)
  const valid = form.protocol === 'all' || form.port === '' || (Number.isInteger(port) && port >= 1 && port <= 65535)

  if (!online) return <FirewallUnavailable title="Managed node is offline" detail="Firewall inventory and mutations remain unavailable until the agent reconnects." />
  if (!readAvailable) return <FirewallUnavailable title="Firewall inventory is disabled" detail="This agent does not advertise firewall.read. Enable it in the node-local agent configuration, then restart the agent." />
  if (firewallQuery.isLoading) return <Card className="border-zinc-800 bg-zinc-900/80"><CardContent className="p-10 text-center"><Loader2 className="mx-auto size-5 animate-spin text-zinc-500" /><p className="mt-3 text-xs text-zinc-500">Loading observed firewall policy and rules…</p></CardContent></Card>
  if (firewallQuery.isError || !firewallQuery.data) return <Card className="border-red-500/30 bg-red-500/[0.07]"><CardContent className="flex flex-col gap-3 p-5 text-xs text-red-200 sm:flex-row sm:items-center sm:justify-between"><div><p className="font-semibold text-red-300">Firewall inventory is unavailable</p><p className="mt-1 break-words text-red-200/80">{firewallQuery.error instanceof Error ? firewallQuery.error.message : 'The managed agent did not return a complete firewall inventory.'}</p><p className="mt-2 text-[10px] text-zinc-500">No empty policy, rule count, or manageable state is inferred from this failure.</p></div><Button variant="outline" size="sm" disabled={firewallQuery.isFetching} onClick={() => firewallQuery.refetch()}>{firewallQuery.isFetching ? <Loader2 className="size-3.5 animate-spin" /> : <RefreshCw className="size-3.5" />} Retry inventory</Button></CardContent></Card>

  return <div className="space-y-4">
    {!writeAvailable ? <Card className="border-amber-500/30 bg-amber-500/[0.07]"><CardContent className="p-4 text-xs text-amber-200">Firewall inventory is available, but managed rule changes are read-only because <code>firewall.write</code> is disabled locally.</CardContent></Card> : null}
    <div className="grid gap-3 md:grid-cols-3">
      <Status label="Backend" value={firewallQuery.data?.backend ?? '—'} good={!!firewallQuery.data?.backend} />
      <Status label="INPUT policy" value={firewallQuery.data?.policy ?? '—'} good={firewallQuery.data?.policy === 'DROP'} />
      <Status label="Persistence" value={firewallQuery.data?.persistence ?? '—'} good={firewallQuery.data?.persistence === 'active'} />
    </div>
    <Card className="border-zinc-800 bg-zinc-900/80"><CardHeader className="gap-3"><div><CardTitle className="text-sm text-zinc-200">Add managed IPv4 rule</CardTitle><p className="mt-1 text-[10px] text-zinc-500">Rules are isolated in HSERVER-INPUT and persisted with netfilter-persistent</p></div>{mutationConflict && <div className="flex flex-col gap-2 rounded-lg border border-amber-500/30 bg-amber-500/[0.07] p-3 text-[10px] text-amber-200 sm:flex-row sm:items-center sm:justify-between"><span className="flex items-start gap-2"><AlertTriangle className="mt-0.5 size-3.5 shrink-0" />Firewall rules changed on remote server. Your form was preserved and no stale mutation was applied.</span><Button variant="outline" size="xs" disabled={firewallQuery.isFetching} onClick={async () => { await firewallQuery.refetch(); setMutationConflict(false) }}>{firewallQuery.isFetching ? <Loader2 className="size-3 animate-spin" /> : <RefreshCw className="size-3" />} Review current rules</Button></div>}</CardHeader><CardContent className="grid gap-3 lg:grid-cols-[130px_130px_120px_220px_minmax(0,1fr)_auto]">
      <label className="space-y-1"><span className="text-[10px] uppercase text-zinc-500">Action</span><select disabled={!writeAvailable} value={form.action} onChange={(e) => setForm({ ...form, action: e.target.value as 'ACCEPT' | 'DROP' })} className="h-9 w-full rounded-lg border border-zinc-800 bg-zinc-950 px-2 text-xs text-zinc-200 disabled:opacity-40"><option value="ACCEPT">Allow</option><option value="DROP">Drop</option></select></label>
      <label className="space-y-1"><span className="text-[10px] uppercase text-zinc-500">Protocol</span><select disabled={!writeAvailable} value={form.protocol} onChange={(e) => setForm({ ...form, protocol: e.target.value as 'tcp' | 'udp' | 'all' })} className="h-9 w-full rounded-lg border border-zinc-800 bg-zinc-950 px-2 text-xs text-zinc-200 disabled:opacity-40"><option value="tcp">TCP</option><option value="udp">UDP</option><option value="all">All</option></select></label>
      <label className="space-y-1"><span className="text-[10px] uppercase text-zinc-500">Port</span><input disabled={!writeAvailable || form.protocol === 'all'} value={form.protocol === 'all' ? '' : form.port} onChange={(e) => setForm({ ...form, port: e.target.value })} className="h-9 w-full rounded-lg border border-zinc-800 bg-zinc-950 px-3 font-mono text-xs text-zinc-200 disabled:opacity-40" placeholder="all" /></label>
      <label className="space-y-1"><span className="text-[10px] uppercase text-zinc-500">Source CIDR</span><input disabled={!writeAvailable} value={form.source} onChange={(e) => setForm({ ...form, source: e.target.value })} className="h-9 w-full rounded-lg border border-zinc-800 bg-zinc-950 px-3 font-mono text-xs text-zinc-200 disabled:opacity-40" placeholder="Any IPv4 source" /></label>
      <label className="space-y-1"><span className="text-[10px] uppercase text-zinc-500">Comment</span><input disabled={!writeAvailable} value={form.comment} onChange={(e) => setForm({ ...form, comment: e.target.value })} className="h-9 w-full rounded-lg border border-zinc-800 bg-zinc-950 px-3 text-xs text-zinc-200 disabled:opacity-40" placeholder="Why this rule exists" /></label>
      <div className="flex items-end"><Button disabled={!controlsReady || !online || !writeAvailable || !valid || !firewallQuery.data?.revision || mutationConflict || addMutation.isPending} onClick={() => { if (form.action === 'DROP' && !window.confirm('Add this DROP rule to remote server?')) return; if (firewallQuery.data?.revision) addMutation.mutate({ form, revision: firewallQuery.data.revision }) }}>{addMutation.isPending ? <Loader2 className="size-3.5 animate-spin" /> : <Plus className="size-3.5" />} Add</Button></div>
    </CardContent></Card>
    <div className="rounded-xl border border-amber-800/40 bg-amber-500/[0.06] px-4 py-3 text-xs text-amber-300"><strong>Local lockout policy:</strong> {(firewallQuery.data?.protected_sources?.length ?? 0) > 0 && (firewallQuery.data?.protected_ports?.length ?? 0) > 0 ? <>The agent rejects overlapping DROP rules for {firewallQuery.data?.protected_sources?.length} configured source{firewallQuery.data?.protected_sources?.length === 1 ? '' : 's'} on port{firewallQuery.data?.protected_ports?.length === 1 ? '' : 's'} {firewallQuery.data?.protected_ports?.join(', ')}.</> : <>No protected source-and-port pair is configured. Set both local firewall protection variables before adding DROP rules that could affect management access.</>}</div>
    <Card className="border-zinc-800 bg-zinc-900/80"><CardHeader className="flex-row items-start justify-between"><div><CardTitle className="text-sm text-zinc-200">HServer-managed rules</CardTitle><p className="mt-1 text-[10px] text-zinc-500">Only rules in this section can be deleted from the panel</p></div><Button variant="ghost" size="xs" disabled={!online || firewallQuery.isFetching} onClick={() => firewallQuery.refetch()}><RefreshCw className={cn('size-3', firewallQuery.isFetching && 'animate-spin')} /> Refresh</Button></CardHeader><CardContent className="p-0">
      {managed.length === 0 ? <div className="p-10 text-center"><ShieldCheck className="mx-auto size-8 text-zinc-700" /><p className="mt-3 text-sm text-zinc-400">No HServer-managed firewall rules.</p></div> : <div className="divide-y divide-zinc-800/70">{managed.map((rule) => <RuleRow key={rule.id} rule={rule} onDelete={() => { if (window.confirm(`Delete firewall rule ${rule.id}?`) && firewallQuery.data?.revision) deleteMutation.mutate({ rule, revision: firewallQuery.data.revision }) }} pending={deleteMutation.isPending} online={online} writeAvailable={writeAvailable} controlsReady={controlsReady} />)}</div>}
    </CardContent></Card>
    <Card className="border-zinc-800 bg-zinc-900/60"><button className="flex w-full items-center justify-between px-5 py-4 text-left" onClick={() => setShowSystem(!showSystem)}><span><span className="flex items-center gap-2 text-sm font-semibold text-zinc-300"><Lock className="size-4" /> Existing INPUT rules</span><span className="mt-1 block text-[10px] text-zinc-600">{system.length} externally managed rules · inventory only</span></span>{showSystem ? <ChevronUp className="size-4 text-zinc-500" /> : <ChevronDown className="size-4 text-zinc-500" />}</button>{showSystem && <CardContent className="space-y-1 border-t border-zinc-800 pt-4">{system.map((rule) => <pre key={rule.id} className="overflow-x-auto rounded bg-zinc-950 px-3 py-2 font-mono text-[10px] text-zinc-500">{rule.raw}</pre>)}</CardContent>}</Card>
  </div>
}

function FirewallUnavailable({ title, detail }: { title: string; detail: string }) { return <Card className="border-amber-500/30 bg-amber-500/[0.07]"><CardContent className="p-5 text-xs text-amber-200"><p className="font-semibold">{title}</p><p className="mt-1 text-amber-200/80">{detail}</p></CardContent></Card> }

function Status({ label, value, good }: { label: string; value: string; good: boolean }) { return <Card className="border-zinc-800 bg-zinc-900/80"><CardContent className="p-4"><p className="text-[10px] font-semibold uppercase tracking-wider text-zinc-500">{label}</p><p className={cn('mt-2 text-lg font-bold', good ? 'text-emerald-400' : 'text-zinc-300')}>{value}</p></CardContent></Card> }
function RuleRow({ rule, onDelete, pending, online, writeAvailable, controlsReady }: { rule: FirewallRule; onDelete: () => void; pending: boolean; online: boolean; writeAvailable: boolean; controlsReady: boolean }) { return <div className="flex flex-col gap-3 p-4 md:flex-row md:items-center"><span className={cn('w-fit rounded px-2 py-1 text-[10px] font-bold', rule.action === 'ACCEPT' ? 'bg-emerald-500/10 text-emerald-400' : 'bg-red-500/10 text-red-400')}>{rule.action === 'ACCEPT' ? 'ALLOW' : 'DROP'}</span><div className="min-w-0 flex-1"><p className="font-mono text-xs text-zinc-200">{rule.protocol.toUpperCase()} {rule.port || 'all ports'} · {rule.source || 'any source'}</p><p className="mt-1 text-[10px] text-zinc-600">{rule.comment || rule.id}</p></div><Button variant="ghost" size="icon-xs" title={writeAvailable ? 'Delete' : 'firewall.write is disabled'} disabled={!controlsReady || !online || !writeAvailable || pending} onClick={onDelete}><Trash2 className="size-3 text-red-400" /></Button></div> }
