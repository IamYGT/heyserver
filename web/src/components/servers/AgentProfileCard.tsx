import { useMemo, useRef, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { CheckCircle2, Copy, Download, Loader2, RefreshCw, ServerCog, ShieldAlert } from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import {
  DeployProfileFields,
  deployProfileEnvFragment,
  emptyDeployProfile,
  isDeployProfileValid,
  type DeployProfile,
} from './DeployProfileFields'

export type AgentProfileObservedState = 'not_reported' | 'not_configured' | 'pending_restart' | 'applied' | 'failed'
export type AgentProfileApplyState = 'manual_required' | 'not_requested' | 'queued' | 'running' | 'awaiting_heartbeat' | 'applied' | 'failed' | 'drifted'

export interface AgentProfileResponse {
  nodeId: string
  desired: {
    state: 'not_configured' | 'configured'
    revision: number
    profile: DeployProfile | null
  }
  observed: {
    online: boolean
    lastSeenAt?: string | null
    agentVersion: string
    protocolVersion: string
    capabilities: string[]
    profileState: AgentProfileObservedState
    profileRevision?: number | null
    profileErrorCode?: string | null
  }
  apply: {
    state: AgentProfileApplyState
    reason?: string | null
    desiredRevision?: number | null
    taskId?: number | string | null
    observedRevision?: number | null
    observedState?: AgentProfileObservedState | null
  }
}

interface AgentProfileCardProps {
  nodeID: string
}

const capabilityLabels = [
  ['deploy.read', 'Deploy inventory'],
  ['deploy.action', 'Deploy actions'],
  ['deploy.domain.read', 'Domain inventory'],
  ['deploy.domain.action', 'Domain actions'],
] as const

const inFlightApplyStates: ReadonlySet<AgentProfileApplyState> = new Set(['queued', 'running', 'awaiting_heartbeat'])

function profileFromAPI(profile: DeployProfile | null | undefined): DeployProfile {
  return profile ? { ...profile, deployWriteRoots: [...profile.deployWriteRoots] } : { ...emptyDeployProfile }
}

function profileToAPI(profile: DeployProfile): DeployProfile {
  return { ...profile, deployWriteRoots: [...profile.deployWriteRoots] }
}

function formatObservedTime(value?: string | null): string {
  if (!value) return 'last heartbeat not reported'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}

function formatRevision(value?: number | null): string {
  return typeof value === 'number' && Number.isFinite(value) ? String(value) : 'not_reported'
}

function formatCode(value?: string | null): string {
  return typeof value === 'string' && value.trim() ? value : 'not_reported'
}

function formatNullable(value?: number | string | null): string {
  return value === undefined || value === null || value === '' ? 'not_reported' : String(value)
}

function isValidCurrentRevision(value?: number | null): value is number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value > 0
}

function errorStatus(error: Error): number | undefined {
  return (error as Error & { status?: number }).status
}

type ProfileOperation = 'load' | 'refresh' | 'save' | 'apply'

const knownProfileErrorMessages: Readonly<Record<string, string>> = {
  'not found': 'The managed agent profile was not found.',
  'invalid request body': 'The profile request was invalid.',
  'invalid profile': 'The desired deploy profile is invalid.',
  stale_profile_revision: 'The profile changed elsewhere. Refresh the latest profile.',
  profile_not_configured: 'Save a configured desired profile before applying it.',
  profile_apply_in_flight: 'A profile apply request is already in progress.',
  profile_apply_capability_unavailable: 'This agent does not support profile apply.',
  node_offline: 'The agent is offline.',
  'agent profile operation failed': 'The managed agent profile operation failed.',
}

function profileErrorMessage(error: Error, operation: ProfileOperation): string {
  const status = errorStatus(error)
  if (status === 403) return 'Deploy profile management is available to Admin users only.'
  if (status === 404) return 'The managed agent profile was not found.'
  if (status === 409) return knownProfileErrorMessages[error.message.trim().toLowerCase()]
    ?? 'The managed agent profile is no longer available in its current state.'
  if (status === 429) return 'Too many profile requests. Please try again shortly.'
  if (status === 0) return 'Could not reach the managed agent profile service.'
  if (status !== undefined && status >= 500) return 'The managed agent profile service is temporarily unavailable.'

  const knownMessage = knownProfileErrorMessages[error.message.trim().toLowerCase()]
  if (knownMessage) return knownMessage

  switch (operation) {
    case 'load':
      return 'Could not load the managed agent profile.'
    case 'refresh':
      return 'Could not refresh the managed agent profile.'
    case 'save':
      return 'Could not save the desired deploy profile.'
    case 'apply':
      return 'Could not apply the desired deploy profile.'
  }
}

