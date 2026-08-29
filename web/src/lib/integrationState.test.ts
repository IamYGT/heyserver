import { describe, expect, it } from 'vitest'
import {
  integrationStateFromObservation,
  integrationStatePresentation,
  isIntegrationState,
  normalizeIntegrationState,
  type IntegrationState,
} from './integrationState'

describe('optional integration wire state', () => {
  it('accepts and canonicalizes the three exact wire values', () => {
    expect(normalizeIntegrationState(' NOT_CONFIGURED ')).toBe('not_configured')
    expect(normalizeIntegrationState('Unavailable')).toBe('unavailable')
    expect(normalizeIntegrationState('healthy')).toBe('healthy')
    expect(isIntegrationState('not_configured')).toBe(true)
  })

  it('rejects the visual and runtime states that are not wire values', () => {
    expect(normalizeIntegrationState('not-configured')).toBeNull()
    expect(normalizeIntegrationState('stopped')).toBeNull()
    expect(normalizeIntegrationState('unknown')).toBeNull()
    expect(normalizeIntegrationState(undefined)).toBeNull()
    expect(isIntegrationState('stopped')).toBe(false)
  })

  it('requires an explicit successful observation before returning healthy', () => {
    expect(integrationStateFromObservation(false, false)).toBe('not_configured')
    expect(integrationStateFromObservation(true, false)).toBe('unavailable')
    expect(integrationStateFromObservation(true, true)).toBe('healthy')
    expect(integrationStateFromObservation(false, true)).toBe('not_configured')
  })

  it('adapts wire states to presentation states without changing the wire contract', () => {
    const cases: Array<[IntegrationState, string]> = [
      ['not_configured', 'not-configured'],
      ['unavailable', 'unavailable'],
      ['healthy', 'healthy'],
    ]

    for (const [state, presentationState] of cases) {
      expect(integrationStatePresentation(state)).toMatchObject({ state: presentationState })
    }
    expect(integrationStatePresentation('not_configured')).not.toMatchObject({ state: 'stopped' })
  })
})
