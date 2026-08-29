interface QuotaBarProps {
  usage: number
  limit: number
  label?: string
  showLabels?: boolean
}

function formatTB(bytes: number): string {
  if (!bytes || bytes <= 0) return '—'
  const tb = bytes / 1024 ** 4
  return tb >= 0.1 ? `${tb.toFixed(2)} TB` : `${(bytes / 1024 ** 3).toFixed(1)} GB`
}

export default function QuotaBar({ usage, limit, label = 'Drive kotası', showLabels = true }: QuotaBarProps) {
  const pct = limit > 0 ? Math.min(100, (usage / limit) * 100) : 0
  const barColor =
    pct > 90 ? 'bg-red-500' : pct > 75 ? 'bg-amber-500' : pct > 50 ? 'bg-blue-500' : 'bg-emerald-500'

  return (
    <div className="space-y-2">
      {showLabels && (
        <div className="flex items-center justify-between text-xs">
          <span className="text-zinc-500">{label}</span>
          <span className="text-zinc-300 font-medium tabular-nums">
            {formatTB(usage)}
            {limit > 0 && (
              <span className="text-zinc-500 font-normal"> / {formatTB(limit)}</span>
            )}
            {limit > 0 && (
              <span className="text-zinc-500 ml-1.5">({pct.toFixed(1)}%)</span>
            )}
          </span>
        </div>
      )}
      <div
        className="h-2 rounded-full bg-zinc-800 overflow-hidden ring-1 ring-zinc-700/50"
        role="progressbar"
        aria-valuenow={Math.round(pct)}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-label={label}
      >
        <div
          className={`h-full rounded-full transition-all duration-500 ${barColor}`}
          style={{ width: `${Math.max(pct, pct > 0 ? 2 : 0)}%` }}
        />
      </div>
    </div>
  )
}