function profileRefetchInterval(query: { state: { data?: AgentProfileResponse } }): number | false {
  const response = query.state.data
  if (!response || !inFlightApplyStates.has(response.apply?.state)) return false
  return 2_000
}

export function AgentProfileCard({ nodeID }: AgentProfileCardProps) {
  const queryClient = useQueryClient()
  const nodeBase = `/nodes/${encodeURIComponent(nodeID)}`
  const profileQuery = useQuery<AgentProfileResponse>({
    queryKey: ['managed-node-profile', nodeID],
    queryFn: () => api.get<AgentProfileResponse>(`${nodeBase}/profile`),
    enabled: Boolean(nodeID),
    retry: false,
    refetchInterval: profileRefetchInterval,
  })
  const [draftOverride, setDraftOverride] = useState<DeployProfile | null>(null)
  const [conflict, setConflict] = useState(false)
  const applyLockRef = useRef(false)

  const observed = profileQuery.data?.observed
  const desired = profileQuery.data?.desired
  const apply = profileQuery.data?.apply
  const observedOnline = observed?.online ?? false
  const draft = draftOverride ?? profileFromAPI(desired?.profile)
  const draftDirty = draftOverride !== null
  const observedCapabilities = useMemo(() => new Set(observed?.capabilities ?? []), [observed?.capabilities])
  const valid = isDeployProfileValid(draft)
  const applyState = apply?.state ?? 'manual_required'
  const supportsApply = observedCapabilities.has('agent.profile.apply')
  const desiredConfigured = desired?.state === 'configured' && desired.profile !== null
  const validDesiredRevision = isValidCurrentRevision(desired?.revision)
  const applyRevisionMatches = apply?.desiredRevision === undefined
    || apply?.desiredRevision === null
    || apply.desiredRevision === desired?.revision
  const applyInFlight = inFlightApplyStates.has(applyState)
  const canApply = supportsApply
    && desiredConfigured
    && validDesiredRevision
    && applyRevisionMatches
    && observedOnline
    && !applyInFlight

  const saveMutation = useMutation<AgentProfileResponse, Error>({
    mutationFn: () => api.put<AgentProfileResponse>(`${nodeBase}/profile`, {
      profile: profileToAPI(draft),
      expectedRevision: desired?.revision ?? 0,
    }),
    onSuccess: async (response) => {
      setConflict(false)
      setDraftOverride(null)
      queryClient.setQueryData(['managed-node-profile', nodeID], response)
      toast.success('Desired deploy profile saved')
    },
    onError: (error) => {
      if (errorStatus(error) === 409) {
        setConflict(true)
        return
      }
      toast.error(profileErrorMessage(error, 'save'))
    },
  })

  const applyMutation = useMutation<unknown, Error, number>({
    mutationFn: (revision) => api.post<unknown>(`${nodeBase}/profile/apply`, {
      expectedRevision: revision,
      confirmed: true,
    }),
    onSuccess: async () => {
      setConflict(false)
      // The POST receipt only acknowledges queuing. Applied state must come
      // from a later profile observation, never from the task receipt.
      await profileQuery.refetch()
      toast.success('Profile apply accepted; waiting for agent observation')
    },
    onError: (error) => {
      if (errorStatus(error) === 409) {
        setConflict(true)
        return
      }
      toast.error(profileErrorMessage(error, 'apply'))
    },
    onSettled: () => {
      applyLockRef.current = false
    },
  })

  const refreshLatest = async () => {
    const result = await profileQuery.refetch()
    if (result.data) {
      setDraftOverride(null)
      setConflict(false)
    }
  }

  const copyFragment = async () => {
    try {
      await navigator.clipboard.writeText(deployProfileEnvFragment(draft))
      toast.success('Deploy env fragment copied')
    } catch {
      toast.error('Could not copy the deploy env fragment')
    }
  }

  const downloadFragment = () => {
    const url = URL.createObjectURL(new Blob([deployProfileEnvFragment(draft)], { type: 'text/plain;charset=utf-8' }))
    const anchor = document.createElement('a')
    anchor.href = url
    anchor.download = `hserver-agent-${nodeID}-deploy-profile.env`
    document.body.appendChild(anchor)
    anchor.click()
    anchor.remove()
    URL.revokeObjectURL(url)
  }

  const applyDisabledReason = applyMutation.isPending
    ? 'An apply request is already being submitted.'
    : !observedOnline
      ? 'The agent is offline.'
      : !desiredConfigured
        ? 'Save a configured desired profile before applying it.'
        : !validDesiredRevision
          ? 'The current desired revision is unavailable.'
          : !applyRevisionMatches
            ? 'The apply revision is stale; refresh the latest profile.'
            : applyInFlight
              ? `Apply is already ${applyState}.`
              : null

  const requestApply = () => {
    const revision = desired?.revision
    if (applyLockRef.current || applyMutation.isPending || !canApply || !isValidCurrentRevision(revision)) return
    if (!window.confirm(`Apply deploy profile revision ${revision} to managed node ${nodeID}?`)) return
    applyLockRef.current = true
    applyMutation.mutate(revision)
  }

  if (profileQuery.isLoading) {
    return <Card className="border-zinc-800 bg-zinc-900/80"><CardContent className="flex h-40 items-center justify-center"><Loader2 className="size-5 animate-spin text-zinc-500" /></CardContent></Card>
  }

  if (profileQuery.error && !profileQuery.data) {
    const permissionDenied = errorStatus(profileQuery.error) === 403
    return <Card className="border-red-500/25 bg-red-500/[0.05]"><CardContent className="flex flex-col items-center gap-3 p-6 text-center"><ShieldAlert className="size-5 text-red-400" /><div><p className="text-sm font-medium text-red-300">{permissionDenied ? 'Deploy profile is available to Admin users only' : 'Could not load managed agent profile'}</p><p className="mt-1 text-xs text-zinc-500">{profileErrorMessage(profileQuery.error, 'load')}</p></div><Button type="button" variant="outline" size="sm" onClick={() => profileQuery.refetch()}><RefreshCw className="size-3.5" /> Refresh</Button></CardContent></Card>
  }

  return <Card className="border-zinc-800 bg-zinc-900/80">
    <CardHeader className="flex-row items-start justify-between gap-3">
      <div>
        <CardTitle className="flex items-center gap-2 text-sm text-zinc-200"><ServerCog className="size-4 text-violet-400" /> Managed agent deploy profile</CardTitle>
        <p className="mt-1 text-[10px] leading-relaxed text-zinc-500">Desired panel settings are separate from the agent's last advertised capabilities and observed profile state.</p>
      </div>
      <Button type="button" variant="ghost" size="xs" onClick={() => void refreshLatest()} disabled={profileQuery.isFetching}><RefreshCw className={`size-3 ${profileQuery.isFetching ? 'animate-spin' : ''}`} /> Refresh</Button>
    </CardHeader>
    <CardContent className="space-y-4">
      {profileQuery.error && <div role="alert" className="rounded-lg border border-red-500/25 bg-red-500/[0.06] p-3 text-xs text-red-200">Could not refresh the latest profile observation. The values below may be stale. <code>{profileErrorMessage(profileQuery.error, 'refresh')}</code></div>}

      <div className="grid gap-3 lg:grid-cols-2">
        <div className="rounded-xl border border-blue-500/20 bg-blue-500/[0.04] p-4">
          <div className="flex items-center justify-between gap-2"><p className="text-xs font-semibold text-blue-200">Observed · agent advertisement</p><span className="rounded-full bg-zinc-800 px-2 py-1 text-[9px] font-semibold uppercase text-zinc-400">{observedOnline ? 'online' : 'offline'}</span></div>
          <p className="mt-1 text-[10px] text-zinc-500">{observedOnline ? 'Current server heartbeat is fresh.' : `Last observed ${formatObservedTime(observed?.lastSeenAt)}; values may be stale.`}</p>
          <div className="mt-3 grid gap-2 sm:grid-cols-2">
            {capabilityLabels.map(([capability, label]) => {
              const advertised = observedCapabilities.has(capability)
              return <div key={capability} className="flex items-center justify-between gap-2 rounded-lg border border-zinc-800/80 bg-zinc-950/40 px-3 py-2"><span className="min-w-0"><span className="block text-[10px] text-zinc-300">{label}</span><code className="block text-[9px] text-zinc-600">{capability}</code></span><span className={`shrink-0 text-[9px] font-semibold uppercase ${advertised ? 'text-emerald-400' : 'text-zinc-500'}`}>{advertised ? 'Advertised' : 'Not advertised'}</span></div>
            })}
          </div>
          <div className="mt-3 grid gap-2 text-[10px] text-zinc-500 sm:grid-cols-3">
            <p>Profile state: <code className="text-zinc-300">{formatCode(observed?.profileState)}</code></p>
            <p>Profile revision: <code className="text-zinc-300">{formatRevision(observed?.profileRevision)}</code></p>
            <p>Profile error: <code className="text-zinc-300">{formatCode(observed?.profileErrorCode)}</code></p>
          </div>
          <p className="mt-2 text-[10px] text-zinc-500">Deploy paths are not inferred from generic file roots.</p>
        </div>
        <div className="rounded-xl border border-amber-500/20 bg-amber-500/[0.04] p-4">
          <p className="text-xs font-semibold text-amber-200">Desired · panel state</p>
          <p className="mt-1 text-[10px] text-zinc-500">{desired?.state === 'configured' ? `Revision ${formatRevision(desired.revision)}` : 'No desired profile has been saved yet.'}</p>
          <div className="mt-2 grid gap-1 text-[10px] text-zinc-500">
            <p>Desired state: <code className="text-zinc-300">{formatCode(desired?.state)}</code></p>
            <p>Desired revision: <code className="text-zinc-300">{formatRevision(desired?.revision)}</code></p>
          </div>
          <div className="mt-3 rounded-lg border border-amber-500/20 bg-amber-500/[0.06] px-3 py-2 text-[10px] text-amber-200">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <span className="flex items-center gap-2"><ShieldAlert className="size-3.5 shrink-0" /><strong>Apply state: <code>{formatCode(applyState)}</code></strong></span>
              {supportsApply && <Button type="button" size="xs" onClick={requestApply} disabled={!canApply || applyMutation.isPending} title={applyDisabledReason ?? `Apply desired profile revision ${formatRevision(desired?.revision)}`}><>{applyMutation.isPending && <Loader2 className="size-3 animate-spin" />} Apply profile</></Button>}
            </div>
            <div className="mt-2 grid gap-1 text-amber-200/75 sm:grid-cols-2">
              <p>Reason: <code>{formatCode(apply?.reason)}</code></p>
              <p>Desired revision: <code>{formatRevision(apply?.desiredRevision)}</code></p>
              <p>Task ID: <code>{formatNullable(apply?.taskId)}</code></p>
              <p>Observed revision: <code>{formatRevision(apply?.observedRevision)}</code></p>
              <p>Observed state: <code>{formatCode(apply?.observedState)}</code></p>
            </div>
            {!supportsApply && <p className="mt-2 text-amber-200/80">This legacy agent does not advertise <code>agent.profile.apply</code>; use the seven-line env fragment below and restart it manually.</p>}
            {supportsApply && applyDisabledReason && <p className="mt-2 text-amber-200/80">{applyDisabledReason}</p>}
            {applyMutation.error && errorStatus(applyMutation.error) !== 409 && <p role="alert" className="mt-2 text-red-300">{profileErrorMessage(applyMutation.error, 'apply')}</p>}
          </div>
        </div>
      </div>

      {conflict && <div role="alert" className="flex flex-col gap-2 rounded-lg border border-red-500/25 bg-red-500/[0.06] p-3 text-xs text-red-200 sm:flex-row sm:items-center sm:justify-between"><span>This profile changed elsewhere. Your edits are preserved; refresh the latest profile before saving or applying again.</span><Button type="button" variant="outline" size="xs" onClick={() => void refreshLatest()} disabled={profileQuery.isFetching}><RefreshCw className="size-3" /> Refresh latest</Button></div>}

      <DeployProfileFields
        value={draft}
        onChange={(next) => { setDraftOverride(next); setConflict(false) }}
        disabled={saveMutation.isPending}
        title="Desired deploy profile"
        description="Only the typed deploy permissions and local paths below are editable. No token or unrelated agent environment is exposed."
        idPrefix={`managed-${nodeID}-deploy-profile`}
      />

      <div className="flex flex-col gap-3 border-t border-zinc-800 pt-4 sm:flex-row sm:items-center sm:justify-between">
        <p className="flex items-start gap-2 text-[10px] leading-relaxed text-zinc-500"><CheckCircle2 className="mt-0.5 size-3.5 shrink-0 text-emerald-400" /> {supportsApply ? 'Apply is confirmed in the browser and its final state comes from the next agent heartbeat.' : <>Manual apply remains <code className="text-amber-300">manual_required</code> for legacy agents.</>}</p>
        <div className="flex flex-wrap gap-2">
          <Button type="button" variant="outline" size="xs" onClick={() => void copyFragment()} disabled={!valid}><Copy className="size-3" /> Copy env fragment</Button>
          <Button type="button" variant="outline" size="xs" onClick={downloadFragment} disabled={!valid}><Download className="size-3" /> Download fragment</Button>
          <Button type="button" size="xs" onClick={() => saveMutation.mutate()} disabled={!valid || saveMutation.isPending || !draftDirty}>{saveMutation.isPending && <Loader2 className="size-3 animate-spin" />} Save desired profile</Button>
        </div>
      </div>
    </CardContent>
  </Card>
}
