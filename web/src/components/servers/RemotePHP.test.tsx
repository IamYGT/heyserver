import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '@/lib/api'
import { RemotePHP } from './RemotePHP'

vi.mock('@/lib/api', () => ({ api: { get: vi.fn(), post: vi.fn(), put: vi.fn() } }))

function renderPHP({ online = true, configReadAvailable = true } = {}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(<QueryClientProvider client={client}><RemotePHP nodeID="contabo" online={online} configReadAvailable={configReadAvailable} configWriteAvailable actionAvailable /></QueryClientProvider>)
}

describe('RemotePHP', () => {
  beforeEach(() => vi.clearAllMocks())

  it('does not query or render pool actions while the node is offline', () => {
    renderPHP({ online: false, configReadAvailable: true })
    expect(screen.getByText('Managed node is offline')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Test' })).not.toBeInTheDocument()
    expect(api.get).not.toHaveBeenCalled()
  })

  it('keeps pool actions hidden and offers retry when inventory fails', async () => {
    vi.mocked(api.get).mockRejectedValue(new Error('HTTP 502: PHP inventory failed'))
    renderPHP()
    expect(await screen.findByText('HTTP 502: PHP inventory failed')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Test' })).not.toBeInTheDocument()
    expect(screen.queryByText('Select a PHP pool')).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Retry inventory' }))
    await waitFor(() => expect(api.get).toHaveBeenCalledTimes(2))
  })

  it('describes a successful empty runtime inventory without opening the editor', async () => {
    vi.mocked(api.get).mockResolvedValue([])
    renderPHP()
    expect(await screen.findByText('No PHP-FPM runtimes found.')).toBeInTheDocument()
    expect(screen.getByText(/successful empty runtime inventory/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Test' })).not.toBeInTheDocument()
  })
})
