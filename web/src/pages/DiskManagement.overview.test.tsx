import { fireEvent, render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useDiskOverview, useSmartInfo } from '@/hooks/useDisk'
import { OverviewTab } from './DiskManagement'

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

function smartUnavailable() {
  return {
    data: {
      available: false,
      healthy: false,
      device: 'overlay',
      status: 'UNAVAILABLE',
      message: 'Root storage is virtual.',
    },
    isLoading: false,
    isFetching: false,
    error: null,
    refetch: vi.fn(),
  } as unknown as ReturnType<typeof useSmartInfo>
}

describe('Disk overview readiness', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(useSmartInfo).mockReturnValue(smartUnavailable())
  })

  it('does not convert an inventory failure into an empty server', () => {
    const refetch = vi.fn()
    vi.mocked(useDiskOverview).mockReturnValue({
      data: undefined,
      isLoading: false,
      isFetching: false,
      isError: true,
      error: new Error('lsblk inventory timed out'),
      refetch,
    } as unknown as ReturnType<typeof useDiskOverview>)

    render(<OverviewTab onExplore={() => {}} onCleanup={() => {}} />)

    expect(screen.getByText('Disk overview could not be observed')).toBeInTheDocument()
    expect(screen.getByText('lsblk inventory timed out')).toBeInTheDocument()
    expect(screen.queryByText('The host returned a successful inventory with no mounted block devices.')).not.toBeInTheDocument()
    expect(screen.getByText('Root Disk SMART Health')).toBeInTheDocument()
    fireEvent.click(screen.getAllByRole('button', { name: 'Retry detection' })[0])
    expect(refetch).toHaveBeenCalledOnce()
  })

  it('labels a successful observed-empty inventory explicitly', () => {
    vi.mocked(useDiskOverview).mockReturnValue({
      data: { partitions: [], ioStats: [], totalSize: 0, totalUsed: 0, totalFree: 0 },
      isLoading: false,
      isFetching: false,
      isError: false,
      error: null,
      refetch: vi.fn(),
    } as unknown as ReturnType<typeof useDiskOverview>)

    render(<OverviewTab onExplore={() => {}} onCleanup={() => {}} />)

    expect(screen.getByText('The host returned a successful inventory with no mounted block devices.')).toBeInTheDocument()
    expect(screen.queryByText('Disk overview could not be observed')).not.toBeInTheDocument()
  })
})
