import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  Clock,
  Plus,
  Pencil,
  Trash2,
  Loader2,
  ToggleLeft,
  ToggleRight,
  Terminal,
  FolderClosed,
  AlertTriangle,
  RefreshCw,
} from 'lucide-react'
import { Card, CardContent } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { DependencyRemediation } from '@/components/DependencyRemediation'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from '@/components/ui/dialog'
import { api } from '@/lib/api'
import { toast } from 'sonner'
import type {
  CronJob,
  CronStatus,
  CreateCronJobRequest,
  SystemCronInfo,
  UpdateCronJobRequest,
} from '@/lib/types'

// ---- Schedule presets ----
interface SchedulePreset {
  label: string
  value: string
}

const SCHEDULE_PRESETS: SchedulePreset[] = [
  { label: 'Every minute', value: '* * * * *' },
  { label: 'Every 5 minutes', value: '*/5 * * * *' },
  { label: 'Every 15 minutes', value: '*/15 * * * *' },
  { label: 'Every 30 minutes', value: '*/30 * * * *' },
  { label: 'Every hour', value: '0 * * * *' },
  { label: 'Every 6 hours', value: '0 */6 * * *' },
  { label: 'Daily at midnight', value: '0 0 * * *' },
  { label: 'Daily at noon', value: '0 12 * * *' },
  { label: 'Weekly (Sunday midnight)', value: '0 0 * * 0' },
  { label: 'Monthly (1st midnight)', value: '0 0 1 * *' },
  { label: 'Custom...', value: 'custom' },
]

function humanizeCron(expr: string): string {
  const preset = SCHEDULE_PRESETS.find((p) => p.value === expr && p.value !== 'custom')
  return preset ? preset.label : expr
}

// ---- Job Form Dialog ----
interface JobDialogProps {
  open: boolean
  onClose: () => void
  onSubmit: (req: CronJobFormValues) => void
  isPending: boolean
  initial?: CronJob | null
}

interface CronJobFormValues {
  schedule: string
  command: string
  user: string
  description: string
  isActive: boolean
}

