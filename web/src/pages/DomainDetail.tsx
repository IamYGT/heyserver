import { useState, Component, type ReactNode, type ErrorInfo } from 'react'
import { useParams, useNavigate, Link } from 'react-router-dom'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import Editor from '@monaco-editor/react'
import {
  ArrowLeft,
  Globe,
  Shield,
  ShieldOff,
  FolderOpen,
  Code2,
  FileCode,
  Lock,
  Network,
  Folder,
  File,
  ScrollText,
  ChevronRight,
  Home,
  RefreshCw,
  Save,
  ExternalLink,
  AlertTriangle,
  Loader2,
  CheckCircle,
  CheckCircle2,
  XCircle,
  Calendar,
  Clock,
  Plus,
  FileCog,
  FileText,
  Eye,
  EyeOff,
  Radar,
  Activity,
} from 'lucide-react'
import {
  LineChart,
  Line,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip as RechartsTooltip,
  ResponsiveContainer,
} from 'recharts'
import type { UptimeMonitor, UptimeHeartbeat, UptimeIncident, UptimeStats } from '@/lib/types'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { api, ApiError } from '@/lib/api'
import { toast } from 'sonner'
import { cn } from '@/lib/utils'
import type {
  Domain,
  NginxConfig,
  SSLCertificate,
  SSLOperationResult,
  PHPPool,
  DNSZone,
  CFZone,
  CFRecord,
} from '@/lib/types'

// ─── Tab types ─────────────────────────────────────────────────────────────────

// Tab-level error boundary to prevent one tab from crashing the whole page
class TabSafe extends Component<{ children: ReactNode }, { error: Error | null }> {
  state = { error: null as Error | null }
  static getDerivedStateFromError(error: Error) { return { error } }
  componentDidCatch(error: Error, info: ErrorInfo) { console.error('[TabSafe]', error, info.componentStack) }
  render() {
    if (this.state.error) {
      return (
        <div className="flex flex-col items-center justify-center py-16 text-zinc-600">
          <AlertTriangle className="w-8 h-8 mb-3 text-red-500/60" />
          <p className="text-sm text-zinc-400 font-medium">This tab encountered an error</p>
          <p className="text-xs text-zinc-600 mt-1 max-w-md text-center">{this.state.error.message}</p>
          <button onClick={() => this.setState({ error: null })} className="mt-4 px-3 py-1.5 bg-blue-600 text-white text-xs rounded-lg hover:bg-blue-500">Try Again</button>
        </div>
      )
    }
    return this.props.children
  }
}

function DomainLoadError({ title, error, retry, retrying = false }: { title: string; error: Error; retry: () => void; retrying?: boolean }) {
  return (
    <div className="flex flex-col items-center gap-3 rounded-xl border border-red-500/25 bg-red-500/[0.05] px-4 py-10 text-center">
      <AlertTriangle className="size-6 text-red-400" />
      <div>
        <p className="text-sm text-red-300">{title}</p>
        <p className="mt-1 break-words font-mono text-xs text-red-400/70">{error.message}</p>
      </div>
      <Button type="button" size="sm" variant="outline" onClick={retry} disabled={retrying}>
        <RefreshCw className={cn('size-3.5', retrying && 'animate-spin')} /> Retry
      </Button>
    </div>
  )
}

type TabId = 'overview' | 'uptime' | 'nginx' | 'php' | 'ssl' | 'dns' | 'files' | 'logs'

interface Tab {
  id: TabId
  label: string
  icon: React.ReactNode
}

const TABS: Tab[] = [
  { id: 'overview', label: 'Overview', icon: <Globe className="w-3.5 h-3.5" /> },
  { id: 'uptime', label: 'Uptime', icon: <Radar className="w-3.5 h-3.5" /> },
  { id: 'nginx', label: 'Nginx', icon: <Code2 className="w-3.5 h-3.5" /> },
  { id: 'php', label: 'PHP', icon: <FileCode className="w-3.5 h-3.5" /> },
  { id: 'ssl', label: 'SSL', icon: <Lock className="w-3.5 h-3.5" /> },
  { id: 'dns', label: 'DNS', icon: <Network className="w-3.5 h-3.5" /> },
  { id: 'files', label: 'Files', icon: <FolderOpen className="w-3.5 h-3.5" /> },
  { id: 'logs', label: 'Logs', icon: <ScrollText className="w-3.5 h-3.5" /> },
]

// ─── Helpers ───────────────────────────────────────────────────────────────────

function formatSize(bytes: number): string {
  if (bytes === 0) return '—'
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
}

function formatDate(iso: string): string {
  try {
    return new Date(iso).toLocaleDateString('en-GB', {
      day: '2-digit',
      month: 'short',
      year: 'numeric',
    })
  } catch {
    return iso
  }
}

function joinPath(base: string, name: string): string {
  return base.endsWith('/') ? `${base}${name}` : `${base}/${name}`
}

function getLanguage(filename: string): string {
  const ext = filename.split('.').pop()?.toLowerCase() ?? ''
  const map: Record<string, string> = {
    ts: 'typescript', tsx: 'typescript', js: 'javascript', jsx: 'javascript',
    json: 'json', html: 'html', css: 'css', scss: 'scss', php: 'php',
    py: 'python', go: 'go', sh: 'shell', conf: 'nginx', nginx: 'nginx',
    yaml: 'yaml', yml: 'yaml', toml: 'toml', xml: 'xml', sql: 'sql',
    md: 'markdown', env: 'dotenv', ini: 'ini', log: 'plaintext', txt: 'plaintext',
  }
  return map[ext] ?? 'plaintext'
}

type ExpiryStatus = 'valid' | 'warning' | 'critical' | 'expired'
function getExpiryStatus(days: number): ExpiryStatus {
  if (days < 0) return 'expired'
  if (days < 7) return 'critical'
  if (days < 30) return 'warning'
  return 'valid'
}

// ─── Overview Tab ──────────────────────────────────────────────────────────────

interface OverviewTabProps {
  domain: Domain
}

export function OverviewTab({ domain }: OverviewTabProps) {
  const queryClient = useQueryClient()

  const toggleMutation = useMutation({
    mutationFn: () =>
      api.post<{ domain: string; active: boolean }>(`/domains/${domain.id}/toggle`, { active: !domain.isActive }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['domains'] })
      toast.success(domain.isActive ? 'Domain disabled' : 'Domain enabled')
    },
    onError: () => toast.error('Failed to toggle domain status'),
  })

  return (
    <div className="space-y-4">
      <Card className="bg-zinc-900 border-zinc-800">
        <CardHeader className="pb-3">
          <CardTitle className="text-sm font-semibold text-zinc-300">Domain Information</CardTitle>
        </CardHeader>
        <CardContent>
          <div className="space-y-3">
            <div className="flex justify-between py-1.5 border-b border-zinc-800/50">
              <span className="text-zinc-500 text-sm">Domain</span>
              <span className="text-zinc-200 text-sm font-mono">{String(domain.name || '')}</span>
            </div>
            <div className="flex justify-between py-1.5 border-b border-zinc-800/50">
              <span className="text-zinc-500 text-sm">Type</span>
              <Badge className="text-xs">{String(domain.type || 'unknown')}</Badge>
            </div>
            {domain.root ? (
              <div className="flex justify-between py-1.5 border-b border-zinc-800/50">
                <span className="text-zinc-500 text-sm">Document Root</span>
                <span className="text-zinc-400 text-xs font-mono truncate max-w-xs">{String(domain.root)}</span>
              </div>
            ) : null}
            {domain.phpVersion ? (
              <div className="flex justify-between py-1.5 border-b border-zinc-800/50">
                <span className="text-zinc-500 text-sm">PHP Version</span>
                <Badge className="bg-purple-500/10 text-purple-400 border-purple-500/20 text-xs">{'PHP ' + String(domain.phpVersion)}</Badge>
              </div>
            ) : null}
            {typeof domain.proxyPort === 'number' && domain.proxyPort > 0 ? (
              <div className="flex justify-between py-1.5 border-b border-zinc-800/50">
                <span className="text-zinc-500 text-sm">Proxy Port</span>
                <span className="text-zinc-200 text-sm font-mono">{String(domain.proxyPort)}</span>
              </div>
            ) : null}
            <div className="flex justify-between py-1.5 border-b border-zinc-800/50">
              <span className="text-zinc-500 text-sm">SSL</span>
              {domain.sslEnabled ? (
                <Badge className="bg-green-500/10 text-green-400 border-green-500/20 text-xs">Enabled</Badge>
              ) : (
                <Badge variant="outline" className="border-zinc-700 text-zinc-500 text-xs">Disabled</Badge>
              )}
            </div>
            <div className="flex justify-between py-1.5">
              <span className="text-zinc-500 text-sm">Status</span>
              <div className="flex items-center gap-2">
                {domain.isActive ? (
                  <Badge className="bg-green-500/10 text-green-400 border-green-500/20 text-xs">Active</Badge>
                ) : (
                  <Badge variant="outline" className="border-zinc-700 text-zinc-500 text-xs">Inactive</Badge>
                )}
                <button
                  onClick={() => toggleMutation.mutate()}
                  disabled={toggleMutation.isPending}
                  className="text-zinc-500 hover:text-white transition-colors"
                  title={domain.isActive ? 'Disable' : 'Enable'}
                  aria-label={domain.isActive ? 'Disable domain' : 'Enable domain'}
                >
                  {domain.isActive ? <EyeOff className="w-4 h-4" /> : <Eye className="w-4 h-4" />}
                </button>
              </div>
            </div>
          </div>
        </CardContent>
      </Card>
    </div>
  )
}

// ─── Nginx Tab ─────────────────────────────────────────────────────────────────

interface NginxTabProps {
  domain: Domain
}

