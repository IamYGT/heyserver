import type { LucideIcon } from 'lucide-react'
import { Button } from '@/components/ui/button'

interface EmptyStateProps {
  /** Lucide icon component */
  icon: LucideIcon
  title: string
  description?: string
  actionLabel?: string
  onAction?: () => void
  secondaryLabel?: string
  onSecondary?: () => void
  className?: string
}

/**
 * Reusable empty state component.
 *
 * Usage:
 *   <EmptyState
 *     icon={Globe}
 *     title="No domains configured"
 *     description="Add your first nginx vhost to get started."
 *     actionLabel="Add Domain"
 *     onAction={() => setCreateOpen(true)}
 *   />
 */
export function EmptyState({
  icon: Icon,
  title,
  description,
  actionLabel,
  onAction,
  secondaryLabel,
  onSecondary,
  className = '',
}: EmptyStateProps) {
  return (
    <div className={`flex flex-col items-center justify-center py-20 px-6 text-center ${className}`}>
      <div className="w-14 h-14 rounded-full bg-zinc-800/80 border border-zinc-700 flex items-center justify-center mb-4">
        <Icon className="w-6 h-6 text-zinc-500" />
      </div>
      <h3 className="text-white font-semibold text-base mb-1">{title}</h3>
      {description && (
        <p className="text-zinc-500 text-sm max-w-xs mb-6">{description}</p>
      )}
      {(actionLabel || secondaryLabel) && (
        <div className="flex items-center gap-3 flex-wrap justify-center mt-2">
          {actionLabel && onAction && (
            <Button onClick={onAction} className="bg-blue-600 hover:bg-blue-500 text-white">
              {actionLabel}
            </Button>
          )}
          {secondaryLabel && onSecondary && (
            <Button variant="ghost" onClick={onSecondary} className="text-zinc-400 hover:text-white">
              {secondaryLabel}
            </Button>
          )}
        </div>
      )}
    </div>
  )
}

export default EmptyState
