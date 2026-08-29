import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import {
  ExternalLink,
  Copy,
  CheckCircle2,
  Loader2,
  ChevronRight,
  KeyRound,
  Zap,
  Wrench,
  Cloud,
} from 'lucide-react'
import { Button } from '@/components/ui/button'
import { api } from '@/lib/api'
import { toast } from 'sonner'
import type { GDriveOAuthAppInfo, GDriveOAuthAppUpdateRequest } from '@/lib/types'

interface GDriveWizardProps {
  oauthApp?: GDriveOAuthAppInfo
  redirectUri?: string
  configured: boolean
  dependencyReady: boolean
  onCredentialsSaved: () => void
  onConnect: () => void
  connectPending: boolean
}

const STEPS = ['Hazırlık', 'Kimlik bilgileri', 'Bağlan'] as const

function StepIndicator({ current, total }: { current: number; total: number }) {
  return (
    <div className="flex items-center gap-2 mb-4">
      {Array.from({ length: total }).map((_, i) => {
        const n = i + 1
        const active = n === current
        const done = n < current
        return (
          <div key={n} className="flex items-center gap-2 flex-1 last:flex-none">
            <div
              className={`flex items-center justify-center w-7 h-7 rounded-full text-xs font-semibold shrink-0 ${
                done
                  ? 'bg-emerald-500/20 text-emerald-400 ring-1 ring-emerald-500/30'
                  : active
                  ? 'bg-blue-500/20 text-blue-400 ring-1 ring-blue-500/40'
                  : 'bg-zinc-800 text-zinc-500 ring-1 ring-zinc-700'
              }`}
            >
              {done ? <CheckCircle2 className="w-3.5 h-3.5" /> : n}
            </div>
            <span
              className={`text-xs hidden sm:block ${
                active ? 'text-zinc-200 font-medium' : 'text-zinc-600'
              }`}
            >
              {STEPS[i]}
            </span>
            {i < total - 1 && <div className="flex-1 h-px bg-zinc-800 min-w-4" />}
          </div>
        )
      })}
    </div>
  )
}

function CopyButton({ text }: { text: string }) {
  const [copied, setCopied] = useState(false)
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(text)
      setCopied(true)
      toast.success('Panoya kopyalandı')
      setTimeout(() => setCopied(false), 2000)
    } catch {
      toast.error('Kopyalanamadı')
    }
  }
  return (
    <Button type="button" size="sm" variant="ghost" onClick={copy} className="text-zinc-400 shrink-0">
      {copied ? <CheckCircle2 className="w-3.5 h-3.5 text-green-400" /> : <Copy className="w-3.5 h-3.5" />}
    </Button>
  )
}

function GoogleConnectButton({ onClick, pending, disabled }: { onClick: () => void; pending: boolean; disabled?: boolean }) {
  return (
    <Button
      size="default"
      disabled={disabled || pending}
      onClick={onClick}
      className="w-full sm:w-auto bg-white hover:bg-zinc-100 text-zinc-900 font-medium h-11"
    >
      {pending ? (
        <Loader2 className="w-4 h-4 mr-2 animate-spin" />
      ) : (
        <svg className="w-4 h-4 mr-2" viewBox="0 0 24 24" aria-hidden>
          <path fill="#4285F4" d="M22.56 12.25c0-.78-.07-1.53-.2-2.25H12v4.26h5.92c-.26 1.37-1.04 2.53-2.21 3.31v2.77h3.57c2.08-1.92 3.28-4.74 3.28-8.09z" />
          <path fill="#34A853" d="M12 23c2.97 0 5.46-.98 7.28-2.66l-3.57-2.77c-.98.66-2.23 1.06-3.71 1.06-2.86 0-5.29-1.93-6.16-4.53H2.18v2.84C3.99 20.53 7.7 23 12 23z" />
          <path fill="#FBBC05" d="M5.84 14.09c-.22-.66-.35-1.36-.35-2.09s.13-1.43.35-2.09V7.07H2.18C1.43 8.55 1 10.22 1 12s.43 3.45 1.18 4.93l2.85-2.22.81-.62z" />
          <path fill="#EA4335" d="M12 5.38c1.62 0 3.06.56 4.21 1.64l3.15-3.15C17.45 2.09 14.97 1 12 1 7.7 1 3.99 3.47 2.18 7.07l3.66 2.84c.87-2.6 3.3-4.53 6.16-4.53z" />
        </svg>
      )}
      Google ile bağlan ve izin ver
    </Button>
  )
}