export function NginxTab({ domain }: NginxTabProps) {
  const [configContent, setConfigContent] = useState('')
  const [configChecksum, setConfigChecksum] = useState('')
  const [isDirty, setIsDirty] = useState(false)

  const configsQuery = useQuery<NginxConfig[]>({
    queryKey: ['nginx', 'configs'],
    queryFn: () => api.get<NginxConfig[]>('/nginx/configs'),
  })
  const configs = configsQuery.data

  const matchedConfig = configs?.find(
    (c: NginxConfig) => c.domain === domain.name || c.filename.includes(domain.name),
  )

  const contentQuery = useQuery({
    queryKey: ['nginx', 'config', matchedConfig?.filename],
    queryFn: async () => {
      const resp = await api.get<{ content: string; checksum: string }>(`/nginx/configs/${encodeURIComponent(matchedConfig!.filename)}`)
      const text = typeof resp === 'string' ? resp : (resp?.content ?? String(resp))
      setConfigContent(text)
      setConfigChecksum(typeof resp === 'string' ? '' : resp.checksum)
      setIsDirty(false)
      return resp
    },
    enabled: !!matchedConfig,
  })

  const saveMutation = useMutation({
    mutationFn: () =>
      api.put<{ checksum: string }>(`/nginx/configs/${encodeURIComponent(matchedConfig!.filename)}`, { content: configContent, checksum: configChecksum }),
    onSuccess: (receipt) => { setConfigChecksum(receipt.checksum); setIsDirty(false); toast.success('Config saved') },
    onError: (error: Error) => toast.error(error.message || 'Failed to save config'),
  })

  if (configsQuery.isLoading || contentQuery.isLoading) {
    return <Skeleton className="h-96 w-full bg-zinc-900" />
  }

  if (configsQuery.isError) {
    return <DomainLoadError title="Nginx configuration inventory could not be loaded." error={configsQuery.error} retry={() => { void configsQuery.refetch() }} retrying={configsQuery.isFetching} />
  }

  if (contentQuery.isError) {
    return <DomainLoadError title={`Nginx configuration for ${domain.name} could not be loaded.`} error={contentQuery.error} retry={() => { void contentQuery.refetch() }} retrying={contentQuery.isFetching} />
  }

  if (!matchedConfig) {
    return (
      <div className="flex flex-col items-center justify-center py-20 text-zinc-600">
        <AlertTriangle className="w-8 h-8 mb-3 opacity-50" />
        <p className="text-sm">{"No nginx config found for this domain."}</p>
        <Link to="/nginx" className="mt-3">
          <Button size="sm" variant="outline" className="border-zinc-700 text-zinc-400 hover:text-white">
            {"View all nginx configs"}
          </Button>
        </Link>
      </div>
    )
  }

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          <span className="text-xs text-zinc-500 font-mono">{String(matchedConfig.filename)}</span>
          <Badge className={matchedConfig.isEnabled ? "text-xs bg-green-500/10 text-green-400 border-green-500/20" : "text-xs bg-zinc-700/40 text-zinc-400"}>
            {matchedConfig.isEnabled ? 'enabled' : 'disabled'}
          </Badge>
        </div>
        <div className="flex items-center gap-2">
          <Button
            size="sm"
            variant="outline"
            onClick={() => saveMutation.mutate()}
            disabled={!isDirty || !configChecksum || saveMutation.isPending}
            className="border-zinc-700 text-zinc-300 hover:text-white"
          >
            <Save className="w-3.5 h-3.5 mr-1.5" />
            {saveMutation.isPending ? 'Saving...' : 'Save'}
          </Button>
        </div>
      </div>
      <div className="border border-zinc-800 rounded-lg overflow-hidden" style={{ height: '500px' }}>
        <Editor
          height="100%"
          language="nginx"
          theme="vs-dark"
          value={configContent}
          onChange={(val) => { setConfigContent(val ?? ''); setIsDirty(true) }}
          options={{ minimap: { enabled: false }, fontSize: 13, wordWrap: 'on', scrollBeyondLastLine: false }}
        />
      </div>
    </div>
  )
}

// ─── Row helper ──────────────────────────────────────────────────────────────

function Row({ label, value, mono, children }: { label: string; value?: string; mono?: boolean; children?: React.ReactNode }) {
  return (
    <div className="flex items-center justify-between py-1.5 border-b border-zinc-800/50 last:border-0">
      <span className="text-zinc-500 text-sm w-32 flex-shrink-0">{String(label)}</span>
      {children ?? (
        <span className={`text-sm text-zinc-200 truncate ${mono ? 'font-mono text-xs text-zinc-400' : ''}`}>
          {String(value ?? '')}
        </span>
      )}
    </div>
  )
}

// ─── PHP Tab — Plesk-style single-page domain PHP management ─────────────────

interface PhpTabProps {
  domain: Domain
}

// Dropdown+text field like Plesk — preset values + custom input
function PhpField({ label, directive, value, presets, placeholder, onChange, onReset }: {
  label: string; directive: string; value: string; presets?: string[];
  placeholder: string; onChange: (v: string) => void; onReset?: () => void
}) {
  const hasValue = value !== ''
  return (
    <div className="flex items-center gap-3 py-2 px-3 rounded-lg hover:bg-zinc-800/30 transition-colors group">
      <div className="flex-1 min-w-0">
        <span className="text-zinc-200 text-xs block">{label}</span>
        <span className="text-zinc-600 text-[10px] font-mono">{directive}</span>
      </div>
      <div className="flex items-center gap-1.5">
        {presets && presets.length > 0 && (
          <select
            value={value || '__default__'}
            onChange={e => {
              const v = e.target.value
              if (v === '__default__') onChange('')
              else if (v === '__custom__') { /* keep current */ }
              else onChange(v)
            }}
            className="px-2 py-1.5 text-xs rounded-lg border border-zinc-700 bg-zinc-800 text-zinc-300 min-w-[120px]"
          >
            <option value="__default__">Default</option>
            {presets.map(p => <option key={p} value={p}>{p}</option>)}
            {hasValue && !presets.includes(value) && <option value={value}>Custom: {value}</option>}
          </select>
        )}
        <input
          type="text"
          value={value}
          placeholder={placeholder}
          onChange={e => onChange(e.target.value)}
          className={cn(
            'w-32 px-2 py-1.5 text-xs font-mono rounded-lg border text-right',
            hasValue ? 'border-purple-500/40 text-purple-300 bg-purple-500/5' : 'border-zinc-700/50 text-zinc-500 bg-zinc-800/30 placeholder:text-zinc-700'
          )}
        />
        {hasValue && onReset && (
          <button onClick={onReset} className="opacity-0 group-hover:opacity-100 text-zinc-600 hover:text-red-400 transition-all" title="Reset to default">
            ✕
          </button>
        )}
      </div>
    </div>
  )
}

