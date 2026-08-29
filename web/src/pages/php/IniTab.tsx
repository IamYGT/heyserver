import { useState, useMemo } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  Search, Save, Filter, ChevronDown, ChevronRight,
  AlertCircle, RefreshCw,
} from 'lucide-react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { api } from '@/lib/api'
import { toast } from 'sonner'
import { cn } from '@/lib/utils'
import type { INIDirective } from '@/lib/types'

// ─── Simple mode section configs ──────────────────────────────────────────────

interface SimpleSection {
  title: string
  icon: string
  keys: Array<{ key: string; label: string; placeholder: string; description?: string }>
}

const SIMPLE_SECTIONS: SimpleSection[] = [
  {
    title: 'Resource Limits',
    icon: '⚡',
    keys: [
      { key: 'memory_limit',         label: 'Memory Limit',          placeholder: '256M',  description: 'Maximum memory a script may consume' },
      { key: 'max_execution_time',   label: 'Max Execution Time',     placeholder: '60',    description: 'Maximum time in seconds a script may run' },
      { key: 'max_input_time',       label: 'Max Input Time',         placeholder: '60',    description: 'Max time to parse input data (GET, POST)' },
      { key: 'max_input_vars',       label: 'Max Input Vars',         placeholder: '1000',  description: 'Maximum number of input variables' },
    ],
  },
  {
    title: 'File Uploads',
    icon: '📁',
    keys: [
      { key: 'file_uploads',         label: 'File Uploads',           placeholder: 'On',    description: 'Whether to allow HTTP file uploads' },
      { key: 'upload_max_filesize',  label: 'Upload Max Filesize',    placeholder: '64M',   description: 'Maximum allowed size for uploaded files' },
      { key: 'post_max_size',        label: 'Post Max Size',          placeholder: '64M',   description: 'Maximum size of POST data allowed' },
      { key: 'max_file_uploads',     label: 'Max File Uploads',       placeholder: '20',    description: 'Maximum number of files in a single upload' },
    ],
  },
  {
    title: 'Error Handling',
    icon: '🐛',
    keys: [
      { key: 'error_reporting',      label: 'Error Reporting',        placeholder: 'E_ALL', description: 'Report which PHP errors' },
      { key: 'display_errors',       label: 'Display Errors',         placeholder: 'Off',   description: 'Print error messages to output (Off in prod!)' },
      { key: 'log_errors',           label: 'Log Errors',             placeholder: 'On',    description: 'Log errors to error_log file' },
      { key: 'error_log',            label: 'Error Log Path',         placeholder: '/var/log/php_errors.log' },
    ],
  },
  {
    title: 'Sessions',
    icon: '🔑',
    keys: [
      { key: 'session.gc_maxlifetime', label: 'Session Lifetime (s)', placeholder: '1440', description: 'Time after which session data is garbage collected' },
      { key: 'session.cookie_secure',  label: 'Cookie Secure',        placeholder: 'On',   description: 'Transmit cookies only over HTTPS' },
      { key: 'session.cookie_httponly',label: 'Cookie HttpOnly',      placeholder: 'On',   description: 'Prevent JS access to session cookie' },
      { key: 'session.cookie_samesite',label: 'Cookie SameSite',      placeholder: 'Lax',  description: 'SameSite cookie attribute (Lax/Strict/None)' },
    ],
  },
  {
    title: 'OPcache',
    icon: '🚀',
    keys: [
      { key: 'opcache.enable',              label: 'OPcache Enable',       placeholder: '1' },
      { key: 'opcache.memory_consumption',  label: 'Memory (MB)',          placeholder: '256' },
      { key: 'opcache.max_accelerated_files', label: 'Max Cached Files',   placeholder: '20000' },
      { key: 'opcache.revalidate_freq',     label: 'Revalidate Freq (s)',  placeholder: '60',   description: '0 = always check (dev), 60 = prod' },
      { key: 'opcache.jit',                 label: 'JIT Mode',             placeholder: 'tracing', description: 'PHP 8+ JIT compiler mode' },
      { key: 'opcache.jit_buffer_size',     label: 'JIT Buffer',           placeholder: '100M' },
    ],
  },
  {
    title: 'Security',
    icon: '🔒',
    keys: [
      { key: 'expose_php',           label: 'Expose PHP Version',     placeholder: 'Off',   description: 'Disable to hide PHP version from headers' },
      { key: 'allow_url_fopen',      label: 'Allow URL fopen',        placeholder: 'Off',   description: 'Off = prevent remote file inclusion attacks' },
      { key: 'allow_url_include',    label: 'Allow URL include',      placeholder: 'Off',   description: 'Never On in production' },
      { key: 'disable_functions',    label: 'Disabled Functions',     placeholder: 'exec,system,passthru', description: 'Comma-separated list of disabled functions' },
    ],
  },
]

