import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '@/lib/api'
import SSL from './SSL'

vi.mock('@/lib/api', () => ({
  api: {
    get: vi.fn(),
    post: vi.fn(),
  },
}))

describe('SSL page failure state', () => {
  beforeEach(() => vi.clearAllMocks())

  it('does not describe an unavailable certificate inventory as empty', async () => {
    vi.mocked(api.get).mockRejectedValue(new Error('certbot inventory unavailable'))
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })

    render(
      <QueryClientProvider client={client}>
        <SSL />
      </QueryClientProvider>,
    )

    expect(await screen.findByText('Certificate inventory is unavailable')).toBeInTheDocument()
    expect(screen.getAllByText('certbot inventory unavailable')).toHaveLength(2)
    expect(screen.queryByText('No SSL certificates found')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Issue New Certificate' })).toBeDisabled()
  })

  it('keeps existing certificates visible but disables renewal when Certbot is missing', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/ssl/status') {
        return Promise.resolve({
          available: false,
          installed: false,
          state: 'certbot-missing',
          plugins: [],
          pluginsAvailable: false,
          nginxPlugin: false,
          dnsCloudflarePlugin: false,
          dnsCloudflareCredentialsConfigured: false,
          dnsCloudflareCredentialsReadable: false,
          error: 'certbot executable not found',
        })
      }
      if (path === '/ssl/certificates') {
        return Promise.resolve([{
          domain: 'example.com',
          issuer: "Let's Encrypt",
          subject: 'example.com',
          serial: '01',
          notBefore: '2026-08-01T00:00:00Z',
          notAfter: '2026-11-01T00:00:00Z',
          daysRemaining: 67,
          isWildcard: false,
          sans: ['example.com'],
          certPath: '/etc/letsencrypt/live/example.com/cert.pem',
          keyPath: '/etc/letsencrypt/live/example.com/privkey.pem',
        }])
      }
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })

    render(
      <QueryClientProvider client={client}>
        <SSL />
      </QueryClientProvider>,
    )

    expect(await screen.findByText('Certbot is not installed')).toBeInTheDocument()
    expect(screen.getByText('example.com')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Renew' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Issue New Certificate' })).toBeDisabled()
  })

  it('enables issuance only after Certbot and the nginx plugin are observed', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/ssl/status') {
        return Promise.resolve({
          available: true,
          installed: true,
          state: 'healthy',
          version: 'certbot 4.0.0',
          plugins: ['nginx'],
          pluginsAvailable: true,
          nginxPlugin: true,
          dnsCloudflarePlugin: false,
          dnsCloudflareCredentialsConfigured: false,
          dnsCloudflareCredentialsReadable: false,
        })
      }
      if (path === '/ssl/certificates') return Promise.resolve([])
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })

    render(
      <QueryClientProvider client={client}>
        <SSL />
      </QueryClientProvider>,
    )

    const issueButton = await screen.findByRole('button', { name: 'Issue New Certificate' })
    await waitFor(() => expect(issueButton).toBeEnabled())
    expect(screen.getByText('No SSL certificates found')).toBeInTheDocument()
  })

  it('pauses new issuance when Certbot plugin inventory is unknown', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/ssl/status') {
        return Promise.resolve({
          available: true,
          installed: true,
          state: 'healthy',
          version: 'certbot 4.0.0',
          plugins: [],
          pluginsAvailable: false,
          nginxPlugin: false,
          dnsCloudflarePlugin: false,
          dnsCloudflareCredentialsConfigured: false,
          dnsCloudflareCredentialsReadable: false,
          pluginError: 'certbot plugins failed',
        })
      }
      if (path === '/ssl/certificates') return Promise.resolve([])
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })

    render(
      <QueryClientProvider client={client}>
        <SSL />
      </QueryClientProvider>,
    )

    expect(await screen.findByText('Certbot plugin inventory is unavailable')).toBeInTheDocument()
    expect(screen.getByText('certbot plugins failed')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Issue New Certificate' })).toBeDisabled()
  })

  it('explains the missing credential boundary when DNS-01 is the only authenticator', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/ssl/status') {
        return Promise.resolve({
          available: true,
          installed: true,
          state: 'healthy',
          version: 'certbot 4.0.0',
          plugins: ['dns-cloudflare'],
          pluginsAvailable: true,
          nginxPlugin: false,
          dnsCloudflarePlugin: true,
          dnsCloudflareCredentialsConfigured: false,
          dnsCloudflareCredentialsReadable: false,
        })
      }
      if (path === '/ssl/certificates') return Promise.resolve([])
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })

    render(
      <QueryClientProvider client={client}>
        <SSL />
      </QueryClientProvider>,
    )

    expect(await screen.findByText('DNS-01 credentials are not configured')).toBeInTheDocument()
    expect(screen.getByText('HSERVER_CERTBOT_CLOUDFLARE_CREDENTIALS')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Issue New Certificate' })).toBeDisabled()
  })
})
