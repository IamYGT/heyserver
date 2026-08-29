import { useMemo, useState, type ReactNode } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link, useLocation } from 'react-router-dom'
import {
  AlertTriangle,
  ArrowRight,
  Bell,
  Cloud,
  Container,
  Database,
  FileCode,
  FileText,
  HardDrive,
  KeyRound,
  Layers3,
  Mail,
  Network,
  RefreshCw,
  Search,
  Server,
  Settings2,
  Shield,
  ShieldCheck,
  X,
  type LucideIcon,
} from 'lucide-react'
import { api } from '@/lib/api'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { normalizeIntegrationState, type IntegrationState } from '@/lib/integrationState'
import {
  isLocalServer,
  managedNodePath,
  managedServerForLocation,
  type ManagedServerID,
} from '@/lib/serverNavigation'

type Requirement = 'optional' | 'feature_specific'
type Target = 'local_host' | 'managed_node'

interface IntegrationConfiguration {
  non_secret_keys: string[]
  secret_key_names: string[]
  secret_file_refs: string[]
  boundary: string
}

interface IntegrationStatus {
  canonical_states: string[]
  raw_state_mappings: Array<{
    raw: string
    canonical: string
    meaning: string
  }>
  api_route_prefixes: string[]
}

interface IntegrationEvidenceItem {
  path: string
  claim: string
}

interface IntegrationEntry {
  id: string
  display_name: string
  purpose: string
  requirement: Requirement
  classes: string[]
  targets: Target[]
  configuration: IntegrationConfiguration
  status: IntegrationStatus
  evidence: {
    web: IntegrationEvidenceItem[]
    docs: IntegrationEvidenceItem[]
    tests: IntegrationEvidenceItem[]
  }
}

interface IntegrationCatalog {
  schema_version: 1
  entries: IntegrationEntry[]
}

interface IntegrationStatusResult {
  id: string
  state: IntegrationState
  observed_at?: string
}

interface IntegrationStatusTarget {
  scope: 'local_host' | 'managed_node'
  node_id?: string
}

interface IntegrationStatusResponse {
  schema_version: 1
  observed_at?: string
  target: IntegrationStatusTarget
  results: IntegrationStatusResult[]
  unprobed: string[]
  partial: boolean
}

const MANAGED_STATUS_INTEGRATIONS = new Set(['process.pm2', 'container.docker'])

const INTEGRATION_ROUTE_ALLOWLIST: Record<string, string> = {
  'cloudflare.dns': '/cloudflare',
  'stalwart.mail': '/mail',
  'mail.access': '/webmail',
  'backup.gdrive': '/backups',
  'backup.snapshot.restic': '/backups',
  'process.pm2': '/pm2',
  'web.nginx': '/nginx',
  'runtime.php_fpm': '/php',
  'firewall.ufw': '/firewall',
  'tls.certbot': '/ssl',
  'dns.bind9': '/dns',
  'database.local': '/databases',
  'container.docker': '/docker',
  'storage.smartmontools': '/disk',
  'notification.delivery': '/notifications',
}

const ICON_BY_INTEGRATION: Record<string, LucideIcon> = {
  'cloudflare.dns': Cloud,
  'stalwart.mail': Mail,
  'mail.access': Network,
  'backup.gdrive': Cloud,
  'backup.snapshot.restic': HardDrive,
  'process.pm2': Settings2,
  'web.nginx': FileText,
  'runtime.php_fpm': FileCode,
  'firewall.ufw': Shield,
  'tls.certbot': ShieldCheck,
  'dns.bind9': Network,
  'database.local': Database,
  'container.docker': Container,
  'storage.smartmontools': HardDrive,
  'notification.delivery': Bell,
}

const LABELS: Record<string, string> = {
  local_capability: 'Local capability',
  managed_node_capability: 'Managed-node capability',
  provider_adapter: 'Provider adapter',
  client_surface: 'Client surface',
  local_host: 'Local host',
  managed_node: 'Managed node',
}

const STATUS_LABELS: Record<string, string> = {
  not_configured: 'not_configured',
  unavailable: 'unavailable',
  healthy: 'healthy',
}

const CLASS_TONE: Record<string, string> = {
  local_capability: 'border-blue-500/20 bg-blue-500/10 text-blue-300',
  managed_node_capability: 'border-cyan-500/20 bg-cyan-500/10 text-cyan-300',
  provider_adapter: 'border-violet-500/20 bg-violet-500/10 text-violet-300',
  client_surface: 'border-zinc-700 bg-zinc-800/80 text-zinc-300',
}

const TARGET_TONE: Record<string, string> = {
  local_host: 'border-emerald-500/20 bg-emerald-500/10 text-emerald-300',
  managed_node: 'border-amber-500/20 bg-amber-500/10 text-amber-300',
}

const REQUIREMENT_TONE: Record<Requirement, string> = {
  optional: 'border-blue-500/25 bg-blue-500/10 text-blue-300',
  feature_specific: 'border-orange-500/25 bg-orange-500/10 text-orange-300',
}