function PhpTab({ domain }: PhpTabProps) {
  const queryClient = useQueryClient()
  const [edits, setEdits] = useState<Record<string, string>>({})
  const [additionalDirectives, setAdditionalDirectives] = useState('')
  const [additionalDirty, setAdditionalDirty] = useState(false)

  const poolsQuery = useQuery<PHPPool[]>({
    queryKey: ['php', 'pools'],
    queryFn: () => api.get<PHPPool[]>('/php/pools'),
    enabled: !!domain.phpVersion,
  })
  const versionsQuery = useQuery<{ version: string; active: boolean }[]>({
    queryKey: ['php', 'versions'],
    queryFn: () => api.get('/php/versions'),
  })
  const pools = poolsQuery.data
  const versions = versionsQuery.data ?? []

  // Match pool: try exact name, then full domain in config_file, then domain without TLD
  const domainPool = pools?.find(p => p.name === domain.name)
    ?? pools?.find(p => p.config_file?.includes(domain.name))
    ?? pools?.find(p => {
      // "app.tune-x.com" → match pool whose config contains "tune-x.com" or "app.tune-x"
      const parts = domain.name.split('.')
      if (parts.length >= 3) {
        const withoutTld = parts.slice(0, -1).join('.')  // "app.tune-x"
        const mainDomain = parts.slice(-2).join('.')      // "tune-x.com"
        return p.config_file?.includes(mainDomain) || p.config_file?.includes(withoutTld)
      }
      return p.config_file?.includes(domain.name)
    })
  const poolVersion = domainPool?.version ?? domain.phpVersion ?? ''
  const poolName = domainPool?.name ?? ''
  const ps = domainPool?.pm_settings
  const raw = domainPool?.raw ?? {}

  // Parse existing overrides from pool raw
  const overrides = new Map<string, string>()
  for (const [k, v] of Object.entries(raw)) {
    const m = k.match(/^php_(?:admin_value|value|flag)\[(.+)\]$/)
    if (m) overrides.set(m[1], v)
  }

  const getVal = (key: string) => edits[key] ?? overrides.get(key) ?? ''
  const setVal = (key: string, value: string) => { setEdits(prev => ({ ...prev, [key]: value })) }
  const resetVal = (key: string) => { setEdits(prev => { const n = { ...prev }; n[key] = ''; return n }) }
  const isDirty = Object.keys(edits).length > 0
  const openBasedirPlaceholder = domain.root.trim()
    ? `${domain.root.replace(/\/+$/, '')}:/tmp:/usr/share/php`
    : 'domain document root:/tmp:/usr/share/php'

  // Mutations
  const switchVersionMutation = useMutation({
    mutationFn: (v: string) => api.post('/php/pools/switch-version', { domain: poolName, from_version: poolVersion, to_version: v }),
    onSuccess: () => { queryClient.invalidateQueries({ queryKey: ['php'] }); queryClient.invalidateQueries({ queryKey: ['domains'] }); toast.success('PHP version switched') },
    onError: () => toast.error('Failed to switch version'),
  })
  const saveMutation = useMutation({
    mutationFn: async () => {
      const settings: Record<string, string> = {}
      for (const [k, v] of Object.entries(edits)) { if (v) settings[k] = v }
      if (Object.keys(settings).length > 0) await api.put(`/php/ini/${poolVersion}/${poolName}`, { settings })
      await api.post(`/php/pools/${poolVersion}/${poolName}/restart`)
    },
    onSuccess: () => { queryClient.invalidateQueries({ queryKey: ['php'] }); setEdits({}); toast.success('PHP settings saved & FPM reloaded') },
    onError: () => toast.error('Failed to save'),
  })
  const restartMutation = useMutation({
    mutationFn: () => api.post(`/php/pools/${poolVersion}/${poolName}/restart`),
    onSuccess: () => { queryClient.invalidateQueries({ queryKey: ['php'] }); toast.success('PHP-FPM restarted') },
    onError: () => toast.error('Failed to restart'),
  })

  if (!domain.phpVersion) return <div className="flex flex-col items-center justify-center py-20 text-zinc-600"><FileCode className="w-8 h-8 mb-3 opacity-50" /><p className="text-sm">This is not a PHP domain.</p></div>
  if (poolsQuery.isLoading || versionsQuery.isLoading) return <div className="space-y-3">{[1,2,3,4].map(i => <Skeleton key={i} className="h-12 bg-zinc-900" />)}</div>
  if (poolsQuery.isError || versionsQuery.isError) {
    const error = poolsQuery.error ?? versionsQuery.error
    return (
      <Card className="border-red-500/25 bg-red-500/[0.05]">
        <CardContent className="p-6 text-center">
          <AlertTriangle className="mx-auto size-6 text-red-400" />
          <p className="mt-2 text-sm font-medium text-red-300">PHP-FPM information could not be loaded.</p>
          <p className="mt-1 text-xs text-zinc-500">{error?.message || 'The server did not return PHP runtime information.'}</p>
          <Button type="button" variant="outline" size="sm" className="mt-4 border-red-500/30 text-red-200" onClick={() => { void poolsQuery.refetch(); void versionsQuery.refetch() }}>
            <RefreshCw className="mr-2 size-3.5" />Retry
          </Button>
        </CardContent>
      </Card>
    )
  }
  if (!domainPool) return <div className="flex flex-col items-center justify-center py-12 text-zinc-600"><AlertTriangle className="w-8 h-8 mb-3 opacity-50" /><p className="text-sm">No PHP-FPM pool found for this domain.</p></div>

  return (
    <div className="space-y-5">
      {/* ═══ SECTION 1: PHP Version + Handler ═══ */}
      <Card className="bg-zinc-900 border-zinc-800">
        <CardContent className="py-4">
          <div className="flex items-center justify-between flex-wrap gap-4">
            <div className="flex items-center gap-6">
              <div>
                <label className="text-zinc-500 text-[10px] uppercase tracking-wider block mb-1.5">PHP Version</label>
                <div className="flex gap-1.5">
                  {versions.filter(v => v.active).map(v => (
                    <button key={v.version}
                      onClick={() => v.version !== poolVersion && confirm(`Switch ${domain.name} to PHP ${v.version}?`) && switchVersionMutation.mutate(v.version)}
                      disabled={switchVersionMutation.isPending}
                      className={cn('px-4 py-2 rounded-lg text-sm font-bold transition-all',
                        v.version === poolVersion
                          ? 'bg-purple-500/20 text-purple-300 border-2 border-purple-500/50'
                          : 'bg-zinc-800 text-zinc-500 border border-zinc-700 hover:border-purple-500/40 hover:text-purple-300'
                      )}>
                      {v.version}
                    </button>
                  ))}
                  {switchVersionMutation.isPending && <Loader2 className="w-5 h-5 animate-spin text-purple-400 self-center" />}
                </div>
              </div>
              <div>
                <label className="text-zinc-500 text-[10px] uppercase tracking-wider block mb-1.5">Run PHP as</label>
                <Badge className="bg-blue-500/10 text-blue-400 border-blue-500/20 text-xs px-3 py-1.5">FPM Application</Badge>
              </div>
              <div>
                <label className="text-zinc-500 text-[10px] uppercase tracking-wider block mb-1.5">Status</label>
                <div className="flex items-center gap-1.5">
                  <span className={cn('w-2.5 h-2.5 rounded-full', domainPool.socket_exists ? 'bg-green-500' : 'bg-red-500')} />
                  <span className={cn('text-sm font-semibold', domainPool.socket_exists ? 'text-green-400' : 'text-red-400')}>
                    {domainPool.socket_exists ? 'Running' : 'Stopped'}
                  </span>
                </div>
              </div>
            </div>
            <div className="flex gap-2">
              {isDirty && (
                <Button size="sm" className="bg-blue-600 hover:bg-blue-700 text-white" onClick={() => saveMutation.mutate()} disabled={saveMutation.isPending}>
                  <Save className="w-3.5 h-3.5 mr-1.5" />{saveMutation.isPending ? 'Saving…' : 'Apply'}
                </Button>
              )}
              <Button size="sm" variant="outline" className="border-zinc-700 text-zinc-400 hover:text-blue-400"
                onClick={() => restartMutation.mutate()} disabled={restartMutation.isPending}>
                <RefreshCw className={cn('w-3.5 h-3.5 mr-1.5', restartMutation.isPending && 'animate-spin')} />Restart
              </Button>
            </div>
          </div>
        </CardContent>
      </Card>

      {/* ═══ SECTION 2: Performance Settings ═══ */}
      <Card className="bg-zinc-900 border-zinc-800">
        <CardHeader className="pb-1"><CardTitle className="text-sm text-zinc-300">Performance Settings</CardTitle></CardHeader>
        <CardContent className="space-y-0 divide-y divide-zinc-800/50">
          <PhpField label="Memory Limit" directive="memory_limit" value={getVal('memory_limit')}
            presets={['64M','128M','256M','512M','1G']} placeholder="Default (128M)" onChange={v => setVal('memory_limit', v)} onReset={() => resetVal('memory_limit')} />
          <PhpField label="Max Execution Time" directive="max_execution_time" value={getVal('max_execution_time')}
            presets={['30','60','120','300','600']} placeholder="Default (30)" onChange={v => setVal('max_execution_time', v)} onReset={() => resetVal('max_execution_time')} />
          <PhpField label="Max Input Time" directive="max_input_time" value={getVal('max_input_time')}
            presets={['60','120','300','600']} placeholder="Default (60)" onChange={v => setVal('max_input_time', v)} onReset={() => resetVal('max_input_time')} />
          <PhpField label="Max Input Vars" directive="max_input_vars" value={getVal('max_input_vars')}
            presets={['1000','3000','5000','10000']} placeholder="Default (1000)" onChange={v => setVal('max_input_vars', v)} onReset={() => resetVal('max_input_vars')} />
          <PhpField label="OPcache" directive="opcache.enable" value={getVal('opcache.enable')}
            presets={['1','0']} placeholder="Default (On)" onChange={v => setVal('opcache.enable', v)} onReset={() => resetVal('opcache.enable')} />
        </CardContent>
      </Card>

      {/* ═══ SECTION 3: Upload Settings ═══ */}
      <Card className="bg-zinc-900 border-zinc-800">
        <CardHeader className="pb-1"><CardTitle className="text-sm text-zinc-300">Upload & Post Limits</CardTitle></CardHeader>
        <CardContent className="space-y-0 divide-y divide-zinc-800/50">
          <PhpField label="Upload Max Filesize" directive="upload_max_filesize" value={getVal('upload_max_filesize')}
            presets={['2M','8M','16M','32M','64M','128M','256M']} placeholder="Default (2M)" onChange={v => setVal('upload_max_filesize', v)} onReset={() => resetVal('upload_max_filesize')} />
          <PhpField label="Post Max Size" directive="post_max_size" value={getVal('post_max_size')}
            presets={['8M','16M','32M','64M','128M','256M']} placeholder="Default (8M)" onChange={v => setVal('post_max_size', v)} onReset={() => resetVal('post_max_size')} />
          <PhpField label="Max File Uploads" directive="max_file_uploads" value={getVal('max_file_uploads')}
            presets={['20','50','100']} placeholder="Default (20)" onChange={v => setVal('max_file_uploads', v)} onReset={() => resetVal('max_file_uploads')} />
        </CardContent>
      </Card>

      {/* ═══ SECTION 4: Error Handling ═══ */}
      <Card className="bg-zinc-900 border-zinc-800">
        <CardHeader className="pb-1"><CardTitle className="text-sm text-zinc-300">Error Handling</CardTitle></CardHeader>
        <CardContent className="space-y-0 divide-y divide-zinc-800/50">
          <PhpField label="Display Errors" directive="display_errors" value={getVal('display_errors')}
            presets={['On','Off']} placeholder="Default (Off)" onChange={v => setVal('display_errors', v)} onReset={() => resetVal('display_errors')} />
          <PhpField label="Error Reporting" directive="error_reporting" value={getVal('error_reporting')}
            presets={['E_ALL','E_ALL & ~E_DEPRECATED','E_ALL & ~E_NOTICE','E_ERROR | E_WARNING']} placeholder="Default" onChange={v => setVal('error_reporting', v)} onReset={() => resetVal('error_reporting')} />
          <PhpField label="Log Errors" directive="log_errors" value={getVal('log_errors')}
            presets={['On','Off']} placeholder="Default (On)" onChange={v => setVal('log_errors', v)} onReset={() => resetVal('log_errors')} />
          <PhpField label="Short Open Tag" directive="short_open_tag" value={getVal('short_open_tag')}
            presets={['On','Off']} placeholder="Default (Off)" onChange={v => setVal('short_open_tag', v)} onReset={() => resetVal('short_open_tag')} />
        </CardContent>
      </Card>

      {/* ═══ SECTION 5: Security ═══ */}
      <Card className="bg-zinc-900 border-zinc-800">
        <CardHeader className="pb-1"><CardTitle className="text-sm text-zinc-300">Security</CardTitle></CardHeader>
        <CardContent className="space-y-0 divide-y divide-zinc-800/50">
          <PhpField label="Disable Functions" directive="disable_functions" value={getVal('disable_functions')}
            placeholder="exec,system,passthru,..." onChange={v => setVal('disable_functions', v)} onReset={() => resetVal('disable_functions')} />
          <PhpField label="Open Basedir" directive="open_basedir" value={getVal('open_basedir')}
            placeholder={openBasedirPlaceholder} onChange={v => setVal('open_basedir', v)} onReset={() => resetVal('open_basedir')} />
          <PhpField label="Allow URL Fopen" directive="allow_url_fopen" value={getVal('allow_url_fopen')}
            presets={['On','Off']} placeholder="Default (On)" onChange={v => setVal('allow_url_fopen', v)} onReset={() => resetVal('allow_url_fopen')} />
          <PhpField label="Allow URL Include" directive="allow_url_include" value={getVal('allow_url_include')}
            presets={['On','Off']} placeholder="Default (Off)" onChange={v => setVal('allow_url_include', v)} onReset={() => resetVal('allow_url_include')} />
        </CardContent>
      </Card>

      {/* ═══ SECTION 6: Session ═══ */}
      <Card className="bg-zinc-900 border-zinc-800">
        <CardHeader className="pb-1"><CardTitle className="text-sm text-zinc-300">Session & General</CardTitle></CardHeader>
        <CardContent className="space-y-0 divide-y divide-zinc-800/50">
          <PhpField label="Timezone" directive="date.timezone" value={getVal('date.timezone')}
            presets={['Europe/Istanbul','Europe/Berlin','UTC','America/New_York']} placeholder="Default" onChange={v => setVal('date.timezone', v)} onReset={() => resetVal('date.timezone')} />
          <PhpField label="Session Lifetime" directive="session.gc_maxlifetime" value={getVal('session.gc_maxlifetime')}
            presets={['1440','3600','7200','86400']} placeholder="Default (1440)" onChange={v => setVal('session.gc_maxlifetime', v)} onReset={() => resetVal('session.gc_maxlifetime')} />
          <PhpField label="Session Cookie Secure" directive="session.cookie_secure" value={getVal('session.cookie_secure')}
            presets={['1','0']} placeholder="Default (0)" onChange={v => setVal('session.cookie_secure', v)} onReset={() => resetVal('session.cookie_secure')} />
          <PhpField label="Session Cookie HttpOnly" directive="session.cookie_httponly" value={getVal('session.cookie_httponly')}
            presets={['1','0']} placeholder="Default (0)" onChange={v => setVal('session.cookie_httponly', v)} onReset={() => resetVal('session.cookie_httponly')} />
        </CardContent>
      </Card>

      {/* ═══ SECTION 7: PHP-FPM Pool Settings ═══ */}
      <Card className="bg-zinc-900 border-zinc-800">
        <CardHeader className="pb-1"><CardTitle className="text-sm text-zinc-300">PHP-FPM Pool Settings</CardTitle></CardHeader>
        <CardContent className="space-y-3">
          <div>
            <label className="text-zinc-500 text-[10px] uppercase tracking-wider block mb-1.5">Process Manager</label>
            <div className="grid grid-cols-3 gap-2">
              {(['dynamic','static','ondemand'] as const).map(mode => (
                <button key={mode} onClick={() => setVal('__pm', mode)}
                  className={cn('p-2.5 rounded-lg border text-center transition-all text-xs',
                    (edits.__pm ?? domainPool.pm) === mode
                      ? 'border-blue-500/40 bg-blue-500/10 text-blue-300 font-bold'
                      : 'border-zinc-700 bg-zinc-800/50 text-zinc-400 hover:border-zinc-600'
                  )}>
                  {mode}
                </button>
              ))}
            </div>
          </div>
          <div className="grid grid-cols-2 sm:grid-cols-3 gap-3 pt-2">
            {[
              { key: 'max_children', label: 'Max Children', val: ps?.max_children ?? 10 },
              { key: 'start_servers', label: 'Start Servers', val: ps?.start_servers ?? 2 },
              { key: 'min_spare_servers', label: 'Min Spare', val: ps?.min_spare_servers ?? 1 },
              { key: 'max_spare_servers', label: 'Max Spare', val: ps?.max_spare_servers ?? 3 },
              { key: 'max_requests', label: 'Max Requests', val: ps?.max_requests ?? 500 },
            ].map(({ key, label, val }) => (
              <div key={key}>
                <label className="text-zinc-500 text-[10px] block mb-1">{label}</label>
                <input type="number" min={0} max={1000} value={edits[`__fpm_${key}`] ?? val}
                  onChange={e => setVal(`__fpm_${key}`, e.target.value)}
                  className={cn('w-full px-3 py-1.5 text-sm font-mono rounded-lg border bg-zinc-800/50',
                    edits[`__fpm_${key}`] !== undefined ? 'border-blue-500/50 text-blue-300' : 'border-zinc-700 text-zinc-300'
                  )} />
              </div>
            ))}
          </div>
          <div className="border-t border-zinc-800 pt-2 mt-2 space-y-1">
            <Row label="Pool Name" value={domainPool.name} mono />
            <Row label="User" value={domainPool.user} mono />
            <Row label="Socket" value={domainPool.listen} mono />
          </div>
        </CardContent>
      </Card>

      {/* ═══ SECTION 8: Additional Directives (raw textarea) ═══ */}
      <Card className="bg-zinc-900 border-zinc-800">
        <CardHeader className="pb-1">
          <CardTitle className="text-sm text-zinc-300">Additional Configuration Directives</CardTitle>
          <p className="text-zinc-600 text-[10px] mt-0.5">Add custom php.ini directives (one per line). These override all settings above.</p>
        </CardHeader>
        <CardContent>
          <textarea
            rows={5}
            value={additionalDirty ? additionalDirectives : Object.entries(raw)
              .filter(([k]) => k.startsWith('php_'))
              .filter(([k]) => {
                // Exclude keys already shown in fields above
                const m = k.match(/^php_(?:admin_value|value|flag)\[(.+)\]$/)
                return m && !['memory_limit','max_execution_time','max_input_time','max_input_vars','opcache.enable',
                  'upload_max_filesize','post_max_size','max_file_uploads','display_errors','error_reporting','log_errors',
                  'short_open_tag','disable_functions','open_basedir','allow_url_fopen','allow_url_include',
                  'date.timezone','session.gc_maxlifetime','session.cookie_secure','session.cookie_httponly',
                  'opcache.memory_consumption','opcache.interned_strings_buffer','opcache.max_accelerated_files',
                  'opcache.revalidate_freq','opcache.max_wasted_percentage'].includes(m[1])
              })
              .map(([k, v]) => { const m = k.match(/\[(.+)\]/); return m ? `${m[1]} = ${v}` : '' })
              .filter(Boolean)
              .join('\n')
            }
            onChange={e => { setAdditionalDirectives(e.target.value); setAdditionalDirty(true) }}
            placeholder="extension = redis.so&#10;date.timezone = Europe/Istanbul&#10;session.save_path = /tmp"
            className="w-full px-3 py-2 text-xs font-mono rounded-lg border border-zinc-700 bg-zinc-800/50 text-zinc-300 placeholder:text-zinc-700 resize-y"
          />
        </CardContent>
      </Card>

      {/* ═══ Sticky Save Bar ═══ */}
      {isDirty && (
        <div className="sticky bottom-4 z-10">
          <div className="bg-blue-600/95 backdrop-blur rounded-xl p-3 flex items-center justify-between shadow-xl border border-blue-500/30">
            <span className="text-white text-sm font-medium">
              {Object.keys(edits).length} unsaved change{Object.keys(edits).length !== 1 ? 's' : ''}
            </span>
            <div className="flex gap-2">
              <Button size="sm" variant="ghost" className="text-white/70 hover:text-white" onClick={() => setEdits({})}>Discard</Button>
              <Button size="sm" className="bg-white text-blue-700 hover:bg-blue-50 font-semibold"
                onClick={() => saveMutation.mutate()} disabled={saveMutation.isPending}>
                {saveMutation.isPending ? <Loader2 className="w-3.5 h-3.5 mr-1.5 animate-spin" /> : <Save className="w-3.5 h-3.5 mr-1.5" />}
                Apply & Reload FPM
              </Button>
            </div>
          </div>
        </div>
      )}

      <div className="flex justify-end">
        <Link to="/php"><Button size="sm" variant="ghost" className="text-zinc-500 hover:text-white text-xs"><FileCode className="w-3.5 h-3.5 mr-1.5" />Full PHP Management →</Button></Link>
      </div>
    </div>
  )
}

