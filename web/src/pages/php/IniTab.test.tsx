import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { api } from '@/lib/api'
import IniTab from './IniTab'

vi.mock('@/lib/api', () => ({
  api: {
    get: vi.fn(),
    put: vi.fn(),
  },
}))

describe('PHP IniTab failure states', () => {
  it('keeps an unavailable php.ini request distinct from an empty directive list', async () => {
    vi.mocked(api.get).mockRejectedValue(new Error('php.ini API unavailable'))
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })

    render(
      <QueryClientProvider client={client}>
        <IniTab
          selectedVersion="8.3"
          selectedDomain="example.com"
          domains={[{ version: '8.3', name: 'example.com' }]}
        />
      </QueryClientProvider>,
    )

    expect(await screen.findByText('php.ini directives could not be loaded.')).toBeInTheDocument()
    expect(screen.getByText('php.ini API unavailable')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /retry/i })).toBeInTheDocument()
  })
})
