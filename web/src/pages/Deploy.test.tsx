import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '@/lib/api'
import Deploy from './Deploy'

const authMocks = vi.hoisted(() => ({ role: 'admin' }))

vi.mock('@/lib/api', () => ({
  api: {
    get: vi.fn(),
    post: vi.fn(),
    put: vi.fn(),
    delete: vi.fn(),
  },
}))

vi.mock('@/hooks/useAuth', () => ({
  useCurrentUser: () => ({ data: { role: authMocks.role } }),
}))

function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })

  return render(
    <QueryClientProvider client={client}>
      <Deploy />
    </QueryClientProvider>,
  )
}

function targetFixture(overrides: Record<string, unknown> = {}) {
  return {
    id: '17',
    name: 'Editable App',
    repoUrl: 'https://github.com/example/editable.git',
    branch: 'main',
    projectDir: '/srv/editable-app',
    environment: 'production',
    deploymentKind: 'compose',
    composeFile: 'compose.yaml',
    deployScript: '',
    webhookProvider: 'github',
    webhookStatus: 'healthy',
    webhookToken: '',
    autoDeploy: true,
    isActive: true,
    createdAt: '2026-08-28T12:00:00Z',
    updatedAt: '2026-08-28T12:00:00Z',
    ...overrides,
  }
}