// ─── SSL Tab ───────────────────────────────────────────────────────────────────

interface SslTabProps {
  domain: Domain
}

export function SslTab({ domain }: SslTabProps) {
  const certificatesQuery = useQuery<SSLCertificate[]>({
    queryKey: ['ssl', 'certificates'],
    queryFn: () => api.get<SSLCertificate[]>('/ssl/certificates'),
  })
  const certs = certificatesQuery.data

  const cert = certs?.find(
    (c) =>
      c.domain === domain.name ||
      c.sans?.includes(domain.name) ||
      (c.isWildcard && domain.name.endsWith('.' + c.domain.replace('*.', ''))),
  )

  const renewMutation = useMutation({
    mutationFn: () =>
      api.post<SSLOperationResult>(`/ssl/renew/${encodeURIComponent(domain.name)}`),
    onSuccess: (res) => {
      if (res.ok) toast.success('Certificate renewed successfully')
      else toast.error(res.message)
    },
    onError: (error: Error) => toast.error(error.message || 'Renewal failed'),
  })

  if (certificatesQuery.isLoading) {
    return (
      <div className="space-y-3">
        {Array.from({ length: 5 }).map((_, i) => (
          <Skeleton key={i} className="h-10 w-full bg-zinc-900" />
        ))}
      </div>
    )
  }

  if (certificatesQuery.isError) {
    return <DomainLoadError title="SSL certificate inventory could not be loaded." error={certificatesQuery.error} retry={() => { void certificatesQuery.refetch() }} retrying={certificatesQuery.isFetching} />
  }

  if (!cert) {
    return (
      <div className="flex flex-col items-center justify-center py-20 text-zinc-600">
        <ShieldOff className="w-8 h-8 mb-3 opacity-50" />
        <p className="text-sm">No SSL certificate found for this domain.</p>
        <Link to="/ssl" className="mt-3">
          <Button size="sm" className="bg-blue-600 hover:bg-blue-500 text-white">
            <Plus className="w-3.5 h-3.5 mr-2" />
            Issue certificate
          </Button>
        </Link>
      </div>
    )
  }

  const expiry = getExpiryStatus(cert.daysRemaining)
  const expiryColor = {
    valid: 'text-green-400',
    warning: 'text-amber-400',
    critical: 'text-red-400',
    expired: 'text-red-500',
  }[expiry]

  return (
    <div className="space-y-4">
      <Card className="bg-zinc-900 border-zinc-800">
        <CardHeader className="pb-3">
          <CardTitle className="text-sm font-semibold text-zinc-300 flex items-center justify-between">
            <span className="flex items-center gap-2">
              <Shield className="w-4 h-4 text-green-400" />
              SSL Certificate
            </span>
            <Button
              size="sm"
              onClick={() => renewMutation.mutate()}
              disabled={renewMutation.isPending}
              variant="outline"
              className="border-zinc-700 text-zinc-300 hover:text-white hover:bg-zinc-800 text-xs"
            >
              {renewMutation.isPending ? (
                <Loader2 className="w-3 h-3 mr-1.5 animate-spin" />
              ) : (
                <RefreshCw className="w-3 h-3 mr-1.5" />
              )}
              Renew
            </Button>
          </CardTitle>
        </CardHeader>
        <CardContent className="space-y-2">
          <Row label="Domain" value={cert.domain} mono />
          <Row label="Issuer" value={cert.issuer} />
          <Row label="Subject" value={cert.subject ?? cert.domain} mono />
          <Row label="Serial">
            <span className="text-xs font-mono text-zinc-500 truncate max-w-48">{cert.serial ?? "N/A"}</span>
          </Row>
          <Row label="Valid From">
            <span className="flex items-center gap-1.5 text-sm text-zinc-300">
              <Calendar className="w-3.5 h-3.5 text-zinc-500" />
              {cert.notBefore ? formatDate(cert.notBefore) : "N/A"}
            </span>
          </Row>
          <Row label="Expires">
            <span className={cn('flex items-center gap-1.5 text-sm', expiryColor)}>
              <Clock className="w-3.5 h-3.5" />
              {cert.notAfter ? formatDate(cert.notAfter) : cert.expiresAt ? formatDate(cert.expiresAt) : "N/A"} ({cert.daysRemaining}d)
            </span>
          </Row>
          <Row label="Wildcard">
            {cert.isWildcard ? (
              <CheckCircle className="w-4 h-4 text-green-400" />
            ) : (
              <XCircle className="w-4 h-4 text-zinc-600" />
            )}
          </Row>
          {cert.sans?.length > 0 && (
            <Row label="SANs">
              <div className="flex flex-wrap gap-1 justify-end">
                {cert.sans.slice(0, 5).map((san) => (
                  <span
                    key={san}
                    className="text-xs font-mono bg-zinc-800 text-zinc-400 px-1.5 py-0.5 rounded"
                  >
                    {san}
                  </span>
                ))}
                {cert.sans.length > 5 && (
                  <span className="text-xs text-zinc-600">+{cert.sans.length - 5} more</span>
                )}
              </div>
            </Row>
          )}
          <Row label="Cert Path" value={cert.certPath} mono />
          <Row label="Key Path" value={cert.keyPath} mono />
        </CardContent>
      </Card>
    </div>
  )
}

