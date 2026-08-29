import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { api } from '@/lib/api'
import MonitoringTab from './MonitoringTab'

vi.mock('@/lib/api', () => ({
  api: {
    get: vi.fn(),
    post: vi.fn(),
  },
  ApiError: class ApiError extends Error {},
}))

describe('PHP MonitoringTab failure states', () => {
  it('keeps an OPcache request failure distinct from unavailable data', async () => {
    vi.mocked(api.get).mockRejectedValue(new Error('OPcache API unavailable'))
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })

    render(
      <QueryClientProvider client={client}>
        <MonitoringTab selectedVersion="8.3" pools={[]} />
      </QueryClientProvider>,
    )

    expect(await screen.findByText('OPcache status could not be loaded for PHP 8.3.')).toBeInTheDocument()
    expect(screen.getByText('OPcache API unavailable')).toBeInTheDocument()
    expect(screen.queryByText('OPcache status unavailable for PHP 8.3')).not.toBeInTheDocument()
  })
})
