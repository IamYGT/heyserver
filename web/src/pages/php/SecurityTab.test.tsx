import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { api } from '@/lib/api'
import type { PHPPool } from '@/lib/types'
import SecurityTab from './SecurityTab'

vi.mock('@/lib/api', () => ({
  api: {
    get: vi.fn(),
    post: vi.fn(),
  },
}))

describe('PHP SecurityTab failure states', () => {
  it('shows a retryable error instead of treating failed profile requests as empty data', async () => {
    vi.mocked(api.get).mockRejectedValue(new Error('PHP security API unavailable'))
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    const pools = [{ name: 'example.com', version: '8.3' }] as PHPPool[]

    render(
      <QueryClientProvider client={client}>
        <SecurityTab selectedVersion="8.3" pools={pools} />
      </QueryClientProvider>,
    )

    expect(await screen.findByText('Could not load PHP security information.')).toBeInTheDocument()
    expect(screen.getByText('PHP security API unavailable')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /retry/i })).toBeInTheDocument()
    expect(screen.queryByText('Security Assessment')).not.toBeInTheDocument()
  })
})
