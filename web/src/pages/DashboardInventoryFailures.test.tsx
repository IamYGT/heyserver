import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '@/lib/api'
import { PM2Summary, RecentActivity, SslExpiryBanner, UptimeSummaryWidget } from './Dashboard'

vi.mock('@/lib/api', () => ({
  api: { get: vi.fn() },
}))

function renderWidgets() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <MemoryRouter>
      <QueryClientProvider client={client}>
        <SslExpiryBanner onManage={vi.fn()} />
        <PM2Summary />
        <UptimeSummaryWidget />
        <RecentActivity />
      </QueryClientProvider>
    </MemoryRouter>,
  )
}

describe('dashboard inventory failures', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(api.get).mockImplementation((path) => Promise.reject(new Error(`${path} unavailable`)))
  })

  it('does not turn failed secondary inventories into healthy, empty, or endless loading states', async () => {
    renderWidgets()

    expect(await screen.findByText('SSL certificate inventory could not be checked.')).toBeInTheDocument()
    expect(await screen.findByText('PM2 process inventory could not be loaded.')).toBeInTheDocument()
    expect(await screen.findByText('Uptime summary could not be loaded.')).toBeInTheDocument()
    expect(await screen.findByText('Recent activity could not be loaded.')).toBeInTheDocument()
    expect(screen.queryByText('online')).not.toBeInTheDocument()
    expect(screen.queryByText('stopped')).not.toBeInTheDocument()
    expect(screen.queryByText('No recent activity')).not.toBeInTheDocument()
  })
})
