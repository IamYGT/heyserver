import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '@/lib/api'
import Webmail from './Webmail'

vi.mock('@/lib/api', () => ({
  api: { get: vi.fn() },
}))

function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <Webmail />
    </QueryClientProvider>,
  )
}

const completeMailSettings = {
  webmail_url: 'https://webmail.example.com',
  mail_admin_url: 'https://admin.example.com',
  mail_server_host: 'mail.example.com',
  mail_imap_port: '993',
  mail_smtp_starttls_port: '587',
  mail_smtp_ssl_port: '465',
}

describe('Webmail dependency readiness', () => {
  beforeEach(() => vi.clearAllMocks())

  it('does not flash a false not-configured state while settings are loading', () => {
    vi.mocked(api.get).mockReturnValue(new Promise(() => {}))

    renderPage()

    expect(screen.getByText('Checking')).toBeInTheDocument()
    expect(screen.getByText('Loading mail access settings')).toBeInTheDocument()
    expect(screen.queryByText('Not configured')).not.toBeInTheDocument()
    expect(screen.queryByText('Mail access is not configured')).not.toBeInTheDocument()
  })

  it('keeps provider links and client guidance hidden when settings are unavailable', async () => {
    vi.mocked(api.get).mockRejectedValue(new Error('settings database is unreadable'))

    renderPage()

    expect(await screen.findByText('Mail access settings are unavailable')).toBeInTheDocument()
    expect(screen.getByText('settings database is unreadable')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Retry detection' })).toBeInTheDocument()
    expect(screen.getByText('Unavailable')).toBeInTheDocument()
    expect(screen.queryByText('Not configured')).not.toBeInTheDocument()
    expect(screen.queryByText('Full email address')).not.toBeInTheDocument()
    expect(screen.queryByRole('link', { name: 'Open Webmail' })).not.toBeInTheDocument()
  })

  it('explains the complete provider-neutral configuration boundary', async () => {
    vi.mocked(api.get).mockResolvedValue({})

    renderPage()

    expect(await screen.findByText('Mail access is not configured')).toBeInTheDocument()
    expect(screen.getByText(/will not invent a provider/)).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Open Settings → Mail Access.' })).toHaveAttribute('href', '/settings')
    expect(screen.queryByRole('link', { name: 'Open Webmail' })).not.toBeInTheDocument()
  })

  it('keeps complete settings usable without claiming provider reachability', async () => {
    vi.mocked(api.get).mockResolvedValue({
      ...completeMailSettings,
      mail_access_state: 'unavailable',
    })

    renderPage()

    expect(await screen.findByText('Mail access reachability is unverified')).toBeInTheDocument()
    expect(screen.getByText('Unavailable')).toBeInTheDocument()
    expect(screen.queryByText('Healthy')).not.toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Open Webmail' })).toHaveAttribute('href', completeMailSettings.webmail_url)
    expect(screen.getByText(/configuration-only until reachability is checked separately/)).toBeInTheDocument()
  })

  it('treats a partial legacy response as not configured even when port defaults exist', async () => {
    vi.mocked(api.get).mockResolvedValue({
      webmail_url: completeMailSettings.webmail_url,
      mail_admin_url: completeMailSettings.mail_admin_url,
      mail_server_host: completeMailSettings.mail_server_host,
    })

    renderPage()

    expect(await screen.findByText('Mail access is not configured')).toBeInTheDocument()
    expect(screen.getAllByText('Not configured')).toHaveLength(2)
    expect(screen.queryByRole('link', { name: 'Open Webmail' })).not.toBeInTheDocument()
  })
})
