import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '@/lib/api'
import { RemoteDomains } from './RemoteDomains'

vi.mock('@/lib/api', () => ({
  api: {
    get: vi.fn(),
    post: vi.fn(),
  },
}))

function renderDomains({ online = true, readAvailable = true, actionAvailable = false } = {}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(
    <MemoryRouter>
      <QueryClientProvider client={client}>
        <RemoteDomains nodeID="contabo" online={online} readAvailable={readAvailable} actionAvailable={actionAvailable} />
      </QueryClientProvider>
    </MemoryRouter>,
  )
}

describe('RemoteDomains', () => {
  beforeEach(() => vi.clearAllMocks())

  it('does not query or infer an empty inventory while the managed node is offline', () => {
    renderDomains({ online: false, readAvailable: true, actionAvailable: true })

    expect(screen.getByText('Managed node is offline')).toBeInTheDocument()
    expect(screen.queryByText('No Nginx-backed domains found.')).not.toBeInTheDocument()
    expect(api.get).not.toHaveBeenCalled()
  })

  it('hides counts and read-only claims when domain inventory fails', async () => {
    vi.mocked(api.get).mockRejectedValue(new Error('HTTP 502: domain inventory failed'))
    renderDomains()

    expect(await screen.findByText('HTTP 502: domain inventory failed')).toBeInTheDocument()
    expect(screen.queryByText('0 enabled')).not.toBeInTheDocument()
    expect(screen.queryByText('0 SSL')).not.toBeInTheDocument()
    expect(screen.queryByText(/Domain inventory is available/)).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Refresh domain inventory' }))
    await waitFor(() => expect(api.get).toHaveBeenCalledTimes(2))
  })

  it('shows observed zero counts only after a successful empty inventory', async () => {
    vi.mocked(api.get).mockResolvedValue([])
    renderDomains()

    expect(await screen.findByText(/Domain inventory is available/)).toBeInTheDocument()
    expect(screen.getByText('0 enabled')).toBeInTheDocument()
    expect(screen.getByText('0 SSL')).toBeInTheDocument()
    expect(screen.getByText('No Nginx-backed domains found.')).toBeInTheDocument()
  })
})
