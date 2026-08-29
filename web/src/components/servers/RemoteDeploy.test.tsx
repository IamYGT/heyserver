import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '@/lib/api'
import { RemoteDeploy } from './RemoteDeploy'

vi.mock('@/lib/api', () => ({
  api: {
    get: vi.fn(),
    post: vi.fn(),
  },
}))

function renderDeploy({ online = true, readAvailable = true, actionAvailable = false } = {}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(
    <MemoryRouter>
      <QueryClientProvider client={client}>
        <RemoteDeploy
          nodeID="contabo"
          online={online}
          terminalAvailable
          readAvailable={readAvailable}
          actionAvailable={actionAvailable}
          domainReadAvailable
          domainActionAvailable={false}
        />
      </QueryClientProvider>
    </MemoryRouter>,
  )
}

describe('RemoteDeploy', () => {
  beforeEach(() => vi.clearAllMocks())

  it('does not invent empty deploy state while the managed node is offline', () => {
    renderDeploy({ online: false, readAvailable: true, actionAvailable: true })

    expect(screen.getByText('Managed node is offline')).toBeInTheDocument()
    expect(screen.queryByText('No local deploy plans are configured on this agent.')).not.toBeInTheDocument()
    expect(screen.queryByText('No persistent deploy jobs recorded yet.')).not.toBeInTheDocument()
    expect(screen.queryByText('No deploy activity recorded yet.')).not.toBeInTheDocument()
    expect(api.get).not.toHaveBeenCalled()
  })

  it('keeps summary counts and read-only claims hidden when inventory fails', async () => {
    vi.mocked(api.get).mockRejectedValue(new Error('HTTP 502: deploy inventory failed'))
    renderDeploy()

    expect(await screen.findByText('Could not load deploy targets')).toBeInTheDocument()
    expect(screen.getAllByText('HTTP 502: deploy inventory failed').length).toBeGreaterThan(0)
    expect(screen.queryByText('Targets')).not.toBeInTheDocument()
    expect(screen.queryByText(/Deploy inventory is available/)).not.toBeInTheDocument()

    fireEvent.click(screen.getAllByRole('button', { name: 'Retry' })[0])
    await waitFor(() => expect(api.get).toHaveBeenCalledTimes(4))
  })

  it('shows zero counts and the read-only boundary only after a successful empty inventory', async () => {
    vi.mocked(api.get).mockImplementation(async (path: string) => {
      if (path === '/nodes/contabo/deploy') return []
      if (path === '/nodes/contabo/deploy/jobs') return []
      if (path.startsWith('/audit?')) return { data: [], total: 0 }
      throw new Error(`unexpected GET ${path}`)
    })
    renderDeploy()

    expect(await screen.findByText(/Deploy inventory is available/)).toBeInTheDocument()
    expect(screen.getByText('No local deploy plans are configured on this agent.')).toBeInTheDocument()
    expect(screen.getByText('Targets')).toBeInTheDocument()
    expect(screen.getAllByText('0')).toHaveLength(3)
  })
})
