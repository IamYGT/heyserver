import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { AlertTriangle, Boxes, CheckCircle2, GitBranch, Globe2, History, Loader2, Play, RefreshCw, RotateCcw, ServerCog, Terminal, Undo2, XCircle } from 'lucide-react'
import { useNavigate } from 'react-router-dom'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { cn } from '@/lib/utils'
import { RemoteProjectDomainsDialog } from '@/components/servers/RemoteProjectDomainsDialog'
import { requireRemoteMutationReadiness, useRemoteMutationReadiness } from './remoteMutationReadiness'

interface DeployContainer { name: string; image: string; state: string; health?: string }
interface DeployTarget {
  id: string; name: string; description?: string; kind: string; path: string; status: string; eligible: boolean; reason?: string; actions?: DeployAction[]
  branch?: string; head?: string; upstream?: string; remote?: string; dirty?: number; ahead?: number; behind?: number
  frameworks?: string[]; containers?: DeployContainer[]; rollback_available?: boolean; rollback_created_at?: string
  host_port?: number
}
interface DeployJob {
  id: string; target_id: string; action: DeployAction; status: 'queued' | 'running' | 'completed' | 'failed'
  message: string; created_at: string; started_at?: string; finished_at?: string; exit_code?: number; output?: string
}
interface AuditEntry { id: number | string; userName?: string; details: string; createdAt: string }
interface AuditResponse { data: AuditEntry[]; total: number }
type DeployAction = 'preflight' | 'deploy' | 'restart' | 'rollback'

