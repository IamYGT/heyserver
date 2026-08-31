import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '@/lib/api'
import { RemoteCron } from './RemoteCron'

vi.mock('@/lib/api', () => ({ api: { get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() } }))

function renderCron({ online = true, readAvailable = true, writeAvailable = false, runAvailable = false } = {}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(<QueryClientProvider client={client}><RemoteCron nodeID="contabo" online={online} readAvailable={readAvailable} writeAvailable={writeAvailable} runAvailable={runAvailable} /></QueryClientProvider>)
}

describe('RemoteCron', () => {
  beforeEach(() => vi.clearAllMocks())

  it('does not query or render editable cron state while offline', () => {
    renderCron({ online: false, readAvailable: true, writeAvailable: true, runAvailable: true })
    expect(screen.getByText('Managed node is offline')).toBeInTheDocument()
    expect(screen.queryByText('Add scheduled job')).not.toBeInTheDocument()
    expect(screen.queryByText('No Heyserver-managed jobs yet.')).not.toBeInTheDocument()
    expect(api.get).not.toHaveBeenCalled()
  })

  it('hides metrics, forms, and capability claims when inventory fails', async () => {
    vi.mocked(api.get).mockRejectedValue(new Error('HTTP 502: cron inventory failed'))
    renderCron()
    expect(await screen.findByText('HTTP 502: cron inventory failed')).toBeInTheDocument()
    expect(screen.queryByText('Cron service')).not.toBeInTheDocument()
    expect(screen.queryByText('Add scheduled job')).not.toBeInTheDocument()
    expect(screen.queryByText(/Cron inventory is available/)).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }))
    await waitFor(() => expect(api.get).toHaveBeenCalledTimes(2))
  })

  it('shows metrics, forms, and empty jobs after a successful inventory', async () => {
    vi.mocked(api.get).mockResolvedValue({ service: 'active', jobs: [], sources: [], revision: 'a'.repeat(64) })
    renderCron()
    expect(await screen.findByText(/Cron inventory is available/)).toBeInTheDocument()
    expect(screen.getByText('Cron service')).toBeInTheDocument()
    expect(screen.getByText('Add scheduled job')).toBeInTheDocument()
    expect(screen.getByText('No Heyserver-managed jobs yet.')).toBeInTheDocument()
  })
})
