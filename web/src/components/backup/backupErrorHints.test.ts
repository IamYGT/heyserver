import { describe, expect, it } from 'vitest'
import { backupOperationHint } from './backupErrorHints'

describe('backupOperationHint', () => {
  it('turns a backup capacity block into concrete scope and storage actions', () => {
    const hint = backupOperationHint(
      'backup blocked: 103.5 GB source data requires about 209.0 GB free space for a full backup, but only 133.8 GB is available',
    )

    expect(hint).toContain('yalnız gerekli siteleri seçin')
    expect(hint).toContain('sadece veritabanı yedeği alın')
    expect(hint).toContain('gerekli boş alanı sağlayın')
  })

  it('keeps the OAuth recovery action for an expired Drive token', () => {
    expect(backupOperationHint('oauth token is not connected')).toContain('OAuth ile yeniden bağlanın')
  })
})
