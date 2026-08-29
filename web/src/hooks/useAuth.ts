import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, setToken, clearToken } from '@/lib/api'
import type { AuthUser, LoginCredentials } from '@/lib/types'

interface LoginResponse {
  token: string
  user: AuthUser
}

export interface TotpRequiredResponse {
  requires_totp: true
  email: string
}

export class TotpRequiredError extends Error {
  email: string
  credentials: LoginCredentials

  constructor(email: string, credentials: LoginCredentials) {
    super('TOTP required')
    this.name = 'TotpRequiredError'
    this.email = email
    this.credentials = credentials
  }
}

export interface TotpVerifyPayload {
  email: string
  password: string
  code: string
}

export interface RecoveryLoginPayload {
  email: string
  password: string
  recovery_code: string
}

export function useCurrentUser() {
  return useQuery<AuthUser>({
    queryKey: ['auth', 'me'],
    queryFn: () => api.get<AuthUser>('/auth/me'),
    retry: false,
    staleTime: 5 * 60 * 1000,
  })
}

export function useLogin() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: async (credentials: LoginCredentials) => {
      const data = await api.post<LoginResponse | TotpRequiredResponse>('/auth/login', credentials)
      if ('requires_totp' in data && data.requires_totp) {
        throw new TotpRequiredError(data.email, credentials)
      }
      return data as LoginResponse
    },
    onSuccess: (data) => {
      setToken(data.token)
      queryClient.setQueryData(['auth', 'me'], data.user)
    },
  })
}

export function useTotpVerify() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (payload: TotpVerifyPayload) =>
      api.post<LoginResponse>('/auth/totp-verify', payload),
    onSuccess: (data) => {
      setToken(data.token)
      queryClient.setQueryData(['auth', 'me'], data.user)
    },
  })
}

export function useRecoveryLogin() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (payload: RecoveryLoginPayload) =>
      api.post<LoginResponse>('/auth/2fa/recovery', payload),
    onSuccess: (data) => {
      setToken(data.token)
      queryClient.setQueryData(['auth', 'me'], data.user)
    },
  })
}

export function useLogout() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: () => api.post('/auth/logout'),
    onSuccess: () => {
      clearToken()
      queryClient.clear()
      window.location.href = '/login'
    },
  })
}
