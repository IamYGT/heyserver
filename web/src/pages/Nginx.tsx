import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  FileText,
  CheckCircle,
  XCircle,
  ToggleLeft,
  ToggleRight,
  RefreshCw,
  Save,
  Loader2,
  TestTube,
  ChevronRight,
  Terminal,
  Layers,
  Copy,
  Check,
  ChevronDown,
  Plus,
  Archive,
  RotateCcw,
} from 'lucide-react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { Separator } from '@/components/ui/separator'
import { api } from '@/lib/api'
import { toast } from 'sonner'
import { cn } from '@/lib/utils'
import type { NginxArchiveReceipt, NginxArchiveRestoreReceipt, NginxBackupRestoreReceipt, NginxConfig, NginxConfigArchive, NginxConfigBackup, NginxCreateRequest, NginxSaveReceipt, NginxTestResult, NginxSnippet, NginxServiceStatus } from '@/lib/types'
import { DependencyRemediation } from '@/components/DependencyRemediation'
import { NginxCreateDialog } from '@/components/nginx/NginxCreateDialog'

// ─── Snippets Panel ────────────────────────────────────────────────────────────

function CopyButton({ text }: { text: string }) {
  const [copied, setCopied] = useState(false)

  function handleCopy() {
    navigator.clipboard.writeText(text).then(() => {
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    })
  }

  return (
    <button
      onClick={handleCopy}
      title="Copy to clipboard"
      className={cn(
        'flex items-center gap-1 px-2 py-1 rounded text-xs font-medium transition-all',
        copied
          ? 'text-green-400 bg-green-500/10 border border-green-500/20'
          : 'text-zinc-500 hover:text-zinc-200 bg-zinc-800 border border-zinc-700 hover:border-zinc-600',
      )}
    >
      {copied ? (
        <>
          <Check className="w-3 h-3" />
          Copied
        </>
      ) : (
        <>
          <Copy className="w-3 h-3" />
          Copy
        </>
      )}
    </button>
  )
}

function SnippetRow({ snippet }: { snippet: NginxSnippet }) {
  const [expanded, setExpanded] = useState(false)

  return (
    <div className="border border-zinc-800 rounded-lg overflow-hidden">
      <button
        onClick={() => setExpanded((v) => !v)}
        className="w-full flex items-center gap-3 px-4 py-3 bg-zinc-900 hover:bg-zinc-800/70 transition-colors text-left"
      >
        <Layers className="w-3.5 h-3.5 text-zinc-500 flex-shrink-0" />
        <span className="font-mono text-sm text-zinc-200 flex-1 truncate">{snippet.name}</span>
        <ChevronDown
          className={cn('w-3.5 h-3.5 text-zinc-600 transition-transform flex-shrink-0', expanded && 'rotate-180')}
        />
      </button>
      {expanded && (
        <div className="border-t border-zinc-800 bg-zinc-950">
          <div className="flex items-center justify-between px-4 py-2 border-b border-zinc-800/50 bg-zinc-900/50">
            <span className="font-mono text-xs text-zinc-600">{snippet.path}</span>
            <CopyButton text={snippet.content} />
          </div>
          <pre className="text-xs font-mono text-zinc-300 leading-relaxed p-4 overflow-x-auto whitespace-pre">
            {snippet.content}
          </pre>
        </div>
      )}
    </div>
  )
}

function SnippetsPanel() {
  const snippetsQuery = useQuery<NginxSnippet[]>({
    queryKey: ['nginx', 'snippets'],
    queryFn: () => api.get<NginxSnippet[]>('/nginx/snippets'),
  })
  const snippets = snippetsQuery.data

  if (snippetsQuery.isLoading) {
    return (
      <div className="space-y-2">
        {Array.from({ length: 5 }).map((_, i) => (
          <Skeleton key={i} className="h-12 w-full bg-zinc-800/60 rounded-lg" />
        ))}
      </div>
    )
  }

  if (snippetsQuery.isError) {
    return (
      <div className="flex flex-col items-center justify-center py-10 text-center">
        <XCircle className="size-4 text-red-400" />
        <p className="mt-2 text-sm text-red-300">Nginx snippets could not be loaded.</p>
        <p className="mt-1 text-xs text-zinc-600">{snippetsQuery.error.message}</p>
        <Button type="button" variant="outline" size="sm" className="mt-4 border-red-500/30 text-red-200" onClick={() => { void snippetsQuery.refetch() }} disabled={snippetsQuery.isFetching}>
          <RefreshCw className={cn('mr-2 size-3.5', snippetsQuery.isFetching && 'animate-spin')} />Retry
        </Button>
      </div>
    )
  }

  if (!snippets || snippets.length === 0) {
    return (
      <div className="flex flex-col items-center justify-center py-16 text-zinc-600">
        <Layers className="w-8 h-8 mb-3 opacity-30" />
        <p className="text-sm">No snippets found in /etc/nginx/snippets/</p>
      </div>
    )
  }

  return (
    <div className="space-y-2">
      <p className="text-zinc-600 text-xs mb-4">
        {snippets.length} snippet{snippets.length !== 1 ? 's' : ''} — read-only reference. Click to expand, copy to use in your configs.
      </p>
      {snippets.map((s) => (
        <SnippetRow key={s.path} snippet={s} />
      ))}
    </div>
  )
}