describe('Deploy page', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    authMocks.role = 'admin'
  })

  it('does not describe an unavailable target inventory as empty', async () => {
    vi.mocked(api.get).mockRejectedValue(new Error('Deploy API unavailable'))

    renderPage()

    expect(await screen.findByText('Deploy targets could not be loaded. Mutating controls are paused.')).toBeInTheDocument()
    expect(screen.getByText('Deploy API unavailable')).toBeInTheDocument()
    expect(screen.queryByText('No deploy targets configured')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Add Target' })).toBeDisabled()
  })

  it('reports an unavailable webhook secret separately from an unconfigured webhook', async () => {
    vi.mocked(api.get)
      .mockResolvedValueOnce([{
        id: '6', name: 'Unavailable Hook', repoUrl: 'git@example.com:team/app.git', branch: 'main',
        projectDir: '/srv/app', deploymentKind: 'script', composeFile: '', deployScript: './deploy.sh',
        webhookProvider: 'github', webhookStatus: 'unavailable', webhookToken: '', autoDeploy: true, isActive: true,
        createdAt: '2026-08-26T12:00:00Z', updatedAt: '2026-08-26T12:00:00Z',
      }])
      .mockResolvedValueOnce([])

    renderPage()

    expect(await screen.findByText('Webhook secret unavailable')).toBeInTheDocument()
    expect(screen.queryByText('GitHub webhook')).not.toBeInTheDocument()
  })

  it('edits a target with a complete request while preserving the write-only webhook secret', async () => {
    const target = targetFixture()
    vi.mocked(api.get).mockImplementation(async (path: string) => {
      if (path === '/deploy/targets') return [target]
      if (path === '/deploy/history') return []
      throw new Error(`unexpected path ${path}`)
    })
    vi.mocked(api.put).mockResolvedValue({ ...target, name: 'Edited App', branch: 'stable' })

    renderPage()

    fireEvent.click(await screen.findByRole('button', { name: 'Edit deploy target for Editable App' }))
    expect(screen.getByPlaceholderText('e.g. my-app')).toHaveValue('Editable App')
    expect(screen.getByPlaceholderText('Leave blank to keep current secret')).toHaveValue('')
    fireEvent.change(screen.getByPlaceholderText('e.g. my-app'), { target: { value: 'Edited App' } })
    fireEvent.change(screen.getByPlaceholderText('main'), { target: { value: 'stable' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save Target' }))

    await waitFor(() => expect(api.put).toHaveBeenCalledWith('/deploy/targets/17', {
      name: 'Edited App',
      repoUrl: 'https://github.com/example/editable.git',
      branch: 'stable',
      projectDir: '/srv/editable-app',
      deploymentKind: 'compose',
      composeFile: 'compose.yaml',
      deployScript: '',
      webhookProvider: 'github',
      webhookToken: '',
      clearWebhookToken: false,
      autoDeploy: true,
      isActive: true,
      expectedUpdatedAt: '2026-08-28T12:00:00Z',
    }))
  })

  it('explicitly clears a webhook secret only with automatic deployment disabled', async () => {
    const target = targetFixture()
    vi.mocked(api.get).mockImplementation(async (path: string) => {
      if (path === '/deploy/targets') return [target]
      if (path === '/deploy/history') return []
      throw new Error(`unexpected path ${path}`)
    })
    vi.mocked(api.put).mockResolvedValue({ ...target, autoDeploy: false, webhookStatus: 'not_configured' })

    renderPage()

    fireEvent.click(await screen.findByRole('button', { name: 'Edit deploy target for Editable App' }))
    fireEvent.click(screen.getByRole('checkbox', { name: /Clear existing webhook secret/ }))
    expect(screen.getByRole('checkbox', { name: /Deploy matching branch pushes automatically/ })).toBeDisabled()
    expect(screen.getByRole('checkbox', { name: /Deploy matching branch pushes automatically/ })).not.toBeChecked()
    fireEvent.click(screen.getByRole('button', { name: 'Save Target' }))

    await waitFor(() => expect(api.put).toHaveBeenCalledWith('/deploy/targets/17', {
      name: 'Editable App',
      repoUrl: 'https://github.com/example/editable.git',
      branch: 'main',
      projectDir: '/srv/editable-app',
      deploymentKind: 'compose',
      composeFile: 'compose.yaml',
      deployScript: '',
      webhookProvider: 'github',
      webhookToken: '',
      clearWebhookToken: true,
      autoDeploy: false,
      isActive: true,
      expectedUpdatedAt: '2026-08-28T12:00:00Z',
    }))
  })

  it('refreshes the target inventory and keeps the editor open after a stale update conflict', async () => {
    const target = targetFixture()
    vi.mocked(api.get).mockImplementation(async (path: string) => {
      if (path === '/deploy/targets') return [target]
      if (path === '/deploy/history') return []
      throw new Error(`unexpected path ${path}`)
    })
    vi.mocked(api.put).mockRejectedValue(Object.assign(new Error('refresh the target before updating it'), { status: 409 }))

    renderPage()
    await screen.findByText('Editable App')
    const targetCallsBefore = vi.mocked(api.get).mock.calls.filter(([path]) => path === '/deploy/targets').length
    fireEvent.click(screen.getByRole('button', { name: 'Edit deploy target for Editable App' }))
    fireEvent.change(screen.getByPlaceholderText('e.g. my-app'), { target: { value: 'Stale App' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save Target' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('refresh the target before updating it')
    await waitFor(() => {
      const targetCalls = vi.mocked(api.get).mock.calls.filter(([path]) => path === '/deploy/targets').length
      expect(targetCalls).toBeGreaterThan(targetCallsBefore)
    })
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(screen.getByRole('button', { name: 'Edit deploy target for Editable App' })).toBeInTheDocument()
  })

  it('requires confirmation before deleting a target and keeps it after a cancelled delete', async () => {
    const target = targetFixture({ id: '18', name: 'Deletable App', webhookStatus: 'not_configured', autoDeploy: false })
    vi.mocked(api.get).mockImplementation(async (path: string) => {
      if (path === '/deploy/targets') return [target]
      if (path === '/deploy/history') return []
      throw new Error(`unexpected path ${path}`)
    })
    vi.mocked(api.delete).mockResolvedValue({})

    renderPage()

    const deleteButton = await screen.findByRole('button', { name: 'Delete deploy target for Deletable App' })
    fireEvent.click(deleteButton)
    expect(await screen.findByText('Delete Deploy Target?')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(api.delete).not.toHaveBeenCalled()

    fireEvent.click(deleteButton)
    fireEvent.click(screen.getByRole('button', { name: 'Delete Target' }))
    await waitFor(() => expect(api.delete).toHaveBeenCalledWith('/deploy/targets/18'))
  })

  it('shows a delete conflict from attached resources without removing the target', async () => {
    const target = targetFixture({ id: '19', name: 'Attached App', webhookStatus: 'not_configured', autoDeploy: false })
    vi.mocked(api.get).mockImplementation(async (path: string) => {
      if (path === '/deploy/targets') return [target]
      if (path === '/deploy/history') return []
      throw new Error(`unexpected path ${path}`)
    })
    vi.mocked(api.delete).mockRejectedValue(Object.assign(new Error('project domains or staging children must be removed first'), { status: 409 }))

    renderPage()

    fireEvent.click(await screen.findByRole('button', { name: 'Delete deploy target for Attached App' }))
    fireEvent.click(await screen.findByRole('button', { name: 'Delete Target' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('project domains or staging children must be removed first')
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(screen.getByRole('button', { name: 'Delete deploy target for Attached App' })).toBeInTheDocument()
    expect(api.delete).toHaveBeenCalledTimes(1)
  })

  it('hides target edit and delete controls from non-admin users', async () => {
    authMocks.role = 'manager'
    const target = targetFixture({ id: '20', name: 'Read Only App' })
    vi.mocked(api.get).mockImplementation(async (path: string) => {
      if (path === '/deploy/targets') return [target]
      if (path === '/deploy/history') return []
      throw new Error(`unexpected path ${path}`)
    })

    renderPage()

    await screen.findByText('Read Only App')
    expect(screen.queryByRole('button', { name: 'Edit deploy target for Read Only App' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Delete deploy target for Read Only App' })).not.toBeInTheDocument()
  })

  it('creates an isolated staging target only from a production target', async () => {
    vi.mocked(api.get).mockImplementation(async (path: string) => {
      if (path === '/deploy/targets') return [{
        id: '21', name: 'Production App', repoUrl: 'https://github.com/example/app.git', branch: 'main',
        projectDir: '/srv/apps/app', environment: 'production', deploymentKind: 'compose', composeFile: 'compose.yaml', deployScript: '',
        webhookProvider: 'github', webhookStatus: 'healthy', webhookToken: '', autoDeploy: true, isActive: true,
        createdAt: '2026-08-27T12:00:00Z', updatedAt: '2026-08-27T12:00:00Z',
      }, {
        id: '22', name: 'Existing Staging', repoUrl: 'https://github.com/example/app.git', branch: 'develop',
        projectDir: '/srv/apps/app-staging', environment: 'staging', sourceTargetId: '21', deploymentKind: 'compose', composeFile: 'compose.yaml', deployScript: '',
        webhookProvider: 'github', webhookStatus: 'not_configured', webhookToken: '', autoDeploy: false, isActive: true,
        createdAt: '2026-08-27T12:05:00Z', updatedAt: '2026-08-27T12:05:00Z',
      }]
      if (path === '/deploy/history') return []
      throw new Error(`unexpected path ${path}`)
    })
    vi.mocked(api.post).mockResolvedValue({
      target: { id: '23', environment: 'staging', sourceTargetId: '21' },
      storageBoundary: 'isolated_project_directory',
      environmentValuesCopied: false,
      webhookSecretCopied: false,
      domainsCopied: false,
      dnsConfigured: false,
    })

    renderPage()

    expect(await screen.findByText('Source target #21')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Create staging environment for Existing Staging' })).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Create staging environment for Production App' }))

    expect(screen.getByText('Explicit isolation boundary')).toBeInTheDocument()
    expect(screen.getByText(/Environment values and webhook signing secrets are not copied/)).toBeInTheDocument()
    expect(screen.getByLabelText('Staging name')).toHaveValue('Production App Staging')
    expect(screen.getByLabelText('Staging branch')).toHaveValue('main')
    expect(screen.getByLabelText('Isolated project directory')).toHaveValue('/srv/apps/app-staging')

    fireEvent.change(screen.getByLabelText('Staging name'), { target: { value: 'App Preview' } })
    fireEvent.change(screen.getByLabelText('Staging branch'), { target: { value: 'develop' } })
    fireEvent.change(screen.getByLabelText('Isolated project directory'), { target: { value: '/srv/apps/app-preview' } })
    fireEvent.click(screen.getByRole('button', { name: 'Create staging environment' }))

    await waitFor(() => expect(api.post).toHaveBeenCalledWith('/deploy/targets/21/staging', {
      name: 'App Preview',
      branch: 'develop',
      projectDir: '/srv/apps/app-preview',
    }))
  })

  it('reads run logs from the backend response envelope', async () => {
    vi.mocked(api.get)
      .mockResolvedValueOnce([{
        id: '7',
        name: 'Example API',
        repoUrl: 'git@example.com:team/api.git',
        branch: 'main',
        projectDir: '/var/www/example-api',
        deploymentKind: 'script',
        composeFile: '',
        deployScript: './deploy.sh',
        webhookProvider: 'github',
        webhookStatus: 'healthy',
        webhookToken: '',
        autoDeploy: true,
        isActive: true,
        createdAt: '2026-08-26T12:00:00Z',
        updatedAt: '2026-08-26T12:00:00Z',
      }])
      .mockResolvedValueOnce([{
        id: '11',
        targetId: '7',
        status: 'success',
        commit: 'abcdef1234567890',
        startedAt: '2026-08-26T12:00:00Z',
      }])
      .mockResolvedValueOnce({ logs: 'release complete' })

    renderPage()

    fireEvent.click(await screen.findByTitle('Deployment History'))
    fireEvent.click(await screen.findByText('abcdef12'))

    expect(await screen.findByText('release complete')).toBeInTheDocument()
    expect(api.get).toHaveBeenCalledWith('/deploy/history/11/logs')
  })

  it('requires a successful compose preflight before enabling deploy actions', async () => {
    vi.mocked(api.get).mockImplementation(async (path: string) => {
      if (path === '/deploy/targets') {
        return [{
          id: '8',
          name: 'Compose App',
          repoUrl: 'https://github.com/example/compose-app.git',
          branch: 'main',
          projectDir: '/srv/compose-app',
          deploymentKind: 'compose',
          composeFile: 'ops/compose.yaml',
          deployScript: '',
          webhookProvider: 'github',
          webhookStatus: 'not_configured',
          webhookToken: '',
          autoDeploy: false,
          isActive: true,
          createdAt: '2026-08-26T12:00:00Z',
          updatedAt: '2026-08-26T12:00:00Z',
        }]
      }
      if (path === '/deploy/history') return []
      if (path === '/deploy/targets/8/preflight') {
        return {
          targetId: '8',
          deploymentKind: 'compose',
          eligible: true,
          checks: [
            { id: 'docker-compose', status: 'pass', message: 'Docker Compose is available' },
            { id: 'compose-config', status: 'pass', message: 'Docker Compose configuration is valid' },
          ],
        }
      }
      throw new Error(`unexpected path ${path}`)
    })

    renderPage()

    expect(await screen.findByText('Docker Compose')).toBeInTheDocument()
    expect(screen.getByText('ops/compose.yaml')).toBeInTheDocument()
    expect(screen.getByTitle('Run a successful preflight before deploying')).toBeDisabled()

    fireEvent.click(screen.getByTitle('Run Deploy Preflight'))

    expect(await screen.findByText('Ready to deploy')).toBeInTheDocument()
    expect(screen.getByText('Docker Compose configuration is valid')).toBeInTheDocument()
    expect(screen.getByTitle('Manual Deploy')).toBeEnabled()
  })

  it('compares the current, deployed, and rollback revisions on demand', async () => {
    vi.mocked(api.get).mockImplementation(async (path: string) => {
      if (path === '/deploy/targets') return [{
        id: '16', name: 'Revision App', repoUrl: 'https://github.com/example/revision-app.git', branch: 'main',
        projectDir: '/srv/revision-app', deploymentKind: 'compose', composeFile: 'compose.yaml', deployScript: '',
        webhookProvider: 'github',
        webhookStatus: 'not_configured',
        webhookToken: '', autoDeploy: false, isActive: true,
        createdAt: '2026-08-26T12:00:00Z', updatedAt: '2026-08-26T12:00:00Z',
      }]
      if (path === '/deploy/history') return []
      if (path === '/deploy/targets/16/revision') return {
        targetId: '16', state: 'ready', branch: 'main',
        currentCommit: 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb',
        deployedCommit: 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb',
        rollbackCommit: 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
        trackedChanges: true, matchesDeployed: true, rollbackAvailable: true,
        commitsAheadRollback: 1, commitsBehindRollback: 0,
        filesChanged: 3, insertions: 24, deletions: 7,
        message: 'Local checkout revision is available. Remote refs were not fetched.',
        checkedAt: '2026-08-27T12:00:00Z',
      }
      throw new Error(`unexpected path ${path}`)
    })

    renderPage()
    fireEvent.click(await screen.findByRole('button', { name: 'Compare revisions for Revision App' }))

    expect(await screen.findByText('Deployment revision comparison')).toBeInTheDocument()
    expect(screen.getByText('Matches latest deployment')).toBeInTheDocument()
    expect(screen.getByText('Tracked checkout changes')).toBeInTheDocument()
    expect(screen.getAllByText('bbbbbbbbbbbb')).toHaveLength(2)
    expect(screen.getByText('aaaaaaaaaaaa')).toBeInTheDocument()
    expect(screen.getByText('1 commit ahead of rollback')).toBeInTheDocument()
    expect(screen.getByText('3 files · +24 −7')).toBeInTheDocument()
    expect(api.get).toHaveBeenCalledWith('/deploy/targets/16/revision')
  })

  it('manages services inside a Compose deploy target', async () => {
    vi.mocked(api.get).mockImplementation(async (path: string) => {
      if (path === '/deploy/targets') return [{
        id: '12', name: 'Compose Project', repoUrl: 'https://github.com/example/project.git', branch: 'main',
        projectDir: '/srv/project', deploymentKind: 'compose', composeFile: 'compose.yaml', deployScript: '',
        webhookProvider: 'github',
        webhookStatus: 'not_configured',
        webhookToken: '', autoDeploy: false, isActive: true,
        createdAt: '2026-08-26T12:00:00Z', updatedAt: '2026-08-26T12:00:00Z',
      }]
      if (path === '/deploy/history') return []
      if (path === '/deploy/targets/12/services') return [{
        service: 'web', container: 'project-web-1', image: 'example/app:latest', state: 'running',
        health: 'healthy', exitCode: 0, ports: ['0.0.0.0:8080->80/tcp'],
      }]
      if (path === '/deploy/targets/12/services/web/logs?tail=200') {
        return { logs: 'web ready\n', tail: 200, truncated: true }
      }
      throw new Error(`unexpected path ${path}`)
    })
    vi.mocked(api.post).mockResolvedValue({ status: 'ok' })

    renderPage()
    fireEvent.click(await screen.findByRole('button', { name: 'Project services for Compose Project' }))

    expect(await screen.findByText('project-web-1 · example/app:latest')).toBeInTheDocument()
    expect(screen.getByText('0.0.0.0:8080->80/tcp')).toBeInTheDocument()
    expect(screen.getByText('healthy')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'View logs for web' }))
    expect(await screen.findByText('web ready')).toBeInTheDocument()
    expect(screen.getByText('1 MiB response limit reached')).toBeInTheDocument()
    expect(api.get).toHaveBeenCalledWith('/deploy/targets/12/services/web/logs?tail=200')

    fireEvent.click(screen.getByRole('button', { name: 'Stop web' }))
    await waitFor(() => expect(api.post).toHaveBeenCalledWith('/deploy/targets/12/services/web/stop'))
  })

  it('stores project environment values without reading them back', async () => {
    vi.mocked(api.get).mockImplementation(async (path: string) => {
      if (path === '/deploy/targets') return [{
        id: '13', name: 'Environment Project', repoUrl: 'https://github.com/example/project.git', branch: 'main',
        projectDir: '/srv/project', deploymentKind: 'compose', composeFile: '', deployScript: '',
        webhookProvider: 'github',
        webhookStatus: 'not_configured',
        webhookToken: '', autoDeploy: false, isActive: true,
        createdAt: '2026-08-26T12:00:00Z', updatedAt: '2026-08-26T12:00:00Z',
      }]
      if (path === '/deploy/history') return []
      if (path === '/deploy/targets/13/environment') return { configured: false, variables: [] }
      throw new Error(`unexpected path ${path}`)
    })
    vi.mocked(api.put).mockResolvedValue({ configured: true, variables: [{ key: 'APP_MODE' }] })
    vi.mocked(api.delete).mockResolvedValue({ configured: false, variables: [] })

    renderPage()
    fireEvent.click(await screen.findByRole('button', { name: 'Project environment for Environment Project' }))
    expect(await screen.findByText('No project variables stored.')).toBeInTheDocument()

    fireEvent.change(screen.getByLabelText('Variable key'), { target: { value: 'app_mode' } })
    fireEvent.change(screen.getByLabelText('New value'), { target: { value: 'production' } })
    fireEvent.click(screen.getByRole('button', { name: 'Store value' }))

    await waitFor(() => expect(api.put).toHaveBeenCalledWith('/deploy/targets/13/environment', {
      key: 'APP_MODE', value: 'production',
    }))
    expect(await screen.findByText('APP_MODE')).toBeInTheDocument()
    expect(screen.getByLabelText('New value')).toHaveValue('')

    fireEvent.click(screen.getByRole('button', { name: 'Remove APP_MODE' }))
    fireEvent.click(screen.getByRole('button', { name: 'Remove' }))
    await waitFor(() => expect(api.delete).toHaveBeenCalledWith('/deploy/targets/13/environment/APP_MODE'))
  })

  it('manages project domains and probes the real loopback upstream', async () => {
    vi.mocked(api.get).mockImplementation(async (path: string) => {
      if (path === '/deploy/targets') return [{
        id: '14', name: 'Domain Project', repoUrl: 'https://github.com/example/project.git', branch: 'main',
        projectDir: '/srv/project', deploymentKind: 'compose', composeFile: '', deployScript: '',
        webhookProvider: 'github',
        webhookStatus: 'not_configured',
        webhookToken: '', autoDeploy: false, isActive: true,
        createdAt: '2026-08-26T12:00:00Z', updatedAt: '2026-08-26T12:00:00Z',
      }]
      if (path === '/deploy/history') return []
      if (path === '/deploy/targets/14/domains') return []
      if (path === '/deploy/targets/14/services') return [{
        service: 'web', container: 'project-web-1', image: 'example/app:latest', state: 'running',
        health: 'healthy', exitCode: 0, ports: ['127.0.0.1:8080->80/tcp'],
      }]
      if (path === '/deploy/targets/14/domains/41/health') return {
        domain: 'app.example.com', upstream: 'http://127.0.0.1:8080', status: 'healthy',
        statusCode: 204, latencyMs: 7, message: 'Loopback upstream returned a successful response.',
        checkedAt: '2026-08-26T12:00:00Z',
      }
      throw new Error(`unexpected path ${path}`)
    })
    vi.mocked(api.post).mockImplementation(async (path: string) => {
      if (path === '/deploy/targets/14/domains') return {
        id: '41', targetId: '14', domain: 'app.example.com', service: 'web', hostPort: 8080,
        upstream: 'http://127.0.0.1:8080', tlsStatus: 'not_configured', tlsMessage: 'TLS is not enabled for this project domain.',
        createdAt: '2026-08-26T12:00:00Z', updatedAt: '2026-08-26T12:00:00Z',
      }
      if (path === '/deploy/targets/14/domains/41/tls') return {
        id: '41', targetId: '14', domain: 'app.example.com', service: 'web', hostPort: 8080,
        upstream: 'http://127.0.0.1:8080', tlsStatus: 'healthy', tlsMessage: 'Managed TLS certificate is valid.',
        tlsExpiresAt: '2026-11-24T12:00:00Z', tlsDaysRemaining: 90,
        createdAt: '2026-08-26T12:00:00Z', updatedAt: '2026-08-26T12:05:00Z',
      }
      throw new Error(`unexpected POST ${path}`)
    })
    vi.mocked(api.delete).mockImplementation(async (path: string) => {
      if (path === '/deploy/targets/14/domains/41/tls') return {
        id: '41', targetId: '14', domain: 'app.example.com', service: 'web', hostPort: 8080,
        upstream: 'http://127.0.0.1:8080', tlsStatus: 'not_configured', tlsMessage: 'TLS is not enabled for this project domain.',
        createdAt: '2026-08-26T12:00:00Z', updatedAt: '2026-08-26T12:06:00Z',
      }
      if (path === '/deploy/targets/14/domains/41') return {}
      throw new Error(`unexpected DELETE ${path}`)
    })

    renderPage()
    fireEvent.click(await screen.findByRole('button', { name: 'Project domains for Domain Project' }))
    expect(await screen.findByText('No project domains configured.')).toBeInTheDocument()

    fireEvent.change(screen.getByLabelText('Domain'), { target: { value: 'app.example.com' } })
    fireEvent.change(screen.getByLabelText('Compose service'), { target: { value: 'web' } })
    fireEvent.change(screen.getByLabelText('Host port'), { target: { value: '8080' } })
    fireEvent.click(screen.getByRole('button', { name: 'Activate domain' }))

    await waitFor(() => expect(api.post).toHaveBeenCalledWith('/deploy/targets/14/domains', {
      domain: 'app.example.com', service: 'web', hostPort: 8080,
    }))
    expect(await screen.findByText('app.example.com')).toBeInTheDocument()
    expect(screen.getByText('TLS not configured')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Probe app.example.com' }))
    expect(await screen.findByText(/Loopback upstream returned a successful response/)).toBeInTheDocument()
    expect(screen.getByText('healthy')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Configure TLS for app.example.com' }))
    fireEvent.change(screen.getByLabelText('ACME account email (optional)'), { target: { value: 'admin@example.com' } })
    fireEvent.click(screen.getByRole('button', { name: 'Issue certificate' }))
    await waitFor(() => expect(api.post).toHaveBeenCalledWith('/deploy/targets/14/domains/41/tls', { email: 'admin@example.com' }))
    expect(await screen.findByText('TLS healthy')).toBeInTheDocument()
    expect(screen.getByText(/Managed TLS certificate is valid/)).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Disable TLS for app.example.com' }))
    fireEvent.click(screen.getByRole('button', { name: 'Disable' }))
    await waitFor(() => expect(api.delete).toHaveBeenCalledWith('/deploy/targets/14/domains/41/tls'))
    expect(await screen.findByText('TLS not configured')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Remove app.example.com' }))
    fireEvent.click(screen.getByRole('button', { name: 'Remove' }))
    await waitFor(() => expect(api.delete).toHaveBeenCalledWith('/deploy/targets/14/domains/41'))
  })

  it('creates a Docker Compose target with a contained compose file', async () => {
    vi.mocked(api.get).mockResolvedValue([])
    vi.mocked(api.post).mockResolvedValue({})

    renderPage()

    await screen.findByText('No deploy targets configured')
    fireEvent.click(screen.getByRole('button', { name: 'Add Target' }))
    fireEvent.change(screen.getByPlaceholderText('e.g. my-app'), { target: { value: 'Compose App' } })
    fireEvent.change(screen.getByPlaceholderText('git@github.com:org/repo.git'), { target: { value: 'git@github.com:example/compose-app.git' } })
    fireEvent.change(screen.getByPlaceholderText('e.g. /srv/apps/example-app'), { target: { value: '/srv/compose-app' } })
    fireEvent.change(screen.getByPlaceholderText('Auto-detect, or e.g. deploy/compose.yaml'), { target: { value: 'ops/compose.yaml' } })
    fireEvent.change(screen.getByPlaceholderText('optional'), { target: { value: 'webhook-secret' } })
    fireEvent.click(screen.getByRole('checkbox', { name: /Deploy matching branch pushes automatically/ }))
    fireEvent.click(screen.getAllByRole('button', { name: 'Add Target' }).at(-1)!)

    await waitFor(() => expect(api.post).toHaveBeenCalledWith('/deploy/targets', {
      name: 'Compose App',
      repoUrl: 'git@github.com:example/compose-app.git',
      branch: 'main',
      projectDir: '/srv/compose-app',
      deploymentKind: 'compose',
      composeFile: 'ops/compose.yaml',
      deployScript: '',
      webhookProvider: 'github',
      webhookToken: 'webhook-secret',
      autoDeploy: true,
    }))
  })

  it('applies an installation template without replacing target identity or webhook settings', async () => {
    vi.mocked(api.get).mockImplementation(async (path: string) => {
      if (path === '/deploy/targets' || path === '/deploy/history') return []
      if (path === '/deploy/templates') return {
        status: 'healthy',
        directory: '/var/lib/hserver/deploy-templates',
        templates: [{
          id: 'node-build',
          name: 'Node.js locked build',
          description: 'Install locked dependencies and build the application.',
          branch: 'stable',
          deploymentKind: 'script',
          composeFile: '',
          deployScript: 'npm ci\nnpm run build\n',
        }],
        issues: [],
      }
      throw new Error(`unexpected path ${path}`)
    })

    renderPage()

    await screen.findByText('No deploy targets configured')
    fireEvent.click(screen.getByRole('button', { name: 'Add Target' }))
    fireEvent.click(screen.getByRole('button', { name: 'Select GitLab webhook provider' }))
    fireEvent.change(screen.getByPlaceholderText('e.g. my-app'), { target: { value: 'Existing Name' } })
    fireEvent.change(screen.getByPlaceholderText('git@github.com:org/repo.git'), { target: { value: 'git@gitlab.com:example/app.git' } })
    fireEvent.change(screen.getByPlaceholderText('e.g. /srv/apps/example-app'), { target: { value: '/srv/existing-app' } })
    fireEvent.change(screen.getByPlaceholderText('whsec_...'), { target: { value: 'whsec_MDEyMzQ1Njc4OWFiY2RlZg==' } })

    await screen.findByRole('option', { name: 'Node.js locked build' })
    fireEvent.change(screen.getByLabelText('Deployment template'), { target: { value: 'node-build' } })

    expect(screen.getByPlaceholderText('e.g. my-app')).toHaveValue('Existing Name')
    expect(screen.getByPlaceholderText('git@github.com:org/repo.git')).toHaveValue('git@gitlab.com:example/app.git')
    expect(screen.getByPlaceholderText('e.g. /srv/apps/example-app')).toHaveValue('/srv/existing-app')
    expect(screen.getByPlaceholderText('whsec_...')).toHaveValue('whsec_MDEyMzQ1Njc4OWFiY2RlZg==')
    expect(screen.getByPlaceholderText('main')).toHaveValue('stable')
    expect(screen.getByPlaceholderText('e.g. ./deploy.sh or npm run build && pm2 restart app')).toHaveValue('npm ci\nnpm run build\n')
    expect(screen.getByText('Install locked dependencies and build the application.')).toBeInTheDocument()
  })

  it('keeps valid templates selectable while reporting invalid installation files', async () => {
    vi.mocked(api.get).mockImplementation(async (path: string) => {
      if (path === '/deploy/targets' || path === '/deploy/history') return []
      if (path === '/deploy/templates') return {
        status: 'unavailable',
        directory: '/var/lib/hserver/deploy-templates',
        templates: [{
          id: 'compose', name: 'Docker Compose', description: '', branch: 'main',
          deploymentKind: 'compose', composeFile: 'compose.yaml', deployScript: '',
        }],
        issues: [{ file: 'broken.json', message: 'id must match the template filename' }],
      }
      throw new Error(`unexpected path ${path}`)
    })

    renderPage()
    await screen.findByText('No deploy targets configured')
    fireEvent.click(screen.getByRole('button', { name: 'Add Target' }))

    expect(await screen.findByText('Some deployment templates are unavailable. Valid templates remain selectable.')).toBeInTheDocument()
    expect(screen.getByText(/id must match the template filename/)).toBeInTheDocument()
    expect(screen.getByRole('option', { name: 'Docker Compose' })).toBeInTheDocument()
    expect(screen.getByLabelText('Deployment template')).not.toBeDisabled()
  })

  it('distinguishes an unconfigured template directory from an unavailable inventory', async () => {
    vi.mocked(api.get).mockImplementation(async (path: string) => {
      if (path === '/deploy/targets' || path === '/deploy/history') return []
      if (path === '/deploy/templates') return {
        status: 'not_configured',
        directory: '/var/lib/hserver/deploy-templates',
        templates: [],
        issues: [],
      }
      throw new Error(`unexpected path ${path}`)
    })

    renderPage()
    await screen.findByText('No deploy targets configured')
    fireEvent.click(screen.getByRole('button', { name: 'Add Target' }))

    expect(await screen.findByText('No installation templates configured. Custom configuration remains available.')).toBeInTheDocument()
    expect(screen.getByText('/var/lib/hserver/deploy-templates')).toBeInTheDocument()
    expect(screen.queryByText(/Some deployment templates are unavailable/)).not.toBeInTheDocument()
  })

  it('creates a GitLab target with a Standard Webhooks signing token', async () => {
    vi.mocked(api.get).mockResolvedValue([])
    vi.mocked(api.post).mockResolvedValue({})

    renderPage()

    await screen.findByText('No deploy targets configured')
    fireEvent.click(screen.getByRole('button', { name: 'Add Target' }))
    fireEvent.click(screen.getByRole('button', { name: 'Select GitLab webhook provider' }))
    fireEvent.change(screen.getByPlaceholderText('e.g. my-app'), { target: { value: 'GitLab App' } })
    fireEvent.change(screen.getByPlaceholderText('git@github.com:org/repo.git'), { target: { value: 'git@gitlab.com:example/gitlab-app.git' } })
    fireEvent.change(screen.getByPlaceholderText('e.g. /srv/apps/example-app'), { target: { value: '/srv/gitlab-app' } })
    fireEvent.change(screen.getByPlaceholderText('whsec_...'), { target: { value: 'whsec_MDEyMzQ1Njc4OWFiY2RlZg==' } })
    fireEvent.click(screen.getByRole('checkbox', { name: /Deploy matching branch pushes automatically/ }))
    fireEvent.click(screen.getAllByRole('button', { name: 'Add Target' }).at(-1)!)

    await waitFor(() => expect(api.post).toHaveBeenCalledWith('/deploy/targets', {
      name: 'GitLab App',
      repoUrl: 'git@gitlab.com:example/gitlab-app.git',
      branch: 'main',
      projectDir: '/srv/gitlab-app',
      deploymentKind: 'compose',
      composeFile: '',
      deployScript: '',
      webhookProvider: 'gitlab',
      webhookToken: 'whsec_MDEyMzQ1Njc4OWFiY2RlZg==',
      autoDeploy: true,
    }))
  })

  it('discards an unsubmitted deploy target and webhook token when the dialog closes', async () => {
    vi.mocked(api.get).mockResolvedValue([])

    renderPage()

    await screen.findByText('No deploy targets configured')
    fireEvent.click(screen.getByRole('button', { name: 'Add Target' }))
    fireEvent.change(screen.getByPlaceholderText('e.g. my-app'), { target: { value: 'Discarded App' } })
    fireEvent.change(screen.getByPlaceholderText('git@github.com:org/repo.git'), { target: { value: 'git@github.com:example/discarded.git' } })
    fireEvent.change(screen.getByPlaceholderText('optional'), { target: { value: 'discarded-webhook-token' } })
    fireEvent.click(screen.getByRole('button', { name: 'Close' }))

    fireEvent.click(screen.getByRole('button', { name: 'Add Target' }))

    expect(screen.getByPlaceholderText('e.g. my-app')).toHaveValue('')
    expect(screen.getByPlaceholderText('git@github.com:org/repo.git')).toHaveValue('')
    expect(screen.getByPlaceholderText('optional')).toHaveValue('')
    expect(screen.getByPlaceholderText('main')).toHaveValue('main')
  })

  it('keeps rollback available when only the current compose config is broken', async () => {
    vi.mocked(api.get).mockImplementation(async (path: string) => {
      if (path === '/deploy/targets') return [{
        id: '9', name: 'Broken App', repoUrl: 'git@example/app.git', branch: 'main',
        projectDir: '/srv/app', deploymentKind: 'compose', composeFile: '', deployScript: '',
        webhookProvider: 'github',
        webhookStatus: 'not_configured',
        webhookToken: '', autoDeploy: false, isActive: true,
        createdAt: '2026-08-26T12:00:00Z', updatedAt: '2026-08-26T12:00:00Z',
      }]
      if (path === '/deploy/history') return [{
        id: '91', targetId: '9', status: 'success', commit: 'new-commit', prevCommit: 'old-commit',
        startedAt: '2026-08-26T12:00:00Z',
      }]
      if (path === '/deploy/targets/9/preflight') return {
        targetId: '9', deploymentKind: 'compose', eligible: false,
        checks: [
          { id: 'active', status: 'pass', message: 'Target is enabled' },
          { id: 'project-directory', status: 'pass', message: 'Project directory is available' },
          { id: 'git-checkout', status: 'pass', message: 'Git checkout is readable' },
          { id: 'docker-compose', status: 'pass', message: 'Docker Compose is available' },
          { id: 'compose-config', status: 'fail', message: 'Docker Compose configuration is invalid' },
        ],
      }
      throw new Error(`unexpected path ${path}`)
    })

    renderPage()
    fireEvent.click(await screen.findByTitle('Run Deploy Preflight'))

    expect(await screen.findByText('Rollback remains available')).toBeInTheDocument()
    expect(screen.getByTitle('Run a successful preflight before deploying')).toBeDisabled()
    expect(screen.getByTitle('Rollback')).toBeEnabled()
  })

  it('shows deferred clone checks without blocking the first deployment', async () => {
    vi.mocked(api.get).mockImplementation(async (path: string) => {
      if (path === '/deploy/targets') return [{
        id: '10', name: 'New App', repoUrl: 'https://github.com/example/new-app.git', branch: 'main',
        projectDir: '/srv/new-app', deploymentKind: 'compose', composeFile: '', deployScript: '',
        webhookProvider: 'github',
        webhookStatus: 'not_configured',
        webhookToken: '', autoDeploy: false, isActive: true,
        createdAt: '2026-08-26T12:00:00Z', updatedAt: '2026-08-26T12:00:00Z',
      }]
      if (path === '/deploy/history') return []
      if (path === '/deploy/targets/10/preflight') return {
        targetId: '10', deploymentKind: 'compose', eligible: true,
        checks: [
          { id: 'git-client', status: 'pass', message: 'Git client is available' },
          { id: 'project-directory', status: 'pending', message: 'Project directory will be created on first deployment' },
          { id: 'git-checkout', status: 'pending', message: 'Repository will be cloned on first deployment' },
          { id: 'docker-compose', status: 'pass', message: 'Docker Compose is available' },
          { id: 'compose-config', status: 'pending', message: 'Compose configuration will be validated after the repository is cloned' },
        ],
      }
      throw new Error(`unexpected path ${path}`)
    })

    renderPage()
    fireEvent.click(await screen.findByTitle('Run Deploy Preflight'))

    expect(await screen.findByText('Ready for first deployment')).toBeInTheDocument()
    expect(screen.getByText('Repository will be cloned on first deployment')).toBeInTheDocument()
    expect(screen.getByTitle('Manual Deploy')).toBeEnabled()
  })
})
