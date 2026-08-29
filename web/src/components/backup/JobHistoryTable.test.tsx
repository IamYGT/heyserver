import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import JobHistoryTable from './JobHistoryTable'

describe('JobHistoryTable failure state', () => {
  it('does not describe an unavailable job inventory as empty', () => {
    const onRetry = vi.fn()

    render(
      <JobHistoryTable
        jobs={[]}
        error={new Error('Backup jobs API unavailable')}
        onRetry={onRetry}
      />,
    )

    expect(screen.getByText('Yedekleme iş geçmişi yüklenemedi.')).toBeInTheDocument()
    expect(screen.getByText('Backup jobs API unavailable')).toBeInTheDocument()
    expect(screen.queryByText('Henüz kayıtlı iş yok.')).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Tekrar dene' }))
    expect(onRetry).toHaveBeenCalledOnce()
  })
})
