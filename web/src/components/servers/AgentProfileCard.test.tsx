import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '@/lib/api'
import { AgentProfileCard, type AgentProfileResponse } from './AgentProfileCard'

vi.mock('@/lib/api', () => ({
  api: {
    get: vi.fn(),
    post: vi.fn(),
    put: vi.fn(),
  },
}))

const profileResponse: AgentProfileResponse = {
  nodeId: 'edge-1',
  desired: {
    state: 'configured',
    revision: 4,
    profile: {
      allowDeployRead: true,
      allowDeployActions: false,
      allowDeployDomainRead: false,
      allowDeployDomainActions: false,
      deployPlansFile: '/srv/plans/deploy.json',
      deployAcmeWebroot: '/srv/acme-challenges',
      deployWriteRoots: ['/srv/apps', '/var/lib/releases'],
    },
  },
  observed: {
    online: false,
    lastSeenAt: '2026-08-28T12:00:00Z',
    agentVersion: '0.1.0',
    protocolVersion: '1',
    capabilities: ['deploy.read'],
    profileState: 'not_reported',
  },
  apply: {
    state: 'manual_required',
    reason: 'self_apply_not_supported',
  },
}

function responseWith(
  observed: Partial<AgentProfileResponse['observed']> = {},
  apply: Partial<AgentProfileResponse['apply']> = {},
  desired: Partial<AgentProfileResponse['desired']> = {},
): AgentProfileResponse {
  return {
    ...profileResponse,
    desired: { ...profileResponse.desired, ...desired },
    observed: { ...profileResponse.observed, ...observed },
    apply: { ...profileResponse.apply, ...apply },
  }
}

const applyCapableResponse = responseWith(
  {
    online: true,
    capabilities: ['deploy.read', 'agent.profile.apply'],
    profileState: 'not_configured',
  },
  {
    state: 'not_requested',
    reason: 'not_requested',
    desiredRevision: 4,
    taskId: null,
    observedRevision: null,
    observedState: 'not_configured',
  },
)

function renderCard(response = profileResponse) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  vi.mocked(api.get).mockResolvedValue(response)
  return render(
    <QueryClientProvider client={client}>
      <AgentProfileCard nodeID="edge-1" />
    </QueryClientProvider>,
  )
}

