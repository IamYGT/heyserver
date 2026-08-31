import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import DeveloperAPI from './DeveloperAPI'

const contract = {
  openapi: '3.1.0',
  info: { title: 'Heyserver API', version: '0.1.0' },
  'x-hserver-contract-version': 4,
  'x-hserver-route-count': 3,
  'x-hserver-schema-count': 30,
  paths: {
    '/api/dns/status': {
      get: { operationId: 'get-api-dns-status', summary: 'GET /api/dns/status', tags: ['dns'], 'x-hserver-access': 'Authenticated' },
    },
    '/api/dns/zones/{domain}': {
      delete: {
        operationId: 'delete-api-dns-zones-domain',
        summary: 'DELETE /api/dns/zones/{domain}',
        tags: ['dns'],
        parameters: [{ name: 'domain', in: 'path', required: true }],
        'x-hserver-access': 'Admin',
      },
    },
    '/api/docker/containers': {
      get: { operationId: 'get-api-docker-containers', summary: 'GET /api/docker/containers', tags: ['docker'], 'x-hserver-access': 'Authenticated' },
    },
  },
}

afterEach(() => vi.unstubAllGlobals())

it('loads, searches, and filters the installed OpenAPI route inventory', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true, json: async () => contract }))
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(<QueryClientProvider client={client}><DeveloperAPI /></QueryClientProvider>)

  expect(await screen.findByText('/api/dns/status')).toBeInTheDocument()
  expect(screen.getByText('/api/docker/containers')).toBeInTheDocument()
  expect(screen.getByText('Promoted schemas')).toBeInTheDocument()
  expect(screen.getByText('30')).toBeInTheDocument()
  expect(screen.getByRole('link', { name: 'Download JSON' })).toHaveAttribute('href', '/openapi.json')

  fireEvent.change(screen.getByLabelText('Search API routes'), { target: { value: 'dns' } })
  expect(screen.queryByText('/api/docker/containers')).not.toBeInTheDocument()
  expect(screen.getByText('/api/dns/zones/{domain}')).toBeInTheDocument()

  fireEvent.change(screen.getByLabelText('Filter by access'), { target: { value: 'Admin' } })
  expect(screen.queryByText('/api/dns/status')).not.toBeInTheDocument()
  expect(screen.getByText('/api/dns/zones/{domain}')).toBeInTheDocument()
  expect(screen.getByText('domain')).toBeInTheDocument()
})

it('keeps the route inventory in a loading state while the contract is pending', () => {
  vi.stubGlobal('fetch', vi.fn().mockReturnValue(new Promise(() => undefined)))
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(<QueryClientProvider client={client}><DeveloperAPI /></QueryClientProvider>)

  expect(screen.getByText('Loading the installed contract…')).toBeInTheDocument()
})

describe('contract failure feedback', () => {
  const failures = [
    [401, 'Permission denied', 'You do not have permission to view the installed OpenAPI contract.'],
    [403, 'Permission denied', 'You do not have permission to view the installed OpenAPI contract.'],
    [404, 'OpenAPI contract not found', 'The installed OpenAPI contract is not available on this server.'],
    [0, 'OpenAPI contract temporarily unavailable', 'The contract service is temporarily unavailable. Check your connection and try again.'],
    [500, 'OpenAPI contract temporarily unavailable', 'The contract service is temporarily unavailable. Check your connection and try again.'],
    [503, 'OpenAPI contract temporarily unavailable', 'The contract service is temporarily unavailable. Check your connection and try again.'],
    [422, 'OpenAPI contract operation failed', 'The installed OpenAPI contract could not be loaded. Please try again.'],
  ] as const

  it.each(failures)('maps HTTP %s to a safe, distinct state', async (status, title, message) => {
    const backendError = 'backend details must not be shown'
    const fetchMock = vi.fn()
    if (status === 0) {
      fetchMock.mockRejectedValue(new Error(backendError))
    } else {
      fetchMock.mockResolvedValue({ ok: false, status, text: async () => backendError })
    }
    vi.stubGlobal('fetch', fetchMock)

    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(<QueryClientProvider client={client}><DeveloperAPI /></QueryClientProvider>)

    expect(await screen.findByText(title)).toBeInTheDocument()
    expect(screen.getByText(message)).toBeInTheDocument()
    expect(screen.queryByText(backendError)).not.toBeInTheDocument()
  })
})