function asStringArray(value: unknown): string[] {
  return Array.isArray(value) ? value.filter((item): item is string => typeof item === 'string') : []
}

function asEvidence(value: unknown): IntegrationEvidenceItem[] {
  if (!Array.isArray(value)) return []
  return value.flatMap((item) => {
    if (!item || typeof item !== 'object') return []
    const candidate = item as { path?: unknown; claim?: unknown }
    return typeof candidate.path === 'string' && typeof candidate.claim === 'string'
      ? [{ path: candidate.path, claim: candidate.claim }]
      : []
  })
}

function normalizeEntry(value: unknown): IntegrationEntry | null {
  if (!value || typeof value !== 'object') return null
  const candidate = value as Record<string, unknown>
  if (
    typeof candidate.id !== 'string'
    || typeof candidate.display_name !== 'string'
    || typeof candidate.purpose !== 'string'
  ) return null

  const configuration = candidate.configuration && typeof candidate.configuration === 'object'
    ? candidate.configuration as Record<string, unknown>
    : {}
  const status = candidate.status && typeof candidate.status === 'object'
    ? candidate.status as Record<string, unknown>
    : {}
  const evidence = candidate.evidence && typeof candidate.evidence === 'object'
    ? candidate.evidence as Record<string, unknown>
    : {}
  const requirement = candidate.requirement === 'feature_specific' ? 'feature_specific' : 'optional'
  const targets = asStringArray(candidate.targets).filter(
    (target): target is Target => target === 'local_host' || target === 'managed_node',
  )

  return {
    id: candidate.id,
    display_name: candidate.display_name,
    purpose: candidate.purpose,
    requirement,
    classes: asStringArray(candidate.classes),
    targets,
    configuration: {
      non_secret_keys: asStringArray(configuration.non_secret_keys),
      secret_key_names: asStringArray(configuration.secret_key_names),
      secret_file_refs: asStringArray(configuration.secret_file_refs),
      boundary: typeof configuration.boundary === 'string'
        ? configuration.boundary
        : 'Configuration remains installation-owned and is not returned as a secret value.',
    },
    status: {
      canonical_states: asStringArray(status.canonical_states),
      raw_state_mappings: Array.isArray(status.raw_state_mappings)
        ? status.raw_state_mappings.flatMap((mapping) => {
            if (!mapping || typeof mapping !== 'object') return []
            const item = mapping as Record<string, unknown>
            return typeof item.raw === 'string'
              && typeof item.canonical === 'string'
              && typeof item.meaning === 'string'
              ? [{ raw: item.raw, canonical: item.canonical, meaning: item.meaning }]
              : []
          })
        : [],
      api_route_prefixes: asStringArray(status.api_route_prefixes),
    },
    evidence: {
      web: asEvidence(evidence.web),
      docs: asEvidence(evidence.docs),
      tests: asEvidence(evidence.tests),
    },
  }
}

function parseCatalog(payload: unknown): IntegrationCatalog {
  if (!payload || typeof payload !== 'object') {
    throw new Error('Invalid integration catalog')
  }
  const candidate = payload as { schema_version?: unknown; entries?: unknown }
  if (candidate.schema_version !== 1 || !Array.isArray(candidate.entries)) {
    throw new Error('Invalid integration catalog schema')
  }

  return {
    schema_version: 1,
    entries: candidate.entries.flatMap((entry) => {
      const normalized = normalizeEntry(entry)
      return normalized ? [normalized] : []
    }),
  }
}

async function fetchIntegrationCatalog(): Promise<IntegrationCatalog> {
  return parseCatalog(await api.get<unknown>('/integrations/catalog'))
}

function safeObservedAt(value: unknown): string | undefined {
  if (typeof value !== 'string' || value.length > 128 || Number.isNaN(Date.parse(value))) return undefined
  return value
}

