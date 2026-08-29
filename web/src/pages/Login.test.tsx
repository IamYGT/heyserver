import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError, api } from '@/lib/api'
import Login from './Login'

const mocks = vi.hoisted(() => ({
  navigate: vi.fn(),
  toastError: vi.fn(),
}))

vi.mock('react-router-dom', () => ({
  useNavigate: () => mocks.navigate,
}))

vi.mock('sonner', () => ({
  toast: { error: mocks.toastError },
}))

const credentials = { email: 'user@example.com', password: 'not-a-secret' }

function renderLogin() {
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  })

  return render(
    <QueryClientProvider client={client}>
      <Login />
    </QueryClientProvider>,
  )
}

function submitForm(control: HTMLElement) {
  const form = control.closest('form')
  if (!form) throw new Error('Expected control to be inside a form')
  fireEvent.submit(form)
}

function fillPasswordForm() {
  fireEvent.change(screen.getByLabelText('Email'), { target: { value: credentials.email } })
  fireEvent.change(screen.getByLabelText('Password'), { target: { value: credentials.password } })
  submitForm(screen.getByLabelText('Password'))
}

const passwordFailures = [
  [401, 'Invalid credentials. Please try again.'],
  [403, 'Invalid credentials. Please try again.'],
  [0, 'Sign-in is temporarily unavailable. Please check your connection and try again.'],
  [503, 'Sign-in is temporarily unavailable. Please check your connection and try again.'],
  [422, 'Sign-in failed. Please try again.'],
] as const

const totpFailures = [
  [401, 'Invalid authentication code. Please try again.'],
  [403, 'Invalid authentication code. Please try again.'],
  [0, 'Two-factor authentication is temporarily unavailable. Please check your connection and try again.'],
  [503, 'Two-factor authentication is temporarily unavailable. Please check your connection and try again.'],
  [422, 'Two-factor authentication failed. Please try again.'],
] as const

const recoveryFailures = [
  [401, 'Invalid recovery code. Please try again.'],
  [403, 'Invalid recovery code. Please try again.'],
  [0, 'Recovery sign-in is temporarily unavailable. Please check your connection and try again.'],
  [503, 'Recovery sign-in is temporarily unavailable. Please check your connection and try again.'],
  [422, 'Recovery sign-in failed. Please try again.'],
] as const

async function expectSingleSafeToast(message: string) {
  await waitFor(() => expect(mocks.toastError).toHaveBeenCalledTimes(1))
  expect(mocks.toastError).toHaveBeenCalledWith(message)
  expect(mocks.toastError).not.toHaveBeenCalledWith(expect.stringContaining('backend secret'))
}

describe('Login failure feedback', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it.each(passwordFailures)('maps password status %s without exposing the response body', async (status, message) => {
    const post = vi.spyOn(api, 'post').mockRejectedValue(new ApiError(status, 'backend secret must not be shown'))

    renderLogin()
    fillPasswordForm()

    await expectSingleSafeToast(message)
    expect(post).toHaveBeenCalledWith('/auth/login', credentials)
  })

  it.each(totpFailures)('maps TOTP status %s without exposing the response body', async (status, message) => {
    const post = vi.spyOn(api, 'post')
      .mockResolvedValueOnce({ requires_totp: true, email: credentials.email })
      .mockRejectedValueOnce(new ApiError(status, 'backend secret must not be shown'))

    renderLogin()
    fillPasswordForm()

    const code = await screen.findByLabelText('Authentication Code')
    fireEvent.change(code, { target: { value: '123456' } })
    submitForm(code)

    await expectSingleSafeToast(message)
    expect(post).toHaveBeenNthCalledWith(2, '/auth/totp-verify', {
      email: credentials.email,
      password: credentials.password,
      code: '123456',
    })
  })

  it.each(recoveryFailures)('maps recovery status %s without exposing the response body', async (status, message) => {
    const post = vi.spyOn(api, 'post')
      .mockResolvedValueOnce({ requires_totp: true, email: credentials.email })
      .mockRejectedValueOnce(new ApiError(status, 'backend secret must not be shown'))

    renderLogin()
    fillPasswordForm()

    await screen.findByLabelText('Authentication Code')
    fireEvent.click(screen.getByRole('button', { name: /use recovery code instead/i }))

    const code = await screen.findByLabelText('Recovery Code')
    fireEvent.change(code, { target: { value: 'ABCD-1234' } })
    submitForm(code)

    await expectSingleSafeToast(message)
    expect(post).toHaveBeenNthCalledWith(2, '/auth/2fa/recovery', {
      email: credentials.email,
      password: credentials.password,
      recovery_code: 'ABCD-1234',
    })
  })
})
