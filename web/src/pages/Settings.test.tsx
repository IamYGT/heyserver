import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '@/lib/api'
import SettingsPage from './Settings'

vi.mock('@/lib/api', () => ({
  api: {
    get: vi.fn(),
    post: vi.fn(),
    put: vi.fn(),
  },
}))

vi.mock('@/components/settings/PortableConfigurationSection', () => ({
  PortableConfigurationSection: () => null,
}))

function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <SettingsPage />
    </QueryClientProvider>,
  )
}

const settings = {
  hostnameDisplay: 'Primary panel',
  adminEmail: 'admin@example.com',
}

const info = {
  os: 'Ubuntu 24.04',
  kernel: '6.8.0',
  nginx: '1.26.0',
  php: ['8.4'],
  postgresql: '17',
  hostname: 'panel.example.com',
  arch: 'amd64',
  panel_version: 'v0.9.0',
  build_commit: 'abc1234',
  build_date: '2026-08-27T00:00:00Z',
}

const health = {
  uptime: 3600,
  cpu: { usage: 12, cores: 4, model: 'Example CPU' },
  memory: { total: 8 * 1024 ** 3, used: 2 * 1024 ** 3, free: 6 * 1024 ** 3, percentage: 25 },
  disk: [],
  hostname: 'panel.example.com',
  os: 'Ubuntu 24.04',
}

function respond(path: string) {
  if (path === '/settings') return Promise.resolve(settings)
  if (path === '/system/info') return Promise.resolve(info)
  if (path === '/system/stats') return Promise.resolve(health)
  if (path === '/auth/2fa/status') return Promise.resolve({ enabled: false, setup_pending: false })
  return Promise.reject(new Error(`Unexpected GET ${path}`))
}

describe('Settings page failure states', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(api.get).mockImplementation(respond)
  })

  it('keeps editable settings and server health available when system information fails', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/system/info') return Promise.reject(new Error('System info API unavailable'))
      return respond(path)
    })

    renderPage()

    expect(await screen.findByDisplayValue('Primary panel')).toBeInTheDocument()
    expect(await screen.findByText('System information could not be loaded')).toBeInTheDocument()
    expect(screen.getByText('Panel build information could not be loaded')).toBeInTheDocument()
    expect(screen.getAllByText('System info API unavailable')).toHaveLength(2)
    expect(screen.getByText('8.0 GB')).toBeInTheDocument()
    expect(screen.queryByText('Settings could not be loaded')).not.toBeInTheDocument()
  })

  it('does not turn unavailable live health into placeholder metrics', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/system/stats') return Promise.reject(new Error('Live stats API unavailable'))
      return respond(path)
    })

    renderPage()

    expect(await screen.findByDisplayValue('Primary panel')).toBeInTheDocument()
    expect(await screen.findByText('Server health could not be loaded')).toBeInTheDocument()
    expect(screen.getByText('Live stats API unavailable')).toBeInTheDocument()
    expect(screen.queryByText('Total RAM')).not.toBeInTheDocument()
    expect(screen.getByText('Ubuntu 24.04')).toBeInTheDocument()
  })

  it('does not substitute editable defaults when settings inventory fails', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/settings') return Promise.reject(new Error('Settings API unavailable'))
      return respond(path)
    })

    renderPage()

    expect(await screen.findByText('Settings could not be loaded')).toBeInTheDocument()
    expect(screen.getByText(/Settings API unavailable/)).toBeInTheDocument()
    expect(screen.queryByPlaceholderText('My Server')).not.toBeInTheDocument()
  })
})
