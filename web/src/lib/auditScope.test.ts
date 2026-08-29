import { describe, expect, it } from 'vitest'
import {
  auditEntryTimestamp,
  auditEventPresentation,
  auditMatchesServer,
  groupAuditNotifications,
} from './auditScope'

describe('audit server scope', () => {
  const local = { action: 'swap_reset', resource: 'system', details: 'configured swap targets cycled' }
  const edge = { action: 'remote_system_action', resource: 'system', details: 'edge-eu-1: memory-optimize' }
  const otherRemote = { action: 'remote_system_action', resource: 'system', details: 'db-primary: reboot' }
  const login = { action: 'login', resource: 'auth', details: 'successful login' }

  it('keeps the unscoped audit view complete', () => {
    expect([local, edge, otherRemote, login].filter(entry => auditMatchesServer(entry, 'all'))).toHaveLength(4)
  })

  it('shows only local system operations for HServer', () => {
    expect([local, edge, login].filter(entry => auditMatchesServer(entry, 'local'))).toEqual([local])
  })

  it('isolates any managed node from other nodes', () => {
    expect([local, edge, otherRemote, login].filter(entry => auditMatchesServer(entry, 'edge-eu-1'))).toEqual([edge])
  })
})

describe('audit entry timestamp', () => {
  it('uses the current API createdAt field', () => {
    expect(auditEntryTimestamp({ createdAt: '2026-08-26T01:00:00Z' })).toBe(Date.parse('2026-08-26T01:00:00Z'))
  })

  it('falls back to the legacy timestamp field and rejects invalid values', () => {
    expect(auditEntryTimestamp({ timestamp: '2026-08-25T23:00:00Z' })).toBe(Date.parse('2026-08-25T23:00:00Z'))
    expect(auditEntryTimestamp({ createdAt: 'not-a-date' })).toBe(0)
    expect(auditEntryTimestamp({})).toBe(0)
  })
})

describe('audit notification presentation', () => {
  it('turns a successful local disk receipt into a useful destination and detail', () => {
    expect(auditEventPresentation({
      action: 'disk_cleanup',
      resource: 'system',
      details: 'npm-cache: reclaimed 149479427 bytes',
    })).toEqual({
      title: 'Disk cleanup',
      detail: 'npm-cache: reclaimed 149479427 bytes',
      tone: 'success',
      target: '/disk',
    })
  })

  it('removes the remote scope prefix and highlights failures', () => {
    expect(auditEventPresentation({
      action: 'remote_deploy_action',
      resource: 'system',
      details: 'edge-eu-1: 52fb152a73bd rollback failed',
    })).toEqual({
      title: 'Deploy action',
      detail: '52fb152a73bd rollback failed',
      tone: 'critical',
      target: '/deploy',
    })
  })

  it('identifies queued work and falls back when details are absent', () => {
    expect(auditEventPresentation({
      action: 'remote_backup_run',
      resource: 'system',
      details: 'edge-eu-1: database-export queued',
    }).tone).toBe('info')
    expect(auditEventPresentation({ action: '', resource: 'system' })).toEqual({
      title: 'System event',
      detail: 'system',
      tone: 'neutral',
      target: '/audit',
    })
  })

  it.each([
    ['memory_optimize', '/monitoring'],
    ['swap_reset', '/monitoring'],
    ['remote_system_action', '/monitoring'],
    ['temporary_files_clean', '/disk'],
    ['nginx_service_restart', '/nginx'],
    ['php_pool_save', '/php'],
    ['postgresql_restart', '/databases'],
    ['certbot_renew', '/ssl'],
    ['firewall_rule_delete', '/firewall'],
    ['terminal_session_open', '/terminal'],
  ])('routes %s to its operational module', (action, target) => {
    expect(auditEventPresentation({ action, resource: 'system' }).target).toBe(target)
  })
})

describe('audit notification grouping', () => {
  it('collapses one cleanup batch while preserving a later run and event order', () => {
    const cleanupBatch = Array.from({ length: 12 }, (_, index) => ({
      id: 20 - index,
      action: 'disk_cleanup',
      resource: 'system',
      details: `target-${index}: reclaimed ${index} bytes`,
      userName: 'Admin',
      createdAt: new Date(Date.parse('2026-08-26T21:33:49Z') - index * 800).toISOString(),
    }))
    const entries = [
      {
        id: 21,
        action: 'disk_cleanup',
        resource: 'system',
        details: 'npm-cache: reclaimed 149479427 bytes',
        userName: 'Admin',
        createdAt: '2026-08-26T21:41:39Z',
      },
      ...cleanupBatch,
      {
        id: 8,
        action: 'backup_partial_cleanup',
        resource: 'system',
        details: 'removed 15 artifacts',
        userName: 'Admin',
        createdAt: '2026-08-26T21:33:30Z',
      },
    ]

    expect(groupAuditNotifications(entries)).toEqual([
      { entry: entries[0], count: 1 },
      { entry: cleanupBatch[0], count: 12 },
      { entry: entries.at(-1), count: 1 },
    ])
  })

  it('does not merge adjacent receipts from different actors or actions', () => {
    const entries = [
      { id: 3, action: 'disk_cleanup', resource: 'system', userName: 'Admin', createdAt: '2026-08-26T21:33:49Z' },
      { id: 2, action: 'disk_cleanup', resource: 'system', userName: 'Operator', createdAt: '2026-08-26T21:33:48Z' },
      { id: 1, action: 'swap_reset', resource: 'system', userName: 'Operator', createdAt: '2026-08-26T21:33:47Z' },
    ]

    expect(groupAuditNotifications(entries).map(group => group.count)).toEqual([1, 1, 1])
  })
})
