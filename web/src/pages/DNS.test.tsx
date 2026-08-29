import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '@/lib/api'
import DNS from './DNS'

const healthyStatus = {
  available: true,
  installed: true,
  state: 'healthy',
  active: true,
  serviceState: 'active',
  configAvailable: true,
  checkToolsAvailable: true,
  reloadAvailable: true,
  zoneManagementReady: true,
  recoveryPending: false,
}

vi.mock('@/lib/api', () => ({
  api: { get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() },
}))

function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <DNS />
    </QueryClientProvider>,
  )
}

describe('DNS inventory failures', () => {
  beforeEach(() => vi.clearAllMocks())

  it('does not present failed zone and service inventories as empty or inactive', async () => {
    vi.mocked(api.get).mockImplementation((path) => Promise.reject(new Error(`${path} unavailable`)))

    renderPage()

    expect(await screen.findByText('DNS zones could not be loaded. Zone controls are paused.')).toBeInTheDocument()
    expect(await screen.findByText('BIND status could not be loaded.')).toBeInTheDocument()
    expect(screen.getByText('BIND Unavailable')).toBeInTheDocument()
    expect(screen.queryByText('BIND Inactive')).not.toBeInTheDocument()
    expect(screen.queryByText('No DNS zones configured')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Add Zone' })).not.toBeInTheDocument()
  })

  it('shows the selected zone detail failure instead of a generic empty editor', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/dns/zones') return Promise.resolve([{ domain: 'example.com', file: '/etc/bind/example.com.zone', serial: 2026082601, records: [] }])
      if (path === '/dns/status') return Promise.resolve(healthyStatus)
      if (path === '/dns/zones/example.com') return Promise.reject(new Error('Zone detail unavailable'))
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })

    renderPage()
    fireEvent.click(await screen.findByText('example.com'))

    expect(await screen.findByText('DNS zone example.com could not be loaded.')).toBeInTheDocument()
    expect(screen.getByText('Zone detail unavailable')).toBeInTheDocument()
  })

  it('explains a missing BIND installation without presenting an empty managed inventory', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/dns/status') return Promise.resolve({
        available: false,
        installed: false,
        state: 'not-installed',
        active: false,
        serviceState: 'unknown',
        configAvailable: false,
        checkToolsAvailable: false,
        reloadAvailable: false,
        zoneManagementReady: false,
        recoveryPending: false,
        error: 'named executable was not found in PATH',
      })
      if (path === '/dns/zones') return Promise.reject(new Error('BIND zone inventory is unavailable'))
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })

    renderPage()

    expect(await screen.findByText('BIND is not installed')).toBeInTheDocument()
    expect(screen.getByText('BIND Not Installed')).toBeInTheDocument()
    expect(screen.getByText('bind9')).toBeInTheDocument()
    expect(screen.queryByText('No DNS zones configured')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Add Zone' })).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Reload BIND' })).toBeDisabled()
  })

  it('locks mutations and explains interrupted BIND lifecycle recovery', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/dns/status') return Promise.resolve({
        ...healthyStatus,
        state: 'unavailable',
        zoneManagementReady: false,
        recoveryPending: true,
        error: 'an interrupted BIND lifecycle transaction needs recovery',
      })
      if (path === '/dns/zones') return Promise.resolve([])
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })

    renderPage()

    expect(await screen.findByText('BIND transaction recovery is required')).toBeInTheDocument()
    expect(screen.getByText('BIND Recovery Required')).toBeInTheDocument()
    expect(screen.getByText(/startup recovery will roll back/i)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Add Zone' })).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Reload BIND' })).toBeDisabled()
  })

  it('maps edited records to the BIND update contract and requests a reload', async () => {
    const zone = {
      domain: 'example.com',
      file: '/etc/bind/zones/db.example.com',
      serial: 2026082601,
      records: [{ name: 'www', ttl: 3600, type: 'A', value: '192.0.2.10' }],
    }
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/dns/status') return Promise.resolve(healthyStatus)
      if (path === '/dns/zones') return Promise.resolve([zone])
      if (path === '/dns/zones/example.com') return Promise.resolve(zone)
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })
    vi.mocked(api.put).mockResolvedValue({ message: 'record updated' })

    renderPage()
    fireEvent.click(await screen.findByText('example.com'))
    fireEvent.click(await screen.findByTitle('Edit'))
    fireEvent.change(screen.getByDisplayValue('192.0.2.10'), { target: { value: '192.0.2.20' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save Changes' }))

    await waitFor(() => expect(api.put).toHaveBeenCalledWith('/dns/zones/example.com/records', {
      name: 'www',
      type: 'A',
      oldValue: '192.0.2.10',
      newValue: '192.0.2.20',
      newTtl: '3600',
      priority: undefined,
      autoReload: true,
    }))
  })
})
