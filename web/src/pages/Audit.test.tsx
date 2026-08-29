import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it, vi } from 'vitest'
import { api } from '@/lib/api'
import Audit from './Audit'

vi.mock('@/lib/api', () => ({
  api: { get: vi.fn() },
}))

describe('Audit page failure states', () => {
  it('does not describe unavailable audit events as an empty filtered result', async () => {
    vi.mocked(api.get).mockRejectedValue(new Error('Audit API unavailable'))
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })

    render(
      <MemoryRouter>
        <QueryClientProvider client={client}>
          <Audit />
        </QueryClientProvider>
      </MemoryRouter>,
    )

    expect(await screen.findByText('Audit events could not be loaded.')).toBeInTheDocument()
    expect(screen.getByText('Managed server options could not be loaded. Local and all-activity filters remain available.')).toBeInTheDocument()
    expect(screen.queryByText('No audit events match the current filters')).not.toBeInTheDocument()
    expect(screen.getByText('Audit events unavailable')).toBeInTheDocument()
  })
})
