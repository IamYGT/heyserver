import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  Server,
  Shield,
  Globe,
  CheckCircle2,
  XCircle,
  ArrowRight,
  ArrowLeft,
  Loader2,
  LayoutDashboard,
  Lock,
  AlertTriangle,
} from 'lucide-react'
import { Card, CardContent } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { api } from '@/lib/api'
import { toast } from 'sonner'
import { cn } from '@/lib/utils'
import {
  firstDomainRequest,
  normalizeOnboardingStep,
  validFirstDomain,
  type OnboardingState,
} from '@/lib/onboarding'

// ─── Types ──────────────────────────────────────────────────────────────────

interface SystemInfo {
  hostname: string
  os: string
  kernel: string
  arch: string
  nginx: string
  php: string[]
  postgresql: string
}

interface SecurityCheck {
  name: string
  status: 'pass' | 'fail' | 'warn'
  detail: string
}

interface SecurityScore {
  score: number
  checks: SecurityCheck[]
}

// ─── Step progress indicator ─────────────────────────────────────────────────

const STEPS = [
  { label: 'Welcome', icon: Server },
  { label: 'Server', icon: Server },
  { label: 'Security', icon: Shield },
  { label: 'Domain', icon: Globe },
  { label: 'Done', icon: CheckCircle2 },
]

function StepIndicator({ current }: { current: number }) {
  return (
    <div className="flex items-center justify-center gap-0 mb-10">
      {STEPS.map((_, idx) => {
        const isDone = idx < current
        const isActive = idx === current
        return (
          <div key={idx} className="flex items-center">
            <div
              className={cn(
                'flex items-center justify-center w-8 h-8 rounded-full text-xs font-semibold transition-all duration-300',
                isDone && 'bg-blue-600 text-white',
                isActive && 'bg-blue-600 text-white ring-4 ring-blue-600/20',
                !isDone && !isActive && 'bg-zinc-800 text-zinc-500',
              )}
            >
              {isDone ? <CheckCircle2 className="w-4 h-4" /> : <span>{idx + 1}</span>}
            </div>
            {idx < STEPS.length - 1 && (
              <div
                className={cn(
                  'h-0.5 w-10 sm:w-16 transition-all duration-300',
                  idx < current ? 'bg-blue-600' : 'bg-zinc-800',
                )}
              />
            )}
          </div>
        )
      })}
    </div>
  )
}

function StepOperationFailure({ title, error, retryHint }: {
  title: string
  error: Error
  retryHint: string
}) {
  return (
    <div role="alert" className="flex items-start gap-3 rounded-xl border border-red-500/20 bg-red-500/10 p-4">
      <AlertTriangle className="mt-0.5 size-5 shrink-0 text-red-400" />
      <div className="min-w-0">
        <p className="text-sm font-medium text-red-200">{title}</p>
        <p className="mt-1 break-words font-mono text-xs text-red-300/70">{error.message}</p>
        <p className="mt-2 text-xs text-zinc-400">{retryHint}</p>
      </div>
    </div>
  )
}

// ─── Step 1: Welcome ─────────────────────────────────────────────────────────

