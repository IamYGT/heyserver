import { toast } from 'sonner'

const BASE_URL = '/api'

class ApiError extends Error {
  status: number

  constructor(status: number, message: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
  }
}

export function getToken(): string | null {
  return localStorage.getItem('hserver_token')
}

export function setToken(token: string) {
  localStorage.setItem('hserver_token', token)
}

export function clearToken() {
  localStorage.removeItem('hserver_token')
}

export async function authenticatedFetch(
  path: string,
  options: RequestInit = {},
): Promise<Response> {
  const headers = new Headers(options.headers)
  const token = getToken()
  if (token) headers.set('Authorization', `Bearer ${token}`)

  const response = await fetch(`${BASE_URL}${path}`, {
    ...options,
    credentials: 'include',
    headers,
  })
  if (response.status === 401) {
    clearToken()
    if (!path.includes('/auth/')) window.location.href = '/login'
  }
  return response
}

async function request<T>(
  path: string,
  options: RequestInit = {},
): Promise<T> {
  const token = getToken()
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    ...(options.headers as Record<string, string>),
  }

  if (token) {
    headers['Authorization'] = `Bearer ${token}`
  }

  let response: Response
  try {
    response = await fetch(`${BASE_URL}${path}`, {
      ...options,
      credentials: 'include',
      headers,
    })
  } catch {
    toast.error('Network error — check your connection', { id: 'network-error' })
    throw new ApiError(0, 'Network error')
  }

  if (response.status === 401) {
    clearToken()
    if (!path.includes('/auth/')) {
      toast.error('Session expired — please log in again', { id: 'session-expired' })
      window.location.href = '/login'
    }
  }

  if (response.status === 403) {
    toast.error('Permission denied', { id: 'permission-denied' })
  }

  if (!response.ok) {
    const text = await response.text().catch(() => response.statusText)
    let message = text
    try {
      const parsed = JSON.parse(text) as { error?: string }
      if (parsed.error) message = parsed.error
    } catch {
      // keep raw text
    }
    throw new ApiError(response.status, message)
  }

  const text = await response.text()
  return text ? JSON.parse(text) : ({} as T)
}

export const api = {
  get: <T>(path: string) => request<T>(path),
  post: <T>(path: string, body?: unknown) =>
    request<T>(path, { method: 'POST', body: JSON.stringify(body) }),
  put: <T>(path: string, body?: unknown) =>
    request<T>(path, { method: 'PUT', body: JSON.stringify(body) }),
  delete: <T>(path: string, body?: unknown) =>
    request<T>(path, { method: 'DELETE', body: body === undefined ? undefined : JSON.stringify(body) }),
}

export { ApiError }
