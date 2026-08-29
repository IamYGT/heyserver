import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { api } from '@/lib/api'
import Firewall from './Firewall'

vi.mock('@/lib/api', () => ({
  api: {
    get: vi.fn(),
    post: vi.fn(),
    delete: vi.fn(),
  },
}))

describe('Firewall failure state', () => {
  it('pauses mutations when current UFW state is unavailable', async () => {
    vi.mocked(api.get).mockRejectedValue(new Error('firewall API unavailable'))
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })

    render(
      <QueryClientProvider client={client}>
        <Firewall />
      </QueryClientProvider>,
    )

    expect(await screen.findByText('Firewall API is unavailable')).toBeInTheDocument()
    expect(screen.getByText('firewall API unavailable')).toBeInTheDocument()
    expect(screen.getByText('UFW unavailable')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /manage ufw/i })).toBeDisabled()
    expect(screen.getByRole('button', { name: /add rule/i })).toBeDisabled()
    expect(screen.queryByText('No rules configured')).not.toBeInTheDocument()
  })

  it('shows legacy iptables rules read-only when UFW is missing', async () => {
    vi.mocked(api.get).mockResolvedValue({
      available: false,
      state: 'ufw-missing',
      backend: 'iptables',
      error: 'ufw: command not found',
      active: true,
      defaultIncoming: 'unknown',
      defaultOutgoing: 'unknown',
      rules: [{ number: 1, to: '22', action: 'ALLOW', from: 'Anywhere', protocol: 'tcp' }],
    })
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })

    render(
      <QueryClientProvider client={client}>
        <Firewall />
      </QueryClientProvider>,
    )

    expect(await screen.findByText('UFW is not installed')).toBeInTheDocument()
    expect(screen.getByText(/Legacy iptables rules remain visible/)).toBeInTheDocument()
    expect(screen.getAllByText('22')).toHaveLength(2)
    expect(screen.getByTitle('Delete rule')).toBeDisabled()
    expect(screen.getByRole('button', { name: /manage ufw/i })).toBeDisabled()
    expect(screen.getByRole('button', { name: /add rule/i })).toBeDisabled()
  })

  it('sends explicit UFW enable intent from a healthy inactive state', async () => {
    vi.mocked(api.get).mockResolvedValue({
      available: true,
      state: 'healthy',
      backend: 'ufw',
      active: false,
      defaultIncoming: 'deny',
      defaultOutgoing: 'allow',
      rules: [],
    })
    vi.mocked(api.post).mockResolvedValue({ message: 'UFW firewall enabled' })
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })

    render(
      <QueryClientProvider client={client}>
        <Firewall />
      </QueryClientProvider>,
    )

    const enableButton = await screen.findByRole('button', { name: /enable ufw/i })
    await waitFor(() => expect(enableButton).toBeEnabled())
    enableButton.click()

    await waitFor(() => expect(api.post).toHaveBeenCalledWith('/firewall/toggle', { enable: true }))
  })
})
