import { useState, useRef, useEffect, useCallback } from 'react'
import { useQuery } from '@tanstack/react-query'
import {
  FileText, RefreshCw, Download, Search, ChevronDown,
  AlertTriangle, XCircle, Loader2, Terminal, Trash2,
  WifiOff,
} from 'lucide-react'
import { Button } from '@/components/ui/button'
import { api, authenticatedFetch } from '@/lib/api'
import { consumeAuthenticatedEventStream } from '@/lib/sse'

interface LogFile {
  category?: string
  label?: string
  sizeBytes?: number
  lastModified?: string
  readable?: boolean
  path: string
  name: string
  size: number
  modified: string
}

interface LogTailResponse {
  lines: string[]
  total: number
}

function detectLevel(line: string): 'error' | 'warn' | 'info' | 'debug' {
  const lower = line.toLowerCase()
  if (lower.includes('[error]') || lower.includes('error') || lower.includes('crit') || lower.includes('emerg')) return 'error'
  if (lower.includes('[warn]') || lower.includes('warning') || lower.includes('notice')) return 'warn'
  if (lower.includes('[info]') || lower.includes('info')) return 'info'
  if (lower.includes('[debug]') || lower.includes('debug')) return 'debug'
  return 'info'
}

function lineColor(level: string): string {
  switch (level) {
    case 'error': return 'text-red-400'
    case 'warn': return 'text-amber-400'
    case 'debug': return 'text-zinc-500'
    default: return 'text-zinc-300'
  }
}

const DEFAULT_LOG_FILES = [
  '/var/log/nginx/error.log',
  '/var/log/nginx/access.log',
  '/var/log/php8.4-fpm.log',
  '/var/log/syslog',
  '/var/log/auth.log',
  '/var/log/fail2ban.log',
]

type SseStatus = 'idle' | 'connecting' | 'connected' | 'reconnecting' | 'error'