function StepWelcome({ onNext, transitioning }: { onNext: () => void; transitioning: boolean }) {
  return (
    <div className="text-center space-y-8">
      <div className="flex justify-center">
        <div className="w-20 h-20 bg-blue-600/10 border border-blue-600/20 rounded-2xl flex items-center justify-center">
          <Server className="w-10 h-10 text-blue-400" />
        </div>
      </div>
      <div className="space-y-3">
        <h1 className="text-3xl font-bold text-white tracking-tight">
          Welcome to HServer Panel
        </h1>
        <p className="text-zinc-400 text-base max-w-md mx-auto leading-relaxed">
          A powerful, self-hosted server management panel. We'll help you get set up in just a few steps.
        </p>
      </div>
      <div className="grid grid-cols-1 sm:grid-cols-3 gap-4 text-left mt-6">
        {[
          { icon: Globe, title: 'Domains & SSL', desc: 'Manage nginx configs, SSL certificates, and PHP-FPM pools' },
          { icon: Shield, title: 'Security', desc: 'Firewall rules, fail2ban, and security scoring out of the box' },
          { icon: LayoutDashboard, title: 'Full Control', desc: 'PM2, databases, mail server, DNS, and real-time monitoring' },
        ].map(({ icon: Icon, title, desc }) => (
          <div key={title} className="bg-zinc-800/50 border border-zinc-700/50 rounded-xl p-4 space-y-2">
            <Icon className="w-5 h-5 text-blue-400" />
            <p className="text-white text-sm font-medium">{title}</p>
            <p className="text-zinc-500 text-xs leading-relaxed">{desc}</p>
          </div>
        ))}
      </div>
      <Button
        onClick={onNext}
        disabled={transitioning}
        size="lg"
        className="bg-blue-600 hover:bg-blue-500 text-white px-8 mt-2"
      >
        Let's get started
        <ArrowRight className="w-4 h-4 ml-2" />
      </Button>
    </div>
  )
}

// ─── Step 2: Server Profile ───────────────────────────────────────────────────

function StepServerProfile({ onNext, onBack, transitioning }: { onNext: () => void; onBack: () => void; transitioning: boolean }) {
  const {
    data: info,
    isLoading,
    isError,
    isFetching,
    refetch,
  } = useQuery<SystemInfo>({
    queryKey: ['system', 'info'],
    queryFn: () => api.get<SystemInfo>('/system/info'),
    staleTime: 60_000,
  })

  const [adminEmail, setAdminEmail] = useState('')
  const [timezone, setTimezone] = useState(
    Intl.DateTimeFormat().resolvedOptions().timeZone,
  )

  const saveMutation = useMutation({
    mutationFn: () =>
      api.put('/settings', {
        ...(adminEmail ? { adminEmail } : {}),
        timezone,
      } as Record<string, string>),
    onSuccess: () => onNext(),
    onError: () => toast.error('Failed to save settings'),
  })

  return (
    <div className="space-y-6">
      <div className="text-center space-y-2">
        <h2 className="text-2xl font-bold text-white">Server Profile</h2>
        <p className="text-zinc-400 text-sm">Confirm your server details and admin settings</p>
      </div>

      {/* Server info strip */}
      <div className="bg-zinc-800/50 border border-zinc-700/50 rounded-xl p-4">
        <p className="text-zinc-500 text-xs font-medium uppercase tracking-wider mb-3">Detected Server</p>
        {isLoading ? (
          <div className="space-y-2">
            {[1, 2, 3].map((i) => (
              <div key={i} className="h-4 bg-zinc-700 rounded animate-pulse" />
            ))}
          </div>
        ) : isError ? (
          <div className="flex items-center justify-between gap-4">
            <p className="text-amber-300 text-sm">Server details could not be detected.</p>
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => refetch()}
              disabled={isFetching}
              className="border-zinc-700 text-zinc-300"
            >
              {isFetching && <Loader2 className="w-3.5 h-3.5 mr-2 animate-spin" />}
              Retry
            </Button>
          </div>
        ) : (
          <div className="grid grid-cols-2 gap-x-6 gap-y-2">
            {[
              ['Hostname', info?.hostname],
              ['OS', info?.os],
              ['Kernel', info?.kernel],
              ['Arch', info?.arch],
              ['Nginx', info?.nginx],
              ['PHP', info?.php?.join(', ')],
            ].map(([label, val]) => (
              <div key={label} className="flex items-center justify-between gap-2">
                <span className="text-zinc-500 text-xs">{label}</span>
                <span className="text-zinc-200 text-xs font-mono truncate">{val ?? '—'}</span>
              </div>
            ))}
          </div>
        )}
      </div>

      {/* Editable fields */}
      <div className="space-y-4">
        <div className="space-y-2">
          <Label className="text-zinc-300">Admin Email</Label>
          <Input
            type="email"
            value={adminEmail}
            onChange={(e) => {
              setAdminEmail(e.target.value)
              saveMutation.reset()
            }}
            placeholder="admin@example.com"
            className="bg-zinc-800 border-zinc-700 text-white placeholder-zinc-500"
          />
          <p className="text-zinc-500 text-xs">Used for system alerts and notifications</p>
        </div>
        <div className="space-y-2">
          <Label className="text-zinc-300">Timezone</Label>
          <Input
            value={timezone}
            onChange={(e) => {
              setTimezone(e.target.value)
              saveMutation.reset()
            }}
            placeholder="Europe/Istanbul"
            className="bg-zinc-800 border-zinc-700 text-white placeholder-zinc-500"
          />
          <p className="text-zinc-500 text-xs">Auto-detected from your browser</p>
        </div>
      </div>

      {saveMutation.isError && (
        <StepOperationFailure
          title="Server settings were not saved"
          error={saveMutation.error}
          retryHint="Review the fields if needed, then press Continue to retry. Your setup step has not advanced."
        />
      )}

      <div className="flex justify-between pt-2">
        <Button variant="ghost" onClick={onBack} disabled={transitioning} className="text-zinc-400 hover:text-white">
          <ArrowLeft className="w-4 h-4 mr-2" />
          Back
        </Button>
        <Button
          onClick={() => saveMutation.mutate()}
          disabled={saveMutation.isPending || transitioning}
          className="bg-blue-600 hover:bg-blue-500 text-white"
        >
          {saveMutation.isPending && <Loader2 className="w-4 h-4 mr-2 animate-spin" />}
          Continue
          <ArrowRight className="w-4 h-4 ml-2" />
        </Button>
      </div>
    </div>
  )
}

