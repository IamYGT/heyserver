import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '@/lib/api'
import { RemoteAgentLifecycle, type RemoteAgentUpdateStatus } from './RemoteAgentLifecycle'

vi.mock('@/lib/api', () => ({
  api: {
    get: vi.fn(),
    post: vi.fn(),
  },
}))

const status: RemoteAgentUpdateStatus = {
  release_status: 'healthy', signature_status: 'verified', current_version: 'v1.0.0', latest_version: 'v1.2.3', latest_version_state: 'ahead',
  update_available: true, platform: 'linux_amd64', release_notes_url: 'https://releases.example.com/v1.2.3',
  release_message: 'A newer HServer release is available.', release_checked_at: '2026-08-26T23:00:00Z',
  operation: '', operation_status: 'idle', operation_detail: 'No agent lifecycle operation has been scheduled.', rollback_available: true,
}

function renderLifecycle({ online = true, readAvailable = true, actionAvailable = true } = {}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(<QueryClientProvider client={client}><RemoteAgentLifecycle nodeID="contabo-1" serverLabel="Contabo" online={online} readAvailable={readAvailable} actionAvailable={actionAvailable} /></QueryClientProvider>)
}

describe('RemoteAgentLifecycle', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(api.get).mockResolvedValue(status)
    vi.mocked(api.post).mockImplementation(async (path: string) => ({ ...status, operation: path.endsWith('/rollback') ? 'rollback' : 'upgrade', operation_status: 'scheduled', operation_version: path.endsWith('/rollback') ? '' : 'v1.2.3', operation_updated_at: '2026-08-26T23:01:00Z' }))
    vi.spyOn(window, 'confirm').mockReturnValue(true)
  })

  it('schedules upgrade and rollback without sending manifest or command input', async () => {
    renderLifecycle()
    expect(await screen.findByText('v1.2.3')).toBeInTheDocument()
    expect(screen.getByText('Ed25519 signature verified')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Upgrade to v1.2.3' }))
    await waitFor(() => expect(api.post).toHaveBeenCalledWith('/nodes/contabo-1/agent-update/upgrade', { version: 'v1.2.3', confirmed: true }))
    const upgradePayload = vi.mocked(api.post).mock.calls[0]?.[1] as Record<string, unknown>
    expect(upgradePayload).not.toHaveProperty('url')
    expect(upgradePayload).not.toHaveProperty('sha256')
    expect(upgradePayload).not.toHaveProperty('command')

    vi.mocked(api.get).mockResolvedValue(status)
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }))
    await waitFor(() => expect(screen.getByRole('button', { name: 'Rollback agent' })).not.toBeDisabled())
    fireEvent.click(screen.getByRole('button', { name: 'Rollback agent' }))
    await waitFor(() => expect(api.post).toHaveBeenCalledWith('/nodes/contabo-1/agent-update/rollback', { confirmed: true }))
  })

  it('keeps lifecycle mutations disabled when the server only advertises read capability', async () => {
    renderLifecycle({ actionAvailable: false })
    expect(await screen.findByText(/Lifecycle status is read-only/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Upgrade to v1.2.3' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Rollback agent' })).toBeDisabled()
  })

  it('keeps unsigned lifecycle discovery visible but sends no upgrade request', async () => {
    vi.mocked(api.get).mockResolvedValue({ ...status, signature_status: 'not_configured' })
    renderLifecycle()

    expect(await screen.findByText('Signed manifest required for installation')).toBeInTheDocument()
    const upgradeButton = screen.getByRole('button', { name: 'Upgrade to v1.2.3' })
    expect(upgradeButton).toBeDisabled()
    fireEvent.click(upgradeButton)

    expect(api.post).not.toHaveBeenCalled()
  })

  it('does not query or render lifecycle state while the managed server is offline', () => {
    renderLifecycle({ online: false })
    expect(screen.getByText(/selected server is offline/)).toBeInTheDocument()
    expect(screen.queryByText('Installed')).not.toBeInTheDocument()
    expect(api.get).not.toHaveBeenCalled()
  })

  it('keeps lifecycle actions hidden and offers retry when status loading fails', async () => {
    vi.mocked(api.get).mockRejectedValue(new Error('HTTP 502: agent lifecycle unavailable'))
    renderLifecycle()
    expect(await screen.findByText('HTTP 502: agent lifecycle unavailable')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Upgrade to/ })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Rollback agent' })).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Retry status' }))
    await waitFor(() => expect(api.get).toHaveBeenCalledTimes(2))
  })
})
