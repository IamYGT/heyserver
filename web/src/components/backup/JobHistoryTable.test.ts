import { describe, expect, it } from 'vitest'
import { historyPhaseLabel } from './phaseLabels'

describe('historyPhaseLabel', () => {
  it('does not describe a failed terminal job as completed', () => {
    expect(historyPhaseLabel({ status: 'failed', phase: 'done' })).toBe('Hata ile sonlandı')
    expect(historyPhaseLabel({ status: 'failed' })).toBe('Hata ile sonlandı')
  })

  it('keeps the successful terminal label for completed jobs', () => {
    expect(historyPhaseLabel({ status: 'completed', phase: 'done' })).toBe('Tamamlandı')
  })

  it('keeps the current phase while a job is still running', () => {
    expect(historyPhaseLabel({ status: 'running', phase: 'gdrive_upload' })).toBe('Drive yükleme')
  })
})
