import { Link, useLocation } from 'react-router-dom'
import { ChevronRight, Home } from 'lucide-react'
import { cn } from '@/lib/utils'
import { managedNavigationTarget, type ManagedServerID } from '@/lib/serverNavigation'
import { buildCrumbs } from '@/components/breadcrumbUtils'

// ─── Component ─────────────────────────────────────────────────────────────────

export function Breadcrumb({ selectedServer }: { selectedServer: ManagedServerID }) {
  const location = useLocation()
  const homeHref = managedNavigationTarget(selectedServer, '/') ?? '/'
  const crumbs = buildCrumbs(location.pathname, homeHref)

  // Don't render if we're on the root and it's just "Home"
  if (crumbs.length === 1 && crumbs[0].href === homeHref) {
    return null
  }

  return (
    <nav
      aria-label="Breadcrumb"
      className="flex items-center gap-1 text-xs text-zinc-500"
    >
      {crumbs.map((crumb, idx) => (
        <span key={crumb.href} className="flex items-center gap-1">
          {idx > 0 && (
            <ChevronRight className="w-3 h-3 text-zinc-700 flex-shrink-0" />
          )}
          {crumb.isLast ? (
            <span className="text-zinc-300 font-medium flex items-center gap-1">
              {idx === 0 && <Home className="w-3 h-3" />}
              {idx !== 0 && crumb.label}
            </span>
          ) : (
            <Link
              to={crumb.href}
              className={cn(
                'flex items-center gap-1 transition-colors hover:text-zinc-200',
                crumb.isLast ? 'text-zinc-300' : 'text-zinc-500 hover:text-zinc-300'
              )}
            >
              {idx === 0 ? <Home className="w-3 h-3" /> : crumb.label}
            </Link>
          )}
        </span>
      ))}
    </nav>
  )
}