export default function Logs() {
  const [selectedFile, setSelectedFile] = useState(DEFAULT_LOG_FILES[0])
  const [lines, setLines] = useState(100)
  const [search, setSearch] = useState('')
  const [autoScroll, setAutoScroll] = useState(true)
  const [liveRefresh, setLiveRefresh] = useState(false)
  const [sseLines, setSseLines] = useState<string[]>([])
  const [sseStatus, setSseStatus] = useState<SseStatus>('idle')

  const bottomRef = useRef<HTMLDivElement>(null)
  const streamRef = useRef<AbortController | null>(null)
  const reconnectTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const reconnectAttempts = useRef(0)

  const logFilesQuery = useQuery<{ sources: LogFile[] }>({
    queryKey: ['log-files'],
    queryFn: () => api.get<{ sources: LogFile[] }>('/logs/sources'),
    staleTime: 60_000,
  })

  const logQuery = useQuery<LogTailResponse>({
    queryKey: ['logs', 'tail', selectedFile, lines],
    queryFn: () =>
      api.get<LogTailResponse>(
        `/logs/read?path=${encodeURIComponent(selectedFile)}&lines=${lines}`,
      ),
    staleTime: 5_000,
    // Fallback polling — used only when fetch streaming is not supported
    refetchInterval: liveRefresh && typeof ReadableStream === 'undefined' ? 5_000 : false,
  })
  const logData = logQuery.data

  // ── SSE connection management ─────────────────────────────────────────────

  const closeSSE = useCallback(() => {
    if (reconnectTimer.current) {
      clearTimeout(reconnectTimer.current)
      reconnectTimer.current = null
    }
    if (streamRef.current) {
      streamRef.current.abort()
      streamRef.current = null
    }
    reconnectAttempts.current = 0
    setSseStatus('idle')
  }, [])

  const openSSE = useCallback((filePath: string) => {
    // Already open for same file → noop
    if (streamRef.current) {
      streamRef.current.abort()
      streamRef.current = null
    }

    setSseStatus('connecting')

    const controller = new AbortController()
    streamRef.current = controller
    const reconnect = () => {
      if (controller.signal.aborted) return
      streamRef.current = null
      const delay = Math.min(1000 * 2 ** reconnectAttempts.current, 30_000)
      reconnectAttempts.current += 1
      setSseStatus('reconnecting')
      reconnectTimer.current = setTimeout(() => {
        // Still in live mode → retry
        openSSE(filePath)
      }, delay)
    }
    void consumeAuthenticatedEventStream(
      `/logs/stream?path=${encodeURIComponent(filePath)}`,
      controller.signal,
      () => {
        reconnectAttempts.current = 0
        setSseStatus('connected')
      },
      (message) => setSseLines((prev) => [...prev, message]),
    ).then(reconnect).catch(reconnect)
  }, [])

  // Toggle live mode
  const handleLiveToggle = () => {
    if (liveRefresh) {
      closeSSE()
      setLiveRefresh(false)
    } else {
      setSseLines([])
      setLiveRefresh(true)
      if (typeof ReadableStream !== 'undefined') {
        openSSE(selectedFile)
      }
    }
  }

  // Re-open SSE when selected file changes while live is active
  useEffect(() => {
    if (liveRefresh && typeof ReadableStream !== 'undefined') {
      setSseLines([])
      openSSE(selectedFile)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedFile])

  // Cleanup on unmount
  useEffect(() => {
    return () => { closeSSE() }
  }, [closeSSE])

  // Auto-scroll
  useEffect(() => {
    if (autoScroll && bottomRef.current) {
      bottomRef.current.scrollIntoView({ behavior: 'smooth' })
    }
  }, [logData, sseLines, autoScroll])

  // ── Derived data ──────────────────────────────────────────────────────────

  const isSSEMode = liveRefresh && typeof ReadableStream !== 'undefined'
  const displayLines = isSSEMode ? sseLines : (logData?.lines ?? [])
  const filtered = search
    ? displayLines.filter((l) => l.toLowerCase().includes(search.toLowerCase()))
    : displayLines

  const errorCount = displayLines.filter((l) => detectLevel(l) === 'error').length
  const warnCount = displayLines.filter((l) => detectLevel(l) === 'warn').length

  // ── Download ──────────────────────────────────────────────────────────────

  const handleDownload = async () => {
    // Try the server-side download endpoint first
    try {
      const res = await authenticatedFetch(`/logs/download?path=${encodeURIComponent(selectedFile)}`)
      if (res.ok) {
        const blob = await res.blob()
        const a = document.createElement('a')
        a.href = URL.createObjectURL(blob)
        a.download = selectedFile.split('/').pop() ?? 'log.txt'
        a.click()
        URL.revokeObjectURL(a.href)
        return
      }
    } catch {
      // fallthrough to client-side blob
    }
    // Fallback: download currently displayed lines as blob
    const content = displayLines.join('\n')
    const blob = new Blob([content], { type: 'text/plain' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = selectedFile.split('/').pop() ?? 'log.txt'
    a.click()
    URL.revokeObjectURL(url)
  }

  const availableFiles = logFilesQuery.data?.sources?.map((f) => f.path) ?? DEFAULT_LOG_FILES

  // ── SSE status label ──────────────────────────────────────────────────────

  const sseStatusBadge = () => {
    switch (sseStatus) {
      case 'connecting':
        return <span className="text-amber-400 text-xs flex items-center gap-1"><Loader2 className="w-3 h-3 animate-spin" /> Connecting…</span>
      case 'reconnecting':
        return <span className="text-amber-400 text-xs flex items-center gap-1"><WifiOff className="w-3 h-3" /> Reconnecting…</span>
      case 'error':
        return <span className="text-red-400 text-xs flex items-center gap-1"><XCircle className="w-3 h-3" /> Disconnected</span>
      case 'connected':
        return <span className="text-green-400 text-xs flex items-center gap-1"><span className="w-1.5 h-1.5 rounded-full bg-green-500 animate-pulse inline-block" /> Live</span>
      default:
        return null
    }
  }

  return (
    <div className="space-y-4 h-full flex flex-col">
      {/* Header */}
      <div className="flex items-center justify-between gap-4 flex-wrap">
        <div>
          <h2 className="text-white text-xl font-bold">Log Viewer</h2>
          <p className="text-zinc-500 text-sm mt-0.5">System and application logs</p>
        </div>
        <div className="flex items-center gap-2">
          <button
            onClick={handleLiveToggle}
            className={`flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium border transition-colors ${
              liveRefresh
                ? 'bg-green-500/10 border-green-500/40 text-green-400'
                : 'border-zinc-700 text-zinc-400 hover:border-zinc-600'
            }`}
          >
            <div className={`w-1.5 h-1.5 rounded-full ${liveRefresh ? 'bg-green-500 animate-pulse' : 'bg-zinc-600'}`} />
            Live
          </button>
          {/* Clear button — visible in SSE mode */}
          {isSSEMode && (
            <Button
              variant="outline"
              size="sm"
              onClick={() => setSseLines([])}
              className="border-zinc-700 text-zinc-300 h-8"
            >
              <Trash2 className="w-3.5 h-3.5 mr-1" />
              Clear
            </Button>
          )}
          <Button
            variant="outline"
            size="sm"
            onClick={() => logQuery.refetch()}
            disabled={logQuery.isFetching || isSSEMode}
            className="border-zinc-700 text-zinc-300 h-8"
          >
            {logQuery.isFetching ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : <RefreshCw className="w-3.5 h-3.5" />}
          </Button>
          <Button
            variant="outline"
            size="sm"
            onClick={handleDownload}
            disabled={!isSSEMode && logQuery.isError}
            className="border-zinc-700 text-zinc-300 h-8"
          >
            <Download className="w-3.5 h-3.5 mr-1" />
            Download
          </Button>
        </div>
      </div>

      {logFilesQuery.isError && (
        <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-amber-500/25 bg-amber-500/[0.06] px-4 py-3 text-xs text-amber-200">
          <span>Log source inventory could not be loaded. Showing portable default paths.</span>
          <Button type="button" variant="outline" size="sm" onClick={() => { void logFilesQuery.refetch() }} disabled={logFilesQuery.isFetching} className="border-amber-500/30 text-amber-100">
            <RefreshCw className={`mr-2 size-3.5 ${logFilesQuery.isFetching ? 'animate-spin' : ''}`} />Retry sources
          </Button>
        </div>
      )}

      {/* Controls */}
      <div className="flex flex-wrap gap-3">
        {/* File selector */}
        <div className="relative flex-1 min-w-48">
          <FileText className="absolute left-3 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-zinc-500" />
          <select
            value={selectedFile}
            onChange={(e) => setSelectedFile(e.target.value)}
            className="w-full bg-zinc-900 border border-zinc-800 text-white text-sm rounded-lg pl-8 pr-8 py-2 focus:outline-none focus:border-blue-500 appearance-none"
          >
            {availableFiles.map((f) => (
              <option key={f} value={f}>{f.split('/').pop()}</option>
            ))}
          </select>
          <ChevronDown className="absolute right-3 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-zinc-500 pointer-events-none" />
        </div>

        {/* Lines selector — hidden in SSE mode since we stream indefinitely */}
        {!isSSEMode && (
          <div className="relative">
            <select
              value={lines}
              onChange={(e) => setLines(Number(e.target.value))}
              className="bg-zinc-900 border border-zinc-800 text-white text-sm rounded-lg px-3 py-2 focus:outline-none focus:border-blue-500 appearance-none pr-8"
            >
              {[50, 100, 250, 500, 1000].map((n) => (
                <option key={n} value={n}>Last {n} lines</option>
              ))}
            </select>
            <ChevronDown className="absolute right-2 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-zinc-500 pointer-events-none" />
          </div>
        )}

        {/* Search */}
        <div className="relative flex-1 min-w-40">
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-zinc-500" />
          <input
            type="text"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="Filter lines..."
            className="w-full bg-zinc-900 border border-zinc-800 text-white text-sm rounded-lg pl-9 pr-4 py-2 focus:outline-none focus:border-blue-500"
          />
        </div>
      </div>

      {/* Stats bar */}
      {displayLines.length > 0 && (
        <div className="flex items-center gap-4 text-xs text-zinc-500">
          <span>{filtered.length} / {displayLines.length} lines</span>
          {errorCount > 0 && (
            <span className="flex items-center gap-1 text-red-400">
              <XCircle className="w-3 h-3" />
              {errorCount} errors
            </span>
          )}
          {warnCount > 0 && (
            <span className="flex items-center gap-1 text-amber-400">
              <AlertTriangle className="w-3 h-3" />
              {warnCount} warnings
            </span>
          )}
          <label className="flex items-center gap-1.5 ml-auto cursor-pointer">
            <input
              type="checkbox"
              checked={autoScroll}
              onChange={(e) => setAutoScroll(e.target.checked)}
              className="w-3 h-3"
            />
            Auto-scroll
          </label>
        </div>
      )}

      {/* Log viewer */}
      <div className="flex-1 bg-zinc-950 border border-zinc-800 rounded-xl overflow-hidden flex flex-col min-h-0">
        <div className="flex items-center justify-between px-4 py-2.5 border-b border-zinc-800 bg-zinc-900">
          <div className="flex items-center gap-2">
            <Terminal className="w-3.5 h-3.5 text-zinc-500" />
            <span className="text-zinc-400 text-xs font-mono truncate max-w-xs">{selectedFile}</span>
          </div>
          <div className="flex items-center gap-2">
            {isSSEMode ? sseStatusBadge() : logQuery.isFetching && <Loader2 className="w-3.5 h-3.5 text-zinc-500 animate-spin" />}
          </div>
        </div>

        <div className="flex-1 overflow-y-auto p-4 min-h-96 max-h-[60vh]">
          {logQuery.isLoading && !isSSEMode ? (
            <div className="flex items-center gap-2 text-zinc-500 justify-center py-12">
              <Loader2 className="w-4 h-4 animate-spin" />
              <span className="text-sm">Loading logs...</span>
            </div>
          ) : logQuery.isError && !isSSEMode ? (
            <div className="flex flex-col items-center justify-center py-12 text-center">
              <AlertTriangle className="size-5 text-red-400" />
              <p className="mt-2 text-sm text-red-300">Selected log could not be read.</p>
              <p className="mt-1 text-xs text-zinc-600">{logQuery.error.message}</p>
              <Button type="button" variant="outline" size="sm" className="mt-4 border-red-500/30 text-red-200" onClick={() => { void logQuery.refetch() }} disabled={logQuery.isFetching}>
                <RefreshCw className={`mr-2 size-3.5 ${logQuery.isFetching ? 'animate-spin' : ''}`} />Retry
              </Button>
            </div>
          ) : filtered.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-12 text-zinc-600">
              <FileText className="w-10 h-10 mb-3 opacity-30" />
              <p className="text-sm">
                {isSSEMode
                  ? sseStatus === 'connecting' ? 'Waiting for log events…' : 'No events yet'
                  : search
                    ? 'No lines match your filter'
                    : 'Log is empty'}
              </p>
            </div>
          ) : (
            <div className="space-y-0.5 font-mono text-xs">
              {filtered.map((line, i) => {
                const level = detectLevel(line)
                return (
                  <div
                    key={i}
                    className={`flex gap-3 py-0.5 hover:bg-zinc-900/60 rounded px-2 -mx-2 leading-relaxed ${lineColor(level)}`}
                  >
                    <span className="text-zinc-700 select-none w-8 text-right shrink-0">
                      {i + 1}
                    </span>
                    <span className="break-all">{line}</span>
                  </div>
                )
              })}
              <div ref={bottomRef} />
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
