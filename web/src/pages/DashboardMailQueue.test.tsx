import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '@/lib/api'
import { MailQueueWidget } from './Dashboard'

vi.mock('@/lib/api', () => ({
  api: { get: vi.fn() },
}))

function renderWidget() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <MailQueueWidget />
    </QueryClientProvider>,
  )
}

describe('Dashboard mail queue widget', () => {
  beforeEach(() => vi.clearAllMocks())

  it('uses the real queue inventory instead of a missing overview counter', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/mail/queue') {
        return Promise.resolve([
          { id: '1', from: 'a@example.com', to: 'b@example.com', subject: 'One', status: 'queued', created: '2026-08-26T12:00:00Z' },
          { id: '2', from: 'a@example.com', to: 'c@example.com', subject: 'Two', status: 'queued', created: '2026-08-26T12:01:00Z' },
        ])
      }
      if (path === '/mail/service/overview') return Promise.resolve({ status: { running: true, status: 'running' } })
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })

    renderWidget()

    expect(await screen.findByText('2')).toBeInTheDocument()
    expect(screen.getByText('messages queued')).toBeInTheDocument()
    expect(screen.getByText('Mail service: running')).toBeInTheDocument()
  })

  it('does not turn queue API failures into a zero count', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/mail/queue') return Promise.reject(new Error('Queue API unavailable'))
      if (path === '/mail/service/overview') return Promise.resolve({ status: { running: true, status: 'running' } })
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })

    renderWidget()

    expect(await screen.findByText('Mail queue could not be loaded.')).toBeInTheDocument()
    expect(screen.getByText('Queue API unavailable')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Retry queue' })).toBeInTheDocument()
    expect(screen.queryByText('0')).not.toBeInTheDocument()
  })

  it('shows an unavailable service observation separately from the healthy queue', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/mail/queue') return Promise.resolve([])
      if (path === '/mail/service/overview') {
        return Promise.resolve({
          status: { running: false, status: 'unknown' },
          sources: { status: { available: false, error: 'systemd unavailable' } },
        })
      }
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })

    renderWidget()

    expect(await screen.findByText('Mail service status is unavailable.')).toBeInTheDocument()
    expect(screen.getByText('systemd unavailable')).toBeInTheDocument()
    expect(screen.getByText('0')).toBeInTheDocument()
    expect(screen.queryByText('stopped')).not.toBeInTheDocument()
  })

  it('keeps the queue count visible and retries only the failed service status', async () => {
    const overviewResponses = [
      Promise.reject(new Error('Mail overview API unavailable')),
      Promise.resolve({ status: { running: true, status: 'running' } }),
    ]
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/mail/queue') {
        return Promise.resolve([
          { id: '1', from: 'a@example.com', to: 'b@example.com', subject: 'One', status: 'queued', created: '2026-08-26T12:00:00Z' },
        ])
      }
      if (path === '/mail/service/overview') return overviewResponses.shift()!
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })

    renderWidget()

    expect(await screen.findByText('1')).toBeInTheDocument()
    expect(await screen.findByText('Mail service status could not be loaded.')).toBeInTheDocument()
    expect(screen.getByText('Mail overview API unavailable')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Retry status' }))
    expect(await screen.findByText('Mail service: running')).toBeInTheDocument()
    expect(screen.getByText('1')).toBeInTheDocument()
  })

  it('does not hide a loaded queue while service status is still loading', async () => {
    vi.mocked(api.get).mockImplementation((path) => {
      if (path === '/mail/queue') {
        return Promise.resolve([
          { id: '1', from: 'a@example.com', to: 'b@example.com', subject: 'One', status: 'queued', created: '2026-08-26T12:00:00Z' },
          { id: '2', from: 'a@example.com', to: 'c@example.com', subject: 'Two', status: 'queued', created: '2026-08-26T12:01:00Z' },
        ])
      }
      if (path === '/mail/service/overview') return new Promise(() => {})
      return Promise.reject(new Error(`Unexpected GET ${path}`))
    })

    renderWidget()

    expect(await screen.findByText('2')).toBeInTheDocument()
    expect(screen.getByText('messages queued')).toBeInTheDocument()
    expect(screen.getByText('Checking mail service status…')).toBeInTheDocument()
  })
})
