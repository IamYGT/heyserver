import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Shield, ShieldCheck, ShieldOff, RefreshCw, CheckCircle2, XCircle, AlertCircle } from 'lucide-react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { api } from '@/lib/api'
import { toast } from 'sonner'
import { cn } from '@/lib/utils'
import type { PHPSecurityProfile, PHPPool } from '@/lib/types'

// ─── Security score ring ──────────────────────────────────────────────────────

function ScoreRing({ score }: { score: number }) {
  const radius = 36
  const circumference = 2 * Math.PI * radius
  const strokeDashoffset = circumference - (score / 100) * circumference
  const color = score >= 90 ? '#22c55e' : score >= 70 ? '#f59e0b' : '#ef4444'

  return (
    <div className="relative w-24 h-24">
      <svg width="96" height="96" className="-rotate-90">
        <circle cx="48" cy="48" r={radius} fill="none" stroke="#27272a" strokeWidth="8" />
        <circle
          cx="48" cy="48" r={radius}
          fill="none"
          stroke={color}
          strokeWidth="8"
          strokeLinecap="round"
          strokeDasharray={circumference}
          strokeDashoffset={strokeDashoffset}
          className="transition-all duration-500"
        />
      </svg>
      <div className="absolute inset-0 flex flex-col items-center justify-center">
        <span className="text-white text-xl font-bold">{score}</span>
        <span className="text-zinc-500 text-[10px]">/100</span>
      </div>
    </div>
  )
}

// ─── Security check item ──────────────────────────────────────────────────────

interface SecurityCheck {
  name: string
  status: 'pass' | 'fail' | 'warn'
  current: string
  recommended: string
  description: string
}

function SecurityCheckRow({ check }: { check: SecurityCheck }) {
  const icon = check.status === 'pass'
    ? <CheckCircle2 className="w-4 h-4 text-green-400 flex-shrink-0" />
    : check.status === 'warn'
    ? <AlertCircle className="w-4 h-4 text-amber-400 flex-shrink-0" />
    : <XCircle className="w-4 h-4 text-red-400 flex-shrink-0" />

  return (
    <div className={cn(
      'flex items-start gap-3 px-4 py-3 border-b border-zinc-800/60 last:border-0',
      check.status === 'fail' && 'bg-red-500/5',
      check.status === 'warn' && 'bg-amber-500/5',
    )}>
      {icon}
      <div className="flex-1 min-w-0">
        <p className="text-zinc-200 text-sm font-medium">{check.name}</p>
        <p className="text-zinc-500 text-xs mt-0.5">{check.description}</p>
      </div>
      <div className="text-right flex-shrink-0 space-y-0.5">
        <div className="flex items-center justify-end gap-1.5">
          <span className="text-zinc-500 text-[10px]">current:</span>
          <code className={cn(
            'text-[11px] font-mono px-1.5 py-0.5 rounded',
            check.status === 'pass' ? 'bg-green-500/10 text-green-400'
            : check.status === 'warn' ? 'bg-amber-500/10 text-amber-400'
            : 'bg-red-500/10 text-red-400'
          )}>
            {check.current}
          </code>
        </div>
        {check.status !== 'pass' && (
          <div className="flex items-center justify-end gap-1.5">
            <span className="text-zinc-600 text-[10px]">recommended:</span>
            <code className="text-[11px] font-mono bg-zinc-800 text-zinc-300 px-1.5 py-0.5 rounded">
              {check.recommended}
            </code>
          </div>
        )}
      </div>
    </div>
  )
}

// ─── Types ────────────────────────────────────────────────────────────────────

interface SecurityAssessment {
  score: number
  profile: string
  checks: SecurityCheck[]
}

interface SecurityIssueResponse {
  severity: string
  setting: string
  current: string
  recommended: string
  description: string
}

interface SecurityProfileResponse {
  name: string
  description?: string
  level: string
}

interface SecurityAssessmentResponse {
  score?: {
    score?: number
    level?: string
    issues?: SecurityIssueResponse[]
  }
  profile?: SecurityProfileResponse | null
}

function securityLevel(level: string): PHPSecurityProfile['level'] {
  return level === 'strict' || level === 'permissive' ? level : 'moderate'
}

function normalizeSecurityProfile(profile: SecurityProfileResponse): PHPSecurityProfile {
  const level = securityLevel(profile.level)
  return {
    name: level,
    level,
    score: level === 'strict' ? 100 : level === 'moderate' ? 80 : 50,
    settings: profile.description ? { description: profile.description } : {},
  }
}

function normalizeSecurityAssessment(response: SecurityAssessmentResponse): SecurityAssessment {
  const issues = response.score?.issues ?? []
  return {
    score: response.score?.score ?? 0,
    profile: response.profile?.level ?? response.score?.level ?? 'custom',
    checks: issues.map(issue => ({
      name: issue.setting,
      status: issue.severity === 'critical' ? 'fail' : 'warn',
      current: issue.current,
      recommended: issue.recommended,
      description: issue.description,
    })),
  }
}

