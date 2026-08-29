import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '@/lib/api'
import Integrations from './Integrations'

vi.mock('@/lib/api', () => ({
  api: { get: vi.fn() },
}))

const integrationIds = [
  'cloudflare.dns',
  'stalwart.mail',
  'mail.access',
  'backup.gdrive',
  'backup.snapshot.restic',
  'process.pm2',
  'web.nginx',
  'runtime.php_fpm',
  'firewall.ufw',
  'tls.certbot',
  'dns.bind9',
  'database.local',
  'container.docker',
  'storage.smartmontools',
  'notification.delivery',
] as const

const catalog = {
  schema_version: 1,
  documentation: {
    table_path: 'docs/optional-integrations.md',
    table_header: 'Integration',
    marker_prefix: 'optional-integrations:v1:',
    marker_convention: 'marker_prefix + slug(display_name)',
  },
  entries: integrationIds.map((id, index) => ({
    id,
    display_name: index === 0 ? 'Cloudflare' : id.split('.')[0],
    purpose: `Purpose for ${id}`,
    requirement: index === 1 || index > 9 ? 'feature_specific' : 'optional',
    classes: index === 1 ? ['managed_node_capability', 'client_surface'] : ['provider_adapter', 'client_surface'],
    targets: index === 1 ? ['local_host', 'managed_node'] : ['local_host'],
    configuration: {
      non_secret_keys: [`HSERVER_${index}_SETTING`],
      secret_key_names: index === 0 ? ['HSERVER_CF_API_TOKEN'] : [],
      secret_file_refs: [],
      boundary: `Configuration boundary for ${id}`,
    },
    status: {
      canonical_states: ['not_configured', 'unavailable', 'healthy'],
      raw_state_mappings: [],
      api_route_prefixes: [`/api/${id}`],
    },
    evidence: {
      web: [{ path: `web/src/pages/${id}.tsx`, claim: 'Client surface' }],
      docs: [{ path: `docs/${id}.md`, claim: 'Documentation boundary' }],
      tests: [{ path: `web/src/pages/${id}.test.tsx`, claim: 'Focused coverage' }],
    },
  })),
}

const observedAt = '2026-08-28T12:00:00Z'

const statusWithCanonicalStates = {
  schema_version: 1,
  observed_at: observedAt,
  results: [
    { id: 'cloudflare.dns', state: 'healthy' },
    { id: 'stalwart.mail', state: 'not_configured' },
    { id: 'mail.access', state: 'unavailable' },
  ],
  unprobed: integrationIds.slice(3),
  partial: true,
}

const completeStatus = {
  schema_version: 1,
  observed_at: observedAt,
  results: integrationIds.map((id) => ({ id, state: 'healthy' })),
  unprobed: [],
  partial: false,
}

const managedStatus = {
  schema_version: 1,
  observed_at: observedAt,
  target: { scope: 'managed_node', node_id: 'edge-eu-1' },
  results: [
    { id: 'process.pm2', state: 'healthy' },
    { id: 'container.docker', state: 'unavailable' },
  ],
  partial: true,
}

function mockApi(statusPayload: unknown = completeStatus) {
  vi.mocked(api.get).mockImplementation((path) => Promise.resolve(
    path === '/integrations/catalog' ? catalog : statusPayload,
  ) as never)
}

