import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { AlertTriangle, Clock, FileClock, Loader2, Pencil, Play, Plus, RefreshCw, Save, Trash2, X } from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { cn } from '@/lib/utils'
import { requireRemoteMutationReadiness, useRemoteMutationReadiness } from './remoteMutationReadiness'

interface CronJob { id: string; schedule: string; user: string; command: string; description?: string; enabled: boolean }
interface CronSource { path: string; entry_count: number; managed: boolean }
interface CronInventory { service: string; jobs: CronJob[]; sources: CronSource[]; revision: string }
interface CronResult { message: string; output?: string }

const emptyForm = { schedule: '0 * * * *', user: 'root', command: '', description: '', enabled: true }

export function RemoteCron({ nodeID, online, readAvailable, writeAvailable, runAvailable }: { nodeID: string; online: boolean; readAvailable: boolean; writeAvailable: boolean; runAvailable: boolean }) {
  const controlsReady = useRemoteMutationReadiness()
  const nodeBase = `/nodes/${encodeURIComponent(nodeID)}`
  const queryClient = useQueryClient()
  const [editing, setEditing] = useState<CronJob | null>(null)
  const [editingRevision, setEditingRevision] = useState('')
  const [saveConflict, setSaveConflict] = useState(false)
  const [form, setForm] = useState(emptyForm)
  const [runOutput, setRunOutput] = useState<{ name: string; output: string } | null>(null)
  const cronQuery = useQuery<CronInventory>({ queryKey: ['managed-node-cron', nodeID], queryFn: () => api.get(nodeBase + '/cron'), enabled: online && readAvailable, refetchInterval: online && readAvailable ? 30_000 : false })
  const editJob = (job: CronJob) => {
    setEditing(job)
    setEditingRevision(cronQuery.data?.revision ?? '')
    setSaveConflict(false)
    setForm({ schedule: job.schedule, user: job.user, command: job.command, description: job.description ?? '', enabled: job.enabled })
  }
  const cancelEditing = () => {
    setEditing(null)
    setEditingRevision('')
    setSaveConflict(false)
    setForm(emptyForm)
  }

  const editConflict = saveConflict || !!(editing && editingRevision && cronQuery.data?.revision && editingRevision !== cronQuery.data.revision)

  const loadServerVersion = async () => {
    const refreshed = await cronQuery.refetch()
    const current = refreshed.data?.jobs.find((job) => job.id === editing?.id)
    if (!current || !refreshed.data?.revision) {
      toast.error('This cron job no longer exists on remote server')
      cancelEditing()
      return
    }
    setEditing(current)
    setEditingRevision(refreshed.data.revision)
    setForm({ schedule: current.schedule, user: current.user, command: current.command, description: current.description ?? '', enabled: current.enabled })
    setSaveConflict(false)
  }

  const refresh = () => queryClient.invalidateQueries({ queryKey: ['managed-node-cron', nodeID] })
  const saveMutation = useMutation<CronResult, Error, void>({
    mutationFn: () => {
      requireRemoteMutationReadiness(controlsReady)
      return editing
        ? api.put(`${nodeBase}/cron/${editing.id}`, { ...form, revision: editingRevision })
        : api.post(nodeBase + '/cron', { ...form, revision: cronQuery.data?.revision })
    },
    onSuccess: async (result) => { toast.success(result.message); setEditing(null); setEditingRevision(''); setSaveConflict(false); setForm(emptyForm); await refresh() },
    onError: async (error) => {
      if (error.message.includes('cron jobs changed on the server')) {
        if (editing) setSaveConflict(true)
        toast.error('Cron jobs changed on remote server. Your form was preserved; review the current server version before saving again.')
        await refresh()
        return
      }
      toast.error(error.message || 'Cron job save failed')
    },
  })
  const toggleMutation = useMutation<CronResult, Error, { job: CronJob; revision: string }>({
    mutationFn: ({ job, revision }) => { requireRemoteMutationReadiness(controlsReady); return api.put(`${nodeBase}/cron/${job.id}`, { ...job, enabled: !job.enabled, revision }) },
    onSuccess: async (result) => { toast.success(result.message); await refresh() }, onError: async (error) => { toast.error(error.message); await refresh() },
  })
  const deleteMutation = useMutation<CronResult, Error, { job: CronJob; revision: string }>({
    mutationFn: ({ job, revision }) => { requireRemoteMutationReadiness(controlsReady); return api.delete(`${nodeBase}/cron/${job.id}`, { revision }) },
    onSuccess: async (result) => { toast.success(result.message); setEditing(null); setEditingRevision(''); setSaveConflict(false); await refresh() }, onError: async (error) => { toast.error(error.message); await refresh() },
  })
  const runMutation = useMutation<CronResult, Error, CronJob>({
    mutationFn: (job) => { requireRemoteMutationReadiness(controlsReady); return api.post(`${nodeBase}/cron/${job.id}/run`) },
    onSuccess: (result, job) => { toast.success(result.message); setRunOutput({ name: job.description || job.command, output: result.output || '(Command completed without output.)' }) },
    onError: (error) => toast.error(error.message || 'Cron job failed'),
  })
  const jobs = cronQuery.data?.jobs ?? []
  const validForm = form.schedule.trim().split(/\s+/).length === 5 && !!form.command.trim() && !!form.user.trim()

  if (!online) return <CronUnavailable title="Managed node is offline" detail="Cron inventory, edits, and manual runs remain unavailable until the agent reconnects." />
  if (!readAvailable) return <CronUnavailable title="Cron inventory is disabled" detail="This agent does not advertise cron.read. Enable cron inventory locally, then restart the agent." />

  return <div className="space-y-4">
    {cronQuery.isSuccess && (!writeAvailable || !runAvailable) ? <Card className="border-amber-500/30 bg-amber-500/[0.07]"><CardContent className="p-4 text-xs text-amber-200">Cron inventory is available. {!writeAvailable ? <span>Job changes are read-only because <code>cron.write</code> is disabled. </span> : null}{!runAvailable ? <span>Manual runs are disabled because <code>cron.run</code> is not enabled.</span> : null}</CardContent></Card> : null}
    {cronQuery.isSuccess && <div className="grid gap-3 md:grid-cols-3">
      <Metric label="Cron service" value={cronQuery.data?.service ?? '—'} active={cronQuery.data?.service === 'active'} />
      <Metric label="HServer jobs" value={String(jobs.length)} active />
      <Metric label="System sources" value={String(cronQuery.data?.sources.length ?? 0)} active />
    </div>}
    {cronQuery.isSuccess && <Card className="border-zinc-800 bg-zinc-900/80"><CardHeader className="gap-3"><div className="flex items-start justify-between gap-3"><div><CardTitle className="text-sm text-zinc-200">{editing ? `Edit ${editing.id}` : 'Add scheduled job'}</CardTitle><p className="mt-1 text-[10px] text-zinc-500">Validated and atomically written to /etc/cron.d/hserver-managed</p></div>{editing && <Button variant="ghost" size="xs" onClick={cancelEditing}><X className="size-3" /> Cancel</Button>}</div>{editConflict && <div className="flex flex-col gap-2 rounded-lg border border-amber-500/30 bg-amber-500/[0.07] p-3 text-[10px] text-amber-200 sm:flex-row sm:items-center sm:justify-between"><span className="flex items-start gap-2"><AlertTriangle className="mt-0.5 size-3.5 shrink-0" />This cron job changed on remote server after you opened it. Your form was not overwritten.</span><Button variant="outline" size="xs" disabled={cronQuery.isFetching} onClick={loadServerVersion}>{cronQuery.isFetching ? <Loader2 className="size-3 animate-spin" /> : <RefreshCw className="size-3" />} Load server version</Button></div>}</CardHeader><CardContent className="grid gap-3 lg:grid-cols-[180px_120px_minmax(0,1fr)_220px_auto]">
      <label className="space-y-1"><span className="text-[10px] uppercase text-zinc-500">Schedule</span><input disabled={!writeAvailable} value={form.schedule} onChange={(e) => setForm({ ...form, schedule: e.target.value })} className="h-9 w-full rounded-lg border border-zinc-800 bg-zinc-950 px-3 font-mono text-xs text-zinc-200 outline-none focus:border-violet-500/60 disabled:text-zinc-600" placeholder="0 * * * *" /></label>
      <label className="space-y-1"><span className="text-[10px] uppercase text-zinc-500">User</span><input disabled={!writeAvailable} value={form.user} onChange={(e) => setForm({ ...form, user: e.target.value })} className="h-9 w-full rounded-lg border border-zinc-800 bg-zinc-950 px-3 text-xs text-zinc-200 outline-none focus:border-violet-500/60 disabled:text-zinc-600" /></label>
      <label className="space-y-1"><span className="text-[10px] uppercase text-zinc-500">Command</span><input disabled={!writeAvailable} value={form.command} onChange={(e) => setForm({ ...form, command: e.target.value })} className="h-9 w-full rounded-lg border border-zinc-800 bg-zinc-950 px-3 font-mono text-xs text-zinc-200 outline-none focus:border-violet-500/60 disabled:text-zinc-600" placeholder="/usr/bin/php /var/www/app/artisan schedule:run" /></label>
      <label className="space-y-1"><span className="text-[10px] uppercase text-zinc-500">Description</span><input disabled={!writeAvailable} value={form.description} onChange={(e) => setForm({ ...form, description: e.target.value })} className="h-9 w-full rounded-lg border border-zinc-800 bg-zinc-950 px-3 text-xs text-zinc-200 outline-none focus:border-violet-500/60 disabled:text-zinc-600" placeholder="Optional label" /></label>
      <div className="flex items-end"><Button disabled={!controlsReady || !online || !writeAvailable || !validForm || !cronQuery.data?.revision || editConflict || saveMutation.isPending} onClick={() => saveMutation.mutate()}>{saveMutation.isPending ? <Loader2 className="size-3.5 animate-spin" /> : editing ? <Save className="size-3.5" /> : <Plus className="size-3.5" />}{editing ? 'Save' : 'Add'}</Button></div>
    </CardContent></Card>}
    <Card className="border-zinc-800 bg-zinc-900/80"><CardHeader className="flex-row items-start justify-between"><div><CardTitle className="text-sm text-zinc-200">remote server scheduled jobs</CardTitle><p className="mt-1 text-[10px] text-zinc-500">Enable, disable, edit, run now or delete HServer-owned jobs</p></div><Button variant="ghost" size="xs" disabled={!online || !readAvailable || cronQuery.isFetching} onClick={() => cronQuery.refetch()}><RefreshCw className={cn('size-3', cronQuery.isFetching && 'animate-spin')} /> Refresh</Button></CardHeader><CardContent className="p-0">
      {cronQuery.isLoading ? <div className="p-10 text-center"><Loader2 className="mx-auto size-5 animate-spin text-zinc-500" /></div> : cronQuery.error ? <p className="p-4 text-xs text-red-400">{cronQuery.error.message}</p> : jobs.length === 0 ? <div className="p-10 text-center"><Clock className="mx-auto size-8 text-zinc-700" /><p className="mt-3 text-sm text-zinc-400">No HServer-managed jobs yet.</p></div> : <div className="divide-y divide-zinc-800/70">{jobs.map((job) => <div key={job.id} className="flex flex-col gap-3 p-4 lg:flex-row lg:items-center"><button disabled={!controlsReady || !online || !writeAvailable || !cronQuery.data?.revision || toggleMutation.isPending} onClick={() => { if (cronQuery.data?.revision) toggleMutation.mutate({ job, revision: cronQuery.data.revision }) }} className={cn('h-6 w-10 shrink-0 rounded-full p-0.5 transition disabled:cursor-not-allowed disabled:opacity-40', job.enabled ? 'bg-emerald-500/70' : 'bg-zinc-700')} title={!online ? 'remote server agent is offline' : !writeAvailable ? 'Cron writing is not enabled on this agent' : job.enabled ? 'Disable' : 'Enable'}><span className={cn('block size-5 rounded-full bg-white transition', job.enabled && 'translate-x-4')} /></button><div className="min-w-0 flex-1"><div className="flex flex-wrap items-center gap-2"><span className="rounded bg-violet-500/10 px-2 py-0.5 font-mono text-[10px] text-violet-300">{job.schedule}</span><span className="text-xs font-semibold text-zinc-200">{job.description || 'Scheduled command'}</span><span className="text-[10px] text-zinc-600">as {job.user}</span></div><p className="mt-1 truncate font-mono text-[10px] text-zinc-500" title={job.command}>{job.command}</p></div><div className="flex shrink-0 gap-1"><Button variant="outline" size="xs" disabled={!controlsReady || !online || !runAvailable || runMutation.isPending} onClick={() => runMutation.mutate(job)}>{runMutation.isPending && runMutation.variables?.id === job.id ? <Loader2 className="size-3 animate-spin" /> : <Play className="size-3" />} Run now</Button><Button variant="ghost" size="icon-xs" title="Edit" disabled={!writeAvailable || !cronQuery.data?.revision} onClick={() => editJob(job)}><Pencil className="size-3" /></Button><Button variant="ghost" size="icon-xs" title="Delete" disabled={!controlsReady || !online || !writeAvailable || !cronQuery.data?.revision || deleteMutation.isPending} onClick={() => { if (window.confirm(`Delete cron job ${job.id}?`) && cronQuery.data?.revision) deleteMutation.mutate({ job, revision: cronQuery.data.revision }) }}><Trash2 className="size-3 text-red-400" /></Button></div></div>)}</div>}
    </CardContent></Card>
    {runOutput && <Card className="border-zinc-800 bg-zinc-900/80"><CardHeader className="flex-row items-center justify-between"><div><CardTitle className="text-sm text-zinc-200">Last manual run</CardTitle><p className="mt-1 truncate text-[10px] text-zinc-500">{runOutput.name}</p></div><Button variant="ghost" size="icon-xs" onClick={() => setRunOutput(null)}><X className="size-3" /></Button></CardHeader><CardContent><pre className="max-h-72 overflow-auto whitespace-pre-wrap rounded-xl border border-zinc-800 bg-zinc-950 p-4 font-mono text-[11px] text-zinc-300">{runOutput.output}</pre></CardContent></Card>}
    {cronQuery.isSuccess && <Card className="border-zinc-800 bg-zinc-900/60"><CardHeader><CardTitle className="flex items-center gap-2 text-sm text-zinc-300"><FileClock className="size-4" /> Existing cron sources</CardTitle></CardHeader><CardContent className="grid gap-2 sm:grid-cols-2 xl:grid-cols-3">{(cronQuery.data?.sources ?? []).map((source) => <div key={source.path} className="rounded-lg border border-zinc-800 bg-zinc-950/50 px-3 py-2"><p className="truncate font-mono text-[10px] text-zinc-300" title={source.path}>{source.path}</p><p className="mt-1 text-[9px] text-zinc-600">{source.entry_count} active entries · {source.managed ? 'HServer managed' : 'read-only inventory'}</p></div>)}</CardContent></Card>}
  </div>
}

function CronUnavailable({ title, detail }: { title: string; detail: string }) { return <Card className="border-amber-500/30 bg-amber-500/[0.07]"><CardContent className="p-5 text-xs text-amber-200"><p className="font-semibold">{title}</p><p className="mt-1 text-amber-200/80">{detail}</p></CardContent></Card> }

function Metric({ label, value, active }: { label: string; value: string; active: boolean }) { return <Card className="border-zinc-800 bg-zinc-900/80"><CardContent className="p-4"><p className="text-[10px] font-semibold uppercase tracking-wider text-zinc-500">{label}</p><p className={cn('mt-2 text-lg font-bold', active ? 'text-emerald-400' : 'text-zinc-300')}>{value}</p></CardContent></Card> }