// ─── SecurityTab ──────────────────────────────────────────────────────────────

interface SecurityTabProps {
  selectedVersion: string
  pools: PHPPool[]
}

export default function SecurityTab({ selectedVersion, pools }: SecurityTabProps) {
  const queryClient = useQueryClient()
  const [selectedPoolChoice, setSelectedPoolChoice] = useState('')
  const versionPools = pools.filter(p => p.version === selectedVersion)
  const selectedPool = versionPools.some(pool => pool.name === selectedPoolChoice)
    ? selectedPoolChoice
    : versionPools[0]?.name ?? ''

  const profilesQuery = useQuery<PHPSecurityProfile[]>({
    queryKey: ['php', 'security', 'profiles'],
    queryFn: async () => (await api.get<SecurityProfileResponse[]>('/php/security/profiles') ?? []).map(normalizeSecurityProfile),
  })
  const profiles = profilesQuery.data ?? []

  const assessmentQuery = useQuery<SecurityAssessment>({
    queryKey: ['php', 'security', selectedVersion, selectedPool],
    queryFn: async () => {
      const response = await api.get<SecurityAssessmentResponse>(`/php/security/${selectedVersion}/${encodeURIComponent(selectedPool)}`)
      return normalizeSecurityAssessment(response)
    },
    enabled: !!selectedPool,
  })
  const assessment = assessmentQuery.data

  const applyProfileMutation = useMutation({
    mutationFn: (profileName: string) =>
      api.post(`/php/security/${selectedVersion}/${encodeURIComponent(selectedPool)}`, { profile: profileName }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['php', 'security', selectedVersion, selectedPool] })
      toast.success('Security profile applied')
    },
    onError: () => toast.error('Failed to apply security profile'),
  })

  const isLoading = assessmentQuery.isLoading || profilesQuery.isLoading
  const loadError = profilesQuery.error ?? assessmentQuery.error

  const passCount = assessment?.checks.filter(c => c.status === 'pass').length ?? 0
  const failCount = assessment?.checks.filter(c => c.status === 'fail').length ?? 0
  const warnCount = assessment?.checks.filter(c => c.status === 'warn').length ?? 0

  return (
    <div className="space-y-5">
      {/* Domain selector */}
      <div className="flex items-center gap-3">
        <span className="text-zinc-500 text-xs">Domain:</span>
        <select
          value={selectedPool}
          onChange={e => setSelectedPoolChoice(e.target.value)}
          className="bg-zinc-800 border border-zinc-700 text-zinc-300 text-xs rounded-md px-2 py-1.5 h-8"
        >
          {versionPools.map(p => (
            <option key={p.name} value={p.name}>{p.name}</option>
          ))}
        </select>
        <Badge className="bg-zinc-800 border-zinc-700 text-zinc-400 text-xs">PHP {selectedVersion}</Badge>
      </div>

      {versionPools.length === 0 ? (
        <Card className="border-zinc-800 bg-zinc-900">
          <CardContent className="p-6 text-center">
            <ShieldOff className="mx-auto size-5 text-zinc-500" />
            <p className="mt-2 text-sm text-zinc-300">No PHP-FPM pool is available for PHP {selectedVersion}.</p>
          </CardContent>
        </Card>
      ) : isLoading ? (
        <div className="space-y-3">
          <Skeleton className="h-32 bg-zinc-800 rounded-xl" />
          <Skeleton className="h-64 bg-zinc-800 rounded-xl" />
        </div>
      ) : loadError ? (
        <Card className="border-red-500/25 bg-red-500/[0.05]">
          <CardContent className="p-6 text-center">
            <AlertCircle className="mx-auto size-5 text-red-400" />
            <p className="mt-2 text-sm text-red-300">Could not load PHP security information.</p>
            <p className="mt-1 text-xs text-zinc-600">{loadError.message}</p>
            <Button type="button" variant="outline" size="sm" className="mt-4 border-red-500/30 text-red-200" onClick={() => { void profilesQuery.refetch(); void assessmentQuery.refetch() }}>
              <RefreshCw className="mr-2 size-3.5" />Retry
            </Button>
          </CardContent>
        </Card>
      ) : (
        <>
          {/* Score + Summary */}
          <Card className="bg-zinc-900 border-zinc-800">
            <CardContent className="p-5">
              <div className="flex items-center gap-6 flex-wrap">
                <ScoreRing score={assessment?.score ?? 0} />
                <div className="flex-1 min-w-0 space-y-3">
                  <div>
                    <p className="text-white font-semibold text-base">Security Assessment</p>
                    <p className="text-zinc-500 text-sm mt-0.5">
                      Profile: <span className="text-zinc-300 capitalize">{assessment?.profile ?? '—'}</span>
                    </p>
                  </div>
                  <div className="flex gap-4">
                    <div className="flex items-center gap-1.5">
                      <CheckCircle2 className="w-3.5 h-3.5 text-green-400" />
                      <span className="text-green-400 text-sm font-semibold">{passCount}</span>
                      <span className="text-zinc-500 text-xs">pass</span>
                    </div>
                    <div className="flex items-center gap-1.5">
                      <AlertCircle className="w-3.5 h-3.5 text-amber-400" />
                      <span className="text-amber-400 text-sm font-semibold">{warnCount}</span>
                      <span className="text-zinc-500 text-xs">warn</span>
                    </div>
                    <div className="flex items-center gap-1.5">
                      <XCircle className="w-3.5 h-3.5 text-red-400" />
                      <span className="text-red-400 text-sm font-semibold">{failCount}</span>
                      <span className="text-zinc-500 text-xs">fail</span>
                    </div>
                  </div>
                </div>
              </div>
            </CardContent>
          </Card>

          {/* Profile Selector */}
          <div>
            <p className="text-zinc-400 text-xs uppercase tracking-wider mb-2">Apply Profile</p>
            <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
              {profiles.map(profile => (
                <Card
                  key={profile.name}
                  className={cn(
                    'bg-zinc-900 border-zinc-800 cursor-pointer transition-all duration-200 hover:border-zinc-600 hover:shadow-md',
                    assessment?.profile === profile.name && 'border-blue-500/60 bg-blue-500/5 ring-2 ring-blue-500/20'
                  )}
                >
                  <CardContent className="p-4">
                    <div className="flex items-start justify-between mb-2">
                      <div>
                        <div className="flex items-center gap-2">
                          {profile.level === 'strict'
                            ? <ShieldCheck className="w-4 h-4 text-green-400" />
                            : profile.level === 'moderate'
                            ? <Shield className="w-4 h-4 text-amber-400" />
                            : <ShieldOff className="w-4 h-4 text-red-400" />
                          }
                          <p className="text-white text-sm font-semibold capitalize">{profile.name}</p>
                        </div>
                        <Badge className={cn(
                          'text-[10px] border mt-1',
                          profile.level === 'strict'     ? 'bg-green-500/10 text-green-400 border-green-500/20'
                          : profile.level === 'moderate' ? 'bg-amber-500/10 text-amber-400 border-amber-500/20'
                          : 'bg-red-500/10 text-red-400 border-red-500/20'
                        )}>
                          {profile.level}
                        </Badge>
                      </div>
                      <span className={cn(
                        'text-lg font-bold',
                        profile.score >= 90 ? 'text-green-400'
                        : profile.score >= 70 ? 'text-amber-400' : 'text-red-400'
                      )}>
                        {profile.score}
                      </span>
                    </div>
                    <Button
                      className={cn(
                        'w-full h-7 text-xs mt-2',
                        assessment?.profile === profile.name
                          ? 'bg-blue-600 hover:bg-blue-500 text-white'
                          : 'bg-zinc-800 hover:bg-zinc-700 text-zinc-300 border border-zinc-700'
                      )}
                      onClick={() => applyProfileMutation.mutate(profile.name)}
                      disabled={applyProfileMutation.isPending}
                    >
                      {applyProfileMutation.isPending && applyProfileMutation.variables === profile.name
                        ? <RefreshCw className="w-3 h-3 animate-spin" />
                        : assessment?.profile === profile.name ? 'Active' : 'Apply'
                      }
                    </Button>
                  </CardContent>
                </Card>
              ))}
            </div>
          </div>

          {/* Checks */}
          {assessment && assessment.checks.length > 0 && (
            <Card className="bg-zinc-900 border-zinc-800 overflow-hidden">
              <CardHeader className="py-3 px-4 border-b border-zinc-800">
                <CardTitle className="text-sm text-zinc-400 font-medium">
                  Security Checks
                </CardTitle>
              </CardHeader>
              <CardContent className="p-0">
                {[
                  { status: 'fail' as const, label: 'Failed' },
                  { status: 'warn' as const, label: 'Warnings' },
                  { status: 'pass' as const, label: 'Passed' },
                ].map(({ status, label }) => {
                  const checks = assessment.checks.filter(c => c.status === status)
                  if (!checks.length) return null
                  return (
                    <div key={status}>
                      <div className="px-4 py-2 bg-zinc-800/30">
                        <p className="text-zinc-500 text-[11px] uppercase tracking-wider">
                          {label} ({checks.length})
                        </p>
                      </div>
                      {checks.map((check, i) => (
                        <SecurityCheckRow key={i} check={check} />
                      ))}
                    </div>
                  )
                })}
              </CardContent>
            </Card>
          )}
        </>
      )}
    </div>
  )
}
