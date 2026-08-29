import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '@/lib/api'
import Domains from './Domains'

vi.mock('@/lib/api', () => ({
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
    <MemoryRouter>
      <QueryClientProvider client={client}>
        <Domains />
      </QueryClientProvider>
    </MemoryRouter>,
  )
}

const provisioning = {
  vhostsRoot: '/srv/www',
  nginxSitesAvailable: '/srv/nginx/available',
  nginxSitesEnabled: '/srv/nginx/enabled',
  nginxSnippetsDir: '/srv/nginx/snippets',
  dns: {
    provider: 'cloudflare',
    status: 'not_configured' as const,
    proxied: false,
    message: 'Cloudflare is not configured',
  },
}

describe('Domains page', () => {
  beforeEach(() => vi.clearAllMocks())

  it('pauses mutations instead of describing unavailable inventories as empty', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/domains') return Promise.reject(new Error('Domain API unavailable'))
      return Promise.reject(new Error('Provisioning API unavailable'))
    })

    renderPage()

    expect(await screen.findByText('Domains could not be loaded. Mutating controls are paused.')).toBeInTheDocument()
    expect(screen.getByText('Domain provisioning capabilities could not be loaded. Creation is paused.')).toBeInTheDocument()
    expect(screen.queryByText('No domains configured yet')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Add Domain' })).toBeDisabled()
  })

  it('uses the discovered vhost root and blocks creation when PHP inventory fails', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/domains') return Promise.resolve({ domains: [] })
      if (path === '/domains/provisioning') return Promise.resolve(provisioning)
      if (path === '/php/versions') return Promise.reject(new Error('PHP API unavailable'))
      if (path === '/settings') return Promise.resolve({ adminEmail: '' })
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })

    renderPage()

    await screen.findByText('No domains configured yet')
    fireEvent.click(screen.getAllByRole('button', { name: 'Add Domain' })[0])

    expect(await screen.findByText('PHP versions could not be loaded.')).toBeInTheDocument()
    fireEvent.change(screen.getByPlaceholderText('example.com or app.example.com'), {
      target: { value: 'example.com' },
    })

    expect(screen.getByDisplayValue('/srv/www/example.com/public_html')).toBeInTheDocument()
    expect(screen.getByText('nginx vhost → /srv/nginx/available/example.com.conf')).toBeInTheDocument()
    expect(screen.getByText('Managed nginx snippets → /srv/nginx/snippets')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Create Domain' })).toBeDisabled()
  })

  it('pauses domain creation when the managed snippet path is unavailable', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/domains') return Promise.resolve({ domains: [] })
      if (path === '/domains/provisioning') {
        return Promise.resolve({ ...provisioning, nginxSnippetsDir: '' })
      }
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })

    renderPage()

    expect(await screen.findByText('Domain creation is paused because installation paths are not configured.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Add Domain' })).toBeDisabled()
  })

  it('requires confirmation before removing a domain while keeping site files', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/domains') {
        return Promise.resolve({
          domains: [{
            id: 'example.com',
            name: 'example.com',
            type: 'php',
            root: '/srv/www/example.com/public_html',
            phpVersion: '8.4',
            sslEnabled: true,
            isActive: true,
          }],
        })
      }
      if (path === '/domains/provisioning') return Promise.resolve(provisioning)
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    vi.mocked(api.delete).mockResolvedValue(undefined)

    renderPage()

    fireEvent.click(await screen.findByRole('button', { name: 'Open actions for example.com' }))
    fireEvent.click(await screen.findByText('Delete'))

    expect(window.confirm).toHaveBeenCalledWith('Remove example.com from HServer? Site files will be kept.')
    await waitFor(() => expect(api.delete).toHaveBeenCalledWith('/domains/example.com'))
  })

  it('submits SPA mode as a real static provisioning option', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/domains') return Promise.resolve({ domains: [] })
      if (path === '/domains/provisioning') return Promise.resolve(provisioning)
      if (path === '/php/versions') return Promise.resolve([])
      if (path === '/settings') return Promise.resolve({ adminEmail: '' })
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })
    vi.mocked(api.post).mockResolvedValue({})

    renderPage()

    await screen.findByText('No domains configured yet')
    fireEvent.click(screen.getAllByRole('button', { name: 'Add Domain' })[0])
    fireEvent.change(screen.getByPlaceholderText('example.com or app.example.com'), {
      target: { value: 'spa.example.com' },
    })
    fireEvent.click(screen.getByRole('button', { name: /Static/ }))
    fireEvent.click(screen.getByRole('button', { name: 'Enable SPA mode' }))

    expect(screen.getByText('SPA fallback → index.html')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Create Domain' }))

    await waitFor(() => expect(api.post).toHaveBeenCalledWith('/domains', expect.objectContaining({
      domain: 'spa.example.com',
      type: 'static',
      spaMode: true,
    })))
  })

  it('submits the selected Node environment for PM2 provisioning', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/domains') return Promise.resolve({ domains: [] })
      if (path === '/domains/provisioning') return Promise.resolve(provisioning)
      if (path === '/php/versions') return Promise.resolve([])
      if (path === '/settings') return Promise.resolve({ adminEmail: '' })
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })
    vi.mocked(api.post).mockResolvedValue({})

    renderPage()

    await screen.findByText('No domains configured yet')
    fireEvent.click(screen.getAllByRole('button', { name: 'Add Domain' })[0])
    fireEvent.change(screen.getByPlaceholderText('example.com or app.example.com'), {
      target: { value: 'node.example.com' },
    })
    fireEvent.click(screen.getByRole('button', { name: /Node.js/ }))
    fireEvent.click(screen.getByRole('button', { name: 'development' }))

    expect(screen.getByText('NODE_ENV → development')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Create Domain' }))

    await waitFor(() => expect(api.post).toHaveBeenCalledWith('/domains', expect.objectContaining({
      domain: 'node.example.com',
      type: 'proxy',
      nodeEnv: 'development',
    })))
  })
})
