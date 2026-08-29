import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { api } from '@/lib/api'
import Logs from './Logs'

vi.mock('@/lib/api', () => ({
  api: { get: vi.fn() },
  authenticatedFetch: vi.fn(),
}))

describe('Logs page failure states', () => {
  it('does not describe failed source and read requests as an empty log', async () => {
    vi.mocked(api.get).mockRejectedValue(new Error('Logs API unavailable'))
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })

    render(
      <QueryClientProvider client={client}>
        <Logs />
      </QueryClientProvider>,
    )

    expect(await screen.findByText('Selected log could not be read.')).toBeInTheDocument()
    expect(screen.getByText('Log source inventory could not be loaded. Showing portable default paths.')).toBeInTheDocument()
    expect(screen.queryByText('Log is empty')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Download' })).toBeDisabled()
  })
})
