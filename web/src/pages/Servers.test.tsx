import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useCurrentUser } from '@/hooks/useAuth'
import { api } from '@/lib/api'
import Servers from './Servers'

vi.mock('@/lib/api', () => ({
  api: {
    get: vi.fn(),
    post: vi.fn(),
    put: vi.fn(),
    delete: vi.fn(),
  },
}))

vi.mock('@/hooks/useAuth', () => ({ useCurrentUser: vi.fn() }))

const cachedNode = {
  id: 'edge-1',
  name: 'Edge One',
  hostname: 'edge.example.com',
  agent_version: '0.1.0',
  protocol_version: '1',
  capabilities: ['terminal', 'service.action', 'logs.read'],
  last_seen_at: new Date().toISOString(),
  online: true,
  inventory: {
    os: 'Ubuntu 24.04',
    arch: 'amd64',
    kernel: '6.8.0',
    boot_id: 'boot-1',
    uptime_seconds: 3600,
    load_1: 0.2,
    memory_total_bytes: 8 * 1024 ** 3,
    memory_available_bytes: 4 * 1024 ** 3,
    disk_total_bytes: 100 * 1024 ** 3,
    disk_used_bytes: 40 * 1024 ** 3,
    disk_available_bytes: 60 * 1024 ** 3,
    disk_use_percent: 40,
    plesk_present: false,
    services: [{ name: 'nginx.service', active: 'active', sub: 'running' }],
    log_sources: ['nginx'],
  },
}

const adminProfileResponse = {
  nodeId: 'edge-1',
  desired: {
    state: 'not_configured',
    revision: 0,
    profile: {
      allowDeployRead: false,
      allowDeployActions: false,
      allowDeployDomainRead: false,
      allowDeployDomainActions: false,
      deployPlansFile: '',
      deployAcmeWebroot: '',
      deployWriteRoots: [],
    },
  },
  observed: {
    online: true,
    lastSeenAt: cachedNode.last_seen_at,
    agentVersion: '0.1.0',
    protocolVersion: '1',
    capabilities: cachedNode.capabilities,
    profileState: 'not_reported',
  },
  apply: { state: 'manual_required', reason: 'self_apply_not_supported' },
} as const

describe('Managed server fleet refresh boundary', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(useCurrentUser).mockReturnValue({ data: undefined } as ReturnType<typeof useCurrentUser>)
    vi.mocked(api.get).mockImplementation(async (path: string) => {
      if (path === '/nodes') throw new Error('Fleet API unavailable')
      if (path === '/nodes/edge-1/tasks?limit=12') return []
      throw new Error(`Unexpected GET ${path}`)
    })
  })

  it('keeps cached inventory readable but pauses every visible remote mutation', async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    client.setQueryData(['managed-nodes'], [cachedNode])

    render(
      <MemoryRouter initialEntries={['/servers?node=edge-1&tab=services']}>
        <QueryClientProvider client={client}>
          <Servers />
        </QueryClientProvider>
      </MemoryRouter>,
    )

    expect(await screen.findByText('Managed server status refresh failed')).toBeInTheDocument()
    expect(screen.getByText('Fleet API unavailable')).toBeInTheDocument()
    expect(screen.getByText('Cached inventory remains visible, but all remote mutations are paused until refresh succeeds.')).toBeInTheDocument()
    expect(screen.getByText('edge.example.com')).toBeInTheDocument()
    expect(screen.getByText('nginx.service')).toBeInTheDocument()
    expect(screen.queryByText('Edge One connection lost')).not.toBeInTheDocument()

    expect(screen.getByRole('button', { name: 'Open Edge One terminal' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Restart' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Stop' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'View nginx.service logs' })).toBeEnabled()
    expect(screen.getByRole('button', { name: 'Retry fleet refresh' })).toBeEnabled()

    fireEvent.click(screen.getByRole('button', { name: 'Restart' }))
    expect(api.post).not.toHaveBeenCalled()
  })
})