// ─── Step 3: Security Checklist ───────────────────────────────────────────────

function StepSecurity({ onNext, onBack, transitioning }: { onNext: () => void; onBack: () => void; transitioning: boolean }) {
  const {
    data: score,
    isLoading,
    isError,
    isFetching,
    refetch,
  } = useQuery<SecurityScore>({
    queryKey: ['security', 'score'],
    queryFn: () => api.get<SecurityScore>('/security/score'),
    staleTime: 30_000,
  })

  const scoreValue = score?.score ?? 0
  const scoreColor =
    scoreValue >= 80 ? 'text-green-400' : scoreValue >= 50 ? 'text-amber-400' : 'text-red-400'
  const scoreBarColor =
    scoreValue >= 80 ? 'bg-green-500' : scoreValue >= 50 ? 'bg-amber-500' : 'bg-red-500'

  return (
    <div className="space-y-6">
      <div className="text-center space-y-2">
        <h2 className="text-2xl font-bold text-white">Security Checklist</h2>
        <p className="text-zinc-400 text-sm">Review your server's current security posture</p>
      </div>

      {/* Score gauge */}
      {isLoading ? (
        <div className="h-24 bg-zinc-800 rounded-xl animate-pulse" />
      ) : isError ? (
        <div className="bg-amber-500/10 border border-amber-500/20 rounded-xl p-5 flex items-center justify-between gap-4">
          <div className="flex items-center gap-3">
            <AlertTriangle className="w-5 h-5 text-amber-400 shrink-0" />
            <div>
              <p className="text-amber-200 text-sm font-medium">Security score is unavailable</p>
              <p className="text-amber-200/60 text-xs mt-1">No risk score was assumed from the failed check.</p>
            </div>
          </div>
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => refetch()}
            disabled={isFetching}
            className="border-amber-500/30 text-amber-200 hover:bg-amber-500/10"
          >
            {isFetching && <Loader2 className="w-3.5 h-3.5 mr-2 animate-spin" />}
            Retry
          </Button>
        </div>
      ) : (
        <div className="bg-zinc-800/50 border border-zinc-700/50 rounded-xl p-5 flex items-center gap-5">
          <div className={cn('text-5xl font-bold tabular-nums', scoreColor)}>
            {scoreValue}
          </div>
          <div className="flex-1 space-y-2">
            <div className="flex items-center justify-between">
              <p className="text-zinc-300 text-sm font-medium">Security Score</p>
              <p className={cn('text-xs font-semibold', scoreColor)}>
                {scoreValue >= 80 ? 'Good' : scoreValue >= 50 ? 'Needs Attention' : 'At Risk'}
              </p>
            </div>
            <div className="h-2 bg-zinc-700 rounded-full overflow-hidden">
              <div
                className={cn('h-full rounded-full transition-all duration-700', scoreBarColor)}
                style={{ width: `${scoreValue}%` }}
              />
            </div>
          </div>
        </div>
      )}

      {/* Check list */}
      <div className="space-y-2">
        {isError
          ? null
          : isLoading
          ? Array.from({ length: 5 }).map((_, i) => (
              <div key={i} className="h-12 bg-zinc-800 rounded-lg animate-pulse" />
            ))
          : score?.checks?.map((check) => (
              <div
                key={check.name}
                className="flex items-center gap-3 bg-zinc-800/50 border border-zinc-700/50 rounded-lg px-4 py-3"
              >
                {check.status === 'pass' ? (
                  <CheckCircle2 className="w-4 h-4 text-green-400 shrink-0" />
                ) : check.status === 'warn' ? (
                  <AlertTriangle className="w-4 h-4 text-amber-400 shrink-0" />
                ) : (
                  <XCircle className="w-4 h-4 text-red-400 shrink-0" />
                )}
                <div className="flex-1 min-w-0">
                  <p className="text-zinc-200 text-sm font-medium">{check.name}</p>
                  <p className="text-zinc-500 text-xs truncate">{check.detail}</p>
                </div>
                <span
                  className={cn(
                    'text-xs font-semibold px-2 py-0.5 rounded-full',
                    check.status === 'pass' && 'bg-green-500/10 text-green-400',
                    check.status === 'warn' && 'bg-amber-500/10 text-amber-400',
                    check.status === 'fail' && 'bg-red-500/10 text-red-400',
                  )}
                >
                  {check.status}
                </span>
              </div>
            ))}
      </div>

      <p className="text-zinc-500 text-xs text-center">
        You can configure these anytime from the Security page.
      </p>

      <div className="flex justify-between pt-2">
        <Button variant="ghost" onClick={onBack} disabled={transitioning} className="text-zinc-400 hover:text-white">
          <ArrowLeft className="w-4 h-4 mr-2" />
          Back
        </Button>
        <div className="flex gap-3">
          <Button variant="outline" onClick={onNext} disabled={transitioning} className="border-zinc-700 text-zinc-400 hover:text-white hover:border-zinc-500">
            Skip for now
          </Button>
          <Button onClick={onNext} disabled={transitioning} className="bg-blue-600 hover:bg-blue-500 text-white">
            Continue
            <ArrowRight className="w-4 h-4 ml-2" />
          </Button>
        </div>
      </div>
    </div>
  )
}

