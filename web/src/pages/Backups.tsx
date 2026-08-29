import { useEffect, useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useSearchParams } from 'react-router-dom'
import {
  Archive,
  Plus,
  Download,
  Trash2,
  RotateCcw,
  Loader2,
  Database,
  Files,
  HardDrive,
  AlertTriangle,
  Clock,
  CalendarClock,
  Pencil,
  CloudUpload,
  RefreshCw,
} from 'lucide-react'
import GDriveSection from '@/components/backup/GDriveSection'
import SnapshotSection from '@/components/backup/SnapshotSection'
import BackupOverviewStrip from '@/components/backup/BackupOverviewStrip'
import BackupStrategyStrip from '@/components/backup/BackupStrategyStrip'
import LiveJobsPanel from '@/components/backup/LiveJobsPanel'
import JobHistoryTable from '@/components/backup/JobHistoryTable'
import InvalidBackupsBanner from '@/components/backup/InvalidBackupsBanner'
import PendingGDriveUploadsBanner from '@/components/backup/PendingGDriveUploadsBanner'
import { useBackupJobs } from '@/hooks/useBackupJobs'
import type { GDriveStatus } from '@/lib/types'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from '@/components/ui/dialog'
import { api, ApiError } from '@/lib/api'
import { toast } from 'sonner'
import type {
  Backup,
  BackupRestoreValidation,
  BackupStorageSummary,
  CreateBackupRequest,
  BackupSchedule,
  BackupScheduleRequest,
  BackupScheduleDeleteRequest,
} from '@/lib/types'
import EmptyState from '@/components/EmptyState'
import { DependencyRemediation } from '@/components/DependencyRemediation'
import { invalidBackupHint } from '@/components/backup/backupErrorHints'

// ---- Helpers ----

function formatBytes(bytes: number): string {
  if (bytes === 0) return '0 B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return `${parseFloat((bytes / Math.pow(k, i)).toFixed(1))} ${sizes[i]}`
}

function formatDate(iso: string): string {
  return new Date(iso).toLocaleString('en-GB', {
    day: '2-digit',
    month: 'short',
    year: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  })
}

function frequencyLabel(f: NonNullable<BackupSchedule['frequency']>): string {
  return { daily: 'Günlük', weekly: 'Haftalık', monthly: 'Aylık' }[f]
}

function backupScheduleLabel(schedule: BackupSchedule): string {
  if (schedule.frequency && schedule.time) {
    return `${frequencyLabel(schedule.frequency)} ${schedule.time}`
  }
  return `Özel cron ${schedule.cron}`
}

// ---- StatusBadge ----

function isBackupUsable(backup: Backup): boolean {
  return backup.status === 'completed'
}

function StatusBadge({ status }: { status: Backup['status'] }) {
  if (status === 'orphaned') {
    return (
      <Badge className="bg-red-500/10 text-red-300 border-red-500/20 text-xs">
        Yarım kalmış
      </Badge>
    )
  }
  if (status === 'invalid') {
    return (
      <Badge className="bg-amber-500/10 text-amber-400 border-amber-500/20 text-xs">
        Geçersiz
      </Badge>
    )
  }
  if (status === 'completed') {
    return (
      <Badge className="bg-green-500/10 text-green-400 border-green-500/20 text-xs">
        Tamamlandı
      </Badge>
    )
  }
  if (status === 'running') {
    return (
      <Badge className="bg-blue-500/10 text-blue-400 border-blue-500/20 text-xs flex items-center gap-1">
        <Loader2 className="w-2.5 h-2.5 animate-spin" />
        Çalışıyor
      </Badge>
    )
  }
  if (status === 'pending') {
    return (
      <Badge className="bg-zinc-500/10 text-zinc-400 border-zinc-500/20 text-xs flex items-center gap-1">
        <Clock className="w-2.5 h-2.5" />
        Bekliyor
      </Badge>
    )
  }
  return (
    <Badge className="bg-red-500/10 text-red-400 border-red-500/20 text-xs">
      Başarısız
    </Badge>
  )
}

// ---- Local backup storage pressure ----

function BackupStorageCard({
  storage,
  orphaned,
  onCleanup,
}: {
  storage?: BackupStorageSummary
  orphaned: Backup[]
  onCleanup: () => void
}) {
  if (!storage) return null
  const critical = storage.rootUsePercent >= 95
  const backupVolumeCritical = storage.backupVolumeUsePercent >= 95
  return (
    <Card className={`border ${critical ? 'border-red-500/30 bg-red-500/[0.04]' : 'border-zinc-800 bg-zinc-900'}`}>
      <CardContent className="p-4">
        <div className="flex flex-col gap-4 lg:flex-row lg:items-center lg:justify-between">
          <div className="flex min-w-0 items-start gap-3">
            <div className={`rounded-lg p-2 ${critical ? 'bg-red-500/10 text-red-400' : 'bg-blue-500/10 text-blue-400'}`}>
              <HardDrive className="h-5 w-5" />
            </div>
            <div className="min-w-0">
              <div className="flex flex-wrap items-center gap-2">
                <p className="font-semibold text-white">Yerel yedek depolaması</p>
                <Badge className={critical ? 'border-red-500/20 bg-red-500/10 text-red-300' : 'border-zinc-700 bg-zinc-800 text-zinc-300'}>
                  Root %{storage.rootUsePercent.toFixed(1)}
                </Badge>
                <Badge className={backupVolumeCritical ? 'border-red-500/20 bg-red-500/10 text-red-300' : 'border-emerald-500/20 bg-emerald-500/10 text-emerald-300'}>
                  Yedek diski %{storage.backupVolumeUsePercent.toFixed(1)}
                </Badge>
              </div>
              <p className="mt-1 text-xs text-zinc-400">
                Aktif yedekler {formatBytes(storage.activeBytes)} · yedek diskinde {formatBytes(storage.backupVolumeAvailable)} kullanılabilir
              </p>
              {storage.orphanedCount > 0 ? (
                <p className="mt-2 text-xs leading-relaxed text-red-300">
                  {storage.orphanedCount} yarım kalmış tam-yedek artığı {formatBytes(storage.orphanedBytes)} alan tutuyor.
                  {storage.legacyOrphanedCount > 0 && ` ${storage.legacyOrphanedCount} tanesi eski root yedek dizininde ${formatBytes(storage.legacyOrphanedBytes)} kullanıyor.`}
                  {' '}Bunlar geri yüklenebilir yedek olarak kabul edilmez.
                </p>
              ) : (
                <p className="mt-2 text-xs text-emerald-400">Yarım kalmış yedek artığı bulunamadı.</p>
              )}
            </div>
          </div>
          {orphaned.length > 0 && (
            <Button onClick={onCleanup} className="shrink-0 bg-red-600 text-white hover:bg-red-500">
              <Trash2 className="mr-2 h-4 w-4" />
              Artıkları incele · {formatBytes(storage.orphanedBytes)}
            </Button>
          )}
        </div>
      </CardContent>
    </Card>
  )
}

