import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '@/lib/api'
import { TerminalPage } from './terminal'

const authMocks = vi.hoisted(() => ({
  state: {} as {
    data?: { role: string }
    isLoading: boolean
    isError: boolean
    error: Error | null
    isFetching: boolean
    refetch: () => unknown
  },
  refetch: vi.fn(),
}))

vi.mock('@/lib/api', () => ({ api: { get: vi.fn() } }))
vi.mock('@/hooks/useAuth', () => ({ useCurrentUser: () => authMocks.state }))
vi.mock('@/hooks/useNow', () => ({ useNow: () => Date.parse('2026-08-28T01:00:00Z') }))
vi.mock('@/components/modules/web-terminal', () => ({
  WebTerminal: ({ node }: { node: string }) => <div data-testid="web-terminal">{node}</div>,
}))
vi.mock('@/components/modules/command-palette', () => ({
  CommandPalette: () => null,
}))

function setAuth(role?: string, error?: Error) {
  authMocks.state = {
    data: role ? { role } : undefined,
    isLoading: !role && !error,
    isError: Boolean(error),
    error: error ?? null,
    isFetching: false,
    refetch: authMocks.refetch,
  }
}

function renderTerminal(route = '/terminal') {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <MemoryRouter initialEntries={[route]}>
      <QueryClientProvider client={client}>
        <TerminalPage />
      </QueryClientProvider>
    </MemoryRouter>,
  )
}

describe('Terminal permission and availability boundaries', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    localStorage.clear()
    let tab = 0
    vi.stubGlobal('crypto', { randomUUID: () => `terminal-tab-${++tab}` })
  })

  it('does not open a writable shell for a non-admin account', async () => {
    setAuth('viewer')
    renderTerminal()

    expect(await screen.findByText('Terminal access denied')).toBeInTheDocument()
    expect(screen.getByText('viewer').closest('p')).toHaveTextContent('The current viewer role was not upgraded to a shell session.')
    expect(screen.queryByTestId('web-terminal')).not.toBeInTheDocument()
    expect(screen.getByTitle('Command Palette (Ctrl+`)')).toBeDisabled()
    expect(screen.getByLabelText('Clear active terminal display')).toBeDisabled()
    expect(screen.getByLabelText('New terminal tab')).toBeDisabled()
    expect(api.get).not.toHaveBeenCalled()
  })

  it('shows account verification failure separately and retries it', async () => {
    setAuth(undefined, new Error('HTTP 503: account state unavailable'))
    renderTerminal()

    expect(await screen.findByText('Terminal permission is unavailable')).toBeInTheDocument()
    expect(screen.getByText('HTTP 503: account state unavailable')).toBeInTheDocument()
    expect(screen.queryByText('Terminal access denied')).not.toBeInTheDocument()
    expect(screen.queryByTestId('web-terminal')).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Retry permission' }))
    expect(authMocks.refetch).toHaveBeenCalledTimes(1)
    expect(api.get).not.toHaveBeenCalled()
  })

  it('opens the local terminal only after an admin identity is known', async () => {
    setAuth('admin')
    vi.mocked(api.get).mockResolvedValue([])
    renderTerminal()

    expect(await screen.findByTestId('web-terminal')).toHaveTextContent('local')
    expect(screen.queryByText('Terminal access denied')).not.toBeInTheDocument()
    expect(screen.getByTitle('Command Palette (Ctrl+`)')).toBeEnabled()
    await waitFor(() => expect(api.get).toHaveBeenCalledWith('/nodes'))
  })

  it('does not infer a remote shell when node status is unavailable', async () => {
    setAuth('admin')
    vi.mocked(api.get).mockRejectedValue(new Error('node inventory unavailable'))
    renderTerminal('/terminal?node=edge-1')

    expect(await screen.findByText('edge-1 status is unavailable')).toBeInTheDocument()
    expect(screen.getByText('The panel could not verify the managed node, so it did not open a remote shell.')).toBeInTheDocument()
    expect(screen.queryByTestId('web-terminal')).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Retry status' }))
    await waitFor(() => expect(api.get).toHaveBeenCalledTimes(2))
  })
})
