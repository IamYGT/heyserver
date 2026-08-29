import type { DomainCreateRequest } from './domainProvisioning'

export interface OnboardingState {
  completed: boolean
  step: number
}

const lastOnboardingStep = 4
const domainPattern = /^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)+$/

export function normalizeOnboardingStep(step: number | undefined): number {
  if (!Number.isInteger(step)) return 0
  return Math.min(lastOnboardingStep, Math.max(0, step ?? 0))
}

export function validFirstDomain(domain: string): boolean {
  return domainPattern.test(domain.trim().toLowerCase())
}

export function firstDomainRequest(domain: string): DomainCreateRequest {
  return {
    domain: domain.trim().toLowerCase(),
    type: 'static',
    wwwRedirect: false,
    issueSSL: false,
    createDnsRecord: false,
  }
}