export function RemoteDeploy({ nodeID, online, terminalAvailable, readAvailable, actionAvailable, domainReadAvailable, domainActionAvailable }: { nodeID: string; online: boolean; terminalAvailable: boolean; readAvailable: boolean; actionAvailable: boolean; domainReadAvailable: boolean; domainActionAvailable: boolean }) {
  const controlsReady = useRemoteMutationReadiness()
  const nodeBase = `/nodes/${encodeURIComponent(nodeID)}`
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [output, setOutput] = useState<{ title: string; text: string } | null>(null)
  const [domainsTarget, setDomainsTarget] = useState<DeployTarget | null>(null)
  const targetsQuery = useQuery<DeployTarget[]>({
    queryKey: ['managed-node-deploy', nodeID], queryFn: () => api.get(nodeBase + '/deploy'), enabled: online && readAvailable, refetchInterval: 60_000,
  })
  const historyQuery = useQuery<AuditResponse>({
    queryKey: ['managed-node-deploy-history', nodeID],
    queryFn: () => api.get(`/audit?${new URLSearchParams({ action: 'remote_deploy_action', server: nodeID, limit: '8' }).toString()}`), enabled: online && readAvailable, refetchInterval: 30_000,
  })
  const jobsQuery = useQuery<DeployJob[]>({
    queryKey: ['managed-node-deploy-jobs', nodeID], queryFn: () => api.get(nodeBase + '/deploy/jobs'),
    enabled: online && readAvailable, refetchInterval: 3_000,
  })
  const actionMutation = useMutation<DeployJob, Error, { target: DeployTarget; action: DeployAction }>({
    mutationFn: ({ target, action }) => { requireRemoteMutationReadiness(controlsReady); return api.post(`${nodeBase}/deploy/${encodeURIComponent(target.id)}/actions/${action}`) },
    onSuccess: async (job, variables) => {
      toast.success(`${variables.target.name} ${variables.action} queued as ${job.id}`)
      await queryClient.invalidateQueries({ queryKey: ['managed-node-deploy-jobs', nodeID] })
    },
    onError: (error) => toast.error(error.message || 'Deploy action failed'),
    onSettled: () => queryClient.invalidateQueries({ queryKey: ['managed-node-deploy-history', nodeID] }),
  })
  const targets = useMemo(() => targetsQuery.data ?? [], [targetsQuery.data])
  const jobs = jobsQuery.data ?? []
  const activeTargets = new Set(jobs.filter((job) => job.status === 'queued' || job.status === 'running').map((job) => job.target_id))
  const stats = useMemo(() => ({
    total: targets.length,
    eligible: targets.filter((target) => target.eligible).length,
    attention: targets.filter((target) => !target.eligible || target.status === 'degraded').length,
  }), [targets])
	const run = (target: DeployTarget, action: DeployAction) => {
		if (action !== 'preflight') {
			if (!window.confirm(`Run the locally configured ${action} action for ${target.name} now?`)) return
    }
    actionMutation.mutate({ target, action })
  }

  if (!online) return <DeployUnavailable title="Managed node is offline" detail="Deploy targets, persistent jobs, and activity remain unavailable until the agent reconnects." />
  if (!readAvailable) return <DeployUnavailable title="Deploy inventory is disabled" detail="This agent does not advertise deploy.read. Configure local deploy plans and enable inventory, then restart the agent." />

  return <div className="space-y-4">
    {targetsQuery.isSuccess && !actionAvailable ? <Card className="border-amber-500/30 bg-amber-500/[0.07]"><CardContent className="p-4 text-xs text-amber-200">Deploy inventory is available. Actions are read-only because <code>deploy.action</code> is disabled locally.</CardContent></Card> : null}
    <div className="flex flex-col gap-3 lg:flex-row lg:items-center lg:justify-between">
      <div><p className="text-xs text-zinc-500">Targets and exact commands come from the agent's local deploy plan; the panel sends only a target ID and fixed action.</p></div>
      <div className="flex gap-2"><Button variant="outline" size="xs" disabled={!controlsReady || !online || !terminalAvailable} title={!terminalAvailable ? 'Writable terminal is not enabled on this agent' : undefined} onClick={() => navigate(`/terminal?${new URLSearchParams({ node: nodeID }).toString()}`)}><Terminal className="size-3" /> Terminal</Button><Button variant="ghost" size="xs" disabled={!online || !readAvailable || targetsQuery.isFetching} onClick={() => targetsQuery.refetch()}><RefreshCw className={cn('size-3', targetsQuery.isFetching && 'animate-spin')} /> Refresh</Button></div>
    </div>
    {targetsQuery.isSuccess && <div className="grid gap-3 sm:grid-cols-3">
      <Summary label="Targets" value={stats.total} tone="violet" />
      <Summary label="Action ready" value={stats.eligible} tone="green" />
      <Summary label="Needs attention" value={stats.attention} tone="amber" />
    </div>}

    {targetsQuery.isLoading ? <div className="grid h-64 place-items-center"><Loader2 className="size-5 animate-spin text-zinc-500" /></div> : targetsQuery.error ? <RemoteQueryError title="Could not load deploy targets" error={targetsQuery.error} onRetry={() => targetsQuery.refetch()} disabled={!online} /> : targets.length === 0 ? <Card><CardContent className="p-8 text-center text-xs text-zinc-500">No local deploy plans are configured on this agent.</CardContent></Card> : <div className="space-y-3">{targets.map((target) => <TargetCard key={target.id} target={target} pending={(actionMutation.isPending && actionMutation.variables?.target.id === target.id) || activeTargets.has(target.id)} onRun={run} onDomains={setDomainsTarget} online={online} actionAvailable={actionAvailable} domainReadAvailable={domainReadAvailable} />)}</div>}

    <DeployJobs jobs={jobs} targets={targets} loading={jobsQuery.isLoading} error={jobsQuery.error} onRefresh={() => jobsQuery.refetch()} onOutput={(job, target) => setOutput({ title: `${target?.name ?? job.target_id} · ${job.action} · ${job.status}`, text: job.output || job.message })} online={online} />

    <DeployHistory entries={historyQuery.data?.data ?? []} targets={targets} loading={historyQuery.isLoading} error={historyQuery.error} onRefresh={() => historyQuery.refetch()} online={online} />

    {output && <Card className="border-violet-500/30 bg-zinc-950/80"><CardHeader className="flex-row items-center justify-between"><CardTitle className="text-sm text-zinc-200">{output.title}</CardTitle><Button variant="ghost" size="xs" onClick={() => setOutput(null)}>Close</Button></CardHeader><CardContent><pre className="max-h-80 overflow-auto whitespace-pre-wrap rounded-xl border border-zinc-800 bg-black/40 p-4 font-mono text-[11px] leading-5 text-zinc-300">{output.text}</pre></CardContent></Card>}
    <RemoteProjectDomainsDialog nodeID={nodeID} target={domainsTarget} open={Boolean(domainsTarget)} onOpenChange={(next) => { if (!next) setDomainsTarget(null) }} online={online} readAvailable={domainReadAvailable} actionAvailable={domainActionAvailable} />
  </div>
}

