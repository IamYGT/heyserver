import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '@/lib/api'
import type { Domain } from '@/lib/types'
import { OverviewTab } from './DomainDetail'

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

describe('domain overview actions', () => {
  beforeEach(() => vi.clearAllMocks())

  it('uses the registered toggle route to disable an active domain', async () => {
    vi.mocked(api.post).mockResolvedValue({ domain: 'example.com', active: false })
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    })

    render(
      <QueryClientProvider client={client}>
        <OverviewTab domain={domain} />
      </QueryClientProvider>,
    )

    fireEvent.click(screen.getByRole('button', { name: 'Disable domain' }))

    await waitFor(() => {
      expect(api.post).toHaveBeenCalledWith('/domains/domain-1/toggle', { active: false })
    })
    expect(api.put).not.toHaveBeenCalled()
  })
})
