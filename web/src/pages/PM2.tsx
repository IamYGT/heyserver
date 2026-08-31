import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  Play,
  Square,
  RotateCcw,
  Trash2,
  Cpu,
  MemoryStick,
  RefreshCw,
  Plus,
  ScrollText,
  Save,
  Rocket,
  Loader2,
  AlertTriangle,
} from 'lucide-react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { api, ApiError } from '@/lib/api'
import { toast } from 'sonner'
import type { PM2Process } from '@/lib/types'
import EmptyState from '@/components/EmptyState'
import { DependencyRemediation } from '@/components/DependencyRemediation'
import {
  INTEGRATION_HEALTHY,
  INTEGRATION_NOT_CONFIGURED,
  INTEGRATION_UNAVAILABLE,
  integrationStateFromObservation,
  integrationStatePresentation,
  normalizeIntegrationState,
  type IntegrationState,
} from '@/lib/integrationState'

function formatMemory(mb: number): string {
  if (mb >= 1024) return `${(mb / 1024).toFixed(1)} GB`
  return `${mb.toFixed(0)} MB`
}

function formatUptime(seconds: number): string {
  const days = Math.floor(seconds / 86400)
  const hours = Math.floor((seconds % 86400) / 3600)
  const mins = Math.floor((seconds % 3600) / 60)
  if (days > 0) return `${days}d ${hours}h`
  if (hours > 0) return `${hours}h ${mins}m`
  return `${mins}m`
}

const statusConfig: Record<PM2Process['status'], { label: string; color: string }> = {
  online: { label: 'Online', color: 'bg-green-500/10 text-green-400 border-green-500/20' },
  stopped: { label: 'Stopped', color: 'bg-zinc-500/10 text-zinc-400 border-zinc-700' },
  errored: { label: 'Errored', color: 'bg-red-500/10 text-red-400 border-red-500/20' },
  launching: { label: 'Launching', color: 'bg-amber-500/10 text-amber-400 border-amber-500/20' },
  stopping: { label: 'Stopping', color: 'bg-orange-500/10 text-orange-400 border-orange-500/20' },
}

interface PM2LogsResponse {
  output: string
}

interface PM2ProcessInventory {
  processes: PM2Process[]
  state: IntegrationState
  error?: string
}

/**
 * Accept the explicit inventory envelope while keeping the page readable
 * during a rolling upgrade where the API may still return a bare process
 * array. A successful bare array is a healthy observation, including `[]`;
 * an object without a canonical state is unavailable rather than healthy.
 */
function adaptPM2ProcessInventory(payload: unknown): PM2ProcessInventory {
  if (Array.isArray(payload)) {
    return {
      processes: payload as PM2Process[],
      state: integrationStateFromObservation(true, true),
    }
  }

  if (payload && typeof payload === 'object') {
    const candidate = payload as { processes?: unknown; data?: unknown; state?: unknown; error?: unknown }
    const rawProcesses = Array.isArray(candidate.processes)
      ? candidate.processes
      : Array.isArray(candidate.data)
        ? candidate.data
        : null
    const state = normalizeIntegrationState(candidate.state)
    if (rawProcesses && state) {
      return {
        processes: rawProcesses as PM2Process[],
        state,
        error: typeof candidate.error === 'string' ? candidate.error : undefined,
      }
    }
  }

  return {
    processes: [],
    state: integrationStateFromObservation(true, false),
  }
}

function pm2ErrorState(error: unknown): IntegrationState {
  return error instanceof ApiError && error.status === 503
    ? INTEGRATION_NOT_CONFIGURED
    : INTEGRATION_UNAVAILABLE
}

type PM2ProcessAction = 'start' | 'stop' | 'restart' | 'reload' | 'delete'

// ── Log Viewer Dialog ─────────────────────────────────────────────────────────

interface LogViewerProps {
  process: PM2Process
  onClose: () => void
}

