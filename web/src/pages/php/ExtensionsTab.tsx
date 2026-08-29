import { useState, useMemo } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Search, AlertTriangle, RefreshCw } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { Skeleton } from '@/components/ui/skeleton'
import { Input } from '@/components/ui/input'
import {
  Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter, DialogDescription,
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { api } from '@/lib/api'
import { toast } from 'sonner'
import { cn } from '@/lib/utils'
import type { PHPExtension } from '@/lib/types'

// ─── Category config ──────────────────────────────────────────────────────────

const CATEGORY_COLORS: Record<string, string> = {
  core:     'bg-blue-500/10 text-blue-400 border-blue-500/20',
  database: 'bg-green-500/10 text-green-400 border-green-500/20',
  caching:  'bg-purple-500/10 text-purple-400 border-purple-500/20',
  imaging:  'bg-pink-500/10 text-pink-400 border-pink-500/20',
  security: 'bg-red-500/10 text-red-400 border-red-500/20',
  network:  'bg-cyan-500/10 text-cyan-400 border-cyan-500/20',
  other:    'bg-zinc-500/10 text-zinc-400 border-zinc-500/20',
}

const CATEGORY_ICONS: Record<string, string> = {
  core:     '⚙️',
  database: '🗄️',
  caching:  '⚡',
  imaging:  '🖼️',
  security: '🔒',
  network:  '🌐',
  other:    '📦',
}

// ─── Extension Card ───────────────────────────────────────────────────────────

interface ExtCardProps {
  ext: PHPExtension
  version: string
  onToggle: (ext: PHPExtension, enable: boolean) => void
  isPending: boolean
}

function ExtCard({ ext, onToggle, isPending }: ExtCardProps) {
  return (
    <div
      className={cn(
        'rounded-xl border p-4 transition-all',
        ext.enabled
          ? 'border-zinc-700 bg-zinc-800/50'
          : 'border-zinc-800 bg-zinc-900 opacity-60'
      )}
    >
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0">
          <div className="flex items-center gap-2 flex-wrap">
            <p className="text-white text-sm font-semibold font-mono">{ext.name}</p>
            {ext.version && (
              <span className="text-zinc-600 text-[11px] font-mono">{ext.version}</span>
            )}
          </div>
          <Badge className={cn('text-[10px] border mt-1.5', CATEGORY_COLORS[ext.type] ?? CATEGORY_COLORS.other)}>
            {CATEGORY_ICONS[ext.type]} {ext.type}
          </Badge>
          {ext.description && (
            <p className="text-zinc-500 text-[11px] mt-2 leading-relaxed">{ext.description}</p>
          )}
        </div>
        {/* Toggle switch */}
        <button
          onClick={() => onToggle(ext, !ext.enabled)}
          disabled={isPending}
          className={cn(
            'relative inline-flex h-5 w-9 items-center rounded-full transition-colors flex-shrink-0',
            ext.enabled ? 'bg-blue-600' : 'bg-zinc-600',
            isPending && 'opacity-50 cursor-wait'
          )}
        >
          <span
            className={cn(
              'inline-block h-3.5 w-3.5 transform rounded-full bg-white shadow transition-transform',
              ext.enabled ? 'translate-x-5' : 'translate-x-0.5'
            )}
          />
        </button>
      </div>
      {ext.dependencies && ext.dependencies.length > 0 && (
        <div className="mt-2 pt-2 border-t border-zinc-700/50">
          <p className="text-zinc-600 text-[10px]">
            Depends on: {ext.dependencies.join(', ')}
          </p>
        </div>
      )}
    </div>
  )
}

// ─── ExtensionsTab ────────────────────────────────────────────────────────────

interface ExtensionsTabProps {
  selectedVersion: string
}

const EMPTY_EXTENSIONS: PHPExtension[] = []

export default function ExtensionsTab({ selectedVersion }: ExtensionsTabProps) {
  const queryClient = useQueryClient()
  const [search, setSearch] = useState('')
  const [categoryFilter, setCategoryFilter] = useState<string>('all')
  const [pendingToggle, setPendingToggle] = useState<string | null>(null)
  const [confirmAction, setConfirmAction] = useState<{
    ext: PHPExtension; enable: boolean
  } | null>(null)

  const extensionsQuery = useQuery<PHPExtension[]>({
    queryKey: ['php', 'extensions', selectedVersion],
    queryFn: () => api.get<PHPExtension[]>(`/php/extensions/${selectedVersion}`),
  })
  const extensions = extensionsQuery.data ?? EMPTY_EXTENSIONS

  const toggleMutation = useMutation({
    mutationFn: ({ name, enable }: { name: string; enable: boolean }) =>
      api.post(`/php/extensions/${selectedVersion}/${name}/${enable ? 'enable' : 'disable'}`),
    onSuccess: (_d, vars) => {
      queryClient.invalidateQueries({ queryKey: ['php', 'extensions', selectedVersion] })
      toast.success(`Extension ${vars.name} ${vars.enable ? 'enabled' : 'disabled'}`)
      setPendingToggle(null)
    },
    onError: (error: Error, vars) => {
      toast.error(error.message || `Failed to ${vars.enable ? 'enable' : 'disable'} extension`)
      setPendingToggle(null)
    },
  })

  const handleToggle = (ext: PHPExtension, enable: boolean) => {
    // If disabling and has dependents, warn
    if (!enable && ext.dependencies && ext.dependencies.length > 0) {
      setConfirmAction({ ext, enable })
      return
    }
    setPendingToggle(ext.name)
    toggleMutation.mutate({ name: ext.name, enable })
  }

  const categories = useMemo(() => {
    const cats = new Set(extensions.map(e => e.type))
    return ['all', ...Array.from(cats).sort()]
  }, [extensions])

  const filtered = useMemo(() => {
    return extensions.filter(e => {
      if (categoryFilter !== 'all' && e.type !== categoryFilter) return false
      if (search) {
        const q = search.toLowerCase()
        return e.name.toLowerCase().includes(q) || e.description?.toLowerCase().includes(q)
      }
      return true
    })
  }, [extensions, search, categoryFilter])

  const grouped = useMemo(() => {
    const map = new Map<string, PHPExtension[]>()
    filtered.forEach(e => {
      const cat = e.type || 'other'
      if (!map.has(cat)) map.set(cat, [])
      map.get(cat)!.push(e)
    })
    return map
  }, [filtered])

  const enabledCount = extensions.filter(e => e.enabled).length

  if (extensionsQuery.isLoading) {
    return (
      <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-4 gap-3">
        {Array.from({ length: 16 }).map((_, i) => (
          <Skeleton key={i} className="h-24 bg-zinc-800 rounded-xl" />
        ))}
      </div>
    )
  }

  if (extensionsQuery.isError) {
    return (
      <div className="flex flex-col items-center gap-3 rounded-xl border border-red-500/25 bg-red-500/[0.05] p-10 text-center">
        <AlertTriangle className="size-7 text-red-400" />
        <div>
          <p className="text-sm text-red-300">PHP extensions could not be loaded. Extension controls are paused.</p>
          <p className="mt-1 break-words font-mono text-xs text-red-400/70">{extensionsQuery.error.message}</p>
        </div>
        <Button type="button" size="sm" variant="outline" onClick={() => { void extensionsQuery.refetch() }} disabled={extensionsQuery.isFetching}>
          <RefreshCw className={`size-3.5 ${extensionsQuery.isFetching ? 'animate-spin' : ''}`} /> Retry
        </Button>
      </div>
    )
  }

  return (
    <div className="space-y-4">
      {/* Summary */}
      <div className="flex items-center gap-4 text-sm text-zinc-400">
        <span>{enabledCount}/{extensions.length} enabled</span>
        <span className="text-zinc-700">·</span>
        <span>PHP {selectedVersion}</span>
      </div>

      {/* Filters */}
      <div className="flex items-center gap-3 flex-wrap">
        <div className="relative">
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-zinc-500" />
          <Input
            value={search}
            onChange={e => setSearch(e.target.value)}
            placeholder="Search extensions…"
            className="pl-9 bg-zinc-800 border-zinc-700 text-white h-8 w-52"
          />
        </div>
        <div className="flex items-center gap-1.5 flex-wrap">
          {categories.map(cat => (
            <button
              key={cat}
              onClick={() => setCategoryFilter(cat)}
              className={cn(
                'px-2.5 py-1 rounded-md text-xs border transition-colors capitalize',
                categoryFilter === cat
                  ? 'border-blue-500 bg-blue-500/10 text-blue-300'
                  : 'border-zinc-700 text-zinc-400 hover:border-zinc-600'
              )}
            >
              {cat === 'all' ? 'All' : `${CATEGORY_ICONS[cat] ?? ''} ${cat}`}
            </button>
          ))}
        </div>
      </div>

      {/* Grid grouped by category */}
      {categoryFilter === 'all' ? (
        Array.from(grouped.entries()).map(([cat, exts]) => (
          <div key={cat}>
            <div className="flex items-center gap-2 mb-3">
              <span>{CATEGORY_ICONS[cat] ?? '📦'}</span>
              <p className="text-zinc-400 text-xs font-medium uppercase tracking-wider capitalize">{cat}</p>
              <span className="text-zinc-700 text-xs">({exts.length})</span>
            </div>
            <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-3">
              {exts.map(ext => (
                <ExtCard
                  key={ext.name}
                  ext={ext}
                  version={selectedVersion}
                  onToggle={handleToggle}
                  isPending={pendingToggle === ext.name}
                />
              ))}
            </div>
          </div>
        ))
      ) : (
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-3">
          {filtered.map(ext => (
            <ExtCard
              key={ext.name}
              ext={ext}
              version={selectedVersion}
              onToggle={handleToggle}
              isPending={pendingToggle === ext.name}
            />
          ))}
        </div>
      )}

      {/* Confirm disable dialog */}
      <Dialog open={!!confirmAction} onOpenChange={v => !v && setConfirmAction(null)}>
        <DialogContent className="bg-zinc-900 border-zinc-800 text-white max-w-md">
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              <AlertTriangle className="w-4 h-4 text-amber-400" />
              Disable Extension
            </DialogTitle>
            <DialogDescription className="text-zinc-400">
              <span className="font-mono text-zinc-200">{confirmAction?.ext.name}</span> has dependencies.
              Disabling it may break other extensions. Continue?
            </DialogDescription>
          </DialogHeader>
          <DialogFooter className="mt-2">
            <Button variant="ghost" onClick={() => setConfirmAction(null)} className="text-zinc-400">Cancel</Button>
            <Button
              onClick={() => {
                if (confirmAction) {
                  setPendingToggle(confirmAction.ext.name)
                  toggleMutation.mutate({ name: confirmAction.ext.name, enable: confirmAction.enable })
                  setConfirmAction(null)
                }
              }}
              className="bg-amber-600 hover:bg-amber-500 text-white"
            >
              Disable Anyway
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
