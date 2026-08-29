import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Archive, CheckCircle2, FileArchive, Loader2, Play, RefreshCw, ShieldAlert } from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { cn } from '@/lib/utils'
import { requireRemoteMutationReadiness, useRemoteMutationReadiness } from './remoteMutationReadiness'

interface BackupFile { name: string; path: string; size: number; modified_at: string }
interface BackupPlan { id: string; name: string; service: string; timer: string; active: string; enabled: string; last_result: string; last_run: string; next_run: string; completed_at?: string; verified: boolean; total_size: number; files: BackupFile[] }
interface ActionResult { message: string }
function formatBytes(bytes: number) { if (!bytes) return '0 B'; const units = ['B', 'KiB', 'MiB', 'GiB']; const power = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1); return `${(bytes / 1024 ** power).toFixed(power > 1 ? 1 : 0)} ${units[power]}` }

export function RemoteBackups({ nodeID, online, readAvailable, runAvailable }: { nodeID: string; online: boolean; readAvailable: boolean; runAvailable: boolean }) {
  const controlsReady = useRemoteMutationReadiness()
  const nodeBase = `/nodes/${encodeURIComponent(nodeID)}`
  const queryClient = useQueryClient()
  const plansQuery = useQuery<BackupPlan[]>({ queryKey: ['managed-node-backups', nodeID], queryFn: () => api.get(nodeBase + '/backups'), enabled: online && readAvailable, refetchInterval: 60_000 })
  const runMutation = useMutation<ActionResult, Error, BackupPlan>({
    mutationFn: (plan) => { requireRemoteMutationReadiness(controlsReady); return api.post(`${nodeBase}/backups/${plan.id}/run`) },
    onSuccess: async (result) => { toast.success(result.message); await queryClient.invalidateQueries({ queryKey: ['managed-node-backups', nodeID] }) },
    onError: (error) => toast.error(error.message || 'Backup failed'),
  })
  const plans = plansQuery.data ?? []

  if (!online) return <BackupUnavailable title="Managed node is offline" detail="Backup plans, verification state, and manual runs remain unavailable until the agent reconnects." />
  if (!readAvailable) return <BackupUnavailable title="Backup inventory is disabled" detail="This agent does not advertise backup.read. Configure local backup plans and enable inventory, then restart the agent." />

  return <div className="space-y-4">
    {plansQuery.isSuccess && !runAvailable ? <Card className="border-amber-500/30 bg-amber-500/[0.07]"><CardContent className="p-4 text-xs text-amber-200">Backup inventory is available. Manual runs are read-only because <code>backup.run</code> is disabled locally.</CardContent></Card> : null}
    <div className="flex items-center justify-between"><div><p className="text-xs text-zinc-500">Locally configured systemd backup plans with bounded SHA-256 verification.</p></div><Button variant="ghost" size="xs" disabled={!online || !readAvailable || plansQuery.isFetching} onClick={() => plansQuery.refetch()}><RefreshCw className={cn('size-3', plansQuery.isFetching && 'animate-spin')} /> Refresh all</Button></div>
    {plansQuery.isLoading ? <div className="grid h-64 place-items-center"><Loader2 className="size-5 animate-spin text-zinc-500" /></div> : plansQuery.error ? <Card><CardContent className="p-5 text-xs text-red-400">{plansQuery.error.message}</CardContent></Card> : plans.length === 0 ? <Card><CardContent className="p-8 text-center text-xs text-zinc-600">No backup plans are configured on this agent.</CardContent></Card> : plans.map((plan) => <Card key={plan.id} className="border-zinc-800 bg-zinc-900/80"><CardHeader className="flex-row items-start justify-between gap-4"><div className="flex min-w-0 gap-3"><span className={cn('grid size-10 shrink-0 place-items-center rounded-xl', plan.verified ? 'bg-emerald-500/10 text-emerald-400' : 'bg-amber-500/10 text-amber-400')}>{plan.verified ? <CheckCircle2 className="size-5" /> : <ShieldAlert className="size-5" />}</span><div className="min-w-0"><CardTitle className="text-sm text-zinc-200">{plan.name}</CardTitle><p className="mt-1 truncate font-mono text-[10px] text-zinc-600">{plan.service}</p><div className="mt-2 flex flex-wrap gap-2"><Badge good={plan.active === 'active'}>{plan.active}</Badge><Badge good={plan.enabled === 'enabled'}>{plan.enabled}</Badge><Badge good={plan.last_result === 'success'}>last: {plan.last_result || 'unknown'}</Badge><Badge good={plan.verified}>{plan.verified ? 'SHA-256 verified' : 'verification failed'}</Badge></div></div></div><Button title={runAvailable ? undefined : 'backup.run is disabled'} disabled={!controlsReady || runMutation.isPending || !online || !runAvailable} onClick={() => { if (window.confirm(`Run ${plan.name} backup now?`)) runMutation.mutate(plan) }}>{runMutation.isPending && runMutation.variables?.id === plan.id ? <Loader2 className="size-3.5 animate-spin" /> : <Play className="size-3.5" />} Run now</Button></CardHeader><CardContent className="space-y-4"><div className="grid gap-2 text-[10px] sm:grid-cols-2 xl:grid-cols-4"><Info label="Completed" value={plan.completed_at || '—'} /><Info label="Last trigger" value={plan.last_run || '—'} /><Info label="Next trigger" value={plan.next_run || '—'} /><Info label="Current set" value={`${plan.files.length} files · ${formatBytes(plan.total_size)}`} /></div><div className="overflow-x-auto rounded-xl border border-zinc-800"><table className="w-full min-w-[680px] text-xs"><thead><tr className="bg-zinc-950/60 text-zinc-500"><th className="px-4 py-2 text-left">File</th><th className="px-3 py-2 text-right">Size</th><th className="px-4 py-2 text-left">Modified</th></tr></thead><tbody>{plan.files.length === 0 ? <tr><td colSpan={3} className="p-8 text-center text-zinc-600"><Archive className="mx-auto mb-2 size-6" />No backup files found.</td></tr> : plan.files.map((file) => <tr key={file.path} className="border-t border-zinc-800/70"><td className="px-4 py-2.5"><span className="flex items-center gap-2 font-mono text-zinc-300"><FileArchive className="size-3.5 text-violet-400" />{file.name}</span><span className="mt-0.5 block truncate font-mono text-[9px] text-zinc-700" title={file.path}>{file.path}</span></td><td className="px-3 py-2.5 text-right text-violet-400">{formatBytes(file.size)}</td><td className="px-4 py-2.5 text-zinc-500">{new Date(file.modified_at).toLocaleString()}</td></tr>)}</tbody></table></div></CardContent></Card>)}
  </div>
}

function BackupUnavailable({ title, detail }: { title: string; detail: string }) { return <Card className="border-amber-500/30 bg-amber-500/[0.07]"><CardContent className="p-5 text-xs text-amber-200"><p className="font-semibold">{title}</p><p className="mt-1 text-amber-200/80">{detail}</p></CardContent></Card> }

function Badge({ good, children }: { good: boolean; children: React.ReactNode }) { return <span className={cn('rounded px-2 py-1 text-[9px] font-semibold uppercase', good ? 'bg-emerald-500/10 text-emerald-400' : 'bg-amber-500/10 text-amber-400')}>{children}</span> }
function Info({ label, value }: { label: string; value: string }) { return <div className="rounded-lg border border-zinc-800 bg-zinc-950/50 px-3 py-2"><p className="uppercase tracking-wider text-zinc-600">{label}</p><p className="mt-1 truncate text-zinc-300" title={value}>{value}</p></div> }