function DeployUnavailable({ title, detail }: { title: string; detail: string }) {
  return <Card className="border-amber-500/30 bg-amber-500/[0.07]"><CardContent className="p-5 text-xs text-amber-200"><p className="font-semibold">{title}</p><p className="mt-1 text-amber-200/80">{detail}</p></CardContent></Card>
}

function DeployJobs({ jobs, targets, loading, error, onRefresh, onOutput, online }: { jobs: DeployJob[]; targets: DeployTarget[]; loading: boolean; error: Error | null; onRefresh: () => void; onOutput: (job: DeployJob, target?: DeployTarget) => void; online: boolean }) {
  return <Card className="border-blue-500/20 bg-zinc-900/80"><CardHeader className="flex-row items-center justify-between"><div><CardTitle className="flex items-center gap-2 text-sm text-zinc-200"><ServerCog className="size-4 text-blue-400" /> Deploy jobs</CardTitle><p className="mt-1 text-[10px] text-zinc-600">Jobs run on remote server and remain visible after page refresh or an HServer restart.</p></div><Button variant="ghost" size="xs" disabled={!online || loading} onClick={onRefresh}><RefreshCw className={cn('size-3', loading && 'animate-spin')} /> Refresh</Button></CardHeader><CardContent className="p-0">{loading ? <div className="grid h-24 place-items-center"><Loader2 className="size-4 animate-spin text-zinc-500" /></div> : error ? <InlineQueryError title="Could not load deploy jobs" error={error} onRetry={onRefresh} disabled={!online} /> : jobs.length === 0 ? <div className="p-6 text-center text-xs text-zinc-600">No persistent deploy jobs recorded yet.</div> : <div className="divide-y divide-zinc-800/70">{jobs.map((job) => { const target = targets.find((item) => item.id === job.target_id); const active = job.status === 'queued' || job.status === 'running'; return <div key={job.id} className="flex flex-col gap-3 px-4 py-3 sm:flex-row sm:items-center sm:justify-between"><div className="flex min-w-0 items-center gap-3">{job.status === 'completed' ? <CheckCircle2 className="size-4 shrink-0 text-emerald-400" /> : job.status === 'failed' ? <XCircle className="size-4 shrink-0 text-red-400" /> : <Loader2 className={cn('size-4 shrink-0 text-blue-400', active && 'animate-spin')} />}<div className="min-w-0"><p className="text-xs font-medium text-zinc-300"><span className="capitalize">{job.action}</span> · {target?.name ?? job.target_id}</p><p className="mt-0.5 truncate font-mono text-[9px] text-zinc-600">{job.id} · {job.message}</p></div></div><div className="flex shrink-0 items-center gap-3"><div className="text-right"><p className={cn('text-[10px] font-semibold uppercase', job.status === 'completed' ? 'text-emerald-400' : job.status === 'failed' ? 'text-red-400' : 'text-blue-400')}>{job.status}</p><p className="mt-0.5 text-[9px] text-zinc-600">{formatDate(job.finished_at || job.started_at || job.created_at)}</p></div><Button variant="outline" size="xs" disabled={!job.output} onClick={() => onOutput(job, target)}>Output</Button></div></div> })}</div>}</CardContent></Card>
}

