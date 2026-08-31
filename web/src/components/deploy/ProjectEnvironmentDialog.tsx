import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { AlertTriangle, Eye, EyeOff, KeyRound, Loader2, RefreshCw, Trash2 } from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import type { DeployEnvironment } from '@/lib/types'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'

interface ProjectEnvironmentDialogProps {
  open: boolean
  targetId: string | null
  targetName: string
  onOpenChange: (open: boolean) => void
}

export function ProjectEnvironmentDialog({ open, targetId, targetName, onOpenChange }: ProjectEnvironmentDialogProps) {
  const queryClient = useQueryClient()
  const [key, setKey] = useState('')
  const [value, setValue] = useState('')
  const [showValue, setShowValue] = useState(false)
  const [pendingDelete, setPendingDelete] = useState<string | null>(null)

  const environmentQuery = useQuery<DeployEnvironment>({
    queryKey: ['deploy', 'environment', targetId],
    queryFn: () => api.get<DeployEnvironment>(`/deploy/targets/${targetId}/environment`),
    enabled: open && targetId !== null,
    retry: false,
  })

  const saveVariable = useMutation({
    mutationFn: (request: { targetId: string; key: string; value: string }) =>
      api.put<DeployEnvironment>(`/deploy/targets/${request.targetId}/environment`, { key: request.key, value: request.value }),
    onSuccess: (environment, request) => {
      queryClient.setQueryData(['deploy', 'environment', request.targetId], environment)
      setKey('')
      setValue('')
      setShowValue(false)
      toast.success('Project environment variable stored')
    },
    onError: () => toast.error('Failed to store project environment variable'),
  })

  const deleteVariable = useMutation({
    mutationFn: (request: { targetId: string; key: string }) => api.delete<DeployEnvironment>(
      `/deploy/targets/${request.targetId}/environment/${encodeURIComponent(request.key)}`,
    ),
    onSuccess: (environment, request) => {
      queryClient.setQueryData(['deploy', 'environment', request.targetId], environment)
      setPendingDelete(null)
      toast.success('Project environment variable removed')
    },
    onError: () => toast.error('Failed to remove project environment variable'),
  })

  const close = (nextOpen: boolean) => {
    if (!nextOpen) {
      setKey('')
      setValue('')
      setShowValue(false)
      setPendingDelete(null)
    }
    onOpenChange(nextOpen)
  }

  return (
    <Dialog open={open} onOpenChange={close}>
      <DialogContent className="max-h-[85vh] max-w-xl overflow-y-auto border-zinc-800 bg-zinc-900 text-white">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2 text-white">
            <KeyRound className="size-4 text-violet-400" />
            Project Environment · {targetName}
          </DialogTitle>
        </DialogHeader>

        <div className="space-y-5 py-1">
          <div className="rounded-lg border border-violet-500/20 bg-violet-500/[0.05] p-3">
            <p className="text-xs font-medium text-violet-200">Values are write-only</p>
            <p className="mt-1 text-[11px] leading-5 text-zinc-500">
              Heyserver stores values outside Git with private file permissions. Existing values can be replaced or removed, but never read back into the browser.
            </p>
          </div>

          <div className="space-y-2">
            <div className="flex items-center gap-2">
              <Label className="text-sm text-zinc-300">Stored variables</Label>
              {environmentQuery.data?.configured && <Badge className="border-emerald-500/20 bg-emerald-500/10 text-emerald-300">Active</Badge>}
              <Button
                aria-label="Refresh project environment"
                type="button"
                variant="ghost"
                size="icon"
                className="ml-auto size-7 text-zinc-500 hover:text-violet-300"
                onClick={() => { void environmentQuery.refetch() }}
                disabled={environmentQuery.isFetching}
              >
                <RefreshCw className={`size-3.5 ${environmentQuery.isFetching ? 'animate-spin' : ''}`} />
              </Button>
            </div>
            {environmentQuery.isLoading ? (
              <div className="space-y-2">
                <Skeleton className="h-10 bg-zinc-800" />
                <Skeleton className="h-10 bg-zinc-800" />
              </div>
            ) : environmentQuery.isError ? (
              <div className="rounded-lg border border-red-500/20 bg-red-500/[0.05] p-3">
                <p className="flex items-center gap-2 text-xs text-red-300"><AlertTriangle className="size-3.5" />Project environment could not be loaded.</p>
                <p className="mt-1 text-[11px] text-zinc-600">{environmentQuery.error.message}</p>
              </div>
            ) : environmentQuery.data && environmentQuery.data.variables.length > 0 ? (
              <div className="divide-y divide-zinc-800 overflow-hidden rounded-lg border border-zinc-800">
                {environmentQuery.data.variables.map((variable) => (
                  <div key={variable.key} className="flex items-center gap-2 bg-zinc-950/60 px-3 py-2">
                    <button
                      type="button"
                      className="min-w-0 flex-1 truncate text-left font-mono text-xs text-zinc-300 hover:text-violet-300"
                      onClick={() => { setKey(variable.key); setValue('') }}
                      title="Select to replace"
                    >
                      {variable.key}
                    </button>
                    <Badge className="border-zinc-700 bg-zinc-800 text-[10px] text-zinc-500">Stored</Badge>
                    {pendingDelete === variable.key ? (
                      <div className="flex items-center gap-1">
                        <Button type="button" variant="ghost" size="sm" className="h-7 text-xs text-zinc-400" onClick={() => setPendingDelete(null)}>Cancel</Button>
                        <Button
                          type="button"
                          variant="destructive"
                          size="sm"
                          className="h-7 text-xs"
                          onClick={() => { if (targetId) deleteVariable.mutate({ targetId, key: variable.key }) }}
                          disabled={deleteVariable.isPending}
                        >Remove</Button>
                      </div>
                    ) : (
                      <Button
                        aria-label={`Remove ${variable.key}`}
                        type="button"
                        variant="ghost"
                        size="icon"
                        className="size-7 text-zinc-600 hover:text-red-300"
                        onClick={() => setPendingDelete(variable.key)}
                      >
                        <Trash2 className="size-3.5" />
                      </Button>
                    )}
                  </div>
                ))}
              </div>
            ) : (
              <p className="rounded-lg border border-zinc-800 bg-zinc-950/50 px-3 py-6 text-center text-xs text-zinc-600">No project variables stored.</p>
            )}
          </div>

          <div className="space-y-3 rounded-lg border border-zinc-800 bg-zinc-950/40 p-3">
            <div className="space-y-1.5">
              <Label htmlFor="project-environment-key" className="text-xs text-zinc-400">Variable key</Label>
              <Input
                id="project-environment-key"
                autoComplete="off"
                placeholder="e.g. DATABASE_URL"
                value={key}
                onChange={(event) => setKey(event.target.value.toUpperCase())}
                className="border-zinc-700 bg-zinc-800 font-mono text-white"
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="project-environment-value" className="text-xs text-zinc-400">New value</Label>
              <div className="relative">
                <Input
                  id="project-environment-value"
                  type={showValue ? 'text' : 'password'}
                  autoComplete="new-password"
                  placeholder="Enter a new value"
                  value={value}
                  onChange={(event) => setValue(event.target.value)}
                  className="border-zinc-700 bg-zinc-800 pr-10 font-mono text-white"
                />
                <Button
                  aria-label={showValue ? 'Hide new value' : 'Show new value'}
                  type="button"
                  variant="ghost"
                  size="icon"
                  className="absolute right-1 top-1/2 size-7 -translate-y-1/2 text-zinc-500 hover:text-white"
                  onClick={() => setShowValue((current) => !current)}
                >
                  {showValue ? <EyeOff className="size-3.5" /> : <Eye className="size-3.5" />}
                </Button>
              </div>
              <p className="text-[11px] text-zinc-600">Selecting an existing key prepares a replacement; its current value remains hidden.</p>
            </div>
          </div>
        </div>

        <DialogFooter>
          <Button type="button" variant="ghost" className="text-zinc-400" onClick={() => close(false)}>Close</Button>
          <Button
            type="button"
            className="bg-violet-600 text-white hover:bg-violet-500"
            onClick={() => { if (targetId) saveVariable.mutate({ targetId, key: key.trim(), value }) }}
            disabled={!targetId || !key.trim() || saveVariable.isPending}
          >
            {saveVariable.isPending && <Loader2 className="mr-2 size-3.5 animate-spin" />}
            Store value
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
