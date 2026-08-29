import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '@/lib/api'
import UsersPage from './Users'
import type { AuthUser } from '@/lib/types'

const authMocks = vi.hoisted(() => ({
  state: {} as {
    data?: AuthUser
    isLoading: boolean
    isError: boolean
    error: Error | null
    isFetching: boolean
    refetch: () => unknown
  },
  refetch: vi.fn(),
}))

vi.mock('@/lib/api', () => ({
  api: {
    get: vi.fn(),
    post: vi.fn(),
    put: vi.fn(),
    delete: vi.fn(),
  },
}))
vi.mock('@/hooks/useAuth', () => ({ useCurrentUser: () => authMocks.state }))

function setIdentity(role?: AuthUser['role'], error?: Error) {
  authMocks.state = {
    data: role ? user(1, 'Alice Admin', 'alice@example.com', role) : undefined,
    isLoading: !role && !error,
    isError: Boolean(error),
    error: error ?? null,
    isFetching: false,
    refetch: authMocks.refetch,
  }
}

function renderUsers() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })

  return render(
    <QueryClientProvider client={client}>
      <UsersPage />
    </QueryClientProvider>,
  )
}

function user(
  id: number,
  name: string,
  email: string,
  role: AuthUser['role'] = id === 1 ? 'admin' : 'manager',
): AuthUser {
  return {
    id,
    name,
    email,
    role,
    createdAt: '2026-08-27T00:00:00Z',
    updatedAt: '2026-08-27T00:00:00Z',
  }
}