// ─── Step 4: First Domain ─────────────────────────────────────────────────────

function StepFirstDomain({ onNext, onBack, transitioning }: { onNext: () => void; onBack: () => void; transitioning: boolean }) {
  const [domain, setDomain] = useState('')
  const [skipped, setSkipped] = useState(false)

  const createMutation = useMutation({
    mutationFn: () => api.post('/domains', firstDomainRequest(domain)),
    onSuccess: () => {
      toast.success(`Domain ${domain} created`)
      onNext()
    },
    onError: (err: Error) => {
      toast.error(`Failed to create domain: ${err.message}`)
    },
  })

  function handleSkip() {
    setSkipped(true)
    onNext()
  }

  return (
    <div className="space-y-6">
      <div className="text-center space-y-2">
        <h2 className="text-2xl font-bold text-white">Add Your First Domain</h2>
        <p className="text-zinc-400 text-sm">
          Optionally add a domain now — you can always add more later.
        </p>
      </div>

      <div className="bg-zinc-800/50 border border-zinc-700/50 rounded-xl p-5 space-y-4">
        <div className="space-y-2">
          <Label className="text-zinc-300">Domain Name</Label>
          <div className="flex gap-2">
            <div className="flex items-center bg-zinc-700/50 border border-zinc-700 rounded-lg px-3">
              <Globe className="w-4 h-4 text-zinc-500" />
            </div>
            <Input
              value={domain}
              onChange={(e) => {
                setDomain(e.target.value.toLowerCase().trim())
                createMutation.reset()
              }}
              placeholder="example.com"
              className="bg-zinc-800 border-zinc-700 text-white placeholder-zinc-500 flex-1"
              disabled={skipped}
            />
          </div>
          <p className="text-zinc-500 text-xs">
            A static nginx vhost will be created. You can configure SSL and PHP afterwards.
          </p>
        </div>
      </div>

      {createMutation.isError && (
        <StepOperationFailure
          title="The domain was not created"
          error={createMutation.error}
          retryHint="Correct the domain if needed, then press Add Domain to retry or skip this optional step."
        />
      )}

      <div className="flex justify-between pt-2">
        <Button variant="ghost" onClick={onBack} disabled={transitioning} className="text-zinc-400 hover:text-white">
          <ArrowLeft className="w-4 h-4 mr-2" />
          Back
        </Button>
        <div className="flex gap-3">
          <Button
            variant="outline"
            onClick={handleSkip}
            disabled={transitioning}
            className="border-zinc-700 text-zinc-400 hover:text-white hover:border-zinc-500"
          >
            Skip — add later
          </Button>
          <Button
            onClick={() => createMutation.mutate()}
            disabled={!validFirstDomain(domain) || createMutation.isPending || transitioning}
            className="bg-blue-600 hover:bg-blue-500 text-white disabled:opacity-50"
          >
            {createMutation.isPending && <Loader2 className="w-4 h-4 mr-2 animate-spin" />}
            Add Domain
            <ArrowRight className="w-4 h-4 ml-2" />
          </Button>
        </div>
      </div>
    </div>
  )
}