// ─── DNS Tab ───────────────────────────────────────────────────────────────────

interface DnsTabProps {
  domain: Domain
}

function DnsTab({ domain }: DnsTabProps) {
  const baseDomain = domain.name.split('.').slice(-2).join('.')

  const { data: zone, isLoading, error } = useQuery<DNSZone>({
    queryKey: ['dns', 'zone', baseDomain],
    queryFn: () => api.get<DNSZone>(`/dns/zones/${encodeURIComponent(baseDomain)}`),
    retry: false,
  })

  // Always call hooks — use `enabled` to control execution (React hook rules)
  const noBind = !!error || (!isLoading && !zone)
  const { data: cfZones } = useQuery<CFZone[]>({
    queryKey: ['cf', 'zones'],
    queryFn: () => api.get<CFZone[]>('/cloudflare/zones'),
    enabled: noBind,
  })
  const cfZone = cfZones?.find((z: CFZone) => z.name === baseDomain || baseDomain.endsWith('.' + z.name))
  const { data: cfRecords } = useQuery<CFRecord[]>({
    queryKey: ['cf', 'records', cfZone?.id],
    queryFn: () => api.get<CFRecord[]>(`/cloudflare/zones/${cfZone!.id}/records`),
    enabled: noBind && !!cfZone,
  })

  if (isLoading) {
    return (
      <div className="space-y-3">
        {Array.from({ length: 5 }).map((_, i) => (
          <Skeleton key={i} className="h-10 w-full bg-zinc-900" />
        ))}
      </div>
    )
  }

  if ((error || !zone) && cfRecords && cfRecords.length > 0) {
    // Show Cloudflare records instead
    const typeColors: Record<string, string> = {
      A: 'bg-blue-500/10 text-blue-400 border-blue-500/20',
      AAAA: 'bg-blue-500/10 text-blue-300 border-blue-500/20',
      CNAME: 'bg-purple-500/10 text-purple-400 border-purple-500/20',
      MX: 'bg-orange-500/10 text-orange-400 border-orange-500/20',
      TXT: 'bg-zinc-700/40 text-zinc-400 border-zinc-600/30',
      NS: 'bg-green-500/10 text-green-400 border-green-500/20',
    }
    return (
      <div className="space-y-4">
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-2">
            <Badge className="bg-orange-500/10 text-orange-400 border-orange-500/20 text-xs">Cloudflare</Badge>
            <span className="text-zinc-500 text-sm">{String(cfRecords.length)} records</span>
          </div>
          <Link to="/cloudflare">
            <Button size="sm" variant="outline" className="border-zinc-700 text-zinc-400 hover:text-white text-xs">
              Full Cloudflare DNS
            </Button>
          </Link>
        </div>
        <Card className="bg-zinc-900 border-zinc-800">
          <CardContent className="p-0">
            <div className="divide-y divide-zinc-800">
              {cfRecords.filter((r: CFRecord) => r.name.includes(domain.name) || r.name === baseDomain || r.name === '@').slice(0, 20).map((record: CFRecord, i: number) => (
                <div key={i} className="flex items-center gap-3 px-4 py-2.5 text-sm">
                  <Badge className={typeColors[record.type] ?? 'bg-zinc-700/40 text-zinc-400 text-xs'}>{String(record.type)}</Badge>
                  <span className="text-zinc-300 font-mono text-xs truncate flex-1">{String(record.name)}</span>
                  <span className="text-zinc-500 text-xs truncate max-w-48">{String(record.content).substring(0, 60)}</span>
                  {record.proxied && <span className="text-orange-400 text-xs">☁️</span>}
                </div>
              ))}
            </div>
          </CardContent>
        </Card>
      </div>
    )
  }

  if (error || !zone) {
    return (
      <div className="flex flex-col items-center justify-center py-20 text-zinc-600">
        <Network className="w-8 h-8 mb-3 opacity-50" />
        <p className="text-sm">{"No DNS records found for " + String(baseDomain)}</p>
        <div className="flex gap-2 mt-3">
          <Link to="/dns">
            <Button size="sm" variant="outline" className="border-zinc-700 text-zinc-400 hover:text-white">BIND Zones</Button>
          </Link>
          <Link to="/cloudflare">
            <Button size="sm" variant="outline" className="border-zinc-700 text-zinc-400 hover:text-white">Cloudflare DNS</Button>
          </Link>
        </div>
      </div>
    )
  }

  const typeColors: Record<string, string> = {
    A: 'bg-blue-500/10 text-blue-400 border-blue-500/20',
    AAAA: 'bg-blue-500/10 text-blue-300 border-blue-500/20',
    CNAME: 'bg-purple-500/10 text-purple-400 border-purple-500/20',
    MX: 'bg-orange-500/10 text-orange-400 border-orange-500/20',
    TXT: 'bg-zinc-700/40 text-zinc-400 border-zinc-600/30',
    NS: 'bg-green-500/10 text-green-400 border-green-500/20',
    SOA: 'bg-zinc-700/40 text-zinc-400 border-zinc-600/30',
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div className="text-xs text-zinc-500">
          Zone: <span className="text-zinc-300 font-mono">{zone.domain}</span>
          <span className="ml-3">Serial: <span className="font-mono">{zone.serial}</span></span>
        </div>
        <Link to="/dns">
          <Button
            size="sm"
            variant="outline"
            className="border-zinc-700 text-zinc-400 hover:text-white text-xs"
          >
            <Network className="w-3.5 h-3.5 mr-2" />
            Full DNS manager
          </Button>
        </Link>
      </div>

      <Card className="bg-zinc-900 border-zinc-800">
        <CardContent className="p-0">
          <div className="divide-y divide-zinc-800">
            {(zone.records ?? []).map((record, idx) => (
              <div
                key={idx}
                className="flex items-center gap-3 px-4 py-2.5 text-sm hover:bg-zinc-800/30 transition-colors"
              >
                <Badge
                  className={cn(
                    'text-xs w-14 justify-center flex-shrink-0',
                    typeColors[record.type] ?? 'bg-zinc-700/40 text-zinc-400 border-zinc-600/30',
                  )}
                >
                  {record.type}
                </Badge>
                <span className="font-mono text-zinc-300 w-36 truncate flex-shrink-0">
                  {record.name}
                </span>
                <span className="text-zinc-500 text-xs w-16 flex-shrink-0">{record.ttl}s</span>
                <span className="font-mono text-zinc-400 text-xs truncate flex-1">{record.value}</span>
                {record.priority !== undefined && (
                  <span className="text-zinc-600 text-xs">prio: {record.priority}</span>
                )}
              </div>
            ))}
          </div>
        </CardContent>
      </Card>
    </div>
  )
}

// ─── File Browser Tab ──────────────────────────────────────────────────────────

interface FileEntry {
  name: string
  is_dir: boolean
  size: number
  permissions: string
  owner: string
  modified: string
}

interface FileListResponse {
  path: string
  entries: FileEntry[]
}

interface FileReadResponse {
  path: string
  content: string
}

