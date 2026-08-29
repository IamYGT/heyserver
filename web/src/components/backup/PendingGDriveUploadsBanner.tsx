import { useMemo } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { CloudUpload, Loader2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { api } from '@/lib/api'
import { toast } from 'sonner'
import type { Backup, GDriveRemoteBackup } from '@/lib/types'

interface PendingGDriveUploadsBannerProps {
  backups: Backup[]
  driveReady: boolean
  onWatchJob?: (jobId: string) => void
}

function backupFileName(backup: Backup): string {
  if (backup.path) {
    const parts = backup.path.split('/')
    return parts[parts.length - 1] || backup.id
  }
  return backup.name || backup.id
}

export default function PendingGDriveUploadsBanner({
  backups,
  driveReady,
  onWatchJob,
}: PendingGDriveUploadsBannerProps) {
  const queryClient = useQueryClient()

  const { data: remoteData, isLoading } = useQuery<{ backups: GDriveRemoteBackup[] }>({
    queryKey: ['gdrive-remote'],
    queryFn: () => api.get<{ backups: GDriveRemoteBackup[] }>('/backups/gdrive/remote'),
    enabled: driveReady,
    staleTime: 120_000,
    refetchOnWindowFocus: false,
  })
  const remoteBackups = remoteData?.backups

  const pending = useMemo(() => {
    if (!driveReady || !remoteBackups) return []
    const remoteNames = new Set(remoteBackups.map((b) => b.name))
    return backups.filter(
      (b) =>
        b.status === 'completed' &&
        (b.type === 'files' || b.type === 'full' || b.type === 'database') &&
        !remoteNames.has(backupFileName(b)),
    )
  }, [backups, driveReady, remoteBackups])

  const uploadPendingMutation = useMutation({
    mutationFn: async (items: Backup[]) => {
      const jobIds: string[] = []
      for (const b of items) {
        const res = await api.post<{ jobId?: string }>(`/backups/upload/${b.id}`, {})
        if (res?.jobId) jobIds.push(res.jobId)
      }
      return jobIds
    },
    onSuccess: (jobIds) => {
      jobIds.forEach((id) => onWatchJob?.(id))
      queryClient.invalidateQueries({ queryKey: ['backup-jobs'] })
      queryClient.invalidateQueries({ queryKey: ['gdrive-remote'] })
      toast.info(
        jobIds.length === 1
          ? 'Drive yüklemesi başladı — canlı panelden izleyin'
          : `${jobIds.length} Drive yüklemesi kuyruğa alındı`,
      )
    },
    onError: (e: Error) => toast.error(e.message || 'Drive yüklemesi başlatılamadı'),
  })

  if (!driveReady || isLoading || pending.length === 0) return null

  const totalBytes = pending.reduce((sum, b) => sum + (b.size ?? 0), 0)
  const sizeLabel =
    totalBytes > 0
      ? `${(totalBytes / (1024 * 1024 * 1024)).toFixed(1)} GB`
      : `${pending.length} dosya`

  return (
    <div className="rounded-xl border border-blue-500/30 bg-blue-500/5 px-4 py-3 flex flex-wrap items-center justify-between gap-3">
      <div className="text-xs text-blue-200/90 space-y-0.5">
        <p className="font-medium text-blue-200 flex items-center gap-2">
          <CloudUpload className="w-4 h-4 shrink-0" />
          Drive&apos;a yüklenmemiş yerel yedekler ({pending.length})
        </p>
        <p className="text-blue-200/70">
          Toplam ~{sizeLabel} — disk temizliği öncesi uzak kopyayı tamamlayın.
        </p>
      </div>
      <Button
        size="sm"
        className="bg-blue-600 hover:bg-blue-500 text-white shrink-0"
        disabled={uploadPendingMutation.isPending}
        onClick={() => uploadPendingMutation.mutate(pending)}
      >
        {uploadPendingMutation.isPending ? (
          <Loader2 className="w-3.5 h-3.5 mr-1.5 animate-spin" />
        ) : (
          <CloudUpload className="w-3.5 h-3.5 mr-1.5" />
        )}
        Bekleyenleri Drive&apos;a yükle
      </Button>
    </div>
  )
}