// ─── Simple Mode ──────────────────────────────────────────────────────────────

interface SimpleModeProps {
  version: string
  domain: string
}

function IniLoadError({ error, retry }: { error: Error; retry: () => void }) {
  return (
    <Card className="border-red-500/25 bg-red-500/[0.05]">
      <CardContent className="p-6 text-center">
        <AlertCircle className="mx-auto size-5 text-red-400" />
        <p className="mt-2 text-sm text-red-300">php.ini directives could not be loaded.</p>
        <p className="mt-1 text-xs text-zinc-600">{error.message}</p>
        <Button type="button" variant="outline" size="sm" className="mt-4 border-red-500/30 text-red-200" onClick={retry}>
          <RefreshCw className="mr-2 size-3.5" />Retry
        </Button>
      </CardContent>
    </Card>
  )
}

function SimpleMode({ version, domain }: SimpleModeProps) {
  const queryClient = useQueryClient()
  const [values, setValues] = useState<Record<string, string>>({})
  const [dirty, setDirty] = useState(false)
  const [expandedSections, setExpandedSections] = useState<Record<string, boolean>>(
    Object.fromEntries(SIMPLE_SECTIONS.map(s => [s.title, true]))
  )

  const { data: directives, isLoading, isError, error, refetch } = useQuery<INIDirective[]>({
    queryKey: ['php', 'ini', version, domain],
    queryFn: async () => {
      const data = await api.get<INIDirective[]>(`/php/ini/${version}/${domain}`)
      const initial: Record<string, string> = {}
      data.forEach(d => { initial[d.key] = d.value })
      setValues(initial)
      setDirty(false)
      return data
    },
  })

  const saveMutation = useMutation({
    mutationFn: () =>
      api.put(`/php/ini/${version}/${domain}`, { directives: values }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['php', 'ini', version, domain] })
      toast.success('php.ini saved and applied')
      setDirty(false)
    },
    onError: () => toast.error('Failed to save php.ini'),
  })

  const toggleSection = (title: string) =>
    setExpandedSections(s => ({ ...s, [title]: !s[title] }))

  if (isLoading) {
    return (
      <div className="space-y-3">
        {Array.from({ length: 6 }).map((_, i) => (
          <Skeleton key={i} className="h-36 bg-zinc-800 rounded-lg" />
        ))}
      </div>
    )
  }
  if (isError) return <IniLoadError error={error} retry={() => { void refetch() }} />

  return (
    <div className="space-y-4">
      {dirty && (
        <div className="flex items-center justify-between bg-amber-500/10 border border-amber-500/30 rounded-lg px-4 py-2.5">
          <div className="flex items-center gap-2 text-amber-400 text-sm">
            <AlertCircle className="w-4 h-4" />
            You have unsaved changes
          </div>
          <Button
            className="bg-amber-500 hover:bg-amber-400 text-black h-7 text-sm"
            onClick={() => saveMutation.mutate()}
            disabled={saveMutation.isPending}
          >
            {saveMutation.isPending ? <RefreshCw className="w-3.5 h-3.5 animate-spin" /> : 'Save & Apply'}
          </Button>
        </div>
      )}

      {SIMPLE_SECTIONS.map(section => (
        <Card key={section.title} className="bg-zinc-900 border-zinc-800">
          <CardHeader
            className="py-3 px-4 cursor-pointer select-none"
            onClick={() => toggleSection(section.title)}
          >
            <CardTitle className="flex items-center justify-between text-sm">
              <div className="flex items-center gap-2">
                <span>{section.icon}</span>
                <span className="text-white">{section.title}</span>
              </div>
              {expandedSections[section.title]
                ? <ChevronDown className="w-4 h-4 text-zinc-500" />
                : <ChevronRight className="w-4 h-4 text-zinc-500" />
              }
            </CardTitle>
          </CardHeader>
          {expandedSections[section.title] && (
            <CardContent className="px-4 pb-4">
              <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
                {section.keys.map(({ key, label, placeholder, description }) => {
                  const directive = directives?.find(d => d.key === key)
                  const currentVal = values[key] ?? directive?.value ?? ''
                  const isModified = directive && currentVal !== directive.defaultValue

                  return (
                    <div key={key} className="space-y-1">
                      <div className="flex items-center gap-1.5">
                        <Label className="text-zinc-400 text-xs">{label}</Label>
                        {isModified && (
                          <span className="w-1.5 h-1.5 rounded-full bg-amber-400 flex-shrink-0" title="Modified from default" />
                        )}
                      </div>
                      <Input
                        value={currentVal}
                        onChange={e => {
                          setValues(v => ({ ...v, [key]: e.target.value }))
                          setDirty(true)
                        }}
                        placeholder={placeholder}
                        className="bg-zinc-800 border-zinc-700 text-white h-8 text-sm font-mono"
                      />
                      {description && (
                        <p className="text-zinc-600 text-[11px] leading-relaxed">{description}</p>
                      )}
                    </div>
                  )
                })}
              </div>
            </CardContent>
          )}
        </Card>
      ))}

      {dirty && (
        <div className="flex justify-end">
          <Button
            className="bg-blue-600 hover:bg-blue-500 text-white"
            onClick={() => saveMutation.mutate()}
            disabled={saveMutation.isPending}
          >
            {saveMutation.isPending ? (
              <><RefreshCw className="w-3.5 h-3.5 mr-2 animate-spin" />Saving…</>
            ) : (
              <><Save className="w-3.5 h-3.5 mr-2" />Save & Apply</>
            )}
          </Button>
        </div>
      )}
    </div>
  )
}

