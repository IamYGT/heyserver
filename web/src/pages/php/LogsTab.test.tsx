import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { api } from '@/lib/api'
import LogsTab from './LogsTab'

vi.mock('@/lib/api', () => ({
  api: { get: vi.fn() },
}))

describe('PHP LogsTab failure states', () => {
  it('does not describe a failed error-log request as an empty log', async () => {
    vi.mocked(api.get).mockRejectedValue(new Error('PHP log API unavailable'))
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })

    render(
      <QueryClientProvider client={client}>
        <LogsTab selectedVersion="8.3" pools={[]} />
      </QueryClientProvider>,
    )

    expect(await screen.findByText('PHP error log could not be loaded.')).toBeInTheDocument()
    expect(screen.getByText('PHP log API unavailable')).toBeInTheDocument()
    expect(screen.queryByText('No error log entries')).not.toBeInTheDocument()
  })
})
