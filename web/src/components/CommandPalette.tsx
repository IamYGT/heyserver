import { useState, useEffect, useRef, useCallback } from 'react'
import { useNavigate } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import {
  Search,
  LayoutDashboard,
  Globe,
  Globe2,
  FileText,
  Activity,
  Cpu,
  Mail,
  Shield,
  Lock,
  FolderOpen,
  Database,
  Archive,
  Clock,
  Container,
  Rocket,
  Terminal,
  Users,
  ScrollText,
  Settings,
  ExternalLink,
  Code2,
  Clock3,
  Server,
  HardDrive,
  FileClock,
  Bell,
  Cloud,
  Info,
  Radar,
  Layers3,
  X,
  Braces,
} from 'lucide-react'
import { cn } from '@/lib/utils'
import {
  isLocalServer,
  managedNavigationTarget,
  type ManagedServerID,
} from '@/lib/serverNavigation'
import { api } from '@/lib/api'
import { filterAndRankCommandPalette } from '@/lib/commandPaletteSearch'

// ─── Types ─────────────────────────────────────────────────────────────────────

interface ResultItem {
  id: string
  label: string
  description?: string
  category: 'page' | 'server' | 'domain' | 'nginx' | 'pm2'
  icon: React.ComponentType<{ className?: string }>
  href?: string
  server?: ManagedServerID
  keywords?: string[]
}

// ─── Static page results ───────────────────────────────────────────────────────