// ─── Type Badge ────────────────────────────────────────────────────────────────

function TypeBadge({ type }: { type: string }) {
  const lower = type.toLowerCase()
  if (lower === 'php') {
    return (
      <Badge className="bg-purple-500/15 text-purple-300 border-purple-500/20 text-[10px] px-1.5 py-0 font-medium">
        PHP
      </Badge>
    )
  }
  if (lower === 'proxy') {
    return (
      <Badge className="bg-blue-500/15 text-blue-300 border-blue-500/20 text-[10px] px-1.5 py-0 font-medium">
        Proxy
      </Badge>
    )
  }
  return (
    <Badge className="bg-zinc-700/40 text-zinc-400 border-zinc-600/30 text-[10px] px-1.5 py-0 font-medium">
      {type || 'Static'}
    </Badge>
  )
}

type NginxTab = 'configs' | 'archives' | 'backups' | 'snippets'

export default function Nginx() {
  const queryClient = useQueryClient()
  const [activeTab, setActiveTab] = useState<NginxTab>('configs')
  const [selectedConfig, setSelectedConfig] = useState<NginxConfig | null>(null)
  const [editContent, setEditContent] = useState<string>('')
  const [isDirty, setIsDirty] = useState(false)
  const [testResult, setTestResult] = useState<NginxTestResult | null>(null)
  const [showTestOutput, setShowTestOutput] = useState(false)
  const [showCreate, setShowCreate] = useState(false)

  const configsQuery = useQuery<NginxConfig[]>({
    queryKey: ['nginx', 'configs'],
    queryFn: () => api.get<NginxConfig[]>('/nginx/configs'),
  })
  const configs = configsQuery.data

  const archivesQuery = useQuery<NginxConfigArchive[]>({
    queryKey: ['nginx', 'archives'],
    queryFn: () => api.get<NginxConfigArchive[]>('/nginx/archives'),
    enabled: activeTab === 'archives',
  })
  const archives = archivesQuery.data

  const backupsQuery = useQuery<NginxConfigBackup[]>({
    queryKey: ['nginx', 'backups'],
    queryFn: () => api.get<NginxConfigBackup[]>('/nginx/backups'),
    enabled: activeTab === 'backups',
  })
  const backups = backupsQuery.data

  const statusQuery = useQuery<NginxServiceStatus>({
    queryKey: ['nginx', 'status'],
    queryFn: () => api.get<NginxServiceStatus>('/nginx/status'),
    refetchInterval: 30_000,
  })
  const nginxStatus = statusQuery.data
  const nginxInstalled = nginxStatus?.installed === true
  const nginxConfigReady = statusQuery.isSuccess && nginxInstalled && nginxStatus.statusAvailable
  const nginxActive = nginxConfigReady && nginxStatus.status === 'active'

  const testMutation = useMutation({
    mutationFn: () => api.post<NginxTestResult>('/nginx/test'),
    onSuccess: (result) => {
      setTestResult(result)
      setShowTestOutput(true)
      if (result.ok) {
        toast.success('nginx -t passed')
      } else {
        toast.error('nginx -t failed — see output below')
      }
    },
    onError: () => toast.error('Failed to run nginx -t'),
  })

  const reloadMutation = useMutation({
    mutationFn: async () => {
      const testRes = await api.post<NginxTestResult>('/nginx/test')
      setTestResult(testRes)
      setShowTestOutput(true)
      if (!testRes.ok) {
        throw new Error('nginx config test failed — reload aborted')
      }
      return api.post('/nginx/reload')
    },
    onSuccess: () => toast.success('Nginx reloaded successfully'),
    onError: (err: Error) => toast.error(err.message || 'Failed to reload nginx'),
  })

  const createMutation = useMutation({
    mutationFn: (request: NginxCreateRequest) => api.post<NginxConfig>('/nginx/configs', request),
    onSuccess: (config) => {
      setShowCreate(false)
      setActiveTab('configs')
      setSelectedConfig(config)
      setEditContent(config.content ?? '')
      setIsDirty(false)
      queryClient.invalidateQueries({ queryKey: ['nginx', 'configs'] })
      toast.success('Nginx site created and validated')
    },
    onError: (error: Error) => toast.error(error.message || 'Failed to create Nginx site'),
  })

  const saveMutation = useMutation({
    mutationFn: ({ path, content, checksum }: { path: string; content: string; checksum: string }) =>
      api.put<NginxSaveReceipt>(`/nginx/configs/${encodeURIComponent(path)}`, { content, checksum }),
    onSuccess: (receipt, variables) => {
      toast.success('Config saved')
      setIsDirty(false)
      setSelectedConfig((current) => current ? { ...current, content: variables.content, checksum: receipt.checksum } : current)
      queryClient.invalidateQueries({ queryKey: ['nginx', 'configs'] })
    },
    onError: (error: Error) => toast.error(error.message || 'Failed to save config'),
  })

  const toggleMutation = useMutation({
    mutationFn: ({ config, enabled }: { config: NginxConfig; enabled: boolean }) =>
      api.put<{ isEnabled: boolean }>(`/nginx/configs/${encodeURIComponent(config.filename)}/state`, { enabled }),
    onSuccess: (result) => {
      queryClient.invalidateQueries({ queryKey: ['nginx', 'configs'] })
      setSelectedConfig((current) => current ? { ...current, isEnabled: result.isEnabled } : current)
      toast.success(result.isEnabled ? 'Config enabled' : 'Config disabled')
    },
    onError: () => toast.error('Failed to toggle config'),
  })

  const archiveMutation = useMutation({
    mutationFn: (config: NginxConfig) =>
      api.delete<NginxArchiveReceipt>(`/nginx/configs/${encodeURIComponent(config.filename)}`, { checksum: config.checksum }),
    onSuccess: () => {
      setSelectedConfig(null)
      setEditContent('')
      setIsDirty(false)
      queryClient.invalidateQueries({ queryKey: ['nginx', 'configs'] })
      toast.success('Config archived; recovery copy retained')
    },
    onError: (error: Error) => toast.error(error.message || 'Failed to archive config'),
  })

  const restoreArchiveMutation = useMutation({
    mutationFn: (archive: NginxConfigArchive) =>
      api.post<NginxArchiveRestoreReceipt>(`/nginx/archives/${encodeURIComponent(archive.archive)}/restore`, { checksum: archive.checksum }),
    onSuccess: (receipt) => {
      queryClient.invalidateQueries({ queryKey: ['nginx', 'archives'] })
      queryClient.invalidateQueries({ queryKey: ['nginx', 'configs'] })
      setActiveTab('configs')
      toast.success(`${receipt.filename} restored disabled; archive retained`)
    },
    onError: (error: Error) => toast.error(error.message || 'Failed to restore Nginx config archive'),
  })

  const restoreBackupMutation = useMutation({
    mutationFn: async (backup: NginxConfigBackup) => {
      const current = await api.get<NginxConfig>(`/nginx/configs/${encodeURIComponent(backup.filename)}`)
      if (current.filename !== backup.filename || !current.checksum) {
        throw new Error('Current Nginx config identity or checksum could not be verified')
      }
      return api.post<NginxBackupRestoreReceipt>(`/nginx/backups/${encodeURIComponent(backup.backup)}/restore`, {
        backupChecksum: backup.checksum,
        currentChecksum: current.checksum,
      })
    },
    onSuccess: (receipt) => {
      queryClient.invalidateQueries({ queryKey: ['nginx', 'backups'] })
      queryClient.invalidateQueries({ queryKey: ['nginx', 'configs'] })
      setSelectedConfig(null)
      setEditContent('')
      setIsDirty(false)
      setActiveTab('configs')
      toast.success(`${receipt.filename} rolled back and validated; pre-restore recovery retained`)
    },
    onError: (error: Error) => toast.error(error.message || 'Failed to roll back Nginx config'),
  })

  const handleSelectConfig = async (config: NginxConfig) => {
    if (isDirty) {
      const confirmed = window.confirm('You have unsaved changes. Discard them?')
      if (!confirmed) return
    }
    try {
      const full = await api.get<NginxConfig>(`/nginx/configs/${encodeURIComponent(config.filename)}`)
      setSelectedConfig(full)
      setEditContent(full.content ?? '')
      setIsDirty(false)
    } catch (error) {
      toast.error(error instanceof Error ? error.message : 'Failed to load nginx config')
    }
  }

  const handleContentChange = (value: string) => {
    setEditContent(value)
    setIsDirty(true)
  }

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between flex-wrap gap-3">
        <div>
          <h2 className="text-white text-xl font-bold">Nginx Configuration</h2>
          <p className="text-zinc-500 text-sm mt-0.5">
            {configsQuery.isError ? 'Nginx config inventory unavailable' : configs ? `${configs.length} config files` : 'Manage nginx virtual hosts'}
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button
            variant="outline"
            size="sm"
            onClick={() => setShowCreate(true)}
            disabled={createMutation.isPending || configsQuery.isLoading || configsQuery.isError || !nginxConfigReady}
            className="border-zinc-700 text-zinc-300 hover:bg-zinc-800 hover:text-white"
          >
            <Plus className="mr-1.5 size-3.5" />
            Create Site
          </Button>
          <Button
            variant="outline"
            size="sm"
            onClick={() => testMutation.mutate()}
            disabled={testMutation.isPending || statusQuery.isLoading || statusQuery.isError || !nginxInstalled}
            className="border-zinc-700 text-zinc-300 hover:text-white hover:bg-zinc-800"
          >
            {testMutation.isPending ? (
              <Loader2 className="w-3.5 h-3.5 mr-1.5 animate-spin" />
            ) : (
              <TestTube className="w-3.5 h-3.5 mr-1.5" />
            )}
            Test Config
          </Button>
          <Button
            size="sm"
            onClick={() => reloadMutation.mutate()}
            disabled={reloadMutation.isPending || configsQuery.isLoading || configsQuery.isError || !nginxActive}
            className="bg-blue-600 hover:bg-blue-500 text-white"
          >
            {reloadMutation.isPending ? (
              <Loader2 className="w-3.5 h-3.5 mr-1.5 animate-spin" />
            ) : (
              <RefreshCw className="w-3.5 h-3.5 mr-1.5" />
            )}
            Reload
          </Button>
        </div>
      </div>

      {statusQuery.isError ? (
        <DependencyRemediation
          title="Nginx service status is unavailable"
          summary="HServer could not determine whether nginx is installed or active. Test, reload, and config mutations remain paused."
          state="unavailable"
          steps={[
            <>Run the packaged HServer doctor and inspect the HServer service logs.</>,
            <>Verify <code>nginx -v</code> and <code>systemctl is-active nginx</code> on the local host.</>,
            <>Correct binary or systemd access, then retry detection.</>,
          ]}
          error={statusQuery.error.message}
          retry={() => { void statusQuery.refetch() }}
          retrying={statusQuery.isFetching}
        />
      ) : nginxStatus && !nginxStatus.installed ? (
        <DependencyRemediation
          title="Nginx is not installed"
          summary="HServer did not find an nginx executable. Nginx management stays disabled; HServer never installs host packages automatically."
          state="not-configured"
          steps={[
            <>Install <code>nginx</code> from the supported Ubuntu repositories.</>,
            <>Verify <code>nginx -v</code>, then enable and start <code>nginx.service</code>.</>,
            <>Run <code>nginx -t</code> and retry detection.</>,
          ]}
          retry={() => { void statusQuery.refetch() }}
          retrying={statusQuery.isFetching}
        />
      ) : nginxStatus && !nginxStatus.statusAvailable ? (
        <DependencyRemediation
          title="Nginx service state is unavailable"
          summary="The nginx binary is installed, but HServer could not inspect its systemd unit. Reload and config mutations remain paused."
          state="unavailable"
          steps={[
            <>Verify <code>systemctl is-active nginx</code> works on the local host.</>,
            <>Inspect <code>systemctl status nginx</code> and the HServer service logs.</>,
            <>Restore systemd access or the nginx unit, then retry detection.</>,
          ]}
          retry={() => { void statusQuery.refetch() }}
          retrying={statusQuery.isFetching}
        />
      ) : nginxStatus && nginxStatus.status !== 'active' ? (
        <DependencyRemediation
          title="Nginx is not active"
          summary={`The nginx binary is installed, but systemd reports ${nginxStatus.status}. Config inspection remains available; reload is paused.`}
          state="stopped"
          steps={[
            <>Inspect <code>systemctl status nginx</code> and <code>journalctl -u nginx</code>.</>,
            <>Run <code>nginx -t</code> and correct any reported configuration error.</>,
            <>Start <code>nginx.service</code>, then retry detection.</>,
          ]}
          error={nginxStatus.configTest.ok ? undefined : nginxStatus.configTest.output}
          retry={() => { void statusQuery.refetch() }}
          retrying={statusQuery.isFetching}
        />
      ) : null}

      {/* Test output panel */}
      {showTestOutput && testResult && (
        <Card className="bg-zinc-950 border-zinc-800">
          <CardHeader className="pb-2 pt-3 px-4">
            <div className="flex items-center justify-between">
              <div className="flex items-center gap-2">
                <Terminal className="w-4 h-4 text-zinc-400" />
                <CardTitle className="text-sm font-medium text-zinc-300">
                  nginx -t output
                </CardTitle>
                {testResult.ok ? (
                  <Badge className="bg-green-500/15 text-green-400 border-green-500/20 text-[10px] px-1.5 py-0">
                    PASS
                  </Badge>
                ) : (
                  <Badge className="bg-red-500/15 text-red-400 border-red-500/20 text-[10px] px-1.5 py-0">
                    FAIL
                  </Badge>
                )}
              </div>
              <button
                onClick={() => setShowTestOutput(false)}
                className="text-zinc-600 hover:text-zinc-400 text-xs"
              >
                dismiss
              </button>
            </div>
          </CardHeader>
          <CardContent className="px-4 pb-4">
            <pre className="text-xs font-mono text-zinc-400 bg-black/40 rounded-md p-3 overflow-x-auto whitespace-pre-wrap">
              {testResult.output || '(no output)'}
            </pre>
          </CardContent>
        </Card>
      )}

      {/* Tabs */}
      <div className="flex items-center gap-1 border-b border-zinc-800">
        <button
          onClick={() => setActiveTab('configs')}
          className={cn(
            'flex items-center gap-1.5 px-3 py-2 text-sm font-medium border-b-2 transition-colors -mb-px',
            activeTab === 'configs'
              ? 'border-blue-500 text-blue-400'
              : 'border-transparent text-zinc-500 hover:text-zinc-300',
          )}
        >
          <FileText className="w-3.5 h-3.5" />
          Config Files
        </button>
        <button
          onClick={() => setActiveTab('archives')}
          className={cn(
            'flex items-center gap-1.5 px-3 py-2 text-sm font-medium border-b-2 transition-colors -mb-px',
            activeTab === 'archives'
              ? 'border-blue-500 text-blue-400'
              : 'border-transparent text-zinc-500 hover:text-zinc-300',
          )}
        >
          <Archive className="w-3.5 h-3.5" />
          Archives
        </button>
        <button
          onClick={() => setActiveTab('backups')}
          className={cn(
            'flex items-center gap-1.5 px-3 py-2 text-sm font-medium border-b-2 transition-colors -mb-px',
            activeTab === 'backups'
              ? 'border-blue-500 text-blue-400'
              : 'border-transparent text-zinc-500 hover:text-zinc-300',
          )}
        >
          <RotateCcw className="w-3.5 h-3.5" />
          Edit Backups
        </button>
        <button
          onClick={() => setActiveTab('snippets')}
          className={cn(
            'flex items-center gap-1.5 px-3 py-2 text-sm font-medium border-b-2 transition-colors -mb-px',
            activeTab === 'snippets'
              ? 'border-blue-500 text-blue-400'
              : 'border-transparent text-zinc-500 hover:text-zinc-300',
          )}
        >
          <Layers className="w-3.5 h-3.5" />
          Snippets
        </button>
      </div>

      {/* Configs tab */}
      {activeTab === 'configs' && (
        <div className="grid grid-cols-1 lg:grid-cols-3 gap-4">
          {/* Config list */}
          <Card className="bg-zinc-900 border-zinc-800">
            <CardHeader className="pb-2 pt-4 px-4">
              <CardTitle className="text-white text-sm font-medium">Config Files</CardTitle>
            </CardHeader>
            <CardContent className="px-2 pb-4">
              {configsQuery.isLoading ? (
                <div className="space-y-1">
                  {Array.from({ length: 8 }).map((_, i) => (
                    <Skeleton key={i} className="h-10 w-full bg-zinc-800 mx-2" />
                  ))}
                </div>
              ) : configsQuery.isError ? (
                <div className="flex flex-col items-center justify-center px-3 py-10 text-center">
                  <XCircle className="size-4 text-red-400" />
                  <p className="mt-2 text-xs text-red-300">Nginx configs could not be loaded. Mutating controls are paused.</p>
                  <p className="mt-1 break-all text-[11px] text-zinc-600">{configsQuery.error.message}</p>
                  <Button type="button" variant="outline" size="sm" className="mt-4 border-red-500/30 text-red-200" onClick={() => { void configsQuery.refetch() }} disabled={configsQuery.isFetching}>
                    <RefreshCw className={cn('mr-2 size-3.5', configsQuery.isFetching && 'animate-spin')} />Retry
                  </Button>
                </div>
              ) : configs && configs.length > 0 ? (
                <div className="space-y-0.5">
                  {configs.map((config) => (
                    <button
                      key={config.filename}
                      onClick={() => handleSelectConfig(config)}
                      className={`w-full flex items-center gap-2.5 px-3 py-2.5 rounded-lg text-left transition-colors group ${
                        selectedConfig?.filename === config.filename
                          ? 'bg-blue-600/10 border border-blue-600/20'
                          : 'hover:bg-zinc-800 border border-transparent'
                      }`}
                    >
                      {config.isEnabled ? (
                        <CheckCircle className="w-3.5 h-3.5 text-green-400 flex-shrink-0" />
                      ) : (
                        <XCircle className="w-3.5 h-3.5 text-zinc-600 flex-shrink-0" />
                      )}
                      <span className={`text-xs flex-1 truncate font-mono ${
                        selectedConfig?.filename === config.filename ? 'text-blue-300' : 'text-zinc-300'
                      }`}>
                        {config.filename}
                      </span>
                      <TypeBadge type={config.type} />
                      <ChevronRight className="w-3 h-3 text-zinc-600 flex-shrink-0 opacity-0 group-hover:opacity-100" />
                    </button>
                  ))}
                </div>
              ) : (
                <p className="text-zinc-600 text-sm text-center py-8">No configs found</p>
              )}
            </CardContent>
          </Card>

          {/* Editor */}
          <div className="lg:col-span-2 space-y-3">
            {selectedConfig ? (
              <>
                <div className="flex items-center justify-between flex-wrap gap-2">
                  <div className="flex items-center gap-2">
                    <FileText className="w-4 h-4 text-blue-400" />
                    <span className="text-white text-sm font-mono">{selectedConfig.filename}</span>
                    <TypeBadge type={selectedConfig.type} />
                    {selectedConfig.isEnabled ? (
                      <Badge className="bg-green-500/15 text-green-400 border-green-500/20 text-[10px] px-1.5 py-0">
                        enabled
                      </Badge>
                    ) : (
                      <Badge className="bg-zinc-700/40 text-zinc-500 border-zinc-600/30 text-[10px] px-1.5 py-0">
                        disabled
                      </Badge>
                    )}
                    {isDirty && (
                      <Badge variant="outline" className="border-amber-500/30 text-amber-400 text-xs">
                        Unsaved
                      </Badge>
                    )}
                  </div>
                  <div className="flex items-center gap-2">
                    <Button
                      variant="ghost"
                      size="sm"
                      title={selectedConfig.isEnabled ? 'Disable this site before archiving it' : isDirty ? 'Save or discard unsaved changes before archiving' : 'Archive config without deleting the document root'}
                      onClick={() => {
                        if (window.confirm(`Archive ${selectedConfig.filename}? The recovery copy is retained and the document root is not deleted.`)) {
                          archiveMutation.mutate(selectedConfig)
                        }
                      }}
                      disabled={archiveMutation.isPending || selectedConfig.isEnabled || isDirty || !selectedConfig.checksum || configsQuery.isError || !nginxConfigReady}
                      className="text-zinc-500 hover:bg-red-500/10 hover:text-red-300"
                    >
                      {archiveMutation.isPending ? <Loader2 className="mr-1.5 size-4 animate-spin" /> : <Archive className="mr-1.5 size-4" />}
                      Archive
                    </Button>
                    <Separator orientation="vertical" className="h-5 bg-zinc-800" />
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => toggleMutation.mutate({ config: selectedConfig, enabled: !selectedConfig.isEnabled })}
                      disabled={toggleMutation.isPending || configsQuery.isError || !nginxConfigReady}
                      className="text-zinc-400 hover:text-white"
                    >
                      {toggleMutation.isPending ? (
                        <Loader2 className="w-4 h-4 mr-1.5 animate-spin" />
                      ) : selectedConfig.isEnabled ? (
                        <>
                          <ToggleRight className="w-4 h-4 mr-1.5 text-green-400" />
                          Disable
                        </>
                      ) : (
                        <>
                          <ToggleLeft className="w-4 h-4 mr-1.5 text-zinc-500" />
                          Enable
                        </>
                      )}
                    </Button>
                    <Separator orientation="vertical" className="h-5 bg-zinc-800" />
                    <Button
                      size="sm"
                      onClick={() => saveMutation.mutate({ path: selectedConfig.filename, content: editContent, checksum: selectedConfig.checksum ?? '' })}
                      disabled={!isDirty || !selectedConfig.checksum || saveMutation.isPending || configsQuery.isError || !nginxConfigReady}
                      className="bg-blue-600 hover:bg-blue-500 text-white disabled:opacity-50"
                    >
                      {saveMutation.isPending ? (
                        <Loader2 className="w-3.5 h-3.5 mr-1.5 animate-spin" />
                      ) : (
                        <Save className="w-3.5 h-3.5 mr-1.5" />
                      )}
                      Save
                    </Button>
                  </div>
                </div>
                <div className="bg-zinc-950 border border-zinc-800 rounded-lg overflow-hidden">
                  <div className="flex items-center justify-between px-4 py-2 border-b border-zinc-800 bg-zinc-900">
                    <span className="text-zinc-500 text-xs font-mono">{selectedConfig.filename}</span>
                    {selectedConfig.domain && (
                      <span className="text-zinc-600 text-xs">{selectedConfig.domain}</span>
                    )}
                  </div>
                  <textarea
                    value={editContent}
                    onChange={(e) => handleContentChange(e.target.value)}
                    className="w-full bg-zinc-950 text-zinc-200 font-mono text-xs leading-relaxed p-4 resize-none focus:outline-none min-h-96 h-[60vh]"
                    spellCheck={false}
                  />
                </div>
              </>
            ) : (
              <div className="flex flex-col items-center justify-center h-64 text-zinc-600">
                <FileText className="w-8 h-8 mb-3 opacity-50" />
                <p className="text-sm">Select a config file to view or edit</p>
              </div>
            )}
          </div>
        </div>
      )}

      {/* Archives tab */}
      {activeTab === 'archives' && (
        <Card className="bg-zinc-900 border-zinc-800">
          <CardHeader className="pb-2 pt-4 px-5">
            <div className="flex items-center justify-between gap-3">
              <div>
                <CardTitle className="text-white text-sm font-medium">Archived Configs</CardTitle>
                <p className="mt-1 text-xs text-zinc-600">Recovery copies stay disabled, never overwrite an existing config, and do not reload Nginx.</p>
              </div>
              <Button type="button" variant="outline" size="sm" onClick={() => { void archivesQuery.refetch() }} disabled={archivesQuery.isFetching} className="border-zinc-700 text-zinc-300">
                <RefreshCw className={cn('mr-2 size-3.5', archivesQuery.isFetching && 'animate-spin')} />Refresh
              </Button>
            </div>
          </CardHeader>
          <CardContent className="px-5 pb-5">
            {archivesQuery.isLoading ? (
              <div className="space-y-2">
                {Array.from({ length: 4 }).map((_, index) => <Skeleton key={index} className="h-16 w-full bg-zinc-800/60" />)}
              </div>
            ) : archivesQuery.isError ? (
              <div className="flex flex-col items-center justify-center py-10 text-center">
                <XCircle className="size-4 text-red-400" />
                <p className="mt-2 text-sm text-red-300">Nginx archive inventory could not be loaded.</p>
                <p className="mt-1 break-all text-xs text-zinc-600">{archivesQuery.error.message}</p>
                <Button type="button" variant="outline" size="sm" className="mt-4 border-red-500/30 text-red-200" onClick={() => { void archivesQuery.refetch() }} disabled={archivesQuery.isFetching}>
                  <RefreshCw className={cn('mr-2 size-3.5', archivesQuery.isFetching && 'animate-spin')} />Retry
                </Button>
              </div>
            ) : archives && archives.length > 0 ? (
              <div className="space-y-2">
                {archives.map((archive) => {
                  const restoring = restoreArchiveMutation.isPending && restoreArchiveMutation.variables?.archive === archive.archive
                  return (
                    <div key={archive.archive} className="flex flex-col gap-3 rounded-lg border border-zinc-800 bg-zinc-950/50 px-4 py-3 sm:flex-row sm:items-center">
                      <Archive className="size-4 flex-shrink-0 text-zinc-500" />
                      <div className="min-w-0 flex-1">
                        <p className="truncate font-mono text-sm text-zinc-200">{archive.filename}</p>
                        <p className="mt-1 truncate font-mono text-[11px] text-zinc-600">{archive.archive}</p>
                        <p className="mt-1 text-[11px] text-zinc-500">
                          {new Date(archive.archivedAt).toLocaleString()} · {archive.size.toLocaleString()} bytes · {archive.checksum.slice(0, 12)}…
                        </p>
                      </div>
                      <Button
                        type="button"
                        variant="outline"
                        size="sm"
                        disabled={restoreArchiveMutation.isPending || archivesQuery.isError || !nginxConfigReady}
                        onClick={() => {
                          if (window.confirm(`Restore ${archive.filename} from ${archive.archive}? Restore is refused if the config already exists. The site remains disabled, the archive is retained, and Nginx is not reloaded.`)) {
                            restoreArchiveMutation.mutate(archive)
                          }
                        }}
                        className="border-zinc-700 text-zinc-300 hover:bg-blue-500/10 hover:text-blue-300"
                      >
                        {restoring ? <Loader2 className="mr-2 size-3.5 animate-spin" /> : <RotateCcw className="mr-2 size-3.5" />}
                        Restore disabled
                      </Button>
                    </div>
                  )
                })}
              </div>
            ) : (
              <div className="flex flex-col items-center justify-center py-16 text-zinc-600">
                <Archive className="mb-3 size-8 opacity-30" />
                <p className="text-sm">No archived configs</p>
                <p className="mt-1 text-xs">Archiving a disabled config creates a recovery copy here.</p>
              </div>
            )}
          </CardContent>
        </Card>
      )}

      {/* Edit backups tab */}
      {activeTab === 'backups' && (
        <Card className="bg-zinc-900 border-zinc-800">
          <CardHeader className="pb-2 pt-4 px-5">
            <div className="flex items-center justify-between gap-3">
              <div>
                <CardTitle className="text-white text-sm font-medium">Edit Backups</CardTitle>
                <p className="mt-1 text-xs text-zinc-600">Rollback requires fresh checksums for both the selected backup and current config. A new pre-restore recovery is retained.</p>
              </div>
              <Button type="button" variant="outline" size="sm" onClick={() => { void backupsQuery.refetch() }} disabled={backupsQuery.isFetching} className="border-zinc-700 text-zinc-300">
                <RefreshCw className={cn('mr-2 size-3.5', backupsQuery.isFetching && 'animate-spin')} />Refresh
              </Button>
            </div>
          </CardHeader>
          <CardContent className="px-5 pb-5">
            {backupsQuery.isLoading ? (
              <div className="space-y-2">
                {Array.from({ length: 4 }).map((_, index) => <Skeleton key={index} className="h-16 w-full bg-zinc-800/60" />)}
              </div>
            ) : backupsQuery.isError ? (
              <div className="flex flex-col items-center justify-center py-10 text-center">
                <XCircle className="size-4 text-red-400" />
                <p className="mt-2 text-sm text-red-300">Nginx edit backup inventory could not be loaded.</p>
                <p className="mt-1 break-all text-xs text-zinc-600">{backupsQuery.error.message}</p>
                <Button type="button" variant="outline" size="sm" className="mt-4 border-red-500/30 text-red-200" onClick={() => { void backupsQuery.refetch() }} disabled={backupsQuery.isFetching}>
                  <RefreshCw className={cn('mr-2 size-3.5', backupsQuery.isFetching && 'animate-spin')} />Retry
                </Button>
              </div>
            ) : backups && backups.length > 0 ? (
              <div className="space-y-2">
                {backups.map((backup) => {
                  const restoring = restoreBackupMutation.isPending && restoreBackupMutation.variables?.backup === backup.backup
                  return (
                    <div key={backup.backup} className="flex flex-col gap-3 rounded-lg border border-zinc-800 bg-zinc-950/50 px-4 py-3 sm:flex-row sm:items-center">
                      <RotateCcw className="size-4 flex-shrink-0 text-zinc-500" />
                      <div className="min-w-0 flex-1">
                        <p className="truncate font-mono text-sm text-zinc-200">{backup.filename}</p>
                        <p className="mt-1 truncate font-mono text-[11px] text-zinc-600">{backup.backup}</p>
                        <p className="mt-1 text-[11px] text-zinc-500">
                          {new Date(backup.createdAt).toLocaleString()} · {backup.size.toLocaleString()} bytes · {backup.checksum.slice(0, 12)}…
                        </p>
                      </div>
                      <Button
                        type="button"
                        variant="outline"
                        size="sm"
                        disabled={restoreBackupMutation.isPending || backupsQuery.isError || !nginxConfigReady}
                        onClick={() => {
                          if (window.confirm(`Roll back ${backup.filename} from ${backup.backup}? HServer will re-read the current config, require both checksums, retain a new pre-restore recovery, validate with nginx -t, and will not reload Nginx.`)) {
                            restoreBackupMutation.mutate(backup)
                          }
                        }}
                        className="border-zinc-700 text-zinc-300 hover:bg-amber-500/10 hover:text-amber-300"
                      >
                        {restoring ? <Loader2 className="mr-2 size-3.5 animate-spin" /> : <RotateCcw className="mr-2 size-3.5" />}
                        Roll back config
                      </Button>
                    </div>
                  )
                })}
              </div>
            ) : (
              <div className="flex flex-col items-center justify-center py-16 text-zinc-600">
                <RotateCcw className="mb-3 size-8 opacity-30" />
                <p className="text-sm">No edit backups</p>
                <p className="mt-1 text-xs">Saving a changed config creates a pre-edit backup here.</p>
              </div>
            )}
          </CardContent>
        </Card>
      )}

      {/* Snippets tab */}
      {activeTab === 'snippets' && (
        <Card className="bg-zinc-900 border-zinc-800">
          <CardHeader className="pb-2 pt-4 px-5">
            <div className="flex items-center gap-2">
              <Layers className="w-4 h-4 text-zinc-400" />
              <CardTitle className="text-white text-sm font-medium">Nginx Snippets</CardTitle>
            </div>
            <p className="text-zinc-600 text-xs mt-1">
              Shared config blocks from <span className="font-mono">/etc/nginx/snippets/</span>
            </p>
          </CardHeader>
          <CardContent className="px-5 pb-5">
            <SnippetsPanel />
          </CardContent>
        </Card>
      )}

      {showCreate && (
        <NginxCreateDialog
          open
          pending={createMutation.isPending}
          onOpenChange={setShowCreate}
          onSubmit={(request) => createMutation.mutate(request)}
        />
      )}
    </div>
  )
}
