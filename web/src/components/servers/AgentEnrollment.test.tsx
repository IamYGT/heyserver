import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '@/lib/api'
import { AgentEnrollment } from './AgentEnrollment'

vi.mock('@/lib/api', () => ({ api: { post: vi.fn() } }))

function renderEnrollment() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <AgentEnrollment onClose={vi.fn()} onRegistered={vi.fn()} />
    </QueryClientProvider>,
  )
}

describe('AgentEnrollment', () => {
  afterEach(() => {
    vi.restoreAllMocks()
    vi.unstubAllGlobals()
  })

  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(api.post).mockResolvedValue({ node: { id: 'node-1', name: 'Example server' }, token: 'token-value' })
  })

  it('downloads a fail-closed environment with the deploy controls present', async () => {
    let generatedBlob: Blob | undefined
    const anchorClick = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})
    vi.stubGlobal('URL', {
      createObjectURL: vi.fn((value: Blob) => {
        generatedBlob = value
        return 'blob:agent-config'
      }),
      revokeObjectURL: vi.fn(),
    })

    renderEnrollment()
    fireEvent.change(screen.getByLabelText('Node ID'), { target: { value: 'node-1' } })
    fireEvent.change(screen.getByLabelText('Display name'), { target: { value: 'Example server' } })
    fireEvent.click(screen.getByRole('button', { name: 'Create enrollment' }))

    expect(await screen.findByText('token-value')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Download config' }))

    expect(anchorClick).toHaveBeenCalled()
    expect(generatedBlob).toBeInstanceOf(Blob)
    const environment = await generatedBlob!.text()
    expect(environment).toMatch(/^HSERVER_AGENT_ALLOW_PM2_READ=false$/m)
    expect(environment).toMatch(/^HSERVER_AGENT_ALLOWED_PM2_ACTIONS=$/m)
    expect(environment).toMatch(/^HSERVER_AGENT_PM2_BINARY=$/m)
    expect(environment).toMatch(/^HSERVER_AGENT_PM2_HOME=$/m)
    expect(environment).toMatch(/^HSERVER_AGENT_PM2_USER=$/m)
    expect(environment).toMatch(/^HSERVER_AGENT_ALLOW_DEPLOY_READ=false$/m)
    expect(environment).toMatch(/^HSERVER_AGENT_ALLOW_DEPLOY_ACTIONS=false$/m)
    expect(environment).toMatch(/^HSERVER_AGENT_ALLOW_DEPLOY_DOMAIN_READ=false$/m)
    expect(environment).toMatch(/^HSERVER_AGENT_ALLOW_DEPLOY_DOMAIN_ACTIONS=false$/m)
    expect(environment).toMatch(/^HSERVER_AGENT_DEPLOY_PLANS_FILE=$/m)
    expect(environment).toMatch(/^HSERVER_AGENT_DEPLOY_ACME_WEBROOT=$/m)
    expect(environment).toMatch(/^HSERVER_AGENT_DEPLOY_WRITE_ROOTS=$/m)
    expect(environment).not.toMatch(/contabo|ygtlabs|ecutuningportal|cserver/i)
  })

  it('emits only the selected deploy permissions and operator-entered paths', async () => {
    let generatedBlob: Blob | undefined
    vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})
    vi.stubGlobal('URL', {
      createObjectURL: vi.fn((value: Blob) => {
        generatedBlob = value
        return 'blob:agent-config'
      }),
      revokeObjectURL: vi.fn(),
    })

    renderEnrollment()
    fireEvent.change(screen.getByLabelText('Node ID'), { target: { value: 'node-1' } })
    fireEvent.change(screen.getByLabelText('Display name'), { target: { value: 'Example server' } })
    fireEvent.change(screen.getByLabelText('Deploy plans file'), { target: { value: '/srv/config/deploy-plans.json' } })
    fireEvent.change(screen.getByLabelText('ACME webroot'), { target: { value: '/srv/acme-challenges' } })
    fireEvent.change(screen.getByLabelText('Deploy write roots'), { target: { value: '/srv/apps,/var/lib/releases' } })
    fireEvent.click(screen.getByRole('checkbox', { name: /Read deploy targets/ }))
    fireEvent.click(screen.getByRole('checkbox', { name: /Run deploy actions/ }))
    fireEvent.click(screen.getByRole('checkbox', { name: /Read project domains/ }))
    fireEvent.click(screen.getByRole('checkbox', { name: /Manage project domains/ }))
    fireEvent.click(screen.getByRole('button', { name: 'Create enrollment' }))

    expect(await screen.findByText('token-value')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Download config' }))

    const environment = await generatedBlob!.text()
    expect(environment).toMatch(/^HSERVER_AGENT_ALLOW_DEPLOY_READ=true$/m)
    expect(environment).toMatch(/^HSERVER_AGENT_ALLOW_DEPLOY_ACTIONS=true$/m)
    expect(environment).toMatch(/^HSERVER_AGENT_ALLOW_DEPLOY_DOMAIN_READ=true$/m)
    expect(environment).toMatch(/^HSERVER_AGENT_ALLOW_DEPLOY_DOMAIN_ACTIONS=true$/m)
    expect(environment).toMatch(/^HSERVER_AGENT_DEPLOY_PLANS_FILE=\/srv\/config\/deploy-plans\.json$/m)
    expect(environment).toMatch(/^HSERVER_AGENT_DEPLOY_ACME_WEBROOT=\/srv\/acme-challenges$/m)
    expect(environment).toMatch(/^HSERVER_AGENT_DEPLOY_WRITE_ROOTS=\/srv\/apps,\/var\/lib\/releases$/m)
    expect(environment).not.toMatch(/contabo|ygtlabs|ecutuningportal|cserver/i)
  })

  it('enforces deploy capability dependencies when toggles change', () => {
    renderEnrollment()

    const deployRead = screen.getByRole('checkbox', { name: /Read deploy targets/ })
    const deployActions = screen.getByRole('checkbox', { name: /Run deploy actions/ })
    const domainRead = screen.getByRole('checkbox', { name: /Read project domains/ })
    const domainActions = screen.getByRole('checkbox', { name: /Manage project domains/ })

    expect(deployActions).toBeDisabled()
    expect(domainRead).toBeDisabled()
    expect(domainActions).toBeDisabled()

    fireEvent.click(deployRead)
    expect(deployActions).not.toBeDisabled()
    expect(domainRead).not.toBeDisabled()
    fireEvent.click(deployActions)
    fireEvent.click(domainRead)
    expect(domainActions).not.toBeDisabled()
    fireEvent.click(domainActions)

    fireEvent.click(domainRead)
    expect(domainActions).not.toBeChecked()
    expect(domainActions).toBeDisabled()

    fireEvent.click(domainRead)
    fireEvent.click(domainActions)
    fireEvent.click(deployRead)
    expect(deployActions).not.toBeChecked()
    expect(domainRead).not.toBeChecked()
    expect(domainActions).not.toBeChecked()
    expect(deployActions).toBeDisabled()
    expect(domainRead).toBeDisabled()
    expect(domainActions).toBeDisabled()
  })

  it('blocks enrollment when a deploy path is not clean and absolute', () => {
    renderEnrollment()
    fireEvent.change(screen.getByLabelText('Node ID'), { target: { value: 'node-1' } })
    fireEvent.change(screen.getByLabelText('Display name'), { target: { value: 'Example server' } })
    fireEvent.change(screen.getByLabelText('Deploy plans file'), { target: { value: '/srv/../deploy-plans.json' } })

    expect(screen.getByText('Use a SAFE_PATH file (clean absolute, ASCII, max 4096 bytes; not / or trailing /).')).toBeInTheDocument()
    const submit = screen.getByRole('button', { name: 'Create enrollment' })
    expect(submit).toBeDisabled()
    fireEvent.click(submit)
    expect(api.post).not.toHaveBeenCalled()
  })

  it('rejects duplicate and oversized write-root lists before enrollment', () => {
    renderEnrollment()
    fireEvent.change(screen.getByLabelText('Node ID'), { target: { value: 'node-1' } })
    fireEvent.change(screen.getByLabelText('Display name'), { target: { value: 'Example server' } })

    const writeRoots = screen.getByLabelText('Deploy write roots')
    const roots = Array.from({ length: 17 }, (_, index) => `/srv/apps-${index}`).join(',')
    fireEvent.change(writeRoots, { target: { value: roots } })
    expect(screen.getByText('Use at most 16 clean absolute directories.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Create enrollment' })).toBeDisabled()

    fireEvent.change(writeRoots, { target: { value: '/srv/apps,/srv/apps' } })
    expect(screen.getByText('Write roots must be unique.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Create enrollment' })).toBeDisabled()
    fireEvent.click(screen.getByRole('button', { name: 'Create enrollment' }))
    expect(api.post).not.toHaveBeenCalled()
  })
})
