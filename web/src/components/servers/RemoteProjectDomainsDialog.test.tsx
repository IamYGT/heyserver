import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '@/lib/api'
import { RemoteProjectDomainsDialog } from './RemoteProjectDomainsDialog'

const toastMocks = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }))

vi.mock('sonner', () => ({ toast: toastMocks }))

vi.mock('@/lib/api', () => ({
  api: {
    get: vi.fn(),
    post: vi.fn(),
    put: vi.fn(),
    delete: vi.fn(),
  },
}))

function renderDialog({ online = true, readAvailable = true, actionAvailable = true } = {}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <RemoteProjectDomainsDialog
        nodeID="contabo-1"
        target={{ id: 'example-app', name: 'Example app', host_port: 8080 }}
        open
        onOpenChange={() => undefined}
        online={online}
        readAvailable={readAvailable}
        actionAvailable={actionAvailable}
      />
    </QueryClientProvider>,
  )
}

describe('RemoteProjectDomainsDialog', () => {
  beforeEach(() => vi.clearAllMocks())
  afterEach(() => vi.restoreAllMocks())

  it('does not query or render mutation fields while the managed node is offline', () => {
    renderDialog({ online: false, readAvailable: true, actionAvailable: true })
    expect(screen.getByText('The managed server is offline.')).toBeInTheDocument()
    expect(screen.queryByLabelText('Domain')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Ensure domain' })).not.toBeInTheDocument()
    expect(api.get).not.toHaveBeenCalled()
  })

  it('keeps mutation fields hidden and offers retry when inventory fails', async () => {
    vi.mocked(api.get).mockRejectedValue(new Error('HTTP 502: project domain inventory failed'))
    renderDialog()
    expect(await screen.findByText('Remote project domain inventory is temporarily unavailable.')).toBeInTheDocument()
    expect(screen.queryByLabelText('Domain')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Ensure domain' })).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Retry inventory' }))
    await waitFor(() => expect(api.get).toHaveBeenCalledTimes(2))
  })

  it('manages remote domains without accepting an upstream from browser input', async () => {
    const observed = {
      target_id: 'example-app', domain: 'app.example.com', host_port: 8080, desired_host_port: 8080,
      upstream: 'http://127.0.0.1:8080', status: 'active', message: 'Observed on managed node',
      tls_status: 'not_configured', tls_message: 'TLS is not configured',
    }
    vi.mocked(api.get).mockImplementation(async (path: string) => {
      if (path === '/nodes/contabo-1/deploy/example-app/domains') return [observed]
      if (path === '/nodes/contabo-1/deploy/example-app/domains/app.example.com/health') {
        return { domain: 'app.example.com', upstream: 'http://127.0.0.1:8080', status: 'healthy', status_code: 204, latency_ms: 3, message: 'Loopback healthy', checked_at: '2026-08-26T23:00:00Z' }
      }
      throw new Error(`unexpected GET ${path}`)
    })
    vi.mocked(api.put).mockResolvedValue({ changed: true, observation: { ...observed, enabled: true, revision: '2'.repeat(64) } })
    vi.mocked(api.post).mockImplementation(async (path: string) => {
      if (path.endsWith('/tls')) return { ...observed, tls_status: 'healthy', tls_message: 'valid' }
      return observed
    })
    vi.spyOn(window, 'confirm').mockReturnValue(true)

    renderDialog()

    expect(await screen.findByText('app.example.com')).toBeInTheDocument()
    expect(screen.getByText('Declared loopback: http://127.0.0.1:8080')).toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('Domain'), { target: { value: 'new.example.com' } })
    fireEvent.click(screen.getByRole('button', { name: 'Ensure domain' }))
    await waitFor(() => expect(api.put).toHaveBeenCalledWith('/nodes/contabo-1/deploy/example-app/domains/new.example.com', { expected_revision: 'absent', confirmed: true }))

    fireEvent.change(screen.getByLabelText('ACME email (optional)'), { target: { value: 'admin@example.com' } })
    fireEvent.click(screen.getByRole('button', { name: 'Enable TLS' }))
    await waitFor(() => expect(api.post).toHaveBeenCalledWith('/nodes/contabo-1/deploy/example-app/domains/app.example.com/tls', { email: 'admin@example.com' }))

    fireEvent.click(screen.getByRole('button', { name: 'Health' }))
    expect(await screen.findByText(/HEALTHY · 204 · 3 ms/)).toBeInTheDocument()
  })

  it('provisions an absent domain with the exact revision-aware ensure body', async () => {
    vi.mocked(api.get).mockResolvedValue([])
    vi.mocked(api.put).mockResolvedValue({ changed: true, observation: {} })
    const confirmMock = vi.spyOn(window, 'confirm').mockReturnValue(true)
    renderDialog()

    await screen.findByText('No HServer-owned domain mappings were observed for this target.')
    fireEvent.change(screen.getByLabelText('Domain'), { target: { value: 'new.example.com' } })
    fireEvent.click(screen.getByRole('button', { name: 'Ensure domain' }))

    await waitFor(() => expect(api.put).toHaveBeenCalledWith('/nodes/contabo-1/deploy/example-app/domains/new.example.com', {
      expected_revision: 'absent',
      confirmed: true,
    }))
    expect(confirmMock).toHaveBeenCalledTimes(1)
    expect(api.post).not.toHaveBeenCalled()
  })

  it('uses the observed revision and reports an already healthy no-op receipt', async () => {
    const observed = {
      target_id: 'example-app', domain: 'app.example.com', host_port: 8080, desired_host_port: 8080,
      upstream: 'http://127.0.0.1:8080', status: 'active' as const, message: 'Observed on managed node',
      tls_status: 'not_configured' as const, tls_message: 'TLS is not configured', enabled: true, revision: '7'.repeat(64),
    }
    vi.mocked(api.get).mockResolvedValue([observed])
    vi.mocked(api.put).mockResolvedValue({ changed: false, observation: observed })
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    renderDialog()

    expect(await screen.findByText('app.example.com')).toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('Domain'), { target: { value: 'app.example.com' } })
    fireEvent.click(screen.getByRole('button', { name: 'Ensure domain' }))

    await waitFor(() => expect(api.put).toHaveBeenCalledWith('/nodes/contabo-1/deploy/example-app/domains/app.example.com', {
      expected_revision: '7'.repeat(64),
      confirmed: true,
    }))
    expect(toastMocks.success).toHaveBeenCalledWith('Remote project domain is already enabled')
  })

  it('does not send an ensure request when confirmation is cancelled', async () => {
    vi.mocked(api.get).mockResolvedValue([])
    const confirmMock = vi.spyOn(window, 'confirm').mockReturnValue(false)
    renderDialog()

    await screen.findByText('No HServer-owned domain mappings were observed for this target.')
    fireEvent.change(screen.getByLabelText('Domain'), { target: { value: 'cancel.example.com' } })
    fireEvent.click(screen.getByRole('button', { name: 'Ensure domain' }))

    expect(confirmMock).toHaveBeenCalledTimes(1)
    expect(api.put).not.toHaveBeenCalled()
  })

  it('refreshes inventory once after a stale ensure and never retries the same revision', async () => {
    let reads = 0
    vi.mocked(api.get).mockImplementation(async () => {
      reads += 1
      return []
    })
    vi.mocked(api.put).mockRejectedValue(Object.assign(new Error('stale revision at /api/nodes/contabo-1/deploy/example-app/domains'), { status: 409 }))
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    renderDialog()

    await screen.findByText('No HServer-owned domain mappings were observed for this target.')
    fireEvent.change(screen.getByLabelText('Domain'), { target: { value: 'stale.example.com' } })
    fireEvent.click(screen.getByRole('button', { name: 'Ensure domain' }))

    await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
    await waitFor(() => expect(reads).toBe(2))
    expect(api.put).toHaveBeenCalledTimes(1)
    expect(screen.getByRole('alert')).toHaveTextContent('The remote project domain changed while you were working.')
    expect(screen.queryByText(/stale revision|\/api\/nodes|deploy\/example-app/)).not.toBeInTheDocument()
  })

  it('keeps the inventory visible but hides ensure controls when domain actions are unsupported', async () => {
    vi.mocked(api.get).mockResolvedValue([])
    renderDialog({ actionAvailable: false })

    expect(await screen.findByText(/Domain inventory is read-only because/)).toBeInTheDocument()
    expect(screen.queryByLabelText('Domain')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Ensure domain' })).not.toBeInTheDocument()
    expect(api.put).not.toHaveBeenCalled()
  })

  it('does not render raw backend error bodies for an ensure failure', async () => {
    const rawBody = '<html>proxy failure /api/nodes/contabo-1/deploy/example-app/domains command=/usr/sbin/nginx</html>'
    vi.mocked(api.get).mockResolvedValue([])
    vi.mocked(api.put).mockRejectedValue(Object.assign(new Error(rawBody), { status: 502 }))
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    renderDialog()

    await screen.findByText('No HServer-owned domain mappings were observed for this target.')
    fireEvent.change(screen.getByLabelText('Domain'), { target: { value: 'error.example.com' } })
    fireEvent.click(screen.getByRole('button', { name: 'Ensure domain' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('Remote project domain service is temporarily unavailable.')
    expect(screen.queryByText(rawBody)).not.toBeInTheDocument()
    expect(screen.queryByText(/proxy failure|\/api\/nodes|\/usr\/sbin\/nginx/)).not.toBeInTheDocument()
  })

  it.each([
    [504, 'Remote project domain inventory timed out.'],
    [503, 'Remote project domain inventory is temporarily unavailable.'],
    [501, 'This agent does not support remote project domain inventory.'],
  ] as const)('distinguishes inventory status %s without exposing backend details', async (status, message) => {
    vi.mocked(api.get).mockRejectedValue(Object.assign(new Error('backend detail /etc/nginx/sites-enabled/app.conf'), { status }))
    renderDialog()

    expect(await screen.findByText(message)).toBeInTheDocument()
    expect(screen.queryByText(/backend detail|\/etc\/nginx/)).not.toBeInTheDocument()
  })
})
