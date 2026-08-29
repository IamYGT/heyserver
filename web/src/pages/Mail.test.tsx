import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '@/lib/api'
import MailPage from './Mail'

vi.mock('@/lib/api', () => ({
  api: {
    get: vi.fn(),
    post: vi.fn(),
    put: vi.fn(),
    delete: vi.fn(),
  },
}))

const overview = {
  status: { running: true, status: 'running', pid: '42', uptime: '1h' },
  version: { raw: 'Stalwart v1.0.0', version: '1.0.0' },
  listeners: [],
  storage: { backend: 'rocksdb', path: '/var/lib/stalwart', sizeBytes: 0 },
}

function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <MailPage />
    </QueryClientProvider>,
  )
}

describe('Mail page failure states', () => {
  beforeEach(() => vi.clearAllMocks())

  it('pauses service controls when the overview is unavailable', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/mail/domains') return Promise.resolve([])
      if (path === '/mail/service/overview') return Promise.reject(new Error('Stalwart API unavailable'))
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })

    renderPage()

    expect(await screen.findByText('Stalwart mail integration is unavailable')).toBeInTheDocument()
    expect(screen.getByText('Stalwart API unavailable')).toBeInTheDocument()
    expect(screen.getByText('HSERVER_STALWART_SERVICE')).toBeInTheDocument()
    expect(screen.getByText('HSERVER_STALWART_CONFIG_PATH')).toBeInTheDocument()
    expect(screen.getByText('HSERVER_STALWART_BIN')).toBeInTheDocument()
    expect(screen.queryByText('Stopped')).not.toBeInTheDocument()
  })

  it('does not invent standard listeners for an empty installation response', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/mail/domains') return Promise.resolve([])
      if (path === '/mail/service/overview') return Promise.resolve(overview)
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })

    renderPage()

    expect(await screen.findByText('No listeners were reported by this Stalwart installation.')).toBeInTheDocument()
    expect(screen.queryByText(':25')).not.toBeInTheDocument()
    expect(screen.queryByText(':587')).not.toBeInTheDocument()
  })

  it('does not describe unknown runtime sources as stopped or empty', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/mail/domains') return Promise.resolve([])
      if (path === '/mail/service/overview') {
        return Promise.resolve({
          ...overview,
          status: { running: false, status: 'unknown', pid: '', uptime: '' },
          sources: {
            status: { available: false, error: 'systemd status unavailable' },
            version: { available: false, error: 'binary unavailable' },
            listeners: { available: false, error: 'config unavailable' },
            storage: { available: false, error: 'storage unavailable' },
          },
        })
      }
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })

    renderPage()

    expect(await screen.findByText('Unknown')).toBeInTheDocument()
    expect(screen.getByText('Stalwart service state is unavailable')).toBeInTheDocument()
    expect(screen.getByText('systemd status unavailable')).toBeInTheDocument()
    expect(screen.getByText('Listener inventory is unavailable.')).toBeInTheDocument()
    expect(screen.getByText('Storage inventory is unavailable.')).toBeInTheDocument()
    expect(screen.queryByText('Stopped')).not.toBeInTheDocument()
  })

  it('presents canonical source availability without translating runtime state or URL configuration', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/mail/domains') return Promise.resolve([])
      if (path === '/mail/service/overview') {
        return Promise.resolve({
          ...overview,
          status: { running: false, status: 'stopped', pid: '', uptime: '' },
          sources: {
            status: { available: true, state: 'stopped' },
            version: { available: true, url: 'https://stalwart.example.com' },
            listeners: { available: true, state: ' HEALTHY ' },
            storage: { available: false, state: 'not_configured', error: 'storage path is not configured' },
          },
        })
      }
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })

    renderPage()

    expect(await screen.findByTestId('mail-source-state-version')).toHaveTextContent('Unavailable')
    expect(screen.getByTestId('mail-source-state-listeners')).toHaveTextContent('Healthy')
    expect(screen.getByTestId('mail-source-state-storage')).toHaveTextContent('Not configured')
    expect(screen.queryByTestId('mail-source-state-status')).not.toBeInTheDocument()
    expect(screen.getByText('Stopped')).toBeInTheDocument()
  })

  it('keeps account inventory failures distinct from an empty account list', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/mail/domains') return Promise.resolve([{ name: 'example.com' }])
      if (path === '/mail/service/overview') return Promise.resolve(overview)
      if (path.startsWith('/mail/accounts')) return Promise.reject(new Error('Accounts API unavailable'))
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })

    renderPage()

    await screen.findByText('No listeners were reported by this Stalwart installation.')
    fireEvent.click(screen.getByRole('tab', { name: 'Accounts' }))

    expect(await screen.findByText('Mail accounts could not be loaded. Mutating controls are paused.')).toBeInTheDocument()
    expect(screen.queryByText('No mail accounts found')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'New Account' })).toBeDisabled()
  })

  it('discards an unsubmitted mail account and password when the dialog closes', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/mail/domains') return Promise.resolve([{ name: 'example.com' }])
      if (path === '/mail/service/overview') return Promise.resolve(overview)
      if (path === '/mail/accounts?domain=example.com') return Promise.resolve([])
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })

    renderPage()

    await screen.findByText('No listeners were reported by this Stalwart installation.')
    fireEvent.click(screen.getByRole('tab', { name: 'Accounts' }))
    await screen.findByText('No mail accounts found')
    fireEvent.click(screen.getByRole('button', { name: 'New Account' }))
    fireEvent.change(await screen.findByPlaceholderText('user@example.com'), { target: { value: 'draft@example.com' } })
    fireEvent.change(screen.getByPlaceholderText('John Doe'), { target: { value: 'Draft User' } })
    fireEvent.change(screen.getByPlaceholderText('••••••••'), { target: { value: 'discarded-password' } })
    fireEvent.change(screen.getByPlaceholderText('5368709120'), { target: { value: '1024' } })
    fireEvent.click(screen.getByRole('button', { name: 'Close' }))

    fireEvent.click(screen.getByRole('button', { name: 'New Account' }))

    expect(screen.getByPlaceholderText('user@example.com')).toHaveValue('')
    expect(screen.getByPlaceholderText('John Doe')).toHaveValue('')
    expect(screen.getByPlaceholderText('••••••••')).toHaveValue('')
    expect(screen.getByPlaceholderText('5368709120')).toHaveValue(5368709120)
  })

  it('discards an unsubmitted mail alias when the dialog closes', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/mail/domains') return Promise.resolve([])
      if (path === '/mail/service/overview') return Promise.resolve(overview)
      if (path === '/mail/aliases') return Promise.resolve([])
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })

    renderPage()

    await screen.findByText('No listeners were reported by this Stalwart installation.')
    fireEvent.click(screen.getByRole('tab', { name: 'Aliases' }))
    await screen.findByText('No aliases configured')
    fireEvent.click(screen.getByRole('button', { name: 'New Alias' }))
    fireEvent.change(await screen.findByPlaceholderText('alias@example.com'), { target: { value: 'draft@example.com' } })
    fireEvent.change(screen.getByPlaceholderText('real@example.com'), { target: { value: 'destination@example.com' } })
    fireEvent.click(screen.getByRole('button', { name: 'Close' }))

    fireEvent.click(screen.getByRole('button', { name: 'New Alias' }))

    expect(screen.getByPlaceholderText('alias@example.com')).toHaveValue('')
    expect(screen.getByPlaceholderText('real@example.com')).toHaveValue('')
  })

  it('does not report an empty DNS inventory when mail domains fail', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/mail/domains') return Promise.reject(new Error('Mail domains API unavailable'))
      if (path === '/mail/service/overview') return Promise.resolve(overview)
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })

    renderPage()

    expect(await screen.findByText('Mail domain inventory could not be loaded. Domain filtering and DNS health are unavailable.')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('tab', { name: 'DNS Health' }))

    expect(await screen.findByText('Mail domains could not be loaded. DNS health is unavailable.')).toBeInTheDocument()
    expect(screen.queryByText('No mail domains found')).not.toBeInTheDocument()
  })

  it('keeps queue failures distinct from an empty mail queue', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/mail/domains') return Promise.resolve([])
      if (path === '/mail/service/overview') return Promise.resolve(overview)
      if (path === '/mail/queue') return Promise.reject(new Error('Queue API unavailable'))
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })

    renderPage()

    await screen.findByText('No listeners were reported by this Stalwart installation.')
    fireEvent.click(screen.getByRole('tab', { name: 'Queue' }))

    expect(await screen.findByText('Mail queue could not be loaded. Queue controls are paused.')).toBeInTheDocument()
    expect(screen.queryByText('Mail queue is empty')).not.toBeInTheDocument()
  })
})
