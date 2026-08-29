import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '@/lib/api'
import { RemotePM2 } from './RemotePM2'

vi.mock('@/lib/api', () => ({ api: { get: vi.fn(), post: vi.fn() } }))

function renderPM2({ online = true, readAvailable = true } = {}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(<MemoryRouter><QueryClientProvider client={client}><RemotePM2 nodeID="contabo" online={online} readAvailable={readAvailable} actionAvailable terminalAvailable /></QueryClientProvider></MemoryRouter>)
}

describe('RemotePM2', () => {
  beforeEach(() => vi.clearAllMocks())

  it('does not query or claim PM2 availability while the node is offline', () => {
    renderPM2({ online: false, readAvailable: true })
    expect(screen.getByText('Managed node is offline')).toBeInTheDocument()
    expect(screen.queryByText(/PM2 is available/)).not.toBeInTheDocument()
    expect(api.get).not.toHaveBeenCalled()
  })

  it('keeps the verified-empty message hidden when inventory fails', async () => {
    vi.mocked(api.get).mockRejectedValue(new Error('HTTP 503: PM2 inventory unavailable'))
    renderPM2()
    expect(await screen.findByText('HTTP 503: PM2 inventory unavailable')).toBeInTheDocument()
    expect(screen.queryByText(/PM2 is available/)).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }))
    await waitFor(() => expect(api.get).toHaveBeenCalledTimes(2))
  })

  it('describes an empty PM2 inventory only after a successful response', async () => {
    vi.mocked(api.get).mockResolvedValue([])
    renderPM2()
    expect(await screen.findByText(/PM2 is available, but no applications are registered/)).toBeInTheDocument()
  })
})