function LogViewer({ process, onClose }: LogViewerProps) {
  const { data, isLoading, isError, error, refetch, isFetching } = useQuery<PM2LogsResponse>({
    queryKey: ['pm2', 'logs', process.id],
    queryFn: () => api.get<PM2LogsResponse>(`/pm2/processes/${process.id}/logs?lines=100`),
    staleTime: 0,
  })

  const logs = data?.output ? data.output.split('\n') : []

  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent className="bg-zinc-900 border-zinc-700 max-w-5xl w-full max-h-[85vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle className="text-white flex items-center gap-2">
            <ScrollText className="w-4 h-4 text-blue-400" />
            Logs — {process.name}
          </DialogTitle>
          <DialogDescription className="text-zinc-500 text-xs font-mono">
            {process.script}
          </DialogDescription>
        </DialogHeader>

        <div className="bg-zinc-950 border border-zinc-800 rounded-lg overflow-hidden">
          {/* toolbar */}
          <div className="flex items-center justify-between px-3 py-2 border-b border-zinc-800 bg-zinc-900">
            <span className="text-zinc-500 text-xs">{logs.length} lines</span>
            <Button
              variant="ghost"
              size="sm"
              onClick={() => refetch()}
              disabled={isFetching}
              className="h-7 text-zinc-400 hover:text-white px-2"
            >
              {isFetching
                ? <Loader2 className="w-3.5 h-3.5 animate-spin" />
                : <RefreshCw className="w-3.5 h-3.5" />}
              <span className="ml-1 text-xs">Refresh</span>
            </Button>
          </div>

          {/* log output */}
          <div className="h-80 overflow-y-auto p-3 font-mono text-xs">
            {isLoading ? (
              <div className="flex items-center justify-center h-full gap-2 text-zinc-500">
                <Loader2 className="w-4 h-4 animate-spin" />
                Loading…
              </div>
            ) : isError ? (
              <div className="flex h-full flex-col items-center justify-center px-4 text-center">
                <AlertTriangle className="size-5 text-red-400" />
                <p className="mt-2 text-sm text-red-300">Process logs could not be loaded.</p>
                <p className="mt-1 text-xs text-zinc-600">{error.message}</p>
                <Button type="button" variant="outline" size="sm" className="mt-4 border-red-500/30 text-red-200" onClick={() => { void refetch() }} disabled={isFetching}>
                  <RefreshCw className={`mr-2 size-3.5 ${isFetching ? 'animate-spin' : ''}`} />Retry
                </Button>
              </div>
            ) : logs.length === 0 ? (
              <div className="flex items-center justify-center h-full text-zinc-600">
                No log output
              </div>
            ) : (
              <div className="space-y-0.5">
                {logs.map((line, i) => (
                  <div key={i} className="text-zinc-300 leading-relaxed break-all hover:bg-zinc-900/60 px-1 -mx-1 rounded">
                    {line}
                  </div>
                ))}
              </div>
            )}
          </div>
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={onClose} className="border-zinc-700 text-zinc-300">
            Close
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ── New Process Dialog ────────────────────────────────────────────────────────

interface NewProcessRequest {
  name: string
  script: string
  cwd: string
  instances: number
  exec_mode: 'fork' | 'cluster'
  node_env: 'production' | 'development'
}

interface NewProcessDialogProps {
  open: boolean
  onClose: () => void
  onSubmit: (request: NewProcessRequest) => void
  isPending: boolean
}

function NewProcessDialog({ open, onClose, onSubmit, isPending }: NewProcessDialogProps) {
  const [form, setForm] = useState<NewProcessRequest>({ name: '', script: '', cwd: '', instances: 1, exec_mode: 'fork', node_env: 'production' })
  const canSubmit = form.name.trim() !== '' && form.script.trim() !== '' && form.instances >= 1 && form.instances <= 64

  return (
    <Dialog open={open} onOpenChange={(nextOpen) => { if (!nextOpen) onClose() }}>
      <DialogContent className="bg-zinc-900 border-zinc-700 max-w-xl max-h-[85vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle className="text-white flex items-center gap-2">
            <Rocket className="w-4 h-4 text-blue-400" />
            New PM2 Process
          </DialogTitle>
          <DialogDescription className="text-zinc-400">
            Start a script under the configured unprivileged PM2 account. Script and working-directory paths must remain under an installation-approved <span className="font-mono text-zinc-300">HSERVER_PM2_ALLOWED_ROOTS</span> entry.
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-4 py-2">
          <div className="space-y-2"><Label htmlFor="pm2-name" className="text-zinc-400">Name</Label><Input id="pm2-name" value={form.name} onChange={(event) => setForm(current => ({ ...current, name: event.target.value }))} placeholder="api" className="border-zinc-700 bg-zinc-800 text-white" /></div>
          <div className="space-y-2"><Label htmlFor="pm2-script" className="text-zinc-400">Script path</Label><Input id="pm2-script" value={form.script} onChange={(event) => setForm(current => ({ ...current, script: event.target.value }))} placeholder="/var/www/example.com/server.js" className="border-zinc-700 bg-zinc-800 font-mono text-white" /></div>
          <div className="space-y-2"><Label htmlFor="pm2-cwd" className="text-zinc-400">Working directory (optional)</Label><Input id="pm2-cwd" value={form.cwd} onChange={(event) => setForm(current => ({ ...current, cwd: event.target.value }))} placeholder="/var/www/example.com" className="border-zinc-700 bg-zinc-800 font-mono text-white" /></div>
          <div className="grid grid-cols-3 gap-3">
            <div className="space-y-2"><Label htmlFor="pm2-mode" className="text-zinc-400">Mode</Label><select id="pm2-mode" value={form.exec_mode} onChange={(event) => setForm(current => ({ ...current, exec_mode: event.target.value as 'fork' | 'cluster' }))} className="h-9 w-full rounded-md border border-zinc-700 bg-zinc-800 px-3 text-sm text-white"><option value="fork">Fork</option><option value="cluster">Cluster</option></select></div>
            <div className="space-y-2"><Label htmlFor="pm2-instances" className="text-zinc-400">Instances</Label><Input id="pm2-instances" type="number" min={1} max={64} value={form.instances} onChange={(event) => setForm(current => ({ ...current, instances: Number(event.target.value) }))} className="border-zinc-700 bg-zinc-800 text-white" /></div>
            <div className="space-y-2"><Label htmlFor="pm2-node-env" className="text-zinc-400">Environment</Label><select id="pm2-node-env" value={form.node_env} onChange={(event) => setForm(current => ({ ...current, node_env: event.target.value as 'production' | 'development' }))} className="h-9 w-full rounded-md border border-zinc-700 bg-zinc-800 px-3 text-sm text-white"><option value="production">Production</option><option value="development">Development</option></select></div>
          </div>
        </div>
        <DialogFooter className="gap-2">
          <Button
            variant="outline"
            onClick={onClose}
            disabled={isPending}
            className="border-zinc-700 text-zinc-300"
          >
            Cancel
          </Button>
          <Button
            onClick={() => onSubmit({ ...form, name: form.name.trim(), script: form.script.trim(), cwd: form.cwd.trim() })}
            disabled={isPending || !canSubmit}
            className="bg-blue-600 hover:bg-blue-500 text-white"
          >
            {isPending ? <Loader2 className="w-4 h-4 animate-spin mr-2" /> : <Rocket className="w-4 h-4 mr-2" />}
            Start Process
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ── Main Page ─────────────────────────────────────────────────────────────────

export default function PM2() {
  const queryClient = useQueryClient()
  const [logProcess, setLogProcess] = useState<PM2Process | null>(null)
  const [newProcessOpen, setNewProcessOpen] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<PM2Process | null>(null)

  const processesQuery = useQuery<PM2ProcessInventory>({
    queryKey: ['pm2', 'processes'],
    queryFn: async () => adaptPM2ProcessInventory(await api.get<unknown>('/pm2/processes')),
    refetchInterval: 5000,
  })
  const inventory = processesQuery.data
  const availabilityState = processesQuery.isError
    ? pm2ErrorState(processesQuery.error)
    : inventory?.state ?? null
  const availability = availabilityState ? integrationStatePresentation(availabilityState) : null
  const remediationState = availabilityState === INTEGRATION_NOT_CONFIGURED ? 'not-configured' : 'unavailable'
  const inventoryHealthy = availabilityState === INTEGRATION_HEALTHY
  const processes = inventoryHealthy ? inventory?.processes : undefined
  const inventoryError = processesQuery.isError
    ? processesQuery.error.message
    : inventory?.error

  const actionMutation = useMutation({
    mutationFn: ({ id, action }: { id: number; action: PM2ProcessAction }) =>
      api.post(`/pm2/processes/${id}/${action}`),
    onSuccess: (_, { action }) => {
      queryClient.invalidateQueries({ queryKey: ['pm2', 'processes'] })
      toast.success(`Process ${action} successful`)
    },
    onError: (_, { action }) => toast.error(`Failed to ${action} process`),
  })

  const deleteProcess = useMutation({
    mutationFn: (id: number) => api.post(`/pm2/processes/${id}/delete`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['pm2', 'processes'] })
      setDeleteTarget(null)
      toast.success('Process deleted')
    },
    onError: () => toast.error('Failed to delete process'),
  })

  const saveMutation = useMutation({
    mutationFn: () => api.post('/pm2/save'),
    onSuccess: () => toast.success('PM2 process list saved'),
    onError: () => toast.error('Failed to save PM2 list'),
  })

  const newProcessMutation = useMutation({
    mutationFn: (request: NewProcessRequest) => api.post('/pm2/deploy', request),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['pm2', 'processes'] })
      toast.success('PM2 process started')
      setNewProcessOpen(false)
    },
    onError: (error: Error) => toast.error(error.message || 'Failed to start PM2 process'),
  })

  const totalOnline = processes?.filter((p) => p.status === 'online').length ?? 0
  const totalCpu = processes?.reduce((sum, p) => sum + p.cpu, 0) ?? 0
  const totalMem = processes?.reduce((sum, p) => sum + p.memory, 0) ?? 0

  return (
    <div className="space-y-6">
      {/* Log viewer dialog */}
      {logProcess && (
        <LogViewer process={logProcess} onClose={() => setLogProcess(null)} />
      )}

      {newProcessOpen && <NewProcessDialog open onClose={() => setNewProcessOpen(false)} onSubmit={(request) => newProcessMutation.mutate(request)} isPending={newProcessMutation.isPending} />}

      <Dialog
        open={deleteTarget !== null}
        onOpenChange={(open) => {
          if (!open && !deleteProcess.isPending) setDeleteTarget(null)
        }}
      >
        <DialogContent className="border-zinc-700 bg-zinc-900 text-white">
          <DialogHeader>
            <DialogTitle>Delete PM2 process?</DialogTitle>
            <DialogDescription className="text-zinc-400">
              The process will stop and leave the current PM2 list. Application files are kept. Use Save after removal to update the resurrected process list.
            </DialogDescription>
          </DialogHeader>
          <div className="rounded-lg border border-zinc-800 bg-zinc-950 px-3 py-2">
            <p className="text-sm font-medium text-zinc-200">{deleteTarget?.name}</p>
            <p className="mt-1 break-all font-mono text-xs text-zinc-500">{deleteTarget?.script}</p>
          </div>
          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              className="border-zinc-700 text-zinc-300"
              disabled={deleteProcess.isPending}
              onClick={() => setDeleteTarget(null)}
            >
              Cancel
            </Button>
            <Button
              type="button"
              variant="destructive"
              disabled={!deleteTarget || deleteProcess.isPending}
              onClick={() => deleteTarget && deleteProcess.mutate(deleteTarget.id)}
            >
              {deleteProcess.isPending ? <Loader2 className="mr-2 size-4 animate-spin" /> : <Trash2 className="mr-2 size-4" />}
              {deleteProcess.isPending ? 'Deleting…' : 'Delete Process'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Header */}
      <div className="flex items-center justify-between flex-wrap gap-3">
        <div>
          <h2 className="text-white text-xl font-bold">PM2 Processes</h2>
          <p className="text-zinc-500 text-sm mt-0.5">
            {processesQuery.isLoading
              ? 'Checking PM2 availability…'
              : availabilityState === INTEGRATION_NOT_CONFIGURED
                ? 'PM2 integration not configured'
                : availabilityState === INTEGRATION_UNAVAILABLE
                  ? 'PM2 inventory unavailable'
                  : processes
                    ? `${totalOnline}/${processes.length} online`
                    : 'Node.js process manager'}
          </p>
        </div>
        <div className="flex items-center gap-2">
          {availability && (
            <Badge
              data-testid="pm2-availability-state"
              aria-label={`PM2 availability: ${availability.label}`}
              className={`text-xs border ${availability.tone === 'healthy'
                ? 'border-green-500/20 bg-green-500/10 text-green-400'
                : availability.tone === 'neutral'
                  ? 'border-zinc-700 bg-zinc-800/60 text-zinc-400'
                  : 'border-red-500/20 bg-red-500/10 text-red-400'}`}
            >
              {availability.label}
            </Badge>
          )}
          <Button
            variant="outline"
            onClick={() => saveMutation.mutate()}
            disabled={saveMutation.isPending || processesQuery.isLoading || !inventoryHealthy}
            className="border-zinc-700 text-zinc-300 hover:text-white"
          >
            {saveMutation.isPending
              ? <Loader2 className="w-4 h-4 mr-2 animate-spin" />
              : <Save className="w-4 h-4 mr-2" />}
            Save
          </Button>
          <Button className="bg-blue-600 hover:bg-blue-500 text-white" onClick={() => setNewProcessOpen(true)} disabled={processesQuery.isLoading || !inventoryHealthy}>
            <Plus className="w-4 h-4 mr-2" />
            New Process
          </Button>
        </div>
      </div>

      {/* Summary cards */}
      {processes && (
        <div className="grid grid-cols-3 gap-3">
          <Card className="bg-zinc-900 border-zinc-800">
            <CardContent className="py-4 text-center">
              <p className="text-zinc-500 text-xs uppercase tracking-wide">Online</p>
              <p className="text-white text-xl sm:text-2xl font-bold mt-1">{totalOnline}</p>
            </CardContent>
          </Card>
          <Card className="bg-zinc-900 border-zinc-800">
            <CardContent className="py-4 text-center">
              <p className="text-zinc-500 text-xs uppercase tracking-wide">CPU</p>
              <p className="text-white text-xl sm:text-2xl font-bold mt-1">{totalCpu.toFixed(1)}%</p>
            </CardContent>
          </Card>
          <Card className="bg-zinc-900 border-zinc-800">
            <CardContent className="py-4 text-center">
              <p className="text-zinc-500 text-xs uppercase tracking-wide">Memory</p>
              <p className="text-white text-xl sm:text-2xl font-bold mt-1">{formatMemory(totalMem)}</p>
            </CardContent>
          </Card>
        </div>
      )}

      {/* Process table */}
      <Card className="bg-zinc-900 border-zinc-800">
        <CardHeader className="pb-0 pt-4">
          <CardTitle className="text-white text-sm font-medium flex items-center gap-2">
            <RefreshCw className="w-3.5 h-3.5 text-blue-400" />
            Process List
            <span className="text-zinc-600 font-normal text-xs ml-1">(auto-refresh every 5s)</span>
          </CardTitle>
        </CardHeader>
        <CardContent className="p-0 mt-3">
          {processesQuery.isLoading ? (
            <div className="p-4 space-y-2">
              {Array.from({ length: 5 }).map((_, i) => (
                <Skeleton key={i} className="h-12 w-full bg-zinc-800" />
              ))}
            </div>
          ) : !inventoryHealthy ? (
            <div className="p-4">
              <DependencyRemediation
                state={remediationState}
                title={availabilityState === INTEGRATION_NOT_CONFIGURED ? 'PM2 integration is not configured' : 'PM2 inventory is unavailable'}
                summary={availabilityState === INTEGRATION_NOT_CONFIGURED
                  ? 'Heyserver requires an explicit unprivileged PM2 owner and never starts a root-owned PM2 daemon automatically.'
                  : 'The configured PM2 identity or executable could not return process inventory, so PM2 mutations remain paused.'}
                error={inventoryError}
                retry={() => { void processesQuery.refetch() }}
                retrying={processesQuery.isFetching}
                steps={availabilityState === INTEGRATION_NOT_CONFIGURED ? [
                  'Install and start PM2 under the unprivileged Linux account that owns the Node.js applications.',
                  <>Set <code className="text-zinc-100">HSERVER_PM2_USER</code>, <code className="text-zinc-100">HSERVER_PM2_HOME</code>, and <code className="text-zinc-100">HSERVER_PM2_BIN</code> in <code className="text-zinc-100">/etc/hserver/hserver.env</code>.</>,
                  <>Restart <code className="text-zinc-100">hserver</code>, then retry detection. Existing PM2 processes are not modified by detection.</>,
                ] : [
                  'Verify the configured PM2 executable exists for the selected unprivileged account.',
                  <>Check <code className="text-zinc-100">systemctl status pm2-&lt;APP_USER&gt;</code> and the configured PM2 home.</>,
                  'Correct the owner, binary, home, or daemon state, then retry detection.',
                ]}
              />
            </div>
          ) : processes && processes.length > 0 ? (
            <div className="overflow-x-auto">
              <Table className="min-w-[760px]">
                <TableHeader>
                  <TableRow className="border-zinc-800 hover:bg-transparent">
                    <TableHead className="text-zinc-500 text-xs font-medium">Name</TableHead>
                    <TableHead className="text-zinc-500 text-xs font-medium">Status</TableHead>
                    <TableHead className="text-zinc-500 text-xs font-medium">Mode</TableHead>
                    <TableHead className="text-zinc-500 text-xs font-medium">
                      <div className="flex items-center gap-1"><Cpu className="w-3 h-3" /> CPU</div>
                    </TableHead>
                    <TableHead className="text-zinc-500 text-xs font-medium">
                      <div className="flex items-center gap-1"><MemoryStick className="w-3 h-3" /> Mem</div>
                    </TableHead>
                    <TableHead className="text-zinc-500 text-xs font-medium">Uptime</TableHead>
                    <TableHead className="text-zinc-500 text-xs font-medium">Restarts</TableHead>
                    <TableHead className="text-zinc-500 text-xs font-medium text-right">Actions</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {processes.map((process) => {
                    const status = statusConfig[process.status] ?? { label: process.status, color: 'bg-zinc-500/10 text-zinc-400 border-zinc-700' }
                    return (
                      <TableRow
                        key={process.id}
                        className="border-zinc-800 hover:bg-zinc-800/50 transition-colors cursor-pointer"
                        onClick={() => setLogProcess(process)}
                      >
                        <TableCell>
                          <div>
                            <span className="text-white text-sm font-medium">{process.name}</span>
                            <span className="block text-zinc-600 text-xs font-mono truncate max-w-32">
                              {process.script}
                            </span>
                          </div>
                        </TableCell>
                        <TableCell>
                          <Badge className={`text-xs border ${status.color}`}>
                            {status.label}
                          </Badge>
                        </TableCell>
                        <TableCell>
                          <span className="text-zinc-400 text-xs capitalize">
                            {process.mode}
                            {process.instances ? ` ×${process.instances}` : ''}
                          </span>
                        </TableCell>
                        <TableCell>
                          <span className={`text-sm font-mono ${process.cpu > 50 ? 'text-amber-400' : 'text-zinc-300'}`}>
                            {process.cpu.toFixed(1)}%
                          </span>
                        </TableCell>
                        <TableCell>
                          <span className="text-zinc-300 text-sm font-mono">
                            {formatMemory(process.memory)}
                          </span>
                        </TableCell>
                        <TableCell>
                          <span className="text-zinc-400 text-xs">
                            {process.uptime ? formatUptime(process.uptime) : '—'}
                          </span>
                        </TableCell>
                        <TableCell>
                          <span className={`text-xs font-mono ${process.restarts > 5 ? 'text-amber-400' : 'text-zinc-400'}`}>
                            {process.restarts}
                          </span>
                        </TableCell>
                        <TableCell className="text-right" onClick={(e) => e.stopPropagation()}>
                          <div className="flex items-center justify-end gap-1">
                            {/* Logs */}
                            <Tooltip>
                              <TooltipTrigger>
                                <Button
                                  aria-label={`View logs for ${process.name}`}
                                  variant="ghost"
                                  size="icon"
                                  className="h-7 w-7 text-zinc-500 hover:text-cyan-400 hover:bg-cyan-400/10"
                                  onClick={() => setLogProcess(process)}
                                >
                                  <ScrollText className="w-3.5 h-3.5" />
                                </Button>
                              </TooltipTrigger>
                              <TooltipContent>View Logs</TooltipContent>
                            </Tooltip>

                            {/* Start / Stop */}
                            {process.status === 'online' ? (
                              <Tooltip>
                                <TooltipTrigger>
                                  <Button
                                    aria-label={`Stop ${process.name}`}
                                    variant="ghost"
                                    size="icon"
                                    className="h-7 w-7 text-zinc-500 hover:text-amber-400 hover:bg-amber-400/10"
                                    onClick={() => actionMutation.mutate({ id: process.id, action: 'stop' })}
                                  >
                                    <Square className="w-3.5 h-3.5" />
                                  </Button>
                                </TooltipTrigger>
                                <TooltipContent>Stop</TooltipContent>
                              </Tooltip>
                            ) : (
                              <Tooltip>
                                <TooltipTrigger>
                                  <Button
                                    aria-label={`Start ${process.name}`}
                                    variant="ghost"
                                    size="icon"
                                    className="h-7 w-7 text-zinc-500 hover:text-green-400 hover:bg-green-400/10"
                                    onClick={() => actionMutation.mutate({ id: process.id, action: 'start' })}
                                  >
                                    <Play className="w-3.5 h-3.5" />
                                  </Button>
                                </TooltipTrigger>
                                <TooltipContent>Start</TooltipContent>
                              </Tooltip>
                            )}

                            {/* Restart */}
                            <Tooltip>
                              <TooltipTrigger>
                                <Button
                                  aria-label={`Restart ${process.name}`}
                                  variant="ghost"
                                  size="icon"
                                  className="h-7 w-7 text-zinc-500 hover:text-blue-400 hover:bg-blue-400/10"
                                  onClick={() => actionMutation.mutate({ id: process.id, action: 'restart' })}
                                >
                                  <RotateCcw className="w-3.5 h-3.5" />
                                </Button>
                              </TooltipTrigger>
                              <TooltipContent>Restart</TooltipContent>
                            </Tooltip>

                            {/* Delete */}
                            <Tooltip>
                              <TooltipTrigger>
                                <Button
                                  aria-label={`Delete ${process.name}`}
                                  variant="ghost"
                                  size="icon"
                                  className="h-7 w-7 text-zinc-500 hover:text-red-400 hover:bg-red-400/10"
                                  onClick={() => setDeleteTarget(process)}
                                >
                                  <Trash2 className="w-3.5 h-3.5" />
                                </Button>
                              </TooltipTrigger>
                              <TooltipContent>Delete</TooltipContent>
                            </Tooltip>
                          </div>
                        </TableCell>
                      </TableRow>
                    )
                  })}
                </TableBody>
              </Table>
            </div>
          ) : (
            <EmptyState
              icon={Cpu}
              title="No PM2 processes running"
              description="Start a new Node.js process to manage it here."
              actionLabel="New Process"
              onAction={() => setNewProcessOpen(true)}
            />
          )}
        </CardContent>
      </Card>
    </div>
  )
}
