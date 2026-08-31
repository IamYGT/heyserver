import { useState, useCallback } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from 'react-router-dom'
import {
  Globe, Shield, ShieldOff, FolderOpen, Plus, MoreHorizontal, Trash2,
  ExternalLink, Loader2, ChevronRight, Server, FileCode, Rocket,
  Lock, ChevronDown, ChevronUp, CheckCircle2, Circle, AlertTriangle, RefreshCw,
} from 'lucide-react'
import { Card, CardContent } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { api } from '@/lib/api'
import {
  DEFAULT_VHOSTS_ROOT,
  autoCwd,
  autoWebRoot,
  dnsCapabilityDescription,
  normalizeVhostsRoot,
  type DomainCreateRequest,
  type DomainProvisioningCapabilities,
  type LocalDomainType,
  type SupportedDomainPHPVersion,
} from '@/lib/domainProvisioning'
import { toast } from 'sonner'
import type { Domain, PHPVersion } from '@/lib/types'
import EmptyState from '@/components/EmptyState'

// ── Types ────────────────────────────────────────────────────────────────────

type AppType = LocalDomainType
type FpmPreset = 'low' | 'medium' | 'high'

const supportedPHPVersions = new Set<SupportedDomainPHPVersion>(['7.4', '8.0', '8.1', '8.2', '8.3', '8.4', '8.5'])

function isSupportedPHPVersion(version: string): version is SupportedDomainPHPVersion {
  return supportedPHPVersions.has(version as SupportedDomainPHPVersion)
}

interface CreateDomainForm {
  // Section 1
  domain: string
  // Section 2
  type: AppType
  // Section 3 — PHP
  phpVersion: string
  webRoot: string
  fpmPreset: FpmPreset
  // Section 3 — Node.js / proxy
  pm2App: string
  pm2Script: string
  pm2Cwd: string
  proxyPort: string
  nodeEnv: 'production' | 'development'
  // Section 3 — Static
  spaMode: boolean
  // Section 4 — SSL & DNS
  issueSSL: boolean
  sslEmail: string
  createDnsRecord: boolean
  wwwRedirect: boolean
}

// ── Helpers ──────────────────────────────────────────────────────────────────

function domainToSlug(domain: string): string {
  return domain.replace(/\./g, '-').replace(/[^a-z0-9-]/g, '')
}

function detectParent(domain: string): string | null {
  const parts = domain.trim().split('.')
  if (parts.length >= 3) {
    return parts.slice(1).join('.')
  }
  return null
}

function defaultForm(vhostsRoot = DEFAULT_VHOSTS_ROOT): CreateDomainForm {
  return {
    domain: '',
    type: 'php',
    phpVersion: '8.4',
    webRoot: `${normalizeVhostsRoot(vhostsRoot)}/`,
    fpmPreset: 'medium',
    pm2App: '',
    pm2Script: 'server.js',
    pm2Cwd: `${normalizeVhostsRoot(vhostsRoot)}/`,
    proxyPort: '3000',
    nodeEnv: 'production',
    spaMode: false,
    issueSSL: false,
    sslEmail: '',
    createDnsRecord: false,
    wwwRedirect: false,
  }
}

function DomainCard({
  domain,
  onDelete,
  onClick,
}: {
  domain: Domain
  onDelete: (id: string) => void
  onClick: (name: string) => void
}) {
  return (
    <Card
      className="bg-zinc-900 border-zinc-800 hover:border-zinc-700 transition-colors cursor-pointer group"
      onClick={() => onClick(domain.name)}
    >
      <CardContent className="p-5">
        <div className="flex items-start justify-between gap-3">
          <div className="flex items-center gap-3 min-w-0">
            <div className="w-9 h-9 bg-blue-600/10 border border-blue-600/20 rounded-lg flex items-center justify-center flex-shrink-0">
              <Globe className="w-4 h-4 text-blue-400" />
            </div>
            <div className="min-w-0">
              <div className="flex items-center gap-2 flex-wrap">
                <span className="text-white font-semibold text-sm truncate max-w-full group-hover:text-blue-300 transition-colors">{domain.name}</span>
                {domain.sslEnabled ? (
                  <Badge className="bg-green-500/10 text-green-400 border-green-500/20 text-xs">
                    <Shield className="w-2.5 h-2.5 mr-1" />
                    SSL
                  </Badge>
                ) : (
                  <Badge variant="outline" className="border-zinc-700 text-zinc-500 text-xs">
                    <ShieldOff className="w-2.5 h-2.5 mr-1" />
                    No SSL
                  </Badge>
                )}
                {domain.phpVersion && (
                  <Badge className="bg-purple-500/10 text-purple-400 border-purple-500/20 text-xs">
                    PHP {domain.phpVersion}
                  </Badge>
                )}
              </div>
              <div className="flex items-center gap-1.5 mt-1.5">
                <FolderOpen className="w-3 h-3 text-zinc-600 flex-shrink-0" />
                <span className="text-zinc-500 text-xs truncate font-mono">{domain.root}</span>
              </div>
            </div>
          </div>

          <DropdownMenu>
            <DropdownMenuTrigger
              aria-label={`Open actions for ${domain.name}`}
              className="inline-flex items-center justify-center text-zinc-500 hover:text-white h-8 w-8 flex-shrink-0 rounded-md hover:bg-zinc-800 transition-colors"
              onClick={(e) => e.stopPropagation()}
            >
              <MoreHorizontal className="w-4 h-4" />
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="bg-zinc-900 border-zinc-800">
              <DropdownMenuItem
                className="text-zinc-300 focus:text-white focus:bg-zinc-800 cursor-pointer"
                onClick={(e) => { e.stopPropagation(); window.open(`https://${domain.name}`, '_blank') }}
              >
                <ExternalLink className="w-3.5 h-3.5 mr-2" />
                Open site
              </DropdownMenuItem>
              <DropdownMenuItem
                className="text-red-400 focus:text-red-300 focus:bg-red-500/10 cursor-pointer"
                onClick={(e) => { e.stopPropagation(); onDelete(domain.id) }}
              >
                <Trash2 className="w-3.5 h-3.5 mr-2" />
                Delete
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>

        <div className="mt-4 pt-4 border-t border-zinc-800 flex items-center justify-between text-xs text-zinc-500">
          <span className="font-mono">{domain.type}</span>
          <div className="flex items-center gap-3">
            {domain.proxyPort && (
              <span>Port: {domain.proxyPort}</span>
            )}
            <span className="flex items-center gap-0.5 text-zinc-700 group-hover:text-zinc-500 transition-colors">
              Manage <ChevronRight className="w-3 h-3" />
            </span>
          </div>
        </div>
      </CardContent>
    </Card>
  )
}

