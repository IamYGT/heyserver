import { History, CheckCircle2, XCircle, Loader2, RefreshCw } from 'lucide-react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import type { BackupJob } from '@/lib/types'
import { formatDuration, historyPhaseLabel, jobTypeLabel } from '@/components/backup/phaseLabels'
import { backupOperationHint, humanizeJobError } from '@/components/backup/backupErrorHints'

function formatWhen(iso?: string): string {
  if (!iso) return '—'
  return new Date(iso).toLocaleString('tr-TR', {
    day: '2-digit',
    month: 'short',
    hour: '2-digit',
    minute: '2-digit',
  })
}

function StatusBadge({ job }: { job: BackupJob }) {
  if (job.status === 'completed') {
    return (
      <Badge className="bg-emerald-500/10 text-emerald-400 border-emerald-500/20 text-xs gap-1">
        <CheckCircle2 className="w-3 h-3" />
        Tamam
      </Badge>
    )
  }
  if (job.status === 'failed') {
    return (
      <Badge className="bg-red-500/10 text-red-400 border-red-500/20 text-xs gap-1">
        <XCircle className="w-3 h-3" />
        Hata
      </Badge>
    )
  }
  return (
    <Badge className="bg-blue-500/10 text-blue-400 border-blue-500/20 text-xs gap-1">
      <Loader2 className="w-3 h-3 animate-spin" />
      Devam
    </Badge>
  )
}

interface JobHistoryTableProps {
  jobs: BackupJob[]
  isLoading?: boolean
  error?: Error | null
  isFetching?: boolean
  onRetry?: () => void
}

export default function JobHistoryTable({ jobs, isLoading, error, isFetching, onRetry }: JobHistoryTableProps) {
  const sorted = [...jobs].sort((a, b) => {
    const ta = new Date(a.startedAt ?? 0).getTime()
    const tb = new Date(b.startedAt ?? 0).getTime()
    return tb - ta
  })

  return (
    <Card className="bg-zinc-900 border-zinc-800">
      <CardHeader className="pb-3 border-b border-zinc-800">
        <CardTitle className="text-white text-base flex items-center gap-2">
          <History className="w-4 h-4 text-zinc-400" />
          İş geçmişi
          <span className="text-zinc-600 text-xs font-normal">son 24 saat</span>
        </CardTitle>
      </CardHeader>
      <CardContent className="p-0">
        {isLoading ? (
          <p className="text-zinc-500 text-sm p-4">Yükleniyor…</p>
        ) : error ? (
          <div className="p-5 text-center">
            <XCircle className="mx-auto size-4 text-red-400" />
            <p className="mt-2 text-sm text-red-300">Yedekleme iş geçmişi yüklenemedi.</p>
            <p className="mt-1 text-xs text-zinc-600">{error.message}</p>
            {onRetry && (
              <Button type="button" variant="outline" size="sm" className="mt-4 border-red-500/30 text-red-200" onClick={onRetry} disabled={isFetching}>
                <RefreshCw className={`mr-2 size-3.5 ${isFetching ? 'animate-spin' : ''}`} />Tekrar dene
              </Button>
            )}
          </div>
        ) : sorted.length === 0 ? (
          <p className="text-zinc-500 text-sm p-4">Henüz kayıtlı iş yok.</p>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-sm min-w-[720px]">
              <thead>
                <tr className="border-b border-zinc-800">
                  <th className="text-left text-zinc-500 font-medium px-5 py-3">İşlem</th>
                  <th className="text-left text-zinc-500 font-medium px-4 py-3">Aşama</th>
                  <th className="text-left text-zinc-500 font-medium px-4 py-3">Başlangıç</th>
                  <th className="text-left text-zinc-500 font-medium px-4 py-3">Süre</th>
                  <th className="text-left text-zinc-500 font-medium px-4 py-3">Durum</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-zinc-800">
                {sorted.map((job) => (
                  <tr key={job.id ?? job.jobId} className="hover:bg-zinc-800/30 align-top">
                    <td className="px-5 py-3">
                      <p className="text-white text-sm">{jobTypeLabel(job)}</p>
                      <p className="text-zinc-600 text-xs truncate max-w-[220px]">
                        {job.outputFile ?? job.message}
                      </p>
                    </td>
                    <td className="px-4 py-3 text-zinc-400 text-xs">{historyPhaseLabel(job)}</td>
                    <td className="px-4 py-3 text-zinc-400 text-xs">{formatWhen(job.startedAt)}</td>
                    <td className="px-4 py-3 text-zinc-400 text-xs tabular-nums">
                      {formatDuration(job.startedAt, job.doneAt)}
                    </td>
                    <td className="px-4 py-3 max-w-[280px]">
                      <StatusBadge job={job} />
                      {job.status === 'failed' && job.error && (
                        <div className="mt-1.5 space-y-1">
                          <p className="text-red-400/80 text-[10px] break-words" title={job.error}>
                            {humanizeJobError(job.error)}
                          </p>
                          <p className="text-amber-200/70 text-[10px] leading-snug">
                            {backupOperationHint(job.error)}
                          </p>
                        </div>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </CardContent>
    </Card>
  )
}