function renderPage(initialEntry = '/integrations') {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={[initialEntry]}>
        <Integrations />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

describe('Integrations catalog and live status page', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockApi()
  })

  it('keeps catalog metadata separate and renders canonical live badges with observed time', async () => {
    mockApi(statusWithCanonicalStates)
    renderPage()

    expect(await screen.findByText('Cloudflare')).toBeInTheDocument()
    expect(screen.getAllByTestId('integration-card')).toHaveLength(15)
    expect(screen.getByText('Catalog metadata — open integration for live status')).toBeInTheDocument()
    expect(screen.getByTestId('integration-live-badge-cloudflare.dns')).toHaveTextContent('healthy')
    expect(screen.getByTestId('integration-live-badge-stalwart.mail')).toHaveTextContent('not_configured')
    expect(screen.getByTestId('integration-live-badge-mail.access')).toHaveTextContent('unavailable')
    expect(screen.getAllByText(`Observed ${observedAt}`)).toHaveLength(4)
    expect(screen.getByText('HSERVER_CF_API_TOKEN')).toBeInTheDocument()
    expect(screen.getAllByText('State vocabulary: not_configured · unavailable · healthy')).toHaveLength(15)
    expect(screen.queryByText('secret-value')).not.toBeInTheDocument()
    expect(screen.getAllByRole('link', { name: 'Open integration' })).toHaveLength(15)
  })

  it('shows Probe not implemented for explicit unprobed IDs without inventing a state', async () => {
    mockApi(statusWithCanonicalStates)
    renderPage()

    await screen.findByText('Cloudflare')
    expect(screen.getAllByText('Probe not implemented')).toHaveLength(12)
    expect(screen.getByTestId('integrations-status-partial')).toHaveTextContent('Live integration status is partial.')
    expect(screen.queryByTestId('integration-live-badge-backup.gdrive')).not.toBeInTheDocument()
  })

  it('joins status results by exact catalog ID', async () => {
    mockApi({
      schema_version: 1,
      observed_at: observedAt,
      results: [
        { id: 'cloudflare', state: 'healthy' },
        { id: 'cloudflare.dns-extra', state: 'unavailable' },
      ],
      unprobed: ['cloudflare.dns'],
      partial: true,
    })
    renderPage()

    await screen.findByText('Cloudflare')
    const cloudflareStatus = screen.getByTestId('integration-live-status-cloudflare.dns')
    expect(cloudflareStatus).toHaveTextContent('Probe not implemented')
    expect(cloudflareStatus).not.toHaveTextContent('healthy')
    expect(cloudflareStatus).not.toHaveTextContent('unavailable')
  })

  it('keeps catalog cards visible with a distinct status error and hides raw provider details', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/integrations/catalog') return Promise.resolve(catalog) as never
      return Promise.reject(new Error('provider-error secret-value /private/token')) as never
    })
    renderPage()

    expect(await screen.findByText('Cloudflare')).toBeInTheDocument()
    expect(screen.getByTestId('integrations-status-error')).toHaveTextContent('Live integration status could not be loaded.')
    expect(screen.getByTestId('integrations-status-error')).toHaveTextContent('Catalog metadata remains available.')
    expect(screen.getAllByTestId('integration-card')).toHaveLength(15)
    expect(screen.getAllByText('Live status unavailable')).toHaveLength(15)
    expect(screen.queryByText('provider-error secret-value /private/token')).not.toBeInTheDocument()
  })

  it('shows catalog cards while the separate status request is loading', async () => {
    let resolveStatus!: (payload: unknown) => void
    const pendingStatus = new Promise((resolve) => { resolveStatus = resolve })
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/integrations/catalog') return Promise.resolve(catalog) as never
      return pendingStatus as never
    })

    renderPage()

    expect(await screen.findByText('Cloudflare')).toBeInTheDocument()
    expect(screen.getByTestId('integrations-status-loading')).toBeInTheDocument()
    expect(screen.getAllByText('Loading live status…')).toHaveLength(15)

    resolveStatus(completeStatus)
    await waitFor(() => expect(screen.getByTestId('integration-live-badge-cloudflare.dns')).toHaveTextContent('healthy'))
  })

  it('retries only the status request from the status error state', async () => {
    let statusAttempts = 0
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/integrations/catalog') return Promise.resolve(catalog) as never
      statusAttempts += 1
      return statusAttempts === 1
        ? Promise.reject(new Error('status raw error')) as never
        : Promise.resolve(completeStatus) as never
    })

    renderPage()
    await screen.findByText('Cloudflare')
    fireEvent.click(await screen.findByRole('button', { name: 'Retry status' }))

    await waitFor(() => expect(screen.getByTestId('integration-live-badge-cloudflare.dns')).toHaveTextContent('healthy'))
    expect(statusAttempts).toBe(2)
    expect(api.get).toHaveBeenCalledWith('/integrations/catalog')
    expect(api.get).toHaveBeenCalledWith('/integrations/status')
  })

  it('uses the URL-selected managed target and joins only PM2 and Docker results', async () => {
    mockApi(managedStatus)
    renderPage('/integrations?node=edge-eu-1')

    expect(await screen.findByText('Cloudflare')).toBeInTheDocument()
    expect(screen.getByTestId('integration-live-badge-process.pm2')).toHaveTextContent('healthy')
    expect(screen.getByTestId('integration-live-badge-container.docker')).toHaveTextContent('unavailable')
    expect(screen.getAllByText('Probe not supported on managed target')).toHaveLength(13)
    expect(api.get).toHaveBeenCalledWith('/nodes/edge-eu-1/integrations/status')
    expect(api.get).not.toHaveBeenCalledWith('/integrations/status')
  })

  it('rejects a managed response whose target does not match the URL node without local fallback', async () => {
    mockApi({ ...managedStatus, target: { scope: 'managed_node', node_id: 'edge-us-2' } })
    renderPage('/integrations?node=edge-eu-1')

    expect(await screen.findByText('Cloudflare')).toBeInTheDocument()
    expect(await screen.findByTestId('integrations-status-error')).toHaveTextContent('Managed integration status could not be loaded.')
    expect(screen.queryByText('edge-us-2')).not.toBeInTheDocument()
    expect(api.get).toHaveBeenCalledWith('/nodes/edge-eu-1/integrations/status')
    expect(api.get).not.toHaveBeenCalledWith('/integrations/status')
  })

  it('shows safe offline state for managed 409 responses without raw details', async () => {
    const offlineError = Object.assign(new Error('provider-error secret-value /private/task-output'), {
      status: 409,
      message: 'managed_node_offline',
    })
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/integrations/catalog') return Promise.resolve(catalog) as never
      return Promise.reject(offlineError) as never
    })
    renderPage('/integrations?node=edge-eu-1')

    expect(await screen.findByText('Cloudflare')).toBeInTheDocument()
    expect(await screen.findByTestId('integrations-status-error')).toHaveTextContent('Managed target is offline.')
    expect(screen.queryByText('provider-error secret-value /private/task-output')).not.toBeInTheDocument()
    expect(screen.getAllByText('Probe not supported on managed target')).toHaveLength(13)
  })

  it('shows safe capability state for managed 409 responses without raw details', async () => {
    const capabilityError = Object.assign(new Error('provider-error secret-value /private/task-output'), {
      status: 409,
      message: 'capability_unavailable',
    })
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/integrations/catalog') return Promise.resolve(catalog) as never
      return Promise.reject(capabilityError) as never
    })
    renderPage('/integrations?node=edge-eu-1')

    expect(await screen.findByTestId('integrations-status-error')).toHaveTextContent('Managed target does not support integration status.')
    expect(screen.queryByText('provider-error secret-value /private/task-output')).not.toBeInTheDocument()
  })

  it('filters catalog metadata without affecting status joining', async () => {
    renderPage()
    await screen.findByText('Cloudflare')

    const search = screen.getByRole('searchbox', { name: 'Search integrations' })
    fireEvent.change(search, { target: { value: 'cloudflare' } })
    expect(screen.getAllByTestId('integration-card')).toHaveLength(1)
    expect(screen.getByTestId('integration-live-badge-cloudflare.dns')).toHaveTextContent('healthy')

    fireEvent.change(search, { target: { value: '' } })
    fireEvent.change(screen.getByRole('combobox', { name: 'Filter by class' }), { target: { value: 'managed_node_capability' } })
    expect(screen.getAllByTestId('integration-card')).toHaveLength(1)

    fireEvent.change(screen.getByRole('combobox', { name: 'Filter by class' }), { target: { value: 'all' } })
    fireEvent.change(screen.getByRole('combobox', { name: 'Filter by target' }), { target: { value: 'managed_node' } })
    expect(screen.getAllByTestId('integration-card')).toHaveLength(1)

    fireEvent.change(screen.getByRole('combobox', { name: 'Filter by target' }), { target: { value: 'all' } })
    fireEvent.change(screen.getByRole('combobox', { name: 'Filter by requirement' }), { target: { value: 'feature_specific' } })
    expect(screen.getAllByTestId('integration-card')).toHaveLength(6)
  })

  it('shows a filtered empty state without changing live status state', async () => {
    renderPage()
    await screen.findByText('Cloudflare')

    fireEvent.change(screen.getByRole('searchbox', { name: 'Search integrations' }), { target: { value: 'does-not-exist' } })

    expect(await screen.findByTestId('integrations-empty')).toHaveTextContent('No integrations match these filters.')
    expect(screen.queryByTestId('integration-card')).not.toBeInTheDocument()
    expect(screen.getByTestId('integrations-status-ready')).toBeInTheDocument()
  })

  it('shows a safe catalog error independently from live status', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/integrations/catalog') return Promise.reject(new Error('catalog offline')) as never
      return Promise.resolve(completeStatus) as never
    })
    renderPage()

    expect(await screen.findByTestId('integrations-error')).toHaveTextContent('Integration catalog could not be loaded.')
    expect(screen.queryByText('catalog offline')).not.toBeInTheDocument()
    expect(screen.getByTestId('integrations-status-ready')).toBeInTheDocument()
    expect(screen.queryByTestId('integration-card')).not.toBeInTheDocument()
  })
})
