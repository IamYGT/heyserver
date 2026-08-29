import { useEffect, useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { CheckCircle2, Code2, FileCode2, Loader2, RefreshCw, RotateCw, Save, Zap } from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { cn } from '@/lib/utils'
import { requireRemoteMutationReadiness, useRemoteMutationReadiness } from './remoteMutationReadiness'

interface PHPPool { name: string; path: string; user?: string; group?: string; listen?: string; pm?: string; max_children?: number }
interface PHPVersion { version: string; unit: string; active: string; enabled: string; masked: boolean; binary?: string; pools: PHPPool[] }
interface FileContent { path: string; content: string; checksum: string; size: number; mode: string; modified_at: string }
interface PHPDraft { key: string; content: string; originalContent: string; checksum: string }

export function RemotePHP({ nodeID, online, configReadAvailable, configWriteAvailable, actionAvailable }: { nodeID: string; online: boolean; configReadAvailable: boolean; configWriteAvailable: boolean; actionAvailable: boolean }) {
  const controlsReady = useRemoteMutationReadiness()
  const nodeBase = `/nodes/${encodeURIComponent(nodeID)}`
  const queryClient = useQueryClient()
  const [versionChoice, setVersionChoice] = useState('')
  const [poolChoice, setPoolChoice] = useState('')
  const [draft, setDraft] = useState<PHPDraft | null>(null)
  const [saveConflict, setSaveConflict] = useState(false)
  const inventoryQuery = useQuery<PHPVersion[]>({ queryKey: ['managed-node-php', nodeID], queryFn: () => api.get(nodeBase + '/php'), enabled: online && configReadAvailable, refetchInterval: online && configReadAvailable ? 30_000 : false })
  const defaultVersion = inventoryQuery.data?.find(item => item.active === 'active') ?? inventoryQuery.data?.[0]
  const version = inventoryQuery.data?.some(item => item.version === versionChoice)
    ? versionChoice
    : defaultVersion?.version ?? ''
  const selectedVersion = useMemo(() => inventoryQuery.data?.find((item) => item.version === version), [inventoryQuery.data, version])
  const pool = selectedVersion?.pools.some(item => item.name === poolChoice)
    ? poolChoice
    : selectedVersion?.pools[0]?.name ?? ''
  const configQuery = useQuery<FileContent>({
    queryKey: ['managed-node-php-pool', nodeID, version, pool],
    queryFn: () => api.get(`${nodeBase}/php/${encodeURIComponent(version)}/pools/${encodeURIComponent(pool)}`),
    enabled: online && configReadAvailable && !!version && !!pool,
  })
  const draftKey = `${version}:${pool}`
  useEffect(() => {
    const config = configQuery.data
    if (!config?.checksum) return
    if (!draft || draft.key !== draftKey) {
      // eslint-disable-next-line react-hooks/set-state-in-effect -- hydrate only a newly selected server pool
      setDraft({ key: draftKey, content: config.content, originalContent: config.content, checksum: config.checksum })
      setSaveConflict(false)
      return
    }
    if (draft.checksum !== config.checksum) {
      setSaveConflict(true)
    }
  }, [configQuery.data, draft, draftKey])
  const dirty = !!draft && draft.key === draftKey && draft.content !== draft.originalContent

  const loadServerVersion = async () => {
    const refreshed = await configQuery.refetch()
    if (!refreshed.data?.checksum) return
    setDraft({ key: draftKey, content: refreshed.data.content, originalContent: refreshed.data.content, checksum: refreshed.data.checksum })
    setSaveConflict(false)
  }

  const saveMutation = useMutation<{ message: string; backup: string }, Error, { reload: boolean; draft: PHPDraft; version: string; pool: string }>({
    mutationFn: ({ reload, draft: submitted, version: submittedVersion, pool: submittedPool }) => { requireRemoteMutationReadiness(controlsReady); return api.put(`${nodeBase}/php/${encodeURIComponent(submittedVersion)}/pools/${encodeURIComponent(submittedPool)}`, { content: submitted.content, checksum: submitted.checksum, reload }) },
    onSuccess: async (result, variables) => {
      toast.success(`${result.message}. Backup: ${result.backup}`)
      await queryClient.invalidateQueries({ queryKey: ['managed-node-php', nodeID] })
      const refreshed = await api.get<FileContent>(`${nodeBase}/php/${encodeURIComponent(variables.version)}/pools/${encodeURIComponent(variables.pool)}`)
      queryClient.setQueryData(['managed-node-php-pool', nodeID, variables.version, variables.pool], refreshed)
      if (version === variables.version && pool === variables.pool && refreshed.checksum) {
        setDraft({ key: draftKey, content: refreshed.content, originalContent: refreshed.content, checksum: refreshed.checksum })
        setSaveConflict(false)
      }
    },
    onError: (error) => {
      if (error.message.includes('changed on the server')) setSaveConflict(true)
      toast.error(error.message || 'PHP-FPM pool save failed')
    },
  })
  const actionMutation = useMutation<{ message: string }, Error, 'test' | 'reload' | 'restart'>({
    mutationFn: (action) => { requireRemoteMutationReadiness(controlsReady); return api.post(`${nodeBase}/php/${encodeURIComponent(version)}/actions/${action}`) },
    onSuccess: async (result) => { toast.success(result.message); await queryClient.invalidateQueries({ queryKey: ['managed-node-php', nodeID] }) },
    onError: (error) => toast.error(error.message || 'PHP-FPM action failed'),
  })
  const runtimeAvailable = !!selectedVersion?.binary && !!version
  const editable = runtimeAvailable && !!pool && configWriteAvailable
  const actionUsable = runtimeAvailable && actionAvailable

  if (!online) return <PHPUnavailable title="Managed node is offline" detail="PHP-FPM runtime inventory, pool configuration, and service actions remain unavailable until the agent reconnects." />
  if (!configReadAvailable) return <PHPUnavailable title="PHP-FPM configuration access is disabled" detail="This agent does not advertise php.read. Enable PHP-FPM configuration reading locally, then restart the agent." />
  if (inventoryQuery.isLoading) return <Card className="border-zinc-800 bg-zinc-900/80"><CardContent className="grid h-56 place-items-center"><div className="text-center"><Loader2 className="mx-auto size-5 animate-spin text-zinc-500" /><p className="mt-3 text-xs text-zinc-500">Loading observed PHP-FPM runtimes and pools…</p></div></CardContent></Card>
  if (inventoryQuery.isError || !inventoryQuery.data) return <Card className="border-red-500/25 bg-red-500/[0.05]"><CardContent className="flex flex-col gap-3 p-5 text-xs text-red-300 sm:flex-row sm:items-center sm:justify-between"><span>{inventoryQuery.error instanceof Error ? inventoryQuery.error.message : 'The managed agent did not return a complete PHP-FPM inventory.'}</span><Button variant="outline" size="sm" disabled={inventoryQuery.isFetching} onClick={() => inventoryQuery.refetch()}><RefreshCw className={cn('size-3.5', inventoryQuery.isFetching && 'animate-spin')} /> Retry inventory</Button></CardContent></Card>
  if (inventoryQuery.data.length === 0) return <Card className="border-zinc-800 bg-zinc-900/80"><CardContent className="flex flex-col items-center gap-3 p-10 text-center"><Code2 className="size-8 text-zinc-700" /><div><p className="text-sm text-zinc-300">No PHP-FPM runtimes found.</p><p className="mt-1 text-xs text-zinc-600">The agent returned a successful empty runtime inventory.</p></div><Button variant="outline" size="sm" disabled={inventoryQuery.isFetching} onClick={() => inventoryQuery.refetch()}><RefreshCw className={cn('size-3.5', inventoryQuery.isFetching && 'animate-spin')} /> Refresh inventory</Button></CardContent></Card>

  return <div className="grid gap-4 xl:grid-cols-[340px_minmax(0,1fr)]">
    <Card className="border-zinc-800 bg-zinc-900/80"><CardHeader className="flex-row items-start justify-between"><div><CardTitle className="text-sm text-zinc-200">PHP-FPM runtimes</CardTitle><p className="mt-1 text-[10px] text-zinc-500">remote server versions, pools and service status</p></div><Button variant="ghost" size="icon-xs" disabled={!online || !configReadAvailable || inventoryQuery.isFetching} onClick={() => inventoryQuery.refetch()} title={!configReadAvailable ? 'PHP-FPM reading is not enabled on this agent' : undefined}><RefreshCw className={cn('size-3', inventoryQuery.isFetching && 'animate-spin')} /></Button></CardHeader><CardContent className="space-y-2">
      {inventoryQuery.data.map((item) => <button key={item.version} onClick={() => { setVersionChoice(item.version); setPoolChoice(item.pools[0]?.name ?? ''); setDraft(null); setSaveConflict(false) }} className={cn('w-full rounded-xl border p-3 text-left transition', item.version === version ? 'border-violet-500/50 bg-violet-500/[0.08]' : 'border-zinc-800 bg-zinc-950/40 hover:border-zinc-700')}><div className="flex items-center justify-between"><span className="flex items-center gap-2 font-semibold text-zinc-200"><Code2 className="size-4 text-violet-400" /> PHP {item.version}</span><span className={cn('rounded px-1.5 py-0.5 text-[9px] font-semibold', item.active === 'active' ? 'bg-emerald-500/10 text-emerald-400' : item.masked ? 'bg-red-500/10 text-red-400' : 'bg-zinc-800 text-zinc-500')}>{item.masked ? 'MASKED' : item.active.toUpperCase()}</span></div><p className="mt-2 text-[10px] text-zinc-500">{item.pools.length} pool · {item.enabled}{!item.binary && ' · runtime binary missing'}</p></button>)}
      {selectedVersion && <div className="pt-2"><p className="mb-2 text-[10px] font-semibold uppercase tracking-wider text-zinc-600">Pools</p>{selectedVersion.pools.map((item) => <button key={item.name} onClick={() => { setPoolChoice(item.name); setDraft(null); setSaveConflict(false) }} className={cn('mb-1 w-full rounded-lg border px-3 py-2 text-left', item.name === pool ? 'border-blue-500/40 bg-blue-500/[0.07]' : 'border-zinc-800 text-zinc-500')}><span className="block font-mono text-xs text-zinc-300">{item.name}</span><span className="block truncate text-[9px] text-zinc-600">{item.pm || 'unknown'} · {item.max_children || 0} children · {item.listen || 'no listen value'}</span></button>)}</div>}
    </CardContent></Card>

    <Card className="min-w-0 border-zinc-800 bg-zinc-900/80"><CardHeader className="gap-3"><div className="flex flex-col gap-3 lg:flex-row lg:items-start lg:justify-between"><div className="min-w-0"><CardTitle className="truncate font-mono text-sm text-zinc-200">{version && pool ? `PHP ${version} · ${pool}` : 'Select a PHP pool'}</CardTitle>{configQuery.data && <p className="mt-1 text-[10px] text-zinc-500">{configQuery.data.path} · {configQuery.data.mode} {dirty && <span className="ml-2 text-amber-400">Unsaved changes</span>}</p>}</div><div className="flex flex-wrap gap-1"><Button variant="outline" size="xs" disabled={!controlsReady || !online || !actionUsable || actionMutation.isPending} onClick={() => actionMutation.mutate('test')}><CheckCircle2 className="size-3" /> Test</Button><Button variant="outline" size="xs" disabled={!controlsReady || !online || !actionUsable || dirty || saveConflict || actionMutation.isPending} onClick={() => actionMutation.mutate('reload')}><RefreshCw className="size-3" /> Reload</Button><Button variant="outline" size="xs" disabled={!controlsReady || !online || !actionUsable || dirty || saveConflict || actionMutation.isPending} onClick={() => { if (window.confirm(`Restart PHP ${version} FPM on remote server?`)) actionMutation.mutate('restart') }}><RotateCw className="size-3" /> Restart</Button><Button variant="outline" size="xs" disabled={!controlsReady || !online || !editable || !dirty || saveConflict || saveMutation.isPending || !draft} onClick={() => draft && saveMutation.mutate({ reload: false, draft, version, pool })}><Save className="size-3" /> Save + test</Button><Button size="xs" disabled={!controlsReady || !online || !editable || !dirty || saveConflict || saveMutation.isPending || !draft} onClick={() => draft && saveMutation.mutate({ reload: true, draft, version, pool })}>{saveMutation.isPending ? <Loader2 className="size-3 animate-spin" /> : <Zap className="size-3" />} Save + reload</Button></div></div>
        <div className="rounded-lg border border-emerald-800/40 bg-emerald-500/[0.05] px-3 py-2 text-[10px] text-emerald-300">Pool saves create a backup, run the matching <code>php-fpm -t</code>, restore invalid content automatically, and reload only after a valid test.</div>
        {saveConflict && <div className="flex flex-col gap-2 rounded-lg border border-amber-500/30 bg-amber-500/[0.07] px-3 py-2 text-[10px] text-amber-200 sm:flex-row sm:items-center sm:justify-between"><span>This PHP-FPM pool changed on remote server after you opened it. Your draft was not overwritten.</span><Button variant="outline" size="xs" disabled={configQuery.isFetching} onClick={loadServerVersion}>{configQuery.isFetching ? <Loader2 className="size-3 animate-spin" /> : <RefreshCw className="size-3" />} Load server version</Button></div>}
      </CardHeader><CardContent>{selectedVersion && !runtimeAvailable ? <div className="mb-3 rounded-lg border border-amber-800/40 bg-amber-500/[0.06] p-3 text-xs text-amber-300">PHP {selectedVersion.version} is legacy or masked and has no executable FPM runtime. Its configuration is inspectable but cannot be activated from this screen.</div> : selectedVersion && !configWriteAvailable ? <div className="mb-3 rounded-lg border border-amber-800/40 bg-amber-500/[0.06] p-3 text-xs text-amber-300">This agent exposes PHP-FPM configuration as read-only. Enable <code>php.write</code> locally to edit pools.</div> : null}{configQuery.isLoading ? <div className="grid min-h-[480px] place-items-center"><Loader2 className="size-5 animate-spin text-zinc-500" /></div> : configQuery.error ? <p className="text-xs text-red-400">{configQuery.error.message}</p> : pool ? <textarea value={draft?.key === draftKey ? draft.content : ''} readOnly={!editable} spellCheck={false} onChange={(event) => setDraft((current) => current && current.key === draftKey ? { ...current, content: event.target.value } : current)} className="min-h-[560px] w-full resize-y rounded-xl border border-zinc-800 bg-zinc-950 p-4 font-mono text-xs leading-5 text-zinc-200 outline-none focus:border-violet-500/60 read-only:text-zinc-500" /> : <div className="grid min-h-[420px] place-items-center text-zinc-600"><div className="text-center"><FileCode2 className="mx-auto size-7" /><p className="mt-2 text-xs">No pool selected.</p></div></div>}</CardContent></Card>
  </div>
}

function PHPUnavailable({ title, detail }: { title: string; detail: string }) { return <Card className="border-amber-500/30 bg-amber-500/[0.07]"><CardContent className="p-5 text-xs text-amber-200"><p className="font-semibold">{title}</p><p className="mt-1 text-amber-200/80">{detail}</p></CardContent></Card> }
