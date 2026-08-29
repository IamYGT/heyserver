import { Archive, CalendarClock, Cloud, HardDrive } from 'lucide-react'
import type { BackupSchedule } from '@/lib/types'

interface BackupOverviewStripProps {
  backupCount: number
  schedule: BackupSchedule | null
  scheduleState: 'loading' | 'unavailable' | 'ready'
  driveState: 'loading' | 'unavailable' | 'disconnected' | 'connected'
  driveEmail?: string
  autoUpload?: boolean
}

function frequencyTR(f: NonNullable<BackupSchedule['frequency']>): string {
  return { daily: 'Günlük', weekly: 'Haftalık', monthly: 'Aylık' }[f]
}

function scheduleLabel(schedule: BackupSchedule): string {
  if (schedule.frequency && schedule.time) return `${frequencyTR(schedule.frequency)} ${schedule.time}`
  return `Özel cron · ${schedule.cron}`
}

export default function BackupOverviewStrip({
  backupCount,
  schedule,
  scheduleState,
  driveState,
  driveEmail,
  autoUpload,
}: BackupOverviewStripProps) {
  const scheduleValue = scheduleState === 'loading'
    ? 'Yükleniyor…'
    : scheduleState === 'unavailable'
      ? 'Kullanılamıyor'
      : schedule
        ? scheduleLabel(schedule)
        : 'Kapalı'
  const scheduleTone = scheduleState === 'unavailable'
    ? 'text-amber-400'
    : scheduleState === 'ready' && schedule
      ? 'text-white'
      : 'text-zinc-500'
  const driveValue = driveState === 'loading'
    ? 'Yükleniyor…'
    : driveState === 'unavailable'
      ? 'Kullanılamıyor'
      : driveState === 'connected'
        ? (driveEmail ?? 'Bağlı')
        : 'Bağlı değil'
  const driveTone = driveState === 'unavailable'
    ? 'text-amber-400'
    : driveState === 'connected'
      ? 'text-emerald-400'
      : 'text-zinc-500'
  const autoUploadValue = driveState === 'loading'
    ? 'Yükleniyor…'
    : driveState === 'unavailable'
      ? 'Bilinmiyor'
      : driveState === 'connected'
        ? (autoUpload ? 'Açık' : 'Kapalı')
        : '—'

  const items = [
    {
      icon: Archive,
      label: 'Yerel yedek',
      value: backupCount > 0 ? `${backupCount} dosya` : 'Henüz yok',
      tone: backupCount > 0 ? 'text-white' : 'text-zinc-500',
      accent: 'text-blue-400',
    },
    {
      icon: CalendarClock,
      label: 'Zamanlama',
      value: scheduleValue,
      tone: scheduleTone,
      accent: 'text-amber-400',
    },
    {
      icon: Cloud,
      label: 'Uzak depo',
      value: driveValue,
      tone: driveTone,
      accent: 'text-emerald-400',
    },
    {
      icon: HardDrive,
      label: 'Otomatik yükleme',
      value: autoUploadValue,
      tone: driveState === 'connected' && autoUpload ? 'text-emerald-400' : driveState === 'unavailable' ? 'text-amber-400' : 'text-zinc-500',
      accent: 'text-violet-400',
    },
  ]

  return (
    <div className="grid grid-cols-2 lg:grid-cols-4 gap-3">
      {items.map((item) => (
        <div
          key={item.label}
          className="rounded-xl border border-zinc-800 bg-zinc-900/80 px-4 py-3 flex items-start gap-3"
        >
          <div className="mt-0.5 rounded-lg bg-zinc-800 p-2">
            <item.icon className={`w-4 h-4 ${item.accent}`} />
          </div>
          <div className="min-w-0">
            <p className="text-zinc-500 text-[11px] uppercase tracking-wide font-medium">{item.label}</p>
            <p className={`text-sm font-medium truncate ${item.tone}`}>{item.value}</p>
          </div>
        </div>
      ))}
    </div>
  )
}