function JobDialog({ open, onClose, onSubmit, isPending, initial }: JobDialogProps) {
  const [preset, setPreset] = useState<string>(
    initial
      ? SCHEDULE_PRESETS.find((p) => p.value === initial.schedule)?.value ?? 'custom'
      : '0 * * * *',
  )
  const [customSchedule, setCustomSchedule] = useState(initial?.schedule ?? '')
  const [command, setCommand] = useState(initial?.command ?? '')
  const [user, setUser] = useState(initial?.user ?? 'root')
  const [description, setDescription] = useState(initial?.description ?? '')

  const schedule = preset === 'custom' ? customSchedule : preset

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault()
    if (!schedule.trim() || !command.trim()) return
    onSubmit({ schedule, command, user, description, isActive: initial?.isActive ?? true })
  }

  return (
    <Dialog open={open} onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="bg-zinc-900 border-zinc-800 text-white max-w-2xl max-h-[85vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle className="text-white">
            {initial ? 'Edit Cron Job' : 'Add Cron Job'}
          </DialogTitle>
        </DialogHeader>
        <form onSubmit={handleSubmit} className="space-y-4 mt-2">
          {/* Schedule */}
          <div className="space-y-1.5">
            <label htmlFor="cron-schedule" className="text-zinc-400 text-sm">Schedule</label>
            <select
              id="cron-schedule"
              value={preset}
              onChange={(e) => setPreset(e.target.value)}
              className="w-full bg-zinc-800 border border-zinc-700 rounded-lg px-3 py-2 text-sm text-white focus:outline-none focus:border-blue-500 transition-colors"
            >
              {SCHEDULE_PRESETS.map((p) => (
                <option key={p.value} value={p.value}>
                  {p.label}
                </option>
              ))}
            </select>
            {preset === 'custom' && (
              <input
                type="text"
                required
                value={customSchedule}
                onChange={(e) => setCustomSchedule(e.target.value)}
                placeholder="* * * * *"
                className="w-full bg-zinc-800 border border-zinc-700 rounded-lg px-3 py-2 text-sm text-white font-mono placeholder:text-zinc-600 focus:outline-none focus:border-blue-500 transition-colors"
              />
            )}
            {preset !== 'custom' && (
              <p className="text-zinc-600 text-xs font-mono px-1">{preset}</p>
            )}
          </div>

          {/* Command */}
          <div className="space-y-1.5">
            <label htmlFor="cron-command" className="text-zinc-400 text-sm">Command</label>
            <input
              id="cron-command"
              type="text"
              required
              value={command}
              onChange={(e) => setCommand(e.target.value)}
              placeholder="/usr/bin/php /var/www/artisan schedule:run"
              className="w-full bg-zinc-800 border border-zinc-700 rounded-lg px-3 py-2 text-sm text-white font-mono placeholder:text-zinc-600 focus:outline-none focus:border-blue-500 transition-colors"
            />
          </div>

          {/* User */}
          <div className="space-y-1.5">
            <label htmlFor="cron-user" className="text-zinc-400 text-sm">User</label>
            <input
              id="cron-user"
              type="text"
              value={user}
              onChange={(e) => setUser(e.target.value)}
              disabled={!!initial}
              placeholder="root"
              className="w-full bg-zinc-800 border border-zinc-700 rounded-lg px-3 py-2 text-sm text-white placeholder:text-zinc-600 focus:outline-none focus:border-blue-500 transition-colors disabled:cursor-not-allowed disabled:opacity-60"
            />
            {initial && (
              <p className="text-zinc-600 text-xs">Recreate the job to move it to another user.</p>
            )}
          </div>

          {/* Description */}
          <div className="space-y-1.5">
            <label htmlFor="cron-description" className="text-zinc-400 text-sm">
              Description{' '}
              <span className="text-zinc-600 text-xs">(optional)</span>
            </label>
            <input
              id="cron-description"
              type="text"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder="What this job does"
              className="w-full bg-zinc-800 border border-zinc-700 rounded-lg px-3 py-2 text-sm text-white placeholder:text-zinc-600 focus:outline-none focus:border-blue-500 transition-colors"
            />
          </div>

          <DialogFooter className="gap-2 pt-2">
            <Button
              type="button"
              variant="ghost"
              onClick={onClose}
              className="text-zinc-400 hover:text-white"
            >
              Cancel
            </Button>
            <Button
              type="submit"
              disabled={isPending}
              className="bg-blue-600 hover:bg-blue-500 text-white"
            >
              {isPending && <Loader2 className="w-3.5 h-3.5 mr-2 animate-spin" />}
              {initial ? 'Save Changes' : 'Add Job'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

// ---- Delete Confirm Dialog ----
interface DeleteDialogProps {
  job: CronJob | null
  onClose: () => void
  onConfirm: () => void
  isPending: boolean
}

function DeleteDialog({ job, onClose, onConfirm, isPending }: DeleteDialogProps) {
  if (!job) return null
  return (
    <Dialog open={!!job} onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="bg-zinc-900 border-zinc-800 text-white max-w-md">
        <DialogHeader>
          <DialogTitle className="text-white">Delete Cron Job</DialogTitle>
        </DialogHeader>
        <p className="text-zinc-400 text-sm mt-2">
          Delete job{' '}
          <span className="text-white font-mono font-medium">{job.command}</span>? This
          cannot be undone.
        </p>
        <DialogFooter className="gap-2 pt-4">
          <Button
            type="button"
            variant="ghost"
            onClick={onClose}
            className="text-zinc-400 hover:text-white"
          >
            Cancel
          </Button>
          <Button
            disabled={isPending}
            onClick={onConfirm}
            className="bg-red-600 hover:bg-red-500 text-white"
          >
            {isPending && <Loader2 className="w-3.5 h-3.5 mr-2 animate-spin" />}
            Delete
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ---- Main Page ----
export default function Cron() {
  const queryClient = useQueryClient()
  const [addOpen, setAddOpen] = useState(false)
  const [editTarget, setEditTarget] = useState<CronJob | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<CronJob | null>(null)

  const statusQuery = useQuery<CronStatus>({
    queryKey: ['cron-status'],
    queryFn: () => api.get<CronStatus>('/cron/status'),
  })

  const jobsQuery = useQuery<{ jobs: CronJob[] }>({
    queryKey: ['cron-jobs'],
    queryFn: () => api.get<{ jobs: CronJob[] }>('/cron/jobs'),
    enabled: statusQuery.data?.installed === true,
  })

  const systemQuery = useQuery<SystemCronInfo>({
    queryKey: ['cron-system'],
    queryFn: () => api.get<SystemCronInfo>('/cron/system'),
  })

  const jobs = jobsQuery.data?.jobs ?? []
  const systemData = systemQuery.data
  const cronStatus = statusQuery.data
  const cronReady = cronStatus?.available === true

  const createMutation = useMutation({
    mutationFn: (req: CreateCronJobRequest) => api.post('/cron/jobs', req),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['cron-jobs'] })
      setAddOpen(false)
      toast.success('Cron job added')
    },
    onError: () => toast.error('Failed to add cron job'),
  })

  const updateMutation = useMutation({
    mutationFn: ({ job, req }: { job: CronJob; req: UpdateCronJobRequest }) =>
      api.put(`/cron/jobs/${encodeURIComponent(job.id)}?user=${encodeURIComponent(job.user)}`, req),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['cron-jobs'] })
      setEditTarget(null)
      toast.success('Cron job updated')
    },
    onError: () => toast.error('Failed to update cron job'),
  })

  const deleteMutation = useMutation({
    mutationFn: (job: CronJob) => api.delete(`/cron/jobs/${encodeURIComponent(job.id)}?user=${encodeURIComponent(job.user)}`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['cron-jobs'] })
      setDeleteTarget(null)
      toast.success('Cron job deleted')
    },
    onError: () => toast.error('Failed to delete cron job'),
  })

  const toggleMutation = useMutation({
    mutationFn: ({ job, isActive }: { job: CronJob; isActive: boolean }) =>
      api.put(`/cron/jobs/${encodeURIComponent(job.id)}?user=${encodeURIComponent(job.user)}`, {
        schedule: job.schedule,
        command: job.command,
        description: job.description,
        isActive,
      } satisfies UpdateCronJobRequest),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['cron-jobs'] })
    },
    onError: () => toast.error('Failed to toggle cron job'),
  })

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between gap-4 flex-wrap">
        <div>
          <h2 className="text-white text-xl font-bold">Cron Jobs</h2>
          <p className="text-zinc-500 text-sm mt-0.5">
            {statusQuery.isError
              ? 'Cron readiness unavailable'
              : cronStatus && !cronStatus.installed
                ? 'Cron is not installed'
                : cronStatus && !cronStatus.running
                  ? 'Cron daemon is not running'
                  : jobsQuery.isError
                    ? 'Cron jobs unavailable'
                    : jobs.length > 0
                      ? `${jobs.length} jobs configured`
                      : 'Schedule automated tasks'}
          </p>
        </div>
        <Button
          onClick={() => setAddOpen(true)}
          disabled={!cronReady || jobsQuery.isLoading || jobsQuery.isError}
          className="bg-blue-600 hover:bg-blue-500 text-white"
        >
          <Plus className="w-4 h-4 mr-2" />
          Add Job
        </Button>
      </div>

      {statusQuery.isError && (
        <DependencyRemediation
          title="Cron readiness could not be determined"
          summary="HServer could not verify the crontab client and cron daemon, so managed Cron inventory and mutations remain paused."
          error={statusQuery.error.message}
          retry={() => { void statusQuery.refetch() }}
          retrying={statusQuery.isFetching}
          steps={[
            <>Verify <code className="text-zinc-100">crontab --version</code> and <code className="text-zinc-100">systemctl status cron</code> on this host.</>,
            'Confirm the native HServer service can reach systemd and the crontab executable through its configured PATH.',
            'Correct the host dependency or service state, then retry detection.',
          ]}
        />
      )}

      {cronStatus && !cronStatus.installed && (
        <DependencyRemediation
          state="not-configured"
          title="Cron is not installed"
          summary="Scheduled-task management is unavailable because this host does not provide the crontab executable. HServer will not install packages automatically."
          error={cronStatus.error}
          retry={() => { void statusQuery.refetch() }}
          retrying={statusQuery.isFetching}
          steps={[
            'Install the cron package using this Linux distribution’s supported package manager.',
            <>Enable scheduled execution with <code className="text-zinc-100">systemctl enable --now cron</code>.</>,
            'Retry detection; inventory and mutations unlock only after the runtime is observed healthy.',
          ]}
        />
      )}

      {cronStatus?.installed && cronStatus.state === 'stopped' && (
        <DependencyRemediation
          state="stopped"
          title="Cron is installed but the daemon is stopped"
          summary="Existing crontabs remain visible, but HServer pauses mutations because newly scheduled work would not execute."
          error={cronStatus.error}
          retry={() => { void statusQuery.refetch() }}
          retrying={statusQuery.isFetching}
          steps={[
            <>Inspect the failure with <code className="text-zinc-100">systemctl status cron</code>.</>,
            <>After correcting the cause, run <code className="text-zinc-100">systemctl start cron</code>.</>,
            'Retry detection. HServer will restore mutation controls after the daemon reports active.',
          ]}
        />
      )}

      {cronStatus?.installed && cronStatus.state === 'unavailable' && (
        <DependencyRemediation
          title="Cron daemon state is unavailable"
          summary="The crontab client exists, but HServer cannot prove that scheduled jobs will run, so mutations remain paused."
          error={cronStatus.error}
          retry={() => { void statusQuery.refetch() }}
          retrying={statusQuery.isFetching}
          steps={[
            <>Check <code className="text-zinc-100">systemctl is-active cron</code> directly on the host.</>,
            'Restore systemd access for the native HServer service without granting an unrelated container host control.',
            'Retry detection after the daemon state becomes observable.',
          ]}
        />
      )}

      {/* Jobs table */}
      <Card className="bg-zinc-900 border-zinc-800">
        <CardContent className="p-0">
          {statusQuery.isLoading ? (
            <div className="p-4 space-y-3">
              {Array.from({ length: 3 }).map((_, i) => (
                <Skeleton key={i} className="h-14 w-full bg-zinc-800" />
              ))}
            </div>
          ) : statusQuery.isError || !cronStatus?.installed ? (
            <div className="flex flex-col items-center justify-center px-4 py-12 text-center">
              <AlertTriangle className="size-5 text-amber-400" />
              <p className="mt-2 text-sm text-zinc-400">Managed cron inventory is paused until Cron readiness is available.</p>
            </div>
          ) : jobsQuery.isLoading ? (
            <div className="p-4 space-y-3">
              {Array.from({ length: 3 }).map((_, i) => (
                <Skeleton key={i} className="h-14 w-full bg-zinc-800" />
              ))}
            </div>
          ) : jobsQuery.isError ? (
            <div className="flex flex-col items-center justify-center px-4 py-12 text-center">
              <AlertTriangle className="size-5 text-red-400" />
              <p className="mt-2 text-sm text-red-300">Managed cron jobs could not be loaded. Mutating controls are paused.</p>
              <p className="mt-1 text-xs text-zinc-600">{jobsQuery.error.message}</p>
              <Button type="button" variant="outline" size="sm" className="mt-4 border-red-500/30 text-red-200" onClick={() => { void jobsQuery.refetch() }} disabled={jobsQuery.isFetching}>
                <RefreshCw className={`mr-2 size-3.5 ${jobsQuery.isFetching ? 'animate-spin' : ''}`} />Retry
              </Button>
            </div>
          ) : jobs.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-16 text-zinc-600">
              <Clock className="w-10 h-10 mb-3 opacity-50" />
              <p className="text-sm">No cron jobs configured</p>
            </div>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-sm min-w-[520px]">
                <thead>
                  <tr className="border-b border-zinc-800">
                    <th className="text-left text-zinc-500 font-medium px-5 py-3">Schedule</th>
                    <th className="text-left text-zinc-500 font-medium px-4 py-3">Command</th>
                    <th className="text-left text-zinc-500 font-medium px-4 py-3">User</th>
                    <th className="text-left text-zinc-500 font-medium px-4 py-3">Status</th>
                    <th className="text-right text-zinc-500 font-medium px-5 py-3">Actions</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-zinc-800">
                  {jobs.map((job) => (
                    <tr key={job.id} className="hover:bg-zinc-800/40 transition-colors">
                      <td className="px-5 py-3.5">
                        <div>
                          <span className="text-white text-xs font-mono">{job.schedule}</span>
                          <p className="text-zinc-500 text-xs mt-0.5">
                            {humanizeCron(job.schedule)}
                          </p>
                        </div>
                      </td>
                      <td className="px-4 py-3.5 max-w-xs">
                        <div>
                          <span className="text-zinc-300 text-xs font-mono truncate block">
                            {job.command}
                          </span>
                          {job.description && (
                            <span className="text-zinc-600 text-xs">{job.description}</span>
                          )}
                        </div>
                      </td>
                      <td className="px-4 py-3.5">
                        <span className="text-zinc-400 text-xs">{job.user}</span>
                      </td>
                      <td className="px-4 py-3.5">
                        {job.isActive ? (
                          <Badge className="bg-green-500/10 text-green-400 border-green-500/20 text-xs">
                            Active
                          </Badge>
                        ) : (
                          <Badge className="bg-zinc-700/50 text-zinc-500 border-zinc-700 text-xs">
                            Inactive
                          </Badge>
                        )}
                      </td>
                      <td className="px-5 py-3.5">
                        <div className="flex items-center justify-end gap-1.5">
                          {/* Toggle */}
                          <button
                            onClick={() =>
                              toggleMutation.mutate({ job, isActive: !job.isActive })
                            }
                            disabled={!cronReady || toggleMutation.isPending}
                            title={job.isActive ? 'Disable' : 'Enable'}
                            className="p-1.5 rounded-md text-zinc-500 hover:text-white hover:bg-zinc-700 transition-colors"
                          >
                            {job.isActive ? (
                              <ToggleRight className="w-4 h-4 text-green-400" />
                            ) : (
                              <ToggleLeft className="w-4 h-4" />
                            )}
                          </button>
                          {/* Edit */}
                          <button
                            onClick={() => setEditTarget(job)}
                            disabled={!cronReady}
                            title="Edit"
                            className="p-1.5 rounded-md text-zinc-500 hover:text-white hover:bg-zinc-700 transition-colors"
                          >
                            <Pencil className="w-3.5 h-3.5" />
                          </button>
                          {/* Delete */}
                          <button
                            onClick={() => setDeleteTarget(job)}
                            disabled={!cronReady}
                            title="Delete"
                            className="p-1.5 rounded-md text-zinc-500 hover:text-red-400 hover:bg-red-400/10 transition-colors"
                          >
                            <Trash2 className="w-3.5 h-3.5" />
                          </button>
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </CardContent>
      </Card>

      {/* System Cron (read-only) */}
      <div>
        <h3 className="text-white text-base font-semibold mb-3 flex items-center gap-2">
          <FolderClosed className="w-4 h-4 text-zinc-400" />
          System Cron (read-only)
        </h3>
        <Card className="bg-zinc-900 border-zinc-800">
          <CardContent className="p-0">
            {systemQuery.isLoading ? (
              <div className="p-4 space-y-3">
                {Array.from({ length: 3 }).map((_, i) => (
                  <Skeleton key={i} className="h-10 w-full bg-zinc-800" />
                ))}
              </div>
            ) : systemQuery.isError ? (
              <div className="flex flex-col items-center justify-center px-4 py-10 text-center">
                <AlertTriangle className="size-5 text-red-400" />
                <p className="mt-2 text-sm text-red-300">System cron inventory could not be loaded.</p>
                <p className="mt-1 text-xs text-zinc-600">{systemQuery.error.message}</p>
                <Button type="button" variant="outline" size="sm" className="mt-4 border-red-500/30 text-red-200" onClick={() => { void systemQuery.refetch() }} disabled={systemQuery.isFetching}>
                  <RefreshCw className={`mr-2 size-3.5 ${systemQuery.isFetching ? 'animate-spin' : ''}`} />Retry
                </Button>
              </div>
            ) : !systemData || !systemData.files || systemData.files.length === 0 ? (
              <div className="flex flex-col items-center justify-center py-10 text-zinc-600">
                <Terminal className="w-8 h-8 mb-2 opacity-50" />
                <p className="text-sm">No system cron files found</p>
              </div>
            ) : (
              <div className="divide-y divide-zinc-800">
                {systemData.files.map((file) => (
                  <div key={file.path} className="px-5 py-3">
                    <div className="flex items-center gap-2 mb-2">
                      <Terminal className="w-3.5 h-3.5 text-zinc-500" />
                      <span className="text-zinc-400 text-xs font-mono">{file.path}</span>
                      <Badge className="bg-zinc-800 text-zinc-500 border-zinc-700 text-xs ml-auto">
                        {file.type}
                      </Badge>
                    </div>
                    {file.entries && file.entries.length > 0 ? (
                      <div className="space-y-1">
                        {file.entries.map((entry, idx) => (
                          <div
                            key={idx}
                            className="font-mono text-xs text-zinc-500 bg-zinc-800/50 rounded px-3 py-1.5 truncate"
                          >
                            {entry}
                          </div>
                        ))}
                      </div>
                    ) : (
                      <p className="text-zinc-700 text-xs">Empty</p>
                    )}
                  </div>
                ))}
              </div>
            )}
          </CardContent>
        </Card>
      </div>

      {/* Dialogs */}
      <JobDialog
        key={addOpen ? 'add-open' : 'add-closed'}
        open={addOpen}
        onClose={() => setAddOpen(false)}
        onSubmit={({ schedule, command, user, description }) =>
          createMutation.mutate({ schedule, command, user, description })
        }
        isPending={createMutation.isPending || !cronReady}
      />

      <JobDialog
        key={editTarget ? `${editTarget.user}:${editTarget.id}` : 'edit-closed'}
        open={!!editTarget}
        onClose={() => setEditTarget(null)}
        onSubmit={({ schedule, command, description, isActive }) =>
          editTarget && updateMutation.mutate({
            job: editTarget,
            req: { schedule, command, description, isActive },
          })
        }
        isPending={updateMutation.isPending || !cronReady}
        initial={editTarget}
      />

      <DeleteDialog
        job={deleteTarget}
        onClose={() => setDeleteTarget(null)}
        onConfirm={() => deleteTarget && deleteMutation.mutate(deleteTarget)}
        isPending={deleteMutation.isPending || !cronReady}
      />
    </div>
  )
}