// ── CreateDomainDialog ────────────────────────────────────────────────────────

function CreateDomainDialog({
  open,
  onOpenChange,
  onCreated,
  provisioning,
  provisioningError,
}: {
  open: boolean
  onOpenChange: (v: boolean) => void
  onCreated: (domain: string) => void
  provisioning?: DomainProvisioningCapabilities
  provisioningError: boolean
}) {
  const vhostsRoot = normalizeVhostsRoot(provisioning?.vhostsRoot)
  const [form, setForm] = useState<CreateDomainForm>(() => defaultForm(vhostsRoot))
  const [sslDnsOpen, setSslDnsOpen] = useState(false)
  const [webRootManual, setWebRootManual] = useState(false)
  const [pm2CwdManual, setPm2CwdManual] = useState(false)

  const dnsCapability = provisioning?.dns
  const dnsReady = dnsCapability?.status === 'healthy'
  const provisioningUnavailable = provisioningError || !provisioning ||
    !provisioning.vhostsRoot || !provisioning.nginxSitesAvailable || !provisioning.nginxSitesEnabled ||
    !provisioning.nginxSnippetsDir

  const set = useCallback(<K extends keyof CreateDomainForm>(key: K, value: CreateDomainForm[K]) => {
    setForm((f) => {
      const next = { ...f, [key]: value }
      // Auto-fill derivative fields whenever domain changes
      if (key === 'domain') {
        const d = String(value).trim().toLowerCase()
        if (!webRootManual) next.webRoot = autoWebRoot(d, vhostsRoot)
        if (!pm2CwdManual) next.pm2Cwd = autoCwd(d, vhostsRoot)
        next.pm2App = d ? domainToSlug(d) : ''
      }
      return next
    })
  }, [pm2CwdManual, vhostsRoot, webRootManual])

  // Fetch PHP versions
  const phpVersionsQuery = useQuery<PHPVersion[]>({
    queryKey: ['php-versions'],
    queryFn: () => api.get<PHPVersion[]>('/php/versions'),
    enabled: open,
  })
  const phpVersions = phpVersionsQuery.data ?? []
  const installedSupportedPHPVersions = phpVersions
    .map((version) => version.version)
    .filter(isSupportedPHPVersion)
  const effectivePHPVersion = isSupportedPHPVersion(form.phpVersion) && installedSupportedPHPVersions.includes(form.phpVersion)
    ? form.phpVersion
    : installedSupportedPHPVersions[0]

  // Fetch settings for default SSL email
  const settingsQuery = useQuery<{ adminEmail?: string }>({
    queryKey: ['settings-basic'],
    queryFn: () => api.get<{ adminEmail?: string }>('/settings'),
    enabled: open && !form.sslEmail,
  })
  // Derive effective sslEmail: use form value if set, else fall back to the canonical adminEmail setting
  const effectiveSslEmail = form.sslEmail || settingsQuery.data?.adminEmail || ''

  const createMutation = useMutation<{ warning?: string }>({
    mutationFn: () => {
      const domainName = form.domain.trim().toLowerCase()
      const body: DomainCreateRequest = {
        domain: domainName,
        type: form.type,
        webRoot: form.webRoot || undefined,
        wwwRedirect: form.wwwRedirect,
        issueSSL: form.issueSSL,
        sslEmail: form.issueSSL ? effectiveSslEmail : undefined,
        createDnsRecord: form.createDnsRecord && dnsReady,
      }
      if (form.type === 'php') {
        body.phpVersion = effectivePHPVersion
        body.fpmPreset = form.fpmPreset
      }
      if (form.type === 'proxy') {
        body.proxyPort = parseInt(form.proxyPort, 10) || 3000
        body.pm2_app = form.pm2App || undefined
        body.pm2_script = form.pm2Script || undefined
        body.pm2_cwd = form.pm2Cwd || undefined
        body.nodeEnv = form.nodeEnv
      }
      if (form.type === 'static') {
        body.spaMode = form.spaMode
      }
      return api.post<{ warning?: string }>('/domains', body)
    },
    onSuccess: (result) => {
      const domainName = form.domain.trim().toLowerCase()
      if (result.warning) toast.warning(result.warning)
      else toast.success(`Domain ${domainName} created successfully`)
      onOpenChange(false)
      setForm(defaultForm(vhostsRoot))
      setWebRootManual(false)
      setPm2CwdManual(false)
      onCreated(domainName)
    },
    onError: (err: Error) => toast.error(err.message || 'Failed to create domain'),
  })

  const domainValue = form.domain.trim().toLowerCase()
  const parentDomain = detectParent(domainValue)
  const isValid = domainValue.length > 0 && /^[a-z0-9][a-z0-9.-]+\.[a-z]{2,}$/.test(domainValue)

  // ── Summary items ──────────────────────────────────────────────────────────

  const summaryItems: string[] = []
  if (domainValue) {
    if (provisioning?.nginxSitesAvailable) {
      summaryItems.push(`nginx vhost → ${provisioning.nginxSitesAvailable}/${domainValue}.conf`)
    }
    if (provisioning?.nginxSnippetsDir) {
      summaryItems.push(`Managed nginx snippets → ${provisioning.nginxSnippetsDir}`)
    }
  }
  if (form.type === 'php') {
    summaryItems.push(`PHP-FPM pool (PHP ${effectivePHPVersion || 'unavailable'}, ${form.fpmPreset} preset) → /etc/php/${effectivePHPVersion || 'unknown'}/fpm/pool.d/${domainValue}.conf`)
  }
  if (form.webRoot && (form.type === 'php' || form.type === 'static')) {
    summaryItems.push(`Document root → ${form.webRoot}`)
  }
  if (form.type === 'proxy') {
    summaryItems.push(`Reverse proxy → localhost:${form.proxyPort}`)
    if (form.pm2App) summaryItems.push(`PM2 app → ${form.pm2App} (${form.pm2Script})`)
	summaryItems.push(`NODE_ENV → ${form.nodeEnv}`)
  }
  if (form.type === 'static' && form.spaMode) {
    summaryItems.push('SPA fallback → index.html')
  }
  if (form.issueSSL) summaryItems.push(`SSL certificate → Let's Encrypt`)
  if (form.createDnsRecord && dnsCapability) summaryItems.push(`DNS → Cloudflare ${dnsCapabilityDescription(dnsCapability)}`)
  if (form.wwwRedirect) summaryItems.push(`www redirect → ${domainValue}`)

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        showCloseButton
        className="bg-zinc-900 border-zinc-800 text-white max-w-3xl max-h-[90vh] overflow-y-auto p-0"
      >
        {/* Header */}
        <DialogHeader className="px-6 pt-5 pb-4 border-b border-zinc-800">
          <DialogTitle className="text-white text-lg font-semibold flex items-center gap-2">
            <Globe className="w-5 h-5 text-blue-400" />
            Add Domain
          </DialogTitle>
          <p className="text-zinc-500 text-sm mt-0.5">Configure a new nginx vhost with all necessary services.</p>
        </DialogHeader>

        <div className="px-6 py-5 space-y-8">
          {provisioningUnavailable && (
            <div className="rounded-lg border border-red-500/25 bg-red-500/[0.05] p-4 text-sm text-red-300">
              Domain provisioning capabilities are unavailable. Creation is paused to avoid using an incorrect vhost root.
            </div>
          )}

          {/* ── Section 1: Domain Name ─────────────────────────────────── */}
          <section className="space-y-3">
            <div className="flex items-center gap-2">
              <span className="w-5 h-5 rounded-full bg-blue-600/20 border border-blue-600/40 text-blue-400 text-xs flex items-center justify-center font-bold">1</span>
              <h3 className="text-white text-sm font-semibold">Domain Name</h3>
            </div>
            <div className="space-y-2">
              <Input
                placeholder="example.com or app.example.com"
                value={form.domain}
                onChange={(e) => set('domain', e.target.value)}
                className="bg-zinc-800 border-zinc-700 text-white placeholder:text-zinc-600 focus:border-blue-500 font-mono"
                autoFocus
              />
              {parentDomain && (
                <p className="text-xs text-amber-400/80 flex items-center gap-1.5">
                  <Globe className="w-3 h-3" />
                  Subdomain detected — parent domain is <strong className="font-mono">{parentDomain}</strong>
                </p>
              )}
              {domainValue && !isValid && (
                <p className="text-xs text-red-400 flex items-center gap-1.5">
                  <Circle className="w-3 h-3" />
                  Invalid domain format
                </p>
              )}
            </div>
          </section>

          {/* ── Section 2: Application Type ───────────────────────────── */}
          <section className="space-y-3">
            <div className="flex items-center gap-2">
              <span className="w-5 h-5 rounded-full bg-blue-600/20 border border-blue-600/40 text-blue-400 text-xs flex items-center justify-center font-bold">2</span>
              <h3 className="text-white text-sm font-semibold">Application Type</h3>
            </div>
            <div className="grid grid-cols-3 gap-3">
              {(
                [
                  {
                    value: 'php' as AppType,
                    icon: <FileCode className="w-6 h-6" />,
                    label: 'PHP',
                    desc: 'Laravel, WP, Frameworks',
                    color: 'text-purple-400',
                    bg: 'bg-purple-600/10 border-purple-600/20',
                    ring: 'ring-purple-500',
                  },
                  {
                    value: 'proxy' as AppType,
                    icon: <Server className="w-6 h-6" />,
                    label: 'Node.js',
                    desc: 'PM2 App, Next.js',
                    color: 'text-green-400',
                    bg: 'bg-green-600/10 border-green-600/20',
                    ring: 'ring-green-500',
                  },
                  {
                    value: 'static' as AppType,
                    icon: <Rocket className="w-6 h-6" />,
                    label: 'Static',
                    desc: 'HTML/SPA, No backend',
                    color: 'text-orange-400',
                    bg: 'bg-orange-600/10 border-orange-600/20',
                    ring: 'ring-orange-500',
                  },
                ] as const
              ).map((opt) => (
                <button
                  key={opt.value}
                  type="button"
                  onClick={() => set('type', opt.value)}
                  className={[
                    'relative flex flex-col items-center gap-2 rounded-xl border-2 p-4 transition-all cursor-pointer',
                    form.type === opt.value
                      ? `border-transparent ring-2 ${opt.ring} bg-zinc-800`
                      : 'border-zinc-700 bg-zinc-800/50 hover:border-zinc-600 hover:bg-zinc-800',
                  ].join(' ')}
                >
                  <div className={`w-12 h-12 rounded-xl border flex items-center justify-center ${opt.bg} ${opt.color}`}>
                    {opt.icon}
                  </div>
                  <div className="text-center">
                    <p className="text-white font-semibold text-sm">{opt.label}</p>
                    <p className="text-zinc-500 text-xs mt-0.5">{opt.desc}</p>
                  </div>
                  {form.type === opt.value && (
                    <CheckCircle2 className={`absolute top-2 right-2 w-4 h-4 ${opt.color}`} />
                  )}
                </button>
              ))}
            </div>
          </section>

          {/* ── Section 3: Type-specific settings ─────────────────────── */}
          <section className="space-y-3">
            <div className="flex items-center gap-2">
              <span className="w-5 h-5 rounded-full bg-blue-600/20 border border-blue-600/40 text-blue-400 text-xs flex items-center justify-center font-bold">3</span>
              <h3 className="text-white text-sm font-semibold">
                {form.type === 'php' ? 'PHP Settings' : form.type === 'proxy' ? 'Node.js Settings' : 'Static Settings'}
              </h3>
            </div>

            {/* PHP ─────────────────────────────────────────────────────── */}
            {form.type === 'php' && (
              <div className="space-y-4 bg-zinc-800/40 border border-zinc-700/50 rounded-xl p-4">
                {/* PHP Version buttons */}
                <div className="space-y-2">
                  <Label className="text-zinc-300 text-xs uppercase tracking-wider font-semibold">PHP Version</Label>
                  <div className="flex flex-wrap gap-2">
                    {phpVersionsQuery.isLoading ? (
                      <span className="text-xs text-zinc-500">Detecting installed PHP versions…</span>
                    ) : phpVersionsQuery.isError ? (
                      <div className="w-full rounded-lg border border-red-500/20 bg-red-500/[0.05] p-3 text-center">
                        <p className="text-xs text-red-300">PHP versions could not be loaded.</p>
                        <p className="mt-1 text-[11px] text-zinc-600">{phpVersionsQuery.error.message}</p>
                        <Button type="button" variant="outline" size="sm" className="mt-3 border-red-500/30 text-red-200" onClick={() => { void phpVersionsQuery.refetch() }} disabled={phpVersionsQuery.isFetching}>
                          <RefreshCw className={`mr-2 size-3.5 ${phpVersionsQuery.isFetching ? 'animate-spin' : ''}`} />Retry
                        </Button>
                      </div>
                    ) : phpVersions.length === 0 ? (
                      <span className="text-xs text-amber-300">No installed PHP versions were detected.</span>
                    ) : phpVersions.map(({ version }) => (
                      <button
                        key={version}
                        type="button"
                        onClick={() => set('phpVersion', version)}
                        className={[
                          'px-3 py-1.5 rounded-lg text-sm font-mono font-medium transition-all border',
                          effectivePHPVersion === version
                            ? 'bg-purple-600 border-purple-500 text-white'
                            : 'bg-zinc-700/50 border-zinc-600 text-zinc-300 hover:border-zinc-500 hover:text-white',
                        ].join(' ')}
                      >
                        PHP {version}
                      </button>
                    ))}
                  </div>
                </div>

                {/* Document Root */}
                <div className="space-y-1.5">
                  <Label className="text-zinc-300 text-xs uppercase tracking-wider font-semibold">Document Root</Label>
                  <Input
                    value={form.webRoot}
                    onChange={(e) => {
                      setWebRootManual(true)
                      set('webRoot', e.target.value)
                    }}
                    className="bg-zinc-700/50 border-zinc-600 text-white placeholder:text-zinc-600 focus:border-blue-500 font-mono text-xs"
                  />
                </div>

                {/* FPM Preset */}
                <div className="space-y-2">
                  <Label className="text-zinc-300 text-xs uppercase tracking-wider font-semibold">FPM Pool Preset</Label>
                  <div className="grid grid-cols-3 gap-2">
                    {(
                      [
                        { value: 'low' as FpmPreset, label: 'Low', desc: '5 workers, 128MB', color: 'text-zinc-400' },
                        { value: 'medium' as FpmPreset, label: 'Medium', desc: '10 workers, 256MB', color: 'text-blue-400' },
                        { value: 'high' as FpmPreset, label: 'High', desc: '30 workers, 512MB', color: 'text-purple-400' },
                      ] as const
                    ).map((p) => (
                      <button
                        key={p.value}
                        type="button"
                        onClick={() => set('fpmPreset', p.value)}
                        className={[
                          'flex flex-col items-center py-2 px-3 rounded-lg border text-center transition-all',
                          form.fpmPreset === p.value
                            ? 'border-blue-500 bg-blue-600/10 text-white'
                            : 'border-zinc-600 bg-zinc-700/30 text-zinc-400 hover:border-zinc-500',
                        ].join(' ')}
                      >
                        <span className={`text-sm font-semibold ${p.color}`}>{p.label}</span>
                        <span className="text-xs text-zinc-500 mt-0.5">{p.desc}</span>
                      </button>
                    ))}
                  </div>
                </div>
              </div>
            )}

            {/* Node.js / Proxy ─────────────────────────────────────────── */}
            {form.type === 'proxy' && (
              <div className="space-y-4 bg-zinc-800/40 border border-zinc-700/50 rounded-xl p-4">
                <div className="grid grid-cols-2 gap-3">
                  {/* App Name */}
                  <div className="space-y-1.5">
                    <Label className="text-zinc-300 text-xs uppercase tracking-wider font-semibold">Application Name</Label>
                    <Input
                      placeholder="my-app"
                      value={form.pm2App}
                      onChange={(e) => set('pm2App', e.target.value)}
                      className="bg-zinc-700/50 border-zinc-600 text-white placeholder:text-zinc-600 focus:border-blue-500 font-mono text-sm"
                    />
                  </div>
                  {/* Port */}
                  <div className="space-y-1.5">
                    <Label className="text-zinc-300 text-xs uppercase tracking-wider font-semibold">Port</Label>
                    <Input
                      type="number"
                      placeholder="3000"
                      value={form.proxyPort}
                      onChange={(e) => set('proxyPort', e.target.value)}
                      min={1}
                      max={65535}
                      className="bg-zinc-700/50 border-zinc-600 text-white placeholder:text-zinc-600 focus:border-blue-500 font-mono text-sm"
                    />
                  </div>
                </div>

                {/* Script Path */}
                <div className="space-y-1.5">
                  <Label className="text-zinc-300 text-xs uppercase tracking-wider font-semibold">Script Path</Label>
                  <Input
                    placeholder="server.js"
                    value={form.pm2Script}
                    onChange={(e) => set('pm2Script', e.target.value)}
                    className="bg-zinc-700/50 border-zinc-600 text-white placeholder:text-zinc-600 focus:border-blue-500 font-mono text-sm"
                  />
                </div>

                {/* Working Directory */}
                <div className="space-y-1.5">
                  <Label className="text-zinc-300 text-xs uppercase tracking-wider font-semibold">Working Directory</Label>
                  <Input
                    value={form.pm2Cwd}
                    onChange={(e) => {
                      setPm2CwdManual(true)
                      set('pm2Cwd', e.target.value)
                    }}
                    className="bg-zinc-700/50 border-zinc-600 text-white placeholder:text-zinc-600 focus:border-blue-500 font-mono text-xs"
                  />
                </div>

                {/* Environment */}
                <div className="space-y-2">
                  <Label className="text-zinc-300 text-xs uppercase tracking-wider font-semibold">Environment</Label>
                  <div className="flex gap-2">
                    {(['production', 'development'] as const).map((env) => (
                      <button
                        key={env}
                        type="button"
                        onClick={() => set('nodeEnv', env)}
                        className={[
                          'px-3 py-1.5 rounded-lg text-sm font-medium border transition-all',
                          form.nodeEnv === env
                            ? 'bg-green-600 border-green-500 text-white'
                            : 'bg-zinc-700/50 border-zinc-600 text-zinc-300 hover:border-zinc-500',
                        ].join(' ')}
                      >
                        {env}
                      </button>
                    ))}
                  </div>
                </div>
              </div>
            )}

            {/* Static ──────────────────────────────────────────────────── */}
            {form.type === 'static' && (
              <div className="space-y-4 bg-zinc-800/40 border border-zinc-700/50 rounded-xl p-4">
                <div className="space-y-1.5">
                  <Label className="text-zinc-300 text-xs uppercase tracking-wider font-semibold">Document Root</Label>
                  <Input
                    value={form.webRoot}
                    onChange={(e) => set('webRoot', e.target.value)}
                    className="bg-zinc-700/50 border-zinc-600 text-white placeholder:text-zinc-600 focus:border-blue-500 font-mono text-xs"
                  />
                </div>
                <div className="flex items-center justify-between">
                  <div>
                    <p className="text-zinc-200 text-sm font-medium">SPA Mode</p>
                    <p className="text-zinc-500 text-xs mt-0.5">All routes → index.html (for React/Vue/etc.)</p>
                  </div>
                  <button
                    type="button"
                    aria-label="Enable SPA mode"
                    aria-pressed={form.spaMode}
                    onClick={() => set('spaMode', !form.spaMode)}
                    className={[
                      'relative inline-flex h-5 w-9 items-center rounded-full transition-colors focus:outline-none',
                      form.spaMode ? 'bg-blue-600' : 'bg-zinc-600',
                    ].join(' ')}
                  >
                    <span
                      className={[
                        'inline-block h-3.5 w-3.5 transform rounded-full bg-white shadow transition-transform',
                        form.spaMode ? 'translate-x-4.5' : 'translate-x-0.5',
                      ].join(' ')}
                    />
                  </button>
                </div>
              </div>
            )}
          </section>

          {/* ── Section 4: SSL & DNS (collapsible) ────────────────────── */}
          <section>
            <button
              type="button"
              onClick={() => setSslDnsOpen((v) => !v)}
              className="w-full flex items-center justify-between text-left"
            >
              <div className="flex items-center gap-2">
                <span className="w-5 h-5 rounded-full bg-blue-600/20 border border-blue-600/40 text-blue-400 text-xs flex items-center justify-center font-bold">4</span>
                <h3 className="text-white text-sm font-semibold">SSL & DNS</h3>
                {(form.issueSSL || form.createDnsRecord || form.wwwRedirect) && (
                  <Badge className="bg-blue-600/20 text-blue-400 border-blue-600/30 text-xs">
                    {[form.issueSSL, form.createDnsRecord, form.wwwRedirect].filter(Boolean).length} enabled
                  </Badge>
                )}
              </div>
              {sslDnsOpen ? (
                <ChevronUp className="w-4 h-4 text-zinc-500" />
              ) : (
                <ChevronDown className="w-4 h-4 text-zinc-500" />
              )}
            </button>

            {sslDnsOpen && (
              <div className="mt-3 space-y-4 bg-zinc-800/40 border border-zinc-700/50 rounded-xl p-4">
                {/* SSL */}
                <div className="space-y-3">
                  <label className="flex items-center gap-3 cursor-pointer">
                    <input
                      type="checkbox"
                      checked={form.issueSSL}
                      onChange={(e) => set('issueSSL', e.target.checked)}
                      className="w-4 h-4 rounded border-zinc-600 bg-zinc-800 accent-blue-500"
                    />
                    <div>
                      <p className="text-zinc-200 text-sm font-medium flex items-center gap-1.5">
                        <Lock className="w-3.5 h-3.5 text-green-400" />
                        Issue SSL Certificate (Let's Encrypt)
                      </p>
                    </div>
                  </label>
                  {form.issueSSL && (
                    <div className="ml-7 space-y-1.5">
                      <Label className="text-zinc-400 text-xs">Contact Email</Label>
                      <Input
                        type="email"
                        placeholder="admin@example.com"
                        value={effectiveSslEmail}
                        onChange={(e) => set('sslEmail', e.target.value)}
                        className="bg-zinc-700/50 border-zinc-600 text-white placeholder:text-zinc-600 focus:border-blue-500 text-sm"
                      />
                      {settingsQuery.isError && !form.sslEmail && (
                        <p className="text-[11px] text-amber-300">Default admin email is unavailable; enter a contact email manually.</p>
                      )}
                    </div>
                  )}
                </div>

                <div className="h-px bg-zinc-700/50" />

                {/* DNS */}
                <label className={`flex items-center gap-3 ${dnsReady ? 'cursor-pointer' : 'cursor-not-allowed opacity-70'}`}>
                  <input
                    type="checkbox"
                    checked={form.createDnsRecord}
                    onChange={(e) => set('createDnsRecord', e.target.checked)}
                    disabled={!dnsReady}
                    className="w-4 h-4 rounded border-zinc-600 bg-zinc-800 accent-blue-500"
                  />
                  <div>
                    <p className="text-zinc-200 text-sm font-medium">Create Cloudflare DNS Record</p>
                    <p className="text-zinc-500 text-xs mt-0.5">
                      {dnsCapability
                        ? dnsCapabilityDescription(dnsCapability)
                        : provisioningError
                          ? 'Unavailable — capability check failed.'
                          : 'Checking provider configuration…'}
                    </p>
                  </div>
                </label>

                <div className="h-px bg-zinc-700/50" />

                {/* www redirect */}
                <label className="flex items-center gap-3 cursor-pointer">
                  <input
                    type="checkbox"
                    checked={form.wwwRedirect}
                    onChange={(e) => set('wwwRedirect', e.target.checked)}
                    className="w-4 h-4 rounded border-zinc-600 bg-zinc-800 accent-blue-500"
                  />
                  <div>
                    <p className="text-zinc-200 text-sm font-medium">Redirect www → non-www</p>
                    <p className="text-zinc-500 text-xs mt-0.5">
                      www.{domainValue || 'example.com'} → {domainValue || 'example.com'}
                    </p>
                  </div>
                </label>
              </div>
            )}
          </section>

          {/* ── Section 5: Summary ────────────────────────────────────── */}
          {isValid && summaryItems.length > 0 && (
            <section className="space-y-3">
              <div className="flex items-center gap-2">
                <span className="w-5 h-5 rounded-full bg-blue-600/20 border border-blue-600/40 text-blue-400 text-xs flex items-center justify-center font-bold">5</span>
                <h3 className="text-white text-sm font-semibold">What will be created</h3>
              </div>
              <div className="bg-zinc-800/40 border border-zinc-700/50 rounded-xl p-4 space-y-2">
                {summaryItems.map((item, i) => (
                  <div key={i} className="flex items-start gap-2 text-xs">
                    <CheckCircle2 className="w-3.5 h-3.5 text-green-400 mt-0.5 flex-shrink-0" />
                    <span className="text-zinc-300 font-mono">{item}</span>
                  </div>
                ))}
              </div>
            </section>
          )}

        </div>

        {/* Footer */}
        <div className="-mx-0 border-t border-zinc-800 bg-zinc-950/50 px-6 py-4 flex items-center justify-between rounded-b-xl">
          <button
            type="button"
            onClick={() => onOpenChange(false)}
            className="text-zinc-400 hover:text-white text-sm transition-colors"
          >
            Cancel
          </button>
          <Button
            onClick={() => createMutation.mutate()}
            disabled={
              !isValid ||
              createMutation.isPending ||
              provisioningUnavailable ||
              (form.type === 'php' && (phpVersionsQuery.isLoading || phpVersionsQuery.isError || !effectivePHPVersion)) ||
              (form.issueSSL && !effectiveSslEmail)
            }
            className="bg-blue-600 hover:bg-blue-500 text-white disabled:opacity-50"
          >
            {createMutation.isPending ? (
              <Loader2 className="w-4 h-4 mr-2 animate-spin" />
            ) : (
              <Plus className="w-4 h-4 mr-2" />
            )}
            Create Domain
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  )
}