function DeployHistory({ entries, targets, loading, error, onRefresh, online }: { entries: AuditEntry[]; targets: DeployTarget[]; loading: boolean; error: Error | null; onRefresh: () => void; online: boolean }) {
  return <Card className="border-zinc-800 bg-zinc-900/80">
    <CardHeader className="flex-row items-center justify-between"><div><CardTitle className="flex items-center gap-2 text-sm text-zinc-200"><History className="size-4 text-violet-400" /> Recent deploy activity</CardTitle><p className="mt-1 text-[10px] text-zinc-600">Submission attempts are retained in the central audit log.</p></div><Button variant="ghost" size="xs" disabled={!online || loading} onClick={onRefresh}><RefreshCw className={cn('size-3', loading && 'animate-spin')} /> Refresh</Button></CardHeader>
    <CardContent className="p-0">{loading ? <div className="grid h-24 place-items-center"><Loader2 className="size-4 animate-spin text-zinc-500" /></div> : error ? <InlineQueryError title="Could not load deploy history" error={error} onRetry={onRefresh} disabled={!online} /> : entries.length === 0 ? <div className="p-6 text-center text-xs text-zinc-600">No deploy activity recorded yet.</div> : <div className="divide-y divide-zinc-800/70">{entries.map((entry) => {
      const failed = entry.details.toLowerCase().includes(' failed')
      const queued = entry.details.toLowerCase().includes(' queued')
      const target = targets.find((item) => entry.details.includes(item.id))
      const action = (entry.details.match(/\b(preflight|deploy|restart|rollback)\b/)?.[1] ?? 'action')
      const state = failed ? 'Failed' : queued ? 'Queued' : 'Recorded'
      return <div key={entry.id} className="flex flex-col gap-2 px-4 py-3 sm:flex-row sm:items-center sm:justify-between"><div className="flex min-w-0 items-center gap-3">{failed ? <XCircle className="size-4 shrink-0 text-red-400" /> : queued ? <Loader2 className="size-4 shrink-0 text-blue-400" /> : <CheckCircle2 className="size-4 shrink-0 text-emerald-400" />}<div className="min-w-0"><p className="text-xs font-medium text-zinc-300"><span className="capitalize">{action}</span> · {target?.name ?? 'remote server target'}</p><p className="truncate font-mono text-[9px] text-zinc-600" title={entry.details}>{entry.details}</p></div></div><div className="shrink-0 text-left sm:text-right"><p className={cn('text-[10px] font-semibold uppercase', failed ? 'text-red-400' : queued ? 'text-blue-400' : 'text-emerald-400')}>{state}</p><p className="mt-0.5 text-[9px] text-zinc-600">{entry.userName || 'System'} · {formatDate(entry.createdAt)}</p></div></div>
    })}</div>}</CardContent>
  </Card>
}

function RemoteQueryError({ title, error, onRetry, disabled = false }: { title: string; error: Error; onRetry: () => void; disabled?: boolean }) {
  return <Card className="border-red-500/25 bg-red-500/[0.05]"><CardContent className="flex flex-col items-center gap-3 p-6 text-center"><AlertTriangle className="size-5 text-red-400" /><div><p className="text-sm font-medium text-red-300">{title}</p><p className="mt-1 text-xs text-zinc-500">{error.message || 'The server did not return this operational data.'}</p></div><Button variant="outline" size="sm" disabled={disabled} onClick={onRetry}><RefreshCw className="size-3.5" /> Retry</Button></CardContent></Card>
}

function InlineQueryError({ title, error, onRetry, disabled = false }: { title: string; error: Error; onRetry: () => void; disabled?: boolean }) {
  return <div className="flex flex-col items-center gap-3 px-4 py-6 text-center"><AlertTriangle className="size-5 text-red-400" /><div><p className="text-xs font-medium text-red-300">{title}</p><p className="mt-1 text-[10px] text-zinc-500">{error.message || 'The server did not return this operational data.'}</p></div><Button variant="outline" size="xs" disabled={disabled} onClick={onRetry}><RefreshCw className="size-3" /> Retry</Button></div>
}

