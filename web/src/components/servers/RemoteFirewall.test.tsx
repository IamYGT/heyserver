import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '@/lib/api'
import { RemoteFirewall } from './RemoteFirewall'

vi.mock('@/lib/api', () => ({
  api: {
    get: vi.fn(),
    post: vi.fn(),
    delete: vi.fn(),
  },
}))

function renderFirewall({ online = true, readAvailable = true, writeAvailable = false } = {}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(<QueryClientProvider client={client}><RemoteFirewall nodeID="contabo" online={online} readAvailable={readAvailable} writeAvailable={writeAvailable} /></QueryClientProvider>)
}

describe('RemoteFirewall', () => {
  beforeEach(() => vi.clearAllMocks())

  it('renders an inventory failure without inventing empty policy or rule state', async () => {
    vi.mocked(api.get).mockRejectedValue(new Error('HTTP 502: read INPUT rules: exit status 1'))
    renderFirewall()

    expect(await screen.findByText('Firewall inventory is unavailable')).toBeInTheDocument()
    expect(screen.getByText('HTTP 502: read INPUT rules: exit status 1')).toBeInTheDocument()
    expect(screen.getByText(/No empty policy, rule count, or manageable state is inferred/)).toBeInTheDocument()
    expect(screen.queryByText('Backend')).not.toBeInTheDocument()
    expect(screen.queryByText('Existing INPUT rules')).not.toBeInTheDocument()
    expect(screen.queryByText(/inventory is available, but managed rule changes/)).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Retry inventory' }))
    await waitFor(() => expect(api.get).toHaveBeenCalledTimes(2))
  })

  it('shows observed inventory before describing a read-only mutation boundary', async () => {
    vi.mocked(api.get).mockResolvedValue({
      backend: 'iptables', policy: 'DROP', persistence: 'active', revision: 'a'.repeat(64),
      protected_sources: ['192.0.2.10/32'], protected_ports: [22],
      rules: [{ id: 'system-1', action: 'ACCEPT', protocol: 'tcp', port: 22, source: '192.0.2.10/32', managed: false, raw: '-A INPUT -s 192.0.2.10/32 -p tcp --dport 22 -j ACCEPT' }],
    })
    renderFirewall()

    expect(await screen.findByText(/inventory is available, but managed rule changes are read-only/)).toBeInTheDocument()
    expect(screen.getByText('iptables')).toBeInTheDocument()
    expect(screen.getByText('DROP')).toBeInTheDocument()
    expect(screen.getByText('active')).toBeInTheDocument()
    expect(screen.getByText(/1 externally managed rule/)).toBeInTheDocument()
  })

  it('does not query the panel firewall endpoint while the managed node is offline', () => {
    renderFirewall({ online: false, readAvailable: true, writeAvailable: true })
    expect(screen.getByText('Managed node is offline')).toBeInTheDocument()
    expect(api.get).not.toHaveBeenCalled()
  })
})