describe('Managed server service confirmation boundary', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(useCurrentUser).mockReturnValue({ data: undefined } as ReturnType<typeof useCurrentUser>)
    vi.mocked(api.get).mockImplementation(async (path: string) => {
      if (path === '/nodes') return [{
        ...cachedNode,
        inventory: {
          ...cachedNode.inventory,
          services: [{ name: 'nginx.service', active: 'inactive', sub: 'dead' }],
        },
      }]
      if (path === '/nodes/edge-1/tasks?limit=12') return []
      if (path === '/nodes/edge-1/actions/status') return { running: false }
      throw new Error(`Unexpected GET ${path}`)
    })
    vi.mocked(api.post).mockResolvedValue({
      id: 41,
      node_id: 'edge-1',
      kind: 'service.action',
      payload: { service: 'nginx.service', action: 'start' },
      status: 'completed',
      result: { active: 'active', sub: 'running' },
    })
  })

  it('requires confirmation for service start and sends the universal confirmation marker', async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const confirmMock = vi.spyOn(window, 'confirm').mockReturnValue(false)

    render(
      <MemoryRouter initialEntries={['/servers?node=edge-1&tab=services']}>
        <QueryClientProvider client={client}>
          <Servers />
        </QueryClientProvider>
      </MemoryRouter>,
    )

    const start = await screen.findByRole('button', { name: 'Start' })
    fireEvent.click(start)
    expect(confirmMock).toHaveBeenCalledWith('Start nginx.service on managed node Edge One?')
    expect(api.post).not.toHaveBeenCalled()

    confirmMock.mockReturnValue(true)
    fireEvent.click(start)
    await waitFor(() => expect(api.post).toHaveBeenCalledWith('/nodes/edge-1/tasks', {
      kind: 'service.action',
      payload: { service: 'nginx.service', action: 'start' },
      confirmed: true,
    }))
    confirmMock.mockRestore()
  })
})

describe('Managed server deploy profile access boundary', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders the selected managed-node profile editor for Admin users', async () => {
    vi.mocked(useCurrentUser).mockReturnValue({ data: { role: 'admin' } } as ReturnType<typeof useCurrentUser>)
    vi.mocked(api.get).mockImplementation(async (path: string) => {
      if (path === '/nodes') return [cachedNode]
      if (path === '/nodes/edge-1/profile') return adminProfileResponse
      throw new Error(`Unexpected GET ${path}`)
    })

    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <MemoryRouter initialEntries={['/servers?node=edge-1']}>
        <QueryClientProvider client={client}>
          <Servers />
        </QueryClientProvider>
      </MemoryRouter>,
    )

    expect(await screen.findByText('Managed agent deploy profile')).toBeInTheDocument()
    expect(api.get).toHaveBeenCalledWith('/nodes/edge-1/profile')
  })
})

const metricsNode = {
  ...cachedNode,
  capabilities: [...cachedNode.capabilities, 'metrics.read'],
}

const healthyMetrics = {
  observed_at: new Date().toISOString(),
  cpu: { usage_percent: 23.5, core_count: 4 },
  load: { one: 0.42, five: 0.31, fifteen: 0.2 },
  memory: {
    total_bytes: 8 * 1024 ** 3,
    used_bytes: 3 * 1024 ** 3,
    available_bytes: 5 * 1024 ** 3,
    usage_percent: 37.5,
  },
  network: { rx_bytes: 2 * 1024 ** 3, tx_bytes: 512 * 1024 ** 2 },
  root_disk: {
    total_bytes: 100 * 1024 ** 3,
    used_bytes: 40 * 1024 ** 3,
    available_bytes: 60 * 1024 ** 3,
    usage_percent: 40,
  },
}

const remoteMemory = {
  memory_total_bytes: 8 * 1024 ** 3,
  memory_available_bytes: 5 * 1024 ** 3,
  swap_total_bytes: 0,
  swap_used_bytes: 0,
  swap_free_bytes: 0,
  swap_reset_eligible: true,
}

function renderManagedOverview() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <MemoryRouter initialEntries={['/servers?node=edge-1']}>
      <QueryClientProvider client={client}>
        <Servers />
      </QueryClientProvider>
    </MemoryRouter>,
  )
}

