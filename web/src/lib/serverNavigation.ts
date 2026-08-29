export type ManagedServerID = string

export const LOCAL_SERVER_ID = 'local'
export const SELECTED_SERVER_KEY = 'hserver_selected_server'
export const MANAGED_NODE_HEARTBEAT_TTL_MS = 45_000

const MAX_MANAGED_SERVER_ID_LENGTH = 128

const MANAGED_NODE_MAX_FUTURE_SKEW_MS = 5 * 60_000

export function isLocalServer(server: ManagedServerID): boolean {
  return server === LOCAL_SERVER_ID
}

/**
 * Return the server encoded by a content URL, if the route has target
 * context. Target-authoritative routes may resolve an omitted node to local.
 *
 * A URL context wins over browser persistence.  In particular, keep an
 * explicitly requested `node=local`/`server=local` as local rather than
 * allowing a previously selected remote node to leak into that route.
 */
export function managedServerContextForLocation(pathname: string, search = ''): ManagedServerID | undefined {
  const params = new URLSearchParams(search)
  // Integrations is target-authoritative: an omitted/empty node explicitly
  // selects the local aggregate rather than inheriting browser persistence.
  if (pathname === '/integrations') return params.get('node') || LOCAL_SERVER_ID
  if (pathname === '/servers' && params.has('node')) return params.get('node') || LOCAL_SERVER_ID
  if (pathname === '/audit' && params.has('server')) return params.get('server') || LOCAL_SERVER_ID
  if (pathname === '/terminal' && params.has('node')) return params.get('node') || LOCAL_SERVER_ID
  return undefined
}

function isValidManagedServerID(value: string): boolean {
  if (value === LOCAL_SERVER_ID) return true
  if (value.length === 0 || value.length > MAX_MANAGED_SERVER_ID_LENGTH || value.trim() !== value) return false

  for (const [index, character] of Array.from(value).entries()) {
    const asciiLetterOrDigit = /[A-Za-z0-9]/.test(character)
    if (index === 0 ? !asciiLetterOrDigit : !asciiLetterOrDigit && !'.-_'.includes(character)) return false
  }
  return true
}

/**
 * Normalize a value read from browser persistence without trusting arbitrary
 * storage contents as a remote-node identifier.
 */
export function persistedManagedServerID(value: string | null | undefined): ManagedServerID | undefined {
  if (typeof value !== 'string' || !isValidManagedServerID(value)) return undefined
  return value
}

/**
 * Resolve a persisted selection against the latest node inventory.  Offline
 * nodes remain valid selections so the header can show their managed health;
 * only an unknown or malformed identifier falls back to the local server.
 */
export function resolvePersistedManagedServer(
  value: string | null | undefined,
  nodes: readonly { id: string }[],
): ManagedServerID {
  const candidate = persistedManagedServerID(value)
  if (!candidate || isLocalServer(candidate)) return LOCAL_SERVER_ID
  return nodes.some((node) => node.id === candidate) ? candidate : LOCAL_SERVER_ID
}

/** @apiRoute /nodes/{server}{suffix} */
export function managedNodePath(server: ManagedServerID, suffix = ''): string {
  if (isLocalServer(server)) throw new Error('local server does not have a managed-node API path')
  const normalizedSuffix = suffix === '' || suffix.startsWith('/') ? suffix : `/${suffix}`
  return `/nodes/${encodeURIComponent(server)}${normalizedSuffix}`
}

export function isManagedNodeOnline(lastSeenAt?: string, now = Date.now()): boolean {
  if (!lastSeenAt) return false
  const lastSeen = new Date(lastSeenAt).getTime()
  if (!Number.isFinite(lastSeen)) return false
  const age = now - lastSeen
  return age <= MANAGED_NODE_HEARTBEAT_TTL_MS && age >= -MANAGED_NODE_MAX_FUTURE_SKEW_MS
}

export function managedNodeOnline(
  node?: { online?: boolean; last_seen_at?: string },
  now = Date.now(),
): boolean {
  if (!node) return false
  return typeof node.online === 'boolean' ? node.online : isManagedNodeOnline(node.last_seen_at, now)
}

function serviceFocusKey(service: string): string {
  const unit = service.trim().replace(/\.service$/, '')
  if (/^postgresql@\d+-main$/.test(unit)) return 'postgresql'
  if (/^pm2(?:-.+)?$/.test(unit)) return 'pm2'
  return unit
}