// ─── Step 5: Done ─────────────────────────────────────────────────────────────

function StepDone({
  onFinish,
  isFinishing,
  finishError,
}: {
  onFinish: (target: string) => void
  isFinishing: boolean
  finishError: Error | null
}) {
  return (
    <div className="text-center space-y-8">
      <div className="flex justify-center">
        <div className="w-20 h-20 bg-green-600/10 border border-green-600/20 rounded-2xl flex items-center justify-center">
          <CheckCircle2 className="w-10 h-10 text-green-400" />
        </div>
      </div>

      <div className="space-y-3">
        <h2 className="text-3xl font-bold text-white">You're all set!</h2>
        <p className="text-zinc-400 text-sm max-w-sm mx-auto">
          HServer Panel is configured and ready. Here's what to explore next.
        </p>
      </div>

      <div className="grid grid-cols-1 sm:grid-cols-3 gap-3 text-left">
        {[
          {
            icon: Globe,
            label: 'Add Domain',
            desc: 'Create nginx vhosts, configure SSL',
            path: '/domains',
          },
          {
            icon: Lock,
            label: 'Configure SSL',
            desc: "Issue Let's Encrypt certificates",
            path: '/ssl',
          },
          {
            icon: LayoutDashboard,
            label: 'View Dashboard',
            desc: 'Real-time server metrics',
            path: '/',
          },
        ].map(({ icon: Icon, label, desc, path }) => (
          <button
            key={label}
            onClick={() => onFinish(path)}
            disabled={isFinishing}
            className="bg-zinc-800/50 border border-zinc-700/50 hover:border-blue-600/40 hover:bg-zinc-800 rounded-xl p-4 text-left space-y-1 transition-all group"
          >
            <Icon className="w-5 h-5 text-blue-400 mb-2" />
            <p className="text-white text-sm font-medium group-hover:text-blue-300 transition-colors">{label}</p>
            <p className="text-zinc-500 text-xs">{desc}</p>
          </button>
        ))}
      </div>

      {finishError && (
        <StepOperationFailure
          title="Setup could not be marked complete"
          error={finishError}
          retryHint="Your saved progress is preserved. Choose a destination or press Go to Dashboard to retry."
        />
      )}

      <Button
        onClick={() => onFinish('/')}
        disabled={isFinishing}
        size="lg"
        className="bg-blue-600 hover:bg-blue-500 text-white px-10"
      >
        {isFinishing ? (
          <Loader2 className="w-4 h-4 mr-2 animate-spin" />
        ) : (
          <LayoutDashboard className="w-4 h-4 mr-2" />
        )}
        Go to Dashboard
      </Button>
    </div>
  )
}

