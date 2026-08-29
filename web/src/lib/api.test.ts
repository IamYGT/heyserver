import { beforeEach, describe, expect, it, vi } from 'vitest'
import { toast } from 'sonner'
import { ApiError, api, clearToken, getToken, setToken } from './api'

vi.mock('sonner', () => ({
  toast: {
    error: vi.fn(),
  },
}))

describe('token helpers', () => {
  beforeEach(() => {
    localStorage.clear()
  })

  it('returns null when no token is stored', () => {
    expect(getToken()).toBeNull()
  })

  it('stores and retrieves a token', () => {
    setToken('test-token')
    expect(getToken()).toBe('test-token')
  })

  it('clears the stored token', () => {
    setToken('test-token')
    clearToken()
    expect(getToken()).toBeNull()
  })
})

describe('ApiError', () => {
  it('captures status and message', () => {
    const error = new ApiError(404, 'Not found')

    expect(error.name).toBe('ApiError')
    expect(error.status).toBe(404)
    expect(error.message).toBe('Not found')
  })
})

describe('api request', () => {
  beforeEach(() => {
    localStorage.clear()
    vi.clearAllMocks()
    vi.stubGlobal('fetch', vi.fn())
    Object.defineProperty(window, 'location', {
      value: { href: '' },
      writable: true,
      configurable: true,
    })
  })

  it('clears token and redirects on 401', async () => {
    setToken('secret-token')

    vi.mocked(fetch).mockResolvedValue({
      ok: false,
      status: 401,
      statusText: 'Unauthorized',
      text: () => Promise.resolve('Unauthorized'),
    } as Response)

    await expect(api.get('/users')).rejects.toMatchObject({
      status: 401,
      message: 'Unauthorized',
    })

    expect(getToken()).toBeNull()
    expect(window.location.href).toBe('/login')
    expect(toast.error).toHaveBeenCalledWith(
      'Session expired — please log in again',
      { id: 'session-expired' },
    )
  })

  it('shows permission denied toast on 403', async () => {
    vi.mocked(fetch).mockResolvedValue({
      ok: false,
      status: 403,
      statusText: 'Forbidden',
      text: () => Promise.resolve('Forbidden'),
    } as Response)

    await expect(api.get('/admin')).rejects.toMatchObject({
      status: 403,
      message: 'Forbidden',
    })

    expect(toast.error).toHaveBeenCalledWith('Permission denied', {
      id: 'permission-denied',
    })
  })

  it('keeps raw text when error body is not valid JSON', async () => {
    vi.mocked(fetch).mockResolvedValue({
      ok: false,
      status: 500,
      statusText: 'Internal Server Error',
      text: () => Promise.resolve('not-json-body'),
    } as Response)

    await expect(api.get('/broken')).rejects.toMatchObject({
      status: 500,
      message: 'not-json-body',
    })
  })

  it('returns empty object when response body is empty', async () => {
    vi.mocked(fetch).mockResolvedValue({
      ok: true,
      status: 200,
      statusText: 'OK',
      text: () => Promise.resolve(''),
    } as Response)

    await expect(api.get('/empty')).resolves.toEqual({})
  })

  it('sends an explicit JSON confirmation body with DELETE requests', async () => {
    vi.mocked(fetch).mockResolvedValue({
      ok: true,
      status: 200,
      statusText: 'OK',
      text: () => Promise.resolve('{}'),
    } as Response)

    await api.delete('/databases/postgresql/example', { confirm: 'DROP example' })

    expect(fetch).toHaveBeenCalledWith('/api/databases/postgresql/example', expect.objectContaining({
      method: 'DELETE',
      body: JSON.stringify({ confirm: 'DROP example' }),
    }))
  })

  it('throws ApiError with status 0 on network failure', async () => {
    vi.mocked(fetch).mockRejectedValue(new Error('Network failed'))

    await expect(api.get('/offline')).rejects.toMatchObject({
      status: 0,
      message: 'Network error',
    })

    expect(toast.error).toHaveBeenCalledWith(
      'Network error — check your connection',
      { id: 'network-error' },
    )
  })

  it('does not redirect on 401 for auth paths', async () => {
    setToken('secret-token')

    vi.mocked(fetch).mockResolvedValue({
      ok: false,
      status: 401,
      statusText: 'Unauthorized',
      text: () => Promise.resolve('Invalid credentials'),
    } as Response)

    await expect(api.post('/auth/login', {})).rejects.toMatchObject({
      status: 401,
    })

    expect(getToken()).toBeNull()
    expect(window.location.href).toBe('')
    expect(toast.error).not.toHaveBeenCalledWith(
      'Session expired — please log in again',
      { id: 'session-expired' },
    )
  })
})
