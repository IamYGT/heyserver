import { Skeleton } from '@/components/ui/skeleton'

/**
 * Full-page skeleton shown while page-level data loads.
 * Mimics the general layout: header + stat cards + content table.
 */
export function PageSkeleton() {
  return (
    <div className="space-y-6 animate-in fade-in duration-300">
      {/* Page header */}
      <div className="flex items-center justify-between gap-4 flex-wrap">
        <div className="space-y-2">
          <Skeleton className="h-6 w-40 bg-zinc-800" />
          <Skeleton className="h-4 w-56 bg-zinc-800/60" />
        </div>
        <Skeleton className="h-9 w-32 bg-zinc-800" />
      </div>

      {/* Stat cards */}
      <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
        {Array.from({ length: 4 }).map((_, i) => (
          <div key={i} className="bg-zinc-900 border border-zinc-800 rounded-xl p-5 space-y-3">
            <div className="flex items-center justify-between">
              <Skeleton className="h-4 w-24 bg-zinc-800" />
              <Skeleton className="h-8 w-8 rounded-lg bg-zinc-800" />
            </div>
            <Skeleton className="h-8 w-20 bg-zinc-800" />
            <Skeleton className="h-3 w-32 bg-zinc-800/60" />
          </div>
        ))}
      </div>

      {/* Main content table */}
      <div className="bg-zinc-900 border border-zinc-800 rounded-xl overflow-hidden">
        <div className="flex items-center gap-6 px-5 py-4 border-b border-zinc-800">
          {Array.from({ length: 5 }).map((_, i) => (
            <Skeleton key={i} className="h-4 flex-1 bg-zinc-800" />
          ))}
        </div>
        <div className="divide-y divide-zinc-800/60">
          {Array.from({ length: 7 }).map((_, i) => (
            <div key={i} className="flex items-center gap-6 px-5 py-4" style={{ opacity: 1 - i * 0.1 }}>
              <div className="flex items-center gap-3 flex-1 min-w-0">
                <Skeleton className="h-8 w-8 rounded-lg bg-zinc-800 flex-shrink-0" />
                <div className="space-y-1.5 flex-1">
                  <Skeleton className="h-4 w-32 bg-zinc-800" />
                  <Skeleton className="h-3 w-24 bg-zinc-800/60" />
                </div>
              </div>
              <Skeleton className="h-5 w-16 rounded-full bg-zinc-800" />
              <Skeleton className="h-4 w-20 bg-zinc-800 hidden md:block" />
              <Skeleton className="h-4 w-16 bg-zinc-800 hidden lg:block" />
              <div className="flex gap-1.5">
                <Skeleton className="h-7 w-7 rounded-md bg-zinc-800" />
                <Skeleton className="h-7 w-7 rounded-md bg-zinc-800" />
              </div>
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}

export default PageSkeleton