describe('AgentProfileCard', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText: vi.fn().mockResolvedValue(undefined) },
    })
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('keeps desired profile separate from advertised capabilities and copies only the seven deploy keys', async () => {
    renderCard()

    expect(await screen.findByText('Managed agent deploy profile')).toBeInTheDocument()
    expect(api.get).toHaveBeenCalledWith('/nodes/edge-1/profile')
    expect(screen.getByText('Advertised')).toBeInTheDocument()
    expect(screen.getAllByText('Not advertised')).toHaveLength(3)
    expect(screen.getAllByText('not_reported').length).toBeGreaterThan(0)
    expect(screen.getByLabelText('Deploy plans file')).toHaveValue('/srv/plans/deploy.json')
    expect(screen.getAllByText('manual_required')).not.toHaveLength(0)
    expect(screen.queryByText(/Save this enrollment token/i)).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Apply profile' })).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Copy env fragment' }))
    await waitFor(() => expect(navigator.clipboard.writeText).toHaveBeenCalledTimes(1))
    const fragment = vi.mocked(navigator.clipboard.writeText).mock.calls[0]?.[0] as string
    expect(fragment.split('\n').filter(Boolean)).toHaveLength(7)
    expect(fragment).toContain('HSERVER_AGENT_ALLOW_DEPLOY_READ=true')
    expect(fragment).toContain('HSERVER_AGENT_DEPLOY_WRITE_ROOTS=/srv/apps,/var/lib/releases')
    expect(fragment).not.toMatch(/TOKEN|HUB_URL|ALLOWED_SERVICES/)
  })

  it('keeps the manual seven-line fallback for a legacy agent without apply capability', async () => {
    renderCard()

    expect(await screen.findByText(/This legacy agent does not advertise/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Apply profile' })).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Copy env fragment' })).toBeEnabled()
  })

  it('shows stale observed state while offline without deriving paths from capabilities', async () => {
    renderCard(profileResponse)

    expect(await screen.findByText(/Last observed/)).toBeInTheDocument()
    expect(screen.getByText(/values may be stale/)).toBeInTheDocument()
    expect(screen.getByText(/Deploy paths are not inferred from generic file roots/)).toBeInTheDocument()
    expect(screen.getAllByText('manual_required')).not.toHaveLength(0)
  })

  it('disables apply while the capable agent is offline', async () => {
    renderCard(responseWith({ online: false, capabilities: ['agent.profile.apply'] }, { state: 'not_requested' }))
    const offlineApply = await screen.findByRole('button', { name: 'Apply profile' })
    expect(offlineApply).toBeDisabled()
  })

  it('disables apply while the capable agent is already in flight', async () => {
    const queued = responseWith({ online: true, capabilities: ['agent.profile.apply'] }, { state: 'queued', desiredRevision: 4 })
    renderCard(queued)
    expect(await screen.findByRole('button', { name: 'Apply profile' })).toBeDisabled()
    expect(screen.getByText('queued')).toBeInTheDocument()
  })

  it('confirms the node and revision, then posts only the exact apply envelope', async () => {
    vi.mocked(api.post).mockResolvedValue({ state: 'queued' })
    const confirmMock = vi.spyOn(window, 'confirm').mockReturnValue(true)
    renderCard(applyCapableResponse)

    const applyButton = await screen.findByRole('button', { name: 'Apply profile' })
    fireEvent.click(applyButton)

    await waitFor(() => expect(api.post).toHaveBeenCalledWith('/nodes/edge-1/profile/apply', {
      expectedRevision: 4,
      confirmed: true,
    }))
    expect(confirmMock).toHaveBeenCalledWith('Apply deploy profile revision 4 to managed node edge-1?')
    expect(vi.mocked(api.post).mock.calls[0]?.[1]).not.toHaveProperty('profile')
    expect(vi.mocked(api.post).mock.calls[0]?.[1]).not.toHaveProperty('token')
    confirmMock.mockRestore()
  })

  it('polls queued state and accepts applied only from a later profile observation', async () => {
    const queued = responseWith(
      { online: true, capabilities: ['agent.profile.apply'], profileState: 'pending_restart' },
      { state: 'queued', reason: 'agent_task_queued', desiredRevision: 4, taskId: 17, observedRevision: 3, observedState: 'pending_restart' },
    )
    const applied = responseWith(
      { online: true, capabilities: ['agent.profile.apply'], profileState: 'applied', profileRevision: 4 },
      { state: 'applied', reason: 'heartbeat_confirmed', desiredRevision: 4, taskId: 17, observedRevision: 4, observedState: 'applied' },
    )
    let reads = 0
    vi.mocked(api.get).mockImplementation(async (path: string) => {
      if (path !== '/nodes/edge-1/profile') throw new Error(`Unexpected GET ${path}`)
      reads += 1
      return reads === 1 ? queued : applied
    })
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
    render(
      <QueryClientProvider client={client}>
        <AgentProfileCard nodeID="edge-1" />
      </QueryClientProvider>,
    )

    expect(await screen.findByText('queued')).toBeInTheDocument()
    expect(screen.queryByText('heartbeat_confirmed')).not.toBeInTheDocument()
    await waitFor(() => expect(screen.getAllByText('applied').length).toBeGreaterThan(0), { timeout: 3_500 })
    expect(reads).toBeGreaterThanOrEqual(2)
    expect(screen.getByText('heartbeat_confirmed')).toBeInTheDocument()
    expect(screen.getAllByText('4').length).toBeGreaterThan(0)
  })

  it('continues polling while an offline agent awaits its heartbeat', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    const awaiting = responseWith(
      { online: false, capabilities: ['agent.profile.apply'], profileState: 'pending_restart' },
      { state: 'awaiting_heartbeat', reason: 'agent_task_queued', desiredRevision: 4 },
    )
    renderCard(awaiting)

    expect(await screen.findByText('awaiting_heartbeat')).toBeInTheDocument()
    expect(api.get).toHaveBeenCalledTimes(1)

    await act(async () => {
      await vi.advanceTimersByTimeAsync(2_000)
    })

    expect(api.get).toHaveBeenCalledTimes(2)
  })

  it.each(['applied', 'failed', 'drifted', 'manual_required', 'not_requested'] as const)('stops polling for terminal %s state', async (state) => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    renderCard(responseWith({ online: false }, { state }))

    expect((await screen.findAllByText(state)).length).toBeGreaterThan(0)
    expect(api.get).toHaveBeenCalledTimes(1)

    await act(async () => {
      await vi.advanceTimersByTimeAsync(4_000)
    })

    expect(api.get).toHaveBeenCalledTimes(1)
  })

  it('does not render a raw upstream error body', async () => {
    const upstreamBody = '<html><body>proxy failure /api/nodes/edge-1/profile secret-output</body></html>'
    vi.mocked(api.get).mockRejectedValue(new Error(upstreamBody))
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
    render(
      <QueryClientProvider client={client}>
        <AgentProfileCard nodeID="edge-1" />
      </QueryClientProvider>,
    )

    expect(await screen.findByText('Could not load managed agent profile')).toBeInTheDocument()
    expect(screen.queryByText(upstreamBody)).not.toBeInTheDocument()
    expect(screen.queryByText(/proxy failure|secret-output|\/api\/nodes\/edge-1\/profile/)).not.toBeInTheDocument()
    expect(screen.getByText('Could not load the managed agent profile.')).toBeInTheDocument()
  })

  it.each(['failed', 'drifted'] as const)('renders %s as a terminal honest apply state', async (state) => {
    const response = responseWith(
      { online: true, capabilities: ['agent.profile.apply'], profileState: state === 'failed' ? 'failed' : 'pending_restart', profileRevision: 3, profileErrorCode: state === 'failed' ? 'profile_apply_failed' : 'profile_revision_drifted' },
      { state, reason: state === 'failed' ? 'agent_rejected_profile' : 'observed_revision_mismatch', desiredRevision: 4, taskId: 18, observedRevision: 3, observedState: state === 'failed' ? 'failed' : 'pending_restart' },
    )
    renderCard(response)

    expect((await screen.findAllByText(state)).length).toBeGreaterThan(0)
    expect(screen.getByText(state === 'failed' ? 'profile_apply_failed' : 'profile_revision_drifted')).toBeInTheDocument()
    expect(screen.getAllByText('18').length).toBeGreaterThan(0)
    expect(screen.getAllByText('3').length).toBeGreaterThan(0)
  })

  it('does not create duplicate apply requests from repeated clicks', async () => {
    let resolvePost: (value: unknown) => void = () => undefined
    vi.mocked(api.post).mockReturnValue(new Promise((resolve) => { resolvePost = resolve }))
    const confirmMock = vi.spyOn(window, 'confirm').mockReturnValue(true)
    renderCard(applyCapableResponse)

    const applyButton = await screen.findByRole('button', { name: 'Apply profile' })
    fireEvent.click(applyButton)
    fireEvent.click(applyButton)

    await waitFor(() => expect(api.post).toHaveBeenCalledTimes(1))
    expect(confirmMock).toHaveBeenCalledTimes(1)
    resolvePost({ state: 'queued' })
    confirmMock.mockRestore()
  })

  it('preserves the editor after an apply revision conflict and offers refresh', async () => {
    vi.mocked(api.post).mockRejectedValue(Object.assign(new Error('stale_profile_revision'), { status: 409 }))
    const confirmMock = vi.spyOn(window, 'confirm').mockReturnValue(true)
    renderCard(applyCapableResponse)

    const plans = await screen.findByLabelText('Deploy plans file')
    fireEvent.change(plans, { target: { value: '/srv/plans/new.json' } })
    fireEvent.click(await screen.findByRole('button', { name: 'Apply profile' }))

    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('Your edits are preserved'))
    expect(plans).toHaveValue('/srv/plans/new.json')
    expect(screen.getByRole('button', { name: 'Refresh latest' })).toBeInTheDocument()
    confirmMock.mockRestore()
  })

  it('preserves edits after a desired-profile 409 and offers an explicit latest-profile refresh', async () => {
    vi.mocked(api.put).mockRejectedValue(Object.assign(new Error('profile revision conflict'), { status: 409 }))
    renderCard()

    const plans = await screen.findByLabelText('Deploy plans file')
    fireEvent.change(plans, { target: { value: '/srv/plans/new.json' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save desired profile' }))

    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('Your edits are preserved'))
    expect(plans).toHaveValue('/srv/plans/new.json')
    expect(screen.getByRole('button', { name: 'Refresh latest' })).toBeInTheDocument()
    expect(api.put).toHaveBeenCalledWith('/nodes/edge-1/profile', {
      profile: {
        allowDeployRead: true,
        allowDeployActions: false,
        allowDeployDomainRead: false,
        allowDeployDomainActions: false,
        deployPlansFile: '/srv/plans/new.json',
        deployAcmeWebroot: '/srv/acme-challenges',
        deployWriteRoots: ['/srv/apps', '/var/lib/releases'],
      },
      expectedRevision: 4,
    })
  })
})
