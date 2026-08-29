import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { PortableConfigurationSection, type PortableConfigurationBundle } from './PortableConfigurationSection'

const mocks = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  toastSuccess: vi.fn(),
  toastError: vi.fn(),
  user: { role: 'admin' },
}))

vi.mock('@/lib/api', () => ({
  api: { get: mocks.get, post: mocks.post },
}))

vi.mock('@/hooks/useAuth', () => ({
  useCurrentUser: () => ({ data: mocks.user }),
}))

vi.mock('sonner', () => ({
  toast: { success: mocks.toastSuccess, error: mocks.toastError },
}))

const bundle: PortableConfigurationBundle = {
  schema_version: 1,
  exported_at: '2026-08-26T20:00:00Z',
  source_version: 'v1.0.0',
  settings: { hostnameDisplay: 'Community server' },
}

const preview = {
  schema_version: 1,
  imported_keys: 1,
  changed_keys: 1,
  unchanged_keys: 0,
  changes: [{ key: 'hostnameDisplay', current: 'Old server', proposed: 'Community server' }],
}

function renderSection() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <PortableConfigurationSection />
    </QueryClientProvider>,
  )
}

describe('PortableConfigurationSection', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mocks.user.role = 'admin'
    mocks.get.mockResolvedValue(bundle)
    mocks.post.mockImplementation((path: string) => {
      if (path === '/settings/portable/preview' || path === '/settings/portable/import') return Promise.resolve(preview)
      return Promise.reject(new Error(`unexpected POST ${path}`))
    })
    vi.stubGlobal('URL', {
      ...URL,
      createObjectURL: vi.fn(() => 'blob:portable-config'),
      revokeObjectURL: vi.fn(),
    })
    vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => undefined)
    vi.spyOn(window, 'confirm').mockReturnValue(true)
  })

  it('downloads, previews, and applies the exact schema-v1 bundle', async () => {
    renderSection()

    fireEvent.click(screen.getByRole('button', { name: /download json/i }))
    await waitFor(() => expect(mocks.get).toHaveBeenCalledWith('/settings/portable'))
    expect(HTMLAnchorElement.prototype.click).toHaveBeenCalled()

    const file = new File([JSON.stringify(bundle)], 'portable.json', { type: 'application/json' })
    fireEvent.change(screen.getByLabelText(/portable configuration json file/i), { target: { files: [file] } })
    await waitFor(() => expect(mocks.post).toHaveBeenCalledWith('/settings/portable/preview', bundle))
    expect(await screen.findByText(/schema v1 validated/i)).toBeInTheDocument()
    expect(screen.getByText(/hostnameDisplay/)).toBeInTheDocument()

    fireEvent.click(screen.getByRole('checkbox', { name: /apply only the reviewed schema-v1 settings/i }))
    fireEvent.click(screen.getByRole('button', { name: /apply reviewed changes/i }))

    await waitFor(() => {
      expect(mocks.post).toHaveBeenCalledWith('/settings/portable/import', { bundle, confirmed: true })
    })
    expect(window.confirm).toHaveBeenCalledWith('Apply 1 portable setting changes from portable.json?')
  })

  it('does not expose portable controls to non-admin users', () => {
    mocks.user.role = 'manager'
    renderSection()

    expect(screen.queryByText('Portable Configuration')).not.toBeInTheDocument()
    expect(mocks.get).not.toHaveBeenCalled()
  })
})
