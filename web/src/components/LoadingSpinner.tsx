export default function LoadingSpinner() {
  return (
    <div className="flex min-h-screen items-center justify-center bg-zinc-950">
      <div className="flex flex-col items-center gap-3">
        <div className="h-8 w-8 animate-spin rounded-full border-2 border-zinc-700 border-t-zinc-400" />
        <span className="text-sm text-zinc-400">Loading…</span>
      </div>
    </div>
  )
}