function parseIntegrationStatus(payload: unknown, expectedServer: ManagedServerID): IntegrationStatusResponse {
  if (!payload || typeof payload !== 'object') {
    throw new Error('Invalid integration status')
  }

  const candidate = payload as Record<string, unknown>
  if (candidate.schema_version !== 1 || !Array.isArray(candidate.results)) {
    throw new Error('Invalid integration status schema')
  }

  const managedTarget = !isLocalServer(expectedServer)
  const rawTarget = candidate.target
  if (rawTarget !== undefined && (!rawTarget || typeof rawTarget !== 'object')) {
    throw new Error('Invalid integration status target')
  }
  const target = rawTarget as { scope?: unknown; node_id?: unknown } | undefined
  if (managedTarget) {
    if (target?.scope !== 'managed_node' || target.node_id !== expectedServer) {
      throw new Error('Invalid managed integration status target')
    }
  } else if (target && target.scope !== 'local_host') {
    throw new Error('Invalid local integration status target')
  }

  const results = candidate.results.flatMap((result) => {
    if (!result || typeof result !== 'object') {
      throw new Error('Invalid integration status result')
    }
    const item = result as Record<string, unknown>
    // The managed endpoint has a deliberately narrow contract. Ignore any
    // future/unknown provider rows rather than joining them to local catalog
    // entries; the two required managed rows are validated below.
    if (managedTarget && (typeof item.id !== 'string' || !MANAGED_STATUS_INTEGRATIONS.has(item.id))) return []
    const state = normalizeIntegrationState(item.state)
    if (typeof item.id !== 'string' || state === null) {
      throw new Error('Invalid integration status result')
    }
    const observedAt = safeObservedAt(item.observed_at)
    return [{
      id: item.id,
      state,
      ...(observedAt ? { observed_at: observedAt } : {}),
    }]
  })

  if (managedTarget) {
    const resultIDs = new Set(results.map((result) => result.id))
    if (
      results.length !== MANAGED_STATUS_INTEGRATIONS.size
      || resultIDs.size !== MANAGED_STATUS_INTEGRATIONS.size
      || Array.from(MANAGED_STATUS_INTEGRATIONS).some((id) => !resultIDs.has(id))
    ) {
      throw new Error('Invalid managed integration status results')
    }
  }

  const observedAt = safeObservedAt(candidate.observed_at)
  const unprobed = Array.isArray(candidate.unprobed)
    ? candidate.unprobed.filter((id): id is string => typeof id === 'string')
    : []

  return {
    schema_version: 1,
    ...(observedAt ? { observed_at: observedAt } : {}),
    target: managedTarget
      ? { scope: 'managed_node', node_id: expectedServer }
      : { scope: 'local_host' },
    results,
    unprobed,
    partial: candidate.partial === true,
  }
}

async function fetchIntegrationStatus(server: ManagedServerID): Promise<IntegrationStatusResponse> {
  const endpoint = isLocalServer(server)
    ? '/integrations/status'
    : managedNodePath(server, '/integrations/status')
  return parseIntegrationStatus(await api.get<unknown>(endpoint), server)
}

function labelFor(value: string): string {
  return LABELS[value] ?? value.replace(/[_-]+/g, ' ').replace(/\b\w/g, (letter) => letter.toUpperCase())
}

function searchableText(entry: IntegrationEntry): string {
  return [
    entry.id,
    entry.display_name,
    entry.purpose,
    entry.requirement,
    ...entry.classes,
    ...entry.targets,
    ...entry.configuration.non_secret_keys,
    ...entry.configuration.secret_key_names,
    ...entry.configuration.secret_file_refs,
    entry.configuration.boundary,
    ...entry.evidence.docs.map((item) => `${item.path} ${item.claim}`),
  ].join(' ').toLowerCase()
}

function IntegrationIcon({ entry }: { entry: IntegrationEntry }) {
  const Icon = ICON_BY_INTEGRATION[entry.id] ?? Layers3
  return (
    <span className="grid size-11 shrink-0 place-items-center rounded-xl border border-blue-500/20 bg-blue-500/10 text-blue-300">
      <Icon className="size-5" aria-hidden="true" />
    </span>
  )
}

function TagList({ values, tone }: { values: string[]; tone: Record<string, string> }) {
  if (values.length === 0) return <span className="text-xs text-zinc-600">Not declared</span>
  return (
    <div className="flex flex-wrap gap-1.5">
      {values.map((value) => (
        <span key={value} className={cn('rounded-md border px-2 py-1 text-[10px] font-medium', tone[value] ?? 'border-zinc-700 bg-zinc-800 text-zinc-300')}>
          {labelFor(value)}
        </span>
      ))}
    </div>
  )
}

