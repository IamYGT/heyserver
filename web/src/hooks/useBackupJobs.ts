import { useCallback, useEffect, useRef } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { consumeAuthenticatedEventStream } from '@/lib/sse'
import type { BackupJob } from '@/lib/types'

const STORAGE_KEY = 'hserver-watched-jobs'

function loadWatched(): string[] {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    return raw ? (JSON.parse(raw) as string[]) : []
  } catch {
    return []
  }
}

function saveWatched(ids: string[]) {
  localStorage.setItem(STORAGE_KEY, JSON.stringify(ids.slice(0, 20)))
}

function mergeJob(list: BackupJob[], next: BackupJob): BackupJob[] {
  const idx = list.findIndex((j) => j.id === next.id || j.jobId === next.jobId)
  if (idx >= 0) {
    const copy = [...list]
    copy[idx] = { ...copy[idx], ...next }
    return copy
  }
  return [next, ...list]
}

function jobIdOf(job: BackupJob): string {
  return job.id ?? job.jobId ?? ''
}

function notifyJobDone(job: BackupJob) {
  const label = job.type === 'gdrive_upload'
    ? 'Drive yüklemesi'
    : job.type === 'gdrive_restore'
      ? 'Drive indirmesi'
      : job.type === 'restore'
        ? 'Geri yükleme'
        : 'Yedekleme'

  if (job.status === 'completed') {
    toast.success(job.message || `${label} tamamlandı`)
  } else if (job.status === 'failed') {
    toast.error(job.error || job.message || `${label} başarısız`)
  }
}

export function useBackupJobs() {
  const queryClient = useQueryClient()
  const streamRef = useRef<AbortController | null>(null)
  const reconnectTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const openSSERef = useRef<() => void>(() => {})
  const reconnectAttempts = useRef(0)
  const statusRef = useRef<Map<string, BackupJob['status']>>(new Map())
  const watchedRef = useRef<string[]>(loadWatched())
  const lastGdriveStatusInvalidate = useRef(0)
  const GDRIVE_STATUS_THROTTLE_MS = 30_000

  const jobsQuery = useQuery<{ jobs: BackupJob[] }>({
    queryKey: ['backup-jobs'],
    queryFn: () => api.get<{ jobs: BackupJob[] }>('/backups/jobs?hours=24'),
    refetchInterval: typeof ReadableStream === 'undefined' ? 3000 : 8000,
  })
  const data = jobsQuery.data
  const jobs = data?.jobs ?? []

  useEffect(() => {
    if (data?.jobs) {
      for (const job of data.jobs) {
        const id = jobIdOf(job)
        if (id) statusRef.current.set(id, job.status)
      }
    }
  }, [data])

  const maybeToastCompletion = useCallback((job: BackupJob) => {
    const id = jobIdOf(job)
    if (!id) return

    const prev = statusRef.current.get(id)
    const wasActive = prev === 'pending' || prev === 'running'
    const watched = watchedRef.current.includes(id)
    const isTerminal = job.status === 'completed' || job.status === 'failed'

    if (wasActive && isTerminal && (watched || job.source === 'manual')) {
      notifyJobDone(job)
    }
    statusRef.current.set(id, job.status)
  }, [])

  const maybeInvalidateGdriveStatus = useCallback(
    (job: BackupJob) => {
      const isGdrive = job.type === 'gdrive_upload' || job.type === 'gdrive_restore'
      if (!isGdrive) return

      const terminal = job.status === 'completed' || job.status === 'failed'
      const now = Date.now()
      if (terminal) {
        lastGdriveStatusInvalidate.current = now
        queryClient.invalidateQueries({ queryKey: ['gdrive-status'] })
        queryClient.invalidateQueries({ queryKey: ['gdrive-remote'] })
        return
      }
      if (now - lastGdriveStatusInvalidate.current < GDRIVE_STATUS_THROTTLE_MS) return
      lastGdriveStatusInvalidate.current = now
      queryClient.invalidateQueries({ queryKey: ['gdrive-status'] })
    },
    [queryClient],
  )

  const applyJob = useCallback(
    (job: BackupJob) => {
      maybeToastCompletion(job)
      queryClient.setQueryData<{ jobs: BackupJob[] }>(['backup-jobs'], (current) => ({
        jobs: mergeJob(current?.jobs ?? [], job),
      }))
      queryClient.invalidateQueries({ queryKey: ['backups'] })
      maybeInvalidateGdriveStatus(job)
    },
    [queryClient, maybeToastCompletion, maybeInvalidateGdriveStatus],
  )

  const watchJob = useCallback((jobId: string) => {
    const ids = loadWatched()
    if (!ids.includes(jobId)) {
      const next = [jobId, ...ids]
      saveWatched(next)
      watchedRef.current = next
    }
  }, [])

  const closeSSE = useCallback(() => {
    if (reconnectTimer.current) {
      clearTimeout(reconnectTimer.current)
      reconnectTimer.current = null
    }
    if (streamRef.current) {
      streamRef.current.abort()
      streamRef.current = null
    }
  }, [])

  const openSSE = useCallback(() => {
    if (typeof ReadableStream === 'undefined') return

    if (streamRef.current) {
      streamRef.current.abort()
      streamRef.current = null
    }

    const controller = new AbortController()
    streamRef.current = controller
    const onMessage = (data: string) => {
      try {
        const job = JSON.parse(data) as BackupJob
        if (job.id || job.jobId) {
          applyJob(job)
        }
      } catch {
        // ignore malformed frames
      }
    }
    const reconnect = () => {
      if (controller.signal.aborted) return
      streamRef.current = null
      const delay = Math.min(1000 * 2 ** reconnectAttempts.current, 30_000)
      reconnectAttempts.current += 1
      reconnectTimer.current = setTimeout(() => {
        openSSERef.current()
      }, delay)
    }
    void consumeAuthenticatedEventStream(
      '/backups/jobs/stream',
      controller.signal,
      () => { reconnectAttempts.current = 0 },
      onMessage,
    ).then(reconnect).catch(reconnect)
  }, [applyJob])

  useEffect(() => {
    openSSERef.current = openSSE
  }, [openSSE])

  useEffect(() => {
    openSSE()
    return () => closeSSE()
  }, [openSSE, closeSSE])

  const activeJobs = jobs.filter((j) => j.status === 'pending' || j.status === 'running')
  const historyJobs = jobs.filter((j) => j.status === 'completed' || j.status === 'failed')

  return {
    jobs,
    activeJobs,
    historyJobs,
    isLoading: jobsQuery.isLoading,
    isError: jobsQuery.isError,
    error: jobsQuery.error,
    isFetching: jobsQuery.isFetching,
    refetch: jobsQuery.refetch,
    watchJob,
    applyJob,
  }
}