const PAGE_RESULTS: ResultItem[] = [
  { id: 'page-dashboard', label: 'Dashboard', description: 'Server overview & metrics', category: 'page', icon: LayoutDashboard, href: '/' },
  { id: 'page-domains', label: 'Domains', description: 'Manage virtual hosts', category: 'page', icon: Globe, href: '/domains' },
  { id: 'page-nginx', label: 'Nginx', description: 'Nginx configs & status', category: 'page', icon: FileText, href: '/nginx', keywords: ['webserver', 'reverse proxy'] },
  { id: 'page-ssl', label: 'SSL', description: 'Certificates & Let\'s Encrypt', category: 'page', icon: Shield, href: '/ssl', keywords: ['tls', 'https', 'cert', 'certbot'] },
  { id: 'page-php', label: 'PHP', description: 'PHP-FPM pools & versions', category: 'page', icon: Code2, href: '/php', keywords: ['fpm', 'php-fpm'] },
  { id: 'page-pm2', label: 'PM2', description: 'Node.js process manager', category: 'page', icon: Cpu, href: '/pm2', keywords: ['node', 'process', 'nodejs'] },
  { id: 'page-servers', label: 'Servers', description: 'Manage local and remote servers', category: 'page', icon: Server, href: '/servers', keywords: ['server', 'node', 'remote', 'fleet'] },
  { id: 'page-monitoring', label: 'Monitoring', description: 'CPU, memory, disk metrics', category: 'page', icon: Activity, href: '/monitoring', keywords: ['stats', 'metrics', 'cpu', 'ram'] },
  { id: 'page-uptime', label: 'Uptime', description: 'Endpoint availability and incidents', category: 'page', icon: Radar, href: '/uptime', keywords: ['status', 'availability', 'incident'] },
  { id: 'page-logs', label: 'Logs', description: 'System and service journals', category: 'page', icon: FileClock, href: '/logs', keywords: ['journal', 'errors', 'remote'] },
  { id: 'page-disk', label: 'Disk', description: 'Filesystem usage and capacity', category: 'page', icon: HardDrive, href: '/disk', keywords: ['storage', 'filesystem', 'remote'] },
  { id: 'page-mail', label: 'Mail', description: 'Email server management', category: 'page', icon: Mail, href: '/mail', keywords: ['smtp', 'stalwart', 'email'] },
  { id: 'page-dns', label: 'DNS', description: 'DNS zones & records', category: 'page', icon: Globe2, href: '/dns' },
  { id: 'page-cloudflare', label: 'Cloudflare', description: 'Cloudflare zones and cache controls', category: 'page', icon: Cloud, href: '/cloudflare', keywords: ['cdn', 'proxy', 'cache'] },
  { id: 'page-webmail', label: 'Webmail', description: 'Open SnappyMail', category: 'page', icon: ExternalLink, href: '/webmail' },
  { id: 'page-firewall', label: 'Firewall', description: 'UFW rules & IP management', category: 'page', icon: Lock, href: '/firewall', keywords: ['ufw', 'iptables', 'block'] },
  { id: 'page-files', label: 'Files', description: 'File manager', category: 'page', icon: FolderOpen, href: '/files', keywords: ['file manager', 'browse'] },
  { id: 'page-databases', label: 'Databases', description: 'PostgreSQL & MariaDB', category: 'page', icon: Database, href: '/databases', keywords: ['postgres', 'mysql', 'mariadb'] },
  { id: 'page-backups', label: 'Backups', description: 'Backup management', category: 'page', icon: Archive, href: '/backups' },
  { id: 'page-cron', label: 'Cron', description: 'Scheduled tasks', category: 'page', icon: Clock, href: '/cron', keywords: ['schedule', 'jobs'] },
  { id: 'page-docker', label: 'Docker', description: 'Container management', category: 'page', icon: Container, href: '/docker', keywords: ['containers', 'images'] },
  { id: 'page-deploy', label: 'Deploy', description: 'Deployment pipelines', category: 'page', icon: Rocket, href: '/deploy' },
  { id: 'page-terminal', label: 'Terminal', description: 'Web terminal / SSH', category: 'page', icon: Terminal, href: '/terminal', keywords: ['shell', 'ssh', 'bash'] },
  { id: 'page-security', label: 'Security', description: 'Security score, fail2ban and IP controls', category: 'page', icon: Shield, href: '/security', keywords: ['fail2ban', 'ban', 'hardening'] },
  { id: 'page-notifications', label: 'Notifications', description: 'Alert channels, rules and delivery history', category: 'page', icon: Bell, href: '/notifications', keywords: ['alerts', 'telegram', 'email', 'rules'] },
  { id: 'page-users', label: 'Users', description: 'Panel user management', category: 'page', icon: Users, href: '/users' },
  { id: 'page-audit', label: 'Audit Log', description: 'Activity history', category: 'page', icon: ScrollText, href: '/audit', keywords: ['logs', 'history', 'activity'] },
  { id: 'page-settings', label: 'Settings', description: 'Panel settings', category: 'page', icon: Settings, href: '/settings' },
  { id: 'page-about', label: 'About', description: 'Panel version and system information', category: 'page', icon: Info, href: '/about' },
  { id: 'page-developer-api', label: 'Developer API', description: 'Explore the installed OpenAPI contract', category: 'page', icon: Braces, href: '/developer/api', keywords: ['openapi', 'routes', 'integration', 'automation'] },
  { id: 'page-integrations', label: 'Integrations', description: 'Optional and feature-specific capabilities', category: 'page', icon: Layers3, href: '/integrations', keywords: ['catalog', 'optional', 'provider', 'capability'] },
]

const CATEGORY_LABEL: Record<ResultItem['category'], string> = {
  page: 'Pages',
  server: 'Servers',
  domain: 'Domains',
  nginx: 'Nginx Configs',
  pm2: 'PM2 Processes',
}

const CATEGORY_COLOR: Record<ResultItem['category'], string> = {
  page: 'text-blue-400',
  server: 'text-cyan-400',
  domain: 'text-green-400',
  nginx: 'text-orange-400',
  pm2: 'text-purple-400',
}

const RECENT_KEY = 'hserver_recent_searches'
const MAX_RECENT = 6

function loadRecent(): string[] {
  try {
    return JSON.parse(localStorage.getItem(RECENT_KEY) ?? '[]')
  } catch {
    return []
  }
}

function saveRecent(query: string): void {
  try {
    const existing = loadRecent().filter((q) => q !== query)
    localStorage.setItem(RECENT_KEY, JSON.stringify([query, ...existing].slice(0, MAX_RECENT)))
  } catch {
    // Search must keep working when browser storage is unavailable.
  }
}