export function serviceFocusMatches(candidate: string, focused: string): boolean {
  return serviceFocusKey(candidate) === serviceFocusKey(focused)
}

export function localServiceFocus(service: string): string {
  return serviceFocusKey(service)
}

const REMOTE_TAB_BY_LOCAL_PATH: Record<string, string | undefined> = {
  '/': undefined,
  '/servers': undefined,
  '/monitoring': undefined,
  '/logs': 'logs',
  '/disk': 'disk',
  '/docker': 'containers',
  '/deploy': 'deploy',
  '/domains': 'domains',
  '/ssl': 'ssl',
  '/nginx': 'nginx',
  '/php': 'php',
  '/pm2': 'pm2',
  '/cron': 'cron',
  '/firewall': 'firewall',
  '/databases': 'databases',
  '/backups': 'backups',
  '/files': 'files',
}

const LOCAL_TARGET_BY_REMOTE_TAB: Record<string, string> = {
  overview: '/',
  services: '/monitoring',
  processes: '/monitoring',
  logs: '/logs',
  disk: '/disk',
  containers: '/docker',
  deploy: '/deploy',
  domains: '/domains',
  ssl: '/ssl',
  nginx: '/nginx',
  php: '/php',
  pm2: '/pm2',
  cron: '/cron',
  firewall: '/firewall',
  databases: '/databases',
  backups: '/backups',
  files: '/files',
}

function remoteServersTarget(server: ManagedServerID, tab?: string, extras?: URLSearchParams): string {
  const params = new URLSearchParams({ node: server })
  if (tab) params.set('tab', tab)
  extras?.forEach((value, key) => {
    if (key !== 'node' && key !== 'tab') params.set(key, value)
  })
  return `/servers?${params.toString()}`
}

export function managedNavigationTarget(server: ManagedServerID, localPath: string): string | undefined {
  if (isLocalServer(server)) {
    // The fleet page has a remote-node fallback when its target is omitted.
    // Keep a local selection explicit so navigation cannot silently select the
    // first enrolled node.
    if (localPath === '/servers') {
      return `/servers?${new URLSearchParams({ node: LOCAL_SERVER_ID }).toString()}`
    }
    return localPath
  }
  if (localPath === '/integrations') {
    return `/integrations?${new URLSearchParams({ node: server }).toString()}`
  }
  if (localPath === '/terminal') return `/terminal?${new URLSearchParams({ node: server }).toString()}`
  if (localPath === '/audit') return `/audit?${new URLSearchParams({ server }).toString()}`
  if (!(localPath in REMOTE_TAB_BY_LOCAL_PATH)) return undefined
  return remoteServersTarget(server, REMOTE_TAB_BY_LOCAL_PATH[localPath])
}

export function serverSwitchTarget(
  next: ManagedServerID,
  pathname: string,
  search = '',
): string {
  if (!isLocalServer(next)) {
    const current = new URLSearchParams(search)
    if (pathname === '/monitoring') {
      const service = current.get('service')
      if (service) return remoteServersTarget(next, 'services', new URLSearchParams({ service }))
      if (current.get('focus') === 'processes') return remoteServersTarget(next, 'processes')
    }
    if (pathname === '/servers') {
      return remoteServersTarget(next, current.get('tab') ?? undefined, current)
    }
    return managedNavigationTarget(next, pathname) ?? remoteServersTarget(next)
  }

  if (pathname === '/terminal') return '/terminal'
  if (pathname === '/audit') return '/audit?server=local'
  if (pathname !== '/servers') return pathname || '/'

  const params = new URLSearchParams(search)
  const tab = params.get('tab') ?? 'overview'
  if (tab === 'services') {
    const service = params.get('service')
    if (service) return `/monitoring?${new URLSearchParams({ service: localServiceFocus(service) }).toString()}`
  }
  if (tab === 'processes') return '/monitoring?focus=processes'
  return LOCAL_TARGET_BY_REMOTE_TAB[tab] ?? '/'
}

/**
 * Scope a health issue href to the selected managed server without changing
 * local issue links.
 */
export function serverHealthIssueTarget(server: ManagedServerID, href: string): string {
  if (isLocalServer(server)) return href
  const target = new URL(href, 'http://localhost')
  return serverSwitchTarget(server, target.pathname, target.search)
}

export function managedServerForLocation(pathname: string, search = ''): ManagedServerID {
  return managedServerContextForLocation(pathname, search) ?? LOCAL_SERVER_ID
}