// ── Main Domains page ─────────────────────────────────────────────────────────

export default function Domains() {
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const [search, setSearch] = useState('')
  const [showCreate, setShowCreate] = useState(false)

  const domainsQuery = useQuery<{ domains: Domain[] }>({
    queryKey: ['domains'],
    queryFn: () => api.get<{ domains: Domain[] }>('/domains'),
  })

  const provisioningQuery = useQuery<DomainProvisioningCapabilities>({
    queryKey: ['domain-provisioning'],
    queryFn: () => api.get<DomainProvisioningCapabilities>('/domains/provisioning'),
    staleTime: 30_000,
  })

  const domains = domainsQuery.data?.domains ?? []
  const provisioning = provisioningQuery.data
  const provisioningPathsReady = Boolean(
    provisioning?.vhostsRoot &&
    provisioning?.nginxSitesAvailable &&
    provisioning?.nginxSitesEnabled &&
    provisioning?.nginxSnippetsDir,
  )

  const deleteMutation = useMutation({
    mutationFn: (id: string) => api.delete(`/domains/${id}`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['domains'] })
      toast.success('Domain removed successfully')
    },
    onError: (error: Error) => toast.error(error.message || 'Failed to remove domain'),
  })

  const filtered = domains.filter((d) =>
    d.name.toLowerCase().includes(search.toLowerCase()),
  )

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between gap-4 flex-wrap">
        <div>
          <h2 className="text-white text-xl font-bold">Domains</h2>
          <p className="text-zinc-500 text-sm mt-0.5">
            {domainsQuery.isError ? 'Domain inventory unavailable' : `${domains.length} domains configured`}
          </p>
        </div>
        <Button
          className="bg-blue-600 hover:bg-blue-500 text-white"
          onClick={() => setShowCreate(true)}
          disabled={domainsQuery.isLoading || domainsQuery.isError || provisioningQuery.isLoading || provisioningQuery.isError || !provisioningPathsReady}
        >
          <Plus className="w-4 h-4 mr-2" />
          Add Domain
        </Button>
      </div>

      {provisioningQuery.isError && (
        <Card className="border-red-500/25 bg-red-500/[0.05]">
          <CardContent className="p-5 text-center">
            <AlertTriangle className="mx-auto size-5 text-red-400" />
            <p className="mt-2 text-sm text-red-300">Domain provisioning capabilities could not be loaded. Creation is paused.</p>
            <p className="mt-1 text-xs text-zinc-600">{provisioningQuery.error.message}</p>
            <Button type="button" variant="outline" size="sm" className="mt-4 border-red-500/30 text-red-200" onClick={() => { void provisioningQuery.refetch() }} disabled={provisioningQuery.isFetching}>
              <RefreshCw className={`mr-2 size-3.5 ${provisioningQuery.isFetching ? 'animate-spin' : ''}`} />Retry
            </Button>
          </CardContent>
        </Card>
      )}

      {provisioning && !provisioningPathsReady && (
        <Card className="border-amber-500/25 bg-amber-500/[0.05]">
          <CardContent className="p-5 text-center">
            <AlertTriangle className="mx-auto size-5 text-amber-400" />
            <p className="mt-2 text-sm text-amber-200">Domain creation is paused because installation paths are not configured.</p>
            <p className="mt-1 text-xs text-zinc-500">Set absolute HSERVER_VHOSTS_ROOT, HSERVER_NGINX_SITES_AVAILABLE, HSERVER_NGINX_SITES_ENABLED, and HSERVER_NGINX_SNIPPETS_DIR values, then restart Heyserver.</p>
          </CardContent>
        </Card>
      )}

      {/* Search */}
      <div className="relative">
        <Globe className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-zinc-500" />
        <input
          type="text"
          placeholder="Search domains..."
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          className="w-full bg-zinc-900 border border-zinc-800 text-white placeholder:text-zinc-600 rounded-lg py-2 pl-9 pr-4 text-sm focus:outline-none focus:border-blue-500 transition-colors"
        />
      </div>

      {/* Domain grid */}
      {domainsQuery.isLoading ? (
        <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
          {Array.from({ length: 6 }).map((_, i) => (
            <Skeleton key={i} className="h-32 w-full bg-zinc-900" />
          ))}
        </div>
      ) : domainsQuery.isError ? (
        <Card className="border-red-500/25 bg-red-500/[0.05]">
          <CardContent className="p-8 text-center">
            <AlertTriangle className="mx-auto size-5 text-red-400" />
            <p className="mt-2 text-sm text-red-300">Domains could not be loaded. Mutating controls are paused.</p>
            <p className="mt-1 text-xs text-zinc-600">{domainsQuery.error.message}</p>
            <Button type="button" variant="outline" size="sm" className="mt-4 border-red-500/30 text-red-200" onClick={() => { void domainsQuery.refetch() }} disabled={domainsQuery.isFetching}>
              <RefreshCw className={`mr-2 size-3.5 ${domainsQuery.isFetching ? 'animate-spin' : ''}`} />Retry
            </Button>
          </CardContent>
        </Card>
      ) : filtered.length > 0 ? (
        <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
          {filtered.map((domain) => (
            <DomainCard
              key={domain.id}
              domain={domain}
              onDelete={(id) => {
                const target = domains.find((item) => item.id === id)
                const label = target?.name ?? id
                if (window.confirm(`Remove ${label} from Heyserver? Site files will be kept.`)) {
                  deleteMutation.mutate(id)
                }
              }}
              onClick={(name) => navigate(`/domains/${name}`)}
            />
          ))}
        </div>
      ) : (
        <EmptyState
          icon={Globe}
          title={search ? 'No domains match your search' : 'No domains configured yet'}
          description={
            search
              ? 'Try a different search term.'
              : 'Add your first nginx vhost to get started.'
          }
          actionLabel={search || !provisioningPathsReady ? undefined : 'Add Domain'}
          onAction={search || !provisioningPathsReady ? undefined : () => setShowCreate(true)}
        />
      )}

      {/* Create Domain Dialog */}
      <CreateDomainDialog
        key={`${provisioning?.vhostsRoot ?? 'default'}:${provisioning?.nginxSitesAvailable ?? 'nginx-unavailable'}:${provisioning?.nginxSnippetsDir ?? 'snippets-unavailable'}:${provisioning?.dns.status ?? 'loading'}:${provisioning?.dns.origin ?? ''}:${provisioning?.dns.proxied ?? false}`}
        open={showCreate}
        onOpenChange={setShowCreate}
        provisioning={provisioning}
        provisioningError={provisioningQuery.isError}
        onCreated={(name) => {
          queryClient.invalidateQueries({ queryKey: ['domains'] })
          navigate(`/domains/${name}`)
        }}
      />

      {deleteMutation.isPending && (
        <div className="fixed bottom-4 right-4 bg-zinc-900 border border-zinc-800 rounded-lg px-4 py-2 flex items-center gap-2 text-sm text-zinc-300">
          <Loader2 className="w-3.5 h-3.5 animate-spin" />
          Removing domain...
        </div>
      )}
    </div>
  )
}
