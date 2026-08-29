import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '@/lib/api'
import Security from './Security'

const mocks = vi.hoisted(() => ({
  auth: {
    data: { id: 1, name: 'Admin', email: 'admin@example.test', role: 'admin' } as { id: number; name: string; email: string; role: string } | undefined,
    isLoading: false,
    isError: false,
    isFetching: false,
    error: null as Error | null,
  },
  refetchIdentity: vi.fn(),
  toastError: vi.fn(),
}))

vi.mock('@/lib/api', () => ({
  api: { get: vi.fn(), post: vi.fn(), delete: vi.fn() },
}))

vi.mock('@/hooks/useAuth', () => ({
  useCurrentUser: () => ({ ...mocks.auth, refetch: mocks.refetchIdentity }),
}))

vi.mock('sonner', () => ({
  toast: { error: mocks.toastError, success: vi.fn() },
}))

function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <Security />
    </QueryClientProvider>,
  )
}

describe('security inventory contracts', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    Object.assign(mocks.auth, {
      data: { id: 1, name: 'Admin', email: 'admin@example.test', role: 'admin' },
      isLoading: false,
      isError: false,
      isFetching: false,
      error: null,
    })
  })

  it('opens security mutation controls only for a verified admin identity', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/security/score') return Promise.resolve({ score: 100, maxScore: 100, checks: [] })
      if (path === '/security/fail2ban/status') return Promise.resolve({
        available: true,
        installed: true,
        running: true,
        state: 'healthy',
        daemonState: 'active',
        jails: [],
      })
      if (path === '/security/ip-blacklist' || path === '/security/ip-whitelist') return Promise.resolve([])
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })

    renderPage()

    expect(await screen.findByRole('button', { name: 'Ban IP' })).toBeEnabled()
    expect(screen.getByRole('button', { name: 'Unban IP' })).toBeEnabled()
    expect(screen.getAllByRole('button', { name: 'Add IP' })).toHaveLength(2)
  })

  it('keeps admin-only inventory and mutation controls hidden from non-admin roles', async () => {
    mocks.auth.data = { id: 2, name: 'Manager', email: 'manager@example.test', role: 'manager' }
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/security/score') return Promise.resolve({ score: 80, maxScore: 100, checks: [] })
      return Promise.reject(new Error(`Unexpected admin inventory request ${path}`))
    })

    renderPage()

    expect(await screen.findByText('Admin access is required for security administration.')).toBeInTheDocument()
    expect(await screen.findByText('80')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Ban IP' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Add IP' })).not.toBeInTheDocument()
    expect(api.get).toHaveBeenCalledTimes(1)
    expect(api.get).toHaveBeenCalledWith('/security/score')
  })

  it('distinguishes identity loading from failure and fails closed in both states', async () => {
    mocks.auth.data = undefined
    mocks.auth.isLoading = true
    vi.mocked(api.get).mockResolvedValue({ score: 75, maxScore: 100, checks: [] })

    const rendered = renderPage()

    expect(await screen.findByText('Verifying security permissions…')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Ban IP' })).not.toBeInTheDocument()

    mocks.auth.isLoading = false
    mocks.auth.isError = true
    mocks.auth.error = new Error('raw identity provider detail')
    rendered.rerender(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <Security />
      </QueryClientProvider>,
    )

    expect(await screen.findByText('Security permissions could not be verified.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Retry permission' })).toBeEnabled()
    expect(screen.queryByText('raw identity provider detail')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Add IP' })).not.toBeInTheDocument()
  })

  it('shows a stable permission message for a rejected security mutation without leaking the raw error', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/security/score') return Promise.resolve({ score: 100, maxScore: 100, checks: [] })
      if (path === '/security/fail2ban/status') return Promise.resolve({
        available: true,
        installed: true,
        running: true,
        state: 'healthy',
        daemonState: 'active',
        jails: [],
      })
      if (path === '/security/ip-blacklist') {
        return Promise.resolve([{ ip: '198.51.100.20', listType: 'blacklist', comment: 'blocked', createdAt: '2026-08-26T12:00:00Z' }])
      }
      if (path === '/security/ip-whitelist') return Promise.resolve([])
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })
    vi.mocked(api.delete).mockRejectedValue(Object.assign(new Error('raw policy backend detail'), { status: 403 }))

    renderPage()

    fireEvent.click(await screen.findByRole('button', { name: 'Remove from blacklist' }))

    await waitFor(() => expect(mocks.toastError).toHaveBeenCalledWith(
      'Permission denied. Admin access is required for security changes.',
    ))
    expect(screen.queryByText('raw policy backend detail')).not.toBeInTheDocument()
  })

  it('distinguishes unavailable security inventories from stopped or empty states', async () => {
    vi.mocked(api.get).mockImplementation((path) => Promise.reject(new Error(`${path} unavailable`)))

    renderPage()

    expect(await screen.findByText('Security score could not be loaded.')).toBeInTheDocument()
    expect(await screen.findByText('Fail2Ban status could not be loaded. IP ban controls are paused.')).toBeInTheDocument()
    expect(await screen.findByText('IP blacklist could not be loaded. Blacklist controls are paused.')).toBeInTheDocument()
    expect(await screen.findByText('IP whitelist could not be loaded. Whitelist controls are paused.')).toBeInTheDocument()
    expect(screen.queryByText('Stopped')).not.toBeInTheDocument()
    expect(screen.queryByText('No jails found')).not.toBeInTheDocument()
    expect(screen.queryByText('No blacklisted IPs')).not.toBeInTheDocument()
    expect(screen.queryByText('No whitelisted IPs')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Ban IP' })).not.toBeInTheDocument()
  })

  it('keeps whitelist controls available when blacklist inventory fails', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/security/score') return Promise.resolve({ score: 100, maxScore: 100, checks: [] })
      if (path === '/security/fail2ban/status') return Promise.resolve({
        available: true,
        installed: true,
        running: true,
        state: 'healthy',
        daemonState: 'active',
        jails: [],
      })
      if (path === '/security/ip-blacklist') return Promise.reject(new Error('Blacklist API unavailable'))
      if (path === '/security/ip-whitelist') {
        return Promise.resolve([{ ip: '203.0.113.10', listType: 'whitelist', comment: 'office', createdAt: '2026-08-26T12:00:00Z' }])
      }
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })
    vi.mocked(api.delete).mockResolvedValue(undefined)

    renderPage()

    expect(await screen.findByText('IP blacklist could not be loaded. Blacklist controls are paused.')).toBeInTheDocument()
    expect(await screen.findByText('203.0.113.10')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Remove from whitelist' }))

    await waitFor(() => expect(api.delete).toHaveBeenCalledWith('/security/ip-whitelist/203.0.113.10'))
  })

  it('keeps blacklist controls available when whitelist inventory fails', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/security/score') return Promise.resolve({ score: 100, maxScore: 100, checks: [] })
      if (path === '/security/fail2ban/status') return Promise.resolve({
        available: true,
        installed: true,
        running: true,
        state: 'healthy',
        daemonState: 'active',
        jails: [],
      })
      if (path === '/security/ip-blacklist') {
        return Promise.resolve([{ ip: '198.51.100.20', listType: 'blacklist', comment: 'blocked', createdAt: '2026-08-26T12:00:00Z' }])
      }
      if (path === '/security/ip-whitelist') return Promise.reject(new Error('Whitelist API unavailable'))
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })
    vi.mocked(api.delete).mockResolvedValue(undefined)

    renderPage()

    expect(await screen.findByText('IP whitelist could not be loaded. Whitelist controls are paused.')).toBeInTheDocument()
    expect(await screen.findByText('198.51.100.20')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Remove from blacklist' }))

    await waitFor(() => expect(api.delete).toHaveBeenCalledWith('/security/ip-blacklist/198.51.100.20'))
  })

  it.each([
    {
      state: 'not-installed',
      installed: false,
      daemonState: 'unknown',
      error: 'fail2ban-client is not installed',
      title: 'Fail2Ban is not installed',
    },
    {
      state: 'stopped',
      installed: true,
      daemonState: 'inactive',
      error: 'fail2ban service is inactive',
      title: 'Fail2Ban is installed but stopped',
    },
  ])('shows $state remediation without presenting a false empty jail inventory', async (fail2ban) => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/security/score') return Promise.resolve({ score: 60, maxScore: 100, checks: [] })
      if (path === '/security/fail2ban/status') return Promise.resolve({
        available: false,
        installed: fail2ban.installed,
        running: false,
        state: fail2ban.state,
        daemonState: fail2ban.daemonState,
        error: fail2ban.error,
        jails: [],
      })
      if (path === '/security/ip-blacklist' || path === '/security/ip-whitelist') return Promise.resolve([])
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })

    renderPage()

    expect(await screen.findByText(fail2ban.title)).toBeInTheDocument()
    expect(screen.queryByText('No jails found')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Ban IP' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Unban IP' })).not.toBeInTheDocument()
  })
})
