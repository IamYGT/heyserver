/** The only optional-integration availability values allowed on the wire. */
export type IntegrationState = 'not_configured' | 'unavailable' | 'healthy'

export const INTEGRATION_NOT_CONFIGURED: IntegrationState = 'not_configured'
export const INTEGRATION_UNAVAILABLE: IntegrationState = 'unavailable'
export const INTEGRATION_HEALTHY: IntegrationState = 'healthy'

/**
 * The remediation component uses a visual state with a hyphen. Keep that
 * value separate from the snake_case API value, and keep runtime `stopped`
 * outside this optional-integration contract.
 */
export type IntegrationPresentationState = 'not-configured' | 'unavailable' | 'healthy'

export type IntegrationPresentationTone = 'neutral' | 'warning' | 'healthy'

export interface IntegrationStatePresentation {
  state: IntegrationPresentationState
  label: string
  tone: IntegrationPresentationTone
}

export function isIntegrationState(value: unknown): value is IntegrationState {
  return value === INTEGRATION_NOT_CONFIGURED || value === INTEGRATION_UNAVAILABLE || value === INTEGRATION_HEALTHY
}

/**
 * Normalize untrusted API data to an exact wire value. Presentation-only
 * values (`not-configured`) and runtime states (`stopped`) are rejected rather
 * than silently treated as an available integration.
 */
export function normalizeIntegrationState(value: unknown): IntegrationState | null {
  if (typeof value !== 'string') return null
  const normalized = value.trim().toLowerCase()
  return isIntegrationState(normalized) ? normalized : null
}

/**
 * Derive availability from an explicit caller-provided observation. A
 * configured integration without a successful observation is unavailable;
 * configuration or a URL alone cannot produce `healthy`.
 */
export function integrationStateFromObservation(configured: boolean, successful: boolean): IntegrationState {
  if (!configured) return INTEGRATION_NOT_CONFIGURED
  return successful ? INTEGRATION_HEALTHY : INTEGRATION_UNAVAILABLE
}

/** Adapt a canonical wire state to the visual state expected by UI surfaces. */
export function integrationStatePresentation(state: IntegrationState): IntegrationStatePresentation {
  switch (state) {
    case INTEGRATION_NOT_CONFIGURED:
      return { state: 'not-configured', label: 'Not configured', tone: 'neutral' }
    case INTEGRATION_UNAVAILABLE:
      return { state: 'unavailable', label: 'Unavailable', tone: 'warning' }
    case INTEGRATION_HEALTHY:
      return { state: 'healthy', label: 'Healthy', tone: 'healthy' }
    default:
      return { state: 'unavailable', label: 'Unavailable', tone: 'warning' }
  }
}
