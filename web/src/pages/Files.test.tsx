import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import Files from './Files'
import { api } from '@/lib/api'
import { fileRootLabel } from '@/lib/fileRoots'

vi.mock('@/lib/api', () => ({
  api: {
    get: vi.fn(),
    post: vi.fn(),
    put: vi.fn(),
    delete: vi.fn(),
  },
}))

vi.mock('@monaco-editor/react', () => ({
  default: ({ value, onChange }: { value?: string; onChange: (value: string) => void }) => (
    <textarea aria-label="File editor" value={value ?? ''} onChange={(event) => onChange(event.target.value)} />
  ),
}))

function renderFiles() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={queryClient}>
      <Files />
    </QueryClientProvider>,
  )
}

function entry(name: string) {
  return {
    name,
    path: `/srv/apps/${name}`,
    type: 'file',
    size: 4,
    permissions: '-rw-r--r--',
    owner: 'hserver',
    group: 'hserver',
    modified: '2026-08-27T00:00:00Z',
  }
}

describe('File manager observation boundaries', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('pauses mutations instead of describing a failed listing as empty', async () => {
    vi.mocked(api.get).mockImplementation(async (url: string) => {
      if (url === '/files') return { roots: ['/srv/apps'], entries: [] }
      throw new Error('permission denied for /srv/apps')
    })

    renderFiles()

    expect(await screen.findByText('Directory contents could not be observed')).toBeInTheDocument()
    expect(screen.getByText('permission denied for /srv/apps')).toBeInTheDocument()
    expect(screen.queryByText('Empty directory')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'New Folder' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'New File' })).toBeDisabled()

    fireEvent.click(screen.getByRole('button', { name: 'Retry detection' }))
    await waitFor(() => expect(api.get).toHaveBeenCalledTimes(3))
  })

  it('keeps root discovery failures distinct from an unconfigured root set', async () => {
    vi.mocked(api.get).mockRejectedValue(new Error('file root inventory unavailable'))

    renderFiles()

    expect(await screen.findByText('File roots could not be observed')).toBeInTheDocument()
    expect(screen.getByText('file root inventory unavailable')).toBeInTheDocument()
    expect(screen.queryByText('No file roots are configured')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'New Folder' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'New File' })).toBeDisabled()
  })

  it('unmounts the old draft before a different file becomes writable', async () => {
    let resolveSecondFile: ((value: { path: string; content: string }) => void) | undefined
    const secondFile = new Promise<{ path: string; content: string }>((resolve) => {
      resolveSecondFile = resolve
    })

    vi.mocked(api.get).mockImplementation(async (url: string) => {
      if (url === '/files') return { roots: ['/srv/apps'], entries: [] }
      if (url.startsWith('/files?path=')) {
        return { path: '/srv/apps', entries: [entry('a.txt'), entry('b.txt')] }
      }
      if (url.includes('a.txt')) return { path: '/srv/apps/a.txt', content: 'alpha' }
      if (url.includes('b.txt')) return secondFile
      throw new Error(`unexpected request: ${url}`)
    })

    renderFiles()

    fireEvent.click(await screen.findByText('a.txt'))
    const editor = await screen.findByRole('textbox', { name: 'File editor' })
    expect(editor).toHaveValue('alpha')
    fireEvent.change(editor, { target: { value: 'unsaved alpha' } })
    expect(screen.getByRole('button', { name: 'Save' })).toBeEnabled()

    fireEvent.click(screen.getByText('b.txt'))

    expect(screen.getByText('Loading file...')).toBeInTheDocument()
    expect(screen.queryByDisplayValue('unsaved alpha')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled()
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(api.put).not.toHaveBeenCalled()

    resolveSecondFile?.({ path: '/srv/apps/b.txt', content: 'bravo' })
    await waitFor(() => expect(screen.getByRole('textbox', { name: 'File editor' })).toHaveValue('bravo'))
  })

  it('loads the selected entry name into the reusable rename dialog', async () => {
    vi.mocked(api.get).mockImplementation(async (url: string) => {
      if (url === '/files') return { roots: ['/srv/apps'], entries: [] }
      return { path: '/srv/apps', entries: [entry('release.conf')] }
    })

    renderFiles()

    await screen.findByText('release.conf')
    fireEvent.click(screen.getByTitle('Rename'))

    expect(screen.getByPlaceholderText('new-name')).toHaveValue('release.conf')
  })

  it('keeps saving disabled when the selected file cannot be read', async () => {
    vi.mocked(api.get).mockImplementation(async (url: string) => {
      if (url === '/files') return { roots: ['/srv/apps'], entries: [] }
      if (url.startsWith('/files?path=')) return { path: '/srv/apps', entries: [entry('broken.conf')] }
      throw new Error('read access denied for broken.conf')
    })

    renderFiles()

    fireEvent.click(await screen.findByText('broken.conf'))

    expect(await screen.findByText('File contents could not be observed')).toBeInTheDocument()
    expect(screen.getByText('read access denied for broken.conf')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled()
  })

  it('labels known configuration roots before applying the first-root fallback', () => {
    expect(fileRootLabel('/etc/nginx', 0)).toBe('Nginx Config')
  })
})
