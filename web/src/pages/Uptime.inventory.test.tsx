import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '@/lib/api'
import type { UptimeMonitor } from '@/lib/types'
import Uptime, { IncidentsTab, MonitorDetail, SettingsTab, StatusPagesTab } from './Uptime'

vi.mock('@/lib/api', () => ({
  api: { get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() },
}))

const monitor = {
  id: 1,
  name: 'Example monitor',
  type: 'http',
  url: 'https://example.com',
  current_status: 1,
  maintenance_mode: false,
} as UptimeMonitor

function renderNode(node: React.ReactNode) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <MemoryRouter>
      <QueryClientProvider client={client}>{node}</QueryClientProvider>
    </MemoryRouter>,
  )
}

describe('uptime inventory failures', () => {
  beforeEach(() => vi.clearAllMocks())

  it('keeps a monitor API failure distinct from an empty monitor inventory', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/uptime/monitors') return Promise.reject(new Error('Monitor API unavailable'))
      return Promise.resolve([])
    })

    renderNode(<Uptime />)

    expect(await screen.findByText('Monitor inventory could not be loaded. Uptime controls are paused.')).toBeInTheDocument()
    expect(screen.getByText('Monitor API unavailable')).toBeInTheDocument()
    expect(screen.queryByText('No monitors found')).not.toBeInTheDocument()
  })

  it('does not report missing incidents or status pages when their inventories fail', async () => {
    vi.mocked(api.get).mockRejectedValue(new Error('Uptime API unavailable'))

    renderNode(
      <>
        <IncidentsTab monitors={[monitor]} onCheckNow={vi.fn()} checkingId={null} />
        <StatusPagesTab monitors={[monitor]} />
      </>,
    )

    expect(await screen.findByText('Incident history could not be loaded.')).toBeInTheDocument()
    expect(await screen.findByText('Status page inventory could not be loaded.')).toBeInTheDocument()
    expect(screen.queryByText('No incidents recorded')).not.toBeInTheDocument()
    expect(screen.queryByText('No status pages yet')).not.toBeInTheDocument()
  })

  it('pauses settings when notification channels are unavailable', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/notify/channels') return Promise.reject(new Error('Notification API unavailable'))
      return Promise.resolve({})
    })

    renderNode(<SettingsTab />)

    expect(await screen.findByText('Notification channel inventory could not be loaded. Settings controls are paused.')).toBeInTheDocument()
    expect(screen.queryByText(/No channels configured/)).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Save Settings' })).toBeDisabled()
  })

  it('does not render fake empty monitor history when detail sources fail', async () => {
    vi.mocked(api.get).mockRejectedValue(new Error('History API unavailable'))

    renderNode(<MonitorDetail monitor={monitor} incidents={[]} />)

    expect(await screen.findByText('90-day heartbeat history could not be loaded.')).toBeInTheDocument()
    expect(await screen.findByText('Uptime statistics could not be loaded.')).toBeInTheDocument()
    expect(await screen.findByText('Response-time history could not be loaded.')).toBeInTheDocument()
    expect(screen.queryByText('No data yet')).not.toBeInTheDocument()
  })

  it('discards cancelled monitor request headers and body', async () => {
    vi.mocked(api.get).mockResolvedValue([])

    renderNode(<Uptime />)

    await screen.findByText('No monitors found')
    fireEvent.click(screen.getByRole('button', { name: 'Add Monitor' }))
    fireEvent.change(screen.getByPlaceholderText('My Website'), { target: { value: 'Private Healthcheck' } })
    fireEvent.change(screen.getByPlaceholderText('https://example.com'), { target: { value: 'https://private.example.com/health' } })
    fireEvent.change(screen.getByPlaceholderText('{"Authorization": "Bearer token"}'), { target: { value: '{"Authorization":"Bearer unsubmitted"}' } })
    fireEvent.change(screen.getByPlaceholderText('POST body'), { target: { value: '{"probe":true}' } })
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))

    fireEvent.click(screen.getByRole('button', { name: 'Add Monitor' }))

    expect(screen.getByPlaceholderText('My Website')).toHaveValue('')
    expect(screen.getByPlaceholderText('https://example.com')).toHaveValue('')
    expect(screen.getByPlaceholderText('{"Authorization": "Bearer token"}')).toHaveValue('')
    expect(screen.getByPlaceholderText('POST body')).toHaveValue('')
  })

  it('discards a cancelled status-page draft', async () => {
    vi.mocked(api.get).mockResolvedValue([])

    renderNode(<StatusPagesTab monitors={[monitor]} />)

    await screen.findByText('No status pages yet')
    fireEvent.click(screen.getByRole('button', { name: 'Create Status Page' }))
    fireEvent.change(screen.getByPlaceholderText('my-status'), { target: { value: 'temporary-status' } })
    fireEvent.change(screen.getByPlaceholderText('System Status'), { target: { value: 'Temporary Status' } })
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))

    fireEvent.click(screen.getByRole('button', { name: 'Create Status Page' }))

    expect(screen.getByPlaceholderText('my-status')).toHaveValue('')
    expect(screen.getByPlaceholderText('System Status')).toHaveValue('')
  })

  it('sends empty strings when optional monitor fields are cleared', async () => {
    const editableMonitor = {
      ...monitor,
      method: 'GET',
      accepted_statuscodes: '200-299',
      keyword: 'ready',
      keyword_invert: false,
      req_headers: '{"X-Probe":"active"}',
      req_body: '{"probe":true}',
      tls_check: false,
      tls_expiry_warn_days: 14,
      interval_secs: 60,
      timeout_secs: 30,
      retries: 1,
      retry_interval: 30,
      max_redirects: 5,
      description: 'temporary',
      alert_channel_ids: [],
      alert_reminder_mins: 0,
      is_active: true,
    } as UptimeMonitor
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/uptime/monitors') return Promise.resolve([editableMonitor])
      return Promise.resolve([])
    })
    vi.mocked(api.put).mockResolvedValue(editableMonitor)

    renderNode(<Uptime />)

    await screen.findByText('Example monitor')
    fireEvent.click(screen.getByTitle('Edit'))
    fireEvent.change(screen.getByPlaceholderText('Optional description'), { target: { value: '' } })
    fireEvent.change(screen.getByPlaceholderText('{"Authorization": "Bearer token"}'), { target: { value: '' } })
    fireEvent.change(screen.getByPlaceholderText('POST body'), { target: { value: '' } })
    fireEvent.change(screen.getByPlaceholderText('Expected text'), { target: { value: '' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save Changes' }))

    await waitFor(() => {
      expect(api.put).toHaveBeenCalledWith('/uptime/monitors/1', expect.objectContaining({
        description: '',
        keyword: '',
        req_headers: '',
        req_body: '',
      }))
    })
  })

  it('preserves DNS record type and expected value during an edit', async () => {
    const dnsMonitor = {
      ...monitor,
      name: 'Mail DNS',
      type: 'dns',
      url: undefined,
      hostname: 'example.com',
      method: 'GET',
      accepted_statuscodes: '["200-299"]',
      keyword: 'MX',
      req_body: 'mail.example.com',
      keyword_invert: false,
      tls_check: false,
      tls_expiry_warn_days: 14,
      interval_secs: 60,
      timeout_secs: 30,
      retries: 1,
      retry_interval: 30,
      max_redirects: 5,
      description: 'mail records',
      alert_channel_ids: [],
      alert_reminder_mins: 0,
      is_active: true,
    } as UptimeMonitor
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/uptime/monitors') return Promise.resolve([dnsMonitor])
      return Promise.resolve([])
    })
    vi.mocked(api.put).mockResolvedValue(dnsMonitor)

    renderNode(<Uptime />)

    await screen.findByText('Mail DNS')
    fireEvent.click(screen.getByTitle('Edit'))
    expect(screen.getByRole('combobox')).toHaveValue('MX')
    expect(screen.getByPlaceholderText('1.2.3.4')).toHaveValue('mail.example.com')
    fireEvent.change(screen.getByPlaceholderText('Optional description'), { target: { value: 'updated mail records' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save Changes' }))

    await waitFor(() => {
      expect(api.put).toHaveBeenCalledWith('/uptime/monitors/1', expect.objectContaining({
        keyword: 'MX',
        req_body: 'mail.example.com',
        description: 'updated mail records',
      }))
    })
  })
})
