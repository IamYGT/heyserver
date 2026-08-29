import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '@/lib/api'
import type { SnapshotStatus } from '@/lib/types'
import SnapshotSection from './SnapshotSection'

vi.mock('@/lib/api', () => ({
  api: { get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() },
}))

function renderSection() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <SnapshotSection />
    </QueryClientProvider>,
  )
}

const baseStatus: SnapshotStatus = {
  resticFound: true,
  repoInitialized: false,
  passwordSet: true,
  destination: 'gdrive',
  destinationStatus: 'healthy',
  canPurgeRepository: true,
  driveConnected: true,
  settings: {
    destination: 'gdrive',
    repoFolder: 'hserver-snapshots',
    enabledPaths: [],
    keepDaily: 14,
    keepWeekly: 8,
    keepMonthly: 6,
    passwordAcknowledged: true,
  },
  manifest: [],
  lastSnapshots: [],
}

function mockStatus(status: SnapshotStatus, vhosts: string[] = []) {
  vi.mocked(api.get).mockImplementation((path) => {
    if (path === '/backups/snapshot/status') return Promise.resolve(status)
    if (path === '/system/stats') return Promise.resolve({ disk: [] })
    if (path === '/backups/snapshot/vhosts') return Promise.resolve({ vhosts })
    return Promise.reject(new Error(`Unexpected GET ${path}`))
  })
}

