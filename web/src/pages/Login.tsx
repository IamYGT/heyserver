import { useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Server, Eye, EyeOff, Loader2, ShieldCheck, KeyRound, ArrowLeft } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { useLogin, useTotpVerify, useRecoveryLogin, TotpRequiredError } from '@/hooks/useAuth'
import { ApiError } from '@/lib/api'
import { toast } from 'sonner'

type Screen = 'password' | 'totp' | 'recovery'

function authErrorMessage(error: unknown, screen: Screen): string {
  const status = error instanceof ApiError ? error.status : undefined
  const credentialsRejected = status === 401 || status === 403
  const serviceUnavailable = status === 0 || (status !== undefined && status >= 500 && status < 600)

  if (screen === 'password') {
    if (credentialsRejected) return 'Invalid credentials. Please try again.'
    if (serviceUnavailable) return 'Sign-in is temporarily unavailable. Please check your connection and try again.'
    return 'Sign-in failed. Please try again.'
  }

  if (screen === 'totp') {
    if (credentialsRejected) return 'Invalid authentication code. Please try again.'
    if (serviceUnavailable) return 'Two-factor authentication is temporarily unavailable. Please check your connection and try again.'
    return 'Two-factor authentication failed. Please try again.'
  }

  if (credentialsRejected) return 'Invalid recovery code. Please try again.'
  if (serviceUnavailable) return 'Recovery sign-in is temporarily unavailable. Please check your connection and try again.'
  return 'Recovery sign-in failed. Please try again.'
}

