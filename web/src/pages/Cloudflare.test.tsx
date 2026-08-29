import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api, ApiError } from '@/lib/api'
import Cloudflare, { EmailRoutingPanel, RecordsTable } from './Cloudflare'

vi.mock('@/lib/api', () => {
  class MockApiError extends Error {
    status: number

    constructor(status: number, message: string) {
      super(message)
      this.status = status
    }
  }

  return {
    api: { get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() },
    ApiError: MockApiError,
  }
})

function renderWithClient(node: React.ReactNode) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(<QueryClientProvider client={client}>{node}</QueryClientProvider>)
}

describe('Cloudflare inventory failures', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(api.get).mockImplementation((path) => Promise.reject(new Error(`${path} unavailable`)))
  })

  it('distinguishes provider and zone failures from empty Cloudflare inventories', async () => {
    renderWithClient(
      <>
        <Cloudflare />
        <RecordsTable zoneId="zone-1" />
        <EmailRoutingPanel zoneId="zone-1" />
      </>,
    )

    expect(await screen.findByText('Cloudflare is unavailable')).toBeInTheDocument()
    expect(screen.getByText('Check the token scope and zone access without printing the token.')).toBeInTheDocument()
    expect(await screen.findByText('Cloudflare DNS records could not be loaded. Record controls are paused.')).toBeInTheDocument()
    expect(await screen.findByText('Cloudflare email routing could not be loaded.')).toBeInTheDocument()
    expect(screen.queryByText('No Cloudflare zones found. Check your API credentials.')).not.toBeInTheDocument()
    expect(screen.queryByText('No records in this zone')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Add Record' })).not.toBeInTheDocument()
  })

  it('identifies a missing provider token without treating it as an outage', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/cloudflare/zones') {
        return Promise.reject(new ApiError(503, 'Cloudflare API token not configured (HSERVER_CF_API_TOKEN)'))
      }
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })

    renderWithClient(<Cloudflare />)

    expect(await screen.findByText('Cloudflare is not configured')).toBeInTheDocument()
    expect(screen.getByText('HSERVER_CF_API_TOKEN')).toBeInTheDocument()
    expect(screen.queryByText('Cloudflare is unavailable')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Add Record' })).not.toBeInTheDocument()
  })

  it('shows healthy for a successful empty zone probe', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/cloudflare/zones') {
        return Promise.resolve({ zones: [], state: 'healthy' })
      }
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })

    renderWithClient(<Cloudflare />)

    expect(await screen.findByTestId('cloudflare-availability-state')).toHaveTextContent('Healthy')
    expect(screen.getByText('No Cloudflare zones found.')).toBeInTheDocument()
    expect(screen.queryByText('Cloudflare is unavailable')).not.toBeInTheDocument()
  })

  it('keeps legacy zone arrays healthy during the envelope rollout', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/cloudflare/zones') {
        return Promise.resolve([])
      }
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })

    renderWithClient(<Cloudflare />)

    expect(await screen.findByTestId('cloudflare-availability-state')).toHaveTextContent('Healthy')
    expect(screen.getByText('No Cloudflare zones found.')).toBeInTheDocument()
  })
})
