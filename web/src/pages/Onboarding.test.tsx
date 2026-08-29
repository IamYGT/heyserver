import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '@/lib/api'
import OnboardingPage from './Onboarding'

vi.mock('@/lib/api', () => ({
  api: { get: vi.fn(), post: vi.fn(), put: vi.fn() },
}))

vi.mock('sonner', () => ({
  toast: { error: vi.fn(), success: vi.fn() },
}))

function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <MemoryRouter>
      <QueryClientProvider client={client}>
        <OnboardingPage />
      </QueryClientProvider>
    </MemoryRouter>,
  )
}

const systemInfo = {
  hostname: 'panel.example.com',
  os: 'Ubuntu 24.04',
  kernel: '6.8.0',
  arch: 'amd64',
  nginx: '1.26.0',
  php: ['8.4'],
  postgresql: '17',
}

describe('Onboarding progress persistence', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/onboarding') return Promise.resolve({ completed: false, step: 0 })
      if (path === '/system/info') return Promise.resolve(systemInfo)
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })
  })

  it('keeps a failed optimistic step save visible and offers a working retry', async () => {
    let attempts = 0
    vi.mocked(api.post).mockImplementation((path) => {
      if (path !== '/onboarding') return Promise.reject(new Error(`Unexpected POST ${path}`))
      attempts += 1
      return attempts === 1
        ? Promise.reject(new Error('Onboarding state API unavailable'))
        : Promise.resolve({ status: 'ok' })
    })

    renderPage()

    fireEvent.click(await screen.findByRole('button', { name: /let's get started/i }))

    expect(await screen.findByText('Server Profile')).toBeInTheDocument()
    expect(await screen.findByText('Setup progress for step 2 is not saved.')).toBeInTheDocument()
    expect(screen.getByText(/reload recovery may return to the last saved step/i)).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Retry saving progress' }))

    await waitFor(() => expect(screen.queryByText('Setup progress for step 2 is not saved.')).not.toBeInTheDocument())
    expect(api.post).toHaveBeenCalledTimes(2)
    expect(api.post).toHaveBeenLastCalledWith('/onboarding', { completed: false, step: 1 })
  })

  it('blocks another navigation while the current step is being persisted', async () => {
    let resolveSave: ((value: { status: string }) => void) | undefined
    vi.mocked(api.post).mockImplementation((path) => {
      if (path !== '/onboarding') return Promise.reject(new Error(`Unexpected POST ${path}`))
      return new Promise<{ status: string }>((resolve) => { resolveSave = resolve })
    })

    renderPage()

    fireEvent.click(await screen.findByRole('button', { name: /let's get started/i }))

    expect(await screen.findByText('Saving setup progress…')).toBeInTheDocument()
    expect(await screen.findByRole('button', { name: 'Continue' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Back' })).toBeDisabled()

    await act(async () => { resolveSave?.({ status: 'ok' }) })

    await waitFor(() => expect(screen.queryByText('Saving setup progress…')).not.toBeInTheDocument())
    expect(screen.getByRole('button', { name: 'Continue' })).toBeEnabled()
    expect(screen.getByRole('button', { name: 'Back' })).toBeEnabled()
  })

  it('keeps a failed server-settings save visible on the current step', async () => {
    vi.mocked(api.post).mockResolvedValue({ status: 'ok' })
    vi.mocked(api.put).mockRejectedValue(new Error('Settings API unavailable'))

    renderPage()

    fireEvent.click(await screen.findByRole('button', { name: /let's get started/i }))
    fireEvent.click(await screen.findByRole('button', { name: 'Continue' }))

    expect(await screen.findByText('Server settings were not saved')).toBeInTheDocument()
    expect(screen.getByText('Settings API unavailable')).toBeInTheDocument()
    expect(screen.getByText(/press Continue to retry/i)).toBeInTheDocument()
    expect(screen.getByText('Server Profile')).toBeInTheDocument()
  })

  it('keeps a failed first-domain creation visible and retryable', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/onboarding') return Promise.resolve({ completed: false, step: 0 })
      if (path === '/system/info') return Promise.resolve(systemInfo)
      if (path === '/security/score') return Promise.resolve({ score: 80, checks: [] })
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })
    vi.mocked(api.put).mockResolvedValue({ status: 'ok' })
    vi.mocked(api.post).mockImplementation((path) => {
      if (path === '/onboarding') return Promise.resolve({ status: 'ok' })
      if (path === '/domains') return Promise.reject(new Error('Domain provisioning unavailable'))
      return Promise.reject(new Error(`Unexpected POST ${path}`))
    })

    renderPage()

    fireEvent.click(await screen.findByRole('button', { name: /let's get started/i }))
    fireEvent.click(await screen.findByRole('button', { name: 'Continue' }))
    expect(await screen.findByText('Security Checklist')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Continue' }))
    expect(await screen.findByText('Add Your First Domain')).toBeInTheDocument()
    fireEvent.change(screen.getByPlaceholderText('example.com'), { target: { value: 'portal.example.com' } })
    fireEvent.click(screen.getByRole('button', { name: 'Add Domain' }))

    expect(await screen.findByText('The domain was not created')).toBeInTheDocument()
    expect(screen.getByText('Domain provisioning unavailable')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Add Domain' })).toBeEnabled()
    expect(api.post).toHaveBeenCalledWith('/domains', {
      domain: 'portal.example.com',
      type: 'static',
      wwwRedirect: false,
      issueSSL: false,
      createDnsRecord: false,
    })
  })

  it('keeps a failed completion visible on the done screen', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/onboarding') return Promise.resolve({ completed: false, step: 4 })
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })
    vi.mocked(api.post).mockRejectedValue(new Error('Completion API unavailable'))

    renderPage()

    expect(await screen.findByText("You're all set!")).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Go to Dashboard' }))

    expect(await screen.findByText('Setup could not be marked complete')).toBeInTheDocument()
    expect(screen.getByText('Completion API unavailable')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Go to Dashboard' })).toBeEnabled()
  })
})
