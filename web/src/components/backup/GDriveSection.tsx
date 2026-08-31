import { useState, useEffect, useRef } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  Cloud,
  Unlink,
  Loader2,
  HardDrive,
  Download,
  RefreshCw,
  AlertTriangle,
  CheckCircle2,
  ChevronDown,
  Shield,
  FolderOpen,
  Upload,
} from 'lucide-react'
import { Card, CardContent } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { api } from '@/lib/api'
import { toast } from 'sonner'
import type {
	GDriveRemoteBackup,
	GDriveRestoreRequest,
	GDriveSettingsUpdateRequest,
	GDriveStatus,
} from '@/lib/types'
import GDriveWizard from '@/components/backup/GDriveWizard'
import QuotaBar from '@/components/backup/QuotaBar'
import { backupOperationHint } from '@/components/backup/backupErrorHints'
import { openGDriveOAuthPopup } from '@/lib/gdriveOAuth'
import { DependencyRemediation } from '@/components/DependencyRemediation'
import {
	INTEGRATION_UNAVAILABLE,
	integrationStatePresentation,
	normalizeIntegrationState,
	type IntegrationState,
} from '@/lib/integrationState'

function formatBytes(bytes: number): string {
  if (!bytes || bytes === 0) return '0 B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return `${parseFloat((bytes / Math.pow(k, i)).toFixed(1))} ${sizes[i]}`
}

function isValidDate(iso: string): boolean {
  const d = new Date(iso)
  return !Number.isNaN(d.getTime()) && d.getFullYear() > 2000
}

function formatDate(iso: string): string {
  if (!isValidDate(iso)) return '—'
  return new Date(iso).toLocaleString('tr-TR', {
    day: '2-digit',
    month: 'short',
    year: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  })
}

function gdriveAvailabilityState(status?: Pick<GDriveStatus, 'state'>): IntegrationState {
	return normalizeIntegrationState(status?.state) ?? INTEGRATION_UNAVAILABLE
}

function ToggleSwitch({
  checked,
  onChange,
  disabled,
  label,
  description,
}: {
  checked: boolean
  onChange: (v: boolean) => void
  disabled?: boolean
  label: string
  description?: string
}) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      disabled={disabled}
      onClick={() => onChange(!checked)}
      className={`w-full flex items-center justify-between gap-4 rounded-xl border px-4 py-3 text-left transition-colors ${
        checked
          ? 'border-emerald-500/30 bg-emerald-500/5'
          : 'border-zinc-800 bg-zinc-800/40 hover:border-zinc-700'
      } disabled:opacity-50`}
    >
      <div className="min-w-0">
        <p className="text-white text-sm font-medium">{label}</p>
        {description && <p className="text-zinc-500 text-xs mt-0.5">{description}</p>}
      </div>
      <div
        className={`relative shrink-0 w-11 h-6 rounded-full transition-colors ${
          checked ? 'bg-emerald-500' : 'bg-zinc-600'
        }`}
      >
        <span
          className={`absolute top-0.5 left-0.5 w-5 h-5 rounded-full bg-white shadow transition-transform ${
            checked ? 'translate-x-5' : 'translate-x-0'
          }`}
        />
      </div>
    </button>
  )
}

interface GDriveSectionProps {
  onWatchJob?: (jobId: string) => void
}