// ─── Hook: dynamic results from API ───────────────────────────────────────────

interface Domain { name: string; root?: string; proxy_target?: string; config?: string }
interface Pm2Process { name: string; status?: string; pm_id?: number; id?: number }
interface NginxConfig { filename?: string; name?: string; domain?: string; enabled?: boolean }

function useDynamicResults(server: ManagedServerID, serverLabel: string, enabled: boolean): ResultItem[] {
  const remote = !isLocalServer(server)
  const remoteBase = `/nodes/${encodeURIComponent(server)}`
  const { data: domains } = useQuery<Domain[]>({
    queryKey: ['cp-domains', server],
    queryFn: () => api.get<Domain[]>(remote ? `${remoteBase}/domains` : '/domains'),
    staleTime: 60_000,
    enabled,
  })

  const { data: pm2 } = useQuery<Pm2Process[]>({
    queryKey: ['cp-pm2', server],
    retry: false,
    queryFn: () => api.get<Pm2Process[]>(remote ? `${remoteBase}/pm2` : '/pm2/processes'),
    staleTime: 60_000,
    enabled,
  })

  const { data: nginx } = useQuery<NginxConfig[]>({
    queryKey: ['cp-nginx', server],
    queryFn: () => api.get<NginxConfig[]>(remote ? `${remoteBase}/nginx/configs` : '/nginx/configs'),
    staleTime: 60_000,
    enabled,
  })

  const results: ResultItem[] = []

  if (!enabled) return results

  ;(Array.isArray(domains) ? domains : []).slice(0, 20).forEach((d) => {
    results.push({
      id: `${server}-domain-${d.name}`,
      label: d.name,
      description: remote
        ? `${serverLabel} · ${d.proxy_target ?? d.root ?? d.config ?? 'Virtual host'}`
        : d.root ?? 'Virtual host',
      category: 'domain',
      icon: Globe,
      href: '/domains',
      keywords: [d.name],
    })
  })

  ;(Array.isArray(pm2) ? pm2 : []).slice(0, 20).forEach((p) => {
    results.push({
      id: `${server}-pm2-${p.name}`,
      label: p.name,
      description: `${remote ? `${serverLabel} · ` : ''}PM2 — ${p.status ?? 'unknown'}`,
      category: 'pm2',
      icon: Cpu,
      href: '/pm2',
      keywords: [p.name, 'process'],
    })
  })

  ;(Array.isArray(nginx) ? nginx : []).slice(0, 20).forEach((n) => {
    const name = n.filename ?? n.name
    if (!name) return
    results.push({
      id: `${server}-nginx-${name}`,
      label: name,
      description: remote
        ? `${serverLabel} · ${n.enabled ? 'enabled' : 'available'} Nginx config`
        : n.domain ?? 'Nginx config file',
      category: 'nginx',
      icon: FileText,
      href: '/nginx',
      keywords: [name, n.domain ?? '', remote ? serverLabel : ''],
    })
  })

  return results
}

// ─── Main component ────────────────────────────────────────────────────────────

interface CommandPaletteProps {
  isOpen: boolean
  onClose: () => void
  selectedServer: ManagedServerID
  selectedOnline: boolean
  serverLabel: string
  managedNodes: Array<{ id: string; name: string; hostname: string; online: boolean }>
  onSwitchServer: (server: ManagedServerID) => void
}

