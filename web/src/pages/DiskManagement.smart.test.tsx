import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { SmartHealthCard } from './DiskManagement'

describe('Root disk SMART readiness', () => {
  it('shows an explicit observation failure instead of an endless skeleton', () => {
    const retry = vi.fn()

    render(<SmartHealthCard smart={undefined} loading={false} error={new Error('lsblk timed out')} retry={retry} retrying={false} />)

    expect(screen.getByText('SMART health could not be observed')).toBeInTheDocument()
    expect(screen.getByText('lsblk timed out')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Retry detection' }))
    expect(retry).toHaveBeenCalledOnce()
  })

  it('explains unsupported virtual or multi-disk root storage without claiming health', () => {
    render(<SmartHealthCard
      smart={{
        available: false,
        healthy: false,
        device: '/dev/md0',
        status: 'UNAVAILABLE',
        message: 'The root filesystem spans multiple physical disks, so Heyserver will not choose one arbitrarily.',
      }}
      loading={false}
      error={null}
      retry={() => {}}
      retrying={false}
    />)

    expect(screen.getByText('SMART health is unavailable for the root storage')).toBeInTheDocument()
    expect(screen.getByText(/will not choose one arbitrarily/)).toBeInTheDocument()
    expect(screen.queryByText('PASSED')).not.toBeInTheDocument()
  })

  it('shows the observed physical device with a definite SMART result', () => {
    render(<SmartHealthCard
      smart={{
        available: true,
        healthy: true,
        device: '/dev/nvme0n1',
        status: 'PASSED',
        model: 'Example NVMe',
      }}
      loading={false}
      error={null}
      retry={() => {}}
      retrying={false}
    />)

    expect(screen.getByText('PASSED')).toBeInTheDocument()
    expect(screen.getByText('/dev/nvme0n1')).toBeInTheDocument()
    expect(screen.getByText('Example NVMe')).toBeInTheDocument()
  })
})