export function FilesTab({ domain }: { domain: Domain }) {
  const defaultRoot = domain.root.trim()
  const [currentPath, setCurrentPath] = useState(defaultRoot)
  const [editFile, setEditFile] = useState<{ path: string; content: string } | null>(null)
  const [editContent, setEditContent] = useState('')
  const [isDirty, setIsDirty] = useState(false)

  const listingQuery = useQuery<FileListResponse>({
    queryKey: ['files', currentPath],
    queryFn: () => api.get<FileListResponse>(`/files?path=${encodeURIComponent(currentPath)}`),
    enabled: defaultRoot !== '',
  })
  const listing = listingQuery.data

  const readMutation = useMutation({
    mutationFn: (path: string) => api.get<FileReadResponse>(`/files/read?path=${encodeURIComponent(path)}`),
    onSuccess: (data) => {
      setEditFile({ path: data.path, content: data.content })
      setEditContent(data.content)
      setIsDirty(false)
    },
    onError: (error: Error) => toast.error(error.message || 'Cannot read file'),
  })

  const saveMutation = useMutation({
    mutationFn: () =>
      api.put<{ ok: boolean }>('/files/write', { path: editFile?.path, content: editContent }),
    onSuccess: () => {
      setIsDirty(false)
      toast.success('File saved')
    },
    onError: (error: Error) => toast.error(error.message || 'Failed to save file'),
  })

  if (defaultRoot === '') {
    return (
      <div className="flex flex-col items-center justify-center py-20 text-zinc-600">
        <AlertTriangle className="mb-3 size-8 opacity-50" />
        <p className="text-sm text-zinc-400">Domain document root is unavailable.</p>
        <p className="mt-1 text-xs">File controls are paused instead of guessing an installation path.</p>
      </div>
    )
  }

  function navigate(path: string) {
    setCurrentPath(path)
    setEditFile(null)
  }

  const parts = currentPath.split('/').filter(Boolean)

  if (editFile) {
    return (
      <div className="space-y-3">
        {/* Editor toolbar */}
        <div className="flex items-center justify-between gap-2 flex-wrap">
          <div className="flex items-center gap-2">
            <button
              onClick={() => setEditFile(null)}
              className="text-zinc-400 hover:text-white transition-colors text-xs flex items-center gap-1"
            >
              <ArrowLeft className="w-3.5 h-3.5" /> back
            </button>
            <span className="text-xs font-mono text-zinc-500 truncate max-w-sm">{editFile.path}</span>
            {isDirty && (
              <Badge className="bg-amber-500/10 text-amber-400 border-amber-500/20 text-xs">
                unsaved
              </Badge>
            )}
          </div>
          <Button
            size="sm"
            onClick={() => saveMutation.mutate()}
            disabled={!isDirty || saveMutation.isPending}
            className="bg-blue-600 hover:bg-blue-500 text-white disabled:opacity-40"
          >
            {saveMutation.isPending ? (
              <Loader2 className="w-3.5 h-3.5 mr-2 animate-spin" />
            ) : (
              <Save className="w-3.5 h-3.5 mr-2" />
            )}
            Save
          </Button>
        </div>
        <div className="rounded-lg overflow-hidden border border-zinc-800">
          <Editor
            height="480px"
            language={getLanguage(editFile.path)}
            theme="vs-dark"
            value={editContent}
            onChange={(val) => {
              setEditContent(val ?? '')
              setIsDirty(true)
            }}
            options={{
              fontSize: 13,
              minimap: { enabled: false },
              scrollBeyondLastLine: false,
              wordWrap: 'on',
              tabSize: 2,
            }}
          />
        </div>
      </div>
    )
  }

  if (listingQuery.isError) {
    return <DomainLoadError title={`Files in ${currentPath} could not be loaded. File controls are paused.`} error={listingQuery.error} retry={() => { void listingQuery.refetch() }} retrying={listingQuery.isFetching} />
  }

  return (
    <div className="space-y-3">
      {/* Breadcrumbs */}
      <div className="flex items-center gap-1 flex-wrap text-xs">
        <button
          onClick={() => navigate('/')}
          className="text-zinc-400 hover:text-white transition-colors"
        >
          <Home className="w-3.5 h-3.5" />
        </button>
        {parts.map((part, idx) => {
          const segPath = '/' + parts.slice(0, idx + 1).join('/')
          const isLast = idx === parts.length - 1
          return (
            <span key={segPath} className="flex items-center gap-1">
              <ChevronRight className="w-3 h-3 text-zinc-600" />
              {isLast ? (
                <span className="text-white font-medium">{part}</span>
              ) : (
                <button
                  onClick={() => navigate(segPath)}
                  className="text-zinc-400 hover:text-white transition-colors"
                >
                  {part}
                </button>
              )}
            </span>
          )
        })}
      </div>

      {/* File list */}
      {listingQuery.isLoading ? (
        <div className="space-y-2">
          {Array.from({ length: 6 }).map((_, i) => (
            <Skeleton key={i} className="h-9 w-full bg-zinc-900" />
          ))}
        </div>
      ) : (
        <Card className="bg-zinc-900 border-zinc-800">
          <CardContent className="p-0">
            {currentPath !== '/' && (
              <button
                onClick={() => navigate('/' + parts.slice(0, -1).join('/'))}
                className="flex items-center gap-3 px-4 py-2.5 w-full text-left hover:bg-zinc-800/40 transition-colors border-b border-zinc-800"
              >
                <Folder className="w-4 h-4 text-zinc-600" />
                <span className="text-zinc-500 text-sm">..</span>
              </button>
            )}
            <div className="divide-y divide-zinc-800">
              {(listing?.entries ?? []).map((entry) => (
                <button
                  key={entry.name}
                  onClick={() => {
                    if (entry.is_dir) {
                      navigate(joinPath(currentPath, entry.name))
                    } else {
                      readMutation.mutate(joinPath(currentPath, entry.name))
                    }
                  }}
                  className="flex items-center gap-3 px-4 py-2.5 w-full text-left hover:bg-zinc-800/40 transition-colors group"
                >
                  {entry.is_dir ? (
                    <Folder className="w-4 h-4 text-blue-400 flex-shrink-0" />
                  ) : (
                    <FileIcon name={entry.name} />
                  )}
                  <span className="text-sm text-zinc-300 group-hover:text-white flex-1 truncate">
                    {entry.name}
                  </span>
                  {!entry.is_dir && (
                    <span className="text-xs text-zinc-600 flex-shrink-0">{formatSize(entry.size)}</span>
                  )}
                  <span className="text-xs text-zinc-700 flex-shrink-0 hidden sm:block">
                    {entry.modified ? new Date(entry.modified).toLocaleDateString('en-GB', { day: '2-digit', month: 'short' }) : ''}
                  </span>
                </button>
              ))}
              {(listing?.entries ?? []).length === 0 && (
                <div className="flex items-center justify-center py-12 text-zinc-600 text-sm">
                  Empty directory
                </div>
              )}
            </div>
          </CardContent>
        </Card>
      )}
      {readMutation.isPending && (
        <div className="fixed bottom-4 right-4 bg-zinc-900 border border-zinc-800 rounded-lg px-4 py-2 flex items-center gap-2 text-sm text-zinc-300">
          <Loader2 className="w-3.5 h-3.5 animate-spin" />
          Loading file...
        </div>
      )}
    </div>
  )
}

function FileIcon({ name }: { name: string }) {
  const ext = name.split('.').pop()?.toLowerCase() ?? ''
  if (['conf', 'nginx', 'ini', 'env'].includes(ext))
    return <FileCog className="w-4 h-4 text-amber-400 flex-shrink-0" />
  if (['ts', 'tsx', 'js', 'jsx', 'php', 'go', 'py', 'sh'].includes(ext))
    return <FileCode className="w-4 h-4 text-green-400 flex-shrink-0" />
  if (['log', 'txt', 'md'].includes(ext))
    return <FileText className="w-4 h-4 text-zinc-400 flex-shrink-0" />
  return <File className="w-4 h-4 text-zinc-500 flex-shrink-0" />
}

// ─── Logs Tab ──────────────────────────────────────────────────────────────────

interface LogsTabProps {
  domain: Domain
}

export function LogsTab({ domain }: LogsTabProps) {
  const [logType, setLogType] = useState<'access' | 'error'>('access')

  // Try domain-specific log first, fallback to general nginx log
  const domainAccessLog = `/var/log/nginx/${String(domain.name)}-access.log`
  const domainErrorLog = `/var/log/nginx/${String(domain.name)}-error.log`
  const generalAccessLog = '/var/log/nginx/access.log'
  const generalErrorLog = '/var/log/nginx/error.log'
  const primaryLogPath = logType === 'access' ? domainAccessLog : domainErrorLog
  const fallbackLogPath = logType === 'access' ? generalAccessLog : generalErrorLog

  const {
    data: logData,
    isLoading,
    isError,
    error,
    refetch,
    isFetching,
  } = useQuery<{ lines?: string[]; content?: string; resolvedPath: string; fallback: boolean }>({
    queryKey: ['logs', 'domain', domain.name, logType],
    queryFn: async () => {
      try {
        const result = await api.get<{ lines: string[]; content?: string }>(`/logs/read?path=${encodeURIComponent(primaryLogPath)}&lines=200`)
        return { ...result, resolvedPath: primaryLogPath, fallback: false }
      } catch (primaryError) {
        try {
          const result = await api.get<{ lines: string[]; content?: string }>(`/logs/read?path=${encodeURIComponent(fallbackLogPath)}&lines=200`)
          return { ...result, resolvedPath: fallbackLogPath, fallback: true }
        } catch (fallbackError) {
          const primaryMessage = primaryError instanceof Error ? primaryError.message : 'domain log unavailable'
          const fallbackMessage = fallbackError instanceof Error ? fallbackError.message : 'general log unavailable'
          throw new Error(`${primaryMessage}; fallback failed: ${fallbackMessage}`, { cause: fallbackError })
        }
      }
    },
    retry: false,
  })

  const lines: string[] = logData?.lines ?? logData?.content?.split('\n').filter(Boolean) ?? []

  return (
    <div className="space-y-3">
      {/* Log selector */}
      <div className="flex items-center justify-between flex-wrap gap-2">
        <div className="flex rounded-lg overflow-hidden border border-zinc-800">
          {(['access', 'error'] as const).map((type) => (
            <button
              key={type}
              onClick={() => setLogType(type)}
              className={cn(
                'px-4 py-1.5 text-sm font-medium transition-colors capitalize',
                logType === type
                  ? 'bg-zinc-800 text-white'
                  : 'text-zinc-500 hover:text-zinc-300',
              )}
            >
              {type}
            </button>
          ))}
        </div>
        <Button
          size="sm"
          variant="outline"
          onClick={() => refetch()}
          disabled={isFetching}
          className="border-zinc-700 text-zinc-400 hover:text-white"
        >
          {isFetching ? (
            <Loader2 className="w-3.5 h-3.5 mr-2 animate-spin" />
          ) : (
            <RefreshCw className="w-3.5 h-3.5 mr-2" />
          )}
          Refresh
        </Button>
      </div>

      <div className="text-xs text-zinc-600 font-mono">{logData?.resolvedPath ?? primaryLogPath}</div>
      {logData?.fallback && (
        <div className="rounded-lg border border-amber-500/20 bg-amber-500/[0.05] px-3 py-2 text-xs text-amber-200">
          The domain-specific log is unavailable; showing the general nginx {logType} log.
        </div>
      )}

      {isLoading ? (
        <Skeleton className="h-96 w-full bg-zinc-900" />
      ) : isError ? (
        <Card className="border-red-500/25 bg-red-500/[0.05]">
          <CardContent className="p-6 text-center">
            <AlertTriangle className="mx-auto size-5 text-red-400" />
            <p className="mt-2 text-sm text-red-300">Neither the domain-specific nor general nginx log could be loaded.</p>
            <p className="mt-1 text-xs text-zinc-600">{error.message}</p>
            <Button type="button" variant="outline" size="sm" className="mt-4 border-red-500/30 text-red-200" onClick={() => { void refetch() }} disabled={isFetching}>
              <RefreshCw className={cn('mr-2 size-3.5', isFetching && 'animate-spin')} />Retry
            </Button>
          </CardContent>
        </Card>
      ) : lines.length > 0 ? (
        <div className="bg-zinc-950 border border-zinc-800 rounded-lg overflow-auto max-h-[500px] p-4">
          <div className="space-y-0.5">
            {lines.slice(-500).map((line, idx) => {
              const isError =
                line.includes(' 5') ||
                line.includes('[error]') ||
                line.includes('[crit]') ||
                line.includes('[emerg]')
              const isWarn = line.includes(' 4') || line.includes('[warn]')
              return (
                <div
                  key={idx}
                  className={cn(
                    'font-mono text-xs leading-5 whitespace-pre-wrap break-all',
                    isError ? 'text-red-400' : isWarn ? 'text-amber-400' : 'text-zinc-400',
                  )}
                >
                  {line}
                </div>
              )
            })}
          </div>
        </div>
      ) : (
        <div className="flex flex-col items-center justify-center py-20 text-zinc-600">
          <ScrollText className="w-8 h-8 mb-3 opacity-50" />
          <p className="text-sm">Log file is empty</p>
        </div>
      )}
    </div>
  )
}

