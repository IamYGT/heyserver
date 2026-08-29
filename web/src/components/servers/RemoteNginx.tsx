import { useEffect, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { CheckCircle2, FileCode2, Loader2, RefreshCw, Save, Zap } from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { cn } from '@/lib/utils'
import { requireRemoteMutationReadiness, useRemoteMutationReadiness } from './remoteMutationReadiness'

interface NginxConfig { name: string; enabled: boolean; size: number; modified_at: string; content?: string; checksum?: string }
interface NginxDraft { name: string; content: string; originalContent: string; checksum: string }

export function RemoteNginx({ nodeID, online, actionAvailable, configReadAvailable, configWriteAvailable }: { nodeID: string; online: boolean; actionAvailable: boolean; configReadAvailable: boolean; configWriteAvailable: boolean }) {
  const controlsReady = useRemoteMutationReadiness()
  const nodeBase = `/nodes/${encodeURIComponent(nodeID)}`
  const queryClient = useQueryClient()
  const [selectedChoice, setSelectedChoice] = useState('')
  const [draft, setDraft] = useState<NginxDraft | null>(null)
  const [saveConflict, setSaveConflict] = useState(false)
  const configsQuery = useQuery<NginxConfig[]>({
    queryKey: ['managed-node-nginx-configs', nodeID],
    queryFn: () => api.get(nodeBase + '/nginx/configs'),
    enabled: online && configReadAvailable,
  })
  const selected = configsQuery.data?.some(config => config.name === selectedChoice)
    ? selectedChoice
    : configsQuery.data?.[0]?.name ?? ''
  const configQuery = useQuery<NginxConfig>({
    queryKey: ['managed-node-nginx-config', nodeID, selected],
    queryFn: () => api.get(`${nodeBase}/nginx/configs/${encodeURIComponent(selected)}`),
    enabled: online && configReadAvailable && !!selected,
  })
  useEffect(() => {
    const config = configQuery.data
    if (config?.content === undefined || !config.checksum) return
    if (!draft || draft.name !== selected) {
      // eslint-disable-next-line react-hooks/set-state-in-effect -- hydrate only a newly selected server config
      setDraft({ name: selected, content: config.content, originalContent: config.content, checksum: config.checksum })
      setSaveConflict(false)
      return
    }
    if (draft.checksum !== config.checksum) {
      setSaveConflict(true)
    }
  }, [configQuery.data, draft, selected])
  const dirty = !!draft && draft.name === selected && draft.content !== draft.originalContent

  const loadServerVersion = async () => {
    const refreshed = await configQuery.refetch()
    const config = refreshed.data
    if (config?.content === undefined || !config.checksum) return
    setDraft({ name: selected, content: config.content, originalContent: config.content, checksum: config.checksum })
    setSaveConflict(false)
  }

  const saveMutation = useMutation<{ message: string; backup: string }, Error, { reload: boolean; draft: NginxDraft }>({
    mutationFn: ({ reload, draft: submitted }) => { requireRemoteMutationReadiness(controlsReady); return api.put(`${nodeBase}/nginx/configs/${encodeURIComponent(submitted.name)}`, { content: submitted.content, checksum: submitted.checksum, reload }) },
    onSuccess: async (result, variables) => {
      toast.success(`${result.message}. Backup: ${result.backup}`)
      await queryClient.invalidateQueries({ queryKey: ['managed-node-nginx-configs', nodeID] })
      const refreshed = await api.get<NginxConfig>(`${nodeBase}/nginx/configs/${encodeURIComponent(variables.draft.name)}`)
      queryClient.setQueryData(['managed-node-nginx-config', nodeID, variables.draft.name], refreshed)
      if (selected === variables.draft.name && refreshed.content !== undefined && refreshed.checksum) {
        setDraft({ name: selected, content: refreshed.content, originalContent: refreshed.content, checksum: refreshed.checksum })
        setSaveConflict(false)
      }
    },
    onError: (error) => {
      if (error.message.includes('changed on the server')) setSaveConflict(true)
      toast.error(error.message || 'Nginx configuration save failed')
    },
  })
  const actionMutation = useMutation<{ message: string }, Error, 'test' | 'reload'>({
    mutationFn: (action) => { requireRemoteMutationReadiness(controlsReady); return api.post(`${nodeBase}/nginx/actions/${action}`) },
    onSuccess: (result) => toast.success(result.message),
    onError: (error) => toast.error(error.message || 'Nginx action failed'),
  })

  if (!online) return <NginxUnavailable title="Managed node is offline" detail="Nginx configuration inventory, validation, and reload actions remain unavailable until the agent reconnects." />
  if (!configReadAvailable) return <NginxUnavailable title="Nginx configuration access is disabled" detail="This agent does not advertise nginx.config.read. Enable local Nginx configuration reading, then restart the agent." />
  if (configsQuery.isLoading) return <Card className="border-zinc-800 bg-zinc-900/80"><CardContent className="grid h-56 place-items-center"><div className="text-center"><Loader2 className="mx-auto size-5 animate-spin text-zinc-500" /><p className="mt-3 text-xs text-zinc-500">Loading observed Nginx site configurations…</p></div></CardContent></Card>
  if (configsQuery.isError || !configsQuery.data) return <Card className="border-red-500/25 bg-red-500/[0.05]"><CardContent className="flex flex-col gap-3 p-5 text-xs text-red-300 sm:flex-row sm:items-center sm:justify-between"><span>{configsQuery.error instanceof Error ? configsQuery.error.message : 'The managed agent did not return a complete Nginx configuration inventory.'}</span><Button variant="outline" size="sm" disabled={configsQuery.isFetching} onClick={() => configsQuery.refetch()}><RefreshCw className={cn('size-3.5', configsQuery.isFetching && 'animate-spin')} /> Retry inventory</Button></CardContent></Card>
  if (configsQuery.data.length === 0) return <Card className="border-zinc-800 bg-zinc-900/80"><CardContent className="flex flex-col items-center gap-3 p-10 text-center"><FileCode2 className="size-8 text-zinc-700" /><div><p className="text-sm text-zinc-300">No Nginx site configurations found.</p><p className="mt-1 text-xs text-zinc-600">The agent returned a successful empty inventory for its configured sites directory.</p></div><Button variant="outline" size="sm" disabled={configsQuery.isFetching} onClick={() => configsQuery.refetch()}><RefreshCw className={cn('size-3.5', configsQuery.isFetching && 'animate-spin')} /> Refresh inventory</Button></CardContent></Card>

  return <div className="grid gap-4 xl:grid-cols-[320px_minmax(0,1fr)]">
    <Card className="border-zinc-800 bg-zinc-900/80"><CardHeader className="flex-row items-center justify-between"><div><CardTitle className="text-sm text-zinc-200">Nginx sites</CardTitle><p className="mt-1 text-[10px] text-zinc-500">agent-configured sites directory</p></div><Button variant="ghost" size="icon-xs" disabled={!online || !configReadAvailable || configsQuery.isFetching} onClick={() => configsQuery.refetch()}><RefreshCw className={cn('size-3', configsQuery.isFetching && 'animate-spin')} /></Button></CardHeader><CardContent className="max-h-[650px] overflow-auto p-0">
      <div className="divide-y divide-zinc-800/60">{configsQuery.data.map((config) => <button key={config.name} onClick={() => { setSelectedChoice(config.name); setDraft(null); setSaveConflict(false) }} className={cn('flex w-full items-center gap-3 px-4 py-3 text-left hover:bg-zinc-800/40', selected === config.name && 'bg-violet-500/10')}><FileCode2 className={cn('size-4 shrink-0', config.enabled ? 'text-emerald-400' : 'text-zinc-600')} /><span className="min-w-0 flex-1"><span className="block truncate font-mono text-xs text-zinc-200">{config.name}</span><span className="text-[9px] text-zinc-600">{config.size} B · {config.enabled ? 'enabled' : 'available only'}</span></span>{config.enabled && <span className="rounded bg-emerald-500/10 px-1.5 py-0.5 text-[9px] font-semibold text-emerald-400">ON</span>}</button>)}</div>
    </CardContent></Card>

    <Card className="min-w-0 border-zinc-800 bg-zinc-900/80">
      <CardHeader className="gap-3"><div className="flex flex-col gap-3 lg:flex-row lg:items-start lg:justify-between"><div className="min-w-0"><CardTitle className="truncate font-mono text-sm text-zinc-200">{selected || 'Select a configuration'}</CardTitle>{configQuery.data && <p className="mt-1 text-[10px] text-zinc-500">{configQuery.data.enabled ? 'Enabled' : 'Not enabled'} · {new Date(configQuery.data.modified_at).toLocaleString()} {dirty && <span className="ml-2 text-amber-400">Unsaved changes</span>}</p>}</div><div className="flex flex-wrap gap-2"><Button variant="outline" size="sm" disabled={!controlsReady || !online || !actionAvailable || actionMutation.isPending} title={!actionAvailable ? 'Nginx actions are not enabled on this agent' : undefined} onClick={() => actionMutation.mutate('test')}><CheckCircle2 className="size-3.5" /> Test</Button><Button variant="outline" size="sm" disabled={!controlsReady || !online || !actionAvailable || actionMutation.isPending || dirty || saveConflict} title={!actionAvailable ? 'Nginx actions are not enabled on this agent' : undefined} onClick={() => actionMutation.mutate('reload')}><RefreshCw className="size-3.5" /> Reload</Button><Button variant="outline" size="sm" disabled={!controlsReady || !online || !configWriteAvailable || !dirty || saveConflict || saveMutation.isPending || !draft} title={!configWriteAvailable ? 'Nginx configuration writing is not enabled on this agent' : undefined} onClick={() => draft && saveMutation.mutate({ reload: false, draft })}><Save className="size-3.5" /> Save + test</Button><Button size="sm" disabled={!controlsReady || !online || !configWriteAvailable || !dirty || saveConflict || saveMutation.isPending || !draft} title={!configWriteAvailable ? 'Nginx configuration writing is not enabled on this agent' : undefined} onClick={() => draft && saveMutation.mutate({ reload: true, draft })}>{saveMutation.isPending ? <Loader2 className="size-3.5 animate-spin" /> : <Zap className="size-3.5" />} Save + reload</Button></div></div>
        <div className="rounded-lg border border-emerald-800/40 bg-emerald-500/[0.05] px-3 py-2 text-[10px] text-emerald-300">Every save creates a timestamped backup and runs <code>nginx -t</code>. Invalid configuration is restored automatically and is never reloaded.</div>
        {saveConflict && <div className="flex flex-col gap-2 rounded-lg border border-amber-500/30 bg-amber-500/[0.07] px-3 py-2 text-[10px] text-amber-200 sm:flex-row sm:items-center sm:justify-between"><span>This Nginx config changed on remote server after you opened it. Your draft was not overwritten.</span><Button variant="outline" size="xs" disabled={configQuery.isFetching} onClick={loadServerVersion}>{configQuery.isFetching ? <Loader2 className="size-3 animate-spin" /> : <RefreshCw className="size-3" />} Load server version</Button></div>}
      </CardHeader>
      <CardContent>{configQuery.isLoading ? <div className="grid min-h-[480px] place-items-center"><Loader2 className="size-5 animate-spin text-zinc-500" /></div> : configQuery.error ? <p className="text-xs text-red-400">{configQuery.error.message}</p> : selected ? <textarea value={draft?.name === selected ? draft.content : ''} readOnly={!configWriteAvailable} spellCheck={false} onChange={(event) => setDraft((current) => current && current.name === selected ? { ...current, content: event.target.value } : current)} className="min-h-[560px] w-full resize-y rounded-xl border border-zinc-800 bg-zinc-950 p-4 font-mono text-xs leading-5 text-zinc-200 outline-none focus:border-violet-500/60 read-only:cursor-default read-only:text-zinc-400" /> : <div className="grid min-h-[420px] place-items-center text-sm text-zinc-600">Select a site configuration.</div>}</CardContent>
    </Card>
  </div>
}

function NginxUnavailable({ title, detail }: { title: string; detail: string }) { return <Card className="border-amber-500/30 bg-amber-500/[0.07]"><CardContent className="p-5 text-xs text-amber-200"><p className="font-semibold">{title}</p><p className="mt-1 text-amber-200/80">{detail}</p></CardContent></Card> }
