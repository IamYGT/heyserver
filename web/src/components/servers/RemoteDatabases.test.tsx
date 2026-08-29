import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '@/lib/api'
import { RemoteDatabases } from './RemoteDatabases'

vi.mock('@/lib/api', () => ({ api: { get: vi.fn(), post: vi.fn() } }))

function renderDatabases({ online = true, readAvailable = true, actionAvailable = false } = {}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(<QueryClientProvider client={client}><RemoteDatabases nodeID="contabo" online={online} readAvailable={readAvailable} actionAvailable={actionAvailable} onBackups={vi.fn()} /></QueryClientProvider>)
}

describe('RemoteDatabases', () => {
  beforeEach(() => vi.clearAllMocks())

  it('does not query or infer an empty database inventory while offline', () => {
    renderDatabases({ online: false, readAvailable: true, actionAvailable: true })
    expect(screen.getByText('Managed node is offline')).toBeInTheDocument()
    expect(screen.queryByText('No supported database engine detected.')).not.toBeInTheDocument()
    expect(api.get).not.toHaveBeenCalled()
  })

  it('hides availability and empty claims when database inventory fails', async () => {
    vi.mocked(api.get).mockRejectedValue(new Error('HTTP 502: database inventory failed'))
    renderDatabases()
    expect(await screen.findByText('HTTP 502: database inventory failed')).toBeInTheDocument()
    expect(screen.queryByText(/Database inventory is available/)).not.toBeInTheDocument()
    expect(screen.queryByText('No supported database engine detected.')).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Retry inventory' }))
    await waitFor(() => expect(api.get).toHaveBeenCalledTimes(2))
  })

  it('shows read-only and empty states only after a successful response', async () => {
    vi.mocked(api.get).mockResolvedValue([])
    renderDatabases()
    expect(await screen.findByText(/Database inventory is available/)).toBeInTheDocument()
    expect(screen.getByText('No supported database engine detected.')).toBeInTheDocument()
  })
})
