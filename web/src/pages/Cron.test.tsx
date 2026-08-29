import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '@/lib/api'
import Cron from './Cron'

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
      <Cron />
    </QueryClientProvider>,
  )
}

const deployJob = {
  id: 'deploy-job-1',
  user: 'deploy-user',
  schedule: '0 * * * *',
  command: '/usr/local/bin/task',
  description: 'Hourly task',
  isActive: true,
  humanSchedule: 'Every hour',
}

function mockCronInventory() {
  vi.mocked(api.get).mockImplementation((path) => {
    if (path === '/cron/status') return Promise.resolve({
      available: true,
      installed: true,
      running: true,
      state: 'healthy',
      daemonState: 'active',
    })
    if (path === '/cron/jobs') return Promise.resolve({ jobs: [deployJob] })
    if (path === '/cron/system') return Promise.resolve({ files: [] })
    return Promise.reject(new Error(`Unexpected GET ${path}`))
  })
}

describe('Cron', () => {
  beforeEach(() => vi.clearAllMocks())

  it('keeps managed and system cron failures distinct from empty inventories', async () => {
    vi.mocked(api.get).mockRejectedValue(new Error('cron API unavailable'))
    renderPage()

    expect(await screen.findByText('Cron readiness could not be determined')).toBeInTheDocument()
    expect(screen.getByText('Managed cron inventory is paused until Cron readiness is available.')).toBeInTheDocument()
    expect(screen.getByText('System cron inventory could not be loaded.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /add job/i })).toBeDisabled()
    expect(screen.queryByText('No cron jobs configured')).not.toBeInTheDocument()
    expect(screen.queryByText('No system cron files found')).not.toBeInTheDocument()
  })

  it('distinguishes a missing Cron installation from an empty job inventory', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/cron/status') return Promise.resolve({
        available: false,
        installed: false,
        running: false,
        state: 'not-installed',
        daemonState: 'unknown',
        error: 'crontab command is not installed',
      })
      if (path === '/cron/system') return Promise.resolve({ files: [] })
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })
    renderPage()

    expect(await screen.findAllByText('Cron is not installed')).toHaveLength(2)
    expect(screen.getByText('Managed cron inventory is paused until Cron readiness is available.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /add job/i })).toBeDisabled()
    expect(api.get).not.toHaveBeenCalledWith('/cron/jobs')
    expect(screen.queryByText('No cron jobs configured')).not.toBeInTheDocument()
  })

  it('keeps existing jobs visible but pauses mutations while the daemon is stopped', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/cron/status') return Promise.resolve({
        available: false,
        installed: true,
        running: false,
        state: 'stopped',
        daemonState: 'inactive',
        error: 'cron service is inactive',
      })
      if (path === '/cron/jobs') return Promise.resolve({ jobs: [deployJob] })
      if (path === '/cron/system') return Promise.resolve({ files: [] })
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })
    renderPage()

    expect(await screen.findByText('Cron is installed but the daemon is stopped')).toBeInTheDocument()
    expect(await screen.findByText('/usr/local/bin/task')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /add job/i })).toBeDisabled()
    expect(screen.getByTitle('Disable')).toBeDisabled()
    expect(screen.getByTitle('Edit')).toBeDisabled()
    expect(screen.getByTitle('Delete')).toBeDisabled()
  })

  it('sends the owning user and complete replacement payload when toggling a job', async () => {
    mockCronInventory()
    vi.mocked(api.put).mockResolvedValue({})
    renderPage()

    fireEvent.click(await screen.findByTitle('Disable'))

    await waitFor(() => expect(api.put).toHaveBeenCalledWith(
      '/cron/jobs/deploy-job-1?user=deploy-user',
      {
        schedule: '0 * * * *',
        command: '/usr/local/bin/task',
        description: 'Hourly task',
        isActive: false,
      },
    ))
  })

  it('loads the selected job into the editor and preserves its owner and active state', async () => {
    mockCronInventory()
    vi.mocked(api.put).mockResolvedValue({})
    renderPage()

    fireEvent.click(await screen.findByTitle('Edit'))
    expect(screen.getByLabelText('User')).toHaveValue('deploy-user')
    expect(screen.getByLabelText('User')).toBeDisabled()
    expect(screen.getByText('Recreate the job to move it to another user.')).toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('Command'), {
      target: { value: '/usr/local/bin/updated-task' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Save Changes' }))

    await waitFor(() => expect(api.put).toHaveBeenCalledWith(
      '/cron/jobs/deploy-job-1?user=deploy-user',
      {
        schedule: '0 * * * *',
        command: '/usr/local/bin/updated-task',
        description: 'Hourly task',
        isActive: true,
      },
    ))
  })

  it('deletes the job from its owning user crontab', async () => {
    mockCronInventory()
    vi.mocked(api.delete).mockResolvedValue({})
    renderPage()

    fireEvent.click(await screen.findByTitle('Delete'))
    fireEvent.click(screen.getByRole('button', { name: 'Delete' }))

    await waitFor(() => expect(api.delete).toHaveBeenCalledWith(
      '/cron/jobs/deploy-job-1?user=deploy-user',
    ))
  })
})
