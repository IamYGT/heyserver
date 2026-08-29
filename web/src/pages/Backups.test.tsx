import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api, ApiError } from '@/lib/api'
import Backups from './Backups'

vi.mock('@/lib/api', () => ({
  ApiError: class ApiError extends Error {
    status: number

    constructor(status: number, message: string) {
      super(message)
      this.status = status
    }
  },
  api: {
    get: vi.fn(),
    post: vi.fn(),
    put: vi.fn(),
    delete: vi.fn(),
  },
}))

vi.mock('@/hooks/useBackupJobs', () => ({
  useBackupJobs: () => ({
    activeJobs: [],
    historyJobs: [],
    isLoading: false,
    isError: true,
    error: new Error('Backup jobs API unavailable'),
    isFetching: false,
    refetch: vi.fn(),
    watchJob: vi.fn(),
  }),
}))

vi.mock('@/components/backup/GDriveSection', () => ({ default: () => <div>Google Drive section</div> }))
vi.mock('@/components/backup/SnapshotSection', () => ({ default: () => <div>Snapshot section</div> }))

function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <MemoryRouter>
      <QueryClientProvider client={client}>
        <Backups />
      </QueryClientProvider>
    </MemoryRouter>,
  )
}

describe('Backups page', () => {
  beforeEach(() => vi.clearAllMocks())

  it('keeps backup and schedule failures distinct from empty states', async () => {
    vi.mocked(api.get).mockRejectedValue(new Error('Backup API unavailable'))

    renderPage()

    expect(await screen.findByText('Yerel yedek envanteri yüklenemedi. Yedekleme ve temizleme kontrolleri duraklatıldı.')).toBeInTheDocument()
    expect(screen.getByText('Cron zamanlama altyapısı kullanılamıyor')).toBeInTheDocument()
    expect(screen.getByText('Canlı yedekleme işleri yüklenemedi.')).toBeInTheDocument()
    expect(screen.queryByText('Henüz yedek yok')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Yedek Oluştur' })).toBeDisabled()
  })

  it('keeps the local overview visible when optional schedule and Drive status fail', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/backups') {
        return Promise.resolve({
          backups: [{ id: 'local-one', type: 'files', status: 'completed', size: 1024, path: '/backups/local-one.tar.gz' }],
        })
      }
      if (path === '/backups/gdrive/status') return Promise.reject(new Error('Drive status unavailable'))
      if (path === '/backups/schedules') return Promise.reject(new Error('Schedule status unavailable'))
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })

    renderPage()

    expect(await screen.findByText('Yerel yedek')).toBeInTheDocument()
    expect(screen.getByText('Yerel yedek').parentElement).toHaveTextContent('1 dosya')
    expect(screen.getByText('Zamanlama').parentElement).toHaveTextContent('Kullanılamıyor')
    expect(screen.getByText('Uzak depo').parentElement).toHaveTextContent('Kullanılamıyor')
    expect(screen.getByText('Otomatik yükleme').parentElement).toHaveTextContent('Bilinmiyor')
  })

  it('explains an unavailable crontab without showing an empty schedule', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/backups') return Promise.resolve({ backups: [] })
      if (path === '/backups/gdrive/status') return Promise.resolve({ connected: false, rcloneFound: false, settings: {} })
      if (path === '/backups/schedules') {
        return Promise.reject(new ApiError(503, 'backup scheduling unavailable: crontab read failed: permission denied'))
      }
      if (path === '/nginx/vhosts') return Promise.resolve({ vhosts: [] })
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })

    renderPage()

    const title = await screen.findByText('Cron zamanlama altyapısı kullanılamıyor')
    expect(title.parentElement?.parentElement).toHaveTextContent('backup scheduling unavailable: crontab read failed: permission denied')
    expect(screen.queryByText('Zamanlama yok. Verilerinizi korumak için otomatik yedekleme kurun.')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Retry detection' })).toBeInTheDocument()
  })

  it('saves retention as a backup count instead of days', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/backups') return Promise.resolve({ backups: [] })
      if (path === '/backups/gdrive/status') return Promise.resolve({ connected: false, rcloneFound: false, settings: {} })
      if (path === '/backups/schedules') return Promise.resolve({ schedules: [] })
      if (path === '/nginx/vhosts') return Promise.resolve({ vhosts: [] })
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })
    vi.mocked(api.post).mockResolvedValue({})

    renderPage()

    fireEvent.click(await screen.findByRole('button', { name: 'Zamanlama Kur' }))
    const dialog = await screen.findByRole('dialog')
    expect(dialog).toHaveTextContent('Saklama — son 10 yedeği tut')
    fireEvent.change(within(dialog).getByRole('spinbutton'), { target: { value: '7' } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Kaydet' }))

    await waitFor(() => {
      expect(api.post).toHaveBeenCalledWith('/backups/schedules', {
        frequency: 'daily',
        time: '03:00',
        retention_count: 7,
      })
    })
  })

  it('deletes the exact observed backup schedule', async () => {
    const rawLine = '0 3 * * * /var/lib/hserver/backups/run-backup.sh type=full retention=7 # hserver-backup'
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/backups') return Promise.resolve({ backups: [] })
      if (path === '/backups/gdrive/status') return Promise.resolve({ connected: false, rcloneFound: false, settings: {} })
      if (path === '/backups/schedules') {
        return Promise.resolve({ schedules: [{
          frequency: 'daily',
          time: '03:00',
          retention_count: 7,
          retention_days: 7,
          cron: '0 3 * * *',
          type: 'full',
          rawLine,
        }] })
      }
      if (path === '/nginx/vhosts') return Promise.resolve({ vhosts: [] })
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })
    vi.mocked(api.delete).mockResolvedValue({ success: true })

    renderPage()

    fireEvent.click(await screen.findByRole('button', { name: 'Sil' }))
    const dialog = await screen.findByRole('dialog')
    fireEvent.click(within(dialog).getByRole('button', { name: 'Sil' }))

    await waitFor(() => {
      expect(api.delete).toHaveBeenCalledWith('/backups/schedules', { rawLine })
    })
  })

  it('shows an observed custom cron without calling it daily', async () => {
    const cron = '0 3 15 * *'
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/backups') return Promise.resolve({ backups: [] })
      if (path === '/backups/gdrive/status') return Promise.resolve({ connected: false, rcloneFound: false, settings: {} })
      if (path === '/backups/schedules') {
        return Promise.resolve({ schedules: [{
          retention_count: 7,
          retention_days: 7,
          cron,
          type: 'full',
          rawLine: `${cron} /var/lib/hserver/backups/run-backup.sh type=full retention=7 # hserver-backup`,
        }] })
      }
      if (path === '/nginx/vhosts') return Promise.resolve({ vhosts: [] })
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })

    renderPage()

    expect(await screen.findByText(`Özel cron ${cron} · son 7 yedek saklanır`)).toBeInTheDocument()
    expect(screen.getByText(`Özel cron · ${cron}`)).toBeInTheDocument()
    expect(screen.queryByText(/Günlük 03:00/)).not.toBeInTheDocument()
  })

  it('blocks selected-site backups while the vhost inventory is unavailable', async () => {
    vi.mocked(api.get)
      .mockResolvedValueOnce({ backups: [] })
      .mockResolvedValueOnce({ connected: false, rcloneFound: false, settings: {} })
      .mockResolvedValueOnce({ schedules: [] })
      .mockRejectedValueOnce(new Error('Vhost API unavailable'))

    renderPage()

    await screen.findByText('Henüz yedek yok')
    const createButtons = await screen.findAllByRole('button', { name: 'Yedek Oluştur' })
    fireEvent.click(createButtons[0])
    fireEvent.click(screen.getByRole('button', { name: 'Seçili siteler' }))

    expect(await screen.findByText('Site listesi yüklenemedi.')).toBeInTheDocument()
    expect(screen.getByText('Vhost API unavailable')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Ön kontrolü başlat' })).toBeDisabled()
  })

  it('requires a successful artifact preflight before restore', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/backups') {
        return Promise.resolve({
          backups: [{
            id: 'database-backup-id',
            type: 'database',
            status: 'completed',
            size: 4096,
            path: '/var/lib/hserver/backups/nightly-db-application-postgresql.sql.gz',
          }],
        })
      }
      if (path === '/gdrive-status') return Promise.resolve({ connected: false, rcloneFound: false, settings: {} })
      if (path === '/backups/gdrive/status') return Promise.resolve({ connected: false, rcloneFound: false, settings: {} })
      if (path === '/backups/schedules') return Promise.resolve({ schedules: [] })
      if (path === '/backups/restore/database-backup-id/validate') {
        return Promise.resolve({
          id: 'database-backup-id',
          name: 'nightly-db-application-postgresql.sql.gz',
          type: 'database',
          artifactBytes: 4096,
          includesDatabase: true,
          includesFiles: false,
          databaseEngine: 'postgresql',
          databaseTarget: 'application',
          databaseRecovery: true,
          filesRollback: false,
        })
      }
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })
    vi.mocked(api.post).mockResolvedValue({ jobId: 'restore-job' })

    renderPage()
    await screen.findByText('nightly-db-application-postgresql.sql.gz')
    fireEvent.click(screen.getByTitle('Geri yükle'))

    const dialog = await screen.findByRole('dialog')
    expect(await within(dialog).findByText('Artifact bütünlüğü doğrulandı.')).toBeInTheDocument()
    expect(within(dialog).getByText(/postgresql \/ application/)).toBeInTheDocument()
    expect(within(dialog).getByText(/otomatik recovery point/)).toBeInTheDocument()
    const confirmScope = within(dialog).getByRole('checkbox')
    expect(confirmScope).toBeEnabled()
    fireEvent.click(confirmScope)
    fireEvent.click(within(dialog).getByRole('button', { name: 'Geri Yükle' }))

    await waitFor(() => {
      expect(api.post).toHaveBeenCalledWith('/backups/restore/database-backup-id')
    })
  })

  it('explains automatic file recovery when restore preflight supports rollback', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/backups') {
        return Promise.resolve({
          backups: [{
            id: 'files-backup-id',
            type: 'files',
            status: 'completed',
            size: 8192,
            path: '/var/lib/hserver/backups/nightly-files.tar.gz',
          }],
        })
      }
      if (path === '/gdrive-status') return Promise.resolve({ connected: false, rcloneFound: false, settings: {} })
      if (path === '/backups/gdrive/status') return Promise.resolve({ connected: false, rcloneFound: false, settings: {} })
      if (path === '/backups/schedules') return Promise.resolve({ schedules: [] })
      if (path === '/backups/restore/files-backup-id/validate') {
        return Promise.resolve({
          id: 'files-backup-id',
          name: 'nightly-files.tar.gz',
          type: 'files',
          artifactBytes: 8192,
          includesDatabase: false,
          includesFiles: true,
          databaseRecovery: false,
          filesRollback: true,
        })
      }
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })

    renderPage()
    await screen.findByText('nightly-files.tar.gz')
    fireEvent.click(screen.getByTitle('Geri yükle'))

    const dialog = await screen.findByRole('dialog')
    expect(await within(dialog).findByText(/Dosyalar değişmeden önce recovery archive oluşturulur/)).toBeInTheDocument()
    expect(within(dialog).getByText(/yeni oluşturulan yollar kaldırılır/)).toBeInTheDocument()
    expect(within(dialog).queryByText(/otomatik rollback yoktur/)).not.toBeInTheDocument()
  })

  it('keeps restore disabled when artifact preflight fails', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/backups') {
        return Promise.resolve({
          backups: [{
            id: 'broken-backup-id',
            type: 'files',
            status: 'completed',
            size: 4096,
            path: '/var/lib/hserver/backups/broken-files.tar.gz',
          }],
        })
      }
      if (path === '/backups/gdrive/status') return Promise.resolve({ connected: false, rcloneFound: false, settings: {} })
      if (path === '/backups/schedules') return Promise.resolve({ schedules: [] })
      if (path === '/backups/restore/broken-backup-id/validate') {
        return Promise.reject(new Error('files artifact validation failed: invalid gzip'))
      }
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })

    renderPage()
    await screen.findByText('broken-files.tar.gz')
    fireEvent.click(screen.getByTitle('Geri yükle'))

    const dialog = await screen.findByRole('dialog')
    expect(await within(dialog).findByText('Geri yükleme ön kontrolü başarısız.')).toBeInTheDocument()
    expect(within(dialog).getByText(/invalid gzip/)).toBeInTheDocument()
    expect(within(dialog).getByRole('checkbox')).toBeDisabled()
    expect(within(dialog).getByRole('button', { name: 'Geri Yükle' })).toBeDisabled()
  })
})