describe('Users page state boundaries', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    localStorage.clear()
    setIdentity('admin')
  })

  it('keeps the page locked while the current identity is loading', async () => {
    setIdentity()

    renderUsers()

    expect(await screen.findByText('Checking user management permission')).toBeInTheDocument()
    expect(screen.getByText('Checking your account permission…')).toBeInTheDocument()
    expect(screen.queryByText('User management access denied')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /add user/i })).not.toBeInTheDocument()
    expect(api.get).not.toHaveBeenCalled()
  })

  it.each(['manager', 'viewer'] as const)('denies %s accounts without requesting or exposing user mutations', async (role) => {
    setIdentity(role)

    renderUsers()

    expect(await screen.findByText('User management access denied')).toBeInTheDocument()
    expect(screen.getByText(role).closest('p')).toHaveTextContent(`Your ${role} account was not granted mutation controls.`)
    expect(screen.queryByRole('button', { name: /add user/i })).not.toBeInTheDocument()
    expect(screen.queryByTitle('Edit user')).not.toBeInTheDocument()
    expect(screen.queryByTitle('Change password')).not.toBeInTheDocument()
    expect(screen.queryByTitle('Delete user')).not.toBeInTheDocument()
    expect(api.get).not.toHaveBeenCalled()
  })

  it('fails closed when the current identity cannot be verified', async () => {
    setIdentity(undefined, new Error('identity API unavailable'))

    renderUsers()

    expect(await screen.findByText('User management identity unavailable')).toBeInTheDocument()
    expect(screen.getByText('User management identity is unavailable.')).toBeInTheDocument()
    expect(screen.getByText('identity API unavailable')).toBeInTheDocument()
    expect(screen.queryByText('User management access denied')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /add user/i })).not.toBeInTheDocument()
    expect(api.get).not.toHaveBeenCalled()

    fireEvent.click(screen.getByRole('button', { name: 'Retry permission' }))
    expect(authMocks.refetch).toHaveBeenCalledTimes(1)
  })

  it('shows an API 403 as permission denied and keeps mutations unavailable', async () => {
    vi.mocked(api.get).mockRejectedValue(Object.assign(new Error('forbidden'), { status: 403 }))

    renderUsers()

    expect(await screen.findByText('User management access denied')).toBeInTheDocument()
    expect(screen.getByText(/server rejected this account's user-management permission/i)).toBeInTheDocument()
    expect(screen.queryByText('Users could not be loaded.')).not.toBeInTheDocument()
    expect(screen.queryByText('Users unavailable')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /add user/i })).not.toBeInTheDocument()
  })

  it('does not describe a failed users request as an empty user list', async () => {
    vi.mocked(api.get).mockRejectedValue(new Error('users API unavailable'))

    renderUsers()

    expect(await screen.findByText('Users could not be loaded.')).toBeInTheDocument()
    expect(screen.getByText('users API unavailable')).toBeInTheDocument()
    expect(screen.queryByText('No users found')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: /add user/i })).toBeDisabled()
  })

  it('loads the newly selected user into the edit form', async () => {
    vi.mocked(api.get).mockResolvedValue({
      data: [user(1, 'Alice Admin', 'alice@example.com'), user(2, 'Bob Manager', 'bob@example.com')],
    })

    renderUsers()

    await screen.findByText('Alice Admin')
    fireEvent.click(screen.getAllByTitle('Edit user')[0])
    expect(screen.getByDisplayValue('Alice Admin')).toBeInTheDocument()
    expect(screen.getByDisplayValue('alice@example.com')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    fireEvent.click(screen.getAllByTitle('Edit user')[1])

    expect(screen.getByDisplayValue('Bob Manager')).toBeInTheDocument()
    expect(screen.getByDisplayValue('bob@example.com')).toBeInTheDocument()
    expect(screen.queryByDisplayValue('Alice Admin')).not.toBeInTheDocument()
  })

  it('opens admin mutation controls while preserving the self-delete invariant', async () => {
    vi.mocked(api.get).mockResolvedValue({
      data: [user(1, 'Alice Admin', 'alice@example.com'), user(2, 'Bob Manager', 'bob@example.com')],
    })

    renderUsers()

    await screen.findByText('Alice Admin')
    expect(screen.getByRole('button', { name: 'Add User' })).toBeEnabled()
    expect(screen.getAllByTitle('Edit user')).toHaveLength(2)
    expect(screen.getAllByTitle('Change password')).toHaveLength(2)
    expect(screen.getByTitle('Cannot delete yourself')).toBeDisabled()
    expect(screen.getByTitle('Delete user')).toBeEnabled()

    fireEvent.click(screen.getByRole('button', { name: 'Add User' }))
    expect(screen.getByRole('heading', { name: 'Add User' })).toBeInTheDocument()
  })

  it('discards an unsubmitted password when the dialog closes', async () => {
    vi.mocked(api.get).mockResolvedValue({ data: [user(1, 'Alice Admin', 'alice@example.com')] })

    renderUsers()

    await screen.findByText('Alice Admin')
    fireEvent.click(screen.getByTitle('Change password'))
    fireEvent.change(screen.getByPlaceholderText('Min. 8 characters'), { target: { value: 'temporary-secret' } })
    fireEvent.change(screen.getByPlaceholderText('Repeat password'), { target: { value: 'temporary-secret' } })
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))

    fireEvent.click(screen.getByTitle('Change password'))

    expect(screen.getByPlaceholderText('Min. 8 characters')).toHaveValue('')
    expect(screen.getByPlaceholderText('Repeat password')).toHaveValue('')
  })

  it('discards an unsubmitted add-user form when the dialog closes', async () => {
    vi.mocked(api.get).mockResolvedValue({ data: [user(1, 'Alice Admin', 'alice@example.com')] })

    renderUsers()

    await screen.findByText('Alice Admin')
    fireEvent.click(screen.getByRole('button', { name: 'Add User' }))
    fireEvent.change(screen.getByPlaceholderText('John Doe'), { target: { value: 'Temporary User' } })
    fireEvent.change(screen.getByPlaceholderText('john@example.com'), { target: { value: 'temporary@example.com' } })
    fireEvent.change(screen.getByPlaceholderText('Min. 8 characters'), { target: { value: 'temporary-secret' } })
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))

    fireEvent.click(screen.getByRole('button', { name: 'Add User' }))

    expect(screen.getByPlaceholderText('John Doe')).toHaveValue('')
    expect(screen.getByPlaceholderText('john@example.com')).toHaveValue('')
    expect(screen.getByPlaceholderText('Min. 8 characters')).toHaveValue('')
  })
})
