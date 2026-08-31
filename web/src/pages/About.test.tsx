import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import About from './About'

const mocks = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  toastSuccess: vi.fn(),
  toastError: vi.fn(),
}))

vi.mock('@/lib/api', () => ({
  api: { get: mocks.get, post: mocks.post },
}))

vi.mock('@/hooks/useAuth', () => ({
  useCurrentUser: () => ({ data: { id: 1, name: 'Admin', email: 'admin@example.com', role: 'admin' } }),
}))

vi.mock('sonner', () => ({
  toast: { success: mocks.toastSuccess, error: mocks.toastError },
}))

const update = {
  status: 'healthy',
  signature_status: 'verified',
  current_version: 'v1.2.3',
  latest_version: 'v1.3.0',
  latest_version_state: 'ahead',
  update_available: true,
  platform: 'linux_amd64',
  artifact: {
    url: 'https://releases.example.com/hserver-panel-v1.3.0-linux-amd64.tar.gz',
    sha256: '0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef',
    size_bytes: 1234,
  },
  message: 'A newer Heyserver release is available.',
  checked_at: '2026-08-26T18:00:00Z',
}

const staged = {
  id: 'v1.3.0-0123456789ab',
  version: 'v1.3.0',
  current_version: 'v1.2.3',
  platform: 'linux_amd64',
  sha256: update.artifact.sha256,
  size_bytes: 1234,
  status: 'staged',
  status_detail: 'Release archive verified and ready for admin confirmation.',
  created_at: '2026-08-26T18:01:00Z',
  updated_at: '2026-08-26T18:01:00Z',
}

function renderAbout() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <About />
    </QueryClientProvider>,
  )
}

function configureGet(stage: typeof staged | null = null, release = update) {
  mocks.get.mockImplementation((path: string) => {
    switch (path) {
      case '/system/info':
        return Promise.resolve({ panel_version: 'v1.2.3', hostname: 'test-host' })
      case '/health':
        return Promise.resolve({ status: 'ok', version: 'v1.2.3', uptime: 60 })
      case '/system/update':
        return Promise.resolve(release)
      case '/system/update/stage':
        return Promise.resolve({ stage })
      default:
        return Promise.reject(new Error(`unexpected GET ${path}`))
    }
  })
}

describe('About release upgrade flow', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    configureGet()
    mocks.post.mockImplementation((path: string) => {
      if (path === '/system/update/stage') return Promise.resolve(staged)
      if (path === '/system/update/install') return Promise.resolve({ ...staged, status: 'scheduled' })
      return Promise.reject(new Error(`unexpected POST ${path}`))
    })
  })

  it('stages first and sends exact second-confirmation payload before restart', async () => {
    renderAbout()

    fireEvent.click(await screen.findByRole('button', { name: /stage & verify/i }))
    expect(await screen.findByText('Archive verified and ready')).toBeInTheDocument()

    const confirmation = screen.getByRole('checkbox', { name: /I understand that Heyserver will restart/i })
    fireEvent.click(confirmation)
    fireEvent.click(screen.getByRole('button', { name: /install verified release/i }))

    await waitFor(() => {
      expect(mocks.post).toHaveBeenCalledWith('/system/update/install', {
        stage_id: staged.id,
        version: staged.version,
        confirmed: true,
      })
    })
    expect(window.confirm).toHaveBeenCalledWith('Install v1.3.0 now? Heyserver will restart.')
  })

  it('shows the server recovery detail for an interrupted upgrade', async () => {
    configureGet({
      ...staged,
      status: 'failed',
      status_detail: 'Detached upgrade unit ended before writing a terminal result; the operation was interrupted.',
    })
    renderAbout()

    expect(await screen.findByText(/operation was interrupted/i)).toBeInTheDocument()
  })

  it('keeps unsigned release discovery visible but sends no staging request', async () => {
    configureGet(null, { ...update, signature_status: 'not_configured' })
    renderAbout()

    expect(await screen.findByText('Checksum only (signing key not configured)')).toBeInTheDocument()
    expect(screen.getByText('Signed manifest required for installation')).toBeInTheDocument()
    const stageButton = screen.getByRole('button', { name: /stage & verify/i })
    expect(stageButton).toBeDisabled()
    fireEvent.click(stageButton)

    expect(mocks.post).not.toHaveBeenCalled()
  })

  it('keeps a staged release read-only until the current manifest is verified', async () => {
    configureGet(staged, { ...update, signature_status: 'unavailable' })
    renderAbout()

    expect(await screen.findByText('Signature verification unavailable')).toBeInTheDocument()
    const confirmation = screen.getByRole('checkbox', { name: /I understand that Heyserver will restart/i })
    fireEvent.click(confirmation)
    const installButton = screen.getByRole('button', { name: /install verified release/i })
    expect(installButton).toBeDisabled()
    fireEvent.click(installButton)

    expect(mocks.post).not.toHaveBeenCalled()
  })
})
