import { describe, expect, it } from 'vitest'
import { firstDomainRequest, normalizeOnboardingStep, validFirstDomain } from './onboarding'

describe('onboarding state', () => {
  it('resumes only within the five-step wizard', () => {
    expect(normalizeOnboardingStep(3)).toBe(3)
    expect(normalizeOnboardingStep(-1)).toBe(0)
    expect(normalizeOnboardingStep(99)).toBe(4)
    expect(normalizeOnboardingStep(undefined)).toBe(0)
  })

  it('builds the actual provider-neutral domain API contract', () => {
    expect(validFirstDomain('Portal.Example.com')).toBe(true)
    expect(validFirstDomain('not a domain')).toBe(false)
    expect(firstDomainRequest(' Portal.Example.com ')).toEqual({
      domain: 'portal.example.com',
      type: 'static',
      wwwRedirect: false,
      issueSSL: false,
      createDnsRecord: false,
    })
  })
})
