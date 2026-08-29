import { useEffect, useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowUp, FileCode2, Folder, FolderOpen, Loader2, RefreshCw, Save } from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { cn } from '@/lib/utils'
import { requireRemoteMutationReadiness, useRemoteMutationReadiness } from './remoteMutationReadiness'

interface FileEntry { name: string; path: string; type: 'directory' | 'file' | 'symlink'; size: number; mode: string; modified_at: string }
interface FileContent { path: string; content: string; checksum: string; size: number; mode: string; modified_at: string }
interface FileDraft { path: string; content: string; originalContent: string; checksum: string }

function formatBytes(bytes: number) {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 ** 2) return `${(bytes / 1024).toFixed(1)} KiB`
  return `${(bytes / 1024 ** 2).toFixed(1)} MiB`
}

function parentPath(current: string, roots: string[]) {
  const root = roots.find((candidate) => current === candidate || current.startsWith(`${candidate}/`)) ?? current
  if (current === root) return current
  const parent = current.slice(0, current.lastIndexOf('/')) || root
  return parent.length < root.length ? root : parent
}

export function RemoteFiles({ nodeID, online, readAvailable, writeAvailable, readRoots, writeRoots }: { nodeID: string; online: boolean; readAvailable: boolean; writeAvailable: boolean; readRoots: string[]; writeRoots: string[] }) {
  const controlsReady = useRemoteMutationReadiness()
  const nodeBase = `/nodes/${encodeURIComponent(nodeID)}`
  const queryClient = useQueryClient()
  const [currentPath, setCurrentPath] = useState(readRoots[0] ?? '')
  const [selectedPath, setSelectedPath] = useState<string | null>(null)
  const [draft, setDraft] = useState<FileDraft | null>(null)
  const [saveConflict, setSaveConflict] = useState(false)

  const filesQuery = useQuery<FileEntry[]>({
    queryKey: ['managed-node-files', nodeID, currentPath],
    queryFn: () => api.get(`${nodeBase}/files?path=${encodeURIComponent(currentPath)}`),
    enabled: online && readAvailable && !!currentPath,
  })
  const fileQuery = useQuery<FileContent>({
    queryKey: ['managed-node-file', nodeID, selectedPath],
    queryFn: () => api.get(`${nodeBase}/file?path=${encodeURIComponent(selectedPath ?? '')}`),
    enabled: online && readAvailable && !!selectedPath,
  })
  const draftPath = draft?.path
  const draftChecksum = draft?.checksum
  useEffect(() => {
    if (!fileQuery.data) return
    if (draftPath === fileQuery.data.path) {
      if (draftChecksum !== fileQuery.data.checksum) {
        // eslint-disable-next-line react-hooks/set-state-in-effect -- report a server-side edit without replacing the local draft
        setSaveConflict(true)
      }
      return
    }
    setDraft({ path: fileQuery.data.path, content: fileQuery.data.content, originalContent: fileQuery.data.content, checksum: fileQuery.data.checksum })
    setSaveConflict(false)
  }, [draftChecksum, draftPath, fileQuery.data])

  const pathWriteAllowed = !!selectedPath && writeRoots.some((root) => selectedPath === root || selectedPath.startsWith(`${root}/`))
  const dedicatedConfigEditor = !!selectedPath && (selectedPath.startsWith('/etc/nginx/') || selectedPath.startsWith('/etc/php/'))
  const readonly = !!selectedPath && (!writeAvailable || !pathWriteAllowed || dedicatedConfigEditor)
  const draftReady = !!draft && draft.path === selectedPath
  const dirty = draftReady && draft.content !== draft.originalContent
  const saveMutation = useMutation<{ message: string; backup: string }, Error, void>({
    mutationFn: () => { requireRemoteMutationReadiness(controlsReady); return api.put(nodeBase + '/file', { path: draft?.path, content: draft?.content, checksum: draft?.checksum }) },
    onSuccess: async (result) => {
      toast.success(`${result.message}. Backup: ${result.backup}`)
      const refreshed = await fileQuery.refetch()
      if (refreshed.data) {
        setDraft({ path: refreshed.data.path, content: refreshed.data.content, originalContent: refreshed.data.content, checksum: refreshed.data.checksum })
        setSaveConflict(false)
      }
      await queryClient.invalidateQueries({ queryKey: ['managed-node-files', nodeID, currentPath] })
    },
    onError: (error) => {
      if (error.message.includes('changed on the server')) setSaveConflict(true)
      toast.error(error.message || 'Remote file save failed')
    },
  })
  const currentRoot = useMemo(() => readRoots.find((root) => currentPath === root || currentPath.startsWith(`${root}/`)) ?? currentPath, [currentPath, readRoots])

  const changeDirectory = (next: string) => {
    setCurrentPath(next)
    setSelectedPath(null)
    setDraft(null)
    setSaveConflict(false)
  }

  const selectFile = (path: string) => {
    setSelectedPath(path)
    setSaveConflict(false)
  }

  const loadServerVersion = async () => {
    const refreshed = await fileQuery.refetch()
    if (!refreshed.data) return
    setDraft({ path: refreshed.data.path, content: refreshed.data.content, originalContent: refreshed.data.content, checksum: refreshed.data.checksum })
    setSaveConflict(false)
  }

  if (!online) return <FileUnavailable title="Managed node is offline" detail="File inventory, content reads, and edits remain unavailable until the agent reconnects." />
  if (!readAvailable || readRoots.length === 0) return <FileUnavailable title="File browsing is disabled" detail="This agent does not advertise a usable files.read capability. Configure HSERVER_AGENT_FILE_READ_ROOTS, then restart the agent." />

  return <div className="space-y-4">
    {filesQuery.isSuccess && (!writeAvailable || writeRoots.length === 0) ? <Card className="border-amber-500/30 bg-amber-500/[0.07]"><CardContent className="p-4 text-xs text-amber-200">File browsing is available. Editing is read-only because <code>files.write</code> has no configured local roots.</CardContent></Card> : null}
    <div className="grid gap-4 xl:grid-cols-[380px_minmax(0,1fr)]">
    <Card className="border-zinc-800 bg-zinc-900/80">
      <CardHeader className="space-y-3">
        <div className="flex items-center justify-between gap-3"><div><CardTitle className="text-sm text-zinc-200">remote server files</CardTitle><p className="mt-1 text-[10px] text-zinc-500">Browse and edit existing text files</p></div><Button variant="ghost" size="icon-xs" aria-label="Refresh directory inventory" disabled={filesQuery.isFetching} onClick={() => filesQuery.refetch()}><RefreshCw className={cn('size-3', filesQuery.isFetching && 'animate-spin')} /></Button></div>
        <div className="flex flex-wrap gap-1">{readRoots.map((root) => <button key={root} onClick={() => changeDirectory(root)} className={cn('rounded-md border px-2 py-1 font-mono text-[10px]', currentRoot === root ? 'border-violet-500/50 bg-violet-500/10 text-violet-300' : 'border-zinc-800 text-zinc-500 hover:text-zinc-300')}>{root}</button>)}</div>
        <div className="flex items-center gap-2 rounded-lg border border-zinc-800 bg-zinc-950/60 px-2 py-1.5"><Button variant="ghost" size="icon-xs" disabled={currentPath === currentRoot} onClick={() => changeDirectory(parentPath(currentPath, readRoots))}><ArrowUp className="size-3" /></Button><span className="min-w-0 truncate font-mono text-[11px] text-zinc-300" title={currentPath}>{currentPath}</span></div>
      </CardHeader>
      <CardContent className="max-h-[640px] overflow-auto p-0">
        {filesQuery.isLoading ? <div className="p-8 text-center"><Loader2 className="mx-auto size-4 animate-spin text-zinc-500" /></div> : filesQuery.error ? <p className="p-4 text-xs text-red-400">{filesQuery.error.message}</p> : (filesQuery.data ?? []).length === 0 ? <p className="p-8 text-center text-xs text-zinc-600">Directory is empty.</p> : <div className="divide-y divide-zinc-800/60">{(filesQuery.data ?? []).map((entry) => {
          const isDirectory = entry.type === 'directory'
          const active = selectedPath === entry.path
          return <button key={entry.path} onClick={() => isDirectory ? changeDirectory(entry.path) : selectFile(entry.path)} className={cn('flex w-full items-center gap-3 px-4 py-2.5 text-left transition hover:bg-zinc-800/40', active && 'bg-violet-500/10')}>
            {isDirectory ? <Folder className="size-4 shrink-0 text-amber-400" /> : <FileCode2 className="size-4 shrink-0 text-blue-400" />}
            <span className="min-w-0 flex-1"><span className="block truncate font-mono text-xs text-zinc-200">{entry.name}</span><span className="block text-[9px] text-zinc-600">{entry.mode} · {isDirectory ? 'directory' : formatBytes(entry.size)}</span></span>
          </button>
        })}</div>}
      </CardContent>
    </Card>

    <Card className="min-w-0 border-zinc-800 bg-zinc-900/80">
      {!filesQuery.isSuccess ? <CardContent className="grid min-h-[420px] place-items-center text-center"><div>{filesQuery.isLoading ? <Loader2 className="mx-auto size-6 animate-spin text-zinc-600" /> : <FolderOpen className="mx-auto size-8 text-red-500/60" />}<p className="mt-3 text-sm text-zinc-400">{filesQuery.isLoading ? 'Loading directory before file selection…' : 'Directory inventory is unavailable.'}</p><p className="mt-1 text-[10px] text-zinc-600">{filesQuery.isLoading ? 'File selection and editing stay unavailable until the directory is observed.' : 'Retry the directory inventory before selecting or editing a file.'}</p></div></CardContent> : !selectedPath ? <CardContent className="grid min-h-[420px] place-items-center text-center"><div><FolderOpen className="mx-auto size-8 text-zinc-700" /><p className="mt-3 text-sm text-zinc-400">Select a text file to inspect or edit.</p><p className="mt-1 text-[10px] text-zinc-600">Nginx and PHP configuration files open read-only here; use their dedicated tabs to save safely.</p></div></CardContent> : <>
        <CardHeader className="flex-row items-start justify-between gap-3"><div className="min-w-0"><CardTitle className="truncate font-mono text-sm text-zinc-200" title={selectedPath}>{selectedPath}</CardTitle>{fileQuery.data && <p className="mt-1 text-[10px] text-zinc-500">{fileQuery.data.mode} · {formatBytes(fileQuery.data.size)} · {new Date(fileQuery.data.modified_at).toLocaleString()}</p>}</div><Button size="sm" disabled={!controlsReady || !online || readonly || !dirty || saveConflict || saveMutation.isPending} onClick={() => saveMutation.mutate()}>{saveMutation.isPending ? <Loader2 className="size-3.5 animate-spin" /> : <Save className="size-3.5" />} Save</Button></CardHeader>
        <CardContent>{readonly && <div className="mb-3 rounded-lg border border-amber-800/40 bg-amber-500/[0.06] px-3 py-2 text-xs text-amber-300">{dedicatedConfigEditor ? <>Read-only here. Save this configuration from the {selectedPath.startsWith('/etc/php/') ? 'PHP' : 'Nginx'} tab, where every write is tested and rolled back automatically on failure.</> : <>This path is outside the agent's configured <code>files.write</code> roots.</>}</div>}{saveConflict && <div className="mb-3 flex flex-col gap-2 rounded-lg border border-red-800/50 bg-red-500/[0.07] px-3 py-2 text-xs text-red-300 sm:flex-row sm:items-center sm:justify-between"><span>This file changed on remote server after you opened it. Your draft was not overwritten.</span><Button type="button" variant="outline" size="xs" disabled={fileQuery.isFetching} onClick={() => void loadServerVersion()}><RefreshCw className={cn('size-3', fileQuery.isFetching && 'animate-spin')} /> Load server version</Button></div>}{fileQuery.isLoading ? <div className="grid min-h-[380px] place-items-center"><Loader2 className="size-5 animate-spin text-zinc-500" /></div> : fileQuery.error ? <p className="text-xs text-red-400">{fileQuery.error.message}</p> : <textarea value={draftReady ? draft.content : ''} readOnly={readonly} spellCheck={false} onChange={(event) => setDraft(current => current ? { ...current, content: event.target.value } : current)} className="min-h-[540px] w-full resize-y rounded-xl border border-zinc-800 bg-zinc-950 p-4 font-mono text-xs leading-5 text-zinc-200 outline-none focus:border-violet-500/60 read-only:text-zinc-400" />}</CardContent>
      </>}
    </Card>
    </div>
  </div>
}

function FileUnavailable({ title, detail }: { title: string; detail: string }) { return <Card className="border-amber-500/30 bg-amber-500/[0.07]"><CardContent className="p-5 text-xs text-amber-200"><p className="font-semibold">{title}</p><p className="mt-1 text-amber-200/80">{detail}</p></CardContent></Card> }