export default function GDriveSection({ onWatchJob }: GDriveSectionProps) {
  const queryClient = useQueryClient()
  const [remoteExpanded, setRemoteExpanded] = useState(true)
  const autoUploadDebounce = useRef<ReturnType<typeof setTimeout> | null>(null)
  const [settingsOpen, setSettingsOpen] = useState(false)
  const [folder, setFolder] = useState('')
  const [autoUpload, setAutoUpload] = useState<boolean | null>(null)
  const [retentionDays, setRetentionDays] = useState(30)
  const [settingsSynced, setSettingsSynced] = useState(false)

  const { data: status, isLoading, isError, error, isFetching, refetch } = useQuery<GDriveStatus>({
    queryKey: ['gdrive-status'],
    queryFn: () => api.get<GDriveStatus>('/backups/gdrive/status'),
    staleTime: 60_000,
    refetchOnWindowFocus: false,
  })
  const statusState = gdriveAvailabilityState(status)
  const statusPresentation = integrationStatePresentation(statusState)

  const { data: remoteData, isLoading: remoteLoading, isError: remoteError, refetch: refetchRemote } = useQuery<{
    backups: GDriveRemoteBackup[]
  }>({
    queryKey: ['gdrive-remote'],
    queryFn: () => api.get<{ backups: GDriveRemoteBackup[] }>('/backups/gdrive/remote'),
    enabled: remoteExpanded && statusState === 'healthy',
  })

  const connectMutation = useMutation({
    mutationFn: () => openGDriveOAuthPopup(),
    onSuccess: () => {
      toast.info('Google hesabınızla giriş yapın — panel otomatik tamamlayacak')
    },
    onError: (e: Error) => toast.error(e.message || 'OAuth başlatılamadı'),
  })

  const disconnectMutation = useMutation({
    mutationFn: () => api.post('/backups/gdrive/disconnect'),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['gdrive-status'] })
      setRemoteExpanded(false)
      toast.success('Google Drive bağlantısı kesildi')
    },
    onError: () => toast.error('Bağlantı kesilemedi'),
  })

  const testMutation = useMutation({
    mutationFn: () => api.post<{ message: string }>('/backups/gdrive/test'),
    onSuccess: (res) => toast.success(res.message ?? 'Bağlantı OK'),
    onError: (e: Error) => toast.error(e.message || 'Bağlantı testi başarısız'),
  })

  const dismissErrorMutation = useMutation({
    mutationFn: () => api.post('/backups/gdrive/dismiss-error'),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['gdrive-status'] })
      toast.success('Hata bildirimi temizlendi')
    },
    onError: () => toast.error('Hata temizlenemedi'),
  })

  const saveSettingsMutation = useMutation({
    mutationFn: (body: GDriveSettingsUpdateRequest) => api.put('/backups/gdrive/settings', body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['gdrive-status'] })
      toast.success('Ayarlar kaydedildi')
    },
    onError: () => toast.error('Ayarlar kaydedilemedi'),
  })

  const restoreMutation = useMutation({
    mutationFn: (fileName: string) =>
      api.post<{ localPath?: string; jobId?: string; status?: string }>(
        '/backups/gdrive/restore',
        { fileName } satisfies GDriveRestoreRequest,
      ),
    onSuccess: (res) => {
      queryClient.invalidateQueries({ queryKey: ['backups'] })
      if (res?.jobId) {
        onWatchJob?.(res.jobId)
        toast.info('Drive indirmesi başladı — canlı panelden izleyin')
      } else if (res?.localPath) {
        toast.success(`Sunucuya indirildi: ${res.localPath}`)
      }
    },
    onError: () => toast.error('Drive\'dan indirme başarısız'),
  })

  const toggleSettings = () => {
    if (!settingsOpen && !settingsSynced && status?.settings) {
      setFolder(status.settings.folder || 'hserver-backups')
      setAutoUpload(status.settings.autoUpload)
      setRetentionDays(status.settings.remoteRetentionDays ?? 30)
      setSettingsSynced(true)
    }
    setSettingsOpen(open => !open)
  }

  const effectiveAutoUpload = autoUpload ?? status?.settings.autoUpload ?? false

  const handleSaveSettings = () => {
    saveSettingsMutation.mutate({
      folder: folder || status?.settings.folder || 'hserver-backups',
      autoUpload: effectiveAutoUpload,
      remoteRetentionDays: retentionDays,
      notifyOnSuccess: status?.settings.notifyOnSuccess ?? true,
      notifyOnFailure: status?.settings.notifyOnFailure ?? true,
    })
  }

  const handleAutoUploadChange = (next: boolean) => {
    setAutoUpload(next)
    const currentStatus = status
    if (statusState !== 'healthy' || !currentStatus) return
    if (autoUploadDebounce.current) {
      clearTimeout(autoUploadDebounce.current)
    }
    autoUploadDebounce.current = setTimeout(() => {
      saveSettingsMutation.mutate({
        folder: folder || currentStatus.settings.folder || 'hserver-backups',
        autoUpload: next,
        remoteRetentionDays: retentionDays,
        notifyOnSuccess: currentStatus.settings.notifyOnSuccess ?? true,
        notifyOnFailure: currentStatus.settings.notifyOnFailure ?? true,
      })
    }, 600)
  }

  useEffect(() => {
    return () => {
      if (autoUploadDebounce.current) clearTimeout(autoUploadDebounce.current)
    }
  }, [])

  if (isLoading) {
    return <Skeleton className="h-56 w-full bg-zinc-800 rounded-xl" />
  }

  if (isError || !status) {
    return (
      <Card className="bg-zinc-900 border-zinc-800">
        <CardContent className="p-5">
          <DependencyRemediation
            title="Google Drive entegrasyon durumu alınamadı"
            summary="Heyserver, uzak yedekleme bağımlılıklarını algılayamadı. Bağlantı ve yükleme işlemleri durum doğrulanana kadar duraklatıldı."
            state="unavailable"
            steps={[
              <>Heyserver API sağlığını ve servis günlüklerini kontrol edin.</>,
              <><code>rclone version</code> komutunun Heyserver servis ortamında çalıştığını doğrulayın.</>,
              <>API veya ağ sorununu giderdikten sonra algılamayı yeniden deneyin.</>,
            ]}
            error={error instanceof Error ? error.message : undefined}
            retry={() => { void refetch() }}
            retrying={isFetching}
          />
        </CardContent>
      </Card>
    )
  }

  const st = status
  const quota = st.quota
  const remoteCount = remoteData?.backups?.length
  const authExpired =
    statusState !== 'healthy' &&
    (st.reconnectRequired ||
      /401|invalid credentials|token refresh|oauth/i.test(st.settings.lastError ?? ''))

  return (
    <Card id="gdrive-section" className="bg-zinc-900 border-zinc-800 overflow-hidden">
      {/* Hero header */}
      <div className="relative border-b border-zinc-800 bg-gradient-to-br from-blue-950/40 via-zinc-900 to-zinc-900 px-5 py-4">
        <div className="flex items-start justify-between gap-4 flex-wrap">
          <div className="flex items-start gap-3">
            <div className="rounded-xl bg-blue-500/10 p-2.5 ring-1 ring-blue-500/20">
              <Cloud className="w-5 h-5 text-blue-400" />
            </div>
            <div>
              <h3 className="text-white font-semibold text-base">Uzak Yedekleme — Google Drive</h3>
              <p className="text-zinc-500 text-xs mt-0.5 max-w-md">
                Sunucu dışında ikinci kopya. Verileriniz sizin Drive hesabınızda kalır — Plesk tarzı offsite koruma.
              </p>
            </div>
          </div>
          <div className="flex items-center gap-2 flex-wrap">
            {statusPresentation.state === 'healthy' ? (
              <Badge className="bg-emerald-500/10 text-emerald-400 border-emerald-500/20">
                <CheckCircle2 className="w-3 h-3 mr-1" />
                Bağlı
              </Badge>
            ) : statusPresentation.state === 'not-configured' ? (
              <Badge className="bg-amber-500/10 text-amber-400 border-amber-500/20">Yapılandırılmadı</Badge>
            ) : (
              <Badge className="bg-red-500/10 text-red-400 border-red-500/20">Kullanılamıyor</Badge>
            )}
            {!st.rcloneFound && (
              <Badge className="bg-amber-500/10 text-amber-400 border-amber-500/20">
                rclone eksik
              </Badge>
            )}
            {statusPresentation.state === 'healthy' && effectiveAutoUpload && (
              <Badge className="bg-violet-500/10 text-violet-400 border-violet-500/20">
                <Upload className="w-3 h-3 mr-1" />
                Otomatik yükleme açık
              </Badge>
            )}
          </div>
        </div>
      </div>

      <CardContent className="p-5 space-y-5">
        {!st.rcloneFound && (
          <DependencyRemediation
            title="rclone kurulumu gerekli"
            summary="Google Drive yedekleme akışı rclone olmadan çalışamaz. Heyserver paketleri otomatik olarak kurmaz ve OAuth bağlantısını bağımlılık hazır olana kadar devre dışı bırakır."
            state={statusPresentation.state === 'healthy' ? 'unavailable' : statusPresentation.state}
            steps={[
              <>İşletim sisteminizin desteklenen paket kaynağından <code>rclone</code> kurun.</>,
              <><code>rclone version</code> komutunu Heyserver servis kullanıcısının ortamında doğrulayın.</>,
              <>Heyserver servisini yeniden başlatın ve algılamayı yeniden deneyin.</>,
            ]}
            retry={() => { void refetch() }}
            retrying={isFetching}
          />
        )}

        {authExpired && (
          <div className="rounded-lg border border-red-500/40 bg-red-500/10 px-4 py-3 space-y-2">
            <p className="text-red-300 text-sm font-medium flex items-center gap-2">
              <AlertTriangle className="w-4 h-4 shrink-0" />
              Google Drive oturumu sona erdi
            </p>
            <p className="text-red-200/80 text-xs">
              Snapshot ve uzak yükleme çalışmaz. Önce bağlantıyı kesin (varsa), ardından OAuth ile yeniden
              bağlanın ve &quot;Bağlantıyı test et&quot; ile doğrulayın.
            </p>
            {st.settings.lastError && (
              <p className="text-red-400/70 text-[11px] font-mono break-all">{st.settings.lastError}</p>
            )}
          </div>
        )}

        {statusPresentation.state !== 'healthy' && (
          <GDriveWizard
            oauthApp={st.oauthApp}
            redirectUri={st.redirectUri}
            configured={st.configured}
            dependencyReady={st.rcloneFound}
            onCredentialsSaved={() => refetch()}
            onConnect={() => connectMutation.mutate()}
            connectPending={connectMutation.isPending}
          />
        )}

        {statusPresentation.state === 'healthy' && (
          <>
            {/* Account + quota dashboard */}
            <div className="grid grid-cols-1 lg:grid-cols-5 gap-4">
              <div className="lg:col-span-2 rounded-xl border border-zinc-800 bg-zinc-950/50 p-4 space-y-3">
                <div className="flex items-center gap-2 text-zinc-500 text-xs uppercase tracking-wide font-medium">
                  <Shield className="w-3.5 h-3.5" />
                  Bağlı hesap
                </div>
                <p className="text-white font-medium truncate">{st.email ?? st.displayName}</p>
                <div className="flex items-center gap-2 text-zinc-500 text-xs">
                  <FolderOpen className="w-3.5 h-3.5 shrink-0" />
                  <span className="truncate font-mono text-zinc-400">
                    /{folder || st.settings.folder || 'hserver-backups'}
                  </span>
                </div>
                {st.settings.lastUploadAt && (
                  <p className="text-zinc-500 text-xs pt-1 border-t border-zinc-800">
                    Son yükleme: {formatDate(st.settings.lastUploadAt)}
                    {st.settings.lastUploadFile && (
                      <span className="block font-mono text-zinc-600 truncate mt-0.5">
                        {st.settings.lastUploadFile}
                      </span>
                    )}
                  </p>
                )}
              </div>

              <div className="lg:col-span-3 rounded-xl border border-zinc-800 bg-zinc-950/50 p-4 space-y-3">
                <div className="flex items-center justify-between">
                  <span className="text-zinc-500 text-xs uppercase tracking-wide font-medium">Depolama kotası</span>
                  {quota && quota.limit > 0 && (
                    <span className="text-zinc-400 text-xs tabular-nums">
                      {quota.usagePercentage.toFixed(1)}% dolu
                    </span>
                  )}
                </div>
                {quota ? (
                  <QuotaBar usage={quota.usage} limit={quota.limit} showLabels />
                ) : (
                  <p className="text-zinc-500 text-sm">Kota bilgisi alınamadı</p>
                )}
                {quota && (
                  <p className="text-zinc-600 text-[11px]">
                    Kişisel Drive kotanız — yedekler yalnızca uygulama klasöründe (
                    <code className="text-zinc-500">drive.file</code> izni).
                  </p>
                )}
              </div>
            </div>

            {st.settings.lastError && (
              <div className="rounded-lg border border-red-500/30 bg-red-500/10 px-4 py-3 flex items-start gap-2">
                <AlertTriangle className="w-4 h-4 text-red-400 shrink-0 mt-0.5" />
                <div className="min-w-0 flex-1">
                  <p className="text-red-300 text-sm font-medium">Son işlem hatası</p>
                  <p className="text-red-400/80 text-xs mt-0.5 font-mono break-all">{st.settings.lastError}</p>
                  <p className="text-amber-200/80 text-xs mt-2">{backupOperationHint(st.settings.lastError)}</p>
                  <div className="flex flex-wrap gap-2 mt-3">
                    <Button
                      size="sm"
                      variant="ghost"
                      className="h-7 text-xs text-zinc-300"
                      onClick={() => testMutation.mutate()}
                      disabled={testMutation.isPending}
                    >
                      Bağlantıyı test et
                    </Button>
                    <Button
                      size="sm"
                      variant="ghost"
                      className="h-7 text-xs text-zinc-400"
                      onClick={() => dismissErrorMutation.mutate()}
                      disabled={dismissErrorMutation.isPending}
                    >
                      Hatayı kapat
                    </Button>
                  </div>
                </div>
              </div>
            )}

            {/* Primary control: auto-upload */}
            <ToggleSwitch
              checked={effectiveAutoUpload}
              onChange={handleAutoUploadChange}
              disabled={saveSettingsMutation.isPending}
              label="Yerel yedek bitince otomatik Drive'a yükle"
              description="Tam yedek tamamlandığında arka planda rclone ile uzak depoya kopyalanır."
            />

            {/* Collapsible settings */}
            <div className="rounded-xl border border-zinc-800 overflow-hidden">
              <button
                type="button"
                onClick={toggleSettings}
                className="w-full flex items-center justify-between px-4 py-3 text-left hover:bg-zinc-800/40 transition-colors"
              >
                <span className="text-zinc-300 text-sm font-medium">Gelişmiş ayarlar</span>
                <ChevronDown
                  className={`w-4 h-4 text-zinc-500 transition-transform ${settingsOpen ? 'rotate-180' : ''}`}
                />
              </button>
              {settingsOpen && (
                <div className="px-4 pb-4 pt-1 border-t border-zinc-800 space-y-3">
                  <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
                    <div className="space-y-1.5">
                      <label className="text-zinc-500 text-xs">Drive klasör adı</label>
                      <input
                        type="text"
                        value={folder || st.settings.folder}
                        onChange={(e) => setFolder(e.target.value)}
                        placeholder="hserver-backups"
                        className="w-full bg-zinc-900 border border-zinc-700 rounded-lg px-3 py-2 text-sm text-white focus:outline-none focus:border-blue-500"
                      />
                    </div>
                    <div className="space-y-1.5">
                      <label className="text-zinc-500 text-xs">Uzak retention (gün)</label>
                      <input
                        type="number"
                        min={0}
                        max={365}
                        value={retentionDays}
                        onChange={(e) => setRetentionDays(Number(e.target.value))}
                        className="w-full bg-zinc-900 border border-zinc-700 rounded-lg px-3 py-2 text-sm text-white focus:outline-none focus:border-blue-500"
                      />
						<p className="text-zinc-600 text-[11px]">0 uzak silmeyi kapatır; 1–365 gün arası otomatik temizlenir.</p>
                    </div>
                  </div>
                  <Button
                    size="sm"
                    onClick={handleSaveSettings}
                    disabled={saveSettingsMutation.isPending}
                    className="bg-blue-600 hover:bg-blue-500 text-white"
                  >
                    {saveSettingsMutation.isPending && (
                      <Loader2 className="w-3.5 h-3.5 mr-1.5 animate-spin" />
                    )}
                    Ayarları kaydet
                  </Button>
                </div>
              )}
            </div>

            {/* Inline remote backups */}
            <div className="rounded-xl border border-zinc-800 overflow-hidden">
              <button
                type="button"
                onClick={() => setRemoteExpanded((v) => !v)}
                className="w-full flex items-center justify-between px-4 py-3 text-left hover:bg-zinc-800/40 transition-colors"
              >
                <div className="flex items-center gap-2">
                  <HardDrive className="w-4 h-4 text-blue-400" />
                  <span className="text-zinc-300 text-sm font-medium">Drive'daki yedekler</span>
                  {remoteExpanded && remoteCount !== undefined && (
                    <Badge variant="outline" className="border-zinc-700 text-zinc-500 text-[10px]">
                      {remoteCount} dosya
                    </Badge>
                  )}
                </div>
                <ChevronDown
                  className={`w-4 h-4 text-zinc-500 transition-transform ${remoteExpanded ? 'rotate-180' : ''}`}
                />
              </button>
              {remoteExpanded && (
                <div className="border-t border-zinc-800 px-4 py-3">
                  {remoteLoading ? (
                    <div className="space-y-2">
                      {Array.from({ length: 2 }).map((_, i) => (
                        <Skeleton key={i} className="h-12 w-full bg-zinc-800" />
                      ))}
                    </div>
                  ) : remoteError ? (
                    <div className="flex items-center justify-between gap-3 py-2">
                      <p className="text-red-400 text-sm">Liste alınamadı — bağlantıyı test edin.</p>
                      <Button size="sm" variant="ghost" onClick={() => refetchRemote()}>
                        Tekrar dene
                      </Button>
                    </div>
                  ) : (remoteData?.backups ?? []).length === 0 ? (
                    <p className="text-zinc-500 text-sm py-2">
                      Henüz Drive'da yedek yok. İlk tam yedekten sonra burada görünür.
                    </p>
                  ) : (
                    <div className="space-y-2 max-h-64 overflow-y-auto">
                      {(remoteData?.backups ?? []).map((b) => (
                        <div
                          key={b.name}
                          className="flex items-center justify-between gap-3 rounded-lg bg-zinc-800/50 px-3 py-2.5 group"
                        >
                          <div className="min-w-0">
                            <div className="flex items-center gap-2 flex-wrap">
                              <p className="text-white text-sm font-mono truncate">{b.name}</p>
                              {b.size > 0 && b.size < 1024 && (
                                <Badge className="bg-amber-500/10 text-amber-400 border-amber-500/20 text-[10px]">
                                  Geçersiz
                                </Badge>
                              )}
                            </div>
                            <p className="text-zinc-500 text-xs">
                              {formatBytes(b.size)} · {b.modTime ? formatDate(b.modTime) : '—'}
                            </p>
                            {b.size > 0 && b.size < 1024 && (
                              <p className="text-amber-400/70 text-[10px] mt-0.5">
                                Boş veya hatalı yükleme — Drive&apos;dan silip yeniden yükleyin.
                              </p>
                            )}
                          </div>
                          <Button
                            size="sm"
                            variant="ghost"
                            disabled={restoreMutation.isPending}
                            onClick={() => restoreMutation.mutate(b.name)}
                            title="Sunucuya indir"
                            className="text-zinc-400 hover:text-white shrink-0"
                          >
                            {restoreMutation.isPending ? (
                              <Loader2 className="w-3.5 h-3.5 animate-spin" />
                            ) : (
                              <Download className="w-3.5 h-3.5" />
                            )}
                          </Button>
                        </div>
                      ))}
                    </div>
                  )}
                  <div className="flex justify-end pt-2">
                    <Button
                      size="sm"
                      variant="ghost"
                      onClick={() => refetchRemote()}
                      className="text-zinc-500 hover:text-white text-xs"
                    >
                      <RefreshCw className="w-3 h-3 mr-1.5" />
                      Listeyi yenile
                    </Button>
                  </div>
                </div>
              )}
            </div>

            {/* Action bar */}
            <div className="flex flex-wrap items-center gap-2 pt-1 border-t border-zinc-800">
              <Button
                size="sm"
                variant="outline"
                onClick={() => testMutation.mutate()}
                disabled={testMutation.isPending}
                className="border-zinc-700 text-zinc-300 hover:text-white"
              >
                {testMutation.isPending ? (
                  <Loader2 className="w-3.5 h-3.5 mr-1.5 animate-spin" />
                ) : (
                  <RefreshCw className="w-3.5 h-3.5 mr-1.5" />
                )}
                Bağlantıyı test et
              </Button>
              <Button
                size="sm"
                variant="ghost"
                onClick={() => refetch()}
                className="text-zinc-500 hover:text-white"
              >
                Durumu yenile
              </Button>
              <div className="flex-1" />
              <Button
                size="sm"
                variant="ghost"
                onClick={() => disconnectMutation.mutate()}
                disabled={disconnectMutation.isPending}
                className="text-zinc-500 hover:text-red-400"
              >
                <Unlink className="w-3.5 h-3.5 mr-1.5" />
                Bağlantıyı kes
              </Button>
            </div>
          </>
        )}
      </CardContent>
    </Card>
  )
}

export { Upload as GDriveUploadIcon }