export function CommandPalette({ isOpen, onClose, selectedServer, selectedOnline, serverLabel, managedNodes, onSwitchServer }: CommandPaletteProps) {
  const navigate = useNavigate()
  const [query, setQuery] = useState('')
  const [selected, setSelected] = useState(0)
  const [recent, setRecent] = useState<string[]>([])
  const inputRef = useRef<HTMLInputElement>(null)
  const listRef = useRef<HTMLDivElement>(null)

  const dynamic = useDynamicResults(
    selectedServer,
    serverLabel,
    isOpen && selectedOnline,
  )
  const serverResults: ResultItem[] = [
    ...(!isLocalServer(selectedServer) ? [{
        id: 'server-local',
        label: 'Switch to HServer',
        description: 'Continue this workflow on HServer',
        category: 'server',
        icon: Server,
        server: 'local',
        keywords: ['server', 'local', 'node', 'hserver'],
      } satisfies ResultItem] : []),
    ...managedNodes.filter(node => node.id !== selectedServer).map((node): ResultItem => ({
        id: `server-${node.id}`,
        label: `Switch to ${node.name || node.hostname || node.id}`,
        description: `Continue this workflow on ${node.name || node.hostname || node.id} · ${node.online ? 'Online' : 'Offline'}`,
        category: 'server',
        icon: Server,
        server: node.id,
        keywords: ['server', 'remote', 'node', node.id, node.name, node.hostname],
      })),
  ]
  const allResults: ResultItem[] = [...PAGE_RESULTS, ...serverResults, ...dynamic]

  const filtered: ResultItem[] = filterAndRankCommandPalette(
    query.trim() === '' ? [...serverResults, ...PAGE_RESULTS] : allResults,
    query,
  )

  // Group results by category
  const grouped = filtered.reduce<Record<string, ResultItem[]>>((acc, item) => {
    const key = item.category
    if (!acc[key]) acc[key] = []
    acc[key].push(item)
    return acc
  }, {})

  // Flat list for keyboard nav
  const flatList = Object.values(grouped).flat()

  const handleSelect = useCallback(
    (item: ResultItem) => {
      if (query.trim()) saveRecent(query.trim())
      setRecent(loadRecent())
      if (item.server) {
        onSwitchServer(item.server)
      } else if (item.href) {
        navigate(managedNavigationTarget(selectedServer, item.href) ?? item.href)
      }
      onClose()
      setQuery('')
      setSelected(0)
    },
    [navigate, onClose, onSwitchServer, query, selectedServer]
  )

  useEffect(() => {
    if (isOpen) {
      setQuery('')
      setSelected(0)
      setRecent(loadRecent())
      setTimeout(() => inputRef.current?.focus(), 50)
    }
  }, [isOpen])

  useEffect(() => { setSelected(0) }, [query])

  useEffect(() => {
    const el = listRef.current?.querySelector(`[data-idx="${selected}"]`) as HTMLElement | null
    el?.scrollIntoView({ block: 'nearest' })
  }, [selected])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (!isOpen) return
      switch (e.key) {
        case 'ArrowDown':
          e.preventDefault()
          setSelected((s) => Math.min(s + 1, flatList.length - 1))
          break
        case 'ArrowUp':
          e.preventDefault()
          setSelected((s) => Math.max(s - 1, 0))
          break
        case 'Enter':
          e.preventDefault()
          if (flatList[selected]) handleSelect(flatList[selected])
          break
        case 'Escape':
          e.preventDefault()
          onClose()
          break
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [isOpen, flatList, selected, handleSelect, onClose])

  if (!isOpen) return null

  let globalIdx = 0

  return (
    <div className="fixed inset-0 z-50 flex items-start justify-center pt-[12vh]">
      {/* Backdrop */}
      <div
        className="absolute inset-0 bg-black/70 backdrop-blur-sm"
        onClick={onClose}
      />

      {/* Panel */}
      <div role="dialog" aria-modal="true" aria-label="Command palette" className="relative z-10 w-full max-w-xl overflow-hidden rounded-xl border border-zinc-700 bg-zinc-900 shadow-2xl">
        {/* Search input */}
        <div className="flex items-center gap-3 border-b border-zinc-800 px-4 py-3">
          <Search className="w-4 h-4 shrink-0 text-zinc-500" />
          <input
            ref={inputRef}
            aria-label="Search commands and servers"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Search pages, domains, configs…"
            className="flex-1 bg-transparent text-sm text-white placeholder:text-zinc-500 focus:outline-none"
          />
          {query && (
            <button onClick={() => setQuery('')} className="text-zinc-500 hover:text-white">
              <X className="w-3.5 h-3.5" />
            </button>
          )}
          <kbd className="hidden rounded border border-zinc-700 bg-zinc-800 px-1.5 py-0.5 text-[10px] text-zinc-500 sm:inline">
            ESC
          </kbd>
        </div>

        {/* Recent searches (shown when empty query) */}
        {!query.trim() && recent.length > 0 && (
          <div className="border-b border-zinc-800 px-4 py-2">
            <p className="mb-1 text-[10px] font-medium uppercase tracking-wider text-zinc-600">
              Recent
            </p>
            <div className="flex flex-wrap gap-1.5">
              {recent.map((r) => (
                <button
                  key={r}
                  onClick={() => setQuery(r)}
                  className="flex items-center gap-1 rounded-md bg-zinc-800 px-2 py-1 text-xs text-zinc-400 hover:bg-zinc-700 hover:text-white transition-colors"
                >
                  <Clock3 className="w-3 h-3" />
                  {r}
                </button>
              ))}
            </div>
          </div>
        )}

        {/* Results */}
        <div ref={listRef} className="max-h-96 overflow-y-auto py-1">
          {filtered.length === 0 ? (
            <p className="px-4 py-10 text-center text-sm text-zinc-500">
              No results for &ldquo;{query}&rdquo;
            </p>
          ) : (
            Object.entries(grouped).map(([cat, items]) => (
              <div key={cat}>
                <div className="px-4 py-1.5">
                  <span className={cn('text-[10px] font-semibold uppercase tracking-wider', CATEGORY_COLOR[cat as ResultItem['category']] ?? 'text-zinc-500')}>
                    {CATEGORY_LABEL[cat as ResultItem['category']] ?? cat}
                  </span>
                </div>
                {items.map((item) => {
                  const idx = globalIdx++
                  const isSelected = selected === idx
                  const Icon = item.icon
                  return (
                    <button
                      key={item.id}
                      data-idx={idx}
                      onClick={() => handleSelect(item)}
                      className={cn(
                        'flex w-full items-center gap-3 px-4 py-2.5 text-left transition-colors',
                        isSelected ? 'bg-blue-600/15 text-white' : 'text-zinc-300 hover:bg-zinc-800'
                      )}
                    >
                      <div className={cn(
                        'flex h-7 w-7 shrink-0 items-center justify-center rounded-md',
                        isSelected ? 'bg-blue-600/20' : 'bg-zinc-800'
                      )}>
                        <Icon className={cn('w-3.5 h-3.5', isSelected ? 'text-blue-400' : 'text-zinc-400')} />
                      </div>
                      <div className="min-w-0 flex-1">
                        <span className="block truncate text-sm font-medium">{item.label}</span>
                        {item.description && (
                          <span className="block truncate text-xs text-zinc-500">{item.description}</span>
                        )}
                      </div>
                      {isSelected && (
                        <kbd className="shrink-0 rounded border border-zinc-700 bg-zinc-800 px-1.5 py-0.5 text-[10px] text-zinc-500">
                          ↵
                        </kbd>
                      )}
                    </button>
                  )
                })}
              </div>
            ))
          )}
        </div>

        {/* Footer hint */}
        <div className="flex items-center gap-4 border-t border-zinc-800 px-4 py-2">
          <div className="flex items-center gap-1 text-[11px] text-zinc-600">
            <kbd className="rounded border border-zinc-700 bg-zinc-800 px-1 py-0.5 text-[10px]">↑↓</kbd>
            navigate
          </div>
          <div className="flex items-center gap-1 text-[11px] text-zinc-600">
            <kbd className="rounded border border-zinc-700 bg-zinc-800 px-1 py-0.5 text-[10px]">↵</kbd>
            open
          </div>
          <div className="ml-auto text-[11px] text-zinc-600">
            <kbd className="rounded border border-zinc-700 bg-zinc-800 px-1 py-0.5 text-[10px]">⌘K</kbd>
            {' '}to open
          </div>
        </div>
      </div>
    </div>
  )
}
