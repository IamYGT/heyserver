import { useState, useCallback } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import Editor from '@monaco-editor/react'
import {
  Folder,
  File,
  FolderOpen,
  ChevronRight,
  Home,
  Plus,
  Trash2,
  Loader2,
  AlertTriangle,
  Save,
  X,
  RefreshCw,
  PencilLine,
  FileText,
  FileCog,
  FileCode,
} from 'lucide-react'
import { Card, CardContent } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from '@/components/ui/dialog'
import { api } from '@/lib/api'
import { activeFileRoot, fileRootLabel } from '@/lib/fileRoots'
import { toast } from 'sonner'
import { cn } from '@/lib/utils'
import { DependencyRemediation } from '@/components/DependencyRemediation'

// ─── Types ─────────────────────────────────────────────────────────────────────

interface FileEntry {
  name: string
  path: string
  type: string // "file" | "directory" | "symlink"
  size: number
  permissions: string
  owner: string
  group: string
  modified: string
}

function isDir(entry: FileEntry): boolean {
  return entry.type === 'directory'
}

interface FileListResponse {
  path: string
  entries: FileEntry[]
}

interface FileRootsResponse {
  roots: string[]
  entries: FileEntry[]
}

interface FileReadResponse {
  path: string
  content: string
}

// ─── Helpers ───────────────────────────────────────────────────────────────────

function formatSize(bytes: number): string {
  if (bytes === 0) return '—'
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
  return `${(bytes / (1024 * 1024 * 1024)).toFixed(2)} GB`
}