describe('Managed server current metrics boundary', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(useCurrentUser).mockReturnValue({ data: undefined } as ReturnType<typeof useCurrentUser>)
  })

  it('renders healthy current CPU, cores, network, root disk, and load values', async () => {
    vi.mocked(api.get).mockImplementation(async (path: string) => {
      if (path === '/nodes') return [metricsNode]
      if (path === '/nodes/edge-1/memory') return remoteMemory
      if (path === '/nodes/edge-1/metrics') return healthyMetrics
      throw new Error(`Unexpected GET ${path}`)
    })

    renderManagedOverview()

    await waitFor(() => expect(screen.getByTestId('managed-node-metrics-status')).toHaveTextContent('Healthy'))
    expect(screen.getByTestId('managed-node-metrics-cpu')).toHaveTextContent('23.5%')
    expect(screen.getByTestId('managed-node-metrics-cpu')).toHaveTextContent('4 cores')
    expect(screen.getByTestId('managed-node-metrics-load')).toHaveTextContent('0.42')
    expect(screen.getByTestId('managed-node-metrics-network')).toHaveTextContent('RX 2.0 GiB')
    expect(screen.getByTestId('managed-node-metrics-network')).toHaveTextContent('TX 512 MiB')
    expect(screen.getByTestId('managed-node-metrics-root-disk')).toHaveTextContent('40.0%')
    expect(api.get).toHaveBeenCalledWith('/nodes/edge-1/metrics')
  })

  it('renders unsupported or offline state for a 409 without exposing the backend body', async () => {
    const backendError = Object.assign(new Error('raw backend metrics body'), { status: 409 })
    vi.mocked(api.get).mockImplementation(async (path: string) => {
      if (path === '/nodes') return [metricsNode]
      if (path === '/nodes/edge-1/memory') return remoteMemory
      if (path === '/nodes/edge-1/metrics') throw backendError
      throw new Error(`Unexpected GET ${path}`)
    })

    renderManagedOverview()

    await waitFor(() => expect(screen.getByTestId('managed-node-metrics-status')).toHaveTextContent('Unsupported / offline'))
    expect(screen.getByText('Current metrics are not supported by this agent or the agent is offline.')).toBeInTheDocument()
    expect(screen.queryByText('raw backend metrics body')).not.toBeInTheDocument()
  })

  it('renders unavailable state for a timed-out gateway response without raw backend text', async () => {
    const backendError = Object.assign(new Error('raw gateway timeout body'), { status: 504 })
    vi.mocked(api.get).mockImplementation(async (path: string) => {
      if (path === '/nodes') return [metricsNode]
      if (path === '/nodes/edge-1/memory') return remoteMemory
      if (path === '/nodes/edge-1/metrics') throw backendError
      throw new Error(`Unexpected GET ${path}`)
    })

    renderManagedOverview()

    await waitFor(() => expect(screen.getByTestId('managed-node-metrics-status')).toHaveTextContent('Unavailable'))
    expect(screen.getByText('Current metrics are temporarily unavailable or the request timed out. Retry to check again.')).toBeInTheDocument()
    expect(screen.queryByText('raw gateway timeout body')).not.toBeInTheDocument()
  })

  it('marks an old observation stale while retaining the observed values', async () => {
    const staleMetrics = {
      ...healthyMetrics,
      observed_at: new Date(Date.now() - 5 * 60_000).toISOString(),
    }
    vi.mocked(api.get).mockImplementation(async (path: string) => {
      if (path === '/nodes') return [metricsNode]
      if (path === '/nodes/edge-1/memory') return remoteMemory
      if (path === '/nodes/edge-1/metrics') return staleMetrics
      throw new Error(`Unexpected GET ${path}`)
    })

    renderManagedOverview()

    await waitFor(() => expect(screen.getByTestId('managed-node-metrics-status')).toHaveTextContent('Stale'))
    expect(screen.getByText(/Observation is stale/)).toBeInTheDocument()
    expect(screen.getByTestId('managed-node-metrics-cpu')).toHaveTextContent('23.5%')
  })

  it('resets current metrics while switching nodes instead of showing the previous node', async () => {
    const secondNode = {
      ...metricsNode,
      id: 'edge-2',
      name: 'Edge Two',
      hostname: 'edge-two.example.com',
    }
    const secondMetrics = {
      ...healthyMetrics,
      cpu: { usage_percent: 67.2, core_count: 8 },
    }
    vi.mocked(api.get).mockImplementation(async (path: string) => {
      if (path === '/nodes') return [metricsNode, secondNode]
      if (path === '/nodes/edge-1/memory' || path === '/nodes/edge-2/memory') return remoteMemory
      if (path === '/nodes/edge-1/metrics') return healthyMetrics
      if (path === '/nodes/edge-2/metrics') return secondMetrics
      throw new Error(`Unexpected GET ${path}`)
    })

    renderManagedOverview()
    expect(await screen.findByTestId('managed-node-metrics-cpu')).toHaveTextContent('23.5%')

    fireEvent.click(screen.getByRole('button', { name: /Edge Two/ }))
    await waitFor(() => expect(screen.queryByText('23.5%')).not.toBeInTheDocument())
    expect(screen.queryByTestId('managed-node-metrics-cpu')).not.toBeInTheDocument()
    expect(await screen.findByTestId('managed-node-metrics-cpu')).toHaveTextContent('67.2%')
    expect(screen.getByTestId('managed-node-metrics-cpu')).toHaveTextContent('8 cores')
    expect(api.get).toHaveBeenCalledWith('/nodes/edge-2/metrics')
  })
})
