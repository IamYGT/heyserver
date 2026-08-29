import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '@/lib/api'
import { RemoteFiles } from './RemoteFiles'

vi.mock('@/lib/api', () => ({ api: { get: vi.fn(), put: vi.fn() } }))

function renderFiles({ online = true, readAvailable = true, writeAvailable = false } = {}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(<QueryClientProvider client={client}><RemoteFiles nodeID="contabo" online={online} readAvailable={readAvailable} writeAvailable={writeAvailable} readRoots={['/srv/sites']} writeRoots={[]} /></QueryClientProvider>)
}

describe('RemoteFiles', () => {
  beforeEach(() => vi.clearAllMocks())

  it('does not query or infer an empty directory while the node is offline', () => {
    renderFiles({ online: false, readAvailable: true, writeAvailable: true })
    expect(screen.getByText('Managed node is offline')).toBeInTheDocument()
    expect(screen.queryByText('Directory is empty.')).not.toBeInTheDocument()
    expect(api.get).not.toHaveBeenCalled()
  })

  it('hides browsing and file-selection claims when directory inventory fails', async () => {
    vi.mocked(api.get).mockRejectedValue(new Error('HTTP 502: directory inventory failed'))
    renderFiles()
    expect(await screen.findByText('HTTP 502: directory inventory failed')).toBeInTheDocument()
    expect(screen.getByText('Directory inventory is unavailable.')).toBeInTheDocument()
    expect(screen.queryByText(/File browsing is available/)).not.toBeInTheDocument()
    expect(screen.queryByText('Select a text file to inspect or edit.')).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Refresh directory inventory' }))
    await waitFor(() => expect(api.get).toHaveBeenCalledTimes(2))
  })

  it('shows read-only browsing and an empty directory only after success', async () => {
    vi.mocked(api.get).mockResolvedValue([])
    renderFiles()
    expect(await screen.findByText(/File browsing is available/)).toBeInTheDocument()
    expect(screen.getByText('Directory is empty.')).toBeInTheDocument()
    expect(screen.getByText('Select a text file to inspect or edit.')).toBeInTheDocument()
  })
})
