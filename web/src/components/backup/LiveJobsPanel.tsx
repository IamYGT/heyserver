import { useEffect, useRef, useState, useCallback } from 'react'
import { Loader2, Activity, CheckCircle2, XCircle, Clock, Terminal, ChevronRight, Maximize2, Copy, X } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { toast } from 'sonner'
import type { BackupJob } from '@/lib/types'
import { formatETA, formatDuration, jobTypeLabel, phaseLabel } from '@/components/backup/phaseLabels'
import { backupOperationHint, humanizeJobError } from '@/components/backup/backupErrorHints'

const PHASE_ORDER = ['preparing', 'database', 'files', 'archive', 'retention', 'gdrive_upload', 'verify', 'restore', 'done'] as const

function formatBytes(n?: number): string {
  if (!n || n <= 0) return '—'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let v = n
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(i > 0 ? 1 : 0)} ${units[i]}`
}

function statusIcon(status: BackupJob['status']) {
  if (status === 'running') return <Loader2 className="w-4 h-4 text-blue-400 animate-spin shrink-0" />
  if (status === 'pending') return <Clock className="w-4 h-4 text-zinc-400 shrink-0" />
  if (status === 'completed') return <CheckCircle2 className="w-4 h-4 text-emerald-400 shrink-0" />
  return <XCircle className="w-4 h-4 text-red-400 shrink-0" />
}

function PhaseTimeline({ job }: { job: BackupJob }) {
  const current = job.phase ?? 'preparing'
  const currentIdx = PHASE_ORDER.indexOf(current as (typeof PHASE_ORDER)[number])

  return (
    <div className="flex flex-wrap items-center gap-1">
      {PHASE_ORDER.filter((p) => p !== 'done' || job.status === 'completed' || job.status === 'failed').map((phase, i) => {
        const idx = PHASE_ORDER.indexOf(phase)
        const done = idx < currentIdx || job.status === 'completed'
        const active = phase === current && job.status === 'running'
        return (
          <div key={phase} className="flex items-center gap-1">
            {i > 0 && <ChevronRight className="w-3 h-3 text-zinc-700" />}
            <span
              className={`text-[10px] px-1.5 py-0.5 rounded border ${
                active
                  ? 'border-blue-500/50 bg-blue-500/15 text-blue-300'
                  : done
                  ? 'border-emerald-500/30 bg-emerald-500/10 text-emerald-400'
                  : 'border-zinc-800 bg-zinc-900 text-zinc-600'
              }`}
            >
              {phaseLabel(phase)}
            </span>
          </div>
        )
      })}
    </div>
  )
}

function JobLogTerminal({
  logs,
  maxHeightClass = 'max-h-52',
  scrollRef,
  onUserScroll,
}: {
  logs?: string[]
  maxHeightClass?: string
  scrollRef?: React.RefObject<HTMLDivElement | null>
  onUserScroll?: () => void
}) {
  const bottomRef = useRef<HTMLDivElement>(null)
  const containerRef = useRef<HTMLDivElement>(null)
  const stickToBottom = useRef(true)

  const handleScroll = () => {
    const el = containerRef.current
    if (!el) return
    const nearBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 48
    stickToBottom.current = nearBottom
    if (!nearBottom) onUserScroll?.()
  }

  const latestLog = logs?.[logs.length - 1]
  useEffect(() => {
    if (stickToBottom.current) {
      bottomRef.current?.scrollIntoView({ behavior: 'smooth' })
    }
  }, [logs?.length, latestLog])

  if (!logs?.length) {
    return (
      <div className="rounded-lg bg-black/60 border border-zinc-800 px-3 py-2 text-zinc-600 text-[11px] font-mono">
        Log bekleniyor…
      </div>
    )
  }

  return (
    <div
      ref={(node) => {
        containerRef.current = node
        if (scrollRef) scrollRef.current = node
      }}
      onScroll={handleScroll}
      className={`rounded-lg bg-black/70 border border-zinc-800 ${maxHeightClass} overflow-y-auto`}
    >
      <pre className="px-3 py-2 text-[11px] leading-relaxed font-mono text-zinc-300 whitespace-pre-wrap break-all">
        {logs.map((line, i) => (
          <div
            key={`${i}-${line.slice(0, 24)}`}
            className={
              line.includes('✗') || line.includes('FATAL') || line.includes('ERROR')
                ? 'text-red-400'
                : line.includes('✓') || line.includes(' OK ')
                  ? 'text-emerald-400'
                  : line.includes('WARN')
                    ? 'text-amber-400'
                    : line.includes('EXEC') || line.includes('rclone:')
                      ? 'text-cyan-400/90'
                      : line.includes('poll #')
                        ? 'text-zinc-400'
                        : undefined
            }
          >
            {line}
          </div>
        ))}
        <div ref={bottomRef} />
      </pre>
    </div>
  )
}

function JobCard({
  job,
  onExpandLog,
  onDismiss,
}: {
  job: BackupJob
  onExpandLog: (job: BackupJob) => void
  onDismiss?: (job: BackupJob) => void
}) {
  const pct = Math.min(100, Math.max(0, job.progress ?? 0))
  const barColor =
    job.status === 'failed' ? 'bg-red-500' : job.status === 'completed' ? 'bg-emerald-500' : 'bg-blue-500'
  const jobId = job.id ?? job.jobId ?? '—'

  return (
    <div className="rounded-xl border border-zinc-800 bg-zinc-950/80 p-4 space-y-3">
      <div className="flex items-start justify-between gap-3 flex-wrap">
        <div className="flex items-start gap-2.5 min-w-0">
          {statusIcon(job.status)}
          <div className="min-w-0">
            <p className="text-white text-sm font-semibold">
              {jobTypeLabel(job)}
              <span className="text-zinc-500 font-normal text-xs ml-2">{job.source}</span>
            </p>
            <p className="text-zinc-400 text-xs mt-0.5">
              {phaseLabel(job.phase)} — {job.message ?? '…'}
            </p>
            <p className="text-zinc-600 text-[10px] font-mono mt-1 truncate" title={jobId}>
              {jobId}
            </p>
          </div>
        </div>
        <div className="flex items-start gap-2 shrink-0">
          <div className="text-right space-y-0.5">
            <span className="text-white text-lg font-bold tabular-nums">{pct}%</span>
            {job.etaSeconds ? <p className="text-zinc-500 text-[10px]">ETA {formatETA(job.etaSeconds)}</p> : null}
            {job.speed ? <p className="text-blue-400/80 text-[10px] font-mono">{job.speed}</p> : null}
          </div>
          {onDismiss && (job.status === 'running' || job.status === 'pending') && (
            <Button
              type="button"
              size="sm"
              variant="ghost"
              className="h-8 w-8 p-0 text-zinc-500 hover:text-amber-300"
              title="Yarım kalan işlemi kapat"
              onClick={() => onDismiss(job)}
            >
              <X className="w-4 h-4" />
            </Button>
          )}
        </div>
      </div>

      <PhaseTimeline job={job} />

      <div className="grid grid-cols-2 sm:grid-cols-4 gap-2 text-[10px]">
        <div className="rounded bg-zinc-900/80 px-2 py-1.5 border border-zinc-800">
          <span className="text-zinc-600 block">Yazılan</span>
          <span className="text-zinc-300 font-mono">{formatBytes(job.bytesDone)}</span>
        </div>
        <div className="rounded bg-zinc-900/80 px-2 py-1.5 border border-zinc-800">
          <span className="text-zinc-600 block">Hedef / çıktı</span>
          <span className="text-zinc-300 font-mono">{formatBytes(job.bytesTotal || job.sizeEstimate)}</span>
        </div>
        <div className="rounded bg-zinc-900/80 px-2 py-1.5 border border-zinc-800">
          <span className="text-zinc-600 block">Süre</span>
          <span className="text-zinc-300 font-mono">{formatDuration(job.startedAt, job.doneAt)}</span>
        </div>
        <div className="rounded bg-zinc-900/80 px-2 py-1.5 border border-zinc-800">
          <span className="text-zinc-600 block">Log satırı</span>
          <span className="text-zinc-300 font-mono">{job.logs?.length ?? 0}</span>
        </div>
      </div>

      {job.command && (
        <div className="space-y-1">
          <p className="text-zinc-600 text-[10px] uppercase tracking-wide flex items-center gap-1">
            <Terminal className="w-3 h-3" />
            Aktif komut
          </p>
          <code className="block text-[11px] font-mono text-cyan-300/90 bg-black/50 border border-zinc-800 rounded px-2 py-1.5 break-all">
            {job.command}
          </code>
        </div>
      )}

      <div className="h-2 rounded-full bg-zinc-800 overflow-hidden" role="progressbar" aria-valuenow={pct}>
        <div
          className={`h-full rounded-full transition-all duration-500 ${barColor}`}
          style={{ width: `${Math.max(pct, pct > 0 ? 2 : 0)}%` }}
        />
      </div>

      <div className="space-y-1.5">
        <div className="flex items-center justify-between gap-2">
          <p className="text-zinc-600 text-[10px] uppercase tracking-wide flex items-center gap-1">
            <Terminal className="w-3 h-3" />
            Teknik log ({job.logs?.length ?? 0} satır)
          </p>
          {(job.logs?.length ?? 0) > 0 && (
            <Button
              type="button"
              size="sm"
              variant="ghost"
              className="h-7 text-[10px] text-zinc-400 hover:text-white"
              onClick={() => onExpandLog(job)}
            >
              <Maximize2 className="w-3 h-3 mr-1" />
              Tam ekran
            </Button>
          )}
        </div>
        <JobLogTerminal logs={job.logs} />
      </div>

      {job.status === 'failed' && job.error && (
        <div className="rounded border border-red-500/30 bg-red-500/10 px-3 py-2 space-y-1">
          <p className="text-red-300 text-xs font-medium">Hata</p>
          <p className="text-red-400/90 text-[11px] mt-1">{humanizeJobError(job.error)}</p>
          <p className="text-amber-200/70 text-[10px]">{backupOperationHint(job.error)}</p>
        </div>
      )}
      {job.outputFile && (
        <p className="text-emerald-500/80 text-[11px] font-mono truncate">çıktı: {job.outputFile}</p>
      )}
    </div>
  )
}

interface LiveJobsPanelProps {
  jobs: BackupJob[]
  onDismissJob?: (job: BackupJob) => void
}

export default function LiveJobsPanel({ jobs, onDismissJob }: LiveJobsPanelProps) {
  const [logModalJob, setLogModalJob] = useState<BackupJob | null>(null)
  const [pausedScroll, setPausedScroll] = useState(false)
  const modalScrollRef = useRef<HTMLDivElement | null>(null)

  const copyLogs = useCallback(async (job: BackupJob) => {
    const text = (job.logs ?? []).join('\n')
    if (!text) return
    try {
      await navigator.clipboard.writeText(text)
      toast.success('Log panoya kopyalandı')
    } catch {
      toast.error('Kopyalama başarısız')
    }
  }, [])

  if (jobs.length === 0) return null

  return (
    <div className="rounded-xl border border-blue-500/25 bg-blue-950/20 overflow-hidden">
      <div className="flex items-center justify-between px-4 py-3 border-b border-blue-500/20 bg-blue-950/30">
        <div className="flex items-center gap-2">
          <Activity className="w-4 h-4 text-blue-400" />
          <h3 className="text-white text-sm font-semibold">Canlı işlemler</h3>
          <Badge className="bg-blue-500/15 text-blue-300 border-blue-500/30 text-[10px]">
            {jobs.length} aktif
          </Badge>
        </div>
        <span className="text-zinc-500 text-[10px] uppercase tracking-wide">verbose · SSE</span>
      </div>
      <div className="p-4 space-y-4">
        {jobs.map((job) => (
          <JobCard
            key={job.id ?? job.jobId}
            job={job}
            onExpandLog={setLogModalJob}
            onDismiss={onDismissJob}
          />
        ))}
      </div>

      <Dialog
        open={!!logModalJob}
        onOpenChange={(open) => {
          if (!open) {
            setLogModalJob(null)
            setPausedScroll(false)
          }
        }}
      >
        <DialogContent className="bg-zinc-950 border-zinc-800 max-w-4xl max-h-[90vh] flex flex-col">
          <DialogHeader>
            <DialogTitle className="text-white text-sm flex items-center justify-between gap-2 flex-wrap">
              <span>
                Teknik log — {logModalJob ? jobTypeLabel(logModalJob) : ''}
                {logModalJob?.id || logModalJob?.jobId ? (
                  <span className="text-zinc-500 font-normal text-xs ml-2 font-mono">
                    {logModalJob.id ?? logModalJob.jobId}
                  </span>
                ) : null}
              </span>
              {logModalJob && (logModalJob.logs?.length ?? 0) > 0 && (
                <Button
                  type="button"
                  size="sm"
                  variant="ghost"
                  className="h-7 text-xs text-zinc-400"
                  onClick={() => copyLogs(logModalJob)}
                >
                  <Copy className="w-3 h-3 mr-1" />
                  Kopyala
                </Button>
              )}
            </DialogTitle>
          </DialogHeader>
          {logModalJob && (
            <div className="flex-1 min-h-0 overflow-hidden space-y-2">
              {pausedScroll && (
                <p className="text-amber-400/80 text-[10px]">
                  Otomatik kaydırma duraklatıldı — en alta inince devam eder.
                </p>
              )}
              {logModalJob.command && (
                <code className="block text-[11px] font-mono text-cyan-300/90 bg-black/50 border border-zinc-800 rounded px-2 py-1.5 break-all">
                  {logModalJob.command}
                </code>
              )}
              <JobLogTerminal
                logs={logModalJob.logs}
                maxHeightClass="max-h-[65vh]"
                scrollRef={modalScrollRef}
                onUserScroll={() => setPausedScroll(true)}
              />
            </div>
          )}
        </DialogContent>
      </Dialog>
    </div>
  )
}
