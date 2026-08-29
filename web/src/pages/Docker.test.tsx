import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '@/lib/api'
import Docker from './Docker'

vi.mock('@/lib/api', () => ({
  api: {
    get: vi.fn(),
    post: vi.fn(),
    delete: vi.fn(),
  },
}))

describe('Docker page failure state', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('does not describe an unavailable status request as a stopped daemon', async () => {
    vi.mocked(api.get).mockRejectedValue(new Error('Docker API unavailable'))
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })

    render(
      <QueryClientProvider client={client}>
        <Docker />
      </QueryClientProvider>,
    )

    expect(await screen.findByText('Docker status is unavailable')).toBeInTheDocument()
    expect(screen.getByText('Docker API unavailable')).toBeInTheDocument()
    expect(screen.getByText(/sudo \.\/doctor\.sh installed/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /retry detection/i })).toBeInTheDocument()
    expect(screen.queryByText('Docker daemon not running')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: /pull image/i })).toBeDisabled()
  })

  it('keeps independently loaded inventory visible but pauses mutations after a status refetch failure', async () => {
    const cachedStatus = {
      installed: true,
      running: true,
      version: '27.3.1',
      containersTotal: 1,
      containersRunning: 1,
      imageCount: 1,
    }
    vi.mocked(api.get).mockImplementation(async (path: string) => {
      if (path === '/docker/status') throw new Error('Docker status refresh failed')
      if (path === '/docker/containers') {
        return [{
          id: 'cached-status-container',
          name: 'still-observed-web',
          image: 'example/web:latest',
          status: 'running',
          detail: 'Up 2 minutes',
          ports: [],
          cpuPercent: 1,
          memoryUsage: 1024,
          memoryLimit: 2048,
          created: 'today',
        }]
      }
      if (path === '/docker/images') {
        return [{ id: 'sha256:observed', repoTags: ['example/web:latest'], size: '10MB', created: 'today' }]
      }
      throw new Error(`Unexpected GET ${path}`)
    })
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    client.setQueryData(['docker', 'status'], cachedStatus)

    render(
      <QueryClientProvider client={client}>
        <Docker />
      </QueryClientProvider>,
    )

    expect(await screen.findByText('Docker status is unavailable')).toBeInTheDocument()
    expect(await screen.findByText('still-observed-web')).toBeInTheDocument()
    expect(screen.getAllByText('example/web:latest')).toHaveLength(2)
    expect(screen.queryByText('Container inventory is paused until Docker status is available.')).not.toBeInTheDocument()
    expect(screen.queryByText('Image inventory is paused until Docker status is available.')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Stop still-observed-web' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Restart still-observed-web' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Remove image sha256:observed' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Pull Image' })).toBeDisabled()
  })

  it.each([
    {
      name: 'missing',
      status: { installed: false, running: false, containersTotal: 0, containersRunning: 0, imageCount: 0 },
      title: 'Docker is not installed',
      command: /systemctl enable --now docker/,
    },
    {
      name: 'stopped',
      status: { installed: true, running: false, version: '27.0.0', containersTotal: 0, containersRunning: 0, imageCount: 0 },
      title: 'Docker is installed but the daemon is stopped',
      command: /systemctl start docker/,
    },
  ])('shows contextual remediation when Docker is $name', async ({ status, title, command }) => {
    vi.mocked(api.get).mockResolvedValue(status)
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })

    render(
      <QueryClientProvider client={client}>
        <Docker />
      </QueryClientProvider>,
    )

    expect(await screen.findByText(title)).toBeInTheDocument()
    expect(screen.getByText(command)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /pull image/i })).toBeDisabled()
  })
})

