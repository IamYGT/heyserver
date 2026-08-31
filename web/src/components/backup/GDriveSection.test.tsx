import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '@/lib/api'
import GDriveSection from './GDriveSection'

vi.mock('@/lib/api', () => ({
  api: { get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() },
}))

vi.mock('@/lib/gdriveOAuth', () => ({
  openGDriveOAuthPopup: vi.fn(),
}))

function renderSection() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <GDriveSection />
    </QueryClientProvider>,
  )
}

describe('Google Drive dependency states', () => {
  beforeEach(() => vi.clearAllMocks())

  it('shows operator recovery guidance when dependency detection fails', async () => {
    vi.mocked(api.get).mockRejectedValue(new Error('backup API unavailable'))

    renderSection()

    expect(await screen.findByText('Google Drive entegrasyon durumu alınamadı')).toBeInTheDocument()
    expect(screen.getByText('backup API unavailable')).toBeInTheDocument()
    expect(screen.getByText('rclone version')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Retry detection' })).toBeEnabled()
  })

  it('keeps OAuth connection disabled until rclone is available', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/backups/gdrive/status') {
        return Promise.resolve({
          state: 'not_configured',
          connected: false,
          configured: true,
          rcloneFound: false,
          redirectUri: 'https://example.com/api/backups/gdrive/oauth/callback',
          oauthApp: {
            configured: true,
            clientId: 'public-client-id',
            clientIdMasked: 'pub...-id',
            hasSecret: true,
            redirectUri: 'https://example.com/api/backups/gdrive/oauth/callback',
            credentialsSource: 'env',
          },
          settings: {
            folder: 'hserver-backups',
            autoUpload: false,
            remoteRetentionDays: 30,
            notifyOnSuccess: true,
            notifyOnFailure: true,
            lastError: '',
          },
        })
      }
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })

    renderSection()

    expect(await screen.findByText('rclone kurulumu gerekli')).toBeInTheDocument()
    expect(screen.getByText(/Heyserver paketleri otomatik olarak kurmaz/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Google ile bağlan ve izin ver' })).toBeDisabled()
  })

  it('saves zero as an explicit disabled remote-retention policy', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/backups/gdrive/status') {
        return Promise.resolve({
          state: 'healthy',
          connected: true,
          configured: true,
          rcloneFound: true,
          email: 'operator@example.com',
          settings: {
            folder: 'hserver-backups',
            autoUpload: false,
            remoteRetentionDays: 30,
            notifyOnSuccess: true,
            notifyOnFailure: true,
          },
        })
      }
      if (path === '/backups/gdrive/remote') return Promise.resolve({ backups: [] })
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })
    vi.mocked(api.put).mockResolvedValue({ success: true })

    renderSection()

    fireEvent.click(await screen.findByRole('button', { name: 'Gelişmiş ayarlar' }))
    const retention = screen.getByRole('spinbutton')
    expect(retention).toHaveAttribute('min', '0')
    fireEvent.change(retention, { target: { value: '0' } })
    fireEvent.click(screen.getByRole('button', { name: 'Ayarları kaydet' }))

    await waitFor(() => expect(api.put).toHaveBeenCalledWith('/backups/gdrive/settings', {
      folder: 'hserver-backups',
      autoUpload: false,
      remoteRetentionDays: 0,
      notifyOnSuccess: true,
      notifyOnFailure: true,
    }))
    expect(screen.getByText(/0 uzak silmeyi kapatır/)).toBeInTheDocument()
  })

  it.each([
    { state: 'not_configured' as const, label: 'Yapılandırılmadı', connected: true, configured: false },
    { state: 'unavailable' as const, label: 'Kullanılamıyor', connected: true, configured: true },
    { state: 'healthy' as const, label: 'Bağlı', connected: false, configured: true },
  ])('renders the canonical $state state through the presentation adapter', async ({ state, label, connected, configured }) => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/backups/gdrive/status') {
        return Promise.resolve({
          state,
          connected,
          configured,
          rcloneFound: true,
          email: 'operator@example.com',
          message: state === 'unavailable' ? 'Drive probe failed' : undefined,
          settings: {
            folder: 'hserver-backups',
            autoUpload: false,
            remoteRetentionDays: 30,
            notifyOnSuccess: true,
            notifyOnFailure: true,
          },
        })
      }
      if (path === '/backups/gdrive/remote') return Promise.resolve({ backups: [] })
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })

    renderSection()

    expect(await screen.findByText(label)).toBeInTheDocument()
  })
})