// ─── Advanced Mode ────────────────────────────────────────────────────────────

function AdvancedMode({ version, domain }: SimpleModeProps) {
  const queryClient = useQueryClient()
  const [search, setSearch] = useState('')
  const [showOnlyModified, setShowOnlyModified] = useState(false)
  const [editedValues, setEditedValues] = useState<Record<string, string>>({})

  const { data: directives = [], isLoading, isError, error, refetch } = useQuery<INIDirective[]>({
    queryKey: ['php', 'ini', version, domain],
    queryFn: () => api.get<INIDirective[]>(`/php/ini/${version}/${domain}`),
  })

  const saveMutation = useMutation({
    mutationFn: () =>
      api.put(`/php/ini/${version}/${domain}`, { directives: editedValues }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['php', 'ini', version, domain] })
      toast.success('php.ini saved and applied')
      setEditedValues({})
    },
    onError: () => toast.error('Failed to save php.ini'),
  })

  const filtered = useMemo(() => {
    return directives.filter(d => {
      if (showOnlyModified && d.value === d.defaultValue && !editedValues[d.key]) return false
      if (search) {
        const q = search.toLowerCase()
        return d.key.toLowerCase().includes(q) || d.section?.toLowerCase().includes(q)
      }
      return true
    })
  }, [directives, search, showOnlyModified, editedValues])

  const sections = useMemo(() => {
    const map = new Map<string, INIDirective[]>()
    filtered.forEach(d => {
      const sec = d.section || 'General'
      if (!map.has(sec)) map.set(sec, [])
      map.get(sec)!.push(d)
    })
    return map
  }, [filtered])

  const hasDirty = Object.keys(editedValues).length > 0

  if (isLoading) {
    return (
      <div className="space-y-2">
        {Array.from({ length: 12 }).map((_, i) => (
          <Skeleton key={i} className="h-10 bg-zinc-800 rounded" />
        ))}
      </div>
    )
  }
  if (isError) return <IniLoadError error={error} retry={() => { void refetch() }} />

  return (
    <div className="space-y-4">
      {/* Toolbar */}
      <div className="flex items-center gap-3">
        <div className="relative flex-1">
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-zinc-500" />
          <Input
            value={search}
            onChange={e => setSearch(e.target.value)}
            placeholder="Search directives…"
            className="pl-9 bg-zinc-800 border-zinc-700 text-white h-8"
          />
        </div>
        <button
          onClick={() => setShowOnlyModified(v => !v)}
          className={cn(
            'flex items-center gap-1.5 rounded-md border px-3 h-8 text-sm transition-colors',
            showOnlyModified
              ? 'border-amber-500 bg-amber-500/10 text-amber-400'
              : 'border-zinc-700 text-zinc-400 hover:border-zinc-600'
          )}
        >
          <Filter className="w-3.5 h-3.5" />
          Modified only
        </button>
        {hasDirty && (
          <Button
            className="bg-blue-600 hover:bg-blue-500 text-white h-8"
            onClick={() => saveMutation.mutate()}
            disabled={saveMutation.isPending}
          >
            {saveMutation.isPending ? <RefreshCw className="w-3.5 h-3.5 animate-spin" /> : <><Save className="w-3.5 h-3.5 mr-1.5" />Save</>}
          </Button>
        )}
      </div>

      {/* Table grouped by section */}
      {Array.from(sections.entries()).map(([sectionName, sectionDirectives]) => (
        <Card key={sectionName} className="bg-zinc-900 border-zinc-800 overflow-hidden">
          <div className="px-4 py-2.5 border-b border-zinc-800 bg-zinc-800/30">
            <p className="text-zinc-400 text-xs font-medium uppercase tracking-wider">
              {sectionName}
              <span className="ml-2 text-zinc-600 normal-case">{sectionDirectives.length} directives</span>
            </p>
          </div>
          <div className="divide-y divide-zinc-800/60">
            {sectionDirectives.map(directive => {
              const currentVal = editedValues[directive.key] ?? directive.value
              const isModified = currentVal !== directive.defaultValue

              return (
                <div
                  key={directive.key}
                  className={cn(
                    'grid grid-cols-[1fr_1fr_auto] gap-3 items-center px-4 py-2.5 hover:bg-zinc-800/30 transition-colors',
                    isModified && 'bg-amber-500/5'
                  )}
                >
                  <div className="flex items-center gap-2 min-w-0">
                    {isModified && <span className="w-1.5 h-1.5 rounded-full bg-amber-400 flex-shrink-0" />}
                    <code className="text-zinc-300 text-xs truncate">{directive.key}</code>
                  </div>
                  <Input
                    value={currentVal}
                    onChange={e => setEditedValues(v => ({ ...v, [directive.key]: e.target.value }))}
                    className={cn(
                      'h-7 text-xs font-mono bg-zinc-800 border-zinc-700 text-white',
                      isModified && 'border-amber-500/50'
                    )}
                  />
                  <div className="text-right min-w-0">
                    <code className="text-zinc-600 text-[11px] truncate block max-w-28" title={directive.defaultValue}>
                      default: {directive.defaultValue || '—'}
                    </code>
                  </div>
                </div>
              )
            })}
          </div>
        </Card>
      ))}
    </div>
  )
}