describe('Docker management controls', () => {
  const status = {
    installed: true,
    running: true,
    version: '27.3.1',
    containersTotal: 1,
    containersRunning: 1,
    imageCount: 1,
  }
  const container = {
    id: 'abc123',
    name: 'web-1',
    image: 'example/app:latest',
    status: 'running' as const,
    detail: 'Up 2 minutes',
    ports: ['0.0.0.0:8080->80/tcp'],
    cpuPercent: 12.5,
    memoryUsage: 64 * 1024 * 1024,
    memoryLimit: 1024 * 1024 * 1024,
    created: '2026-08-26 20:00:00 +0000 UTC',
  }

  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(api.get).mockImplementation(async (path: string) => {
      if (path === '/docker/status') return status
      if (path === '/docker/containers') return [container]
      if (path === '/docker/images') {
        return [{ id: 'sha256:abc', repoTags: ['example/app:latest'], size: '120MB', created: 'today' }]
      }
      if (path === '/docker/containers/abc123/logs?tail=200') {
        return { logs: 'ready\n', tail: 200, truncated: true }
      }
      throw new Error(`Unexpected GET ${path}`)
    })
    vi.mocked(api.post).mockResolvedValue({ status: 'ok' })
    vi.mocked(api.delete).mockResolvedValue(undefined)
  })

  function renderDocker() {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    return render(
      <QueryClientProvider client={client}>
        <Docker />
      </QueryClientProvider>,
    )
  }

  it('renders normalized inventory and fetches bounded logs', async () => {
    renderDocker()

    expect(await screen.findByText('web-1')).toBeInTheDocument()
    expect(screen.getByText('0.0.0.0:8080->80/tcp')).toBeInTheDocument()
    expect(screen.getByText('12.5%')).toBeInTheDocument()
    expect(screen.getByText('64.0 MB / 1.0 GB')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Remove web-1' })).toBeDisabled()

    fireEvent.click(screen.getByRole('button', { name: 'View logs for web-1' }))
    expect(await screen.findByText('ready')).toBeInTheDocument()
    expect(screen.getByText('1 MiB response limit reached')).toBeInTheDocument()
    expect(api.get).toHaveBeenCalledWith('/docker/containers/abc123/logs?tail=200')
  })

  it('pulls an image through the management API', async () => {
    renderDocker()
    await screen.findByText('web-1')
    const pullImage = screen.getByRole('button', { name: 'Pull Image' })
    expect(pullImage).toBeEnabled()
    fireEvent.click(pullImage)
    fireEvent.change(screen.getByPlaceholderText('e.g. nginx:latest, postgres:16-alpine'), {
      target: { value: 'nginx:1.27' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Pull', hidden: true }))

    await waitFor(() => {
      expect(api.post).toHaveBeenCalledWith('/docker/images/pull', { name: 'nginx:1.27' })
    })
  })

  it('discards an unsubmitted image name when the pull dialog closes', async () => {
    renderDocker()
    await screen.findByText('web-1')

    fireEvent.click(screen.getByRole('button', { name: 'Pull Image' }))
    fireEvent.change(await screen.findByPlaceholderText('e.g. nginx:latest, postgres:16-alpine'), {
      target: { value: 'example/discarded:latest' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Close' }))

    fireEvent.click(screen.getByRole('button', { name: 'Pull Image' }))

    expect(await screen.findByPlaceholderText('e.g. nginx:latest, postgres:16-alpine')).toHaveValue('')
  })

  it('requires confirmation before removing a stopped container', async () => {
    vi.mocked(api.get).mockImplementation(async (path: string) => {
      if (path === '/docker/status') return { ...status, containersRunning: 0 }
      if (path === '/docker/containers') return [{ ...container, status: 'exited', detail: 'Exited (0)' }]
      if (path === '/docker/images') return []
      throw new Error(`Unexpected GET ${path}`)
    })

    renderDocker()
    await screen.findByText('web-1')

    fireEvent.click(screen.getByRole('button', { name: 'Remove web-1' }))

    expect(screen.getByText('Remove Docker container?')).toBeInTheDocument()
    expect(screen.getByText('web-1', { selector: 'code' })).toBeInTheDocument()
    expect(api.post).not.toHaveBeenCalledWith('/docker/containers/abc123/remove')

    fireEvent.click(screen.getByRole('button', { name: 'Remove' }))

    await waitFor(() => expect(api.post).toHaveBeenCalledWith('/docker/containers/abc123/remove'))
  })

  it('requires confirmation before removing an image', async () => {
    renderDocker()
    await screen.findByText('web-1')

    fireEvent.click(screen.getByRole('button', { name: 'Remove image sha256:abc' }))

    expect(screen.getByText('Remove Docker image?')).toBeInTheDocument()
    expect(screen.getByText('example/app:latest', { selector: 'code' })).toBeInTheDocument()
    expect(api.delete).not.toHaveBeenCalled()

    fireEvent.click(screen.getByRole('button', { name: 'Remove' }))

    await waitFor(() => expect(api.delete).toHaveBeenCalledWith('/docker/images/sha256%3Aabc'))
  })
})