function ConfigurationDetails({ entry }: { entry: IntegrationEntry }) {
  const { configuration } = entry
  const secretCount = configuration.secret_key_names.length
  const nonSecretCount = configuration.non_secret_keys.length
  const fileRefCount = configuration.secret_file_refs.length

  return (
    <details className="group rounded-lg border border-zinc-800 bg-zinc-950/50">
      <summary className="flex min-h-11 cursor-pointer list-none items-center justify-between gap-3 px-3 py-2.5 text-xs text-zinc-300 outline-none transition-colors motion-reduce:transition-none hover:bg-zinc-800/50 focus-visible:ring-2 focus-visible:ring-blue-500/70 [&::-webkit-details-marker]:hidden">
        <span className="flex min-w-0 items-center gap-2">
          <KeyRound className="size-3.5 shrink-0 text-zinc-500" aria-hidden="true" />
          <span className="truncate">Configuration surface</span>
          <span className="text-[10px] text-zinc-600">
            {nonSecretCount} non-secret · {secretCount} secret name{secretCount === 1 ? '' : 's'}
          </span>
        </span>
        <ArrowRight className="size-3.5 shrink-0 text-zinc-600 transition-transform motion-reduce:transition-none group-open:rotate-90" aria-hidden="true" />
      </summary>
      <div className="space-y-4 border-t border-zinc-800 px-3 py-3">
        <div>
          <p className="mb-1.5 text-[10px] font-semibold uppercase tracking-wider text-zinc-500">Non-secret keys</p>
          {configuration.non_secret_keys.length > 0 ? (
            <div className="flex flex-wrap gap-1.5">
              {configuration.non_secret_keys.map((key) => (
                <code key={key} className="rounded bg-zinc-800 px-1.5 py-1 text-[10px] text-zinc-300">{key}</code>
              ))}
            </div>
          ) : <p className="text-xs text-zinc-600">No non-secret keys declared.</p>}
        </div>
        <div>
          <p className="mb-1.5 flex items-center gap-1.5 text-[10px] font-semibold uppercase tracking-wider text-zinc-500">
            <KeyRound className="size-3" aria-hidden="true" />
            Secret key names <span className="font-normal normal-case tracking-normal text-zinc-600">(values never returned)</span>
          </p>
          {secretCount > 0 ? (
            <div className="flex flex-wrap gap-1.5">
              {configuration.secret_key_names.map((key) => (
                <code key={key} className="rounded border border-amber-500/20 bg-amber-500/5 px-1.5 py-1 text-[10px] text-amber-200">{key}</code>
              ))}
            </div>
          ) : <p className="text-xs text-zinc-600">No secret key names declared.</p>}
        </div>
        {fileRefCount > 0 && (
          <div>
            <p className="mb-1.5 text-[10px] font-semibold uppercase tracking-wider text-zinc-500">Protected file references</p>
            <div className="flex flex-wrap gap-1.5">
              {configuration.secret_file_refs.map((file) => (
                <code key={file} className="rounded bg-zinc-800 px-1.5 py-1 text-[10px] text-zinc-300">{file}</code>
              ))}
            </div>
          </div>
        )}
      </div>
    </details>
  )
}

function DocsBoundary({ entry }: { entry: IntegrationEntry }) {
  const docs = entry.evidence.docs
  return (
    <div className="space-y-2">
      <p className="text-[10px] font-semibold uppercase tracking-wider text-zinc-500">Documentation boundary</p>
      {docs.length > 0 ? (
        <ul className="space-y-1.5">
          {docs.map((doc) => (
            <li key={`${doc.path}-${doc.claim}`} className="flex min-w-0 items-start gap-2 text-[10px] leading-4 text-zinc-500">
              <span className="mt-1.5 size-1 shrink-0 rounded-full bg-zinc-700" aria-hidden="true" />
              <span className="min-w-0"><code className="break-all text-zinc-300">{doc.path}</code><span className="text-zinc-600"> · {doc.claim}</span></span>
            </li>
          ))}
        </ul>
      ) : <p className="text-xs text-zinc-600">No documentation path declared.</p>}
    </div>
  )
}

const LIVE_STATUS_TONE: Record<IntegrationState, string> = {
  not_configured: 'border-zinc-700 bg-zinc-800/80 text-zinc-300',
  unavailable: 'border-amber-500/30 bg-amber-500/10 text-amber-200',
  healthy: 'border-emerald-500/30 bg-emerald-500/10 text-emerald-200',
}

interface LiveStatusProps {
  entryId: string
  data: IntegrationStatusResponse | undefined
  isLoading: boolean
  isError: boolean
  managedTarget: boolean
}

function LiveStatus({ entryId, data, isLoading, isError, managedTarget }: LiveStatusProps) {
  const result = data?.results.find((item) => item.id === entryId)
  const isUnprobed = data?.unprobed.includes(entryId) ?? false
  const observedAt = result?.observed_at ?? data?.observed_at

  let content: ReactNode
  if (managedTarget && !MANAGED_STATUS_INTEGRATIONS.has(entryId)) {
    content = <span className="text-xs text-zinc-500">Probe not supported on managed target</span>
  } else if (isLoading) {
    content = <span className="text-xs text-blue-200">Loading live status…</span>
  } else if (isError) {
    content = <span className="text-xs text-amber-200">Live status unavailable</span>
  } else if (result) {
    content = (
      <div className="flex flex-wrap items-center gap-2">
        <Badge
          data-testid={`integration-live-badge-${entryId}`}
          className={cn('rounded-md border px-2 py-0.5 text-[10px] font-medium', LIVE_STATUS_TONE[result.state])}
        >
          {result.state}
        </Badge>
        {observedAt && (
          <time dateTime={observedAt} className="text-[10px] text-zinc-500">
            Observed {observedAt}
          </time>
        )}
      </div>
    )
  } else if (isUnprobed) {
    content = <span className="text-xs text-zinc-500">Probe not implemented</span>
  } else {
    content = <span className="text-xs text-zinc-500">No live result</span>
  }

  return (
    <div data-testid={`integration-live-status-${entryId}`} className="rounded-lg border border-zinc-800 bg-zinc-950/40 p-3">
      <p className="mb-1.5 text-[10px] font-semibold uppercase tracking-wider text-zinc-500">Live status</p>
      {content}
    </div>
  )
}

