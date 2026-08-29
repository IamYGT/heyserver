import { describe, expect, it } from 'vitest'
import { localServerHealth, remoteServerHealth } from './serverHealth'
import type { SystemStats } from './types'

function statsWithRootDisk(percentage: number): SystemStats {
  return {
    cpu: { usage: 1, cores: 1, model: 'test' },
    memory: {
      total: 1, used: 0, free: 1, percentage: 0,
      buffers: 0, cached: 0, available: 1,
      swapTotal: 0, swapUsed: 0, swapFree: 0, swapPercentage: 0,
    },
    disk: [{ mount: '/', total: 100, used: percentage, free: 100 - percentage, percentage }],
    load: [0, 0, 0], uptime: 1, hostname: 'test', os: 'test', network: [],
  }
}

describe('server health', () => {
  it('turns a full local root disk and a degraded service into actionable issues', () => {
    const health = localServerHealth(statsWithRootDisk(99), [{
      name: 'PostgreSQL', status: 'degraded', detail: 'rejecting connections',
    }])

    expect(health.level).toBe('critical')
    expect(health.label).toBe('2 issues')
    expect(health.issues).toEqual(expect.arrayContaining([
      expect.objectContaining({ id: 'disk', href: '/disk?tab=cleanup', level: 'critical' }),
      expect.objectContaining({ id: 'service:PostgreSQL', href: '/monitoring?service=PostgreSQL', service: undefined }),
    ]))
  })

  it('marks only allowlisted local service issues as directly restartable', () => {
    const health = localServerHealth(statsWithRootDisk(20), [
      { name: 'postgresql', status: 'degraded', detail: 'rejecting connections' },
      { name: 'pm2-deploy', status: 'failed' },
      { name: 'custom-worker', status: 'failed' },
    ])

    expect(health.issues.find(issue => issue.id === 'service:postgresql')?.service).toBe('postgresql')
    expect(health.issues.find(issue => issue.id === 'service:pm2-deploy')?.service).toBe('pm2-deploy')
    expect(health.issues.find(issue => issue.id === 'service:custom-worker')?.service).toBeUndefined()
  })

  it('links an expired Google Drive authorization to Backups', () => {
    const health = localServerHealth(statsWithRootDisk(20), [], {
      connected: false,
      reconnectRequired: true,
      configured: true,
      settings: {
        folder: '',
        autoUpload: true,
        remoteRetentionDays: 30,
        notifyOnSuccess: true,
        notifyOnFailure: true,
      },
      rcloneFound: true,
    })

    expect(health.level).toBe('warning')
    expect(health.issues[0]).toMatchObject({
      id: 'gdrive:reconnect',
      href: '/backups?focus=gdrive',
      action: 'gdrive-reconnect',
    })
  })

  it('offers a guarded reset when local swap is high and RAM is sufficient', () => {
    const stats = statsWithRootDisk(20)
    stats.memory.total = 24 * 1024 ** 3
    stats.memory.available = 14 * 1024 ** 3
    stats.memory.swapTotal = 16 * 1024 ** 3
    stats.memory.swapUsed = 8 * 1024 ** 3
    stats.memory.swapFree = 8 * 1024 ** 3
    stats.memory.swapPercentage = 50

    const health = localServerHealth(stats, [])

    expect(health.issues[0]).toMatchObject({
      id: 'swap',
      title: 'Swap reset is available',
      href: '/',
      action: 'swap-reset',
    })
    expect(health.issues[0].detail).toContain('14.0 GB RAM is available')
  })

  it('reports high swap without an action when the RAM safety reserve is unavailable', () => {
    const stats = statsWithRootDisk(20)
    stats.memory.total = 16 * 1024 ** 3
    stats.memory.available = 4 * 1024 ** 3
    stats.memory.swapTotal = 16 * 1024 ** 3
    stats.memory.swapUsed = 8 * 1024 ** 3
    stats.memory.swapFree = 8 * 1024 ** 3
    stats.memory.swapPercentage = 50

    const issue = localServerHealth(stats, []).issues[0]

    expect(issue).toMatchObject({ id: 'swap', title: 'Swap usage is high', action: undefined })
    expect(issue.detail).toContain('reset needs 8.5 GB available RAM')
  })

  it('surfaces reclaimable interrupted backups as a reviewed cleanup action', () => {
    const health = localServerHealth(statsWithRootDisk(100), [], undefined, {
      directory: '/mnt/backups', legacyDirectories: ['/var/lib/hserver/backups'],
      totalBytes: 98_000_000_000, activeBytes: 0, completedBytes: 0, invalidBytes: 0,
      orphanedBytes: 91 * 1024 ** 3, legacyOrphanedBytes: 91 * 1024 ** 3,
      completedCount: 0, invalidCount: 0, orphanedCount: 15, legacyOrphanedCount: 15,
      rootSize: 100, rootUsed: 100, rootAvailable: 0, rootUsePercent: 100,
      backupVolumeSize: 200, backupVolumeUsed: 100, backupVolumeAvailable: 100, backupVolumeUsePercent: 50,
    })

    expect(health.issues).toEqual(expect.arrayContaining([
      expect.objectContaining({
        id: 'backup:orphaned',
        detail: '91.0 GB can be reclaimed after reviewing the exact files',
        href: '/backups?cleanup=orphaned',
        level: 'critical',
      }),
    ]))
  })

  it('reports an online healthy remote node without issues', () => {
    expect(remoteServerHealth({
      online: true,
      diskTotal: 100,
      diskAvailable: 60,
      services: [{ name: 'nginx.service', active: 'active', sub: 'running' }],
    })).toEqual({ level: 'healthy', label: 'Healthy', issues: [] })
  })

  it('does not claim a managed node is healthy before the management channel is checked', () => {
    expect(remoteServerHealth({
      online: true,
      managementStatus: 'checking',
      diskTotal: 100,
      diskAvailable: 60,
    })).toEqual({ level: 'loading', label: 'Checking', issues: [] })
  })

  it('reports a fresh heartbeat with an unreachable management channel', () => {
    const health = remoteServerHealth({
      nodeName: 'Edge EU 1',
      online: true,
      managementStatus: 'unreachable',
      diskTotal: 100,
      diskAvailable: 60,
    })

    expect(health.level).toBe('critical')
    expect(health.issues[0]).toMatchObject({
      id: 'management',
      title: 'Edge EU 1 management is unavailable',
      detail: 'The heartbeat is current, but the panel could not complete an agent management request',
      href: '/servers?tab=overview',
      level: 'critical',
    })
  })

  it('uses the agent-reported managed-node filesystem percentage for disk health', () => {
    const health = remoteServerHealth({
      online: true,
      diskTotal: 1000,
      diskUsed: 850,
      diskAvailable: 100,
      diskUsePercent: 89.47,
    })

    expect(health.issues[0]).toMatchObject({
      id: 'disk',
      detail: 'Root disk is 89% full',
      href: '/servers?tab=disk',
    })
  })

  it('makes high managed-node swap actionable when its live memory state allows reset', () => {
    const health = remoteServerHealth({
      online: true,
      memoryTotal: 24 * 1024 ** 3,
      memoryAvailable: 18 * 1024 ** 3,
      swapTotal: 8 * 1024 ** 3,
      swapUsed: 4 * 1024 ** 3,
    })

    expect(health.issues[0]).toMatchObject({
      id: 'swap',
      href: '/servers?tab=overview',
      action: 'swap-reset',
    })
  })

  it('links a failed remote service to its exact managed-node control row', () => {
    const health = remoteServerHealth({
      online: true,
      services: [{ name: 'nginx.service', active: 'failed', sub: 'failed' }],
    })

    expect(health.issues[0]).toMatchObject({
      id: 'service:nginx.service',
      href: '/servers?tab=services&service=nginx.service',
      service: 'nginx.service',
    })
  })

  it('keeps observed-only remote failures navigable but not directly restartable', () => {
    const health = remoteServerHealth({
      online: true,
      services: [{ name: 'hserver-agent.service', active: 'failed', sub: 'failed' }],
    })
    expect(health.issues[0]).toMatchObject({ id: 'service:hserver-agent.service', service: undefined })
  })

  it('makes a stale remote node a critical actionable issue', () => {
    const health = remoteServerHealth({ nodeName: 'Edge EU 1', online: false })
    expect(health.level).toBe('critical')
    expect(health.issues[0]).toMatchObject({ id: 'offline', title: 'Edge EU 1 is offline', href: '/servers' })
  })
})
