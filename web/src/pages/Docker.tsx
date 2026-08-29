import { Fragment, useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  Container,
  Play,
  Square,
  RotateCcw,
  Trash2,
  RefreshCw,
  FileText,
  Download,
  Package,
  AlertTriangle,
} from 'lucide-react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
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
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { api } from '@/lib/api'
import { toast } from 'sonner'
import type { DockerStatus, DockerContainer, DockerImage, DockerLogs } from '@/lib/types'
import { DependencyRemediation } from '@/components/DependencyRemediation'

function formatBytes(bytes: number): string {
  if (bytes === 0) return '0 B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return `${(bytes / Math.pow(k, i)).toFixed(1)} ${sizes[i]}`
}

function formatDockerSize(size: DockerImage['size']): string {
  return typeof size === 'number' ? formatBytes(size) : size
}

const containerStatusConfig: Record<
  DockerContainer['status'],
  { label: string; color: string }
> = {
  running: { label: 'Running', color: 'bg-green-500/10 text-green-400 border-green-500/20' },
  stopped: { label: 'Stopped', color: 'bg-zinc-500/10 text-zinc-400 border-zinc-700' },
  exited: { label: 'Exited', color: 'bg-red-500/10 text-red-400 border-red-500/20' },
  paused: { label: 'Paused', color: 'bg-amber-500/10 text-amber-400 border-amber-500/20' },
  restarting: { label: 'Restarting', color: 'bg-blue-500/10 text-blue-400 border-blue-500/20' },
}

type DockerRemovalTarget =
  | { kind: 'container'; id: string; label: string }
  | { kind: 'image'; id: string; label: string }

type DockerContainerAction = 'start' | 'stop' | 'restart' | 'pause' | 'unpause' | 'remove'

function DockerLoadError({ title, error, retry, retrying }: { title: string; error: Error; retry: () => void; retrying: boolean }) {
  return (
    <div className="flex flex-col items-center justify-center px-4 py-10 text-center">
      <AlertTriangle className="size-5 text-red-400" />
      <p className="mt-2 text-sm text-red-300">{title}</p>
      <p className="mt-1 text-xs text-zinc-600">{error.message}</p>
      <Button type="button" variant="outline" size="sm" className="mt-4 border-red-500/30 text-red-200" onClick={retry} disabled={retrying}>
        <RefreshCw className={`mr-2 size-3.5 ${retrying ? 'animate-spin' : ''}`} />Retry
      </Button>
    </div>
  )
}

