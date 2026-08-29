import { useRef, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { CheckCircle2, Download, FileJson, Loader2, Upload } from 'lucide-react'
import { toast } from 'sonner'
import { useCurrentUser } from '@/hooks/useAuth'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'

const MAX_PORTABLE_FILE_BYTES = 128 * 1024

export interface PortableConfigurationBundle {
  schema_version: 1
  exported_at: string
  source_version: string
  settings: Record<string, string>
  warnings?: string[]
}

interface PortableConfigurationChange {
  key: string
  current: string
  proposed: string
}

interface PortableConfigurationPreview {
  schema_version: number
  imported_keys: number
  changed_keys: number
  unchanged_keys: number
  changes: PortableConfigurationChange[]
}

function downloadBundle(bundle: PortableConfigurationBundle) {
  const blob = new Blob([`${JSON.stringify(bundle, null, 2)}\n`], { type: 'application/json' })
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = 'hserver-portable-config-v1.json'
  document.body.appendChild(link)
  link.click()
  link.remove()
  URL.revokeObjectURL(url)
}

function readFile(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result ?? ''))
    reader.onerror = () => reject(reader.error ?? new Error('Could not read the selected file'))
    reader.readAsText(file)
  })
}

export function PortableConfigurationSection() {
  const { data: currentUser } = useCurrentUser()
  const queryClient = useQueryClient()
  const fileInputRef = useRef<HTMLInputElement>(null)
  const [bundle, setBundle] = useState<PortableConfigurationBundle | null>(null)
  const [preview, setPreview] = useState<PortableConfigurationPreview | null>(null)
  const [fileName, setFileName] = useState('')
  const [confirmed, setConfirmed] = useState(false)

  const exportMutation = useMutation({
    mutationFn: () => api.get<PortableConfigurationBundle>('/settings/portable'),
    onSuccess: (exported) => {
      const count = Object.keys(exported.settings).length
      if (count === 0) {
        toast.error('No portable settings are stored yet')
        return
      }
      downloadBundle(exported)
      toast.success(`Downloaded ${count} portable settings`)
      if (exported.warnings?.length) toast.error(`${exported.warnings.length} invalid settings were skipped`)
    },
    onError: (error: Error) => toast.error(`Export failed: ${error.message}`),
  })

  const previewMutation = useMutation({
    mutationFn: (candidate: PortableConfigurationBundle) =>
      api.post<PortableConfigurationPreview>('/settings/portable/preview', candidate),
    onSuccess: (result) => {
      setPreview(result)
      setConfirmed(false)
      toast.success(`Validated ${result.imported_keys} portable settings`)
    },
    onError: (error: Error) => {
      setBundle(null)
      setPreview(null)
      setConfirmed(false)
      toast.error(`File rejected: ${error.message}`)
    },
  })

  const importMutation = useMutation({
    mutationFn: (candidate: PortableConfigurationBundle) =>
      api.post<PortableConfigurationPreview>('/settings/portable/import', { bundle: candidate, confirmed: true }),
    onSuccess: async (result) => {
      toast.success(`Applied ${result.changed_keys} portable settings`)
      setPreview(result)
      setConfirmed(false)
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ['settings', 'system'] }),
        queryClient.invalidateQueries({ queryKey: ['settings', 'mail'] }),
      ])
    },
    onError: (error: Error) => toast.error(`Import failed: ${error.message}`),
  })

  if (currentUser?.role !== 'admin') return null

  async function handleFile(file?: File) {
    if (!file) return
    setFileName(file.name)
    setBundle(null)
    setPreview(null)
    setConfirmed(false)
    if (file.size > MAX_PORTABLE_FILE_BYTES) {
      toast.error('Portable configuration files must be 128 KB or smaller')
      return
    }
    try {
      const parsed = JSON.parse(await readFile(file)) as PortableConfigurationBundle
      setBundle(parsed)
      previewMutation.mutate(parsed)
    } catch {
      toast.error('The selected file is not valid JSON')
    }
  }

  function applyImport() {
    if (!bundle || !preview || !confirmed) return
    if (!window.confirm(`Apply ${preview.changed_keys} portable setting changes from ${fileName}?`)) return
    importMutation.mutate(bundle)
  }

  return (
    <Card className="border-zinc-800 bg-zinc-900">
      <CardHeader className="pb-3">
        <CardTitle className="flex items-center gap-2 text-sm font-medium text-white">
          <FileJson className="size-4 text-zinc-400" />
          Portable Configuration
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-5">
        <div className="flex flex-col justify-between gap-4 sm:flex-row sm:items-start">
          <div className="max-w-3xl">
            <p className="text-sm text-zinc-300">Move safe panel preferences between self-hosted HServer installations with a versioned JSON file.</p>
            <p className="mt-1 text-xs leading-5 text-zinc-500">
              Schema v1 includes the panel label, admin email, notification preferences, mail endpoints, and timezone. It excludes users, passwords, tokens, 2FA, provider credentials, server inventory, audit history, and runtime data.
            </p>
          </div>
          <Button type="button" variant="outline" onClick={() => exportMutation.mutate()} disabled={exportMutation.isPending}>
            {exportMutation.isPending ? <Loader2 className="size-4 animate-spin" /> : <Download className="size-4" />}
            Download JSON
          </Button>
        </div>

        <div className="border-t border-zinc-800 pt-5">
          <input
            ref={fileInputRef}
            type="file"
            accept="application/json,.json"
            aria-label="Portable configuration JSON file"
            className="sr-only"
            onChange={(event) => void handleFile(event.target.files?.[0])}
          />
          <div className="flex flex-col gap-3 sm:flex-row sm:items-center">
            <Button type="button" variant="outline" onClick={() => fileInputRef.current?.click()} disabled={previewMutation.isPending || importMutation.isPending}>
              {previewMutation.isPending ? <Loader2 className="size-4 animate-spin" /> : <Upload className="size-4" />}
              Choose JSON file
            </Button>
            <span className="truncate text-xs text-zinc-500">{fileName || 'No file selected'}</span>
          </div>
        </div>

        {preview && bundle && (
          <div className="space-y-4 rounded-lg border border-blue-500/20 bg-blue-500/[0.05] p-4">
            <div className="flex flex-wrap items-center gap-2 text-xs">
              <span className="flex items-center gap-1.5 font-medium text-blue-200"><CheckCircle2 className="size-3.5" /> Schema v{preview.schema_version} validated</span>
              <span className="rounded bg-zinc-800 px-2 py-1 text-zinc-300">{preview.imported_keys} imported</span>
              <span className="rounded bg-zinc-800 px-2 py-1 text-zinc-300">{preview.changed_keys} changed</span>
              <span className="rounded bg-zinc-800 px-2 py-1 text-zinc-300">{preview.unchanged_keys} unchanged</span>
            </div>

            {preview.changes.length > 0 ? (
              <div className="max-h-64 space-y-2 overflow-y-auto pr-1">
                {preview.changes.map((change) => (
                  <div key={change.key} className="grid gap-2 rounded-md border border-zinc-800 bg-zinc-950/60 p-3 text-xs sm:grid-cols-[11rem_1fr_1fr]">
                    <span className="font-mono text-zinc-300">{change.key}</span>
                    <span className="break-all text-zinc-500">Current: {change.current || 'Not set'}</span>
                    <span className="break-all text-blue-200">New: {change.proposed || 'Clear value'}</span>
                  </div>
                ))}
              </div>
            ) : (
              <p className="text-xs text-zinc-400">This installation already has the same values.</p>
            )}

            <div className="flex flex-col justify-between gap-3 border-t border-zinc-800 pt-4 sm:flex-row sm:items-center">
              <label className="flex cursor-pointer items-start gap-2 text-xs text-zinc-300">
                <input type="checkbox" checked={confirmed} onChange={(event) => setConfirmed(event.target.checked)} className="mt-0.5" />
                Apply only the reviewed schema-v1 settings; keep all other settings unchanged.
              </label>
              <Button type="button" onClick={applyImport} disabled={!confirmed || preview.changed_keys === 0 || importMutation.isPending} className="bg-blue-600 hover:bg-blue-500">
                {importMutation.isPending && <Loader2 className="size-4 animate-spin" />}
                Apply reviewed changes
              </Button>
            </div>
          </div>
        )}
      </CardContent>
    </Card>
  )
}
