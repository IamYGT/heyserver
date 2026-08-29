import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '@/lib/api'
import Monitoring from './Monitoring'

const metricsMocks = vi.hoisted(() => ({
  history: vi.fn(),
  refetch: vi.fn(),
}))
const statsMocks = vi.hoisted(() => ({
  system: vi.fn(),
  services: vi.fn(),
  refetch: vi.fn(),
  servicesRefetch: vi.fn(),
}))
const serviceMocks = vi.hoisted(() => ({
  history: vi.fn(),
  refetch: vi.fn(),
}))

vi.mock('@/lib/api', () => ({ api: { get: vi.fn(), post: vi.fn() } }))
vi.mock('@/hooks/useAuth', () => ({ useCurrentUser: () => ({ data: { role: 'viewer' } }) }))
vi.mock('@/hooks/useStats', () => ({
  useSystemStats: statsMocks.system,
  useServiceStatuses: statsMocks.services,
}))
vi.mock('@/hooks/useMetrics', () => ({
  useMetricsHistory: metricsMocks.history,
  useProcessSnapshot: () => ({ data: undefined, isLoading: false, isFetching: false, error: null, refetch: vi.fn() }),
  useServiceHistory: serviceMocks.history,
  useMetricsSummary: () => ({ data: undefined }),
}))
vi.mock('recharts', () => {
  const Container = ({ children }: { children?: React.ReactNode }) => <div>{children}</div>
  const Empty = () => null
  return {
    AreaChart: Container, Area: Empty, LineChart: Container, Line: Empty,
    XAxis: Empty, YAxis: Empty, CartesianGrid: Empty, Tooltip: Empty,
    ResponsiveContainer: Container, ReferenceLine: Container, Label: Empty,
  }
})

function renderMonitoring() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(<MemoryRouter><QueryClientProvider client={client}><Monitoring /></QueryClientProvider></MemoryRouter>)
}

describe('Monitoring historical metrics failure state', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(api.get).mockResolvedValue([])
    statsMocks.system.mockReturnValue({ data: undefined, isError: false, error: null, isFetching: false, refetch: statsMocks.refetch })
    statsMocks.services.mockReturnValue({ data: [], isLoading: false, isError: false, error: null, isFetching: false, refetch: statsMocks.servicesRefetch })
    serviceMocks.history.mockReturnValue({ data: [], isLoading: false, isError: false, error: null, isFetching: false, refetch: serviceMocks.refetch })
    metricsMocks.history.mockReturnValue({
      data: undefined,
      isLoading: false,
      isError: true,
      error: new Error('HTTP 502: metrics history unavailable'),
      isFetching: false,
      refetch: metricsMocks.refetch,
    })
  })

  it('does not turn a failed history request into six empty collector charts', async () => {
    renderMonitoring()

    expect(await screen.findByText('Historical metrics could not be loaded')).toBeInTheDocument()
    expect(screen.getByText('HTTP 502: metrics history unavailable')).toBeInTheDocument()
    expect(screen.getByText(/No empty chart or collector state is inferred/)).toBeInTheDocument()
    expect(screen.queryByText('Collecting data...')).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Retry historical metrics' }))
    await waitFor(() => expect(metricsMocks.refetch).toHaveBeenCalledTimes(1))
  })

  it('does not silently remove live resource state when system stats fail', async () => {
    metricsMocks.history.mockReturnValue({
      data: { data: [], resolution: 'raw' },
      isLoading: false,
      isError: false,
      error: null,
      isFetching: false,
      refetch: metricsMocks.refetch,
    })
    statsMocks.system.mockReturnValue({
      data: undefined,
      isError: true,
      error: new Error('HTTP 503: live system stats unavailable'),
      isFetching: false,
      refetch: statsMocks.refetch,
    })

    renderMonitoring()

    expect(await screen.findByText('Live system stats could not be loaded')).toBeInTheDocument()
    expect(screen.getByText('HTTP 503: live system stats unavailable')).toBeInTheDocument()
    expect(screen.getByText(/no current resource state is inferred/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Retry live stats' }))
    await waitFor(() => expect(statsMocks.refetch).toHaveBeenCalledTimes(1))
  })

  it('labels a successful empty history as empty instead of loading', async () => {
    metricsMocks.history.mockReturnValue({
      data: { data: [], resolution: 'raw' },
      isLoading: false,
      isError: false,
      error: null,
      isFetching: false,
      refetch: metricsMocks.refetch,
    })

    renderMonitoring()

    expect(await screen.findAllByText('No historical samples yet')).toHaveLength(6)
    expect(screen.queryByText('Collecting data...')).not.toBeInTheDocument()
  })

  it('does not turn failed service status into an endless availability skeleton', async () => {
    metricsMocks.history.mockReturnValue({
      data: { data: [], resolution: 'raw' },
      isLoading: false,
      isError: false,
      error: null,
      isFetching: false,
      refetch: metricsMocks.refetch,
    })
    statsMocks.services.mockReturnValue({
      data: undefined,
      isLoading: false,
      isError: true,
      error: new Error('HTTP 502: service status unavailable'),
      isFetching: false,
      refetch: statsMocks.servicesRefetch,
    })

    renderMonitoring()

    expect(await screen.findByText('Service availability could not be loaded')).toBeInTheDocument()
    expect(screen.getByText('HTTP 502: service status unavailable')).toBeInTheDocument()
    expect(screen.getByText(/No uptime percentage or healthy service state is inferred/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Retry service availability' }))
    await waitFor(() => expect(statsMocks.servicesRefetch).toHaveBeenCalledTimes(1))
    expect(serviceMocks.refetch).toHaveBeenCalledTimes(1)
  })

  it('turns the pause control into a real polling boundary', async () => {
    metricsMocks.history.mockReturnValue({
      data: { data: [], resolution: 'raw' },
      isLoading: false,
      isError: false,
      error: null,
      isFetching: false,
      refetch: metricsMocks.refetch,
    })

    renderMonitoring()
    fireEvent.click(screen.getByRole('button', { name: 'Live' }))

    expect(await screen.findByRole('button', { name: 'Paused' })).toBeInTheDocument()
    await waitFor(() => {
      expect(metricsMocks.history).toHaveBeenLastCalledWith('1h', false)
      expect(statsMocks.system).toHaveBeenLastCalledWith(true, false)
      expect(serviceMocks.history).toHaveBeenLastCalledWith('1h', false)
      expect(statsMocks.services).toHaveBeenLastCalledWith(true, false)
    })
  })
})