function formatDate(iso: string): string {
  try {
    return new Date(iso).toLocaleString('en-GB', {
      day: '2-digit',
      month: 'short',
      year: 'numeric',
      hour: '2-digit',
      minute: '2-digit',
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
    ts: 'typescript',
    tsx: 'typescript',
    js: 'javascript',
    jsx: 'javascript',
    json: 'json',
    html: 'html',
    htm: 'html',
    css: 'css',
    scss: 'scss',
    php: 'php',
    py: 'python',
    go: 'go',
    sh: 'shell',
    bash: 'shell',
    zsh: 'shell',
    conf: 'nginx',
    nginx: 'nginx',
    yaml: 'yaml',
    yml: 'yaml',
    toml: 'toml',
    xml: 'xml',
    sql: 'sql',
    md: 'markdown',
    env: 'dotenv',
    ini: 'ini',
    log: 'plaintext',
    txt: 'plaintext',
  }
  return map[ext] ?? 'plaintext'
}

function FileIcon({ entry }: { entry: FileEntry }) {
  if (isDir(entry)) return <Folder className="w-4 h-4 text-blue-400 flex-shrink-0" />
  const ext = entry.name.split('.').pop()?.toLowerCase() ?? ''
  if (['conf', 'nginx', 'ini', 'env'].includes(ext))
    return <FileCog className="w-4 h-4 text-amber-400 flex-shrink-0" />
  if (['ts', 'tsx', 'js', 'jsx', 'php', 'go', 'py', 'sh'].includes(ext))
    return <FileCode className="w-4 h-4 text-green-400 flex-shrink-0" />
  if (['log', 'txt', 'md'].includes(ext))
    return <FileText className="w-4 h-4 text-zinc-400 flex-shrink-0" />
  return <File className="w-4 h-4 text-zinc-500 flex-shrink-0" />
}

// ─── Quick Shortcuts ───────────────────────────────────────────────────────────

// ─── Breadcrumbs ───────────────────────────────────────────────────────────────

interface BreadcrumbsProps {
  path: string
  root: string
  onNavigate: (path: string) => void
}

function Breadcrumbs({ path, root, onNavigate }: BreadcrumbsProps) {
  const relativePath = path === root ? '' : path.slice(root.length).replace(/^\//, '')
  const parts = relativePath.split('/').filter(Boolean)

  return (
    <div className="flex items-center gap-1 flex-wrap text-xs min-w-0">
      <button
        onClick={() => onNavigate(root)}
        className="text-zinc-400 hover:text-white transition-colors flex items-center gap-1"
      >
        <Home className="w-3.5 h-3.5" />
      </button>
      {parts.map((part, idx) => {
        const segmentPath = `${root}/${parts.slice(0, idx + 1).join('/')}`
        const isLast = idx === parts.length - 1
        return (
          <span key={segmentPath} className="flex items-center gap-1">
            <ChevronRight className="w-3 h-3 text-zinc-600" />
            {isLast ? (
              <span className="text-white font-medium">{part}</span>
            ) : (
              <button
                onClick={() => onNavigate(segmentPath)}
                className="text-zinc-400 hover:text-white transition-colors"
              >
                {part}
              </button>
            )}
          </span>
        )
      })}
    </div>
  )
}

// ─── Name Input Dialog ─────────────────────────────────────────────────────────

interface NameDialogProps {
  open: boolean
  title: string
  placeholder: string
  initialValue?: string
  onClose: () => void
  onConfirm: (name: string) => void
  isPending: boolean
  disabled?: boolean
}

function NameDialog({
  open,
  title,
  placeholder,
  initialValue = '',
  onClose,
  onConfirm,
  isPending,
  disabled = false,
}: NameDialogProps) {
  const [value, setValue] = useState(initialValue)

  function handleSubmit() {
    if (disabled) return
    if (!value.trim()) { toast.error('Name is required'); return }
    onConfirm(value.trim())
  }

  return (
    <Dialog open={open} onOpenChange={(o) => { if (!o) onClose() }}>
      <DialogContent className="bg-zinc-900 border-zinc-800 text-white max-w-md overflow-y-auto">
        <DialogHeader>
          <DialogTitle className="text-white">{title}</DialogTitle>
        </DialogHeader>
        <div className="py-2">
          <input
            autoFocus
            type="text"
            value={value}
            onChange={(e) => setValue(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && handleSubmit()}
            placeholder={placeholder}
            className="w-full bg-zinc-800 border border-zinc-700 text-white placeholder:text-zinc-600 rounded-md px-3 py-2 text-sm focus:outline-none focus:border-blue-500 transition-colors font-mono"
          />
        </div>
        <DialogFooter className="gap-2">
          <Button
            variant="ghost"
            onClick={onClose}
            className="text-zinc-400 hover:text-white hover:bg-zinc-800"
          >
            Cancel
          </Button>
          <Button
            onClick={handleSubmit}
            disabled={isPending || disabled}
            className="bg-blue-600 hover:bg-blue-500 text-white"
          >
            {isPending && <Loader2 className="w-3.5 h-3.5 mr-2 animate-spin" />}
            Confirm
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ─── Delete Confirm Dialog ─────────────────────────────────────────────────────

interface ConfirmDeleteDialogProps {
  open: boolean
  name: string
  onConfirm: () => void
  onClose: () => void
  isPending: boolean
  disabled?: boolean
}

function ConfirmDeleteDialog({
  open,
  name,
  onConfirm,
  onClose,
  isPending,
  disabled = false,
}: ConfirmDeleteDialogProps) {
  return (
    <Dialog open={open} onOpenChange={(o) => { if (!o) onClose() }}>
      <DialogContent className="bg-zinc-900 border-zinc-800 text-white max-w-md overflow-y-auto">
        <DialogHeader>
          <DialogTitle className="text-white flex items-center gap-2">
            <AlertTriangle className="w-4 h-4 text-red-400" />
            Delete
          </DialogTitle>
        </DialogHeader>
        <p className="text-zinc-400 text-sm py-2">
          Delete <span className="text-white font-mono">{name}</span>? This cannot be undone.
        </p>
        <DialogFooter className="gap-2">
          <Button
            variant="ghost"
            onClick={onClose}
            className="text-zinc-400 hover:text-white hover:bg-zinc-800"
          >
            Cancel
          </Button>
          <Button
            onClick={onConfirm}
            disabled={isPending || disabled}
            className="bg-red-600 hover:bg-red-500 text-white"
          >
            {isPending && <Loader2 className="w-3.5 h-3.5 mr-2 animate-spin" />}
            Delete
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ─── File Editor Panel ─────────────────────────────────────────────────────────

interface FileEditorProps {
  path: string
  onClose: () => void
}

export function FileEditor({ path, onClose }: FileEditorProps) {
  const queryClient = useQueryClient()
  const [editorContent, setEditorContent] = useState<string | undefined>(undefined)
  const [isDirty, setIsDirty] = useState(false)

  const { isLoading, isFetching, isError, error, refetch } = useQuery<FileReadResponse>({
    queryKey: ['file-content', path],
    queryFn: async () => {
      const data = await api.get<FileReadResponse>(`/files/read?path=${encodeURIComponent(path)}`)
      setEditorContent(data.content)
      setIsDirty(false)
      return data
    },
  })

  const saveMutation = useMutation({
    mutationFn: () =>
      api.put('/files/write', { path, content: editorContent }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['file-content', path] })
      setIsDirty(false)
      toast.success('File saved')
    },
    onError: () => toast.error('Failed to save file'),
  })

  const filename = path.split('/').pop() ?? path

  function handleEditorChange(value: string | undefined) {
    setEditorContent(value)
    setIsDirty(true)
  }

  return (
    <div className="flex flex-col h-full">
      {/* Editor header */}
      <div className="flex items-center gap-3 px-4 py-3 border-b border-zinc-800 flex-shrink-0">
        <button
          onClick={onClose}
          className="text-zinc-500 hover:text-white transition-colors"
          title="Close editor"
        >
          <X className="w-4 h-4" />
        </button>
        <div className="flex-1 min-w-0">
          <p className="text-white text-sm font-medium truncate">{filename}</p>
          <p className="text-zinc-500 text-xs truncate font-mono">{path}</p>
        </div>
        {isDirty && (
          <span className="text-amber-400 text-xs font-medium">Unsaved</span>
        )}
        <Button
          size="sm"
          onClick={() => saveMutation.mutate()}
          disabled={isLoading || isError || saveMutation.isPending || !isDirty}
          className="bg-green-600 hover:bg-green-500 text-white disabled:opacity-40"
        >
          {saveMutation.isPending ? (
            <Loader2 className="w-3.5 h-3.5 mr-1.5 animate-spin" />
          ) : (
            <Save className="w-3.5 h-3.5 mr-1.5" />
          )}
          Save
        </Button>
      </div>

      {/* Editor body */}
      <div className="flex-1 overflow-hidden">
        {isLoading ? (
          <div className="flex items-center justify-center h-full text-zinc-600">
            <Loader2 className="w-5 h-5 animate-spin mr-2" />
            Loading file...
          </div>
        ) : isError ? (
          <div className="h-full overflow-auto p-4">
            <DependencyRemediation title="File contents could not be observed" summary="Saving stays disabled until HServer successfully reads this exact file." error={error?.message} retry={() => refetch()} retrying={isFetching} steps={['Retry the file read.', 'Confirm the file still exists inside an allowed root.', 'Verify the HServer service identity can read the file.']} />
          </div>
        ) : (
          <Editor
            height="100%"
            theme="vs-dark"
            language={getLanguage(filename)}
            value={editorContent}
            onChange={handleEditorChange}
            options={{
              minimap: { enabled: false },
              fontSize: 13,
              lineNumbers: 'on',
              scrollBeyondLastLine: false,
              wordWrap: 'on',
              tabSize: 2,
              renderWhitespace: 'boundary',
            }}
          />
        )}
      </div>
    </div>
  )
}

// ─── Main Files Page ───────────────────────────────────────────────────────────

export default function Files() {
  const queryClient = useQueryClient()
  const [currentPath, setCurrentPath] = useState('')
  const [openFile, setOpenFile] = useState<string | null>(null)

  // Dialogs
  const [newFileOpen, setNewFileOpen] = useState(false)
  const [newFolderOpen, setNewFolderOpen] = useState(false)
  const [deleteEntry, setDeleteEntry] = useState<FileEntry | null>(null)
  const [renameEntry, setRenameEntry] = useState<FileEntry | null>(null)

  const rootsQuery = useQuery<FileRootsResponse>({
    queryKey: ['file-roots'],
    queryFn: () => api.get<FileRootsResponse>('/files'),
  })
  const { data: rootsData, isLoading: rootsLoading, isFetching: rootsFetching, isError: rootsError, error: rootsFailure } = rootsQuery
  const allowedRoots = rootsData?.roots ?? []
  const effectivePath = currentPath || allowedRoots[0] || ''
  const activeRoot = activeFileRoot(effectivePath, allowedRoots)
  const isAtAllowedRoot = allowedRoots.includes(effectivePath.replace(/\/$/, ''))

  const directoryQuery = useQuery<FileListResponse>({
    queryKey: ['files', effectivePath],
    queryFn: () =>
      api.get<FileListResponse>(`/files?path=${encodeURIComponent(effectivePath)}`),
    enabled: effectivePath !== '',
  })
  const { data, isLoading: directoryLoading, isFetching: directoryFetching, isError: directoryError, error: directoryFailure } = directoryQuery
  const isLoading = rootsLoading || directoryLoading
  const directoryObserved = Boolean(effectivePath && data && !rootsError && !directoryError && !isLoading)
  const refreshing = rootsFetching || directoryFetching

  const retryObservation = () => {
    if (rootsError || !rootsData) {
      void rootsQuery.refetch()
      return
    }
    void directoryQuery.refetch()
  }

  const createMutation = useMutation({
    mutationFn: ({ name, type }: { name: string; type: 'file' | 'dir' }) =>
      api.post('/files/create', { path: joinPath(effectivePath, name), type }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['files', effectivePath] })
      setNewFileOpen(false)
      setNewFolderOpen(false)
      toast.success('Created')
    },
    onError: () => toast.error('Failed to create'),
  })

  const deleteMutation = useMutation({
    mutationFn: (path: string) =>
      api.delete(`/files?path=${encodeURIComponent(path)}`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['files', effectivePath] })
      setDeleteEntry(null)
      if (openFile?.startsWith(joinPath(effectivePath, deleteEntry?.name ?? ''))) {
        setOpenFile(null)
      }
      toast.success('Deleted')
    },
    onError: () => toast.error('Failed to delete'),
  })

  const renameMutation = useMutation({
    mutationFn: ({ oldPath, newName }: { oldPath: string; newName: string }) => {
      const newPath = joinPath(effectivePath, newName)
      return api.post('/files/rename', { old_path: oldPath, new_path: newPath })
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['files', effectivePath] })
      setRenameEntry(null)
      toast.success('Renamed')
    },
    onError: () => toast.error('Failed to rename'),
  })

  const navigate = useCallback((path: string) => {
    setCurrentPath(path)
    setOpenFile(null)
  }, [])

  const entries = data?.entries ?? []
  // Sort: folders first, then files, both alphabetically
  const sorted = [...entries].sort((a, b) => {
    if (isDir(a) !== isDir(b)) return isDir(a) ? -1 : 1
    return a.name.localeCompare(b.name)
  })

  return (
    <div className="space-y-4">
      {/* Page header */}
      <div className="flex items-center justify-between gap-4 flex-wrap flex-shrink-0">
        <div>
          <h2 className="text-white text-xl font-bold">File Manager</h2>
          <p className="text-zinc-500 text-sm mt-0.5">Browse and edit server files</p>
        </div>

        {/* Toolbar */}
        <div className="flex items-center gap-2">
          <Button
            variant="outline"
            size="sm"
            onClick={retryObservation}
            disabled={refreshing}
            aria-label="Refresh file inventory"
            className="border-zinc-700 text-zinc-300 hover:text-white hover:bg-zinc-800"
          >
            <RefreshCw className="w-3.5 h-3.5" />
          </Button>
          <Button
            variant="outline"
            size="sm"
            onClick={() => setNewFolderOpen(true)}
            disabled={!directoryObserved}
            className="border-zinc-700 text-zinc-300 hover:text-white hover:bg-zinc-800"
          >
            <Plus className="w-3.5 h-3.5 mr-1.5" />
            New Folder
          </Button>
          <Button
            size="sm"
            onClick={() => setNewFileOpen(true)}
            disabled={!directoryObserved}
            className="bg-blue-600 hover:bg-blue-500 text-white"
          >
            <Plus className="w-3.5 h-3.5 mr-1.5" />
            New File
          </Button>
        </div>
      </div>

      {/* Shortcuts */}
      <div className="flex items-center gap-2 flex-wrap flex-shrink-0">
        {allowedRoots.map((path, index) => (
          <button
            key={path}
            onClick={() => navigate(path)}
            className={cn(
              'flex items-center gap-1.5 px-3 py-1.5 rounded-lg border text-xs font-medium transition-colors',
              effectivePath === path || effectivePath.startsWith(`${path}/`)
                ? 'bg-blue-600/10 border-blue-600/20 text-blue-400'
                : 'bg-zinc-900 border-zinc-800 text-zinc-400 hover:text-white hover:border-zinc-700',
            )}
          >
            <FolderOpen className="w-3.5 h-3.5" />
            {fileRootLabel(path, index)}
          </button>
        ))}
      </div>

      {/* Split panel: file list + editor */}
      <div className="flex gap-3">
        {/* LEFT — File listing */}
        <Card
          className={cn(
            'bg-zinc-900 border-zinc-800',
            openFile ? 'w-80 flex-shrink-0 hidden lg:block' : 'flex-1',
          )}
        >
          {/* Breadcrumbs bar */}
          <div className="px-4 py-3 border-b border-zinc-800 flex-shrink-0">
            {effectivePath ? <Breadcrumbs path={effectivePath} root={activeRoot} onNavigate={navigate} /> : <span className="text-xs text-zinc-600">File roots unavailable</span>}
          </div>

          {/* List body */}
          <div className="max-h-[calc(100vh-16rem)] overflow-y-auto">
            {isLoading ? (
              <div className="p-4 space-y-2">
                {Array.from({ length: 8 }).map((_, i) => (
                  <Skeleton key={i} className="h-9 w-full bg-zinc-800" />
                ))}
              </div>
            ) : rootsError ? (
              <div className="p-4">
                <DependencyRemediation title="File roots could not be observed" summary="File browsing and every file mutation stay paused until HServer returns its configured root boundaries." error={rootsFailure?.message} retry={() => rootsQuery.refetch()} retrying={rootsFetching} steps={['Retry root detection.', 'Confirm the HServer API is reachable.', 'Verify the installation-owned file-root configuration.']} />
              </div>
            ) : allowedRoots.length === 0 ? (
              <div className="p-4">
                <DependencyRemediation state="not-configured" title="No file roots are configured" summary="HServer has no absolute root boundary available for file browsing or mutation." retry={() => rootsQuery.refetch()} retrying={rootsFetching} steps={['Configure an absolute virtual-host root.', 'Restart HServer after changing installation settings.', 'Retry root detection.']} />
              </div>
            ) : directoryError ? (
              <div className="p-4">
                <DependencyRemediation title="Directory contents could not be observed" summary={`HServer did not return a successful listing for ${effectivePath}. File mutations remain paused.`} error={directoryFailure?.message} retry={() => directoryQuery.refetch()} retrying={directoryFetching} steps={['Retry the directory listing.', 'Confirm the path still exists inside an allowed root.', 'Verify the HServer service identity can read the path.']} />
              </div>
            ) : sorted.length === 0 ? (
              <div className="flex flex-col items-center justify-center h-full py-16 text-zinc-600">
                <FolderOpen className="w-8 h-8 mb-2 opacity-40" />
                <p className="text-sm">Empty directory</p>
              </div>
            ) : (
              <div>
                {/* Up one level */}
                {!isAtAllowedRoot && (
                  <button
                    onClick={() => {
                      const up = effectivePath.replace(/\/[^/]+\/?$/, '') || '/'
                      navigate(up)
                    }}
                    className="flex items-center gap-3 w-full px-4 py-2.5 hover:bg-zinc-800/60 transition-colors text-zinc-400 hover:text-zinc-300 text-sm"
                  >
                    <Folder className="w-4 h-4 flex-shrink-0 opacity-50" />
                    <span className="font-mono">..</span>
                  </button>
                )}
                {sorted.map((entry) => {
                  const fullPath = joinPath(effectivePath, entry.name)
                  const isOpen = openFile === fullPath
                  return (
                    <div
                      key={entry.name}
                      className={cn(
                        'group flex items-center gap-3 px-4 py-2.5 transition-colors cursor-pointer',
                        isOpen
                          ? 'bg-blue-600/10 border-l-2 border-blue-500'
                          : 'hover:bg-zinc-800/60 border-l-2 border-transparent',
                      )}
                      onClick={() => {
                        if (isDir(entry)) {
                          navigate(fullPath)
                        } else {
                          setOpenFile(fullPath)
                        }
                      }}
                    >
                      <FileIcon entry={entry} />
                      <div className="flex-1 min-w-0">
                        <p
                          className={cn(
                            'text-sm truncate',
                            isOpen ? 'text-blue-300' : 'text-zinc-200',
                          )}
                        >
                          {entry.name}
                        </p>
                        <p className="text-zinc-600 text-xs font-mono mt-0.5 truncate">
                          {entry.permissions} &nbsp; {entry.owner} &nbsp;
                          {!isDir(entry) && formatSize(entry.size)}
                        </p>
                      </div>
                      <span className="text-zinc-600 text-xs hidden sm:block flex-shrink-0">
                        {formatDate(entry.modified)}
                      </span>
                      {/* Action buttons */}
                      <div className="flex items-center gap-0.5 opacity-0 group-hover:opacity-100 transition-opacity flex-shrink-0">
                        <button
                          onClick={(e) => { e.stopPropagation(); setRenameEntry(entry) }}
                          className="text-zinc-500 hover:text-blue-400 p-1 rounded transition-colors"
                          title="Rename"
                        >
                          <PencilLine className="w-3.5 h-3.5" />
                        </button>
                        <button
                          onClick={(e) => { e.stopPropagation(); setDeleteEntry(entry) }}
                          className="text-zinc-500 hover:text-red-400 p-1 rounded transition-colors"
                          title="Delete"
                        >
                          <Trash2 className="w-3.5 h-3.5" />
                        </button>
                        {!isDir(entry) && (
                          <ChevronRight
                            className={cn(
                              'w-3.5 h-3.5 transition-colors',
                              isOpen ? 'text-blue-400' : 'text-zinc-600',
                            )}
                          />
                        )}
                      </div>
                    </div>
                  )
                })}
              </div>
            )}
          </div>
        </Card>

        {/* RIGHT — Monaco Editor */}
        {openFile && (
          <Card className="bg-zinc-900 border-zinc-800 flex-1 min-w-0 overflow-hidden flex flex-col">
            <CardContent className="p-0 h-full flex flex-col">
              <FileEditor key={openFile} path={openFile} onClose={() => setOpenFile(null)} />
            </CardContent>
          </Card>
        )}

        {/* Placeholder when no file open (desktop) */}
        {!openFile && (
          <div className="hidden lg:flex flex-1 items-center justify-center text-zinc-700 select-none">
            <div className="text-center">
              <FileText className="w-10 h-10 mx-auto mb-2 opacity-20" />
              <p className="text-sm">Select a file to edit</p>
            </div>
          </div>
        )}
      </div>

      {/* Dialogs */}
      <NameDialog
        key={`new-file-${newFileOpen}`}
        open={newFileOpen}
        title="New File"
        placeholder="filename.conf"
        onClose={() => setNewFileOpen(false)}
        onConfirm={(name) => createMutation.mutate({ name, type: 'file' })}
        isPending={createMutation.isPending}
        disabled={!directoryObserved}
      />

      <NameDialog
        key={`new-folder-${newFolderOpen}`}
        open={newFolderOpen}
        title="New Folder"
        placeholder="folder-name"
        onClose={() => setNewFolderOpen(false)}
        onConfirm={(name) => createMutation.mutate({ name, type: 'dir' })}
        isPending={createMutation.isPending}
        disabled={!directoryObserved}
      />

      <NameDialog
        key={renameEntry?.path ?? 'rename-closed'}
        open={renameEntry !== null}
        title={`Rename "${renameEntry?.name ?? ''}"`}
        placeholder="new-name"
        initialValue={renameEntry?.name ?? ''}
        onClose={() => setRenameEntry(null)}
        onConfirm={(newName) =>
          directoryObserved && renameEntry &&
          renameMutation.mutate({
            oldPath: joinPath(effectivePath, renameEntry.name),
            newName,
          })
        }
        isPending={renameMutation.isPending}
        disabled={!directoryObserved}
      />

      <ConfirmDeleteDialog
        open={deleteEntry !== null}
        name={deleteEntry?.name ?? ''}
        onConfirm={() =>
          directoryObserved && deleteEntry &&
          deleteMutation.mutate(joinPath(effectivePath, deleteEntry.name))
        }
        onClose={() => setDeleteEntry(null)}
        isPending={deleteMutation.isPending}
        disabled={!directoryObserved}
      />
    </div>
  )
}
