import { beforeEach, describe, expect, it, vi } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { api } from '@/lib/api'
import { TotpRequiredError, useLogin } from './useAuth'

vi.mock('@/lib/api', () => ({
  api: {
    get: vi.fn(),
    post: vi.fn(),
    put: vi.fn(),
    delete: vi.fn(),
  },
  setToken: vi.fn(),
  clearToken: vi.fn(),
}))

function createWrapper() {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  })

  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  )
}

describe('TotpRequiredError', () => {
  it('stores email and credentials', () => {
    const credentials = { email: 'user@example.com', password: 'secret' }
    const error = new TotpRequiredError('user@example.com', credentials)

    expect(error).toBeInstanceOf(Error)
    expect(error.name).toBe('TotpRequiredError')
    expect(error.message).toBe('TOTP required')
    expect(error.email).toBe('user@example.com')
    expect(error.credentials).toEqual(credentials)
  })
})

describe('useLogin', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('throws TotpRequiredError when login response requires totp', async () => {
    vi.mocked(api.post).mockResolvedValue({
      requires_totp: true,
      email: 'user@example.com',
    })

    const credentials = { email: 'user@example.com', password: 'secret' }
    const { result } = renderHook(() => useLogin(), { wrapper: createWrapper() })

    result.current.mutate(credentials)

    await waitFor(() => {
      expect(result.current.isError).toBe(true)
    })

    expect(result.current.error).toBeInstanceOf(TotpRequiredError)
    const error = result.current.error as TotpRequiredError
    expect(error.email).toBe('user@example.com')
    expect(error.credentials).toEqual(credentials)
  })
})
