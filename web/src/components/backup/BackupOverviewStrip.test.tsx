import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import BackupOverviewStrip from './BackupOverviewStrip'

describe('BackupOverviewStrip', () => {
  it('does not describe unavailable schedule or Drive state as disabled', () => {
    render(
      <BackupOverviewStrip
        backupCount={2}
        schedule={null}
        scheduleState="unavailable"
        driveState="unavailable"
      />,
    )

    expect(screen.getByText('Yerel yedek').parentElement).toHaveTextContent('2 dosya')
    expect(screen.getByText('Zamanlama').parentElement).toHaveTextContent('Kullanılamıyor')
    expect(screen.getByText('Uzak depo').parentElement).toHaveTextContent('Kullanılamıyor')
    expect(screen.getByText('Otomatik yükleme').parentElement).toHaveTextContent('Bilinmiyor')
    expect(screen.queryByText('Bağlı değil')).not.toBeInTheDocument()
    expect(screen.queryByText('Kapalı')).not.toBeInTheDocument()
  })

  it('renders observed schedule and connected Drive values', () => {
    render(
      <BackupOverviewStrip
        backupCount={1}
        schedule={{ frequency: 'daily', time: '03:00', retention_count: 7, retention_days: 7, cron: '0 3 * * *', type: 'full', rawLine: 'observed' }}
        scheduleState="ready"
        driveState="connected"
        driveEmail="operator@example.com"
        autoUpload
      />,
    )

    expect(screen.getByText('Zamanlama').parentElement).toHaveTextContent('Günlük 03:00')
    expect(screen.getByText('Uzak depo').parentElement).toHaveTextContent('operator@example.com')
    expect(screen.getByText('Otomatik yükleme').parentElement).toHaveTextContent('Açık')
  })
})
