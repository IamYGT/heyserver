import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '@/lib/api'
import { RemoteNginx } from './RemoteNginx'

vi.mock('@/lib/api', () => ({ api: { get: vi.fn(), post: vi.fn(), put: vi.fn() } }))

function renderNginx({ online = true, configReadAvailable = true } = {}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(<QueryClientProvider client={client}><RemoteNginx nodeID="contabo" online={online} actionAvailable configReadAvailable={configReadAvailable} configWriteAvailable /></QueryClientProvider>)
}

describe('RemoteNginx', () => {
  beforeEach(() => vi.clearAllMocks())

  it('does not query or render config actions while the node is offline', () => {
    renderNginx({ online: false, configReadAvailable: true })
    expect(screen.getByText('Managed node is offline')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Test' })).not.toBeInTheDocument()
    expect(api.get).not.toHaveBeenCalled()
  })

  it('keeps editor actions hidden and offers retry when inventory fails', async () => {
    vi.mocked(api.get).mockRejectedValue(new Error('HTTP 502: Nginx inventory failed'))
    renderNginx()
    expect(await screen.findByText('HTTP 502: Nginx inventory failed')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Test' })).not.toBeInTheDocument()
    expect(screen.queryByText('Select a configuration')).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Retry inventory' }))
    await waitFor(() => expect(api.get).toHaveBeenCalledTimes(2))
  })

  it('describes a successful empty sites directory without opening the editor', async () => {
    vi.mocked(api.get).mockResolvedValue([])
    renderNginx()
    expect(await screen.findByText('No Nginx site configurations found.')).toBeInTheDocument()
    expect(screen.getByText(/successful empty inventory/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Test' })).not.toBeInTheDocument()
  })
})