function TargetCard({ target, pending, onRun, onDomains, online, actionAvailable, domainReadAvailable }: { target: DeployTarget; pending: boolean; onRun: (target: DeployTarget, action: DeployAction) => void; onDomains: (target: DeployTarget) => void; online: boolean; actionAvailable: boolean; domainReadAvailable: boolean }) {
  const controlsReady = useRemoteMutationReadiness()
  const compose = target.kind === 'compose'
  const Icon = compose ? Boxes : target.kind === 'application' ? ServerCog : GitBranch
  const actions = new Set(target.actions ?? ['preflight'])
  const healthy = target.eligible && target.status !== 'degraded'
  return <Card className="border-zinc-800 bg-zinc-900/80">
    <CardHeader className="flex-row items-start justify-between gap-4">
      <div className="flex min-w-0 gap-3"><span className={cn('grid size-10 shrink-0 place-items-center rounded-xl', healthy ? 'bg-emerald-500/10 text-emerald-400' : 'bg-amber-500/10 text-amber-400')}><Icon className="size-5" /></span><div className="min-w-0"><div className="flex flex-wrap items-center gap-2"><CardTitle className="text-sm text-zinc-100">{target.name}</CardTitle><Badge good={healthy}>{target.kind} · {target.status}</Badge></div><p className="mt-1 truncate font-mono text-[10px] text-zinc-600" title={target.path}>{target.path}</p>{target.reason && <p className="mt-2 flex items-center gap-1.5 text-[11px] text-amber-400"><AlertTriangle className="size-3 shrink-0" />{target.reason}</p>}</div></div>
      <div className="flex shrink-0 flex-wrap justify-end gap-1.5"><Button variant="outline" size="xs" disabled={!online || !domainReadAvailable || !target.host_port} title={!target.host_port ? 'Add host_port to this server local deploy plan' : !domainReadAvailable ? 'deploy.domain.read is disabled on this agent' : undefined} onClick={() => onDomains(target)}><Globe2 className="size-3" /> Domains</Button>{actions.has('preflight') && <Button variant="outline" size="xs" disabled={!controlsReady || !online || !actionAvailable || pending || !target.eligible} onClick={() => onRun(target, 'preflight')}>{pending ? <Loader2 className="size-3 animate-spin" /> : <CheckCircle2 className="size-3" />} Preflight</Button>}{actions.has('deploy') && <Button size="xs" disabled={!controlsReady || !online || !actionAvailable || pending || !target.eligible} onClick={() => onRun(target, 'deploy')}><Play className="size-3" /> Deploy</Button>}{actions.has('restart') && <Button variant="outline" size="xs" disabled={!controlsReady || !online || !actionAvailable || pending || !target.eligible} onClick={() => onRun(target, 'restart')}><RotateCcw className="size-3" /> Restart</Button>}{actions.has('rollback') && <Button variant="outline" size="xs" disabled={!controlsReady || !online || !actionAvailable || pending || !target.eligible} onClick={() => onRun(target, 'rollback')}><Undo2 className="size-3" /> Rollback</Button>}</div>
    </CardHeader>
    <CardContent>{compose && target.containers ? <ComposeDetails target={target} /> : target.branch ? <GitDetails target={target} /> : <ConfiguredPlanDetails target={target} />}</CardContent>
  </Card>
}

function ConfiguredPlanDetails({ target }: { target: DeployTarget }) {
  return <div className="space-y-3"><p className="text-xs text-zinc-400">{target.description || 'Locally configured deploy plan.'}</p><div className="flex flex-wrap gap-2">{(target.actions ?? []).map((action) => <Tag key={action}>{action}</Tag>)}</div><p className="text-[10px] text-zinc-600">Command paths, arguments, working directory, and timeouts remain local to this server.</p></div>
}

