import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useCurrentUser } from '@/hooks/useAuth'
import { useHostActionStatus } from '@/hooks/useHostActionStatus'
import { useMetricsHistory } from '@/hooks/useMetrics'
import { useServiceStatuses, useSystemStats } from '@/hooks/useStats'
import { api } from '@/lib/api'
import Dashboard from './Dashboard'

vi.mock('@/hooks/useAuth', () => ({ useCurrentUser: vi.fn() }))
vi.mock('@/hooks/useHostActionStatus', () => ({
  hostActionStatusKey: vi.fn(() => ['system-actions', 'status', 'local']),
  useHostActionStatus: vi.fn(),
}))
vi.mock('@/hooks/useMetrics', () => ({ useMetricsHistory: vi.fn() }))
vi.mock('@/hooks/useStats', () => ({ useSystemStats: vi.fn(), useServiceStatuses: vi.fn() }))
vi.mock('@/lib/api', () => ({ api: { get: vi.fn(), post: vi.fn() } }))

function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <MemoryRouter>
      <QueryClientProvider client={client}>
        <Dashboard />
      </QueryClientProvider>
    </MemoryRouter>,
  )
}

describe('dashboard primary inventory failures', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(useCurrentUser).mockReturnValue({ data: { role: 'admin' } } as ReturnType<typeof useCurrentUser>)
    vi.mocked(useHostActionStatus).mockReturnValue({
      data: { running: false },
      isLoading: false,
      isError: false,
      isFetching: false,
      refetch: vi.fn(),
    } as unknown as ReturnType<typeof useHostActionStatus>)
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/ssl/certificates' || path === '/pm2/processes' || path === '/mail/queue') return Promise.resolve([])
      if (path === '/mail/service/overview') return Promise.resolve({ status: { running: true, status: 'active' }, sources: { status: { available: true } } })
      if (path === '/uptime/monitors/summary') return Promise.resolve({ total: 0, up: 0, down: 0, paused: 0 })
      if (path === '/audit?limit=5') return Promise.resolve({ data: [] })
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })
  })

  it('shows retryable failures instead of hiding gauges or presenting an empty service inventory', async () => {
    const refetchStats = vi.fn()
    const refetchServices = vi.fn()
    const refetchMetrics = vi.fn()
    vi.mocked(useSystemStats).mockReturnValue({
      data: undefined,
      isLoading: false,
      isError: true,
      error: new Error('System stats API unavailable'),
      isFetching: false,
      refetch: refetchStats,
    } as unknown as ReturnType<typeof useSystemStats>)
    vi.mocked(useServiceStatuses).mockReturnValue({
      data: undefined,
      isLoading: false,
      isError: true,
      error: new Error('Service inventory API unavailable'),
      isFetching: false,
      refetch: refetchServices,
    } as unknown as ReturnType<typeof useServiceStatuses>)
    vi.mocked(useMetricsHistory).mockReturnValue({
      data: undefined,
      isLoading: false,
      isError: true,
      error: new Error('Metric history API unavailable'),
      isFetching: false,
      refetch: refetchMetrics,
    } as unknown as ReturnType<typeof useMetricsHistory>)

    renderPage()

    expect(await screen.findByText('Live system statistics could not be loaded.')).toBeInTheDocument()
    expect(screen.getByText('System stats API unavailable')).toBeInTheDocument()
    expect(await screen.findByText('Service inventory could not be loaded. Service controls are paused.')).toBeInTheDocument()
    expect(screen.getByText('Service inventory API unavailable')).toBeInTheDocument()
    expect(screen.getByText('Historical dashboard trends could not be loaded. Live gauges remain available.')).toBeInTheDocument()
    expect(screen.getByText('Metric history API unavailable')).toBeInTheDocument()
    expect(screen.queryByText('No service data available')).not.toBeInTheDocument()
    expect(screen.queryByText('No monitored services were returned.')).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Retry statistics' }))
    fireEvent.click(screen.getByRole('button', { name: 'Retry services' }))
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(refetchStats).toHaveBeenCalledOnce()
    expect(refetchServices).toHaveBeenCalledOnce()
    expect(refetchMetrics).toHaveBeenCalledOnce()
  })
})
