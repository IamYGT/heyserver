import type { BackupJob } from '@/lib/types'

const PHASE_TR: Record<string, string> = {
  preparing: 'Hazırlanıyor',
  database: 'Veritabanı',
  files: 'Dosyalar',
  archive: 'Arşiv',
  retention: 'Temizlik',
  gdrive_upload: 'Drive yükleme',
  gdrive_restore: 'Drive indirme',
  restore: 'Geri yükleme',
  verify: 'Doğrulama',
  done: 'Tamamlandı',
}

const TYPE_TR: Record<string, string> = {
  full: 'Tam yedek',
  database: 'Veritabanı',
  files: 'Dosyalar',
  restore: 'Geri yükleme',
  gdrive_upload: 'Drive yükleme',
  gdrive_restore: 'Drive indirme',
  snapshot: 'Sunucu snapshot',
  snapshot_restore: 'Snapshot geri yükleme',
  retention: 'Retention',
}

export function phaseLabel(phase?: string): string {
  if (!phase) return 'İşlem'
  return PHASE_TR[phase] ?? phase
}

export function historyPhaseLabel(job: Pick<BackupJob, 'status' | 'phase'>): string {
  if (job.status === 'failed' && (!job.phase || job.phase === 'done')) return 'Hata ile sonlandı'
  return phaseLabel(job.phase)
}

export function jobTypeLabel(job: BackupJob): string {
  const type = job.type ?? 'unknown'
  return TYPE_TR[type] ?? type
}

export function formatETA(seconds?: number): string {
  if (!seconds || seconds <= 0) return ''
  if (seconds < 60) return `~${seconds} sn`
  const m = Math.floor(seconds / 60)
  const s = seconds % 60
  return s > 0 ? `~${m} dk ${s} sn` : `~${m} dk`
}

export function formatDuration(start?: string, end?: string): string {
  if (!start) return '—'
  const a = new Date(start).getTime()
  const b = end ? new Date(end).getTime() : Date.now()
  const sec = Math.max(0, Math.floor((b - a) / 1000))
  if (sec < 60) return `${sec} sn`
  const m = Math.floor(sec / 60)
  return `${m} dk ${sec % 60} sn`
}
