import { fireEvent, render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import {
  useDirList,
  useDiskAnalysisStart,
  useDiskAnalysisStatus,
  useDiskMounts,
  useLargestFiles,
} from '@/hooks/useDisk'
import { DeepAnalysisCard, MountsTab, SpaceAnalysisTab } from './DiskManagement'

vi.mock('@/hooks/useDisk', () => ({
  useDiskOverview: vi.fn(),
  useSmartInfo: vi.fn(),
  useDirList: vi.fn(),
  useLargestFiles: vi.fn(),
  useCleanupScan: vi.fn(),
  useCleanupExecute: vi.fn(),
  useDiskMounts: vi.fn(),
  useDiskAnalysisStart: vi.fn(),
  useDiskAnalysisStatus: vi.fn(),
}))

describe('Disk state fidelity', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(useDiskAnalysisStatus).mockReturnValue({
      data: { status: 'idle', message: 'No analysis has run.', entries: [] },
      isLoading: false,
      isFetching: false,
      isError: false,
      error: null,
      refetch: vi.fn(),
    } as unknown as ReturnType<typeof useDiskAnalysisStatus>)
    vi.mocked(useDiskAnalysisStart).mockReturnValue({
      mutate: vi.fn(),
      isPending: false,
    } as unknown as ReturnType<typeof useDiskAnalysisStart>)
    vi.mocked(useLargestFiles).mockReturnValue({
      data: undefined,
      isFetching: false,
      error: null,
    } as unknown as ReturnType<typeof useLargestFiles>)
  })

  it('pauses deep analysis when the current job state is unavailable', () => {
    const refetch = vi.fn()
    vi.mocked(useDiskAnalysisStatus).mockReturnValue({
      data: undefined,
      isLoading: false,
      isFetching: false,
      isError: true,
      error: new Error('analysis worker timed out'),
      refetch,
    } as unknown as ReturnType<typeof useDiskAnalysisStatus>)

    render(<DeepAnalysisCard />)

    expect(screen.getByText('Deep analysis status could not be observed')).toBeInTheDocument()
    expect(screen.getByText('analysis worker timed out')).toBeInTheDocument()
    expect(screen.queryByText('No deep analysis has run yet.')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Start analysis' })).toBeDisabled()
    fireEvent.click(screen.getByRole('button', { name: 'Retry detection' }))
    expect(refetch).toHaveBeenCalledOnce()
  })

  it('does not convert a directory listing failure into an empty directory', () => {
    const refetch = vi.fn()
    vi.mocked(useDirList).mockReturnValue({
      data: undefined,
      isLoading: false,
      isFetching: false,
      isError: true,
      error: new Error('permission denied for /srv/data'),
      refetch,
    } as unknown as ReturnType<typeof useDirList>)

    render(<SpaceAnalysisTab initialPath="/srv/data" />)

    expect(screen.getByText('Directory contents could not be observed')).toBeInTheDocument()
    expect(screen.getByText('permission denied for /srv/data')).toBeInTheDocument()
    expect(screen.getByText('Directory unavailable')).toBeInTheDocument()
    expect(screen.queryByText('Empty directory')).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Retry detection' }))
    expect(refetch).toHaveBeenCalledOnce()
  })

  it('does not convert an fstab failure into an empty mount inventory', () => {
    const refetch = vi.fn()
    vi.mocked(useDiskMounts).mockReturnValue({
      data: undefined,
      isLoading: false,
      isFetching: false,
      isError: true,
      error: new Error('fstab read failed'),
      refetch,
    } as unknown as ReturnType<typeof useDiskMounts>)

    render(<MountsTab />)

    expect(screen.getByText('Mount configuration could not be observed')).toBeInTheDocument()
    expect(screen.getByText('fstab read failed')).toBeInTheDocument()
    expect(screen.queryByText('No mount entries found')).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Retry detection' }))
    expect(refetch).toHaveBeenCalledOnce()
  })
})
