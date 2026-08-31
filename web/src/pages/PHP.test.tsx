import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '@/lib/api'
import PHP from './PHP'

vi.mock('@/lib/api', () => ({
  api: { get: vi.fn(), post: vi.fn() },
}))

vi.mock('./php/PoolsTab', () => ({ default: () => <div>Pool controls</div> }))
vi.mock('./php/IniTab', () => ({ default: () => <div>INI controls</div> }))
vi.mock('./php/ExtensionsTab', () => ({ default: () => <div>Extension controls</div> }))
vi.mock('./php/MonitoringTab', () => ({ default: () => <div>Monitoring controls</div> }))
vi.mock('./php/LogsTab', () => ({ default: () => <div>Log controls</div> }))
vi.mock('./php/SecurityTab', () => ({ default: () => <div>Security controls</div> }))

function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <PHP />
    </QueryClientProvider>,
  )
}

describe('PHP page inventory truthfulness', () => {
  beforeEach(() => vi.clearAllMocks())

  it('keeps version and pool failures distinct from empty inventories', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/php/versions') return Promise.reject(new Error('PHP versions API unavailable'))
      if (path === '/php/pools') return Promise.reject(new Error('PHP pools API unavailable'))
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })

    renderPage()

    expect(await screen.findByText('PHP-FPM inventory is unavailable')).toBeInTheDocument()
    expect(screen.getByText('PHP versions API unavailable')).toBeInTheDocument()
    expect(screen.getByText('/etc/php/*/fpm')).toBeInTheDocument()
    expect(screen.queryByText('No PHP-FPM versions detected')).not.toBeInTheDocument()
  })

  it('keeps a pool failure visible while using version-only tabs', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/php/versions') return Promise.resolve([{ version: '8.4', active: true, info: '', pool_dir: '', pool_count: 0 }])
      if (path === '/php/pools') return Promise.reject(new Error('PHP pools API unavailable'))
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })

    renderPage()

    fireEvent.click(await screen.findByRole('button', { name: 'Extensions' }))
    expect(await screen.findByText('PHP pool inventory could not be loaded. Pool-dependent controls are paused.')).toBeInTheDocument()
    expect(await screen.findByText('Extension controls')).toBeInTheDocument()
  })

  it('does not invent an average health score when pools have no measurements', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/php/versions') {
        return Promise.resolve([{ version: '8.4', active: true, info: 'PHP 8.4', pool_dir: '/etc/php/8.4/fpm/pool.d', pool_count: 1 }])
      }
      if (path === '/php/pools') {
        return Promise.resolve([{
          name: 'example.com',
          version: '8.4',
          config_file: '/etc/php/8.4/fpm/pool.d/example.com.conf',
          user: 'www-data',
          group: 'www-data',
          listen: '/run/php/example.com.sock',
          pm: 'dynamic',
          pm_settings: { max_children: 5, start_servers: 2, min_spare_servers: 1, max_spare_servers: 3, process_idle_timeout: '10s', max_requests: 500 },
          socket_exists: true,
        }])
      }
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })

    renderPage()

    expect(await screen.findByText('Avg Health')).toBeInTheDocument()
    expect(screen.getByText('—')).toBeInTheDocument()
    expect(screen.queryByText('85/100')).not.toBeInTheDocument()
  })

  it('preserves a genuine empty PHP installation state', async () => {
    vi.mocked(api.get).mockResolvedValue([])

    renderPage()

    expect(await screen.findByText('PHP-FPM is not installed')).toBeInTheDocument()
    expect(screen.getByText('/etc/php/<VERSION>/fpm')).toBeInTheDocument()
    expect(screen.getByText(/Heyserver does not install runtimes automatically/)).toBeInTheDocument()
    expect(screen.queryByRole('tab', { name: 'Pools' })).not.toBeInTheDocument()
  })

  it('runs every local PHP-FPM lifecycle action through the canonical action route', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/php/versions') {
        return Promise.resolve([{ version: '8.4', active: true, info: 'PHP 8.4', pool_dir: '/etc/php/8.4/fpm/pool.d', pool_count: 0 }])
      }
      if (path === '/php/pools') return Promise.resolve([])
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })
    vi.mocked(api.post).mockResolvedValue({ message: 'action completed' })

    renderPage()

    for (const [label, action] of [
      ['Test PHP-FPM 8.4', 'test'],
      ['Reload PHP-FPM 8.4', 'reload'],
      ['Restart PHP-FPM 8.4', 'restart'],
    ] as const) {
      fireEvent.click(await screen.findByRole('button', { name: label }))
      await waitFor(() => expect(api.post).toHaveBeenCalledWith(`/php/versions/8.4/actions/${action}`))
    }

    expect(api.post).toHaveBeenCalledTimes(3)
    expect(api.post).not.toHaveBeenCalledWith('/php/restart/8.4')
  })
})
