import { describe, expect, it } from 'vitest'
import { buildOperationHistory } from './operationHistory'

describe('operation history', () => {
  const audits = [
    { id: 1, action: 'memory_optimize', resource: 'system', details: 'filesystem caches dropped', createdAt: '2026-08-25T10:00:00Z' },
    { id: 2, action: 'remote_system_action', resource: 'system', details: 'edge-eu-1: memory-optimize', createdAt: '2026-08-25T11:00:00Z' },
    { id: 3, action: 'remote_deploy_action', resource: 'system', details: 'other: deploy failed', createdAt: '2026-08-25T12:00:00Z' },
    { id: 4, action: 'login', resource: 'auth', details: 'successful login', createdAt: '2026-08-25T13:00:00Z' },
  ]

  it('shows only local system operations for HServer', () => {
    expect(buildOperationHistory('local', audits)).toEqual([
      expect.objectContaining({ id: 'audit:1', label: 'Memory Optimize', status: 'completed', source: 'audit' }),
    ])
  })

  it('merges the selected node audit events and agent results by time', () => {
    const history = buildOperationHistory('edge-eu-1', audits, [{
      id: 91,
      node_id: 'edge-eu-1',
      kind: 'service.action',
      payload: { service: 'nginx.service', action: 'restart' },
      status: 'completed',
      result: { active: 'active', sub: 'running' },
      completed_at: '2026-08-25T12:30:00Z',
    }])

    expect(history.map(item => item.id)).toEqual(['task:91', 'audit:2'])
    expect(history[0]).toMatchObject({ label: 'nginx.service · restart', detail: 'active/running', status: 'completed', source: 'agent' })
  })

  it('derives visible failure and queued states from persisted records', () => {
    const history = buildOperationHistory('local', [
      { id: 5, action: 'disk_cleanup', resource: 'system', details: 'candidate: failed', createdAt: '2026-08-25T14:00:00Z' },
      { id: 6, action: 'disk_analysis', resource: 'system', details: 'scan: queued', createdAt: '2026-08-25T15:00:00Z' },
    ])
    expect(history.map(item => item.status)).toEqual(['queued', 'failed'])
  })

  it('collapses one measured disk cleanup into a single useful receipt', () => {
    const history = buildOperationHistory('local', [
      { id: 11, action: 'disk_cleanup', resource: 'system', details: 'tmp: reclaimed 10485760 bytes', createdAt: '2026-08-25T21:33:40Z' },
      { id: 12, action: 'disk_cleanup', resource: 'system', details: 'npm-cache: failed', createdAt: '2026-08-25T21:33:45Z' },
      { id: 13, action: 'disk_cleanup', resource: 'system', details: 'apt-cache: reclaimed 5242880 bytes', createdAt: '2026-08-25T21:33:49Z' },
    ])

    expect(history).toEqual([
      expect.objectContaining({
        id: 'audit-batch:13',
        label: 'Disk Cleanup',
        detail: '3 cleanup steps · 15 MiB reclaimed · 1 failed',
        status: 'failed',
        timestamp: '2026-08-25T21:33:49Z',
      }),
    ])
  })

  it('keeps separate cleanup runs outside the batching window', () => {
    const history = buildOperationHistory('local', [
      { id: 20, action: 'disk_cleanup', resource: 'system', details: 'tmp: reclaimed 1024 bytes', createdAt: '2026-08-25T21:33:40Z' },
      { id: 21, action: 'disk_cleanup', resource: 'system', details: 'journal: reclaimed 0 bytes', createdAt: '2026-08-25T21:33:45Z' },
      { id: 22, action: 'disk_cleanup', resource: 'system', details: 'npm-cache: reclaimed 2048 bytes', createdAt: '2026-08-25T21:41:39Z' },
    ])

    expect(history.map(item => item.id)).toEqual(['audit:22', 'audit-batch:21'])
  })

  it('reconciles the latest disk analysis receipt with its durable worker result', () => {
    const history = buildOperationHistory('local', [
      { id: 30, action: 'disk_analysis', resource: 'system', details: 'disk-old: queued', createdAt: '2026-08-25T17:46:55Z' },
      { id: 31, action: 'disk_analysis', resource: 'system', details: 'disk-current: queued', createdAt: '2026-08-25T21:33:03Z' },
    ], [], {
      id: 'disk-current',
      status: 'completed',
      message: 'Deep analysis completed with 100 entries',
      created_at: '2026-08-25T21:33:03Z',
      started_at: '2026-08-25T21:33:03Z',
      finished_at: '2026-08-25T21:33:34Z',
    })

    expect(history).toEqual([
      expect.objectContaining({
        id: 'audit:31',
        label: 'Disk Analysis',
        detail: 'Deep analysis completed with 100 entries',
        status: 'completed',
        timestamp: '2026-08-25T21:33:34Z',
      }),
    ])
  })

  it('keeps audit-only analysis history when no matching durable state exists', () => {
    const history = buildOperationHistory('local', [
      { id: 40, action: 'disk_analysis', resource: 'system', details: 'disk-missing: queued', createdAt: '2026-08-25T22:00:00Z' },
    ], [], {
      id: 'disk-other',
      status: 'completed',
      message: 'Different analysis completed',
      created_at: '2026-08-25T21:00:00Z',
    })

    expect(history[0]).toMatchObject({ id: 'audit:40', status: 'queued' })
  })

  it('formats measured cleanup receipts in readable storage units', () => {
    const history = buildOperationHistory('local', [
      { id: 50, action: 'disk_cleanup', resource: 'system', details: 'npm-cache: reclaimed 149479427 bytes', createdAt: '2026-08-25T23:00:00Z' },
      { id: 51, action: 'backup_partial_cleanup', resource: 'system', details: 'removed 15 artifacts; reclaimed 97714900992 bytes', createdAt: '2026-08-25T22:00:00Z' },
    ])

    expect(history).toEqual([
      expect.objectContaining({ id: 'audit:50', detail: 'Npm Cache · 143 MiB reclaimed' }),
      expect.objectContaining({ id: 'audit:51', detail: '15 partial artifacts removed · 91 GiB reclaimed' }),
    ])
  })
})
