import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { api } from '@/lib/api'
import type { Domain } from '@/lib/types'
import { LogsTab } from './DomainDetail'

vi.mock('@/lib/api', () => ({
  api: { get: vi.fn() },
}))

function renderLogs() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const domain = { name: 'example.com' } as Domain
  return render(
    <QueryClientProvider client={client}>
      <LogsTab domain={domain} />
    </QueryClientProvider>,
  )
}

describe('domain log fallback', () => {
  it('labels a successful fallback to the general nginx log', async () => {
    vi.mocked(api.get)
      .mockRejectedValueOnce(new Error('domain log missing'))
      .mockResolvedValueOnce({ lines: ['fallback request'] })

    renderLogs()

    expect(await screen.findByText('The domain-specific log is unavailable; showing the general nginx access log.')).toBeInTheDocument()
    expect(screen.getByText('fallback request')).toBeInTheDocument()
    expect(screen.getByText('/var/log/nginx/access.log')).toBeInTheDocument()
  })

  it('shows a retryable failure when both log sources fail', async () => {
    vi.mocked(api.get).mockRejectedValue(new Error('log source unavailable'))

    renderLogs()

    expect(await screen.findByText('Neither the domain-specific nor general nginx log could be loaded.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /retry/i })).toBeInTheDocument()
    expect(screen.queryByText('Log file is empty')).not.toBeInTheDocument()
  })
})
