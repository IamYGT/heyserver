import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { CheckCircle2, Loader2, RefreshCw, RotateCcw, ShieldCheck } from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { cn } from '@/lib/utils'
import { requireRemoteMutationReadiness, useRemoteMutationReadiness } from './remoteMutationReadiness'

interface RemoteCertificate {
  name: string; domains: string[]; issuer: string; serial: string
  not_before: string; not_after: string; days_remaining: number; auto_renew: boolean
}

export function RemoteSSL({ nodeID, online, readAvailable, actionAvailable }: { nodeID: string; online: boolean; readAvailable: boolean; actionAvailable: boolean }) {
  const controlsReady = useRemoteMutationReadiness()
  const nodeBase = `/nodes/${encodeURIComponent(nodeID)}`
  const queryClient = useQueryClient()
  const certificatesQuery = useQuery<RemoteCertificate[]>({
    queryKey: ['managed-node-certificates', nodeID],
    queryFn: () => api.get(nodeBase + '/certificates'),
    enabled: online && readAvailable,
    refetchInterval: 5 * 60_000,
  })
  const actionMutation = useMutation<{ message: string }, Error, { name: string; action: 'check' | 'renew' }>({
    mutationFn: ({ name, action }) => { requireRemoteMutationReadiness(controlsReady); return api.post(`${nodeBase}/certificates/${encodeURIComponent(name)}/actions/${action}`) },
    onSuccess: async (result) => {
      toast.success(result.message)
      await queryClient.invalidateQueries({ queryKey: ['managed-node-certificates', nodeID] })
    },
    onError: (error) => toast.error(error.message || 'Certificate action failed'),
  })
  const certificates = certificatesQuery.data ?? []
  const expiring = certificates.filter((certificate) => certificate.days_remaining < 30).length

  if (!online) return <SSLUnavailable title="Managed node is offline" detail="Certificate inventory and actions remain unavailable until the agent reconnects." />
  if (!readAvailable) return <SSLUnavailable title="SSL certificate inventory is disabled" detail="This agent does not advertise ssl.read. Enable certificate inventory locally, then restart the agent." />

  return <div className="space-y-4">
    {certificatesQuery.isSuccess && !actionAvailable ? <Card className="border-amber-500/30 bg-amber-500/[0.07]"><CardContent className="p-4 text-xs text-amber-200">Certificate inventory is available. Check and renew controls are read-only because <code>ssl.action</code> is disabled locally.</CardContent></Card> : null}
    <Card className="border-zinc-800 bg-zinc-900/80"><CardHeader className="flex-row items-start justify-between gap-3"><div><CardTitle className="text-sm text-zinc-200">remote server SSL certificates</CardTitle><p className="mt-1 text-[10px] text-zinc-500">Certificate chain validation and Certbot renewal controls</p></div><div className="flex items-center gap-2">{certificatesQuery.isSuccess && <span className={cn('rounded-lg border px-2.5 py-1.5 text-[10px]', expiring ? 'border-amber-800/50 bg-amber-500/[0.06] text-amber-400' : 'border-zinc-800 bg-zinc-950/50 text-zinc-400')}>{certificates.length} certificates · {expiring} expiring</span>}<Button variant="ghost" size="icon-xs" aria-label="Refresh certificate inventory" disabled={certificatesQuery.isFetching} onClick={() => certificatesQuery.refetch()}><RefreshCw className={cn('size-3', certificatesQuery.isFetching && 'animate-spin')} /></Button></div></CardHeader><CardContent className="p-0">
    {certificatesQuery.isLoading ? <div className="p-12 text-center"><Loader2 className="mx-auto size-5 animate-spin text-zinc-500" /></div> : certificatesQuery.error ? <p className="p-4 text-xs text-red-400">{certificatesQuery.error.message}</p> : certificates.length === 0 ? <p className="p-12 text-center text-xs text-zinc-600">No Certbot certificates found.</p> : <div className="divide-y divide-zinc-800/60">{certificates.map((certificate) => {
      const pending = actionMutation.isPending && actionMutation.variables?.name === certificate.name
      const state = certificate.days_remaining < 0 ? 'expired' : certificate.days_remaining < 30 ? 'warning' : 'healthy'
      return <div key={certificate.name} className="flex flex-col gap-3 px-4 py-3 lg:flex-row lg:items-center lg:justify-between"><div className="flex min-w-0 items-start gap-3"><span className={cn('mt-0.5 grid size-9 shrink-0 place-items-center rounded-lg', state === 'healthy' ? 'bg-emerald-500/10 text-emerald-400' : state === 'warning' ? 'bg-amber-500/10 text-amber-400' : 'bg-red-500/10 text-red-400')}><ShieldCheck className="size-4" /></span><div className="min-w-0"><div className="flex flex-wrap items-center gap-2"><p className="font-mono text-xs font-semibold text-zinc-200">{certificate.name}</p><span className={cn('rounded px-1.5 py-0.5 text-[9px] font-semibold', state === 'healthy' ? 'bg-emerald-500/10 text-emerald-400' : state === 'warning' ? 'bg-amber-500/10 text-amber-400' : 'bg-red-500/10 text-red-400')}>{certificate.days_remaining} DAYS</span>{certificate.auto_renew && <span className="rounded bg-blue-500/10 px-1.5 py-0.5 text-[9px] text-blue-400">AUTO-RENEW</span>}</div><p className="mt-1 truncate text-[10px] text-zinc-500" title={certificate.domains.join(' · ')}>{certificate.domains.join(' · ')}</p><p className="mt-1 truncate text-[9px] text-zinc-600">Expires {new Date(certificate.not_after).toLocaleString()} · {certificate.issuer}</p></div></div><div className="flex shrink-0 gap-1"><Button variant="outline" size="xs" title={actionAvailable ? undefined : 'ssl.action is disabled'} disabled={!controlsReady || !online || !actionAvailable || actionMutation.isPending} onClick={() => actionMutation.mutate({ name: certificate.name, action: 'check' })}>{pending && actionMutation.variables?.action === 'check' ? <Loader2 className="size-3 animate-spin" /> : <CheckCircle2 className="size-3" />} Check</Button><Button variant="outline" size="xs" title={actionAvailable ? undefined : 'ssl.action is disabled'} disabled={!controlsReady || !online || !actionAvailable || actionMutation.isPending || !certificate.auto_renew} onClick={() => { if (window.confirm(`Run Certbot renewal check for ${certificate.name}? Certificates are renewed only when due.`)) actionMutation.mutate({ name: certificate.name, action: 'renew' }) }}>{pending && actionMutation.variables?.action === 'renew' ? <Loader2 className="size-3 animate-spin" /> : <RotateCcw className="size-3" />} Renew if due</Button></div></div>
    })}</div>}
  </CardContent></Card>
  </div>
}

function SSLUnavailable({ title, detail }: { title: string; detail: string }) {
  return <Card className="border-amber-500/30 bg-amber-500/[0.07]"><CardContent className="p-5 text-xs text-amber-200"><p className="font-semibold">{title}</p><p className="mt-1 text-amber-200/80">{detail}</p></CardContent></Card>
}
