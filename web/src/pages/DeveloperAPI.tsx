import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Braces, Check, Copy, Download, ExternalLink, RefreshCw, Search } from 'lucide-react'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { buttonVariants } from '@/components/ui/button-variants'
import { ApiError } from '@/lib/api'
import { cn } from '@/lib/utils'

interface OpenAPIOperation {
  operationId: string
  summary: string
  description?: string
  tags?: string[]
  parameters?: Array<{ name: string; in: string; required?: boolean }>
  'x-hserver-access'?: string
  'x-hserver-loopback-only'?: boolean
}

interface OpenAPIContract {
  openapi: string
  info: { title: string; version: string; description?: string }
  paths: Record<string, Partial<Record<string, OpenAPIOperation>>>
  components?: { schemas?: Record<string, unknown> }
  'x-hserver-contract-version': number
  'x-hserver-route-count': number
  'x-hserver-schema-count': number
}

interface Endpoint extends OpenAPIOperation {
  method: string
  path: string
  access: string
  tag: string
}

const HTTP_METHODS = ['get', 'post', 'put', 'patch', 'delete', 'options', 'head'] as const

function endpointInventory(contract?: OpenAPIContract): Endpoint[] {
  if (!contract) return []
  const endpoints: Endpoint[] = []
  Object.entries(contract.paths).forEach(([path, pathItem]) => {
    HTTP_METHODS.forEach((method) => {
      const operation = pathItem[method]
      if (!operation) return
      endpoints.push({
        ...operation,
        method: method.toUpperCase(),
        path,
        access: operation['x-hserver-access'] ?? 'Unspecified',
        tag: operation.tags?.[0] ?? 'other',
      })
    })
  })
  return endpoints
}

function methodTone(method: string): string {
  switch (method) {
    case 'GET': return 'border-emerald-500/25 bg-emerald-500/10 text-emerald-300'
    case 'POST': return 'border-blue-500/25 bg-blue-500/10 text-blue-300'
    case 'PUT':
    case 'PATCH': return 'border-amber-500/25 bg-amber-500/10 text-amber-300'
    case 'DELETE': return 'border-red-500/25 bg-red-500/10 text-red-300'
    default: return 'border-zinc-700 bg-zinc-800 text-zinc-300'
  }
}

function accessTone(access: string): string {
  if (access === 'Public') return 'text-emerald-300'
  if (access === 'Admin') return 'text-red-300'
  if (access === 'Managed-node agent') return 'text-cyan-300'
  if (access === 'Local internal trigger') return 'text-purple-300'
  return 'text-amber-200'
}

async function loadContract(): Promise<OpenAPIContract> {
  let response: Response
  try {
    response = await fetch('/openapi.json', { credentials: 'same-origin' })
  } catch {
    throw new ApiError(0, 'OpenAPI contract request failed')
  }
  if (!response.ok) throw new ApiError(response.status, 'OpenAPI contract request failed')
  const contract = await response.json() as OpenAPIContract
  if (contract.openapi !== '3.1.0' || !contract.paths || !contract['x-hserver-route-count']) {
    throw new Error('The installed OpenAPI contract is incomplete or incompatible')
  }
  return contract
}

function contractErrorState(error: unknown): { title: string; message: string } {
  const status = error instanceof ApiError ? error.status : undefined

  if (status === 401 || status === 403) {
    return {
      title: 'Permission denied',
      message: 'You do not have permission to view the installed OpenAPI contract.',
    }
  }

  if (status === 404) {
    return {
      title: 'OpenAPI contract not found',
      message: 'The installed OpenAPI contract is not available on this server.',
    }
  }

  if (status === 0 || (status !== undefined && status >= 500 && status < 600)) {
    return {
      title: 'OpenAPI contract temporarily unavailable',
      message: 'The contract service is temporarily unavailable. Check your connection and try again.',
    }
  }

  return {
    title: 'OpenAPI contract operation failed',
    message: 'The installed OpenAPI contract could not be loaded. Please try again.',
  }
}