// ─── Main Onboarding Page ─────────────────────────────────────────────────────

export default function OnboardingPage() {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const cachedState = queryClient.getQueryData<OnboardingState>(['onboarding'])
  const [step, setStep] = useState<number | null>(() => (
    cachedState ? normalizeOnboardingStep(cachedState.step) : null
  ))
  const [unsavedStep, setUnsavedStep] = useState<number | null>(null)

  const {
    data: savedState,
    isLoading: isStateLoading,
    isError: isStateError,
    isFetching: isStateFetching,
    refetch: refetchState,
  } = useQuery<OnboardingState>({
    queryKey: ['onboarding'],
    queryFn: () => api.get<OnboardingState>('/onboarding'),
    staleTime: 5 * 60 * 1000,
  })

  const activeStep = step ?? normalizeOnboardingStep(savedState?.step)

  const completeMutation = useMutation({
    mutationFn: async (target: string) => {
      await api.post<{ status: string }>('/onboarding', { completed: true, step: 5 })
      return target
    },
    onSuccess: (target) => {
      queryClient.setQueryData(['onboarding'], { completed: true, step: 5 })
      navigate(target, { replace: true })
    },
    onError: () => toast.error('Could not finish setup. Your progress is preserved; please try again.'),
  })

  const saveStepMutation = useMutation({
    mutationFn: (s: number) =>
      api.post<{ status: string }>('/onboarding', { completed: false, step: s }),
    onSuccess: (_, savedStep) => {
      queryClient.setQueryData(['onboarding'], { completed: false, step: savedStep })
      setUnsavedStep((current) => current === savedStep ? null : current)
    },
    onError: (_error, failedStep) => {
      setUnsavedStep(failedStep)
      toast.error('Setup progress could not be saved. This screen remains usable; retry before leaving.')
    },
  })

  function goNext() {
    if (saveStepMutation.isPending) return
    const next = activeStep + 1
    setStep(next)
    setUnsavedStep(null)
    saveStepMutation.mutate(next)
  }

  function goBack() {
    if (saveStepMutation.isPending) return
    const previous = Math.max(0, activeStep - 1)
    setStep(previous)
    setUnsavedStep(null)
    saveStepMutation.mutate(previous)
  }

  function retryProgressSave() {
    if (unsavedStep === null || saveStepMutation.isPending) return
    saveStepMutation.mutate(unsavedStep)
  }

  if (step === null && isStateLoading) {
    return (
      <div className="min-h-screen bg-zinc-950 flex items-center justify-center p-4">
        <div className="flex items-center gap-3 text-zinc-400 text-sm">
          <Loader2 className="w-5 h-5 animate-spin text-blue-400" />
          Loading saved setup progress…
        </div>
      </div>
    )
  }

  if (step === null && isStateError) {
    return (
      <div className="min-h-screen bg-zinc-950 flex items-center justify-center p-4">
        <Card className="w-full max-w-md bg-zinc-900 border-zinc-800 shadow-2xl">
          <CardContent className="p-8 text-center space-y-5">
            <AlertTriangle className="w-10 h-10 text-amber-400 mx-auto" />
            <div className="space-y-2">
              <h1 className="text-xl font-semibold text-white">Setup progress is unavailable</h1>
              <p className="text-sm text-zinc-400">
                HServer could not load the saved onboarding state. No progress was reset.
              </p>
            </div>
            <Button
              onClick={() => refetchState()}
              disabled={isStateFetching}
              className="bg-blue-600 hover:bg-blue-500 text-white"
            >
              {isStateFetching && <Loader2 className="w-4 h-4 mr-2 animate-spin" />}
              Try Again
            </Button>
          </CardContent>
        </Card>
      </div>
    )
  }

  return (
    <div className="min-h-screen bg-zinc-950 flex items-center justify-center p-4">
      <div className="w-full max-w-2xl">
        {/* Logo / brand strip */}
        <div className="text-center mb-8">
          <div className="flex items-center justify-center gap-2 mb-1">
            <Server className="w-5 h-5 text-blue-400" />
            <span className="text-white font-semibold tracking-tight">HServer Panel</span>
          </div>
          <p className="text-zinc-600 text-xs">Setup Wizard</p>
        </div>

        <StepIndicator current={activeStep} />

        {saveStepMutation.isPending && (
          <div role="status" className="mb-4 flex items-center gap-2 rounded-xl border border-blue-500/20 bg-blue-500/[0.06] px-4 py-3 text-xs text-blue-300">
            <Loader2 className="size-3.5 animate-spin" />
            Saving setup progress…
          </div>
        )}

        {unsavedStep !== null && !saveStepMutation.isPending && (
          <div role="alert" className="mb-4 flex flex-col items-start justify-between gap-3 rounded-xl border border-amber-500/25 bg-amber-500/[0.07] px-4 py-3 text-xs text-amber-200 sm:flex-row sm:items-center">
            <div>
              <p className="font-medium">Setup progress for step {unsavedStep + 1} is not saved.</p>
              <p className="mt-1 text-amber-200/60">The current screen remains usable, but reload recovery may return to the last saved step.</p>
            </div>
            <Button type="button" variant="outline" size="sm" onClick={retryProgressSave} className="border-amber-500/30 text-amber-100">
              Retry saving progress
            </Button>
          </div>
        )}

        <Card className="bg-zinc-900 border-zinc-800 shadow-2xl">
          <CardContent className="p-8">
            {activeStep === 0 && <StepWelcome onNext={goNext} transitioning={saveStepMutation.isPending} />}
            {activeStep === 1 && <StepServerProfile onNext={goNext} onBack={goBack} transitioning={saveStepMutation.isPending} />}
            {activeStep === 2 && <StepSecurity onNext={goNext} onBack={goBack} transitioning={saveStepMutation.isPending} />}
            {activeStep === 3 && (
              <StepFirstDomain
                onNext={goNext}
                onBack={goBack}
                transitioning={saveStepMutation.isPending}
              />
            )}
            {activeStep === 4 && (
              <StepDone
                onFinish={(target) => completeMutation.mutate(target)}
                isFinishing={completeMutation.isPending || saveStepMutation.isPending}
                finishError={completeMutation.isError ? completeMutation.error : null}
              />
            )}
          </CardContent>
        </Card>

        <p className="text-center text-zinc-600 text-xs mt-6">
          Step {activeStep + 1} of {STEPS.length}
        </p>
      </div>
    </div>
  )
}