function managedStatusErrorMessage(error: unknown): string {
  if (error && typeof error === 'object') {
    const candidate = error as { status?: unknown; message?: unknown }
    const status = candidate.status
    const code = candidate.message
    if ((status === 409 || status === undefined) && code === 'managed_node_offline') {
      return 'Managed target is offline.'
    }
    if ((status === 409 || status === undefined) && code === 'capability_unavailable') {
      return 'Managed target does not support integration status.'
    }
  }
  return 'Managed integration status could not be loaded.'
}

function LiveStatusBanner({
  data,
  isLoading,
  isError,
  isFetching,
  retry,
  target,
  error,
}: {
  data: IntegrationStatusResponse | undefined
  isLoading: boolean
  isError: boolean
  isFetching: boolean
  retry: () => void
  target: ManagedServerID
  error: unknown
}) {
  if (isLoading) {
    return (
      <div data-testid="integrations-status-loading" role="status" className="flex items-center gap-3 rounded-xl border border-zinc-800 bg-zinc-900/70 p-4">
        <Skeleton className="size-9 rounded-lg bg-zinc-800 motion-reduce:animate-none" />
        <div className="space-y-1">
          <p className="text-sm font-medium text-zinc-200">Loading live integration status…</p>
          <p className="text-xs text-zinc-500">Catalog metadata remains available while local probes run.</p>
        </div>
      </div>
    )
  }

  if (isError) {
    const message = isLocalServer(target)
      ? 'Live integration status could not be loaded.'
      : managedStatusErrorMessage(error)
    return (
      <div data-testid="integrations-status-error" role="alert" className="flex flex-col gap-4 rounded-xl border border-amber-500/25 bg-amber-500/[0.06] p-5 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <p className="text-sm font-medium text-amber-200">{message}</p>
          <p className="mt-1 text-xs leading-5 text-amber-200/65">Catalog metadata remains available. No current state is inferred from configuration or catalog metadata.</p>
        </div>
        <Button type="button" variant="outline" size="sm" onClick={retry} disabled={isFetching} className="min-h-9 shrink-0 border-amber-500/30 text-amber-100 hover:bg-amber-500/10 hover:text-white">
          <RefreshCw className={cn('mr-2 size-3.5', isFetching && 'animate-spin motion-reduce:animate-none')} aria-hidden="true" />
          Retry status
        </Button>
      </div>
    )
  }

  if (!data) return null

  if (data.partial) {
    return (
      <div data-testid="integrations-status-partial" role="status" className="flex flex-col gap-2 rounded-xl border border-amber-500/25 bg-amber-500/[0.06] p-4 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <p className="text-sm font-medium text-amber-200">Live integration status is partial.</p>
          <p className="mt-1 text-xs leading-5 text-amber-200/65">Only the explicit probe results below are shown; missing results are not treated as healthy.</p>
        </div>
        {data.observed_at && <time dateTime={data.observed_at} className="shrink-0 text-[10px] text-amber-200/65">Observed {data.observed_at}</time>}
      </div>
    )
  }

  return (
    <div data-testid="integrations-status-ready" role="status" className="flex flex-col gap-2 rounded-xl border border-emerald-500/20 bg-emerald-500/[0.04] p-4 sm:flex-row sm:items-center sm:justify-between">
      <p className="text-sm font-medium text-emerald-200">
        {isLocalServer(target) ? 'Live integration status from this local host.' : 'Live integration status from the selected managed target.'}
      </p>
      {data.observed_at && <time dateTime={data.observed_at} className="text-[10px] text-emerald-200/65">Observed {data.observed_at}</time>}
    </div>
  )
}

