import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  Rocket,
  Plus,
  Play,
  RotateCcw,
  ChevronDown,
  ChevronRight,
  Copy,
  Check,
  Clock,
  GitCommit,
  GitCompareArrows,
  RefreshCw,
  Loader2,
  AlertTriangle,
  Box,
  ShieldCheck,
  CircleCheck,
  CircleX,
  FileText,
  Square,
  KeyRound,
  Globe2,
  GitBranchPlus,
  Pencil,
  Trash2,
} from 'lucide-react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { api } from '@/lib/api'
import { toast } from 'sonner'
import { useCurrentUser } from '@/hooks/useAuth'
import { ProjectEnvironmentDialog } from '@/components/deploy/ProjectEnvironmentDialog'
import { ProjectDomainsDialog } from '@/components/deploy/ProjectDomainsDialog'
import type {
  ComposeProjectService,
  ComposeProjectServiceLogs,
  DeployTarget,
  DeployTemplateInventory,
  DeployRun,
  DeployPreflight,
  DeployRevisionComparison,
  DeployStagingReceipt,
} from '@/lib/types'

const deployStatusConfig: Record<
  DeployRun['status'],
  { label: string; color: string }
> = {
  success: { label: 'Success', color: 'bg-green-500/10 text-green-400 border-green-500/20' },
  failed: { label: 'Failed', color: 'bg-red-500/10 text-red-400 border-red-500/20' },
  running: { label: 'Running', color: 'bg-blue-500/10 text-blue-400 border-blue-500/20' },
  pending: { label: 'Pending', color: 'bg-amber-500/10 text-amber-400 border-amber-500/20' },
}

function formatDuration(seconds?: number): string {
  if (!seconds) return '—'
  if (seconds < 60) return `${seconds}s`
  return `${Math.floor(seconds / 60)}m ${seconds % 60}s`
}

function formatDate(iso?: string): string {
  if (!iso) return '—'
  return new Date(iso).toLocaleString()
}

interface AddTargetForm {
  name: string
  repoUrl: string
  branch: string
  projectDir: string
  deploymentKind: 'script' | 'compose'
  composeFile: string
  deployScript: string
  webhookProvider: 'github' | 'gitlab'
  webhookToken: string
  autoDeploy: boolean
  isActive: boolean
  clearWebhookToken: boolean
}

interface StagingTargetForm {
  name: string
  branch: string
  projectDir: string
}

const emptyStagingForm: StagingTargetForm = {
  name: '',
  branch: '',
  projectDir: '',
}

const emptyForm: AddTargetForm = {
  name: '',
  repoUrl: '',
  branch: 'main',
  projectDir: '',
  deploymentKind: 'compose',
  composeFile: '',
  deployScript: '',
  webhookProvider: 'github',
  webhookToken: '',
  autoDeploy: false,
  isActive: true,
  clearWebhookToken: false,
}

function mutationErrorMessage(error: unknown, fallback: string): string {
  return error instanceof Error && error.message ? error.message : fallback
}

function mutationErrorStatus(error: unknown): number | undefined {
  if (typeof error !== 'object' || error === null || !('status' in error)) return undefined
  const status = (error as { status?: unknown }).status
  return typeof status === 'number' ? status : undefined
}

