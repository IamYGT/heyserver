import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '@/lib/api'
import Notifications from './Notifications'

vi.mock('@/lib/api', () => ({
  api: { get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() },
}))

function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <Notifications />
    </QueryClientProvider>,
  )
}

describe('notification inventory failures', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(api.get).mockRejectedValue(new Error('Notification API unavailable'))
  })

  it('does not present an unavailable channel inventory as empty', async () => {
    renderPage()

    expect(await screen.findByText('Notification channels could not be loaded. Channel controls are paused.')).toBeInTheDocument()
    expect(screen.getByText('Notification API unavailable')).toBeInTheDocument()
    expect(screen.queryByText('No channels configured')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Add Channel' })).not.toBeInTheDocument()
  })

  it('does not expose rule mutations when the rule inventory is unavailable', async () => {
    renderPage()
    fireEvent.click(screen.getByRole('button', { name: 'Alert Rules' }))

    expect(await screen.findByText('Alert rules could not be loaded. Rule controls are paused.')).toBeInTheDocument()
    expect(screen.queryByText('No alert rules configured')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Add Rule' })).not.toBeInTheDocument()
  })

  it('keeps a history failure distinct from a genuine empty history', async () => {
    renderPage()
    fireEvent.click(screen.getByRole('button', { name: 'History' }))

    expect(await screen.findByText('Notification history could not be loaded.')).toBeInTheDocument()
    expect(screen.queryByText('No alerts triggered yet')).not.toBeInTheDocument()
  })

  it('discards an unsubmitted channel draft and its secret fields', async () => {
    vi.mocked(api.get).mockResolvedValue([])
    renderPage()

    await screen.findByText('No notification channels are configured')
    fireEvent.click(screen.getByRole('button', { name: 'Add Channel' }))
    fireEvent.change(screen.getByPlaceholderText('My Channel'), { target: { value: 'Temporary SMTP' } })
    fireEvent.change(screen.getByPlaceholderText('user@example.com'), { target: { value: 'operator@example.com' } })
    fireEvent.change(screen.getByPlaceholderText('••••••••'), { target: { value: 'unsubmitted-password' } })
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))

    fireEvent.click(screen.getByRole('button', { name: 'Add Channel' }))

    expect(screen.getByPlaceholderText('My Channel')).toHaveValue('')
    expect(screen.getByPlaceholderText('user@example.com')).toHaveValue('')
    expect(screen.getByPlaceholderText('••••••••')).toHaveValue('')
  })

  it('requires confirmation before deleting a notification channel', async () => {
    vi.mocked(api.get).mockResolvedValue([{
      id: 7,
      type: 'email',
      name: 'Operations Email',
      enabled: true,
      config: '{}',
    }])
    vi.mocked(api.delete).mockResolvedValue({ status: 'ok' })
    renderPage()

    await screen.findByText('Operations Email')
    fireEvent.click(screen.getByTitle('Delete'))

    expect(api.delete).not.toHaveBeenCalled()
    expect(screen.getByText('Delete Notification Channel')).toBeInTheDocument()
    expect(screen.getByText(/^Delete “Operations Email”/)).toBeInTheDocument()

    const deleteButtons = screen.getAllByRole('button', { name: 'Delete' })
    fireEvent.click(deleteButtons[deleteButtons.length - 1])
    await waitFor(() => expect(api.delete).toHaveBeenCalledWith('/notify/channels/7'))
  })

  it('discards an unsubmitted alert-rule draft', async () => {
    vi.mocked(api.get).mockResolvedValue([])
    renderPage()
    fireEvent.click(screen.getByRole('button', { name: 'Alert Rules' }))

    await screen.findByText('No alert rules configured')
    fireEvent.click(screen.getByRole('button', { name: 'Add Rule' }))
    fireEvent.change(screen.getByPlaceholderText('High CPU Alert'), { target: { value: 'Temporary Rule' } })
    fireEvent.change(screen.getByPlaceholderText('90'), { target: { value: '75' } })
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))

    fireEvent.click(screen.getByRole('button', { name: 'Add Rule' }))

    expect(screen.getByPlaceholderText('High CPU Alert')).toHaveValue('')
    expect(screen.getByPlaceholderText('90')).toHaveValue(90)
  })

  it('creates CPU rules with the canonical evaluator type and no fake operator', async () => {
    vi.mocked(api.get).mockResolvedValue([])
    vi.mocked(api.post).mockResolvedValue({ status: 'ok' })
    renderPage()
    fireEvent.click(screen.getByRole('button', { name: 'Alert Rules' }))

    await screen.findByText('No alert rules configured')
    fireEvent.click(screen.getByRole('button', { name: 'Add Rule' }))
    fireEvent.change(screen.getByPlaceholderText('High CPU Alert'), { target: { value: 'CPU pressure' } })
    expect(screen.queryByText('Operator')).not.toBeInTheDocument()
    const saveButtons = screen.getAllByRole('button', { name: 'Add Rule' })
    fireEvent.click(saveButtons[saveButtons.length - 1])

    await waitFor(() => expect(api.post).toHaveBeenCalledWith('/notify/rules', {
      name: 'CPU pressure',
      type: 'cpu_usage',
      threshold: 90,
      durationMins: 5,
      cooldownMins: 30,
      target: '',
    }))
  })

  it('requires and submits the SSL certificate domain', async () => {
    vi.mocked(api.get).mockResolvedValue([])
    vi.mocked(api.post).mockResolvedValue({ status: 'ok' })
    renderPage()
    fireEvent.click(screen.getByRole('button', { name: 'Alert Rules' }))
    await screen.findByText('No alert rules configured')
    fireEvent.click(screen.getByRole('button', { name: 'Add Rule' }))

    fireEvent.change(screen.getByPlaceholderText('High CPU Alert'), { target: { value: 'Certificate expiry' } })
    fireEvent.change(screen.getByRole('combobox'), { target: { value: 'ssl_expiry' } })
    const saveButtons = screen.getAllByRole('button', { name: 'Add Rule' })
    fireEvent.click(saveButtons[saveButtons.length - 1])
    expect(api.post).not.toHaveBeenCalled()

    fireEvent.change(screen.getByPlaceholderText('example.com'), { target: { value: 'panel.example.com' } })
    fireEvent.click(saveButtons[saveButtons.length - 1])
    await waitFor(() => expect(api.post).toHaveBeenCalledWith('/notify/rules', expect.objectContaining({
      type: 'ssl_expiry',
      threshold: 14,
      target: 'panel.example.com',
    })))
  })

  it('requires and submits a systemd unit without a meaningless threshold', async () => {
    vi.mocked(api.get).mockResolvedValue([])
    vi.mocked(api.post).mockResolvedValue({ status: 'ok' })
    renderPage()
    fireEvent.click(screen.getByRole('button', { name: 'Alert Rules' }))
    await screen.findByText('No alert rules configured')
    fireEvent.click(screen.getByRole('button', { name: 'Add Rule' }))

    fireEvent.change(screen.getByPlaceholderText('High CPU Alert'), { target: { value: 'Nginx availability' } })
    fireEvent.change(screen.getByRole('combobox'), { target: { value: 'service_down' } })
    expect(screen.getByText('Unit is not active')).toBeInTheDocument()
    const saveButtons = screen.getAllByRole('button', { name: 'Add Rule' })
    fireEvent.click(saveButtons[saveButtons.length - 1])
    expect(api.post).not.toHaveBeenCalled()

    fireEvent.change(screen.getByPlaceholderText('nginx.service'), { target: { value: 'nginx.service' } })
    fireEvent.click(saveButtons[saveButtons.length - 1])
    await waitFor(() => expect(api.post).toHaveBeenCalledWith('/notify/rules', expect.objectContaining({
      type: 'service_down',
      threshold: 1,
      target: 'nginx.service',
    })))
  })

  it('offers failed-login rules and preserves partial toggle updates', async () => {
    vi.mocked(api.get).mockResolvedValue([{
      id: 12,
      name: 'SSH attacks',
      type: 'failed_logins',
      threshold: 5,
      durationMins: 0,
      cooldownMins: 30,
      enabled: true,
      target: '',
    }])
    vi.mocked(api.put).mockResolvedValue({ status: 'ok' })
    renderPage()
    fireEvent.click(screen.getByRole('button', { name: 'Alert Rules' }))

    await screen.findByText('SSH attacks')
    expect(screen.getByText('Failed SSH Logins')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Add Rule' }))
    expect(screen.getByRole('option', { name: 'Failed SSH Logins' })).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    fireEvent.click(screen.getByRole('button', { name: 'Disable SSH attacks' }))
    await waitFor(() => expect(api.put).toHaveBeenCalledWith('/notify/rules/12', { enabled: false }))
  })

  it('requires confirmation before deleting an alert rule', async () => {
    vi.mocked(api.get).mockImplementation(async (url: string) => {
      if (url === '/notify/rules') {
        return [{
          id: 11,
          name: 'Disk Pressure',
          type: 'disk',
          threshold: 90,
          durationMins: 5,
          cooldownMins: 30,
          enabled: true,
          target: '',
        }]
      }
      return []
    })
    vi.mocked(api.delete).mockResolvedValue({ status: 'ok' })
    renderPage()
    fireEvent.click(screen.getByRole('button', { name: 'Alert Rules' }))

    await screen.findByText('Disk Pressure')
    fireEvent.click(screen.getByTitle('Delete'))

    expect(api.delete).not.toHaveBeenCalled()
    expect(screen.getByText('Delete Alert Rule')).toBeInTheDocument()

    const deleteButtons = screen.getAllByRole('button', { name: 'Delete' })
    fireEvent.click(deleteButtons[deleteButtons.length - 1])
    await waitFor(() => expect(api.delete).toHaveBeenCalledWith('/notify/rules/11'))
  })

  it('never receives an existing channel secret and submits a blank preserve marker on edit', async () => {
    vi.mocked(api.get).mockResolvedValue([{
      id: 21,
      type: 'telegram',
      name: 'Operations Telegram',
      enabled: true,
      config: '{"chat_id":"-1001","secret_configured":true}',
    }])
    vi.mocked(api.put).mockResolvedValue({ status: 'ok' })
    renderPage()

    await screen.findByText('Operations Telegram')
    fireEvent.click(screen.getByTitle('Edit'))

    const tokenInput = screen.getByPlaceholderText('Leave blank to keep current bot token')
    expect(tokenInput).toHaveValue('')
    expect(tokenInput).toHaveAttribute('type', 'password')
    fireEvent.change(screen.getByDisplayValue('-1001'), { target: { value: '-2002' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save Changes' }))

    await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
    const [, payload] = vi.mocked(api.put).mock.calls[0]
    expect(payload).toMatchObject({ name: 'Operations Telegram', type: 'telegram' })
    expect(JSON.parse((payload as { config: string }).config)).toEqual({ bot_token: '', chat_id: '-2002' })

    fireEvent.click(screen.getByTitle('Edit'))
    const removeCredential = screen.getByRole('checkbox', { name: /Remove the stored credential/ })
    fireEvent.click(removeCredential)
    expect(screen.getByPlaceholderText('Leave blank to keep current bot token')).toBeDisabled()
    fireEvent.click(screen.getByRole('button', { name: 'Save Changes' }))

    await waitFor(() => expect(api.put).toHaveBeenCalledTimes(2))
    expect(vi.mocked(api.put).mock.calls[1][1]).toMatchObject({ clearSecret: true })
  })
})

describe('notification delivery availability', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('shows not configured for an empty channel inventory', async () => {
    vi.mocked(api.get).mockResolvedValue([])
    renderPage()

    expect(await screen.findByTestId('notification-availability')).toHaveTextContent('Not configured')
    expect(screen.getByTestId('notification-availability-card')).toHaveTextContent('No channel configuration is present')
  })

  it('does not infer healthy from a configured channel without a persisted probe', async () => {
    vi.mocked(api.get).mockResolvedValue([{
      id: 31,
      type: 'slack',
      name: 'Operations Slack',
      enabled: true,
      config: '{"channel":"#ops","secret_configured":true}',
      state: 'unavailable',
      detail: 'probe_unverified',
    }])
    renderPage()

    expect(await screen.findByTestId('notification-availability')).toHaveTextContent('Unavailable')
    expect(screen.getByTestId('notification-availability-card')).toHaveTextContent('no successful delivery probe is persisted')
    expect(screen.queryByText('Healthy')).not.toBeInTheDocument()
  })

  it('renders healthy only when the API supplies the explicit canonical state', async () => {
    vi.mocked(api.get).mockResolvedValue([{
      id: 32,
      type: 'discord',
      name: 'Verified Discord',
      enabled: true,
      config: '{}',
      state: 'healthy',
      detail: 'delivery_confirmed',
    }])
    renderPage()

    expect(await screen.findByTestId('notification-availability')).toHaveTextContent('Healthy')
    expect(screen.getByTestId('notification-channel-detail-32')).toHaveTextContent('delivery_confirmed')
    expect(screen.getByTestId('notification-availability-card')).toHaveTextContent('receipt is current')
  })

  it('does not infer a state from redacted config when the API omits its observation', async () => {
    vi.mocked(api.get).mockResolvedValue([{
      id: 33,
      type: 'slack',
      name: 'Unobserved Slack',
      enabled: true,
      config: '{"channel":"#ops","secret_configured":true}',
    }])
    renderPage()

    expect(await screen.findByTestId('notification-availability')).toHaveTextContent('Unavailable')
    expect(screen.getByTestId('notification-channel-detail-33')).toHaveTextContent('probe_unverified')
    expect(screen.queryByText('Healthy')).not.toBeInTheDocument()
  })

  it.each([
    {
      detail: 'delivery_failed',
      id: 41,
      action: 'Check the channel settings and provider, then send a new test notification.',
    },
    {
      detail: 'delivery_stale',
      id: 42,
      action: 'The last successful delivery receipt is older than 7 days.',
    },
    {
      detail: 'probe_unverified',
      id: 43,
      action: 'No current successful delivery receipt matches this channel configuration.',
    },
  ])('renders exact $detail detail with its next action', async ({ detail, id, action }) => {
    vi.mocked(api.get).mockResolvedValue([{
      id,
      type: 'slack',
      name: `Channel ${id}`,
      enabled: true,
      config: '{"channel":"#ops","secret_configured":true}',
      state: 'unavailable',
      detail,
    }])
    renderPage()

    const detailRegion = await screen.findByTestId(`notification-channel-detail-${id}`)
    expect(detailRegion).toHaveTextContent(detail)
    expect(detailRegion).toHaveTextContent(action)
    expect(screen.getByTestId('notification-availability-card')).toHaveTextContent(detail)
  })
})