function GitDetails({ target }: { target: DeployTarget }) {
  return <div className="space-y-3"><div className="grid gap-2 text-[10px] sm:grid-cols-2 xl:grid-cols-4"><Info label="Branch" value={target.branch || 'detached'} /><Info label="Head" value={target.head || '—'} /><Info label="Upstream" value={target.upstream || 'not configured'} /><Info label="Working tree" value={`${target.dirty ?? 0} changes · ${target.ahead ?? 0} ahead · ${target.behind ?? 0} behind`} /></div><div className="flex flex-wrap gap-2">{target.remote && <Tag>{target.remote}</Tag>}{target.frameworks?.map((framework) => <Tag key={framework}>{framework}</Tag>)}</div><p className="text-[10px] text-zinc-600">Git targets are inventory/preflight only until a clean, target-specific release command is configured.</p></div>
}

function ComposeDetails({ target }: { target: DeployTarget }) {
  const containers = target.containers ?? []
  return <div className="space-y-3"><div className={cn('flex items-center gap-2 rounded-lg border px-3 py-2 text-[10px]', target.rollback_available ? 'border-emerald-500/20 bg-emerald-500/5 text-emerald-400' : 'border-zinc-800 bg-zinc-950/40 text-zinc-600')}><Undo2 className="size-3.5" />{target.rollback_available ? `Rollback ready · snapshot ${formatDate(target.rollback_created_at || '')}` : 'Rollback will become available automatically after the first managed deploy.'}</div><div className="overflow-x-auto rounded-xl border border-zinc-800"><table className="w-full min-w-[620px] text-xs"><thead><tr className="bg-zinc-950/60 text-zinc-500"><th className="px-4 py-2 text-left">Container</th><th className="px-3 py-2 text-left">Image</th><th className="px-3 py-2 text-left">State</th><th className="px-4 py-2 text-left">Health</th></tr></thead><tbody>{containers.length === 0 ? <tr><td colSpan={4} className="p-8 text-center text-zinc-600"><ServerCog className="mx-auto mb-2 size-6" />No running containers reported.</td></tr> : containers.map((container) => { const good = container.state === 'running' && (!container.health || container.health === 'healthy'); return <tr key={container.name} className="border-t border-zinc-800/70"><td className="px-4 py-2.5 font-mono text-zinc-300">{container.name}</td><td className="px-3 py-2.5 text-zinc-500">{container.image}</td><td className={cn('px-3 py-2.5', good ? 'text-emerald-400' : 'text-amber-400')}>{container.state}</td><td className="px-4 py-2.5 text-zinc-500">{container.health || 'not configured'}</td></tr> })}</tbody></table></div></div>
}

function Summary({ label, value, tone }: { label: string; value: number; tone: 'violet' | 'green' | 'amber' }) { return <div className="rounded-xl border border-zinc-800 bg-zinc-900/70 px-4 py-3"><p className="text-[10px] font-semibold uppercase tracking-wider text-zinc-600">{label}</p><p className={cn('mt-1 text-xl font-bold', tone === 'green' ? 'text-emerald-400' : tone === 'amber' ? 'text-amber-400' : 'text-violet-400')}>{value}</p></div> }
function Badge({ good, children }: { good: boolean; children: React.ReactNode }) { return <span className={cn('rounded px-2 py-1 text-[9px] font-semibold uppercase', good ? 'bg-emerald-500/10 text-emerald-400' : 'bg-amber-500/10 text-amber-400')}>{children}</span> }
function Info({ label, value }: { label: string; value: string }) { return <div className="rounded-lg border border-zinc-800 bg-zinc-950/50 px-3 py-2"><p className="uppercase tracking-wider text-zinc-600">{label}</p><p className="mt-1 truncate text-zinc-300" title={value}>{value}</p></div> }
function Tag({ children }: { children: React.ReactNode }) { return <span className="rounded-md border border-zinc-800 bg-zinc-950/50 px-2 py-1 font-mono text-[9px] text-zinc-500">{children}</span> }
function formatDate(value: string) { const date = new Date(value); return Number.isNaN(date.getTime()) ? value : date.toLocaleString() }
