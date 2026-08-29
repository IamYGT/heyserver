import { describe, expect, it } from 'vitest'
import {
  autoCwd,
  autoWebRoot,
  dnsCapabilityDescription,
  normalizeVhostsRoot,
} from './domainProvisioning'

describe('domain provisioning defaults', () => {
  it('builds roots below the installation-owned base path', () => {
    expect(autoWebRoot('example.com', '/srv/hserver/sites/')).toBe('/srv/hserver/sites/example.com/public_html')
    expect(autoWebRoot('app.example.com', '/srv/hserver/sites')).toBe('/srv/hserver/sites/example.com/app.example.com/public_html')
    expect(autoCwd('app.example.com', '/srv/hserver/sites')).toBe('/srv/hserver/sites/example.com/app.example.com')
    expect(normalizeVhostsRoot('')).toBe('')
  })

  it('does not invent a site root before installation configuration is available', () => {
    expect(autoWebRoot('example.com')).toBe('')
    expect(autoCwd('example.com')).toBe('')
  })

  it('describes healthy and unavailable provider states honestly', () => {
    expect(dnsCapabilityDescription({
      provider: 'cloudflare',
      status: 'healthy',
      origin: '203.0.113.25',
      recordType: 'A',
      proxied: true,
      message: 'ready',
    })).toBe('A record → 203.0.113.25 (proxied)')

    expect(dnsCapabilityDescription({
      provider: 'cloudflare',
      status: 'unavailable',
      proxied: false,
      message: 'provider did not respond',
    })).toBe('Unavailable — provider did not respond')
  })
})