// ─── IniTab ───────────────────────────────────────────────────────────────────

interface IniTabProps {
  selectedVersion: string
  selectedDomain: string
  domains: Array<{ version: string; name: string }>
}

export default function IniTab({ selectedVersion, selectedDomain, domains }: IniTabProps) {
  const [mode, setMode] = useState<'simple' | 'advanced'>('simple')
  const [domain, setDomain] = useState(selectedDomain || domains[0]?.name || '')

  const version = domains.find(d => d.name === domain)?.version ?? selectedVersion

  if (!domain) {
    return (
      <Card className="border-zinc-800 bg-zinc-900">
        <CardContent className="p-6 text-center text-sm text-zinc-400">No PHP-FPM domain is available for php.ini management.</CardContent>
      </Card>
    )
  }

  return (
    <div className="space-y-4">
      {/* Toolbar */}
      <div className="flex items-center gap-3 flex-wrap">
        <div className="flex items-center gap-0 border border-zinc-700 rounded-lg overflow-hidden">
          <button
            onClick={() => setMode('simple')}
            className={cn(
              'px-3 py-1.5 text-sm transition-colors',
              mode === 'simple' ? 'bg-zinc-700 text-white' : 'text-zinc-400 hover:text-zinc-200'
            )}
          >
            Simple
          </button>
          <button
            onClick={() => setMode('advanced')}
            className={cn(
              'px-3 py-1.5 text-sm transition-colors border-l border-zinc-700',
              mode === 'advanced' ? 'bg-zinc-700 text-white' : 'text-zinc-400 hover:text-zinc-200'
            )}
          >
            Advanced
          </button>
        </div>
        <div className="flex items-center gap-2">
          <Label className="text-zinc-500 text-xs">Domain:</Label>
          <select
            value={domain}
            onChange={e => setDomain(e.target.value)}
            className="bg-zinc-800 border border-zinc-700 text-zinc-300 text-xs rounded-md px-2 py-1.5 h-8"
          >
            {domains.map(d => (
              <option key={`${d.version}-${d.name}`} value={d.name}>{d.name} (PHP {d.version})</option>
            ))}
          </select>
        </div>
        <Badge className="bg-zinc-800 border-zinc-700 text-zinc-400 text-xs">PHP {version}</Badge>
      </div>

      {mode === 'simple'
        ? <SimpleMode version={version} domain={domain} />
        : <AdvancedMode version={version} domain={domain} />
      }
    </div>
  )
}
