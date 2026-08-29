import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api, ApiError } from '@/lib/api'
import type { Domain } from '@/lib/types'
import DomainDetail, { FilesTab, NginxTab, SslTab, UptimeTab } from './DomainDetail'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, api: { get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() } }
})

const domain: Domain = {
  id: 'domain-1',
  name: 'example.com',
  type: 'php',
  root: '/var/www/example.com',
  phpVersion: '8.4',
  sslEnabled: true,
  isActive: true,
}

function renderNode(node: React.ReactNode) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <MemoryRouter initialEntries={['/domains/example.com']}>
      <QueryClientProvider client={client}>{node}</QueryClientProvider>
    </MemoryRouter>,
  )
}

describe('domain detail inventory failures', () => {
  beforeEach(() => vi.clearAllMocks())

  it('keeps a domain inventory failure distinct from a missing domain', async () => {
    vi.mocked(api.get).mockRejectedValue(new Error('Domain API unavailable'))

    renderNode(
      <Routes>
        <Route path="/domains/:name" element={<DomainDetail />} />
      </Routes>,
    )

    expect(await screen.findByText('Domain inventory could not be loaded.')).toBeInTheDocument()
    expect(screen.getByText('Domain API unavailable')).toBeInTheDocument()
    expect(screen.queryByText('Domain not found')).not.toBeInTheDocument()
  })

  it('pauses domain tab controls when their source inventories are unavailable', async () => {
    vi.mocked(api.get).mockImplementation((path) => Promise.reject(new Error(`${path} unavailable`)))

    renderNode(
      <>
        <NginxTab domain={domain} />
        <SslTab domain={domain} />
        <FilesTab domain={domain} />
        <UptimeTab domain={domain} />
      </>,
    )

    expect(await screen.findByText('Nginx configuration inventory could not be loaded.')).toBeInTheDocument()
    expect(await screen.findByText('SSL certificate inventory could not be loaded.')).toBeInTheDocument()
    expect(await screen.findByText('Files in /var/www/example.com could not be loaded. File controls are paused.')).toBeInTheDocument()
    expect(await screen.findByText('Uptime monitor for example.com could not be loaded.')).toBeInTheDocument()
    expect(screen.queryByText('No nginx config found for this domain.')).not.toBeInTheDocument()
    expect(screen.queryByText('No SSL certificate found for this domain.')).not.toBeInTheDocument()
    expect(screen.queryByText('Empty directory')).not.toBeInTheDocument()
  })

  it('preserves the genuine no-monitor setup state for a 404 response', async () => {
    vi.mocked(api.get).mockRejectedValue(new ApiError(404, 'no monitor found for domain: example.com'))

    renderNode(<UptimeTab domain={domain} />)

    expect(await screen.findByText('No Uptime Monitoring')).toBeInTheDocument()
    expect(screen.queryByText('Uptime monitor for example.com could not be loaded.')).not.toBeInTheDocument()
  })

  it('pauses file controls instead of guessing a provider-specific root', () => {
    renderNode(<FilesTab domain={{ ...domain, root: '' }} />)

    expect(screen.getByText('Domain document root is unavailable.')).toBeInTheDocument()
    expect(screen.getByText('File controls are paused instead of guessing an installation path.')).toBeInTheDocument()
    expect(api.get).not.toHaveBeenCalled()
  })
})