// ─── Uptime Tab ────────────────────────────────────────────────────────────────

type UptimePeriod = '24h' | '7d' | '30d'

const periodHours: Record<UptimePeriod, number> = { '24h': 24, '7d': 168, '30d': 720 }

function formatUptime(pct?: number): string {
  if (pct == null) return '—'
  if (pct === 100) return '100%'
  if (pct === 0) return '0%'
  return `${pct.toFixed(1)}%`
}

function formatDuration(secs?: number): string {
  if (!secs) return '—'
  if (secs < 60) return `${secs}s`
  if (secs < 3600) return `${Math.floor(secs / 60)}m`
  return `${Math.floor(secs / 3600)}h ${Math.floor((secs % 3600) / 60)}m`
}

function formatTimeAgo(iso?: string): string {
  if (!iso) return '—'
  const d = new Date(iso)
  const now = new Date()
  const diff = Math.floor((now.getTime() - d.getTime()) / 1000)
  if (diff < 60) return `${diff}s ago`
  if (diff < 3600) return `${Math.floor(diff / 60)}m ago`
  if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`
  return d.toLocaleDateString()
}

function DomainUptimeBar({ heartbeats }: { heartbeats: UptimeHeartbeat[] }) {
  const days: { date: string; status: number }[] = []
  const now = new Date()
  for (let i = 89; i >= 0; i--) {
    const d = new Date(now)
    d.setDate(d.getDate() - i)
    const key = d.toISOString().split('T')[0]
    const dayBeats = heartbeats.filter((h) => h.created_at.startsWith(key))
    const upCount = dayBeats.filter((h) => h.status === 1).length
    const total = dayBeats.length
    let status = -1 // no data
    if (total > 0) status = upCount / total >= 0.9 ? 1 : 0
    days.push({ date: key, status })
  }

  return (
    <div className="flex gap-0.5 items-end h-8">
      {days.map((d) => (
        <div
          key={d.date}
          title={`${d.date}: ${d.status === 1 ? 'Up' : d.status === 0 ? 'Down' : 'No data'}`}
          className={cn(
            'flex-1 rounded-sm h-full cursor-default transition-opacity hover:opacity-75',
            d.status === 1 && 'bg-green-500',
            d.status === 0 && 'bg-red-500',
            d.status === -1 && 'bg-zinc-700',
          )}
        />
      ))}
    </div>
  )
}

function SetupMonitoringCard({ domain }: { domain: Domain }) {
  const queryClient = useQueryClient()
  const createMutation = useMutation({
    mutationFn: () =>
      api.post('/uptime/monitors', {
        name: domain.name,
        type: 'http',
        url: `https://${domain.name}`,
        interval_secs: 60,
        timeout_secs: 10,
        retries: 1,
        retry_interval: 30,
        accepted_statuscodes: '200-299',
        max_redirects: 5,
        is_active: true,
        tls_check: true,
        tls_expiry_warn_days: 14,
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['uptime', 'monitor-by-domain', domain.name] })
      toast.success('Uptime monitoring enabled')
    },
    onError: () => toast.error('Failed to enable monitoring'),
  })

  return (
    <Card className="bg-zinc-900 border-dashed border-zinc-700">
      <CardContent className="flex flex-col items-center justify-center py-16">
        <Radar className="w-10 h-10 text-zinc-600 mb-4" />
        <h3 className="text-white font-medium mb-1">No Uptime Monitoring</h3>
        <p className="text-zinc-500 text-sm mb-6 text-center">
          Start monitoring {domain.name} availability and response time
        </p>
        <Button
          onClick={() => createMutation.mutate()}
          disabled={createMutation.isPending}
          className="bg-blue-600 hover:bg-blue-500 text-white"
        >
          {createMutation.isPending ? (
            <Loader2 className="w-4 h-4 mr-2 animate-spin" />
          ) : (
            <Plus className="w-4 h-4 mr-2" />
          )}
          Enable Monitoring
        </Button>
      </CardContent>
    </Card>
  )
}

export function UptimeTab({ domain }: { domain: Domain }) {
  const [period, setPeriod] = useState<UptimePeriod>('24h')

  // by-domain returns {monitor: UptimeMonitor, stats: UptimeStats}
  const monitorQuery = useQuery<{monitor: UptimeMonitor, stats: UptimeStats | null}>({
    queryKey: ['uptime', 'monitor-by-domain', domain.name],
    queryFn: () => api.get(`/uptime/monitor-by-domain/${encodeURIComponent(domain.name)}`),
    retry: false,
  })
  const byDomainResp = monitorQuery.data
  const monitor = byDomainResp?.monitor ?? null
  const stats = byDomainResp?.stats ?? null

  const heartbeatsQuery = useQuery<UptimeHeartbeat[]>({
    queryKey: ['uptime', 'heartbeats', monitor?.id, period],
    queryFn: () =>
      api.get<UptimeHeartbeat[]>(
        `/uptime/monitors/${monitor!.id}/heartbeats?hours=${periodHours[period]}`,
      ),
    enabled: !!monitor,
  })
  const heartbeats = heartbeatsQuery.data ?? []

  const archiveQuery = useQuery<UptimeHeartbeat[]>({
    queryKey: ['uptime', 'heartbeats', monitor?.id, '2160h'],
    queryFn: () =>
      api.get<UptimeHeartbeat[]>(`/uptime/monitors/${monitor!.id}/heartbeats?hours=2160`),
    enabled: !!monitor,
  })
  const allHeartbeats = archiveQuery.data ?? []

  const incidentsQuery = useQuery<UptimeIncident[]>({
    queryKey: ['uptime', 'incidents', monitor?.id],
    queryFn: () =>
      api.get<UptimeIncident[]>(`/uptime/monitors/${monitor!.id}/incidents`),
    enabled: !!monitor,
  })
  const incidents = incidentsQuery.data ?? []

  // Build chart data from heartbeats
  const chartData = heartbeats.slice(-120).map((h) => ({
    time: new Date(h.created_at).toLocaleTimeString('en', {
      hour: '2-digit',
      minute: '2-digit',
    }),
    ms: h.ping_ms ?? 0,
    status: h.status,
  }))

  // Loading skeleton
  if (monitorQuery.isLoading) {
    return (
      <div className="space-y-4">
        <Skeleton className="h-24 w-full bg-zinc-900" />
        <Skeleton className="h-12 w-full bg-zinc-900" />
        <div className="grid grid-cols-4 gap-3">
          {Array.from({ length: 4 }).map((_, i) => (
            <Skeleton key={i} className="h-20 bg-zinc-900" />
          ))}
        </div>
        <Skeleton className="h-36 w-full bg-zinc-900" />
      </div>
    )
  }

  if (monitorQuery.isError && !(monitorQuery.error instanceof ApiError && monitorQuery.error.status === 404)) {
    return <DomainLoadError title={`Uptime monitor for ${domain.name} could not be loaded.`} error={monitorQuery.error} retry={() => { void monitorQuery.refetch() }} retrying={monitorQuery.isFetching} />
  }

  // No monitor — show setup card
  if (!monitor) {
    return <SetupMonitoringCard domain={domain} />
  }

  const isUp = monitor.current_status === 1
  const isPaused = !monitor.is_active
  const statusColor = isPaused
    ? 'text-yellow-400'
    : isUp
      ? 'text-green-400'
      : 'text-red-400'
  const statusLabel = isPaused ? 'Paused' : isUp ? 'Up' : 'Down'
  const dotColor = isPaused
    ? 'bg-yellow-400'
    : isUp
      ? 'bg-green-400 shadow-[0_0_6px_theme(colors.green.400)]'
      : 'bg-red-400 shadow-[0_0_6px_theme(colors.red.400)]'

  return (
    <div className="space-y-6">
      {/* Status banner */}
      <Card className="bg-zinc-900 border-zinc-800">
        <CardContent className="py-4">
          <div className="flex items-center justify-between flex-wrap gap-3">
            <div className="flex items-center gap-3">
              <span className={cn('inline-block w-3 h-3 rounded-full flex-shrink-0', dotColor)} />
              <div>
                <p className="font-medium text-white leading-tight">{domain.name}</p>
                <p className="text-xs text-zinc-500 mt-0.5">
                  Last checked: {monitor.last_check_at ? formatTimeAgo(monitor.last_check_at) : '—'}
                </p>
              </div>
              <Badge
                className={cn(
                  'text-xs ml-1',
                  isPaused
                    ? 'bg-yellow-500/10 text-yellow-400 border-yellow-500/20'
                    : isUp
                      ? 'bg-green-500/10 text-green-400 border-green-500/20'
                      : 'bg-red-500/10 text-red-400 border-red-500/20',
                )}
              >
                {statusLabel}
              </Badge>
            </div>
            <div className="text-right">
              <p className={cn('text-2xl font-bold', statusColor)}>
                {formatUptime(stats?.uptime_24h)}
              </p>
              <p className="text-xs text-zinc-500">24h uptime</p>
            </div>
          </div>
        </CardContent>
      </Card>

      {/* 90-day uptime bar */}
      <Card className="bg-zinc-900 border-zinc-800">
        <CardContent className="pt-4 pb-4">
          <div className="flex items-center justify-between mb-2">
            <span className="text-xs text-zinc-400 font-medium">90-day availability</span>
            <span className="text-xs text-zinc-400">{formatUptime(stats?.uptime_90d)}</span>
          </div>
          {archiveQuery.isError ? (
            <DomainLoadError title="90-day heartbeat history could not be loaded." error={archiveQuery.error} retry={() => { void archiveQuery.refetch() }} retrying={archiveQuery.isFetching} />
          ) : (
            <DomainUptimeBar heartbeats={allHeartbeats} />
          )}
          <div className="flex justify-between text-xs text-zinc-600 mt-1.5">
            <span>90 days ago</span>
            <span>Today</span>
          </div>
        </CardContent>
      </Card>

      {/* Stats grid */}
      <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
        {[
          { label: 'Uptime 24h', value: formatUptime(stats?.uptime_24h) },
          { label: 'Uptime 7d', value: formatUptime(stats?.uptime_7d) },
          { label: 'Uptime 30d', value: formatUptime(stats?.uptime_30d) },
          { label: 'Uptime 90d', value: formatUptime(stats?.uptime_90d) },
        ].map((s) => (
          <Card key={s.label} className="bg-zinc-900 border-zinc-800">
            <CardContent className="py-3 text-center">
              <div className="text-xl font-bold text-white">{s.value}</div>
              <div className="text-xs text-zinc-500 mt-0.5">{s.label}</div>
            </CardContent>
          </Card>
        ))}
      </div>

      {/* Response time stats */}
      <div className="grid grid-cols-3 gap-3">
        {[
          {
            label: 'Avg response',
            value: stats?.avg_ping_ms != null ? `${Math.round(stats.avg_ping_ms)}ms` : '—',
          },
          {
            label: 'P95 response',
            value: stats?.p95_ping_ms != null ? `${Math.round(stats.p95_ping_ms)}ms` : '—',
          },
          {
            label: 'P99 response',
            value: stats?.p99_ping_ms != null ? `${Math.round(stats.p99_ping_ms)}ms` : '—',
          },
        ].map((s) => (
          <Card key={s.label} className="bg-zinc-900 border-zinc-800">
            <CardContent className="py-3 text-center">
              <div className="text-base font-semibold text-white">{s.value}</div>
              <div className="text-xs text-zinc-500 mt-0.5">{s.label}</div>
            </CardContent>
          </Card>
        ))}
      </div>

      {/* Response time chart */}
      <Card className="bg-zinc-900 border-zinc-800">
        <CardContent className="pt-4 pb-4">
          <div className="flex items-center justify-between mb-3">
            <div className="flex items-center gap-2">
              <Activity className="w-3.5 h-3.5 text-zinc-400" />
              <span className="text-xs text-zinc-400 font-medium">Response Time</span>
            </div>
            <div className="flex gap-1">
              {(['24h', '7d', '30d'] as UptimePeriod[]).map((p) => (
                <button
                  key={p}
                  onClick={() => setPeriod(p)}
                  className={cn(
                    'px-2 py-0.5 text-xs rounded transition-colors',
                    period === p
                      ? 'bg-blue-600 text-white'
                      : 'bg-zinc-800 text-zinc-400 hover:text-white',
                  )}
                >
                  {p}
                </button>
              ))}
            </div>
          </div>
          {heartbeatsQuery.isLoading ? (
            <Skeleton className="h-36 w-full bg-zinc-800" />
          ) : heartbeatsQuery.isError ? (
            <DomainLoadError title="Response-time history could not be loaded." error={heartbeatsQuery.error} retry={() => { void heartbeatsQuery.refetch() }} retrying={heartbeatsQuery.isFetching} />
          ) : chartData.length === 0 ? (
            <div className="h-36 flex items-center justify-center text-zinc-600 text-sm">
              No data yet
            </div>
          ) : (
            <ResponsiveContainer width="100%" height={140}>
              <LineChart data={chartData} margin={{ top: 4, right: 4, bottom: 0, left: -20 }}>
                <CartesianGrid strokeDasharray="3 3" stroke="#27272a" />
                <XAxis
                  dataKey="time"
                  tick={{ fill: '#71717a', fontSize: 10 }}
                  tickLine={false}
                  interval="preserveStartEnd"
                />
                <YAxis
                  tick={{ fill: '#71717a', fontSize: 10 }}
                  tickLine={false}
                  axisLine={false}
                />
                <RechartsTooltip
                  contentStyle={{
                    background: '#18181b',
                    border: '1px solid #3f3f46',
                    borderRadius: 6,
                    fontSize: 12,
                  }}
                  labelStyle={{ color: '#a1a1aa' }}
                  itemStyle={{ color: '#60a5fa' }}
                  formatter={(v) => [`${Number(v ?? 0)}ms`, 'Response']}
                />
                <Line
                  type="monotone"
                  dataKey="ms"
                  stroke="#3b82f6"
                  strokeWidth={1.5}
                  dot={false}
                  isAnimationActive={false}
                />
              </LineChart>
            </ResponsiveContainer>
          )}
        </CardContent>
      </Card>

      {/* Recent incidents */}
      {incidentsQuery.isError ? (
        <DomainLoadError title="Uptime incidents could not be loaded." error={incidentsQuery.error} retry={() => { void incidentsQuery.refetch() }} retrying={incidentsQuery.isFetching} />
      ) : incidents.length > 0 && (
        <Card className="bg-zinc-900 border-zinc-800">
          <CardHeader className="pb-2 pt-4">
            <CardTitle className="text-sm font-semibold text-zinc-300">Recent Incidents</CardTitle>
          </CardHeader>
          <CardContent className="pb-4 space-y-1.5">
            {incidents.slice(0, 10).map((inc) => (
              <div
                key={inc.id}
                className="flex items-center justify-between bg-zinc-800/60 rounded px-3 py-2 text-xs"
              >
                <div className="flex items-center gap-2">
                  {inc.resolved_at ? (
                    <CheckCircle2 className="w-3.5 h-3.5 text-green-400 shrink-0" />
                  ) : (
                    <XCircle className="w-3.5 h-3.5 text-red-400 shrink-0" />
                  )}
                  <span className="text-zinc-300">{inc.cause ?? inc.type}</span>
                </div>
                <div className="flex items-center gap-3 text-zinc-500">
                  <span>{new Date(inc.started_at).toLocaleString()}</span>
                  {inc.duration_secs != null && (
                    <span className="text-zinc-600">{formatDuration(inc.duration_secs)}</span>
                  )}
                </div>
              </div>
            ))}
          </CardContent>
        </Card>
      )}

      {!incidentsQuery.isError && incidents.length === 0 && (
        <div className="flex flex-col items-center justify-center py-8 text-zinc-600">
          <CheckCircle2 className="w-7 h-7 mb-2 text-green-500/40" />
          <p className="text-sm">No incidents recorded</p>
        </div>
      )}
    </div>
  )
}

