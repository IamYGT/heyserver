import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '@/lib/api'
import DatabasePage from './Database'

vi.mock('@/lib/api', () => ({
  api: {
    get: vi.fn(),
    post: vi.fn(),
    delete: vi.fn(),
  },
}))

function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <DatabasePage />
    </QueryClientProvider>,
  )
}

const emptyInventory = {
  databases: [],
  sources: { postgresql: { available: true, state: 'healthy' } },
}

describe('Database page failure states', () => {
  beforeEach(() => vi.clearAllMocks())

  it('does not turn an unavailable inventory into healthy zero metrics', async () => {
    vi.mocked(api.get).mockRejectedValue(new Error('Database API unavailable'))

    renderPage()

    expect(await screen.findByText('Database API is unavailable')).toBeInTheDocument()
    expect(screen.queryByText('No databases found')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'New Database' })).toBeDisabled()
    expect(screen.getAllByText('—')).toHaveLength(3)
  })

  it('explains how to recover a missing PostgreSQL client', async () => {
    vi.mocked(api.get).mockResolvedValue({
      databases: [],
      sources: {
        postgresql: {
          available: false,
          state: 'client-missing',
          error: 'psql: command not found',
        },
      },
    })

    renderPage()

    expect(await screen.findByText('PostgreSQL client is not installed')).toBeInTheDocument()
    expect(screen.getByText('postgresql-client')).toBeInTheDocument()
    expect(screen.getByText('psql: command not found')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'New Database' })).toBeDisabled()
    expect(screen.queryByText('No databases found')).not.toBeInTheDocument()
  })

  it('keeps MariaDB mutations paused while the local service is stopped', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/databases?engine=postgresql') return Promise.resolve(emptyInventory)
      if (path === '/databases?engine=mariadb') {
        return Promise.resolve({
          databases: [],
          sources: {
            mariadb: {
              available: false,
              state: 'stopped',
              error: "Can't connect to local server through socket '/run/mysqld/mysqld.sock'",
            },
          },
        })
      }
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })

    renderPage()

    await screen.findByText('No databases found')
    fireEvent.click(screen.getByRole('button', { name: 'MariaDB' }))

    expect(await screen.findByText('MariaDB is not accepting local connections')).toBeInTheDocument()
    expect(screen.getByText('systemctl status mariadb')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'New Database' })).toBeDisabled()
  })

  it('keeps user inventory failures distinct from an empty user list', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/databases?engine=postgresql') return Promise.resolve(emptyInventory)
      if (path === '/databases/users?engine=postgresql') return Promise.reject(new Error('Database users API unavailable'))
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })

    renderPage()

    await screen.findByText('No databases found')
    fireEvent.click(screen.getByRole('button', { name: 'Users' }))

    expect(await screen.findByText('Database users could not be loaded')).toBeInTheDocument()
    expect(screen.getByText('Database users API unavailable')).toBeInTheDocument()
    expect(screen.queryByText('No database users found')).not.toBeInTheDocument()
  })

  it('keeps backup inventory failures distinct from an empty backup directory', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/databases?engine=postgresql') return Promise.resolve(emptyInventory)
      if (path === '/databases/pgm-backups') return Promise.reject(new Error('Backup API unavailable'))
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })

    renderPage()

    await screen.findByText('No databases found')
    fireEvent.click(screen.getByRole('button', { name: 'Backups' }))

    expect(await screen.findByText('Database backups could not be loaded')).toBeInTheDocument()
    expect(screen.getByText('Backup API unavailable')).toBeInTheDocument()
    expect(screen.queryByText('No backups found')).not.toBeInTheDocument()
  })

  it('does not leave failed backup contents in a permanent loading state', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/databases?engine=postgresql') return Promise.resolve(emptyInventory)
      if (path === '/databases/pgm-backups') {
        return Promise.resolve([{
          name: '20260826_120000',
          path: '/srv/hserver/database-backups/20260826_120000',
          size: '12 MB',
          databases: 1,
          createdAt: '2026-08-26 12:00:00',
        }])
      }
      if (path === '/databases/pgm-backup-files/20260826_120000') {
        return Promise.reject(new Error('Backup contents API unavailable'))
      }
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })

    renderPage()

    await screen.findByText('No databases found')
    fireEvent.click(screen.getByRole('button', { name: 'Backups' }))
    fireEvent.click(await screen.findByText('20260826_120000'))

    expect(await screen.findByText('Backup contents could not be loaded.')).toBeInTheDocument()
    expect(screen.getByText('Backup contents API unavailable')).toBeInTheDocument()
    expect(screen.queryByText('Loading...')).not.toBeInTheDocument()
  })
})
