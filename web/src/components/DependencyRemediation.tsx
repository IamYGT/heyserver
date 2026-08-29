import type { ReactNode } from 'react'
import { AlertTriangle, RefreshCw, Wrench } from 'lucide-react'
import { Button } from '@/components/ui/button'

interface DependencyRemediationProps {
  title: string
  summary: string
  steps: ReactNode[]
  error?: string
  retry: () => void
  retrying?: boolean
  retryLabel?: string
  state?: 'not-configured' | 'stopped' | 'unavailable'
}

const stateStyle = {
  'not-configured': 'border-amber-500/25 bg-amber-500/[0.06]',
  stopped: 'border-blue-500/25 bg-blue-500/[0.06]',
  unavailable: 'border-red-500/25 bg-red-500/[0.05]',
}

export function DependencyRemediation({
  title,
  summary,
  steps,
  error,
  retry,
  retrying = false,
  retryLabel = 'Retry detection',
  state = 'unavailable',
}: DependencyRemediationProps) {
  return (
    <div className={`rounded-xl border p-4 sm:p-5 ${stateStyle[state]}`}>
      <div className="flex flex-col justify-between gap-4 sm:flex-row sm:items-start">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            {state === 'unavailable' ? <AlertTriangle className="size-4 shrink-0 text-red-400" /> : <Wrench className="size-4 shrink-0 text-amber-300" />}
            <p className="text-sm font-medium text-zinc-100">{title}</p>
          </div>
          <p className="mt-2 text-xs leading-5 text-zinc-400">{summary}</p>
          {error && <p className="mt-2 break-words rounded bg-zinc-950/60 px-2.5 py-2 font-mono text-[11px] text-zinc-500">{error}</p>}
        </div>
        <Button type="button" variant="outline" size="sm" onClick={retry} disabled={retrying} className="shrink-0 border-zinc-700 text-zinc-200">
          <RefreshCw className={`size-3.5 ${retrying ? 'animate-spin' : ''}`} />
          {retryLabel}
        </Button>
      </div>
      <ol className="mt-4 grid gap-2 text-xs text-zinc-300 sm:grid-cols-3">
        {steps.map((step, index) => (
          <li key={index} className="flex gap-2 rounded-lg border border-zinc-800 bg-zinc-950/50 p-3 leading-5">
            <span className="flex size-5 shrink-0 items-center justify-center rounded-full bg-zinc-800 text-[10px] font-semibold text-zinc-300">{index + 1}</span>
            <span>{step}</span>
          </li>
        ))}
      </ol>
    </div>
  )
}
