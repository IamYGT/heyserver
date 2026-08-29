import { describe, expect, it } from 'vitest'
import {
  isManagedNodeOnline,
  localServiceFocus,
  managedNavigationTarget,
  managedNodePath,
  managedNodeOnline,
  managedServerContextForLocation,
  managedServerForLocation,
  persistedManagedServerID,
  resolvePersistedManagedServer,
  serverHealthIssueTarget,
  serviceFocusMatches,
  serverSwitchTarget,
} from './serverNavigation'

describe('server navigation', () => {
  it('uses one bounded heartbeat freshness rule for managed nodes', () => {
    const now = Date.parse('2026-08-26T03:00:00Z')
    expect(isManagedNodeOnline('2026-08-26T02:59:30Z', now)).toBe(true)
    expect(isManagedNodeOnline('2026-08-26T02:59:15Z', now)).toBe(true)
    expect(isManagedNodeOnline('2026-08-26T02:59:14Z', now)).toBe(false)
    expect(isManagedNodeOnline('2026-08-26T03:03:00Z', now)).toBe(true)
    expect(isManagedNodeOnline('2026-08-26T03:06:00Z', now)).toBe(false)
    expect(isManagedNodeOnline('not-a-date', now)).toBe(false)
    expect(isManagedNodeOnline(undefined, now)).toBe(false)
  })

  it('prefers the hub connectivity decision and falls back for older APIs', () => {
    const now = Date.parse('2026-08-26T03:00:00Z')
    expect(managedNodeOnline({ online: false, last_seen_at: '2026-08-26T03:00:00Z' }, now)).toBe(false)
    expect(managedNodeOnline({ online: true, last_seen_at: '2026-08-25T03:00:00Z' }, now)).toBe(true)
    expect(managedNodeOnline({ last_seen_at: '2026-08-26T02:59:30Z' }, now)).toBe(true)
    expect(managedNodeOnline(undefined, now)).toBe(false)
  })

  it('builds encoded API paths for arbitrary managed-node identifiers', () => {
    expect(managedNodePath('edge-eu-1', '/memory')).toBe('/nodes/edge-eu-1/memory')
    expect(managedNodePath('edge west', 'tasks')).toBe('/nodes/edge%20west/tasks')
    expect(() => managedNodePath('local', '/memory')).toThrow(/local server/)
  })

  it('preserves the active module when switching to any remote node', () => {
    expect(serverSwitchTarget('edge-eu-1', '/databases')).toBe('/servers?node=edge-eu-1&tab=databases')
    expect(serverSwitchTarget('edge-eu-1', '/disk')).toBe('/servers?node=edge-eu-1&tab=disk')
    expect(serverSwitchTarget('edge-eu-1', '/terminal')).toBe('/terminal?node=edge-eu-1')
    expect(serverSwitchTarget('edge-eu-1', '/integrations')).toBe('/integrations?node=edge-eu-1')
    expect(serverSwitchTarget('edge-us-2', '/integrations', '?node=edge-eu-1')).toBe('/integrations?node=edge-us-2')
    expect(serverSwitchTarget('edge-us-2', '/servers', '?node=edge-eu-1&tab=files')).toBe('/servers?node=edge-us-2&tab=files')
  })

  it('returns to the matching local module from a remote tab', () => {
    expect(serverSwitchTarget('local', '/servers', '?node=edge-eu-1&tab=databases')).toBe('/databases')
    expect(serverSwitchTarget('local', '/servers', '?node=edge-eu-1&tab=containers')).toBe('/docker')
    expect(serverSwitchTarget('local', '/servers', '?node=edge-eu-1&tab=services')).toBe('/monitoring')
    expect(serverSwitchTarget('local', '/servers', '?node=edge-eu-1&tab=processes')).toBe('/monitoring?focus=processes')
    expect(serverSwitchTarget('local', '/terminal', '?node=edge-eu-1')).toBe('/terminal')
    expect(serverSwitchTarget('local', '/audit', '?server=edge-eu-1')).toBe('/audit?server=local')
    expect(serverSwitchTarget('local', '/integrations', '?node=edge-eu-1')).toBe('/integrations')
  })

  it('preserves focused services and processes across switches', () => {
    expect(serverSwitchTarget('edge-eu-1', '/monitoring', '?service=postgresql'))
      .toBe('/servers?node=edge-eu-1&tab=services&service=postgresql')
    expect(serverSwitchTarget('local', '/servers', '?node=edge-eu-1&tab=services&service=postgresql%4018-main.service'))
      .toBe('/monitoring?service=postgresql')
    expect(serverSwitchTarget('edge-eu-1', '/monitoring', '?focus=processes'))
      .toBe('/servers?node=edge-eu-1&tab=processes')
  })

  it('scopes remote health issue links to the selected node and preserves issue queries', () => {
    expect(serverHealthIssueTarget('edge-us-2', '/servers?tab=disk'))
      .toBe('/servers?node=edge-us-2&tab=disk')
    expect(serverHealthIssueTarget('edge-us-2', '/servers?tab=services&service=nginx.service'))
      .toBe('/servers?node=edge-us-2&tab=services&service=nginx.service')
  })

  it('leaves local health issue links byte-for-byte unchanged', () => {
    const href = '/backups?focus=gdrive&return=%2Fservers'
    expect(serverHealthIssueTarget('local', href)).toBe(href)
  })

  it('matches portable systemd service identifiers', () => {
    expect(serviceFocusMatches('nginx', 'nginx.service')).toBe(true)
    expect(serviceFocusMatches('postgresql', 'postgresql@18-main.service')).toBe(true)
    expect(serviceFocusMatches('pm2-app', 'pm2-root.service')).toBe(true)
    expect(serviceFocusMatches('mariadb', 'nginx.service')).toBe(false)
    expect(localServiceFocus('pm2-app.service')).toBe('pm2')
  })

  it('uses the same generic mapping for sidebar and command navigation', () => {
    expect(managedNavigationTarget('local', '/servers')).toBe('/servers?node=local')
    expect(managedNavigationTarget('edge-eu-1', '/backups')).toBe('/servers?node=edge-eu-1&tab=backups')
    expect(managedNavigationTarget('edge-eu-1', '/servers')).toBe('/servers?node=edge-eu-1')
    expect(managedNavigationTarget('edge-eu-1', '/integrations')).toBe('/integrations?node=edge-eu-1')
    expect(managedNavigationTarget('edge-eu-1', '/audit')).toBe('/audit?server=edge-eu-1')
    expect(managedNavigationTarget('edge-eu-1', '/mail')).toBeUndefined()
    expect(managedNavigationTarget('local', '/backups')).toBe('/backups')
  })

  it('derives arbitrary selected nodes from the content route', () => {
    expect(managedServerForLocation('/servers', '?node=edge-eu-1&tab=databases')).toBe('edge-eu-1')
    expect(managedServerForLocation('/terminal', '?node=edge-us-2')).toBe('edge-us-2')
    expect(managedServerForLocation('/audit', '?server=edge-eu-1')).toBe('edge-eu-1')
    expect(managedServerForLocation('/servers', '?tab=databases')).toBe('local')
    expect(managedServerForLocation('/audit')).toBe('local')
    expect(managedServerForLocation('/databases')).toBe('local')
  })

  it('keeps explicit URL context authoritative over persistence', () => {
    expect(managedServerContextForLocation('/servers', '?node=edge-eu-1&tab=files')).toBe('edge-eu-1')
    expect(managedServerContextForLocation('/terminal', '?node=local')).toBe('local')
    expect(managedServerContextForLocation('/audit', '?server=edge-us-2')).toBe('edge-us-2')
    expect(managedServerContextForLocation('/integrations', '?node=edge-eu-1')).toBe('edge-eu-1')
    expect(managedServerContextForLocation('/integrations', '?node=local')).toBe('local')
    expect(managedServerContextForLocation('/integrations')).toBe('local')
    expect(managedServerContextForLocation('/databases')).toBeUndefined()
  })

  it('accepts only bounded managed identifiers from browser persistence', () => {
    expect(persistedManagedServerID('edge-eu-1')).toBe('edge-eu-1')
    expect(persistedManagedServerID('local')).toBe('local')
    expect(persistedManagedServerID(' edge-eu-1')).toBeUndefined()
    expect(persistedManagedServerID('../edge')).toBeUndefined()
    expect(persistedManagedServerID('x'.repeat(129))).toBeUndefined()
  })

  it('restores known persisted nodes and drops stale selections', () => {
    const nodes = [{ id: 'edge-eu-1' }, { id: 'edge-us-2' }]
    expect(resolvePersistedManagedServer('edge-eu-1', nodes)).toBe('edge-eu-1')
    expect(resolvePersistedManagedServer('local', nodes)).toBe('local')
    expect(resolvePersistedManagedServer('retired-node', nodes)).toBe('local')
    expect(resolvePersistedManagedServer(null, nodes)).toBe('local')
  })
})