export default function Deploy() {
  const queryClient = useQueryClient()
  const { data: currentUser } = useCurrentUser()
  const [addDialogOpen, setAddDialogOpen] = useState(false)
  const [editingTarget, setEditingTarget] = useState<DeployTarget | null>(null)
  const [pendingDeleteTarget, setPendingDeleteTarget] = useState<DeployTarget | null>(null)
  const [form, setForm] = useState<AddTargetForm>(emptyForm)
  const [selectedTemplateID, setSelectedTemplateID] = useState('')
  const [expandedHistory, setExpandedHistory] = useState<Record<string, boolean>>({})
  const [expandedLogs, setExpandedLogs] = useState<Record<string, string | null>>({})
  const [loadingLogs, setLoadingLogs] = useState<Record<string, boolean>>({})
  const [copiedWebhook, setCopiedWebhook] = useState<string | null>(null)
  const [preflightResults, setPreflightResults] = useState<Record<string, DeployPreflight>>({})
  const [preflightLoading, setPreflightLoading] = useState<Record<string, boolean>>({})
  const [revisionResults, setRevisionResults] = useState<Record<string, DeployRevisionComparison>>({})
  const [revisionLoading, setRevisionLoading] = useState<Record<string, boolean>>({})
  const [revisionErrors, setRevisionErrors] = useState<Record<string, string>>({})
  const [expandedServices, setExpandedServices] = useState<Record<string, boolean>>({})
  const [composeServices, setComposeServices] = useState<Record<string, ComposeProjectService[]>>({})
  const [composeServicesLoading, setComposeServicesLoading] = useState<Record<string, boolean>>({})
  const [composeServicesError, setComposeServicesError] = useState<Record<string, string>>({})
  const [composeServiceLogs, setComposeServiceLogs] = useState<Record<string, ComposeProjectServiceLogs>>({})
  const [composeServiceLogsLoading, setComposeServiceLogsLoading] = useState<Record<string, boolean>>({})
  const [environmentTarget, setEnvironmentTarget] = useState<DeployTarget | null>(null)
  const [domainsTarget, setDomainsTarget] = useState<DeployTarget | null>(null)
  const [stagingTarget, setStagingTarget] = useState<DeployTarget | null>(null)
  const [stagingForm, setStagingForm] = useState<StagingTargetForm>(emptyStagingForm)

  const handleTargetDialogOpenChange = (open: boolean) => {
    if (open) return
    setAddDialogOpen(false)
    setEditingTarget(null)
    setForm(emptyForm)
    setSelectedTemplateID('')
  }

  const targetsQuery = useQuery<DeployTarget[]>({
    queryKey: ['deploy', 'targets'],
    queryFn: () => api.get<DeployTarget[]>('/deploy/targets'),
  })
  const targets = targetsQuery.data

  const templatesQuery = useQuery<DeployTemplateInventory>({
    queryKey: ['deploy', 'templates'],
    queryFn: () => api.get<DeployTemplateInventory>('/deploy/templates'),
    enabled: addDialogOpen,
  })
  const templateInventory = templatesQuery.data
  const templates = templateInventory?.templates ?? []

  const applyTemplate = (templateID: string) => {
    setSelectedTemplateID(templateID)
    if (!templateID) return
    const template = templates.find((item) => item.id === templateID)
    if (!template) return
    setForm((current) => ({
      ...current,
      branch: template.branch,
      deploymentKind: template.deploymentKind,
      composeFile: template.composeFile,
      deployScript: template.deployScript,
    }))
  }

  const historyQuery = useQuery<DeployRun[]>({
    queryKey: ['deploy', 'history'],
    queryFn: () => api.get<DeployRun[]>('/deploy/history'),
    refetchInterval: 10_000,
  })
  const history = historyQuery.data

  const addTarget = useMutation({
    mutationFn: (data: AddTargetForm) =>
      api.post<DeployTarget>('/deploy/targets', {
        name: data.name,
        repoUrl: data.repoUrl,
        branch: data.branch,
        projectDir: data.projectDir,
        deploymentKind: data.deploymentKind,
        composeFile: data.deploymentKind === 'compose' ? data.composeFile : '',
        deployScript: data.deployScript,
        webhookProvider: data.webhookProvider,
        webhookToken: data.webhookToken,
        autoDeploy: data.autoDeploy,
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['deploy', 'targets'] })
      handleTargetDialogOpenChange(false)
      toast.success('Deploy target added')
    },
    onError: () => toast.error('Failed to add deploy target'),
  })

  const updateTarget = useMutation({
    mutationFn: ({ target, data }: { target: DeployTarget; data: AddTargetForm }) =>
      api.put<DeployTarget>(`/deploy/targets/${target.id}`, {
        name: data.name,
        repoUrl: data.repoUrl,
        branch: data.branch,
        projectDir: data.projectDir,
        deploymentKind: data.deploymentKind,
        composeFile: data.deploymentKind === 'compose' ? data.composeFile : '',
        deployScript: data.deploymentKind === 'script' ? data.deployScript : '',
        webhookProvider: data.webhookProvider,
        webhookToken: data.webhookToken.trim(),
        clearWebhookToken: data.clearWebhookToken,
        autoDeploy: data.clearWebhookToken ? false : data.autoDeploy,
        isActive: data.isActive,
        expectedUpdatedAt: target.updatedAt,
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['deploy', 'targets'] })
      handleTargetDialogOpenChange(false)
      toast.success('Deploy target updated')
    },
    onError: (error) => {
      const message = mutationErrorMessage(error, 'Failed to update deploy target')
      if (mutationErrorStatus(error) === 409) void targetsQuery.refetch()
      toast.error(message)
    },
  })

  const removeTarget = useMutation({
    mutationFn: (target: DeployTarget) => api.delete(`/deploy/targets/${target.id}`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['deploy', 'targets'] })
      setPendingDeleteTarget(null)
      toast.success('Deploy target deleted')
    },
    onError: (error) => {
      toast.error(mutationErrorMessage(error, 'Failed to delete deploy target'))
    },
  })

  const openAddTarget = () => {
    setEditingTarget(null)
    setForm(emptyForm)
    setSelectedTemplateID('')
    setAddDialogOpen(true)
  }

  const openEditTarget = (target: DeployTarget) => {
    updateTarget.reset()
    setAddDialogOpen(false)
    setEditingTarget(target)
    setSelectedTemplateID('')
    setForm({
      name: target.name,
      repoUrl: target.repoUrl,
      branch: target.branch,
      projectDir: target.projectDir,
      deploymentKind: target.deploymentKind,
      composeFile: target.composeFile,
      deployScript: target.deployScript,
      webhookProvider: target.webhookProvider,
      // The API never returns an existing secret. An empty value preserves it.
      webhookToken: '',
      autoDeploy: target.autoDeploy,
      isActive: target.isActive,
      clearWebhookToken: false,
    })
  }

  const openDeleteTarget = (target: DeployTarget) => {
    removeTarget.reset()
    setPendingDeleteTarget(target)
  }

  const openStagingDialog = (target: DeployTarget) => {
    setStagingTarget(target)
    setStagingForm({
      name: `${target.name} Staging`,
      branch: target.branch,
      projectDir: `${target.projectDir.replace(/\/$/, '')}-staging`,
    })
  }

  const closeStagingDialog = () => {
    setStagingTarget(null)
    setStagingForm(emptyStagingForm)
  }

  const createStaging = useMutation({
    mutationFn: ({ sourceTargetId, form }: { sourceTargetId: string; form: StagingTargetForm }) =>
      api.post<DeployStagingReceipt>(`/deploy/targets/${sourceTargetId}/staging`, form),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['deploy', 'targets'] })
      closeStagingDialog()
      toast.success('Isolated staging environment created')
    },
    onError: () => toast.error('Failed to create staging environment'),
  })

  const manualDeploy = useMutation({
    mutationFn: (id: string) => api.post(`/deploy/manual/${id}`),
    onSuccess: (_data, id) => {
      queryClient.invalidateQueries({ queryKey: ['deploy', 'history'] })
      setPreflightResults((current) => {
        const next = { ...current }
        delete next[id]
        return next
      })
      setRevisionResults((current) => {
        const next = { ...current }
        delete next[id]
        return next
      })
      toast.success('Deployment triggered')
    },
    onError: () => toast.error('Failed to trigger deployment'),
  })

  const rollback = useMutation({
    mutationFn: (id: string) => api.post(`/deploy/rollback/${id}`),
    onSuccess: (_data, id) => {
      queryClient.invalidateQueries({ queryKey: ['deploy', 'history'] })
      setPreflightResults((current) => {
        const next = { ...current }
        delete next[id]
        return next
      })
      setRevisionResults((current) => {
        const next = { ...current }
        delete next[id]
        return next
      })
      toast.success('Rollback triggered')
    },
    onError: () => toast.error('Failed to trigger rollback'),
  })

  const loadComposeServices = async (targetId: string) => {
    setComposeServicesLoading((current) => ({ ...current, [targetId]: true }))
    setComposeServicesError((current) => {
      const next = { ...current }
      delete next[targetId]
      return next
    })
    try {
      const services = await api.get<ComposeProjectService[]>(`/deploy/targets/${targetId}/services`)
      setComposeServices((current) => ({ ...current, [targetId]: services }))
    } catch (error) {
      setComposeServicesError((current) => ({
        ...current,
        [targetId]: error instanceof Error ? error.message : 'Compose services are unavailable',
      }))
    } finally {
      setComposeServicesLoading((current) => ({ ...current, [targetId]: false }))
    }
  }

  const composeServiceAction = useMutation({
    mutationFn: ({ targetId, service, action }: { targetId: string; service: string; action: string }) =>
      api.post(`/deploy/targets/${targetId}/services/${encodeURIComponent(service)}/${action}`),
    onSuccess: (_data, { targetId, service, action }) => {
      toast.success(`${service} ${action} completed`)
      void loadComposeServices(targetId)
    },
    onError: (_error, { service, action }) => toast.error(`Failed to ${action} ${service}`),
  })

  const copyWebhookUrl = (targetId: string) => {
    const url = `${window.location.origin}/api/deploy/webhook/${targetId}`
    navigator.clipboard.writeText(url).then(() => {
      setCopiedWebhook(targetId)
      setTimeout(() => setCopiedWebhook(null), 2000)
    })
  }

  const toggleHistory = (targetId: string) => {
    setExpandedHistory((prev) => ({ ...prev, [targetId]: !prev[targetId] }))
  }

  const toggleComposeServices = (targetId: string) => {
    const opening = !expandedServices[targetId]
    setExpandedServices((current) => ({ ...current, [targetId]: opening }))
    if (opening) void loadComposeServices(targetId)
  }

  const toggleComposeServiceLogs = async (targetId: string, service: ComposeProjectService) => {
    const key = `${targetId}:${service.container}`
    if (composeServiceLogs[key] !== undefined) {
      setComposeServiceLogs((current) => {
        const next = { ...current }
        delete next[key]
        return next
      })
      return
    }
    setComposeServiceLogsLoading((current) => ({ ...current, [key]: true }))
    try {
      const logs = await api.get<ComposeProjectServiceLogs>(
        `/deploy/targets/${targetId}/services/${encodeURIComponent(service.service)}/logs?tail=200`,
      )
      setComposeServiceLogs((current) => ({ ...current, [key]: logs }))
    } catch {
      toast.error(`Failed to fetch logs for ${service.service}`)
    } finally {
      setComposeServiceLogsLoading((current) => ({ ...current, [key]: false }))
    }
  }

  const toggleRunLog = async (runId: string) => {
    if (expandedLogs[runId] !== undefined) {
      setExpandedLogs((prev) => {
        const next = { ...prev }
        delete next[runId]
        return next
      })
      return
    }
    setLoadingLogs((prev) => ({ ...prev, [runId]: true }))
    try {
      const response = await api.get<{ logs: string }>(`/deploy/history/${runId}/logs`)
      setExpandedLogs((prev) => ({ ...prev, [runId]: response.logs }))
    } catch {
      toast.error('Failed to fetch run logs')
    } finally {
      setLoadingLogs((prev) => ({ ...prev, [runId]: false }))
    }
  }

  const targetHistory = (targetId: string) =>
    history?.filter((r) => r.targetId === targetId) ?? []

  const runPreflight = async (targetId: string) => {
    setPreflightLoading((current) => ({ ...current, [targetId]: true }))
    try {
      const report = await api.get<DeployPreflight>(`/deploy/targets/${targetId}/preflight`)
      setPreflightResults((current) => ({ ...current, [targetId]: report }))
      if (report.eligible) toast.success('Deploy preflight passed')
      else toast.error('Deploy preflight found blockers')
    } catch {
      toast.error('Deploy preflight could not be completed')
    } finally {
      setPreflightLoading((current) => ({ ...current, [targetId]: false }))
    }
  }

  const compareRevisions = async (targetId: string) => {
    setRevisionLoading((current) => ({ ...current, [targetId]: true }))
    setRevisionErrors((current) => {
      const next = { ...current }
      delete next[targetId]
      return next
    })
    try {
      const report = await api.get<DeployRevisionComparison>(`/deploy/targets/${targetId}/revision`)
      setRevisionResults((current) => ({ ...current, [targetId]: report }))
    } catch (error) {
      setRevisionErrors((current) => ({
        ...current,
        [targetId]: error instanceof Error ? error.message : 'Revision comparison is unavailable',
      }))
    } finally {
      setRevisionLoading((current) => ({ ...current, [targetId]: false }))
    }
  }

  const webhookSecretAvailable = Boolean(form.webhookToken.trim()) || editingTarget?.webhookStatus === 'healthy'
  const canClearWebhookToken = Boolean(editingTarget && editingTarget.webhookStatus !== 'not_configured')
  const targetMutationPending = addTarget.isPending || updateTarget.isPending

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between flex-wrap gap-3">
        <div>
          <h2 className="text-white text-xl font-bold">Deploy</h2>
          <p className="text-zinc-500 text-sm mt-0.5">
            {targetsQuery.isError
              ? 'Deploy targets unavailable'
              : targets
              ? `${targets.length} target${targets.length !== 1 ? 's' : ''} configured`
              : 'Deployment manager'}
          </p>
        </div>
        <Button
          className="bg-blue-600 hover:bg-blue-500 text-white"
          onClick={openAddTarget}
          disabled={targetsQuery.isLoading || targetsQuery.isError}
        >
          <Plus className="w-4 h-4 mr-2" />
          Add Target
        </Button>
      </div>

      {/* Deploy targets */}
      <Card className="bg-zinc-900 border-zinc-800">
        <CardHeader className="pb-0 pt-4">
          <CardTitle className="text-white text-sm font-medium flex items-center gap-2">
            <Rocket className="w-3.5 h-3.5 text-blue-400" />
            Deploy Targets
          </CardTitle>
        </CardHeader>
        <CardContent className="p-0 mt-3">
          {targetsQuery.isLoading ? (
            <div className="p-4 space-y-2">
              {Array.from({ length: 3 }).map((_, i) => (
                <Skeleton key={i} className="h-12 w-full bg-zinc-800" />
              ))}
            </div>
          ) : targetsQuery.isError ? (
            <div className="flex flex-col items-center justify-center px-4 py-12 text-center">
              <AlertTriangle className="size-5 text-red-400" />
              <p className="mt-2 text-sm text-red-300">Deploy targets could not be loaded. Mutating controls are paused.</p>
              <p className="mt-1 text-xs text-zinc-600">{targetsQuery.error.message}</p>
              <Button type="button" variant="outline" size="sm" className="mt-4 border-red-500/30 text-red-200" onClick={() => { void targetsQuery.refetch() }} disabled={targetsQuery.isFetching}>
                <RefreshCw className={`mr-2 size-3.5 ${targetsQuery.isFetching ? 'animate-spin' : ''}`} />Retry
              </Button>
            </div>
          ) : targets && targets.length > 0 ? (
            <div className="divide-y divide-zinc-800">
              {targets.map((target) => {
                const targetEnvironment = target.environment ?? 'production'
                const runs = targetHistory(target.id)
                const histOpen = expandedHistory[target.id]
                const servicesOpen = expandedServices[target.id]
                const targetServices = composeServices[target.id]
                const preflight = preflightResults[target.id]
                const revision = revisionResults[target.id]
                const revisionError = revisionErrors[target.id]
                const preflightPending = preflight?.checks.some((check) => check.status === 'pending') ?? false
                const rollbackAvailable = runs.some((run) => run.status === 'success' && Boolean(run.prevCommit))
                const rollbackEligible = rollbackAvailable && preflight !== undefined && preflight.checks.every((check) => check.status === 'pass' || check.id === 'compose-config')
                const lastRun = runs[0] ?? null
                const statusCfg = lastRun
                  ? deployStatusConfig[lastRun.status]
                  : null

                return (
                  <div key={target.id}>
                    <div className="flex items-start gap-3 px-4 py-3 hover:bg-zinc-800/40 transition-colors">
                      <div className="flex-1 min-w-0">
                        <div className="flex items-center gap-2 flex-wrap">
                          <span className="text-white text-sm font-medium">{target.name}</span>
                          <Badge className={`text-xs border ${targetEnvironment === 'staging' ? 'border-amber-500/20 bg-amber-500/10 text-amber-300' : 'border-emerald-500/20 bg-emerald-500/10 text-emerald-300'}`}>
                            {targetEnvironment === 'staging' ? 'Staging' : 'Production'}
                          </Badge>
                          <Badge className={`text-xs border ${target.deploymentKind === 'compose' ? 'border-cyan-500/20 bg-cyan-500/10 text-cyan-300' : 'border-zinc-700 bg-zinc-800 text-zinc-400'}`}>
                            {target.deploymentKind === 'compose' ? 'Docker Compose' : 'Script'}
                          </Badge>
                          {statusCfg && (
                            <Badge className={`text-xs border ${statusCfg.color}`}>
                              {statusCfg.label}
                            </Badge>
                          )}
                          {lastRun?.startedAt && (
                            <span className="text-zinc-600 text-xs flex items-center gap-1">
                              <Clock className="w-3 h-3" />
                              {formatDate(lastRun.startedAt)}
                            </span>
                          )}
                        </div>
                        <div className="flex items-center gap-3 mt-1 flex-wrap">
                          <span className="text-zinc-500 text-xs font-mono truncate max-w-48">
                            {target.repoUrl}
                          </span>
                          <span className="text-zinc-600 text-xs bg-zinc-800 px-1.5 py-0.5 rounded">
                            {target.branch}
                          </span>
                          {target.deploymentKind === 'compose' && (
                            <span className="text-zinc-600 text-xs font-mono">
                              {target.composeFile || 'Compose file auto-detect'}
                            </span>
                          )}
                          {targetEnvironment === 'staging' && target.sourceTargetId && (
                            <span className="text-amber-500/80 text-xs">
                              Source target #{target.sourceTargetId}
                            </span>
                          )}
                          {target.webhookStatus === 'unavailable' ? (
                            <span className="text-red-400/90 text-xs">Webhook secret unavailable</span>
                          ) : target.autoDeploy && (
                            <span className="text-emerald-500/80 text-xs">
                              {target.webhookProvider === 'gitlab' ? 'GitLab webhook' : 'GitHub webhook'}
                            </span>
                          )}
                        </div>
                        <div className="flex items-center gap-2 mt-1">
                          <span className="text-zinc-600 text-xs font-mono truncate max-w-64">
                            {window.location.origin}/api/deploy/webhook/{target.id}
                          </span>
                          <button
                            onClick={() => copyWebhookUrl(target.id)}
                            className="text-zinc-600 hover:text-zinc-300 transition-colors flex-shrink-0"
                            title="Copy webhook URL"
                          >
                            {copiedWebhook === target.id ? (
                              <Check className="w-3 h-3 text-green-400" />
                            ) : (
                              <Copy className="w-3 h-3" />
                            )}
                          </button>
                        </div>
                      </div>

                      <div className="flex items-center gap-1 flex-shrink-0">
                        {currentUser?.role === 'admin' && (
                          <>
                            <Button
                              aria-label={`Edit deploy target for ${target.name}`}
                              variant="ghost"
                              size="icon"
                              className="h-7 w-7 text-zinc-500 hover:bg-zinc-700 hover:text-white"
                              onClick={() => openEditTarget(target)}
                              title="Edit Deploy Target"
                            >
                              <Pencil className="w-3.5 h-3.5" />
                            </Button>
                            <Button
                              aria-label={`Delete deploy target for ${target.name}`}
                              variant="ghost"
                              size="icon"
                              className="h-7 w-7 text-zinc-500 hover:bg-red-400/10 hover:text-red-300"
                              onClick={() => openDeleteTarget(target)}
                              title="Delete Deploy Target"
                            >
                              <Trash2 className="w-3.5 h-3.5" />
                            </Button>
                          </>
                        )}
                        {currentUser?.role === 'admin' && targetEnvironment === 'production' && (
                          <Button
                            aria-label={`Create staging environment for ${target.name}`}
                            variant="ghost"
                            size="icon"
                            className="h-7 w-7 text-zinc-500 hover:text-amber-300 hover:bg-amber-400/10"
                            onClick={() => openStagingDialog(target)}
                            title="Create Staging Environment"
                          >
                            <GitBranchPlus className="w-3.5 h-3.5" />
                          </Button>
                        )}
                        <Button
                          aria-label={`Compare revisions for ${target.name}`}
                          variant="ghost"
                          size="icon"
                          className="h-7 w-7 text-zinc-500 hover:text-violet-300 hover:bg-violet-400/10"
                          onClick={() => { void compareRevisions(target.id) }}
                          disabled={revisionLoading[target.id]}
                          title="Compare Deployment Revisions"
                        >
                          {revisionLoading[target.id] ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : <GitCompareArrows className="w-3.5 h-3.5" />}
                        </Button>
                        <Button
                          variant="ghost"
                          size="icon"
                          className="h-7 w-7 text-zinc-500 hover:text-cyan-300 hover:bg-cyan-400/10"
                          onClick={() => { void runPreflight(target.id) }}
                          disabled={preflightLoading[target.id]}
                          title="Run Deploy Preflight"
                        >
                          {preflightLoading[target.id] ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : <ShieldCheck className="w-3.5 h-3.5" />}
                        </Button>
                        <Button
                          variant="ghost"
                          size="icon"
                          className="h-7 w-7 text-zinc-500 hover:text-green-400 hover:bg-green-400/10"
                          onClick={() => manualDeploy.mutate(target.id)}
                          disabled={manualDeploy.isPending || preflight?.eligible !== true}
                          title={preflight?.eligible === true ? 'Manual Deploy' : 'Run a successful preflight before deploying'}
                        >
                          <Play className="w-3.5 h-3.5" />
                        </Button>
                        <Button
                          variant="ghost"
                          size="icon"
                          className="h-7 w-7 text-zinc-500 hover:text-amber-400 hover:bg-amber-400/10"
                          onClick={() => rollback.mutate(target.id)}
                          disabled={rollback.isPending || !rollbackEligible}
                          title={rollbackEligible ? 'Rollback' : rollbackAvailable ? 'Run preflight before rollback' : 'No rollback revision is available'}
                        >
                          <RotateCcw className="w-3.5 h-3.5" />
                        </Button>
                        {target.deploymentKind === 'compose' && (
                          <Button
                            aria-label={`Project services for ${target.name}`}
                            variant="ghost"
                            size="icon"
                            className="h-7 w-7 text-zinc-500 hover:text-cyan-300 hover:bg-cyan-400/10"
                            onClick={() => toggleComposeServices(target.id)}
                            title="Project Services"
                          >
                            <Box className="w-3.5 h-3.5" />
                          </Button>
                        )}
                        {target.deploymentKind === 'compose' && currentUser?.role === 'admin' && (
                          <Button
                            aria-label={`Project environment for ${target.name}`}
                            variant="ghost"
                            size="icon"
                            className="h-7 w-7 text-zinc-500 hover:text-violet-300 hover:bg-violet-400/10"
                            onClick={() => setEnvironmentTarget(target)}
                            title="Project Environment"
                          >
                            <KeyRound className="w-3.5 h-3.5" />
                          </Button>
                        )}
                        {target.deploymentKind === 'compose' && (
                          <Button
                            aria-label={`Project domains for ${target.name}`}
                            variant="ghost"
                            size="icon"
                            className="h-7 w-7 text-zinc-500 hover:bg-zinc-700 hover:text-sky-300"
                            onClick={() => setDomainsTarget(target)}
                            title="Project Domains"
                          >
                            <Globe2 className="size-3.5" />
                          </Button>
                        )}
                        <Button
                          variant="ghost"
                          size="icon"
                          className="h-7 w-7 text-zinc-500 hover:text-white hover:bg-zinc-700"
                          onClick={() => toggleHistory(target.id)}
                          title="Deployment History"
                        >
                          {histOpen ? (
                            <ChevronDown className="w-3.5 h-3.5" />
                          ) : (
                            <ChevronRight className="w-3.5 h-3.5" />
                          )}
                        </Button>
                      </div>
                    </div>

                    {preflight && (
                      <div className={`border-t px-4 py-3 ${preflight.eligible ? preflightPending ? 'border-amber-500/20 bg-amber-500/[0.04]' : 'border-emerald-500/20 bg-emerald-500/[0.04]' : 'border-red-500/20 bg-red-500/[0.04]'}`}>
                        <div className="mb-2 flex items-center gap-2">
                          {preflight.eligible ? preflightPending ? <Clock className="size-4 text-amber-400" /> : <CircleCheck className="size-4 text-emerald-400" /> : <CircleX className="size-4 text-red-400" />}
                          <p className={`text-xs font-medium ${preflight.eligible ? preflightPending ? 'text-amber-300' : 'text-emerald-300' : 'text-red-300'}`}>
                            {preflight.eligible ? preflightPending ? 'Ready for first deployment' : 'Ready to deploy' : 'Deployment blocked by preflight'}
                          </p>
                          {!preflight.eligible && rollbackEligible && (
                            <Badge className="border-amber-500/20 bg-amber-500/10 text-amber-300">Rollback remains available</Badge>
                          )}
                        </div>
                        <div className="grid gap-1 sm:grid-cols-2">
                          {preflight.checks.map((check) => (
                            <div key={check.id} className="flex items-start gap-2 text-xs text-zinc-400">
                              {check.status === 'pass' ? <CircleCheck className="mt-0.5 size-3 flex-shrink-0 text-emerald-400" /> : check.status === 'pending' ? <Clock className="mt-0.5 size-3 flex-shrink-0 text-amber-400" /> : <CircleX className="mt-0.5 size-3 flex-shrink-0 text-red-400" />}
                              <span>{check.message}</span>
                            </div>
                          ))}
                        </div>
                      </div>
                    )}

                    {(revision || revisionError) && (
                      <div className={`border-t px-4 py-3 ${revision?.state === 'ready' ? 'border-violet-500/20 bg-violet-500/[0.04]' : revision?.state === 'not_deployed' ? 'border-amber-500/20 bg-amber-500/[0.04]' : 'border-red-500/20 bg-red-500/[0.04]'}`}>
                        {revisionError ? (
                          <div className="flex flex-wrap items-center gap-2">
                            <CircleX className="size-4 text-red-400" />
                            <p className="text-xs font-medium text-red-300">Revision comparison could not be loaded.</p>
                            <span className="text-xs text-zinc-600">{revisionError}</span>
                            <Button type="button" variant="outline" size="sm" className="ml-auto h-7 border-red-500/30 text-red-200" onClick={() => { void compareRevisions(target.id) }} disabled={revisionLoading[target.id]}>
                              <RefreshCw className={`mr-1.5 size-3 ${revisionLoading[target.id] ? 'animate-spin' : ''}`} />Retry
                            </Button>
                          </div>
                        ) : revision ? (
                          <div className="space-y-2">
                            <div className="flex flex-wrap items-center gap-2">
                              <GitCompareArrows className={`size-4 ${revision.state === 'ready' ? 'text-violet-300' : revision.state === 'not_deployed' ? 'text-amber-300' : 'text-red-300'}`} />
                              <p className={`text-xs font-medium ${revision.state === 'ready' ? 'text-violet-200' : revision.state === 'not_deployed' ? 'text-amber-200' : 'text-red-200'}`}>
                                {revision.state === 'ready' ? 'Deployment revision comparison' : revision.state === 'not_deployed' ? 'Checkout not deployed yet' : 'Checkout revision unavailable'}
                              </p>
                              {revision.state === 'ready' && revision.matchesDeployed && <Badge className="border-emerald-500/20 bg-emerald-500/10 text-emerald-300">Matches latest deployment</Badge>}
                              {revision.state === 'ready' && revision.trackedChanges && <Badge className="border-amber-500/20 bg-amber-500/10 text-amber-300">Tracked checkout changes</Badge>}
                            </div>
                            <p className="text-[11px] text-zinc-500">{revision.message}</p>
                            {revision.state === 'ready' && (
                              <div className="grid gap-2 sm:grid-cols-3">
                                <div className="rounded border border-zinc-800 bg-zinc-950/70 px-3 py-2">
                                  <p className="text-[10px] uppercase tracking-wide text-zinc-600">Current checkout</p>
                                  <p className="mt-1 font-mono text-xs text-zinc-300">{revision.currentCommit?.slice(0, 12) || 'Unavailable'}</p>
                                </div>
                                <div className="rounded border border-zinc-800 bg-zinc-950/70 px-3 py-2">
                                  <p className="text-[10px] uppercase tracking-wide text-zinc-600">Latest deployment</p>
                                  <p className="mt-1 font-mono text-xs text-zinc-300">{revision.deployedCommit?.slice(0, 12) || 'No successful run'}</p>
                                </div>
                                <div className="rounded border border-zinc-800 bg-zinc-950/70 px-3 py-2">
                                  <p className="text-[10px] uppercase tracking-wide text-zinc-600">Rollback revision</p>
                                  <p className="mt-1 font-mono text-xs text-zinc-300">{revision.rollbackCommit?.slice(0, 12) || 'Not available'}</p>
                                </div>
                              </div>
                            )}
                            {revision.state === 'ready' && revision.rollbackAvailable && (
                              <div className="flex flex-wrap gap-2 text-[11px] text-zinc-400">
                                <span className="rounded bg-zinc-800 px-2 py-1">{revision.commitsAheadRollback} commit{revision.commitsAheadRollback === 1 ? '' : 's'} ahead of rollback</span>
                                {revision.commitsBehindRollback > 0 && <span className="rounded bg-red-500/10 px-2 py-1 text-red-300">{revision.commitsBehindRollback} behind rollback</span>}
                                <span className="rounded bg-zinc-800 px-2 py-1">{revision.filesChanged} files · +{revision.insertions} −{revision.deletions}</span>
                              </div>
                            )}
                          </div>
                        ) : null}
                      </div>
                    )}

                    {servicesOpen && target.deploymentKind === 'compose' && (
                      <div className="border-t border-cyan-500/15 bg-cyan-500/[0.025] px-4 py-3">
                        <div className="mb-3 flex items-center gap-2">
                          <Box className="size-3.5 text-cyan-400" />
                          <p className="text-xs font-medium text-cyan-200">Project Services</p>
                          <span className="text-[11px] text-zinc-600">Observed from this target&apos;s Compose project</span>
                          <Button
                            aria-label={`Refresh services for ${target.name}`}
                            type="button"
                            variant="ghost"
                            size="icon"
                            className="ml-auto size-7 text-zinc-500 hover:text-cyan-300"
                            onClick={() => { void loadComposeServices(target.id) }}
                            disabled={composeServicesLoading[target.id]}
                          >
                            <RefreshCw className={`size-3.5 ${composeServicesLoading[target.id] ? 'animate-spin' : ''}`} />
                          </Button>
                        </div>
                        {composeServicesLoading[target.id] && targetServices === undefined ? (
                          <div className="space-y-2">
                            {Array.from({ length: 2 }).map((_, index) => <Skeleton key={index} className="h-14 bg-zinc-800" />)}
                          </div>
                        ) : composeServicesError[target.id] ? (
                          <div className="rounded-lg border border-red-500/20 bg-red-500/[0.05] p-3">
                            <p className="text-xs text-red-300">Compose services could not be loaded.</p>
                            <p className="mt-1 text-[11px] text-zinc-600">{composeServicesError[target.id]}</p>
                          </div>
                        ) : targetServices && targetServices.length > 0 ? (
                          <div className="space-y-2">
                            {targetServices.map((service) => {
                              const key = `${target.id}:${service.container}`
                              const logs = composeServiceLogs[key]
                              const serviceBusy = composeServiceAction.isPending && composeServiceAction.variables?.targetId === target.id && composeServiceAction.variables?.service === service.service
                              const running = service.state === 'running'
                              const stateColor = running
                                ? 'border-emerald-500/20 bg-emerald-500/10 text-emerald-300'
                                : service.state === 'exited'
                                  ? 'border-red-500/20 bg-red-500/10 text-red-300'
                                  : 'border-zinc-700 bg-zinc-800 text-zinc-400'
                              return (
                                <div key={service.container} className="rounded-lg border border-zinc-800 bg-zinc-950/70">
                                  <div className="flex flex-wrap items-center gap-3 px-3 py-2.5">
                                    <div className="min-w-0 flex-1">
                                      <div className="flex flex-wrap items-center gap-2">
                                        <span className="text-sm font-medium text-white">{service.service}</span>
                                        <Badge className={`border text-[10px] ${stateColor}`}>{service.state || 'unknown'}</Badge>
                                        {service.health && <Badge className="border border-blue-500/20 bg-blue-500/10 text-[10px] text-blue-300">{service.health}</Badge>}
                                        {!running && service.exitCode !== 0 && <span className="text-[11px] text-red-300">exit {service.exitCode}</span>}
                                      </div>
                                      <p className="mt-0.5 truncate font-mono text-[11px] text-zinc-500">{service.container} · {service.image}</p>
                                      {service.ports.length > 0 && (
                                        <div className="mt-1 flex flex-wrap gap-1">
                                          {service.ports.map((port) => <span key={port} className="rounded bg-zinc-800 px-1.5 py-0.5 font-mono text-[10px] text-zinc-400">{port}</span>)}
                                        </div>
                                      )}
                                    </div>
                                    <div className="flex items-center gap-1">
                                      <Button
                                        aria-label={`${logs ? 'Hide' : 'View'} logs for ${service.service}`}
                                        variant="ghost"
                                        size="icon"
                                        className="size-7 text-zinc-500 hover:text-purple-300"
                                        onClick={() => { void toggleComposeServiceLogs(target.id, service) }}
                                        disabled={composeServiceLogsLoading[key]}
                                      >
                                        {composeServiceLogsLoading[key] ? <Loader2 className="size-3.5 animate-spin" /> : <FileText className="size-3.5" />}
                                      </Button>
                                      <Button
                                        aria-label={`${running ? 'Stop' : 'Start'} ${service.service}`}
                                        variant="ghost"
                                        size="icon"
                                        className={`size-7 text-zinc-500 ${running ? 'hover:text-amber-300' : 'hover:text-emerald-300'}`}
                                        onClick={() => composeServiceAction.mutate({ targetId: target.id, service: service.service, action: running ? 'stop' : 'start' })}
                                        disabled={serviceBusy}
                                      >
                                        {running ? <Square className="size-3.5" /> : <Play className="size-3.5" />}
                                      </Button>
                                      <Button
                                        aria-label={`Restart ${service.service}`}
                                        variant="ghost"
                                        size="icon"
                                        className="size-7 text-zinc-500 hover:text-blue-300"
                                        onClick={() => composeServiceAction.mutate({ targetId: target.id, service: service.service, action: 'restart' })}
                                        disabled={serviceBusy}
                                      >
                                        <RotateCcw className="size-3.5" />
                                      </Button>
                                      <Button
                                        aria-label={`Recreate ${service.service}`}
                                        variant="ghost"
                                        size="icon"
                                        className="size-7 text-zinc-500 hover:text-cyan-300"
                                        onClick={() => composeServiceAction.mutate({ targetId: target.id, service: service.service, action: 'recreate' })}
                                        disabled={serviceBusy}
                                      >
                                        <RefreshCw className={`size-3.5 ${serviceBusy && composeServiceAction.variables?.action === 'recreate' ? 'animate-spin' : ''}`} />
                                      </Button>
                                    </div>
                                  </div>
                                  {logs && (
                                    <div className="border-t border-zinc-800 px-3 pb-3">
                                      <div className="mt-2 flex items-center gap-2 text-[11px] text-purple-300">
                                        <FileText className="size-3" />Last {logs.tail} lines
                                        {logs.truncated && <Badge className="border-amber-500/20 bg-amber-500/10 text-amber-300">1 MiB response limit reached</Badge>}
                                      </div>
                                      <pre className="mt-2 max-h-56 overflow-y-auto whitespace-pre-wrap break-all rounded bg-black p-3 font-mono text-xs text-zinc-300">{logs.logs || '(no output)'}</pre>
                                    </div>
                                  )}
                                </div>
                              )
                            })}
                          </div>
                        ) : (
                          <p className="rounded-lg border border-zinc-800 bg-zinc-950/60 px-3 py-6 text-center text-xs text-zinc-600">No Compose services are currently observed.</p>
                        )}
                      </div>
                    )}

                    {histOpen && (
                      <div className="bg-zinc-950 border-t border-zinc-800 px-4 py-3">
                        <p className="text-zinc-500 text-xs font-medium mb-2 flex items-center gap-1">
                          <RefreshCw className="w-3 h-3" />
                          Deployment History
                        </p>
                        {historyQuery.isLoading ? (
                          <div className="space-y-2">
                            {Array.from({ length: 2 }).map((_, i) => (
                              <Skeleton key={i} className="h-8 w-full bg-zinc-800" />
                            ))}
                          </div>
                        ) : historyQuery.isError ? (
                          <div className="rounded-lg border border-red-500/20 bg-red-500/[0.05] p-4 text-center">
                            <p className="text-xs text-red-300">Deployment history could not be loaded.</p>
                            <p className="mt-1 text-[11px] text-zinc-600">{historyQuery.error.message}</p>
                            <Button type="button" variant="outline" size="sm" className="mt-3 border-red-500/30 text-red-200" onClick={() => { void historyQuery.refetch() }} disabled={historyQuery.isFetching}>
                              <RefreshCw className={`mr-2 size-3.5 ${historyQuery.isFetching ? 'animate-spin' : ''}`} />Retry
                            </Button>
                          </div>
                        ) : runs.length > 0 ? (
                          <div className="space-y-1">
                            {runs.map((run) => {
                              const rc = deployStatusConfig[run.status]
                              const logOpen = expandedLogs[run.id] !== undefined
                              return (
                                <div
                                  key={run.id}
                                  className="rounded-lg bg-zinc-900 border border-zinc-800"
                                >
                                  <div
                                    className="flex items-center gap-3 px-3 py-2 cursor-pointer hover:bg-zinc-800/50 transition-colors"
                                    onClick={() => toggleRunLog(run.id)}
                                  >
                                    <Badge
                                      className={`text-xs border flex-shrink-0 ${rc.color}`}
                                    >
                                      {rc.label}
                                    </Badge>
                                    {(run.commit || run.commitSha) && (
                                      <span className="text-zinc-500 text-xs font-mono flex items-center gap-1">
                                        <GitCommit className="w-3 h-3" />
                                        {(run.commit || run.commitSha!).slice(0, 8)}
                                      </span>
                                    )}
                                    <span className="text-zinc-600 text-xs flex items-center gap-1">
                                      <Clock className="w-3 h-3" />
                                      {formatDate(run.startedAt)}
                                    </span>
                                    <span className="text-zinc-600 text-xs ml-auto">
                                      {formatDuration(run.durationMs ? Math.round(run.durationMs / 1000) : run.duration)}
                                    </span>
                                    {loadingLogs[run.id] ? (
                                      <Loader2 className="w-3.5 h-3.5 text-zinc-500 animate-spin flex-shrink-0" />
                                    ) : logOpen ? (
                                      <ChevronDown className="w-3.5 h-3.5 text-zinc-500 flex-shrink-0" />
                                    ) : (
                                      <ChevronRight className="w-3.5 h-3.5 text-zinc-500 flex-shrink-0" />
                                    )}
                                  </div>
                                  {logOpen && (
                                    <div className="px-3 pb-3 border-t border-zinc-800">
                                      <pre className="text-xs text-zinc-300 font-mono bg-black rounded p-3 mt-2 max-h-48 overflow-y-auto whitespace-pre-wrap break-all">
                                        {expandedLogs[run.id] || '(no output)'}
                                      </pre>
                                    </div>
                                  )}
                                </div>
                              )
                            })}
                          </div>
                        ) : (
                          <p className="text-zinc-600 text-xs">No deployments yet</p>
                        )}
                      </div>
                    )}
                  </div>
                )
              })}
            </div>
          ) : (
            <div className="flex flex-col items-center justify-center py-16 text-zinc-600">
              <Rocket className="w-8 h-8 mb-3 opacity-50" />
              <p className="text-sm">No deploy targets configured</p>
            </div>
          )}
        </CardContent>
      </Card>

      <ProjectEnvironmentDialog
        open={environmentTarget !== null}
        targetId={environmentTarget?.id ?? null}
        targetName={environmentTarget?.name ?? ''}
        onOpenChange={(open) => { if (!open) setEnvironmentTarget(null) }}
      />
      <ProjectDomainsDialog
        open={domainsTarget !== null}
        onOpenChange={(open) => { if (!open) setDomainsTarget(null) }}
        targetId={domainsTarget?.id ?? null}
        targetName={domainsTarget?.name ?? ''}
        canManage={currentUser?.role === 'admin'}
      />

      <Dialog open={stagingTarget !== null} onOpenChange={(open) => { if (!open) closeStagingDialog() }}>
        <DialogContent className="max-w-xl border-zinc-800 bg-zinc-900 text-white">
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2 text-white">
              <GitBranchPlus className="size-4 text-amber-400" />
              Create Staging Environment · {stagingTarget?.name}
            </DialogTitle>
          </DialogHeader>
          <div className="space-y-4 py-1">
            <div className="rounded-lg border border-amber-500/20 bg-amber-500/[0.05] p-3">
              <p className="text-xs font-medium text-amber-200">Explicit isolation boundary</p>
              <ul className="mt-1 space-y-1 text-[11px] leading-5 text-zinc-500">
                <li>• Uses a separate project directory that cannot overlap another deployment.</li>
                <li>• Environment values and webhook signing secrets are not copied.</li>
                <li>• Auto-deploy starts disabled.</li>
                <li>• Domains, TLS, and DNS state are not copied or configured.</li>
              </ul>
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="staging-name" className="text-xs text-zinc-400">Staging name</Label>
              <Input
                id="staging-name"
                value={stagingForm.name}
                onChange={(event) => setStagingForm((current) => ({ ...current, name: event.target.value }))}
                className="border-zinc-700 bg-zinc-800 text-white"
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="staging-branch" className="text-xs text-zinc-400">Staging branch</Label>
              <Input
                id="staging-branch"
                value={stagingForm.branch}
                onChange={(event) => setStagingForm((current) => ({ ...current, branch: event.target.value }))}
                className="border-zinc-700 bg-zinc-800 font-mono text-white"
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="staging-project-directory" className="text-xs text-zinc-400">Isolated project directory</Label>
              <Input
                id="staging-project-directory"
                value={stagingForm.projectDir}
                onChange={(event) => setStagingForm((current) => ({ ...current, projectDir: event.target.value }))}
                className="border-zinc-700 bg-zinc-800 font-mono text-white"
              />
              <p className="text-[11px] text-zinc-600">Use an absolute path reserved only for this staging target.</p>
            </div>
          </div>
          <DialogFooter>
            <Button type="button" variant="ghost" className="text-zinc-400" onClick={closeStagingDialog}>Cancel</Button>
            <Button
              type="button"
              className="bg-amber-600 text-white hover:bg-amber-500"
              disabled={!stagingTarget || !stagingForm.name.trim() || !stagingForm.branch.trim() || !stagingForm.projectDir.trim() || createStaging.isPending}
              onClick={() => {
                if (stagingTarget) createStaging.mutate({ sourceTargetId: stagingTarget.id, form: stagingForm })
              }}
            >
              {createStaging.isPending && <Loader2 className="mr-2 size-3.5 animate-spin" />}
              Create staging environment
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog
        open={pendingDeleteTarget !== null}
        onOpenChange={(open) => { if (!open && !removeTarget.isPending) setPendingDeleteTarget(null) }}
      >
        <DialogContent className="max-w-md border-zinc-800 bg-zinc-900 text-white">
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2 text-white">
              <AlertTriangle className="size-4 text-red-400" />
              Delete Deploy Target?
            </DialogTitle>
          </DialogHeader>
          <div className="space-y-3 text-sm text-zinc-400">
            <p>
              Delete <span className="font-medium text-white">{pendingDeleteTarget?.name}</span> and its deployment history?
            </p>
            <p className="text-xs leading-5 text-zinc-500">
              Project domains and staging environments are not removed automatically. The server will keep this target when dependent resources are still attached.
            </p>
            {removeTarget.isError && (
              <p role="alert" className="rounded-lg border border-red-500/20 bg-red-500/[0.06] p-3 text-xs text-red-300">
                {mutationErrorMessage(removeTarget.error, 'Failed to delete deploy target')}
              </p>
            )}
          </div>
          <DialogFooter>
            <Button
              type="button"
              variant="ghost"
              onClick={() => setPendingDeleteTarget(null)}
              disabled={removeTarget.isPending}
              className="text-zinc-400 hover:text-white"
            >
              Cancel
            </Button>
            <Button
              type="button"
              variant="destructive"
              onClick={() => { if (pendingDeleteTarget) removeTarget.mutate(pendingDeleteTarget) }}
              disabled={!pendingDeleteTarget || removeTarget.isPending}
            >
              {removeTarget.isPending ? <Loader2 className="mr-2 size-3.5 animate-spin" /> : <Trash2 className="mr-2 size-3.5" />}
              Delete Target
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Add Target Dialog */}
      <Dialog open={addDialogOpen || editingTarget !== null} onOpenChange={handleTargetDialogOpenChange}>
        <DialogContent className="bg-zinc-900 border-zinc-800 text-white max-w-2xl max-h-[85vh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle className="text-white flex items-center gap-2">
              {editingTarget ? <Pencil className="w-4 h-4 text-blue-400" /> : <Plus className="w-4 h-4 text-blue-400" />}
              {editingTarget ? `Edit Deploy Target · ${editingTarget.name}` : 'Add Deploy Target'}
            </DialogTitle>
          </DialogHeader>
          <div className="space-y-4 py-2">
            {!editingTarget && <div className="space-y-2 rounded-lg border border-zinc-800 bg-zinc-950/40 p-3">
              <Label htmlFor="deploy-template" className="text-zinc-400 text-sm">Installation Template</Label>
              <select
                id="deploy-template"
                aria-label="Deployment template"
                value={selectedTemplateID}
                onChange={(event) => applyTemplate(event.target.value)}
                disabled={templatesQuery.isLoading || templates.length === 0}
                className="h-10 w-full rounded-md border border-zinc-700 bg-zinc-800 px-3 text-sm text-white disabled:cursor-not-allowed disabled:opacity-60"
              >
                <option value="">Custom configuration</option>
                {templates.map((template) => (
                  <option key={template.id} value={template.id}>{template.name}</option>
                ))}
              </select>
              {templatesQuery.isLoading && <p className="text-xs text-zinc-500">Loading installation templates…</p>}
              {templatesQuery.isError && (
                <p className="text-xs text-red-400">Deployment templates could not be loaded. Custom configuration remains available.</p>
              )}
              {templateInventory?.status === 'not_configured' && (
                <div className="space-y-1 text-xs text-zinc-500">
                  <p>No installation templates configured. Custom configuration remains available.</p>
                  <p className="break-all font-mono text-zinc-600">{templateInventory.directory}</p>
                </div>
              )}
              {templateInventory?.status === 'unavailable' && (
                <div className="space-y-1 text-xs text-amber-400">
                  <p>Some deployment templates are unavailable. Valid templates remain selectable.</p>
                  {templateInventory.issues.map((issue) => (
                    <p key={`${issue.file}:${issue.message}`} className="break-words text-amber-500/80">
                      <span className="font-mono">{issue.file}</span>: {issue.message}
                    </p>
                  ))}
                </div>
              )}
              {selectedTemplateID && templates.find((item) => item.id === selectedTemplateID)?.description && (
                <p className="text-xs text-zinc-500">
                  {templates.find((item) => item.id === selectedTemplateID)?.description}
                </p>
              )}
            </div>}
            <div className="space-y-2">
              <Label className="text-zinc-400 text-sm">Deployment Type</Label>
              <div className="grid grid-cols-2 gap-2">
                <button
                  type="button"
                  onClick={() => { setSelectedTemplateID(''); setForm((current) => ({ ...current, deploymentKind: 'compose', deployScript: '' })) }}
                  className={`rounded-lg border p-3 text-left transition-colors ${form.deploymentKind === 'compose' ? 'border-cyan-500/50 bg-cyan-500/10' : 'border-zinc-700 bg-zinc-800/60 hover:border-zinc-600'}`}
                >
                  <span className="flex items-center gap-2 text-sm font-medium text-white"><Box className="size-4 text-cyan-400" />Docker Compose</span>
                  <span className="mt-1 block text-xs text-zinc-500">Validated config and fixed up command</span>
                </button>
                <button
                  type="button"
                  onClick={() => { setSelectedTemplateID(''); setForm((current) => ({ ...current, deploymentKind: 'script', composeFile: '' })) }}
                  className={`rounded-lg border p-3 text-left transition-colors ${form.deploymentKind === 'script' ? 'border-blue-500/50 bg-blue-500/10' : 'border-zinc-700 bg-zinc-800/60 hover:border-zinc-600'}`}
                >
                  <span className="flex items-center gap-2 text-sm font-medium text-white"><Rocket className="size-4 text-blue-400" />Script</span>
                  <span className="mt-1 block text-xs text-zinc-500">Existing installation-owned workflow</span>
                </button>
              </div>
            </div>
            <div className="space-y-2">
              <Label className="text-zinc-400 text-sm">Name</Label>
              <Input
                placeholder="e.g. my-app"
                value={form.name}
                onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
                className="bg-zinc-800 border-zinc-700 text-white placeholder-zinc-500 focus:border-blue-500"
              />
            </div>
            <div className="space-y-2">
              <Label className="text-zinc-400 text-sm">Repository URL</Label>
              <Input
                placeholder="git@github.com:org/repo.git"
                value={form.repoUrl}
                onChange={(e) => setForm((f) => ({ ...f, repoUrl: e.target.value }))}
                className="bg-zinc-800 border-zinc-700 text-white placeholder-zinc-500 focus:border-blue-500"
              />
              <p className="text-xs text-zinc-600">Use token-free HTTPS or SSH. Missing projects are cloned on the first deployment.</p>
            </div>
            <div className="space-y-2">
              <Label className="text-zinc-400 text-sm">Project Directory</Label>
              <Input
                placeholder="e.g. /srv/apps/example-app"
                value={form.projectDir}
                onChange={(e) => setForm((f) => ({ ...f, projectDir: e.target.value }))}
                className="bg-zinc-800 border-zinc-700 text-white placeholder-zinc-500 focus:border-blue-500"
              />
            </div>
            <div className="space-y-2">
              <Label className="text-zinc-400 text-sm">Webhook Provider</Label>
              <div className="grid grid-cols-2 gap-2">
                <button
                  type="button"
                  aria-label="Select GitHub webhook provider"
                  onClick={() => setForm((current) => ({ ...current, webhookProvider: 'github', webhookToken: '', autoDeploy: false, clearWebhookToken: false }))}
                  className={`rounded-lg border p-3 text-left transition-colors ${form.webhookProvider === 'github' ? 'border-violet-500/50 bg-violet-500/10' : 'border-zinc-700 bg-zinc-800/60 hover:border-zinc-600'}`}
                >
                  <span className="block text-sm font-medium text-white">GitHub</span>
                  <span className="mt-1 block text-xs text-zinc-500">HMAC secret and unique delivery ID</span>
                </button>
                <button
                  type="button"
                  aria-label="Select GitLab webhook provider"
                  onClick={() => setForm((current) => ({ ...current, webhookProvider: 'gitlab', webhookToken: '', autoDeploy: false, clearWebhookToken: false }))}
                  className={`rounded-lg border p-3 text-left transition-colors ${form.webhookProvider === 'gitlab' ? 'border-orange-500/50 bg-orange-500/10' : 'border-zinc-700 bg-zinc-800/60 hover:border-zinc-600'}`}
                >
                  <span className="block text-sm font-medium text-white">GitLab</span>
                  <span className="mt-1 block text-xs text-zinc-500">Standard Webhooks signed delivery</span>
                </button>
              </div>
            </div>
            <div className="grid grid-cols-2 gap-3">
              <div className="space-y-2">
                <Label className="text-zinc-400 text-sm">Branch</Label>
                <Input
                  placeholder="main"
                  value={form.branch}
                  onChange={(e) => { setSelectedTemplateID(''); setForm((f) => ({ ...f, branch: e.target.value })) }}
                  className="bg-zinc-800 border-zinc-700 text-white placeholder-zinc-500 focus:border-blue-500"
                />
              </div>
              <div className="space-y-2">
                <Label className="text-zinc-400 text-sm">
                  {form.webhookProvider === 'gitlab' ? 'GitLab Signing Token' : 'GitHub Webhook Secret'}
                </Label>
                <Input
                  type="password"
                  autoComplete="new-password"
                  placeholder={editingTarget ? 'Leave blank to keep current secret' : form.webhookProvider === 'gitlab' ? 'whsec_...' : 'optional'}
                  value={form.webhookToken}
                  onChange={(e) => setForm((f) => ({
                    ...f,
                    webhookToken: e.target.value,
                    clearWebhookToken: e.target.value.trim() ? false : f.clearWebhookToken,
                    autoDeploy: e.target.value.trim() ? f.autoDeploy : false,
                  }))}
                  className="bg-zinc-800 border-zinc-700 text-white placeholder-zinc-500 focus:border-blue-500"
                />
                <p className="text-xs text-zinc-600">
                  {editingTarget
                    ? 'Leave blank to preserve the existing write-only secret. Enter a replacement only when it should change.'
                    : form.webhookProvider === 'gitlab'
                    ? 'Use the whsec_ signing token from GitLab Standard Webhooks, not a legacy plaintext token.'
                    : 'Use the same secret configured for GitHub X-Hub-Signature-256 verification.'}
                </p>
              </div>
            </div>
            {editingTarget && canClearWebhookToken && (
              <label className={`flex items-start gap-3 rounded-lg border p-3 ${form.clearWebhookToken ? 'cursor-pointer border-red-500/30 bg-red-500/[0.05]' : 'cursor-pointer border-zinc-700 bg-zinc-800/50'}`}>
                <input
                  type="checkbox"
                  checked={form.clearWebhookToken}
                  onChange={(event) => setForm((current) => ({
                    ...current,
                    clearWebhookToken: event.target.checked,
                    autoDeploy: event.target.checked ? false : current.autoDeploy,
                  }))}
                  className="mt-0.5 size-4 rounded border-zinc-600 bg-zinc-800 text-red-500"
                />
                <span>
                  <span className="block text-sm text-zinc-300">Clear existing webhook secret</span>
                  <span className="mt-0.5 block text-xs text-zinc-600">This also disables automatic deployment. The current secret is never displayed.</span>
                </span>
              </label>
            )}
            <label className={`flex items-start gap-3 rounded-lg border p-3 ${webhookSecretAvailable && !form.clearWebhookToken ? 'cursor-pointer border-zinc-700 bg-zinc-800/50' : 'border-zinc-800 bg-zinc-900 opacity-60'}`}>
              <input
                type="checkbox"
                checked={form.autoDeploy}
                disabled={!webhookSecretAvailable || form.clearWebhookToken}
                onChange={(event) => setForm((current) => ({ ...current, autoDeploy: event.target.checked }))}
                className="mt-0.5 size-4 rounded border-zinc-600 bg-zinc-800 text-blue-500"
              />
              <span>
                <span className="block text-sm text-zinc-300">Deploy matching branch pushes automatically</span>
                <span className="mt-0.5 block text-xs text-zinc-600">Requires the selected provider signing secret; server-side preflight still runs before every queued deployment.</span>
              </span>
            </label>
            {form.deploymentKind === 'compose' ? (
              <div className="space-y-2">
                <Label className="text-zinc-400 text-sm">Compose File</Label>
                <Input
                  placeholder="Auto-detect, or e.g. deploy/compose.yaml"
                  value={form.composeFile}
                  onChange={(e) => { setSelectedTemplateID(''); setForm((f) => ({ ...f, composeFile: e.target.value })) }}
                  className="bg-zinc-800 border-zinc-700 text-white placeholder-zinc-500 focus:border-cyan-500"
                />
                <p className="text-xs text-zinc-600">Leave empty to use Docker Compose file discovery. Explicit files must remain inside the project directory.</p>
              </div>
            ) : (
              <div className="space-y-2">
                <Label className="text-zinc-400 text-sm">Deploy Script</Label>
                <textarea
                  placeholder="e.g. ./deploy.sh or npm run build && pm2 restart app"
                  value={form.deployScript}
                  onChange={(e) => { setSelectedTemplateID(''); setForm((f) => ({ ...f, deployScript: e.target.value })) }}
                  className="flex min-h-28 w-full rounded-md border border-zinc-700 bg-zinc-800 px-3 py-2 font-mono text-sm text-white placeholder:text-zinc-500 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-blue-500"
                />
              </div>
            )}
            {editingTarget && (
              <label className="flex cursor-pointer items-start gap-3 rounded-lg border border-zinc-700 bg-zinc-800/50 p-3">
                <input
                  type="checkbox"
                  checked={form.isActive}
                  onChange={(event) => setForm((current) => ({ ...current, isActive: event.target.checked }))}
                  className="mt-0.5 size-4 rounded border-zinc-600 bg-zinc-800 text-blue-500"
                />
                <span>
                  <span className="block text-sm text-zinc-300">Target active</span>
                  <span className="mt-0.5 block text-xs text-zinc-600">Inactive targets remain configured but cannot be deployed by webhook or manual actions.</span>
                </span>
              </label>
            )}
            {editingTarget && updateTarget.isError && (
              <p role="alert" className="rounded-lg border border-red-500/20 bg-red-500/[0.06] p-3 text-xs text-red-300">
                {mutationErrorMessage(updateTarget.error, 'Failed to update deploy target')}
              </p>
            )}
          </div>
          <DialogFooter>
            <Button
              variant="ghost"
              className="text-zinc-400 hover:text-white"
              onClick={() => handleTargetDialogOpenChange(false)}
            >
              Cancel
            </Button>
            <Button
              className="bg-blue-600 hover:bg-blue-500 text-white"
              disabled={!form.name.trim() || !form.projectDir.trim() || !form.repoUrl.trim() || targetMutationPending}
              onClick={() => {
                if (editingTarget) updateTarget.mutate({ target: editingTarget, data: form })
                else addTarget.mutate(form)
              }}
            >
              {targetMutationPending ? (editingTarget ? 'Saving…' : 'Adding…') : editingTarget ? 'Save Target' : 'Add Target'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
