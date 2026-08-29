import { AlertTriangle, Trash2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import type { Backup } from '@/lib/types'

function formatBytes(bytes: number): string {
  if (bytes === 0) return '0 B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return `${parseFloat((bytes / Math.pow(k, i)).toFixed(1))} ${sizes[i]}`
}

interface InvalidBackupsBannerProps {
  backups: Backup[]
  onPurge: () => void
  isPurging?: boolean
}

export default function InvalidBackupsBanner({ backups, onPurge, isPurging }: InvalidBackupsBannerProps) {
  const invalid = backups.filter((b) => b.status === 'invalid')
  if (invalid.length === 0) return null

  const reclaimable = invalid.reduce((sum, b) => sum + (b.size ?? 0), 0)

  return (
    <div className="rounded-xl border border-amber-500/30 bg-amber-500/5 px-4 py-3 flex flex-wrap items-center justify-between gap-3">
      <div className="flex items-start gap-2 min-w-0">
        <AlertTriangle className="w-4 h-4 text-amber-400 shrink-0 mt-0.5" />
        <div className="text-xs">
          <p className="text-amber-200 font-medium">
            {invalid.length} geçersiz yerel yedek ({formatBytes(reclaimable)} — çoğu stub)
          </p>
          <p className="text-amber-200/70 mt-0.5">
            Disk %90+ doluysa snapshot başarısız olabilir. Geçersiz dosyaları silerek alan açın.
          </p>
        </div>
      </div>
      <Button
        size="sm"
        variant="outline"
        className="border-amber-500/40 text-amber-200 hover:bg-amber-500/10"
        disabled={isPurging}
        onClick={onPurge}
      >
        <Trash2 className="w-3.5 h-3.5 mr-1.5" />
        Geçersizleri toplu sil
      </Button>
    </div>
  )
}