export default function Login() {
  const navigate = useNavigate()
  const login = useLogin()
  const totpVerify = useTotpVerify()
  const recoveryLogin = useRecoveryLogin()

  // Password screen state
  const [showPassword, setShowPassword] = useState(false)
  const [form, setForm] = useState({ email: '', password: '' })

  // TOTP / recovery screen state
  const [screen, setScreen] = useState<Screen>('password')
  const [totpCode, setTotpCode] = useState('')
  const [recoveryCode, setRecoveryCode] = useState('')
  const [pendingCredentials, setPendingCredentials] = useState<{ email: string; password: string } | null>(null)

  const totpInputRef = useRef<HTMLInputElement>(null)
  const recoveryInputRef = useRef<HTMLInputElement>(null)

  // Auto-focus TOTP / recovery inputs when screen changes
  useEffect(() => {
    if (screen === 'totp') {
      setTimeout(() => totpInputRef.current?.focus(), 50)
    } else if (screen === 'recovery') {
      setTimeout(() => recoveryInputRef.current?.focus(), 50)
    }
  }, [screen])

  const handlePasswordSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    try {
      await login.mutateAsync(form)
      navigate('/')
    } catch (err) {
      if (err instanceof TotpRequiredError) {
        setPendingCredentials({ email: err.email, password: err.credentials.password })
        setScreen('totp')
        return
      }
      toast.error(authErrorMessage(err, 'password'))
    }
  }

  const handleTotpSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!pendingCredentials) return
    try {
      await totpVerify.mutateAsync({
        email: pendingCredentials.email,
        password: pendingCredentials.password,
        code: totpCode,
      })
      setPendingCredentials(null)
      navigate('/')
    } catch (err) {
      toast.error(authErrorMessage(err, 'totp'))
      setTotpCode('')
      setTimeout(() => totpInputRef.current?.focus(), 50)
    }
  }

  const handleRecoverySubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!pendingCredentials) return
    try {
      await recoveryLogin.mutateAsync({
        email: pendingCredentials.email,
        password: pendingCredentials.password,
        recovery_code: recoveryCode,
      })
      setPendingCredentials(null)
      navigate('/')
    } catch (err) {
      toast.error(authErrorMessage(err, 'recovery'))
      setRecoveryCode('')
      setTimeout(() => recoveryInputRef.current?.focus(), 50)
    }
  }

  const goBackToPassword = () => {
    setScreen('password')
    setPendingCredentials(null)
    setTotpCode('')
    setRecoveryCode('')
  }

  const isLoading = login.isPending || totpVerify.isPending || recoveryLogin.isPending

  return (
    <div className="min-h-screen bg-zinc-950 flex items-center justify-center p-4">
      <div className="w-full max-w-sm">
        {/* Logo */}
        <div className="flex flex-col items-center mb-8">
          <div className="w-14 h-14 bg-blue-600 rounded-2xl flex items-center justify-center mb-4 shadow-lg shadow-blue-600/25">
            {screen === 'password' ? (
              <Server className="w-7 h-7 text-white" />
            ) : (
              <ShieldCheck className="w-7 h-7 text-white" />
            )}
          </div>
          <h1 className="text-white text-2xl font-bold">Heyserver Panel</h1>
          <p className="text-zinc-500 text-sm mt-1">
            {screen === 'password' && 'Sign in to your account'}
            {screen === 'totp' && 'Two-factor authentication'}
            {screen === 'recovery' && 'Use recovery code'}
          </p>
        </div>

        {/* Password Screen */}
        {screen === 'password' && (
          <form
            onSubmit={handlePasswordSubmit}
            className="bg-zinc-900 border border-zinc-800 rounded-2xl p-6 space-y-4"
          >
            <div className="space-y-1.5">
              <Label htmlFor="email" className="text-zinc-400 text-xs font-medium uppercase tracking-wide">
                Email
              </Label>
              <Input
                id="email"
                type="email"
                autoComplete="email"
                placeholder="admin@server.local"
                value={form.email}
                onChange={(e) => setForm((p) => ({ ...p, email: e.target.value }))}
                required
                className="bg-zinc-800 border-zinc-700 text-white placeholder:text-zinc-600 focus:border-blue-500 focus:ring-blue-500/20 h-10"
              />
            </div>

            <div className="space-y-1.5">
              <Label htmlFor="password" className="text-zinc-400 text-xs font-medium uppercase tracking-wide">
                Password
              </Label>
              <div className="relative">
                <Input
                  id="password"
                  type={showPassword ? 'text' : 'password'}
                  autoComplete="current-password"
                  placeholder="••••••••"
                  value={form.password}
                  onChange={(e) => setForm((p) => ({ ...p, password: e.target.value }))}
                  required
                  className="bg-zinc-800 border-zinc-700 text-white placeholder:text-zinc-600 focus:border-blue-500 focus:ring-blue-500/20 h-10 pr-10"
                />
                <button
                  type="button"
                  onClick={() => setShowPassword((p) => !p)}
                  className="absolute right-3 top-1/2 -translate-y-1/2 text-zinc-500 hover:text-zinc-300 transition-colors"
                >
                  {showPassword ? (
                    <EyeOff className="w-4 h-4" />
                  ) : (
                    <Eye className="w-4 h-4" />
                  )}
                </button>
              </div>
            </div>

            <Button
              type="submit"
              className="w-full bg-blue-600 hover:bg-blue-500 text-white h-10 font-medium"
              disabled={isLoading}
            >
              {isLoading ? (
                <>
                  <Loader2 className="w-4 h-4 mr-2 animate-spin" />
                  Signing in...
                </>
              ) : (
                'Sign In'
              )}
            </Button>
          </form>
        )}

        {/* TOTP Screen */}
        {screen === 'totp' && (
          <form
            onSubmit={handleTotpSubmit}
            className="bg-zinc-900 border border-zinc-800 rounded-2xl p-6 space-y-4"
          >
            <div className="text-center pb-1">
              <p className="text-zinc-400 text-sm leading-relaxed">
                Enter the 6-digit code from your authenticator app.
              </p>
            </div>

            <div className="space-y-1.5">
              <Label htmlFor="totp-code" className="text-zinc-400 text-xs font-medium uppercase tracking-wide">
                Authentication Code
              </Label>
              <Input
                ref={totpInputRef}
                id="totp-code"
                type="text"
                inputMode="numeric"
                pattern="[0-9]*"
                autoComplete="one-time-code"
                placeholder="000000"
                maxLength={6}
                value={totpCode}
                onChange={(e) => {
                  const val = e.target.value.replace(/\D/g, '')
                  setTotpCode(val)
                }}
                required
                className="bg-zinc-800 border-zinc-700 text-white placeholder:text-zinc-600 focus:border-blue-500 focus:ring-blue-500/20 h-10 text-center text-lg tracking-[0.5em] font-mono"
              />
            </div>

            <Button
              type="submit"
              className="w-full bg-blue-600 hover:bg-blue-500 text-white h-10 font-medium"
              disabled={isLoading || totpCode.length !== 6}
            >
              {isLoading ? (
                <>
                  <Loader2 className="w-4 h-4 mr-2 animate-spin" />
                  Verifying...
                </>
              ) : (
                'Verify Code'
              )}
            </Button>

            <div className="pt-1 space-y-2">
              <button
                type="button"
                onClick={() => setScreen('recovery')}
                className="w-full flex items-center justify-center gap-1.5 text-zinc-500 hover:text-zinc-300 text-xs transition-colors py-1"
              >
                <KeyRound className="w-3.5 h-3.5" />
                Use recovery code instead
              </button>
              <button
                type="button"
                onClick={goBackToPassword}
                className="w-full flex items-center justify-center gap-1.5 text-zinc-600 hover:text-zinc-400 text-xs transition-colors py-1"
              >
                <ArrowLeft className="w-3.5 h-3.5" />
                Back to sign in
              </button>
            </div>
          </form>
        )}

        {/* Recovery Code Screen */}
        {screen === 'recovery' && (
          <form
            onSubmit={handleRecoverySubmit}
            className="bg-zinc-900 border border-zinc-800 rounded-2xl p-6 space-y-4"
          >
            <div className="text-center pb-1">
              <p className="text-zinc-400 text-sm leading-relaxed">
                Enter one of your recovery codes.
                <br />
                <span className="text-zinc-600 text-xs">Format: XXXX-XXXX</span>
              </p>
            </div>

            <div className="space-y-1.5">
              <Label htmlFor="recovery-code" className="text-zinc-400 text-xs font-medium uppercase tracking-wide">
                Recovery Code
              </Label>
              <Input
                ref={recoveryInputRef}
                id="recovery-code"
                type="text"
                autoComplete="off"
                placeholder="XXXX-XXXX"
                maxLength={9}
                value={recoveryCode}
                onChange={(e) => {
                  // Auto-insert dash after 4 chars
                  let val = e.target.value.replace(/[^A-Za-z0-9-]/g, '').toUpperCase()
                  if (val.length === 4 && !val.includes('-') && recoveryCode.length < 4) {
                    val = val + '-'
                  }
                  setRecoveryCode(val)
                }}
                required
                className="bg-zinc-800 border-zinc-700 text-white placeholder:text-zinc-600 focus:border-blue-500 focus:ring-blue-500/20 h-10 text-center font-mono tracking-widest"
              />
            </div>

            <Button
              type="submit"
              className="w-full bg-blue-600 hover:bg-blue-500 text-white h-10 font-medium"
              disabled={isLoading || recoveryCode.length < 9}
            >
              {isLoading ? (
                <>
                  <Loader2 className="w-4 h-4 mr-2 animate-spin" />
                  Verifying...
                </>
              ) : (
                'Use Recovery Code'
              )}
            </Button>

            <div className="pt-1 space-y-2">
              <button
                type="button"
                onClick={() => setScreen('totp')}
                className="w-full flex items-center justify-center gap-1.5 text-zinc-500 hover:text-zinc-300 text-xs transition-colors py-1"
              >
                <ShieldCheck className="w-3.5 h-3.5" />
                Use authenticator app instead
              </button>
              <button
                type="button"
                onClick={goBackToPassword}
                className="w-full flex items-center justify-center gap-1.5 text-zinc-600 hover:text-zinc-400 text-xs transition-colors py-1"
              >
                <ArrowLeft className="w-3.5 h-3.5" />
                Back to sign in
              </button>
            </div>
          </form>
        )}

        <p className="text-center text-zinc-600 text-xs mt-6">
          Heyserver Panel — Self-hosted server management
        </p>
      </div>
    </div>
  )
}
