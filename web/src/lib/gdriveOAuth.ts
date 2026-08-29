import { api } from '@/lib/api'

export const GDRIVE_OAUTH_CHANNEL = 'hserver-gdrive-oauth'

export interface GDriveOAuthPayload {
  type: 'gdrive-oauth'
  state: string
}

interface OAuthStartResponse {
  authUrl: string
  state: string
}

interface PopupHandle {
  location: { replace: (url: string) => void }
  close: () => void
}

interface OpenGDriveOAuthOptions {
  openWindow?: () => PopupHandle | null
  startOAuth?: () => Promise<OAuthStartResponse>
}

export function gdriveOAuthState(payload: unknown): string | null {
  if (!payload || typeof payload !== 'object') return null
  const candidate = payload as Partial<GDriveOAuthPayload>
  if (candidate.type !== 'gdrive-oauth' || typeof candidate.state !== 'string') return null
  const state = candidate.state.trim()
  return state || null
}

/**
 * Open a blank popup synchronously in the user's click event, then navigate it
 * after the API returns the Google authorization URL. Opening only after the
 * async request is a common popup-blocker failure mode.
 */
export async function openGDriveOAuthPopup(options: OpenGDriveOAuthOptions = {}): Promise<OAuthStartResponse> {
  const popup = (options.openWindow ?? (() => window.open(
    'about:blank',
    'hserver-gdrive-oauth',
    'popup=yes,width=600,height=700',
  ) as PopupHandle | null))()

  if (!popup) {
    throw new Error('Popup engellendi — tarayıcıda popup izni verin')
  }

  try {
    const result = await (options.startOAuth ?? (() =>
      api.post<OAuthStartResponse>('/backups/gdrive/oauth/start')))()
    popup.location.replace(result.authUrl)
    return result
  } catch (error) {
    popup.close()
    throw error
  }
}