describe('Snapshot dependency states', () => {
  beforeEach(() => vi.clearAllMocks())

  it('shows recovery guidance when snapshot status detection fails', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/backups/snapshot/status') return Promise.reject(new Error('snapshot API unavailable'))
      if (path === '/system/stats') return Promise.resolve({ disk: [] })
      if (path === '/backups/snapshot/vhosts') return Promise.resolve({ vhosts: [] })
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })

    renderSection()

    expect(await screen.findByText('Snapshot bağımlılık durumu alınamadı', {}, { timeout: 3000 })).toBeInTheDocument()
    expect(screen.getByText('snapshot API unavailable')).toBeInTheDocument()
    expect(screen.getByText('restic version')).toBeInTheDocument()
  })

  it('keeps snapshot actions disabled and explains a missing restic binary', async () => {
    mockStatus({ ...baseStatus, resticFound: false })

    renderSection()

    expect(await screen.findByText('restic kurulumu gerekli')).toBeInTheDocument()
    expect(screen.getByText('HSERVER_RESTIC_BIN')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Snapshot al (artımlı)' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Günlük 04:00 zamanla' })).toBeDisabled()
  })

  it('requires an installation-owned encryption password before snapshots can run', async () => {
    mockStatus({ ...baseStatus, passwordSet: false })

    renderSection()

    expect(await screen.findByText('Snapshot şifreleme parolası gerekli')).toBeInTheDocument()
    expect(screen.getByText('HSERVER_RESTIC_PASSWORD')).toBeInTheDocument()
    expect(screen.getByText(/HServer bu parolayı gösteremez veya kurtaramaz/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Snapshot al (artımlı)' })).toBeDisabled()
  })

  it('schedules the persisted snapshot retention policy instead of a hardcoded value', async () => {
    mockStatus({
      ...baseStatus,
      settings: { ...baseStatus.settings, keepDaily: 21 },
    })
    vi.mocked(api.post).mockResolvedValue({})

    renderSection()

    fireEvent.click(await screen.findByRole('button', { name: 'Günlük 04:00 zamanla' }))
    await waitFor(() => {
      expect(api.post).toHaveBeenCalledWith('/backups/schedules', {
        frequency: 'daily',
        time: '04:00',
        retention_count: 21,
        type: 'snapshot',
      })
    })
  })

  it('saves a complete snapshot policy instead of a partial mutation', async () => {
    mockStatus({
      ...baseStatus,
      settings: { ...baseStatus.settings, keepDaily: 21 },
    })
    vi.mocked(api.put).mockResolvedValue({ success: true })

    renderSection()

    fireEvent.click(await screen.findByRole('button', { name: 'Retention kaydet' }))
    await waitFor(() => {
      expect(api.put).toHaveBeenCalledWith('/backups/snapshot/settings', {
        destination: 'gdrive',
        repoFolder: 'hserver-snapshots',
        enabledPaths: [],
        keepDaily: 21,
        keepWeekly: 8,
        keepMonthly: 6,
        passwordAcknowledged: true,
      })
    })
  })

  it('uses a provider-neutral vhost selector for snapshot restore', async () => {
    const status = {
      ...baseStatus,
      repoInitialized: true,
      manifest: [{ id: 'vhosts' as const, path: '/srv/www', label: 'Web sites', enabled: true, available: true, required: true }],
      lastSnapshots: [{
        id: 'abcdef1234567890',
        time: '2026-08-27T05:00:00Z',
        hostname: 'server.example.com',
        paths: 4,
      }],
    }
    mockStatus(status, ['example.com'])
    vi.mocked(api.post).mockResolvedValue({ jobId: 'snapshot-restore-job' })

    renderSection()
    expect(await screen.findByText('/srv/www (tüm siteler)')).toBeInTheDocument()

    fireEvent.change(screen.getByLabelText('Tek site (vhost)'), { target: { value: 'example.com' } })
    fireEvent.click(await screen.findByRole('button', { name: 'Geri yükle' }))
    fireEvent.click(await screen.findByRole('button', { name: 'Onayla ve başlat' }))

    await waitFor(() => {
      expect(api.post).toHaveBeenCalledWith('/backups/snapshot/restore', {
        snapshotId: 'abcdef1234567890',
        vhosts: ['example.com'],
      })
    })
  })

  it('requires the observed repository identity before a destructive reset', async () => {
    mockStatus({
      ...baseStatus,
      repoInitialized: true,
      repoStats: { snapshotCount: 3, totalSize: 1024, totalFileSize: 4096 },
    })
    vi.mocked(api.post).mockResolvedValue({ success: true })

    renderSection()

    fireEvent.click(await screen.findByRole('button', { name: 'Snapshot deposunu sıfırla' }))
    const confirmButton = screen.getByRole('button', { name: 'Depoyu kalıcı olarak sil' })
    expect(confirmButton).toBeDisabled()
    fireEvent.change(screen.getByLabelText('Onay için depo yolunu yazın'), {
      target: { value: 'hserver-snapshots' },
    })
    expect(confirmButton).toBeEnabled()
    fireEvent.click(confirmButton)

    await waitFor(() => expect(api.post).toHaveBeenCalledWith('/backups/snapshot/purge-repo', {
      repoFolder: 'hserver-snapshots',
      confirmation: 'purge-snapshot-repository',
    }))
  })

  it('switches destinations with a complete policy and explains protected S3 credential files', async () => {
    mockStatus({
      ...baseStatus,
      destination: 's3',
      destinationStatus: 'not_configured',
      destinationMessage: 'S3-compatible destination is not configured',
      canPurgeRepository: false,
      settings: { ...baseStatus.settings, destination: 's3' },
    })
    vi.mocked(api.put).mockResolvedValue({ success: true })

    renderSection()

    expect(await screen.findByText('S3-compatible hedef yapılandırılmadı')).toBeInTheDocument()
    expect(screen.getByText('HSERVER_S3_ACCESS_KEY_FILE')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Snapshot deposunu sıfırla' })).not.toBeInTheDocument()

    fireEvent.change(screen.getByLabelText('Snapshot hedefi'), { target: { value: 'gdrive' } })
    await waitFor(() => expect(api.put).toHaveBeenCalledWith('/backups/snapshot/settings', {
      destination: 'gdrive',
      repoFolder: 'hserver-snapshots',
      enabledPaths: [],
      keepDaily: 14,
      keepWeekly: 8,
      keepMonthly: 6,
      passwordAcknowledged: true,
    }))
  })
})
