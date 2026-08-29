import { describe, expect, it, vi } from 'vitest'
import { gdriveOAuthState, openGDriveOAuthPopup } from './gdriveOAuth'

describe('Google Drive OAuth popup', () => {
  it('opens the popup before awaiting the OAuth start request', async () => {
    let popupOpened = false
    const replace = vi.fn()
    const close = vi.fn()

    await openGDriveOAuthPopup({
      openWindow: () => {
        popupOpened = true
        return { location: { replace }, close }
      },
      startOAuth: async () => {
        expect(popupOpened).toBe(true)
        return { authUrl: 'https://accounts.google.com/o/oauth2/auth', state: 'state-1' }
      },
    })

    expect(replace).toHaveBeenCalledWith('https://accounts.google.com/o/oauth2/auth')
    expect(close).not.toHaveBeenCalled()
  })

  it('closes the placeholder popup when OAuth start fails', async () => {
    const close = vi.fn()

    await expect(openGDriveOAuthPopup({
      openWindow: () => ({ location: { replace: vi.fn() }, close }),
      startOAuth: async () => { throw new Error('start failed') },
    })).rejects.toThrow('start failed')

    expect(close).toHaveBeenCalledOnce()
  })

  it('accepts only a non-empty gdrive oauth state payload', () => {
    expect(gdriveOAuthState({ type: 'gdrive-oauth', state: ' state-1 ' })).toBe('state-1')
    expect(gdriveOAuthState({ type: 'other', state: 'state-1' })).toBeNull()
    expect(gdriveOAuthState({ type: 'gdrive-oauth', state: '  ' })).toBeNull()
    expect(gdriveOAuthState(null)).toBeNull()
  })
})
