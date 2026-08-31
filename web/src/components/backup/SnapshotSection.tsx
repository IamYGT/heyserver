import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  Layers,
  Play,
  Loader2,
  HardDrive,
  Database,
  Server,
  RotateCcw,
  CalendarClock,
  CheckCircle2,
  AlertTriangle,
  Shield,
  Save,
  Trash2,
} from 'lucide-react'
import { Card, CardContent } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { api } from '@/lib/api'
import { toast } from 'sonner'
import { DependencyRemediation } from '@/components/DependencyRemediation'
import type {
  SnapshotManifestEntry,
  SnapshotManifestId,
  SnapshotDestination,
  SnapshotPurgeRepositoryRequest,
  SnapshotRestoreRequest,
  SnapshotSettingsUpdateRequest,
  SnapshotStatus,
  ResticSnapshot,
  SystemStats,
} from '@/lib/types'

interface SnapshotSectionProps {
  onWatchJob?: (jobId: string) => void
  snapshotBusy?: boolean
}

function formatBytes(bytes: number): string {
  if (!bytes || bytes <= 0) return '0 B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return `${parseFloat((bytes / Math.pow(k, i)).toFixed(1))} ${sizes[i]}`
}

export default function SnapshotSection({ onWatchJob, snapshotBusy }: SnapshotSectionProps) {
  const queryClient = useQueryClient()
  const [restoreSnapshot, setRestoreSnapshot] = useState('')
  const [selectedVhost, setSelectedVhost] = useState('')
  const [keepDailyChoice, setKeepDailyChoice] = useState<number | null>(null)
  const [enabledPathsChoice, setEnabledPathsChoice] = useState<SnapshotManifestId[] | null>(null)
  const [restoreConfirmOpen, setRestoreConfirmOpen] = useState(false)
  const [purgeConfirmOpen, setPurgeConfirmOpen] = useState(false)
  const [purgeRepositoryName, setPurgeRepositoryName] = useState('')

  const {
    data: status,
    isPending,
    isError,
    error,
    isFetching,
    refetch,
  } = useQuery<SnapshotStatus>({
    queryKey: ['snapshot-status'],
    queryFn: () => api.get<SnapshotStatus>('/backups/snapshot/status'),
    staleTime: 120_000,
    retry: 1,
  })

  const { data: sysStats } = useQuery<SystemStats>({
    queryKey: ['system-stats'],
    queryFn: () => api.get<SystemStats>('/system/stats'),
    staleTime: 30_000,
  })
  const diskPct = sysStats?.disk?.find((d) => d.mount === '/')?.percentage

  const enabledPaths = enabledPathsChoice
    ?? status?.manifest?.filter((entry) => entry.enabled).map((entry) => entry.id)
    ?? []
  const keepDaily = keepDailyChoice ?? status?.settings?.keepDaily ?? 14

  const { data: vhostsData } = useQuery<{ vhosts: string[] }>({
    queryKey: ['snapshot-vhosts'],
    queryFn: () => api.get<{ vhosts: string[] }>('/backups/snapshot/vhosts'),
  })

  const runMutation = useMutation({
    mutationFn: () => api.post<{ jobId?: string }>('/backups/snapshot/run'),
    onSuccess: (res) => {
      if (res?.jobId) onWatchJob?.(res.jobId)
      toast.info('Artımlı snapshot başladı — canlı panelden izleyin')
      queryClient.invalidateQueries({ queryKey: ['snapshot-status'] })
    },
    onError: (e: Error) => toast.error(e.message || 'Snapshot başlatılamadı'),
  })

  const saveSettingsMutation = useMutation({
    mutationFn: (body: SnapshotSettingsUpdateRequest) =>
      api.put('/backups/snapshot/settings', body),
    onSuccess: () => {
      toast.success('Snapshot ayarları kaydedildi')
      refetch()
    },
    onError: () => toast.error('Ayarlar kaydedilemedi'),
  })

  const restoreMutation = useMutation({
    mutationFn: (body: SnapshotRestoreRequest) =>
      api.post<{ jobId?: string }>('/backups/snapshot/restore', body),
    onSuccess: (res) => {
      if (res?.jobId) onWatchJob?.(res.jobId)
      toast.info('Geri yükleme başladı — canlı panelden izleyin')
    },
    onError: () => toast.error('Geri yükleme başlatılamadı'),
  })

  const purgeMutation = useMutation({
    mutationFn: (body: SnapshotPurgeRepositoryRequest) =>
      api.post('/backups/snapshot/purge-repo', body),
    onSuccess: () => {
      setPurgeConfirmOpen(false)
      setPurgeRepositoryName('')
      toast.success('Uzak snapshot deposu sıfırlandı')
      void queryClient.invalidateQueries({ queryKey: ['snapshot-status'] })
    },
    onError: (error: Error) => toast.error(error.message || 'Snapshot deposu sıfırlanamadı'),
  })

  const scheduleSnapshotMutation = useMutation({
    mutationFn: () =>
      api.post('/backups/schedules', {
        frequency: 'daily',
        time: '04:00',
        retention_count: status?.settings?.keepDaily ?? 14,
        type: 'snapshot',
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['backups-schedule'] })
      toast.success('Günlük artımlı snapshot zamanlandı (04:00)')
    },
    onError: () => toast.error('Zamanlama kaydedilemedi'),
  })

  const togglePath = (entry: SnapshotManifestEntry) => {
    if (entry.required) return
    setEnabledPathsChoice((choice) => {
      const current = choice ?? enabledPaths
      return current.includes(entry.id)
        ? current.filter((id) => id !== entry.id)
        : [...current, entry.id]
    },
    )
  }

  if (isPending && !status) {
    return (
      <div className="rounded-xl border border-zinc-800 bg-zinc-900/40 p-6 space-y-3">
        <Skeleton className="h-48 w-full bg-zinc-800 rounded-xl" />
        <p className="text-zinc-500 text-xs text-center">
          Şifreli restic deposu kontrol ediliyor… İlk yükleme birkaç saniye sürebilir.
        </p>
      </div>
    )
  }

  if (isError && !status) {
    return (
      <DependencyRemediation
        title="Snapshot bağımlılık durumu alınamadı"
        summary="Heyserver snapshot API'si restic, şifreleme ve uzak depo hazırlığını doğrulayamadı. Snapshot işlemleri durum bilinene kadar duraklatıldı."
        state="unavailable"
        steps={[
          <>Heyserver API sağlığını ve servis günlüklerini kontrol edin.</>,
          <><code>restic version</code> komutunu ve seçili uzak hedef yapılandırmasını Heyserver servis ortamında doğrulayın.</>,
          <>Bağımlılık veya API sorununu giderdikten sonra algılamayı yeniden deneyin.</>,
        ]}
        error={error instanceof Error ? error.message : undefined}
        retry={() => { void refetch() }}
        retrying={isFetching}
      />
    )
  }

  const st = status
  const destination = st?.destination ?? st?.settings?.destination ?? 'gdrive'
  const destinationStatus = st?.destinationStatus ?? (st?.driveConnected ? 'healthy' : 'unavailable')
  const providerLabel = destination === 's3' ? 'S3-compatible storage' : 'Google Drive'
  const ready = st?.resticFound && st?.passwordSet && destinationStatus === 'healthy'
  const snaps = st?.lastSnapshots ?? []
  const stats = st?.repoStats
  const observedRepoFolder = st?.settings?.repoFolder ?? ''
  const observedVhostsRoot = st?.manifest?.find((entry) => entry.id === 'vhosts')?.path

  const settingsUpdate = (
    overrides: Partial<SnapshotSettingsUpdateRequest> = {},
  ): SnapshotSettingsUpdateRequest => ({
    destination,
    repoFolder: st?.settings?.repoFolder ?? '',
    enabledPaths,
    keepDaily,
    keepWeekly: st?.settings?.keepWeekly ?? 0,
    keepMonthly: st?.settings?.keepMonthly ?? 0,
    passwordAcknowledged: st?.settings?.passwordAcknowledged ?? false,
    ...overrides,
  })

  const handleRestore = () => {
    const id = restoreSnapshot || snaps[0]?.id
    if (!id) {
      toast.error('Snapshot seçin')
      return
    }
    setRestoreConfirmOpen(true)
  }

  const confirmRestore = () => {
    const id = restoreSnapshot || snaps[0]?.id
    if (!id) return
    const request: SnapshotRestoreRequest = { snapshotId: id }
    if (selectedVhost) request.vhosts = [selectedVhost]
    restoreMutation.mutate(request)
    setRestoreConfirmOpen(false)
  }

  return (
    <Card className="bg-zinc-900 border-zinc-800 overflow-hidden">
      <div className="border-b border-zinc-800 bg-gradient-to-br from-violet-950/40 via-zinc-900 to-zinc-900 px-5 py-4">
        <div className="flex items-start justify-between gap-4 flex-wrap">
          <div className="flex items-start gap-3">
            <div className="rounded-xl bg-violet-500/10 p-2.5 ring-1 ring-violet-500/20">
              <Layers className="w-5 h-5 text-violet-400" />
            </div>
            <div>
              <h3 className="text-white font-semibold text-base">Sunucu Snapshot — Artımlı (restic)</h3>
              <p className="text-zinc-500 text-xs mt-0.5 max-w-xl">
                Plesk++: PostgreSQL, MariaDB, Nginx, SSL, PHP, panel verisi ve vhosts — günlük artımlı,
                seçili uzak hedefte istemci tarafında şifreli. Her snapshot tam geri yüklenebilir.
              </p>
            </div>
          </div>
          {ready && (stats?.snapshotCount ?? 0) > 0 ? (
            <Badge className="bg-emerald-500/10 text-emerald-400 border-emerald-500/20">
              <CheckCircle2 className="w-3 h-3 mr-1" />
              Hazır · {stats?.snapshotCount} snapshot
            </Badge>
          ) : ready ? (
            <Badge className="bg-amber-500/10 text-amber-400 border-amber-500/20">
              <AlertTriangle className="w-3 h-3 mr-1" />
              Repo hazır — henüz snapshot yok
            </Badge>
          ) : (
            <Badge className="bg-amber-500/10 text-amber-400 border-amber-500/20">
              <AlertTriangle className="w-3 h-3 mr-1" />
              Kurulum gerekli
            </Badge>
          )}
        </div>
      </div>

      <CardContent className="p-5 space-y-5">
        {isFetching && status && (
          <div className="flex items-center gap-2 rounded-lg border border-blue-500/25 bg-blue-500/5 px-3 py-2 text-blue-300/90 text-[11px]">
            <Loader2 className="w-3.5 h-3.5 animate-spin shrink-0" />
            {providerLabel} repo istatistikleri güncelleniyor…
          </div>
        )}
        {diskPct != null && diskPct >= 90 && (
          <div className="rounded-lg border border-amber-500/30 bg-amber-500/10 px-4 py-3 text-amber-200/90 text-xs space-y-1">
            <p className="font-medium text-amber-200">Disk %{diskPct} dolu</p>
            <p className="text-amber-200/70">
              İlk snapshot uzun sürebilir. Yerel arşivleri silmeden önce seçili uzak hedefteki
              snapshot işleminin tamamlandığını doğrulayın.
            </p>
          </div>
        )}
        {!st?.resticFound && (
          <DependencyRemediation
            title="restic kurulumu gerekli"
            summary="Artımlı ve şifreli sunucu snapshot'ları restic olmadan çalışamaz. Heyserver paketleri otomatik kurmaz."
            state="not-configured"
            steps={[
              <>İşletim sisteminizin desteklenen paket kaynağından <code>restic</code> kurun.</>,
              <>Standart dışı kurulumlarda <code>HSERVER_RESTIC_BIN</code> değerini <code>/etc/hserver/hserver.env</code> içinde mutlak binary yoluna ayarlayın.</>,
              <><code>restic version</code> komutunu Heyserver servis ortamında doğrulayın, servisi yeniden başlatın ve algılamayı yineleyin.</>,
            ]}
            retry={() => { void refetch() }}
            retrying={isFetching}
          />
        )}
        {st?.resticFound && !st.passwordSet && (
          <DependencyRemediation
            title="Snapshot şifreleme parolası gerekli"
            summary="Her kurulum kendi restic parolasını üretmeli ve kurulumdan ayrı, kalıcı bir şifre kasasında saklamalıdır. Heyserver bu parolayı gösteremez veya kurtaramaz."
            state="not-configured"
            steps={[
              <>Sunucuda <code>openssl rand -base64 32</code> ile güçlü ve benzersiz bir parola üretin.</>,
              <>Parolayı şifre kasasına kaydedin ve <code>HSERVER_RESTIC_PASSWORD</code> olarak <code>/etc/hserver/hserver.env</code> içine ekleyin.</>,
              <>Heyserver servisini yeniden başlatın ve algılamayı yeniden deneyin.</>,
            ]}
            retry={() => { void refetch() }}
            retrying={isFetching}
          />
        )}
        {st?.passwordSet && !st?.settings?.passwordAcknowledged && (
          <div className="rounded-lg border border-red-500/25 bg-red-500/5 px-4 py-3 text-xs flex gap-2 items-start">
            <Shield className="w-4 h-4 text-red-400 shrink-0 mt-0.5" />
            <div className="text-red-200/90 space-y-2 flex-1">
              <p className="font-medium text-red-300">Şifre kaybı = veri kaybı</p>
              <p className="text-red-200/70">
                <code className="text-red-100">HSERVER_RESTIC_PASSWORD</code> şifre kasasına (1Password /
                Bitwarden) kaydedin. Bu parola olmadan uzak depodaki snapshot&apos;lar açılamaz.
              </p>
              <Button
                type="button"
                size="sm"
                variant="outline"
                className="h-7 text-[11px] border-red-500/30 text-red-200 hover:bg-red-500/10"
                disabled={saveSettingsMutation.isPending}
                onClick={() => saveSettingsMutation.mutate(settingsUpdate({ passwordAcknowledged: true }))}
              >
                Şifre kasasına kaydettim
              </Button>
            </div>
          </div>
        )}
        <div className="rounded-xl border border-zinc-800 bg-zinc-950/40 p-4 space-y-2">
          <label htmlFor="snapshot-destination" className="text-zinc-300 text-sm font-medium">
            Snapshot hedefi
          </label>
          <div className="flex flex-col sm:flex-row gap-2 sm:items-center">
            <select
              id="snapshot-destination"
              value={destination}
              disabled={saveSettingsMutation.isPending || snapshotBusy}
              onChange={(event) => saveSettingsMutation.mutate(settingsUpdate({
                destination: event.target.value as SnapshotDestination,
              }))}
              className="w-full sm:max-w-xs bg-zinc-900 border border-zinc-700 rounded-lg px-3 py-2 text-sm text-white"
            >
              <option value="gdrive">Google Drive</option>
              <option value="s3">S3-compatible / MinIO</option>
            </select>
            <Badge className={destinationStatus === 'healthy'
              ? 'bg-emerald-500/10 text-emerald-400 border-emerald-500/20'
              : destinationStatus === 'not_configured'
                ? 'bg-zinc-500/10 text-zinc-300 border-zinc-500/20'
                : 'bg-red-500/10 text-red-300 border-red-500/20'}>
              {destinationStatus === 'healthy' ? 'Hazır' : destinationStatus === 'not_configured' ? 'Yapılandırılmadı' : 'Kullanılamıyor'}
            </Badge>
          </div>
          {st?.destinationMessage && <p className="text-zinc-500 text-xs">{st.destinationMessage}</p>}
        </div>

        {destination === 's3' && destinationStatus !== 'healthy' && (
          <DependencyRemediation
            title={destinationStatus === 'not_configured' ? 'S3-compatible hedef yapılandırılmadı' : 'S3-compatible hedef kullanılamıyor'}
            summary="Heyserver, S3 kimlik bilgilerini panelde veya veritabanında tutmaz; yalnız kurulum sahibinin koruduğu dosyalardan okur."
            state={destinationStatus === 'not_configured' ? 'not-configured' : 'unavailable'}
            steps={[
              <><code>HSERVER_S3_ENDPOINT</code>, <code>HSERVER_S3_BUCKET</code> ve gerekirse <code>HSERVER_S3_REGION</code> değerlerini ayarlayın.</>,
              <><code>HSERVER_S3_ACCESS_KEY_FILE</code> ve <code>HSERVER_S3_SECRET_KEY_FILE</code> için mutlak dosya yolları tanımlayın.</>,
              <>Credential dosyalarını servis kullanıcısına ait, düzenli dosyalar olarak oluşturun ve izinlerini <code>0600</code> yapın.</>,
            ]}
            error={destinationStatus === 'unavailable' ? st?.destinationMessage : undefined}
            retry={() => { void refetch() }}
            retrying={isFetching}
          />
        )}

        {ready && (stats?.snapshotCount ?? 0) === 0 && (
          <div className="rounded-lg border border-violet-500/30 bg-violet-500/10 px-4 py-3 text-violet-200/90 text-xs flex flex-wrap items-center justify-between gap-3">
            <div>
              <p className="font-medium text-violet-200">Henüz sunucu snapshot&apos;ı yok</p>
              <p className="text-violet-200/70 mt-0.5">
                İlk çalıştırma uzun sürebilir; tamamlandığında {providerLabel} üzerinde şifreli restic repo oluşur.
              </p>
            </div>
            <Button
              size="sm"
              className="bg-violet-600 hover:bg-violet-500 text-white shrink-0"
              disabled={runMutation.isPending || snapshotBusy}
              onClick={() => runMutation.mutate()}
            >
              {runMutation.isPending ? (
                <Loader2 className="w-3.5 h-3.5 mr-1.5 animate-spin" />
              ) : (
                <Play className="w-3.5 h-3.5 mr-1.5" />
              )}
              İlk snapshot&apos;ı al
            </Button>
          </div>
        )}
        {destination === 'gdrive' && destinationStatus !== 'healthy' && (
          <div className="rounded-lg border border-red-500/30 bg-red-500/10 px-4 py-3 text-red-300 text-xs space-y-2">
            <p className="font-medium text-red-200">
              Google Drive bağlantısı gerekli — snapshot seçili Drive restic reposuna yazılır.
            </p>
            <p>
              Oturum süresi dolmuş olabilir. Aşağıdaki{' '}
              <a href="#gdrive-section" className="text-red-100 underline underline-offset-2">
                Uzak Yedekleme
              </a>{' '}
              bölümünden OAuth ile yeniden bağlanın; ardından gerekirse repo temizleyip snapshot alın.
            </p>
          </div>
        )}

        {stats && (
          <div className="grid grid-cols-1 sm:grid-cols-3 gap-3 text-xs">
            <div className="rounded-lg border border-zinc-800 bg-zinc-950/50 p-3">
              <p className="text-zinc-500 text-[10px] uppercase">Snapshot sayısı</p>
              <p className="text-white font-medium mt-1">{stats.snapshotCount}</p>
            </div>
            <div className="rounded-lg border border-zinc-800 bg-zinc-950/50 p-3">
              <p className="text-zinc-500 text-[10px] uppercase">Repo boyutu (dedupe)</p>
              <p className="text-white font-medium mt-1">{formatBytes(stats.totalSize)}</p>
            </div>
            <div className="rounded-lg border border-zinc-800 bg-zinc-950/50 p-3">
              <p className="text-zinc-500 text-[10px] uppercase">Mantıksal dosya boyutu</p>
              <p className="text-white font-medium mt-1">{formatBytes(stats.totalFileSize)}</p>
            </div>
          </div>
        )}

        <div className="grid grid-cols-1 md:grid-cols-3 gap-3 text-xs">
          <div className="rounded-lg border border-zinc-800 bg-zinc-950/50 p-3 flex items-center gap-2">
            <Database className="w-4 h-4 text-blue-400 shrink-0" />
            <span className="text-zinc-400">DB dump + config export</span>
          </div>
          <div className="rounded-lg border border-zinc-800 bg-zinc-950/50 p-3 flex items-center gap-2">
            <Server className="w-4 h-4 text-cyan-400 shrink-0" />
            <span className="text-zinc-400">Nginx, SSL, PHP, systemd, cron</span>
          </div>
          <div className="rounded-lg border border-zinc-800 bg-zinc-950/50 p-3 flex items-center gap-2">
            <HardDrive className="w-4 h-4 text-violet-400 shrink-0" />
            <span className="text-zinc-400">
              {observedVhostsRoot || 'Yapılandırılmış vhost kökü'} (tüm siteler)
            </span>
          </div>
        </div>

        {st?.manifest && st.manifest.length > 0 && (
          <div className="rounded-xl border border-zinc-800 p-3 space-y-3">
            <div className="flex items-center justify-between gap-2 flex-wrap">
              <p className="text-zinc-500 text-[10px] uppercase tracking-wide">Yedeklenecek yollar</p>
              <Button
                size="sm"
                variant="ghost"
                className="text-zinc-400 h-7 text-xs"
                disabled={saveSettingsMutation.isPending}
                onClick={() =>
                  saveSettingsMutation.mutate(settingsUpdate({
                    enabledPaths,
                    keepDaily,
                    keepWeekly: st?.settings?.keepWeekly ?? 8,
                    keepMonthly: st?.settings?.keepMonthly ?? 6,
                  }))
                }
              >
                {saveSettingsMutation.isPending ? (
                  <Loader2 className="w-3 h-3 mr-1 animate-spin" />
                ) : (
                  <Save className="w-3 h-3 mr-1" />
                )}
                Yolları kaydet
              </Button>
            </div>
            <div className="flex flex-wrap gap-2">
              {st.manifest.map((m: SnapshotManifestEntry) => {
                const on = enabledPaths.includes(m.id)
                const missing = m.available === false
                return (
                  <button
                    key={m.id}
                    type="button"
                    disabled={m.required}
                    onClick={() => togglePath(m)}
                    className={`rounded-lg border px-2.5 py-1.5 text-[10px] font-medium transition-colors ${
                      on
                        ? 'border-violet-500/40 bg-violet-500/10 text-violet-200'
                        : 'border-zinc-700 bg-zinc-950/50 text-zinc-500'
                    } ${m.required ? 'cursor-default opacity-90' : 'hover:border-zinc-600'}`}
                  >
                    {m.label}
                    {m.required && ' *'}
                    {missing && ' (yok)'}
                  </button>
                )
              })}
            </div>
            <p className="text-zinc-600 text-[10px]">* Zorunlu — vhosts her zaman dahil</p>
          </div>
        )}

        <div className="flex flex-wrap gap-2">
          <Button
            onClick={() => runMutation.mutate()}
            disabled={!ready || runMutation.isPending || snapshotBusy}
            className="bg-violet-600 hover:bg-violet-500 text-white"
          >
            {runMutation.isPending ? (
              <Loader2 className="w-4 h-4 mr-2 animate-spin" />
            ) : (
              <Play className="w-4 h-4 mr-2" />
            )}
            Snapshot al (artımlı)
          </Button>
          <Button
            variant="outline"
            className="border-zinc-700 text-zinc-300"
            onClick={() => scheduleSnapshotMutation.mutate()}
            disabled={!ready || scheduleSnapshotMutation.isPending || snapshotBusy}
          >
            <CalendarClock className="w-4 h-4 mr-2" />
            Günlük 04:00 zamanla
          </Button>
        </div>

        <div className="grid grid-cols-1 sm:grid-cols-3 gap-3 items-end">
          <div className="space-y-1">
            <label className="text-zinc-500 text-xs">Günlük sakla (gün)</label>
            <input
              type="number"
              min={3}
              max={90}
              value={keepDaily}
              onChange={(e) => setKeepDailyChoice(Number(e.target.value))}
              className="w-full bg-zinc-900 border border-zinc-700 rounded-lg px-3 py-2 text-sm text-white"
            />
          </div>
          <Button
            size="sm"
            variant="ghost"
            className="text-zinc-400"
            onClick={() =>
              saveSettingsMutation.mutate(settingsUpdate({
                enabledPaths,
                keepDaily,
                keepWeekly: st?.settings?.keepWeekly ?? 8,
                keepMonthly: st?.settings?.keepMonthly ?? 6,
              }))
            }
          >
            Retention kaydet
          </Button>
        </div>

        {snaps.length > 0 && (
          <div className="rounded-xl border border-zinc-800 overflow-hidden">
            <div className="px-4 py-2 border-b border-zinc-800 bg-zinc-950/50 text-zinc-400 text-xs font-medium">
              Son snapshot&apos;lar ({providerLabel})
            </div>
            <div className="divide-y divide-zinc-800 max-h-40 overflow-y-auto">
              {snaps.map((s: ResticSnapshot) => (
                <button
                  key={s.id}
                  type="button"
                  onClick={() => setRestoreSnapshot(s.id)}
                  className={`w-full text-left px-4 py-2 text-xs hover:bg-zinc-800/40 ${
                    restoreSnapshot === s.id ? 'bg-violet-500/10' : ''
                  }`}
                >
                  <span className="text-white font-mono">{s.id.slice(0, 8)}…</span>
                  <span className="text-zinc-500 ml-2">
                    {new Date(s.time).toLocaleString('tr-TR')}
                  </span>
                </button>
              ))}
            </div>
          </div>
        )}

        <div className="rounded-xl border border-zinc-800 p-4 space-y-3">
          <p className="text-white text-sm font-medium flex items-center gap-2">
            <RotateCcw className="w-4 h-4 text-amber-400" />
            Geri yükleme
          </p>
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
            <div className="space-y-1">
              <label htmlFor="snapshot-restore-vhost" className="text-zinc-500 text-xs">Tek site (vhost)</label>
              <select
                id="snapshot-restore-vhost"
                value={selectedVhost}
                onChange={(e) => setSelectedVhost(e.target.value)}
                className="w-full bg-zinc-900 border border-zinc-700 rounded-lg px-3 py-2 text-sm text-white"
              >
                <option value="">Tüm snapshot (seçili yollar)</option>
                {(vhostsData?.vhosts ?? []).map((v) => (
                  <option key={v} value={v}>
                    {v}
                  </option>
                ))}
              </select>
            </div>
            <div className="flex items-end">
              <Button
                onClick={handleRestore}
                disabled={restoreMutation.isPending || snaps.length === 0}
                className="w-full bg-amber-600/90 hover:bg-amber-500 text-white"
              >
                {restoreMutation.isPending ? (
                  <Loader2 className="w-4 h-4 animate-spin" />
                ) : (
                  'Geri yükle'
                )}
              </Button>
            </div>
          </div>
          <div className="rounded-lg border border-amber-500/20 bg-amber-500/5 px-3 py-2.5 text-[11px] text-amber-200/80 space-y-1.5">
            <p className="font-medium text-amber-300">Üretim ortamında geri yükleme adımları</p>
            <ol className="list-decimal list-inside space-y-0.5 text-amber-200/70">
              <li>Snapshot dosyaları <code className="text-amber-100">restore-{'{id}'}</code> klasörüne çıkarılır</li>
              <li>
                Tek site: içeriği{' '}
                <code className="text-amber-100">{observedVhostsRoot || 'yapılandırılmış vhost kökü'}/domain</code>{' '}
                üzerine kopyalayın
              </li>
              <li>Tam sunucu: Nginx/SSL/PHP dosyalarını ilgili <code className="text-amber-100">/etc</code> yollarına taşıyın</li>
              <li>DB: staging içindeki <code className="text-amber-100">*.sql</code> dump&apos;larını manuel import edin</li>
              <li><code className="text-amber-100">nginx -t</code> → <code className="text-amber-100">systemctl reload nginx</code> → PHP-FPM restart</li>
            </ol>
          </div>
        </div>

        {st?.repoInitialized && st.canPurgeRepository && (
          <div className="rounded-xl border border-red-500/20 bg-red-500/5 p-4 space-y-3">
            <div className="flex flex-wrap items-start justify-between gap-3">
              <div className="space-y-1">
                <p className="text-red-200 text-sm font-medium flex items-center gap-2">
                  <Trash2 className="w-4 h-4" />
                  Tehlikeli bölge
                </p>
                <p className="text-red-200/60 text-xs max-w-2xl">
                  {providerLabel} üzerindeki şifreli restic deposunu kalıcı olarak siler. Yerel backup dosyaları etkilenmez.
                </p>
              </div>
              <Button
                type="button"
                size="sm"
                variant="outline"
                className="border-red-500/30 text-red-300 hover:bg-red-500/10 hover:text-red-200"
                disabled={purgeMutation.isPending || snapshotBusy}
                onClick={() => {
                  setPurgeRepositoryName('')
                  setPurgeConfirmOpen(true)
                }}
              >
                Snapshot deposunu sıfırla
              </Button>
            </div>
          </div>
        )}

        {snapshotBusy && (
          <p className="text-amber-400/80 text-xs">Snapshot işlemi devam ediyor — yeni işlem başlatılamaz.</p>
        )}

        {st?.settings?.lastError && (
          <p className="text-red-400/80 text-xs font-mono">{st.settings.lastError}</p>
        )}

        {restoreConfirmOpen && (
          <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4">
            <div className="w-full max-w-md rounded-xl border border-zinc-700 bg-zinc-900 p-5 space-y-4">
              <p className="text-white font-medium">Geri yüklemeyi onaylayın</p>
              <p className="text-zinc-400 text-sm">
                Dosyalar staging klasörüne çıkarılır; üretim yollarına taşıma manuel yapılır.
                {selectedVhost ? ` Site: ${selectedVhost}` : ' Tam snapshot (tüm yollar).'}
              </p>
              <div className="flex gap-2 justify-end">
                <Button variant="ghost" onClick={() => setRestoreConfirmOpen(false)}>
                  İptal
                </Button>
                <Button className="bg-amber-600 hover:bg-amber-500" onClick={confirmRestore}>
                  Onayla ve başlat
                </Button>
              </div>
            </div>
          </div>
        )}

        {purgeConfirmOpen && (
          <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 p-4">
            <div className="w-full max-w-md rounded-xl border border-red-500/30 bg-zinc-950 p-5 space-y-4">
              <div className="space-y-1">
                <p className="text-red-200 font-medium">Uzak snapshot deposunu kalıcı olarak sil</p>
                <p className="text-zinc-400 text-sm">
                  Bu işlem geri alınamaz. Devam etmek için gözlemlenen depo yolunu aynen yazın.
                </p>
              </div>
              <code className="block rounded-lg border border-zinc-800 bg-zinc-900 px-3 py-2 text-xs text-red-200 break-all">
                {observedRepoFolder}
              </code>
              <div className="space-y-1.5">
                <label htmlFor="snapshot-purge-repository" className="text-zinc-400 text-xs">
                  Onay için depo yolunu yazın
                </label>
                <input
                  id="snapshot-purge-repository"
                  type="text"
                  value={purgeRepositoryName}
                  onChange={(event) => setPurgeRepositoryName(event.target.value)}
                  autoComplete="off"
                  className="w-full rounded-lg border border-zinc-700 bg-zinc-900 px-3 py-2 text-sm text-white focus:border-red-500 focus:outline-none"
                />
              </div>
              <div className="flex gap-2 justify-end">
                <Button variant="ghost" onClick={() => setPurgeConfirmOpen(false)}>
                  İptal
                </Button>
                <Button
                  className="bg-red-700 hover:bg-red-600 text-white"
                  disabled={purgeMutation.isPending || purgeRepositoryName !== observedRepoFolder}
                  onClick={() => purgeMutation.mutate({
                    repoFolder: observedRepoFolder,
                    confirmation: 'purge-snapshot-repository',
                  })}
                >
                  {purgeMutation.isPending && <Loader2 className="w-4 h-4 mr-2 animate-spin" />}
                  Depoyu kalıcı olarak sil
                </Button>
              </div>
            </div>
          </div>
        )}
      </CardContent>
    </Card>
  )
}