function IntegrationCard({ entry, status }: { entry: IntegrationEntry; status: LiveStatusProps }) {
  const route = INTEGRATION_ROUTE_ALLOWLIST[entry.id]
  const requirementLabel = entry.requirement === 'feature_specific' ? 'Feature-specific' : 'Optional'
  const statuses = entry.status.canonical_states.filter((state) => STATUS_LABELS[state])

  return (
    <Card data-testid="integration-card" className="border border-zinc-800 bg-zinc-900/80 ring-0 transition-colors motion-reduce:transition-none hover:border-zinc-700">
      <CardHeader className="gap-4 border-b border-zinc-800/80 pb-4">
        <div className="flex items-start gap-3">
          <IntegrationIcon entry={entry} />
          <div className="min-w-0 flex-1">
            <div className="flex flex-wrap items-center gap-2">
              <CardTitle className="truncate text-sm font-semibold text-white">{entry.display_name}</CardTitle>
              <Badge className={cn('rounded-md border px-2 py-0.5 text-[10px] font-medium', REQUIREMENT_TONE[entry.requirement])}>
                {requirementLabel}
              </Badge>
            </div>
            <code className="mt-1 block truncate text-[10px] text-zinc-500" title={entry.id}>{entry.id}</code>
          </div>
        </div>
        <p className="text-sm leading-6 text-zinc-300">{entry.purpose}</p>
      </CardHeader>

      <CardContent className="space-y-4 pt-4">
        <LiveStatus {...status} />

        <div className="grid gap-4 sm:grid-cols-2">
          <div className="space-y-2">
            <p className="text-[10px] font-semibold uppercase tracking-wider text-zinc-500">Classes</p>
            <TagList values={entry.classes} tone={CLASS_TONE} />
          </div>
          <div className="space-y-2">
            <p className="text-[10px] font-semibold uppercase tracking-wider text-zinc-500">Targets</p>
            <TagList values={entry.targets} tone={TARGET_TONE} />
          </div>
        </div>

        <div className="rounded-lg border border-zinc-800 bg-zinc-950/40 p-3">
          <p className="mb-1.5 flex items-center gap-2 text-[10px] font-semibold uppercase tracking-wider text-zinc-500">
            <ShieldCheck className="size-3.5 text-zinc-600" aria-hidden="true" />
            Configuration boundary
          </p>
          <p className="text-xs leading-5 text-zinc-400">{entry.configuration.boundary}</p>
        </div>

        <ConfigurationDetails entry={entry} />

        <div className="space-y-3 border-t border-zinc-800/80 pt-3">
          <DocsBoundary entry={entry} />
          <div className="flex flex-wrap items-center justify-between gap-3">
            <p className="text-[10px] text-zinc-600">
              State vocabulary: {statuses.length > 0 ? statuses.join(' · ') : 'not declared'}
            </p>
            {route && (
              <Link
                to={route}
                className="inline-flex min-h-9 items-center gap-1.5 rounded-md border border-blue-500/30 px-3 py-1.5 text-xs font-medium text-blue-300 transition-colors motion-reduce:transition-none hover:border-blue-400/50 hover:bg-blue-500/10 hover:text-blue-200 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blue-500/70"
              >
                Open integration
                <ArrowRight className="size-3.5" aria-hidden="true" />
              </Link>
            )}
          </div>
        </div>
      </CardContent>
    </Card>
  )
}

function CatalogLoading() {
  return (
    <div data-testid="integrations-loading" className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
      {Array.from({ length: 6 }, (_, index) => (
        <Card key={index} className="border border-zinc-800 bg-zinc-900/80 ring-0">
          <CardHeader className="border-b border-zinc-800/80 pb-4">
            <div className="flex items-center gap-3">
              <Skeleton className="size-11 rounded-xl bg-zinc-800 motion-reduce:animate-none" />
              <div className="space-y-2">
                <Skeleton className="h-4 w-32 bg-zinc-800 motion-reduce:animate-none" />
                <Skeleton className="h-3 w-24 bg-zinc-800 motion-reduce:animate-none" />
              </div>
            </div>
            <Skeleton className="h-4 w-full bg-zinc-800 motion-reduce:animate-none" />
            <Skeleton className="h-4 w-4/5 bg-zinc-800 motion-reduce:animate-none" />
          </CardHeader>
          <CardContent className="space-y-4 pt-4">
            <Skeleton className="h-12 w-full bg-zinc-800 motion-reduce:animate-none" />
            <Skeleton className="h-20 w-full bg-zinc-800 motion-reduce:animate-none" />
            <Skeleton className="h-11 w-full bg-zinc-800 motion-reduce:animate-none" />
          </CardContent>
        </Card>
      ))}
    </div>
  )
}

function CatalogError({ retry, retrying }: { retry: () => void; retrying: boolean }) {
  return (
    <div data-testid="integrations-error" role="alert" className="flex flex-col gap-4 rounded-xl border border-amber-500/25 bg-amber-500/[0.06] p-5 sm:flex-row sm:items-center sm:justify-between">
      <div className="flex items-start gap-3">
        <span className="grid size-9 shrink-0 place-items-center rounded-lg bg-amber-500/10 text-amber-300">
          <AlertTriangle className="size-4" aria-hidden="true" />
        </span>
        <div>
          <p className="text-sm font-medium text-amber-200">Integration catalog could not be loaded.</p>
          <p className="mt-1 text-xs leading-5 text-amber-200/65">The catalog endpoint did not return a valid schema-v1 object. No integration status is inferred.</p>
        </div>
      </div>
      <Button type="button" variant="outline" size="sm" onClick={retry} disabled={retrying} className="min-h-9 shrink-0 border-amber-500/30 text-amber-100 hover:bg-amber-500/10 hover:text-white">
        <RefreshCw className={cn('mr-2 size-3.5', retrying && 'animate-spin motion-reduce:animate-none')} aria-hidden="true" />
        Retry
      </Button>
    </div>
  )
}

