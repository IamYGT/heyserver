import { Archive, Layers } from 'lucide-react'

export default function BackupStrategyStrip() {
  return (
    <div className="grid grid-cols-1 lg:grid-cols-2 gap-3">
      <div className="rounded-xl border border-blue-500/20 bg-blue-500/5 px-4 py-3">
        <div className="flex items-start gap-3">
          <div className="rounded-lg bg-blue-500/10 p-2 mt-0.5">
            <Archive className="w-4 h-4 text-blue-400" />
          </div>
          <div className="min-w-0">
            <p className="text-white text-sm font-medium">Yerel arşiv (tar.gz)</p>
            <p className="text-zinc-500 text-xs mt-1 leading-relaxed">
              Tek seferlik tam yedek: PostgreSQL/MariaDB dump + seçili dosyalar. Hızlı indirme ve
              manuel geri yükleme için. Google Drive&apos;a isteğe bağlı yüklenir.
            </p>
          </div>
        </div>
      </div>
      <div className="rounded-xl border border-violet-500/20 bg-violet-500/5 px-4 py-3">
        <div className="flex items-start gap-3">
          <div className="rounded-lg bg-violet-500/10 p-2 mt-0.5">
            <Layers className="w-4 h-4 text-violet-400" />
          </div>
          <div className="min-w-0">
            <p className="text-white text-sm font-medium">Sunucu snapshot (restic)</p>
            <p className="text-zinc-500 text-xs mt-1 leading-relaxed">
              Günlük artımlı, şifreli tam sunucu kopyası: vhosts, Nginx, SSL, DB dump, cron, systemd.
              Drive&apos;da dedupe; tek site veya tam sunucu geri yükleme.
            </p>
          </div>
        </div>
      </div>
    </div>
  )
}
