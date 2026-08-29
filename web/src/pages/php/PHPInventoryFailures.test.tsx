import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '@/lib/api'
import PoolsTab from './PoolsTab'
import ExtensionsTab from './ExtensionsTab'

vi.mock('@/lib/api', () => ({
  api: { get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() },
}))

function renderWithQueryClient(node: React.ReactNode) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(<QueryClientProvider client={client}>{node}</QueryClientProvider>)
}

describe('PHP child inventory failures', () => {
  beforeEach(() => vi.clearAllMocks())

  it('pauses pool controls when pool inventory is unavailable', async () => {
    vi.mocked(api.get).mockRejectedValue(new Error('Pool API unavailable'))

    renderWithQueryClient(<PoolsTab selectedVersion="8.4" versions={[{ version: '8.4', active: true, info: '', pool_dir: '', pool_count: 0 }]} />)

    expect(await screen.findByText('PHP pools could not be loaded. Mutating controls are paused.')).toBeInTheDocument()
    expect(screen.getByText('Pool API unavailable')).toBeInTheDocument()
    expect(screen.queryByText('No pools configured')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Create Pool' })).not.toBeInTheDocument()
  })

  it('pauses extension controls when extension inventory is unavailable', async () => {
    vi.mocked(api.get).mockRejectedValue(new Error('Extension API unavailable'))

    renderWithQueryClient(<ExtensionsTab selectedVersion="8.4" />)

    expect(await screen.findByText('PHP extensions could not be loaded. Extension controls are paused.')).toBeInTheDocument()
    expect(screen.getByText('Extension API unavailable')).toBeInTheDocument()
    expect(screen.queryByText('0/0 enabled')).not.toBeInTheDocument()
  })
})