function EmptyCatalog({ filtered }: { filtered: boolean }) {
  return (
    <div data-testid="integrations-empty" className="rounded-xl border border-dashed border-zinc-700 bg-zinc-900/50 px-6 py-14 text-center">
      <span className="mx-auto grid size-11 place-items-center rounded-xl bg-zinc-800 text-zinc-500">
        <Layers3 className="size-5" aria-hidden="true" />
      </span>
      <h2 className="mt-4 text-sm font-semibold text-white">{filtered ? 'No integrations match these filters.' : 'No integrations are available.'}</h2>
      <p className="mx-auto mt-1 max-w-md text-xs leading-5 text-zinc-500">
        {filtered ? 'Try clearing a filter or searching for another integration.' : 'The authenticated catalog currently contains no entries.'}
      </p>
    </div>
  )
}

export default function Integrations() {
  const location = useLocation()
  const selectedServer = managedServerForLocation(location.pathname, location.search)
  const managedTarget = !isLocalServer(selectedServer)
  const [search, setSearch] = useState('')
  const [classFilter, setClassFilter] = useState('all')
  const [targetFilter, setTargetFilter] = useState<'all' | Target>('all')
  const [requirementFilter, setRequirementFilter] = useState<'all' | Requirement>('all')
  const catalogQuery = useQuery({
    queryKey: ['integrations-catalog'],
    queryFn: fetchIntegrationCatalog,
    staleTime: 60_000,
    retry: false,
  })
  const statusQuery = useQuery({
    queryKey: ['integrations-status', selectedServer],
    queryFn: () => fetchIntegrationStatus(selectedServer),
    staleTime: 60_000,
    retry: false,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
  })

  const catalogEntries = catalogQuery.data?.entries
  const entries = useMemo(() => catalogEntries ?? [], [catalogEntries])
  const classes = useMemo(
    () => Array.from(new Set(entries.flatMap((entry) => entry.classes))).sort(),
    [entries],
  )
  const targets = useMemo(
    () => Array.from(new Set(entries.flatMap((entry) => entry.targets))).sort(),
    [entries],
  )
  const filteredEntries = useMemo(() => {
    const normalizedSearch = search.trim().toLowerCase()
    return entries.filter((entry) => {
      if (normalizedSearch && !searchableText(entry).includes(normalizedSearch)) return false
      if (classFilter !== 'all' && !entry.classes.includes(classFilter)) return false
      if (targetFilter !== 'all' && !entry.targets.includes(targetFilter)) return false
      if (requirementFilter !== 'all' && entry.requirement !== requirementFilter) return false
      return true
    })
  }, [classFilter, entries, requirementFilter, search, targetFilter])
  const hasFilters = search.trim() !== '' || classFilter !== 'all' || targetFilter !== 'all' || requirementFilter !== 'all'

  function clearFilters() {
    setSearch('')
    setClassFilter('all')
    setTargetFilter('all')
    setRequirementFilter('all')
  }

  return (
    <div data-testid="integrations-page" className="mx-auto max-w-[1500px] space-y-6">
      <header className="flex flex-col gap-4 border-b border-zinc-800/80 pb-6 sm:flex-row sm:items-end sm:justify-between">
        <div className="min-w-0">
          <p className="mb-2 text-[10px] font-semibold uppercase tracking-[0.22em] text-blue-400">System catalog / schema v1</p>
          <h1 className="text-2xl font-semibold tracking-tight text-white sm:text-3xl">Integrations</h1>
          <p className="mt-2 max-w-2xl text-sm leading-6 text-zinc-400">A provider-neutral map of optional and feature-specific capabilities available to this HServer installation.</p>
        </div>
        <Button type="button" variant="outline" size="sm" onClick={() => void catalogQuery.refetch()} disabled={catalogQuery.isFetching} className="min-h-9 shrink-0 self-start border-zinc-700 text-zinc-300 hover:bg-zinc-800 hover:text-white sm:self-auto">
          <RefreshCw className={cn('mr-2 size-3.5', catalogQuery.isFetching && 'animate-spin motion-reduce:animate-none')} aria-hidden="true" />
          Refresh catalog
        </Button>
      </header>

      <section data-testid="integrations-catalog-banner" className="flex flex-col gap-3 rounded-xl border border-blue-500/20 bg-blue-500/[0.06] p-4 sm:flex-row sm:items-start">
        <span className="grid size-9 shrink-0 place-items-center rounded-lg bg-blue-500/10 text-blue-300">
          <Server className="size-4" aria-hidden="true" />
        </span>
        <div>
          <p className="text-sm font-medium text-blue-100">Catalog metadata — open integration for live status</p>
          <p className="mt-1 text-xs leading-5 text-blue-100/60">
            This page documents integration boundaries separately from live state. Current badges come only from the {managedTarget ? 'selected managed-node' : 'local'} read-only status endpoint; configuration and catalog metadata never imply health.
          </p>
        </div>
      </section>

      <LiveStatusBanner
        data={statusQuery.data}
        isLoading={statusQuery.isLoading}
        isError={statusQuery.isError}
        isFetching={statusQuery.isFetching}
        retry={() => { void statusQuery.refetch() }}
        target={selectedServer}
        error={statusQuery.error}
      />

      <section aria-label="Integration filters" className="rounded-xl border border-zinc-800 bg-zinc-900/70 p-4">
        <div className="grid gap-3 lg:grid-cols-[minmax(250px,1.6fr)_repeat(3,minmax(0,1fr))_auto] lg:items-end">
          <label className="block min-w-0">
            <span className="mb-1.5 block text-[10px] font-semibold uppercase tracking-wider text-zinc-500">Search catalog</span>
            <span className="relative block">
              <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-zinc-600" aria-hidden="true" />
              <input
                type="search"
                aria-label="Search integrations"
                value={search}
                onChange={(event) => setSearch(event.target.value)}
                placeholder="Name, purpose, key, class…"
                className="h-10 w-full rounded-lg border border-zinc-700 bg-zinc-950/70 pl-9 pr-9 text-sm text-white outline-none transition-colors placeholder:text-zinc-600 focus:border-blue-500/70 focus:ring-2 focus:ring-blue-500/20"
              />
              {search && (
                <button type="button" aria-label="Clear search" onClick={() => setSearch('')} className="absolute right-2 top-1/2 grid size-7 -translate-y-1/2 place-items-center rounded text-zinc-500 hover:bg-zinc-800 hover:text-white focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blue-500/70">
                  <X className="size-3.5" aria-hidden="true" />
                </button>
              )}
            </span>
          </label>

          <label className="block min-w-0">
            <span className="mb-1.5 block text-[10px] font-semibold uppercase tracking-wider text-zinc-500">Class</span>
            <select aria-label="Filter by class" value={classFilter} onChange={(event) => setClassFilter(event.target.value)} className="h-10 w-full rounded-lg border border-zinc-700 bg-zinc-950/70 px-3 text-sm text-zinc-200 outline-none focus:border-blue-500/70 focus:ring-2 focus:ring-blue-500/20">
              <option value="all">All classes</option>
              {classes.map((value) => <option key={value} value={value}>{labelFor(value)}</option>)}
            </select>
          </label>

          <label className="block min-w-0">
            <span className="mb-1.5 block text-[10px] font-semibold uppercase tracking-wider text-zinc-500">Target</span>
            <select aria-label="Filter by target" value={targetFilter} onChange={(event) => setTargetFilter(event.target.value as 'all' | Target)} className="h-10 w-full rounded-lg border border-zinc-700 bg-zinc-950/70 px-3 text-sm text-zinc-200 outline-none focus:border-blue-500/70 focus:ring-2 focus:ring-blue-500/20">
              <option value="all">All targets</option>
              {targets.map((value) => <option key={value} value={value}>{labelFor(value)}</option>)}
            </select>
          </label>

          <label className="block min-w-0">
            <span className="mb-1.5 block text-[10px] font-semibold uppercase tracking-wider text-zinc-500">Requirement</span>
            <select aria-label="Filter by requirement" value={requirementFilter} onChange={(event) => setRequirementFilter(event.target.value as 'all' | Requirement)} className="h-10 w-full rounded-lg border border-zinc-700 bg-zinc-950/70 px-3 text-sm text-zinc-200 outline-none focus:border-blue-500/70 focus:ring-2 focus:ring-blue-500/20">
              <option value="all">All requirements</option>
              <option value="optional">Optional</option>
              <option value="feature_specific">Feature-specific</option>
            </select>
          </label>

          {hasFilters && (
            <Button type="button" variant="ghost" size="sm" onClick={clearFilters} className="min-h-10 text-zinc-400 hover:bg-zinc-800 hover:text-white lg:px-3">
              Clear filters
            </Button>
          )}
        </div>
        <div className="mt-4 flex flex-wrap items-center justify-between gap-2 border-t border-zinc-800/80 pt-3">
          <p data-testid="integrations-count" className="text-xs text-zinc-500"><span className="font-medium text-zinc-300">{filteredEntries.length}</span> of {entries.length} catalog entries</p>
          {hasFilters && <p className="text-[10px] text-zinc-600">Filters search metadata, configuration names, and documented boundaries.</p>}
        </div>
      </section>

      {catalogQuery.isLoading ? <CatalogLoading /> : catalogQuery.isError ? <CatalogError retry={() => void catalogQuery.refetch()} retrying={catalogQuery.isFetching} /> : filteredEntries.length === 0 ? <EmptyCatalog filtered={hasFilters} /> : (
        <div data-testid="integrations-grid" className="grid items-start gap-4 md:grid-cols-2 xl:grid-cols-3">
          {filteredEntries.map((entry) => (
            <IntegrationCard
              key={entry.id}
              entry={entry}
              status={{
                entryId: entry.id,
                data: statusQuery.data,
                isLoading: statusQuery.isLoading,
                isError: statusQuery.isError,
                managedTarget,
              }}
            />
          ))}
        </div>
      )}
    </div>
  )
}
