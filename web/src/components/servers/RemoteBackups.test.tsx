import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '@/lib/api'
import { RemoteBackups } from './RemoteBackups'

vi.mock('@/lib/api', () => ({ api: { get: vi.fn(), post: vi.fn() } }))

function renderBackups({ online = true, readAvailable = true, runAvailable = false } = {}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(<QueryClientProvider client={client}><RemoteBackups nodeID="contabo" online={online} readAvailable={readAvailable} runAvailable={runAvailable} /></QueryClientProvider>)
}

describe('RemoteBackups', () => {
  beforeEach(() => vi.clearAllMocks())

  it('does not query or infer empty backup plans while offline', () => {
    renderBackups({ online: false, readAvailable: true, runAvailable: true })
    expect(screen.getByText('Managed node is offline')).toBeInTheDocument()
    expect(screen.queryByText('No backup plans are configured on this agent.')).not.toBeInTheDocument()
    expect(api.get).not.toHaveBeenCalled()
  })

  it('hides the read-only and verified-empty claims when inventory fails', async () => {
    vi.mocked(api.get).mockRejectedValue(new Error('HTTP 502: backup inventory failed'))
    renderBackups()
    expect(await screen.findByText('HTTP 502: backup inventory failed')).toBeInTheDocument()
    expect(screen.queryByText(/Backup inventory is available/)).not.toBeInTheDocument()
    expect(screen.queryByText('No backup plans are configured on this agent.')).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Refresh all' }))
    await waitFor(() => expect(api.get).toHaveBeenCalledTimes(2))
  })

  it('shows the read-only and empty states only after a successful response', async () => {
    vi.mocked(api.get).mockResolvedValue([])
    renderBackups()
    expect(await screen.findByText(/Backup inventory is available/)).toBeInTheDocument()
    expect(screen.getByText('No backup plans are configured on this agent.')).toBeInTheDocument()
  })
})
