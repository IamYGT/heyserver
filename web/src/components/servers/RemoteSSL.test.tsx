import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '@/lib/api'
import { RemoteSSL } from './RemoteSSL'

vi.mock('@/lib/api', () => ({
  api: {
    get: vi.fn(),
    post: vi.fn(),
  },
}))

function renderSSL({ online = true, readAvailable = true, actionAvailable = false } = {}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(<QueryClientProvider client={client}><RemoteSSL nodeID="contabo" online={online} readAvailable={readAvailable} actionAvailable={actionAvailable} /></QueryClientProvider>)
}

describe('RemoteSSL', () => {
  beforeEach(() => vi.clearAllMocks())

  it('does not query or infer an empty certificate inventory while offline', () => {
    renderSSL({ online: false, readAvailable: true, actionAvailable: true })

    expect(screen.getByText('Managed node is offline')).toBeInTheDocument()
    expect(screen.queryByText('No Certbot certificates found.')).not.toBeInTheDocument()
    expect(api.get).not.toHaveBeenCalled()
  })

  it('hides certificate counts and read-only claims when inventory fails', async () => {
    vi.mocked(api.get).mockRejectedValue(new Error('HTTP 502: certificate inventory failed'))
    renderSSL()

    expect(await screen.findByText('HTTP 502: certificate inventory failed')).toBeInTheDocument()
    expect(screen.queryByText('0 certificates · 0 expiring')).not.toBeInTheDocument()
    expect(screen.queryByText(/Certificate inventory is available/)).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Refresh certificate inventory' }))
    await waitFor(() => expect(api.get).toHaveBeenCalledTimes(2))
  })

  it('shows zero counts and the read-only boundary after a successful empty inventory', async () => {
    vi.mocked(api.get).mockResolvedValue([])
    renderSSL()

    expect(await screen.findByText(/Certificate inventory is available/)).toBeInTheDocument()
    expect(screen.getByText('0 certificates · 0 expiring')).toBeInTheDocument()
    expect(screen.getByText('No Certbot certificates found.')).toBeInTheDocument()
  })
})