export default function Docker() {
  const queryClient = useQueryClient()
  const [expandedLogs, setExpandedLogs] = useState<Record<string, DockerLogs>>({})
  const [loadingLogs, setLoadingLogs] = useState<Record<string, boolean>>({})
  const [pullDialogOpen, setPullDialogOpen] = useState(false)
  const [pullImageName, setPullImageName] = useState('')
  const [removalTarget, setRemovalTarget] = useState<DockerRemovalTarget | null>(null)

  const statusQuery = useQuery<DockerStatus>({
    queryKey: ['docker', 'status'],
    queryFn: () => api.get<DockerStatus>('/docker/status'),
    refetchInterval: 10_000,
  })
  const status = statusQuery.data
  const dockerActionsReady = statusQuery.isSuccess && status?.running === true

  const containersQuery = useQuery<DockerContainer[]>({
    queryKey: ['docker', 'containers'],
    queryFn: () => api.get<DockerContainer[]>('/docker/containers'),
    refetchInterval: 5_000,
    enabled: status?.running ?? false,
  })
  const containers = containersQuery.data

  const imagesQuery = useQuery<DockerImage[]>({
    queryKey: ['docker', 'images'],
    queryFn: () => api.get<DockerImage[]>('/docker/images'),
    refetchInterval: 15_000,
    enabled: status?.running ?? false,
  })
  const images = imagesQuery.data

  const containerAction = useMutation({
    mutationFn: ({ id, action }: { id: string; action: DockerContainerAction }) =>
      api.post(`/docker/containers/${id}/${action}`),
    onSuccess: (_, { action }) => {
      queryClient.invalidateQueries({ queryKey: ['docker', 'containers'] })
      queryClient.invalidateQueries({ queryKey: ['docker', 'status'] })
      toast.success(`Container ${action} successful`)
    },
    onError: (_, { action }) => toast.error(`Failed to ${action} container`),
  })

  const removeContainer = useMutation({
    mutationFn: (id: string) => api.post(`/docker/containers/${id}/remove`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['docker', 'containers'] })
      queryClient.invalidateQueries({ queryKey: ['docker', 'status'] })
      setRemovalTarget(null)
      toast.success('Container removed')
    },
    onError: () => toast.error('Failed to remove container'),
  })

  const removeImage = useMutation({
    mutationFn: (id: string) => api.delete<void>(`/docker/images/${encodeURIComponent(id)}`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['docker', 'images'] })
      queryClient.invalidateQueries({ queryKey: ['docker', 'status'] })
      setRemovalTarget(null)
      toast.success('Image removed')
    },
    onError: () => toast.error('Failed to remove image'),
  })

  const pullImage = useMutation({
    mutationFn: (name: string) => api.post('/docker/images/pull', { name }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['docker', 'images'] })
      queryClient.invalidateQueries({ queryKey: ['docker', 'status'] })
      setPullDialogOpen(false)
      setPullImageName('')
      toast.success('Image pulled successfully')
    },
    onError: () => toast.error('Failed to pull image'),
  })

  const handlePullDialogOpenChange = (open: boolean) => {
    if (!open && pullImage.isPending) return
    setPullDialogOpen(open)
    if (!open) setPullImageName('')
  }

  const removalPending = removeContainer.isPending || removeImage.isPending

  const confirmRemoval = () => {
    if (!removalTarget || removalPending || !dockerActionsReady) return
    if (removalTarget.kind === 'container') {
      removeContainer.mutate(removalTarget.id)
      return
    }
    removeImage.mutate(removalTarget.id)
  }

  const toggleLogs = async (containerId: string) => {
    if (expandedLogs[containerId] !== undefined) {
      setExpandedLogs((prev) => {
        const next = { ...prev }
        delete next[containerId]
        return next
      })
      return
    }
    setLoadingLogs((prev) => ({ ...prev, [containerId]: true }))
    try {
      const logs = await api.get<DockerLogs>(`/docker/containers/${containerId}/logs?tail=200`)
      setExpandedLogs((prev) => ({ ...prev, [containerId]: logs }))
    } catch {
      toast.error('Failed to fetch logs')
    } finally {
      setLoadingLogs((prev) => ({ ...prev, [containerId]: false }))
    }
  }

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between flex-wrap gap-3">
        <div>
          <h2 className="text-white text-xl font-bold">Docker</h2>
          <p className="text-zinc-500 text-sm mt-0.5">
            {status
              ? status.installed
                ? `${status.containersRunning}/${status.containersTotal} containers running`
                : 'Docker not installed'
              : 'Container management'}
          </p>
        </div>
        <Button
          className="bg-blue-600 hover:bg-blue-500 text-white"
          onClick={() => setPullDialogOpen(true)}
          disabled={!dockerActionsReady}
        >
          <Download className="w-4 h-4 mr-2" />
          Pull Image
        </Button>
      </div>

      {/* Status cards */}
      {statusQuery.isLoading ? (
        <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
          {Array.from({ length: 4 }).map((_, i) => (
            <Skeleton key={i} className="h-20 w-full bg-zinc-800" />
          ))}
        </div>
      ) : statusQuery.isError ? (
        <DependencyRemediation
          title="Docker status is unavailable"
          summary="HServer could not determine whether Docker is installed or running, so container mutations remain paused."
          error={statusQuery.error.message}
          retry={() => { void statusQuery.refetch() }}
          retrying={statusQuery.isFetching}
          steps={[
            <>Run <code className="text-zinc-100">sudo ./doctor.sh installed</code> from the HServer release directory.</>,
            <>Verify <code className="text-zinc-100">docker version</code> and <code className="text-zinc-100">systemctl status docker</code> on this host.</>,
            'Correct the host service or socket access, then retry detection. HServer will not install packages automatically.',
          ]}
        />
      ) : status ? (
        <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
          <Card className="bg-zinc-900 border-zinc-800">
            <CardContent className="py-4 text-center">
              <p className="text-zinc-500 text-xs uppercase tracking-wide">Docker</p>
              <p className="mt-1">
                <Badge
                  className={
                    status.installed
                      ? 'bg-green-500/10 text-green-400 border-green-500/20'
                      : 'bg-red-500/10 text-red-400 border-red-500/20'
                  }
                >
                  {status.installed ? 'Installed' : 'Not Installed'}
                </Badge>
              </p>
              {status.version && (
                <p className="text-zinc-600 text-xs mt-1 font-mono">{status.version}</p>
              )}
            </CardContent>
          </Card>
          <Card className="bg-zinc-900 border-zinc-800">
            <CardContent className="py-4 text-center">
              <p className="text-zinc-500 text-xs uppercase tracking-wide">Daemon</p>
              <p className="mt-1">
                <Badge
                  className={
                    status.running
                      ? 'bg-green-500/10 text-green-400 border-green-500/20'
                      : 'bg-zinc-500/10 text-zinc-400 border-zinc-700'
                  }
                >
                  {status.running ? 'Running' : 'Stopped'}
                </Badge>
              </p>
            </CardContent>
          </Card>
          <Card className="bg-zinc-900 border-zinc-800">
            <CardContent className="py-4 text-center">
              <p className="text-zinc-500 text-xs uppercase tracking-wide">Containers</p>
              <p className="text-white text-2xl font-bold mt-1">{status.containersTotal}</p>
              <p className="text-zinc-500 text-xs">{status.containersRunning} running</p>
            </CardContent>
          </Card>
          <Card className="bg-zinc-900 border-zinc-800">
            <CardContent className="py-4 text-center">
              <p className="text-zinc-500 text-xs uppercase tracking-wide">Images</p>
              <p className="text-white text-2xl font-bold mt-1">{status.imageCount}</p>
            </CardContent>
          </Card>
        </div>
      ) : null}

      {status && !status.installed && (
        <DependencyRemediation
          state="not-configured"
          title="Docker is not installed"
          summary="Container management is optional. HServer leaves host package installation under the operator's control."
          retry={() => { void statusQuery.refetch() }}
          retrying={statusQuery.isFetching}
          steps={[
            'Install Docker Engine using the supported packages for this Linux distribution.',
            <>Enable the daemon with <code className="text-zinc-100">systemctl enable --now docker</code>.</>,
            'Return here and retry detection; container controls remain disabled until the daemon is observed running.',
          ]}
        />
      )}

      {status?.installed && !status.running && (
        <DependencyRemediation
          state="stopped"
          title="Docker is installed but the daemon is stopped"
          summary="Images and containers cannot be inspected safely until the local Docker daemon is available."
          retry={() => { void statusQuery.refetch() }}
          retrying={statusQuery.isFetching}
          steps={[
            <>Inspect the failure with <code className="text-zinc-100">systemctl status docker</code>.</>,
            <>After correcting the cause, run <code className="text-zinc-100">systemctl start docker</code>.</>,
            'Retry detection. HServer will enable inventory and mutations only after the daemon reports running.',
          ]}
        />
      )}

      {/* Containers table */}
      <Card className="bg-zinc-900 border-zinc-800">
        <CardHeader className="pb-0 pt-4">
          <CardTitle className="text-white text-sm font-medium flex items-center gap-2">
            <Container className="w-3.5 h-3.5 text-blue-400" />
            Containers
            <span className="text-zinc-600 font-normal text-xs ml-1">(auto-refresh every 5s)</span>
            <RefreshCw className="w-3 h-3 text-zinc-600 ml-auto" />
          </CardTitle>
        </CardHeader>
        <CardContent className="p-0 mt-3">
          {statusQuery.isError && containers === undefined ? (
            <div className="px-4 py-10 text-center text-sm text-zinc-500">Container inventory is paused until Docker status is available.</div>
          ) : containersQuery.isLoading ? (
            <div className="p-4 space-y-2">
              {Array.from({ length: 4 }).map((_, i) => (
                <Skeleton key={i} className="h-12 w-full bg-zinc-800" />
              ))}
            </div>
          ) : containersQuery.isError ? (
            <DockerLoadError title="Containers could not be loaded." error={containersQuery.error} retry={() => { void containersQuery.refetch() }} retrying={containersQuery.isFetching} />
          ) : containers && containers.length > 0 ? (
            <div className="overflow-x-auto">
              <Table className="min-w-[640px]">
                <TableHeader>
                  <TableRow className="border-zinc-800 hover:bg-transparent">
                    <TableHead className="text-zinc-500 text-xs font-medium">Name</TableHead>
                    <TableHead className="text-zinc-500 text-xs font-medium">Image</TableHead>
                    <TableHead className="text-zinc-500 text-xs font-medium">Status</TableHead>
                    <TableHead className="text-zinc-500 text-xs font-medium">Ports</TableHead>
                    <TableHead className="text-zinc-500 text-xs font-medium">CPU</TableHead>
                    <TableHead className="text-zinc-500 text-xs font-medium">Memory</TableHead>
                    <TableHead className="text-zinc-500 text-xs font-medium text-right">
                      Actions
                    </TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {containers.map((container) => {
                    const sc = containerStatusConfig[container.status] ?? containerStatusConfig.stopped
                    const logsOpen = expandedLogs[container.id] !== undefined
                    return (
                      <Fragment key={container.id}>
                        <TableRow
                          className="border-zinc-800 hover:bg-zinc-800/50 transition-colors"
                        >
                          <TableCell>
                            <span className="text-white text-sm font-medium font-mono">
                              {container.name}
                            </span>
                          </TableCell>
                          <TableCell>
                            <span className="text-zinc-400 text-xs font-mono truncate max-w-40 block">
                              {container.image}
                            </span>
                          </TableCell>
                          <TableCell>
                            <Badge className={`text-xs border ${sc.color}`}>{sc.label}</Badge>
                          </TableCell>
                          <TableCell>
                            <div className="flex flex-wrap gap-1">
                              {container.ports.length > 0 ? (
                                container.ports.map((p) => (
                                  <span
                                    key={p}
                                    className="text-xs font-mono text-zinc-400 bg-zinc-800 px-1.5 py-0.5 rounded"
                                  >
                                    {p}
                                  </span>
                                ))
                              ) : (
                                <span className="text-zinc-600 text-xs">—</span>
                              )}
                            </div>
                          </TableCell>
                          <TableCell>
                            <span
                              className={`text-sm font-mono ${container.cpuPercent > 50 ? 'text-amber-400' : 'text-zinc-300'}`}
                            >
                              {container.cpuPercent.toFixed(1)}%
                            </span>
                          </TableCell>
                          <TableCell>
                            <span className="text-zinc-300 text-xs font-mono">
                              {formatBytes(container.memoryUsage)} / {formatBytes(container.memoryLimit)}
                            </span>
                          </TableCell>
                          <TableCell className="text-right">
                            <div className="flex items-center justify-end gap-1">
                              <Tooltip>
                                <TooltipTrigger>
                                  <Button
                                    aria-label={logsOpen ? `Hide logs for ${container.name}` : `View logs for ${container.name}`}
                                    variant="ghost"
                                    size="icon"
                                    className="h-7 w-7 text-zinc-500 hover:text-purple-400 hover:bg-purple-400/10"
                                    onClick={() => toggleLogs(container.id)}
                                    disabled={loadingLogs[container.id]}
                                  >
                                    <FileText className="w-3.5 h-3.5" />
                                  </Button>
                                </TooltipTrigger>
                                <TooltipContent>
                                  {logsOpen ? 'Hide Logs' : 'View Logs'}
                                </TooltipContent>
                              </Tooltip>

                              {container.status === 'running' ? (
                                <Tooltip>
                                  <TooltipTrigger>
                                    <Button
                                      aria-label={`Stop ${container.name}`}
                                      variant="ghost"
                                      size="icon"
                                      className="h-7 w-7 text-zinc-500 hover:text-amber-400 hover:bg-amber-400/10"
                                    onClick={() =>
                                      containerAction.mutate({ id: container.id, action: 'stop' })
                                    }
                                    disabled={!dockerActionsReady || containerAction.isPending}
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
                                      aria-label={`Start ${container.name}`}
                                      variant="ghost"
                                      size="icon"
                                      className="h-7 w-7 text-zinc-500 hover:text-green-400 hover:bg-green-400/10"
                                    onClick={() =>
                                      containerAction.mutate({ id: container.id, action: 'start' })
                                    }
                                    disabled={!dockerActionsReady || containerAction.isPending}
                                    >
                                      <Play className="w-3.5 h-3.5" />
                                    </Button>
                                  </TooltipTrigger>
                                  <TooltipContent>Start</TooltipContent>
                                </Tooltip>
                              )}

                              <Tooltip>
                                <TooltipTrigger>
                                  <Button
                                    aria-label={`Restart ${container.name}`}
                                    variant="ghost"
                                    size="icon"
                                    className="h-7 w-7 text-zinc-500 hover:text-blue-400 hover:bg-blue-400/10"
                                    onClick={() =>
                                      containerAction.mutate({ id: container.id, action: 'restart' })
                                    }
                                    disabled={!dockerActionsReady || containerAction.isPending}
                                  >
                                    <RotateCcw className="w-3.5 h-3.5" />
                                  </Button>
                                </TooltipTrigger>
                                <TooltipContent>Restart</TooltipContent>
                              </Tooltip>

                              <Tooltip>
                                <TooltipTrigger>
                                  <Button
                                    aria-label={`Remove ${container.name}`}
                                    variant="ghost"
                                    size="icon"
                                    className="h-7 w-7 text-zinc-500 hover:text-red-400 hover:bg-red-400/10"
                                    onClick={() => setRemovalTarget({ kind: 'container', id: container.id, label: container.name })}
                                    disabled={!dockerActionsReady || container.status === 'running' || container.status === 'restarting' || container.status === 'paused'}
                                  >
                                    <Trash2 className="w-3.5 h-3.5" />
                                  </Button>
                                </TooltipTrigger>
                                <TooltipContent>{container.status === 'running' || container.status === 'restarting' || container.status === 'paused' ? 'Stop before removing' : 'Remove'}</TooltipContent>
                              </Tooltip>
                            </div>
                          </TableCell>
                        </TableRow>

                        {logsOpen && (
                          <TableRow
                            key={`${container.id}-logs`}
                            className="border-zinc-800 bg-zinc-950"
                          >
                            <TableCell colSpan={7} className="p-0">
                              <div className="p-3 border-t border-zinc-800">
                                <div className="flex items-center gap-2 mb-2">
                                  <FileText className="w-3 h-3 text-purple-400" />
                                  <span className="text-purple-400 text-xs font-medium">
                                    Logs — {container.name} · last {expandedLogs[container.id]?.tail ?? 200} lines
                                  </span>
                                  {expandedLogs[container.id]?.truncated && <Badge className="border-amber-500/20 bg-amber-500/10 text-amber-300">1 MiB response limit reached</Badge>}
                                </div>
                                <pre className="text-xs text-zinc-300 font-mono bg-black rounded p-3 max-h-64 overflow-y-auto whitespace-pre-wrap break-all">
                                  {expandedLogs[container.id]?.logs || '(no output)'}
                                </pre>
                              </div>
                            </TableCell>
                          </TableRow>
                        )}
                      </Fragment>
                    )
                  })}
                </TableBody>
              </Table>
            </div>
          ) : (
            <div className="flex flex-col items-center justify-center py-16 text-zinc-600">
              <Container className="w-8 h-8 mb-3 opacity-50" />
              <p className="text-sm">
                {status?.running ? 'No containers found' : 'Docker daemon not running'}
              </p>
            </div>
          )}
        </CardContent>
      </Card>

      {/* Images section */}
      <Card className="bg-zinc-900 border-zinc-800">
        <CardHeader className="pb-0 pt-4">
          <CardTitle className="text-white text-sm font-medium flex items-center gap-2">
            <Package className="w-3.5 h-3.5 text-blue-400" />
            Images
          </CardTitle>
        </CardHeader>
        <CardContent className="p-0 mt-3">
          {statusQuery.isError && images === undefined ? (
            <div className="px-4 py-10 text-center text-sm text-zinc-500">Image inventory is paused until Docker status is available.</div>
          ) : imagesQuery.isLoading ? (
            <div className="p-4 space-y-2">
              {Array.from({ length: 3 }).map((_, i) => (
                <Skeleton key={i} className="h-10 w-full bg-zinc-800" />
              ))}
            </div>
          ) : imagesQuery.isError ? (
            <DockerLoadError title="Docker images could not be loaded." error={imagesQuery.error} retry={() => { void imagesQuery.refetch() }} retrying={imagesQuery.isFetching} />
          ) : images && images.length > 0 ? (
            <div className="overflow-x-auto">
              <Table className="min-w-[500px]">
                <TableHeader>
                  <TableRow className="border-zinc-800 hover:bg-transparent">
                    <TableHead className="text-zinc-500 text-xs font-medium">Repository</TableHead>
                    <TableHead className="text-zinc-500 text-xs font-medium">ID</TableHead>
                    <TableHead className="text-zinc-500 text-xs font-medium">Size</TableHead>
                    <TableHead className="text-zinc-500 text-xs font-medium text-right">
                      Actions
                    </TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {images.map((image) => {
                    const imageLabel = image.repoTags?.[0] || image.name || image.id
                    return (
                    <TableRow
                      key={image.id}
                      className="border-zinc-800 hover:bg-zinc-800/50 transition-colors"
                    >
                      <TableCell>
                        <div className="space-y-0.5">
                          {(image.repoTags?.length || image.name) ? (
                            (image.repoTags?.length ? image.repoTags : [image.name as string]).map((tag) => (
                              <span key={tag} className="block text-zinc-300 text-xs font-mono">
                                {tag}
                              </span>
                            ))
                          ) : (
                            <span className="text-zinc-600 text-xs">&lt;none&gt;</span>
                          )}
                        </div>
                      </TableCell>
                      <TableCell>
                        <span className="text-zinc-500 text-xs font-mono">
                          {image.id.replace('sha256:', '').slice(0, 12)}
                        </span>
                      </TableCell>
                      <TableCell>
                        <span className="text-zinc-400 text-sm">{formatDockerSize(image.size)}</span>
                      </TableCell>
                      <TableCell className="text-right">
                        <Tooltip>
                          <TooltipTrigger>
                            <Button
                              aria-label={`Remove image ${image.id}`}
                              variant="ghost"
                              size="icon"
                              className="h-7 w-7 text-zinc-500 hover:text-red-400 hover:bg-red-400/10"
                              onClick={() => setRemovalTarget({ kind: 'image', id: image.id, label: imageLabel })}
                              disabled={!dockerActionsReady}
                            >
                              <Trash2 className="w-3.5 h-3.5" />
                            </Button>
                          </TooltipTrigger>
                          <TooltipContent>Remove Image</TooltipContent>
                        </Tooltip>
                      </TableCell>
                    </TableRow>
                    )
                  })}
                </TableBody>
              </Table>
            </div>
          ) : (
            <div className="flex flex-col items-center justify-center py-12 text-zinc-600">
              <Package className="w-8 h-8 mb-3 opacity-50" />
              <p className="text-sm">{status?.running ? 'No images found' : 'Docker daemon not running'}</p>
            </div>
          )}
        </CardContent>
      </Card>

      {/* Remove Confirmation Dialog */}
      <Dialog
        open={removalTarget !== null}
        onOpenChange={(open) => {
          if (!open && !removalPending) setRemovalTarget(null)
        }}
      >
        <DialogContent className="border-zinc-800 bg-zinc-900 text-white">
          <DialogHeader>
            <DialogTitle>
              Remove Docker {removalTarget?.kind === 'container' ? 'container' : 'image'}?
            </DialogTitle>
            <DialogDescription className="text-zinc-400">
              {removalTarget?.kind === 'container'
                ? 'The stopped container and its writable layer will be removed. This cannot be undone.'
                : 'The local image will be removed. Docker may reject the request while a container still depends on it.'}
            </DialogDescription>
          </DialogHeader>
          <code className="break-all rounded-lg border border-zinc-800 bg-zinc-950 px-3 py-2 text-sm text-zinc-200">
            {removalTarget?.label}
          </code>
          <DialogFooter>
            <Button
              type="button"
              variant="ghost"
              className="text-zinc-400 hover:text-white"
              disabled={removalPending}
              onClick={() => setRemovalTarget(null)}
            >
              Cancel
            </Button>
            <Button
              type="button"
              variant="destructive"
              disabled={!removalTarget || removalPending || !dockerActionsReady}
              onClick={confirmRemoval}
            >
              {removalPending ? 'Removing…' : 'Remove'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Pull Image Dialog */}
      <Dialog open={pullDialogOpen} onOpenChange={handlePullDialogOpenChange}>
        <DialogContent className="bg-zinc-900 border-zinc-800 text-white max-w-xl max-h-[85vh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle className="text-white flex items-center gap-2">
              <Download className="w-4 h-4 text-blue-400" />
              Pull Image
            </DialogTitle>
          </DialogHeader>
          <div className="space-y-4 py-2">
            <div className="space-y-2">
              <Label className="text-zinc-400 text-sm">Image Name</Label>
              <Input
                placeholder="e.g. nginx:latest, postgres:16-alpine"
                value={pullImageName}
                onChange={(e) => setPullImageName(e.target.value)}
                className="bg-zinc-800 border-zinc-700 text-white placeholder-zinc-500 focus:border-blue-500"
                onKeyDown={(e) => {
                  if (e.key === 'Enter' && pullImageName.trim() && dockerActionsReady) {
                    pullImage.mutate(pullImageName.trim())
                  }
                }}
              />
            </div>
          </div>
          <DialogFooter>
            <Button
              variant="ghost"
              className="text-zinc-400 hover:text-white"
              disabled={pullImage.isPending}
              onClick={() => handlePullDialogOpenChange(false)}
            >
              Cancel
            </Button>
            <Button
              className="bg-blue-600 hover:bg-blue-500 text-white"
              disabled={!pullImageName.trim() || pullImage.isPending || !dockerActionsReady}
              onClick={() => pullImage.mutate(pullImageName.trim())}
            >
              {pullImage.isPending ? 'Pulling…' : 'Pull'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
