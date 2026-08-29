import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '@/lib/api'
import Nginx from './Nginx'

vi.mock('@/lib/api', () => ({
  api: {
    get: vi.fn(),
    post: vi.fn(),
    put: vi.fn(),
    delete: vi.fn(),
  },
}))

function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <Nginx />
    </QueryClientProvider>,
  )
}

const healthyStatus = {
  installed: true,
  status: 'active',
  statusAvailable: true,
  version: 'nginx/1.26.0',
  uptime: 'Tue 2026-08-25 10:00:00 UTC',
  configTest: { ok: true, output: 'syntax is ok' },
}

describe('Nginx page', () => {
  beforeEach(() => vi.clearAllMocks())

  it('does not describe an unavailable config inventory as empty', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/nginx/status') return Promise.resolve(healthyStatus)
      if (path === '/nginx/configs') return Promise.reject(new Error('Nginx API unavailable'))
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })

    renderPage()

    expect(await screen.findByText('Nginx configs could not be loaded. Mutating controls are paused.')).toBeInTheDocument()
    expect(screen.getByText('Nginx API unavailable')).toBeInTheDocument()
    expect(screen.queryByText('No configs found')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Reload' })).toBeDisabled()
  })

  it('shows nginx syntax failures as completed test results', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/nginx/status') return Promise.resolve(healthyStatus)
      if (path === '/nginx/configs') return Promise.resolve([])
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })
    vi.mocked(api.post).mockResolvedValue({ ok: false, output: 'nginx: invalid directive' })

    renderPage()

    await screen.findByText('No configs found')
    fireEvent.click(screen.getByRole('button', { name: 'Test Config' }))

    expect(await screen.findByText('nginx: invalid directive')).toBeInTheDocument()
    expect(screen.getByText('FAIL')).toBeInTheDocument()
  })

  it('creates a validated static site with only relevant fields', async () => {
    const createdConfig = {
      filename: 'new.example.conf',
      domain: 'new.example',
      type: 'static',
      isEnabled: false,
      content: 'server {}',
      checksum: 'b'.repeat(64),
    }
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/nginx/status') return Promise.resolve(healthyStatus)
      if (path === '/nginx/configs') return Promise.resolve([])
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })
    vi.mocked(api.post).mockResolvedValue(createdConfig)

    renderPage()

    await screen.findByText('No configs found')
    fireEvent.click(screen.getByRole('button', { name: 'Create Site' }))
    const dialog = await screen.findByRole('dialog')
    fireEvent.change(within(dialog).getByLabelText('Domain'), { target: { value: ' new.example ' } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Create Site' }))

    await waitFor(() => expect(api.post).toHaveBeenCalledWith('/nginx/configs', {
      domain: 'new.example',
      type: 'static',
      useSSL: false,
    }))
    expect((await screen.findAllByText('new.example.conf')).length).toBeGreaterThan(0)
  })

  it('renders the portable snippet response with content', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/nginx/status') return Promise.resolve(healthyStatus)
      if (path === '/nginx/configs') return Promise.resolve([])
      if (path === '/nginx/snippets') {
        return Promise.resolve([{
          name: 'security.conf',
          path: '/etc/nginx/snippets/security.conf',
          content: 'add_header X-Test true;',
        }])
      }
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })

    renderPage()

    await screen.findByText('No configs found')
    fireEvent.click(screen.getByRole('button', { name: 'Snippets' }))
    fireEvent.click(await screen.findByRole('button', { name: /security\.conf/i }))

    expect(screen.getByText('/etc/nginx/snippets/security.conf')).toBeInTheDocument()
    expect(screen.getByText('add_header X-Test true;')).toBeInTheDocument()
  })

  it('sends an explicit desired state when enabling a config', async () => {
    const config = {
      filename: 'site.conf',
      domain: 'site.example',
      type: 'static',
      isEnabled: false,
      content: 'server {}',
      checksum: 'a'.repeat(64),
    }
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/nginx/status') return Promise.resolve(healthyStatus)
      if (path === '/nginx/configs') return Promise.resolve([config])
      if (path === '/nginx/configs/site.conf') return Promise.resolve(config)
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })
    vi.mocked(api.put).mockResolvedValue({ isEnabled: true })

    renderPage()

    fireEvent.click(await screen.findByText('site.conf'))
    fireEvent.click(await screen.findByRole('button', { name: 'Enable' }))

    await waitFor(() => expect(api.put).toHaveBeenCalledWith('/nginx/configs/site.conf/state', { enabled: true }))
  })

  it('archives an observed disabled config under its checksum', async () => {
    const config = {
      filename: 'site.conf',
      domain: 'site.example',
      type: 'static',
      isEnabled: false,
      content: 'server {}',
      checksum: 'c'.repeat(64),
    }
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/nginx/status') return Promise.resolve(healthyStatus)
      if (path === '/nginx/configs') return Promise.resolve([config])
      if (path === '/nginx/configs/site.conf') return Promise.resolve(config)
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })
    vi.mocked(api.delete).mockResolvedValue({
      message: 'config archived',
      archive: '/etc/nginx/sites-available/site.conf.hserver-archive-test',
      checksum: config.checksum,
    })
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(true)

    renderPage()

    fireEvent.click(await screen.findByText('site.conf'))
    fireEvent.click(await screen.findByRole('button', { name: 'Archive' }))

    await waitFor(() => expect(api.delete).toHaveBeenCalledWith('/nginx/configs/site.conf', { checksum: config.checksum }))
    confirm.mockRestore()
  })

  it('restores an observed archive as a disabled config', async () => {
    const archive = {
      archive: 'site.conf.hserver-archive-20260827T120000.000000000Z',
      filename: 'site.conf',
      checksum: 'd'.repeat(64),
      size: 42,
      archivedAt: '2026-08-27T12:00:00Z',
      modifiedAt: '2026-08-27T12:00:00Z',
    }
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/nginx/status') return Promise.resolve(healthyStatus)
      if (path === '/nginx/configs') return Promise.resolve([])
      if (path === '/nginx/archives') return Promise.resolve([archive])
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })
    vi.mocked(api.post).mockResolvedValue({
      message: 'config restored',
      archive: archive.archive,
      filename: archive.filename,
      checksum: archive.checksum,
      isEnabled: false,
    })
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(true)

    renderPage()

    await screen.findByText('No configs found')
    fireEvent.click(screen.getByRole('button', { name: 'Archives' }))
    expect(await screen.findByText(archive.archive)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Restore disabled' }))

    await waitFor(() => expect(api.post).toHaveBeenCalledWith(`/nginx/archives/${archive.archive}/restore`, { checksum: archive.checksum }))
    expect(await screen.findByText('No configs found')).toBeInTheDocument()
    confirm.mockRestore()
  })

  it('rolls back from an edit backup with fresh backup and current checksums', async () => {
    const backup = {
      backup: 'site.conf.hserver-backup-20260827T130000.000000000Z',
      filename: 'site.conf',
      checksum: 'e'.repeat(64),
      size: 43,
      createdAt: '2026-08-27T13:00:00Z',
      modifiedAt: '2026-08-27T13:00:00Z',
    }
    const current = {
      filename: 'site.conf',
      domain: 'site.example',
      type: 'static',
      isEnabled: true,
      content: 'server { listen 8080; }',
      checksum: 'f'.repeat(64),
    }
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/nginx/status') return Promise.resolve(healthyStatus)
      if (path === '/nginx/configs') return Promise.resolve([])
      if (path === '/nginx/backups') return Promise.resolve([backup])
      if (path === '/nginx/configs/site.conf') return Promise.resolve(current)
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })
    vi.mocked(api.post).mockResolvedValue({
      message: 'config rolled back',
      backup: backup.backup,
      recovery: 'site.conf.hserver-backup-20260827T130001.000000000Z',
      filename: backup.filename,
      checksum: backup.checksum,
      isEnabled: true,
    })
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(true)

    renderPage()

    await screen.findByText('No configs found')
    fireEvent.click(screen.getByRole('button', { name: 'Edit Backups' }))
    expect(await screen.findByText(backup.backup)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Roll back config' }))

    await waitFor(() => expect(api.get).toHaveBeenCalledWith('/nginx/configs/site.conf'))
    await waitFor(() => expect(api.post).toHaveBeenCalledWith(`/nginx/backups/${backup.backup}/restore`, {
      backupChecksum: backup.checksum,
      currentChecksum: current.checksum,
    }))
    expect(await screen.findByText('No configs found')).toBeInTheDocument()
    confirm.mockRestore()
  })

  it('keeps nginx actions disabled when the binary is not installed', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/nginx/status') {
        return Promise.resolve({
          installed: false,
          status: 'not-installed',
          statusAvailable: false,
          version: '',
          uptime: '',
          configTest: { ok: false, output: 'nginx executable not found' },
        })
      }
      if (path === '/nginx/configs') return Promise.resolve([])
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })

    renderPage()

    expect(await screen.findByText('Nginx is not installed')).toBeInTheDocument()
    expect(screen.getByText(/HServer never installs host packages automatically/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Test Config' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Reload' })).toBeDisabled()
  })

  it('allows syntax inspection but pauses reload while nginx is inactive', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/nginx/status') {
        return Promise.resolve({
          ...healthyStatus,
          status: 'inactive',
          configTest: { ok: false, output: 'nginx: invalid directive' },
        })
      }
      if (path === '/nginx/configs') return Promise.resolve([])
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })

    renderPage()

    expect(await screen.findByText('Nginx is not active')).toBeInTheDocument()
    expect(screen.getByText('nginx: invalid directive')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Test Config' })).toBeEnabled()
    expect(screen.getByRole('button', { name: 'Reload' })).toBeDisabled()
  })

  it('pauses all nginx actions when status detection fails', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/nginx/status') return Promise.reject(new Error('status API unavailable'))
      if (path === '/nginx/configs') return Promise.resolve([])
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })

    renderPage()

    expect(await screen.findByText('Nginx service status is unavailable')).toBeInTheDocument()
    expect(screen.getByText('status API unavailable')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Test Config' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Reload' })).toBeDisabled()
  })
})
