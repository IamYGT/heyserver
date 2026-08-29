import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { api, ApiError } from '@/lib/api'
import PM2 from './PM2'

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
    delete: vi.fn(),
  },
}))

function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })

  return render(
    <QueryClientProvider client={client}>
      <PM2 />
    </QueryClientProvider>,
  )
}

describe('PM2 page', () => {
  it('keeps mutations disabled when the PM2 integration is unavailable', async () => {
    vi.mocked(api.get).mockRejectedValue(new ApiError(503, 'PM2 binary is unavailable'))

    renderPage()

    expect(await screen.findByText('PM2 integration is not configured')).toBeInTheDocument()
    expect(screen.getByText('PM2 binary is unavailable')).toBeInTheDocument()
    expect(screen.getByText(/HSERVER_PM2_USER/)).toBeInTheDocument()
    expect(screen.queryByText(/systemctl status pm2-/)).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: /retry detection/i })).toBeInTheDocument()
    expect(screen.queryByText('No PM2 processes running')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'New Process' })).toBeDisabled()
  })

  it('shows daemon and identity remediation when configured PM2 inventory fails', async () => {
    vi.mocked(api.get).mockRejectedValue(new ApiError(500, 'pm2 jlist failed'))

    renderPage()

    expect(await screen.findByText('PM2 inventory is unavailable')).toBeInTheDocument()
    expect(screen.getByText(/systemctl status pm2-/)).toBeInTheDocument()
    expect(screen.getByText(/Correct the owner, binary, home, or daemon state/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'New Process' })).toBeDisabled()
  })

  it('renders healthy for an explicit successful empty inventory', async () => {
    vi.clearAllMocks()
    vi.mocked(api.get).mockResolvedValue({ processes: [], state: 'healthy' })

    renderPage()

    expect(await screen.findByTestId('pm2-availability-state')).toHaveTextContent('Healthy')
    expect(screen.getByText('No PM2 processes running')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Save' })).not.toBeDisabled()
    expect(screen.getAllByRole('button', { name: 'New Process' }).every((button) => !button.hasAttribute('disabled'))).toBe(true)
  })

  it('keeps process runtime status separate from provider availability', async () => {
    vi.clearAllMocks()
    vi.mocked(api.get).mockResolvedValue({
      processes: [{
        id: 7,
        name: 'stopped-worker',
        status: 'stopped',
        pid: 0,
        cpu: 0,
        memory: 0,
        uptime: 0,
        restarts: 1,
        mode: 'fork',
        script: '/var/www/example.com/worker.js',
        instances: 1,
      }],
      state: 'healthy',
    })

    renderPage()

    expect(await screen.findByTestId('pm2-availability-state')).toHaveTextContent('Healthy')
    expect(screen.getByText('Stopped')).toBeInTheDocument()
    expect(screen.getByText('0/1 online')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Save' })).not.toBeDisabled()
  })

  it('shows explicit unavailable inventory state and pauses mutations', async () => {
    vi.clearAllMocks()
    vi.mocked(api.get).mockResolvedValue({ processes: [], state: 'unavailable', error: 'pm2 jlist failed' })

    renderPage()

    expect(await screen.findByTestId('pm2-availability-state')).toHaveTextContent('Unavailable')
    expect(screen.getByText('PM2 inventory is unavailable')).toBeInTheDocument()
    expect(screen.getByText('pm2 jlist failed')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'New Process' })).toBeDisabled()
  })

  it('submits the process form using the backend deploy contract', async () => {
    vi.mocked(api.get).mockResolvedValue([])
    vi.mocked(api.post).mockResolvedValue({})

    renderPage()

    await screen.findByText('No PM2 processes running')
    const newProcessButtons = await screen.findAllByRole('button', { name: 'New Process' })
    fireEvent.click(newProcessButtons[0])
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'api' } })
    fireEvent.change(screen.getByLabelText('Script path'), { target: { value: '/var/www/example.com/server.js' } })
    fireEvent.change(screen.getByLabelText('Working directory (optional)'), { target: { value: '/var/www/example.com' } })
    fireEvent.click(screen.getByRole('button', { name: 'Start Process' }))

    await waitFor(() => {
      expect(api.post).toHaveBeenCalledWith('/pm2/deploy', {
        name: 'api',
        script: '/var/www/example.com/server.js',
        cwd: '/var/www/example.com',
        instances: 1,
        exec_mode: 'fork',
        node_env: 'production',
      })
    })
  })

  it('confirms process deletion and uses the supported PM2 action route', async () => {
    vi.clearAllMocks()
    vi.mocked(api.get).mockResolvedValue([{
      id: 7,
      name: 'example-api',
      status: 'online',
      pid: 4242,
      cpu: 1.5,
      memory: 128,
      uptime: 300,
      restarts: 0,
      mode: 'fork',
      script: '/var/www/example.com/server.js',
      instances: 1,
    }])
    vi.mocked(api.post).mockResolvedValue({})

    renderPage()

    fireEvent.click(await screen.findByRole('button', { name: 'Delete example-api' }))

    expect(screen.getByText('Delete PM2 process?')).toBeInTheDocument()
    expect(screen.getByText('/var/www/example.com/server.js', { selector: 'p' })).toBeInTheDocument()
    expect(api.post).not.toHaveBeenCalledWith('/pm2/processes/7/delete')

    fireEvent.click(screen.getByRole('button', { name: 'Delete Process' }))

    await waitFor(() => expect(api.post).toHaveBeenCalledWith('/pm2/processes/7/delete'))
  })
})