// ---- TypeIcon ----

function TypeIcon({ type }: { type: Backup['type'] }) {
  if (type === 'database') return <Database className="w-4 h-4 text-purple-400" />
  if (type === 'files') return <Files className="w-4 h-4 text-amber-400" />
  return <HardDrive className="w-4 h-4 text-blue-400" />
}

// ---- Create Backup Dialog ----

interface CreateDialogProps {
  open: boolean
  onClose: () => void
  onSubmit: (req: CreateBackupRequest) => void
  isPending: boolean
}

function CreateBackupDialog({ open, onClose, onSubmit, isPending }: CreateDialogProps) {
  const [type, setType] = useState<CreateBackupRequest['type']>('full')
  const [dbName, setDbName] = useState('')
  const [compressionLevel, setCompressionLevel] = useState(6)
  const [selectedVhosts, setSelectedVhosts] = useState<string[]>([])
  const [filesScope, setFilesScope] = useState<'all' | 'sites'>('all')

  const vhostsQuery = useQuery<{ vhosts: string[] }>({
    queryKey: ['backup-vhosts'],
    queryFn: () => api.get<{ vhosts: string[] }>('/backups/snapshot/vhosts'),
    enabled: open && (type === 'files' || type === 'full'),
  })
  const vhosts = vhostsQuery.data?.vhosts ?? []
  const selectedSitesUnavailable = filesScope === 'sites' && (vhostsQuery.isLoading || vhostsQuery.isError)

  const toggleVhost = (name: string) => {
    setSelectedVhosts((prev) =>
      prev.includes(name) ? prev.filter((v) => v !== name) : [...prev, name],
    )
  }

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault()
    if (selectedSitesUnavailable) return
    if ((type === 'files' || type === 'full') && filesScope === 'sites' && selectedVhosts.length === 0) {
      toast.error('En az bir site seçin')
      return
    }
    onSubmit({
      type,
      compression: compressionLevel,
      database: type === 'database' ? dbName : undefined,
      engine: type === 'database' ? 'postgresql' : undefined,
      retention: 10,
      vhosts: filesScope === 'sites' ? selectedVhosts : undefined,
    })
  }

  return (
    <Dialog open={open} onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="bg-zinc-900 border-zinc-800 text-white max-w-xl max-h-[85vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle className="text-white">Yedek Oluştur</DialogTitle>
        </DialogHeader>
        <form onSubmit={handleSubmit} className="space-y-4 mt-2">
          <div className="space-y-1.5">
            <label className="text-zinc-400 text-sm">Yedek türü</label>
            <div className="grid grid-cols-3 gap-2">
              {(['full', 'database', 'files'] as const).map((t) => (
                <button
                  key={t}
                  type="button"
                  onClick={() => setType(t)}
                  className={`px-3 py-2 rounded-lg border text-sm capitalize transition-colors ${
                    type === t
                      ? 'border-blue-500 bg-blue-600/10 text-blue-400'
                      : 'border-zinc-700 text-zinc-400 hover:border-zinc-600 hover:text-white'
                  }`}
                >
                  {t}
                </button>
              ))}
            </div>
          </div>

          {type === 'database' && (
            <div className="space-y-1.5">
              <label className="text-zinc-400 text-sm">Veritabanı adı</label>
              <input
                type="text"
                required
                value={dbName}
                onChange={(e) => setDbName(e.target.value)}
                placeholder="e.g. myapp_db"
                className="w-full bg-zinc-800 border border-zinc-700 rounded-lg px-3 py-2 text-sm text-white placeholder:text-zinc-600 focus:outline-none focus:border-blue-500 transition-colors"
              />
            </div>
          )}

          {(type === 'files' || type === 'full') && (
            <div className="space-y-2">
              <label className="text-zinc-400 text-sm">Dosya kapsamı</label>
              <div className="grid grid-cols-2 gap-2">
                <button
                  type="button"
                  onClick={() => setFilesScope('all')}
                  className={`px-3 py-2 rounded-lg border text-sm transition-colors ${
                    filesScope === 'all'
                      ? 'border-blue-500 bg-blue-600/10 text-blue-400'
                      : 'border-zinc-700 text-zinc-400 hover:border-zinc-600'
                  }`}
                >
                  Tüm vhosts
                </button>
                <button
                  type="button"
                  onClick={() => setFilesScope('sites')}
                  className={`px-3 py-2 rounded-lg border text-sm transition-colors ${
                    filesScope === 'sites'
                      ? 'border-blue-500 bg-blue-600/10 text-blue-400'
                      : 'border-zinc-700 text-zinc-400 hover:border-zinc-600'
                  }`}
                >
                  Seçili siteler
                </button>
              </div>
              {filesScope === 'sites' && (
                <div className="max-h-40 overflow-y-auto rounded-lg border border-zinc-800 bg-zinc-950/50 p-2 space-y-1">
                  {vhostsQuery.isLoading ? (
                    <p className="text-zinc-500 text-xs p-2">Site listesi yükleniyor…</p>
                  ) : vhostsQuery.isError ? (
                    <div className="p-2 text-center">
                      <p className="text-xs text-red-300">Site listesi yüklenemedi.</p>
                      <p className="mt-1 text-[11px] text-zinc-600">{vhostsQuery.error.message}</p>
                      <Button type="button" variant="outline" size="sm" className="mt-3 border-red-500/30 text-red-200" onClick={() => { void vhostsQuery.refetch() }} disabled={vhostsQuery.isFetching}>
                        <RefreshCw className={`mr-2 size-3.5 ${vhostsQuery.isFetching ? 'animate-spin' : ''}`} />Tekrar dene
                      </Button>
                    </div>
                  ) : vhosts.length === 0 ? (
                    <p className="text-zinc-500 text-xs p-2">Sunucuda yedeklenebilir vhost bulunamadı.</p>
                  ) : (
                    vhosts.map((v) => (
                      <label
                        key={v}
                        className="flex items-center gap-2 text-xs text-zinc-300 cursor-pointer hover:bg-zinc-800/50 rounded px-2 py-1"
                      >
                        <input
                          type="checkbox"
                          checked={selectedVhosts.includes(v)}
                          onChange={() => toggleVhost(v)}
                          className="rounded border-zinc-600"
                        />
                        {v}
                      </label>
                    ))
                  )}
                </div>
              )}
            </div>
          )}

          <div className="space-y-1.5">
            <label className="text-zinc-400 text-sm">Sıkıştırma düzeyi</label>
            <div className="grid grid-cols-3 gap-2">
              {([
                { level: 1, label: 'Hızlı' },
                { level: 6, label: 'Dengeli' },
                { level: 9, label: 'En küçük' },
              ] as const).map((option) => (
                <button
                  key={option.level}
                  type="button"
                  onClick={() => setCompressionLevel(option.level)}
                  className={`px-3 py-2 rounded-lg border text-sm transition-colors ${
                    compressionLevel === option.level
                      ? 'border-blue-500 bg-blue-600/10 text-blue-400'
                      : 'border-zinc-700 text-zinc-400 hover:border-zinc-600 hover:text-white'
                  }`}
                >
                  {option.label}
                </button>
              ))}
            </div>
            <p className="text-[11px] leading-relaxed text-zinc-500">
              Gzip seviyesi {compressionLevel}/9. Kaynak boyutu arka planda ölçülür; kapasite uygunsa yedek otomatik başlar.
            </p>
          </div>

          <DialogFooter className="gap-2 pt-2">
            <Button
              type="button"
              variant="ghost"
              onClick={onClose}
              className="text-zinc-400 hover:text-white"
            >
              İptal
            </Button>
            <Button
              type="submit"
              disabled={isPending || selectedSitesUnavailable}
              className="bg-blue-600 hover:bg-blue-500 text-white"
            >
              {isPending && <Loader2 className="w-3.5 h-3.5 mr-2 animate-spin" />}
              {isPending ? 'Başlatılıyor…' : 'Ön kontrolü başlat'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

// ---- Schedule Dialog ----

interface ScheduleDialogProps {
  open: boolean
  onClose: () => void
  current: BackupSchedule | null
  onSubmit: (s: BackupScheduleRequest) => void
  isPending: boolean
}

function ScheduleDialog({ open, onClose, current, onSubmit, isPending }: ScheduleDialogProps) {
  const [frequency, setFrequency] = useState<NonNullable<BackupSchedule['frequency']>>(
    current?.frequency ?? 'daily',
  )
  const [time, setTime] = useState(current?.time ?? '03:00')
  const [retentionCount, setRetentionCount] = useState(
    current?.retention_count ?? current?.retention_days ?? 10,
  )

  // State is initialized from `current` props; dialog remounts via key prop when reopened

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault()
    onSubmit({ frequency, time, retention_count: retentionCount })
  }

  return (
    <Dialog open={open} onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="bg-zinc-900 border-zinc-800 text-white max-w-xl max-h-[85vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle className="text-white">
            {current ? 'Zamanlamayı Düzenle' : 'Otomatik Yedekleme Kur'}
          </DialogTitle>
        </DialogHeader>
        <form onSubmit={handleSubmit} className="space-y-4 mt-2">
          {/* Frequency */}
          <div className="space-y-1.5">
            <label className="text-zinc-400 text-sm">Sıklık</label>
            <div className="grid grid-cols-3 gap-2">
              {(['daily', 'weekly', 'monthly'] as const).map((f) => (
                <button
                  key={f}
                  type="button"
                  onClick={() => setFrequency(f)}
                  className={`px-3 py-2 rounded-lg border text-sm capitalize transition-colors ${
                    frequency === f
                      ? 'border-blue-500 bg-blue-600/10 text-blue-400'
                      : 'border-zinc-700 text-zinc-400 hover:border-zinc-600 hover:text-white'
                  }`}
                >
                  {f}
                </button>
              ))}
            </div>
          </div>

          {/* Time */}
          <div className="space-y-1.5">
            <label className="text-zinc-400 text-sm">Saat (sunucu saati)</label>
            <input
              type="time"
              required
              value={time}
              onChange={(e) => setTime(e.target.value)}
              className="w-full bg-zinc-800 border border-zinc-700 rounded-lg px-3 py-2 text-sm text-white focus:outline-none focus:border-blue-500 transition-colors [color-scheme:dark]"
            />
          </div>

          {/* Retention */}
          <div className="space-y-1.5">
            <label className="text-zinc-400 text-sm">
              Saklama — son{' '}
              <span className="text-white font-medium">{retentionCount}</span> yedeği tut
            </label>
            <input
              type="number"
              required
              min={1}
              max={365}
              value={retentionCount}
              onChange={(e) => setRetentionCount(Number(e.target.value))}
              className="w-full bg-zinc-800 border border-zinc-700 rounded-lg px-3 py-2 text-sm text-white focus:outline-none focus:border-blue-500 transition-colors"
            />
          </div>

          <DialogFooter className="gap-2 pt-2">
            <Button
              type="button"
              variant="ghost"
              onClick={onClose}
              className="text-zinc-400 hover:text-white"
            >
              İptal
            </Button>
            <Button
              type="submit"
              disabled={isPending}
              className="bg-blue-600 hover:bg-blue-500 text-white"
            >
              {isPending && <Loader2 className="w-3.5 h-3.5 mr-2 animate-spin" />}
              Kaydet
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

// ---- Delete Schedule Confirm Dialog ----

interface DeleteScheduleDialogProps {
  open: boolean
  onClose: () => void
  onConfirm: () => void
  isPending: boolean
}

function DeleteScheduleDialog({ open, onClose, onConfirm, isPending }: DeleteScheduleDialogProps) {
  return (
    <Dialog open={open} onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="bg-zinc-900 border-zinc-800 text-white max-w-md">
        <DialogHeader>
          <DialogTitle className="text-white flex items-center gap-2">
            <AlertTriangle className="w-5 h-5 text-amber-400" />
            Zamanlamayı Sil
          </DialogTitle>
        </DialogHeader>
        <p className="text-zinc-400 text-sm mt-2">
          Otomatik yedekleme zamanlaması kaldırılsın mı? Mevcut yedek dosyaları etkilenmez.
        </p>
        <DialogFooter className="gap-2 pt-4">
          <Button
            type="button"
            variant="ghost"
            onClick={onClose}
            className="text-zinc-400 hover:text-white"
          >
            İptal
          </Button>
          <Button
            disabled={isPending}
            onClick={onConfirm}
            className="bg-red-600 hover:bg-red-500 text-white"
          >
            {isPending && <Loader2 className="w-3.5 h-3.5 mr-2 animate-spin" />}
            Sil
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ---- Restore Confirm Dialog ----

interface RestoreDialogProps {
  backup: Backup | null
  onClose: () => void
  onConfirm: () => void
  isPending: boolean
}

function RestoreDialog({ backup, onClose, onConfirm, isPending }: RestoreDialogProps) {
  const [confirmed, setConfirmed] = useState(false)
  const preflightQuery = useQuery<BackupRestoreValidation>({
    queryKey: ['backup-restore-validation', backup?.id],
    queryFn: () => api.get<BackupRestoreValidation>(`/backups/restore/${backup?.id}/validate`),
    enabled: Boolean(backup),
    retry: false,
  })

  const handleClose = () => {
    setConfirmed(false)
    onClose()
  }

  const preflightReady = Boolean(preflightQuery.data && !preflightQuery.isError && !preflightQuery.isFetching)

  if (!backup) return null

  return (
    <Dialog open={!!backup} onOpenChange={(v) => !v && handleClose()}>
      <DialogContent className="bg-zinc-900 border-zinc-800 text-white max-w-xl max-h-[85vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle className="text-white flex items-center gap-2">
            <AlertTriangle className="w-5 h-5 text-amber-400" />
            Yedeği Geri Yükle
          </DialogTitle>
        </DialogHeader>
        <div className="space-y-4 mt-2">
          <p className="text-zinc-400 text-sm">
            Şu yedeği geri yüklemek istediğinize emin misiniz:{' '}
            <span className="text-white font-medium">
              {backup.path ? backup.path.split('/').pop() : backup.id}
            </span>
            ?
          </p>
          {preflightQuery.isLoading ? (
            <div className="flex items-center gap-2 rounded-lg border border-blue-500/20 bg-blue-500/10 px-4 py-3 text-sm text-blue-300">
              <Loader2 className="size-4 animate-spin" />
              Yedek artifact'i baştan sona doğrulanıyor…
            </div>
          ) : preflightQuery.isError ? (
            <div className="rounded-lg border border-red-500/20 bg-red-500/10 px-4 py-3">
              <p className="text-sm font-medium text-red-300">Geri yükleme ön kontrolü başarısız.</p>
              <p className="mt-1 break-words font-mono text-xs text-red-400/80">
                {preflightQuery.error instanceof Error ? preflightQuery.error.message : 'Yedek doğrulanamadı'}
              </p>
              <Button
                type="button"
                variant="outline"
                size="sm"
                className="mt-3 border-red-500/30 text-red-200"
                onClick={() => { void preflightQuery.refetch() }}
                disabled={preflightQuery.isFetching}
              >
                <RefreshCw className={`mr-2 size-3.5 ${preflightQuery.isFetching ? 'animate-spin' : ''}`} />
                Tekrar doğrula
              </Button>
            </div>
          ) : preflightQuery.data ? (
            <div className="space-y-3">
              <div className="rounded-lg border border-emerald-500/20 bg-emerald-500/10 px-4 py-3">
                <p className="text-sm font-medium text-emerald-300">Artifact bütünlüğü doğrulandı.</p>
                <p className="mt-1 text-xs text-emerald-200/70">
                  {formatBytes(preflightQuery.data.artifactBytes)} tamamen okundu. Bağlantı ve recovery point oluşturma işlem başlarken ayrıca doğrulanır.
                </p>
                {preflightQuery.data.includesDatabase && (
                  <p className="mt-2 text-xs text-zinc-300">
                    Veritabanı: <span className="font-mono text-white">{preflightQuery.data.databaseEngine} / {preflightQuery.data.databaseTarget}</span>
                  </p>
                )}
              </div>
              {preflightQuery.data.databaseRecovery && (
                <div className="rounded-lg border border-blue-500/20 bg-blue-500/10 px-4 py-3 text-sm text-blue-200">
                  Veritabanı değişmeden önce otomatik recovery point alınır; restore hatasında veritabanı otomatik geri yüklenir.
                </div>
              )}
              {preflightQuery.data.includesFiles && preflightQuery.data.filesRollback && (
                <div className="rounded-lg border border-blue-500/20 bg-blue-500/10 px-4 py-3 text-sm text-blue-200">
                  Dosyalar değişmeden önce recovery archive oluşturulur; extraction hatasında üzerine yazılan dosyalar otomatik geri yüklenir ve yeni oluşturulan yollar kaldırılır.
                </div>
              )}
              {preflightQuery.data.includesFiles && !preflightQuery.data.filesRollback && (
                <div className="rounded-lg border border-amber-500/20 bg-amber-500/10 px-4 py-3 text-sm text-amber-200">
                  Dosyaların üzerine yazılabilir. Dosya değişiklikleri için otomatik rollback yoktur.
                </div>
              )}
            </div>
          ) : null}
          <label className={`flex items-center gap-2 select-none ${preflightReady ? 'cursor-pointer' : 'cursor-not-allowed opacity-60'}`}>
            <input
              type="checkbox"
              checked={confirmed}
              onChange={(e) => setConfirmed(e.target.checked)}
              disabled={!preflightReady}
              className="accent-blue-500"
            />
            <span className="text-zinc-400 text-sm">Restore kapsamını ve rollback sınırlarını anlıyorum</span>
          </label>
        </div>
        <DialogFooter className="gap-2 pt-2">
          <Button
            type="button"
            variant="ghost"
            onClick={handleClose}
            className="text-zinc-400 hover:text-white"
          >
            İptal
          </Button>
          <Button
            disabled={!confirmed || !preflightReady || isPending}
            onClick={onConfirm}
            className="bg-red-600 hover:bg-red-500 text-white"
          >
            {isPending && <Loader2 className="w-3.5 h-3.5 mr-2 animate-spin" />}
            Geri Yükle
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ---- Delete Backup Confirm Dialog ----

interface DeleteDialogProps {
  backup: Backup | null
  onClose: () => void
  onConfirm: () => void
  isPending: boolean
}

function DeleteDialog({ backup, onClose, onConfirm, isPending }: DeleteDialogProps) {
  if (!backup) return null
  return (
    <Dialog open={!!backup} onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="bg-zinc-900 border-zinc-800 text-white max-w-md">
        <DialogHeader>
          <DialogTitle className="text-white">Yedeği Sil</DialogTitle>
        </DialogHeader>
        <p className="text-zinc-400 text-sm mt-2">
          <span className="text-white font-medium">
            {backup.path ? backup.path.split('/').pop() : backup.id}
          </span>{' '}
          silinsin mi? Bu işlem geri alınamaz.
        </p>
        <DialogFooter className="gap-2 pt-4">
          <Button
            type="button"
            variant="ghost"
            onClick={onClose}
            className="text-zinc-400 hover:text-white"
          >
            İptal
          </Button>
          <Button
            disabled={isPending}
            onClick={onConfirm}
            className="bg-red-600 hover:bg-red-500 text-white"
          >
            {isPending && <Loader2 className="w-3.5 h-3.5 mr-2 animate-spin" />}
            Sil
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ---- Orphaned partial cleanup dialog ----

function OrphanedCleanupDialog({
  open,
  backups,
  onClose,
  onConfirm,
  isPending,
}: {
  open: boolean
  backups: Backup[]
  onClose: () => void
  onConfirm: () => void
  isPending: boolean
}) {
  const [confirmed, setConfirmed] = useState(false)
  const total = backups.reduce((sum, backup) => sum + (backup.diskSize ?? backup.size ?? 0), 0)
  const close = () => {
    if (isPending) return
    setConfirmed(false)
    onClose()
  }

  return (
    <Dialog open={open} onOpenChange={(value) => !value && close()}>
      <DialogContent className="max-h-[85vh] max-w-2xl overflow-y-auto border-zinc-800 bg-zinc-900 text-white">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2 text-white">
            <AlertTriangle className="h-5 w-5 text-red-400" />
            Yarım kalmış yedek artıklarını temizle
          </DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          <div className="rounded-lg border border-red-500/20 bg-red-500/10 p-4">
            <p className="text-sm font-medium text-red-300">
              {backups.length} dosya silinecek · en fazla {formatBytes(total)} alan açılacak
            </p>
            <p className="mt-1 text-xs leading-relaxed text-red-200/70">
              Yalnızca kesilmiş veya başarısız tam-yedek işlemlerinden kalan <code>-partial-</code> ve <code>.part</code> dosyaları seçildi.
              Tamamlanmış yedekler bu işleme dahil edilmez. Aktif bir yedek işi varsa sunucu temizliği reddeder.
            </p>
          </div>
          <div className="max-h-64 space-y-1 overflow-y-auto rounded-lg border border-zinc-800 bg-zinc-950/60 p-2">
            {backups.map((backup) => (
              <div key={backup.id} className="flex items-center justify-between gap-4 rounded px-2 py-2 text-xs hover:bg-zinc-800/50">
                <span className="min-w-0 truncate font-mono text-zinc-300" title={backup.path}>{backup.path?.split('/').pop() ?? backup.id}</span>
                <span className="shrink-0 tabular-nums text-zinc-500">{formatBytes(backup.diskSize ?? backup.size ?? 0)}</span>
              </div>
            ))}
          </div>
          <label className="flex cursor-pointer select-none items-start gap-2 rounded-lg border border-zinc-800 p-3">
            <input
              type="checkbox"
              checked={confirmed}
              onChange={(event) => setConfirmed(event.target.checked)}
              className="mt-0.5 accent-red-500"
            />
            <span className="text-sm text-zinc-300">
              Listelenen dosyaların geri yüklenebilir tamamlanmış yedekler olmadığını ve silineceğini onaylıyorum.
            </span>
          </label>
        </div>
        <DialogFooter className="gap-2 pt-2">
          <Button type="button" variant="ghost" onClick={close} disabled={isPending} className="text-zinc-400 hover:text-white">
            İptal
          </Button>
          <Button disabled={!confirmed || isPending || backups.length === 0} onClick={onConfirm} className="bg-red-600 text-white hover:bg-red-500">
            {isPending ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : <Trash2 className="mr-2 h-4 w-4" />}
            {formatBytes(total)} alanı temizle
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ---- Schedule Section ----

function ScheduleSection({
  schedules,
  isLoading,
  error,
  isFetching,
  onRetry,
  onEdit,
  onDelete,
}: {
  schedules: BackupSchedule[]
  isLoading: boolean
  error?: Error | null
  isFetching?: boolean
  onRetry: () => void
  onEdit: () => void
  onDelete: () => void
}) {
  const schedule = schedules[0] ?? null
  return (
    <Card className="bg-zinc-900 border-zinc-800">
      <CardHeader className="pb-3">
        <div className="flex items-center justify-between">
          <CardTitle className="text-white text-base flex items-center gap-2">
            <CalendarClock className="w-4 h-4 text-zinc-400" />
            Otomatik Zamanlama
          </CardTitle>
          {schedule && !isLoading && (
            <div className="flex items-center gap-2">
              <Button
                size="sm"
                variant="ghost"
                onClick={onEdit}
                className="h-7 px-2 text-zinc-400 hover:text-white"
              >
                <Pencil className="w-3.5 h-3.5 mr-1.5" />
                Düzenle
              </Button>
              <Button
                size="sm"
                variant="ghost"
                onClick={onDelete}
                className="h-7 px-2 text-zinc-400 hover:text-red-400"
              >
                <Trash2 className="w-3.5 h-3.5 mr-1.5" />
                Sil
              </Button>
            </div>
          )}
        </div>
      </CardHeader>
      <CardContent>
        {isLoading ? (
          <Skeleton className="h-10 w-full bg-zinc-800" />
        ) : error ? (
          <DependencyRemediation
            title="Cron zamanlama altyapısı kullanılamıyor"
            summary="HServer mevcut kullanıcı crontab'ını güvenle okuyamadığı için zamanlama oluşturma, değiştirme ve silme kontrollerini duraklattı. Mevcut cron kayıtları boş kabul edilmez ve üzerine yazılmaz."
            state="unavailable"
            error={error.message}
            steps={[
              <span>Sunucuda <code className="text-zinc-100">crontab</code> istemcisinin kurulu ve panel servis kullanıcısı tarafından çalıştırılabilir olduğunu doğrulayın.</span>,
              <span>Panel servis kullanıcısının kendi crontab'ını okuma ve yazma iznini kontrol edin.</span>,
              <span>Hata ayrıntısındaki paket, izin veya çalışma zamanı sorununu giderip tespiti yeniden deneyin.</span>,
            ]}
            retry={onRetry}
            retrying={isFetching}
          />
        ) : schedules.length > 0 ? (
          <div className="space-y-2">
            {schedules.map((s) => (
              <div key={s.rawLine} className="flex flex-wrap gap-3 items-center">
                <Badge className="bg-violet-500/10 text-violet-300 border-violet-500/20 text-[10px]">
                  {s.type === 'snapshot' ? 'Snapshot' : s.type === 'database' ? 'Veritabanı' : s.type ?? 'yedek'}
                </Badge>
                <span className="text-zinc-300 text-sm">
                  {backupScheduleLabel(s)} · son {s.retention_count} yedek saklanır
                </span>
              </div>
            ))}
          </div>
        ) : (
          <div className="flex items-center justify-between gap-4 py-1">
            <p className="text-zinc-500 text-sm">
              Zamanlama yok. Verilerinizi korumak için otomatik yedekleme kurun.
            </p>
            <Button
              size="sm"
              onClick={onEdit}
              className="bg-blue-600 hover:bg-blue-500 text-white shrink-0"
            >
              <Plus className="w-3.5 h-3.5 mr-1.5" />
              Zamanlama Kur
            </Button>
          </div>
        )}
      </CardContent>
    </Card>
  )
}

// ---- Main Page ----

export default function Backups() {
  const queryClient = useQueryClient()
  const [searchParams, setSearchParams] = useSearchParams()

  // Backup list state
  const [createOpen, setCreateOpen] = useState(false)
  const [restoreTarget, setRestoreTarget] = useState<Backup | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<Backup | null>(null)
  const [orphanCleanupOpen, setOrphanCleanupOpen] = useState(
    () => searchParams.get('cleanup') === 'orphaned',
  )

  const {
    activeJobs,
    historyJobs,
    isLoading: jobsLoading,
    isError: jobsError,
    error: jobsQueryError,
    isFetching: jobsFetching,
    refetch: refetchJobs,
    watchJob,
  } = useBackupJobs()

  // Schedule state
  const [scheduleDialogOpen, setScheduleDialogOpen] = useState(false)
  const [deleteScheduleOpen, setDeleteScheduleOpen] = useState(false)

  // ---- Queries ----

  const backupsQuery = useQuery<{ backups: Backup[]; storage: BackupStorageSummary }>({
    queryKey: ['backups'],
    queryFn: () => api.get<{ backups: Backup[]; storage: BackupStorageSummary }>('/backups'),
  })

  const gdriveStatusQuery = useQuery<GDriveStatus>({
    queryKey: ['gdrive-status'],
    queryFn: () => api.get<GDriveStatus>('/backups/gdrive/status'),
    staleTime: 60_000,
    refetchOnWindowFocus: false,
  })

  const scheduleQuery = useQuery<{ schedules: BackupSchedule[] }>({
    queryKey: ['backups-schedule'],
    queryFn: () => api.get<{ schedules: BackupSchedule[] }>('/backups/schedules'),
  })

  const backups = backupsQuery.data?.backups ?? []
  const storage = backupsQuery.data?.storage
  const gdriveStatus = gdriveStatusQuery.data
  const orphanedBackups = backups.filter((backup) => backup.status === 'orphaned')
  const completedBackups = backups.filter((backup) => backup.status === 'completed')
  const schedules = scheduleQuery.data?.schedules ?? []
  const schedule = schedules[0] ?? null
  const gdriveReady = gdriveStatus?.connected && gdriveStatus?.rcloneFound
  const scheduleState = scheduleQuery.isLoading ? 'loading' : scheduleQuery.isError ? 'unavailable' : 'ready'
  const driveState = gdriveStatusQuery.isLoading
    ? 'loading'
    : gdriveStatusQuery.isError
      ? 'unavailable'
      : gdriveStatus?.connected
        ? 'connected'
        : 'disconnected'

  useEffect(() => {
    if (backupsQuery.isLoading || backupsQuery.isError || searchParams.get('cleanup') !== 'orphaned') return
    const next = new URLSearchParams(searchParams)
    next.delete('cleanup')
    setSearchParams(next, { replace: true })
  }, [backupsQuery.isLoading, backupsQuery.isError, orphanedBackups.length, searchParams, setSearchParams])

  useEffect(() => {
    if (gdriveStatusQuery.isLoading || gdriveStatusQuery.isError || searchParams.get('focus') !== 'gdrive') return
    const scrollToGDrive = () => {
      document.getElementById('gdrive-section')?.scrollIntoView({ behavior: 'smooth', block: 'start' })
    }
    window.requestAnimationFrame(scrollToGDrive)
    window.setTimeout(scrollToGDrive, 500)
    const next = new URLSearchParams(searchParams)
    next.delete('focus')
    setSearchParams(next, { replace: true })
  }, [gdriveStatusQuery.isLoading, gdriveStatusQuery.isError, searchParams, setSearchParams])

  // ---- Mutations ----

  const uploadGDriveMutation = useMutation({
    mutationFn: (id: string) => api.post<{ jobId?: string }>(`/backups/upload/${id}`, {}),
    onSuccess: (res) => {
      if (res?.jobId) watchJob(res.jobId)
      toast.info('Google Drive yüklemesi başladı — canlı panelden izleyin')
    },
    onError: () => toast.error('Google Drive yüklemesi başlatılamadı'),
  })

  const createMutation = useMutation({
    mutationFn: (req: CreateBackupRequest) =>
      api.post<{ jobId?: string; id?: string }>('/backups', req),
    onSuccess: (res) => {
      queryClient.invalidateQueries({ queryKey: ['backups'] })
      setCreateOpen(false)
      const jobId = res?.jobId ?? res?.id
      if (jobId) {
        watchJob(jobId)
        toast.info('Ön kontrol başladı — kapasite uygunsa yedek otomatik başlayacak')
      } else {
        toast.success('Yedek başlatıldı')
      }
    },
    onError: (error) => {
      const message = error instanceof ApiError ? error.message : 'Yedek oluşturulamadı'
      toast.error(message)
    },
  })

  const deleteMutation = useMutation({
    mutationFn: (id: string) => api.delete(`/backups/${id}`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['backups'] })
      setDeleteTarget(null)
      toast.success('Backup deleted')
    },
    onError: () => toast.error('Failed to delete backup'),
  })

  const restoreMutation = useMutation({
    mutationFn: (id: string) => api.post<{ jobId?: string }>(`/backups/restore/${id}`),
    onSuccess: (res) => {
      queryClient.invalidateQueries({ queryKey: ['backups'] })
      setRestoreTarget(null)
      if (res?.jobId) watchJob(res.jobId)
      toast.info('Geri yükleme başladı — canlı panelden izleyin')
    },
    onError: (error) => {
      const message = error instanceof Error ? error.message : 'Geri yükleme başlatılamadı'
      toast.error(message)
    },
  })

  const saveScheduleMutation = useMutation({
    mutationFn: (s: BackupScheduleRequest) => api.post('/backups/schedules', s),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['backups-schedule'] })
      setScheduleDialogOpen(false)
      toast.success('Schedule saved')
    },
    onError: (error) => toast.error(error instanceof Error ? error.message : 'Failed to save schedule'),
  })

  const dismissJobMutation = useMutation({
    mutationFn: (jobId: string) =>
      api.post(`/backups/jobs/${jobId}/dismiss`, { reason: 'Kullanıcı tarafından kapatıldı' }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['backup-jobs'] })
      toast.success('İşlem kapatıldı')
    },
    onError: () => toast.error('İşlem kapatılamadı'),
  })

  const purgeInvalidMutation = useMutation({
    mutationFn: () => api.post<{ removed: number; bytesFreed: number }>('/backups/purge-invalid', {}),
    onSuccess: (res) => {
      queryClient.invalidateQueries({ queryKey: ['backups'] })
      const freed = res?.bytesFreed ? formatBytes(res.bytesFreed) : ''
      toast.success(
        freed
          ? `${res?.removed ?? 0} geçersiz yedek silindi (${freed} alan açıldı)`
          : `${res?.removed ?? 0} geçersiz yedek silindi`,
      )
    },
    onError: () => toast.error('Toplu silme başarısız'),
  })

  const purgeOrphanedMutation = useMutation({
    mutationFn: () => api.post<{ removed: number; bytesFreed: number }>('/backups/purge-orphaned', {
      ids: orphanedBackups.map((backup) => backup.id),
      confirm: 'DELETE_ORPHANED_PARTIALS',
    }),
    onSuccess: (result) => {
      queryClient.invalidateQueries({ queryKey: ['backups'] })
      queryClient.invalidateQueries({ queryKey: ['disk'] })
      setOrphanCleanupOpen(false)
      toast.success(`${result.removed} yarım kalmış dosya silindi · ${formatBytes(result.bytesFreed)} alan açıldı`)
    },
    onError: (error) => toast.error(error.message || 'Yarım kalmış yedekler temizlenemedi'),
  })

  const deleteScheduleMutation = useMutation({
    mutationFn: (rawLine: string) =>
      api.delete('/backups/schedules', { rawLine } satisfies BackupScheduleDeleteRequest),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['backups-schedule'] })
      setDeleteScheduleOpen(false)
      toast.success('Schedule deleted')
    },
    onError: (error) => toast.error(error instanceof Error ? error.message : 'Failed to delete schedule'),
  })

  const handleDownload = (backup: Backup) => {
    window.open(`/api/backups/download/${backup.id}`, '_blank')
  }

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between gap-4 flex-wrap">
        <div>
          <h2 className="text-white text-xl font-bold">Yedekleme</h2>
          <p className="text-zinc-500 text-sm mt-0.5">
            Yerel yedekler, zamanlama ve Google Drive offsite koruma
          </p>
        </div>
        <Button
          onClick={() => setCreateOpen(true)}
          disabled={backupsQuery.isLoading || backupsQuery.isError}
          className="bg-blue-600 hover:bg-blue-500 text-white"
        >
          <Plus className="w-4 h-4 mr-2" />
          Yedek Oluştur
        </Button>
      </div>

      {backupsQuery.data && (
        <BackupOverviewStrip
          backupCount={completedBackups.length}
          schedule={schedule}
          scheduleState={scheduleState}
          driveState={driveState}
          driveEmail={gdriveStatus?.email}
          autoUpload={gdriveStatus?.settings?.autoUpload}
        />
      )}

      <BackupStrategyStrip />

      <BackupStorageCard
        storage={storage}
        orphaned={orphanedBackups}
        onCleanup={() => setOrphanCleanupOpen(true)}
      />

      <InvalidBackupsBanner
        backups={backups}
        isPurging={purgeInvalidMutation.isPending}
        onPurge={() => purgeInvalidMutation.mutate()}
      />

      <PendingGDriveUploadsBanner
        backups={backups}
        driveReady={!!gdriveReady}
        onWatchJob={watchJob}
      />

      {jobsError ? (
        <Card className="border-red-500/25 bg-red-500/[0.05]">
          <CardContent className="flex flex-col items-center p-5 text-center">
            <AlertTriangle className="size-4 text-red-400" />
            <p className="mt-2 text-sm text-red-300">Canlı yedekleme işleri yüklenemedi.</p>
            <p className="mt-1 text-xs text-zinc-600">{jobsQueryError?.message}</p>
            <Button type="button" variant="outline" size="sm" className="mt-4 border-red-500/30 text-red-200" onClick={() => { void refetchJobs() }} disabled={jobsFetching}>
              <RefreshCw className={`mr-2 size-3.5 ${jobsFetching ? 'animate-spin' : ''}`} />Tekrar dene
            </Button>
          </CardContent>
        </Card>
      ) : (
        <LiveJobsPanel
          jobs={activeJobs}
          onDismissJob={(job) => {
            const id = job.id ?? job.jobId
            if (id) dismissJobMutation.mutate(id)
          }}
        />
      )}

      {/* Google Drive offsite */}
      <SnapshotSection
        onWatchJob={watchJob}
        snapshotBusy={activeJobs.some((j) => j.type === 'snapshot' || j.type === 'snapshot_restore')}
      />

      <GDriveSection onWatchJob={watchJob} />

      {/* Schedule */}
      <ScheduleSection
        schedules={schedules}
        isLoading={scheduleQuery.isLoading}
        error={scheduleQuery.error}
        isFetching={scheduleQuery.isFetching}
        onRetry={() => { void scheduleQuery.refetch() }}
        onEdit={() => setScheduleDialogOpen(true)}
        onDelete={() => setDeleteScheduleOpen(true)}
      />

      <JobHistoryTable jobs={historyJobs} isLoading={jobsLoading} error={jobsQueryError} isFetching={jobsFetching} onRetry={() => { void refetchJobs() }} />

      {/* Backup Table */}
      <Card className="bg-zinc-900 border-zinc-800">
        <CardHeader className="pb-3 border-b border-zinc-800">
          <CardTitle className="text-white text-base flex items-center gap-2">
            <Archive className="w-4 h-4 text-zinc-400" />
            Yerel Yedekler
          </CardTitle>
        </CardHeader>
        <CardContent className="p-0">
          {backupsQuery.isLoading ? (
            <div className="p-4 space-y-3">
              {Array.from({ length: 4 }).map((_, i) => (
                <Skeleton key={i} className="h-14 w-full bg-zinc-800" />
              ))}
            </div>
          ) : backupsQuery.isError ? (
            <div className="flex flex-col items-center justify-center px-4 py-12 text-center">
              <AlertTriangle className="size-5 text-red-400" />
              <p className="mt-2 text-sm text-red-300">Yerel yedek envanteri yüklenemedi. Yedekleme ve temizleme kontrolleri duraklatıldı.</p>
              <p className="mt-1 text-xs text-zinc-600">{backupsQuery.error.message}</p>
              <Button type="button" variant="outline" size="sm" className="mt-4 border-red-500/30 text-red-200" onClick={() => { void backupsQuery.refetch() }} disabled={backupsQuery.isFetching}>
                <RefreshCw className={`mr-2 size-3.5 ${backupsQuery.isFetching ? 'animate-spin' : ''}`} />Tekrar dene
              </Button>
            </div>
          ) : backups.length === 0 ? (
            <EmptyState
              icon={Archive}
              title="Henüz yedek yok"
              description="İlk yedeğinizi oluşturarak sunucu verilerinizi koruyun."
              actionLabel="Yedek Oluştur"
              onAction={() => setCreateOpen(true)}
            />
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-sm min-w-[560px]">
                <thead>
                  <tr className="border-b border-zinc-800">
                    <th className="text-left text-zinc-500 font-medium px-5 py-3">Dosya</th>
                    <th className="text-left text-zinc-500 font-medium px-4 py-3">Tür</th>
                    <th className="text-left text-zinc-500 font-medium px-4 py-3">Boyut</th>
                    <th className="text-left text-zinc-500 font-medium px-4 py-3">Tarih</th>
                    <th className="text-left text-zinc-500 font-medium px-4 py-3">Durum</th>
                    <th className="text-right text-zinc-500 font-medium px-5 py-3">İşlemler</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-zinc-800">
                  {backups.map((backup) => (
                    <tr key={backup.id} className="hover:bg-zinc-800/40 transition-colors group">
                      <td className="px-5 py-3.5">
                        <div className="flex items-center gap-2.5">
                          <TypeIcon type={backup.type} />
                          <span className="text-white font-medium font-mono text-xs">
                            {backup.path ? backup.path.split('/').pop() : backup.id}
                          </span>
                        </div>
                      </td>
                      <td className="px-4 py-3.5">
                        <span className="text-zinc-400 capitalize">{backup.type}</span>
                      </td>
                      <td className="px-4 py-3.5">
                        <span className={backup.status === 'invalid' ? 'text-amber-400' : 'text-zinc-400'}>
                          {formatBytes(backup.size ?? 0)}
                        </span>
                        {backup.status === 'invalid' && (
                          <p className="text-amber-400/70 text-[10px] mt-0.5 max-w-[200px] leading-snug">
                            {invalidBackupHint(backup.type, backup.size ?? 0)}
                          </p>
                        )}
                      </td>
                      <td className="px-4 py-3.5">
                        <span className="text-zinc-400 text-xs">
                          {formatDate(backup.createdAt ?? backup.created_at ?? '')}
                        </span>
                      </td>
                      <td className="px-4 py-3.5">
                        <StatusBadge status={backup.status} />
                      </td>
                      <td className="px-5 py-3.5">
                        <div className="flex items-center justify-end gap-1.5">
                          <button
                            onClick={() => handleDownload(backup)}
                            disabled={!isBackupUsable(backup)}
                            title="İndir"
                            className="p-1.5 rounded-md text-zinc-500 hover:text-white hover:bg-zinc-700 transition-colors disabled:opacity-30 disabled:cursor-not-allowed"
                          >
                            <Download className="w-3.5 h-3.5" />
                          </button>
                          {gdriveReady && (
                            <button
                              onClick={() => uploadGDriveMutation.mutate(backup.id)}
                              disabled={!isBackupUsable(backup) || uploadGDriveMutation.isPending}
                              title="Google Drive'a yükle"
                              className="p-1.5 rounded-md text-zinc-500 hover:text-blue-400 hover:bg-blue-400/10 transition-colors disabled:opacity-30 disabled:cursor-not-allowed"
                            >
                              <CloudUpload className="w-3.5 h-3.5" />
                            </button>
                          )}
                          <button
                            onClick={() => setRestoreTarget(backup)}
                            disabled={!isBackupUsable(backup)}
                            title="Geri yükle"
                            className="p-1.5 rounded-md text-zinc-500 hover:text-amber-400 hover:bg-amber-400/10 transition-colors disabled:opacity-30 disabled:cursor-not-allowed"
                          >
                            <RotateCcw className="w-3.5 h-3.5" />
                          </button>
                          <button
                            onClick={() => setDeleteTarget(backup)}
                            disabled={backup.status === 'running'}
                            title="Sil"
                            className="p-1.5 rounded-md text-zinc-500 hover:text-red-400 hover:bg-red-400/10 transition-colors disabled:opacity-30 disabled:cursor-not-allowed"
                          >
                            <Trash2 className="w-3.5 h-3.5" />
                          </button>
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </CardContent>
      </Card>

      {/* Dialogs */}
      <CreateBackupDialog
        open={createOpen}
        onClose={() => setCreateOpen(false)}
        onSubmit={(req) => createMutation.mutate(req)}
        isPending={createMutation.isPending}
      />

      <ScheduleDialog
        key={scheduleDialogOpen ? `${schedule?.frequency}-${schedule?.time}` : 'closed'}
        open={scheduleDialogOpen}
        onClose={() => setScheduleDialogOpen(false)}
        current={schedule}
        onSubmit={(s) => saveScheduleMutation.mutate(s)}
        isPending={saveScheduleMutation.isPending}
      />

      <DeleteScheduleDialog
        open={deleteScheduleOpen}
        onClose={() => setDeleteScheduleOpen(false)}
        onConfirm={() => schedule && deleteScheduleMutation.mutate(schedule.rawLine)}
        isPending={deleteScheduleMutation.isPending}
      />

      <RestoreDialog
        key={restoreTarget?.id ?? 'closed'}
        backup={restoreTarget}
        onClose={() => setRestoreTarget(null)}
        onConfirm={() => restoreTarget && restoreMutation.mutate(restoreTarget.id)}
        isPending={restoreMutation.isPending}
      />

      <DeleteDialog
        backup={deleteTarget}
        onClose={() => setDeleteTarget(null)}
        onConfirm={() => deleteTarget && deleteMutation.mutate(deleteTarget.id)}
        isPending={deleteMutation.isPending}
      />

      <OrphanedCleanupDialog
        open={orphanCleanupOpen && !backupsQuery.isError}
        backups={orphanedBackups}
        onClose={() => setOrphanCleanupOpen(false)}
        onConfirm={() => purgeOrphanedMutation.mutate()}
        isPending={purgeOrphanedMutation.isPending}
      />
    </div>
  )
}