// ─── Main page ─────────────────────────────────────────────────────────────────

export default function DomainDetail() {
  const { name } = useParams<{ name: string }>()
  const navigate = useNavigate()
  const [activeTab, setActiveTab] = useState<TabId>('overview')

  const domainsQuery = useQuery<{ domains: Domain[] }>({
    queryKey: ['domains'],
    queryFn: () => api.get<{ domains: Domain[] }>('/domains'),
  })
  const domainsResp = domainsQuery.data

  const domain = domainsResp?.domains?.find((d) => d.name === name)

  if (domainsQuery.isLoading) {
    return (
      <div className="space-y-6">
        <Skeleton className="h-10 w-64 bg-zinc-900" />
        <Skeleton className="h-10 w-full bg-zinc-900" />
        <Skeleton className="h-64 w-full bg-zinc-900" />
      </div>
    )
  }

  if (domainsQuery.isError) {
    return <DomainLoadError title="Domain inventory could not be loaded." error={domainsQuery.error} retry={() => { void domainsQuery.refetch() }} retrying={domainsQuery.isFetching} />
  }

  if (!domain) {
    return (
      <div className="flex flex-col items-center justify-center py-32 text-zinc-600">
        <Globe className="w-12 h-12 mb-4 opacity-30" />
        <p className="text-base font-medium text-zinc-400">Domain not found</p>
        <p className="text-sm mt-1 mb-6">"{name}" is not in the domain list</p>
        <Button
          onClick={() => navigate('/domains')}
          variant="outline"
          className="border-zinc-700 text-zinc-300 hover:text-white"
        >
          <ArrowLeft className="w-4 h-4 mr-2" />
          Back to Domains
        </Button>
      </div>
    )
  }

  const visibleTabs = TABS.filter((tab) => {
    if (tab.id === 'php' && !domain.phpVersion) return true // still show, just with message
    return true
  })

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center gap-4 flex-wrap">
        <button
          onClick={() => navigate('/domains')}
          className="text-zinc-400 hover:text-white transition-colors flex items-center gap-1.5 text-sm"
        >
          <ArrowLeft className="w-4 h-4" />
          Domains
        </button>
        <ChevronRight className="w-4 h-4 text-zinc-700" />
        <div className="flex items-center gap-3">
          <div className="w-8 h-8 bg-blue-600/10 border border-blue-600/20 rounded-lg flex items-center justify-center">
            <Globe className="w-4 h-4 text-blue-400" />
          </div>
          <div>
            <h2 className="text-white font-semibold text-base leading-tight">{String(domain.name)}</h2>
            <div className="flex items-center gap-2 mt-0.5">
              <Badge className="text-xs">{String(domain.type || "static")}</Badge>
              {domain.sslEnabled ? (
                <Badge className="bg-green-500/10 text-green-400 border-green-500/20 text-xs">
                  <Shield className="w-2.5 h-2.5 mr-1" />
                  SSL
                </Badge>
              ) : (
                <Badge variant="outline" className="border-zinc-700 text-zinc-500 text-xs">
                  No SSL
                </Badge>
              )}
              {domain.phpVersion && (
                <Badge className="bg-purple-500/10 text-purple-400 border-purple-500/20 text-xs">
                  PHP {String(domain.phpVersion)}
                </Badge>
              )}
            </div>
          </div>
        </div>
        <div className="ml-auto">
          <Button
            size="sm"
            variant="outline"
            onClick={() => window.open(`https://${String(domain.name)}`, '_blank')}
            className="border-zinc-700 text-zinc-300 hover:text-white hover:bg-zinc-800"
          >
            <ExternalLink className="w-3.5 h-3.5 mr-2" />
            Open site
          </Button>
        </div>
      </div>

      {/* Tabs */}
      <div className="border-b border-zinc-800">
        <nav className="flex gap-0 -mb-px overflow-x-auto scrollbar-none">
          {visibleTabs.map((tab) => (
            <button
              key={tab.id}
              onClick={() => setActiveTab(tab.id)}
              className={cn(
                'flex items-center gap-1.5 px-4 py-2.5 text-sm font-medium border-b-2 whitespace-nowrap transition-colors',
                activeTab === tab.id
                  ? 'border-blue-500 text-white'
                  : 'border-transparent text-zinc-500 hover:text-zinc-300',
              )}
            >
              {tab.icon}
              {tab.label}
            </button>
          ))}
        </nav>
      </div>

      {/* Tab content */}
      <div>
        <TabSafe key={activeTab}>
          {activeTab === 'overview' && <OverviewTab domain={domain} />}
          {activeTab === 'uptime' && <UptimeTab domain={domain} />}
          {activeTab === 'nginx' && <NginxTab domain={domain} />}
          {activeTab === 'php' && <PhpTab domain={domain} />}
          {activeTab === 'ssl' && <SslTab domain={domain} />}
          {activeTab === 'dns' && <DnsTab domain={domain} />}
          {activeTab === 'files' && <FilesTab domain={domain} />}
          {activeTab === 'logs' && <LogsTab domain={domain} />}
        </TabSafe>
      </div>
    </div>
  )
}