export default function GDriveWizard({
  oauthApp,
  redirectUri,
  configured,
  dependencyReady,
  onCredentialsSaved,
  onConnect,
  connectPending,
}: GDriveWizardProps) {
  const queryClient = useQueryClient()
  const uri = redirectUri || oauthApp?.redirectUri || ''
  const links = oauthApp?.setupLinks
  const [mode, setMode] = useState<'express' | 'advanced'>(configured ? 'express' : 'express')
  const [clientId, setClientId] = useState(oauthApp?.clientId ?? '')
  const [clientSecret, setClientSecret] = useState('')
  const [gcpProjectId, setGcpProjectId] = useState(oauthApp?.gcpProjectId ?? '')
  const [step, setStep] = useState(configured ? 3 : 1)

  const saveAppMutation = useMutation({
    mutationFn: (body: GDriveOAuthAppUpdateRequest) =>
      api.put('/backups/gdrive/oauth-app', body),
    onSuccess: (_data, vars) => {
      queryClient.invalidateQueries({ queryKey: ['gdrive-status'] })
      queryClient.invalidateQueries({ queryKey: ['gdrive-oauth-app'] })
      if (vars.clientId) {
        toast.success('Google uygulama bilgileri kaydedildi')
        setStep(3)
        onCredentialsSaved()
      } else if (vars.gcpProjectId) {
        toast.success('GCP proje ID kaydedildi')
      }
    },
    onError: (e: Error) => toast.error(e.message || 'Kaydedilemedi'),
  })

  const handleSave = (e: React.FormEvent) => {
    e.preventDefault()
    saveAppMutation.mutate({
      clientId: clientId.trim(),
      clientSecret: clientSecret.trim() || undefined,
      gcpProjectId: gcpProjectId.trim() || undefined,
    })
  }

  const saveProjectId = () => {
    if (!gcpProjectId.trim()) return
    saveAppMutation.mutate({ gcpProjectId: gcpProjectId.trim() })
  }

  // Plesk-style: credentials pre-provisioned
  if (oauthApp?.credentialsSource === 'env' || oauthApp?.credentialsSource === 'vendor') {
    const label =
      oauthApp.credentialsSource === 'env'
        ? 'sunucu ortam değişkenleri'
        : 'yönetimli OAuth uygulaması'
    return (
      <div className="rounded-xl border border-emerald-500/20 bg-emerald-950/20 p-5 space-y-4">
        <StepIndicator current={3} total={3} />
        <div className="flex items-center gap-2 text-white font-medium">
          <Zap className="w-4 h-4 text-emerald-400" />
          Hızlı kurulum hazır
        </div>
        <p className="text-zinc-400 text-sm leading-relaxed">
          OAuth {label} üzerinden yüklendi. Tek yapmanız gereken Google hesabınızı bağlamak — Console adımı yok.
        </p>
        {uri && (
          <div className="rounded-lg border border-amber-500/30 bg-amber-500/5 p-3 space-y-2">
            <p className="text-amber-200/90 text-xs font-medium">
              redirect_uri_mismatch hatası alırsanız bu URI'yi Authorized redirect URIs alanına ekleyin:
            </p>
            <div className="flex items-center gap-2 bg-zinc-900 rounded-lg px-3 py-2">
              <code className="text-xs text-amber-100 break-all flex-1">{uri}</code>
              <CopyButton text={uri} />
            </div>
          </div>
        )}
        <GoogleConnectButton onClick={onConnect} pending={connectPending} disabled={!dependencyReady} />
      </div>
    )
  }

  if (configured && oauthApp?.credentialsSource === 'panel') {
    return (
      <div className="rounded-xl border border-blue-500/20 bg-blue-950/10 p-5 space-y-4">
        <StepIndicator current={3} total={3} />
        <div className="flex items-center gap-2 text-white font-medium">
          <KeyRound className="w-4 h-4 text-blue-400" />
          Bağlantıya hazır
        </div>
        <p className="text-zinc-500 text-sm">
          OAuth uygulaması kayıtlı ({oauthApp.clientIdMasked}). Google hesabınızla tek tıkla bağlanın.
        </p>
        <GoogleConnectButton onClick={onConnect} pending={connectPending} disabled={!dependencyReady} />
      </div>
    )
  }

  return (
    <div className="rounded-xl border border-zinc-800 bg-zinc-950/50 p-5 space-y-4">
      <div className="flex items-center gap-2 text-white font-medium">
        <Cloud className="w-4 h-4 text-blue-400" />
        İlk kurulum sihirbazı
      </div>

      <div className="flex gap-2">
        <Button
          type="button"
          size="sm"
          variant={mode === 'express' ? 'default' : 'outline'}
          className={mode === 'express' ? 'bg-emerald-700 hover:bg-emerald-600' : 'border-zinc-700'}
          onClick={() => setMode('express')}
        >
          <Zap className="w-3.5 h-3.5 mr-1" />
          Hızlı
        </Button>
        <Button
          type="button"
          size="sm"
          variant={mode === 'advanced' ? 'default' : 'outline'}
          className={mode === 'advanced' ? 'bg-blue-700 hover:bg-blue-600' : 'border-zinc-700'}
          onClick={() => setMode('advanced')}
        >
          <Wrench className="w-3.5 h-3.5 mr-1" />
          Kendi GCP projeniz
        </Button>
      </div>

      {mode === 'express' ? (
        <div className="rounded-lg p-4 bg-zinc-800/60 ring-1 ring-zinc-700 space-y-3">
          <p className="text-zinc-300 text-sm font-medium">Yönetici kurulumu gerekli</p>
          <p className="text-zinc-400 text-xs leading-relaxed">
            Google OAuth client oluşturmayı API ile otomatikleştirmeye izin vermiyor. Plesk de merkezi OAuth
            kullanır; sunucu yöneticisi bir kez{' '}
            <code className="text-zinc-300">HSERVER_GDRIVE_CLIENT_ID</code> /{' '}
            <code className="text-zinc-300">SECRET</code> ayarlar, siz yalnızca bağlanırsınız.
          </p>
          <p className="text-amber-400/90 text-xs">
            Bu sunucuda henüz tanımlı değil — &quot;Kendi GCP projeniz&quot; sekmesine geçin veya yöneticiden hızlı kurulum isteyin.
          </p>
        </div>
      ) : (
        <>
          <StepIndicator current={step} total={3} />

          <div className={`rounded-lg p-4 transition-colors ${step === 1 ? 'bg-zinc-800/80 ring-1 ring-blue-500/30' : 'bg-zinc-800/30'}`}>
            <p className="text-zinc-300 text-sm font-medium mb-2">1. Google Cloud projesi</p>
            <p className="text-zinc-500 text-xs mb-3">Proje ID ile Console sayfalarına tek tıkla gidin.</p>
            <div className="flex gap-2 mb-3">
              <input
                type="text"
                value={gcpProjectId}
                onChange={(e) => setGcpProjectId(e.target.value)}
                placeholder="proje-id"
                className="flex-1 bg-zinc-900 border border-zinc-700 rounded-lg px-3 py-2 text-sm text-white focus:outline-none focus:border-blue-500"
              />
              <Button
                type="button"
                size="sm"
                variant="outline"
                className="border-zinc-600 shrink-0"
                disabled={saveAppMutation.isPending || !gcpProjectId.trim()}
                onClick={saveProjectId}
              >
                Kaydet
              </Button>
            </div>
            <div className="flex flex-wrap gap-2 mb-3">
              {links?.enableDriveAPI && (
                <a
                  href={links.enableDriveAPI}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="text-xs inline-flex items-center gap-1 px-2.5 py-1.5 rounded-md bg-zinc-900 text-blue-400 hover:bg-zinc-800"
                >
                  Drive API
                  <ExternalLink className="w-3 h-3" />
                </a>
              )}
              {links?.createOAuthClient && (
                <a
                  href={links.createOAuthClient}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="text-xs inline-flex items-center gap-1 px-2.5 py-1.5 rounded-md bg-zinc-900 text-blue-400 hover:bg-zinc-800"
                >
                  OAuth client
                  <ExternalLink className="w-3 h-3" />
                </a>
              )}
            </div>
            {uri && (
              <div className="flex items-center gap-2 bg-zinc-900 rounded-lg px-3 py-2 mb-3">
                <code className="text-xs text-zinc-300 break-all flex-1">{uri}</code>
                <CopyButton text={uri} />
              </div>
            )}
            {step === 1 && (
              <Button size="sm" className="bg-zinc-700 hover:bg-zinc-600" onClick={() => setStep(2)}>
                Client oluşturdum
                <ChevronRight className="w-3.5 h-3.5 ml-1" />
              </Button>
            )}
          </div>

          <div className={`rounded-lg p-4 transition-colors ${step === 2 ? 'bg-zinc-800/80 ring-1 ring-blue-500/30' : 'bg-zinc-800/30'}`}>
            <p className="text-zinc-300 text-sm font-medium mb-2">2. Kimlik bilgileri</p>
            <form onSubmit={handleSave} className="space-y-3">
              <div>
                <label className="text-zinc-500 text-xs">Client ID</label>
                <input
                  type="text"
                  required
                  value={clientId}
                  onChange={(e) => setClientId(e.target.value)}
                  placeholder="xxxxx.apps.googleusercontent.com"
                  className="w-full mt-1 bg-zinc-900 border border-zinc-700 rounded-lg px-3 py-2 text-sm text-white focus:outline-none focus:border-blue-500"
                />
              </div>
              <div>
                <label className="text-zinc-500 text-xs">
                  Client Secret {oauthApp?.hasSecret && '(boş = mevcut korunur)'}
                </label>
                <input
                  type="password"
                  required={!oauthApp?.hasSecret}
                  value={clientSecret}
                  onChange={(e) => setClientSecret(e.target.value)}
                  placeholder="GOCSPX-..."
                  className="w-full mt-1 bg-zinc-900 border border-zinc-700 rounded-lg px-3 py-2 text-sm text-white focus:outline-none focus:border-blue-500"
                />
              </div>
              <Button
                type="submit"
                size="sm"
                disabled={saveAppMutation.isPending}
                className="bg-blue-600 hover:bg-blue-500 text-white"
              >
                {saveAppMutation.isPending && <Loader2 className="w-3.5 h-3.5 mr-1.5 animate-spin" />}
                Kaydet ve devam
              </Button>
            </form>
          </div>

          <div className={`rounded-lg p-4 transition-colors ${step === 3 ? 'bg-zinc-800/80 ring-1 ring-emerald-500/30' : 'bg-zinc-800/30'}`}>
            <p className="text-zinc-300 text-sm font-medium mb-2">3. Google hesabı</p>
            <p className="text-zinc-500 text-xs mb-3">
              Yalnızca uygulama klasörüne erişim (<code className="text-zinc-400">drive.file</code>).
            </p>
            <GoogleConnectButton onClick={onConnect} pending={connectPending} disabled={!configured || !dependencyReady} />
            {!configured && (
              <p className="text-amber-400/80 text-xs mt-2">Önce 2. adımda kimlik bilgilerini kaydedin.</p>
            )}
          </div>
        </>
      )}
    </div>
  )
}
