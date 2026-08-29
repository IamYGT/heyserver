import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { FileClock, Loader2, Play, RefreshCw, RotateCw, Square, Terminal } from 'lucide-react'
import { useNavigate } from 'react-router-dom'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { cn } from '@/lib/utils'
import { requireRemoteMutationReadiness, useRemoteMutationReadiness } from './remoteMutationReadiness'

interface PM2Process { id: number; name: string; status: string; pid: number; cpu: number; memory: number; uptime: number; restarts: number; mode: string; cwd: string; script: string; version: string }
interface PM2Logs { logs: string }

function formatBytes(bytes: number) { return bytes < 1024 ** 2 ? `${(bytes / 1024).toFixed(0)} KiB` : `${(bytes / 1024 ** 2).toFixed(1)} MiB` }
function formatUptime(timestamp: number) {
  if (!timestamp) return '—'
  const seconds = Math.max(0, Math.floor((Date.now() - timestamp) / 1000))
  const days = Math.floor(seconds / 86400); const hours = Math.floor((seconds % 86400) / 3600); const minutes = Math.floor((seconds % 3600) / 60)
  return days ? `${days}d ${hours}h` : hours ? `${hours}h ${minutes}m` : `${minutes}m`
}

export function RemotePM2({ nodeID, online, terminalAvailable, readAvailable, actionAvailable }: { nodeID: string; online: boolean; terminalAvailable: boolean; readAvailable: boolean; actionAvailable: boolean }) {
  const controlsReady = useRemoteMutationReadiness()
  const nodeBase = `/nodes/${encodeURIComponent(nodeID)}`
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [logProcess, setLogProcess] = useState<string | null>(null)
  const processesQuery = useQuery<PM2Process[]>({ queryKey: ['managed-node-pm2', nodeID], queryFn: () => api.get(nodeBase + '/pm2'), enabled: online && readAvailable, refetchInterval: online && readAvailable ? 10_000 : false })
  const logsQuery = useQuery<PM2Logs>({ queryKey: ['managed-node-pm2-logs', nodeID, logProcess], queryFn: () => api.get(`${nodeBase}/pm2/${encodeURIComponent(logProcess ?? '')}/logs?lines=200`), enabled: online && readAvailable && !!logProcess })
  const actionMutation = useMutation<{ message: string }, Error, { name: string; action: 'start' | 'stop' | 'restart' | 'reload' }>({
    mutationFn: ({ name, action }) => { requireRemoteMutationReadiness(controlsReady); return api.post(`${nodeBase}/pm2/${encodeURIComponent(name)}/actions/${action}`) },
    onSuccess: async (result) => { toast.success(result.message); await queryClient.invalidateQueries({ queryKey: ['managed-node-pm2', nodeID] }) },
    onError: (error) => toast.error(error.message || 'PM2 action failed'),
  })
  const processes = processesQuery.data ?? []

  if (!online) return <PM2Unavailable title="Managed node is offline" detail="PM2 inventory, logs, and actions remain unavailable until the agent reconnects." />
  if (!readAvailable) return <PM2Unavailable title="PM2 inventory is disabled" detail="This agent does not advertise pm2.read. Enable PM2 inventory locally, then restart the agent." />

  return <div className="space-y-4"><Card className="border-zinc-800 bg-zinc-900/80"><CardHeader className="flex-row items-start justify-between"><div><CardTitle className="text-sm text-zinc-200">remote server PM2 processes</CardTitle><p className="mt-1 text-[10px] text-zinc-500">Persistent start, stop, restart and zero-downtime reload controls</p></div><Button variant="ghost" size="xs" disabled={!online || !readAvailable || processesQuery.isFetching} onClick={() => processesQuery.refetch()} title={!readAvailable ? 'PM2 reading is not enabled on this agent' : undefined}><RefreshCw className={cn('size-3', processesQuery.isFetching && 'animate-spin')} /> Refresh</Button></CardHeader><CardContent className="p-0">
    {processesQuery.isSuccess && !actionAvailable ? <div className="m-4 rounded-lg border border-amber-500/30 bg-amber-500/[0.07] p-3 text-xs text-amber-200">This agent exposes PM2 as read-only. Enable <code>pm2.action</code> locally to operate processes.</div> : null}
    {processesQuery.isLoading ? <div className="p-12 text-center"><Loader2 className="mx-auto size-5 animate-spin text-zinc-500" /></div> : processesQuery.error ? <p className="p-4 text-xs text-red-400">{processesQuery.error.message}</p> : processes.length === 0 ? <div className="p-12 text-center"><Terminal className="mx-auto size-8 text-zinc-700" /><p className="mt-3 text-sm text-zinc-300">PM2 is available, but no applications are registered.</p><p className="mt-1 text-xs text-zinc-600">Start an application from the remote server terminal; it will appear here automatically.</p><Button className="mt-4" size="sm" disabled={!controlsReady || !online || !terminalAvailable} title={!terminalAvailable ? 'Writable terminal is not enabled on this agent' : undefined} onClick={() => navigate(`/terminal?${new URLSearchParams({ node: nodeID }).toString()}`)}><Terminal className="size-3.5" /> Open remote server terminal</Button></div> : <div className="overflow-x-auto"><table className="w-full min-w-[860px] text-xs"><thead><tr className="border-b border-zinc-800 text-zinc-500"><th className="px-4 py-2 text-left">App</th><th className="px-3 py-2 text-left">Status</th><th className="px-3 py-2 text-right">CPU</th><th className="px-3 py-2 text-right">Memory</th><th className="px-3 py-2 text-right">Uptime</th><th className="px-3 py-2 text-right">Restarts</th><th className="px-4 py-2 text-right">Actions</th></tr></thead><tbody>{processes.map((process) => { const pending = actionMutation.isPending && actionMutation.variables?.name === process.name; const onlineStatus = process.status === 'online'; return <tr key={process.id} className="border-b border-zinc-800/50 hover:bg-zinc-800/20"><td className="px-4 py-3"><p className="font-mono font-semibold text-zinc-200">{process.name}</p><p className="mt-0.5 max-w-[280px] truncate font-mono text-[9px] text-zinc-600" title={process.script}>{process.mode} · {process.script}</p></td><td className="px-3 py-3"><span className={cn('rounded px-1.5 py-0.5 text-[9px] font-semibold uppercase', onlineStatus ? 'bg-emerald-500/10 text-emerald-400' : 'bg-zinc-800 text-zinc-500')}>{process.status}</span></td><td className="px-3 py-3 text-right text-blue-400">{process.cpu.toFixed(1)}%</td><td className="px-3 py-3 text-right text-violet-400">{formatBytes(process.memory)}</td><td className="px-3 py-3 text-right text-zinc-400">{formatUptime(process.uptime)}</td><td className="px-3 py-3 text-right text-zinc-400">{process.restarts}</td><td className="px-4 py-3"><div className="flex justify-end gap-1"><Button variant="ghost" size="icon-xs" title="Logs" disabled={!online || !readAvailable} onClick={() => setLogProcess(logProcess === process.name ? null : process.name)}><FileClock className="size-3" /></Button><Button variant="outline" size="xs" disabled={!controlsReady || !online || !actionAvailable || actionMutation.isPending || onlineStatus} onClick={() => actionMutation.mutate({ name: process.name, action: 'start' })}><Play className="size-3" /> Start</Button><Button variant="outline" size="icon-xs" title="Reload" disabled={!controlsReady || !online || !actionAvailable || actionMutation.isPending || !onlineStatus} onClick={() => actionMutation.mutate({ name: process.name, action: 'reload' })}><RefreshCw className="size-3" /></Button><Button variant="outline" size="icon-xs" title="Restart" disabled={!controlsReady || !online || !actionAvailable || actionMutation.isPending || !onlineStatus} onClick={() => actionMutation.mutate({ name: process.name, action: 'restart' })}>{pending ? <Loader2 className="size-3 animate-spin" /> : <RotateCw className="size-3" />}</Button><Button variant="outline" size="icon-xs" title="Stop" disabled={!controlsReady || !online || !actionAvailable || actionMutation.isPending || !onlineStatus} onClick={() => { if (window.confirm(`Stop PM2 process ${process.name}?`)) actionMutation.mutate({ name: process.name, action: 'stop' }) }}><Square className="size-3" /></Button></div></td></tr> })}</tbody></table></div>}
  </CardContent></Card>
  {logProcess && <Card className="border-zinc-800 bg-zinc-900/80"><CardHeader className="flex-row items-center justify-between"><div><CardTitle className="font-mono text-sm text-zinc-200">{logProcess} logs</CardTitle><p className="mt-1 text-[10px] text-zinc-500">Last 200 PM2 log lines</p></div><Button variant="ghost" size="xs" disabled={!online || !readAvailable || logsQuery.isFetching} onClick={() => logsQuery.refetch()}><RefreshCw className={cn('size-3', logsQuery.isFetching && 'animate-spin')} /> Refresh</Button></CardHeader><CardContent>{logsQuery.isLoading ? <Loader2 className="mx-auto size-4 animate-spin text-zinc-500" /> : logsQuery.error ? <p className="text-xs text-red-400">{logsQuery.error.message}</p> : <pre className="max-h-[520px] overflow-auto whitespace-pre-wrap rounded-xl border border-zinc-800 bg-zinc-950 p-4 font-mono text-[11px] leading-5 text-zinc-300">{logsQuery.data?.logs || 'No log output.'}</pre>}</CardContent></Card>}
  </div>
}

function PM2Unavailable({ title, detail }: { title: string; detail: string }) {
  return <Card className="border-amber-500/30 bg-amber-500/[0.07]"><CardContent className="p-5 text-xs text-amber-200"><p className="font-semibold">{title}</p><p className="mt-1 text-amber-200/80">{detail}</p></CardContent></Card>
}