export default function DeveloperAPI() {
  const [search, setSearch] = useState('')
  const [method, setMethod] = useState('all')
  const [access, setAccess] = useState('all')
  const [tag, setTag] = useState('all')
  const [copied, setCopied] = useState(false)
  const contractQuery = useQuery({ queryKey: ['openapi-contract'], queryFn: loadContract, staleTime: 300_000 })
  const endpoints = useMemo(() => endpointInventory(contractQuery.data), [contractQuery.data])
  const methods = useMemo(() => [...new Set(endpoints.map((item) => item.method))].sort(), [endpoints])
  const accesses = useMemo(() => [...new Set(endpoints.map((item) => item.access))].sort(), [endpoints])
  const tags = useMemo(() => [...new Set(endpoints.map((item) => item.tag))].sort(), [endpoints])
  const filtered = useMemo(() => {
    const needle = search.trim().toLowerCase()
    return endpoints.filter((item) =>
      (method === 'all' || item.method === method)
      && (access === 'all' || item.access === access)
      && (tag === 'all' || item.tag === tag)
      && (!needle || `${item.method} ${item.path} ${item.operationId} ${item.tag} ${item.access}`.toLowerCase().includes(needle)),
    )
  }, [access, endpoints, method, search, tag])
  const errorState = contractQuery.isError ? contractErrorState(contractQuery.error) : null

  const copyURL = async () => {
    const url = `${window.location.origin}/openapi.json`
    try {
      await navigator.clipboard.writeText(url)
      setCopied(true)
      window.setTimeout(() => setCopied(false), 1500)
      toast.success('OpenAPI URL copied')
    } catch {
      toast.error('Could not copy the OpenAPI URL')
    }
  }

  return (
    <div className="space-y-5">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div className="flex items-start gap-3">
          <div className="flex size-10 shrink-0 items-center justify-center rounded-xl border border-cyan-500/20 bg-cyan-500/10">
            <Braces className="size-5 text-cyan-300" />
          </div>
          <div>
            <h1 className="text-xl font-bold text-white">Developer API</h1>
            <p className="mt-1 max-w-2xl text-sm text-zinc-500">Explore the route, access, and promoted payload-schema contract generated from this installation&apos;s authoritative API manifest.</p>
          </div>
        </div>
        <div className="flex flex-wrap gap-2">
          <Button variant="outline" size="sm" onClick={() => void copyURL()} className="border-zinc-700 text-zinc-300">
            {copied ? <Check className="mr-1.5 size-3.5 text-emerald-400" /> : <Copy className="mr-1.5 size-3.5" />}
            Copy URL
          </Button>
          <a href="/openapi.json" download="hserver-openapi.json" className={cn(buttonVariants({ variant: 'outline', size: 'sm' }), 'border-zinc-700 text-zinc-300')}>
            <Download className="mr-1.5 size-3.5" />Download JSON
          </a>
        </div>
      </div>

      {contractQuery.isError ? (
        <div className="rounded-xl border border-red-500/25 bg-red-500/[0.06] p-5">
          <p className="font-medium text-red-300">{errorState?.title}</p>
          <p className="mt-1 text-sm text-zinc-400">{errorState?.message}</p>
          <Button variant="outline" size="sm" className="mt-4 border-red-500/30 text-red-200" onClick={() => void contractQuery.refetch()}>
            <RefreshCw className="mr-1.5 size-3.5" />Retry
          </Button>
        </div>
      ) : (
        <>
          <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-5">
            {[
              ['OpenAPI', contractQuery.data?.openapi ?? 'Loading'],
              ['Contract version', contractQuery.data ? `v${contractQuery.data['x-hserver-contract-version']}` : 'Loading'],
              ['Declared routes', contractQuery.data?.['x-hserver-route-count']?.toString() ?? 'Loading'],
              ['Promoted schemas', contractQuery.data?.['x-hserver-schema-count']?.toString() ?? 'Loading'],
              ['Visible after filters', contractQuery.isLoading ? 'Loading' : filtered.length.toString()],
            ].map(([label, value]) => (
              <div key={label} className="rounded-xl border border-zinc-800 bg-zinc-900/70 p-4">
                <p className="text-[11px] font-medium uppercase tracking-wide text-zinc-600">{label}</p>
                <p className="mt-1 text-lg font-semibold text-zinc-100">{value}</p>
              </div>
            ))}
          </div>

          <div className="rounded-xl border border-zinc-800 bg-zinc-900/70">
            <div className="grid gap-2 border-b border-zinc-800 p-3 lg:grid-cols-[minmax(15rem,1fr)_10rem_14rem_12rem]">
              <label className="relative">
                <Search className="absolute left-3 top-1/2 size-4 -translate-y-1/2 text-zinc-600" />
                <input aria-label="Search API routes" value={search} onChange={(event) => setSearch(event.target.value)} placeholder="Search path, operation or tag" className="h-9 w-full rounded-lg border border-zinc-700 bg-zinc-950 pl-9 pr-3 text-sm text-white outline-none placeholder:text-zinc-600 focus:border-cyan-500/60" />
              </label>
              <FilterSelect label="Method" value={method} onChange={setMethod} options={methods} />
              <FilterSelect label="Access" value={access} onChange={setAccess} options={accesses} />
              <FilterSelect label="Tag" value={tag} onChange={setTag} options={tags} />
            </div>

            <div className="divide-y divide-zinc-800/80">
              {contractQuery.isLoading ? (
                <p className="p-8 text-center text-sm text-zinc-500">Loading the installed contract…</p>
              ) : filtered.length === 0 ? (
                <p className="p-8 text-center text-sm text-zinc-500">No API routes match these filters.</p>
              ) : filtered.map((endpoint) => (
                <div key={`${endpoint.method}-${endpoint.path}`} className="grid gap-3 px-4 py-3 hover:bg-zinc-800/25 lg:grid-cols-[5rem_minmax(15rem,1fr)_12rem] lg:items-center">
                  <span className={cn('w-fit rounded-md border px-2 py-1 font-mono text-[11px] font-bold', methodTone(endpoint.method))}>{endpoint.method}</span>
                  <div className="min-w-0">
                    <div className="flex flex-wrap items-center gap-2">
                      <code className="break-all text-sm text-zinc-100">{endpoint.path}</code>
                      {endpoint['x-hserver-loopback-only'] && <span className="rounded bg-purple-500/10 px-1.5 py-0.5 text-[10px] text-purple-300">loopback only</span>}
                    </div>
                    <div className="mt-1 flex flex-wrap gap-1.5 text-[10px] text-zinc-600">
                      <span>{endpoint.operationId}</span>
                      {endpoint.parameters?.filter((parameter) => parameter.in === 'path').map((parameter) => (
                        <span key={parameter.name} className="rounded border border-zinc-800 px-1.5 py-0.5 text-zinc-500">{parameter.name}</span>
                      ))}
                    </div>
                  </div>
                  <div className="flex items-center justify-between gap-2 lg:justify-end">
                    <span className="rounded-md border border-zinc-800 bg-zinc-950 px-2 py-1 text-[10px] text-zinc-500">{endpoint.tag}</span>
                    <span className={cn('text-xs font-medium', accessTone(endpoint.access))}>{endpoint.access}</span>
                  </div>
                </div>
              ))}
            </div>
          </div>

          <div className="flex flex-wrap items-center justify-between gap-3 rounded-xl border border-zinc-800 bg-zinc-900/50 px-4 py-3 text-xs text-zinc-500">
            <span>Payload schemas are promoted incrementally from implemented handlers; generated routes never invent request or response behavior.</span>
            <a href="https://spec.openapis.org/oas/v3.1.0" target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 text-cyan-400 hover:text-cyan-300">OpenAPI 3.1 specification <ExternalLink className="size-3" /></a>
          </div>
        </>
      )}
    </div>
  )
}

function FilterSelect({ label, value, onChange, options }: { label: string; value: string; onChange: (value: string) => void; options: string[] }) {
  return (
    <select aria-label={`Filter by ${label.toLowerCase()}`} value={value} onChange={(event) => onChange(event.target.value)} className="h-9 rounded-lg border border-zinc-700 bg-zinc-950 px-3 text-sm text-zinc-300 outline-none focus:border-cyan-500/60">
      <option value="all">All {label.toLowerCase()}s</option>
      {options.map((option) => <option key={option} value={option}>{option}</option>)}
    </select>
  )
}
